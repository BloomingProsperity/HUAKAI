# Feature-Tree Audit: provider-account-mgmt

**Domain summary:** HUAKAI has a production-grade provider-account management subsystem (~82% feature coverage) with strong credential lifecycle, multi-vendor OAuth, health FSM, score-based pool selection, and quota enforcement; main gaps are automated API-key rotation scheduling, external secrets-vault integration, per-model quota granularity, and force-refresh admin endpoint.

**Audit date:** 2026-06-02  
**Branch audited:** `fix/hermes-phase-1-e33d940`  
**Scope:** `backend/` Go source + `backend/sql/migrations/`  
**Method:** grep + targeted file reads; every PRESENT/PARTIAL/MISSING verdict backed by file:line evidence or exhaustive grep record.

---

## Coverage Table

| # | Feature | Status | Evidence (file:line or grep tried) | Gap note |
|---|---------|--------|-------------------------------------|----------|
| **A. Credential Storage & Lifecycle** | | | | |
| A-01 | API-key / OAuth token storage in DB | PRESENT | `backend/sql/migrations/0001_pool_routing.up.sql:108-156` — `provider_accounts.credentials JSONB`; `backend/sql/migrations/0016_account_credentials.up.sql:9-59` — `account_credentials` encrypted table | V2 table (0016) is canonical; V1 JSONB is legacy fallback |
| A-02 | Token encryption at rest (AES-256-GCM) | PRESENT | `0016_account_credentials.up.sql:27-29` — `encrypted_payload bytea`, `encryption_scheme='aes-256-gcm'`, `key_id`, `nonce bytea`, `aad_hash text`; `backend/internal/credentialstore/postgres_store.go` — full enc/dec lifecycle | |
| A-03 | Token-type differentiation (api_key / OAuth / service-account) | PRESENT | `0001_pool_routing.up.sql:115` — `account_type IN ('oauth','api_key','service_account','upstream_static')`; `backend/internal/credentialstore/types.go:14-43` — 10+ auth modes | |
| A-04 | Credential CRUD (create / update / delete) | PRESENT | `backend/internal/gatewayhttp/admin_credentials_handler.go:66-72` — POST create, POST rotate, PATCH state, DELETE routes; `postgres_store.go` — Create/Rotate/SetState/Delete | |
| A-05 | Credential expiry / TTL fields | PRESENT | `0016_account_credentials.up.sql:35-37` — `access_expires_at`, `refresh_expires_at`, `refresh_before_at` timestamptz | |
| A-06 | Soft-delete / archival | PRESENT | `0016_account_credentials.up.sql:46` — `deleted_at timestamptz`; `types.go:47-54` — `StateRevoked`, `StateExpired` states | |
| A-07 | Emergency revoke by admin | PRESENT | `admin_credentials_handler.go:28` — `SetState` method; `types.go:50` — `StateRevoked` | |
| **B. OAuth / Token Refresh** | | | | |
| B-01 | OAuth 2.0 authorization-code flow with PKCE | PRESENT | `backend/internal/credentialacq/oauth_authorization_code.go` — full PKCE flow; `0024_encrypt_pkce_verifier_at_rest.up.sql` — encrypted verifier at rest | |
| B-02 | Refresh-token storage & rotation | PRESENT | `0006_upstream_credential_management.up.sql:21,34,63-64` — `refresh_token_fingerprint`, `last_refresh_at`, `oauth_refresh_audit_events`; `backend/internal/anthropicoauth/refresher.go` | |
| B-03 | Auto-refresh background scheduler | PRESENT | `backend/internal/credentialworker/scheduler.go:27,101-138` — `Scheduler` polls `provider_accounts.expires_at` on ticker; handles OAuth token renewal | Scheduler covers OAuth token refresh, NOT API-key rotation (see F-02) |
| B-04 | Token-refresh failure tracking + outcomes | PRESENT | `0006_upstream_credential_management.up.sql:23-29` — `last_refresh_outcome` enum with 11 outcomes (storm_budget_exhausted, invalid_grant_race_recovered, cas_lost, etc.) | |
| B-05 | Anthropic claude.ai OAuth | PRESENT | `backend/internal/credentialacq/anthropic_oauth.go`; `types.go:31` — `AuthModeClaudeAIOAuth` | |
| B-06 | Google OAuth (Gemini / Google One / Code Assist) | PRESENT | `backend/internal/credentialacq/gemini_oauth.go`; `types.go:40-42` — `AuthModeGoogleOne`, `AuthModeVertexSA`, `AuthModeCodeAssist` | |
| B-07 | OpenAI / ChatGPT OAuth | PRESENT | `backend/internal/credentialacq/chatgpt_oauth.go`; `types.go:35-36` — `AuthModeChatGPTOAuth`, `AuthModeCodexCLIOAuth` | |
| B-08 | Device-code flow (Cursor / Windsurf / CLI tools) | PRESENT | `backend/internal/credentialacq/oauth_devicecode.go`; `windsurf_token.go` | |
| B-09 | CLI credential import | PRESENT | `backend/internal/credentialacq/cli_import.go` | |
| **C. Account Health Monitoring** | | | | |
| C-01 | Per-account health-state field | PRESENT | `0056_provider_account_health_state_check.up.sql:24-26` — `health_state IN ('healthy','throttled','revoked','cooldown')`; `0001_pool_routing.up.sql:120-122` — earlier enum (superseded by 0056) | |
| C-02 | Health-check probe endpoint (on-demand) | PRESENT | `backend/internal/observability/account_health_probe_handler.go:25-66`; `backend/internal/adminhttp/provider_account_test_handler.go:57-59` — POST `/{id}/test` | |
| C-03 | Error-rate / health-score tracking | PRESENT | `backend/internal/gateway/health_fsm.go:86-92` — `HealthSnapshot` with `RecentErrorTimes`, `AmbiguousErrorCount`, `HealthScore` | |
| C-04 | Consecutive-failure counter + threshold demotion | PRESENT | `health_fsm.go:31-40` — `DefaultDegradedErrorThreshold=3`, `DefaultUpgradeStreak=10`, `DefaultManualRecoveryThreshold=5` | |
| C-05 | 429 → rate-limit demotion | PRESENT | `0001_pool_routing.up.sql:94` — `failover_status_codes ARRAY[401,403,429,529]`; `health_fsm.go` — 6-state FSM classifies 429 as throttle event | |
| C-06 | Auto-recovery after cooldown | PRESENT | `0001_pool_routing.up.sql:122` — `health_state_until timestamptz`; `health_fsm.go:105-108` — `CooldownUntil`, `DefaultCooldownDuration=60s`; scheduler re-probes on expiry | |
| C-07 | Scheduled health-state expiry scan | PRESENT | `credentialworker/scheduler.go:155-172` — `RunOnce` method re-evaluates accounts whose `health_state_until <= NOW()` | Only covers accounts with `expires_at`; no dedicated health-expiry sweep for throttled/cooldown accounts without OAuth tokens |
| **D. Quota & Usage Tracking** | | | | |
| D-01 | Per-account token/request/cost counters | PRESENT | `0001_pool_routing.up.sql:140-149` — `quota_used_total`, `quota_used_daily`, `quota_used_weekly` numeric(20,8) | |
| D-02 | Daily / weekly / monthly quota caps | PRESENT | `0001_pool_routing.up.sql:140-147` — `cap_quota_total`, `cap_quota_daily`, `cap_quota_weekly`; `0072_quota_calendar_month.up.sql` — calendar-month window added | |
| D-03 | Quota-exhaustion detection + demotion | PRESENT | `0001_pool_routing.up.sql:148-149` — `quota_status IN ('active','exhausted','paused')`; `0070_quota_subsystem.up.sql` — enforce/observe/manual_first modes | |
| D-04 | Quota-reset schedule (daily / monthly) | PRESENT | `0072_quota_calendar_month.up.sql`; `quota_window_*_start` fields track reset windows | |
| D-05 | Soft (observe) vs hard (enforce) quota | PRESENT | `0070_quota_subsystem.up.sql:33-35` — `mode IN ('enforce','observe','manual_first','disabled')` | |
| D-06 | Per-model quota differentiation | PARTIAL | `0004_rate_limiting.up.sql:55-56` — `model_rate_limits jsonb` (Antigravity-specific RPM); no per-(account,model) cost/token quota cap found | Rate limits per model exist; cost/token quota caps are account-level only — cannot set "max $10/day on claude-3-opus specifically" per account |
| D-07 | Historical usage audit log per account | PRESENT | `0070_quota_subsystem.up.sql:173-192` — `quota_audit_events` with event_type, payload, actor | |
| **E. Load Balancing & Selection** | | | | |
| E-01 | Multi-account pool per provider | PRESENT | `0001_pool_routing.up.sql:52-80` — `pool_groups` table; `backend/internal/pool/dispatcher/dispatcher.go` — 5 dispatch modes | |
| E-02 | Weighted selection (8 weight axes) | PRESENT | `0001_pool_routing.up.sql:239-246` — `weight_priority`, `weight_load_rate`, `weight_last_used`, `weight_recent_error_rate`, `weight_recent_latency`, `weight_quota_headroom`, `weight_fairness_debt`, `weight_snapshot_freshness`; `backend/internal/pool/router/default_selector.go` | |
| E-03 | Least-loaded / headroom-aware selection | PRESENT | `backend/internal/pool/scoring/blend.go` — `LoadRate`, `HeadroomWeight` score components | |
| E-04 | Cache-locality / affinity pinning | PRESENT | `pool/scoring/blend.go:10` — `LocalityBonus` for prompt-cache affinity | |
| E-05 | PASR score-based selection | PRESENT | `backend/internal/pool/router/pasr.go` — PASR implementation; `dispatcher.go:23-25` — `pasr-primary` and `pasr-strict` modes | |
| E-06 | Sticky session binding | PRESENT | `0001_pool_routing.up.sql:203-215` — `sticky_bindings` table (session_hash+model → account_id, 1h TTL); `backend/internal/pool/binding/sticky.go` — `DBStickyStore` | |
| E-07 | Shadow / canary traffic splitting | PRESENT | `dispatcher.go:23-25` — `shadow` and `canary` dispatch modes | |
| E-08 | Account exclusion list (in-flight skip) | PARTIAL | Health-state demotion implicitly excludes unhealthy accounts; no explicit per-request exclusion set or "temporarily skip this account ID" mechanism found; grep: `exclusion_list`, `skip_account`, `in_flight_blacklist` — all NONE | Must fully disable an account to exclude it; cannot surgically skip one account for a single request while keeping it available for others |
| **F. Account Rotation & Key Cycling** | | | | |
| F-01 | Manual API-key rotation by admin | PRESENT | `admin_credentials_handler.go:69` — POST `/{id}/credentials/{credentialID}/rotate`; `postgres_store.go` — `Rotate` method; `credential_audit_events` records `credential_rotated` | |
| F-02 | Scheduled automatic API-key rotation | MISSING | grep: `auto_rotat`, `AutoRotat`, `rotation_schedule`, `rotation_interval`, `rotation_due_at` — all NONE; `needs_rotation` state exists (`types.go:52`) but no background job handles it; scheduler (`credentialworker/scheduler.go`) only processes OAuth token refresh | **Critical gap:** `needs_rotation` state is a dead signal — nothing ever acts on it automatically; operator must notice and manually rotate |
| F-03 | Pre-rotation dual-active window (zero-downtime) | PARTIAL | `0016_account_credentials.up.sql:38` — `grace_until timestamptz`; `StateRefreshingWithGrace` in types covers OAuth refresh failure grace; no dual-active overlap for API-key rotation specifically | Grace window covers refresh retry, not new-vs-old key overlap during rotation |
| F-04 | Rotation audit log | PRESENT | `0016_account_credentials.up.sql:89-116` — `credential_audit_events` with `'credential_rotated'` event type | |
| **G. Rate-Limit Handling** | | | | |
| G-01 | Per-account rate-limit state machine | PRESENT | `0004_rate_limiting.up.sql` — `rate_limited_at`, `rate_limit_reset_at`, `overload_until`, `temp_unschedulable_until`; `health_fsm.go` — 6-state FSM | |
| G-02 | RPM / TPM reason tracking | PRESENT | `0004_rate_limiting.up.sql:29-32` — `rate_limit_reason IN ('rate_limit_rpm','rate_limit_tpm',...)` | |
| G-03 | Retry-After header parsing from upstream 429 | PRESENT | `backend/internal/gateway/error_normalize.go` — upstream error classification; retry-after backoff logic | |
| G-04 | Cooldown queue / outbox for rate-limited accounts | PRESENT | `0001_pool_routing.up.sql:313-336` — `scheduler_outbox` transactional outbox; `health_fsm.go` — CooldownUntil + auto-recovery | |
| G-05 | Global vs per-account throttle coordination | PRESENT | `0070_quota_subsystem.up.sql:70` — quota concurrency slots; in-flight count tracked on `provider_accounts` | |
| **H. Account Grouping & Priority** | | | | |
| H-01 | Account groups / logical pools | PRESENT | `0001_pool_routing.up.sql:52-80` — `pool_groups` table with routing rules | |
| H-02 | Priority tiers within pool | PRESENT | `0001_pool_routing.up.sql:134` — `priority integer NOT NULL DEFAULT 100` on `provider_accounts` | |
| H-03 | Provider → group → account config hierarchy | PRESENT | `0001_pool_routing.up.sql` — `providers` → `pool_groups` → `channels` → `provider_accounts` four-level hierarchy | |
| H-04 | Tag-based / capability-based filtering | PARTIAL | `provider_accounts` has `capability_flags text[]` and `model_allow_list text[]`; no generic `tags` table found; grep: `account_tags`, `tags jsonb` on provider_accounts — NONE | Capability and model-allow filtering present; arbitrary label/tag filtering absent |
| **I. Admin Operations API** | | | | |
| I-01 | List provider accounts (paginated) | PRESENT | `backend/internal/gatewayhttp/admin_pool_accounts_handler.go:392-450` — `newListProviderAccountsHandler` with filters | |
| I-02 | Create / update / delete account | PRESENT | `admin_pool_accounts_handler.go` — handlers for POST create, PUT update, DELETE delete, PATCH disable | |
| I-03 | Force-refresh token NOW (without rotation) | MISSING | grep: `force.refresh`, `force_refresh`, `forceRefresh`, `force-refresh`, `refresh.now` in `backend/` — NONE in handler files | Workaround: use admin credential rotate (destroys old + creates new), or manually flip `refresh_before_at` in DB; no clean UX path |
| I-04 | Toggle account enable / disable | PRESENT | `admin_pool_accounts_handler.go` — PATCH `/{id}/enabled` route | |
| I-05 | Per-account usage stats API | PARTIAL | `backend/internal/adminhttp/provider_account_health_handler.go` — GET `/{id}/health` returns quota state snapshot; no dedicated time-series usage history endpoint found | Health snapshot covers current quota; no API for "usage over last 7 days" per account |
| I-06 | Admin audit log for account mutations | PRESENT | `0010_admin_auth.up.sql:68-100` — `admin_audit_events` table; credential handler emits events on create, rotate, state-change, delete | |
| **J. Multi-Provider Normalization** | | | | |
| J-01 | Abstract credential provider interface | PRESENT | `backend/internal/provider/vault.go` — `CredentialVault` interface; `credentialstore/types.go` — `ModeHandler` interface | |
| J-02 | Per-vendor config (base URL, auth scheme, model map) | PRESENT | `0001_pool_routing.up.sql:32-44` — `providers` table with `code`, `upstream_protocol`; `backend/internal/registry/normalize.go` — provider normalization | |
| J-03 | Provider registry / catalog API | PRESENT | `backend/internal/adminhttp/provider_catalog_handler.go`; `backend/internal/registry/` package | |
| J-04 | AWS Bedrock SigV4 authentication | PRESENT | `backend/internal/provider/bedrock/sigv4.go`; `credentialstore/types.go:260` — `AuthModeBedrock` with `aws_access_key_id` + `aws_secret_access_key` | |
| J-05 | GCP Vertex / service-account auth | PRESENT | `credentialstore/types.go:38-39` — `AuthModeVertexSA`, `AuthModeVertexADC`; `credentialacq/gemini_oauth.go` | |
| **K. Secret Management Integration** | | | | |
| K-01 | AES-256-GCM envelope encryption in DB | PRESENT | `0016_account_credentials.up.sql:27-32` — encrypted_payload + key_id reference; decryption only at runtime in `postgres_store.go` | |
| K-02 | External vault integration (HashiCorp Vault / AWS Secrets Manager / GCP KMS) | MISSING | grep: `hashicorp`, `vault.client`, `secretsmanager`, `aws.*ssm`, `kms` in backend/ — NONE; comment at `backend/internal/sign/keygen.go:10` says "生产环境 priv key 应从 KMS / vault / env var" (production keys SHOULD come from KMS/vault) but no implementation | Comment acknowledges gap; production deployments store enc key in env var only; no rotation path for the encryption key itself |
| K-03 | Encryption-key rotation (key version migration) | MISSING | grep: `key_version`, `ReEncrypt`, `key_rotation`, `rotate.*enc` — NONE in backend/ | `key_id` field in DB exists as a slot for versioning but no re-encryption job or key-version migration found |
| K-04 | Secret reference pattern (no raw key in DB) | PRESENT | `0016_account_credentials.up.sql:27` — `encrypted_payload` only; raw API keys are never stored in plaintext | |
| **L. Alerting & Observability** | | | | |
| L-01 | Per-account Prometheus metrics (labelled) | PRESENT | `backend/internal/pool/dispatcher/metrics.go` — metrics with account-level labels | |
| L-02 | Quota-exhaustion event emission | PRESENT | `0070_quota_subsystem.up.sql:336` — `scheduler_outbox` emits `quota_changed` events; `quota_audit_events` table | |
| L-03 | Auth-failure tracking (refresh audit trail) | PRESENT | `0006_upstream_credential_management.up.sql:43-83` — `oauth_refresh_audit_events` with 11 outcome codes | |
| L-04 | Unified account dashboard query surface | PARTIAL | Health handler + catalog handler + quota audit + metrics endpoints exist separately; no single aggregated `/admin/v1/accounts/{id}/dashboard` endpoint; grep: `dashboard` in adminhttp/ — NONE | Operator must stitch health, quota, and metrics from 3+ endpoints |
| L-05 | Alert webhooks on quota exhaustion / auth failure | MISSING | grep: `webhook`, `alert.*send`, `notification.*send` in backend/ — NONE (only scheduler_outbox for internal events) | No push notification to external alert system (PagerDuty, Slack, etc.) when account quota is exhausted or auth permanently fails |

