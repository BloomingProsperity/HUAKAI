# 2026-07-21 账号导入与用户创建全链路问题报告

| 项目 | 结论 |
| --- | --- |
| 审计对象 | HUAKAI `fcb82c7e`，与 `origin/main@95b60260` 文件树一致 |
| 成熟项目基线 | Sub2API `5a8d6c4e41e38f05cea4164e6ff03443fc0f6923` |
| 审计方式 | 只读源码、生产接线与测试交叉核实；未修改业务代码 |
| Observed regions | 41 |
| Inferences | 0；爆炸半径属于基于 HUAKAI 调用关系的风险分析，不冒充已发生的生产事故 |
| Open questions | 2 |

## 一、总判断

本轮确认 **14 个问题**：`S1` 级 11 个，`S2` 级 3 个。最危险的不是某一个 URL 写错，而是同一业务存在多套入口、权限合同和提交方式：正规的账号导入链已经具备预检、冲突、事务和日志闭环，但旧的直接创建入口仍可绕开来源合同并分步写库；用户创建则在部署者、租户管理员、终端用户三种身份边界、首装抢占、匿名限流和提交后补偿上存在断口。

当前测试全绿不代表这些问题不存在。现有测试主要证明单个入口的正常路径，而同一模式的旁路、事务中段失败、邮件/日志/奖励失败和 Owner 已定的三身份边界没有被完整覆盖。

## 二、问题总表

| ID | 等级 | 问题 | 主要爆炸半径 |
| --- | --- | --- | --- |
| AI-01 | S1 | OAuth-only 来源合同可被通用导入和直接建号绕过 | 多家 OAuth 账号、身份/套餐可信度、刷新与日志口径 |
| AI-02 | S1 | 直接建账号入口不是原子链 | 孤儿账号、假失败真生效、健康未初始化、重试重复 |
| AI-03 | S1 | 账号导入的三身份权限合同互相矛盾 | 部署者自己的账号无法导入，另一路却可指名任意租户 |
| AI-04 | S1 | Claude 浏览器 OAuth 与 Cookie/刷新配置分裂 | 首次换码失败、后续刷新失败、同厂商行为漂移 |
| AI-05 | S2 | 交互式 OAuth 仍要求先建空账号 | 所有浏览器/设备授权账号的运维体验和恢复复杂度 |
| AI-06 | S2 | Claude OAuth 存在两套实现与部分僵尸接线 | 后续修改改错位置、配置再次漂移 |
| UC-01 | S1 | 部署者可跨租户操作下级租户终端用户 | 全部租户用户的创建、读取、删除、状态与恢复操作 |
| UC-02 | S1 | 首装管理员可被公网抢先注册 | 整个新部署被接管 |
| UC-03 | S1 | 公开注册没有独立限流，验证码可静默退化为空操作 | 匿名 Argon2 内存/CPU 与数据库资源耗尽 |
| UC-04 | S1 | 社交注册的“建用户 + 绑身份”不在同一事务 | 孤儿用户、邮箱占用、重试失败、登录死路 |
| UC-05 | S1 | 管理端建用户与写日志不在同一事务 | 接口报失败但用户已存在，且无可信操作日志 |
| UC-06 | S1 | 注册奖励失败被静默吞掉且无恢复任务 | 用户少余额、邀请奖励丢失、客服只能人工补账 |
| UC-07 | S2 | 验证邮件硬失败发生在用户提交之后，且无重发入口 | 待验证用户被卡死、接口假失败、日志缺口 |
| UC-08 | S1 | 四种建号入口的邮箱、密码、名称校验互相漂移 | 弱密码、不可投递邮箱、异常名称进入库和 UI |

## 三、账号导入与账号创建

### AI-01：OAuth-only 来源合同可被两条旁路绕过（S1）

**源码事实**

