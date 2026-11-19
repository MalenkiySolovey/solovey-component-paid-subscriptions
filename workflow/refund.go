package workflow

import (
	"context"
	"errors"
	"strings"
	"time"

	paid "github.com/MalenkiySolovey/solovey-component-paid-subscriptions"
	"github.com/MalenkiySolovey/solovey-component-paid-subscriptions/provider"
	"github.com/MalenkiySolovey/solovey-component-paid-subscriptions/store"
	"gorm.io/gorm"
)

var ErrRefundNotApplicable = errors.New("order is not refundable")

type RefundPorts struct {
	NewIdentity func() (string, error)
	// RefundStars is backed by the existing Host Telegram transport. The
	// transport adapter may accept only an exact already-refunded outcome.
	RefundStars func(context.Context, int64, string) error
	// Finalize atomically marks the refund and revokes Panel entitlements if
	// requested. With a nonempty claim, use the persisted policy for that claim.
	Finalize      func(db *gorm.DB, orderID uint, claim string, revoke bool) error
	ReleaseFailed func(orderID uint)
}

// RefundOrder arbitrates a durable Stars refund claim before calling Telegram.
// A local write failure leaves it recoverable by the same charge and policy.
// Other providers are finalized locally only, after an external refund.
func RefundOrder(ctx context.Context, db *gorm.DB, orderID uint, revoke bool, ports RefundPorts) (string, error) {
	if ports.Finalize == nil {
		return "", errors.New("refund entitlement callback is required")
	}
	order, err := store.GetOrder(db, orderID)
	if err != nil {
		return "", err
	}
	if order.Status != paid.StatusPaid && order.Status != paid.StatusRefundPending || paid.ValidateOrderGrant(*order) != nil {
		return "", ErrRefundNotApplicable
	}
	if order.Provider != string(provider.ProviderStars) {
		if order.Status != paid.StatusPaid {
			return "", ErrRefundNotApplicable
		}
		if err := ports.Finalize(db, orderID, "", revoke); err != nil && !errors.Is(err, store.ErrOrderAlreadyFinalized) {
			return "", err
		}
		return "refunded_manual", nil
	}
	if !strings.HasPrefix(order.ProviderChargeID, "tg:") || order.ProviderChargeID == "tg:" {
		return "", ErrRefundNotApplicable
	}
	if ports.NewIdentity == nil || ports.RefundStars == nil {
		return "", errors.New("Stars refund transport and claim identity are required")
	}
	token, err := ports.NewIdentity()
	if err != nil {
		return "", err
	}
	claimed, err := store.ClaimStarsRefund(db, orderID, revoke, token, time.Now().Unix())
	if err != nil {
		return "", err
	}
	defer func() {
		// Release using the retained DB, even after the network context ends.
		if err := store.ReleaseRefundClaim(db, orderID, token); err != nil && ports.ReleaseFailed != nil {
			ports.ReleaseFailed(orderID)
		}
	}()
	charge := strings.TrimPrefix(claimed.ProviderChargeID, "tg:")
	if err := ports.RefundStars(ctx, claimed.TelegramUserId, charge); err != nil {
		return "", err
	}
	if err := ports.Finalize(db, orderID, token, claimed.RefundRevoke); err != nil {
		return "", err
	}
	return "refunded", nil
}
