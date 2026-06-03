# Gap Critique: Notification System

**Reviewed:** 2026-06-03
**Reviewer:** Adversarial PM review (automated)
**Design file:** `docs/process/gap-designs/notifications.md`
**Verdict:** NEEDS-WORK (3 must-fix blockers, several high-severity holes)

---

## Verdict

**needs-work** — The overall package decomposition is sound and migration numbers are correct. However three blockers must be resolved before implementation begins: (1) the cipher AAD cannot encode `user_id` with the existing `credentialstore.AAD` struct, breaking the claimed cross-user isolation guarantee; (2) the admin broadcast fan-out is synchronous inside a live HTTP request with no timeout guard beyond the server-level deadline, making it a trivial DoS vector for any tenant with more than a few thousand users; (3) the design claims `SendTenantMessage` is the delivery entry point for alert-email, but that method has zero rate-limiting — the claimed 60-second bind cooldown lives in a DB table that is only checked separately, not wired into the send path, leaving a race window open.

---

## Holes

### H-1 (BLOCKER) — `credentialstore.AAD` has no `user_id` field; claimed cross-user isolation is impossible with the existing type

The design states (Invariants, Schema section):

> `cipher.go` binds AAD to `(tenant_id, user_id, "notif_webhook_secret")` so cross-user or cross-tenant decryption returns `ErrDecryptFailed`. Test `TestCipherAADMismatch_RejectsDecrypt` defends this.

The real `credentialstore.AAD` struct (`internal/credentialstore/crypto.go`) has these fields only:

```
TenantID          int64
ProviderAccountID int64
Vendor            string
AuthMode          string
Version           int32
KeyID             string
```

There is no `user_id` field. The serialised AAD bytes are:
`tenant=…;provider_account=…;vendor=…;auth_mode=…;version=…;key_id=…`

To bind per-user, the design must either (a) repurpose `ProviderAccountID` as `user_id` — which is undocumented, semantically wrong, and will confuse future readers — or (b) introduce a new AAD type specific to notifications. The design does neither. The test `TestCipherAADMismatch_RejectsDecrypt` as described would either not compile or would silently test the wrong isolation boundary. **This must be resolved before `cipher.go` is written.**

### H-2 (BLOCKER) — Admin broadcast is synchronous in-request fan-out with no bounded timeout; DoS / connection exhaustion

The design (admin broadcast section, Risks table) acknowledges "admin broadcast fan-out latency" but the mitigation is "HTTP timeout is the guard." There is no specification of what that timeout is, where it is set, or how partial deliveries are surfaced to the caller. A tenant with 50 000 users and all three channels active will fan out up to 150 000 outbound HTTP/SMTP calls inside a single API request. Even at 100 ms per delivery this is hours. The cursor-based pagination (1 000 per page) does not help because all pages are processed synchronously before the handler returns. The design says "consider moving to an outbox/DLQ model in a future slice" — but defers this entirely. A synchronous fan-out of unbounded depth in a single HTTP handler is a production availability bug, not a known risk to defer.

**Minimum acceptable fix:** enforce a hard per-request delivery budget (e.g. max 200 users or 30 s wall-clock), return 202 Accepted with a cursor token the caller can continue with, or dispatch into the existing `dlq` outbox. The design must choose and specify one approach.

### H-3 (BLOCKER) — Rate-limit race between `notification_rate_ledger` check and `email.AuthSender.SendTenantMessage` delivery

The design routes alert-email delivery through `email.AuthSender.SendTenantMessage`. That method (`sender_factory.go:137`) has **no cooldown** — cooldowns in `AuthSender` are only applied in `SendVerification` and `SendPasswordReset` via `reserveCooldown`. `SendTenantMessage` calls `sendForTenant` directly. The design's 60-second bind rate-limit is enforced via `notification_rate_ledger` with a separate DB check — but that check and the subsequent `SendTenantMessage` call are two separate operations with no atomic lock. Between the "rate-limit check passes" and the "email is sent" steps, a second concurrent request can pass the same check and send a duplicate email. The design offers no transaction boundary, advisory lock, or optimistic-concurrency mechanism to close this race.

