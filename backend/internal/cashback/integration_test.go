//go:build integration

package cashback

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/basonipresent/flash-cashback/backend/internal/migrate"
)

// testDB returns a pool against a real Postgres, migrated and truncated for
// this test. Requires TEST_DATABASE_URL (falls back to DATABASE_URL);
// skips (not fails) if neither is set, so `make test` never needs a DB.
func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL (or DATABASE_URL) not set, skipping integration test")
	}

	if err := migrate.Up(dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(db.Close)

	// Isolate this test from any other: truncate everything (including
	// users - all the FK columns reference it, so it has to be truncated
	// in the same statement) and reseed the singleton campaign_budget row
	// exactly as the initial migration does.
	_, err = db.Exec(ctx, `
		TRUNCATE payments, campaign_budget, user_daily_cashback, ledger, user_balance, redemptions, users;
		INSERT INTO campaign_budget (id, total_budget_idr, awarded_total_idr) VALUES (1, 10000000, 0);
	`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}

	return db
}

// createTestUser inserts a users row directly via SQL and returns its UUID.
// Every user_id column is FK-constrained to users(id) (decisions.md "User
// identity"), so tests can't just make up a string anymore - and there's no
// API/package to go through either, since users are meant to be populated
// by a SQL script, not application code.
func createTestUser(t *testing.T, ctx context.Context, db *pgxpool.Pool, name string) string {
	t.Helper()
	id := uuid.New().String()
	if _, err := db.Exec(ctx, `INSERT INTO users (id, name) VALUES ($1::uuid, $2)`, id, name); err != nil {
		t.Fatalf("createTestUser(%q): %v", name, err)
	}
	return id
}

// TestConcurrentPayments_DailyCap is INV-06: many concurrent payments for
// one user on one day, requesting far more than the 50,000 IDR daily cap,
// must never let the user's daily total exceed it.
func TestConcurrentPayments_DailyCap(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	const (
		numPayments  = 20
		amountPerPay = 100_000 // base cashback 5,000 each; 20 * 5,000 = 100,000 requested vs 50,000 cap
	)
	userID := createTestUser(t, ctx, db, "user-daily-cap")
	paidAt := time.Now()

	var wg sync.WaitGroup
	for i := 0; i < numPayments; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := AwardPayment(ctx, db, PaymentInput{
				PaymentID: fmt.Sprintf("daily-cap-%d", i),
				UserID:    userID,
				AmountIDR: amountPerPay,
				PaidAt:    paidAt,
			})
			if err != nil {
				t.Errorf("AwardPayment: %v", err)
			}
		}(i)
	}
	wg.Wait()

	usage, err := GetDailyUsage(ctx, db, userID, paidAt)
	if err != nil {
		t.Fatalf("GetDailyUsage: %v", err)
	}
	if usage.EarnedTodayIDR != DailyCapIDR {
		t.Errorf("earned today = %d, want exactly the cap %d (never more, and all 20 requests together exceed it so it should be fully consumed)", usage.EarnedTodayIDR, DailyCapIDR)
	}

	var ledgerSum int64
	if err := db.QueryRow(ctx, `SELECT COALESCE(SUM(amount_idr), 0) FROM ledger WHERE user_id = $1 AND entry_type = 'AWARD'`, userID).Scan(&ledgerSum); err != nil {
		t.Fatalf("sum ledger: %v", err)
	}
	if ledgerSum != DailyCapIDR {
		t.Errorf("ledger AWARD sum = %d, want %d (INV-07: counter must match ledger)", ledgerSum, DailyCapIDR)
	}
}

// TestConcurrentPayments_CampaignBudget is INV-04/INV-05: many concurrent
// payments across many users, requesting far more than the 10,000,000 IDR
// campaign budget, must never let the total awarded exceed it.
func TestConcurrentPayments_CampaignBudget(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	const (
		numPayments  = 300
		amountPerPay = 1_000_000 // base cashback 50,000 each; 300 * 50,000 = 15,000,000 requested vs 10,000,000 budget
	)
	paidAt := time.Now()

	// Distinct users so the daily cap never binds first.
	userIDs := make([]string, numPayments)
	for i := 0; i < numPayments; i++ {
		userIDs[i] = createTestUser(t, ctx, db, fmt.Sprintf("user-budget-%d", i))
	}

	var wg sync.WaitGroup
	for i := 0; i < numPayments; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := AwardPayment(ctx, db, PaymentInput{
				PaymentID: fmt.Sprintf("budget-%d", i),
				UserID:    userIDs[i],
				AmountIDR: amountPerPay,
				PaidAt:    paidAt,
			})
			if err != nil {
				t.Errorf("AwardPayment: %v", err)
			}
		}(i)
	}
	wg.Wait()

	status, err := GetCampaignStatus(ctx, db)
	if err != nil {
		t.Fatalf("GetCampaignStatus: %v", err)
	}
	if status.Active {
		t.Errorf("campaign should be ended: requested total far exceeds the budget")
	}

	var budgetAwarded int64
	if err := db.QueryRow(ctx, `SELECT awarded_total_idr FROM campaign_budget WHERE id = 1`).Scan(&budgetAwarded); err != nil {
		t.Fatalf("read campaign_budget: %v", err)
	}
	if budgetAwarded > TotalCampaignBudgetIDR {
		t.Fatalf("campaign_budget.awarded_total_idr = %d, exceeds budget %d (INV-04 violated)", budgetAwarded, TotalCampaignBudgetIDR)
	}
	if budgetAwarded != TotalCampaignBudgetIDR {
		t.Errorf("campaign_budget.awarded_total_idr = %d, want exactly %d (requested total exceeds it, so it should be fully consumed)", budgetAwarded, TotalCampaignBudgetIDR)
	}

	var ledgerSum int64
	if err := db.QueryRow(ctx, `SELECT COALESCE(SUM(amount_idr), 0) FROM ledger WHERE entry_type = 'AWARD'`).Scan(&ledgerSum); err != nil {
		t.Fatalf("sum ledger: %v", err)
	}
	if ledgerSum != budgetAwarded {
		t.Errorf("ledger AWARD sum = %d, want %d (INV-05: counter must match ledger)", ledgerSum, budgetAwarded)
	}
}

