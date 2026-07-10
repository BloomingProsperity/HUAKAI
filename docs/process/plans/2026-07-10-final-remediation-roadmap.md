# 2026-07-10 HUAKAI 最终修复+建设路线图（合成终版·无后缀）

> 依 CLAUDE.md #10：Claude 与 Codex 各自独立起草（`-claude.md` / `-codex.md`），本文件是两版交叉讨论后的**合成终版**。执行只认本文件。
> 综合本轮：6 账号直连验证 + Antigravity 一号多厂商实证破解 + 系统性普查双轨（codex 21 / workflow 28，26 双确认，S0=0，确认区 S1=3 / S2=6 / S3=10）。
> 系统性根因两轨共同命名 **「构件完工幻觉」——采集面建好、serving 面未接/占位/端点错，两半之间无跨层一致性关卡**。

## 0. 交叉讨论结论（agree / conflict / gaps）

### 0.1 两版一致（直接采纳）
1. **第一刀=薄能力闭合闸**（root 节点），小、不触钱、自带永久守门测试。
2. **拟真/指纹最后做 + 默认 park + 显式 opt-in**；协议可用性可以现在做，身份伪装缓。
3. **钱/配额/entitlement 全 Owner-gated**，未决用「fail-safe / test-only / hidden」安全默认。
4. **零功能缩水**：所有延后项有明确 disposition（Feature Flag / Hidden Experimental / Mandatory Roadmap / Owner Decision）。
5. **Antigravity 战略级、ROI 最高，但对外售卖必须晚于钱/安全门**。

### 0.2 冲突处（Claude 让步给 Codex，附理由——这是本次讨论的实质产出）
| 分歧点 | Claude 初版 | Codex 版 | 终版取 Codex，理由 |
| --- | --- | --- | --- |
| **解阻先后** | Phase 1 先上 Antigravity（ROI 最高） | 先 Claude OAuth（R1A）验证闸，Antigravity 数据面并行但对外售卖后置 | Claude 未知量远小于 Antigravity（已有真账号+adapter+切片计划），是**用真实生产链验证 R0 闸是否真兜底**的最低风险方式；Antigravity 的 ROI 只在「对外售卖」兑现，而售卖本就 money-gated，先并行做它的 wire 数据面、不抢 Claude 的验证位，一分不亏 |
| **Antigravity 协议族** | 含糊说「新 protocol family + 多厂商路由」 | 明确**一个** `antigravity_session` wire family，model-origin 是**数据轴**不是 transport 轴，**禁止**拆 `antigravity_gemini/claude/gpt` 三族 | 拆三族会复制 adapter/健康/账号槽，把**一个订阅当三份容量**算——是错的。一个 wire、来源做数据维度才对 |
| **拟真共享层** | 「统一账号→UA/session 生成层，mimicry composer 跨 vendor 共享，改一处动全部」 | **不建**跨 vendor 万能 mimicry composer；只共享中性 primitive（profile 版本/host allowlist/typed error/审计/最终 body hook）；Claude system/tool 变换 ≠ Antigravity CloudCode envelope=两个 composer | 我那句「改一处动全部」描述的正是要**避免**的爆炸半径，我却当成要拥抱的抽象——这是我框架里的假抽象。中性 primitive 共享、vendor 专属 body 逻辑各自独立才对 |

