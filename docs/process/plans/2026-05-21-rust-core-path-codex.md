# 2026-05-21 Rust 核心网关性能 + 资源安全加固实施计划（Codex 独立稿）

| 字段 | 内容 |
|---|---|
| Owner directive | “HUAKAI Rust 核心网关 —— 性能 + 资源安全加固。任务：独立起草一份实施计划。” |
| 独立性声明 | 已按要求独立核源码；未读取 `docs/process/plans/2026-05-21-rust-core-path-claude.md`。 |
| Scope | 仅规划 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/` 及对应测试、配置、proto 语义文档内的 6 个已初核问题。 |
| Out of scope | 不改 `LICENSE`、不改数据库 schema、不改 auth/billing/quota 核心、不接第三方参考项目、不在本任务执行代码修改、git 操作或 cargo 构建/测试。 |
| Success criteria | 6 项问题均有本地源码证据、具体修法、文件范围、风险/回归面、测试/验证方式、拆分估时、顺序依赖；缓存一致性和全局限流 503 返回点明确。 |
| Time estimate | 计划起草 1.5-2h；后续实现预计 1.5-2.5 人日，取决于是否接入自定义 hyper serve loop。 |
| Blast radius | 业务请求入口、control-plane route 查询频率、SSE 流解析、proxy relay 超时、HTTP server 接入层。 |
| Decision points | 是否允许启用 `hyper-util` 的 `server-auto/server-graceful/service` features 来替换 `axum::serve`；是否把 route cache 默认保持 0ms 关闭；是否允许新增少量资源保护配置项。 |

## 源码核实结论

1. OpenAI SSE 解析判断准确，但措辞需收窄：每个非空、非 `[DONE]` 的 OpenAI SSE data 帧都会先透传 `Data`，再调用 `extract_usage_from_json_bytes(data)`；该函数用 `serde_json::from_slice::<Value>` 解析整棵 DOM 后只取 `usage`。证据：`stream_pipeline/openai.rs:45-46`、`stream_pipeline/openai.rs:62-64`、`stream_pipeline/openai.rs:67-86`。

2. Anthropic SSE 解析判断基本准确，但 error 事件是另一条 JSON 解析路径：`message_start/content_delta/content_block_delta/message_delta/空事件名/unknown` 会调用 `extract_json_metrics`，其中 `serde_json::from_slice::<Value>` 解析 DOM 后只抽 usage/cache；`error` 事件用 `parse_error_message` 再解析一次 JSON DOM 取错误 message。证据：`stream_pipeline/anthropic.rs:43-57`、`stream_pipeline/anthropic.rs:68-93`、`stream_pipeline/anthropic.rs:95-102`、`stream_pipeline/anthropic.rs:105-165`。

3. route cache 判断准确且现有测试语义漂移：`AccountPlanner::new(route_client, _cache_ttl)` 丢弃 `_cache_ttl`，`AccountPlannerInner` 只保存 `RouteClient`；`plan()` 每次构造 query 后直接 `route_client.query_route(query).await`。证据：`account_planner.rs:30-36`、`account_planner.rs:177-195`。同时 `RouteClientOptions` 有 `route_cache_ttl` 字段但 `RouteClientInner` 不保存任何 cache map，`query_route()` 直接进入 RPC/retry loop。证据：`route_client.rs:165-181`、`route_client.rs:193-197`、`route_client.rs:273-318`。现有测试 `route_cache_ttl_enabled_still_queries_control_plane_each_time` 明确断言 TTL 开启仍查询 2 次，说明当前实现是“配置存在但缓存关闭”。证据：`tests/route_client_test.rs:487-505`。

4. 第 3 项路径需纠正：仓库里没有 `proxy_engine/redaction.rs`，实际敏感 header 判断在 `src/redaction.rs`。该函数每次调用 `name.to_ascii_lowercase()` 分配 `String` 后与静态数组比较。证据：`redaction.rs:12-20`、`redaction.rs:23-28`。

5. 全局资源保护判断准确：`lib.rs` 只在业务路由上加了 `RequestBodyLimitLayer` 和 drain middleware，没有进程级连接/请求并发上限；`run()` 直接 `TcpListener::bind` 后 `axum::serve(listener, router)`。证据：`lib.rs:161-180`、`lib.rs:183-201`。`listener.rs` handler 只做 Content-Length 体积检查、control-plane 失败 503 和 proxy 错误映射，没有 overload load-shedding。证据：`listener.rs:50-70`、`listener.rs:83-118`、`listener.rs:131-147`。

6. stream frame 上限判断准确：SSE 默认是 64KiB。证据：`stream_pipeline/sse.rs:4`。但 route plan 的 `max_stream_frame_bytes` 非 0 时直接转成 `usize` 返回，没有本地 hard cap。证据：`proxy_engine/mod.rs:278-289`、`proxy_engine/mod.rs:338-343`。

7. 超时判断准确：proxy engine 有 `BODY_IDLE_TIMEOUT = 30s`，relay 每次等待 upstream body frame 时硬编码 30s，超时后报 `body stream idle timeout`。证据：`proxy_engine/mod.rs:47-49`、`proxy_engine/relay.rs:100-120`。server 侧 `run()` 直接用 `axum::serve`，当前本地代码没有配置 HTTP/1 header read、HTTP/2 keepalive/idle、请求 body read idle、下游写 idle 等保护。证据：`lib.rs:183-201`。

## 总体技术方向

优先采用“低依赖、可配置、默认安全”的路径：

- SSE usage/cache 提取：去掉 `serde_json::Value` DOM，改为协议专用的 `#[derive(Deserialize)]` 小结构体或轻量扫描 helper，只解析需要的字段，保持非法 JSON 仍产生 `ProtocolError` 的现有语义。
- route cache：落在 `AccountPlanner` 层，不落在 `RouteClient` 传输层；只缓存 control plane 明确带 `route_ttl_ms > 0` 的 plan，命中时重新生成本地 `AttemptLifecycle`，并用 `route_ttl_ms`、本地 `route_cache_ttl_ms`、`upstream_auth.expires_at_unix_ms` 共同决定过期时间。
- 全局限流：请求级用 `tokio::sync::Semaphore::try_acquire_owned()` 做 fail-fast middleware，overload 直接 503；连接级用 axum `serve::Listener` wrapper 持有连接 permit，限制进程已接受连接数。
- server timeout：若 Owner 接受 feature 扩展，使用现有 direct dependency `hyper-util` 增加 `server-auto/server-graceful/service` features，实现自有 serve loop 才能配置 hyper server builder；否则 `axum::serve` 无法完整暴露这些 server knobs，只能先做 request/relay 层 timeout。

