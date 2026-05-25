# 2026-05-02 Account-to-API Spine 0011 Codex Parallel Draft

| Field | Value |
| --- | --- |
| Owner directive | "Write your independent CLAUDE.md #10 parallel-draft plan for the Account-to-API spine migration 0011_accapi_spine, saving as docs/process/plans/2026-05-02-accapi-spine-codex.md. DO NOT read docs/process/plans/2026-05-02-accapi-spine-claude.md (the Claude side). DO read docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md (the audit) since both Claude and Codex are working from the same audit. Cover: migration sql, sqlc queries, admin handler, OpenAPI, test plan, rollback, risks, time estimate, open questions. Be specific about schema decisions and indexes. After writing the file, commit it (you can use 'git commit' as Codex author)." |
| Scope | Independent Codex plan only. No migration, query, handler, or OpenAPI implementation in this commit. |
| Success criteria | The synthesized follow-up plan can implement 0011 without re-deciding the core schema shape, indexes, admin API contract, test coverage, rollback posture, or risk boundaries. |
| Time estimate | Plan writing: 45-75 minutes. Future implementation: 6-9 agent hours, 1-2 wall-clock days if PostgreSQL/sqlc/test environment is healthy. |
| Blast radius | The future implementation touches schema, generated db code, admin endpoints, OpenAPI, and integration tests. It must not alter auth core, billing ledger semantics, quota enforcement, payment logic, secrets, or `LICENSE`. |
| Failure modes | Cross-tenant FK gaps, polymorphic target uniqueness bugs, stale sqlc generated code, handler leaking credentials, OpenAPI drift, migration rollback data loss, and over-expanding into Slice 5 credential injection before the binding spine is anchored. |
| Decision points | Owner must approve synthesized Claude/Codex plan before execution. Owner must decide whether 0011 includes only GET/POST bindings or also a disable/delete recovery route; whether `account_state_view` is in 0011 or deferred; whether CLIProxyAPI mining blocks schema landing. |
| Pre-execution checklist | Confirm synthesized plan exists; confirm no one edits the Claude draft during implementation; confirm 0011 migration number is still free; confirm 9 F-ACCAPI rows and DR/spec source are accepted or created before OpenAPI contract changes; run `git status`; protect unrelated dirty files; stage/review/commit only intended files. |

## Independent Read Boundary

- Read shared audit: `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md`.
- Read HUAKAI project rules, parity/risk context, existing migrations, sqlc config, admin handler patterns, OpenAPI contract, and current routes.
- Do not open or use `docs/process/plans/2026-05-02-accapi-spine-claude.md` before the cross-discussion phase.
- This plan is implementer-facing and uses only HUAKAI domain terms. It does not require reading non-MIT reference source.

## Scope

In scope for the later 0011 work:

1. `backend/sql/migrations/0011_accapi_spine.up.sql`
2. `backend/sql/migrations/0011_accapi_spine.down.sql`
3. sqlc query files for API key bindings and request attempts.
4. Regenerated `backend/internal/db/*.sql.go`.
5. Minimal admin binding service and HTTP handlers:
   - `GET /admin/v1/api-keys/{id}/bindings`
   - `POST /admin/v1/api-keys/{id}/bindings`
6. Route wiring in `backend/cmd/gateway/main.go`.
7. `docs/openapi/openapi.yaml` path and schema updates.
8. Focused unit/integration tests plus existing regression suites.

Out of scope for 0011 unless Owner expands it:

- Real upstream credential injection implementation.
- Error classifier implementation.
- Multi-attempt retry/fallback executor changes.
- Capability snapshot table.
- Full admin request trace endpoint.
- Payment, billing ledger, quota enforcement, auth core, secrets, deployment scripts, and destructive migrations.

## Schema Decisions

### Migration Shape

Create exactly one additive spine migration pair:

- `backend/sql/migrations/0011_accapi_spine.up.sql`
- `backend/sql/migrations/0011_accapi_spine.down.sql`

Use `BEGIN; ... COMMIT;` following the existing migration style. This is acceptable for the current dev-size database. If this lands against production-sized data, split index creation into a production migration plan using `CREATE INDEX CONCURRENTLY`, because PostgreSQL cannot run concurrent index creation inside a transaction.

### Composite FK Prerequisites

0011 should create missing tenant-scoped FK targets before adding new tenant-scoped references:

```sql
CREATE UNIQUE INDEX IF NOT EXISTS uq_provider_accounts_tenant_id_id
    ON provider_accounts (tenant_id, id);
```

`uq_pool_groups_tenant_id_id` already exists from the model registry migration path, but 0011 may defensively keep `CREATE UNIQUE INDEX IF NOT EXISTS uq_pool_groups_tenant_id_id ON pool_groups (tenant_id, id);` if sqlc/migrate is expected to run against partially applied dev databases.

Create `uq_api_key_bindings_tenant_id_id` only after the `api_key_bindings` table exists and before adding `usage_records.binding_id` / `request_attempts.binding_id` FKs.

### `api_key_bindings`

Use explicit per-target columns. Do not use a polymorphic `target_id`, because PostgreSQL cannot enforce tenant-scoped FKs against multiple target tables from one discriminator column.

Proposed table:

```sql
CREATE TABLE IF NOT EXISTS api_key_bindings (
    id                      bigserial PRIMARY KEY,
    tenant_id               bigint      NOT NULL REFERENCES tenants(id),
    api_key_id              bigint      NOT NULL,
    binding_kind            text        NOT NULL CHECK (binding_kind IN
                                    ('pool_group', 'provider_account', 'tenant_default')),
    pool_group_id           bigint,
    provider_account_id     bigint,
    tenant_default_token    text,
    priority                integer     NOT NULL DEFAULT 100 CHECK (priority >= 0),
    enabled                 boolean     NOT NULL DEFAULT true,
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    deleted_at              timestamptz,
    created_by_actor        text,
    last_modified_by_actor  text,
    CONSTRAINT api_key_bindings_kind_target_check CHECK (
        (binding_kind = 'pool_group'
            AND pool_group_id IS NOT NULL
            AND provider_account_id IS NULL
            AND tenant_default_token IS NULL)
        OR
        (binding_kind = 'provider_account'
            AND pool_group_id IS NULL
            AND provider_account_id IS NOT NULL
            AND tenant_default_token IS NULL)
        OR
        (binding_kind = 'tenant_default'
            AND pool_group_id IS NULL
            AND provider_account_id IS NULL
            AND tenant_default_token = 'default')
    ),
    FOREIGN KEY (tenant_id, api_key_id)
        REFERENCES api_keys (tenant_id, id)
        ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, pool_group_id)
        REFERENCES pool_groups (tenant_id, id)
        ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, provider_account_id)
        REFERENCES provider_accounts (tenant_id, id)
        ON DELETE RESTRICT
);
```

Index decisions:

```sql
CREATE UNIQUE INDEX uq_api_key_bindings_key_pool_active
    ON api_key_bindings (tenant_id, api_key_id, pool_group_id)
    WHERE binding_kind = 'pool_group' AND deleted_at IS NULL;

CREATE UNIQUE INDEX uq_api_key_bindings_key_account_active
    ON api_key_bindings (tenant_id, api_key_id, provider_account_id)
    WHERE binding_kind = 'provider_account' AND deleted_at IS NULL;

CREATE UNIQUE INDEX uq_api_key_bindings_key_default_active
    ON api_key_bindings (tenant_id, api_key_id, tenant_default_token)
    WHERE binding_kind = 'tenant_default' AND deleted_at IS NULL;

CREATE UNIQUE INDEX uq_api_key_bindings_key_priority_active
    ON api_key_bindings (tenant_id, api_key_id, priority)
    WHERE deleted_at IS NULL;

CREATE INDEX idx_api_key_bindings_resolve
    ON api_key_bindings (tenant_id, api_key_id, enabled, priority, id)
    WHERE deleted_at IS NULL;

CREATE INDEX idx_api_key_bindings_pool_target
    ON api_key_bindings (tenant_id, pool_group_id, priority)
    WHERE binding_kind = 'pool_group' AND deleted_at IS NULL;

CREATE INDEX idx_api_key_bindings_account_target
    ON api_key_bindings (tenant_id, provider_account_id, priority)
    WHERE binding_kind = 'provider_account' AND deleted_at IS NULL;

CREATE UNIQUE INDEX uq_api_key_bindings_tenant_id_id
    ON api_key_bindings (tenant_id, id);
```

Rationale:

- The three per-kind unique partial indexes avoid PostgreSQL's NULL-distinct uniqueness trap.
- `tenant_default` is explicit via `tenant_default_token = 'default'`; never persist an all-NULL target row.
- Priority is unique per active key to make fallback order deterministic. If Owner wants duplicate priorities, drop this index and require `ORDER BY priority, id` everywhere.
- `enabled=false` supports future disable without deleting history. Active uniqueness is scoped by `deleted_at`, not `enabled`, so a disabled duplicate still blocks accidental duplicate active contracts until explicitly soft-deleted. Owner can revise this if disabled bindings should free the slot.

### `usage_records` Additions

Add nullable columns so historical rows remain valid:

```sql
ALTER TABLE usage_records
    ADD COLUMN IF NOT EXISTS pool_group_id bigint,
    ADD COLUMN IF NOT EXISTS binding_id bigint,
    ADD COLUMN IF NOT EXISTS credential_kind text,
    ADD COLUMN IF NOT EXISTS credential_version integer,
    ADD CONSTRAINT usage_records_credential_pair_check
        CHECK ((credential_kind IS NULL AND credential_version IS NULL)
            OR (credential_kind IS NOT NULL AND credential_version IS NOT NULL
                AND credential_kind <> '' AND credential_version >= 0));
```

Add tenant-scoped FKs:

```sql
ALTER TABLE usage_records
    ADD CONSTRAINT fk_usage_pool_group
        FOREIGN KEY (tenant_id, pool_group_id)
        REFERENCES pool_groups (tenant_id, id)
        ON DELETE RESTRICT
        NOT VALID;

ALTER TABLE usage_records
    ADD CONSTRAINT fk_usage_api_key_binding
        FOREIGN KEY (tenant_id, binding_id)
        REFERENCES api_key_bindings (tenant_id, id)
        ON DELETE RESTRICT
        NOT VALID;

ALTER TABLE usage_records VALIDATE CONSTRAINT fk_usage_pool_group;
ALTER TABLE usage_records VALIDATE CONSTRAINT fk_usage_api_key_binding;
```

Index decisions:

```sql
CREATE INDEX idx_usage_records_pool_group_settled
    ON usage_records (tenant_id, pool_group_id, settled_at DESC)
    WHERE pool_group_id IS NOT NULL;

CREATE INDEX idx_usage_records_binding_settled
    ON usage_records (tenant_id, binding_id, settled_at DESC)
    WHERE binding_id IS NOT NULL;

CREATE INDEX idx_usage_records_account_credential_settled
    ON usage_records (tenant_id, provider_account_id, credential_kind, credential_version, settled_at DESC)
    WHERE credential_kind IS NOT NULL;
```

New traffic should populate `pool_group_id`, `binding_id`, `credential_kind`, and `credential_version`. The columns stay nullable only for pre-0011 history and transition windows.

Credential version source: use `provider_accounts.token_version` from F-AUTH-005 as the persisted credential CAS/version field, not a new version nested in JSONB.

### `request_attempts`

Create an append-only per-attempt audit table. This table is not the full trace endpoint; it is the trace substrate needed before retry/fallback work lands.

```sql
CREATE TABLE IF NOT EXISTS request_attempts (
    id                          bigserial PRIMARY KEY,
    tenant_id                   bigint      NOT NULL REFERENCES tenants(id),
    request_id                  text        NOT NULL CHECK (request_id <> ''),
    attempt_number              integer     NOT NULL CHECK (attempt_number >= 0),
    binding_id                  bigint,
    provider_account_id         bigint      NOT NULL,
    pool_group_id               bigint      NOT NULL,
    credential_kind             text        NOT NULL CHECK (credential_kind <> ''),
    credential_version          integer     NOT NULL CHECK (credential_version >= 0),
    started_at                  timestamptz NOT NULL,
    finished_at                 timestamptz,
    upstream_status_code        integer CHECK (
                                    upstream_status_code IS NULL
                                    OR upstream_status_code BETWEEN 100 AND 599),
    error_class                 text,
    retry_after_ms              integer CHECK (retry_after_ms IS NULL OR retry_after_ms >= 0),
    state_transition_emitted    text,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT request_attempts_time_check CHECK (
        finished_at IS NULL OR finished_at >= started_at
    ),
    FOREIGN KEY (tenant_id, binding_id)
        REFERENCES api_key_bindings (tenant_id, id)
        ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, provider_account_id)
        REFERENCES provider_accounts (tenant_id, id)
        ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, pool_group_id)
        REFERENCES pool_groups (tenant_id, id)
        ON DELETE RESTRICT
);
```

Index decisions:

```sql
CREATE UNIQUE INDEX uq_request_attempts_request_attempt
    ON request_attempts (tenant_id, request_id, attempt_number);

CREATE INDEX idx_request_attempts_account_started
    ON request_attempts (tenant_id, provider_account_id, started_at DESC);

CREATE INDEX idx_request_attempts_binding_started
    ON request_attempts (tenant_id, binding_id, started_at DESC)
    WHERE binding_id IS NOT NULL;

CREATE INDEX idx_request_attempts_error_started
    ON request_attempts (tenant_id, error_class, started_at DESC)
    WHERE error_class IS NOT NULL;

CREATE INDEX idx_request_attempts_unfinished
    ON request_attempts (tenant_id, started_at)
    WHERE finished_at IS NULL;
```

Rationale:

- Unique `(tenant_id, request_id, attempt_number)` prevents duplicate attempt rows from making admin trace ambiguous.
- `binding_id` remains nullable only for pre-binding local failure rows or transitional code. Tenant-default traffic should still use an explicit `api_key_bindings` row with `tenant_default_token='default'`.
- `provider_account_id` is non-null because this table records upstream-targeted attempts. Local rejects before account selection belong in admin/audit events, not `request_attempts`.
- For direct provider-account binding, derive `pool_group_id` through `provider_accounts.channel_id -> channels.pool_group_id` at selection/write time.

### Down Migration

Down order must preserve dependencies:

1. Drop `request_attempts`.
2. Drop `usage_records` FK constraints and spine indexes.
3. Drop `usage_records.pool_group_id`, `binding_id`, `credential_kind`, `credential_version`, and the credential pair check.
4. Drop `api_key_bindings`.
5. Drop only 0011-owned prerequisite indexes. Do not drop `uq_pool_groups_tenant_id_id` if it was created by 0008.

Production rollback warning: after real traffic writes bindings or attempts, `migrate down 1` destroys operational evidence. In production, prefer app rollback with writes disabled, export `api_key_bindings` and `request_attempts`, then get Owner confirmation before destructive DB rollback.

## sqlc Query Plan

Create a focused query file:

- `backend/sql/queries/admin_api_key_bindings.sql`

Admin binding queries:

1. `AdminGetAPIKeyForBinding :one`
   - Select key metadata by `(tenant_id, id)`.
   - Require `deleted_at IS NULL`.
   - Handler maps missing key to 404.
2. `AdminInsertPoolGroupBinding :one`
   - `INSERT ... SELECT` guarded by active/non-deleted api_key and non-deleted pool group in same tenant.
   - Returns the created row.
3. `AdminInsertProviderAccountBinding :one`
   - Same pattern; target account must be same tenant and `deleted_at IS NULL`.
   - It may bind disabled/unhealthy accounts only if Owner wants pre-staging. My recommendation: allow disabled accounts but expose state in list; binding is a contract, scheduler still gates dispatch.
4. `AdminInsertTenantDefaultBinding :one`
   - Inserts `binding_kind='tenant_default'`, `tenant_default_token='default'`.
5. `AdminListAPIKeyBindings :many`
   - Tenant/key scoped.
   - Left join `pool_groups`, `provider_accounts`, and `channels` for display metadata.
   - Never select `provider_accounts.credentials`.
   - Order by `priority ASC, id ASC`.
6. `AdminCheckBindingTenantExists :one`
   - Mirror current admin list's deterministic 404 behavior before audit insert.

Request attempt queries may live in either:

- `backend/sql/queries/request_attempts.sql`, or
- `backend/sql/queries/accapi_attempts.sql`

Proposed queries:

1. `InsertRequestAttempt :one`
   - Insert the started row.
   - Used by Slice 5 executor when real upstream attempts exist.
2. `FinishRequestAttempt :execrows`
   - Tenant/id scoped update of `finished_at`, `upstream_status_code`, `error_class`, `retry_after_ms`, `state_transition_emitted`.
   - Must not change target/account/credential fields after insert.
3. `ListRequestAttemptsByRequest :many`
   - Tenant/request scoped, ordered by `attempt_number`.
   - Future `GET /admin/v1/requests/{request_id}` consumes this.
4. `ListRecentAttemptsForBinding :many`
   - Tenant/binding scoped operator diagnostic.

Regenerate code from `backend/`:

```powershell
make generate
```

Expected generated changes:

- New `backend/internal/db/admin_api_key_bindings.sql.go`.
- New `backend/internal/db/request_attempts.sql.go` or equivalent.
- `backend/internal/db/models.go` includes `ApiKeyBinding`, `RequestAttempt`, and new nullable `usage_records` fields when used by queries.

## Admin Handler Plan

Add a small admin service rather than putting write logic directly in HTTP:

- `backend/internal/admin/bindings.go`
- `backend/internal/adminhttp/api_key_bindings_handler.go`

Service responsibilities:

- Authenticate caller is already done by `AdminResolver`.
- Enforce `ident.CanIssueForTenant(tenantID)` or a new equivalent `CanManageTenant`.
- Validate path `api_key_id` and body/query `tenant_id`.
- Normalize request body into one of three binding kinds.
- Call the kind-specific sqlc insert query.
- Map uniqueness violations to 409 Conflict.
- Write `admin_audit_events` for successful create/list with payload that includes IDs, kind, priority, and reason, but no credential material.
- Fail closed on audit write failure, matching current admin list behavior.

HTTP contract:

`GET /admin/v1/api-keys/{id}/bindings?tenant_id=...&limit=50&offset=0`

- 200 returns `{items, limit, offset}`.
- 400 invalid id/tenant/pagination.
- 401/403 admin auth/scope errors.
- 404 tenant or api key not found.
- 503 DB/audit failure.

`POST /admin/v1/api-keys/{id}/bindings`

Request body:

```json
{
  "tenant_id": 1,
  "binding_kind": "pool_group",
  "pool_group_id": 10,
  "provider_account_id": null,
  "priority": 100,
  "reason": "primary GPT pool for live key"
}
```

For tenant default:

```json
{
  "tenant_id": 1,
  "binding_kind": "tenant_default",
  "priority": 900,
  "reason": "fallback to tenant policy"
}
```

Response body should mirror `APIKeyBinding` and include target display fields:

- `id`
- `tenant_id`
- `api_key_id`
- `binding_kind`
- `pool_group_id`
- `provider_account_id`
- `tenant_default_token`
- `priority`
- `enabled`
- `target_name`
- `created_at`
- `updated_at`

Route wiring:

In `backend/cmd/gateway/main.go`, extend the existing `/admin/v1/api-keys` route mount. Do not add a new top-level admin dependency tree unless the service needs a pgx pool for transaction/audit atomicity.

Error mapping additions:

- Add 409 Conflict for duplicate active binding or duplicate priority.
- Keep current JSON error envelope.
- Do not return raw PostgreSQL constraint names in public error messages.

Open handler decision:

- My recommendation is to include only GET/POST in 0011 to match the audit's minimum action, but this leaves no operator undo route. If Owner wants first-slice recovery, add `POST /admin/v1/api-keys/{id}/bindings/{binding_id}/disable` or `DELETE` as a soft delete in the same slice. That is a medium-risk scope expansion, not a schema blocker.

## OpenAPI Plan

Update `docs/openapi/openapi.yaml` after the F-ACCAPI rows and spine spec/DR source exist. The OpenAPI `x-huakai-spec-source` should cite a HUAKAI-owned released artifact, not raw reference source.

Add path:

- `/admin/v1/api-keys/{id}/bindings`
  - `get`, operationId `listAPIKeyBindings`
  - `post`, operationId `createAPIKeyBinding`

Add schemas:

- `APIKeyBinding`
- `APIKeyBindingCreate`
- `APIKeyBindingList`
- `APIKeyBindingKind` if the file style prefers reusable enums.

Schema rules:

- `additionalProperties: false`.
- int64 IDs with minimum 1 where applicable.
- `binding_kind` enum: `pool_group`, `provider_account`, `tenant_default`.
- Nullability uses OpenAPI 3.1 union style already used in the file, not `nullable: true`.
- For create requests, document the exactly-one-target rule:
  - `pool_group` requires `pool_group_id`.
  - `provider_account` requires `provider_account_id`.
  - `tenant_default` requires neither target ID; server persists `tenant_default_token='default'`.

Responses:

- 201 create success.
- 200 list success.
- 400 bad shape.
- 401 unauthorized.
- 403 forbidden.
- 404 tenant/key/target not found.
- 409 duplicate binding or duplicate priority.
- 503 backend/audit failure.

Validation command:

```powershell
python -c "import yaml; yaml.safe_load(open('docs/openapi/openapi.yaml', encoding='utf-8'))"
```

