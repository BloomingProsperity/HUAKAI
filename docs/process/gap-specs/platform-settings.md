# Gap Spec: Admin Platform-Settings Consolidation

**Gap ID:** platform-settings
**Spec date:** 2026-06-03
**Migration:** 0077 (verified: current max is 0076 — `sql/migrations/0076_user_role.up.sql`)
**Risk class:** schema (new table + audit CHECK extension; no money/auth hot-path writes)
**Parallelizable:** true (new packages `internal/platformsettings` and `internal/platformsettingshttp` touch no shared files except `cmd/gateway/routes.go`, `cmd/gateway/wiring.go`, and `sqlc.yaml`)

---

## False Premises in the Gap Design

The following claims in `docs/process/gap-designs/platform-settings.md` were verified against real code and are **false**. Implementing the design as written without these corrections will produce broken or inert behavior.

### FP-1 — `registration_enabled` / `invitation_required` are NOT missing runtime toggles

**Design claim:** These are missing; putting them in `platform_settings` fills the gap.

**Reality:** `userauth.Service` already has `RegistrationMode RegistrationMode` and `InviteRequired bool` as live struct fields set from `HUAKAI_USER_REGISTRATION_MODE` env var at boot.

- `cmd/gateway/config.go:83` — `loadUserRegistrationModeFromEnv()` parses open/invite_required/disabled/admin_only
- `cmd/gateway/lifecycle.go:258-262` — `buildUserServices` calls `loadUserRegistrationModeFromEnv()` and assigns `userAuthService.RegistrationMode = registrationMode`
- `internal/userauth/service.go:43-44` — `RegistrationMode RegistrationMode` and `InviteRequired bool` are struct fields checked in `registrationMode()` (line 210)

**Consequence:** Writing `registration_enabled` or `invitation_required` keys to `platform_settings` has zero behavioral effect unless `buildUserServices` is also changed to read from the DB service at startup (or on TTL). The design does not specify this wiring change. These two keys remain **inert** unless the integration spec is extended.

**Correction:** The first slice must either (a) omit these two keys from v1 scope and add a wiring-integration subtask, or (b) explicitly describe changing `buildUserServices` to accept a `platformsettings.Service` and read `RegistrationMode` from DB at startup with a fallback to env.

### FP-2 — `stream_timeout_seconds` is NOT hard-coded

**Design claim:** Stream timeout is a hard-coded constant; platform_settings replaces it.

**Reality:** Stream timeout is already env-configurable, not hard-coded:
- `cmd/gateway/middleware.go:244-249` — `buildStreamForwarder` calls `streamDurationEnv("HUAKAI_STREAM_TOTAL_TIMEOUT", 600*time.Second)` and similarly for First/Inter timeouts
- `internal/gateway/forwarder_types.go:60-63` — `TimeoutConfig` struct with `TotalStreamTimeout time.Duration`

There is no hard-coded constant to remove (Risk R3 in the design is vacuously true). Adding `stream_timeout_seconds` to `platform_settings` is still valid for runtime mutability without restart, but the migration from a hard-coded constant is a false premise — the integration task is simpler (read from DB, fall back to env).

### FP-3 — `cooldown_429_seconds` / `cooldown_529_seconds` do NOT map to an existing service

**Design claim:** "`internal/rate.ModelCooldownService` reads cooldown durations at construction time from env-vars. Changing them to DB-backed values requires the cooldown service to re-read on each evaluation."

**Reality:**
- `internal/rate/model_cooldown.go:36-99` — `ModelCooldownService` has a single `defaultCooldown time.Duration` (default 5 min), settable via `WithDefaultCooldown` option at construction. It calls `SetProviderAccountModelRateLimit` on a billing DB store.
- There is **no** per-HTTP-status (429 vs 529) distinction. There is no env-var for cooldown duration today either — the value is a Go constant (`defaultModelCooldownDuration = 5 * time.Minute`).

**Consequence:** The design's claim that these keys wire into existing cooldown logic is false — the integration requires adding a new read path in `ModelCooldownService` (or its caller), not just changing a dependency injection.

### FP-4 — Audit action `platform_setting.upsert` REQUIRES a migration to extend the CHECK constraint

**Design claim (implicit):** Writing `action = 'platform_setting.upsert'` to `admin_audit_events` works with just the `platform_settings` table migration.

**Reality:** `admin_audit_events.action` has a CHECK constraint that is updated in every new migration that adds audit actions. The current (post-0049) allowed set is:

```
'issue_api_key', 'revoke_api_key', 'list_api_keys',
'issue_admin_token', 'revoke_admin_token', 'admin_login',
'create_provider_account', 'disable_provider_account',
'enable_provider_account', 'delete_provider_account',
'create_account_credential', 'rotate_account_credential',
'disable_account_credential', 'delete_account_credential',
'list_account_credentials',
'credential_acquisition_started', 'credential_acquisition_completed',
'credential_acquisition_failed', 'credential_acquisition_cancelled',
'update_billing_settings',
'create_pool_group', 'update_pool_group'
```

`platform_setting.upsert` (or any new value) is **not** in this list. Any INSERT will fail at the DB level. Migration 0077 **must** also extend both `admin_audit_events_action_check` and `admin_audit_events_target_type_check`.

**Correction:** Migration 0077 must add `'upsert_platform_setting'` to the action CHECK and `'platform_setting'` to the target_type CHECK (using the established snake_case verb-noun pattern matching existing entries like `'update_billing_settings'`).

---

## Verified True Residual (what is genuinely missing)

The following features have no existing implementation and require real new code:

| Feature | Status |
|---|---|
| `platform_settings` table (scope, setting_key, setting_value, updated_at, updated_by) | Not present in any migration |
| GET/LIST/PUT admin API at `/v1/admin/platform-settings` | No route exists in `cmd/gateway/routes.go` |
| `promo_enabled` setting + wiring into `voucher.Service` | `voucher.Service` has no enabled-flag check today |
| `oauth_providers_enabled` setting + enabled-flag gate in OAuth handlers | OAuth is always active when provider is configured |
| `captcha_enabled` / `captcha_site_key` settings | No CAPTCHA infrastructure exists |
| Audit CHECK constraint extension for `upsert_platform_setting` / `platform_setting` | Required in migration 0077 (see FP-4) |
| `stream_timeout_seconds` for runtime mutability without restart | Valid addition; integration is simpler than design states (see FP-2) |
| `cooldown_*_seconds` for runtime mutability | Valid but integration is harder than design states (see FP-3); defer to a follow-on slice |

Items that should be **deferred** from the first slice (complex integrations):
- Wiring `registration_enabled` / `invitation_required` into `userauth.Service` (FP-1 correction required first)
- Wiring `stream_timeout_seconds` into `gateway.StreamForwarder` (env fallback wiring)
- Wiring `cooldown_*_seconds` into `ModelCooldownService` (new read path required)
- These can go in slice 2 once the store/service/handler/audit infra exists

---

## Reuse Points (existing code to reuse, file:line)

| Reuse target | Location | How to reuse |
|---|---|---|
| `billing_settings.sql` upsert pattern | `sql/queries/billing_settings.sql:21-29` | Model `platform_settings.sql` queries on same ON CONFLICT pattern with `RETURNING` |
| `admin.AdminIdentity` / role constants | `internal/admin/operator_auth.go:31-36`, `internal/admin/admin.go:53-56` | Import `internal/admin` from `internal/platformsettingshttp`; check `ident.Role == admin.RolePlatformAdmin` |
| `adminCanAccessTenant` helper | `internal/gatewayhttp/admin_cache_l2_handler.go:128-132` | Copy (not import — frozen package) into `internal/platformsettingshttp/handler.go` or inline; platform_settings is global so no tenant check needed |
| `MountAdminBillingSettingsRoutes` handler pattern | `internal/gatewayhttp/admin_billing_settings_handler.go:67-70` | Use as structural template for `MountPlatformSettingsRoutes` |
| `routeadmin.Service` audit pattern | `internal/routeadmin/service.go:13-17` | Use same nil-safe `audit AuditSink` optional-sink pattern |
| `InsertAdminAuditEvent` sqlc query | `sql/queries/admin_audit.sql:5-23` | Import existing `internal/db/admin.Queries.InsertAdminAuditEvent` from `platformsettings.Service` |
| `billing.PolicyResolver` TTL cache pattern | `internal/billing` (PolicyResolver) | Model 30s in-process cache on same sync.Map + time.Time expiry pattern |
| Audit CHECK extension pattern | `sql/migrations/0047_admin_audit_billing_setting_action.up.sql` | Use identical DROP CONSTRAINT / ADD CONSTRAINT pattern in migration 0077 |

---

## First Slice Specification

**Scope:** Store + service + HTTP handler + migration + audit extension. No integration of settings into existing services (that is slice 2). This slice delivers a fully functional admin CRUD surface.

### Files to ADD