## 任务拆分与具体修法

### Task 1：SSE usage/cache 提取去 DOM

**要改文件**

- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/stream_pipeline/openai.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/stream_pipeline/anthropic.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/stream_pipeline_test.rs`

**修法**

- OpenAI：
  - 删除 `use serde_json::Value`。
  - 新增只含 `usage: Option<OpenAiUsageFields>` 的 envelope。
  - `OpenAiUsageFields` 覆盖现有兼容字段：`prompt_tokens/input_tokens/completion_tokens/output_tokens/total_tokens`。
  - `extract_usage_from_json_bytes()` 保持函数签名和错误语义，内部 `serde_json::from_slice::<OpenAiUsageEnvelope>(data)`，不构造 DOM。
- Anthropic：
  - 删除 metrics 路径的 `Value` DOM。
  - 新增 `AnthropicMetricsEnvelope { usage, message }`，`message` 里只保留 `usage`。
  - 新增 `AnthropicUsageFields`，覆盖 `input_tokens/output_tokens/total_tokens/cache_creation_input_tokens/cache_read_input_tokens`。
  - `parse_error_message()` 改为只解析 `error.message` 或顶层 `message` 的 typed envelope；只有需要返回错误文本时分配 `String`。
- 保持现有行为：
  - OpenAI 非 DONE 帧仍 emit `Data`。
  - malformed JSON 仍 emit `ProtocolError`，并继续处理下一帧。
  - Anthropic `message_start` 中 `message.usage`、`message_delta` 中顶层 `usage`、cache metrics 均保持。

**风险与回归面**

- serde typed struct 会忽略未知字段；这符合现在只抽 usage/cache 的目标，但必须确保 malformed JSON 仍失败。
- Anthropic 兼容字段较多，容易漏掉 `message.usage` 嵌套路径。
- CPU 降幅主要来自避免 DOM 分配；仍会遍历 JSON tokens。若后续压测仍显示热点，再考虑 `serde_json::value::RawValue` 或自研 usage-object scanner，但不在首轮引入新 runtime dependency。

**测试/验证**

- 扩展 `tests/stream_pipeline_test.rs`：
  - OpenAI 普通 delta 帧无 usage 时不报错、不分配 DOM语义不可直接断言，但可断言只有 `Data`。
  - OpenAI usage 字段新旧别名都能聚合。
  - Anthropic `message_start.message.usage`、`message_delta.usage`、cache fields 都能产生事件。
  - malformed JSON 仍产生 `ProtocolError`。
- 后续允许编译时运行：
  - `cargo test -p core_gateway --test stream_pipeline_test`
  - 可选微基准：构造 10k 个小 SSE data 帧，比较改前后 parser wall time/alloc 统计；本计划阶段不运行。

**估时**

- 实现 2-3h，测试 1h，回归/修边界 1h。

### Task 2：AccountPlanner 层安全 route cache

**要改文件**

- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/lib.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/proxy_engine_test.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/route_client_test.rs`
- 如测试需要动态返回不同 route plan：`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mock_control_plane.rs`

