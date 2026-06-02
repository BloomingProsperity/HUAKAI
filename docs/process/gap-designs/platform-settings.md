# Gap Design: Admin Platform-Settings Consolidation

**Gap ID:** platform-settings  
**Status:** Ready for implementation  
**Author:** Senior HUAKAI Backend Architect (2026-06-03)  
**Migration:** 0077 (current max is 0076)

---

## Summary

HUAKAI currently has two isolated runtime-config surfaces:

| Existing surface | Location | Scope |
|---|---|---|
| `email_settings` | `internal/email` + `gatewayhttp` | per-tenant SMTP/delivery config |
| `billing_settings` | `sql/queries/billing_settings.sql` + `gatewayhttp` | per-tenant billing policy |
| Env-vars in `internal/config` | `config.go` | boot-time only, operator-only, no DB |

**What is missing:** a consolidated, runtime-mutable, admin-only settings table that governs platform-wide behavior toggles: registration on/off, invitation-code requirement, Turnstile/CAPTCHA configuration, per-provider OAuth enablement, promo (voucher) enablement, stream timeout, and 429/529 cooldown. These settings must be readable at request time (hot path — read-only cache OK) and writable only by `platform_admin` operators via the admin API.

This design introduces the new package `internal/platformsettings` (service + store) and its HTTP façade `internal/platformsettingshttp`, backed by a single new table `platform_settings` (migration 0077) keyed by `(scope, setting_key)` where `scope` is always `"global"` in v1 (tenant-scoped expansion is a later slice).

**Behavioral parity goal:** match the runtime-toggle surface of reference projects' admin panel configuration, including fail-closed defaults for all security-sensitive flags (registration off when key absent, Turnstile required when configured, OAuth disabled when not explicitly enabled).

---

## Package layout

New packages only — no files added to frozen packages (`internal/gatewayhttp`, `internal/gateway`, `internal/proto`).

```
internal/platformsettings/         (NEW package)
    doc.go                         ~20 lines  — package doc + CMB citation
    types.go                       ~120 lines — SettingKey constants, Settings struct, sentinel errors
    store.go                       ~40 lines  — Store interface
    store_postgres.go              ~160 lines — PostgresStore: Get/List/Upsert via db.DBTX
    store_memory.go                ~100 lines — MemoryStore for unit tests
    service.go                     ~170 lines — Service: validate + write + emit audit; read with defaults
    service_test.go                ~300 lines — discriminating unit tests (memory store)

internal/platformsettingshttp/     (NEW package)
    doc.go                         ~15 lines  — package doc + CMB citation
    handler.go                     ~280 lines — MountPlatformSettingsRoutes; GET list, GET single, PUT upsert
    handler_test.go                ~380 lines — discriminating handler tests (httptest)
```

All hand-written files are under 500 lines; functions are under 80 lines. No god-files.

SQL artifacts (generated, not hand-written):

```
sql/queries/platform_settings.sql  ~40 lines  — GetSetting, ListSettings, UpsertSetting queries
internal/db/platformsettings/      (NEW generated sub-package, created by sqlc)
    db.go                          — sqlc boilerplate
    querier.go                     — Querier interface
    platform_settings.sql.go       — generated query implementations
```

Migration:

```
sql/migrations/0077_platform_settings.up.sql    ~50 lines
sql/migrations/0077_platform_settings.down.sql  ~10 lines
```

**Line-count confirmation:** every hand-written source file is planned under 500 lines (the largest, `service_test.go` at ~300 lines, is well within limit). The generated `platform_settings.sql.go` is expected to be ~80 lines.

---

## Schema / migrations

### Migration 0077 — `platform_settings` table

**File:** `sql/migrations/0077_platform_settings.up.sql`

