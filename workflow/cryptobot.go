// Package workflow owns bounded commerce work; the Panel supplies the
// scheduler, its admitted database and the entitlement transaction callback.
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

const cryptoBotGraceSeconds int64 = 24 * 60 * 60

// ApplyCharge must use the supplied connection. During invoice recovery it is
// already inside the Paid transaction. Return only effects that must run after
// the transaction commits, such as live runtime refresh or notification.
type ApplyCharge func(db *gorm.DB, orderID uint, chargeID string) (afterCommit func(context.Context), err error)

type Warn func(message string, err error)

// RunCryptoBot processes one bounded pass. It never starts a scheduler or
// reads Panel settings. Callers own cancellation and admission of this pass.
func RunCryptoBot(ctx context.Context, db *gorm.DB, poller provider.PollingProvider, reconciler provider.ReconciliationProvider, apply ApplyCharge, warn Warn) {
	if db == nil || poller == nil || apply == nil || warn == nil || ctx.Err() != nil {
		return
	}
	db = db.WithContext(ctx)
	seen := make(map[uint]bool)
	// Ten batches of at most 100, with durable rotation past failing records.
	for batch := 0; batch < 10 && ctx.Err() == nil; batch++ {
		orders, err := store.WorkOrders(db, "poll", 100)
		if err != nil {
			warn("load poll work", err)
			break
		}
		if len(orders) == 0 || seen[orders[0].Id] {
			break
		}
		for _, order := range orders {
			seen[order.Id] = true
		}
		outcome, err := poller.Poll(ctx, providerOrders(orders))
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			warn("provider poll unavailable", err)
		} else {
			for _, result := range outcome.Paid {
				after, err := apply(db, result.OrderID, result.ProviderChargeID)
				if errors.Is(err, store.ErrOrderAlreadyFinalized) {
					continue
				}
				if err != nil {
					warn("apply provider payment", err)
					continue
				}
				if after != nil {
					after(ctx)
				}
			}
			if err := store.ReviewProviderOrders(db, outcome.ReviewOrderIDs); err != nil {
				warn("review provider work", err)
			}
			if err := store.ExpireVerifiedProviderOrders(db, outcome.TerminalOrderIDs, time.Now().Unix(), cryptoBotGraceSeconds); err != nil {
				warn("expire verified invoices", err)
			}
		}
		if ctx.Err() != nil {
			return
		}
		if err := store.AdvanceProviderCursor(db, "poll", orders[len(orders)-1].Id); err != nil {
			warn("persist poll cursor", err)
			break
		}
	}
	if reconciler == nil || ctx.Err() != nil {
		return
	}
	unresolved, err := store.WorkOrders(db, "reconcile", 100)
	if err != nil {
		warn("load recovery work", err)
	} else if len(unresolved) > 0 {
		recovered, err := reconciler.Reconcile(ctx, providerOrders(unresolved))
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			warn("provider reconciliation unavailable", err)
		} else {
			for _, result := range recovered {
				var after func(context.Context)
				err := store.RecoverProviderOrder(db, result, time.Now().Unix(), cryptoBotGraceSeconds, func(tx *gorm.DB, orderID uint, chargeID string) error {
					var applyErr error
					after, applyErr = apply(tx, orderID, chargeID)
					if applyErr != nil {
						after = nil
					}
					return applyErr
				})
				if err != nil {
					warn("persist provider recovery", err)
					continue
				}
				if after != nil {
					after(ctx)
				}
			}
		}
		if ctx.Err() != nil {
			return
		}
		if err := store.AdvanceProviderCursor(db, "reconcile", unresolved[len(unresolved)-1].Id); err != nil {
			warn("persist recovery cursor", err)
		}
	}
	work, err := store.CancellationWork(db, 100)
	if err != nil {
		warn("load cancellation work", err)
		return
	}
	for _, item := range work {
		resolved, err := reconciler.CancelInvoice(ctx, item.Ref)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			warn("cancellation remains queued", err)
		} else if resolved {
			if err := store.CompleteCancellation(db, item.Id); err != nil {
				warn("persist cancellation", err)
			}
		}
		if err := store.AdvanceProviderCursor(db, "cancel", item.Id); err != nil {
			warn("persist cancellation cursor", err)
			return
		}
	}
}

func ProviderOrder(order paid.PaymentOrder) provider.Order {
	return provider.Order{
		Id: order.Id, Amount: order.Amount, Currency: order.Currency,
		IdempotencyKey: order.IdempotencyKey, ProviderRef: order.ProviderRef,
		ProviderPayload: append([]byte(nil), order.ProviderPayload...),
	}
}

func providerOrders(orders []paid.PaymentOrder) []provider.Order {
	result := make([]provider.Order, len(orders))
	for i, order := range orders {
		result[i] = ProviderOrder(order)
	}
	return result
}
