package store

import (
	"testing"

	paid "github.com/MalenkiySolovey/solovey-component-paid-subscriptions"
)

func TestEnsureSchemaIdempotent(t *testing.T) {
	db := openPaidTestDB(t)
	if err := paid.EnsureSchema(db); err != nil {
		t.Fatalf("paid.EnsureSchema first run: %v", err)
	}
	// Second run must be a no-op (all statements use IF NOT EXISTS).
	if err := paid.EnsureSchema(db); err != nil {
		t.Fatalf("paid.EnsureSchema second run: %v", err)
	}
	for _, table := range []string{"paidsub_bindings", "tariffs", "payment_orders", "paidsub_provider_cursors", "paidsub_invoice_cancellations"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("table %q missing after paid.EnsureSchema", table)
		}
	}
	for _, column := range []string{"grant_add_days", "grant_traffic_bytes", "grant_snapshot"} {
		if !db.Migrator().HasColumn(&paid.PaymentOrder{}, column) {
			t.Fatalf("payment_orders column %q missing after paid.EnsureSchema", column)
		}
	}
}

// TestPaymentOrdersTelegramIndex pins O-1: order history is queried by
// telegram_user_id (OrdersForTgUser / RefundableOrdersForTgUser), so the column
// must be indexed to avoid a full-table scan.
func TestPaymentOrdersTelegramIndex(t *testing.T) {
	db := openPaidTestDB(t)
	if err := paid.EnsureSchema(db); err != nil {
		t.Fatalf("paid.EnsureSchema: %v", err)
	}
	var count int64
	if err := db.Raw(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_payment_orders_telegram'",
	).Scan(&count).Error; err != nil {
		t.Fatalf("query index: %v", err)
	}
	if count != 1 {
		t.Fatalf("idx_payment_orders_telegram missing (count=%d)", count)
	}
}

func TestBindingUniqueness(t *testing.T) {
	db := openPaidTestDB(t)
	if err := paid.EnsureSchema(db); err != nil {
		t.Fatalf("paid.EnsureSchema: %v", err)
	}
	if err := db.Create(&paid.Binding{ClientId: 1, TgUserId: 1000}).Error; err != nil {
		t.Fatalf("first binding: %v", err)
	}
	// Same tg id, different client → must violate the UNIQUE(tg_user_id) index.
	if err := db.Create(&paid.Binding{ClientId: 2, TgUserId: 1000}).Error; err == nil {
		t.Fatal("expected duplicate tg_user_id to be rejected")
	}
	// Same client, different tg id → must violate the UNIQUE(client_id) index.
	if err := db.Create(&paid.Binding{ClientId: 1, TgUserId: 2000}).Error; err == nil {
		t.Fatal("expected duplicate client_id to be rejected")
	}
}

func TestSetBindingReleasesPrevious(t *testing.T) {
	db := openPaidTestDB(t)
	if err := paid.EnsureSchema(db); err != nil {
		t.Fatalf("paid.EnsureSchema: %v", err)
	}
	if err := SetBinding(db, 1, 1000, 1); err != nil {
		t.Fatalf("SetBinding 1->1000: %v", err)
	}
	// Rebind the same tg id to a different client: old row must be released.
	if err := SetBinding(db, 2, 1000, 2); err != nil {
		t.Fatalf("SetBinding 2->1000: %v", err)
	}
	var count int64
	db.Model(&paid.Binding{}).Where("tg_user_id = ?", 1000).Count(&count)
	if count != 1 {
		t.Fatalf("expected exactly 1 binding for tg 1000, got %d", count)
	}
	if _, err := BindingForClient(db, 1); err == nil {
		t.Fatal("expected client 1 binding to be released")
	}
	b, err := BindingForClient(db, 2)
	if err != nil {
		t.Fatalf("BindingForClient(2): %v", err)
	}
	if b.TgUserId != 1000 {
		t.Fatalf("expected tg 1000 bound to client 2, got %d", b.TgUserId)
	}
}
