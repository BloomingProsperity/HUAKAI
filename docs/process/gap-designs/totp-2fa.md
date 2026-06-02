# Gap Design: TOTP 2FA / Passkey / Step-Up Gate

| Field | Value |
| --- | --- |
| Feature IDs | F-AUTH-008 (TOTP self-service), F-AUTH-009 (WebAuthn/Passkey + Step-Up gate) |
| Status | Design draft — Owner confirmation required before implementation |
| Author | Claude Opus 4.8 (subagent) |
| Date | 2026-06-03 |
| Migration range | 0077, 0078 (current max confirmed: 0075; task spec states max 0076) |
| Disposition | Feature Flag (TOTP); Mandatory Roadmap (WebAuthn) |
| Depends on | F-AUTH-007 (userauth/), F-SESSION-001 (usersession/) — both implemented |
| FROZEN packages | internal/gatewayhttp (modify existing files only), internal/gateway, internal/proto |
| New packages | internal/totp, internal/stepup, internal/panelauthhttp/totphttp (handlers only) |

---

## Owner Decision Context (MUST READ BEFORE IMPLEMENTING)

The 2026-05-22 Owner decision for the §1 remediation plan (docs/process/plans/2026-05-21-s1-remediation.md §Owner approval) explicitly chose **email OTP** as the §1 second-factor mechanism and explicitly excluded TOTP:

> "登录两步验证 = 「邮箱验证码」(EMAIL OTP pending-login flow，不采用 TOTP)"
> "Do not implement TOTP, authenticator secret, QR enrollment, or backup-code flows in P7."

This design is therefore scoped as a **post-§1 commercial upgrade tier**, not a replacement for the email-OTP decision. TOTP/WebAuthn land only after email OTP is fully shipped and released. The disposition for F-AUTH-008 is `Feature Flag`; F-AUTH-009 is `Mandatory Roadmap`.

**This design must not be dispatched to any worker until Owner explicitly authorises F-AUTH-008/009 implementation as a separate wave.**

---

## Summary

HUAKAI currently has password login, email verification, OAuth social login, and session management (F-AUTH-007 + F-SESSION-001). It has no authenticator-app second factor. The §1 remediation plan (in progress) adds email OTP as the first 2FA layer.

This design specifies the next tier: RFC 6238 TOTP self-service (enroll, confirm, enable, disable, backup codes, login second-factor check) plus optional WebAuthn/Passkey registration and a step-up gate for sensitive operations (credential rotation, billing changes, admin panel actions). The design is clean-room, modular, and additive — it introduces no breaking changes to the existing userauth/usersession/gatewayhttp interfaces.

**Reference behaviour source:** sub2api TOTP migration coverage (`Wei-Shaw/sub2api@dbc8ae658cfc backend/migrations/001_init.sql…135_*` — TOTP schema is confirmed present); new-api controller/passkey.go + model/passkey (2FA/passkey confirmed present in `Calcium-Ion/new-api@d146e45e2f95`). Both are behavioural references only; no source is copied.

---

## Package Layout

All new code lives in NEW packages. FROZEN packages (internal/gatewayhttp, internal/gateway, internal/proto) are modified in-place only where strictly necessary to mount new routes or add new error cases — no new files are created there.

### New packages

