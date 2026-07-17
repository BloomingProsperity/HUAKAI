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
| Parallel-plan status | Owner 已要求 Codex 独立工作且不要触碰另一目标。本计划不读取、不修改 Claude 工作树或同题计划，作为 Owner 指定的独立 Codex 车道。后端参考行为仍由隔离 Codex specifier 会话读取并产生报告；本会话为回答 Owner 的前端布局问题单独读取过 Sub2API UI 源码，但该阅读不进入本批后端 schema/API 实现，也不复制其 UI 代码、结构或标识符。 |

### 三身份落地前的高敏账号接入授权过渡合同

账号批量接入会写入上游账号和加密凭据，不能沿用当前 `platform_admin` 可携任意请求体 `tenant_id` 的跨租户能力。正式 capability grant schema 落地前，采用以下可撤销的 Safe Equivalent：

1. 只接受部署者签发的 `tenant_operator` 程序化 token；签发和撤销 token 即当前授权与撤权载体，未签发时默认无权限。
2. 请求 `tenant_id` 必须与 token 的 `ScopeTenantID` 完全相等且均为正数；tenant 身份取自已认证 scope，请求体只作一致性校验，不能扩大权限。
3. `platform_admin`、session admin、无 scope token 和跨 tenant 请求全部 fail-closed；部署者不能用平台身份替任意租户执行账号接入。
4. 该过渡合同不引入租户层级，也不允许租户创建租户。正式租户 capability grant 上线后，用 grant 校验替换 token 载体，继续保留 tenant scope 强绑定和审计归属。

参考项目源码可确认的账号级导入结果是逐项识别后创建、更新、跳过或失败；HUAKAI 只吸收该运营结果，并按自身已确定的单层租户边界重新设计授权，不外推或照搬参考项目的权限模型。行为证据见 `docs/process/research/2026-07-16-backend-global-wiring-audit-codex.md` 的 GW-WIRE-010。

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

#### Batch 3A：请求观测与主动探测存储合同拆分

Owner 已批准把普通请求观测从 `last_probe_at` 迁到独立合同。本批使用独立堆叠分支和 Draft PR，只处理存储与消费者切换，不启动真实主动探测：

1. 使用迁移 `0189` 新增 nullable `last_request_observed_at`，避开其它未合并分支已经使用的 `0182～0188` 编号。
2. 上迁移只搬运 `last_probe_at IS NOT NULL AND last_probe_latency_ms IS NULL` 的历史值，并清空这些旧 probe 值；带 latency 的记录保留为潜在主动探测证据，不做不可证实覆盖。
3. 下迁移在 `last_probe_at` 为空时把请求观测值放回旧列，再删除新列，保证回滚不把已有主动 probe 覆盖掉。
4. 请求完成事件只单调写 `last_request_observed_at`；事件乱序、重试和 DLQ 重放不得让时间倒退。
5. 健康详情、账号列表和 Hermes 必须分别暴露 `last_request_observed_at` 与来源；`last_probe_at` 只保留真正 probe 语义。
6. OpenAPI 同步两套字段，不用 deprecated alias 冒充迁移完成。
7. 真 PostgreSQL 测试覆盖：历史无 latency 行迁移、带 latency 行保留、单调写、跨租户隔离、down 回滚；普通测试覆盖所有管理消费者。
8. 失败回滚：若任何消费者仍依赖旧被动值，撤销本堆叠 PR；不得在生产部署后手工双写或静默混用两列。

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

## GW-WIRE-010 实施切片：账号级批量接入

Owner 已授权按源码核实结果直接修复，并明确要求同一上游身份命中多条账号时不得任选第一条。本切片只关闭账号级批量导入及其身份持久化链，不混入账号运营聚合、其它协议修复或租户授权模型重做。

### 范围与合同

