//go:build integration

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/basonipresent/flash-cashback/backend/internal/migrate"
)

// newTestRouter builds a real router against Postgres. Requires
// TEST_DATABASE_URL (falls back to DATABASE_URL); skips (not fails) if
// neither is set, so `make test` never needs a DB. Redis is wired up but
// never exercised by these tests (only /readyz touches it), so it doesn't
// need to be reachable.
func newTestRouter(t *testing.T) http.Handler {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL (or DATABASE_URL) not set, skipping HTTP integration test")
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

	_, err = db.Exec(ctx, `
		TRUNCATE payments, campaign_budget, user_daily_cashback, ledger, user_balance, redemptions;
		INSERT INTO campaign_budget (id, total_budget_idr, awarded_total_idr) VALUES (1, 10000000, 0);
	`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}

	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		redisURL = os.Getenv("REDIS_URL")
	}
	if redisURL == "" {
		redisURL = "redis://localhost:6379/0"
	}
	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	rdb := redis.NewClient(redisOpts)
	t.Cleanup(func() { _ = rdb.Close() })

	return NewRouter(db, rdb)
}

// doRequest performs a request against handler and returns the recorded
// response, decoding a JSON body into out if it's non-nil.
func doRequest(t *testing.T, handler http.Handler, method, path string, headers map[string]string, body any, out any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if out != nil {
		if err := json.NewDecoder(rec.Body).Decode(out); err != nil {
			t.Fatalf("decode response body %q: %v", rec.Body.String(), err)
		}
	}

	return rec
}

func TestPostPayment_HappyPath(t *testing.T) {
	router := newTestRouter(t)

	var resp postPaymentResponse
	rec := doRequest(t, router, http.MethodPost, "/payments", nil, postPaymentRequest{
		PaymentID: "pay-http-1",
		UserID:    "alice",
		AmountIDR: 100_000, // base cashback 5,000
		PaidAt:    time.Now().Format(time.RFC3339),
	}, &resp)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if resp.AwardedAmountIDR != 5_000 || resp.ReasonCode != "AWARDED" || resp.BalanceAfterIDR != 5_000 {
		t.Errorf("got %+v, want awarded=5000 reason=AWARDED balance=5000", resp)
	}
}

func TestPostPayment_IdempotentRetry(t *testing.T) {
	router := newTestRouter(t)

	req := postPaymentRequest{
		PaymentID: "pay-http-retry",
		UserID:    "alice",
		AmountIDR: 100_000,
		PaidAt:    time.Now().Format(time.RFC3339),
	}

	var first, second postPaymentResponse
	doRequest(t, router, http.MethodPost, "/payments", nil, req, &first)
	rec := doRequest(t, router, http.MethodPost, "/payments", nil, req, &second)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if first != second {
		t.Errorf("retried payment returned a different result: first=%+v second=%+v", first, second)
	}
}

