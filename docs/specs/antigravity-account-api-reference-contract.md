# Antigravity 账号转 API clean-room 行为合同

## 1. 合同元数据

| 字段 | 值 |
| --- | --- |
| Status | Reviewed — clean-room Pass；可作为实现输入，能力发布仍需代码与验收测试闭环 |
| Lane mode | Option C |
| Lane | specifier |
| Feature ID | `F-CRED-001`（账号获取与导入）、`F-AUTH-005`（凭据刷新）、`F-RATE-001`（上游冷却）、`F-POOL-001`（账号池）、`F-PROTO-002`（原生工具与协议转换）、`F-PROTO-001`（MCP 外部协议桥） |
| Parity 能力 ID | 同上；本合同是这些既有能力族的 Antigravity 专项增量，不另造重复编号 |
| 专项参考 | `lbjlaq/Antigravity-Manager` |
| 固定提交 | `80db5f2651a4bb7226a565a1637f110508b2ec2a` |
| 固定提交时间 | 2026-07-20T17:56:33+08:00 |
| 许可证 | CC BY-NC-SA 4.0；只允许 clean-room 行为证据，不允许代码复用或近似翻译 |
| 许可证证据编号 | `E-LIC-010`；CC BY-NC-SA 4.0，仅允许 clean-room 行为证据 |
| 默认三镜 | CLIProxyAPI + sub2api + new-api（强制参考清单） |

本合同是 **Antigravity 专项增量**。本轮没有重读默认三镜，也没有对三镜提出新增行为断言；仅列名不构成证据。综合实现继续以已发布的 [账号凭据管理](upstream-credential-management.md)、[账号池路由](pool-routing.md)、[上游冷却](rate-limiting.md)、[协议转换](protocol-translation.md) 和 [功能对照矩阵](../03_FEATURE_PARITY_MATRIX.md) 为三镜基线；本合同只补 Antigravity 专项行为、风险和验收，不替代这些基线。

### 可复现 clone provenance

- 本地只读克隆：`/home/ubuntu/refs/Antigravity-Manager-20260720`
- 可复现命令：`git clone --depth 1 https://github.com/lbjlaq/Antigravity-Manager.git /home/ubuntu/refs/Antigravity-Manager-20260720`
- 核验结果：浅克隆；分支 `main`；`HEAD` 与 `origin/main` 均为固定提交；工作树无修改。
- 仓库是否归档：Open Question。本轮只核验本地 Git 元数据，未以网页状态替代可复现源码事实。

## 2. 来源、隔离与证据口径

