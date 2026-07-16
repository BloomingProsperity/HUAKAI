# 2026-07-14 绑定级 `fallback_class` 降级类别真生效（Codex 独立计划）

| 项目 | 内容 |
| --- | --- |
| Owner directive | “HUAKAI 片3 独立计划 · 绑定级 fallback_class 降级类别真生效——只写计划禁实现” |
| 本轮产物 | 仅本文件；不改生产代码、测试、迁移、OpenAPI 或其它文档 |
| 独立性 | Codex 独立稿；未读取同主题 Claude 计划，等待双计划并列讨论后再综合 |
| Clean-room lane | `specifier` |
| Prior lanes on this artifact | `none` |
| 参考范围 | `sub2api`、`new-api`、`CLIProxyAPI` 三镜真码；只提炼行为，不复制实现、命名、注释、结构或源码片段 |
| 范围内 | 语义裁定、触发边界、路由/选号/attempt 接缝、管理写口、前端、OpenAPI、验收测试、风险、工作量与执行顺序 |
| 范围外 | 本轮一切实现；未来实现也不含数据库迁移、认证核心改造、账本规则改造或新外部依赖 |
| 成功标准 | 计划能直接指导后续 failing-test-first 实施；默认 `normal` 路径零翻转；四个目标类精确触发；无计费/auth/安全绕过；每条核心测试有可证红变异点 |
| 预计工作量 | 本计划与交叉讨论约 2～3 小时；Owner 批准后的完整实现约 6～9 工程日（45～70 小时），另需真 PostgreSQL、race 与全协议门 1～2 小时墙钟 |
| Blast radius | 所有模型路由协议、池内选号、重试/排队、claim 生命周期、管理绑定表单和路由审计；失败可导致错误越级、重复扣费、并发上限绕过或默认流量漂移 |
| Observed regions | HUAKAI 37 个源码/契约区域；三镜 22 个源码/测试区域 |
| Inferences | 6 个 HUAKAI-fit 设计推断，均在本文标为“裁定/推断” |
| Open questions | 3 个，均给出 Codex 推荐答案并列入 Owner decision points |

## 1. 先给结论

我的裁定是：`fallback_class` 是**绑定候选的类别标签，经 `DefaultRouter.Plan` 编译成 attempt 层的第二梯队；是否越级必须由 executor 在看到规范化失败后决定**。它既不能只做 selector 内部排序，也不能把所有非 `normal` 绑定预先混进今天的普通 `Attempts`。

具体优先关系固定为：

1. **Class 最外层**：`normal` 是唯一主类；请求只可从 `normal` 越级到一个与失败族精确匹配的目标类。
2. **Priority 类内严格分层**：数值小的层先用；任何非 `normal` 绑定即使 Priority 更小，也不得抢在仍可用的 `normal` 前面。
3. **Weight 只在同 Class、同 Priority 段内做绑定级加权无放回首选**。
4. **`selection_mode` 不控制绑定 Weight**；它继续随命中的 `BindingID` 进入 pool，决定该 pool 内账号的均匀或 `static_weight` 加权选号。现有判别测试已明确绑定 Weight 在两种 `selection_mode` 下都生效（`backend/internal/router/default_router_weighted_test.go:11-48`）。

自动映射裁定如下：

| 目标 class | 精确定义 | 自动触发 |
| --- | --- | --- |
| `normal` | 主类；空值/缺元数据也按它解释 | 首次请求总从这里开始 |
| `quota` | 上游或绑定/账号容量车道 | 绑定并发/RPM 饱和、池槽耗尽、纯健康/冷却/速率 gate 耗尽、排队超时/溢出、规范化上游 429 |
| `context_window` | 运维明确声明可承接更大上下文的目标车道；该 class 本身就是运维 attestation | 仅精确的本地上下文 gate 或规范化“上下文过长”错误；普通 body 413/请求格式 400 不算 |
| `safety` | 仍受 HUAKAI 本地审核约束的替代上游车道 | 仅上游内容策略拒绝；本地 moderation、租户策略和权限拒绝绝不触发 |
| `manual` | 运维手工把某绑定标为“通用瞬态故障兜底”；“manual”描述配置来源，不表示请求时人工点选 | 仅既有可重试的上游 5xx、连接/首字节超时、空响应等通用瞬态故障 |

每个请求、每个模型最多发生一次 `normal -> 对应 class` 转移；禁止 `quota -> manual`、`safety -> quota` 或同类递归。第一版目标类只有 **1 次额外 attempt 子预算**，避免与现有模型 fallback、普通 attempt 和 auth 子预算相乘成重试风暴。目标类 attempt 仍携带自己的 `BindingID`、`MaxParallelRequests`、RPM/TPM 和 `selection_mode`。

## 2. 取证边界、新鲜度与 Source Coverage Proof

### 2.1 许可证和版本

本轮在本地镜像上核对了 HEAD 与 `origin/main` 相等，三者提交日期均在当前 UTC 日期 30 天内。许可证台账记录：Sub2API 为 LGPL-3.0、New API 为 AGPL-3.0-or-later、CLIProxyAPI 为 MIT（`docs/07_REFERENCE_EVIDENCE_LEDGER.md:13-23`）。源许可证也与台账一致（`Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:LICENSE:1`；`QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:LICENSE:1`；`router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:LICENSE:1`）。

| 镜像 | 已核对 HEAD | 提交时间（UTC/带时区原值） | 本文用途 |
| --- | --- | --- | --- |
| Sub2API | `12d811bd76572836d6df6e1fa8aa5ff91be3b12e` | `2026-07-09T14:57:53Z` | 两级候选集合、gate/冷却/满载/排队边界 |
| New API | `246d62aa5ed3ba2a4728322c269c180a016dc9cd` | `2026-07-09T22:03:45+08:00` | Priority/Weight 与失败驱动 retry 的结合 |
| CLIProxyAPI | `26d45fd46a2d2911adef14772465131066dae465` | `2026-07-10T05:30:12+08:00` | 账号优先级桶、冷却降级、终态请求错误及 auth fallback 边界 |

本文件是行为规格，不是实现蓝图。后续 implementer 不得重读 LGPL/AGPL 源码，只能基于本文、HUAKAI 自身代码和已登记证据实施。受本轮“只能新增一个计划文档”约束，新的三镜行为证据尚未写入 `docs/07_REFERENCE_EVIDENCE_LEDGER.md`；它必须成为综合计划批准后的第一项独立 docs 动作，且在代码实施前完成。

### 2.2 Sub2API：观察到的两级行为

- 首选模型路由集合先做账号存在性、静态可调度、平台、模型映射、模型级限流、配额、窗口花费和 RPM 等过滤；首选集合没有候选时才继续普通集合（`Wei-Shaw/sub2api@12d811b:backend/internal/service/gateway_scheduling.go:251-310`）。账号活动状态、过期、过载、速率冷却、临时冷却和配额也会让候选退出（`Wei-Shaw/sub2api@12d811b:backend/internal/service/account.go:145-165`）。
- 首选候选仍有容量快照且只是即时槽未取到时，会先给首选候选返回等待方案；只有首选全部满载，或全部不具备等待资格，才进入普通集合（`Wei-Shaw/sub2api@12d811b:backend/internal/service/gateway_scheduling.go:400-485`）。
- 普通集合重新做完整 eligibility 过滤，再按严格优先级、负载和使用时序选取；即时槽仍不可得时还有自己的等待兜底（`Wei-Shaw/sub2api@12d811b:backend/internal/service/gateway_scheduling.go:607-651`；`Wei-Shaw/sub2api@12d811b:backend/internal/service/gateway_scheduling.go:675-739`）。
- 判别测试证明两条不同边界：首选账号即时槽均失败但仍可等待时不碰普通账号（`Wei-Shaw/sub2api@12d811b:backend/internal/service/gateway_multiplatform_test.go:2780-2835`）；首选账号负载均为 100% 时才选普通集合中的第三账号（`Wei-Shaw/sub2api@12d811b:backend/internal/service/gateway_multiplatform_test.go:2837-2893`）。