```sql
-- 0077_platform_settings.up.sql
--
-- Consolidated platform-wide runtime toggles for admin configuration.
-- Scope 'global' is the only value in v1; tenant-scoped settings are a
-- future slice. All security-sensitive keys default to the restrictive
-- posture when absent (fail-closed): registration=off, captcha=required,
-- invitation_required=true, promo=disabled.
--
-- Credentials NEVER stored here (CMB invariant). OAuth client secrets
-- live in credentialstore; this table stores only non-secret config
-- (enabled flag, provider name, scope strings).

BEGIN;

CREATE TABLE IF NOT EXISTS platform_settings (
    id           bigserial    PRIMARY KEY,
    scope        text         NOT NULL DEFAULT 'global',
    setting_key  text         NOT NULL,
    setting_value text        NOT NULL,
    updated_at   timestamptz  NOT NULL DEFAULT now(),
    updated_by   text         NOT NULL,
    UNIQUE (scope, setting_key),
    CHECK (scope  <> ''),
    CHECK (setting_key <> ''),
    CHECK (setting_value <> '')
);

CREATE INDEX IF NOT EXISTS idx_platform_settings_scope
    ON platform_settings (scope, setting_key);

COMMENT ON TABLE platform_settings IS
    '0077 (2026-06-03): Platform-wide runtime-mutable admin settings. '
    'scope=global for v1. Fail-closed defaults apply when key is absent. '
    'No credential material stored here — secrets live in credentialstore.';

COMMIT;
```

**File:** `sql/migrations/0077_platform_settings.down.sql`

```sql
BEGIN;
DROP TABLE IF EXISTS platform_settings;
COMMIT;
```

### Defined setting keys (enforced in `types.go`)

| `setting_key` | Type | Default (absent) | Description |
|---|---|---|---|
| `registration_enabled` | bool string `"true"/"false"` | `"false"` (fail-closed) | Allow new user registration |
| `invitation_required` | bool string | `"true"` (fail-closed) | Registration requires a valid invitation code |
| `captcha_enabled` | bool string | `"false"` | Whether Turnstile/CAPTCHA gate is active |
| `captcha_provider` | `"turnstile"` or `""` | `""` | Which CAPTCHA provider is configured |
| `captcha_site_key` | string | `""` | Public site key (non-secret, safe to store) |
| `oauth_providers_enabled` | comma-separated provider names | `""` | Which OAuth login providers are active (e.g. `"cursor,windsurf"`) |
| `promo_enabled` | bool string | `"false"` | Whether promotional voucher flows are active |
| `stream_timeout_seconds` | int string | `"120"` | Maximum stream duration before server-side close |
| `cooldown_429_seconds` | int string | `"60"` | Back-off duration after upstream 429 |
| `cooldown_529_seconds` | int string | `"300"` | Back-off duration after upstream 529 (overloaded) |

**Invariant:** `captcha_site_key` is public (handed to the browser). The corresponding secret key lives in `credentialstore` and is NEVER stored in `platform_settings`. The handler rejects any write attempt to keys not in the allow-list above (`ErrUnknownKey`).

---

## Endpoints

All endpoints require `platform_admin` role (checked via `admin.AdminIdentity.Role == admin.RolePlatformAdmin`). A `tenant_operator` is rejected with 403.

| Method | Path | Auth scope | Description |
|---|---|---|---|
| `GET` | `/v1/admin/platform-settings` | `platform_admin` | List all current platform settings with their values and `updated_at` |
| `GET` | `/v1/admin/platform-settings/{key}` | `platform_admin` | Fetch a single setting by key; returns the default value when absent |
| `PUT` | `/v1/admin/platform-settings/{key}` | `platform_admin` | Upsert a single setting; validates key is in allow-list and value is well-formed |

### Request / Response shapes

**GET `/v1/admin/platform-settings`**

```json
{
  "settings": [
    {
      "key": "registration_enabled",
      "value": "false",
      "updated_at": "2026-06-03T00:00:00Z",
      "updated_by": "admin_token:42"
    }
  ]
}
```

**GET `/v1/admin/platform-settings/{key}`**

```json
{
  "setting": {
    "key": "registration_enabled",
    "value": "false",
    "updated_at": null,
    "source": "default"
  }
}
```

`source` is `"db"` when the row exists, `"default"` when the key is absent (value is the fail-closed default). This allows callers to distinguish "explicitly set to false" from "never configured".

