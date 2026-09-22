package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/basonipresent/flash-cashback/backend/internal/config"
	"github.com/basonipresent/flash-cashback/backend/internal/httpapi"
	"github.com/basonipresent/flash-cashback/backend/internal/migrate"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	// The distroless runtime image has no shell/curl, so the container
	// HEALTHCHECK runs this binary against itself instead.
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		healthcheck()
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "error", err)
		os.Exit(1)
	}

	// `-migrate`: run migrations against the configured DATABASE_URL and
	// exit, without starting the HTTP server. Mainly for `make migrate`
	// (manual/CI re-runs) - migrations already run automatically below on
	// every normal boot, so this isn't required for `make up` to work.
	if len(os.Args) > 1 && os.Args[1] == "-migrate" {
		if err := migrate.Up(cfg.DatabaseURL); err != nil {
			slog.Error("migrate", "error", err)
			os.Exit(1)
		}
		slog.Info("migrate: up to date")
		os.Exit(0)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connectCtx, cancelConnect := context.WithTimeout(ctx, 10*time.Second)
	defer cancelConnect()

	db, err := pgxpool.New(connectCtx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("postgres: failed to create pool", "error", err)
		os.Exit(1)
	}
	defer db.Close()
	if err := db.Ping(connectCtx); err != nil {
		slog.Error("postgres: unreachable", "error", err)
		os.Exit(1)
	}

	// Safe under concurrent replicas: golang-migrate holds a Postgres
	// advisory lock for the duration of the run.
	if err := migrate.Up(cfg.DatabaseURL); err != nil {
		slog.Error("migrate", "error", err)
		os.Exit(1)
	}
	slog.Info("migrate: up to date")

	redisOpts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		slog.Error("redis: invalid REDIS_URL", "error", err)
		os.Exit(1)
	}
	rdb := redis.NewClient(redisOpts)
	defer rdb.Close()
	if err := rdb.Ping(connectCtx).Err(); err != nil {
		slog.Error("redis: unreachable", "error", err)
		os.Exit(1)
	}

	httpServer := &http.Server{
		Addr:         ":" + cfg.HTTPPort,
		Handler:      httpapi.NewRouter(db, rdb),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("listening", "port", cfg.HTTPPort, "timezone", cfg.Timezone)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "error", err)
		os.Exit(1)
	}
}

func healthcheck() {
	port := os.Getenv("HTTP_PORT")
	if port == "" {
		port = "8080"
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil || resp.StatusCode != http.StatusOK {
		os.Exit(1)
	}
	os.Exit(0)
}
