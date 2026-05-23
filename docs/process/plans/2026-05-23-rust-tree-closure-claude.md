# HUAKAI Rust 线 tree-vertical 闭环分析（claude 草稿）

日期：2026-05-23
作者：claude (PM-Orchestrator)
适用对象：Rust 数据面（`exploratory/rust-core-gateway/merged/crates/core_gateway/`）
配套：W11+W12+指纹 synthesis plan（`2026-05-22-rust-hardening-plan.md`）
触发：Owner 2026-05-23 directive "停止横向扩展，只做树向闭环；参考项目只用来发现已有模块未闭环叶子节点，不能用来新增整条功能线"
范式约束：CLAUDE.md #10 (parallel-draft + cross-discuss)，本稿待与 -codex.md 综合后定稿

---

## §0 适用约束 / 禁止扩展清单（Rust 线版）

**绝对不允许在 Rust 线引入的方向**（横向扩展，违反本次 directive）：

- ❌ 用户系统：注册 / 登录 / API Key 自助 / 个人资料 / 2FA / Passkey
- ❌ 商业闭环：支付 / 订阅 / 套餐 / 充值 / 退款 / 兑换码 / 推荐返佣
- ❌ 业务账本：billing / pricing / receipt / dispute / 账本写入
- ❌ 管理员 UI / 用户 UI / 前端任何代码
- ❌ 新协议入口：embeddings / images / audio / rerank / realtime / assistants / vector store / batch / files
- ❌ 新 vendor 接入：Rust 侧不增加非已支持 vendor 的协议解析（保持 OpenAI/Anthropic/Gemini 三族）
- ❌ Go 控制面逻辑迁移到 Rust（Rust 仅作数据面 forwarder + telemetry，不抢业务逻辑）
- ❌ L4 / L5 / L6 反封禁深度扩展（指纹只做 F-1 / F-2 收口，F-3 自动切换走 roadmap）
- ❌ 多语言 i18n / 移动端 / 桌面 GUI
- ❌ DB 迁移 / SQL schema 任何修改（Rust 数据面不动 schema）

**为什么这些禁止**：Rust 进程的角色是"高性能 HTTP 反向代理 + TLS 指纹 + 数据面遥测上报"——其余职责 Go 控制面拥有。引入上述任何一条会让 Rust 进程变胖、回到三方 AI 状态树第三方都遇到过的"网关吞所有功能"陷阱，无法保持轻、快、可单测、可灰度。

**唯一允许的添加形态**：

- ✓ 已有模块内的**新方法 / 新字段 / 新测试 / 新指标 / 新中间件**——只要它闭合的是该模块本身的一个已识别叶子节点
- ✓ 跨已有模块的**安全 fail-closed 守门**（如 W11-D-2 在 config + listener 上加的 mock_upstream 产线 fail-fast + dev/test mock attempt event）
- ✓ 测试基础设施：让既有不可观察的行为变可观察（discriminating fixture / mutation-resistant）
- ✓ 文档 / spec / plan 收紧

---

## §1 已存在 Rust 模块盘点（截至 2026-05-23 HEAD `8d5d16d`，merge 后）

按文件夹拆，每条简单一句话职责。本节是"是什么"，不评价缺什么。

