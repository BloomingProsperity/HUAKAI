# Growth-UX 域特性审计

**域说明**: 覆盖用户获取、激活、变现、留存、增长机制全链路——包括注册/邀请/推荐、引导流、定价与试用、用量看板与告警、自助账户设置、出站 webhook、以及面向中国市场的本地化支付与社交登录。审计基于 `backend/` Go 包和 `frontend/` Next.js 应用的真实代码，分类标准：PRESENT（有实现，有 file:line 证据）、PARTIAL（部分实现，标明缺口）、MISSING（完全缺失）。

---

## 特性清单

### A. 用户获取 (Acquisition)

| 特性 | 状态 | 证据 (file:line) | 缺口说明 |
|------|------|-----------------|---------|
| 邮箱/密码注册 | PRESENT | `backend/internal/userauth/service.go`；路由 `POST /v1/auth/register` in `backend/cmd/gateway/routes.go` | — |
| 注册模式开关 (开放/邀请/禁用) | PRESENT | `backend/internal/userauth/types.go`:`RegistrationModeOpen/InviteRequired/Disabled`（≈L42-48） | — |
| 邀请码生成与校验 | PRESENT | `backend/internal/userauth/invite.go`:L14-39；`GenerateInviteCode`/`HashInviteCode`/advisory-lock 并发保护 | 无用户自助生成入口，仅 admin 或 community 模块生成 |
| 邀请码引荐关系记录 (referrals 表) | PARTIAL | `backend/internal/userauth/store.go`:L570 — INSERT INTO referrals；`community/invitation` 包有 referral 记录 | 仅存库，无引荐奖励逻辑、无转化漏斗、无引荐人佣金 |
| 用户可见的推荐/邀请程序 | MISSING | grep `referral_reward\|referrer_commission\|invite_bonus` — 无匹配 | 无推荐奖励、无社交分享链接、无引荐积分/返现 |
| GitHub / Google OAuth 用户注册 | MISSING | `backend/internal/credentialacq/oauth_sso.go` 的 SSO 流仅用于**供应商凭据获取**（Anthropic/ChatGPT/Gemini 等），非用户注册 | 用户注册无 GitHub/Google 社交登录 |
| 微信登录 | MISSING | grep `wechat\|weixin\|wx_login` — 无匹配 | 无中国社交登录，影响国内用户注册转化 |
| 欢迎邮件 (注册后) | MISSING | `backend/internal/email/` 仅有 `SendVerification` + `SendPasswordReset`；grep `SendWelcome\|welcome.*email` — 无匹配 | 注册后无欢迎邮件，激活引导断链 |
| 免费套餐 / 试用期 | MISSING | grep `freemium\|free.tier\|trial.period\|free.quota` — 无匹配 | 无免费配额、无试用区间、无体验升级流 |
| 新用户欢迎积分/充值赠送 | MISSING | grep `welcome.bonus\|signup.credit\|new.user.credit` — 无匹配 | 无新注册自动赠余额机制 |

### B. 用户激活 (Activation)

