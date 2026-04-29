This file is agent-facing and authoritative.

# Phase 3 Skeleton — Status & Map

Per [DR-008 §1](decisions/DR-008-methodology-choice-strict-authenticity.md): skeleton allowed only after Phase 2 contracts Released. All 7 Released specs + Phase 2.2 OpenAPI lock are in place.

## What's in the skeleton

| Path | Purpose |
| --- | --- |
| `go.mod` | Go module root: `github.com/BloomingProsperity/HUAKAI` |
| `cmd/gateway/main.go` | Entry point + chi router with `notImplemented` stubs for all OpenAPI paths |
| `internal/pool/` | F-POOL-001 selector interface |
| `internal/billing/` | F-OBS-001 + F-BILL-001 framing: ClaimGate (Tx1) + Settler (Tx2) interfaces |
| `internal/gateway/` | F-GW-002 streaming Forwarder interface + EndClass + UsageSource enums |
| `internal/rate/` | F-RATE-001 cooldown Service interface + 19-reason taxonomy enum |
| `internal/auth/` | F-AUTH-005 TokenProvider + MimicryEngine interfaces |
| `internal/proto/` | F-PROTO-002 ClientAdapter + UpstreamAdapter interfaces |
| `internal/obs/` | F-OBS-001 Repository interface |
| `pkg/adapter/` | Concrete protocol adapters live here (Phase 4) |
| `sql/migrations/` | 6 ordered migration files (0001..0006), copies of `docs/schema/*.sql` |
| `sql/queries/` | sqlc query files (Phase 4 vertical-slice implementer-lane adds) |
| `sqlc.yaml` | sqlc config: pgx/v5, decimal money types, UUID acquisition tokens |
| `Makefile` | build / test / fmt / vet / generate / migrate-up / migrate-down / run / docker-build |
| `config.example.yaml` | Operator config schema (illustrative; Phase 4 may refine) |
| `Dockerfile` | Development image (operator-grade hardening in Phase 8) |

## What is NOT in the skeleton (deferred)

- **Business logic**: every `internal/<feature>` declares interfaces only. No Provider Account selection algorithm. No Tx1/Tx2 SQL execution. No streaming forwarder loop. Per DR-008, business logic is Phase 4+ vertical-slice work.
- **Generated code**: `internal/db/` (sqlc output) is not committed; regenerate via `make generate`.
- **Down migrations**: `sql/migrations/*.down.sql` files are Phase 4 task (adds rollback for each up).
- **Tests**: per-package unit tests + integration tests are Phase 4 (TDD per Released spec).
- **Frontend**: TypeScript admin UI is Gemini's domain; not in this skeleton.

## How an implementer-lane agent picks up from here

Phase 4 vertical-slice order (per docs/decompositions/_cross-cutting):

1. F-AUTH-005: implement TokenProvider for Antigravity (single provider) + MimicryEngine opt-in.
2. F-POOL-001: implement Selector for the 5-layer algorithm + Pattern B writeback.
3. F-PROTO-002: implement adapter for openai_chat × anthropic upstream pair (+capability matrix table seed).
4. F-GW-002: implement Forwarder pipeline using F-AUTH + F-POOL + F-PROTO.
5. F-OBS-001 (billing core): implement ClaimGate + Settler against billing_ledger_claims + billing_events + usage_records.
6. F-OBS-001 (observability surface): implement Repository for admin queries; outbox consumer.
7. F-RATE-001: implement upstream-error decision tree integrated with F-AUTH 401 flow.

Per implementer step:
- TDD: read `docs/specs/<feature>.md` §Acceptance Test Direction → write failing Go integration tests → implement until tests pass.
- Reference Audit: after implementation, write `docs/audits/<feature>.md` with KEEP/MISSING/EXTRA matrix vs Sub2API/one-api/LiteLLM/Portkey/Helicone.
- Vertical slice smoke: every 2 features, run end-to-end smoke test (curl request → routed → forwarded → settled → audit visible).

## Acceptance Test Direction (for the skeleton itself)

- AT-SKEL-001 / `go build ./...` succeeds (after `go mod tidy`).
- AT-SKEL-002 / `go vet ./...` clean.
- AT-SKEL-003 / `make build` produces `bin/huakai-gateway` binary.
- AT-SKEL-004 / Running the gateway returns 501 NOT_IMPLEMENTED on every OpenAPI path with a JSON envelope referencing the spec.
- AT-SKEL-005 / `migrate -path sql/migrations -database $DATABASE_URL up` succeeds against an empty PostgreSQL database.
- AT-SKEL-006 / `sqlc generate` succeeds with no query files (queries dir empty; emit nothing).
- AT-SKEL-007 / Module path `github.com/BloomingProsperity/HUAKAI` matches GitHub remote.

These tests can be run by Phase 4 first agent before adding business logic.

## License-discipline check

This skeleton contains no upstream identifier names from non-MIT references. Package names (`pool`, `billing`, `gateway`, `rate`, `auth`, `proto`, `obs`) are HUAKAI domain language. Interface methods (`Select`, `Reserve`, `Settle`, `Forward`, `HandleUpstreamError`, `GetAccessToken`, `RequestToCanonical`) are HUAKAI domain operations. Field names match `docs/19_DOMAIN_MODEL.md` and `docs/openapi/openapi.yaml`.
