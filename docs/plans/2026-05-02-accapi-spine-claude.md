# Account-to-API spine — Claude plan (CLAUDE.md #10 parallel-draft)

Date: 2026-05-02
Lane: planner / Claude side of the parallel-draft pair.
Companion (must be written independently): `docs/plans/2026-05-02-accapi-spine-codex.md` — to be drafted by Codex without seeing this file.

## 0. Purpose

Implement Track 1 of `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md` (commits `fee331a` → `1545ef5` → `0a89450`):
- 9 spine F-* IDs added to 03 matrix
- 1 migration `0011_accapi_spine` adding `api_key_bindings` + lease/binding/pool_group columns on `usage_records` + `request_attempts` table
- 1 admin endpoint pair `POST/GET /admin/v1/api-keys/{id}/bindings`

This plan is the implementation contract for that scope. Slice-5 prerequisite. Track 2 (F-REQ-BODY-001 + F-LOG-SAFE-001) is independent — see separate plan.

## 1. Scope

### In scope
- Migration `backend/sql/migrations/0011_accapi_spine.up.sql` + `0011_accapi_spine.down.sql`
- Sqlc queries: `backend/sql/queries/api_key_bindings.sql` (CRUD), `backend/sql/queries/request_attempts.sql` (insert + lookup-by-request)
- Generated db code via `sqlc generate`
- Admin handler additions in `backend/internal/adminhttp/api_keys_handler.go`: bind/unbind endpoints
- `backend/internal/admin/binding.go`: BindingService with RBAC + tenant-scope checks
- Integration tests: bind happy path, RBAC violation, duplicate-binding rejection (per-kind partial-unique enforced), tenant_default explicit-row pattern
- Doc updates: `docs/03_FEATURE_PARITY_MATRIX.md` (9 rows), `docs/02_HUAKAI_FUSION_ARCHITECTURE.md` (spine section), `docs/decisions/DR-NNN-account-to-api-mainline.md` new DR
- OpenAPI: `docs/openapi/openapi.yaml` add binding endpoints + APIKeyBinding schema

### Out of scope (deferred to Slice 5)
- F-ACCAPI-CRED-INJECT-001 implementation (interface only is in spec; concrete impls Slice 5)
- F-ACCAPI-ERR-CLASSIFY-001 implementation
- F-ACCAPI-CAP-SNAP-001 (per-account capability snapshot — needs ResolveModel touch)
- F-ACCAPI-TRACE-001 admin trace endpoint (depends on request_attempts being populated, which Slice 5 does)
- gateway handler refactor to use binding lookup (Slice 5 wires)
- usage_records writer code change (Slice 5 fills new columns)

The migration + admin endpoint land NOW so the schema is in place when Slice 5 starts; the data path code lands with Slice 5.

## 2. Migration scope (single migration)

### 2.1 Up

