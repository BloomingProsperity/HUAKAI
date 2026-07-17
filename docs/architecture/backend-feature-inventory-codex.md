# HUAKAI 后端功能全景 × 前端覆盖盘点(codex)

> 盘点日期：2026-07-13（UTC）  
> 口径：只认当前工作树真码，不使用记忆、规划文档或借鉴项目结论。  
> 结论先行：前端并非完全没有“上游账号”入口，而是入口被抽象成通用目录和通用表单；全局 `platform_admin` 的新建链路又漏传必需的 `tenant_id`，同时 UI 不消费后端返回的 `serving_readiness`。因此 Owner 看到的是“页面似乎有，但厂商不可发现、不可判断是否能发流量，且平台管理员可能直接创建失败”。

## 盘点基线与判定口径

- 题目所称“209 个包”对应当前 `backend/internal/` 的 **209 个一级目录**；按 `go list ./internal/...` 递归计算，当前实际是 **268 个 Go 包路径**。其中 267 个包含生产 Go 文件，`internal/codebudget` 只有测试和基线文件。本报告按更严格的 268 个递归包盘点，末尾逐一列出。
- `backend/cmd/gateway/routes*.go` 当前共 16 个文件：**15 个生产路由文件 + 1 个测试文件**。15 个生产文件均纳入端点盘点。
- `docs/openapi/openapi.yaml` 当前有 **412 个 method + path operation**；实际路由源码还包含 OpenAPI 漏列的 `PUT /v1/admin/models/{id}/capability-bindings`、`PUT /v1/admin/routes/{id}`，以及收据双段 ID、Provider Account 三前缀等运行时别名。`go test ./cmd/gateway -run TestOpenAPI_ImplementationConsistency -count=1 -v` 已通过，证明 334 个 OpenAPI path 与运行时 path 集合一致；该测试只比较 path，不足以替代本报告对 method 的源码核验。
- 前端严格匹配 `frontend/src/features/*/api.ts` 的文件是 **61 个**；另有 9 个 `*Api.ts` 命名变体。为避免把真实调用误判成 `none`，本报告还检查了认证、setup、Hermes stream 等 feature 目录外请求模块。
- `frontend/src/app/router.tsx` 中有 59 个 `BUILT_PAGES`、11 个公开路由、2 个详情路由；导航项没有落入 `Placeholder`。本报告不以“路由存在”代替“工作流可用”。

Coverage 只使用以下四值：

- `full`：端点由页面完整消费，读、写、错误/确认等主工作流可实际完成。
- `partial`：有页面，但只消费部分端点、字段或流程，或页面存在却有实际阻断。
- `none`：后端能力已存在，但前端没有可用消费路径，用户或运维无法通过 UI 使用。
- `backend_only`：纯协议入口、机器回调、worker、中间件或内部基建，本来就不需要 UI。

## 一、领域能力树

### 1. 认证账号安全

| 能力 | 端点 | 前端页面 | coverage | 缺口 |
|---|---|---|---|---|
| 注册、邀请校验、登录与找回 | `POST /v1/auth/register`<br>`POST /v1/auth/validate-invitation-code`<br>`POST /v1/auth/login`<br>`POST /v1/auth/login/2fa`<br>`POST /v1/auth/verify-email`<br>`POST /v1/auth/confirm-device`<br>`POST /v1/auth/reset-password` | `/login`、`/forgot-password`、`/reset-password`、`/email-verify`、`/device-confirm` | `full` | 注册、2FA 登录、邀请预校验、找回/重置、邮件和新设备确认均有真实表单与请求。后端证据：`routes.go:285-307`；前端证据：`auth/api.ts` 及对应页面。 |
| OAuth、Telegram、Passkey 登录 | `POST /v1/auth/oauth-init`<br>`POST /v1/auth/oauth-callback`<br>`POST /v1/auth/oauth-pending/send-code`<br>`POST /v1/auth/oauth-pending/complete`<br>`POST /v1/auth/telegram-login`<br>`POST /v1/auth/passkey/login/begin`<br>`POST /v1/auth/passkey/login/finish` | `/login`、`/oauth/callback` | `full` | OAuth 待补邮箱、Telegram widget、WebAuthn 登录均有闭环。 |
| 社交身份变化回调 | `POST /v1/auth/social/identity-changed` | 无 | `backend_only` | 上游身份变化通知入口，不是人工 UI 操作。 |
| 当前账号与安全资料 | `GET /v1/auth/me`<br>`PUT /v1/auth/me/profile`<br>`POST /v1/auth/me/password`<br>`DELETE /v1/auth/me`<br>`POST /v1/auth/logout`<br>`DELETE /v1/auth/account-bindings/{provider}` | `/profile` | `full` | 资料、改密、注销账号和退出均有页面；解绑主流程使用下方规范化 binding 路径，旧解绑路径未单独调用但功能未缩水。 |
| 2FA | `POST /v1/auth/2fa/setup`<br>`POST /v1/auth/2fa/enable`<br>`GET /v1/auth/2fa/status`<br>`POST /v1/auth/2fa/disable`<br>`POST /v1/auth/2fa/backup-codes/regenerate` | `/profile` | `full` | 完整启用、停用、状态和恢复码流程。 |
| 用户 Passkey 管理 | `GET /v1/me/passkeys`<br>`POST /v1/me/passkeys/register/begin`<br>`POST /v1/me/passkeys/register/finish`<br>`DELETE /v1/me/passkeys/{id}` | `/profile` | `full` | 注册、列表、删除均消费。 |
| 会话刷新与设备会话 | `POST /v1/sessions/refresh`<br>`POST /v1/sessions/revoke`<br>`POST /v1/sessions/list` | `/profile`；刷新为全局后台逻辑 | `full` | `sessionsApi.ts` 负责列表/撤销，`refreshClient.ts` 负责自动刷新。 |
| OAuth 绑定管理 | `GET /v1/users/me/oauth-bindings`<br>`DELETE /v1/users/me/oauth-bindings/{provider}`<br>`POST /v1/users/me/oauth-bindings/telegram` | `/profile` | `full` | 列表、解绑、Telegram 绑定均消费。 |
| 平台运维 token | `GET /admin/v1/admin-tokens`<br>`POST /admin/v1/admin-tokens`<br>`POST /admin/v1/admin-tokens/{id}/revoke` | `/admin/platform-credentials` | `full` | 列表、一次性创建和撤销均有强确认与秘密只显一次语义。 |

### 2. 用户与 Key

| 能力 | 端点 | 前端页面 | coverage | 缺口 |
|---|---|---|---|---|
| 用户 API Key 生命周期 | `GET /v1/api-keys`<br>`POST /v1/api-keys`<br>`GET /v1/api-keys/{id}`<br>`PATCH /v1/api-keys/{id}`<br>`DELETE /v1/api-keys/{id}`<br>`POST /v1/api-keys/batch-revoke` | `/keys`、`/overview` | `partial` | 列表、创建、编辑、单个/批量撤销完整；单 Key 的独立 GET 没有前端调用，详情只能依赖列表数据。 |
| Key 配额、组、IP 与模型控制 | `GET /v1/api-keys/{id}/quota`<br>`PUT /v1/api-keys/{id}/quota`<br>`GET /v1/api-keys/{id}/group`<br>`PUT /v1/api-keys/{id}/group`<br>`GET /v1/api-keys/{id}/ip-allowlist`<br>`PUT /v1/api-keys/{id}/ip-allowlist`<br>`GET /v1/api-keys/{id}/ip-blacklist`<br>`PUT /v1/api-keys/{id}/ip-blacklist`<br>`GET /v1/api-keys/{id}/model-allowlist`<br>`PUT /v1/api-keys/{id}/model-allowlist` | `/keys` | `full` | `keys/controlsApi.ts` 与 `KeyControlsSection` 完整消费 10 个端点。 |
| 管理员代发/撤销 Key | `GET /admin/v1/api-keys`<br>`POST /admin/v1/api-keys`<br>`POST /admin/v1/api-keys/{id}/revoke` | `/admin/platform-credentials` | `full` | 列表、创建、撤销完整。 |
| 用户管理 | `GET /admin/v1/users`<br>`POST /admin/v1/users`<br>`GET /admin/v1/users/{id}`<br>`DELETE /admin/v1/users/{id}`<br>`PUT /admin/v1/users/{id}/status`<br>`POST /admin/v1/users/{id}/unlock`<br>`POST /admin/v1/users/{id}/2fa/force-disable`<br>`DELETE /admin/v1/users/{id}/passkeys`<br>`PUT /admin/v1/users/{id}/group`<br>`PUT /admin/v1/users/{id}/remark`<br>`DELETE /admin/v1/users/{id}/account-bindings/{provider}`<br>`GET /admin/v1/users/{id}/balance-history`<br>`GET /admin/v1/users/{id}/usage`<br>`GET /admin/v1/users/2fa-adoption-stats` | `/users`、`/users/:id` | `full` | 搜索、分页、创建、状态、解锁、详情、余额/用量、2FA/Passkey、组/备注、解绑和软删均有实际组件。 |
| 邀请、推荐与奖励 | `POST /v1/invitations`<br>`GET /v1/me/invitations`<br>`GET /v1/me/invitation-code`<br>`GET /v1/me/referrals`<br>`GET /v1/me/referrals/rewards`<br>`GET /v1/admin/referrals`<br>`GET /v1/admin/referrals/rewards`<br>`GET /v1/admin/referrals/overview` | `/affiliate`、`/admin/affiliates` | `full` | 用户邀请/奖励与管理总览均消费。 |
| 用户可见组 | `GET /v1/me/groups` | `/my-groups` | `full` | 用户能看到可达模型分组与倍率。 |

