# 2026-07-10 HUAKAI 最终修复 + 建设路线图：Codex 独立平行计划

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: CLIProxyAPI + sub2api + new-api

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors —
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: <relative paths>
  Lane: <specifier | reviewer>
  Agent: <model + ID>
  UTC timestamp: <ISO 8601>

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

| 项目 | 内容 |
| --- | --- |
| Owner 指令 | “独立起草 HUAKAI『最终修复+建设路线图』；这是平行计划，不是审查；只读+产出计划文档，不改生产代码、不 commit。” |
| 任务性质 | specifier-lane 独立排序、根因归并、依赖拓扑与风险门设计 |
| 范围内 | 两轨存活缺陷、Claude OAuth serving、Antigravity 多厂商 serving、能力闭合闸、模型发现、路由、协议、quota/billing 交界、后续长尾修复 |
| 范围外 | 本轮不改生产代码、不改 schema、不跑构建/测试、不调真实上游、不 commit、不决定真实售卖价格或订阅成本分摊 |
| 成功标准 | 19 条双镜头确认缺陷和 2 条待确认项均有处置；Antigravity 有可实施的数据面/模型面/钱面方案；每阶段有依赖、爆炸半径、验收、回滚和 Owner gate；无功能静默缩水 |
| 总工时粗估 | 强制路线 R0–R6 串行约 33–59 工程日；可选/Owner-gated 的 R7 深拟真另加 5–10 日。在 R0 之后允许 2–3 条独立工作流并行时，墙钟约 5–8 周。估算不含 Owner/法律等待和真实上游观察窗口 |
| 第一刀工时 | 6–10 工程小时：只做“薄闭合闸”的判别性契约与当前启用态矩阵；随后 Claude OAuth official-only serving 约 2–4 工程日 |
| 总爆炸半径 | credential acquisition/finalize/refresh、provider registry、models CHECK、model registry、pool selection、HCSF、SSE、transport、pricing reserve、billing claim、quota reservation、账号槽、管理面准入、运行时设置 |
| Observed regions | 52 个实际读取区间：36 个 HUAKAI/共同证据区间 + 16 个三镜生产源码区间 |
| Inferences | 10 项，均以“建议/推断”表述，不冒充上游事实 |
| Open questions | 7 项，见 §12 |

## 0. 独立性、证据完整性与限制

1. 我没有打开或通读 Claude 的平行路线图。为寻找已丢失的 Codex 21 条报告正文，我执行了一次跨目录关键词检索，意外返回 Claude 路线图的第 3、7 行摘要；之后立即排除该文件。那两行不作为证据，不参与排序。本计划因此是“有限污染已披露”，不是虚称绝对零接触。
2. `/tmp/.../tasks/bkrmcrpdq.output` 当前只剩 `EXIT=0`，21 条正文不可恢复；本文不会重建或臆造其逐条内容。
3. workflow 轨结构化结果仍完整：28 条存活、26 条双镜头确认；综合为 19 条确认缺陷和 2 条待确认项，S0=0、确认区 S1=3/S2=6/S3=10。本文用该报告作发现索引，并重新读取关键 HUAKAI 生产路径。
4. Antigravity 的 daily 端点、请求外层、信用类型、模型目录与三个来源族来自 Owner 实测规格；当前落盘规格明确记录 Claude/Gemini 200，GPT 型号已出现在可用目录。GPT 内容成功、流式、工具调用和 usage 形状仍列为独立验收，不用“目录可见”替代“serving 已证”。[HUAKAI: docs/process/plans/2026-07-10-antigravity-multivendor-spec.md:11-44]
5. 本轮未运行构建或测试，符合纯计划要求。

## 1. 结论先行：先修薄闭合闸，再解 Claude，再上 Antigravity

我的排序是：**先做跨层能力闭合闸，但只给它 1 天上限；随后立刻用 Claude OAuth S1 验证；Antigravity 是下一项最高 ROI 建设，不先于闭合闸，也不等待所有长尾修完。**

原因：

- 直接先写 Antigravity adapter，会在现有“采集可见、配置可写、serving 受 env 关闭、响应/HCSF 仍按 OpenAI 占位”的拓扑上再造一个半成品。当前代码已经把 `antigravity_session` 的请求形态映射到 OpenAI、响应也交给 OpenAI 解析，但 Owner 实测 wire 是 CloudCode/Gemini 外层；只改 endpoint 会得到“URL 对了、协议仍错”的第二次构件完工幻觉。[HUAKAI: internal/gateway/upstream_dispatcher_hcsf.go:381-425；internal/gateway/protocol_selector.go:156-167]
- 不能先建一个数周的大一统能力平台。现有仓库已经有 registry↔migration、registry↔transport、registry↔pool vendor 等局部对称测试；真正缺的是“按**当前启用态**把采集、凭据、serving、协议、定价焊成闭环”。第一刀应扩展现有守卫，而不是先重构全仓。[HUAKAI: internal/provider/registrydefault/default_test.go:36-127；internal/pool/vendor_guard_test.go:10-67]
- Claude OAuth 是已知 S1、已有真实账号、已有 adapter 和独立切片计划，未知量小于 Antigravity；先闭合它能验证新闸是否真的保护生产链，而不是只保护集合数量。
- Antigravity 的商业杠杆极高，但它同时引入账号级动态模型、三来源模型、订阅 entitlement、daily 私有端点和 usage 解析。可先做内部 canary；“动态自动发布 + 对外售卖”必须晚于钱/配额门。

### 1.1 顶层依赖图

```text
R0 薄能力闭合闸（唯一共同前置，≤1.5 日）
├── R1A Claude OAuth official-only serving（当前最高 S1）
├── R1B 可售模型↔价格闭合（Kimi/Grok/Step 等当前 S1） [OWNER-GATED: money]
├── R1C Vertex / Kimi / xAI 当前账号链修复
└── R3A Antigravity wire 实验与静态模型 canary

R1A + R1B + R2 auth/quota 边界 ──> 当前 6 账号“可采集且可服务”门
R0 + R3A ──> R3B Antigravity 单账号三来源内部 canary
R2 + R4 钱/生命周期完整性 + R3B ──> R5 Antigravity 动态目录与对外售卖
R0 ──> R6 设置/健康/长尾凭据修复
R1 稳定 + Owner 合规批准 ──> R7 深拟真/指纹类工作
```

