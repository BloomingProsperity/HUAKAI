# 2026-07-16 Sub2 账号系统完整生产逻辑（隔离 specifier）

## 元数据

| 项目 | 值 |
| --- | --- |
| Lane | specifier |
| 主对象 | Sub2API |
| 补充对象 | CLIProxyAPI、New API；可达默认分支证据另见 `2026-07-16-reference-default-branch-account-runtime-supplement.md` |
| 本地可读版本 | Sub2API `09c6c6d74050cf49ed2fb864be6c11647798ef53`；CLIProxyAPI `09da52ad509e2c18e7b9540db3b98c2214c280aa`；New API `a63364d156cf2a64f1c3d1ee4923d73d5f3222a1` |
| 版本限定 | Sub2 行为来自本 artifact 的隔离源码深读；CLIProxyAPI 和 New API 的补充行为由独立 artifact 在执行时核实为远端 `main` 可达 SHA |
| Observed regions | 38 个 Sub2 直接观察区域 + 21 个独立默认分支补充观察区域 |
| Inferences | 7 |
| Open questions | 12 |
| 事实纪律 | “Observed”表示直接读到源码或测试；“Inferred”只由已观察区域组合推出；未读到或无法证明的内容写入 Open Questions |

## A. 一页大白话全链

### A.1 结论

**Observed：Sub2 的“账号系统”不是一个账号 CRUD 模块，而是一条贯穿管理面、凭据面、调度面、执行面、计量面和恢复面的生产链。** 一个账号从创建或授权进入系统后，会同时携带平台、凭据形态、代理、并发、优先级、分组、过期策略和调度开关；进入请求热路径前，还要经过活动状态、人工调度开关、过期、过载、限流、临时冷却及本地额度等多层门。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_service.go:149` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account.go:20` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account.go:148`

**Observed：创建只是起点。** 管理员可以直接录入 OAuth、setup token、API key、上游兼容端点、云签名凭据或 service account；也可以走授权码入口、已有会话导入、批量会话导入、单独的长期访问令牌入口或特定 SSO 转换入口。批量导入会逐项解析、去重、决定创建/更新/跳过/失败，并保留可续期账号原有的刷新材料。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:108` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_codex_import.go:117` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_codex_import.go:159`

**Observed：凭据不是“读出来直接用”。** 管理响应会遮蔽 access token、refresh token、API key、session key、cookie、云密钥和私钥类值；编辑时若前端未回传这些敏感子项，服务端保留旧值，只有明确提供时才旋转。某些子账号不拥有凭据，只借用母账号凭据，并且持久化汇聚点会拒绝把完整凭据写入子账号。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_credentials_redact.go:3` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_credentials_redact.go:29` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_credentials_persistence.go:12`

**Observed：OAuth 既有请求时准备，也有后台续期。** 后台按游标分页扫描候选账号，并按平台设并发、QPS、超时、重试和连续失败熔断；成功后更新凭据、清理旧 token 缓存、同步调度快照和解除临时阻断。凭据中存在版本标记，请求线程准备把 token 放进缓存前会再次读取持久层，避免把后台刚刷新的新 token 被旧账号快照覆盖。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_refresh_service.go:20` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_refresh_service.go:55` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_cache_invalidator.go:71`

**Observed：调度首先是候选门，其次才是排序。** 默认门会排除非活动、人工停调度、到期、过载、全局限流、临时冷却和本地 quota 用尽的账号；随后按分组、平台、模型映射、端点能力、传输能力、账号类型限制等继续筛选。OpenAI/Codex 路径还有前一响应粘性、会话粘性和负载评分三层选择，评分可综合优先级、当前负载、排队、近期错误率、首 token 延迟、额度重置距离、剩余额度、上游成本和粘性加分。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account.go:148` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:368` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:924`

**Observed：选中账号后仍不等于可以发请求。** 系统还会尝试占用并发槽，准备或刷新 token，解析代理，选择传输形态，注入账号身份，执行协议转换和按账号模型映射重写。跨账号 fallback 时会从原始请求体重新构建下一账号的上游请求，避免沿用前一个账号的模型映射或解析缓存。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:511` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_failover_cached_body_test.go:26`

**Observed：失败会反向改变账号。** 401 对可续期 OAuth 账号通常先失效 token 缓存并进入短期不可调度，让后台或请求时刷新自愈；缺少刷新材料、明确撤销、工作区停用、余额耗尽或身份验证失败等会进入错误/停调度状态；429 会写入账号级或模型级恢复窗口；529 会写过载窗口；可配置错误规则还可写临时冷却。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:170` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:221` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:271`