### 3. 配额用量

| 能力 | 端点 | 前端页面 | coverage | 缺口 |
|---|---|---|---|---|
| 用户配额与 API Key 用量 | `GET /v1/me/quota`<br>`GET /v1/me/usage`<br>`GET /v1/generation`<br>`GET /v1/me/analytics/time-series`<br>`GET /v1/me/keys/{id}/usage-summary` | `/overview`、`/usage`、`/key-usage` | `full` | 配额、汇总、单请求、时序和 Key 摘要均有页面；其中部分端点要求 API Key，页面明确要求用户提供 Key。 |
| 用户逐请求明细与导出 | `GET /v1/me/usage-records`<br>`GET /v1/me/usage/export.csv` | `/usage-records` | `full` | 游标分页、时间筛选、CSV 下载完整。 |
| 管理用量与性能 | `GET /admin/v1/usage`<br>`GET /v1/admin/usage/overview`<br>`GET /v1/admin/usage/leaderboard`<br>`GET /v1/admin/usage/performance`<br>`GET /v1/admin/usage/perf-metrics/summary`<br>`GET /v1/admin/usage/perf-metrics/by-bucket`<br>`GET /v1/admin/usage/health-score`<br>`GET /v1/admin/usage/provider-account-counts`<br>`GET /v1/admin/usage/export.csv` | `/ops`、根运维 Dashboard、`/admin/billing-claims`、`/admin/orders` | `full` | 总览、排行、性能桶、健康分、账号计数、原始用量和导出都有实际消费。 |
| 配额策略 CRUD | `GET /admin/v1/quota-policies`<br>`POST /admin/v1/quota-policies`<br>`GET /admin/v1/quota-policies/{id}`<br>`PUT /admin/v1/quota-policies/{id}`<br>`DELETE /admin/v1/quota-policies/{id}` | `/admin/quota-policies` | `partial` | 列表、新建、编辑、删除完整；独立 `GET /{id}` 虽有 API 封装，页面未调用，编辑依赖列表行数据。 |
| 热路径配额与预算执行 | 无公开 UI 端点：reserve、settle、reverse、窗口计数、并发槽、预算 breaker | 无 | `backend_only` | `quota`、`quotaenforce`、`budget`、`budgetenforce`、`rate/precheck` 等为请求热路径，不应做人工作台。 |

### 4. 计费钱包订单订阅

| 能力 | 端点 | 前端页面 | coverage | 缺口 |
|---|---|---|---|---|
| 用户钱包与充值开单 | `GET /v1/users/me/payments/balance`<br>`GET /v1/users/me/payments/config`<br>`GET /v1/users/me/payments/orders`<br>`POST /v1/users/me/payments/orders`<br>`GET /v1/users/me/payments/orders/{id}`<br>`POST /v1/users/me/payments/orders/{id}/cancel`<br>`POST /v1/users/me/payments/orders/{id}/refund-request`<br>`POST /v1/users/me/recharges` | `/wallet`、`/orders` | `full` | 新支付中心完整消费；旧 `recharges` 兼容入口未直接调用，但同一充值能力由规范订单入口完整提供。 |
| 支付 webhook | `POST /v1/payment/webhooks/{provider}`<br>`POST /v1/payments/webhooks/{provider}` | 无 | `backend_only` | 支付商机器回调；旧、规范两路径并存。 |
| 用户订单收据 | `GET /v1/me/orders/{id}/receipt` | `/orders` | `full` | 用户订单详情可下载/查看收据。 |
| 支付订单管理与退款 | `GET /v1/admin/payments`<br>`POST /v1/admin/payments`<br>`GET /v1/admin/payments/dashboard`<br>`GET /v1/admin/payments/{id}`<br>`GET /v1/admin/payments/{id}/audit`<br>`POST /v1/admin/payments/{id}/confirm`<br>`POST /v1/admin/payments/{id}/retry`<br>`POST /v1/admin/payments/{id}/cancel`<br>`POST /v1/admin/payments/{id}/refund`<br>`GET /v1/admin/payments/refund-requests`<br>`POST /v1/admin/payments/refund-requests/{id}/approve`<br>`POST /v1/admin/payments/refund-requests/{id}/reject`<br>`GET /v1/admin/payments/providers/{provider}/config`<br>`PUT /v1/admin/payments/providers/{provider}/config` | `/admin/orders` | `full` | 列表/详情、代客建单、确认/取消/重试/退款、工单审批、支付商配置均有页面；详情响应已带审计事件，所以未单独调用 audit helper 不构成功能缺口。 |
| 财务导出 | `GET /v1/admin/payments/export.csv`<br>`GET /v1/admin/orders/export.csv`<br>`GET /v1/admin/refunds/export.csv` | `/admin/orders` | `full` | 三种 CSV 均有下载入口。 |
| 余额调整与计费追补 | `POST /admin/v1/balances/adjustments`<br>`GET /admin/v1/billing/settings`<br>`PUT /admin/v1/billing/settings`<br>`GET /admin/v1/billing/claims`<br>`POST /admin/v1/billing/reprice` | `/users/:id`、`/admin/pricing`、`/admin/billing-claims` | `full` | 调额、结算策略、claim 查看和重算都有明确高风险确认。 |
| 兑换券与签到 | `POST /v1/users/me/vouchers/redeem`<br>`GET /v1/me/voucher-redemptions`<br>`GET /v1/admin/vouchers`<br>`POST /v1/admin/vouchers`<br>`POST /v1/admin/vouchers/batch`<br>`GET /v1/admin/vouchers/batches/{batch_id}`<br>`POST /v1/admin/vouchers/{id}/revoke`<br>`GET /v1/me/checkin`<br>`POST /v1/me/checkin` | `/redeem`、`/admin/vouchers`、`/checkin` | `full` | 用户兑换/历史、管理员生成/批次/撤销、签到状态/领取均完整。 |
| 用户订阅 | `GET /v1/users/me/subscriptions`<br>`GET /v1/users/me/subscriptions/plans`<br>`GET /v1/users/me/subscriptions/me`<br>`GET /v1/users/me/subscriptions/me/progress`<br>`POST /v1/users/me/subscriptions/purchase`<br>`POST /v1/users/me/subscriptions/change-plan`<br>`POST /v1/users/me/subscriptions/cancel-renew` | `/subscriptions` | `full` | 计划、当前订阅、进度、购买、改档、取消续订均消费。 |
| 订阅管理 | `GET /v1/admin/subscriptions/plans`<br>`POST /v1/admin/subscriptions/plans`<br>`GET /v1/admin/subscriptions/plans/{id}`<br>`PUT /v1/admin/subscriptions/plans/{id}`<br>`POST /v1/admin/subscriptions/plans/{id}`<br>`POST /v1/admin/subscriptions/plans/{id}/disable`<br>`GET /v1/admin/subscriptions/assignments`<br>`POST /v1/admin/subscriptions/assignments`<br>`POST /v1/admin/subscriptions/assignments/bulk`<br>`GET /v1/admin/subscriptions/assignments/{id}`<br>`POST /v1/admin/subscriptions/assignments/{id}/cancel`<br>`POST /v1/admin/subscriptions/assignments/{id}/extend`<br>`POST /v1/admin/subscriptions/assignments/{id}/reset-quota`<br>`POST /v1/admin/subscriptions/assignments/{id}/change-plan`<br>`POST /v1/admin/subscriptions/assignments/{id}/revoke`<br>`POST /v1/admin/subscriptions/vouchers` | `/admin/subscriptions` | `full` | 计划、分配、批量分配、延长/取消/重置/改档/撤销和订阅券均有工作流。 |
| 订阅/Key 到期与自动续费 worker | `GET /v1/admin/notifications/worker-stats`（可观测面）；其余无人工端点 | `/admin/broadcast`（worker 状态） | `backend_only` | 到期、提醒、自动续费和 Key 过期执行是 worker；UI 只需看状态，现已覆盖。 |

