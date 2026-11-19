package workflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	paid "github.com/MalenkiySolovey/solovey-component-paid-subscriptions"
	"github.com/MalenkiySolovey/solovey-component-paid-subscriptions/provider"
	"github.com/MalenkiySolovey/solovey-component-paid-subscriptions/store"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type workProvider struct {
	poll      func(context.Context, []provider.Order) (provider.PollOutcome, error)
	reconcile func(context.Context, []provider.Order) ([]provider.Reconciliation, error)
	cancel    func(context.Context, string) (bool, error)
}

func (f workProvider) Poll(ctx context.Context, rows []provider.Order) (provider.PollOutcome, error) {
	if f.poll == nil {
		return provider.PollOutcome{}, nil
	}
	return f.poll(ctx, rows)
}

func (f workProvider) Reconcile(ctx context.Context, rows []provider.Order) ([]provider.Reconciliation, error) {
	if f.reconcile == nil {
		return nil, nil
	}
	return f.reconcile(ctx, rows)
}

func (f workProvider) CancelInvoice(ctx context.Context, ref string) (bool, error) {
	return f.cancel(ctx, ref)
}

func workDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "work.db")+"?_journal_mode=WAL"), &gorm.Config{})
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
	return db
}

func noCharge(t *testing.T) ApplyCharge {
	t.Helper()
	return func(*gorm.DB, uint, string) (func(context.Context), error) {
		t.Fatal("unexpected charge application")
		return nil, nil
	}
}

func TestPollWorkIsBoundedAndRotatesWithoutExpiringUnknownInvoices(t *testing.T) {
	db := workDB(t)
	var orders []paid.PaymentOrder
	for id := 1; id <= 1205; id++ {
		orders = append(orders, paid.PaymentOrder{ClientId: 1, TariffId: uint(id), Provider: "cryptobot", Amount: 100, Currency: "RUB", Status: paid.StatusPending, ProviderRef: fmt.Sprint(id), IdempotencyKey: fmt.Sprint("fair-", id), GrantSnapshot: true, GrantAddDays: 1, CreatedAt: time.Now().Add(-48 * time.Hour).Unix()})
	}
	if err := db.CreateInBatches(orders, 100).Error; err != nil {
		t.Fatal(err)
	}
	outage := errors.New("provider unavailable")
	calls := 0
	seen := make(map[uint]bool)
	fake := workProvider{poll: func(_ context.Context, rows []provider.Order) (provider.PollOutcome, error) {
		calls++
		if len(rows) > 100 {
			t.Fatal("poll batch exceeded 100 rows")
		}
		for _, row := range rows {
			seen[row.Id] = true
		}
		return provider.PollOutcome{}, outage
	}}
	warn := func(message string, err error) {
		if !errors.Is(err, outage) {
			t.Fatalf("unexpected work error %s: %v", message, err)
		}
	}
	RunCryptoBot(context.Background(), db, fake, nil, noCharge(t), warn)
	if calls != 10 || len(seen) != 1000 {
		t.Fatalf("first pass: %d calls, %d distinct orders", calls, len(seen))
	}
	calls = 0
	RunCryptoBot(context.Background(), db, fake, nil, noCharge(t), warn)
	if calls > 10 || len(seen) != len(orders) {
		t.Fatalf("second pass: %d calls, %d distinct orders overall", calls, len(seen))
	}
	var pending int64
	if err := db.Model(&paid.PaymentOrder{}).Where("status = ?", paid.StatusPending).Count(&pending).Error; err != nil || pending != int64(len(orders)) {
		t.Fatalf("outage changed invoice state: %d pending, %v", pending, err)
	}
}

func TestCanceledPollDoesNotApplyChargeOrAdvanceCursor(t *testing.T) {
	db := workDB(t)
	order := paid.PaymentOrder{ClientId: 1, TariffId: 1, Provider: "cryptobot", Amount: 100, Currency: "RUB", Status: paid.StatusPending, ProviderRef: "41", IdempotencyKey: "cancelled", GrantSnapshot: true, GrantAddDays: 1}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := workProvider{poll: func(context.Context, []provider.Order) (provider.PollOutcome, error) {
		cancel()
		return provider.PollOutcome{Paid: []provider.PollResult{{OrderID: order.Id, ProviderChargeID: "cryptobot:41"}}}, nil
	}}
	RunCryptoBot(ctx, db, fake, nil, noCharge(t), func(message string, err error) { t.Fatalf("%s: %v", message, err) })
	var cursors int64
	if err := db.Model(&paid.ProviderCursor{}).Count(&cursors).Error; err != nil || cursors != 0 {
		t.Fatalf("cancel advanced durable cursor: %d, %v", cursors, err)
	}
}