**Observed：最终计费拿的是最终成功账号和最终上游结果。** 使用记录包含账号、用户、API key、分组、请求模型、最终上游模型、模型映射链、各类 token 桶、缓存创建/读取 token、请求类型、传输模式、时延和首 token 时延；成本计算后再结合分组倍率、账号倍率、订阅或余额路径结算。简单模式只记账不扣费，正常模式将结算任务异步提交，并保留同步回退或丢弃策略的运行指标。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_gateway_usage.go:113` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_gateway_usage.go:233` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/usage_record_worker_pool.go:46`

**Observed：管理恢复和后台协同不是附属品。** 管理端可看实时并发、窗口费用、活跃 session、RPM 和调度分数，并提供测试、刷新、重新授权、清错误、清限流/冷却、重置 quota、批量修改和调度启停。后台还维护账号到期、token 续期、调度快照、outbox、水位、定时测试、用量清理和异步记账；启动时重建调度快照，关闭时等待各 worker 收尾。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:181` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/scheduler_snapshot_service.go:170` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/cmd/server/wire.go:150`

### A.2 真正协同与局部模块

**Observed：真正协同的主链**是：

1. 管理入口写账号与分组关系；
2. 持久层同时发出调度变更事件；
3. 调度 worker 把可调度账号发布为 Redis 快照；
4. 请求按分组/模型/能力取候选并占并发槽；
5. token provider 取得可用凭据，必要时刷新；
6. 网关执行协议和模型转换后调用上游；
7. 失败分类器回写冷却、限流、错误或过载；
8. 成功结果把最终账号和 token 用量交给结算与日志；
9. 后台用量探测、定时测试和管理员操作再修正账号状态。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/repository/scheduler_outbox_repo.go:181` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/scheduler_snapshot_service.go:345` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_gateway_usage.go:249`

**Observed：局部模块**包括某一平台独有的授权入口、额度解析器、隐私设置、模型能力探针、特定传输、子账号维度或特殊账单头解析。它们只有在对应账号类型或请求能力命中时参与主链，并不构成独立账号系统。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_refresh_service.go:121` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:289`

**Inferred 1：Sub2 的核心设计中心不是“provider adapter”，而是“可被持续修正的账号运行态”。** 这是由统一账号门、统一调度快照、统一失败回写、统一使用归因和平台局部扩展共同推出的结论。

## B. 功能域总账

| 功能域 | Sub2 主行为（Observed） | 协同关系 |
| --- | --- | --- |
| 创建与类型 | 支持 OAuth、setup token、API key、上游兼容、云签名和 service account；创建时带分组、代理、并发、优先级、到期和自动暂停策略。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:108` | 创建后立即进入分组、调度快照和后台到期管理。 |
| 授权入口 | 授权 URL 与 code exchange 使用短期会话标识、state、redirect URI 和可选代理；兑换结果可直接建账号。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/openai_oauth_handler.go:40` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/openai_oauth_handler.go:69` | 临时授权会话只服务于交换，不替代持久账号凭据。 |
| 导入与批量导入 | 支持原始 token、JSON、数组和混合逐行输入；逐项去重、创建/更新、错误隔离和汇总。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_codex_import.go:144` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_codex_import.go:190` | 更新后主动清 token 缓存，避免继续使用导入前凭据。 |
| 敏感边界 | 响应遮蔽 token、key、cookie 和私钥；全对象编辑时，未显式回传的敏感值继续保留。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_credentials_redact.go:3` | 管理 UI 不需要也不应重新获得完整秘密。 |
| 凭据持久化 | 统一写入口更新账号凭据；凭据借用型子账号拒绝持有完整秘密。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_credentials_persistence.go:12` | 续期、补全和导入共享同一安全边界。 |
| token 版本与缓存 | 不同平台使用各自缓存键；刷新后删除可能的旧键；请求线程比较持久层版本，拒绝把旧 token 重新缓存。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_cache_invalidator.go:23` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_cache_invalidator.go:78` | 解决后台刷新与请求并发竞态。 |
| 自动续期 | 候选分页、平台注册表、平台并发/QPS、尝试超时、周期超时、重试和失败阈值。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_refresh_service.go:20` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_refresh_service.go:132` | 刷新结果同时影响缓存、调度和临时阻断。 |
| 手动续期与重新授权 | 管理端可对单账号刷新；无法续期或明确撤销的账号需重新授权。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/grok_oauth_handler.go:128` | 手动入口和后台入口最终写回同一账号。 |
| 旧 token 降级 | access-token-only 导入到已有可续期账号时保留旧 refresh token；401 路径不把请求开始时的旧凭据快照整列写回。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_codex_import.go:265` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:290` | 避免一次临时导入或并发 401 破坏长期可续期能力。 |
| 状态与调度门 | 活动状态、人工开关、到期、过载、限流、临时冷却、本地 quota 都能阻止调度。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account.go:148` | 所有 provider 先经过共同门，再进局部分叉。 |
| 分组与模型 | 候选可按分组和平台查询；模型映射、模型级限流、端点能力、传输能力及 OAuth-only 分组继续收窄。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_service.go:86` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_service.go:231` | 分组既影响准入，也影响计费倍率和模型集合。 |
| 优先级与评分 | 基础优先级可叠加负载、排队、错误率、TTFT、重置时间、额度余量、上游成本和粘性。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:879` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:924` | 评分只对已经通过硬门的候选排序。 |
| 粘性 | 前一响应绑定优先于会话粘性；粘性账号失效、不匹配、传输不兼容或运行表现恶化时会清理或逃逸。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:379` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:480` | 粘性不是绕过健康检查的永久绑定。 |
| 并发/RPM/session | 账号级并发槽是请求前硬门；Anthropic 类账号还暴露活动 session、RPM 和窗口费用给管理端。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:511` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:218` | 管理可见性与调度门使用同一运行数据。 |
| 请求前准备 | 最终账号确定后准备 token、代理、身份头、传输和协议/模型转换。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_gateway_service.go:383` | 准备失败可以在不发送业务请求时触发换号或恢复。 |
| 单账号 retry | 部分短暂失败在同账号内按预算和退避重试，相关次数、退避时长和耗尽均有指标。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_gateway_service.go:310` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_gateway_service.go:924` | 同账号 retry 先于或区别于跨账号 fallback。 |
| 跨账号 fallback | 错误对象带失败阶段、范围和下一步语义；下一账号重新应用自己的模型映射和身份。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/gateway_service.go:564` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_failover_cached_body_test.go:96` | 防止把账号 A 的转换结果泄漏到账号 B。 |
| 错误回写 | 400/401/402/403/429/529 和自定义错误策略进入不同状态；OAuth 401 的可恢复与不可恢复情况分开。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:221` | 状态回写立即影响缓存和后续候选。 |
| 用量与窗口 | 支持本地窗口统计、上游主动探测、错误负缓存、单飞抑制、五小时/七天/日/月、逐模型额度和余额摘要。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:87` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:183` | 同一管理视图融合被动响应头、本地日志和主动探测。 |
| 本地 quota 预检 | Gemini 类账号可在发送前按日请求数和分钟请求数预检；本地预检命中不会伪装成真实上游 429。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:389` | 减少无意义上游调用，同时保持状态语义真实性。 |
| 模型发现/白名单/迁移 | 账号可持模型映射和白名单，主动探针能更新能力；Antigravity 用量还携带废弃模型迁移信息。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:198` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:226` | 模型能力既参与列表展示，也参与调度准入。 |
| 使用归因与计费 | 日志明确保存最终账号、最终模型、请求模型、映射链、缓存 token、成本分解、倍率和渠道。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_gateway_usage.go:249` | fallback 后只对最终实际执行结果结算。 |
| 管理恢复 | 列表、详情、实时并发、测试、刷新、重新授权、清状态、重置 quota、批量修改和调度启停形成闭环。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:147` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:209` | 管理动作需使 DB、token 缓存和调度快照一致。 |
| 后台协调 | 调度采用快照、变更 outbox、水位、分桶写入 fencing、生命周期租约、清理锁和周期全量重建。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/scheduler_cache.go:76` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/scheduler_snapshot_service.go:299` | 多副本最终收敛，旧 writer 不能复活已退役分桶。 |
| 观测与审计 | 记录调度选择层、候选数、Top-K、延迟、负载偏斜、粘性命中、换号率、retry 指标、worker 丢弃/同步回退和系统错误日志。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:88` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/usage_record_worker_pool.go:337` | 观测项直接对应恢复和容量判断。 |

