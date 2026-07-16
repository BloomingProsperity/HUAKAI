# 2026-07-16 后端全局接线真实性审计（Codex）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “还有呢！这种问题在别的模块，别的链存在吗？全局排查。”；“主要看建立了很多功能但没接入使用，没有完美的配合起来，以及各个链路逻辑是不是有问题”；“记得随时看借鉴项目的逻辑处理”；“后续所有修复以及改动都需要作为 PR 提交。后面并入主线要我同意”。 |
| Scope | 审计 HUAKAI Go 后端全部生产能力链，优先回答三件事：已经建立的功能是否真正进入生产请求/事件/定时任务；各模块是否在统一链路中协同而不是各自存在；入口、状态流转、失败处理和恢复逻辑是否自洽。检查配置来源、composition root 构造、`deps` 保存、路由或 worker 注入、运行时消费、持久化/副本语义、故障恢复、运维观测、OpenAPI 和测试。覆盖公开协议 handler、管理员接口、后台 worker、事件总线、DLQ、Hermes 工具、注册表、provider、路由、配额、计费、鉴权、缓存、健康、审计、通知和凭据链。`rg` 只作定位，所有发现必须打开真实源码核实。前端、数据库结构变更、生产部署和真实密钥不在本轮修改范围。 |
| Success criteria | 建立可追踪的全局能力接线矩阵；每个已审能力至少回答“定义/构造/注入/消费/激活/持久化/副本安全/可观测/测试”九轴；所有问题带生产源码 `file:line`、可达链、辐射模块、严重度和修复建议；低/中风险且不改变资金、鉴权、强配额、schema、运行时依赖的确定问题直接修复并测试；高风险问题只形成 Owner 决策包；不得用文件存在、类型存在、注册名存在或文档状态代替接线证据。所有改动通过独立 Draft PR 提交，未经 Owner 明确同意不得合并主线。 |
| Time estimate | 首轮完整审计约 6-12 小时；按可闭环批次持续交付，不等全部扫描完才报告。预计 4-8 个审计批次，每批包含证据、修复、测试和独立 review。 |
| Blast radius | 审计本身为只读；后续低/中风险修复可能影响诊断合同、依赖装配、后台任务启动、开关读取和非资金业务路径。任何涉及数据库 schema、资金账、鉴权核心、billing ledger、quota enforcement、运行时依赖或生产部署的改动停止在决策包。 |
| Failure modes | 只搜名字不读消费代码导致假阳性；把 nil fallback 当未接线或把 non-nil 当已激活；只看 chat 漏掉其他协议；只看请求路径漏 worker/恢复路径；把单机内存状态误称多副本安全；修复一个入口却漏 alias/Hermes/OpenAPI；测试 fixture 没制造接线差异；把参考源码阅读和实现放进同一会话造成 clean-room 污染；在含其它目标改动的工作树误混 PR。缓解方式是九轴矩阵、逐链引用、协议横向表、差异 fixture、参考 specifier 隔离车道、独立工作树和分批 PR。 |
| Decision points | 发现需要改变数据库 schema、资金/退款/余额、鉴权/RBAC、billing ledger、强配额、真实凭据、runtime dependency 或部署形态时，停止该项并向 Owner 提交当前 HUAKAI 真码链、隔离 specifier 提供的参考项目行为证据、可选方案、优缺点、迁移/测试/回滚和明确推荐。其余可测试、可回滚的低/中风险接线修复直接执行。 |
| Parallel-plan status | Owner 已要求 Codex 独立工作且不要触碰另一目标。本计划不读取、不修改 Claude 工作树或同题计划，作为 Owner 指定的独立 Codex 车道。参考项目由另一个隔离 Codex specifier 会话读取并产生行为报告；当前审计/实现会话不直接读取参考源码。 |

## 审计判定模型

每项能力使用以下九个状态轴，不再用单一“已实现”结论：

1. **Defined**：类型、接口、配置、handler 或 worker 已定义。
2. **Constructed**：生产 composition root 实际构造了实例。
3. **Retained**：实例或启动配置被保存，运行时和诊断能读取真实快照。
4. **Injected**：实例进入对应 handler、selector、worker、registry 或事件消费者。
5. **Consumed**：真实请求/事件/定时任务代码会调用该依赖，而非仅保存字段。
6. **Active**：默认值、feature flag、运行模式和前置依赖允许该路径真正执行。
7. **Durable**：需要跨请求/重启保存的状态确实持久化；本地内存状态必须明确标记。
8. **Observable**：管理员、Hermes、健康探针、日志或指标能准确区分未接、降级和激活。
9. **Verified**：测试能区分“存在但未接”和“真实生效”，并覆盖失败及恢复路径。

