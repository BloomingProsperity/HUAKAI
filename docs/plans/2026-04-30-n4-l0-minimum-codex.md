# 2026-04-30 N+4 L0 Minimum - Codex Independent Plan

| Field | Value |
| --- | --- |
| Plan type | Independent Codex draft for parallel-plan discussion |
| Owner directive | "B 你所有的决策都要和codex讨论" |
| Work unit | N+4 L0 Minimum: schema 0009 for `api_keys` + `users`, bcrypt bearer hashing, retire `SmokeAuthResolver` |
| Current progress driver | Move HUAKAI from about 30% to about 40% by replacing smoke-only inbound auth with table-backed API key auth |
| Clean-room lane | Implementation plan; no upstream reference source needed or read |
| Claude plan status | A parallel Claude plan may exist, but this draft was written without reading it |

## 0. Ground Rules For This Plan

1. This plan is an independent Codex draft.
2. I did not read `docs/plans/2026-04-30-n4-l0-minimum-claude.md`.
3. I did not read any `docs/plans/*-claude.md` file.
4. This plan only uses HUAKAI internal docs, HUAKAI code, and local project rules.
5. No non-MIT reference project source is quoted or used.
6. The goal is actionable execution planning, not a broad auth redesign.
7. Schema and auth-core edits are high-risk per project rules; the plan records Owner decision points before execution.
8. Low-risk docs and tests are allowed once the synthesized plan is approved.
9. Implementation must preserve the existing Phase C smoke regression bar: end-to-end POST plus 5 PostgreSQL assertions.
10. The smoke path must stop relying on env-injected tenant/user/key IDs.

## 1. Observed Current State

1. `docs/02_HUAKAI_FUSION_ARCHITECTURE.md` marks N+4 as the next default session.
2. The same architecture doc says L0 commercialization is still 0%.
3. The same doc names N+4 as `0009 schema (api_keys + users)` plus bcrypt plus retiring `SmokeAuthResolver`.
4. `docs/specs/_invariants/cross-module-boundaries.md` defines the stable auth contract as `ResolveInboundAuth(ctx, *http.Request) (RequestContext, error)`.
5. That invariant also says Auth is read-only on `api_keys` and `users` in this phase.
6. `backend/internal/router/route_plan.go` already defines a `RequestContext` carrying `TenantID`, `UserID`, `APIKeyID`, and `RequestID`.
7. `backend/internal/auth/smoke_resolver.go` currently returns `SmokeIdentity`, not `router.RequestContext`.
8. `backend/internal/gatewayhttp/chat_completions_handler.go` depends directly on `*auth.SmokeAuthResolver`.
9. `backend/cmd/gateway/main.go` constructs the smoke resolver from `HUAKAI_SMOKE_*` environment variables.
10. `backend/internal/config/config.go` treats smoke auth env as optional for boot and lets the handler return 503 when missing.
11. `backend/cmd/gateway/smoke_test.go` seeds only `tenants`, providers, pools, channels, and provider accounts.
12. The smoke test then invents `apiKeyID` and `userID` in memory as arithmetic from `tenantID`.
13. The smoke test starts the gateway with `HUAKAI_SMOKE_BEARER_TOKEN`, `HUAKAI_SMOKE_TENANT_ID`, `HUAKAI_SMOKE_API_KEY_ID`, and `HUAKAI_SMOKE_USER_ID`.
14. `billing_ledger_claims.api_key_id`, `billing_ledger_claims.user_id`, `usage_records.api_key_id`, and `usage_records.user_id` already exist as plain bigint columns.
15. Those columns currently do not have foreign keys to user/key tables because those tables do not exist.
16. Current migrations are `0001` through `0006`.
17. The architecture roadmap reserves `0007` for Model Registry, `0008` for the three-ID chain, and `0009` for L0 API keys/users.
18. `backend/sqlc.yaml` uses PostgreSQL, sqlc, pgx/v5, JSON tags, DB tags, and explicit rename overrides.
19. Existing query files use both positional `$1` style and `sqlc.arg(...)` style.
20. Existing query files keep hot-path queries narrow and tenant-scoped.
21. `backend/go.mod` does not currently require `golang.org/x/crypto`.
22. Bcrypt in Go normally means `golang.org/x/crypto/bcrypt`, which would add a runtime dependency.
23. Adding a runtime dependency is high-risk per project rules, so Owner sign-off is needed for the exact password-hash algorithm/dependency decision.

## 2. Scope In

### 2.1 Files To Add

| File | Intent |
| --- | --- |
| `backend/sql/migrations/0009_l0_inbound_auth.up.sql` | Add `users` and `api_keys` tables for table-backed inbound auth. |
| `backend/sql/queries/inbound_auth.sql` | Add sqlc queries for API key lookup and, optionally, smoke/test seed helper reads. |
| `backend/internal/auth/inbound_resolver.go` | Implement table-backed bearer resolver using hash verification. |
| `backend/internal/auth/inbound_resolver_test.go` | Unit tests for header parsing, hash matching, revocation, expiry, tenant/user/key status, and no plaintext leakage. |

### 2.2 Files To Modify

