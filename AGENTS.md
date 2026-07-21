本文件面向执行 agent，并且是项目规则的最高权威入口。

# HUAKAI Agent 执行规则

HUAKAI 是 MIT clean-room 的 AI Gateway + Account Hub + Admin Ops Platform。目标是能够真实上线、稳定运行，并在有效能力、联动精度和运维体验上达到成熟项目同等或更好。

## 0. 权威顺序与必读入口

发生冲突时按以下顺序执行：

1. Owner 最新明确指令；
2. 当前分支真实源码、迁移、配置和可判别测试；
3. 本文件；
4. `docs/RULES.md` 规则清单；
5. 当前唯一执行计划；
6. 历史报告、旧计划、注释和记忆。

历史文档只能作为线索，不能证明功能已实现或未实现。被当前源码或 Owner 新指令推翻的规则、文档和注释应删除或合并到最新合同，不保留多份互相冲突的“真相”。

规则整理只允许三种动作：调整执行顺序、合并完全重复内容、删除被 Owner 最新明确指令覆盖的旧条款。每一条仍有效的细化要求必须能映射到新章节或对应 Skill；不得为了缩短文件而丢失测试 smell、clean-room、引用、风险、运维或验收颗粒度。

开始工作前至少读取：

1. `docs/RULES.md`；
2. 当前唯一执行计划；
3. 与任务匹配的 `.agents/skills/<name>/SKILL.md`；
4. 涉及的真实源码和测试。

## 1. 不可违反的底线

- **真实第一。** 不造假、不猜实现、不以文档、搜索命中、测试文件或记忆代替生产源码。负面结论必须证明入口、DI、运行时调用和状态回流均不存在。
- **功能不缩水。** 有效能力必须归入 `Implemented`、`Implemented Better`、`Merged Equivalent`、`Safe Equivalent`、`Plugin`、`Feature Flag` 或 `Mandatory Roadmap`；禁止静默删除、`Dropped` 或用 clean-room/安全风险作缩水理由。
- **能力并集、实现唯一。** 重复实现或互相冲突的逻辑必须先从完整业务链核实正确合同，把各实现的有效独有能力合入唯一生产入口，并覆盖正常、失败、恢复和运维路径；验证无功能缩水后，删除其余实现及其旧配置、旧测试和误导注释。禁止用去重或死代码清理的名义缩减功能。
- **独立实现。** 外部项目是行为证据，不是代码提供者。禁止复制或近似翻译源码、函数/字段名、注释、schema、文件结构、UI 源码、测试和独特实现顺序。
- **语言全中文。** 面向 Owner 的汇报、计划、`docs/process` 文档、代码注释、测试注释、commit message 正文、agent 指令与返回报告一律中文；技术标识符保留英文。
- **日志统一合同。** 项目产品层统称“日志系统”，不再把“审计系统”作为并列产品或独立运营面。该合同对所有业务域、协议、后台任务和运营入口全局生效：日志按操作、资金、安全、错误、访问、恢复等类别细分，每条都要有明确事件类型、结果、错误码和严重级别。普通日志统一滚动保留 30 天并自动分批清理，不允许模块自行改成永久保留或更短周期；`billing_events`、充值、退款、争议资金效果、账本和幂等事实等业务真相不属于可清理日志，必须永久保留。既有 `audit` 包名、表名、API 字段等技术标识符可保留，避免无收益的破坏性改名，但新增散文、界面、计划与代码注释统一使用“日志”或具体日志类别。
- 存量英文注释在触及相关包时逐步转中文，但不修改生成码、vendor/`pkg/external`、`//go:`/`//nolint`/build tag、LICENSE/SPDX/版权头；翻译只改散文，不动逻辑和标识符。
- **代码注释不提借鉴项目。** `.go`、Rust、TS 代码注释只解释 HUAKAI 自身机制，不出现 sub2api、new-api、CLIProxyAPI 等项目名或“参考某项目”。
- **秘密不泄漏。** 真实密钥、token、cookie、凭据、私钥、原始请求体不得进入日志系统、响应、提交或规则文档。
- 未经 Owner 明确要求不得修改 `LICENSE`。

## 2. Owner Start Gate 与当前所有权

`docs/RULES.md` §2 的 S-001/S-002 是启动门唯一清单。Owner 发出“开始、继续、去做、开干、修复、同意、批准”等明确执行信号后，当前被指派 agent 应独立推进到可验证闭环。

当前全局执行约束：

