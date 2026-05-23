# HUAKAI Rust 线 tree-vertical 闭环分析（synthesis 定稿）

日期：2026-05-23
来源：claude 草稿（`2026-05-23-rust-tree-closure-claude.md`）+ Codex 平行稿（`2026-05-23-rust-tree-closure-codex.md`）独立产出后综合。
触发：Owner 2026-05-23 directive "停止横向扩展，只在已有模块内做树向闭环；只管 Rust 线"
契约：本稿是定稿；后续 W11 实施以本稿 §5 P0/P1/P2 清单为准，不以两份草稿为准。

---

## §0 双稿独立性 + 共识度

- **Claude 稿**：基于本会话内已读模块（listener / attempt_reporter / config / mimicry / proxy_engine / synthesis plan），逐模块 14 项状态树 + 禁止清单 + P0/P1/P2 + 与 synthesis plan mapping。
- **Codex 稿**：独立读了 32 个源文件（含 `proxy_engine/headers.rs`、`auth.rs`、`route_client.rs`、`circuit_breaker.rs`、`mimicry/openssl_adapter.rs`、`mimicry/http2_adapter.rs`、`mock_control_plane.rs`、`redaction.rs` —— 这些 Claude 没读到的细节）。Codex 显式声明"未读 Claude 稿；rg 扫描时偶遇几行 Claude 文件已排除证据基"。
- **共识度**：~80%。两稿在禁止清单、D-1a / D-3 / D-6 / D-4 / D-5 / D-7 / mimicry 章节 verdict 高度一致。冲突 3 处（D-2 / D-10 编号 / D-1b 优先级），gap 互补 ~6 处（见 §2 §3）。
- **结论**：synthesis plan 13 项 + 本稿补 D-9/O-2/L1-canary/L2-not-wired/mock fault knobs 共 ~18 项树向闭环 leaf；零横向扩展项。Owner directive 落地路径清晰。

---

## §1 强一致项（双稿确认）

### §1.1 禁止扩展清单（一致）

Rust 数据面**绝对不允许**：用户系统 / 商业闭环 / 业务账本 / 前端 / 新协议入口（embeddings / images / audio / rerank / realtime / assistants / vector store / batch / files）/ 新 vendor / Go 业务逻辑迁移 / L4 pacing / L5 IP pool / L6 主动反封禁 / 多语言 / 移动端 / DB schema / 任何 SQL driver / 任何 reference 项目源码 import。

**唯一允许的添加形态**：已有模块内的新方法 / 新字段 / 新测试 / 新指标 / 新中间件（仅闭合已识别的叶子节点）。

### §1.2 已存在 Rust 模块盘点（一致 + Codex 补充）

Claude 列 14 模块；Codex 加细：
- `src/proxy_engine/headers.rs`（header allowlist 已存在）
- `src/proxy_engine/auth.rs`（plan auth 注入 + acquisition token 拒绝已存在）
- `src/redaction.rs`（错误信息脱敏）
- `src/circuit_breaker.rs`（route_client 半开探测）
- `src/route_client.rs:593-630`（UDS socket 类型 + mode + uid/gid 验证 on Unix）
- `src/mimicry/openssl_adapter.rs:129-148`（exact OpenSSL preflight）
- `src/mimicry/http2_adapter.rs:1-4`（HTTP/2 fork **未接进 ProxyEngine**）
- `src/mock_control_plane.rs:168-181`（last_request/report/heartbeat capture，无 fault knobs）

### §1.3 树向闭环判定（一致）

Synthesis plan 13 项工作项中 **12 项属树向闭环**（在 1-2 个已有模块内扩展叶子），1 项（F-3 自动切换）已正确归 roadmap。**与 directive 完全一致，无需推翻 synthesis plan**。本稿是 synthesis plan 的 "discipline 重述 + 状态树细化 + gap 补漏"。

### §1.4 双稿对 P0 高度一致的 leaf