因此，不能把“看到一个 `ErrNoSlotAvailable`”等价成“主类耗尽”。HUAKAI-fit 推断是：先保留主类的现有排队与同类 attempt，只有排队预算用尽或所有主类 attempt 都给出同一可降级失败族时才跨类。

### 2.3 New API：观察到的渠道降级/重试链

- 候选先按离散 Priority 分桶，retry 序号选择对应的优先级层，层内按 Weight 随机；retry 超过层数后钳到最低层（`QuantumNous/new-api@246d62a:model/channel_cache.go:108-202`）。
- 外层在每次转发前重新选择渠道；转发失败经规范化判断后才增加 retry 并进入下一轮（`QuantumNous/new-api@246d62a:controller/relay.go:181-237`）。是否 retry 会同时考虑渠道错误、显式禁重试、剩余预算、固定渠道和状态码（`QuantumNous/new-api@246d62a:controller/relay.go:325-355`）。
- 默认状态规则包含 429 和部分 5xx，同时明确排除若干状态与坏响应体类别（`QuantumNous/new-api@246d62a:setting/operation_setting/status_code_ranges.go:17-38`；`QuantumNous/new-api@246d62a:setting/operation_setting/status_code_ranges.go:70-85`）。跨组模式会先消耗当前组的 Priority 层再进入下一组（`QuantumNous/new-api@246d62a:service/channel_select.go:84-162`）。

HUAKAI-fit 推断是：Priority/Weight 可在 plan 阶段确定候选顺序，但“是否进入第二梯队”必须留到 executor 看到失败分类之后；只在 selector 里提前拼接 fallback 会失去 429、5xx、上下文和安全类别差异。

### 2.4 CLIProxyAPI：观察到的账号 fallback

- 账号先剔除禁用与模型冷却项，再只返回最高可用 Priority 桶；同桶才做轮转或首选（`router-for-me/CLIProxyAPI@26d45fd:sdk/cliproxy/auth/selector.go:199-302`）。高 Priority 可用时低 Priority 从不被选；高 Priority 因模型配额冷却时才选低 Priority（`router-for-me/CLIProxyAPI@26d45fd:sdk/cliproxy/auth/selector_test.go:64-125`）。
- 单次执行维护已尝试账号集合；请求本身无效时立即终止，其他账号级失败才继续寻找下一个凭据（`router-for-me/CLIProxyAPI@26d45fd:sdk/cliproxy/auth/conductor.go:2519-2635`）。外层等待重试也明确排除请求无效，只接受受限的冷却或 429 等待提示（`router-for-me/CLIProxyAPI@26d45fd:sdk/cliproxy/auth/conductor.go:3586-3617`）。
- 401 有更窄的内部恢复边界：当前凭据刷新成功时继续当前账号且不碰备用账号；刷新失败或刷新后仍失败才进入备用账号（`router-for-me/CLIProxyAPI@26d45fd:sdk/cliproxy/auth/conductor_unauthorized_refresh_test.go:161-193`；`router-for-me/CLIProxyAPI@26d45fd:sdk/cliproxy/auth/conductor_unauthorized_refresh_test.go:230-259`；`router-for-me/CLIProxyAPI@26d45fd:sdk/cliproxy/auth/conductor_unauthorized_refresh_test.go:315-335`）。

HUAKAI-fit 推断是：严格层级必须由“更高层没有可执行候选”保护，且 auth 恢复应留在既有 auth 子预算内；auth 终态不能借 `fallback_class` 跨到另一绑定，以免掩盖坏凭据或绕过权限。

### 2.5 三镜对照与本地落点

| 维度 | Sub2API 观察 | New API 观察 | CLIProxyAPI 观察 | HUAKAI 裁定 |
| --- | --- | --- | --- | --- |
| 层级位置 | 首选账号集合后接普通集合（`Wei-Shaw/sub2api@12d811b:backend/internal/service/gateway_scheduling.go:251-310`） | retry 序号推进 Priority/组层（`QuantumNous/new-api@246d62a:model/channel_cache.go:137-168`） | 只暴露最高可用账号桶（`router-for-me/CLIProxyAPI@26d45fd:sdk/cliproxy/auth/selector.go:224-253`） | binding class 先在 Router 分组，再由 executor 激活目标 phase |
| 主层仍活着 | 即时槽满但可等则留在首选层（`Wei-Shaw/sub2api@12d811b:backend/internal/service/gateway_scheduling.go:447-480`） | 本轮成功即终止 retry（`QuantumNous/new-api@246d62a:controller/relay.go:224-236`） | 高层可用时低层不入选（`router-for-me/CLIProxyAPI@26d45fd:sdk/cliproxy/auth/selector_test.go:64-90`） | 任一 `normal` 成功或排队成功，fallback 调用数必须为 0 |
| 触发条件 | gate/冷却/满载或等待资格耗尽（`Wei-Shaw/sub2api@12d811b:backend/internal/service/gateway_scheduling.go:251-297`；`Wei-Shaw/sub2api@12d811b:backend/internal/service/gateway_scheduling.go:466-485`） | 转发失败分类 + retry 预算（`QuantumNous/new-api@246d62a:controller/relay.go:229-236`；`QuantumNous/new-api@246d62a:controller/relay.go:325-355`） | 候选被禁用/冷却，或可继续的账号失败（`router-for-me/CLIProxyAPI@26d45fd:sdk/cliproxy/auth/selector.go:305-361`；`router-for-me/CLIProxyAPI@26d45fd:sdk/cliproxy/auth/conductor.go:2605-2633`） | 同类耗尽 + 同一规范化失败族 + 目标类存在 + 未交付 + 子预算允许 |
| 终态边界 | 无直接可移植结论 | 显式 skip 与预算终止（`QuantumNous/new-api@246d62a:controller/relay.go:325-355`） | 请求无效立即终止（`router-for-me/CLIProxyAPI@26d45fd:sdk/cliproxy/auth/conductor.go:2614-2628`） | billing/auth/本地策略/请求无效/已交付绝不跨 class |

## 3. HUAKAI 现状：四个断点与两个隐藏冲突

### 3.1 数据和后端写口已经贯通

- Registry 的 `BindingMetadata` 已包含 `Priority`、`Weight`、`SelectionMode`、`MaxParallelRequests`、`FallbackClass`，但注释明确说 fallback 仅存储（`backend/internal/registry/registry.go:69-82`）。PostgreSQL resolve 已把数据库值写入该字段（`backend/internal/registry/postgres_registry.go:171-192`）。
- 管理 DTO 的 response/create/update 均接收 `fallback_class`，校验枚举为 `normal/context_window/safety/quota/manual`，省略时默认 `normal`（`backend/internal/modelbindingadminhttp/routes.go:65-68`、`backend/internal/modelbindingadminhttp/routes.go:72-137`、`backend/internal/modelbindingadminhttp/routes.go:187-258`、`backend/internal/modelbindingadminhttp/routes.go:279-299`）。
- Registry create/update SQL 都实际写列并 bump snapshot（`backend/internal/registry/bindings_admin.go:193-217`、`backend/internal/registry/bindings_admin.go:220-249`）。因此本片**不需要迁移，也不需要重新发明 CRUD**；只需补语义校验、注释和消费。