## 2. 三镜 feature-shape 清单与 HUAKAI 取舍

以下只转述实际读取的生产路径：

| 镜像 | Observed 形态 | 对 HUAKAI 的启示 |
| --- | --- | --- |
| CLIProxyAPI | 把 Antigravity 作为独立 provider/data-plane 执行边界，daily/prod endpoint、信用类型提示与鉴权在该边界组合；模型发现使用某一凭据实例的 access token。其运行时模型目录按“客户端凭据实例→模型集合”登记，并再聚合 provider 视图，而不是假设所有账号模型集合相同。<router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/runtime/executor/antigravity_executor.go:45-59,400-468> <router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:sdk/cliproxy/antigravity_models.go:16-74,89-114> <router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/registry/model_registry.go:269-333,343-479,924-990> | 模型可用性必须账号级；provider 聚合是派生视图。不能把一个账号的目录直接当全局真相。 |
| sub2api | 一个 Antigravity 平台账号可由不同入站路径承接 Claude 形和 Gemini 形请求；两路最后进入同一平台服务边界。其模型发现结果包含每模型剩余比例/重置时间，并有响应体上限和受限跳转。<Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/handler/gateway_handler.go:417-439,784-797> <Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/handler/endpoint.go:154-202> <Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/pkg/antigravity/client.go:622-736> | “一个账号三来源”不等于三个原生 provider；先统一到一个 Antigravity wire family，再保留模型来源维度。目录/配额抓取必须受 SSRF、body limit、host allowlist 保护。 |
| new-api | 本次读取的 channel 类型枚举和 adapter 分派没有独立 Antigravity 分支；其通用形态是 channel 自带类型、key、模型集合和模型映射，再按请求模型选满足条件的 channel。<QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:constant/channel.go:3-59,125-181> <QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:relay/relay_adaptor.go:54-127> <QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:model/channel.go:23-56> <QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:service/channel_select.go:84-162> | 不能从 new-api 借得 Antigravity wire 结论；可保留“模型资格属于可选渠道/账号”的用户结果。HUAKAI 需要更严格地把动态发现、批准、定价、绑定拆开。 |

HUAKAI 的合并升级不是照搬任何一镜：**账号级 capability edge + provider 聚合目录 + 显式 sellability 闸 + 独立 entitlement/cost 维度**。这同时是架构升级（分离身份轴）、算法升级（选号读账号模型余量）和生态升级（运营可见的 discovered/approved/priced/released 状态）。

## 3. 同根聚类：19 条确认缺陷 + 2 条待确认项

“同根”不表示一个代码 diff 自动修完全部；下表区分“共修动作”和“仍需个别 wire/业务修复”，防止以合并为名缩水。

| 根因簇 | 纳入项 | 同一个共修动作 | 仍需独立完成 |
| --- | --- | --- | --- |
| C1 能力闭合与准入真相源分裂 | A Claude OAuth 未注册；C env-gated 可配置但运行未注册；D 可启用 vendor 无价格；#3 Grok session 死脚手架；新增 Antigravity endpoint/协议占位 | 建立当前启用态的 `acquisition → materialization → adapter → request/response/stream → vendor/transport → model → pricing` 闭合闸；管理面只展示真实 readiness | Claude 六站接线、每 vendor 定价/禁用决策、Grok 产品处置、Antigravity wire 重写仍分别实现 |
| C2 凭据获取/刷新契约不完整 | B Vertex SA；#21 Azure AAD；#5 Kimi device flow；#6 xAI redirect；待确认 Ernie 经典双凭据；Antigravity 旧 `gemini/antigravity` 与新 `antigravity/oauth` 双轨 | 每个 auth mode 声明 acquisition、required fields、token mint/refresh、runtime kind、expiry、health transition；启动测试验证每步有消费者 | JWT/AAD/Kimi/xAI/Ernie 都是不同上游协议，禁止用万能 token adapter 假装修复 |
| C3 协议/客户端 profile 仍是占位 | #7 Codex；#8 Gemini Code Assist；#9 Cursor/Copilot；Antigravity 错 endpoint/外层；Claude 深拟真建设 | 统一“版本化 wire profile + host allowlist + typed conformance error + 无秘密审计”的框架；只共享中性 composer primitive | 各 vendor 的 body、响应、usage、工具调用、header 必须各自有一方证据；不做通用指纹伪装器 |
| C4 设置可写但不生效 | #12 登录 provider 开关只挡 UI；#10 冷却设置无人消费；#11 Codex 指纹策略无人执行 | 设置注册表增加 `consumer/effective source/restart requirement`，启动或测试期拒绝“可写无消费者”；后台展示 effective 值 | 登录开关属于 auth core；冷却属于路由健康；指纹门属于客户端安全，仍分开实现/审批 |
| C5 强制层与钱生命周期接缝 | #13 quota retryable 静默 fail-open；#14 budget/quota 故障方向冲突；#15 billing abort 后 quota 槽异步释放窗口；#18 media timeout 与 claim lease 快照漂移 | 建立统一 request/claim/reservation/slot 终态矩阵和跨模块并发/故障注入套件 | 是否 fail-open、事务边界、补偿 SLA、动态 lease 策略都是 money/quota 决策，必须 Owner gate |
| C6 账号健康真相源分裂 | #17 revoked 自愈注释与扫描 SQL 相反；待确认 `provider_accounts.credential_state` 不迁移 | 选定一个权威状态机，明确 `credential state → account health → eligibility` 投影与审计；禁止双列无同步 | 第二镜头确认 credential_state 写面；区分 auth-expired 终态与 risk-control 可恢复态 |