func TestCancellationFailureRotatesWithoutCompletingWork(t *testing.T) {
	db := workDB(t)
	var queue []paid.InvoiceCancellation
	for id := 1; id <= 205; id++ {
		queue = append(queue, paid.InvoiceCancellation{OrderID: uint(id), Ref: fmt.Sprint(5000 + id)})
	}
	if err := db.CreateInBatches(queue, 100).Error; err != nil {
		t.Fatal(err)
	}
	outage := errors.New("provider unavailable")
	requests := 0
	fake := workProvider{cancel: func(context.Context, string) (bool, error) {
		requests++
		return false, outage
	}}
	warn := func(message string, err error) {
		if !errors.Is(err, outage) {
			t.Fatalf("unexpected work error %s: %v", message, err)
		}
	}
	run := func() { RunCryptoBot(context.Background(), db, fake, fake, noCharge(t), warn) }
	done := func(want int64) {
		t.Helper()
		var completed int64
		if err := db.Model(&paid.InvoiceCancellation{}).Where("completed = ?", true).Count(&completed).Error; err != nil || completed != want {
			t.Fatalf("completed %d, want %d: %v", completed, want, err)
		}
	}
	run()
	if requests != 100 {
		t.Fatalf("cancellation calls: %d, want 100", requests)
	}
	done(0)
	fake.cancel = func(context.Context, string) (bool, error) { return true, nil }
	run()
	done(100)
	run()
	run()
	done(205)
}

func TestRecoveryEffectsRunOnlyAfterCommit(t *testing.T) {
	db := workDB(t)
	order := paid.PaymentOrder{ClientId: 1, TariffId: 1, Provider: "cryptobot", Amount: 100, Currency: "RUB", Status: paid.StatusRecoverable, IdempotencyKey: "recover-work", GrantSnapshot: true, GrantAddDays: 1}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	fake := workProvider{
		reconcile: func(context.Context, []provider.Order) ([]provider.Reconciliation, error) {
			return []provider.Reconciliation{{OrderID: order.Id, Candidates: []provider.RecoveredInvoice{
				{Ref: "41", State: provider.InvoicePaid, Verified: true},
				{Ref: "42", State: provider.InvoiceActive, Verified: true},
			}}}, nil
		},
		cancel: func(context.Context, string) (bool, error) { return true, nil },
	}
	fixtureErr := errors.New("entitlement failed")
	attempts, delivered := 0, 0
	apply := func(tx *gorm.DB, id uint, chargeID string) (func(context.Context), error) {
		attempts++
		if err := tx.Model(&paid.PaymentOrder{}).Where("id = ?", id).Updates(map[string]any{"status": paid.StatusPaid, "provider_charge_id": chargeID}).Error; err != nil {
			t.Fatal(err)
		}
		after := func(context.Context) {
			delivered++
			saved, err := store.GetOrder(db, id)
			if err != nil || saved.Status != paid.StatusPaid || saved.ProviderChargeID != "cryptobot:41" {
				t.Fatalf("effect ran before recovery committed: %+v, %v", saved, err)
			}
		}
		if attempts == 1 {
			return after, fixtureErr
		}
		return after, nil
	}
	warn := func(message string, err error) {
		if !errors.Is(err, fixtureErr) {
			t.Fatalf("unexpected work error %s: %v", message, err)
		}
	}
	RunCryptoBot(context.Background(), db, fake, fake, apply, warn)
	saved, err := store.GetOrder(db, order.Id)
	if err != nil || saved.Status != paid.StatusRecoverable || delivered != 0 {
		t.Fatalf("rollback escaped: order=%+v effects=%d err=%v", saved, delivered, err)
	}
	for range 2 {
		RunCryptoBot(context.Background(), db, fake, fake, apply, warn)
	}
	if attempts != 2 || delivered != 1 {
		t.Fatalf("expected one committed effect: %d attempts, %d effects", attempts, delivered)
	}
}
