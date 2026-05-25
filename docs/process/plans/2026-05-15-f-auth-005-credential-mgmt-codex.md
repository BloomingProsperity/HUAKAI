# 2026-05-15 F-AUTH-005 upstream credential management Codex plan

| Field | Value |
| --- | --- |
| Owner directive | "Write CODEX plan for HUAKAI F-AUTH-005 (upstream credential management with multi-auth-mode support) — Go backend." |
| Authorization | Owner 2026-05-15 全 session 授权 |
| Lane | SPECIFIER |
| Independence rule | Do not read `docs/process/plans/2026-05-15-f-auth-005-credential-mgmt-claude.md`; this plan was drafted without reading it. |
| Target feature | F-AUTH-005 upstream Provider Account credential management |
| Implementation status | Plan only. No code, schema, generated files, tests, or commit. |
| Observed regions | 18 |
| Inferences | 11 |
| Open questions | 5 Owner OCAW |

## Scope

本计划把已经 Released 的 F-AUTH-005 从"provider account credentials JSONB + 部分 OAuth refresh worker"推进到可实现的 Go backend 方案，覆盖 Owner 2026-05-15 survey 指定的 3 vendor x 5 auth modes:

- `anthropic`: `api_key`, `claude_ai_oauth`, `claude_code`, `bedrock`, `vertex_anthropic`
- `openai`: `api_key`, `chatgpt_oauth`, `codex_cli_oauth`, `azure`, `refresh_token`
- `gemini`: `aistudio_api_key`, `vertex_sa`, `code_assist`, `google_one`, `antigravity`

In scope:

- Go backend credential storage model, especially new `account_credentials` table and encrypted-at-rest payload.
- Provider-account runtime credential resolution for request path.
- Background refresh/rotation pipeline for OAuth, cloud STS/token exchange, and static credential state.
- Auth-mode validation, per-mode lifecycle state, refresh/failure classification, and fallback semantics.
- Admin create/rotate/disable/reveal-deny API shape at planning level.
- Tests needed before implementation can claim F-AUTH-005 expanded coverage.

Out of scope:

- No first-login OAuth bootstrap UX or token acquisition ceremony. That remains F-AUTH-006 unless Owner explicitly merges it into this slice.
- No real credential handling, no live upstream call, no Owner secret read.
- No frontend implementation, except API assumptions for future UI.
- No non-MIT reference source reading. `sub2api` is only acknowledged as Owner-approved survey/reputation context; this plan does not add new upstream mechanism claims.
- No implementation or commit in this session.

## Current Backend Facts From Source Reading

- F-AUTH-005 spec is Released and defines cache, refresh decision, three-scope storm budget, CAS persistence, audit, token leakage discipline, and tests AT-AUTH-005-001..020.
- Current base schema stores credential JSON on `provider_accounts.credentials`, with `account_type` initially restricted to `oauth`, `api_key`, `service_account`, `upstream_static`.
- Migration `0006_upstream_credential_management.up.sql` adds `token_version`, `refresh_token_fingerprint`, `last_refresh_*`, `oauth_refresh_audit_events`, `oauth_storm_budget`, and `mimicry_policy`.
- Migration `0011_protocol_family_session_extension.up.sql` later allows `account_type=session`.
- Current `credentialworker` has a scheduler, adapter registry, and Postgres CAS writeback, but it scans `provider_accounts`, not a dedicated credential table.
- Current `auth.StormController` implements account-scope only; endpoint/global scope are explicit deferred panic paths.
- Current `PostgresCredentialVault` reads `provider_accounts.credentials` directly and maps `api_key`, `oauth`, `service_account`, `upstream_static`, `session`, and `aws_sigv4` into runtime `provider.Credential`.
- Current admin provider-account handler accepts raw `credentials` JSON and validates only account type and object shape. It does not model `auth_mode`, encryption envelope, or per-mode schemas.

## File-By-File Impact

### Schema and sqlc