- 不再强制 Claude/Codex 并行双计划；旧的 `*-claude.md` / `*-codex.md` 平行制度退役。
- 不按固定模型划分“Claude 设计、Codex 小修、Gemini 前端”。谁被 Owner 指派，谁对该工作单元的调研、设计、实现、测试和收口负责。
- 同一目标只保留一个最新执行计划、一个干净分支和一个 PR；不得为同一问题继续开分支或堆重复计划。
- 未经 Owner 同意不得合并主线；不得触碰另一个目标或未授权 worktree。
- 大型临时文件、构建缓存和抓取产物不得放 `/tmp`，使用当前磁盘上的项目缓存目录。

## 3. 决策与风险门

Owner 启动后默认继续执行，不因复杂、跨模块或需要多读源码而自行缩小边界。

### 3.1 必须停下询问

以下操作始终需要 Owner 明确批准：

- 合并主线、生产部署或操作真实生产数据；
- 读取、写入或轮换真实秘密/凭据；
- 修改 `LICENSE`；
- 不可逆删除、破坏性迁移、销毁分支/记录；
- 超出当前目标的真实资金转移。

除上述硬边界外，只有一个决策**同时满足**以下三项时才停下：

1. 存在实质性方案分歧；
2. 官方规范和成熟领域项目均无可用依据；
3. 选错会造成高危资金、鉴权、数据、合规或不可逆影响。

需要 Owner 决策时必须同时给出：HUAKAI 真码现状、领域成熟项目源码做法、各方案优缺点、影响半径、实施与回退/恢复计划。能够从源码、官方规范或成熟项目证据消歧的，不把问题甩回 Owner。

### 3.2 可以直接推进

- 已在当前目标中明确授权的钱路、鉴权、schema、配额或部署代码改动，可在有行为证据、计划、判别测试和审查门时直接做；
- 低风险文档、测试、类型、结构整理直接做；
- 中风险实现按计划推进并记录风险；
- 发现同根问题辐射到兄弟模块时一并修复，不以原任务文字作死边界。

## 4. 强制执行顺序

非平凡工作必须按以下顺序，不得事后补调研装作前置：

1. 判断任务领域与用户/运营结果；
2. 选择领域成熟项目并核实许可证、维护活跃度和真实源码；
3. clean-room `specifier` 只产出行为合同；
4. 再读取 HUAKAI 真码，追完整运行链并做差距分析；
5. 更新当前唯一计划，列 shape inventory、影响半径、失败模式和验收；
6. 独立设计并实现；
7. 跑判别性单元、集成、并发、故障与恢复测试；
8. stage 精确 diff，执行强制 review；
9. 小提交、推送到唯一 PR，等待 Owner 批准合并；
10. 清理被替代规则、死代码、错误注释和重复文档。

顺序做反时：立即停止继续修改业务代码，补齐缺失的前置行为合同，再倒查并修正当前补丁；未完成不得落地。

## 5. 参考项目选择：中转站基线 + 领域头部项目

### 5.1 先分领域，禁止万能镜像

- **中转站核心**：账号池、凭据、协议转换、选号、路由、重试/failover、上游健康、模型目录、gateway 观测，默认三镜为 sub2api、CLIProxyAPI、new-api。
- **专业领域**：支付、退款、订单、订阅、账本、身份、风控、日志系统、可观测、前端运维等，不得只看三镜。必须全网选择该领域维护活跃、真实落地、源码完整的头部项目。
- **跨领域链路**：既看领域项目的专业行为，也看三镜如何与中转站入口/账号/用量/运营面接线；两边缺一不可。

钱路按问题颗粒度从以下类别选证据，不把候选名称写成永久定论：

- 发卡/数字商品：订单、支付回调、库存/卡密交付、下级租户和后台运营；
- 电商：取消、退货、部分/累计退款、订单状态与人工处理；
- 支付编排：渠道请求、稳定幂等、异步同步、退款查询和对账；
- 订阅/用量计费：预付、后付、credit note、账期和额度；
- 财务账本：双分录、冻结/提交/撤销、冲正、对账和资金日志。

候选可来自独角数卡、Hyperswitch、Medusa、Spree、Saleor、Kill Bill、OpenMeter、Formance、Blnk、Midaz 等，但每次必须重新核实当前 HEAD、许可证、活跃度和与具体问题的匹配度；名称本身不构成证据。

