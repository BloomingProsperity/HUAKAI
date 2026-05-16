# F-BILL-002: Voucher System

| Field | Value |
| --- | --- |
| Status | Draft |
| Feature ID | F-BILL-002 |
| Specifier | Codex GPT-5 implementer/spec writer |
| Specifier date | 2026-05-16 |
| Reviewer | Pending |
| Review date | Pending |
| Released date | Pending |
| Lane mode | Option B, HUAKAI-owned commercial billing spec from local docs and Owner directive only |
| Supersedes | Existing short F-BILL-002 roadmap row text in `docs/03_FEATURE_PARITY_MATRIX.md` |
| Superseded by | — |

## Sources

This draft consumes HUAKAI-owned docs and prior local specs only. It does not read reference-project source.

- `docs/03_FEATURE_PARITY_MATRIX.md` - existing F-BILL-002 row and Phase 6 roadmap placement.
- `docs/11_ACCEPTANCE_TEST_MATRIX.md` - acceptance-test matrix format.
- `docs/specs/observability-billing.md` - F-OBS-001 and F-BILL-001 Tx1/Tx2 settlement boundary.
- `docs/specs/user-authentication.md` - F-AUTH-007 platform User identity boundary.
- `docs/specs/session-management.md` - F-SESSION-001 platform session boundary.
- `docs/specs/credential-acquisition.md` - local advisory-lock pattern note and upstream credential boundary.
- `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md` - local billing/quota lock-order and top-up guidance.
- `docs/02_CAPABILITY_CONTRACT.md`, `docs/18_GLOSSARY.md`, and `docs/19_DOMAIN_MODEL.md` - local User, Quota, Billing Ledger, Usage Record, and Audit Event vocabulary.

## Capability

F-BILL-002 covers **voucher-based balance top-up** for HUAKAI Users. An Admin/Owner can create one voucher or a batch of vouchers. A logged-in User can redeem a valid voucher code to add credit to the User balance that later participates in quota checks and F-BILL-001 request settlement.

This is a neutral commercial foundation feature. It is not a payment provider, not an upstream Provider Account credential feature, not a user invite system, and not a request-routing feature.

## Boundary

F-BILL-002 owns:

- Voucher lifecycle: create, batch create, activate, expire, exhaust, revoke.
- Voucher code uniqueness and redemption eligibility checks.
- User redemption endpoint and idempotent redemption result.
- Voucher redemption audit events and billing event emission.
- Anti-fraud controls around redemption attempts.
- Positive balance/quota reflection after a successful redemption.

F-BILL-002 does not own:

- Payment provider integration. That remains F-PAY-001 or later payment plugin work.
- Per-request price calculation, request reservation, or request settlement. Those remain F-BILL-001/F-OBS-001.
- User registration invite logic. That remains F-AUTH-007.
- Platform session creation/refresh. That remains F-SESSION-001.
- Upstream Provider Account credential acquisition or refresh. Those remain F-CRED-001/F-AUTH-005.
- Backend code, schema migration, or runtime dependency changes in this docs-only wave.

## Actor

- **Admin/Owner** creates vouchers, creates batches, revokes vouchers, reviews redemption activity, and investigates bursts.
- **User** redeems a voucher code while authenticated to HUAKAI.
- **System** validates code, eligibility, status, concurrency, fraud limits, balance effects, billing event emission, and audit trail.
- **Support/Operator** may inspect audit and billing events but cannot retroactively mutate a redemption row.

## Preconditions

1. F-AUTH-007 has established a HUAKAI User identity, or an admin-controlled support flow explicitly acts on behalf of a User.
2. F-SESSION-001 has authenticated the request when the caller is a User.
3. Tenant context is resolved. Even Personal Edition MVP uses a default tenant; future schema must carry tenant scope.
4. A balance/quota target exists for the User, or the redemption transaction can create the initial balance row atomically.
5. Billing event storage exists for append-only commercial events.
6. F-TRUST audit chain is available or the implementation fails closed for admin create/revoke and redeem success paths.
7. Anti-fraud policy is configured with tenant/operator thresholds. The per-User+IP per-minute redemption attempt cap is represented as `N` until Owner sets defaults.

## Voucher Entity

Future schema names are implementation choices, but the core voucher entity must carry these logical fields:

| Field | Required | Purpose |
| --- | --- | --- |
| `code` | yes | Unique redemption code. Raw code is shown only at creation/export time; lookup should use a normalized code and stored hash/fingerprint if implementation chooses. |
| `value_cents` | yes | Positive integer top-up value in tenant currency minor units for admin/user surfaces. Ledger arithmetic still follows exact-money rules and must not use float. |
| `valid_from` | yes | Earliest redeem timestamp. |
| `valid_until` | yes | Expiry timestamp. Redemption after this time is rejected and may transition status to expired. |
| `max_redemptions` | yes | Total allowed successful redemptions for this voucher. `1` means single global redemption; higher values support campaign/batch use. |
| `single_use_per_user` | yes | When true, one User can redeem this voucher at most once even if `max_redemptions > 1`. |
| `status` | yes | Derived or stored lifecycle status: `active`, `expired`, `exhausted`. Admin revocation is an explicit non-active state in lifecycle handling and must be visible in audit/UI even if the persisted enum is later expanded. |

Implementation may add tenant id, actor id, batch id, eligibility policy, metadata, creation timestamps, revocation fields, redemption counters, and code hash fields during the implementation wave. This spec does not add schema.

## Lifecycle

### Status Semantics

- `active`: voucher is redeemable if time, redemption count, eligibility, anti-fraud, and idempotency checks pass.
- `expired`: current time is after `valid_until`, or an expiry worker/operator action has marked the voucher expired.
- `exhausted`: successful redemptions have reached `max_redemptions`.
- `revoked`: admin has disabled the voucher before natural expiry/exhaustion. Revoked vouchers reject future redemption and do not refund prior successful redemptions.

If Phase 6 implementation keeps `status` as only `active | expired | exhausted`, revoked state must still be represented through a separate revocation marker and rendered as non-redeemable. The user-facing and audit-facing behavior is binding: revoked means no future redemption and no automatic refund.

### Admin Create Single Voucher

1. Admin submits value, validity window, max redemptions, single-use-per-user flag, optional code or code-generation request, and optional eligibility policy.
2. System validates admin permission, tenant scope, positive value, sane validity window, max redemption count, and unique code.
3. System creates the voucher in active state if `valid_from <= now < valid_until`; otherwise it is scheduled to become active at `valid_from`.
4. System emits `voucher_created`.
5. If raw code is generated, it is shown/exported once according to admin UI policy; later audit/logs use fingerprint/redacted code only.

### Admin Batch Create

1. Admin submits a batch request: count, value, validity window, redemption policy, code-generation policy, and optional campaign metadata.
2. System validates the entire batch before creating any voucher when possible.
3. System creates one voucher record per code and one batch summary.
4. System emits one `voucher_created` audit event per voucher plus one batch summary audit event.
5. Partial batch creation is allowed only if the operator explicitly chooses a partial mode; otherwise batch is all-or-nothing.

### User Redeem

1. User submits a voucher code while authenticated to HUAKAI.
2. System normalizes the submitted code and applies per-User+IP attempt limit for the current minute. Exceeding `N` attempts rejects before balance mutation.
3. System starts the redemption transaction and acquires a transaction-scoped PostgreSQL advisory lock keyed to `(tenant_id, normalized_code_hash)` or an equivalent voucher identity. If `single_use_per_user` is true, the same transaction also protects the `(tenant_id, voucher_id, user_id)` redemption uniqueness check.
4. System rereads voucher state inside the transaction.
5. System rejects if voucher is not active, not yet valid, expired, exhausted, revoked, tenant-mismatched, or not eligible for this User.
6. System checks idempotency. A retry with the same User and redemption idempotency key returns the prior result without adding balance twice.
7. System writes a voucher redemption record, increments the successful redemption count, and transitions to exhausted if this redemption reaches `max_redemptions`.
8. System writes `billing_events { type: voucher_redeemed, value, voucher_id }` in the same transaction as the balance/quota mutation.
9. System increases User balance or balance-backed quota by the voucher value in the same transaction.
10. System emits `voucher_redeemed` into the F-TRUST audit chain.
11. Commit returns the new balance/quota view to the User.

## F-BILL-001 / Tx2 Relationship

Voucher redemption is a **top-up event**, not a per-request settlement. It creates positive value that request-time F-BILL-001/F-OBS-001 settlement later consumes.

Binding relationship:

1. Successful voucher redemption writes an append-only billing event with at least: event type `voucher_redeemed`, tenant id, User id, voucher id, redemption id, value, actor/source, idempotency key, and timestamp.
2. The billing event and balance/quota increment are atomic. A user must never receive balance without the billing event, and the billing event must never claim success without the balance effect.
3. F-BILL-001 request Tx1/Tx2 continues to own reserve and settle for model usage. Voucher credit is just one positive source in the User balance that Tx1 can reserve against.
4. Voucher redemption does not mutate existing Usage Records or request Billing Ledger entries.
5. Revocation after redemption does not reverse prior balance. If a future operator correction is needed, it must use the existing append-only adjustment/reversal pattern and explicit Owner-approved policy.

## Logical Storage Intent

No schema is implemented in this wave. Phase 6 implementation should introduce storage equivalent to:

| Logical table | Purpose |
| --- | --- |
| Voucher | Tenant-scoped voucher code fingerprint, value, validity, max redemptions, single-use flag, lifecycle state, admin creator, batch id, timestamps, revocation marker. |
| Voucher redemption | Tenant, voucher id, User id, redemption idempotency key, value, status, attempt context, redeemed timestamp, billing event id. |
| Billing event | Append-only commercial event, including `type = voucher_redeemed`, value, voucher id, User id, tenant id, and idempotency metadata. |
| Audit event | F-TRUST chain event for create, redeem, expire, revoke, and fraud alert. |
| Anti-fraud window | Short-lived rate window keyed by tenant, User, IP class/hash, and minute bucket. |

Voucher code lookup must be designed so raw codes do not appear in routine logs, traces, or audit payloads.

## Failure Path

### Failure: Duplicate Code

- Trigger: admin creates a code already present for the tenant.
- Observable outcome: create rejects before voucher exists; batch all-or-nothing rolls back unless partial mode is explicitly selected.
- Operator-visible signal: validation error and failed create audit without raw code.

### Failure: Expired Or Not-Yet-Valid Voucher

- Trigger: current time is outside `[valid_from, valid_until)`.
- Observable outcome: redemption rejects; no balance change; no billing event success row.
- Operator-visible signal: `voucher_expired` event when state transitions to expired; failed redeem audit reason class.

### Failure: Exhausted Voucher

- Trigger: redemption count already reached `max_redemptions`.
- Observable outcome: redemption rejects; no balance change.
- Operator-visible signal: exhausted status and failed redeem audit reason class.

### Failure: Wrong User Or Tenant

- Trigger: voucher eligibility policy targets another User, email hash, cohort, tenant, or campaign; or the redemption request carries mismatched tenant context.
- Observable outcome: redemption rejects with safe message; no balance change and no leakage about the intended recipient beyond policy-safe text.
- Operator-visible signal: failed redeem audit with reason `wrong_user` or `tenant_mismatch`.

### Failure: Redeem Race

- Trigger: two or more requests redeem the same voucher concurrently, or the same User races multiple requests.
- Observable outcome: advisory lock plus transaction reread allows at most the permitted redemption count and at most one redemption per User when `single_use_per_user` is true.
- Operator-visible signal: contention metric and idempotent duplicate audit if applicable.

### Failure: Revoked Voucher

- Trigger: admin revoked the voucher before redemption.
- Observable outcome: future redemption rejects. Prior successful redemptions remain credited; revoke does not refund.
- Operator-visible signal: `voucher_revoked` audit with actor, reason class, and affected unredeemed capacity.

### Failure: Anti-Fraud Limit

- Trigger: same User+IP exceeds `N` redemption attempts per minute, or burst pattern crosses tenant/operator alert thresholds.
- Observable outcome: further attempts are blocked before balance mutation.
- Operator-visible signal: alert with aggregate counts, User ids or hashes, IP class/hash, voucher batch id when known, and time window.

### Failure: Billing Event Or Balance Mutation Fails

- Trigger: storage error or transaction conflict prevents writing either event or balance effect.
- Observable outcome: transaction rolls back; User receives retryable error; no partial credit.
- Operator-visible signal: commercial integrity alert if repeated.

## Anti-Fraud Requirements

Minimum controls:

1. Per User + IP per minute redemption attempt limit: at most `N` attempts before temporary block.
2. Burst-pattern alert when many failed attempts hit one voucher, one batch, one User, one IP class/hash, or many Users from one IP class/hash.
3. Admin-visible investigation fields: voucher id/batch id, count of failed reason classes, source IP class/hash, User id or hash, first/last attempt timestamp.
4. Failed attempts must not consume voucher capacity.
5. Anti-fraud blocks must not reveal whether the voucher code is valid.
6. Admin override or unblock, if added later, must be audited and must not bypass voucher status/eligibility checks.