### 0.3 Codex 补上、我漏掉的缺口（并入终版）
1. **四身份轴**（§5.1）：serving vendor / protocol family / model origin / client protocol 必须分开。
2. **模型发现账号级、非全局**（§5.4）：直接复用现有 vendor-global modelsync 会把「账号 A 看不到某模型」误判成**全局下架**（现 `modelsync` 按 vendor 完整快照禁用缺失 alias）。必须账号级 snapshot→聚合。
3. **schema 约束**：`models.protocol_family` 是 model 级单值、binding 无自有 family、pool 按 resolved family 精确过滤 → 同一公共 alias 现在**无法**同时 fallback 到 native Anthropic 与 Antigravity 两族。MVP=**source-qualified alias**（区分 native / Antigravity 路径）；统一 alias 跨族 fallback 列 Owner-gated route-candidate 演进。
4. **`remainingFraction` 只是调度信号**，不是货币/token/quota，**禁止**写 billing ledger 或用户余额。
5. **R2 安全边界作为独立门**（登录开关只挡 UI 需服务端双校验；quota retryable 静默 fail-open 需 typed 5xx）——先于 Antigravity 对外。
6. **三笔账分离**（§5.5）：对客收费（按公共 alias/定价）/ 上游成本归属（记到 Antigravity·Google One 订阅，**不因模型叫 Claude/GPT 就记成 Anthropic/OpenAI API 成本**）/ 分析维度。
7. **transport `mimicry_switch` 默认开启姿态**=一个 default-flip，需单列 Owner 决策纠偏。

## 1. 拓扑排序（动上游点先）

```text
R0 薄能力闭合闸（唯一共同前置，≤1.5 工程日硬上限）
├── R1A Claude OAuth official-only serving（当前最高 S1，第一个价值验证）
├── R1B 可售模型↔价格闭合  [OWNER-GATED: money]
├── R1C Vertex / Kimi / xAI 当前账号链修复
├── R1D 两条 strength=1 第二镜头核实（credential_state 写面 / Ernie 双凭据）
└── R3A Antigravity wire 实验 + 静态模型 canary（与 R1 并行启动）

R1A + R1B + R2 ──> 当前 6 账号「可采集且可服务」门
R0 + R3A ──> R3B Antigravity 单账号三来源内部 canary
R2 + R4 + R3B ──> R5 Antigravity 动态目录 + entitlement 路由 + 对外售卖
R0 ──> R6 设置/健康/长尾凭据
R1 稳定 + Owner 合规批准 ──> R7 深拟真/指纹（默认 park）
```

## 2. R0：薄能力闭合闸（立即第一刀，6–10 工程小时）

**只建一个最小 `ServingCapabilityContract`，不手写第四张巨表**（其余事实从真实 registry / handler / model registry / pricing snapshot / 当前 env 查询，不复制）。硬上限 ≤1.5 日；若还在扩框架就停，先用最小 contract 解 Claude。

Contract 只承载现有层无法推导的关系：family↔vendor、允许的 auth mode/runtime credential kind、request/response/stream shape、release state（`scaffold/experimental/released/retired`）、是否必须有价才能对外、model discovery 是 global 还是 account-scoped。

**五条硬不变量**（判别测试，删任一站点→红）：
1. `CatalogVisible(mode) ⇒ finalizer ∧ acquisition/refresh disposition ∧ ≥1 个 released/experimental serving contract`；只采不能服务→UI 明示「仅采集/实验，不可 serving」。
2. `ProviderConfigEnabled(family) ⇒ 当前进程 registry 真有 adapter ∧ response parser ∧ request marshal ∧ stream scanner ∧ pool vendor ∧ transport policy`；env off 不能仅凭「代码有 opt-in 路径」允许 enabled=true。
3. `AccountEligible(family, auth_mode) ⇒ runtime credential kind 被 adapter 接受 ∧ 未过期 ∧ 权威健康允许`。
4. `ModelSellable(alias) ⇒ model active ∧ binding active ∧ ≥1 eligible account 支持 wire model ∧ pricing 可解析 ∧ usage 可结算`；discovered ≠ sellable。
5. `SettingAdvertised(key) ⇒ ≥1 生产 consumer ∧ effective value 可观测`。

**失败姿态**：`released+enabled` 违反→启动 fail-loud / 写入拒绝；`experimental` 违反→不挡网关启动但管理面标红、禁绑真流量；`scaffold/retired`→默认隐藏、已有配置只读保留 fail-closed、不删数据；价格缺失→`sellable=false`、发布/绑定期阻断（不是每次 reserve 才 503）。

