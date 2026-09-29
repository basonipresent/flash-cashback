# On Windows, `make` implementations (e.g. GnuWin32) default recipes to
# cmd.exe, which doesn't understand this Makefile's POSIX shell syntax
# (/dev/null, `true`, etc.) - point SHELL at Git for Windows' bash.exe
# directly instead. Deliberately NOT using `$(shell where bash)`: that
# approach was tried and failed twice - (1) `where` can list a WSL-relay
# stub (System32/WindowsApps bash.exe) ahead of the real one depending on
# the invoking shell's PATH order, which fails outright if WSL has no
# working default distro; (2) any Make list function (firstword,
# filter-out, wildcard) silently mis-splits "C:\Program Files\..." on its
# internal space, since GNU Make 3.81 has no escaping for that. Using the
# 8.3 short name (PROGRA~1, no spaces) for the one literal path this needs
# sidesteps both problems entirely rather than working around them.
ifeq ($(OS),Windows_NT)
ifneq ($(wildcard C:/PROGRA~1/Git/bin/bash.exe),)
SHELL := C:/PROGRA~1/Git/bin/bash.exe
.SHELLFLAGS := -c
else ifneq ($(wildcard C:/PROGRA~1/Git/usr/bin/bash.exe),)
SHELL := C:/PROGRA~1/Git/usr/bin/bash.exe
.SHELLFLAGS := -c
endif
endif

.DEFAULT_GOAL := help

.PHONY: help docker-up docker-down docker-logs docker-ps docker-reset docker-migrate seed test docker-test-race lint mobile mobile-start mobile-stop demo

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-12s\033[0m %s\n", $$1, $$2}'

docker-up: ## Build and start db, redis, and app
	docker compose up -d --build

docker-down: ## Stop all services
	docker compose down

docker-logs: ## Follow app logs
	docker compose logs -f app

docker-ps: ## Show service status
	docker compose ps

docker-reset: ## Recreate containers and reset their anonymous volumes (this destroys all data)
	@echo "WARNING: this resets the Postgres and Redis data (anonymous volumes)."
	docker compose up -d --build --force-recreate --renew-anon-volumes

docker-migrate: ## Manually (re-)run database migrations against the running app container
	docker compose exec app /app -migrate

seed: ## Seed the database with demo data via the real API (needs `make docker-up` running)
	bash scripts/seed.sh

test: ## Run backend unit tests (no DB required)
	cd backend && go test ./...

# -p 1: packages share one DB and TRUNCATE common tables, so they can't run
# in parallel without stomping on each other.
#
# -race needs cgo, which this Windows host has no C toolchain for - so this
# runs via the `test` compose service (full Debian Go image with gcc)
# instead of natively. That service's TEST_DATABASE_URL points at
# flash_cashback_test, not the live flash_cashback database - testDB()
# TRUNCATEs everything it touches on every run, so this creates
# flash_cashback_test fresh each time rather than risking the seeded demo
# data. A previous run's container can also exit before Postgres finishes
# closing its connections, which would otherwise make the DROP DATABASE
# below flaky - terminate anything still attached first so the target is
# repeatable. -tags=integration pulls in the concurrency tests on top of
# the plain unit tests, so this one target covers both.
docker-test-race: ## Run all backend tests with the race detector, against a disposable flash_cashback_test database (needs `make docker-up`; never touches the live app database or its demo data)
	@docker compose exec -T db psql -U postgres -d flash_cashback -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = 'flash_cashback_test' AND pid <> pg_backend_pid();" >/dev/null
	@docker compose exec -T db psql -U postgres -d flash_cashback -c "DROP DATABASE IF EXISTS flash_cashback_test;" >/dev/null
	@docker compose exec -T db psql -U postgres -d flash_cashback -c "CREATE DATABASE flash_cashback_test;" >/dev/null
	@docker compose run --rm test go test -race -tags=integration -p 1 -v ./... 2>&1 | grep -v '^go: downloading'; \
	exit "$${PIPESTATUS[0]}"

lint: ## Run go vet, and golangci-lint if it's installed
	cd backend && go vet ./...
	@command -v golangci-lint >/dev/null 2>&1 && (cd backend && golangci-lint run) || echo "golangci-lint not installed, skipping"

mobile: ## Install deps and start the Expo dev server (native/Expo Go via LAN - the primary way to run mobile)
	cd mobile && npm install && npx expo start

mobile-start: ## Start a containerized web preview of the mobile app at http://localhost:8081 (dev-convenience only; needs `make docker-up` for it to reach the backend; no hot-reload - rerun after code changes)
	docker compose --profile mobile up -d --build mobile
	@echo "Starting - http://localhost:8081 (first load takes ~20s to bundle)"

mobile-stop: ## Stop the mobile web preview container
	docker compose --profile mobile rm -fs mobile

demo: ## Run payments -> redemptions -> reads against a running backend (needs `make docker-up`; users must exist first, e.g. `make seed`)
	@echo "== payments =="
	@bash scripts/test_payments.sh
	@echo
	@echo "== redemptions =="
	@bash scripts/test_redemptions.sh
	@echo
	@echo "== reads =="
	@bash scripts/test_reads.sh
