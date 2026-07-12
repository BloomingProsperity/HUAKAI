# 2026-07-10 Claude OAuth 账号到 API serving 接线与出站 body 拟真：Codex 独立平行计划

| 项目 | 内容 |
| --- | --- |
| Owner 指令 | “独立起草 ‘Claude OAuth 账号→API serving 接线 + 出站 body 拟真’ 实现计划（平行计划，非审查）” |
| 任务性质 | 只读调查后的 specifier-lane 实现计划；不是 Claude 计划的审查 |
| LANE | specifier |
| PRIOR LANES ON THIS ARTIFACT | none |
| REFERENCE PROJECTS IN SCOPE | CLIProxyAPI + sub2api + new-api |
| 范围 | 池内 Claude OAuth/session 账号到 api.anthropic.com 的 serving 接线；官方 Claude Code 原 body 直发；非官方客户端在 Owner 放行后执行结构化 body 拟真；覆盖路由、凭据、adapter、流式/非流式协议、计费/配额/并发槽交界 |
| 范围外 | 本计划不改生产代码、不创建 migration、不跑构建、不发真实上游请求、不改 LICENSE、不提交 commit；不声称镜像行为等同于 Anthropic 当前官方客户端 |
| 成功标准 | 两个切片分别有可判别验收：S1 能用独立 session 协议族选中 OAuth/session 账号并完成流式与非流式转发，官方请求 body 字节等价；S2 在严格官方判别下仅对非官方请求执行完整且可逆的 body 变换，所有失败路径终结 hold/配额预留/并发槽 |
| 工时估算 | 约 5–8 个工程日；含实现、迁移演练、判别测试、两轮必要 review 与灰度观测。纯编码约 3–5 日，数据库与运行验证约 1–2 日，review/修复约 1 日 |
| 爆炸半径 | models 协议约束、provider catalog、模型绑定、池选号、凭据物化、Anthropic transport、HCSF/流式解析、客户端准入、出站 body、工具调用响应回写、billing claim、quota reservation、slot acquisition、OAuth 热刷新 |
| Observed regions | 63 个纳入本文证据的独立源码区间；仅搜索命中但未作为论据的区域不计 |
| Inferences | 12 项，均标成“推断/建议”，不伪装成上游事实 |
| Open questions | 3 项，见 §13 |

## 0. 独立性与 clean-room 声明

本计划没有打开、阅读或询问 Claude 的同名平行计划。只读全仓搜索时曾意外命中该未跟踪文件的两行标题式文本；命中内容未被当作证据，也未进入本计划的架构推导。后续调查只读取 HUAKAI 源码、三份本地参考镜像及项目规则。

本文只转述可观察行为，不复制参考项目的函数名、结构字段、注释、代码块或源码顺序。三份参考镜像只提供行为证据；HUAKAI 的边界、命名、数据结构、错误分类与测试均须自行设计。涉及参考项目的结论都绑定到本次实际读取的本地 commit 与行区间。

## 1. 关键判断

### 1.1 必须拆成两个发布切片

结论：serving 解阻与 body 深拟真不合并。

- S1 只解决“协议族可配置、可选号、可物化 OAuth/session 凭据、可选中正确 adapter、可完成流式/非流式响应、可在失败时正确释放资源”。S1 保持现有 Anthropic 反转账号“只允许官方客户端”的安全默认，官方请求 body 字节等价直发。
- S2 再解决“非官方客户端准入 + 严格官方判别 + 固定 CLI 身份块 + 归因块 + 原 system 下沉 + 工具名往返改写 + cache/metadata 组合”。S2 必须由 feature flag 与准入策略共同控制，Owner 明确启用后才允许非官方请求通过。

理由：

1. S1 是协议族对称性与资源生命周期问题；S2 是客户端身份、请求语义与上游合规风险问题。故障域不同，回滚方式也不同。
2. 当前客户端门会拒绝 Anthropic OAuth/session 的非官方请求；若把注册和拟真合并，一旦拟真有缺口，只能同时回滚 serving。
3. 现有固定 system 原子不能表达“替换 system 后把原内容迁移进 messages”，工具名改写也尚无响应侧逆变换。把这些未闭环能力与注册一起上线，会形成“请求出去但响应工具名泄漏/语义损坏”的半成品。
4. S1 可先让真实 Claude Code 获得可用链路，并给 S2 提供真实的资源释放、错误分类与协议测试底座。

### 1.2 协议族独立，vendor 身份归一

保留 anthropic_claude_session 作为独立 protocol family；不要把 OAuth/session 凭据塞进 anthropic_messages。二者的凭据准入、默认 query、客户端策略与 body 策略不同，独立协议族能让错误配置 fail-closed。

同时，OAuth adapter 的 Platform 应返回 anthropic，而不是重复 protocol family 字符串。Platform 在 HUAKAI 中参与 transport、计价/指标 vendor 切片及账号平台对齐；协议族已经由 registry key 表达。当前 adapter 返回 session 族字符串，而凭据仓投影的账号平台是 anthropic，若原样注册会触发 vendor 对称守卫或迫使系统增加一个并不存在的 transport vendor。[HUAKAI: internal/provider/anthropic/oauth_session.go:16-30；internal/provider/postgres_vault.go:172-184；internal/pool/api.go:17-38；internal/transport/policy.go:21-36,118-128]

### 1.3 body 落点在“最终 Anthropic body 组合接缝”，不在 adapter

OAuth adapter 继续只负责：

- 凭据类型与过期校验；
- endpoint/query；
- Bearer、版本、beta 白名单、设备与 session header；
- 构造 HTTP request。

结构化 body 拟真应由一个新的、小而内聚的 claudeoauthmimicry 子包完成，并通过 gateway 现有“最终出站 body”接缝调用。理由：

- raw 流式、raw buffered 和 HCSF buffered 三条路径必须共享同一策略；
- HCSF 会先把 canonical envelope marshal 成最终 Anthropic body，metadata 等字段可能直到最后接缝才补回；在 adapter 之前的最终接缝改写才能覆盖所有入口而不重复；
- adapter 当前看不到足够的入站客户端证据，也不应承担官方判别、system 语义迁移或响应工具名逆变换；
- gateway 与 gatewayhttp 已超过软预算基线，不应再向这两个目录新增生产文件；新职责应进入新子包，只在既有接缝文件内做小幅接线。[HUAKAI: internal/gatewayhttp/chat_completions_stream.go:127-147,154-175；internal/gateway/upstream_dispatcher_hcsf.go:290-342]

### 1.4 现有 SystemRewrite 不够

当前 SystemRewrite 只支持前缀、全量替换、尾部追加；它只改 system 字段。它不能：

- 生成多块固定身份/归因结构；
- 按确定顺序计算归因指纹；
- 把原 system 的文本语义下沉为 leading user/assistant 消息对；
- 对不支持的 system block 形态给出拟真专属的判决；
- 携带工具名逆映射与组合审计。

因此不能只在 identity.go 中把 SystemRewrite 从 nil 改为 enabled。应新增“结构化 Claude OAuth body profile”原子，再让现有 MimicryPlan 负责它已经擅长的 cache、工具字段、metadata 与 tools-tail 步骤。现有 metadata 入口还会在 external account id 或 server secret 缺失时整段短路；若直接复用它作为总开关，会把固定 system/归因块也静默跳过。[HUAKAI: internal/gateway/system_rewrite.go:46-67,160-218；internal/gateway/mimicry_compose.go:57-86,106-243；internal/mimicryidentity/identity.go:67-94,109-125]

## 2. 参考证据与边界

### 2.1 本地镜像快照