```sql
BEGIN;

-- ============================================================================
-- Table: api_key_bindings (F-ACCAPI-BIND-001)
-- ============================================================================
CREATE TABLE IF NOT EXISTS api_key_bindings (
    id                       bigserial PRIMARY KEY,
    tenant_id                bigint     NOT NULL REFERENCES tenants(id),
    api_key_id               bigint     NOT NULL,
    binding_kind             text       NOT NULL
                              CHECK (binding_kind IN ('pool_group', 'provider_account', 'tenant_default')),
    pool_group_id            bigint,
    provider_account_id      bigint,
    tenant_default_token     text,
    priority                 integer    NOT NULL DEFAULT 100,
    enabled                  boolean    NOT NULL DEFAULT true,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    deleted_at               timestamptz,
    created_by_actor         text,
    last_modified_by_actor   text,

    -- Exactly one target column non-NULL, matching binding_kind.
    CONSTRAINT binding_target_consistency CHECK (
        (binding_kind = 'pool_group'        AND pool_group_id IS NOT NULL
                                            AND provider_account_id IS NULL
                                            AND tenant_default_token IS NULL)
     OR (binding_kind = 'provider_account' AND provider_account_id IS NOT NULL
                                            AND pool_group_id IS NULL
                                            AND tenant_default_token IS NULL)
     OR (binding_kind = 'tenant_default'   AND tenant_default_token IS NOT NULL
                                            AND pool_group_id IS NULL
                                            AND provider_account_id IS NULL)
    )
);

-- Cross-tenant defense: composite FK to api_keys (tenant_id, id).
ALTER TABLE api_key_bindings
    ADD CONSTRAINT fk_api_key_bindings_api_key
    FOREIGN KEY (tenant_id, api_key_id)
    REFERENCES api_keys (tenant_id, id);

-- Cross-tenant defense: composite FK to pool_groups when set.
-- pool_groups has no composite uq_(tenant_id, id) yet. Adding it here in same TX
-- mirrors N+4b1 pattern.
CREATE UNIQUE INDEX IF NOT EXISTS uq_pool_groups_tenant_id_id
    ON pool_groups (tenant_id, id);

ALTER TABLE api_key_bindings
    ADD CONSTRAINT fk_api_key_bindings_pool_group
    FOREIGN KEY (tenant_id, pool_group_id)
    REFERENCES pool_groups (tenant_id, id);

-- provider_accounts already has uq_provider_accounts_tenant_name partial; add tenant_id_id.
CREATE UNIQUE INDEX IF NOT EXISTS uq_provider_accounts_tenant_id_id
    ON provider_accounts (tenant_id, id) WHERE deleted_at IS NULL;

ALTER TABLE api_key_bindings
    ADD CONSTRAINT fk_api_key_bindings_provider_account
    FOREIGN KEY (tenant_id, provider_account_id)
    REFERENCES provider_accounts (tenant_id, id);

-- Per-kind partial unique indexes (Codex pass-12 P2 NULL-safe fix).
CREATE UNIQUE INDEX uq_api_key_bindings_pool_group
    ON api_key_bindings (tenant_id, api_key_id, pool_group_id)
    WHERE binding_kind = 'pool_group' AND deleted_at IS NULL;

CREATE UNIQUE INDEX uq_api_key_bindings_provider_account
    ON api_key_bindings (tenant_id, api_key_id, provider_account_id)
    WHERE binding_kind = 'provider_account' AND deleted_at IS NULL;

CREATE UNIQUE INDEX uq_api_key_bindings_tenant_default
    ON api_key_bindings (tenant_id, api_key_id, tenant_default_token)
    WHERE binding_kind = 'tenant_default' AND deleted_at IS NULL;

-- Lookup by api_key for resolver hot path.
CREATE INDEX idx_api_key_bindings_api_key_priority
    ON api_key_bindings (tenant_id, api_key_id, priority, deleted_at)
    WHERE enabled = true AND deleted_at IS NULL;

COMMENT ON TABLE api_key_bindings IS
    'Slice 2 (N+5c): F-ACCAPI-BIND-001 spine. Maps local api_keys to pool_groups / provider_accounts / tenant_default. Per-kind partial unique enforces NULL-safe duplicate prevention.';

-- ============================================================================
-- usage_records: lease + binding + pool_group columns (F-ACCAPI-LEASE-001)
-- ============================================================================
ALTER TABLE usage_records
    ADD COLUMN IF NOT EXISTS pool_group_id      bigint,
    ADD COLUMN IF NOT EXISTS binding_id         bigint,
    ADD COLUMN IF NOT EXISTS credential_kind    text,
    ADD COLUMN IF NOT EXISTS credential_version integer;

ALTER TABLE usage_records
    ADD CONSTRAINT fk_usage_records_pool_group
    FOREIGN KEY (tenant_id, pool_group_id)
    REFERENCES pool_groups (tenant_id, id);

ALTER TABLE usage_records
    ADD CONSTRAINT fk_usage_records_binding
    FOREIGN KEY (tenant_id, binding_id)
    REFERENCES api_key_bindings (tenant_id, id);

CREATE INDEX idx_usage_records_pool_time
    ON usage_records (tenant_id, pool_group_id, settled_at DESC)
    WHERE pool_group_id IS NOT NULL;

CREATE INDEX idx_usage_records_binding_time
    ON usage_records (tenant_id, binding_id, settled_at DESC)
    WHERE binding_id IS NOT NULL;

-- ============================================================================
-- Table: request_attempts (F-ACCAPI-ATTEMPT-001)
-- ============================================================================
CREATE TABLE IF NOT EXISTS request_attempts (
    id                         bigserial PRIMARY KEY,
    tenant_id                  bigint     NOT NULL REFERENCES tenants(id),
    request_id                 text       NOT NULL,
    attempt_number             integer    NOT NULL,
    binding_id                 bigint,
    provider_account_id        bigint     NOT NULL,
    pool_group_id              bigint     NOT NULL,
    credential_kind            text       NOT NULL,
    credential_version         integer    NOT NULL,
    started_at                 timestamptz NOT NULL,
    finished_at                timestamptz,
    upstream_status_code       integer,
    error_class                text,
    retry_after_ms             integer,
    state_transition_emitted   text,
    created_at                 timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT chk_request_attempts_attempt_nonneg CHECK (attempt_number >= 0)
);

ALTER TABLE request_attempts
    ADD CONSTRAINT fk_request_attempts_provider_account
    FOREIGN KEY (tenant_id, provider_account_id)
    REFERENCES provider_accounts (tenant_id, id);

ALTER TABLE request_attempts
    ADD CONSTRAINT fk_request_attempts_pool_group
    FOREIGN KEY (tenant_id, pool_group_id)
    REFERENCES pool_groups (tenant_id, id);

ALTER TABLE request_attempts
    ADD CONSTRAINT fk_request_attempts_binding
    FOREIGN KEY (tenant_id, binding_id)
    REFERENCES api_key_bindings (tenant_id, id);

CREATE UNIQUE INDEX uq_request_attempts_req_attempt
    ON request_attempts (tenant_id, request_id, attempt_number);

CREATE INDEX idx_request_attempts_account_time
    ON request_attempts (provider_account_id, started_at DESC);

CREATE INDEX idx_request_attempts_error_class_time
    ON request_attempts (error_class, started_at DESC)
    WHERE error_class IS NOT NULL;

COMMENT ON TABLE request_attempts IS
    'Slice 2 (N+5c): F-ACCAPI-ATTEMPT-001 per-attempt audit. Multi-account retry/fallback chain becomes forensically traceable.';

COMMIT;
```

