# 12 Payment order / recovery / refund

## Sub2API behavior summary

Sub2API models the full payment order lifecycle. Order entities store trade IDs, a provider configuration snapshot, status, and timestamps. Refund state, reason, time, and requester are persisted alongside the order. A payment audit log records every action. Order creation persists a pending order and invokes the provider. Fulfillment logic marks orders as paid, recharging, completed, or failed and supports retry. Refund logic has prepare, execute, fail, success, rollback, and status-restore phases. A webhook handler caps the incoming body, verifies provider signatures, acknowledges unknown orders to avoid loops, and sends provider-specific success responses. Admin can retry fulfillment and process refunds. A signed resume token with expiry exists for interrupted payment flows.

## Entity / fields

Payment order includes user, amount, provider instance/key/snapshot, status, trade numbers, timestamps, refund state fields, and audit log linkage.

## Request chain

Create order -> provider create payment -> webhook/query marks paid -> fulfillment updates balance/subscription -> complete/fail -> admin retry/refund/recover.

## State machine

`pending -> paid -> recharging -> completed | failed | expired | cancelled | refund_requested -> refunding -> refunded/partially_refunded/refund_failed`.

## Failure modes

- Paid webhook but fulfillment fails.
- Unknown order webhook loops unless acknowledged.
- Provider config changes after order creation.
- Refund rollback can fail critically.

## Sub2API capability

Sub2API has full order lifecycle, provider snapshot, webhook ack, fulfillment retry, refund rollback, audit and resume token.

## HUAKAI current capability

HUAKAI only has broad `F-PAY-001` in `docs/03_FEATURE_PARITY_MATRIX.md:75`.

## HUAKAI gap

`MISSED_BY_HUAKAI`: payment must be split into order state, webhook idempotency, fulfillment recovery, refund rollback, provider snapshot and resume token.

## HUAKAI stronger design

Add payment order state machine plus fulfillment outbox/retry and refund plan/execute/rollback. Tie balance mutation to ledger events.

## Suggested Feature ID / level

- `F-PAY-ORDER-001`: L3
- `F-PAY-WEBHOOK-001`: L3
- `F-PAY-FULFILL-001`: L3
- `F-PAY-REFUND-001`: L3
- `F-PAY-RESUME-001`: L3

## Acceptance tests

- Paid order with fulfillment failure is admin-retryable.
- Unknown webhook returns provider success and logs warning.
- Refund gateway failure restores local balance/subscription or marks rollback failure.

## Open questions

- open-question: which payment providers are in HUAKAI Phase 1.

---
Source files read: sub2api backend/ent/schema/payment_order, backend/ent/schema/payment_audit_log, backend/internal/service/payment_order, backend/internal/service/payment_fulfillment, backend/internal/service/payment_refund, backend/internal/handler/payment_webhook_handler, backend/internal/handler/admin/payment_handler, backend/internal/service/payment_resume_service
Lane: specifier
Agent: claude executor (scrub pass 2026-05-06)
UTC timestamp: Wed May  6 07:29:28 UTC 2026