### 5. 定价

| 能力 | 端点 | 前端页面 | coverage | 缺口 |
|---|---|---|---|---|
| 公开价目与费率快照 | `GET /v1/pricing/page`<br>`GET /v1/pricing/rate-table`<br>`GET /v1/pricing/snapshots`<br>`GET /v1/pricing/snapshots/{snapshot_id}` | `/models`、`/available-channels`、`/welcome` | `partial` | 当前价目、快照列表和快照详情均有页面；`GET /v1/pricing/rate-table?version=...` 有前端封装但页面未调用。 |
| 池组倍率 | `GET /admin/v1/pricing/ratios`<br>`GET /admin/v1/pricing/ratios/audit/verify`<br>`GET /admin/v1/pricing/ratios/{pool_group_id}`<br>`PUT /admin/v1/pricing/ratios/{pool_group_id}`<br>`DELETE /admin/v1/pricing/ratios/{pool_group_id}` | `/admin/pricing` | `partial` | 列表、设置、删除和哈希链校验均消费；独立 `GET /{pool_group_id}` 无前端调用，编辑依赖列表行数据。 |
| 缓存价格覆盖 | `GET /v1/admin/cache-price-overrides`<br>`PUT /v1/admin/cache-price-overrides/{scope}`<br>`DELETE /v1/admin/cache-price-overrides/{scope}` | `/admin/pricing` | `full` | 全局、模型和租户 scope 的列、设、清均有 UI。 |
| 图片、音频、工具、缓存命中计价 | 无独立端点；请求热路径内部计算 | 无 | `backend_only` | `imagepricing`、`audiopricing`、`toolpricing`、`pricingeval` 为计价核心，不能让 UI 旁路执行。 |

### 6. 上游账号池凭证

> Provider Account 完整子树真实挂在三个前缀：`/admin/v1/provider-accounts`、`/v1/admin/provider-accounts`、`/v1/admin/pool-accounts`。下表把规范前缀写成完整 method + path；另外两个前缀共用同一组 handler，可将规范前缀原样替换得到对应别名。OpenAPI 还单列了 `POST /v1/admin/provider-accounts/bulk-by-tag` 与 `GET /v1/admin/provider-accounts/{id}/upstream-models`。

| 能力 | 端点 | 前端页面 | coverage | 缺口 |
|---|---|---|---|---|
| Account mode 目录 | `GET /admin/v1/account-modes` | `/accounts` 新建弹窗 | `partial` | 后端返回 `serving_readiness`（`adminhttp/catalog.go:32-59,331-358`），前端类型和页面完全丢弃该字段，只按 `is_enabled` 展示；实验/默认关闭模式会被误看成可发流量。 |
| Provider 目录 CRUD | `GET /admin/v1/providers`<br>`POST /admin/v1/providers`<br>`PUT /admin/v1/providers/{code}`<br>`DELETE /admin/v1/providers/{code}` | `/admin/catalogs`、`/accounts` | `partial` | 必须先手工建 provider，才会出现在“新建账号”；全局 `platform_admin` 的新建弹窗查询漏传 `tenant_id`。前端协议下拉还多出后端不接受的 `gemini`、`bedrock`、`antigravity` 三个非 canonical 值（前端 42 项，后端注册/合同 39 项）。 |
| Channel 目录 CRUD | `GET /admin/v1/channels`<br>`POST /admin/v1/channels`<br>`PUT /admin/v1/channels/{id}`<br>`DELETE /admin/v1/channels/{id}` | `/admin/catalogs`、`/accounts` | `partial` | CRUD 页面存在；新建账号弹窗的目录请求同样漏传全局平台管理员必需的 `tenant_id`。 |
| Channel 测试模板 | `GET /admin/v1/channel-test-templates`<br>`POST /admin/v1/channel-test-templates`<br>`GET /admin/v1/channel-test-templates/{id}`<br>`PUT /admin/v1/channel-test-templates/{id}`<br>`DELETE /admin/v1/channel-test-templates/{id}` | `/admin/channel-test-templates` | `partial` | 列表、新建、编辑、删除完整；独立 GET 详情有封装但页面未调用。 |
| Pool/组 | `GET /admin/v1/pools`<br>`POST /admin/v1/pools`<br>`GET /admin/v1/pools/{id}`<br>`PATCH /admin/v1/pools/{id}` | `/admin/groups` | `partial` | 列表、新建、编辑、启停和成员展开可用；独立 GET 详情封装未调用。 |
| Provider Account CRUD 与调度控制 | `GET /admin/v1/provider-accounts`<br>`POST /admin/v1/provider-accounts`<br>`GET /admin/v1/provider-accounts/{id}`<br>`PATCH /admin/v1/provider-accounts/{id}`<br>`DELETE /admin/v1/provider-accounts/{id}`<br>`PATCH /admin/v1/provider-accounts/{id}/enabled`<br>`POST /admin/v1/provider-accounts/{id}/clear-rate-limit`<br>`POST /admin/v1/provider-accounts/{id}/test`<br>`GET /admin/v1/provider-accounts/{id}/health`<br>`GET /admin/v1/provider-accounts/health-summary`<br>`GET /admin/v1/provider-accounts/{id}/recent-requests`<br>`POST /admin/v1/provider-accounts/bulk-by-tag`<br>`GET /admin/v1/provider-accounts/{id}/upstream-models` | `/accounts`、`/accounts/:id` | `partial` | 列表/筛选、详情、编辑、删除、启停、试跑、清限流、健康、最近请求、批量标签、上游模型都已接；但 `AccountsPage` 打开 `CreateAccountModal` 时不传 `tenantId`，创建 POST 也无 query/body tenant，导致无隐式 scope 的 `platform_admin` 可能直接 400（后端要求见 `admin_pool_accounts_handler.go:685-723`）。 |
| 账号凭证 CRUD | `GET /admin/v1/provider-accounts/{id}/credentials`<br>`POST /admin/v1/provider-accounts/{id}/credentials`<br>`POST /admin/v1/provider-accounts/{id}/credentials/{credential_id}/rotate`<br>`PATCH /admin/v1/provider-accounts/{id}/credentials/{credential_id}/state`<br>`DELETE /admin/v1/provider-accounts/{id}/credentials/{credential_id}`<br>`POST /admin/v1/provider-accounts/{id}/credentials/{credential_id}/resolve-project` | `/accounts/:id` | `partial` | 功能都能触发，但手工表单把 25 个 vendor 与 19 个 auth mode 做无约束笛卡尔积，并要求操作者写原始 JSON；大量前端可选组合会被后端拒绝。 |
| 凭证获取 flow | `POST /admin/v1/provider-accounts/{id}/credential-acquisitions`<br>`GET /admin/v1/provider-accounts/{id}/credential-acquisitions/{flow_id}`<br>`POST /admin/v1/provider-accounts/{id}/credential-acquisitions/{flow_id}/callback`<br>`POST /admin/v1/provider-accounts/{id}/credential-acquisitions/{flow_id}/cancel`<br>`POST /admin/v1/provider-accounts/{id}/credential-acquisitions/{flow_id}/finalize` | `/accounts/:id` | `partial` | UI 只有 `oauth`、`paste`、`cli_import`、`csv_import`、`json_import`；后端已有的 `cloud_bootstrap`、`token_exchange`、`setup_token`、`manual_first` 没有专属入口。独立 finalize 端点也被前端明确留空，只由 callback/导入自动落库。Device Code 的 `user_code`/验证地址未由 HTTP 响应和 UI 展示。 |
| 凭证全局辅助 | `GET /admin/v1/credentials/renew-status`<br>`POST /admin/v1/credentials/paste`<br>`POST /admin/v1/credentials/cli-import`<br>`POST /admin/v1/credentials/csv-import`<br>`POST /admin/v1/credentials/json-import`<br>`POST /admin/v1/credentials/oauth-init`<br>`GET /admin/v1/credentials/oauth-callback` | `/admin/credential-renew`、`/accounts/:id` | `partial` | renew 状态和四种导入 helper 已用；全局 `oauth-init`/`oauth-callback` 没有前端调用，页面走账号级 acquisition flow。 |
| Channel 健康 | `GET /v1/admin/channel-health`<br>`GET /v1/admin/channel-health/summary`<br>`GET /v1/admin/channel-health/{channel_id}`<br>`POST /admin/v1/provider-accounts/{id}/channel-health/pause`<br>`POST /admin/v1/provider-accounts/{id}/channel-health/resume`<br>`POST /admin/v1/provider-accounts/{id}/channel-health/force-active` | `/admin/channel-health` | `partial` | 列表、汇总、暂停/恢复/强制 active 已用；前端写动作使用等价的 `/v1/admin/provider-accounts` 别名前缀。单渠道详情 API 有封装但页面未调用，运维看不到详情和审计事件。 |
| 凭证加密、刷新、轮换与健康状态机 | 无人工端点或由上述状态端点只读投影 | 无 | `backend_only` | `credentialstore`、`credentialworker`、`channelhealth`、`channelprobe` 是后台执行链。 |

