# Feature Tree: observability-analytics

**Domain summary**: HUAKAI has a solid structured-logging and billing-event backbone (async eventbus, DLQ, Tx2 idempotency, per-request token/cost/end-class records, channel-health audit, admin query APIs) but lacks a Prometheus scrape endpoint, global latency histograms, OpenTelemetry distributed tracing, real-time threshold webhooks, log-retention policy, and fine-grained per-channel usage counters.

---

## Feature Coverage Table

| Feature | Status | Evidence (file:line or grep tried) | Gap Note |
|---------|--------|-------------------------------------|----------|
| **F-OBS-01** Per-request log records (model, provider, latency, tokens, cost, status) | PRESENT | `backend/internal/eventbus/types.go:58-77` (RequestCompletionEvent); `backend/internal/obs/repository.go:51-75` (UsageRow with TokensInput, TokensOutput, ActualCost, EndClass) | — |
| **F-OBS-02** Request body logging (prompt content, PII masking) | PARTIAL | `backend/internal/privacy/middleware.go:32-64` (body read + AllowlistRedactor); `backend/internal/eventbus/types.go:70` (RedactedBodyRef hash ref) | Raw body zeroed after metadata extraction; only hash ref persisted — no content-queryable prompt log |
| **F-OBS-03** Response body logging (completion content) | PARTIAL | `backend/internal/trustreceipt/builder.go` (signed receipt with completion); `backend/internal/audit/receipt_storage.go` (receipt store) | Body captured only in audit-ledger receipts path; not in the main `usage_records` table for all flows |
| **F-OBS-04** Structured log fields (JSON / typed schema) | PRESENT | `backend/internal/obs/repository.go:51-75` (typed Go struct → sqlc); `backend/internal/eventbus/types.go:58-77` | sqlc-generated, fully typed; no raw-string serialisation |
| **F-OBS-05** Log retention policy / TTL / purge | MISSING | grep: `retention\|ttl\|purge\|expire\|cleanup.*log` — no matches in `backend/internal/obs/` or migration files | No automated purge or TTL; must be handled at DB layer manually |
| **F-OBS-06** Input token count per request | PRESENT | `backend/internal/gateway/forwarder_types.go:84` (TokensInput int); `backend/internal/obs/repository.go:59` | — |
| **F-OBS-07** Output token count per request | PRESENT | `backend/internal/gateway/forwarder_types.go:85` (TokensOutput int); `backend/internal/obs/repository.go:60` | — |
| **F-OBS-08** Cached token count (cache read/write) | PRESENT | `backend/internal/gateway/forwarder_types.go:87-89` (CacheCreationTokens, CacheReadTokens); `backend/internal/cachemetrics/cachemetrics.go`; `backend/internal/obs/repository.go:61-62` | — |
| **F-OBS-09** Cost calculation per request (USD / credits) | PRESENT | `backend/internal/gateway/forwarder_types.go:91` (ActualCost decimal.Decimal); `backend/internal/billing/settler.go` (Settle); `backend/internal/obs/repository.go:63` | — |
| **F-OBS-10** Cumulative cost per user / API key / channel | PRESENT | `backend/internal/meusagehttp/handler.go` (GET /v1/me/usage); `backend/internal/gatewayhttp/admin_observability_handler.go` (GET /admin/v1/usage) | — |
| **F-OBS-11** Per-user quota consumption tracking | PRESENT | `backend/internal/quota/types.go` (Scope, Metric=MetricCostUSD, Reservation); `backend/internal/quota/service.go` (ReserveRequest + settle loop) | Scope-aware: user / api_key / channel / pool_group / provider_account |
| **F-OBS-12** Per-channel / per-provider usage counters | PARTIAL | `backend/internal/pool/router/metrics.go`; `backend/internal/channelhealth/service.go`; `backend/internal/gatewayhttp/admin_observability_handler.go` (Provider/Model filter) | Channel health state machine exists; no separate per-channel usage-counter table; usage queries are model/provider/tenant-scoped aggregates |
| **F-OBS-13** Per-model usage breakdown | PRESENT | `backend/internal/obs/repository.go` (RequestedModel field); `backend/internal/db/billing/observability.sql.go` (Model filter in ListUsageRecords); `backend/internal/gatewayhttp/admin_observability_handler.go:146` | — |
| **F-OBS-14** Rate-limit hit counters | PRESENT | `backend/internal/quota/types.go` (AuditEvent with DecisionCode, RetryAfterSeconds); quota_audit_events table; channelhealth RateLimitHitRate threshold | — |
| **F-OBS-15** Admin action audit log | PRESENT | `backend/internal/auditledger/` (Merkle append-only ledger); `backend/internal/audit/receipt_worker.go`; `backend/internal/gatewayhttp/admin_observability_handler.go` | — |
| **F-OBS-16** API key creation / revocation events | PRESENT | `backend/internal/admin/issuer.go`; `backend/internal/admin/revoker.go`; auditledger emits lifecycle events; route `/admin/v1/api-keys` | — |
| **F-OBS-17** Authentication events (login, token refresh) | PRESENT | `backend/internal/userauth/service.go`; `backend/internal/hermes/audit.go`; `backend/internal/usersession/` | — |
| **F-OBS-18** Billing / top-up / credit events | PRESENT | `backend/internal/payment/audit.go`; `backend/internal/auditledger/`; `backend/internal/subscription/`; `backend/internal/billing/settler.go` | — |
| **F-OBS-19** Prometheus / metrics endpoint (/metrics) | PARTIAL | `backend/internal/cachemetrics/cachemetrics.go:51` (expvar.NewMap "cache_token_count"); `backend/internal/clientid/metrics.go:36` (expvar.NewMap "clientid_request_count"); grep: `prometheus\|promhttp` — no matches | Only Go stdlib expvar (/debug/vars) exposed; no Prometheus scrape endpoint; no histograms/counters for import into Grafana/Alertmanager |
| **F-OBS-20** Request count by model / channel / user | PRESENT | `backend/internal/obs/repository.go` (ListUsage with model/tenant filter); `backend/internal/gatewayhttp/admin_observability_handler.go` (Model filter) | Derived from usage_records table via SQL; not a real-time counter |
| **F-OBS-21** Latency histogram / percentiles (global) | MISSING | grep: `Histogram\|latency.*p99\|p95\|percentile\|latency_bucket` in `backend/internal/obs/`, `backend/internal/gatewayhttp/` — no matches | Per-channel P99 in channelhealth; no global request-level latency histogram or P99/P95 endpoint |
| **F-OBS-22** Error rate / error code breakdown | PRESENT | `backend/internal/gateway/forwarder_types.go:18-35` (StreamEndClass enum: graceful/upstream_4xx/5xx/timeout/…); `backend/internal/obs/repository.go:64` (EndClass string); admin API Status filter | — |
| **F-OBS-23** Token throughput (tokens/sec) | PARTIAL | `backend/internal/cachemetrics/cachemetrics.go`; `backend/internal/proto/stream_billing_state.go` (DeliveredTokenCount accumulates in-stream) | No real-time tokens/sec endpoint; throughput derivable post-facto from usage_records but no live metric |
| **F-OBS-24** Provider health metrics (success rate, latency per upstream) | PRESENT | `backend/internal/channelhealth/service.go` (rolling window: FailedAttempts, LatencyP99MS, RateLimitHitRate); `backend/internal/pool/router/metrics.go`; `backend/internal/gatewayhttp/channel_health_admin_handler.go` | — |
| **F-OBS-25** Log query API (pagination, filter by user/model/date) | PRESENT | `backend/internal/meusagehttp/handler.go:78-129` (from/to/cursor/limit params); `backend/internal/gatewayhttp/admin_observability_handler.go:58-97` | — |
| **F-OBS-26** Usage summary API (aggregate stats per key/user) | PRESENT | `backend/internal/meusagehttp/handler.go` (aggregates by APIKeyID + TenantID); admin usage handler | — |
| **F-OBS-27** Cost breakdown API | PRESENT | `backend/internal/meusagehttp/handler.go:172` (actual_cost in response); `backend/internal/gatewayhttp/admin_observability_handler.go` (ActualCost in rows) | — |
| **F-OBS-28** Admin dashboard data endpoint | PRESENT | `backend/internal/gatewayhttp/admin_observability_handler.go` (NewUsageHandler, NewClaimsHandler, NewAuditEventsHandler); routes: `/admin/v1/usage`, `/admin/v1/billing/claims`, `/admin/v1/audit-events` | — |
| **F-OBS-29** SSE/streaming request token counting (mid-stream) | PRESENT | `backend/internal/proto/anthropic_messages_stream.go`; `backend/internal/proto/openai/sse.go` (mid-stream token delta parsing); `backend/internal/obs/repository.go:68` (DeliveredTokenCount int64) | — |
| **F-OBS-30** Real-time usage feed / webhook on threshold | MISSING | grep: `webhook\|threshold.*notify\|usage.*alert\|SSE.*usage` — no matches | No subscription-based alert, webhook delivery, or SSE push when quota/cost threshold is breached |
| **F-OBS-31** Async/buffered log writes (non-blocking request path) | PRESENT | `backend/internal/observability/billing_persister_handler.go`; `backend/internal/eventbus/bus.go` (TierHigh/TierMed/TierLow worker pools) | Events dispatched through async bus; request path not blocked on DB write |
| **F-OBS-32** Log write failure handling (DLQ, drop-and-alert) | PRESENT | `backend/internal/dlq/` (types.go, worker.go, handlers.go, store.go); `backend/internal/observability/billing_persister_handler.go:62-76` (DLQKind enum + DLQPayload); `backend/internal/settlementrecovery/` (DLQ replay handler) | DLQ replay via POST `/admin/v1/usage-record-dlq/{id}/replay` |
| **F-OBS-33** Idempotent log records (dedup on retry) | PRESENT | `backend/internal/billing/settler.go` (Tx2 atomicity); `backend/internal/audit/receipt_formatter.go` (deterministic receipt hash); DLQ replay encoding | Tx2 two-phase settlement prevents double-write on retry |
| **F-OBS-34** Request ID / trace ID propagated through logs | PRESENT | `backend/internal/proto/request_meta.go:21,35` (RequestID mandatory, ≤256 chars); `backend/internal/eventbus/types.go:64,93`; `backend/internal/meusagehttp/handler.go:63,168` | X-Request-Id header propagated; request_id queryable in user API |
| **F-OBS-35** OpenTelemetry / distributed tracing integration | MISSING | grep: `otel\|opentelemetry\|otlp\|jaeger\|zipkin` — no matches | Only inline RequestID string correlation; no span export, no OTel SDK wiring |
| **F-OBS-36** Correlation between upstream attempt and log record | PRESENT | `backend/internal/gateway/forwarder.go` (AttemptSeq per upstream call); `backend/internal/eventbus/types.go` (AuditLedgerID + AuditLedgerDLQRef); `backend/internal/billing_ledger_claims` (ClaimID + AttemptSeq) | — |
| **F-OBS-37** Per-channel error tracking (last-error, error-count) | PRESENT | `backend/internal/channelhealth/types.go:271-293` (WindowSummary: FailedAttempts, LastSignalClass, LastSignalAt, RampFailureCount); `backend/internal/channelhealth/service.go` (ApplySignal rolling window) | — |
| **F-OBS-38** Channel disable/enable events logged | PRESENT | `backend/internal/channelhealth/types.go` (AuditEventType: EventDisabled, EventRecovered, EventRampStarted); `backend/internal/channelhealth/service.go:112` (emitTransitionEvents) | — |
| **F-OBS-39** Provider latency tracking and alerting | PRESENT | `backend/internal/channelhealth/types.go:119-164` (Policy: LatencyP99ThresholdMS, ErrorRateThresholdPct, RateLimitHitRateThresholdPct); `backend/internal/channelhealth/service.go` (evaluate → auto disable/cooldown/ramp) | — |

