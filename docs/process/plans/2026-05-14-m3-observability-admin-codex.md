# 2026-05-14 M3 Observability Admin API

| Owner directive | "HUAKAI M3 — 3 个 observability admin API 真接入 (消除 3 个 501 stub, F-OBS-001 第一片)。" |
| Scope | In: `/admin/v1/usage`, `/admin/v1/billing/claims`, `/admin/v1/audit-events` read-only query handlers; sqlc SELECT queries; handler unit tests; route replacement in gateway main. Out: DLQ replay, trust-chain audit verify, L1/L2/L3/M1/M2 behavior, schema migration, auth/billing/quota core mutation. |
| Success criteria | Three routes no longer use `notImplemented`; handlers require admin auth; query params validate RFC3339 `from/to`, `limit` 1-200, opaque base64 cursor; JSON response is `{items,next_cursor,total}`; tests cover success, auth failure, bad cursor, limit validation, pagination round-trip, and filter narrowing; `sqlc generate`, `go test ./internal/gatewayhttp/...`, and vet target pass if existing repo state allows. |
| Time estimate | 60-90 minutes wall clock; one Codex implementation pass plus local test/debug loop. |
| Blast radius | Read-only admin observability surface and generated sqlc DB code. Main runtime risk is query shape or auth dependency mismatch causing admin endpoints to fail while gateway hot path stays unaffected. |
| Failure modes | sqlc cannot express optional filters cleanly: use typed nullable params and stable keyset predicates. Missing canonical `audit_events` table: use the operator-facing audit tables already present or stop if the requested table truly does not exist. Cursor mismatch: version/type-tag cursor payloads and reject malformed base64/JSON. Existing unrelated test failures: report separately, do not mask. |
| Decision points | High-risk schema/auth/billing/quota changes are out of scope and require Owner confirmation. If a real `audit_events` table is absent, prefer a safe read-only equivalent over trust-chain `audit_ledger_entries`, because Owner explicitly said not to mix auditledger. |
| Pre-execution checklist | Read relevant rules and existing handler/auth/sqlc patterns; inspect current migrations for observability tables; add sqlc queries; generate DB code; implement narrow handlers; update main route registrations; write focused tests; run formatting, sqlc, tests, vet; write `/tmp/codex-m3-observability-admin-final.txt`. |
| Concrete execution order | 1. Inspect schema/query/generated types. 2. Add `backend/sql/queries/observability.sql`. 3. Run sqlc. 4. Add `admin_observability_handler.go`. 5. Add handler tests. 6. Replace three stubs in `main.go`. 7. Run checks and collect evidence. |

## Assumptions

- This task is HUAKAI-internal implementation; no non-MIT reference source is read.
- The audit admin endpoint should query operator-facing audit/event tables, not the trust-chain ledger used by `audit_verify_handler`.
- Route-level admin auth can reuse the existing `admin.AdminResolver` interface shape already used in `gatewayhttp` admin pool handlers.
