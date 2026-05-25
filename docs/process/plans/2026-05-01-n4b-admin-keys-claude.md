# N+4b — Admin API-key Issuance + Ledger FK Backfill (Claude independent draft)

| Field | Value |
| --- | --- |
| Status | Drafted independently per CLAUDE.md #10 |
| Counterpart | `docs/process/plans/2026-05-01-n4b-admin-keys-codex.md` (parallel) |
| Lane | specifier (Claude) |
| Driver | A→B→C order picked 2026-04-30; B unblocks "ship to non-Owner users" — operators can issue keys via HTTP instead of hand-writing SQL |
| Predecessor | N+5b (commit `0b97880`); chained to N+4a (`121db58`) for `users` + `api_keys` schema |
| Migrations | 0009 (FK backfill), 0010 (admin auth + audit) — split per Codex pass-3 N+4a discipline |
| Citation discipline | Owner directive 2026-04-30; every claim cites repo file:line. No training-time recall. |

---

## Two-scope elevator

**Scope A**: Build `POST /admin/v1/api-keys` so operators can issue HUAKAI bearers without manually writing INSERT SQL into `api_keys`. Extras: `GET /admin/v1/api-keys` (list within tenant) + `POST /admin/v1/api-keys/{id}/revoke` (no DELETE).

**Scope B**: Add the foreign keys that N+4a deferred. Three billing tables have `api_key_id` and `user_id` columns with NO FK back to `api_keys` / `users`:
- `billing_ledger_claims.api_key_id`, `.user_id` ([0002_observability_billing.up.sql:29-30])
- `billing_ledger_archive.api_key_id` ([0002_observability_billing.up.sql:80])
- `usage_records.api_key_id`, `.user_id` ([0002_observability_billing.up.sql:125-126])

Test fixtures use synthetic ids (`apiKeyID = tenantID*100 + 1`, [claim_gate_integration_test.go:51]); they must seed real `users` + `api_keys` rows before the FKs go live.

---

## Goals (acceptance criteria)

1. `POST /admin/v1/api-keys` mints a key, bcrypt-hashes it, returns plaintext **once** in the response body, never logs or persists plaintext.
2. `GET /admin/v1/api-keys` lists tenant's keys (no plaintext, no key_hash; just metadata + masked prefix).
3. `POST /admin/v1/api-keys/{id}/revoke` flips `status='revoked'` + sets `revoked_at` + `revoked_reason`.
4. Admin endpoints require an admin credential mode (D1).
5. Admin actions log to `admin_audit_events` (new table).
6. Migration 0009 adds the 5 FKs; existing tests pass under FKs by seeding real rows instead of synthetic ids.
7. Smoke test stays green (it already seeds real api_keys after N+4a).
8. Integration tests cover: happy issue / list / revoke; revoked key fails to authenticate; cross-tenant probe rejected; rate-limit blocks key flood; non-admin caller rejected; bcrypt cost honored.
9. CMB-1 / CMB-5 / CMB-7 hold.

---

## Non-goals (explicitly deferred)

- **Admin UI** — only HTTP endpoints; UI is Phase E.
- **OAuth / SSO admin login** — Phase E. L0 admin auth uses table-backed admin credential.
- **Per-key fine-grained scopes** (read-only / write-only / specific endpoints) — N+5+ feature.
- **Bulk key import / CSV** — operator-tooling-later.
- **Key rotation atomic swap** — revoke + re-issue flow at L0; rotation-with-grace-window is later.
- **Admin RBAC roles** beyond a single `admin` capability — multi-tier admin (super-admin / tenant-admin / read-only ops) is L1.

---

## Decision points

### D1. Admin authentication mode

How does an operator authenticate to `POST /admin/v1/api-keys`?

**Option A**: Reuse `api_keys` with a new `is_admin boolean` column. Operator's bearer is a regular `hk_live_*` key flagged admin.

**Option B**: New `admin_credentials` table (separate hash store). Operator's bearer namespace is `hk_admin_*`.

**Option C**: Bootstrap operator credential via env var `HUAKAI_ADMIN_BOOTSTRAP_TOKEN`; first admin then uses the endpoint to issue more.

**Claude pick: Option B + Option C combined.** Reasoning:
- Mixing admin bearers with customer-data-plane bearers in `api_keys` is a footgun: the resolver path is hot, and a stray admin flag column in the hot lookup row is easy to misread later.
- Separate `admin_credentials` table mirrors the N+4a `api_keys` shape (bcrypt hash, prefix, status, expires_at) so reviewers don't learn a new pattern.
- Bootstrap token solves the chicken-and-egg: how do you issue the first admin key when no admin exists? Env-var bootstrap that issues exactly ONE admin key on first call, then deactivates the bootstrap path.

