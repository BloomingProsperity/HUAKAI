# 2026-07-16 后端全局接线真实性审计（Codex）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “还有呢！这种问题在别的模块，别的链存在吗？全局排查。”；“主要看建立了很多功能但没接入使用，没有完美的配合起来，以及各个链路逻辑是不是有问题”；“记得随时看借鉴项目的逻辑处理”；“后续所有修复以及改动都需要作为 PR 提交。后面并入主线要我同意”。 |
| Scope | 审计 HUAKAI Go 后端全部生产能力链，优先回答三件事：已经建立的功能是否真正进入生产请求/事件/定时任务；各模块是否在统一链路中协同而不是各自存在；入口、状态流转、失败处理和恢复逻辑是否自洽。检查配置来源、composition root 构造、`deps` 保存、路由或 worker 注入、运行时消费、持久化/副本语义、故障恢复、运维观测、OpenAPI 和测试。覆盖公开协议 handler、管理员接口、后台 worker、事件总线、DLQ、Hermes 工具、注册表、provider、路由、配额、计费、鉴权、缓存、健康、审计、通知和凭据链。除九轴接线状态外，必须按成熟项目源码把大功能继续拆成输入分支、严格校验、身份规范化、批内/存量冲突、状态门、选择、凭据准备、出站、失败分类、重试/换号、状态回写、计费归因、审计、恢复和运维动作等微功能节点，并核对节点间传递的真实字段和调用顺序。`rg` 只作定位，所有发现必须打开真实源码核实。前端、生产部署和真实密钥不在本轮直接修改范围。 |
| Success criteria | 建立可追踪的全局能力接线矩阵；每个已审能力至少回答“定义/构造/注入/消费/激活/持久化/副本安全/可观测/测试”九轴，并有一张微功能节点表，逐项区分“代码存在、生产调用、失败闭环、运维可见、人工可恢复、判别测试”六种成熟度。所有问题带生产源码 `file:line`、可达链、辐射模块、严重度和修复建议；可测试、可回滚且不命中全局三项并集决策门的确定问题直接修复并测试；不得用文件存在、类型存在、注册名存在、文档状态或单一 happy-path 测试代替接线证据。所有改动通过独立 Draft PR 提交，未经 Owner 明确同意不得合并主线。 |
| Time estimate | 首轮完整审计约 6-12 小时；按可闭环批次持续交付，不等全部扫描完才报告。预计 4-8 个审计批次，每批包含证据、修复、测试和独立 review。 |
| Blast radius | 审计本身为只读；后续低/中风险修复可能影响诊断合同、依赖装配、后台任务启动、开关读取和非资金业务路径。任何涉及数据库 schema、资金账、鉴权核心、billing ledger、quota enforcement、运行时依赖或生产部署的改动停止在决策包。 |
| Failure modes | 只搜名字不读消费代码导致假阳性；把 nil fallback 当未接线或把 non-nil 当已激活；只看 chat 漏掉其他协议；只看请求路径漏 worker/恢复路径；把单机内存状态误称多副本安全；修复一个入口却漏 alias/Hermes/OpenAPI；测试 fixture 没制造接线差异；把参考源码阅读和实现放进同一会话造成 clean-room 污染；在含其它目标改动的工作树误混 PR。缓解方式是九轴矩阵、逐链引用、协议横向表、差异 fixture、参考 specifier 隔离车道、独立工作树和分批 PR。 |
| Decision points | 全项目统一执行 Owner 最新规则：只有“源码核实后的成熟项目存在实质分歧”且“没有源码核实的成熟先例或 Safe Equivalent”且“选错会造成高危”三项同时成立，才暂停该实现选择并集中提交 Owner 决策；三项缺一则继续实施、记录风险并用判别测试闭环。PR 合并、生产部署、真实秘密、`LICENSE` 和破坏性操作仍是独立硬门。涉及 schema、鉴权、资金、强配额或真实上游费用时，必须在实施记录中给出当前 HUAKAI 真码链、成熟项目行为、方案优缺点、迁移、测试和回滚；不再因为标签本身自动停工。 |
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

## 微功能与调用链颗粒度

每个“大功能已实现”的结论必须继续拆到以下最小审计单元；某一项缺失时，不得用相邻模块代替：

