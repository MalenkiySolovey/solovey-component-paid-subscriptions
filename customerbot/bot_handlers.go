package customerbot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	commerce "github.com/MalenkiySolovey/solovey-component-paid-subscriptions/store"
	"gorm.io/gorm"

	paidcore "github.com/MalenkiySolovey/solovey-component-paid-subscriptions"
	paidprovider "github.com/MalenkiySolovey/solovey-component-paid-subscriptions/provider"
)

func (b *Handler) cmdStart(ctx context.Context, chatID int64, from *User, l Language) {
	_, err := b.ClientID(from.ID)
	if err != nil {
		// Only a genuine "not found" may lead to auto-registration. A transient
		// DB error must NOT be treated as unbound (that would auto-create and
		// rebind a new client, orphaning an existing subscription).
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			b.Warn("client lookup failed", err)
			_ = b.Send.Message(ctx, chatID, Text(l, "error"), nil)
			return
		}
		if b.TryAutoRegister(ctx, chatID, from, l) {
			return
		}
		_ = b.Send.Message(ctx, chatID, Text(l, "not_linked"), nil)
		return
	}
	greeting := Text(l, "greeting")
	if custom, _ := b.Greeting(); strings.TrimSpace(custom) != "" {
		greeting = truncateRunes(custom, 4096)
	}
	_ = b.Send.Message(ctx, chatID, greeting, MenuKeyboard(l))
}

// cmdPaymentMenu opens the "Payment" submenu (buy/renew, my purchases, refund).
func (b *Handler) cmdPaymentMenu(ctx context.Context, chatID int64, l Language) {
	_ = b.Send.Message(ctx, chatID, Text(l, "payment_title"), b.paymentMenuKeyboard(l))
}

// cmdMyOrders lists the requesting user's own orders (read-only history).
func (b *Handler) cmdMyOrders(ctx context.Context, chatID int64, tgID int64, l Language) {
	if _, err := b.ClientID(tgID); err != nil {
		_ = b.Send.Message(ctx, chatID, Text(l, "not_linked"), nil)
		return
	}
	orders, err := commerce.OrdersForTelegramUser(b.DB, tgID, 20)
	if err != nil {
		_ = b.Send.Message(ctx, chatID, Text(l, "error"), nil)
		return
	}
	if len(orders) == 0 {
		_ = b.Send.Message(ctx, chatID, Text(l, "orders_none"), b.backToPaymentKeyboard(l))
		return
	}
	chunks := chunkText(b.buildOrdersText(orders, l), 4000)
	for i, chunk := range chunks {
		if i == len(chunks)-1 {
			_ = b.Send.Message(ctx, chatID, chunk, b.backToPaymentKeyboard(l))
		} else {
			_ = b.Send.Message(ctx, chatID, chunk, nil)
		}
	}
}

// cmdRefundMenu lists the user's refundable (paid) orders as tappable buttons.
func (b *Handler) cmdRefundMenu(ctx context.Context, chatID int64, tgID int64, l Language) {
	if _, err := b.ClientID(tgID); err != nil {
		_ = b.Send.Message(ctx, chatID, Text(l, "not_linked"), nil)
		return
	}
	orders, err := commerce.RefundableOrdersForTelegramUser(b.DB, tgID, 20)
	if err != nil {
		_ = b.Send.Message(ctx, chatID, Text(l, "error"), nil)
		return
	}
	if len(orders) == 0 {
		_ = b.Send.Message(ctx, chatID, Text(l, "refund_none"), b.backToPaymentKeyboard(l))
		return
	}
	names := b.tariffNameMap()
	var rows [][]InlineButton
	for i := range orders {
		o := orders[i]
		rows = append(rows, []InlineButton{{Text: refundOrderButtonLabel(&o, names), CallbackData: fmt.Sprintf("refund:%d", o.Id)}})
	}
	rows = append(rows, []InlineButton{{Text: Text(l, "menu_back"), CallbackData: "payment"}})
	_ = b.Send.Message(ctx, chatID, Text(l, "refund_choose"), &Keyboard{InlineKeyboard: rows})
}

// handleRefundRequest processes a refund button. Stars are refunded
// programmatically through the shared payment coordinator; every
// other provider cannot be refunded via the Bot API, so it sends the admin a
// request. It never acts on another user's order.
func (b *Handler) handleRefundRequest(ctx context.Context, chatID int64, tgID int64, orderID uint, l Language) {
	order, err := commerce.GetOrder(b.DB, orderID)
	if err != nil {
		return
	}
	if order.TelegramUserId != tgID {
		b.AuditCrossUser(tgID, orderID, "refund")
		return
	}
	if order.Status != paidcore.StatusPaid && order.Status != paidcore.StatusRefundPending {
		_ = b.Send.Message(ctx, chatID, Text(l, "refund_not_eligible"), b.backToPaymentKeyboard(l))
		return
	}
	if err := paidcore.ValidateOrderGrant(*order); err != nil {
		_ = b.Send.Message(ctx, chatID, Text(l, "refund_not_eligible"), b.backToPaymentKeyboard(l))
		return
	}
	if order.Provider != string(paidprovider.ProviderStars) {
		b.Notify("paidsub_refund_request", map[string]string{
			"orderId":  fmt.Sprintf("%d", order.Id),
			"clientId": fmt.Sprintf("%d", order.ClientId),
			"provider": order.Provider,
		})
		_ = b.Send.Message(ctx, chatID, Text(l, "refund_requested"), b.backToPaymentKeyboard(l))
		return
	}
	// Stars: the admin policy (paidSubRefundRevoke) decides rollback; the user
	// does not choose, to prevent buy → refund → keep-using abuse.
	revoke, err := b.RefundRevokes()
	if err != nil {
		b.Warn("read refund revoke policy failed", err)
		_ = b.Send.Message(ctx, chatID, Text(l, "error"), nil)
		return
	}
	if _, rerr := b.Refund(ctx, order.Id, revoke); rerr != nil {
		b.Warn("refundStarPayment failed; manual refund needed", nil)
		b.Notify("paidsub_refund_failed", map[string]string{
			"orderId":  fmt.Sprintf("%d", order.Id),
			"clientId": fmt.Sprintf("%d", order.ClientId),
		})
		_ = b.Send.Message(ctx, chatID, Text(l, "refund_requested"), b.backToPaymentKeyboard(l))
		return
	}
	b.Notify("paidsub_refunded", map[string]string{
		"orderId":  fmt.Sprintf("%d", order.Id),
		"clientId": fmt.Sprintf("%d", order.ClientId),
	})
	_ = b.Send.Message(ctx, chatID, Text(l, "refund_done"), b.backToPaymentKeyboard(l))
}
