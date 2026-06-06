# 2026-06-06 Async Media Task Slice-1 Codex Plan

| Owner directive | `TASK: Implement ASYNC MEDIA-TASK relay + billing for HUAKAI — slice-1 (branch fix/async-media-task).` |
| --- | --- |
| Scope | Build HUAKAI-native async media task schema, engine, provider abstraction, worker, HTTP handlers, platform settings, and gateway wiring. Do not read `/home/ubuntu/refs` or external projects. Do not commit. Do not add files to frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`. |
| Success criteria | `/v1/media-tasks` session-auth routes are feature-gated; submit is idempotent and reserves once; success captures actual cents and releases remainder; failure and expiry release full hold; lease fencing prevents double terminal money effects; tenant/user status and list are isolated; migration 0099 creates/drops the requested table and indexes. |
| Time estimate | 3-5 hours agent time, depending on compile/test fallout in existing packages. |
| Blast radius | Money ledger writes (`billing_ledger_claims`, `balance_holds`, `billing_events`), user balances, one new table, platform settings allowlist, gateway route/worker lifecycle. |
| Failure modes | Orphan hold without task; task without hold; double capture/release under worker race; session user without any active API key cannot satisfy billing claim FK; provider submit duplicate after crash before provider_task_id is stored; disabled feature accidentally reserving money; migration rollback with live rows. |
| Mitigations | One serializable submit transaction inserts claim, reserves hold, and inserts task. Idempotency locks `(tenant_id, request_id)` before reservation. Terminal settlement/refund locks task and claim in one serializable transaction and checks non-terminal status plus lease owner. Feature disabled is checked before store mutation. Resolve one active user-owned API key inside submit transaction; if none exists, return a typed error before row/hold creation. Include request_id in provider submit payload for later vendor idempotency. Down migration refuses rollback when `media_tasks` contains rows. |
| Decision points | Owner has already authorized schema and money-path work. No extra Owner confirmation needed unless implementation would require changing `LICENSE`, auth core, quota enforcement, billing ledger schema beyond adding `media_tasks`, real secrets, deployment scripts, or destructive data operations. |
| Pre-execution checklist | Trace existing billing reserve/settle/hold code; trace worker lifecycle and route wiring; verify current worktree is already isolated; write this plan; write tests before production code; implement small packages; run focused tests, build, and vet as sandbox allows. |

## Understand-First Analysis

The existing money path is claim-backed: a reserving `billing_ledger_claims` row owns the `balance_holds` row, and `billing.Capture` / `billing.Release` are the correct primitives for actual charge and full refund. `DefaultClaimGate.Reserve` cannot be called directly for media submit because it commits before the `media_tasks` row exists, which would allow reserved-but-no-task on later insert failure. The media store will therefore create the billing claim, call `billing.Reserve`, and insert `media_tasks` inside one serializable transaction. The existing `DefaultSettler.Settle` is provider-account/acquisition-token bound, while slice-1 intentionally has a pluggable provider and no live vendor account, so media settlement will reuse claim update, billing event insertion, and balance hold capture/release directly without writing a misleading provider-upstream usage record. Session auth carries tenant/user but not API key, so submit will resolve one active API key owned by that tenant/user for the existing billing FK; no active key means no task and no reservation.

## File Plan

- Create `backend/sql/migrations/0099_media_tasks.up.sql` and `backend/sql/migrations/0099_media_tasks.down.sql` for the requested table, checks, unique constraint, and indexes.
- Create `backend/internal/mediatask/types.go` for public task/provider/config types and errors.
- Create `backend/internal/mediatask/pricing.go` for default-estimate cents parsing and `pricingeval`-based per-task estimate conversion.
- Create `backend/internal/mediatask/provider.go` for `AsyncMediaProvider`, HTTP provider, and no-op provider.
- Create `backend/internal/mediatask/store.go` for PostgreSQL submit/status/list/lease/terminal money transactions.
- Create `backend/internal/mediatask/service.go` for validation, feature gate, idempotent submit, status, and list orchestration.
- Create `backend/internal/mediatask/worker.go` for `RunOnce`, `Start`, `Stop`, provider submit/poll, timeout handling, and lease owner fencing.
- Create `backend/internal/mediatask/*_test.go` for unit tests with mutation comments and `*_integration_test.go` under `integration_pg` for money/schema tests PM will run.
- Create `backend/internal/mediataskhttp/handlers.go` and tests for session-scoped submit/status/list and disabled behavior.
- Modify `backend/internal/platformsettings/types.go` to add `mediatask_*` settings and validation/defaults.
- Modify `backend/cmd/gateway/wiring.go`, `routes.go`, and `lifecycle.go` to wire the service, provider, worker, stop hook, and routes.

## Concrete Execution Order

1. Add tests first for pricing/config validation, provider HTTP behavior, service idempotency with fake store, worker terminal transitions with fake store/provider, and HTTP disabled/isolation behavior.
2. Add integration_pg tests for submit reserve/idempotency, success capture actual, failure refund, timeout refund, double-worker no double-settle, submit atomicity, tenant isolation, and migration 0099 structure.
3. Implement mediatask types, provider, pricing, service, worker, and store until focused package tests compile and pass.
4. Add migration 0099 and platform setting keys.
5. Wire gateway deps, route registration, worker start/stop, and config construction.
6. Run `gofmt` on touched Go files.
7. Run focused unit tests for `./internal/mediatask`, `./internal/mediataskhttp`, `./internal/platformsettings`, and `./cmd/gateway`.
8. Run requested `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./... && go vet ./...` if sandbox time/resources allow; report exact output or blocker.

## Risk Notes

- The submit API is session-authenticated but billing claims require an API key. Using one active user-owned key keeps existing FKs and audit ownership intact, but users without active keys must create one before submitting media tasks.
- Slice-1 does not add real Midjourney/Sora adapters, provider account attribution, quota integration, or usage-record analytics for media tasks. Those belong in slice-2 with vendor adapters and account acquisition semantics.
- Provider submit is asynchronous and includes the client `request_id` for idempotency, but a crash after provider submit and before storing `provider_task_id` can still duplicate work against a non-idempotent fake/vendor endpoint. Slice-2 adapters must use vendor idempotency or a submit outbox to close that operational gap.