外部行为合同只是设计输入之一，不是 HUAKAI 的自动实现指令。HUAKAI 本身是中转站，发卡、电商、支付编排、账本或身份项目的对象模型与调用边界未必互通；实现 lane 必须结合 HUAKAI 当前源码、不变量、租户模型、账务事实和运维方式，对每项外部能力明确判定 `直接适配 / 融合改造 / Safe Equivalent / 不适用并说明原因`。不得为了“对标”强塞不兼容结构，也不得用“不兼容”跳过其中对 HUAKAI 有效的业务结果。

### 5.2 源码必读

以下断言一律必须读生产源码：

| 断言类型 | 必须证明 |
| --- | --- |
| 能力 | 项目是否真的有该入口、状态和运行时接线 |
| 机制 | 状态机、算法、幂等、重试、恢复如何工作 |
| 差异 | HUAKAI 与外部项目具体差在哪一维 |
| 缺失 | 搜索范围、注册/DI、调用者和状态回流均已核实 |
| 对比表 | 每个非平凡单元格都有独立源码证据 |

README、宣传页、前端按钮、测试文件、issue 和文档只能提供线索，不能单独证明生产能力。技术标准和供应商 API 以官方规范为第一来源。

每次调研任何借鉴项目都必须同时核实默认分支近期提交、最新 Release/Changelog 和仍开放或近期关闭的公开 Issue，并把结果并入同一份行为合同：生产源码用于证明当前能力与机制，提交和发布记录用于定位变更范围，Issue 用于提取真实故障与恢复场景。后两者未经当前源码或独立复现交叉验证，不得写成“当前已实现”“缺陷已修复”或“固定提交仍有该缺陷”。发现借鉴项目新增有效能力或已公开缺陷时，必须沿 HUAKAI 完整链路核实是否存在同类差距，并进入实现、`Implemented Better` 或明确的强制路线，不能只登记线索。

账号转 API、协议和网关类借鉴项目还必须核实两条独立能力轴：模型原生 function/tool calling 的请求、流式事件、终止原因、用量和失败恢复是否闭环；项目是否提供 MCP client/server、工具注册与发现、权限隔离、租户边界、调用日志和人工恢复。未发现源码证据时标为 `Open Question`，不得用搜索无命中直接断言“不支持”；外部项目缺失也不能让 HUAKAI 静默删掉有效能力，必须结合自身定位判定直接实现、`Safe Equivalent`、`Feature Flag` 或 `Mandatory Roadmap`。

引用格式：`<owner>/<repo>@<commit-sha>:<file>:<line-range>`。首次使用项目时还要记录：未归档、默认分支 HEAD、最近提交时间、许可证；旧于 30 天的引用先更新镜像。每段提到外部项目行为的文字都要有紧邻引用。

行为断言只允许三类：`Observed`（已读源码直接看到）、`Inferred`（由已观察区域推导并明确标注）、`Open Question`（证据不足）。`Speculative` 不得写成事实。分解产物必须列 `Observed regions / Inferences / Open questions` 和 Source Coverage Proof；字数不是目标，禁止用无证据内容凑篇幅。

HUAKAI 内部事实直接引用本地源码/测试；公开协议引用官方规范；已经带完整源码证据且未过期的当前计划可以复用，不要求为同一事实重复读取外部源码。

### 5.3 Clean-room 前置门

任何读取外部项目源码的任务都必须使用下面的 guard；字段必须真实填写：

下面代码块是跨工具协议字面量，必须原样放进派发 prompt；它是“全中文”规则中唯一保留英文的兼容模板，周边说明和实际报告仍须中文：

```text
=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: <specifier | reviewer>
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: <list (agent + lane + UTC) | "none">

REFERENCE PROJECTS IN SCOPE: <MUST include ALL THREE default mirrors
  CLIProxyAPI + sub2api + new-api, then domain extras e.g. LiteLLM /
  portkey. Omitting any default mirror makes this dispatch invalid
  — see §"sub2api + CLIProxyAPI + new-api Default Triple-Mirror".>

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
```

同一行为合同的 `specifier` 与 clean-room `reviewer` 必须是不同 session。实现 lane 只能在行为合同完成后读取 HUAKAI 真码；不得让刚读过受限源码的同一上下文做贴近源码的实现翻译。

`reviewer` 只验证行为合同的完整性、证据覆盖和 clean-room 风险，不重新读取同一外部源码。引用锚点可以保留上游路径与行号，但周围散文不得复述独特标识符。