## Owner 指定的首要排查方向

本轮不是普通 dead-code 检查，也不以“模块注册表字段齐全”为完成标准。优先级如下：

1. **建立了但生产不使用**
   - 类型、service、handler、worker、配置、表访问器或恢复器已经存在，但生产 composition root 从未构造。
   - 已构造并放进 `deps`，但没有进入任何生产 handler、worker、selector、event consumer 或工具。
   - 已注入字段，但真实执行代码从不读取，或仅测试使用。
   - 路由 handler 已完成但生产 router 未 mount，或只挂 alias/内部入口而主入口缺失。
   - 配置能保存、API 能修改，但运行时没有消费者，形成“管理面看起来能配，实际不生效”。

2. **功能彼此没有配合起来**
   - 路由、健康、冷却、重试、fallback、限流、quota、claim、billing、audit、DLQ 各自存在，但没有共享同一身份、状态或结果。
   - 上游模块产生的信息在下游丢失，例如选择结果未进入计费、错误分类未进入健康、结算失败未进入恢复、恢复成功未回写状态。
   - 同一功能在 chat 生效，其他协议走旁路；或 admin/Hermes/worker 使用另一套不一致逻辑。
   - 多个模块重复承担同一职责，却没有统一优先级、幂等键、状态机或冲突规则。
   - 功能顺序错误，例如先产生不可逆副作用再过 gate，先响应客户端再丢失必要恢复证据。

3. **整条链路逻辑有问题**
   - 正常路径、上游失败、超时、客户端断开、重试、fallback、部分流式交付、结算失败、重放恢复分别追踪。
   - 核对 tenant/user/key/account/model/claim/request/audit 等身份是否从入口贯穿到最终持久化和恢复。
   - 核对状态机是否存在跳转缺口、重复执行、永久悬挂、错误终局、假成功或恢复后二次扣费。
   - 核对本地内存、PostgreSQL 和缓存状态的权威关系，以及重启、多副本和并发下是否仍成立。
   - 核对测试是否真正驱动整条生产链，而不是各模块单测都绿、组合起来却没有工作。

运维 activation、健康探针和 OpenAPI 是验证真实接线的证据面，不是本轮主目标。若真实执行链正确但诊断错误，修诊断；若诊断揭示真实链未接，则优先修真实链。

## 参考项目隔离车道

- 每个批次先由独立 specifier 会话读取 Sub2API、CLIProxyAPI、New API 当前源码；按领域补充其它必要参考。
- specifier 只输出行为链、路径/模式/状态、失败协作和逐项源码引用，不输出或复制实现代码、函数名、字段名、注释和独特目录结构。
- 当前审计/实现会话只读取行为报告和 HUAKAI 真码，不直接访问参考源码。
- 参考行为用于发现遗漏、比较协作逻辑和形成 Owner 决策包，不能作为逐行翻译来源。
- 每份参考报告必须带 clean-room lane guard、源码版本 SHA、实际阅读区域和 open questions。

## 分批执行顺序

### Batch 1：composition root 与生产入口总账

- 逐字段核对 `cmd/gateway` 的 `deps`、runtime options、构造赋值和消费者。
- 核对所有路由 mount：生产 handler 是否挂载、是否经过正确 gate、alias 是否一致。
- 核对所有后台 worker：是否构造、注册 handler、`Start`、生命周期停止、失败告警及恢复入口。
- 反向核对主要 `internal/*` service、handler、worker 和 store 的生产 callsite，找出只在测试、工具或文档中出现的完整功能。
- 输出第一版“定义但未构造 / 已构造但未注入 / 已注入但未消费 / 可配置但不生效 / 只在测试生效”清单。

### Batch 2：六类公开协议横向一致性

- chat、completions、embeddings、rerank、images、audio。
- 逐项比较 registry、router、selector、quota、claim、pricing、billing、credential、dispatcher、fallback、健康、缓存、结算恢复、审计和限流。
- 单独核 Gemini、Responses、媒体任务等旁路是否绕开统一链。
- 对每条协议画真实调用顺序，检查模块不是简单“都有”，而是以正确次序传递同一请求身份、选号结果、计费 claim、健康反馈和审计引用。

### Batch 3：配置、开关与运行时管理