覆盖证明：C1 4 条 + C2 4 条 + C3 3 条 + C4 3 条 + C5 4 条 + C6 1 条 = 19 条确认缺陷；两条 strength=1 分别进入 C2/C6 的“先确认”入口，没有被当成已证事实。

## 4. R0：薄能力闭合闸（立即第一刀）

### 4.1 不建第二份静态清单

现有 `SupportedProtocolFamilies()` 把“存在 opt-in 注册路径”称为 supported，而实际 `Build()` 只在 env 打开时注册；管理端却直接拿前者放行配置，采集目录又只看 finalizer。三个真相源因此可以同时各自正确、组合后错误。[HUAKAI: internal/provider/registrydefault/default.go:69-129,353-389；internal/adminhttp/catalog.go:95-131；internal/adminhttp/provider_catalog_mutation_handler.go:328-372,484-485]

第一阶段不再手写第四张巨型表。先定义一个最小 `ServingCapabilityContract`，其内容只承载现有层之间无法推导的关系：

- family 对应 vendor；
- 允许的 auth mode/runtime credential kind；
- request marshal shape、response shape、stream framing；
- release state：`scaffold / experimental / released / retired`；
- 是否必须有价格才能对外；
- model discovery 是 global 还是 account-scoped。

其它事实从真实 registry、handler registry、model registry、pricing snapshot 和当前 env 查询，不复制。

### 4.2 五条硬不变量

1. `CatalogVisible(mode) ⇒ finalizer ∧ acquisition/refresh disposition ∧ 至少一个 released/experimental serving contract`。若只能采集不能服务，UI 必须明确显示“仅采集/实验，不可 serving”，不能伪装 ready。
2. `ProviderConfigEnabled(family) ⇒ 当前进程 registry 真有 adapter ∧ response parser ∧ request marshal ∧ stream scanner ∧ pool vendor ∧ transport policy`。env off 时不能仅因“代码里有 opt-in 路径”而允许 enabled=true。
3. `AccountEligible(family, auth_mode) ⇒ runtime credential kind 被 adapter 接受 ∧ 未过期 ∧ 权威健康状态允许`。
4. `ModelSellable(alias) ⇒ model active ∧ binding active ∧ 至少一个 eligible account 支持 wire model ∧ pricing policy 可解析 ∧ usage 可结算`。discovered 不等于 sellable。
5. `SettingAdvertised(key) ⇒ 至少一个生产 consumer ∧ effective value 可观测`。无 consumer 时只能是 roadmap/hidden，不能显示“已生效”。

### 4.3 失败姿态

- `released + enabled` 违反闭合：启动 fail-loud 或配置写入拒绝。
- `experimental` 违反闭合：不阻止核心网关启动，但管理面标红、禁止绑定真实流量。
- `scaffold/retired`：默认隐藏；已有配置保留、只读可见、请求 fail-closed，不删除数据。
- 价格缺失：模型可做 operator test，但 `sellable=false`；公共 API 请求在绑定/发布前被阻止，而不是等到每次 reserve 才 503。

### 4.4 第一刀交付与验收

建议第一提交只做契约/判别测试和最小 readiness 查询，不顺手修所有红项：

- 默认 env、单个 env on、全部 env on 三个矩阵；
- 删除任一 adapter/parser/marshal/vendor/price fixture 后测试必须红；
- 明确钉住当前 Claude OAuth 为 `collectable_not_serving`；
- 明确钉住当前 Antigravity 为 `experimental_wire_unverified`；
- 管理端不得把 env-off family 接受为 enabled；
- 报告列出每个 family 缺的站点，不只返回 bool。

工时：6–10 小时。爆炸半径：registrydefault、admin catalog、credential mode catalog、现有对称测试；不碰 schema/money/auth core。若超过 1.5 日还在扩框架，停止扩张，先用最小 contract 解 Claude。

## 5. 拓扑化执行路线

### R1：先闭合当前 S1 与已验证账号（约 8–14 工程日，可并行）

#### R1A Claude OAuth official-only serving（2–4 日）

- 采用既有独立计划的 S1，不把深拟真绑进同一发布：注册 session family，补 response/SSE/HCSF/pool/admin/model CHECK 对称站点，校验 auth mode，处理本地过期热刷新与资源释放。[HUAKAI: docs/process/plans/2026-07-10-claude-oauth-serving-mimicry-codex.md:190-217,327-369]
- 当前 adapter 已接受 OAuth/session，但平台身份和 registry 仍未闭合。[HUAKAI: internal/provider/anthropic/oauth_session.go:16-115；internal/provider/registrydefault/default.go:69-101,212-237]
- 默认继续 official-only；非官方 body 拟真进入 R7。
- **[OWNER-GATED: schema/auth/default]** models CHECK、client gate 默认、真实账号灰度。

验收：流式/非流式/HCSF 三路；A 号过期→abort/release/refresh/exclude，B 号成功只结算一次；删任一六站登记测试红。

#### R1B 可售模型与定价闭合（1–2 日 + Owner 定价时间）

- 先把“无价但可启用”改成发布期阻断；再由 Owner 选择补价或保持 operator-test-only。
- 当前价格选择会先尝试 provider+model，再尝试全局 model；全 miss 即 pricing unavailable，而 reserve 在选号前即失败。[HUAKAI: internal/gatewayhttp/chat_completions_pricing.go:187-225,308-329,433-462；internal/gatewayhttp/chat_completions_dispatch.go:329-385]
- 现有国内价格迁移明确未覆盖部分型号，且没有 Kimi/Grok/Step 条目。[HUAKAI: sql/migrations/0131_domestic_model_pricing.up.sql:1-13]
- **[OWNER-GATED: money]** Grok/Kimi/Step/Hunyuan 及 Antigravity 的对客价格、是否允许零价、历史 pricing version 变更。

验收：`enabled vendor models ⊆ priced-or-test-only models`；删一条价格后发布门红，而不是线上请求才 503。

#### R1C Vertex SA、Kimi、xAI（4–7 日）

