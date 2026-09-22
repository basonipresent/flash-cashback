// Package migrate runs the backend's embedded SQL migrations against
// Postgres. It runs automatically on boot (see cmd/api/main.go) so `make up`
// alone stands up a fully working schema - no separate migrate step needed.
package migrate

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/basonipresent/flash-cashback/backend/migrations"
)

// Up applies every pending migration. It is safe to call concurrently from
// multiple replicas: golang-migrate takes a Postgres advisory lock for the
// duration of the run.
func Up(databaseURL string) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("migrate: open: %w", err)
	}
	defer db.Close()

	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("migrate: source: %w", err)
	}

	dbDriver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("migrate: driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "postgres", dbDriver)
	if err != nil {
		return fmt.Errorf("migrate: init: %w", err)
	}

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate: up: %w", err)
	}

	return nil
}