**修法**

- 在 `AccountPlannerInner` 增加：
  - `cache_ttl: Duration`
  - `cache: DashMap<RouteCacheKey, CachedRoutePlan>`，复用当前 crate 已有 `dashmap` dependency。证据：`Cargo.toml:27`。
- `RouteCacheKey` 来自 `build_route_query()` 的稳定路由维度：
  - `tenant_id`
  - `requested_model`
  - `session_hash`
  - `request_protocol`
  - `stream`
  - 当前 `previous_attempts` 和 `capability_hints` 为空；未来若非空，默认 bypass cache，避免失败重试路径复用旧选择。
  - 不包含 `request_id`，否则每个请求 key 都不同，缓存无效。
- 缓存 eligibility：
  - 本地 `cache_ttl > 0`。
  - control plane 返回 `route_plan.route_ttl_ms > 0`。`route_ttl_ms == 0` 表示不缓存。
  - `upstream_auth.expires_at_unix_ms == 0` 时只受 `route_ttl_ms` 和本地 TTL 限制；非 0 时缓存过期不得晚于 `expires_at_unix_ms - skew`。
  - 建议 `skew = min(1000ms, ttl / 10)`，最小 100ms，避免临界过期凭据被命中。
- 命中流程：
  - 读取 `CachedRoutePlan`，若 `expires_at_ms > now + skew`，clone `RoutePlan` 后调用现有 `planned_attempt(plan)`，从而每次命中仍生成新的本地 `attempt_id`。证据：`account_planner.rs:257-265`。
  - 若命中但过期或 `planned_attempt()` 因 credential 过期失败，立即删除该 key，重新查 control plane 一次。
- 未命中流程：
  - 调用 `route_client.query_route(query).await`。
  - 对返回 plan 先 `planned_attempt(plan.clone())` 做验证；成功后按上面的过期策略写入 cache。
  - 不缓存 control-plane 错误、不缓存 invalid route plan。
- `RouteClientOptions.route_cache_ttl`：
  - 首轮不要在 `RouteClient` 传输层实现 cache，因为 `RouteClient` 不知道 handler 语义，容易在 raw RPC 层复用含 per-attempt 凭据的 plan。
  - 保留字段但文档化为 deprecated/unused，或在 `lib.rs` 构建 route client 时继续传入 0；真正可用 cache 只由 `AccountPlanner::new(..., cache_ttl)` 控制。
- 删除或改写 `log_route_plan_cache_disabled()`：当前日志声称 cache disabled，和新行为冲突。证据：`lib.rs:69-75`、`lib.rs:126-137`。

**缓存失效策略与一致性窗口**

- 数据面只做短 TTL 被动失效，不新增 control-plane 主动 invalidation schema，避免高风险协议/schema 改动。
- 一致性窗口上限为 `min(HUAKAI_ROUTE_CACHE_TTL_MS, route_plan.route_ttl_ms, upstream_auth_expiry_remaining - skew)`。
- control plane 若要立即撤销某账号或凭据，短期安全路径是返回 `route_ttl_ms=0` 或把本地 `HUAKAI_ROUTE_CACHE_TTL_MS=0` 作为部署开关；主动 invalidation channel 放入后续 roadmap，不在本次改 proto。
- 默认建议继续 `HUAKAI_ROUTE_CACHE_TTL_MS=0`，生产压测确认收益后由 Owner/运维显式开启 250-1000ms 级短 TTL。

**风险与回归面**