### 7. 网关转发协议

| 能力 | 端点 | 前端页面 | coverage | 缺口 |
|---|---|---|---|---|
| OpenAI Chat/Completions | `POST /v1/chat/completions`<br>`POST /v1/completions` | `/playground`、`/integration` | `full` | Playground 支持普通和 Chat 流式；集成页提供 API 使用说明。 |
| Embeddings 与 Rerank | `POST /v1/embeddings`<br>`POST /engines/{model}/embeddings`<br>`POST /v1/rerank` | `/playground` | `partial` | 标准 embeddings、rerank 已接；`/engines/{model}/embeddings` 兼容别名无 UI 调用。 |
| 图片 | `POST /v1/images/generations`<br>`POST /v1/images/edits`<br>`POST /v1/images/variations` | `/playground` | `partial` | 只接 generations；编辑、变体无 UI。 |
| 音频 | `POST /v1/audio/speech`<br>`POST /v1/audio/transcriptions`<br>`POST /v1/audio/translations` | `/playground` | `partial` | 只接 speech；转写、翻译无 UI。 |
| OpenAI Responses/Codex | `POST /v1/responses`<br>`POST /v1/responses/compact`<br>`POST /backend-api/codex/responses`<br>`POST /backend-api/codex/responses/compact` | `/playground` | `partial` | 标准 Responses 普通/流式已接；compact 与两个 Codex 兼容别名无 UI。 |
| Anthropic Messages | `POST /v1/messages`<br>`POST /v1/messages/count_tokens` | `/playground` | `partial` | Messages 普通/流式已接；独立 count_tokens 无 UI。 |
| Gemini native | `GET /v1beta/models`<br>`GET /v1beta/models/{rest}`<br>`POST /v1beta/models/{rest}` | `/playground` | `partial` | generateContent、countTokens、embedContent、batchEmbedContents 可试用；通配路由并非所有 `{rest}` 能力都有专用控件。 |
| Realtime 占位 | `GET /v1/realtime` | 无 | `none` | 真码固定返回 `501 realtime_not_available`（`routes.go:449-451`），不能写成已实现；前端也无入口。 |
| 协议归一、HCSF、SSE、流式用量、重试与缓存 | 无人工端点 | 无 | `backend_only` | `proto/*`、`gateway/*`、`protosse`、`streamdelivery`、`streamusage` 等为协议和请求链核心。 |

### 8. 模型注册路由

| 能力 | 端点 | 前端页面 | coverage | 缺口 |
|---|---|---|---|---|
| API 模型目录 | `GET /v1/models`<br>`GET /v1/models/{model}` | `/playground` | `partial` | 模型列表被 Playground 消费；独立模型详情无页面调用。 |
| 模型能力、Alias、binding 与租户策略 | `PUT /v1/admin/models/{id}/capabilities`<br>`POST /v1/admin/models/aliases/bulk-import`<br>`GET /v1/admin/models/{id}/capability-bindings`<br>`PUT /v1/admin/models/{id}/capability-bindings`<br>`GET /v1/admin/model-registry-policy`<br>`PUT /v1/admin/model-registry-policy` | `/admin/model-registry` | `full` | 所有运行时 method 均有页面；注意 OpenAPI 当前漏列 capability binding 的 PUT，这是契约缺口而非前端缺口。 |
| 模型到池 binding | `GET /admin/v1/model-pool-bindings`<br>`POST /admin/v1/model-pool-bindings`<br>`GET /admin/v1/model-pool-bindings/{id}`<br>`PATCH /admin/v1/model-pool-bindings/{id}`<br>`DELETE /admin/v1/model-pool-bindings/{id}` | `/routing` | `partial` | 列表、新建、编辑、删除完整；独立 GET 详情封装未调用。 |
| 上游模型同步 | `POST /admin/v1/model-sync` | `/admin/model-sync` | `full` | 可选账号、拉取上游模型并执行同步。 |
| 路由规则 | `GET /v1/admin/routes`<br>`POST /v1/admin/routes`<br>`GET /v1/admin/routes/{id}`<br>`PUT /v1/admin/routes/{id}`<br>`DELETE /v1/admin/routes/{id}`<br>`PUT /v1/admin/routes/{id}/enabled` | `/admin/route-rules` | `partial` | CRUD/启停实际可用；独立 GET 详情封装未调用。OpenAPI 当前漏列 PUT，但前端真码已调用。 |
| 公开模型排行 | `GET /v1/public/rankings` | `/rankings` | `full` | 公开页实际消费。 |
| Alias 解析、fallback、能力匹配和 snapshot | 无人工端点 | 无 | `backend_only` | `registry`、`modelfallback`、`router` 在请求热路径执行。 |

### 9. 出口代理 TLS

| 能力 | 端点 | 前端页面 | coverage | 缺口 |
|---|---|---|---|---|
| 出站代理 CRUD、状态与测试 | `GET /admin/v1/proxies`<br>`POST /admin/v1/proxies`<br>`GET /admin/v1/proxies/{id}`<br>`PATCH /admin/v1/proxies/{id}`<br>`DELETE /admin/v1/proxies/{id}`<br>`PUT /admin/v1/proxies/{id}/status`<br>`POST /admin/v1/proxies/{id}/test` | `/admin/proxies` | `partial` | 列表、创建、编辑、删除、启停、测试均消费；独立 GET 详情没有前端封装，编辑依赖列表行数据。 |
| TLS 指纹 profile | `GET /v1/admin/tls-fingerprint-profiles`<br>`POST /v1/admin/tls-fingerprint-profiles`<br>`GET /v1/admin/tls-fingerprint-profiles/{id}`<br>`PUT /v1/admin/tls-fingerprint-profiles/{id}`<br>`DELETE /v1/admin/tls-fingerprint-profiles/{id}`<br>`POST /v1/admin/tls-fingerprint-profiles/{id}/status` | `/admin/tls-fingerprints` | `partial` | 列表、创建、编辑、删除、状态完整；独立 GET 详情封装未调用。 |
| Account 指纹绑定 | `PATCH /admin/v1/provider-accounts/{id}/fingerprint-profile`（三个 Account 前缀均挂载） | `/accounts/:id` | `full` | 账号详情可绑定和解绑 profile。 |
| SSRF、代理密钥、探测、uTLS/sidecar 与 TLS 漂移 | 无人工端点；固定 canary 探测由 `routes_proxy_probe.go` 组装 | 无 | `backend_only` | 安全和传输基建，不应从前端直接绕过策略。 |

### 10. 媒体任务

| 能力 | 端点 | 前端页面 | coverage | 缺口 |
|---|---|---|---|---|
| 通用媒体任务 | `GET /v1/media-tasks`<br>`POST /v1/media-tasks`<br>`GET /v1/media-tasks/{id}` | `/media-tasks` | `full` | 新建、列表、详情完整。 |
| Midjourney 兼容 | `POST /mj/submit/{action}`<br>`POST /mj/insight-face/swap`<br>`GET /mj/task/{id}/fetch`<br>`GET /mj/task/{id}/image-seed`<br>`POST /mj/task/list-by-condition` | `/media-tasks` | `full` | 五类端点均有页面动作。 |
| Suno | `POST /suno/submit`<br>`POST /suno/submit/{action}`<br>`GET /suno/fetch`<br>`GET /suno/fetch/{id}` | `/media-tasks` | `full` | 提交、动作、单查/查询均覆盖。 |
| 视频 | `POST /video/submit`<br>`GET /video/fetch`<br>`GET /video/fetch/{id}` | `/media-tasks` | `full` | 提交、单查和列表查询覆盖。 |
| 孤儿任务对账 | `GET /admin/v1/media-task-orphans`<br>`POST /admin/v1/media-task-orphans/{id}/reconcile` | `/admin/orphan-reconcile` | `full` | 列表与人工对账有确认流程。 |
| 租约、轮询、结算、失败追扣 worker | 无人工端点 | 无 | `backend_only` | `mediatask` worker 负责状态机和结算。 |

