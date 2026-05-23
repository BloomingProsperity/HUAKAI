# 2026-05-22 Rust 网关加固计划（双稿合成定稿）—— W11 安全边界 + W12 账务遥测 + 指纹

> **Rust 并行线**（独立于 Go 线 W4…）。本稿 = `2026-05-22-rust-hardening-plan-claude.md` rev3.1 + `2026-05-22-rust-hardening-plan-codex.md`（CLAUDE.md #10 严格平行起草）**双稿合成定稿**。审批前不写任何实现代码；本会话是 clean-room specifier 污染（读了 sub2api LGPL + clewdr AGPL），实现必须新干净 Claude 会话接手。
>
> **状态：synthesis 定稿（2026-05-23）**。Codex 对 Claude 稿 rev3 终评 **APPROVE WITH MINOR**；Codex 平行稿独立起草 359 行作 evidence 归档于 `plans/`。
>
> **演进史**：rev1 → rev2 → rev3（Codex APPROVE WITH MINOR）→ rev3.1（Owner 决策 lock-in）→ **synthesis**（吸收 Codex 平行稿 6 处实施侧补充）。
>
> **Owner 决策（2026-05-23）已 bake-in**：（1）D-1b = β（控制面权威身份，需 §4.5 P-1）；（2）§4.5 P-1 已批 + P-2 已批 + P-3 已否定（转文档化）；（3）§7-A/B/E 推荐项默认通过（波次顺序 W11→W12→指纹、D-3 默认安全、L2 HTTP/2 走 L2-α）；（4）指纹 **L1-only canary 阻断**（L2 未闭环 profile 不准生产）；（5）合成走"Claude rev3.1 为基 + Codex 6 处补充"路径。**§7 全部决策已闭环；W11 全部 finding 均可立即开干。**
>
> **Synthesis 从 Codex 平行稿吸收的 6 处补充**：（1）§8 Implementation-ready acceptance gate map（28 gate 按 slice 整理）；（2）§8 显式 WSL2 verification commands per commit；（3）§4.5 Phase 1/2/3 proto 兼容计划；（4）§2 D-1b "Manual First" 静态 key 图作 P-1 控制面消费侧未就绪时的 canary 兜底；（5）§7-I L1-only canary 决策（已决阻断）；（6）§5 recommended commit grouping。
>
> **从 Codex 稿拒绝吸收**：D-4 简化版（"fail-closed on enqueue"无时序拆分，会重蹈 rev2 reject 覆辙——Codex 平行稿没看到自己 rev2 review 的时序架构发现）；O-2 误读（CI verification ≠ 审计原 dead `ACTIVE_CONNECTIONS` gauge）；指纹 0.75d 严重低估。这三处保留 Claude rev3.1 设计。
>
> ~~rev1 → rev2 / rev2 → rev3 / rev3.1 Owner 决策~~ 详细演进历史见 `-claude.md`（synthesis 已 bake 关键决策于上述 Owner 决策段，不重复展开）。

## 元数据

| 字段 | 内容 |
|---|---|
| Owner directive | "你就动 Rust 模块……rust 该做的你就去做"；"读源码，读借鉴项目相对应的功能模块……每个小功能都不能放过"；"借鉴项目是 sub2api 和 cliproxyapi 和别的类似这种反代的项目"；"你看 github 上有没有用 rust 写的这样的项目"（2026-05-22） |
| 范围 | W11 安全边界硬化（D-1/2/3/6/10）+ W12 账务遥测硬化（D-4/5/7/8/9/O-2）+ 指纹（L1 TLS 缺口 + L2 HTTP/2 接线） |
| 只动 | `exploratory/rust-core-gateway/merged/`（下称 `cg/`，crate 根 `cg/crates/core_gateway/`）。**绝不碰** `backend/`。`route.proto` 位于 `cg/proto/`，本计划含一组受控变更（§4.5），其 Go 控制面消费侧需 Owner 协调。 |
| 分支 | `claude/rust-hardening` |
| 审计来源 | `docs/process/research/2026-05-22-deep-audit-rust.md`（D-1..D-10）+ `docs/process/plans/2026-05-22-audit-remediation-wave.md` W11/W12 + O-2 |
| 参考挖矿（Go） | sub2api `Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427`（LGPL-3.0，未归档，2026-05-22 push）；cliproxyapi `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054`（MIT，未归档，2026-05-21 push）。3 份 specifier 拆解已逐条核验。 |
| 参考挖矿（Rust，rev2 新增） | clewdr `Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a`（**AGPL-3.0，仅释义、禁止 vendor**——copyleft 与 DR-002 冲突；未归档，2026-05-09 push，1167 stars）；smg `lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c`（Apache-2.0，未归档，2026-05-21 push，277 stars）；litellm-rs `majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254`（MIT，未归档，default-branch HEAD 2026-05-13，59 stars）。三仓 first-cite 时效与归档状态均已核验 CLAUDE.md #12。三份 Rust specifier 拆解在本会话上下文。**实施会话纪律（synthesis 强化，闭 Codex per-commit review 第 4 轮 P2）**：本计划列出的 SHA 自定稿（2026-05-23）起若被引用时已老于 **30 天**，实施会话必须先 re-fetch 对应 repo HEAD 并校验该 SHA reachable-from-default-branch（per AGENTS.md / CLAUDE.md #12 30-day rule），再依赖；90 天仅作 first-cite 入选时效窗口，不可作长期使用窗口。 |
| 成功标准 | 见 §1 |
| 估时（rev2 honest re-estimate） | W11 ~6.5 codex-day；W12 ~7 codex-day（C2 重）；指纹 ~8-12 codex-day。合计 **~21.5-25.5 codex-day**。rev1 ~15-19 偏低，C2 durable spool 与 D-1b 完整设计大于 rev1 估算。 |
| 影响面 | Rust 数据面请求入口、上游转发、流式 relay、attempt 上报、心跳、mimicry 后端、`route.proto` 受控演进。**不碰** Go 控制面实现、计费账本 schema、DB 迁移。 |
| clean-room | 本会话读非 MIT/AGPL 源（sub2api LGPL + clewdr AGPL）→ specifier 车道。本稿全程释义，无 verbatim 函数名/结构/注释/代码块；rev1 泄漏的三处上游标识符在 rev2 已替换为行为描述。实现由独立干净会话完成（§8）。 |

---

## §0 背景与方法

HUAKAI 的 Rust 核心网关（`cg/`）是反向代理数据面：收客户端请求 → 问 Go 控制面要 route plan → 自己转发上游、流式返回、上报 attempt。2026-05-22 Zone D 深度审计发现 10 个缺陷（D-1..D-10），加首轮遗留 O-2，分两波：**W11 安全边界**、**W12 账务遥测**。Owner 另把**指纹**（L1 TLS / L2 HTTP/2 客户端伪装）纳入本轮。

**方法（按 docs/22 Deep Mining + CLAUDE.md #11/#12）**：
1. 逐行核验 HUAKAI 自身源码确认每个 finding 属实（已完成，11/11 confirmed）。
2. clean-room specifier 深挖参考反代/网关项目对应功能模块——Go 侧 sub2api/cliproxyapi（rev1）+ Rust 侧 clewdr/smg/litellm-rs（rev2 新增）——产出带 `<repo>@<sha>:<file>:<line>` 引用的行为拆解。
3. 本计划逐 finding 给出：HUAKAI 现状 → 参考做法（多源） → 修法 → **融合升级 delta（架构/算法/生态维度）** → 判别性测试 → 切片。
4. §4.5 集中列出 `route.proto` 受控变更集，使跨线协调单点化。

**rev1 → rev2 闭环 Codex 4 HIGH + 4 MED + 1 LOW**：
- HIGH-1（D-1 身份路径未闭合）→ §2 D-1 完整重写：拆 **D-1a**（body 取 model/stream，无契约改）+ **D-1b**（身份从认证凭据派生，α/β/γ 三选项 + Owner 决策点 H + §4.5 受控 proto 变更 P-1）。**关键新事实**：核验确认 Rust crate 内无客户端认证、无 `APIKeyResolver`、`RouteQueryRequest` 无凭据字段——这是 D-1 真正的"底"，rev1 未点透。
- HIGH-2（D-4 二选一不可执行）→ §3 D-4 **已定 C2 durable spool**，附 5 条 acceptance criteria（溢出无丢/崩溃恢复/重放幂等/磁盘满/控制面长期不可用）+ 红线 + 自证测试。
- HIGH-3（D-5 reconciliation 字段未定）→ §3 D-5 复用现有 `TokensUsed.source`（proto 已通过 `into_proto` 透传，验证见 `attempt_reporter/metrics.rs:8`），定义词表 `response_body` / `pending_reconciliation`，**无 proto 改动**；附 missing/bad-JSON/OpenAI/Anthropic 四类判别测试。
- HIGH-4（clean-room 上游标识符泄漏）→ 通稿替换为行为描述。
- MED-1（D-3 测试单薄）→ §2 D-3 加配置实体化、host-allowlist/私网/DNS-rebinding 测试、"guard 必须在转发路径上"陷阱断言（litellm-rs 教训）。
- MED-2（D-7/D-8 contract 不对齐）→ D-7 字段已存在拉真值（不需 proto）；D-8 核心重分类不需 proto；reset 时间作为 §4.5 P-2 可选项。
- MED-3（指纹无内置测试）→ §4 F-1/F-2 判别性测试已折入本计划。
- MED-4（D-6 测试偏窄）→ §2 D-6 补 `openai-project`、残留 client auth/key 剥除、route-plan 注入保留三类断言。
- LOW（O-2 二选一）→ §3 O-2 **已定接真实生命周期**（与 D-7 一致），附 acceptance 测试。

**rev2 → rev3 闭环 Codex 1 HIGH + 2 MED**：
- HIGH（D-4 AC-4 时序架构不可执行）→ §3 D-4 重写：拆 **pre-commit gate**（spool reservation 失败 → 转发上游**前** 503，唯一可改 HTTP 结果的点）与 **post-commit drop**（响应头已提交 → HTTP 不可改，loud metric/alert + durable spool cap 内兜底）；AC-4 拆为 AC-4-pre/AC-4-post；AC-5 加 watermark 触发 pre-commit gate。架构现实：响应头一旦送出，HTTP 不可逆，rev2 旧 AC-4 的"返回 5xx"对 post-commit 不成立。
- MED（D-5 缺 bad-JSON 测试）→ §3 D-5 测试加 T5（结构不完整/语法错 JSON → `pending_reconciliation`）。
- MED（D-6 注入保留偏窄）→ §2 D-6 T2 同时断言注入的 `openai-organization` 与 `openai-project` 保留。

**环境**：`cg/` 用 `tokio::net::UnixStream`（控制面 UDS 传输），Linux-only，Windows 编译失败。已在 **WSL2 Ubuntu** 装 Rust 1.95，`cargo build -p core_gateway` 在 Linux 下 55s 干净通过。实现 + `cargo test` 在 WSL 跑，`CARGO_TARGET_DIR` 置于 WSL 原生盘。

---

## §1 范围与成功标准

**做**：W11（5）+ W12（6）+ 指纹（2 大块）+ §4.5 受控 proto 变更，共 13 个工作项的源码级修复 + 判别性回归测试。

**不做**：① 不碰 `backend/` Go 线实现；② proto 变更限 §4.5 列出的受控集，不在其外扩散；③ 不改 HUAKAI 计费账本/schema；④ 不引入新 runtime 依赖（除指纹 L2 见 §4 决策点）；⑤ 不读 rquest/curl_cffi/wreq/utls 源码（沿用 `ja3_wire.rs` 既定纪律）。

**成功标准**（每项必须全满足）：
- 每个 finding 有源码级修法 + 至少一个**判别性**回归测试（CLAUDE.md #14：把缺陷植入 → 测试必须变红；fixture 在代码正确/损坏时输出不同；自证测试同测对照两路）。
- `cargo test -p core_gateway` 在 WSL 全绿；`cargo clippy` 无 warning。
- 每次 commit 前 `codex exec review --uncommitted` 处理 HIGH。
- 每个 finding 的融合升级 delta 可一句话说清，落在架构/算法/生态某一维或多维（CLAUDE.md #12 三维分类）。
- 无 clean-room 违规——不抄参考项目函数名/结构/注释/代码块；clewdr AGPL **仅释义、禁止 vendor**；smg Apache-2.0 / litellm-rs MIT 本稿亦仅释义，vendoring 决策留给实现会话按 CLAUDE.md #12 permitted-license 政策。

