package workflow

import (
	"context"
	"errors"
	"fmt"
	"testing"

	paid "github.com/MalenkiySolovey/solovey-component-paid-subscriptions"
	"github.com/MalenkiySolovey/solovey-component-paid-subscriptions/provider"
	"github.com/MalenkiySolovey/solovey-component-paid-subscriptions/store"
	"gorm.io/gorm"
)

type invoiceProvider struct {
	provider.PaymentProvider
	create func(context.Context, *provider.Order, *provider.Offer, uint) (*provider.Invoice, error)
}

func (p invoiceProvider) Kind() provider.ProviderKind { return provider.ProviderCryptoBot }

func (p invoiceProvider) CreateInvoice(ctx context.Context, order *provider.Order, offer *provider.Offer, id uint) (*provider.Invoice, error) {
	return p.create(ctx, order, offer, id)
}

func TestPurchaseSnapshotsCanonicalTariffBeforeTheProviderAndReusesIntent(t *testing.T) {
	db := workDB(t)
	tariff := paid.Tariff{Name: "offer", Price: 250, Currency: "RUB", AddDays: 5, Enabled: true}
	if err := db.Create(&tariff).Error; err != nil {
		t.Fatal(err)
	}
	calls, created := 0, 0
	prov := invoiceProvider{create: func(_ context.Context, order *provider.Order, offer *provider.Offer, clientID uint) (*provider.Invoice, error) {
		calls++
		durable, err := store.GetOrder(db, order.Id)
		if err != nil || durable.Status != paid.StatusInvoiceCreating || durable.GrantAddDays != 5 || order.Amount != 250 || order.Currency != "RUB" || clientID != 7 || offer.Id != tariff.Id {
			t.Fatalf("provider did not see the durable tariff snapshot: %+v, %v", durable, err)
		}
		return &provider.Invoice{Method: provider.InvoiceURL, ProviderRef: "41", PayURL: "https://pay.example.test/41"}, nil
	}}
	request := PurchaseRequest{ClientID: 7, TariffID: tariff.Id, TelegramUserID: 41, TTLMinutes: 15}
	ports := PurchasePorts{
		CheckClient: func(_ *gorm.DB, id uint) error {
			if id != 7 {
				return errors.New("unknown client")
			}
			return nil
		},
		NewIdentity: func() (string, error) { return "purchase-identity", nil },
		OnCreated: func(order paid.PaymentOrder) {
			created++
			if order.Id == 0 {
				t.Fatal("creation effect preceded persist")
			}
		},
	}
	bad := request
	bad.ClientID = 8
	if _, _, err := CreateInvoice(context.Background(), db, bad, prov, ports); err == nil || calls != 0 || created != 0 {
		t.Fatal("invoice created without Client owner admission")
	}
	first, inv, err := CreateInvoice(context.Background(), db, request, prov, ports)
	if err != nil || first.Status != paid.StatusPending || inv.ProviderRef != "41" {
		t.Fatalf("first purchase: %+v, %+v, %v", first, inv, err)
	}
	if err := db.Model(&tariff).Update("price", 500).Error; err != nil {
		t.Fatal(err)
	}
	second, inv, err := CreateInvoice(context.Background(), db, request, prov, ports)
	if err != nil || second.Id != first.Id || second.Amount != 250 || inv.ProviderRef != "41" || calls != 1 || created != 1 {
		t.Fatalf("purchase retry severed original intent: %+v, %+v, %v", second, inv, err)
	}
}

func TestStarsRefundLocalFailureKeepsRemoteChargeAndPolicy(t *testing.T) {
	db := workDB(t)
	order := paid.PaymentOrder{ClientId: 1, TariffId: 1, TelegramUserId: 7, Provider: "stars", Status: paid.StatusPaid, Amount: 10, Currency: "XTR", IdempotencyKey: "stars-refund", ProviderChargeID: "tg:original", GrantSnapshot: true, GrantAddDays: 1}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	localFailure := errors.New("Panel transaction failed")
	issued, calls, finalizations := 0, 0, 0
	ports := RefundPorts{
		NewIdentity: func() (string, error) { issued++; return fmt.Sprint("lease-", issued), nil },
		RefundStars: func(_ context.Context, user int64, charge string) error {
			calls++
			if user != 7 || charge != "original" {
				t.Fatalf("wrong remote identity %d, %q", user, charge)
			}
			return nil
		},
		Finalize: func(db *gorm.DB, id uint, claim string, revoke bool) error {
			finalizations++
			if !revoke || id != order.Id || claim != fmt.Sprint("lease-", issued) {
				t.Fatal("refund retry lost committed policy or claim")
			}
			if finalizations == 1 {
				return localFailure
			}
			return db.Model(&paid.PaymentOrder{}).Where("id = ? AND refund_claim = ?", id, claim).Updates(map[string]any{"status": paid.StatusRefunded, "refund_claim": "", "refund_claim_until": 0}).Error
		},
		ReleaseFailed: func(id uint) { t.Fatalf("unexpected release failure: %d", id) },
	}
	ctx := context.Background()
	if _, err := RefundOrder(ctx, db, order.Id, true, ports); !errors.Is(err, localFailure) {
		t.Fatal(err)
	}
	row, err := store.GetOrder(db, order.Id)
	if err != nil || row.Status != paid.StatusRefundPending || !row.RefundRevoke || row.RefundClaim != "" {
		t.Fatalf("intent lost after failure: %+v, %v", row, err)
	}
	if status, err := RefundOrder(ctx, db, order.Id, false, ports); err != nil || status != "refunded" {
		t.Fatalf("refund retry: %s, %v", status, err)
	}
	if _, err := RefundOrder(ctx, db, order.Id, false, ports); !errors.Is(err, ErrRefundNotApplicable) || calls != 2 || finalizations != 2 {
		t.Fatalf("finalized refund was retried: %d calls, %d finalizations, %v", calls, finalizations, err)
	}
}
