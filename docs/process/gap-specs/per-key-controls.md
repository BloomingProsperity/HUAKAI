# Gap Spec: Per-API-Key Controls

**Spec date:** 2026-06-03
**Author:** Residual-verification agent (Sonnet 4.6)
**Based on design:** docs/process/gap-designs/per-key-controls.md
**Status:** Verified — false premises corrected, first-slice ready

---

## 1. Verification Summary

Every premise in the gap design was checked against real code. The table below
lists confirmed facts (file:line), corrected false premises, and items that are
genuinely missing.

### 1.1 Confirmed correct premises

| Premise | Verified location |
|---------|------------------|
| Current migration max = 0076 | `sql/migrations/0076_user_role.up.sql` |
| `quota_policies` has composite UNIQUE `(tenant_id, id)` — FK target valid | `sql/migrations/0070_quota_subsystem.up.sql:47` |
| `quota_policies.limit_value` is `numeric(20,8)` | `sql/migrations/0070_quota_subsystem.up.sql:35` |
| `quota_policies.scope_kind` CHECK already includes `'api_key'` | `sql/migrations/0070_quota_subsystem.up.sql:16-19` |
| `WindowCalendarMonth` exists in quota engine | `internal/quota/types.go:46`, `internal/quota/rate_window.go:35` |
| `ScopeAPIKey = "api_key"` | `internal/quota/types.go:15` |
| `api_key_groups` table does NOT exist (needs creating) | migrations scan |
| `key_group_id` column does NOT exist on `api_keys` | `sql/migrations/0007_l0_inbound_auth.up.sql:51-70` |
| `quota_policy_id` column does NOT exist on `api_keys` | `sql/migrations/0007_l0_inbound_auth.up.sql:51-70` |
| `SessionIdentity` has NO `KeyGroupID` field | `internal/auth/session_middleware.go:15-21` |
| `LookupAPIKeysByPrefixRow` has NO `KeyGroupID` field | `internal/db/auth/auth_inbound.sql.go:77-87` |
| Advisory lock scheme `pg_advisory_xact_lock(hashtextextended($1::text,0))` exists | `internal/userkey/userkey.go:228-233` |
| `userkey.Service` has Issue/List/Get/Revoke only — no BatchRevoke | `internal/userkey/userkey.go:149-154` |
| No existing step-up / TOTP / WebAuthn framework | `internal/auth/session_middleware.go` (full file) |
| `userkeyhttp.MountUserAPIKeyRoutes` mounts at `/v1/api-keys` under SessionMiddleware | `cmd/gateway/routes.go:142-145` |
| `internal/{gatewayhttp,gateway,proto}` are frozen (no new files) | confirmed by package listing |

### 1.2 FALSE PREMISES in the design — corrections required

#### FP-1: "Uses quota engine's existing policy upsert path"

**Design claim:** `SetKeyQuota` uses the "quota engine's existing policy upsert path" /
"a thin `PolicyWriter` interface extracted from the existing `PGStore`."

**Reality:** There is NO upsert/insert/write path for `quota_policies` anywhere in the
quota engine. The `quotaQueries` interface (`internal/quota/pg_store_queries.go:9-31`)
has no `UpsertQuotaPolicy` or `InsertQuotaPolicy`. The `PGStore` interface
(`internal/quota/store.go:14-39`) has no policy-write method. The `quota.sql` file
(`sql/queries/quota.sql`) has no INSERT/UPDATE for `quota_policies`.

**Correction:** `userkeycontrols` must add its own new sqlc query
`UpsertKeyQuotaPolicy` directly to `quota_policies` via a new query file
`sql/queries/userkeycontrols.sql`. This query is NOT extracted from an existing
path — it is net-new. The `internal/db/quota` package must be regenerated (or the
new query placed in a new db package for `userkeycontrols`).

**Impact:** The "thin `PolicyWriter` interface" is not extracted from an existing
store — it is a new interface with a new sqlc query. Effort is the same; the
description is wrong.

#### FP-2: `subscription_policy_links` uses a HARD FK to `quota_policies`

**Design claim:** The `api_keys.quota_policy_id FK → quota_policies` follows "existing
pattern." The closest existing pattern is `subscription_policy_links.quota_policy_id`
(0073), which explicitly uses a **soft reference** ("软引用, 不硬 FK 跨包耦合") —
no FK, just a bigint column with a comment.