---

## §2 W11 安全边界硬化（先做）

### D-1 路由规划信可伪造 header / 身份完全未认证（HIGH）

**HUAKAI 现状**：`cg/.../src/listener.rs:83-85` 把 `request.headers()` 直接交给 `account_planner().plan()`；`account_planner.rs:201-223` `build_route_query` 从 `x-tenant-id`/`x-huakai-model`/`x-huakai-session-hash`/`x-huakai-stream` 取 tenant/model/session/stream，缺失分别落 `default-tenant`/`unknown`。**核验新发现（rev1 未点透）**：crate 内**没有客户端认证环节**、**不存在 `APIKeyResolver`**、`route.proto:28-38` `RouteQueryRequest` **没有任何客户端凭据字段**。任何客户端都可伪造 `x-tenant-id`（跨租户污染）、`x-huakai-model`（低价改高价）、`x-huakai-stream`，控制面无能力辨别。

D-1 拆为两条修法：

#### D-1a：model/stream 取请求体（无契约改动，可立即开）

**参考做法（多源一致）**：clewdr 上游账号选择全服务端、model 取自请求体 `model` 字段（`Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/services/cookie_actor.rs:176-199`，`Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/claude_code_state/chat.rs:55`）；litellm-rs 路由从已解析 JSON 请求体取 model 名走服务端路由表选择（`majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/server/routes/ai/chat.rs:281-308`）；sub2api 路由输入只从 body 取且非字符串 model 严格 400（`Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/service/gateway_request.go:151-166`）。**三源一致：model/stream 取自 body，不取自 header。**

**修法**：① 在已有 `max_body_bytes` 上限保护下解析请求体 JSON 顶层；② `model` 取自 body，非字符串 → 400 严格拒绝；③ `stream` 取自 body 的 `stream` 布尔字段，非 bool → 400；`Accept: text/event-stream` 仅作传输层 hint 不作账务输入；④ 不再读 `x-huakai-model`/`x-huakai-stream` 作权威源；保留它们仅当来自可信内部前置层且经 D-1b 验证。

**融合升级 delta**：算法升级（严格类型拒绝，不静默 coerce）+ 架构升级（路由账务输入与传输层信号解耦）。delta：clewdr 单租户，model 来自 body 即足够；HUAKAI 多协议（Anthropic Messages + OpenAI Chat Completions）需在同一入口对两种 body schema 做一致提取，由 `protocol` 派生 JSON-path——HUAKAI 的精细度差异。

**判别性测试**：
- T1：body `model` = 高价模型 + header `x-huakai-model` = 低价模型 → 断言 route query 用 body 高价模型。Mutation：回读 header → 红。
- T2：body `model` 为数字（非字符串）→ 断言 400。
- T3：header `x-huakai-model` 存在 + body 无 `model` → 断言**不静默回落 header**（按选定政策：400 或显式 `unknown`，不能默默用 header）。
- fixture 关键：body 模型与 header 模型必须**显式不同字符串**，否则正确/损坏两路输出相同，测试不判别。

**切片**：1 commit `listener 路由模型与流式开关改取请求体`。

#### D-1b：tenant 身份从认证凭据派生（HIGH，需 §4.5 P-1 决策）

**参考做法（多源，smg 为金本位）**：
- smg 在中间件按严格优先级解析租户身份（`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/middleware/tenant_resolution.rs:58-73`）：① bearer auth 中间件已存放的"已认证调用方"对象赢；② **显式 opt-in 的"可信内部 header"模式**（默认 OFF）；③ 客户端 socket IP 兜底；④ 匿名 sentinel。身份对象在 request extensions 传递，下游处理器从那里读，**不再从 wire 重读**（`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/middleware/tenant_resolution.rs:121-139`，`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/tenant.rs:90-133`）。Bearer token 经 SHA-256 常时比较，租户键**由 hash digest 派生**——客户端无法自选键（`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/middleware/auth.rs:35-66`，`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/tenant.rs:154-164`）。身份来源类型保留在 enum（authenticated/header/IP/anonymous，`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/tenant.rs:11-32`）。
- clewdr：账号选择从不读客户端 header/body 决定（`Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/services/cookie_actor.rs:176-199`）。单租户，无 tenant 抽象。
- sub2api：身份全服务端派生，认证中间件从 Authorization/厂商 key 取凭据 → 解析 key 记录 → 加载 user/group/平台/订阅写 ctx，明确拒绝 URL query 凭据（`Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/server/middleware/api_key_auth.go:32-77`）。

**修法 — 采纳 smg 优先级链 + Owner 拍板**：

HUAKAI 数据面无 key 库（key 库在 Go 控制面），"在 Rust 内本地认证"不可行。三个可行选项：

- **β（推荐）控制面权威身份**：`RouteQueryRequest` 增 `client_credential` 字段（或走 gRPC request metadata 携带，避免 .proto diff 但类型化弱）。控制面认证凭据 → 派生 tenant/user → 路由 → 返回 plan，**一次 RPC 内完成**。数据面零身份权威。需 §4.5 P-1 + Go 线协调。
- **α 独立 `ResolveIdentity` RPC**：Rust 先解 identity → 缓存 → 再 RouteQuery。多一跳；契约面更大。
- **γ 受信前置层 header（即 smg 的 opt-in 模式）**：默认 OFF，开启时仍需校验请求来自受信前置（peer mTLS 或签名 header）。无 proto 改动。仅严格部署拓扑下安全；数据面一旦暴露身份就崩——只能作 β 的次要 opt-in。

**已选 β**（Owner 2026-05-23 决策，详见 §7-H）：β 为生产路径；γ trusted-header opt-in 暂不开放（如未来部署拓扑确认有受信前置层认证可再讨论）；α 不采纳。`x-tenant-id` 在所有路径下**永不被信任**。

**Manual First 静态 key 图（canary 过渡兜底，synthesis 吸收自 Codex 平行稿，按 Feature Preservation Rule）**：β 需要 §4.5 P-1 在 Go 控制面对应消费侧实现（详见 §4.5 "Phase 1/2/3 兼容计划"）。若 Go 线 spec/排期未就绪时 Rust 端需先上 canary，提供**过渡 Manual First**：Rust 数据面引入显式 feature flag 的**本地静态 key 表**（YAML/TOML 配置，明文 prefix + secret hash），仅 canary 环境可启用 + 启动时显著日志告警 + 默认 OFF。canary 数据面读本地表认证客户端、派生 tenant、填 `RouteQueryRequest.tenant_id`（旧字段，与 P-1 新字段双写）；控制面消费侧（Phase 2）上线后此 feature 永久下线。本地表内容必须可审计、可热轮换、不与生产真实 key 库混用。**这是过渡 Manual First，不是常驻能力。**

**融合升级 delta**：架构升级——数据面零身份权威，客户端 wire 永不被信任为身份源。clewdr/litellm-rs 服务端派生但单进程；smg 有优先级链但其 trusted-header 模式是弱点；HUAKAI 把"已认证凭据"路径作默认、trusted-header 作 provenance-tagged 的 default-off 次路径，**在数据/控制面分离拓扑里让控制面（key DB 持有者）成为唯一身份权威**——smg 身份停在 HTTP 前端，HUAKAI 跨内部契约下沉。维度：架构。

**判别性测试**（β-only — §7-H 锁定 γ 永久禁；测试只验"`x-tenant-id` 永不被信任"）：
- T1：凭据解析为 tenant A + 伪造 `x-tenant-id: B` → 断言查询/计划的 tenant 为 A，**永远不是 B**。Mutation：回读 header → 红。
- T2：请求带 `x-tenant-id: X` 但**不带任何客户端凭据** → 断言请求被拒（401）；任何派生 tenant = X 的代码路径都视为缺陷（β-only 政策：`x-tenant-id` 在 Rust 数据面所有路径下永远被忽略）。Mutation：在 `account_planner` / `listener` 任何一处回读 `x-tenant-id` 派生 tenant → 红。
- T3（roadmap 守护，**本轮不实现** γ）：未来如部署拓扑确认需 trusted-header 模式，必须**新一轮 Owner 决策 + 单独 spec** 重启；本计划基线**不预留**任何 `x-tenant-id` 信任路径；任何在本计划基线上添加 `x-tenant-id`-as-identity 信任逻辑的 PR 应被 reviewer 拒绝。此条作为 reviewer 检查要求，不需 runtime 测试。
- fixture 关键：A 与 B 必须为两个**显式不同**租户值（不能用 "default-tenant" 这种默认值，会被默认逻辑掩盖）；T2 的 X 不能等于任何默认 tenant 值。

**切片**：1-2 commit `account_planner 租户身份改由认证凭据派生`（条件：§4.5 P-1 已 Owner 批 + Go 控制面同意排期）。

### D-2 `HUAKAI_MOCK_UPSTREAM_ENDPOINT` 生产绕过（HIGH）

**HUAKAI 现状**：`config.rs:274` 从环境变量读 `mock_upstream_endpoint`，无任何生产守门；`listener.rs:72` 只要它存在就直接 `forward_endpoint`，**跳过** `plan()`、账号选择、attempt 上报（`proxy_engine/mod.rs:258` 传 `planned=None, terminal_reporter=None`）。生产残留该变量 → 真实流量直发 mock、零账务记录。

**参考做法**：sub2api 数据面无任何运行期 mock/test 旁路——上游 transport 是 interface，fake 只在 `*_test.go` 由构造器注入，无 env/config 开关在请求期换 fake（`Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/service/account_test_service.go:173-321`）。clewdr 行为同向：所有安全相关旋钮是运行期配置 + 默认 fail-closed，cargo features 仅控资源打包不静默改 auth/transport 安全（`Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/config/clewdr_config.rs:103-110`，`Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/config/constants.rs:138-140`）。

**修法**：① mock upstream 仅 test/dev build 或显式非生产模式可用；② 生产启动若检测到该变量 → **fail-fast 拒绝启动**（参 `config.rs:337` `require_loopback_endpoint` fail-fast 范式）；③ 如保留人工演练入口，必须生成明确 attempt/audit 记录且禁注真实凭据。

**融合升级 delta**：架构升级 + 生态升级。delta：sub2api 靠"fake 根本不构造"的开发纪律（结构性安全，无显式断言）；clewdr 靠"安全旋钮全是运行期配置"的纪律——HUAKAI 更进一步加 fail-closed 启动断言，把"生产不可能跑 mock"从纪律变为强制不变量。

**判别性测试**：设 `HUAKAI_MOCK_UPSTREAM_ENDPOINT` + 生产模式 → 断言启动失败；非生产模式 → 断言可用但日志显著标记。Mutation：去掉守门 → 红。

**切片**：1 commit `config mock 上游生产启动守门`。

### D-3 planned vendor endpoint 允许明文 HTTP（HIGH）

**HUAKAI 现状**：`account_planner.rs:249` 只校验 `vendor_endpoint` 有 scheme+authority，**不要求 https**；`proxy_engine/http_client.rs:32` connector 是 `https_or_http()`。控制面配错/被污染返回 `http://…` → 上游 Bearer + 用户 prompt 走明文。

**参考做法（四源——**没有一个**强制 https，litellm-rs 教训最尖锐）**：
- sub2api：两级 validator 但**默认不安全**——URL 白名单默认关（`Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/config/config.go:1513`）、允许私网（`Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/config/config.go:1528`）、允许明文 http（`Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/config/config.go:1529`）；白名单关时只跑 format validator（不查 host、不防 SSRF）。定义了 resolved-IP validator 防 DNS-rebinding（`Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/util/urlvalidator/validator.go:108-126`）但**转发路径未接线**（仅作 schema 校验，未在 dialer/transport 中调用）。
- clewdr：上游基底是硬编码 https 常量（`Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/config/constants.rs:14`），**但 operator 反代覆盖无 scheme 检查**（`Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/config/clewdr_config.rs:377-382`）——可被运维错配引入明文；不程式化强制 https。
- smg：worker URL 校验放行 `http`/`https`/`grpc`/`grpcs` 四选一白名单，明文通过（`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/config/validation.rs:828-873`）。"反例"参考。
- litellm-rs：有 SSRF guard 拒绝非 http(s)/ws(s) + 阻断私网/loopback/metadata（`majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/core/net/ssrf_guard.rs:39-99`）；**但允许明文 http**，且 guard **未接到 provider 请求路径**——provider client 直接 `reqwest` 凭配置 base URL 调（`majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/core/providers/openai/client.rs:116`）。**"guard 存在但不在转发路径上"的典型陷阱。**

