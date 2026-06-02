# Gap Design: Notification System

**Status:** Draft — 2026-06-03
**Author:** Senior HUAKAI Backend Architect (AI)
**Gap ID:** notifications

---

## Summary

HUAKAI currently sends transactional auth emails (verification, password reset)
via `internal/email` and receives inbound payment callbacks via
`internal/paymenthttp`. What is absent:

1. **Alert-email binding** — a per-user secondary email address (distinct from
   the login email) dedicated to operational alerts, with an email-token
   verification flow before the binding activates.
2. **Outbound webhook channel** — per-user HTTPS endpoint + secret where HUAKAI
   POSTs structured notification events (balance-low, admin broadcast, etc.).
3. **WxPush channel** — per-user WxPush token where HUAKAI delivers push
   notifications via the WxPush HTTP API.
4. **Per-user notification prefs** — per-channel enable/disable flags, balance
   threshold for balance-low push, and a rate-limit ledger that prevents
   duplicate alerts within a configurable cooldown window.
5. **Balance-low push worker** — a background ticker that reads `payment_credits`
   derived balance, compares it against each user's threshold, and fans out
   notifications via all configured channels.
6. **Admin broadcast** — an admin endpoint that sends a free-text notification
   to all users (or a filtered set) of a tenant via their active channels.

All six are purely additive. They do not modify any money table, do not alter
Tx1/Tx2 reserve-settle semantics, and do not touch the frozen packages
`internal/gatewayhttp`, `internal/gateway`, or `internal/proto`.

Reference behavioral anchor: one-api and new-api both surface user-facing
alerting and channel health as first-class product features rather than
afterthoughts. The clean-room decomposition below is HUAKAI-native and does not
replicate reference source structure.

---

## Package layout

All new code lives in **new packages only**. Each file is kept under 500 lines;
each exported function under 80 lines.

```
internal/notifchannel/              ← channel types, store interface, secret cipher
    doc.go                          package doc + CMB note                  ~20 lines
    types.go                        Channel, ChannelKind, Prefs, errors     ~120 lines
    store.go                        ChannelStore interface                   ~60 lines
    store_postgres.go               PostgreSQL implementation                ~350 lines
    cipher.go                       AES-GCM envelope for webhook secrets     ~110 lines
    cipher_test.go                  round-trip encrypt/decrypt tests          ~80 lines

internal/notifpref/                 ← per-user pref CRUD + rate-limit ledger
    doc.go                          package doc                              ~15 lines
    types.go                        PrefRow, UpdateInput, RateLimitKey        ~80 lines
    store.go                        PrefStore interface                       ~50 lines
    store_postgres.go               PostgreSQL implementation                ~300 lines
    ratelimit.go                    DB-persisted cooldown check + record      ~120 lines
    ratelimit_test.go               discriminating unit tests                ~100 lines

internal/notifdelivery/             ← outbound delivery: email / webhook / wxpush
    doc.go                          package doc                              ~15 lines
    types.go                        Event, DeliveryResult, channel adapters   ~80 lines
    email_adapter.go                wraps email.AuthSender.SendTenantMessage  ~90 lines
    webhook_adapter.go              HTTPS POST with HMAC-SHA256 signature    ~180 lines
    wxpush_adapter.go               WxPush HTTP API call                     ~120 lines
    dispatcher.go                   fan-out to all active channels           ~150 lines
    dispatcher_test.go              stub-adapter discriminating tests        ~180 lines

internal/alertemail/                ← alert-email bind / verify / get / delete
    doc.go                          package doc                              ~15 lines
    types.go                        Binding, VerifyInput, errors              ~70 lines
    service.go                      bind, verify-token, get, delete          ~200 lines
    service_test.go                 unit tests with stub store               ~160 lines
    store.go                        AlertEmailStore interface                 ~40 lines
    store_postgres.go               PostgreSQL implementation                ~200 lines

internal/alertemailhttp/            ← HTTP layer for alert-email endpoints
    doc.go                          package doc                              ~10 lines
    routes.go                       MountRoutes(r, Deps)                      ~40 lines
    handler.go                      bind, verify, get, delete handlers       ~300 lines
    handler_test.go                 discriminating HTTP-layer tests          ~250 lines

internal/notifhttp/                 ← HTTP layer for channel prefs + admin broadcast
    doc.go                          package doc                              ~10 lines
    routes.go                       MountUserRoutes + MountAdminRoutes        ~50 lines
    user_handler.go                 prefs CRUD + channel upsert/delete       ~320 lines
    admin_handler.go                admin broadcast handler                  ~180 lines
    handler_test.go                 discriminating tests                     ~280 lines

internal/balancelowworker/          ← background worker: balance-low push
    doc.go                          package doc                              ~15 lines
    types.go                        WorkerConfig, ScanCursor                  ~60 lines
    worker.go                       tick loop, cursor-based page scan        ~300 lines
    worker_test.go                  stub-store discriminating tests          ~200 lines
```