**第一刀验收**：默认 env / 单 env on / 全 env on 三矩阵；删任一 adapter/parser/marshal/vendor/price fixture→红；钉住当前 Claude OAuth=`collectable_not_serving`、当前 Antigravity=`experimental_wire_unverified`；管理端不得把 env-off family 接受为 enabled；报告列每个 family 缺的站点（非只返回 bool）。爆炸半径=registrydefault / admin catalog / credential mode catalog / 现有对称测试；不碰 schema/money/auth core。

## 3. R1：闭合当前 S1 与已验证账号（约 8–14 工程日，可并行）

### R1A Claude OAuth official-only serving（2–4 日）——第一个价值验证
注册 session family，补 response/SSE/HCSF/pool/admin/model CHECK 六站对称；校验 auth mode；本地过期热刷新 + 资源释放。当前 `anthropic/oauth_session.go` 的 adapter 已收 OAuth/session，但平台身份+registry 未闭合。默认 official-only；非官方 body 拟真进 R7。
**验收**：流式/非流式/HCSF 三路；A 号过期→abort/release/refresh/exclude，B 号成功只结算一次；删任一六站登记测试红。
**[OWNER-GATED: schema/auth/default]** models CHECK、client gate 默认、真实账号灰度。

### R1B 可售模型↔定价闭合（1–2 日 + Owner 定价时间）
「无价但可启用」改成**发布期阻断**；Owner 选补价或保持 operator-test-only。现有国内价格迁移未覆盖 Kimi/Grok/Step。
**验收**：`enabled vendor models ⊆ priced-or-test-only`；删一条价→发布门红（非线上 503）。
**[OWNER-GATED: money]** 各厂对客价、是否允许零价、历史 pricing version 变更。

### R1C Vertex SA / Kimi 设备码 / xAI redirect（4–7 日）
Vertex：真实 SA assertion→token 铸造+刷新，移除「字段名像 metadata 却拒 link-local」的伪路径；Kimi：改真实编码+设备头 contract（现用通用 JSON helper）；xAI：补经验证的固定回调 contract。
**[OWNER-GATED: auth core/runtime dependency]** OAuth client/redirect/SA signing/依赖选择。

### R1D 两条 strength=1 第二镜头（0.5–1 日）
全仓确认 `provider_accounts.credential_state` 是否确无生产写回（未确认前不改 schema/选号）；确认 Ernie 只承诺 v2 单凭据还是也承诺经典双凭据（据结论收窄 UI 或补 auth mode，不凭空实现）。

## 4. R2：安全与强制边界（约 3–6 日，Antigravity 对外前置）

1. 登录 provider 开关在 start+callback **两处服务端**校验，callback 用 flow 创建时的配置版本（防中途开关竞态）；当前只解析可用 provider、不读 UI enabled 集。
2. quota retryable/infra error 不得静默放行；选 `fail-closed / bounded degrade / owner-configured` 之一并给 typed 5xx，不把 DB 故障伪装成余额不足。
3. budget 与 quota 同一故障姿态+同一观测词汇，不得一层默认开一层默认关。
**[OWNER-GATED: auth/quota/default]** 全属 auth core / quota enforcement / 默认行为变化。
**验收**：高并发 serializable retry 耗尽不超 cap；后端故障响应区别于真实 quota deny；开关关闭后直调 start/callback 都被拒。

## 5. Antigravity 架构决策 + R3/R5

### 5.1 四身份轴（必须分开）
| 轴 | 值 | 用途 |
| --- | --- | --- |
| serving vendor | `antigravity` | 账号/transport/健康/上游成本归属 |
| protocol family | 现有 `antigravity_session`（**唯一**） | 选 adapter；表示 CloudCode internal wire |
| model origin | `google/anthropic/openai/internal/unknown` | 展示/能力/分析，**不**选 native adapter |
| client protocol | OpenAI/Responses/Anthropic/Gemini | HCSF 入站转换，与上游 wire 解耦 |