| File | Intent |
| --- | --- |
| `backend/sqlc.yaml` | Add rename overrides only if new names generate poor Go identifiers. |
| `backend/internal/db/*` | Regenerate with sqlc after migration/query additions. |
| `backend/internal/gatewayhttp/chat_completions_handler.go` | Change dependency from smoke resolver to stable auth resolver interface returning request context. |
| `backend/cmd/gateway/main.go` | Wire DB-backed resolver instead of env smoke resolver. |
| `backend/internal/config/config.go` | Remove or deprecate smoke-only identity fields; keep only non-secret runtime config. |
| `backend/cmd/gateway/smoke_test.go` | Seed real users/API keys, hash bearer with bcrypt, start gateway without `HUAKAI_SMOKE_*` identity env. |
| `backend/go.mod` / `backend/go.sum` | Add `golang.org/x/crypto` only if Owner approves bcrypt. |
| `docs/02_HUAKAI_FUSION_ARCHITECTURE.md` | After implementation, mark N+4 status and update measured progress truthfully. |
| `docs/10_RISK_REGISTER.md` | Add or update inbound-auth specific risks if implementation reveals material risk. |

### 2.3 Files To Remove Or Retire

| File | Plan |
| --- | --- |
| `backend/internal/auth/smoke_resolver.go` | Prefer delete only after all references are gone and after Owner accepts removal risk. If deletion is considered too high-risk in the work unit, leave a compile-unused retired file only if package still compiles. |

My preference: delete `smoke_resolver.go` in the implementation commit because the goal explicitly says retire `SmokeAuthResolver`; however, deletion is high-risk under project rules, so this must be called out as an Owner decision.

### 2.4 Schema Design

#### 2.4.1 `users`

Proposed first-pass table:

```sql
CREATE TABLE IF NOT EXISTS users (
    id              bigserial PRIMARY KEY,
    tenant_id       bigint      NOT NULL REFERENCES tenants(id),
    email           text,
    display_name    text        NOT NULL DEFAULT '',
    status          text        NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'disabled', 'deleted')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    deleted_at      timestamptz
);
```

Indexes:

```sql
CREATE INDEX IF NOT EXISTS idx_users_tenant_status
    ON users (tenant_id, status)
    WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_users_tenant_email
    ON users (tenant_id, lower(email))
    WHERE email IS NOT NULL AND deleted_at IS NULL;
```

Rationale:

1. `tenant_id` is mandatory per DR-001.
2. `email` is nullable because N+4 smoke seeding does not require full signup/login.
3. `display_name` avoids forcing an identity-provider shape into L0.
4. `status` allows fail-closed disabled users before RBAC exists.
5. `deleted_at` follows existing soft-delete patterns.
6. No password column is added in N+4.
7. Avoiding password storage prevents confusing API key auth with end-user login.

#### 2.4.2 `api_keys`

Proposed first-pass table:

```sql
CREATE TABLE IF NOT EXISTS api_keys (
    id                  bigserial PRIMARY KEY,
    tenant_id           bigint      NOT NULL REFERENCES tenants(id),
    user_id             bigint      NOT NULL REFERENCES users(id),
    name                text        NOT NULL,
    key_hash            text        NOT NULL,
    key_prefix          text        NOT NULL,
    status              text        NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active', 'disabled', 'revoked', 'expired')),
    expires_at          timestamptz,
    last_used_at        timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    revoked_at          timestamptz,
    deleted_at          timestamptz
);
```

Indexes:

```sql
CREATE INDEX IF NOT EXISTS idx_api_keys_tenant_prefix
    ON api_keys (tenant_id, key_prefix)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_api_keys_user_status
    ON api_keys (tenant_id, user_id, status)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_api_keys_expires_at
    ON api_keys (expires_at)
    WHERE expires_at IS NOT NULL AND deleted_at IS NULL;
```

Optional uniqueness:

```sql
CREATE UNIQUE INDEX IF NOT EXISTS uq_api_keys_tenant_prefix_hash
    ON api_keys (tenant_id, key_prefix, key_hash)
    WHERE deleted_at IS NULL;
```

Rationale:

1. `key_hash`, not plaintext bearer, is stored.
2. `key_prefix` makes lookup bounded without storing plaintext.
3. Prefix lookup means a malformed token cannot force bcrypt comparisons against every row.
4. `tenant_id` is duplicated even though it is derivable through `users`; this keeps every primary table tenant-scoped and keeps hot-path filters tenant-aware.
5. `status`, `expires_at`, `revoked_at`, and `deleted_at` allow disable/rotate/expire semantics without needing the future key issuance endpoint.
6. `last_used_at` is useful operationally but must not be synchronously updated in the auth hot path in N+4 unless explicitly chosen; see decision D8.
7. No quota columns are added in N+4; quota enforcement belongs to a later L0/L1 slice.

#### 2.4.3 Foreign Key Strategy

My default recommendation: add immediate FKs from `users.tenant_id` to `tenants.id`, `api_keys.tenant_id` to `tenants.id`, and `api_keys.user_id` to `users.id`, but do not yet add FKs from billing/usage rows back to `api_keys(id)` and `users(id)`.

Reasons:

1. New tables can safely enforce internal consistency from day one.
2. Existing `billing_ledger_claims` and `usage_records` may already contain smoke rows with arbitrary `api_key_id` and `user_id` from older runs.
3. Adding FKs from existing ledger tables can fail on existing dev/prod data unless backfilled or cleaned.
4. Ledger rows are money/audit history; deleting API keys or users must not cascade into ledger history.
5. The safer sequence is N+4a new auth tables with enforced new-row correctness, then N+4b or later ledger FK backfill after data audit.