- 模式表明确把 Claude 浏览器 OAuth、ChatGPT OAuth、Codex Web OAuth、Gemini Code Assist、Gemini Google One 和 Grok OAuth 定义为只允许交互式 OAuth：`backend/internal/credentialacq/types.go:255-293`、`backend/internal/credentialacq/types.go:335-370`。
- 正规临时会话入口确实校验来源种类：`backend/internal/credentialacq/session_store.go:105-129`；对应测试也明确要求 OAuth-only 拒绝粘贴和 JSON：`backend/internal/credentialacq/anthropic_oauth_test.go:552-595`。
- 通用 JSON/CSV 导入允许输入自己声明 `vendor/auth_mode`：`backend/internal/credentialacq/cli_import.go:202-242`。导入预检只检查模式是否发布及载荷形状，没有把 `source_kind` 映射回模式允许的来源：`backend/internal/credentialacq/intake/plan.go:203-260`。
- 旧的直接建账号入口同样只检查协议与凭据形状，测试甚至把手写 Claude OAuth access/refresh token 当作合法正向用例：`backend/internal/gatewayhttp/admin_pool_accounts_handler.go:255-340`、`backend/internal/gatewayhttp/admin_pool_accounts_handler_test.go:487-500`。

**爆炸半径**

- 触发者必须先拥有相应管理权限，不是匿名远程攻击；但一旦进入管理面，就能把手写、窃取或来源不明的 token 标成“官方 OAuth 获取”。
- 直接影响账号身份、订阅套餐、到期时间和凭据来源可信度；间接影响刷新 worker、额度健康、账号选号和运营日志，因为后续模块会相信错误的 `auth_mode`。
- 不影响正规 OAuth 临时会话本身的 state/PKCE 校验；漏洞在其两个兄弟写入口。

**修复建议**

以 HUAKAI 现有 `ModePlan` 为唯一来源合同，在最终写凭据之前统一校验“模式 + 获取证据 + 来源种类”，而不是只在某个 HTTP 入口校验。通用导入只允许该模式声明的导入来源；OAuth-only 必须携带由服务端签发且已消费的短期授权证据。直接建号入口不再接受 OAuth 凭据，统一委托给账号导入执行链。

### AI-02：旧的直接建账号入口不是原子链（S1）

**源码事实**

旧入口依次执行账号插入、凭据插入、健康初始化和管理日志：`backend/internal/gatewayhttp/admin_pool_accounts_handler.go:318-407`。凭据失败时只做一次忽略错误的软删除补偿：`backend/internal/gatewayhttp/admin_pool_accounts_handler.go:341-347`；日志失败时直接返回 503，但账号和凭据已经生效：`backend/internal/gatewayhttp/admin_pool_accounts_handler.go:390-394`。

相反，新的账号导入执行链已经在同一事务内完成协议复核、账号、凭据和管理日志：`backend/internal/gatewayhttp/accountintake/execute.go:206-323`。这说明 HUAKAI 内部已经有正确范式，不需要再发明第二套。

**爆炸半径**

- 凭据写失败且软删除也失败：留下无可用凭据的活动账号，可能进入选号候选。
- 健康初始化失败：接口仍可能成功，但运营面与调度健康口径不完整。
- 日志或最终读取失败：调用者收到 503，真实账号却已创建；重试会生成重复账号或撞唯一约束。
- 影响只限本次指定租户，但会扩散到账号池、健康、刷新、模型同步、调度和人工排障。

**修复建议**

保留一个创建核心：所有“带凭据建号”统一走 `accountintake` 的事务执行；健康初始化作为提交后的幂等任务，并有待处理状态、重试和日志。成熟对照的前端“两次调用”只能作为用户体验证据，不能照搬其非原子边界。

### AI-03：账号导入的角色合同互相矛盾（S1）

**源码事实**

- 正式账号导入只接受带 capability 的 `tenant_operator` 专用令牌，明确拒绝部署者和管理员会话：`backend/internal/gatewayhttp/accountintakehttp/handler.go:154-201`。
- 已有账号的 OAuth 获取又只接受 `platform_admin`：`backend/internal/gatewayhttp/credentialacqhttp/handler.go:398-449`。
- 直接建账号同时放行两种角色，并允许 `platform_admin` 指名任意租户：`backend/internal/gatewayhttp/admin_pool_accounts_handler.go:656-690`、`backend/internal/admin/operator_auth.go:140-157`。

**爆炸半径**

部署者无法使用最完整、最安全的导入链管理自己的平台租户，只能绕回旧入口；而旧入口权限更宽、事务更弱。租户管理员则能导入，但不能复用同一套已有账号 OAuth 获取合同。权限差异直接诱导运营人员选择风险更高的入口。