---

## Coverage Summary

| Status | Count | Features |
|--------|-------|----------|
| PRESENT | 31 | F-OBS-01,04,06-10,11,13-18,20,22,24-29,31-34,36-39 |
| PARTIAL | 5 | F-OBS-02,03,12,19,23 |
| MISSING | 3 | F-OBS-05,21,30,35 |

---

## Top Missing, Ranked by Commercial Value

1. **F-OBS-19 (PARTIAL→full) — Prometheus /metrics endpoint**
   No Prometheus scrape target exists; only Go stdlib expvar. Every commercial gateway (new-api, sub2api, OpenRouter) exposes `/metrics` for Grafana/Alertmanager. Without it, operators cannot build latency/error dashboards or on-call alerts. Highest-impact single gap; straightforward to add (`promhttp.Handler()` + instrument key counters/histograms).

2. **F-OBS-21 — Global latency histogram / P50/P95/P99**
   Per-channel P99 exists in channelhealth, but there is no request-level latency histogram aggregated across all requests. Operators and tenants cannot see end-to-end gateway latency percentiles. Required for SLAs, capacity planning, and public status pages.

3. **F-OBS-35 — OpenTelemetry / distributed tracing**
   No OTel SDK, no span export (OTLP/Jaeger/Zipkin). Tracing is table-stakes for debugging multi-hop routing (gateway → pool → channel → upstream). Missing makes production incidents significantly harder to diagnose.