| 模块路径 | 职责一句话 |
|---|---|
| `src/listener.rs` | HTTP 入口；request_id 提取/生成、body 上限、drain / overload 守门、mock_upstream 短路、转 account_planner 或 proxy_engine |
| `src/account_planner/` | 调控制面 `Plan()` RPC；解析路由计划；分错误类型映射 HTTP 状态 |
| `src/proxy_engine/mod.rs` | 出站 HTTP client；`forward_endpoint`（mock）/ `forward_planned`（生产）两条转发路径 |
| `src/proxy_engine/relay.rs` | 流式 / 非流式 body 中继；body chunk 字节计量；attempt terminal reporter 调度 |
| `src/stream_pipeline/` | SSE 解析；OpenAI / Anthropic / Gemini 三族流式 usage / 工具调用片段 |
| `src/attempt_reporter/` | 把转发 attempt 终态以 mpsc 队列上报控制面；重试 / 丢弃 / 计数 metrics |
| `src/mimicry/` | TLS 指纹 profile loading；BoringSSL / OpenSSL adapter；`dispatch.rs` 生产 gate |
| `src/heartbeat.rs` | 周期性给控制面发健康摘要（**当前硬编码 0**） |
| `src/config.rs` | env → StartupConfig；validation；transport baseline；W11-D-2 在加 RuntimeMode + production fail-fast |
| `src/transport/` | gRPC RouteClient；mTLS / loopback HTTP 两种 transport baseline |
| `src/resource_limits.rs` | in-flight 计数器 + overload gate（超过阈值返 503） |
| `src/drain.rs` | drain 模式开关 + middleware（drain 期间业务请求 503，healthz/metrics 仍 200） |
| `src/body_timeout.rs` | 请求 body idle timeout middleware |
| `src/request_id.rs` | request_id 生成 / 校验 / header 常量 |
| `src/server_runtime.rs` | tokio 服务 wrap + 超时 / 信号 |
| `src/lib.rs` | `GatewayState` 顶层组装 + `build_router` |
| `src/metrics.rs` | Prometheus registry |
| `proto/route.proto` + 生成绑定 | 与 Go 控制面的数据契约（authoritative） |

**没有的模块**（不在 Rust 数据面职责内，**也不应该在 Rust 里出现**）：
- 任何 `auth / user / api_key / session` 模块
- 任何 `billing / pricing / receipt / refund / dispute` 模块
- 任何 `voucher / order / subscription / payment` 模块
- 任何前端 / 模板 / 静态资源
- 任何 SQL / DB driver

---

## §2 模块状态树

每个模块按统一格式：已有功能 / 已闭环路径 / 未闭环叶子节点 / 必须补齐 / 暂不补 / 风险 / 测试用例。"必须补齐"对应 P0/P1，"暂不补"对应 P2/roadmap。

### M1. `listener`

- **已有功能**：HTTP 入口 framing；request_id 提取/生成；body 长度预检；drain / overload 守门；mock_upstream 短路；转 account_planner.plan → proxy_engine.forward_planned；planning error → synthetic_control_plane_error attempt 上报。
- **已闭环路径**：
  - 正常 plan → forward_planned 路径全程被 attempt_reporter 终态记账（`relay.rs` 装 terminal_reporter）。
  - Plan 失败路径（control_plane / invalid_route_plan）→ `report_listener_planning_error` 发 synthetic attempt（`listener.rs:150-167`）。
  - drain mode 阻断业务请求但保留 `/healthz` `/metrics`（`drain.rs` + `lib.rs:202` 中间件链）。
- **未闭环叶子节点**：
  - L1.A：mock_upstream 短路分支（`listener.rs:72-81`）**完全不发 attempt event** —— 真转发 mock 但账本无记录、审计无标记 → W11-D-2 B2 acceptance gate 未闭合（**本会话 Codex P1 finding，正是阻止当前 commit 的根因**）。
  - L1.B：mock_upstream 短路分支对**真凭据透传无禁注守门** —— dev 误把生产 key 配到 mock 端点 → 凭据离开 Rust 边界进入 mock server。
- **必须补齐（P0）**：
  - 闭合 L1.A：在 mock 分支起 `AttemptReportContext::synthetic_mock_attempt(&request_id)` + 终态调 `reporter.report(Success/InternalError, http_status, _, Some("mock_upstream_drill"|"mock_upstream_error"), _)` —— W11-D-2 B2 已识别，仅未实施。
  - 闭合 L1.B：mock 分支前剥除 / 拒绝 Authorization / x-api-key / cookie 等凭据头（fail-closed if present in production runtime mode，dev 模式只 log warn）。
- **暂不补（P2）**：mock_upstream 多 endpoint failover（单 endpoint 足够 drill 目的）。
- **风险**：当前状态 mock drill 完全不可审计 → 演练流量看不出区别于真实流量 → 账本对账有空洞。
- **测试用例**：
  - **T1.A discriminating**：dev runtime + mock endpoint + 发请求 → `state.attempt_reporter().enqueued_count()` 必须从 N 变 N+1；assert 最后 enqueue 的 report.error_class == `"mock_upstream_drill"`。Mutation：删 listener mock 分支的 reporter.report 调用 → 计数不变 → 测试红。
  - **T1.B discriminating**：dev runtime + mock endpoint + 带 `Authorization: Bearer real-key` → reporter 上报必须含明确"credential stripped"标记 OR fail-closed。Mutation：删凭据剥除逻辑 → 测试看到 Authorization 透传 → 红。