### 2.2 Down

Mirror the up in reverse:
- DROP `request_attempts` (and its 4 FKs + 3 indexes)
- ALTER `usage_records` DROP COLUMN pool_group_id, binding_id, credential_kind, credential_version (and their FKs + indexes)
- DROP `api_key_bindings` (and its 3 FKs + 4 indexes + 1 CHECK)
- DROP `uq_pool_groups_tenant_id_id`, `uq_provider_accounts_tenant_id_id`

Order matters: drop dependent FKs first, then tables.

### 2.3 Migration safety notes

- Application code does NOT yet read or write the new columns. Migration is purely additive on the read side. Existing N+5b chat handler is unaffected.
- New columns on `usage_records` are nullable so the migration does not require backfill. Once Slice 5 ships, the writer fills them; pre-migration rows stay NULL forever (acceptable per audit §5.4).
- All FKs are ON DELETE RESTRICT (default). No tenant cascade.
- Composite indexes for cross-tenant FK targets are added separately because pool_groups + provider_accounts didn't have them yet (mirrors N+4b1 pattern).

## 3. Sqlc queries

### 3.1 `backend/sql/queries/api_key_bindings.sql`

- `BindingsForKey :many` — by (tenant_id, api_key_id) ORDER BY priority ASC. Hot path.
- `InsertPoolGroupBinding :one` — params: tenant_id, api_key_id, pool_group_id, priority, actor.
- `InsertProviderAccountBinding :one` — analogous.
- `InsertTenantDefaultBinding :one` — params: tenant_id, api_key_id, priority, actor. Token always 'default'.
- `SoftDeleteBinding :execrows` — by (tenant_id, id) WHERE deleted_at IS NULL. Updates deleted_at + last_modified_by_actor.
- `GetBindingByID :one` — for trace endpoint join.

### 3.2 `backend/sql/queries/request_attempts.sql`

- `InsertRequestAttempt :exec` — Slice 5 caller; full row.
- `AttemptsForRequest :many` — by (tenant_id, request_id) ORDER BY attempt_number ASC.
- `RecentErrorsForAccount :many` — by (provider_account_id, error_class, started_at) — for admin investigation.

## 4. Admin HTTP layer

### 4.1 `backend/internal/admin/binding.go` (new)

