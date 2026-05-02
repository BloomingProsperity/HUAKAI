# Account-to-API mainline audit

Date: 2026-05-02 (revised v2 same day per Owner directive)
Status: analysis only. No backend / admin / OpenAPI / main planning matrix file is modified by this document. Implementation changes wait for Owner direction after this audit.

**Naming note (v2 fix)**: The F-* IDs in this document use prefix `F-ACCAPI-*` (Account-to-API). The earlier draft used `F-A2A-*` which collides with LiteLLM's "Agent-to-Agent" protocol bridging row already on the backlog. The `ACCAPI` prefix is unambiguous.

## 0. Why this file exists

Owner directive 2026-05-02 quote:

> 请暂停只按"通用 AI Gateway / 中转网关"推进的思路，重新把 **Account-to-API（账号转 API）** 提升为 HUAKAI 的一级核心主线。
> ... provider account、routing、auth、billing 被拆开后，账号转 API 这条链路没有成为架构中轴 ...
> 上游账号是我的核心产品资产；本地 API key 是把这个资产产品化的出口。

The risk being flagged: by deep-diving 8 reference projects through "gateway" eyes (routing / auth / billing / observability as separate concerns), the synthesis is biasing HUAKAI toward looking like one more LLM relay instead of an account-asset productization platform. Owner wants the chain

```
local API key
  -> APIKeyBinding
  -> upstream account or account pool
  -> credential lease
  -> reverse proxy adapter
  -> upstream provider/account endpoint
  -> normalized response
  -> usage + billing + account state update
```

to be the architectural spine, not an emergent property of separate F-* features.

## 1. Owner's 9 concepts vs current code & plan

Coverage as of commit `9edb30b` (post N+4b2 admin endpoint set) + commit `4c2dd64` (Codex reference deep dives) + commit `e4d6a84` (Claude reviewer notes).

