# Feature Tree: model-catalog-pricing

**Domain summary**: HUAKAI has a solid versioned billing core and a normalized model-registry/alias pipeline, but is missing admin CRUD for model management, model deprecation lifecycle, model-family grouping, batch/image/audio/reasoning tier pricing, and meaningful model-metadata in the public /v1/models response.

---

## Feature Coverage Table

| # | Feature | Status | Evidence (file:line or grep terms tried) | Gap note |
|---|---------|--------|------------------------------------------|----------|
| **MODEL REGISTRY / CATALOG** |
| 1 | Models table with canonical identity | PRESENT | `backend/sql/migrations/0008_model_registry.up.sql:56-91` — `models` table: canonical_id, protocol_family, default_provider_model_id, pricing_class, model_owner, status | Core present; see gaps in downstream features |
| 2 | OpenAI-compatible `GET /v1/models` endpoint | PRESENT | `backend/cmd/gateway/routes.go:52`; `backend/internal/modelhttp/list_handler.go:1-98` | Response is bare-bones: only id/object/created/owned_by — see #10 |
| 3 | `GET /v1/models/{model_id}` single-model endpoint | MISSING | Grep: `models/{`, `GetModel`, `modelhttp` — only list handler found | No per-model detail endpoint; OpenAI-spec requires this |
| 4 | Model metadata in list response (context_window, caps, pricing) | MISSING | `backend/internal/modelhttp/list_handler.go:30-37` — `modelObject` has only 4 fields | Sub2api and new-api both expose context_window, pricing, description, and capabilities inline |
| 5 | Admin CRUD: Create/Update/Delete model | MISSING | `backend/cmd/gateway/routes.go:479` — only `/admin/v1/model-sync`; grep: `POST /admin.*model`, `CreateModel`, `UpdateModel`, `DeleteModel` — zero hits | Operators cannot add custom models or override vendor catalog; only automated sync exists |
| 6 | Admin CRUD: List models (admin view with full detail) | MISSING | `backend/cmd/gateway/routes.go` — no admin GET /models; `backend/internal/adminhttp/` — no model files except model_sync_handler.go | Admin has no way to inspect full model state (capabilities, bindings, aliases together) |
| 7 | Vendor model auto-sync (pull catalog from provider) | PRESENT | `backend/internal/modelsync/scheduler.go`; `backend/internal/registry/model_sync_writer.go:1-150`; route at `backend/cmd/gateway/routes.go:479` | Anthropic/OpenAI/Gemini catalog pull with alias upsert/disable/reactivate |
| 8 | Model registry snapshots (versioned for audit replay) | PRESENT | `backend/sql/migrations/0008_model_registry.up.sql:24-32` — `model_registry_snapshots`: tenant_id, version, reason, updated_by_actor | D6 invariant; snapshot_version propagated to usage_records |
| 9 | Tenant vs global catalog scoping | PRESENT | `backend/sql/migrations/0008_model_registry.up.sql:99-125`; `backend/sql/migrations/0008_model_registry.up.sql:41-48` — `model_registry_tenant_policies.inherit_global_catalog` | Tenant-local and global scopes; inherit flag controls fallback |
| 10 | Model status lifecycle (active/disabled/deleted) | PARTIAL | `backend/sql/migrations/0008_model_registry.up.sql:72-74` — status: 'active'/'disabled'/'deleted'; deleted_at soft-delete | Missing: `deprecated` status, `sunset_date`, `migration_path`, `replacement_model_id` — see #11 |
| 11 | Model deprecation lifecycle (sunset date, migration hints) | MISSING | Grep: `deprecated`, `sunset`, `migration_path`, `replacement_model_id` — zero hits in migrations or Go code | No deprecation workflow; cannot schedule end-of-life or redirect users to a successor model |
| 12 | Model grouping / family / suite | MISSING | Grep: `ModelGroup`, `model_group`, `model_set`, `ModelFamily`, `model_family` — zero hits in any file | Cannot associate gpt-4o / gpt-4-turbo / gpt-4o-mini into a GPT-4 family; no shared pricing/quota across group |
| 13 | Model tier classification (premium / standard / free / experimental) | PARTIAL | `backend/sql/migrations/0008_model_registry.up.sql:70` — `pricing_class text NOT NULL DEFAULT 'standard'`; `backend/internal/registry/registry.go:51` — `PricingClass string` | pricing_class is a free-form opaque tag used for pricing-table lookup key; no tier semantics enforced, no user-visible tier field |
| **MODEL ALIASES & ROUTING** |
| 14 | Tenant-scoped aliases with normalized lookup | PRESENT | `backend/sql/migrations/0008_model_registry.up.sql:99-125`; `backend/sql/queries/registry.sql:8-24` — `LookupTenantAlias` with disabled-check | Unique index on (tenant_id, public_alias_normalized) |
| 15 | Global alias catalog with inheritance | PRESENT | `backend/sql/queries/registry.sql:26-40` — `LookupGlobalAlias`; `backend/sql/migrations/0008_model_registry.up.sql:41-48` | inherit_global_catalog flag per tenant |
| 16 | Per-binding provider model ID override | PRESENT | `backend/sql/migrations/0008_model_registry.up.sql:178` — `provider_model_id_override text nullable`; `backend/internal/registry/registry.go:45-46` | Maps alias → canonical → provider-specific model id at binding granularity |
| 17 | Alias status management (active/disabled/deleted) | PRESENT | `backend/sql/migrations/0008_model_registry.up.sql:108-112` — status + disabled_reason + source fields | Disable/reactivate tracked with source (vendor_model_sync vs operator) |
| 18 | Alias deprecation / sunset / migration hints | MISSING | Grep: `alias.*deprecated`, `alias.*sunset`, `alias.*migration` — zero hits | No "this alias is going away, use X instead" signal to API consumers |
| 19 | Alias fallback chain (try A → fall back to B) | MISSING | Grep: `alias.*fallback`, `alias.*chain`, `FallbackAlias` — zero hits | Fallback exists at pool-binding level (fallback_class) but not at alias resolution level |
| 20 | Alias bulk import | MISSING | `backend/internal/adminhttp/model_sync_handler.go` — only vendor sync, no bulk CSV/JSON import | |
| **PRICING — RATES** |
| 21 | Versioned rate table (billing_pricing_versions) | PRESENT | `backend/sql/migrations/0002_observability_billing.up.sql:277-291`; `backend/internal/billing/rate_table_source.go:1-160` | JSONB pricing_data, effective_from/to, is_public flag |
| 22 | Per-model input token pricing | PRESENT | `backend/internal/gatewayhttp/chat_completions_pricing.go:221` — `input_micro_usd` (aliases: input_rate_micro, input_cost_micro_usd, input_per_token_micro_usd); bootstrap: `backend/sql/migrations/0068_default_pricing_bootstrap.up.sql` | Flexible field-name aliases |
| 23 | Per-model output token pricing | PRESENT | `backend/internal/gatewayhttp/chat_completions_pricing.go:225` — `output_micro_usd` (aliases: output_rate_micro, output_cost_micro_usd, output_per_token_micro_usd) | |
| 24 | Cache read pricing | PRESENT | `backend/internal/gatewayhttp/chat_completions_pricing.go:241` — `cache_read_micro_usd` (aliases: cache_read_rate_micro, cached_input_micro_usd) | |
| 25 | Cache creation pricing — ephemeral tiers (5m/1h) | PRESENT | `backend/internal/gatewayhttp/chat_completions_pricing.go:229-237` — `cache_creation_5m_micro_usd`, `cache_creation_1h_micro_usd`, fallback `cache_creation_micro_usd` | Anthropic-specific tiers correctly modeled |
| 26 | Per-model pricing multiplier / markup | PRESENT | `backend/internal/gatewayhttp/chat_completions_pricing.go:245-248` — `model_multiplier`/`multiplier` JSON field; `completionRateVector.Multiplier decimal.Decimal`; applied at `scaledMicros()` | Multiplier in pricing data JSON; no per-tenant or per-user override capability |
| 27 | Batch API pricing tier | MISSING | Grep: `batch_price`, `batch_multiplier`, `batch_discount`, `batch_micro_usd` in pricing code — zero hits; `CapabilityBatch` exists in proto (`backend/internal/proto/capability_graph.go:26`) but has no rate entry | Sub2api and new-api both have batch price reduction (typically 50% off); HUAKAI tracks batch capability but has no batch pricing rate |
| 28 | Reasoning / thinking token separate pricing | MISSING | ReasoningTokens tracked (`backend/internal/proto/accounting.go:16`; `backend/internal/gateway/forwarder_types.go:110`) but grep: `reasoning_micro_usd`, `thinking_micro_usd`, `reasoning_price` — zero hits in pricing code | Reasoning tokens are counted as output tokens for billing; OpenAI o1/o3 and Claude 3.7 charge separately — HUAKAI over-bills or under-bills |
| 29 | Image output token pricing | MISSING | `image_output_tokens` tracked (`backend/sql/migrations/0002_observability_billing.up.sql:137`); `image_output_cost` field exists in usage_records; but grep: `image_output_micro_usd`, `image_price`, `image_output_price` in pricing code — zero hits | Field exists, cost tracked in DB, but no rate configured — cost always zero |
| 30 | Audio token pricing | MISSING | `CapabilityAudio` in proto (`backend/internal/proto/capability_graph.go:23`); grep: `audio_micro_usd`, `audio_price`, `audio_tokens` in pricing — zero hits | |
| 31 | Video token pricing | MISSING | `CapabilityVideo` in proto (`backend/internal/proto/capability_graph.go:24`); grep: `video_micro_usd`, `video_price` — zero hits | |
| 32 | Fine-tuned model custom pricing | MISSING | Grep: `fine_tune`, `finetune`, `fine_tuned_model`, `custom_model_price` — zero hits | No support for custom price per fine-tuned model variant |
| 33 | Regional / geography-based pricing | MISSING | Grep: `region_price`, `geo_price`, `regional_pricing` — zero hits | |
| 34 | Volume / commitment discount tiers | MISSING | Grep: `volume_discount`, `commitment`, `tier_price`, `price_tier` — zero hits | |
| 35 | Per-tenant custom pricing (beyond global rate table) | PARTIAL | `billing_pricing_versions.tenant_id` allows per-tenant rate tables; `backend/internal/billing/rate_table_source.go` supports tenant lookup | Admin cannot set per-tenant overrides via API — no endpoint; must be done via direct DB insert |
| 36 | Per-user / per-segment pricing | MISSING | Grep: `user_price`, `user_markup`, `segment_price`, `plan_price` — zero hits | All users under a tenant see identical rates |
| 37 | Time-based pricing (peak / off-peak) | MISSING | Grep: `time_price`, `peak_price`, `schedule_price` — zero hits | |
| 38 | Pricing admin API (create/update/list rate tables) | PRESENT | `backend/cmd/gateway/routes.go:90-92` — `GET /v1/pricing/rate-table`, `GET /v1/pricing/snapshots`, `GET /v1/pricing/snapshots/{id}` | Read-only; no write endpoint (POST/PUT) for creating new pricing versions via API |
| 39 | Pricing write API (publish new rate version) | MISSING | Grep: `POST /v1/pricing`, `PUT /v1/pricing`, `CreateRateTable`, `PublishPricing` — zero hits | Pricing updates require direct DB insert |
| **COST CALCULATION** |
| 40 | Pre-request predicted cost (reservation) | PRESENT | `backend/internal/billing/billing.go` — `ReserveRequest` with `PredictedCost`; `backend/internal/gatewayhttp/chat_completions_pricing.go:51-60` — `predictedCompletionCost()` | Heuristic estimation: length/4 for input, max_tokens for output |
| 41 | Post-response actual cost (settlement) | PRESENT | `backend/internal/billing/billing.go` — `SettleRequest` with `ActualCost`; `backend/internal/gatewayhttp/chat_completions_pricing.go:68-73` — `actualCompletionCost()` from real token counts | Decimal precision (shopspring/decimal) |
| 42 | Cost breakdown by token type | PRESENT | `backend/sql/migrations/0002_observability_billing.up.sql:155-163` — usage_records: input_cost, output_cost, cache_creation_cost, cache_read_cost, image_output_cost | 5 separate cost buckets |
| 43 | Cost reconciliation (late adjustment) | PRESENT | `backend/sql/migrations/0002_observability_billing.up.sql:250-268` — `billing_ledger_adjustments`; `usage_record_reconciliation_events` with cost_delta | Append-only signed delta; immutable originals |
| 44 | Cost attribution to billing claim | PRESENT | `backend/sql/migrations/0002_observability_billing.up.sql:19-66` — `billing_ledger_claims`: predicted_cost, actual_cost, claim lifecycle state machine | |
| 45 | Signed user cost receipt | PRESENT | `backend/sql/migrations/0028_user_cost_receipts.up.sql:3-22` — `user_cost_receipts`: cost_usd_micros, signer_fingerprint, signed_hash | F-TRUST / HUAKAI trust-chain differentiator |
| 46 | Cost forecasting / budget alert | MISSING | Grep: `budget_alert`, `cost_forecast`, `spending_limit`, `budget_limit` — zero hits | No pre-spend warning to users or tenant admins |
| 47 | Promotional / coupon cost adjustment | MISSING | Grep: `coupon`, `promo`, `discount_code`, `voucher` — zero hits | |
| **TOKEN COUNTING** |
| 48 | Input token tracking (from upstream response) | PRESENT | `backend/internal/proto/accounting.go:1-30`; usage_records.tokens_input; forwarder accumulation | Actual upstream-reported counts |
| 49 | Output token tracking | PRESENT | `backend/internal/proto/accounting.go:1-30`; usage_records.tokens_output | |
| 50 | Cache creation token tracking (5m + 1h split) | PRESENT | `backend/sql/migrations/0002_observability_billing.up.sql:134-136` — cache_creation_5m_tokens, cache_creation_1h_tokens | |
| 51 | Cache read token tracking | PRESENT | `backend/sql/migrations/0002_observability_billing.up.sql:132` — cache_read_tokens | |
| 52 | Reasoning / thinking token tracking | PRESENT | `backend/internal/proto/accounting.go:16`; `backend/internal/gateway/forwarder_types.go:110`; estimated via `canonicalReasoningEstimate()` | Tracked for auditing but NOT separately priced (see #28) |
| 53 | Image output token tracking | PRESENT | `backend/sql/migrations/0002_observability_billing.up.sql:137` — image_output_tokens | Tracked but cost rate is zero (see #29) |
| 54 | Heuristic pre-request token estimation | PRESENT | `backend/internal/gatewayhttp/chat_completions_pricing.go:154-169` — input: len/4, output: max_tokens value | Crude; no model-specific tokenizer |
| 55 | Real tokenizer (tiktoken / vendor SDK counter) | MISSING | Grep: `tiktoken`, `tokenizer`, `CountTokens`, `cl100k`, `o200k` — zero hits | All pre-request estimates use 4-bytes/token heuristic; no per-model or per-provider token counter |
| 56 | Tool / function-call token accounting | MISSING | Grep: `tool_tokens`, `function_tokens`, `ToolTokens` — zero hits | Tool schemas count as input tokens upstream but HUAKAI does not track or price separately |
| 57 | Per-model token counter selection | MISSING | Grep: `token_counter`, `TokenCounter`, `CounterStrategy` — zero hits | |
| **MODEL CAPABILITIES** |
| 58 | Capability storage table (model_registry_capabilities) | PRESENT | `backend/sql/migrations/0008_model_registry.up.sql:217-245` — capability, capability_value, capability_params jsonb, enabled, source | Parameterized; supports arbitrary capability values |
| 59 | Proto capability kind enum (14 families) | PRESENT | `backend/internal/proto/capability_graph.go:14-28` — text, tool_use, tool_result, thinking, cache_control, structured_output, computer_use, file, image, audio, video, live_session, batch, mcp_server, data_retention | 14 protocol-level capability kinds defined |
| 60 | Capability-model binding (registry ↔ proto alignment) | PARTIAL | `backend/internal/registry/model_sync_writer.go:363-432` — upserts free-text capability strings from vendor catalog; integration test seeds "tools" (`backend/internal/registry/postgres_registry_integration_test.go:341`) | No enforced constraint that registry capability values match CapabilityKind enum; strings can diverge from proto; no API to query which models support which capability |
| 61 | Vision / image input capability flag | PARTIAL | `CapabilityImage` in proto (`backend/internal/proto/capability_graph.go:22`); tracked in capability graph; but grep: `supports_vision`, `vision_capability`, model-level vision flag — zero hits in registry/admin | Proto handles image nodes in HCSF but no per-model registry flag exposed to routing or /v1/models |
| 62 | Tool use / function calling capability flag | PARTIAL | `CapabilityToolUse` in proto (`backend/internal/proto/capability_graph.go:15`); `backend/internal/registry/postgres_registry_integration_test.go:341` seeds "tools"; no enforced registry check | Similar gap: tracked in proto pipeline but not queryable at model-selection time |
| 63 | Structured output / JSON mode capability flag | PARTIAL | `CapabilityStructuredOutput`, `StructuredOutputJSONMode`, `StructuredOutputJSONSchema` in `backend/internal/proto/capability_structured.go:9-10` | Protocol-level handling present; no per-model registry flag gating which models support it |
| 64 | Extended thinking / reasoning capability flag | PARTIAL | `CapabilityThinking` in proto (`backend/internal/proto/capability_graph.go:17`); handled in anthropic + gemini + openai responses parsers | Proto-level only; no model registry capability flag for "this model supports extended thinking" |
| 65 | Audio / video / file capability flags | PARTIAL | `CapabilityAudio/Video/File` defined (`backend/internal/proto/capability_graph.go:21-24`); loss entries reference them | Defined in proto but not stored in model registry; no capability gate at routing |
| 66 | Batch processing capability flag | PARTIAL | `CapabilityBatch` defined (`backend/internal/proto/capability_graph.go:26`); `backend/internal/proto/cross_ref.go:93` references it | No batch pricing rate and no per-model batch-enabled registry flag |
| 67 | Computer use capability flag | PARTIAL | `CapabilityComputerUse` defined (`backend/internal/proto/capability_graph.go:20`) | Proto-only; no model registry flag |
| 68 | MCP server capability flag | PARTIAL | `CapabilityMCPServer` defined (`backend/internal/proto/capability_graph.go:27`) | Proto-only; no model registry flag |
| 69 | Capability-based model routing | MISSING | Grep: `RouteByCapability`, `capability_routing`, `capability_filter` — zero hits | Router cannot filter candidates by "must support image input" or "must support tools" |
| 70 | Capability cost multiplier (e.g., vision input costs more) | MISSING | Grep: `capability_multiplier`, `vision_multiplier`, `capability_price` — zero hits | All capability types are priced identically as input tokens |
| 71 | Per-model max output tokens | MISSING | `default_context_window` exists but grep: `max_output_tokens`, `MaxOutputTokens`, `max_completion_tokens` as stored per-model limit — zero hits in registry schema | Context window ≠ max output; models like Claude have max 8k/64k output regardless of 200k context |
| **PER-MODEL RATE LIMITS** |
| 72 | Per-binding RPM limit | PRESENT | `backend/sql/migrations/0008_model_registry.up.sql:179` — rpm_limit integer nullable; `backend/internal/registry/registry.go:76` — `BindingMetadata.RPMLimit *int32` | |
| 73 | Per-binding TPM limit | PRESENT | `backend/sql/migrations/0008_model_registry.up.sql:180` — tpm_limit integer nullable | |
| 74 | Per-binding max parallel requests | PRESENT | `backend/sql/migrations/0008_model_registry.up.sql:185` — max_parallel_requests integer nullable | |
| 75 | Model cooldown (upstream 429 tracking) | PRESENT | `backend/internal/rate/model_cooldown.go:1-100` — `RecordModelRateLimit()` with provider_account + model key + reset_at | |
| 76 | Per-user model quota (free-tier request limit) | MISSING | Grep: `user_model_quota`, `free_model_quota`, `per_user_limit`, `user_quota_model` — zero hits | No concept of "user X gets N calls/day to model Y" |
| 77 | Burst allowance / burst multiplier | MISSING | Grep: `burst`, `burst_limit`, `burst_multiplier` — zero hits | |
| 78 | Dynamic rate limit adjustment (auto-throttle based on load) | MISSING | Grep: `dynamic_rate`, `auto_throttle`, `adaptive_rate` — zero hits | |
| 79 | Rate limit pooling across aliases of same model | MISSING | Grep: `rate_pool`, `shared_limit`, `rate_sharing` — zero hits | Each binding has its own limits; no shared pool across aliases pointing to same model |
| **ADMIN OPERATIONS** |
| 80 | Admin: Create/Update model alias | MISSING | `backend/internal/adminhttp/` — no alias CRUD files; grep: `CreateAlias`, `UpdateAlias`, `POST /admin.*alias` — zero hits | Only vendor sync can create aliases |
| 81 | Admin: Create/Update model pool binding | MISSING | Grep: `CreateBinding`, `UpdateBinding`, `POST /admin.*binding` — zero hits | |
| 82 | Admin: Create/Update model capability | MISSING | Grep: `CreateCapability`, `UpdateCapability`, `POST /admin.*capability` — zero hits | |
| 83 | Admin: Pricing version management (write) | MISSING | `backend/cmd/gateway/routes.go:90-92` — only GET endpoints; grep: `POST /v1/pricing`, `CreatePricing`, `PublishRateTable` — zero hits | |
| 84 | Model deprecation workflow (admin-triggered) | MISSING | Grep: `DeprecateModel`, `SetSunset`, `deprecation_workflow` — zero hits | |
| 85 | Model bulk import (admin upload) | MISSING | Grep: `BulkImport`, `ImportModels`, `bulk_model` — zero hits | |
| **PROVIDER / VENDOR NAMESPACE** |
| 86 | Provider catalog table | PRESENT | `backend/sql/migrations/0001_pool_routing.up.sql:32-44` — providers: id, code, display_name, upstream_protocol, enabled | |
| 87 | Provider account model allow list | PRESENT | `backend/sql/migrations/0001_pool_routing.up.sql` — `provider_accounts.model_allow_list text[]` | Per-account model restriction |
| 88 | Protocol family classification | PRESENT | `backend/sql/migrations/0008_model_registry.up.sql:64` — models.protocol_family; `backend/internal/modelsync/types.go` — VendorAnthropic/OpenAI/Gemini | |
| 89 | Provider feature / capability matrix | PRESENT | `backend/sql/migrations/0005_protocol_translation.up.sql:23-56` — `protocol_capability_matrix`: (client_protocol × upstream_protocol × feature_name) → verdict | |
| 90 | Provider-specific token count method | MISSING | Grep: `provider_token_counter`, `VendorTokenCounter`, `provider_tokenizer` — zero hits | |
| 91 | Provider rate limit contract (declared RPM/TPM per vendor) | MISSING | Grep: `vendor_rpm`, `vendor_tpm`, `provider_rate_contract` — zero hits | Upstream limits discovered empirically (429s) rather than declared |
| 92 | Provider version / API version tracking | MISSING | Grep: `provider_version`, `api_version_tracking`, `vendor_api_version` — zero hits | |

---

## Top Missing Features, Ranked by Commercial Value

| Rank | Feature | Why it matters |
|------|---------|----------------|
| 1 | **Admin CRUD for models/aliases/bindings/capabilities** (#5, #6, #80, #81, #82) | Operators cannot onboard custom models or patch catalog without DB access; blocks self-serve model ops |
| 2 | **Batch API pricing tier** (#27) | Major AI providers (Anthropic, OpenAI) offer ~50% batch discounts; without it HUAKAI mis-prices batch workloads and loses cost-sensitive enterprise customers |
| 3 | **Reasoning / thinking token separate pricing** (#28) | OpenAI o1/o3 and Claude 3.7+ charge reasoning tokens differently; HUAKAI collapses them into output tokens → systematic billing error for AI-heavy workloads |
| 4 | **Pricing write API** (#39, #83) | Cannot update rates without DB access; makes pricing ops brittle and blocks SaaS multi-tenancy where each tenant needs their own rate card |
| 5 | **Model deprecation lifecycle** (#11) | No sunset dates or migration hints → provider retirements cause silent breakage; sub2api and new-api both have a deprecated flag with replacement model pointer |
| 6 | **/v1/models rich response** (#4, #3) | Clients (cursor, windsurf, coding assistants) rely on context_window, capability, and description fields to auto-select models; bare 4-field response forces client-side hardcoding |
| 7 | **Image / audio / video pricing rates** (#29, #30, #31) | image_output_tokens tracked but rate is zero → multimodal workloads are free by accident; Gemini and GPT-4V both have image pricing |
| 8 | **Capability-based model routing** (#69) | Router cannot answer "find me a model that supports tools AND vision"; forces clients to hardcode model IDs instead of capability querying |
| 9 | **Model grouping / family** (#12) | No way to express GPT-4 family or Claude 3 family; blocks shared quota, fallback routing within a family, and family-level pricing tiers |
| 10 | **Real tokenizer integration** (#55) | 4-bytes/token heuristic causes ±30% prediction error on code/structured content; over-reserves quota and distorts billing forecasts |
| 11 | **Per-user model quotas / free tier** (#76) | No free-tier gating per user per model; cannot offer "10 free GPT-4o calls/day" tier which is standard for SaaS AI gateway monetization |
| 12 | **Per-model max output tokens** (#71) | Conflating context_window with max output causes incorrect prompt-budget calculation for models with asymmetric limits (Claude 200k in / 8k out) |
| 13 | **Model alias deprecation / migration hints** (#18) | When old alias is retired, API consumers get a hard 404 rather than a redirect/warning to the new alias |
| 14 | **Volume / commitment discount tiers** (#34) | Enterprise deals typically involve committed-use discounts; HUAKAI has no rate-table mechanism for volume breakpoints |
| 15 | **Pricing write API + per-tenant pricing UI** (#39, #35) | Current state requires direct DB insert to set rates; blocks self-serve tenant onboarding with custom rate cards |
