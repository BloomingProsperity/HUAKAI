# Coverage Verification — Gap Design vs Real Backend Code

**Date:** 2026-06-03  
**Analyst:** Coverage-verification sweep (read-only, no code modified)  
**Scope:** 11 gap designs in `docs/process/gap-designs/` vs backend at `C:\HUAKAI\repo\backend`  
**Current max migration:** `0076_user_role.up.sql`

---

## app-rate-limit

**Existing coverage:**

- `cmd/gateway/rate_limit.go` — IP-keyed token-bucket rate limiter (two tiers: global + auth-strict). Uses `gateway.TokenBucket` from `internal/gateway`. The `ipBucketRegistry` (line 73) pattern — per-key bounded map with eviction — is the exact same pattern the design proposes reusing for `userratelimit.BucketRegistry`.
- `cmd/gateway/rate_limit.go:176` — `newRateLimiter()`, `retryAfterForRatePerSec`, `Retry-After` header logic fully implemented.
- `cmd/gateway/rate_limit_test.go` — comprehensive test coverage for the IP limiter.
- `internal/rate/` — `ModelCooldownService` for upstream-account cooldown (429/5xx back-pressure). Confirmed in `cmd/gateway/wiring.go:55,81,547`.
- `sql/migrations/0008_model_registry.up.sql:179-182` — `rpm_limit`/`tpm_limit` columns exist on `model_proxy_bindings` for **model-level** caps (not user-level). Referenced in `sql/queries/registry.sql:115-116`.
- **No** `user_rate_limit_policies` table. **No** `internal/userratelimit` package. **No** per-user or per-group application-level RPM/TPM enforcement.

**True residual:**

1. `internal/userratelimit` package (policy loader, bucket registry, gate, success window, middleware).
2. `sql/migrations/0077_user_rate_limit_policies.up.sql` — new table.
3. Wiring: `ChatHandlerDeps.AppRateLimiter` field + call-sites in `chat_completions_handler.go` (post-auth RPM check) and `prepareRoute` (TPM check).
4. Config env vars (`HUAKAI_APP_RL_DEFAULT_RPM`, etc.) in `cmd/gateway/config.go`.

**Coverage estimate:** 20%

**Recommendation:** SMALL-ENHANCEMENT — The `gateway.TokenBucket` primitive and `ipBucketRegistry` eviction pattern are directly reusable. The IP limiter in `rate_limit.go` is partial prior art for the pattern, but the per-user/per-group gate and DB-backed policy table are entirely absent. Net new build is ~85% of the design scope.

---

## ops-suite

**Existing coverage:**

- `internal/channelhealth/` (15 files) — reactive alert/state machine: `Service.AppendAlert` (`service.go:620`), `Store.AppendAlert` (`store_postgres.go:352`), `channel_health_admin_alerts` table (`store_postgres.go:369`). Provides reactive per-channel alert objects stored to DB.
- `internal/channelhealth/types.go:326` — `Store` interface with `AppendAlert(ctx, Alert)`.
- `internal/channelhealth/service.go` — `ManualResume` method exists (referenced in `schedtest` design as the auto-recover integration point).
- `internal/adminhttp/` — `ProviderAccountCredentialTester` mentioned in design as the existing test harness to wrap.
- **No** `ops_alert_rules` table. **No** `ops_alert_events` table. **No** `ops_alert_silences` table.
- **No** `synthetic_monitors` / `synthetic_monitor_history` tables.
- **No** `schedtest_plans` / `schedtest_results` tables.
- **No** `internal/opsalert`, `internal/syntheticmonitor`, `internal/schedtest`, `internal/opshttp` packages.

**True residual:**

All three sub-features are absent. Specifically:
1. Alert rules/events/silences with cooldown, email dispatch, cursor-paginated admin API.
2. Proactive synthetic channel monitor (independent credentials, SSRF-safe checker, 7-day availability rollup).
3. Cron-scheduled account test plans with auto-recover.
4. 3 new migrations (0077–0079), 3 new packages, 1 new HTTP package.

**Coverage estimate:** 10%

**Recommendation:** BUILD-NEW — The only reusable primitives are `channelhealth.Service.ManualResume` (for schedtest auto-recover) and `adminhttp.ProviderAccountCredentialTester` (for schedtest runner). All three feature areas require new packages and schema.