- **D-1a body parse**（`listener.rs:64-70` + `account_planner.rs:201-222`）：body `model`/`stream` 权威，header 不再 authoritative。
- **D-3 endpoint guard**（`account_planner.rs:244-253` + `proxy_engine/mod.rs:301-316`）：https-only + 私网/loopback/reserved IP 阻断 + DNS rebinding guard。
- **D-6 strip client org/project headers**（`proxy_engine/headers.rs:45-58`）：客户端 `openai-organization` `openai-project` 不到上游；路由计划注入版本到达上游。
- **D-4 attempt spool**（`attempt_reporter/mod.rs:140-159, 207-268`）：durable 本地 spool + replay worker + pre-commit reserve gate + post-commit loud failure metric。
- **D-7 heartbeat 真值**（`heartbeat.rs:72-83` ↔ `attempt_reporter/mod.rs:166-192` + `resource_limits.rs:96-104`）：拉真 in-flight / queue depth。
- **mimicry feature-matrix CI**（`Cargo.toml:13-17`）：default + `tls` + `mimicry-boring` + `mimicry-http2-fork`。

---

## §2 Gaps Claude 抓到，Codex 漏的

| # | Leaf | Claude 在何处提 | Codex 漏的原因 |
|---|---|---|---|
| **G-C1** | **W11-D-2 B2** mock 路径 attempt event 当前 in-progress（plan §5.5 acceptance gate 明文要求）| Claude §2 M1.L1.A + P0-1 | Codex 直接读 `config.rs:222-232` 认为 D-2 "mostly closed"，未交叉读 synthesis plan §5.5 B2 gate，**也未识别本会话 Codex 自己的 P1 review finding 正是 B2 缺口** |
| **G-C2** | `AttemptReportContext::synthetic_mock_attempt(&request_id)` constructor 缺失（B2 实施钩子）| Claude §2 M5.L5.B | Codex 把 mock event 归类到 D-2 P2 守门，未识别需新增 constructor |
| **G-C3** | **TI-1 listener attempt event 测试探针缺失**（W11-D-2 B2 强制依赖）| Claude §3 TI-1 + P0-7 | Codex 在 §3 提了 "need end-to-end report harness"，但未把 listener 单元测试探针单独定 P0 |
| **G-C4** | **mimicry dispatch_test 6 红 = phase-1 合并前后都红**（本会话亲测）| Claude §2 M6.L6.A + P0-6 + TI-2 | Codex 也指出 D-10 是 resolver bypass，但**未识别"测试 6 红是 CI 长期遮蔽真问题的根因，必须先修"** |
| **G-C5** | **明确把 W11-D-2 B1 已实施 + 4 测试已绿 + B2 阻塞 codex review** 作为当前状态记录 | Claude §6 执行顺序第 1 步 | Codex 写时假设从零开始，未识别 W11-D-2 已部分落地 |

**Synthesis 取舍**：G-C1 ~ G-C5 全部采纳，进入 synthesized P0 清单。

---

## §3 Gaps Codex 抓到，Claude 漏的

| # | Leaf | Codex 在何处提 | Claude 漏的原因 |
|---|---|---|---|
| **G-D1** | **D-10 真正含义是 mimicry resolver bypass**（`backend_resolver.rs:72-92` 早 return `Boring` 当 `mimicry-boring` feature 编译时，绕过 `backend_intent()` 的 KnownGap/UnsupportedTemplate 阻断）| Codex §2.8 + §5 P0 | Claude 误把 D-10 编号当 "stream 取消"（synthesis plan 根本没有 stream 取消编号项）；编号错配 |
| **G-D2** | **D-9 + O-2 合 commit**：`bytes_in` 当前只取 `Content-Length`，chunked / H2 真实字节漏记；`ACTIVE_CONNECTIONS` / `QUEUE_DEPTH` / `OPEN_UPSTREAM_CONNECTIONS` registered 但 lifecycle 未接 | Codex §2.4 + §2.7 + §5 P1 | Claude 在 TI-3 模糊提"metrics 盘点"，未识别具体 gauge gap + 字节漏记是 D-9 |
| **G-D3** | **L2 HTTP/2 fork adapter 未接进 ProxyEngine** —— feature flag 编译但 `proxy_engine/mod.rs:19-26` 不 import；生产 dispatch 必须显式 block L2-only profile | Codex §2.8 + §5 P1 | Claude 在 M6 章只提 F-1/F-2 (W11-F)，未识别"adapter 编译 ≠ 已接线" gap |
| **G-D4** | **Mock control plane 缺 fault knobs**：D-4 disk-full / D-7 real-load assertion / D-5 bad-JSON / replay duplicate ack 测试钩子缺失（`mock_control_plane.rs:51-66, 168-181` 当前只支持 RPC 行为/延迟/last capture）| Codex §2.9 + §3 + §5 P1 | Claude 完全没读 `mock_control_plane.rs`，对此盲区 |
| **G-D5** | **D-1b 边界更显式**："Rust 不建 user/auth system；只 enforce boundary，等控制面 contract 准备好"；Phase 1 fail-closed for production-billable direct-client traffic | Codex §2.2 + §5 P1 | Claude P0-2 把 D-1a + D-1b 合 W11-A 一起标 P0，未充分识别 D-1b 是 cross-line contract 不能 Rust 单边 |
| **G-D6** | **Plan auth 注入已闭环**：`proxy_engine/auth.rs:33-43` 已拒绝 upstream bearer 等于 acquisition token；这是已有闭环细节 | Codex §2.4 已闭环路径 | Claude 没读 `proxy_engine/auth.rs`，对此盲区 |