Explicit cascade policy:

1. No `ON DELETE CASCADE` from `users` or `api_keys` into billing, usage, or audit tables.
2. Prefer soft deletion for users and keys.
3. If hard deletion is ever allowed, use `ON DELETE RESTRICT` or no action for auth entities.
4. For `api_keys.user_id`, default `REFERENCES users(id)` is acceptable because users should not be hard-deleted while keys exist.
5. If SaaS deletion/GDPR workflows later require scrubbing PII, scrub `email`/`display_name`, not audit IDs.

#### 2.4.4 Tenant Consistency Constraint

PostgreSQL cannot enforce "api_keys.tenant_id equals users.tenant_id" with a simple FK unless `users` has a composite unique key.

Recommended N+4 shape:

```sql
CREATE UNIQUE INDEX IF NOT EXISTS uq_users_tenant_id_id
    ON users (tenant_id, id);
```

Then define:

```sql
FOREIGN KEY (tenant_id, user_id) REFERENCES users(tenant_id, id)
```

This avoids a class of cross-tenant key/user mismatches.

If sqlc/migration compatibility makes this cumbersome, keep a normal `user_id` FK in N+4a and add a test that the resolver joins on both `api_keys.user_id = users.id` and `api_keys.tenant_id = users.tenant_id`.

## 3. Auth Resolver Design

### 3.1 Public Shape

Create a stable interface in `internal/auth`:

```go
type InboundResolver interface {
    ResolveInboundAuth(ctx context.Context, req *http.Request) (router.RequestContext, error)
}
```

Or, to avoid importing `internal/router` into `internal/auth`, define `auth.RequestContext` and map it in the handler.

My preference: define `auth.RequestContext` in `internal/auth`, then later align the CMB contract with an alias or conversion. Reason: importing router from auth inverts the conceptual call order `Auth -> Registry -> Router`.

However, the invariant names `RequestContext` as the auth output. If implementation needs to avoid package cycles, use:

```go
type RequestContext struct {
    TenantID int64
    UserID   int64
    APIKeyID int64
}
```

Then `gatewayhttp` converts to the exact fields it already needs. Slice 2 can reconcile with router's `RequestContext`.

### 3.2 Error Model

Add typed errors:

1. `ErrMissingBearer`
2. `ErrInvalidBearer`
3. `ErrAuthUnavailable`
4. `ErrAPIKeyNotFound`
5. `ErrAPIKeyDisabled`
6. `ErrAPIKeyExpired`
7. `ErrUserDisabled`
8. `ErrTenantDisabled`

HTTP mapping in handler:

| Error | HTTP | Body code |
| --- | --- | --- |
| missing or malformed bearer | 401 | `unauthorized` |
| unknown key or hash mismatch | 401 | `unauthorized` |
| disabled/revoked/expired key | 401 or 403; prefer 401 for less oracle surface in N+4 | `unauthorized` |
| disabled user or tenant | 401 or 403; prefer 401 for less oracle surface in N+4 | `unauthorized` |
| DB unavailable | 503 | `auth_unavailable` |
| resolver nil/miswired | 503 | `gateway_not_configured` |

Security note: N+4 should not reveal whether the token prefix exists, whether the key is expired, or whether the user is disabled to clients. Tests can assert typed internal errors while HTTP stays generic.

### 3.3 Bearer Parsing

Rules:

1. Require exactly `Authorization: Bearer <token>`.
2. Trim outer whitespace around the token.
3. Reject empty token.
4. Reject token containing whitespace after trim.
5. Reject token longer than a configured maximum, proposed 256 bytes for N+4.
6. Reject token shorter than a minimum prefix+secret shape.
7. Do not log the token.
8. Do not include the token in returned errors.

### 3.4 Bearer Format

Recommended format for N+4:

```text
hk_live_<random>
hk_test_<random>
```

For N+4 smoke, a constant test token is acceptable only inside tests. The smoke seed should hash that token before inserting.

Prefix extraction:

1. Store a non-secret `key_prefix`, for example the first 12 to 16 visible chars or a generated prefix section.
2. Query candidates by prefix.
3. Run bcrypt comparison against returned candidate hashes.
4. If multiple candidates share prefix, compare each and accept exactly one match.
5. Keep prefix length long enough to bound candidate count.

Decision point D2 below asks Owner to pick the final bearer format.

### 3.5 Bcrypt Verification

Default if Owner approves bcrypt:

1. Use `golang.org/x/crypto/bcrypt`.
2. Store `bcrypt.GenerateFromPassword([]byte(token), cost)`.
3. Use cost 10 for development and N+4 smoke.
4. Make cost configurable only for tests if needed, not via production env in N+4.
5. Compare with `bcrypt.CompareHashAndPassword`.
6. Normalize all compare failures to generic unauthorized externally.

Risk:

1. Bcrypt is CPU-bound.
2. Prefix lookup is mandatory to avoid table-wide hash checks.
3. No async hash in the request path.
4. No plaintext or reversible storage.

### 3.6 Last-Used Updates

Do not update `api_keys.last_used_at` synchronously in the auth path in N+4 by default.

