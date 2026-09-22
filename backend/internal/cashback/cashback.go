// Package cashback implements the cashback earning, budget/daily-cap
// enforcement, and redemption logic. See spec/design.md and
// spec/invariants.md for the rules this package must uphold.
package cashback

import "time"

// ReasonCode explains why a payment was awarded the amount it was (FR-09).
type ReasonCode string

const (
	ReasonAwarded         ReasonCode = "AWARDED"
	ReasonPartialDailyCap ReasonCode = "PARTIAL_DAILY_CAP"
	ReasonPartialBudget   ReasonCode = "PARTIAL_BUDGET"
	ReasonBelowMinimum    ReasonCode = "BELOW_MINIMUM"
	ReasonDailyCapReached ReasonCode = "DAILY_CAP_REACHED"
	ReasonCampaignEnded   ReasonCode = "CAMPAIGN_ENDED"
)

// Business constants. All money is integer IDR (NFR-01).
const (
	// MinEligibleAmountIDR: payments under this earn nothing (FR-02).
	MinEligibleAmountIDR int64 = 20_000

	// CashbackRateNumerator / CashbackRateDenominator: 5% (FR-03).
	CashbackRateNumerator   int64 = 5
	CashbackRateDenominator int64 = 100

	// DailyCapIDR: max a single user can earn per WIB calendar day (FR-05).
	DailyCapIDR int64 = 50_000

	// TotalCampaignBudgetIDR mirrors the seeded campaign_budget row
	// (0001_init.up.sql); kept here too for tests that don't hit Postgres.
	TotalCampaignBudgetIDR int64 = 10_000_000

	// MinRedemptionIDR: decisions.md - "Minimum redemption amount: 1,000 IDR".
	MinRedemptionIDR int64 = 1_000
)

// MaxFutureClockSkew: how far into the future a payment's paid_at may be,
// tolerating clock skew between the payment source and this service while
// still rejecting evidently-impossible timestamps. decisions.md
// "paid_at bounds" - no lower bound: backfill/replay with delay is normal.
const MaxFutureClockSkew = 5 * time.Minute

// Timezone is the fixed offset used to derive a payment's calendar day for
// the daily cap (FR-05). Asia/Jakarta has no DST, so a fixed offset is safe
// (see spec/risks.md "Timezone assumption").
var Timezone = time.FixedZone("WIB", 7*60*60)

// PaymentInput is a payment-succeeded event (FR-01).
type PaymentInput struct {
	PaymentID string
	UserID    string
	AmountIDR int64
	PaidAt    time.Time
}

// AwardResult is the outcome of processing a payment.
type AwardResult struct {
	AwardedAmountIDR int64
	ReasonCode       ReasonCode
	BalanceAfterIDR  int64
}

// RedemptionInput is a redemption request (FR-14).
type RedemptionInput struct {
	UserID         string
	AmountIDR      int64
	IdempotencyKey string
}

// RedemptionResult is the outcome of a redemption request.
type RedemptionResult struct {
	RedemptionID  string
	NewBalanceIDR int64
}

// DailyUsage backs GET /cashback/daily (FR-11).
type DailyUsage struct {
	EarnedTodayIDR    int64
	RemainingTodayIDR int64
}

// CampaignStatus backs GET /campaign (FR-13). Deliberately does not expose
// the remaining budget - see design.md §7.
type CampaignStatus struct {
	Active bool
}

// HistoryEntry backs GET /cashback/history (FR-12). ReasonCode is set for
// AWARD entries (including zero-award outcomes like BELOW_MINIMUM, which
// have no ledger row but still belong in history) and empty for REDEEM.
type HistoryEntry struct {
	EntryType  string
	AmountIDR  int64
	RefType    string
	RefID      string
	ReasonCode ReasonCode
	CreatedAt  time.Time
}
