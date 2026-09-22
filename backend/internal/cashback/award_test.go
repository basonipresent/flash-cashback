package cashback

import (
	"testing"
	"time"
)

func TestComputeAward(t *testing.T) {
	tests := []struct {
		name            string
		amount          int64
		dailyRemaining  int64
		budgetRemaining int64
		wantAwarded     int64
		wantReason      ReasonCode
	}{
		{
			name:   "below minimum eligible amount", // FR-02
			amount: 19_999, dailyRemaining: 50_000, budgetRemaining: 10_000_000,
			wantAwarded: 0, wantReason: ReasonBelowMinimum,
		},
		{
			name:   "exactly at minimum eligible amount is eligible", // FR-02
			amount: 20_000, dailyRemaining: 50_000, budgetRemaining: 10_000_000,
			wantAwarded: 1_000, wantReason: ReasonAwarded,
		},
		{
			name:   "floor rounding on a non-round base", // FR-03
			amount: 39_999, dailyRemaining: 50_000, budgetRemaining: 10_000_000,
			wantAwarded: 1_999, wantReason: ReasonAwarded, // floor(39999*5/100) = floor(1999.95) = 1999
		},
		{
			name:   "campaign budget already exhausted", // FR-07
			amount: 100_000, dailyRemaining: 50_000, budgetRemaining: 0,
			wantAwarded: 0, wantReason: ReasonCampaignEnded,
		},
		{
			name:   "daily cap already reached",
			amount: 100_000, dailyRemaining: 0, budgetRemaining: 10_000_000,
			wantAwarded: 0, wantReason: ReasonDailyCapReached,
		},
		{
			name:   "partial award bound by the daily cap", // FR-04
			amount: 100_000, dailyRemaining: 3_000, budgetRemaining: 10_000_000,
			wantAwarded: 3_000, wantReason: ReasonPartialDailyCap,
		},
		{
			name:   "partial award bound by the campaign budget", // FR-04
			amount: 100_000, dailyRemaining: 50_000, budgetRemaining: 2_000,
			wantAwarded: 2_000, wantReason: ReasonPartialBudget,
		},
		{
			name:   "both caps bind at exactly the same value - budget wins the tie", // decisions.md "Tie-break reason code"
			amount: 100_000, dailyRemaining: 3_000, budgetRemaining: 3_000,
			wantAwarded: 3_000, wantReason: ReasonPartialBudget,
		},
		{
			name:   "daily cap strictly smaller than budget remaining", // not a tie - daily genuinely binds
			amount: 100_000, dailyRemaining: 3_000, budgetRemaining: 3_001,
			wantAwarded: 3_000, wantReason: ReasonPartialDailyCap,
		},
		{
			name:   "remaining exactly equals base is a full award, not partial",
			amount: 20_000, dailyRemaining: 1_000, budgetRemaining: 1_000,
			wantAwarded: 1_000, wantReason: ReasonAwarded,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAwarded, gotReason := computeAward(tt.amount, tt.dailyRemaining, tt.budgetRemaining)
			if gotAwarded != tt.wantAwarded || gotReason != tt.wantReason {
				t.Errorf("computeAward(%d, %d, %d) = (%d, %s), want (%d, %s)",
					tt.amount, tt.dailyRemaining, tt.budgetRemaining,
					gotAwarded, gotReason, tt.wantAwarded, tt.wantReason)
			}
		})
	}
}

func TestValidatePaidAt(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		paidAt  time.Time
		wantErr error
	}{
		{"exactly now", now, nil},
		{"in the past is always fine, however far", now.Add(-365 * 24 * time.Hour), nil},
		{"within the clock-skew tolerance", now.Add(MaxFutureClockSkew - time.Second), nil},
		{"exactly at the tolerance boundary", now.Add(MaxFutureClockSkew), nil},
		{"just past the tolerance boundary", now.Add(MaxFutureClockSkew + time.Second), ErrFuturePaymentTimestamp},
		{"far in the future", now.Add(24 * time.Hour), ErrFuturePaymentTimestamp},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validatePaidAt(tt.paidAt, now); err != tt.wantErr {
				t.Errorf("validatePaidAt(%v, %v) = %v, want %v", tt.paidAt, now, err, tt.wantErr)
			}
		})
	}
}
