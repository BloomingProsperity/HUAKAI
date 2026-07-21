# 2026-07-21 账号导入、账号创建与用户创建闭环报告

| 项目 | 结论 |
| --- | --- |
| 分支 | `fix/reverse-account-model-pull-closure-codex`，未新建并行分支或工作树 |
| 实施范围 | 只处理账号导入、账号创建与用户创建完整链路 |
| 总结论 | 原报告中 12 个真实缺口已闭环，1 个限流误报维持撤销；完成度复核新增发现并闭环 OAuth 终态原子性、Grok 身份、共享账号消歧、导入后探测、套餐证据、xAI 设备码接线和 access token audience 7 类缺口 |
| 数据库 | 新增迁移 `0211`，真实 PostgreSQL 从全量迁移后完成账号、凭据、健康、订阅标签、日志和终态断言 |
| 外部验证 | 未消费或旋转真实厂商 refresh token；真实 Claude OAuth 端点兼容性和 xAI 完整设备授权仍需可消耗的专用测试账号裁定 |

## 一、最终业务合同

1. 部署者只能管理平台自有租户的账号和终端用户，不能越级处理下级租户的终端用户。
2. 下级租户管理员只有获得 `advanced_account_intake` 授权后，才能管理本租户上游账号；终端用户永远不能导入上游账号。
3. OAuth 创建型导入不再要求先建空账号。授权完成后，凭据和上游身份进入短期加密暂存；预检决定创建、更新、跳过或冲突；执行成功后才绑定真实账号。
4. 同一上游身份出现多条候选时不再随便更新第一条，必须显式冲突并人工消歧。
5. 成功创建账号时，账号、加密凭据、默认健康状态和管理日志保持原子一致；成功创建用户时，用户与必要身份/日志保持原子一致。
6. 注册奖励失败进入可重试 outbox/DLQ；验证邮件支持防枚举重发；所有创建入口使用同一字段校验合同。

## 二、原问题逐项状态

| ID | 最终状态 | 源码证据与结果 |
| --- | --- | --- |
| AI-01 OAuth 来源旁路 | 已闭环 | `credentialacq/intake/plan.go` 将导入来源回校到 `ModePlan`；直接建号拒绝 OAuth-only 手工载荷，专用来源测试覆盖绕过。 |
| AI-02 直接建号非原子 | 已闭环 | `gatewayhttp/admin_pool_accounts_handler.go` 通过凭据事务统一写账号、凭据、健康和日志；`admin_pool_accounts_create_atomic_integration_test.go` 用真 PostgreSQL 验证日志失败全回滚。 |
| AI-03 三身份合同矛盾 | 已闭环 | `accountintakehttp/handler.go`、`credentialacqhttp/authorization.go:38` 和账号整包服务统一为“平台自有租户或获授权的租户管理员”；跨租户均 fail-closed。 |
| AI-04 Claude OAuth 漂移 | 安全边界已守，活体待验 | 生产只保留服务端可信 profile，不允许凭据自报刷新目标；旧 `anthropicoauth` 流程与换码死码已删。当前浏览器端点是否仍被上游接受，必须用可旋转的真实订阅号验证，不能靠猜测改端点。 |
| AI-05 OAuth 要先建空账号 | 已闭环 | `accountintake/oauth_service.go:67-252` 提供 start、callback、poll、plan、execute；迁移 `0211` 仅允许显式账号导入目的暂时为空账号，并在完成时绑定真实账号。 |
| AI-06 Claude OAuth 平行死码 | 已闭环 | 删除旧 flow/exchanger/token 实现，只保留刷新链仍使用的公开 client ID 与受控 HTTP transport。 |
| UC-01 部署者越级管理用户 | 已闭环 | `adminuserhttp/tenant_scope.go` 要求部署者目标等于 `PlatformTenantID`；租户管理员只能使用自身 scope，跨租户返回 403。 |
| UC-02 首装抢占窗口 | 已闭环 | `setuphttp` 增加一次性 setup token 合同和真 PostgreSQL 并发测试；首装不再仅凭“第一个请求”取得管理员。 |
| UC-03 注册无独立限流 | 非问题 | 顶层 `auth_register` 专用桶仍在，未重复造限流；路由和测试保持原合同。 |
| UC-04 社交建号半事务 | 已闭环 | `userauth/social_login.go:221`、`:318` 使用现有事务能力把建用户和绑身份合并；失败残留测试覆盖两个入口。 |
| UC-05 管理端建用户无原子日志 | 已闭环 | `adminuserhttp/user_create.go:170` 在同一事务写用户和日志；真 PostgreSQL 测试证明日志失败时用户不存在。 |
| UC-06 奖励静默丢失 | 已闭环 | `userauth/service.go:537` 记录分类错误并投递恢复事件；`payment/signup_reward_recovery.go:23-78` 复用幂等发放和通用 outbox/DLQ。 |
| UC-07 邮件失败后无法重发 | 已闭环 | `gatewayhttp/session_handler.go:58` 增加防枚举重发；不存在、已验证和待验证邮箱统一 202，真实发信仍受冷却限制。 |
| UC-08 创建入口校验漂移 | 已闭环 | `userauth/newuser_fields.go` 成为邮箱、密码、名称统一校验入口，公开注册、社交补全、管理端和首装共同使用。 |