---

## Top Missing Features — Ranked by Commercial Value

| Rank | Feature | Why it matters commercially |
|------|---------|------------------------------|
| 1 | **F-02 — Scheduled automatic API-key rotation** | Provider API keys have 90-day / 1-year TTL policies (especially OpenAI, Anthropic enterprise keys). Without automation, expired keys = silent gateway downtime. `needs_rotation` state exists as a dead signal — nothing acts on it. Every competitor (sub2api, new-api) has an operator-rotation workflow; HUAKAI defers entirely to manual ops. |
| 2 | **K-02 — External secrets-vault integration (HashiCorp Vault / AWS Secrets Manager)** | Enterprise and regulated customers require credentials to live in audited vaults, not in DB columns. AES-256-GCM wrapping is good hygiene but the enc key itself lives in an env var — compromise of the host = compromise of all credentials. Blocking for SOC-2 / ISO-27001 certification path. |
| 3 | **K-03 — Encryption-key rotation / re-encryption job** | Even with vault integration deferred, the current design has `key_id` in DB but no path to rotate the master encryption key. A one-time key compromise requires re-encrypting all credential rows. Without this job, rotating the key means downtime. |
| 4 | **I-03 — Force-refresh-token admin endpoint** | When an OAuth token is revoked or invalidated server-side (password change, session revoke), the scheduler waits for `refresh_before_at` to pass. Operators need an immediate "kick this account to re-auth now" button. Workaround (rotate = destroy + recreate) loses historical state. This is the highest-frequency admin rescue operation. |
| 5 | **D-06 — Per-(account, model) quota differentiation** | High-volume operators need per-model cost caps: "Account A may use claude-opus-4 up to $5/day but unlimited claude-haiku-4." Current caps are account-level only. Without this, one expensive-model request can exhaust the daily quota that was meant to cover 1000 cheap requests. |
| 6 | **L-05 — Alert webhooks on quota exhaustion / auth failure** | Silent quota exhaustion means customer requests fail without operator awareness. Push to PagerDuty / Slack / email is table-stakes for a managed gateway. sub2api and new-api both have email-on-quota-exhaustion paths. Currently HUAKAI emits `scheduler_outbox` events but no external delivery path. |
| 7 | **E-08 — Per-request account exclusion list** | When an account starts behaving oddly mid-batch (intermittent errors without crossing the FSM demotion threshold), operators need to surgically exclude it from new requests while letting in-flight ones drain. Current granularity is binary (enabled/disabled), not "pause new selection." |
| 8 | **I-05 — Time-series usage history API per account** | The health snapshot gives current quota state, but operators need "show me last 7 days of token usage for account X" for capacity planning, billing reconciliation, and anomaly detection. Missing endpoint forces direct DB queries. |
| 9 | **H-04 — Generic tag-based account filtering** | `capability_flags` and `model_allow_list` cover structured filtering but operators need arbitrary labels (e.g., `region:us-west`, `tier:premium`, `cost-center:team-a`) for flexible pool construction without schema changes. |
| 10 | **L-04 — Unified account dashboard query surface** | Stitching health + quota + metrics + audit across 3-4 endpoints creates N+1 operator friction and makes building any admin UI panel slow. A single projection endpoint would remove this without changing underlying storage. |