**Reality:** `subscription_policy_links.quota_policy_id` at
`sql/migrations/0073_subscription.up.sql:110` is NOT a hard FK. The design proposes
a DEFERRABLE FK from `api_keys.quota_policy_id` to `quota_policies`, which is
**stronger coupling** than the subscription pattern. This is architecturally valid
but is NOT following "existing pattern" — it is a deliberate upgrade in coupling
strength. Implementors must be aware of this difference.

#### FP-3: `TestPutQuota_MissingSession_503` test description is self-contradicting

**Design claim:** Test `TestPutQuota_MissingSession_503` "expects 503" then
immediately self-corrects with "Correction: expects 401."

**Reality:** The handler returns 401 (`writeError(w, http.StatusUnauthorized,
"session_required", ...)`) as implemented in `internal/userkeyhttp/handlers.go:239`.
The test must assert 401, not 503. The "Correction" note in the design is correct;
the test name is misleading. Rename to `TestPutQuota_MissingSession_401`.

---

## 2. True Residual (genuinely missing features)

All four sub-features are genuinely absent:

1. **Per-key quota cap** — no `quota_policy_id` column on `api_keys`, no
   admin-write path for `quota_policies` scoped to `api_key`. The quota engine
   hot-path (`internal/quota/service.go`) already supports `ScopeAPIKey` policies
   once rows exist.

2. **Key group assignment** — no `api_key_groups` table, no `key_group_id` column,
   no `KeyGroupID` in `SessionIdentity` or `LookupAPIKeysByPrefixRow`.

3. **Batch revoke** — `userkey.Service` has only single-key `Revoke`. No batch
   path exists anywhere.

4. **Secure reveal token** — no `api_key_reveal_tokens` table, no reveal endpoint,
   no step-up challenge infrastructure.

---

## 3. Reuse Points (existing code to reuse, file:line)

| Reuse point | File:line | How reused |
|------------|-----------|-----------|
| `quota.PGStore.ListActivePolicies` + `ResolvePolicies` | `internal/quota/store.go:14`, `internal/quota/policy.go:33` | SetKeyQuota reads existing policy via same path used by Reserve |
| Advisory xact lock scheme | `internal/userkey/userkey.go:228-233` | BatchRevoke acquires same `"userkey:tenantID:userID"` per-key lock before each revoke |
| `userkey.Service.Revoke` | `internal/userkey/userkey.go:440-505` | BatchRevoke calls existing single-key Revoke in a loop (no need to duplicate revoke logic) |
| `SessionMiddleware` + `SessionFromContext` | `internal/auth/session_middleware.go:42`, `:31` | All new handlers read `SessionIdentity` via `SessionFromContext` — no new session infrastructure |
| `userkeyhttp.resolveSession` pattern | `internal/userkeyhttp/handlers.go:233-244` | New handlers copy same nil-service guard + 401 pattern |
| `quota_policies` composite UNIQUE `(tenant_id, id)` | `sql/migrations/0070_quota_subsystem.up.sql:47` | FK from `api_keys.quota_policy_id` targets this existing composite |
| `ScopeAPIKey`, `MetricCostUSD`, `WindowCalendarDay`, `WindowCalendarMonth` | `internal/quota/types.go:15,32,43,46` | SetKeyQuota sets scope_kind=`api_key`, metric=`cost_usd`, window_kind as chosen |
| `bcrypt.GenerateFromPassword` + cost 10 | `internal/userkey/userkey.go:216` | RevealToken nonce hashing reuses same bcrypt cost |
| `shopspring/decimal` | `internal/quota/types.go:7` | SetKeyQuota.LimitUSD uses same decimal package |
| `chi.URLParam` pattern | `internal/userkeyhttp/handlers.go:247` | New handlers parse `{id}` path param same way |

---

## 4. Risk Classification

**riskClass: `schema`**

Rationale:
- Requires two new migrations (0077, 0078) adding columns and tables.
- No existing money-path write is modified. `SetKeyQuota` writes to `quota_policies`
  (admin config, not request hot-path).
- No changes to `internal/{billing,gateway,gatewayhttp,proto}`.
- BatchRevoke touches `api_keys` rows (status → revoked) but via the existing
  `userkey.Service.Revoke` lock-safe path.
- RevealToken is session-gated and writes to a new table only.

---

## 5. Parallelizable

**Yes.** The first slice (`userkeycontrols` + `userkeycontrolshttp` + migration 0077
+ quota policy write query) operates entirely in new files. The only shared-file
edit is `cmd/gateway/routes.go` (add the new mount call). An agent working the
first slice in an isolated worktree will not collide with other in-flight work
unless another agent is also editing routes.go simultaneously.