- 复用 `acquisition_token` 可能影响 attempt report 归因；proto 显示 attempt report 会携带 `acquisition_token`。证据：`proto/route.proto:77-82`、`attempt_reporter/types.rs:145-160`。因此只能在 control plane 明确给 `route_ttl_ms > 0` 时复用。
- 控制面更新到数据面生效存在 TTL 窗口；必须在配置说明和日志中显式展示。
- 如果未来 `previous_attempts` 非空用于 failover，缓存必须 bypass，否则会错误复用首次选择。

**测试/验证**

- 改写 `tests/proxy_engine_test.rs:127-174` 附近的 planner cache 测试：
  - TTL 0：两次 `plan()` 查询 control plane 2 次。
  - TTL >0 且 `route_ttl_ms >0`：同 key 两次 `plan()` 查询 control plane 1 次，两个 `attempt_id` 不同。
  - TTL 过期后再次查询 control plane。
  - `route_ttl_ms=0` 即使本地 TTL >0 也不缓存。
  - `upstream_auth.expires_at_unix_ms` 已过或临近过期时不命中 cache。
- 保持 `tests/route_client_test.rs` 对 raw `RouteClient` 的预期：`RouteClient` 不做 planner cache；或改名说明“route client transport cache intentionally absent”。
- 后续允许编译时运行：
  - `cargo test -p core_gateway --test proxy_engine_test account_planner`
  - `cargo test -p core_gateway --test route_client_test route_cache`

**估时**

- 实现 3-5h，测试 2-3h，文档/日志调整 0.5h。

### Task 3：敏感 header 判断去分配

**要改文件**

- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/redaction.rs`

**修法**

- 将 `is_sensitive_header(name)` 改为零分配比较：
  - `SENSITIVE_HEADERS.iter().any(|s| name.eq_ignore_ascii_case(s))`
- 保持 `SENSITIVE_HEADERS` 小数组，不引入 HashSet/phf。
- 本项只修 header 名称判断；`is_prompt_body_content_type()` 和 secret token 清洗中的 lowercase 分配属于另一个热点，不混入本次范围。

**风险与回归面**

- HTTP header 名大小写不敏感，`eq_ignore_ascii_case` 符合现有测试语义。
- 非 ASCII 输入只做 ASCII case-insensitive；HTTP header name 本身应是 ASCII token，风险低。

**测试/验证**

- 现有 `redaction.rs` 单元测试覆盖 Authorization/X-Api-Key/Cookie 大小写和非敏感 header。证据：`redaction.rs:218-241`。
- 增加一个长非敏感 header 名测试，确保不会误报。
- 后续允许编译时运行：
  - `cargo test -p core_gateway redaction`

**估时**

- 实现 10min，测试 10min。

### Task 4：进程级连接数和 in-flight 请求上限

**要改文件**

- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/lib.rs`
- 新增 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/resource_limits.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/listener_test.rs`

**技术选型**

- 请求级：自研 axum middleware + `tokio::sync::Semaphore`，不使用 Tower `LoadShedLayer/GlobalConcurrencyLimitLayer` 作为首选，因为当前 `tower` 是 optional runtime dependency，启用 `limit/load-shed` feature 会扩大依赖面。证据：`Cargo.toml:54-55`。
- 连接级：实现 axum 0.8 的 `serve::Listener` wrapper。axum 的 Listener trait 允许自定义 `accept()` 和 IO 类型。证据：本地 axum 源 `axum-0.8.9/src/serve/listener.rs:8-24`。
- 503 返回点：只在请求级 middleware fail-fast；连接级 limit 负责限制已接受连接/FD，不承诺 HTTP 503，因为超过连接上限时请求尚未进入 HTTP handler。

**修法**

- 配置新增：
  - `HUAKAI_MAX_CONNECTIONS`，默认建议 4096，必须 >0。
  - `HUAKAI_MAX_IN_FLIGHT_REQUESTS`，默认建议 1024，必须 >0。
  - 可选 `HUAKAI_OVERLOAD_RETRY_AFTER_SECS`，默认 1。
- `GatewayState` 增加 `resource_limits: Arc<ResourceLimits>`，内含：
  - `connection_semaphore: Arc<Semaphore>`
  - `in_flight_semaphore: Arc<Semaphore>`
  - limits 数值，用于日志和测试。
- 业务 router layer 顺序：
  - drain 仍最外层，保证 drain 模式不消耗 overload permit。
  - overload middleware 在 RequestBodyLimit 之前，过载时直接 503，不读取大 body。
  - 保持 `/healthz` 和 `/metrics` 不受业务 in-flight limit，便于观测和 LB 探活。
- middleware 行为：
  - 从 request header 读取/生成 `RequestId`。
  - `try_acquire_owned()` 成功：把 permit 持有到 response future 完成。
  - 失败：返回 `503 Service Unavailable`，JSON `{"error":"overloaded","request_id":"..."}`，header `Retry-After: <secs>` 和 `Connection: close`。
  - 记录 warn/metrics hook；若 metrics 当前没有相关 counter，可先加低风险 counter 或日志，避免扩大 scope。
- `LimitedListener` 行为：
  - `accept()` 先获取 connection permit，再 accept TCP。
  - 返回的 `TrackedIo<TcpStream>` 持有 `OwnedSemaphorePermit`，连接任务 drop 时自动释放。
  - accept error 时释放 permit 并按 axum 当前策略 sleep/retry。

**风险与回归面**

- 请求级 limit 对长流式请求计入整个响应生命周期；这符合保护宿主的目标，但会降低长连接高并发吞吐，需要配置化。
- 连接 limit 如果设置过低，会造成 TCP backlog 排队或连接失败；这是资源保护预期，需要运维明确。
- middleware layer 顺序错误会导致 drain 或 body limit 语义回归，必须用测试锁定。

**测试/验证**

- `listener_test.rs` 增加：
  - `max_in_flight_requests=1`，构造一个慢 upstream 占住 permit，第二个业务请求立即 503，body 包含 `overloaded`，上游未收到第二个请求。
  - `/healthz` 和 `/metrics` 在业务 overloaded 时仍可返回。
  - drain=true 时返回现有 drain 503，且不消耗 in-flight permit。
  - connection limit 可用 TCP 客户端打开 N 个 keep-alive 连接，第 N+1 个不进入业务；该测试避免依赖精确 503，只验证进程不接受超过上限的活动连接。
- 后续允许编译时运行：
  - `cargo test -p core_gateway --test listener_test overload`

**估时**

- 请求级 middleware 2-3h；连接 listener wrapper 3-5h；测试 3h。

### Task 5：SSE frame size 本地 hard cap

**要改文件**

- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/proxy_engine_test.rs` 或新增更小的 unit test module

