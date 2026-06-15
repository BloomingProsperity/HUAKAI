<!-- 前端接线测试覆盖矩阵 · Owner 指令"后端每个功能模块都要前端接线测试" · 2026-06-15 -->
<!-- 权威源: backend/cmd/gateway/routes*.go 的 Mount*Routes / r.Route 分组 -->

# 前端接线测试覆盖矩阵

> **目标(Owner)**:后端有的**每一个功能模块**,都要有①前端接线(页面/lib 真调它)+ ②接线测试(`frontend_wiring_test.go` 对真后端断言它解析的字段)。
>
> **状态图例**:✅ 接线+测试齐全 · 🟡 部分(有接线无测试 / 有页面是 mock)· ❌ 未接线 · ⬜ N/A(webhook/internal,无前端)
>
> **A-R 闭环完成(2026-06-15 终版)**:**25 个功能模块已接线 + 接线测试(`frontend_wiring_test.go` 26 子测试全 PASS,hermes 因最小 dev 装配未挂载诚实 Skip)**,34 静态页 next build 绿,分 5 批 + 收尾落 `feat/frontend-portal`。
>
> **覆盖面**:用户门户 14 模块(登录/概览/Playground/调试台/API Keys/用量/账户/安全/会话/充值/兑换/订阅/通知/审计回执/定价)全闭;admin 控制台 9 面(运营总览/用户/账号池/渠道健康/凭证代理/运营管理/平台设置/审核系统/Hermes)+ context-aware 外壳导航。测试环境 seed **四轨凭证**:session + hk_ key + tenant_operator + platform_admin。
>
> **诚实未尽(非阻塞,排后续)**:Hermes 需 hermesService+runner 才挂载(前端页已建用核对契约);部分 admin 深度表单为列表+核心动作(复杂多步表单精简);推理 Rerank tab 留位;sessions「撤销全部」遍历实现(后端无单端点)。

## A. 用户门户(portal)

| # | 模块 | 后端 Mount / 端点 | 鉴权 | 接线 | 测试 | 批次 |
|---|---|---|---|---|---|---|
| 1 | 鉴权(注册/登录/me/登出) | MountAuthRoutes·MountLoginRoutes·MountAuthMeRoutes | session | ✅ | ✅ | 0 |
| 2 | 会话(刷新/列表/撤销) | MountSessionProtectedRoutes /v1/sessions | session | 🟡(仅刷新) | 🟡 | 1 |
| 3 | API Keys(CRUD+用量摘要) | MountUserAPIKeyRoutes /v1/api-keys | session | ✅ | ✅ | 0 |
| 4 | 用量/额度/分析/导出 | /v1/me/usage·quota·analytics·export.csv | mix | ✅ | ✅ | 0 |
| 5 | 推理-聊天+模型列表 | /v1/chat/completions·/v1/models | apikey | ✅ | ✅ | 0 |
| 6 | 概览(用户总览落地页) | (聚合 quota+usage+keys) | session/apikey | ❌ | ❌ | **1** |
| 7 | 分组(可用分组) | MountUserRoutes /v1/me/groups | session | ❌ | ❌ | **1** |
| 8 | 邀请/推荐/签到 | /v1/me/invitations·referrals·checkin·invitation-code | session | ❌ | ❌ | **1** |
| 9 | 兑换码(redeem+历史) | MountVoucherUserRoutes /v1/users/me/vouchers | session | ❌ | ❌ | **1** |
| 10 | 订阅(列表/进度/购买) | MountSubscriptionUserRoutes /v1/users/me/subscriptions | session | ❌ | ❌ | **1** |
| 11 | 余额/充值/订单 | MountPaymentUserRoutes·MountBalanceCreditRoutes /v1/users/me/payments | session | ❌ | ❌ | 2(跳真支付SDK) |
| 12 | 通知(收件箱/设置) | MountNotifyUserRoutes /v1/notifications | session | ❌ | ❌ | **1** |
| 13 | 公告 | /v1/announcements | session | ❌ | ❌ | **1** |
| 14 | 2FA(设置/启停/状态) | MountTwoFARoutes /v1/auth/2fa | session | ❌ | ❌ | 2 |
| 15 | Passkey(注册/登录/列表) | passkey /v1/me/passkeys·/v1/auth/passkey | mix | ❌ | ❌ | 2 |
| 16 | OAuth 绑定(列表/解绑) | MountOAuthBindingsRoutes /v1/users/me/oauth-bindings | session | ❌ | ❌ | 2 |
| 17 | 回执/争议/信任/审计 | /v1/receipts·/v1/me/disputes·/v1/trust·/v1/audit | mix | 🟡(audit页mock) | ❌ | 2 |
| 18 | 定价(页/费率表/快照) | MountPricingRatioRoutes·routes_pricing /v1/pricing | public | ❌ | ❌ | 2 |
| 19 | 站点配置/排行榜 | routes_siteconfig·/v1/public/rankings | public | 🟡(login用) | ❌ | 1 |
| 20 | 推理-其余协议 | messages·responses·embeddings·rerank·audio·images·realtime·completions·generation | apikey | 🟡(messages) | ❌ | 3 |

