package paid

import (
	"errors"
	"math"
)

// ValidateOrderGrant checks the purchase snapshot before Paid or the Panel
// can use it. Applying those terms to Client state belongs to the Panel.
func ValidateOrderGrant(order PaymentOrder) error {
	if !order.GrantSnapshot {
		return errors.New("payment order requires manual review: purchase snapshot missing")
	}
	if order.Amount <= 0 || order.GrantAddDays < 0 || int64(order.GrantAddDays) > math.MaxInt64/86400 || order.GrantTraffic < 0 {
		return errors.New("payment order grant snapshot is invalid")
	}
	return nil
}