### D2. Key prefix length + entropy

**Default** (matches N+4a): 16-char prefix indexed for hot lookup, full token = `hk_live_<24-char-base32>` or `hk_test_<24-char-base32>` (32 chars total). Crypto/rand-derived. Bootstrap admin = `hk_admin_<24>`.

**Claude pick**: 24-char random suffix → 120 bits of entropy (well above 80-bit safe baseline). Base32 (no `0`/`O`/`1`/`I` ambiguity) over base64 because operators paste these by hand.

### D3. Plaintext display

**Option A**: Return plaintext in the issuance response body ONLY. Operator copies it from the JSON. If they navigate away, key is unrecoverable.

**Option B**: Return plaintext + a one-time-display URL (operator can navigate back once).

**Claude pick: Option A.** Reasoning: one-time URL adds session/cache infrastructure for a Phase E concern. Operator-facing CLI / curl can pipe to `jq` or save to file. Document the "key is shown once" warning in response body.

### D4. Issuance rate-limit

**Default**: 30 keys / hour / admin credential, enforced by `admin_audit_events` row-count window. Key flood would force an attacker who steals admin creds to either be slow or noisy.

**Claude pick**: ship the limit but at 30/h not 5/h — Owner mass-onboarding 50 friends at one go shouldn't hit it.

### D5. Audit event shape

New table `admin_audit_events`:
```
id, tenant_id, admin_credential_id, action ('issue_api_key'/'revoke_api_key'/'list_api_keys'/'admin_login'),
target_resource_type ('api_key'/...), target_resource_id, ip_inet inet, user_agent text,
outcome text CHECK IN ('ok','denied','error'), reason text, occurred_at timestamptz
```

Pattern matches existing audit tables (`oauth_refresh_audit_events` [0006:48], `rate_limit_audit_events` [0004:92]). Append-only.

### D6. Should PATCH/DELETE exist now?

**PATCH** (rename, change expires_at): defer to N+4c — not blocking customer onboarding.
**DELETE**: never. Use `revoke` (soft delete via status='revoked'). DELETE risks audit trail loss.
**REVOKE**: yes, ship in N+4b — operator must be able to invalidate stolen keys.

### D7. LIST endpoint

**Yes, ship it.** Operator dashboard's first feature is "what keys exist". Returns paginated rows with: id, name, key_prefix (16 chars only — already public), status, created_at, expires_at, last_used_at, revoked_at. Never key_hash, never plaintext.

### D8. RBAC scope

**L0 simplest**: every admin credential is global (can issue keys for any tenant). At L1 we'll partition by tenant ownership. Document this hard.

### DB1. FK migration shape — single or split?

**Option A**: Single 0009 — `ALTER TABLE billing_ledger_claims ADD FOREIGN KEY ... NOT VALID; ... VALIDATE CONSTRAINT ...`.
**Option B**: Split — 0009 adds NOT VALID, 0010 VALIDATEs.

**Claude pick: Option A (single).** Reasoning: HUAKAI is pre-customer per blueprint v0.2; no production data to walk over. NOT VALID + VALIDATE is the production-safe pattern but adds nothing here. Single migration is cleaner. Document that Option B becomes mandatory at L1.

### DB2. ON DELETE behavior

**Default**: `ON DELETE RESTRICT` for all 5 FKs. Reasoning:
- Money-grade tables (`billing_ledger_claims`, `usage_records`, `billing_ledger_archive`) are append-only audit ledgers. Deleting an `api_keys` or `users` row that's referenced from them must FAIL — that's a data-integrity invariant per F-OBS-001 §Invariant 4.
- `RESTRICT` (also: implicit `NO ACTION` deferred) blocks the delete with a clear error. Operators must `revoke` instead of `delete`.

### DB3. Composite FK on `(tenant_id, api_key_id)`

N+4a established `(tenant_id, user_id) → users(tenant_id, id)` composite FK to defend cross-tenant binding. Same logic applies to `api_key_id` columns: a billing claim row carrying `tenant_id=A, api_key_id=k` should fail FK if `k` belongs to tenant B.

**Claude pick**: composite FK on all five. Requires `(tenant_id, id)` unique index on `api_keys` (mirrors `uq_users_tenant_id_id` from N+4a). Add it in 0009.

### DB4. Test fixture cleanup before FK or after?

