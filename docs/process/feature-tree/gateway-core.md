# Gateway-Core Feature Tree

**Domain summary:** HUAKAI's gateway-core layer (Go `backend/` + Rust egress sidecar) is a commercially serious multi-tenant AI gateway with strong routing, billing, and trust-chain differentiation; its primary gaps are the auxiliary OpenAI-compatible endpoint surface (embeddings/images/audio/realtime), production-grade observability (Prometheus/OTEL absent), and inbound IP filtering.

> Audit date: 2026-06-02 | Branch: fix/hermes-phase-1-e33d940 | Auditor: Claude PM-Orchestrator (read-only pass, no code changes)

---

## Feature Table

| # | Feature | Status | Evidence (file:line or grep terms) | Gap Note |
|---|---------|--------|-------------------------------------|----------|
| **ENDPOINT SURFACE** |
| 1 | `POST /v1/chat/completions` | **PRESENT** | `backend/cmd/gateway/routes.go:48` → `gatewayhttp.NewChatCompletionsHandler` | — |
| 2 | `POST /v1/messages` (Anthropic native) | **PRESENT** | `backend/cmd/gateway/routes.go:50` → `gatewayhttp.NewMessagesHandler` | — |
| 3 | `POST /v1/responses` (OpenAI Responses API) | **PRESENT** | `backend/cmd/gateway/routes.go:49` → `gatewayhttp.NewResponsesHandler` | — |
| 4 | `GET /v1/models` listing | **PRESENT** | `backend/cmd/gateway/routes.go:52-55` → `modelhttp.NewListHandler` | — |
| 5 | `POST /v1/embeddings` | **MISSING** | grep "embed" in routes.go + handlers → 0 hits | No handler, no adapter; sub2api/new-api both expose this as a core relay endpoint |
| 6 | `POST /v1/images/generations` | **MISSING** | grep "image\|dall-e\|dalle" in routes.go → 0 hits | DALL-E / Stability relay absent; competitors relay it trivially |
| 7 | `POST /v1/audio/speech`, `/v1/audio/transcriptions` | **MISSING** | grep "audio\|speech\|transcri" in routes.go → 0 hits; `CapabilityAudio` constant at `proto/capability_graph.go:23` is parse-only | Capability constant exists in HCSF but no endpoint, no provider relay |
| 8 | `GET /v1/realtime` (WebSocket streaming) | **MISSING** | `backend/cmd/gateway/routes.go:154-157` returns 501 with "Phase 9+ mandatory roadmap item" | Explicitly deferred; roadmap entry F-RT-001 exists |
| **ROUTING & SELECTION** |
| 9 | Multi-pool routing with plan API | **PRESENT** | `backend/internal/router/router.go` (Router interface), `default_router.go:40-98` (Plan execution), `route_plan.go` | — |
| 10 | Weighted scoring / load balancing (PASR + HRW) | **PRESENT** | `backend/internal/pool/router/pasr.go`, `hrw_ring.go`, `scoring/blend.go`; weight columns in `sql/migrations/0001_pool_routing.up.sql:238-246` | — |
| 11 | Retry with end-class classification | **PRESENT** | `backend/internal/router/default_router.go:22-38` (5 retryable classes: 5xx, rate-limit, first-token-timeout, inter-event-timeout); `route_plan.go:65-76` AttemptBudget | — |
| 12 | Fallback / failover to next channel | **PRESENT** | `backend/internal/channelhealth/failover.go:42-100`; `pool/dispatcher/dispatcher.go:18-24` (5 dispatcher modes); `pool/dispatcher/retry.go:12-17` `canFallbackAfterPASRError()` | — |
| 13 | Circuit breaker (3-state FSM) | **PRESENT** | `backend/internal/circuitbreaker/breaker.go:16-150` (Closed/Open/HalfOpen states, threshold-based opening, half-open probing); `sql/migrations/0006_upstream_credential_management.up.sql:32` `circuit_open` | — |
| 14 | Channel health monitoring (5-state FSM) | **PRESENT** | `backend/internal/channelhealth/types.go:19-24` (Active/Degraded/CoolingDown/Ramping/Disabled/ManualPaused); `health_fsm.go`, `signal_classifier.go`, `store_postgres.go` | — |
| 15 | Model-level rate-limit cooldown tracking | **PRESENT** | `backend/internal/pool/router/types.go:86-89` ModelRateLimit with reset times; `channelhealth/service.go:427-441` | — |
| 16 | Singleflight / storm protection | **PRESENT** | `backend/internal/gateway/singleflight.go:45-66`; `storm_policy.go:18-63` (3-scope policy: account→endpoint→global) | — |
| 17 | Canary / shadow mode routing | **PRESENT** | `backend/internal/pool/dispatcher/dispatcher.go:18-24` (5 modes: default/shadow/canary/pasr-primary/pasr-strict); `sql/migrations/0001_pool_routing.up.sql` | — |
| **PROTOCOL & ADAPTERS** |
| 18 | HCSF canonical format (protocol-neutral IR) | **PRESENT** | `backend/internal/proto/hcsf.go`, `envelope.go`; `proto/proto.go:24-37` (ClientAdapter + UpstreamAdapter interfaces) | — |
| 19 | OpenAI Chat → HCSF adapter | **PRESENT** | `backend/internal/proto/openai_chat_request.go`, `openai_chat_parse.go` | — |
| 20 | Anthropic Messages → HCSF adapter | **PRESENT** | `backend/internal/proto/anthropic_messages_request.go`, `proto/anthropic/sse.go` | — |
| 21 | Gemini → HCSF adapter | **PRESENT** | `backend/internal/proto/gemini/`, `proto/gemini/sse.go:43` AccumulatedUsage | — |
| 22 | Tool / function calling | **PRESENT** | `backend/internal/proto/capability_tool.go:15-52` (ToolUseNode); `proto/tool_call_id.go`; `proto/capability_graph.go:15` CapabilityToolUse/Result; fixtures confirm multi-turn tool chains | — |
| 23 | Structured output (json_mode / json_schema) | **PRESENT** | `backend/internal/proto/capability_structured.go:1-34` (StructuredOutputNode, 4 modes); `envelope_validate.go:48` INV-36; fixtures `structured_output_minimal.json` | — |
| 24 | Vision / image input parsing | **PARTIAL** | `backend/internal/proto/openai_chat_parse.go:28-29` (image_url parsed → CapabilityImage loss entry); `openai_chat_types.go:55` ImageURL field | Parsed and tracked as ProtocolLoss; NOT forwarded to upstream providers. Comment in `openai_chat_request.go:18`: "image_url … 暂 warning loss" |
| 25 | Audio / video input parsing | **PARTIAL** | `backend/internal/proto/capability_graph.go:22-24` constants CapabilityImage/Audio/Video; parse code records loss | Same as vision — capability constants exist, provider relay absent |
| 26 | SSE streaming (multi-provider) | **PRESENT** | `backend/internal/gatewayhttp/chat_completions_stream.go:188` `text/event-stream`; `proto/openai/sse.go`, `proto/anthropic/sse.go`, `proto/gemini/sse.go`; stream_scanner.go | — |
| 27 | Protocol loss tracking | **PRESENT** | `backend/internal/proto/protocol_loss.go`; `ProtocolLossEntry` propagated through all adapters | Unique differentiator — no reference project has this |
| 28 | System / tool name rewriting (mimicry) | **PRESENT** | `backend/internal/gateway/system_rewrite.go`, `tool_name_rewrite.go`, `mimicry_compose.go` | — |
| 29 | Multi-provider upstream (10+ vendors) | **PRESENT** | `backend/internal/provider/` subdirs: openai, anthropic, gemini, bedrock, deepseek, fireworks, mistral, openrouter, perplexity, together, grok, groqcloud, antigravity, cursor, copilot, windsurf, kiro | — |
| 30 | Bedrock (AWS SigV4) adapter | **PRESENT** | `backend/internal/provider/bedrock/`, `backend/internal/proto/bedrock/`, `backend/internal/gateway/bedrock_stream_scanner.go` | — |
| **AUTHENTICATION** |
| 31 | Inbound Bearer API key auth (bcrypt) | **PRESENT** | `backend/internal/auth/api_key_resolver.go:95-110` (16-char prefix lookup + bcrypt fanout); returns Identity{TenantID,APIKeyID,UserID} | — |
| 32 | Session auth (user panel) | **PRESENT** | `backend/internal/usersession/`, `auth/session_middleware.go`, `gatewayhttp/session_handler.go` | — |
| 33 | Admin token auth (platform_admin / tenant_operator) | **PRESENT** | `backend/internal/admin/admin.go:53-56` roles; `adminhttp/model_sync_handler.go:23-72` Resolve + role enforcement | — |
| 34 | User account auth (register/login/password-reset/magic-link) | **PRESENT** | `backend/internal/userauth/service.go:280` password reset; `gatewayhttp/auth_session_handler_test.go:264` resetTokenSentinel; magic-link in email flow | — |
| 35 | OAuth upstream credential acquisition | **PRESENT** | `backend/internal/credentialacq/`, `credentialstore/`, `credentialworker/` | — |
| **RATE LIMITING & QUOTA** |
| 36 | Per-IP inbound rate limiting | **PRESENT** | `backend/cmd/gateway/rate_limit.go:44-56` (global 180 req/180s per IP; auth-specific stricter tiers); ipBucketRegistry with max 50,000 entries | — |
| 37 | Per-user / per-key quota (token limits) | **PRESENT** | `backend/internal/quota/` (service, policy, reservation, windows, reconciliation); `sql/migrations/0070_quota_subsystem.up.sql` (quota_policies, quota_windows, quota_reservations, quota_concurrency_slots) | — |
| 38 | Per-API-key RPM / RPS rate limiting | **MISSING** | grep "ratePerUser\|PerKeyRate\|key_rate\|rpm\|rps" → 0 hits outside quota package | Quota covers token budgets; no request-per-minute cap per API key (new-api and sub2api both have this); must rely on IP-level rate limit as proxy |
| 39 | Subscription-based quota binding | **PRESENT** | `backend/internal/subscription/store_postgres.go:716-763` quota policy installation from subscription caps | — |
| 40 | Per-model concurrency slots | **PRESENT** | `backend/internal/quota/service.go:49` RequestFingerprint + `db/quota/quota.sql.go:23` `quota_acquire_concurrency_slot()` | — |
| 41 | Request backpressure / queue | **PARTIAL** | `backend/internal/clienterr/catalog.go:49` CodeQueueWait exists; `db/billing/pool_accounts.sql.go:115` CapQueueSticky/Fallback schema fields | Schema and error code exist; no actual request queuing wired in handler path |
| **BILLING & COST** |
| 42 | Pre-flight cost reservation (Tx1) | **PRESENT** | `backend/internal/billing/billing.go:70` PredictedCost in ReserveRequest; `billing/claim_gate.go` | — |
| 43 | Post-request cost settlement (Tx2) | **PRESENT** | `backend/internal/billing/billing.go:90` ActualCost in SettleRequest; `billing/settler.go` | — |
| 44 | Cache-hit zero-cost path | **PRESENT** | `backend/internal/billing/billing.go:45-51` CommitCacheHit | — |
| 45 | Per-request cost receipt | **PRESENT** | `backend/internal/gatewayhttp/cost_receipt_handler.go:101`; `cmd/gateway/routes.go:82-88` `/v1/receipts/{request_id}` GET/verify | — |
| 46 | Admin balance credit (manual top-up) | **PRESENT** | `backend/internal/adminhttp/balance_credit_handler.go:30` AdminAdjustBalance; idempotency conflict detection | — |
| 47 | Admin balance debit | **PARTIAL** | `backend/internal/adminhttp/balance_credit_handler.go:120` `admin_debit_not_yet_supported` error | Hardcoded refusal; debit path not implemented |
| 48 | Billing DLQ + reconciliation | **PRESENT** | `backend/internal/billing/settler.go:776-787` DLQ enqueue on failure; `billing/reconciliation_worker.go`, `billing/lease_sweep.go` | — |
| 49 | Pricing rate tables | **PRESENT** | `backend/internal/billing/rate_table_source.go`, `billing/settings_resolver.go`; `/v1/pricing/*` endpoint | — |
| 50 | Balance holds (wallet model) | **PRESENT** | `backend/internal/balancehold/balancehold.go:35-95` (ReserveBalanceHold, CaptureBalanceHold, ReleaseBalanceHold) | — |
| **OBSERVABILITY** |
| 51 | Cryptographic audit ledger (Merkle tree) | **PRESENT** | `backend/internal/auditledger/postgres.go:46` Append; `auditledger/postgres.go:271` LatestMerkleRoot; `trustreceipt/canonical.go:60` CanonicalHash; `cmd/gateway/routes.go:71` `/v1/audit/merkle-tree.json` | HUAKAI-unique differentiator |
| 52 | Request ID propagation + validation | **PRESENT** | `backend/cmd/gateway/middleware.go:45-48` RequestID middleware; `gatewayhttp/request_id_limiter.go:8` MaxRequestIDLength=256; X-Huakai-Request-Id response header | — |
| 53 | User usage analytics (`/v1/me/usage`) | **PRESENT** | `backend/internal/meusagehttp/handler.go:78`; `cmd/gateway/routes.go:56` | — |
| 54 | Admin observability APIs (audit-events, billing claims) | **PRESENT** | `cmd/gateway/routes.go` `/admin/v1/usage`, `/admin/v1/billing/claims`, `/admin/v1/audit-events` | — |
| 55 | Structured logs (zap) | **PRESENT** | zap used throughout; `internal/redact/` package enforces system_log field allowlist | — |
| 56 | PII redaction in system logs | **PRESENT** | `backend/internal/redact/` package (allowlist.go, redactor.go); 2026-05-13 Owner directive T0 constraint; prompt/completion body never logged | — |
| 57 | expvar metrics (`/debug/vars`) | **PRESENT** | `backend/internal/cachemetrics/cachemetrics.go:39-92`; `pool/dispatcher/metrics.go` (shadow/canary/mode counters); `billing/settings_resolver.go` resolver counters | — |
| 58 | Prometheus / OpenTelemetry metrics | **MISSING** | grep "prometheus\|promhttp\|otelhttp\|go.opentelemetry" → 0 hits; `pool/dispatcher/metrics.go:18` comment: "prometheus 是后续 atom" | Only expvar stdout-accessible metrics; no scrape endpoint; operational gap for SRE tooling |
| 59 | Distributed tracing (trace_id propagation) | **PARTIAL** | `redact/allowlist.go` includes trace_id as safe field; no W3C Trace-Context header injection or span creation found | trace_id field exists in log schema; no actual tracing instrumentation |
| **CACHING** |
| 60 | L2 in-memory response cache | **PRESENT** | `backend/internal/cache/store.go` MemoryStore; `cmd/gateway/lifecycle.go:196-204` L2 config + init; admin routes `/admin/v1/cache/l2` | — |
| 61 | Distributed (Redis) response cache | **MISSING** | grep "redis\|Redis\|REDIS" → 0 hits across all `.go` files | Only in-memory; no Redis backend; L2 cache does not survive restarts or scale horizontally |
| 62 | Prompt-level cache token tracking | **PRESENT** | `backend/internal/cachemetrics/cachemetrics.go:51-54` keyCreationTotal/keyReadTotal; `trustreceipt/builder.go:143-150` CacheCreationInputTokens/CacheReadInputTokens | — |
| **RELIABILITY** |
| 63 | Graceful shutdown (SIGTERM) | **PRESENT** | `backend/cmd/gateway/lifecycle.go:90-168` (60s HTTP shutdown, 5s per worker); `main.go:65-66` signal.NotifyContext SIGINT/SIGTERM | — |
| 64 | Audit DLQ (failed ledger writes) | **PRESENT** | `backend/internal/auditledger/dlq_producer.go:15` EnqueuePreparedEntryToDLQ; `obs/dlq/store_postgres.go:175` INSERT INTO dlq_events | — |
| 65 | DLQ admin replay API | **PRESENT** | `cmd/gateway/routes.go` `/admin/v1/dlq/*`; `gatewayhttp/admin_dlq_handler.go` | — |
| 66 | Context cancellation / stream abort | **PRESENT** | `backend/internal/gateway/forwarder.go:143` upstreamCtx+Cancel; `pool/dispatcher/dispatcher.go:321` context.Canceled handling; `stream_scanner_test.go:179` OrchestratorCancel classification | — |
| 67 | First-token + inter-event timeout classification | **PRESENT** | `backend/internal/router/default_router.go:25-26` retryableEndClassFirstTokenTimeout + InterEventTimeout | — |
| **SECURITY** |
| 68 | CORS with origin allowlist (no wildcard) | **PRESENT** | `backend/cmd/gateway/middleware.go:49-56,147-212`; HUAKAI_CORS_ALLOWED_ORIGINS env; `cors_security_headers_test.go` enforces no wildcard | — |
| 69 | Anti-H2 smuggling (Rust TLS sidecar) | **PRESENT** | `exploratory/rust-core-gateway/merged/crates/tls-sidecar/` (duplicate Content-Length + duplicate Host rejection per d3d1893); also go-level ContentLength validation in `privacy/middleware.go:38` | Rust sidecar not yet in production Go path; go-path protection is basic |
| 70 | Request body size enforcement | **PARTIAL** | `backend/internal/privacy/middleware.go:38` ContentLength == 0 check; `gatewayhttp/audit_verify_handler.go:169` auditVerifyBodyMaxBytes | No per-tenant configurable max body size; hardcoded per-handler limits only |
| 71 | IP allowlist / blocklist | **MISSING** | grep "ipAllow\|ipBlock\|ipWhitelist\|blacklist\|IPFilter\|allowedIP" → 0 hits | No IP-level ACL; sub2api and new-api both support IP whitelist per channel/user; gap for enterprise tenants |
| 72 | Content moderation / prompt injection filter | **MISSING** | grep "moderation\|contentFilter\|injection_filter" → 0 hits | No OpenAI-style `/v1/moderations` endpoint or input guard; potential brand risk in multi-tenant deployments |
| 73 | Header sanitization (upstream) | **PRESENT** | `backend/internal/provider/anthropic/passthrough.go:79`, `openai/passthrough.go:79` reconstruct headers from scratch; `gateway/forwarder.go` strips inbound headers | — |
| 74 | Bearer secret never logged | **PRESENT** | `backend/internal/auth/sanitizer.go`; CMB-5 boundary; redact allowlist excludes any credential field | — |
| **MULTI-TENANCY & ADMIN** |
| 75 | Multi-tenancy (tenant_id isolation) | **PRESENT** | TenantID threaded through all auth, quota, billing, cache, audit layers; `cachemetrics/cachemetrics.go:170,224` per-tenant cache segment isolation | — |
| 76 | Admin pool management | **PRESENT** | `cmd/gateway/routes.go` `/admin/v1/pools/*`; `adminhttp/` pool handlers | — |
| 77 | Admin provider-account management | **PRESENT** | `cmd/gateway/routes.go` `/admin/v1/provider-accounts/*`; `adminhttp/provider_account_health_handler.go` | — |
| 78 | Admin billing settings | **PRESENT** | `cmd/gateway/routes.go` `/admin/v1/billing/*`; `adminhttp/billing_settings_handler.go` | — |
| 79 | Admin credential renewal / acquisition | **PRESENT** | `cmd/gateway/routes.go` `/admin/v1/credentials/*`; `credentialworker/` | — |
| 80 | Voucher / coupon redemption | **PRESENT** | `backend/internal/gatewayhttp/voucher_handler.go:84,212` `/redeem` endpoint; `voucher/types.go:20` GrantKindSubscription | — |
| 81 | Subscription management | **PRESENT** | `backend/internal/subscription/` (service, activation, reminder, types); quota policy installation from subscription | — |
| 82 | Invitation system | **PRESENT** | `backend/internal/community/invitation/`; `cmd/gateway/routes.go:146` `/v1/invitations` | — |
| 83 | Email notifications | **PRESENT** | `backend/internal/subscription/reminder_mailer.go:19` NewEmailReminderMailer; `mailinfra` package; ErrEmailBackendUnconfigured graceful fallback | — |
| 84 | Model catalog sync | **PRESENT** | `backend/internal/modelsync/`, `internal/registry/model_sync_writer.go`; capability flags including ContextWindow | — |
| 85 | Model context-window capability flag | **PRESENT** | `backend/internal/modelsync/types.go:21` ContextWindow int; `router/route_plan.go:20`; fetched from upstream API `modelsync/http_fetcher.go:235` | — |
| 86 | Model name aliasing / registry resolution | **PRESENT** | `backend/internal/registry/registry.go:30-64` AliasNormalize→LookupTenantAlias→GetModelByID pipeline; ProviderModelIDOverride per binding | — |
| 87 | Payment webhook (auto credit) | **PRESENT** | `cmd/gateway/routes.go` payment webhook routes (signature-verified, no session required); `payment/` package | — |
| 88 | Payment history / methods | **PRESENT** | `cmd/gateway/routes.go` `/v1/users/me/payments/*`, `/v1/users/me/subscriptions/*` | — |
| **RUST EGRESS SIDECAR** |
| 89 | TLS mimicry / JA3 fingerprint spoofing | **PRESENT** | `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/` (14 files): JA3 builder, BoringSSL/OpenSSL adapters, ClientHello constructor, TLS capture | Not in production Go path; exploratory lane |
| 90 | SSE protocol relay (Rust) | **PRESENT** | `exploratory/rust-core-gateway/merged/crates/core_gateway/src/stream_pipeline/` Anthropic+OpenAI stream parsers | Not in production path |