1. 在 `/admin/v1/credentials` 下增加账号批量接入预检与执行入口；保留既有 `paste/cli-import/csv-import/json-import` 作为“向已知账号写凭据”的低层助手，不改变其兼容合同。
2. 预检请求携带租户、来源类型、provider/channel/account 默认配置和原始导入内容；响应只返回脱敏逐项动作、原因、已有账号、风险、确认项和摘要，绝不回显 token、cookie、密钥或原始内容。
3. 预检生成覆盖输入形态、账号默认配置、当前无秘密身份 inventory 和逐项动作的 `plan_hash`。执行必须重新读取当前 inventory、重新生成计划并精确匹配 `plan_hash`，避免预检后数据库变化仍按旧结论落库。
4. 支持 `create/update/skip/conflict/fail` 五种逐项结果。`conflict/fail` 永不自动执行；`skip` 不写库；`create/update` 只有在请求提供全部必要确认项时才执行。
5. 每个候选是独立账号接入单元：单项事务内完成账号行、加密凭据、凭据审计和管理审计；某项失败回滚该项，但继续处理其它项并返回完整结果。
6. 新账号默认名称由请求前缀和输入序号稳定生成，不使用 email、token、上游个人标识或其它秘密派生名称。账号协议兼容和同渠道混用风险继续复用现有 `accountcreate` 与 `mixedchannelrisk` 合同。
7. 更新已有账号时必须锁定精确 credential ID、auth mode 和 credential version，并走既有凭据轮换 CAS；预检后凭据版本变化必须返回冲突，不覆盖新版本。

### 身份与 schema 决策

1. 使用 `0190_account_credential_intake_identity` 加性迁移，为 `account_credentials` 增加上游个人主体、身份来源和稳定凭据材料指纹；`external_account_id/external_account_email` 继续复用既有 `0141` 列。
2. 不增加唯一约束：历史数据可能已经重复，强行建唯一索引会让迁移失败或替 Owner 擅自删除数据。查询命中多个账号或同账号同模式多条候选时统一返回显式冲突，要求人工消歧。
3. 凭据材料指纹只选择当前实际认证材料，使用租户域分隔和单向 SHA-256；它不参与鉴权、计费、配额或出站，只用于无刷新材料账号的精确去重。凭据 rotate/refresh 后必须同步更新为当前材料，旧材料不得继续命中。代价是数据库与低熵秘密同时泄露时存在离线枚举风险；当前凭据形态均要求高熵 token/key，仍禁止把普通密码类材料纳入该指纹。
4. OAuth/token exchange 自动提取的主体来源可作为强身份；导入文件声明的主体只作为弱身份和冲突证据，不能无确认自动选择已有强身份账号。
5. access-token-only 候选只按稳定材料指纹匹配，不按 workspace/email/显示名合并，避免共享账号作用域中的不同个人被误覆盖。

### 鉴权与风险边界

1. 本切片沿用现有敏感凭据采集的 token-only、`platform_admin` 边界，不扩大 session 管理员或租户管理员权限。
2. “部署管理员授权某个租户管理员、默认未授权，部署管理员不得任意代办”的最终合同仍由 GW-WIRE-018 统一实现；本切片不得自行发明 grant 表或扩大角色。
3. 不触碰资金、quota、billing ledger、真实上游请求和生产秘密；预检纯本地解析与数据库只读，不发网、不产生上游费用。

### 判别测试与回滚

1. 领域测试覆盖：批内重复、同身份多账号、同账号多 auth mode、缺少目标 mode、邮箱弱匹配、强弱身份冲突、access-token-only 指纹、过期且不可刷新、撤销/刷新中/operator-attention 状态门和秘密不回显。
2. HTTP 测试覆盖：预检无写入、执行哈希漂移拒绝、缺确认不执行、部分成功返回完整逐项结果、tenant/body 校验、token-only 鉴权和请求体大小限制。
3. PostgreSQL 集成测试覆盖：创建账号与凭据同事务、任一步失败无孤儿；轮换版本 CAS；重复存量身份显式冲突；身份列和材料指纹 create/rotate/refresh 持久化；迁移 up/down/up。
4. composition-root 与 OpenAPI 判别测试必须证明两条新路由真实挂载且生产依赖非空；删除 wiring 或 schema 字段时测试精确变红。
5. 回滚优先停用新路由；代码回滚后 `0190` down 只删除新增索引和三列，不影响 `0141` 既有身份字段、账号、凭据密文或其它业务数据。

### 实施状态

1. 状态：`Implemented / Reviewed`。账号级预检、执行、身份 inventory、精确版本轮换、逐项事务、双审计、健康初始化、管理路由和 OpenAPI 已接线。
2. HTTP handler 已放入独立 `accountintakehttp` 子包，未继续扩大超预算的根 `gatewayhttp` 包。
3. 真 PostgreSQL 已验证创建原子性、审计失败回滚、精确轮换、旧计划拒绝和 `0190` up/down/up；普通测试已覆盖身份歧义、状态门、秘密不回显、严格 JSON、错误收敛和鉴权边界。
4. `0190` 所在分支堆叠在迁移 `0189` 的 PR #259 之上。由于项目使用 `golang-migrate` 单一版本轴，合并顺序必须为 #259 后本批；不得先部署 `0190` 再补 `0189`。
5. 本批不扩大租户管理员权限；GW-WIRE-018 完成授权合同前仍只允许 token 来源的 `platform_admin` 使用。