## 三、OAuth 导入即创建链路

### 1. 开始

`OAuthService.Start` 先验证租户、操作者、模式来源和 OAuth exchanger，再创建带 `account_intake` 目的的临时会话。此时 `provider_account_id` 必须为空，数据库也保存为 SQL `NULL`，不会产生占位账号。

### 2. 回调或轮询

回调先核对租户、操作者、角色和 state，再换码。错误 state 不会杀死合法流程，避免别人仅凭 flow ID 让真实授权失效。换码结果还要反向核对 vendor/auth mode，防止 exchanger 返回错误模式。

### 3. 加密暂存与预检

授权结果通过 `credentialacq/intake/oauth_candidate.go` 形成仅供服务端使用的信封，立即进入有时效的加密暂存。预检复用统一冲突算法和身份/订阅投影，前端将来只消费 create/update/skip/conflict 结果，不需要自己判断身份。

### 4. 执行与终态

执行链在同一个 PostgreSQL 事务内创建或更新账号、凭据、健康和账号操作日志，同时把 OAuth 会话绑定到实际 `provider_account_id` 与 `credential_id`，把暂存记录置为完成并写入完成日志。任一环节失败会整体回滚；重复执行被拒绝，不会重复建号或重复写凭据。

### 5. 上游身份与导入后采集

- Grok 使用固定公共客户端的 RFC 8628 设备码，不再接受请求体改写 client、端点、scope 或回调地址。启动返回用户码、验证地址和轮询间隔，随后复用数据库租约、加密暂存和失败恢复。
- xAI ID Token 用固定 issuer、client audience 与 JWKS 验证 ES256 签名和时效，确定个人主体；access token 另行验证签名、固定 client audience、同一主体与时效后提取团队范围。缺 refresh token、缺任一主体/团队、两枚 token 主体不一致、audience 不符或签名失效均拒绝导入。
- OpenAI 工作区与 Grok 团队都使用“账号范围 + 个人主体”的复合身份。同一人位于不同工作区/团队时分别建号；只有同一复合身份才轮换原凭据；旧记录缺任一维度时显式冲突。
- 创建或轮换事务提交后，账号进入单账号异步 quota probe 队列；上游探测失败不反向撤销已经成功的导入，周期扫描继续兜底。
- 设备码响应中的明确套餐字段会保留为发行方证据并进入统一系统标签。没有明确证据时保持 unknown，不把官方 Key 或无套餐字段的 OAuth 账号伪装成订阅账号。

## 四、失败与恢复

