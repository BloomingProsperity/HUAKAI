# 2026-06-06 Subscription Admin Ops Codex Plan

| Owner directive | "Add missing ADMIN subscription lifecycle ops to HUAKAI (branch fix/subscription-admin-ops). Verified absent on landing. Reach CLOSURE: routes + service + store + tests + gate-ready. Money/entitlement path — idempotent, audited, no shortcuts." |
| --- | --- |
| Scope | In: `backend/internal/subscription`, `backend/internal/subscriptionhttp`, and a minimal subscription constraint migration if required. Out: `/home/ubuntu/refs`, payment/billing ledger behavior, quota enforcement internals, frozen packages `gatewayhttp`, `gateway`, `proto`, commits, and sandbox-unavailable `integration_pg` execution. |
| Success criteria | Admin routes exist for plan update, extend, reset quota, bulk assign, and revoke; service and store expose transactional operations; operations validate tenant/resource inputs, are audited, and preserve money/entitlement invariants; discriminating unit and integration_pg tests are written; `go build`, `go vet`, focused subscription tests, and `cmd/gateway` tests are run where sandbox permits. |
| Time estimate | 2-4 hours wall clock in this session; PM later runs PostgreSQL/socket integration and mutation checks outside sandbox. |
| Blast radius | Subscription entitlements, quota policy ownership rows, admin HTTP contracts, and DB CHECK constraints. Failure can over-grant access, leave quota policies enabled after revoke, double-extend subscriptions, or hide missing audit evidence. |
| Failure modes | Double extension on idempotent retry: guard with request-id audit replay lookup in the same transaction. Revoke leaves active quota policies: reuse close-cap path and assert link/policy closure in integration test. Bulk assign global rollback: call service once per user and collect per-user result. Plan update mutates existing subscriptions: update only `subscription_plans`, never active subscription snapshots. Reset quota mis-modeled as local counters: rebuild subscription-owned quota policies from the active subscription plan snapshot because HUAKAI quota state is stored in `quota_policies`, not in subscription rows. |
| Decision points | Add `0101` migration because current constraints do not permit `revoked`, `subscription_extended`, `subscription_quota_reset`, `subscription_plan_updated`, or `subscription_revoked`. No Owner confirmation required beyond this task because the migration is minimal/additive for explicitly requested admin lifecycle semantics. |
| Pre-execution checklist | Read local subscription service/store/HTTP patterns; verify current migration constraints; avoid `/home/ubuntu/refs`; write tests before implementation; do not add files to frozen packages; do not commit. |

## Concrete Execution Order

1. Write failing service and integration_pg tests for `UpdatePlan`, `ExtendSubscription`, `ResetQuota`, `BulkAssign`, and `RevokeSubscription`, including mutation comments from the Owner prompt.
2. Run focused subscription tests to verify RED. Non-integration tests should fail at compile or missing-method level before implementation; `integration_pg` tests are authored but not run in this sandbox.
3. Add types, store interface records, service methods, and memory-store behavior for fast tests.
4. Implement PostgreSQL transactions in `store_postgres.go`, reusing existing close/reinstall helpers where possible and adding request-id audit replay detection for idempotent extend/revoke/reset.
5. Add minimal `0101_subscription_admin_ops` migration to extend status and audit event CHECK constraints, with fail-closed down migration.
6. Add admin HTTP request DTOs, routes, handlers, and response views in `internal/subscriptionhttp/handler.go`.
7. Run focused package tests, `go test ./cmd/gateway`, `go build ./...`, and `go vet ./...`; record exact results and any sandbox-only limitations.

## Clean-Room Note

This is HUAKAI-native implementation from the Owner-provided behavior summary and local code patterns only. No reference source is read, copied, or structurally mirrored.