```
internal/platformsettings/
    doc.go                    (~20 lines)  package doc + CMB citation
    types.go                  (~130 lines) SettingKey constants, Settings struct, defaults map, sentinel errors
    store.go                  (~35 lines)  Store interface (Get, List, Upsert)
    store_postgres.go         (~150 lines) PostgresStore wrapping db/platformsettings generated queries
    store_memory.go           (~90 lines)  MemoryStore for unit tests
    service.go                (~160 lines) Service: validate + write + emit audit + read with defaults + 30s TTL cache
    service_test.go           (~280 lines) discriminating unit tests against MemoryStore

internal/platformsettingshttp/
    doc.go                    (~15 lines)  package doc + CMB citation
    handler.go                (~270 lines) MountPlatformSettingsRoutes; GET list, GET single, PUT upsert
    handler_test.go           (~360 lines) discriminating handler tests via httptest

sql/queries/platform_settings.sql         (~40 lines) GetSetting, ListSettings, UpsertSetting
sql/migrations/0077_platform_settings.up.sql   (~70 lines) CREATE TABLE + audit CHECK extension
sql/migrations/0077_platform_settings.down.sql (~15 lines) DROP TABLE
```

### Files to EDIT

| File | Change |
|---|---|
| `cmd/gateway/routes.go` | Add `r.Route("/v1/admin/platform-settings", ...)` inside `mountAdminRoutes` |
| `cmd/gateway/wiring.go` | Wire `platformsettings.NewPostgresStore` + `platformsettings.NewService` into `deps` |
| `sqlc.yaml` | Add `internal/db/platformsettings` output block |

### SQL Schema (migration 0077)

```sql
-- 0077_platform_settings.up.sql

BEGIN;

CREATE TABLE IF NOT EXISTS platform_settings (
    id            bigserial    PRIMARY KEY,
    scope         text         NOT NULL DEFAULT 'global',
    setting_key   text         NOT NULL,
    setting_value text         NOT NULL,
    updated_at    timestamptz  NOT NULL DEFAULT now(),
    updated_by    text         NOT NULL,
    UNIQUE (scope, setting_key),
    CHECK (scope        <> ''),
    CHECK (setting_key  <> ''),
    CHECK (setting_value <> '')
);

CREATE INDEX IF NOT EXISTS idx_platform_settings_scope
    ON platform_settings (scope, setting_key);

COMMENT ON TABLE platform_settings IS
    '0077 (2026-06-03): Platform-wide runtime-mutable admin settings. scope=global in v1. '
    'Fail-closed defaults apply when key is absent. No credential material stored here.';

-- Extend admin_audit_events CHECK constraints to allow platform setting audit rows.
-- Pattern established by 0047_admin_audit_billing_setting_action.up.sql.
ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_action_check,
    ADD CONSTRAINT admin_audit_events_action_check
        CHECK (action IN
            ('issue_api_key', 'revoke_api_key', 'list_api_keys',
             'issue_admin_token', 'revoke_admin_token', 'admin_login',
             'create_provider_account', 'disable_provider_account',
             'enable_provider_account', 'delete_provider_account',
             'create_account_credential', 'rotate_account_credential',
             'disable_account_credential', 'delete_account_credential',
             'list_account_credentials',
             'credential_acquisition_started', 'credential_acquisition_completed',
             'credential_acquisition_failed', 'credential_acquisition_cancelled',
             'update_billing_settings',
             'create_pool_group', 'update_pool_group',
             'upsert_platform_setting'));

ALTER TABLE admin_audit_events
    DROP CONSTRAINT IF EXISTS admin_audit_events_target_type_check,
    ADD CONSTRAINT admin_audit_events_target_type_check
        CHECK (target_type IN
            ('api_key', 'admin_token', 'tenant', 'user',
             'provider_account', 'account_credential',
             'billing_setting', 'pool_group',
             'platform_setting'));

COMMIT;
```

```sql
-- 0077_platform_settings.down.sql
BEGIN;
DROP TABLE IF EXISTS platform_settings;
-- Note: audit CHECK constraints are not rolled back (append-only invariant;
-- removing 'upsert_platform_setting' would break existing audit rows).
COMMIT;
```

### SQLC Queries (`sql/queries/platform_settings.sql`)

