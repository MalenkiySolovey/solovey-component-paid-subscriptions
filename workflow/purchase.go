package workflow

import (
	"context"
	"errors"
	"time"

	paid "github.com/MalenkiySolovey/solovey-component-paid-subscriptions"
	"github.com/MalenkiySolovey/solovey-component-paid-subscriptions/provider"
	"github.com/MalenkiySolovey/solovey-component-paid-subscriptions/store"
	"gorm.io/gorm"
)

type PurchaseRequest struct {
	ClientID, TariffID uint
	TelegramUserID     int64
	TTLMinutes         int
}

type PurchasePorts struct {
	CheckClient func(*gorm.DB, uint) error
	NewIdentity func() (string, error)
	OnCreated   func(paid.PaymentOrder)
}

// CreateInvoice snapshots an admitted purchase and persists the intent before
// calling the provider. The caller retains the same admitted DB generation;
// no SQL transaction remains open during the network call.
func CreateInvoice(ctx context.Context, db *gorm.DB, request PurchaseRequest, prov provider.PaymentProvider, ports PurchasePorts) (*paid.PaymentOrder, *provider.Invoice, error) {
	if request.ClientID == 0 || request.TariffID == 0 || ports.CheckClient == nil || ports.NewIdentity == nil {
		return nil, nil, errors.New("purchase identity is missing")
	}
	if prov == nil {
		return nil, nil, errors.New("provider not available")
	}
	kind := prov.Kind()
	if err := ports.CheckClient(db, request.ClientID); err != nil {
		return nil, nil, err
	}
	if kind == provider.ProviderCryptoBot {
		existing, err := store.ActiveCryptoBotOrder(db, request.ClientID, request.TariffID, request.TelegramUserID)
		if err != nil {
			return nil, nil, err
		}
		if existing != nil {
			return reusableInvoice(existing)
		}
	}
	if request.TTLMinutes < 1 {
		return nil, nil, errors.New("invalid paid subscription order TTL")
	}
	tariff, err := store.GetTariff(db, request.TariffID)
	if err != nil || !tariff.Enabled {
		return nil, nil, errors.New("tariff is unavailable")
	}
	amount, currency := tariff.Price, tariff.Currency
	if kind == provider.ProviderStars {
		amount, currency = tariff.StarsAmount, "XTR"
		if amount <= 0 {
			return nil, nil, errors.New("tariff has no stars price")
		}
	} else if amount <= 0 {
		return nil, nil, errors.New("tariff has no price")
	}
	payload, err := ports.NewIdentity()
	if err != nil {
		return nil, nil, err
	}
	if payload == "" {
		return nil, nil, errors.New("purchase identity is missing")
	}
	order := store.NewPendingOrder(request.ClientID, tariff, kind, amount, currency, request.TelegramUserID, payload, time.Now().Unix(), request.TTLMinutes)
	if err := paid.ValidateOrderGrant(*order); err != nil {
		return nil, nil, err
	}
	if kind == provider.ProviderCryptoBot {
		var created bool
		order, created, err = store.CreateInvoiceIntent(db, order)
		if err != nil {
			return nil, nil, err
		}
		if !created {
			return reusableInvoice(order)
		}
	} else if err := db.Create(order).Error; err != nil {
		return nil, nil, err
	}
	if ports.OnCreated != nil {
		ports.OnCreated(*order)
	}
	snapshot := ProviderOrder(*order)
	offer := provider.Offer{Id: tariff.Id, Name: tariff.Name, Description: tariff.Description}
	inv, err := prov.CreateInvoice(ctx, &snapshot, &offer, request.ClientID)
	if err == nil {
		switch {
		case inv == nil:
			err = errors.New("provider returned an empty invoice")
		case kind == provider.ProviderCryptoBot && (inv.ProviderRef == "" || inv.PayURL == ""):
			err = errors.New("provider returned an incomplete invoice")
		default:
			err = store.SaveInvoiceResult(db, order.Id, inv)
		}
	}
	if err != nil {
		// The admitted DB outlives the network request: preserve ambiguous
		// remote outcomes even when the request was canceled or timed out.
		if kind == provider.ProviderCryptoBot {
			return order, nil, errors.Join(err, store.MarkInvoiceRecoverable(db, order.Id))
		}
		return nil, nil, errors.Join(err, store.MarkOrderFailed(db, order.Id))
	}
	order.Status, order.ProviderRef, order.ExternalURL = paid.StatusPending, inv.ProviderRef, inv.PayURL
	return order, inv, nil
}

func reusableInvoice(order *paid.PaymentOrder) (*paid.PaymentOrder, *provider.Invoice, error) {
	if order.Status != paid.StatusPending || !order.GrantSnapshot || order.ProviderRef == "" || order.ExternalURL == "" {
		return order, nil, errors.New("existing invoice requires reconciliation or manual review")
	}
	return order, &provider.Invoice{Method: provider.InvoiceURL, PayURL: order.ExternalURL, ProviderRef: order.ProviderRef, Payload: order.IdempotencyKey}, nil
}