**修复建议**

按 Owner 的三身份定性建立能力矩阵：部署者只管理平台所属租户；下级租户管理员仅在部署者显式授权 capability 后管理自己的上游账号；终端用户永远无此权限。权限检查应由同一 tenant capability 服务完成，HTTP 入口不再各自硬编码角色。

### AI-04：Claude 浏览器 OAuth 与 Cookie/刷新配置分裂（S1）

**源码事实**

- HUAKAI 浏览器授权使用 `api.anthropic.com` token 端点、localhost 回调和三项 scope：`backend/internal/credentialacq/anthropic_oauth.go:21-30`、`backend/internal/credentialacq/anthropic_oauth.go:125-145`、`backend/internal/credentialacq/anthropic_oauth.go:228-276`。
- HUAKAI 刷新器默认也使用同一个旧 token 端点：`backend/internal/credentialworker/adapters/anthropic.go:14-66`。
- 同仓库 Cookie 导入却使用平台 token 端点、官方回调和完整会话能力 scope：`backend/internal/credentialacq/claudecookie/exchange.go:23-30`、`backend/internal/credentialacq/claudecookie/exchange.go:186-271`。
- 当前成熟对照的浏览器授权与刷新统一使用平台 token 端点及官方回调；浏览器 scope 包含建 key 权限，Cookie 内部换码使用不含该权限的会话 scope：`Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:backend/internal/pkg/oauth/oauth.go:16-31`、`Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:backend/internal/service/oauth_service.go:64-120`、`Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:backend/internal/service/oauth_service.go:175-239`、`Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:backend/internal/repository/claude_oauth_service.go:174-262`。

**对先前 AI 表格的纠正**

- Client ID 相同：真实。
- “HUAKAI 浏览器 scope 缺少 `org:create_api_key`”：不真实，生产浏览器实现已经包含；缺少它的是另一套未进入生产主链的旧实现。
- token 端点和回调不同：真实。
- “HUAKAI 没有 sessionKey 自动授权”：不真实，Cookie 导入已存在且多组织时要求人工选择，反而比成熟对照静默选组织更稳。

**爆炸半径**

只影响 Claude 浏览器 OAuth 首次换码及其后刷新，不影响 Claude Cookie 导入和 Setup Token。首次可能直接失败；即使历史环境兼容，刷新时仍可能在运行数小时后批量失效，继而触发健康降级、选号减少和请求失败。

**修复建议**

建立一个厂商级不可变 OAuth profile，浏览器换码与刷新共用 token/client/redirect 合同；Cookie 和 Setup Token 只按用途选择不同 scope，不能各自维护整套常量。先用真实测试号验证“授权、换码、身份、刷新、一次模型请求”五段，再替换生产 profile。

### AI-05：交互式 OAuth 没有“授权后直接创建账号”的后端闭环（S2）

账号导入路由已有通用、Codex、Agent、Claude Setup Token、Claude Cookie、CRS 和整包迁移，但没有浏览器 OAuth staged-create 路由：`backend/internal/gatewayhttp/accountintakehttp/handler.go:74-91`。通用 OAuth 获取强制要求已有 `provider_account_id`：`backend/internal/gatewayhttp/credentialacqhttp/handler.go:398-413`。

成熟对照在一个 UI 向导里完成“换 token 后再创建账号”，但后端仍是两次调用，并非原子操作：`Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:backend/internal/handler/admin/account_handler.go:2017-2116`、`Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:frontend/src/components/account/CreateAccountModal.vue:6099-6102`、`Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:frontend/src/components/account/CreateAccountModal.vue:6128-6248`。

**修复建议**

不要复制两次调用。把 OAuth 回调结果写入 HUAKAI 已有短期加密 staging，随后复用预检/确认/原子执行创建账号；覆盖 Claude、ChatGPT/Codex、Gemini/Antigravity、Grok 和 Kimi 已发布交互模式。

### AI-06：Claude OAuth 两套实现造成部分僵尸接线（S2）

