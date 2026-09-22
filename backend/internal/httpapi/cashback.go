package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/basonipresent/flash-cashback/backend/internal/cashback"
)

// handleGetBalance backs GET /cashback/balance (FR-10).
func (d *Deps) handleGetBalance(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	balance, err := cashback.GetBalance(r.Context(), d.DB, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get balance")
		return
	}

	writeJSON(w, http.StatusOK, map[string]int64{"balance": balance})
}

// handleGetDaily backs GET /cashback/daily (FR-11).
func (d *Deps) handleGetDaily(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	usage, err := cashback.GetDailyUsage(r.Context(), d.DB, userID, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get daily usage")
		return
	}

	writeJSON(w, http.StatusOK, map[string]int64{
		"earned_today":    usage.EarnedTodayIDR,
		"remaining_today": usage.RemainingTodayIDR,
	})
}

type historyEntryResponse struct {
	EntryType  string    `json:"entry_type"`
	AmountIDR  int64     `json:"amount"`
	RefType    string    `json:"ref_type"`
	RefID      string    `json:"ref_id"`
	ReasonCode string    `json:"reason_code"`
	CreatedAt  time.Time `json:"created_at"`
}

// handleGetHistory backs GET /cashback/history (FR-12). ?limit= and
// ?offset= are optional (defaults: 50, 0).
func (d *Deps) handleGetHistory(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	entries, err := cashback.GetHistory(r.Context(), d.DB, userID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get history")
		return
	}

	response := make([]historyEntryResponse, 0, len(entries))
	for _, e := range entries {
		response = append(response, historyEntryResponse{
			EntryType:  e.EntryType,
			AmountIDR:  e.AmountIDR,
			RefType:    e.RefType,
			RefID:      e.RefID,
			ReasonCode: string(e.ReasonCode),
			CreatedAt:  e.CreatedAt,
		})
	}

	writeJSON(w, http.StatusOK, response)
}

type postRedemptionRequest struct {
	AmountIDR int64 `json:"amount"`
}

type postRedemptionResponse struct {
	RedemptionID  string `json:"redemption_id"`
	NewBalanceIDR int64  `json:"new_balance"`
}

// handlePostRedemption backs POST /cashback/redemptions (FR-14–18).
func (d *Deps) handlePostRedemption(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "Idempotency-Key header is required")
		return
	}

	var req postRedemptionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.AmountIDR <= 0 {
		writeError(w, http.StatusBadRequest, "amount must be positive")
		return
	}

	result, err := cashback.Redeem(r.Context(), d.DB, cashback.RedemptionInput{
		UserID:         userID,
		AmountIDR:      req.AmountIDR,
		IdempotencyKey: idempotencyKey,
	})
	switch {
	case errors.Is(err, cashback.ErrBelowMinimumRedemption):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, cashback.ErrInsufficientBalance):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, cashback.ErrIdempotencyKeyConflict):
		writeError(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "failed to process redemption")
		return
	}

	writeJSON(w, http.StatusOK, postRedemptionResponse{
		RedemptionID:  result.RedemptionID,
		NewBalanceIDR: result.NewBalanceIDR,
	})
}