Reason:

1. CMB says Auth is read-only on `api_keys/users` in Phase E.
2. Hot-path writes from auth increase latency and create write amplification.
3. Billing and usage already create durable request records.
4. A later ops slice can update last-used through an outbox or sampled async worker.

If Owner wants immediate visibility, add a post-auth best-effort update as a separate decision because it violates the current "Auth read-only" invariant.

## 4. SQLC Query Plan

Add `backend/sql/queries/inbound_auth.sql`.

Primary query:

```sql
-- name: ListActiveAPIKeyCandidatesByPrefix :many
SELECT
    ak.id AS api_key_id,
    ak.tenant_id,
    ak.user_id,
    ak.key_hash,
    ak.status AS api_key_status,
    ak.expires_at,
    u.status AS user_status,
    t.status AS tenant_status
FROM api_keys ak
JOIN users u
  ON u.id = ak.user_id
 AND u.tenant_id = ak.tenant_id
JOIN tenants t
  ON t.id = ak.tenant_id
WHERE ak.key_prefix = sqlc.arg(key_prefix)
  AND ak.deleted_at IS NULL
  AND u.deleted_at IS NULL
  AND t.deleted_at IS NULL;
```

Implementation filters:

1. Key status must be `active`.
2. User status must be `active`.
3. Tenant status must be `active`.
4. Expiry must be null or in the future.
5. Bcrypt must match.

Possible query alternative:

1. Put status filters in SQL to reduce rows.
2. Keep some status values selected for test diagnostics.

My preference: filter soft-deleted rows in SQL, return statuses, and apply final semantic checks in Go. This yields better typed errors in unit tests without leaking to clients.

## 5. Handler Wiring Plan

### 5.1 Current Handler Change

Current:

```go
Auth *auth.SmokeAuthResolver
```

Planned:

```go
Auth auth.InboundResolver
```

Current call:

```go
ident, err := d.Auth.Resolve(ctx, r)
```

Planned call:

```go
ident, err := d.Auth.ResolveInboundAuth(ctx, r)
```

Then the rest of the handler continues using:

1. `ident.TenantID`
2. `ident.APIKeyID`
3. `ident.UserID`

No billing, pool, forwarder, or settler API should change in N+4.

### 5.2 Request ID

Do not force request ID into the N+4 auth result unless needed by Slice 2/3.

Reason:

1. Current handler does not pass request_id into billing yet.
2. Slice 3 owns the request_id/attempt_id schema chain.
3. Pulling request_id into N+4 risks scope creep.

If the resolver returns `router.RequestContext`, set only Tenant/User/APIKey and leave RequestID empty until Slice 3. If this conflicts with CMB wording, record as a N+4 limitation and fix in Slice 3.

### 5.3 Main DI

Current:

1. `deps.authSmoke *auth.SmokeAuthResolver`
2. `authSmoke` constructed from `cfg.Smoke*`
3. logger emits `smoke_auth_configured`

Planned:

1. `deps.authResolver auth.InboundResolver`
2. construct with `auth.NewDBInboundResolver(q, auth.ResolverConfig{...})`
3. remove `smoke_auth_configured` log field
4. optionally log `inbound_auth_backend="postgres"`

The gateway should boot if the database opens and migrations have been applied. If `api_keys` table is missing, first request returns 503 auth unavailable or boot fails depending on Owner choice D9.

My preference: boot succeeds if DB opens, request returns 503 if auth query fails due missing table. Smoke catches migration omissions. Production later can add startup migration checks.

## 6. Smoke Test Plan

### 6.1 Seed Graph Changes

Current smoke seed:

1. insert tenant
2. set `apiKeyID = tenantID*100 + 1`
3. set `userID = tenantID*100 + 2`
4. seed provider/pool/channel/provider account
5. pass fake identity through env

Planned smoke seed:

1. insert tenant
2. insert user row under tenant
3. bcrypt-hash the test bearer
4. insert API key row under `(tenant_id, user_id)`
5. seed provider/pool/channel/provider account
6. start gateway with only normal runtime env: database URL and listen addr
7. POST with `Authorization: Bearer <test-token>`
8. assert claim and usage rows carry the seeded `api_key_id` and `user_id`

### 6.2 Cleanup Order

Cleanup must account for new tables:

1. usage records
2. billing events
3. pool slot acquisitions
4. billing claims
5. provider accounts
6. channels
7. pool groups
8. providers
9. api_keys
10. users
11. tenants

If new FKs from billing rows to keys/users are deferred, this order still works.

If FKs from billing rows to keys/users are added, cleanup order becomes mandatory.

### 6.3 Regression Bar

Keep existing checks:

1. HTTP 200.
2. `Content-Type: text/event-stream`.
3. non-empty SSE body.
4. committed claim row.
5. one usage record.
6. one billing event.
7. provider account in-flight returns to seed value.
8. one released slot row.

Add checks:

1. claim row `api_key_id` equals seeded API key.
2. claim row `user_id` equals seeded user.
3. usage row `api_key_id` equals seeded API key.
4. usage row `user_id` equals seeded user.
5. no `HUAKAI_SMOKE_*` env vars are passed to the subprocess.

## 7. Out Of Scope In N+4