- Vertex：实现真实 SA assertion→token 铸造与刷新；移除“字段名像 metadata、实现又拒 link-local”的伪路径。若需要新增 runtime dependency，另过 Owner gate。
- Kimi：把设备授权/轮询改为其真实编码与设备头 contract；现有通用 JSON helper不能继续作为 Kimi 唯一路径。[HUAKAI: internal/credentialacq/oauth_devicecode.go:451-639]
- xAI：补经验证的固定回调 contract 与配置诊断。
- **[OWNER-GATED: auth core/runtime dependency]** OAuth client、redirect、SA signing、依赖选择。

#### R1D 两条 strength=1 的第二镜头（0.5–1 日）

- 全仓确认 `provider_accounts.credential_state` 是否确无生产写回；未确认前不改 schema/选号。
- 确认 Ernie 产品只承诺 v2 单凭据，还是也承诺经典双凭据；若只承诺 v2，改 UI/文档/校验，不凭空实现另一模式；若承诺两者，补独立 auth mode。

### R2：安全与强制边界（约 3–6 日，Antigravity 对外前置）

1. 登录 provider 开关必须在 start 和 callback 两处服务端校验，且 callback 使用 flow 创建时的配置版本，避免中途开关竞态。当前路径只解析可用 provider，不读取 UI 的 enabled 集。[HUAKAI: internal/userauth/social_login.go:95-140,169-213；internal/sitepublichttp/handler.go:62]
2. quota 的 retryable/infra error 不得落入无审计的静默放行。选择 `fail-closed / bounded degrade / owner-configured` 之一，并给客户端 5xx/typed retry，不把 DB 故障伪装成余额不足。当前 glue 对非 deny error 直接放行。[HUAKAI: internal/gatewayhttp/chat_completions_dispatch.go:388-427；internal/quota/errors.go:5-74]
3. budget 与 quota 采用同一故障姿态配置和同一观测词汇；不得一层默认开、一层默认关。[HUAKAI: internal/budgetenforce/enforce.go:27-74]
4. **[OWNER-GATED: auth/quota/default behavior]** 全部属于 auth core、quota enforcement 或默认行为变化。

验收：高并发制造 serializable retry exhaustion 时不能超 cap；后端故障返回区别于真实 quota deny；开关关闭后直调 start/callback 都被拒。

### R3：Antigravity 静态模型内部 canary（约 5–8 日）

依赖：R0；真实对外售卖还依赖 R2/R4。这里先拿到可靠 data plane，不自动发布动态模型。

1. canonical auth mode 选 `vendor=antigravity, auth_mode=oauth`；旧 `gemini/antigravity` 保留只读兼容/迁移提示，不静默删账号。当前两条路径一条暂停、一条用 operator config，必须先统一产品语义。[HUAKAI: internal/credentialacq/types.go:235-243；internal/credentialworker/mode_refresh.go:109-140]
2. OAuth redirect、scope、token endpoint 按 Owner 实测规格修正；client secret 只从 secret store/runtime config 读取，本计划不复制。
3. 抽取中性的 CloudCode wire composer；Antigravity wrapper 提供 daily endpoint、项目、模型、信用类型、auth/profile。旧 Gemini Code Assist 若保留，可复用 composer，但保留独立 provider/auth/entitlement，不合并成一个产品身份。
4. 把 Antigravity HCSF request shape 从 OpenAI 改为 Gemini；把 response parser 从 OpenAI 占位改为 CloudCode/Gemini envelope parser；SSE scanner 可复用，但需实流证明 frame/终止/usage。[HUAKAI: internal/provider/gemini/code_assist.go:72-188；internal/proto/geminicodeassist/code_assist.go:12-101]
5. 仅批准三个已验证/待验证的 source-qualified alias 做 canary；`API_PROVIDER_INTERNAL` 和空 display 默认隔离。
6. daily host 固定 allowlist；不自动 fallback 到错误的旧域名，也不把 prod 当多厂商等价端点。切换 endpoint profile 是显式 rollout 动作。
7. **[OWNER-GATED: auth/internal endpoint/real account/default]** 真实订阅账号、daily 私有端点、信用类型、feature flag 默认。

验收：

- 同一账号分别路由 Gemini/Claude/GPT wire model，三者都经过同一个 `antigravity_session` adapter；
- body 精确含 model/project/request/已批准信用类型，且没有 token/内部 ID 入日志；
- 非流、流、取消、401、429、5xx、usage 缺失分别有 typed 结果；
- Claude/GPT 模型也按实际 CloudCode response 解析，不因名称前缀切到原生 Anthropic/OpenAI parser；
- 未批准/internal 模型不可绑定；删 response unwrap 或把 request shape 退回 OpenAI 时测试红。

### R4：钱、槽、健康生命周期完整性（约 5–9 日，可在 R3 并行；R5 前置）

1. billing abort 与 quota release 保持现有补偿语义，但给出明确 SLA/状态；不能假装跨服务原子。当前是 billing 先提交，quota 后置，失败可入补偿队列。[HUAKAI: internal/quotaenforce/settler.go:146-197；internal/quota/service_settle.go:200-242]
2. media task 的 claim lease 与运行时 timeout 采用不可漂移策略：要么任务创建后 timeout 快照不可变，要么续租；不能 worker 用新值、claim 用旧值。[HUAKAI: internal/mediatask/service.go:51-64；internal/mediatask/store_money.go:101-133；internal/mediatask/worker.go:171-188]
3. account health：`auth_expired` 可终态，但注释/扫描器必须一致；`risk_control` 是否允许探测恢复由 Owner 选。当前 revoked 被 refresh scanner 排除且 nil-until 不自动恢复。[HUAKAI: internal/credentialworker/health_state.go:48-80；internal/credentialworker/mode_refresh.go:366-391；internal/db/billing/pool_accounts.sql.go:650-715]
4. cooldown 设置接入实际 policy，后台显示 effective 值；Codex 指纹设置要么真正消费，要么标 roadmap/不可编辑，不能继续宣称生效。
5. **[OWNER-GATED: billing/quota/schema/default]** 事务边界、补偿 SLA、媒体 lease、健康默认。