根许可证标题及条款明确为 CC BY-NC-SA 4.0。该许可证与 HUAKAI 的 MIT 商业目标不构成可直接复用边界，所以本文件只提取用户可观察结果、失败语义与验收方向。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:LICENSE:1-13,54-84`

证据标签含义：

- `Observed`：固定提交生产源码直接支持的可观察行为。
- `Inferred`：由多个已观察区域推导，仍需实现 lane 自行设计和验证。
- `Open Question`：源码或本轮权限不足，不能下结论。
- `HUAKAI design — not in source`：为保留用户结果而提出的独立增强，不宣称参考源码已有。

公开 Issue #3202、#2184、#1915、#1736、#3074、#1732、#1575、#1565 仅作为场景证据，全部 **未在固定提交运行复现**；Issue 的开关状态、根因猜测、补丁和载荷均不能证明固定提交行为。

### 近期变更线索（不作为当前能力的独立证明）

- 2026-07-19 发布的 v4.4.7 只声明 Linux 更新与进程退出修复，未声明模型工具调用或 MCP 新增能力；固定提交比该发布提交更新，但浅克隆没有可审历史，不能由此断言两轴没有其他变化。[Release v4.4.7](https://github.com/lbjlaq/Antigravity-Manager/releases/tag/v4.4.7)
- 2026-07-17 发布的 v4.4.6 声明跨协议的思考档位映射，是请求思考配置的变更线索；本合同仅以固定提交源码证明可观察行为。[Release v4.4.6](https://github.com/lbjlaq/Antigravity-Manager/releases/tag/v4.4.6)
- 2026-07-13 发布的 v4.4.2 声明远程搜索/正文读取 MCP 与嵌套工具链扩展；固定提交直接证明的是可开关远程转发和本地视觉 MCP server，不把发布说明中的其余机制写成已观察事实。[Release v4.4.2](https://github.com/lbjlaq/Antigravity-Manager/releases/tag/v4.4.2)
- 固定提交的本地历史只有一个浅克隆提交，近期提交核实范围为该提交元数据、生产源码与公开 Release；无法诚实形成逐提交演进结论，记入 Q-12。

## 3. KEEP：应保留的已观察用户结果

| Claim ID | 合同子项 | 分类、依据与紧邻锚点 | Acceptance ID |
| --- | --- | --- | --- |
| K-01 | 支持长期凭据导入与交互式授权；远程环境仍有人工完成路径。缺失必要刷新材料或用户取消时应明确失败。 | Observed。账号建立入口及远程完成分支可见。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/modules/account_service.rs:15-83,114-164` | AT-01 |
| K-02 | 身份成功后，即使辅助额度同步失败，账号事实仍可保存；消费者必须能区分“已建档”与“能力已知”。 | Observed（前半句）。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/modules/account_service.rs:45-83`；Inferred（必须区分状态），由该非原子结果与严格能力过滤共同推出。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:1245-1265` | AT-02 |
| K-03 | 项目归属可在账号生命周期的多个入口补齐；带归属请求遇权限拒绝时，存在一次不携带该归属的受限探测机会。该顺序是协议所需：先验证错误是否由可选归属上下文造成，避免直接把账号判为永久失效。 | Observed。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/modules/account_service.rs:27-58`；`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/upstream/client.rs:379-387,457-521` | AT-03 |
| K-04 | 账号能力按模型保留剩余比例、恢复时刻和调用限制；辅助时间窗摘要失败不抹掉主结果。 | Observed。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/modules/quota.rs:355-416` | AT-04 |
| K-05 | 模型目录合并静态入口、运营映射和账号动态能力；请求只进入明确具有目标模型能力事实的账号，未知能力不冒充可用。 | Observed。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/common/model_mapping.rs:140-199`；`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:1235-1265,3011-3031` | AT-05 |
| K-06 | 账号选择会消费订阅、目标模型额度、健康与恢复时刻等事实；本合同只保留“结果可解释且能力门先于偏好”的保证，不保留源码的比较顺序或阈值。 | Observed。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:1268-1319` | AT-06 |
| K-07 | 固定偏好和会话粘性不得越过禁用、目标模型冷却或额度保护；偏好失效时请求仍可寻找合格账号。 | Observed。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:1354-1418,1815-1836`；Inferred：粘性若越过这些门会把局部故障固化。 | AT-07 |
| K-08 | 同一账号的短期凭据并发刷新应合并，等待者消费已成功更新的事实，避免重复外部副作用。 | Observed。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:1421-1477,1838-1881` | AT-08 |
| K-09 | 实时 429 会形成目标模型范围的恢复事实；恢复信息缺失时仍能得到有界的保守等待，而不是立即重打。这里保留最终结果，不保留源码的来源优先链。 | Observed。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:2674-2719,2722-2814` | AT-09 |
| K-10 | 上游连接失败、超时和部分状态错误可在兼容端点间恢复；项目上下文导致的权限拒绝与普通账号禁用应产生不同结果。具体尝试顺序不是合同。 | Observed。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/upstream/client.rs:401-525` | AT-10 |
| K-11 | 明确失效的长期凭据会使账号退出服务；临时验证风险保留恢复时刻和用户可执行恢复入口。 | Observed。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/modules/account.rs:1708-1723`；`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:3060-3177` | AT-11 |
| K-12 | 流式转换保留文本、思考和工具块的协议语义，并在终止位置给出与工具使用和输出限制一致的结束结果及最终用量。协议必需顺序仅限“内容块先发生、终止事实后封口”，否则客户端无法判断块是否完整。 | Observed。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/mappers/openai/streaming.rs:11-103`；`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/mappers/claude/streaming.rs:484-536,679-828` | AT-12 |
| K-13 | 账号更新和删除会改变新请求可见的服务池，并清理会话、偏好、健康与冷却等派生状态。 | Observed。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:175-229` | AT-13 |
| K-14 | 运营者能够检索、过滤、导出和清理流量记录，并执行额度刷新、冷却清理、重新登录或账号启停等恢复动作。 | Observed。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/modules/proxy_db.rs:85-164,232-269`；`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:2310-2318` | AT-14 |
| K-15 | 模型工具调用入口接受工具声明、普通对话历史、模型产生的调用请求及客户端回注的执行结果；工具结果能按稳定调用标识关联回原调用，多轮输入不会被当作无关文本。 | Observed。请求转换和多轮交互事实均保留调用关联。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/mappers/openai/request.rs:115-180,356-418`；`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/mappers/openai/interaction_ledger.rs:31-74,108-174` | AT-28 |
| K-16 | 流式工具调用按独立调用项输出开始、参数增量、参数完成和调用项完成；并行调用保持彼此独立的稳定标识，最终完成结果仍包含待执行调用。 | Observed。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/handlers/openai.rs:4962-5108` | AT-29 |
| K-17 | 思考内容与普通文本分离输出；与工具调用关联的签名可从响应捕获并在后续同一会话回注。缺失或无效签名时存在受限降级/修复路径，不能静默把跨会话签名混用。 | Observed。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/mappers/openai/response.rs:78-179`；`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/mappers/openai/request.rs:228-273,356-385`；`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/mappers/claude/request.rs:1090-1120,1190-1331` | AT-30 |
| K-18 | 终止原因必须反映正常结束、输出上限、安全阻断或仍需工具执行；最终用量只在封口处形成唯一结算事实，流错误应以可解析错误事件结束而非伪装成功。 | Observed。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/mappers/openai/streaming.rs:269-339,367-369`；`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/mappers/openai/response.rs:285-326,355-440` | AT-31 |
| K-19 | MCP 是独立协议面：服务可整体关闭并按能力分别关闭；关闭时入口不可用。启用后，一类入口把客户端 MCP 流量转发到远端服务，另一类入口在本地完成握手、工具发现、调用和会话删除。 | Observed。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/config.rs:301-354`；`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/server.rs:430-515`；`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/handlers/mcp.rs:47-163,282-419` | AT-32 |
| K-20 | 本地 MCP server 先建立不透明会话，再允许发现和调用一组固定视觉工具；未知会话、未知方法、非法参数和工具执行失败分别返回可判别错误，显式删除后旧会话不再有效。 | Observed。会话只在当前进程内保存。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/zai_vision_mcp.rs:1-41`；`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/handlers/mcp.rs:165-234,236-395`；`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/zai_vision_tools.rs:174-287,449-456` | AT-33 |
| K-21 | MCP 路由与其他代理路由共用入口鉴权、IP 过滤和流量记录；鉴权关闭时也可匿名进入，用户令牌身份只用于识别与记账，未观察到 MCP 工具级授权或账号级工具绑定。 | Observed（共用中间件与鉴权模式）。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/server.rs:475-519`；`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/middleware/auth.rs:33-105,114-236`。Open Question（更细粒度边界），见 Q-09。 | AT-34 |

## 4. IMPROVE：HUAKAI 独立增强

以下均为 **HUAKAI design — not in source**，不应被理解为参考项目已有机制。

| Claim ID | 独立增强与保留结果 | 分类与设计依据 | Acceptance ID |
| --- | --- | --- | --- |
| I-01 | 以稳定的上游主体标识去重；重复导入更新既有主体并保留可追溯历史，避免一个真实主体被静默复制后重复消耗额度。 | HUAKAI design — not in source。参考源码只证明可建档，未证明主体唯一约束。 | AT-15 |
| I-02 | 能力快照带来源、新鲜度和“陈旧”状态；实时失败优先于陈旧展示，但旧快照仍可用于解释而非路由承诺。 | HUAKAI design — not in source。由 K-02、K-04、K-09 的不同事实时效推导。 | AT-16 |
| I-03 | 项目归属按租户与账号隔离，记录验证结果、撤销和到期；不接受未经授权的跨租户共享兜底。 | HUAKAI design — not in source。参考源码存在共享兜底，合法授权无法由源码证明。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:1491-1519` | AT-17 |
| I-04 | 账号选择纳入可验证的在途占用或共享并发上限，避免相同排序结果把并发压到单一账号。 | HUAKAI design — not in source。已读选择区域没有集群级在途预算证据。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:1268-1319` | AT-18 |
| I-05 | 所有端点与账号恢复共享请求级截止时间、尝试数和成本预算；客户端收到部分流式内容后禁止静默重放语义输出。 | HUAKAI design — not in source。K-10 只证明存在恢复尝试，不证明共享预算和部分输出安全。 | AT-19 |
| I-06 | 多副本共享冷却、禁用与恢复事实，使用带版本的条件写入；单进程内存状态不得成为全局真相。 | HUAKAI design — not in source。已读状态容器是进程内结构，跨进程一致性未证明。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:69-87` | AT-20 |
| I-07 | 热更新采用原子可恢复事实：半写、损坏或崩溃时保留最后完整快照并进入可操作隔离；删除与派生状态清理要么共同提交，要么可幂等补偿。 | HUAKAI design — not in source。K-13 证明清理结果，但未证明崩溃原子性。 | AT-21 |
| I-08 | 日志统一记录事件类型、结果、稳定错误码、严重级别和恢复关联；秘密、授权码、凭据片段与原始敏感体永不记录。 | HUAKAI design — not in source。参考源码存在直接记录访问凭据前缀的风险。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/modules/oauth.rs:414-440` | AT-22 |
| I-09 | 流式验收把终止、工具循环、思考上下文和最终用量作为一个原子结果面；部分成功必须明确标记用量是否完整。 | HUAKAI design — not in source。[Issue #1915](https://github.com/lbjlaq/Antigravity-Manager/issues/1915)、[Issue #1732](https://github.com/lbjlaq/Antigravity-Manager/issues/1732)、[Issue #1575](https://github.com/lbjlaq/Antigravity-Manager/issues/1575) 仅提供未复现的场景线索，不证明固定提交存在缺陷。 | AT-23 |
| I-10 | 稳定调用标识在协议转换、流式重组、重试和结果回注中保持一一对应；冲突、孤儿结果和重复结果必须明确拒绝或幂等收敛。 | HUAKAI design — not in source。Issue #3202 是第二轮签名/工具关联失败的场景线索，未在固定提交复现。[Issue #3202](https://github.com/lbjlaq/Antigravity-Manager/issues/3202) | AT-28～AT-30 |
| I-11 | 工具流、终止原因、思考签名与用量共同受请求级状态机约束；断流后不得既标成功又重复结算，恢复必须说明能否安全续接。 | HUAKAI design — not in source。[Issue #1915](https://github.com/lbjlaq/Antigravity-Manager/issues/1915) 是重复用量线索、[Issue #1732](https://github.com/lbjlaq/Antigravity-Manager/issues/1732) 是工具循环停滞线索、[Issue #1575](https://github.com/lbjlaq/Antigravity-Manager/issues/1575) 是思考中断线索；均未复现，不证明固定提交存在缺陷。 | AT-29～AT-31 |
| I-12 | MCP server/client、远程 MCP 和模型原生工具调用分别授权；按租户、账号、工具、远端与网络目标实施最小权限、调用预算和可撤销凭据。 | HUAKAI design — not in source。固定提交只证明共用入口鉴权，未证明细粒度授权。 | AT-32～AT-34 |
| I-13 | MCP 会话、禁用、重启与多副本恢复有持久化且带版本的事实；运营者能按租户查看发现、调用、失败、撤销和恢复日志，日志不保存原始敏感参数或远端凭据。 | HUAKAI design — not in source。固定提交的本地 MCP 会话为单进程内存状态。 | AT-33～AT-34 |

## 5. AVOID：不继承的行为与保留的替代结果

| Claim ID | 原用户结果 | 适用性 | 处置类别 | 证据与保留的替代结果 | Acceptance ID |
| --- | --- | --- | --- | --- | --- |
| A-01 | 池暂时耗尽时尽快恢复请求。 | 适用，但不能破坏真实冷却。 | AVOID + Implemented Better 候选 | Observed：源码存在全局清除冷却后再尝试的路径。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:1729-1811`。替代结果：保留最早恢复时间、受控等待和人工清除指定事实，不清除仍有效的全局冷却。 | AT-24 |
| A-02 | 项目发现失败时仍尽量可调用。 | 适用，但共享默认归属的授权与租户边界不明。 | AVOID + Safe Equivalent 候选 | Observed：存在共享兜底。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:1491-1519`。替代结果：账号级验证、无归属安全探测，或经授权且租户隔离的可撤销配置。 | AT-17 |
| A-03 | 账号刷新后尽快保存冷却，重启后仍可恢复。 | 适用，但直接覆盖本地事实不能证明原子性。 | AVOID + Implemented Better 候选 | Observed：限制事实以直接文件写入保存。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:2722-2776`。替代结果：原子提交、版本检查、崩溃恢复和幂等补偿。 | AT-21 |
| A-04 | 调试授权失败时识别凭据来源。 | 适用，但秘密片段不应进入日志。 | AVOID + Safe Equivalent 候选 | Observed：日志记录访问凭据前缀。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/modules/oauth.rs:414-440`。替代结果：记录不可逆指纹、凭据版本和刷新事件，不记录任何秘密片段。 | AT-22 |
| A-05 | 桌面端更新、窗口、托盘、多语言、IDE 本地切换、隧道和赞助展示。 | 不适用于本账号转 API 服务合同。 | Plugin / 不纳入核心专项增量 | 这些是桌面或分发结果，不是网关账号生命周期结果；如未来需要，以独立插件或客户端产品验收，不以删除网关能力换取。场景证据未在本轮生产源码覆盖。 | AT-25 |
| A-06 | 通过客户端或传输指纹提高某些上游兼容性。 | 合规与授权风险高，不应成为核心隐式行为。 | Feature Flag / Safe Equivalent 候选 | 本轮不把独特传输实现作为行为证据。替代结果：显式、可审计、经授权的上游身份与网络配置；关闭时返回清晰兼容性错误。 | AT-26 |
| A-07 | 复刻本地文件、桌面事件桥、固定端口、端点尝试序列或上游对象结构。 | 属于实现形状，不是用户结果。 | AVOID | clean-room 禁止继承这些形状。替代结果：K-01 至 K-21 与 I-01 至 I-13 定义的入口、恢复、状态和运维保证不缩水。 | AT-27 |
| A-08 | 用模糊名称自动改写模型选择的工具调用。 | 可提高容错，但误选工具会产生真实副作用。 | AVOID + Safe Equivalent 候选 | Observed：流式转换存在对客户端已注册工具的近似名称匹配。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/mappers/claude/streaming.rs:1085-1160`。替代结果：默认精确匹配；仅对无副作用或明确批准的别名启用确定性映射，歧义时拒绝并返回候选。 | AT-35 |
| A-09 | 远程 MCP 透明透传任意请求体并把流错误拼成普通响应数据。 | 保留远程 MCP 可用性，但不能混淆协议错误和正常内容。 | AVOID + Implemented Better 候选 | Observed：远程入口缓冲请求体并直接转发，响应流读取错误被转成普通字节。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/handlers/mcp.rs:47-119`。替代结果：限额流式转发、协议级错误、取消传播和可判别部分成功。 | AT-36 |

## 6. Open Questions 与发布阻断

| ID | 问题 | 分类与证据 |
| --- | --- | --- |
| Q-04 | 共享项目兜底是否获得授权并能按租户隔离？ | Open Question。固定提交源码不能证明授权。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:1491-1519` |
| Q-05 | 多副本能否一致处理冷却、会话和删除？ | Open Question。进程内结构不能证明跨副本收敛。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:69-87` |
| Q-06 | 删除账号时在途请求如何停止、结算或标记部分成功？ | Open Question。已读删除区域只证明新请求派生状态清理。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/token_manager.rs:211-229` |
| Q-07 | Issue 场景在固定提交是否仍可复现？ | Open Question。所有列出的 Issue 均只是场景证据、未复现，不能据此宣称缺陷存在或已修复。 |
| Q-08 | 是否存在通用 MCP client，可主动连接任意远端、协商能力、发现并注册工具？ | Open Question。完整搜索范围为 `src-tauri/src/**`、`src-tauri/Cargo.toml`、`package.json` 的 MCP 依赖、客户端构造、初始化、发现、注册与运行时调用；观察到的是两个固定远程反向代理和一个本地 server，没有足够证据把搜索无命中升级为“不支持”。 |
| Q-09 | MCP 是否按租户、账号、远端和工具实施独立权限隔离？ | Open Question。已读入口只证明共用代理鉴权与 IP 门；工具执行使用单一外部配置，未观察到本地 MCP 会话绑定用户身份。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/handlers/mcp.rs:47-88,282-368` |
| Q-10 | MCP 调用日志是否能区分发现、执行、取消、远端错误与人工恢复，并可靠脱敏？ | Open Question。共用监控能记录路由级请求，但本轮未观察到 MCP 专属事件分类或参数级脱敏合同。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/monitor.rs:82-176` |
| Q-11 | 本地 MCP 会话如何过期、限量，并在重启或多副本间恢复？ | Open Question。已读会话容器只证明创建、查询和删除；创建时间未形成可观察过期行为。`lbjlaq/Antigravity-Manager@80db5f2651a4bb7226a565a1637f110508b2ec2a:src-tauri/src/proxy/zai_vision_mcp.rs:1-41` |
| Q-12 | 固定提交前 30 天内两条轴的逐提交变化是什么？ | Open Question。本地仓库为单提交浅克隆，公开 Release 只提供版本级线索；未用缺失历史编造提交级结论。 |

