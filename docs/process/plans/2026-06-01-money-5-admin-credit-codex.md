# 2026-06-01 MONEY-5 admin manual balance adjustment

| Owner directive | "Do ONE task now... MONEY-5 admin 手动充值/调额 + 统一审计(不需外部网关,先让闭环可用)" |
| Scope | In: implement the assigned admin manual credit/debit path in `backend/internal/payment`, `backend/internal/adminhttp`, gateway route wiring, and matching OpenAPI contract; require an operator-supplied idempotency key for retry safety; add discriminating tests for admin-only access, recharge audit, billing event, returned net balance, debit clamp-to-zero, and idempotent replay. Out: new schema migrations, provider gateway/payment integration, frozen package additions, production deployment, landing merge. |
| Success criteria | Admin credits a user by 200 USD and receives the net balance; `user_balances.balance` is `200.00000000`; `payment_audit_log` has `RECHARGE_SUCCESS`; `billing_events` has `balance_recharged`; a later `-250` adjustment clamps balance to `0.00000000`; replaying the same idempotency key/body is a no-op; non-admin request returns 403 and writes no money rows. |
| Time estimate | 3-5 wall-clock hours including TDD red/green checks, focused integration tests, Codex pre-commit review, commit, push, and task review handoff. |
| Blast radius | Money path and admin HTTP surface. Runtime files are non-frozen except existing `backend/cmd/gateway/routes.go`/`wiring.go`, which are allowed for route wiring. `docs/openapi/openapi.yaml` is updated only to keep the existing implementation/spec consistency test green. No schema changes are planned because MONEY-4 already added audit and balance recharge event schema. |
| Failure modes | Auth guard regression could allow arbitrary recharge; missing idempotency could double-credit on operator retry; clamp bug could write negative wallet balances; audit omission could make manual operator action unreconstructable; billing event insert could violate MONEY-4 constraints; raw reason/metadata could leak secrets; route wiring could expose the endpoint without admin middleware. Mitigation: write failing tests first, assert exact good state not only absence of bad state, require stable idempotency keys, keep metadata redacted, reuse existing payment credit primitives, and run focused payment/admin tests. |
| Decision points | No new Owner decision is surfaced in this implementation plan; the board assignment authorizes implementation and review handoff only. Because this is money-path, the worker will not mark done or merge; dispatcher/Owner gates the landing path. If existing admin auth primitives cannot be identified safely, park the task instead of inventing a bypass. |
| Pre-execution checklist | `task.sh start MONEY-5` has claimed the task. Read `.coordination/DISPATCH.md`, `CLAUDE.md` #8/#11/#12/#13/#14, `AGENTS.md`, `docs/RULES.md`, and money-loop plans. Confirm target packages are not frozen before adding files. Do not read non-MIT reference source in this implementation lane. Write tests before production code. |

## Target Files

- `backend/internal/payment/admin_credit.go`: new non-frozen payment package file for manual balance adjustment service logic.
- `backend/internal/adminhttp/balance_credit_handler.go`: new non-frozen admin HTTP package file for request parsing, admin guard usage, and response mapping.
- `backend/cmd/gateway/routes.go`: existing route file for mounting the admin balance route through existing admin dependencies.
- `backend/cmd/gateway/wiring.go`: listed by the board; inspect only unless dependency construction needs a change.
- `docs/openapi/openapi.yaml`: low-risk contract support required by the repo's route/spec consistency test after mounting the route.

## Execution Order

1. Inspect existing payment service, credit primitive, audit log, billing event, admin HTTP auth, and route wiring patterns.
2. Write RED tests:
   - payment integration: `+200` creates balance/audit/billing-event and returns `200.00000000`.
   - payment integration: `-250` from `200` clamps final balance to zero.
   - payment integration: same idempotency key/body replay does not change balance or duplicate audit/event rows.
   - handler/auth test: non-admin gets 403 and store/service is not called.
3. Run focused tests and confirm the new tests fail for missing behavior.
4. Implement minimal service/handler/wiring:
   - transactionally adjust `user_balances`, clamping negative final balance to zero.
   - write `payment_audit_log` success row and a `billing_events.balance_recharged` row for positive manual recharge.
   - require and persist the operator idempotency key as the manual trade identifier so retries are no-ops.
   - reject non-admin before invoking payment logic.
   - add the matching OpenAPI path so mounted routes do not create impl-only drift.
5. Run focused tests, then broader relevant package tests.
6. Stage intended diff, run `codex exec review --uncommitted -m gpt-5.5 -c model_reasoning_effort=xhigh`, normalize findings, and fix any S0/S1 before commit.
7. Commit, push exact commit to `origin work/money-5`, and mark task review with branch and SHA.

## Clean-Room Note

This implementation relies on the assigned behavior contract and HUAKAI-local code. I will not re-read LGPL/AGPL reference source in this implementation lane, will not vendor source, and will not copy upstream identifiers, comments, schemas, or implementation structure.
