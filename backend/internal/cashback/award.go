package cashback

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// computeAward implements FR-02/FR-03/FR-04/FR-07: integer-only, no floats
// (NFR-01). It is a pure function so it is fully unit-testable without a DB.
func computeAward(amountIDR, dailyRemainingIDR, budgetRemainingIDR int64) (awarded int64, reason ReasonCode) {
	if amountIDR < MinEligibleAmountIDR {
		return 0, ReasonBelowMinimum
	}
	if budgetRemainingIDR <= 0 {
		return 0, ReasonCampaignEnded
	}
	if dailyRemainingIDR <= 0 {
		return 0, ReasonDailyCapReached
	}

	base := amountIDR * CashbackRateNumerator / CashbackRateDenominator

	switch {
	case dailyRemainingIDR < base && budgetRemainingIDR < base:
		// Both caps bind at once. decisions.md "Tie-break reason code": on
		// an exact tie, budget exhaustion is a global, one-time event and
		// takes priority over a personal daily cap that resets tomorrow.
		if dailyRemainingIDR < budgetRemainingIDR {
			return dailyRemainingIDR, ReasonPartialDailyCap
		}
		return budgetRemainingIDR, ReasonPartialBudget
	case dailyRemainingIDR < base:
		return dailyRemainingIDR, ReasonPartialDailyCap
	case budgetRemainingIDR < base:
		return budgetRemainingIDR, ReasonPartialBudget
	default:
		return base, ReasonAwarded
	}
}

// validatePaidAt implements decisions.md "paid_at bounds": a payment can't
// have succeeded before the event reporting it was created. Pure function,
// unit-testable without a DB.
func validatePaidAt(paidAt, now time.Time) error {
	if paidAt.After(now.Add(MaxFutureClockSkew)) {
		return ErrFuturePaymentTimestamp
	}
	return nil
}

