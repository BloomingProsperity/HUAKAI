.PHONY: build test perf fmt vet tidy generate migrate-up migrate-down run docker-build clean help \
	db-up db-down db-reset db-migrate db-psql test-integration embed-assets build-embed

GO          ?= go
BIN_DIR     := bin
GATEWAY_BIN := $(BIN_DIR)/huakai-gateway
# 仓库根目录(本 Makefile 位于 backend/ 下)。
REPO_ROOT   := $(abspath ..)

# Database URL for migrations (override per environment)
DATABASE_URL ?= postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable

help: ## Show this help.
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'

build: ## Build the gateway binary.
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(GATEWAY_BIN) ./cmd/gateway

embed-assets: ## Build the frontend SPA and copy it into the go:embed dist dir.
	$(REPO_ROOT)/scripts/build-frontend-embed.sh

build-embed: embed-assets ## Build the gateway binary with the embedded SPA (-tags embed).
	@mkdir -p $(BIN_DIR)
	$(GO) build -tags embed -o $(GATEWAY_BIN) ./cmd/gateway

test: ## Run all tests.
	$(GO) test ./... -race -count=1

perf: ## Run performance gate v2 and informational benchmarks.
	GO="$(GO)" bash scripts/perf-gate.sh

fmt: ## Format Go source.
	$(GO) fmt ./...

vet: ## Run go vet.
	$(GO) vet ./...

tidy: ## Tidy go.mod.
	$(GO) mod tidy

generate: ## Run sqlc codegen (requires sqlc installed).
	sqlc generate

migrate-up: ## Apply database migrations (requires golang-migrate).
	migrate -path sql/migrations -database "$(DATABASE_URL)" up

migrate-down: ## Roll back one migration step (.down.sql files are paired; rollback is dev/test only).
	migrate -path sql/migrations -database "$(DATABASE_URL)" down 1

run: build ## Build and run the gateway.
	$(GATEWAY_BIN)

docker-build: ## Build the gateway Docker image.
	docker build -t huakai-gateway:dev .

clean: ## Remove build artifacts.
	rm -rf $(BIN_DIR)

db-up: ## Start dev PostgreSQL container (Phase B integration sprint).
	docker compose -f docker-compose.dev.yml up -d postgres
	@echo "Waiting for PostgreSQL to be ready..."
	@for i in $$(seq 1 30); do \
	  docker compose -f docker-compose.dev.yml exec -T postgres pg_isready -U huakai -d huakai >/dev/null 2>&1 && echo "ready" && exit 0; \
	  sleep 1; \
	done; \
	echo "PostgreSQL not ready after 30s" && exit 1

db-down: ## Stop dev PostgreSQL (preserve volume).
	docker compose -f docker-compose.dev.yml stop postgres

db-reset: ## Drop dev PostgreSQL volume + restart (destructive).
	docker compose -f docker-compose.dev.yml down -v
	$(MAKE) db-up

db-migrate: ## Apply migrations via golang-migrate (tracks schema_migrations; idempotent).
	@MSYS_NO_PATHCONV=1 docker run --rm --network=huakai-dev-net \
	  -v "$(CURDIR)/sql/migrations:/migrations" \
	  migrate/migrate:v4.18.1 \
	  -path=/migrations \
	  -database "postgres://huakai:huakai@huakai-dev-pg:5432/huakai?sslmode=disable" \
	  up

db-psql: ## Open a psql shell against dev PG.
	docker compose -f docker-compose.dev.yml exec postgres psql -U huakai -d huakai

test-integration: ## Run integration tests (requires db-up + db-migrate first).
	HUAKAI_DATABASE_URL="$(DATABASE_URL)" $(GO) test -tags=integration_pg -race -count=1 ./...

quality-gate: ## Block new staticcheck findings + new deadcode (ratchet vs baseline).
	bash scripts/quality-gate.sh
