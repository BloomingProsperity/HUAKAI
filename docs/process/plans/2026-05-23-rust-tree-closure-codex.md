# 2026-05-23 Rust Data Plane Tree Closure - Codex Draft

| Field | Value |
|---|---|
| Owner directive | "HUAKAI 项目停止横向扩展，只在已有模块内做树向闭环；参考项目（sub2api / new-api / one-api / CLIProxyAPI）只作检查清单识别未闭环叶子节点，不能新增 HUAKAI 没有的整条功能线。" |
| Scope | Only `exploratory/rust-core-gateway/merged/crates/core_gateway/` as Rust data plane. `exploratory/rust-core-gateway/merged/proto/route.proto` is cited only where existing Rust contracts depend on it. |
| Repo metadata | `HUAKAI-code@8d5d16dd48aef546e5ecccbf30272ca37969c425`, observed on 2026-05-23 Asia/Shanghai. In-prose citations below are repo-root relative `path:line`. |
| Lane | HUAKAI internal L0 source analysis. No reference project source was read for this draft. |
| Independence note | This draft does not use any Claude parallel plan as input. During file discovery, a broad `rg` command returned a few lines from `docs/process/plans/2026-05-23-rust-tree-closure-claude.md`; those lines are excluded from this plan's evidence base and synthesis must treat this note as a contamination caveat, not as a source. |
| Success criteria | Every existing Rust module has a tree-status entry; every closure leaf has a real source citation; every recommended test states the discriminating fixture and mutation that must turn it red. |

## §0 禁止扩展清单

This Rust pass must not add any whole new product line. Allowed work is only closing leaves already implied by the current data-plane modules and the existing W11/W12/fingerprint hardening plan.

- No business/account-hub feature in Rust: no user registration, login UI, SaaS tenant onboarding, voucher, referral, payment, billing ledger, admin workflow, or commercial dashboard. Current Rust scope is request entry, route query, proxying, stream observation, attempt report, heartbeat, resource limits, and mimicry modules (`lib.rs:4-24`, `lib.rs:56-62`).
- No frontend or admin UI. The Rust crate has no frontend surface; its public business endpoints are only `/v1/messages` and `/v1/chat/completions` (`listener.rs:30-34`).
- No new external API protocol surface. Do not add Realtime, MCP, A2A, rerank, image/audio, new native vendor routes, or websocket surfaces. Existing protocol handling is Anthropic Messages and OpenAI Chat Completions in the listener, with stream parsing limited to `StreamProtocol::{Anthropic, OpenAi}` (`listener.rs:36-48`, `stream_pipeline/mod.rs:15-30`).
- No new vendor family. Existing profile/vendor names are OpenAI, Kiro, Gemini, and Anthropic for fingerprint templates; do not add more vendor profiles in this pass (`mimicry/profile.rs:35-49`, `mimicry/profile.rs:70-85`).
- No L4 pacing, L5 outbound IP pool, or L6 active anti-detection. This pass may close existing L1 TLS / L2 HTTP/2 mimicry leaves only; the HTTP/2 adapter explicitly says it is feature-gated and not wired into `ProxyEngine` (`mimicry/http2_adapter.rs:1-4`).
- No DB schema, migration, quota enforcement, billing ledger, auth-core, or payment logic. `route.proto` may be discussed only as the existing Rust data-plane contract (`route.proto:28-38`, `route.proto:77-98`, `route.proto:120-129`).
- No reference implementation import. sub2api / new-api / one-api / CLIProxyAPI may only identify missing closure leaves; this plan reads only HUAKAI source.

## §1 现状盘点

| Module group | Existing Rust modules | Current role |
|---|---|---|
| Crate shell | `account_planner`, `attempt_reporter`, `config`, `heartbeat`, `listener`, `metrics`, `mimicry`, `mock_control_plane`, `proxy_engine`, `redaction`, `request_id`, `route_client`, `route_proto`, `server_runtime`, `stream_pipeline`, plus private `body_timeout`, `circuit_breaker`, `drain`, `resource_limits` (`lib.rs:4-24`) | Data-plane entry, routing contract, proxy, observability, fingerprint, test harness. |
| Router/state | `GatewayState` owns config, planner, proxy engine, reporter, resource limits (`lib.rs:56-62`); `build_router` wires `/healthz`, `/metrics`, and business routes with body limit, idle timeout, overload, and drain middleware (`lib.rs:181-208`) | Shared runtime state and middleware order. |
| Config/runtime | `StartupConfig` covers listener, control-plane endpoint, transport baseline, mTLS files, runtime mode, timeouts, in-flight limits, connection limits, and mock upstream (`config.rs:56-114`) | Typed environment config with startup validation. |
| Listener | `/v1/messages` and `/v1/chat/completions` route to `handle_gateway_request` (`listener.rs:30-50`) | Reads request, plans route, forwards to upstream, maps errors. |
| Planner/proto | `AccountPlanner::plan` builds `RouteQueryRequest`; current query fields include tenant/model/session/protocol/stream/deadline/attempts/hints (`account_planner.rs:188-222`, `route.proto:28-38`) | Data-plane to control-plane route query. |
| Route client/circuit | Transport baseline supports UDS, HTTP, mTLS (`route_client.rs:42-50`); query route retries through a circuit breaker (`route_client.rs:271-318`); UDS security validates socket type, mode, uid/gid on Unix (`route_client.rs:593-630`) | Control-plane RPC transport and route-query resilience. |
| Proxy engine | `ProxyEngine` normalizes headers, applies plan auth, sends upstream request, wraps response body with relay terminal reporting (`proxy_engine/mod.rs:214-335`, `proxy_engine/headers.rs:20-42`) | Core reverse proxy path. |
| Attempt reporter | Bounded mpsc queue, async worker, retry loop, typed report/context/stats, idempotency key (`attempt_reporter/mod.rs:27-159`, `attempt_reporter/mod.rs:195-268`, `attempt_reporter/types.rs:144-225`) | Terminal attempt delivery to control plane. |
| Stream pipeline | SSE scanner, OpenAI usage extraction, Anthropic usage/cache/error parsing (`stream_pipeline/sse.rs:34-115`, `stream_pipeline/openai.rs:31-70`, `stream_pipeline/anthropic.rs:31-94`) | Observes streaming bytes without changing relayed body (`stream_pipeline/mod.rs:1-3`). |
| Heartbeat/drain | Heartbeat sends drain/health payload and updates global drain flag (`heartbeat.rs:72-89`); drain guard rejects business routes before handler (`drain.rs:17-31`) | Control-plane liveness and graceful drain. |
| Resource/metrics | In-flight request guard updates `huakai_rust_inflight_requests` (`resource_limits.rs:75-105`); connection limiter wraps listener IO (`resource_limits.rs:237-283`); Prometheus registers active/queue/upstream/in-flight gauges (`metrics.rs:37-47`, `metrics.rs:81-115`) | Local resource protection and scrape surface. |
| Mimicry | Feature flags exist for OpenSSL, Boring, HTTP/2 fork (`Cargo.toml:13-17`); resolver/dispatch pick a backend (`mimicry/backend_resolver.rs:45-92`, `mimicry/dispatch.rs:41-107`); profile intent marks known gaps and unsupported templates (`mimicry/profile.rs:181-208`) | Existing fingerprint/mimicry workstream. |

