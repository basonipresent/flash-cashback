package cashback

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// GetBalance backs GET /cashback/balance (FR-10).
func GetBalance(ctx context.Context, db *pgxpool.Pool, userID string) (int64, error) {
	var balance int64
	err := db.QueryRow(ctx, `
		SELECT COALESCE((SELECT balance_idr FROM user_balance WHERE user_id = $1), 0)
	`, userID).Scan(&balance)
	if err != nil {
		return 0, fmt.Errorf("cashback: get balance: %w", err)
	}
	return balance, nil
}

// GetDailyUsage backs GET /cashback/daily (FR-11). now is injected for
// testability; callers pass time.Now().
func GetDailyUsage(ctx context.Context, db *pgxpool.Pool, userID string, now time.Time) (DailyUsage, error) {
	cashbackDate := now.In(Timezone).Format("2006-01-02")

	var earned int64
	err := db.QueryRow(ctx, `
		SELECT COALESCE((
			SELECT awarded_total_idr FROM user_daily_cashback
			WHERE user_id = $1 AND cashback_date = $2
		), 0)
	`, userID, cashbackDate).Scan(&earned)
	if err != nil {
		return DailyUsage{}, fmt.Errorf("cashback: get daily usage: %w", err)
	}

	remaining := DailyCapIDR - earned
	if remaining < 0 {
		remaining = 0
	}

	return DailyUsage{EarnedTodayIDR: earned, RemainingTodayIDR: remaining}, nil
}

// GetCampaignStatus backs GET /campaign (FR-13).
func GetCampaignStatus(ctx context.Context, db *pgxpool.Pool) (CampaignStatus, error) {
	var totalBudget, awarded int64
	err := db.QueryRow(ctx, `
		SELECT total_budget_idr, awarded_total_idr FROM campaign_budget WHERE id = 1
	`).Scan(&totalBudget, &awarded)
	if err != nil {
		return CampaignStatus{}, fmt.Errorf("cashback: get campaign status: %w", err)
	}
	return CampaignStatus{Active: awarded < totalBudget}, nil
}

// GetHistory backs GET /cashback/history (FR-12), newest first. Merges two
// sources so every payment outcome is represented, not just ones that moved
// money: ledger entries (AWARD with amount > 0, and REDEEM - joined back to
// payments for the AWARD reason_code, since ledger itself doesn't carry one)
// and zero-award payments (BELOW_MINIMUM/DAILY_CAP_REACHED/CAMPAIGN_ENDED),
// which never get a ledger row at all (AwardPayment only writes one when
// awarded > 0) but still need to appear here with their reason.
func GetHistory(ctx context.Context, db *pgxpool.Pool, userID string, limit, offset int) ([]HistoryEntry, error) {
	rows, err := db.Query(ctx, `
		SELECT entry_type, amount_idr, ref_type, ref_id, reason_code, created_at
		FROM (
			SELECT
				l.entry_type AS entry_type,
				l.amount_idr AS amount_idr,
				l.ref_type AS ref_type,
				l.ref_id AS ref_id,
				COALESCE(p.reason_code, '') AS reason_code,
				l.created_at AS created_at
			FROM ledger l
			LEFT JOIN payments p ON l.ref_type = 'PAYMENT' AND l.ref_id = p.payment_id
			WHERE l.user_id = $1

			UNION ALL

			SELECT
				'AWARD' AS entry_type,
				0 AS amount_idr,
				'PAYMENT' AS ref_type,
				p.payment_id AS ref_id,
				p.reason_code AS reason_code,
				p.processed_at AS created_at
			FROM payments p
			WHERE p.user_id = $1 AND p.awarded_cashback_idr = 0 AND p.reason_code <> ''
		) combined
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("cashback: get history: %w", err)
	}
	defer rows.Close()

	var entries []HistoryEntry
	for rows.Next() {
		var e HistoryEntry
		if err := rows.Scan(&e.EntryType, &e.AmountIDR, &e.RefType, &e.RefID, &e.ReasonCode, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("cashback: scan history row: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cashback: history rows: %w", err)
	}

	return entries, nil
}