**File count:** 32 files across 7 packages. Every file is comfortably under 500
lines; no god-file risk.

---

## Schema / migrations

### Migration 0077 — notification channel bindings + prefs

```sql
-- internal/notifchannel: per-user outbound channel registrations.
-- internal/notifpref:    per-user notification preferences + rate-limit ledger.
-- No money tables touched. Tenant-scoped throughout.

BEGIN;

-- -----------------------------------------------------------------------
-- notification_channels: one row per active outbound channel per user.
-- kind IN ('webhook', 'wxpush') — email alert uses alert_email_bindings.
-- -----------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS notification_channels (
    id            bigserial   PRIMARY KEY,
    tenant_id     bigint      NOT NULL REFERENCES tenants(id),
    user_id       bigint      NOT NULL REFERENCES users(id),
    kind          text        NOT NULL CHECK (kind IN ('webhook', 'wxpush')),
    -- For webhook: HTTPS endpoint URL (plaintext, validated on insert).
    -- For wxpush:  WxPush UID token (plaintext).
    endpoint      text        NOT NULL,
    -- webhook_secret_envelope: AES-GCM envelope (same scheme as email
    -- smtp_password). NULL for wxpush (no secret needed).
    secret_envelope text,
    enabled       boolean     NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_notification_channel_user_kind
        UNIQUE (tenant_id, user_id, kind)
);

CREATE INDEX IF NOT EXISTS idx_notification_channels_user
    ON notification_channels (tenant_id, user_id);

-- -----------------------------------------------------------------------
-- alert_email_bindings: per-user secondary alert email (distinct from
-- login email). Pending until token is verified.
-- -----------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS alert_email_bindings (
    id              bigserial   PRIMARY KEY,
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    user_id         bigint      NOT NULL REFERENCES users(id),
    alert_email     text        NOT NULL,
    status          text        NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'verified')),
    -- HMAC-SHA256 of the verification token (never store raw token).
    token_hash      bytea,
    token_expires_at timestamptz,
    verified_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_alert_email_binding_user
        UNIQUE (tenant_id, user_id)
);

-- -----------------------------------------------------------------------
-- notification_prefs: per-user opt-in flags + balance-low threshold.
-- -----------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS notification_prefs (
    id                      bigserial   PRIMARY KEY,
    tenant_id               bigint      NOT NULL REFERENCES tenants(id),
    user_id                 bigint      NOT NULL REFERENCES users(id),
    -- channel enable flags
    alert_email_enabled     boolean     NOT NULL DEFAULT true,
    webhook_enabled         boolean     NOT NULL DEFAULT true,
    wxpush_enabled          boolean     NOT NULL DEFAULT true,
    -- balance-low threshold in USD (shopspring decimal, stored as
    -- numeric(20,8)). NULL = no balance-low alert configured.
    balance_low_threshold   numeric(20,8) CHECK (
        balance_low_threshold IS NULL OR balance_low_threshold >= 0),
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_notification_prefs_user
        UNIQUE (tenant_id, user_id)
);

-- -----------------------------------------------------------------------
-- notification_rate_ledger: DB-persisted cooldown records.
-- One row per (user, event_kind) = last time that event kind was sent.
-- Prevents duplicate alerts within cooldown window even after restarts.
-- -----------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS notification_rate_ledger (
    id          bigserial   PRIMARY KEY,
    tenant_id   bigint      NOT NULL REFERENCES tenants(id),
    user_id     bigint      NOT NULL REFERENCES users(id),
    event_kind  text        NOT NULL,  -- e.g. 'balance_low', 'broadcast'
    last_sent_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_notification_rate_ledger_user_event
        UNIQUE (tenant_id, user_id, event_kind)
);

CREATE INDEX IF NOT EXISTS idx_notification_rate_ledger_user
    ON notification_rate_ledger (tenant_id, user_id);

COMMIT;
```