**修法**

- 新增本地 hard cap 常量：
  - 首轮建议 `LOCAL_MAX_STREAM_FRAME_BYTES = DEFAULT_MAX_FRAME_BYTES`，也就是 64KiB，保持现有默认资源边界。
  - 若 Owner 后续要求支持更大 SSE frame，再新增受 validate 约束的配置项，而不是完全信任 control-plane plan。
- `route_stream_frame_limit(max_stream_frame_bytes)` 改为：
  - 0 -> `DEFAULT_MAX_FRAME_BYTES`
  - 非 0 -> `min(usize::try_from(value).unwrap_or(LOCAL_MAX_STREAM_FRAME_BYTES), LOCAL_MAX_STREAM_FRAME_BYTES)`
- 可选增加 warn：当 route plan 值超过本地 hard cap 时记录 clamp，但不要把原值暴露给客户端。

**风险与回归面**

- 如果控制面当前依赖大于 64KiB 的 frame，此改动会让 stream parser 更早报 protocol error；但这是资源安全目标要求的本地边界。
- 过小 route plan 值仍可降低上限，可能导致控制面误配置影响请求；这是现有能力，不扩大风险。

**测试/验证**

- 单元测试：
  - `route_stream_frame_limit(0) == 64KiB`
  - `route_stream_frame_limit(1024) == 1024`
  - `route_stream_frame_limit(u64::MAX) == LOCAL_MAX_STREAM_FRAME_BYTES`
  - `route_stream_frame_limit(LOCAL_MAX_STREAM_FRAME_BYTES as u64 + 1)` 被 clamp。
- 集成测试：
  - 控制面返回超大 `max_stream_frame_bytes`，实际 parser 仍按 64KiB 报 oversized frame。
- 后续允许编译时运行：
  - `cargo test -p core_gateway --test proxy_engine_test stream_frame`
  - `cargo test -p core_gateway --test stream_pipeline_test sse_scanner_reports_oversized_partial_line`

**估时**

- 实现 20min，测试 40min。

