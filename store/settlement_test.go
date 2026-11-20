package store

import (
	"errors"
	"testing"

	paid "github.com/MalenkiySolovey/solovey-component-paid-subscriptions"
	"gorm.io/gorm"
)

func TestSettlementRollsBackWithEntitlementAndRetainsClaimPolicy(t *testing.T) {
	db := newPaidDB(t)
	order := paid.PaymentOrder{ClientId: 1, TariffId: 1, Provider: "stars", Currency: "XTR", Amount: 10,
		TelegramUserId: 7, IdempotencyKey: "settlement", Status: paid.StatusPending, GrantSnapshot: true, GrantTraffic: 500}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	check := func(status string) *paid.PaymentOrder {
		t.Helper()
		got, err := GetOrder(db, order.Id)
		if err != nil || got.Status != status {
			t.Fatalf("want status %s, got %+v: %v", status, got, err)
		}
		return got
	}
	failed := errors.New("entitlement operation failed")
	if err := SettlePayment(db, fixtureTransaction, order.Id, "tg:charge", 100, func(tx *gorm.DB, got paid.PaymentOrder) (GrantedUsage, error) {
		if got.Status != paid.StatusPaid || got.ProviderChargeID != "tg:charge" {
			t.Fatal("entitlement did not receive the admitted charge")
		}
		if err := tx.Model(&got).Update("granted_up", 99).Error; err != nil {
			t.Fatal(err)
		}
		return GrantedUsage{}, failed
	}); !errors.Is(err, failed) {
		t.Fatal(err)
	}
	if got := check(paid.StatusPending); got.ProviderChargeID != "" || got.GrantedUp != 0 {
		t.Fatal("failed entitlement left committed writes")
	}
	renew := func(*gorm.DB, paid.PaymentOrder) (GrantedUsage, error) { return GrantedUsage{Up: 40, Down: 60}, nil }
	if err := SettlePayment(db, nil, order.Id, "tg:charge", 101, renew); !errors.Is(err, errTransactionOwnerRequired) {
		t.Fatalf("settlement accepted without a transaction owner: %v", err)
	}
	check(paid.StatusPending)
	if err := SettlePayment(db, fixtureTransaction, order.Id, "tg:charge", 101, renew); err != nil {
		t.Fatal(err)
	}
	if err := SettlePayment(db, fixtureTransaction, order.Id, "tg:different", 102, renew); !errors.Is(err, ErrOrderAlreadyFinalized) {
		t.Fatal("duplicate payment was not rejected", err)
	}
	if got := check(paid.StatusPaid); got.ProviderChargeID != "tg:charge" || got.GrantedUp != 40 || got.GrantedDown != 60 {
		t.Fatal("settlement lost purchase evidence")
	}
	if _, err := ClaimStarsRefund(db, order.Id, true, "claim", 103); err != nil {
		t.Fatal(err)
	}
	if err := SettleRefund(db, nil, order.Id, "claim", false, nil); !errors.Is(err, errTransactionOwnerRequired) {
		t.Fatalf("refund accepted without a transaction owner: %v", err)
	}
	if err := SettleRefund(db, fixtureTransaction, order.Id, "claim", false, func(*gorm.DB, paid.PaymentOrder, bool) error { return failed }); !errors.Is(err, failed) {
		t.Fatal("failed revocation did not respect the original refund policy", err)
	}
	check(paid.StatusRefundPending)
	if err := SettleRefund(db, fixtureTransaction, order.Id, "claim", false, func(_ *gorm.DB, got paid.PaymentOrder, restore bool) error {
		if !restore || got.ProviderChargeID != "tg:charge" || got.GrantedUp != 40 {
			t.Fatal("refund retry did not receive original payment evidence")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	check(paid.StatusRefunded)
}