生产换码实际使用 `credentialacq` 实现；`anthropicoauth` 包仍保留另一套 profile、流程和换码器：`backend/internal/anthropicoauth/client_id.go:5-32`、`backend/internal/anthropicoauth/flow.go:14-59`、`backend/internal/anthropicoauth/exchanger.go:20-153`。默认 provider 注册仅空导入该包，但该包没有自动注册入口；生产刷新只借用了它的 client ID 和 HTTP client：`backend/internal/provider/registrydefault/default.go:46-51`、`backend/internal/credentialworker/adapters/anthropic.go:11-14`。

**修复建议**

先抽出唯一 profile/HTTP client 所有权，再删除未被生产调用的流程与换码器；用启动接线测试证明唯一实现存在。不能直接整包删除，因为刷新器仍引用其中两个公共部件。

## 四、平台用户创建

### UC-01：部署者可越级操作下级租户终端用户（S1）

`platform_admin` 的共享授权方法对任意 tenant 直接放行：`backend/internal/admin/operator_auth.go:140-157`。用户管理共享解析器因此允许部署者只要带 `tenant_id` 就进入任意租户：`backend/internal/adminuserhttp/tenant_scope.go:14-41`、`backend/internal/adminuserhttp/routes.go:396-427`；测试还把该行为锁成正向合同：`backend/internal/adminuserhttp/routes_test.go:752-823`。

**爆炸半径**

这不是只影响“创建用户”。同一解析器覆盖列表、详情、创建、删除、解锁、强制关闭 2FA、清 passkey、改分组、备注、状态、余额历史、用量和解绑社交身份：`backend/internal/adminuserhttp/routes.go:147-165`。这与 Owner 已定规则“部署者只可增减下级租户管理员余额，不可越级操作下级租户管理员的终端用户”直接冲突。

**修复建议**

不能继续复用“可为租户签 key”的粗粒度能力判断。拆成至少三项：平台自身租户用户管理、下级租户实体/余额管理、下级租户终端用户管理；第三项对部署者永久拒绝，只有该租户管理员可执行。日志可供部署者查看合规摘要，但不能反向变成操作权限。

### UC-02：首装管理员存在公网抢占窗口（S1）

`/setup/status` 和 `/setup/install` 都无鉴权；唯一门是“当前没有管理员”：`backend/internal/setuphttp/setuphttp.go:34-75`。事务锁只保证并发请求恰好一个获胜：`backend/internal/setuphttp/setuphttp.go:138-200`。测试也只证明单赢家，不区分赢家是不是部署者：`backend/internal/setuphttp/setuphttp_integration_test.go:139-180`。

**爆炸半径**

新实例若在部署者完成首装前暴露端口，任何可访问者都能抢先创建第一个已验证、已激活的管理员，取得整个部署控制权。锁反而会稳定地把真正部署者挡在外面。

**修复建议**

保持现有事务锁，再增加部署时生成的一次性 bootstrap secret，并默认只监听 loopback/内网或要求部署者 CLI 完成首装；成功后立即销毁 secret 并永久关闭入口。该 secret 属于部署者身份，不得复用租户或终端用户凭据。

### UC-03：公开注册缺少独立限流，Captcha 可退化为空操作（S1）

公开注册路由直接进入 handler，没有注册专用限流中间件：`backend/internal/gatewayhttp/auth_handler.go:188-203`。Captcha 未配置 secret 时明确返回成功：`backend/internal/captcha/verifier.go:135-150`。随后每次请求都会执行 64 MiB、3 轮 Argon2：`backend/internal/userauth/service.go:178-205`、`backend/internal/userauth/password.go:22-48`。

成熟对照把注册入口放在分布式限流器后，并在限流存储故障时关闭入口：`Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:backend/internal/server/routes/auth.go:24-45`。

**爆炸半径**

匿名请求可持续消耗内存、CPU 和数据库连接，影响登录、网关请求、刷新 worker 和管理面，不局限于注册模块。Captcha 只能作为反滥用补充，不能替代资源限流。

**修复建议**

在哈希和数据库之前增加“IP + tenant + 并发槽”的注册专用限流；生产模式分布式限流后端故障时 fail-closed，同时保留进程内并发硬上限。Captcha 未配置时应有明确日志和发布门，不应让运维误以为保护已启用。