### M2. `config`

- **已有功能**：env 解析（17+ 个 env var）；validation；transport baseline 选择；W11-D-2 进行中 RuntimeMode (Production/Development/Test) + production+mock_upstream fail-fast。
- **已闭环路径**：
  - listen_addr / max_body_bytes / 各种 timeout 的 default + reject invalid。
  - HTTP transport baseline 强制 loopback（`config.rs:337` `require_loopback_endpoint`）。
  - W11-D-2 B1 已闭合：production runtime + mock endpoint → fail-fast（本会话 4 个新 discriminating 测试已绿）。
- **未闭环叶子节点**：
  - L2.A：HUAKAI_RUNTIME_MODE 与 HUAKAI_MOCK_UPSTREAM 之间的关系只在 production 拒绝 mock，对 dev/test 模式没有 attempt accounting 前提条件（**L1.A 在 listener 侧的孪生**）。
  - L2.B：transport baseline 配置改动后无 health re-probe 钩子（启动时 probe 一次，启动后控制面凭据轮换无重新载入）。
- **必须补齐（P0）**：闭 L2.A —— 在 `validate()` 加一条：runtime_mode=dev/test 且 mock_upstream 存在 → 同时 record warn-log + 期望 listener 侧补 attempt event（与 M1 P0 配对，文档化交接边界）。
- **暂不补（P2）**：L2.B（mTLS 凭据 hot-reload）—— 当前 restart 即可；hot-reload 是运维便利性升级，非闭环必需。
- **风险**：L2.A 与 M1.L1.A 是同一个 P1 的两面（config 允许 + listener 不记账 = 整体审计盲区）。
- **测试用例**：W11-D-2 已有 4 个 config 测试；新增 1 个 doc-level 断言：dev mode + mock 时 listener attempt count 增 1（实测在 listener_test 加，不在 config_test）。

### M3. `account_planner`

- **已有功能**：调控制面 `Plan()` RPC（携带 protocol + headers proxy 字段），将路由计划 + planned_attempt 返回 listener；错误三态（ControlPlane / InvalidRoutePlan / 其他）。
- **已闭环路径**：Plan 成功 → planned 透传 proxy_engine.forward_planned；Plan 失败 → listener 出 synthetic attempt + 503/502。
- **未闭环叶子节点**：
  - L3.A：身份字段当前**不在 Rust 内派生**——`RouteQueryRequest` 在 W11-D-1b 之前没有 client 凭据字段（synthesis plan §2 D-1b 已识别）。Rust 把 headers proxy 给控制面，控制面拿不到客户端身份强信号，只能凭 routing key / IP / 路径 hint 判断 → 误路由/账本租户错位。
  - L3.B：D-1a body parse（取 model / stream 二选一信号送进 Plan）尚未接入（synthesis plan §2 D-1a）—— 控制面没 model 信号 → 选不出正确账号池。
- **必须补齐（P0）**：W11-A 已规划：D-1a body parse（无契约改）+ D-1b 凭据从认证派生（含 §4.5 P-1 proto P-1 字段，受控变更）。本次 directive 内**仍是 P0**，且属树向闭环（在已有 account_planner 模块内扩 Plan 入参，未新增模块）。
- **暂不补（P2）**：account 缓存层（route_cache_ttl 当前已禁用，待后续 W12+ 决定，本次不动）。
- **风险**：D-1b 未闭合 → 控制面识别精度依赖 client header → 任何代理 / 客户端篡改 header 就可错路由。
- **测试用例**：D-1a 测试覆盖 model 抽取在 chat / responses / messages 三族 endpoint；D-1b 测试覆盖凭据缺失 / 残值 fail-closed。

### M4. `proxy_engine` + `proxy_engine/relay`

