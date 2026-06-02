# 2026-06-02 money-5 admin balance adjustment fix
| Owner directive | "HUAKAI money-5 FIX — admin 余额调整 money-path 缺陷修复...自主跑完:修→判别测试→build/test→self-review→commit→push." |
| Scope | In: fix admin manual balance adjustment debit safety, idempotency conflict HTTP mapping, focused tests, and OpenAPI contract note. Out: schema migration, new billing table, runtime dependency, unrelated money-path refactor, reference-project source. |
| Success criteria | Negative admin amount is rejected with 400 `admin_debit_not_yet_supported` and no `user_balances` mutation; reused `idempotency_key` with different input maps to HTTP 409; positive admin credit behavior remains durable through `balance_recharged`; required build/tests/self-review pass; commit is pushed to `origin work/money-5-fix`. |
| Time estimate | 60-90 minutes wall clock; 1 Codex implementer session. |
| Blast radius | Money path and admin billing HTTP endpoint. Files are existing non-frozen `backend/internal/payment/*`, existing non-frozen `backend/internal/adminhttp/*`, and `docs/openapi/openapi.yaml`. No file is added under frozen packages `backend/internal/{gatewayhttp,gateway,proto}`. |
| Failure modes | Rejecting negative amounts may break callers that expected manual debit; mitigation: explicit roadmap error code and OpenAPI note because durable debit events need schema support. A debit gate placed before idempotency lookup could hide replay/conflict state for existing keys; mitigation: perform replay/conflict lookup first and gate only new debit keys. Incorrect 409 mapping could hide backend health; mitigation: map only `payment.ErrExternalTradeConflict`. Weak tests could pass under mutation; mitigation: run focused RED, then mutation self-check by temporarily reverting the relevant guards/mapping. |
| Decision points | FIX-1 mode: `event` is blocked without migration because `billing_events_event_type_check` currently allows `balance_recharged` but no debit event type. Owner already forbade new migrations, so choose `gate`. No further Owner sign-off unless tests prove a schema change is unavoidable. |
| Pre-execution checklist | Read `CLAUDE.md`, `AGENTS.md`, `docs/RULES.md`, `.coordination/README.md`; inspect `backend/internal/payment/admin_credit.go`, `backend/sql/migrations/0063_billing_events_balance_recharged.up.sql`, `backend/internal/adminhttp/balance_credit_handler.go`, current tests, and OpenAPI route; claim files through `.coordination`; write failing tests before production edits. |

## Concrete execution order

1. Add RED tests:
   - payment PG integration: after a positive credit, a new negative key returns `payment.ErrAdminDebitNotSupported` and leaves `user_balances` unchanged.
   - payment PG integration: reusing a positive credit key with a negative amount returns `payment.ErrExternalTradeConflict` before the debit gate.
   - payment PG integration: a legacy accepted debit key replays before the debit gate.
   - admin HTTP unit: service debit-gate errors map to 400 `admin_debit_not_yet_supported`.
   - admin HTTP unit: positive and negative idempotency conflicts map to 409, not 503.
2. Run focused tests and confirm RED failures.
3. Implement minimal changes:
   - add `ErrAdminDebitNotSupported`;
   - reject new negative admin adjustments in the Postgres money path after replay/conflict lookup;
   - map the debit gate at HTTP with explicit roadmap code;
   - map `ErrExternalTradeConflict` to HTTP 409.
4. Update `docs/openapi/openapi.yaml`:
   - add 409 response to `/admin/v1/balances/adjustments`;
   - describe negative amounts as currently gated until durable debit event schema lands;
   - add this plan to `x-huakai-spec-source`.
5. Run `gofmt` on touched Go files.
6. Run focused tests, requested build/tests, and PG integration test.
7. Mutation self-check:
   - temporarily remove negative gate and verify debit guard tests fail;
   - temporarily add the old pre-lookup gate and verify replay/conflict tests fail;
   - temporarily remove 409 mapping and verify conflict tests fail;
   - restore implementation and rerun focused tests.
8. Stage intended diff and run Codex self-review command from Owner directive. Normalize/fix any S0/S1 findings within the two-round cap.
9. Commit with root cause, rules touched, and review verdict; push `HEAD:work/money-5-fix`.

## Assumptions and risks

- This is IMPLEMENTER lane work on HUAKAI-owned code only; no reference-project source is read or summarized.
- Gate mode is a feature-preserving safe equivalent, not a silent drop: debit support becomes Mandatory Roadmap until a durable debit billing event type can be added with an approved schema migration.
- FIX-3 is moot in gate mode because debit clamp no longer executes. The existing zero-balance metadata omission is not changed in this patch to avoid expanding scope after the negative path is disabled.
