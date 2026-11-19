package customerbot

import (
	"context"
	"fmt"
	"strings"

	paidcore "github.com/MalenkiySolovey/solovey-component-paid-subscriptions"
	paidprovider "github.com/MalenkiySolovey/solovey-component-paid-subscriptions/provider"
	commerce "github.com/MalenkiySolovey/solovey-component-paid-subscriptions/store"
)

func (b *Handler) cmdBuy(ctx context.Context, chatID int64, tgID int64, l Language) {
	if _, err := b.ClientID(tgID); err != nil {
		_ = b.Send.Message(ctx, chatID, Text(l, "not_linked"), nil)
		return
	}
	tariffs, _ := commerce.ListEnabledTariffs(b.DB)
	var rows [][]InlineButton
	for i := range tariffs {
		t := tariffs[i]
		if len(b.enabledProvidersForTariff(&t)) == 0 {
			continue
		}
		rows = append(rows, []InlineButton{{Text: tariffButtonLabel(&t), CallbackData: fmt.Sprintf("tariff:%d", t.Id)}})
	}
	if len(rows) == 0 {
		_ = b.Send.Message(ctx, chatID, Text(l, "buy_none"), nil)
		return
	}
	_ = b.Send.Message(ctx, chatID, Text(l, "buy_title"), &Keyboard{InlineKeyboard: rows})
}

func (b *Handler) handleTariffSelect(ctx context.Context, chatID int64, tgID int64, tariffID uint, l Language) {
	t, err := commerce.GetTariff(b.DB, tariffID)
	if err != nil || !t.Enabled {
		_ = b.Send.Message(ctx, chatID, Text(l, "buy_none"), nil)
		return
	}
	provs := b.enabledProvidersForTariff(t)
	if len(provs) == 0 {
		_ = b.Send.Message(ctx, chatID, Text(l, "buy_none"), nil)
		return
	}
	if len(provs) == 1 {
		b.startPurchase(ctx, chatID, tgID, t, provs[0], l)
		return
	}
	var rows [][]InlineButton
	for _, prov := range provs {
		rows = append(rows, []InlineButton{{Text: prov.Title(string(l)), CallbackData: fmt.Sprintf("pay:%d:%s", t.Id, prov.Kind())}})
	}
	_ = b.Send.Message(ctx, chatID, Text(l, "buy_choose_provider"), &Keyboard{InlineKeyboard: rows})
}

func (b *Handler) handlePay(ctx context.Context, chatID int64, tgID int64, tariffID uint, kind string, l Language) {
	t, err := commerce.GetTariff(b.DB, tariffID)
	if err != nil || !t.Enabled {
		_ = b.Send.Message(ctx, chatID, Text(l, "buy_none"), nil)
		return
	}
	prov := b.Provider(paidprovider.ProviderKind(kind))
	if prov == nil {
		_ = b.Send.Message(ctx, chatID, Text(l, "buy_none"), nil)
		return
	}
	b.startPurchase(ctx, chatID, tgID, t, prov, l)
}

func (b *Handler) startPurchase(ctx context.Context, chatID int64, tgID int64, t *paidcore.Tariff, prov paidprovider.PaymentProvider, l Language) {
	client, err := b.ClientID(tgID)
	if err != nil {
		_ = b.Send.Message(ctx, chatID, Text(l, "not_linked"), nil)
		return
	}
	_, inv, err := b.Purchase(ctx, client, t.Id, prov.Kind(), tgID)
	if err != nil {
		b.Warn("create order failed", err)
		_ = b.Send.Message(ctx, chatID, Text(l, "pay_invoice_failed"), nil)
		return
	}
	switch inv.Method {
	case paidprovider.InvoiceTelegramNative:
		if err := b.Send.Invoice(ctx, chatID, inv); err != nil {
			b.Warn("sendInvoice failed", err)
			_ = b.Send.Message(ctx, chatID, Text(l, "pay_invoice_failed"), nil)
		}
	case paidprovider.InvoiceURL:
		kb := &Keyboard{InlineKeyboard: [][]InlineButton{{{Text: Text(l, "pay_open"), URL: inv.PayURL}}}}
		_ = b.Send.Message(ctx, chatID, Text(l, "pay_open_hint"), kb)
	case paidprovider.InvoiceManualLink:
		var order *paidcore.PaymentOrder
		// Re-fetch the freshly created order id for the manual button.
		order, _ = commerce.FindOrderByPayload(b.DB, inv.Payload)
		var rows [][]InlineButton
		rows = append(rows, []InlineButton{{Text: Text(l, "pay_open"), URL: inv.PayURL}})
		if order != nil {
			rows = append(rows, []InlineButton{{Text: Text(l, "pay_manual_btn"), CallbackData: fmt.Sprintf("paid:%d", order.Id)}})
		}
		_ = b.Send.Message(ctx, chatID, Text(l, "pay_open_hint"), &Keyboard{InlineKeyboard: rows})
	}
}