### 11. 运维观测告警

| 能力 | 端点 | 前端页面 | coverage | 缺口 |
|---|---|---|---|---|
| 存活与系统健康 | `GET /healthz`<br>`HEAD /healthz`<br>`GET /v1/admin/system/health`<br>`GET /admin/v1/system/health` | `/health` | `full` | 页面使用规范 admin 健康端点；`healthz` 是探针、别名前缀无需重复 UI。 |
| 模块知识 | `GET /admin/v1/modules`<br>`GET /v1/admin/modules` | `/admin/modules` | `full` | 后端只有只读模块知识端点，页面完整展示；导航若称“模块开关”会造成认知误导，但不存在被遗漏的后端 mutation。 |
| 备份 manifest | `GET /v1/admin/backup/manifest` | `/admin/backup` | `full` | 当前后端只有 manifest，页面完整展示。页面标题“备份与恢复”不代表后端已有导出/恢复端点。 |
| 日志级别与运行日志 | `GET /admin/v1/loglevel`<br>`PUT /admin/v1/loglevel`<br>`GET /v1/admin/loglevel`<br>`PUT /v1/admin/loglevel`<br>`GET /v1/admin/ops/runtime-logs`<br>`GET /v1/admin/ops/runtime-logs/health`<br>`POST /v1/admin/ops/runtime-logs/cleanup` | `/admin/logs` | `partial` | 日志级别、列表和 sink 健康已用；cleanup 虽有 API 封装，页面没有按钮，运维无法清理。 |
| 告警规则、事件与静默 | `GET /v1/admin/alert-rules`<br>`POST /v1/admin/alert-rules`<br>`GET /v1/admin/alert-rules/{id}`<br>`PUT /v1/admin/alert-rules/{id}`<br>`DELETE /v1/admin/alert-rules/{id}`<br>`GET /v1/admin/alert-events`<br>`POST /v1/admin/alert-events/{id}/manual-resolve`<br>`GET /v1/admin/alert-silences`<br>`POST /v1/admin/alert-silences`<br>`DELETE /v1/admin/alert-silences/{id}` | `/admin/alerting` | `partial` | 三个 Tab 的列表、写入、筛选和人工解除均可用；独立 GET 单条规则没有前端封装，编辑依赖列表行数据。 |
| DLQ | `GET /admin/v1/dlq/{handler}`<br>`POST /admin/v1/dlq/{id}/replay`<br>`POST /admin/v1/usage-record-dlq/{id}/replay`<br>`GET /admin/v1/obs-dlq`<br>`POST /admin/v1/obs-dlq/{id}/replay` | `/admin/dlq` | `full` | 业务、用量和观测 DLQ 均可查和重放。 |
| L2 cache 运维 | `GET /admin/v1/cache/l2/stats`<br>`DELETE /admin/v1/cache/l2/{key}` | `/admin/cache` | `full` | 统计与单 key 删除完整。 |
| 版本信息 | `GET /admin/v1/version`<br>`GET /v1/admin/version` | `/admin/version` | `full` | 页面使用规范别名；无需双发。 |
| Hermes 设置、对话、历史、上下文和工具 | `GET /v1/hermes/settings`<br>`POST /v1/hermes/settings/enable`<br>`POST /v1/hermes/settings/disable`<br>`GET /v1/hermes/api-profiles`<br>`POST /v1/hermes/api-profiles`<br>`GET /v1/hermes/api-profiles/{id}`<br>`DELETE /v1/hermes/api-profiles/{id}`<br>`POST /v1/hermes/chat`<br>`GET /v1/hermes/conversations`<br>`GET /v1/hermes/conversations/{id}`<br>`DELETE /v1/hermes/conversations/{id}`<br>`GET /v1/hermes/conversations/{id}/messages`<br>`GET /v1/hermes/tools`<br>`POST /v1/hermes/tool-execute`<br>`GET /v1/hermes/context` | `/admin/hermes` + AppShell 的 Hermes 面板 | `full` | 配置、profile、SSE 对话、历史、删除、上下文和工具 dry-run/确认均有真实 UI。 |
| 内部 runner 与指标 | `POST /internal/runner/bootstrap`<br>`POST /internal/runner/refresh`<br>`GET /internal/keys`<br>条件 `POST /internal/hermes/tool-execute`<br>`* /debug/vars`<br>条件 `* /metrics` | 无 | `backend_only` | runner、expvar、Prometheus 是机器接口。 |

### 12. 平台管理租户

| 能力 | 端点 | 前端页面 | coverage | 缺口 |
|---|---|---|---|---|
| 首装 | `GET /setup/status`<br>`POST /setup/install` | `/setup` | `full` | 状态检查和安装向导完整。 |
| 匿名站点配置 | `GET /v1/site/config` | `/login`、`/welcome`、`/legal` | `full` | 登录方式、品牌和公开内容均消费。 |
| 平台设置 | `GET /v1/admin/platform-settings`<br>`GET /v1/admin/platform-settings/{key}`<br>`PUT /v1/admin/platform-settings/{key}` | `/system` | `partial` | 列表、按 allowlist 分组和逐项编辑可用；单 key GET 没有前端调用，单项刷新只能重拉全量列表。 |
| SMTP 与邮件模板 | `GET /v1/admin/email/settings`<br>`PUT /v1/admin/email/settings`<br>`POST /v1/admin/email/test`<br>`POST /v1/admin/email/templates/preview` | `/system`、`/admin/version` | `full` | SMTP 回填/保存/测试、模板编辑/服务端预览完整。 |
| 公告 | `GET /v1/announcements`<br>`GET /v1/admin/announcements`<br>`POST /v1/admin/announcements`<br>`PUT /v1/admin/announcements/{id}`<br>`DELETE /v1/admin/announcements/{id}` | `/overview`、`/admin/announcements` | `full` | 用户生效公告与管理员 CRUD 完整。 |
| 通知偏好与站内信 | `GET /v1/users/me/notifications`<br>`PUT /v1/users/me/notifications`<br>`GET /v1/admin/users/{user_id}/notifications`<br>`PUT /v1/admin/users/{user_id}/notifications`<br>`GET /v1/notifications`<br>`GET /v1/notifications/unread-count`<br>`POST /v1/notifications/{id}/read`<br>`POST /v1/admin/notifications/broadcast` | `/profile`、`/notifications`、`/users/:id`、`/admin/broadcast` | `full` | 用户偏好、管理员代管、收件箱、未读、已读和广播完整。 |

真码边界：当前未发现 `/tenants` 或 tenant CRUD HTTP 路由，只有各 admin 端点的 `tenant_id` 作用域和模型目录继承策略。因此这里不虚构一个“租户管理能力”，也不对不存在的后端端点给 coverage 判定；若 Owner 需要租户生命周期管理，应先补后端契约。

### 13. 安全合规审核

| 能力 | 端点 | 前端页面 | coverage | 缺口 |
|---|---|---|---|---|
| 公钥、审计验签与 Merkle | `GET /.well-known/huakai-pubkey.json`<br>`POST /v1/trust/verify`<br>`GET /v1/audit/pubkey`<br>`GET /v1/audit/pubkeys`<br>`GET /v1/audit/pubkey/{fingerprint_hex}`<br>`GET /v1/audit/verify`<br>`POST /v1/audit/verify`<br>`GET /v1/audit/merkle-tree.json` | `/trust` | `partial` | 公钥列表/指纹、POST 验签、Merkle、trust verify 均有 UI；GET verify 变体有封装但页面未调用；well-known 是机器发现接口。 |
| 审计事件、证明与导出 | `GET /v1/me/audit-events`<br>`GET /admin/v1/audit-events`<br>`GET /v1/audit/proof/{request_id}.json`<br>`GET /v1/audit/export` | `/activity`、`/security` | `full` | 用户活动、管理审计、单条证明和链导出均消费。 |
| 成本收据与争议 | `GET /v1/receipts/{request_id}`<br>`POST /v1/receipts/{request_id}/verify`<br>`POST /v1/receipts/{request_id}/disputes`<br>`GET /v1/receipts/{request_id_host}/{request_id_tail}`<br>`POST /v1/receipts/{request_id_host}/{request_id_tail}/verify`<br>`POST /v1/receipts/{request_id_host}/{request_id_tail}/disputes`<br>`GET /v1/me/disputes`<br>`GET /v1/admin/disputes`<br>`POST /v1/admin/disputes/{id}/resolve` | `/usage-records`、`/admin/disputes` | `full` | 前端按段编码 request ID，兼容单段/双段；查看、验签、发起、列表和裁决完整。 |
| 风险总览 | `GET /admin/v1/risk/overview` | `/admin/risk` | `full` | 真实只读总览。 |
| 内容审核 | `GET /admin/v1/moderation/config`<br>`PUT /admin/v1/moderation/config`<br>`GET /admin/v1/moderation/logs`<br>`GET /admin/v1/moderation/keywords`<br>`POST /admin/v1/moderation/keywords`<br>`POST /admin/v1/moderation/keywords/bulk`<br>`DELETE /admin/v1/moderation/keywords/{id}`<br>`GET /admin/v1/moderation/hashes`<br>`POST /admin/v1/moderation/hashes`<br>`POST /admin/v1/moderation/hashes/bulk`<br>`DELETE /admin/v1/moderation/hashes/{id}`<br>`GET /admin/v1/moderation/banned`<br>`POST /admin/v1/moderation/api-keys/{id}/unban` | `/admin/moderation` | `full` | 配置、日志、关键词/hash 规则、批量导入、封禁和解封均完整。 |
| 隐私、脱敏、Header firewall、SSRF 与解压限制 | 无人工端点 | 无 | `backend_only` | 这些是默认强制的安全基建，不应由普通 UI 关闭。 |