### UC-04：社交注册的建用户与绑身份不在同一事务（S1）

有已验证邮箱的新社交用户先创建用户、再绑定身份：`backend/internal/userauth/social_login.go:278-294`；补邮箱路径同样分两次写：`backend/internal/userauth/social_login.go:350-366`。同文件后面的“给已有用户绑定身份”已经会使用 `withStoreTx`，说明事务能力现成：`backend/internal/userauth/social_login.go:379-390`。

**爆炸半径**

身份绑定失败时接口返回错误，但用户已经存在并占用邮箱；重试会走“已有邮箱”分支或冲突，用户既不能社交登录，也未必有本地密码恢复。注册奖励也不会执行。该问题覆盖所有首次社交建号来源，不影响已经绑定身份的正常登录。

**修复建议**

复用 `withStoreTx`，把用户创建、社交身份绑定和必要的邀请绑定放进同一事务；事务提交后再通过幂等 outbox 触发奖励和通知。测试必须注入“建用户成功、绑身份失败”，并断言数据库零残留。

### UC-05：管理端建用户与写日志不在同一事务（S1）

管理端先调用独立的用户创建 store，再单独写管理日志：`backend/internal/adminuserhttp/user_create.go:107-159`、`backend/internal/adminuserhttp/user_create.go:177-201`。日志失败时返回 503，但用户不会回滚。相同包的解锁操作已经实现“状态更新 + 日志同事务”：`backend/internal/adminuserhttp/routes.go:104-145`。

**爆炸半径**

租户管理员看到失败会重试，第二次得到邮箱重复；真实用户已能登录，却没有可信创建日志。叠加 UC-01 时，部署者还可能跨租户制造无日志用户。

**修复建议**

按本包已有解锁事务范式实现 `CreateUserWithLog`，用户与日志同提交/同回滚；不要在 handler 里串两次独立 store 调用。新增日志失败和提交失败的真 SQL 测试。

### UC-06：注册奖励错误被静默吞掉且没有恢复任务（S1）

密码注册和社交注册在用户提交后调用奖励函数，但两个错误都被直接丢弃：`backend/internal/userauth/service.go:518-530`。生产确实接入了注册奖励和被邀请人奖励：`backend/cmd/gateway/wiring.go:1135-1149`。奖励服务本身已经具备可重放的幂等键、串行事务和日志：`backend/internal/payment/signup_invitee_reward.go:130-235`、`backend/internal/payment/signup_invitee_reward.go:312-390`，缺的是失败任务被可靠保存和重试。

**爆炸半径**

数据库或连接瞬时失败时，注册仍返回成功，但用户永久少余额；邀请关系已写入，却没有对应奖励。由于错误既不记录日志事件也不进入 DLQ，客服无法自动区分“配置为 0”与“应该发但失败”。

**修复建议**

注册事务内只写一条幂等奖励意图，提交后由 worker 发放；失败进入现有 outbox/DLQ，并带 tenant、user、奖励种类、重试状态和人工恢复入口。奖励服务现有幂等实现可直接承接重放，不应把钱并入用户创建大事务。

### UC-07：验证邮件硬失败发生在用户提交之后，且没有重发入口（S2）

用户及验证 token 已在事务中提交，HTTP handler 随后才限流并发送邮件：`backend/internal/userauth/service.go:203-258`、`backend/internal/gatewayhttp/auth_handler.go:222-251`。生产邮件发送在临时失败时会进入 outbox，但永久配置错误或 outbox 写失败仍返回硬错误：`backend/internal/email/sender_factory.go:85-112`。公开路由只有注册和验证，没有“重发验证邮件”：`backend/internal/gatewayhttp/auth_handler.go:188-200`。

**爆炸半径**

接口返回 503，但待验证用户和 token 已存在；再次注册撞重复，用户又没有公开重发入口。邮件失败路径还没有记录 `user_register_failed` 或“用户已创建但通知失败”的分类日志。

**修复建议**

增加按 tenant/email 限流的重发端点，重新签发单次 token；注册响应区分“用户已创建，邮件待重试”和“注册失败”。永久邮件配置错误应在允许公开注册前由发布门阻断，并把通知状态写入日志。