- `backend/sql/migrations/0015_account_credentials.up.sql` new, high-risk schema:
  - Add `account_credentials` as the source of truth for secret payloads.
  - Keep existing `provider_accounts.credentials` for compatibility during migration, but stop writing new plaintext secrets there after cutover.
  - Add unique active credential constraint on `(tenant_id, provider_account_id, vendor, auth_mode)` where `deleted_at IS NULL AND state IN (...)`.
  - Add encrypted payload, key metadata, lifecycle state, version/CAS, expiry, refresh policy, failure classification, and fingerprints.
- `backend/sql/migrations/0015_account_credentials.down.sql` optional if migration discipline requires it:
  - Because schema changes are high risk, down migration should be conservative and refuse if any active encrypted credential exists.
- `backend/sql/queries/account_credentials.sql` new:
  - Load active credential by tenant/account/vendor/mode with `FOR UPDATE` for refresh.
  - CAS update encrypted payload by `credential_version`.
  - List refresh candidates by `refresh_before_at`, `state`, and `mode`.
  - Mark temp/permanent failure without selecting ciphertext.
- `backend/sql/queries/pool_accounts.sql` update:
  - Remove hot eligibility dependency on raw `provider_accounts.credentials`.
  - Add lightweight join or state projection from `account_credentials` if selector needs credential state.
- `backend/sql/queries/auth_credentials.sql` update:
  - Move CAS credential writes to `account_credentials`.
  - Keep `provider_accounts.token_version` only as compatibility or derive from credential version.
- `backend/sqlc.yaml`:
  - Add overrides for `account_credentials` identifiers if custom types exist.
  - Regenerate `backend/internal/db/*.sql.go` after implementation.

### Credential storage and crypto

- `backend/internal/credentialstore/` new package recommended:
  - `Store`, `Envelope`, `Cipher`, `KeyProvider`, `ModeSchema`, `RuntimeMaterial`.
  - Owns encryption/decryption and refuses plaintext logging.
  - Keeps implementer code from scattering AES/KMS logic across provider adapters.
- `backend/internal/credentialstore/crypto.go` new:
  - AES-256-GCM using Go stdlib for Personal Edition v1.
  - `KeyProvider` interface: local env/file key now, external KMS backend later.
  - AAD: `tenant_id`, `provider_account_id`, `vendor`, `auth_mode`, `credential_version`, `key_id`.
- `backend/internal/credentialstore/modes.go` new:
  - Closed enum for the 15 Owner survey modes.
  - Validation table: vendor -> allowed auth modes -> required encrypted JSON fields -> runtime credential kind.
- `backend/internal/credentialstore/redaction.go` new or extend `backend/internal/auth/sanitizer.go`:
  - Redact credential fragments for audit/log/error, including API keys, bearer, JWT, refresh token labels, cloud private keys, cookies, and session-like tokens.

### Auth and refresh pipeline

- `backend/internal/auth/auth.go`:
  - Keep public `TokenProvider` boundary, but make implementation provider-neutral.
  - Add outcomes for endpoint/global throttle, refresh timeout, invalid grant, expired static key, cloud credential exchange failure, and key decrypt failure.
- `backend/internal/auth/storm_controller.go`:
  - Replace deferred panics with real account/endpoint/global acquisition.
  - Endpoint key should use `vendor + auth_mode + endpoint_fingerprint`, not raw endpoint URL.
- `backend/internal/credentialworker/types.go`:
  - Expand scheduler row type from account-only to credential-aware: credential ID, vendor, auth mode, endpoint fingerprint, refresh policy, expiry.
- `backend/internal/credentialworker/refresh_adapter.go`:
  - Replace provider-name-only adapter lookup with `(vendor, auth_mode)` handler lookup.
  - New contract should receive decrypted refresh material and return a `RefreshResult` with new payload, access expiry, refresh expiry, outcome, and rotation metadata.
- `backend/internal/credentialworker/refresher.go`:
  - Load encrypted active credential, decrypt only inside the refresher, run mode handler, encrypt new payload, CAS by `credential_version`, write audit/outbox.
  - Do not pass decrypted refresh tokens to scheduler or logs.
- `backend/internal/credentialworker/scheduler.go`:
  - Scan `account_credentials.refresh_before_at`.
  - Use A07 account/endpoint/global storm controller before handler call.
  - Classify failure into temp unsched, refresh retry, permanent revoked, or operator attention.