- **已有功能**：出站 HTTP client；mock / planned 两条转发路径；body chunk 计量；流式 SSE 中继；attempt terminal_reporter 在 planned 路径上挂接。
- **已闭环路径**：planned 路径 forward → relay → terminal_reporter 完整链路。
- **未闭环叶子节点**：
  - L4.A：D-3 vendor endpoint 协议（控制面可能下发 `http://` vendor）—— Rust 未在 forward 前 https-only 守门，→ 生产明文转发风险（synthesis plan §2 D-3）。
  - L4.B：D-6 透传 headers 包含客户端 Authorization / Cookie 残留 —— Rust 未剥除 → 客户端凭据原样到 vendor（synthesis plan §2 D-6）。
  - L4.C：D-10 stream 取消时 upstream 连接处置（synthesis plan §2 D-10）。
  - L4.D：D-5 非流式响应不解析 vendor body 内 usage —— token 漏记（synthesis plan §3 D-5）。
- **必须补齐（P0/P1）**：D-3 / D-6 / D-10 是 P0（W11-C / W11-D / W11-E，三个独立 commit）；D-5 是 P1（W12-B，依赖 D-4 spool 先到位）。
- **暂不补（P2）**：连接池 idle 超时精调（默认值足够，无 incident 触发）。
- **风险**：L4.A / L4.B 是凭据 + 明文 + 路由错配三联，单点缺失就是事故。
- **测试用例**：每条 D-3/D-6/D-10/D-5 都需 discriminating fixture（synthesis plan acceptance gate C/D/E 段已列）。

### M5. `attempt_reporter`

- **已有功能**：mpsc 队列（cap 1024）+ worker；try_send 满 → DroppedFull 计数器；retry / failed_reports 计数器；synthetic context 工厂方法。
- **已闭环路径**：planned attempt 成功 → enqueue → worker → 控制面 RPC → ack。
- **未闭环叶子节点**：
  - L5.A：D-4 队列满 / 重试耗尽**没有 durable spool** —— 直接静默丢账（synthesis plan §3 D-4，已定 C2 durable spool 实施路径）。
  - L5.B：本会话发现 —— `AttemptReportContext` 无 `synthetic_mock_attempt(&request_id)` constructor（M1.L1.A 闭合所需，当前只有 `synthetic_control_plane_error`）。
  - L5.C：D-8 429/408 当前归 `Upstream4xx` 不可重试 → 限流误判为客户端错误（synthesis plan §3 D-8）。
- **必须补齐（P0/P1）**：L5.B 是本次 W11-D-2 B2 内立刻补（~10 行）；L5.A 是 W12-A（最重的单项，3 个 sub-slice）；L5.C 是 W12-D（小，与 D-7 同 commit 区）。
- **暂不补（P2）**：worker 多线程并行（当前单 worker 已能跟上正常吞吐；满盘是 L5.A 解决范畴）。
- **风险**：L5.A 未闭 → 账本空洞最终触发审计 dispute；L5.C 未闭 → 节流账号被错误降级。
- **测试用例**：W11-D-2 B2 加 1 个 discriminating（断言 synthetic_mock_attempt 产生的 report.error_class 含 "mock"）；W12-A 加 5 个 acceptance（synthesis plan §3 D-4 已列：溢出无丢 / 崩溃恢复 / 重放幂等 / 磁盘满 / 控制面长期不可用）。

### M6. `mimicry`

- **已有功能**：profile loading（Codex / Kiro / Gemini 等 builtin profiles）；BoringSSL adapter（feature flag `mimicry-boring`）；OpenSSL adapter（feature flag `mimicry-openssl`）；backend resolver 选择 Boring vs OpenSSL vs KnownGapBlocked；dispatch.rs 生产 gate 决策。
- **已闭环路径**：profile loading + backend resolver 三态分支；exact stable profile + OpenSSL 已 capture 路径。
- **未闭环叶子节点**：
  - L6.A：本会话发现 —— `tests/mimicry_dispatch_test.rs` 6 个测试期望 `BlockUnsupportedTemplate { reason }`，实际 resolver 返回 `BlockKnownGap { reason: "X profile requires mimicry-boring..." }`（`backend_resolver.rs:86`）→ 测试 ↔ 实现语义漂移（**phase-1 合并前后都红，与 W11-D-2 无关**）。
  - L6.B：F-1 L2 profile 接线收口（synthesis plan §4 F-1）。
  - L6.C：F-2 L1 缺口收口（synthesis plan §4 F-2）。
  - L6.D：F-3 在线自动切换（synthesis plan §4 F-3，已 roadmap）。