### H-4 (HIGH) — Fail-open on `notifpref` DB error is asserted but not specified atomically per channel

The design claims (CMB invariants): "if `notifpref` cannot be read (DB error), the delivery is skipped." However the dispatcher fans out to multiple independent adapters. If prefs are read successfully but a **channel-level** DB error occurs mid-fan-out (e.g. decrypting the webhook secret fails after the DB goes away), the design specifies `TestWebhookAdapter_SecretDecryptFailure_Skips` — meaning the dispatcher silently skips that channel and continues. That is correct. But there is no test defending the case where the **pref read itself** fails after one adapter has already fired: is the already-sent email a problem? The design should explicitly state that the pref read is always the first step, performed atomically before any adapter is invoked, and add a test `TestDispatcher_PrefReadError_NoAdapterCalled`.

### H-5 (HIGH) — `endpoint` column stores webhook URL in plaintext; SSRF validation done at insert time AND delivery time, but no test for the delivery-time re-validation path

The design says SSRF validation runs in `store_postgres.go` at insert time and "again at delivery time" in `webhook_adapter.go`. There is no discriminating test for the delivery-time path. An attacker who smuggles an internal IP via DNS rebinding (the URL passes insert-time validation because it resolves to a public IP at insert time, then resolves to `169.254.169.254` at delivery time) would only be caught by the delivery-time check. No test defends that path. Add `TestWebhookAdapter_SSRFRebind_Blocked`.

### H-6 (MEDIUM) — `wxpush_adapter.go` WxPush token stored in plaintext; design justifies this but provides no threat model

The design stores WxPush tokens as plaintext in `notification_channels.endpoint`. The justification is: "WxPush tokens are not credentials in the cryptographic sense." WxPush tokens are bearer tokens — anyone with DB read access can impersonate the user to WxPush. Given that the HUAKAI codebase encrypts SMTP passwords and upstream API keys, storing WxPush tokens in plaintext is an inconsistency that violates least-privilege. The design should either encrypt them (same AES-GCM envelope, with a separate AAD) or explicitly document the accepted residual risk with a reference to a security decision record.

### H-7 (MEDIUM) — Balance-low worker reads `payment_credits` derived balance with no isolation level specification

`balancelowworker` performs a SUM over `payment_credits` to derive the user balance. The design does not specify the transaction isolation level for this read. Under `READ COMMITTED` the SUM is subject to phantom reads during concurrent `CompleteFulfill` writes (which run under `SERIALIZABLE`). A user's balance might appear briefly negative during a credit write, firing a false-positive balance-low alert. The design should specify `READ COMMITTED` is acceptable (alerts are advisory) or use `REPEATABLE READ` and say so.

### H-8 (MEDIUM) — `alert_email_bindings` has no index on `(tenant_id, user_id, status)` for the verified-binding lookup

The delivery path for alert-email must look up the verified binding for a user (`WHERE status = 'verified'`). The schema defines only a unique constraint on `(tenant_id, user_id)`. The unique constraint creates an implicit B-tree index, so the lookup is indexed — but only if Postgres uses the covering index. For completeness and to make the access pattern explicit, the design should add a partial index `WHERE status = 'verified'` or note that the unique constraint is sufficient with rationale.

### H-9 (LOW) — Plaintext token returned in bind response with no explicit TLS-only note

The design says "The plaintext token is returned **once** in the bind response and also emailed." There is no statement that the bind endpoint must be served over TLS. All other HUAKAI HTTP endpoints are assumed TLS, but for a feature that handles a one-time secret token, the design should explicitly state the transport requirement.

---

## Money/Schema/Auth/CMB risks

### MS-1 — Migration numbers 0077 and 0078 are correct

Confirmed: the highest existing migration file in `backend/sql/migrations/` is `0076_user_role.up.sql`. Migrations 0077 and 0078 are safe and non-colliding. No risk here.

### MS-2 — Down-migration not specified