---

## Top Missing Features, Ranked by Commercial Value

| Rank | Feature | Why it matters commercially | Effort estimate |
|------|---------|------------------------------|-----------------|
| 1 | **`/v1/embeddings` endpoint** | Most used OpenAI-compatible endpoint after chat; required for RAG customers; sub2api and new-api relay it as a passthrough; every competitor supports it | Low — pure passthrough relay, same auth/billing stack |
| 2 | **Prometheus / OpenTelemetry metrics** | SRE/ops teams require scrape-based alerting; expvar is dev-only; no Grafana/Datadog integration possible; blocks enterprise sales | Medium — add `promhttp.Handler()`, convert expvar counters |
| 3 | **Per-API-key RPM / RPS rate limiting** | Token-budget quota exists but a customer can hammer 10,000 req/s on one key and starve others; new-api and sub2api both have per-key rate limit separate from quota | Medium — rate.Limiter per key_id, Redis or local bucket |
| 4 | **Redis-backed distributed L2 response cache** | In-memory cache lost on restart / doesn't scale horizontally across pods; zero cache benefit in multi-instance deploy | Medium — abstract Store interface already exists; add redis Store impl |
| 5 | **`/v1/audio/speech` + `/v1/audio/transcriptions`** | Whisper transcription and TTS are growth segments; capability constants already exist in HCSF; pure relay possible | Low-Medium — adapter + endpoint mount; provider relay straightforward |
| 6 | **IP allowlist / blocklist** | Enterprise security requirement; sub2api supports per-channel IP whitelist; absence means no defense against credential-sharing or geo-restriction enforcement | Medium — middleware + DB table + admin API |
| 7 | **`/v1/images/generations`** | DALL-E 3 / Stability relay; lower priority than embeddings but needed for full OpenAI-parity spec | Low — pure relay; no streaming needed |
| 8 | **Admin balance debit** | Manual quota reduction / refund admin workflow blocked (`admin_debit_not_yet_supported`); needed for ops/support tooling when over-crediting | Low — unblock the gated path in `balance_credit_handler.go:120` |
| 9 | **Content moderation / prompt injection filter** | Required for enterprise acceptable-use compliance; `/v1/moderations` passthrough or in-gateway filter; absence is a brand and contractual risk in multi-tenant B2B | Medium-High — moderation endpoint + optional pre-filter middleware |
| 10 | **`/v1/realtime` WebSocket** | Highest latency-sensitive use-case (voice assistants); already roadmapped as F-RT-001 Phase 9+; confirms the gap is known | High — full WebSocket relay architecture |
| 11 | **Per-tenant configurable max request body size** | Hardcoded per-handler limits; enterprise tenants need custom limits to prevent abuse without gateway restart | Low — config field + middleware parameterization |
| 12 | **Request queueing / backpressure (wired)** | Schema columns + error code exist (`CodeQueueWait`) but not wired; under heavy load requests fail immediately instead of queuing | Medium — wire `CapQueueSticky/Fallback` into handler reject/wait logic |
| 13 | **Distributed tracing (W3C Trace-Context)** | trace_id field in log allowlist but no span creation or header propagation; blocks correlation with APM tools | Medium — add otel SDK, inject/extract W3C headers |
| 14 | **Vision / audio fully forwarded to providers** | Parsed as `ProtocolLoss` warning today; customers sending image inputs get silent capability loss; breaking for GPT-4o/Claude vision use-cases | Medium — upstream adapters need multimodal content part forwarding |