**PUT `/v1/admin/platform-settings/{key}`**

Request body:

```json
{ "value": "true", "reason": "enabling registration for launch" }
```

Response (200 OK):

```json
{
  "setting": {
    "key": "registration_enabled",
    "value": "true",
    "updated_at": "2026-06-03T01:00:00Z",
    "updated_by": "admin_token:42"
  }
}
```

`reason` is optional; it is recorded in `admin_audit_events.reason` but never in `setting_value`.

### Route mounting in `cmd/gateway/routes.go`

Added inside `mountAdminRoutes`:

```go
r.Route("/v1/admin/platform-settings", func(r chi.Router) {
    platformsettingshttp.MountPlatformSettingsRoutes(r, platformsettingshttp.Deps{
        Auth:    d.adminAuth,
        Service: d.platformSettingsService,
    })
})
```

`d.platformSettingsService` is a `*platformsettings.Service` wired in `cmd/gateway/wiring.go`.

---

## Invariants honored

### CMB-1 — hot path isolation
`internal/platformsettings` and `internal/platformsettingshttp` do NOT import `internal/router`, `internal/auth`, `internal/gateway`, `internal/gatewayhttp`, or `internal/proto`. The hot request path never blocks on settings writes.

### CMB-5 — no credential leakage
`platform_settings` stores only public/non-secret config values. The CAPTCHA secret key, OAuth client secrets, etc. are explicitly excluded from the allowed key set. Any PUT attempt with a key containing `secret`, `password`, or `key_hash` (or any key not in the explicit allow-list) is rejected with `ErrUnknownKey` → 400. Audit events do NOT echo the `setting_value` in the `payload` jsonb when the key name suggests sensitivity (defense-in-depth, though all allowed keys are non-secret by design).

### CMB-7 — write scope
`platformsettings.Service` writes only to `platform_settings` and `admin_audit_events`. It never writes to `billing`, `pool`, `registry`, `api_keys`, or `admin_tokens` tables.

### Fail-closed defaults
Every security-relevant key returns a restrictive default when absent from the DB:
- `registration_enabled` → `false`
- `invitation_required` → `true`
- `captcha_enabled` → `false` (CAPTCHA off by default, operator must explicitly enable)
- `promo_enabled` → `false`
- `oauth_providers_enabled` → `""` (no providers active)

This means a fresh deployment with an empty `platform_settings` table is safe: registration is closed, no promos run, no unintended OAuth flows.

### Audit trail
Every `PUT` produces an `admin_audit_events` row (action = `platform_setting.upsert`, target_type = `platform_setting`, target_id = NULL, payload = `{"key":"...", "old_value":"...", "new_value":"..."}`, reason from request). Read operations are not audited (read-only, no state mutation). Audit write is best-effort (non-blocking on audit failure); failure is logged but does not roll back the settings write, matching the pattern in `routeadmin.Service`.

### Modular decomposition
The reference projects (based on behavioral analysis) tend to bundle all platform config into a single large settings service. HUAKAI decomposes: the store layer (`store.go` / `store_postgres.go`) is purely persistence; the service layer (`service.go`) handles validation + defaults + audit; the HTTP layer (`handler.go`) handles HTTP serialization only. No function exceeds 80 lines.

---

## Discriminating tests

Each test is named and described so it fails if — and only if — the specific defect it guards against is introduced.

### `internal/platformsettings/service_test.go`