---

## 6. First Slice Specification

### 6.1 Scope

**Sub-features in first slice:** Per-key quota cap (PUT/GET `/v1/api-keys/{id}/quota`)
and Key group assignment (PUT/GET `/v1/api-keys/{id}/group`).

Batch revoke and reveal token are deferred to slice 2. Rationale: quota + group
are the commercially highest-value features (enable policy dispatch immediately),
share the same migration (0077), and have no dependency on 0078.

### 6.2 Migration 0077 (required)

**File:** `backend/sql/migrations/0077_api_key_controls.up.sql`

```sql
BEGIN;

-- 1. api_keys: group membership + quota policy link
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS key_group_id    bigint,
    ADD COLUMN IF NOT EXISTS quota_policy_id bigint;

-- 2. Table: api_key_groups
CREATE TABLE IF NOT EXISTS api_key_groups (
    id          bigserial   PRIMARY KEY,
    tenant_id   bigint      NOT NULL REFERENCES tenants(id),
    name        text        NOT NULL,
    description text        NOT NULL DEFAULT '',
    enabled     boolean     NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    deleted_at  timestamptz,
    CONSTRAINT uq_api_key_groups_tenant_id_id UNIQUE (tenant_id, id)
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_api_key_groups_tenant_name
    ON api_key_groups (tenant_id, name) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_api_key_groups_tenant_enabled
    ON api_key_groups (tenant_id, enabled) WHERE deleted_at IS NULL;
COMMENT ON TABLE api_key_groups IS
    '0077: named API key groups. key_group_id on api_keys; resolver stamps '
    'Identity.KeyGroupID. No bearer plaintext ever here (CMB-5).';

-- 3. FKs back on api_keys
ALTER TABLE api_keys
    ADD CONSTRAINT fk_api_keys_key_group
        FOREIGN KEY (tenant_id, key_group_id)
        REFERENCES api_key_groups (tenant_id, id)
        DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT fk_api_keys_quota_policy
        FOREIGN KEY (tenant_id, quota_policy_id)
        REFERENCES quota_policies (tenant_id, id)
        DEFERRABLE INITIALLY DEFERRED;

-- 4. Lookup indexes
CREATE INDEX IF NOT EXISTS idx_api_keys_group
    ON api_keys (tenant_id, key_group_id)
    WHERE deleted_at IS NULL AND key_group_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_api_keys_quota_policy
    ON api_keys (tenant_id, quota_policy_id)
    WHERE deleted_at IS NULL AND quota_policy_id IS NOT NULL;

COMMIT;
```

**File:** `backend/sql/migrations/0077_api_key_controls.down.sql`

```sql
BEGIN;
ALTER TABLE api_keys
    DROP CONSTRAINT IF EXISTS fk_api_keys_quota_policy,
    DROP CONSTRAINT IF EXISTS fk_api_keys_key_group;
ALTER TABLE api_keys
    DROP COLUMN IF EXISTS quota_policy_id,
    DROP COLUMN IF EXISTS key_group_id;
DROP TABLE IF EXISTS api_key_groups;
COMMIT;
```

### 6.3 New sqlc query for policy write

**File:** `backend/sql/queries/userkeycontrols.sql`

Because there is NO existing policy write path in `quota.sql` (FP-1 corrected),
add a new query file. This avoids mutating the quota package's generated code
by adding a new named query only used by `userkeycontrols`.