**Option A**: Same migration commit changes both schema + tests.
**Option B**: First commit changes tests to use real seeded ids (FK-safe but FK not yet enforced); next commit adds the FKs.

**Claude pick: Option A.** Same N+5a rationale: small focused change, single revert. Tests must compile before commit anyway, so they have to land together.

### DB5. Production rollout consideration

Confirmed: HUAKAI has no external customers per blueprint v0.2 (pre-L0). Destructive FK additions are cheap. Document as: "if at L1 there's any production data, the migration MUST switch to NOT VALID + VALIDATE pattern — this is a free pass at L0".

---

## Touch list

### Scope A — Admin endpoint

| File | Change |
|---|---|
| `backend/sql/migrations/0010_admin_auth.up.sql` (NEW) | `admin_credentials` + `admin_audit_events` tables |
| `backend/sql/migrations/0010_admin_auth.down.sql` (NEW) | DROP both |
| `backend/sql/queries/admin_keys.sql` (NEW) | sqlc queries: InsertAdminCredential, LookupAdminCredentialByPrefix, InsertAPIKey, ListAPIKeys, RevokeAPIKey, InsertAdminAuditEvent |
| `backend/internal/db/admin_keys.sql.go` | sqlc-generated |
| `backend/internal/admin/admin.go` (NEW) | Package doc + types |
| `backend/internal/admin/credentials.go` (NEW) | AdminResolver: similar to APIKeyResolver but for `admin_credentials` |
| `backend/internal/admin/issuer.go` (NEW) | KeyIssuer service: generate token, bcrypt, INSERT api_keys row, INSERT admin_audit_events |
| `backend/internal/admin/issuer_test.go` (NEW) | Unit tests with stubs |
| `backend/internal/admin/issuer_integration_test.go` (NEW) | Integration tests |
| `backend/internal/adminhttp/api_keys_handler.go` (NEW) | HTTP handlers: POST/GET/POST-revoke |
| `backend/internal/adminhttp/api_keys_handler_test.go` (NEW) | Unit tests |
| `backend/cmd/gateway/main.go` | Wire AdminResolver + KeyIssuer into deps; mount routes |
| `backend/internal/admin/bootstrap.go` (NEW) | Bootstrap-token issuance (one-time, env-driven) |

### Scope B — FK backfill

| File | Change |
|---|---|
| `backend/sql/migrations/0009_ledger_fk_backfill.up.sql` (NEW) | Composite uniq index `uq_api_keys_tenant_id_id`; 5 ALTER TABLE ADD FOREIGN KEY clauses on billing tables |
| `backend/sql/migrations/0009_ledger_fk_backfill.down.sql` (NEW) | DROP CONSTRAINT for each FK, DROP INDEX |
| `backend/internal/billing/claim_gate_integration_test.go` | Replace `apiKeyID = tenantID*100 + 1` with real seeded rows ([:51]) |
| `backend/internal/billing/settler_integration_test.go` | Same fix if present |
| Any other test using synthetic ids | Same fix |

---

## Code shape

### `admin_credentials` table (mirrors `api_keys` shape)

```sql
CREATE TABLE IF NOT EXISTS admin_credentials (
    id              bigserial PRIMARY KEY,
    name            text        NOT NULL,
    key_hash        text        NOT NULL,
    key_prefix      text        NOT NULL,
    -- 'admin' is the only role at L0; multi-tier comes later
    role            text        NOT NULL DEFAULT 'admin'
                    CHECK (role IN ('admin', 'super_admin', 'read_only')),
    status          text        NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'disabled', 'revoked')),
    bootstrap       boolean     NOT NULL DEFAULT false,
    expires_at      timestamptz,
    last_used_at    timestamptz,
    revoked_at      timestamptz,
    revoked_reason  text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);
CREATE INDEX idx_admin_credentials_prefix
    ON admin_credentials (key_prefix)
    WHERE deleted_at IS NULL AND status = 'active';
```

### `admin_audit_events` table

(see D5 shape above — append-only with the standard columns).

### Issuer service core