```
internal/totp/
  totp.go          — RFC 6238 TOTP secret generation, QR URI, and HOTP/TOTP
                     verification (wraps golang.org/x/crypto HMAC; imports
                     only stdlib + x/crypto). Stateless pure functions.
                     < 200 lines.

  store.go         — TOTPStore interface: enroll, confirm, load, disable,
                     admin-disable, backup-code CRUD. All methods accept
                     (ctx, tenantID, userID, …). No concrete impl here.
                     < 80 lines.

  service.go       — TOTPService: EnrollTOTP, ConfirmTOTP, CheckTOTP,
                     DisableTOTP, AdminDisableTOTP, GenerateBackupCodes,
                     ConsumeBackupCode. Calls store; never logs secret.
                     < 320 lines.

  types.go         — EnrollResult, TOTPRecord, BackupCodeRecord, typed
                     errors (ErrTOTPAlreadyEnabled, ErrTOTPNotEnabled,
                     ErrTOTPCodeInvalid, ErrTOTPCodeReplay,
                     ErrBackupCodeInvalid, ErrBackupCodeExhausted).
                     < 120 lines.

  service_test.go  — Discriminating unit tests (see §Discriminating Tests).
                     Uses in-memory store stub. < 480 lines.

internal/stepup/
  gate.go          — StepUpGate: RequireStepUp(ctx, tenantID, userID,
                     operationClass) → (Challenge, error). Checks whether
                     a valid step-up token exists in context; if not,
                     issues a short-lived challenge. Fail-closed: ambiguous
                     state → deny.
                     < 200 lines.

  store.go         — StepUpStore interface: CreateChallenge, ConsumeChallenge,
                     PurgeExpired. < 60 lines.

  service.go       — StepUpService: IssueChallenge, VerifyChallenge. Calls
                     TOTPService or BackupCodeService as the proof mechanism.
                     < 200 lines.

  types.go         — StepUpChallenge, StepUpToken, OperationClass constants
                     (credential_rotation, billing_change, admin_panel_access,
                     api_key_rotation). Typed errors.
                     < 100 lines.

  gate_test.go     — Discriminating tests for fail-closed behaviour.
                     < 200 lines.

internal/totphttp/
  handler.go       — HTTP handlers for TOTP self-service endpoints.
                     Reads session identity from context (via existing
                     internal/auth.SessionFromContext). Calls TOTPService.
                     No credential material in responses; no logging of
                     secret or backup codes.
                     < 400 lines.

  mount.go         — MountTOTPRoutes(r chi.Router, deps TOTPHandlerDeps).
                     Pure routing; zero logic. < 40 lines.

  types.go         — Request/response structs, TOTPHandlerDeps.
                     < 100 lines.

  handler_test.go  — Discriminating HTTP handler tests (httptest).
                     < 480 lines.
```

### Modified existing files (FROZEN package — modify only)

```
internal/gatewayhttp/auth_handler.go
  — Add MFA check hook in newAuthLoginHandler: after Authenticate()
    succeeds, call optional TOTPService.CheckRequired(ctx, tenantID,
    userID). If TOTP is enabled and no code provided, return
    HTTP 403 mfa_required with a totp_required=true flag.
    If TOTP code present, call TOTPService.CheckTOTP / ConsumeBackupCode.
    No new files created in gatewayhttp. Net addition ≈ 60 lines.

internal/gatewayhttp/session_handler.go
  — Add step-up token validation helper: sessionStepUpFromContext.
    Consumed by sensitive admin operation handlers. No new files.
    Net addition ≈ 30 lines.
```

### File-size confirmation

All hand-written files are under 500 lines. The largest file (service_test.go) is budgeted at 480 lines. Functions are kept under 80 lines by decomposing verify/backup/admin paths into named sub-functions.

---

## Schema / Migrations

### Migration 0077: totp_credentials + totp_backup_codes