**Migration number: 0077**

### Migration 0078 — balance-low scan index

```sql
-- Optimises the balance-low worker cursor scan across payment_credits.
-- The worker queries: net balance per user ordered by user_id for cursor paging.
-- payment_credits already has (tenant_id, user_id) from prior migrations;
-- this adds a partial index on users that have a notification_prefs row with
-- a non-NULL balance_low_threshold to narrow the scan set.

BEGIN;

CREATE INDEX IF NOT EXISTS idx_notification_prefs_balance_low_active
    ON notification_prefs (tenant_id, user_id)
    WHERE balance_low_threshold IS NOT NULL;

COMMIT;
```

**Migration number: 0078**

---

## Endpoints

All session-authenticated endpoints read `SessionIdentity` from context via
`auth.SessionFromContext` — exactly as `userkeyhttp` and `paymenthttp` do.
All admin endpoints resolve `admin.AdminIdentity` via an injected
`adminBalanceCreditAuth`-style interface — exactly as `adminhttp` does.
Credentials and raw webhook secrets are **never logged**.

### Alert-email (package `alertemailhttp`)

| Method | Path | Auth scope | Description |
|--------|------|-----------|-------------|
| `POST` | `/v1/users/me/alert-email` | session (user) | Bind + send verification token to new alert email |
| `POST` | `/v1/users/me/alert-email/verify` | session (user) | Submit token to activate binding |
| `GET` | `/v1/users/me/alert-email` | session (user) | Get current binding (status, masked email) |
| `DELETE` | `/v1/users/me/alert-email` | session (user) | Remove binding |

Body for `POST /v1/users/me/alert-email`: `{"alert_email": "..."}`.
Response for `GET`: `{"alert_email": "u***@example.com", "status": "verified", "verified_at": "..."}`.
The plaintext token is returned **once** in the bind response and also emailed.
Token is stored only as HMAC-SHA256 hash. Bind rate-limited to 1 request per
minute per user (checked in service layer against `notification_rate_ledger`
with `event_kind = "alert_email_verify_send"`).

### Notification channel prefs (package `notifhttp`)

| Method | Path | Auth scope | Description |
|--------|------|-----------|-------------|
| `PUT` | `/v1/users/me/notification-channels/{kind}` | session (user) | Upsert webhook or wxpush channel |
| `DELETE` | `/v1/users/me/notification-channels/{kind}` | session (user) | Remove channel |
| `GET` | `/v1/users/me/notification-prefs` | session (user) | Read all prefs |
| `PUT` | `/v1/users/me/notification-prefs` | session (user) | Update channel enable flags + threshold |

`{kind}` is `webhook` or `wxpush`.