### 3.2 Registry 到 Router 在 fallback 字段处断线

- `PoolCandidateMeta` 当前只有 pool/provider、`BindingID`、并发上限、Priority、Weight 和 `selection_mode`，没有 fallback 类别；`AttemptPlan` 也只携带 `BindingID` 与 `MaxParallelRequests`（`backend/internal/router/route_plan.go:42-62`、`backend/internal/router/route_plan.go:97-134`）。
- chat 的 registry→router 映射透传了 Priority/Weight/selection/并发，却丢了 fallback（`backend/internal/gatewayhttp/chat_completions_attempt.go:48-76`）。现有测试 fixture 已放入 `normal/quota/manual`，期望结构却刻意不含该字段，直接证明它是死字段（`backend/internal/gatewayhttp/chat_completions_dispatch_test.go:1760-1800`）。
- completions、embeddings、audio、images、rerank 的 `route.go` 目前甚至只映射 pool/provider、`BindingID` 和并发上限（例如 `backend/internal/completionshttp/route.go:8-35`、`backend/internal/embeddingshttp/route.go:8-35`、`backend/internal/audiohttp/route.go:27-54`、`backend/internal/imageshttp/route.go:27-54`、`backend/internal/rerankhttp/route.go:8-35`）。本片若只修 chat，会造成同一个绑定在不同协议下语义不同，不能发布。

### 3.3 Router 今天把所有 binding 混成一个普通 attempt 列表

`DefaultRouter.Plan` 对全部 pool 一起排序，按 pool 数生成 2 或 3 次 attempt，并循环填入 `primary/cross_pool_fallback/same_pool_account_failover`；它不知道 class（`backend/internal/router/default_router.go:49-111`、`backend/internal/router/default_router.go:181-189`）。现有 Weight 只在连续同 Priority 段洗序（`backend/internal/router/default_router.go:117-170`）。

隐藏冲突一：若仅把非 `normal` 候选排到列表尾部，当前 `AttemptBudget=2/3` 可能在到达目标类前耗尽；若把目标类提前，又会在失败类型未知时越级。因此不能只改排序键。

隐藏冲突二：现有一个 pool 会生成第二次同 pool attempt，多 pool 会生成三次；外层模型 fallback 又会为下一个模型重建整套 plan（`backend/internal/gatewayhttp/chat_completions_handler.go:471-508`）。若每个 class 都复制 2/3 次预算，最坏请求数会乘法膨胀。

### 3.4 Selector 的错误粒度不足以精确选 class

- pool gate 已有 tenant/lifecycle/channel/protocol/model/capability/credential/health/group/exclusion/pinned/window/session/context/rate 等原因（`backend/internal/pool/router/gates.go:8-25`、`backend/internal/pool/router/gates.go:202-220`），routing reason 也已按原因计数（`backend/internal/pool/router/routing_reason.go:50-80`）。
- 但所有候选被挡后，selector 只在“全是 health”时返回专门哨兵，其余原因都压成 `ErrNoEligibleAccount`（`backend/internal/pool/router/default_selector.go:90-103`）。槽失败也在候选循环中被吞掉并最终压成无候选/等待（`backend/internal/pool/router/default_selector.go:206-245`）。
- 本地 context gate 能精确记录 `context_window`，但当前 per-model 上限对同模型所有 binding 相同（`backend/internal/pool/router/context_window_gate.go:7-24`、`backend/internal/pool/router/context_window_gate.go:38-56`）；chat 还只在外层模型 fallback 开启时才把窗口值喂给 selector（`backend/internal/gatewayhttp/chat_completions_dispatch.go:492-502`）。因此必须把“是否做原始模型预检”扩成“模型 fallback 或存在 binding context phase”，并让已进入 `context_window` 目标的 attempt 只跳过这一个 canonical-window gate、继续接受 body 大小和其它所有 gate。否则该 class 会在模型 fallback 关闭时永远没有本地触发，或被同一个窗口第二次挡住。
- 当前 selector 在即时槽满后先返回 `WaitPlan`（`backend/internal/pool/router/default_selector.go:131-167`）。chat 会等待；只有超时/溢出才产生可重试的 queue failure，取消是终态（`backend/internal/gatewayhttp/chat_completions_queue_wait.go:25-66`）。这一行为应保留。

### 3.5 Executor 当前把 binding 429 当终态

- chat 把 `ErrBindingConcurrencyLimited` 直接写专用 429，并明确不可 retry/switch；绑定 RPM 与 key rate limit 也被合并为终态 429（`backend/internal/gatewayhttp/chat_completions_handler.go:950-989`，现有判别测试见 `backend/internal/gatewayhttp/chat_completions_dispatch_test.go:1803-1829`）。
- 一般无容量才被映射成可重试 503；attempt 循环只看 `RetryableBeforeDelivery`、`RetryableEndClasses` 和 final slot（`backend/internal/gatewayhttp/chat_completions_attempt.go:282-300`）。
- 每次 attempt 已先激活 `AttemptPlan`，再 reserve claim、带 `BindingID`/并发上限选号；失败 abort 后清空 claim，下一 attempt 重新 reserve（`backend/internal/gatewayhttp/chat_completions_attempt.go:363-377`、`backend/internal/gatewayhttp/chat_completions_attempt.go:444-467`、`backend/internal/gatewayhttp/chat_completions_dispatch.go:327-390`）。这正是 fallback 目标继续透传自己 `BindingID` 的接缝，但也意味着每次跨类必须验证 claim 不双扣。
- completions、embeddings、rerank、Gemini 已有不同程度的多 attempt loop；audio/images 目前只激活第一个 attempt（`backend/internal/completionshttp/handler.go:194-219`、`backend/internal/embeddingshttp/handler.go:172-186`、`backend/internal/rerankhttp/handler.go:175-189`、`backend/internal/geminihttp/generate_content.go:172-177`、`backend/internal/audiohttp/handler.go:207-212`、`backend/internal/imageshttp/handler.go:212-217`）。必须用同一状态机补齐，不能复制七套略有差异的 class 判断。

### 3.6 前端和 OpenAPI 已有字段但主动封死

- `types.ts` 三个 DTO 都把 fallback 标为“旧客户端兼容字段；当前界面不展示也不下发”（`frontend/src/features/routing/types.ts:6-21`、`frontend/src/features/routing/types.ts:29-55`）。
- `selection.ts` 的 edit/create form 没有 fallback，PATCH 也不回填；后端 PATCH 实际按整行默认覆盖，所以编辑其它字段会把既有非 `normal` 值重置为 `normal`（`frontend/src/features/routing/selection.ts:45-58`、`frontend/src/features/routing/selection.ts:75-105`、`frontend/src/features/routing/selection.ts:108-144`；后端默认见 `backend/internal/modelbindingadminhttp/routes.go:224-250`）。
- 当前测试反向断言 payload/DOM 中不得出现 fallback，实施时必须有意识翻转，而非新增一组与旧断言冲突的测试（`frontend/src/features/routing/selection.test.ts:32-47`、`frontend/src/features/routing/selection.test.ts:78-95`、`frontend/src/features/routing/BindingModal.test.tsx:21-53`、`frontend/src/features/routing/api.test.ts:25-45`）。
- OpenAPI 已列 enum，但没有运行语义，且 `selection_mode` 仍错误描述为“只存不执行”（`docs/openapi/openapi.yaml:16817-16832`、`docs/openapi/openapi.yaml:16841-16874`）。