func (b *Handler) handleManualPaid(ctx context.Context, chatID int64, tgID int64, orderID uint, l Language) {
	order, err := commerce.GetOrder(b.DB, orderID)
	if err != nil {
		return
	}
	if order.TelegramUserId != tgID {
		b.AuditCrossUser(tgID, orderID, "manual_paid") // never act on another user's order
		return
	}
	b.Notify("paidsub_manual_claim", map[string]string{
		"orderId":  fmt.Sprintf("%d", order.Id),
		"clientId": fmt.Sprintf("%d", order.ClientId),
	})
	_ = b.Send.Message(ctx, chatID, Text(l, "pay_manual_sent"), nil)
}

// ---- payment confirmation (Telegram-native) ----

func (b *Handler) HandlePreCheckout(ctx context.Context, q *PreCheckoutQuery) {
	if q == nil || ctx.Err() != nil {
		return
	}
	order, err := commerce.FindOrderByPayload(b.DB, q.InvoicePayload)
	ok := err == nil &&
		telegramNativeOrder(order) && order.GrantSnapshot &&
		order.Status == paidcore.StatusPending &&
		q.TotalAmount == order.Amount &&
		strings.EqualFold(q.Currency, order.Currency) &&
		(order.TelegramUserId == 0 || q.From.ID == order.TelegramUserId)
	if ok {
		_ = b.Send.AnswerPreCheckout(ctx, q.ID, true, "")
		return
	}
	_ = b.Send.AnswerPreCheckout(ctx, q.ID, false, "Order is no longer valid")
}

func (b *Handler) HandleSuccessfulPayment(ctx context.Context, m *Message) {
	if m == nil || m.From == nil || m.SuccessfulPayment == nil || ctx.Err() != nil {
		return
	}
	l := pickLanguage(m.From.LanguageCode)
	sp := m.SuccessfulPayment
	order, err := commerce.FindOrderByPayload(b.DB, sp.InvoicePayload)
	if err != nil {
		b.Warn("successful_payment for unknown order", nil)
		return
	}
	if !telegramNativeOrder(order) {
		return
	}
	if sp.TotalAmount != order.Amount || !strings.EqualFold(sp.Currency, order.Currency) {
		b.Warn("payment amount/currency mismatch; refusing renewal", nil)
		if err := commerce.MarkOrderFailed(b.DB, order.Id); err != nil {
			b.Warn("mark mismatched payment failed", err)
		}
		b.Notify("paidsub_payment_mismatch", map[string]string{
			"orderId": fmt.Sprintf("%d", order.Id),
		})
		return
	}
	// Defence in depth: the payer must be the Telegram user the order was created
	// for (the payload + pending status are the primary gate).
	if order.TelegramUserId != 0 && m.From.ID != order.TelegramUserId {
		b.Warn("successful_payment from unexpected telegram user; refusing renewal", nil)
		if err := commerce.MarkOrderFailed(b.DB, order.Id); err != nil {
			b.Warn("mark unexpected payer failed", err)
		}
		b.Notify("paidsub_payment_mismatch", map[string]string{
			"orderId": fmt.Sprintf("%d", order.Id),
		})
		return
	}
	charge := sp.TelegramPaymentChargeID
	if charge == "" && order.Provider != string(paidprovider.ProviderStars) {
		charge = sp.ProviderPaymentChargeID
	}
	if charge == "" {
		if err := commerce.ReviewProviderOrders(b.DB, []uint{order.Id}); err != nil {
			b.Warn("review missing payment charge", err)
		}
		return
	}
	applied, err := b.ApplyPayment(ctx, order.Id, "tg:"+charge)
	if err != nil {
		b.Warn("apply paid order failed", err)
		_ = b.Send.Message(ctx, m.Chat.ID, Text(l, "error"), nil)
		return
	}
	if applied {
		_ = b.Send.Message(ctx, m.Chat.ID, Text(l, "pay_success"), MenuKeyboard(l))
	}
}

func telegramNativeOrder(order *paidcore.PaymentOrder) bool {
	switch paidprovider.ProviderKind(order.Provider) {
	case paidprovider.ProviderStars, paidprovider.ProviderYooKassa, paidprovider.ProviderStripe, paidprovider.ProviderPayMaster:
		return true
	}
	return false
}

// ---- helpers ----

func tariffButtonLabel(t *paidcore.Tariff) string {
	price := ""
	switch {
	case t.Price > 0:
		price = fmt.Sprintf("%.2f %s", float64(t.Price)/100, t.Currency)
	case t.StarsAmount > 0:
		price = fmt.Sprintf("%d \u2b50", t.StarsAmount)
	}
	if price == "" {
		return t.Name
	}
	return fmt.Sprintf("%s: %s", t.Name, price)
}

// formatOrderAmount renders an order amount: Telegram Stars (XTR) are whole
// units; every other currency is stored in minor units (e.g. kopeks/cents).
func formatOrderAmount(amount int64, currency string) string {
	if currency == "XTR" {
		return fmt.Sprintf("%d \u2b50", amount)
	}
	return fmt.Sprintf("%.2f %s", float64(amount)/100, currency)
}