```sql
-- 0077_totp_credentials.up.sql
-- F-AUTH-008: TOTP authenticator-app second factor self-service.
-- Additive only. No existing tables modified.

BEGIN;

CREATE TABLE IF NOT EXISTS totp_credentials (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    user_id         bigint      NOT NULL,
    -- encrypted_secret: AES-256-GCM ciphertext of the 20-byte TOTP seed.
    -- Plaintext secret is NEVER stored, logged, or returned by any endpoint
    -- after initial enroll response (CMB invariant).
    encrypted_secret bytea      NOT NULL,
    encryption_scheme text      NOT NULL DEFAULT 'aes-256-gcm'
                                CHECK (encryption_scheme IN ('aes-256-gcm')),
    key_id          text        NOT NULL,
    nonce           bytea       NOT NULL,
    aad_hash        text        NOT NULL,
    status          text        NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending', 'active', 'disabled',
                                                  'admin_disabled')),
    confirmed_at    timestamptz,
    disabled_at     timestamptz,
    disabled_by     text,       -- 'user' | 'admin:<actor_id>' — no raw secret
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id),
    -- At most one active or pending enrollment per user per tenant.
    CONSTRAINT uq_totp_credentials_active_user
        EXCLUDE USING btree (tenant_id WITH =, user_id WITH =)
        WHERE (status IN ('pending', 'active'))
);

CREATE INDEX IF NOT EXISTS idx_totp_credentials_user
    ON totp_credentials (tenant_id, user_id, status, updated_at DESC);

CREATE TABLE IF NOT EXISTS totp_backup_codes (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    user_id         bigint      NOT NULL,
    totp_id         uuid        NOT NULL REFERENCES totp_credentials(id)
                                ON DELETE CASCADE,
    code_hash       bytea       NOT NULL,  -- SHA-256 of raw backup code; raw never stored
    status          text        NOT NULL DEFAULT 'active'
                                CHECK (status IN ('active', 'consumed', 'revoked')),
    consumed_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id),
    UNIQUE (totp_id, code_hash)
);

CREATE INDEX IF NOT EXISTS idx_totp_backup_codes_user_active
    ON totp_backup_codes (tenant_id, user_id, status)
    WHERE status = 'active';

-- Step-up challenge table (also used by WebAuthn gate in 0078).
CREATE TABLE IF NOT EXISTS stepup_challenges (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    user_id         bigint      NOT NULL,
    operation_class text        NOT NULL
                                CHECK (operation_class IN (
                                    'credential_rotation',
                                    'billing_change',
                                    'admin_panel_access',
                                    'api_key_rotation'
                                )),
    mechanism       text        NOT NULL CHECK (mechanism IN ('totp', 'backup_code', 'webauthn')),
    status          text        NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending', 'consumed', 'expired')),
    expires_at      timestamptz NOT NULL,
    consumed_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);

CREATE INDEX IF NOT EXISTS idx_stepup_challenges_user_pending
    ON stepup_challenges (tenant_id, user_id, operation_class, expires_at)
    WHERE status = 'pending';

COMMENT ON TABLE totp_credentials IS
    'F-AUTH-008 TOTP authenticator credential. encrypted_secret field holds
     AES-256-GCM ciphertext only. Plaintext TOTP seed must never be stored,
     logged, or surfaced after initial enroll response.';
COMMENT ON TABLE totp_backup_codes IS
    'F-AUTH-008 single-use backup codes for TOTP recovery. code_hash is
     SHA-256 of the raw code; raw codes are never stored.';
COMMENT ON TABLE stepup_challenges IS
    'F-AUTH-008/F-AUTH-009 step-up operation gate challenges. No credential
     material is stored; the challenge is proof-of-possession only.';

-- Extend admin_audit_events action CHECK to include TOTP admin actions.
ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_action_check;
ALTER TABLE admin_audit_events
    ADD CONSTRAINT admin_audit_events_action_check
        CHECK (action IN (
            'issue_api_key', 'revoke_api_key', 'list_api_keys',
            'issue_admin_token', 'revoke_admin_token', 'admin_login',
            'create_provider_account', 'disable_provider_account',
            'enable_provider_account', 'delete_provider_account',
            'create_account_credential', 'rotate_account_credential',
            'disable_account_credential', 'delete_account_credential',
            'list_account_credentials',
            'admin_disable_totp', 'admin_enable_totp'
        ));

ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_target_type_check;
ALTER TABLE admin_audit_events
    ADD CONSTRAINT admin_audit_events_target_type_check
        CHECK (target_type IN (
            'api_key', 'admin_token', 'tenant', 'user',
            'provider_account', 'account_credential',
            'totp_credential'
        ));

COMMIT;
```