Body for `PUT /v1/users/me/notification-channels/webhook`:
```json
{"endpoint": "https://...", "secret": "..."}
```
The `secret` is encrypted using AES-GCM (same envelope scheme as
`internal/email` SMTP password) before storage. The plaintext secret is never
logged and never returned after the initial `PUT` response.

Body for `PUT /v1/users/me/notification-prefs`:
```json
{
  "alert_email_enabled": true,
  "webhook_enabled": true,
  "wxpush_enabled": false,
  "balance_low_threshold": "10.00"
}
```

### Admin broadcast (package `notifhttp`)

| Method | Path | Auth scope | Description |
|--------|------|-----------|-------------|
| `POST` | `/v1/admin/tenants/{tenant_id}/notifications/broadcast` | admin token (`platform_admin` or `tenant_operator` scoped to tenant) | Broadcast message to all users of the tenant |

Body:
```json
{"subject": "...", "body": "...", "channels": ["alert_email", "webhook", "wxpush"]}
```
`channels` defaults to all three if omitted. The handler resolves
`admin.AdminIdentity`, enforces tenant scope, pages through users (cursor-based,
max 1 000 per page), and fans out via `notifdelivery.Dispatcher`.
No money path involvement.

---

## Invariants honored

### CMB invariants

- **Credentials never logged**: webhook secrets are stored only as AES-GCM
  envelopes (`notifchannel/cipher.go`). Plaintext is decrypted in memory only
  at delivery time and never written to any log, audit row, or error message.
  WxPush tokens are not credentials in the cryptographic sense but are still
  excluded from structured log fields.
- **Router reads no credentials and writes nothing**: `internal/gateway` and
  `internal/gatewayhttp` are not imported by any new package. The notification
  path is entirely orthogonal to the request-relay hot path.
- **Fail-closed on ambiguity**: if `notifpref` cannot be read (DB error), the
  delivery is skipped and the error is surfaced to the caller / worker log.
  No notification is sent with unverified channel configuration.

### Money path invariants

- **No Tx1/Tx2 modification**: `balancelowworker` reads a derived balance by
  summing `payment_credits` (a read-only aggregate). It does not write to
  `payment_credits`, `billing_events`, `billing_ledger_claims`, or
  `usage_records`.
- **shopspring/decimal for threshold comparison**: the balance-low threshold
  stored as `numeric(20,8)` is read into `decimal.Decimal` via
  `decimal.RequireFromString`. All comparisons use `decimal.Decimal.LessThan`.
  No float64 is used in the threshold path.
- **Audit trail**: admin broadcast writes one `admin_audit_events` row per
  invocation (actor, tenant, payload summary — no PII body). The row is written
  before fan-out begins so a partial delivery is auditable.

### Schema invariants

- Every new table carries `tenant_id` as the first FK column (tenant-scoped
  isolation, consistent with all existing tables).
- Unique constraints prevent duplicate channel rows and duplicate pref rows per
  user — no silent accumulation of duplicate send targets.
- `alert_email_bindings.token_hash` stores only the HMAC-SHA256 digest; the raw
  token is never persisted (consistent with `userauth` token handling).
- New migrations are numbered 0077 and 0078 (current max is 0076, confirmed by
  filesystem scan).

### Modularity

- Each of the seven new packages is single-responsibility. No package is a
  catch-all. Each file is under 500 lines.
- `internal/email` is imported (consumed) but **not modified** by any new
  package. The `email.AuthSender.SendTenantMessage` method is the only entry
  point used by `notifdelivery/email_adapter.go`.
- `internal/notifchannel` is the only package that touches the cipher; the
  three delivery adapters receive a decrypted endpoint/secret from the
  dispatcher, which loads channels via `ChannelStore`.

---

## Discriminating tests

Each test is designed so that the specific defect it defends will cause it to
fail when introduced.