## C. 状态机

### C.1 主状态

**Observed：账号至少同时存在四组正交状态，而不是单一枚举。**

1. **管理状态**：活动、停用、错误；
2. **人工调度状态**：允许或禁止进入候选；
3. **时间状态**：业务到期、全局限流恢复点、过载恢复点、临时不可调度恢复点、session 窗口；
4. **能力状态**：逐模型限流、额度用尽、需要验证、被封禁、需要重新授权、传输或端点能力不可用。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account.go:37` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account.go:45` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:229`

### C.2 关键迁移

| 事件 | 迁移（Observed） | 恢复入口 |
| --- | --- | --- |
| 创建成功 | 默认活动；是否可调度由创建逻辑和管理策略决定。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_service.go:207` | 管理测试后启用调度。 |
| 到期 | 开启自动暂停时退出候选。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account.go:152` | 延长到期时间或关闭自动暂停。 |
| OAuth 401，可续期 | 保持活动语义但短期不可调度，清 token 缓存，等待刷新。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:271` | 自动/手动刷新成功，或重新授权。 |
| OAuth 401，无刷新材料 | 进入错误并停调度。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:279` | 重新授权或重新导入完整凭据。 |
| token 明确撤销 | 进入错误并停调度。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:250` | 重新授权。 |
| 429 | 写账号级或模型级恢复窗口；其他模型可能继续可用。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:192` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:364` | 到点自动恢复或管理员清限流。 |
| 529 | 写短期过载窗口。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:367` | 到点自动恢复。 |
| 身份验证要求/封禁 | 用量探测输出需要验证、被封或重新授权标记。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:229` | 人工验证、申诉或重新授权。 |
| 测试成功 | 可清错误和限流状态。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:42` | 管理端测试。 |

**Inferred 2：状态恢复的正确性依赖“恢复动作同时触达持久层、运行缓存和调度快照”，只改一个布尔值不足以恢复生产流量。**

## D. 请求时序

1. **解析租户侧请求**：取得 API key、分组、请求模型、端点、会话线索和前一响应线索。
2. **加载候选快照**：优先从调度缓存读取；miss 时受限回源数据库，并用写入令牌防止陈旧结果覆盖新快照。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/scheduler_snapshot_service.go:210`
3. **执行硬门**：活动、人工调度、到期、过载、限流、冷却、quota、分组、平台、模型、能力和传输。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account.go:148`
4. **粘性尝试**：先前响应绑定或 session 绑定仍健康则复用；不健康则清理或逃逸。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:379`
5. **评分与选择**：对剩余候选算分、取 Top-K 并形成选择顺序；近期结果会更新错误率和 TTFT 运行统计。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:225` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:850`
6. **占用运行资源**：取得账号并发槽，必要时还受 RPM/session 限制。
7. **准备凭据**：读取缓存；需要刷新时用账号级锁串行续期；刷新后比较版本并失效旧缓存。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_cache_invalidator.go:78`
8. **构造上游请求**：解析代理，选择 HTTP/SSE/WebSocket 等传输，注入身份，执行协议转换、模型映射和请求规范化。
9. **单账号重试**：仅对允许在同账号恢复的错误执行，受总时间预算和退避控制。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_gateway_service.go:924`
10. **错误分类与回写**：写限流、过载、临时冷却、错误或 token 缓存失效。
11. **跨账号 fallback**：排除已失败账号，重新从原始请求生成下一账号请求；不能复用前账号模型映射结果。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_failover_cached_body_test.go:96`
12. **记录最终结果**：把最终账号、最终上游模型、token 桶、缓存命中 token、时延和映射链交给日志与结算。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_gateway_usage.go:249`
13. **释放资源与更新运行统计**：释放并发槽，更新最后使用时间、调度成功率和 TTFT。