**融合升级 delta**：架构升级——HUAKAI 默认安全，反转四源参考的不安全默认。

**修法（HUAKAI 做得比所有参考都好）**：① 生产 planned endpoint **强制 https**，明文 http 仅 mock/test 路径且不带真实凭据；② 按 vendor 做 host allowlist；③ connector 改为 https-only（去掉 `https_or_http` 的 http 分支）；④ 接一个 resolved-IP 校验（或 pinned-dialer 重校验已连 IP）防 DNS rebinding；⑤ **校验必须在真实转发路径上生效**——通过端到端测试断言而非孤立 validator 单测。

**具体配置（Codex MED-1 demand 实体化）**：单一 `upstream_security` 配置块——默认 `require_https=true` + `host_allowlist=<vendor 列表>` + `block_private_ip=true` + `validate_resolved_ip=true`。放宽是显式 opt-in 字段（如 `upstream_security.allow_plaintext_for_envs=[dev, test]`），启动时每个 opt-in 显著记入 startup log。

**判别性测试（强化 — 打转发路径，非孤立 validator）**：
- T1：控制面返回 `http://...` vendor_endpoint → 生产模式打实际转发路径 → 断言请求**拒绝**（不是 validator 单测，是真转发被挡）。Mutation：拒绝逻辑去掉 → 红。
- T2：endpoint host 不在 allowlist → 断言转发拒绝。
- T3：endpoint 解析到私网/loopback → 断言拒绝（依 `validate_resolved_ip`）。
- T4：`allow_plaintext_for_envs=[dev]` + dev 模式 → 断言 `http://` 放行 + startup log 显著告警；prod 模式 → 断言仍拒。
- fixture 关键：必须在**完整请求流**里断言，不能只测 validator 函数——litellm-rs 教训。

**切片**：1 commit `account_planner 强制 https 上游与 host 校验`。

### D-6 客户端 org/project header 透传上游（MED）

**HUAKAI 现状**：`proxy_engine/headers.rs:54-56` 把客户端 `openai-organization`/`openai-project` 列入转发白名单，与网关自己的 Bearer 一起发上游。客户端可借此把账单/授权归到别的 org/project。

**参考做法（多源融合）**：
- sub2api：**按信任域分离的严格 allowlist**（Anthropic、OpenAI Responses、OpenAI raw-relay 各独立小 allowlist）；上游 auth **永远服务端设**，客户端 auth 不在任何 allowlist；OpenAI 路径 allowlist 拷贝后**再显式删** residual `authorization`/`x-api-key`/`x-goog-api-key`；厂商账号选择 header 服务端设；session id 按 API key **命名空间化重写**防跨租户串（`Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/service/gateway_service.go:359-381`，`Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/service/openai_gateway_service.go:3179-3224`）。
- clewdr：**根本不转发客户端 header 集**——重建请求、服务端设 header，上游 auth 永远服务端铸（`Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/claude_code_state/chat.rs:128-144`）；只回显 `anthropic-beta`（合并）。drop-by-omission 的结构性保证。
- smg：内部路径 allowlist 显式**阻断 `x-api-key` 透传**（测试断言），上游 auth 按 provider 服务端铸；错误响应体里 upstream org/project 标识被正则**擦除**后回（`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/routers/common/header_utils.rs:289-331`，`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/routers/error.rs:161-203`）。
- litellm-rs：上游 auth 从 provider 配置服务端铸，org/project header 来自 config 而非客户端（`majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/core/providers/openai/client.rs:48-69`）。

**修法（多源融合）**：① 客户端的 provider-account-scoping header（org/project/账号选择）**剥除**，只允许 route plan/控制面显式注入；② 保持按信任域分离的 allowlist；③ 所有上游请求构造走**一个共享 allowlist 应用 helper**（补 sub2api 三处分散的 gap）；④ 显式阻断 `x-api-key`/`authorization` 残留（采 smg）；⑤ 借鉴 clewdr "重建请求" 的结构保证作为长期演进方向。

**融合升级 delta**：架构升级——结合 clewdr 的"重建请求"结构保证 + sub2api 的"按信任域 allowlist" + smg 的"显式残留剥除 + 错误体擦除" + 单一共享 chokepoint 闭合 sub2api 的散点风险。

**判别性测试（强化 — Codex MED-4）**：
- T1：客户端同时带 `openai-organization`、`openai-project`、`x-api-key`、`authorization` → 断言**四个均不**到上游。
- T2：route plan/控制面显式注入的 `openai-organization` **和** `openai-project` headers → 断言**两者均到达**上游（被保留，不被误剥）。Mutation：注入路径误剥 → 红。**T2 同时验证剥除（T1）与保留（T2）两条逻辑，并覆盖 project 命名 — 闭 Codex rev2 MED**。
- T3：上游响应体含 org-/proj- 标识 → 断言错误回路擦除（如采 smg）。
- Mutation：把任一客户端 header 加回 allowlist → T1 对应断言红。
- fixture 关键：必须同时测 `openai-project`（不止 `openai-organization`）+ 剥除 + 注入双向。

**切片**：1 commit `proxy_engine 剥除客户端账号归属 header`。

### D-10 `mimicry-boring` feature 绕过 fail-closed（MED）

**HUAKAI 现状**：`mimicry/backend_resolver.rs:77` `resolve_vendor_mimicry_backend` —— 只要编了 `mimicry-boring` feature 就**立即返回 `Boring`**，完全跳过 `profile.backend_intent()` 的 KnownGap 阻断（`mimicry/profile.rs:181-208`）。未过 R-D 验证的 vendor profile（Kiro rustls 模板等）在生产构建被直接放行。

**参考做法**：HUAKAI 内部逻辑顺序 bug，参考项目无直接对应模块（cliproxyapi 用固定 utls preset，无此分级 gate）。**原则佐证**——clewdr 安全相关行为由运行期配置门控且 fail-closed，cargo features 不被设计为"静默改安全行为"的载体（`Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/config/clewdr_config.rs:103-110`，`Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/config/constants.rs:138-140`）。D-10 恰是反例：build feature 绕过运行期 gate。

**修法**：`resolve_profile_mimicry_backend` 必须**先执行 `backend_intent()`**、尊重 unsupported/known-gap 阻断；只有明确支持 Boring 且通过验证的 profile 才返回 `Boring`。补 feature-matrix 测试覆盖 `mimicry-boring` 组合。

**融合升级 delta**：算法升级（修复后端选择判定顺序）+ 原则佐证（与 clewdr"安全行为不由 build feature 静默控制"一致）。

**判别性测试**：`mimicry-boring` feature ON + Kiro known-gap profile → 断言被 fail-closed 阻断（非 AllowBoring）。植入缺陷（恢复提前 return）→ 红。注意：现有 `mimicry_dispatch_test.rs:173` 不覆盖 feature 组合，必须新增带 feature 的测试。

**切片**：1 commit `mimicry 后端选择先过 intent 闸再返回 Boring`。

---

## §3 W12 账务遥测硬化（W11 闭合后）

### D-4 terminal report 队列满/重试耗尽丢账（HIGH，已定 C2）

**HUAKAI 现状**：`attempt_reporter/mod.rs:27` 队列 cap 1024；`:140-160` `report()` 用 `try_send`，Full → `DroppedFull`（计数器）；`:207-268` `send_with_retry` 在 `retry_attempts`（默认 3）耗尽后 → `failed_reports`（计数器，报告丢）；`proxy_engine/relay.rs:382` 与 `listener.rs:160` 用 `let _ =` 忽略丢弃结果。**两个丢点**：入队溢出 + worker 投递耗尽。成功的可计费请求 → terminal report 静默丢 → 账本空洞。

**参考做法（四源——**没有一个**有 durable spool）**：
- sub2api：响应后 best-effort 投有界 worker 池，**溢出时按采样策略丢弃**，无 durable spool；进程崩溃整个内存队列全丢（核验已确认，rev1 此处的上游字段标识已替换为行为描述）。cite anchor：`Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/service/usage_record_worker_pool.go`（worker pool overflow 采样行为）+ `Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/repository/usage_billing_repo.go`（账务 repo 无 spool/WAL/durable 写入路径）。
- clewdr：用量持久化是**内存计数 + 周期快照**（计数活在内存，整 config 定期 TOML 落盘）；**无 per-request durable ledger**，两次快照之间崩溃丢最近增量（`Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/services/cookie_actor.rs:58-79`）。
- smg：用量只作进程内 Prometheus 风格计数器；**无 durable ledger、WAL、可生还账记录**；最 best-effort。cite anchor：`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/observability/metrics.rs`（metric facade 仅内存 Prometheus 计数器，无持久化路径）。
- litellm-rs：**最差**——budget tracker 是内存 DashMap、无持久化，崩溃丢全部 spend（`majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/core/budget/tracker.rs:16-21`）；log aggregator 内存 Vec，flush 失败时条目已 drain → **丢而不重试**（`majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/core/observability/logging.rs:57-72`）；全仓库无 spool/outbox/dead-letter/idempotency。

**决策已定**：**C2 — 本地 durable spool**。Owner round-2 delegated（"[No preference]"）；rev2 经四源证据再确认——每个参考都在过载或崩溃时丢账，durable spool 是没人补的洞，**真融合升级**而非抄袭。C1（有界阻塞回压）不是独立选项，而是 spool 自身的兜底降级路径。

**修法（C2 完整 spec — rev3 时序架构修正）**：

**关键架构事实**（rev2 漏掉，Codex 在 rev2 评审指出）：Rust 数据面**响应头一旦提交给 client，HTTP 结果不可逆转**。当前 Rust 路径在 `proxy_engine/mod.rs:327` 把上游响应包成 streaming body 返回，terminal report 发生在 body relay / stream 生命周期中（`relay.rs:373,382`，且当前用 `let _ =` 忽略 `report()` 结果）。响应头已送出后再说"spool 写失败就改 5xx"——架构上不可能，HTTP 已经在线了。

因此 D-4 必须区分两个失败时序：

- **Pre-commit gate（响应头送出**前**）**：是唯一可以"用 HTTP 结果保护账务"的点。进上游转发**之前**做 spool 健康预留（spool depth < watermark + 上次 spool 写未失败 + 内存队列残留容量充足）。预留失败 → forwarder **立即返回 503**——请求未进上游、未产生计费事件、未提交响应头。这是 fail-closed 真生效的地方。
- **Post-commit drop（响应头送出**后**）**：响应头已提交给 client → HTTP 结果**不可改变**（200 维持）。处理：① durable spool 在 size cap 内重放兜底（`idempotency_key` 已存在，幂等消费）；② 一旦真发生 drop（spool cap 超出 或 spool 物理故障），`spool_drop_billable` metric 致命级告警 + 结构化日志（含 request_id + idempotency_key + tenant + 可计费 token 数）page on-call；③ 调用方禁 `let _ =`——post-commit 失败必须计 metric。
- **不变量**：pre-commit gate 失败 ⇒ 503 拒；post-commit drop ⇒ HTTP 不可逆但 loud 告警 + spool 在 cap 内零丢失。**rev2 旧 AC-4 的"返回 5xx"在 post-commit 不成立，已修正。**

**落地机制**：
- 新增 `AttemptReporter::reserve()`（或类似 RAII guard）在 forwarder 进入上游转发前调用。Reservation 失败 → forwarder 抛 503。
- terminal `report()` 时消费预留 slot。Reserve 到 write 之间真发生 spool 物理故障 → 计 metric + 进入 durable 重放队列（不可逆，但 cap 内 spool 兜底）。
- terminal reporter 在 `try_send` Full **或** worker 重试耗尽时，**不丢弃**，而是把报告写入**本地 durable spool**（配置目录下的 append-only 文件，按 size/time roll）。
- spool 重放 worker 排空 spool：成功 ack → 标记消费；持续失败 → 留待下一轮。
- 幂等：每个报告已携 `idempotency_key`（`route.proto:97`，由 `request_id+attempt_id+acquisition_token` 派生）；重放沿用同 key；控制面按此 key 去重 → at-least-once 投递 + 幂等消费者 = 实质 exactly-once。
- 调用方禁 `let _ =`——入队/spool/post-commit 结果必须处理（至少 metrics + 日志；可计费 post-commit drop 作为致命告警路径）。
- spool 有界：max 大小可配；spool depth、replay lag、`spool_drop_billable` 都暴露为 metric。

