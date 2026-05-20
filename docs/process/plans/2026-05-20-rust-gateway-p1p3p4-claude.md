# Rust 网关 P1/P3/P4 修复 — Claude 计划

> 平行计划之一(CLAUDE.md #10)。Claude 独立起草,未参考 codex 版本。
> 配对文件: `2026-05-20-rust-gateway-p1p3p4-codex.md`。
> 目标 crate: `exploratory/rust-core-gateway/merged/crates/core_gateway`。

## 背景

Owner 2026-05-20 批准"一批清掉"Rust 网关评审的 P1+P3+P4。三项已对 `merged` 真实代码核实属实
(P1 零调用点 / P2-style try_send 确认 / P3 阈值5+连续失败+无半开 / P4 fmt::layer 同步)。
P2(计费 WAL)Owner 决定排在 P1/P3/P4 之后单独立项,本计划不含。

## P1 — drain_mode 接线 (🔴 · ~0.5d)

现状: `heartbeat.rs` 定义 `is_drain_mode()`/`DRAIN_MODE`,heartbeat 循环每 5s 从控制面刷新;
全 crate 无任何请求路径读它(grep 已确认)。控制面发排空指令网关照收新请求。

改动:
- 新增 `src/drain.rs`(单独 module,职责清晰):axum `middleware::from_fn` 实现 `drain_guard` ——
  进入业务路由前 `if heartbeat::is_drain_mode()` → 直接返回 `503` + `Connection: close` header
  (让 LB/客户端不复用连接),不进业务 handler。
- `lib.rs build_router`: `drain_guard` 作为 `.layer()` 只套在**业务路由**
  (`listener::build_router()` 的结果)上,**不**套 `/healthz`、`/metrics` —— 排空期 metrics
  仍要可抓、healthz 仍要能应答。
- `lib.rs healthz`: 排空时返回 `503` + `{"status":"draining"}`,让 LB 从就绪检查层面停止
  派新流量;503 中间件只作兜底。
- 在飞的流式请求: 中间件只在**进入路由时**判定,已在 handler 内的流不受影响 → 自然跑完 =
  正确排空语义。

测试: drain=false 业务请求正常通过;drain=true 业务请求 503 + Connection: close;
drain=true 时 healthz 返回 503/draining、metrics 仍 200;已在途请求不被中断。

风险: 低。纯加法,默认 drain=false 时全链路行为不变。

## P3 — 熔断器半开探测 (🟡 · ~0.5–1d)

现状: `route_client.rs` `RouteClientOptions` 阈值 `5` 连续失败、冷却 `1s`;
`RouteClientInner` 用 `consecutive_failures: AtomicU32` + `circuit_open_until_ms: AtomicU64`;
`circuit_is_open()` 仅判 `open_until > now`;冷却到点直接 closed,无半开探测 —— 积压请求
冷却一过一拥而上、极易再打满重开。

改动:
- **半开单探**: 冷却到点(`now >= circuit_open_until_ms` 且曾 open)后,放**恰好一个**探测
  请求。用一个 atomic(如 `probe_in_flight: AtomicBool` 的 CAS,或把 breaker 建模成
  `closed/open/half_open` 三态枚举存进一个 `AtomicU8`)抢占探测名额: 抢到的 query 正常发,
  抢不到的仍 `circuit breaker open` 拒绝。探测成功 → `record_success` 整体 closed;
  探测失败 → 立即 `circuit_open_until_ms = now + cooldown` 重开。
- **连续失败 vs 窗口失败率**: 我的推荐是**本批保留"连续失败"计数,不改窗口失败率**。
  理由: 窗口失败率要引滑动窗口/环形缓冲,改动面和风险都更大;而"微秒抖动烧满 5 次"的
  根因更应由下面的 keepalive + 半开快速恢复来缓解。窗口失败率作为后续可选 refinement,
  数据证明确有误触发再做。—— 这是 P3 唯一真正的设计取舍点,留给 Owner / 与 codex 计划对比。
- **gRPC keepalive**: 给控制面 channel 的 tonic `Endpoint` 开
  `http2_keep_alive_interval` + `keep_alive_timeout` + `keep_alive_while_idle(true)`,
  让瞬时空闲抖动不至于直接产生硬失败 —— 从源头降低误触发。改 `build_tcp_endpoint` /
  `build_route_endpoint_parts`。

测试: 连续失败到阈值 → 熔断 open;冷却内所有 query 立即拒;冷却到点第一个 query 为探测、
其余仍拒;探测成功 → closed 恢复;探测失败 → 重新 open 一个冷却周期。

风险: 中。熔断是 fail-closed 路径,改它要保证"控制面真宕机时仍 fail-closed"不被破坏 ——
只改"太敏感 + 无探测",不改"该不该 fail-closed"。

## P4 — 非阻塞日志 (🟡 · ~2–3h)

现状: `tracing_init.rs` 两条分支都用裸 `fmt::layer().json()` / `.compact()`,无 `with_writer`,
默认同步阻塞写 stdout;容器里 stdout 采集变慢会阻塞 Tokio worker。无 `tracing-appender` 依赖。

改动:
- `Cargo.toml` 加 `tracing-appender` 依赖。
- `tracing_init.rs install()`: 用 `tracing_appender::non_blocking(std::io::stdout())` 造
  non-blocking writer,`fmt::layer().with_writer(writer)`。
- `WorkerGuard` 必须存活到进程退出(drop 即停日志)—— `install()` 返回类型从
  `Result<Option<TracerProvider>, _>` 改为返回一个 guard 结构(如
  `struct TracingGuards { otlp: Option<TracerProvider>, log: WorkerGuard }`),调用方
  `main.rs` 持有到 `main` 结束。
- 同步改 `main.rs` 接新返回类型;两条 json/compact 分支都要接 non-blocking writer。

测试: `install` 单测仍能跑通(已有 `install_json_logs_*` / `install_text_logs_*`);
返回值含一个非空 `WorkerGuard`。

风险: 低。注意经典坑: guard 被提前 drop → 日志静默停。

## 实施 / 提交顺序

3 项互相独立,各自单独 commit(`feedback_one_commit_one_module`):
1. **P4** 先做(最小、最独立、~2-3h)→ commit `tracing 非阻塞日志写入`。
2. **P1** → commit `gateway drain_mode 中间件接线`。
3. **P3** → commit `route_client 熔断半开探测`。

每项: codex 实现 → 我独立 `cargo build` + `cargo test` 验证 → per-commit codex review → 提交。
codex 实现,Claude 规划+审查+验证。Rust 代码按职责分 module。

## 成功标准

- `cargo build` + `cargo test`(core_gateway crate)0 失败。
- P1: 排空指令下新请求被拒、在途流不断、healthz 转 unhealthy。
- P3: 半开单探可用、控制面真宕机仍 fail-closed。
- P4: 日志非阻塞、WorkerGuard 生命周期正确。

## 影响面 / 需 Owner 确认

- 影响面: 仅 `core_gateway` crate;P1 默认值下零行为变化;P4 改 `install` 返回类型(波及 main.rs)。
- 需 Owner 确认点: P3 的"连续失败 vs 窗口失败率"取舍(我推荐保留连续失败,见上)。其余无高风险项
  (不动计费、不动 schema、不动 auth)。
