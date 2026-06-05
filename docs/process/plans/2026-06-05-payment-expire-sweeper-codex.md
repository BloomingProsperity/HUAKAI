# 2026-06-05 payment expire sweeper

| Owner directive | "商业 C1 expire-sweeper(过期 pending 订单扫成 expired + 审计)" |
| Scope | Add a payment order expiration store method, a payment-package background sweeper, runtime config, gateway wiring, discriminating memory-store tests, reference comparison note, and a final local commit. Out of scope: schema changes, frozen packages, payment confirmation semantics beyond stale pending expiration, pushing. |
| Success criteria | `ExpireStalePendingOrders(ctx, now, limit)` expires only pending orders with non-nil `expires_at < now`, writes `order_expired` audit rows, returns the affected count, and leaves future pending and paid orders unchanged. Gateway starts the worker only when interval config is positive. Required build/vet/payment tests pass. |
| Time estimate | 45-75 minutes wall clock; one Codex work unit. |
| Blast radius | Payment order status mutation, payment audit events, gateway boot wiring, config loading. No auth, quota, billing ledger, database schema, frozen package, or deployment script changes. |
| Failure modes | Wrong expiry predicate could expire future orders or paid orders; mitigate with discriminating test and requested mutation check. Missing audit could lose operator traceability; assert audit event. Worker nil-guard ordering could panic on nil receiver; mirror lease-sweeper guard style and add a nil receiver test. Misparsed config could unexpectedly enable worker; empty/0 interval remains disabled. |
| Decision points | No high-risk decision expected. If implementation requires schema/auth/billing-ledger/quota/deployment changes, stop for Owner confirmation. |
| Pre-execution checklist | 1. Inspect local payment/config/wiring and existing worker lifecycle. 2. Write failing tests before production code. 3. Implement store method and worker in non-frozen packages. 4. Wire config and gateway start/stop. 5. Run red/green tests, then mutate expiry predicate to verify test fails, then restore. 6. Run gofmt and required gates. 7. Search the three reference worktrees narrowly and summarize clean-room evidence in the commit message. 8. Stage and commit without push. |

## Concrete execution order

1. Add payment tests for stale pending expiration and sweeper nil receiver behavior.
2. Run the focused payment test command and confirm it fails because the new API/types do not exist yet.
3. Add `ExpireStalePendingOrders` to the payment `Store` interface and implement memory behavior.
4. Implement the Postgres expiration transaction in a focused payment store file to avoid expanding the existing large `store_postgres.go`.
5. Add `internal/payment/expire_sweeper.go` with `Start`, `RunOnce`, and `Stop` lifecycle matching the existing nil-guard-before-lock worker pattern.
6. Add `HUAKAI_PAYMENT_EXPIRE_SWEEP_INTERVAL` and `HUAKAI_PAYMENT_EXPIRE_SWEEP_BATCH_LIMIT` parsing to `internal/config`, with tests for disabled-by-empty, enabled interval, default batch, and invalid values.
7. Update gateway wiring and runtime cleanup to start/stop the sweeper only when config interval is positive.
8. Run the focused payment tests, then perform the requested mutation by flipping the expiry predicate and confirm the discriminating test fails.
9. Restore the predicate, run `gofmt`, then run the required build/vet/payment test gate plus config tests.
10. Run narrow clean-room reference searches in `/home/ubuntu/refs/{sub2api,new-api,CLIProxyAPI}` only for order-expiration/sweeper evidence, record absence/presence in the commit body, then commit without push.