| # | Concept | Status | Where it lives | Gap |
| --- | --- | --- | --- | --- |
| 1 | `UpstreamAccount` modeled | ✅ exists | `backend/sql/migrations/0001_pool_routing.up.sql:108-156` table `provider_accounts` (17 columns: tenant_id / provider_id / channel_id / account_type / enabled / expires_at / health_state / credential_state / credentials jsonb / cap_concurrency / in_flight_count / cap_queue_sticky / cap_queue_fallback / queue_depth / priority / last_dispatch_at / model_allow_list / capability_flags / quota_*) | Conceptualized as "credential + capacity unit" (routing primitive) per `COMMENT ON TABLE` line 164. Not framed as core product asset. Service layer treats it as routing input not as user-visible inventory. |
| 2 | `CredentialLease` (token / session / cookie current usable credential) | ❌ **missing** | OAuth refresh + token caching live in F-AUTH-005 spec; `provider_accounts.credentials jsonb` is a static blob. `pool_slot_acquisitions` exists but it leases the concurrency *slot*, not the credential. | No entity that says "this request, this account, this token version, valid until T". No record of "credential X was used at request Y" except indirectly through audit. Multi-token rotation per single account (e.g. session cookie + access token + refresh token) is not first-class. |
| 3 | `CapabilitySnapshot` (account supports models / quota / membership / health) | ⚠️ partial / mutable | `provider_accounts.model_allow_list[]` + `capability_flags[]` + `cap_quota_total/daily/weekly` + `quota_status`. `model_registry` has `registry_version` for the global model catalog. | Account capabilities are mutable columns on the live row, not snapshotted at request time. There is no `account_capability_versions` table mirroring `registry_version`. Two requests landing on the same account 1ms apart can disagree on what models / quotas are available if an admin edits the row mid-request. F-POOL-001 stamps `pool_snapshot_version` per route plan, but per-account capability snapshot is not separately versioned. |
| 4 | `LocalAPIKey` | ✅ exists | `backend/sql/migrations/0007_l0_inbound_auth.up.sql` table `api_keys` + N+4b2 admin endpoint surface in `internal/admin/` and `internal/adminhttp/api_keys_handler.go` (committed 9edb30b). | Modeled as "customer credential into HUAKAI" — quota / status / prefix / hash / IP whitelist subset. **Not yet linked** to any upstream account. |
| 5 | `APIKeyBinding` (local key → upstream account or pool) | ❌ **critical missing** | No `api_key_bindings` table. Routing currently chains through tenant_id + (group via api_keys.user_id?) + model → pool selection inside `internal/router` + `internal/pool`. There is no persisted contract "key K always uses pool P or accounts {A, B, C}". | This is the architectural-spine gap. Without an explicit binding entity, you cannot: (a) sell "premium key bound to GPT-Plus pool" vs "free key bound to Gemini-CLI pool"; (b) audit "this key drained that account"; (c) implement per-key fallback policy distinct from tenant default; (d) revoke binding without revoking the key. |
| 6 | `ReverseProxyAdapter` (path mapping / header / auth / session injection / body rewrite / stream / error normalize) | ⚠️ partial / wrong layer | `internal/proto/AnthropicAdapter` exists; F-PROTO-002 spec released 2026-04-28. Streaming forwarder F-GW-002 spec released. | These are **protocol-level** adapters (Anthropic Messages ↔ OpenAI Chat ↔ Gemini ↔ Bedrock). They are NOT **account-aware reverse-proxy adapters** that take credentials from `provider_accounts.credentials jsonb` and inject them per request. The auth/header injection currently happens inline in gateway code — it is implicit, not an interface. There is no per-account-type adapter (OAuth web session vs API key vs service account vs upstream-static) with its own header/auth/session contract. |
| 7 | `AccountState` unified state machine (normal / cooling_down / expired / needs_refresh / needs_manual_recovery / quota_exhausted / disabled) | ⚠️ fragmented | `health_state` (5 values: operational/degraded/failed/cooling_down/error) + `credential_state` (5 values: valid/refreshing/refreshing_with_grace/refresh_failed/revoked) + `quota_status` (3 values: active/exhausted/paused) + `enabled` (boolean) + `expires_at` (timestamptz nullable) = 5 axes, no documented composition rule. | What does the scheduler do when `health_state='operational' AND credential_state='refresh_failed' AND quota_status='active'`? Today: implicit precedence inside `internal/pool/selector.go`. Owner's flat 7-state model is more user-visible. Operator UI cannot say "this account is `needs_refresh`" — it has to say three independent facts and let the operator infer. |
| 8 | Usage maps to (local API key, user, upstream account, account pool) | ⚠️ 3 of 4 | `backend/sql/migrations/0002_observability_billing.up.sql:121-189` table `usage_records` has `tenant_id` + `api_key_id` (line 125) + `provider_account_id` (line 127). Composite FKs to api_keys + provider_accounts via N+4b1 0009 backfill. | Missing **pool_group_id** in usage_records. Cannot answer "which Pool did this usage go through" without joining. Also missing `binding_id` (because §1.5 doesn't exist yet). |
| 9 | Admin end-to-end request trace view | ❌ missing | N+4b2 admin endpoint `/admin/v1/api-keys` shipped 2026-05-02 — only shows api_keys metadata. F-OBS-001 spec released framing of audit trail, but no concrete "trace request id → key → binding → account → credential → adapter → upstream → usage" UI/API. Codex deep-dive proposed `F-OBS-QUERY-001` at L2 (helicone-inspired). | The admin cannot, in one screen, answer "request abc-123 used which credential of which account in which pool, who paid, why was it routed there?" Today the operator has to grep audit logs and join 4 tables manually. |

## 2. Where current 03/17 plan and Codex deep dives are biased toward "generic gateway"

After re-reading Codex's 8 deep dives + Claude reviewer notes through Account-to-API eyes, the bias is visible:

- **Codex `feature-backlog-insertions-v2.md` ordering**: F-REQ-BODY-001, F-LOG-SAFE-001, F-UPSTREAM-RETRY-002, F-UPSTREAM-FALLBACK-001, F-ROUTER-HEALTH-001, F-ACC-SCHED-005, F-BILL-SESSION-001, F-BILL-SNAPSHOT-001, F-PAY-RECOVERY-001, F-PAY-REFUND-001, F-OBS-QUERY-001, F-RETENTION-001, F-BUDGET-SCOPE-001. Of the 8 highest-priority items, **0 are about Account-to-API binding or credential lease**. They are all about gateway-as-relay safety/correctness. This is correct gateway-engineering but does not advance the product spine.
- **Codex sub2api dive** correctly identifies sub2api as "strongest commercial reference for account-pool operations, payment lifecycle ..." but the F-IDs proposed (`F-ACC-SCHED-001..005`) describe scheduler internals (filters, scoring inputs, sticky routing, wait plan, outbox), not the `local key → binding → account → credential lease → adapter` spine.
- **Cross-repo Venn (Claude reviewer notes §5)** captured 30 capabilities but the Venn rows are routing / billing / quota / observability. There is no "key→account binding" row in the Venn, no "credential lease" row, no "account capability snapshot" row. These dimensions are HUAKAI-specific and not present in any of the 8 references — which is exactly why those references can't drive HUAKAI's spine.
- **Existing 03 matrix** has F-POOL-001 (pool aggregation), F-AUTH-005 (upstream credential refresh), F-KEY-001 (api key lifecycle), F-SESSION-001 (sticky session). The link `key → binding → pool` is **between** F-POOL-001 and F-KEY-001 but neither row owns it. The chain falls into the cracks.

The audit conclusion: **HUAKAI needs an explicit "Account-to-API" feature spine** that owns the chain, with separate F-IDs whose acceptance tests verify the chain end-to-end, not its parts.

## 3. Proposed feature spine (NEW, account-to-api oriented)

These are NEW F-* IDs that should sit ABOVE F-POOL-001 / F-AUTH-005 / F-KEY-001 in priority. They're not implementations themselves — they are the architectural contract.

| Feature ID | Name | Owns | L target |
| --- | --- | --- | --- |
| `F-ACCAPI-CORE-001` | Account-to-API mainline contract | The end-to-end chain `local key → binding → account/pool → credential lease → adapter → upstream → normalized response → usage + billing + account state update`. This is a META row whose acceptance tests verify the chain works as one piece, not one feature. | **L1** (architectural spine, must hold from MVP) |
| `F-ACCAPI-BIND-001` | API key binding entity | New table `api_key_bindings` mapping `(api_key_id) → (target_type, target_id)` where target_type ∈ {`account`, `pool_group`, `tenant_default`}. Includes binding state, fallback ordering, override policy. | **L1** |
| `F-ACCAPI-LEASE-001` | Credential lease | New entity capturing "request R, account A, credential version V, valid until T, used X tokens". Either a row in a `credential_leases` table OR a structured field on usage_records carrying `lease_token / credential_version / credential_kind`. Resolves "which token was actually used" forensically. | **L1** |
| `F-ACCAPI-CAP-SNAP-001` | Per-account capability snapshot | Versioned snapshot of `(account_id, capability_version, model_allow_list, capability_flags, quota_remaining, health_class)` so a request frozen at start has stable capability view even if admin edits the account row mid-request. Mirror of F-MODEL-001 registry version pattern. | **L2** (correctness improvement, not blocker for L1 single-version case) |
| `F-ACCAPI-CRED-INJECT-001` | Credential injector | Registry-dispatched contract that takes `provider_accounts.credentials jsonb` + a request being sent → injects auth header / cookie / Bearer / session / signature. Registry lookup is `(provider, credential_kind)` with fallback to `(*, credential_kind)`: e.g. `(anthropic, oauth_access)` impl differs from `(openai, oauth_access)` because OAuth bearers carry provider-specific session cookies / x-headers; the generic `oauth_access` impl is the floor. Distinct from F-PROTO-002 (which only translates body shape). Distinct from F-AUTH-005 (which manages credential lifecycle); CRED-INJECT-001 is the per-request mount point. | **L1** |
| `F-ACCAPI-ERR-CLASSIFY-001` | Error classifier | Takes raw upstream HTTP response → normalized HUAKAI error envelope + `AccountStateTransition` advice (e.g. 401 → needs_refresh, 429 → cooling_down with Retry-After, 402 → quota_exhausted). Drives the unified state machine in F-ACCAPI-STATE-001. Distinct from F-RATE-001 (which owns cooldown enforcement); CLASSIFY-001 is the input signal. | **L1** |
| `F-ACCAPI-ATTEMPT-001` | Upstream attempt audit | New table `request_attempts` (or `upstream_attempts`) capturing per-retry/per-fallback row: `request_id, attempt_number, provider_account_id, binding_id, credential_version, started_at, finished_at, status, error_class, retry_after_ms, transition_emitted`. Without this, multi-account retry/fallback chains are forensically opaque (admin can see "request failed" but not "tried account A v1, got 401, refreshed, tried v2, got 429, fell over to account B"). | **L1** |
| `F-ACCAPI-STATE-001` | Unified account state machine | Single computed state field exposed to operator UI + scheduler: `normal / cooling_down / expired / needs_refresh / needs_manual_recovery / quota_exhausted / disabled`. Computed deterministically from existing `health_state + credential_state + quota_status + enabled + expires_at`. Documented precedence. | **L1** |
| `F-ACCAPI-TRACE-001` | Admin end-to-end request trace | One admin endpoint `/admin/v1/requests/{request_id}` returning the full chain: api_key prefix + binding + account + credential lease + protocol adapter + credential injector + error classifier + every upstream attempt + normalized response shape + usage row + billing claim + audit events. Reads from F-ACCAPI-ATTEMPT-001 + F-OBS-001 audit trail. | **L2** |

Acceptance test direction (one shared AT bundle for the spine):
- `AT-ACCAPI-001` HappyPath: customer key → bound pool → schedule account → mint lease → adapter inject → upstream OK → usage row records (key, account, pool, lease) → billing settles → state machine reflects fresh `last_dispatch_at`.
- `AT-ACCAPI-002` BindingMismatch: customer key bound to pool A, model only in pool B → 404 NotFound with audit.
- `AT-ACCAPI-003` LeaseExpiry: lease points to credential v1, refresh has bumped to v2 mid-request → request fails with `needs_refresh`, account state machine flips, retry on v2 succeeds.
- `AT-ACCAPI-004` CapabilityDrift: admin removes model from account.allow_list while in-flight request running → request continues on snapshot v1, completes successfully.
- `AT-ACCAPI-005` InjectAndClassifyFailure: account_type=oauth, credential_state=refresh_failed → CRED-INJECT-001 rejects upfront, no upstream call. Or upstream returns 401 → ERR-CLASSIFY-001 maps to needs_refresh, account state machine flips, audit visible.
- `AT-ACCAPI-006` AdminTrace: a known request_id returns full chain.
- `AT-ACCAPI-007` AttemptAudit: a request that retries 3 times across 2 accounts produces 3 rows in `request_attempts` with correct credential_version per row; admin trace endpoint surfaces all 3 attempts in order.

## 4. Reconciliation with existing F-IDs

The new spine doesn't replace existing rows. It **owns the contract** and points down to existing rows for implementation:

| Spine row | Existing F-IDs that implement parts |
| --- | --- |
| F-ACCAPI-CORE-001 | F-GW-001 + F-GW-002 (forwarding), F-POOL-001 (pool selection), F-RATE-001 (cooldown), F-OBS-001 (audit) |
| F-ACCAPI-BIND-001 | (none — net-new, no existing F-ID owns this) |
| F-ACCAPI-LEASE-001 | F-AUTH-005 covers refresh; LEASE owns the per-request snapshot of which token was used |
| F-ACCAPI-CAP-SNAP-001 | F-POOL-001 has pool_snapshot_version; CAP-SNAP adds per-account version |
| F-ACCAPI-CRED-INJECT-001 | F-AUTH-005 covers credential lifecycle but NOT per-request injection. Net-new mount point. Composes with F-PROTO-002 (which translates body shape); spine does not re-own protocol concerns. |
| F-ACCAPI-ERR-CLASSIFY-001 | F-RATE-001 owns cooldown enforcement; CLASSIFY-001 is the input signal that drives transitions. F-AUTH-005 oauth-error sanitizer overlaps but stays at adapter boundary; CLASSIFY-001 outputs HUAKAI envelope. |
| F-ACCAPI-ATTEMPT-001 | F-OBS-001 audit covers events but NOT structured per-attempt rows. Net-new table. |
| F-ACCAPI-STATE-001 | health/credential/quota states from F-POOL-001 + F-RATE-001 + F-AUTH-005 — STATE-001 composes them |
| F-ACCAPI-TRACE-001 | F-OBS-001 audit + Codex's F-OBS-QUERY-001; reads ATTEMPT-001 rows |

## 5. Schema additions sketched (analysis only — no migration written yet)

### 5.1 `api_key_bindings` (revised v3 — Owner directive 2026-05-02)

Postgres FK does not support polymorphic targets, so a single `target_id` column with `binding_kind` discriminator forces FK validation into the service layer (lossy). HUAKAI uses explicit per-target columns + CHECK constraint:

- `id bigserial PK`
- `tenant_id bigint NOT NULL` (cross-tenant defense)
- `api_key_id bigint NOT NULL` with composite FK to `(tenant_id, id)` of api_keys
- `binding_kind text NOT NULL CHECK IN ('pool_group', 'provider_account', 'tenant_default')`
- `pool_group_id bigint` — FK to pool_groups when binding_kind = 'pool_group'
- `provider_account_id bigint` — FK to provider_accounts when binding_kind = 'provider_account'
- `tenant_default_token text` — fixed sentinel `'default'` when binding_kind = 'tenant_default'; allows uniqueness without a NULL discriminator
- CHECK constraint enforces exactly one of `(pool_group_id, provider_account_id, tenant_default_token)` is non-NULL per row, matching the binding_kind value
- Composite FK `(tenant_id, pool_group_id) → pool_groups(tenant_id, id)` (cross-tenant defense)
- Composite FK `(tenant_id, provider_account_id) → provider_accounts(tenant_id, id)` (cross-tenant defense)
- `priority integer NOT NULL DEFAULT 100` (lower = higher priority; multi-binding fallback order)
- `enabled boolean`, `created_at`, `updated_at`, `deleted_at`, `created_by_actor`, `last_modified_by_actor`
- **Three** partial-unique indexes — one per `binding_kind`, each scoped to the relevant target column. Postgres treats NULL columns inside a unique index as distinct values (pre-PG15 default), so a single combined index over the polymorphic columns leaks duplicate active bindings (Codex pass-12 P2). Per-kind partial indexes avoid the NULL-distinct trap:
  - `UNIQUE (tenant_id, api_key_id, pool_group_id) WHERE binding_kind = 'pool_group' AND deleted_at IS NULL`
  - `UNIQUE (tenant_id, api_key_id, provider_account_id) WHERE binding_kind = 'provider_account' AND deleted_at IS NULL`
  - `UNIQUE (tenant_id, api_key_id, tenant_default_token) WHERE binding_kind = 'tenant_default' AND deleted_at IS NULL`

**tenant_default behavior** (Owner directive): NEVER persist a binding with all three target columns NULL. When a customer key falls back to tenant default, the system writes an explicit `binding_kind='tenant_default'` row with `tenant_default_token='default'`. This way `usage_records.binding_id` is non-NULL for all post-spine traffic and admin trace can always answer "which binding was used?". `binding_id` is NULL ONLY for pre-migration historical rows.

**Multiplicity rule** (Owner-recommended L1 default per §10): one api_key has 1 PRIMARY binding + 0..N ordered FALLBACK bindings. The `priority` column defines the order. CLIProxyAPI uses 1-to-1; sub2api uses N-to-N at account_groups level — HUAKAI defaults to the disciplined middle ground.

### 5.2 Credential lease — L1 lightweight, L2 separate table (Owner-decided 2026-05-02)

**L1 lightweight** (decided): add lease fields to `usage_records` and `request_attempts` (§5.6) — no separate table:
- `binding_id bigint` (FK to api_key_bindings; tells us which contract resolved the account)
- `provider_account_id bigint` (already exists on usage_records since 0002)
- `credential_kind text` (e.g. 'oauth_access', 'api_key', 'service_account_jwt', 'session_cookie', 'upstream_static')
- `credential_version int` (the version of the credential record on `provider_accounts.credentials jsonb` at request time; bumped by F-AUTH-005 refresh)

The lease IS effectively `(binding_id, provider_account_id, credential_kind, credential_version)` per row. Forensics: "request R, what credential did we send" answers from a single row read.

**L2 promotion** (deferred): if operations need partial-lease forensics (e.g. "lease was acquired but request never settled — was the credential consumed?"), promote to a separate `credential_leases` table with: `id, request_id, binding_id, provider_account_id, credential_kind, credential_version, acquired_at, released_at, release_reason`. L2 only — admin can today reconstruct from request_attempts + usage_records.

### 5.3 `account_capability_snapshots`
- `id`, `tenant_id`, `provider_account_id`, `version int`, `taken_at`
- `model_allow_list text[]`, `capability_flags text[]`, `quota_remaining numeric(20,8)`, `health_class text`
- Snapshot taken at admin write time (mirrors `model_registry` version pattern from N+5a)
- Or computed-on-read with cache
- Open question: snapshot pull vs push

### 5.4 `usage_records` add `pool_group_id` + `binding_id` + lease fields
- Add column `pool_group_id bigint NULL` (which Pool the request resolved through)
- Add column `binding_id bigint NULL` (FK to api_key_bindings — the contract chosen)
- Add column `credential_kind text NULL`
- Add column `credential_version int NULL`
- Indexed by `(tenant_id, pool_group_id, settled_at DESC)`, `(tenant_id, binding_id, settled_at DESC)`
- Backfilled NULL for pre-migration rows. New rows MUST populate.

`binding_id` and `pool_group_id` are NULL-tolerant only for the migration window; once F-ACCAPI-BIND-001 lands they are required for new rows.

### 5.5 `account_state_view` (unified state derivation)
- Either a Postgres view computing the unified state from the 5 axes, OR a materialized column `account_state text` updated on each transition.
- View is cheaper but adds latency; materialized column requires triggers or app-level discipline.

### 5.6 `request_attempts` (per-attempt audit, F-ACCAPI-ATTEMPT-001)
- `id bigserial PK`
- `tenant_id bigint NOT NULL` (cross-tenant defense)
- `request_id text NOT NULL` (correlation key from chi middleware)
- `attempt_number int NOT NULL` (0-indexed; first attempt = 0)
- `binding_id bigint` (FK to api_key_bindings; nullable for tenant_default fallback)
- `provider_account_id bigint NOT NULL` (account this attempt targeted)
- `credential_kind text NOT NULL`, `credential_version int NOT NULL`
- `pool_group_id bigint NOT NULL`
- `started_at timestamptz NOT NULL`, `finished_at timestamptz`
- `upstream_status_code int` (HTTP status from upstream; NULL on local failure)
- `error_class text` (HUAKAI-normalized error class from F-ACCAPI-ERR-CLASSIFY-001: e.g. `upstream_5xx / upstream_4xx_auth / upstream_4xx_quota / upstream_429 / local_timeout / local_validate / network`)
- `retry_after_ms int` (parsed from upstream `Retry-After` if present)
- `state_transition_emitted text` (which AccountStateTransition this attempt triggered, if any)
- `created_at timestamptz`
- Composite indexes: `(tenant_id, request_id, attempt_number)`, `(provider_account_id, started_at DESC)`, `(error_class, started_at DESC)`
- Composite FK `(tenant_id, provider_account_id) → provider_accounts(tenant_id, id)` (cross-tenant binding defense, same pattern as N+4b1)
- Composite FK `(tenant_id, binding_id) → api_key_bindings(tenant_id, id)` when binding_id NOT NULL

Why this is L1: without this table, a multi-account retry/fallback flow leaves only one usage_records row + scattered audit events. Operator cannot reconstruct "we tried account A v1, got 401, refreshed to v2, retried, got 429 with Retry-After:60, fell over to account B v1, succeeded" from existing data.

Append-only. Settles before usage_records (Tx2 settlement reads request_attempts to know which account to credit).

## 6. Account-aware adapter design (revised v3 — Owner directive 2026-05-02)

The earlier draft had a single `ReverseProxyAdapter` keyed on `account_type`. Owner's correction: split into three orthogonal concerns because shape / credential / error are independent. Cross-product (account_type × provider) was the wrong slice. Of those three, **shape is already owned by released spec F-PROTO-002**, so the spine adds only the other two as net-new interfaces.

```
// (A) ProtocolAdapter — ALREADY OWNED by released F-PROTO-002 spec (2026-04-28).
// Spine references it; spine does not re-own it. No new F-ID.
//   TranslateRequest / TranslateResponse / StreamFrameNormalize
//   keyed on (client_protocol, upstream_protocol) pair.

// (B) CredentialInjector — F-ACCAPI-CRED-INJECT-001. Net-new.
// Registry lookup keyed on (provider, credential_kind) FIRST, with
// fallback to (*, credential_kind) generic impl. Rationale: an
// `oauth_access` token for Anthropic carries provider-specific
// session cookies / x-app-id / x-anthropic-version headers that an
// `oauth_access` token for OpenAI Codex does not, so a single
// credential_kind impl is too coarse. Provider-specific impls
// override the generic impl when registered.
type CredentialInjector interface {
    Provider() string         // 'anthropic' / 'openai' / 'gemini' / 'bedrock' / '*' (generic)
    Kind() string             // 'oauth_access' / 'api_key' / 'service_account_jwt' / 'session_cookie' / 'upstream_static'
    Inject(req *http.Request, creds Credentials) error
    Validate(creds Credentials) error                  // pre-flight check (e.g. JWT not expired); returns specific error class
    RedactForLog(creds Credentials) string             // structured redaction for audit/incident log
}

type CredentialInjectorRegistry interface {
    Register(inj CredentialInjector)
    // Resolve picks (provider, kind) impl first; falls back to ('*', kind);
    // returns ErrNoInjector if neither exists.
    Resolve(provider, kind string) (CredentialInjector, error)
}

// (C) ErrorClassifier — F-ACCAPI-ERR-CLASSIFY-001. Net-new.
// Provider-specific because Anthropic's `429` body shape differs from
// OpenAI's, and per-provider Retry-After header semantics differ.
type ErrorClassifier interface {
    Provider() string                                  // 'anthropic' / 'openai' / 'gemini' / 'bedrock'
    Classify(resp *http.Response, body []byte) ErrorClassification
}

type ErrorClassification struct {
    Class             string                 // HUAKAI taxonomy: 'upstream_5xx' / 'upstream_4xx_auth' / 'upstream_4xx_quota' / 'upstream_429' / 'invalid_request' / ...
    AccountTransition AccountStateTransition // suggested transition for F-ACCAPI-STATE-001 ('needs_refresh' / 'cooling_down' / 'quota_exhausted' / 'no_change')
    RetryAfterMs      int                    // parsed from upstream Retry-After / x-ratelimit-reset / per-provider header
    Retryable         bool                   // can the executor try another account / attempt?
    HumanMessage      string                 // operator-visible reason, secret-scrubbed
}
```

**Why two new pieces, not one** (PROTO already exists):
- A credential injector for `oauth_access` differs subtly per provider (Anthropic OAuth wants `x-anthropic-version` and a session cookie; OpenAI OAuth wants only Bearer). Provider-specific impls beat a single generic impl, but the generic floor must exist for upstream_static / api_key cases.
- An error classifier is provider-specific (status mapping + Retry-After parsing) but doesn't care about the credential kind.

Folding both into one `account_type`-keyed interface would force an N×M matrix; splitting yields O(M)+O(K) plus the registry fallback rule.

**Where they compose** (in `internal/gatewayhttp` Slice 5+ implementation):

```
client request
  -> F-PROTO-002 ProtocolAdapter.TranslateRequest
  -> F-ACCAPI-CRED-INJECT-001 CredentialInjector.Inject       (registry: provider + kind, fallback kind)
  -> F-GW-002 StreamForwarder.Send
  -> F-ACCAPI-ERR-CLASSIFY-001 ErrorClassifier.Classify       (per provider)
  -> F-ACCAPI-STATE-001 AccountState.Transition               AND/OR retry-fallback decision
  -> F-PROTO-002 ProtocolAdapter.TranslateResponse
  -> usage_records + request_attempts row
```

Today the gateway handler does only `ProtocolAdapter.Translate*` and ad-hoc credential injection inline. Slice 5 should land both new interfaces; otherwise the inline credential code becomes load-bearing and refactoring it later is the same 500+ LOC change Owner is trying to prevent.

Concrete impls: existing `internal/proto/` for protocol; new `internal/adapter/credential/` and `internal/adapter/errclass/` for the spine additions.

## 7. Admin UI / API surface implied

To make the spine operator-visible (Owner concept #9):

| Surface | Path | Returns |
| --- | --- | --- |
| List bindings for a key | `GET /admin/v1/api-keys/{id}/bindings` | array of api_key_bindings |
| Bind a key to a target | `POST /admin/v1/api-keys/{id}/bindings` body `{kind, target_id, priority}` | created binding |
| List accounts with unified state | `GET /admin/v1/provider-accounts?state=needs_refresh` | accounts filtered by F-ACCAPI-STATE-001 enum |
| Inspect a specific account | `GET /admin/v1/provider-accounts/{id}` | account + capability snapshot version + recent leases + recent usage |
| End-to-end request trace | `GET /admin/v1/requests/{request_id}` | spine chain (F-ACCAPI-TRACE-001) |
| Force account state transition | `POST /admin/v1/provider-accounts/{id}/state` body `{new_state, reason}` | manual recovery / disable / re-enable |

These are NOT yet in `cmd/gateway/main.go` route table (which currently has placeholder `notImplemented` for /admin/v1/pools, /admin/v1/provider-accounts, /admin/v1/usage). They should be added alongside or after the next admin slice.

## 8. What in the current implementation will drift away from the spine if we don't course-correct

Concrete risks if we proceed with current Codex / Claude backlog as-is:

1. **The next slice (Slice 5 real upstream)** will likely hard-code credential injection inline in the gateway handler because there's no `CredentialInjector` mount point and no `ErrorClassifier` mount point. Once that lands, refactoring later is a 500+ LOC change.
2. **Per-key cost tracking** can't be done correctly because there's no `api_key_bindings`. Today usage_records joins api_key_id → tenant → group → ... but cannot answer "this customer key has access to pools P1 and P2; how much did they spend on each?".
3. **Admin "rotate this account's credential"** flow can't be safe because there's no credential lease — admin can't see "this account has 3 in-flight requests holding credential v1; if I revoke v1 now, those requests fail mid-stream". The pool_slot_acquisitions table tracks slots not credentials.
4. **The Codex feature-backlog-insertions-v2.md "P0 / L1" items can land in parallel** (Owner directive 2026-05-02): F-REQ-BODY-001 (decompression guard) and F-LOG-SAFE-001 (panic / log sanitization) are independent of the spine — they touch ingress + log emission, not the account chain — so blocking them on spine progress is wrong. They can ship while spine schema work proceeds. The risk to watch is OTHER P0 items (`F-RESP-META-001`, `F-UPSTREAM-RETRY-002`, `F-UPSTREAM-FALLBACK-001`, `F-ROUTER-HEALTH-001`) which DO touch spine surfaces — those should wait until binding + attempt schema is in.
5. **The new candidate references CLIProxyAPI / OmniRoute** are both account-to-API-shaped products. If we mine them with gateway-eyes we'll miss what actually matters for HUAKAI's spine (CLIProxyAPI's "wrap CLI session into API endpoint" is ALMOST EXACTLY HUAKAI's use case).

## 9. Recommended minimum next action

**No big bang refactor. No new code in this audit.** Two parallel tracks (Owner directive 2026-05-02):

### Track 1 — Account-to-API spine (must precede Slice 5 real upstream)

1. **Owner approves this audit's framing**. If the spine framing is right, the rest follows. If wrong, fix it now before any code lands.
2. **Add 9 new F-* rows to `docs/03_FEATURE_PARITY_MATRIX.md`** under "HUAKAI-native features" (they don't have reference projects to mine — they're our spine). Protocol shape is NOT a new row; the spine references existing released spec F-PROTO-002:
   - F-ACCAPI-CORE-001 (META)
   - F-ACCAPI-BIND-001
   - F-ACCAPI-LEASE-001 (lightweight: lease fields on usage_records + request_attempts)
   - F-ACCAPI-CAP-SNAP-001
   - F-ACCAPI-CRED-INJECT-001
   - F-ACCAPI-ERR-CLASSIFY-001
   - F-ACCAPI-ATTEMPT-001
   - F-ACCAPI-STATE-001
   - F-ACCAPI-TRACE-001
3. **Add a section to `docs/02_HUAKAI_FUSION_ARCHITECTURE.md`** named "Account-to-API Mainline" describing the chain as the architectural spine.
4. **Open a DR** `DR-NNN-account-to-api-mainline` capturing: this audit, Owner approval, the precedence rule (F-ACCAPI-* spine before any spine-touching F-* row enters Slice 5).
5. **Insert ONE schema migration** `0011_accapi_spine.up.sql` adding:
   - `api_key_bindings` table (§5.1)
   - `usage_records.pool_group_id` column + index (§5.4)
   - `usage_records.binding_id` column + index (§5.4)
   - `usage_records.credential_kind` + `credential_version` columns (§5.4)
   - `request_attempts` table with composite tenant FKs (§5.6)

   This is the smallest migration that anchors the spine without breaking N+5b chat handler.
6. **Insert minimal admin endpoints**: `POST /admin/v1/api-keys/{id}/bindings` and `GET /admin/v1/api-keys/{id}/bindings` so binding becomes operationally visible. Defer issue/revoke flow refactor; defer state-transition admin actions.
7. **Defer to Slice 5/6**: F-ACCAPI-CRED-INJECT-001, F-ACCAPI-ERR-CLASSIFY-001, F-ACCAPI-CAP-SNAP-001, F-ACCAPI-TRACE-001. They land WITH real-upstream Slice 5 because they need provider HTTP traffic to be meaningful.
8. **Codex parallel plan (CLAUDE.md #10)**: before step 5 lands, both Claude and Codex draft `docs/plans/2026-05-NN-accapi-spine-{claude|codex}.md` independently, then cross-discuss. This audit document is Claude's pre-plan input. Do not execute step 5 until that round happens.

### Track 2 — Spine-independent P0 safety (can ship in parallel)

9. **F-REQ-BODY-001** guarded request decompression (Codex feature-backlog-insertions-v2 §P0). Touches ingress middleware only, independent of binding/account chain. Owner approved parallel ship.
10. **F-LOG-SAFE-001** panic + upstream error log sanitization. Touches log emission, independent. Owner approved parallel ship.

### What blocks what (precedence digest)

| Feature | Blocks on | Reason |
| --- | --- | --- |
| Slice 5 real upstream | Track 1 step 5 | Otherwise credential injection becomes hardcoded |
| F-RESP-META-001 debug headers | Track 1 step 5 | Headers reference binding_id / attempt_number |
| F-UPSTREAM-RETRY-002 | Track 1 step 5 | Retry needs request_attempts table |
| F-UPSTREAM-FALLBACK-001 | Track 1 step 5 | Same |
| F-ROUTER-HEALTH-001 | Track 1 step 5 | Health-aware select needs unified F-ACCAPI-STATE-001 |
| F-REQ-BODY-001 | (nothing) | Track 2 parallel |
| F-LOG-SAFE-001 | (nothing) | Track 2 parallel |

## 10. Open questions (Owner to resolve)

1. ~~Lease design~~ — **resolved in §5.2 by Owner**: L1 lightweight (fields on usage_records + request_attempts); L2 separate `credential_leases` table only if operations need it.
2. ~~Adapter granularity~~ — **resolved in §6 by Owner**: spine adds two net-new interfaces (CredentialInjector keyed on (provider, credential_kind) with generic-kind fallback; ErrorClassifier per provider). Protocol shape stays in F-PROTO-002, not duplicated as an F-ACCAPI row.
3. ~~Binding multiplicity~~ — **resolved in §5.1 by Owner-recommended default**: 1 primary + N ordered fallback per api_key, ordered by `priority` column.
4. ~~Tenant default binding~~ — **resolved in §5.1 by Owner**: NEVER persist NULL-target bindings. When fallback fires, write an explicit `binding_kind='tenant_default'` row with `tenant_default_token='default'`. `usage_records.binding_id` is always non-NULL for new traffic; NULL only for pre-migration historical rows.
5. **State machine transition authority**: admin-only (manual transitions), system-driven (background daemon flips states based on observed signals), or hybrid? Sub2API is hybrid. Recommended L1 default: hybrid — system flips to `cooling_down/needs_refresh/quota_exhausted` based on F-ACCAPI-ERR-CLASSIFY-001 signals; admin can manually flip to `disabled/needs_manual_recovery`.
6. **CLIProxyAPI as primary reference**: CLIProxyAPI is the closest existing project to HUAKAI's account-to-API spine. Should we open a Phase 2 specifier session against it BEFORE shipping spine schema, so we don't reinvent? Owner approved CLIProxyAPI as a new reference candidate earlier today. Recommended: yes, schedule alongside Track 1 step 4 DR.
7. **Codex meta-review P1 (verbatim schema names in claude-reviewer-notes.md §3.A)**: Codex flagged that listing exact LGPL ent column names (`target_user_id`, `completion_code_hash`, etc.) in a reviewer-lane file weakens lane separation. Per Owner's clean-room relaxation memory the same day (algorithm/state-machine details OK to capture; only verbatim copy still prohibited), the existing wording is allowable IF treated as behavior evidence. Recommendation: leave reviewer-notes as-is BUT mark §3.A explicitly as "specifier-derived behavior summary" so the lane status is unambiguous.
8. **Codex meta-review P2/P3** (3 minor findings still open from `b46mvsm55`): unverified leads marker for CLIProxyAPI/OmniRoute/Bifrost; pricing-promotion Venn citation fix; README-ack provenance link. All worth fixing in a follow-up cleanup commit; none block this audit.

## 11. Single-line summary

HUAKAI's current code + plan covers `provider_accounts` (✅ rich) + `api_keys` (✅ N+4b2) + protocol adapters (⚠️ shape only, no credential injection) + multi-axis account state (⚠️ fragmented) + multi-dim usage (⚠️ missing pool_group_id + binding_id), but **completely lacks** `api_key_bindings`, lease metadata, unified `AccountState`, the three-piece account-aware adapter (PROTO + CRED-INJECT + ERR-CLASSIFY), `request_attempts` audit, and the admin end-to-end trace endpoint. Without these, HUAKAI is one Slice-5 commit away from being structurally a generic gateway. Recommendation: run **two parallel tracks** — Track 1 ships 9 F-ACCAPI-* spine rows (PROTOCOL stays in F-PROTO-002) + 1 minimal migration (api_key_bindings with explicit pool_group_id/provider_account_id/tenant_default_token columns + CHECK + usage_records lease/binding/pool_group columns + request_attempts table) BEFORE Slice 5; Track 2 ships F-REQ-BODY-001 + F-LOG-SAFE-001 in parallel because they don't touch the spine.