// AwardPayment processes a payment-succeeded event, awarding cashback per
// the rules above under a concurrency-safe transaction. See spec/design.md
// §3 for the locking scheme this implements.
func AwardPayment(ctx context.Context, db *pgxpool.Pool, in PaymentInput) (AwardResult, error) {
	// Fast path: already processed (FR-08). A stale/future paid_at on a
	// *retry* doesn't matter - the original request already passed
	// validation, so just return its result as-is.
	if result, ok, err := existingAward(ctx, db, in.PaymentID); err != nil {
		return AwardResult{}, err
	} else if ok {
		return result, nil
	}

	if err := validatePaidAt(in.PaidAt, time.Now()); err != nil {
		return AwardResult{}, err
	}

	// cashback_date: paid_at converted to WIB, truncated to date (FR-05).
	cashbackDate := in.PaidAt.In(Timezone).Format("2006-01-02")

	tx, err := db.Begin(ctx)
	if err != nil {
		return AwardResult{}, fmt.Errorf("cashback: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful Commit

	var claimedID string
	err = tx.QueryRow(ctx, `
		INSERT INTO payments (payment_id, user_id, amount_idr, paid_at, cashback_date)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (payment_id) DO NOTHING
		RETURNING payment_id
	`, in.PaymentID, in.UserID, in.AmountIDR, in.PaidAt, cashbackDate).Scan(&claimedID)

	if errors.Is(err, pgx.ErrNoRows) {
		// Lost the race: another request already fully processed this
		// payment_id. Postgres serializes the conflicting INSERT, so by the
		// time we observe the conflict the winner has committed.
		_ = tx.Rollback(ctx)
		result, ok, err := existingAward(ctx, db, in.PaymentID)
		if err != nil {
			return AwardResult{}, err
		}
		if !ok {
			return AwardResult{}, fmt.Errorf("cashback: payment %q conflicted but no result found", in.PaymentID)
		}
		return result, nil
	}
	if err != nil {
		return AwardResult{}, fmt.Errorf("cashback: claim payment: %w", err)
	}

	// Fixed lock order (budget, then daily) avoids deadlocks between
	// concurrent payments touching both rows (design.md §3).
	var totalBudget, budgetAwarded int64
	if err := tx.QueryRow(ctx, `
		SELECT total_budget_idr, awarded_total_idr FROM campaign_budget WHERE id = 1 FOR UPDATE
	`).Scan(&totalBudget, &budgetAwarded); err != nil {
		return AwardResult{}, fmt.Errorf("cashback: lock campaign_budget: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_daily_cashback (user_id, cashback_date, awarded_total_idr)
		VALUES ($1, $2, 0)
		ON CONFLICT (user_id, cashback_date) DO NOTHING
	`, in.UserID, cashbackDate); err != nil {
		return AwardResult{}, fmt.Errorf("cashback: init user_daily_cashback: %w", err)
	}

	var dailyAwarded int64
	if err := tx.QueryRow(ctx, `
		SELECT awarded_total_idr FROM user_daily_cashback
		WHERE user_id = $1 AND cashback_date = $2
		FOR UPDATE
	`, in.UserID, cashbackDate).Scan(&dailyAwarded); err != nil {
		return AwardResult{}, fmt.Errorf("cashback: lock user_daily_cashback: %w", err)
	}

	budgetRemaining := totalBudget - budgetAwarded
	dailyRemaining := DailyCapIDR - dailyAwarded
	base := in.AmountIDR * CashbackRateNumerator / CashbackRateDenominator
	awarded, reason := computeAward(in.AmountIDR, dailyRemaining, budgetRemaining)

	if _, err := tx.Exec(ctx, `
		UPDATE payments
		SET base_cashback_idr = $1, awarded_cashback_idr = $2, reason_code = $3, processed_at = now()
		WHERE payment_id = $4
	`, base, awarded, reason, in.PaymentID); err != nil {
		return AwardResult{}, fmt.Errorf("cashback: finalize payment: %w", err)
	}

	var balanceAfter int64
	if awarded > 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE campaign_budget SET awarded_total_idr = awarded_total_idr + $1, updated_at = now() WHERE id = 1
		`, awarded); err != nil {
			return AwardResult{}, fmt.Errorf("cashback: update campaign_budget: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE user_daily_cashback SET awarded_total_idr = awarded_total_idr + $1
			WHERE user_id = $2 AND cashback_date = $3
		`, awarded, in.UserID, cashbackDate); err != nil {
			return AwardResult{}, fmt.Errorf("cashback: update user_daily_cashback: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO ledger (user_id, entry_type, amount_idr, ref_type, ref_id)
			VALUES ($1, 'AWARD', $2, 'PAYMENT', $3)
		`, in.UserID, awarded, in.PaymentID); err != nil {
			return AwardResult{}, fmt.Errorf("cashback: insert ledger: %w", err)
		}

		if err := tx.QueryRow(ctx, `
			INSERT INTO user_balance (user_id, balance_idr, updated_at)
			VALUES ($1, $2, now())
			ON CONFLICT (user_id) DO UPDATE
				SET balance_idr = user_balance.balance_idr + EXCLUDED.balance_idr, updated_at = now()
			RETURNING balance_idr
		`, in.UserID, awarded).Scan(&balanceAfter); err != nil {
			return AwardResult{}, fmt.Errorf("cashback: update user_balance: %w", err)
		}
	} else {
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE((SELECT balance_idr FROM user_balance WHERE user_id = $1), 0)
		`, in.UserID).Scan(&balanceAfter); err != nil {
			return AwardResult{}, fmt.Errorf("cashback: read user_balance: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return AwardResult{}, fmt.Errorf("cashback: commit: %w", err)
	}

	return AwardResult{AwardedAmountIDR: awarded, ReasonCode: reason, BalanceAfterIDR: balanceAfter}, nil
}

// existingAward returns the previously-computed result for paymentID, if any.
func existingAward(ctx context.Context, db *pgxpool.Pool, paymentID string) (AwardResult, bool, error) {
	var userID string
	var awarded int64
	var reason string
	var processedAt any
	err := db.QueryRow(ctx, `
		SELECT user_id, awarded_cashback_idr, reason_code, processed_at
		FROM payments WHERE payment_id = $1
	`, paymentID).Scan(&userID, &awarded, &reason, &processedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AwardResult{}, false, nil
	}
	if err != nil {
		return AwardResult{}, false, fmt.Errorf("cashback: lookup payment: %w", err)
	}
	if processedAt == nil {
		// A claim row exists (INSERT succeeded) but hasn't been finalized
		// yet - the owning transaction hasn't committed. Treat as not-found;
		// the caller that owns the claim will finish it.
		return AwardResult{}, false, nil
	}

	var balance int64
	if err := db.QueryRow(ctx, `
		SELECT COALESCE((SELECT balance_idr FROM user_balance WHERE user_id = $1), 0)
	`, userID).Scan(&balance); err != nil {
		return AwardResult{}, false, fmt.Errorf("cashback: lookup balance: %w", err)
	}

	return AwardResult{AwardedAmountIDR: awarded, ReasonCode: ReasonCode(reason), BalanceAfterIDR: balance}, true, nil
}