| Test name | Defect it catches |
|---|---|
| `TestService_Get_absentKeyReturnsFailClosedDefault` | Service returns non-fail-closed value (e.g. `true`) for `registration_enabled` when DB has no row |
| `TestService_Get_absentCaptchaKeyDefaultsFalse` | `captcha_enabled` default changed from `false` to `true` |
| `TestService_Upsert_unknownKeyRejected` | `ErrUnknownKey` guard removed, arbitrary keys accepted |
| `TestService_Upsert_emptyValueRejected` | Empty `setting_value` passes validation and reaches the store |
| `TestService_Upsert_boolKeyInvalidValueRejected` | Boolean key accepts non-bool string (e.g. `"yes"`) |
| `TestService_Upsert_intKeyNegativeRejected` | `stream_timeout_seconds = "-1"` accepted silently |
| `TestService_Upsert_intKeyZeroRejected` | `stream_timeout_seconds = "0"` accepted (semantically a no-op timeout) |
| `TestService_Upsert_auditEventEmittedOnSuccess` | Audit sink not called after successful upsert |
| `TestService_Upsert_settingValueNotEchoedInAuditSensitiveField` | `setting_value` written into audit `payload.secret` field |
| `TestService_NilStore_returnsSentinel` | Nil store panics instead of returning `ErrStoreNotConfigured` |
| `TestService_List_returnsAllWithDefaults` | List returns DB rows but omits keys that have defaults and are absent (should include all defined keys with their source) |

### `internal/platformsettingshttp/handler_test.go`

| Test name | Defect it catches |
|---|---|
| `TestHandler_GET_list_tenantOperatorGets403` | `tenant_operator` role allowed to read platform settings |
| `TestHandler_PUT_tenantOperatorGets403` | `tenant_operator` role allowed to write platform settings |
| `TestHandler_PUT_unknownKeyGets400` | Unknown key accepted and forwarded to service |
| `TestHandler_PUT_missingValueGets400` | Empty body or missing `value` field silently uses zero-value |
| `TestHandler_PUT_nilDepsReturns503` | Nil deps panic instead of 503 |
| `TestHandler_GET_single_absentKeyReturnsDefaultNotNull` | Absent key returns 404 instead of 200 with `source:"default"` |
| `TestHandler_PUT_reasonNotRequired` | Request without `reason` field rejected with 400 |
| `TestHandler_PUT_largeBodyRejectedWith413` | Body larger than 64 KiB accepted (request body limit not enforced) |

---

## Parity-or-better vs reference

The following behaviors are derived from behavioral analysis of reference project admin panels (referenced as `fusion-upgrade` clean-room decomposition per Owner rule). No source code is copied; behaviors are re-implemented from first principles.

| Reference behavior | HUAKAI implementation | File:line (behavioral cite) |
|---|---|---|
| Registration toggle: admin can enable/disable user registration at runtime without restart | `setting_key = "registration_enabled"`, read by auth layer on each registration attempt | Ref: admin settings → `registrationEnabled` flag checked in signup handler |
| Invitation-code gate: when enabled, `/v1/auth/register` rejects requests without a valid code | `invitation_required` setting; existing `internal/community/invitation` service already validates codes; signup handler reads this flag | Ref: `invitationOnlyRegistration` boolean setting |
| CAPTCHA/Turnstile: site key served to frontend; validation on backend before registration | `captcha_enabled` + `captcha_site_key` (public) stored; secret key in `credentialstore` (not here); verification is a future slice that reads these settings | Ref: Turnstile site-key in admin config, server-side verify call |
| OAuth provider toggle: per-provider enable/disable without restart | `oauth_providers_enabled` comma-list; OAuth handlers check list membership before initiating flow | Ref: per-provider `enabled` flag in OAuth config section |
| Promo enablement: admin can turn promotional voucher flows on/off | `promo_enabled`; `voucher.Service` checks this setting before issuing promo vouchers | Ref: `promoEnabled` admin toggle |
| Stream timeout: admin-configurable maximum stream duration | `stream_timeout_seconds`; gateway stream handler reads this at start of each request (replaces hard-coded env-var default) | Ref: `streamTimeoutSecs` in runtime config panel |
| 429 cooldown: configurable back-off duration after upstream 429 | `cooldown_429_seconds`; `internal/rate` ModelCooldownService reads this at startup/cache-reload | Ref: `rateLimitCooldownSecs` in admin config |
| 529 cooldown: configurable back-off for upstream overload | `cooldown_529_seconds`; same cooldown service, separate key | Ref: `overloadCooldownSecs` |

**Parity notes:**