1. **入口形态**：单项、批量、文件、OAuth callback、后台扫描、内部事件分别是否有真实入口。
2. **解析与严格校验**：字段范围、未知字段、过期值、空值、格式、大小、数量和组合约束是否 fail-closed。
3. **身份与规范化**：tenant、user、key、provider、account、credential、model、request、claim 的规范身份如何产生和传递。
4. **重复与冲突**：批内重复、存量重复、同一身份多账号、同账号多凭据、并发写入和旧版本覆盖如何处理。
5. **状态门**：active、expired、revoked、refreshing、cooldown、overloaded、operator attention 等状态分别允许什么动作。
6. **选择与占用**：候选硬门、排序、粘性、并发槽、RPM/TPM/session/window 和 claim 的实际先后顺序。
7. **凭据准备**：解密、刷新、版本/CAS、代理、TLS/transport、协议身份和秘密清零是否在发网前闭环。
8. **出站与协议**：模型映射、请求变换、超时、流式边界、响应解析和上游 request ID 是否进入统一合同。
9. **失败分类**：本地校验、401/403、429、5xx、网络、客户端断开、部分流、结算失败是否被不同处理。
10. **重试与换号**：同号 retry、跨号 fallback、模型 fallback、可重放请求体、交付后禁止换号和预算耗尽语义。
11. **状态与健康回写**：credential、账号、channel、逐模型冷却、额度和人工状态分别由谁写、由谁读、怎样恢复。
12. **资金与归因**：最终账号、最终模型、claim、quota、usage、价格、退款/释放和幂等身份是否保持一致。
13. **审计与可观测**：成功、拒绝、重试、换号、恢复和批量逐项结果是否可追踪且不泄密。
14. **自动与人工恢复**：DLQ、sweeper、refresh-now、清状态、重新授权、重放和 operator action 是否回到同一状态机。
15. **多副本与传播**：租约、锁、cache invalidation、outbox、水位或数据库直选是否保证跨副本收敛。
16. **判别测试**：正常、失败、并发、恢复、租户隔离和变异测试是否能让“删掉接线”后精确变红。

成熟项目颗粒度用于发现微功能和协作义务，不用于复制实现。每个非 HUAKAI 行为结论必须来自当前默认分支源码的独立 clean-room specifier 报告；当前实现会话只读取报告，不直接读取参考源码。

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

#### Batch 2D：非 Chat 上游反馈与安全重试纵向切片

先以 completions 与 messages countTokens 为首个可判别切片，提炼 provider-neutral 的上游反馈合同，再横向复用到 embeddings、rerank、images、audio：

1. 上游 HTTP 错误必须复用统一分类结果，写入账号健康、账号×模型冷却或 auth 冷却车道；401/明确凭据失效触发去抖热刷新。
2. 上游成功必须写 success 信号，使 auth 冷却可以自愈；Anthropic 响应头继续进入 session-window 持久化。
3. retry 只允许发生在客户端交付前；失败账号进入本请求排除集，下一 attempt 不能仅重复命中同一账号。
4. `Abort` 成功是重新 Reserve 的前置条件；`Abort` 失败时立即终止，避免 claim 状态未知时继续产生第二笔预扣。
5. 普通 retry 遵守 `RoutePlan.RetryableEndClasses`、attempt budget、租户 retry budget 和全局 retry kill-switch；auth failover 保留独立且至多一次的子预算。
6. 无计费 claim 的 countTokens 复用同一错误反馈与账号排除逻辑，但不得重新引入并发槽占用或伪造计费动作。
7. 判别测试至少证明：500/429/401 的状态反馈不同；失败账号被排除；HTTP 500 能换号成功；400 不重试；Abort 失败不重试；成功会清 auth 车道；生产 composition root 注入同一共享反馈器。
8. 同一逻辑请求复活已中止 claim 时，选号、槽租约、路由观测和结算必须使用 `ClaimGate.Reserve` 返回的权威 `AttemptSeq`，不能使用当前 HTTP 请求内从 1 重新计数的本地循环号。
9. 图片生成必须额外服从副作用安全门：裸换行保活一旦已写入客户端，当前 attempt 视为已开始传输，禁止再换号；可能已经创建付费异步任务的协议只在明确 401/429 且响应没有任务 ID，或已有任务已确认取消成功时自动换号。传输错误、空响应、5xx、业务终态失败和取消失败均保守终止并保留对账证据，避免重复生成和重复上游费用。
10. 图片与音频的计价必须随当前 RoutePlan attempt 重新计算，不能让第一候选的上游模型或 pool 倍率污染后续候选的 reserve/settle；上游成功信号必须先于本地翻译、usage 解析、计价或结算失败写入。

