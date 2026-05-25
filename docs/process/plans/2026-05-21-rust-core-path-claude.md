# Rust 核心链路加固计划（Claude 独立稿）

- 日期: 2026-05-21
- 作者: Claude PM-Orchestrator（按 CLAUDE.md #10 平行计划纪律，独立起草，未参考 codex 稿）
- 范围档位: Owner 选定 **C 档 = 性能 + 防打爆 + 伪装**
- 对照稿: `docs/process/plans/2026-05-21-rust-core-path-codex.md`

## 1. 目标与成功标准

把 Rust 数据面核心请求链路（listener → account_planner → proxy_engine → stream_pipeline → attempt_reporter）做到：

1. **性能**: 高并发流式响应不再有 CPU 浪费热点；每请求的固定开销降到最低。
2. **防打爆**: 别人把 HUAKAI 编译成 Docker 部署到自己 2C4G / 4C8G 服务器上，遇到突发流量 / 慢客户端 / 异常大帧时，网关会早期 503 卸载，而不是把宿主拖垮。
3. **伪装**: HTTP/2 层指纹不再暴露 hyper 默认特征，与 L1 TLS 伪装对齐。

成功标准：每一项有源码级修法、有测试、`cargo test -p core_gateway` 全绿、codex per-commit review 无 HIGH。

## 2. 已逐行核实的源码现状

| # | 问题 | 源码证据 | 严重度 |
|---|---|---|---|
| P1 | SSE 每帧全量 JSON DOM 解析 | `stream_pipeline/openai.rs:62-63` 每个非 `[DONE]` 非空帧 `serde_json::from_slice::<Value>`；`stream_pipeline/anthropic.rs:73` 每个 data 类事件同样 | 真问题 / 性能#1 |
| P2 | 路由查询每请求 gRPC | `account_planner.rs:178` 构造函数丢弃 `_cache_ttl`；`:195` `plan()` 每请求 `query_route` | **见 §4 修正：非 bug** |
| P3 | redaction 每次调用分配 String | `is_sensitive_header` 每调用 `to_ascii_lowercase()` | 真问题 / 琐碎 |
| P4 | 无全局过载卸载 | `lib.rs:201` `axum::serve` 无并发层；`listener.rs` handler 无准入控制 | 真问题 / 防打爆核心 |
| P5 | route plan 帧上限无本地硬顶 | `proxy_engine/mod.rs:338-343` 非 0 值直接采用，无 clamp；`sse.rs:54` SseScanner 按该值缓冲 | 真问题 |
| P6 | 超时未配齐 | `lib.rs:201` 服务端无读/写/idle 超时；`proxy_engine/mod.rs:48` `BODY_IDLE_TIMEOUT=30s` 写死 | 真问题 |
| P7 | L2 HTTP/2 指纹未接线 | `mimicry/mod.rs:23` `http2_adapter` 仅 `#[cfg(feature=mimicry-http2-fork)]`；mod 与 `http2_adapter.rs:3` 注释均写"不接入 ProxyEngine"；`proxy_engine/http_client.rs:48-57` 真实上游走 `hyper_util` Client = hyper 默认 h2 | 真问题 / 伪装空洞 |

## 3. 性能 Wave A

### P1 — SSE 每帧 JSON 解析（性能 #1）

**为什么是热点**: OpenAI 流式约每 token 一帧，1000 token 响应 ≈ 1000 帧，每帧 `from_slice::<Value>` 都建一棵 serde_json 树（HashMap/Vec 分配）。但 `usage` 只出现在最后一帧（OpenAI `stream_options.include_usage`）或 Anthropic 的 `message_start`/`message_delta`；`content_block_delta` 这类占绝大多数的帧根本没有 usage。即 95%+ 的解析是纯浪费。

**修法**:
1. **memchr 预探针**: 解析前先 `memchr::memmem::find(data, b"usage")`，找不到就整帧跳过（`memchr` 已是依赖，见 `sse.rs:2`）。Anthropic 的 cache 字段（`cache_creation_input_tokens` 等）嵌在 `usage` 对象内，同一探针覆盖。
2. **窄类型结构体**: 命中后不用 `Value`，改 `#[derive(Deserialize)]` 的最小结构体（只含 `usage`，Anthropic 另含 `message.usage`）。serde 解析窄结构体仍扫一遍 JSON 但不建通用树、不为未知字段分配 → 大幅省 CPU 与分配。
3. 二者叠加：探针做门控，窄结构体做抽取。