| 镜像 | 本次读取的本地 HEAD | 本地 commit 时间 | 证据边界 |
| --- | --- | --- | --- |
| CLIProxyAPI | 26d45fd46a2d2911adef14772465131066dae465 | 2026-07-10T05:30:12+08:00 | 只对该 commit 的读取区域作观察结论 |
| sub2api | 12d811bd76572836d6df6e1fa8aa5ff91be3b12e | 2026-07-09T14:57:53Z | 只对该 commit 的读取区域作观察结论 |
| new-api | 246d62aa5ed3ba2a4728322c269c180a016dc9cd | 2026-07-09T22:03:45+08:00 | 只对该 commit 的读取区域作观察结论 |

三仓本地工作树干净。本任务未执行 fetch，因此不能证明本地 HEAD 等于远端最新 HEAD；“当前”在本文中严格指上述本地 commit，不外推到远端。这也是 §13 的第一个 open question。

### 2.2 body 落点对照

- CLIProxyAPI 在供应商执行层完成 OAuth body 准备：工具名处理、内容签名、固定 query 与 HTTP 构造位于同一执行边界；固定身份/归因 system 结构与原 system 下沉也在该供应商执行层完成。其本地版本默认给 OAuth body 增加内容校验段。<router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/runtime/executor/claude_executor.go:266-290,1829-1957> <router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/runtime/executor/claude_signing.go:15-40>
- sub2api 在 gateway body 处理层先判定是否为官方客户端，再对非官方 OAuth 请求执行 system 重建、metadata/cache/tool 等变换；其 HTTP 请求构造另在上游 request 层。<Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/gateway_forward.go:150-210> <Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/gateway_claude_oauth_body.go:857-941> <Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/gateway_upstream_request.go:21-48>
- new-api 本次实际读取的 Anthropic 生产路径是 API-key channel adapter：它按 channel 配置决定是否加 beta query，并写 API key/version header；本次读取区域未见与 HUAKAI session family 等价的 OAuth body 深拟真分支。该结论只覆盖读取路径，不声称全仓绝对不存在。<QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:relay/channel/claude/adaptor.go:44-98> <QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:relay/relay_adaptor.go:54-65> <QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:model/channel.go:23-49>

HUAKAI 应采用与自身 HCSF/dispatch 架构相容的集中组合接缝，而不是照搬任一参考项目的文件落点。

### 2.3 官方客户端判别对照

- CLIProxyAPI 的自动模式只看入站 User-Agent 是否以固定前缀开头，命中即跳过覆盖；这是可观察的轻量判据，也容易被伪造。<router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/runtime/executor/helps/cloak_utils.go:39-56>
- sub2api 对 messages 路径使用组合判据：锚定的 UA 形态、system 特征、应用/版本/beta header 与 metadata 结构；命中后跳过 body 拟真。<Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/claude_code_validator.go:67-145> <Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/handler/gateway_helper.go:24-59> <Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/gateway_forward.go:161-175>

建议 HUAKAI 不复用当前 clientid 的宽松“自报 header 优先 + UA 子串”判定作为直发依据。该检测器把客户端可控声明当作强信号，UA 也只是 substring；它适合统计归因，不足以决定是否跳过必需的 body 保护。[HUAKAI: internal/clientid/clientid.go:44-124,168-184]

### 2.4 beta query 对照

- CLIProxyAPI 的本地 OAuth 路径固定请求 messages?beta=true。<router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/runtime/executor/claude_executor.go:271-290>
- sub2api 的本地默认 OAuth messages 与 count_tokens URL 都带 beta=true；自定义 API-key base URL 也显式构造该 query。<Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/gateway_service.go:30-38> <Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/gateway_upstream_request.go:27-48>
- new-api 把该 query 作为请求级/channel 级可选设置，不是无条件默认。<QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:relay/channel/claude/adaptor.go:44-70>

HUAKAI 建议：session family 访问默认官方 endpoint 时，缺省带 beta=true；凭据显式 false 时关闭。对自定义 base URL，缺省不擅自添加，只有显式 true 才添加。无论哪种情况都用 URL helper 合并，保留已有 query、不重复键。

### 2.5 归因块与 CCH 事实

sub2api 本地版本的归因文本由三类语义组成：

1. 客户端 profile 版本；
2. 由当前 body 与版本派生的短内容指纹；
3. 客户端入口类型。<Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/gateway_billing_block.go:73-94>

该本地版本明确不再加入 CCH 段。<Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/gateway_billing_block.go:73-94>

CLIProxyAPI 同日本地版本则相反：它为 OAuth body 默认计算并写入 CCH，且归因内容还可带 workload。<router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/runtime/executor/claude_executor.go:1829-1870> <router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/runtime/executor/claude_signing.go:15-40>

两镜在同日快照上冲突，不能把任何一方当作当前官方真相。HUAKAI v1 自研契约建议：

- AttributionProfileVersion：HUAKAI 钉定的出站 profile 修订，不直接信任非官方入站 UA；
- ContentDigest：对“已完成 system/messages 结构变换、但尚未写归因块”的 canonical body 做域分离摘要；内部审计保留完整摘要，wire 截断方式由独立一方流量验证后写入 profile 版本；
- EntrySource：封闭枚举。官方请求直发不重写；非官方请求使用 HUAKAI 自己判定的入口，不能接受任意客户端 header；
- WorkloadClass：仅在来源可认证且 Owner 需要时加入，默认缺省；
- SignatureEvidence：v1 不发送 CCH，结构上预留算法版本与 feature flag；在官方当前流量证据和法律/合规决策到位前不得猜测算法。

不得写入 tenant_id、account_id、request_id、用户提示词原文、token、设备 secret 或其他 HUAKAI 内部标识。wire 字段名与长度必须由独立规格/一方流量验证确定，不能从镜像复制字面量或算法。

这里的“归因块”只是出站客户端 profile 元数据，不是 HUAKAI 的 billing claim、价格、余额或 usage ledger。它不得读取或改变 HUAKAI money path，也不得因名字相同而与结算模块建立依赖。

## 3. HUAKAI 当前链路：实际接线点

### 3.1 没有 authMode 到 protocol family 的自动映射

必须纠正一个容易误做的假设：credentialstore 当前只做 vendor/authMode 到 runtime credential kind 的映射，不决定 protocol family。

- Anthropic API key 被物化为 APIKey；
- Claude AI OAuth 被物化为 OAuthAccessToken；
- Claude Code 被物化为 SessionToken。[HUAKAI: internal/credentialstore/types.go:42-60,71-99,213-278]

Postgres vault 优先从 v2 credential store 取 active 记录，调用对应 handler 得到 runtime material，再映射为 provider.Credential；AccountInfo.Platform 来自 vendor，AccountType 来自 authMode，并携带 credential 行版本与上游账号标识。[HUAKAI: internal/provider/postgres_vault.go:74-84,143-184,286-307]

protocol family 来自 model/binding/provider 配置：

~~~text
models.protocol_family = anthropic_claude_session
        ↓ registry resolve / Router.Plan
pool SelectionRequest.ProtocolFamily
        ↓ SQL 精确匹配 providers.upstream_protocol
provider_account + slot acquisition
        ↓ vault: authMode → runtime credential kind
registry.For(anthropic_claude_session)
        ↓ OAuthSessionAdapter
api.anthropic.com/v1/messages?beta=true
~~~

Router 只生成 pool attempt 序列，不选择 adapter。[HUAKAI: internal/router/default_router.go:40-97；internal/gatewayhttp/chat_completions_dispatch.go:181-234]