4. **F-OBS-30 — Real-time usage threshold webhooks / SSE push**
   No mechanism to notify users or operators when quota/cost thresholds are breached in near-real-time. New-api and sub2api both fire email/webhook on quota exhaustion. Critical for SaaS UX (overage warnings, budget caps).

5. **F-OBS-05 — Log retention policy / TTL / purge**
   No automated purge of `usage_records`, `quota_audit_events`, or `channel_health_audit_events`. In a high-traffic deployment these tables grow unbounded. sub2api has a configurable log retention window. Missing causes operational storage bloat and potential DB degradation.

6. **F-OBS-02 (PARTIAL→full) — Queryable prompt-content logging (opt-in)**
   Raw body is zeroed after privacy middleware; only a hash ref is persisted. Enterprise tenants often need optional, permission-gated prompt logging for compliance and debugging. sub2api supports an opt-in log body toggle per channel. Currently not possible in HUAKAI.

7. **F-OBS-12 (PARTIAL→full) — Fine-grained per-channel usage counters table**
   Channel health state machine tracks failure signals, but there is no dedicated per-channel usage-count table (requests / tokens / cost). Operators cannot see "how much traffic is going through channel X" without a full table scan on `usage_records`.

8. **F-OBS-23 (PARTIAL→full) — Real-time token throughput metric**
   Token/sec is derivable post-facto but not surfaced as a live metric. Useful for capacity planning and detecting anomalous bursts in streaming workloads.