---

## usage-dashboard

**Existing coverage:**

- `internal/meusagehttp/handler.go` — `GET /v1/me/usage`: self-scoped, keyset-paginated, per-request usage log over `usage_records`. Confirmed at `cmd/gateway/routes.go:58`. Supports `tenant_id`, `api_key_id`, `from`/`to` filters, cursor pagination.
- `internal/gatewayhttp/admin_observability_handler.go` — `GET /admin/v1/usage`: admin-scoped `ListUsageRecords` with provider/pool/model/api_key_id filters + keyset pagination.
- `sql/queries/observability.sql:4` — `ListUsageRecords :many` query: flat row-level SELECT with all filter params. **No `GROUP BY`, no `date_trunc`, no aggregation.**
- `internal/billing/rate_table_source.go` — `RateTableSource`: `GetRateTable`, `ListRateTableSnapshots` — already exposed at `GET /v1/pricing/rate-table`, `/v1/pricing/snapshots` (`routes.go:92-94`).
- `sql/migrations/0070_quota_subsystem.up.sql` — `quota_policies` + `quota_windows` tables exist (needed for `QuotaWindowSnapshot` query in design).
- **No** `usage_analytics.sql` query file. **No** `internal/usageanalytics` or `internal/usageanalyticshttp` packages.
- **No** time-series aggregation (date_trunc/GROUP BY) queries anywhere in `sql/queries/`.
- **No** spend ranking or RPM/TPM summary queries.
- **No** `GET /v1/me/analytics/*` or `GET /admin/v1/analytics/*` endpoints.

**True residual:**

1. `sql/queries/usage_analytics.sql` — 4 new aggregation queries (`AggregateUsageByDay`, `RankSpendByAPIKey`, `SummariseRPMTPM`, `QuotaWindowSnapshot`).
2. `sql/migrations/0077_usage_analytics_views.up.sql` — 2 new `CONCURRENTLY` indexes on `usage_records`.
3. `internal/usageanalytics` package (aggregator, repo, service).
4. `internal/usageanalyticshttp` package (admin + user handlers).
5. 4 new endpoints (`/admin/v1/analytics/usage/*`, `/v1/me/analytics/*`).

**Coverage estimate:** 25%

**Recommendation:** SMALL-ENHANCEMENT — The data (`usage_records`, `quota_windows`) and the flat-list read infrastructure (`ListUsageRecords`, admin observability) already exist. The entire delta is aggregation queries + new HTTP handlers on top of the same tables. The `quota_windows` tables for the snapshot query are already present.

---

## per-key-controls

**Existing coverage:**

- `internal/userkey/userkey.go` — `Service.Issue`, `Service.List`, `Service.Revoke` (single-key). Per-key expiry implemented (`IssueRequest.ExpiresAt`, `ErrInvalidExpiry`).
- `internal/userkeyhttp/` — HTTP layer for create/list/revoke API keys.
- `internal/quota/` — Quota engine with `quota_policies` + `quota_windows` (migration 0070). `scope_kind` field supports `api_key` scope (per design).
- `sql/migrations/0073_subscription.up.sql:110` — `quota_policy_id bigint` column exists in `subscription_policy_links` as a reference pattern — but **not** on `api_keys`.
- **No** `api_key_groups` table. **No** `key_group_id` / `quota_policy_id` columns on `api_keys`.
- **No** batch-revoke (`BatchRevoke`) method in `userkey.Service`.
- **No** `api_key_reveal_tokens` table. **No** reveal-token / step-up challenge infrastructure.
- **No** `internal/userkeycontrols` or `internal/userkeycontrolshttp` packages.

**True residual:**

1. Migration 0077: `api_key_groups` table + `key_group_id`/`quota_policy_id` columns on `api_keys`.
2. Migration 0078: `api_key_reveal_tokens` table.
3. `internal/userkeycontrols` package (SetKeyQuota, SetKeyGroup, BatchRevoke, reveal-token).
4. `internal/userkeycontrolshttp` package (handlers for all 7 new endpoints).
5. sqlc regeneration for `internal/db/auth` after column add.