- **必须补齐（P0/P1）**：L6.A 是 P1（小修：要么更新测试期望 `BlockKnownGap`，要么把 resolver 这几个分支改回 `BlockUnsupportedTemplate`；前者更符合"feature-flag gap"语义）；L6.B / L6.C 是 W11-F（指纹波，~8-12 codex-day）。
- **暂不补（P2）**：L6.D F-3 自动切换 → roadmap；reqwest-impersonate / curl-impersonate 替代 → BoringSSL 决策已 lock。
- **风险**：L6.A 当前让 `cargo test` 红灯长期挂在 CI，**遮蔽真问题**——必须先修，否则后续每次 commit 看到的"6 个 failed"会被疲劳忽略。
- **测试用例**：L6.A 修完后 6 个测试转绿；F-1 / F-2 各 discriminating fixture（synthesis plan F 段已列）。

### M7. `heartbeat`

- **已有功能**：周期性 HeartbeatRequest 给控制面。
- **已闭环路径**：定时器 + RPC 调用 + 失败重试。
- **未闭环叶子节点**：
  - L7.A：D-7 `node_id` 固定串，`in_flight_requests` / `attempt_report_queue_depth` / `p95_*` / `error_rate_1m` **全硬编码 0**——真实 gauge 已存在（`resource_limits.rs:96` 真 in-flight、`attempt_reporter/mod.rs:166` 真 queue depth），但 heartbeat 没拉（synthesis plan §3 D-7）。
- **必须补齐（P0）**：W12-C —— 拉真值即可；不需 proto 改动（HeartbeatRequest 字段已存在，`route.proto:120-130`）。
- **暂不补（P2）**：`p95_*` / `error_rate_1m` 的精确直方图计算 → 当前 Prometheus client 提供的简化版本足够；优化是 roadmap。
- **风险**：控制面拿到的健康状态全是假数 → 调度决策依赖错的输入 → 选不出真健康的 Rust 节点。
- **测试用例**：discriminating：把 in_flight 推到 N → 下一次 heartbeat 携带的 in_flight_requests == N（不再是 0）。Mutation：删拉真值的那行 → 测试看到 0 → 红。

### M8. `transport`

- **已有功能**：gRPC RouteClient；mTLS / HTTP loopback 两种 baseline；启动时 probe。
- **已闭环路径**：mTLS 完整链路 + 证书 path 校验（`config.rs` 配合）。
- **未闭环叶子节点**：当前无识别的闭环缺口 —— 工程上稳定。
- **必须补齐**：无。
- **暂不补**：mTLS 凭据 hot-reload（M2 L2.B 同源）。
- **风险**：低。
- **测试用例**：现有 transport + listener 集成测试覆盖。

### M9. `resource_limits` / `drain` / `body_timeout` / `request_id` / `server_runtime`

- **已有功能 / 已闭环**：各自的中间件 + middleware test 覆盖。listener_test 12 个测试覆盖 drain / overload / body limit / request_id / timeout 路径。
- **未闭环叶子节点**：本会话未发现新缺口。
- **必须补齐**：无（前期工作已完成）。
- **暂不补**：无。
- **风险**：低。
- **测试用例**：现有 12 个全绿。

### M10. `proto`（控制面契约）

- **已有功能**：`route.proto` 定义 PlanRequest / PlanResponse / AttemptReport / HeartbeatRequest 等；prost 生成 Rust 绑定。
- **已闭环路径**：现有字段已 plumb through Rust ↔ Go 双侧。
- **未闭环叶子节点**：
  - L10.A：D-1b §4.5 P-1 受控字段变更（PlanRequest 加客户端凭据派生身份） —— 需 Rust + Go 双侧协调（synthesis plan §4.5 P-1 已通过 Owner 决策）。
