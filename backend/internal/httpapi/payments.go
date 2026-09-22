package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/basonipresent/flash-cashback/backend/internal/cashback"
)

type postPaymentRequest struct {
	PaymentID string `json:"payment_id"`
	UserID    string `json:"user_id"`
	AmountIDR int64  `json:"amount"`
	PaidAt    string `json:"paid_at"` // RFC3339
}

type postPaymentResponse struct {
	AwardedAmountIDR int64  `json:"awarded_amount"`
	ReasonCode       string `json:"reason_code"`
	BalanceAfterIDR  int64  `json:"balance_after"`
}

// handlePostPayment ingests a payment-succeeded event (FR-01). This is a
// system-to-system call - user_id comes from the body, not X-User-Id (see
// spec/design.md §4).
func (d *Deps) handlePostPayment(w http.ResponseWriter, r *http.Request) {
	var req postPaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.PaymentID == "" || req.UserID == "" {
		writeError(w, http.StatusBadRequest, "payment_id and user_id are required")
		return
	}
	if req.AmountIDR <= 0 {
		writeError(w, http.StatusBadRequest, "amount must be positive")
		return
	}

	paidAt, err := time.Parse(time.RFC3339, req.PaidAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "paid_at must be RFC3339")
		return
	}

	result, err := cashback.AwardPayment(r.Context(), d.DB, cashback.PaymentInput{
		PaymentID: req.PaymentID,
		UserID:    req.UserID,
		AmountIDR: req.AmountIDR,
		PaidAt:    paidAt,
	})
	switch {
	case errors.Is(err, cashback.ErrFuturePaymentTimestamp):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to process payment")
		return
	}

	writeJSON(w, http.StatusOK, postPaymentResponse{
		AwardedAmountIDR: result.AwardedAmountIDR,
		ReasonCode:       string(result.ReasonCode),
		BalanceAfterIDR:  result.BalanceAfterIDR,
	})
}