许可证风险改变实现方式，不删除功能。AGPL/GPL/LGPL 只允许行为证据；MIT/Apache/BSD 也默认独立实现。只有官方 SDK 或明确批准的隔离 vendoring 才允许复用，并必须经过 dependency/license audit、保留 LICENSE/NOTICE 和来源 SHA。

## 6. 行为合同与能力闭环

行为合同至少覆盖：

- `path / mode / state / actor` 完整清单；
- 身份与权限来源；
- 输入规范化与核心决策；
- 持久化事实与唯一/并发约束；
- 外部副作用与稳定幂等标识；
- 部分成功、超时、崩溃和重放；
- retry/fallback/compensation/DLQ/reconciliation；
- 日志、可观测与人工恢复；
- 默认值、开关、成本预算和 Day-2 运维入口。

参考项目未实现或实现不完整也必须如实记录。HUAKAI 不以“对标项目也没有”为理由保留明显资金、鉴权、幂等或运维缺陷；成熟项目的缺点进入 `Implemented Better` 的升级差量。

差量必须落到以下至少一维：

- **架构升级**：边界、数据流、存储事实、合同面；
- **算法升级**：选号、评分、重试、降级、恢复策略；
- **生态升级**：运维、可观测、日志系统、前端与生命周期。

## 7. 全链路闭环与模块联动

当前报错只是调查入口，不是修复边界。必须亲读 HUAKAI 真码，沿下列完整链追踪：

`入口 -> 身份/权限 -> 规范化 -> 核心决策 -> 持久化 -> 外部副作用 -> 异步任务 -> retry/fallback -> health -> billing/quota -> log（内部技术标识可为 audit） -> DLQ/recovery -> 用户/管理员状态`

同时横查：

- 同构协议和兄弟模块是否存在同根旁路；
- 上下游消费者是否仍使用旧合同；
- DI、registry、route、worker、scheduler、readiness 是否真实接线；
- 并发/幂等、租户隔离、金额/额度/凭据副作用；
- 进程崩溃、部分成功、网络超时、重复回调和多副本竞争；
- 运营是否能看懂、查询、重试、对账、隔离和人工解决。

调查必须由点到面：从一个报错或字段出发，先验证本点，再向调用者、被调用者、同构模块、持久化事实、异步恢复和运营界面扩散；既看系统级闭环，也逐项核对细小状态、默认值、错误分类、条件分支、幂等摘要、锁和 `WHERE` 约束。宏观链路不能掩盖细节缺陷，局部修复也不能替代全局链路判断。

代码存在但无入口/DI/worker/状态回流，视为未实现。错误消失但钱、hold、quota、槽位、账号健康或日志状态未收敛，视为未闭环。

## 8. 计划与决策纪律

### 8.1 一个目标只保留一个计划

非平凡工作必须更新当前唯一计划，而不是创建 Claude/Codex 双计划或多份补充报告。计划最少包含：

```text
Owner 指令
范围 / 不在范围
行为合同 / shape 清单
参考项目与源码证据
成功标准
执行顺序
时间估算
爆炸半径
失败模式与缓解
决策点
判别测试
运维恢复
执行前检查清单
```

旧计划被新计划覆盖时删除或合并，只保留最新权威版本。单字符修复、单个既有测试和纯只读核实可不改计划。

计划必须在实施前向 Owner 简要展示范围、顺序、风险和成功标准；Owner 已授权当前目标时，展示不等于重新等待批准，除非命中 §3.1 决策停门。

### 8.2 决策材料

需要 Owner 拍板的选项必须包含：

- HUAKAI 当前源码事实；
- 官方规范；
- 至少一个真正匹配该领域的成熟项目源码做法；
- 中转站相关决策再补三镜；
- 每个选项的收益、代价、迁移、故障和运维影响；
- 推荐项及理由。

不得只写 HUAKAI 内部 A/B 而让 Owner 再问“成熟项目怎么做”。

## 9. 实现纪律

### 9.1 责任边界

- 一个 package/module 对应一个内聚职责；新功能域优先建职责清晰的子包。
- 一个源文件对应一个内聚职责；非测试源文件超过约 600 行必须拆分或给出下降计划。
- 一个 Go 包目录默认不超过 6000 非测试行或 20 个非测试文件；存量超标项只允许在 `backend/internal/codebudget/baseline.json` 的既有 +5% 余量内修复。
- 禁止为了过门抬高 baseline；只有有意拆分使体量下降后才能重生成。
- 每次触及结构都运行 `backend/internal/codebudget`。

### 9.2 变更原则