```
type BindingRequest struct {
    Caller    AdminIdentity
    APIKeyID  int64
    TenantID  int64
    Kind      string  // 'pool_group' / 'provider_account' / 'tenant_default'
    TargetID  int64   // for pool_group / provider_account; ignored for tenant_default
    Priority  int     // optional; default 100
    Reason    string
    RequestID string
}
type BindingResult struct { ID int64; CreatedAt time.Time }

type BindingService struct {
    pool *pgxpool.Pool
    bcryptCost int  // not used but mirrors KeyIssuer shape
}

func (b *BindingService) Bind(ctx, req) (BindingResult, error) {
    // RBAC: req.Caller.CanIssueForTenant(req.TenantID) — same rule as N+4b2 issuer
    // Validate: Kind ∈ enum; TargetID > 0 unless Kind='tenant_default'; api_key exists in tenant
    // For pool_group: verify pool_group exists in tenant
    // For provider_account: verify exists in tenant
    // TX: insert binding row with audit
    // Returns ErrAdminBadRequest / ErrAdminNotFound / ErrAdminForbidden / ErrAdminBackend per N+4b2 conventions
}

func (b *BindingService) Unbind(ctx, BindingID, TenantID, Caller, RequestID) error {
    // Soft-delete via SoftDeleteBinding; audit row in admin_audit_events
    // action: 'unbind_api_key' (CHECK constraint on admin_audit_events.action requires migration to extend the enum — handled in 0011 too)
}

func (b *BindingService) ListByKey(ctx, TenantID, APIKeyID) ([]BindingRow, error) {
    // BindingsForKey query
}
```

Audit event extension: extend `admin_audit_events.action` CHECK to allow `'bind_api_key'` and `'unbind_api_key'`. Done in 0011 as part of the migration.

### 4.2 `backend/internal/adminhttp/api_keys_handler.go` additions

- `POST /admin/v1/api-keys/{id}/bindings` body `{kind, target_id, priority, reason}` → 201 `{id, created_at}`
- `GET /admin/v1/api-keys/{id}/bindings` query `?tenant_id=N&include_deleted=false` → `{items}`
- `POST /admin/v1/api-keys/{id}/bindings/{binding_id}/unbind` body `{tenant_id, reason}` → 200 `{id, deleted_at}`

These reuse the AdminResolver from N+4b2; no new auth path.

### 4.3 `cmd/gateway/main.go` deps + route wiring

Add `bindingService *admin.BindingService` to `deps`. Add 3 new chi routes under existing `/admin/v1/api-keys` group.

## 5. OpenAPI updates

- Path operations for `/admin/v1/api-keys/{id}/bindings` (POST + GET) and `/admin/v1/api-keys/{id}/bindings/{binding_id}/unbind` (POST)
- Schemas: `APIKeyBindingCreate`, `APIKeyBinding`, `APIKeyBindingList`, `APIKeyBindingUnbindRequest`, `APIKeyBindingUnbindResponse`
- All under existing `admin-api-keys` tag

## 6. Test plan

### 6.1 Unit tests (no DB)
- BindingService input validation (Kind enum, TargetID rules)
- RBAC delegation to AdminIdentity.CanIssueForTenant

### 6.2 Integration tests (`-tags=integration_pg`) in `internal/admin/binding_integration_test.go`
1. **TestBind_PoolGroup_HappyPath** — bind active key to active pool, list returns 1.
2. **TestBind_ProviderAccount_HappyPath** — bind to active account.
3. **TestBind_TenantDefault_HappyPath** — bind with kind=tenant_default, target=ignored, token='default' on row.
4. **TestBind_DuplicateRejected** — bind same kind+target twice → 409 conflict (per-kind partial-unique fires).
5. **TestBind_CrossTenantBlocked** — tenant_operator A binds key in A to pool in B → 403.
6. **TestBind_InactiveTargetRejected** — bind to disabled pool_group → 400.
7. **TestUnbind_Idempotent** — unbind twice → second returns AlreadyDeleted (200 with flag).
8. **TestList_OrderedByPriority** — multiple bindings return in priority ASC.
9. **TestNullSafeUniqueness** — direct SQL test that two `pool_group` bindings with same (tenant_id, api_key_id, pool_group_id) and tenant_default_token=NULL are rejected (this is the Codex pass-12 P2 regression).

### 6.3 OpenAPI contract test
- `python -c "import yaml; yaml.safe_load(open('docs/openapi/openapi.yaml'))"` parses
- All `$ref` resolve

### 6.4 Smoke test
- Run `cmd/gateway` smoke; verify routes mount; verify `MaybeBootstrap` still no-ops on populated DB.

## 7. File-by-file change list