生产选号把 resolved family 原样传给 pool，SQL 对 providers.upstream_protocol 做精确等值过滤；AccountSnapshot 再携带该 family 通过 gate。[HUAKAI: internal/gatewayhttp/chat_completions_dispatch.go:474-520；internal/pool/dispatcher/account_source.go:34-66；internal/db/billing/pool_accounts.sql.go:664-716；internal/pool/router/gates.go:241-250]

该生产选号查询没有投影或过滤 account_type，只按 provider family、凭据状态、模型与 capability 过滤；因此同一 session provider 下若混入 API-key 账号，选号阶段本身不会识别错误 authMode。兼容性必须在配置写入时阻止，并在 vault 物化后、发网前再做一次防御校验。[HUAKAI: internal/db/billing/pool_accounts.sql.go:664-716；internal/pool/dispatcher/account_source.go:46-69]

因此 S1 的配置不变量是：

1. model.protocol_family = anthropic_claude_session；
2. provider.upstream_protocol = anthropic_claude_session；
3. provider vendor/code 对应 anthropic；
4. 该 provider 下 serving 账号只能使用 claude_ai_oauth 或 claude_code authMode；
5. runtime credential 必须属于 OAuthAccessToken、SessionToken 或经明确审计的 passthrough 类型。

不要在 credentialstore 增加“authMode 自动改路由”的隐式行为。应新增共享的 family↔authMode/credential compatibility 校验，供 admin 配置时预检与 dispatch 前防御；路由仍由模型/供应商配置显式决定。

### 3.2 registry 与配置面当前确实 fail-closed

常量已存在，但默认支持集合没有 session family；Build 只注册 API-key Anthropic adapter，并明确把 OAuth adapter 留在 fail-closed 状态。[HUAKAI: internal/provider/registrydefault/default.go:69-101,139-146,212-237]

registry 按 protocol family 精确查 adapter；未注册直接返回错误。[HUAKAI: internal/provider/registry.go:31-65]

真正的 adapter 选择发生在 upstream dispatcher：它使用本次 resolved protocol family 查 registry，再把已物化 credential、账号信息与最终 body 交给 BuildRequest；不是 Router 或 credentialstore 直接选择 adapter。[HUAKAI: internal/gateway/upstream_dispatcher.go:133-180]

models.protocol_family 的现有 CHECK 不含 session family，且修改约束会拿 models 表 ACCESS EXCLUSIVE lock。[HUAKAI: sql/migrations/0172_models_protocol_family_registered_adapters.up.sql:9-57]

admin provider catalog 又复用 SupportedProtocolFamilies 做 upstream_protocol 校验；所以注册支持集一旦加入该 family，配置面也会同时放开。[HUAKAI: internal/adminhttp/provider_catalog_mutation_handler.go:328-372,484-485]

当前测试显式要求该 family 不受支持且不出现在 migration 中；这些断言必须改成正向覆盖，而不是简单删除。[HUAKAI: internal/provider/registrydefault/default_test.go:62-78,372-381]

### 3.3 serving 不是一行注册：共有六个对称站点

仅在 provider registry 加一行会得到“部分可用”：

1. provider registry：session family → OAuthSessionAdapter；
2. protocol response registry：session family → Anthropic response adapter；
3. stream scanner registry：session family → SSE scanner；
4. HCSF request marshal family：session family → anthropic_messages 的线形状；
5. pool vendor：session family → anthropic；
6. migration/admin supported family。

HCSF 对所有入站注册 family 有正向/显式 fail-closed 守卫；session family 是 Anthropic Messages 同形，应走正向 marshal，不应列入例外表。[HUAKAI: internal/gateway/protocol_selector.go:91-168；internal/gateway/stream_scanner.go:171-247；internal/gateway/upstream_dispatcher_hcsf.go:381-425；internal/gateway/hcsf_graph_marshal_test.go:939-965]

pool 的注册表驱动测试要求每个注册 family 都有非空 vendor，通常还要求 vendor 等于 adapter.Platform。把 OAuth adapter 的 Platform 归一为 anthropic，可直接满足该不变量。[HUAKAI: internal/pool/api.go:17-102；internal/pool/vendor_guard_test.go:10-67]

### 3.4 当前 OAuth adapter 与 beta 缺口

adapter 已接受 OAuth access token、session token 与 passthrough；它验证本地 expires_at，注入 Bearer、版本、白名单 beta header、设备静态头与 session 头，但 body 直接使用 InboundBody。[HUAKAI: internal/provider/anthropic/oauth_session.go:16-85,98-115]

默认 endpoint 是 api.anthropic.com/v1/messages。API-key adapter 已有可选 beta query helper，OAuth adapter 尚未调用它。[HUAKAI: internal/provider/anthropic/passthrough.go:23-28,61-76；internal/provider/anthropic/oauth_session.go:46-54]

实现建议：

- 官方默认 endpoint：claude_beta_query 缺省或 true → 合并 beta=true；false → 不加；
- custom base_url：只有显式 true 才加；
- 已有 beta 值不重复、不覆盖 operator 显式值；
- query 处理在 endpoint SSRF/credential override 解析后、构造 request 前完成；
- count_tokens 不在本切片，另列 mandatory roadmap，不能暗示已支持。

### 3.5 当前非官方客户端会在 body 拟真前被拒绝

当前政策对 Anthropic reverse account 默认强制 Claude Code；clientgate 以 clientid 结果作判决，非官方请求在凭据解析后、dispatch 前 abort claim 并返回 403。[HUAKAI: internal/officialclient/policy.go:40-99；internal/gatewayhttp/clientgate/gate.go:35-50；internal/gatewayhttp/chat_completions_dispatch.go:540-589]

因此 S2 不是“只打开 SystemRewrite”即可。应把 bool 型 allow/deny 决策升级为三态：

| 决策 | 条件 | 行为 |
| --- | --- | --- |
| OfficialDirect | 严格官方判据成立 | 放行，body 字节等价，不运行任何拟真覆盖 |
| RewriteRequired | 非官方 + session family + 深拟真 profile 已启用 | 放行，但必须成功完成必需变换后才可发网 |
| Reject | 非官方且 profile 未启用/不完整，或账号策略显式 official-only | abort hold/quota/slot，返回稳定 403 |

硬不变量：系统绝不能出现“非官方已放行，但拟真 composer 因开关/缺字段而空操作”的第四种状态。

## 4. 目标模块设计

### 4.1 新建职责包，而不是继续膨胀大包

建议新建 internal/claudeoauthmimicry，职责仅为：

- 解析与校验 Anthropic Messages body；
- 基于已作出的三态客户端决策构建 profile；
- 重建固定 system 结构；
- 下沉原 system；
- 生成 HUAKAI 自研归因语义；
- 生成 collision-safe 的工具名正/逆映射；
- 组合现有 cache、metadata、tools-tail 原子；
- 返回 body、逆映射、审计摘要与 typed error。

它不做：

- 选号、凭据解密、HTTP、OAuth 刷新；
- billing ledger 或 quota 写入；
- 客户端 HTTP 错误渲染；
- 参考项目同名结构或算法。

gateway 与 gatewayhttp 当前非测试代码分别约 8474 行/27 文件和 13802 行/33 文件，均已越过软预算触发线；不得在这两个目录新增生产文件。只允许在现有接缝做小幅调用/类型接线，并由新子包承载新职责。

### 4.2 composer 输入/输出契约

建议输入：

- ProtocolFamily；
- AccountType 与 ExternalAccountID；
- ClientDecision（OfficialDirect / RewriteRequired / Reject）；
- ProfileRevision；
- 原始/最终 Anthropic body；
- 请求会话标识与 server secret 的受控派生入口；
- 已认证的 entry source；
- feature flags。