## 二、模型厂商接入专项

### 2.1 先把“接入”拆成四层

后端真码不是一个简单的“支持/不支持”布尔值：

1. `provider/registrydefault` 有 **39 个协议族注册路径**：32 个默认注册、7 个环境变量门控。
2. `servingcapability` 把它们明确分成 **21 个 Released、7 个 Experimental、11 个 Scaffold**（`contracts.go:149-280`）。
3. `credentialacq` 定义 40 个 ModePlan，但 `credentialstore` 只有 34 个真正的 credential handler；不可落凭据的 adapter 不能称为端到端可用。
4. 前端 `/admin/catalogs` 有 42 个协议字符串，下拉比后端 canonical 集多 3 个；`/accounts` 又只显示数据库中已手工创建的 provider/channel，并不自动展示所有 Released 厂商。

`proto` 的专用协议子包只有 Anthropic、Bedrock、Dify、Gemini、Gemini Code Assist、Ollama、OpenAI；Grok、Kimi、DeepSeek、通义、智谱、文心等复用通用 OpenAI-compatible 协议层，这是复用，不是缺包。

下表“条件可配”表示：对应 credential handler 和 account mode 存在，`tenant_operator` 或带有效 tenant scope 的请求可完成；但仍需先在 `/admin/catalogs` 手工建立 provider/channel。**当前无隐式 scope 的全局 `platform_admin` 因 tenant_id 漏传，所有“条件可配”项在新建弹窗中都可能被 P0 阻断。**

| 厂商 | 后端支持协议 + 凭证 | 前端可配？ | 缺口 |
|---|---|---|---|
| Anthropic 官方 API | `anthropic_messages`；API Key；Released（`registrydefault/default.go:235-238`，`contracts.go:165-168`） | 条件可配 | 通用动态字段可完成；没有厂商卡片，须先手工建目录。 |
| Claude OAuth / Claude Code | `anthropic_claude_session`；`claude_ai_oauth`、`claude_code`；Released | 条件可配 | OAuth、CLI/JSON 导入可用；仍受通用入口与 tenant scope 问题影响。 |
| AWS Bedrock | `bedrock_invoke`；AWS AK/SK/region、SigV4；Released | 部分可配 | 基础三字段可填；`cloud_bootstrap`、AWS SSO 没有专属 UI。当前代码只证明 Anthropic/Claude 形的 Bedrock 路径。 |
| Vertex AI Anthropic | `vertex_anthropic`；access token、metadata token endpoint、client email；Released | 部分可配 | 主向导没有完整 private key、project/location 引导，复杂形态依赖原始 JSON/账号配置。 |
| OpenAI 官方 API | `openai_chat`、`openai_responses`；API Key；Released | 条件可配 | 通用入口能配；须先建目录。 |
| OpenAI Codex / ChatGPT session | `openai_codex`；`chatgpt_oauth`、`codex_cli_oauth`、`codex_web_oauth`；Released | 部分可配 | 浏览器 OAuth/导入可用；Device Code 的 `user_code` 和验证地址不展示。 |
| Azure OpenAI | 复用 OpenAI family；`azure` mode | 部分可配 | Azure API Key 在运行时明确 fail-closed；仅 Entra access token + 完整 `base_url` 可走，而主向导未暴露完整 endpoint 配置（`credentialstore/types.go:239-250,290`）。 |
| Gemini AI Studio | `gemini_messages`；`aistudio_api_key`；Released | 条件可配 | 通用入口能配；须先建目录。 |
| Vertex AI Gemini | `vertex_gemini`；`vertex_sa`；Released | 部分可配 | 与 Vertex Anthropic 同样缺完整云凭证结构化引导。 |
| Gemini Code Assist | `gemini_code_assist`；OAuth/session；Experimental、默认 env-off | 只能建凭据，默认不可发流量 | UI 不显示 `serving_readiness`，会把默认关闭的实验能力展示成普通可选模式。 |
| Gemini Advanced / Google One | `gemini_advanced_session`；`google_one` session；Experimental、默认 env-off | 只能建凭据，默认不可发流量 | 真实 wire 未闭环；UI 不展示 readiness。 |
| Antigravity | `antigravity_session`；Gemini `antigravity` 或 Antigravity `oauth` session；Experimental、默认 env-off | 只能建凭据，默认不可发流量 | Cloud Code wire 已有，但未发布；前端协议下拉还同时含无效旧值 `antigravity`。 |
| GitHub Copilot | `copilot_session`；`copilot_oauth`；Experimental、默认 env-off | 部分可配 | 可建/导入凭据；浏览器 callback exchanger fail-closed，缺 Device Code 专用 UI，且 readiness 不展示。 |
| Cursor | `cursor_session`；Experimental、默认 env-off | 不可配 | 前端静态 vendor 有 Cursor 文案，但无 ModePlan、无 credential handler，提交会被后端拒绝；`Mandatory Roadmap`。 |
| AWS Kiro | `kiro_session`；预期 AWS SSO/session；Experimental、默认 env-off | 不可配 | 无 vendor handler、无 account mode；`Mandatory Roadmap`。 |
| Windsurf | `windsurf_session`；OAuth/session；Experimental、默认 env-off | 部分可配 | 只能手工 token；缺 `token_exchange` 专用 UI，默认不可 serving。 |
| xAI Grok 官方 API | `grok_chat`；API Key、`xai_oauth`；Released | 条件可配 | 官方 API 路径可配；须先建目录。 |
| Grok 网页 session | `provider/grok` 有独立 session adapter 代码，但未注册任何 serving family、未接凭据目录 | 不可配 | 不能与已发布的 xAI API 路径混写；目前是孤立实现。 |
| Kimi API Key | `kimi_chat`；API Key；Released | 条件可配 | 通用入口能配。 |
| Kimi OAuth | 同 `kimi_chat`；`kimi_oauth` Device Code；Released | 部分可配 | 前端不展示 `user_code`/验证地址，Device Code 不闭环。 |
| DeepSeek | `deepseek_chat`；API Key；Released | 条件可配 | 通用入口能配；须先建目录。 |
| 通义千问 Qwen | `qwen_chat`，DashScope OpenAI-compatible；API Key；Released | 条件可配 | 通用入口能配；没有通义专属卡片/地域说明。 |
| 智谱 GLM | `glm_chat`，BigModel OpenAI-compatible；API Key；Released | 条件可配 | 通用入口能配；没有智谱专属卡片。 |
| 零一万物 Yi | `yi_chat`，OpenAI-compatible；API Key；Released | 条件可配 | 通用入口能配。 |
| 百川 | `baichuan_chat`，OpenAI-compatible；API Key；Released | 条件可配 | 通用入口能配。 |
| 豆包 / 火山方舟 | `doubao_chat`，Ark OpenAI-compatible；API Key；Released | 条件可配 | 通用入口能配；无地域/endpoint 引导。 |
| 文心 ERNIE / 千帆 | `ernie_chat`，千帆 v2 OpenAI-compatible；API Key；Released | 条件可配 | 通用入口能配；真码没有证明旧式百度原生协议，不能宣称支持。 |
| 阶跃 Step | `step_chat`，OpenAI-compatible；API Key；Released | 条件可配 | 通用入口能配。 |
| 腾讯混元 | `hunyuan_chat`，OpenAI-compatible；API Key；Released | 条件可配 | 通用入口能配。 |
| MiniMax | `minimax_chat`，OpenAI-compatible；API Key；Released | 条件可配 | 通用入口能配；无国内/国际 endpoint 引导。 |
| OpenRouter | `openrouter_chat` adapter；API Key 设计；Scaffold | 不可配 | ModePlan 被 experimental/flag 隐藏且无 handler，协议下拉和静态 vendor 文案制造“似乎支持”的错觉。 |
| Mistral | `mistral_chat` adapter；Scaffold、`product_not_released` | 不可配 | 无 credential handler，后端存储层拒绝。 |
| GroqCloud | `groqcloud_chat` adapter；Scaffold | 不可配 | 无 credential handler。 |
| Together | `together_chat` adapter；Scaffold | 不可配 | 无 credential handler。 |
| Perplexity | `perplexity_chat` adapter；Scaffold | 不可配 | 无 credential handler。 |
| Fireworks | `fireworks_chat` adapter；Scaffold | 不可配 | 无 credential handler。 |
| Cohere | `cohere_chat` OpenAI-compatible adapter；Scaffold | 不可配 | 无 vendor mode/credential handler，只有协议下拉。 |
| Ollama | `ollama_chat` + `ollama_native`；OpenAI-compatible + 原生 NDJSON；Scaffold | 不可配 | 无 account mode/handler；即使部署可无鉴权，当前新建流程仍要求模式和凭据，无法闭环。 |
| Dify | `dify_chat`；per-app token；Scaffold | 不可配 | 无 vendor mode/handler；当前真码只证明 chatflow/agent，workflow/completion fail-closed。 |
| Replicate | `replicate_image`；API token；仅图片 lane；Scaffold | 不可配 | adapter 已有，但无 account mode/credential handler。 |