| Package | Test name | Defect defended |
|---------|-----------|-----------------|
| `notifchannel` | `TestCipherRoundTrip_AES256GCM` | Ciphertext decrypts to wrong plaintext if AAD is omitted or key ID mismatch |
| `notifchannel` | `TestCipherAADMismatch_RejectsDecrypt` | Different tenant_id AAD accepted (cross-tenant secret read) |
| `notifpref` | `TestRateLimit_BlocksWithinCooldown` | Second delivery within cooldown window is not suppressed |
| `notifpref` | `TestRateLimit_AllowsAfterCooldown` | Clock-advanced delivery after cooldown is incorrectly blocked |
| `notifpref` | `TestRateLimit_DBErrorDoesNotSend` | DB failure is swallowed and delivery proceeds anyway |
| `alertemail` | `TestBind_SendsVerificationEmail` | Binding created without verification email being sent |
| `alertemail` | `TestVerify_TokenHashMismatch_Rejects` | Wrong token accepted as valid |
| `alertemail` | `TestVerify_ExpiredToken_Rejects` | Expired token accepted |
| `alertemail` | `TestBind_RateLimit_BlocksDuplicate` | Two rapid bind requests both send emails (double-send) |
| `notifdelivery` | `TestDispatcher_FanOutAllChannels` | One of three adapters not called when all channels configured |
| `notifdelivery` | `TestDispatcher_SkipsDisabledChannel` | Disabled channel adapter is still called |
| `notifdelivery` | `TestWebhookAdapter_HMACSig_PresentAndCorrect` | HMAC header absent or computed with wrong key |
| `notifdelivery` | `TestWebhookAdapter_SecretDecryptFailure_Skips` | Delivery proceeds when secret cannot be decrypted |
| `notifdelivery` | `TestWxPushAdapter_NonOKResponse_ReturnsErr` | Non-200 WxPush response silently treated as success |
| `balancelowworker` | `TestWorker_FiresWhenBelowThreshold` | Balance below threshold does not trigger notification |
| `balancelowworker` | `TestWorker_SkipsWhenAboveThreshold` | Balance above threshold incorrectly triggers notification |
| `balancelowworker` | `TestWorker_RateLimitSuppressesDuplicate` | Same user notified twice within cooldown window |
| `balancelowworker` | `TestWorker_NilPrefSkipsUser` | User with no pref row causes panic or incorrect delivery |
| `alertemailhttp` | `TestBindHandler_MissingEmail_400` | Empty email body returns 200 instead of 400 |
| `alertemailhttp` | `TestBindHandler_NoSession_401` | Unauthenticated request returns 200 |
| `notifhttp` | `TestWebhookUpsert_SecretNotReturnedAfterPUT` | Plaintext secret leaked in response body |
| `notifhttp` | `TestAdminBroadcast_TenantOperatorWrongTenant_403` | Operator for tenant A can broadcast to tenant B |
| `notifhttp` | `TestAdminBroadcast_AuditRowWrittenBeforeFanOut` | Audit row missing when delivery partially fails |

---

## Parity-or-better vs reference

Reference behavioral sources are the clean-room deep-dive documents in
`reference_deep_dive/2026-05-03/` and `reference_deep_dive/2026-05-02/`. No
reference source code is reproduced.