## §2 逐模块状态树

### 2.1 Config / Router / Startup Boundary

- 已有功能: typed env config, runtime mode, production default, mock-upstream rejection in production validation, middleware order, health/metrics split (`config.rs:26-51`, `config.rs:222-232`, `lib.rs:181-208`).
- 已闭环路径: `runtime_mode=production` plus `mock_upstream_endpoint` now startup-fails, so the obvious production bypass is already blocked at config validation (`config.rs:222-232`). Drain guard wraps business routes outside overload/body-limit, so drain returns 503 before request body work (`lib.rs:188-203`, `drain.rs:17-31`).
- 未闭环叶子节点:
  - `HUAKAI_RUNTIME_MODE` has only production/development/test. If a future canary mode is added, mock-upstream must remain forbidden unless explicitly classified non-billable (`config.rs:36-50`, `config.rs:222-232`).
  - Existing listener still bypasses route planning whenever mock upstream is configured; this is fine for dev/test, but tests must keep proving it cannot happen in production config (`listener.rs:72-80`, `config.rs:222-232`).
- 必须补齐:
  - P2 add a config test fixture that treats any non-development/non-test runtime as production-equivalent for mock-upstream rejection; do not add a canary mode in this pass (`RuntimeMode::parse` is the only mode parser, `config.rs:36-46`).
- 暂不补:
  - Do not add runtime-mode policy service, admin toggle, or database-backed deployment mode. That would be product/config expansion outside Rust data-plane closure.
- 风险:
  - A future mode string could accidentally reopen mock bypass if tests assert only exact `"production"` (`config.rs:36-46`, `config.rs:222-232`).
- 测试用例（discriminating + mutation）:
  - Fixture: env with `HUAKAI_RUNTIME_MODE=production` and `HUAKAI_MOCK_UPSTREAM_ENDPOINT=http://127.0.0.1:9` must fail config parse. Mutation: remove the `runtime_mode.is_production()` block and the same env becomes valid, so the test turns red (`config.rs:222-232`).

### 2.2 Listener / AccountPlanner / Route Query Identity

- 已有功能: listener extracts request id, checks content length, supports mock-upstream dev path, asks planner for route, then forwards planned attempt (`listener.rs:55-86`, `listener.rs:121-134`). Planner builds route query from headers (`account_planner.rs:201-222`).
- 已闭环路径: control-plane unavailable and invalid route plan fail closed and synthesize attempt reports (`listener.rs:89-118`, `listener.rs:150-166`). Route plan validation rejects missing account/acquisition/credential fields and malformed endpoint shape (`account_planner.rs:225-256`).
- 未闭环叶子节点:
  - `tenant_id`, `requested_model`, `session_hash`, and `stream` are currently trusted from client-controllable headers with defaults (`account_planner.rs:206-219`). This is not a closed identity/model boundary.
  - `RouteQueryRequest` has no client credential field, so Rust cannot ask the control plane to derive tenant from an authenticated credential today (`route.proto:28-38`).
  - `model` and `stream` already exist in request bodies for both supported APIs, but the listener does not parse body before planning (`listener.rs:83-86`, `account_planner.rs:201-222`).
- 必须补齐:
  - P0 D-1a: parse bounded JSON request body once before route query; derive `requested_model` from body `model`; derive `stream` only from body boolean. Header `x-huakai-model` and `x-huakai-stream` must not be authoritative (`listener.rs:64-70`, `account_planner.rs:201-222`).
  - P1 D-1b boundary: do not build an `APIKeyResolver` or user system in Rust. Rust should either pass an already-authenticated credential contract to the existing route service once approved, or fail closed for production-billable direct-client traffic until the control plane consumes that credential. Current absence is proven by `RouteQueryRequest` fields (`route.proto:28-38`).
- 暂不补:
  - No Rust-side users table, key database, login, RBAC, tenant CRUD, or local billing quota. Those are banned by §0 and absent from current module ownership.