```go
func (i *KeyIssuer) Issue(ctx context.Context, req IssueRequest) (IssueResult, error) {
    // 1. RBAC check
    if req.Caller.Role != "admin" && req.Caller.Role != "super_admin" {
        i.audit(ctx, req, "denied", "non_admin_caller")
        return IssueResult{}, ErrAdminUnauthorized
    }
    // 2. Rate limit (D4)
    if err := i.checkIssueRate(ctx, req.Caller.ID); err != nil {
        i.audit(ctx, req, "denied", "rate_limited")
        return IssueResult{}, ErrAdminRateLimited
    }
    // 3. Generate token
    bearer, prefix, err := generateBearer(req.Env) // hk_live_<24> or hk_test_<24>
    if err != nil {
        return IssueResult{}, fmt.Errorf("%w: gen: %v", ErrAdminBackend, err)
    }
    // 4. bcrypt
    hash, err := bcrypt.GenerateFromPassword([]byte(bearer), bcrypt.DefaultCost)
    if err != nil {
        return IssueResult{}, fmt.Errorf("%w: bcrypt: %v", ErrAdminBackend, err)
    }
    // 5. INSERT in tx with audit row
    var apiKeyID int64
    err = i.tx(ctx, func(qtx *db.Queries) error {
        id, err := qtx.InsertAPIKey(ctx, ...)
        if err != nil { return err }
        apiKeyID = id
        return qtx.InsertAdminAuditEvent(ctx, ... action='issue_api_key', ok)
    })
    if err != nil {
        return IssueResult{}, fmt.Errorf("%w: insert: %v", ErrAdminBackend, err)
    }
    return IssueResult{
        APIKeyID: apiKeyID,
        Plaintext: bearer, // shown ONCE
        Prefix: prefix,
        ExpiresAt: req.ExpiresAt,
    }, nil
}
```

### HTTP handler

`POST /admin/v1/api-keys` body: `{"tenant_id":N, "user_id":N, "name":"...", "expires_at":"..."}`. Response: `{"id":N, "key":"hk_live_...", "key_prefix":"hk_live_xxx...", "expires_at":"..."}`. Header `X-Huakai-Key-Display: once-only` to remind operator.

### Bootstrap

`backend/internal/admin/bootstrap.go`:
- Reads `HUAKAI_ADMIN_BOOTSTRAP_TOKEN` env on boot
- If set AND `admin_credentials` is empty → INSERT a row with `bootstrap=true`, `key_hash=bcrypt(token)`, `name='bootstrap-admin'`
- Logs "bootstrap admin created; rotate immediately"
- After first issuance via this admin, `bootstrap=true` row should be marked `status='disabled'` (post-bootstrap cleanup, optional)

---

## Test plan

### Unit (no DB)

- `TestIssuer_RejectsNonAdmin` — caller role=customer → ErrAdminUnauthorized
- `TestIssuer_GeneratedBearerHasNamespacePrefix` — `hk_live_*` / `hk_test_*`
- `TestIssuer_PlaintextNotInResult` — IssueResult.Plaintext set; in audit event row only the prefix is recorded
- `TestBootstrap_InsertsOnceWhenTableEmpty`
- `TestBootstrap_NoOpWhenTableNonEmpty`
- `TestNormalize_PrefixDerivation` — first 16 chars

### Integration (`-tags=integration_pg`)

- `TestAdminIssue_HappyPath` — bootstrap admin issues key for new user; verify api_keys row + admin_audit_events row + key resolves via APIKeyResolver
- `TestAdminIssue_AdminAuthRequired` — no admin bearer → 401
- `TestAdminIssue_RateLimited` — issue 30 keys quickly; 31st returns 429
- `TestAdminList_TenantScoped` — list returns only that tenant's keys
- `TestAdminRevoke_HappyPath` — revoke flips status; resolver subsequently returns ErrUnauthorized
- `TestAdminRevoke_AlreadyRevoked` — idempotent
- `TestFKBackfill_BlocksDeleteOfReferencedAPIKey` — INSERT claim referencing api_key, then attempt DELETE api_key → FK error
- `TestFKBackfill_BlocksCrossTenantBilling` — claim with tenant_id=A but api_key_id of tenant B → FK error

### Migration regression

- After 0009 + 0010 applied, run all existing integration tests with `synthetic-id-replaced` fixtures green.
- Down migrations roll back cleanly.

---

## Risk matrix