- `backend/internal/credentialworker/adapters/*.go`:
  - Keep current simple OAuth helpers as starting point, but split by mode semantics rather than broad vendor names.
  - Add static/no-refresh handlers for API keys.
  - Add cloud handlers for AWS/Azure/GCP only after OCAW dependency decision.

### Provider runtime vault and adapters

- `backend/internal/provider/vault.go`:
  - Extend `CredentialVault.Resolve` or introduce `CredentialResolver.ResolveForAttempt(ctx, tenantID, accountID, vendor, authMode)`.
  - Request path should get only runtime material needed for this attempt, not refresh token/private key unless the mode requires per-request signing.
- `backend/internal/provider/postgres_vault.go`:
  - Switch from `provider_accounts.credentials` to active `account_credentials` row.
  - Decrypt payload using `credentialstore`.
  - Preserve compatibility fallback only behind a temporary feature flag.
- `backend/internal/provider/adapter.go`:
  - Add or normalize credential types: `api_key`, `oauth_access_token`, `session_token`, `aws_sigv4`, `azure_key`, `gcp_service_account`, `upstream_passthrough`.
  - Keep adapter responsibility limited to request construction and auth header/signature injection.
- `backend/internal/provider/bedrock/*`:
  - Continue SigV4 signing, but source AWS material from encrypted `account_credentials`.
  - If STS is added, the STS result should be a short-lived encrypted credential version.
- `backend/internal/provider/openai/*`, `backend/internal/provider/gemini/*`, `backend/internal/provider/anthropic/*`, `backend/internal/provider/antigravity/*`:
  - Accept mode-specific runtime material without reading refresh secrets.
  - Session-like modes remain feature-flagged and auditable.

### Admin/API wiring

- `backend/internal/gatewayhttp/admin_pool_accounts_handler.go`:
  - Add `auth_mode` to create/update request.
  - Validate `(vendor, auth_mode)` combination before storing.
  - Store credential through `credentialstore`, never directly in `provider_accounts.credentials`.
  - Add rotate endpoint plan: `POST /v1/admin/provider-accounts/{id}/credentials/rotate`.
  - Add status endpoint plan: `GET /v1/admin/provider-accounts/{id}/credentials`.
  - Plaintext reveal should be denied by default; any break-glass reveal needs separate Owner/security decision.
- `backend/cmd/gateway/main.go`:
  - Wire `CredentialKeyProvider`, `credentialstore.Store`, mode handler registry, A07 storm controller, and scheduler.
  - Replace broad provider registrations with `(vendor, auth_mode)` registrations.
- `backend/config.example.yaml`:
  - Add `credential_encryption` config: `mode`, `active_key_id`, `local_key_file` or env name, optional KMS provider.
  - Add per-vendor/mode refresh skew/timeouts and feature flags.

### Tests

- `backend/internal/credentialstore/*_test.go` new.
- `backend/internal/credentialworker/*_test.go` expand.
- `backend/internal/provider/postgres_vault_*_test.go` update.
- `backend/internal/gatewayhttp/admin_pool_accounts_handler_test.go` update.
- `backend/cmd/gateway/smoke_test.go` update only with fake encrypted fixtures, never real secrets.

## Data Model

Recommended table: `account_credentials`.

Core columns:

```sql
id bigserial primary key
tenant_id bigint not null references tenants(id)
provider_account_id bigint not null references provider_accounts(id)
vendor text not null
auth_mode text not null
state text not null
credential_version integer not null default 1
encrypted_payload bytea not null
encryption_scheme text not null
key_id text not null
nonce bytea not null
aad_hash text not null
payload_fingerprint text
refresh_token_fingerprint text
access_expires_at timestamptz
refresh_expires_at timestamptz
refresh_before_at timestamptz
grace_until timestamptz
last_refresh_at timestamptz
last_refresh_outcome text
failure_class text
failure_count integer not null default 0
next_attempt_at timestamptz
created_at timestamptz not null default now()
updated_at timestamptz not null default now()
deleted_at timestamptz
created_by_actor text
last_modified_by_actor text
```

Lifecycle enum proposal:

- `active`: usable.
- `refreshing`: background refresh in progress.
- `refreshing_with_grace`: old access token may be used for low-risk request class until `grace_until`.
- `expired`: cannot use until refresh/rotation succeeds.
- `temp_unschedulable`: selector should fallback to another account.
- `needs_rotation`: static or cloud credential needs manual/operator rotation.
- `revoked`: invalid grant, disabled upstream account, or confirmed permanent credential failure.
- `operator_attention`: malformed token/decrypt failure/drift requiring manual action.

Encryption at rest:

- Use AES-256-GCM envelope encryption in v1 using Go stdlib.
- Use `CredentialKeyProvider` so local env/file key can be replaced by AWS KMS, GCP KMS, Azure Key Vault, or Vault without changing DB schema.
- AAD must bind ciphertext to `tenant_id`, `provider_account_id`, `vendor`, `auth_mode`, and `credential_version`.
- Fingerprints must be HMAC-based with a fingerprint key, not raw SHA of secret material.
- Store no plaintext secret in `provider_accounts`, `oauth_refresh_audit_events`, admin audit payload, logs, metrics, or test snapshots.

Migration posture:

- Add table and write path first.
- Dual-read behind feature flag only during transition: prefer `account_credentials`, fallback to legacy `provider_accounts.credentials` if no active row.
- Backfill must be an explicit, offline admin command because it decrypts/re-encrypts existing plaintext JSONB.
- Do not drop `provider_accounts.credentials` in this slice. Dropping or rewriting existing secrets is high risk and needs Owner approval.

## Per-Vendor Per-Mode Flow

| Vendor | Auth mode | Storage | Rotation policy | Refresh handler | Expired/failure fallback |
| --- | --- | --- | --- | --- | --- |
| `anthropic` | `api_key` | Encrypt `{api_key, base_url?, headers?}`. Runtime returns API key only. | Manual rotation, optional `rotate_after_at`. | No-op static handler. | On 401/403 mark `needs_rotation` or `temp_unschedulable`; pool retries another eligible account before first token. |
| `anthropic` | `claude_ai_oauth` | Encrypt access token, refresh token, expiry, endpoint, account metadata. | Pre-expiry refresh with skew; rotate refresh token if provider returns replacement. | `AnthropicOAuthRefresh` via shared OAuth chain. | `invalid_grant` -> `revoked`; timeout/5xx -> `temp_unschedulable`; fallback account if request has not crossed retry boundary. |
| `anthropic` | `claude_code` | Encrypt OAuth/session material plus client identity metadata; policy flag references mimicry/legal row. | Same as OAuth where refresh token exists; otherwise manual/session renewal. | `AnthropicClaudeCodeRefresh` or feature-flagged session handler. | If policy/legal gate missing, refuse mode at config. If expired, temp unsched and fallback. |
| `anthropic` | `bedrock` | Encrypt AWS access key, secret, region, optional STS session token/expiry/role source. | Static AWS keys manual; STS expires and pre-rotates if STS source configured. | `AWSStaticNoop` or `AWSSTSRefresh` after OCAW dependency decision. | SigV4 failure or expired STS -> temp unsched; fallback to another account/provider only if route policy permits. |
| `anthropic` | `vertex_anthropic` | Encrypt GCP service account JSON or workload identity reference, scopes, project/location. | Access token refresh before 1h expiry; SA key manual rotation. | `GCPServiceAccountTokenRefresh`. | Token exchange failure -> temp unsched; private key parse failure -> operator attention; fallback if available. |
| `openai` | `api_key` | Encrypt `{api_key, org_id?, project_id?, base_url?}`. | Manual key rotation. | No-op static handler. | 401/403 -> `needs_rotation` or `revoked`; fallback to another OpenAI/Azure account if route allows. |
| `openai` | `chatgpt_oauth` | Encrypt access/refresh token, account id, optional device/client metadata. | Pre-expiry refresh; legal/ToS gate required before production. | `OpenAIChatGPTOAuthRefresh` using shared OAuth chain. | Refresh failure -> temp unsched; invalid grant -> revoked; fallback before-first-token only. |
| `openai` | `codex_cli_oauth` | Encrypt Codex CLI access/refresh token and client metadata required by adapter. | Pre-expiry refresh; client identity policy audited. | `OpenAICodexOAuthRefresh`, may reuse generic OpenAI OAuth implementation with mode-specific endpoint config. | Expired -> force refresh; failure -> fallback; mode disabled if OCAW legal gate is not satisfied. |
| `openai` | `azure` | Encrypt Azure API key or Entra token config, endpoint, deployment, tenant/client metadata. | API key manual; Entra/managed identity token pre-refresh. | `AzureKeyNoop` or `AzureTokenRefresh`. | 401/403 -> temp unsched or needs rotation; fallback to another deployment/account if route policy permits. |
| `openai` | `refresh_token` | Encrypt generic refresh token payload, token endpoint, client id/secret if applicable. | Pre-expiry access refresh; refresh-token rotation if returned. | `GenericOAuthRefresh` constrained to configured endpoint allowlist. | Invalid grant -> revoked; endpoint failure -> temp unsched; no fallback after unsafe retry boundary. |
| `gemini` | `aistudio_api_key` | Encrypt API key and optional project metadata. | Manual rotation. | No-op static handler. | 401/403 -> needs rotation; fallback to another Gemini account if available. |
| `gemini` | `vertex_sa` | Encrypt GCP SA/workload identity reference, scopes, project/location. | Access token pre-refresh; SA key rotation manual or KMS-backed later. | `GCPServiceAccountTokenRefresh`. | Token exchange failure -> temp unsched; key parse/decrypt -> operator attention; fallback if route allows. |
| `gemini` | `code_assist` | Encrypt OAuth/session material plus client identity metadata. | OAuth refresh if refresh token exists; otherwise manual renewal. | `GoogleCodeAssistRefresh`, feature-flagged until legal/OCAW approval. | Expired -> temp unsched; invalid grant -> revoked; fallback. |
| `gemini` | `google_one` | Encrypt subscription OAuth/session material and account metadata. | OAuth refresh if available; otherwise manual/session renewal. | `GoogleOneRefresh`, feature-flagged. | Expired or 401 -> temp unsched/revoked by class; fallback. |
| `gemini` | `antigravity` | Encrypt Antigravity OAuth/session material; current Go code has Antigravity token-provider building blocks. | Pre-expiry refresh with current F-AUTH-005 pattern, then migrate to mode registry. | Existing `AntigravityTokenProvider` logic becomes a mode handler. | Malformed token -> operator attention; refresh failure -> temp unsched; fallback. |