```sql
-- name: GetPlatformSetting :one
SELECT id, scope, setting_key, setting_value, updated_at, updated_by
FROM platform_settings
WHERE scope = sqlc.arg(scope)::text AND setting_key = sqlc.arg(setting_key)::text;

-- name: ListPlatformSettings :many
SELECT id, scope, setting_key, setting_value, updated_at, updated_by
FROM platform_settings
WHERE scope = sqlc.arg(scope)::text
ORDER BY setting_key;

-- name: UpsertPlatformSetting :one
INSERT INTO platform_settings (scope, setting_key, setting_value, updated_by)
VALUES (sqlc.arg(scope)::text, sqlc.arg(setting_key)::text, sqlc.arg(setting_value)::text, sqlc.arg(updated_by)::text)
ON CONFLICT (scope, setting_key)
DO UPDATE SET setting_value = EXCLUDED.setting_value,
              updated_at    = now(),
              updated_by    = EXCLUDED.updated_by
RETURNING id, scope, setting_key, setting_value, updated_at, updated_by;
```

All column names (`scope`, `setting_key`, `setting_value`, `updated_at`, `updated_by`) are verified against the migration above. The `billing_settings` table uses the same column pattern (`sql/queries/billing_settings.sql:5-7`) as a reference.

### Key type signatures

```go
// internal/platformsettings/types.go

type SettingKey string

const (
    KeyRegistrationEnabled  SettingKey = "registration_enabled"
    KeyInvitationRequired   SettingKey = "invitation_required"
    KeyCaptchaEnabled       SettingKey = "captcha_enabled"
    KeyCaptchaProvider      SettingKey = "captcha_provider"
    KeyCaptchaSiteKey       SettingKey = "captcha_site_key"
    KeyOAuthProvidersEnabled SettingKey = "oauth_providers_enabled"
    KeyPromoEnabled         SettingKey = "promo_enabled"
    KeyStreamTimeoutSeconds SettingKey = "stream_timeout_seconds"
    KeyCooldown429Seconds   SettingKey = "cooldown_429_seconds"
    KeyCooldown529Seconds   SettingKey = "cooldown_529_seconds"
)

// defaultValues maps each key to its fail-closed default.
// Keys absent from platform_settings return this value with source="default".
var defaultValues = map[SettingKey]string{
    KeyRegistrationEnabled:   "false",
    KeyInvitationRequired:    "true",
    KeyCaptchaEnabled:        "false",
    KeyCaptchaProvider:       "",
    KeyCaptchaSiteKey:        "",
    KeyOAuthProvidersEnabled: "",
    KeyPromoEnabled:          "false",
    KeyStreamTimeoutSeconds:  "120",
    KeyCooldown429Seconds:    "60",
    KeyCooldown529Seconds:    "300",
}

var AllowedKeys = map[SettingKey]struct{}{ /* populated from above */ }

var (
    ErrUnknownKey        = errors.New("platformsettings: unknown setting key")
    ErrInvalidValue      = errors.New("platformsettings: invalid value for key")
    ErrStoreNotConfigured = errors.New("platformsettings: store not configured")
)

type StoredSetting struct {
    Key       SettingKey
    Value     string
    UpdatedAt time.Time
    UpdatedBy string
    Source    string // "db" or "default"
}

// internal/platformsettings/store.go
type Store interface {
    Get(ctx context.Context, scope, key string) (StoredSetting, bool, error)
    List(ctx context.Context, scope string) ([]StoredSetting, error)
    Upsert(ctx context.Context, scope, key, value, updatedBy string) (StoredSetting, error)
}

// internal/platformsettings/service.go
type AuditSink interface {
    WriteAdminAudit(ctx context.Context, params AuditParams) error
}

type AuditParams struct {
    ActorID   string
    ActorRole string
    Key       SettingKey
    OldValue  string
    NewValue  string
    Reason    string
    RequestID string
}

type Service struct {
    store     Store
    audit     AuditSink // nil-safe
    cache     sync.Map  // key: SettingKey -> cachedEntry{value, expiry}
    cacheTTL  time.Duration
    now       func() time.Time
}
```

### Route mounting in `cmd/gateway/routes.go`

Inside `mountAdminRoutes`, after the existing `tlsfphttp` block:

```go
r.Route("/v1/admin/platform-settings", func(r chi.Router) {
    platformsettingshttp.MountPlatformSettingsRoutes(r, platformsettingshttp.Deps{
        Auth:    d.adminAuth,
        Service: d.platformSettingsService,
    })
})
```

`d.platformSettingsService` is a `*platformsettings.Service` added to the `deps` struct in `wiring.go`.

---

## Discriminating Tests

Each test is designed so the exact code mutation described is the **only** mutation that makes it go red.

### `internal/platformsettings/service_test.go`

