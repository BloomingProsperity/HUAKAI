# 2026-05-22 Rust Core Gateway 深度审计

审计区域：`exploratory/rust-core-gateway/merged/`

约束记录：本轮只读调查 HUAKAI 内部代码，未读取 reference-project 源码，未运行 `git`，未修改业务代码。本文不重复上一轮已确认的 8 项问题：L2 cache key 缺 client protocol、buffered path 上游错误体泄漏、storm-controller 生产 panic、真实流量标成 EvidenceMock、SSE 错误头丢失、`err.Error()` 泄漏、Rust dead metric、RoutePlan cache disabled。

## Findings

### 1. HIGH - 路由规划使用可伪造 header，而不是请求体中的真实 model/stream

证据：

- `exploratory/rust-core-gateway/merged/src/listener.rs:83` 在进入代理前直接调用 `planner.plan(request.headers())`。
- `exploratory/rust-core-gateway/merged/src/account_planner.rs:201` `build_route_query` 只接收 `HeaderMap`。
- `exploratory/rust-core-gateway/merged/src/account_planner.rs:208` 从 `x-huakai-tenant` 读取 tenant，缺失时落到 `default-tenant`。
- `exploratory/rust-core-gateway/merged/src/account_planner.rs:213` 从 `x-huakai-model` 读取 model，缺失时落到 `unknown`。
- `exploratory/rust-core-gateway/merged/src/account_planner.rs:217` 从 `x-huakai-stream` 读取 stream，而不是解析 OpenAI/Anthropic 请求体。

具体失败场景：客户端发送 `/v1/chat/completions`，body 中真实 `model` 是高价模型且 `stream=false`，但 header 中伪造或省略 `x-huakai-model` / `x-huakai-stream`。控制面按 header/default 值选账号、定价或限流，代理随后仍把原始 body 转发给上游，导致路由计划、实际供应商请求、attempt report 和账务依据不一致。若 gateway 直接暴露给租户，`x-huakai-tenant` 还允许客户端伪造 tenant 参与路由规划，存在跨租户路由/账务污染风险。

修复方向：在有大小上限的前提下解析请求体中的协议元数据，用认证上下文派生 tenant；model/stream 应以 body 和已认证租户为准，header 只能作为内部可信元数据或被校验后使用。

### 2. HIGH - `HUAKAI_MOCK_UPSTREAM_ENDPOINT` 可在生产路径绕过控制面、账号规划和 attempt ledger

证据：

- `exploratory/rust-core-gateway/merged/src/config.rs:54` 配置包含 `mock_upstream_endpoint`。
- `exploratory/rust-core-gateway/merged/src/config.rs:274` 从环境变量 `HUAKAI_MOCK_UPSTREAM_ENDPOINT` 读取该值。
- `exploratory/rust-core-gateway/merged/src/listener.rs:72` 只要该值存在，请求直接进入 `forward_endpoint`。
- `exploratory/rust-core-gateway/merged/src/listener.rs:83` 正常的 `planner.plan(...)` 在 mock 分支之后才执行，因此被跳过。
- `exploratory/rust-core-gateway/merged/src/proxy_engine/mod.rs:258` `forward_endpoint` 调用 `forward_inner` 时传入 `planned=None` 且 `terminal_reporter=None`。

具体失败场景：生产或 canary 环境残留 `HUAKAI_MOCK_UPSTREAM_ENDPOINT` 后，真实用户请求会被直接发到 mock/替代 endpoint，不经过 route plan、账号凭据选择、attempt reporter 和控制面结算链路。客户端仍可能拿到 200，但账务、审计、quota 和账号健康完全没有对应记录。

修复方向：mock upstream 只允许 test/dev build 或显式非生产模式；生产启动时发现该变量应 fail fast。若保留人工演练入口，也必须生成明确的审计/attempt 记录并禁止真实凭据注入。

### 3. HIGH - planned vendor endpoint 允许明文 HTTP，可能泄漏上游 Bearer 凭据和请求内容

证据：