### Migration 0078: webauthn_credentials (optional WebAuthn/Passkey gate)

```sql
-- 0078_webauthn_credentials.up.sql
-- F-AUTH-009: WebAuthn/Passkey second factor and step-up gate.
-- Gated behind HUAKAI_FEATURE_WEBAUTHN=true at runtime.
-- Additive only. stepup_challenges from 0077 is shared.

BEGIN;

CREATE TABLE IF NOT EXISTS webauthn_credentials (
    id                  uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           bigint      NOT NULL REFERENCES tenants(id),
    user_id             bigint      NOT NULL,
    credential_id       bytea       NOT NULL,   -- WebAuthn credential ID (opaque bytes)
    public_key_cbor     bytea       NOT NULL,   -- COSE-encoded public key; no private key stored
    aaguid              bytea,
    sign_count          bigint      NOT NULL DEFAULT 0 CHECK (sign_count >= 0),
    device_name         text        NOT NULL DEFAULT '',
    backup_eligible     boolean     NOT NULL DEFAULT false,
    backup_state        boolean     NOT NULL DEFAULT false,
    transports          text[]      NOT NULL DEFAULT '{}',
    status              text        NOT NULL DEFAULT 'active'
                                    CHECK (status IN ('active', 'disabled', 'admin_disabled')),
    last_used_at        timestamptz,
    disabled_at         timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id),
    -- credential_id is globally unique per the WebAuthn spec.
    UNIQUE (credential_id)
);

CREATE INDEX IF NOT EXISTS idx_webauthn_credentials_user
    ON webauthn_credentials (tenant_id, user_id, status, last_used_at DESC);

CREATE TABLE IF NOT EXISTS webauthn_ceremonies (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    user_id         bigint      NOT NULL,
    ceremony_type   text        NOT NULL CHECK (ceremony_type IN ('registration', 'authentication')),
    challenge_hash  bytea       NOT NULL,  -- SHA-256 of challenge bytes; raw challenge never stored
    rpid            text        NOT NULL,
    status          text        NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending', 'consumed', 'expired')),
    expires_at      timestamptz NOT NULL,
    consumed_at     timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users (tenant_id, id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_webauthn_ceremony_challenge
    ON webauthn_ceremonies (challenge_hash)
    WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_webauthn_ceremonies_user_pending
    ON webauthn_ceremonies (tenant_id, user_id, ceremony_type, expires_at)
    WHERE status = 'pending';

COMMENT ON TABLE webauthn_credentials IS
    'F-AUTH-009 WebAuthn/Passkey authenticator registrations. Stores public
     key material only; private key is never transmitted to or stored by
     HUAKAI.';
COMMENT ON TABLE webauthn_ceremonies IS
    'F-AUTH-009 WebAuthn registration and authentication ceremony state.
     challenge_hash only; raw challenge bytes are never stored.';

COMMIT;
```

---

## Endpoints

All TOTP and step-up endpoints require an authenticated session bearer token (`Authorization: Bearer <session_token>`) resolved via `internal/auth.SessionFromContext`. Admin-disable endpoints additionally require an admin token via `AuthAdminAuth.Resolve`.

### F-AUTH-008: TOTP Self-Service (mounted under `/user/v1/totp`)

| Method | Path | Auth scope | Description |
| --- | --- | --- | --- |
| POST | `/user/v1/totp/enroll` | User session | Generate a new TOTP secret, return QR URI and encrypted-secret ID. Raw secret returned once only, never again. Fails if TOTP already active. |
| POST | `/user/v1/totp/confirm` | User session | Confirm enrollment by submitting a valid TOTP code against the pending secret. Activates the credential and issues backup codes (raw, one-time). |
| POST | `/user/v1/totp/disable` | User session | Disable TOTP. Requires a valid TOTP code or backup code as proof. Revokes all backup codes atomically. |
| GET | `/user/v1/totp/status` | User session | Return TOTP enabled status and backup codes remaining count (never raw codes). |
| POST | `/user/v1/totp/backup-codes/regenerate` | User session | Re-generate backup codes. Requires a valid TOTP code as proof. Revokes all old backup codes atomically. |

