# Adversarial Review: totp-2fa.md (F-AUTH-008 / F-AUTH-009)

Reviewer: Claude Sonnet 4.6 (adversarial PM subagent)
Date: 2026-06-03
Design file: docs/process/gap-designs/totp-2fa.md

---

## Verdict

**needs-work**

The design is well-structured and the security intent is sound, but it contains several concrete defects that would produce a broken or unsafe implementation if dispatched as-is. The most critical are: (1) the replay-prevention column `last_used_window` is documented in prose but deliberately omitted from the published migration SQL — meaning the migration as written cannot enforce TOTP replay prevention at all; (2) the `admin_audit_events` CHECK constraint replacement in 0077 silently drops 7 actions that prior migrations added, which would cause foreign-key-adjacent constraint violations on any deployment that has already run 0047–0049; (3) the step-up token is described as "embedded in a signed context claim" with no specification of the signing scheme, leaving the implementation underspecified and potentially forgeable. All three must be fixed before dispatch.

---

## Holes

### H-1 (CRITICAL): `last_used_window` replay-prevention column is absent from the published migration SQL

The Invariants section states:

> "Replay prevention uses a last-accepted-counter stored in `totp_credentials.last_used_window` (added as a column in 0077 — omitted above for brevity, added to the actual migration)."

The actual 0077 SQL block in the design does NOT contain this column. A worker implementing from this document will create the `totp_credentials` table without `last_used_window`. The service code in `service.go` would then reference a non-existent column, or — worse — a worker who notices the gap and adds it ad-hoc would produce an undocumented schema divergence. This is not a matter of "brevity"; it is the column that backs the most critical security property (`TestCheckTOTP_ReplayCodeRejected`). The design is unimplementable as published for this invariant.

**Fix:** Add `last_used_window bigint NOT NULL DEFAULT -1` (or equivalent) to the `totp_credentials` DDL in the design. Do not omit load-bearing columns.

### H-2 (CRITICAL): `admin_audit_events` CHECK constraint replacement drops 7 existing actions

The design's 0077 migration replaces the `admin_audit_events_action_check` constraint with:

```sql
CHECK (action IN (
    'issue_api_key', 'revoke_api_key', 'list_api_keys',
    'issue_admin_token', 'revoke_admin_token', 'admin_login',
    'create_provider_account', 'disable_provider_account',
    'enable_provider_account', 'delete_provider_account',
    'create_account_credential', 'rotate_account_credential',
    'disable_account_credential', 'delete_account_credential',
    'list_account_credentials',
    'admin_disable_totp', 'admin_enable_totp'
))
```

The current maximum migration is 0076. The existing constraint (installed by 0049) includes these 7 additional actions that are absent from the design's replacement list:

- `credential_acquisition_started`
- `credential_acquisition_completed`
- `credential_acquisition_failed`
- `credential_acquisition_cancelled`
- `update_billing_settings`
- `create_pool_group`
- `update_pool_group`

Running the design's 0077 migration on a database that already has 0049 will silently narrow the constraint. Any subsequent `admin_disable_totp` audit write would succeed, but the next time a credential-acquisition or billing-settings or pool-group audit event is inserted, it will be rejected with a constraint violation, breaking those subsystems at runtime. This is a silent regression introduced by the migration.

Similarly, `admin_audit_events_target_type_check` is missing `billing_setting` and `pool_group` from the replacement.

**Fix:** The 0077 migration must carry forward ALL values from the 0049 constraint and add only the new TOTP values on top. The correct pattern is used in every prior migration that touches this constraint (0016, 0047, 0049).

### H-3 (HIGH): Step-up token signing scheme is unspecified — forgeable

The design says:

> "returns a short-lived step-up token (opaque UUID) embedded in a signed context claim"

There is no specification of: what is signed, what key signs it, where that key lives, how it is verified by `StepUpGate.RequireStepUp`, or how the claim is transmitted to the consuming handler. "Opaque UUID embedded in a signed context claim" is ambiguous to the point of being unimplementable without guessing. If a worker uses an unsigned UUID stored in context (a common misreading), the step-up gate provides no security — any request that injects an arbitrary UUID into context would pass.

The step-up challenge record IS stored in the database (`stepup_challenges`), so one correct approach would be to treat the `challenge_id` itself as the "token" and have `RequireStepUp` look it up by status+expiry. If that is the intent, the design should say so explicitly and remove the misleading "signed context claim" language.