## 4. 精确运行语义

### 4.1 候选编译

`DefaultRouter.Plan` 先把缺失/空 `FallbackClass` 规范化成 `normal`，再分成一个主 phase 和四个固定顺序的目标 phase。每个 phase 独立执行现有 Priority/Weight 算法；phase 之间绝不按 Priority/Weight比较。

建议本地契约形态：

- `PoolCandidateMeta.FallbackClass`：registry→router 必须透传。
- `AttemptPlan.FallbackClass`：每次 attempt 自描述，保证 `BindingID`、并发上限、选择模式和 class 属于同一 binding。
- `RoutePlan.Attempts` 与 `AttemptBudget`：继续只表示 `normal` 主 phase，normal-only 时结构、长度、reason、预算完全不变。
- `RoutePlan.FallbackPhases []FallbackPhasePlan`：固定 enum 顺序，每项包含 class、按类内规则选出的候选和 `AttemptBudget=1`。normal-only 时为 `nil`，避免默认 JSON/反射/审计形态变化。

第一版不把全部 fallback 候选展开成多 attempt；它只在 class 的最高 Priority 段按 Weight 选一个 binding。这样 Weight 决定跨请求分流，目标 pool 内 selector 仍会遍历/排队其账号；失败后不递归。未来要增加 fallback 子预算，必须依据观测数据单独提案，不能在本片顺手扩张。

### 4.2 主类耗尽的定义

“主类耗尽”不是“某个账号失败”，也不是“第一个 binding 返回 429”，而是以下状态机：

1. 按今天完全相同的 `normal` attempt 顺序和普通预算执行。
2. 任一 normal attempt 成功或排队后成功：立即结束，fallback phase 从未调用。
3. 排队仍有预算：继续等，不算耗尽；仅 queue timeout/overflow 才记为 `quota` 候选触发，客户端取消立即终态。
4. 每个失败先过“绝不降级”门；命中任一终态，立即返回，不等主类剩余 slot，也不跨类。
5. 可降级失败记录其精确目标 class，并继续今天允许的 normal attempt。只有 normal 普通 attempt 预算全部消费后，且**所有已记录的可降级失败都映射到同一个 class**，才算该 class 意义上的主类耗尽。
6. 若失败族混合（例如一个 quota、一个 5xx/manual），不猜测主因、不按最后一次覆盖，返回现有最终失败；不跨类。
7. 目标 class 不存在时，逐字节保留今天的响应/headers/retry 行为；特别是 binding 并发 429 仍是终态，不因为代码认识 `quota` 就全局翻转。
8. 目标存在且 retry budget 允许时，执行一次目标 attempt；它失败后返回该最终失败，或由现有外层 model fallback 决定是否换模型，但不再跨 binding class。

### 4.3 触发矩阵

| 当前信号 | 目标 class | 何时允许跨类 | 何时保持终态/原行为 |
| --- | --- | --- | --- |
| `ErrBindingConcurrencyLimited` | `quota` | 配置了 quota phase、未交付、normal 预算耗尽；先尝试其它 normal binding | 无 quota phase 时保持现有专用 429；fallback binding 自己也满时返回其 429 |
| `ErrBindingRateLimited` | `quota` | 同上 | 不得与 `ErrKeyRateLimited` 合并处理 |
| `ErrKeyRateLimited`、用户/tenant/token quota 拒绝 | 无 | 永不 | 换 binding 会绕过客户限额，必须原地拒绝 |
| `ErrNoSlotAvailable` / selector 无账号 | `quota` | typed cause 证明所有候选只因 health/cooldown/model-rate/rate-precheck/window/session/slot 容量失败；排队已不可用或已耗尽 | tenant/lifecycle/protocol/model/capability/credential/group policy 等静态不匹配不得降级 |
| `ErrAllChannelsDegraded` | `quota` | 所有 normal attempt 同类耗尽 | 无 quota target 时保持今天 503/Retry-After |
| queue timeout/overflow | `quota` | 已先执行主类等待预算 | queue cancelled、请求 ctx 取消不降级 |
| 规范化上游 429 | `quota` | 交付前、普通 retry 允许、主类耗尽 | 本地 key/user quota 429 不得冒充上游 429 |
| 本地 `GateFailureContextWindow` | `context_window` | class 本身作为运维容量 attestation；目标 attempt 不再次用原 canonical window 将自己挡掉，并写审计 | inbound body 太大、JSON/参数错误不降级；目标上游仍可独立拒绝并终止 |
| 规范化上游上下文过长 | `context_window` | 分类器能区分上下文过长与普通 4xx/body 413 | 泛化 `request_too_large` 不足以自动跨类，需补判别规则 |
| 规范化上游内容策略拒绝 | `safety` | HUAKAI 本地 moderation 已通过、交付前、规则明确为内容策略 | 本地 moderation、tenant policy、official-client gate、权限/认证 403 永不降级 |
| 上游 5xx、连接/首字节/上游超时、空响应 | `manual` | 现有普通 retry 本来就允许，且运维配置了 manual target | 本地 panic/config/credential resolve 错误不得用 fallback 掩盖 |
| 上游 401/凭据过期 | 无 | 只保留现有一次 auth refresh/同类换号子预算 | auth 子预算耗尽后终态，绝不跨 binding class |
| billing reserve/settle/abort、pricing、claim race/fingerprint conflict | 无 | 永不 | 钱账错误必须 fail-closed 或走既有恢复，不能发起另一上游 |
| 已向客户端交付任何字节 | 无 | 永不 | 防重复流、重复工具副作用和重复计费 |

### 4.4 fallback binding 的并发与限额

跨到目标 class 后，必须使用目标 `AttemptPlan` 的 `BindingID`、`MaxParallelRequests`、RPM/TPM、provider override 和 selection mode。当前 DB 硬闸在事务内以请求里的 `BindingID` 计数并返回绑定并发错误（`backend/internal/pool/dispatcher/slot_manager.go:81-127`）；dispatch 已从同一个 attempt 同时取 ID 与上限（`backend/internal/gatewayhttp/chat_completions_dispatch.go:503-535`）。

因此，normal binding 满不代表 quota binding 满；quota binding 也绝不共享、借用或绕过 normal 的 K。若 quota binding 自己达到 K，本次请求终止，不再去 `manual` 或另一个 class。释放/abort 后两条 binding 的派生计数都必须恢复，且任何 acquire 记录都应带实际目标 `BindingID`。

### 4.5 与模型 fallback 和 auth 子预算的顺序

固定顺序：

1. 当前模型的 `normal` 普通 attempts；
2. 若满足同类耗尽，当前模型的一次 binding-class attempt；
3. 若仍失败且现有 `modelfallback` 明确允许，再进入下一个模型；新模型从自己的 `normal` 开始；
4. auth refresh/换号仍是独立的一次子预算，但只能留在当前 binding class，耗尽后不得触发 class 转移。

现有模型 fallback 会在每个模型失败后按错误类选择下一模型并重建 registry/plan（`backend/internal/gatewayhttp/chat_completions_handler.go:471-508`），而现有 auth 子预算可在普通最后 slot 额外加一次同 pool attempt（`backend/internal/gatewayhttp/chat_completions_handler.go:552-603`）。实施时必须增加请求级指标与上限测试，证明最大尝试数为“每模型 normal 预算 + 至多 1 次 binding fallback + 至多 1 次既有 auth 子预算”，不能变成各 class 都复制 2/3 次。