### 5.2 R3A/R3B Antigravity 静态模型内部 canary（约 5–8 日；依赖 R0，对外还依赖 R2/R4）
- canonical auth mode=`vendor=antigravity, auth_mode=oauth`；旧 `gemini/antigravity` 只读兼容+迁移提示，不静默删号。
- OAuth redirect/scope/token endpoint 按实测规格（`docs/process/plans/2026-07-10-antigravity-multivendor-spec.md`）；secret 只从 secret store 读，计划不落密文。
- 抽中性 `cloudcodewire` composer（action URL / inner envelope / stream query / response envelope），不知 OAuth/account/pool/billing；Antigravity wrapper 注入 daily endpoint / project / model / credit / UA/auth；旧 Code Assist 保留独立身份、默认 retired/hidden。
- HCSF request shape 从 OpenAI 改 Gemini；response parser 从 OpenAI 占位改 CloudCode/Gemini envelope；SSE scanner 可复用但需实流证 frame/终止/usage。
- daily host 固定 allowlist，不自动 fallback prod，不把 prod 当多厂商等价端点。
- 仅批准 source-qualified alias 做 canary；`API_PROVIDER_INTERNAL`/空 display 默认隔离。
**验收**：同账号分别路由 Gemini/Claude/GPT wire model，三者都经**同一** `antigravity_session` adapter；body 精确含 model/project/request/已批准信用类型且无 token 入日志；非流/流/取消/401/429/5xx/usage 缺失各有 typed 结果；Claude/GPT 也按 CloudCode response 解析（不因名字前缀切 native parser）；删 response unwrap 或 request 退回 OpenAI→红。
**[OWNER-GATED]** 真实订阅账号、daily 私有端点、信用类型、feature flag 默认。

### 5.3 R5 动态目录 + entitlement 路由 + 对外售卖（约 5–9 日；依赖 R2/R3/R4 + Owner schema/money）
- **账号级 capability snapshot**（非喂现有全局 modelsync）：记 account/wire model/origin/display/capability/remaining fraction/reset time/observed_at/stale_at/availability，不存 token。
- 聚合流程 `discovered → classified → operator-approved → priced → bound → released`；只有 released 进公共 alias。
- 单账号目录失败只标该账号 snapshot stale，不删全局模型、不影响其它账号；空/大幅收缩需人工确认。
- 选号先满足 account-model capability，再看剩余比例/冷却/粘性/槽/成本。
**[OWNER-GATED: schema/money/quota]** 新表/字段、售价、订阅成本摊销、entitlement 使用、公开 alias。

### 5.4 动态模型进 registry 的规则
`fetchAvailableModels` 是 account-scoped probe 非 global sync；`apiProvider` 只做 origin 分类，internal/unknown 默认 quarantine；同 wire ID 多账号出现→聚合存在性但 eligibility 逐账号；最后一个账号 edge 消失只标 unavailable、不删 operator alias/价格/历史 usage；新模型默认 `discovered`，过 smoke+能力+price+binding+批准才 `released`；`remainingFraction/resetTime` 带 TTL，过期标 unknown（不把旧 0.8 当永久、不因未知伪造 0）。

### 5.5 billing/entitlement 三笔账（Owner-gated）
①对客收费（按公共 alias/定价版本，与模型来源无必然等价）②上游成本归属（记到 Antigravity·Google One 订阅+entitlement pool，**不记成 Anthropic/OpenAI API 成本**）③分析维度（model origin/wire model/serving account/credit type）。`remainingFraction` 仅调度信号，禁入 ledger/余额。默认建议：usage 不可靠解析的模型只内部 canary、不对外计费。