**Synthesis 取舍**：G-D1 ~ G-D6 全部采纳。**D-10 编号以 Codex 为准**（mimicry resolver bypass）。

---

## §4 冲突 + 解决

### §4.1 D-2 优先级冲突

- **Claude**：P0（W11-D-2 B1 done + B2 in-progress + 阻塞 codex review）
- **Codex**：P2（"mostly closed in current config; keep regression tests"）
- **裁定**：**P0**。Codex 未交叉读 plan §5.5 B2 acceptance gate（dev/test mock 路径必须 emit explicit mock attempt event），也未识别本会话 Codex review P1 finding 正是 B2 缺口。证据：plan §5.5 B2 + 本会话 Codex review output `bpujb4v4e.output:4626-4631`。

### §4.2 D-10 编号冲突

- **Claude**：把 D-10 误标为 "stream 取消"（P0-5 W11-E）
- **Codex**：D-10 = mimicry resolver bypass（P0）
- **裁定**：**Codex 正确**。证据：synthesis plan `2026-05-22-rust-hardening-plan.md:195-201` 权威定义。Claude 草稿 M4.L4.C 那一行删除；W11-E 重命名为 "mimicry resolver bypass"，归 M6 章节而非 M4。**Stream client cancel 处理不是 W11 P0 项**——它在 W12-B D-5 + W12-A D-4 的覆盖范围内（client cancel → terminal_reporter 终态 client_cancel）。

### §4.3 D-1b 优先级冲突

- **Claude**：P0（W11-A D-1a + D-1b 合包）
- **Codex**：P1（"Rust 不建 user/auth；boundary only，等控制面 contract"）
- **裁定**：**拆开**。
  - D-1a 仍 **P0**（body parse 是 Rust 单边可完成的）
  - D-1b 降 **P1**（依赖控制面 P-1 字段消费者上线；Phase 1 Manual First feature flag 兜底，已 Owner 批准；Phase 2 待 Go 控制面同步部署后 advance）
  - Rust 单边可做的：proto P-1 字段定义 + Phase 1 写入侧但不读取侧、行为不变保护。读侧 enable 由 Go 控制面消费 commit 触发。

---

## §5 最终清单（synthesized 定稿，本稿权威）

### §5.1 P0 必须补齐（W11 波 + W12 最关键三项）

