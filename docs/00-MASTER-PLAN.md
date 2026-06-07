# HUAKAI 项目总纲 (MASTER PLAN)
> 唯一权威的「定位 + 架构 + 规则 + 冻结区 + 战略 + 路线 + 现状」一处看全文档。
> 生成 2026-06-07 · landing `origin/fix/hermes-phase-1-e33d940 @ ddc0d729`(领先 origin/main 500+ 提交)· 6-agent 挖掘合成,对准真码。
> 配套:功能态势看板 `HUAKAI-gateway-status-2026-06-07.html` · 标杆功能树 `/home/ubuntu/benchmark/` · 规则原文 `AGENTS.md`/`docs/RULES.md` · 本会话纠错记忆 `gateway-scope-and-frozen-rule.md`。

---

## 0. 一页速览 (TL;DR)
- **是什么**:HUAKAI = **多租户商业 AI 中转网关 + 账号中心 + 后台运营平台**(MIT clean-room)。定位**与 new-api / sub2api 同位**(非 cliproxy 单租户),目标:**融合三家、比三家都强更方便**。
- **架构**:**Go `gatewayhttp` 当大脑**(认证→选号→计费→路由→中继)+ **Rust `tls-sidecar` 当出站强伪装传输**(方向 C)+ canonical **HCSF 协议中枢**(N+M adapter,任意客户端协议↔任意上游)。
- **现状**:核心 **8 轴领先 6**(上游/路由/计费/多租户/安全审计/可观测/管理API);两块落后:**入站协议广度**(零 Gemini 原生、realtime 桩)+ **前端**(冻结)。+ 第 9 轴 **TLS/H2 线级伪装(Rust)** 能力已建,生产姿态 Owner-gated(合规优先)。
- **战略主攻**:① 关入站协议缺口(W1)② 完成 Rust 出口生产化决策(方向 C 门槛)→ **8+ 轴严格超三家**。
- **底线**:真实 PSP / 前端 / 越界 money·auth·schema / 主动反检测 = **冻结或 Park 待你拍**。

---