## Refresh Handler Architecture

Recommended interface shape:

```go
type ModeHandler interface {
    Vendor() string
    AuthMode() string
    ValidateStaticPayload(ctx context.Context, payload map[string]any) error
    RuntimeMaterial(ctx context.Context, payload map[string]any) (provider.Credential, error)
    Refresh(ctx context.Context, input RefreshInput) (RefreshResult, error)
    ClassifyFailure(error) FailureClass
}
```

Request path:

1. Inbound API key resolves tenant/user.
2. Registry/router/pool selects Provider Account.
3. Credential resolver loads active `account_credentials` row by tenant/account/vendor/mode.
4. Decrypts only the active runtime payload.
5. If `state` is `active` or allowed `refreshing_with_grace`, returns runtime material.
6. Provider adapter injects auth/signature.
7. On upstream 401/403/expired-token class, gateway marks credential state and lets retry/fallback policy choose another account if safe.

Background path:

1. Scheduler scans `account_credentials.refresh_before_at <= now`.
2. A07 storm controller acquires account, endpoint, and global permits.
3. Singleflight collapses same credential refresh.
4. Mode handler runs token exchange or cloud auth pipeline.
5. New payload is encrypted, CAS-written by `credential_version`.
6. Audit and outbox are written without plaintext.
7. Pool/account state updates to `active`, `temp_unschedulable`, `revoked`, or `operator_attention`.

## Test Plan

Unit tests:

- `credentialstore`: AES-GCM round trip, AAD mismatch fails, wrong key fails, key rotation creates new `key_id`, no plaintext in serialized DB row.
- `credentialstore`: 15 vendor/mode validation table accepts valid fixture and rejects wrong vendor/mode pairing.
- `auth/sanitizer`: API key, JWT, refresh token labels, private-key-like payload, cookie/session token fragments are redacted.
- `credentialworker`: static handlers never call HTTP; OAuth handlers call only mock endpoints; cloud handlers use mock token endpoint/metadata server.
- `storm_controller`: account, endpoint, global buckets; token release on error; singleflight join does not hold capacity.