func TestPostPayment_Validation(t *testing.T) {
	router := newTestRouter(t)

	tests := []struct {
		name string
		body postPaymentRequest
	}{
		{"missing payment_id", postPaymentRequest{UserID: "alice", AmountIDR: 100_000, PaidAt: time.Now().Format(time.RFC3339)}},
		{"missing user_id", postPaymentRequest{PaymentID: "p1", AmountIDR: 100_000, PaidAt: time.Now().Format(time.RFC3339)}},
		{"zero amount", postPaymentRequest{PaymentID: "p2", UserID: "alice", AmountIDR: 0, PaidAt: time.Now().Format(time.RFC3339)}},
		{"negative amount", postPaymentRequest{PaymentID: "p3", UserID: "alice", AmountIDR: -1, PaidAt: time.Now().Format(time.RFC3339)}},
		{"unparseable paid_at", postPaymentRequest{PaymentID: "p4", UserID: "alice", AmountIDR: 100_000, PaidAt: "not-a-date"}},
		{"paid_at too far in the future", postPaymentRequest{PaymentID: "p5", UserID: "alice", AmountIDR: 100_000, PaidAt: time.Now().Add(time.Hour).Format(time.RFC3339)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(t, router, http.MethodPost, "/payments", nil, tt.body, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestPostPayment_NearFutureWithinClockSkewToleranceAccepted(t *testing.T) {
	router := newTestRouter(t)

	var resp postPaymentResponse
	rec := doRequest(t, router, http.MethodPost, "/payments", nil, postPaymentRequest{
		PaymentID: "pay-near-future",
		UserID:    "alice",
		AmountIDR: 100_000,
		PaidAt:    time.Now().Add(2 * time.Minute).Format(time.RFC3339), // within cashback.MaxFutureClockSkew (5m)
	}, &resp)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if resp.ReasonCode != "AWARDED" {
		t.Errorf("reason_code = %q, want AWARDED", resp.ReasonCode)
	}
}

func TestPostPayment_MalformedJSON(t *testing.T) {
	router := newTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/payments", bytes.NewReader([]byte("{not json")))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestGetBalance_RequiresUserID(t *testing.T) {
	router := newTestRouter(t)

	rec := doRequest(t, router, http.MethodGet, "/cashback/balance", nil, nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestGetBalance_AfterPayment(t *testing.T) {
	router := newTestRouter(t)

	doRequest(t, router, http.MethodPost, "/payments", nil, postPaymentRequest{
		PaymentID: "pay-balance", UserID: "bob", AmountIDR: 100_000, PaidAt: time.Now().Format(time.RFC3339),
	}, nil)

	var balance map[string]int64
	rec := doRequest(t, router, http.MethodGet, "/cashback/balance", map[string]string{"X-User-Id": "bob"}, nil, &balance)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if balance["balance"] != 5_000 {
		t.Errorf("balance = %d, want 5000", balance["balance"])
	}
}

func TestGetDaily_AfterPayment(t *testing.T) {
	router := newTestRouter(t)

	doRequest(t, router, http.MethodPost, "/payments", nil, postPaymentRequest{
		PaymentID: "pay-daily", UserID: "carol", AmountIDR: 100_000, PaidAt: time.Now().Format(time.RFC3339),
	}, nil)

	var daily map[string]int64
	rec := doRequest(t, router, http.MethodGet, "/cashback/daily", map[string]string{"X-User-Id": "carol"}, nil, &daily)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if daily["earned_today"] != 5_000 || daily["remaining_today"] != 45_000 {
		t.Errorf("got %+v, want earned_today=5000 remaining_today=45000", daily)
	}
}

func TestGetHistory_AfterPayment(t *testing.T) {
	router := newTestRouter(t)

	doRequest(t, router, http.MethodPost, "/payments", nil, postPaymentRequest{
		PaymentID: "pay-history", UserID: "dave", AmountIDR: 100_000, PaidAt: time.Now().Format(time.RFC3339),
	}, nil)

	var entries []historyEntryResponse
	rec := doRequest(t, router, http.MethodGet, "/cashback/history", map[string]string{"X-User-Id": "dave"}, nil, &entries)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(entries) != 1 || entries[0].EntryType != "AWARD" || entries[0].AmountIDR != 5_000 ||
		entries[0].RefID != "pay-history" || entries[0].ReasonCode != "AWARDED" {
		t.Errorf("got %+v, want one AWARD entry of 5000 reason=AWARDED referencing pay-history", entries)
	}
}

// TestGetHistory_IncludesZeroAwardPayments covers FR-12 ("including the
// reason for each payment outcome"): a payment that earned nothing still
// has to show up in history, since it never gets a ledger row (AwardPayment
// only writes one when awarded > 0).
func TestGetHistory_IncludesZeroAwardPayments(t *testing.T) {
	router := newTestRouter(t)

	doRequest(t, router, http.MethodPost, "/payments", nil, postPaymentRequest{
		PaymentID: "pay-below-min", UserID: "hank", AmountIDR: 10_000, PaidAt: time.Now().Format(time.RFC3339),
	}, nil)

	var entries []historyEntryResponse
	rec := doRequest(t, router, http.MethodGet, "/cashback/history", map[string]string{"X-User-Id": "hank"}, nil, &entries)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(entries) != 1 || entries[0].EntryType != "AWARD" || entries[0].AmountIDR != 0 ||
		entries[0].RefID != "pay-below-min" || entries[0].ReasonCode != "BELOW_MINIMUM" {
		t.Errorf("got %+v, want one AWARD entry of 0 reason=BELOW_MINIMUM referencing pay-below-min", entries)
	}
}

func TestPostRedemption_HappyPath(t *testing.T) {
	router := newTestRouter(t)

	doRequest(t, router, http.MethodPost, "/payments", nil, postPaymentRequest{
		PaymentID: "pay-redeem", UserID: "erin", AmountIDR: 100_000, PaidAt: time.Now().Format(time.RFC3339),
	}, nil)

	var resp postRedemptionResponse
	rec := doRequest(t, router, http.MethodPost, "/cashback/redemptions",
		map[string]string{"X-User-Id": "erin", "Idempotency-Key": "redeem-http-1"},
		postRedemptionRequest{AmountIDR: 2_000}, &resp)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if resp.NewBalanceIDR != 3_000 || resp.RedemptionID == "" {
		t.Errorf("got %+v, want new_balance=3000 and a non-empty redemption_id", resp)
	}
}

func TestPostRedemption_IdempotencyKeyConflict(t *testing.T) {
	router := newTestRouter(t)

	doRequest(t, router, http.MethodPost, "/payments", nil, postPaymentRequest{
		PaymentID: "pay-redeem-conflict", UserID: "grant", AmountIDR: 100_000, PaidAt: time.Now().Format(time.RFC3339),
	}, nil)

	headers := map[string]string{"X-User-Id": "grant", "Idempotency-Key": "redeem-http-conflict"}

	rec := doRequest(t, router, http.MethodPost, "/cashback/redemptions", headers, postRedemptionRequest{AmountIDR: 2_000}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(t, router, http.MethodPost, "/cashback/redemptions", headers, postRedemptionRequest{AmountIDR: 3_000}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second request (different amount, same key) status = %d, want 409, body = %s", rec.Code, rec.Body.String())
	}

	var balance map[string]int64
	doRequest(t, router, http.MethodGet, "/cashback/balance", map[string]string{"X-User-Id": "grant"}, nil, &balance)
	if balance["balance"] != 3_000 {
		t.Errorf("balance = %d, want 3000 (only the first redemption should have applied)", balance["balance"])
	}
}

func TestPostRedemption_IdempotentRetry(t *testing.T) {
	router := newTestRouter(t)

	doRequest(t, router, http.MethodPost, "/payments", nil, postPaymentRequest{
		PaymentID: "pay-redeem-retry", UserID: "frank", AmountIDR: 100_000, PaidAt: time.Now().Format(time.RFC3339),
	}, nil)

	headers := map[string]string{"X-User-Id": "frank", "Idempotency-Key": "redeem-http-retry"}
	body := postRedemptionRequest{AmountIDR: 2_000}

	var first, second postRedemptionResponse
	doRequest(t, router, http.MethodPost, "/cashback/redemptions", headers, body, &first)
	rec := doRequest(t, router, http.MethodPost, "/cashback/redemptions", headers, body, &second)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if first != second {
		t.Errorf("retried redemption returned a different result: first=%+v second=%+v", first, second)
	}

	var balance map[string]int64
	doRequest(t, router, http.MethodGet, "/cashback/balance", map[string]string{"X-User-Id": "frank"}, nil, &balance)
	if balance["balance"] != 3_000 {
		t.Errorf("balance = %d, want 3000 (redeemed once, not twice)", balance["balance"])
	}
}

func TestPostRedemption_Validation(t *testing.T) {
	router := newTestRouter(t)

	// A payment first, so "insufficient balance" is tested against a real,
	// known-too-small balance rather than an absent user.
	doRequest(t, router, http.MethodPost, "/payments", nil, postPaymentRequest{
		PaymentID: "pay-redeem-validation", UserID: "grace", AmountIDR: 100_000, PaidAt: time.Now().Format(time.RFC3339),
	}, nil)

	tests := []struct {
		name    string
		headers map[string]string
		body    postRedemptionRequest
	}{
		{"missing X-User-Id", map[string]string{"Idempotency-Key": "k1"}, postRedemptionRequest{AmountIDR: 2_000}},
		{"missing Idempotency-Key", map[string]string{"X-User-Id": "grace"}, postRedemptionRequest{AmountIDR: 2_000}},
		{"zero amount", map[string]string{"X-User-Id": "grace", "Idempotency-Key": "k2"}, postRedemptionRequest{AmountIDR: 0}},
		{"below minimum", map[string]string{"X-User-Id": "grace", "Idempotency-Key": "k3"}, postRedemptionRequest{AmountIDR: 500}},
		{"exceeds balance", map[string]string{"X-User-Id": "grace", "Idempotency-Key": "k4"}, postRedemptionRequest{AmountIDR: 999_999}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := doRequest(t, router, http.MethodPost, "/cashback/redemptions", tt.headers, tt.body, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestGetCampaign_Active(t *testing.T) {
	router := newTestRouter(t)

	var status map[string]string
	rec := doRequest(t, router, http.MethodGet, "/campaign", nil, nil, &status)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if status["status"] != "active" {
		t.Errorf(`status = %q, want "active"`, status["status"])
	}
}

func TestGetCampaign_EndedAfterBudgetExhausted(t *testing.T) {
	router := newTestRouter(t)

	// Exhaust the budget directly, same technique as the cashback package's
	// own INV-13 test.
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(context.Background(), `UPDATE campaign_budget SET awarded_total_idr = total_budget_idr WHERE id = 1`); err != nil {
		t.Fatalf("exhaust budget: %v", err)
	}

	var status map[string]string
	rec := doRequest(t, router, http.MethodGet, "/campaign", nil, nil, &status)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if status["status"] != "ended" {
		t.Errorf(`status = %q, want "ended"`, status["status"])
	}
}
