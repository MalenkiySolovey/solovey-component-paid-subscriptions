package customerbot

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/MalenkiySolovey/solovey-component-paid-subscriptions"
	"github.com/MalenkiySolovey/solovey-component-paid-subscriptions/store"
)

// TrialClient is a proposal for the Panel's canonical client creation command.
// Paid supplies eligibility and entitlement limits, not client credentials.
type TrialClient struct {
	TelegramID     int64
	Description    string
	Inbounds       []uint
	Expiry, Volume int64
}

// TrialRegistration reads settings and invokes client creation on the same
// admitted Panel generation as the handler. It does not open a DB or poll.
type TrialRegistration struct {
	Enabled      func() (bool, error)
	StartLimit   func() (int, error)
	AllowStart   func(telegramID int64, limit int) bool
	Inbounds     func() ([]uint, error)
	ClientLimit  func() (int, error)
	Limits       func() (days, gigabytes int, err error)
	CreateClient func(context.Context, TrialClient) (uint, error)
	OnRegistered func(clientID uint, telegramID int64)
}

func (b *Handler) tryAutoRegister(ctx context.Context, chatID int64, from *User, l Language) bool {
	trial := b.Trial
	if trial == nil {
		return false
	}
	on, err := trial.Enabled()
	if err != nil || !on {
		return false
	}
	send := func(key string, menu *Keyboard) bool {
		_ = b.Send.Message(ctx, chatID, Text(l, key), menu)
		return true
	}
	fail := func(message string, err error) bool {
		b.Warn(message, err)
		return send("error", nil)
	}
	limit, err := trial.StartLimit()
	if err != nil {
		return fail("read auto-register rate limit", err)
	}
	if !trial.AllowStart(from.ID, limit) {
		return send("rate_limited", nil)
	}
	inbounds, err := trial.Inbounds()
	if err != nil || len(inbounds) == 0 {
		return send("reg_no_setup", nil)
	}
	maxClients, err := trial.ClientLimit()
	if err != nil || maxClients < 1 {
		return fail("invalid auto-register client cap", err)
	}
	db := b.DB
	var count int64
	if err := db.Model(&paid.Binding{}).Count(&count).Error; err != nil {
		return fail("count bindings for cap failed", err)
	}
	if count >= int64(maxClients) {
		return send("reg_full", nil)
	}
	days, gigabytes, err := trial.Limits()
	const day = 86400
	const gib = 1 << 30
	now := time.Now().Unix()
	if err != nil || days < 0 || gigabytes < 0 || int64(days) > (math.MaxInt64-now)/day || int64(gigabytes) > math.MaxInt64/gib {
		return fail("invalid auto-register trial limits", err)
	}
	proposal := TrialClient{TelegramID: from.ID, Description: sanitizeUsername(from.Username), Inbounds: inbounds, Volume: int64(gigabytes) * gib}
	if days > 0 {
		proposal.Expiry = now + int64(days)*day
	}
	clientID, err := trial.CreateClient(ctx, proposal)
	if err != nil {
		return fail("auto-register create client failed", err)
	}
	// Finish the binding on the admitted DB even if the poll was cancelled
	// after the Panel committed client creation.
	if err := store.SetBinding(db, clientID, from.ID, time.Now().Unix()); err != nil {
		return fail("auto-register bind failed", err)
	}
	if trial.OnRegistered != nil {
		trial.OnRegistered(clientID, from.ID)
	}
	return send("registered", MenuKeyboard(l))
}

func sanitizeUsername(username string) string {
	var label strings.Builder
	for _, r := range username {
		if r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			label.WriteRune(r)
		}
	}
	if label.Len() == 0 {
		return "telegram"
	}
	return "@" + label.String()
}