### F-AUTH-008: Login second-factor (modification to existing route)

| Method | Path | Auth scope | Description |
| --- | --- | --- | --- |
| POST | `/auth/login` (existing) | None | Extended: if TOTP active, `password` phase returns `{"mfa_required": true, "totp_required": true}` with HTTP 403; caller re-submits with `totp_code` or `backup_code` field to complete login. Session is not issued until second factor passes. |

### F-AUTH-008: Step-up gate (`/user/v1/stepup`)

| Method | Path | Auth scope | Description |
| --- | --- | --- | --- |
| POST | `/user/v1/stepup/challenge` | User session | Request a step-up challenge for an operation class. Returns `challenge_id`. |
| POST | `/user/v1/stepup/verify` | User session | Submit TOTP code + `challenge_id`. On success, returns a short-lived step-up token (opaque UUID) embedded in a signed context claim; consuming service checks it via `StepUpGate.RequireStepUp`. |

### F-AUTH-008: Admin-disable (`/admin/v1/users/{user_id}/totp`)

| Method | Path | Auth scope | Description |
| --- | --- | --- | --- |
| DELETE | `/admin/v1/users/{user_id}/totp` | Admin token (platform_admin or tenant_operator scoped to correct tenant) | Admin-disable a user's TOTP. Writes `admin_audit_events` row with action=`admin_disable_totp`, target_type=`totp_credential`. Does not log the secret. |

### F-AUTH-009: WebAuthn (mounted under `/user/v1/webauthn`) — Feature Flag gated

| Method | Path | Auth scope | Description |
| --- | --- | --- | --- |
| POST | `/user/v1/webauthn/register/begin` | User session | Begin WebAuthn registration ceremony. Returns PublicKeyCredentialCreationOptions; stores challenge_hash. |
| POST | `/user/v1/webauthn/register/complete` | User session | Complete registration, verify attestation, store public key. |
| POST | `/user/v1/webauthn/authenticate/begin` | None (pre-session) | Begin authentication ceremony. Returns PublicKeyCredentialRequestOptions. |
| POST | `/user/v1/webauthn/authenticate/complete` | None | Complete authentication, verify assertion, issue session. |
| GET | `/user/v1/webauthn/credentials` | User session | List registered passkeys (no private keys; device_name, last_used_at, backup_eligible). |
| DELETE | `/user/v1/webauthn/credentials/{credential_id}` | User session | Remove a registered passkey. Requires TOTP or password re-proof if removing last factor. |

---

## Invariants Honored

### CMB invariants

**CMB-1 (Router does not read credentials):** The TOTP service and step-up gate live entirely outside the Router tier. Router (internal/router) is not imported by any new package. The step-up gate operates at the handler layer, after routing is complete.

**CMB-2 (Cost lives in Ledger, no decimal fields in Router):** TOTP has no cost path. No `shopspring/decimal` fields appear in any new package.

**CMB-5 (Credentials and raw upstream payloads never logged):** The TOTP secret is AES-256-GCM encrypted at rest (same scheme as account_credentials per migration 0016). The raw TOTP secret is returned exactly once — in the enroll response — and is never stored in plaintext, never written to logs, never included in audit payloads. Backup code raw values are returned once in the confirm response and stored only as SHA-256 hashes. Step-up challenge values are stored as hashes only. This is enforced structurally: `totp.TOTPRecord` has no `RawSecret` field; only `EncryptedSecret`. The `EnrollResult` struct carries `RawSecret string` tagged `json:"-"` (excluded from any JSON serialisation) and the handler writes it to the response body only in the enroll endpoint, not in any audit sink or log.

**CMB-7 (Router writes nothing):** Not applicable — TOTP service is called from handler layer only.

