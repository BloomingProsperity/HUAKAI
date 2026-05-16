# HUAKAI Rust Core Gateway 探索计划

## 1. 元信息

日期: 2026-05-09

lane: codex Rust 探索

工作性质: 探索性 fork, 暂不接入主线。本文只定义 `exploratory/rust-core-gateway/` 下的独立 Rust 数据面验证路线, 不要求 Go 主项目同步改造。

Owner 战略 directive:

> Rust 核心: Rust 接客户端请求, 向 Go/PG 拿路由和账号计划, 然后自己完成转发、流式返回、attempt 上报。这个才是真正的"Rust 核心网关"。

本计划使用的事实边界:

- 当前主线为 Go 后端 + Next.js TS 前端 + PostgreSQL + sqlc。
- Go 后端已经承担 PASR-lite HRW K=3 段调度、cache routing、Bedrock/OpenAI/Anthropic adapter 等控制面与现有数据面能力。
- 本轮不读取、复用或改写任何主线 plan 文件。
- 本轮不读取外部 Rust 反代项目源码；后续若因研究需要阅读外部源码, 必须按 HUAKAI clean-room lane 记录, 且不能把函数名、结构、注释、文件结构、测试或算法顺序搬入本 fork。

## 2. 目标 / 非目标

目标:

- 建立一个独立 Rust 数据面, 由 `m1_listener` 接收客户端 HTTP 请求。
- Rust 数据面通过 RPC 向 Go control plane 请求 route plan 与 account plan。
- Go control plane 继续拥有 PASR-lite、账号池选择、quota/billing/account 状态等权威逻辑。
- Rust 使用 route plan 中的 `account_id`, `acquisition_token`, `vendor_endpoint`, `credentials_handle` 完成 upstream forwarding。
- Rust 自己完成 Anthropic/OpenAI/Bedrock 的流式返回处理, 包括 frame decoding、pass-through、错误分类与 client cancellation。
- Rust 将每次 attempt 的结果上报给 Go control plane, 让 settle、billing、quota、audit 闭环仍在 Go/PG 权威链路内完成。
- Rust fork 提供 mock Go control plane 和 mock upstream vendor, 支持 e2e、failure、load、cancellation 测试。
- 输出可度量的 shadow/canary 指标, 为未来是否接入主线提供依据。

非目标:

- 不替换 Go control plane。
- 不重写 PASR-lite HRW K=3 段调度算法。
- 不改 PostgreSQL schema。
- 不改 billing ledger、quota enforcement、auth core、账号池状态机。
- 不接入主线流量。
- 不修改主线 Go handler、adapter、scheduler、sqlc、migration 或 deployment。
- 不引入生产部署脚本。
- 不修改 `LICENSE`。
- 不把 envoy-ai-gateway、Pingora、linkerd-proxy、tower 等外部项目源码 verbatim 复制进 HUAKAI；即使 license 允许引用, HUAKAI clean-room 策略也禁止源码搬运。

## 3. 模块拆分 + 责任划分

设计原则:

- Rust fork 是数据面, Go 是控制面。任何涉及账号选择、quota、billing、tenant 权限、segment ownership 的权威决策都留在 Go。
- Rust 可以缓存短 TTL route plan, 但缓存只是性能优化, 不能成为第二套调度真相。
- Rust 默认不直接写 PostgreSQL。直接读 PG 也默认不做；如果未来必须读取只读配置快照, 需要 Owner 单独确认读取表、字段、TTL、fallback 和审计边界。
- 每个模块只使用 public crate API 和公开协议文档。禁止复刻外部 proxy 项目的目录结构、middleware 层次、函数命名或测试 fixture。