| # | 编号 | 模块 | leaf 描述 | 工作量 | 当前状态 |
|---|---|---|---|---|---|
| **P0-1** | W11-D-2 B1+B2 + TI-1 | config + listener + attempt_reporter | mock 生产 fail-fast（B1，已绿）+ dev/test mock attempt event（B2 加 `synthetic_mock_attempt` + listener mock 分支 emit）+ listener attempt 测试探针 | 1 codex-day | **B1 done，B2 进行中（Codex review P1 阻塞）** |
| **P0-2** | W11-A D-1a | listener + account_planner | bounded body parse 一次 → `requested_model` / `stream` 从 body 派生，header 不再 authoritative | 1 codex-day | 未启动 |
| **P0-3** | W11-C D-3 | account_planner + proxy_engine | vendor endpoint guard：https-only + 私网/loopback/reserved IP 阻断 + DNS rebinding | 0.5-1 codex-day | 未启动 |
| **P0-4** | W11-D D-6 | proxy_engine/headers | strip 客户端 `openai-organization` / `openai-project` / 残留 `authorization` / `x-api-key`；route plan 注入版本到达上游 | 0.5 codex-day | 未启动 |
| **P0-5** | W11-E D-10 | mimicry/backend_resolver | **mimicry resolver bypass 闭合**：`resolve_vendor_mimicry_backend` 必须先调 `backend_intent()`，feature 可用不能覆盖 KnownGap/UnsupportedTemplate | 0.5 codex-day | 未启动 |
| **P0-6** | W11-F.precondition | mimicry/dispatch_test | 修齐 6 个红 dispatch_test（要么改测试期望 `BlockKnownGap`，要么把 resolver 几个分支改回 `BlockUnsupportedTemplate`）—— **必须先做，否则 CI 长期红遮蔽真问题** | 0.5 codex-day | 未启动（本会话发现） |
| **P0-7** | W12-A D-4（3 sub-slice）| attempt_reporter + proxy_engine | durable 本地 spool（数据结构 + 写路径 + 关闭条件） + replay worker（启动 replay + ack + replay metrics） + 满盘降级 + `reserve()` pre-commit gate + post-commit loud failure | 2-3 codex-day | 未启动；W12 最重单项 |
| **P0-8** | W12-B D-5 minimum | proxy_engine/relay + stream_pipeline | 非流式 2xx body 抽 `usage` for OpenAI / Anthropic；`tokens_used.source` 词表 `response_body` / `pending_reconciliation`；不需 proto 改 | 1 codex-day | 未启动 |
| **P0-9** | W12-C D-7 | heartbeat | 拉真 in-flight + 真 attempt_report_queue_depth；不需 proto 改（字段已存在） | 0.5 codex-day | 未启动 |
| **P0-10** | W12-E D-9+O-2 | proxy_engine + resource_limits + metrics | 真实 inbound body 字节计量（包装 body）+ `ACTIVE_CONNECTIONS` / `OPEN_UPSTREAM_CONNECTIONS` lifecycle 接线（accept/drop + upstream req begin/end）| 1 codex-day | 未启动 |

**P0 合计估时**：~8.5-10 codex-day。

### §5.2 P1 顺手做（W11 / W12 同波内）

| # | 编号 | 模块 | leaf | 工作量 |
|---|---|---|---|---|
| **P1-1** | W11-A D-1b（Phase 1）| account_planner + proto | `RouteQueryRequest` 增 client credential 派生字段（§4.5 P-1 已批）；Phase 1 Manual First feature flag OFF → 旧字段路径不工作；Phase 2 待 Go 控制面消费 commit 后切 ON | 1-2 codex-day |
| **P1-2** | W12-D D-8 | proxy_engine + attempt_reporter | 429 / 408 单独识别 → retryable；401 / 403 保持 non-retryable | 0.5 codex-day |
| **P1-3** | W11-F L1 canary | mimicry/dispatch | profile 缺 L1 capture 证据 → 生产 dispatch fail-closed | 1 codex-day |
| **P1-4** | W11-F L2 not-wired | mimicry/http2_adapter + proxy_engine | 显式标 HTTP/2 fork 未接进 ProxyEngine；生产 dispatch 拒绝 L2-only profile | 0.5 codex-day |
| **P1-5** | Test infra mock fault knobs | mock_control_plane | 加 disk-full / real-load / bad-JSON / replay duplicate 测试钩子（test code only，不进产线） | 1 codex-day |
| **P1-6** | Feature-matrix CI | Cargo + tools | `cargo test` matrix：default + `tls` + `mimicry-boring` + `mimicry-http2-fork` 各跑一次 | 0.5 codex-day（doc + CI 配置） |
| **P1-7** | M1 L1.B mock 凭据剥除 | listener | mock 分支前剥除 `Authorization` / `x-api-key` / `cookie`（fail-closed if present in production，dev 模式 log warn） | 0.5 codex-day |

**P1 合计估时**：~5-6 codex-day。

### §5.3 P2 暂缓

