# N+4b — Admin API-key Issuance + Ledger FK Backfill (Synthesized)

| Field | Value |
| --- | --- |
| Status | Synthesized after parallel-draft cross-discuss (CLAUDE.md #10) |
| Sources | [-claude.md](2026-05-01-n4b-admin-keys-claude.md) + [-codex.md](2026-05-01-n4b-admin-keys-codex.md) |
| Synthesis authority | Claude per Owner "PM 决定" 2026-05-01; no Owner-blocking conflicts surfaced |
| Date | 2026-05-01 |
| Predecessor | N+5b (commit `0b97880`); chained to N+4a (`121db58`) |
| Migrations | `0009_admin_api_keys_and_ledger_fks` (single — codex shape adopted) |
| Citation discipline | Owner directive 2026-04-30; every claim cites repo file:line. No training-time recall. |

---

## Conflict resolution

The plans converged strongly. Codex grounded every claim in file:line evidence; Claude proposed slightly bigger scope (REVOKE + LIST + bootstrap). Adopted resolution:

| Topic | Claude pick | Codex pick | **Adopted** | Why |
|---|---|---|---|---|
| D1 admin auth table | `admin_credentials` | `admin_tokens` | **`admin_tokens`** | Codex name aligns with `docs/openapi/openapi.yaml` AuditEvent role enum + actor concept |
| D5 audit table shape | tenant+admin_credential_id+action+target+ip_inet+ua | tenant+actor_id+actor_role+action+target_type+target_id+request_id+reason+payload | **Codex shape** | Matches OpenAPI `AuditEvent` schema at `openapi.yaml:1148-1158` |
| D6 N+4b scope | POST + LIST + REVOKE | POST only; LIST/REVOKE deferred | **POST + LIST + REVOKE** | Cheap (~30 min extra); operator absolutely needs revoke for stolen keys; LIST is read-only |
| Bootstrap admin | env-var `HUAKAI_ADMIN_BOOTSTRAP_TOKEN` first-time issuance | not addressed | **env-var bootstrap** | Without it, issuing the FIRST admin key requires hand-SQL — defeats N+4b's purpose |
| Migration shape | single migration, plain ALTER ADD FK | single migration with NOT VALID + VALIDATE | **NOT VALID + VALIDATE pattern** | Codex's pattern is the production-safe norm; same migration but lower regret cost |
| `pool_slot_acquisitions.claim_id` FK | (missed) | add as part of N+4b | **Add it** | Codex caught: column exists since N+0001 with comment "FK deferred"; close it now |
| OpenAPI + api-contract doc update | (missed) | add to touch list | **Add it** | Reviewer-lane gate; admin endpoints must appear in the contract |
| Migration name | `0009_ledger_fk_backfill` + `0010_admin_auth` | `0009_admin_api_keys_and_ledger_fks` (single) | **Two migrations: 0009 FK only + 0010 admin tables** | Codex bundled both into 0009; Claude split. Splitting gives independent rollback (FK migration is more dangerous than table-add). Net: keep Codex's single-PR sequencing but use TWO migration files for granular revert. |
| Effort estimate | 6.5h | 2.5–4 engineer-days | **~6–8h** for code + tests; reviewer cycles add ~2h | Claude's hours estimate is calibrated to recent N+4a/N+5a/N+5b cadence in this repo |

D7 RBAC, D2 entropy, D3 plaintext-once-only, DB1 single migration, DB2 RESTRICT, DB3 composite uniq index, DB4 fix-fixtures-first, DB5 pre-L0-status: **all identical** across plans, no decision needed.

No remaining conflicts requiring Owner input.

---

## Final scope

### Scope A — Admin API-key issuance
1. `POST /admin/v1/api-keys` — issue
2. `GET /admin/v1/api-keys` — tenant-scoped list
3. `POST /admin/v1/api-keys/{id}/revoke` — soft revoke (no DELETE)
4. Admin auth via separate `admin_tokens` table (D1)
5. Bootstrap: env-var `HUAKAI_ADMIN_BOOTSTRAP_TOKEN` for first-key issuance
6. Audit: every admin action writes a row to `admin_audit_events`
7. RBAC: `tenant_operator` (own-tenant only) and `platform_admin` (cross-tenant), per `docs/specs/api-contract.md:47-50`

### Scope B — Ledger FK backfill
Six FK constraints (Codex § 5):

```
billing_ledger_claims.(tenant_id, api_key_id) → api_keys.(tenant_id, id)
billing_ledger_claims.(tenant_id, user_id)    → users.(tenant_id, id)
usage_records.(tenant_id, api_key_id)          → api_keys.(tenant_id, id)
usage_records.(tenant_id, user_id)             → users.(tenant_id, id)
billing_ledger_archive.(tenant_id, api_key_id) → api_keys.(tenant_id, id)
pool_slot_acquisitions.claim_id                → billing_ledger_claims.id
```

All `ON DELETE RESTRICT`. Composite tenant-bound prevents cross-tenant misbinding.

Pre-FK fixture cleanup: replace `apiKeyID = tenantID*100 + 1` synthetic ids in:
- `backend/internal/billing/claim_gate_integration_test.go:51`
- `backend/internal/billing/settler_integration_test.go` (if same pattern)
- `backend/internal/pool/db_adapters_integration_test.go:48-63` (Codex caught)

---

## Final D-points

### D1 — `admin_tokens` table

```sql
CREATE TABLE IF NOT EXISTS admin_tokens (
    id              bigserial PRIMARY KEY,
    name            text        NOT NULL,
    key_hash        text        NOT NULL,
    key_prefix      text        NOT NULL,           -- first 16 chars; mirrors api_keys
    role            text        NOT NULL DEFAULT 'tenant_operator'
                    CHECK (role IN ('platform_admin', 'tenant_operator')),
    -- For tenant_operator: scoping tenant. NULL for platform_admin.
    scope_tenant_id bigint      REFERENCES tenants(id),
    bootstrap       boolean     NOT NULL DEFAULT false,
    status          text        NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'disabled', 'revoked')),
    expires_at      timestamptz,
    last_used_at    timestamptz,
    revoked_at      timestamptz,
    revoked_reason  text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz,
    CONSTRAINT scope_tenant_consistency
        CHECK ((role = 'platform_admin' AND scope_tenant_id IS NULL) OR
               (role = 'tenant_operator' AND scope_tenant_id IS NOT NULL))
);
CREATE INDEX idx_admin_tokens_prefix
    ON admin_tokens (key_prefix)
    WHERE deleted_at IS NULL AND status = 'active';
```

### D2 — Token shape

`hk_admin_<24-char-base32>` (32 chars total). 24-char random suffix = 120 bits entropy. Base32 (no `0`/`O`/`1`/`I`) for clipboard friendliness. Prefix = `bearer[:16]` = `hk_admin_xxxxxxxx`.

### D3 — Plaintext display

POST response body field `plaintext_bearer` populated **once** in 201; never returned by LIST or any subsequent fetch. `X-Huakai-Key-Display: once-only` response header reminds operator.

### D4 — Issuance rate-limit

DB-backed sliding-window count over `admin_audit_events` rows where `actor_id = caller_id AND action = 'issue_api_key' AND occurred_at > now() - interval '1 hour'`. **Cap = 30/h per admin token.** 30 lets operator onboard a small group; under 31 attempt → 429.

### D5 — `admin_audit_events` shape (Codex)

```sql
CREATE TABLE IF NOT EXISTS admin_audit_events (
    id           bigserial PRIMARY KEY,
    tenant_id    bigint REFERENCES tenants(id),                -- NULL for cross-tenant platform actions
    actor_id     text NOT NULL,                                -- e.g. admin_token id stringified
    actor_role   text NOT NULL CHECK (actor_role IN ('platform_admin', 'tenant_operator')),
    action       text NOT NULL,                                -- 'issue_api_key' / 'revoke_api_key' / 'list_api_keys' / 'admin_login'
    target_type  text NOT NULL,                                -- 'api_key' / 'admin_token'
    target_id    bigint,
    request_id   text,                                         -- chi middleware-set
    reason       text,
    payload      jsonb NOT NULL DEFAULT '{}'::jsonb,           -- redacted: NEVER plaintext or hash
    occurred_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_admin_audit_events_tenant_time
    ON admin_audit_events (tenant_id, occurred_at DESC);
CREATE INDEX idx_admin_audit_events_actor_action_time
    ON admin_audit_events (actor_id, action, occurred_at DESC);  -- used by D4 rate-limit window
```

Append-only. Shape mirrors OpenAPI `AuditEvent` at `docs/openapi/openapi.yaml:1148-1158`.

### D6 — Endpoints

- `POST /admin/v1/api-keys` — issue (200 → 201). Body: `tenant_id`, `user_id`, `name`, optional `display_name`, optional `expires_at`, optional `environment` (`live`/`test`, default `live`), optional `reason`. Response: api_keys row metadata + `plaintext_bearer`.
- `GET /admin/v1/api-keys` — list. Query: `tenant_id` (required for tenant_operator; optional for platform_admin), `status` filter, `limit`/`offset`. Response: array; each item has id/name/key_prefix/status/timestamps. Never key_hash, never plaintext.
- `POST /admin/v1/api-keys/{id}/revoke` — revoke. Body: `reason`. Idempotent: revoking already-revoked returns 200 with `already_revoked: true`.

PATCH and DELETE explicitly deferred. Document in OpenAPI as "POST + GET + revoke only at L0; PATCH/DELETE = N+4c".

### D7 — RBAC

Per `docs/specs/api-contract.md:47-50`:
- `tenant_operator` token's `scope_tenant_id` MUST match request body's `tenant_id`. 403 otherwise.
- `platform_admin` may issue/list/revoke for any tenant.
- POST validates `users(tenant_id, id)` exists and `status='active'` before creating the api_keys row (use existing `GetUserByID` query, `backend/sql/queries/auth_inbound.sql:41-48`).

### D8 — RBAC additions

Bootstrap row is `role='platform_admin'`, `bootstrap=true`. Documented as: rotate before public exposure.

### DB1 — Migration shape

`0009` (FK backfill) + `0010` (admin tables). Two files for granular revert. Both apply in same N+4b commit but rollback story is atomic per file.

```sql
-- 0009_ledger_fk_backfill.up.sql
BEGIN;
CREATE UNIQUE INDEX IF NOT EXISTS uq_api_keys_tenant_id_id ON api_keys (tenant_id, id);
ALTER TABLE billing_ledger_claims  ADD CONSTRAINT fk_claims_api_key   FOREIGN KEY (tenant_id, api_key_id) REFERENCES api_keys (tenant_id, id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE billing_ledger_claims  ADD CONSTRAINT fk_claims_user      FOREIGN KEY (tenant_id, user_id)    REFERENCES users    (tenant_id, id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE usage_records          ADD CONSTRAINT fk_usage_api_key    FOREIGN KEY (tenant_id, api_key_id) REFERENCES api_keys (tenant_id, id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE usage_records          ADD CONSTRAINT fk_usage_user       FOREIGN KEY (tenant_id, user_id)    REFERENCES users    (tenant_id, id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE billing_ledger_archive ADD CONSTRAINT fk_archive_api_key  FOREIGN KEY (tenant_id, api_key_id) REFERENCES api_keys (tenant_id, id) ON DELETE RESTRICT NOT VALID;
ALTER TABLE pool_slot_acquisitions ADD CONSTRAINT fk_psa_claim        FOREIGN KEY (claim_id)              REFERENCES billing_ledger_claims (id) ON DELETE RESTRICT NOT VALID;
-- VALIDATE in same TX (HUAKAI is pre-L0 per blueprint; no production data to walk over).
ALTER TABLE billing_ledger_claims  VALIDATE CONSTRAINT fk_claims_api_key;
ALTER TABLE billing_ledger_claims  VALIDATE CONSTRAINT fk_claims_user;
ALTER TABLE usage_records          VALIDATE CONSTRAINT fk_usage_api_key;
ALTER TABLE usage_records          VALIDATE CONSTRAINT fk_usage_user;
ALTER TABLE billing_ledger_archive VALIDATE CONSTRAINT fk_archive_api_key;
ALTER TABLE pool_slot_acquisitions VALIDATE CONSTRAINT fk_psa_claim;
COMMIT;
```

`0009_ledger_fk_backfill.down.sql` drops the 6 FK constraints, then `uq_api_keys_tenant_id_id`.

---

## Touch list (synthesized)

| File | Change |
|---|---|
| `backend/sql/migrations/0009_ledger_fk_backfill.up.sql` (NEW) | 6 composite FKs + uq index |
| `backend/sql/migrations/0009_ledger_fk_backfill.down.sql` (NEW) | reverse |
| `backend/sql/migrations/0010_admin_auth.up.sql` (NEW) | `admin_tokens` + `admin_audit_events` tables |
| `backend/sql/migrations/0010_admin_auth.down.sql` (NEW) | DROP both |
| `backend/sql/queries/admin_tokens.sql` (NEW) | LookupAdminTokenByPrefix, InsertAdminToken, RevokeAdminToken |
| `backend/sql/queries/admin_audit.sql` (NEW) | InsertAdminAuditEvent, CountIssuanceInWindow (for D4 rate-limit) |
| `backend/sql/queries/admin_api_keys.sql` (NEW) | InsertAPIKey, ListAPIKeysForTenant, RevokeAPIKey |
| `backend/internal/db/admin_*.sql.go` | sqlc-generated |
| `backend/internal/admin/admin.go` (NEW) | Package doc + types |
| `backend/internal/admin/keygen.go` (NEW) | Bearer generator + prefix derivation (shared helper across api_keys + admin_tokens) |
| `backend/internal/admin/operator_auth.go` (NEW) | AdminResolver — looks up admin_tokens by prefix, bcrypt-verifies |
| `backend/internal/admin/issuer.go` (NEW) | KeyIssuer service: RBAC + tenant scope check + rate-limit + tx + audit |
| `backend/internal/admin/issuer_test.go` (NEW) | Unit tests |
| `backend/internal/admin/issuer_integration_test.go` (NEW) | Integration |
| `backend/internal/admin/bootstrap.go` (NEW) | Env-var bootstrap admin token |
| `backend/internal/adminhttp/api_keys_handler.go` (NEW) | POST/GET/POST-revoke handlers |
| `backend/internal/adminhttp/api_keys_handler_test.go` (NEW) | Unit tests with stubs |
| `backend/cmd/gateway/main.go` | Wire AdminResolver + KeyIssuer + bootstrap; mount routes |
| `backend/internal/billing/claim_gate_integration_test.go` | Replace synthetic ids ([:51]) |
| `backend/internal/billing/settler_integration_test.go` | Same |
| `backend/internal/pool/db_adapters_integration_test.go` | Same ([:48-63] per Codex) |
| `docs/openapi/openapi.yaml` | Add 3 admin endpoints + schemas; reuse AuditEvent |
| `docs/specs/api-contract.md` | Mention admin issuance flow + RBAC |

---

## Test plan (synthesized)

### Unit (no DB)
- Keygen produces correct namespace + length + entropy + prefix-derivation.
- Bcrypt cost honored.
- `IssueResult.Plaintext` field population; `String()` method elides plaintext for log-safety (CMB-5).
- RBAC: `tenant_operator` blocked from cross-tenant; `platform_admin` allowed.
- Bootstrap inserts once when admin_tokens empty; no-op when non-empty.
- Handler 401 / 403 / 400 / 201 for stub-driven flows.

### Integration (`-tags=integration_pg`)
- HappyIssue: bootstrap admin → issue → resolve via APIKeyResolver succeeds.
- AdminAuthRequired: missing `Authorization` → 401.
- TenantOperatorCrossTenantBlocked: tenant_operator with `scope_tenant_id=A` issuing for `tenant_id=B` → 403.
- PlatformAdminCrossTenant: platform_admin issuing for any tenant → 201.
- RateLimited: issue 30 keys quickly; 31st in window → 429.
- ListTenantScoped: returns only requested tenant's keys (or all for platform_admin).
- RevokeIdempotent: double-revoke returns 200 with `already_revoked=true`.
- RevokeBlocksAuth: revoked key → resolver returns ErrUnauthorized.
- AuditRowWritten: each action persists exactly one `admin_audit_events` row with correct `action`/`target_type`/`actor_role`.
- AuditNeverContainsPlaintext: assertion that `payload jsonb` field excludes `plaintext_bearer` substring.

### FK regression (under integration_pg with 0009 applied)
- `INSERT billing_ledger_claims` with nonexistent `api_key_id` → FK violation.
- `DELETE api_keys` referenced by claim → RESTRICT.
- Cross-tenant binding: `(tenant_id=A, api_key_id of tenant B)` → FK error (composite key is the defense).

### Smoke
- `seedSmokeGraph` continues to work (already seeds real api_keys after N+4a). The new FKs should be a no-op for smoke. New PG-state assertion: smoke run must populate `admin_audit_events = 0` rows (no admin work in smoke path).

---

## Risk matrix (synthesized)

| Risk | Severity | Trigger | Detection | Mitigation |
|---|---|---|---|---|
| Plaintext leaks into logs/audit | **HIGH** | Logger threading mistake; audit row carrying `plaintext_bearer` | Grep CI gate; CMB-5 reviewer | `IssueResult.String()` elides plaintext; payload jsonb explicitly schema-checked; assertion in `TestAuditNeverContainsPlaintext` |
| Customer key flagged as admin | **HIGH** | Hot-path resolver shared with admin path leads to confusion | Schema separation: `admin_tokens` is its own table; APIKeyResolver never reads from it | Two distinct resolvers; reviewer-lane gate |
| FK migration breaks existing tests | **HIGH** | Synthetic ids in fixtures vs new FK | Tests fail at FK violation in setup | Fix fixtures BEFORE migration validates (DB4 — same commit) |
| Cascade delete erases billing | **HIGH** | Mistaken `ON DELETE CASCADE` | Money-grade audit gone | RESTRICT only; reviewer checks ON DELETE clause line by line |
| Bootstrap token leaked in container env logs | MED | k8s ConfigMap vs Secret misuse | env-printing CI gate | Doc rule: bootstrap MUST be a Secret; env-var pattern recommended via `op://` or k8s Secret |
| Bootstrap reused after first issuance | MED | Operator forgets to rotate | log warning each boot | After first non-bootstrap admin issuance, mark `bootstrap=true` row `status='disabled'` automatically |
| Composite uniq index missing for FK target | LOW | `uq_api_keys_tenant_id_id` not yet created when FK ALTER fires | migration error halt | Migration creates index FIRST, before ALTER ADD FK |
| Existing dev DB has rows that violate new FK | LOW | Dev PG has leftover synthetic-id rows | `VALIDATE CONSTRAINT` rejects | TRUNCATE billing tables in 0009 BEFORE the ALTER (HUAKAI pre-L0; no production data) |
| OpenAPI spec drift | LOW | Doc not updated alongside code | Reviewer catches | Doc updates listed in touch list; reviewer-lane gate |
| `pool_slot_acquisitions.claim_id` FK breaks pool tests | MED | Existing pool tests insert PSA rows without seeding a claim | FK violation at insert | Codex evidence: pool_adapters tests already have synthetic ids; same fixture cleanup applies |

---

## CMB compliance

| Invariant | Status | Notes |
|---|---|---|
| **CMB-1** Router does not read credentials | ✅ | `internal/admin` is its own package; no Router import |
| **CMB-5** Credentials never logged | ✅ | Plaintext in IssueResult only; audit payload jsonb redacted; test asserts |
| **CMB-7** Layer write discipline | ✅ | admin package writes ONLY to `admin_tokens`, `api_keys`, `admin_audit_events`. Never to billing/pool tables. |

---

## Sequencing

**Single PR, two commits**:

**Commit 1 (`N+4b1`)**: Schema 0009 + fixture cleanup + FK regression tests.
- Migration 0009 up/down
- 3 fixture files updated
- New FK regression tests
- Integration_pg green
- Smoke green
- Codex review pass before commit

**Commit 2 (`N+4b2`)**: Schema 0010 + admin package + adminhttp + bootstrap + main.go wiring + admin tests + OpenAPI/api-contract docs.
- Migration 0010 up/down
- New `internal/admin` + `internal/adminhttp` packages
- Bootstrap env-var path
- 10+ unit tests, 10 integration tests
- OpenAPI + api-contract.md updates
- main.go wiring
- Codex review pass before commit

**Why split**: FK migration is data-integrity-class (high-risk if wrong); admin endpoint is HTTP-surface-class (different risk class). Independent reverts. If admin endpoint has a bug, we keep the FK improvements. Single PR keeps "B is shipped" atomic at merge.

---

## Estimated effort

~6–8 hours implementation + ~2 hours codex review iterations = **~8–10h total**.

---

## Open questions for Owner (deferred to implementation, not blocking)

1. **Display name on api_keys**: existing schema has `name` (required). Is `display_name` (separate, optional) worth adding now? Default: NO (keep `name` only); Phase E can add if multi-key UI needs it.
2. **Bootstrap mechanism rollout**: env var (this plan) vs k8s Secret-mounted-as-file vs Vault sidecar. Default: env var, document file pattern as alternative.
3. **PATCH / DELETE timing**: deferred to N+4c (rotate / hard-delete-with-audit). Confirm OK to leave out.
4. **Additional admin endpoints in N+4b**: `POST /admin/v1/users` (create user)? Currently the issuance flow REQUIRES `user_id` to exist; without `users` endpoint operators must create users via SQL. Scope creep risk vs operator onboarding. Default: defer; stub `POST /admin/v1/users` returns 501.

---

Source files read (Claude this round): Codex N+4b plan (full); Claude N+4b plan (already authored); `docs/openapi/openapi.yaml` (AuditEvent schema, lines 1148-1158); `docs/specs/api-contract.md` (lines 45-66, 96-107); `backend/sql/migrations/0001_pool_routing.up.sql` (pool_slot_acquisitions.claim_id deferred FK, lines 168-196); `backend/sql/migrations/0002_observability_billing.up.sql` (claims/usage/archive missing FKs); `backend/sql/migrations/0007_l0_inbound_auth.up.sql`; `backend/internal/auth/api_key_resolver.go`; `backend/internal/billing/claim_gate_integration_test.go:51`; `backend/internal/pool/db_adapters_integration_test.go` (synthetic id pattern per Codex evidence).
Lane: implementer (synthesis post-round-1)
Agent: Claude (claude-opus-4-7)
UTC timestamp: 2026-05-01T11:30:00Z
Citation discipline: Owner directive 2026-04-30 "所有的动作都不允许凭借自己的记忆库知识".

**Ready for N+4b1 implementation. BLOCKER: re-run integration_pg under Docker first to verify N+5b fingerprint fix is harmless before stacking N+4b on top.**