建议输出：

- Body；
- ToolForwardMap 与 ToolReverseMap；
- AppliedProfileRevision；
- Audit：只含步骤、布尔结果、错误类别、摘要，不含提示词、token 或完整 metadata；
- TypedError：InvalidShape、RequiredRewriteFailed、ProfileUnavailable、ToolMapCollision。

OfficialDirect 必须在任何 JSON unmarshal 前立即返回输入切片的克隆，保证字节等价，也避免“解析再序列化”造成 key 顺序、空白、未知字段漂移。

字节等价的边界是“进入拟真 composer 的最终 Anthropic body”与“composer 输出”。跨协议 HCSF marshal、标准 model remap 等既有协议变换发生在该边界之前，不应被误写成“HTTP 入站原始字节到上游完全不变”。

RewriteRequired 的必需步骤失败必须 fail-closed：不发上游、detached abort、不得切换账号重试同一确定性坏 body。metadata 派生缺少 external account id 可作为单独的可观测 optional-step fail-open；它不能让固定 system/归因/工具闭环整体跳过。

### 4.3 system 结构契约

HUAKAI 自研 v1 结构：

1. system[0]：归因块；不带 cache_control；
2. system[1]：固定 CLI 身份块；文本由 HUAKAI profile registry 管理；
3. system[2]：中性、工具无关的操作说明；允许稳定 cache breakpoint；
4. 原入站 system 不继续留在 system；
5. 原 system 为字符串或纯 text-block 数组时，合并成一条 leading user 指令消息，并跟一条最小 assistant acknowledgement，再拼接原 messages；
6. 原 system 含无法等价下沉的非文本/未知语义 block 时，返回 InvalidShape，不静默丢弃。

两镜都观察到“固定 system + 原 system 下沉到消息”的总体行为，但块内容、数量和细节不完全相同；HUAKAI 只复用行为目标，不复制文字或构造顺序。<router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/runtime/executor/claude_executor.go:1877-1957> <Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/gateway_claude_oauth_body.go:857-941>

### 4.4 工具名必须请求/响应闭环

现有原子能同步改 tools 声明、历史 tool_use 与强制 tool_choice，但只改请求 body，结果只含审计明细；没有自动把上游 assistant tool_use 的名称逆回客户端原名。[HUAKAI: internal/gateway/tool_name_rewrite.go:33-114,117-269]

S2 的启用条件必须是：

1. composer 在 dispatch 前按请求工具集合生成无碰撞映射；
2. raw streaming：ForwardRequest 携带逆映射，在 canonical tool-use 事件写给客户端前恢复；
3. raw buffered/HCSF：上游响应转 canonical 后、client adapter 前恢复；
4. 重试必须沿用同一 logical request 的映射，不能每次 attempt 变名；
5. audit 只记摘要与计数，不记完整用户工具名；
6. 任一响应路径未接逆变换时，工具名拟真 flag 保持关闭。

这部分建议作为 S2 内部的 S2b checkpoint；system/billing 可先在 S2a 完成，但深拟真总开关不得在 S2b 通过前启用。

### 4.5 严格官方直发判据

建议在 officialclient 或新的窄职责子包内实现 Anthropic 专属判据，至少要求：

- UA 完整匹配受支持的 claude-cli 语法与可解析版本，而非 substring；
- messages 路径存在预期的应用、协议版本与 beta header 组合；
- metadata.user_id 能按 HUAKAI 已有 parser 通过结构校验；
- body 具有可识别的一方归因/system 结构，但不对整段静态 prompt 做脆弱的逐字匹配；
- 多信号互相冲突时不判 OfficialDirect；
- X-Client-Name 等客户端自报 header 不参与直发授权。

特殊路径（count_tokens、max_tokens=1 探测）必须单独建 contract；本任务只接 messages，不能因为 UA 命中就把所有未来路径视为官方。

判据用途只应是“是否跳过覆盖”，不是身份认证或防攻击边界。官方客户端未来变形时，默认落入 RewriteRequired（若已启用）或 Reject，并记录 reason metric，避免静默直发可疑 body。

## 5. 切片与执行次序

### S1：serving 解阻，官方直发

目标：官方 Claude Code 请求能经池内 OAuth/session 账号完成端到端 serving；不引入非官方 body 拟真。

执行顺序：

1. Owner 先批准 schema migration 与协议族启用。
2. 新增下一可用 migration（执行时重新确认编号，当前看应为 0174）：
   - 给 models.protocol_family CHECK 加 session family；
   - down migration 回到前一完整 allowlist；
   - preflight 统计 models 行数、锁等待预算、现存非法值；
   - 在 staging 记录 ACCESS EXCLUSIVE 持锁时长。
3. 修正 OAuth adapter Platform 为 anthropic。
4. 默认 registry 注册 session family → OAuthSessionAdapter，并加入 SupportedProtocolFamilies。
5. pool vendor 映射 session family → anthropic。
6. protocol response registry 注册 Anthropic adapter；stream scanner 注册 SSE；HCSF marshal family 映射到 anthropic_messages。
7. admin/provider/model 配置面加入正向测试；新增 family↔authMode/credential compatibility 校验，阻止 API-key 账号挂到 session provider。
8. OAuth endpoint helper实现 tri-state beta 规则。
9. 把 credentialstore.ErrCredentialExpired 变成专门的 pre-delivery auth decision：
   - detached abort 当前 claim，释放 slot；
   - 排除当前账号；
   - 触发一次 hot refresh；
   - 消耗既有 auth-failover 子预算而非普通 retry budget；
   - 不向 channel health 写普通 5xx 降级；
   - 选号下一账号，最多一次。
10. 在注册放量前先落 strict official predicate；S1 仍只产生 OfficialDirect 或 Reject，不开放 RewriteRequired。不能沿用 UA substring-only 的现门，否则伪 UA 可在 S2 之前 raw pass-through。
11. 保持 Anthropic reverse account 的 official-only 策略；OfficialDirect 对进入 composer 的最终 body 字节等价。
12. 运行 unit、integration、race-sensitive resource tests；按 schema/serving 级别做 reviewer-lane cross-review。

S1 当前本地过期缺口的证据：adapter 会返回 credentialstore.ErrCredentialExpired，但 dispatch error 分类只把无法识别的本地错误归为 local_dispatch_error；现有热刷新/换号只由上游 OAuth 401/撤销分类触发。[HUAKAI: internal/provider/anthropic/oauth_session.go:98-113；internal/gateway/upstream_dispatcher.go:168-180；internal/gateway/attempt_error.go:175-214；internal/gatewayhttp/chat_completions_handler.go:558-609]

S1 验收门：

- registry、DB CHECK、admin、pool vendor、HCSF、scanner 的 family 集合完全对称；
- model/provider 精确配置能选到 OAuth/session 账号；
- wrong authMode 在配置时被拒，运行时仍 fail-closed；
- strict predicate 拒绝只伪造 UA/X-Client 的非官方请求；
- 官方 body 在 raw stream、raw buffered、HCSF buffered 三路的 composer 输入/输出字节等价；
- 默认官方 endpoint 有且只有一个 beta=true；
- 本地过期和上游 401 都能释放 A 账号资源、最多换到 B 一次并只结算 B；
- 任何 adapter/marshal/credential 失败都不留 reserving claim 或 acquired slot。

### S2：非官方准入与深拟真

前置：S1 已上线稳定；Owner 批准客户端策略与拟真合规风险。

执行顺序：