该切片不改 schema、余额算法、费率、quota 规则、鉴权角色和真实上游默认费用；只复用现有 HUAKAI 分类、健康、冷却、刷新、selector 和 billing 合同。若实现过程中发现现有合同无法同时满足 claim 安全与重试，则停止该分支并提交 Owner 决策，不以静默降级换取测试通过。

进度：completions、messages countTokens、embeddings、rerank、images、audio 与 Gemini countTokens 已完成该合同接线和判别测试；Responses、Gemini generate/embed 已由源码确认复用 chat/embeddings 主链。Media task 已完成关闭提交后仍可查询、worker 错误分类观测两项低风险收口，并确认开关 drain、提交歧义、超时取消/快照、统一账号路由四项需要 Owner 定性的结构性问题；图片保活最终状态合同同样等待 Owner 决策。

#### Batch 2E：Released 账号族 opt-in live 验收矩阵

复用现有 `e2e_upstream` 的真实 gateway 子进程、独立 PostgreSQL 种库、客户 key、账号池、计价、quota、claim、usage 和槽释放断言，不再为每家复制整套测试基础设施：

1. 覆盖 Anthropic API key、Claude OAuth/session、Gemini AI Studio API key、Gemini Code Assist、Antigravity OAuth/session、Kimi API key 与 Kimi OAuth。
2. 上游秘密只从环境变量读取；OAuth/session 使用完整 JSON 环境变量，写入测试库前走现有 handler 严格校验和 AES-GCM 加密。
3. 每项必须同时提供显式模型环境变量，禁止把易漂移的线上模型名硬编码成“永远可用”。
4. env-gated adapter 只在对应 live 子进程打开，不改变生产默认值。
5. 默认执行无真实凭据时全部 `Skip`，不产生上游费用；带 `e2e_upstream` tag 时必须至少编译全部矩阵。
6. live 成功必须证明 HTTP 内容、usage、committed claim、quota settle 和账号槽释放；未实际提供凭据并运行前，只能标记“验收入口已建”，不得宣称真实账号已验收通过。
7. Anthropic API key 与 Claude OAuth/session 使用原生 `/v1/messages` 响应合同；Claude OAuth/session 额外满足现有兼容形态门，禁止用 `/v1/chat/completions` 绕过或制造必然 403 的假旅程。

#### Batch 2B：以 Sub2 账号系统为主轴的完整功能总账

Owner 补充指令：“你主要看 sub2 他一整套逻辑是怎么样的，包含了那些功能。我怀疑我们这个逻辑乱七八糟，功能缺失很多。”

- 由隔离 clean-room specifier 以 Sub2API 为主对象，按真实源码拆出账号系统完整生命周期：创建/导入、授权、凭据规范化、刷新、缓存与锁、账号状态、调度门、评分、并发/RPM/额度、模型范围、请求出站、单账号 retry、跨账号 fallback、错误分类、冷却/封禁/验证、健康/套餐/余额同步、代理一致性、管理恢复、测试和可观测。
- CLIProxyAPI 与 New API 只作默认三镜反证和补充，不抢 Sub2 主轴；任何“Sub2 有/没有/更强”结论必须逐项引用当前可读 SHA。
- 当前 HUAKAI 会话只读取 specifier 行为报告，再把每项映射为 `完整接入 / 部分接入 / 重复体系 / 建而未用 / 缺失 / 产品不同不适用 / 待真实验证`。
- 输出一张功能总账和一张真实调用顺序图，区分“请求能跑”“账号可运营”“多副本可恢复”“发布证据充分”四种成熟度。
- 先做只读审计；涉及 schema、鉴权契约、强配额、资金、真实凭据或数据迁移的修复继续进入 Owner 决策包。

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

## GW-WIRE-009 实施切片：账号调度真相只读聚合

