// Package customerbot handles one authenticated customer Telegram update at a
// time. It does not fetch updates, open a DB, start a goroutine or own Client
// creation, subscription publishing or accounting.
package customerbot

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MalenkiySolovey/solovey-component-paid-subscriptions"
	"github.com/MalenkiySolovey/solovey-component-paid-subscriptions/provider"
	"gorm.io/gorm"
)

// Sender delegates the Telegram Bot API calls to the caller's existing Host
// transport. The handler never receives the bot token.
type Sender struct {
	Message           func(context.Context, int64, string, *Keyboard) error
	Photo             func(context.Context, int64, []byte, string) error
	Invoice           func(context.Context, int64, *provider.Invoice) error
	AnswerCallback    func(context.Context, string, string) error
	AnswerPreCheckout func(context.Context, string, bool, string) error
}

type Usage struct {
	Used, Limit, Expiry int64
	Enabled, Online     bool
}

// Handler operates on the admitted Panel generation. Only invoke it for a
// Telegram update received via the authenticated getUpdates transport.
type Handler struct {
	DB   *gorm.DB
	Send Sender

	Allow         func(telegramID int64) bool
	ClientID      func(telegramID int64) (uint, error)
	Trial         *TrialRegistration
	Subscription  func(telegramID int64) (url string, links []string, err error)
	ReadUsage     func(telegramID int64) (Usage, error)
	RenderQR      func(string) ([]byte, error)
	Greeting      func() (string, error)
	RefundRevokes func() (bool, error)

	Provider     func(provider.ProviderKind) provider.PaymentProvider
	Purchase     func(context.Context, uint, uint, provider.ProviderKind, int64) (*paid.PaymentOrder, *provider.Invoice, error)
	ApplyPayment func(context.Context, uint, string) (bool, error)
	Refund       func(context.Context, uint, bool) (string, error)

	AuditCrossUser func(telegramID int64, orderID uint, action string)
	Notify         func(event string, details map[string]string)
	Warn           func(message string, err error)
}

func (b *Handler) cmdLinks(ctx context.Context, chatID, tgID int64, l Language) {
	url, links, err := b.Subscription(tgID)
	if err != nil {
		_ = b.Send.Message(ctx, chatID, Text(l, "not_linked"), nil)
		return
	}
	var text strings.Builder
	text.WriteString(Text(l, "links_title") + "\n")
	if url != "" {
		text.WriteString(url + "\n")
	}
	if len(links) > 0 {
		text.WriteString("\n" + strings.Join(links, "\n"))
	}
	message := strings.TrimSpace(text.String())
	if message == Text(l, "links_title") || message == "" {
		message = Text(l, "links_none")
	}
	for _, chunk := range chunkText(message, 4000) {
		_ = b.Send.Message(ctx, chatID, chunk, nil)
	}
}

func (b *Handler) cmdQR(ctx context.Context, chatID, tgID int64, l Language) {
	url, _, err := b.Subscription(tgID)
	if err != nil {
		_ = b.Send.Message(ctx, chatID, Text(l, "not_linked"), nil)
		return
	}
	if url == "" {
		_ = b.Send.Message(ctx, chatID, Text(l, "links_none"), nil)
		return
	}
	png, err := b.RenderQR(url)
	if err != nil || b.Send.Photo(ctx, chatID, png, Text(l, "qr_caption_sub")) != nil {
		_ = b.Send.Message(ctx, chatID, Text(l, "error"), nil)
	}
}

func (b *Handler) cmdStats(ctx context.Context, chatID, tgID int64, l Language) {
	usage, err := b.ReadUsage(tgID)
	if err != nil {
		_ = b.Send.Message(ctx, chatID, Text(l, "not_linked"), nil)
		return
	}
	_ = b.Send.Message(ctx, chatID, usageText(usage, l, time.Now().Unix()), MenuKeyboard(l))
}

func usageText(usage Usage, l Language, now int64) string {
	var text strings.Builder
	text.WriteString(Text(l, "stats_title") + "\n\n")
	fmt.Fprintf(&text, "%s: %s\n", Text(l, "stats_used"), humanBytes(usage.Used))
	if usage.Limit > 0 {
		pct := 100
		if usage.Used < usage.Limit {
			pct = int(float64(max(0, usage.Used)) * 100 / float64(usage.Limit))
		}
		fmt.Fprintf(&text, "%s: %s (%d%%)\n%s\n", Text(l, "stats_limit"), humanBytes(usage.Limit), pct, progressBar(pct))
	} else {
		fmt.Fprintf(&text, "%s: %s\n", Text(l, "stats_limit"), Text(l, "stats_unlim"))
	}
	if usage.Expiry > 0 {
		if usage.Expiry < now {
			fmt.Fprintf(&text, "%s: %s\n", Text(l, "stats_expiry"), Text(l, "stats_expired"))
		} else {
			fmt.Fprintf(&text, "%s: %d %s\n", Text(l, "stats_expiry"), (usage.Expiry-now)/86400, Text(l, "stats_days"))
		}
	}
	if usage.Enabled {
		text.WriteString(Text(l, "stats_enabled") + "\n")
	} else {
		text.WriteString(Text(l, "stats_disabled") + "\n")
	}
	if usage.Online {
		text.WriteString(Text(l, "stats_online"))
	} else {
		text.WriteString(Text(l, "stats_offline"))
	}
	return strings.TrimSpace(text.String())
}

func (b *Handler) enabledProvidersForTariff(tariff *paid.Tariff) []provider.PaymentProvider {
	var kinds []provider.ProviderKind
	if tariff.StarsAmount > 0 {
		kinds = append(kinds, provider.ProviderStars)
	}
	if tariff.Price > 0 {
		kinds = append(kinds, provider.ProviderYooKassa, provider.ProviderStripe, provider.ProviderPayMaster, provider.ProviderCryptoBot, provider.ProviderExternal)
	}
	var available []provider.PaymentProvider
	for _, kind := range kinds {
		if configured := b.Provider(kind); configured != nil {
			available = append(available, configured)
		}
	}
	return available
}

func truncateRunes(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	runes := []rune(value)
	return string(runes[:min(maximum, len(runes))])
}