验收：并发 cap=2 的三请求判别测试；abort/settle/断连/DB 故障/DLQ 重放；TaskTimeout 在任务中途变化；risk-control/auth-expired 分开恢复；设置值改变后真实 policy 必变。

### R5：Antigravity 动态目录、entitlement 路由与对外售卖（约 5–9 日）

依赖：R2、R3、R4，以及 Owner 的 schema/money 决策。

- 新建账号级 capability snapshot，而不是把 `fetchAvailableModels` 直接喂现有全局 modelsync。现有同步器按 vendor 完整快照禁用缺失 alias；这适合 vendor-global API，不适合“账号 A 看不到、账号 B 仍可用”的 entitlement。[HUAKAI: internal/modelsync/types.go:5-30；internal/registry/model_sync_writer.go:35-101,193-214]
- 账号级记录至少表达：account、wire model、origin、display、capability、remaining fraction、reset time、observed_at、stale_at、availability；不存 token/原始响应。
- 聚合流程：`discovered → classified → operator-approved → priced → bound → released`。只有 released 才进公共 aliases。
- 单账号目录失败只标该账号 snapshot stale；不得删除全局模型或影响其它账号。空/大幅收缩需人工确认。
- 选号先满足 account-model capability，再看剩余比例、冷却、粘性、槽和成本；remaining fraction 是排序/冷却信号，不是绝对 token 数。
- **[OWNER-GATED: schema/money/quota]** 新表/字段、售卖价格、订阅成本、entitlement 使用、公开 alias。

### R6：其余凭据/设置/能力尾项（约 6–11 日）

- Azure AAD 真实刷新；
- account credential 状态权威投影（第二镜头成立后）；
- Grok web session：若 Owner 要该用户结果，补采集+serving；否则保留 `Mandatory Roadmap/hidden`，不删除 adapter；
- Ernie 结论后的独立模式或 UI 收窄；
- env-gated Cursor/Copilot/Kiro/Windsurf 只有真实 contract 完成才从 scaffold 晋级；
- 设置 consumer registry 与 effective-value 运维面。

### R7：深拟真/指纹类工作最后做，且默认 park（约 5–10 日）

- Claude official-only serving 不依赖深拟真；其非官方 body composer、工具名双向映射、严格官方判别维持独立 feature flag。
- Codex/Gemini/Cursor/Copilot 当前占位 profile 先从“已发布能力”降为 experimental/hidden；只做功能必需协议兼容，不把“降低被识别概率”当产品验收。
- 不建跨 vendor 的万能 mimicry composer。可共享的只有：profile version、host allowlist、typed error、审计摘要、最终 body hook；Claude system/tool 变换与 Antigravity CloudCode envelope 是两个不同 composer。
- 当前 transport 全局开关默认非 `false` 即开启所有 mimicry mode，和“高风险显式 opt-in”目标相冲突；默认行为翻转必须单列 Owner 决策。[HUAKAI: internal/transport/mimicry_switch.go:5-35]
- **[OWNER-GATED: auth/default/legal/ToS]** 任一身份拟真、设备/header profile、默认开关、真实账号验证。

## 6. Antigravity 特有架构决策

### 6.1 四个身份轴必须分开

| 轴 | 建议值 | 用途 |
| --- | --- | --- |
| serving vendor | `antigravity` | 账号、transport、健康、上游成本归属 |
| protocol family | 保留现有 `antigravity_session` | 选择唯一 adapter；表示 CloudCode internal wire，而不是模型原创厂商 |
| model origin | `google / anthropic / openai / internal / unknown` | 展示、能力、分析；来自模型目录分类，不选择原生 adapter |
| client protocol | OpenAI/Responses/Anthropic/Gemini 等 | HCSF 入站转换；与上游 wire 解耦 |

**不建议**为一个账号建 `antigravity_gemini / antigravity_claude / antigravity_gpt` 三个 protocol family。三者 endpoint、auth、outer envelope 和 entitlement 相同；拆三族会复制 adapter/健康/账号槽并造成同一订阅被当三份容量。模型来源是数据维度，不是 transport family。

### 6.2 当前 schema 对跨 family fallback 的限制

当前 `models.protocol_family` 是 model 级单值，binding 没有自己的 family；pool 又按 resolved family 精确过滤 provider。因此同一个公共 alias 目前不能自然同时 fallback 到 native Anthropic family 和 Antigravity family。[HUAKAI: sql/migrations/0008_model_registry.up.sql:49-90,125-180；internal/db/billing/pool_accounts.sql.go:641-715]

MVP 建议：

- 公开 source-qualified alias，例如区分 native 与 Antigravity 路径；
- 不在 credentialstore 根据 model 名偷偷改 family；
- 跨来源统一 alias 列为后续 Owner-gated 路由演进：把 protocol/adapter choice 下沉到 route candidate/binding，并重审 claim fingerprint、sticky、failover 和价格。

这不是功能删除：统一 alias + 跨 family fallback 标 `Mandatory Roadmap`；先交付可审计的独立路径，避免隐式路由。

### 6.3 CloudCode 共享 composer 的边界

建议抽出 `internal/provider/cloudcodewire`（最终包名可在实现计划定）：

- 只负责 action URL、inner request envelope、stream query、response envelope；
- 不知道 OAuth、account、pool、billing、vendor origin；
- Antigravity wrapper 注入 daily profile、credit entitlement、UA/auth/project；
- 旧 Code Assist wrapper 保留自身 endpoint/身份，个人版停服不等于删除企业/遗留路径；默认可保持 retired/hidden；
- HCSF/response adapter 共享 Gemini canonical shape，不共享产品状态。

### 6.4 动态模型如何进入 registry