## 7. 验收方向

| Acceptance ID | 区分性 fixture | 明确好结果 | 失败或恢复结果 | Mutation 点 |
| --- | --- | --- | --- | --- |
| AT-01 | 同一主体分别用长期凭据、交互授权和取消流程导入。 | 两种成功入口收敛为一个可用账号，取消不留半成品。 | 缺刷新材料返回稳定错误且可重新发起。 | 移除主体收敛或取消清理后测试变红。 |
| AT-02 | 身份成功，但额度同步返回超时。 | 账号已建档，能力状态为未知且不参与模型路由。 | 后续同步成功后才转为已知能力。 | 把未知能力当可用时测试变红。 |
| AT-03 | 已验证项目、错误项目和无项目三个请求。 | 正确项目一次成功；项目原因拒绝只触发一次有界安全探测。 | 探测仍失败时进入可操作状态，不永久误封账号。 | 删除尝试上限或错误分类后测试变红。 |
| AT-04 | 两个模型分别具有不同剩余比例、恢复时刻和限制。 | 每个模型只展示并消费自己的完整事实。 | 辅助窗口失败保留主额度并标记辅助事实未知。 | 复用另一模型额度后测试变红。 |
| AT-05 | 静态目录含模型 A，账号动态能力仅含模型 B。 | 目录可合并展示，但请求 A 不进入缺少 A 能力事实的账号。 | 动态能力未知时拒绝路由承诺并给出原因。 | 移除能力门后测试变红。 |
| AT-06 | 两账号分别在订阅、目标额度、健康和恢复时刻上优劣相反。 | 只有先通过全部资格门的账号进入偏好评分，并输出选择理由。 | 无合格账号时返回最早恢复事实。 | 将评分移到资格门前后测试变红。 |
| AT-07 | 粘性账号被禁用、冷却、额度耗尽或能力不兼容。 | 每种情况都打断粘性并选择其他合格账号。 | 无替代账号时返回具体阻断原因。 | 让粘性越过任一资格门后测试变红。 |
| AT-08 | 同一账号并发发起多次短期凭据刷新。 | 只有一次外部刷新，所有等待者读到同一新版本。 | 刷新失败时等待者获得同一分类错误且可重试。 | 移除合并或版本复读后测试变红。 |
| AT-09 | 目标模型返回带恢复时刻、带持续时间和无恢复信息的 429。 | 三种输入均形成该模型的有界恢复事实。 | 到期后可重新进入资格判断，不立即重打。 | 忽略模型维度或默认等待后测试变红。 |
| AT-10 | 端点连接失败、超时、普通权限拒绝和项目上下文拒绝。 | 仅可恢复类别在共享预算内切端点；项目拒绝单独分类。 | 预算耗尽返回最终可操作错误。 | 将所有状态一律重试或禁用后测试变红。 |
| AT-11 | 长期凭据永久失效与临时验证风险各一例。 | 永久失效退出服务；临时风险保留恢复时刻和恢复入口。 | 人工完成恢复后账号重新通过资格门。 | 合并两类状态或缺恢复入口后测试变红。 |
| AT-12 | 文本、思考、工具和用量交错，并随机分片、缺尾和中断。 | 块完整、终止原因合法、最终用量唯一。 | 中断以可解析错误或明确部分成功结束，不伪装完成。 | 删除封口状态或重复用量保护后测试变红。 |
| AT-13 | 热更新、禁用、删除后立即发起新请求并保留旧粘性。 | 新请求不再看到失效账号，派生状态完成清理。 | 清理中断可幂等补偿，旧粘性不能复活账号。 | 跳过任一派生状态清理后测试变红。 |
| AT-14 | 生成成功、429、刷新失败和人工恢复日志。 | 可按事件、结果、错误码和恢复关联筛选导出。 | 清理或恢复失败有明确状态且日志无秘密。 | 删除分类字段或脱敏后测试变红。 |
| AT-15 | 同一上游主体以不同入口、不同显示名重复导入。 | 更新唯一主体并保留版本历史，不新增第二个可调度账号。 | 多条既有匹配时显式冲突，要求人工消歧。 | 改为随便取首条或无唯一门后测试变红。 |
| AT-16 | 陈旧高额度快照与实时目标模型 429 同时存在。 | 实时阻断优先，陈旧快照仅用于解释。 | 新鲜同步成功且冷却到期后恢复路由。 | 让陈旧事实覆盖实时失败后测试变红。 |
| AT-17 | 两租户配置相同项目名，并注入共享兜底。 | 项目事实严格绑定租户和账号，未授权共享被拒绝。 | 撤销授权后立即失去资格并留下恢复日志。 | 删除租户条件或撤销检查后测试变红。 |
| AT-18 | 多个等价账号在高并发下同时可选。 | 在途上限生效，负载不会持续压到单一账号。 | 达上限后有界等待或选择其他账号。 | 移除共享占用事实后分布断言变红。 |
| AT-19 | 多端点、多账号恢复均可触发，且客户端已收到部分输出。 | 所有尝试共享截止、次数和成本预算；部分输出后不语义重放。 | 预算耗尽返回最终错误并释放资源。 | 给各层独立无限预算后测试变红。 |
| AT-20 | 两副本并发写入相反冷却与恢复事实。 | 版本条件写入产生唯一、可解释的最终状态。 | 陈旧写失败后复读最新事实。 | 移除版本条件后测试变红。 |
| AT-21 | 在状态写入、账号删除和派生清理中逐点注入崩溃。 | 保留最后完整事实或通过幂等补偿收敛。 | 半写状态进入可操作隔离，不参与路由。 | 改为非原子覆盖或非幂等补偿后测试变红。 |
| AT-22 | 凭据、授权码、请求体和可逆账号标识注入所有日志路径。 | 日志只含事件、稳定码、级别、恢复关联和不可逆指纹。 | 日志写失败不泄密并产生受控告警。 | 关闭任一脱敏规则后测试变红。 |
| AT-23 | 流在文本、思考、工具参数和终止前分别中断。 | 每种中断都明确用量完整性和是否可安全续接。 | 不可续接时禁止重复结算或透明重放。 | 去掉部分成功标记或幂等结算后测试变红。 |
| AT-24 | 所有账号分别处于不同到期时刻的有效冷却。 | 返回最早恢复时间或等待后只恢复到期账号。 | 人工只能清除指定事实，其他冷却保持。 | 全局清空冷却后测试变红。 |
| AT-25 | 核心服务不安装桌面、托盘、隧道与分发组件。 | 账号导入、调度、恢复和日志仍完整可用。 | 请求可选桌面能力时返回插件未安装。 | 将核心路径依赖桌面组件后测试变红。 |
| AT-26 | 兼容传输开关关闭、开启未授权、开启已授权三种配置。 | 默认关闭；未授权拒绝；已授权记录策略和操作日志。 | 兼容失败返回明确错误，不静默降级泄露身份。 | 默认开启或绕过授权后测试变红。 |
| AT-27 | 用不同存储、动态端口和不同目录布局部署，并逐项调用导入、刷新、冷却、调度、工具流、MCP 会话和运营恢复。 | 七类入口均按公开合同完成，返回值和持久事实不包含参考实现的路径、端口或对象名。 | 可选组件缺失时只返回对应能力不可用；账号导入、调度和恢复主链继续可用。 | 测试或生产接线绑定固定路径、固定端口、参考对象名或可选组件后测试变红。 |
| AT-28 | 两个并行工具调用含不同稳定 ID，并回注对应结果。 | 每个结果只关联原调用，多轮历史保持一一对应。 | 冲突、重复或孤儿结果被明确拒绝或幂等收敛。 | 仅按顺序而非 ID 关联后测试变红。 |
| AT-29 | 并行调用的参数增量任意交错并中途断流。 | 每项独立开始、累积、完成，终止仍保留待执行调用。 | 断流产生可解析错误，不拼接不同调用参数。 | 移除调用项隔离后测试变红。 |
| AT-30 | 同会话、跨会话、缺失和无效思考签名分别回注。 | 只复用同会话有效签名；缺失或无效走受限降级。 | 跨会话签名被拒绝且不污染后续历史。 | 移除会话绑定后测试变红。 |
| AT-31 | 正常结束、输出上限、安全阻断、待工具和流错误各一例。 | 终止原因准确，最终用量只形成一次。 | 流错误不能同时标成功；恢复沿同一结算幂等键。 | 合并终止原因或重复封口后测试变红。 |
| AT-32 | 原生工具开启，MCP 总开关和各 MCP 能力逐一关闭再恢复。 | MCP 入口按授权可达，原生工具调用不受 MCP 开关影响。 | 关闭入口返回明确不可用，恢复只开放获准能力。 | 共用一个开关或关闭后仍可达时测试变红。 |
| AT-33 | MCP 握手、发现、调用、非法参数、删除和删除后重放。 | 会话边界、工具结果和资源释放明确。 | 未知会话/方法/参数各有稳定错误，删除后旧会话失效。 | 移除会话校验或删除清理后测试变红。 |
| AT-34 | 两租户、两账号、不同工具与远端权限访问同一入口。 | 未授权工具不可发现也不可调用，日志和凭据不跨租户。 | 撤权后新调用立即拒绝，进行中调用按合同终止。 | 删除租户、工具或远端任一权限条件后测试变红。 |
| AT-35 | 两个相似工具名，其中一个具有副作用。 | 默认精确匹配；歧义拒绝并返回候选，不误执行。 | 只有明确批准的无副作用别名可确定性映射。 | 启用模糊自动选择后测试变红。 |
| AT-36 | 超大 MCP 请求、远端断流和客户端取消。 | 请求受预算约束，取消传播并释放资源。 | 远端断流返回协议错误或明确部分成功，不伪装正常内容。 | 移除大小限制、取消传播或错误分类后测试变红。 |