- F-3 自动切换（roadmap）
- mTLS hot-reload
- route_cache_ttl 复用
- heartbeat p95 / error_rate 直方图
- attempt_reporter worker 多线程
- 文档化未源的 heartbeat 字段（p95 / error_rate）注释

### §5.4 明确禁止（违反 directive）

- ❌ 用户系统：auth / user / api_key / session / passkey / 2FA
- ❌ 商业闭环：billing / pricing / payment / voucher / subscription / order / refund
- ❌ 业务账本：receipt / dispute / merkle / signer 在 Rust
- ❌ 前端：UI / template / 静态资源
- ❌ 新协议入口：embeddings / images / audio / rerank / realtime / assistants / vector store / batch / files / models endpoint
- ❌ 新 vendor：超过 OpenAI / Anthropic / Gemini 三族
- ❌ Go 业务逻辑迁移到 Rust（Plan / billing / audit / channel health 留在 Go 控制面）
- ❌ L4 pacing / L5 outbound IP pool / L6 主动反封禁（F-3 之外的任何反封禁深度扩展）
- ❌ i18n / 移动端 / 桌面 GUI
- ❌ DB schema / migration / SQL driver
- ❌ Reference 项目源码 import（clean-room 限制，永久禁止）

---

## §6 执行顺序（W11 + W12 + 指纹波）

### W11 波（安全边界，~5-6 codex-day）

1. **当前 commit**：P0-1 W11-D-2 完整闭合（B1 + B2 + TI-1 listener 测试探针）+ P0-6 mimicry 6 红修齐 → 拆 2 commit
2. P0-2 W11-A D-1a → 1 commit
3. P0-3 W11-C D-3 → 1 commit
4. P0-4 W11-D D-6 → 1 commit
5. P0-5 W11-E D-10 mimicry resolver bypass → 1 commit
6. P1-1 W11-A D-1b Phase 1（含 §4.5 P-1 字段写入侧）→ 1 commit（**末位放，因依赖 Go 控制面排期**）
7. P1-7 M1.L1.B mock 凭据剥除 → 1 commit（与 W11-D-2 同源，可合 P0-1 也可独立）

### W12 波（账务遥测，~5-7 codex-day）

1. **必须先 P0-7 W12-A D-4 spool**（3 sub-slice，最重单项）—— spool 持久化先就位，后续 usage 抽取与字节计量数据才有去处
2. P0-8 W12-B D-5 非流式 usage → 1 commit
3. P0-9 W12-C D-7 heartbeat 真值 → 1 commit
4. P1-2 W12-D D-8 429/408 重分类 → 1 commit
5. P0-10 W12-E D-9 字节 + O-2 connection gauge lifecycle → 1 commit

### 指纹波（~3-5 codex-day）

1. P1-3 W11-F L1 canary fail-closed gate → 1 commit
2. P1-4 W11-F L2 HTTP/2 fork not-wired gate → 1 commit（与 P1-3 同 commit 区，可合）
3. F-1 + F-2 真接线（synthesis plan §4，独立 spec 内）

### 测试基础设施（穿插各波内）

- P1-5 mock_control_plane fault knobs → 与 W12-A D-4 同 commit（D-4 测试强依赖）
- P1-6 Feature-matrix CI doc → release-checklist commit（W12 末位）

---

## §7 Owner 决策点