If PyYAML is unavailable, use the repo's existing OpenAPI validation convention if one has been added by then.

## Test Plan

### Migration and SQL Constraints

Use PostgreSQL integration tests with `-tags=integration_pg` where existing DB tests already run.

Coverage:

1. Applying migrations from empty DB succeeds.
2. `api_key_bindings` rejects all-NULL targets.
3. `api_key_bindings` rejects more than one target column.
4. `tenant_default` persists only with `tenant_default_token='default'`.
5. Cross-tenant `api_key_id`, `pool_group_id`, or `provider_account_id` is rejected by composite FK or guarded insert.
6. Duplicate active pool/account/default binding is rejected.
7. Duplicate active priority for the same key is rejected if the synthesized plan keeps `uq_api_key_bindings_key_priority_active`.
8. Soft-deleted old binding permits a new binding to the same target.
9. Historical `usage_records` rows with NULL spine fields remain valid.
10. New `usage_records` rejects credential_kind without credential_version and vice versa.
11. `request_attempts` rejects duplicate `(tenant_id, request_id, attempt_number)`.
12. `request_attempts` rejects cross-tenant account/binding/pool references.
13. `request_attempts` can store an unfinished row and later finish it.

### sqlc and Service Tests

Run:

```powershell
cd backend
make generate
go test ./internal/admin ./internal/adminhttp
```

Add tests:

- `internal/admin` binding manager:
  - creates pool binding.
  - creates provider-account binding.
  - creates tenant-default binding.
  - duplicate target maps to conflict.
  - duplicate priority maps to conflict if priority uniqueness is kept.
  - cross-tenant target maps to not found/forbidden without leaking target existence.
  - audit event is written on create.
- `internal/adminhttp` binding handler:
  - missing/invalid admin bearer -> 401.
  - tenant operator cannot bind another tenant -> 403.
  - invalid body combinations -> 400.
  - successful create -> 201 with no `credentials`, no `key_hash`.
  - list -> 200 ordered by priority.
  - audit write failure -> 503 if the current admin surface continues fail-closed reads.

### Regression Tests

After focused tests:

```powershell
cd backend
go test ./...
go test -tags=integration_pg ./...
go test -tags=smoke ./cmd/gateway
```

If Smart App Control/AppLocker blocks test executables, use the existing wrapper:

```powershell
cd backend
.\scripts\run-go-test.ps1 -GoTestArgs @("./...")
```

For smoke tests, update seed data only if the current smoke path starts writing the new non-null spine fields. Since 0011 keeps usage spine fields nullable, smoke should not require a large rewrite in this slice.

### OpenAPI

Run YAML parse validation and any existing OpenAPI review checklist. Add a tiny contract test only if the repo already has one; otherwise record parse validation in the commit body.

## Rollback Plan

Pre-merge:

- Revert the implementation commit.
- No database action if migration was never applied.

After migration in dev:

```powershell
cd backend
make migrate-down
```

Expected effects:

- Drops `request_attempts`.
- Drops `api_key_bindings`.
- Removes added nullable `usage_records` columns.
- Removes 0011-owned indexes and constraints.

After production traffic:

1. Stop new binding writes and attempt writes at the app layer.
2. Export `api_key_bindings` and `request_attempts`.
3. Prefer app rollback while leaving the schema in place.
4. Only run destructive down migration after Owner confirmation, because it destroys binding contracts and request-attempt forensic evidence.

Rollback of OpenAPI/handler only:

- Revert route wiring and contract changes.
- Leave schema additive columns/tables in place.
- This is the safest rollback if the bug is in admin handler validation, not schema.

## Risks

| Risk | Severity | Mitigation |
| --- | --- | --- |
| Cross-tenant binding | High | Explicit target columns plus composite FKs on `(tenant_id, target_id)`; guarded insert queries. |
| NULL uniqueness trap | High | Three per-kind partial unique indexes; no single unique index across nullable polymorphic columns. |
| Binding order ambiguity | Medium | Unique active `(tenant_id, api_key_id, priority)` or mandatory `ORDER BY priority, id` if Owner rejects uniqueness. |
| Admin secret leak | High | Binding list never selects `provider_accounts.credentials`, never returns key_hash or plaintext bearer. |
| Stale sqlc code | Medium | `make generate` is mandatory; compile tests catch generated type drift. |
| Migration lock | Medium | Current transactional migration is fine for dev; production-sized table needs concurrent/split index plan. |
| Audit fail-closed reads annoy operators | Medium | Match existing admin behavior for consistency; Owner can later decide read audit best-effort. |
| Lightweight lease lacks pre-settlement forensic row | Medium | 0011 records lease fields on attempts and usage. Separate `credential_leases` table remains L2 if operators need acquire/release lifecycle. |
| request_attempts cannot represent pre-account local rejects | Low | Keep those as admin/audit events; attempts represent selected upstream account tries. |
| OpenAPI source citation gap | Medium | Create/approve F-ACCAPI spec or DR before adding `x-huakai-spec-source` entries. |
| Clean-room contamination | Low | This work uses HUAKAI audit/spec/migration patterns. Do not read non-MIT source in implementation session. |