| 项目 | 内容 |
| --- | --- |
| Owner directive | “找到之后接线，修复他们！”；“像这种问题不要糊弄过去，参考成熟项目就行”；“注意颗粒度……整个链路调用出现的细小的功能我们有没有”。 |
| Scope | 扩展现有 provider-account health 管理接口，聚合生产 selector 已实际消费的账号启用状态、provider health、credential state、channel health、进程内 auth cooldown 和逐模型 cooldown；旧 `rate_limit_reset_at`、`overload_until`、`temp_unschedulable_until` 继续只读展示但明确标记为不参与当前 selector。不得修改 selector、状态写入、数据库结构、鉴权角色、资金、强配额或真实上游调用。 |
| Success criteria | 管理端能区分 `eligible`、`blocked`、`request_dependent`，返回明确阻断原因、条件原因、状态来源、持久化范围和未评估的请求级 gate；channel health/auth cooldown 接线缺失、记录不存在和读取失败不得被混成同一种“健康”。测试删除任一真实状态读取后必须精确变红。 |
| Time estimate | 约 2-4 小时，包含查询扩展、运行时依赖接线、单元/集成/竞态/全仓验证、独立 review 和 Draft PR。 |
| Blast radius | 只增加管理 API 响应字段和只读状态方法；现有字段与路由保持兼容。若聚合判断错误，会误导运维但不会直接改变流量。 |
| Failure modes | 把“没有 channel health 记录”误报为故障；把进程内 auth cooldown 冒充跨副本真相；把 ramping 简化成全放或全禁；把模型级 cooldown 错算成整号禁用；把旧账号时间字段继续冒充 selector 状态；读取辅助状态失败仍返回绿色。缓解方式是三态结论、来源/持久化标记、请求级未评估门清单和失败时 503。 |
| Decision points | 本切片不命中 schema、鉴权、资金、真实费用或秘密边界，不需要新增 Owner 决策。后续若要让旧账号时间字段进入 selector、持久化 auth cooldown 或改变 ramp admission，必须另开决策与迁移切片。 |

### 实施顺序

1. 查询补齐 selector 当前读取的 `credential_state`、`model_rate_limits` 和三个旧账号时间字段。
2. 为 channel health service 增加按 provider account 的只读最新状态查询。
3. 为 auth cooldown 增加并发安全只读快照，明确 `process_local`。
4. 在 provider-account health 响应增加调度评估对象，不改变现有字段。
5. 增加 provider/channel/auth/model/legacy 各轴的判别测试与 PostgreSQL 查询测试。
6. 运行目标包、`-race`、全仓、`vet`、质量门、代码预算和变异自检。
7. 暂存后执行独立 Codex review；无 S0/S1 后提交独立 Draft PR，未经 Owner 同意不合并。

### 实施结果

- 已完成账号启用、provider/channel 可用性、provider health、credential state、channel health、进程内 auth cooldown、逐模型 cooldown 和旧时间字段的只读聚合。
- 已完成 `eligible / blocked / request_dependent` 三态合同，明确阻断原因、条件原因、数据来源、持久化范围和未评估请求级 gate。
- 已把管理详情接到 selector 共用的 channel health/auth cooldown 实例；辅助状态读取失败和损坏 JSON 均 fail-closed 返回 503。
- 已完成判别性变异自检：临时断开 channel health 阻断接线后，目标测试精确变红；恢复后通过。
- 已通过目标测试、相关包 `-race`、全仓 `go test ./...`、`go vet ./...`、OpenAPI 一致性、代码预算、`git diff --check` 和质量门。本机 PostgreSQL 集成测试因未设置 `HUAKAI_DATABASE_URL` 明确跳过，交由 PR CI 真库任务验证。
- 尚未改变 selector、schema、auth cooldown 副本语义、旧时间字段权威关系或 `clear-rate-limit` 恢复范围；这些属于后续独立切片。

### 回滚

本切片无数据迁移。回滚新增响应对象、只读方法、查询列和测试即可；不得通过吞掉
channel health 读取错误或把未知状态强行归为 `eligible` 来保住接口可用性。

## GW-WIRE-007 实施切片：账号观测来源与覆盖真相轴