### 2.2 前端“新建上游账号”真实工作流

当前不是逐厂商配置页，而是：

1. `/admin/catalogs` 手工创建 provider，选择协议字符串；
2. `/admin/catalogs` 手工创建 channel；
3. `/accounts` 打开通用弹窗，读取 provider、channel、account-mode 三个目录；
4. 弹窗按 `required_fields` 生成凭证输入，再 `POST /admin/v1/provider-accounts`。

这个设计的真实问题不是“一个入口都没有”，而是：

- 厂商不可发现：Released 厂商不会自动以卡片/预设出现；空数据库时下拉就是空的。
- 平台管理员不可用：目录和创建请求漏 `tenant_id`。
- 发布态不可见：前端丢弃 `serving_readiness`。
- 组合不可校验：provider、protocol、vendor、auth mode 没有联动。
- 字段渲染不完整：`select`、`boolean` 等 schema 类型退化成普通文本框。
- 高级凭证不闭环：云 bootstrap、token exchange、setup token、manual-first、Device Code 都缺专用交互。

## 三、前端覆盖缺口清单

| 优先级 | 缺口 | coverage | 谁用不到 | 真码证据与影响 |
|---|---|---|---|---|
| P0 | 全局 `platform_admin` 新建账号漏 `tenant_id` | `partial` | 运维用不到 | `AccountsPage.tsx:184-200` 未向 modal 传 tenant；`createApi.ts:36-58` 的 provider/channel 查询和创建 POST 均无 tenant；后端 `provider_catalog_handler.go:161-168`、`admin_pool_accounts_handler.go:685-723` 明确要求无隐式 scope 的平台管理员传 `?tenant_id=N`。 |
| P0 | 前端丢弃 `serving_readiness` | `partial` | 运维会误判 | 后端 account-mode 响应含 release state、ready、traffic_allowed、action/reason；`createTypes.ts:23-39` 无该字段，modal 只过滤 `is_enabled`。Experimental/env-off 模式会被误当成普通可用项。 |
| P0 | 厂商配置入口不可发现、无预设 | `partial` | 运维找不到/配不出 | `/accounts` 只显示 DB 里已建 provider/channel；Released 21 个协议族不会自动出现，操作者必须先理解协议常量并去 `/admin/catalogs` 手工建两级目录。 |
| P1 | provider、protocol、vendor、auth mode 无合法矩阵联动 | `partial` | 运维易误配 | `CredentialPanel` 把 25×19 做任意组合；Create modal 的 mode 也不按所选 provider 过滤。大量组合 UI 可选、后端必拒。 |
| P1 | 动态凭证字段只完整处理文本/textarea/JSON | `partial` | 运维易误配 | `CreateAccountModal.tsx:156-183` 把 `select`、`boolean` 等退化为文本输入；云凭证、开关型字段缺类型化控件。 |
| P1 | 四类 acquisition flow 无入口 | `none` | 运维用不到 | 后端有 `cloud_bootstrap`、`token_exchange`、`setup_token`、`manual_first`；前端向导只列 oauth/paste/cli/csv/json。 |
| P1 | Device Code 不闭环 | `partial` | 运维用不到 | 后端内部能产生 `user_code` 与验证地址，但 HTTP handler只回 authorize URL/challenge，前端也不展示；影响 Codex CLI、Kimi OAuth、Copilot 等流程。 |
| P1 | Bedrock、Vertex、Azure 缺结构化云配置 | `partial` | 运维难以安全配置 | Bedrock 缺 cloud bootstrap/SSO；Vertex 缺完整 project/location/private-key 引导；Azure API Key fail-closed，主向导又缺完整 base URL/Entra 引导。 |
| P1 | Playground 未覆盖全部已注册入站协议 | `partial` | 用户无法在 UI 试用 | 缺图片 edits/variations、音频 transcriptions/translations、Responses compact、Codex aliases、Messages count_tokens、engine embeddings alias、模型详情；Realtime 本身仍是 501。 |
| P1 | 运行日志 cleanup 无按钮 | `none` | 运维用不到 | `logsdiag/api.ts:70-71` 已封装 `POST /v1/admin/ops/runtime-logs/cleanup`，`RuntimeLogsPanel` 只用列表和健康。 |
| P1 | 渠道健康详情未展示 | `none`（端点级） | 运维看不到详情 | `channelhealth/api.ts` 已封装 `GET /v1/admin/channel-health/{channel_id}`，页面只用 list、summary 和状态动作。 |
| P1 | 11 个 Scaffold 协议没有端到端凭证路径 | `none` | 运维用不到 | OpenRouter、Mistral、GroqCloud、Together、Perplexity、Fireworks、Cohere、Ollama 两族、Dify、Replicate 有 adapter/协议名但缺 credential handler；前端协议下拉会制造“已支持”错觉。 |
| P1 | Cursor、Kiro 只有实验 adapter 路径，无 account mode/handler | `none` | 运维用不到 | 两者在后端明确为 `Mandatory Roadmap`；Cursor 甚至在静态 vendor 下拉出现但提交必拒。 |
| P2 | 前端协议下拉多出 3 个后端不接受值 | `partial` | 运维会遇到 400 | 前端 42 项包含 `gemini`、`bedrock`、`antigravity`；后端 canonical mutation 只接受 `registrydefault`/contract 的正式 family，三者不是 family。 |
| P2 | 多个详情 GET 没有被页面消费 | `partial` | 用户/运维少一层详情 | 涉及用户 API Key、quota policy、route rule、pool、proxy、TLS profile、channel test template、alert rule、platform setting 的单条 GET；主工作流可用，但独立详情/刷新不完整。 |
| P2 | `GET /v1/pricing/rate-table?version=...` 与 trust GET verify 未使用 | `partial` | 用户少一种查询方式 | 快照详情和 POST 验签已有等价主工作流，不阻断核心能力。 |
| P2 | 导航文案超出后端事实 | `partial` | 运维认知误导 | “模块开关”实际上只有只读 module list；“备份与恢复”实际上只有 manifest。不是被漏接的后端端点，但应改成真相文案或另立 Mandatory Roadmap。 |

## 四、给 Owner 的下一步建议

1. **P0 先修“能不能建账号”**：给运营台提供明确 tenant 选择；把同一个 `tenant_id` 带入 provider、channel 查询和 Provider Account 创建。修后用全局 `platform_admin` 与 `tenant_operator` 两种身份各跑一遍真实创建闭环。
2. **P0 让发布态成为 UI 一等信息**：完整消费 `serving_readiness`，对 Released / Experimental / Scaffold 分色；`traffic_allowed=false` 时禁止“启用流量”，并展示后端 action/reason。
3. **P0/P1 建立厂商配置矩阵**：由后端下发 `厂商 → 协议 → account mode → credential schema → acquisition flow → readiness`，前端渲染厂商卡片和搜索；不要再让操作者手工拼 protocol/vendor/auth mode。
4. **P1 为 21 个 Released 协议族提供可发现预设**：至少预置名称、默认协议、凭证形态、endpoint/地域提示。预设可创建目录记录，但不能绕过租户、审计和凭证校验。
5. **P1 补高级凭证向导**：优先 Device Code、Bedrock cloud bootstrap、Vertex SA、Azure Entra/base URL、token exchange；secret 继续只写不回显。
6. **P1 补日常运维闭环**：日志清理、渠道健康详情；Playground 再补已发布且用户会实际调试的协议，不要为 501 Realtime 做假入口。
7. **P2 清理“有封装无页面”的详情端点**：确有操作价值的补抽屉；列表响应已安全等价的，记录等价关系并移除孤儿封装。
8. **Scaffold 不删功能，但必须诚实标态**：OpenRouter 等 11 个协议族继续保留为 `Mandatory Roadmap`/`Feature Flag`，在 credential handler、发布门和前端向导闭合前，不对 Owner 宣称“可配置”。这不构成功能缩水，而是把 adapter、实验、正式发布三种状态说清楚。