- 风险:
  - If D-1a is skipped, a client can choose billing/routing model via headers while body says a different model (`account_planner.rs:206-219`).
  - If D-1b is implemented as local Rust static key map for real traffic, it creates a second identity authority outside the control plane (`route.proto:28-38`).
- 测试用例（discriminating + mutation）:
  - D-1a body wins: send body `{"model":"claude-real","stream":true}` with headers `x-huakai-model: cheap-model`, `x-huakai-stream: false`; assert mock control plane sees `requested_model=claude-real`, `stream=true`. Mutation: keep `build_route_query(headers, ...)` unchanged and the control plane sees header/default values, so the test turns red (`listener.rs:83-86`, `account_planner.rs:206-219`, `mock_control_plane.rs:168-181`).
  - D-1b no silent tenant default: direct production-bound request without authenticated credential must not emit `tenant_id=default-tenant`. Mutation: allow `unwrap_or("default-tenant")`; mock control plane observes a route query and the test turns red (`account_planner.rs:208-210`, `route.proto:28-38`).

### 2.3 RouteClient / Circuit / Control-Plane Transport

- 已有功能: UDS default, HTTP loopback-only baseline in config, optional mTLS material, route-query retry, circuit breaker with half-open single probe (`route_client.rs:37-50`, `config.rs:261-272`, `route_client.rs:271-318`, `circuit_breaker.rs:96-170`).
- 已闭环路径: HTTP control-plane baseline rejects non-loopback endpoints (`config.rs:264-272`, `config.rs:387-407`). UDS validates socket file type, permissions, and owner on Unix (`route_client.rs:593-630`).
- 未闭环叶子节点:
  - `report_attempt` only checks whether the route circuit is already open; it does not feed attempt-report failures back into its own durable health model. This is acceptable only if D-4 durable spool becomes the report reliability mechanism (`route_client.rs:318-340`, `attempt_reporter/mod.rs:207-268`).
  - mTLS is feature-gated; when feature `tls` is absent, `configure_mtls_endpoint` returns the endpoint unchanged after material parsing, so CI must include a feature/build check if mTLS is claimed (`route_client.rs:526-532`).
- 必须补齐:
  - P2 make verification matrix explicit: default, `tls`, `mimicry-boring`, and `mimicry-http2-fork` where platform supports them. This is test infrastructure closure, not feature expansion (`Cargo.toml:13-17`, `route_client.rs:483-532`).
- 暂不补:
  - Do not add new control-plane RPCs or retry algorithms beyond current query/report/health/heartbeat service (`route.proto:5-10`).
- 风险:
  - Without feature-matrix CI, a code path may compile only in default features while mTLS or mimicry feature builds rot (`Cargo.toml:13-17`).
- 测试用例（discriminating + mutation）:
  - Build gate: `cargo test -p core_gateway --features tls` must fail if mTLS code is broken and pass when configured correctly. Mutation: break `client_tls_config_from_material`; default tests still pass, feature test turns red (`route_client.rs:483-547`).

### 2.4 ProxyEngine / Header Boundary / Relay Terminal

- 已有功能: proxy builds upstream URI, copies allowed headers, injects plan bearer auth, sets request id, forwards through shared HTTP client, classifies HTTP status, and wraps body relay (`proxy_engine/mod.rs:275-335`, `proxy_engine/headers.rs:20-42`, `proxy_engine/auth.rs:7-48`).
- 已闭环路径: client `Authorization` is not in the request header allowlist, and plan auth overwrites upstream bearer from route plan (`proxy_engine/headers.rs:45-58`, `proxy_engine/auth.rs:21-48`). Route plan auth rejects an upstream bearer equal to the acquisition token (`proxy_engine/auth.rs:33-43`).
- 未闭环叶子节点:
  - D-3: vendor endpoint currently only requires scheme and authority; there is no HTTPS-only policy, host allowlist, private-IP block, or DNS rebinding guard (`account_planner.rs:244-253`, `proxy_engine/headers.rs:81-107`).
  - D-6: client `openai-organization` and `openai-project` headers are forwarded as-is because they are in the allowlist; there is no route-plan-owned injection point for those values (`proxy_engine/mod.rs:55-56`, `proxy_engine/headers.rs:45-58`).
  - D-8: status classification only maps any 4xx to `Upstream4xx` and any 5xx to `Upstream5xx`; no special 429/408/reset handling exists (`proxy_engine/mod.rs:370-378`, `attempt_reporter/types.rs:61-79`).
  - D-9: `bytes_in` starts from `Content-Length` only; chunked uploads and actual consumed request bytes are not counted (`proxy_engine/mod.rs:222-229`, `proxy_engine/mod.rs:380-386`).
- 必须补齐:
  - P0 D-3 endpoint guard in the planned forward path: reject non-HTTPS vendor endpoints by default, block loopback/private/reserved resolved IPs for production, and add test-only explicit opt-in if needed. Do not add vendor catalog CRUD (`account_planner.rs:244-253`).
  - P0 D-6 strip client org/project headers by default; preserve only route-plan-injected org/project once the existing plan contract carries them. Until then, stripping is safer than forwarding client-supplied account selectors (`proxy_engine/headers.rs:45-58`).
  - P1 D-8 typed retry class: at minimum 429 and 408 must be distinguishable from generic non-retryable 4xx; do not implement whole provider policy engine in Rust (`proxy_engine/mod.rs:370-378`, `attempt_reporter/types.rs:61-79`).
  - P1 D-9 count actual inbound bytes with a body wrapper, not only `Content-Length` (`proxy_engine/mod.rs:222-229`, `proxy_engine/mod.rs:380-386`).
