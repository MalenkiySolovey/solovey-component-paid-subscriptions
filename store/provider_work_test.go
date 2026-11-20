package store

import (
	"errors"
	"fmt"
	"testing"

	paid "github.com/MalenkiySolovey/solovey-component-paid-subscriptions"
	"github.com/MalenkiySolovey/solovey-component-paid-subscriptions/provider"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRecoveryUsesOneTransactionForChargeAndCancellation(t *testing.T) {
	db := newPaidDB(t)
	order := paid.PaymentOrder{ClientId: 1, Provider: "cryptobot", Status: paid.StatusRecoverable, Amount: 100, Currency: "RUB", IdempotencyKey: "recover", GrantSnapshot: true, GrantAddDays: 1}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	recovery := provider.Reconciliation{OrderID: order.Id, Candidates: []provider.RecoveredInvoice{
		{Ref: "41", State: provider.InvoicePaid, Verified: true},
		{Ref: "42", State: provider.InvoiceActive, Verified: true},
	}}
	if err := RecoverProviderOrder(db, fixtureTransaction, recovery, 1000, 86400, nil); err == nil {
		t.Fatal("recovery accepted without the Panel grant owner")
	}
	fixtureErr := errors.New("fixture entitlement failure")
	attempts := 0
	apply := func(tx *gorm.DB, orderID uint, chargeID string) error {
		attempts++
		current, err := GetOrder(tx, orderID)
		if err != nil || current.Status != paid.StatusPending || current.ProviderRef != "41" || chargeID != "cryptobot:41" {
			t.Fatalf("charge did not use the selected invoice: %v, %v", current, err)
		}
		queued, err := CancellationWork(tx, 100)
		if err != nil || len(queued) != 1 || queued[0].Ref != "42" {
			t.Fatalf("sibling cancellation did not share the transaction: %v, %v", queued, err)
		}
		if attempts == 1 {
			return fixtureErr
		}
		return tx.Model(current).Updates(map[string]any{"status": paid.StatusPaid, "provider_charge_id": chargeID}).Error
	}
	if err := RecoverProviderOrder(db, nil, recovery, 1000, 86400, apply); !errors.Is(err, errTransactionOwnerRequired) || attempts != 0 {
		t.Fatalf("recovery accepted without a transaction owner: %v, attempts=%d", err, attempts)
	}
	if err := RecoverProviderOrder(db, fixtureTransaction, recovery, 1000, 86400, apply); !errors.Is(err, fixtureErr) {
		t.Fatalf("failed entitlement was accepted: %v", err)
	}
	current, err := GetOrder(db, order.Id)
	if err != nil || current.Status != paid.StatusRecoverable || current.ProviderRef != "" {
		t.Fatalf("recoverable order changed after rollback: %v, %v", current, err)
	}
	if queued, err := CancellationWork(db, 100); err != nil || len(queued) != 0 {
		t.Fatalf("sibling cancellation escaped rollback: %v, %v", queued, err)
	}
	for range 2 {
		if err := RecoverProviderOrder(db, fixtureTransaction, recovery, 1000, 86400, apply); err != nil {
			t.Fatal(err)
		}
	}
	if attempts != 2 {
		t.Fatalf("replayed recovery attempted another grant: %d attempts", attempts)
	}
}

func TestPersistentProviderCursorRotatesBeyondThousandAndWraps(t *testing.T) {
	db := newPaidDB(t)
	var orders []paid.PaymentOrder
	for id := 1; id <= 1205; id++ {
		orders = append(orders, paid.PaymentOrder{ClientId: uint(id), TariffId: 1, Provider: "cryptobot", Amount: 100, Currency: "RUB", Status: paid.StatusPending, ProviderRef: fmt.Sprint(id), IdempotencyKey: fmt.Sprint("fair-", id), GrantSnapshot: true})
	}
	if err := db.CreateInBatches(orders, 100).Error; err != nil {
		t.Fatal(err)
	}
	for batch := 0; batch < 13; batch++ {
		work, err := WorkOrders(db.Session(&gorm.Session{NewDB: true}), "poll", 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(work) == 0 {
			t.Fatalf("cursor batch %d returned no work", batch)
		}
		if work[0].Id != uint(batch*100+1) {
			t.Fatal("cursor starvation", batch, work[0].Id)
		}
		if err := AdvanceProviderCursor(db, "poll", work[len(work)-1].Id); err != nil {
			t.Fatal(err)
		}
	}
	work, err := WorkOrders(db, "poll", 100)
	if err != nil || len(work) == 0 || work[0].Id != 1 {
		t.Fatal("cursor did not wrap", err)
	}
	// Remove the cursor's row, then reopen the real file-backed database. The
	// next worker must retain durable progress and wrap despite the missing ID.
	if err := db.Delete(&paid.PaymentOrder{}, 1205).Error; err != nil {
		t.Fatal(err)
	}
	var databaseFile string
	if err := db.Raw("SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&databaseFile).Error; err != nil || databaseFile == "" {
		t.Fatal("fixture is not file backed", err)
	}
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := gorm.Open(sqlite.Open(databaseFile+"?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	restartedPool, err := restarted.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restartedPool.Close() })
	work, err = WorkOrders(restarted, "poll", 100)
	if err != nil || len(work) != 100 || work[0].Id != 1 {
		t.Fatal("restart/deleted cursor prevented wrap", work, err)
	}
	if err := AdvanceProviderCursor(restarted, "poll", work[len(work)-1].Id); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Delete(&paid.PaymentOrder{}, []uint{100, 101}).Error; err != nil {
		t.Fatal(err)
	}
	work, err = WorkOrders(restarted, "poll", 100)
	if err != nil || len(work) != 100 || work[0].Id != 102 {
		t.Fatal("deletion near cursor lost fair progress", work, err)
	}
}