- 授权前失败：不创建账号、不保存候选凭据。
- state/操作者错误：返回拒绝但保留合法流程，避免拒绝服务。
- 换码后暂存瞬时失败：有限重试；全部失败时用脱离请求取消的短事务把流程退回 `started`，操作者可沿原授权链接重新取得 code，不会留下 `validated` 但无候选的死流程。
- 执行事务失败：账号、凭据、健康和日志全部回滚。
- 流程终态或完成日志失败：与账号事务一起回滚；失败记录再以脱离请求取消的短事务写入。若这层恢复也失败，两个错误合并返回，不再静默吞掉恢复失败。
- 暂存领取后进程中断：现有超时清理把卡住流程转为失败/需人工处理，不把未知状态当成功。
- 奖励发放失败：进入 outbox/DLQ，重放依赖既有业务幂等键，避免重复加余额。

## 五、迁移与 API

- `0211_oauth_account_intake_flow.up.sql` 只把临时 OAuth 会话的账号外键放宽为可空，并用 purpose/source/status 约束限制生命周期；普通凭据获取流程仍必须绑定已有账号。
- `0211_oauth_account_intake_flow.down.sql` 在存在 OAuth 创建型会话时拒绝盲目回滚，避免丢失流程语义。
- OpenAPI 已加入 OAuth start/callback/poll/plan/execute 五个入口，明确执行前 `provider_account_id` 可空，角色合同为平台管理员或获授权租户管理员。
- 路由写分类和生产依赖装配已更新，OpenAPI 与实现一致性测试通过。

## 六、结构与死代码

- OAuth 候选编解码下沉到 `credentialacq/intake`，设备授权共用 HTTP、响应上限和 token 字段归一化下沉到 `credentialacq/oauthwire`；主包回到代码预算内。
- 凭据获取授权/日志辅助从 600 行以上 handler 拆到 `credentialacqhttp/authorization.go`。
- 认证重发逻辑从超长 `auth_handler.go` 移到已有认证会话职责文件，未增加 `gatewayhttp` 顶层文件预算。
- 删除全仓零调用、零测试的 `credentialacq/cloud_bootstrap.go` 平行构造器；Bedrock、Azure、Vertex 的正式模式、载荷校验和 `cloud_bootstrap` 导入能力仍由 `ModePlan`、credential handler 与 credentialstore 保留，没有功能缩水。
- `go test ./internal/codebudget` 通过，未修改 `baseline.json`。

## 七、测试证据

普通全量测试：

```text
go test -count=1 ./...
结果：全部通过
```

xAI 公开端点活体握手：

```text
GET OIDC discovery + POST device authorization(form)
结果：发现文档声明 device-code grant；固定设备端点返回 device_code、user_code、verification_uri、expires_in、interval。
安全处理：只输出字段存在性和验证地址 host，码值未打印、未落盘、未完成用户授权、未签发或旋转真实 token。
```

xAI 既有测试令牌合同核实：

```text
仅离线解码 access token 的非身份合同字段，确认 issuer、audience、client_id、scope 和 claim 名称；未打印令牌、主体、团队或邮箱。
结果：真实 audience 与固定公共客户端 ID 一致，访问令牌校验已据此严格绑定，并有错误 audience 拒绝测试。
```

真实 PostgreSQL 全链路与邻接回归：

```text
./scripts/integration-pg.sh
结果：最终迁移 1-211 后逐包克隆纯净库，80/80 包带 -race 全绿，运行库自动清理。
过程说明：第一次最终全跑曾因同机瞬时数据库连接竞争，在第 50 包克隆前清库失败；单独复现前置包后连续检查无连接泄漏且立即清库成功，第二次完整全跑越过同点并 80/80 通过，未改脚本或降低门槛掩盖该失败。
```

