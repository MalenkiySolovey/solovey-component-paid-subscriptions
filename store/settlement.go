package store

import (
	"errors"

	paid "github.com/MalenkiySolovey/solovey-component-paid-subscriptions"
	"gorm.io/gorm"
)

// GrantedUsage stores the Panel's usage snapshot for a later refund.
type GrantedUsage struct {
	Up, Down int64
}

// SettlePayment records one authenticated charge. The entitlement callback
// must use tx; live effects belong after the caller's outermost commit.
func SettlePayment(db *gorm.DB, orderID uint, chargeID string, now int64, renew func(tx *gorm.DB, order paid.PaymentOrder) (GrantedUsage, error)) error {
	if chargeID == "" || renew == nil {
		return errors.New("charge identity and entitlement callback are required")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		changed := tx.Model(&paid.PaymentOrder{}).Where("id = ? AND status = ?", orderID, paid.StatusPending).
			Updates(map[string]any{"status": paid.StatusPaid, "paid_at": now, "provider_charge_id": chargeID})
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrOrderAlreadyFinalized
		}
		order, err := GetOrder(tx, orderID)
		if err != nil {
			return err
		}
		if err := paid.ValidateOrderGrant(*order); err != nil {
			return err
		}
		usage, err := renew(tx, *order)
		if err != nil || order.GrantTraffic == 0 {
			return err
		}
		return tx.Model(order).Updates(map[string]any{"granted_up": usage.Up, "granted_down": usage.Down}).Error
	})
}

// SettleRefund records a manual refund or an already-confirmed Stars refund.
// A claimed refund uses only the policy saved with the original claim.
func SettleRefund(db *gorm.DB, orderID uint, claim string, revoke bool, revokeGrant func(tx *gorm.DB, order paid.PaymentOrder, restoreLiveUsage bool) error) error {
	return db.Transaction(func(tx *gorm.DB) error {
		query := tx.Model(&paid.PaymentOrder{}).Where("id = ?", orderID)
		if claim == "" {
			query = query.Where("status = ?", paid.StatusPaid)
		} else {
			query = query.Where("status = ? AND refund_claim = ?", paid.StatusRefundPending, claim)
		}
		changed := query.Updates(map[string]any{"status": paid.StatusRefunded, "refund_claim": "", "refund_claim_until": 0})
		if changed.Error != nil {
			return changed.Error
		}
		if changed.RowsAffected != 1 {
			return ErrOrderAlreadyFinalized
		}
		order, err := GetOrder(tx, orderID)
		if err != nil {
			return err
		}
		if err := paid.ValidateOrderGrant(*order); err != nil {
			return err
		}
		shouldRevoke := revoke
		if claim != "" {
			shouldRevoke = order.RefundRevoke
		}
		if !shouldRevoke {
			return nil
		}
		if revokeGrant == nil {
			return errors.New("refund entitlement callback is required")
		}
		var newerTrafficOrders int64
		if order.GrantTraffic > 0 {
			if err := tx.Model(&paid.PaymentOrder{}).
				Where("client_id = ? AND id > ? AND status IN ?", order.ClientId, order.Id, []string{paid.StatusPaid, paid.StatusRefundPending}).
				Where("grant_snapshot = ? AND grant_traffic_bytes > 0", true).
				Count(&newerTrafficOrders).Error; err != nil {
				return err
			}
		}
		return revokeGrant(tx, *order, newerTrafficOrders == 0)
	})
}