1. 复用 S1 的 Anthropic strict direct-pass predicate，将客户端门从两态改成三态 decision。
2. 新建 internal/claudeoauthmimicry，先完成纯函数 contract 与 mutation tests。
3. 完成固定 system、HUAKAI 自研 attribution、原 system 下沉；CCH 默认不发送。
4. 把现有 metadata、cache breakpoint、tools-tail 作为独立 optional steps 组合；不要让 metadata 前置条件控制必需步骤。
5. 完成工具名 request/response 双向闭环。
6. 把当前 identityRewrite 扩成 error-aware 的 outbound body composer hook，并在：
   - raw stream；
   - raw buffered；
   - HCSF canonical marshal 后
   三处都接到最终 Anthropic body。
7. 增加策略不变量：RewriteRequired 只有 composer profile 可用时才放行。
8. 先以 per-account/per-binding feature flag 灰度；official-only 保持默认。
9. 观测一段时间后，由 Owner 决定是否把 Anthropic reverse account 默认从 official-only 改为 rewrite_non_official。

S2 验收门：

- 官方请求三路 body 字节等价，工具名、cache、metadata 均不被覆盖；
- spoofed UA/X-Client 自报不会绕过拟真；
- 非官方请求得到固定三块 system、原 system 仅下沉一次、归因摘要可重复；
- 工具调用在客户端看到的名字与入站声明一致；
- 必需变换失败时没有上游 HTTP call，hold/quota/slot 各终结一次；
- profile flag 关闭时非官方请求仍被拒，绝不 raw pass-through。

## 6. 预期文件影响

此表是未来执行范围，不代表本计划已修改这些文件。

| 切片 | 预期文件/目录 | 目的 | 风险级别 |
| --- | --- | --- | --- |
| S1 | internal/provider/registrydefault/default.go、default_test.go | 注册与支持集正向化 | 中 |
| S1 | internal/provider/anthropic/oauth_session.go、oauth_session_test.go | vendor 归一、beta tri-state、typed expiry | 中 |
| S1 | internal/pool/api.go、vendor_guard_test.go | session family → anthropic | 中 |
| S1 | internal/gateway/protocol_selector.go、stream_scanner.go、upstream_dispatcher_hcsf.go 及既有 tests | 响应/SSE/HCSF 对称接线；不新增 gateway 生产文件 | 中 |
| S1 | internal/adminhttp 既有 catalog tests | 配置面正向可用与错误 authMode 拒绝 | 中 |
| S1 | sql/migrations/0174_*（最终编号执行前确认） | models CHECK 放开 | 高，Owner gate |
| S1 | internal/gateway/attempt_error.go、internal/gatewayhttp 既有 retry tests | 本地过期进入 auth failover；不新增大包生产文件 | 高，auth/资源生命周期 |
| S2 | internal/claudeoauthmimicry/ | 新的纯 body profile 与 tests | 中 |
| S2 | internal/officialclient/policy.go 或窄职责子包、internal/gatewayhttp/clientgate/gate.go | 三态准入与严格官方直发 | 高，auth/client access |
| S2 | internal/gatewayhttp/chat_completions_stream.go、handler.go、dispatch.go 既有接缝 | 三路组合；不新增 gatewayhttp 生产文件 | 中高 |
| S2 | internal/gateway/forwarder.go 或既有 canonical response 接缝 | 工具名逆变换 | 中高 |
| S2 | docs/specs、docs/process/decisions、feature flag 文档 | 固化 contract、风险、回滚 | 低 |

## 7. §17 模块配合与最易出错的交界

### 7.1 HUAKAI 资源生命周期

| 阶段 | 当前真实机制 | 本任务配合点 | 主要故障 |
| --- | --- | --- | --- |
| 计费/配额预留 | 先 Reserve billing claim，再 Reserve quota；quota 基础设施错误目前 fail-open | 任何后续本地失败都必须 abort 同一 claim | composer/adapter 在发网前失败却未 abort，留下 hold/quota reservation |
| 选号 | SelectionRequest 带 family、claim、排除账号；SQL 精确 family 过滤 | session family 与 provider 配置必须一致 | family 只注册 adapter、DB/SQL/模型未接，导致无号或半链路 |
| 并发槽 | selector Acquire 后把 acquisition token 写回 claim；写回失败立即 detached release | retry 前必须终结旧 attempt | slot 已增但 claim 未锚定；abort 走取消 ctx 导致泄漏 |
| 凭据物化 | vault 将 authMode 转 runtime credential，投影账号平台/类型 | compatibility 校验与本地过期 typed decision | 错 authMode 被选；本地过期落通用 502，不热刷新 |
| client gate/body | 当前 gate 在凭据后、dispatch 前；现 body hook默认只做 metadata | 三态 gate 与 composer 必须原子配合 | 非官方放行但 composer 空操作；官方 body 被重序列化 |
| adapter/transport | registry 按 family 取 adapter；transport 按账号平台/反转类型选 mimicry mode | adapter Platform 归一 anthropic | 使用 session 字符串造成 transport/vendor/计价分叉 |
| 上游错误 | OAuth 401 可触发热刷新、排除账号并消费一次 auth 子预算 | 本地 expiry 应同语义但无上游 call | 同一坏号被再次选中；刷新风暴；错误写入普通健康降级 |
| 结算/释放 | Settle 与 Abort 都在 DB 事务内释放 slot；post-delivery settle 失败进 DLQ | 重写不能改变 claim fingerprint；每 attempt 仅一次终结 | 已交付但 settle 失败，slot 只能待重放/lease；错误测试假定即时释放 |

证据：

- claim 与 quota 预留：[HUAKAI: internal/gatewayhttp/chat_completions_dispatch.go:329-427]
- slot acquire、claim token 回写与失败释放：[HUAKAI: internal/pool/router/default_selector.go:206-242；internal/pool/dispatcher/slot_manager.go:49-115]
- retry 排除账号与一次 auth 子预算：[HUAKAI: internal/gatewayhttp/chat_completions_handler.go:558-609]
- detached abort：[HUAKAI: internal/gatewayhttp/chat_completions_attempt.go:186-199,354-367]
- settle/abort 原子释放：[HUAKAI: internal/billing/settler.go:250-281,420-439]
- quota post-billing finalizer 与 release：[HUAKAI: internal/quotaenforce/settler.go:146-197]
- post-delivery DLQ：[HUAKAI: internal/gatewayhttp/chat_completions_billing.go:291-359]

### 7.2 参考项目给出的交界提醒

- CLIProxyAPI 的管理层把凭据选择、单凭据内的一次未授权刷新、结果标记与换凭据 retry 串在统一执行循环；这说明“刷新一次”和“换号一次”必须分别有明确预算，不能在 adapter 内自行无限 retry。<router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:sdk/cliproxy/auth/conductor.go:2315-2351,2565-2634>
- sub2api 的调度结果在选中后再 hydrate 完整账号凭据，并显式携带 release 回调；粘性账号拿到槽但会话限制失败时立即 release。这个行为提醒 HUAKAI：选号快照与 secret materialization 分离后，二者之间的任何失败都必须终结槽。<Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/gateway_scheduling.go:325-365,1386-1410>
- new-api 的控制器在 channel retry 外层复用请求 body、重选 channel，并把预扣与失败退款放在统一账务 session；其 Anthropic adapter 本身不承担选号或结算。HUAKAI 同样不应把 retry/slot/计费责任塞进 OAuth adapter。<QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:controller/relay.go:175-237,295-340> <QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:service/billing_session.go:184-220> <QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:relay/channel/api_request.go:307-334>

## 8. §14 判别测试清单：每项都有“变异 → 红”

测试不得只断言“不等于坏值”，必须断言完整好值、资源计数与调用次数。