所有测试必须断言具体状态、事实与副作用；只断言 HTTP 状态不算覆盖。

## 8. Coverage matrix

| Claim ID | Classification | Source / design | Acceptance ID |
| --- | --- | --- | --- |
| K-01 | Observed | 固定提交源码 | AT-01 |
| K-02 | Observed + Inferred | 固定提交源码及跨区域推导 | AT-02 |
| K-03 | Observed | 固定提交源码 | AT-03 |
| K-04 | Observed | 固定提交源码 | AT-04 |
| K-05 | Observed | 固定提交源码 | AT-05 |
| K-06 | Observed | 固定提交源码 | AT-06 |
| K-07 | Observed + Inferred | 固定提交源码及失败放大推导 | AT-07 |
| K-08 | Observed | 固定提交源码 | AT-08 |
| K-09 | Observed | 固定提交源码 | AT-09 |
| K-10 | Observed | 固定提交源码 | AT-10 |
| K-11 | Observed | 固定提交源码 | AT-11 |
| K-12 | Observed | 固定提交源码与协议终止因果 | AT-12 |
| K-13 | Observed | 固定提交源码 | AT-13 |
| K-14 | Observed | 固定提交源码 | AT-14 |
| K-15 | Observed | 工具声明与多轮调用关联 | AT-28 |
| K-16 | Observed | 流式工具事件与稳定标识 | AT-29 |
| K-17 | Observed | 思考、签名捕获与回注 | AT-30 |
| K-18 | Observed | 终止、错误与最终用量 | AT-31 |
| K-19 | Observed | 远程 MCP 与本地 MCP server 独立入口 | AT-32 |
| K-20 | Observed | MCP 发现、调用与会话生命周期 | AT-33 |
| K-21 | Observed + Open Question | 共用入口鉴权；细粒度边界待证 | AT-34 |
| I-01 | HUAKAI design — not in source | 独立主体去重设计 | AT-15 |
| I-02 | HUAKAI design — not in source | 独立事实新鲜度设计 | AT-16 |
| I-03 | HUAKAI design — not in source | 独立租户隔离设计 | AT-17 |
| I-04 | HUAKAI design — not in source | 独立共享并发设计 | AT-18 |
| I-05 | HUAKAI design — not in source | 独立请求预算设计 | AT-19 |
| I-06 | HUAKAI design — not in source | 独立多副本一致性设计 | AT-20 |
| I-07 | HUAKAI design — not in source | 独立原子恢复设计 | AT-21 |
| I-08 | HUAKAI design — not in source | 独立日志安全设计 | AT-22 |
| I-09 | HUAKAI design — not in source | 独立流式结算设计 | AT-23 |
| I-10 | HUAKAI design — not in source | 独立调用关联与幂等设计 | AT-28～AT-30 |
| I-11 | HUAKAI design — not in source | 独立工具流状态机设计 | AT-29～AT-31 |
| I-12 | HUAKAI design — not in source | 独立 MCP 最小权限设计 | AT-32～AT-34 |
| I-13 | HUAKAI design — not in source | 独立 MCP 恢复与日志设计 | AT-33～AT-34 |
| A-01 | Observed anti-pattern + 替代设计 | 固定提交源码与受控恢复结果 | AT-24 |
| A-02 | Observed anti-pattern + 替代设计 | 固定提交源码与租户安全等价 | AT-17 |
| A-03 | Observed anti-pattern + 替代设计 | 固定提交源码与原子恢复等价 | AT-21 |
| A-04 | Observed anti-pattern + 替代设计 | 固定提交源码与脱敏诊断等价 | AT-22 |
| A-05 | 不适用 + 保留可选结果 | 桌面产品边界 | AT-25 |
| A-06 | 风险隔离 + Safe Equivalent | 合规网络合同 | AT-26 |
| A-07 | 实现形状排除 + 保留行为 | clean-room 设计 | AT-27 |
| A-08 | Observed anti-pattern + Safe Equivalent | 工具选择安全边界 | AT-35 |
| A-09 | Observed anti-pattern + 替代设计 | 远程 MCP 错误与资源边界 | AT-36 |
| Q-04 | Open Question | 授权事实无法由源码证明 | AT-17 |
| Q-05 | Open Question | 跨副本事实无法由源码证明 | AT-20 |
| Q-06 | Open Question | 在途删除语义缺口 | AT-13、AT-21 |
| Q-07 | Open Question | Issue 未复现 | AT-09、AT-12、AT-23 |
| Q-08 | Open Question | 通用 MCP client 调用链未证实 | 阻能力断言 |
| Q-09 | Open Question | MCP 细粒度权限与边界未证实 | AT-34 |
| Q-10 | Open Question | MCP 专属日志与脱敏未证实 | AT-34 |
| Q-11 | Open Question | MCP 会话过期与多副本恢复未证实 | AT-33 |
| Q-12 | Open Question | 浅克隆无法证明逐提交演进 | 不阻专项合同，阻历史断言 |