- `exploratory/rust-core-gateway/merged/src/account_planner.rs:244` 解析 control plane 返回的 `vendor_endpoint`。
- `exploratory/rust-core-gateway/merged/src/account_planner.rs:249` 只校验存在 scheme。
- `exploratory/rust-core-gateway/merged/src/account_planner.rs:253` 只校验存在 authority，没有要求 `https`。
- `exploratory/rust-core-gateway/merged/src/proxy_engine/http_client.rs:28` default connector 使用 `HttpsConnectorBuilder`。
- `exploratory/rust-core-gateway/merged/src/proxy_engine/http_client.rs:32` connector 明确启用 `https_or_http()`。
- `exploratory/rust-core-gateway/merged/src/proxy_engine/auth.rs:45` planned request 会设置 `Authorization: Bearer ...`。

具体失败场景：控制面配置错误、被污染或返回 `http://provider.internal/...` 时，gateway 会接受该 endpoint，并在明文 HTTP 请求上附带供应商 Bearer token 和用户 prompt/body。这样会把真实上游凭据和用户数据暴露给网络路径或恶意 endpoint。

修复方向：生产 planned route 必须强制 `https`，并按 vendor/tenant 策略做 host allowlist；HTTP 只允许 mock/test 路径，且该路径不得携带真实上游凭据。

### 4. HIGH - terminal attempt report 是 best-effort，队列满时成功请求可丢账

证据：

- `exploratory/rust-core-gateway/merged/src/attempt_reporter/mod.rs:27` 默认队列容量只有 `1024`。
- `exploratory/rust-core-gateway/merged/src/attempt_reporter/mod.rs:140` `report` 使用 `try_send`。
- `exploratory/rust-core-gateway/merged/src/attempt_reporter/mod.rs:151` 队列满时只返回 `ReportEnqueueResult::DroppedFull` 并打计数。
- `exploratory/rust-core-gateway/merged/src/proxy_engine/relay.rs:373` `report_terminal` 调用 reporter。
- `exploratory/rust-core-gateway/merged/src/proxy_engine/relay.rs:390` `let _ = reporter.report(report);` 忽略了丢弃结果。
- `exploratory/rust-core-gateway/merged/src/listener.rs:165` route planning error 的 report 也用 `let _ = reporter.report(report);` 忽略失败。
- `exploratory/rust-core-gateway/merged/tests/attempt_reporter_test.rs:515` 测试覆盖并接受 bounded channel full 时的 drop 行为。

具体失败场景：控制面短暂不可用或消费变慢时，1024 个 report 很容易被突发流量填满。后续真实上游请求仍可成功返回给客户端，但 terminal report 被静默丢弃，billing/audit ledger 没有对应成功调用、token 使用和账号结算记录，形成漏计费和 trust-chain 空洞。

修复方向：对可计费 terminal report 使用 fail-closed、阻塞退避或本地 durable spool；至少成功上游调用的 report 不能仅靠内存 `try_send` 丢弃。

### 5. HIGH - 非流式成功响应没有解析 JSON usage，普通请求 token 账务会缺失

证据：

- `exploratory/rust-core-gateway/merged/src/proxy_engine/relay.rs:64` 只有 SSE 响应才创建 `StreamUsageTap`。
- `exploratory/rust-core-gateway/merged/src/proxy_engine/relay.rs:155` 转发 body chunk 时只记录响应字节。
- `exploratory/rust-core-gateway/merged/src/proxy_engine/relay.rs:163` usage parser 只在 `stream_usage` 存在时执行。
- `exploratory/rust-core-gateway/merged/src/attempt_reporter/types.rs:208` 缺失 token metrics 时填 `UsageMetrics::missing`。
- `exploratory/rust-core-gateway/merged/src/stream_pipeline/openai.rs:63` 已有 OpenAI 非流式 usage 解析函数，但代理转发路径没有调用。
- `exploratory/rust-core-gateway/merged/tests/stream_pipeline_test.rs:243` 只测试了独立解析函数，不覆盖 proxy relay 上报链路。