**Fix:** Specify exactly what data structure carries the step-up proof from `VerifyChallenge` through to `RequireStepUp`, and whether it is: a DB-backed token ID, a JWT, an HMAC-signed struct, or something else. The scheme must be implementable without ambiguity.

### H-4 (HIGH): `AuthHandlerDeps` struct modification is underdescribed for FROZEN package rule

The design adds TOTP second-factor checking to `newAuthLoginHandler` in the FROZEN `internal/gatewayhttp` package. The actual `AuthHandlerDeps` struct (confirmed in `auth_handler.go`) currently has no TOTP-related field. The design says "Net addition ~60 lines" but does not specify:

- Whether a new interface field (`TOTPService` or similar) is added to `AuthHandlerDeps`.
- Whether the new field is optional (nil = TOTP disabled) or required.
- How `MountAuthRoutes` callers wire this dependency.

If a worker adds the field to `AuthHandlerDeps` without updating all callers (including test helpers in `httptest_server_test.go`), the build breaks. If a worker makes it optional with a nil check, the fail-open risk (H-5 below) applies. This is not a "modify existing files only" change that can be inferred from "~60 lines" — it requires an exported struct change.

**Fix:** Explicitly state that `AuthHandlerDeps` gains a `TOTP interface { CheckRequired(...); CheckTOTP(...); ConsumeBackupCode(...) }` (or equivalent) field, that it is nil-safe (nil = TOTP not enrolled = pass through), and that all existing callers work unchanged because nil is the zero value.

### H-5 (MEDIUM): Fail-open risk in `TestCheckTOTP_NotEnabledFailsClosed` test semantics

The test table entry reads:

> `TestCheckTOTP_NotEnabledFailsClosed` — defect: "CheckTOTP succeeds (returns nil error) when user has no active TOTP"

This is contradictory. If a user has no active TOTP enrolled, `CheckTOTP` SHOULD return nil (or a "not enrolled" sentinel) and the login flow should proceed without a second-factor check — that is correct behavior, not a defect. The test name claims "NotEnabledFailsClosed" but the described defect is exactly what correct behavior looks like.

The actual defect this test should defend against is: "the TOTP check path is bypassed or misconfigured such that even when TOTP IS active, a no-code login request succeeds." As written, the test defends against correct behavior, meaning it would pass vacuously on a broken implementation that always returns nil.

**Fix:** Rename/respecify to `TestCheckTOTP_ActiveTOTPBlocksLoginWithoutCode` — the defect it defends is "CheckTOTP returns nil (allows login) when the user HAS an active TOTP record but no code was supplied."

### H-6 (MEDIUM): `EXCLUDE USING btree` constraint requires `btree_gist` extension not guaranteed present

The 0077 migration uses:

```sql
CONSTRAINT uq_totp_credentials_active_user
    EXCLUDE USING btree (tenant_id WITH =, user_id WITH =)
    WHERE (status IN ('pending', 'active'))
```

PostgreSQL's `EXCLUDE USING btree` is not part of the default btree operator. The standard btree access method supports exclusion constraints only via the `btree_gist` extension (`USING gist` with btree_gist operators) or as a unique partial index. A plain `EXCLUDE USING btree` with equality operators (`WITH =`) is NOT valid SQL in PostgreSQL — btree does not support the `=` operator in EXCLUDE syntax because `=` is not an exclusion operator (it is handled by UNIQUE). The correct formulation for this constraint is either:

```sql
-- Option A: partial unique index (standard, no extension needed)
CONSTRAINT uq_totp_credentials_active_user
    UNIQUE (tenant_id, user_id)  -- does not work with WHERE; use index instead
```

```sql
-- Option B: unique partial index (correct)
CREATE UNIQUE INDEX uq_totp_credentials_active_user
    ON totp_credentials (tenant_id, user_id)
    WHERE status IN ('pending', 'active');
```

The `EXCLUDE USING btree ... WITH =` syntax will fail at migration time on a standard PostgreSQL installation without btree_gist. The design should use a partial unique index instead.

**Fix:** Replace the EXCLUDE constraint with a `CREATE UNIQUE INDEX ... WHERE status IN ('pending', 'active')`.

### H-7 (LOW): No down-migration provided for 0077 or 0078

The design provides only `.up.sql` blocks. Given that `stepup_challenges` is created in 0077 and shared with 0078 (WebAuthn), there is no way to roll back 0078 without also dropping `stepup_challenges`, which breaks 0077's tables. And there is no `.down.sql` for 0077 at all. Every other migration in this repo provides a down-migration. This must be specified.