### Task 6：长 reasoning 兼容 + server/read/write/idle timeout 补齐

**要改文件**

- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/lib.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs`
- 可能新增 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/server_runtime.rs`
- 可能新增 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/body_timeout.rs`
- 可能修改 `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml` 的 `hyper-util` features
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/listener_test.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/proxy_engine_test.rs`

**修法**

- 上游响应 body idle：
  - 把 `BODY_IDLE_TIMEOUT = 30s` 改为配置值。
  - 新增 `HUAKAI_UPSTREAM_BODY_IDLE_TIMEOUT_MS`，默认建议 300_000ms。
  - 通过 `ProxyEngine` 持有 `ProxyTimeouts`，传给 `relay_body()`，替代硬编码常量。
  - long reasoning 请求 30s 无 token 不再被掐断，但仍有 5min 保护。
- 下游写 idle：
  - 当前 relay task 向 bounded channel `sender.send(Ok(data)).await`，如果客户端停读可长期卡住。证据：`proxy_engine/relay.rs:88-90`、`proxy_engine/relay.rs:140-151`。
  - 给 `sender.send()` 加 `tokio::time::timeout(downstream_write_idle_timeout, ...)`。
  - 新增 `HUAKAI_DOWNSTREAM_WRITE_IDLE_TIMEOUT_MS`，默认建议 60_000ms。
  - 超时后 report `ClientCancel` 或 `Timeout` 需要统一：建议 `ClientCancel` + error_class `client_slow_or_disconnected`，因为上游不是故障。
- 请求 body read idle：
  - 新增 `HUAKAI_REQUEST_BODY_IDLE_TIMEOUT_MS`，默认建议 30_000ms。
  - 在 listener 将 request 交给 proxy 前包装 `Body`，若上传 body 两帧之间超过阈值，返回/中断为超时错误，避免 slowloris 打满 upstream 和 runtime。
  - 若实现复杂度过高，第一阶段至少在 proxy client request body wrapper 内返回 body error，让 upstream request fail closed；第二阶段再把错误映射为清晰 408/503。
- server hyper knobs：
  - 当前 `axum::serve` 内部创建 hyper-util auto builder，但本地 `run()` 无法设置 header read/h2 keepalive。证据：`lib.rs:201`；本地 axum 源 `axum-0.8.9/src/serve/mod.rs:389-396`。
  - 若 Owner 接受 feature 扩展，把 `hyper-util` direct dependency features 增加 `server-auto`, `server-graceful`, `service`，这些是现有 crate 的 feature，不是新增 crate。证据：本地 `hyper-util-0.1.20/Cargo.toml:80-107`。
  - 新增 `server_runtime::serve(listener, router, limits, timeouts)`，自建 accept loop：
    - 使用 `hyper_util::server::conn::auto::Builder::new(TokioExecutor::new())`。
    - `builder.http1().header_read_timeout(Some(config.server_header_read_timeout))` 并设置 `TokioTimer`。
    - `builder.http2().keep_alive_interval(Some(config.http2_keep_alive_interval))`、`keep_alive_timeout(config.http2_keep_alive_timeout)` 并设置 `TokioTimer`。
    - 集成 `LimitedListener` 的 connection permit。
  - 若不接受 feature 扩展，保留 `axum::serve`，只实施 request/relay timeout，并把 server-level timeout 标为 Mandatory Roadmap，因为 axum 当前 API 不暴露完整配置。

**风险与回归面**

- 自定义 serve loop 是本轮最大风险，涉及 HTTP/1/2 upgrade、graceful shutdown、测试 helper 与生产 `run()` 差异。
- 下游写超时太短会误伤慢客户端；默认应比正常慢读测试宽松。
- 上游 body idle 默认从 30s 改到 300s，会让坏 upstream 占用资源更久；必须与全局 in-flight limit 一起落地。

**测试/验证**

- long reasoning：
  - mock upstream 首帧后 sleep 45s 再发下一帧；配置 `UPSTREAM_BODY_IDLE_TIMEOUT_MS=60_000` 时成功，配置 30_000 时超时。为了测试时长，可用 100ms/300ms 小值模拟。
- downstream write：
  - 客户端发请求后不读 response，mock upstream 持续产出；确认 relay 在 `DOWNSTREAM_WRITE_IDLE_TIMEOUT_MS` 后停止，不无限读 upstream。