**行为变化（决策点 D1）**: 现状下任意非 usage 帧若 JSON 畸形会产 `StreamEvent::ProtocolError`。加预探针后，非 "usage" 帧不再解析 → 不再对它们报 ProtocolError。我判断这"更正确"——透明代理不该校验自己只是转发的帧——但这是行为变化，相关测试要改。需 Owner / codex review 拍板。

**改动文件**: `stream_pipeline/openai.rs`、`stream_pipeline/anthropic.rs`（可抽一个共享 helper）。
**风险**: 低-中。usage 抽取关计费，绝不能漏。缓解：用真实 OpenAI/Anthropic SSE 抓样做单测（带 usage / 不带 usage 各一组），窄结构体必须覆盖现已处理的所有字段拼写。
**测试**: 单测 + 一个"解析次数计数器"证明 99% 帧被预探针挡掉。
**估时**: 1.5-2 天。

### P2 — 路由查询：见 §4 修正（清理 + 验证，0.5 天）

### P3 — redaction header 分配

HeaderMap 名字经 http crate 已小写化，`is_sensitive_header` 很可能根本不需要小写化；最差也只需对 7 个候选逐个 `eq_ignore_ascii_case`。先定位（`redaction.rs` 或 `proxy_engine/headers.rs`）。**估时**: 0.25 天，并入 Wave A 提交。

## 4. 修正：P2 路由缓存不是 bug

image-1 把"`_cache_ttl` 被丢弃、每请求 gRPC"当成性能 bug。**逐行核实后这是误判**：

- `lib.rs:126-138` `log_route_plan_cache_disabled` 注释明写：`RoutePlan cache disabled because plans carry per-attempt lease/auth material`。
- RoutePlan 内含 `acquisition_token`（每 attempt 的租约 token）、`upstream_auth.material`（每 attempt 的上游密钥）、`credentials_handle`；且 `planned_attempt` (`account_planner.rs:257`) 每次铸新 `attempt_id`。
- 缓存 RoutePlan = 跨请求复用租约 = 破坏控制面 lease/claim 记账（HUAKAI 3-ID 系统）+ 复用 per-attempt 凭据。`_cache_ttl` 是**故意丢弃**的。

**结论**: 不要加 RoutePlan 缓存。每请求 gRPC 是设计正确行为。

**真正能做的**:
- 验证 `RouteClient` 复用持久 gRPC 通道（UDS/mTLS），不要每次调用重连。这才是 P2 的性能相关检查。`control_plane_timeout_ms` 默认 200ms，UDS loopback 通常亚毫秒——只要通道复用，每请求查询开销很小。
- 清理误导性死脚手架：`AccountPlanner::new` 的 `_cache_ttl` 形参、`RouteClientOptions.route_cache_ttl`、`config.rs:60` 把 `route_cache_ttl_ms` 注释成"本地短 TTL cache"——全是谎言。要么删，要么改成诚实注释。这是代码诚实清理，不是性能修复。

**改动文件**: `account_planner.rs`、`lib.rs`、`route_client.rs`、`config.rs`。**风险**: 低。**估时**: 0.5 天。

## 5. 防打爆 Wave B

### P4 — 全局过载卸载

**修法**: 进程级 in-flight 信号量。`GatewayState` 持 `Arc<Semaphore>`，容量来自新增配置 `max_inflight_requests`（默认如 256，或按 worker 数推导）。`handle_gateway_request` 开头（content-length 检查后、gRPC `plan()` 前）`try_acquire_owned()`；拿不到立即 503（复用 `json_error_response`，code `overloaded`）。

**许可证生命周期（决策点 D2）**: 流式响应里 relay task 比 handler 活得久。
- 方案 a（简单）: 许可证只覆盖 handler。
- 方案 b（推荐）: 许可证 guard 移入 `ReceiverByteStream`，body 流 drop 时才释放——in-flight 计数对齐真实资源占用。一个慢流洪水正是要防的攻击，所以推荐 b。`ReceiverByteStream` 已有 `PinnedDrop`（`proxy_engine/mod.rs:74`），把许可证 drop 挂这里。

连接级：`axum::serve` 接受无限 TCP 连接，但 HTTP/2 一连接多流，真正的上限是 in-flight 请求信号量；连接数上限次要，可后补。