```sql
-- name: UpsertKeyQuotaPolicy :one
-- Admin write: create or update a quota_policies row scoped to a single api_key.
-- scope_kind must be 'api_key'; scope_id is the api_key id as text.
-- CMB-5: no bearer plaintext or key_hash referenced here.
-- Caller (userkeycontrols.SetKeyQuota) is NOT on the request hot-path.
INSERT INTO quota_policies (
    tenant_id,
    scope_kind,
    scope_id,
    metric,
    window_kind,
    window_seconds,
    limit_value,
    burst_value,
    mode,
    priority,
    enabled,
    valid_from,
    created_by_actor
)
VALUES (
    sqlc.arg(tenant_id)::bigint,
    'api_key',
    sqlc.arg(scope_id)::text,
    sqlc.arg(metric)::text,
    sqlc.arg(window_kind)::text,
    sqlc.arg(window_seconds)::integer,
    sqlc.arg(limit_value)::numeric(20,8),
    0::numeric(20,8),
    sqlc.arg(mode)::text,
    200,
    true,
    sqlc.arg(valid_from)::timestamptz,
    sqlc.arg(actor)::text
)
ON CONFLICT ON CONSTRAINT uq_quota_policies_live_scope_metric
DO UPDATE SET
    limit_value          = EXCLUDED.limit_value,
    mode                 = EXCLUDED.mode,
    window_kind          = EXCLUDED.window_kind,
    window_seconds       = EXCLUDED.window_seconds,
    valid_from           = EXCLUDED.valid_from,
    last_modified_by_actor = EXCLUDED.created_by_actor,
    updated_at           = NOW()
WHERE quota_policies.tenant_id = sqlc.arg(tenant_id)::bigint
RETURNING
    tenant_id,
    id,
    scope_kind,
    scope_id,
    metric,
    window_kind,
    window_seconds,
    limit_value,
    mode,
    priority,
    valid_from;

-- name: GetKeyQuotaPolicy :one
-- Read back the quota_policies row for a single api_key (for GET /v1/api-keys/{id}/quota).
SELECT
    tenant_id,
    id,
    scope_kind,
    scope_id,
    metric,
    window_kind,
    window_seconds,
    limit_value,
    mode,
    priority,
    enabled,
    valid_from,
    valid_until
FROM quota_policies
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND scope_kind = 'api_key'
  AND scope_id   = sqlc.arg(scope_id)::text
  AND enabled    = true
  AND valid_until IS NULL
ORDER BY priority ASC, id DESC
LIMIT 1;

-- name: SetAPIKeyGroupID :execrows
-- Set or clear the key_group_id on an api_keys row owned by (tenant_id, user_id).
UPDATE api_keys
SET key_group_id = sqlc.narg(key_group_id)::bigint,
    updated_at   = NOW()
WHERE id        = sqlc.arg(api_key_id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND user_id   = sqlc.arg(user_id)::bigint
  AND deleted_at IS NULL;

-- name: GetAPIKeyGroupID :one
-- Read the key_group_id (and group name if set) for a single api_keys row.
SELECT
    ak.id              AS api_key_id,
    ak.key_group_id,
    ag.name            AS group_name,
    ag.description     AS group_description,
    ag.enabled         AS group_enabled
FROM api_keys ak
LEFT JOIN api_key_groups ag
    ON ag.tenant_id  = ak.tenant_id
   AND ag.id         = ak.key_group_id
   AND ag.deleted_at IS NULL
WHERE ak.id        = sqlc.arg(api_key_id)::bigint
  AND ak.tenant_id = sqlc.arg(tenant_id)::bigint
  AND ak.user_id   = sqlc.arg(user_id)::bigint
  AND ak.deleted_at IS NULL;

-- name: ValidateGroupBelongsToTenant :one
-- Check that a group_id belongs to a tenant (for SetKeyGroup tenant isolation).
SELECT id, name, enabled
FROM api_key_groups
WHERE tenant_id = sqlc.arg(tenant_id)::bigint
  AND id        = sqlc.arg(group_id)::bigint
  AND deleted_at IS NULL;

-- name: SetAPIKeyQuotaPolicyID :execrows
-- Link or unlink api_keys.quota_policy_id after policy upsert.
UPDATE api_keys
SET quota_policy_id = sqlc.narg(quota_policy_id)::bigint,
    updated_at      = NOW()
WHERE id        = sqlc.arg(api_key_id)::bigint
  AND tenant_id = sqlc.arg(tenant_id)::bigint
  AND user_id   = sqlc.arg(user_id)::bigint
  AND deleted_at IS NULL;
```

**Important:** sqlc regen must run after adding this file. The generated package
goes to `internal/db/userkeycontrols/` (a new package, not modifying
`internal/db/quota/`). The sqlc.yaml needs a new codegen block targeting this
query file with output package `internal/db/userkeycontrols`.

### 6.4 New packages

#### `internal/userkeycontrols/` (new package, ~5 files in first slice)

Files to ADD (each well under 500 lines):