其中 `accountintake/oauth_service_integration_test.go` 验证：授权前无账号且外键为 `NULL`、错误操作者与错误 state 被拒绝、回调只产生一条创建计划、执行后账号/凭据/健康/外部身份/订阅标签/日志全部存在、会话绑定真实账号和凭据、暂存密文清空、重复授权轮换同一账号、完成日志失败整体回滚、回滚事务不触发后台探测、重放失败。

质量门：

```text
./scripts/quality-gate.sh
结果：staticcheck 无新增、deadcode 无新增、quality-gate PASS
```

强制评审第一轮发现并修复两个 `S1`：OAuth 换码成功但候选暂存失败会卡死流程，以及 HTTP 请求取消会连带取消奖励恢复入队。`credentialacq/session_store_realpg_test.go` 现在用真 PostgreSQL 证明持久化失败退回 `started` 且重新授权可完成；`userauth/signup_reward_log_test.go` 证明取消父请求后恢复入队仍获得有效的有界 context。

本批提交前首轮复审又发现一个 `S1`：xAI OIDC 在缺少团队范围时会把个人主体退化成账号范围，同一用户跨团队可能误轮换凭据。继续核实 xAI 当前发现文档与官方企业合同后确认，ID Token 负责个人主体，团队主身份由 access token 携带；生产轮询器现分别验签两枚 token 并强制主体一致，团队范围只从已验签 access token 取得。缺团队或跨主体测试均返回 `ErrInvalidTokenShape`，不会进入预检或写库。

本次强制评审第一轮再发现一个 `S1`：access token 没有校验目标 audience，同一发行方为其他客户端签发的有效令牌可能被错当成团队范围证据。xAI 发现文档没有公开 audience 取值，成熟实现也没有补足该校验；读取 Owner 既有测试令牌时只解码非身份合同字段，确认真实 `aud` 等于固定公共客户端 ID。生产验证现强制匹配该 audience，并新增“签名、issuer、主体和团队均正确但 audience 错误”仍拒绝的测试；随后进入第二轮强制评审。

强制评审第二轮未发现 S0/S1，也未发现可明确归因于当前变更的其他离散缺陷；结论明确确认 xAI 设备码接线、双 token 验证、身份加密恢复和测试之间一致。评审只读沙箱无法创建 Go 临时构建目录，因此其附带测试命令未执行成功；正常工作环境的专项测试已独立通过，最终全量门继续由本批执行。

PR 首轮远端质量门又发现两个重构遗留死入口：无生产调用的旧 OAuth 回调包装和账号创建自开事务包装。生产路径分别已经统一为“注册表解析、候选先持久化”的回调入口，以及由上层业务事务调用 `InsertTx`。本次删除两个旧包装，单元测试直接验证 OAuth 核心状态机，并把 PostgreSQL 并发测试的开事务动作收回测试辅助函数；目标测试、真实 PostgreSQL 测试和死代码质量门重新通过，没有通过抬高基线掩盖问题。

## 八、尚未冒充完成的事项

唯一剩余的是会消费真实授权的外部系统活体验证，不是已知本地代码缺口：真实 Claude 浏览器 OAuth 与 Cookie 凭据分别跑“授权、换码、身份、刷新、一次模型请求”；xAI 还需由测试账号在验证页重新完成设备授权，才能验证当前完整响应中的 ID Token、换码、刷新和一次模型请求。既有 access token 已只用于离线核实 audience 合同，不能代替完整 E2E。还要用 Claude 订阅 OAuth 反转号验证 `opus-4-8`、`fable-5` 是否与 Sonnet 一样返回 429；现有官方 `sk-ant-api03` key 不覆盖该路径。刷新可能旋转或作废现有 refresh token，因此必须使用允许消耗的专用测试账号；在此之前不把握手、mock 或真 PostgreSQL 测试说成厂商 E2E。Kimi 当前公开设备码行为只证明 token、scope 与有效期，不把缺失的套餐查询冒充已实现。

## 九、风险与功能完整性