## 5. 四层贯通实施清单

### 5.1 层一：Registry/管理写口

| 文件/接缝 | 现状 | 后续动作 |
| --- | --- | --- |
| `backend/internal/registry/registry.go` | 已读出 `FallbackClass`，注释仍说仅存储（`:69-82`） | 翻转注释为运行时元数据；空值只作兼容归一为 `normal` |
| `backend/internal/registry/postgres_registry.go` | 已映射数据库字段（`:171-192`） | 精确回归测试，生产 SQL 无需修改 |
| `backend/internal/registry/bindings_admin.go` | create/update 已持久化（`:193-249`） | 更新“仅存储”注释；不加迁移 |
| `backend/internal/modelbindingadminhttp/routes.go` | create/PATCH 已收、校验、默认 normal（`:65-68`、`:187-258`） | 保留 enum/default；更新 whole-row PATCH 契约说明；不臆造数据库当前无法验证的“真实上下文容量”写时检查 |

不建议本片强制“每个 model 始终至少一个 normal”写时事务校验，因为 effective window/分阶段配置会让单行 PATCH 难以正确判断。运行时遇到只有 fallback、没有 normal 的配置应 fail-closed 为 `no_primary_binding`，管理 UI 明示红色配置错误；绝不把任一 fallback 暗升为 primary。

### 5.2 层二：Router/Selector/状态机

| 文件/接缝 | 后续动作 |
| --- | --- |
| `backend/internal/router/route_plan.go` | 增加 class 类型、`PoolCandidateMeta.FallbackClass`、`AttemptPlan.FallbackClass` 与有序 `FallbackPhases`；主 `Attempts/AttemptBudget` 兼容不动 |
| `backend/internal/router/default_router.go` | 先按 class 分区，再在各区复用 Priority/Weight；normal-only 走原函数原 reason 原预算；非 normal 只生成一个目标 attempt；策略 stamp 仅在实际存在非 normal phase 时升级，normal-only 保留旧 stamp |
| `backend/internal/pool/router/types.go` | 扩展 `NoCapacityError` 的结构化耗尽原因/直方图并保持 `Unwrap`，确保既有 `errors.Is` 不变 |
| `backend/internal/pool/router/default_selector.go` | 在 filter/slot/wait 接缝收集 typed cause；`ErrNoEligibleAccount` 不再丢失所有 gate 差异；不让 slot full 伪装成静态模型不匹配 |
| `backend/internal/pool/router/routing_reason.go` | 复用已有 exclusion 计数，增加只读导出/归约 helper；审计写 class transition，不暴露原始上游文本 |
| 新的内聚职责包 `backend/internal/bindingfallback/` | 只放 HUAKAI 自身的 class 常量、trigger 归约、终态门和单次 phase 状态机；不得复制三镜命名/结构；无外部依赖 |

新增独立小包是为避免在 `gatewayhttp`、五个兼容 handler 和 Gemini 中各复制一套易漂移的错误映射；它属于中风险实现支持文件，实施稿需记录理由并通过 codebudget 门。

### 5.3 层三：Executor 与全协议

| 协议/文件 | 接缝 |
| --- | --- |
| chat/messages/responses：`backend/internal/gatewayhttp/chat_completions_attempt.go` | registry→router 映射 class；`AttemptPlan` 激活 class |
| chat 主循环：`backend/internal/gatewayhttp/chat_completions_handler.go` | 在 `shouldRetryAttemptFailure` 外增加“普通同类 retry / class transition / model fallback”三段状态；binding 429 仅在 quota phase 存在时内部化 |
| chat pool 分类：`backend/internal/gatewayhttp/chat_completions_handler.go`、`chat_completions_dispatch.go`、`chat_completions_queue_wait.go` | 分开 key rate 与 binding rate；保留先等待后 quota；取消终态；仅当模型 fallback 或 binding context phase 存在时启用 canonical context 预检，目标 context attempt 只跳过该 gate |
| completions：`backend/internal/completionshttp/route.go`、`handler.go`、`attempt.go`、`count_tokens.go` | 完整映射 Priority/Weight/selection/class；使用共享 phase 状态机，禁止直接写响应后再 fallback |
| embeddings：`backend/internal/embeddingshttp/route.go`、`handler.go`、`attempt.go` | 同上 |
| rerank：`backend/internal/rerankhttp/route.go`、`handler.go`、`attempt.go` | 同上 |
| Gemini：`backend/internal/geminihttp/generate_content.go` | router 映射、countTokens/生成路径的 class phase；已写 429 后不得再尝试 |
| audio/images：各自 `route.go`、`handler.go`、`attempt.go` | 先补成与契约一致的 bounded multi-attempt loop，再接 class；当前只用 `Attempts[0]`，不能宣称已贯通 |

所有协议必须满足：失败响应尚未写客户端才可转移；multipart/大 body 必须确认可重放；stream 首字节后禁转移；每次失败先 abort/release，再 reserve 目标 attempt；最终只 settle 一次。若某协议无法安全重放，显式保留一次 attempt 并把 binding fallback 标为 Mandatory Roadmap，不能假装已支持。

### 5.4 层四：OpenAPI 与前端运维 UI

| 文件 | 后续动作 |
| --- | --- |
| `docs/openapi/openapi.yaml` | 为 response/create/PATCH 的 `fallback_class` 写完整 enum 语义、默认、单次转移与终态边界；修正 `selection_mode` 的过期“未执行”描述；说明 PATCH 省略会回默认的现有契约 |
| `frontend/src/features/routing/types.ts` | 新增 `FallbackClass` union；把三处“旧兼容字段”翻转为运行时字段；响应缺值兼容成 `normal` |
| `frontend/src/features/routing/selection.ts` | 增加 `FALLBACK_CLASSES` label/hint、edit/create form 字段、hydrate、dirty compare、POST 和 PATCH；PATCH 始终回填当前 class，镜像 `max_parallel_requests` 的做法 |
| `frontend/src/features/routing/BindingModal.tsx` | 创建/编辑都提供 select；每项说明触发族；选择 `context_window` 时明确“管理员需确认目标 pool/model 真有更大窗口”，但不伪称系统已验证；明确本地审核不会被 safety 绕过 |
| `frontend/src/features/routing/RoutingPage.tsx` | 列表增加紧凑 class badge/筛选，使运维无需逐行编辑即可发现没有 normal 或错误分类；不展示任何 secret |
| `frontend/src/features/routing/*.test.*` | 翻转现有“不出现/不下发”断言，验证表单、POST、PATCH 回填、列表和租户 scope |

管理动作沿用现有 tenant scope、权限和 snapshot/audit；本片不增加危险的客户端 header 来强制 class，也不让普通 API 用户选择 `manual`。运维恢复流程是修正/禁用 binding、恢复 normal 容量并通过 Usage/route reason 核对，不是客户端绕过路由策略。

## 6. 判别测试设计（failing-test-first）

以下每条都包含 Normal、Failure、Operator recovery 和证红点，落地时同步新增到 `docs/11_ACCEPTANCE_TEST_MATRIX.md`，并关联现有 AT-GW-004、AT-POOL-001、AT-R1A-007。能力契约已要求 weighted/fallback/retry（`docs/02_CAPABILITY_CONTRACT.md:11-21`），真实场景要求账号禁用、耗尽、限流或不健康时 failover（`docs/08_REAL_WORLD_SCENARIOS.md:13-18`），且 BUG-GW-001 明确要求 retry 不得重复计费（`docs/09_BUG_PATTERN_LIBRARY.md:13-18`）。