**Coverage estimate:** 20%

**Recommendation:** BUILD-NEW — The quota engine is the biggest reusable piece (~30% of the design), but the group table, quota linking, batch revoke, and reveal-token flow are all absent.

---

## totp-2fa

**Existing coverage:**

- `internal/userauth/` — password auth, email verification, OAuth social login, session management fully implemented.
- `internal/usersession/` — session management.
- `internal/panelauth/` / `internal/panelauthhttp/` — admin panel auth.
- Email OTP (the §1 second-factor mechanism) is explicitly **not** TOTP — confirmed by design noting Owner chose email OTP for §1.
- **No** `totp_credentials` table. **No** `totp_backup_codes` table. **No** `stepup_challenges` table. **No** `webauthn_credentials` or `webauthn_ceremonies` tables.
- **No** `internal/totp`, `internal/stepup`, `internal/totphttp` packages.
- The design itself states: **dispatch blocked** — requires explicit Owner approval before implementation (Owner chose email OTP for §1, TOTP/WebAuthn is post-§1 commercial upgrade).

**True residual:**

Everything in the design is absent. However, per the design's own "Owner Decision Context": this feature is **gated behind Owner approval** and must not be dispatched until email OTP (P7 of §1 remediation) is Released.

**Coverage estimate:** 5%

**Recommendation:** BUILD-NEW — But **blocked on Owner approval**. The design explicitly calls this out. Do not dispatch without the Owner authorization step documented in the design.

---

## pricing-catalog

**Existing coverage:**

- `internal/billing/rate_table_source.go` — `RateTableSource` interface: `GetRateTable`, `GetRateTableSnapshot`, `ListRateTableSnapshots`. Backed by `billing_pricing_versions`.
- `cmd/gateway/routes.go:92-94` — 3 public pricing endpoints already live: `GET /v1/pricing/rate-table`, `GET /v1/pricing/snapshots`, `GET /v1/pricing/snapshots/{snapshot_id}`.
- `internal/gatewayhttp/cost_receipt_handler.go` — cost receipt handler (reads pricing for billing receipts).
- `sql/migrations/0002_observability_billing.up.sql:271` — `billing_pricing_versions` table exists with `pricing_data JSONB`.
- `sql/migrations/0030`, `0031`, `0068` — public-scope columns and data seeding on `billing_pricing_versions`.
- `internal/modelsync/http_fetcher.go` — HTTP fetcher with redirect-block and body-size cap patterns (directly reusable by `upstreamprice.HTTPFetcher`).
- **No** `pool_group_pricing_ratios` table. **No** `upstream_price_presets` table.
- **No** `internal/pricingcatalog`, `internal/pricingratiohttp`, `internal/upstreamprice` packages.
- **No** per-group ratio multiplier logic. **No** upstream preset fetch/diff/apply pipeline.
- **No** admin endpoints for ratio CRUD or upstream sync.

**True residual:**

1. Migration 0077: `pool_group_pricing_ratios` + `upstream_price_presets` tables.
2. `internal/pricingcatalog` (read service with effective price = base × ratio).
3. `internal/pricingratiohttp` (`GET /v1/pricing` — per-group effective prices).
4. `internal/upstreamprice` (fetcher + diff + apply, reusing `modelsync.HTTPFetcher` pattern).
5. Admin handlers for ratio CRUD and upstream sync (new files in `internal/adminhttp`).

**Coverage estimate:** 30%

**Recommendation:** SMALL-ENHANCEMENT — The pricing foundation (`billing_pricing_versions`, `RateTableSource`, public endpoints) is solid and reusable. The `modelsync.HTTPFetcher` security pattern is directly applicable. What's missing is the per-group ratio layer and the upstream sync pipeline (roughly 70% new work).

---

## notifications

**Existing coverage:**