1. `fetchAvailableModels` 是 account-scoped probe，不是 global vendor sync。
2. `apiProvider` 只做 origin 分类；internal/unknown 默认 quarantine。
3. 同 wire ID 在多个账号出现时，聚合模型存在性；账号 eligibility 仍逐账号保存。
4. 一次账号目录缺失只移除该账号 edge；最后一个账号 edge 消失后也只把公共模型标 unavailable，不能删 operator alias/价格/历史 usage。
5. 新模型默认 `discovered`，必须通过 response-shape smoke、能力映射、price、binding 和 Owner/运营批准才 `released`。
6. fetch 的 `remainingFraction/resetTime` 带 TTL；过期后标 unknown，不把旧 0.8 当永久可用，也不因未知直接伪造 0。

### 6.5 billing / entitlement 归属

必须同时保存三种不同事实：

1. **对客收费**：按公共 alias/定价版本计算，和模型来自哪家无必然等价。当前 provider-specific rate table 可用 `providers.antigravity.models.<wire>`，但具体售价由 Owner 决定。
2. **上游成本归属**：归到 Antigravity/Google One 订阅账号与 entitlement pool；不能因为模型名是 Claude/GPT 就记成 Anthropic/OpenAI API 成本。
3. **分析维度**：另记 model origin、wire model、serving account、credit type，支持 ROI/容量分析。

`remainingFraction` 不是货币、token 或可加总额度，不得直接写 billing ledger 或用户余额。它只用于 account-model availability/排序，直到有绝对单位 contract。

**[OWNER-GATED]**：Google One 月费如何摊销、是否允许超额 credit、各模型对客价、是否按 token/请求/订阅售卖、usage 缺失时是否拒绝或人工对账。默认建议：usage 不可可靠解析的模型只允许内部 canary，不对外计费。

## 7. 爆炸半径矩阵

| 项目 | 直接改动面 | 下游/对称点 | 最大风险 | 回滚 |
| --- | --- | --- | --- | --- |
| R0 能力闭合闸 | registrydefault、admin catalog、credential mode catalog、tests | model CHECK、transport、pool vendor、price readiness | 闸过严导致实验功能不可配；闸过松继续僵尸账号 | 只把未发布项降为 explicit experimental，不删数据 |
| Claude OAuth serving | adapter、family、HCSF、response/SSE、pool、migration、expiry retry | model/provider 配置、槽、claim、quota、client gate | 半接线、错误换号、重复结算 | 先解绑 family 流量，再关 adapter；schema down 前查行 |
| 定价闭合 | pricing version、admin publish、reserve | balance、receipt、reprice、公开 price API | 错价/免费/过扣 | 新 pricing version + 生效时间，不原地改历史 |
| Antigravity composer | adapter、CloudCode wire、proto parser | 所有 Gemini/Claude/GPT 模型、流/非流、tools/usage | 一个共享 bug 同时打断三来源 | per-account flag；退回 hidden/canary，保留账号 |
| 动态模型 | OAuth probe、account capability、registry/admin | pool eligibility、alias、price、model health | 一个账号目录污染全局/误下架 | snapshot 版本化；不删除 operator 资产 |
| entitlement routing | account-model score、cooldown | sticky、failover、并发槽、capacity forecast | 把比例误当额度、跨账号重复消耗 | 关闭 entitlement score，回到静态 allowlist |
| 登录开关 | userauth start/callback | 登录 UI、existing flow、SSO | 运维误锁全站或 callback 绕过 | emergency admin session + audit；不依赖同一 OAuth |
| quota/budget | reserve/error mapping/policy | billing hold、并发、client status、DLQ | 越权放量或全站拒绝 | 版本化 policy；保留观测模式但 money 默认 fail-safe |
| credential/health | worker、account health、eligibility SQL | pool 容量、自动恢复、admin alerts | 死账号反复选或好账号永久 revoked | 可审计 operator resume；状态迁移可逆 |
| media lease | task config、claim lease/sweeper | 上游成本、退款、orphan recovery | 任务成功但 claim 已退导致亏损 | 冻结 per-task timeout 或续租 flag |
| mimicry/profile | client gate、composer、transport | 法律/ToS、封号、请求语义 | 默认开启高风险行为、误改官方请求 | official-only + standard transport 一键回退 |

## 8. 跨层验收门（每项含变异）

### 8.1 能力闭合门

- 删除 adapter 注册：released family 启动/测试红。
- env off 但 admin 写 enabled provider：精确 4xx + readiness reason；env on 且 contract complete 才成功。
- acquisition plan 存在、serving contract 缺失：目录显示 collect-only/experimental，不能显示 ready。
- 删 response parser、marshal、scanner、vendor 或 transport 任一站：集合差异测试红。
- 删价格：模型从 sellable 退回 test-only；公开 binding/publish 失败。

### 8.2 Antigravity

- 一个账号、三个 origin 模型、三个不同 wire ID：都选同一账号/family，analytics origin 不同。
- 账号 A 有 Claude/Gemini、账号 B 只有 Gemini：Claude 不能选 B；B 的目录不能删除 A 的 Claude 全局模型。
- internal/unknown 模型即使目录返回也不可发布。
- daily endpoint、host allowlist、project/model/request/credit 精确断言；旧假域名或 OpenAI body 变异后红。
- 流中断、usage 缺失、401 refresh、429 reset、5xx failover；第一 token 后禁止跨账号重发。
- remaining fraction 过期时变 unknown；不改用户余额、不产生虚构 cost。
- composer 失败：上游调用 0，claim/quota/slot 各终结一次。

### 8.3 钱/并发/恢复

- cap=2、并发 3：同时在途精确 2；success/abort 后容量都恢复。
- quota serializable 重试耗尽：不得以 allow 返回；响应不是余额不足假象。
- billing abort 成功、quota release 失败：补偿恰一条；重放幂等；用户误拒窗口有 SLA/指标。
- post-delivery settle 失败：不重发上游、不重复 usage，DLQ 后只结算一次。
- media timeout 从 15m 调到 60m：在途任务 contract 明确采用快照或续租，不能 20m 被 sweeper 退账。

## 9. Owner-gated 决策清单