## B. 管理控制台(admin)

| # | 模块 | 后端 Mount / 端点 | 接线 | 测试 | 批次 |
|---|---|---|---|---|---|
| 21 | 用户管理 | MountAdminRoutes /admin/v1/users | 🟡(thin) | ❌ | 3 |
| 22 | 账号池(CRUD+批量+健康+测试+模型) | MountAdminPoolAccountRoutes·ProviderAccount* /v1/admin/provider-accounts | 🟡(/accounts) | ❌ | 3 |
| 23 | 池组 pools | /admin/v1/pools | ❌ | ❌ | 4 |
| 24 | 凭证(导入/续期/获取流) | MountAdminCredential*Routes /admin/v1/credentials | ❌ | ❌ | 4 |
| 25 | 代理 proxies | /admin/v1/proxies | ❌ | ❌ | 4 |
| 26 | 渠道/渠道健康 | MountChannelHealth*Routes /v1/admin/channel-health | 🟡(observability) | ❌ | 3 |
| 27 | 计费设置/余额信用 | MountAdminBillingSettingsRoutes /admin/v1/billing | ❌ | ❌ | 4 |
| 28 | 管理用量分析 | routes_usageadmin /v1/admin/usage | ❌ | ❌ | 3 |
| 29 | 定价比率/缓存覆盖 | MountPricingRatioRoutes·MountCacheOverrideAdminRoutes | ❌ | ❌ | 4 |
| 30 | 模型/模型同步 | MountModelSyncRoutes /admin/v1/model-sync | ❌ | ❌ | 4 |
| 31 | 平台设置 | MountPlatformSettingsRoutes /v1/admin/platform-settings | ❌ | ❌ | 3 |
| 32 | 邮件设置 | MountAdminEmailSettingsRoutes /v1/admin/email | ❌ | ❌ | 4 |
| 33 | 路由表 | MountRouteAdminRoutes /v1/admin/routes | ❌ | ❌ | 4 |
| 34 | TLS 指纹 | MountTLSFPAdminRoutes /v1/admin/tls-fingerprint-profiles | ❌ | ❌ | 4 |
| 35 | 告警 | routes_alerting | ❌ | ❌ | 3 |
| 36 | 内容审核 | MountModerationAdminRoutes /admin/v1/moderation | ❌ | ❌ | 4 |
| 37 | 管理-推荐/兑换/订阅/支付 | MountReferral·Voucher·Subscription·PaymentAdminRoutes | ❌ | ❌ | 3 |
| 38 | 系统(版本/日志级别/模块/健康) | MountVersion·LogLevel·routes_modules·routes_systemhealth | 🟡(部分) | ❌ | 4 |

## C. N/A(无前端,设计如此)
- 支付 webhooks(MountPaymentWebhookRoutes·MountWebhookRoutes)— 外部回调,⬜
- internal HMAC 控制面(/internal/*,私网门)— runner 用,⬜
- Hermes 运维助手(/v1/hermes)— 操作员/HMAC,前端按需,排末

## 推进批次(执行记录)
- ✅ **批次 0**(核心环):登录/API Keys/用量/Playground — 15ae1cbd / 7865d6ed(后并入分支)
- ✅ **批次 1**(用户门户):兑换/订阅/通知公告/账户(分组/邀请/签到/推荐)— 09611764,接线测试 11 子测试
- ✅ **批次 2**(用户深度):充值余额/账户安全(2FA+Passkey+OAuth)/定价/审计回执护城河 — 4fc57d6a,15 子测试
- ✅ **批次 3**(admin 核心):用户管理/账号池/渠道健康/运营总览 — 74ec06f2,19 子测试(引入 tenant_operator+platform_admin 双管理 token)
- ✅ **批次 4**(admin 深度):凭证代理/平台设置/运营管理/审核系统 — c77ae40a,23 子测试(assertWired 容忍角色/租户细差)
- ✅ **admin 外壳**:Sidebar context-aware 导航树(/admin/* 切 admin 导航)— c33f5f90
- ✅ **收尾批次**:用户概览(/dashboard 重建)/会话管理/Hermes助手/多协议调试台 — 685e365c,26 子测试(hermes Skip)

每批已执行:前端页/lib 接真端点 → 扩 `frontend_wiring_test.go` 断言 → tsc+build+接线测试绿 → 提交推送分支。
布局总纲见 `FUSION-LAYOUT-PLAN-v3.md`。