### UC-08：四种建号入口输入校验互相漂移（S1）

- 公开密码注册只要求邮箱、密码非空，未调用邮箱语法和名称校验：`backend/internal/userauth/service.go:178-205`。
- 密码哈希函数只拒绝空白，没有最短和最长长度：`backend/internal/userauth/password.go:22-48`。
- 管理端只用“包含 @”判断邮箱，密码最短 8，但名称仅 Trim：`backend/internal/adminuserhttp/user_create.go:61-104`。
- 首装入口使用完整邮箱解析、密码 8-128、名称 64 字符：`backend/internal/setuphttp/setuphttp.go:88-120`。
- 现成的名称校验会拒绝空值、非法 UTF-8、控制字符和超过 100 字符，但仅资料更新使用：`backend/internal/userauth/service.go:123-137`。

**爆炸半径**

公开注册可建立极弱密码和不可投递邮箱；管理端/社交源可把超长或控制字符名称写入库，后续污染列表、日志和前端布局。入口不同还会出现“首装不允许、注册却允许”的运维困惑。

**修复建议**

建立一个共享的新用户字段校验器：裸邮箱解析与长度、密码最短/最长、名称 UTF-8/控制字符/长度统一；各入口只保留角色、验证状态和邀请码等业务差异。不能让数据库或邮件发送成为第一道格式校验。

## 五、横向链路矩阵

### 上游账号

| 入口 | 身份/租户 | 来源证明 | 冲突 | 原子性 | 失败恢复 | 结论 |
| --- | --- | --- | --- | --- | --- | --- |
| 正式账号导入 | 租户管理员 + capability | 缺最终统一校验 | 强 | 账号/凭据/日志强 | staging 清理与超时日志 | 主体可靠，补 AI-01/03 |
| Claude Cookie | 同上 | 服务端换码 | 强，多组织显式选择 | 复用正式导入 | staging | 已存在，不是缺项 |
| 浏览器/设备 OAuth | 仅部署管理员、且要求已有账号 | state/PKCE 强 | 不负责建号 | 只写凭据 | 临时会话 | 缺自动建号闭环 |
| 直接带凭据建号 | 两种管理员 | 只看 payload | 弱于正式导入 | 分步写 | 尽力软删 | 应收敛到正式导入 |

### 平台用户

| 入口 | 权限/公开门 | 创建事务 | 提交后动作 | 恢复 | 结论 |
| --- | --- | --- | --- | --- | --- |
| 密码注册 | 公开，策略/邀请码/Captcha | 用户、邀请、验证 token 同事务 | 邮件、奖励 | 邮件 DLQ 部分存在，奖励无 | 事务主体好，外围有断口 |
| 社交注册 | 公开注册策略 | 建用户与绑身份分离 | 奖励 | 无孤儿清理 | 必须原子化 |
| 租户管理员建用户 | 管理令牌 | 用户与日志分离 | 无 | 无 | 假失败真生效 |
| 首装管理员 | 完全公开直到首个管理员 | 锁内单赢家 | 无 | 无部署者身份验证 | 存在整站接管窗口 |

## 六、测试结果与覆盖空洞

已执行：

```text
go test -count=1 ./internal/userauth/... ./internal/adminuserhttp/... ./internal/setuphttp/... ./internal/oauthpendinghttp/... ./internal/gatewayhttp/accountintake/... ./internal/gatewayhttp/accountintakehttp/... ./internal/credentialacq/... ./internal/credentialstore/... ./internal/credentialworker/...
```

结果全部通过。`setuphttp` 和账号导入真 SQL 用例带 `integration_pg` 构建标签，本次普通命令未执行。

必须新增的判别性场景：

1. 对每个 OAuth-only 模式，用通用 JSON 和直接建号两路注入 token，必须在写库前拒绝。
2. 直接建号的凭据、健康、日志任一点失败，数据库不得留下可服务账号。
3. 部署者操作下级租户终端用户，所有用户管理端点必须 403；只允许下级租户管理员管理自己的用户。
4. 首装没有 bootstrap secret 时必须拒绝，两个不同主体竞速只能部署者成功。
5. 注册限流后端故障时必须在 Argon2 前拒绝。
6. 社交绑定失败、管理日志失败、奖励失败、邮件硬失败分别验证残留、重试和日志状态。
7. 四个建号入口使用同一组邮箱、密码和名称边界样本。