| 模块 | 职责 | 估 LoC | 依赖 crate | Go control plane 接口 | PostgreSQL 边界 |
| --- | --- | ---: | --- | --- | --- |
| `m1_listener` | 提供 HTTP/HTTPS server, 接收 `/v1/messages`, `/v1/chat/completions`, Bedrock-compatible endpoint; 处理 header normalization、body size limit、client disconnect、request id。 | 700-1100 | `tokio`, `axum` or `hyper`, `hyper-util`, `http-body-util`, `bytes`, TLS listener library, `tower-http` public API | 调用 `m2_route_client` 间接请求 `RouteQuery`; 不直接理解 Go scheduler。 | 无 |
| `m2_route_client` | Rust 到 Go control plane 的 RPC client; 管理 deadlines、retries、circuit breaker、schema version、idempotency key。 | 600-900 | 推荐生产 `tonic`, `prost`; 探索 mock 可加 `reqwest`, `serde`, `serde_json`; `thiserror` | `RouteQuery`, `AttemptReport`, `HealthCheck`, `Heartbeat`。 | 无 |
| `m3_account_planner` | 封装一次 client request 的 account acquisition lifecycle; 调 Go `pool.Selector` 等价能力拿 `account_id` + `AcquisitionToken`; 维护 per-request attempt state。 | 500-800 | `uuid`, `time`, `tokio`, `parking_lot` or `dashmap` only if needed | 只通过 `m2_route_client`; 不在 Rust 里实现 PASR。 | 无 |
| `m4_proxy_engine` | 根据 route plan 建立 upstream connection; bearer auth 用于 Anthropic/OpenAI; SigV4 用于 Bedrock; 处理 timeout、retry handoff、header allowlist、body streaming。 | 1200-1900 | mimicry transport path, `bytes`, `http`, `aws-sigv4`, `aws-credential-types`, `pin-project-lite` if needed | route plan 提供 `vendor_endpoint`, `credentials_handle`, `auth_mode`; attempt 完成后交给 `m6_attempt_reporter`。 | D3 burn-the-boats: 不保留备用 rustls upstream 通路；mimicry path 坏了修 mimicry path。 |
| `m5_stream_pipeline` | 处理 Anthropic SSE、OpenAI SSE、Bedrock binary EventStream 的 frame decode/encode、usage extraction、cache metrics extraction、protocol error 分类。 | 1300-2100 | `bytes`, `memchr`, `tokio-util`, `futures`, `serde_json`, `crc32fast`; Bedrock 可评估 `aws-smithy-eventstream` public API | 不直接 RPC; 输出 attempt metrics 给 `m6_attempt_reporter`。 | 无 |
| `m6_attempt_reporter` | 可靠上报 attempt; 支持 success/failure/cancel/timeout/protocol_error; 保证 idempotency; 将 settle/billing 闭环交给 Go。 | 500-800 | `tonic` or `reqwest`, `tokio`, `serde`, `uuid`, `thiserror` | `AttemptReport(acquisition_token, status, tokens_used, cache_metrics, idempotency_key)`。 | 无；禁止直接写 usage/billing/quota 表 |
| `m7_observability` | tracing、metrics、logs redaction、OTLP/Prometheus export、health endpoint; 统一 Rust span 与 Go route/attempt id。 | 500-900 | `tracing`, `tracing-subscriber`, `opentelemetry`, `opentelemetry-otlp`, `metrics`, `metrics-exporter-prometheus` | `Heartbeat` 上报 node status; metrics label 与 Go request id 对齐。 | 无 |
| `m8_test_harness` | mock Go control plane、mock Anthropic/OpenAI/Bedrock upstream、golden stream tests、fault injection、load smoke。 | 1200-1800 | `tokio-test`, `rstest`, `proptest` optional, `wiremock` or local `axum` mocks, `criterion` optional | mock 实现完整 v0 RPC contract; 不依赖真实 Go 主线。 | 无；测试可用临时 fake store, 不连接生产 PG |

模块之间的数据流:

1. `m1_listener` 接收 client request, 提取 `tenant_id`, `requested_model`, `session_hash`, `request_protocol`, stream mode。
2. `m3_account_planner` 通过 `m2_route_client` 调 Go control plane, 获取 route/account plan。
3. `m4_proxy_engine` 根据 plan 连接 upstream vendor, 构造鉴权, 推送 request body。
4. `m5_stream_pipeline` 在 upstream response 和 client response 之间做 bounded streaming parse, 同时提取 usage/cache/error 指标。
5. `m6_attempt_reporter` 用 `acquisition_token` 上报 attempt 结果。
6. `m7_observability` 贯穿全部模块, 输出 request span、attempt span、stream frame metrics、control plane RPC metrics。
7. `m8_test_harness` 在不启动真实 Go/PG 的情况下验证上述链路。