| Test name | Mutation that makes it red |
|---|---|
| `TestService_Get_absentKeyReturnsFailClosedDefault` | Change `defaultValues[KeyRegistrationEnabled]` from `"false"` to `"true"` |
| `TestService_Get_absentKeySourceIsDefault` | Remove `source = "default"` assignment in `Get` when row is absent |
| `TestService_Get_presentKeySourceIsDB` | Change `source = "db"` to `source = "default"` when row exists |
| `TestService_Upsert_unknownKeyRejected` | Remove `ErrUnknownKey` guard in `Upsert` validation |
| `TestService_Upsert_emptyValueRejected` | Remove empty-string check before forwarding to store |
| `TestService_Upsert_boolKeyInvalidValueRejected` | Remove bool-string validation for `registration_enabled` |
| `TestService_Upsert_intKeyNonPositiveRejected` | Remove `> 0` check for `stream_timeout_seconds` |
| `TestService_Upsert_auditSinkCalledOnSuccess` | Remove `s.audit.WriteAdminAudit(...)` call in `Upsert` |
| `TestService_Upsert_auditOldValueCaptured` | Remove `oldValue` fetch before upsert in `Upsert` |
| `TestService_NilService_returnsStoreNotConfigured` | Remove `s == nil` guard at top of each method |
| `TestService_List_includesAbsentKeysWithDefaults` | Remove logic that merges absent keys with defaults in `List` |
| `TestService_Cache_hitSkipsStore` | Remove cache lookup in `Get`; store call count becomes 2 instead of 1 |

### `internal/platformsettingshttp/handler_test.go`

| Test name | Mutation that makes it red |
|---|---|
| `TestHandler_GET_list_tenantOperatorGets403` | Change RBAC check from `!= RolePlatformAdmin` to `!= RolePlatformAdmin && != RoleTenantOperator` |
| `TestHandler_PUT_tenantOperatorGets403` | Same mutation as above on PUT path |
| `TestHandler_PUT_unknownKeyGets400` | Remove key-in-allowlist check in handler before calling service |
| `TestHandler_PUT_missingValueFieldGets400` | Remove check for empty `value` in request body parsing |
| `TestHandler_PUT_nilServiceReturns503` | Remove `if d.Service == nil` guard at handler entry |
| `TestHandler_GET_single_absentKeyReturns200WithDefault` | Change absent-key path to return 404 instead of 200 |
| `TestHandler_PUT_reasonOptional` | Add `if req.Reason == ""` rejection block |
| `TestHandler_PUT_largeBodyRejectedWith413` | Remove `http.MaxBytesReader` wrapper on request body (64 KiB limit) |
| `TestHandler_GET_list_returnsAllDefinedKeys` | Remove merge of absent keys with defaults in handler response assembly |

---

## Slice 2 (deferred integrations — implement after slice 1 is merged)

1. **Registration/invitation wiring:** Change `buildUserServices` in `cmd/gateway/lifecycle.go` to accept a `*platformsettings.Service`; read `KeyRegistrationEnabled` and `KeyInvitationRequired` at startup and set `userAuthService.RegistrationMode` accordingly. Add a background goroutine or request-time re-read with TTL.

2. **Stream timeout wiring:** In `cmd/gateway/middleware.go:buildStreamForwarder`, if `platformSettingsService != nil`, call `Get(KeyStreamTimeoutSeconds)` and override the env-var default.

3. **Cooldown wiring:** Add a `platformsettings.Service` dependency to `ModelCooldownService`; call `Get(KeyCooldown429Seconds)` in `RecordModelRateLimit` as the fallback duration instead of `defaultModelCooldownDuration`.

4. **Promo wiring:** Add an `EnabledChecker` interface to `internal/voucher`; `platformsettings.Service` satisfies it; `voucher.Service.Create` checks before issuing.

5. **OAuth enabled-list wiring:** Gate `StartOAuth` in `userauth.Service` against `KeyOAuthProvidersEnabled`.

---

## Invariants honored

- **CMB-5:** `platform_settings` stores only non-secret config. Audit `WriteAdminAudit` writes `old_value`/`new_value` only for non-sensitive keys; `captcha_site_key` is public. No credential material (OAuth secrets, SMTP passwords) enters this table.
- **CMB-7:** `platformsettings.Service` writes only to `platform_settings` and `admin_audit_events`. Never to billing, pool, registry, api_keys, or admin_tokens.
- **CMB-1:** `internal/platformsettings` and `internal/platformsettingshttp` do NOT import `internal/router`, `internal/gateway`, `internal/gatewayhttp`, or `internal/proto`.
- **Frozen packages:** No new files added to `internal/gatewayhttp`, `internal/gateway`, `internal/proto`. Only existing files in `cmd/gateway/routes.go` and `cmd/gateway/wiring.go` are edited.
- **Modularity:** All hand-written files are under 500 lines; all functions under 80 lines.