SQL and store tests:

- Insert active credential, resolve by tenant/account/vendor/mode.
- CAS update succeeds once and loser sees winner version.
- Tenant isolation: same provider account id in different tenants cannot decrypt/resolve cross-tenant because AAD differs.
- Active unique constraint prevents two active credentials for the same mode.
- Soft delete preserves audit history and allows new active credential.

Gateway integration tests:

- Admin create provider account with `auth_mode` stores encrypted payload and audit has `credentials_present=true` only.
- Admin response never echoes plaintext.
- Gateway request resolves credential from `account_credentials`, not `provider_accounts.credentials`.
- Expired OAuth account falls back to another eligible account before first token.
- Mid-stream 401 marks credential but does not retry after first token.

Acceptance mapping:

- Preserve AT-AUTH-005-001..017 from Released spec.
- Add/cover AT-AUTH-005-018..020 for endpoint/global storm controller and cooperative yield.
- Add new mode matrix tests for all 15 Owner survey modes, using fake credentials and `httptest.Server` only.

Checks:

- `go test ./internal/credentialstore/...`
- `go test ./internal/credentialworker/...`
- `go test ./internal/auth/...`
- `go test ./internal/provider/...`
- `go test ./internal/gatewayhttp/...`
- Focused integration test with local Postgres if available.
- Secret scan over test output and generated fixtures for token-like strings.

## Time Estimate

Plan to split implementation into reviewable slices:

| Slice | Work | Estimate |
| --- | --- | --- |
| S0 | Spec reconciliation and Owner OCAW decisions | 0.5 to 1 engineering day |
| S1 | `account_credentials` schema, sqlc, encryption store | 2 to 3 engineering days |
| S2 | Runtime vault migration and admin create/status/rotate API | 2 engineering days |
| S3 | Scheduler/storm controller credential-aware refresh | 2 to 3 engineering days |
| S4 | 15-mode handler matrix, mostly static/OAuth/cloud mocks | 3 to 4 engineering days |
| S5 | Integration/security tests, docs, review fixes | 2 engineering days |

Total: 11.5 to 15 engineering days for production-quality backend implementation, assuming no real cloud SDK dependency and no live credential testing. Add 3 to 5 days if Owner chooses official cloud SDKs and real cloud auth smoke tests.

## Blast Radius

High risk if implemented:

- Database schema and sqlc-generated code.
- Authentication/credential core.
- Request-path provider credential resolution.
- Background scheduler and refresh storm controller.
- Admin API for high-value secrets.
- Provider adapters for Anthropic/OpenAI/Gemini/Azure/Bedrock/Vertex paths.
- Logs, audit, metrics, and tests that could accidentally expose secrets.

Mitigation:

- Additive schema only at first.
- Feature flag `credential_store_v2`.
- Dual-read but single-write migration window.
- No plaintext reveal by default.
- Use fake secrets in tests.
- Per-commit Codex review before any implementation commit.
- Full reviewer-lane cross-review before declaring F-AUTH-005 expanded slice done.

## Decision Points: 5 Owner OCAW

1. `OCAW-1 account_credentials cutover`: approve adding a new `account_credentials` table and moving new writes away from `provider_accounts.credentials`. Without this, encryption-at-rest remains bolted onto the old JSONB field and the model is weaker.
2. `OCAW-2 encryption backend`: choose v1 key source. Recommended: AES-256-GCM with local env/file key and `KeyProvider` interface now; KMS backends later. Alternative: require KMS from day one, which delays Personal Edition.
3. `OCAW-3 subscription OAuth legality`: decide whether `claude_code`, `chatgpt_oauth`, `codex_cli_oauth`, `code_assist`, `google_one`, and `antigravity` are enabled, feature-flagged, or manual-first pending legal/ToS review.
4. `OCAW-4 cloud auth dependencies`: decide whether AWS/Azure/GCP auth pipelines may add official SDK dependencies. Recommended default: no new runtime dependency for this slice; use existing stdlib SigV4 and mock token-exchange interfaces until SDK choice is separately approved.
5. `OCAW-5 fallback/grace policy`: decide stale-while-refresh grace by request class and vendor. Recommended: static keys no grace after confirmed 401; OAuth/cloud modes may use `refreshing_with_grace` before first token only; no retry after stream/content side effects.