| File | Action |
| --- | --- |
| `backend/sql/migrations/0011_accapi_spine.up.sql` | new |
| `backend/sql/migrations/0011_accapi_spine.down.sql` | new |
| `backend/sql/queries/api_key_bindings.sql` | new |
| `backend/sql/queries/request_attempts.sql` | new |
| `backend/internal/db/api_key_bindings.sql.go` | sqlc-generated |
| `backend/internal/db/request_attempts.sql.go` | sqlc-generated |
| `backend/internal/db/models.go` | sqlc-regenerated (3 new types) |
| `backend/internal/db/querier.go` | sqlc-regenerated |
| `backend/internal/admin/binding.go` | new (~250 LOC) |
| `backend/internal/admin/admin.go` | add `bind_api_key` / `unbind_api_key` to action enum constants |
| `backend/internal/admin/binding_integration_test.go` | new (9 cases) |
| `backend/internal/adminhttp/api_keys_handler.go` | extend with bind/unbind/list-bindings handlers (~150 LOC) |
| `backend/cmd/gateway/main.go` | wire BindingService into deps + 3 chi routes |
| `docs/openapi/openapi.yaml` | new path entries + 5 schemas |
| `docs/03_FEATURE_PARITY_MATRIX.md` | append 9 F-ACCAPI-* rows in HUAKAI-native section |
| `docs/02_HUAKAI_FUSION_ARCHITECTURE.md` | new "Account-to-API Mainline" section |
| `docs/decisions/DR-NNN-account-to-api-mainline.md` | new DR (NNN to be assigned) |

Total estimated LOC: ~1500 (code + SQL + tests + docs). Most of that is sqlc generated + boilerplate + tests. Net hand-written code: ~600 LOC.

## 8. Rollback strategy

If migration applied but code not yet shipped: `0011_accapi_spine.down.sql` reverses cleanly because no application code reads/writes the new columns yet.

If migration applied + code shipped + bug found:
- Soft-rollback: feature-flag the binding endpoints off in main.go (return 503), keep schema, retry implementation.
- Hard-rollback: down migration only safe if NO row was written to api_key_bindings or request_attempts in production. Otherwise the FK on usage_records.binding_id would prevent dropping the binding table without forcing those usage rows to NULL first (FK is RESTRICT).

Recommend: don't ship to prod until at least 24h soak in dev with traffic. Personal-edition operators should treat this as a config-locking moment.

## 9. Risks

| Risk | Mitigation |
| --- | --- |
| Per-kind partial unique missed an edge case | Test #9 specifically targets the NULL-distinct trap |
| `admin_audit_events.action` CHECK extension migrates an existing CHECK (Postgres requires drop+add) | Migration uses `ALTER TABLE ... DROP CONSTRAINT ... ADD CONSTRAINT` in same TX |
| pool_groups composite uniqueness didn't exist before — adding it could collide with stale data | Migration includes `ON CONFLICT DO NOTHING` semantics? No — uniqueness over existing data must be checked first. Pre-migration step: `SELECT tenant_id, id, count(*) FROM pool_groups GROUP BY 1,2 HAVING count(*) > 1` (should be empty since id is PK). Owner-verified before deploy. |
| Slice 5 not yet ready when 0011 lands | Acceptable — schema is additive, no behavior change. Slice 5 wires reads/writes when its time comes. |
| Codex pass-13 finds new issue | Re-run codex review post-commit; address before merging to release |

## 10. Time estimate (Claude implementing)

- Migration up + down + sqlc queries: 90 min
- BindingService + handler: 90 min
- Integration tests: 60 min (9 cases)
- OpenAPI: 30 min
- 03 matrix updates: 60 min (9 rows + cross-references)
- 02 architecture section: 45 min
- DR-NNN: 30 min
- Codex review iterations: 1-3 rounds × ~15 min each
- **Total: ~6-8 hours focused work**

## 11. Open decision points (Owner to confirm before implementation)

1. **DR number**: which DR-NNN to assign? Last DR is `DR-008` (released gate); proposed `DR-009-account-to-api-mainline`. Owner confirms.
2. **Action enum extension**: extending `admin_audit_events.action` CHECK in 0011 means a tightly-coupled migration. Alternative is a separate 0011a migration. Recommendation: same migration for atomicity.
3. **Tenant default semantics**: when a customer key has only a tenant_default binding and the tenant has no default pool configured, what does the gateway do? Recommend: 503 `tenant_default_pool_unconfigured` with operator-visible alert. Outside this plan's scope but flagged here.
4. **Smoke test extension**: Slice-5 prerequisite says smoke must verify schema; extend `cmd/gateway/smoke_test.go` to assert table existence + index existence? Recommend yes, 30 min add.

## 12. Sign-off needed before implementation

- [ ] Owner confirms DR-009 number
- [ ] Owner approves migration scope (this plan §2)
- [ ] Codex reads `docs/plans/2026-05-02-accapi-spine-codex.md` is written without seeing this file (CLAUDE.md #10)
- [ ] Cross-discuss the two plans → record agreement / conflict / gaps
- [ ] Owner says "go" → execute