- 遵循当前真码和本地合同，使用结构化 API/解析器，不用脆弱字符串拼接。
- 未上线阶段不为假想回滚长期保留双栈、死代码、旧开关或静默 fallback；Git 历史就是恢复手段。
- 删除前先证明替代链路、测试和运维入口齐全。
- 不修改与当前目标无关的用户改动；遇到并发变化先核实归属。
- 前端若明确要求重构，旧前端只能用于核 API 接口，不能作为设计真相。

## 10. 测试质量与验收

每个测试必须回答“它会在什么具体缺陷出现时变红”。

### 10.1 判别性要求

1. 使用会让正确实现与破坏实现产生不同结果的 fixture；
2. 心智或实际 mutation：删守卫、翻条件、忽略输入、绕接线后测试必须红；
3. 断言明确好结果，不只断言“不等于坏结果”；
4. 不用 `nil` stub、`t.Skip` 或宽松 mock 掩盖真实风险；
5. 测生产 SQL 的真实 `WHERE`、锁、唯一约束和事务边界；
6. 测入口到最终状态，不把单模块全绿当全链闭环。

可行时让测试自证判别性：同一测试同时跑正确路径和故意缺少关键输入/守卫的基线路径，并断言两者结果不同。实际修改生产码做 mutation 前必须先保存当前补丁且保证可无损恢复，禁止用破坏性 checkout/reset 丢未提交工作。

### 10.2 风险分层

- 普通逻辑：targeted unit + package tests；
- 跨模块：真实 handler/DI/worker 集成；
- 并发/幂等：race、跨节点/多事务重放、冲突和唯一约束；
- 钱/鉴权/schema：PostgreSQL 集成、失败注入、部分成功、崩溃恢复与审计；
- 网络/出口：proxy、超时、取消、流式中断、故障分类和资源回收；
- 发布：全量 test、vet/lint、构建、容器/readiness smoke。

真实上游成本只允许通过最便宜模型、小 `max_tokens` 和受控次数降低，不得删测试场景。无法取得真实账号/DB/容器时如实标注“未验证”，不得宣称上线门通过。

当前后端全量门以仓库脚本和 CI 为准：普通单元/race 门设置 `HUAKAI_SKIP_PERF_LATENCY_GATE=1`，性能门单独运行；`integration_pg` 使用 `backend/scripts/integration-pg.sh` 为每个包克隆纯净迁移库并串行执行，禁止让多包共享同一数据库产生假阳或因缺少 DSN 静默假绿。

## 11. Review 与提交门

### 11.1 每个提交

1. 只 stage 当前提交的预期 diff；
2. 本地必需检查通过；
3. 运行 `codex exec review --uncommitted --full-auto --sandbox read-only`；若 CLI 参数变化，先查 `codex exec review --help`；
4. 将 finding 归一为 S0/S1/S2/S3；
5. 未解决 S0/S1 阻止提交；S2/S3 记录到 commit body 或当前唯一计划，不新建零散文档；
6. Round 1 出 S0/S1 或修复实质改变行为/安全/schema/测试语义时跑 Round 2；同一提交默认最多两轮，仍有 S0/S1 则继续修而不是降级；
7. commit message 正文记录测试与 review 结论。

若当前执行者就是 Codex，review 必须使用独立只读 session，不能把自审当独立审查。

严重度统一如下：

| 级别 | 含义 | 典型问题 | 阻提交 |
| --- | --- | --- | --- |
| `S0` | 灾难性、法律或生产不可接受 | secret 泄漏、跨租户、auth/billing/quota/data-loss、clean-room/许可证污染、破坏性迁移、发布必需门失败 | 是 |
| `S1` | 会破坏当前切片正确性、信任或硬规则 | 功能缩水、钱/安全回归、非判别测试、codebudget/结构违规、schema 风险、未处理 reviewer 高危、严重度不确定 | 是 |
| `S2` | 真实缺陷但不否定当前闭环 | 非发布文档同步、来源尾部清理、次要合同精度、已有行为受保护后的工具清理 | 否，必须排期 |
| `S3` | 样式、一致性或可选优化 | 措辞、格式、冗余说明、低风险局部整理 | 否，按价值记录 |

不得为逃避轮次上限降低真实严重度。review 若开始逐轮发现与当前提交无关的新需求，应先关闭无 S0/S1 的当前小切片，把完整后续要求并入唯一计划；不得把无关合规润色、出处清理或样式改动无限塞进同一提交。即使只改文档，也不能跳过提交 review。

### 11.2 完整 reviewer lane