Neither migration 0077 nor 0078 includes a `DOWN` specification. Every existing migration in the codebase has both `.up.sql` and `.down.sql` files. The design must supply matching `.down.sql` files that DROP the tables/indexes in dependency order: `notification_rate_ledger`, `notification_prefs`, `alert_email_bindings`, `notification_channels` (0077 down) and `idx_notification_prefs_balance_low_active` (0078 down). Missing down migrations break rollback.

### MS-3 — `balance_low_threshold` stored as `numeric(20,8)` and read via `decimal.RequireFromString` — correct

This is sound. The design correctly avoids float64 in the threshold comparison path. `decimal.Decimal.LessThan` is the right comparator. No issue.

### MS-4 — No Tx1/Tx2 or money-table writes — confirmed correct

The worker reads `payment_credits` read-only. No writes to `billing_events`, `billing_ledger_claims`, `usage_records`, or `payment_credits`. This is sound.

### MS-5 — `admin_audit_events` write before fan-out is correct but partial-failure semantics are unspecified

The design says the audit row is written before fan-out begins. That is correct: even a fully failed delivery is auditable. However the design does not specify what the HTTP response looks like when the audit row succeeds but all deliveries fail (is that a 200, 207, or 500?). This needs to be specified to prevent silent failures being reported as successes.

### MS-6 — AUTH: `tenant_operator` scope check for broadcast is described but not wired to `AdminIdentity.CanIssueForTenant`

The design references the `adminBalanceCreditAuth`-style interface for admin broadcast. The real enforcement mechanism for tenant operator scope is `AdminIdentity.CanIssueForTenant` (`admin/operator_auth.go`). The design does not specify whether `admin_handler.go` calls this method or reimplements the check. If it reimplements it, any fix to `CanIssueForTenant` will not propagate. The implementation spec should explicitly call `identity.CanIssueForTenant(tenantID)` and return `ErrAdminForbidden` → 403.

### MS-7 — CMB: no new package imports `internal/gateway` or `internal/gatewayhttp` — asserted and plausible

The design is explicit that the seven new packages do not import the frozen gateway packages. This is consistent with the package layout (no dependency arrows cross that boundary). No issue, but must be enforced at review gate with a `go list -deps` check.

### MS-8 — CMB: no credential or raw payload logging — partially asserted

The design asserts webhook secrets are never logged. However `wxpush_adapter.go` will make outbound HTTP calls to the WxPush API with the user's UID token in the request body or URL. The design does not specify whether the WxPush HTTP request/response is logged (e.g. via structured logger or error wrapping). If a WxPush call fails and the error message includes the URL (which may embed the UID token as a query parameter), that token will appear in error logs. The adapter must sanitise error messages before propagating them.

---

## Parity gaps

### PG-1 — No channel health / last-delivery-status surface

Both one-api and new-api expose a per-channel health status (last attempted at, last succeeded at, consecutive failure count) in the user settings UI. The design provides no `last_delivery_at`, `last_error`, or `consecutive_failures` column in `notification_channels`. Users have no way to diagnose a misconfigured webhook without external tooling. This is a parity gap vs the reference behavioral anchor cited in the design ("channel health as first-class product features"). Either add a delivery-status column or explicitly downgrade parity claim to "below parity / deferred."

### PG-2 — No per-event-kind opt-out granularity

The design provides per-channel enable/disable (email/webhook/wxpush) but no per-event-kind filter (e.g. "balance-low: yes; admin broadcast: no"). Reference platforms uniformly expose per-event subscription. This may be intentional scope reduction but is not acknowledged as a parity gap in the design's parity table.

### PG-3 — No unsubscribe / one-click opt-out link in alert emails

Transactional alert emails sent from commercial platforms must include an unsubscribe mechanism (CAN-SPAM, GDPR Art. 21). The design's `email_adapter.go` sends arbitrary `Message` bodies constructed by the dispatcher. There is no mention of an unsubscribe link, token, or landing page. This is both a parity gap and a legal compliance gap for tenants in applicable jurisdictions.

---

## Maintainability (god-file check)

All 32 files are budgeted under 500 lines; no single file is at risk. The seven-package split is appropriate. No god-file concern.