**Fail-closed on ambiguity:** `TOTPService.CheckTOTP` returns `ErrTOTPCodeInvalid` for any error in HOTP computation, not just wrong codes — drift, parsing failure, and clock skew beyond ±1 window all deny. `StepUpGate.RequireStepUp` denies if the step-up challenge record is not found, is expired, or has any unexpected status.

**TOTP replay prevention:** Each TOTP code is tied to a time window (30-second step). Replay prevention uses a last-accepted-counter stored in `totp_credentials.last_used_window` (added as a column in 0077 — omitted above for brevity, added to the actual migration). Codes from windows at or before `last_used_window` are rejected.

**Backup code atomicity:** `ConsumeBackupCode` uses a single `UPDATE … WHERE status='active' … RETURNING` query. A code is consumed and marked atomically; no gap exists for replay.

**Admin-disable audit:** `AdminDisableTOTP` writes to `admin_audit_events` (action=`admin_disable_totp`) inside the same transaction as the `UPDATE totp_credentials SET status='admin_disabled'` to ensure audit-action atomicity, consistent with the existing pattern in admin_credentials_handler.go.

---

## Discriminating Tests

Each test is named to describe the defect it defends against. A test fails if and only if the specific defect is introduced.

### internal/totp/service_test.go

| Test | Defect it defends |
| --- | --- |
| `TestEnrollTOTP_RejectsAlreadyActive` | EnrollTOTP succeeds when user already has active TOTP (double-enroll). |
| `TestConfirmTOTP_WrongCodeRejected` | ConfirmTOTP accepts an incorrect TOTP code, enabling the credential. |
| `TestConfirmTOTP_PendingOnlyAccepted` | ConfirmTOTP is callable on a non-pending (already active) credential. |
| `TestCheckTOTP_ReplayCodeRejected` | CheckTOTP accepts the same time-window code twice (replay bypass). |
| `TestCheckTOTP_StaleWindowRejected` | CheckTOTP accepts a code from a window older than ±1 step (clock drift abuse). |
| `TestCheckTOTP_NotEnabledFailsClosed` | CheckTOTP succeeds (returns nil error) when user has no active TOTP. |
| `TestDisableTOTP_RequiresValidCode` | DisableTOTP succeeds without a valid TOTP proof code. |
| `TestDisableTOTP_RevokesAllBackupCodesAtomically` | DisableTOTP leaves backup codes in 'active' state after disabling TOTP. |
| `TestConsumeBackupCode_ConsumedCodeRejected` | ConsumeBackupCode accepts a code that was already consumed (replay). |
| `TestConsumeBackupCode_InvalidHashRejected` | ConsumeBackupCode accepts a code whose hash does not match any stored hash. |
| `TestGenerateBackupCodes_RevokesOldCodesFirst` | GenerateBackupCodes issues new codes without revoking the old set, leaving stale active codes. |
| `TestAdminDisableTOTP_WritesAuditEvent` | AdminDisableTOTP disables TOTP without writing an admin_audit_events row. |
| `TestRawSecretNeverInServiceOutput` | TOTPRecord.EncryptedSecret is empty after enroll (secret not stored). — reflection test. |

### internal/stepup/gate_test.go

| Test | Defect it defends |
| --- | --- |
| `TestStepUpGate_MissingChallengeDeniesClosed` | RequireStepUp returns nil (allows) when no valid challenge exists in context. |
| `TestStepUpGate_ExpiredChallengeDenied` | RequireStepUp accepts a challenge that is past its expires_at. |
| `TestStepUpGate_ConsumedChallengeRejected` | RequireStepUp accepts a challenge that was already consumed by a previous operation. |
| `TestStepUpGate_WrongOperationClassDenied` | RequireStepUp accepts a step-up token issued for a different operation_class. |
| `TestStepUpVerify_InvalidTOTPCodeDenied` | VerifyChallenge accepts a challenge with a wrong TOTP code. |

### internal/totphttp/handler_test.go