- **必须补齐（P0）**：与 W11-A D-1b 同步（不孤立）。
- **暂不补**：D-7 `unknown vs 0` 区分（`optional double` 需求） → §4.5 P-3 可选项，**非 D-7 必须**。
- **风险**：proto 变更是跨进程契约，必须双侧同步部署 + Manual First feature flag 兜底。
- **测试用例**：mock_control_plane 同步演进；proto consistency test。

---

## §3 跨模块共识 / 测试基础设施缺口

本节是本会话新发现 —— 在状态树之外，但影响多模块。

### TI-1：listener 没有 attempt event 测试探针

- **现状**：`listener_test.rs` 12 个测试都验证 HTTP 行为（status / body / header），**无任何测试断言 attempt_reporter.enqueued_count() 变化**。
- **闭环阻碍**：W11-D-2 B2 需 mutation-resistant 测试 → 缺探针 → 要么加 unit test 在 listener.rs 内（构造最小 GatewayState）+ 暴露 reporter，要么走集成测 + 通过 `/metrics` Prometheus 读 `attempt_reporter_enqueued_total`。
- **必须补齐（P0）**：路径 1（unit test in `src/listener.rs` 加 `#[cfg(test)] mod tests`，构造 `GatewayState::new(test_config)`，用 tower::ServiceExt::oneshot 跑请求，断言 `state.attempt_reporter().enqueued_count()` 增 1）—— 这是 W11-D-2 B2 的强制依赖。
- **替代方案**：Prometheus `/metrics` 端点是否已暴露 `attempt_reporter_enqueued_total` ？若有，集成测可直接 grep —— 待 codex 平行稿验证。

### TI-2：mimicry dispatch 测试 ↔ 实现长期红灯

- **现状**：6 个测试在 phase-1 合并前后均红（本会话验证）。
- **闭环阻碍**：cargo test 默认状态下永远 6 红，团队对"红"麻木后真正的 regression 不会被发现。
- **必须补齐（P0）**：M6 L6.A 修齐，把"全绿"作为新基线；后续每个 commit 必须保持全绿。

### TI-3：Rust 端 `tracing` / `metrics` exposed 字段是否够细

- **现状**：`metrics.rs` 暴露 Prometheus registry；具体哪些 counter / histogram 已注册需逐项 grep。
- **闭环价值**：D-7 heartbeat 闭合 + L5.A spool 落地后，运维需要 Rust 端能 introspect 自身 attempt queue / spool depth / drop rate。
- **必须补齐（P1）**：盘点已注册的 metric；缺哪些核心 gauge 补哪些。本稿不强制清单，留 W11-D-7 / W12-A 同 commit 区批量做。

---

## §4 与 W11+W12+指纹 synthesis plan 的关系

**本稿是 synthesis plan 的"discipline 重述 + 状态树细化"，不是替代**。Synthesis plan 提供：

- 每个 D-x / F-x 的现状证据（带 file:line）
- 修法 + acceptance gates
- 时间估算 + 提交顺序

本稿在 synthesis plan 之上加：

1. **明确禁止扩展清单**（§0）—— synthesis plan 没明写"不做什么"
2. **逐模块状态树**（§2）—— synthesis plan 是按 D-x 编号扁平展示
3. **跨模块测试基础设施缺口**（§3）—— 本会话新发现
4. **每个 P0 leaf 标"是否横向扩展"** —— 全部"否"，全部树向闭环（在已有模块内加方法/字段/测试/中间件）

**对接关系**：