## Clean-Room

- Lane is SPECIFIER.
- This session did not read non-MIT reference project source.
- This plan uses HUAKAI internal docs/code plus Owner survey as source. `sub2api` is mentioned only as Owner-approved survey/reputation context and existing F-AUTH-005 provenance; no new source-derived mechanism claim is made.
- All other non-MIT reference source is forbidden and was not read: new-api, portkey, helicone, litellm, all-api-hub, envoy-ai-gateway.
- No function names, schemas, comments, UI structures, or implementation details were copied from reference projects.
- Implementation must be done in a clean implementer session that reads only HUAKAI internal specs/docs/code and approved official vendor protocol docs.

## Sources Read

- `CLAUDE.md`: independent plan rule, clean-room lane guard, source-must-read trigger matrix, high-risk confirmation boundaries.
- `docs/RULES.md`: Owner Start Gate, clean-room, feature preservation, tech stack, review discipline.
- `docs/03_FEATURE_PARITY_MATRIX.md`: F-AUTH-005 row, A06/A07/A08/A23/A24 links, F-AUTH-006 and F-CRED-001 separation.
- `docs/specs/upstream-credential-management.md`: Released F-AUTH-005 lifecycle, CAS, audit, failure paths, AT-AUTH-005-001..020.
- `docs/specs/_invariants/cross-module-boundaries.md`: Router must not read credentials; logs must not include credential material.
- `docs/18_GLOSSARY.md`: Provider Account vs API Key terminology.
- `docs/10_RISK_REGISTER.md`: credential material transport risk.
- `docs/15_RELEASE_GATES.md`: high-risk schema/auth/secrets/dependency gates.
- `backend/sql/migrations/0001_pool_routing.up.sql`: current `provider_accounts` schema and credential fields.
- `backend/sql/migrations/0004_rate_limiting.up.sql`: temp unschedulable fields.
- `backend/sql/migrations/0006_upstream_credential_management.up.sql`: token version, refresh audit, storm budget, mimicry policy.
- `backend/sql/migrations/0011_protocol_family_session_extension.up.sql`: `account_type=session` extension.
- `backend/sql/queries/auth_credentials.sql`, `backend/sql/queries/auth_storm.sql`, `backend/sql/queries/pool_accounts.sql`: current sqlc query surfaces.
- `backend/internal/auth/auth.go`, `sanitizer.go`, `storm_controller.go`, `antigravity_token_provider.go`: current F-AUTH-005 auth abstractions and gaps.
- `backend/internal/credentialworker/*`: scheduler, registry refresher, adapter contract, mock-only provider list.
- `backend/internal/credentialworker/adapters/*.go`: existing broad vendor refresh helpers.
- `backend/internal/provider/vault.go`, `postgres_vault.go`, `adapter.go`, `bedrock/sigv4.go`, `openai/codex_session.go`: runtime credential mapping and request-adapter boundaries.
- `backend/internal/gatewayhttp/admin_pool_accounts_handler.go`, `backend/cmd/gateway/main.go`, `backend/config.example.yaml`: admin create path, scheduler wiring, config surface.

中文总结：本计划只基于 HUAKAI 内部规范和 Go backend 源码观察起草，没有读取 Claude 独立计划，也没有读取非 MIT reference source；真实观察是 F-AUTH-005 已 Released、当前凭据仍主要落在 `provider_accounts.credentials`、refresh worker/Antigravity/token audit/storm account-scope 已有雏形但缺独立 `account_credentials`、加密 at rest、15-mode 生命周期、endpoint/global storm 和 admin mode validation；合理推断是需要用新增 `account_credentials` + AES-GCM envelope + `(vendor, auth_mode)` handler registry 作为安全实现路径；Open questions 为 5 个 Owner OCAW，主要集中在表切换、KMS/本地 key、订阅 OAuth 法务门、云 SDK 依赖和 stale/fallback 策略。