## 读过的包列表

下面是本次按 `go list ./internal/...` 逐包检索并归类的 268 个递归包路径；它完整覆盖题目中的 209 个一级目录。

```text
internal/accesslog
internal/accountfphttp
internal/admin
internal/adminhttp
internal/adminquotahttp
internal/adminsessionauth
internal/adminsessionauthtest
internal/adminuserhttp
internal/affinityrules
internal/alerting
internal/alertinghttp
internal/alertmetrics
internal/announcement
internal/announcementhttp
internal/anthropicoauth
internal/apikeyexpiry
internal/apikeyipallow
internal/apikeyipdeny
internal/apikeymodelallow
internal/apikeyns
internal/audiohttp
internal/audiopricing
internal/audit
internal/auditexporthttp
internal/auditledger
internal/auth
internal/authaudit
internal/authcooldown
internal/authpolicy
internal/authpolicyadapter
internal/backuphttp
internal/billing
internal/billingdsl
internal/billingreconhttp
internal/bodyfeatures
internal/bodyparamgate
internal/budget
internal/budgetenforce
internal/buildinfo
internal/cache
internal/cache_routing
internal/cachemetrics
internal/cacheplan
internal/captcha
internal/channelhealth
internal/channelprobe
internal/checkin
internal/checkinhttp
internal/circuitbreaker
internal/clienterr
internal/clientid
internal/clientip
internal/codebudget
internal/codexclientaccess
internal/community/invitation
internal/completionshttp
internal/config
internal/controlhttp
internal/credentialacq
internal/credentialacq/accountident
internal/credentialacq/projectenrich
internal/credentialprojecthttp
internal/credentialstore
internal/credentialworker
internal/credentialworker/adapters
internal/db
internal/db/admin
internal/db/audit
internal/db/auth
internal/db/billing
internal/db/hermes
internal/db/hermestoolsdb
internal/db/moderation
internal/db/platformsettings
internal/db/pricingcatalog
internal/db/quota
internal/db/quotaadmin
internal/db/registry
internal/db/twofa
internal/db/userkeycontrols
internal/dbmigrate
internal/dlq
internal/email
internal/emailpolicy
internal/emailsendlimit
internal/embeddingshttp
internal/engineembeddingsalias
internal/eventbus
internal/exporthttp
internal/gateway
internal/gateway/codexreqctl
internal/gateway/streamdelivery
internal/gateway/streamusage
internal/gatewayhttp
internal/gatewayhttp/accountcreate
internal/gatewayhttp/bodymodel
internal/gatewayhttp/chatpipe
internal/gatewayhttp/clientgate
internal/geminihttp
internal/headerfirewall
internal/healthhttp
internal/healthscore
internal/hermes
internal/hermesadmin
internal/hermeschat
internal/hermesconfirm
internal/hermeshttp
internal/hermesops
internal/hermesops/mutateguard
internal/httpkeepalive
internal/imagepricing
internal/imageshttp
internal/invitevalidatehttp
internal/invoicehttp
internal/logfacade
internal/loginthrottle
internal/loglevel
internal/logsink
internal/mediatask
internal/mediataskhttp
internal/meexporthttp
internal/megroupshttp
internal/mequotahttp
internal/meusagehttp
internal/mimicryidentity
internal/mixedchannelrisk
internal/mjclient
internal/modelbindingadminhttp
internal/modelfallback
internal/modelsync
internal/moderation
internal/moderationhttp
internal/modulecatalog
internal/modulehttp
internal/moduleregistry
internal/notify
internal/oauthpendinghttp
internal/obs
internal/obs/dlq
internal/obsconfig
internal/obsdlqhttp
internal/observability
internal/observability/accounthealthprobe
internal/officialclient
internal/openapicheck
internal/orphanreconcilehttp
internal/otelbridge
internal/panelauth
internal/paramgate
internal/passkey
internal/passkeyhttp
internal/payloadhash
internal/payment
internal/paymenthttp
internal/platformsettings
internal/pool
internal/pool/binding
internal/pool/dispatcher
internal/pool/queuewait
internal/pool/router
internal/pricingcatalog
internal/pricingcataloghttp
internal/pricingeval
internal/pricingpublichttp
internal/privacy
internal/proto
internal/proto/anthropic
internal/proto/bedrock
internal/proto/dify
internal/proto/gemini
internal/proto/geminicodeassist
internal/proto/ollama
internal/proto/openai
internal/protosse
internal/provider
internal/provider/anthropic
internal/provider/antigravity
internal/provider/bedrock
internal/provider/bedrock/eventstream
internal/provider/copilot
internal/provider/cursor
internal/provider/dify
internal/provider/gemini
internal/provider/grok
internal/provider/kiro
internal/provider/ollama
internal/provider/openai
internal/provider/openai_codex
internal/provider/openrouter
internal/provider/registrydefault
internal/provider/replicate
internal/provider/vertex
internal/provider/vertexsa
internal/provider/windsurf
internal/proxyadmin
internal/proxyadminhttp
internal/proxyhealth
internal/proxysecret
internal/publicrankinghttp
internal/quota
internal/quotaenforce
internal/quotaprobe
internal/rate
internal/rate/precheck
internal/realtokenizer
internal/recentreq
internal/redact
internal/referralhttp
internal/registry
internal/relaybody
internal/reqdecompress
internal/rerankhttp
internal/respdecompress
internal/responsescompacthttp
internal/retrybudget
internal/riskoverviewhttp
internal/routeadmin
internal/router
internal/sensitiveobfuscate
internal/servingcapability
internal/sessioncap
internal/settingscipher
internal/settlementintent
internal/settlementrecovery
internal/setuphttp
internal/sign
internal/sitepublichttp
internal/ssrfpolicy
internal/subscription
internal/subscriptionenforce
internal/subscriptionhttp
internal/sunoclient
internal/systemhealthhttp
internal/telegramauth
internal/tenancy
internal/textsafe
internal/thinkingnorm
internal/tlsfpadmin
internal/tlsfphealth
internal/tlsfphttp
internal/tlsfpresolve
internal/tokencheck
internal/tokenestimate
internal/toolpricing
internal/transport
internal/transport/mimicry
internal/trust
internal/trusthttp
internal/trustreceipt
internal/twofa
internal/usageanalyticshttp
internal/usageretention
internal/userauditlog
internal/userauditloghttp
internal/userauth
internal/userkey
internal/userkeycontrols
internal/userkeycontrolshttp
internal/userkeyhttp
internal/usernotice
internal/usernoticehttp
internal/usersession
internal/videoclient
internal/voucher
internal/voucherhttp
internal/warmupintercept
internal/webui
internal/windowcost
```

读取的 15 个生产路由文件：

```text
backend/cmd/gateway/routes.go
backend/cmd/gateway/routes_alerting.go
backend/cmd/gateway/routes_backup.go
backend/cmd/gateway/routes_invitevalidate.go
backend/cmd/gateway/routes_moderation.go
backend/cmd/gateway/routes_modules.go
backend/cmd/gateway/routes_notifications.go
backend/cmd/gateway/routes_platformsettings.go
backend/cmd/gateway/routes_pricing.go
backend/cmd/gateway/routes_proxy_probe.go
backend/cmd/gateway/routes_risk.go
backend/cmd/gateway/routes_siteconfig.go
backend/cmd/gateway/routes_systemhealth.go
backend/cmd/gateway/routes_usageadmin.go
backend/cmd/gateway/routes_userkeycontrols.go
```

前端核验范围：61 个标准 `features/*/api.ts`、9 个 `*Api.ts` 命名变体、`frontend/src/app/router.tsx`、`frontend/src/app/nav.ts`，以及 `frontend/src/auth/`、`features/setup/`、`features/hermes/` 中实际发请求的模块。