| ID | 改动/场景 | 必须断言的好行为 | 变异后为何变红 |
| --- | --- | --- | --- |
| S1-AT-01 | registry 注册 | For(session family) 精确返回 OAuth adapter，Platform 精确等于 anthropic，支持集只出现一次 | 删除注册或 Platform 改回 family 字符串，类型/等值断言红 |
| S1-AT-02 | migration/支持集 | 每个支持 family 都在最新 CHECK；session family 正向出现；down 恢复上一 allowlist | 从 migration 或支持集删 session，双向集合断言红 |
| S1-AT-03 | 配置到选号 | model 与 provider 都配 session family 时只选 OAuth 账号，SelectionResult 含非零 token | 把 provider family 改为 anthropic_messages，必须得到 no eligible，证明精确接线 |
| S1-AT-04 | 凭据物化 | claude_ai_oauth 精确得到 OAuthAccessToken；claude_code 精确得到 SessionToken；Bearer 值精确 | 调换 runtime kind 或取错 secret 字段，类型/header 断言红 |
| S1-AT-05 | compatibility | API-key 账号不能保存/绑定到 session provider；运行时防御也不发 HTTP | 删除 admin 校验后 mutation fixture 被接受，状态码与零调用断言红 |
| S1-AT-06 | beta query | 默认官方 endpoint 精确为 /v1/messages?beta=true；false 无 query；custom 缺省无 query；已有其他 query 被保留且 beta 不重复 | 删除默认注入、无条件 custom 注入或字符串拼接重复键，URL 全值断言红 |
| S1-AT-07 | 协议对称 | session family 非流式 JSON 与流式 SSE 都经 Anthropic adapter；HCSF marshal 产出 Anthropic shape | 漏 protocol/scanner/marshal 任一登记，对称 guard 或端到端用例红 |
| S1-AT-08 | 官方直发与伪造拒绝 | strict 多信号官方 fixture 在 raw stream、raw buffered、HCSF 三路的 composer 输入/输出字节完全相同；只伪 UA/X-Client 的 fixture 精确 Reject | 官方分支先 unmarshal/remarshal 会使 bytes.Equal 红；退回 UA-only 会使伪造判决红 |
| S1-AT-09 | 本地 expires_at 过期 | A：abort 一次、slot 归零、hot refresh 一次、加入 excluded；B：被选中并 settle 一次；总 auth failover 一次 | 去掉 sentinel 分类会终态 502/500；漏 abort 会 claim/slot 计数红 |
| S1-AT-10 | 上游 401 | A 的 response 不交付、abort/refresh/exclude 各一次；B 成功；A 不写普通 5xx health degradation | 去掉 auth 子预算或排除集合会重选 A/多刷新，调用序列红 |
| S1-AT-11 | adapter/marshal 缺失 | 上游 HTTP 调用为 0，claim aborted，slot released，quota released | 删除 detached abort 后 DB 状态/spy 次数红 |
| S1-AT-12 | 并发槽 | cap=2、3 个并发请求时同时在途精确为 2；一个成功、一个 abort 后容量恢复为 2 | 去掉任一 settle/abort release，恢复容量断言红 |
| S2-AT-01 | strict 官方判别 | 满足 UA+headers+metadata+body 组合才返回 OfficialDirect，输出 body 字节相同 | 只保留 UA 判据时“UA 真、metadata 假”fixture 被误直发，判决红 |
| S2-AT-02 | spoof | curl/自报 X-Client/伪 UA 组合精确返回 RewriteRequired，不得 OfficialDirect | 复用 clientid 自报优先规则后判决红 |
| S2-AT-03 | system 重构 | system 精确为 3 个预期类别；原 system 不在 system，且只在 leading user 中出现一次；assistant acknowledgement 次序固定 | 只开 EnsurePrefix 或 ReplaceAll 时，块数/下沉/唯一性断言红 |
| S2-AT-04 | attribution digest | 同一逻辑 body 稳定同摘要；改变用户内容摘要必变；改变归因块本身不造成递归漂移；审计无原文 | 摘要包含自身或漏 messages，稳定性/差异性断言红 |
| S2-AT-05 | CCH 默认 | v1 profile 明确无 SignatureEvidence，wire 不出现 CCH；只有 Owner-approved flag + 已注册算法版本才可出现 | 默认打开或未知算法 fail-open 时精确字段集合断言红 |
| S2-AT-06 | 工具名往返 | declaration、历史 tool_use、tool_choice 出站同映射；流式和 buffered 返回均恢复客户端原名 | 删除任一路逆映射，客户端响应 HCSF/SSE 的 exact name 断言红 |
| S2-AT-07 | metadata 独立失败 | external account id 缺失时 system/attribution/tool 必需步骤仍生效，仅 metadata 标 optional skipped | 复用当前总短路逻辑后整个 body 未变，步骤矩阵红 |
| S2-AT-08 | 必需变换失败 | 不支持的 system block → typed error、上游调用 0、claim/slot/quota 各终结一次、不换账号 | fail-open 原 body 或把确定性错误当账号失败，调用/attempt 次数红 |
| S2-AT-09 | gate/composer 原子性 | profile disabled + 非官方 → 403/Reject；profile enabled + 非官方 → RewriteRequired 且 AnyApplied=true | 单独放宽 gate 而 composer 关闭，会出现 2xx/raw body，断言红 |
| S2-AT-10 | 官方三路旁路 | 官方 body、tool、cache、metadata 在三路逐字节/逐字段保持；composer audit 为 bypass | 官方误入任一步，bytes/字段集合红 |
| S2-AT-11 | 断连释放 | 客户端取消 ctx 后 detached abort 仍完成，claim aborted、slot 归零、quota released | abort 复用 request ctx 时超时，最终状态红 |
| S2-AT-12 | post-delivery settle 故障 | 已交付响应不反悔；recovery DLQ 恰一条；重放后 settle/slot release 恰一次，无重复 usage | 假定立即 release 或重复 enqueue/settle，状态机/唯一性断言红 |

额外测试纪律：

- 不允许遇到零字段就 t.Skip；
- 注释中的并发数必须与代码一致；
- winner/loser fixture 必须真的在 authMode/family/expiry 上有区分；
- SQL stub 必须包含生产 WHERE 的 family、credential_state 与 tenant 条件；
- gate chain 测试必须至少覆盖 protocol mismatch、credential mismatch、slot busy，不得全用 AllowAll。

## 9. 错误语义与回滚

### 9.1 typed error 分类

| 错误 | 是否换号 | 是否刷新 | 是否发上游 | claim/slot/quota |
| --- | --- | --- | --- | --- |
| 本地 OAuth 凭据过期 | 是，最多一次 auth 子预算 | 是，一次 | A 不发；B 可发 | A detached abort |
| 上游 401 invalid/revoked | 是，最多一次 auth 子预算 | 是，一次 | A 已发；B 可发 | A detached abort |
| body InvalidShape | 否 | 否 | 否 | detached abort，终态 400 或内部 contract 422 |
| profile flag 关闭且非官方 | 否 | 否 | 否 | detached abort，403 policy reject |
| profile 已启用但配置/修订不可用 | 否 | 否 | 否 | detached abort，503 profile unavailable |
| adapter 未注册/marshal 缺失 | 否；这是部署不一致 | 否 | 否 | detached abort，500/502 + high-severity metric |
| transport 暂态 | 按现有普通 retry budget | 否 | 可能 | 每 attempt 独立 abort |
| post-delivery settle 失败 | 不重发请求 | 否 | 已完成 | DLQ 重放；不得重复 billing |