| ID | 前置与判别 fixture | Normal / Failure / Recovery 与精确期望 | 必须证红的变异 |
| --- | --- | --- | --- |
| AT-BFC-001 主类活着绝不碰降级类 | normal A：Priority=100/Weight=1；quota Q：Priority=0/Weight=2,000,000,000；Q 的 mock 一调用就报错 | Normal：A 成功，selector/dispatcher 调用严格为 `[A]`，Q=0；Failure：把 A 变成 typed quota exhaustion 后才进入后续流程；Recovery：恢复 A 后下一请求仍只用 A | class 后于 Priority 排序、把非 normal 混进首选、成功后仍预热 fallback，任一变异都会调用 Q |
| AT-BFC-002 主类耗尽只切对应 class | 两个 normal attempts 都给 typed binding capacity；quota Q、context C、safety S、manual M 同时存在，其中错误类候选有更差 Priority，错误目标有巨权重 | Failure：所有 normal 用尽后恰好调用 Q 一次；C/S/M 均 0；Normal：任一 normal 第二次成功则 Q=0；Recovery：恢复 normal 容量后不再产生 transition audit | 取最低 Priority、把所有 non-normal 合并、按最后一个配置类、第一次 429 立刻越级、同类未耗尽就切换 |
| AT-BFC-003 无 fallback 配置逐字节兼容 | 固定随机种子；所有 metadata class 为 `normal`/空；保存实施前 RoutePlan、HTTP status/body/header、claim 次数与 routing reason golden | Normal/Failure 两条路径实施前后 bytes/DeepEqual 完全相同；无新 header、无 fallback phase、旧 router stamp 保持；Recovery 不适用 | 空 class 当 manual、normal-only 初始化空 slice 为非 nil、无配置也把 binding 429改可重试、全局升级 stamp |
| AT-BFC-004 并发闸叠加 | normal N 的 K=1 已占满；quota Q 的 K=1 空闲；两者用不同 BindingID；再做 Q 也占满的子用例 | Failure-1：N 的 429 不写客户端，normal 耗尽后 Q 成功；DB acquisition/usage 指向 Q BindingID。Failure-2：Q 也满，最终专用 429，绝不进入 M。Recovery：释放 Q 后成功，两个 binding 计数归零 | fallback 沿用 N 的 BindingID/K、把 K 当提示、Q 满后递归、abort 未释放、最终错误误写 503 |
| AT-BFC-005 排队优先 | normal 即时槽满但有 WaitPlan；一例等待后获得，一例 timeout，一例 client cancel | 获得：fallback=0；timeout/overflow：normal 预算耗尽后 quota=1；cancel：quota=0、终态；Recovery：释放 normal 槽后等待者成功 | 一看到 slot full 就降级、timeout 不降级、cancel 后仍发上游 |
| AT-BFC-006 不降级错误族 | 分别注入 key/user quota、billing reserve、pricing、claim race、local moderation、tenant policy、请求 JSON、auth 子预算耗尽、首字节后失败 | 每个用例目标 class 调用均为 0；status/code/Retry-After 与现有契约相同；billing/claim 不新增；Recovery：修复对应配置/凭据/余额后从 normal 成功 | 按 HTTP 429/403 粗分、把 auth 当 manual、先写响应再尝试、billing 失败仍发 provider |
| AT-BFC-007 四类映射 | 每类各有一个正确目标和三个诱饵；context 使用不同 override；safety 同时放一个本地 moderation 拒绝对照；manual 使用 5xx/timeout | 只有精确 upstream/context/capacity/transient 信号进入对应类；普通 400、body 413、权限 403、credential error 均不进入；Recovery：修复 primary 后不再降级 | `request_too_large` 全算 context、所有 403 算 safety、所有 5xx 包括本地配置错误算 manual |
| AT-BFC-008 Class/Priority/Weight/selection 叠加 | normal 与 quota 各含两 Priority 层；同层权重 1:9；目标 pool 内账号权重也为 1:9；固定大量样本 | class 不越层；类内低 Priority 永不抢高层；同层首 binding 命中约 10/90；`selection_mode` 只改变池内账号分布，不开关 binding Weight | Priority 放 class 外、全局权重洗序、selection_mode 关闭 binding weight、binding Weight误传成账号 Weight |
| AT-BFC-009 attempt/claim/审计 | normal 两次失败、quota 一次成功；带 claim/settler、usage、routing reason 记录器 | 失败 attempts 各 abort/release；最终仅一次 settle/charge；实际 Pool/Account/Binding 为 Q；审计含 from/to/trigger/attempt count，无 raw body/secret；Recovery 后无 transition | 重用已 abort claim、双 settle、目标 reserve 仍写 primary pool、reason 只有“fallback”无 class |
| AT-BFC-010 模型 fallback 组合上限 | 原模型 normal+binding target 都失败，下一个模型 normal 成功；另有 auth refresh 子预算 | 执行顺序严格按 §4.5；次数不超过公式；同一失败不会同时触发 binding 和 model 两个跳转；最终只结算成功模型 | class phase 之后回到原 normal、每 class 复制 3 次、auth 终态跨 class、模型与 binding fallback 同时递归 |
| AT-BFC-011 前端 round-trip | API 返回 `quota`；编辑 priority/maxparallel 不动 class；创建选择 context；tenant_id 存在 | PATCH 必含原 `quota`，POST 必含选择值，dirty compare/DOM/badge/筛选正确；后端错误可行动；Recovery：修正 override 后提交成功 | 沿用当前省略字段、表单 hydrate 默认覆盖、只改类型不发 payload、跨 tenant 请求 |
| AT-BFC-012 OpenAPI/后端契约 | 五 enum 正反表；POST/PATCH/get/list；非法、空、省略值 | 合法精确回显；非法 400；省略默认 normal；OpenAPI lint/client type 与实现一致；Recovery：改为合法值 | OpenAPI enum 与后端 map 漂移、PATCH 省略静默清值未记录、UI 自造第六值 |
| AT-BFC-013 全协议一致性 | chat/messages/responses/completions/embeddings/rerank/Gemini/audio/images 各一 normal 成功、一 quota fallback、一终态不降级 | 可安全重放的协议行为一致；stream 首字节后均停；不可重放协议 fail-closed 并明确 roadmap；Recovery 后 normal 恢复 | 只修 chat、audio/images 仍只取 `Attempts[0]`、某协议写完 429 后继续 |
| AT-BFC-014 运维恢复 | normal 全冷却、quota 接管；管理员查看列表/usage/audit，恢复 normal 并可禁用错误 target | 降级原因、源/目标 binding、最终账号可追踪；恢复后的新请求回 normal；管理变更有 audit，不修改历史 usage | silent fallback、只能看最终 pool、恢复后 sticky/缓存仍永久留 fallback、管理动作无 audit |

### 6.1 变异点总表

至少运行以下定向 mutation；只跑覆盖率不足以证明语义：