**Fix:** Add 0077.down.sql and 0078.down.sql to the design. The 0078 down must NOT drop `stepup_challenges` (owned by 0077). The 0077 down must restore the prior `admin_audit_events` CHECK constraints (the 0049 values, not the 0016 values).

---

## Money/Schema/Auth/CMB risks

### MS-1: Schema — `last_used_window` absent from DDL (see H-1)

The replay-prevention mechanism has no DDL representation in the canonical migration. This is a schema correctness defect, not just documentation sloppiness.

### MS-2: Schema — `admin_audit_events` constraint regression (see H-2)

Running 0077 on a 0049+ database narrows an existing CHECK constraint and silently breaks 7 audit action types at the DB layer. This is an unsafe migration.

### MS-3: Auth — Step-up token is not tied to the session that requested it

The step-up challenge stores `(tenant_id, user_id, operation_class)` but does NOT bind to a specific session token. If user A has two concurrent sessions and session-1 completes the step-up for `billing_change`, session-2 can consume the resulting step-up claim. The design should bind the challenge to the session `family_id` or `token_id` from `SessionFromContext`.

### MS-4: Auth — Backup code comparison must be constant-time; SHA-256 lookup is table-driven

The design stores `code_hash = SHA-256(raw_code)` and uses `WHERE code_hash = $hash`. If the comparison is done at the application layer after fetching a row (rather than purely in SQL), timing differences could theoretically leak whether a hash prefix matches. More critically: the SQL `WHERE code_hash = $hash` is a direct equality match on `bytea`, which PostgreSQL evaluates without timing guarantees in application layer code. The service must use `subtle.ConstantTimeCompare` if any in-memory comparison is done after the DB fetch. The design does not mention this.

### MS-5: CMB-5 — `EnrollResult.RawSecret` tagged `json:"-"` is insufficient if the struct is ever marshalled to a log sink

The design states `RawSecret string` is tagged `json:"-"`. This prevents JSON serialisation but does NOT prevent the field from appearing in: `fmt.Sprintf("%+v", result)`, `slog.Info("enroll result", "result", result)`, or reflection-based audit sinks. The design relies solely on `json:"-"` for the CMB-5 guarantee. The correct pattern (already used in `userauth.TokenChallenge.RawToken`) is `json:"-"` PLUS a `String() string` method that returns `[redacted]`. The design should mandate this for `EnrollResult`.

### MS-6: CMB-2 — No money path in TOTP/step-up (confirmed clean)

TOTP has no cost path. No `shopspring/decimal` fields appear in any described package. CMB-2 is honoured.

### MS-7: Auth — Admin-disable endpoint tenant isolation not fully specified

The `DELETE /admin/v1/users/{user_id}/totp` endpoint requires `platform_admin or tenant_operator scoped to correct tenant`. The design does not specify how the `user_id` in the URL path is validated against the admin token's `scope_tenant_id`. Without an explicit `WHERE tenant_id = $admin_scope_tenant_id` in the SQL query (or a pre-fetch that checks the user's tenant), a `tenant_operator` for tenant 42 could disable TOTP for a user in tenant 99 by guessing a `user_id`. The existing `adminCanAccessTenant` helper in `auth_handler.go` provides a pattern, but the design does not reference it for this endpoint.

---

## Parity gaps

### PG-1: OAuth login path missing TOTP second-factor gate

The design adds TOTP second-factor checking to `POST /auth/login` (password path) but the OAuth callback path (`newAuthOAuthCallbackHandler`) in `auth_handler.go` is not mentioned. A user who has TOTP enabled can bypass the second factor entirely by authenticating via Google/GitHub OAuth. If the security model requires TOTP for all login paths, the OAuth callback must also be gated. If OAuth is intentionally exempt (e.g., TOTP applies to password-only accounts), this must be explicitly stated.

### PG-2: Session refresh path not gated

After a TOTP-complete session is established, a session refresh (`POST /session/refresh`) issues new tokens without re-verifying TOTP. This is standard and acceptable, but the design does not explicitly acknowledge that the refresh path is intentionally exempt.

### PG-3: Password-reset-via-email flow not gated post-reset

When a user completes a password reset, all sessions are revoked and the user must re-login. If the user has TOTP enabled, the post-reset login will correctly require TOTP (since it goes through the standard login path). However, the admin-driven password reset flow is not mentioned. Confirm this gap is not an issue.