### 9.2 回滚顺序

- S2 事故：先把深拟真 flag 关回 official-only；S1 serving 仍服务官方客户端。
- S1 adapter/协议事故：从 model binding 移除 session family 流量，再关闭 provider；不要先回滚 DB CHECK，因为已有模型行可能阻止 down migration。
- schema down 前先确认没有 models 行使用 session family；否则 down 必须 fail-loud，不得删/改模型数据。
- 任何回滚都不能修改 LICENSE、删除账号或清空凭据。

## 10. 爆炸半径与 Owner-gated 决策

### 10.1 高风险 Owner gate

1. Schema：新增 protocol family 到 models CHECK，涉及 ACCESS EXCLUSIVE lock；Owner 批准维护窗口与 migration。
2. Client access：Anthropic reverse account 从 official-only 放宽为 RewriteRequired，属于 auth/client access core；Owner 决定默认值、per-account 覆盖与 rollout。
3. 上游拟真/合规：固定身份、归因、设备/header/body 组合可能触及 Anthropic 条款与账号风控；Owner/法律决定是否启用。
4. CCH：两镜证据冲突；Owner 决定是否先做一方流量验证、是否永远不实现、或用实验 flag。
5. Money-path review：实现虽不改变价格/ledger schema，但错误路径会触及 claim、quota、slot；必须按 money-path full reviewer-lane 验证。

### 10.2 中风险但可实施

- adapter Platform 归一；
- protocol/scanner/HCSF/vendor 对称登记；
- beta tri-state；
- 新纯 body package；
- typed local expiry 分类；
- 工具名 request/response 逆变换。

### 10.3 功能缩水检查

没有静默删功能：

- OAuth/session serving：S1 Implemented；
- 非官方客户端 body 拟真：S2 Feature Flag，Owner 启用后 Implemented；
- 官方客户端无覆盖：S1/S2 都 Implemented Better，以三路字节等价锁定；
- CCH：Mandatory Decision / Feature Flag，不假装当前已支持；
- count_tokens：Mandatory Roadmap，不混进 messages 范围；
- 工具名拟真：S2b，只有双向闭环完成才启用，不以“只改请求”冒充完成。

## 11. 我预计与 Claude 版本最可能分歧的地方

1. 切片：我主张 S1 serving 与 S2 拟真强制拆开；若另一版合并，我认为回滚耦合和半链路风险不可接受。
2. 落点：我主张新 claudeoauthmimicry 子包 + 最终 body 接缝；不赞成 adapter 内改 body，也不赞成只打开现有 SystemRewrite。
3. 协议身份：我主张 protocol family 保持 anthropic_claude_session，但 adapter.Platform 改为 anthropic；不赞成新增虚构 transport vendor。
4. 路由：我主张 model/provider 显式 family + compatibility validation；不赞成 credentialstore 根据 authMode 隐式改路由。
5. 官方判别：我主张多信号 strict predicate，且 X-Client 自报不参与直发；不赞成 UA prefix-only 或复用宽松 clientid。
6. beta：我主张默认官方 endpoint 带 true，自定义 endpoint 缺省不带；不赞成对所有 base URL 无条件拼接。
7. CCH：我主张 v1 默认不发、等待一方证据；不赞成从任一镜像复制算法。
8. 失败策略：必需拟真失败应 fail-closed 并 abort，不应 raw body fail-open；metadata 等非必需步骤可独立 fail-open。
9. 工具名：我主张请求/响应闭环是启用前置；不赞成只改请求声明。
10. rollout：我主张非官方准入与 composer 可用性是一个原子策略；不赞成先全局放宽 gate 再补 body。

## 12. Pre-execution checklist

在任何生产代码修改前：

1. Owner 对 §10.1 五项逐项给结论。
2. Claude/Codex 两份独立计划完成 agree/conflict/gaps 交叉讨论，写出无后缀 synthesized plan。
3. 重新 fetch 三镜到独立只读 snapshot，记录新 SHA；若行为变化，重新做 specifier/reviewer lane 分离。
4. 独立读取当前官方协议/一方流量证据，确定 profile version、归因 wire contract、CCH 与 strict official 信号。
5. 查询最新 migration 编号；跑只读 SQL 统计 models 行数、协议值与潜在锁等待。
6. 检查 codebudget baseline；确认 gateway/gatewayhttp 不新增生产文件。
7. 先写 S1 acceptance contract 与 mutation tests，再写实现。
8. S1 只 stage 自身文件，跑 unit/integration；schema/serving 用 full reviewer-lane；无 S0/S1 才提交。
9. S1 staging 验证官方 body 三路字节等价、slot/claim/quota 归零与本地过期换号。
10. S2 先写纯 package 与三态 gate contract；在工具逆映射全覆盖前保持总 flag off。
11. S2 staging 只使用测试账号与合规允许的 endpoint，不使用 Owner 未批准的真实订阅账号。
12. 灰度指标至少含：client decision、rewrite step result、typed error、hot refresh、auth failover、claim age、slot lease、settle DLQ；不得记录 body/token。
13. 每个 commit 依 AGENTS.md 做 Codex review；schema/auth/money 交界使用完整 cross-review。

## 13. 最不确定的三点 / Open Questions

1. 当前官方 CCH 与归因格式究竟是什么：本地两镜在相近时间的 commit 上互相冲突，且本任务未 fetch 远端。没有一方流量或官方规格，不能诚实定论。
2. “官方 Claude Code 直发”的稳定判据：UA、header、metadata 与 system 结构会随客户端版本变化；需要至少两个当前官方版本的 messages/count_tokens/探测样本，才能定兼容窗口与降级策略。
3. 自定义 endpoint 的 beta 与 profile 语义：官方 endpoint 默认 beta=true 有两镜证据；自建 relay 是否接受/需要同一 query 与 body profile 取决于 operator contract，必须明确“官方默认”与“custom 显式 opt-in”的边界。

## 14. Source Coverage Proof

### 14.1 HUAKAI

