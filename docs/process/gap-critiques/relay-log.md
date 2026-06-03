# Gap Critique: Per-request relay log (`relay-log.md`)

_Reviewer: adversarial principal review_
_Date: 2026-06-03_
_Design: `docs/process/gap-designs/relay-log.md`_

---

## Verdict

**needs-work**

The money-path and migration number are sound. The schema is mostly clean. However
there are six must-fix issues before implementation starts: a false source-of-truth
claim for `user_group`, silent omission of Abort and CommitCacheHit paths from the
log, a missing idempotency guard against DLQ-replayed duplicate rows, an absent FK
on `api_key_id`, a fabricated reference to a function that does not exist in the
codebase, and a discriminating-test defect that describes a guard the reference
pattern does not implement.

---

## Holes

### H-1 `user_group` is not in `SettleRequest` or `UsageRecordDraft` — false source claim

The design states: _"populated entirely from fields already present in
`billing.SettleRequest` and `gateway.UsageRecordDraft`"_. This is wrong.

Confirmed from code:

- `billing.SettleRequest` (`internal/billing/billing.go:84–115`): no `UserGroup` field.
- `gateway.UsageRecordDraft` (`internal/gateway/forwarder_types.go:83–120`): no `UserGroup` field.
- `auth.Identity` (`internal/auth/api_key_resolver.go:44–52`): **has** `UserGroup string`, populated from `users.user_group` at auth-resolution time, carried through `gatewayhttp` to pool selection only.

`UserGroup` exists on the identity resolved at Tx1 time, is threaded into the pool
`SelectionRequest` (`gatewayhttp/chat_completions_dispatch.go:360`), but is never
written into `SettleRequest`. The writer therefore cannot source it from the
objects it receives at Tx2.

**Required fix:** Either add `UserGroup string` to `SettleRequest` and thread it
from `gatewayhttp` through to `Settle`, or look it up at settle time via
`claim.UserID` join against `users`. The design must be explicit about which; the
"from fields already present" claim must be removed or corrected. Not doing so
means `user_group` will always be `""` in prod, making the group filter meaningless.

### H-2 Abort and CommitCacheHit paths produce no relay log row — silent gap

The design says the log is written _"inside Tx2"_ and only wires `Settle`. The
`DefaultSettler` has three Tx2 paths:

1. `Settle` — covered by the design
2. `Abort` (`settler.go:259–397`) — NOT covered; produces a `usage_records` row,
   but the design does not wire a relay log write here
3. `CommitCacheHit` (`settler.go:410–533`) — NOT covered; same omission

This means aborted requests and L2 cache hits will never appear in `GET /v1/me/logs`,
creating a systematically false picture of a caller's usage. In the reference
(one-api `model/log.go`), every request outcome — including quota-denied,
cancelled, and error — produces a log row. HUAKAI's parity claim fails for these
two paths.

The design must explicitly decide: write a log row on Abort/CommitCacheHit (with
`quota_denied=true` or appropriate `end_class`), or document the omission as an
intentional scope reduction. The current text neither wires them nor acknowledges
the gap.

### H-3 No idempotency guard — DLQ relay can produce duplicate relay log rows

The `DefaultSettler.insertUsageRecordOrDLQ` mechanism means `Settle` can be
replayed from the DLQ. If the billing transaction commits but the relay log write
fails the first time and is retried, the relay log INSERT is not idempotent:
there is no UNIQUE constraint on `claim_id` (one settle per claim), so each replay
produces an additional row.

The design's "immutable after insert" invariant is meaningless without a uniqueness
guard. The correct fix is `UNIQUE (claim_id)` on `relay_request_logs` (a claim
is settled exactly once). Note that `billing_ledger_claims` enforces single-settle
via its status machine; the relay log must mirror that.

**Required fix:** Add `UNIQUE (claim_id)` to `0077_relay_request_logs.up.sql` and
update the INSERT query to `ON CONFLICT (claim_id) DO NOTHING`.

### H-4 Missing FK on `api_key_id`

The design schema declares `api_key_id bigint NOT NULL` with no `REFERENCES`
clause. The equivalent columns in `billing_ledger_claims` and `usage_records` have
FK constraints added in migration 0009 (`fk_claims_api_key`, `fk_usage_api_key`,
both `REFERENCES api_keys(tenant_id, id)`). The relay log follows the same
multi-tenant row structure but opts out of the FK silently. If a relay-key is
hard-deleted (not currently possible, but the schema should be defensive), orphan
rows will accumulate invisibly.