| Gate | Owner 必须决定 | 未决定时安全默认 |
| --- | --- | --- |
| O1 schema | account-model capability、route candidate family、Claude family CHECK、media/health 是否需字段 | 不迁移；用静态 canary/独立 alias |
| O2 auth core | Antigravity OAuth 固定配置、xAI/Kimi/Vertex/Azure、登录 provider 开关 | 新模式 hidden/fail-closed；现有账号不删除 |
| O3 money | 缺失价格、Antigravity 售价/月费摊销、usage 缺失、credit overage | test-only，不对外售卖，不写虚构 upstream cost |
| O4 quota/default | infra fail posture、budget/quota 统一默认、健康自动恢复 | money/quota fail-safe；typed 5xx；不静默放量 |
| O5 internal endpoint | daily endpoint、真实 Google AI Pro canary、host/profile rollout | per-account flag off，仅 mock/contract test |
| O6 mimicry/合规 | Claude 深拟真、设备/指纹/header profile、transport 默认 | official-only + standard transport；功能保留为 Feature Flag/Mandatory Roadmap |
| O7 runtime dependency | Vertex JWT/OAuth 库等新依赖 | 优先 stdlib/既有依赖；不能安全实现则等待批准 |

## 10. 立即第一刀：精确建议

**第一提交目标：让“能采集但不能 serving / 能配置但当前进程没 adapter / 能启用但无价格”在测试和管理面立即显形。**

执行顺序：

1. 先写 `ServingCapabilityContract` 最小字段和闭合报告，不做全仓重构。
2. 把现有局部对称守卫改成“默认/单 env/全 env”三矩阵，保留 mutation 说明。
3. catalog 同时查 finalizer 与 serving readiness；scaffold 明示但不能 enabled。
4. provider config 校验改读“当前进程 enabled family”，不读理论支持全集。
5. 新增 sellability 预检，只报告/阻断发布，不在此提交修改任何价格。
6. 跑标准 unit gate和 codebudget（未来执行时）；本计划阶段不跑。

预计 6–10 小时。若出现 schema/auth/money 需求，第一提交不越界，只输出 blocker reason；随后 R1A 以 Claude OAuth 把 contract 从 `collectable_not_serving` 推进到 `released`。

## 11. 与 Claude 版最可能需要交叉讨论的争议点

我没有读取 Claude 正文；以下是我认为任何另一版都容易排错的分叉，不预测对方实际选择。

1. **根因优先 vs ROI 优先**：我选“1 天薄闸优先，Antigravity 第二”，不是数周平台优先，也不是 adapter 先行。
2. **Claude vs Antigravity**：我把 Claude official-only S1 作为第一个价值验证；Antigravity wire 工作可并行启动，但不能先公开放量。
3. **一个账号三厂商是否拆三 protocol family**：我坚持一个 Antigravity wire family + 独立 model origin；拆三族会重复容量与健康状态。
4. **动态模型落全局还是账号级**：我坚持账号级 snapshot，再聚合；直接复用当前 vendor-global modelsync 会把一个账号的缺失误当全局下架。
5. **计费归属**：上游成本归 Antigravity 订阅/entitlement，不归模型原创厂商；对客售价另算。把 Claude 名称当 Anthropic API 成本会污染利润分析。
6. **同名模型是否立即跨 native/Antigravity fallback**：我主张 MVP 使用 source-qualified alias；当前单值 protocol family 不支持诚实混路由。若要统一 alias，先做 Owner-gated route candidate 设计。
7. **`remainingFraction` 的语义**：我只把它当 account-model 调度信号，不当 token/quota/money。
8. **共享 composer 范围**：共享 CloudCode wire primitive，不共享 Claude system/tool 拟真；更不建万能跨 vendor mimicry composer。
9. **拟真是否现在做**：我主张协议可用性现在做、身份/指纹拟真最后做且显式 opt-in；当前 transport 的默认开启姿态应单独纠偏。
10. **daily→prod fallback**：我不接受自动 fallback 为多厂商等价，除非每个 origin/model 的真实响应、usage 和 entitlement 都独立验证。

## 12. 最不确定的七点 / Open Questions

1. GPT 型号是否已实际完成非流/流内容请求；当前落盘规格只明确列出 Claude/Gemini 200。
2. Claude/GPT 经 Antigravity 返回的非流、SSE、usage、thinking/tool signature 是否完全同 Gemini CloudCode envelope。
3. `enabledCreditTypes` 是否每请求必须、是否会触发付费 overage、是否存在 free/credit 双层选择。
4. Owner 实测账号当前落在哪个 HUAKAI vendor/auth mode；双轨合并需要数据迁移还是只需代码兼容。
5. dynamic fetch 的模型目录是否逐账号/地区/订阅等级变化，以及 stale TTL 的合理上限。
6. 当前 schema 是否要现在演进到 binding-level family，还是先用 source-qualified alias 获得更快、风险更小的 MVP。
7. risk-control 账号应永久 revoked、定时 probe，还是只允许 operator resume；这会直接影响池容量与封号风险。

## 13. Pre-execution checklist

1. Claude/Codex 两份计划完成 agree/conflict/gaps，对 Owner 输出合成版；执行只认无后缀 synthesized plan。
2. Owner 逐项决定 O1–O7；未决定项采用表中安全默认。
3. R0 先落判别测试，确认它会抓住当前 Claude/Antigravity/env-gated/无价问题。
4. 每个未来切片重新检查三镜 HEAD 与官方/一方 wire；引用超过 30 天先刷新。
5. 每个实现切片先写 runtime-logic 协作图和 failure/compensation 表。
6. schema/money/auth 切片走 full reviewer-lane；普通提交走强制 Codex review，两轮上限、无 S0/S1 才落地。
7. 所有测试使用区分性 fixture、并发、真实错误注入；不以 `t.Skip` 掩盖零值。
8. 所有日志/metrics 不含 token、OAuth code、完整 body、prompt、account external ID；真实 canary 只用 Owner 批准账号。
9. 每个阶段更新 parity/risk/acceptance/runtime-logic 文档，不用“Feature Flag”掩盖未实现。
10. 任何 rollback 都不删账号、凭据、模型历史、pricing history 或 LICENSE。

