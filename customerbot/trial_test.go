package customerbot

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/MalenkiySolovey/solovey-component-paid-subscriptions"
	"github.com/MalenkiySolovey/solovey-component-paid-subscriptions/store"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTrialRegistrationUsesThePanelCommandOnlyWhenAdmitted(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "payment.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	if err := paid.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}

	const userID, clientID = 7, 71
	creates, audits := 0, 0
	lookupErr := errors.New("transient client lookup failure")
	enabled, allowed, cap := false, true, 1
	var reply string
	var before int64
	var cancel context.CancelFunc
	trial := TrialRegistration{
		Enabled:     func() (bool, error) { return enabled, nil },
		StartLimit:  func() (int, error) { return 3, nil },
		AllowStart:  func(id int64, limit int) bool { return id == userID && limit == 3 && allowed },
		Inbounds:    func() ([]uint, error) { return []uint{4, 9}, nil },
		ClientLimit: func() (int, error) { return cap, nil },
		Limits:      func() (int, int, error) { return 2, 5, nil },
		CreateClient: func(_ context.Context, proposal TrialClient) (uint, error) {
			creates++
			if proposal.TelegramID != userID || proposal.Description != "@Alice_2" || !reflect.DeepEqual(proposal.Inbounds, []uint{4, 9}) || proposal.Volume != 5<<30 {
				t.Fatalf("unexpected trial proposal: %+v", proposal)
			}
			if day := int64(86400); proposal.Expiry < before+2*day || proposal.Expiry > time.Now().Unix()+2*day {
				t.Fatalf("expected a two-day expiry, got %d", proposal.Expiry)
			}
			cancel() // The Panel may have committed just as the poll was cancelled.
			return clientID, nil
		},
		OnRegistered: func(client uint, telegram int64) {
			audits++
			if client != clientID || telegram != userID {
				t.Fatalf("registration audit addressed wrong client or user: %d, %d", client, telegram)
			}
			binding, err := store.BindingForClient(db, client)
			if err != nil || binding.TgUserId != telegram {
				t.Fatalf("registration audit preceded binding: %+v, %v", binding, err)
			}
		},
	}
	h := Handler{
		DB: db, Trial: &trial,
		ClientID: func(int64) (uint, error) { return 0, lookupErr },
		Send: Sender{Message: func(_ context.Context, chat int64, text string, _ *Keyboard) error {
			if chat != userID {
				t.Fatalf("unexpected chat %d", chat)
			}
			reply = text
			return nil
		}},
		Warn: func(string, error) {},
	}
	user := &User{ID: userID, Username: "Alice_2!"}
	start := func(key string, wantCreated int) {
		t.Helper()
		reply = ""
		before = time.Now().Unix()
		var ctx context.Context
		ctx, cancel = context.WithCancel(t.Context())
		defer cancel()
		h.cmdStart(ctx, userID, user, English)
		if reply != Text(English, key) || creates != wantCreated || audits != creates {
			t.Fatalf("%s: reply=%q, creates=%d, audits=%d", key, reply, creates, audits)
		}
	}
	start("error", 0)
	lookupErr = gorm.ErrRecordNotFound
	start("not_linked", 0)
	enabled, allowed = true, false
	start("rate_limited", 0)
	allowed = true
	start("registered", 1)
	start("reg_full", 1)
}

func TestSanitizeTrialDisplayName(t *testing.T) {
	for in, want := range map[string]string{
		"": "telegram", "alice": "@alice", "bad name!": "@badname", "a_b_2024": "@a_b_2024",
		"!!!": "telegram", "\u0418\u0432\u0430\u043d": "telegram", "john.doe": "@johndoe",
	} {
		if got := sanitizeUsername(in); got != want {
			t.Errorf("sanitizeUsername(%q) = %q, want %q", in, got, want)
		}
	}
}