具体失败场景：常见的非流式 OpenAI Chat Completions 或 Anthropic Messages 返回 JSON body，其中包含 `usage`。当前代理会把 body 直接 relay 给客户端，但 attempt report 中 token 仍为 missing/zero。若 billing 或 reconciliation 依赖 attempt report，普通请求会少计 token 成本；若后续用响应字节做兜底，也无法可靠反推模型 token。

修复方向：对非 SSE 成功响应增加有大小上限的协议 JSON usage 抽取；解析失败应进入 reconciliation 风险状态，而不是静默当作 missing。

### 6. MED - 客户端提供的 OpenAI org/project header 会在 gateway Bearer 凭据下继续转发

证据：

- `exploratory/rust-core-gateway/merged/src/proxy_engine/headers.rs:37` 先调用 `apply_authorization` 设置计划中的上游认证。
- `exploratory/rust-core-gateway/merged/src/proxy_engine/headers.rs:45` `OPENAI_ORG` 被列入可转发 header。
- `exploratory/rust-core-gateway/merged/src/proxy_engine/headers.rs:58` `OPENAI_PROJECT` 被列入可转发 header。
- `exploratory/rust-core-gateway/merged/src/proxy_engine/auth.rs:45` planned auth 使用 gateway/control-plane 提供的 Bearer token。

具体失败场景：租户请求中带入 `openai-organization` 或 `openai-project`，gateway 又用路由计划中的供应商 Bearer token 发给 OpenAI。上游可能按客户端指定的 org/project 做授权、限额或账单归属，而 HUAKAI 的 route plan 和 attempt report 仍按另一个账号/租户记录，造成跨账号错误、账务污染或供应商侧异常拒绝。

修复方向：客户端侧 provider account selection header 必须剥离；org/project 等供应商账户维度只能由 route plan/control plane 明确注入。

### 7. MED - heartbeat 向控制面发送硬编码健康数据，过载节点会被报告成空闲健康

证据：

- `exploratory/rust-core-gateway/merged/src/heartbeat.rs:73` `node_id` 是固定字符串。
- `exploratory/rust-core-gateway/merged/src/heartbeat.rs:74` `started_at_unix_ms` 固定为 `0`。
- `exploratory/rust-core-gateway/merged/src/heartbeat.rs:75` `in_flight_requests` 固定为 `0`。
- `exploratory/rust-core-gateway/merged/src/heartbeat.rs:77` `attempt_queue_depth` 固定为 `0`。
- `exploratory/rust-core-gateway/merged/src/heartbeat.rs:78` `p95_latency_ms` 固定为 `0`。
- `exploratory/rust-core-gateway/merged/src/heartbeat.rs:79` `error_rate_per_minute` 固定为 `0`。
- `exploratory/rust-core-gateway/merged/src/resource_limits.rs:96` 运行时已有真实 in-flight gauge。
- `exploratory/rust-core-gateway/merged/src/attempt_reporter/mod.rs:166` reporter 也能读出真实 queue depth。

具体失败场景：Rust 节点已经过载、draining、attempt reporter 积压或上游错误激增时，heartbeat 仍向控制面汇报 0 in-flight、0 queue、0 error、0 latency。控制面如果据此做节点健康、调度或摘流判断，会继续把流量压到不健康节点，放大失败和丢账风险。

修复方向：heartbeat 必须接入真实 ResourceLimits、AttemptReporter 和延迟/错误统计；暂不可用字段应显式标记 unknown，不能用 0 伪装健康。

### 8. MED - 429/408 等上游限流类响应被归为不可重试 `Upstream4xx`

证据：

- `exploratory/rust-core-gateway/merged/src/proxy_engine/mod.rs:370` `classify_status` 将所有 4xx 归为 `AttemptStatus::Upstream4xx`。
- `exploratory/rust-core-gateway/merged/src/attempt_reporter/types.rs:61` `is_retryable` 只把 `TransportError` 视为 retryable。
- `exploratory/rust-core-gateway/merged/src/attempt_reporter/types.rs:62` `Timeout` 视为 retryable。
- `exploratory/rust-core-gateway/merged/src/attempt_reporter/types.rs:63` `Upstream5xx` 视为 retryable。
- `exploratory/rust-core-gateway/merged/src/proxy_engine/relay.rs:256` terminal report 使用该分类结果。

