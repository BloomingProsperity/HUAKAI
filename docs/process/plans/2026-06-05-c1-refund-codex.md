# 2026-06-05 C1 Refund Codex Plan

| Owner directive | "任务: 商业 C1 退款 refund(MONEY 高危,极度保守)" |
| Scope | In: HUAKAI-only implementation of admin refund for paid payment orders, migration 0092, net balance, billing event, audit, HTTP route, OpenAPI, tests, local commit. Out: reference source reading, push, TASK.md, frozen package new files, schema beyond the specified migration. |
| Success criteria | A paid order can be refunded once per order/idempotency key; repeated key returns existing result without double deduction; non-paid refund rejects without writes; balance reads credit minus refund in memory and PostgreSQL; OpenAPI and gateway route tests stay consistent; required build/vet/test gates pass; commit created without push. |
| Time estimate | 2-4 wall-clock hours; one Codex implementation pass with verification and review. |
| Blast radius | Money path: payment order state, credit-derived balance, billing_events, audit events, admin payment HTTP contract, migrations. |
| Failure modes | Double refund or repeated billing event: enforce unique tenant/order and tenant/idempotency plus store-level idempotent branch. Wrong balance: update the shared memory and PostgreSQL balance helpers and add discriminating tests. Dirty non-paid state: lock order first and return before inserts. OpenAPI drift: add route contract and run cmd/gateway tests. Clean-room risk: do not read reference source and do not cite it. |
| Decision points | Stop for Owner only if the existing schema cannot safely represent `refunded`, if the only balance source is ambiguous, or if required money-path checks cannot run. |
| Pre-execution checklist | 1. Read `docs/RULES.md` and HUAKAI payment/billing/http code only. 2. Confirm migration path and 0091 format. 3. Confirm frozen packages are not receiving new files. 4. Write tests before production edits. 5. Run mutation check for net-balance subtraction. 6. Run gofmt and required gates. 7. Stage intended files, run Codex review if CLI supports it, then commit. |

## Concrete Execution Order

1. Add payment service/store tests for refund happy path, idempotency, non-paid rejection, and net balance.
2. Run the narrow tests to confirm they fail for missing API/behavior.
3. Add `payment_refunds` migration 0092 and update order status CHECK to include `refunded`.
4. Add refund types/errors/store interface, memory refund storage, net balance subtraction, and audit event.
5. Add PostgreSQL refund implementation in `backend/internal/payment/store_postgres_refund.go`, using one transaction with order lock, idempotency lookup, refund insert, negative billing event, order status update, audit, and net balance read.
6. Add service validation and delegate to store.
7. Add payment HTTP request/handler/route/error mapping/test mock, then wire gateway admin payment route if route mounting requires explicit registration.
8. Add OpenAPI `/v1/admin/payments/{id}/refund`.
9. Run focused tests, perform the requested mutation check by temporarily removing refund subtraction and confirming the balance test fails, then restore.
10. Run `gofmt`, required build/vet/test gates with `/usr/local/go/bin/go` and `GOCACHE=/tmp/go-build`.
11. Stage intended changes, run `codex exec review --uncommitted --full-auto --sandbox read-only` if available, normalize any findings, commit with the required co-author trailer, and do not push.