1. User registration endpoint.
2. Tenant registration endpoint.
3. API key issuance endpoint.
4. API key rotation endpoint.
5. API key quota or balance enforcement.
6. Password login.
7. OAuth login.
8. Session storage.
9. RBAC.
10. Admin UI for key management.
11. End-user dashboard.
12. Wallet, recharge, payment, balance deduction.
13. Model pricing table.
14. Model Registry / Slice 2.
15. request_id / attempt_id schema chain / Slice 3.
16. Real upstream adapter.
17. OpenAI client adapter.
18. Async last-used updater.
19. Key export.
20. Credential KMS envelope work for provider account credentials.
21. Any non-MIT reference-source study.

These are not removed from the product. They remain later L0/L1/L2 work.

## 8. Success Criteria

1. Migration `0009_l0_inbound_auth.up.sql` creates `users` and `api_keys` on a fresh database.
2. Migration applies cleanly after current `0001` through `0006`.
3. No plaintext bearer is stored in any table.
4. `api_keys.key_hash` stores bcrypt hashes if Owner selects bcrypt.
5. Auth resolver accepts a seeded valid bearer and returns real `tenant_id`, `user_id`, and `api_key_id` from PostgreSQL.
6. Auth resolver rejects missing bearer.
7. Auth resolver rejects malformed bearer.
8. Auth resolver rejects unknown bearer.
9. Auth resolver rejects disabled/revoked/expired key.
10. Auth resolver rejects disabled user.
11. Auth resolver rejects disabled tenant.
12. HTTP handler maps invalid bearer to generic 401 without leaking which condition failed.
13. HTTP handler maps DB/auth unavailability to 503.
14. `SmokeAuthResolver` is no longer used by `cmd/gateway` or `gatewayhttp`.
15. `HUAKAI_SMOKE_BEARER_TOKEN`, `HUAKAI_SMOKE_TENANT_ID`, `HUAKAI_SMOKE_API_KEY_ID`, and `HUAKAI_SMOKE_USER_ID` are no longer required for smoke success.
16. Smoke test seeds real user and API key rows.
17. Smoke test remains green with the existing 5 PostgreSQL state assertions.
18. Smoke test additionally proves claims/usage rows carry seeded auth IDs.
19. `go test ./internal/auth ./internal/gatewayhttp ./cmd/gateway` passes where build tags allow.
20. `go test -tags smoke ./cmd/gateway` passes against a migrated PostgreSQL dev database.
21. No logs or errors include the bearer token or hash.
22. No routing, pool, billing, or forwarder behavior changes except the identity source.
23. docs are updated truthfully after implementation.
24. Per-commit Codex review runs before commit.

## 9. Risk And Mitigation

| Risk | Impact | Mitigation | Test / Gate |
| --- | --- | --- | --- |
| Bcrypt cost too high on hot path | Auth becomes CPU bottleneck or probe vector | Prefix lookup before bcrypt; cost 10 for N+4; cap token length; benchmark if needed | Unit test candidate count; optional microbench |
| FK absence from billing rows to auth tables | Historical rows can reference deleted/nonexistent keys | Enforce FKs inside new auth tables now; defer ledger FKs until data audit; never cascade delete ledger | Plan D4/D5; smoke asserts new rows match |
| Smoke regression | 30% money path confidence drops | Change smoke seed first, keep existing PG checks, add auth-ID checks | `go test -tags smoke ./cmd/gateway` |
| Rollback unsafe after migration | Existing DB left with tables code no longer uses | Forward-only rollback by reverting code to smoke resolver while leaving unused tables; no destructive down migration in normal rollback | Rollback plan section |
| Multi-tenant probe attacks | Attacker learns key/user/tenant status through error differences or timing | Generic external 401; prefix lookup not tenant-scoped by caller; no status-specific client messages | Handler tests assert same body for invalid states |
| Migration on existing data | Adding FKs to old ledger rows could fail | Do not add ledger->auth FKs in N+4a; only add new tables | Migration test on current dev DB |
| Password storage anti-pattern | Team may treat users table as login-ready and add passwords casually | No password columns; auth is API key only; future login must use separate spec | Schema review |
| Plaintext key leakage | Bearer appears in DB/log/test failure | Store only hash + prefix; sanitize errors; avoid printing token in test fatal paths | grep for test bearer and logs |
| Prefix collision | Multiple candidate rows require multiple bcrypt compares | Use long enough prefix; compare all candidates; accept exact hash match only | Unit test same prefix two candidates |
| Auth writes violate CMB-7 | `last_used_at` update makes Auth write to DB | Do not update in N+4; defer async last-used | Code review against CMB |
| Dependency addition risk | `golang.org/x/crypto` introduces license/supply-chain review | Owner sign-off; dependency license audit if needed | Per-commit review plus dependency audit if requested |
| Missing migration at runtime | Gateway returns 500/503 unexpectedly | Smoke uses migrated DB; error mapped to 503 `auth_unavailable`; future startup check deferred | Handler integration test |

## 10. Decision Points Needing Owner Sign-Off

### D1: Hash Algorithm

Options:

1. Bcrypt via `golang.org/x/crypto/bcrypt`.
2. Argon2id via `golang.org/x/crypto/argon2` plus salt/parameter encoding.

Codex recommendation: bcrypt for N+4 because the Owner brief explicitly says bcrypt, Go support is straightforward, and the immediate threat model is API bearer hashing rather than password login. Revisit Argon2id for user passwords if/when password login exists.