| 特性 | 状态 | 证据 (file:line) | 缺口说明 |
|------|------|-----------------|---------|
| 邮箱验证流 | PRESENT | `backend/internal/email/`:`SendVerification`；`backend/internal/userauth/` 含 email_token 表操作；路由 `POST /v1/auth/verify-email` | — |
| 密码重置 (邮件令牌) | PRESENT | `backend/internal/email/`:`SendPasswordReset`；`POST /v1/auth/reset-password` | — |
| 引导向导 / 新手 checklist | MISSING | grep `onboard\|wizard\|getting.started` — backend + frontend 均无匹配 | 无步骤式引导流，用户注册后直接面对空白状态 |
| 模型目录 (OpenAI-compat `/v1/models`) | PRESENT | `backend/internal/modelhttp/list_handler.go`；`backend/internal/registry/models_list.go` | — |
| 交互式 API Playground | MISSING | grep `playground\|swagger.ui\|redoc` — 无匹配；frontend `app/chat/page.tsx` 是管理员调试器，非用户自助 playground | — |
| OpenAPI / Swagger UI 文档端点 | MISSING | grep `swagger\|openapi.ui` — 无匹配 | 无公开开发者文档 UI |
| API Key 自助管理 | PRESENT | `backend/internal/userkeyhttp/handlers.go`；路由 `GET/POST/PATCH/DELETE /v1/api-keys` | — |
| API Key 权限 Scope | MISSING | grep `key.scope\|api.key.permission\|KeyScope` — 无匹配 | 所有 key 全权限，无细粒度控制 |
| API Key IP 白名单 | MISSING | grep `ip.whitelist\|key.*allowed.ip` — 无匹配 | — |
| API Key 过期策略/自动轮换 | MISSING | `userkeyhttp` 仅有手动 rotate；无 TTL/expiry 字段 | — |
| SDK / 文档链接端点 | MISSING | grep `sdk.*url\|docs.*endpoint\|documentation.*link` — 无匹配 | 无 /docs、无 SDK 下载引导、无开发者资源中心 |
| 定价页 / 套餐选择 UI | MISSING | `frontend/` 无 pricing 页；backend `GET /v1/pricing/rate-table` 存在但无面向用户的 UI | 用户无法自助查看套餐对比和升级 |

### C. 变现 (Monetization)

| 特性 | 状态 | 证据 (file:line) | 缺口说明 |
|------|------|-----------------|---------|
| 支付订单系统 (通用) | PRESENT | `backend/internal/payment/`、`backend/internal/paymenthttp/`；支持 manual/test/HMAC provider | — |
| 充值码 / Voucher 兑换 | PRESENT | `backend/internal/voucher/`:`types.go`/`service.go`/`store.go`；用户端 `POST /v1/users/me/vouchers`；支持 balance + subscription 两类 grant | 无前端充值码输入框；无批量邮件分发；术语用"voucher"非"充值码"，中文用户认知摩擦 |
| 固定额度优惠券 (Fixed-amount) | PRESENT | `backend/internal/voucher/types.go`:GrantKindBalance | — |
| 折扣/百分比优惠券 | MISSING | voucher 仅支持固定额度 grant，无 percentage-off 类型 | — |
| 订阅套餐 (管理员分配) | PRESENT | `backend/internal/subscription/`；含 DailyCapUSD/WeeklyCapUSD/MonthlyCapUSD caps；`subscriptionhttp/`、`subscriptionenforce/` | 仅管理员分配，用户无法自助购买/升级订阅 |
| 用户自助购买/升级订阅 | MISSING | 无 `POST /v1/me/subscribe` 类用户端购买路由 | — |
| 微信支付 / 支付宝 | MISSING | grep `wechat\|alipay\|wxpay\|weixin` — 无匹配 | 无中国主流支付方式；payment provider 插件架构存在，可接入 |
| 人民币 (CNY) 计价 | MISSING | billing 仅见 USD；无 CNY 汇率换算 | — |
| 用量计费 (按 token 结算) | PRESENT | `backend/internal/billing/`；`backend/internal/settlementrecovery/`；audit ledger + DLQ | — |
| 成本估算 / 价格计算器 UI | PARTIAL | `backend/internal/tokencheck/estimator.go`：后端 token 估算；`GET /v1/pricing/rate-table` 可查价格；frontend 无互动计算器 | 无前端计算器，无"发请求前预估费用"功能 |
| 按用量自动扣费 (预付余额) | PRESENT | `backend/internal/balancehold/`；`backend/internal/quota/`：quota 执行 + hold 机制 | — |
| 消费限额 (用户自设预算) | MISSING | 订阅 cap 是管理员设的套餐属性；无用户自设 spend-limit；grep `user.budget\|per.user.budget` — 无匹配 | — |

### D. 留存 (Retention)

