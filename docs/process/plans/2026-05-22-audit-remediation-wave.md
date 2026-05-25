# 2026-05-22 全仓深度审计 — 补救波权威计划(合成稿)

> **状态:待 Owner 批准开第一波。**
> 本文是平行起草(`-claude.md` + `-codex.md`)交叉合成后的权威稿。
> 结构主体采用 codex 稿(12 波、依赖驱动排序、文件重叠矩阵);并入 Claude 稿的执行纪律(参照对照闸门、commit 规范、P2 联动)。

## §0 元数据与授权出处

| 字段 | 内容 |
|---|---|
| Owner 决策 1 | "先深挖全仓再一次补"(2026-05-22) |
| Owner 决策 2 | 范围 = **全补 56 条**,不丢 MED 进 backlog(2026-05-22 AskUserQuestion) |
| Owner 决策 3 | 节奏 = **串行**,一次一波(2026-05-22 AskUserQuestion) |
| 起草方式 | Claude + codex 各自独立起草(CLAUDE.md #10 平行交叉),codex 未读 Claude 稿 |
| 来源 findings | 4 区 zone 文档共 53 条 + 首轮遗留 O-1/O-2/O-3 = **56 条**(HIGH 30 / MED 24 / LOW 2) |
| clean-room | 全部 HUAKAI 内部代码,无参考项目源码读取 |
| 抽验 | Claude 已独立读源码复核 C-01 / B-01 / B-03 / C-07 四条最致命 finding —— 全部确认为真 |

源 zone 文档:`docs/process/research/2026-05-22-deep-audit-{gatewayhttp,billing-proto,routing-auth,rust}.md`
平行草案:`docs/process/plans/2026-05-22-audit-remediation-wave-{claude,codex}.md`

## §1 头号系统性根因(两 agent 独立命中)

**「持久化安全/金钱/信任链事实」被设计成 optional 或 best-effort —— 写入失败后仍允许用户可见的成功。**

跨 `gatewayhttp` / `billing` / `eventbus` / `auditledger` / `auth` / `credentialstore` / `channelhealth` / `gateway/forwarder` / Rust attempt reporter。根因不是某处缺一个 `if err != nil`,而是**缺一条统一策略**:哪些事实必须在返回成功前持久化、哪些可进可重放 reconciliation、哪些仅 dev 可选。

衍生根因(均需统一治理,非逐点打补丁):
2. 信任边界从 nullable 依赖或客户端可控元数据推断(nil ledger/signer/store 即放行;OAuth endpoint 来自凭据 JSON;Rust 路由信 header)。安全默认应为"未经控制面认证/配置即不可信"。
3. 内部错误明细与对外协议面未分离(`err.Error()` 进 JSON body / header / SSE 帧 / eventbus state / DLQ payload)。
4. 协议规范化缺单一 owner 契约(tool delta / call id / session continuation 各自漂移)。
5. 测试 stub 屏蔽真实生产约束(nullable audit id、事务回滚、Serializable 冲突、缺 tenant `WHERE`、吞 DB 损坏、no-op audit writer)。

## §2 主题分组(56 条 → 8 主题,每条仅属一个)

| 主题 | 名称 | findings |
|---|---|---|
| T1 | 外部安全边界与公开错误面 | C-01 C-02 B-14 O-1 GW-02 GW-04 GW-05 GW-06 GW-09 C-18 B-11 C-12 |
| T2 | 缓存与协议身份隔离 | GW-01 C-06 C-15 C-16 |
| T3 | Trust ledger / audit durability / 读取完整性 | GW-07 B-12 B-13 B-15 C-13 C-14 GW-10 C-03 C-04 C-05 C-10 |
| T4 | Billing 金钱事实与幂等正确性 | B-01 B-02 B-03 B-04 B-05 |
| T5 | Routing 容量 / 健康门控 / 路由 perf | C-07 C-08 C-09 C-17 O-3 |
| T6 | Usage / evidence provenance 与计费来源可信度 | GW-03 GW-08 C-11 |
| T7 | 跨协议流式 tool-use 正确性 | B-06 B-07 B-08 B-09 B-10 |
| T8 | Rust exploratory 预生产硬化 | D-01..D-10 O-2 |

## §3 补救波分解(12 波,串行)

| 波 | commit 单元(`<模块> <中文说明>`) | findings | 估时 | 高风险? |
|---|---|---|---:|---|
| **W1** | `security 收紧外部信任边界` | C-01 C-02 B-14 O-1 | 2.0d | 是(auth core) |
| **W2** | `gatewaycache 隔离协议缓存键` | GW-01 | 1.0d | 否 |
| **W3** | `errors 建立公开错误安全模型` | GW-02 GW-04 GW-05 GW-06 GW-09 C-18 B-11 C-12 | 2.0d | 否 |
| **W4** | `trustledger 强制账本引用与完整性` | GW-07 B-12 B-13 B-15 C-13 C-14 | 3.0d | 是(信任链核心) |
| **W5** | `audit 原子化敏感变更审计` | GW-10 C-03 C-04 C-05 C-10 | 2.5d | 中(含 schema 可能) |
| **W6** | `billing 修复金钱事实幂等与结算` | B-01 B-02 B-03 B-04 B-05 | 4.0d | 是(billing ledger) |
| **W7** | `routing 收紧容量与健康门控` | C-07 C-08 C-09 C-17 O-3 | 3.0d | 中 |
| **W8** | `usage 修正用量证据来源` | GW-03 GW-08 C-11 | 2.0d | 中 |
| **W9** | `proto 修复跨协议流式工具调用` | B-06 B-07 B-08 B-09 B-10 | 3.5d | 否 |
| **W10** | `protocols 生产协议注册与投影收口` | C-06 C-15 C-16 | 1.5d | 否 |
| **W11** | `rust 安全边界预生产硬化` | D-01 D-02 D-03 D-06 D-10 | 3.0d | 否(非生产) |
| **W12** | `rust 账务遥测预生产硬化` | D-04 D-05 D-07 D-08 D-09 O-2 | 2.5d | 否(非生产) |

**总计 30.0 codex-day。** Go 生产链路 W1-W10 = 24.5d;Rust W11-W12 = 5.5d。

> **W5 含 §1 P2 收尾** —— GW-10 即 §1 P2 的审计-变更原子性问题;P2 已完成的租户隔离修复不单独提交,随 W5 一并落地提交。

## §4 执行顺序与理由(依赖驱动,非叙事驱动)

1. **W1 security** —— C-01 SSRF 会带 OAuth secrets 出网、B-14 公开 verify 跨租户读、C-02 漏 `client_secret`、O-1 生产 panic。**唯一可被外部主动利用的一类,必须最先。**
2. **W2 cache** —— GW-01 跨 endpoint 复用响应是 live 正确性/信任风险;改动小,早落地。
3. **W3 errors** —— 先建公共错误模型(`public_code/public_message/internal_error`),避免后续波继续扩散 `err.Error()`。
4. **W4 trustledger** —— 定义 request completion / streaming ledger / append-read 的 fail-closed/degraded-mode 契约;**W5/W6/W8 都依赖此契约**。
5. **W5 audit** —— admin/credential/channel-health 审计原子化;排 W4 后,需引用 W4 的 fail-closed 规则。
6. **W6 billing** —— B-01..B-05 money-path;排 trust/audit 契约后(B-02 依赖真实 ledger id/signature 策略,B-03 依赖 durable financial fact 策略)。
7. **W7 routing** —— C-07 绕并发 cap、C-09 fail-open 选坏通道;影响成本/可靠性,多为 misconfig latent,排已确认 money ledger 后。
8. **W8 usage** —— 修真实用量与 evidence provenance;排 billing validator 后,避免新 usage source 绕过 W6 的 token/value 边界。
9. **W9 proto** —— 流式 tool-use 是生产功能正确性 HIGH,但不如 security/money/trust 紧急;波较大,放数据面基础收口后。
10. **W10 protocols** —— mock/test-only config、HCSF fallback、session 协议门控,latent-on-misconfig,放 Go live 后半段。
11. **W11 rust-security** → **12. W12 rust-telemetry** —— Rust 当前 exploratory 未接生产。
    **条件升级:若 Owner 决定 Rust 进 canary,W11/W12 必须提前到 W7 之前,并新增 release gate「Rust 未过 W11/W12 不得收生产流量」。**

## §5 跨波文件重叠热点(串行 commit 排序依据)

| 包 / 文件簇 | 涉及波 | 重叠风险与排序 |
|---|---|---|
| `cache/key.go` + gatewayhttp 调用点 | W2 | W2 独占;先 bump key version 再做后续 gatewayhttp 大改 |
| `gatewayhttp/chat_completions_{handler,dispatch,stream,attempt,handler_headers}.go` | W2 W3 W8 | 热路径;串行序 cache key → error model → usage/evidence |
| `gatewayhttp/chat_completions_billing.go` | W4 W8 | W4 ledger 策略先落,W8 evidence 后 |
| `gateway/forwarder*.go` | W3 W4 W8 | 高重叠:W3 脱敏流式错误、W4 ledger 计时/fail-closed、W8 缺 usage;commit 严格按序,测试收窄 |
| `auditledger/{ledger,postgres,privacy}.go` | W1 W4 | W1 tenant-scoped verify 先,W4 append/read 完整性后 |
| `auth/{antigravity_token_provider,sanitizer,storm_controller}.go` | W1 W5 | W1 endpoint/脱敏/panic 先;W5 审计 durability 后(同文件) |
| `eventbus/{types,bus}.go` | W3 W4 | W3 脱敏 handler failure/DLQ;W4 收紧 RequestCompletionEvent 校验 |
| `channelhealth/{failover,store_postgres,types}.go` | W5 W7 | W5 签名审计、W7 fail-open/vendor 健康 —— 不可合并,W5 先 |
| `credentialstore/{postgres_store,types}.go` | W5 W10 | W5 审计语义、W10 Azure mock 校验;同包不同文件 |
| `pool/**` | W7 | W7 独占(PASR / DBSlotManager / vendor mapping / route-plan cache) |
| `proto/**` | W9 | W9 独占,横跨 OpenAI/Anthropic/Gemini renderer |
| Rust `listener.rs` `proxy_engine/*` | W11 W12 | W11 先(去 mock/header 信任旁路),W12 后(report 路径) |

## §6 硬依赖约束

| 依赖项 | 先落地 | 后续依赖 | 原因 |
|---|---|---|---|
| 公共错误模型 | W3 | W10 及后续 gateway 改动 | 新错误返回稳定 `public_code` 而非 `err.Error()` |
| Ledger/audit-ref 契约 | W4 | W5 W6 W8 | 无 W4 的 required ledger/signature 语义,W5/W6 无法判定 fail-closed vs degraded |
| auditledger tenant-scoped 查询 | W1 | W4 测试 | B-14 改 ledger 接口面;W4 corruption/read 测试用新 scoped 查询 |
| token/count 校验 helper | W6 | W8 | C-11/GW-08 引入估算/ambiguous usage,须过 W6 集中边界校验 |
| 路由 slot 不变量 | W7 | 后续 PASR/health perf | O-3 route-plan cache 不得缓存绕过 slot/health gate 的计划 |
| canonical tool-use enum/id 契约 | W9 前半 | W9 后半 | renderer 修复须等 upstream adapter 与 canonical event 统一 enum/opaque id |
| Rust 路由/安全硬化 | W11 | W12 + 任何 Rust canary | 请求规划仍可伪造/mock-bypass 时遥测正确性无意义 |

## §7 测试纪律(`feedback_risk_based_testing`)

每条 finding 的补救必须带一个对应**具体风险**的测试,不是"能跑通/全绿"。stub 不得屏蔽风险所在(若风险活在真实 DB 约束/账本/并发,stub `return nil` = 没测)。

### 必须真 PostgreSQL integration test 的波(`integration_pg` tag)

| 波 | 真 PG 覆盖点 | 为何 stub 不够 |
|---|---|---|
| W1 | B-14 tenant-scoped audit verify 查询 | 需真 `WHERE tenant_scope_ref` 行为 + request-id 碰撞行 |
| W4 | B-12/B-13/B-15/C-13/C-14/GW-07 ledger append/read/fail-closed | 真 JSONB decode、Merkle root 字节、nullable 列、事务、signer 缺失 |
| W5 | GW-10/C-03/C-04/C-05/C-10 变更+审计 事务/DLQ | 须证明审计 insert 失败时变更回滚 或 存在 durable recovery 行 |
| W6 | B-01..B-05 claim reserve/settle/refund | unique index、committed-claim replay、事务回滚、slot release 行、signed cost 字段 |
| W7 | C-07/C-08/C-09/C-17/O-3 slot/retry/health/cache | Serializable 冲突与 in-flight 计数器无法靠内存 stub |
| W8 | GW-03/GW-08/C-11 usage provenance(请求→ledger→billing 行) | 须证明真行记录 evidence_label / usage_source / confidence / pending_reconciliation |

### 强制对抗性负向测试(每波至少一组"我能不能搞坏它")

- **W1**: SSRF 拒 `169.254.169.254`/loopback/私网 CIDR/非 HTTPS/重定向内网;OAuth 错误体含 `client_secret` 被脱敏;缺/错 `tenant_scope_ref` 不能读他租户;storm controller 生产路径畸形/缺失 state 不 panic。
- **W2**: 同 tenant/vendor/model/body 跨三端点产生不同 L2 key;旧 key version 失效;跨协议缓存体永不被服务。
- **W3**: 上游 4xx/5xx 体(含账号线索)永不出现在 JSON/header/SSE error/DLQ 公开 payload/eventbus state;TCP reset 不被归为 overflow。
- **W4**: 缺 signer/ledger/audit-ref → fail closed 或建显式 durable degraded 项;redactor 失败不能签原始 payload;畸形 ledger JSON/root 返 corruption。
- **W5**: 池/凭据/健康变更期审计 insert 失败 → 无 committed 变更 除非有 durable recovery 项;`SetState(active)` 不被记为 `credential_disabled`。
- **W6**: 同 API key/idempotency/payload 的跨用户 claim 折叠必须冲突或产生独立 claim;合成 `audit-refund-*` 被拒;slot release miss 不能抹掉已 committed usage/billing;负数/`>MaxInt32` token 被拒;refund replay 返存储封顶额。
- **W7**: PASR actual/canary 遇 nil/unavailable slot manager fail closed;Serializable 冲突 retry 后成功无双 slot;未知/缺失 health state 生产被拦;空 vendor 被拒。
- **W8**: 真实上游 dispatch 不能 emit `EvidenceMock`;估算/缺失 usage 不能 confidence `1.0/reported`;有交付无 usage 的流进 pending reconciliation。
- **W9**: tool arg chunk 跨 OpenAI→canonical→Anthropic/Responses 存活;text block 在 tool block 前不移位 slot;非 hex opaque tool id 往返不丢;Responses `previous_response_id` output-only 请求合法时接受。
- **W10**: 仅含 `mock_token_endpoint` 的 Azure 凭据生产被拒;HCSF 控制注入失败不 raw-forward。
- **W11/W12**: 见 codex 草案 §Test Discipline(Rust 波,执行时展开)。

## §8 执行纪律(每波)

1. 每波 = 1 个聚焦 commit,命名 `<英文模块> <中文说明>`(`feedback_commit_naming_v2`,无阶段号/无 PASS 字样/无跨模块混合)。
2. 每波先写测试(TDD),每个测试在注释/命名引用它杀死的风险或 `AT-*` 行。
3. commit 前跑 `codex exec review --uncommitted --full-auto --sandbox read-only`,处理全部 HIGH。
4. **每波收尾对照参照项目同模块**(`feedback_per_slice_ref_recompare`):查缺补漏 + 升级点(架构/算法/生态三维)。
5. 收尾跑全量 `cd backend && go test ./...`,不能 scoped 绿当 repo 绿(`feedback_full_suite_verification`)。
6. 高风险波(W1 auth core / W4 信任链核心 / W6 billing ledger / W5 可能 schema)开波前向 Owner 单独确认(Risk-Based Confirmation Rule)。
7. 一波未绿不开下一波(串行硬约束)。

## §9 留给 Owner 的批准点

1. 批准本 12 波计划与执行顺序 → 即可开 **W1 security**。
2. 确认 Rust(W11/W12)维持最后;若计划让 Rust 进 canary,按 §4 条件升级前置。
3. W1 触及 auth core(`antigravity_token_provider.go` 等)属高风险,开波即需 Owner 确认 —— 批准本计划即视为 W1 放行,或单独说明。

---
Lane:Claude 合成(HUAKAI 内部代码,无 clean-room 约束)。平行草案作者:Claude + codex(独立起草)。
UTC:2026-05-22
