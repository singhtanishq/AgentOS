# AgentOS root Makefile — developer entrypoints.
# Services: apps/web (Next.js), services/control-plane (Go), services/agent-runtime (Python).

.DEFAULT_GOAL := help
SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c

# ------------------------------------------------------------------
# Development
# ------------------------------------------------------------------

.PHONY: dev
dev: ## Run web, control plane and agent runtime together (foreground)
	@./scripts/dev.sh

.PHONY: dev-web
dev-web: ## Run the Next.js dashboard on :3000
	@pnpm --filter @agentos/web dev

.PHONY: dev-api
dev-api: ## Run the Go control plane on :8080
	@cd services/control-plane && go run ./cmd/server

.PHONY: dev-runtime
dev-runtime: ## Run the Python agent runtime API on :8090
	@cd services/agent-runtime && uv run python -m app

# ------------------------------------------------------------------
# Build
# ------------------------------------------------------------------

.PHONY: build
build: build-web build-api ## Build all applications

.PHONY: build-web
build-web: ## Production build of the Next.js dashboard
	@pnpm --filter @agentos/web build

.PHONY: build-api
build-api: ## Compile the Go control plane
	@cd services/control-plane && go build ./...

# ------------------------------------------------------------------
# Test
# ------------------------------------------------------------------

.PHONY: test
test: test-go test-python test-web ## Run all test suites

.PHONY: test-go
test-go: ## Go unit tests with race detector
	@cd services/control-plane && go test -race ./...

.PHONY: test-python
test-python: ## Python runtime tests
	@cd services/agent-runtime && uv run pytest

.PHONY: test-web
test-web: ## Web unit tests
	@pnpm --filter @agentos/web test

# ------------------------------------------------------------------
# Lint
# ------------------------------------------------------------------

.PHONY: lint
lint: lint-go lint-python lint-web ## Run all linters

.PHONY: lint-go
lint-go: ## go vet for the control plane
	@cd services/control-plane && go vet ./...

.PHONY: lint-python
lint-python: ## ruff and mypy for the agent runtime
	@cd services/agent-runtime && uv run ruff check . && uv run mypy app

.PHONY: lint-web
lint-web: ## eslint and tsc for the dashboard
	@pnpm --filter @agentos/web lint
	@pnpm --filter @agentos/web typecheck

# ------------------------------------------------------------------
# Infrastructure (milestone 02) and database (milestone 03)
# ------------------------------------------------------------------

.PHONY: infra-up
infra-up: ## Start Docker Compose infrastructure services
	@./scripts/infra-up.sh

.PHONY: infra-down
infra-down: ## Stop Docker Compose infrastructure services
	@docker compose down

.PHONY: migrate
migrate: ## Apply PostgreSQL schema migrations
	@./scripts/migrate.sh

.PHONY: seed
seed: ## Populate the local demo environment
	@./scripts/seed.sh

.PHONY: health
health: ## Check health of all running services
	@./scripts/health-check.sh

.PHONY: clean
clean: ## Remove disposable build artifacts
	@./scripts/clean.sh

# ------------------------------------------------------------------
# Help
# ------------------------------------------------------------------

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