以下提交还必须运行 `docs/templates/codex-reviewer.md` 的完整只读 reviewer：

- money/billing/quota 表写入；
- schema 迁移；
- authentication/authorization core；
- 跨功能集成；
- 声明一个完整 slice 已完成或准备发布。

reviewer 必须逐项覆盖 acceptance ID，引用 spec 与测试 `file:line`，检查弱断言、假 fixture、缺失失败门和运维恢复。REJECT 时不得宣称完成。

完整 reviewer 的 coverage matrix 必须让每个 `AT-*` 进入 `COVERED / COVERED-WEAK / SKIPPED（说明合法性）/ MISSING` 之一。以下 smell 即使测试绿也必须报告：

- 只断言 `!= bad`，从不断言 `== good`；
- 字段为零就 `t.Skip`，把覆盖洞伪装成防御；
- 注释称 100 并发而代码只跑很小的 N；
- winner/loser fixture 共享本应区分它们的特征；
- stub 没有复现生产 SQL 的 `WHERE`、锁或事务条件；
- 所有 gate 都用 `AllowAll`，从未触发 gate failure；
- 只测函数返回，不验证余额、hold、quota、审计、DLQ 或 operator 状态。

完整 reviewer 的 HIGH 阻当前 slice，MED 必须在下一 slice 前处理，LOW 才可进入 backlog；映射到 S0/S1/S2/S3 时仍以本文件的资金、安全、功能和测试质量定义为准。

## 12. Skill 调用顺序

`.agents/skills/` 是唯一 canonical；`.claude/skills/` 只是机械镜像，不直接编辑。按任务阶段调用：

| 阶段 | Skill | 作用 |
| --- | --- | --- |
| 全程编排 | `pm-orchestrator` | 维护当前唯一计划、能力处置和发布状态 |
| 候选筛选 | `dependency-license-auditor` | 先验许可证、依赖与可复用边界 |
| 证据前置 | `reference-project-miner` | 选中转站/领域头部项目并读源码形成行为合同 |
| 场景补强 | `issue-scenario-extractor` | 把真实 issue 转为失败与恢复场景 |
| Clean-room 门 | `clean-room-license-guard` | 检查行为合同和补丁没有实现污染 |
| 能力合并 | `feature-parity-auditor` -> `feature-merger` | 防漏功能并证明合并等价 |
| 设计风控 | `api-gateway-risk-review` + `production-scenario-review` | 查完整链、失败、滥用、恢复和运维 |
| 前端运维 | `frontend-ops-ui-review` | 仅在设计/审查运营 UI 时触发 |
| 验收设计 | `acceptance-test-writer` | 把合同与场景写成正常/失败/恢复测试 |
| 发布收口 | `release-readiness-gate` | 判定是否可发布，不以文档状态代替测试事实 |

Skill 不得复制本文件的大段规则；只保留本领域触发条件、输入、步骤、输出和阻断项。

权威规划、合同、风险和 release gate 放 `docs/`；复杂可复用流程放 `.agents/skills/`；不要把业务实现写进规则或 Skill。

## 13. 工作区、PR 与协调

- 默认沿用当前工作树与分支；未经 Owner 要求不新建 worktree/branch。
- 当前目标只开一个 PR；所有后续提交继续推到该 PR。
- 不自动合并主线，不替 Owner 做最终 merge 决策。
- 提交前检查 dirty tree，明确哪些是当前改动、哪些是用户或其他目标改动；不回滚他人内容。
- Owner 当前关闭并行双计划。若未来明确恢复多 agent 并行，才启用 `.coordination/check.sh`、`claim.sh`、`release.sh`；未恢复时不为单 agent 制造协调文档噪音。

## 14. 完成定义与 Owner 汇报

“完成”必须同时满足：

- 真实入口、DI、核心逻辑、存储、worker、状态回流和运维入口已接通；
- 正常、失败、并发、幂等、崩溃/恢复有判别性证据；
- 关联模块没有遗留同根旁路；
- clean-room 和依赖许可证门通过；
- required build/test/review 通过；
- 当前计划、规则和代码注释只保留最新合同；
- PR 已推送但未擅自合并。

最终中文汇报固定说明：

1. 做了什么；
2. 改了哪些文件；
3. 为什么这样做；
4. 全链路和关联模块如何收敛；
5. 测了什么、哪些未能实测；
6. 有没有功能缩水；
7. clean-room、许可证和安全风险；
8. 仍需 Owner 决策或批准的事项；
9. 下一步建议。