| 特性 | 状态 | 证据 (file:line) | 缺口说明 |
|------|------|-----------------|---------|
| 用户用量看板 (User-facing) | PARTIAL | `GET /v1/me/usage` 存在 (`backend/internal/meusagehttp/`)；frontend 无对应用户用量页，Sidebar "Usage" 项为 disabled 状态 | 有 API 无 UI；无图表、无趋势、无历史 |
| 按天/周/月用量聚合 | MISSING | `meusagehttp` 返回 billing 记录列表，无时间维度聚合 API | — |
| 按模型用量拆分 | MISSING | grep `per.model.usage\|model.*breakdown` — 无匹配 | — |
| Token 用量拆分 (input/output) | MISSING | billing event 含 cost，未见 token 计数拆分暴露给用户 | — |
| API 调用历史 (用户可见) | MISSING | audit ledger 面向管理员；无 `GET /v1/me/calls` 类端点 | — |
| 信任链/成本收据 (用户验证) | PRESENT | `backend/internal/trustreceipt/`；`backend/internal/trusthttp/`；前端 `app/audit/page.tsx` — Merkle 证明、hop chain | HUAKAI 差异化核心，已实现 |
| 用量配额告警 (接近上限提醒) | MISSING | grep `quota.alert\|quota.warning\|approaching.limit` — 无匹配；订阅执行是 hard-stop，无提前警告 | — |
| 消费阈值告警邮件 | MISSING | grep `spend.alert\|budget.alert\|threshold.notify` — 无匹配 | — |
| 订阅到期提醒邮件 | PARTIAL | `backend/internal/subscription/reminder.go`(？) 文件存在，但 grep `SendSubscriptionReminder\|reminder.*email` — email 包无对应发送调用 | 逻辑框架存在，email 发送未接通 |
| 账单确认邮件 | MISSING | `email/` 仅有 verify + reset；无 receipt email | — |
| 站内通知中心 (In-app notification bell) | MISSING | frontend grep `notification\|alert.*bell\|inbox` — 无匹配 | — |
| 邮件通知偏好设置 | MISSING | grep `notification.*pref\|email.*pref\|unsubscribe\|email_opt` — 无匹配 | — |
| 站外推送 (Push/Telegram/Slack) | MISSING | 无 webhook outbound 用户侧；无 push notification 集成 | — |

### E. 账户自助 (Self-Service Settings)

| 特性 | 状态 | 证据 (file:line) | 缺口说明 |
|------|------|-----------------|---------|
| 查看自身账户信息 | PRESENT | `GET /v1/auth/me` — 返回当前用户详情 | — |
| 修改用户资料 (昵称/头像等) | MISSING | grep `UpdateUserProfile\|PATCH /v1/me\|PUT /v1/auth/me` — 无匹配；`db/hermes/hermes.sql.go:L848` 的 UpdateProfile 是 Hermes API 配置文件非用户资料 | — |
| 已登录用户修改密码 | MISSING | 仅有 `reset-password`（邮件令牌流）；无 `PATCH /v1/me/password`（旧密码验证后改新密码） | — |
| 账户注销 / 数据删除 (GDPR) | PARTIAL | `backend/internal/userauth/` 含 `status=deleted` 软删；`backend/internal/rate/rate.go` + `gatewayhttp/admin_credentials_handler.go` 有 deactivate；无用户自助注销端点 | 无 `DELETE /v1/me` 用户端路由；无数据导出接口 |
| API Key 使用日志 (用户可见) | MISSING | grep `key.activity\|key.usage.log\|key.*history` — 无匹配 | — |
| 用户隐私设置 | MISSING | `backend/internal/privacy/` 存在但关注数据脱敏/审计，非用户隐私偏好控制 | — |

### F. 增长机制 (Growth Mechanics)

