package customerbot

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	paidcore "github.com/MalenkiySolovey/solovey-component-paid-subscriptions"
	commerce "github.com/MalenkiySolovey/solovey-component-paid-subscriptions/store"
)

func MenuKeyboard(l Language) *Keyboard {
	return &Keyboard{InlineKeyboard: [][]InlineButton{
		{{Text: Text(l, "menu_links"), CallbackData: "links"}, {Text: Text(l, "menu_qr"), CallbackData: "qr"}},
		{{Text: Text(l, "menu_stats"), CallbackData: "stats"}, {Text: Text(l, "menu_payment"), CallbackData: "payment"}},
		{{Text: Text(l, "menu_help"), CallbackData: "help"}},
	}}
}

// paymentMenuKeyboard is the "Payment" submenu: buy/renew, my purchases, refund.
func (b *Handler) paymentMenuKeyboard(l Language) *Keyboard {
	return &Keyboard{InlineKeyboard: [][]InlineButton{
		{{Text: Text(l, "menu_buy"), CallbackData: "buy"}},
		{{Text: Text(l, "menu_orders"), CallbackData: "orders"}},
		{{Text: Text(l, "menu_refund"), CallbackData: "refund"}},
		{{Text: Text(l, "menu_back"), CallbackData: "menu"}},
	}}
}

// backToPaymentKeyboard is a single "Back" button returning to the payment menu.
func (b *Handler) backToPaymentKeyboard(l Language) *Keyboard {
	return &Keyboard{InlineKeyboard: [][]InlineButton{
		{{Text: Text(l, "menu_back"), CallbackData: "payment"}},
	}}
}

// tariffNameMap returns tariffId → name for labelling orders (best-effort).
func (b *Handler) tariffNameMap() map[uint]string {
	names := map[uint]string{}
	all, err := commerce.ListTariffs(b.DB)
	if err != nil {
		return names
	}
	for i := range all {
		names[all[i].Id] = all[i].Name
	}
	return names
}

func (b *Handler) buildOrdersText(orders []paidcore.PaymentOrder, l Language) string {
	names := b.tariffNameMap()
	var sb strings.Builder
	sb.WriteString(Text(l, "orders_title") + "\n")
	for i := range orders {
		o := orders[i]
		date := ""
		if o.CreatedAt > 0 {
			date = time.Unix(o.CreatedAt, 0).Format("2006-01-02")
		}
		sb.WriteString(fmt.Sprintf("\n• %s: %s\n  %s · %s",
			orderTariffName(&o, names),
			formatOrderAmount(o.Amount, o.Currency),
			orderStatusLabel(o.Status, l),
			date,
		))
	}
	return strings.TrimSpace(sb.String())
}

func orderTariffName(o *paidcore.PaymentOrder, names map[uint]string) string {
	if name := names[o.TariffId]; name != "" {
		return name
	}
	return "#" + strconv.FormatUint(uint64(o.Id), 10)
}

func refundOrderButtonLabel(o *paidcore.PaymentOrder, names map[uint]string) string {
	return fmt.Sprintf("%s: %s", orderTariffName(o, names), formatOrderAmount(o.Amount, o.Currency))
}

// orderStatusLabel localizes a status, falling back to the raw value.
func orderStatusLabel(status string, l Language) string {
	key := "order_status_" + status
	if v := Text(l, key); v != key {
		return v
	}
	return status
}

// ---- helpers ----

func parseCommand(text string) (cmd string, arg string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", ""
	}
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return "", ""
	}
	cmd = fields[0]
	if i := strings.IndexByte(cmd, '@'); i >= 0 {
		cmd = cmd[:i]
	}
	if len(fields) > 1 {
		arg = fields[1]
	}
	return strings.ToLower(cmd), arg
}

func humanBytes(n int64) string {
	if n < 0 {
		n = 0
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func progressBar(pct int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := pct / 10
	return "[" + strings.Repeat("\u2588", filled) + strings.Repeat("\u2591", 10-filled) + "]"
}

func chunkText(s string, max int) []string {
	if len(s) <= max {
		return []string{s}
	}
	var chunks []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			chunks = append(chunks, strings.TrimRight(current.String(), "\n"))
			current.Reset()
		}
	}
	for _, line := range strings.Split(s, "\n") {
		for len([]rune(line)) > max {
			flush()
			runes := []rune(line)
			chunks = append(chunks, string(runes[:max]))
			line = string(runes[max:])
		}
		if current.Len()+len(line)+1 > max && current.Len() > 0 {
			flush()
		}
		current.WriteString(line + "\n")
	}
	flush()
	return chunks
}