## 4. RPC contract 设计

推荐路线: production contract 使用 gRPC, 探索期 mock 同时支持 HTTP/JSON shim。

理由:

- gRPC + `tonic`/`grpc-go` 适合 Rust 数据面与 Go 控制面的内部 RPC: typed contract、deadline、status code、metadata、codegen、schema evolution 都更清楚。
- HTTP/JSON 适合探索期快速 mock、curl 调试、复用已有 Go net/http 习惯, 但长期容易出现 schema drift, deadline/cancellation 语义弱, 高频 route query CPU/alloc 成本更高。
- 由于给主线 Go 增加 `grpc-go` 属于未来主线依赖变更, 本 fork 不直接要求主线引入。当前 fork 先把 IDL 和 mock 做清楚, 未来接入前由 Owner 决策是否接受 gRPC 依赖。

### 4.1 路由查询 RPC: `RouteQuery`

输入字段:

- `request_id`: Rust 生成或透传的 request id。
- `tenant_id`: 租户标识, 必填。
- `requested_model`: client 请求的模型名。
- `session_hash`: 用于 Go PASR/cache routing 的稳定 hash 输入。
- `request_protocol`: `anthropic_messages`, `openai_chat_completions`, `bedrock_runtime`。
- `stream`: client 是否请求流式返回。
- `client_deadline_ms`: Rust 侧从 context 推导的剩余 deadline。
- `previous_attempts`: 同一 client request 下已失败 attempt 的摘要; Go 用它决定是否返回下一账号, Rust 不自行计算 K=3。
- `capability_hints`: 可选, 如 tool use、vision、large context、cache preference。字段必须是 control-plane-defined allowlist, 避免 Rust 私自创造调度维度。

输出字段:

- `route_plan_id`: Go 生成的 plan id, 用于 trace correlation。
- `account_id`: 本次 attempt 使用的账号。
- `acquisition_token`: Go control plane 颁发的一次性或短 TTL token, attempt 上报必须携带。
- `vendor`: `anthropic`, `openai`, `bedrock`, or future enum。
- `upstream_model`: Go 映射后的供应商模型名。
- `vendor_endpoint`: Rust 应连接的 upstream base URL 或 fully qualified endpoint。
- `credentials_handle`: Rust 可用于获取或使用凭据的 opaque handle。
- `auth_mode`: `bearer`, `aws_sigv4`, `pre_signed`, `control_plane_signed`。
- `route_ttl_ms`: Rust 可缓存 plan 的上限; v0 推荐 `0` 或极短 TTL。
- `attempt_deadline_ms`: 本 attempt 的 upstream deadline。
- `max_body_bytes`, `max_stream_frame_bytes`: Go 下发的 guardrail。

凭据边界:

- v0 不允许 Rust 直接读 PG secret。
- 如果 `auth_mode=bearer`, Go 可以返回短 TTL bearer material 或让 `credentials_handle` 指向 control-plane credential broker。
- 如果 `auth_mode=aws_sigv4`, Rust 若要自己签名, 需要短 TTL AWS signing material; 如果 Owner 不接受 Rust 持有短 TTL secret, 则必须增加 Go signing broker 或 `pre_signed` 模式。这是 Owner 决策点, 不在本 fork 默认强行定死。
- 所有 credential 字段在 trace/log/metrics 中必须 redacted。

### 4.2 attempt 上报 RPC: `AttemptReport`

输入字段:

- `request_id`, `route_plan_id`, `attempt_id`。
- `acquisition_token`: Go 颁发, Rust 原样带回。
- `status`: `success`, `client_cancel`, `upstream_4xx`, `upstream_5xx`, `timeout`, `network_error`, `protocol_error`, `control_plane_error`, `internal_error`。
- `http_status`: upstream status 或 client-visible status。
- `started_at`, `ended_at`, `latency_ms`。
- `tokens_used`: `input_tokens`, `output_tokens`, `total_tokens`, `source`。
- `cache_metrics`: `cache_read_tokens`, `cache_write_tokens`, `cache_hit`, `source`。
- `bytes_in`, `bytes_out`, `frames_in`, `frames_out`。
- `vendor_request_id`: 从 upstream header/event 提取, 如存在。
- `retryable`: Rust 对 transport/protocol 层的判断; 最终 retry 仍由 Go 下次 route query 决定。
- `error_class`, `error_message_redacted`。
- `idempotency_key`: 至少由 `request_id + attempt_id + acquisition_token` 派生。

输出字段:

- `ack`: bool。
- `ack_id`: Go 生成的 settle ack id。
- `accepted_at`: Go control plane 接受时间。
- `advisory`: 可选; 仅用于日志或下一步 hint, 不让 Rust 越权更新 billing/quota。

上报语义:

- Rust 必须尽力上报 every attempt, 包括 client cancel 和 protocol parse failure。
- 上报失败时, Rust 记录本地 retry buffer; 探索期 buffer 可以是内存队列, 生产接入前必须决定是否需要 WAL-like durable queue。
- Go 侧 ack 是 billing/quota 闭环的唯一确认, Rust 不直接写账。

### 4.3 健康检查 + 心跳

`HealthCheck`:

- Rust 调 Go: 检查 control plane 是否可用, 返回 schema version、server time、route service status。
- Go 调 Rust 或探测 Rust: 检查 listener、upstream dialer、stream parser、attempt reporter queue 状态。

`Heartbeat`:

- Rust 定期上报 `node_id`, `build_sha`, `schema_version`, `started_at`, `in_flight_requests`, `open_upstream_connections`, `attempt_report_queue_depth`, `p95_control_plane_rpc_ms`, `error_rate_1m`。
- Go 返回 `ack`, `desired_schema_version`, `drain_mode`。若 Go 要求 drain, Rust 停止接新请求但继续完成已有 stream。

### 4.4 gRPC vs HTTP/JSON 利弊

gRPC 优点:

- Strong IDL, Rust/Go 双端 codegen, 字段演进可控。
- Deadline/cancellation 与 status code 明确。
- 高频内部 RPC 开销较低。
- Metadata 可承载 trace id、tenant context、schema version。
- 更适合未来双向 health/watch。

gRPC 缺点:

- 主线 Go 未来要加 `grpc-go` 依赖, 属于 Owner 需确认的中高风险变更。
- proto 管理、codegen、CI 工具链会增加维护成本。
- 运维排查不如 JSON 直观, 需要 grpcurl 或专门 tooling。

HTTP/JSON 优点:

- Go 当前技术栈更自然, 不需要新 RPC runtime。
- curl/debug 简单, mock 快。
- 初期可直接复用已有 internal/admin HTTP idiom。

HTTP/JSON 缺点:

- 字段 drift 风险高。
- deadline/cancel/retry 语义需要自行约定。
- 高频 route query 下 JSON encode/decode 与 allocation 成本更高。
- 错误码、schema version、metadata 容易散落到 header/body 两套位置。

结论:

- `M-rust-1` 到 `M-rust-3` 用 mock HTTP/JSON 快速验证数据面流程。
- `M-rust-4` 前冻结 gRPC proto v0 作为生产候选 contract。
- 未来接主线前由 Owner 决定是否接受 `grpc-go`; 若不接受, 则保留同字段 HTTP/JSON contract, 并补强 schema validation 与 deadline/cancel 测试。

## 5. 流式协议处理

总体策略:

- Rust stream parser 必须是 bounded incremental parser, 不把完整响应读入内存。
- 每个 parser 都输出统一 `StreamEvent` 内部事件: `Data`, `Usage`, `CacheMetric`, `Done`, `ProtocolError`, `UpstreamError`。
- 对 client response 尽量 pass-through 原协议, 避免无意义转换；只在需要 usage/cache extraction 或错误归类时解析。
- frame size、header size、idle timeout、total bytes 都必须有上限, 上限来自 Go route plan 或 Rust config。

