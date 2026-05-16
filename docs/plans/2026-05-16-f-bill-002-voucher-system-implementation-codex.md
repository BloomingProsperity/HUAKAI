# 2026-05-16 F-BILL-002 Voucher System Implementation Codex Plan

| Owner directive | "任务 = F-BILL-002 voucher system 实施 (Phase 6 commercial foundation)." |
| --- | --- |
| Scope | In: migration 0023 for voucher storage; new `backend/internal/voucher` package; gateway HTTP voucher handlers; gateway wiring; OpenAPI endpoints; AT-level Go tests for create, redeem, race, expiry, wrong tenant/user, revoke, audit, anti-fraud, idempotency, batch. Out: reference-project source; LICENSE; auth core; quota enforcement core; existing F-BILL-001 request settlement internals beyond using its exposed schema/path; Rust `core_gateway`; production secrets; new runtime dependencies. |
| Success criteria | `tenant_id NOT NULL` is first-class in all new voucher tables; raw voucher code is never written to audit/log/error payloads; successful redeem emits `billing_events.type = voucher_redeemed` and balance/quota credit through local schema; five requested endpoints exist; AT-BILL-002-001..010 are covered by tests; targeted `go test -race -count=1` and `go build ./...` are run or failures are reported with concrete blockers. |
| Time estimate | 3-5 hours wall clock depending on existing billing/userauth/session handler shape; high agent time because this crosses schema, service, HTTP, OpenAPI, and tests. |
| Blast radius | Medium. Adds new billing-adjacent storage and handlers. Main failure impact is compile breakage, incomplete transaction semantics, duplicate credit under race, raw voucher leakage, or miswired auth/admin middleware. |
| Failure modes | Existing billing schema may not expose a reusable balance increment API; session/admin identity extraction may differ from expected handler pattern; Postgres integration tests may not be available locally; OpenAPI schema style may require fitting existing conventions; migration 0023 number may conflict with concurrent work. |
| Mitigations | Read HUAKAI-local specs and code only; mirror local handler/wire/test style; keep all new code in `internal/voucher` plus one new gateway handler where possible; use exact integer cents and DB transactions; store code hashes/fingerprints rather than raw codes; build a memory store for deterministic unit tests; document any integration-test fallback honestly. |
| Decision points | Stop for Owner only if implementation requires changing auth core, quota enforcement core, existing billing ledger semantics, DB schema outside additive voucher tables, adding dependencies, changing LICENSE, or destructive migration behavior. |
| Pre-execution checklist | Confirm Owner start signal; read `docs/RULES.md`; read F-BILL-002 spec/decomposition/AT/feature row; inspect existing migrations, billing package, gateway handlers, main wiring, userauth/session middleware; confirm next migration is 0023; verify working tree status; create migration before code; run focused tests before broad build. |

## Concrete Execution Order

1. Inspect current backend package layout, billing schema/API, gateway route/middleware patterns, and test conventions.
2. Add migration `0023_voucher_system` with additive tenant-scoped tables and safe down migration.
3. Implement `backend/internal/voucher` types, store interfaces, memory store, Postgres store, anti-fraud/idempotency helpers, audit facade, and service rules.
4. Add service tests covering AT-BILL-002-001..010 where possible in memory, and add race/idempotency/batch tests.
5. Add `backend/internal/gatewayhttp/voucher_handler.go` with the five requested endpoints and safe request/response types.
6. Wire the service and routes in `backend/cmd/gateway/main.go` using existing admin/user middleware patterns.
7. Update `docs/openapi/openapi.yaml` for the five endpoints.
8. Run targeted tests and `go build ./...`; fix compile/test issues within scope.
9. Run clean-room self-check, `git diff --check`, and summarize diff/test status.

## Clean-Room Guard

- Implementer lane only reads HUAKAI-owned specs/code and Go standard library docs by memory.
- No reference-project source, schemas, comments, tests, function names, or distinctive file structures are read or reused.
- F-BILL-002 feature outcome is preserved through a local independent implementation.