**Acceptance criteria（rev3 — 6 条红线，按时序拆分）**：
- **AC-1 溢出无丢**：内存队列满 + 提交一条可计费成功报告 → 报告落 spool 并最终被投递（不丢）。
- **AC-2 崩溃恢复**：报告已写 spool 但未 ack → 模拟进程重启 → 重放 worker 重读 spool 并投递。
- **AC-3 重放幂等**：报告投递成功后被重复重放（模拟"发送后标记消费前崩溃"）→ 控制面按同 `idempotency_key` 去重 → 单一效应（不重复计费）。测试断言消费者去重。
- **AC-4-pre（pre-commit gate）**：spool 预留检查不通过（spool depth ≥ watermark 或写测失败）→ forwarder 在转发上游**之前**返回 503；请求未进上游、未产生计费事件、未提交响应头。Mutation：去掉 `reserve()` 调用 → 测试断言 503 不发生 → 红。
- **AC-4-post（post-commit drop）**：响应头已提交给 client 之后 spool 写失败（cap 已满 + 控制面长期下行）→ HTTP 响应不变（200 维持）；`spool_drop_billable` metric +1；结构化日志含 request_id + idempotency_key + tenant + token 数。Mutation：去掉 metric/log 路径 → 红（漏告警）。**测试同时断言 post-commit 不试图修改 HTTP 结果**——响应头/状态码与正常路径相同。
- **AC-5 控制面长期不可用**：控制面长期不可达 → 报告堆 spool 至 cap；恢复后 worker 排空 backlog；上限内零丢失；接近 cap 时 watermark 触发，pre-commit gate 开始 503 新请求（AC-4-pre 路径）；超 cap 走 AC-4-post 降级。

**判别性测试（自证 — CLAUDE.md #14）**：每条 AC 一条红线测试。头条**自证**测试：填满队列 → 提交可计费成功报告 → 同测内跑 spool ENABLED 与 spool BASELINE（关）两路 → 断言两路结果**不同**（ENABLED：投递/可重放；BASELINE：丢）。Mutation：去掉 spool 写 → ENABLED 路变红。fixture 关键：必须是真的可计费成功（status=Success、http 200、真 token），不能用空 stub——否则丢失也无意义。

**融合升级 delta**：生态升级（崩溃可生还账务持久化）+ 架构升级（数据面内嵌 append-only outbox）。delta source-checkable：clewdr 周期快照丢窗口、smg 内存计数器崩溃丢、litellm-rs flush 失败 + 崩溃双丢、sub2api 溢出采样丢——HUAKAI durable spool + 幂等重放在 bounded cap 内零丢失、超 cap 显式降级。**没有任何参考做到这一点。**

**切片（D-4 是最重单项 — 2-3 codex-day）**：
- 切片 1：`attempt_reporter 计费上报落本地 durable spool`（spool 数据结构、写路径、关闭条件）。
- 切片 2：`attempt_reporter spool 重放 worker 与崩溃恢复`（启动重放、ack 标记、replay metrics）。
- 切片 3：`attempt_reporter spool 满盘降级与调用方丢弃处理`（disk-full 路径、`let _ =` 替换、5xx 降级）。

### D-5 非流式响应不解析 usage（HIGH，无 proto 改动）

**HUAKAI 现状**：`relay.rs:64` 只给 SSE 响应建 `StreamUsageTap`；非流式只 `record_body_chunk` 数字节、不解析 body 里 `usage`；`attempt_reporter/types.rs:208-210` 缺 token 时填 `AttemptTokenMetrics::missing()` → `source = "missing"`（`attempt_reporter/metrics.rs:12-17`）。`stream_pipeline/openai.rs:63` 已有非流式 usage 解析函数但转发路径不调用 → 普通非流式 token 漏记。

**参考做法（四源——三个解析、miss 处理均欠佳）**：
- clewdr：读完非流式 body、解 `usage` 抽 input/output token；**缺时回退用本地 tokenize 估算 output**（`Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/claude_code_state/chat.rs:403-452`）。估算不可审计、可能错。
- smg：Anthropic 路径反序列化非流式 body 抽 token + 记 metric（`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/routers/anthropic/worker.rs:88-108`）；**但 OpenAI 兼容代理路径原样字节转发不解析**（`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/routers/openai/chat.rs:222-231`）——跨路径不一致。
- litellm-rs：每 provider transformer 各自解 usage；OpenAI 类型化路径缺 usage 时给 `None`（无错）；**Anthropic 手解 Value tree 用 get-then-coerce + 默认 0**——一个 numeric 字段缺失/非数字被**静默计费为零，无任何标记**（`majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/core/providers/anthropic/client.rs:730-767`）；**全仓库无 reconciliation 机制**；**非流式无 body size cap**（`majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/core/providers/openai/client.rs:120-142`）；OpenAI 客户端**解析前不检 HTTP status**——错误响应可能被当成 success 解析。
- sub2api（rev1 已挖）：OpenAI 路径缺 usage 直接硬报错；Anthropic 某些子路径缺 usage 静默计零——前后不一致。

**关键发现**：`AttemptReportRequest.tokens_used.source`（proto field 4，已 plumb through `into_proto()`，`attempt_reporter/types.rs:299`）是字符串。当前词表 `"missing"`/`"stream_pipeline"`（`attempt_reporter/metrics.rs:12-17, 23-26`）。**D-5 不需 proto 改动**——扩展词表即闭合 reconciliation 风险态。

**修法（复用 `tokens_used.source` 词表，无 proto 改）**：
- 对非 SSE 成功（2xx）响应，在已有 size 上限保护下运行 JSON usage 抽取（复用 `extract_usage_from_json_bytes` / `stream_pipeline/openai.rs:63`），覆盖**两个命名族**——OpenAI `prompt_tokens`/`completion_tokens` 与 Anthropic `input_tokens`/`output_tokens`（含 cache 字段）。
- `TokensUsed.source` 词表：
  - `"missing"`：从未尝试抽取（保留，仅给确实无 usage 的场景）。
  - `"stream_pipeline"`：SSE tap 抽取（已存在）。
  - **新增 `"response_body"`**：非流式 JSON body 成功抽取。
  - **新增 `"pending_reconciliation"`**：非流式成功响应**已被检查**但 usage 无法抽取（缺失/坏 JSON/非数字）。控制面读到此值 → 路由 attempt 到对账队列；**不静默计零、不硬失败已交付响应**。
- 解析前必检 status 2xx（smg 做对，litellm-rs 没做——陷阱）。
- 解析前必受 body size cap 约束（litellm-rs 没有——陷阱）。

**融合升级 delta**：算法升级（两种 protocol 一致 miss 策略，smg/sub2api 都不一致）+ 生态升级（usage 抽取失败变可审计可对账记录——clewdr 静默估算、litellm-rs 静默 0 都不可审）。HUAKAI 是四源里**唯一**让 miss 可观察可修正的。

**判别性测试（Codex HIGH-3 demand — missing/bad-JSON/OpenAI/Anthropic 四类）**：
- T1 OpenAI happy：非流式 200 body 含 `usage:{prompt_tokens:100, completion_tokens:50}` → 断言 report tokens 100/50，`source="response_body"`。Mutation：不解析 → tokens 0、`source="missing"` → 红。
- T2 Anthropic happy：非流式 200 body 含 `usage:{input_tokens:100, output_tokens:50}` → 断言 100/50，`source="response_body"`。
- T3 缺 usage：非流式 200 body **无 `usage` 对象** → 断言 `source="pending_reconciliation"`，**非** `"missing"`，**非** tokens 0 作终值。Mutation：静默计零 → `source` 会是 `"missing"` 或 tokens 0 → 红。**这是 litellm-rs Anthropic 路径"数值字段缺失/非数字时静默回退为零"缺陷的精确镜像**（cite anchor：`majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/core/providers/anthropic/client.rs:730-767`）——fixture **必须**是 present-but-incomplete body，让"只验 happy 路径"的弱测试无法通过。
- T4 坏字段：非流式 200 body 含 `usage` 对象**但 `output_tokens` 非数字/缺失** → 断言 `source="pending_reconciliation"`，**不**对该字段静默 0。
- T5 坏 JSON 语法：非流式 200 body 是**结构不完整/语法错的 JSON**（截断 body、未闭合括号、非法字符）→ 断言 `source="pending_reconciliation"`，**非 silent `missing`**。Mutation：坏 JSON 走静默 missing 路径 → 红。**这是 rev3 新加，闭 Codex rev2 MED**。
- fixture 关键：T3/T4/T5 的期望输出必须与"损坏代码"输出**不同**——损坏代码给 tokens 0 + `source="missing"`，正确代码给 `source="pending_reconciliation"`；测试断言 `source` 值即判别。

**切片**：1 commit `proxy_engine 非流式响应解析用量并标记对账风险态`。

### D-7 heartbeat 硬编码假健康数据（MED，contract 已对齐）

**HUAKAI 现状**：`heartbeat.rs:73-83` `node_id` 固定串，`started_at`/`in_flight_requests`/`attempt_report_queue_depth`/`p95_*`/`error_rate_1m` 全硬编码 0。真实 gauge 已存在（`resource_limits.rs:96` 真 in-flight、`attempt_reporter/mod.rs:166` 真 queue depth）。

**参考做法**：clewdr 无 node health（单进程，无多节点概念）；smg 暴露**真实测量**信号——in-flight tracker（live map keyed by id + start timestamps + age histogram，驱动 graceful drain，`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/observability/inflight_tracker.rs:22-124`）+ per-worker 原子负载计数器由 **RAII guard tied to response body lifetime**（流式请求计到 stream 结束，`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/worker/worker.rs:638-654`）。smg **不**做 node 级 queue-depth/computed error-rate/latency-percentile 作健康信号——HUAKAI 的 headroom。

**关键发现**：`HeartbeatRequest` 字段（`route.proto:120-130`）**全部存在**：`in_flight_requests`/`open_upstream_connections`/`attempt_report_queue_depth`/`p95_control_plane_rpc_ms`/`error_rate_1m`。**D-7 核心不需 proto 改**——拉真值即可。`unknown vs 0` 区分（Codex MED-2 关切）：proto3 标量缺省 0 无法区分；如要区分需 `optional double`——列为 §4.5 P-3 可选项，**非 D-7 必须**。

**修法**：① 接 `in_flight_requests ← resource_limits`；② `attempt_report_queue_depth ← AttemptReporter::queue_depth()`；③ `open_upstream_connections ← O-2 拉真值后的 gauge`；④ `node_id ← 真进程标识`；⑤ `started_at ← 真启动时间`；⑥ `p95_control_plane_rpc_ms`/`error_rate_1m`：若有便宜真源就计算，否则 0 + 文档化"未接源"，并把 §4.5 P-3（optional 化）摆给 Owner；⑦ 借鉴 smg 的 **RAII-lifetime-guard idiom** 让 in-flight 在流式响应期间保持计数。

**融合升级 delta**：生态升级——真节点健康；HUAKAI 组合 smg 未组合的 node 级信号。

**判别性测试**：占满 in-flight（驱动真负载，非 mock）→ 断言 heartbeat `in_flight_requests` > 0。填满 report queue → 断言 `attempt_report_queue_depth` > 0。Mutation：回硬编码 0 → 两条均红。fixture 关键：必须驱动**真**负载使 0/非 0 形成判别。

**切片**：1 commit `heartbeat 接入真实节点健康指标`。

### D-8 429/408 误归不可重试 Upstream4xx（MED，核心不需 proto）

**HUAKAI 现状**：`proxy_engine/mod.rs:370` `classify_http_status` 所有 4xx → `Upstream4xx`；`attempt_reporter/types.rs:61-66` `retryable()` = {Timeout, Upstream5xx, NetworkError, ControlPlaneError}——`Upstream4xx` 不在集。429（限流）、408（超时）被当普通不可重试 4xx，控制面无法区分"租户请求错误"与"账号限流/临时不可用"。

