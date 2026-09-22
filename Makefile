.DEFAULT_GOAL := help

.PHONY: help up down logs ps reset migrate seed test test-race lint mobile demo

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

migrate: ## Run database migrations (TODO: migration tool not chosen yet)
	@echo "TODO(decision): pick a migration tool (e.g. golang-migrate, goose, atlas) and wire this target up. See spec/decisions.md."

seed: ## Seed the database with demo data (TODO)
	@echo "TODO: seed script not implemented yet."

test: ## Run backend unit tests
	cd backend && go test ./...

test-race: ## Run backend unit tests with the race detector
	cd backend && go test -race ./...

lint: ## Run go vet, and golangci-lint if it's installed
	cd backend && go vet ./...
	@command -v golangci-lint >/dev/null 2>&1 && (cd backend && golangci-lint run) || echo "golangci-lint not installed, skipping"

mobile: ## Install deps and start the Expo dev server
	cd mobile && npm install && npx expo start

demo: ## Run the end-to-end demo (TODO)
	@echo "TODO: demo script not implemented yet."