1. 删除 registry→router 的 `FallbackClass` 映射。
2. 把排序键改成 Priority 在 Class 之前。
3. 把四个非 normal 类合并成一个布尔 fallback。
4. 第一个 normal 失败即切类，不等待 normal 预算耗尽。
5. 混合 quota/manual 失败时按最后一次失败选类。
6. 无目标 class 时仍把 binding 429 改成 retryable。
7. 把 `ErrKeyRateLimited` 与 binding rate 一起映射 quota。
8. queue 返回 WaitPlan 时立即跨类，或 client cancel 后跨类。
9. fallback attempt 丢失/复用 primary 的 `BindingID` 或 `MaxParallelRequests`。
10. fallback binding 自己满后递归到其它 class。
11. normal-only 创建非 nil fallback phase、增加 header、改变 reason/stamp 或响应 bytes。
12. 把普通 400/body 413 映射 context，把权限/本地审核 403 映射 safety。
13. 把 credential/billing/local config 5xx 映射 manual。
14. 已交付首字节后仍进入 fallback。
15. fallback 前不 abort/release，或成功后 settle 两次。
16. `selection_mode` 错误地开关 binding Weight。
17. frontend PATCH 省略 `fallback_class`，把 quota 静默重置 normal。
18. 只接 chat，任一其它协议继续忽略 class。
19. class phase 为每个目标复制普通 2/3 attempt，突破子预算。
20. 审计只写通用 fallback，不写精确 class/trigger/source/target。

## 7. 风险、失败模式与缓解

| 风险 | 严重度 | 失败模式 | 缓解/门 |
| --- | --- | --- | --- |
| 默认行为翻转 | S1 | 旧绑定突然把 429/5xx 送去其它 pool | 只有显式非 normal target 存在才启用；normal-only golden bytes；动态 policy stamp |
| 限额绕过 | S0/S1 | key/user quota 429 被误当 provider quota | typed cause；key/binding rate 拆分；钱/客户限额表驱动负向测试 |
| 并发硬闸绕过 | S0/S1 | fallback 复用 primary ID 或不进事务硬闸 | `AttemptPlan` 同源携带 ID/K；真 PG 并发测试；目标满终止 |
| 计费/claim 重复 | S0 | 多 attempt 重复 reserve/settle、失败槽泄漏 | 复用既有 abort→re-reserve 生命周期；每失败零成本 abort、最终一次 settle；账本集成门 |
| auth/权限掩盖 | S1 | 坏凭据或权限错误被其它绑定“救活” | auth 只走既有一次子预算；终态门早于 class 归约 |
| safety 绕过 | S0/S1 | 本地 moderation/tenant policy 被替代上游绕过 | 本地安全门永远终态；仅已通过本地门的 upstream content-policy 可进 safety；默认审计和 feature flag 灰度 |
| context 假降级 | S1 | 同一 canonical window 再次挡住目标，或 body 413误分 | class 作为显式运维容量 attestation；typed context cause；目标 attempt 只跳过原 canonical-window gate；负向 400/413 测试 |
| 重试放大 | S1 | 普通 attempt × class × model × auth 相乘 | 每模型最多一次 binding-class 子预算；tenant retry budget；请求级计数指标与上限测试 |
| 全协议漂移 | S1 | chat 生效而 audio/images/embeddings 不同 | 共享纯状态机；协议矩阵；不可重放显式 fail-closed，不虚报支持 |
| PATCH 静默重置 | S1 | 编辑并发上限把 quota 重置 normal | UI 必须回填；API 契约写明 whole-row 语义；round-trip 测试 |
| 无主类配置 | S1 | fallback 被暗升 primary 或模型不可用无解释 | runtime fail-closed `no_primary_binding`；UI 红色诊断；不做隐式晋升 |
| 可观测性不足 | S2→发布前 S1 | 只见最终 pool，不知为何越级 | routing reason/usage 写精确 class、trigger、源/目标 binding、attempt count；列表 badge/filter |
| clean-room 污染 | S0 | implementer 按 LGPL/AGPL 结构翻译 | 先登记行为证据；implementer 不重读非 MIT 源；独立 reviewer 检查命名/结构/注释 |

## 8. 工作量拆分

| 工作包 | 预计工程时间 | 说明 |
| --- | ---: | --- |
| 契约、failing tests、证据登记 | 0.75～1.25 日 | 先锁 class/trigger/budget/审计，不写“!=坏值”弱断言 |
| Router plan 与兼容性 | 0.75～1.25 日 | phase、Priority/Weight、normal-only golden |
| Selector typed exhaustion | 1～1.5 日 | gate/slot/wait 原因归约，保持 `errors.Is` |
| chat executor、billing/auth 组合 | 1～1.5 日 | 风险最高；含 model fallback 与 auth 子预算 |
| 其它协议 | 1.5～2.5 日 | audio/images 需要先补 bounded loop；全协议负向门 |
| 管理 API、OpenAPI、前端 | 0.75～1.25 日 | 表单、PATCH 回填、badge/filter、契约 lint |
| 真 PG、race、全门、review 修复 | 0.75～1.25 日 | 并发、claim、codebudget、前后端构建 |

总计 6～9 工程日。若综合计划决定只做 chat 第一批，必须把其它协议标为 Mandatory Roadmap 并在 OpenAPI/UI 明示协议范围；我的推荐仍是一个发布 slice 内全协议闭合，因为同一 binding 字段跨协议语义不一致属于功能缩水。

## 9. Owner decision points

1. **`manual` 的含义**：推荐“运维手工配置的通用瞬态故障目标，运行时自动触发”，而不是新增客户端/管理员临时 header。后者会扩大权限与审计面，且不是现有字段能安全表达的能力。
2. **`safety` 的上线门**：推荐实现完整但默认 feature flag 关闭；只有本地 moderation 通过、上游精确内容策略类才允许灰度。不得删除该 class，也不得默认放开。
3. **发布原子性**：推荐全协议闭合后再宣称字段生效。若 Owner 选择 chat-first，OpenAPI、UI 和 parity matrix 必须显式标注其它协议 Mandatory Roadmap，audio/images 尤其不能被默认为支持。

此外，因为实施会触及 retry 与 claim 生命周期，属于钱路径相邻高风险工作；综合计划和 Owner 批准是动代码前的硬门。无需数据库迁移，也不应新增外部 runtime dependency。

## 10. Pre-execution checklist

1. 等 Claude 独立计划完成；并列列出 agreements/conflicts/gaps，禁止先读后改写成“独立稿”。
2. Owner 选择 §9 三个 decision point，形成无后缀综合计划。
3. 把本轮三镜行为和完整 SHA/许可证登记到 `docs/07_REFERENCE_EVIDENCE_LEDGER.md`；implementer lane 不再读取 LGPL/AGPL 源。
4. 确认当前工作树基线，隔离与本片无关的并发改动；不得覆盖 Owner/其它 agent 文件。
5. 检查 `router`、`pool/router`、各 HTTP 包 codebudget；共享状态机放新内聚包，不向超大包新增无关文件。
6. 先把 AT-BFC-001～014 写成 failing tests，并逐条说明 mutation；禁止 `t.Skip`、零值即 skip、只断言“不等于坏值”或 winner/loser fixture 无区别。
7. 锁定 normal-only golden：RoutePlan、HTTP body/header、claim 次数、routing reason 与 router policy stamp。
8. 锁定错误 taxonomy：binding/key/user/billing/upstream 429 分开；context/safety 的精确 classifier rule 有正反 fixture。
9. 锁定 attempt 上限、abort/re-reserve/settle 不变量和 stream delivery boundary。
10. 依次实施 core types → Router → Selector typed cause → chat → 其它协议 → 管理 API/OpenAPI → frontend，不交叉提交半接线状态。
11. 执行 Go 门前先按 Owner 指定设置 `GOCACHE=/home/ubuntu/HUAKAI/.gocache`、`GOTMPDIR=/home/ubuntu/HUAKAI/.gotmp`；每个小闭环运行 package tests，最终运行 Go unit、相关 integration_pg、race、frontend test/build、OpenAPI lint、codebudget。
12. Stage 仅本片文件，运行强制 Codex uncommitted review；S0/S1 修复后再 commit。跨钱路径集成提交用完整 reviewer lane。