## Open Questions

1. Should 0011 include `account_state_view`, or should F-ACCAPI-STATE-001 land in the next slice with error classifier/state transition code?
2. Should `api_key_bindings.priority` be unique per active key, or should duplicate priorities be allowed with `ORDER BY priority, id`?
3. Should disabled bindings block duplicate target creation until soft-deleted, or should uniqueness apply only to `enabled=true` rows?
4. Should 0011 admin surface include a disable/delete route for operator recovery, or only the audit-minimum GET/POST?
5. Should provider-account bindings allow disabled/unhealthy accounts for pre-staging, or reject them until the account is schedulable?
6. Should request attempts allow `provider_account_id NULL` for local failures after binding but before account selection, or should those remain only in admin/audit events?
7. Should CLIProxyAPI Phase 2 mining block 0011 schema, or run in parallel and feed later refinements?
8. Should OpenAPI cite a new `docs/specs/account-to-api-spine.md`, a DR, or both?
9. Should `binding_id` become NOT NULL on `usage_records` after a future backfill/traffic cutover, or remain nullable forever for historical simplicity?

## Concrete Execution Order

1. Cross-discuss Claude and Codex independent plans.
2. Owner approves synthesized plan and decision points.
3. Add/update F-ACCAPI rows, architecture section, and DR/spec source if not already done.
4. Write 0011 up/down migrations.
5. Add sqlc queries for bindings and attempts.
6. Run `make generate` from `backend`.
7. Implement `internal/admin` binding service.
8. Implement `internal/adminhttp` binding handlers and route wiring.
9. Update OpenAPI paths/schemas.
10. Add migration/sql/admin handler tests.
11. Run focused tests, then broader regression tests.
12. Stage intended files only.
13. Run `codex exec review --uncommitted --full-auto`.
14. Fix any HIGH findings; repeat review if needed.
15. Commit with Codex author and include the review verdict in the commit body.

## Source Files Read

- `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md`
- `docs/RULES.md`
- `docs/01_PROJECT_BRIEF.md`
- `docs/03_FEATURE_PARITY_MATRIX.md`
- `docs/10_RISK_REGISTER.md`
- `docs/12_AGENT_WORKFLOW.md`
- `backend/sqlc.yaml`
- `backend/Makefile`
- `backend/sql/migrations/0001_pool_routing.up.sql`
- `backend/sql/migrations/0002_observability_billing.up.sql`
- `backend/sql/migrations/0006_upstream_credential_management.up.sql`
- `backend/sql/migrations/0007_l0_inbound_auth.up.sql`
- `backend/sql/migrations/0008_model_registry.up.sql`
- `backend/sql/migrations/0009_ledger_fk_backfill.up.sql`
- `backend/sql/migrations/0010_admin_auth.up.sql`
- `backend/sql/queries/admin_api_keys.sql`
- `backend/sql/queries/pool_accounts.sql`
- `backend/sql/queries/obs_queries.sql`
- `backend/sql/queries/billing_settle.sql`
- `backend/cmd/gateway/main.go`
- `backend/internal/admin/admin.go`
- `backend/internal/adminhttp/api_keys_handler.go`
- `docs/openapi/openapi.yaml`

## Owner Summary

Codex recommendation: land 0011 as a narrow, additive spine anchor: explicit `api_key_bindings`, nullable usage spine fields, append-only `request_attempts`, sqlc-backed admin GET/POST binding endpoints, and OpenAPI contract updates. Do not pull credential injection, error classification, state-machine transitions, capability snapshots, or full request trace into this slice. The key implementation hazards are tenant isolation, nullable-target uniqueness, stale generated code, and rollback data loss after real traffic.