- 暂不补:
  - No L5 proxy pool, no residential/mobile outbound routing, no per-vendor admin policy table, no DB-backed host allowlist.
- 风险:
  - Without D-3, a compromised control-plane route plan can send traffic to plaintext or local metadata targets (`account_planner.rs:244-253`).
  - Without D-6, end users can smuggle provider account selectors to upstream (`proxy_engine/headers.rs:45-58`).
- 测试用例（discriminating + mutation）:
  - D-3 forwarding guard: mock control plane returns `http://example.com`; request must fail before upstream dial and attempt report should show bad route/security class. Mutation: only parse URI as today; request proceeds to `forward_inner`, so test turns red (`account_planner.rs:244-253`, `proxy_engine/mod.rs:301-316`).
  - D-6 strip: request includes client `openai-project: attacker`; mock upstream must not receive it, while existing safe headers like `content-type` still arrive. Mutation: leave `OPENAI_PROJECT` in `should_forward_request_header`; upstream sees attacker value and test turns red (`proxy_engine/headers.rs:45-58`).
  - D-8 classify: upstream returns 429; attempt report must be retryable/rate-limited, while 401 remains non-retryable. Mutation: keep `status.is_client_error()` as one branch and 429 becomes generic `upstream_4xx`, so test turns red (`proxy_engine/mod.rs:370-378`, `attempt_reporter/types.rs:61-79`).

### 2.5 AttemptReporter / Accounting Delivery

- 已有功能: terminal reporter is single-submit, builds idempotency key, enqueues bounded report, retries transient control-plane failures, and exposes queue counters (`attempt_reporter/mod.rs:81-105`, `attempt_reporter/mod.rs:140-192`, `attempt_reporter/types.rs:151-163`).
- 已闭环路径: report payload includes idempotency key and token/cache/byte/frame fields, and `into_proto` maps them to `AttemptReportRequest` (`attempt_reporter/types.rs:198-225`, `attempt_reporter/types.rs:283-317`, `route.proto:77-98`).
- 未闭环叶子节点:
  - D-4 pre-queue overflow drops reports with `DroppedFull`; retry exhaustion increments `failed_reports` and returns; both are lossy for billable success reports (`attempt_reporter/mod.rs:140-159`, `attempt_reporter/mod.rs:207-268`).
  - Callers ignore terminal report results with `let _ =`, including listener planning errors, client-cancel drop path, and relay terminal report (`listener.rs:158-166`, `proxy_engine/mod.rs:111-119`, `proxy_engine/relay.rs:373-389`).
  - No local durable spool, replay worker, or post-commit loud failure metric exists (`attempt_reporter/mod.rs:54-63`, `metrics.rs:37-47`).
- 必须补齐:
  - P0 D-4 local durable spool inside `attempt_reporter`: when bounded queue is full or worker retries exhaust, append report to local spool with existing `idempotency_key`, replay until ack, and expose spool depth/drop metrics. No DB schema, no billing ledger writes in Rust (`attempt_reporter/mod.rs:140-159`, `attempt_reporter/types.rs:151-163`, `route.proto:97`).
  - P0 D-4 pre-commit gate: before upstream forward, reserve spool/report capacity; if unavailable, return 503 before upstream is called. After response headers are committed, do not try to change HTTP status; emit loud metric/log if final reporting still fails (`proxy_engine/mod.rs:214-242`, `proxy_engine/relay.rs:50-77`, `proxy_engine/relay.rs:373-389`).
- 暂不补:
  - No Rust-side billing settlement, no durable DB outbox, no money ledger, no cross-node queue. Rust only guarantees delivery/replay of attempt reports to the control plane.
- 风险:
  - Durable spool introduces local disk IO and replay ordering risk.
  - Post-commit failure cannot be converted into client 5xx because `upstream_response_to_client` has already returned a streaming response (`proxy_engine/mod.rs:327-335`, `proxy_engine/relay.rs:50-77`).
- 测试用例（discriminating + mutation）:
  - D-4 overflow self-proof: with queue capacity 1 and blocked worker, submit a billable success report. Run enabled path vs baseline-no-spool path in one test; enabled path must replay/ack, baseline must drop. Mutation: remove spool append on `TrySendError::Full`; enabled path matches baseline and test turns red (`attempt_reporter/mod.rs:140-159`, `attempt_reporter/mod.rs:195-268`).
  - D-4 post-commit loud path: make downstream receive 200 headers/body, then force report spool cap failure; assert HTTP remains 200 and `spool_drop_billable` metric/log increments. Mutation: remove metric/log path; HTTP assertion still passes but metric assertion turns red (`proxy_engine/relay.rs:50-77`, `proxy_engine/relay.rs:373-389`, `metrics.rs:37-47`).

### 2.6 StreamPipeline / Non-Streaming Usage / SSE Semantics

- 已有功能: SSE parser handles frame boundaries and max-frame errors; OpenAI stream parser extracts `usage`; Anthropic stream parser extracts usage/cache and upstream errors (`stream_pipeline/sse.rs:34-115`, `stream_pipeline/openai.rs:31-70`, `stream_pipeline/anthropic.rs:69-94`).
- 已闭环路径: stream relay records body chunks, stream events, DONE/message_stop, protocol errors, client cancel, and body idle timeout into terminal reports (`proxy_engine/relay.rs:119-168`, `proxy_engine/relay.rs:246-264`, `proxy_engine/relay.rs:325-369`).
- 未闭环叶子节点:
  - D-5: non-SSE 2xx responses are relayed without parsing JSON usage; `extract_usage_from_json_bytes` exists but is not called by the non-streaming relay path (`stream_pipeline/openai.rs:63-70`, `proxy_engine/relay.rs:64-65`, `proxy_engine/relay.rs:155-168`).
  - Missing/non-numeric/bad-JSON usage currently falls back to `AttemptTokenMetrics::missing()` via report defaulting, which loses whether extraction was attempted (`attempt_reporter/types.rs:208-213`, `attempt_reporter/metrics.rs:12-26`).
