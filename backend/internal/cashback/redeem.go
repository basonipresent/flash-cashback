package cashback

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Redeem processes a redemption request (FR-14–16). See spec/design.md §3.
func Redeem(ctx context.Context, db *pgxpool.Pool, in RedemptionInput) (RedemptionResult, error) {
	// Fast path: already processed under this idempotency key (FR-16).
	if result, ok, err := lookupRedemption(ctx, db, in); err != nil {
		return RedemptionResult{}, err
	} else if ok {
		return result, nil
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return RedemptionResult{}, fmt.Errorf("cashback: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful Commit

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_balance (user_id, balance_idr) VALUES ($1, 0)
		ON CONFLICT (user_id) DO NOTHING
	`, in.UserID); err != nil {
		if isForeignKeyViolation(err) {
			return RedemptionResult{}, ErrUserNotFound
		}
		return RedemptionResult{}, fmt.Errorf("cashback: init user_balance: %w", err)
	}

	var balance int64
	if err := tx.QueryRow(ctx, `
		SELECT balance_idr FROM user_balance WHERE user_id = $1 FOR UPDATE
	`, in.UserID).Scan(&balance); err != nil {
		return RedemptionResult{}, fmt.Errorf("cashback: lock user_balance: %w", err)
	}

	// Validate before claiming the idempotency key, so an invalid request
	// stays retryable rather than permanently consuming the key (FR-14, FR-15).
	if in.AmountIDR < MinRedemptionIDR {
		return RedemptionResult{}, ErrBelowMinimumRedemption
	}
	if in.AmountIDR > balance {
		return RedemptionResult{}, ErrInsufficientBalance
	}

	id := uuid.New().String()
	var claimedID string
	err = tx.QueryRow(ctx, `
		INSERT INTO redemptions (id, user_id, amount_idr, idempotency_key)
		VALUES ($1::uuid, $2, $3, $4)
		ON CONFLICT (user_id, idempotency_key) DO NOTHING
		RETURNING id
	`, id, in.UserID, in.AmountIDR, in.IdempotencyKey).Scan(&claimedID)

	if errors.Is(err, pgx.ErrNoRows) {
		// Lost the race: a concurrent request for the same key won between
		// our lookupRedemption check above and this transaction's lock.
		_ = tx.Rollback(ctx)
		result, ok, err := lookupRedemption(ctx, db, in)
		if err != nil {
			return RedemptionResult{}, err
		}
		if !ok {
			return RedemptionResult{}, fmt.Errorf("cashback: redemption conflicted but no result found")
		}
		return result, nil
	}
	if err != nil {
		return RedemptionResult{}, fmt.Errorf("cashback: claim redemption: %w", err)
	}

	var newBalance int64
	if err := tx.QueryRow(ctx, `
		UPDATE user_balance SET balance_idr = balance_idr - $1, updated_at = now()
		WHERE user_id = $2
		RETURNING balance_idr
	`, in.AmountIDR, in.UserID).Scan(&newBalance); err != nil {
		return RedemptionResult{}, fmt.Errorf("cashback: update user_balance: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger (user_id, entry_type, amount_idr, ref_type, ref_id)
		VALUES ($1, 'REDEEM', $2, 'REDEMPTION', $3)
	`, in.UserID, -in.AmountIDR, claimedID); err != nil {
		return RedemptionResult{}, fmt.Errorf("cashback: insert ledger: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return RedemptionResult{}, fmt.Errorf("cashback: commit: %w", err)
	}

	return RedemptionResult{RedemptionID: claimedID, NewBalanceIDR: newBalance}, nil
}

// lookupRedemption returns the previously-processed result for
// (in.UserID, in.IdempotencyKey), if any. If the key was already used for a
// different amount, returns ErrIdempotencyKeyConflict rather than silently
// returning the stale result (decisions.md "Redemption idempotency-key
// conflict").
func lookupRedemption(ctx context.Context, db *pgxpool.Pool, in RedemptionInput) (RedemptionResult, bool, error) {
	var id string
	var amount int64
	err := db.QueryRow(ctx, `
		SELECT id, amount_idr FROM redemptions WHERE user_id = $1 AND idempotency_key = $2
	`, in.UserID, in.IdempotencyKey).Scan(&id, &amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return RedemptionResult{}, false, nil
	}
	if err != nil {
		return RedemptionResult{}, false, fmt.Errorf("cashback: lookup redemption: %w", err)
	}
	if amount != in.AmountIDR {
		return RedemptionResult{}, false, ErrIdempotencyKeyConflict
	}

	var balance int64
	if err := db.QueryRow(ctx, `
		SELECT COALESCE((SELECT balance_idr FROM user_balance WHERE user_id = $1), 0)
	`, in.UserID).Scan(&balance); err != nil {
		return RedemptionResult{}, false, fmt.Errorf("cashback: lookup balance: %w", err)
	}

	return RedemptionResult{RedemptionID: id, NewBalanceIDR: balance}, true, nil
}