// TestConcurrentPayments_SameIDIdempotent is INV-11: the same payment_id
// submitted concurrently must award at most once.
func TestConcurrentPayments_SameIDIdempotent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	const (
		paymentID = "duplicate-payment"
		amount    = 100_000
	)
	userID := createTestUser(t, ctx, db, "user-idempotent")
	paidAt := time.Now()

	const numAttempts = 10
	results := make([]AwardResult, numAttempts)
	errs := make([]error, numAttempts)

	var wg sync.WaitGroup
	for i := 0; i < numAttempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = AwardPayment(ctx, db, PaymentInput{
				PaymentID: paymentID,
				UserID:    userID,
				AmountIDR: amount,
				PaidAt:    paidAt,
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("attempt %d: AwardPayment: %v", i, err)
		}
	}
	for i := 1; i < numAttempts; i++ {
		if results[i] != results[0] {
			t.Errorf("attempt %d result %+v differs from attempt 0 result %+v", i, results[i], results[0])
		}
	}

	var ledgerCount int
	if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM ledger WHERE user_id = $1 AND ref_id = $2`, userID, paymentID).Scan(&ledgerCount); err != nil {
		t.Fatalf("count ledger: %v", err)
	}
	if ledgerCount != 1 {
		t.Errorf("ledger has %d AWARD entries for payment %q, want exactly 1 (INV-11)", ledgerCount, paymentID)
	}
}

// TestConcurrentRedemptions_SameKeyIdempotent is INV-12/INV-01: the same
// redemption idempotency key submitted concurrently must decrement the
// balance at most once.
func TestConcurrentRedemptions_SameKeyIdempotent(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	userID := createTestUser(t, ctx, db, "user-redeem-idempotent")
	seedBalance(t, ctx, db, userID, 100_000)

	key := uuid.New().String()
	const numAttempts = 10
	results := make([]RedemptionResult, numAttempts)
	errs := make([]error, numAttempts)

	var wg sync.WaitGroup
	for i := 0; i < numAttempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = Redeem(ctx, db, RedemptionInput{
				UserID:         userID,
				AmountIDR:      10_000,
				IdempotencyKey: key,
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("attempt %d: Redeem: %v", i, err)
		}
	}
	for i := 1; i < numAttempts; i++ {
		if results[i] != results[0] {
			t.Errorf("attempt %d result %+v differs from attempt 0 result %+v", i, results[i], results[0])
		}
	}

	balance, err := GetBalance(ctx, db, userID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if balance != 90_000 {
		t.Errorf("balance = %d, want 90,000 (started at 100,000, redeemed 10,000 exactly once - INV-12)", balance)
	}
	if balance < 0 {
		t.Fatalf("balance is negative (INV-01 violated)")
	}
}

// TestRedeem_IdempotencyKeyConflict covers decisions.md "Redemption
// idempotency-key conflict": reusing a key with a different amount is
// rejected, and does not touch the balance a second time.
func TestRedeem_IdempotencyKeyConflict(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	userID := createTestUser(t, ctx, db, "user-key-conflict")
	seedBalance(t, ctx, db, userID, 100_000)

	key := uuid.New().String()

	first, err := Redeem(ctx, db, RedemptionInput{UserID: userID, AmountIDR: 10_000, IdempotencyKey: key})
	if err != nil {
		t.Fatalf("first Redeem: %v", err)
	}

	_, err = Redeem(ctx, db, RedemptionInput{UserID: userID, AmountIDR: 20_000, IdempotencyKey: key})
	if err != ErrIdempotencyKeyConflict {
		t.Fatalf("second Redeem (different amount, same key): got err %v, want ErrIdempotencyKeyConflict", err)
	}

	balance, err := GetBalance(ctx, db, userID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if balance != first.NewBalanceIDR {
		t.Errorf("balance = %d, want unchanged %d after the rejected conflicting retry", balance, first.NewBalanceIDR)
	}
}

// TestRedeem_InsufficientBalanceRejectedWithoutMutation is INV-01/INV-15:
// an over-balance redemption must be rejected and must not touch the balance.
func TestRedeem_InsufficientBalanceRejectedWithoutMutation(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	userID := createTestUser(t, ctx, db, "user-insufficient")
	seedBalance(t, ctx, db, userID, 5_000)

	_, err := Redeem(ctx, db, RedemptionInput{
		UserID:         userID,
		AmountIDR:      10_000,
		IdempotencyKey: uuid.New().String(),
	})
	if err != ErrInsufficientBalance {
		t.Fatalf("Redeem: got err %v, want ErrInsufficientBalance", err)
	}

	balance, err := GetBalance(ctx, db, userID)
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if balance != 5_000 {
		t.Errorf("balance = %d, want unchanged 5,000 after a rejected redemption", balance)
	}
}

// TestRedeem_BelowMinimumRejected covers FR-14 / decisions.md's 1,000 IDR
// minimum redemption amount.
func TestRedeem_BelowMinimumRejected(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	userID := createTestUser(t, ctx, db, "user-below-min")
	seedBalance(t, ctx, db, userID, 100_000)

	_, err := Redeem(ctx, db, RedemptionInput{
		UserID:         userID,
		AmountIDR:      MinRedemptionIDR - 1,
		IdempotencyKey: uuid.New().String(),
	})
	if err != ErrBelowMinimumRedemption {
		t.Fatalf("Redeem: got err %v, want ErrBelowMinimumRedemption", err)
	}
}

// TestRedeem_AvailableAfterCampaignEnded is INV-13: redemption keeps working
// even once the campaign budget is fully exhausted.
func TestRedeem_AvailableAfterCampaignEnded(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	userID := createTestUser(t, ctx, db, "user-post-campaign")
	seedBalance(t, ctx, db, userID, 50_000)

	// Exhaust the campaign budget directly.
	if _, err := db.Exec(ctx, `UPDATE campaign_budget SET awarded_total_idr = total_budget_idr WHERE id = 1`); err != nil {
		t.Fatalf("exhaust budget: %v", err)
	}

	status, err := GetCampaignStatus(ctx, db)
	if err != nil {
		t.Fatalf("GetCampaignStatus: %v", err)
	}
	if status.Active {
		t.Fatalf("expected campaign to be ended")
	}

	result, err := Redeem(ctx, db, RedemptionInput{
		UserID:         userID,
		AmountIDR:      10_000,
		IdempotencyKey: uuid.New().String(),
	})
	if err != nil {
		t.Fatalf("Redeem after campaign ended: %v", err)
	}
	if result.NewBalanceIDR != 40_000 {
		t.Errorf("balance after redemption = %d, want 40,000", result.NewBalanceIDR)
	}
}

// TestAwardPayment_UnknownUserRejected covers decisions.md "User identity":
// a payment for a user_id that doesn't reference a real users row is
// rejected via the FK constraint, not silently accepted.
func TestAwardPayment_UnknownUserRejected(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	_, err := AwardPayment(ctx, db, PaymentInput{
		PaymentID: "pay-unknown-user",
		UserID:    uuid.New().String(), // never created
		AmountIDR: 100_000,
		PaidAt:    time.Now(),
	})
	if err != ErrUserNotFound {
		t.Fatalf("AwardPayment: got err %v, want ErrUserNotFound", err)
	}
}

// TestRedeem_UnknownUserRejected is the same, for redemptions.
func TestRedeem_UnknownUserRejected(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	_, err := Redeem(ctx, db, RedemptionInput{
		UserID:         uuid.New().String(), // never created
		AmountIDR:      10_000,
		IdempotencyKey: uuid.New().String(),
	})
	if err != ErrUserNotFound {
		t.Fatalf("Redeem: got err %v, want ErrUserNotFound", err)
	}
}

// seedBalance gives userID a starting balance directly, bypassing
// AwardPayment (and so the daily/budget caps it enforces) - the award path
// has its own dedicated tests above; these redemption tests just need an
// arbitrary starting balance, including ones above the 50,000 daily cap.
func seedBalance(t *testing.T, ctx context.Context, db *pgxpool.Pool, userID string, amountIDR int64) {
	t.Helper()

	_, err := db.Exec(ctx, `
		INSERT INTO user_balance (user_id, balance_idr) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET balance_idr = EXCLUDED.balance_idr
	`, userID, amountIDR)
	if err != nil {
		t.Fatalf("seedBalance: %v", err)
	}

	_, err = db.Exec(ctx, `
		INSERT INTO ledger (user_id, entry_type, amount_idr, ref_type, ref_id)
		VALUES ($1, 'AWARD', $2, 'PAYMENT', 'seed')
	`, userID, amountIDR)
	if err != nil {
		t.Fatalf("seedBalance: insert ledger: %v", err)
	}
}
