# 2026-07-16 Sub2 账号导入四项能力 clean-room 行为报告

```yaml
Artifact: 2026-07-16-sub2-account-import-four-gaps-specifier
Primary repository: Wei-Shaw/sub2api
Locked SHA: 7f5d067af21c836b359aef9a70863bd90bf9f5a5
HEAD verification: HEAD == origin/main == locked SHA
Default mirrors in scope: Wei-Shaw/sub2api + router-for-me/CLIProxyAPI + QuantumNous/new-api
Supplemental mirror read: no
Supplemental mirror reason: primary repository supplied sufficient positive evidence; dispatch allowed supplemental reads only when primary evidence was insufficient
Observed regions: 27
Inferences: 0
Open questions: 5
Lane: specifier
```

归档说明：隔离会话的完整输出曾记录 4 项推断；本压缩归档删除了这些未逐项展开的推断，只保留直接观察、明确的“未观察到”和 HUAKAI 方案建议，因此本文件的 `Inferences` 为 0。三镜均在有效 dispatch 范围内；CLIProxyAPI 与 New API 按 dispatch 约束未被读取，因为 Sub2 已提供四项问题所需的正向证据。

## 结论表

| 能力 | 判定 | 已观察行为 |
| --- | --- | --- |
| Claude Cookie/sessionKey 自动登录 | `Confirmed` | 管理员粘贴一个或多个 `sessionKey`，服务端完成组织识别、PKCE、授权码取得和令牌交换，不要求在该流程中打开浏览器。原 Cookie 只作为转换输入，账号保存转换后的 OAuth/Setup Token 凭据。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/oauth_service.go:175-282` |
| Claude Setup Token | `Confirmed` | 独立账号类型、独立授权范围、独立授权入口，同时支持 Cookie 自动转换；运行时仍依赖短期访问令牌与刷新材料，不是单个永久 token。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/server/routes/admin.go:365-371` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/token_refresher.go:40-71` |
| Codex 专用批量导入 | `Confirmed` | 支持裸 access token、多行、JSON 对象、JSON 数组、连续 JSON 和混合输入；逐项决定创建、更新、跳过或失败。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_codex_import.go:24-67` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_codex_import.go:237-488` |
| Agent Identity | `Confirmed as credential mode` | 不是普通账号备注，而是包含运行时标识、Ed25519 私钥和任务绑定的实际认证模式。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/openai_agent_identity.go:64-132` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/openai_agent_identity.go:175-313` |
| CRS 同步 | `Confirmed` | CRS 指 `claude-relay-service`；后端登录其管理端并拉取包含秘密的账号数据，支持预览、创建、更新和可选代理同步。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/crs_sync_service.go:106-260` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/crs_sync_service.go:263-380` |
| 账号整包导入导出 | `Partial` | 能迁移账号、凭据和可选代理，但包是有损的，且未观察到文件级加密、签名或批次回滚。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_data.go:27-73` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_data.go:245-484` |

## Claude Cookie 自动登录

用户界面提供批量 Cookie 自动授权入口；前端按行拆分输入，后端逐个转换。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:frontend/src/i18n/locales/zh/admin/accounts.ts:884-929` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:frontend/src/composables/useAccountOAuth.ts:111-153`

服务端使用 Cookie 查询组织、生成一次性 PKCE/state、取得授权码并交换正式令牌。转换结果包含访问令牌、刷新材料、到期和可用的账号身份；未观察到原始 Cookie 被写入账号凭据。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/oauth_service.go:175-238` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/oauth_service.go:251-282`

该流程的 HTTP 客户端不保留持久 Cookie 容器，只在指定请求上附加输入 Cookie。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/repository/claude_oauth_service.go:121-170` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/repository/claude_oauth_service.go:264-279`

失败可区分组织发现、授权码取得、令牌交换、网络/代理和上游响应错误，但批量用户体验主要仍是按序号展示错误文本，不是稳定机器错误码。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/oauth_service.go:202-230`

## Claude Setup Token

Setup Token 是独立账号形态，与普通 OAuth 并列，使用更窄的推理授权范围。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/server/routes/admin.go:365-371` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/oauth_service.go:64-73`

授权结果仍包含短期访问令牌、到期和可选刷新材料；后台刷新器同时覆盖普通 Claude OAuth 与 Setup Token。因此“长期号”表示可持续刷新，不表示访问令牌永久有效。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/oauth_service.go:130-141` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/token_refresher.go:40-71`

## Codex 专用批量与 Agent Identity

Codex 导入接受裸令牌、多行、单对象、数组、连续对象和混合文本。每项可以携带访问令牌、刷新材料、身份令牌、账号/用户标识、邮箱、套餐和组织信息。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_codex_import.go:24-67` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_codex_import.go:382-615`

导入按稳定账号身份、邮箱、运行时身份或令牌指纹识别重复；单批重复跳过，已有账号默认合并更新，新账号创建。只有新 access token 时会保留旧账号已有的 refresh material。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_codex_import.go:237-379`

Agent Identity 是独立认证材料：运行时标识、Ed25519 私钥、上游账号/用户标识和可选任务绑定。请求时生成签名声明；任务绑定缺失或失效时可以注册或恢复。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_codex_import.go:490-537` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/openai_agent_identity.go:64-132` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/openai_agent_identity.go:175-313`