| synthesis plan D-x | 本稿对应 leaf | 树向闭环判定 |
|---|---|---|
| D-1a body parse | M3.L3.B | ✓ 在 account_planner 内 |
| D-1b 凭据派生 | M3.L3.A + M10.L10.A | ✓ proto 字段扩展（受控） |
| D-2 mock 生产 fail-fast | M2.L2.A + M1.L1.A | ✓ 在 config + listener 内（**进行中**） |
| D-3 https-only vendor | M4.L4.A | ✓ 在 proxy_engine 内 |
| D-4 attempt spool | M5.L5.A | ✓ 在 attempt_reporter 内 |
| D-5 非流式 usage | M4.L4.D | ✓ 在 proxy_engine/relay 内 |
| D-6 headers 残留 | M4.L4.B | ✓ 在 proxy_engine 内 |
| D-7 heartbeat 真值 | M7.L7.A | ✓ 在 heartbeat 内 |
| D-8 429/408 重分类 | M5.L5.C | ✓ 在 attempt_reporter 内 |
| D-10 stream 取消 | M4.L4.C | ✓ 在 proxy_engine 内 |
| F-1 L2 接线 | M6.L6.B | ✓ 在 mimicry 内 |
| F-2 L1 缺口 | M6.L6.C | ✓ 在 mimicry 内 |
| F-3 自动切换 | M6.L6.D | ✗ 大功能，roadmap |

**结论**：synthesis plan 13 个工作项中 **12 个属树向闭环**（只在 1-2 个已有模块内扩展叶子）；F-3 是仅有的"较大功能"，已正确归类 roadmap。**与本次 Owner directive 完全一致，无需推翻 synthesis plan**。

---

## §5 最终清单：P0 / P1 / P2 / 明确禁止

### P0 必须补齐（本波及次波内必做）

| 编号 | 模块 | leaf | 工作量 | 当前状态 |
|---|---|---|---|---|
| P0-1 | M1 + M2 + M5 | W11-D-2 B1 + B2（mock fail-fast + dev/test attempt event） | 0.5-1 codex-day | B1 已实施 + 测试绿；B2 in-progress（Codex P1 阻塞） |
| P0-2 | M3 + M10 | W11-A D-1a + D-1b（含 §4.5 P-1 proto 字段） | 2-3 codex-day | 未启动；P-1 已 Owner 批准 |
| P0-3 | M4 | W11-C D-3 https-only vendor | 0.5-1 codex-day | 未启动 |
| P0-4 | M4 | W11-D D-6 headers 残留 | 0.5-1 codex-day | 未启动 |
| P0-5 | M4 | W11-E D-10 stream 取消 | 0.5-1 codex-day | 未启动 |
| P0-6 | M6 | L6.A mimicry dispatch test ↔ 实现对齐 | 0.5 codex-day | **新发现**，需立即修否则 CI 长期红 |
| P0-7 | TI-1 | listener attempt event 测试探针 | 0.5 codex-day | P0-1 B2 强制依赖 |
| P0-8 | M5 | W12-A D-4 attempt spool（3 sub-slice） | 2-3 codex-day | 未启动；W12 最重单项 |
| P0-9 | M7 | W12-C D-7 heartbeat 真值 | 0.5 codex-day | 未启动 |

**P0 合计估时**：~8-12 codex-day。覆盖 W11 全部 + W12 D-4/D-7（账务遥测最关键两条）。

### P1 可补齐（W12 同波内顺手做）

| 编号 | 模块 | leaf | 工作量 |
|---|---|---|---|
| P1-1 | M4 | W12-B D-5 非流式 usage | 0.5-1 codex-day |
| P1-2 | M5 | W12-D D-8 429/408 重分类 | 0.5 codex-day |
| P1-3 | TI-3 | metrics 盘点 + 补缺核心 gauge | 0.5 codex-day |
| P1-4 | M1 | L1.B mock 分支凭据剥除 | 0.5 codex-day |

**P1 合计估时**：~2-3 codex-day。

### P2 暂缓 / roadmap

| 编号 | leaf | 暂缓理由 |
|---|---|---|
| P2-1 | M6 L6.D F-3 自动切换 | 大功能，需稳态指纹基线先就位 |
| P2-2 | M2 L2.B + M8 mTLS hot-reload | 重启可解决；非闭环必需 |
| P2-3 | M3 route_cache_ttl 复用 | W12 后再评估 |
| P2-4 | M7 p95/error_rate 直方图 | 简化版足够 |
| P2-5 | M5 worker 多线程并行 | 单 worker 跟得上；L5.A 解决了满盘问题就够 |

### 明确禁止（违反本次 directive）