- 必须补齐:
  - P0/P1 D-5 parse bounded non-SSE 2xx JSON response bodies for OpenAI and Anthropic usage; set `source="response_body"` on success and `source="pending_reconciliation"` when checked but unusable. Reuse existing report `tokens_used.source`; no proto change needed (`route.proto:63-68`, `attempt_reporter/types.rs:295-300`).
- 暂不补:
  - Do not add tokenizer-based local estimation, provider-specific pricing, or reconciliation queue in Rust. The Rust leaf is to mark the attempt accurately for control-plane reconciliation.
- 风险:
  - Reading non-streaming body to parse usage must preserve exact response bytes and respect max body limits; otherwise the proxy can change client-visible behavior (`proxy_engine/relay.rs:50-77`).
- 测试用例（discriminating + mutation）:
  - OpenAI non-stream happy: upstream 200 non-SSE body contains `usage:{prompt_tokens:100,completion_tokens:50}`; attempt report must contain 100/50 and `source=response_body`. Mutation: skip non-stream parser; report source remains `missing`, so test turns red (`stream_pipeline/openai.rs:63-70`, `attempt_reporter/metrics.rs:12-26`).
  - Bad JSON self-proof: same 200 path with body containing malformed JSON near `usage`; report must be `source=pending_reconciliation`, not `missing` and not zero-success. Mutation: default to `AttemptTokenMetrics::missing`; test turns red (`attempt_reporter/types.rs:208-213`, `attempt_reporter/metrics.rs:12-26`).

### 2.7 Heartbeat / ResourceLimits / Metrics

- 已有功能: heartbeat sends schema version and drain fields; resource limits maintain in-flight request gauge; metrics registers active connections, queue depth, open upstream connections, and in-flight gauges (`heartbeat.rs:72-83`, `resource_limits.rs:96-104`, `metrics.rs:37-47`).
- 已闭环路径: in-flight request count is updated on request begin/drop and exposed through metrics (`resource_limits.rs:75-105`, `metrics.rs:107-115`). Drain ack updates global drain mode (`heartbeat.rs:85-99`).
- 未闭环叶子节点:
  - D-7: heartbeat sends zeros for `in_flight_requests`, `open_upstream_connections`, `attempt_report_queue_depth`, p95 RPC, and error rate instead of real values (`heartbeat.rs:72-83`, `route.proto:120-129`).
  - O-2: `ACTIVE_CONNECTIONS`, `QUEUE_DEPTH`, and `OPEN_UPSTREAM_CONNECTIONS` are registered but not wired to lifecycle outside tests; `rg` found setters only for in-flight (`metrics.rs:37-47`, `metrics.rs:81-115`, `resource_limits.rs:237-283`).
  - Connection limiter tracks permits but does not increment/decrement `active_connections` (`resource_limits.rs:237-283`, `metrics.rs:81-84`).
- 必须补齐:
  - P1 D-7 pass real in-flight and attempt-report queue depth into heartbeat from `GatewayState` / `AttemptReporter`; leave p95/error_rate documented as not sourced until implemented (`heartbeat.rs:72-83`, `attempt_reporter/mod.rs:166-192`).
  - P1 O-2 wire accepted connection lifetime to `active_connections` and upstream request lifetime to `open_upstream_connections`, with scrapeable gauges (`resource_limits.rs:251-283`, `metrics.rs:81-115`).
- 暂不补:
  - Do not build dashboard, alerting backend, SLO service, or metrics database.
- 风险:
  - Zero heartbeat fields can make control-plane drain/health decisions trust false capacity (`heartbeat.rs:72-83`).
  - Gauge tests that set metric values directly do not prove lifecycle wiring (`metrics.rs:282-286`).
- 测试用例（discriminating + mutation）:
  - Heartbeat queue/in-flight: create one in-flight streaming request and one queued report; mock control plane heartbeat must see non-zero fields. Mutation: keep hardcoded zeros; mock last heartbeat remains zero and test turns red (`heartbeat.rs:72-83`, `mock_control_plane.rs:176-181`).
  - Active connection gauge: open N real TCP connections through the limited listener; `/metrics` must show gauge N then 0 after close. Mutation: omit lifecycle inc/dec; gauge stays 0 and test turns red (`resource_limits.rs:237-283`, `metrics.rs:81-84`).

### 2.8 Mimicry / Fingerprint Closure