**参考做法（四源融合）**：
- clewdr：429 分两子类（long-context-usage 429 留 plain、其他 429 → rate-limit reason 携 reset 时间戳——从 body 解、再 fallback 响应 header、再 fallback 1h，`Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/error.rs:364-403`）；**无 408 处理、无 `Retry-After` 解析**。
- smg：固定 retryable 集 {408, 429, 500, 502, 503, 504}，401/403 不重试；传输层失败映射到合成 status（`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/routers/common/retry.rs:10-20`）；**无 `Retry-After`/rate-limit-reset 解析**——纯指数退避忽略上游时序。
- litellm-rs：429→rate-limit→retryable，408/504→timeout→retryable，401/403→auth→不重试；显式 retryable/non-retryable 分离（`majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/core/router/execution.rs:16-24,58-86`）；Anthropic 客户端从 **429 响应体**解 retry-after（关键字 60s fallback，`majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/core/providers/shared.rs:209-231`）；**全 provider 都不解 `Retry-After` HTTP header**——退避忽略上游时序。

**v1 修正（CLAUDE.md #12 anti-pattern flag）**：rev1 把 408 框为"无参考做"。**误**——smg + litellm-rs 都把 408 归 retryable。**精确表述**：sub2api 与 clewdr 缺 408 显式处理；smg 与 litellm-rs 有；HUAKAI 加 408 = 与 smg/litellm-rs 持平，不是凭空发明。

**修法（核心 — 不需 proto 改）**：
- `classify_http_status`：429 → 新增 `AttemptStatus::RateLimited` 变种；408 → **复用已有 `AttemptStatus::Timeout`**（已 retryable，408 无需新变种）；其他 4xx → `Upstream4xx`（不变）。
- `retryable()`：加 `RateLimited`（`Timeout` 本已在）。
- `error_class()`/`as_str()`：给 `RateLimited` 字符串形态。
- **不需 `route.proto` 改**——`status`/`error_class` 是字符串、`retryable` 是 bool，契约只是新字符串值。

**修法（可选 — reset 时间传递，需 §4.5 P-2 proto）**：
- 解 429 reset 时间（来自 body 与/或 `anthropic-ratelimit-*` / `Retry-After` header——clewdr 双源、litellm-rs 仅 body、smg 都没）并跨数据/控制面传递让控制面定账号冷却——`AttemptReportRequest` 当前无 reset 字段。可选 proto 加 `uint64 rate_limit_reset_at_unix_ms`（§4.5 P-2）。如延后，D-8 核心仍闭合 HIGH 相关行为。

**融合升级 delta**：算法升级——采纳 smg/litellm-rs 的 408+429 retryable 分类 + clewdr 的双源（body+header）reset 解析；HUAKAI 真升级在**跨数据/控制面传 reset 时间让权威面（非数据面）拍冷却时机**——clewdr 解 reset 但单进程内拍板；HUAKAI 拆 plane 推下沉决策。维度：算法 + 架构。

**判别性测试**：
- T1：上游 429 → 断言 `RateLimited`、`retryable()==true`、`error_class` 区别于通用上游错误。
- T2：上游 408 → 断言 `Timeout`、retryable。
- T3：上游 400 → 断言仍 `Upstream4xx`、不可重试。
- Mutation：回退到 4xx 一刀切 → T1/T2 红，T3 仍绿——证测试判别**特殊码**而非"4xx 整体"。
- fixture 关键（CLAUDE.md #14）：必须用 429/408 这种"非看 status 不够、要进专门分类"的码；裸 400 非判别（本就 4xx，正确/损坏两路同输出）。

**切片**：1 commit `proxy_engine 上游限流与超时错误分级`。

### D-9 H2/chunked body 字节记 0（MED）

**HUAKAI 现状**：`proxy_engine/mod.rs:223` `request_bytes_in` 从 header 算；`:380` `content_length_bytes` 只读 `Content-Length`，缺失返 0。HTTP/2、chunked 上传 body 真实流过但 `bytes_in` 记 0。（`listener.rs:169-183` `content_length_exceeds` 同样只看 header——对**大小 gate** 是保守低估、可接受；对**计费字节**就低估。）

**参考做法（四源——没有一个测真字节）**：
- clewdr：**完全不做字节计量**——纯 token 计费。
- smg：代理路径**不数响应字节**。
- litellm-rs：字节计量**极简、信 header**——请求大小**只从入站 `content-length` header**取、缺失则 0（`majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/server/middleware/metrics.rs:77-82`）；响应字节**完全不数**（`majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/server/middleware/metrics.rs:87-103`）；analytics 的 request-size 字段**全仓库只在 `#[cfg(test)]` 块**赋值（`majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/server/types.rs:219-431`）。**litellm-rs 与 HUAKAI 缺陷同形**——Rust 网关的 D-9。
- sub2api：纯 token 计费，无字节字段。

**rev1 修正**：rev1 把 D-9 框为"无参考可抄/HUAKAI 自研"。**更精确**：四源都不数或同形信 header；**litellm-rs 与 HUAKAI 同 bug**——HUAKAI 的 D-9 = "把 Rust 同行的同 bug 修对"。litellm-rs 的另一个陷阱（"仅 cfg(test) 设值"）符合 CLAUDE.md #12 "lives only in tests" 防呆，本计划测试也避此陷阱。

**修法**：包装 inbound body，在实际转发时**累计真实请求体字节**；不把缺 `Content-Length` 等同空 body。

**融合升级 delta**：架构升级——可测量流式字节计量，没有参考做。

**判别性测试**：发 chunked body **无 Content-Length** → 断言 `bytes_in` = 真实字节数（非 0、非 header 值）。Mutation：回退只读 header → `bytes_in==0` → 红。fixture 关键：必须用 chunked + 缺 Content-Length 的请求（不是带 Content-Length 的）——否则正确/损坏两路输出相同。

**切片**：与 O-2 合并 1 commit。

### O-2 死指标 `ACTIVE_CONNECTIONS`（LOW，已定接真值）

**HUAKAI 现状**：`metrics.rs:43` 一个 `ACTIVE_CONNECTIONS` gauge 定义注册但生产从不 inc/dec。

**决策已定**：**接真实连接生命周期**（不删除）——与 D-7 真节点健康方向一致；该 gauge 喂 `HeartbeatRequest.open_upstream_connections`。

**修法**：采 smg 的 **RAII guard idiom**——连接建立时 inc、连接/响应生命期结束（drop）时 dec；流式连接的 RAII guard 把"未结束流"保留在计数里。引用：`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:model_gateway/src/worker/worker.rs:638-654`。

**判别性测试（Codex LOW demand acceptance）**：开 N 条并发上游连接 → 断言 gauge 读 N；关闭后 → 断言归 0。Mutation：去掉 inc/dec → gauge 永 0 → 红。fixture 关键：N 与 0 都必须**显式不同**（不能 N=0）。

**切片**：与 D-9 合并 → 1 commit `proxy_engine 真实请求体字节计量与连接数 gauge 接线`。

---

## §4 指纹（L1 TLS + L2 HTTP/2）

### 现状

- **L1 TLS**：BoringSSL connector 已接线（`proxy_engine/boring_tls_connector.rs`，`mimicry-boring` feature），Anthropic profile 字节级 JA3 已验证。缺口：Codex/Kiro/Gemini profile 标 KnownGap（ETM ext22、JA4、后量子 group 等字段当前 backend 复刻不到）。
- **L2 HTTP/2**：`mimicry/http2_adapter.rs` 的 `HttpTwoMimicryAdapter` 已封装（settings order、pseudo-header order），**但模块注释自己写明"不接入 ProxyEngine"**，feature `mimicry-http2-fork` 默认关。真实上游走 `hyper_util` Client = hyper 默认 h2 指纹。

### 参考挖矿结论（关键）

**Go 参考都不做 HTTP/2 字节级指纹。** cliproxyapi 用 utls 造 TLS ClientHello 但 HTTP/2 走 Go 原生 net/http stack（cite anchor：`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/request_body.go:14`——transport 由标准库构造，无 HTTP/2 SETTINGS / 伪 header 顺序重排）；sub2api 造 ClientHello 但 `ForceAttemptHTTP2` 被关、ALPN 默认 `http/1.1`（`Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/pkg/tlsfingerprint/dialer.go:361-364`）。**Rust 参考亦不做 HTTP/2 字节级指纹**——clewdr 仅作应用层 UA/header 伪装（cite anchor：`Xerxes-2/clewdr@57626809d80cfe4f09e999c06cd8726cc465356a:src/utils/mod.rs:58`），不做 TLS/HTTP/2 字节级覆盖；smg 用标准 tonic + hyper 构造 gRPC client（cite anchor：`lightseekorg/smg@9a93938a61cb9e9c60a1ebf199d1df857079204c:clients/rust/src/transport.rs:14`），transport 无指纹自定义；litellm-rs 用 reqwest 标准 outbound 路径（cite anchor：`majiayu000/litellm-rs@82d0181660d5dccdf94e7bec507abe0c7d43b254:src/core/providers/openai/client.rs:116`），provider client 直接 reqwest 无 TLS/H2 指纹层。→ **L2 HTTP/2 接线是 HUAKAI 领先所有参考的真实价值点**，且 HUAKAI 的 adapter 已存在，主要是接线工程。

**sub2api 的 TLS 指纹模型值得学（行为，非代码）**：把指纹存成**字段级分解的数据库实体**——cipher suites / curves / point formats / sig algs / ALPN / supported versions / key-share groups / PSK modes / **按发送顺序排列的扩展 ID 列表**全部独立可热改（`Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/ent/schema/tls_fingerprint_profile.go:19-100`）；空字段回落内建默认；GREASE 作为一等概念处理（含 GREASE-ECH，`Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/pkg/tlsfingerprint/dialer.go:329-457`）；per-account 绑定，`-1` = 随机 profile；指纹连接池与普通池分离命名空间；Redis pub/sub 热重载。

**cliproxyapi 的应用层伪装技巧值得学**：device profile 版本门控升级；按 auth mode 条件性增删 header；请求体内容签名（占位替换→带 seed 哈希全体→回填）；可逆改写用**每请求 reverse map**（cliproxyapi 自己的真 bug）。**所有 magic 常量必须外置热重载**。

**Rust 参考反例佐证**：D-3 已示——litellm-rs 的 SSRF guard 存在但未接入转发路径，是"adapter 不接线就是 fail-open 静默"的典型；HUAKAI L2 adapter 当前同形（封装好但未接 ProxyEngine），F-1 必修。

### 工作分块

- **F-1 L2 HTTP/2 接线**（核心、最大）：把已有 `http2_adapter` 接到 `BoringTlsConnector` 产出的 TLS 流上，自管 `SendRequest` 连接池，替换 `hyper_util` Client 的 H2 路径。沿用 05-21 plan 的 P7 拆法（P7-1..P7-7）。
- **F-2 L1 缺口**：Codex/Kiro/Gemini profile 的 ETM ext22 / JA4 / 后量子字段缺口闭环（需 BoringSSL fork 既有能力，见 05-17 R-3-A-fix）。
- **F-3 指纹 profile 模型升级**（**已定 → 进 roadmap，不在本轮**）：参考 sub2api 把指纹做成字段级分解 + 热重载存储模型；HUAKAI 当前是 JSON 模板编译进二进制。Owner round-2 delegated。

**估时**：F-1 ~5-8d（与全链路 relay/连接池交互，05-21 plan 已估）；F-2 ~3-4d；合计 8-12d。

### 判别性回归测试（Codex MED-3 demand — 折入本计划）

- **F-1 L2 HTTP/2 接线测试**：通过已接线 adapter 对真上游建立 H2 连接，捕获本端发出的 **SETTINGS 帧顺序 + 伪 header 顺序**；断言其匹配目标客户端 profile，**不**匹配 hyper 默认 profile。Mutation：旁路 adapter（回 hyper_util 默认路径）→ profile-match 断言红——因 hyper 默认与目标 profile 不同。fixture 关键：目标 profile 的期望 SETTINGS/伪 header 顺序必须与 hyper 默认**显式不同**——否则无论接没接 adapter 测试都通过，不判别。
- **F-2 L1 缺口测试**（按 Codex/Kiro/Gemini 三个 profile 各一）：捕获 ClientHello 字节，对 profile 涵盖的 JA3/JA4 相关字段断言；缺口字段闭合后断言**特定扩展存在且位置正确**（如 ETM ext22）。Mutation：回退缺口修复 → 字段存在性断言红。
- **共同 fail-safe**：两测必须在**真握手/真连接**上跑，**非 unit stub**——litellm-rs 的"guard 不在路径上即 fail-open 静默"陷阱也适用 F-1/F-2，adapter 存在但不在转发路径上就是 D-10/F-1 的失败模式镜像。