## 9. Source Coverage Proof 与 clean-room 自检

- 已读生产区域覆盖：账号入口与授权、身份与订阅、项目发现、额度、模型归一、能力过滤、选择与粘性、并发刷新、冷却、端点恢复、模型原生工具请求/流式事件/结果回注/思考签名/终止/用量、远程 MCP 入口、本地 MCP server 的发现/调用/会话、共用鉴权与日志、热更新/删除和人工恢复。
- MCP client 搜索覆盖：`src-tauri/src/**`、`src-tauri/Cargo.toml`、`package.json` 中的入口、依赖、注册、初始化、发现与运行时调用关键词；结果只支撑 Q-08，未以搜索无命中断言不存在。
- 统计口径：KEEP 21 项均含 `Observed`（K-02、K-07 另含 `Inferred`，K-21 另含细粒度 `Open Question`）；HUAKAI 独立增强 13 项；AVOID 9 项；Open Question 9 项。Issue/Release 线索不计入 `Observed`。
- 每个非平凡参考行为都在 KEEP/AVOID 中独立标注 `Observed` 或 `Inferred` 并紧邻固定提交锚点；独立增强均标注 `HUAKAI design — not in source`。
- 正文没有复制函数名、字段名、配置常量、schema、注释、测试或算法步骤；仅保留协议正确性所必需的因果顺序并说明理由。
- 本 lane 未读取 HUAKAI backend/frontend、diff、schema、内部实现文档、parity matrix 或验收矩阵，也未修改其他文件。
- clean-room 自检：内容污染、内部编号归属和独立 reviewer 均为 Pass；Q-04～Q-12 继续限制对应能力断言，但不阻本合同作为独立实现输入。

