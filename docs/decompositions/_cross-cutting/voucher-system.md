# Voucher System Cross-Cutting Decomposition

| Field | Value |
| --- | --- |
| Status | Draft |
| Capability | F-BILL-002 |
| Author | Codex GPT-5 implementer/spec writer |
| Date | 2026-05-16 |
| Lane mode | Option B, HUAKAI-owned commercial billing spec from local docs and Owner directive only |
| References read | HUAKAI docs only; no reference-project source |

## Purpose

This decomposition places the voucher system between HUAKAI User identity, balance/quota state, billing events, and the F-TRUST audit chain. It is a docs-only planning artifact for Phase 6 commercial foundation work.

The feature outcome is simple: Admin issues a voucher; User redeems it; balance/quota increases; later F-BILL-001 request settlement consumes that balance through existing reserve/settle paths.

## Boundary Map

```
F-AUTH-007 User Authentication
  -> proves HUAKAI User identity
  -> supplies tenant_id + user_id for redemption
  -> owns invite-code registration, not voucher top-up

F-SESSION-001 Platform Session Management
  -> authenticates the logged-in redeem request
  -> owns session token/refresh lifecycle
  -> does not own voucher eligibility or balance mutation

F-BILL-002 Voucher System
  -> owns voucher create/batch/redeem/expire/revoke
  -> writes voucher_redemption
  -> writes billing_events(type=voucher_redeemed, value, voucher_id)
  -> increases User balance / balance-backed quota atomically
  -> emits F-TRUST voucher audit events

F-BILL-001 / F-OBS-001 Tx2 Settlement
  -> owns request reserve/reconcile for model usage
  -> consumes the balance made available by voucher redemption
  -> keeps Usage Record and request Billing Ledger immutable
  -> does not create or validate voucher codes

F-TRUST Audit Chain
  -> records voucher_created / voucher_redeemed / voucher_expired / voucher_revoked
  -> also records failed redeem and burst-alert events
```

## In Scope

- Tenant-scoped voucher entity with unique code, value, validity window, max redemptions, single-use-per-user policy, and lifecycle status.
- Admin single create and batch create.
- User redeem endpoint after HUAKAI login.
- Race-safe redemption with PostgreSQL advisory lock in the future implementation wave.
- Billing event and balance/quota mutation in one transaction.
- Anti-fraud limit and burst alert.
- Audit trail sufficient for support and commercial investigation.

## Out Of Scope

- Payment providers, checkout, refunds, chargebacks, and PCI handling.
- Request pricing formula or model-usage cost calculation.
- Provider Account credential acquisition, refresh, storage, or routing.
- User invite redemption for registration.
- Backend code, schema migrations, or dependencies in this wave.

## Storage Intent

The implementation wave should introduce storage equivalent to this model. Names are logical and not migration commitments.

```
voucher
  tenant_id
  voucher_id
  code_hash_or_fingerprint
  value_cents
  valid_from
  valid_until
  max_redemptions
  single_use_per_user
  status / revocation marker
  batch_id
  created_by_admin_id
  created_at
  revoked_at / revoked_by / revoke_reason

voucher_redemption
  tenant_id
  redemption_id
  voucher_id
  user_id
  idempotency_key
  value_cents
  status
  redeemed_at
  billing_event_id
  request_id
  source_ip_hash_or_class

billing_events
  tenant_id
  type = voucher_redeemed
  voucher_id
  redemption_id
  user_id
  value
  idempotency_key
  created_at

audit_events
  tenant_id
  action in voucher_created / voucher_redeemed / voucher_expired / voucher_revoked / voucher_redeem_failed / voucher_redeem_burst_alert
  actor_id
  target ids
  reason class
  request id
  redacted evidence
```

## Redemption Transaction Shape

1. Apply pre-transaction attempt-rate gate for `(tenant_id, user_id, ip_hash_or_class, minute_bucket)`.
2. Start database transaction.
3. Acquire transaction-scoped advisory lock for the voucher identity. If single-use-per-user is true, the transaction also protects the voucher/User uniqueness check.
4. Reread voucher row under transaction.
5. Check status, validity window, redemption count, tenant, eligibility, and revocation marker.
6. Check idempotency key and existing redemption row for this User.
7. Insert redemption row.
8. Increment redeemed count and mark exhausted when needed.
9. Increment User balance or balance-backed quota.
10. Insert billing event with `type = voucher_redeemed`.
11. Insert F-TRUST audit event or enqueue audit inside the same durable boundary required by F-TRUST implementation.
12. Commit.

The binding invariant is all-or-nothing: a successful User-facing redemption must leave voucher redemption, balance/quota, billing event, and audit evidence in a consistent state.

## Race Model

Two races matter:

| Race | Required outcome |
| --- | --- |
| Same voucher, many Users | At most `max_redemptions` successful commits. Losing requests reread after lock and reject as exhausted if capacity is gone. |
| Same voucher, same User | If `single_use_per_user = true`, exactly one successful redemption for that User. Replays with same idempotency key return the prior result; conflicting retries reject safely. |

The future implementation should follow the local F-CRED S8 advisory-lock style: acquire a transaction-scoped PostgreSQL advisory lock, reread authoritative state after the lock, and let stale workers observe the new state instead of performing duplicate external or balance effects.

## F-BILL-001 Boundary

F-BILL-001 request settlement remains the only owner of model-usage charge calculation and request Tx1/Tx2. Voucher redemption is a top-up event that changes available balance before later request admission.

Voucher redemption may reuse local billing lock-order discipline for User balance mutation, but it must not:

- open an upstream request claim,
- write or mutate a Usage Record,
- infer model usage,
- bypass F-BILL-001 quota reservation for later gateway requests,
- rewrite historical request Billing Ledger rows.

## F-AUTH-007 Boundary

F-AUTH-007 owns invite-code registration. F-BILL-002 owns voucher-code top-up. Both may use code-like user input, but they differ in target and effect:

| Feature | Code purpose | Effect |
| --- | --- | --- |
| F-AUTH-007 invite | Controls registration or invite attribution. | Creates/binds User identity state. |
| F-BILL-002 voucher | Adds commercial credit after User identity exists. | Adds balance/quota credit and billing event. |

A registration invite must not add balance unless a future Owner-approved feature explicitly composes invite and voucher as separate artifacts.

## Audit Chain

The minimum audit chain is:

1. `voucher_created` for each created voucher.
2. Batch summary audit for batch create.
3. `voucher_redeemed` for successful redemption.
4. `voucher_expired` when status transitions by time/worker/admin action.
5. `voucher_revoked` for admin revocation.
6. `voucher_redeem_failed` for rejected attempts where auditing is useful and safe.
7. `voucher_redeem_burst_alert` for anti-fraud burst detection.

Raw voucher code is not part of routine audit. Use voucher id, code fingerprint, or redacted suffix.

## Acceptance Mapping

| Scenario | Acceptance ID |
| --- | --- |
| Admin creates one voucher | AT-BILL-002-001 |
| User redeem happy path | AT-BILL-002-002 |
| Concurrent race same voucher/User | AT-BILL-002-003 |
| Expired voucher reject | AT-BILL-002-004 |
| Wrong User or tenant reject | AT-BILL-002-005 |
| Revoked voucher reject/no refund | AT-BILL-002-006 |
| Audit and billing event chain | AT-BILL-002-007 |
| Anti-fraud per User+IP and burst alert | AT-BILL-002-008 |
| Idempotency retry | AT-BILL-002-009 |
| Admin batch create | AT-BILL-002-010 |

## Assumptions

- Voucher value is represented as cents in admin/user-facing logical entity fields because the Owner asked for `value (cents)`.
- Later ledger implementation still follows exact-money rules and should avoid float arithmetic.
- Revocation does not refund. Any later refund/correction is a separate explicit adjustment feature.
- Anti-fraud thresholds are tenant/operator configurable; `N` remains an open default until Owner chooses it.

## Risks

| Risk | Mitigation |
| --- | --- |
| Voucher code enumeration | Safe generic user-facing failures, attempt limit, raw-code redaction. |
| Double credit under race | Advisory lock, transaction reread, redemption uniqueness, idempotency key. |
| Billing drift | Billing event and balance/quota mutation in one transaction. |
| Invite/voucher confusion | Explicit F-AUTH-007 boundary and separate audit events. |
| Support removes credit by revoking | Binding rule: revoke blocks future redemption only; no automatic refund. |
| Burst abuse | Per User+IP minute cap plus batch/User/IP burst alert. |

## Open Questions

1. Default value for `N`.
2. Eligibility policy scope for Phase 6: target User only, target email hash, User Group, campaign cohort, or all four.
3. Whether extending expired vouchers is allowed or replacement-only.
4. Exact balance/quota row to mutate in Phase 6 schema.
5. User-facing invalid-code wording.

Source files read: docs/RULES.md; .agents/skills/acceptance-test-writer/SKILL.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/11_ACCEPTANCE_TEST_MATRIX.md; docs/specs/observability-billing.md; docs/specs/user-authentication.md; docs/specs/session-management.md; docs/specs/credential-acquisition.md; docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md; docs/plans/2026-05-16-user-auth-session-spec-codex.md; docs/02_CAPABILITY_CONTRACT.md; docs/18_GLOSSARY.md; docs/19_DOMAIN_MODEL.md

Lane: implementer

Agent: Codex GPT-5

UTC: 2026-05-16T07:03:38Z