### Canary 策略（Owner 2026-05-23 已决：**阻断**——§7-I）

**L1-only canary：profile L1 TLS 字节级验证已通过但 L2 HTTP/2 capture 缺失时，禁止上生产/canary 流量。** 理由：L2 默认即裸暴露 hyper 默认 h2 指纹，与已伪装的 L1 不匹配，反而比纯诚实客户端更可疑。

具体后果：
- Anthropic profile 当前 L1 已验、L2 缺 capture → 必须等 §4 F-1 接线 + 至少一次真上游 L2 capture 验证才可上生产/canary。
- Codex / Kiro / Gemini profile 同理：必须 F-2 缺口闭环 + L2 接线 + capture 双满足。
- `mimicry-boring` + `mimicry-http2-fork` 两个 feature 必须**同时编译并通过** backend_resolver 的 known-gap gate（D-10 修后），单 feature 不可对外。
- 任何"L1-only ok 就上 canary"的 escape hatch 被显式禁止。L1-only 路径只能用于 internal smoke/diagnostic，不可服务真实租户流量。

**判别性测试**（折入 W11-F slice）：在 mimicry profile 标 L2 capture unavailable 时，dispatch 必须 fail-closed 阻断生产/canary 路径；Mutation：恢复"L1 通过即放行"旁路 → 测试断言放行发生 → 红（应禁止）。fixture：用 Anthropic profile 把 L2 capture 字段标 unavailable 即可模拟。

---

## §4.5 `route.proto` 受控变更集（rev2 新增 — Owner 决策聚合点）

`route.proto` 在 `cg/proto/`，本线可改；但 `RouteService` 是 Rust↔控制面共享契约——producing/consuming 端在 Go 控制面。本节集中列出 finding 强制/可选的 proto 变更，使跨线协调单点化（→ §7 G）。

| 变更 ID | 状态 | 涉及 finding | 变更内容 | 触发条件 | 影响面 |
|---|---|---|---|---|---|
| **P-1** | **已批（Owner 2026-05-23），契约 pinned（Codex per-commit review #6 P2）** | D-1b | `RouteQueryRequest` 增 `string client_credential = 10`（**消息字段，proto3 string，field number 10**——紧接现有 `capability_hints = 9` 之后；**不走 gRPC metadata** 以保持类型化契约 + 跨语言代码生成一致；Phase 1 仍双写旧 `tenant_id` 见 §4.5 Phase 1/2/3 兼容计划） | D-1b = β 已定 | Rust 添字段 + Go 控制面认证消费（**需 Owner 在 Go 线启动 spec**） |
| **P-2** | **已批（Owner 2026-05-23）** | D-8 | `AttemptReportRequest` 增 `uint64 rate_limit_reset_at_unix_ms` | D-8 reset 时间传递纳入本轮 | Rust 添字段 + 控制面读以定账号冷却 |
| **P-3** | **已否定（Owner 2026-05-23）** | D-7 | 不改 `HeartbeatRequest` 字段为 optional | 改为**文档化** D-7 暂未接源字段（`p95_control_plane_rpc_ms` / `error_rate_1m`），运维 dashboard 注明"信号未接源、不要信 0" | 仅文档化，无 proto 改动 |

**跨线协调**：P-1/P-2/P-3 的 Go 控制面消费/产生侧不在本线范围。Owner 决策点 G 决定哪些纳入本轮 + 是否安排 Go 线对接。

**回退路径**：
- 若 P-1 被否：D-1b 退化到 γ（trusted-header opt-in only，默认 OFF + 前置层验证）——身份模型弱化但可工作。
- 若 P-2 被否：D-8 核心重分类仍闭合 HIGH-相关行为；reset 时间传递留 roadmap。
- 若 P-3 被否：D-7 字段维持 0 默认，文档化"未接源"。

### Phase 1/2/3 兼容计划（synthesis 吸收自 Codex 平行稿）

P-1 在 `RouteQueryRequest` 增 `client_credential` 字段后，Rust 数据面与 Go 控制面**分阶段 rollout**，缓冲跨线协调与 Manual First 兜底：

- **Phase 1（Rust 添字段 + Manual First 双写双读，**仅限非可计费 mock/test/internal smoke**——Codex per-commit review #5 P1-2 加固，闭与 §7-H β 一致性冲突）**：Rust 数据面发 `RouteQueryRequest` 时**同时**填旧 `tenant_id`（由本地 Manual First 静态 key 图派生，见 §2 D-1b）与新 `client_credential`（携带客户端凭据）。控制面**可暂时只读** `tenant_id`；`client_credential` 接收但忽略即可工作。Rust 侧本地认证 + 双写两个字段保持兼容。**这是 Manual First 兜底所在阶段**——Go 控制面尚未消费 `client_credential`。**关键约束（β-only 一致性，§7-J）**：Phase 1 **不可承载任何可计费真实租户流量**——理由：Manual First 把 tenant 权威留在 Rust 数据面本地表，若跑真流量则 §7-H β "控制面是唯一身份权威" 政策被实质破坏。Phase 1 仅供：① mock/staging 联调；② internal smoke / canary 流量替代物（不算账、不写真实 attempt report）；③ 把 W11-A 切片端到端跑通的验证场景。**任何 production-bound 或 billable canary 必须等 Phase 2**（Go 控制面消费 `client_credential` 认证）。若 Owner 因业务紧迫需 Phase 1 承载真实流量 → 必须按 §7-J 单独召开 auth-core 决策会，明确承认 β 在 Phase 1 短期失效 + 本地表审计/轮换/回滚策略 + 硬时限 advance 到 Phase 2。
- **Phase 2（控制面消费 `client_credential` 并校验）**：Go 控制面 spec 完成、CI/staging 验证后切到读 `client_credential` 派生 tenant，与 Rust 旧 `tenant_id` 字段**交叉比对**——不一致即拒绝并告警（早期捕获 Manual First 配置错位）。Rust 仍双写；Manual First feature flag 保留作回滚开关。
- **Phase 3（移除 Rust 信赖 header / Manual First）**：控制面在 prod 稳定消费 `client_credential` 后，Rust 数据面 Manual First feature flag **永久下线**；`tenant_id` 字段在 Rust 侧改为由控制面回填或仅作 advisory；`x-tenant-id` header 在 Rust 全路径硬阻。

每阶段切换是独立的部署事件，需 Owner 显式批准 advance 到下一阶段。Phase 1→2 切换前必须有 staging cross-check 验证；Phase 2→3 切换前必须有 prod 数据证明控制面派生 tenant 与 Rust Manual First 派生 tenant 完全一致达 N 天（建议 N≥7）。

---

## §5 波次、顺序、估时

| 波 | 内容 | 估时（codex-day） | 顺序理由 |
|---|---|---|---|
| **W11** | D-1a + D-1b + D-2 + D-3 + D-6 + D-10，6-7 commit | **~6.5** | 安全边界最先——请求规划仍可伪造/mock 绕过时遥测正确性无意义；D-1b 受 §4.5 P-1 决策门控 |
| **W12** | D-4（最重，3 切片）+ D-5 + D-7 + D-8 + D-9+O-2，~7 commit | **~7** | 账务遥测，依赖 W11 闭合；D-4 C2 是 ~3 codex-day 单项 |
| **指纹** | F-1 L2 接线 + F-2 L1 缺口（F-3 → roadmap） | **~8-12** | 最大最险、与全链路交互，放最后 |

**合计 ~21.5-25.5 codex-day**（串行）。比 rev1 ~15-19 偏高，因 C2 + D-1b 完整设计大于 rev1 估算。每波收尾跑全量 `cargo test`，一波未绿不开下一波。

**W11 内部顺序**（**rev3.1 Owner 决策后已 unblocked**）：D-1a / D-2 / D-3 / D-6 / D-10 / D-1b **均可立即开**（§4.5 P-1 已批 + H 已定 β → D-1b 解锁）。建议把 D-1b 排在 W11 末尾切片：① 触及 proto 改动需 mock_control_plane 同步演进；② Go 控制面对接需 Owner 启动 Go 线 spec 才能端到端联通；③ 把它放尾巴上能让 D-1a/D-2/D-3/D-6/D-10 五个无依赖 finding 先稳。

### Recommended commit grouping（synthesis 吸收自 Codex 平行稿）

每个 slice 独立 commit，**不**把"安全清理"或"账务清理"塞进一个大 commit——测试/rollback 路径不耦合更安全：

- **W11-A（D-1a + D-1b + Manual First feature + proto P-1）**：可拆 2 commit（D-1a body parsing 单 commit；D-1b + Manual First + P-1 字段一起一个 commit），架构闸门 commit rollback 复杂度高。
- **W11-B（D-2）/ W11-C（D-3）/ W11-D（D-6）/ W11-E（D-10）**：各自独立 commit，互不依赖；fixture 与 rollback 路径都不同。
- **W11-F（指纹 F-1 + F-2 + Canary 阻断）**：按 05-21 plan P7-1..P7-7 子步骤可拆 2-4 commit；L2 接线必须以 L1 已验为前置；末尾 commit 含 canary 阻断逻辑（§4 Canary 策略 + §7-I）。
- **W12-A（D-4 spool）**：**必须在 W12-B / W12-E 之前 commit**——只有 spool 持久化先就位，后续的 usage 抽取与字节计量数据才有去处。3 个 sub-slice（spool 数据结构 → 重放 worker → 满盘降级 + `reserve()` pre-commit gate）顺序提交。
- **W12-D（D-8 retry/分级）vs W12-C（D-7 heartbeat）**：可任意顺序（heartbeat 不依赖 D-8 引入的健康信号——本计划 spec 保持解耦）。
- **W12-E（D-9 字节）+ O-2（连接 gauge）**：合并 1 commit `proxy_engine 真实请求体字节计量与连接数 gauge 接线`。
- **W12-F（CI 验证 — synthesis 吸收自 Codex 平行稿）**：作为 release-checklist 文档化 commit（不动 Rust 源码），单独 `docs/ exploratory WSL feature-matrix verification`。

每 commit 之前必跑 §8 verification commands；commit 之间不可携带未 ack 状态。

---

## §6 影响面、风险、失败模式

**影响面**：请求入口（`listener.rs`）、路由规划（`account_planner.rs`）、上游转发（`proxy_engine/`）、流式 relay（`relay.rs`）、attempt 上报（`attempt_reporter/`）、心跳（`heartbeat.rs`）、mimicry 后端选择（`mimicry/backend_resolver.rs`）、config（`config.rs`）、`route.proto`（§4.5 受控）。**不碰** Go 控制面实现、计费账本 schema、DB 迁移。

**失败模式 + 缓解**：
- D-1a body 解析 → 现有客户端兼容性。缓解：保留 header 作经校验的内部可信旁路（D-1b γ），body 优先。
- D-1b 认证身份 → 部署拓扑变化（数据面不再裸接外网或必须前置认证层）。缓解：β 推荐 + γ opt-in 兜底；Owner 决定拓扑。
- D-3 强制 https → 可能挡掉某些 dev/测试上游。缓解：`allow_plaintext_for_envs` 显式 opt-in。
- D-4 durable spool → 引入磁盘 IO/状态、崩溃恢复正确性是新风险面。缓解：spool 设 size cap + 满盘降级 + 重放幂等 + 端到端崩溃-恢复测试覆盖（AC-2/AC-3/AC-4）。
- D-8 重分类 → 改 retryable 语义影响 fallback。缓解：判别性测试锁定每条分类。
- 指纹 F-1 → 连接池正确性、h2 流控、与 in-flight/relay 交互——最大不确定性。缓解：先做最小 spike 验证 fork 能在真 BoringTLS 流上 handshake，再展开。