### 5.1 Anthropic SSE

协议特点:

- SSE frame 由 `event:` 与一个或多个 `data:` 行组成, 空行结束 frame。
- `data:` 可能是多行, 需要按 SSE 语义合并。
- event type 承载 message start、content delta、message delta、message stop、error 等状态。

解码策略:

- 使用 `bytes::BytesMut` 增量读取, 按 LF 查找行边界, 兼容 CRLF。
- 忽略 comment line, 保留未知字段到 raw frame metadata。
- 遇到空行时组装一个完整 SSE frame。
- 对 `data:` 做 bounded JSON parse; parse 失败归类为 `protocol_error`, 并触发 attempt report。
- 对 usage/cache 相关字段只做 allowlist 提取, 不把 vendor JSON schema 散落到业务模块。
- client 已断开时立即停止 upstream read, report `client_cancel`。

性能预期:

- 时间复杂度 O(response bytes)。
- 内存占用约等于当前 frame buffer + socket buffer, 默认单 frame 上限建议 64 KiB, 可由 Go 下发覆盖。
- p99 frame parse overhead 目标低于 Go 当前 hot path 的可测解析开销, 具体阈值由 shadow benchmark 决定。

### 5.2 OpenAI SSE

协议特点:

- 常见 frame 为单行 `data: <json>`。
- `data: [DONE]` 表示终止。
- usage 可能在最后 chunk 或非流式响应中出现。

解码策略:

- 与 Anthropic 共享基础 SSE line scanner, 但 OpenAI parser 只要求 `data:` 行。
- 对 `[DONE]` 立即输出 `Done`, flush client response, 然后触发 attempt settle。
- 支持空行、comment line、偶发多行 data 的容错, 但不把容错当作调度信号。
- usage 提取失败不阻塞 stream; 标记 `tokens_used.source=missing`, 由 Go billing 策略决定后续处置。

性能预期:

- parser hot loop 只做 memchr line split + minimal branch。
- 对普通 delta chunk 不做完整结构化转换, 只在配置要求 usage extraction 时解析 JSON。
- 目标是单 stream 额外 allocation 接近 frame count 常数级, 不随总 token 数线性积累。

### 5.3 Bedrock binary EventStream

协议特点:

- AWS EventStream 使用 8-byte prelude 表示 total length 与 headers length, 随后有 prelude CRC、headers、payload、message CRC。
- headers 是 typed key/value; payload 可承载模型输出 JSON chunk、错误事件或 trace。
- Bedrock request signing 需要 SigV4, signing region/service 必须由 route plan 或 endpoint 派生。

解码策略:

- 先读取 fixed prelude, 校验 length 范围, 再读取剩余 message。
- 使用 `crc32fast` 校验 prelude/message CRC；CRC 失败归类为 `protocol_error`。
- header parser 只提取 allowlist event type 与 content type, 未知 header 保留计数但不透出 secret。
- payload 作为 `Bytes` slice 处理, 只在需要 usage/error 提取时 JSON parse。
- 输出给 client 时保持 Bedrock-compatible binary stream, 除非未来明确要做跨协议转换。
- 对 upstream partial frame、oversized frame、idle timeout 都要有专门测试。

性能预期:

- 每条 message 一次 bounded allocation 或 `Bytes` slice view。
- CRC 校验是 CPU 成本主项；benchmark 要分离 network copy、CRC、JSON extraction 三类耗时。
- 默认 max message size 由 Go route plan 下发；探索期建议 1 MiB 起步, load test 后调整。

## 6. 与主项目协作边界

本轮什么动:

- 只在 `exploratory/rust-core-gateway/` 下创建 Rust fork 计划和后续 workspace。
- Rust fork 可以定义自己的 `Cargo.toml`, mock server, proto draft, benchmark scripts。
- Rust fork 可以通过 mock Go control plane 验证 contract。
- Rust fork 可以在文档里列出未来主线接入所需 Go API, 但本轮不修改 Go 主线实现。

本轮不动:

- 不改 `backend/`。
- 不改主线 Go `pool.Selector`, scheduler, provider adapter, forwarder, SSE parser。
- 不改 `frontend/`。
- 不改 SQL migration、sqlc query、PostgreSQL schema。
- 不改 deployment、Docker、CI 主线配置。
- 不改 billing/quota/auth core。

Go control plane 协作方式:

- 探索期用 mock control plane 实现 v0 RPC。
- 若主线已经有可安全调用的 admin/internal HTTP API, Rust fork 可以写 adapter client, 但不得要求主线为探索 fork 改接口。
- 真实 `pool.Selector` 调用需要未来 Go control plane 暴露 route/account planning RPC；这是接入前决策, 不是本计划执行的默认动作。

未来接入条件建议:

- Shadow 模式至少覆盖 Anthropic/OpenAI/Bedrock 三类 streaming happy path 和主要 failure path。
- 与 Go 数据面相比, Rust 数据面在同等硬件与同等 upstream mock 条件下达到 Owner 认可的硬指标。建议候选值: p95 proxy overhead 降低 >= 20%, 每 1000 并发 stream RSS 降低 >= 30%, CPU/token 降低 >= 20%。
- attempt report 与 Go billing/quota audit 完全对账, 不允许成功请求漏报或 cancel 漏报。
- stream byte-for-byte 或 semantic parity 测试通过, 包括 `[DONE]`, Anthropic multi-line SSE, Bedrock CRC failure。
- Observability 能在同一 trace 中串起 Rust request span、Go route span、Go settle span。

clean-room 边界:

- 不 verbatim 抄 envoy-ai-gateway、Pingora、linkerd-proxy、tower 或其他外部 Rust 反代项目源码。
- 允许使用 crates 的 public API, 例如 `hyper`, `axum`, `tonic`, `tracing`; 禁止复制其内部实现、目录结构、注释或测试。
- 协议行为优先来自 vendor public protocol docs 和 HUAKAI 自有 specs/tests。
- 如果后续阅读外部源码, 只允许 specifier lane 输出行为摘要, implementer lane 只能读摘要；同一 agent session 不得同时做两条 lane。

## 7. 风险登记

| ID | 风险 | 影响 | 缓解 / 测试方向 |
| --- | --- | --- | --- |
| `R-rust-1` | clean-room 污染: Rust proxy 生态有成熟项目, 容易无意复制结构或命名。 | 破坏 MIT clean-room 防线, 影响未来开源/商业尽调。 | 本 fork 不读外部源码起步; 若必须读, 按 lane guard 记录; 模块名、contract、tests 使用 HUAKAI 自有命名。 |
| `R-rust-2` | RPC 跨进程延迟: 每次 attempt 都要问 Go, 可能抵消 Rust hot path 收益。 | p95/p99 变差, 高并发下 control plane 成瓶颈。 | route query deadline; local short TTL cache 只做性能优化; benchmark 分离 listener/proxy/RPC 成本; 必测 Go RPC degraded path。 |
| `R-rust-3` | Rust async lifetime / backpressure 复杂度。 | stream cancel、half-close、borrow/pin 错误会变成难排查 bug。 | 先实现简单 ownership model; 用 `Bytes` 和 channel 边界减少 borrowed stream; cancellation、slow client、slow upstream 必测。 |
| `R-rust-4` | 双语言维护成本。 | Go/Rust contract drift, debug 成本上升, Owner 单人节奏压力更大。 | proto/contract 单源; mock conformance tests; 不到 hard metrics 不接主线; 文档记录每个跨语言字段 owner。 |
| `R-rust-5` | 段表/route plan 分布式一致性。 | Rust 缓存过期 route plan 可能绕过 Go 最新账号状态、quota、禁用标记。 | v0 默认 route TTL 为 0; 如启用 cache, Go 下发 TTL 与 invalidation version; 禁止 Rust 计算 PASR。 |
| `R-rust-6` | observability 双栈割裂。 | 生产事故中无法串起 client request、route decision、upstream attempt、billing settle。 | 所有 RPC 带 `request_id`, `route_plan_id`, `attempt_id`; OTLP trace propagation; heartbeat 暴露 queue/deadline/status。 |
| `R-rust-7` | Owner 单人节奏与探索分支发散。 | Rust fork 变成另一个大项目, 拖慢主线。 | milestone 每个 atom 可停; 不接主线前只保留少量关键指标; 每阶段输出 go/no-go。 |
| `R-rust-8` | secret handling 边界不清。 | Rust 日志泄漏 bearer/AWS key, 或 long-lived secret 离开 Go control plane。 | 默认 opaque `credentials_handle`; 短 TTL material 必须 redacted; Owner 决定 signing broker vs Rust signing。 |
| `R-rust-9` | attempt 上报与 billing/quota 不一致。 | 成功请求漏计费、失败请求误计费、重试重复扣量。 | `idempotency_key`; every terminal path report; mock Go 对账 tests; report failure queue 指标和恢复策略。 |
| `R-rust-10` | streaming parser 细节错误。 | 客户端收到坏帧、卡流、提前结束, 或 usage/cache metrics 丢失。 | vendor-specific golden streams; fuzz line splitting / EventStream length; slow frame and partial frame e2e。 |

