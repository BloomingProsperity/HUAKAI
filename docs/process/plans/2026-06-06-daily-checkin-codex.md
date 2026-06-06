# 2026-06-06 Daily Check-In Reward Codex Plan

| Owner directive | `TASK: Implement an opt-in DAILY CHECK-IN REWARD feature for HUAKAI (branch fix/daily-checkin).` |
| Scope | Add an opt-in UTC daily user check-in reward using existing payment credit machinery. In scope: migration 0097, `payment` check-in transaction method, new `checkin` service/read store, new `checkinhttp` user routes, platform setting keys, gateway wiring, unit tests, and integration_pg tests matching the existing harness. Out of scope: reading `/home/ubuntu/refs`, external reference projects, production deployment, commits, auth-core changes, quota enforcement changes, billing ledger schema changes beyond the requested `daily_checkin` table. |
| Success criteria | Disabled-by-default settings exist; authenticated session user can get status and perform one UTC-day check-in when enabled; reward is random in `[min,max]`; concurrent same-day requests produce exactly one `daily_checkin` row, one payment credit, one `payment_credited` billing event, and one balance increment; second same-day check-in returns `ErrAlreadyCheckedIn`; credit failure rolls back the `daily_checkin` insert; migration test verifies table, unique constraint, and index. |
| Time estimate | 2-4 hours agent time depending on local compile/test failures; PM must run `integration_pg` later because this sandbox cannot. |
| Blast radius | Money path tables: `payment_orders`, `payment_credits`, `billing_events`, `user_balances`, and new `daily_checkin`. HTTP blast radius is session-protected `/v1/me/checkin`. Platform setting blast radius is adding three allowed global keys. |
| Failure modes | Double-credit under concurrency: mitigate with serializable transaction, `lockPaymentUserTx`, `daily_checkin` unique key, stable payment `out_trade_no`, and payment credit unique order. Orphan check-in row without credit: mitigate by inserting check-in and crediting in one transaction, updating `billing_event_id` before commit, and testing rollback on injected credit failure. Disabled feature writes money: mitigate by reading config before reward generation/payment call and testing no write. Bad config creates zero/negative credit: mitigate by validating positive min/max and `min <= max`. HTTP auth bypass: mount only inside existing session middleware and read `SessionFromContext`. |
| Decision points | PM/Owner should confirm whether settings should be exposed in admin settings UI/API beyond being valid platform setting keys; PM must run the real `integration_pg` gate and mutation checks. No high-risk schema/auth/billing-ledger changes beyond the requested additive table will be made here. |
| Pre-execution checklist | 1. Verify branch/status and avoid unrelated untracked files. 2. Trace admin credit transaction and payment store helpers. 3. Trace platform setting defaults/validation. 4. Trace session-protected `/v1/me` route pattern. 5. Write tests before implementation. 6. Implement only in editable/new packages; do not add files to frozen packages. 7. Run `gofmt`, package tests, `go build ./...`, and `go vet ./...`; report sandbox limits. |

## Concrete execution order

1. Add failing tests for `platformsettings` check-in keys and check-in service config/reward behavior.
2. Add failing HTTP tests for session requirement, disabled response, already-checked-in response, and success response shape.
3. Add failing `integration_pg` tests in `payment` for concurrency, replay, rollback, cross-day, disabled no-write via service, and migration 0097 shape.
4. Add migration `0097_daily_checkin`.
5. Implement `payment.CheckinRewardInput`, `CheckinRewardResult`, `Service.ApplyCheckinReward`, and `PostgresStore.ApplyCheckinReward` using a single serializable transaction and the existing payment credit helpers.
6. Implement new `backend/internal/checkin` package with config read/validation, UTC date/month handling, random reward generation, and Postgres read store.
7. Implement new `backend/internal/checkinhttp` package and route tests using `auth.ContextWithSession`.
8. Wire check-in service in `backend/cmd/gateway/wiring.go` and mount `GET/POST /v1/me/checkin` in `backend/cmd/gateway/routes.go` under existing session middleware.
9. Run formatting and available non-socket checks; leave `integration_pg` for PM.
