# 2026-06-03 tiered-billing slice1 Codex plan

| Owner directive | "Implement ONLY its first slice (smallest highest-value complete unit)." |
| Scope | In: `backend/internal/billingdsl` pure parser/evaluator package and tests. Out: settlement semantics, claim gate, funding source, HTTP handlers, route wiring, sqlc query edits, and schema migrations unless proven unavoidable. |
| Success criteria | `internal/billingdsl` parses tier-rule JSON, rejects invalid tier definitions, evaluates token tiers with `shopspring/decimal`, supports explicit flat-rate fallback, and has mutation-verified discriminating tests. Required backend verification commands are run and reported. |
| Time estimate | 1 focused Codex work unit; expected wall clock under 2 hours including mutation checks. |
| Blast radius | New package only. No money-path database writes, no Tx1/Tx2 behavior change, no credential selection or logging, no frozen-package new files. |
| Failure modes | Parser accepts ambiguous tiers; evaluator silently zero-prices missing nonzero buckets; decimal precision regresses to float; integration is attempted before schema exists. Mitigation: table-driven tests, red/green check, explicit mutation checks, no integration in this slice. |
| Decision points | Owner/PM must decide later whether schema migration should be 0077 per spec or 0080 per current instruction, and must separately approve any `gatewayhttp`, `billing`, sqlc, or settle-path money touch. |
| Pre-execution checklist | Read `docs/process/gap-specs/tiered-billing.md`; confirm current migration max; confirm `billing_pricing_versions`, `billing_ledger_claims`, and `api_keys` column state; confirm frozen package rule; confirm `shopspring/decimal` is already present. |

## Execution order

1. Add failing tests in `backend/internal/billingdsl` for parser validation, tier splitting, fallback, missing-rate errors, and decimal precision.
2. Run targeted tests to verify RED from missing package/functions.
3. Add `doc.go`, `types.go`, `parser.go`, and `evaluator.go` in `backend/internal/billingdsl`.
4. Run targeted tests to verify GREEN.
5. Mutation-verify each discriminating money behavior by temporarily introducing the named defect, observing RED, then restoring the implementation.
6. Run required verification: `cd backend && sqlc generate`, `go build ./...`, `go vet ./internal/... 2>&1 | tail`, and package tests for created packages.

## Verified premises before execution

- `backend/sql/migrations` currently ends at `0076_user_role`.
- `billing_pricing_versions` exists and currently has `id`, `tenant_id`, `version`, `pricing_data`, `effective_from`, `effective_to`, `created_at`, `created_by_actor`, plus later `is_public`; no `tier_rules` column was found.
- `billing_ledger_claims` and `api_keys` exist; no first-slice edit depends on their columns.
- `github.com/shopspring/decimal v1.4.0` is already in `backend/go.mod`.
- `internal/gatewayhttp`, `internal/gateway`, and `internal/proto` are frozen for new files; this plan adds no files there.