**Inferred 3：Sub2 将“选择账号”和“执行账号”拆成可反馈回路；调度不是纯静态负载均衡，而是会被请求结果持续训练的运行态排序。**

## E. 后台协同

### E.1 调度快照与多副本

**Observed：调度快照有启动重建、变更事件轮询和周期全量重建三条收敛路径。** 请求 miss 时还能受 QPS 与超时保护地回源数据库。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/scheduler_snapshot_service.go:170` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/scheduler_snapshot_service.go:299`

**Observed：变更事件按递增水位消费；只有整批处理和水位写入成功后才清理旧事件。** 清理由 PostgreSQL advisory lock 保证多副本只有一个清理者，删除还留出提交延迟宽限，防止序列号先分配、事务后提交导致漏消费。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/scheduler_snapshot_service.go:345` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/repository/scheduler_outbox_repo.go:122` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/repository/scheduler_outbox_repo.go:152`

**Observed：分桶写入有代际 fencing，分组退役/重开有跨实例租约。** 因而旧重建任务不能在分组已经删除或重建后把陈旧账号重新发布。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/scheduler_cache.go:25` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/scheduler_cache.go:85`

### E.2 token 续期

**Observed：后台续期服务启动即执行一次，随后按周期运行；关闭会取消上下文并等待 worker。** 候选采用稳定游标分页，平台各自限并发和 QPS，周期与单次尝试均有超时。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_refresh_service.go:201` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_refresh_service.go:229`

**Observed：至少一个平台的刷新回写使用“凭据和代理仍未变化”条件更新。** 管理员若在刷新期间重新授权或修复代理，旧 worker 的失败结果不能把新状态覆盖成错误或冷却。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_refresh_service.go:46` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_refresh_service_test.go:179`

### E.3 其他 worker

**Observed：账号到期、代理到期、订阅到期、定时账号测试、用量清理、异步使用记录和运维定时报表均有独立生命周期。** 服务 wiring 在关闭时逐一停止并等待。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/cmd/server/wire.go:150` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/cmd/server/wire.go:179` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/cmd/server/wire.go:214`

