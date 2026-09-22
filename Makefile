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

.PHONY: help up down logs ps reset migrate seed test test-integration test-race lint mobile mobile-start mobile-stop demo

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-12s\033[0m %s\n", $$1, $$2}'

up: ## Build and start postgres, redis, and backend
	docker compose up -d --build

down: ## Stop all services
	docker compose down

logs: ## Follow backend logs
	docker compose logs -f backend

ps: ## Show service status
	docker compose ps

reset: ## Stop all services and DELETE volumes (this destroys all data)
	@echo "WARNING: this deletes the Postgres and Redis data volumes."
	docker compose down -v

migrate: ## Manually (re-)run database migrations against the running backend container
	docker compose exec backend /app/api -migrate

seed: ## Seed the database with demo data via the real API (needs `make up` running)
	bash scripts/seed.sh

test: ## Run backend unit tests (no DB required)
	cd backend && go test ./...

test-integration: ## Run backend concurrency/invariant tests against a real Postgres (needs `make up` running, or TEST_DATABASE_URL set)
	# -p 1: packages share one live DB and TRUNCATE common tables, so they
	# can't run in parallel without stomping on each other.
	cd backend && go test -tags=integration -p 1 ./...

test-race: ## Run backend unit tests with the race detector
	cd backend && go test -race ./...

lint: ## Run go vet, and golangci-lint if it's installed
	cd backend && go vet ./...
	@command -v golangci-lint >/dev/null 2>&1 && (cd backend && golangci-lint run) || echo "golangci-lint not installed, skipping"

mobile: ## Install deps and start the Expo dev server (native/Expo Go via LAN - the primary way to run mobile)
	cd mobile && npm install && npx expo start

mobile-start: ## Start a containerized web preview of the mobile app at http://localhost:8081 (dev-convenience only; needs `make up` for it to reach the backend; no hot-reload - rerun after code changes)
	@docker rm -f flash-cashback-mobile-web >/dev/null 2>&1 || true
	docker run --rm -d --name flash-cashback-mobile-web \
		-p 8081:8081 -p 19000:19000 \
		-v $(CURDIR)/mobile:/app -w //app \
		-e EXPO_PUBLIC_API_URL=http://localhost:8080 \
		-e CI=1 \
		node:20-alpine sh -c "npx expo start --web --port 8081"
	@echo "Starting - http://localhost:8081 (first load takes ~20s to bundle)"

mobile-stop: ## Stop the mobile web preview container
	docker rm -f flash-cashback-mobile-web >/dev/null 2>&1 || true

demo: ## Run the end-to-end demo (TODO)
	@echo "TODO: demo script not implemented yet."