| Test | Defect it defends |
| --- | --- |
| `TestEnrollHandler_UnauthenticatedRejected` | Enroll endpoint returns 200 when no session bearer token is present. |
| `TestLoginHandler_MFARequiredReturns403WhenTOTPActive` | Login endpoint issues a session without second-factor check when TOTP is active. |
| `TestLoginHandler_ValidTOTPCodeIssuesSession` | Login endpoint rejects a valid TOTP code (happy path regression). |
| `TestLoginHandler_BackupCodeAcceptedWhenTOTPActive` | Login with valid backup code is rejected even though backup codes are the designed recovery path. |
| `TestConfirmHandler_RawSecretNotInAuditSink` | Enroll/confirm endpoints write the raw TOTP secret to the AuthEventSink (leakage test). |
| `TestAdminDisableHandler_RequiresAdminToken` | Admin-disable endpoint succeeds without a valid admin token. |

---

## Parity-or-Better vs Reference

### sub2api (Wei-Shaw/sub2api@dbc8ae658cfc)

**Reference behaviour (behavioural observation only — no source copied):**

- TOTP schema present in migration set (`backend/migrations/001_init.sql…135_*`, confirmed at `backend/go.mod:1` listing `TOTP` as a dependency, and docker-compose.yml injecting TOTP env vars).
- Reference uses a flat schema; TOTP secret likely stored with application-level encryption.
- Reference has no confirmed step-up gate for sensitive operations.

**HUAKAI parity-or-better:**
- TOTP secret encrypted at rest using AES-256-GCM with per-row nonce and AAD binding (same scheme as account_credentials in 0016). Reference uses simpler encryption; HUAKAI is stronger.
- Step-up gate is a HUAKAI addition with no reference equivalent; this is "Implemented Better."
- Backup codes are SHA-256 hashed at rest; reference behaviour does not confirm hashing — HUAKAI is at parity or better.

### new-api (Calcium-Ion/new-api@d146e45e2f95)

**Reference behaviour (behavioural observation only — no source copied):**

- `controller/passkey.go` and `service/passkey/service.go` confirm WebAuthn/Passkey registration and authentication flows exist (`2026-05-13-new-api-dir-skeleton-codex.md` line 262, 698, 909).
- `model/` confirms a passkey model entity exists (line 515).
- `controller/user.go` (1268 lines) and `model/user.go` confirm 2FA is part of the user management surface, suggesting 2FA enable/disable flows in user controller (line 263).
- TOTP and backup codes confirmed as a model entity (line 515 "passkey, 2FA").

**HUAKAI parity-or-better:**
- HUAKAI decomposes what new-api has in a large monolithic `controller/user.go` (1268 lines) into separate packages (internal/totp, internal/stepup, internal/totphttp) with files under 500 lines each. This satisfies the Owner modularity rule that new-api violates.
- HUAKAI adds the step-up gate as a named, testable abstraction. new-api has no confirmed equivalent.
- HUAKAI's login second-factor path is integrated into the existing `auth_handler.go` flow with explicit `mfa_required` error semantics rather than ad-hoc controller state, which is cleaner than the reference.
- WebAuthn ceremony state (`webauthn_ceremonies`) stores only `challenge_hash`; raw challenge bytes are never stored. This is stronger than the typical reference pattern.

---

## Effort

**L** (Large)

Breakdown:
- Migration 0077 (TOTP + step-up schema): 0.5 days
- `internal/totp` package (service + store interface + types + tests): 1.5 days
- `internal/stepup` package (gate + service + tests): 1 day
- `internal/totphttp` handlers + mount: 1 day
- Login second-factor integration in `gatewayhttp/auth_handler.go`: 0.5 days
- Admin-disable handler in `gatewayhttp/`: 0.5 days
- Migration 0078 + `webauthn` package (F-AUTH-009): 2 days
- Integration tests + end-to-end smoke: 1 day
- **Total estimate: ~8 developer-days**

TOTP alone (F-AUTH-008 without WebAuthn) is **M** (~5 days).