**Inferred 4：Sub2 的多副本一致性以“数据库事实 + Redis 运行快照 + outbox 最终收敛”为主，不是把 Redis 当唯一真相。**

## F. 管理恢复

### F.1 管理列表与诊断

**Observed：账号列表可按平台、类型、状态、搜索、分组和隐私状态过滤，并可展示实时并发和调度分数。** 对部分账号还展示窗口费用、活动 session 与 RPM。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/admin_account.go:22` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:181`

### F.2 管理动作

**Observed：管理 API 具备单账号创建、编辑、复制、删除、测试、刷新、重新授权、批量编辑、调度启停、清错误、清限流、清临时冷却、清模型限流、重置 API key quota、恢复代理 fallback 和用量/账单探测。** 批量编辑可更新代理、并发、优先级、倍率、负载因子、状态、调度开关、分组及凭据附加项。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_handler.go:147` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_service.go:95`

**Observed：复制账号不是复制运行态。** 仅允许不依赖轮转秘密的若干类型；新副本默认不可调度，剔除 quota 使用量、窗口、限流、探针、能力和外部同步身份等瞬态数据，并保留分组优先级。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/admin_account.go:94` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/admin_account.go:232`

**Observed：连接测试是真实恢复入口，不只是 ping。** 它会按账号类型构造对应端点请求，成功时可解析额度头、更新能力/额度快照并清理错误或限流；失败会进入与生产请求一致的错误分类。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_test_service_openai_test.go:104` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:42`

### F.3 观测、告警、审计、恢复入口

**Observed：可直接观察调度选择次数、粘性命中、负载选择、换号、平均调度延迟、粘性命中率、换号率和平均负载偏斜。** retry、非重试快速 fallback、旧 session 兼容读取、异步记账队列饱和和同步回退也有独立计数。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:100` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_gateway_service.go:310` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/usage_record_worker_pool.go:337`

**Observed：报告读取到运维错误日志、系统日志、账号可用性投影和告警评估服务，但本次未深读完整通知发送链。** 因此只能确认存在告警评估与运维日志能力，不能断言所有账号状态变化都会主动通知。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ops_account_availability.go:1` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ops_alert_evaluator_service_test.go:1`

## G. provider 分叉

只记录确实改变账号链的分叉。

### G.1 Claude / Anthropic

**Observed：OAuth 与 setup token 都可能需要续期；两者是否刷新由到期时间门控。** OAuth 能主动读取上游五小时/七天用量，setup token 因 scope 不足主要依赖 session 窗口与本地数据。账号还能配置窗口费用、最大 session 和 RPM 门。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_refresher.go:40` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:335`

### G.2 Gemini

**Observed：授权入口分为多种账号形态，其中部分需要 project 标识，部分不需要；账号可保存 tier。** 请求前可按 Pro/Flash 或共享池做日 quota 与 RPM 预检，主动 quota 数据和本地使用统计共同参与管理展示。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/gemini_oauth_handler.go:28` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service.go:389`

### G.3 Antigravity

**Observed：账号额度是逐模型的，并附带模型能力、订阅等级、AI credits、模型迁移、验证/封禁/重新授权状态。** 401 还会写强制刷新提示；部分 429 可先同账号短暂等待，较长恢复期则直接切账号并写模型级状态。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:198` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/antigravity_gateway_service.go:30`

### G.4 Grok

**Observed：除授权码外，还有 SSO token 批量转换入口；创建后安排额度/账单探测。** OAuth 刷新失败回写特别保护管理员并发重新授权和代理修复，额度快照同时区分请求 quota、token quota、重试时间、权益状态和账单摘要。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/grok_oauth_handler.go:266` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_usage_service.go:201`

### G.5 OpenAI / Codex

**Observed：支持 OAuth、API key、长期访问令牌和会话批量导入。** 调度分叉最深：前一响应粘性、session 粘性、负载评分、HTTP/SSE/WebSocket 能力、compact 能力、订阅优先、上游成本和额度余量都可能改变选择。响应头还能形成五小时/七天用量快照。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/openai_oauth_handler.go:110` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_account_scheduler.go:68` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_gateway_usage.go:649`

**Observed：存在凭据借用型 quota 子账号。** 子账号只拥有独立模型/额度维度，不持完整凭据；母账号认证或传输故障会阻断子账号，但母账号全局 quota 429 不必连坐子账号的独立额度维度。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account.go:171` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_credentials_persistence.go:17`

### G.6 Kimi

**Observed：在 Sub2 本次深读范围中，未观察到 Kimi 形成独立账号创建、调度、额度或恢复分叉。** 不将其列为 Sub2 已实现账号模式。