- 功能没有缩水：正式导入、Cookie、Setup Token、Codex 批量/Agent Identity、CRS、账号整包、直接建号和 OAuth 导入均保留，并收敛到统一权限与事务合同。
- clean-room 风险：未复制成熟项目源码、标识符、注释或文件结构；外部项目只提供行为结果。
- 安全风险：权限默认 fail-closed；OAuth state、PKCE、操作者绑定、短期加密暂存和重放拒绝均有测试；只读取既有 xAI 测试令牌的非身份合同字段确认 audience，未打印、复制或提交真实凭据及身份值。
- 数据风险：迁移是可逆约束变更，下迁移会在有活跃新流程时拒绝破坏性回滚。

## 十、成熟行为核实

- 已观察：Grok 成熟实现会从换码材料投影个人主体与团队范围，并持久化套餐字段；导入完成后进入有并发上限和执行超时的主动探测队列。证据：`Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:backend/internal/service/grok_oauth_service.go:265`、`:329`，`backend/internal/handler/admin/grok_import_probe.go:54`、`:100`。
- 已观察：该实现同时解码 ID Token 和 access token claims 用于账号元数据，但没有在该函数内验证签名。HUAKAI 没有照搬，改为分别验签：ID Token 校验固定发行方、client audience 与时效，access token 校验固定发行方、client audience、时效并必须与 ID Token 为同一主体，团队范围只接受已验签结果。证据同上 `grok_oauth_service.go:329`。
- 已观察：xAI 当前 OIDC 发现文档声明支持设备码 grant，设备授权端点为固定 xAI HTTPS 地址；官方企业文档说明无浏览器环境可使用设备码，并说明团队主身份由 access token 携带。HUAKAI 因此采用设备码，分别验签 ID Token 与 access token，而不是继续依赖可变浏览器回调。[xAI Enterprise Deployments](https://docs.x.ai/build/enterprise)。
- 已观察：当前 Kimi 设备码换码响应只读取 access token、refresh token、token type、有效期和 scope，没有读取套餐或稳定个人身份。HUAKAI 因此只保留明确出现的套餐字段，不编造查询接口。证据：`router-for-me/CLIProxyAPI@db82d65d1cc3be6dc9662ee2b9a3810ac948d377:internal/auth/kimi/kimi.go:267`。
- 已观察：xAI 官方把 API 使用、计费和发票归属到 team，API key 也绑定 team；同一用户可以加入或创建多个 team。因此个人主体不能替代账号范围。证据：[xAI Team Management FAQ](https://docs.x.ai/developers/faq/team-management)、[xAI Management API Authorization](https://docs.x.ai/developers/rest-api-reference/management/auth)。
- 推断：导入后异步探测与周期扫描组合，既能让运营 UI 更快看到额度/套餐，又不会把上游网络不稳定扩大到账号落库事务；队列满或租约竞争时仍可能等待周期扫描，这是明确降级而非假成功。

Source files read: `backend/internal/credentialacq/`, `backend/internal/gatewayhttp/accountintake/`, `backend/internal/gatewayhttp/accountintakehttp/`, `backend/internal/gatewayhttp/credentialacqhttp/`, `backend/internal/gatewayhttp/admin_pool_accounts_handler.go`, `backend/internal/adminuserhttp/`, `backend/internal/userauth/`, `backend/internal/setuphttp/`, `backend/internal/payment/`, `backend/internal/quotaprobe/`, `backend/cmd/gateway/`, `backend/sql/migrations/0211_oauth_account_intake_flow.*.sql`, `docs/openapi/openapi.yaml`, `Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:backend/internal/service/grok_oauth_service.go`, `Wei-Shaw/sub2api@5a8d6c4e41e38f05cea4164e6ff03443fc0f6923:backend/internal/handler/admin/grok_import_probe.go`, `router-for-me/CLIProxyAPI@db82d65d1cc3be6dc9662ee2b9a3810ac948d377:internal/auth/kimi/kimi.go`

Lane: implementation verification

Agent: Codex GPT-5

UTC timestamp: 2026-07-21T19:03:39Z