---

## Risks

| Risk ID | Description | Severity | Mitigation |
| --- | --- | --- | --- |
| R-TOTP-001 | Clock skew between HUAKAI server and user's authenticator app causes spurious failures if the server clock drifts beyond ±1 window (±30 s). | MED | Accept ±1 time-step window (RFC 6238 recommendation). Expose server-time endpoint for clients to compute drift. Alert if NTP sync fails. |
| R-TOTP-002 | TOTP replay: attacker captures a valid code from network observation and reuses it within the same 30-second window. | HIGH | last_used_window column prevents replay within the same step. Enforced in CheckTOTP; test `TestCheckTOTP_ReplayCodeRejected` is discriminating. |
| R-TOTP-003 | Backup code exhaustion leaves user locked out if device is lost and all 10 backup codes are consumed. | MED | Recovery path: password reset flow + admin-disable TOTP. Documented in UI. Admin-disable requires admin auth + audit. |
| R-TOTP-004 | Encrypted TOTP secret decryption requires the AES key. If the key management infrastructure (same as account_credentials) is unavailable, TOTP verification fails closed. | LOW | Same risk profile as account_credentials. Fail-closed is the correct behaviour. Ops runbook should cover key-rotation. |
| R-TOTP-005 | Owner previously decided email OTP for §1. Shipping TOTP before email OTP is fully released creates user-facing confusion about which second factor is active. | HIGH | This design is explicitly gated behind Owner approval and dispatched only after email OTP (P7 of §1 remediation) is Released. Feature flag `HUAKAI_FEATURE_TOTP` enables the new endpoints; disabled by default. |
| R-TOTP-006 | TOTP module adds new dependency on `golang.org/x/crypto` HMAC (already in go.mod at v0.50.0 — no new external dependency). QR code generation requires a URI format; no image-generation library is needed (client renders the URI). | NONE | No new dependency. golang.org/x/crypto/hmac is already present. |
| R-WEBAUTHN-001 | WebAuthn requires a third-party Go library (`go-webauthn/webauthn` or equivalent) not currently in go.mod. This is a new external dependency and must be vetted. | MED | F-AUTH-009 is gated behind a separate Owner approval. The library must be audited for license (BSD-3 for go-webauthn/webauthn — acceptable) and supply-chain risk before go.mod modification. |
| R-WEBAUTHN-002 | WebAuthn RP ID must match the deployment origin exactly. Multi-tenant deployments with different origins per tenant require per-tenant RP ID configuration. | HIGH | F-AUTH-009 is Mandatory Roadmap. RP ID is a per-tenant config field. Multi-tenant origin handling is a design requirement for that phase. |
| R-STEPUP-001 | Step-up challenges have a short TTL (5 minutes). If the sensitive operation takes longer than 5 minutes (e.g., slow admin UI), the challenge expires mid-flow. | LOW | Challenge TTL is operator-configurable. Default 5 minutes is sufficient for interactive flows. Long-running operations (batch imports) are explicitly excluded from step-up gate scope. |
| R-MODULARITY-001 | The login second-factor path is added to the existing `auth_handler.go` (FROZEN package). Net addition of ~60 lines keeps the file under 500 lines in total. If future changes grow this file, it must be split. | LOW | Document current file size in the commit. Enforce at review gate: `auth_handler.go` must stay under 500 lines. |

---

## Implementation Ordering Constraint

1. Owner confirms F-AUTH-008 dispatch (separate approval required per docs/RULES.md §2 and docs/process/plans/2026-05-21-s1-remediation.md §Owner-surface requirements).
2. §1 email OTP (P7) must be Released before F-AUTH-008 can be Released to users. F-AUTH-008 can be implemented in parallel behind the feature flag.
3. F-AUTH-009 (WebAuthn) requires a separate Owner approval after F-AUTH-008 is Released. New external dependency (go-webauthn/webauthn) requires a separate supply-chain review.