### G.7 默认三镜补充（合计控制在全文 20% 内）

| 项目 | Sub2 未突出或未采用的补充机制 |
| --- | --- |
| CLIProxyAPI | 凭据池可在最高优先级内按 provider+模型维度轮询，或持续使用稳定排序后的首个可用凭据；逐模型冷却可返回最早恢复时间。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/selector.go:199` `router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/selector.go:256` |
| CLIProxyAPI | 自动续期按下一检查时间调度并由固定 worker 执行；请求前凭据补全和刷新按凭据隔离并发。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/auto_refresh_loop.go:13` `router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/conductor.go:2956` `router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:sdk/cliproxy/auth/conductor.go:5887` |
| CLIProxyAPI | 文件型凭据和配置可被观察器增量热加载，原子替换和删除带 debounce，并产生新增、修改和删除事件。`router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/watcher/events.go:67` `router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa:internal/watcher/dispatcher.go:145` |
| New API | 自动分组可在当前组耗尽优先级或重试额度后切到下一组，并把进度保存在请求上下文。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:service/channel_select.go:84` `QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:service/channel_select.go:107` |
| New API | 亲和键可从上下文、请求头或请求 JSON 提取，使用本地 TTL/LRU 与可选 Redis 的混合缓存，并支持清理和统计。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:service/channel_affinity.go:81` `QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:service/channel_affinity.go:198` |
| New API | 渠道测试复用实际 relay 上下文和模型映射，可按模型、端点和渠道类型选择测试协议。`QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:controller/channel-test.go:95` `QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1:controller/channel-test.go:235` |

**Inferred 5：CLIProxyAPI 更偏单机/文件驱动的凭据运行时，New API 更偏渠道与分组路由；Sub2 则把数据库账号、运行状态、计费和管理恢复结合得更完整。** 这是三组已观察行为的产品形态比较，不是对未读区域的能力否定。

## H. 测试证据

### H.1 已观察到

| 层级 | 真实证据（Observed） | 能证明什么 |
| --- | --- | --- |
| 单元：批量导入 | 测试覆盖原始 token、JSON、数组、混合行、session secret 不落凭据、保留旧 refresh token、拒绝过期 token。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_codex_import_test.go:15` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_codex_import_test.go:89` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/handler/admin/account_codex_import_test.go:148` | 导入格式、秘密边界和旧续期材料保护。 |
| 单元：401 恢复 | 测试覆盖 OAuth 进入临时不可调度、非 OAuth 进入错误、缓存失效失败仍不覆盖凭据、子账号 401 回写母账号。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service_401_test.go:89` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service_401_test.go:156` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/ratelimit_service_401_test.go:197` | 可恢复认证失败不会被误判为永久死亡，也不会回滚新 token。 |
| 单元：跨账号 fallback | 第一账号 429 后，第二账号重新使用自己的模型映射；旧解析缓存被忽略。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/openai_failover_cached_body_test.go:26` | 跨账号不会污染请求语义。 |
| 单元：账号测试 | 成功测试解析用量头并写快照；子账号测试使用母账号凭据但保留子账号模型。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_test_service_openai_test.go:104` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/account_test_service_openai_test.go:168` | 管理测试与真实凭据/额度链相连。 |
| 单元：异步记账 | 覆盖正常入队、队满丢弃、同步回退、采样策略、停止后拒绝和自动扩缩容。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/usage_record_worker_pool_test.go:13` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/usage_record_worker_pool_test.go:41` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/usage_record_worker_pool_test.go:78` | 计量压力下的明确降级策略。 |
| 单元：刷新竞态 | 测试桩模拟管理员重新授权、代理修复和刷新期间状态改变，条件回写只在旧快照仍匹配时生效。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/token_refresh_service_test.go:179` | 后台失败不能覆盖更近的人工修复。 |
| 单元：调度生命周期 | 覆盖分桶退役、写入令牌、锁竞争、水位和全量重建生命周期。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/scheduler_snapshot_full_rebuild_lifecycle_test.go:16` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/service/scheduler_snapshot_full_rebuild_lifecycle_test.go:260` | 多副本陈旧写入与分组生命周期风险被显式测试。 |
| 集成：数据库候选 | 真实数据库测试覆盖多平台、分组绑定、非活动/错误/停调度账号排除。`Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/repository/gateway_routing_integration_test.go:34` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/repository/gateway_routing_integration_test.go:97` `Wei-Shaw/sub2api@09c6c6d74050cf49ed2fb864be6c11647798ef53:backend/internal/repository/gateway_routing_integration_test.go:165` | 仓储候选门与业务预期一致。 |

### H.2 未观察到

