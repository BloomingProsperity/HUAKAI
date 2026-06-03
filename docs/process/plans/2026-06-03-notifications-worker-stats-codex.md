# 2026-06-03 notifications worker stats first slice
| Owner directive | "Implement the FIRST SLICE of gap \"notifications\" — a SAFE-READ admin worker-stats endpoint" |
| Scope | In: expose existing in-process subscription reminder/expiry worker counters through a platform-admin-only read endpoint, add a cmd/gateway adapter and route mount helper, and cover auth/nil-dependency/counter behavior with discriminating tests. Out: email pipeline changes, migrations, persistent metrics, subscription state mutation, route integration in `routes.go`, frozen packages, commits. |
| Success criteria | Source confirms existing worker counter methods; `internal/subscriptionhttp` returns JSON counters only to `platform_admin`; unauth gets 401, `tenant_operator` gets 403, nil reader/auth gets 503; adapter reads `TickCount`, `SentTotal`, `ExpiredTotal`, and `FailedTicks`; `cd backend && go build ./...`, `go vet ./internal/subscriptionhttp/...`, and `go test ./internal/subscriptionhttp/...` pass. |
| Time estimate | One focused Codex session, about 60-90 minutes including TDD red checks, mutation verification, build/vet/test. |
| Blast radius | Low to medium: new safe-read admin HTTP surface and cmd/gateway dependency fields. No database, auth core, quota, billing ledger, payment logic, secrets, or frozen packages. |
| Failure modes | Wrong worker method names break build; wrong route path forces later integration churn; auth check order could leak stats to tenant operators or return 503 instead of 401/403; nil reader could panic; tests could pass without checking exact counter values. Mitigation: read concrete worker source first, reuse `AdminAuth`/`writeJSONError`/`admin.RolePlatformAdmin`, assert exact status and JSON values, mutation-verify each required test. |
| Decision points | Owner/PM may later choose where to call `mountNotificationRoutes` from `routes.go`; this task intentionally does not edit `routes.go`. No high-risk file changes planned. |
| Pre-execution checklist | 1. Read `docs/process/gap-designs/notifications.md`. 2. Verify `ReminderWorker`/`ExpiryWorker` concrete counter methods in `internal/subscription`. 3. Verify `subscriptionhttp` admin auth/error patterns. 4. Confirm no new files in frozen `internal/gatewayhttp`, `internal/gateway`, or `internal/proto`. 5. Write failing tests before production code. 6. Implement minimal handler, adapter, and route helper. 7. Run targeted tests, mutation checks, final build/vet/test. |

## Concrete Execution Order

1. Add `backend/internal/subscriptionhttp/admin_worker_stats_test.go` with four failing tests:
   - `tenant_operator` receives 403.
   - unauthenticated admin resolver error receives 401.
   - `platform_admin` receives 200 and exact reminder/expiry counter values.
   - nil stats reader fails closed with 503 instead of panicking.
2. Run targeted tests and record the expected RED result caused by missing symbols.
3. Add `backend/internal/subscriptionhttp/admin_worker_stats.go` with `WorkerStatsReader`, DTOs, dependency struct, and `NewAdminWorkerStatsHandler`.
4. Add `backend/cmd/gateway/subscription_worker_stats_adapter.go` to adapt live workers to the reader interface.
5. Add `backend/cmd/gateway/routes_notifications.go` with `mountNotificationRoutes(r chi.Router, d *deps)` and `GET /v1/admin/notifications/worker-stats`.
6. Add minimal worker fields to `cmd/gateway` deps and populate them after worker construction in `wiring.go`.
7. Run targeted green tests.
8. Mutation-verify each required test by introducing one defect at a time, running the targeted test to observe RED, then restoring.
9. Run final required verification commands from `backend/`.