- request body read idle：
  - TCP client 发送 headers 和少量 body 后停顿超过阈值；确认连接被关闭/请求失败，listener 仍健康。
- server header read：
  - TCP client 只连接不发完整 headers，超过 `SERVER_HEADER_READ_TIMEOUT_MS` 后连接关闭。
- 后续允许编译时运行：
  - `cargo test -p core_gateway --test listener_test timeout`
  - `cargo test -p core_gateway --test proxy_engine_test timeout`

**估时**

- relay/config timeout 3-4h；request body wrapper 3-5h；custom server runtime 5-8h；测试 4-6h。

## 整体执行顺序与依赖

1. 先做低风险性能修复：Task 3 redaction 去分配。无依赖，快速降低热点。
2. 做 Task 5 frame hard cap。无外部依赖，先建立资源安全硬边界。
3. 做 Task 1 SSE typed extraction。只影响 parser，可用现有 golden tests 锁行为。
4. 做 Task 4 请求级 in-flight 503 middleware。它是 Task 6 放宽 body idle 到 300s 的前置保护。
5. 做 Task 6 relay/request timeout 中不需要 custom server loop 的部分：上游 body idle 配置、下游写 idle、请求 body idle。
6. 决策点：Owner 确认是否启用 `hyper-util` server features。确认后再做 Task 6 custom server runtime；不确认则记录为 Mandatory Roadmap。
7. 做 Task 2 planner route cache。放在资源保护之后，避免先提高 control-plane/cache 吞吐再暴露无全局限流窗口。
8. 最后跑定向测试，再跑 crate 级测试。由于本计划任务禁止 cargo，本轮不执行。

## Pre-execution checklist

- 确认本计划与 Claude 独立计划交叉讨论后的合成版本。
- 确认是否允许修改 `Cargo.toml` 的 `hyper-util` feature 列表；这是 medium risk，不是新增 crate，但会改变 server runtime 编译面。
- 确认 route cache 默认仍为 0ms 关闭，生产开启由部署配置控制。
- 确认本次不改 proto/schema；主动 route invalidation 只作为后续 roadmap。
- 确认所有新增配置都有默认值、validate、测试 env helper 更新。

## Verification plan

本计划阶段按 Owner 要求不运行 cargo。后续实现阶段建议按顺序运行：

1. `cargo test -p core_gateway redaction`
2. `cargo test -p core_gateway --test stream_pipeline_test`
3. `cargo test -p core_gateway --test proxy_engine_test stream_frame`
4. `cargo test -p core_gateway --test listener_test overload`
5. `cargo test -p core_gateway --test listener_test timeout`
6. `cargo test -p core_gateway --test proxy_engine_test timeout`
7. `cargo test -p core_gateway --test proxy_engine_test account_planner`
8. `cargo test -p core_gateway --test route_client_test route_cache`
9. 最后再按磁盘情况运行 `cargo test -p core_gateway`。

## 需要 Owner 确认

- 是否接受 `hyper-util` feature 扩展并替换生产 `run()` 的 `axum::serve` 为自定义 serve loop。没有这一步，server-level header/h2 keepalive timeout 无法完整配置。
- route cache 是否默认保持关闭。我的建议是保持 `HUAKAI_ROUTE_CACHE_TTL_MS=0` 默认，只在压测/生产明确设置短 TTL。
- 本地 stream frame hard cap 首轮是否固定 64KiB。我的建议是先固定 64KiB，后续确有模型/供应商需要时再加受限配置。

## 读过的源码文件清单

- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/stream_pipeline/openai.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/stream_pipeline/anthropic.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/stream_pipeline/mod.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/stream_pipeline/sse.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/redaction.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/lib.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/listener.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/main.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/error.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/auth.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/types.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mock_control_plane.rs`
- `exploratory/rust-core-gateway/merged/proto/route.proto`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/stream_pipeline_test.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/proxy_engine_test.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/listener_test.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/route_client_test.rs`
- 本地依赖源码用于可行性核实：`axum-0.8.9/src/serve/listener.rs`、`axum-0.8.9/src/serve/mod.rs`、`hyper-util-0.1.20/Cargo.toml`、`hyper-util-0.1.20/src/server/conn/auto/mod.rs`、`hyper-1.9.0/src/server/conn/http1.rs`、`hyper-1.9.0/src/server/conn/http2.rs`。

Agent: Codex / GPT-5

UTC timestamp: 2026-05-21T02:53:45Z