1. **未观察到**一个从浏览器授权开始、经过真实第三方 OAuth、真实 Redis/PostgreSQL、多账号 fallback、最终计费和管理恢复的全外部端到端测试。
2. **未观察到**在本次读取范围内使用故障注入验证 Redis 分区、PostgreSQL 主从切换或多副本时钟偏差。
3. **未观察到**所有 provider 都有同等深度的授权、刷新、quota、fallback 和账单旅程测试。
4. **未观察到**主动告警发送到邮件、Webhook、IM 的完整账号事故 E2E。
5. **未观察到**长期运行测试证明 outbox 水位、事件清理和周期全量重建在数日压力下无漂移。
6. **未观察到**管理批量动作中途部分失败后的统一补偿旅程；目前能确认逐项结果和部分幂等机制。

**Inferred 6：测试体系对高风险竞态和局部机制覆盖较深，但“整条生产旅程”仍主要由多个单元/集成测试拼合证明。**

## I. 可转化为 HUAKAI 验收标准

以下只写行为验收，不给实现方案。

1. **账号入口完整性**：每种账号类型必须有明确创建/导入/授权入口、必填凭据、可选代理、分组、并发、优先级、到期策略和失败反馈。
2. **批量导入真实性**：混合输入逐项隔离；结果必须区分创建、更新、跳过和失败；重复身份不得静默创建；access-token-only 更新不得删除仍有效的 refresh token。
3. **敏感值边界**：管理读取永不返回完整 token/key/cookie/private key；编辑未提供敏感子项时保留旧值；日志和错误不得泄漏秘密。
4. **凭据所有权**：借用母账号凭据的子账号不得持久化完整秘密；认证失败和 token 缓存失效必须回写真正凭据所有者。
5. **刷新竞态**：同账号并发刷新必须串行；后台刷新、请求时刷新和管理员重新授权并发时，旧结果不得覆盖新凭据或新代理。
6. **旧 token 防回滚**：任何 401、测试失败或异步任务都不得用请求开始时的旧账号快照整列覆盖凭据。
7. **状态正交性**：活动状态、人工调度、到期、限流、过载、临时冷却、逐模型限流、验证/封禁/重授权必须能独立表达和恢复。
8. **候选硬门**：排序前必须排除所有硬不可用账号；粘性不得绕过硬门、模型能力、分组、传输和并发检查。
9. **分组与模型**：OAuth-only、账号类型、模型白名单/映射、逐模型 quota 和端点能力必须在请求前生效。
10. **选择可解释性**：每次调度至少可观测选择层、候选数、最终账号、主要排除原因和是否发生粘性/换号。
11. **并发真实性**：账号并发槽必须原子占用和释放；异常、取消、流式中断和 fallback 不得泄漏槽位。
12. **单账号 retry 与跨账号 fallback 分离**：错误分类必须明确允许同账号重试、必须换号或必须停止；总尝试和总耗时有上限。
13. **跨账号请求重建**：fallback 到新账号时，必须从原始请求重新应用该账号的模型映射、身份和协议转换。
14. **状态回写**：401/403/429/过载/模型不存在/本地 quota 等错误必须写入正确粒度；本地预检不得伪装成上游限流事实。
15. **最终账号归因**：使用日志和计费必须记录最终实际执行账号，而不是首次候选账号。
16. **缓存 token 计量**：普通输入、缓存创建和缓存读取 token 必须互斥计量，避免双算；fallback 后以最终响应为准。
17. **额度视图**：支持上游主动探测、本地被动统计和错误负缓存；明确数据来源、更新时间和降级状态。
18. **管理恢复闭环**：测试、刷新、重新授权、清错误、清限流、清冷却、重置 quota、启停调度、批量动作后，持久层、token 缓存和调度快照必须收敛一致。
19. **复制安全**：复制账号不得复制瞬态 quota、限流、探针、外部同步身份和运行错误；新副本默认不得立即吃生产流量。
20. **多副本快照安全**：陈旧重建不得覆盖新快照；已退役分组不得被旧任务复活；outbox 只有在事件处理和水位持久化成功后才能清理。
21. **worker 生命周期**：启动时执行必要重建/扫描，关闭时取消并等待；停止后不得接受新异步记账任务。
22. **压力降级**：异步计量队列满时必须有明确且可观测的同步回退、采样或丢弃策略，不得静默消失。
23. **真实测试门**：至少包含导入秘密边界、刷新竞态、401 自愈、逐模型限流、粘性逃逸、并发泄漏、跨账号重建、最终计费归因、outbox 陈旧写和管理恢复旅程。
24. **E2E 缺口门**：发布前应补一个真实 PostgreSQL+Redis 的多账号旅程，从创建/授权到请求、fallback、计费、限流、刷新和恢复。

