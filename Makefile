.PHONY: build test fmt vet tidy generate migrate-up migrate-down run docker-build clean help

GO          ?= go
BIN_DIR     := bin
GATEWAY_BIN := $(BIN_DIR)/huakai-gateway

# Database URL for migrations (override per environment)
DATABASE_URL ?= postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable

help: ## Show this help.
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'

build: ## Build the gateway binary.
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(GATEWAY_BIN) ./cmd/gateway

test: ## Run all tests.
	$(GO) test ./... -race -count=1

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

migrate-down: ## Roll back one migration step (requires .down.sql files; Phase 4 task).
	migrate -path sql/migrations -database "$(DATABASE_URL)" down 1

run: build ## Build and run the gateway.
	$(GATEWAY_BIN)

docker-build: ## Build the gateway Docker image.
	docker build -t huakai-gateway:dev .

clean: ## Remove build artifacts.
	rm -rf $(BIN_DIR)