| File | Responsibility | Est. lines |
|------|---------------|-----------|
| `doc.go` | Package doc + CMB statement | ~20 |
| `errors.go` | Sentinel errors: `ErrQuotaPolicyNotFound`, `ErrGroupNotFound`, `ErrKeyNotFound`, `ErrServiceMisconfig` | ~40 |
| `types.go` | `SetKeyQuotaRequest`, `SetKeyQuotaResult`, `SetKeyGroupRequest`, `SetKeyGroupResult`, `KeyQuotaView`, `KeyGroupView` | ~80 |
| `quota.go` | `SetKeyQuota(ctx, req) (SetKeyQuotaResult, error)`, `GetKeyQuota(ctx, tenantID, userID, apiKeyID) (KeyQuotaView, error)` — writes `quota_policies` + links `api_keys.quota_policy_id` in a single Tx using `UpsertKeyQuotaPolicy` + `SetAPIKeyQuotaPolicyID` | ~180 |
| `group.go` | `SetKeyGroup(ctx, req) (SetKeyGroupResult, error)`, `GetKeyGroup(ctx, tenantID, userID, apiKeyID) (KeyGroupView, error)` — validates group tenant, calls `SetAPIKeyGroupID` / `GetAPIKeyGroupID` | ~160 |

Total first-slice domain: ~480 lines across 5 files.

Store interface for `userkeycontrols`:

```go
// Store is the minimum DB interface for userkeycontrols.
// Backed by internal/db/userkeycontrols generated code.
type Store interface {
    UpsertKeyQuotaPolicy(ctx context.Context, arg UpsertKeyQuotaPolicyParams) (KeyQuotaPolicyRow, error)
    GetKeyQuotaPolicy(ctx context.Context, tenantID int64, scopeID string) (KeyQuotaPolicyRow, error)
    SetAPIKeyGroupID(ctx context.Context, arg SetAPIKeyGroupIDParams) (int64, error)
    GetAPIKeyGroupID(ctx context.Context, tenantID, userID, apiKeyID int64) (APIKeyGroupRow, error)
    ValidateGroupBelongsToTenant(ctx context.Context, tenantID, groupID int64) (GroupRow, error)
    SetAPIKeyQuotaPolicyID(ctx context.Context, arg SetAPIKeyQuotaPolicyIDParams) (int64, error)
    WithTx(ctx context.Context, fn func(Store) error) error
}
```

#### `internal/userkeycontrolshttp/` (new package, ~5 files in first slice)

| File | Responsibility | Est. lines |
|------|---------------|-----------|
| `doc.go` | Package doc | ~20 |
| `mount.go` | `MountRoutes(r chi.Router, d Deps)`, `Deps` struct | ~50 |
| `quota_handler.go` | `PUT /v1/api-keys/{id}/quota`, `GET /v1/api-keys/{id}/quota` | ~120 |
| `group_handler.go` | `PUT /v1/api-keys/{id}/group`, `GET /v1/api-keys/{id}/group` | ~120 |
| `helpers.go` | `resolveSession`, `parsePathID`, `writeJSON`, `writeError`, `writeControlsError` | ~80 |

Total first-slice HTTP: ~390 lines across 5 files.

### 6.5 Existing files to EDIT

| File | Change | Constraint |
|------|--------|-----------|
| `cmd/gateway/routes.go` | Add `userkeycontrolshttp.MountRoutes(r, ...)` call inside the existing `/v1/api-keys` route group (or as an adjacent group with same SessionMiddleware). Add `d.userKeyControlsService` field to `deps` struct in `cmd/gateway/deps.go` | routes.go is shared; only one agent edits it |
| `internal/auth/session_middleware.go` | Add `KeyGroupID *int64` field to `SessionIdentity` struct | Safe additive — all existing callers ignore new field |
| `internal/db/auth/auth_inbound.sql.go` | Regenerated by sqlc after 0077 adds `key_group_id` to `api_keys`; `LookupAPIKeysByPrefixRow` gains `KeyGroupID *int64` | sqlc-managed; run `make sqlc` |
| `sql/queries/auth_inbound.sql` | Add `ak.key_group_id` to SELECT in `LookupAPIKeysByPrefix` | Additive column addition |

### 6.6 Discriminating tests

Each test must go red if the specific defect it defends is introduced.

#### `internal/userkeycontrols/` integration tests (`pg_integration_test.go`)