## 8. milestones

总估时: 31-48 atom-day。估时只覆盖探索 fork 到 shadow-readiness report, 不含主线 Go 接入、不含真实生产部署。

| Milestone | atom 范围 | 估 LoC | 依赖 | 验收 |
| --- | --- | ---: | --- | --- |
| `M-rust-1` | 建立独立 cargo workspace、config、error type、request ids、基础 tracing; 不实现业务 forwarding。 | 500-800 | `tokio`, `tracing`, `thiserror`, `serde` | `cargo test` 通过; 启动 binary 可返回 local health; 不读取真实 PG/Go。 |
| `M-rust-2` | `m1_listener` HTTP server + mock echo upstream; 支持 request body streaming、client cancel detection、body limit。 | 800-1200 | `axum`/`hyper`, `bytes`, TLS listener optional | e2e: normal request、oversized body、client cancel、slow client。 |
| `M-rust-3` | `m2_route_client` + mock Go control plane HTTP/JSON contract; route query/health/heartbeat 初版。 | 700-1000 | `reqwest` or local `axum`, `serde_json` | mock route plan 返回 account/token/endpoint; control plane down 时 Rust 返回明确 5xx 并记录 metrics。 |
| `M-rust-4` | gRPC proto v0 draft + `tonic` client/server mock; 冻结字段命名、deadline、status mapping。 | 900-1300 | `tonic`, `prost`, `tonic-build` | HTTP/JSON 与 gRPC mock conformance tests 覆盖同一 contract; Owner 可选择是否保留 gRPC。 |
| `M-rust-5` | `m3_account_planner` + `m4_proxy_engine` bearer vendors; Anthropic/OpenAI non-streaming 与 streaming pass-through。 | 1300-1900 | mimicry transport path, `http-body-util` | mock vendor e2e: 200、4xx、5xx、timeout、network reset; Rust 不重写 PASR。 |
| `M-rust-6` | `m5_stream_pipeline` for Anthropic/OpenAI SSE; usage/cache extraction; `[DONE]` 与 multi-line frame。 | 1000-1600 | `bytes`, `memchr`, `serde_json`, `tokio-util` | golden stream tests; partial line、CRLF、多 data line、bad JSON、client cancel 全覆盖。 |
| `M-rust-7` | Bedrock SigV4 + binary EventStream parser; CRC、headers、payload、error event 分类。 | 1200-1800 | `aws-sigv4`, `aws-credential-types`, `crc32fast`, `bytes` | mock Bedrock: valid stream、CRC fail、oversized frame、partial frame、SigV4 failure。 |
| `M-rust-8` | `m6_attempt_reporter`; every terminal path report; idempotency; in-memory retry queue; Go ack semantics。 | 700-1100 | `tonic`/`reqwest`, `uuid`, `tokio` | success/cancel/timeout/protocol_error 都有 attempt report; duplicate report 被 mock Go 幂等接受。 |
| `M-rust-9` | `m7_observability`; Prometheus/OTLP; redaction; heartbeat drain mode; dashboard-ready metric names。 | 600-1000 | `opentelemetry`, `metrics`, `metrics-exporter-prometheus` | metrics 包含 RPC latency、stream frames、queue depth、cancel count; logs 不含 secrets。 |
| `M-rust-10` | `m8_test_harness` load smoke + shadow-readiness report; 给出 go/no-go 与主线接入差距。 | 1000-1600 | `criterion` optional, `tokio-test`, mock servers | 100/500/1000 concurrent stream smoke; 对比 Go hot path 需要的 benchmark harness 清单; 输出 Owner 决策报告。 |