## Operator Recovery

| Failure | Recovery |
| --- | --- |
| Duplicate code | Regenerate code or import with conflict report; no voucher created for the duplicate. |
| Expired voucher | Admin may create a new voucher; extending an expired voucher is a policy decision and must emit audit. |
| Exhausted voucher | Admin may create a new voucher or new batch; capacity is not silently increased without audit. |
| Wrong user | Support verifies intended recipient and issues a replacement voucher if policy allows. |
| Race/duplicate redeem | User sees prior successful redemption or safe rejection; operator checks redemption row and billing event. |
| Revoked voucher complaint | Operator reviews `voucher_revoked`; prior credits are not automatically removed. |
| Fraud burst | Operator reviews alert, revokes affected voucher/batch if needed, tightens threshold, or blocks source via existing security controls. |
| Transaction/storage failure | Retry after storage recovery; verify no billing event exists without matching balance effect. |

## Audit / Usage / Log Evidence

F-BILL-002 emits these F-TRUST event types:

| Event | When | Payload allowlist |
| --- | --- | --- |
| `voucher_created` | Admin creates one voucher or each voucher in a batch. | tenant id, voucher id, batch id, value, validity window, max redemptions, single-use flag, actor id, request id. |
| `voucher_redeemed` | User successfully redeems a voucher. | tenant id, voucher id, redemption id, User id, value, new balance/quota summary, request id, source IP class/hash. |
| `voucher_expired` | Voucher transitions to expired by time check or expiry worker. | tenant id, voucher id, expired_at, unredeemed capacity, reason class. |
| `voucher_revoked` | Admin revokes voucher before future redemption. | tenant id, voucher id, actor id, reason class, unredeemed capacity, request id. |
| `voucher_redeem_failed` | Redemption attempt is rejected. | tenant id, voucher fingerprint, User id when authenticated, reason class, request id, source IP class/hash. |
| `voucher_redeem_burst_alert` | Anti-fraud burst threshold trips. | tenant id, window, aggregate counts, reason classes, voucher/batch ids when known, User/IP hashes. |

Audit/log payloads must not include raw voucher code after the initial admin creation/export reveal. The code may be represented by a fingerprint or redacted suffix only.

## Acceptance Test Direction

Detailed acceptance rows live in [11_ACCEPTANCE_TEST_MATRIX.md](../11_ACCEPTANCE_TEST_MATRIX.md): `AT-BILL-002-001..010`.

Minimum coverage:

- Admin single voucher create.
- User redeem happy path.
- Concurrent redemption race.
- Expired voucher reject.
- Wrong User or tenant reject.
- Revoked voucher reject with no refund.
- Audit and billing event chain.
- Anti-fraud per User+IP minute limit and burst alert.
- Idempotent redemption retry.
- Admin batch create with all-or-nothing and partial-mode behavior.

## Open Questions

1. What default value should tenant/operator use for per User+IP per-minute redeem attempts `N`?
2. Should voucher eligibility support only target User/email hash in Phase 6, or also User Group/campaign cohorts?
3. Should admin be allowed to extend an expired voucher, or must replacement voucher issuance be the only recovery path?
4. Which exact balance/quota row owns the positive credit in Phase 6 schema?
5. What is the user-visible wording for invalid code vs anti-fraud block so it is safe against code enumeration?

## Implementation Notes

This docs wave does not implement code, schema, migrations, dependencies, or runtime configuration. Phase 6 implementation needs Owner confirmation before touching billing ledger tables, quota enforcement, auth core, or production anti-fraud defaults.

Source files read: docs/RULES.md; .agents/skills/acceptance-test-writer/SKILL.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/11_ACCEPTANCE_TEST_MATRIX.md; docs/specs/observability-billing.md; docs/specs/user-authentication.md; docs/specs/session-management.md; docs/specs/credential-acquisition.md; docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md; docs/plans/2026-05-16-user-auth-session-spec-codex.md; docs/02_CAPABILITY_CONTRACT.md; docs/18_GLOSSARY.md; docs/19_DOMAIN_MODEL.md

Lane: implementer

Agent: Codex GPT-5

UTC: 2026-05-16T07:03:38Z
