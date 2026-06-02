# 2026-05-28 Quota B2a Reserve Codex Plan

| Owner directive | "实现 HUAKAI 配额子系统「切片 B2a」:policy resolver + rate_window + service.Reserve 准入决策 + reserve 测试。" |
| --- | --- |
| Scope | In: add focused files under non-frozen `backend/internal/quota` for rate window calculation, policy resolution, reserve service/types, and reserve tests. Out: settle/release/overage, migrations, gatewayhttp/gateway/proto, Store interface signature changes, commits/pushes. |
| Success criteria | Store interface is sufficient and unchanged; `ComputeWindow` has table-driven boundary tests; Reserve integration tests cover reserved+settled denial, observe non-denial, strictest-wins across scopes, idempotent claim handling, and allow path ledger/window/audit evidence; `cd backend && go build ./internal/quota/...` exits 0; real PG test command is provided for Owner rerun. |
| Time estimate | 2-3 wall-clock hours, mostly service semantics and integration fixture assertions. |
| Blast radius | Medium. The change implements quota admission in the quota package only. A wrong decision can allow or deny requests incorrectly, but no schema/auth/billing/gateway/deployment files are in scope. |
| Failure modes | Store method missing: stop and report instead of changing the interface. Window semantics ambiguous: keep documented B2a-safe behavior and record follow-up. Tests accidentally non-discriminating: each test asserts DB evidence tied to the named mutation. Concurrency slot partial acquisition after reservation: keep failure surfaced fail-closed; B2b can add reconciliation/release hardening if needed. |
| Decision points | Owner confirmation is required before changing Store signatures, migrations, frozen packages, quota enforcement schema, billing ledger, auth core, runtime dependencies, or destructive database operations. |
| Pre-execution checklist | 1. Read `types.go`, `store.go`, `errors.go`, `pg_store.go`, query SQL, and existing PG fixture. 2. Confirm all needed Store methods already exist. 3. Write failing pure rate-window tests first. 4. Write failing Reserve integration tests before service implementation. 5. Implement only in `backend/internal/quota`. 6. Run gofmt and `cd backend && go build ./internal/quota/...`; run non-PG unit tests locally; provide PG command for Owner. |

## Concrete Execution Order

1. Add `backend/internal/quota/rate_window_test.go`, run targeted unit test, and confirm `ComputeWindow` is missing/failing.
2. Add `backend/internal/quota/rate_window.go` with deterministic UTC window calculation.
3. Add Reserve integration tests in a new `backend/internal/quota/service_integration_test.go` using existing PG fixture style and direct DB assertions.
4. Add `backend/internal/quota/policy.go`, `backend/internal/quota/service.go`, and `backend/internal/quota/reservation.go` only if needed for small helper types.
5. Run `gofmt` on touched quota files.
6. Run `cd backend && go test ./internal/quota/...` for pure tests and `cd backend && go build ./internal/quota/...`.
7. Report any integration test command that could not be executed locally due to missing `HUAKAI_DATABASE_URL`.

## File Structure Check

- Create: `backend/internal/quota/rate_window.go` and `backend/internal/quota/rate_window_test.go`.
- Create: `backend/internal/quota/policy.go`.
- Create: `backend/internal/quota/service.go`.
- Create only if helper state would otherwise clutter service: `backend/internal/quota/reservation.go`.
- Create: `backend/internal/quota/service_integration_test.go`.

No new files are added under frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.