**Inferred 7：若以上验收只按 provider 分开写，会漏掉跨 provider 共用的凭据所有权、状态正交性、最终账号归因和多副本收敛；应以生产链为主轴、provider 分叉为附录。**

## Open Questions

1. Sub2 的 OAuth 临时会话具体持久介质、TTL、一次性消费和跨副本语义，在本次读取区域中未完整核实。
2. 所有平台请求时 token 获取是否都使用分布式锁，还是部分仅用进程内锁，未逐个 provider 深读。
3. 自动续期失败后是否所有平台都具备旧 access token 的安全降级窗口，未观察到统一保证；目前只能确认部分导入与刷新路径会保留旧字段。
4. 账号凭据在数据库静态加密、密钥轮换和备份恢复时的完整边界，本次未深读。
5. 管理端“重新授权”对每个平台是否都原子替换凭据并清理全部旧 session/token 缓存，未逐平台验证。
6. 分组标签是否存在独立于分组关系的通用标签系统，未观察到；本文不把分组等同于标签能力。
7. 账号级权重与优先级的全部组合规则，除 OpenAI/Codex 深度评分外，其他 provider 未逐一核实。
8. 单账号 retry 与跨账号 fallback 的全局总预算是否在所有协议入口一致，未观察到统一证明。
9. 计费异步任务在进程崩溃时是否有持久化队列或重放保障，未观察到；当前读到的是进程内 worker pool 与数据库结算接口。
10. 运维告警是否覆盖 token 刷新熔断、outbox lag、全部账号冷却、计量丢弃和 quota 异常，未完整核实。
11. 是否存在覆盖真实 OAuth provider 和真实上游 sandbox 的 CI E2E，未观察到。
12. Kimi 在 Sub2 当前本地 SHA 是否通过未命名兼容入口接入，未观察到足够源码证据。

## 最终总结

本报告的真实观察是：Sub2 的账号系统是一条“入口与秘密管理 → 可恢复状态机 → 多层候选与调度 → 请求前凭据/代理/协议准备 → retry/fallback → 最终账号计量计费 → 管理与后台收敛”的完整生产链，核心协同点是账号运行态、token 缓存、调度快照、错误回写和最终使用归因；7 条 Inferred 均由这些已观察区域组合推出，没有当作上游事实；Open Questions 共 12 条，主要集中在临时 OAuth session、全平台分布式刷新锁、静态加密、崩溃后计量重放、主动告警和真实外部 E2E。

Source files read: backend/internal/service/account.go; backend/internal/service/account_service.go; backend/internal/service/account_credentials_persistence.go; backend/internal/service/account_credentials_redact.go; backend/internal/service/admin_account.go; backend/internal/handler/admin/account_handler.go; backend/internal/handler/admin/account_codex_import.go; backend/internal/handler/admin/openai_oauth_handler.go; backend/internal/handler/admin/gemini_oauth_handler.go; backend/internal/handler/admin/grok_oauth_handler.go; backend/internal/service/token_refresh_service.go; backend/internal/service/token_refresher.go; backend/internal/service/refresh_token_cache.go; backend/internal/service/token_cache_invalidator.go; backend/internal/service/scheduler_cache.go; backend/internal/service/scheduler_events.go; backend/internal/service/scheduler_outbox.go; backend/internal/service/scheduler_snapshot_service.go; backend/internal/repository/scheduler_outbox_repo.go; backend/internal/service/openai_account_scheduler.go; backend/internal/service/gateway_service.go; backend/internal/service/openai_gateway_service.go; backend/internal/service/openai_gateway_usage.go; backend/internal/service/ratelimit_service.go; backend/internal/service/temp_unsched.go; backend/internal/service/proxy_fallback.go; backend/internal/service/account_usage_service.go; backend/internal/service/account_test_service.go; backend/internal/service/usage_record_worker_pool.go; backend/cmd/server/wire.go; backend/internal/handler/admin/account_codex_import_test.go; backend/internal/service/ratelimit_service_401_test.go; backend/internal/service/openai_failover_cached_body_test.go; backend/internal/service/account_test_service_openai_test.go; backend/internal/service/token_refresh_service_test.go; backend/internal/service/scheduler_snapshot_full_rebuild_lifecycle_test.go; backend/internal/service/usage_record_worker_pool_test.go; backend/internal/repository/gateway_routing_integration_test.go; sdk/cliproxy/auth/selector.go; sdk/cliproxy/auth/conductor.go; sdk/cliproxy/auth/auto_refresh_loop.go; internal/watcher/watcher.go; service/channel_select.go; service/channel_affinity.go; controller/channel-test.go; controller/model_sync.go
Lane: specifier
Agent: GPT-5 Codex / root
UTC timestamp: 2026-07-16T09:56:32Z