- 环境变量、启动配置、platform settings、租户设置、账号设置。
- 查“配置能写但运行时不读”“只启动读取但管理面声称实时生效”“错误 fallback 静默翻转”“默认值与诊断不一致”。
- 记录配置来源、缓存刷新、跨副本传播和实际消费者。

### Batch 4：事件、DLQ、恢复和后台闭环

- event bus 注册、审计引用、结算恢复、通知 outbox、凭据刷新、模型同步、健康探测、账务对账、租约清理。
- 查 producer 有而 consumer 未注册、注册未启动、失败只记录不入 DLQ、DLQ 有事件无 replay handler、恢复成功不清状态。
- 查事件 payload 是否携带下游完成动作所需的全部身份和幂等证据，避免“事件发了但消费者无法正确闭环”。

### Batch 5：管理面、Hermes 与真实能力观测

- 模块注册表、system health、admin observability、Hermes context/tools、OpenAPI。
- 查运维接口把“定义/构造/non-nil”误报为 active/verified/shared-safe。
- 核所有 alias 和消费者是否使用同一合同。

### Batch 6：持久化、多副本与局部内存状态

- 找需要全局一致但使用进程内 map/cache/once/ticker 的能力。
- 核 PostgreSQL/Redis/内存混合状态是否在诊断、恢复和多副本语义中准确表达。
- 不直接修改 schema、资金、鉴权和强配额；高风险项形成决策包。

## 问题分类

- **W-01 建而未用**：生产构造或代码完整存在，但没有任何可达消费者。
- **W-02 半接线**：只接入口或出口的一半，失败/恢复/alias/协议之一缺失。
- **W-03 假激活**：non-nil、配置存在或注册成功被误报为运行时 active。
- **W-04 配置断路**：配置可写/可读，但真实执行链不消费或只在启动时读取。
- **W-05 协议漂移**：chat 已接，其他公开协议未接且无明确产品理由。
- **W-06 恢复断路**：错误被捕获或入队，但没有可靠重放、幂等、状态清理或告警。
- **W-07 副本错觉**：本地内存状态被当作平台全局状态。
- **W-08 观测失真**：OpenAPI、模块状态、健康探针、Hermes 或日志与真实接线不一致。
- **W-09 测试假覆盖**：测试只断言非 nil/非错误，无法证明能力真实执行。
- **W-10 信息断链**：前一模块生成了关键状态、身份、错误分类或结果，但后续模块没有接收或使用。
- **W-11 顺序错误**：模块都已接线，但 gate、副作用、响应、审计、计费或恢复的执行顺序会造成绕过、漏账或假成功。
- **W-12 重复体系**：同一职责存在两套以上生产逻辑，入口选择不清或状态不共享，导致协议、worker 或管理面行为漂移。

## 证据和记录格式

每条发现必须包含：

1. 编号、严重度和分类。
2. 用户或运营影响。
3. 定义、构造、注入、消费、失败/恢复五段源码证据；不适用项明确说明。
4. 可达条件和反证检查，区分真实问题与设计取舍。
5. 辐射协议、模块、worker、OpenAPI、Hermes 和多副本影响。
6. 修复方案、测试方案、回滚方式。
7. 状态：`Confirmed`、`Fixed`、`Owner Decision Required` 或 `Not a Bug`。

## Pre-execution checklist

1. 只使用 `HUAKAI-wt-global-wiring-codex` 和分支 `audit/backend-global-wiring-20260716-codex`。
2. 每批开始和结束检查 `git status`，所有改动通过该批次的 Draft PR 提交；未经 Owner 同意不合并。
3. 当前会话不读取参考项目源码，不触碰 Claude 或其它目标工作树。
4. 参考源码由隔离 specifier 会话读取，输出行为报告后退出。
5. 建立生产入口、worker 和协议 handler 的源码清单。
6. 每个候选发现至少打开构造、注入和消费源码，不以 `rg` 输出直接下结论。
7. 每批先写证据再改代码；高风险项停止在决策包。
8. 每个修复运行目标测试、相关链测试、代码预算和必要的全仓测试。
9. 每批暂存预期差异并执行只读 Codex review；S0/S1 修复后最多再跑一轮。

## 第一批交付物

- `docs/process/research/2026-07-16-backend-global-wiring-audit-codex.md`
- production route / worker / registry / setting 四张接线矩阵。
- 第一批确认问题及低/中风险修复。
- 目标测试、全仓回归结果和独立 review 结论。
- 独立 Draft PR；Owner 明确同意前不合并。