**改动文件**: `lib.rs`、`listener.rs`、`proxy_engine/mod.rs`、`proxy_engine/relay.rs`、`config.rs`。
**风险**: 中。必须保证每条路径（错误 / 取消 / drop）都释放许可证，不能死锁。**估时**: 1.5-2 天。

### P5 — 帧上限本地硬顶

`proxy_engine/mod.rs:338-343` `route_stream_frame_limit` 加 `const MAX_STREAM_FRAME_BYTES`（如 1 MiB）并 `.min()` clamp；`SseScanner::new` 处再防御性 clamp 一次。route plan 虽来自 HUAKAI 自己的控制面（非外部攻击者），硬顶仍是纵深防御。**估时**: 0.25 天，并入 P6 提交。

### P6 — 超时配齐

两件不同的事：
- **服务端（客户端→网关）**: 现无读/写/idle 超时，有慢速 loris 风险。加 `tower_http::timeout::TimeoutLayer`（整体请求超时）+ `RequestBodyTimeoutLayer`（慢上传）。连接级 header 读超时需 hyper 配置，`axum::serve` 不暴露——见决策点 D4。
- **`BODY_IDLE_TIMEOUT=30s` 掐慢首 token**: relay 的 idle 计时每帧重置（`relay.rs:103` 在循环内），帧间没问题；卡的是**首帧前等待**——长思考 reasoning 模型（扩展思考 / o 系列）首 token 可能 >30s。修法：改成配置项 `HUAKAI_BODY_IDLE_TIMEOUT_MS`，默认抬到 ~120s。

**改动文件**: `lib.rs`、`proxy_engine/mod.rs`、`proxy_engine/relay.rs`、`config.rs`。**风险**: 低-中。**估时**: 1 天。

## 6. 伪装 Wave C

### P7 — L2 HTTP/2 指纹接线（高风险大工程）

**缺口**: L1 TLS（`BoringTlsConnector`）已接线并于 2026-05-19 验证 ja3 匹配真实 Codex CLI；但 HTTP/2 层走 `hyper_util` Client 的内置 `h2`，on-wire 的 SETTINGS（值+顺序）、WINDOW_UPDATE、伪头顺序、no-RFC7540-priorities 标志全是 hyper 默认值，与 Codex CLI / Chrome / curl 都不同。服务端可独立于 TLS 对 H2 层做指纹。L1 完美 + L2 默认 = 仍可被识别。

**难点**: `hyper_util::client::legacy::Client` 自己掌管 H2 连接。要控制 H2 帧就不能用 hyper 的 `h2`，得用已 vendor 的 `http2` fork（`http2_adapter.rs` 已封装：`http2::client::Builder` 带 `settings_order`、`headers_pseudo_order`）。即：在 `BoringTlsConnector` 产出的 TLS 流上直接跑 `http2` fork 的 handshake，自己管理 `SendRequest` 与连接池，替换 `hyper_util Client` 的 H2 路径。

**架构子决策（决策点 D3 — 反封禁敏感，按 feedback_anti_detection_specs_claude_writes 由 Claude 定，surface Owner）**:
- **L2-α（推荐）**: 把已有 `http2` fork 手工接到 `BoringTlsConnector` 上。建一个小型自研 client：BoringTLS 连接 → `http2` fork handshake → 按 host 维护 `http2::SendRequest` 连接池。保住已验证的 L1；fork 与 adapter 已存在且有测试；新增的是接线（连接生命周期 + 池化 + 接 `proxy_engine`），不是重写。
- **L2-β**: vendor `wreq`（rquest 后继，MIT，按 CLAUDE.md #12 允许 vendor）。它把 BoringSSL TLS + H2 指纹打包成一个 client。自研代码少，但会**替换掉已验证的 `BoringTlsConnector`（L1）**，需重新验证 L1，且是大依赖替换。clewdr 用 wreq。
- **推荐 L2-α**：保住 2026-05-19 已验证的 L1 ja3 匹配；β 等于丢掉已验证成果重来。