## 6. R4/R6/R7（其余）
- **R4 钱/槽/健康生命周期完整性**（约 5–9 日，可与 R3 并行，R5 前置）：billing abort↔quota release 补偿给明确 SLA/状态（不假装跨服务原子）；media claim lease 与 timeout 不可漂移（快照不可变或续租）；account health `auth_expired` 终态但注释/扫描器一致、`risk_control` 是否探测恢复 Owner 选；cooldown/Codex 指纹设置要么真消费要么标 roadmap。**[OWNER-GATED: billing/quota/schema/default]**
- **R6 尾项**（约 6–11 日）：Azure AAD 真实刷新；credential 状态权威投影（第二镜头成立后）；Grok web session（Owner 要就补，否则保留 hidden 不删 adapter）；Ernie 结论后收窄；env-gated Cursor/Copilot/Kiro/Windsurf 只有真 contract 完成才从 scaffold 晋级；设置 consumer registry + effective-value 运维面。
- **R7 深拟真/指纹**（约 5–10 日，默认 park）：official-only 不依赖它；不建万能 mimicry composer；transport 默认开启姿态需 Owner 纠偏。**[OWNER-GATED: auth/default/legal/ToS]**

## 7. Owner-gated 决策清单
| Gate | Owner 决定 | 未决安全默认 |
| --- | --- | --- |
| O1 schema | account-model capability 表、route candidate family、Claude family CHECK、media/health 字段 | 不迁移；静态 canary/独立 alias |
| O2 auth core | Antigravity OAuth 固定配置、xAI/Kimi/Vertex/Azure、登录 provider 开关 | 新模式 hidden/fail-closed；不删号 |
| O3 money | 缺价、Antigravity 售价/月费摊销、usage 缺失、credit overage | test-only，不对外，不写虚构上游成本 |
| O4 quota/default | infra fail 姿态、budget/quota 统一默认、健康自动恢复 | money/quota fail-safe；typed 5xx；不静默放量 |
| O5 internal endpoint | daily endpoint、真实 Pro canary、host/profile rollout | per-account flag off，仅 mock/contract test |
| O6 mimicry/合规 | Claude 深拟真、设备/指纹/header、transport 默认 | official-only + standard transport |
| O7 runtime dependency | Vertex JWT/OAuth 库等新依赖 | 优先 stdlib/既有依赖，否则等批准 |

## 8. 最不确定的关键 open questions（先证再算）
1. **GPT 经 Antigravity 是否真跑通**内容/流/工具/usage？实测规格只明确 Claude/Gemini 200，GPT 只「目录可见」——serving 未证，不用可见替证。
2. Claude/GPT 经 Antigravity 返回是否完全同 Gemini CloudCode envelope（决定 response parser）。
3. `enabledCreditTypes` 是否每请求必须、是否触发付费 overage、有无 free/credit 双层。
4. Owner 实测账号当前落哪个 HUAKAI vendor/auth mode，双轨合并要迁移还是仅代码兼容。
5. 现 schema 是否现在演进到 binding-level family，还是先 source-qualified alias 拿更快更稳的 MVP。

## 9. 立即执行：第一刀
**R0 薄能力闭合闸**（6–10 工程小时）：写 `ServingCapabilityContract` 最小字段 + 五不变量判别测试 + 闭合报告；把现有局部对称守卫改「默认/单 env/全 env」三矩阵；catalog 同查 finalizer+serving readiness；provider config 校验读「当前进程 enabled family」；加 sellability 预检（只报告/阻断发布，不改任何价格）。**不触 schema/money/auth core**。第一刀落地即钉住 Claude OAuth=`collectable_not_serving`、Antigravity=`experimental_wire_unverified`，随后 R1A 把 Claude 推到 `released`。

**写码全交本机 codex（gpt-5.6），Claude 只规划/spec/验收**（记忆硬规则）。

---
Source files read（合成自两份 -claude/-codex 草案 + 实测规格 + 双轨报告）：
docs/process/plans/2026-07-10-final-remediation-roadmap-claude.md; docs/process/plans/2026-07-10-final-remediation-roadmap-codex.md; docs/process/plans/2026-07-10-antigravity-multivendor-spec.md; docs/process/plans/2026-07-10-claude-oauth-serving-mimicry-{claude,codex}.md; tasks/wy4lmo211.output; tasks/bkrmcrpdq.output
Lane: specifier（合成，不改生产代码）
Agent: Claude Opus 4.8 / 06b5fe50
UTC: 2026-07-10