Owner decision needed because this adds or confirms a runtime dependency and locks storage format.

### D2: Bearer Format

Options:

1. `hk_live_<random>` and `hk_test_<random>`.
2. OpenAI-like `sk-...`.
3. Opaque random without product prefix.

Codex recommendation: `hk_live_` / `hk_test_` because it is recognizable, avoids pretending to be another provider's key, and supports prefix lookup.

### D3: Prefix Length

Options:

1. Store first 12 chars.
2. Store first 16 chars.
3. Store a structured prefix before the last secret segment.

Codex recommendation: 16 chars for N+4 if using `hk_live_`/`hk_test_`, with a max candidate guard.

### D4: Auth Table FKs

Options:

1. Immediate FKs inside auth tables only.
2. Immediate FKs from billing/usage to users/api_keys too.
3. No FKs yet.

Codex recommendation: immediate FKs inside auth tables; defer ledger FKs.

### D5: Billing/Usage FK Timing

Options:

1. N+4a no ledger FKs, N+4b backfill + ledger FKs.
2. One big N+4 migration adds all FKs.

Codex recommendation: two sub-phases. Avoid breaking existing smoke/dev rows and avoid tying auth replacement to ledger history cleanup.

### D6: Migration Numbering

Options:

1. Respect architecture order: implement `0009` now while `0007`/`0008` are absent.
2. Rename to next physical number `0007`.
3. Add placeholder no-op `0007`/`0008` then `0009`.

Codex recommendation: use `0009_l0_inbound_auth.up.sql` only if the migration runner applies lexicographic files and missing `0007/0008` are acceptable. If the runner requires contiguous numbering, ask Owner whether to create placeholders or renumber roadmap.

This must be verified before execution.

### D7: `SmokeAuthResolver` Deletion

Options:

1. Delete the file and all smoke env config.
2. Leave file but unused with a deprecation comment.
3. Keep behind a build tag for emergency local testing.

Codex recommendation: delete from production build after tests pass. If Owner wants conservative rollback, keep behind test-only build tag.

### D8: `last_used_at`

Options:

1. Do not update in N+4.
2. Update synchronously on every successful auth.
3. Add async/event update.

Codex recommendation: do not update in N+4 because CMB-7 says Auth is read-only in this phase.

### D9: Missing Auth Table Runtime Behavior

Options:

1. Boot fails if auth tables are missing.
2. Boot succeeds but requests return 503.

Codex recommendation: boot succeeds, request returns 503 in N+4. Add startup migration checks later.

### D10: HTTP Status For Disabled/Expired Keys

Options:

1. Always 401.
2. 403 for known disabled/expired/revoked keys.

Codex recommendation: always 401 in N+4 to reduce account/key enumeration signals.

## 11. Concrete Execution Order

1. Write this independent Codex plan.
2. Exit and let Owner/Claude perform parallel-plan comparison.
3. Wait for synthesized authoritative plan.
4. Confirm D1 through D10 decisions.
5. Verify migration runner behavior for non-contiguous `0009`.
6. Create `backend/sql/migrations/0009_l0_inbound_auth.up.sql`.
7. Add `users` table with tenant FK, status, timestamps, soft delete.
8. Add `api_keys` table with tenant/user relationship, key hash, key prefix, status, expiry, timestamps, soft delete.
9. Add indexes for prefix lookup, user/status lookup, expiry lookup.
10. Add comments documenting no plaintext key storage.
11. Add `backend/sql/queries/inbound_auth.sql`.
12. Run sqlc generation using the project's existing command or documented workflow.
13. Add or adjust sqlc renames only if generated names are poor.
14. Add auth resolver types and typed errors.
15. Implement bearer parsing.
16. Implement prefix extraction.
17. Implement DB candidate lookup.
18. Implement bcrypt compare.
19. Implement status/expiry checks.
20. Ensure all external auth failures normalize to non-leaking errors.
21. Write resolver unit tests.
22. Change `ChatHandlerDeps.Auth` to an interface.
23. Change handler error mapping from smoke errors to new auth errors.
24. Keep billing/pool/forwarder/settler code unchanged.
25. Wire DB resolver in `cmd/gateway/main.go`.
26. Remove smoke env identity config from `Config` or mark unused if deletion deferred.
27. Remove smoke env injection from smoke test subprocess.
28. Update smoke seed to insert user and API key rows.
29. Generate bcrypt hash for the smoke bearer in test setup.
30. Add smoke assertions for real auth IDs in claims and usage records.
31. Run targeted unit tests.
32. Run sqlc compile/build tests.
33. Run smoke test against migrated PostgreSQL.
34. Update architecture doc status after tests pass.
35. Update risk register if any material risk remains open.
36. Stage changes.
37. Run `codex exec review --uncommitted --full-auto` from repo root per CLAUDE.md #8.
38. Fix HIGH findings.
39. Repeat review if fixes are material.
40. Commit with review verdict in commit body.
41. Optionally run post-commit `codex exec review --commit <SHA> --full-auto`.
42. Report Owner summary in Chinese.

## 12. Test Plan

### 12.1 Unit Tests

`backend/internal/auth/inbound_resolver_test.go` should cover:

1. nil resolver returns unavailable.
2. missing Authorization returns unauthorized.
3. non-Bearer scheme returns unauthorized.
4. empty Bearer returns unauthorized.
5. bearer with internal whitespace returns unauthorized.
6. overlong bearer returns unauthorized.
7. unknown prefix returns unauthorized.
8. known prefix with bcrypt mismatch returns unauthorized.
9. known prefix with bcrypt match returns tenant/user/api_key IDs.
10. disabled key returns unauthorized.
11. revoked key returns unauthorized.
12. expired key returns unauthorized.
13. soft-deleted key returns unauthorized.
14. disabled user returns unauthorized.
15. soft-deleted user returns unauthorized.
16. disabled tenant returns unauthorized.
17. two keys with same prefix: resolver finds matching hash.
18. no returned error includes raw bearer.

### 12.2 Handler Tests

If existing handler tests are light, add focused tests around auth mapping:

1. invalid bearer maps to 401 generic response.
2. auth DB unavailable maps to 503.
3. valid auth enters existing request validation path.

### 12.3 Smoke Test

Run:

```powershell
go test -tags smoke ./cmd/gateway
```

Only after applying migrations to the dev PostgreSQL database.

### 12.4 Static Checks

Run:

```powershell
go test ./internal/auth ./internal/gatewayhttp ./cmd/gateway
```

Then broaden if fast:

```powershell
go test ./...
```

If AppLocker/SAC requires the project wrapper mentioned by the architecture doc, use the existing wrapper instead of raw `go test`.

## 13. Per-Commit Cross-Review

CLAUDE.md #8 requires:

```text
codex exec review --uncommitted --full-auto
```

Application here:

1. Stage all implementation, migration, generated sqlc, tests, and docs.
2. Run the uncommitted review from repo root.
3. Treat HIGH findings as blocking.
4. Fix HIGH findings before commit.
5. MED findings must be fixed or explicitly documented in the commit body.
6. LOW findings may be deferred with a note.
7. Commit body should state the review command and verdict.
8. Because this touches schema and auth core, consider a post-commit review too.

Escalation:

1. If review finds clean-room concern, stop.
2. If review finds auth bypass, stop.
3. If review finds billing identity drift, stop.
4. If review finds migration cannot apply to existing data, stop and ask Owner.

## 14. Rollback Plan

### 14.1 Code Rollback

Rollback by reverting:

1. `backend/internal/auth/inbound_resolver.go`
2. `backend/internal/auth/inbound_resolver_test.go`
3. `backend/internal/gatewayhttp/chat_completions_handler.go`
4. `backend/cmd/gateway/main.go`
5. `backend/internal/config/config.go`
6. `backend/cmd/gateway/smoke_test.go`
7. generated sqlc files from inbound auth queries
8. `backend/go.mod` / `backend/go.sum` dependency changes if bcrypt was the only consumer

### 14.2 Schema Rollback

Do not run destructive rollback by default.

Recommended rollback posture:

1. Leave `users` and `api_keys` tables in place.
2. Revert code to previous smoke-auth behavior only if absolutely needed.
3. Mark `0009` as unused by reverted code.
4. Because no old tables are altered in the default plan, leaving new tables is low operational risk.

If Owner explicitly requires a down migration, write it only after confirmation because dropping auth tables is destructive.

### 14.3 Regression Bar

Rollback is only acceptable if:

1. previous smoke test behavior is restored, or
2. new table-backed smoke remains green after partial rollback, and
3. billing claims/usage still write tenant/user/api_key IDs consistently, and
4. no plaintext bearer is introduced as a fallback.

## 15. Sequencing Thoughts

### 15.1 Should L0 Minimum Ship Before Slice 2 Model Registry?

My recommendation: yes, ship N+4 L0 minimum before Slice 2.

Reasons:

1. The current handler already accepts `pool_group_id` directly.
2. Slice 2 improves model resolution and routing shape, but it does not solve the biggest commercialization gap: end-user API keys.
3. Table-backed inbound auth unlocks real tenant/user/key identity for every later slice.
4. Billing rows already carry `api_key_id` and `user_id`; those IDs should become real before more routing complexity depends on them.
5. A small auth migration is easier to reason about before introducing Model Registry and handler/router changes.
6. The risk of continuing with env-injected identity is higher than the risk of deferring Model Registry one slice.

Constraint:

1. N+4 must not add routing behavior.
2. N+4 must not block Slice 2's future `Router.Plan` transition.
3. Auth result shape should be compatible with router `RequestContext`.

### 15.2 Bcrypt Or Argon2id?

My recommendation: bcrypt for N+4 API bearer hashing.

Reasons:

1. Owner brief explicitly names bcrypt.
2. Bcrypt has a simple Go API and mature storage format.
3. API keys are high-entropy random secrets; the main need is non-reversible storage, not human-password hardening.
4. Argon2id is a stronger password-hashing choice, but it requires parameter encoding, salt handling, and more tuning decisions.
5. N+4 should avoid expanding into a password-login design.

Guardrail:

1. Do not use bcrypt as an excuse to add user password storage.
2. If password login enters scope later, re-open Argon2id as a separate decision.

### 15.3 One Big Migration Or Two Sub-Phases?

My recommendation: two sub-phases.

N+4a:

1. Add `users`.
2. Add `api_keys`.
3. Add internal auth-table FKs.
4. Add table-backed resolver.
5. Update smoke.
6. No ledger FK backfill.

N+4b:

1. Audit existing claim and usage rows.
2. Decide how to handle old smoke rows with fake IDs.
3. Add ledger/usage FKs if desired.
4. Add key issuance endpoint or admin seed path if Owner wants operational key creation next.

Reason:

1. It reduces blast radius.
2. It keeps smoke green while real identity lands.
3. It avoids turning auth replacement into a historical data cleanup task.
4. It respects the fact that billing ledger is high-risk.

## 16. Assumptions

1. The implementation will not read upstream reference source.
2. PostgreSQL is the only supported DB.
3. Migrations are forward-only in normal operation.
4. Existing migration tooling can apply a file named `0009` even if `0007` and `0008` are absent, or Owner will approve placeholders/renumbering.
5. Smoke tests run against a disposable dev database or a database safe for test row cleanup.
6. API key creation endpoint is not required in N+4.
7. Test seed code may generate bcrypt hashes directly.
8. `golang.org/x/crypto` is acceptable if Owner approves bcrypt.
9. No production secrets are present in the repo.
10. Existing billing/pool behavior must remain unchanged.

## 17. Non-Shrinkage Check

No feature is removed from the roadmap.

Preserved for later:

1. Tenant signup/login.
2. API key issuance UI/API.
3. Per-key quota.
4. Payment/recharge/balance.
5. RBAC.
6. User-facing auth providers.
7. Model Registry.
8. Executor extraction.
9. Real upstream adapter.
10. Admin operations UI.

N+4 only replaces a test stand-in with the first production-shaped inbound auth read path.

## 18. Clean-Room Check

1. This plan uses HUAKAI internal docs/code only.
2. No non-MIT source code was read.
3. No upstream implementation details are copied.
4. No distinctive upstream schema or identifier is used.
5. The proposed schema follows HUAKAI's existing tenant-scoped PostgreSQL style.
6. The proposed resolver follows the HUAKAI CMB auth contract.

## 19. Security Check

Security posture:

1. Store hash, not plaintext bearer.
2. Store prefix only for lookup.
3. Generic external 401 for all auth denial reasons.
4. No password column.
5. No key material in logs.
6. No auth hot-path writes by default.
7. No cascade delete into ledger.
8. Disabled tenant/user/key fail closed.

Known remaining gaps:

1. No API key rotation endpoint yet.
2. No key issuance endpoint yet.
3. No operator audit event for key creation in N+4 if seed-only.
4. No per-key quota yet.
5. No brute-force rate limit on inbound auth yet.

These gaps should be tracked, not hidden.

## 20. Owner Confirmation Needed Before Execution

Owner should explicitly confirm:

1. D1 hash algorithm and dependency addition.
2. D2 bearer format.
3. D4/D5 FK strategy.
4. D6 migration numbering.
5. D7 whether to delete or test-gate `SmokeAuthResolver`.
6. D8 whether `last_used_at` stays deferred.
7. D10 whether disabled/expired keys return generic 401.

After those decisions, implementation can proceed as a bounded N+4a slice.

## 21. Planned Final Owner Report Shape

After execution, final report should include:

1. 做了什么.
2. 改了哪些文件.
3. 为什么这样做.
4. 有没有功能缩水.
5. 有没有 clean-room 风险.
6. 有没有安全风险.
7. 哪些地方需要 Owner 确认.
8. 下一步建议.

## 22. Required Output Tail

Source files read:

- `docs/02_HUAKAI_FUSION_ARCHITECTURE.md`
- `docs/specs/_invariants/cross-module-boundaries.md`
- `backend/internal/auth/smoke_resolver.go`
- `backend/internal/gatewayhttp/chat_completions_handler.go`
- `backend/cmd/gateway/main.go`
- `backend/cmd/gateway/smoke_test.go`
- `backend/sql/migrations/0001_pool_routing.up.sql`
- `backend/sql/migrations/0002_observability_billing.up.sql`
- `backend/sql/migrations/0004_rate_limiting.up.sql`
- `backend/sql/migrations/0006_upstream_credential_management.up.sql`
- `backend/sql/queries/billing_claims.sql`
- `backend/sql/queries/billing_settle.sql`
- `backend/sql/queries/auth_credentials.sql`
- `backend/sql/queries/auth_storm.sql`
- `backend/sqlc.yaml`
- `backend/internal/config/config.go`
- `backend/internal/router/route_plan.go`
- `backend/internal/auth/auth.go`
- `backend/internal/auth/auth_test.go`
- `backend/go.mod`
- `docs/01_PROJECT_BRIEF.md`
- `docs/03_FEATURE_PARITY_MATRIX.md`
- `docs/10_RISK_REGISTER.md`
- `docs/12_AGENT_WORKFLOW.md`
- `docs/15_RELEASE_GATES.md`
- `CLAUDE.md`
- `.agents/skills/pm-orchestrator/SKILL.md`
- `.agents/skills/api-gateway-risk-review/SKILL.md`

Files explicitly not read:

- `docs/plans/2026-04-30-n4-l0-minimum-claude.md`
- any `docs/plans/*-claude.md`

Lane: implementation plan

Agent: Codex (GPT-5 session)

UTC timestamp: 2026-04-30T07:19:54.7557088Z