- HUAKAI decomposes the monolithic reference settings blob into per-domain keys with explicit validation, improving maintainability (Owner modularity rule).
- HUAKAI adds `source: "default"` on single-key GET — reference projects return 404 for absent keys, which forces clients to handle 404 as "use default". Our approach is strictly better: clients know the effective value without needing fallback logic.
- HUAKAI stores OAuth client secrets in `credentialstore` rather than in the settings table, hardening the credential boundary that reference projects sometimes blurred.

---

## Effort

**M** (Medium)

Breakdown:
- Migration 0077: 0.5 day
- SQL queries + sqlc generation: 0.5 day
- `internal/platformsettings` (types + store + service + unit tests): 2 days
- `internal/platformsettingshttp` (handler + tests): 1.5 days
- Wiring in `cmd/gateway/wiring.go` + `routes.go`: 0.5 day
- Integration of `registration_enabled` / `invitation_required` into `gatewayhttp` auth routes (reading from service): 1 day (modifying existing files, not frozen packages)
- Integration of `stream_timeout_seconds` into gateway stream handler: 0.5 day
- Integration of `cooldown_*_seconds` into `internal/rate`: 0.5 day

Total: ~7 days

---

## Risks

### R1 — Hot-path read latency
If `gatewayhttp` reads `platformsettings.Service.Get()` on every request (e.g. for stream timeout), a slow DB query adds per-request latency. **Mitigation:** `Service` wraps reads in a short-lived in-process cache (TTL 30s) using a `sync.Map` + `time.Time` expiry. Cache is invalidated (TTL reset) on every successful Upsert. This is the same pattern as `billing.PolicyResolver`. Cache must be fail-open for read (return stale value rather than block) and fail-closed for security flags (registration, captcha): those defaults are already the restrictive value, so a stale `false` is safe.

### R2 — Schema CHECK constraint vs future keys
The current migration uses no DB-level CHECK on allowed `setting_key` values (unlike `billing_settings` which CHECKs `stream_input_only_interrupted_policy`). This is intentional: the allow-list is enforced in the service layer where it is easy to extend. Adding a DB CHECK constraint for each key would require a migration per new key. **Mitigation:** service-layer `ErrUnknownKey` guard + the test `TestService_Upsert_unknownKeyRejected` ensures no bypass.

### R3 — Dual source of truth for stream timeout
Currently `stream_timeout_seconds` may also be expressed as a hard-coded constant in `gatewayhttp`. If both exist, the gateway may use the wrong one after migration. **Mitigation:** when integrating, remove the hard-coded constant and make the gateway read exclusively from `platformsettings.Service`; add a test that asserts the env-var path is gone.

### R4 — `cooldown_*` settings require service reload or cache expiry
`internal/rate.ModelCooldownService` currently reads cooldown durations at construction time from env-vars. Changing them to DB-backed values requires the cooldown service to re-read on each evaluation (or on TTL expiry). **Mitigation:** pass `platformsettings.Service` as a dependency to `ModelCooldownService`; it calls `Get()` per-evaluation (which is cache-backed at 30s TTL, not a live DB query). This is a pure dependency injection change — no frozen packages modified.

### R5 — `invitation_required` and `registration_enabled` are separate flags that can produce incoherent state
If `registration_enabled=true` and `invitation_required=true`, registration is open but gated by codes — coherent. If `registration_enabled=false` and `invitation_required=false`, registration is fully closed — also coherent. The only incoherent state is `registration_enabled=false` + `invitation_required=false` being interpreted as "open without codes", but since `registration_enabled=false` is the outer gate, the inner flag is irrelevant. **Mitigation:** document this in `types.go` and in the GET response description; no DB constraint needed.

### R6 — `platform_admin`-only write surface vs multi-tenant future
This design scopes `platform_settings` to `scope='global'` exclusively. If a future slice adds per-tenant platform toggles, the `scope` column allows it without a schema change. However, the current `PUT` endpoint does not accept a `scope` parameter. **Mitigation:** the column is present in the schema; the service and handler in v1 hardcode `scope="global"`. A future slice adds a `?scope=tenant:{id}` query param with a separate RBAC check.
