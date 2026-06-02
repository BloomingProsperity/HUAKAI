# 2026-06-02 subscription-domain Codex plan

| Owner directive | "HUAKAI 订阅域 plan/subscription/订单(对照 new-api,clean-room 引证不抄)。IMPLEMENTER。新非冻结包。中文注释。自主→push origin HEAD:work/subscription-domain。不碰 landing。含 schema 迁移=高危,完工 park 等 Owner schema 批。" |
| Scope | In: `backend/internal/subscription`, `backend/internal/subscriptionhttp`, gateway wiring/routes, OpenAPI contract, focused tests, migration files for subscription plans/orders/subscriptions. Out: landing branch, `LICENSE`, production secrets, auth core, quota enforcement core, billing ledger mutation logic, payment provider implementation. |
| Success criteria | Admin can CRUD plans; user can list own subscriptions and create a subscription order; existing payment webhook path can activate a matching paid subscription order idempotently; expiry sweep marks due active subscriptions expired; discriminating PG tests fail if the subscription order loses its recharge linkage, double callback creates duplicate state, or expiry sweep is removed. |
| Time estimate | 4-6 hours wall clock, depending on PG integration environment and codex review output. |
| Blast radius | Medium/high: new DB schema and new routes; payment callback wrapper runs on the existing webhook path but should pass through ordinary recharge orders unchanged. Payment core implementation is not directly edited. |
| Failure modes | Missing migration on local DB can make integration tests fail; mitigate by naming migration clearly and reporting schema park. Wrapper could turn an already-credited payment into a 503 if activation fails; mitigate with idempotent activation and no-op when no matching subscription order exists. OpenAPI drift can fail route consistency; mitigate by updating `docs/openapi/openapi.yaml`. |
| Decision points | Owner schema approval is still required before landing the migration. If payment core changes become necessary, stop for Owner confirmation; current plan avoids that by composition. If OpenAPI path naming is disputed, keep routes explicit and record as follow-up rather than hiding implementation. |
| Pre-execution checklist | 1. Confirm worktree branch is `work/subscription-domain`. 2. Read `docs/RULES.md` and frozen package constraints. 3. Fetch/read only New API `origin/main` target files and record behavior evidence. 4. Read HUAKAI `internal/payment`, `paymenthttp`, `billing`, migration and gateway route patterns. 5. Write failing tests before production implementation. 6. Run build/tests and per-commit review before commit/push. |

## Clean-room source evidence

Reference lane: specifier-style behavior extraction only. Local implementation will not copy upstream function names, schemas, comments, identifiers, provider code, or file structure.

- Observed: New API stores plan duration, payment provider binding fields, purchase caps, quota totals, reset policy, and user-subscription lifecycle fields as subscription-domain state. Evidence: `QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:model/subscription.go:145`, `:204`, `:243`, `:284`, `:310`, `:319`.
- Observed: New API creates a pending subscription payment order after validating plan availability and user purchase cap, then uses provider callback/return handling to complete the order. Evidence: `QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/subscription_payment_epay.go:35`, `:53`, `:87`, `:118`, `:160`; `QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/subscription_payment_stripe.go:34`, `:67`, `:89`; `QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:controller/subscription_payment_creem.go:44`, `:73`, `:88`.
- Observed: New API completes a subscription order inside a transaction, treats already-successful order completion as a no-op, creates a user subscription snapshot, writes a related top-up record, then marks the order successful. Evidence: `QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:model/subscription.go:520`, `:536`, `:544`, `:558`, `:562`, `:565`, `:595`.
- Observed: New API has explicit expiration and reset sweeps for active subscriptions. Evidence: `QuantumNous/new-api@7aaa5332657e00fe801a2a7dd8b421e4ce4c842c:model/subscription.go:937`, `:963`, `:1048`, `:1214`, `:1237`.

HUAKAI fit: implement the behavior outcome with HUAKAI's PostgreSQL/sqlc-free store style in a new cohesive package. Money representation stays `numeric(20,8)` / `decimal.Decimal`; payment callback signature, anti-tamper, credit, payment audit, and `billing_events.recharge_order_id` remain owned by `internal/payment`.

## File plan

- Create `backend/sql/migrations/0070_subscription_domain.up.sql` and `.down.sql`: add `subscription_plans`, `subscription_orders`, and `user_subscriptions`. Also extend `billing_events` with nullable `subscription_order_id` and a CHECK/FK branch for linked recharge events only if needed by the discriminating linkage test.
- Create `backend/internal/subscription/types.go`: domain types, statuses, errors, request/result structs, duration/reset helpers.
- Create `backend/internal/subscription/store_postgres.go`: PostgreSQL store for plan CRUD, order creation linkage, activation after payment callback, listing, expiry sweep, reset sweep.
- Create `backend/internal/subscription/service.go`: validation, payment recharge composition, callback bridge, idempotent state transitions.
- Create `backend/internal/subscription/store_postgres_integration_test.go`: PG discriminating tests for callback activation/linkage, callback idempotency, and expiry.
- Create `backend/internal/subscriptionhttp/routes.go` and tests: admin plan CRUD, user subscription list/order creation.
- Modify `backend/cmd/gateway/wiring.go`: add subscription service and bridge into deps.
- Modify `backend/cmd/gateway/routes.go`: mount user and admin subscription routes; pass subscription payment bridge to `paymenthttp`.
- Modify `docs/openapi/openapi.yaml`: add declared paths so route consistency stays true.

## Execution order

1. Write failing PG integration tests in `internal/subscription` for activation/linkage, replay idempotency, and expiry sweep.
2. Write focused HTTP handler tests for admin CRUD auth behavior and user session sourcing.
3. Add migration and minimal subscription store/service implementation until tests pass.
4. Add HTTP routes and gateway wiring.
5. Update OpenAPI for new paths.
6. Run `gofmt`, targeted tests, `go test ./...`, `go build ./...`, and `go test -tags=integration_pg ./internal/subscription/...` with `HUAKAI_DATABASE_URL` when available.
7. Stage intended files, run `codex exec review --uncommitted --full-auto --sandbox read-only` if CLI supports it; cap at two rounds for S0/S1.
8. Commit and push `origin HEAD:work/subscription-domain`; do not touch landing.

## Assumptions

- The Owner directive is the start signal for implementation and branch push, but not final schema landing approval.
- The existing payment webhook route remains the only provider webhook endpoint; subscription activation is a downstream idempotent bridge when a paid recharge maps to a subscription order.
- Admin DELETE for plans will be a soft archive/disable operation, not destructive deletion.

## Risks

- Schema migration is high risk and must be parked for Owner schema approval.
- Activation after payment credit is a second transaction because payment core remains unchanged. If Owner requires single-transaction activation+credit later, that is a high-risk payment-core change requiring explicit approval.
- Subscription quota integration into gateway spending is not part of this slice unless Owner separately approves touching quota enforcement core.

## Parallel-plan note

AGENTS.md asks Claude and Codex to draft independently for non-trivial work. This session has only Codex available and the Owner explicitly requested autonomous implementation. This file records the Codex plan before execution; no Claude plan was read or used.
