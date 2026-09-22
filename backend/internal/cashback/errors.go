package cashback

import "errors"

var (
	// ErrBelowMinimumRedemption: amount is under MinRedemptionIDR (FR-14).
	ErrBelowMinimumRedemption = errors.New("cashback: amount is below the minimum redemption amount")

	// ErrInsufficientBalance: amount exceeds the user's balance (FR-15).
	ErrInsufficientBalance = errors.New("cashback: amount exceeds redeemable balance")

	// ErrIdempotencyKeyConflict: the same (user_id, idempotency_key) was
	// already used for a redemption of a different amount (FR-16,
	// decisions.md "Redemption idempotency-key conflict").
	ErrIdempotencyKeyConflict = errors.New("cashback: idempotency key was already used for a different amount")

	// ErrFuturePaymentTimestamp: paid_at is further in the future than
	// MaxFutureClockSkew tolerates (decisions.md "paid_at bounds").
	ErrFuturePaymentTimestamp = errors.New("cashback: paid_at cannot be in the future")
)