Codex 导入请求体被整体排除在审计正文之外，私钥不向前端返回。未观察到账号仓储路径提供应用级凭据信封加密。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/server/middleware/audit_log.go:81-129` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/account_credentials_redact.go:3-13` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/repository/account_repo.go:545-617`

## CRS 同步

CRS 是 `claude-relay-service` 的专用兼容协议，不是通用行业标准。管理员输入服务地址和管理登录信息，由本系统后端登录并拉取包含秘密的账号导出。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:frontend/src/i18n/locales/zh/admin/accounts.ts:47-55` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/crs_sync_service.go:222-260`

同步覆盖多类 Claude、OpenAI、Gemini 账号和可选代理。执行前可预览本地已有与新增账号；已有账号自动更新，新账号由管理员选择创建。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/crs_sync_service.go:106-219` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:frontend/src/components/account/SyncFromCrsModal.vue:293-356`

地址检查包含主机白名单、解析后 IP 和私网策略；响应体有大小限制。结果逐项返回创建、更新、跳过或失败，未观察到全批事务回滚。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/crs_sync_service.go:224-250` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/crs_sync_service.go:263-380` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/service/crs_sync_service.go:1352-1367`

## 账号迁移包

导出可包含账号属性、原始凭据、附加配置、并发/优先级/费率/到期控制，以及可选代理和代理秘密。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_data.go:27-73` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_data.go:150-215`

包不会完整保存凭据影子账号、影子调度设置和分组绑定，也不包含系统计费、使用、审计、用户或 API Key。因此它是账号与代理迁移包，不是完整灾备。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_data.go:53-73` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_data.go:115-130` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_data.go:432-447`

敏感导出需要额外身份验证和敏感读取审计，但未观察到导出文件加密、签名、一次性下载或批次撤销。导入允许部分成功，没有全批回滚。`Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/server/routes/admin.go:349-352` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/server/middleware/audit_log.go:51-62` `Wei-Shaw/sub2api@7f5d067af21c836b359aef9a70863bd90bf9f5a5:backend/internal/handler/admin/account_data.go:245-484`

## HUAKAI 行为目标

1. 所有来源先进入统一 dry-run 计划，输出 `create/update/skip/conflict/fail`，管理员确认后才写库。
2. Cookie 只做一次性转换，不持久化；请求正文不进审计，默认要求 step-up，并提供稳定阶段错误码。
3. Setup Token 作为独立账号形态展示“当前访问令牌到期”和“刷新能力”，不宣称 token 永久有效。
4. Codex token 会话与 Agent Identity 分成两个清晰认证入口；后者明确标记包含私钥。
5. CRS 只作为连接器插件，不把专用协议写进账号核心域；地址必须受 SSRF allowlist 和解析时、连接时双重检查。
6. 迁移包分为默认无秘密的安全迁移包，以及显式启用、加密、签名、可审计的恢复包。
7. 每个导入批次有稳定 ID、逐项结果、失败项重试和新建对象撤销清单。
8. 成功导入的账号立即进入 HUAKAI 现有加密凭据、刷新、selector、健康、诊断和恢复闭环。

## Open Questions

1. 参考系统部署层是否另有磁盘或数据库透明加密，无法从账号仓储区域确认。
2. 通用审计脱敏是否覆盖 Cookie 与 CRS 登录输入，需要单独审计脱敏器。
3. Setup Token 是否在所有账号状态下都返回可刷新材料，需要真实协议验证。
4. Agent Identity 的上游撤销、租户绑定和生命周期需要真实协议验证。
5. 迁移包响应是否由外围代理统一添加禁止缓存头，本轮未确认。

Source files read:
- backend/internal/server/routes/admin.go
- backend/internal/handler/admin/account_handler.go
- backend/internal/handler/admin/account_codex_import.go
- backend/internal/handler/admin/account_data.go
- backend/internal/repository/claude_oauth_service.go
- backend/internal/repository/account_repo.go
- backend/internal/service/oauth_service.go
- backend/internal/service/token_refresher.go
- backend/internal/service/openai_agent_identity.go
- backend/internal/service/crs_sync_service.go
- backend/internal/service/account_credentials_persistence.go
- backend/internal/service/account_credentials_redact.go
- backend/internal/pkg/oauth/oauth.go
- backend/internal/handler/dto/mappers.go
- backend/internal/server/middleware/audit_log.go
- frontend/src/components/account/OAuthAuthorizationFlow.vue
- frontend/src/components/account/SyncFromCrsModal.vue
- frontend/src/components/admin/account/ImportDataModal.vue
- frontend/src/components/account/CreateAccountModal.vue
- frontend/src/composables/useAccountOAuth.ts
- frontend/src/api/admin/accounts.ts
- frontend/src/i18n/locales/zh/admin/accounts.ts
- deploy/config.example.yaml
Supplemental mirrors not read:
- router-for-me/CLIProxyAPI@09da52ad509e2c18e7b9540db3b98c2214c280aa
- QuantumNous/new-api@a63364d156cf2a64f1c3d1ee4923d73d5f3222a1
Lane: specifier
Agent: OpenAI GPT-5 Codex / root
UTC timestamp: 2026-07-16T11:31:39Z
