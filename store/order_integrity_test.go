package store

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	paid "github.com/MalenkiySolovey/solovey-component-paid-subscriptions"
	"github.com/MalenkiySolovey/solovey-component-paid-subscriptions/provider"
)

func TestPersistedProviderIdentityCannotBeReplacedOrShared(t *testing.T) {
	db := newPaidDB(t)
	for i := 1; i <= 2; i++ {
		row := paid.PaymentOrder{ClientId: uint(i), TariffId: 1, Provider: "cryptobot", Status: paid.StatusInvoiceCreating, Amount: 100, Currency: "RUB", GrantSnapshot: true, IdempotencyKey: fmt.Sprint("identity-", i)}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		invoice := &provider.Invoice{ProviderRef: "41", PayURL: "https://pay.example/41"}
		err := SaveInvoiceResult(db, row.Id, invoice)
		if i == 1 {
			if err != nil {
				t.Fatal(err)
			}
			if err := SaveInvoiceResult(db, row.Id, &provider.Invoice{ProviderRef: "42", PayURL: "https://pay.example/42"}); err == nil {
				t.Fatal("immutable invoice replaced")
			}
		} else if err == nil {
			t.Fatal("two orders share one invoice")
		}
	}
}

func TestConcurrentInvoiceIntentHasOneDurableCreator(t *testing.T) {
	db := newPaidDB(t)
	start := make(chan struct{})
	type outcome struct {
		created bool
		id      uint
		err     error
	}
	results := make(chan outcome, 8)
	var ready sync.WaitGroup
	ready.Add(8)
	for i := range 8 {
		go func() {
			ready.Done()
			<-start
			order := &paid.PaymentOrder{ClientId: 1, TariffId: 1, TelegramUserId: 7, Provider: "cryptobot", Amount: 100, Currency: "RUB", GrantSnapshot: true, IdempotencyKey: fmt.Sprint("intent-", i)}
			got, created, err := CreateInvoiceIntent(db, order)
			var id uint
			if got != nil {
				id = got.Id
			}
			results <- outcome{created, id, err}
		}()
	}
	ready.Wait()
	close(start)
	winners := 0
	var id uint
	for range 8 {
		r := <-results
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.created {
			winners++
		}
		if id == 0 {
			id = r.id
		}
		if r.id != id {
			t.Fatal("different durable intents")
		}
	}
	if winners != 1 {
		t.Fatalf("creators=%d", winners)
	}
}

func TestManualReviewHoldSurvivesConcurrentInvoiceIntentRetries(t *testing.T) {
	db := newPaidDB(t)
	history := paid.PaymentOrder{ClientId: 1, TariffId: 1, TelegramUserId: 7, Provider: "cryptobot", Amount: 100, Currency: "RUB", Status: paid.StatusManualReview, IdempotencyKey: "review-history"}
	if err := db.Create(&history).Error; err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	done := make(chan error, 8)
	var ready sync.WaitGroup
	ready.Add(8)
	for i := range 8 {
		go func() {
			ready.Done()
			<-start
			attempt := &paid.PaymentOrder{ClientId: 1, TariffId: 1, TelegramUserId: 7, Provider: "cryptobot", Amount: 100, Currency: "RUB", GrantSnapshot: true, IdempotencyKey: fmt.Sprint("review-retry-", i)}
			got, created, err := CreateInvoiceIntent(db, attempt)
			if err == nil && (created || got == nil || got.Id != history.Id || got.Status != paid.StatusManualReview) {
				err = errors.New("manual review hold bypassed")
			}
			done <- err
		}()
	}
	ready.Wait()
	close(start)
	for range 8 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	var count int64
	if err := db.Model(&paid.PaymentOrder{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatal("review retry created new purchase", count, err)
	}
}