**Required fix:** Add `FOREIGN KEY (tenant_id, api_key_id) REFERENCES api_keys
(tenant_id, id) ON DELETE RESTRICT NOT VALID` consistent with migration 0009
precedent, or explicitly document why it is omitted (e.g. soft-delete only).

### H-5 Fabricated reference to non-existent function

The "Fail-closed on ambiguity" invariants section states: _"same pattern as the
audit ledger DLQ path in `submitAuditLedgerEntry`"_. No function named
`submitAuditLedgerEntry` exists anywhere in `internal/billing` or any other
package in the codebase. This appears to be a hallucinated reference. Fabricated
code citations in a design document are a quality failure that undermines trust in
the rest of the document's accuracy claims. The reference must be corrected to the
actual pattern used (e.g. `insertUsageRecordOrDLQ` with SAVEPOINT + DLQ enqueue).

### H-6 `TestHandler_AuthRejectsUnknownBearer` describes a guard that does not match the reference pattern

The test description says: _"Without the `validBearerFormat` check the test passes
with 200, so the test fails if the guard is removed."_ No `validBearerFormat`
function or bearer-format pre-check exists in the actual auth pattern used by
`meusagehttp` (the reference the design claims to mirror). `meusagehttp` delegates
all bearer validation to `AuthResolver.Resolve`; there is no separate format gate.

If `relayloghttp` adds a new `validBearerFormat` guard not present in the reference
pattern, that is a new contract the design must explicitly introduce. If it does not
add such a guard, the test as described is not discriminating — the AuthResolver
will already return a 401, but `removing the format check` would have no effect.