## 1. 项目定位 (Positioning)
- **同位参照 = new-api + sub2api**(多租户商业平台:陌生付费用户、key=可计费凭据、严格按租户隔离)。**cliproxy 是单租户 CLI 代理**,其便利(URL-key/ambient account)是单租户假设,**不可盲搬**。
- **融合三家(Owner 指令「CLI+new-api+sub2 结合」)**:取 **cliproxy 的协议翻译广度 + drop-in SDK 兼容**,跑在 **new-api/sub2 的多租户骨架**上,用 **HUAKAI 自有的更优引擎**(HCSF 中枢 / PASR 路由 / reservation 计费 / 审计链 / Rust 伪装)→ 结果**严格强于任一家**。
- **三镜基线**:sub2api `@635ad81` · new-api `@adc390c` · CLIProxyAPI `@3abfc83`。每个功能设计前**先读三镜出 shape inventory**(规则 #16),不漏成熟架构的分支(payment 曾因只建一条入账路径而踩坑)。

---

## 2. 架构 (Architecture)
**(A) Go 生产数据面(大脑):** `cmd/gateway/routes.go` 入站 → `internal/gatewayhttp` 泛型 handler(NewChatCompletions/Responses/Messages)→ 认证(`hk_` bcrypt key → 租户 Identity{TenantID,APIKeyID,UserID})→ `ClientAdapter.RequestToCanonical`(进 **HCSF 中枢**)→ 选号(`internal/pool/router` HRW+PASR 前缀粘性 + 5-mode dispatcher)→ 预扣计费(`claim_gate` reservation + 6-scope×4-metric 配额)→ 上游中继 → `CanonicalToClientResponse` → settle + **ed25519+Merkle 审计链**。
**(B) Rust 出站 sidecar(方向 C,出口走 Rust):** `exploratory/rust-core-gateway/merged/crates/tls-sidecar` = 自维护 **BoringSSL fork**(`SSL_CTX_set_extension_order` patch)+ JA3/JA4 + **H2 SETTINGS wire 指纹** + 防走私;Go 侧 `backend/internal/transport/mimicry`(`transport.Factory` + `sidecar_client` + `utls_dialer` + **fail-closed 契约**)。**旧 `core_gateway` 全数据面 fork 已退役为 legacy**;现 Rust 只做高性能强伪装出站传输,Go 仍是大脑。
**(C) 多租户商业模型:** tenant / `api_keys`(bcrypt,IP/过期/USD 配额)/ groups / subscription / wallet。`tenant_id` 在每张主表(TS-006);钱用 `numeric(20,8)`(TS-007)。
**(D) 技术栈(锁定 DR-003..006):** Go stdlib net/http + chi(Fiber/fasthttp 永禁)· TS+React+Next.js App Router+Tailwind · PostgreSQL + sqlc + Docker Compose(SQLite 生产禁)· OpenAPI 为契约真源,前端类型 codegen。

---

## 3. 规则与治理 (Rules & Governance)  ——原文 `AGENTS.md`(738行,codex 必读)/`CLAUDE.md` #1-#16 / `docs/RULES.md`
**铁律(冲突时 truth-first 压一切):**
1. **真实第一(truth-first)**:不造假。Observed/Inferred 标注,Speculative 禁止(进 Open Questions 或删)。4000 字真 > 9000 字掺水。
2. **Clean-room / 许可**:参照=证据非源码;**禁抄** AGPL/GPL 源/目录/注释/schema/UI/函数名;codex **盲实现**(PM 读源写自述 spec,codex prompt 禁含 refs 路径);CL-001..010 泄漏清单逐项过;LICENSE 改动需 Owner。
3. **功能不缩水(Feature Preservation)**:许可风险改**方法**不删功能;安全风险改**门控/默认**不删功能;每个上游功能落 7 种合法处置之一,**禁静默 Dropped**。
4. **逐提交 codex 审 + 严重度门**:每次提交先 `git add` 暂存→`codex exec review --uncommitted`→修未结 **S0/S1(阻断)**→再 commit。S0=灾难(secret/auth/billing/quota/data-loss/许可污染/破坏迁移)；S1=切片正确性(功能缩水/钱·安全回归/**非判别测试**/**冻结包新文件**/schema 风险)。money-path/schema/auth 升 full reviewer-lane。
5. **判别测试(mutation-check)**:每个测试必须能在它该守的缺陷被注入时**变红**;spec 必须给**判别性示例**非仅意图。测过不等于有效。
6. **plan-before-execute + 平行双稿**:非平凡动作先写 `docs/process/plans/` 计划 + 知会 Owner;重大决策 Claude 与 codex **各自独立起稿→交叉讨论→Owner 批合成稿**(单稿顺序评审被否)。Owner「你定」= 委托**一次**,下个决策仍需平行。
7. **#15 参考对照**:每个 Owner 决策选项带「≥2 参照项目 file:line 对照」。**#12 source-must-read**:能力/机制/对比断言必须带 `owner/repo@sha:file:line`,「我记得」=删断言。
8. **风险三档**:LOW 直接做;MEDIUM 做+记原因;**HIGH 停+问 Owner**(删文件/改 LICENSE/改 schema/改 auth 核心/改 billing 账本/改 quota/加运行时依赖/碰真 secret/部署/支付逻辑/破坏迁移)。
9. **集成门**:真库测试在 `integration_pg` tag。**kaifa 无 docker → 用 fresh `huakai_gate` 库 + `migrate up`(golang-migrate)+ `go test -tags=integration_pg -p1`**(必须 -p1,否则多包共享库触发 40001 假失败)。
10. **提交铁律**:`git add` 只暂存预期 diff;新公共路由必同步 `openapi.yaml`(TS-004,串行热点文件);commit 末尾 `Rules touched: <ID>`;每任务出中文 8 点报告。
11. **模块化(见 §4)**:一包一职责;超预算包冻结。
12. **协调**:`.coordination/` 锁文件(热点 `routes.go`/`openapi.yaml` 串行,禁并行写)。
**角色(Owner 2026-06-04)**:opus(我)=研究+设计+验证+审查+wiring+决策;**codex=多并行实现**;sonnet 已退役;gemini=前端(禁碰后端核心)。算力拉满,在 kaifa(server)上跑。

---

## 4. 冻结与 Park (Frozen & Parked)  ——每条:是什么 / 为什么 / 解冻条件
| # | 冻结/Park 项 | 为什么 | 解冻 / 推进条件 |
|---|---|---|---|
| **F1** | **冻结包** `internal/{gatewayhttp,gateway,proto}` 禁**新增文件** | 反 god-package(gatewayhttp 曾 68 文件/2万行,超 ~20文件/~5000行 预算) | **= 模块化约束,非协议冻结**。加新协议/功能=**落新包**(如 `internal/geminiclient`)+ 既有 registry/route 文件**加性 edit**;需内部能力就**导出**。**禁 shim hack**。彻底解冻=12 波审计后按职责拆分 |
| **F2** | **真实 PSP**(Stripe/支付宝/微信/EasyPay/Airwallex)| Owner 无商户号/SDK/沙箱/实名;签名验证 bug=伪造结算(money 高危) | Owner 提供商户凭据 + 单条 PSP 走 full money-path 审 + 三层防伪造 |
| **F3** | **整个用户+后台 web 前端** | Owner 冻结(2026-06-07 审计重申);现 frontend/ 仅 Chat+Observability 垂直楔子 | Owner 解冻 + 定栈(Next.js `frontend/app`);**多数后端 API 已就绪,解冻后多数页面无需后端工** |
| **P1** | **主动反检测/ban-evasion 伪装**(CB-001 / D-R3-A)| PM 立场**合规优先**:不假冒一方客户端、不绕上游检测;R7 请求体伪装=PARK | Owner 拍 D-R3-A;在此之前 Rust 伪装仅作**传输策略 + 出站诊断**,不做指纹复制式 ban-evasion |
| **P2** | **Rust 出口全 vendor 生产启用** | 接入门槛 D-rust-3=max 未达(READINESS NO-GO)| Go 热路径 benchmark + Rust p95/RSS≥−50% 实测 + 7d 真账号 shadow + **Owner go/no-go**。(sigalgs+h2-bridge **已合**;uTLS 已对 anthropic 生产)|
| **P3** | **越界 money/auth/schema 高危** + 返佣 admin 提现 + totp-2fa | money 账本/认证核心/破坏迁移=HIGH;返佣自动发放无人工队列(by design)| 逐项 Owner 批 + full reviewer-lane;W3/W4 的 schema 项**均加性/可空**,过 PM 门即可派(非冻结)|

---

## 5. 战略方向 (Strategy)
**全网关 × 三家 证据级融合审计(对抗去误报 0 假阳)结论:核心已赢。**
- **领先 6 轴**:上游账号广度(16 vendor)· 路由/健康(HRW+PASR+5-mode+exactly-once 换号)· 计费(reservation+6-scope 配额+生效日期定价)· 多租户(租户门控+per-key)· 安全审计(ed25519+Merkle 哈希链+连接期 SSRF)· 可观测(唯一真 Prometheus + 唯一真告警引擎)。
- **第 9 轴(Rust)**:TLS/H2 **线级伪装能力**已建+测(36 测试,sigalgs+h2-bridge 合)——**技术上领先**三家(cliproxy 自承不做 L2 H2),但**生产姿态 Owner-gated**(P1/P2)。
- **落后 2 轴**:**入站协议广度**(零 Gemini 原生、Responses 仅 POST、realtime 桩)+ **前端**(冻结)。
**差异化护城河(超三家的根)**:HCSF 跨协议中枢 · PASR 前缀粘性路由 · reservation 预扣计费 · 签名审计链 · Rust BoringSSL 伪装 · 唯一告警引擎(规则/事件/沉默 + 本会话刚接通租户级指标源+投递)。
**主攻 = 关掉唯一真短板(入站协议)+ 做 Rust 出口生产化决策。**

---

## 6. 路线图 (Roadmap) —— 双轨
### Track 1 · Go 网关升级(全网关审计 14 缺口 / 5 波)
- **W1 入站协议广度**(最大短板,纯加新包+加性edit,无钱无schema风险):Gemini 原生 `/v1beta`(反转既有 `proto/gemini` 转换器,**新包 internal/geminiclient**,出站走 Rust)→ `/v1internal` gemini-cli → realtime-WS + GET/compact responses(共一 ws 依赖)。
- **W2 运行时接线**(✅ **本会话已全闭环**):加权随机 selector · **告警投递+租户级指标源**(独有引擎彻底活)· session-hash 拓宽。
- **W3 schema 加性(PM 门)**:per-key 模型白名单(P1)· client_ip 入用量 · 主动健康探测 · admin 用户变更动词 · 运营可调 SSRF 策略。
- **W4 定价细分(PM 门)**:group_group_ratio 2D · service_tier 差异定价 · 长上下文溢价 · key_group 预算 scope。
- **W5 尾巴(缓)**:/v1/moderations 中继 · /v1/videos · admin 渠道钉选(需 Identity 加 admin 角色)。
> W1–W4 落完 → 8 轴严格超三家。
### Track 2 · Rust 出口生产化(方向 C)
- **已完成并入 landing**:R-SIDECAR-001 sigalgs raw · R-SIDECAR-002 h2-bridge(+Stage-2 streaming+防走私)· Go↔Rust fail-closed 契约 · uTLS 对 anthropic 生产。
- **剩余(门槛 D-rust-3,需 Owner+真环境)**:更多 vendor 指纹 profile(openai/gemini/codex)· **Go 热路径 benchmark + Rust 实测对比** · 7d 真账号 shadow · **Owner go/no-go 全 vendor 启用** · Docker 多阶段部署。
- **合规前提**:P1(CB-001/D-R3-A)未拍前,伪装只作合规传输策略。

---

## 7. 现状 (Current State)
- **landing** `ddc0d729`(领先 main 500+);本驱动自 `e89d7fce` 起已落 **35+ 闭环**;本会话:Wave-1 网关核心(xAI/Kimi 上游 OAuth + Codex 入口)+ **W2 全波 5 闭环**(加权selector/告警投递/usage过滤/session-hash/租户级metric-source)+ 跨包 refresh 回归修复 + AGENTS.md 规则澄清。
- **标杆功能树** `/home/ubuntu/benchmark/`(8 模块 A-H,2322 行,六级状态禁虚标):已完成 **1330** / 部分完成 395 / 后端有·前端缺 177 / 缺失 **399**(原约497)/ 未做 21。基线刷到 b55754d7。
- **Rust**:tls-sidecar `cargo test` 36 测试全绿;cargo 1.95 在 kaifa(`source ~/.cargo/env`)。整体仍 Owner-decreed exploratory(标杆树记 `部分完成`)。
- **在建/下一步**:W1 Gemini 原生(按纠正后正道=新包,不绕冻结);Track-2 Rust 生产化决策待 Owner。

---

## 8. 唯一真相源 (Source of Truth)
| 用途 | 位置 |
|---|---|
| **规划/规则/战略(本文件)** | repo `docs/00-MASTER-PLAN.md` + `/home/ubuntu/benchmark/` + 桌面 `kaifa\` |
| **功能态势(逐行,活)** | 标杆功能树 `/home/ubuntu/benchmark/`(A-H + master + INDEX)+ 全网关审计 `00-WHOLE-GATEWAY-AUDIT-2026-06-07.md` |
| **可视化看板** | `HUAKAI-gateway-status-2026-06-07.html`(9 轴 + 六级环 + 5 波 + Rust) |
| **硬规则原文** | `AGENTS.md`(codex 必读)· `CLAUDE.md` #1-#16 · `docs/RULES.md` |
| **本会话纠错** | memory `gateway-scope-and-frozen-rule.md`(冻结真义 + Rust 范围 + 对准 landing) |

> 规则改动 → 同步 `AGENTS.md`/`docs/RULES.md` + 本总纲;功能状态变 → 刷标杆树 + 看板。**不再东一块西一块。**