- 已有功能: built-in profiles load from templates; match policy and backend intent exist; backend resolver selects Boring/OpenSSL/KnownGap; dispatch builds Boring HTTP client when feature is enabled; HTTP/2 fork adapter can encode/capture settings and pseudo-header order under feature flag (`mimicry/profile.rs:18-68`, `mimicry/profile.rs:163-208`, `mimicry/backend_resolver.rs:45-92`, `mimicry/dispatch.rs:98-120`, `mimicry/http2_adapter.rs:65-201`).
- 已闭环路径: exact OpenSSL adapter performs profile preflight before marking `preflight_passed` (`mimicry/openssl_adapter.rs:129-148`, `mimicry/openssl_adapter.rs:201-210`). HTTP/2 adapter validates required profile fields and settings/value consistency (`mimicry/http2_adapter.rs:65-125`).
- 未闭环叶子节点:
  - D-10: `resolve_vendor_mimicry_backend` returns `Boring` immediately when feature `mimicry-boring` is available, before consulting `backend_intent`; this can bypass KnownGap/unsupported blocking (`mimicry/backend_resolver.rs:72-92`, `mimicry/profile.rs:181-208`).
  - L1 canary: production dispatch can be allowed by resolver/dispatch without a single canary rule that says "profile capture not proven equals block" (`mimicry/dispatch.rs:41-107`, `mimicry/profile.rs:163-179`).
  - L2 HTTP/2: adapter says it is not wired into `ProxyEngine`, so any production claim beyond local capture is not closed (`mimicry/http2_adapter.rs:1-4`, `proxy_engine/mod.rs:19-26`).
- 必须补齐:
  - P0 D-10 reorder resolver: always compute `backend_intent()` first, then check feature availability; Boring feature must not override KnownGap/UnsupportedTemplate (`mimicry/backend_resolver.rs:72-92`, `mimicry/profile.rs:181-208`).
  - P1 L1 canary gate: dispatch must fail closed when a profile lacks proven local capture status. This is existing mimicry closure, not L4/L5/L6 anti-detection expansion (`mimicry/dispatch.rs:41-107`).
  - P1 L2 local-only: keep HTTP/2 fork as capture/adapter tests until it is explicitly wired; production dispatch must document "not wired" and block L2-only profiles (`mimicry/http2_adapter.rs:1-4`).
- 暂不补:
  - No new profiles, no vendor profile mining, no request pacing, no outbound IP pool, no active challenge detection.
- 风险:
  - A cargo feature must not silently turn a known-bad profile into an allowed production path (`mimicry/backend_resolver.rs:72-92`).
- 测试用例（discriminating + mutation）:
  - D-10 feature-on gap: with `AvailableMimicryFeatures { boring:true, openssl:true }` and Kiro/Codex known-gap profile, resolver must return block/error, not `Boring`. Mutation: restore early `if available_features.boring { return Ok(Boring) }`; test turns red (`mimicry/backend_resolver.rs:72-92`, `mimicry/profile.rs:181-208`).
  - L2 not wired: feature `mimicry-http2-fork` can encode/capture bytes locally, but `ProxyEngine` construction must not claim to use it. Mutation: falsely allow dispatch on HTTP/2 adapter existence; production-gate test turns red (`mimicry/http2_adapter.rs:1-4`, `proxy_engine/mod.rs:19-26`).

### 2.9 Redaction / RequestId / Error / Mock Control Plane

- 已有功能: request id parsing/generation, redaction helpers, public error mapping, and mock control plane with last request/report/heartbeat capture (`request_id.rs:15-73`, `redaction.rs:12-82`, `proxy_engine/error.rs:21-41`, `mock_control_plane.rs:168-181`).
- 已闭环路径: route-plan and control-plane error messages are redacted before public/log/report use (`listener.rs:89-118`, `attempt_reporter/mod.rs:295-297`, `redaction.rs:69-82`).
- 未闭环叶子节点:
  - Mock control plane lacks fault knobs for durable spool disk-full, replay duplicate ack, and heartbeat real-load assertions. Current knobs cover RPC behavior/delay and last message capture only (`mock_control_plane.rs:51-66`, `mock_control_plane.rs:168-181`).
- 必须补齐:
  - P1/P2 extend test helpers only, not production code: add deterministic fault injection for D-4/D-7/O-2 tests. This remains test infrastructure under existing mock module ownership (`mock_control_plane.rs:30-66`, `mock_control_plane.rs:168-181`).
- 暂不补:
  - No fake billing ledger or fake user service in Rust tests.
- 风险:
  - Without fault knobs, tests can become "can run" smoke tests instead of proving the bug would be caught.
- 测试用例（discriminating + mutation）:
  - Duplicate replay: mock accepts same `idempotency_key` twice but records one logical effect; mutation: count every replay as billable, test turns red. The idempotency key is already part of report proto (`attempt_reporter/types.rs:151-163`, `route.proto:97`).

## §3 跨模块测试基础设施缺口

- Need an end-to-end report harness that captures route query, upstream headers/body, streamed response, and final attempt report in one scenario. Existing tests have pieces (`proxy_engine_test.rs:150-194`, `attempt_reporter_test.rs:111-156`, `mock_control_plane.rs:168-181`) but D-1/D-4/D-5 need one integrated harness.
- Need a baseline-vs-guard self-proof helper for weak-test prevention. Example: D-5 should run parser-enabled and parser-disabled/baseline paths and assert different `tokens_used.source`; current stream tests prove parser units only, not relay/report integration (`stream_pipeline_test.rs:151-170`, `proxy_engine/relay.rs:155-168`).
- Need deterministic report-backpressure/failure injection. Current `AttemptReporterOptions` can shrink queue and retry attempts, and mock control plane can delay/fail attempt reports (`attempt_reporter/mod.rs:32-47`, `mock_control_plane.rs:51-66`), but no local spool/disk fault hook exists yet.
- Need real connection/heartbeat probes. Metrics are globally registered and some tests set gauges directly; lifecycle tests must drive TCP connections and heartbeat capture instead of setting gauges by hand (`metrics.rs:282-286`, `resource_limits.rs:237-283`, `heartbeat.rs:72-83`).
- Need feature-matrix CI for default, `tls`, `mimicry-boring`, and `mimicry-http2-fork`; the existing synthesis plan already calls out feature tests for mimicry (`docs/process/plans/2026-05-22-rust-hardening-plan.md:587-603`).
- Need mutation notes per test in comments or test names. Every suggested test above has a named mutation; implementation must not accept tests that only assert "response is not bad" without asserting the expected good value.