## 14. 功能缩水、clean-room 与安全结论

- **功能缩水：无。** 未立即发布的 Grok session、统一 alias、深拟真、Ernie 双凭据都进入明确的 `Feature Flag / Hidden Experimental / Mandatory Roadmap / Owner Decision`，没有 Dropped。
- **clean-room：可控。** 三镜只用于行为形态和风险证据；HUAKAI 的 capability contract、账号级 snapshot、身份轴、定价/entitlement 分离均为本地设计。本计划未复制非 MIT 源码、命名、schema 或算法顺序。
- **安全：高风险集中在 auth、money/quota、daily 私有 endpoint、真实订阅账号和 mimicry。** 均已标 Owner gate；默认姿态为 hidden/fail-closed/official-only/test-only。
- **最重要的防复发措施：** 发布状态不再由“某个构件存在”决定，而由跨层闭合证据决定；每个 released capability 必须能从采集一路证明到 usage/settlement/recovery。

## 15. Owner 中文摘要

本计划做了什么：把 workflow 轨 19 条确认缺陷和 2 条待确认项归成 6 个根因簇，并给出从薄闭合闸、Claude S1、当前账号链、Antigravity canary、钱/健康完整性到动态售卖的依赖路线；改了哪些文件：仅新增本计划文档，不改生产代码、不跑构建、不 commit；为什么这样做：先用 1 天内可完成的闭合闸消灭“构件完工幻觉”，再释放 Claude 与 Antigravity 的真实 ROI；功能缩水：没有，所有延后能力都有明确 disposition；clean-room 风险：已用 specifier 车道和三镜逐项引用控制，未复制实现；安全风险：auth、schema、money/quota、真实账号、daily endpoint、mimicry 均为 Owner-gated；需要 Owner 确认：O1–O7；下一步：交叉比较 Claude/Codex 两版，形成无后缀合成计划后，第一刀落 R0 判别闸并马上推进 Claude official-only serving。

Source files read:
- HUAKAI rules/evidence: ../AGENTS.md; ../CLAUDE.md; ../docs/RULES.md; ../docs/01_PROJECT_BRIEF.md; ../docs/03_FEATURE_PARITY_MATRIX.md; ../docs/10_RISK_REGISTER.md; ../docs/12_AGENT_WORKFLOW.md; ../docs/15_RELEASE_GATES.md; docs/process/plans/2026-07-10-antigravity-multivendor-spec.md; docs/process/plans/2026-07-10-claude-oauth-serving-mimicry-codex.md; /tmp/claude-1000/-home-ubuntu/06b5fe50-5803-4f21-9b82-cc02f9ac2c67/tasks/wy4lmo211.output; /tmp/claude-1000/-home-ubuntu/06b5fe50-5803-4f21-9b82-cc02f9ac2c67/tasks/bkrmcrpdq.output
- HUAKAI implementation: internal/adminhttp/catalog.go; internal/adminhttp/provider_catalog_mutation_handler.go; internal/adminhttp/provider_catalog_whitelist_test.go; internal/auth/antigravity_token_provider.go; internal/budgetenforce/enforce.go; internal/codexclientaccess/evaluate.go; internal/codexclientaccess/policy.go; internal/credentialacq/oauth_authorization_code.go; internal/credentialacq/oauth_devicecode.go; internal/credentialacq/types.go; internal/credentialacq/vendor_exchangers.go; internal/credentialstore/postgres_store.go; internal/credentialstore/types.go; internal/credentialworker/adapters/antigravity.go; internal/credentialworker/health_state.go; internal/credentialworker/mode_refresh.go; internal/gateway/protocol_selector.go; internal/gateway/stream_scanner.go; internal/gateway/upstream_dispatcher_hcsf.go; internal/gatewayhttp/chat_completions_dispatch.go; internal/gatewayhttp/chat_completions_pricing.go; internal/mediatask/service.go; internal/mediatask/store_money.go; internal/mediatask/types.go; internal/mediatask/worker.go; internal/modelsync/http_fetcher.go; internal/modelsync/service.go; internal/modelsync/types.go; internal/pool/api.go; internal/pool/vendor_guard_test.go; internal/proto/geminicodeassist/code_assist.go; internal/provider/anthropic/oauth_session.go; internal/provider/antigravity/antigravity_session.go; internal/provider/antigravity/bootstrap.go; internal/provider/antigravity/refresher.go; internal/provider/gemini/code_assist.go; internal/provider/registrydefault/default.go; internal/provider/registrydefault/default_test.go; internal/quota/errors.go; internal/quota/service_settle.go; internal/quotaenforce/settler.go; internal/registry/model_sync_writer.go; internal/sitepublichttp/handler.go; internal/transport/mimicry_switch.go; internal/transport/policy.go; internal/userauth/social_login.go; sql/migrations/0008_model_registry.up.sql; sql/migrations/0131_domestic_model_pricing.up.sql; sql/migrations/0172_models_protocol_family_registered_adapters.up.sql
- CLIProxyAPI: internal/runtime/executor/antigravity_executor.go; sdk/cliproxy/antigravity_models.go; internal/registry/model_registry.go; internal/registry/model_definitions.go
- sub2api: backend/internal/pkg/antigravity/client.go; backend/internal/handler/gateway_handler.go; backend/internal/handler/endpoint.go; backend/internal/service/antigravity_gateway_gemini.go; backend/internal/service/antigravity_gateway_claude.go; backend/internal/service/antigravity_gateway_upstream.go; backend/internal/service/antigravity_quota_fetcher.go
- new-api: constant/channel.go; relay/relay_adaptor.go; model/channel.go; service/channel_select.go; controller/model_sync.go
Lane: specifier
Agent: OpenAI Codex / GPT-5 / root
UTC timestamp: 2026-07-10T09:41:15Z