Source files read: LICENSE; package.json; src-tauri/Cargo.toml; src-tauri/src/modules/account.rs; src-tauri/src/modules/account_service.rs; src-tauri/src/modules/oauth.rs; src-tauri/src/modules/quota.rs; src-tauri/src/modules/proxy_db.rs; src-tauri/src/proxy/common/model_mapping.rs; src-tauri/src/proxy/config.rs; src-tauri/src/proxy/handlers/mcp.rs; src-tauri/src/proxy/handlers/openai.rs; src-tauri/src/proxy/mappers/claude/request.rs; src-tauri/src/proxy/mappers/claude/response.rs; src-tauri/src/proxy/mappers/claude/streaming.rs; src-tauri/src/proxy/mappers/gemini/wrapper.rs; src-tauri/src/proxy/mappers/openai/collector.rs; src-tauri/src/proxy/mappers/openai/interaction_ledger.rs; src-tauri/src/proxy/mappers/openai/request.rs; src-tauri/src/proxy/mappers/openai/response.rs; src-tauri/src/proxy/mappers/openai/streaming.rs; src-tauri/src/proxy/middleware/auth.rs; src-tauri/src/proxy/middleware/monitor.rs; src-tauri/src/proxy/monitor.rs; src-tauri/src/proxy/server.rs; src-tauri/src/proxy/token_manager.rs; src-tauri/src/proxy/upstream/client.rs; src-tauri/src/proxy/zai_vision_mcp.rs; src-tauri/src/proxy/zai_vision_tools.rs
Lane: specifier
Agent: GPT-5 Codex /root（新的独立 specifier session；环境未暴露可核验 session ID）
UTC timestamp: 2026-07-20T16:16:46Z