- `internal/email/` — SMTP/delivery infrastructure. `settings_store.go:63` confirms `email_settings` table exists. `AuthSender.SendTenantMessage` is the entry point the design uses.
- `internal/paymenthttp/` — inbound payment callbacks (not notification outbound).
- `sql/queries/billing_settle.sql:76` — comment about "cross-threshold notification" in Tx2 but no notification table write — this is a comment/spec reference, not an implementation.
- `sql/migrations/0062_payment_audit_log.up.sql` — payment webhook audit log (inbound only).
- **No** `notification_channels` table. **No** `alert_email_bindings` table. **No** `notification_prefs` table. **No** `notification_rate_ledger` table.
- **No** `internal/notifchannel`, `internal/notifpref`, `internal/notifdelivery`, `internal/alertemail`, `internal/alertemailhttp`, `internal/notifhttp`, `internal/balancelowworker` packages.
- **No** webhook or WxPush delivery adapters. **No** balance-low worker.

**True residual:**

Everything in the design is absent. The only reusable component is `internal/email` (already imported by the design's `email_adapter.go`). All 7 new packages, 2 migrations, webhook/WxPush adapters, and balance-low worker must be built.

**Coverage estimate:** 5%

**Recommendation:** BUILD-NEW — Only the email transport layer is reusable. All channel management, preference storage, delivery fan-out, and background worker are entirely absent.

---

## tiered-billing

**Existing coverage:**

- `internal/billing/` — full Tx1/Tx2 reserve+settle pipeline. `ClaimGate.Reserve`, `Settler.Settle` exist.
- `internal/gatewayhttp/chat_completions_pricing.go` — `completionCost()` with flat-rate `completionRateVector.price()`. The design's injection point is a 6-line `if` branch in this existing function.
- `internal/billing/rate_table_source.go` — reads `billing_pricing_versions.pricing_data JSONB`. Design adds `tier_rules JSONB` column alongside `pricing_data`.
- `internal/quota/` — `quota.Service.Reserve`/`Settle` for the `subscription_cap` funding source path.
- `internal/balancehold/` — `Reserve`/`Capture` pipeline for the `wallet` funding source path.
- `sql/migrations/0070_quota_subsystem.up.sql` — `quota_policies` + `quota_windows` already exist.
- **No** `tier_rules` column on `billing_pricing_versions`. **No** `funding_source` column on `billing_ledger_claims`. **No** `default_funding_source` column on `api_keys`.
- **No** `internal/billingdsl`, `internal/fundingsource`, `internal/billinghttp` packages.

**True residual:**

1. Migrations 0077–0078: `tier_rules`/`tier_rules_version` on `billing_pricing_versions`; `funding_source` on `billing_ledger_claims`; `default_funding_source` on `api_keys`.
2. `internal/billingdsl` — DSL parser + evaluator (pure functions, no DB).
3. `internal/fundingsource` — resolver (reads `api_keys.default_funding_source`, checks active subscription).
4. `internal/billinghttp` — admin tier-rule CRUD + user funding-source preference endpoint.
5. Call-site modifications in `chat_completions_pricing.go` (~6 lines) and `billing/claim_gate.go` (~8 lines).

**Coverage estimate:** 25%

**Recommendation:** BUILD-NEW — The Tx1/Tx2 and quota infrastructure are reusable integration points, but the DSL evaluator, funding-source resolver, and schema changes are entirely absent.

---

## multi-oauth

**Existing coverage:**

- `internal/userauth/social_login.go:218-227` — `normalizeSocialProvider` switch: handles **only** `"google"` and `"github"` (lines 220-222). Default branch returns `""` → `ErrOAuthProviderMissing` for all other providers.
- `internal/userauth/social_login.go:11-12` — `SocialProviderGoogle = "google"`, `SocialProviderGitHub = "github"` constants.
- `internal/userauth/oauth_flow.go` — full PKCE S256 + nonce OAuth flow infrastructure (state, PKCE verifier, SSO sessions, SSRF-protected client). Directly reusable by all 4 new providers.
- `sql/migrations` — `oauth_flow_sessions` table exists (provider CHECK constraint covers only `"google"`, `"github"`). `social_identity_links` table exists with same constraint.
- `internal/userauth/types.go:37` — `ErrOAuthProviderMissing` sentinel exists and is the exact error the new providers will trigger without the switch extension.
- **No** `wechat`, `dingtalk`, `linuxdo`, or `oidc` in `normalizeSocialProvider`.
- **No** `pending_oauth_sessions` table. **No** `oidc_provider_configs` table.
- **No** `internal/socialprovider/wechat`, `/dingtalk`, `/linuxdo`, `/oidc` packages. **No** `internal/pendingoauth` package.

**True residual:**

1. Migration 0077: widen provider CHECK constraints + `pending_oauth_sessions` + `oidc_provider_configs` tables.
2. `internal/socialprovider/wechat`, `dingtalk`, `linuxdo`, `oidc` sub-packages.
3. `internal/pendingoauth` package (two-step flow for WeChat email-less identity).
4. `normalizeSocialProvider` extension (4 new cases + `oidc:<slug>` prefix routing).
5. `userauth/types.go` new sentinel + `social_login.go` pending-oauth branch.
6. `gatewayhttp/auth_handler.go` modifications (pending-email + complete-pending handlers).
7. `internal/oidcproviderhttp` admin CRUD handler.

**Coverage estimate:** 25%

**Recommendation:** SMALL-ENHANCEMENT — The OAuth infrastructure (PKCE, state management, SSRF client, session store) is fully implemented and reusable. The delta is adding 4 provider implementations on top of the existing `OAuthProvider` interface, plus the WeChat-specific pending-oauth two-step flow.

---

## content-moderation

**Existing coverage:**

- `internal/gateway/error_normalize.go:37` — `ErrorClassPlatformPolicy` exists as an error class, referenced in the design's `FeeCharger.ChargeViolation` integration point. The classification of upstream content-policy errors is already present.
- `sql/migrations/0004_rate_limiting.up.sql:54,83` — `temp_unschedulable_rules` JSONB on `provider_accounts` with `keywords[]` for **upstream error matching** (rate limiting / unschedulable rules). This is upstream-error-keyword matching, not inbound request body screening.
- `sql/migrations/0008_model_registry.up.sql:152` — comment references `content_policy_fallbacks` as a registry field name (reference only; no implementation found).
- `internal/billing/` — `ClaimGate.Reserve` + `Settler.Settle` + `Settler.Abort` all exist and are the exact Tx1/Tx2 hooks `FeeCharger.ChargeViolation` will use.
- `internal/gatewayhttp/chat_completions_dispatch.go` — the injection points for `Screener.Screen` and `FeeCharger.ChargeViolation` exist as existing files (permitted to modify).
- **No** `moderation_keywords` table. **No** `moderation_hashes` table. **No** `moderation_log` table. **No** `moderation_config` table.
- **No** `internal/moderation` or `internal/moderationhttp` packages.
- No inbound request body screening anywhere in the codebase.

**True residual:**

1. Migration 0077: 4 new tables (`moderation_keywords`, `moderation_hashes`, `moderation_log`, `moderation_config`).
2. `internal/moderation` package (screener, keyword/hash stores with LRU cache, ban counter, fee charger, audit logger, sampler).
3. `internal/moderationhttp` admin handlers (keywords CRUD, hashes CRUD, ban list, log query, config).
4. Wiring call-sites in `chat_completions_dispatch.go` (~2 injection points in existing files).

**Coverage estimate:** 10%

**Recommendation:** BUILD-NEW — The only reusable pieces are the `ErrorClassPlatformPolicy` classification and the Tx1/Tx2 billing path. The entire moderation subsystem (screening, blocklist management, ban counter, audit log) is absent.

---

## platform-settings

**Existing coverage:**

- `internal/billing/settings_store.go` / `sql/migrations/0046_billing_settings.up.sql` — `billing_settings` table (key-value, per-tenant). Handles billing-specific policy keys (`stream_input_only_interrupted_policy`, `balance_enforcement_mode`). Pattern is directly analogous to the proposed `platform_settings` table.
- `internal/email/settings_store.go` — `email_settings` table, same key-value pattern.
- `internal/gatewayhttp/admin_billing_settings_handler.go` — admin billing settings CRUD with audit. The allow-list + `ErrUnknownKey` pattern exists here.
- `internal/gatewayhttp/admin_email_settings_handler.go` — admin email settings CRUD.
- `internal/gatewayhttp/auth_handler.go:447,599` — `registration_disabled` error code and HTTP 403 response: registration mode is **already read** from `internal/userauth/Service.registrationMode()` (`service.go:83,210`). The `RegistrationMode` / `ErrRegistrationDisabled` types exist in `internal/userauth/types.go:30,59`.
- `sql/migrations/0003_streaming_forwarder.up.sql:31` — `total_stream_timeout_ms` exists on `routes` table (per-route timeout, not a global platform setting).
- **No** `platform_settings` table. **No** `internal/platformsettings` or `internal/platformsettingshttp` packages.
- **No** consolidated runtime-mutable global settings surface (invitation_required, captcha, oauth_providers_enabled, promo_enabled, stream_timeout_seconds, cooldown_429/529_seconds).
- Registration mode is currently read from the `userauth.Service` which reads from... (the existing pattern likely reads from config or a DB table — but it is NOT from a `platform_settings` table).

**True residual:**

1. Migration 0077: `platform_settings` table.
2. `sql/queries/platform_settings.sql` + sqlc generation.
3. `internal/platformsettings` package (types, store, service with fail-closed defaults + audit).
4. `internal/platformsettingshttp` package (3 admin endpoints, `platform_admin` only).
5. Integration: wire `registration_enabled`, `invitation_required` into `gatewayhttp` auth routes; `stream_timeout_seconds` into stream handler; `cooldown_*_seconds` into `internal/rate`.

**Coverage estimate:** 20%

**Recommendation:** SMALL-ENHANCEMENT — The `billing_settings` / `email_settings` pattern is exactly the structural template for `platform_settings`. The admin handler pattern (allow-list validation + audit) is already implemented and copy-adaptable. The registration mode concept exists in `userauth`. What's missing is the consolidated table, the new package, and the integration of new setting keys.

---

## Summary Table

| Gap | Coverage % | Recommendation |
|---|---|---|
| app-rate-limit | 20% | SMALL-ENHANCEMENT |
| ops-suite | 10% | BUILD-NEW |
| usage-dashboard | 25% | SMALL-ENHANCEMENT |
| per-key-controls | 20% | BUILD-NEW |
| totp-2fa | 5% | BUILD-NEW *(blocked on Owner approval)* |
| pricing-catalog | 30% | SMALL-ENHANCEMENT |
| notifications | 5% | BUILD-NEW |
| tiered-billing | 25% | BUILD-NEW |
| multi-oauth | 25% | SMALL-ENHANCEMENT |
| content-moderation | 10% | BUILD-NEW |
| platform-settings | 20% | SMALL-ENHANCEMENT |

---

## Key Findings and Notes

### Relay-log analogy confirmed for usage-dashboard
As with the relay-log example in the task brief, `usage-dashboard` has ~25% prior coverage: `GET /v1/me/usage` and `GET /admin/v1/usage` already provide flat paginated row access to `usage_records` with full filters. The design's proposed SQL queries add `GROUP BY` / `date_trunc` aggregation on top of the same table. **Do not rebuild the data access layer — extend it.**

### billing_settings pattern reuse
`platform-settings`, `pricing-catalog` (admin ratio handler), and `app-rate-limit` (policy store) can all follow the `billing_settings` key-value pattern (migration 0046, `settings_store.go`, `admin_billing_settings_handler.go`) rather than inventing new patterns.

### Migration 0077 collision
**Seven** gap designs all propose migration `0077` as their first migration. Since the current max is `0076`, only one can be `0077`. Implementation must be sequenced and each design's migration numbers reassigned. The PM must assign sequential numbers before dispatch.

### totp-2fa dispatch gate
The gap design explicitly states this must not be dispatched without Owner approval. The design is ready but gated. This is not a code coverage issue — it is an Owner process gate.

### multi-oauth normalizeSocialProvider is the key unlock
The entire multi-oauth gap is unlocked by extending 9 lines in `internal/userauth/social_login.go:218-227`. The PKCE/SSRF/session infrastructure is fully implemented and battle-tested. The new provider packages implement the `OAuthProvider` interface (line 35) with no changes to the interface itself.

### modelsync.HTTPFetcher reuse for pricing-catalog
`internal/modelsync/http_fetcher.go` implements the exact redirect-block and body-size-cap pattern the `upstreamprice.HTTPFetcher` needs. Implementation should import or copy this pattern rather than re-derive it.