| 项目 | 内容 |
| --- | --- |
| Owner directive | “找到之后接线，修复他们！”；“主要看建立了很多功能但没接入使用，没有完美的配合起来”；“注意颗粒度”。 |
| Scope | 在账号健康详情现有只读聚合中增加 provider、账号类型、当前凭据模式、账号额度、模型目录和 project 身份的来源/覆盖状态。只消费现有 PostgreSQL 安全元数据、Claude OAuth 5h/7d 快照、全局 provider 模型同步时间和 credential `project_ref`；不解密凭据、不请求新上游、不改变 selector、schema、鉴权、资金、配额或账号状态。 |
| Success criteria | 运维能明确区分：Claude OAuth 额度“已观测/已过期/尚无快照”，其他模式“尚未实现账号额度采集”；模型同步是 provider 全局目录而非账号级模型；Code Assist/Antigravity 所需 project 是否已解析。缺少凭据、provider 被删除、无快照和过期快照必须是不同状态。现有字段保持兼容，两个 admin alias 使用同一响应合同。 |
| Time estimate | 约 2-3 小时，包含查询、只读视图、OpenAPI、单元/集成/全仓门禁、独立 review 和 Draft PR。 |
| Blast radius | 只增加管理 API 响应对象和 SQL 查询列；错误只会影响运维展示，不改变真实流量。 |
| Failure modes | 把 provider 目录同步误报为账号模型发现；把没有快照误报为 0% 用量；把过期窗口当可用；把所有 Gemini 模式混成同一种；为展示 project 去解密并泄露 secret；使用删除 provider 的 code 仍报可用。通过来源、粒度、覆盖枚举、时间判定和响应敏感词测试缓解。 |
| Decision points | 本切片不命中高风险边界，无需新增 Owner 决策。它不宣称完成 Gemini/Antigravity/Kimi 的真实额度采集；后续 provider-neutral 持久化快照、上游 quota/model fetcher、真实费用和让观测参与 selector，仍需单独 schema/费用决策。 |

后续主动探测切片的 Owner 决策已确定：成本预算采用 **B**，建设按自然日持久化的探测账本，并同时以请求数、Token 和金额设置硬上限；自动恢复范围采用 **A**，只允许探测当前健康/降级账号以及冷却到期、具备恢复资格的账号，成功后进入渐进恢复。人工停用、鉴权失效和凭据失效账号不得自动探测或自动恢复。该切片涉及 schema、真实上游费用和账号状态写入，必须另开干净分支与 Draft PR，不混入本只读观测切片。

### 实施顺序

1. 健康查询补齐 provider code、account type 和运行时可服务 credential 的 vendor/auth mode/project ref；刷新历史继续独立选择，禁止用最近刷新记录冒充当前凭据。
2. 在 `accounthealthview` 构建独立 observability 对象，使用稳定枚举表达来源、粒度和覆盖状态。
3. 健康 handler 复用该对象，不读取明文凭据；现有 session-window 字段继续兼容。
4. OpenAPI 补齐新增对象及枚举，明确 provider 目录不代表账号可用模型。
5. 增加 Claude 有效/过期/空快照、Gemini/Antigravity project、Kimi 未实现额度、删除 provider、凭据轮换/多模式冲突和敏感字段排除测试。
6. 运行目标测试、PostgreSQL 集成、`-race`、全仓、`vet`、质量门、代码预算和变异自检。
7. 暂存后执行独立 Codex review；无 S0/S1 后提交叠放在账号调度真相 PR 之上的 Draft PR，未经 Owner 同意不合并。

### 提交前审查记录

第一轮 review 发现刷新历史可能冒充当前运行凭据，归一化为 S1；现已拆分“运行时可服务凭据”和“最近刷新历史”，多个可服务模式明确返回冲突。第二轮 review 发现已停用账号仍可能被观测为存在可服务凭据，同样归一化为 S1；现已把账号启用状态纳入与生产解析一致的候选条件，并增加“停用后候选归零”的 PostgreSQL 判别测试。两次修复后专项、race、`sqlc compile`、全仓测试、`go vet` 和质量门均通过；本机 PostgreSQL 集成测试因未配置 `HUAKAI_DATABASE_URL` 明确跳过，等待 CI 真库执行。

### 回滚

本切片无数据迁移。回滚新增查询列、observability 响应对象、OpenAPI 和测试即可；不得回滚为把 provider 目录或空 quota 快照冒充账号级已验证能力。

## Pre-execution checklist

1. 审计使用 `HUAKAI-wt-global-wiring-codex`；本切片只使用独立工作树
   `HUAKAI-wt-account-scheduling-truth-codex` 和分支
   `fix/account-scheduling-truth-20260716-codex`。
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