| 区域 | 贡献 |
| --- | --- |
| internal/provider/anthropic/oauth_session.go:16-115 | OAuth/session adapter 的凭据、body、headers、expiry 与 Platform |
| internal/provider/anthropic/passthrough.go:23-90 | 默认 endpoint 与现有 beta query helper |
| internal/provider/registrydefault/default.go:69-146,212-237 | family 常量、支持集、未注册事实 |
| internal/provider/registry.go:31-65 | family 到 adapter 的精确查找 |
| internal/credentialstore/types.go:42-99,213-278 | authMode 到 runtime kind，不负责 protocol |
| internal/provider/postgres_vault.go:74-184,286-307 | v2 物化与 AccountInfo 投影 |
| sql/migrations/0172_models_protocol_family_registered_adapters.up.sql:9-57 | CHECK allowlist 与锁 |
| internal/router/default_router.go:40-97 | Router 只计划 attempts |
| internal/gatewayhttp/chat_completions_dispatch.go:181-234,329-427,449-520,540-589 | resolved family、claim/quota、选号、凭据与 client gate |
| internal/pool/dispatcher/account_source.go:34-66 | family 进入 DB account source |
| internal/db/billing/pool_accounts.sql.go:664-716 | provider family 与 credential state 精确过滤 |
| internal/pool/router/gates.go:241-250 | snapshot family gate |
| internal/gateway/upstream_dispatcher.go:133-180 | adapter 选择与 BuildRequest |
| internal/gateway/upstream_dispatcher_hcsf.go:290-342,381-425 | 最终 HCSF body 接缝与 marshal family |
| internal/gateway/protocol_selector.go:91-168 | response adapter registry |
| internal/gateway/stream_scanner.go:171-247 | SSE scanner registry |
| internal/pool/api.go:17-102；internal/pool/vendor_guard_test.go:10-67 | vendor 映射与对称守卫 |
| internal/transport/policy.go:21-36,118-128 | Anthropic transport vendor/mode |
| internal/mimicryidentity/identity.go:67-125 | 当前仅 metadata plan 与总短路条件 |
| internal/gateway/mimicry_compose.go:57-86,106-243 | 现有六步 composer |
| internal/gateway/system_rewrite.go:46-67,160-218 | system 原子能力边界 |
| internal/gateway/tool_name_rewrite.go:33-114,117-269 | 请求侧工具名能力与逆变换缺口 |
| internal/gatewayhttp/chat_completions_stream.go:127-175；internal/gatewayhttp/chat_completions_handler.go:728-756 | raw stream/buffered body hook |
| internal/officialclient/policy.go:40-99；internal/gatewayhttp/clientgate/gate.go:35-50 | 当前 official-only 判决 |
| internal/clientid/clientid.go:44-124,168-184 | 当前宽松客户端检测 |
| internal/gateway/hcsf_graph_marshal_test.go:939-965 | family marshal 对称守卫 |
| internal/adminhttp/provider_catalog_mutation_handler.go:328-372,484-485 | admin 支持集接线 |
| internal/provider/registrydefault/default_test.go:62-78,372-381 | 当前 fail-closed tests |
| internal/pool/router/default_selector.go:206-242；internal/pool/dispatcher/slot_manager.go:49-115 | slot acquire 与 claim token |
| internal/gatewayhttp/chat_completions_handler.go:517-610 | retry、排除账号、auth 子预算 |
| internal/gateway/attempt_error.go:175-214；internal/gatewayhttp/chat_completions_error.go:178-203 | dispatch/401 分类与 hot refresh |
| internal/gatewayhttp/chat_completions_attempt.go:186-199,354-367 | detached abort 与 attempt reset |
| internal/billing/settler.go:250-281,420-439 | settle/abort 释放 slot |
| internal/quotaenforce/settler.go:146-197 | quota finalize/release |
| internal/gatewayhttp/chat_completions_billing.go:291-359 | post-delivery recovery DLQ |

### 14.2 参考镜像

| 区域 | 贡献 |
| --- | --- |
| CLIProxyAPI executor body/HTTP 区域 | OAuth body、query、归因/system、CCH 的本地观察。<router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/runtime/executor/claude_executor.go:266-290,1829-1957> <router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/runtime/executor/claude_signing.go:15-40> |
| CLIProxyAPI client 判别区域 | UA 前缀直发判据。<router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/runtime/executor/helps/cloak_utils.go:39-56> |
| CLIProxyAPI credential conductor 区域 | 刷新一次、结果标记、换凭据 retry 的交界。<router-for-me/CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:sdk/cliproxy/auth/conductor.go:2315-2351,2565-2634> |
| sub2api gateway forward/body 区域 | 官方 bypass、非官方 body 变换落点。<Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/gateway_forward.go:150-210> <Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/gateway_claude_oauth_body.go:857-941> |
| sub2api billing attribution 区域 | 版本、短指纹、入口与当前无 CCH。<Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/gateway_billing_block.go:73-94> |
| sub2api validator/helper 区域 | 组合信号官方判别。<Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/claude_code_validator.go:67-145> <Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/handler/gateway_helper.go:24-59> |
| sub2api upstream request/scheduling 区域 | 默认 beta、hydrate 与 release。<Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/gateway_upstream_request.go:21-48> <Wei-Shaw/sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/gateway_scheduling.go:325-365,1386-1410> |
| new-api Anthropic adapter 区域 | API-key header 与可选 beta。<QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:relay/channel/claude/adaptor.go:44-98> |
| new-api controller/channel/billing 区域 | channel retry、adapter 边界与预扣/结算。<QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:controller/relay.go:175-237,295-340> <QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:service/billing_session.go:184-220> <QuantumNous/new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:relay/channel/api_request.go:307-334> |

## 15. 计划结论

最小安全路径不是“注册 adapter 后顺手改 body”，而是先闭合 session family 的六站对称接线与资源生命周期，再以独立策略切片启用非官方深拟真。最终架构应保持：显式 protocol family 路由、anthropic vendor 身份、集中且 error-aware 的最终 body composer、严格官方直发、非官方必改写、工具名双向闭环、CCH 默认缺省、所有 pre-delivery 失败 detached abort。这样没有功能缩水，也不会用参考镜像的实现细节替代 HUAKAI 自己的 contract。

Source files read:
- HUAKAI: CLAUDE.md; AGENTS.md; docs/RULES.md; docs/05_CLEAN_ROOM_POLICY.md; internal/provider/anthropic/oauth_session.go; internal/provider/anthropic/oauth_session_test.go; internal/provider/anthropic/passthrough.go; internal/provider/registrydefault/default.go; internal/provider/registrydefault/default_test.go; internal/provider/registry.go; internal/credentialstore/types.go; internal/provider/postgres_vault.go; sql/migrations/0172_models_protocol_family_registered_adapters.up.sql; internal/router/default_router.go; internal/gatewayhttp/chat_completions_dispatch.go; internal/gatewayhttp/chat_completions_stream.go; internal/gatewayhttp/chat_completions_handler.go; internal/gatewayhttp/chat_completions_attempt.go; internal/gatewayhttp/chat_completions_error.go; internal/gatewayhttp/chat_completions_billing.go; internal/pool/dispatcher/account_source.go; internal/db/billing/pool_accounts.sql.go; internal/pool/router/gates.go; internal/pool/router/default_selector.go; internal/pool/dispatcher/slot_manager.go; internal/pool/api.go; internal/pool/vendor_guard_test.go; internal/gateway/upstream_dispatcher.go; internal/gateway/upstream_dispatcher_hcsf.go; internal/gateway/upstream_dispatcher_hcsf_test.go; internal/gateway/protocol_selector.go; internal/gateway/stream_scanner.go; internal/gateway/hcsf_graph_marshal_test.go; internal/gateway/mimicry_compose.go; internal/gateway/system_rewrite.go; internal/gateway/tool_name_rewrite.go; internal/mimicryidentity/identity.go; internal/officialclient/policy.go; internal/clientid/clientid.go; internal/gatewayhttp/clientgate/gate.go; internal/adminhttp/provider_catalog_mutation_handler.go; internal/transport/policy.go; internal/billing/settler.go; internal/quotaenforce/settler.go
- CLIProxyAPI: internal/runtime/executor/helps/cloak_utils.go; internal/runtime/executor/claude_executor.go; internal/runtime/executor/claude_signing.go; sdk/cliproxy/auth/conductor.go
- sub2api: backend/internal/service/gateway_service.go; backend/internal/service/gateway_upstream_request.go; backend/internal/service/gateway_forward.go; backend/internal/service/gateway_billing_block.go; backend/internal/service/gateway_claude_oauth_body.go; backend/internal/service/claude_code_validator.go; backend/internal/service/gateway_scheduling.go; backend/internal/handler/gateway_helper.go; backend/internal/handler/gateway_handler.go; backend/internal/handler/openai_gateway_handler.go
- new-api: relay/channel/claude/adaptor.go; relay/relay_adaptor.go; relay/claude_handler.go; relay/channel/api_request.go; model/channel.go; service/channel_select.go; service/billing_session.go; service/text_quota.go; controller/relay.go
Lane: specifier
Agent: OpenAI Codex / GPT-5
UTC timestamp: 2026-07-10T07:39:02Z