**风险登记建议**（Owner 确认后我可写进风险册——属 docs/ 顶层需 Owner 批）：
- R-RUST-W11-DEFAULT（D-3 默认安全配置必须开）。
- R-RUST-W11-IDENTITY（D-1b 拓扑决策与 P-1 跨线协调）。
- R-RUST-W12-SPOOL-IMPL（D-4 durable spool 崩溃恢复正确性新风险面——取代 rev1 的 "spool 未做前可丢"）。
- R-RUST-PROTO-COORD（§4.5 P-1 跨线协调）。

---

## §7 Owner 决策点

- **A — 波次顺序**：W11 → W12 → 指纹（推荐，按硬依赖）。**同意？**
- **B — D-3 默认安全**：HUAKAI 是否确认**默认 https-only + host allowlist 开 + 私网阻断 + resolved-IP 校验**（反转所有四源参考的不安全默认），放宽为显式 opt-in 配置块？（推荐：是。）
- **C — D-4 spool**：**已定 C2 durable spool**（Owner round-2 delegated + 四源证据再确认）。本条非开放问题，记录为已决。请确认 acceptance criteria **6 条**（rev3 按时序拆分：AC-1 / AC-2 / AC-3 / AC-4-pre / AC-4-post / AC-5）满足你预期。
- **D — 指纹 F-3**：**已定 → 进 roadmap，本轮只 F-1+F-2**（Owner round-2 delegated）。记录为已决。
- **E — L2 HTTP/2 架构**：F-1 用 L2-α（手工接已 vendor 的 `http2` fork，保住已验证的 L1）vs L2-β（vendor wreq，替换 L1 重验证）。推荐 α（见 05-21 plan D3）。**同意？**
- **F — 实现交接**：**已定 F1 — 新干净 Claude 会话读本 spec 实现**（本会话读 sub2api LGPL + clewdr AGPL 已是 specifier 污染，CR-004）。记录为已决。
- **G（Owner 2026-05-23 全部已决）— §4.5 route.proto 变更集**：
  - **G.1 P-1 已批**：`RouteQueryRequest` 增 `client_credential`（D-1b=β 强制要求）。Rust 线先按预定字段实现 + `mock_control_plane` 联调；**Go 线对接需 Owner 后续在 Go 线启动 spec**。
  - **G.2 P-2 已批**：`AttemptReportRequest` 增 `uint64 rate_limit_reset_at_unix_ms`。D-8 reset 时间传递纳入本轮——clewdr 解析了但不跨进程传、litellm-rs 部分解析、smg 不解析；HUAKAI 加 P-2 是实质 fusion 升级。
  - **G.3 P-3 已否定**：不改 `HeartbeatRequest` 为 optional。改为**文档化** `p95_control_plane_rpc_ms` / `error_rate_1m` 暂未接源；运维 dashboard 注明"信号未接源、不要信 0"，省一笔跨线 proto 协调成本。
- **H（Owner 2026-05-23 已决）— D-1b 认证身份方案 = β（控制面权威身份）**：
  - Rust 数据面把客户端凭据（Authorization / API key）经 `RouteQueryRequest.client_credential`（G.1 P-1）透传给 Go 控制面；控制面认证 → 派生 tenant → 路由，一次 RPC 内完成。
  - Rust 端**不持有任何身份权威**——`x-tenant-id` header **永不被信任**；trusted-header opt-in 模式（γ）暂不开放，如未来部署拓扑确认有受信前置层认证，可再讨论重启。
  - α 与 γ 已弃，不再保留为待决选项。
- **I（Owner 2026-05-23 已决，synthesis 吸收自 Codex 平行稿提出的决策点）— 指纹 L1-only canary 策略 = 阻断**：
  - L1 TLS 字节级验证通过但 L2 HTTP/2 capture 缺失的 profile **禁止上生产/canary 流量**（详见 §4 "Canary 策略"）。
  - 理由：L2 默认裸暴露 hyper 默认 h2 指纹，与已伪装 L1 不匹配比纯诚实更可疑。
  - 影响：Anthropic profile 当前 L1 已验 / L2 缺 capture → 必须等 F-1 接线 + 真上游 L2 capture 完成才能上生产；Codex/Kiro/Gemini 同理需 F-2 缺口闭环。
  - L1-only 路径只能用于 internal smoke/diagnostic，不可服务真实租户流量。
  - 测试在 §4 "判别性回归测试" 末段定义（mimicry profile 标 L2 capture unavailable → dispatch fail-closed 阻断；mutation 恢复放行旁路 → 红）。
- **J（Codex per-commit review #5 P1-2 强制新增，Owner 待显式确认）— Phase 1 流量约束**：
  - **默认实施约束**：Phase 1 Manual First 阶段**禁止承载可计费真实租户流量**——仅 mock/staging/internal smoke。理由：Manual First 把 tenant 权威留在 Rust 数据面本地表，与 §7-H β "控制面是唯一身份权威" 直接冲突；若 Phase 1 跑真流量则 β 政策被实质破坏。**这是默认实施约束，不需要 Owner 提前批准就生效；synthesis 已 bake-in。**
  - **替代方案（仅在 Owner 因业务紧迫必须在 Phase 1 承载真实流量时启用）**：必须**单独召开 auth-core 决策会**承认以下事实：① β 在 Phase 1 短期失效（数据面持有身份权威是临时违例）；② 明确 Manual First 本地表的审计 / 轮换 / 回滚策略；③ 设置硬时限 advance 到 Phase 2（如 ≤14 天）；④ 单独 Codex review + Owner 二次签字。
  - **Owner 拍板项**：如要默认（mock/staging only） → 不动；如要替代（Phase 1 承载真流量） → 触发 auth-core 决策会启动条件。

---

---

## §8 Clean-room、环境、交接

- **Clean-room**：本会话读了 sub2api（LGPL）+ clewdr（AGPL）+ smg（Apache-2.0）+ litellm-rs（MIT）+ cliproxyapi（MIT）源——specifier 车道。本稿全程释义，rev1 的三处上游标识符泄漏（line 132/174/270）已替换为行为描述；所有参考行为均带 `<repo>@<sha>:<file>:<line>` 引用，无 verbatim 函数名/结构/注释/代码块。clewdr AGPL **禁止 vendor**（copyleft 与 DR-002 冲突，仅释义）；smg Apache-2.0 / litellm-rs MIT 本稿亦仅释义，vendoring 决策留给实现会话按 CLAUDE.md #12 permitted-license 政策评估（如取，需 LICENSE/NOTICE/MODIFICATIONS.md 全套）。
- **实现车道**：实现会话**只读本 spec + HUAKAI 自有代码 + MIT 锚（one-api）+ 公开协议规范**，不读 sub2api/clewdr/smg/litellm-rs/cliproxyapi 源。
- **环境**：实现在 WSL2 Ubuntu，Rust 1.95，`CARGO_TARGET_DIR` 置 WSL 原生盘。Windows 编不过（UDS Linux-only）。本机连不上 git 服务器（SSH key 不在），推送需 Owner 处理。
- **提交纪律**：每 commit `<英文模块> <中文说明>`，无 type/无阶段号/无 PASS 字样，结尾 Co-Authored-By；commit 前 `codex exec review --uncommitted`。
- **下一步**：本稿已 synthesis 定稿（Codex APPROVE WITH MINOR + Owner 全部决策 lock-in + Codex 平行稿 6 处补充织入）→ 由 Owner 决定是 commit + push（Owner 持 SSH key）/ 直接交接新干净 Claude 会话开干 W11 / 或两者皆做。W11 内部 D-1a/D-2/D-3/D-6/D-10/D-1b 均可立即开（D-1b 在 Phase 1 用 Manual First 兜底，等 Go 控制面 P-1 消费侧上线后 advance 到 Phase 2）。

### Implementation-ready acceptance gate map（synthesis 吸收自 Codex 平行稿）

实施时按以下 28 条 gate 逐条勾选；每条 gate 对应至少一条判别性测试（详见各 §2/§3/§4 finding 的"判别性测试"段）。Mutation 原则：拿掉对应 gate 的实现 → 对应测试必须红。

- **W11-A（D-1a + D-1b + Manual First + P-1）**:
  - A1 客户端凭据缺失 → 公共 401，**route query 未发出**。
  - A2 body 的 `model` 与 header `x-huakai-model` 冲突时，route query 用 body 值。
  - A3 凭据派生的 tenant 与 header `x-tenant-id` 冲突时，route query 用凭据派生值；header 永远被忽略。
  - A4 route query debug/日志输出仅含 hash/prefix，永不含 raw 客户端 secret。
  - A5 Manual First feature flag OFF → 旧字段路径不工作（强制走 P-1 控制面认证）；ON → 双写新旧字段。
- **W11-B（D-2 mock）**:
  - B1 production/canary 启动时 `HUAKAI_MOCK_UPSTREAM_ENDPOINT` 存在 → 启动失败 fail-fast。
  - B2 dev/test mock 路径仍工作但发出 explicit mock attempt event（带显著标记、禁注真凭据）。
- **W11-C（D-3 https）**:
  - C1 控制面返回 `http://` vendor_endpoint → 生产模式整链路转发被挡（端到端测试，非 validator 单测）。
  - C2 上游凭据注入对 non-HTTPS planned endpoint 阻断（即便 URL 通过 format validator）。
  - C3 私网/loopback 解析 IP 被阻断（resolved-IP 校验生效）。
  - C4 opt-in `upstream_security.allow_plaintext_for_envs=[dev]` + dev 模式 → `http://` 放行 + startup log 显著告警；prod 模式 → 仍拒。
- **W11-D（D-6 headers）**:
  - D1 客户端 `openai-organization` / `openai-project` / 残留 `authorization` / `x-api-key` → **全部不到上游**。
  - D2 route plan / 控制面注入的 `openai-organization` **与** `openai-project` → **两者均到达**上游。
  - D3 上游响应错误体含 org/proj 标识 → 擦除后回客户端。
- **W11-E（D-10 mimicry intent）**:
  - E1 `mimicry-boring` feature ON + Kiro known-gap profile → backend_resolver fail-closed 阻断（非 AllowBoring）。
  - E2 resolver 报告阻断 reason，不静默降级。
- **W11-F（指纹 L1+L2 + canary 阻断）**:
  - F1 L1 TLS 字节级 preflight 不匹配 profile → 阻断 profile 使用。
  - F2 F-1 接线后 H2 SETTINGS / 伪 header 顺序匹配目标 profile，**不**匹配 hyper 默认（mutation：旁路 adapter → 红）。
  - F3 profile 标 L2 capture unavailable → dispatch fail-closed 阻断生产/canary（§7-I L1-only canary 阻断政策）。
- **W12-A（D-4 spool + pre/post-commit）** — 即 §3 D-4 AC-1..AC-5（含 AC-4-pre / AC-4-post 拆分）：
  - A1=AC-1 溢出无丢、A2=AC-2 崩溃恢复、A3=AC-3 重放幂等、A4-pre / A4-post 时序拆分（`reserve()` 失败 → 转发前 503；响应头已送出 → 200 不变 + `spool_drop_billable` metric/alert）、A5=AC-5 控制面长期不可用（watermark 触发 pre-commit gate）。
- **W12-B（D-5 usage 抽取）**:
  - B1 OpenAI 非流式 happy 200 body usage 解析到 attempt report，`source="response_body"`。
  - B2 Anthropic 非流式 happy 同上，命名族（`input_tokens` / `output_tokens`）正确解析。
  - B3 缺 usage → `source="pending_reconciliation"`（非 `missing`）。
  - B4 坏字段（非数字 output_tokens）→ `pending_reconciliation`，不静默 0。
  - B5 坏 JSON 语法（截断 / 未闭合）→ `pending_reconciliation`。
  - B6 上游 4xx/5xx 不被当 success 解析（status 2xx gate 生效）。
- **W12-C（D-7 heartbeat 真值）**:
  - C1 heartbeat 暴露真实 in-flight / queue depth（驱动真负载 → 断言 > 0）。
  - C2 暂未接源的 p95 / error_rate 字段在运维 dashboard / 文档中标"未接源、不要信 0"（Owner P-3 已否定，转文档化）。
- **W12-D（D-8 retry 分级）**:
  - D1 429 → `RateLimited`、retryable、与 rate-limited 信号；reset 时间通过 P-2 `rate_limit_reset_at_unix_ms` 字段传递。
  - D2 408 → `Timeout`、retryable。
  - D3 401 / 403 仍 non-retryable；与上游 401 区分。
  - D4 400 → 仍 `Upstream4xx`、不可重试（判别性 fixture）。