| 特性 | 状态 | 证据 (file:line) | 缺口说明 |
|------|------|-----------------|---------|
| 用户侧 Outbound Webhook (事件订阅) | MISSING | `backend/internal/paymenthttp/provider_hmac.go` 是入站支付 webhook；grep `UserWebhook\|user.*webhook.*outbound` — 无匹配 | 用户无法订阅 API 事件通知 |
| 积分/成就/游戏化 | MISSING | grep `points\|gamif\|badge\|achievement` — 无匹配 | — |
| 排行榜 | MISSING | grep `leaderboard\|top.*user\|ranking.*user` — 无匹配 | — |
| 多语言 / i18n | MISSING | grep `i18n\|localize\|zh.CN` — 仅 `error_normalize_test.go` 等无关文件；frontend 无 next-intl/i18next | 产品全英文/中文混合，无系统化语言切换 |
| 团队/组织账户 | MISSING | grep `team\|organization\|workspace` — 匹配文件均为路由/pool管理，无 team/org 实体 | — |
| SSO / SAML 企业登录 | MISSING | `credentialacq/oauth_sso.go` 是供应商 SSO（获取供应商 OAuth token）非企业用户 SSO | — |
| 限时闪购 / 时效优惠 | MISSING | voucher 无 time-limited flash-sale 类型；无 campaign 管理 | — |
| 速率限制友好提示 (Retry-After UX) | PARTIAL | `backend/internal/quota/` 含 quota 执行；`clienterr/catalog.go` 含用户可读错误码；`gateway/error_normalize.go` 含 HTTP 头标准化；quota 仅 hard-stop，无"X 分钟后重试/配额将于 HH:MM 重置"提示 | — |
| 用户侧费率/模型对比工具 | MISSING | `GET /v1/pricing/rate-table` 有数据；frontend 无对比 UI | — |

---

## Top Missing，按商业价值排序

| 排名 | 缺失特性 | 商业价值说明 |
|------|---------|------------|
| 1 | **用量配额告警 + 消费阈值邮件** | 防止用户因余额/配额耗尽无感知而流失；sub2api/new-api 均有；直接影响续费率 |
| 2 | **用户自助购买/升级订阅 + 定价页 UI** | 当前订阅只能管理员分配，阻断自助变现漏斗；商业化的核心瓶颈 |
| 3 | **微信支付 / 支付宝** | 中国主要用户群的支付首选；缺失直接导致国内付费转化为零 |
| 4 | **用量看板 UI (图表 + 历史 + 模型拆分)** | `/v1/me/usage` API 已有，缺前端；用户无法感知消费趋势，是流失高风险因素 |
| 5 | **欢迎邮件 + 引导向导** | 注册后无引导，首次体验断链；新用户留存的第一道门 |
| 6 | **免费套餐 / 试用期** | 无 try-before-buy 路径，付费转化完全依赖管理员开账号；阻断自助增长 |
| 7 | **推荐奖励系统 (Referral Rewards)** | referrals 表已建，引荐关系已记录，但无奖励发放；补全即可驱动病毒增长 |
| 8 | **用户自助修改密码 / 资料编辑** | 基础账户体验缺失；用户满意度直接影响留存 |
| 9 | **GitHub / Google 社交登录 (用户注册)** | 当前 OAuth 仅用于供应商凭据获取；社交登录降低注册摩擦，提升转化 |
| 10 | **用户侧 Outbound Webhook** | 企业/开发者集成场景必需；无此功能难以支撑 API-first 用户的自动化工作流 |
| 11 | **API Key 权限 Scope + 过期策略** | 安全合规要求；缺失导致企业客户无法满足最小权限原则 |
| 12 | **订阅到期提醒邮件 (接通现有框架)** | `reminder.go` 骨架已有，email 发送未接通；低成本高价值补全 |
| 13 | **账户注销 / 数据导出 (GDPR)** | 欧盟用户合规必需；影响出海合规风险 |
| 14 | **交互式 API Playground / Swagger UI** | 降低开发者上手门槛，直接影响技术用户激活率 |
| 15 | **多语言 (i18n) / 中文本地化** | 充值码/套餐等术语中英文不统一；系统化 i18n 是国际化/本地化基础 |