| Risk | Trigger | Detection | Mitigation |
|---|---|---|---|
| Plaintext leaks into logs | Future logger threading mistakenly logs `IssueResult` whole | grep CI gate; CMB-5 review | `Plaintext` field marked with `// SECRET` comment + a struct method `String() string` that elides it |
| Bootstrap token reused | Operator forgets to rotate, env stays set | log warning + suggest rotation each boot | After first successful real-admin issuance, mark bootstrap row `disabled` automatically |
| FK migration breaks N+4a smoke | Smoke fixture seeds api_keys but billing tests run before smoke runs FK-safely | Test ordering matters — but each test seeds its own tenant + api_key | Each test owns its tenant; FK enforcement only sees rows in same tenant |
| Composite FK requires uniq index that doesn't exist | `uq_api_keys_tenant_id_id` not yet created when FK ALTER fires | migration error; halt | Create the index FIRST in 0009 before the ALTER ADD FOREIGN KEY |
| Existing claim/usage rows in dev DB violate the new FK | Old rows from N+4a/N+5a tests left behind in dev PG; ALTER ADD FK rejects | migration apply error | TRUNCATE billing_ledger_claims/usage_records/billing_ledger_archive at 0009 start (HUAKAI is pre-L0; no production data to preserve) |
| Admin endpoint exposed without auth | Misconfigured deps wire | curl → 200 means broken; integration test asserts 401 on no-bearer | `gateway_not_configured` 503 if `AdminResolver==nil` (mirror N+5b nil-guard) |
| Bcrypt cost too low | `bcrypt.DefaultCost` may be 10 (current) → fast brute force | unit test asserts cost=12+ | Set `bcrypt.DefaultCost` consistent with N+4a OR bump to 12 if industry hardening is wanted (but slower issuance) |
| Bootstrap env exposed in container logs | k8s configmap/secrets misconfig | env-printing CI gate; doc note | Doc must say: bootstrap token is a Secret, never a ConfigMap; recommend `op://...` or k8s Secret |

---

## CMB compliance

- **CMB-1 (Router does not read credentials)**: untouched.
- **CMB-5 (Credentials never logged)**: KeyIssuer audit event records only `key_prefix` (16 chars — already public-safe); never plaintext, never key_hash.
- **CMB-7 (Layer write discipline)**: admin package writes ONLY to `admin_credentials`, `api_keys`, `admin_audit_events`. Never to `billing_*` or `pool_*`. Reviewer checklist confirms.

---

## Sequencing

**Two commits**:
1. **N+4b1**: Schema 0009 (FK backfill) + fixture cleanup. Smoke + integration_pg green at end. Independent of admin endpoint work.
2. **N+4b2**: Schema 0010 (admin_credentials + admin_audit_events) + admin package + adminhttp + bootstrap + tests + main.go wiring + smoke seeding bootstrap admin.

Why split: FK backfill is risk-isolated (just a schema op + test cleanup), admin endpoint is risk-bigger (new HTTP surface + auth path). Independent reverts. Codex review pass between them.

---

## Estimated effort

| Step | Estimated |
|---|---|
| 0009 migration + test fixture cleanup | 1 hour |
| Codex review pass on N+4b1 + commit | 30 min |
| 0010 migration + admin package | 90 min |
| adminhttp handlers + tests | 90 min |
| Bootstrap + main.go wiring | 30 min |
| Integration tests (8 cases) | 90 min |
| Codex review pass on N+4b2 + fixes + commit | 30 min |
| **Total** | **~6.5 hours** |

---

## Open questions for Owner

1. Bootstrap secret: env var `HUAKAI_ADMIN_BOOTSTRAP_TOKEN` OR a separate `bootstrap.token` file? Env var is simpler, file is k8s-friendly via Secret-mounted-as-file. **Default**: env var; document the file pattern as alternative.
2. Bcrypt cost: keep `DefaultCost` (10) consistent with N+4a, OR bump to 12 for admin-only? Issuance is rare so the latency cost is acceptable. **Default**: keep 10 for shape consistency; bump can be a Phase E sweep.
3. Should N+4b1 and N+4b2 land in one PR but two commits, or two PRs? **Default**: two commits, one PR (atomic ship of "B is done"). Owner pick if you want them rolled out separately.

---

Source files read: docs/process/plans/2026-04-30-n4-l0-minimum.md, docs/process/plans/2026-04-30-n5b-handler-rewrite.md, backend/sql/migrations/0007_l0_inbound_auth.up.sql, backend/sql/migrations/0002_observability_billing.up.sql (lines 20–149), backend/sql/migrations/0001_pool_routing.up.sql (pool_slot_acquisitions block), backend/sql/migrations/0006_upstream_credential_management.up.sql (oauth_refresh_audit_events shape, lines 43–82), backend/sql/migrations/0004_rate_limiting.up.sql (rate_limit_audit_events shape, lines 88–119), backend/internal/auth/api_key_resolver.go, backend/internal/auth/api_key_resolver_integration_test.go, backend/internal/billing/claim_gate_integration_test.go (line 51 synthetic-id pattern), backend/cmd/gateway/main.go, docs/specs/_invariants/cross-module-boundaries.md.
Lane: specifier
Agent: Claude (claude-opus-4-7)
UTC timestamp: 2026-05-01T11:00:00Z