## 11. Concrete execution order

1. **Spec/test commit**：证据 ledger、AT matrix、错误映射表、normal-only golden、failing tests；无生产行为。
2. **Router closed increment**：只让 plan 能表达 primary/fallback phases，normal-only 完全相同；executor 尚不消费时 feature flag/测试确保不可误启用。
3. **Selector closed increment**：typed exhaustion 保持 `errors.Is` 和现有 HTTP 行为；尚不跨类。
4. **Chat closed increment**：class transition、queue、binding 429、model/auth/billing 组合；先默认 flag off，再灰度 on。
5. **Protocol parity increment**：completions/embeddings/rerank/Gemini，再补 audio/images 安全重放 loop；任何不可重放端点 fail-closed。
6. **Control-plane increment**：后端语义校验、OpenAPI、前端表单/PATCH/badge/filter；打开运维配置入口前，运行时必须已接通。
7. **Release gate**：真 PG 并发与账本、race、全协议 E2E、mutation、clean-room reviewer、feature parity disposition；无 unresolved S0/S1 才可启用。

## 12. 完成定义

- `fallback_class` 从 DB/registry 到 router/attempt/executor/usage/UI/OpenAPI 不丢字段。
- `normal`、Priority、Weight、`selection_mode` 的组合符合 §1，且 normal-only 网络与 plan golden 逐字节不变。
- 主类成功、可等待或恢复时绝不访问 fallback；同类耗尽后只访问精确目标 class 一次。
- binding 并发/RPM 可触发 quota，但 key/user/billing quota 不能；fallback binding 受自己的事务内并发硬闸。
- billing/auth/本地安全/请求终态/已交付路径绝不跨类；失败 attempts 全部正确 abort/release，最终仅一次 settle。
- 所有可安全重放协议一致；不可重放协议显式 fail-closed/Mandatory Roadmap，无静默功能缩水。
- 运维能在列表、Usage/route reason 和 audit 中看见 class、trigger、源/目标 binding、attempt 数并完成恢复。
- 三镜行为只以本文件和 evidence ledger 的行为证据驱动；代码命名、结构与注释均为 HUAKAI 自主设计。

## 13. 本轮产出自检

已在 `2026-07-14T17:22:20Z` 执行 `git status --short`，实际输出：

```text
?? docs/process/plans/2026-07-14-binding-fallback-class-claude.md
?? docs/process/plans/2026-07-14-binding-fallback-class-codex.md
```

同时确认：

- `git diff --name-only` 为空，说明没有 tracked 文件改动。
- `git diff --no-index --check /dev/null docs/process/plans/2026-07-14-binding-fallback-class-codex.md` 通过，无 whitespace error。
- 我方只新增 `2026-07-14-binding-fallback-class-codex.md`。Claude 独立稿在本轮期间由其它 actor 并发出现；为保持独立性，我没有读取或修改它。
- 没有改生产代码、测试、迁移、OpenAPI 或其它文档。由于并发存在 Claude 独立稿，不能虚报“整个工作树只有 Codex 文件”；可以确认“Codex 本任务写集只有目标文件”。

---

Source files read:

- HUAKAI：`AGENTS.md`；`docs/RULES.md`；`docs/02_CAPABILITY_CONTRACT.md`；`docs/06_REFERENCE_PROJECTS.md`；`docs/07_REFERENCE_EVIDENCE_LEDGER.md`；`docs/08_REAL_WORLD_SCENARIOS.md`；`docs/09_BUG_PATTERN_LIBRARY.md`；`docs/11_ACCEPTANCE_TEST_MATRIX.md`；`docs/openapi/openapi.yaml`
- HUAKAI：`backend/internal/registry/registry.go`；`backend/internal/registry/postgres_registry.go`；`backend/internal/registry/bindings_admin.go`；`backend/internal/modelbindingadminhttp/routes.go`
- HUAKAI：`backend/internal/router/route_plan.go`；`backend/internal/router/default_router.go`；`backend/internal/router/default_router_weighted_test.go`
- HUAKAI：`backend/internal/pool/router/types.go`；`backend/internal/pool/router/default_selector.go`；`backend/internal/pool/router/gates.go`；`backend/internal/pool/router/context_window_gate.go`；`backend/internal/pool/router/routing_reason.go`；`backend/internal/pool/dispatcher/slot_manager.go`
- HUAKAI：`backend/internal/gateway/attempt_error.go`；`backend/internal/gateway/error_apply.go`；`backend/internal/modelfallback/resolver.go`
- HUAKAI：`backend/internal/gatewayhttp/chat_completions_attempt.go`；`backend/internal/gatewayhttp/chat_completions_dispatch.go`；`backend/internal/gatewayhttp/chat_completions_handler.go`；`backend/internal/gatewayhttp/chat_completions_queue_wait.go`；`backend/internal/gatewayhttp/chat_completions_dispatch_test.go`
- HUAKAI：`backend/internal/completionshttp/route.go`；`backend/internal/completionshttp/handler.go`；`backend/internal/completionshttp/attempt.go`；`backend/internal/completionshttp/count_tokens.go`
- HUAKAI：`backend/internal/embeddingshttp/route.go`；`backend/internal/embeddingshttp/handler.go`；`backend/internal/embeddingshttp/attempt.go`
- HUAKAI：`backend/internal/audiohttp/route.go`；`backend/internal/audiohttp/handler.go`；`backend/internal/audiohttp/attempt.go`
- HUAKAI：`backend/internal/imageshttp/route.go`；`backend/internal/imageshttp/handler.go`；`backend/internal/imageshttp/attempt.go`
- HUAKAI：`backend/internal/rerankhttp/route.go`；`backend/internal/rerankhttp/handler.go`；`backend/internal/rerankhttp/attempt.go`；`backend/internal/geminihttp/generate_content.go`
- HUAKAI：`frontend/src/features/routing/types.ts`；`frontend/src/features/routing/selection.ts`；`frontend/src/features/routing/BindingModal.tsx`；`frontend/src/features/routing/selection.test.ts`；`frontend/src/features/routing/BindingModal.test.tsx`；`frontend/src/features/routing/api.test.ts`
- `Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e`：`LICENSE`；`backend/internal/service/gateway_scheduling.go`；`backend/internal/service/account.go`；`backend/internal/service/gateway_multiplatform_test.go`
- `QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd`：`LICENSE`；`model/channel_cache.go`；`model/ability.go`；`controller/relay.go`；`service/channel_select.go`；`middleware/distributor.go`；`setting/operation_setting/status_code_ranges.go`
- `router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465`：`LICENSE`；`sdk/cliproxy/auth/selector.go`；`sdk/cliproxy/auth/selector_test.go`；`sdk/cliproxy/auth/scheduler.go`；`sdk/cliproxy/auth/conductor.go`；`sdk/cliproxy/auth/conductor_unauthorized_refresh_test.go`

Lane: specifier
Agent: GPT-5 Codex `/root`
UTC timestamp: 2026-07-14T17:22:20Z