| Test name | Exact mutation that makes it go red |
|-----------|-------------------------------------|
| `TestSetKeyQuota_ScopeKindIsAPIKey` | Change `scope_kind` insert arg to `'user'` — test asserts `SELECT scope_kind FROM quota_policies WHERE id=$policyID` returns `'api_key'` |
| `TestSetKeyQuota_ScopeIDIsAPIKeyID` | Change scope_id to `strconv.FormatInt(userID, 10)` (wrong id) — test asserts scope_id equals `strconv.FormatInt(apiKeyID, 10)` |
| `TestSetKeyQuota_UpsertIdempotent_NoDuplicatePolicyRow` | Change upsert to INSERT-only (no ON CONFLICT) — second call creates second row; test asserts `COUNT(*) = 1` after two SetKeyQuota calls on same key |
| `TestSetKeyQuota_LimitUSD_DecimalPrecision` | Truncate `decimal.Decimal` to `float64` before bind — test sets `LimitUSD = "0.00000001"`, reads back exact string from DB |
| `TestSetKeyQuota_LinksQuotaPolicyIDOnAPIKey` | Remove `SetAPIKeyQuotaPolicyID` call from SetKeyQuota — test asserts `api_keys.quota_policy_id IS NOT NULL` after SetKeyQuota |
| `TestSetKeyGroup_RejectsWrongTenantGroup` | Remove tenant_id check in `ValidateGroupBelongsToTenant` query (remove `AND tenant_id = $1`) — cross-tenant group assignment succeeds; test expects `ErrGroupNotFound` |
| `TestSetKeyGroup_ClearsWithNilGroupID` | Change nil handling to set `key_group_id = 0` instead of NULL — test asserts `key_group_id IS NULL` after clearing |
| `TestGetKeyQuota_ReturnsNilIfNoPolicyLinked` | Hard-code a return instead of querying DB — test creates key with no quota, expects `ErrQuotaPolicyNotFound` |

#### `internal/userkeycontrolshttp/` handler unit tests

| Test name | Exact mutation that makes it go red |
|-----------|-------------------------------------|
| `TestPutQuota_MissingSession_401` | Remove `resolveSession` call — unauthenticated caller reaches service; test asserts 401 on missing Authorization header |
| `TestPutQuota_NegativeLimitUSD_400` | Remove `< 0` validation on LimitUSD — negative value reaches service; test sends `{"limit_usd": "-1"}`, expects 400 |
| `TestPutGroup_InvalidGroupID_400` | Remove `> 0` check on group_id — zero/negative group_id reaches service; test sends `{"group_id": -1}`, expects 400 |
| `TestGetQuota_ServiceNil_503` | Remove nil-service guard — nil pointer panic on call; test expects 503 when Deps.Service is nil |
| `TestGetGroup_UnknownKey_404` | Return 200 with empty body instead of 404 — test expects 404 on non-existent api_key_id |

---

## 7. sqlc.yaml update required

Add a new codegen stanza targeting `sql/queries/userkeycontrols.sql` with:
- `out: internal/db/userkeycontrols`
- `package: userkeycontrols`
- Same `pgx/v5` emit settings as existing quota stanza

Also add `ak.key_group_id` to `sql/queries/auth_inbound.sql`'s
`LookupAPIKeysByPrefix` SELECT, then regenerate `internal/db/auth/`.

---

## 8. Invariant compliance

| Invariant | Mechanism in first slice |
|-----------|-------------------------|
| CMB-5: no credentials logged | `userkeycontrols` never reads `key_hash`. `quota.go` and `group.go` log only `api_key_id`, `tenant_id`, `user_id` — no bearer fields |
| CMB-7: router reads no creds, writes nothing | `userkeycontrols` is NOT imported by `gateway`, `gatewayhttp`, or `proto`. `key_group_id` is a nullable int64 — resolver reads it after sqlc regen but does not write it |
| Ownership enforcement | All queries carry `AND tenant_id = $1 AND user_id = $2` (verified in query signatures above) |
| Fail-closed | Missing policy → `GetKeyQuota` returns `ErrQuotaPolicyNotFound`; nil Store → `ErrServiceMisconfig` → 503 |
| Decimal precision | `LimitUSD` is `decimal.Decimal` bound to `numeric(20,8)`; never converted to float64 |
| No new files in frozen packages | Only editing existing `session_middleware.go` (additive field) and `auth_inbound.sql.go` (sqlc regen) |
| Modularity | 10 new files total; largest ~180 lines; no function exceeds 80 lines |

---

## 9. What is NOT in first slice (deferred to slice 2)

- Batch revoke (`POST /v1/api-keys/batch-revoke`)
- Secure reveal token (`POST /v1/api-keys/{id}/reveal-token`)
- Migration 0078 (`api_key_reveal_tokens` table)
- `userkeycontrols/batch_revoke.go`, `reveal.go`
- `userkeycontrolshttp/batch_revoke_handler.go`, `reveal_handler.go`
- HMAC step-up challenge framework
- `reveal_challenge_secret` config key