## 账号导入凭证总修复：整合与专用入口

| 项目 | 内容 |
| --- | --- |
| Owner directive | “做这个啊”；“账号导入凭证模块都修复了吗”。 |
| Scope | 以已接入生产路由且 CI 全绿的 PR #262 为唯一主干，迁移 PR #258 中仍有价值但漏接的上游个人 subject 身份与判别测试；bulk、恢复动作继续归属既有独立 PR，不重复并入。随后依次建设 Claude Cookie、Setup Token、Codex 专用批量与 Agent Identity、CRS 插件与安全迁移包。前端不在本目标范围。 |
| Success criteria | #258 不再保有未迁移的独有账号导入能力；普通凭据批量接入可准确保存账号作用域与个人 subject，重复身份不任选第一条。四类专用入口均具备预检、权限、秘密处理、原子落库、审计、失败分类和恢复合同，并分别通过单元、PostgreSQL、OpenAPI、全仓与独立 review。 |
| Time estimate | 整合切片约 1-2 小时；四类专用能力按独立 PR 连续实施，不用一个巨型提交承载。 |
| Blast radius | 整合切片只影响 OpenAI/Codex 身份元数据，不改变鉴权、计费、配额或出站。后续 Cookie/Setup/Agent/CRS 会触及高敏感凭据、网络和 schema，必须默认关闭并逐批验证。 |
| Failure modes | 把个人 subject 误当账号作用域导致错合并；把弱导入声明当可信身份；Cookie/私钥进入日志或审计；Setup Token 能导入但不能刷新；CRS 被用于 SSRF；迁移包明文泄密。分别以双层身份、显式冲突、secret-mask、统一开关、固定上游/双时刻 SSRF 和加密签名恢复包缓解。 |
| Decision points | Owner 已明确要求直接完成账号导入凭证模块。采用既定安全默认：部署治理主体只授权/撤权，不代租户操作；能力默认未授权；Agent Identity 保持 Experimental；CRS 为插件；迁移包默认不含秘密，含秘密恢复包必须 step-up、加密、签名、短时有效。若源码证明这些默认与现有鉴权/schema 无法兼容，再按“有疑问必须停下”提交具体冲突。 |

### 执行顺序

1. 从 #262 建立整合分支，对照 #258 真码迁移 ChatGPT/Codex subject 身份和被删减的判别测试；不得重新引入 #258 的死代码入口、旧迁移编号或已拆分运营模块。
2. 跑目标、race、PostgreSQL、全仓和质量门，独立 review 后提交叠放 Draft PR；确认 #258 无独有能力遗留后再处理旧 PR，未经 Owner 同意不合并。
3. Claude Cookie：单次 Cookie 转换、固定域名、step-up、授权租户自操作、逐项 dry-run/execute、内存清零。
4. Setup Token：一等 acquisition plan、专用入口、acquisition/refresher 同源开关和启动一致性门。
5. Codex：专用多行 `auth.json`/access token 接入；Agent Identity 使用独立凭据模式、AES-GCM 信封、签名/任务绑定/恢复/撤销，并保持 Experimental。
6. CRS 与迁移包：插件化远程源、allowlist 与双时刻 SSRF、逐项差异预览；结构包默认无秘密，恢复包加密签名且短时有效。
7. 每一项使用独立干净分支、独立 Draft PR 和独立回滚，不跨 PR 合并数据库迁移、鉴权授权和真实上游网络风险。

## Pre-execution checklist

1. 主审计只使用 `HUAKAI-wt-global-wiring-codex`；已获批 schema 批次使用其堆叠工作树 `HUAKAI-wt-request-observation-codex` 和分支 `fix/request-observation-schema-20260716-codex`，PR 基于主审计分支。
2. 每批开始和结束检查 `git status`，所有改动通过该批次的 Draft PR 提交；未经 Owner 同意不合并。
3. 当前后端实现不使用本会话为前端答疑读取的参考 UI 源码；后端参考行为只消费隔离 specifier 报告，并且不触碰 Claude 或其它目标工作树。
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