One minor concern: `notifhttp/user_handler.go` at ~320 lines handles both channel CRUD (webhook/wxpush upsert/delete) and preference CRUD. These are related but distinct responsibilities. At the projected line count this is acceptable, but if either surface grows the file should be split. Flag for review at implementation.

---

## Must-fix before implementation

1. **[BLOCKER — H-1] Define a notification-specific AAD type or document the `ProviderAccountID`-as-`user_id` repurposing.** The existing `credentialstore.AAD` cannot represent `user_id` as a distinct field. Either introduce `notifchannel.WebhookSecretAAD` (a local struct that serialises `tenant_id + user_id + "notif_webhook_secret"` and is passed as raw `[]byte` to `credentialstore.Cipher.Encrypt/Decrypt`), or explicitly state that `ProviderAccountID` encodes `user_id` and add a compile-time comment. Update `TestCipherAADMismatch_RejectsDecrypt` to actually test cross-user decryption failure, not just cross-tenant.

2. **[BLOCKER — H-2] Bound the admin broadcast fan-out.** Choose one: (a) hard cap at N users per synchronous request and return a cursor for continuation, (b) return 202 Accepted and enqueue work into the existing `dlq` outbox infrastructure, or (c) enforce a wall-clock deadline (e.g. 25 s) and return a partial-success response with count delivered / count remaining. Document the choice in the design. Add a discriminating test `TestAdminBroadcast_ExceedsUserCap_Returns202WithCursor` (or equivalent).

3. **[BLOCKER — H-3] Close the rate-limit race on alert-email bind.** Either (a) execute the `notification_rate_ledger` upsert and the `SendTenantMessage` call inside a single database transaction with `SELECT ... FOR UPDATE` on the rate-ledger row, or (b) use `ON CONFLICT DO UPDATE SET last_sent_at = CASE WHEN now() - last_sent_at > $cooldown THEN now() ELSE last_sent_at END RETURNING (changed)` as a single atomic upsert that acts as both the check and the record. The design must specify which approach and add a test `TestBind_ConcurrentRequests_OnlyOneEmailSent`.

4. **[HIGH — MS-2] Supply `.down.sql` files for migrations 0077 and 0078.** All existing migrations have paired down files. Drop tables and indexes in reverse dependency order.

5. **[HIGH — MS-5 / MS-6] Specify broadcast HTTP response codes for partial failure and wire `CanIssueForTenant` explicitly.** (a) Define what 207 / 500 / 200 the broadcast handler returns when audit succeeds but deliveries fail. (b) State that `admin_handler.go` calls `identity.CanIssueForTenant(tenantID)` — not a reimplemented check — and returns `ErrAdminForbidden` → 403.

6. **[HIGH — H-5] Add `TestWebhookAdapter_SSRFRebind_Blocked`.** The delivery-time SSRF check must have a discriminating test where the URL passes insert-time validation but fails delivery-time IP resolution (simulated via a stub DNS resolver that returns a private IP on the second call).

7. **[HIGH — MS-8] Sanitise WxPush error messages before propagation.** Add an explicit note in `wxpush_adapter.go` spec that HTTP error wrapping must redact the UID token from any error string before returning from the adapter. Add test `TestWxPushAdapter_ErrorDoesNotLeakToken`.

8. **[MEDIUM — PG-1] Acknowledge channel health parity gap explicitly.** Either add `last_delivery_at` and `last_error text` columns to `notification_channels` (and a migration line in 0077), or downgrade the parity claim in the parity table from "parity" to "below parity / deferred to next slice" with a rationale.

9. **[MEDIUM — PG-3] Specify unsubscribe mechanism for alert emails.** Add at minimum a one-click unsubscribe URL (pointing to the `DELETE /v1/users/me/alert-email` endpoint via a signed token) in the alert email body, or document the legal-compliance decision not to include one.

10. **[LOW — H-6] Encrypt WxPush tokens or document the accepted risk.** Given that every other bearer-class secret in HUAKAI is encrypted at rest, plaintext WxPush tokens in `notification_channels.endpoint` are an inconsistency. Either apply the same AES-GCM envelope (with `vendor="wxpush"` AAD) or write a security decision note in the design explaining why the risk is accepted.
