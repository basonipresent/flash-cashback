// Package httpapi wires up the backend's HTTP routes using only the
// standard library (Go 1.22+ ServeMux method patterns) - no web framework.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Deps are the external dependencies the API handlers need.
type Deps struct {
	DB  *pgxpool.Pool
	RDB *redis.Client
}

// NewRouter builds the backend's HTTP handler.
func NewRouter(db *pgxpool.Pool, rdb *redis.Client) http.Handler {
	deps := &Deps{DB: db, RDB: rdb}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz)
	mux.HandleFunc("GET /readyz", deps.handleReadyz)

	mux.HandleFunc("POST /payments", deps.handlePostPayment)

	mux.HandleFunc("GET /cashback/balance", deps.handleGetBalance)
	mux.HandleFunc("GET /cashback/daily", deps.handleGetDaily)
	mux.HandleFunc("GET /cashback/history", deps.handleGetHistory)
	mux.HandleFunc("POST /cashback/redemptions", deps.handlePostRedemption)

	mux.HandleFunc("GET /campaign", deps.handleGetCampaign)

	return withLogging(withCORS(mux))
}

// handleHealthz is a liveness check: 200 whenever the process is up.
func handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz is a readiness check: 200 only if Postgres and Redis both
// respond to a ping, otherwise 503 naming which dependency failed.
func (d *Deps) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	result := map[string]string{}
	ready := true

	if err := d.DB.Ping(ctx); err != nil {
		result["postgres"] = err.Error()
		ready = false
	} else {
		result["postgres"] = "ok"
	}

	if err := d.RDB.Ping(ctx).Err(); err != nil {
		result["redis"] = err.Error()
		ready = false
	} else {
		result["redis"] = "ok"
	}

	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, result)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// requireUserID extracts the caller's identity from X-User-Id (no auth in
// MVP - see spec/design.md §4). Writes a 400 and returns "", false if absent.
func requireUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID := r.Header.Get("X-User-Id")
	if userID == "" {
		writeError(w, http.StatusBadRequest, "X-User-Id header is required")
		return "", false
	}
	return userID, true
}

// withCORS allows any browser origin to call the API. There's no auth in
// this MVP (X-User-Id is a plain, untrusted header - see requireUserID
// above), so a wildcard origin doesn't widen the trust boundary any further
// than it already is; this exists so the mobile app's web target (a
// different origin/port) can reach the API during local development.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-User-Id, Idempotency-Key")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}