## 七、建议修复顺序

1. 先封住 UC-01、UC-02、AI-01：身份越级、首装接管和凭据来源绕过属于上线阻断项。
2. 再把 AI-02、UC-04、UC-05 收敛到现有事务范式，消灭半创建状态。
3. 完成 AI-04、AI-03、AI-05：统一厂商 profile、三身份 capability 和 OAuth 自动建号。
4. 接入 UC-03、UC-06、UC-07 的限流/outbox/DLQ/人工恢复。
5. 最后清理 AI-06 并统一 UC-08 输入合同。

## 八、开放问题

1. 部署者“平台自身租户”的唯一标识目前应取工作租户、部署租户还是单独 capability，需要在实施前由现有 tenancy 真码定出唯一来源，不能继续把 `platform_admin = 任意 tenant` 当答案。
2. Claude 浏览器 OAuth 切换官方回调后，是由后端接收回调还是保留“页面显码后提交”，需要真实账号 E2E 决定；但 token 端点、刷新端点和 profile 唯一化不依赖这个 UI 决策。

## 九、审计边界

本报告只说明问题和建议，没有修复业务代码、没有接触真实凭据、没有创建 PR。成熟项目只作为行为合同输入；报告没有复制其源码、结构或实现细节。成熟对照自身也存在两段式创建和部分提交后尽力执行，HUAKAI 不应照搬这些缺点。

本轮结论以实际读取的 41 个源码区域为依据，没有把未观察到的上游行为写成事实，也没有保留推测性结论；两项尚不能由现有源码唯一回答的事项已集中放入“开放问题”。

Source files read: `backend/internal/credentialacq/types.go`, `backend/internal/credentialacq/session_store.go`, `backend/internal/credentialacq/cli_import.go`, `backend/internal/credentialacq/intake/plan.go`, `backend/internal/credentialacq/anthropic_oauth.go`, `backend/internal/credentialacq/claudecookie/exchange.go`, `backend/internal/anthropicoauth/client_id.go`, `backend/internal/anthropicoauth/flow.go`, `backend/internal/anthropicoauth/exchanger.go`, `backend/internal/credentialworker/adapters/anthropic.go`, `backend/internal/gatewayhttp/accountintake/service.go`, `backend/internal/gatewayhttp/accountintake/execute.go`, `backend/internal/gatewayhttp/accountintake/staged_store.go`, `backend/internal/gatewayhttp/accountintakehttp/handler.go`, `backend/internal/gatewayhttp/credentialacqhttp/handler.go`, `backend/internal/gatewayhttp/admin_pool_accounts_handler.go`, `backend/internal/admin/operator_auth.go`, `backend/internal/adminuserhttp/tenant_scope.go`, `backend/internal/adminuserhttp/routes.go`, `backend/internal/adminuserhttp/user_create.go`, `backend/internal/userauth/service.go`, `backend/internal/userauth/social_login.go`, `backend/internal/userauth/password.go`, `backend/internal/userauth/store.go`, `backend/internal/gatewayhttp/auth_handler.go`, `backend/internal/captcha/verifier.go`, `backend/internal/email/sender_factory.go`, `backend/internal/payment/signup_invitee_reward.go`, `backend/internal/setuphttp/setuphttp.go`, `backend/internal/setuphttp/setuphttp_integration_test.go`, `backend/internal/provider/registrydefault/default.go`, `backend/cmd/gateway/routes.go`, `backend/cmd/gateway/wiring.go`, `Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:backend/internal/pkg/oauth/oauth.go`, `Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:backend/internal/service/oauth_service.go`, `Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:backend/internal/repository/claude_oauth_service.go`, `Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:backend/internal/handler/admin/account_handler.go`, `Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:frontend/src/components/account/CreateAccountModal.vue`, `Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:backend/internal/server/routes/auth.go`, `Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:backend/internal/service/auth_service.go`, `Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:backend/internal/service/auth_oauth_email_flow.go`

Lane: specifier

Agent: Codex GPT-5

UTC timestamp: 2026-07-21T12:03:28Z