| # | 决策 | 推荐 | 备注 |
|---|---|---|---|
| **OD-1** | W11-D-2 B2 实施立即继续吗（约 ~30 min 实施 + Codex 复审）？| 推荐 ✓ | B2 是当前 commit 阻塞；P0-1 闭合后才能 push W11-D-2 完整 commit |
| **OD-2** | 是否同意 D-10 编号纠正（从 "stream 取消" → "mimicry resolver bypass"）？| 推荐 ✓ | 与 synthesis plan §2 D-10 权威定义一致 |
| **OD-3** | 是否同意 D-1b 降 P1（Phase 1 Manual First 兜底）？| 推荐 ✓ | Codex 论点更对：D-1b 不能 Rust 单边完成，需 Go 控制面同步 |
| **OD-4** | P0-6 mimicry 6 红修齐方向：改测试 vs 改实现？| 推荐改测试（让期望对齐 `BlockKnownGap` "feature-flag gap" 语义） | resolver 现实现的语义"feature 没编→ KnownGap"是合理的；测试期望 `BlockUnsupportedTemplate` 是更早的设计选择，与现在的资源分类不符 |
| **OD-5** | W11 当前在 `claude/rust-hardening` 分支，主线 `claude/phase-1`。W11 P0 commit 在哪个分支落地？| 推荐继续 `claude/rust-hardening`（已 push GitHub）后续 W11 收尾时合 phase-1 | 与 W4/W5 同 phase-1 主线汇合时点：W11 全部 P0 落 + cargo test 全绿后开 PR / merge |
| **OD-6** | 本稿是否取代 synthesis plan 作为 W11 / W12 实施 source of truth？| 推荐：synthesis plan 仍为各 D-x 详细 spec / acceptance gate 来源；**本稿 §5 P0/P1/P2 取代任何"是否做"的判断** | 两稿协同，不替换 |

---

## §8 失败模式 + 风险

- **R1**：W11-D-2 B2 测试探针选 unit vs 集成路径不当 → 探针不 mutation-resistant。**缓解**：本稿明确 TI-1 路径选 unit test（构造 minimal `GatewayState::new(test_config)`，tower::ServiceExt oneshot，断言 `state.attempt_reporter().enqueued_count()` 增 1，断言 last enqueued report.error_class 含 "mock"）。
- **R2**：P0-6 修向选错（改测试 vs 改实现） → 长期 hide 真问题。**缓解**：OD-4 决策。
- **R3**：D-1b 单边落地危险。**缓解**：Phase 1 Manual First feature flag OFF；Phase 2 待 Go 控制面消费 commit 后切。Owner 已批 §4.5 P-1。
- **R4**：D-4 durable spool 引入磁盘 IO 风险（损坏 / 满盘 / 重放重复）。**缓解**：bounded spool + ack marker + idempotency_key 重放幂等 + 满盘 disk-full 测试（synthesis plan §3 D-4 5 个 acceptance criteria 已列）。
- **R5**：Codex per-commit review 在 W11 / W12 每个 commit 上会产生新 finding。**缓解**：CLAUDE.md #8 termination criteria（2 rounds 上限对 P2/P3 form 类）；P1 实质安全/审计 findings 不计入终止条件，必修。
- **R6**：本稿 §5 P0 清单不全 → 后续发现新 P0 leaf。**缓解**：本稿不是 frozen 清单；W11 / W12 实施过程中新发现的 leaf 走 supplement spec（同 plan + synthesis pattern）。
- **R7**：本稿是 claude / codex 双稿合成；Owner 可能不同意 §4 conflict 裁定。**缓解**：§7 OD-1 ~ OD-6 显式列决策点；Owner 否决任一项时同步更新本稿 + 标 v2。

---

## §9 与现有 docs 的关系

- **取代**：无
- **补充**：`docs/process/plans/2026-05-22-rust-hardening-plan.md`（synthesis plan）—— 本稿 §5 P0/P1/P2 是 W11+W12 实施的优先级 source of truth；synthesis plan 仍是各 D-x 的详细 spec / acceptance gate
- **保留**：`docs/process/plans/2026-05-22-rust-hardening-plan-{claude,codex}.md`（历史平行稿）— 不动
- **新增**：本稿（synthesis）+ `2026-05-23-rust-tree-closure-claude.md` + `2026-05-23-rust-tree-closure-codex.md`
- **关联**：`docs/process/clean-room/mechanical-enforcement.md`（M1-M5 enforcement spec）；`docs/process/citation-cleanup-backlog.md`（P2/P3 citation deferred items）

---

## §10 Change Log

- **2026-05-23 v1**：synthesis 创建。基于 Owner 2026-05-23 directive "停止横向扩展只做树向闭环、只管 Rust 线"。Claude 草稿 + Codex 平行稿独立产出后综合。共识度 ~80%，3 冲突全部裁定，6 gap 互补全部采纳。Owner 决策点 6 项待确认。

---

**Clean-room-attestation**: original HUAKAI analysis; both Claude and Codex drafts read only HUAKAI L0 source; no copied source/comments/tests/schemas from non-permissive references.