## §4 与 W11 + W12 + 指纹 Synthesis Plan 的关系

| Existing hardening item | Tree-closure verdict | Codex status |
|---|---|---|
| W11 order before W12/fingerprint | Tree-closed sequencing: security boundary first, then accounting/telemetry, then fingerprint. Existing plan says W11 then W12 then fingerprint and gives commit grouping (`docs/process/plans/2026-05-22-rust-hardening-plan.md:454-472`). | Keep. |
| D-1a model/stream body authority | Tree closure inside listener/planner; no new feature line. Existing plan identifies header-derived route query as D-1 (`docs/process/plans/2026-05-22-rust-hardening-plan.md:82-92`). | P0. |
| D-1b client identity | Partially tree closure, partially cross-line contract. Rust must not build user/auth system; use existing route contract evolution only if Owner-approved, and block billable production until control plane consumes it (`route.proto:28-38`, `docs/process/plans/2026-05-22-rust-hardening-plan.md:104-133`, `docs/process/plans/2026-05-22-rust-hardening-plan.md:442-454`). | P1 with explicit no-expansion guard. |
| D-2 mock upstream | Mostly closed in current config; keep regression tests and no canary loophole. Existing plan flags it as W11 (`docs/process/plans/2026-05-22-rust-hardening-plan.md:133-147`); current code has production rejection (`config.rs:222-232`). | P2 guard. |
| D-3 endpoint safety | Pure tree closure in planner/proxy boundary; does not add vendors. Existing plan calls HTTPS/allowlist/private-IP checks (`docs/process/plans/2026-05-22-rust-hardening-plan.md:147-161`). | P0. |
| D-6 org/project header | Pure header-boundary closure. Existing plan demands stripping client org/project and preserving route-plan injection (`docs/process/plans/2026-05-22-rust-hardening-plan.md:172-186`). | P0. |
| D-10 mimicry feature bypass | Pure existing mimicry tree closure; no new anti-detection layer. Existing plan names the early Boring bypass (`docs/process/plans/2026-05-22-rust-hardening-plan.md:195-205`). | P0. |
| D-4 durable spool | Existing attempt-report leaf; large but not horizontal if confined to `attempt_reporter` and proxy pre-commit gate. Existing plan defines pre/post commit split and ACs (`docs/process/plans/2026-05-22-rust-hardening-plan.md:213-252`). | P0, highest implementation risk. |
| D-5 non-stream usage | Existing stream/report leaf; no proto change because `TokensUsed.source` exists (`route.proto:63-68`). Existing plan says non-stream response body usage parsing and `pending_reconciliation` (`docs/process/plans/2026-05-22-rust-hardening-plan.md:261-286`). | P0/P1. |
| D-7 heartbeat truth | Existing heartbeat/resource/metrics leaf. Existing plan's test gate says heartbeat must expose real in-flight/queue depth (`docs/process/plans/2026-05-22-rust-hardening-plan.md:575-576`). | P1. |
| D-8 retry classification | Existing proxy/report classification leaf. Existing plan's gates distinguish 429/408/401/403 and reset field (`docs/process/plans/2026-05-22-rust-hardening-plan.md:578-581`). | P1. |
| D-9 + O-2 bytes/gauges | Existing resource/metrics leaf. Existing plan says bytes and `ACTIVE_CONNECTIONS` lifecycle should be real (`docs/process/plans/2026-05-22-rust-hardening-plan.md:583-586`). | P1. |
| Fingerprint L1/L2 | Existing mimicry leaf only. Existing plan says L1/L2 canary gating and feature tests (`docs/process/plans/2026-05-22-rust-hardening-plan.md:562-566`, `docs/process/plans/2026-05-22-rust-hardening-plan.md:587-590`). | P1; L4/L5/L6 prohibited. |
| W12-F CI verification | Cross-module test infra, not product feature. Existing plan lists WSL2 verification commands (`docs/process/plans/2026-05-22-rust-hardening-plan.md:587-603`). | P2/P1 depending on CI urgency. |

## §5 最终清单

### P0

- D-1a: body-derived model/stream route query; no direct client header authority (`listener.rs:83-86`, `account_planner.rs:206-219`).
- D-3: planned upstream endpoint guard before forwarding (`account_planner.rs:244-253`, `proxy_engine/mod.rs:301-316`).
- D-6: strip client org/project headers; route-plan-owned injection only when contract exists (`proxy_engine/headers.rs:45-58`).
- D-10: mimicry resolver must honor profile intent before feature availability (`mimicry/backend_resolver.rs:72-92`, `mimicry/profile.rs:181-208`).
- D-4: durable local attempt-report spool, replay, pre-commit reservation gate, and post-commit loud failure metric (`attempt_reporter/mod.rs:140-159`, `attempt_reporter/mod.rs:207-268`, `proxy_engine/relay.rs:373-389`).
- D-5 minimum: non-stream 2xx usage extraction with `response_body` / `pending_reconciliation` source vocabulary (`stream_pipeline/openai.rs:63-70`, `route.proto:63-68`, `attempt_reporter/metrics.rs:12-26`).

### P1