执行顺序:

1. 先做 `M-rust-1` 到 `M-rust-3`, 证明 Rust listener -> route query -> mock upstream -> attempt report 的骨架可跑。
2. 再做 `M-rust-4`, 防止 HTTP/JSON mock 演化成无类型 contract。
3. `M-rust-5` 和 `M-rust-6` 先覆盖 Anthropic/OpenAI, 因为 SSE 是主热路径。
4. `M-rust-7` 单独处理 Bedrock, 避免 binary EventStream 和 SigV4 把前面模块拖复杂。
5. `M-rust-8` 到 `M-rust-10` 收口 reliability、observability、load 和决策报告。

## 9. 决策点

`D-rust-1`: RPC 协议选型。

- 选项 A: gRPC as production contract, HTTP/JSON only for mock/debug。
- 选项 B: HTTP/JSON 全程保留, 不引入 `grpc-go`。
- 推荐: A, 但不在本 fork 强迫主线引入依赖。接主线前 Owner 决策。

`D-rust-2`: PASR 调度所有权。

- 选项 A: Rust 永远通过 Go `pool.Selector`/route RPC 获取账号计划。
- 选项 B: Rust 重新实现 PASR-lite HRW K=3。
- 推荐: A。本探索明确不重写 Go 那套调度逻辑, 避免双真相、clean-room 风险和对账风险。

`D-rust-3`: 接入主线硬指标。

- 需要 Owner 拍板 Rust fork 到什么程度才值得接入。
- 建议候选: p95 proxy overhead 降低 >= 20%; 每 1000 并发 stream RSS 降低 >= 30%; CPU/token 降低 >= 20%; attempt report 零漏报; 三 vendor streaming parity 全通过。

`D-rust-4`: credential material 边界。

- 选项 A: Go 返回短 TTL bearer/AWS signing material, Rust 内存中使用并 redacted。
- 选项 B: Go 保持 secret, Rust 通过 signing broker 或 pre-signed request 完成鉴权。
- 推荐: Bedrock 优先评估 B, bearer 可接受短 TTL A; 最终按安全审查决定。

`D-rust-5`: Bedrock stream 对外策略。

- 选项 A: pass-through Bedrock binary EventStream, Rust 只解析 metrics/error。
- 选项 B: Rust 转换为统一 SSE。
- 推荐: A。协议转换容易造成 parity bug, 统一转换可作为未来功能, 不放入 v0。

## 10. 不做的 (Out of Scope)

- 不写 Rust 实现代码作为本次任务的一部分。
- 不修改主线 Go 后端。
- 不修改 Next.js 前端。
- 不改 PostgreSQL schema、migration、sqlc query。
- 不重写 PASR-lite、HRW、K=3 fallback、segment table。
- 不改 quota enforcement。
- 不改 billing ledger。
- 不改 auth core。
- 不接生产或 staging 流量。
- 不发布 Docker image 或 deployment manifest。
- 不新增主线 runtime dependency。
- 不读、复制或改写外部 Rust proxy 项目源码。
- 不把 Rust fork 作为默认 gateway。
- 不承诺 Rust 一定会取代 Go data path; 本探索必须用指标证明价值。
- 不修改 `LICENSE`。
- 不处理 admin UI。
- 不处理 provider/account 配置 CRUD。
- 不把 mock control plane 的行为当作真实 Go control plane 已实现能力。
