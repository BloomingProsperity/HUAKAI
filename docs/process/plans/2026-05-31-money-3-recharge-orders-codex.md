# 2026-05-31 MONEY-3 Recharge Orders
| Owner directive | "Do ONE task now... MONEY-3 充值订单实体+创建管线(新 internal/payment 包,迁移 0061),仅建单不接支付" |
| Scope | In: local recharge order schema, service/store create pipeline, postgres tests, gateway wiring. Out: payment provider calls, callbacks, balance credit capture, admin/user HTTP routes. |
| Success criteria | Creating a 50.00000000 USD order stores `PENDING`, unique external trade id, and credited amount exactly; pending limit rejects N+1; forced concurrent id collision yields exactly one success; migration up/down parses and round-trips locally when PostgreSQL is available. |
| Time estimate | 2-4 wall-clock hours depending on integration DB and Codex review latency; one focused worker commit. |
| Blast radius | New additive table and new non-frozen package. Wiring adds a dependency pointer but no route exposes payment yet. No production deployment or landing merge by this worker. |
| Failure modes | Schema mistake blocks migrations; pending-limit race allows too many open orders; id collision retry masks uniqueness; clean-room leakage from non-MIT references; generated/god-package churn. Mitigation: write discriminating tests first, use HUAKAI names, no AGPL/LGPL source read in implementer session, no new files in frozen packages. |
| Decision points | Schema/payment logic is high risk for merge/deploy; dispatcher/Owner must audit before landing. This worker treats the board assignment as permission to implement and push for review only, not production approval. |
| Pre-execution checklist | Read CLAUDE.md #8/#13/#14 and docs/05 clean-room policy; avoid reference-source re-read; claim task files; create plan; write RED tests; implement minimal local package; run targeted tests; run review gate; push work branch; mark review. |

## File Scope

- Create `backend/internal/payment/order.go`: HUAKAI-owned domain types, validation, errors, deterministic recharge reference generation, and service orchestration.
- Create `backend/internal/payment/store_postgres.go`: serializable transaction that counts pending rows, enforces per-user pending and daily amount limits, inserts one order, maps unique conflicts.
- Create `backend/internal/payment/order_test.go`: unit tests for validation/reference behavior if needed.
- Create `backend/internal/payment/store_postgres_integration_test.go`: PostgreSQL discriminating tests for create, pending limit, and concurrent external id collision.
- Create `backend/sql/migrations/0061_recharge_orders.up.sql`: additive `recharge_orders` table, checks, indexes, and uniqueness.
- Create `backend/sql/migrations/0061_recharge_orders.down.sql`: dev rollback for the additive table.
- Modify `backend/cmd/gateway/wiring.go`: add `paymentService` dependency and instantiate with `payment.NewService(payment.NewPostgresStore(pgPool))`.

`backend/internal/payment` is a new package and not frozen. `backend/cmd/gateway` is not frozen. No new files under `backend/internal/{gatewayhttp,gateway,proto}`.

## Clean-Room Guard

I will not read AGPL/LGPL source files in this implementer session. The implementation uses only the board behavior contract, HUAKAI docs, and local code patterns. Upstream names called out as forbidden in the task will not appear as Go symbols or comments. Reference citations, if needed, stay in commit/review notes rather than code.

## Execution Order

1. Write RED integration tests in `backend/internal/payment/store_postgres_integration_test.go`.
   - `TestPostgresStoreOpenRechargePersistsPendingOrder`: create a 50 USD order and assert stored status, external id uniqueness, credited amount scale, and generated recharge ref.
   - `TestPostgresStoreOpenRechargeEnforcesPendingLimit`: create up to max pending and assert N+1 returns `ErrPendingLimit`; mutation deleting the count guard makes this red.
   - `TestPostgresStoreOpenRechargeEnforcesDailyAmountLimit`: create one 50 USD order under a 99 USD daily cap and assert the second same-day order is rejected; mutation deleting the sum guard makes this red.
   - `TestPostgresStoreOpenRechargeExternalTradeIDUniqueUnderRace`: inject a fixed external id generator and run two concurrent creates; assert exactly one succeeds and one returns `ErrExternalTradeConflict`.
2. Run the new package tests with `go test -tags=integration_pg ./internal/payment`; expected RED because the package/schema does not exist yet.
3. Add `order.go` and `store_postgres.go` with minimal API:
   - `CreateInput{TenantID, UserID, Amount, CurrencyCode, MaxPendingPerUser, DailyAmountLimit, ExternalTradeID, Now}`
   - `Service.OpenRecharge(ctx, input)`
   - `PostgresStore.OpenRecharge(ctx, input)`
4. Add migration 0061 with `numeric(20,8)`, `PENDING/PAID/CREDITING/COMPLETED/FAILED/EXPIRED/CANCELLED`, tenant/user FK, unique `(tenant_id, external_trade_no)`, pending count index, daily amount index, and unique `(tenant_id, recharge_ref)`.
5. Wire the service in `backend/cmd/gateway/wiring.go`.
6. Run targeted tests:
   - `cd backend && go test ./internal/payment`
   - `cd backend && go test -tags=integration_pg ./internal/payment`
   - `cd backend && go test ./cmd/gateway`
7. If a local DB is available, apply up/down/up migration using the repo migration tool or `psql`; otherwise record that integration migration round-trip was skipped due missing `HUAKAI_DATABASE_URL`.
8. Stage intended diff and run `timeout 600 codex exec review --uncommitted -m gpt-5.5 -c model_reasoning_effort=xhigh`. Commit only if no unresolved S0/S1.
9. Push `HEAD:work/money-3`, then `bash .coordination/task.sh review MONEY-3 "branch work/money-3 @ <sha> + self-review result"`.

## Test Quality

The pending-limit test is the main mutation discriminator: deleting the `max pending` guard makes the N+1 order succeed and the test fails. The race test discriminates uniqueness by forcing both goroutines to use the same external id; if the table lacks the unique index or the store silently retries that injected id, both can succeed and the test fails.