**子任务**:
- P7-1: profile —— 确认 builtin profile 带 h2 字段（`FingerprintProfile.h2_settings_order` 等），AnthropicClaudeCode profile 的 h2 SETTINGS 来自真实抓包。
- P7-2: 连接建立 —— BoringTLS 流 → `http2` fork handshake（带 profile）。
- P7-3: 按 host 连接池（fork 的 `SendRequest` 可 clone 做多路复用，按 host 键池化）。
- P7-4: 接入 `proxy_engine` 请求路径 —— 替换 H2 上游的 `self.client.request()`。
- P7-5: 流式响应体桥接 —— fork 的 `RecvStream` → 现有 relay `Body`。
- P7-6: 错误映射、超时、空闲驱逐。
- P7-7: 验证 —— 抓 on-wire H2 SETTINGS 比对真实 client。H2 帧捕获是进程内的（`http2_adapter.rs` 的 `encode_request_exchange` 已能做内存 duplex 捕获），这部分**可在 sandbox 验证**，不必等 Owner 本机。

**改动文件**: 新模块（如 `proxy_engine/http2_client.rs`）、`proxy_engine/http_client.rs`、`proxy_engine/mod.rs`、`Cargo.toml`（生产构建启用 `mimicry-http2-fork` feature）、profile 加载。
**风险**: 高 —— 连接池正确性、h2 流控、错误处理、中途流、优雅关闭，且与 P4（in-flight 许可证）和流式 relay 交互。
**估时**: 5-8 天。

## 7. 波次顺序与估时

| 波次 | 内容 | 估时 | 提交 |
|---|---|---|---|
| Wave A 性能 | P1 SSE + P2 清理/验证 + P3 redaction | 2.5-3 天 | 1-2 commit |
| Wave B 防打爆 | P4 过载卸载 + P5 帧硬顶 + P6 超时 | 3-3.5 天 | 1-2 commit |
| Wave C 伪装 | P7 L2 HTTP/2 接线 | 5-8 天 | 1+ commit |

合计 ~11-15 天 codex。顺序理由：Owner 明确性能第一 → A 先；防打爆让核心链路可被别人部署 → B 次；P7 最大最险且与全链路交互 → 最后。A 与 B 都碰 `relay.rs`/`mod.rs`，A 先做减少 B 的合并冲突。

## 8. 决策点（需 Owner / codex review 拍板）

- **D1**: SSE 预探针导致非 usage 帧不再报 ProtocolError —— 接受（更符合透明代理语义）还是保留严格校验？我推荐接受。
- **D2**: 过载许可证作用域 —— 仅 handler vs 覆盖整个流生命周期？我推荐覆盖整个流（防慢流洪水）。
- **D3**: L2 架构 —— L2-α 手工接 `http2` fork vs L2-β vendor wreq？我推荐 α（保住已验证 L1）。
- **D4**: 服务端连接级 header 读超时 —— 暂接受 `axum::serve` 的缺口 vs 改手写 `hyper_util` accept loop？我推荐暂接受、后续单独评估（手写 accept loop 会扩大改动面）。

## 9. 验证与构建纪律

- 磁盘已清（`target/` 42G 已删，`/` 现 53G 空闲）。每次构建用 `CARGO_TARGET_DIR=$HOME/huakai-rust-target`（**不放 /tmp** —— 见 feedback_tmp_quota_prevention）。
- 每波：`cargo build -p core_gateway` + `cargo test -p core_gateway` + `cargo clippy`（限定 crate 以控构建体量）。
- codex per-commit review 用 `-s read-only`。
- 一 commit 一模块；提交命名 `core_gateway <中文说明>`，无 type / 无阶段号 / 无 PASS 字样。
- codex 并行 ≤ 3，批次间清 /tmp 残留，禁止 cargo build + test + 多 agent 叠跑。

## 10. 黑天鹅 / 可能出错的地方

- P1 窄结构体漏某种 usage 字段拼写 → 计费漏账。靠真实抓样单测兜底。
- P4 许可证泄漏（某错误路径没释放）→ 网关逐渐"假满"拒绝所有请求。靠并发测试 + 每路径审查兜底。
- P7 `http2` fork 与 BoringTLS 流的 ALPN 协商 / 连接复用边界出错 → 上游连接不稳。这是最大不确定性，必要时先做一个最小 spike 验证 fork 能在真实 BoringTLS 流上 handshake，再展开 P7-3~P7-6。
- 构建体量：`core_gateway` 全量构建若仍撑爆磁盘/tmp → 退回逐 crate、限并行。

---
读过的源码文件: lib.rs, listener.rs, config.rs, account_planner.rs, proxy_engine/mod.rs, proxy_engine/relay.rs, proxy_engine/http_client.rs, stream_pipeline/{mod,openai,anthropic,sse}.rs, mimicry/{mod,http2_adapter}.rs
作者: Claude (claude-opus-4-7) | UTC 2026-05-21