**Required fix:** Either (a) explicitly add a `validBearerFormat` pre-check and
justify the deviation from the reference pattern, or (b) rewrite the test to be
discriminating against the actual auth guard: test that a request with `APIKeyID ==
0` from a misconfigured resolver returns 503, not 401 (matching the R3 guard
described in the design's own risk section).

---

## Money / Schema / Auth / CMB risks

### MS-1 Migration number is correct

Current maximum migration is `0076_user_role` (confirmed in
`sql/migrations/`). The proposed `0077_relay_request_logs` is the correct next
number. No collision risk.

### MS-2 Down migration is unsafe without CASCADE check

`0077_relay_request_logs.down.sql` issues `DROP TABLE IF EXISTS relay_request_logs`.
The migration adds `claim_id bigint NOT NULL REFERENCES billing_ledger_claims(id)`.
PostgreSQL will refuse a plain `DROP TABLE` if the FK is declared the other way
(child referencing parent), but the issue is: if other tables later reference
`relay_request_logs.id` (the design leaves room for future phases), the down will
fail. More immediately, the FK from `relay_request_logs(claim_id)` to
`billing_ledger_claims(id)` means you cannot DROP `relay_request_logs` while
`billing_ledger_claims` exists **unless** you drop the constraint first.
PostgreSQL drops the FK automatically when the child table is dropped, so `DROP
TABLE relay_request_logs` succeeds here. This is safe as written. However, the
design's silence on this must not be read as reviewed — any reviewer should confirm
this before applying.

### MS-3 No money fields in relay log — correct

`relay_request_logs` contains no `actual_cost`, `predicted_cost`, or any decimal
money field. Token counts are mirrored only. Billing remains the exclusive domain
of `usage_records`. This is correct and the CMB invariant is honored at the schema
level.

### MS-4 Tx2 atomicity claim is accurate but subtly misleading

The design states: _"The relay log write happens **after** the Tx2 commit of
`usage_records` + `billing_events`. It is NOT inside the Tx2 serializable
transaction."_ This is the correct, safe design: billing commits first, log is
best-effort. However, the phrase "fail-closed on ambiguity" in the invariants
section is used for the relay log error path, but fail-closed is a security term;
the actual behaviour is fail-open for observability (log failure does not abort
billing). The terminology is backwards and could mislead an implementor. Rename to
"fail-open for observability" to be precise.

### MS-5 Auth / CMB invariants

- CMB-1, CMB-5 (no credentials, no raw payloads): honored at schema and type level.
  `LogRecord` has no `[]byte` payload fields. **Verified** against `auth.Identity`
  structure — `api_key_id` is an internal surrogate, never the bearer string.
- CMB-2 (router reads no credentials, writes nothing): the log write is in
  `billing.DefaultSettler`, not the router layer. **Correct**.
- Tenant isolation: the `ListRelayLogs` filter on `(tenant_id, api_key_id)` from
  the resolved `auth.Identity` is the right pattern, matching `meusagehttp`.
  **R3 guard** (reject if `APIKeyID <= 0`) is correctly identified; this must be
  implemented. Not doing so is a fail-OPEN bug: a zero `api_key_id` would return
  all rows for the tenant.

---

## Parity gaps

### PG-1 Aborted and quota-denied requests invisible

As noted in H-2, the design does not write a relay log row for Abort or
CommitCacheHit paths. The one-api reference writes a log row for every request
outcome including quota-denied (the `quota_denied` field is only set on the
`Settle` path, not Abort). This is an unacknowledged parity gap.

### PG-2 No `type` / `endpoint_family` discriminator for quota-denied pre-acquire aborts

In `DefaultSettler.Abort`, when `providerAccountID == nil` (pre-acquire abort),
no `usage_records` row is written. By extension, no relay log row would be written
(H-2). The reference one-api logs quota-denied rows. HUAKAI's design omits this
class of request entirely from the log. The design must either explicitly exclude
pre-acquire quota-denied requests or handle them.

### PG-3 Portkey body-logging omission is correctly documented

The intentional omission of raw request/response bodies (CMB-5) vs Portkey's
approach is correctly called out. Not a gap — it is a deliberate parity choice.

---

## Maintainability (god-file check)

All files as budgeted are under 500 lines. The package split is appropriate:
`internal/relaylog` (domain + writer), `internal/relayloghttp` (handler + cursor),
`internal/db/relaylog` (generated). No god-file violations. The single modification
to `internal/billing` (adding one optional field + one nil-guarded call to
`DefaultSettler`) is minimal and reversible.

One concern: `handler.go` is budgeted at ~260 lines. The handler parses 6 query
parameters, calls the reader, and maps rows. If the `user_group` sourcing fix
(H-1) requires a join at query time, the SQL complexity may bleed into the handler.
Keep the handler as a thin HTTP adapter; push complexity into `relaylog.Writer` or
the SQL query.

---

## Must-fix before implementation (numbered list)

1. **H-1 — Fix `user_group` sourcing.** Add `UserGroup string` to
   `billing.SettleRequest`, thread it from `gatewayhttp` (where `ident.UserGroup`
   is available), and update `Settle` to pass it to the `LogRecord`. Remove the
   claim that all log fields come from fields already present in `SettleRequest`
   and `UsageRecordDraft`.

2. **H-2 — Wire relay log on Abort and CommitCacheHit.** Both Tx2 paths must
   either write a relay log row (after commit, best-effort, same nil-guard pattern)
   or the design must explicitly document the scope limitation and update the
   parity table to reflect that aborted and L2-cache-hit requests are excluded from
   `GET /v1/me/logs`.

3. **H-3 — Add `UNIQUE (claim_id)` to the migration and `ON CONFLICT DO NOTHING`
   to the INSERT query.** Without this, DLQ replay of the billing settler produces
   duplicate relay log rows, violating the "immutable after insert / write-once"
   invariant the design asserts.

4. **H-4 — Add FK on `(tenant_id, api_key_id) REFERENCES api_keys(tenant_id, id)`
   consistent with migration 0009 precedent**, or justify the omission explicitly
   in the migration comment.

5. **H-5 — Remove the fabricated `submitAuditLedgerEntry` reference.** Replace
   with the actual pattern used in the codebase: `insertUsageRecordOrDLQ` with
   SAVEPOINT + DLQ enqueue. Audit all other cited function names and file paths
   in the design for accuracy before implementation.

6. **H-6 — Rewrite `TestHandler_AuthRejectsUnknownBearer` to be discriminating
   against the actual auth guard.** Either introduce `validBearerFormat` as an
   explicit contract (and justify it), or replace the test with one that guards
   the R3 invariant: `APIKeyID <= 0` from resolver → HTTP 503. The current test
   description is non-discriminating against the reference pattern.

---

_End of critique._
