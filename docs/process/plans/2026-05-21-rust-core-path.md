# Rust 核心链路加固 — 综合权威计划

- 日期: 2026-05-21
- 范围档位: Owner 选定 **C 档 = 性能 + 防打爆 + 伪装**
- 合成来源(CLAUDE.md #10 平行计划纪律):
  - Claude 独立稿 `2026-05-21-rust-core-path-claude.md`
  - Codex 独立稿 `2026-05-21-rust-core-path-codex.md`
  - 借鉴项目调研 4 份: L2 HTTP/2(wreq/http2 fork)、SSE 抽 usage(new-api/one-api/sub2api/litellm)、防打爆(envoy-ai-gateway/litellm/kong/llmgateway/helicone)、CLIProxyAPI 全链路
- 状态: 已合成,待 Owner 批准后进入执行

> 【2026-06-02 已更新】本文是旧 `core_gateway` C档加固历史计划。当前数据面方向已定为 C:
> Go `gatewayhttp` 是生产大脑,Rust 已重定位为高性能 + 强伪装出站传输 sidecar；旧
> `core_gateway` 的 listener / RouteService / route_client / mock_control_plane 口径按 C 退役。
> 当前可用状态以 `tls-sidecar`、Go `transport.Factory` sidecar socket 接线和
> `sidecar_client.go` 为准；下文 Wave A/B/C/D 保留为历史风险与资产清单,不是当前生产数据面状态。

## 1. 目标与成功标准

把 Rust 数据面核心请求链路(listener → account_planner → proxy_engine → stream_pipeline → attempt_reporter)做到:性能无浪费热点、别人部署不被流量打爆宿主、HTTP/2 层指纹与 L1 TLS 伪装对齐。每项有源码级修法 + 测试,`cargo test -p core_gateway` 全绿,codex per-commit review 无 HIGH。

## 2. 双计划交叉结论(一致 / 冲突 / 借鉴项目裁决)

### 2.1 一致项(直接采纳)

| 项 | 两份计划一致结论 |
|---|---|
| P1 SSE | 去掉每帧 `serde_json::Value` DOM,改窄类型 `#[derive(Deserialize)]` 结构体 |
| P3 redaction | `is_sensitive_header` 改 `eq_ignore_ascii_case` 零分配 |
| P4 过载 | `tokio::sync::Semaphore` + fail-fast middleware,过载返回 503;自研 middleware 不引 tower load-shed feature |
| P4 流式 | in-flight 许可证必须覆盖整条流(进 ReceiverByteStream 的 PinnedDrop),不能只覆盖 handler |
| P5 帧上限 | 加本地 hard cap,route plan 值只能往下钳不能往上超 |
| P6 超时 | `BODY_IDLE_TIMEOUT` 写死 30s 改成配置项 |
| 默认 | route cache 默认关 (`HUAKAI_ROUTE_CACHE_TTL_MS=0`) |

### 2.2 冲突项 P2 路由缓存 —— 借鉴项目裁决

- **Claude 稿**: 路由计划带一次性租约凭据,缓存绝不能加。
- **Codex 稿**: 当控制面发 `route_ttl_ms>0` 时视为可缓存信号,设计 AccountPlanner 层安全缓存(每次命中重铸 attempt_id、尊重凭据过期、默认关)。
- **CLIProxyAPI 裁决**(`CLIProxyAPI@21fad9db:sdk/cliproxy/auth/selector.go:437-515`、`session_cache.go`): 业界头号账号转 API 中转(33.8k★)的做法是 **凭据与选择彻底解耦** —— 会话亲和缓存里只存账号 ID(稳定、不过期),**绝不存任何凭据**;凭据永远活在权威账号对象里、由独立后台刷新;缓存命中后必须重新校验账号当前可用性,不可用即视为 miss 重选。

  **裁决结果**: Codex 的方向(`route_ttl_ms` 是信号)有道理,但它的机制仍把 `acquisition_token`(租约)放进缓存 —— 这正是 CLIProxyAPI 明确规避的点。HUAKAI 的架构里,租约和账号选择是控制面在一条 gRPC 消息里**捆绑下发**的;数据面没有"权威账号对象 + 后台刷新"那一层(那是控制面的职责),所以每请求 gRPC 查询**就等于** CLIProxyAPI 的"从权威源实时解析凭据"。

  **本轮决定**:
  1. 缓存保持关闭。**不**实现 Codex 的 opt-in 缓存机制(它会缓存租约,与 CLIProxyAPI 实证模式相悖)。
  2. 只做诚实清理: 删 `AccountPlanner::new` 的 `_cache_ttl` 死参数、`RouteClientOptions.route_cache_ttl`、修正 `config.rs:60` 和 `lib.rs` 把缓存说成"短 TTL cache"的假注释/日志。
  3. **Roadmap**: 若将来要为性能做路由缓存,前提是 proto 拆分 —— 把"账号选择"(可缓存)与"租约签发"(必须每请求)拆成两个字段/两步。这是控制面/数据面合约变更,独立决策,不在本轮。

### 2.3 借鉴项目带来的增量(并入计划)

- **P1 增强**: CLIProxyAPI 用 `gjson` 逐帧轻量探测(无 usage 字段立即返回,不全量 unmarshal)—— 与 Claude 稿的 memchr 预探针同思路、实证有效。**采纳 memchr 预探针 + 窄结构体**(比 Codex 稿仅换窄结构体更进一步;memchr 是 SIMD 字节扫描,无 new-api Sunday 算法的偏移表构建/哈希查找开销 —— 定量差幅待 P1 落地后用微基准实测,不在本计划预设倍数)。
- **P4 增强**: 采纳 Codex 稿的连接级 `LimitedListener`(axum 0.8 `serve::Listener` wrapper),与 in-flight 信号量叠加。litellm 实证:计数层(可观测)与卸载层(限制)职责分离 —— 加 Prometheus gauge `huakai_inflight_requests`。
- **P6 增强**: 采纳 Codex 稿的多档超时(上游 body idle / 下游写 idle / 请求 body 读 idle / 服务端 header 读)。llmgateway 实证三层超时分级。
- **P7 确认**: CLIProxyAPI 只做 uTLS(Chrome JA3),HTTP/2 用 Go 标准库裸奔 —— **连 33.8k★ 的头号项目都没做 L2 HTTP/2 指纹**。HUAKAI 做 L2 是真差异化,不是过度工程。
- **防打爆确认**: CLIProxyAPI 无 in-flight 上限、主代理 server 无任何超时 —— HUAKAI 的 Wave B 是直接超越点。
- **新增 Wave D**: CLIProxyAPI 的多阶段静态编译 Dockerfile(builder + alpine,镜像 ~8MB,配置/凭据/日志 volume 挂出)—— Owner 要 HUAKAI 编译成 Docker 部署,而 Rust 网关当前无 Dockerfile。

## 3. Wave A — 性能(~3 天)

按 Codex 稿的低风险优先顺序: P3 → P5 → P1 → P2。

- **P3 redaction 去分配**: `redaction.rs` `is_sensitive_header` 改 `SENSITIVE_HEADERS.iter().any(|s| name.eq_ignore_ascii_case(s))`。详见 codex 稿 Task 3。估时 0.25 天。
- **P5 帧上限硬顶**: `proxy_engine/mod.rs` `route_stream_frame_limit` 加 `LOCAL_MAX_STREAM_FRAME_BYTES = 64 KiB` 并 `.min()` 钳制(route plan 值只能下调)。**决定**: 首轮硬顶 = 64 KiB(= 现默认值)。理由: 配合 P4 in-flight 上限 1024,64KiB×1024≈64MB 缓冲上限,4GB 机器安全;若 1MiB 则最坏 1GB,会打爆小机。详见 codex 稿 Task 5。估时 0.25 天。
- **P1 SSE 去 DOM + memchr 预探针**: `stream_pipeline/openai.rs` + `anthropic.rs`。每帧解析前先 `memchr::memmem::find(data, b"usage")`,找不到跳过整帧;命中才用窄结构体(OpenAI: `prompt_tokens/input_tokens/completion_tokens/output_tokens/total_tokens`;Anthropic: `input_tokens/output_tokens/total_tokens/cache_*` + `message.usage` 嵌套)反序列化。窄结构体字段拼写详见 codex 稿 Task 1。
  - **决定 D1**: 加 memchr 预探针后,非 "usage" 帧不再被解析 → 不再对非 usage 帧报 `ProtocolError`。这是行为变化,但符合透明代理语义(代理不该校验自己只转发的帧),且 CLIProxyAPI 的 gjson 路径同样只对命中帧细解析。采纳。带 usage 的帧若 JSON 畸形仍报 ProtocolError(语义保留)。
  - 估时 2 天(含真实 SSE 抓样单测)。
- **P2 路由死代码清理**: 按 §2.2 决定 —— 删死参数 + 修假注释 + 验证 `RouteClient` gRPC 通道复用(只读核实)。估时 0.5 天。

## 4. Wave B — 防打爆(~4-5 天)

- **P4 过载卸载**:
  - **Owner 2026-05-21 拍板: 两层都做,但全局上限默认关闭** —— `HUAKAI_MAX_IN_FLIGHT_REQUESTS` 与 `HUAKAI_MAX_CONNECTIONS` 默认 0 = 不限制;部署者按自己服务器大小显式设 >0 才启用。`validate` 必须接受 0。理由: 全局上限是部署者的物理保险丝,不该由 HUAKAI 替部署者写死;账号级并发由管理员另设。
  - 请求级: **始终挂一个轻量计数层**(原子 inc/dec,近零开销,喂 `huakai_inflight_requests` gauge);**卸载(503)只在 `HUAKAI_MAX_IN_FLIGHT_REQUESTS > 0` 时启用** —— 配置 >0 时 `GatewayState` 持 `Arc<Semaphore>`,middleware 在 drain_guard 之后、RequestBodyLimit 之前 `try_acquire_owned()`,失败立即 503 `{"error":"overloaded"}` + `Retry-After`,许可证 guard 进 `ReceiverByteStream` PinnedDrop 覆盖整条流;配置 = 0 时只计数不卸载。`/healthz`/`/metrics` 不受限。
  - 连接级: 配置 >0 时启用 `LimitedListener` wrapper,accept 前取连接许可证;= 0 时用原始 listener。
  - 账号级并发: 属控制面 / 管理员职责(业务配额公平),不在本 Rust 核心路径范围;P4 只做宿主物理保护这一层。
  - 可观测: Prometheus gauge `huakai_inflight_requests`(当前在途数,因计数层始终在,故**始终导出**、与卸载开关无关,便于部署者据此决定要不要设上限)+ `huakai_inflight_limit`(0 = 未启用卸载)。
  - 详见 codex 稿 Task 4(默认值按上述 Owner 决定改为 0)。估时 2-2.5 天。
- **P6 超时配齐**: 新增配置 `HUAKAI_UPSTREAM_BODY_IDLE_TIMEOUT_MS`(默认 300000 —— 长 reasoning 安全,P4 已兜底宿主)、`HUAKAI_DOWNSTREAM_WRITE_IDLE_TIMEOUT_MS`(默认 60000)、`HUAKAI_REQUEST_BODY_IDLE_TIMEOUT_MS`(默认 30000)、`HUAKAI_SERVER_HEADER_READ_TIMEOUT_MS`(默认 30000)、`HUAKAI_GATEWAY_TIMEOUT_MS`(默认 300000)。
  - **决定 D4**: 采纳 Codex 稿的自研 serve loop —— 启用现有 `hyper-util` 的 `server-auto/server-graceful/service` features(是已有 crate 的 feature,非新增依赖),用 `hyper_util::server::conn::auto::Builder` 自建 accept loop 才能配 `http1().header_read_timeout()` 等 server knob。理由: 防慢速 loris 是"防打爆"的应有之义,没有 header 读超时则 Wave B 不完整。这是 Wave B 最大风险点,但 Owner 明确要"别人服务器不被打爆"。
  - 详见 codex 稿 Task 6。估时 2-2.5 天。

## 5. Wave C — 伪装(~6.5-10 天,高风险)

### 5.0 L1 TLS 地基核实(Owner 2026-05-21 质疑 → 已专项调研定案)

L1 TLS 后端经专项调研确认: **继续用 patched BoringSSL,不切 OpenSSL / rustls。** 关键依据: 伪装是 wire 逐字节控制,不是底层库同源 —— OpenSSL 客户端 ClientHello 结构性无法重排扩展顺序;Codex CLI profile 已发后量子 key share(X25519MLKEM768 / group 4588),BoringSSL 内建 ML-KEM 而 OpenSSL 的 PQ 依赖部署环境 OpenSSL≥3.5,脆弱。真实资产是 HUAKAI 自维护的 BoringSSL C 层 fork。详见 [[project_l1_tls_boringssl]]。

调研同时发现 2 个与库选择无关的 L1 真实缺口,作为 Wave C 前置任务 **P7-0**:
- **ETM ext 22**: BoringSSL 客户端从不 offer `encrypt_then_mac`(ext 22),而 Codex/Kiro profile 模板含 22。需核实 `boring_wire.rs` 的 Codex 测试是否真复刻了 22,还是被宽松断言放过。若是真缺口,需在 C 层 fork 再补一个 ETM offer patch。估时 0.5 天(核实)+ 不定(若需补 patch)。
- **JA4 未实现**: `ja3_wire.rs` 当前只算 JA3 MD5,JA4(`t13d...`)计算逻辑没做。深层指纹检测越来越多用 JA4。估时 1-1.5 天。
- (可选 roadmap) 评估改用社区维护的 `btls`(wreq 的 boring fork)替自维护 vendored boring,外包 BoringSSL 安全更新维护负担。

**建议**: P7-0 至少先做 ETM 核实 —— 在 L2 接线前确认 L1 地基无洞,再往上盖。JA4 可与 L2 并行或紧随。

### 5.1 L2 HTTP/2 接线

- **D3 已裁决 = 路 α**(借鉴调研确认): 只用 `http2` fork(HUAKAI 已依赖),接到现有 `BoringTlsConnector` 上,自建 H2 连接池。**不**走路 β(vendor wreq)—— 它会替换掉已验证 ja3 的 L1 TLS。证据: wreq 自身把 H2 指纹外包给 `http2` fork(`wreq@68c4a886:Cargo.toml:86`);该 fork 比标准 h2 仅多 7 个指纹控制方法,且 `http2@a33b27e4:examples/akamai.rs` 已演示脱离 hyper、直接在裸 TLS 流上 handshake。
- **子任务**(详见 Claude 稿 §6 P7-1~P7-7):
  - P7-1 profile h2 字段确认 / 数值标定(Chrome/Codex 的 SETTINGS 值+顺序、伪头顺序、priority)。数值表本地 repo 没有 —— 可 vendor `wreq-proto`(Apache-2.0)的预置表,或自抓包标定。
  - P7-2 BoringTLS 流(确认实现 `AsyncRead+AsyncWrite+Unpin`、ALPN 含 `h2`)→ `http2` fork handshake。
  - P7-3 按 (host,port,profile) 自建 H2 连接池(fork 不带池;`SendRequest` 可 clone 多路复用;需检测半死连接 = `SendRequest` 在但 `Connection` 任务已退)。
  - P7-4 接入 proxy_engine 请求路径。
  - P7-5 fork `RecvStream` → relay `Body` 桥接。
  - P7-6 错误映射 / 超时 / 空闲驱逐。
  - P7-7 验证: `encode_request_exchange` 式内存捕获比对 on-wire H2 SETTINGS(进程内,可 sandbox 验证)。
- **坑(借鉴调研提示)**: `Connection` future 必须持续 spawn 驱动;首帧时序 preface→SETTINGS→WINDOW_UPDATE 即指纹,不能插入打乱;fork 版本以 git+SHA 钉死。
- **建议**: P7 展开前先做一个最小 spike —— 验证 `http2` fork 能在真实 BoringTLS 流上完成 handshake,再投入 P7-3~P7-6。

## 6. Wave D — Docker 部署(~0.5 天)

Rust 网关当前无 Dockerfile。按 CLIProxyAPI 模式(`CLIProxyAPI@21fad9db:Dockerfile`)写多阶段:rust builder(musl 静态编译,先拷 `Cargo.toml/Cargo.lock` 预编依赖再拷源码)→ 极小运行镜像(distroless 或 alpine + CA + tzdata);配置/凭据/日志 volume 挂出;build 注入版本号。

## 7. 顺序、估时、提交

| 波次 | 内容 | 估时 | 提交 |
|---|---|---|---|
| Wave A 性能 | P3 → P5 → P1 → P2 | ~3 天 | 1-2 commit |
| Wave B 防打爆 | P4 → P6(含自研 serve loop) | ~4-5 天 | 1-2 commit |
| Wave C 伪装 | P7-0 L1 缺口核实(ETM/JA4) + P7 L2 HTTP/2(先 spike) | ~6.5-10 天 | 1+ commit |
| Wave D 部署 | Dockerfile | ~0.5 天 | 1 commit |

合计 ~14-19 天 codex。A→B→C→D 顺序: Owner 性能第一 → A 先;防打爆让核心链路可被别人安全部署 → B 次;P7 最大最险 → C(先核 L1 地基 P7-0 再接 L2);Docker 收尾。

## 8. 已决定的判断 + 需 Owner 知会

**我已裁决(借鉴调研已给出依据,Owner 可推翻)**:
- D1 SSE 预探针致非 usage 帧不再校验 JSON → 采纳(透明代理语义 + CLIProxyAPI 实证)。
- D2 过载许可证覆盖整条流 → 采纳(两份计划 + litellm 实证一致)。
- D3 L2 架构 = 路 α(http2 fork 接 BoringTLS)→ 采纳(借鉴调研强确认)。
- D4 自研 serve loop 拿 header 读超时 → 采纳(防打爆完整性需要)。
- P2 路由缓存 = 保持关 + 仅清理,不建缓存机制;proto 拆分入 roadmap → 采纳(CLIProxyAPI 实证)。
- P5 帧硬顶 = 64 KiB;P6 上游 body idle 默认 300s。

**Owner 已拍板**:
- P4 防打爆 = 两层都做,全局上限(in-flight + 连接数)默认关闭、部署者显式开启(Owner 2026-05-21);账号级并发归控制面 / 管理员。

**L1 TLS 地基(Owner 质疑 → 专项调研定案)**:
- L1 后端 = patched BoringSSL 不变;否决 OpenSSL(结构性不可控扩展顺序 + PQ 靠环境)、否决 rustls(设计上拒绝暴露伪装控制点)。
- 发现 ETM ext 22 / JA4 两个 L1 缺口 → 新增 Wave C 前置任务 P7-0。总工期相应增至 ~14-19 天 codex。

**需 Owner 知会的风险**:
- Wave B 的自研 serve loop 是本轮最大工程风险(替换 `axum::serve`,涉及 HTTP/1/2 升级、优雅关闭)。
- Wave C 是高风险大工程,L2 profile 数值标定可能需 Owner 本机出站验证。
- 总工期 ~14-19 天 codex,是一个长周期投入。

## 9. 验证与构建纪律

- 磁盘已清(`target/` 42G 已删,`/` 现 53G 空闲)。构建用 `CARGO_TARGET_DIR=$HOME/huakai-rust-target`(不放 /tmp)。
- 每波: `cargo build -p core_gateway` + `cargo test -p core_gateway` + `cargo clippy`。
- codex per-commit review 用 `codex exec review --uncommitted -c sandbox_mode="read-only"`(`-s`/`--sandbox` flag 已不被 review 子命令接受,用 `-c sandbox_mode` 强制只读);一 commit 一模块;命名 `core_gateway <中文说明>`,无 type/无阶段号/无 PASS 字样。
- codex 并行 ≤ 3,批次间清 /tmp 残留,禁止 cargo build + test + 多 agent 叠跑(曾导致 Bash 全挂)。

## 10. 借鉴证据引用(clean-room: 行为引用,无逐字复制)

- `wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d` (Apache-2.0): H2 指纹外包给 http2 fork —— `Cargo.toml:86`;TLS/H2 松耦合 —— `src/client/layer/client.rs:531,555`。
- `http2@a33b27e469434a99105f35670c9970f22112e892` (MIT): 7 个指纹控制方法 —— `src/client.rs`;脱离 hyper —— `examples/akamai.rs`。
- `new-api@20d3e73734527cded251aff23202dfbf5a2582ca`: 字符串预扫描仅用于音频 —— `service/str.go:14-48`、`relay/channel/openai/audio.go:42`。
- `litellm@79b45786719778117debd57e38b9262283431ce2` (MIT): 全局并发信号量 + in-flight 计数分离 —— `litellm/proxy/middleware/in_flight_requests_middleware.py`、`litellm/proxy/hooks/parallel_request_limiter.py`。
- `llmgateway@1146e111a0b9a0fdda0817091cab2c3047fdb043`: 三层超时分级 —— `apps/gateway/src/lib/timeout-config.ts`。
- `CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054` (MIT): 凭据/选择解耦、会话亲和只缓存账号 ID —— `sdk/cliproxy/auth/selector.go:437-515`;token 刷新 singleflight —— `internal/auth/claude/anthropic_auth.go:342`;uTLS 仅 TLS 层、H2 用 Go 标准库 —— `internal/auth/claude/utls_transport.go:115`;无 in-flight 限制 / server 无超时 —— `internal/api/server.go:330`;gjson 逐帧轻量探测 —— `internal/runtime/executor/helps/usage_helpers.go:373`;多阶段静态 Dockerfile —— `Dockerfile`。

---
本计划 lane: reviewer / synthesizer —— Claude (claude-opus-4-7) 综合 Claude 与 Codex 两份独立平行稿 + 5 份 specifier-lane 调研报告而成;Claude 本人未直接读取第三方参考项目源码。

§10 所有参考项目行为 claim 均出自下列 specifier-lane 调研 agent 的实读(各 agent 报告均自带 file:line 引用、HEAD SHA、specifier lane 声明):
- L2 HTTP/2 调研 —— wreq / http2 / h2 / boring
- SSE 抽 usage 调研 —— new-api / one-api / sub2api / litellm
- 防打爆调研 —— envoy-ai-gateway / litellm / kong / llmgateway / helicone
- CLIProxyAPI 全链路调研 —— CLIProxyAPI
- L1 TLS 后端调研 —— boring / rust-openssl / rustls / wreq / CLIProxyAPI

Source files read: 见各 specifier 调研 agent 报告内的"读过的源码文件清单"(HUAKAI 自研代码读取不受 clean-room 限制)。
Lane: reviewer/synthesizer (Claude);上游 specifier lanes 见上。
Agent: Claude (claude-opus-4-7)
UTC: 2026-05-21