- **W12-E（D-9 字节）+ O-2（连接 gauge）**:
  - E1 chunked body 无 Content-Length → `bytes_in` = 真实字节。
  - E2 Content-Length 与实际不符 → 上报 observed bytes + mismatch flag。
  - E3 `ACTIVE_CONNECTIONS` gauge 随真实连接生命周期 inc/dec（开 N 条 → gauge=N；关闭 → 归 0）。
- **W12-F（CI verification — synthesis 吸收自 Codex 平行稿）**:
  - F1 `cargo test -p core_gateway` 全绿。
  - F2 `cargo test -p core_gateway --features mimicry-boring` 全绿。
  - F3 `cargo test -p core_gateway --features mimicry-http2-fork` 全绿。
  - F4 feature-matrix 命令是 release checklist 一部分；缺一即 review fail（mutation：删除任一命令 → review checklist 自动检测缺失）。

### WSL2 verification commands per commit（synthesis 吸收自 Codex 平行稿）

每次 commit 前必跑以下命令链（全部在 **WSL2 Ubuntu Rust 1.95** 下执行；`CARGO_TARGET_DIR` 置 WSL 原生盘加速；Windows 不作为通过标准——UDS Linux-only）：

**重要**：每条 cargo 命令必须 inline `export CARGO_TARGET_DIR=$HOME/.cache/huakai-rust-target`——这把 build artifact 放到 WSL 原生盘（非 `/mnt/c`），避免跨文件系统 IO 10x 慢，per-commit gate 可复现（Codex per-commit review #8 P2 加固）。

```bash
wsl -d Ubuntu -- bash -lc 'export CARGO_TARGET_DIR=$HOME/.cache/huakai-rust-target && cd /mnt/c/HUAKAI/server-clones/HUAKAI-code/exploratory/rust-core-gateway/merged && cargo build -p core_gateway'
wsl -d Ubuntu -- bash -lc 'export CARGO_TARGET_DIR=$HOME/.cache/huakai-rust-target && cd /mnt/c/HUAKAI/server-clones/HUAKAI-code/exploratory/rust-core-gateway/merged && cargo test -p core_gateway'
wsl -d Ubuntu -- bash -lc 'export CARGO_TARGET_DIR=$HOME/.cache/huakai-rust-target && cd /mnt/c/HUAKAI/server-clones/HUAKAI-code/exploratory/rust-core-gateway/merged && cargo test -p core_gateway --features mimicry-boring'
wsl -d Ubuntu -- bash -lc 'export CARGO_TARGET_DIR=$HOME/.cache/huakai-rust-target && cd /mnt/c/HUAKAI/server-clones/HUAKAI-code/exploratory/rust-core-gateway/merged && cargo test -p core_gateway --features mimicry-http2-fork'
wsl -d Ubuntu -- bash -lc 'export CARGO_TARGET_DIR=$HOME/.cache/huakai-rust-target && cd /mnt/c/HUAKAI/server-clones/HUAKAI-code/exploratory/rust-core-gateway/merged && cargo clippy -p core_gateway --all-features -- -D warnings'
codex exec review --uncommitted --full-auto
```

任一命令失败禁提交。`codex exec review --uncommitted --full-auto` 出 HIGH 必须先修。第一次运行前可一次性 `wsl -d Ubuntu -- mkdir -p $HOME/.cache/huakai-rust-target` 创建 cache 目录。

---

Source files read: docs/process/research/2026-05-22-deep-audit-rust.md; docs/process/plans/2026-05-22-audit-remediation-wave.md; docs/process/plans/2026-05-22-rust-hardening-plan-claude.md; docs/process/plans/2026-05-22-rust-hardening-plan-codex.md; CLAUDE.md; AGENTS.md; docs/05_CLEAN_ROOM_POLICY.md; docs/RULES.md; exploratory/rust-core-gateway/merged/proto/route.proto; exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml; exploratory/rust-core-gateway/merged/crates/core_gateway/src/lib.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/main.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/listener.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/circuit_breaker.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/auth.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/headers.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/boring_tls_connector.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/sse_parser.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/error.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/mod.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/types.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/metrics.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter/idempotency.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/stream_pipeline/mod.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/stream_pipeline/sse.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/stream_pipeline/openai.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/stream_pipeline/anthropic.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/heartbeat.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/resource_limits.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/body_timeout.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/drain.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/server_runtime.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/metrics.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/redaction.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/error.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/request_id.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/tracing_init.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/mock_control_plane.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_proto.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/backend_resolver.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http2_adapter.rs; exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profiles/anthropic_claude_code.json; exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/vendor_profile_audit_notes.md; C:/Users/h/refs/sub2api/backend/internal/config/config.go; C:/Users/h/refs/sub2api/backend/internal/service/gateway_request.go; C:/Users/h/refs/sub2api/backend/internal/service/account_test_service.go; C:/Users/h/refs/sub2api/backend/internal/service/gateway_service.go; C:/Users/h/refs/sub2api/backend/internal/service/openai_gateway_service.go; C:/Users/h/refs/sub2api/backend/internal/repository/usage_billing_repo.go; C:/Users/h/refs/sub2api/backend/internal/service/usage_record_worker_pool.go; C:/Users/h/refs/sub2api/backend/internal/service/ratelimit_service.go; C:/Users/h/refs/sub2api/backend/internal/server/middleware/api_key_auth.go; C:/Users/h/refs/sub2api/backend/ent/schema/tls_fingerprint_profile.go; C:/Users/h/refs/sub2api/backend/internal/pkg/tlsfingerprint/dialer.go; C:/Users/h/refs/sub2api/backend/internal/util/urlvalidator/validator.go; C:/Users/h/refs/cliproxyapi/internal/access/config_access/provider.go; C:/Users/h/refs/cliproxyapi/sdk/api/handlers/request_body.go; C:/Users/h/refs/cliproxyapi/sdk/api/handlers/header_filter.go; C:/Users/h/refs/cliproxyapi/internal/redisqueue/plugin.go; C:/Users/h/refs/cliproxyapi/internal/redisqueue/plugin_test.go; C:/Users/h/refs/cliproxyapi/test/usage_logging_test.go; C:/Users/h/refs/clewdr/Cargo.toml; C:/Users/h/refs/clewdr/src/router.rs; C:/Users/h/refs/clewdr/src/middleware/auth.rs; C:/Users/h/refs/clewdr/src/middleware/claude/request.rs; C:/Users/h/refs/clewdr/src/middleware/claude/response.rs; C:/Users/h/refs/clewdr/src/error.rs; C:/Users/h/refs/clewdr/src/utils/mod.rs; C:/Users/h/refs/clewdr/src/config/constants.rs; C:/Users/h/refs/clewdr/src/config/clewdr_config.rs; C:/Users/h/refs/clewdr/src/config/cookie.rs; C:/Users/h/refs/clewdr/src/config/reason.rs; C:/Users/h/refs/clewdr/src/claude_code_state/mod.rs; C:/Users/h/refs/clewdr/src/claude_code_state/chat.rs; C:/Users/h/refs/clewdr/src/claude_code_state/exchange.rs; C:/Users/h/refs/clewdr/src/claude_web_state/chat.rs; C:/Users/h/refs/clewdr/src/claude_web_state/bootstrap.rs; C:/Users/h/refs/clewdr/src/services/cookie_actor.rs; C:/Users/h/refs/clewdr/src/providers/claude/mod.rs; C:/Users/h/refs/clewdr/src/providers/mod.rs; C:/Users/h/refs/clewdr/src/api/claude_code.rs; C:/Users/h/refs/clewdr/src/api/misc.rs; C:/Users/h/refs/clewdr/clewdr-types/src/reason.rs; C:/Users/h/refs/clewdr/clewdr-types/src/usage.rs; C:/Users/h/refs/smg/clients/rust/src/transport.rs; C:/Users/h/refs/smg/clients/rust/tests/test_error.rs; C:/Users/h/refs/smg/model_gateway/src/server.rs; C:/Users/h/refs/smg/model_gateway/src/main.rs; C:/Users/h/refs/smg/model_gateway/src/middleware/tenant_resolution.rs; C:/Users/h/refs/smg/model_gateway/src/middleware/auth.rs; C:/Users/h/refs/smg/model_gateway/src/tenant.rs; C:/Users/h/refs/smg/model_gateway/src/routers/common/header_utils.rs; C:/Users/h/refs/smg/model_gateway/src/routers/common/retry.rs; C:/Users/h/refs/smg/model_gateway/src/routers/common/worker_selection.rs; C:/Users/h/refs/smg/model_gateway/src/routers/openai/router.rs; C:/Users/h/refs/smg/model_gateway/src/routers/openai/chat.rs; C:/Users/h/refs/smg/model_gateway/src/routers/openai/provider/anthropic.rs; C:/Users/h/refs/smg/model_gateway/src/routers/anthropic/worker.rs; C:/Users/h/refs/smg/model_gateway/src/routers/error.rs; C:/Users/h/refs/smg/model_gateway/src/routers/grpc/client.rs; C:/Users/h/refs/smg/model_gateway/src/worker/worker.rs; C:/Users/h/refs/smg/model_gateway/src/worker/circuit_breaker.rs; C:/Users/h/refs/smg/model_gateway/src/observability/inflight_tracker.rs; C:/Users/h/refs/smg/model_gateway/src/observability/metrics.rs; C:/Users/h/refs/smg/model_gateway/src/config/validation.rs; C:/Users/h/refs/smg/crates/auth/src/middleware.rs; C:/Users/h/refs/smg/crates/grpc_client/src/channel.rs; C:/Users/h/refs/litellm-rs/src/core/types/responses/usage.rs; C:/Users/h/refs/litellm-rs/src/core/providers/anthropic/models.rs; C:/Users/h/refs/litellm-rs/src/core/providers/anthropic/client.rs; C:/Users/h/refs/litellm-rs/src/core/providers/anthropic/models/cost.rs; C:/Users/h/refs/litellm-rs/src/core/providers/openai/transformer/response.rs; C:/Users/h/refs/litellm-rs/src/core/providers/openai/client.rs; C:/Users/h/refs/litellm-rs/src/core/providers/openai/error_mapper.rs; C:/Users/h/refs/litellm-rs/src/core/providers/openai/streaming.rs; C:/Users/h/refs/litellm-rs/src/core/providers/base/http.rs; C:/Users/h/refs/litellm-rs/src/core/providers/base/sse.rs; C:/Users/h/refs/litellm-rs/src/core/providers/base/sse/anthropic.rs; C:/Users/h/refs/litellm-rs/src/core/providers/base/connection_pool.rs; C:/Users/h/refs/litellm-rs/src/core/providers/shared.rs; C:/Users/h/refs/litellm-rs/src/core/cost/calculator.rs; C:/Users/h/refs/litellm-rs/src/core/pricing.rs; C:/Users/h/refs/litellm-rs/src/core/pricing_service/loader.rs; C:/Users/h/refs/litellm-rs/src/core/router/deployment.rs; C:/Users/h/refs/litellm-rs/src/core/router/execution.rs; C:/Users/h/refs/litellm-rs/src/core/router/unified.rs; C:/Users/h/refs/litellm-rs/src/core/analytics/collector.rs; C:/Users/h/refs/litellm-rs/src/core/budget/tracker.rs; C:/Users/h/refs/litellm-rs/src/core/observability/logging.rs; C:/Users/h/refs/litellm-rs/src/core/net/ssrf_guard.rs; C:/Users/h/refs/litellm-rs/src/core/http/outbound.rs; C:/Users/h/refs/litellm-rs/src/server/routes/ai/chat.rs; C:/Users/h/refs/litellm-rs/src/server/routes/ai/execution.rs; C:/Users/h/refs/litellm-rs/src/server/routes/ai/provider_selection.rs; C:/Users/h/refs/litellm-rs/src/server/middleware/metrics.rs; C:/Users/h/refs/litellm-rs/src/server/types.rs

Lane: specifier (synthesis 合成自 Claude rev3.1 specifier 车道 + Codex 平行 specifier 车道，CLAUDE.md #10 严格双稿模式)

Agent: Claude Opus 4.7 (1M context); Codex (平行起草贡献，独立 specifier 车道)

UTC: 2026-05-23