| 类别 | 具体禁止项 | 禁止理由 |
|---|---|---|
| 用户系统 | 在 Rust 加 auth/user/api_key/session/passkey | Rust 是数据面，不持业务 |
| 商业闭环 | Rust 加 billing/pricing/payment/voucher | 同上 |
| 业务账本 | Rust 加 receipt/dispute/refund | 同上 |
| 前端 | Rust 加任何 UI / template | 数据面进程无 UI 渲染需求 |
| 新协议 | Rust 加 embeddings/images/audio/rerank/realtime/assistants/vector_store/batch/files endpoint | 三族协议 (OpenAI/Anthropic/Gemini) 足够；新协议属横向扩展 |
| 新 vendor | Rust 加非已支持 vendor 协议解析 | 同上 |
| Go 业务迁移 | 把 Go 控制面 Plan() / billing / audit 逻辑搬进 Rust | 角色边界违反；契约只走 proto |
| 深度反封禁 | F-3 之外的 L4/L5/L6 指纹扩展 | 与 F-3 同性质，roadmap |
| i18n / 移动 | Rust 加多语言 / 移动端代码 | 数据面进程无展示职责 |
| DB / schema | Rust 加 SQL driver / schema migration | 数据面不持有持久化 |

---

## §6 执行顺序建议（在 directive 内）

1. **立即（本会话）**：
   - 闭合 P0-1（W11-D-2 B2）+ P0-7（listener attempt 测试探针），一并 commit → push
   - 闭合 P0-6（mimicry 测试 6 红），让 cargo test 全绿基线建立 → 单独 commit → push
2. **下个会话起**（顺序）：P0-2 → P0-3 → P0-4 → P0-5（W11 收尾），每个独立 commit
3. **W12 波**：P0-8 → P0-9 → P1-1 → P1-2 → P1-3（spool 先到位再做 usage / 重分类）
4. **指纹波**：P1-4（mock 凭据剥除，与 W11 同源）+ F-1 + F-2（M6.L6.B / L6.C）
5. **roadmap**：所有 P2

---

## §7 风险 / 失败模式

- **风险 R1**：W11-D-2 B2 测试探针选错路径（unit vs 集成） → 探针不 mutation-resistant → 闭合是假闭合。**缓解**：本稿明确要 TI-1 路径 1（unit test，断言 enqueued_count 增 1）。
- **风险 R2**：P0-6 mimicry 测试修复方向选错（改测试还是改实现）→ 长期 hide 真问题。**缓解**：本稿建议改测试（让期望对齐"feature-flag gap"语义的实际返回值 `BlockKnownGap`），但实际定调留给 codex 平行稿验证。
- **风险 R3**：清单内 P0 项隐含 W11-A D-1b 需 Go 侧 P-1 字段消费者同步上线 → 单边落地是危险。**缓解**：D-1b 排在 W11 末尾切片，Phase-1 用 Manual First feature flag 兜底，Phase-2 待 Go 控制面同步部署后 advance。
- **风险 R4**：本稿与 Owner directive 在"是否在 Rust 加凭据剥除（L1.B）"的判定上属边界 —— "剥客户端凭据"是数据面纯转发职责的一部分（不是业务），归 P1。**缓解**：surface 给 Owner 确认。

---

## §8 Codex 平行稿位（CLAUDE.md #10）

本稿为 Claude 草稿。Codex 平行稿位 `2026-05-23-rust-tree-closure-codex.md`，要求 Codex 独立产出：

- 是否同意 §0 禁止扩展清单的边界
- 是否漏掉任何已有 Rust 模块（§1）
- 是否漏掉任何已闭环 / 未闭环叶子节点（§2）
- 是否同意 P0 / P1 / P2 分级（§5）
- 是否同意 §3 测试基础设施缺口判定
- 任何 Codex 想加 / 想删 / 想重排的 item
- 风险与失败模式补充

之后综合双稿出 `2026-05-23-rust-tree-closure-synthesis.md`（agree / conflict / gap），surface Owner。

---

## Change Log

- **2026-05-23 v1**：claude 草稿创建。基于 Owner 2026-05-23 directive "停止横向扩展、只做树向闭环、只管 Rust 线"，配合 W11+W12+指纹 synthesis plan 重新切角度。