- D-1b Rust-side identity contract only after control-plane contract is ready; no Rust user/auth system (`route.proto:28-38`).
- D-7 real heartbeat in-flight and queue depth (`heartbeat.rs:72-83`, `attempt_reporter/mod.rs:166-192`).
- D-8 429/408/401/403 retry classification and optional reset-time contract if already approved (`proxy_engine/mod.rs:370-378`, `attempt_reporter/types.rs:61-79`).
- D-9/O-2 actual bytes-in and connection/upstream gauges (`proxy_engine/mod.rs:380-386`, `resource_limits.rs:237-283`, `metrics.rs:81-115`).
- Fingerprint L1 canary fail-closed and L2 HTTP/2 local-only gate (`mimicry/dispatch.rs:41-107`, `mimicry/http2_adapter.rs:1-4`).
- Cross-module fault injection and feature-matrix tests (`Cargo.toml:13-17`, `mock_control_plane.rs:51-66`).

### P2

- Config canary/mock regression guard (`config.rs:36-50`, `config.rs:222-232`).
- mTLS feature build coverage (`route_client.rs:483-547`).
- Metrics helper cleanup so tests do not set lifecycle gauges directly (`metrics.rs:282-286`).
- Documentation-only notes for fields deliberately unsourced, such as heartbeat p95/error_rate until a real source exists (`heartbeat.rs:80-82`).

### 明确禁止

- No DB schema/migration, billing ledger, quota enforcement, payment, auth core, user system, frontend/admin UI, new vendor line, new external protocol, L4 pacing, L5 IP pool, L6 active anti-detection, or reference-source implementation import.

## §6 执行顺序

1. Synthesize with Claude parallel draft after both drafts exist; Owner approves one merged plan before implementation. This file is not an implementation go-ahead by itself.
2. P0 security leaves first: D-1a, D-3, D-6, D-10. These are smaller than D-4 and reduce blast radius before accounting work (`docs/process/plans/2026-05-22-rust-hardening-plan.md:454-468`).
3. P0 accounting delivery: D-4 durable spool in three slices: data structure/write path, replay/ack, pre-commit gate/post-commit loud failure (`docs/process/plans/2026-05-22-rust-hardening-plan.md:256-259`).
4. P0/P1 usage: D-5 non-stream usage extraction and `pending_reconciliation`.
5. P1 telemetry/retry: D-7 heartbeat truth, D-8 retry class, D-9/O-2 bytes and gauges.
6. P1 fingerprint: D-10 already done in step 2; then L1 canary gate and L2 local-only HTTP/2 adapter verification.
7. P2 verification: feature-matrix cargo tests and `codex exec review --uncommitted --full-auto` before any commit, per project rule.

## §7 风险

- Expansion drift: D-1b can tempt Rust-side auth/user implementation. Mitigation: Rust only enforces boundary and passes/blocks an existing control-plane credential contract; no local account system.
- Post-commit accounting failure: once streaming response headers are returned, Rust cannot change HTTP status. Mitigation: pre-commit reserve gate for fail-closed and loud post-commit metric/log for the unavoidable residual (`proxy_engine/mod.rs:327-335`, `proxy_engine/relay.rs:50-77`).
- Durable spool new state: local disk corruption/full/replay duplication can create reliability bugs. Mitigation: bounded spool, ack marker, idempotency-key replay tests, disk-full tests (`attempt_reporter/types.rs:151-163`, `route.proto:97`).
- Weak tests: fixtures that would pass under broken code are worse than no tests. Mitigation: every P0/P1 test above has a named mutation that must fail.
- Feature flag false confidence: mimicry feature builds can pass default CI while breaking gated code. Mitigation: default + feature-matrix verification (`Cargo.toml:13-17`).
- Clean-room risk: low for this draft because only HUAKAI L0 source was read. Future reference checks must use clean-room guard if they read non-MIT source.

## §8 Claude 平行稿位

This section is intentionally a placeholder for synthesis. Codex does not fill agreements/conflicts/gaps until the Claude draft is separately available for cross-discussion.

| Item | To be filled in synthesis |
|---|---|
| Claude draft path | `docs/process/plans/2026-05-23-rust-tree-closure-claude.md` |
| Agreements | TBD after Owner-authorized comparison |
| Conflicts | TBD after Owner-authorized comparison |
| Gaps Claude caught | TBD |
| Gaps Codex caught | TBD |
| Owner decisions needed | TBD |

Source files read for this draft: `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml`; `exploratory/rust-core-gateway/merged/proto/route.proto`; `exploratory/rust-core-gateway/merged/crates/core_gateway/src/lib.rs`; `src/config.rs`; `src/listener.rs`; `src/account_planner.rs`; `src/route_client.rs`; `src/circuit_breaker.rs`; `src/proxy_engine/mod.rs`; `src/proxy_engine/headers.rs`; `src/proxy_engine/auth.rs`; `src/proxy_engine/error.rs`; `src/proxy_engine/relay.rs`; `src/attempt_reporter/mod.rs`; `src/attempt_reporter/types.rs`; `src/attempt_reporter/metrics.rs`; `src/stream_pipeline/mod.rs`; `src/stream_pipeline/sse.rs`; `src/stream_pipeline/openai.rs`; `src/stream_pipeline/anthropic.rs`; `src/heartbeat.rs`; `src/drain.rs`; `src/resource_limits.rs`; `src/metrics.rs`; `src/body_timeout.rs`; `src/server_runtime.rs`; `src/mock_control_plane.rs`; `src/mimicry/backend_resolver.rs`; `src/mimicry/dispatch.rs`; `src/mimicry/profile.rs`; `src/mimicry/http2_adapter.rs`; `src/mimicry/openssl_adapter.rs`; selected existing tests under `tests/`; `docs/process/plans/2026-05-22-rust-hardening-plan.md`.
