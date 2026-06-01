# 2026-06-01 MONEY-6 balance enforcement mandatory

| Owner directive | "MONEY-6 余额 enforcement 翻 mandatory(Owner:没余额=402,直接显示「余额不足」)+历史 backfill + admin/trust bypass" |
| Scope | In: balance hold reservation mode, billing setting parsing/store support, gateway 402 error body, migrations 0064/0065, focused unit/integration tests. Out: admin UI/API for editing this setting, new auth roles, payment callback logic, quota ledger redesign. |
| Success criteria | Mandatory mode treats missing `user_balances` as insufficient balance; opt-in mode still allows missing rows; gateway returns HTTP 402 with `type=insufficient_quota`, `code=insufficient_balance`, `message=余额不足`; migration 0065 backfills historical USD voucher redemptions into `user_balances`; users with sufficient balance still pass. |
| Time estimate | 2-4 hours wall clock; one Codex worker session. |
| Blast radius | Money-path Tx1 reservation, public gateway error shape, billing settings constraints, and database migration order. |
| Failure modes | Over-blocking old users if backfill misses voucher history; under-blocking new users if mandatory mode is ignored; weak tests if they only assert status; schema rollback losing live rows if down migration deletes data. Mitigation: discriminating tests for mandatory vs opt-in, gateway body fields, and migration SQL content; conservative down migration removes settings/check constraints only, not user balances. |
| Decision points | Admin/trust bypass is not implemented because the current gateway auth identity has only tenant/api-key/user IDs and no admin/trust tier. If Owner wants a bypass tier, it needs a separate auth-contract slice. MONEY-6 is money/schema risk and remains dispatcher/Owner-gated after review. |
| Pre-execution checklist | Read `.coordination/DISPATCH.md`, `CLAUDE.md` #8/#13/#14, clean-room policy, F-OBS-001 billing spec, current balance hold and billing settings code, current voucher/user balance schemas; start task with `.coordination/task.sh start MONEY-6`; work in isolated worktree because the main checkout has unrelated dirty state. |

## Concrete Execution Order

1. Add RED tests:
   - `balancehold` integration test: mandatory mode + no `user_balances` returns `ErrInsufficientBalance`; existing opt-in missing-row test remains green after signature update.
   - `billing` unit tests: parse/store support for `balance_enforcement_mode` with `mandatory` default and `opt_in` escape hatch.
   - `gatewayhttp` unit test: `billing.ErrInsufficientBalance` response is HTTP 402 with exact client-parseable fields and no generic reserve error.
   - migration text test if no migration runner exists locally: assert 0065 backfill groups successful USD voucher redemptions into `user_balances` with `ON CONFLICT` additive semantics.
2. Run targeted tests and confirm the new tests fail on current code.
3. Implement:
   - Add `BalanceEnforcementMode` constants and parser.
   - Add store/resolver support for the new setting.
   - Extend `billing.ReserveRequest` and `balancehold.ReserveParams` to carry enforcement mode.
   - Pass resolved mode from `gatewayhttp` to `ClaimGate.Reserve`.
   - Change only the insufficient-balance gateway branch to the required 402 body.
   - Add migrations 0064 and 0065.
4. Run targeted tests, then `go test ./internal/balancehold ./internal/billing ./internal/gatewayhttp`.
5. Run generated/sql checks if needed; run `go test ./...` or the broadest feasible backend suite.
6. Stage only MONEY-6 files, run `timeout 600 codex exec review --uncommitted -m gpt-5.5 -c model_reasoning_effort=xhigh`; record timeout/error if best-effort self-review cannot complete.
7. Commit, push `HEAD:work/money-6`, then mark review with branch + SHA + self-review result.