| Reference behavior | Source doc | HUAKAI design choice |
|--------------------|-----------|----------------------|
| User can configure a distinct alert/notification email separate from login email | `other-reference-missed-pass/huakai-missed-insertions.md` gap list | `alertemailhttp`: bind + token verify flow; token stored as HMAC hash only. Parity. |
| Outbound webhook channel with HMAC signature on each POST | Inferred from `sub2api-feature-pass/12-payment-order-recovery-refund.md` webhook model (`F-PAY-WEBHOOK-001`) | `notifdelivery/webhook_adapter.go`: HMAC-SHA256 `X-HUAKAI-Signature` header on every delivery. Better: secret stored encrypted at rest; reference does not specify storage. |
| Push channel (WxPush) for operational alerts | General reference pattern from commercial Chinese API platforms | `notifdelivery/wxpush_adapter.go`: structured HTTP POST to WxPush API. Parity. |
| Per-user per-channel enable/disable preference | Consistent across one-api, new-api user settings surfaces | `notifpref`: per-channel boolean flags + threshold. Parity. |
| Balance-low notification with user-configurable threshold | `sub2api-feature-pass/huakai-gap-and-upgrade-plan.md` (balance mutation tied to ledger events) | `balancelowworker`: cursor-scan of derived balance, compare vs `notification_prefs.balance_low_threshold` using `decimal.Decimal`. Better: DB-persisted rate-limit ledger prevents duplicate alerts across worker restarts; reference does not specify restart behavior. |
| Admin broadcast to all users of a tenant | Inferred from `other-reference-missed-pass/all-api-hub.md` status notification pattern | `notifhttp/admin_handler.go`: cursor-paged fan-out, audit row before delivery. Better: per-user channel preference honored even in broadcast (opt-out respected); reference does not specify. |
| Rate limiting to prevent alert storms | `sub2api-feature-pass/15-async-workers-cleanup.md` worker cleanup model | `notifpref/ratelimit.go`: DB-persisted `notification_rate_ledger`, upserted with `ON CONFLICT DO UPDATE` so last-send timestamp survives restart. Better than in-memory map. |

---

## Effort

**L** (Large)

Rationale: seven new packages, two new migrations, ~32 files, three delivery
adapters (email + webhook + WxPush), a background worker, a full alert-email
bind+verify flow, admin broadcast with cursor paging, DB-persisted rate-limit
ledger, AES-GCM cipher for webhook secrets, and a comprehensive discriminating
test suite covering every invariant. Each component is individually modest but
the surface area is broad. Estimated implementation: 3–4 developer-days with
parallel codex execution on server-a.

---

## Risks

| Risk | Severity | Mitigation |
|------|----------|-----------|
| WxPush API availability / rate limits unknown | Medium | `wxpush_adapter` wraps the call behind a `DeliveryAdapter` interface; if WxPush is unavailable the adapter returns an error and the dispatcher records it without failing other channels. |
| Webhook endpoint can be an internal IP (SSRF) | High | `webhook_adapter` validates the endpoint URL scheme (`https` only) and blocks RFC-1918 / loopback IP ranges before dialing. This check is enforced in `notifchannel/store_postgres.go` at insert time and again at delivery time. |
| Balance-low scan query performance at scale | Medium | Migration 0078 adds a partial index on `notification_prefs (tenant_id, user_id) WHERE balance_low_threshold IS NOT NULL`. The worker uses cursor paging (keyed on `user_id`) with a configurable page size (default 500). At 10 k users this is 20 pages; acceptable for a minute-interval ticker. |
| Duplicate alert-email verification emails | Low | `notification_rate_ledger` with `event_kind = "alert_email_verify_send"` enforces a 60-second cooldown, consistent with `email.DefaultVerificationEmailCooldown`. |
| Admin broadcast fan-out latency for large tenants | Medium | Fan-out is synchronous per-page within the HTTP request; consider moving to an outbox/DLQ model in a future slice if tenant user count exceeds ~10 k. For now, HTTP timeout is the guard. |
| Webhook secret encrypted with wrong tenant AAD | Low | `cipher.go` binds AAD to `(tenant_id, user_id, "notif_webhook_secret")` so cross-user or cross-tenant decryption returns `ErrDecryptFailed`. Test `TestCipherAADMismatch_RejectsDecrypt` defends this. |
| `internal/email.AuthSender.SendTenantMessage` returns `ErrEmailBackendUnconfigured` when SMTP not set | Low | `email_adapter` wraps this error as a non-fatal delivery failure; the dispatcher records it and continues to other channels. The per-user alert-email bind handler returns a clear 503 with `"email_backend_unconfigured"` code. |