### PG-4: Parity claim against sub2api is weak

The design states sub2api has TOTP based on `docker-compose.yml injecting TOTP env vars` and `go.mod listing TOTP as a dependency`. This is thin evidence. The design's parity claims ("HUAKAI is stronger because...") are speculative since the reference behaviour was not directly observed. This is acceptable given clean-room constraints, but the parity section should be labelled as "inferred" rather than "confirmed".

---

## Maintainability (god-file check)

All budgeted file sizes are under 500 lines. No god-file violations are introduced by the new packages. Specific checks:

| File | Budgeted lines | Status |
|---|---|---|
| `internal/totp/service.go` | < 320 | OK |
| `internal/totp/service_test.go` | < 480 | OK (borderline — monitor) |
| `internal/totphttp/handler.go` | < 400 | OK |
| `internal/totphttp/handler_test.go` | < 480 | OK (borderline — monitor) |
| `internal/gatewayhttp/auth_handler.go` | + ~60 lines net | Current file is 628 lines. Adding 60 lines brings it to ~688 lines — **OVER the 500-line Owner hard limit.** |

**The `auth_handler.go` god-file violation is real.** The current file is 628 lines (confirmed by reading the file). The design claims "Net addition ~60 lines keeps the file under 500 lines in total" — but 628 + 60 = 688, which is already well over 500. The design's own R-MODULARITY-001 risk entry acknowledges this risk but incorrectly states the file will stay under 500 lines.

**Fix:** The MFA check hook for the login handler must be extracted into a helper in a new file within `internal/gatewayhttp` (e.g., `auth_mfa_hook.go`) so that `auth_handler.go` itself does not grow further. The net addition to `auth_handler.go` must be zero or near-zero (a single function call to the extracted helper).

---

## Must-fix before implementation (numbered list)

1. **Add `last_used_window` column to the 0077 `totp_credentials` DDL.** The replay-prevention invariant cannot be implemented without it. Do not "omit for brevity" load-bearing schema columns from the canonical design SQL.

2. **Fix the 0077 `admin_audit_events` CHECK constraint replacement to carry forward all 0049 values** (`credential_acquisition_started/completed/failed/cancelled`, `update_billing_settings`, `create_pool_group`, `update_pool_group`) plus the new TOTP actions. Also carry forward `billing_setting` and `pool_group` in the `target_type` check.

3. **Replace `EXCLUDE USING btree` with a partial unique index.** `EXCLUDE USING btree (col WITH =)` is not valid PostgreSQL syntax for a standard installation. Use `CREATE UNIQUE INDEX ... WHERE status IN ('pending', 'active')` instead.

4. **Specify the step-up token scheme precisely.** Describe exactly what data is returned by `VerifyChallenge`, how it is transmitted to `RequireStepUp`, and how `RequireStepUp` authenticates it. If the intent is DB-backed challenge ID lookup, say so. Remove the ambiguous "signed context claim" language.

5. **Fix `TestCheckTOTP_NotEnabledFailsClosed` test semantics.** The described defect is correct behavior, not a bug. Respecify this test as `TestCheckTOTP_ActiveTOTPBlocksLoginWithoutCode` defending the actual dangerous failure mode.

6. **Add 0077.down.sql and 0078.down.sql to the design.** Ensure 0078.down does not drop `stepup_challenges`. Ensure 0077.down restores the 0049-era `admin_audit_events` constraint values.

7. **Extract the MFA hook out of `auth_handler.go` into a separate file (`auth_mfa_hook.go`).** The file is already 628 lines; adding 60 more lines exceeds the Owner hard limit of 500. The design's own risk entry (R-MODULARITY-001) contains an incorrect claim that the file will stay under 500 lines.

8. **Bind step-up challenges to a specific session family ID.** Add a `session_family_id` column to `stepup_challenges` and validate it in `RequireStepUp` to prevent cross-session step-up token reuse.

9. **Add `String() string { return "[redacted]" }` to `EnrollResult`.** `json:"-"` alone does not prevent the raw secret from appearing in `%+v` log calls or reflection-based audit sinks. Follow the pattern already used by `userauth.TokenChallenge`.

10. **Specify TOTP second-factor behaviour for the OAuth login path.** Either gate `newAuthOAuthCallbackHandler` the same way as password login, or explicitly document why OAuth-authenticated users are exempt from TOTP.