具体失败场景：供应商账号返回 429 rate limit 或 408 request timeout 时，gateway 把它记录为普通不可重试 4xx。控制面无法从 attempt report 区分“租户请求错误”和“供应商账号限流/临时不可用”，可能不会冷却账号、不会触发 failover，也可能把供应商限流误算成用户错误。

修复方向：单独分类 429、408 以及供应商定义的临时限流错误，报告为 retryable/rate_limited，并带上账号健康信号。

### 9. MED - 请求入站字节数只信任 `Content-Length`，chunked/H2 body 会被记为 0

证据：

- `exploratory/rust-core-gateway/merged/src/proxy_engine/mod.rs:222` `request_bytes_in` 在转发前通过 header 计算。
- `exploratory/rust-core-gateway/merged/src/proxy_engine/mod.rs:380` `content_length_bytes` 只读取 `CONTENT_LENGTH`。
- `exploratory/rust-core-gateway/merged/src/proxy_engine/mod.rs:385` 缺失或解析失败时返回 `0`。
- `exploratory/rust-core-gateway/merged/src/attempt_reporter/types.rs:214` report 中 `bytes_in` 等于 context 的 bytes_in 加 stats 的 bytes_in。
- `exploratory/rust-core-gateway/merged/src/proxy_engine/relay.rs:155` relay 只更新响应 body 统计，没有对请求 body 做实际计数。

具体失败场景：HTTP/2、chunked upload 或任何没有 `Content-Length` 的请求携带真实 prompt/body 发给上游，但 attempt report 记录 `bytes_in=0`。这会破坏按字节的 quota、滥用检测、成本审计和请求体大小追踪；如果后续用 bytes_in 做 reconcile，也会系统性低估。

修复方向：包装 inbound body，在实际转发时累计请求体字节数；不要把缺失 `Content-Length` 等同于空 body。

### 10. MED - `mimicry-boring` feature 打开后会绕过 profile backend intent 的阻断判断

证据：

- `exploratory/rust-core-gateway/merged/src/mimicry/backend_resolver.rs:60` `resolve_profile_mimicry_backend` 进入 backend 选择。
- `exploratory/rust-core-gateway/merged/src/mimicry/backend_resolver.rs:72` 只要 profile 是 `Vendor` 且编译了 `mimicry-boring`，直接返回 Boring backend。
- `exploratory/rust-core-gateway/merged/src/mimicry/backend_resolver.rs:83` 只有未命中 Boring feature 时才继续调用 `profile.backend_intent()`。
- `exploratory/rust-core-gateway/merged/src/mimicry/profile.rs:181` `backend_intent` 中有按 template 计算的支持/阻断逻辑。
- `exploratory/rust-core-gateway/merged/src/mimicry/profile.rs:196` `RustlsUnsupported` 会返回 `KnownGap`。
- `exploratory/rust-core-gateway/merged/src/mimicry/dispatch.rs:52` dispatch 使用 `resolve_profile_mimicry_backend` 的结果。
- `exploratory/rust-core-gateway/merged/src/mimicry/dispatch.rs:59` Boring backend 会进入 `AllowBoring`。
- `exploratory/rust-core-gateway/merged/tests/mimicry_dispatch_test.rs:173` 测试期望 Kiro 这类 profile 被 fail-closed 阻断，但该测试不覆盖 `mimicry-boring` feature 组合。

具体失败场景：生产构建启用 `mimicry-boring` 后，原本应由 `backend_intent()` 判定为 unsupported/known-gap 的 vendor profile 可以直接被允许走 Boring。结果是未完成 R-D 验证的 TLS/header mimicry profile 进入生产 dispatch，控制面的 fail-closed 保障被编译特性绕开。

修复方向：`resolve_profile_mimicry_backend` 应先执行 `backend_intent()` 并尊重 unsupported/known-gap 阻断；只有明确支持 Boring 且通过验证的 profile 才能返回 Boring，同时补齐 feature matrix 测试。

