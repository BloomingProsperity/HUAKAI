# HUAKAI 项目标杆功能树 (BENCHMARK · 2026-06-06)

> 这是 HUAKAI 唯一权威功能树标杆。以后所有开发**按本树逐个推进**:挑一行 → 看 `推进动作` → 建 → PM 全门(build+vet+unit+integration_pg -p1 + mutation 自证)→ 把该行 `HUAKAI状态` 升级 + 标 commit。**禁止虚标**:后端有路由但无前端 ≠ 已完成。

## 基线 (HEAD, 2026-06-06)
- HUAKAI: `origin/fix/hermes-phase-1-e33d940 @ e89d7fce`
- 参照: sub2api `origin/main@635ad81` · new-api `origin/main@adc390c` · CLIProxyAPI `origin/main@3abfc83`

## 三源合并(本树由此三者校准而成)
1. **大功能树骨架** — `docs/process/feature-tree/`(16 模块 + 9 加权轴),提供模块结构。
2. **字段级细树** — 我 8-agent 真码枚举的 2784 行(每请求参数/配置字段/CIDR/定价维度)。
3. **第三方 151 树** — `151-granular-feature-tree-20260606/`(28 文件),提供**六级状态词 + git 行证据 + 12D 优先级 + 禁虚标(12E)**。

## 状态图例(六级 · 禁虚标)
- `已完成` — 后端代码+路由+服务层/测试齐,且(产品功能)前端闭环或不需要前端。
- `部分完成` — 有框架/后端,但缺真实 provider / 缺完整生命周期 / 只覆盖测试或手动路径。
- `后端有·前端缺` — 后端路由确有(带证据),但无用户/后台前端页面 → **不算产品完成**。
- `缺失` — 参照有明确功能,HUAKAI 无等价。
- `未做` — 仅 TODO/mock/占位/文档承认未接入。
- `未合并main` — fix 分支有,默认 `origin/main` 无(main 仅 83 端点,fix 183)。

参照列 `✓/✗`:✗ 多为该项目 by-design 范围外(尤其 CLIProxyAPI = 单租户 CLI 代理,无用户/计费体系)。

## 优先级
- `P0` — 直接卡商业闭环(用户/后台前端、真实 PSP、provider-instance、订阅/返佣前端)。
- `P1` — 补齐后才像成熟后台(后台 CRUD、财务导出、对账、UI 完善)。
- `P2` — 对标 CLIProxyAPI/运维生态(CLI/TUI、auth-file、Amp、realtime、SDK)。
- `P3` / `—` — 增强项 / 已完成项。

## 三条真断层(headline · 来自 12D)
1. **前端闭环断层** — 后端一大批,前端只有 9 个运维页;login/register/wallet/payment/orders/subscription/affiliate/checkin/profile/api-keys-UI 全缺。
2. **真实 PSP + 支付 provider-instance 断层** — 仅 manual/taobao/hmac/test 框架 + Provider interface;Stripe/支付宝/微信/EasyPay/Airwallex `PROVIDERS.md` 明写"暂不实现";instance CRUD/负载/健康/密钥/对账全缺。
3. **CLI/API 代理生态断层** — /v1/completions、count_tokens、Gemini原生、realtime-WS、auth-file 导入导出、TUI、Amp、管理API、Go SDK 全缺。

## 总账(2704 行 · 约数)
| HUAKAI状态 | 数量 |
|---|---|
| 已完成 | ~1335 |
| 部分完成 | ~399 |
| 后端有·前端缺 | ~186 |
| 缺失 | ~497 |
| 未做 | ~29 |
| 未合并main | ~35 |

| 优先级 | 数量 |
|---|---|
| P0 | ~175 |
| P1 | ~288 |
| P2 | ~386 |
| P3 | ~380 |

## 模块目录 (TOC)
- A 用户与权限/认证/会话
- B 网关核心/模型接入/协议转换
- C 上游账号/凭证/账号池
- D 路由调度/渠道健康/限流
- E 用量/计费/配额/定价
- F 支付/订阅/voucher/签到/返佣
- G 审计/可观测 + 安全/隐私/反封禁
- H API-key/通知/后台/前端/部署/Rust/i18n

---


# ====================  模块 A  ====================
# 标杆 · 用户与权限/认证/会话

> 单模块标杆功能树。三源合并去重: (1) 大功能树骨架 `feature-tree/accounts-auth.md`,(2) 字段级细树 `feature-audit/01-auth-fine.md`,(3) 151 第三方对标 `151-ref/`(12A/12B/12C 上架树 + 12D 优先树 + 12E/06 证据)。
>
> HUAKAI 云端基线: `origin/fix/hermes-phase-1-e33d940@e89d7fce`(183 OpenAPI paths)。**`origin/main` 仅 83 paths,落后 fix 分支** → 部分 fix 分支独有能力标 `未合并main`。
>
> **HUAKAI状态 六级(禁止虚标)**: `已完成`(后端+前端闭环 或 无需 UI 的内部安全原语已就绪并测过) / `部分完成`(后端全+前端部分,或后端有缺高级动作) / `后端有·前端缺`(路由存在但无前端页面——12E §2/§3 证实无 login/register/profile/security/sessions/api-keys 页,Sidebar 相关项 disabled) / `缺失`(参考有、HUAKAI 后端也无) / `未做`(从未实现,无设计) / `未合并main`(fix 分支有,默认分支 main 尚未合并)。
> 校准证据: 12E §7/§8(auth/2FA/passkey/key 路由后端存在) + 12E §2/§3(前端页面树无、Sidebar disabled) + 06 §G/§H(端点状态) + 12D P0-U 行(前端缺=P0)。
>
> sub2api/new-api/cliproxy 列: ✓=该参考已上架该功能, ✗=无。cliproxy 为单租户 CLI 代理、无平台用户账户,平台用户类几乎全 ✗(by-design)。

---

## A. 注册与开关 (Registration)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-001 | 注册总开关 (master enable) | ✓ | ✓ | ✗ | 已完成 | — | `platformsettings` KeyRegistrationEnabled 默认 "false"(types.go:23) | — 后端开关就绪;前端注册页见 AUTH-006 |
| AUTH-002 | 三态注册模式枚举 open/invite_required/disabled | 🟡(仅 invite bool) | 🟡(分散 bool) | ✗ | 已完成 | — | `userauth/types.go:46` RegistrationMode;legacy admin_only→disabled(types.go:57) | — |
| AUTH-003 | 默认 deny-by-default (默认 invite_required) | open-ish | open | ✗ | 已完成 | — | KeyInvitationRequired 默认 "true"(types.go:24,63) | — 强于两参考 |
| AUTH-004 | 密码注册子开关 (区分社交) | 🟡 | ✓(PasswordRegisterEnabled) | ✗ | 部分完成 | P3 | 折叠进 RegistrationMode | 拆出独立 password-register 开关以对齐 new-api 粒度 |
| AUTH-005 | 密码登录子开关 | ✓(BackendModeAuthGuard) | ✓(PasswordLoginEnabled) | ✗ | 部分完成 | P3 | 经 registration/login gate 间接控制 | 暴露独立 password-login 开关 |
| AUTH-006 | 注册页 (前端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端 `POST /v1/auth/register`(auth_handler.go:148);12E §2 无 register 页 | 新建 `frontend/app/register/page.tsx` 接 register API |
| AUTH-007 | 邮箱域名 allowlist 开关 | ✓ | ✓(EmailDomainRestrictionEnabled) | ✗ | 部分完成 | P2 | platformsettings/email pkg(无硬编码域列表) | 补可配置域名白名单(对齐 new-api 9 域硬列表语义) |
| AUTH-008 | 邮箱域名白名单内容 | ✓ | ✓(gmail/163/qq/outlook… 9 域) | ✗ | 缺失 | P2 | HUAKAI 非硬编码 9 域列表 | 提供管理端可编辑域名白名单 |
| AUTH-009 | 邮箱 +tag 别名限制开关 | 🟡 | ✓(EmailAliasRestrictionEnabled) | ✗ | 缺失 | P3 | new-api constants.go:95 | 增加 alias 规范化/拒绝开关 |
| AUTH-010 | 保留/封禁邮箱策略 | ✓(isReservedEmail) | 🟡 | ✗ | 部分完成 | P3 | email/ pkg | 补 reserved-email 显式策略表 |
| AUTH-011 | 重复邮箱守卫 | ✓(ErrEmailExists) | ✓(CheckUserExistOrDeleted) | ✗ | 已完成 | — | users unique + ErrDuplicateUser;含复活守卫 | — |
| AUTH-012 | 新用户默认配额 | ✓ | ✓(QuotaForNewUser) | ✗ | 已完成 | — | CreateUserParams + quota pkg | — |
| AUTH-013 | 注册默认角色 (永不自动 admin) | ✓ | ✓(Role 默认 Common) | ✗ | 已完成 | — | users.role 默认 'user'(migration 0076) | — |
| AUTH-014 | 管理员创建用户 (后端) | ✓ | ✓(controller CreateUser) | ✗ | 后端有·前端缺 | P1 | admin/ + userauth CreateUser;但无 `/admin/v1/users` 列表/管理路由(骨架 #45) | 补 admin 用户 CRUD 路由 + 后台用户管理页 |

## B. users 表 / 用户结构字段 (User model)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-015 | 核心字段 id/username/email/display_name | ✓ | ✓ | ✗ | 已完成 | — | users(0007/0020) | — |
| AUTH-016 | password_hash (nullable, argon2id) | ✓(bcrypt) | ✓(bcrypt) | 🟡(mgmt SecretKey) | 已完成 | — | `password_hash text` 0020;argon2id | — |
| AUTH-017 | role 字段 (text CHECK admin/user) | ✓(string) | ✓(int 0/1/10/100) | ✗ | 已完成 | — | role text CHECK(0076) — 二元 panel 角色 | — |
| AUTH-018 | status 6 态枚举 | ✓(string) | ✓(int) | ✗ | 已完成 | — | CHECK(pending_verification,active,disabled,locked,reset_required,deleted) 0020 | — |
| AUTH-019 | email_verified 标志 | 🟡 | 🟡 | ✗ | 已完成 | — | email_verified bool NOT NULL DEFAULT false(0020) | — |
| AUTH-020 | password_version (会话失效计数器) | ✗ | ✗ | ✗ | 已完成 | — | password_version int DEFAULT 1 CHECK>=1(0020);重置时 bump | — HUAKAI 独有 |
| AUTH-021 | failed_login_count | ✗ | 🟡(2FA 级) | ✗ | 已完成 | — | failed_login_count CHECK>=0(0020) | — |
| AUTH-022 | locked_until | ✗ | 🟡(2FA) | ✗ | 已完成 | — | locked_until timestamptz(0020) | — |
| AUTH-023 | invite_code_used (hashed) | 🟡 | 🟡(InviterId) | ✗ | 已完成 | — | invite_code_used = sha256/base64url,原文不存(0020) | — |
| AUTH-024 | social_login_provider | 🟡 | 🟡(分列) | ✗ | 已完成 | — | social_login_provider text(0020) | — |
| AUTH-025 | last_login_at / MarkLoginSuccess | ✓ | ✓(LastLoginAt) | ✗ | 已完成 | — | MarkLoginSuccess | — |
| AUTH-026 | 软删除 (status='deleted') | 🟡 | ✓(gorm.DeletedAt) | ✗ | 已完成 | — | status='deleted' 软删 | — |
| AUTH-027 | 用户资料自助更新 (display_name/email) | ✓(PUT /api/user) | ✓(PUT /api/user/self) | ✗ | 缺失 | P1 | 无 PUT/PATCH /v1/users/me;userauth 无 UpdateProfile(骨架 #10) | 增 `PUT /v1/users/me` + UpdateProfile + 资料页 |
| AUTH-028 | 账户自助删除 (GDPR) | ✓ | ✓ | ✗ | 缺失 | P1 | 无 DELETE /v1/users/me;无 DeleteSelf(骨架 #11) | 增账户删除路由 + 软删流程(GDPR Art.17) |
| AUTH-029 | aff_code/aff_count/aff_quota 联盟字段 | ✓ | ✓(5 字段) | ✗ | 部分完成 | P2 | 仅 referral(referral_reward.go) | 补完整 affiliate 字段族 + 提现 |
| AUTH-030 | remark/notes 管理员备注 | ✓ | ✓(Remark) | ✗ | 部分完成 | P3 | 🟡 | 补 admin 备注字段 |
| AUTH-031 | group/allowed_groups | ✓ | ✓(default) | ✗ | 部分完成 | P2 | group pkg(分离) | 对齐用户级 group 绑定 |

## C. 邮箱验证 (Email verification)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-032 | 登录前必须验证开关 | ✓(IsEmailVerifyEnabled) | ✓(EmailVerificationEnabled) | ✗ | 已完成 | — | RequireVerified + ErrEmailUnverified | — |
| AUTH-033 | 验证 token 表 (token_hash bytea) | 🟡 | 🟡 | ✗ | 已完成 | — | email_verification_tokens(0020) | — |
| AUTH-034 | token 哈希存储 (sha256, 原文不存) | 🟡 | 🟡 | ✗ | 已完成 | — | HashToken sha256(email_verify.go) | — |
| AUTH-035 | active-token 部分索引 (consumed_at IS NULL) | ✗ | ✗ | ✗ | 已完成 | — | idx_email_verification_user_active(0020) | — HUAKAI 独有 |
| AUTH-036 | 验证 TTL (24h) | ✓ | ✓ | ✗ | 已完成 | — | DefaultEmailVerificationTTL=24h(email_verify.go:15) | — |
| AUTH-037 | 发码限流 (专用) | ✓(5/min) | ✓(redis+mem per-IP) | ✗ | 部分完成 | P2 | 经 global/loginthrottle 覆盖,无专用 EV 限流器 | 增专用 send-verify-code 限流器 |
| AUTH-038 | consumed_at 一次性守卫 | 🟡 | 🟡 | ✗ | 已完成 | — | ConsumeEmailVerificationToken | — |
| AUTH-039 | 验证邮件投递 | ✓ | ✓ | ✗ | 已完成 | — | AuthEmailSender.SendVerification | — |
| AUTH-040 | 邮箱验证页 (前端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端 `POST /v1/auth/verify-email`(auth_handler.go:151);P0-U-004 | 新建邮箱验证落地页 |

## D. 密码哈希 (Password hashing)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-041 | 算法 argon2id (强于参考 bcrypt) | bcrypt | bcrypt | bcrypt | 已完成 | — | argon2.IDKey(password.go:41) | — 强于三参考 |
| AUTH-042 | argon2 参数 (m=64MiB,t=3,p=1,salt16,key32) | n/a | n/a | n/a | 已完成 | — | password.go:24 等 | — |
| AUTH-043 | 哈希头格式 $argon2id$v=19$… | n/a | n/a | n/a | 已完成 | — | password.go:42 | — |
| AUTH-044 | 哈希篡改守卫 (拒 m=0/t=0/p=0 + 上界) | ✗ | ✗ | ✗ | 已完成 | — | parsePasswordHash:m≤1GiB,t≤100,p≤255(password.go:97-106) — 无 fail-open | — HUAKAI 独有 |
| AUTH-045 | 常量时间比较 | ✓ | ✓ | ✓ | 已完成 | — | subtle.ConstantTimeCompare(password.go:57) | — |
| AUTH-046 | 用户枚举时序均衡 (dummy argon2) | ✗ | ✗ | ✗ | 已完成 | — | equalizeLoginWork(service.go:23,185) | — HUAKAI 独有 |
| AUTH-047 | 密码复杂度策略 (长度/字符类) | 🟡 | 🟡 | ✗ | 已完成 | — | PasswordPolicy + DefaultPasswordPolicy(service.go:47) | — |

## E. 密码重置与修改 (Password reset & change)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-048 | 忘记/重置密码端点 (非鉴权) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | RequestPasswordReset/ResetPassword;`POST /v1/auth/reset-password`(auth_handler.go:152);P0-U-003 | 补忘记/重置密码前端页 |
| AUTH-049 | 重置 token 表 + 绑 password_version | ✗ | ✗ | ✗ | 已完成 | — | password_reset_tokens.password_version(0020) — 旧 token 失效 | — HUAKAI 独有 |
| AUTH-050 | 重置 token TTL (30m) | ✓ | ✓ | ✗ | 已完成 | — | DefaultPasswordResetTTL=30m(email_verify.go:16) | — |
| AUTH-051 | 重置 token 哈希 + 唯一索引 | 🟡 | 🟡 | ✗ | 已完成 | — | token_hash bytea + uq_password_reset_token_hash(0020) | — |
| AUTH-052 | 已登录改密 (校验旧密码) | ✓(user_service) | ✓(UpdateSelf:635) | ✗ | 部分完成 | P1 | 控制流存在并 bump password_version;但无独立 `POST /v1/users/me/password` + 无前端 | 暴露独立改密路由 + 安全设置页改密入口 |
| AUTH-053 | 重置后会话全失效 | ✓ | 🟡 | ✗ | 已完成 | — | ConsumePasswordResetToken bump password_version → 全 session/refresh 死 | — |

## F. OAuth / 社交登录 提供方 (OAuth providers)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-054 | Google OAuth (enable/client/secret/scope/redirect) | ✓ | 🟡 | ✗ | 已完成 | — | KeyOAuthProvidersEnabled;oauth_flow.go | — |
| AUTH-055 | Google ID-token JWKS 验签 | 🟡 | ✗ | ✗ | 已完成 | — | verifyGoogleIDToken+fetchRSAKey(oauth_flow.go:284) — 强于参考 | — |
| AUTH-056 | GitHub OAuth | ✓ | ✓(默认关) | ✗ | 已完成 | — | githubIdentity(oauth_flow.go:385) | — |
| AUTH-057 | DingTalk 登录 | ✓ | ✗ | ✗ | 已完成 | — | dingTalkIdentity(oauth_social_provider_flows.go:216) | — |
| AUTH-058 | DingTalk corp 限制/属性同步 (bypass-reg/sync-attr) | ✓(corp policy + 6 sync 字段 + attr-matching) | ✗ | ✗ | 缺失 | P2 | HUAKAI 仅登录,无企业属性同步 | 补 DingTalk corp-restriction + 属性同步(企业向) |
| AUTH-059 | LinuxDo 登录 | ✓ | ✓ | ✗ | 已完成 | — | SocialProviderLinuxDo(social_login.go:17) | — |
| AUTH-060 | LinuxDo 最低 trust-level 门 | 🟡 | ✓(LinuxDOMinimumTrustLevel) | ✗ | 缺失 | P2 | HUAKAI 通用 userinfo,无 trust 门 | 补 LinuxDo trust-level 校验 |
| AUTH-061 | OIDC 提供方 (issuer/client/secret/scopes/claim-map) | ✓ | ✓ | ✗ | 已完成 | — | oidc_provider_configs(0081);client_secret_cipher 加密 | — |
| AUTH-062 | OIDC per-tenant slug 唯一 + enabled | ✗ | 🟡 | ✗ | 已完成 | — | UNIQUE(tenant_id,slug)(0081) | — |
| AUTH-063 | OIDC ValidateIDToken / RequireEmailVerified 开关 | ✓ | 🟡 | ✗ | 缺失 | P2 | HUAKAI 未暴露这两个 per-provider bool | 补 OIDC id-token 校验/邮箱验证要求开关 |
| AUTH-064 | QQ 登录 | ✗ | ✗ | ✗ | 已完成 | — | SocialProviderQQ(oauth_social_provider_flows.go:106) | — HUAKAI 独有 |
| AUTH-065 | NodeSeek 登录 | ✗ | ✗ | ✗ | 已完成 | — | SocialProviderNodeSeek(social_login.go:16) | — HUAKAI 独有 |
| AUTH-066 | WeChat 登录 (5 app secret 变体) | ✓ | ✓ | ✗ | 部分完成 | P2 | SocialProviderWeChat 声明,仅通用 exchange 路径(social_login.go:14) | 补 WeChat MP/Open/Mobile 多变体 + QR 扫码 |
| AUTH-067 | Discord 登录 | ✗ | ✓(DiscordSettings) | ✗ | 未做 | P2 | 无 SocialProviderDiscord(细树 §6 确认) | 新增 Discord OAuth 提供方 |
| AUTH-068 | Telegram 登录 (bot token + HMAC 校验) | ✗ | ✓(checkTelegramAuthorization) | ✗ | 未做 | P2 | 无 Telegram(细树 §6 确认) | 新增 Telegram login widget + HMAC 校验 |
| AUTH-069 | 自定义/任意 OAuth 提供方 DB 注册表 | ✗ | ✓(CustomOAuthProvider 全结构) | ✗ | 部分完成 | P1 | OAuthHTTPProvider 配置驱动;oidc_provider_configs 仅 OIDC 形 | 建管理端通用 OAuth provider CRUD(AI-016) |
| AUTH-070 | 自定义 provider AuthStyle 枚举 (auto/params/header) | ✗ | ✓ | ✗ | 缺失 | P2 | oidc_provider_configs 无 auth-style 字段 | 增 AuthStyle 字段 |
| AUTH-071 | 自定义 provider AccessPolicy + 拒绝消息 | ✗ | ✓ | ✗ | 未做 | P2 | HUAKAI 无 claim-based 访问策略 | 增 AccessPolicy/AccessDeniedMessage(claim 级访问控制) |
| AUTH-072 | 自定义 provider per-field JSONPath 映射 (>3 claim) | ✗ | ✓(UserId/Username/DisplayName/Email field) | ✗ | 部分完成 | P2 | 仅 3 个 claim-map(sub/email/name) | 扩展任意 JSONPath claim 映射 |
| AUTH-073 | OAuth callback 前端页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端 `POST /v1/auth/oauth-callback`(auth_handler.go:154);P0-U-005 | 建 `/oauth/[provider]` 回调页 |

## G. OAuth 流程安全 (state/PKCE/SSRF)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-074 | OAuth flow-session 表 (state/nonce/pkce) | 🟡 | 🟡 | ✗ | 已完成 | — | oauth_flow_sessions(0020) | — |
| AUTH-075 | state/nonce 哈希存储 + 唯一约束 | 🟡 | 🟡 | ✗ | 已完成 | — | state_hash/nonce_hash bytea + uq(0020) | — |
| AUTH-076 | PKCE verifier 静态加密 (AES-GCM, AAD 绑定) | 🟡(明文列) | ✗ | ✗ | 已完成 | — | encryptPKCEVerifier(store.go:754) | — 强于参考 |
| AUTH-077 | OAuth flow TTL (10m) | ✓ | ✓ | ✗ | 已完成 | — | DefaultOAuthFlowTTL=10m(email_verify.go:17) | — |
| AUTH-078 | redirect_uri allowlist (防开放重定向) | 🟡 | 🟡 | ✗ | 已完成 | — | validateOAuthRedirectURI fail-closed(social_login.go:91) | — |
| AUTH-079 | 上游端点 SSRF 守卫 (loopback/private/CGNAT/metadata) | 🟡 | ✗ | ✗ | 已完成 | — | ValidateOAuthEndpointURL + dial 时 SSRF deny(oauth_flow.go:86) | — 强于参考 |
| AUTH-080 | Pending-OAuth → 邮箱绑定 (无 provider 邮箱) | ✓ | 🟡 | ✗ | 已完成 | — | pending_oauth_sessions AES-256-GCM AAD(0081);ErrOAuthPendingEmailRequired | — |
| AUTH-081 | 无邮箱 provider 合成邮箱 | ✓ | 🟡 | ✗ | 已完成 | — | syntheticOAuthEmail(oauth_social_provider_flows.go:464) | — |

## H. 账号绑定 / 社交身份链接 (Account binding)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-082 | 社交身份链接表 + 单 provider 唯一 | ✓ | ✓ | ✗ | 已完成 | — | social_identity_links PK(tenant,provider,subject)(0020) | — |
| AUTH-083 | provider 枚举约束 (google/github/wechat/dingtalk/linuxdo/oidc) | open string | per-col | ✗ | 已完成 | — | CHECK 加宽(0081) | — |
| AUTH-084 | 绑定到现有账户 + 按身份查用户 | ✓ | ✓ | ✗ | 已完成 | — | LinkSocialIdentity/GetUserBySocialIdentity(store.go:197/175) | — |
| AUTH-085 | identity-changed 端点 | 🟡 | 🟡 | ✗ | 后端有·前端缺 | P1 | `POST /v1/auth/social/identity-changed`(auth_handler.go:155);无前端 | 接前端绑定管理页 |
| AUTH-086 | 解绑 / 管理员清除绑定 | ✓(admin) | ✓(AdminClearUserBinding) | ✗ | 缺失 | P1 | HUAKAI 仅 link,无显式 unbind(骨架 #8) | 增 DELETE 社交绑定 + admin 清绑 |
| AUTH-087 | 按属性采纳 (name/avatar per-attr on link) | ✓(identity_adoption_decision) | 🟡 | ✗ | 未做 | P3 | HUAKAI 无 per-attribute adoption(细树 §9) | 增链接时属性采纳决策(可选) |

## I. Passkey / WebAuthn

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-088 | passkey_credentials 表 (credential_id/public_key/sign_count/aaguid…) | ✗ | ✓ | ✗ | 已完成 | — | passkey_credentials(0098) | — |
| AUTH-089 | 克隆检测 (sign-count 回退 → clone_warning) | ✗ | ✓ | ✗ | 已完成 | — | signCountRegressed→ErrCloneDetected(service.go:193);clone_warning(0098) | — |
| AUTH-090 | 注册 begin/finish (后端) | ✗ | ✓ | ✗ | 后端有·前端缺 | P0 | `/v1/me/passkeys/register/begin|finish`(06 §G,targeted test);无安全设置页 | 接前端 passkey 注册流 |
| AUTH-091 | 可发现登录 begin/finish (后端) | ✗ | ✓ | ✗ | 后端有·前端缺 | P0 | BeginDiscoverableLogin(webauthn.go:68);P0-U-007 | 登录页加 passkey 按钮 |
| AUTH-092 | 列出 / 删除凭证 (后端) | ✗ | ✓ | ✗ | 后端有·前端缺 | P0 | GET / + DELETE /{id}(06 §G) | 安全设置页列/删 passkey |
| AUTH-093 | ceremony challenge TTL (5m) + session 表 | ✗ | 🟡 | ✗ | 已完成 | — | DefaultChallengeTTL=5m;webauthn_session(0098) | — |
| AUTH-094 | RP origin allowlist (fail-closed) | ✗ | 🟡(可配 + AllowInsecureOrigin) | ✗ | 已完成 | — | RPOrigins + ErrOriginNotAllowed(types.go:43) | — |
| AUTH-095 | 凭证 owner/duplicate 守卫 | ✗ | 🟡 | ✗ | 已完成 | — | ErrCredentialOwnerMismatch/ErrDuplicateCredential | — |
| AUTH-096 | Passkey 注册开关 (独立) | — | ✓(折叠进 Enabled) | ✗ | 已完成 | — | KeyPasskeyRegistrationEnabled "false" | — |
| AUTH-097 | UserVerification / Attachment 管理端可配 | ✗ | ✓(UserVerification/AttachmentPreference) | ✗ | 缺失 | P3 | HUAKAI fail-closed,无这些管理端开关 | 增 UV/attachment 偏好设置(可选) |
| AUTH-098 | 管理员重置 passkey | ✗ | ✓(AdminResetPasskey) | ✗ | 缺失 | P2 | HUAKAI 无专用 admin handler | 增 admin 重置用户 passkey |

## J. TOTP / 2FA

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-099 | 2FA 总开关 (默认开) | 🟡 | 🟡 | ✗ | 已完成 | — | KeyTwoFactorEnabled 默认 "true"(types.go:26) | — |
| AUTH-100 | TOTP 参数 (digits6/step30s/window1/HMAC-SHA1 RFC6238) | 🟡 | 🟡 | ✗ | 已完成 | — | twofa/totp.go:16/22/48 | — |
| AUTH-101 | secret 静态加密 (AES-256, AAD tenant+user) | ✓ | 🟡(明文) | ✗ | 已完成 | — | two_factor_settings.secret_enc(0087);Cipher.Encrypt | — 强于 new-api |
| AUTH-102 | 加密密钥就绪门 (无 key 不可开 2FA) | ✓ | ✗ | ✗ | 已完成 | — | ErrKeyUnavailable(types.go:25) | — |
| AUTH-103 | 备份码 (10 个, 哈希存储, 一次性, 可重生) | ✗(无) | ✓ | ✗ | 已完成 | — | DefaultBackupCodeCount=10;code_hash bytea(0087);RegenerateBackupCodes | — |
| AUTH-104 | 失败计数 + N 次锁定 (5 次/15m) | 🟡 | ✓ | ✗ | 已完成 | — | DefaultMaxFailedAttempts5;DefaultLockDuration15m(types.go:13,19) | — |
| AUTH-105 | 登录态 2FA 挑战 token (HMAC 签名) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | StartLoginChallenge;`POST /v1/auth/login/2fa`(auth_handler.go:150);P0-U-006 | 建 2FA 登录挑战页 |
| AUTH-106 | 2FA setup/enable/status/disable (后端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `/v1/auth/2fa/{setup,enable,status,disable}`(06 §G [已完成]) | 安全设置页接 2FA 启用流 |
| AUTH-107 | 备份码重生端点 (后端) | ✗ | ✓ | ✗ | 后端有·前端缺 | P1 | `/v1/auth/2fa/backup-codes/regenerate`(06 §G) | 安全设置页接备份码重生 |
| AUTH-108 | 管理员强制禁用 2FA + 2FA 统计 | ✗ | ✓(AdminDisable2FA/Admin2FAStats) | ✗ | 缺失 | P2 | HUAKAI 仅 status(细树 §11) | 增 admin force-disable-2FA + 统计 |

## K. 步进认证 (Step-up re-auth)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-109 | step-up 机制 (敏感操作 re-auth) | 🟡 | ✓(UniversalVerify) | ✗ | 已完成 | — | LocalStepUpVerifier(passkeyhttp/stepup.go) | — |
| AUTH-110 | step-up 方法枚举 (2fa/passkey) | 🟡 | ✓ | ✗ | 部分完成 | P3 | VerifyStepUp via 2FA(passkey 方法待补全) | 补 passkey 作为 step-up 方法 |
| AUTH-111 | step-up 门控 passkey 注册/删除 | n/a | ✓ | ✗ | 已完成 | — | verifyStepUp on register begin/finish(passkeyhttp/handler.go:80) | — |
| AUTH-112 | passkey-ready 短时标记 / step-up 窗口 | n/a | ✓ | ✗ | 部分完成 | P3 | 🟡 verified-marker | 补 step-up 时间窗 + ready 标记常量 |

## L. 验证码 (Captcha)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-113 | Captcha 开关 + provider 选择器 | ✓(Turnstile only) | ✓(Turnstile only) | ✗ | 已完成 | — | KeyCaptchaEnabled/KeyCaptchaProvider(verifier.go:161) | — |
| AUTH-114 | Turnstile 校验 (10s 超时, fail-closed) | ✓ | ✓ | ✗ | 已完成 | — | siteverify(verifier.go:17);ErrTokenRequired | — |
| AUTH-115 | 注册 + 登录均接 captcha | ✓ | ✓ | ✗ | 已完成 | — | verifyAuthCaptcha(auth_handler.go:415) | — |
| AUTH-116 | reCAPTCHA / hCaptcha / GeeTest provider | ✗ | ✗ | ✗ | 未做 | P3 | HUAKAI 仅 Turnstile | 增多 captcha provider(可选) |

## M. 登录限流 / 锁定 (Login throttle)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-117 | per-IP 并发上限 (InFlight=4, 限并发 argon2) | 🟡 | ✗ | ✗ | 已完成 | — | InFlightLimit=4(limiter.go:71) | — HUAKAI 独有 |
| AUTH-118 | 失败滑窗 (Window 1m / WindowLimit 10) | ✓(20/min) | 🟡 | ✗ | 已完成 | — | limiter.go:72-73 | — |
| AUTH-119 | 封禁窗 (BanWindow 10m / BanAfter 20) | ✗ | ✗ | ✗ | 已完成 | — | limiter.go:74-75 → ReasonIPBanned | — HUAKAI 独有 |
| AUTH-120 | 限流原因枚举 (5 态) + pre-hash 门 | ✗ | ✗ | ✗ | 已完成 | — | Allowed/IPInFlight/IPWindow/IPBanned/KeyPressure(limiter.go:23);Begin 在 argon2 前 | — HUAKAI 独有 |
| AUTH-121 | KeyPressure 内存守卫 fail-closed | ✗ | ✗ | ✗ | 已完成 | — | ReasonKeyPressure(limiter.go:27) | — |
| AUTH-122 | Retry-After 头 + 429 | ✗ | ✗ | ✗ | 已完成 | — | writeLoginThrottled 429(auth_handler.go:947) | — |
| AUTH-123 | 账户级锁定阈值 (5 次→locked) | 🟡 | ✓ | ✗ | 已完成 | — | DefaultLockoutThreshold=5(email_verify.go:18) → UserStatusLocked | — |
| AUTH-124 | 管理员解锁账户 | 🟡 | 🟡 | ✗ | 缺失 | P1 | locked_until 列存在但无 `/admin/v1/users/{id}/unlock`(骨架 #14,#49) | 增 admin 解锁路由 + 后台入口 |
| AUTH-125 | 多节点分布式限流 (redis) | ✓(redis) | ✓(redis) | ✗ | 部分完成 | P2 | 单进程(limiter.go:91 注释 redis follow-up) | 增 redis 后端使限流跨节点 |
| AUTH-126 | 注册端点专用限流 | ✓(5/min) | 🟡 | ✗ | 已完成 | — | cmd/gateway/rate_limit.go register 5/min | — |

## N. 会话管理 (Session / token families)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-127 | 机制: 不透明哈希 token families | JWT+refresh | cookie | mgmt secret | 已完成 | — | session_families/session_tokens/refresh_tokens(0021) | — |
| AUTH-128 | family 状态枚举 (5 态) + generation 计数器 | n/a | n/a | ✗ | 已完成 | — | CHECK(active,revoked,expired,suspicious,replaced)(0021) | — |
| AUTH-129 | device_info jsonb (object-checked) + ip_baseline | 🟡 | ✗ | ✗ | 已完成 | — | device_info CHECK jsonb_typeof='object';ip_baseline(0021) | — |
| AUTH-130 | token 哈希存储 + HMAC 签名 | 🟡 | 🟡(cookie sig) | ✗ | 已完成 | — | HashRefreshToken/HashSessionToken sha256;HMAC sign(rotation.go:250) | — |
| AUTH-131 | session TTL 15m / refresh TTL 30d | ✓ | ✓ | ✗ | 已完成 | — | DefaultSessionTTL=15m;DefaultRefreshTTL=30d(rotation.go:17-18) | — |
| AUTH-132 | refresh 轮转 (新 generation, 每族唯一) | ✓ | ✗ | ✗ | 已完成 | — | RotateRefreshToken(rotation.go:76);uq(family_id,generation) | — |
| AUTH-133 | refresh 重放检测 → 撤销整族 | ✓(ErrRefreshTokenReused) | ✗ | ✗ | 已完成 | — | consumed-token reuse → RevokeFamily("refresh_replay")(rotation.go:103) | — |
| AUTH-134 | 跨用户 refresh 守卫 | 🟡 | ✗ | ✗ | 已完成 | — | RevokeFamily("..cross_user");ErrSessionUserMismatch(rotation.go:96) | — HUAKAI 独有 |
| AUTH-135 | 异常/设备漂移检测 (DriftLevel 4 态 + 命名原因) | ✗ | ✗ | ✗ | 已完成 | — | DetectDrift(anomaly.go:11);High→RevokeFamily;ua/ip_changed 原因 | — HUAKAI 独有 |
| AUTH-136 | IPClass/UserAgentClass 粗化 (隐私保护) | ✗ | ✗ | ✗ | 已完成 | — | IPClass/UserAgentClass(anomaly.go:46,61) | — HUAKAI 独有 |
| AUTH-137 | 撤销单会话 / 全部 / 其他 (保留当前) | ✓ | 🟡 | ✗ | 已完成 | — | Revoke/RevokeUser/RevokeOthers(invalidation.go:8/46/52) | — |
| AUTH-138 | 设备数限制 + 设备确认策略 | 🟡 | ✗ | ✗ | 已完成 | — | ErrDeviceLimitExceeded(rotation.go:309);ErrDeviceConfirmationRequired | — |
| AUTH-139 | session refresh/list/revoke 端点 (后端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `/v1/sessions/{refresh,list,revoke}`(session_handler.go:40-45);P0-U-010 | 建 `/profile/sessions` 设备管理页 |
| AUTH-140 | 专用 logout 端点 | ✓(/api/user/logout) | ✓ | ✗ | 缺失 | P2 | 无 `POST /v1/auth/logout`,经 sessions/revoke 代替(骨架 #23) | 增命名 logout 端点(OpenAPI 约定) |
| AUTH-141 | session me 端点 (who am I) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `GET /v1/auth/me`(panelauthhttp);P0-U-008 资料页 | 建 `/profile` 资料页接 /auth/me |
| AUTH-142 | session 签名密钥轮转 | 🟡 | 🟡 | ✗ | 缺失 | P2 | SigningKey 静态 []byte,无 key-versioning(骨架 #69) | 增 key 版本化 + 滚动窗 |
| AUTH-143 | token 内省端点 (introspect) | 🟡 | ✗ | ✗ | 未做 | P3 | 无 `POST /v1/auth/introspect`(骨架 #70) | 增 introspection(服务间鉴权用) |
| AUTH-144 | 登录页 (前端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端 `POST /v1/auth/login`(auth_handler.go:149);P0-U-001 | 新建 `frontend/app/login/page.tsx` |
| AUTH-145 | 安全设置页 (聚合 2FA/passkey/改密/会话) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 各后端就绪;无 `/profile/security`(12E §2;P0-U-009) | 建安全设置页聚合 AUTH-052/106/092/139 |

## O. 入站 API Key 管理 (Inbound API keys)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-146 | 用户自助 API key 签发 (后端) | ✓ | ✓ | ✓(static AccessProvider) | 后端有·前端缺 | P0 | `POST /v1/api-keys`(userkeyhttp/handlers.go:41);明文仅一次;P0-U-012 | 建 API key 创建/复制 modal |
| AUTH-147 | API key 列表 (后端) | ✓ | ✓ | 🟡 | 后端有·前端缺 | P0 | `GET /v1/api-keys`(handlers.go:42);Sidebar `/api-keys` disabled(12E §3);P0-U-011 | 上架 API key 列表页 + 启用导航 |
| AUTH-148 | API key 撤销 (用户/管理员) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `DELETE /v1/api-keys/{id}`(handlers.go:44);admin DELETE;P0-U-013 | 列表页接撤销动作 |
| AUTH-149 | API key 环境 live/test 前缀 (hk_live_/hk_test_) | 🟡 | 🟡 | ✗ | 已完成 | — | EnvLive/EnvTest(userkey.go);EnvAdmin 拒用户签发 | — |
| AUTH-150 | API key 过期 (expires_at) | ✓ | 🟡 | ✗ | 部分完成 | P2 | expires_at 检查;sweep 索引在,worker 未接(骨架 #33 Phase E) | 接 expiry sweep worker |
| AUTH-151 | 每用户活跃 key 上限 (20, advisory lock) | ✓ | 🟡 | ✗ | 已完成 | — | MaxActiveKeysPerUser=20(userkey.go:53) | — |
| AUTH-152 | API key name/label (≤128) | ✓ | ✓ | ✗ | 已完成 | — | api_keys.name;MaxNameLen=128 | — |
| AUTH-153 | per-key 配额设置/读取 (后端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `PUT/GET /v1/api-keys/{id}/quota`(userkeycontrolshttp/mount.go:25);P0-U-014 | key 设置页接 quota(纠正骨架旧"MISSING") |
| AUTH-154 | per-key group 设置/读取 (后端) | ✓ | 🟡 | ✗ | 后端有·前端缺 | P0 | `PUT/GET /v1/api-keys/{id}/group`(mount.go:27);P0-U-015 | key 设置页接 group |
| AUTH-155 | per-key IP allowlist 设置/读取 (后端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `PUT/GET /v1/api-keys/{id}/ip-allowlist`(mount.go:29);P0-U-016 | key 设置页接 IP allowlist(纠正骨架旧"MISSING") |
| AUTH-156 | per-key 模型 allowlist / scopes | ✓(OpenRouter) | ✓(channel group) | ✗ | 缺失 | P2 | api_keys 无 allowed_models/scope 列(骨架 #37) | 增 key 级模型 allowlist/scope |
| AUTH-157 | per-key 速率/消费上限 | ✓ | ✓(per-channel) | ✗ | 部分完成 | P2 | 有 per-key quota,无独立 rate/spending cap(骨架 #39) | 增 key 级 rate-limit / 月预算 |
| AUTH-158 | 明文 token 永不持久/打日志 | ✓ | ✓ | ✓ | 已完成 | — | IssueResult.String() redact;仅存 key_hash(userkey.go:87) | — |

## P. 管理员认证 / RBAC (Admin auth & RBAC)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-159 | 角色表示 (text CHECK admin/user) | ✓(string 2 级) | ✓(int 4 级) | 单 mgmt secret | 已完成 | — | users.role(0076) | — |
| AUTH-160 | 分离的 admin 凭证表 (admin_tokens) | 🟡(JWT claim) | 🟡(session) | ✓(SecretKey bcrypt) | 已完成 | — | admin_tokens 13 字段:key_hash bcrypt/prefix16/role/scope/bootstrap/status/expires(0010) | — |
| AUTH-161 | admin token bearer 解析 (hk_admin_*) | n/a | n/a | n/a | 已完成 | — | Resolve()(operator_auth.go:52);16-char prefix 索引 | — |
| AUTH-162 | admin RBAC: platform_admin vs tenant_operator | n/a | n/a | n/a | 已完成 | — | CanIssueForTenant(operator_auth.go:119);ErrAdminForbidden | — HUAKAI 独有 |
| AUTH-163 | scope-tenant 一致性 CHECK 约束 | ✗ | ✗ | ✗ | 已完成 | — | CHECK(platform_admin↔NULL, tenant_operator↔NOT NULL)(0010) | — HUAKAI 独有 |
| AUTH-164 | admin bootstrap token (env-seeded) | ✗ | 🟡(root env) | 🟡(env) | 已完成 | — | HUAKAI_ADMIN_BOOTSTRAP_TOKEN;hk_admin_+24-char(bootstrap.go:90) | — |
| AUTH-165 | admin token 过期 + 状态枚举 | n/a | n/a | 🟡 | 已完成 | — | expires_at;CHECK(active,disabled,revoked)(0010) | — |
| AUTH-166 | admin 签发限流 (30/hr/actor) + advisory lock | ✗ | 🟡 | ✗ | 已完成 | — | rateLimitPerHour=30(issuer.go:84);advisory-lock(issuer.go:166) | — |
| AUTH-167 | 防提权守卫 (manage-target-role) | 🟡 | ✓(canManageTargetRole) | ✗ | 已完成 | — | CanIssueForTenant scope | — |
| AUTH-168 | panel 路由 deny-by-default | 🟡 | 🟡(sidebar by role) | ✗ | 已完成 | — | PanelForRole/PanelForAdminToken(panelauth/resolve.go:8/17) | — |
| AUTH-169 | 角色分配 (提升为 admin, 后端写) | ✓ | ✓ | ✗ | 缺失 | P1 | 0076 加 role 列,panelauth 读;无 `/admin/v1/users/{id}/role` 写(骨架 #50) | 增 admin 角色分配路由 |

## Q. 管理员用户管理 (Admin user management)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-170 | 用户列表/搜索 | ✓ | ✓ | ✗ | 缺失 | P1 | 无 `/admin/v1/users`;api_keys_handler.go:3 注释"later slices"(骨架 #45) | 建 admin 用户列表/搜索路由 + 页 |
| AUTH-171 | 用户创建 (admin) | ✓ | ✓ | ✗ | 部分完成 | P1 | userauth CreateUser 存在,无 admin HTTP 路由(骨架 #46) | 挂 admin create-user 路由 |
| AUTH-172 | 用户禁用/启用 | ✓ | ✓ | ✗ | 缺失 | P1 | 无 `/admin/v1/users/{id}/status`(骨架 #47) | 增用户状态切换路由 |
| AUTH-173 | 用户删除 (软删) | ✓ | ✓ | ✗ | 缺失 | P1 | users.deleted_at 列在,无 API(骨架 #48) | 增 admin 软删路由 |

## R. 多租户 (Multi-tenant)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-174 | 所有 auth SQL 租户隔离 | 🟡 | 🟡 | ✗ | 已完成 | — | 每查询含 tenant_id=$N 或复合 FK(tenant_id,id)(0007) | — HUAKAI 独有 |
| AUTH-175 | 注册模式 per-tenant DB 配置 | ✓(per-channel) | 🟡 | ✗ | 缺失 | P2 | 模式从 env 加载,无 per-tenant DB 设置(骨架 #52) | 模式迁到 per-tenant DB 配置 |
| AUTH-176 | admin 租户管理 (create/list/disable) | ✓ | 🟡 | ✗ | 缺失 | P2 | tenants 表在(0001),无 `/admin/v1/tenants` 路由(骨架 #53) | 增租户管理 API |

## S. 邀请门控注册 (Invite-gated registration)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-177 | 邀请必需开关 | 🟡 | ✗ | ✗ | 已完成 | — | KeyInvitationRequired 默认 "true";ErrInviteRequired | — |
| AUTH-178 | invite_codes 表 (max_uses/used_count/status/valid_until) | 🟡 | 🟡(InviterId) | ✗ | 已完成 | — | invite_codes(0020);CHECK used_count<=max_uses | — |
| AUTH-179 | 邀请码哈希存储 (hki_ 前缀, sha256) | ✓ | ✗ | ✗ | 已完成 | — | HashInviteCode(invite.go:23) | — |
| AUTH-180 | invite_bindings 兑换审计 | ✓ | 🟡 | ✗ | 已完成 | — | invite_bindings UNIQUE(tenant,user,code)(0020) | — |
| AUTH-181 | 兑换竞态安全 (advisory lock) | 🟡 | ✗ | ✗ | 已完成 | — | pg_advisory_xact_lock(invite.go:28) | — HUAKAI 独有 |
| AUTH-182 | 独立 validate-invite 端点 | ✓(10/min) | ✗ | ✗ | 缺失 | P2 | HUAKAI 仅注册时内联(细树 §17) | 增独立 validate-invitation-code 端点 |
| AUTH-183 | 社交注册 invite-gate | 🟡 | 🟡 | ✗ | 已完成 | — | SocialSignup flag + social_invite_gate_test | — |
| AUTH-184 | 邀请/返佣 用户/后台 端点 | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | `/v1/invitations`,`/v1/me/invitations`(06 §H [部分]);无 affiliate 前端 | 建邀请/返佣前端 + 提现流 |
| AUTH-185 | 返佣奖励开关 + cents | ✓(affiliate) | ✓(QuotaForInviter) | ✗ | 部分完成 | P2 | KeyReferralRewardEnabled/Cents;referral_reward.go(reward 提交在 fix 分支) | 前端化 + 提现/转余额 |

## T. 用户通知设置 (User notification settings)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-186 | notify_type 5 渠道枚举 (none/email/webhook/bark/gotify) | 🟡(balance only) | ✗ | ✗ | 后端有·前端缺 | P1 | CHECK 5 渠道(0089);`/v1/users/me/notifications`(06 §M) | 建用户通知设置页(纠正骨架未覆盖) |
| AUTH-187 | webhook_url+secret / bark / gotify(token+priority) | ✗ | ✗ | ✗ | 后端有·前端缺 | P2 | gotify_priority CHECK 0-10(0089) | 前端各渠道配置表单 |
| AUTH-188 | balance_threshold 余额告警 | ✓ | ✗ | ✗ | 后端有·前端缺 | P2 | numeric(20,8) DEFAULT 5(0089) | 前端余额阈值设置 |
| AUTH-189 | per-type 完整性 CHECK 约束 | ✗ | ✗ | ✗ | 已完成 | — | chk per-type required-field(0089) | — HUAKAI 独有 |

## U. 登录安全横切 / 审计 (Cross-cutting / audit)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-190 | 安全事件审计 sink (login-fail/register/reset/oauth/refresh) | 🟡(ops log) | 🟡(SysLog) | 🟡(request log) | 已完成 | — | newAuthEventSink(zap);AuthEventSink 接口 | — |
| AUTH-191 | 通用登录失败响应 (不泄信息) | 🟡 | 🟡 | n/a | 已完成 | — | writeLoginFailureGeneric(auth_handler.go:252) | — |
| AUTH-192 | refresh 结果分类 (outcome taxonomy) | ✗ | ✗ | ✗ | 已完成 | — | ClassifyRefreshError/RefreshOutcome(auth/audit.go) | — HUAKAI 独有 |
| AUTH-193 | OAuth provider-account refresh 审计链 | 🟡 | 🟡 | ✗ | 已完成 | — | WriteRefreshAudit;oauth_refresh_audit_events(0013) | — |
| AUTH-194 | 持久化用户 API-key 审计日志 (DB-backed) | ✓ | 🟡 | ✗ | 部分完成 | P2 | 仅 slog;DB user_audit_events 推迟(RR-W5-009)(骨架 #73) | 落地 user_audit_events 表(SOC2/ISO 合规) |
| AUTH-195 | 管理员审计事件表 (action/target CHECK 枚举 + 限流索引) | 🟡(ops log) | 🟡(SysLog) | 🟡 | 已完成 | — | admin_audit_events(0010);action 6 值/target 4 值 CHECK | — HUAKAI 独有 |
| AUTH-196 | 租户 SMTP 邮件设置 (admin 可配, AES-GCM 密码信封) | ✓ | ✓ | ✗ | 后端有·前端缺 | P2 | email_settings(0025);`/v1/admin/email/{settings,test}`(06 §G);settings 页 disabled(12E §3) | 建管理端邮件设置页 |
| AUTH-197 | platform-settings 管理端读写 | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | `/v1/admin/platform-settings[/{key}]`(06 §G);settings 页 disabled | 建平台设置管理页 |

## V. 企业 SSO (Enterprise SSO)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-198 | SAML 2.0 SSO | ✓(enterprise tier) | ✗ | ✗ | 未做 | P2 | 无 SAML 库/handler(骨架 #58) | 实现 SAML SP(企业合同硬需求) |
| AUTH-199 | LDAP / Active Directory | ✗ | ✗ | ✗ | 未做 | P3 | 无 LDAP 命中(骨架 #59) | 实现 LDAP 绑定认证 |
| AUTH-200 | SCIM 用户供给 | ✗ | ✗ | ✗ | 未做 | P3 | 无 SCIM 端点(骨架 #60) | 实现 SCIM 2.0 provisioning |
| AUTH-201 | OIDC Provider (HUAKAI 作为 IdP) | ✗ | ✗ | ✗ | 未做 | P3 | HUAKAI 仅消费 OIDC,非签发方(骨架 #61) | 实现 OIDC IdP(可选) |

## W. CLIProxy 管理面认证 (CLIProxy mgmt — 单租户对照)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AUTH-202 | 管理 SecretKey (明文/bcrypt 自动哈希) | n/a | n/a | ✓(config.go:675 hashSecret) | 已完成 | — | HUAKAI 用 admin_tokens 等价替代 | — 不同域,HUAKAI 平台化更强 |
| AUTH-203 | local-password keep-alive 面板门 | n/a | n/a | ✓(server.go:778 ConstantTimeCompare) | 已完成 | — | HUAKAI SessionMiddleware 等价 | — |
| AUTH-204 | mgmt allow-remote 开关 | n/a | n/a | ✓(config.go:196 AllowRemote) | 已完成 | — | HUAKAI 经网络边界/admin 路由控制 | — |
| AUTH-205 | 入站静态 AccessProvider API-key 条目 | n/a | n/a | ✓(sdk/access/types.go) | 已完成 | — | HUAKAI userkey + admin api_keys 等价且更强 | — |

---

## 优先级汇总 (Priorities rollup)

**P0 (商业闭环必补 — 12D P0-U;全部为"后端有·前端缺"):**
登录页 AUTH-144 / 注册页 AUTH-006 / 重置密码页 AUTH-048 / 邮箱验证页 AUTH-040 / OAuth 回调页 AUTH-073 / 2FA 挑战页 AUTH-105 / 2FA 设置 AUTH-106 / Passkey 登录+注册+列删 AUTH-090..092 / 资料页(/auth/me) AUTH-141 / 安全设置页 AUTH-145 / 会话设备页 AUTH-139 / API key 列表+创建+删除 AUTH-146..148 / per-key quota+group+IP allowlist 页 AUTH-153..155。

**P1 (成熟后台需补):**
用户资料自助更新 AUTH-027 / 账户删除 AUTH-028 / 已登录改密路由+入口 AUTH-052 / 社交解绑 AUTH-086 / identity-changed 前端 AUTH-085 / 备份码重生前端 AUTH-107 / 管理员解锁 AUTH-124 / 角色分配路由 AUTH-169 / admin 用户 CRUD AUTH-014,AUTH-170..173 / 自定义 OAuth provider CRUD AUTH-069 / 通知设置页 AUTH-186 / platform-settings 管理页 AUTH-197 / 邀请返佣前端 AUTH-184。

**P2 (对标参考增强):**
邮箱域白名单 AUTH-007/008 / 专用发码限流 AUTH-037 / DingTalk corp-sync AUTH-058 / LinuxDo trust 门 AUTH-060 / OIDC 校验开关 AUTH-063 / WeChat 多变体 AUTH-066 / Discord AUTH-067 / Telegram AUTH-068 / custom-provider AuthStyle/AccessPolicy AUTH-070..072 / admin 重置 passkey AUTH-098 / admin force-disable-2FA AUTH-108 / key 模型 scope+rate cap AUTH-156/157 / key expiry worker AUTH-150 / 分布式限流 AUTH-125 / 专用 logout AUTH-140 / session 签名密钥轮转 AUTH-142 / per-tenant 注册模式 AUTH-175 / 租户管理 AUTH-176 / 独立 validate-invite AUTH-182 / DB 审计日志 AUTH-194 / 邮件设置页 AUTH-196 / SAML AUTH-198。

**P3 (低优/可选):** 密码子开关拆分 AUTH-004/005 / alias 限制 AUTH-009 / 属性采纳 AUTH-087 / passkey UV/attachment AUTH-097 / step-up 方法补全 AUTH-110/112 / 多 captcha provider AUTH-116 / token introspect AUTH-143 / LDAP/SCIM/OIDC-IdP AUTH-199..201。

## HUAKAI 独有强项 (无参考拥有该字段, 标杆领先)
password_version 会话失效列 · argon2 哈希篡改上界守卫 · 用户枚举时序均衡 · 重置 token 绑 password_version · PKCE 加密 + state/nonce 哈希 · session family generation + 5 态 + device_info object-check + ip_baseline · refresh 重放/跨用户/漂移撤族 + DriftLevel 4 态 + IP/UA 粗化 · loginthrottle 5 参 + 5 原因 + pre-hash + KeyPressure · admin_tokens scope-一致性 CHECK + 审计 action/target CHECK 枚举 + 30/hr advisory-lock · invite used_count<=max_uses CHECK + advisory-lock 兑换 · 加密 TOTP secret + 版本化备份码哈希前缀 · 通知 5 渠道 per-type 完整性 CHECK · 租户隔离复合 FK · platform_admin/tenant_operator 双层 RBAC · QQ/NodeSeek 登录。

## 关键校准说明
1. **骨架(源1)旧标"MISSING"已被 12E §7 推翻的项**: per-key IP allowlist / quota / group 后端均已存在(`userkeycontrolshttp/mount.go:25-30`),故标 `后端有·前端缺` 而非 `缺失`。
2. **禁止虚标**: auth/2FA/passkey/session/key 后端路由存在(12E §7/§8, 06 §G/§H),但 12E §2/§3 证实无 login/register/profile/security/sessions/api-keys 前端页且 Sidebar 相关项 disabled → 一律 `后端有·前端缺`,不写"已完成"。
3. **无需 UI 的内部安全原语**(哈希/限流/SSRF/token 轮转/审计 sink/租户隔离)在后端就绪且有针对测试 → `已完成`。
4. **main 分支滞后**: 基线为 `fix/hermes-phase-1`(183 paths),`main` 仅 83 paths;referral reward(AUTH-185)等 fix 分支较新提交在 main 合并前对默认分支视为部分;整体合并状态以 fix 分支为准。


# ====================  模块 B  ====================
# 标杆 · 网关核心/模型接入/协议转换

> 模块组: 网关核心 + 模型接入 + 协议转换 + 内容/媒体端点 + Juice(路由透明).
> 合并自 3 源: ① 大功能树骨架(`docs/process/feature-tree/{gateway-core,model-catalog-pricing,content-features}.md`) ② 字段级细树(`feature-audit/02-model-protocol-fine.md`) ③ 151 参考(`151-ref/{02-model-protocol(=02-reference-code-feature-trees),06-huakai-endpoint-status-tree,12D-priority-tree,02-reference-code-feature-trees}.md`).
> HUAKAI 云端基线: `origin/fix/hermes-phase-1-e33d940@e89d7fce` (OpenAPI 183 paths). 参考: sub2api@635ad81 / new-api@adc390c5 / CLIProxyAPI@3abfc83.
> refs 列: ✓ 有 / 🟡 部分或裸透传 / ✗ 无. HUAKAI状态六级: 已完成 / 部分完成 / 后端有·前端缺 / 缺失 / 未做 / 未合并main.
>
> **关键源冲突已标注**: 骨架源①(gateway-core.md, 早期/粗读) 把 embeddings/images/audio 判为 MISSING(grep routes.go 0 命中); 但细树源②与 151 源③(均深读 fix/hermes HEAD) 确认它们已挂载在 `routes.go:57-63`. 本表以 fix 分支真实挂载为准(已完成/部分完成), 并在证据列保留源①分歧. **main 仅 83 paths, fix 183**: 凡 fix 有而 main 无的端点统一加 `未合并main`.

---

## A. 入站端点 (每个 HTTP route = 一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| PROTO-001 | `POST /v1/chat/completions` | ✓ | ✓ | ✓ | 已完成 (fix); 未合并main? | — | `routes.go:56` gatewayhttp NewChatCompletionsHandler | main 基线已含; 守住回归 |
| PROTO-002 | `POST /v1/messages` (Anthropic native) | ✓ | ✓ | ✓ | 已完成 | — | `routes.go:65` NewMessagesHandler | — |
| PROTO-003 | `POST /v1/responses` (OpenAI Responses) | ✓ | ✓ | ✓ | 已完成 | — | `routes.go:64` NewResponsesHandler | — |
| PROTO-004 | `POST /v1/responses/compact` | 🟡 | ✓ | ✓ | 缺失 | P2 | grep responses/compact → 0 | 评估是否随 new-api 补 compact 变体 |
| PROTO-005 | `POST /v1/completions` (legacy text) | ✗ | ✓ | ✓ | 缺失 | P3 | grep `/v1/completions` 独立 route → 0 (`routes.go`) | legacy, 低优; 需要时复用 chat adapter |
| PROTO-006 | `POST /v1/messages/count_tokens` | ✓ | 🟡 | ✓ | 缺失 | P2 | 未挂载; 连带跨格式 token-count 翻译缺 (H 段) | 挂 count_tokens handler + 跨格式估算 |
| PROTO-007 | `POST /v1/embeddings` | ✗ | ✓ | ✗ | 已完成 (fix) · 未合并main | P0 | 源②③: `routes.go:57` embeddingshttp 独立 pkg ‖ **源①gateway-core判MISSING(分歧, 早期grep未命中)** | 合入 main; 守 input-token passthrough 计费 |
| PROTO-008 | `GET/POST /engines/:model/embeddings` (alias) | ✗ | ✓ | ✗ | 缺失 | P3 | new-api alias route; HUAKAI 无 | 低优 alias |
| PROTO-009 | `POST /v1/rerank` | ✗ | ✓ | ✗ | 缺失 | P2 | 无 rerankhttp pkg; `relay/rerank_handler.go` 仅 new-api | 新建 rerankhttp + 5 参数 (F.4) |
| PROTO-010 | `POST /v1/moderations` | ✗ | ✓ | ✗ | 部分完成 | P1 | inbound ModerationScreener + admin `moderationhttp/`; 无 OpenAI 兼容 `/v1/moderations` 出口端点 | 暴露兼容端点 (透传 OpenAI/自有 screener) |
| PROTO-011 | `POST /v1/edits` (legacy) | ✗ | ✓ | ✗ | 缺失 | P3 | new-api RelayModeEdits only | legacy, park |
| PROTO-012 | `POST /v1/images/generations` | 🟡 OpenAI组 | ✓ | ✓ | 已完成 (fix) · 未合并main | P0 | 源②③: `routes.go:58` imageshttp ‖ **源①判MISSING(分歧)** | 合 main |
| PROTO-013 | `POST /v1/images/edits` | 🟡 | ✓ | ✓ | 已完成 (fix) · 未合并main | P1 | `routes.go:59` edits handler ‖ 源①判MISSING(分歧) | 合 main |
| PROTO-014 | `POST /v1/images/variations` | ✗ | 🟡 stub | ✗ | 已完成 (fix) · 未合并main | P2 | `routes.go:60` (**HUAKAI 独有 vs 三参考**) ‖ 源①判MISSING(分歧) | 合 main; 标差异化亮点 |
| PROTO-015 | `POST /v1/audio/speech` (TTS) | ✗ | ✓ | ✗ | 已完成 (fix) · 未合并main | P1 | `routes.go:61` audiohttp ‖ 源①判MISSING(分歧) | 合 main |
| PROTO-016 | `POST /v1/audio/transcriptions` (STT) | ✗ | ✓ | ✗ | 已完成 (fix) · 未合并main | P1 | `routes.go:62` ‖ 源①判MISSING(分歧) | 合 main |
| PROTO-017 | `POST /v1/audio/translations` | ✗ | ✓ | ✗ | 已完成 (fix) · 未合并main | P2 | `routes.go:63` ‖ 源①判MISSING(分歧) | 合 main |
| PROTO-018 | `GET /v1/models` listing | ✓ | ✓ | ✓ | 已完成 | — | `routes.go:67` controlhttp/modelhttp NewListHandler | 响应仅 4 字段, 见 MODEL-004 |
| PROTO-019 | `GET /v1/models/{model_id}` 单模型 | ✓ | ✓ | ✓ | 缺失 | P2 | grep `models/{`,`GetModel` → 仅 list handler | 补单模型详情端点 (OpenAI spec) |
| PROTO-020 | `GET /v1beta/models/*` (Gemini native generateContent) | ✓ | ✓ | ✓ | 缺失 | P2 | 无 /v1beta native ingress; 连带 5 个 Gemini→X client 方向缺 (H 段) | 新增 Gemini 客户端入站 adapter |
| PROTO-021 | `GET /v1/realtime` (WebSocket) | ✗ | 🟡 ws | ✗ | 部分完成 | P3 | `routes.go:66` 返回 `realtime_not_available` (path 存在/501 roadmap). 151: path-exists-returns-not_available → 部分完成 | 实时链路专项 (LiveSessionNode 已就绪) |
| PROTO-022 | `GET /v1/responses` (Responses WebSocket) | ✓ | 🟡 | ✓ | 缺失 | P3 | sub2api ResponsesWebSocket / cliproxy `openai_responses_websocket.go`; HUAKAI 无 | 随 realtime 一并评估 |
| PROTO-023 | `/v1internal:method`, `/backend-api/codex/responses` (Codex native ingress) | 🟡 透传 | ✓ codex channel | ✓ `server.go:406,431` | 部分完成 | P2 | ingress map 有, route 未挂载 (`gateway/protocol_selector.go`) | 挂载 codex native route |
| PROTO-024 | `/v1/generation` (cost lookup) | ✗ | ✗ | ✗ | 已完成 (fix) · 未合并main | — | `routes.go:76` (**HUAKAI 独有**) | 合 main; 差异化 |
| PROTO-025 | `GET /v1/me/usage` | 🟡 | 🟡 | ✗ | 已完成 | — | `routes.go:72` meusagehttp (**HUAKAI 独有深度**) | — |
| PROTO-026 | `GET /v1/me/analytics/time-series` | ✗ | ✗ | ✗ | 已完成 (fix) · 未合并main | — | `routes.go:80` (**HUAKAI 独有**) | 合 main |

---

## B. 媒体/任务平台端点 (MJ/Suno/Video) — 每平台 = 一行

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| MEDIA-001 | 通用媒体任务 `/v1/media-tasks`, `/v1/media-tasks/{id}` | ✗ | 🟡 | ✗ | 已完成 (fix) · 未合并main | — | `routes.go` mediataskhttp; 151-06-B: targeted test (**HUAKAI 通用任务, 非平台树**) | 合 main |
| MEDIA-002 | `/v1/generation` (媒体生成) | ✗ | ✓ | 🟡 | 部分完成 | P2 | 151-06-B 标 [部分]; generic media-task 路径 | 完善生成链 |
| MEDIA-003 | Midjourney `/mj/*` (15 actions: imagine/describe/blend/change/action/modal/shorten/swap-face/upload/video/edits/notify/fetch/image-seed/list) | ✗ | ✓ registerMjRouterGroup | ✗ | 缺失 | P2 | new-api 全套 MJ; HUAKAI ❌; MidjourneyRequest 11 字段 (F.5) 全缺 | 评估 MJ 平台任务树 (商业取决于市场) |
| MEDIA-004 | Suno `/suno/submit`, `/suno/fetch`, `/suno/fetch/:id` | ✗ | ✓ | ✗ | 缺失 | P3 | new-api SunoSubmitReq 9 字段 + GoAPI 变体 (F.5); HUAKAI ❌ | park |
| MEDIA-005 | Video submit/fetch (Kling/Jimeng/Vidu/Sora/Hailuo) | ✗ | ✓ `video-router.go` | 🟡 OpenAI/xAI videos only | 部分完成 | P2 | new-api 12 字段 VideoRequest; cliproxy OpenAI/xAI only; HUAKAI 仅 generic media-task | 平台 video adapter (按需) |
| MEDIA-006 | MJ swap-face `/mj/insight-face/swap` | ✗ | ✓ SwapFaceRequest | ✗ | 缺失 | P3 | sourceBase64/targetBase64; HUAKAI ❌ | park |

---

## C. Chat-completions 请求参数 (OpenAI 格式) — 每参数 = 一行

> HUAKAI: ✅typed=显式 struct 字段; 🟡passthrough=`PassthroughEnvelope.UnmarshalWithExtras` 两遍捕获再发射并带 field-matrix verdict (即"经透传保留", 非缺失). new-api 是 typed 字段覆盖数冠军.

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CPARM-001 | model | 🟡 | ✓ | ✓ | 已完成 | — | typed `openai_chat_types.go:8` | — |
| CPARM-002 | messages | 🟡 | ✓ | ✓ | 已完成 | — | typed `:9` | — |
| CPARM-003 | stream | 🟡 | ✓ | ✓ | 已完成 | — | typed `:10` | — |
| CPARM-004 | stream_options.include_usage | 🟡 | ✓ | 🟡 | 部分完成 | P3 | passthrough | 评估 typed 化 |
| CPARM-005 | stream_options.include_obfuscation | ✗ | ✓ (默认过滤) | ✗ | 部分完成 | P3 | passthrough (无 per-channel gate) | 见 CFG param-gate |
| CPARM-006 | max_tokens | 🟡 | ✓ | ✓ | 已完成 | — | typed `:11` | — |
| CPARM-007 | max_completion_tokens | 🟡 | ✓ | ✓ | 已完成 | — | typed `:12` | — |
| CPARM-008 | reasoning_effort | 🟡 | ✓ | ✓ codex | 部分完成 | P2 | passthrough, 映射到 reasoning capability; openai_chat ingress 尚未解析进 ThinkingNode (骨架③ content #14) | 解析 reasoning_effort→ThinkingNode |
| CPARM-009 | verbosity (gpt-5) | ✗ | ✓ raw | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-010 | temperature | 🟡 | ✓ | ✓ | 已完成 | — | typed `:13` | — |
| CPARM-011 | top_p | 🟡 | ✓ | ✓ | 已完成 | — | typed `:14` | — |
| CPARM-012 | top_k | ✗ | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| CPARM-013 | stop | 🟡 | ✓ | ✓ | 已完成 | — | typed `:15` (raw string/[]string) | — |
| CPARM-014 | n (multiple completions) | ✗ | ✓ | ✓ | 部分完成 | P2 | passthrough; 无 typed (计费需 cost×n, 负载需一账号多响应) | typed 化 + 计费倍数 |
| CPARM-015 | frequency_penalty | ✗ | ✓ | ✓ | 部分完成 | P3 | passthrough (无 per-channel override) | — |
| CPARM-016 | presence_penalty | ✗ | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| CPARM-017 | seed | ✗ | ✓ | ✓ | 已完成 | — | typed `:20` *int | — |
| CPARM-018 | response_format (type) | 🟡 | ✓ | ✓ | 已完成 | — | typed raw `:19` + structured capability | — |
| CPARM-019 | response_format.json_schema | 🟡 | ✓ | ✓ | 部分完成 | P1 | StructuredOutputNode.Schema (`capability_structured.go`); 但 openai_chat ingress D5 raw-passthrough 占位, 未填充 node (骨架③ content #13) | 完成 D5.x schema enforcement on openai_chat |
| CPARM-020 | tools | ✓ | ✓ | ✓ | 已完成 | — | typed `:16` + ToolUseNode | — |
| CPARM-021 | tools[].type (function/custom) | 🟡 | ✓ | ✓ | 已完成 | — | typed | — |
| CPARM-022 | tool_choice | ✓ | ✓ | ✓ | 已完成 | — | typed raw `:17` | — |
| CPARM-023 | parallel_tool_calls | 🟡 | ✓ | ✓ | 已完成 | — | typed `:18` + FeatureParallelToolCalls | — |
| CPARM-024 | functions (legacy) | ✗ | ✓ | 🟡 | 部分完成 | P3 | passthrough | — |
| CPARM-025 | function_call (legacy) | ✗ | ✓ | 🟡 | 部分完成 | P3 | passthrough | — |
| CPARM-026 | logprobs | ✗ | ✓ | 🟡 | 部分完成 | P3 | passthrough (无 typed 提取/过滤) | — |
| CPARM-027 | top_logprobs | ✗ | ✓ | 🟡 | 部分完成 | P3 | passthrough | — |
| CPARM-028 | logit_bias | ✗ | ✓ | 🟡 | 部分完成 | P3 | passthrough | — |
| CPARM-029 | user | 🟡 | ✓ | ✓ | 已完成 | — | typed `:21` | — |
| CPARM-030 | safety_identifier | ✗ | ✓ (默认过滤) | ✗ | 部分完成 | P3 | passthrough; 无 per-param gate | — |
| CPARM-031 | service_tier | 🟡 | ✓ (默认过滤) | ✗ | 部分完成 | P3 | passthrough; 无 gate | — |
| CPARM-032 | store | 🟡 | ✓ (默认允许) | 🟡 | 已完成 | — | typed `:22` *bool | — |
| CPARM-033 | metadata | 🟡 | ✓ | 🟡 | 已完成 | — | typed `:23` map | — |
| CPARM-034 | prompt_cache_key | ✓ | ✓ | 🟡 | 部分完成 | P3 | passthrough + cache capability | — |
| CPARM-035 | prompt_cache_retention | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-036 | prediction | ✗ | ✓ | 🟡 | 部分完成 | P3 | passthrough | — |
| CPARM-037 | modalities | 🟡 | ✓ | 🟡 | 部分完成 | P3 | passthrough | — |
| CPARM-038 | audio (output config) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-039 | web_search_options.search_context_size | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-040 | web_search_options.user_location | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-041 | prefix / suffix (FIM) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-042 | extra_body (gemini) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-043 | search_parameters (xai) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-044 | usage (openrouter) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-045 | reasoning (openrouter) | ✗ | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| CPARM-046 | vl_high_resolution_images (qwen) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-047 | enable_thinking (qwen) | 🟡 | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-048 | chat_template_kwargs (qwen) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-049 | enable_search (qwen) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-050 | think (ollama) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-051 | web_search (baidu v2) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-052 | thinking (doubao/zhipu_v4) | 🟡 | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-053 | search_domain_filter (pplx) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-054 | search_recency_filter (pplx) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-055 | return_images (pplx) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-056 | return_related_questions (pplx) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-057 | search_mode (pplx) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-058 | reasoning_split (minimax) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-059 | dimensions (embed-on-chat) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| CPARM-060 | encoding_format | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |

### C.1 消息内容子类型 (OpenAI 多模态 part) — 每类型 = 一行

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CPART-001 | text | 🟡 | ✓ | ✓ | 已完成 | — | TextNode | — |
| CPART-002 | image_url (+detail/+mime_type) | 🟡 | ✓ | ✓ | 已完成 | — | ImageNode{SourceKind,MediaType,Locator,Dimensions} `capability_image.go` | 注: 骨架① 标 vision PARTIAL=解析但 ProtocolLoss 未转发上游; 细树标 ✅ 数据模型. 真实转发上游需 MEDIA-fwd |
| CPART-003 | input_audio (data+format) | ✗ | ✓ | 🟡 | 已完成 | — | AudioNode `capability_audio.go` (4 transport mode) | 同 vision: 上游转发待补 |
| CPART-004 | file (filename/file_data/file_id) | ✗ | ✓ | 🟡 | 已完成 | — | FileNode `capability_file.go` | — |
| CPART-005 | video_url (Ali Bailian) | ✗ | ✓ | ✗ | 已完成 | — | VideoNode `capability_video.go` | 上游转发待补 |
| CPART-006 | cache_control (OpenRouter on part) | ✗ | ✓ | 🟡 | 已完成 | — | CacheControlNode | — |
| CPART-007 | message.reasoning_content / reasoning | 🟡 | ✓ | ✓ | 已完成 | — | ThinkingNode | — |
| CPART-008 | message.prefix (assistant prefill) | 🟡 | ✓ | 🟡 | 部分完成 | P3 | passthrough | — |
| CPART-009 | message.tool_calls / tool_call_id | ✓ | ✓ | ✓ | 已完成 | — | ToolUseNode/ToolResultNode | — |

---

## D. Anthropic Messages 请求参数 — 每参数 = 一行

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| APARM-001 | model | 🟡 | ✓ | ✓ | 已完成 | — | typed `anthropic_messages_types.go:12` | — |
| APARM-002 | max_tokens | 🟡 | ✓ | ✓ | 已完成 | — | typed `:13` | — |
| APARM-003 | max_tokens_to_sample (legacy) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| APARM-004 | messages | ✓ | ✓ | ✓ | 已完成 | — | typed `:14` | — |
| APARM-005 | system (string/array) | ✓ | ✓ | ✓ | 已完成 | — | typed raw `:15` + FeatureSystemPromptArray | — |
| APARM-006 | stream | 🟡 | ✓ | ✓ | 已完成 | — | typed `:16` | — |
| APARM-007 | temperature | 🟡 | ✓ | ✓ | 已完成 | — | typed `:17` | — |
| APARM-008 | top_p | ✗ | ✓ | ✓ | 已完成 | — | typed `:18` | — |
| APARM-009 | top_k | ✗ | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| APARM-010 | stop_sequences | 🟡 | ✓ | ✓ | 已完成 | — | typed `:19` | — |
| APARM-011 | tools | ✓ | ✓ | ✓ | 已完成 | — | typed []raw `:20` | — |
| APARM-012 | tool_choice (type/name/disable_parallel) | ✓ | ✓ | ✓ | 已完成 | — | typed raw `:21` | — |
| APARM-013 | thinking.type | ✓ | ✓ | ✓ | 已完成 | — | typed `:23` + ThinkingNode | — |
| APARM-014 | thinking.budget_tokens | ✓ | ✓ | ✓ | 已完成 | — | ThinkingNode.BudgetTokens | — |
| APARM-015 | thinking.display (summarized/omitted, Opus4.7) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| APARM-016 | metadata (user_id) | 🟡 | ✓ | 🟡 | 已完成 | — | typed map `:22` | — |
| APARM-017 | context_management | ✗ | ✓ | 🟡 | 部分完成 | P3 | passthrough | — |
| APARM-018 | output_config (+effort) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| APARM-019 | output_format | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| APARM-020 | container | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| APARM-021 | mcp_servers | ✗ | ✓ | 🟡 | 部分完成 | P2 | capability_mcp.go (schema only, 无活跃 MCP proxy handler) | 实现 MCP proxy runtime |
| APARM-022 | inference_geo (data residency) | ✗ | ✓ (默认过滤) | ✗ | 部分完成 | P3 | passthrough; 无 gate | — |
| APARM-023 | speed (inference speed mode) | ✗ | ✓ (默认过滤) | ✗ | 部分完成 | P3 | passthrough; 无 gate | — |
| APARM-024 | service_tier | 🟡 | ✓ (默认过滤) | ✗ | 部分完成 | P3 | passthrough; 无 gate | — |
| APARM-025 | cache_control (top-level + per-block) | ✓ | ✓ | 🟡 | 已完成 | — | CacheControlNode{Scope,BreakpointRefs,LocalityHint} | — |
| APARM-026 | cache_control.ttl (5m/1h ephemeral) | ✓ | ✓ | 🟡 | 已完成 | — | anthropicCacheControl.TTL `:52` | — |

### D.1 Anthropic 内容块类型 — 每块类型 = 一行

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| ABLK-001 | text | 🟡 | ✓ | ✓ | 已完成 | — | TextNode | — |
| ABLK-002 | image (source.type/media_type/data/url) | 🟡 | ✓ | ✓ | 已完成 | — | anthropicImageSource (url+base64) → ImageNode | — |
| ABLK-003 | tool_use (id/name/input) | ✓ | ✓ | ✓ | 已完成 | — | ToolUseNode (id remap `tool_call_id.go`) | — |
| ABLK-004 | tool_result (tool_use_id/content/is_error) | ✓ | ✓ | ✓ | 已完成 | — | ToolResultNode{IsError} | — |
| ABLK-005 | thinking (+signature) | ✓ | ✓ | ✓ | 已完成 | — | ThinkingNode{Signature} + signature_delta carry-forward | — |
| ABLK-006 | web_search_tool (max_uses/user_location) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| ABLK-007 | server_tool_use (web_search usage) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |

---

## E. OpenAI Responses 请求参数 — 每参数 = 一行

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| RPARM-001 | model | 🟡 | ✓ | ✓ | 已完成 | — | typed `openai_responses_types.go:6` | — |
| RPARM-002 | input (string/array) | 🟡 | ✓ | ✓ | 已完成 | — | typed raw `:7` | — |
| RPARM-003 | instructions | 🟡 | ✓ | ✓ | 已完成 | — | typed `:8` | — |
| RPARM-004 | stream | 🟡 | ✓ | ✓ | 已完成 | — | typed `:9` | — |
| RPARM-005 | max_output_tokens | 🟡 | ✓ | ✓ | 已完成 | — | typed `:10` | — |
| RPARM-006 | temperature | 🟡 | ✓ | ✓ | 已完成 | — | typed `:11` | — |
| RPARM-007 | top_p | ✗ | ✓ | ✓ | 已完成 | — | typed `:12` | — |
| RPARM-008 | top_logprobs | ✗ | ✓ | 🟡 | 部分完成 | P3 | passthrough | — |
| RPARM-009 | tools | ✓ | ✓ | ✓ | 已完成 | — | typed []raw `:13` | — |
| RPARM-010 | tool_choice | ✓ | ✓ | ✓ | 已完成 | — | typed raw `:14` | — |
| RPARM-011 | parallel_tool_calls | 🟡 | ✓ | ✓ | 已完成 | — | typed `:15` | — |
| RPARM-012 | max_tool_calls | ✗ | ✓ | 🟡 | 部分完成 | P3 | passthrough | — |
| RPARM-013 | text (format) | 🟡 | ✓ | ✓ | 已完成 | — | typed raw `:16` | — |
| RPARM-014 | reasoning.effort | ✓ | ✓ | ✓ | 已完成 | — | typed raw `:17` | — |
| RPARM-015 | reasoning.summary | ✓ | ✓ | ✓ | 已完成 | — | + FeatureReasoningSummary | — |
| RPARM-016 | include | 🟡 | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| RPARM-017 | conversation | ✗ | ✓ | 🟡 | 部分完成 | P3 | passthrough | — |
| RPARM-018 | context_management | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| RPARM-019 | previous_response_id | ✓ | ✓ | ✓ | 已完成 | — | typed `:20` | — |
| RPARM-020 | store | ✓ | ✓ | 🟡 | 已完成 | — | typed `:18` *bool | — |
| RPARM-021 | metadata | 🟡 | ✓ | 🟡 | 已完成 | — | typed map `:19` | — |
| RPARM-022 | prompt_cache_key | ✓ | ✓ | 🟡 | 部分完成 | P3 | passthrough | — |
| RPARM-023 | prompt_cache_retention | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| RPARM-024 | safety_identifier | ✗ | ✓ (过滤) | ✗ | 部分完成 | P3 | passthrough | — |
| RPARM-025 | service_tier | 🟡 | ✓ (过滤) | ✗ | 部分完成 | P3 | passthrough | — |
| RPARM-026 | truncation | ✗ | ✓ | 🟡 | 部分完成 | P3 | passthrough | — |
| RPARM-027 | user | 🟡 | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| RPARM-028 | prompt (template) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| RPARM-029 | enable_thinking (qwen) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| RPARM-030 | preset (perplexity) | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| RPARM-031 | input item: input_text/input_image/input_file | 🟡 | ✓ | ✓ | 已完成 | — | via canonical content blocks | — |
| RPARM-032 | reasoning item: encrypted_content/summary | ✗ | 🟡 | ✓ | 已完成 | — | openAIResponsesItem{EncryptedContent,Summary} `:27-28` | — |

---

## F. Gemini generateContent 请求参数 — 每参数 = 一行

> HUAKAI: Gemini 仅上游 (`gemini_messages` adapter), 无 Gemini 客户端入站, 故 client-side 参数 N/A; canonical→gemini upstream 映射在 dispatcher.

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 (gemini upstream) | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| GPARM-001 | contents | ✓ | ✓ | ✓ | 已完成 | — | canonical→contents | — |
| GPARM-002 | systemInstruction (camel+snake) | ✓ | ✓ | ✓ | 已完成 | — | — | — |
| GPARM-003 | safetySettings (category/threshold) | ✓ | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| GPARM-004 | cachedContent | 🟡 | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| GPARM-005 | tools (googleSearch/codeExecution/functionDeclarations/urlContext) | ✓ | ✓ | ✓ | 已完成 | — | tool nodes | — |
| GPARM-006 | toolConfig.functionCallingConfig.mode | ✓ | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| GPARM-007 | toolConfig.functionCallingConfig.allowedFunctionNames | ✗ | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| GPARM-008 | toolConfig.retrievalConfig.latLng/languageCode | ✗ | ✓ | 🟡 | 部分完成 | P3 | passthrough | — |
| GPARM-009 | toolConfig.includeServerSideToolInvocations | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| GPARM-010 | generationConfig.temperature | ✓ | ✓ | ✓ | 已完成 | — | — | — |
| GPARM-011 | generationConfig.topP | ✓ | ✓ | ✓ | 已完成 | — | — | — |
| GPARM-012 | generationConfig.topK | ✗ | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| GPARM-013 | generationConfig.maxOutputTokens | ✓ | ✓ | ✓ | 已完成 | — | — | — |
| GPARM-014 | generationConfig.candidateCount | ✗ | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| GPARM-015 | generationConfig.stopSequences (max5) | 🟡 | ✓ | ✓ | 已完成 | — | — | — |
| GPARM-016 | generationConfig.responseMimeType | 🟡 | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| GPARM-017 | generationConfig.responseSchema | 🟡 | ✓ | ✓ | 已完成 | — | structured capability | — |
| GPARM-018 | generationConfig.responseJsonSchema | ✗ | ✓ | 🟡 | 部分完成 | P3 | passthrough | — |
| GPARM-019 | generationConfig.presencePenalty | ✗ | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| GPARM-020 | generationConfig.frequencyPenalty | ✗ | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| GPARM-021 | generationConfig.responseLogprobs / logprobs | ✗ | ✓ | 🟡 | 部分完成 | P3 | passthrough | — |
| GPARM-022 | generationConfig.enableEnhancedCivicAnswers | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| GPARM-023 | generationConfig.mediaResolution | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| GPARM-024 | generationConfig.seed | ✗ | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| GPARM-025 | generationConfig.responseModalities | 🟡 | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| GPARM-026 | thinkingConfig.includeThoughts | ✓ | ✓ | ✓ | 已完成 | — | ThinkingNode | — |
| GPARM-027 | thinkingConfig.thinkingBudget | ✓ | ✓ | ✓ | 已完成 | — | ThinkingNode.BudgetTokens | — |
| GPARM-028 | thinkingConfig.thinkingLevel | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| GPARM-029 | generationConfig.speechConfig | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| GPARM-030 | generationConfig.imageConfig | ✗ | ✓ | ✗ | 部分完成 | P3 | passthrough | — |
| GPARM-031 | part: inlineData (mimeType+data) | ✓ | ✓ | ✓ | 已完成 | — | — | — |
| GPARM-032 | part: fileData | ✗ | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| GPARM-033 | part: functionCall (name/args) | ✓ | ✓ | ✓ | 已完成 | — | tool node | — |
| GPARM-034 | part: functionResponse (+willContinue/scheduling/parts/id) | ✓ | ✓ | ✓ | 已完成 | — | — | — |
| GPARM-035 | part: executableCode (language/code) | ✗ | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| GPARM-036 | part: codeExecutionResult (outcome) | ✗ | ✓ | ✓ | 部分完成 | P3 | passthrough | — |
| GPARM-037 | part: videoMetadata / mediaResolution | ✗ | ✓ | 🟡 | 部分完成 | P3 | passthrough | — |
| GPARM-038 | requests[] (batch) | ✗ | ✓ | ✗ | 缺失 | P3 | ❌ | — |

### F.1-F.6 其他端点请求参数 (embeddings/images/audio/rerank/video/realtime)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| EPARM-001 | embeddings: model/input | ✗ | ✓ | ✗ | 已完成 (fix) · 未合并main | P0 | embeddingshttp | 合 main |
| EPARM-002 | embeddings: encoding_format | ✗ | ✓ | ✗ | 已完成 (fix) · 未合并main | P1 | ✅ | — |
| EPARM-003 | embeddings: dimensions | ✗ | ✓ | ✗ | 已完成 (fix) · 未合并main | P1 | ✅ | — |
| EPARM-004 | embeddings: user | ✗ | ✓ | ✗ | 已完成 (fix) · 未合并main | — | ✅ | — |
| EPARM-005 | embeddings: ollama opts (seed/temp/topK/topP/freq/pres/numPredict/numCtx) | ✗ | ✓ | ✗ | 部分完成 | P3 | 🟡 | — |
| IPARM-001 | image: model/prompt/n/size | 🟡 | ✓ | ✓ | 已完成 (fix) · 未合并main | P0 | imageshttp | 合 main |
| IPARM-002 | image: quality | 🟡 | ✓ | ✓ | 已完成 (fix) · 未合并main | P1 | ✅ | — |
| IPARM-003 | image: response_format | 🟡 | ✓ | ✓ | 已完成 (fix) · 未合并main | P1 | ✅ | — |
| IPARM-004 | image: style | ✗ | ✓ | 🟡 | 部分完成 | P3 | 🟡 | — |
| IPARM-005 | image: background | ✗ | ✓ | ✗ | 部分完成 | P3 | 🟡 | — |
| IPARM-006 | image: moderation | ✗ | ✓ | ✗ | 部分完成 | P3 | 🟡 | — |
| IPARM-007 | image: output_format/output_compression | ✗ | ✓ | ✗ | 部分完成 | P3 | 🟡 | — |
| IPARM-008 | image: partial_images | ✗ | ✓ | ✗ | 部分完成 | P3 | 🟡 | — |
| IPARM-009 | image: input_fidelity | ✗ | ✓ | ✗ | 部分完成 | P3 | 🟡 | — |
| IPARM-010 | image: images/mask (edits) | ✗ | ✓ | 🟡 | 已完成 (fix) · 未合并main | P1 | edits handler | 合 main |
| IPARM-011 | image: watermark/watermark_enabled (zhipu) | ✗ | ✓ | ✗ | 部分完成 | P3 | 🟡 | — |
| IPARM-012 | image: user_id/image (zhipu) | ✗ | ✓ | ✗ | 部分完成 | P3 | 🟡 | — |
| IPARM-013 | image: arbitrary extra fields | ✗ | ✓ Extra map | 🟡 | 已完成 | — | passthrough | — |
| AUPARM-001 | audio: model/input/voice (speech) | ✗ | ✓ | ✗ | 已完成 (fix) · 未合并main | P1 | audiohttp speech | 合 main |
| AUPARM-002 | audio: instructions | ✗ | ✓ | ✗ | 已完成 (fix) · 未合并main | P2 | ✅ | — |
| AUPARM-003 | audio: response_format | ✗ | ✓ | ✗ | 已完成 (fix) · 未合并main | P2 | ✅ | — |
| AUPARM-004 | audio: speed | ✗ | ✓ | ✗ | 已完成 (fix) · 未合并main | P2 | ✅ | — |
| AUPARM-005 | audio: stream_format (sse) | ✗ | ✓ | ✗ | 部分完成 | P3 | 🟡 | — |
| AUPARM-006 | audio: vllm-omini (task_type/language/ref_audio/ref_text/x_vector/max_new_tokens/codec) | ✗ | ✓ (7 raw) | ✗ | 部分完成 | P3 | 🟡 | — |
| AUPARM-007 | audio: transcription/translation | ✗ | ✓ | ✗ | 已完成 (fix) · 未合并main | P1 | audiohttp transcription/translation | 合 main |
| RRPARM-001 | rerank: documents/query/model | ✗ | ✓ | ✗ | 缺失 | P2 | ❌ | 新建 rerank |
| RRPARM-002 | rerank: top_n | ✗ | ✓ | ✗ | 缺失 | P2 | ❌ | — |
| RRPARM-003 | rerank: return_documents | ✗ | ✓ | ✗ | 缺失 | P2 | ❌ | — |
| RRPARM-004 | rerank: max_chunk_per_doc | ✗ | ✓ | ✗ | 缺失 | P3 | ❌ | — |
| RRPARM-005 | rerank: overlap_tokens | ✗ | ✓ | ✗ | 缺失 | P3 | ❌ | — |
| VPARM-001 | video: model/prompt/image/duration/w/h/fps/seed/n/response_format/user/metadata (12) | ✗ | ✓ | 🟡 OpenAI only | 部分完成 | P2 | 🟡 generic media-task | 平台 video adapter |
| VPARM-002 | midjourney: 11 字段 (prompt/customId/botType/notifyHook/action/index/state/taskId/base64Array/content/maskBase64) | ✗ | ✓ | ✗ | 缺失 | P2 | ❌ | 见 MEDIA-003 |
| VPARM-003 | midjourney swap-face: sourceBase64/targetBase64 | ✗ | ✓ | ✗ | 缺失 | P3 | ❌ | — |
| VPARM-004 | suno: 9 字段 (gpt_description_prompt/prompt/mv/title/tags/continue_at/task_id/continue_clip_id/make_instrumental) | ✗ | ✓ | ✗ | 缺失 | P3 | ❌ | 见 MEDIA-004 |
| VPARM-005 | suno GoAPI: custom_mode/input/notify_hook | ✗ | ✓ | ✗ | 缺失 | P3 | ❌ | — |
| RTPARM-001 | realtime: modalities/instructions/voice | ✓ ws | ✓ | 🟡 | 部分完成 | P3 | 501 stub | 见 PROTO-021 |
| RTPARM-002 | realtime: input/output_audio_format | ✗ | ✓ | ✗ | 部分完成 | P3 | 501 stub | — |
| RTPARM-003 | realtime: input_audio_transcription.model | ✗ | ✓ | ✗ | 部分完成 | P3 | 501 stub | — |
| RTPARM-004 | realtime: turn_detection | ✗ | ✓ | ✗ | 部分完成 | P3 | 501 stub | — |
| RTPARM-005 | realtime: tools/tool_choice/temperature | ✗ | ✓ RealTimeTool | ✗ | 部分完成 | P3 | 501 stub | — |
| RTPARM-006 | realtime: 9 event types (session.update/response.create/input_audio_buffer.append/response.done/...) | 🟡 | ✓ (9 enum) | 🟡 | 部分完成 | P3 | 501 stub | — |

---

## G. 渠道/绑定配置字段 — 每字段 = 一行

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CFG-001 | base_url override | 🟡 | ✓ Channel.BaseURL | ✓ | 已完成 | — | provider catalog base_url | — |
| CFG-002 | model list (allowed models) | 🟡 | ✓ Channel.Models | ✓ | 已完成 | — | binding model set | — |
| CFG-003 | model_mapping (alias→upstream) | 🟡 | ✓ Channel.ModelMapping | 🟡 | 已完成 | — | ProviderModelIDOverride `registry.go` | — |
| CFG-004 | status_code_mapping | ✗ | ✓ | ✗ | 部分完成 | P3 | statuscode policy | — |
| CFG-005 | param_override (force-set params) | ✗ | ✓ ParamOverride + ApplyParamOverrideWithRelayInfo | ✗ | 缺失 | P2 | 无 per-channel param-override; 仅 model_rewrite | 加 per-channel param override |
| CFG-006 | header_override | ✗ | ✓ | 🟡 | 已完成 | — | headerfirewall + header override | — |
| CFG-007 | priority | 🟡 | ✓ | 🟡 | 已完成 | — | routing priority | — |
| CFG-008 | weight | 🟡 | ✓ | 🟡 | 已完成 | — | weighted routing (HRW) | — |
| CFG-009 | tag (grouping) | 🟡 | ✓ | ✗ | 已完成 | — | ✅ | — |
| CFG-010 | group | ✓ | ✓ | 🟡 | 已完成 | — | tenant/group | — |
| CFG-011 | auto_ban | ✗ | ✓ | ✗ | 已完成 | — | channelhealth circuit-breaker | — |
| CFG-012 | test_model | 🟡 | ✓ | 🟡 | 已完成 | — | health probe | — |
| CFG-013 | openai_organization | ✗ | ✓ | ✗ | 部分完成 | P3 | 🟡 | — |
| CFG-014 | setting.force_format | ✗ | ✓ | ✗ | 部分完成 | P3 | 🟡 | — |
| CFG-015 | setting.thinking_to_content | ✓ | ✓ | 🟡 | 部分完成 | P3 | 🟡 | — |
| CFG-016 | setting.proxy | 🟡 | ✓ | ✓ | 已完成 | — | transport proxy | — |
| CFG-017 | setting.pass_through_body_enabled | ✓ | ✓ | 🟡 | 已完成 | — | passthrough envelope (always-on) | — |
| CFG-018 | setting.system_prompt / override | ✓ | ✓ | 🟡 | 部分完成 | P2 | system_rewrite | per-channel system prompt config |
| CFG-019 | other.azure_responses_version | ✗ | ✓ | ✗ | 部分完成 | P3 | 🟡 | — |
| CFG-020 | other.vertex_key_type (json/api_key) | ✓ | ✓ | 🟡 | 部分完成 | P3 | 🟡 | — |
| CFG-021 | other.aws_key_type (ak_sk/api_key) | ✓ | ✓ | ✗ | 部分完成 | P3 | bedrock creds | — |
| CFG-022 | other.openrouter_enterprise | ✗ | ✓ | ✗ | 缺失 | P3 | ❌ | — |
| CFG-023 | other.claude_beta_query (?beta=true) | 🟡 | ✓ | ✗ | 部分完成 | P3 | 🟡 | — |
| CFG-024 | other.allow_service_tier (param gate) | ✗ | ✓ | ✗ | 缺失 | P2 | ❌ (passthrough always) | 见 G.1 param-gate |
| CFG-025 | other.allow_inference_geo | ✗ | ✓ | ✗ | 缺失 | P2 | ❌ | 见 G.1 |
| CFG-026 | other.allow_speed | ✗ | ✓ | ✗ | 缺失 | P2 | ❌ | 见 G.1 |
| CFG-027 | other.allow_safety_identifier | ✗ | ✓ | ✗ | 缺失 | P2 | ❌ | 见 G.1 |
| CFG-028 | other.disable_store | ✗ | ✓ | ✗ | 缺失 | P3 | ❌ | 见 G.1 |
| CFG-029 | other.allow_include_obfuscation | ✗ | ✓ | ✗ | 缺失 | P3 | ❌ | 见 G.1 |
| CFG-030 | other.upstream_model_update (check/auto_sync/last_check/detected/removed/ignored, 6字段) | ✗ | ✓ (6) | 🟡 model_updater | 已完成 | — | modelsync `modelsync/service.go` | — |
| CFG-031 | multi-key mode (is_multi_key/size/status_list/polling_index/mode, 6字段) | ✓ | ✓ ChannelInfo (6) | 🟡 | 已完成 | — | credentialstore multi-cred | — |

### G.1 参数门控 / 禁用字段剥离 — 每门控参数 = 一行 (new-api 独有隐私/计费安全能力)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| GATE-001 | service_tier gate (unless AllowServiceTier) | ✗ | ✓ | ✗ | 缺失 | P2 | 始终透传无 gate | 加 opt-in param gating 框架 |
| GATE-002 | inference_geo gate | ✗ | ✓ | ✗ | 缺失 | P2 | 始终透传 | — |
| GATE-003 | speed gate | ✗ | ✓ | ✗ | 缺失 | P2 | 始终透传 | — |
| GATE-004 | safety_identifier gate | ✗ | ✓ | ✗ | 缺失 | P2 | 始终透传 | — |
| GATE-005 | store gate (DisableStore) | ✗ | ✓ | ✗ | 缺失 | P3 | typed 但无 gate | — |
| GATE-006 | stream_options.include_obfuscation gate | ✗ | ✓ | ✗ | 缺失 | P3 | 始终透传 | — |

---

## H. 协议转换 — 每跨格式对×方向 = 一行 (Juice 路由透明核心)

> cliproxy 注册具体 NxN 矩阵 (`translator/init.go`): 源格式 claude/codex/gemini/gemini-cli/openai/antigravity × 目标 claude/gemini/gemini-cli/openai-chat/openai-responses. new-api 经 GeneralOpenAIRequest hub. HUAKAI 用 HCSF canonical: 3 client (openai_chat/openai_responses/anthropic_messages) ↔ canonical ↔ N upstream.

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CONV-001 | OpenAI-chat req → canonical | 🟡 native | ✓ hub | ✓ | 已完成 | — | `proto.OpenAIChatClient` RequestToCanonical | — |
| CONV-002 | canonical → OpenAI-chat resp (stream) | 🟡 | ✓ | ✓ | 已完成 | — | openai_chat_stream.go | — |
| CONV-003 | canonical → OpenAI-chat resp (nonstream) | 🟡 | ✓ | ✓ | 已完成 | — | openai_chat_response.go | — |
| CONV-004 | OpenAI-chat → OpenAI-responses (req) | 🟡 | ✓ | ✓ | 已完成 | — | both client adapters | — |
| CONV-005 | OpenAI-responses → OpenAI-chat (resp) | 🟡 | ✓ | ✓ | 已完成 | — | ✅ | — |
| CONV-006 | Anthropic-msg req → OpenAI-chat | ✓ | ✓ | ✓ | 已完成 | — | anthropic client→HCSF→openai upstream | — |
| CONV-007 | OpenAI-chat resp → Anthropic-msg (stream) | ✓ | ✓ | ✓ | 已完成 | — | anthropic_messages_response_stream.go | — |
| CONV-008 | OpenAI-chat resp → Anthropic-msg (nonstream) | ✓ | ✓ | ✓ | 已完成 | — | anthropic_messages_response.go | — |
| CONV-009 | Anthropic-msg req → OpenAI-responses | 🟡 | 🟡 hub | ✓ | 已完成 | — | anthropic→HCSF→responses | — |
| CONV-010 | Anthropic-msg req → Gemini | 🟡 | 🟡 hub | ✓ | 部分完成 | P2 | anthropic→HCSF→gemini upstream | 补 gemini lossy 校验 |
| CONV-011 | Anthropic-msg req → Gemini-CLI | ✗ | ✗ | ✓ | 缺失 | P3 | ❌ | gemini-cli 生态, park |
| CONV-012 | OpenAI-chat req → Anthropic-msg | ✓ | ✓ | ✓ | 已完成 | — | openai→HCSF→anthropic | — |
| CONV-013 | OpenAI-chat req → Gemini | 🟡 | ✓ | ✓ | 部分完成 | P2 | canonical→gemini upstream | — |
| CONV-014 | OpenAI-chat req → Gemini-CLI | ✗ | ✗ | ✓ | 缺失 | P3 | ❌ | park |
| CONV-015 | Gemini req → OpenAI-chat | ✓ | ✓ | ✓ | 缺失 | P2 | ❌ (无 gemini client ingress, 连带 PROTO-020) | 加 Gemini 客户端入站 |
| CONV-016 | Gemini req → OpenAI-responses | ✗ | 🟡 | ✓ | 缺失 | P3 | ❌ | — |
| CONV-017 | Gemini req → Anthropic-msg | ✓ | 🟡 hub | ✓ | 缺失 | P3 | ❌ | — |
| CONV-018 | Gemini ↔ Gemini-CLI | ✓ | ✗ | ✓ | 缺失 | P3 | ❌ | park |
| CONV-019 | Gemini → Gemini (passthrough/normalize) | ✓ | ✓ | ✓ | 已完成 | — | gemini upstream passthrough | — |
| CONV-020 | Gemini-CLI req → OpenAI-chat | ✗ | ✗ | ✓ | 缺失 | P3 | ❌ | park |
| CONV-021 | Gemini-CLI req → OpenAI-responses | ✗ | ✗ | ✓ | 缺失 | P3 | ❌ | park |
| CONV-022 | Gemini-CLI req → Anthropic-msg | ✗ | ✗ | ✓ | 缺失 | P3 | ❌ | park |
| CONV-023 | Gemini-CLI → Gemini | ✓ | ✗ | ✓ | 缺失 | P3 | ❌ | park |
| CONV-024 | Codex req → OpenAI-chat | ✓ | ✓ | ✓ | 部分完成 | P2 | openai_codex reuses openai.Adapter | — |
| CONV-025 | Codex req → OpenAI-responses | ✓ | ✓ | ✓ | 部分完成 | P2 | 🟡 reuses openai.Adapter | — |
| CONV-026 | Codex req → Anthropic-msg | ✓ | 🟡 | ✓ | 缺失 | P3 | ❌ | park |
| CONV-027 | Codex req → Gemini | ✗ | ✗ | ✓ | 缺失 | P3 | ❌ | park |
| CONV-028 | Codex req → Gemini-CLI | ✗ | ✗ | ✓ | 缺失 | P3 | ❌ | park |
| CONV-029 | Antigravity req → OpenAI-chat | ✓ | ✗ | ✓ | 部分完成 | P2 | antigravity_session reuses openai.Adapter (OCAW stub 趋势) | 完善 antigravity adapter |
| CONV-030 | Antigravity req → OpenAI-responses | ✓ | ✗ | ✓ | 部分完成 | P2 | 🟡 reuses openai.Adapter | — |
| CONV-031 | Antigravity req → Anthropic-msg | ✓ | ✗ | ✓ | 缺失 | P3 | ❌ | park |
| CONV-032 | Antigravity req → Gemini | 🟡 | ✗ | ✓ | 缺失 | P3 | ❌ | park |
| CONV-033 | AWS Bedrock EventStream (binary) ↔ canonical | ✗ | ✓ | ✗ | 已完成 | — | `proto/bedrock/eventstream.go` (Bedrock-on-Anthropic) | — |
| CONV-034 | Token-count 跨格式翻译 (count_tokens) | ✓ | 🟡 | ✓ | 缺失 | P2 | ❌ count_tokens 未挂载 (连带 PROTO-006) | 挂 count_tokens + 跨格式 |

---

## I. 上游协议族 (每注册族 = 一行)

> HUAKAI `gateway/protocol_selector.go BuildDefaultProtocolAdapterRegistry` 注册 **20 族** (比粗审"12"多). new-api ~40 vendor adaptor. **OCAW 提醒**: antigravity/kiro/windsurf/cursor/copilot session adapter 当前复用 openai.Adapter 即 stub/TODO 趋势 (151).

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| FAM-001 | anthropic (messages) | ✓ | ✓ claude | ✓ claude | 已完成 | — | `anthropic_messages` → anthropic.Adapter | — |
| FAM-002 | openai chat | ✓ | ✓ openai | ✓ openai | 已完成 | — | `openai_chat` → openai.Adapter | — |
| FAM-003 | openai responses | ✓ | ✓ openai | ✓ openai-response | 已完成 | — | `openai_responses` → openai.Adapter | — |
| FAM-004 | openai codex (ChatGPT backend) | ✓ | ✓ codex | ✓ codex | 已完成 | — | `openai_codex` → openai.Adapter | — |
| FAM-005 | gemini | ✓ | ✓ gemini/vertex | ✓ gemini | 已完成 | — | `gemini_messages` → gemini.Adapter | — |
| FAM-006 | AWS Bedrock | ✗ | ✓ aws | ✗ | 已完成 | — | `bedrock_invoke` → bedrock.EventStreamAdapter | — |
| FAM-007 | openrouter | ✓ | ✓ openrouter | 🟡 | 已完成 | — | `openrouter_chat` → openai.Adapter | — |
| FAM-008 | grok / xai | ✓ | ✓ xai | 🟡 | 已完成 | — | `grok_chat` → openai.Adapter | — |
| FAM-009 | deepseek | ✓ | ✓ deepseek | 🟡 | 已完成 | — | `deepseek_chat` → openai.Adapter | — |
| FAM-010 | mistral | ✓ | ✓ mistral | 🟡 | 已完成 | — | `mistral_chat` → openai.Adapter | — |
| FAM-011 | groqcloud | ✗ | ✗ | 🟡 | 已完成 | — | `groqcloud_chat` → openai.Adapter | — |
| FAM-012 | together | ✗ | ✗ | 🟡 | 已完成 | — | `together_chat` → openai.Adapter | — |
| FAM-013 | perplexity | ✓ | ✓ perplexity | 🟡 | 已完成 | — | `perplexity_chat` → openai.Adapter | — |
| FAM-014 | fireworks | ✗ | ✗ | 🟡 | 已完成 | — | `fireworks_chat` → openai.Adapter | — |
| FAM-015 | github copilot | ✗ | ✗ | ✓ (auth) | 部分完成 | P2 | `copilot_session` → openai.Adapter (OCAW: 复用 openai, 非原生 adapter) | 实现原生 copilot adapter |
| FAM-016 | gemini advanced (internal SSE) | ✗ | ✗ | 🟡 | 部分完成 | P2 | `gemini_advanced_session` → gemini.Adapter | — |
| FAM-017 | cursor | ✗ | ✗ | ✗ | 部分完成 | P2 | `cursor_session` → openai.Adapter (OCAW TODO/stub per 151) | 原生 cursor adapter |
| FAM-018 | antigravity | ✗ | ✗ | ✓ | 部分完成 | P2 | `antigravity_session` → openai.Adapter (OCAW TODO/stub per 151) | 原生 antigravity adapter |
| FAM-019 | kiro | ✗ | ✗ | ✗ | 部分完成 | P2 | `kiro_session` → openai.Adapter (OCAW TODO/stub per 151) | 原生 kiro adapter |
| FAM-020 | windsurf | ✗ | ✗ | ✗ | 部分完成 | P2 | `windsurf_session` → openai.Adapter (OCAW TODO/stub per 151) | 原生 windsurf adapter |
| FAM-021 | ali/baidu/tencent/zhipu/moonshot/minimax/cohere/ollama/360/xunfei (~25 more) | ✗ | ✓ (one each) | ✗ | 缺失 | P3 | ❌ (非独立族; OpenAI-compat 复用) | 按需补独立族 |
| FAM-022 | midjourney/suno/jimeng/replicate/coze/dify (task adaptors) | ✗ | ✓ | ✗ | 缺失 | P3 | ❌ | 见 MEDIA-003/004 |

---

## J. 能力节点 / canonical-model 特性 (HUAKAI HCSF) — 每节点+字段 = 一行

> HUAKAI 是唯一带形式化 canonical envelope 与 typed capability node 的代码库; refs 无对应 (临时翻译). 此为 HUAKAI 协议建模深度差异化亮点.

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CAP-001 | TextNode (role, block) | 🟡 | ✓ | ✓ | 已完成 | — | capability_text.go | — |
| CAP-002 | ThinkingNode.budget_tokens | ✓ | ✓ | ✓ | 已完成 | — | capability_thinking.go:16 | — |
| CAP-003 | ThinkingNode.blocks | 🟡 | ✓ | ✓ | 已完成 | — | :19 | — |
| CAP-004 | ThinkingNode.hidden_tokens | ✗ | 🟡 | ✗ | 已完成 | — | :22 | — |
| CAP-005 | ThinkingNode.signature | ✓ | ✓ | ✓ | 已完成 | — | :25 | — |
| CAP-006 | ThinkingNode.redaction (RedactionClass) | ✗ | ✗ | ✗ | 已完成 | — | :28 (**HUAKAI 独有**) | — |
| CAP-007 | ToolUseNode.tool_call_id + original (remap) | 🟡 | 🟡 | ✓ id repair | 已完成 | — | capability_tool.go:18,21 + tool_call_id.go | — |
| CAP-008 | ToolUseNode.display_name | ✗ | ✗ | ✗ | 已完成 | — | :27 (**HUAKAI 独有**) | — |
| CAP-009 | ToolUseNode.partial_input (streaming) | ✗ | 🟡 | ✓ | 已完成 | — | :33 | — |
| CAP-010 | ToolUseNode.status (ToolNodeStatus) | ✗ | ✗ | ✗ | 已完成 | — | :36 (**HUAKAI 独有**) | — |
| CAP-011 | ToolResultNode.is_error | 🟡 | ✓ | ✓ | 已完成 | — | :51 | — |
| CAP-012 | CacheControlNode.scope (CacheScope enum) | ✓ | 🟡 | 🟡 | 已完成 | — | capability_cache.go:20 | — |
| CAP-013 | CacheControlNode.breakpoint_refs | ✗ | 🟡 | ✗ | 已完成 | — | :23 | — |
| CAP-014 | CacheControlNode.cache_key_hint | ✓ | ✓ | ✗ | 已完成 | — | :26 | — |
| CAP-015 | CacheControlNode.cache_creation/read_input_tokens | ✓ | ✓ | 🟡 | 已完成 | — | :29,32 | — |
| CAP-016 | CacheControlNode.sanitize_system_metadata (anti cache-bust) | ✗ | ✗ | ✗ | 已完成 | — | :36 (**HUAKAI 独有**) | — |
| CAP-017 | CacheControlNode.locality_hint | ✗ | ✗ | ✗ | 已完成 | — | :40 (**HUAKAI 独有**) | — |
| CAP-018 | StructuredOutputNode (mode/strict/schema/parser_mode/failure_recovery/fallback_strategy, 6字段) | 🟡 | 🟡 | 🟡 | 部分完成 | P1 | capability_structured.go; openai_chat ingress 尚未填充 (CPARM-019) | 完成 openai_chat schema 填充 |
| CAP-019 | ImageNode (source_kind/media_type/locator/dimensions) | 🟡 | ✓ | ✓ | 已完成 | — | capability_image.go | 上游转发待补 (CPART-002) |
| CAP-020 | VideoNode | ✗ | ✓ | 🟡 | 已完成 | — | capability_video.go | — |
| CAP-021 | AudioNode | ✗ | ✓ | 🟡 | 已完成 | — | capability_audio.go | — |
| CAP-022 | FileNode | ✗ | ✓ | 🟡 | 已完成 | — | capability_file.go | — |
| CAP-023 | BatchNode | ✗ | ✗ | ✗ | 部分完成 | P2 | capability_batch.go (schema only); 无 `/v1/batches` handler/queue/poll | 实现 batch 端点+队列+状态轮询 |
| CAP-024 | ComputerUseNode | ✗ | ✗ | ✗ | 部分完成 | P3 | capability_computer_use.go (schema only); 无 HTTP handler | runtime 待补 |
| CAP-025 | McpNode | ✗ | 🟡 raw | 🟡 | 部分完成 | P2 | capability_mcp.go (schema); 无活跃 MCP proxy | MCP proxy runtime |
| CAP-026 | LiveNode | ✗ | ✗ | ✗ | 部分完成 | P3 | capability_live.go (schema; runtime roadmap, 连带 realtime) | 见 PROTO-021 |
| CAP-027 | GraphNode | ✗ | ✗ | ✗ | 已完成 | — | capability_graph.go (14 capability kind enum) | — |
| CAP-028 | DataRetentionNode (ZDR) | ✗ | ✗ | ✗ | 部分完成 | P2 | capability_data_retention.go (tracking only, 无主动 enforcement/ZDR 合约校验) | 加 enforcement |

### J.1 能力矩阵裁决 (HUAKAI `capability_matrix.go`) — 每特性 flag = 一行

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| MX-001 | text_streaming | 🟡 | 🟡 | 🟡 | 已完成 | — | enumerated (Preserved/Lossy/Unsupported verdict) | — |
| MX-002 | tool_use | 🟡 | 🟡 | 🟡 | 已完成 | — | ✅ | — |
| MX-003 | reasoning_summary | ✗ | 🟡 | 🟡 | 已完成 | — | ✅ | — |
| MX-004 | parallel_tool_calls | ✗ | 🟡 | 🟡 | 已完成 | — | ✅ | — |
| MX-005 | structured_output_schema | ✗ | 🟡 | 🟡 | 已完成 | — | ✅ | — |
| MX-006 | image_input/audio_input/image_output | 🟡 | 🟡 | 🟡 | 已完成 | — | ✅ | — |
| MX-007 | max_tokens_finish_reason | ✗ | ✗ | ✗ | 已完成 | — | ✅ (**HUAKAI 独有**) | — |
| MX-008 | max_completion_tokens | ✗ | 🟡 | ✗ | 已完成 | — | ✅ | — |
| MX-009 | stop_sequence_emit | ✗ | ✗ | ✗ | 已完成 | — | ✅ | — |
| MX-010 | cache_breakpoints | ✗ | 🟡 | ✗ | 已完成 | — | ✅ | — |
| MX-011 | signature_delta | ✗ | 🟡 | ✗ | 已完成 | — | ✅ | — |
| MX-012 | system_prompt_array | ✗ | 🟡 | 🟡 | 已完成 | — | ✅ | — |
| MX-013 | multi_role_messages | 🟡 | 🟡 | 🟡 | 已完成 | — | ✅ | — |

### J.2 字段级损耗记账 (HUAKAI 独有) — 每 verdict 类型 = 一行

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| LOSS-001 | FieldVerdict: preserved | ✗ | ✗ | ✗ | 已完成 | — | field_matrix.go:26 (**HUAKAI 独有**) | — |
| LOSS-002 | FieldVerdict: transformed (+TransformKind lossy/lossless) | ✗ | ✗ | ✗ | 已完成 | — | :29,60 | — |
| LOSS-003 | FieldVerdict: dropped | ✗ | ✗ | ✗ | 已完成 | — | :32 | — |
| LOSS-004 | FieldVerdict: preserved_default | ✗ | ✗ | ✗ | 已完成 | — | :36 | — |
| LOSS-005 | ProtocolLossEntry (severity info/warning/error + direction + verdict) | ✗ | ✗ | ✗ | 已完成 | — | protocol_loss.go:11-67 (**HUAKAI 独有差异化**) | — |
| LOSS-006 | ProjectionVerdict (preserved / native_required) | ✗ | ✗ | ✗ | 已完成 | — | projection.go:8 + envelope_validate.go:460 | — |
| LOSS-007 | PassthroughEnvelope (两遍 unknown-field 捕获再发射) | 🟡 raw forward | 🟡 Extra only | 🟡 gjson in-place | 已完成 | — | passthrough.go UnmarshalWithExtras/MergeExtrasInto | — |

---

## K. 模型注册 / 同步 / fallback / alias — 每子能力 = 一行 (模型接入 + Juice 透明)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| REG-001 | Model alias → canonical resolution | 🟡 | ✓ ModelMapping | 🟡 | 已完成 | — | registry.ResolveModel (alias→model→bindings→snapshot) | — |
| REG-002 | Alias normalization (NFC + lowercase) | ✗ | ✗ | ✗ | 已完成 | — | normalize.go AliasNormalize (**HUAKAI 独有**) | — |
| REG-003 | Tenant + global alias (inherit_global_catalog) | ✗ | ✗ | ✗ | 已完成 | — | registry.go | — |
| REG-004 | Display-casing preserved | ✗ | ✗ | ✗ | 已完成 | — | ✅ | — |
| REG-005 | Per-binding provider-model-id override | 🟡 | ✓ | ✗ | 已完成 | — | ProviderModelIDOverride | — |
| REG-006 | Outbound body model rewrite (JSON + multipart) | 🟡 | ✓ | ✓ sjson | 已完成 | — | relaybody/model_rewrite.go (only-if-different) | — |
| REG-007 | Auto-sync from upstream /v1/models | ✗ | ✓ CDN models.json | ✓ GitHub models.json 3h | 已完成 | — | modelsync/service.go (live /v1/models, SSRF allowlist, paginate, atomic) | — |
| REG-008 | Sync scheduler | ✗ | ✓ | ✓ 3h | 已完成 | — | modelsync/scheduler.go | — |
| REG-009 | Admin model-sync trigger | ✗ | ✓ | ✓ mgmt api | 已完成 | — | /admin/v1/model-sync | — |
| REG-010 | Model capabilities (jsonb, max_output_tokens, model_mode) | 🟡 | ✓ | ✓ ModelInfo | 已完成 | — | registry/model_capabilities.go + 15 node types | — |
| REG-011 | Admin model capability edit | 🟡 | ✓ | ✓ | 部分完成 | P2 | PUT `/v1/admin/models/{id}/capabilities` (151-06-L 标 [部分]) | 前端 UI |
| REG-012 | Model-level fallback chain (cross-model by error class) | 🟡 | ✓ channel retry | ✗ | 已完成 | — | modelfallback/resolver.go (general/context_window/content_policy, wildcard, max_depth) (**opt-in, exactly-once**) | — |
| REG-013 | Channel/account failover (same model) | ✓ | ✓ | 🟡 | 已完成 | — | router + channelhealth | — |
| REG-014 | Model discovery projection (GET /v1/models shape) | ✓ | ✓ | ✓ | 已完成 | — | registry/models_list.go | — |

### K.1 模型目录 / 定价 / 能力 元数据 — 来自骨架① model-catalog-pricing — 每项 = 一行

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| MODEL-001 | Models table canonical identity | 🟡 | ✓ | 🟡 | 已完成 | — | `0008_model_registry.up.sql:56-91` | — |
| MODEL-002 | `GET /v1/models` 端点 | ✓ | ✓ | ✓ | 已完成 | — | `routes.go:67`; list_handler.go | 响应仅 4 字段 (MODEL-004) |
| MODEL-003 | `GET /v1/models/{id}` 单模型 | ✓ | ✓ | ✓ | 缺失 | P2 | 仅 list handler (=PROTO-019) | 补单模型端点 |
| MODEL-004 | list 响应富元数据 (context_window/caps/pricing/desc) | ✓ | ✓ | ✓ | 缺失 | P1 | list_handler.go:30-37 仅 id/object/created/owned_by | 扩充 list 响应 |
| MODEL-005 | Admin CRUD 模型 (create/update/delete) | ✓ | ✓ | ✓ | 缺失 | P1 | 仅 /admin/v1/model-sync; 无 CreateModel | 加 admin 模型 CRUD |
| MODEL-006 | Admin CRUD list (full detail) | ✓ | ✓ | ✓ | 缺失 | P2 | 无 admin GET /models | — |
| MODEL-007 | Vendor 自动同步 (=REG-007) | ✗ | ✓ | ✓ | 已完成 | — | modelsync | — |
| MODEL-008 | Registry snapshots (versioned audit replay) | ✗ | ✗ | ✗ | 已完成 | — | `0008:24-32` (**HUAKAI 独有 D6**) | — |
| MODEL-009 | Tenant vs global catalog scoping | ✗ | ✗ | ✗ | 已完成 | — | `0008:99-125,41-48` | — |
| MODEL-010 | Model status lifecycle (active/disabled/deleted) | ✓ | ✓ | 🟡 | 部分完成 | P2 | `0008:72-74` 缺 deprecated/sunset/migration_path/replacement | 加 deprecation 状态 |
| MODEL-011 | Model deprecation lifecycle (sunset/migration hints) | ✓ | ✓ | ✗ | 缺失 | P1 | grep deprecated/sunset → 0 | 加 EOL 工作流 |
| MODEL-012 | Model grouping / family / suite | ✓ | ✓ | ✗ | 缺失 | P2 | grep ModelGroup/model_family → 0 | 加 family 分组 |
| MODEL-013 | Model tier classification (premium/standard/free/exp) | ✓ | ✓ | 🟡 | 部分完成 | P3 | pricing_class 自由标签, 无 tier 语义 | tier 语义化 |
| MODEL-014 | Tenant-scoped aliases (normalized lookup, =REG-001/002) | 🟡 | ✓ | 🟡 | 已完成 | — | `0008:99-125`; registry.sql:8-24 | — |
| MODEL-015 | Global alias catalog (inheritance) | ✗ | ✗ | ✗ | 已完成 | — | registry.sql:26-40 | — |
| MODEL-016 | Alias status mgmt (active/disabled/deleted) | ✓ | ✓ | 🟡 | 已完成 | — | `0008:108-112` | — |
| MODEL-017 | Alias deprecation/sunset/migration hints | ✓ | ✓ | ✗ | 缺失 | P2 | grep → 0 | 加 alias EOL 提示 |
| MODEL-018 | Alias fallback chain (alias-level) | ✓ | ✓ | ✗ | 缺失 | P3 | pool-binding 级有 fallback_class, alias 级无 | — |
| MODEL-019 | Alias bulk import (CSV/JSON) | ✓ | ✓ | ✗ | 缺失 | P2 | 仅 vendor sync | 加 bulk import |

### K.2 定价 / 成本 / token 计数 — 来自骨架① — 每项 = 一行

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| PRICE-001 | Versioned rate table (billing_pricing_versions) | ✓ | ✓ | ✗ | 已完成 | — | `0002:277-291`; rate_table_source.go | — |
| PRICE-002 | Per-model input token pricing | ✓ | ✓ | ✗ | 已完成 | — | chat_completions_pricing.go:221 (多 alias) | — |
| PRICE-003 | Per-model output token pricing | ✓ | ✓ | ✗ | 已完成 | — | :225 | — |
| PRICE-004 | Cache read pricing | ✓ | ✓ | ✗ | 已完成 | — | :241 | — |
| PRICE-005 | Cache creation pricing (5m/1h ephemeral tiers) | ✓ | 🟡 | ✗ | 已完成 | — | :229-237 (Anthropic 正确建模) | — |
| PRICE-006 | Per-model pricing multiplier / markup | ✓ | ✓ | ✗ | 已完成 | — | :245-248 model_multiplier | 无 per-tenant/user override |
| PRICE-007 | Batch API pricing tier (~50% off) | ✓ | ✓ | ✗ | 缺失 | P1 | CapabilityBatch 有但无 rate (=CAP-023) | 加 batch 定价 |
| PRICE-008 | Reasoning/thinking token separate pricing | ✓ | ✓ | ✗ | 缺失 | P1 | ReasoningTokens 追踪但并入 output 计费; grep reasoning_micro → 0 | 加 reasoning 独立定价 (系统性误计费) |
| PRICE-009 | Image output token pricing | ✓ | ✓ | ✗ | 缺失 | P2 | image_output_tokens 追踪但 rate=0 (永远免费) | 配 image rate |
| PRICE-010 | Audio token pricing | ✓ | ✓ | ✗ | 缺失 | P2 | grep audio_micro → 0 | — |
| PRICE-011 | Video token pricing | ✗ | 🟡 | ✗ | 缺失 | P3 | grep video_micro → 0 | — |
| PRICE-012 | Fine-tuned model custom pricing | ✓ | ✓ | ✗ | 缺失 | P3 | grep → 0 | — |
| PRICE-013 | Regional/geo pricing | ✗ | 🟡 | ✗ | 缺失 | P3 | grep → 0 | — |
| PRICE-014 | Volume/commitment discount tiers | ✓ | 🟡 | ✗ | 缺失 | P2 | grep → 0 | 加 volume breakpoint |
| PRICE-015 | Per-tenant custom pricing | 🟡 | ✓ | ✗ | 部分完成 | P2 | tenant_id 支持但无 admin 写 API (须直接 DB insert) | 加写 API (=PRICE-018) |
| PRICE-016 | Per-user/segment pricing | ✗ | 🟡 | ✗ | 缺失 | P3 | grep → 0 | — |
| PRICE-017 | Time-based pricing (peak/off-peak) | ✗ | ✗ | ✗ | 缺失 | P3 | grep → 0 | — |
| PRICE-018 | Pricing admin 读 API (rate-table/snapshots) | ✗ | ✗ | ✗ | 已完成 | — | `routes.go:128-130` GET (**HUAKAI 独有读端点**) | — |
| PRICE-019 | Pricing 写 API (publish new version) | ✓ | ✓ | ✗ | 缺失 | P1 | 须直接 DB insert; grep POST /v1/pricing → 0; 151-06-L `/admin/v1/pricing/ratios` 部分 | 加 publish 端点 |
| PRICE-020 | Pre-request predicted cost (reservation) | 🟡 | ✓ | ✗ | 已完成 | — | billing.go ReserveRequest; predictedCompletionCost (启发: len/4) | — |
| PRICE-021 | Post-response actual cost (settlement) | ✓ | ✓ | ✗ | 已完成 | — | SettleRequest; actualCompletionCost (decimal) | — |
| PRICE-022 | Cost breakdown by token type (5 buckets) | 🟡 | ✓ | ✗ | 已完成 | — | `0002:155-163` | — |
| PRICE-023 | Cost reconciliation (late adjustment) | ✗ | 🟡 | ✗ | 已完成 | — | `0002:250-268` append-only signed delta | — |
| PRICE-024 | Cost attribution to billing claim | ✗ | 🟡 | ✗ | 已完成 | — | `0002:19-66` claim lifecycle FSM | — |
| PRICE-025 | Signed user cost receipt | ✗ | ✗ | ✗ | 已完成 | — | `0028:3-22` signer_fingerprint (**HUAKAI 独有 F-TRUST**) | — |
| PRICE-026 | Cost forecast / budget alert | ✗ | 🟡 | ✗ | 缺失 | P3 | grep budget_alert → 0 | — |
| PRICE-027 | Promo/coupon cost adjustment | 🟡 | ✓ | ✗ | 缺失 | P3 | grep coupon/promo → 0 (voucher≠promo) | — |
| TOK-001 | Input token tracking (upstream-reported) | ✓ | ✓ | ✗ | 已完成 | — | accounting.go | — |
| TOK-002 | Output token tracking | ✓ | ✓ | ✗ | 已完成 | — | accounting.go | — |
| TOK-003 | Cache creation token tracking (5m+1h split) | ✓ | ✓ | ✗ | 已完成 | — | `0002:134-136` | — |
| TOK-004 | Cache read token tracking | ✓ | ✓ | ✗ | 已完成 | — | `0002:132` | — |
| TOK-005 | Reasoning token tracking | ✓ | 🟡 | ✗ | 已完成 | — | accounting.go:16 (追踪非单独定价, =PRICE-008) | — |
| TOK-006 | Image output token tracking | ✗ | 🟡 | ✗ | 已完成 | — | `0002:137` (追踪但 rate=0, =PRICE-009) | — |
| TOK-007 | Heuristic pre-request estimation (len/4) | ✓ | 🟡 | ✗ | 已完成 | — | chat_completions_pricing.go:154-169 (粗) | — |
| TOK-008 | Real tokenizer (tiktoken/vendor SDK) | 🟡 | ✓ | ✗ | 缺失 | P2 | grep tiktoken/cl100k/o200k → 0 (±30% 误差) | 集成真 tokenizer |
| TOK-009 | Tool/function-call token accounting | ✗ | 🟡 | ✗ | 缺失 | P3 | grep tool_tokens → 0 | — |
| TOK-010 | Per-model token counter selection | ✗ | 🟡 | ✗ | 缺失 | P3 | grep TokenCounter → 0 | — |

### K.3 模型级速率限制 / 能力 flag / provider 命名空间 — 来自骨架① — 每项 = 一行

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| RL-001 | Per-binding RPM limit | 🟡 | ✓ | 🟡 | 已完成 | — | `0008:179` rpm_limit; registry.go:76 | — |
| RL-002 | Per-binding TPM limit | 🟡 | ✓ | 🟡 | 已完成 | — | `0008:180` tpm_limit | — |
| RL-003 | Per-binding max parallel requests | 🟡 | ✓ | 🟡 | 已完成 | — | `0008:185` | — |
| RL-004 | Model cooldown (upstream 429 tracking) | ✓ | ✓ | 🟡 | 已完成 | — | rate/model_cooldown.go | — |
| RL-005 | Per-user model quota (free-tier req limit) | ✓ | ✓ | ✗ | 缺失 | P2 | grep user_model_quota → 0 | 加 per-user-per-model quota |
| RL-006 | Burst allowance / multiplier | ✗ | 🟡 | ✗ | 缺失 | P3 | grep burst → 0 | — |
| RL-007 | Dynamic rate limit adjustment (auto-throttle) | ✗ | ✗ | ✗ | 缺失 | P3 | grep dynamic_rate → 0 | — |
| RL-008 | Rate limit pooling across aliases | ✗ | ✗ | ✗ | 缺失 | P3 | grep rate_pool → 0 | — |
| CAPF-001 | Capability storage table (model_registry_capabilities) | 🟡 | ✓ | ✓ | 已完成 | — | `0008:217-245` parameterized | — |
| CAPF-002 | Proto capability kind enum (14 families) | 🟡 | ✓ | ✓ | 已完成 | — | capability_graph.go:14-28 (=CAP-027) | — |
| CAPF-003 | Capability-model binding (registry↔proto align) | 🟡 | ✓ | ✓ | 部分完成 | P2 | model_sync_writer.go:363-432 free-text; 无 enum 约束/查询 API | 加 enum 约束 + 查询 API |
| CAPF-004 | Vision/image input capability flag (per-model) | 🟡 | ✓ | ✓ | 部分完成 | P2 | CapabilityImage in proto; 无 per-model registry flag 暴露给 routing/models | 暴露 per-model flag |
| CAPF-005 | Tool use capability flag (per-model) | 🟡 | ✓ | ✓ | 部分完成 | P2 | proto 有, 选型时不可查 | — |
| CAPF-006 | Structured output capability flag (per-model) | 🟡 | ✓ | 🟡 | 部分完成 | P3 | proto 级, 无 per-model gate | — |
| CAPF-007 | Extended thinking capability flag (per-model) | 🟡 | ✓ | 🟡 | 部分完成 | P3 | proto 级 | — |
| CAPF-008 | Audio/video/file capability flags (per-model) | ✗ | ✓ | 🟡 | 部分完成 | P3 | proto 定义, registry 未存 | — |
| CAPF-009 | Batch capability flag (per-model) | ✗ | ✗ | ✗ | 部分完成 | P3 | proto 有 (=CAP-023), 无 per-model flag | — |
| CAPF-010 | Computer use capability flag (per-model) | ✗ | ✗ | ✗ | 部分完成 | P3 | proto-only | — |
| CAPF-011 | MCP server capability flag (per-model) | ✗ | 🟡 | ✗ | 部分完成 | P3 | proto-only | — |
| CAPF-012 | Capability-based model routing | ✗ | 🟡 | ✗ | 缺失 | P2 | grep RouteByCapability → 0 (不能"找支持 tools+vision 的模型") | 加 capability 路由过滤 |
| CAPF-013 | Capability cost multiplier (vision 更贵) | ✗ | 🟡 | ✗ | 缺失 | P3 | grep capability_multiplier → 0 | — |
| CAPF-014 | Per-model max output tokens | 🟡 | ✓ | ✓ | 缺失 | P2 | context_window≠max output; grep max_output_tokens (stored) → 0 | 加 per-model max output |
| PROV-001 | Provider catalog table | 🟡 | ✓ | ✓ | 已完成 | — | `0001:32-44` providers | — |
| PROV-002 | Provider account model allow list | 🟡 | ✓ | ✓ | 已完成 | — | `0001` provider_accounts.model_allow_list | — |
| PROV-003 | Protocol family classification | ✓ | ✓ | ✓ | 已完成 | — | `0008:64` protocol_family; modelsync/types.go | — |
| PROV-004 | Provider feature/capability matrix (client×upstream×feature→verdict) | ✗ | 🟡 | 🟡 | 已完成 | — | `0005:23-56` protocol_capability_matrix (**HUAKAI 独有 D**) | — |
| PROV-005 | Provider-specific token count method | ✗ | 🟡 | ✗ | 缺失 | P3 | grep provider_token_counter → 0 | — |
| PROV-006 | Provider rate limit contract (declared RPM/TPM) | ✗ | 🟡 | ✗ | 缺失 | P3 | 经验式 429 发现 (非声明) | — |
| PROV-007 | Provider version / API version tracking | ✗ | 🟡 | ✗ | 缺失 | P3 | grep provider_version → 0 | — |

---

## L. 内容特性 / 流式 / 缓存 / 审计 — 来自骨架① content-features — 每项 = 一行 (内容端点)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CT-001 | SSE/chunked streaming (多 provider) | 🟡 | ✓ | ✓ | 已完成 | — | stream_plan.go (Buffered/Streaming/Replay); chat_completions_stream.go | — |
| CT-002 | Mid-stream cancellation/fallback | 🟡 | 🟡 | 🟡 | 已完成 | — | stream_plan.go FallbackBoundary, MidStreamFallbackPolicy (none/continuation/restart) | — |
| CT-003 | System prompt inject/override (prefix/suffix/block-array, 3 mode) | ✓ | ✓ | 🟡 | 已完成 | — | system_rewrite.go (EnsurePrefix/ReplaceAll/AppendAfter, idempotent) | — |
| CT-004 | Tool name rewriting/obfuscation | ✗ | ✗ | ✗ | 已完成 | — | tool_name_rewrite.go (ToolRename audit) (**HUAKAI 独有 mimicry**) | — |
| CT-005 | Cross-check/audit of reported tokens | ✗ | ✗ | ✗ | 已完成 | — | chat_completions_billing.go crossCheckAudit (**HUAKAI 独有 trust**) | — |
| CT-006 | Exact-match response cache (L2 in-memory) | ✓ | ✓ | 🟡 | 已完成 | — | cache/store.go MemoryStore LRU+TTL; cache/key.go BuildKey | — |
| CT-007 | Cache-aware sticky routing (prompt prefix affinity, PASR Track B) | 🟡 | 🟡 | ✗ | 已完成 | — | cache_routing/auto_inject.go + PASR prefix hash (**Juice 透明核心**) | — |
| CT-008 | Cache hit metrics | ✓ | ✓ | 🟡 | 已完成 | — | cachemetrics/l2.go | — |
| CT-009 | Distributed (Redis) response cache | ✓ | ✓ | ✗ | 缺失 | P2 | grep redis → 0; 仅 in-memory (重启丢/不可横扩) | 加 Redis Store impl |
| CT-010 | Semantic/embedding-based cache | ✓ | ✓ | ✗ | 缺失 | P2 | grep SemanticCache → 0 | 加语义缓存层 (省 20-40% 上游) |
| CT-011 | Native passthrough route | ✓ | 🟡 | ✓ | 已完成 | — | client_adapter_default_registry.go:52 /v1/native/openai/responses | — |
| CT-012 | Idempotency / request dedup | ✓ | 🟡 | 🟡 | 已完成 | — | chat_completions_idempotency_replay.go (payload_fingerprint unique) | — |
| CT-013 | Content redaction / privacy sanitisation | 🟡 | 🟡 | 🟡 | 部分完成 | P2 | privacy/default_redactor.go AllowlistRedactor (allowlist only, 无 keyword blocklist) | 加 keyword/LLM moderation |
| CT-014 | Content moderation / keyword blocklist | ✓ sensitive-word | ✓ content_filter | ✗ | 部分完成 | P1 | admin `moderationhttp/` keywords/hashes/config (151-06-L) 后端有; 无入站 chat 拦截管线接通 | 接通入站 moderation 管线 |
| CT-015 | Prompt injection detection | ✗ | ✗ | ✗ | 缺失 | P2 | grep prompt.inject → 0 | 加注入检测 |
| CT-016 | Output watermarking | ✗ | ✗ | ✗ | 缺失 | P3 | grep watermark → 0 | — |
| CT-017 | Batch inference endpoint `/v1/batches` | ✗ | ✗ | ✗ | 部分完成 | P2 | BatchNode 数据模型有 (=CAP-023); 无 route/queue/poll | 实现 batch 端点 |
| CT-018 | Computer-use agentic passthrough | ✗ | ✗ | ✗ | 部分完成 | P3 | ComputerUseNode (=CAP-024); 无 HTTP handler | — |
| CT-019 | MCP server integration (active proxy) | ✗ | 🟡 | 🟡 | 部分完成 | P2 | MCPServerNode (=CAP-025); 无活跃 MCP proxy handler | — |
| CT-020 | Data retention / ZDR tracking | ✗ | ✗ | ✗ | 部分完成 | P2 | DataRetentionNode (=CAP-028) tracking only | 加 enforcement |
| CT-021 | Live sessions / WebSocket bidirectional | ✗ | 🟡 | ✗ | 部分完成 | P3 | LiveSessionNode (=CAP-026); routes.go 501 | 见 PROTO-021 |
| CT-022 | Named prompt templates (stored) | ✓ | 🟡 | ✗ | 缺失 | P3 | grep PromptTemplate → 0 (system rewrite 非 stored template) | — |
| CT-023 | Variable substitution in prompts | ✓ | 🟡 | ✗ | 缺失 | P3 | 无模板引擎 | — |
| CT-024 | Conversation history mgmt (stateful) | ✓ | ✓ | ✗ | 缺失 | P2 | grep ConversationHistory → 0 (Hermes conversations 是产品层, 非网关) | — |
| CT-025 | Context summarisation (auto-compress) | ✓ | 🟡 | ✗ | 缺失 | P3 | 无 summarisation 模块 | — |
| CT-026 | RAG / retrieval hooks | ✗ | 🟡 | ✗ | 缺失 | P3 | grep RAG/VectorStore → 0 | — |
| CT-027 | Per-channel parameter defaults | ✓ | ✓ | ✗ | 缺失 | P2 | 无 channel 级默认 (RequestControls per-request only) (=CFG-005/018) | per-channel defaults |
| CT-028 | Response post-processing hooks (generic) | ✗ | 🟡 | ✗ | 部分完成 | P3 | 仅 system + tool-name rewrite; 无通用插件链 | — |
| CT-029 | Fallback content on error | ✓ | 🟡 | ✗ | 已完成 | — | stream_plan.go FallbackBoundary; DLQ | — |

---

## 摘要

- **行数 ~330**: 跨 12 段 (A 端点26 · B 媒体6 · C chat参数60+9 · D anthropic26+7 · E responses32 · F gemini38+其他端点参数~40 · G 渠道配置31+6门控 · H 协议转换34 · I 上游族22 · J 能力节点28+13+7 · K 注册/定价/限速/能力~95 · L 内容29).
- **源冲突处置**: 骨架①(gateway-core) 把 embeddings/images/audio 判 MISSING, 但细树②+151③ 深读 fix HEAD 确认 `routes.go:57-63` 已挂载 → 本表以 fix 真实挂载为准 (已完成), 证据列保留分歧, 并对 main(83 path) 缺者标 `未合并main`.
- **realtime**: 151 明确 path-exists-returns-not_available → `部分完成` (非缺失/未做).
- **HUAKAI 差异化亮点 (refs 均无)**: ProtocolLossEntry 两层损耗记账 (LOSS 段) · capability node 独有字段 (ThinkingNode.redaction/CacheControlNode.sanitize/locality_hint/ToolUseNode.display_name/status) · /v1/images/variations · /v1/generation · /v1/me/usage · /v1/trust/verify · signed cost receipt · protocol_capability_matrix · registry snapshots · alias NFC normalize · model-level fallback (opt-in exactly-once) · cache-aware PASR sticky (Juice).
- **OCAW TODO/stub (per 151)**: antigravity/kiro/windsurf/cursor/copilot session adapter 均复用 openai.Adapter (FAM-015~020, CONV-029~032) — 原生 adapter 待实现.
- **最大真缺口**: 协议转换 Gemini/Gemini-CLI/Codex/Antigravity 多方向 (cliproxy NxN 领先, H 段) · count_tokens 跨格式 · rerank · MJ/Suno 平台树 · reasoning/batch/image 独立定价 · Redis/semantic cache · capability-based routing · 参数 6-门控 · 真 tokenizer.


# ====================  模块 C  ====================
# 标杆 · 上游账号/凭证/账号池

> CANONICAL BENCHMARK feature tree — 单模块「上游 provider 账号 / 凭证获取+续期 / 账号池 / 粘性路由」，由 3 源合并去重而成。
>
> **合并来源**:
> 1. 骨架大功能树 — `wt/treeview/docs/process/feature-tree/provider-account-mgmt.md`(A–L 段，含 commercial-value 排名)
> 2. 字段级细树 — `feature-audit/03-upstream-creds-fine.md`(A–N 段,每字段/OAuth参数/storm 旋钮一行,file:line 证据)
> 3. 151 参考集 — `151-ref/06-...endpoint-status-tree.md`(K 段 AI provider mgmt 端点)+`151-ref/12D-...priority-tree.md`(AI- 行)+`151-ref/12C-cliproxyapi-shipped-usable-feature-tree.md`(CP-A/CP-M/CP-S 行)+`151-ref/04-comparison-missing-tree.md`
>
> **HUAKAI 基线**: `origin/fix/hermes-phase-1-e33d940@e89d7fce`。参考基线: sub2api `635ad81` · new-api `adc390c5` · CLIProxyAPI `3abfc83d`。
>
> **列定义**:
> - `ID` = CRED-NNN(全局唯一,跨源去重后稳定编号)。
> - `sub2api / new-api / cliproxy` ref 列: ✓ 有 / ✗ 无 / 🟡 部分。
> - `HUAKAI状态` 六级(**禁虚标**): 已完成 / 部分完成 / 后端有·前端缺 / 缺失 / 未做 / 未合并main。
> - `优先级` P0–P3 / —(— = 已完成且无对标缺口,无需推进)。
> - `证据` = pkg/file(:line);`推进动作` = concrete next step。
>
> **范围排除**(同源2): end-user SSO/社交登录 OAuth(wechat/dingtalk/linuxdo/oidc,mig 0081 `pending_oauth_sessions`/`oidc_provider_configs`)= 消费者身份,非上游 provider 凭证 → 不计入本树。

---

## A. 渠道/账号记录字段(每 config field = 一行)

来源: new-api `model/channel.go:22-70` · sub2api `ent/schema/account.go`+`proxy.go` · HUAKAI `sql/migrations/0001_pool_routing.up.sql`(`provider_accounts`)+`0016_account_credentials.up.sql`。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-001 | 上游 type/vendor 枚举 | ✓ | ✓ | ✓ | 已完成 | — | `provider_accounts.account_type` CHECK(oauth/api_key/service_account/upstream_static)+`account_credentials.vendor`(0001/0016) | 无 |
| CRED-002 | API key / secret 材料(加密) | ✓(JSON 明文) | ✓(`Key` 明文) | ✓(auth-file JSON) | 已完成 | — | `account_credentials.encrypted_payload bytea`(0016) | 无;HUAKAI 优于 3 ref(全密文) |
| CRED-003 | base_url override(per-cred 列) | ✗ | ✓`BaseURL *string`(channel.go:34) | ✓(per-auth attrs) | 部分完成 | P2 | 经 providers/channels 表,非 per-cred 列 | 评估是否补 per-account base_url 列以对齐 new-api/cliproxy |
| CRED-004 | models 允许列表 | ✓ | ✓`Models`(channel.go:40) | ✓models.go | 已完成 | — | `provider_accounts.model_allow_list text[]`(0001) | 无 |
| CRED-005 | group/pool 归属 | ✓(M2M+priority) | ✓`Group`(channel.go:41) | ✗ | 已完成 | — | `pool_groups`+provider_accounts pool 关联(0001) | 无 |
| CRED-006 | priority | ✓(default 50) | ✓`Priority *int64`(channel.go:45) | ✗ | 已完成 | — | `provider_accounts.priority` default 100(0001) | 无 |
| CRED-007 | static weight | ✗ | ✓`Weight *uint`(channel.go:30) | ✗ | 缺失 | P3 | HRW score 取代静态 weight | 设计决策:确认 HRW 替代足够,或补静态 weight override |
| CRED-008 | proxy 绑定 | ✓`proxy_id` | ✓`ChannelSettings.Proxy` | ✓`Auth.ProxyURL` | 已完成 | — | `provider_accounts.proxy_url text`(0012) | 无(见 L 段) |
| CRED-009 | status / enabled | ✓ | ✓`Status`(channel.go:27) | ✓`Auth.Status`+`Disabled` | 已完成 | — | `provider_accounts.enabled bool`+health_state(0001) | 无 |
| CRED-010 | auto_ban on error | ✗ | ✓`AutoBan *int` default 1(channel.go:46) | ✓(Unavailable flag) | 已完成 | — | `auto_pause_on_expired`+health policy(credentialworker/health_state.go) | 无 |
| CRED-011 | test_model(per-cred) | ✗ | ✓`TestModel *string`(channel.go:28) | ✗ | 缺失 | P3 | channelhealth probe 存在但非 per-cred test_model | 评估补 per-account test_model 字段 |
| CRED-012 | model_mapping(渠道列) | ✗ | ✓`ModelMapping *string`(channel.go:42) | ✗ | 部分完成 | P2 | modelsync/registry 层,非 channel 列 | 评估把 model_mapping 下沉到渠道/账号列 |
| CRED-013 | status_code_mapping | ✗ | ✓`StatusCodeMapping *string`(channel.go:44) | ✗ | 缺失 | P2 | 无 | 补 per-channel 状态码重映射(new-api 独有) |
| CRED-014 | param_override(body 覆写) | ✗ | ✓`ParamOverride *string`(channel.go:50) | ✗ | 部分完成 | P2 | affinity ParamOverrideTemplate 类比缺,channel-col 无 | 补 channel 级 body param override |
| CRED-015 | header_override | ✗ | ✓`HeaderOverride *string`(channel.go:51) | ✗ | 部分完成 | P3 | headerfirewall pkg(独立,非 channel 列) | 评估把 header override 暴露成渠道配置 |
| CRED-016 | openai_organization | ✗ | ✓`OpenAIOrganization *string`(channel.go:27) | ✗ | 缺失 | P3 | 无 | 补 OpenAI org 头透传字段 |
| CRED-017 | balance/used_quota 跟踪 | ✗ | ✓`Balance`/`UsedQuota`(channel.go:36-39) | ✗ | 已完成 | — | `quota_used_total/daily/weekly numeric(20,8)`(0001) | 无(见 J3 段) |
| CRED-018 | response_time/test_time | ✗ | ✓(channel.go:32-33) | ✗ | 部分完成 | P3 | channelhealth | 评估暴露 response_time/test_time 到账号视图 |
| CRED-019 | tag(批量操作分组) | ✗ | ✓`Tag *string` index(channel.go:48) | ✗ | 缺失 | P2 | 无通用 tags 表 | 补任意 label/tag 过滤(对齐骨架 H-04) |
| CRED-020 | remark / notes | ✗ | ✓`Remark`(channel.go:52) | (label) | 已完成 | — | `notes`(account.go) | 无 |
| CRED-021 | created_by/modified_by actor | ✗ | ✗ | ✗ | 已完成 | — | `created_by_actor`/`last_modified_by_actor`(0001+0016) | 无;HUAKAI 独有 |

## A2. ChannelSettings 子字段(new-api `dto/channel_settings.go:3-9`)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-022 | force_format | ✗ | ✓(:4) | ✗ | 缺失 | P3 | 无 | 评估补响应格式强制 |
| CRED-023 | thinking_to_content | ✗ | ✓(:5) | ✗ | 部分完成 | P2 | hermes thinking handling(他 pkg) | 确认 hermes 路径是否等价并暴露开关 |
| CRED-024 | proxy(settings) | ✓ | ✓`Proxy`(:6) | ✓ | 已完成 | — | proxy_url(0012) | 无 |
| CRED-025 | pass_through_body_enabled | ✗ | ✓(:7) | ✗ | 已完成 | — | RuntimeUpstreamPassthrough mode(credentialstore/types.go:60) | 无 |
| CRED-026 | system_prompt+override | ✗ | ✓(:8-9) | ✗ | 部分完成 | P2 | mimicry system_rewrite(0006 audit) | 确认 mimicry 是否覆盖,或补显式 system_prompt 配置 |

## A3. ChannelOtherSettings 透传开关(new-api `dto/channel_settings.go:26-45`)— Claude/OpenAI body-param 透传门(隐私/计费安全),**new-api 独家字段级控制**

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-027 | azure_responses_version | ✗ | ✓(:27) | 🟡 | 部分完成 | P2 | AuthModeAzure(types.go:37) | 补 azure responses 版本透传 |
| CRED-028 | vertex_key_type(json/api_key) | ✗ | ✓(:28) | ✓ | 已完成 | — | AuthModeVertexSA/VertexAnthropic(types.go:34,40) | 无 |
| CRED-029 | openrouter_enterprise | ✗ | ✓(:29) | ✗ | 部分完成 | P3 | VendorOpenRouter const(types.go:21) 无 flag | 补 openrouter enterprise 开关 |
| CRED-030 | claude_beta_query(?beta=true) | ✗ | ✓(:30) | ✗ | 缺失 | P2 | 无 | 补 claude beta query 透传开关 |
| CRED-031 | allow_service_tier 透传 | ✗ | ✓(:31 默认过滤) | ✗ | 缺失 | P1 | 无 | 补 service_tier 透传门(计费安全) |
| CRED-032 | allow_inference_geo 透传 | ✗ | ✓(:32 data-residency) | ✗ | 缺失 | P1 | 无 | 补 inference geo 透传门(数据驻留合规) |
| CRED-033 | allow_speed 透传 | ✗ | ✓(:33) | ✗ | 缺失 | P2 | 无 | 补 speed 参数透传门 |
| CRED-034 | allow_safety_identifier 透传 | ✗ | ✓(:34 隐私) | ✗ | 缺失 | P1 | 无 | 补 safety_identifier 透传门(隐私) |
| CRED-035 | disable_store 透传 | ✗ | ✓(:35) | ✗ | 缺失 | P2 | 无 | 补 store 禁用透传 |
| CRED-036 | allow_include_obfuscation 透传 | ✗ | ✓(:36) | ✗ | 缺失 | P2 | 无 | 补 obfuscation 透传门 |
| CRED-037 | aws_key_type | ✗ | ✓(:37) | ✗ | 已完成 | — | AuthModeBedrock SigV4(types.go:33) | 无 |
| CRED-038 | upstream_model_update_check_enabled | ✗ | ✓(:38) | ✗ | 已完成 | — | modelsync pkg | 无 |
| CRED-039 | upstream_model_update_auto_sync_enabled | ✗ | ✓(:39) | ✗ | 已完成 | — | modelsync | 无 |
| CRED-040 | upstream_model_update_last_check_time | ✗ | ✓(:40) | ✗ | 部分完成 | P3 | modelsync state | 暴露 last_check_time 到账号视图 |
| CRED-041 | model_update ignored/detected/removed lists | ✗ | ✓(:41-45) | ✗ | 部分完成 | P3 | modelsync | 暴露三类 model-diff 列表 |

## A4. sub2api account 调度/会话字段(`ent/schema/account.go`)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-042 | extra (JSON) metadata | ✓ | ✓OtherInfo | ✓Metadata | 部分完成 | P3 | credentials jsonb | 评估补结构化 extra 字段 |
| CRED-043 | concurrency cap | ✓(default 3) | ✗ | ✗ | 已完成 | — | `cap_concurrency` default 4(0001) | 无 |
| CRED-044 | load_factor | ✓`EffectiveLoadFactor` | 🟡weight | ✗ | 已完成 | — | in_flight_count/cap(0001) | 无 |
| CRED-045 | rate_multiplier(default 1.0) | ✓ | ✗ | ✗ | 已完成 | — | pool_group pricing ratios(0078) | 无 |
| CRED-046 | auto_pause_on_expired | ✓(default true) | 🟡auto_ban | ✗ | 已完成 | — | auto-pause via expires_at(0001) | 无 |
| CRED-047 | schedulable | ✓(default true) | ✓Status | ✓Disabled | 已完成 | — | enabled(0001) | 无 |
| CRED-048 | expires_at | ✓ | ✗ | ✓(token) | 已完成 | — | `expires_at`(0001)+access/refresh_expires_at(0016) | 无 |
| CRED-049 | rate_limited_at/reset_at | ✓ | 🟡 | ✓QuotaState.NextRecoverAt | 已完成 | — | temp_unschedulable_until+quota windows | 无 |
| CRED-050 | overload_until | ✓ | ✗ | ✗ | 已完成 | — | cooling_down health_state_until(0001) | 无 |
| CRED-051 | temp_unschedulable_until+reason | ✓ | ✗ | ✗ | 已完成 | — | StateTempUnschedulable(types.go:51) | 无 |
| CRED-052 | session_window_start/end/status | ✓(3 字段) | ✗ | ✗ | 部分完成 | P2 | session mgmt(mig 0021) | 对齐 sub2api session_window 三元组(用量窗口跟踪) |

---

## B. OAuth 授权码参数(每 provider × 每 param = 一行)

来源: cliproxy `internal/auth/{provider}/*.go` · new-api `service/codex_oauth.go` · HUAKAI `credentialacq/{anthropic_oauth,chatgpt_oauth}.go`+`vendor_exchangers.go`。

### B1. Anthropic / Claude(claude_ai_oauth, PKCE)
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-053 | Anthropic client_id | ✓ | ✗(仅 API key) | ✓`9d1c250a-...`(claude/anthropic_auth.go:27) | 已完成 | — | 同 id(credentialacq/anthropic_oauth.go:22) | 无 |
| CRED-054 | Anthropic auth_url | ✓ | ✗ | ✓`claude.ai/oauth/authorize`(:25) | 已完成 | — | 同(anthropic_oauth.go:20) | 无 |
| CRED-055 | Anthropic token_url | ✓ | ✗ | ✓`api.anthropic.com/v1/oauth/token`(:26) | 已完成 | — | 同(:21) | 无 |
| CRED-056 | Anthropic redirect_uri | ✓ | ✗ | ✓`localhost:54545/callback`(:28) | 已完成 | — | 同(:24) | 无 |
| CRED-057 | Anthropic scope | ✓ | ✗ | ✓`user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload`(:200) | 已完成 | P2 | **HUAKAI=`org:create_api_key user:profile user:inference`(:23)— mint-key 范围,非 Claude-Code session 范围** | 确认 scope 分歧是否刻意(mint-key vs proxy-session);若需 session 推理能力则补 |
| CRED-058 | Anthropic code_challenge_method S256 | ✓ | ✗ | ✓(:202) | 已完成 | — | S256(anthropic_oauth.go) | 无 |
| CRED-059 | Anthropic loopback callback server(:54545) | ✓ | ✗ | ✓(claude/oauth_server.go) | 部分完成 | P2 | exchanger 消费 operator 提供的 code+verifier,无 in-proc loopback | 补 in-proc loopback 回调服务以对齐本地接入体验 |

### B2. OpenAI Codex CLI(codex_cli_oauth / chatgpt_oauth, PKCE+device-code)
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-060 | Codex client_id | 🟡 | ✓`app_EMoamEEZ73f0CkXaXp7hrann`(codex_oauth.go:20) | ✓同(codex/openai_auth.go:26) | 已完成 | — | device-code primary(oauth_devicecode.go) | 无 |
| CRED-061 | Codex authorize_url | 🟡 | ✓`auth.openai.com/oauth/authorize`(:21) | ✓同(:24) | 已完成 | — | ✓ | 无 |
| CRED-062 | Codex token_url | 🟡 | ✓`auth.openai.com/oauth/token`(:22) | ✓同(:25) | 已完成 | — | ✓ | 无 |
| CRED-063 | Codex redirect_uri(:1455) | 🟡 | ✓`localhost:1455/auth/callback`(:23) | ✓(:27) | 部分完成 | P3 | n/a(走 device) | device 路径已覆盖;评估是否补 auth-code redirect |
| CRED-064 | Codex scope | 🟡 | ✓`openid profile email offline_access`(:24) | ✓同 | 已完成 | — | ✓ | 无 |
| CRED-065 | Codex code_challenge S256 | 🟡 | ✓(:224, generatePKCEPair :244) | ✓ | 已完成 | — | ✓ | 无 |
| CRED-066 | Codex device-code 流程 | ✗ | ✗(仅 auth-code) | ✓`codex_device.go` | 已完成 | — | **primary**(oauth_devicecode.go:126 pollOpenAICodexDeviceAuthorization) | 无;HUAKAI device-code 为主路 |

### B3. Google / Gemini(code_assist, google_one)
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-067 | Gemini client_id | ✓ | ✗ | ✓`681255809395-...`(gemini/gemini_auth.go:31) | 已完成 | — | public-CLI exchanger(vendor_exchangers.go:40-41) | 无 |
| CRED-068 | Gemini scopes | ✓ | ✗ | ✓`Scopes` var(:37) | 已完成 | — | code_assist+google_one(2 模式) | 无 |
| CRED-069 | Gemini token_uri | ✓ | ✗ | ✓`oauth2.googleapis.com/token`(:178) | 已完成 | — | ✓ | 无 |
| CRED-070 | Gemini loopback callback | ✓ | ✗ | ✓`:port/oauth2callback`(:79,:211) | 部分完成 | P2 | operator code(无 in-proc loopback) | 补 in-proc loopback 回调 |
| CRED-071 | Gemini access_type=offline+prompt=consent | ✓ | ✗ | ✓(:259 AccessTypeOffline) | 部分完成 | P3 | 🟡 | 确认 offline+consent 参数是否齐备 |
| CRED-072 | Gemini userinfo probe(tier map) | ✓ | ✗ | ✓`oauth2/v1/userinfo`(:142) | 部分完成 | P3 | JWT claim(非显式 userinfo probe) | 评估补 userinfo tier 探测 |

### B4. Antigravity(Google-backed OAuth)
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-073 | Antigravity client_id | ✓ | ✗ | ✓`1071006060591-...`(antigravity/*.go:6) | 部分完成 | P2 | **FAIL-CLOSED/PAUSED**(vendor_exchangers.go:45-46 "DR-GEM-3-ANTIGRAVITY-PAUSED") | 解除 paused 或确认停用决策,补完整 exchanger |
| CRED-074 | Antigravity auth_endpoint | ✓ | ✗ | ✓`accounts.google.com/o/oauth2/v2/auth`(:23) | 未做 | P2 | ✗(paused) | 随 CRED-073 解封 |
| CRED-075 | Antigravity token_endpoint | ✓ | ✗ | ✓`oauth2.googleapis.com/token`(:22) | 未做 | P2 | ✗(paused) | 随 CRED-073 解封 |
| CRED-076 | Antigravity scopes | ✓ | ✗ | ✓`Scopes` var(:12) | 未做 | P2 | ✗(paused) | 随 CRED-073 解封 |
| CRED-077 | Antigravity userinfo_endpoint(credits fetch) | ✓ | ✗ | ✓`oauth2/v2/userinfo`(:24) | 未做 | P3 | ✗ | 随解封补 credits 抓取 |

### B5. xAI / Grok(loopback PKCE)
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-078 | Grok client_id | ✗ | ✗ | ✓`b1a00492-...`(xai/*.go:14) | 缺失 | P1 | VendorGrok const(types.go:23) 无 exchanger | 补 xAI/Grok OAuth exchanger(cliproxy 独有,HUAKAI 仅常量) |
| CRED-079 | Grok scope | ✗ | ✗ | ✓`openid profile email offline_access grok-cli:access api:access`(:16) | 缺失 | P1 | ✗ | 随 CRED-078 |
| CRED-080 | Grok redirect_uri(:56121) | ✗ | ✗ | ✓`127.0.0.1:56121/callback`(:16/:37) | 缺失 | P1 | ✗ | 随 CRED-078 |
| CRED-081 | Grok code_challenge S256 | ✗ | ✗ | ✓(:87) | 缺失 | P1 | ✗ | 随 CRED-078 |

### B6. Kimi / Moonshot(device-code RFC 8628)
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-082 | Kimi client_id | ✗ | ✗ | ✓`17e5f671-...`(kimi/kimi.go:25) | 缺失 | P1 | 无 Kimi vendor | 补 Kimi/Moonshot device-code vendor(cliproxy 独有) |
| CRED-083 | Kimi device_code_url | ✗ | ✗ | ✓`/api/oauth/device_authorization`(:29) | 缺失 | P1 | ✗ | 随 CRED-082 |
| CRED-084 | Kimi token_url | ✗ | ✗ | ✓`/api/oauth/token`(:31) | 缺失 | P1 | ✗ | 随 CRED-082 |
| CRED-085 | Kimi poll interval/expires 处理 | ✗ | ✗ | ✓PollForToken(:219,:224-231) | 缺失 | P1 | ✗ | 随 CRED-082 |

### B7. 其他 vendor 获取流(acquisition presence)
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-086 | Copilot device_code | ✗ | ✗ | ✗ | 已完成 | — | `copilot/device_code` NewDeviceCodeExchanger(vendor_exchangers.go:54) | 无;HUAKAI 独有 |
| CRED-087 | Copilot oauth callback | ✗ | ✗ | ✗ | 部分完成 | P3 | **FAIL-CLOSED**(:60-61 "未实现;用 device_code 或 import") | 评估补 oauth callback 路径或保持 device_code-only |
| CRED-088 | Kiro AWS SSO | ✗ | ✗ | ✗ | 已完成 | — | `kiro/sso` NewSSOExchanger(:55) | 无;HUAKAI 独有 |
| CRED-089 | Bedrock SSO | ✗ | ✗ | ✗ | 已完成 | — | `anthropic/bedrock` NewSSOExchanger(:52) | 无;HUAKAI 独有 |
| CRED-090 | Windsurf token import | ✗ | ✗ | ✗ | 已完成 | — | windsurf_token.go candidate | 无;HUAKAI 独有 |
| CRED-091 | Cursor session+refresher | ✗ | ✗ | ✗ | 部分完成 | P3 | provider/cursor session+refresher,无 OAuth 流 | 评估是否补 Cursor OAuth 获取流 |
| CRED-092 | Gemini generic /oauth(operator) | ✗ | ✗ | ✗ | 已完成 | — | `gemini/oauth` authcode exchanger(:47) | 无;HUAKAI 独有 |
| CRED-093 | Antigravity generic /oauth | ✓ | ✗ | ✓ | 已完成 | — | `antigravity/oauth` authcode(:53) | 无 |

---

## C. Vendor × auth_mode handler 声明式契约(HUAKAI `credentialstore/types.go:257-275`)
19 handlerSpec 行,定义 required/anyOf 凭证字段+refreshable+allowGrace+sessionFirst+runtimeKind。**无任何 ref 有此 per-mode 声明式契约。**

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-094 | anthropic/api_key → RuntimeAPIKey | 🟡 | ✓ | 🟡 | 已完成 | — | types.go:257(req api_key) | 无 |
| CRED-095 | anthropic/claude_ai_oauth → OAuthAccessToken(refreshable,grace) | ✓ | ✗ | ✓ | 已完成 | — | types.go:258 | 无 |
| CRED-096 | anthropic/claude_code → SessionToken(refreshable,grace,sessionFirst) | 🟡 | ✗ | 🟡 | 已完成 | — | types.go:259 | 无 |
| CRED-097 | anthropic/bedrock → AWSSigV4 | ✗ | 🟡 | ✗ | 已完成 | — | types.go:260(req aws_access_key_id+secret+region) | 无 |
| CRED-098 | anthropic/vertex_anthropic → UpstreamPassthrough(refreshable,grace) | ✗ | 🟡 | ✓ | 已完成 | — | types.go:261 | 无 |
| CRED-099 | openai/api_key → RuntimeAPIKey | 🟡 | ✓ | 🟡 | 已完成 | — | types.go:262 | 无 |
| CRED-100 | openai/chatgpt_oauth → SessionToken(refreshable,grace,sessionFirst) | ✓ | ✗ | 🟡 | 已完成 | — | types.go:263 | 无 |
| CRED-101 | openai/codex_cli_oauth → SessionToken(refreshable,grace,sessionFirst) | 🟡 | ✓ | ✓ | 已完成 | — | types.go:264 | 无 |
| CRED-102 | openai/azure → RuntimeAPIKey(refreshable,grace) | ✗ | ✓ | 🟡 | 已完成 | — | types.go:265 | 无 |
| CRED-103 | openai/refresh_token → UpstreamPassthrough(refreshable,grace) | ✗ | ✗ | ✗ | 已完成 | — | types.go:266 | 无;HUAKAI 独有 |
| CRED-104 | gemini/aistudio_api_key → RuntimeAPIKey | ✓ | ✓ | ✓ | 已完成 | — | types.go:267 | 无 |
| CRED-105 | gemini/vertex_sa → UpstreamPassthrough(refreshable,grace) | ✗ | ✗ | ✓ | 已完成 | — | types.go:268 | 无 |
| CRED-106 | gemini/code_assist → SessionToken(refreshable,grace,sessionFirst) | ✓ | ✗ | ✓ | 已完成 | — | types.go:269 | 无 |
| CRED-107 | gemini/google_one → SessionToken(refreshable,grace,sessionFirst) | ✓ | ✗ | ✗ | 已完成 | — | types.go:270 | 无 |
| CRED-108 | gemini/antigravity → SessionToken(refreshable,grace,sessionFirst) | ✓ | ✗ | ✓ | 部分完成 | P2 | types.go:271(paused adapter) | 随 CRED-073 解封 |
| CRED-109 | gemini/oauth → SessionToken(refreshable,grace,sessionFirst) | ✗ | ✗ | ✗ | 已完成 | — | types.go:272 | 无;HUAKAI 独有 |
| CRED-110 | copilot/copilot_oauth → SessionToken(refreshable,grace,sessionFirst) | ✗ | ✗ | ✗ | 已完成 | — | types.go:273 | 无;HUAKAI 独有 |
| CRED-111 | antigravity/oauth → SessionToken(refreshable,grace,sessionFirst) | ✓ | ✗ | ✓ | 已完成 | — | types.go:274 | 无 |
| CRED-112 | windsurf/oauth → SessionToken(refreshable,grace,sessionFirst) | ✗ | ✗ | ✗ | 已完成 | — | types.go:275 | 无;HUAKAI 独有 |

---

## D. 续期调度器旋钮(每数值旋钮 = 一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-113 | proactive 扫描间隔 | ✓ | ✓10m(codex_credential_refresh_task.go:20) | ✓heap-driven | 已完成 | — | **60s** DefaultInterval(credentialworker/scheduler.go:20) | 无 |
| CRED-114 | near-expiry 预警窗口 | 🟡 | ✓24h(:21) | ✓per-provider RefreshLead | 已完成 | — | **15m** DefaultWarningWindow(:21) | 无 |
| CRED-115 | refresh RPC timeout | 🟡 | ✓15s(:23) | ✗ | 部分完成 | P3 | ctx-driven(无显式常量) | 评估补显式 refresh timeout 常量 |
| CRED-116 | RefreshLead: claude | ✓ | n/a | ✓4h(sdk/auth/claude.go:34) | 已完成 | — | mode adapter(mode_refresh.go:70) | 无 |
| CRED-117 | RefreshLead: codex | n/a | n/a | ✓5d(codex.go:34) | 已完成 | — | mode adapter(:76) | 无 |
| CRED-118 | RefreshLead: gemini | ✓ | n/a | ✓nil(无 proactive)(gemini.go:26) | 已完成 | — | builtin client adapter(:81-82) | 无 |
| CRED-119 | RefreshLead: antigravity | ✓ | n/a | ✓5min(antigravity.go:30) | 部分完成 | P2 | paused adapter(:84) | 随 CRED-073 解封 |
| CRED-120 | RefreshLead: kimi | n/a | n/a | ✓5min(kimi.go:17) | 缺失 | P1 | 无 | 随 CRED-082 补 Kimi |
| CRED-121 | RefreshLead: xai | n/a | n/a | ✓registered(refresh_registry.go:16) | 缺失 | P1 | 无 | 随 CRED-078 补 xAI |
| CRED-122 | per-account 扫描上限 | 🟡 | ✗ | ✓refreshMaxConcurrency | 已完成 | — | **100** DefaultAccountLimit(:22) | 无 |
| CRED-123 | max refresh attempts | 🟡 | ✗ | ✗ | 已完成 | — | **3** DefaultMaxAttempts(:23) | 无 |
| CRED-124 | backoff schedule | 🟡 | ✗ | ✓refreshFailureBackoff(conductor.go) | 已完成 | — | defaultBackoff(attempt)(:289) | 无 |
| CRED-125 | expiry-skew grace | ✗ | ✗ | ✗ | 已完成 | — | **30s** defaultExpirySkewGrace(anthropicoauth/refresher.go:32) | 无;HUAKAI 独有 |

## E. 反应式 on-401 续期(每旋钮 = 一行)
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-126 | on-401 触发续期 | ✓ratelimit_service_401* | ✓codex re-refresh | ✓isUnauthorizedError(conductor.go) | 已完成 | — | `oauth_401_force_refresh` outcome(0006 audit enum) | 无 |
| CRED-127 | 401 ⇒ bounded vendor calls | ✗ | ✗ | ✗ | 已完成 | — | "account 401s ⇒ 1 vendor refresh call"(gateway/storm_policy.go:11) | 无;HUAKAI 独有 |
| CRED-128 | 401 sub-budget override | ✗ | ✗ | ✗ | 已完成 | — | attempt_error.go override-1 401 sub-budget | 无;HUAKAI 独有 |

## F. 续期失败策略(per-provider action = 一行)
来源: sub2api `service/refresh_policy.go` · HUAKAI `anthropicoauth/refresher.go` failure classes。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-129 | Claude OnRefreshError | ✓UseExistingToken(:34) | 🟡fixed | 🟡 | 已完成 | — | classified failureAuthExpired non-retryable(refresher.go:300) | 无 |
| CRED-130 | Claude OnLockHeld | ✓WaitForCache(:35) | ✗ | 🟡markRefreshPending | 已完成 | — | advisory-lock wait(refresh_lock.go) | 无 |
| CRED-131 | Claude FailureTTL | ✓1m(:36) | ✗ | ✗ | 部分完成 | P3 | next_attempt_at 列(0016) | 评估补显式 per-provider FailureTTL |
| CRED-132 | OpenAI OnRefreshError | ✓UseExistingToken(:42) | 🟡 | 🟡 | 已完成 | — | ✓ | 无 |
| CRED-133 | OpenAI FailureTTL | ✓1m(:44) | ✗ | ✗ | 部分完成 | P3 | 🟡 | 同 CRED-131 |
| CRED-134 | Gemini OnRefreshError | ✓Return(:50) | ✗ | 🟡 | 已完成 | — | Return failureClass(refresher.go) | 无 |
| CRED-135 | Gemini OnLockHeld | ✓UseExistingToken(:51) | ✗ | ✗ | 已完成 | — | ✓ | 无 |
| CRED-136 | Gemini FailureTTL | ✓0(:52) | ✗ | ✗ | 部分完成 | P3 | 🟡 | 同 CRED-131 |
| CRED-137 | Antigravity OnRefreshError | ✓Return(:58) | ✗ | 🟡 | 部分完成 | P2 | paused | 随 CRED-073 |
| CRED-138 | Background OnLockHeld | ✓SkipAsSkipped(:82) | ✗ | ✓ | 已完成 | — | storm-gated skip | 无 |
| CRED-139 | failure class taxonomy | 🟡3 actions | ✗ | 🟡unauthorized/backoff | 已完成 | — | **5 classes** auth_expired/rate_limit_exceeded/non_retryable/temporary/payload_invalid(refresher.go:34-38) | 无;优于 ref |

## G. 续期 storm 控制(每 scope/旋钮 = 一行)
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-140 | single-flight lock TTL | ✓60s(oauth_refresh_api.go:22) | 🟡task-level | 🟡refreshPendingBackoff | 已完成 | — | `pg_advisory_xact_lock(hashtext('credential_refresh:'\|\|id))`(refresh_lock.go:18) | 无 |
| CRED-141 | in-proc local mutex map | ✓localLocks sync.Map(:38) | ✗ | ✗ | 已完成 | — | n/a(DB advisory 替代) | 无 |
| CRED-142 | 分布式锁(Redis) | ✓tokenCache.AcquireRefreshLock(:91) | ✗ | ✗ | 已完成 | — | Postgres-durable advisory lock | 无 |
| CRED-143 | account-scope budget | ✗ | ✗ | ✗ | 已完成 | — | `cap_concurrent_refreshes` default **1**(0006);TryAcquireAccountStormSlot(storm_controller.go:74) | 无;HUAKAI 独有 |
| CRED-144 | account refreshes/minute cap | ✗ | ✗ | ✗ | 已完成 | — | `cap_refreshes_per_minute` default **60**(0006) | 无;HUAKAI 独有 |
| CRED-145 | in-flight counter(durable) | ✗ | ✗ | ✗ | 已完成 | — | `current_in_flight`/`refreshes_in_window`/`window_start`(0006) | 无;HUAKAI 独有 |
| CRED-146 | circuit breaker open-until | ✗ | ✗ | ✗ | 已完成 | — | `circuit_open_until timestamptz`(0006) | 无;HUAKAI 独有 |
| CRED-147 | endpoint-scope token bucket | ✗ | ✗ | ✗ | 已完成 | — | in-mem endpointBucket(providerCode\|fingerprint)(storm_controller.go:102) | 无;HUAKAI 独有 |
| CRED-148 | global-scope token bucket | ✗ | ✗ | ✗ | 已完成 | — | globalBucket.tryAcquire(storm_controller.go:117) | 无;HUAKAI 独有 |
| CRED-149 | refund-on-deny semantics | ✗ | ✗ | ✗ | 已完成 | — | "refund only when bucket denied, not on fn error"(gateway/storm_policy.go:19-20) | 无;HUAKAI 独有 |
| CRED-150 | SSRF guard on token endpoint | ✗ | ✗ | ✗ | 已完成 | — | oauth_devicecode_ssrf_test.go lane | 无;HUAKAI 独有 |
| CRED-151 | form-response byte cap | ✗ | ✗ | ✗ | 已完成 | — | **1MiB** oauthFormResponseMaxBytes(oauth_devicecode.go:20) | 无;HUAKAI 独有 |
| CRED-152 | device-code slow-down step | ✗ | ✗ | ✓deviceauth | 已完成 | — | **5s** deviceCodeSlowDownStep(oauth_devicecode.go:19) | 无 |

## H. 静态加密+密钥管理(每字段 = 一行)
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-153 | cipher scheme | ✗(明文) | ✗ | ✗(明文 0600) | 已完成 | — | `encryption_scheme` CHECK('aes-256-gcm')(0016) | 无;全 ref 明文,HUAKAI 独有加密 |
| CRED-154 | encrypted_payload 列 | ✗ | ✗ | ✗ | 已完成 | — | `encrypted_payload bytea`(0016) | 无 |
| CRED-155 | nonce 列 | ✗ | ✗ | ✗ | 已完成 | — | `nonce bytea`(0016) | 无 |
| CRED-156 | key_id 列(轮转槽) | ✗ | ✗ | ✗ | 部分完成 | P1 | `key_id text`+KeyProvider CurrentKey/by-ID(crypto.go) | **补加密密钥轮转/re-encrypt job**(骨架 K-03:槽存在但无 re-encryption job/key-version migration) |
| CRED-157 | 6-tuple AAD(TenantID/ProviderAccountID/Vendor/AuthMode/Version/KeyID) | ✗ | ✗ | ✗ | 已完成 | — | crypto.go AAD struct(6 元组) | 无;HUAKAI 独有 |
| CRED-158 | aad_hash 列 | ✗ | ✗ | ✗ | 已完成 | — | `aad_hash text`(0016) | 无 |
| CRED-159 | payload_fingerprint+refresh_token_fingerprint | ✗ | ✗ | ✗ | 已完成 | — | (0016) | 无;HUAKAI 独有 |
| CRED-160 | proxy-secret envelope 加密 | ✗(明文 URL) | ✗(明文含 creds) | ✗ | 已完成 | — | `huakai-proxy-secret-v1:` prefix+AAD(proxysecret/secret.go:12) | 无;HUAKAI 独有 |
| CRED-161 | 外部 vault 集成(HashiCorp/AWS SM/GCP KMS) | ✗ | ✗ | ✗ | 缺失 | P1 | grep 全无;注释承认应从 KMS/vault(sign/keygen.go:10),enc key 仅在 env var | **补外部 secrets-vault 集成**(SOC-2/ISO-27001 阻塞项,骨架 K-02) |

## I. 版本 CAS / 写回(每项 = 一行)
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-162 | credential_version 列 | ✗ | ✗ | ✗ | 已完成 | — | `credential_version integer CHECK(>0)`(0016) | 无;HUAKAI 独有 |
| CRED-163 | CAS update 守卫 | ✗ | ✗ | ✗ | 已完成 | — | `...WHERE credential_version=$N`(postgres_store.go) | 无;HUAKAI 独有 |
| CRED-164 | provider_id-changed 守卫 | ✗ | ✗ | ✗ | 已完成 | — | refresher.go PostgresRefreshStore mismatch guard | 无;HUAKAI 独有 |
| CRED-165 | same-tx credential+audit | ✗ | ✗ | ✗ | 已完成 | — | credential_audit_tx_test.go(RR-W5-002) | 无;HUAKAI 独有 |

---

## J. 账号池 / 多密钥(每 config field = 一行)
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-166 | is_multi_key flag | 🟡multi-account | ✓ChannelInfo.IsMultiKey(channel.go:63) | 🟡multi-file | 部分完成 | P2 | multi-cred per account(非 channel 内多 key 数组) | 评估对齐 new-api channel 内 multi-key 数组模型 |
| CRED-167 | multi_key_size | ✗ | ✓(:64, recalc :555) | ✗ | 缺失 | P3 | n/a | 随 multi-key 模型决策 |
| CRED-168 | multi_key_status_list(idx→status) | ✗ | ✓(:65 map) | ✗ | 已完成 | — | per-cred state | 无 |
| CRED-169 | multi_key_disabled_reason | ✗ | ✓(:66 map) | ✗ | 已完成 | — | last_refresh_outcome(0016) | 无 |
| CRED-170 | multi_key_disabled_time | ✗ | ✓(:67 map) | ✗ | 已完成 | — | next_attempt_at(0016) | 无 |
| CRED-171 | multi_key_polling_index | ✗ | ✓(:68 GetNextEnabledKey:199) | ✗ | 缺失 | P3 | n/a | 随 multi-key 模型决策 |
| CRED-172 | multi_key_mode(random/polling) | ✗ | ✓MultiKeyMode(multi_key_mode.go:6-7) | ✗ | 缺失 | P2 | 无 | 补 channel 内多 key 轮询/随机模式(new-api 独有) |
| CRED-173 | cap_concurrency | ✓(=3) | ✗ | ✗ | 已完成 | — | default **4**(0001) | 无 |
| CRED-174 | in_flight_count(atomic admission) | ✓ | ✗ | ✗ | 已完成 | — | `in_flight_count CHECK(>=0)`(0001) | 无 |
| CRED-175 | cap_queue_sticky | ✗ | ✗ | ✗ | 已完成 | — | default **2**(0001) | 无;HUAKAI 独有 |
| CRED-176 | cap_queue_fallback | ✗ | ✗ | ✗ | 已完成 | — | default **8**(0001) | 无;HUAKAI 独有 |
| CRED-177 | queue_depth | ✗ | ✗ | ✗ | 已完成 | — | `queue_depth CHECK(>=0)`(0001) | 无;HUAKAI 独有 |
| CRED-178 | pool_mode flag | ✓IsPoolMode retry=3 | 🟡 | ✗ | 已完成 | — | pool dispatcher slots | 无 |
| CRED-179 | capability_flags | ✗ | ✗ | ✗ | 已完成 | — | `capability_flags text[]` tool_use/vision/reasoning_high(0001) | 无;HUAKAI 独有 |
| CRED-180 | lease+heartbeat | ✗ | ✗ | ✓session conn reuse | 已完成 | — | `pool_slot_acquisitions.lease_expires_at`+`heartbeat_at`(0001) | 无 |
| CRED-181 | acquisition_token(idempotency) | ✗ | ✗ | ✗ | 已完成 | — | `acquisition_token uuid`+status enum(0001) | 无;HUAKAI 独有 |
| CRED-182 | orphan-slot sweep | ✗ | ✗ | ✗ | 已完成 | — | status='orphan_swept'(0001) | 无;HUAKAI 独有 |

## J2. pool_groups 策略字段(HUAKAI `0001`)— 各默认值,无 ref 等价
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-183 | routing_policy_version | ✗ | ✗ | ✗ | 已完成 | — | default '1.0' | 无 |
| CRED-184 | top_k_default(1-10) | ✗ | ✗ | ✗ | 已完成 | — | default **1** CHECK 1..10 | 无 |
| CRED-185 | capability_default | ✗ | ✗ | ✗ | 已完成 | — | exact_capability_only/safe_equivalent_allowed | 无 |
| CRED-186 | allow_tenant_operator_force | ✗ | ✗ | ✗ | 已完成 | — | default false | 无 |
| CRED-187 | allow_last_resort | ✗ | ✗ | ✗ | 已完成 | — | default false | 无 |
| CRED-188 | sticky_wait_max_waiting | ✗ | ✗ | ✗ | 已完成 | — | default **2** | 无 |
| CRED-189 | fallback_wait_max_waiting | ✗ | ✗ | ✗ | 已完成 | — | default **8** | 无 |
| CRED-190 | sticky_wait_timeout_ms | ✗ | ✗ | ✗ | 已完成 | — | default **5000** | 无 |
| CRED-191 | fallback_wait_timeout_ms | ✗ | ✗ | ✗ | 已完成 | — | default **30000** | 无 |
| CRED-192 | forced_route_rate_limit_per_hour | ✗ | ✗ | ✗ | 已完成 | — | default **5** | 无 |

## J3. 配额字段(HUAKAI `provider_accounts` 0001)— 每窗口 = 一行
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-193 | cap_quota_total/used_total | ✓ | ✓Balance/UsedQuota | ✗ | 已完成 | — | numeric(20,8)(0001) | 无 |
| CRED-194 | cap_quota_daily+window_start | ✓ | 🟡 | ✗ | 已完成 | — | `cap_quota_daily`+`quota_window_daily_start` | 无 |
| CRED-195 | cap_quota_weekly+window_start | ✗ | ✗ | ✗ | 已完成 | — | `cap_quota_weekly`+`quota_window_weekly_start` | 无;HUAKAI 独有 |
| CRED-196 | quota calendar-month window | ✗ | ✗ | ✗ | 已完成 | — | 0072_quota_calendar_month | 无 |
| CRED-197 | quota_status(active/exhausted/paused) | ✓ | ✓ratelimit | ✓QuotaState.Exceeded | 已完成 | — | 3-state(0001) | 无 |
| CRED-198 | quota mode(enforce/observe/manual_first/disabled) | 🟡 | 🟡 | ✗ | 已完成 | — | 0070_quota_subsystem:33-35 | 无;HUAKAI 软硬配额模式 |
| CRED-199 | backoff_level(cooldown 指数) | ✓ | ✓ | ✓QuotaState.BackoffLevel | 已完成 | — | aging_worker | 无 |
| CRED-200 | next_recover_at | ✓rate_limit_reset_at | ✓ | ✓QuotaState.NextRecoverAt | 已完成 | — | health_state_until | 无 |
| CRED-201 | per-(account,model) 配额差异化 | ✗ | 🟡model_rate_limits(RPM) | ✗ | 部分完成 | P1 | 0004:55-56 model_rate_limits jsonb(仅 RPM,无 per-model cost/token cap) | **补 per-(account,model) cost/token 配额上限**(骨架 D-06:无法"claude-opus $10/day"账号级) |

---

## K. 粘性 / 亲和路由(每 setting = 一行)
来源: new-api `service/channel_affinity.go`+`channel_affinity_setting.go` · sub2api `service/openai_sticky_compat.go` · HUAKAI `pool/binding/sticky.go`+`cache_routing/prompt_hash.go`+`pool/router/{pasr,hrw_ring}.go`。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-202 | sticky key=session-hash | ✓DeriveSessionHashFromSeed(:34) | 🟡key-source configurable | 🟡session_cache.go | 已完成 | — | `session_hash` 列(sticky_bindings 0001) | 无 |
| CRED-203 | sticky key 含 model | ✗ | ✓IncludeModelName | ✗ | 已完成 | — | `(session_hash, model)` pair(0001) | 无 |
| CRED-204 | sticky TTL | ✓legacy TTL(:111) | ✓rule.TTLSeconds/default 3600s(:90) | ✗ | 已完成 | — | **1h** defaultStickyTTL(sticky.go:64,对齐 Anthropic extended cache) | 无 |
| CRED-205 | affinity rule:name | ✗ | ✓ChannelAffinityRule.Name | ✗ | 部分完成 | P3 | n/a(per-tenant,无命名规则) | 评估补命名亲和规则 |
| CRED-206 | affinity rule:model_regex | ✗ | ✓ModelRegex []string | ✗ | 缺失 | P2 | 无 | 补 model_regex 亲和规则匹配(new-api 独有) |
| CRED-207 | affinity rule:path_regex | ✗ | ✓PathRegex []string | ✗ | 缺失 | P2 | 无 | 补 path_regex 亲和匹配 |
| CRED-208 | affinity rule:user_agent_include | ✗ | ✓UserAgentInclude []string | ✗ | 缺失 | P3 | 无 | 补 UA-include 亲和匹配 |
| CRED-209 | affinity key_source.type(context/header/gjson) | ✗ | ✓KeySource.Type | ✗ | 缺失 | P2 | n/a(prompt-hash 固定) | 评估补可配置 key_source |
| CRED-210 | affinity key_source.key | ✗ | ✓KeySource.Key | ✗ | 缺失 | P3 | n/a | 随 CRED-209 |
| CRED-211 | affinity key_source.path(gjson) | ✗ | ✓KeySource.Path | ✗ | 缺失 | P3 | n/a | 随 CRED-209 |
| CRED-212 | affinity value_regex | ✗ | ✓ValueRegex | ✗ | 缺失 | P3 | 无 | 随 CRED-209 |
| CRED-213 | affinity param_override_template | ✗ | ✓ParamOverrideTemplate map(merge:545) | ✗ | 缺失 | P2 | 无 | 补亲和命中时 param override 模板(关联 CRED-014) |
| CRED-214 | affinity skip_retry_on_failure | ✗ | ✓SkipRetryOnFailure | ✗ | 已完成 | — | PASR safe-fallback(gates.go) | 无 |
| CRED-215 | affinity include_using_group | ✗ | ✓IncludeUsingGroup | ✗ | 已完成 | — | tenant scope | 无 |
| CRED-216 | affinity include_rule_name | ✗ | ✓IncludeRuleName | ✗ | 部分完成 | P3 | n/a | 随命名规则 CRED-205 |
| CRED-217 | prompt-prefix hash(system+tools only) | ✗(opaque seed) | 🟡gjson path | ✗ | 已完成 | — | ComputePromptHash sha256(system+tools),msgs excluded(prompt_hash.go:43-60) | 无;HUAKAI 独有精细化 |
| CRED-218 | double-SHA 字段序稳定 hash | ✗ | ✗ | ✗ | 已完成 | — | prompt_hash.go:40(raw-extract then hash) | 无;HUAKAI 独有 |
| CRED-219 | empty-prefix sentinel | ✗ | ✗ | ✗ | 已完成 | — | PromptHashEmpty(prompt_hash.go:56) | 无;HUAKAI 独有 |
| CRED-220 | HRW rendezvous ring | ✗(flat map) | 🟡fnv hash | ✗ | 已完成 | — | `HRWScore=SHA256(seed‖prefix‖acc)[0:8]`(hrw_ring.go:8) | 无;HUAKAI 独有 |
| CRED-221 | HRW top-K segment | ✗ | ✗ | ✗ | 已完成 | — | **K=3** SegmentTable(pasr.go:4) | 无;HUAKAI 独有 |
| CRED-222 | HRW seed rotation(30d) | ✗ | ✗ | ✗ | 已完成 | — | AccountRing.Seed 30d rotate(hrw_ring.go:58) | 无;HUAKAI 独有 |
| CRED-223 | PASR HasCache-bit preference | ✗ | 🟡usage-cache stats | ✗ | 已完成 | — | HasCache 集合成员优先(pasr.go:8) | 无;HUAKAI 独有 |
| CRED-224 | PASR LoadRate tie-break | ✓load_factor | ✗ | ✗ | 已完成 | — | LoadRate lowest(pasr.go:9) | 无 |
| CRED-225 | PASR loadCap cutoff(0.95) | ✗ | ✗ | ✗ | 已完成 | — | loadCap default **0.95**(pasr.go:64) | 无;HUAKAI 独有 |
| CRED-226 | PASR readOnlySegments mode | ✗ | ✗ | ✗ | 已完成 | — | ReadOnlySegments Lookup-only(pasr.go:95) | 无;HUAKAI 独有 |
| CRED-227 | segment-all-unhealthy → HRW full-ring | 🟡reselect | ✓skip-retry | 🟡next avail | 已完成 | — | empty-hash→HRW full ring(pasr.go:172) | 无 |
| CRED-228 | pre/post-mutation fail split | ✗ | ✗ | ✗ | 已完成 | — | ErrPASRPreMutationFail(safe) vs ErrPASRPostMutationFail(fail-closed)(pasr.go) | 无;HUAKAI 独有 |
| CRED-229 | legacy sticky dual-write 兼容 | ✓openAIStickyLegacyDualWriteTotal(:23) | ✗ | ✗ | 缺失 | P3 | n/a(无 legacy 包袱) | 不适用(HUAKAI 无历史迁移负担) |
| CRED-230 | legacy read-fallback counters | ✓legacyReadFallbackTotal/Hit(:21-22) | ✗ | ✗ | 缺失 | P3 | n/a | 不适用 |
| CRED-231 | affinity cache stats by rule | 🟡atomic counters | ✓ChannelAffinityCacheStats | ✗ | 已完成 | — | routing_reason+metrics(pool/router/metrics.go) | 无 |

---

## L. Per-account proxy / IP 隔离(每字段 = 一行)
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-232 | proxy 绑定 | ✓account.proxy_id | ✓ChannelSettings.Proxy | ✓Auth.ProxyURL | 已完成 | — | provider_accounts.proxy_url(0012) | 无 |
| CRED-233 | proxy protocol 字段 | ✓proxy.go protocol | 🟡URL scheme | 🟡URL | 已完成 | — | URL scheme(proxy_resolver.go) | 无 |
| CRED-234 | proxy host/port 离散列 | ✓host+port | 🟡in URL | 🟡 | 部分完成 | P3 | in URL(非离散列) | 评估是否需离散 host/port 列(对齐 sub2api) |
| CRED-235 | proxy username/password | ✓(明文) | 🟡in URL(明文) | 🟡 | 已完成 | — | proxysecret envelope 加密 | 无;HUAKAI 优于 ref(加密) |
| CRED-236 | proxy status | ✓default active | ✗ | ✗ | 部分完成 | P3 | channelhealth | 评估补 proxy 独立 status |
| CRED-237 | NULL=显式直连 vs 未注册 | ✗ | ✗ | ✗ | 已完成 | — | row-exists+NULL=direct;absent=unregistered(0012 comment) | 无;HUAKAI 独有 |
| CRED-238 | fail-loud on misconfig | 🟡 | ✗(silent direct) | ✗ | 已完成 | — | ErrProxyResolverMisconfigured vs ErrAccountNotFound(proxy_resolver.go) | 无;HUAKAI 独有 |
| CRED-239 | unsupported-transport fail | ✗ | ✗ | ✗ | 已完成 | — | ErrProxyUnsupportedTransport | 无;HUAKAI 独有 |
| CRED-240 | proxy latency probe | ✓proxy_latency_cache.go | 🟡 | ✗ | 部分完成 | P3 | channelhealth | 评估补 proxy 专项 latency probe |

---

## M. 审计 / 可观测(oauth_refresh_audit_events.outcome,0006)— 每 outcome enum = 一行
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-241 | cache_hit | 🟡log | 🟡 | 🟡 | 已完成 | — | (0006) | 无 |
| CRED-242 | refresh_lock_held | 🟡 | ✗ | 🟡 | 已完成 | — | (0006) | 无 |
| CRED-243 | refresh_succeeded | 🟡 | 🟡 | 🟡 | 已完成 | — | (0006) | 无 |
| CRED-244 | refresh_token_rotated(+old/new fingerprint) | ✗ | ✗ | ✗ | 已完成 | — | old/new_token_fingerprint 列(0006) | 无;HUAKAI 独有 |
| CRED-245 | db_version_conflict | ✗ | ✗ | ✗ | 已完成 | — | (0006) | 无;HUAKAI 独有 |
| CRED-246 | invalid_grant_race_recovered | ✗ | ✗ | ✗ | 已完成 | — | (0006) | 无;HUAKAI 独有 |
| CRED-247 | storm_budget_exhausted(+storm_scope) | ✗ | ✗ | ✗ | 已完成 | — | storm_scope CHECK account/provider_endpoint/global(0006) | 无;HUAKAI 独有 |
| CRED-248 | cas_lost | ✗ | ✗ | ✗ | 已完成 | — | (0006) | 无;HUAKAI 独有 |
| CRED-249 | token_malformed | ✗ | ✗ | ✗ | 已完成 | — | (0006) | 无;HUAKAI 独有 |
| CRED-250 | oauth_401_force_refresh | 🟡 | 🟡 | ✓ | 已完成 | — | (0006) | 无 |
| CRED-251 | permanent_disable | 🟡auto-ban | ✓ | ✓ | 已完成 | — | (0006) | 无 |
| CRED-252 | mimicry_applied(+components[]+policy_version) | ✗ | ✗ | ✗ | 已完成 | — | mimicry_components_applied text[] system_rewrite/cache_strip/tool_obfuscation(0006) | 无;HUAKAI 独有 |
| CRED-253 | error_message_redacted(token-safe) | 🟡 | 🟡 | 🟡 | 已完成 | — | OAuth sanitizer(0006) | 无 |
| CRED-254 | request_id/client_protocol/model context | 🟡 | 🟡 | ✗ | 已完成 | — | 3 列(0006) | 无 |
| CRED-255 | oauth_refresh_audit_events 表 | 🟡 | ✗ | ✗ | 已完成 | — | 0006_upstream_credential_management:43-83(11 outcome 码) | 无 |

## N. Vendor × auth_mode 续期 adapter 注册表(HUAKAI `credentialworker/mode_refresh.go:69-110`)
18 adapters;ref 无等价声明式注册表。**整体能力行**(逐 adapter 已在 C 段覆盖,此处记注册表机制本身)。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-256 | 声明式 18-adapter 续期注册表 | ✗ | ✗ | ✗ | 已完成 | — | mode_refresh.go:69-110(staticModeAdapter/legacyOAuth/metadataTokenAdapter/geminiBuiltinClientOAuth/copilotOAuthModeAdapter/windsurfManualModeAdapter/operatorOAuth 等) | 无;HUAKAI 独有,paused 项随 CRED-073 |

---

## O. AI provider 管理端点 + CLIProxyAPI 管理差距(151-ref 06 K段 / 12C CP-* / 12D AI-*)

来源: `151-ref/06-...:K段`(`/admin/v1/*` 端点) · `12C`(CP-A/CP-M auth-files+management) · `12D`(AI-001..AI-020)。前端基线: `frontend/app/accounts`(导航 disabled)。

### O1. provider account / credential / pool 管理端点(HUAKAI 后端存在)
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-257 | provider-accounts list/create/get/update | ✓ | ✓ | ✓ | 后端有·前端缺 | P1 | `/admin/v1/provider-accounts`(+/{id});`frontend/app/accounts/page.tsx`(导航 disabled) | 启用 accounts 导航,补完整 CRUD UI(12D AI-001) |
| CRED-258 | provider-account delete | ✓ | ✓ | n/a | 缺失 | P2 | providerAccounts client 无 delete(12D AI-002) | 补 account delete 端点+UI |
| CRED-259 | provider-account test | ✓ | ✓ | ✓ | 后端有·前端缺 | P2 | `/admin/v1/provider-accounts/{id}/test` | 补 test UI 入口(12D AI-003) |
| CRED-260 | provider-account health | ✓ | 🟡 | ✓ | 后端有·前端缺 | P2 | `/admin/v1/provider-accounts/{id}/health`;前端部分 | 补 health 面板(12D AI-004) |
| CRED-261 | clear-rate-limit | 🟡 | ✓ | 🟡 | 后端有·前端缺 | P2 | `/admin/v1/provider-accounts/{id}/clear-rate-limit` | 补清限流按钮 |
| CRED-262 | enabled toggle | ✓ | ✓ | ✓ | 后端有·前端缺 | P2 | `/admin/v1/provider-accounts/{id}/enabled` | 补启停开关 UI |
| CRED-263 | channel-health pause/resume/force-active | 🟡 | ✓ | ✓ | 后端有·前端缺 | P2 | `/admin/v1/provider-accounts/{id}/channel-health/{pause,resume,force-active}`;`/v1/admin/channel-health` | 补渠道健康控制 UI |
| CRED-264 | credentials CRUD | ✓ | ✓ | ✓auth-file | 后端有·前端缺 | P1 | `/admin/v1/provider-accounts/{id}/credentials`(+/{credential_id}) | 补凭证管理 UI |
| CRED-265 | credential rotate | ✓ | 🟡 | 🟡 | 后端有·前端缺 | P1 | `.../credentials/{credential_id}/rotate` | 补手动轮转 UI(关联骨架 F-01) |
| CRED-266 | credential state(set/revoke/expire) | 🟡 | 🟡 | 🟡 | 后端有·前端缺 | P2 | `.../credentials/{credential_id}/state` | 补 state 控制 UI(骨架 A-07 emergency revoke) |
| CRED-267 | credential-acquisitions flow(list/get/callback/cancel/finalize) | 🟡 | ✗ | ✓OAuth flow | 后端有·前端缺 | P1 | `/admin/v1/provider-accounts/{id}/credential-acquisitions/{flow_id}/{callback,cancel,finalize}` | 补 OAuth 接入向导 UI(12D AI-005) |
| CRED-268 | pools list/get | 🟡 | 🟡 | ✗ | 部分完成 | P2 | `/admin/v1/pools`(+/{id}) | 补池管理 UI |
| CRED-269 | account-modes / providers / channels 端点 | ✓ | ✓ | 🟡 | 部分完成 | P2 | `/admin/v1/{account-modes,providers,channels}` | 补 channel CRUD UI(12D AI-008) |

### O2. 凭证导入/获取入口(HUAKAI 后端,前端/TUI 缺)
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-270 | credentials/paste | 🟡 | ✗ | ✓ | 后端有·前端缺 | P2 | `/admin/v1/credentials/paste` | 补粘贴导入 UI |
| CRED-271 | credentials/cli-import | ✗ | ✗ | ✓CLI auth | 后端有·前端缺 | P2 | `/admin/v1/credentials/cli-import`;credentialacq/cli_import.go | 补 CLI 导入向导 |
| CRED-272 | credentials/csv-import | ✗ | ✓batch | ✗ | 后端有·前端缺 | P3 | `/admin/v1/credentials/csv-import` | 补 CSV 批量导入 UI |
| CRED-273 | credentials/json-import | ✗ | ✗ | ✓auth-file | 后端有·前端缺 | P3 | `/admin/v1/credentials/json-import` | 补 JSON 导入 UI |
| CRED-274 | credentials/oauth-init+oauth-callback | 🟡 | ✗ | ✓ | 后端有·前端缺 | P1 | `/admin/v1/credentials/{oauth-init,oauth-callback}` | 补 OAuth init/callback 前端 |
| CRED-275 | credentials/renew-status | ✗ | ✗ | 🟡 | 部分完成 | P2 | `/admin/v1/credentials/renew-status`;`frontend/app/renew` 页存在 | 完善 renew 状态页(已有页面骨架) |

### O3. CLIProxyAPI 管理 parity 硬缺口(12C CP-A-008/CP-M-* + 12D AI-006/007/PROTO-013)
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-276 | auth-files list/download/upload/delete/status/fields | ✗ | ✗ | ✓management routes(server.go) | 缺失 | P2 | 无等价 token 文件管理后台(12C CP-A-008,12D AI-006/AI-007) | 评估是否需 auth-file CRUD(HUAKAI 用 DB credential store,定位不同) |
| CRED-277 | OAuth auth-URL helpers(anthropic/codex/gemini-cli/antigravity/kimi/xai) | ✗ | ✗ | ✓management routes(CP-A-009) | 部分完成 | P2 | 有 credential acquisition helpers,缺成熟管理 UI/TUI | 补统一 OAuth auth-URL 管理面板 |
| CRED-278 | CLI/TUI 本地账号接入流 | ✗ | ✗ | ✓internal/cmd+internal/tui(CP-A-011) | 缺失 | P3 | 不具备本地 CLI/TUI 账号接入体验(12D PROTO-014) | 评估是否提供 CLI/TUI(云端 SaaS 架构,优先级低) |
| CRED-279 | config YAML 热读取/更新 | ✗ | ✗ | ✓/v0/management config(CP-M-003) | 缺失 | P3 | 无同等 config-file API(架构不同) | 不直接对标(HUAKAI=云端 DB 配置) |
| CRED-280 | provider keys 管理(gemini/claude/codex/openai-compat/vertex) | 🟡 | ✓ | ✓management(CP-M-010/CP-M-018) | 部分完成 | P2 | DB/credential store 管理,前端有限 | 补 provider-keys 管理 UI |
| CRED-281 | proxy-url per-provider 管理 | ✓ | 🟡 | ✓config(CP-M-011) | 后端有·前端缺 | P2 | proxy_url 后端有(0012),无 UI(12D AI-020) | 补 proxy 管理 UI |
| CRED-282 | routing strategy 配置(round-robin/priority/fill-first) | 🟡 | ✓ | ✓config/management(CP-M-014/CP-S-001..003) | 后端有·前端缺 | P2 | router/pool 策略后端有,前端配置缺(12D PROTO-019) | 补路由策略配置 UI |
| CRED-283 | model alias / oauth-excluded-models | ✗ | ✓ | ✓management(CP-M-015/016,CP-S-009/010) | 缺失 | P3 | 未确认等价(12D PROTO-012) | 评估补 per-OAuth model alias/exclude |
| CRED-284 | force-model-prefix | ✗ | ✗ | ✓config(CP-M-012) | 缺失 | P3 | 未确认(12D PROTO-018) | 评估补 force-model-prefix |
| CRED-285 | smart model fallback 配置 | ✗ | ✓ | ✓Amp module(CP-S-011) | 后端有·前端缺 | P2 | model fallback 后端有,前端/配置缺(12D PROTO-020) | 补 fallback 链配置 UI |
| CRED-286 | virtual parents / grouped auths | ✗ | ✗ | ✓scheduler(CP-S-007) | 缺失 | P3 | 未确认 | 评估补虚拟父账号/分组 auth |
| CRED-287 | Retry-After for model cooldown | 🟡 | 🟡 | ✓scheduler 返回 429 Retry-After(CP-S-008) | 部分完成 | P2 | 需协议测试确认 | 验证 model-cooldown Retry-After 透传 |

---

## P. 骨架补充缺口(provider-account-mgmt.md MISSING/PARTIAL,未在上表显式编号者)
| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CRED-288 | 计划性自动 API-key 轮转(scheduled) | ✓operator 流 | ✓operator 流 | ✗ | 缺失 | P0 | `needs_rotation` state 存在(types.go:52)但**无后台 job 处理=死信号**;scheduler 仅处理 OAuth refresh(骨架 F-02) | **补 rotation_schedule/rotation_due_at + 后台轮转 job**(P0:90d/1y key TTL,否则静默宕机) |
| CRED-289 | 预轮转双活窗口(零停机 key 切换) | 🟡 | 🟡 | ✗ | 部分完成 | P2 | `grace_until`(0016)覆盖 OAuth refresh 重试,非新旧 key 重叠(骨架 F-03) | 补 API-key 轮转的 new-vs-old 双活窗口 |
| CRED-290 | force-refresh-token NOW(不轮转) | ✓ | ✓ | ✓ | 缺失 | P1 | grep force.refresh/refresh.now 全无(骨架 I-03);workaround=rotate(毁旧建新)或手改 refresh_before_at | **补 force-refresh admin 端点**(最高频运维救援:token 被服务端 revoke 后立即重 auth) |
| CRED-291 | per-account 时序用量历史 API | ✓ | ✓ | 🟡 | 部分完成 | P2 | health snapshot 给当前配额,无"近7天用量"端点(骨架 I-05);`provider_account_health_handler.go` | 补 per-account 时序用量端点(容量规划/对账/异常检测) |
| CRED-292 | 统一账号 dashboard 查询面 | 🟡 | 🟡 | 🟡 | 部分完成 | P3 | health+catalog+quota+metrics 分散 3-4 端点,无单一 `/admin/v1/accounts/{id}/dashboard`(骨架 L-04) | 补单一聚合投影端点 |
| CRED-293 | 配额耗尽/认证失败 告警 webhook | ✓email-on-quota | ✓email-on-quota | ✗ | 缺失 | P1 | scheduler_outbox 仅内部事件,无外部推送(PagerDuty/Slack/email)(骨架 L-05) | **补外部告警投递**(配额耗尽/auth 永久失败时推送) |
| CRED-294 | per-request 账号排除集(in-flight skip) | 🟡 | 🟡 | ✓disabled skip | 部分完成 | P2 | health 降级隐式排除,无显式"临时跳过此账号 ID"机制(骨架 E-08) | 补 per-request 账号排除/暂停新选择(保留 in-flight 排空) |
| CRED-295 | scheduled health-expiry sweep(throttled/cooldown 无 OAuth token 账号) | 🟡 | 🟡 | ✓cooldown | 部分完成 | P3 | RunOnce 仅覆盖有 expires_at 账号(骨架 C-07);credentialworker/scheduler.go:155-172 | 补专门 health-expiry 扫描覆盖无 OAuth token 的 throttled/cooldown 账号 |
| CRED-296 | scheduler transactional outbox | ✗ | ✗ | ✗ | 已完成 | — | `scheduler_outbox`(0001:313-336)(骨架 G-04) | 无;HUAKAI 独有 |
| CRED-297 | shadow / canary 流量切分 | ✗ | ✗ | ✗ | 已完成 | — | dispatcher.go:23-25 shadow+canary modes(骨架 E-07) | 无;HUAKAI 独有 |
| CRED-298 | 8-axis 加权选择 | ✗ | 🟡weight | ✗ | 已完成 | — | weight_priority/load_rate/last_used/recent_error_rate/recent_latency/quota_headroom/fairness_debt/snapshot_freshness(0001:239-246)(骨架 E-02) | 无;HUAKAI 独有 |

---

## 摘要 · 关键合并结论

1. **HUAKAI 凭证生命周期/多 vendor OAuth/storm 控制/PASR-HRW 路由 显著领先 3 ref**:加密静态存储(6-tuple AAD)、credential_version CAS、3-scope storm budget+circuit breaker、12-value refresh outcome 审计、19-row vendor×mode 声明式契约、18-adapter 续期注册表、PASR/HRW(K=3/loadCap 0.95/seed-rotation/pre-post-mutation split)— 这些在任何 ref 均无等价(CRED-094..112/125/127/128/143..152/157..165/175..192/195/217..228/237..239/244..256/296..298)。

2. **P0 真缺口(直接商业/可用性阻塞)**:
   - **CRED-288 计划性自动 API-key 轮转**(`needs_rotation` 死信号,无后台 job)。

3. **P1 真缺口**:
   - CRED-078..085 **xAI/Grok + Kimi OAuth 获取流**(cliproxy 独有,HUAKAI 仅 VendorGrok 常量/无 Kimi);
   - CRED-156/161 **加密密钥轮转 + 外部 vault 集成**(SOC-2 阻塞);
   - CRED-201 **per-(account,model) 配额差异化**;
   - CRED-290 **force-refresh admin 端点**(最高频运维);
   - CRED-293 **告警 webhook**;
   - CRED-031/032/034 new-api **隐私/合规透传门**(service_tier/inference_geo/safety_identifier);
   - CRED-257/264/265/267/274 **provider-account/credential/OAuth 接入前端闭环**(后端有·前端缺,导航 disabled)。

4. **new-api 字段级独占**=透传开关集(A3:service_tier/inference_geo/speed/safety_identifier/store/obfuscation/claude_beta_query,CRED-027..036)+ status_code_mapping/param_override/header_override/multi_key_mode/tag/test_model(CRED-013/014/015/172/019/011)— 全 ref/HUAKAI 零覆盖或部分,是 new-api 唯一稳定优势。

5. **sub2api 字段级独占**=per-provider ProviderRefreshPolicy 三元组(OnRefreshError/OnLockHeld/FailureTTL,CRED-129..137)+proxy 一等实体离散列(CRED-234)+session_window 三元组(CRED-052)+legacy sticky dual-write 观测(CRED-229/230,HUAKAI 无历史包袱不适用)。

6. **cliproxy 字段级独占**=xAI/Grok+Kimi 获取(CRED-078..085)+auth-file 管理后台+CLI/TUI 接入+config YAML 热更+Amp 生态(CRED-276..287),其中 auth-file/TUI/config-YAML 因架构不同(云端 SaaS vs 本地代理)优先级低,xAI/Kimi 获取流为真 P1。

**行数统计见文件末尾(298 条 CRED 行)。**


# ====================  模块 D  ====================
# 标杆 · 路由调度/渠道健康/限流

模块: 路由调度 / 渠道健康 / 限流 / 重试 / 故障转移 / 冷却
合并自三源(去重后规范化):
1. 大功能树骨架 `wt/treeview/docs/process/feature-tree/routing-loadbalance.md`(分支 `fix/hermes-phase-1-e33d940`)
2. 字段级细树 `feature-audit/04-routing-health-fine.md`(HUAKAI `wt/treeview`@e89d7fce, refs new-api / sub2api / cliproxy)
3. 151 参考: `151-ref/04-comparison-missing-tree.md` + `151-ref/12{A,B,C,D}` (PROTO-019/020 routing UI、AI-018 route admin UI、CP-M/CP-S cliproxy 调度行)

云端基线: HUAKAI `origin/fix/hermes-phase-1-e33d940@e89d7fce` | sub2api `635ad81c` | new-api `adc390c5` | cliproxy `3abfc83d`

## 列说明
- **ID** `ROUTE-001`… 全模块连续编号(路由策略 / 限流 scope+window / 健康阈值&定时器 / 重试&退避 / 故障转移&冷却 / 熔断 / PASR runtime / 混路风险 / 可观测&Admin / UI 缺口)。
- **sub2api / new-api / cliproxy** refs 是否实现: ✓ 有 / ✗ 无 / 🟡 部分(括注 file+常量/默认值)。
- **HUAKAI状态** 六级(禁虚标): 已完成 / 部分完成 / 后端有·前端缺 / 缺失 / 未做 / 未合并main。
- **优先级** P0-P3 / —(已完成或纯 ref-only 无缺口记 —)。
- **证据** pkg/file(:line 或常量名/默认值)。
- **推进动作** concrete next step。

---

## A. 路由 / 选择策略 (strategy-by-strategy)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| ROUTE-001 | 优先级严格分层路由 (strict_priority, 升序逐层耗尽) | ✓(scheduler.go:432 Priority compare) | ✓(ability.go:67 `Order("priority DESC")`) | ✓(selector.go bestPriority bucket) | 已完成 | — | `pool/router/default_selector.go:225-229` rankFresh `a.Priority<b.Priority` 主键; `sql/migrations/0008:149` selection_mode 'strict_priority' | 无 |
| ROUTE-002 | 加权随机 — score-shifted 多因子 (sub2api 累积权重) | ✓(scheduler.go:543-584 cumulative pick) | ✗ | ✗ | 部分完成 | P1 | `default_selector.go:rankFresh` 仅对 top-tier 等位账号 `rand.Shuffle`(:236-241); weight 字段读出但 **rankFresh 排序键不含 weight** | 将 weight 接入 rankFresh 概率抽样(weight→cumulative 或 score 项),使 operator 权重真正生效 |
| ROUTE-003 | 加权随机 — `weight+10` 累积 (new-api 算法) | ✗ | ✓(ability.go:127 `weightSum+=Weight+10`; :130 GetRandomInt) | ✗ | 部分完成 | P1 | 同 ROUTE-002;weight 列存于 `sql/migrations/0008:165`, `registry/registry.go:73` 读出未传入 selector | 同 ROUTE-002(可借 new-api `weight+10` 防零权重饿死) |
| ROUTE-004 | 最小负载 (least in-flight / LoadRate) 次级排序 | ✓(scheduler.go:736 `weights.Load*loadFactor`) | ✗ | ✗ | 已完成 | — | `default_selector.go:230` rankFresh `a.LoadRate<b.LoadRate` 第2键 | 无 |
| ROUTE-005 | LRU 近似轮询 (LastUsedAt tie-break) | ✗ | ✗ | ✗ | 已完成 | — | `default_selector.go:233` `LastUsedAt.Before` 第3键(非严格 RR) | 无 |
| ROUTE-006 | 同优先级随机洗牌 (anti-herd top-tier) | ✗ | ✗ | ✗ | 已完成 | — | `default_selector.go:236-241` rand.Shuffle 等位账号 | 无 |
| ROUTE-007 | 扁平 round-robin (cursor%len) | ✗ | ✗ | ✓(selector.go:313 `RoundRobinSelector.Pick` cursor++) | 缺失 | P3 | grep 无 flat-RR selector | 评估是否需要(HUAKAI 用 HRW+rankFresh 已覆盖均衡;flat-RR 仅 cliproxy CLI 场景) |
| ROUTE-008 | 二级 round-robin (group→member, gemini virtual-parent) | ✗ | ✗ | ✓(selector.go:281-307 `groupByVirtualParent`+per-group cursor) | 缺失 | P3 | grep 无 2-level RR | 同 ROUTE-007;低优先,HRW 段路由更优 |
| ROUTE-009 | RR cursor-map 容量驱逐 (有界内存) | ✗ | ✗ | ✓(selector.go:324 `ensureCursorKey` flush≥limit) | 缺失 | — | 依赖 ROUTE-007/008 | 随 ROUTE-007/008 评估 |
| ROUTE-010 | RR 随机初始 cursor (anti-thundering-herd) | ✗ | ✗ | ✓(selector.go:288 `rand.IntN(len(parentOrder))`) | 缺失 | — | 依赖 ROUTE-007/008 | 随 ROUTE-007/008 评估 |
| ROUTE-011 | Fill-first / burn-one-account (确定性 ID 序) | ✗ | ✗ | ✓(selector.go:359 `FillFirstSelector.Pick`) | 缺失 | P2 | grep 无 fill-first selector | 评估 burn 场景(单账号打满再切)是否商业需要;HUAKAI 当前 spread-load |
| ROUTE-012 | 重试推进到下一 priority 索引 | ✗ | ✓(ability.go:86 `priorityToUse=priorities[retry]`) | ✗ | 部分完成 | P2 | AttemptSeq 传入但 caller-driven;`pool/router/types.go` AttemptSeq | 在 selector 内显式按 attempt 跳 priority band,减少 caller 协调负担 |
| ROUTE-013 | 跨组 auto-group fallthrough | ✗ | ✓(channel_select.go:118 `GetRandomSatisfiedChannel(autoGroup)`; :137 crossGroupRetry) | ✗ | 已完成 | — | `subscriptionenforce/gate.go` GroupPolicyGate UserGroup | 无(HUAKAI 经 GroupPolicy 门控实现) |
| ROUTE-014 | HRW / rendezvous 哈希 (splitmix64+SHA256, 30天种子轮换) | ✗ | ✗ | ✗ | 已完成 | — | `pool/router/hrw_ring.go` HRWScore/TopK/Top3; **超越同类** | 无 |
| ROUTE-015 | HRW tie-break account_id 升序 (确定性) | ✗ | ✗ | ✗ | 已完成 | — | `hrw_ring.go:TopK` `scoredAll[i].id<[j].id` | 无 |
| ROUTE-016 | PASR 前缀段路由 K=3 | ✗ | 🟡(channel-affinity key cache,非 HRW 段) | ✗ | 已完成 | — | `pool/router/pasr.go`+`prefix_segment.go` `PASRSegmentSize=3`; **超越同类** | 无 |
| ROUTE-017 | Top-K 候选采样 (再 shuffle/score) | ✓(scheduler.go selectTopKOpenAICandidates heap) | ✗ | ✗ | 已完成 | — | `default_selector.go:topK`; policy.TopKDefault/BroadTopK/OperatorScoring | 无 |
| ROUTE-018 | 缓存局部性加分 (score 项) | 🟡(channel_affinity billing) | 🟡(token-rate modes) | ✗ | 已完成 | — | `blend.go LocalityBonus=1.0`; `locality.go LocalityWeight=1.0` | 无 |
| ROUTE-019 | Headroom (1-LoadRate)*weight 加分 | ✗ | ✗ | ✗ | 已完成 | — | `blend.go (1-LoadRate)*HeadroomWeight`; `locality.go HeadroomWeight=0.3` | 无 |
| ROUTE-020 | Degraded 惩罚 (score 项) | ✗ | ✗ | ✗ | 已完成 | — | `blend.go -DegradedPenalty`; `locality.go DegradedPenalty=2.0` | 无 |
| ROUTE-021 | 协议族路由 (OpenAI/Anthropic/Gemini/…) | ✓(isAccountTransportCompatible) | 🟡 channel type | ✓(preferWebsocket) | 已完成 | — | `gateway/upstream_dispatcher_hcsf.go:210` 多 case; `pool/router/gates.go:194-203` protocolFamilyGate | 无 |
| ROUTE-022 | 混合-provider 单请求路由 (mixed channel) | ✗ | ✗ | ✓(conductor pickMixed/executeMixedOnce) | 缺失 | P3 | protocolFamilyGate 强制单协议族;无 pickMixed | 评估混路场景需求(HUAKAI 单族更安全,见 ROUTE-070 风控) |
| ROUTE-023 | 上下文窗口感知路由 (req-tokens>window→skip/fallback) | ✗ | ✗ | ✗ | 部分完成 | P1 | `registry/registry.go:49` ContextWindow 字段传入 RoutePlan; FallbackClass='context_window' 枚举 (:78) 预留但 **不参与 gate 决策** | 实现 ContextWindowGate: 请求 token 数超模型窗口→剔除/触发 fallback 链 |
| ROUTE-024 | 能力标记路由 (vision/tools/streaming 实际执行) | 🟡 | 🟡 | 🟡 | 部分完成 | P1 | `gates.go:51-63` CapabilityGate 接口+`types.go:38` CapabilityFlags 已接线,但生产实现 **AllowAll 全过** (`db/billing/pool_accounts.sql.go:707` 注释) | 将 CapabilityGate 生产实现从 AllowAll 改为真实能力匹配,防 vision/tools 请求路由到不支持 provider→上游400 |
| ROUTE-025 | 模型别名/归一化 | ✓ | ✓ | ✓ | 已完成 | — | `sql/migrations/0008:95-122` model_aliases; `registry/errors.go:21` ErrUnknownModel | 无 |
| ROUTE-026 | 模型通配符模式匹配 ('*'/suffix) | ✓ | ✓ | ✓ | 已完成 | — | `routeadmin/types.go:18-51` model_pattern_match; `subscriptionenforce/routes_repo_postgres.go:27-62` | 无 |
| ROUTE-027 | 指定/钉死账号覆盖 (pinned account) | ✓(StickyAccountID) | ✓(distributor TokenSpecificChannelId) | ✓(conductor pinnedAuthIDFromMetadata) | 部分完成 | P2 | 仅 ExcludedAccounts(`pool/router/types.go`);无 pin 字段 | 增加 PinnedAccountID 请求字段,支持 operator/调试钉死单账号 |
| ROUTE-028 | 延迟优化正向路由 (prefer fastest / EWMA latency) | ✗(sub2api EWMA α=0.2 on TTFT) | 🟡(channel_latency EWMA 排名) | ✗ | 缺失 | P2 | P99 仅用于降级负向门控; rankFresh 无测量延迟正向偏好 | 增加 EWMA TTFT 排名项到 score,主动选快的(LiteLLM lowest_latency 同类) |
| ROUTE-029 | 成本感知路由 (prefer cheapest) | ✓(channel weight+cost 混合) | ✗ | ✗ | 缺失 | P2 | `billing/settler.go` 成本追踪有,但 SelectionRequest/rankFresh 无成本权重 | 增加 cost 权重项,多定价区间 provider 引流到低成本通道(降 COGS) |
| ROUTE-030 | 地理/区域路由 (region affinity) | ✗ | ✗ | ✗ | 缺失 | P3 | grep geo/region/nearest → 0 routing hits | 全球部署后实现区域亲和(将亚洲流量固定到亚洲 provider) |

## B. 会话粘性 / 亲和性

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| ROUTE-031 | Prompt-hash 粘性 (缓存亲和, SHA-256 前缀) | ✓(scheduler selectBySessionHash Redis+TTL) | 🟡(channel_affinity usage cache) | ✓(selector.go:484 SessionAffinitySelector) | 已完成 | — | `cache_routing/prompt_hash.go:1-37`; `default_selector.go:123-131` upsert sticky | 无 |
| ROUTE-032 | 粘性绑定持久化 + TTL | ✓ | 🟡 | ✓(cache.Put after pick) | 已完成 | — | `sql/migrations/0001:199-215` sticky_bindings expires_at; `db/billing/pool_sticky_bindings.sql.go:14-76` | 无 |
| ROUTE-033 | 粘性 fresh-pick 写回 | ✗ | 🟡 | ✓ | 已完成 | — | `default_selector.go` fresh→sticky.Upsert best-effort | 无 |
| ROUTE-034 | 粘性绑定失效 / 过期清理 | ✗ | ✗ | ✗ | 已完成 | — | `sql/migrations/0001:318` audit 'sticky_binding_invalidated'; DeleteExpiredStickyBindings | 无 |
| ROUTE-035 | 粘性多源 session-id 提取 (8 源) | 🟡 | 🟡 | ✓(selector.go:572 8源: user_id/X-Session-ID/X-Amp-Thread-Id/X-Client-Request-Id/conversation_id/body-hash 等) | 部分完成 | P2 | HUAKAI SessionHash/ContinuationKey 上游预计算(单源) | 扩展为多源 session-id 提取(对齐 cliproxy 8 源),提升跨客户端粘性命中 |
| ROUTE-036 | prev-response-id 粘性层 (对话连续) | ✓(scheduler SelectAccountByPreviousResponseID layer1) | ✗ | 🟡(conversation_id 为 8 源之一) | 部分完成 | P2 | ContinuationKey→RampAdmissionKey 基础,但无 prev-response-id 专用层 | 增加 prev-response-id 优先粘性层(Responses API 多轮连续性) |
| ROUTE-037 | 多轮对话续流路由 (continuation) | ✓ | ✗ | 🟡 | 部分完成 | P2 | ContinuationKey 字段已传播(`types.go:30-80`); proto MidStreamFallbackContinuation 枚举;中流续转 P-0 验证器强拒(`envelope_validate.go:499`),续流选择逻辑待 P-8 | 落地 P-8 续流策略,放开验证器对 continuation 的强拒 |

## C. 选择器 rollout 模式 (dispatcher) — HUAKAI 独有

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| ROUTE-038 | `default` 模式 | ✗ | ✗ | ✗ | 已完成 | — | `pool/dispatcher/dispatcher.go:DispatchModeDefault` | 无 |
| ROUTE-039 | `shadow` 模式 (异步对比,无 mutation) | ✗ | ✗ | ✗ | 已完成 | — | `dispatcher.go:DispatchModeShadow` Slots=nil+Claims=nil+ReadOnlySegments 三层防护 | 无 |
| ROUTE-040 | shadow 异步队列 cap=1024 | ✗ | ✗ | ✗ | 已完成 | — | `dispatcher.go shadowQueueCap=1024` | 无 |
| ROUTE-041 | shadow 单 Select timeout=500ms | ✗ | ✗ | ✗ | 已完成 | — | `dispatcher.go shadowSelectTimeout=500ms` (context.WithoutCancel) | 无 |
| ROUTE-042 | shadow Stop drain timeout=30s | ✗ | ✗ | ✗ | 已完成 | — | `dispatcher.go defaultDispatcherDrainTimeout=30s` (env HUAKAI_DISPATCHER_DRAIN_TIMEOUT_SECONDS) | 无 |
| ROUTE-043 | `canary` 模式 (fnv64a bucket %100<pct) | ✗ | ✗ | ✗ | 已完成 | — | `dispatcher.go:DispatchModeCanary` dispatchCanary shouldSample | 无 |
| ROUTE-044 | CanaryPercent/ShadowPercent 字段 [0,100] + 采样 salt | ✗ | ✗ | ✗ | 后端有·前端缺 | P2 | `dispatcher.go CanaryPercent/ShadowPercent` validate 0-100; SamplingSalt | 增加运行时 admin API 动态调整百分比(现由 ENV 驱动,改需重启,灰度不友好)→ 见 ROUTE-085 |
| ROUTE-045 | `pasr-primary` 模式 (pre-mutation fallback OK) | ✗ | ✗ | ✗ | 已完成 | — | `dispatcher.go:DispatchModePASRPrimary` | 无 |
| ROUTE-046 | `pasr-strict` 模式 (任何 error fail-closed) | ✗ | ✗ | ✗ | 已完成 | — | `dispatcher.go:DispatchModePASRStrict` | 无 |
| ROUTE-047 | pre/post-mutation error 分流 (safe fallback vs fail-closed) | ✗ | ✗ | ✗ | 已完成 | — | `pasr.go ErrPASRPreMutationFail/ErrPASRPostMutationFail`; `retry.go canFallbackAfterPASRError` | 无 |

## D. 资格门控 (per-candidate gates)

门控链顺序: Tenant→Lifecycle→Channel→Protocol→Model→Capability→Credential→Health→GroupPolicy→Exclusion (`gates.go` ordered)。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| ROUTE-048 | 租户隔离门 | 🟡 group-scoped | 🟡 | ✗ 单租户 | 已完成 | — | `gates.go TenantGate/GateFailureTenantFilter` | 无 |
| ROUTE-049 | 生命周期门 (schedulable) | ✓(IsSchedulable) | ✓(channel Status) | ✓(Disabled/StatusDisabled) | 已完成 | — | `gates.go LifecycleGate` | 无 |
| ROUTE-050 | Channel 门 | 🟡 | ✓ | ✗ | 已完成 | — | `gates.go ChannelGate` | 无 |
| ROUTE-051 | 协议/传输族门 | ✓(isAccountTransportCompatible) | 🟡 | ✓ | 已完成 | — | `gates.go protocolFamilyGate account.ProtocolFamily!=req.ProtocolFamily` | 无 |
| ROUTE-052 | 模型支持/模型冷却门 | ✓(isAccountRequestCompatible) | ✓(ability group+model 行) | ✓(supportedModelSet) | 已完成 | — | `gates.go modelRateLimitGate` 读 ModelRateLimits[key].RateLimitResetAt | 无 |
| ROUTE-053 | 凭据门 | ✓ | ✓ | ✓ | 已完成 | — | `gates.go CredentialGate` | 无 |
| ROUTE-054 | 健康状态门 | ✓(IsSchedulable) | ✓(only enabled) | ✓(isAuthBlockedForModel) | 已完成 | — | `gates.go ProviderAccountHealthGate` states healthy/throttled/revoked/cooldown | 无 |
| ROUTE-055 | 组/订阅策略门 | ✓(RequirePrivacySet) | ✓(group access) | ✗ | 已完成 | — | `gates.go GroupPolicyGate`; routes.user_group_match | 无 |
| ROUTE-056 | 已试账号排除门 (per-request) | ✓(ExcludedIDs) | 🟡 隐式 | ✓(conductor tried map) | 已完成 | — | `gates.go exclusionGate req.ExcludedAccounts[ID]` | 无 |
| ROUTE-057 | scored-band 门 (load cap 拒绝) | ✗ | ✗ | ✗ | 已完成 | — | `pasr.go LoadRate>=loadCap→GateFailureScoredBand` | 无 |
| ROUTE-058 | 门链每次 Select 单次预备 (无 N×DB) | ✗ | ✗ | ✗ | 已完成 | — | `gates.go ForSelection/SelectionGatePreparer.PrepareForSelection` | 无 |
| ROUTE-059 | `all_channels_degraded` 专属错误 | ✗ | ✗ | ✗ | 已完成 | — | `types.go ErrAllChannelsDegraded`; reason.onlyFailure(Health) | 无 |

## E. 渠道健康 FSM — 状态 + 每信号阈值 + 定时器

`channelhealth.Policy` (types.go DefaultPolicy)。6 态: active/degraded/cooling_down/ramping/disabled/manual_paused。**超越同类**(new-api 仅 enable/disable 二值)。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 (默认值) | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| ROUTE-060 | 正式多态 FSM (6 态) | 🟡 status flags | 🟡 enabled/auto-disabled 二值 | 🟡 ready/cooldown/blocked/disabled per-auth | 已完成 | — | `channelhealth/types.go` 6 states HealthState | 无 |
| ROUTE-061 | MinSampleCount 决策前样本门 =10 | 🟡 timeout-counter | ✗ single test | ✗ | 已完成 | — | `types.go MinSampleCount=10` | 无 |
| ROUTE-062 | MinObservation 观测窗 =1m | ✗ | ✗ | ✗ | 已完成 | — | `types.go MinObservation=1m` | 无 |
| ROUTE-063 | 错误率阈值 =50% / 窗口 5m / 冷却 5m | 🟡 per-error 立即 | 🟡 status-code | 🟡 | 已完成 | — | `types.go ErrorRateThresholdPct=50/ErrorRateWindow=5m/ErrorRateCooldown=5m`; service.go:errorRateDecision | 无 |
| ROUTE-064 | 延迟 p99 阈值 =30000ms / 窗口 5m / 冷却 5m | ✗ | ✗ | ✗ | 已完成 | — | `types.go LatencyP99ThresholdMS=30000/LatencyWindow=5m/LatencyCooldown=5m`; `window.go:percentile99` idx=len*0.99+0.5 | 无 |
| ROUTE-065 | 限速命中率阈值 =40% / 窗口 5m / 默认冷却 5m | ✓(default 5s apply429Fallback) | ✗ | 🟡 | 已完成 | — | `types.go RateLimitHitRateThresholdPct=40/RateLimitWindow=5m/DefaultRateLimitCooldown=5m`(被 sig.RateLimitResetAt 覆盖) | 无 |
| ROUTE-066 | 上游 5xx 率阈值 =50% / 窗口 5m / 冷却 5m | ✗ retry-transient | 🟡 status config | 🟡 503→1min | 已完成 | — | `types.go Upstream5xxRateThresholdPct=50/Upstream5xxWindow=5m/Upstream5xxCooldown=5m`; service.go degraded→cooldown 升级 | 无 |
| ROUTE-067 | Ban 信号冷却 min=24h / max=72h | 🟡 401→permanent | ✓ keyword/status | 🟡 401/403→30m,404→12h | 已完成 | — | `types.go BanSignalMinCooldown=24h/BanSignalMaxCooldown=72h`; service.go isBanSignal | 无 |
| ROUTE-068 | Ban 需 operator-ack (无自动 ramp) | ✗ | ✗ | ✗ | 已完成 | — | service.go `AutomaticPostBanRamp=false`→recoveryBlockedReason=operator_ack_required | 无 |
| ROUTE-069 | Degraded→cooldown 2-strike 升级 | ✗ | ✗ | ✗ | 已完成 | — | service.go upstream5xx/latencyDecision: 仅当已 Degraded 且同 ReasonClass 才升级 | 无 |
| ROUTE-070 | local-gateway-5xx 不计入健康扣分 | ✗ | ✗ | ✗ | 已完成 | — | `window.go summarizeSamples LocalGateway5xxHits` 不计; service.go 从 attempts 减去 | 无 |
| ROUTE-071 | client-malformed 不计入健康扣分 | ✗ | ✗ | ✗ | 已完成 | — | window.go SignalClientMalformed 不计 | 无 |
| ROUTE-072 | 连续健康分 + 半衰 (score 0-1, 半衰 10m) | ✗ | ✗ | ✗ | 已完成 | — | `gateway/health_fsm.go` score; `sql/migrations/0022:20` score numeric | 无 |

## F. 渠道健康 — 恢复 ramp + 手动覆盖

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 (默认值) | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| ROUTE-073 | 分级恢复 ramp 阶段 0→1→10→50→100% | ✗ 二值 re-enable | ✗ 二值 | ✗ 到期二值 | 已完成 | — | service.go:advanceRamp `0→1→10→50→100%`→active; **超越同类** | 无 |
| ROUTE-074 | Ramp 准入哈希 (fnv32a%100<pct) | ✗ | ✗ | ✗ | 已完成 | — | `failover.go AdmitRamp` fnv%100<pct; RampAdmissionKey=tenant:acc:basis:attempt | 无 |
| ROUTE-075 | Ramp 阶段 min 持续 1m / min 样本 3 | ✗ | ✗ | ✗ | 已完成 | — | `types.go RampStageMinDuration=1m/RampStageMinSamples=3` | 无 |
| ROUTE-076 | Ramp 错误阈值 =10% (回滚触发) + 回滚 backoff×2 | ✗ | ✗ | ✗ | 已完成 | — | `types.go RampErrorThresholdPct=10/RampBackoffFactor=2`; service.go rampFailureRate>thresh OR BanSignals>0→rollbackRamp | 无 |
| ROUTE-077 | Ramp 到期自动启动 (lazy) | ✓(scheduled_test) | ✓(ShouldEnableChannel) | ✓(NextRetryAfter) | 已完成 | — | service.go MaybeStartRamp; failover.go maybeStartExpiredRamp | 无 |
| ROUTE-078 | 重复回滚告警阈值 =2 | ✗ | ✗ | ✗ | 已完成 | — | `types.go RepeatedRampRollbackAlertThreshold=2`; emitAlert AlertRepeatedRampRollback severity=high | 无 |
| ROUTE-079 | 手动 pause / resume(→ramping@1%) | 🟡 admin disable | ✓ admin disable/enable | 🟡 Disabled flag | 后端有·前端缺 | P2 | service.go ManualPause→StateManualPaused; ManualResume→StateRamping pct=1; admin handler `gatewayhttp/channel_health_admin_handler.go` | 提供 channel-health admin UI(暂停/恢复/强制 active),现仅后端 API |
| ROUTE-080 | force-active (跳过 ramp) + 安全告警 | 🟡 | ✓ | 🟡 | 后端有·前端缺 | P2 | service.go ForceActive severity=security→AlertManualForceActive; `channel_health_admin_handler.go:50` POST force-active | 同 ROUTE-079 UI |
| ROUTE-081 | force-cooldown (ops, 需 future ts) | ✓(ForceCooldown) | ✓ | ✓ | 后端有·前端缺 | P2 | service.go ForceCooldown 校验 until>now, 取 max | 同 ROUTE-079 UI |
| ROUTE-082 | 手动覆盖需 reason | ✗ | ✗ | ✗ | 已完成 | — | `types.go ManualOverrideRequiresReason=true`; manualTransitionLocked 空 reason 报错 | 无 |
| ROUTE-083 | 转换信心层级 (Observed/Inferred/OperatorOverride) | ✗ | ✗ | ✗ | 已完成 | — | types.go ConfidenceObserved/Inferred/OperatorOverride; signal_classifier.go | 无 |
| ROUTE-084 | 转换审计事件 (6 类) | 🟡 slog | 🟡 notify root | 🟡 status msg | 已完成 | — | types.go 6 AuditEventTypes; service.go emitTransitionEvents→AppendAudit | 无 |
| ROUTE-085 | 告警类型 (4) → DLQ outbox | ✓(ops_alert_evaluator) | ✓(NotifyRootUser) | ✗ | 已完成 | — | types.go BanSignal/RepeatedRampRollback/ManualForceActive/NoHealthyAlternate; emitAlert→obsdlq.Outbox | 无 |
| ROUTE-086 | 隐私: 原始上游文本永不持久化 | 🟡 sanitize | 🟡 mask | 🟡 | 已完成 | — | types.go Signal.RawUpstreamText 注释 "never stores"; privacy.go | 无 |
| ROUTE-087 | ListChannelHealth 分页 clamp (default 50/max 200) | n/a | n/a | n/a | 已完成 | — | service.go limit default 50 max 200 | 无 |
| ROUTE-088 | 主动合成探测 worker (synthetic probe) | ✓(channel_monitor 45s timeout/6s degraded/15-3600s interval/conc 5/SSRF-safe) | ✓(AutomaticallyTestChannels AutoTestChannelMinutes) | ✗ | 缺失 | P2 | grep 无 in-scope probe worker; HUAKAI 仅被动信号 | 增加 SSRF-safe 合成探测 worker(主动发现死渠道,不等真实流量打到才降级) |

## G. 信号分类 (status/keyword → class)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| ROUTE-089 | account_suspended/disabled → ban | ✓(keyword) | ✓(AutomaticDisableKeywords) | 🟡 | 已完成 | — | signal_classifier.go Classify "account_suspended"/account_disabled observed | 无 |
| ROUTE-090 | token_revoked/invalid_token/credential_revoked → ban | ✓(401 permanent) | 🟡 | ✓(401→30m) | 已完成 | — | signal_classifier.go observed "api key revoked" | 无 |
| ROUTE-091 | workspace/subscription_disabled → ban | 🟡 | 🟡 | 🟡 | 已完成 | — | signal_classifier.go observed | 无 |
| ROUTE-092 | 429 → rate_limit (observed) | ✓ | ✓ | ✓ | 已完成 | — | signal_classifier.go StatusCode==429 | 无 |
| ROUTE-093 | 403+rate/quota keyword → rate_limit (inferred) | ✓(handle403) | 🟡 | ✓(403→payment_required) | 已完成 | — | signal_classifier.go 403+keyword→inferred confidence | 无 |
| ROUTE-094 | ≥500 → upstream_5xx; ≥400 → channel_error | ✗ transient | 🟡 | 🟡 503→cooldown | 已完成 | — | signal_classifier.go observed | 无 |
| ROUTE-095 | local-gateway-5xx 分离 (不归咎 provider) | ✗ | ✗ | ✗ | 已完成 | — | signal_classifier.go LocalGatewayError&&≥500→SignalLocalGateway5xx | 无 |

## H. 限流 — 每 scope × window × policy

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| ROUTE-096 | 每-IP 全局-API 限流 | ✓(rate_limiter.go ClientIP key) | ✓(rate-limit.go GlobalAPIRateLimit GA+ClientIP) | ✗ | 缺失 | P1 | grep 无 per-IP limiter | 增加边缘每-IP 全局限流(防 DDoS/滥用;new-api 7 个 IP 限流器为标杆) |
| ROUTE-097 | 每-IP 全局-web 限流 | ✓ | ✓(GlobalWebRateLimit GW) | ✗ | 缺失 | P2 | 同上 | 同 ROUTE-096 |
| ROUTE-098 | 每-IP critical-path 限流 (20/20m) | ✗ | ✓(CriticalRateLimit; CriticalRateLimitNum=20/Duration=20*60) | ✗ | 缺失 | P2 | 同上 | 对登录/支付等关键路径加 IP 限流 |
| ROUTE-099 | 每-IP upload/download 限流 (10/60s) | ✗ | ✓(UploadRateLimitNum=10/DownloadRateLimitNum=10, Duration=60) | ✗ | 缺失 | P3 | 同上 | 媒体上传/下载路径加限流 |
| ROUTE-100 | 每-user search 限流 (10/60s) | ✗ | ✓(SearchRateLimitEnable=true/Num=10/Duration=60) | ✗ | 缺失 | P3 | grep 无 | 评估搜索端点限流需求 |
| ROUTE-101 | 每-user (auth) 限流 | ✓(user_rpm_cache) | ✓(userRateLimitFactory key rateLimit:%s:user:%d) | ✗ | 部分完成 | P2 | budget ScopeUser 存在 | 补全 per-user RPM/TPM 显式限流(当前 budget 偏 api-key/pool-group scope) |
| ROUTE-102 | 每-user 模型请求总量限流 | ✗ | ✓(model-rate-limit.go totalMaxCount token-bucket; ModelRequestRateLimitEnabled=false default) | ✗ | 已完成 | — | budget LimitSpec.Models; ModelRequestRateLimitDurationMinutes=1 同等 minute-bucket | 无 |
| ROUTE-103 | 每-user 模型请求 **success-only** 限流 | ✗ | ✓(model-rate-limit.go MRRLS key, 仅 status<400 记) | ✗ | 缺失 | P2 | budget 计所有 attempt | 增加 success-only 计数模式(失败请求不占用户配额,对齐 new-api MRRLS) |
| ROUTE-104 | 每-API-key RPM / TPM | ✓(api_key_rate_limit) / 🟡 | 🟡 token-group / ✗ | ✗ | 已完成 | — | budget ScopeAPIKey LimitPair.RPM/TPM; scope.go key `k:tenant:id`; CounterTPM | 无 |
| ROUTE-105 | 每-API-key 每-model RPM/TPM | ✗ | ✗ | ✗ | 已完成 | — | budget LimitSpec.Models; scope.go `km:tenant:id:model` | 无 |
| ROUTE-106 | 每-pool-group 限流 | ✓(group_capacity) | ✓(model-rate-limit GetGroupRateLimit) | ✗ | 已完成 | — | budget ScopePoolGroup; scope.go `g:tenant:id` | 无 |
| ROUTE-107 | 每-tenant scope | 🟡 group-level | ✗ | ✗ | 已完成 | — | 所有 budget Scope 带 TenantID; retrybudget per-tenant | 无 |
| ROUTE-108 | TPM token 计数 | 🟡 Gemini precheck | 🟡 success-count proxy | ✗ | 已完成 | — | budget CounterTPM; ReservedTokens→TPMIncrement | 无 |
| ROUTE-109 | 每-account 并发 cap (slot manager) | ✓(concurrency_service AcquireAccountSlot Redis sorted-set) | ✗ | ✗ | 已完成 | — | `pool/dispatcher/slot_manager.go` IncrementInFlightCount→0 rows=ErrNoSlotAvailable | 无 |
| ROUTE-110 | 每-user 并发 cap | ✓(concurrency_service AcquireUserSlot) | ✗ | ✗ | 缺失 | P2 | HUAKAI 仅 per-account | 增加 per-user 并发槽(防单用户独占;sub2api AcquireUserSlot 标杆) |
| ROUTE-111 | 满载等待队列 (bounded) | ✓(IncrementWaitCount; defaultExtraWaitSlots=20; maxWait=conc+20) | ✗ | 🟡(closestCooldownWait) | 已完成 | — | `default_selector.go fallbackPlan→WaitPlan TimeoutMS/MaxWaiting` | 无 |
| ROUTE-112 | slot-cleanup worker (孤儿回收 / lease) | ✓(StartSlotCleanupWorker ticker) | ✗ | ✗ | 已完成 | — | slot_manager.go DefaultLeaseDuration=90s lease | 无 |
| ROUTE-113 | window 实现: 分钟桶 (redis key per minute) | ✗ | ✗ | ✗ | 已完成 | — | budget scope.go RedisCounterKey `bgt:{scope}:r:minute` | 无 |
| ROUTE-114 | window 实现: in-mem 滑动事件列表 | ✗ | ✗ | ✗ | 已完成 | — | retrybudget.go events[]time.Time | 无 |
| ROUTE-115 | window 实现: 固定 (Lua INCR+PEXPIRE) | ✓(rate_limiter.go rateLimitScript) | ✗ | ✗ | 缺失 | P3 | HUAKAI 无 Lua 固定窗 | 如加边缘 IP 限流(ROUTE-096)可选 Lua 固定窗实现 |
| ROUTE-116 | window 实现: list-滑动 (LPush/LIndex/LTrim) | ✗ | ✓(rate-limit.go redisRateLimiter) | ✗ | 缺失 | P3 | HUAKAI 无 | 同 ROUTE-115(实现选型) |
| ROUTE-117 | window 实现: token-bucket | ✗ | ✓(model-rate-limit.go limiter.New) | ✗ | 缺失 | P3 | HUAKAI 用 minute-bucket | 评估 token-bucket(更平滑突发);当前 minute-bucket 已够 |
| ROUTE-118 | Redis-fail-open / fail-close / memory-fallback policy | ✓(RateLimitFailOpen/Close iota) | 🟡 fail to 500 | ✗ | 已完成 | — | budget types.go FailModeOpen/FailModeClosed/FailModeMemoryFallback(default); service.go handleStoreError | 无 |
| ROUTE-119 | fail-open 计数 metric | ✗ | ✗ | ✗ | 已完成 | — | budget types.go expvar `budget_fail_open_total` | 无 |
| ROUTE-120 | RetryAfter 算到下一分钟边界 + 限额 normalize/clamp | ✗ | ✗ | ✗ | 已完成 | — | budget types.go minuteRetryAfter(≤1m) + normalizeLimit(≤MaxInt32) | 无 |
| ROUTE-121 | 本地预检避免上游 429 (local pre-check) | ✓(ratelimit_service PreCheckUsage Gemini RPM minute window) | ✗ | ✗ | 缺失 | P0 | grep `rpm_limit` in pool/ → 0; `gates.go:210-229 modelRateLimitGate` 仅查 RateLimitResetAt(429 后写),无主动滑动窗计数 | **最高价值**: 实现主动 RPM/TPM 预算滑动窗计数器,打满限额前绕开,每次 429 = 已失败请求+延迟 |

## I. 重试 / 退避 / 故障转移 params

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| ROUTE-122 | 跨账号 failover 循环 | ✓(failover_loop.go HandleFailoverError) | ✓(relay.go retry loop) | ✓(conductor executeMixedOnce) | 部分完成 | P2 | caller-driven (AttemptSeq+ExcludedAccounts re-select);`router/default_router.go` | 评估是否内聚 failover 循环到 router(减少 caller 重试编排) |
| ROUTE-123 | 可重试错误自动 failover (end-class 列表) | ✓(error_policy) | ✓ | ✓ | 已完成 | — | `router/default_router.go:40-98` RetryableEndClasses: upstream_error_5xx/upstream_rate_limit/first_token_timeout/inter_event_timeout | 无 |
| ROUTE-124 | 重试排除已失败 provider | ✓(ExcludedIDs) | 🟡 | ✓(tried map) | 已完成 | — | `pool/router/types.go ExcludedAccounts`; 每 Attempt 注入上次 accountID | 无 |
| ROUTE-125 | 最大凭据切换次数 | ✓(failover_loop.go MaxSwitches) | ✓(common.RetryTimes=0) | ✓(max-retry-credentials:0=all) | 部分完成 | P2 | retrybudget cap(非 per-req count) | 增加 per-request 最大切换次数上限(防单请求耗尽所有账号) |
| ROUTE-126 | 同账号重试次数 + 延迟 | ✓(maxSameAccountRetries=3 / sameAccountRetryDelay=500ms) | 🟡 | ✓(per-model loop) | 缺失 | P2 | HUAKAI 无同账号重试 | 增加同账号短重试(瞬时抖动不必立刻切账号;sub2api 3×500ms 标杆) |
| ROUTE-127 | 请求级重试次数 + 状态码白名单 | ✓ | ✓(AutomaticRetryStatusCodeRanges 100-199,300-399,401-407,409-499,500-503,505-523,525-599) | ✓(request-retry:3; hardcoded 403,408,500,502,503,504) | 部分完成 | P2 | modelfallback ClassForFailure 按类;无显式 status-range 配置 | 暴露请求级重试次数 + status-code 范围配置(对齐 new-api 可配范围) |
| ROUTE-128 | 永不重试状态码/错误码 | ✓(RetryableOnSameAccount flag) | ✓(alwaysSkipRetryStatusCodes{504,524} / alwaysSkipRetryCodes{BadResponseBody}) | ✓(isRequestInvalidError) | 部分完成 | P2 | gateway error classes 隐含 | 显式 always-skip 列表(504/524 等)避免无谓重试放大延迟 |
| ROUTE-129 | 指数退避 base/max | ✗ | ✗ | ✓(conductor quotaBackoffBase=1s/quotaBackoffMax=30m, base*(1<<level)) | 部分完成 | P2 | 仅 ramp backoff factor=2; circuitbreaker OpenCooldown | 引入跨-attempt 指数退避(配额错误重试间隔递增,对齐 cliproxy) |
| ROUTE-130 | 单账号组 503 backoff (2s) / 线性 inter-switch backoff | ✓(singleAccountBackoffDelay=2s / (SwitchCount-1)*1s) | ✗ | ✗ | 缺失 | P2 | selector 一次返回,无 attempt 间 sleep | 增加 attempt 间退避 sleep(单账号组打满时不立即重试)→ 配合 ROUTE-126/129 |
| ROUTE-131 | 等到最近冷却到期 (wait-to-nearest-cooldown) | ✗ | ✗ | ✓(conductor closestCooldownWait; max-retry-interval:30s) | 部分完成 | P3 | WaitPlan 返回 caller | 在 router 内实现等待最近冷却到期(现仅给 caller WaitPlan) |
| ROUTE-132 | 重试预算防风暴 (per-tenant 滑动) | ✗ | ✗ | ✗ | 已完成 | — | `retrybudget.go New(limit,window)`; defaultWindow=1m; Allow per-tenant | 无 |
| ROUTE-133 | 跨模型 fallback 链 (失败换模型) | 🟡 antigravity mapping | 🟡 cross-group | 🟡 openai-compat pool rotate | 已完成 | — | `modelfallback.go` General/ContextWindow/ContentPolicy chains; defaultMaxDepth=2 (注: 大功能树 F-3 记 MISSING,细树证实已落地→以已完成为准) | 无(可做 PROTO-020 UI,见 ROUTE-159) |
| ROUTE-134 | 错误类感知 fallback (按 ClassForFailure) | ✗ | ✗ | ✗ | 已完成 | — | modelfallback.go ClassForFailure→ContextWindowExceeded/ContentPolicy/General | 无 |
| ROUTE-135 | fallback exactly-once 逻辑请求 ID | ✗ | ✗ | ✗ | 已完成 | — | modelfallback.go DeriveLogicalRequestID base+#mf:sha256[:8] | 无 |
| ROUTE-136 | fallback 已试 dedup + 通配模型链 | ✗ | ✗ | 🟡 | 已完成 | — | modelfallback.go Resolve tried-map skip; wildcardModel="*" firstConfiguredChain | 无 |
| ROUTE-137 | 中流 failover (streaming 断续切换) | ✓(gateway_handler_stream_failover) | ✗ | ✓(conductor executeStreamMixedOnce before first byte) | 部分完成 | P2 | `sql/migrations/0003:41` mid_stream_failover_default; proto MidStreamFallbackContinuation; P-0 验证器强拒非 none(`envelope_validate.go:499`); 路线图 P-8 | 落地 P-8 中流 failover(streaming provider 断连无需客户端重发整请求) |
| ROUTE-138 | 最大重试次数控制 (终止条件) | ✓ | ✓ | ✓ | 已完成 | — | RoutePlan.Attempts 由 PoolCandidates 数上界; RetryableEndClasses 穷举终止 | 无 |
| ROUTE-139 | pg 40001 序列化重试 | ✗ | ✗ | ✗ | 已完成 | — | `retry.go slotAcquireSerializableAttempts=3`; isSerializationFailure code=="40001" | 无 |

## J. 上游错误检测 → typed 冷却 (status-by-status)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| ROUTE-140 | 429 → 冷却 (StateRateLimited) | ✓(handle429) | ✓(processChannelError) | ✓(case 429 nextQuotaCooldown) | 已完成 | — | `upstream_service.go` 429→StateRateLimited ReasonRateLimitRPM; platformsettings KeyCooldown429Seconds="60" | 无 |
| ROUTE-141 | 529 / overload → 冷却 | ✓(handle529) | 🟡 generic 5xx | ✗ | 已完成 | — | upstream_service.go 529→StateOverloaded; platformsettings KeyCooldown529Seconds="300" | 无 |
| ROUTE-142 | 401 → auth 冷却 | ✓(OAuth401CooldownMinutes default 10) | 🟡 disable | ✓(401→30m unauthorized) | 已完成 | — | channelhealth token_revoked ban(24-72h, 比 ref 更严) | 无 |
| ROUTE-143 | 402/403 → payment/quota 冷却 | ✓(handle403) | 🟡 keyword | ✓(402,403→30m payment_required) | 已完成 | — | `rate.go` ReasonKYCRequired/OrgDisabled/CreditExhausted | 无 |
| ROUTE-144 | 403-连续计数器 → disable | ✓(openAI403DisableThreshold=3 / window 180m / cooldown 10m) | ✗ | ✗ | 缺失 | P3 | HUAKAI 无 403 连续计数 | 评估 403 连续计数器→自动 disable(sub2api 3/180m/10m;HUAKAI 经健康 FSM 部分覆盖) |
| ROUTE-145 | 404 → 长冷却 (5m, 可达 12h) | ✗ | ✗ | ✓(case 404→12h not_found; model_cooldown SourceLayer=gateway_upstream_404) | 已完成 | — | `model_cooldown.go defaultModelCooldownDuration=5m` | 无 |
| ROUTE-146 | 408/500/502/503/504 → 短冷却 | ✗ | 🟡 | ✓(case→1m) | 部分完成 | P3 | upstream5xx 经健康窗(非直接冷却) | 可补直接短冷却映射(现走健康 FSM 聚合) |
| ROUTE-147 | model-not-supported → suspend | ✗ | ✗ | ✓(isModelSupportResultError→12h) | 部分完成 | P3 | 部分(模型门控剔除,无显式长 suspend) | 评估 model-unsupported 长冷却(cliproxy 12h) |
| ROUTE-148 | Retry-After 秒数解析 | ✓(calculateOpenAI429ResetTime) | ✗ | ✓(parseCodexRetryAfter resets_in_seconds) | 已完成 | — | upstream_service.go retryAfterCooldown Atoi | 无 |
| ROUTE-149 | Retry-After HTTP-date 解析 | ✗ | ✗ | ✗ | 已完成 | — | upstream_service.go http.ParseTime 分支; **超越同类** | 无 |
| ROUTE-150 | 厂商多窗 429 reset 解析 (5h/7d 统一头) | ✓(calculateAnthropic429ResetTime; RateLimitWindow5h=5h/Window1d=24h) | ✗ | ✓(codex resets_in_seconds body) | 部分完成 | P2 | ModelRateLimit.RateLimitResetAt 被尊重; rate.go ReasonRateLimit5h/7d/Both 枚举存在但 **无 header parser** | 实现 anthropic/openai 5h+7d 多窗头解析,精确冷却到 reset(现仅 generic Retry-After) |
| ROUTE-151 | 冷却 clamp 边界 | ✓(default 5s / max 7200s) | ✗ | ✓(quotaBackoffMax 30m) | 已完成 | — | types.go normalized() floors; durationSeconds floor 1s | 无 |
| ROUTE-152 | typed reason 枚举 (20 consts) | ✓(ErrorPolicyResult) | 🟡 status code | 🟡 StatusMessage strings | 已完成 | — | rate.go 20 Reason consts | 无 |
| ROUTE-153 | 默认 fallback 冷却 (无 hint) | ✓(apply429Fallback 5s) | ✗ | ✓(nextQuotaCooldown exp) | 已完成 | — | `upstream_service.go defaultUpstreamCooldown=5m`; model_cooldown 5m | 无 |
| ROUTE-154 | 级联清除所有冷却状态 (ClearCascade) | ✓(clear tests) | ✓(EnableChannel) | ✓(resetModelState) | 已完成 | — | rate.go Service.ClearCascade interface; admin clear-rate-limit | 无 |
| ROUTE-155 | disable-cooling 全局/per-auth 覆盖 | ✗ | ✗ | ✓(disable-cooling:false; SetQuotaCooldownDisabled; DisableCoolingOverride per-auth) | 部分完成 | P3 | 部分 | 增加全局/per-account disable-cooling 开关(测试/特殊 provider 用) |

## K. 熔断器 (circuit breaker, field-level)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 (默认值) | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| ROUTE-156 | 通用 CB 状态机 (Closed/Open/HalfOpen) | 🟡 billing-path only | ✗ | ✗ (per-auth cooldown FSM ≈) | 已完成 | — | `circuitbreaker/breaker.go` 三态 | 无 |
| ROUTE-157 | 失败阈值→open =5 / open 冷却→half-open =1m | ✓(billing cfg) | ✗ | 🟡 per-status 立即 | 已完成 | — | breaker.go defaultFailureThreshold=5 / defaultOpenCooldown=1m | 无 |
| ROUTE-158 | half-open max probes=1 / 成功转闭=1 | ✓(HalfOpenRequests) | ✗ | ✗ | 已完成 | — | breaker.go defaultHalfOpenMaxProbes=1 / defaultHalfOpenSuccessesToClose=1 | 无 |
| ROUTE-159b | fail-open vs fail-closed 服务模式 + force open/close + per-key 隔离 + 决策原因串 | ✗ | ✗ | per-auth-per-model | 已完成 | — | breaker.go FailClosed default/FailOpen→ServingUntracked; ForceOpen/ForceClose; entries map[string]*entry; decision reason strings | 无 |

## L. PASR 段运行时 (HUAKAI 独有, field-level)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 (默认值) | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| ROUTE-160 | 段大小 K=3 + 默认 max-age 5m (Anthropic-cache 对齐) + 扩展缓存 TTL 1h | ✗ | 🟡 affinity TTL 3600s | ✗ | 已完成 | — | prefix_segment.go PASRSegmentSize=3/DefaultSegmentMaxAge=5m/DefaultExtendedCacheTTL=1h | 无 |
| ROUTE-161 | 段表 cap 10万 (LRU evict) + LRU touch on hit | ✗ | 🟡(channel_affinity MaxEntries) | ✗ | 已完成 | — | prefix_segment.go DefaultSegmentTableCap=100000; MoveToFront | 无 |
| ROUTE-162 | cache-seen bitmap (3 bits/段, atomic) | ✗ | ✗ | ✗ | 已完成 | — | prefix_segment.go HasCacheBitmap atomic.Uint32 | 无 |
| ROUTE-163 | 连续 miss 降级阈值=2 + sticky 成员降级 | ✗ | ✗ | ✗ | 已完成 | — | prefix_segment.go PASRDemoteThreshold=2; feedback.go RecordMiss→ShouldDemote; demote.go | 无 |
| ROUTE-164 | 缓存感知降级 (cache-miss demote N=2) | 🟡 | 🟡 | ✗ | 已完成 | — | `feedback.go:54-100` MissCount N=2 demote; prefix_segment.go HasCache 位清除 | 无 |
| ROUTE-165 | per-tenant 段键 (无跨租户共享) | ✗ | ✗ | ✗ | 已完成 | — | prefix_segment.go segmentKey(tenantID,prefix) M5b | 无 |
| ROUTE-166 | read-only 段模式 (shadow) | ✗ | ✗ | ✗ | 已完成 | — | prefix_segment.go Lookup-only; pasr.go readOnlySegments | 无 |
| ROUTE-167 | load-cap 候选过滤 =0.95 | ✗ | ✗ | ✗ | 已完成 | — | pasr.go loadCap=0.95 default | 无 |
| ROUTE-168 | aging/evict worker tick (1min, TTL 对齐) | ✗ | 🟡 affinity janitor | ✗ | 已完成 | — | aging_worker.go PASRAgingWorker 1min tick | 无 |
| ROUTE-169 | 段不健康时全环 HRW fallback | ✗ | ✗ | ✗ | 已完成 | — | pasr.go scheduleHRWFullRing TopK(prefix,len) | 无 |
| ROUTE-170 | 环种子默认 (可轮换) 0xCAFEBABE + post-mutation release timeout 2s | ✗ | ✗ | ✗ | 已完成 | — | pasr.go defaultPASRRingSeed=0xCAFEBABE; pasrPostMutationReleaseTimeout=2s | 无 |
| ROUTE-171 | 金丝雀流量分割 (hash-bucket %) | ✗ | ✗ | ✗ | 已完成 | — | `pool/dispatcher/dispatcher.go` fnv64a%100<CanaryPercent; metrics.go canary_pasr_used | 无 (动态调比例见 ROUTE-044/085) |

## M. 混路风险 + 准入写回 + 流量管理

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| ROUTE-172 | 混路组成风险 (source/vendor/credential_type 三维 + dedup) | ✗ | ✗ | ✗ | 已完成 | — | risk.go Evaluate source/vendor/credential_type mismatch; dedupe by dim+acc+vals | 无 |
| ROUTE-173 | 跨租户 slot-acquire 拒绝 (DR-001) | 🟡 | ✗ | ✗ 单租户 | 已完成 | — | slot_manager.go account.TenantID!=req.TenantID→refuse | 无 |
| ROUTE-174 | 原子 slot+claim 同-Tx 写回 (Serializable) | 🟡 Redis slot only | ✗ | ✗ | 已完成 | — | slot_manager.go Serializable Tx IncrementInFlightCount+InsertSlotAcquisition+commit | 无 |
| ROUTE-175 | 幂等 slot 释放 | 🟡 | ✗ | ✗ | 已完成 | — | slot_manager.go NewIdempotentRelease; CTE flips status='acquired' only | 无 |
| ROUTE-176 | ClaimID 有但无 writer → 报错 (无静默跳过) | ✗ | ✗ | ✗ | 已完成 | — | default_selector.go:tryLayer errors if ClaimID!=0 && claims==nil | 无 |
| ROUTE-177 | 响应 drain 预算 (bytes/s/cost) | ✗ | ✗ | ✗ | 已完成 | — | `gateway/forwarder_types.go:49-75` DrainOutcome/DrainBudgets; `sql/migrations/0003:56` drain_max_estimated_cost_usd | 无 |
| ROUTE-178 | 预算 reservation/settle/release 包裹 quota | 🟡 | ✓(pre_consume_quota) | ✗ | 已完成 | — | budgetenforce/enforce.go Reserve→Settle/Release; Reserve denial 释放预算 | 无 |
| ROUTE-179 | Quota 策略感知路由 | ✓ | ✓ | ✗ | 已完成 | — | `sql/migrations/0070:18-60` quota_policies; quota/service.go Reserve/Release | 无 |
| ROUTE-180 | Provider 优雅排水 (graceful drain, weight→0 平滑迁移) | ✗ | ✗ | ✗ | 缺失 | P3 | grep drain.channel/weight.zero/graceful.remove → 无 channel-level 排水 | 增加 channel-level 优雅排水(维护/下线 provider 时 weight 渐降平滑迁流量,非硬切) |

## N. 可观测性 & Admin + UI 缺口 (含 151 PROTO/AI 行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| ROUTE-181 | 每次调度路由原因日志 (why-this-account JSON) | 🟡 in-mem metrics | 🟡 use_channel log | 🟡 debug log | 已完成 | — | routing_reason.go RoutingReasonBuilder→RoutingReasonJSON; layers+gate-failures+chosen | 无 |
| ROUTE-182 | Canary / Shadow 指标 | ✗ | ✗ | ✗ | 已完成 | — | `pool/dispatcher/metrics.go:37-210` canary_pasr_used/shadow_drop_full/pre_mutation_fallback | 无 |
| ROUTE-183 | Channel 健康状态审计日志 | 🟡 | ✓(NotifyRootUser) | 🟡 | 已完成 | — | channelhealth/store_postgres_audit_required_test.go; EventManualOverride/EventDisabled | 无 |
| ROUTE-184 | 路由策略 Admin CRUD (route admin API) | ✓ | ✓ | ✓(management routing strategy route) | 后端有·前端缺 | P2 | `routeadmin/`; `adminhttp/channel_catalog_handler.go`; 151 **AI-018 route admin UI** 后端有前端缺 | 提供 route admin 前端 UI(规则 CRUD/通配/组绑定可视化) |
| ROUTE-185 | 实时路由指标 (Prometheus 挂载) | ✓ | ✓ | 🟡 | 部分完成 | P2 | metrics.go 指标定义存在,但未找到完整 prometheus.Register→/metrics 挂载路径 | 验证/补全 Prometheus 挂载到 /metrics 端点(监控告警缺口) |
| ROUTE-186 | 路由策略 UI/config (选择策略可视配置) — 151 PROTO-019 | 🟡 | 🟡 | ✓(config round-robin; management route) | 后端有·前端缺 | P2 | 151 12D **PROTO-019** "后端有策略,前端缺"; cliproxy CP-M-014 routing strategy config | 提供路由策略选择/参数前端配置页(暴露 selection_mode/canary%/shadow% 等) |
| ROUTE-187 | 模型 fallback 链 UI/config — 151 PROTO-020 | 🟡 | 🟡 | 🟡(openai-compat pool) | 后端有·前端缺 | P2 | 151 12D **PROTO-020** "后端部分,前端缺"; modelfallback.go 后端已落地(ROUTE-133) | 提供 fallback 链编辑前端(配置 General/ContextWindow/ContentPolicy 链 + depth) |
| ROUTE-188 | AI provider account health UI — 151 AI-004 | ✓(ChannelStatusView) | ✓ | 🟡 | 后端有·前端缺 | P2 | 151 AI-004 "后端有,前端部分"; channelhealth admin API 有(ROUTE-079~081) | 补全 provider/channel 健康可视化 + 手动操作前端 |
| ROUTE-189 | 请求级重试/max-retry 前端配置 — cliproxy CP-M-013 | n/a | n/a | ✓(config/management) | 后端有·前端缺 | P3 | 151 12C CP-M-013 "HUAKAI 有 retry budget 后端,前端配置缺" | 暴露 retry budget/max-retry 前端配置(配合 ROUTE-127) |
| ROUTE-190 | clear-rate-limit / 健康强制操作 admin API | ✓ | ✓ | ✓ | 后端有·前端缺 | P2 | provider-accounts `.../clear-rate-limit`; channel_health_admin_handler force-active/pause/cooldown | 同 ROUTE-188 纳入健康 admin UI |

---

## 优先级汇总 (HUAKAI 缺口 ranked, 仅列 P0-P2)

| 优先级 | ID | 功能 | 状态 | 商业影响 |
|---|---|---|---|---|
| **P0** | ROUTE-121 | 主动 RPM/TPM 预算追踪 (local pre-check) | 缺失 | **最高**: 无滑动窗计数,限额前无法预防性绕开;每次 429=用户可感知错误/延迟。sub2api PreCheckUsage / new-api channel_limit rpm 计数器为标杆 |
| **P1** | ROUTE-002/003 | weight 实际生效 (加权抽样) | 部分完成 | operator 设权重无效,等同纯优先级路由 |
| **P1** | ROUTE-023 | 上下文窗口感知路由 | 部分完成 | 超长 prompt 发到窗口不足 provider→上游400;枚举已预留未执行 |
| **P1** | ROUTE-024 | 能力标记路由实际执行 | 部分完成 | vision/tools 请求路由到不支持 provider→上游400;现 AllowAll 不保护 |
| **P1** | ROUTE-096 | 每-IP 全局限流 | 缺失 | 无边缘限流,DDoS/滥用暴露;new-api 7 限流器为标杆 |
| **P2** | ROUTE-028 | 延迟优化正向路由 (EWMA) | 缺失 | 健康 FSM 仅负向门控,不主动选快的;p50 高于下界 |
| **P2** | ROUTE-029 | 成本感知路由 | 缺失 | 多定价 provider 无法引流低成本通道(COGS 杠杆) |
| **P2** | ROUTE-044/085/184/186/187/188 | 路由/fallback/健康 admin 前端 UI (含 PROTO-019/020, AI-018, AI-004) | 后端有·前端缺 | 策略/灰度/健康全靠重启或后端 API,灰度发布与运维体验差 |
| **P2** | ROUTE-088 | 主动合成探测 worker | 缺失 | 仅被动信号,死渠道需真实流量打到才降级 |
| **P2** | ROUTE-103 | success-only 请求计数 | 缺失 | 失败请求占用户配额 |
| **P2** | ROUTE-110 | 每-user 并发 cap | 缺失 | 单用户可独占并发,无公平隔离 |
| **P2** | ROUTE-126/129/130 | 同账号重试 + attempt 间退避 | 缺失 | 瞬时抖动立即切账号 / 无退避放大故障 |
| **P2** | ROUTE-137 | 中流 failover (P-8) | 部分完成 | streaming provider 断连须客户端重发整请求 |
| **P2** | ROUTE-150 | 厂商多窗 429 reset 解析 (5h/7d) | 部分完成 | 枚举有无 header parser,冷却不精确 |

## 备注 (审计诚实声明)
- **大功能树 F-3 (跨模型 fallback) 标 MISSING,但字段级细树 + modelfallback.go 证实已落地** → 本标杆以代码为准记 ROUTE-133 **已完成**(细树审计日期更新,backend 已补)。同理 H-3 drain / E-3 canary 等以细树最新代码为准。
- HUAKAI **超越同类**(ref 均无): 6 态健康 FSM + 8 独立 per-signal 阈值/窗/冷却、分级 ramp 1→10→50→100% + fnv 准入 + 回滚@10% + backoff×2 + 重复回滚告警@2、5 模式 dispatcher(default/shadow/canary/pasr-primary/pasr-strict) + pre/post-mutation 分流、HRW splitmix64+SHA256 环 + PASR K=3 段(5m/1h TTL/10万 cap/3-bit bitmap/demote@2/per-tenant)、通用熔断器、per-tenant 重试预算、错误类 model-fallback exactly-once、minute-bucket budget 多 scope + fail-open/close/memory-fallback、原子 slot+claim Serializable Tx + 跨租户拒绝 + 40001 重试×3、Retry-After HTTP-date 解析、路由原因审计 JSON。
- HUAKAI **核心缺口集中在**: (1) 主动限额预检 (ROUTE-121, P0); (2) 权重/能力/上下文窗口门控未实际生效 (ROUTE-002/024/023, P1); (3) 正向偏好路由 (latency/cost, ROUTE-028/029, P2); (4) 边缘 IP 限流 (ROUTE-096, P1); (5) 全套路由/健康 admin 前端 UI (后端有·前端缺, P2)。


# ====================  模块 E  ====================
# 标杆 · 用量/计费/配额/定价

> CANONICAL BENCHMARK feature tree — 模块 = **用量计量 / 计费(token+按次) / 配额 / 定价(ratio·per-unit·catalog)**。
> 支付渠道(PSP/easypay/stripe…)/钱包充值订单/voucher 兑换码/订阅套餐/返佣激励 = **另一 agent (D-money/commerce) 负责**，本表只在这些子系统**直接喂给计量·计费·配额·定价引擎的接缝处**保留行(如 subscription→quota cap、refund→usage 负向 event)。
>
> **三源合并去重**:
> 1. 大功能树 = `wt/treeview/docs/process/feature-tree/billing-quota.md` + `model-catalog-pricing.md`(pricing/ratio 部分)
> 2. 字段级细树 = `feature-audit/05-billing-fine.md`(§A-D 定价/计费/配额 + §C token引擎 + §L 用量列 + §M 用量分析,只取计量/计费/配额/定价行)
> 3. 151 = `feature-audit/151-ref/06-huakai-endpoint-status-tree.md`(C/F/L 段 usage/billing/pricing) + `10A`(usage 部分) + `09B`(账本/对账 与计费相关行)
>
> **refs 列**: ✓=present / ✗=absent。sub2api=`refs/sub2api/backend`,new-api=`refs/new-api`,cliproxy=`refs/CLIProxyAPI`(纯转发,全程无货币计费,仅上游配额耗尽探测+用量队列透传)。
> **HUAKAI状态 六级(禁虚标)**: 已完成 / 部分完成 / 后端有·前端缺 / 缺失 / 未做 / 未合并main。
> **优先级** P0-P3 / —。HUAKAI 目标库 = `origin/fix/hermes-phase-1-e33d940`(183 paths;main 仅 83 paths,带 ⚠未合并main 标注)。

---

## A. 定价 — ratio / 倍率 / 维度 (每个倍率一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| BILL-001 | model_ratio (per-model 输入基准价/倍率) | ✓ | ✓ | ✗ | 已完成 | — | `pricingeval/resolver.go` FlatRateFallback.Input micro-USD/token + `billing/rate_table_source.go`;migration 0068 | 无 |
| BILL-002 | completion_ratio (输出价 / 输出÷输入比) | ✓ | ✓ | ✗ | 已完成 | — | FlatRateFallback.Output 独立 micro-USD(非比率,直接 output rate) | 无 |
| BILL-003 | cache_ratio (cache-read 折扣倍率) | ✓ | ✓ | ✗ | 已完成 | — | FlatRateFallback.CacheRead + Result.CacheReadCost | 无 |
| BILL-004 | create_cache_ratio (cache-write 加价倍率, Claude 1.25) | ✓ | ✓ | ✗ | 已完成 | — | FlatRateFallback.CacheCreation + Result.CacheCreationCost | 无 |
| BILL-005 | cache_creation_5m (5m TTL 分档价) | ✓ | ✓ | ✗ | 已完成 | — | pricingeval Usage.CacheCreation5mTokens + FlatRateFallback.CacheCreation5m;`cacheCreationTierMicros()`;`chat_completions_pricing.go` cache_creation_5m_micro_usd | 无 |
| BILL-006 | cache_creation_1h (1h TTL 分档价) | ✓ | ✓ | ✗ | 已完成 | — | Usage.CacheCreation1hTokens + FlatRateFallback.CacheCreation1h;tier 缺失软回退 base CacheCreation | 无 |
| BILL-007 | group_ratio (分组倍率 default/vip/svip) | ✓ | ✓ | ✗ | 部分完成 | P2 | `pricingcatalog/ratio_resolver.go` GroupPricingRatio per pool_group(migration 0078);仅单层 | 评估是否需多层;当前单层 pool_group ratio 已够大多数场景 |
| BILL-008 | **group_group_ratio (userGroup×usingGroup 二维矩阵)** | 🟡(无显式二维) | ✓ | ✗ | 缺失 | P2 | new-api `group_ratio.go` GetGroupGroupRatio(userGroup,usingGroup) default vip→{edit_this:0.9};HUAKAI 仅单层 pool_group ratio | 真缺口:设计二维 (user_group × pool_group) 倍率矩阵或论证单层足够 |
| BILL-009 | group_special_usable_group (分组特殊可用组 append/remove) | 🟡 | ✓ | ✗ | 缺失 | P3 | new-api `group_ratio.go` GroupSpecialUsableGroup default vip→{append_1,-:remove_1} | 评估必要性(与订阅 granted_group 部分重叠) |
| BILL-010 | audio_ratio (音频输入 token 倍率) | ✓ | ✓ | ✗ | 已完成 | — | `audiopricing/catalog.go` scheme=token TokenRates.Input | 无 |
| BILL-011 | audio_completion_ratio (音频输出比) | ✓ | ✓ | ✗ | 已完成 | — | audiopricing TokenRates.Output | 无 |
| BILL-012 | image_ratio (图片 token 倍率) | ✓ | ✓ | ✗ | 已完成 | — | `imagepricing` scheme=token_image TokenRates(Input/Output/Multiplier) | 无 |
| BILL-013 | model_price (per-model 整价覆盖 ratio) | ✓ | ✓ | ✗ | 已完成 | — | pricingeval per_unit / rate_table model 级 override;new-api GetModelRatioOrPrice price>ratio | 无 |
| BILL-014 | per-model 通用 multiplier / markup (pricing JSON model_multiplier) | ✗ | ✓ | ✗ | 已完成 | — | `chat_completions_pricing.go` model_multiplier→`completionRateVector.Multiplier`;`scaledMicros()` | 无 |
| BILL-015 | topup_group_ratio (充值分组折扣倍率) | 🟡(plan-level) | ✓ | ✗ | 缺失 | P3 | new-api `controller/topup.go` GetTopupGroupRatio(group)×payMoney;**充值侧→主属 D-money,定价影响保留索引** | 与 D-money agent 对齐;充值折扣非计量定价核心 |
| BILL-016 | **service-tier cost multiplier (auto/flex/priority 系数)** | ✓ | 🟡 | ✗ | 缺失 | P1 | sub2api `billing_service.go` serviceTierCostMultiplier + normalizeBillingServiceTier;HUAKAI 无 service-tier 系数 | 真缺口:加 service_tier 维度到 pricingeval(与 BILL-035 priority 双价配套) |
| BILL-017 | expose_ratio (公开价表开关) | 🟡 | ✓ | ✗ | 已完成 | — | `pricingcataloghttp` 公开 ratio handler;new-api expose_ratio.go atomic+30s TTL | 无 |
| BILL-018 | compact-model ratio suffix (`-openai-compact`) | ✗ | ✓ | ✗ | 缺失 | P3 | new-api `compact_suffix.go` WithCompactModelSuffix;HUAKAI 无 | 评估必要性(罕用) |
| BILL-019 | reasoning/thinking token 独立定价 | ✗ | 🟡 | ✗ | 缺失 | P1 | ReasoningTokens 已追踪(`proto/accounting.go:16`)但 grep reasoning_micro_usd 零命中→当作 output token 计费;o1/o3/Claude3.7 单独计价 | 真缺口:加 reasoning_micro_usd rate slot,避免系统性多/少计费 |
| BILL-020 | image-output token 定价 | ✗ | 🟡(ImageRatio) | ✗ | 部分完成 | P2 | image_output_tokens 已追踪 + usage_records.image_output_cost 列;但走 imagepricing token_image 方案,纯 image_output_micro_usd rate 在 chat 路径零命中 | 确认 image-output 经 imagepricing 已正确计价;否则补 rate |
| BILL-021 | audio token 定价(通用) | ✓ | ✓ | ✗ | 已完成 | — | audiopricing scheme=token | 无 |
| BILL-022 | Gemini audio input per-MTok (model 专档常量) | 🟡 | ✓ | ✗ | 部分完成 | P3 | new-api GetGeminiInputAudioPricePerMillionTokens 6 档;HUAKAI 通用 token rate 无 Gemini 专档 | 可经 rate_table model 级覆盖配置,无需专码 |
| BILL-023 | video token 定价 | ✗ | ✗ | ✗ | 缺失 | P3 | CapabilityVideo 定义于 proto;无 video rate | 低优先;先确保 capability 追踪 |
| BILL-024 | fine-tuned model 自定义定价 | ✗ | ✗ | ✗ | 缺失 | P3 | grep fine_tune 零命中(三 refs 全无) | parity:三 refs 都无,park |
| BILL-025 | per-tenant 自定义价表(超出全局) | 🟡 | ✗ | ✗ | 部分完成 | P2 | `billing_pricing_versions.tenant_id` 支持 per-tenant;`rate_table_source.go` tenant lookup;但无写 API(直插 DB) | 配合 BILL-040 写 API 暴露 per-tenant override |
| BILL-026 | per-user / per-segment 定价 | ✗ | ✗ | ✗ | 缺失 | P3 | grep user_price 零命中(三 refs 全无) | parity:全无,park |
| BILL-027 | 时段定价(peak/off-peak) | ✗ | ✗ | ✗ | 缺失 | P3 | grep peak_price 零命中 | parity:全无,park |
| BILL-028 | volume / commitment 折扣阶梯 | ✗ | ✗ | ✗ | 缺失 | P3 | grep volume_discount 零命中;企业承诺折扣 | 经 tiered DSL(BILL-033)可表达;无需专表 |

## B. 定价 — per-unit / tool-call / tiered 方案 (每种方案一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| BILL-029 | image per-image fixed (size×quality 价) | ✓ | ✓ | ✗ | 已完成 | — | imagepricing scheme=per_image: image_base_micro_usd × image_size_multipliers × quality_multiplier;new-api GetGPTImage1PriceOnceCall 9 档 | 无 |
| BILL-030 | image amount_range (张数上下限) | 🟡(RequestTiers) | ✗ | ✗ | 已完成 | — | imagepricing AmountRange(Min/Max) — **HUAKAI 独有/领先** | 无 |
| BILL-031 | image output_token_upper_bound (按 size 封顶 token) | ✗ | ✗ | ✗ | 已完成 | — | imagepricing OutputTokenUpperBound(size) — **HUAKAI 独有** | 无 |
| BILL-032 | image token-image scheme (走 token 计价) | ✓ | ✓ | ✗ | 已完成 | — | imagepricing scheme=token_image | 无 |
| BILL-033 | audio per_char (input_char_micro_usd) | 🟡(走 token) | ✗ | ✗ | 已完成 | — | audiopricing scheme=per_char CharMicroUSD — **HUAKAI 独有方案** | 无 |
| BILL-034 | audio per_second (input_second_micro_usd) | 🟡 | ✗ | ✗ | 已完成 | — | audiopricing scheme=per_second SecondMicroUSD — **HUAKAI 独有方案** | 无 |
| BILL-035 | **tool-call: web_search 按次** | ✗ | ✓ | ✗ | 缺失 | P1 | new-api `tools.go` defaultToolPrices web_search=10.0 $/1K calls;GetToolPriceForModel;HUAKAI 仅通用 per_unit 无 tool-call 目录 | 真缺口:建 tool-call 计费目录(per-call $/1K) |
| BILL-036 | **tool-call: web_search_preview 按次 + model-prefix override** | ✗ | ✓ | ✗ | 缺失 | P1 | new-api web_search_preview=10.0;override gpt-4o*/4.1*=25.0;longest-prefix match | 同 BILL-035:含 model-prefix 覆盖机制 |
| BILL-037 | **tool-call: file_search 按次** | ✗ | ✓ | ✗ | 缺失 | P1 | new-api file_search=2.5 $/1K calls | 同 BILL-035 |
| BILL-038 | **tool-call: google_search (Gemini grounding) 按次** | ✗ | ✓ | ✗ | 缺失 | P1 | new-api google_search=14.0 | 同 BILL-035 |
| BILL-039 | tool-call: tool:model_prefix* override 机制 | ✗ | ✓ | ✗ | 缺失 | P2 | new-api `tools.go` prefixEntry longest-match index;RebuildToolPriceIndex atomic | 同 BILL-035 配套:longest-prefix 匹配引擎 |
| BILL-040 | rerank 单元计费 | ✗ | 🟡(走通用 ratio) | ✗ | 缺失 | P2 | new-api 走通用 model_ratio;HUAKAI 无 rerank 专计费 | 评估:可经 per_unit 方案表达 rerank search-unit |
| BILL-041 | tiered/区间阶梯 DSL 表达式 | ✓ | ✓ | ✗ | 已完成 | — | `billingdsl/{parser,evaluator}.go` + pricingeval isTieredPricingData;失败软回退 flat + RequiresReconciliation | 无 |
| BILL-042 | tiered expr smoke-test (存前校验向量) | 🟡 | ✓ | ✗ | 已完成 | — | billingdsl 解析校验 + tieredSnapshot;new-api SmokeTestExpr 4 token×2 request | 无 |
| BILL-043 | tiered expr request-aware (header/body 入参) | 🟡 | ✓ | ✗ | 部分完成 | P2 | new-api billingexpr RunExprWithRequest(anthropic-beta/service_tier/messages len);HUAKAI billingdsl 仅 usage 维度无 request-body 入参 | 真缺口(若需 header-aware 计费):扩展 DSL 入参 |
| BILL-044 | **long-context 整次倍率 (超阈值提价)** | ✓ | 🟡 | ✗ | 缺失 | P1 | sub2api LongContextInputThreshold=272000/InputMult=2.0/OutputMult=1.5(gpt-5.4);HUAKAI 无整次倍率档 | 真缺口:加 long-context 阈值×倍率档(Gemini/Claude 长上下文提价) |
| BILL-045 | **priority service-tier 双价** | ✓ | 🟡 | ✗ | 缺失 | P1 | sub2api InputPricePerTokenPriority/OutputPricePerTokenPriority/CacheReadPricePerTokenPriority;usePriorityServiceTierPricing;HUAKAI 无 priority tier 双价 | 真缺口:加 priority tier 价向量(配合 BILL-016) |
| BILL-046 | cache price override (per-tenant 手动覆写) | 🟡(channel) | ✓ | ✗ | 已完成 | — | `billing/cache_price_override.go` + `pricingeval/cache_override.go` | 无 |
| BILL-047 | pricing 公开价表 catalog | ✓ | ✓ | ✗ | 已完成 | — | `billing/public_price_table.go` + `pricingcatalog/catalog.go` + pricingcataloghttp;`GET /v1/pricing/rate-table` | 无 |
| BILL-048 | upstream ratio sync (远端拉价表) | 🟡(LiteLLM) | ✓ | ✗ | 已完成 | — | `modelsync/{service,http_fetcher,scheduler}.go`;migration 0088_pricing_ratio_audit_log | 无 |
| BILL-049 | pricing 版本表 + 审计日志 | 🟡 | 🟡 | ✗ | 已完成 | — | billing_pricing_versions(migration 0030/0031/0068)+ `pricingcatalog/audit.go` + 0088;`GET /v1/pricing/snapshots` | 无 |
| BILL-050 | pricing 管理写 API (运行时发布新价版本) | ✗(DB) | 🟡 | ✗ | 缺失 | P1 | `/v1/pricing/*` 仅 GET;grep POST/PUT pricing 零命中;只能改价靠 DB migration;`/admin/v1/pricing/ratios` 仅 ratio 读写(pool_group)非全价表写 | 真缺口:加 admin pricing 版本发布 API(运行时改价) |
| BILL-051 | pricing/ratio admin 读写 (pool_group ratio) | ✓ | ✓ | ✗ | 后端有·前端缺 | P2 | `/admin/v1/pricing/ratios` + `/admin/v1/pricing/ratios/{pool_group_id}`(151 §L 标"部分") | 补前端 ratio 管理面板 |

## C. 计费引擎 — token-level 机制 (每个机制一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| BILL-052 | input bucket 计费 | ✓ | ✓ | ✗ | 已完成 | — | pricingeval tokenMicros(InputTokens) + settler InputCost | 无 |
| BILL-053 | output bucket 计费 | ✓ | ✓ | ✗ | 已完成 | — | tokenMicros(OutputTokens) + settler OutputCost | 无 |
| BILL-054 | cache-create bucket 计费 | ✓ | ✓ | ✗ | 已完成 | — | cacheCreationMicros + settler CacheCreationCost | 无 |
| BILL-055 | cache-read bucket 计费 | ✓ | ✓ | ✗ | 已完成 | — | tokenMicros(CacheReadTokens) + settler CacheReadCost | 无 |
| BILL-056 | image-output bucket 计费 | ✓ | ✓ | ✗ | 已完成 | — | settler ImageOutputCost | 无 |
| BILL-057 | per_unit bucket (按次/按张) | 🟡 | ✓ | ✗ | 已完成 | — | pricingeval unitMicros(BillableUnits, PerUnit) | 无 |
| BILL-058 | **2-phase reserve(Tx1)→settle(Tx2)** | ✓ | ✓ | ✗ | 已完成 | — | `billing/billing.go` ClaimGate.Reserve(Tx1)+Settler.Settle(Tx2) SERIALIZABLE;`balancehold/balancehold.go` Reserve | 无 |
| BILL-059 | pre-consume 不足拒绝 (402/403) | ✓ | ✓ | ✗ | 已完成 | — | balancehold ErrBalanceHoldInsufficientBalance→402;new-api ErrorCodeInsufficientUserQuota 403 | 无 |
| BILL-060 | pre-consume free-model 开关 | ✗ | ✓ | ✗ | 部分完成 | P3 | new-api EnableFreeModelPreConsume default true;HUAKAI 经 quota Mode 控制,无专用 free-model preconsume flag | 评估必要性(quota mode 已覆盖) |
| BILL-061 | post-settle delta 补/退 (actual vs reserved) | ✓ | ✓ | ✗ | 已完成 | — | settler Tx2 actualCost flip claim;new-api delta:=actual-preConsumed | 无 |
| BILL-062 | balance-hold held 列 (capture/release) | ✓ | 🟡(无 held 列) | ✗ | 已完成 | — | balancehold Reserve/Capture/Release;migration 0060 balance_holds.state{held/captured/released}+captured 列 | 无 |
| BILL-063 | serializable Tx + 固定行锁序 | 🟡 | ✗ | ✗ | 已完成 | — | billing.go "fixed six-row lock order";pgx.Serializable — **领先 (refs 弱)** | 无 |
| BILL-064 | stream-state 4-态机精确计费 | 🟡(end-class) | 🟡 | ✗ | 已完成 | — | `billing/state.go` StreamState{acquired/inflight/partial/failed};CostForAttempt 只 partial 计费;AmbiguousUsage→0 — **HUAKAI 独有/领先** | 无 |
| BILL-065 | idempotent / replay 防重 (request_id fingerprint) | ✓ | ✓ | ✗ | 已完成 | — | `billing/replay_store.go` + migration 0044;ReserveResult.IdempotencyHit;ComputeIdempotencyFingerprint | 无 |
| BILL-066 | refund 负向 append-only event | ✓ | ✓ | ✗ | 已完成 | — | settler.Refund append-only event;migration 0027/0039 triggers | 无 |
| BILL-067 | abort 零成本中止 (input-only 审计) | 🟡 | ✓ | ✗ | 已完成 | — | settler.Abort tenant-scoped observedInputTokens;new-api ReturnPreConsumedQuota 整退 | 无 |
| BILL-068 | cache-hit L2 零成本仍写 usage | 🟡 | ✗ | ✗ | 已完成 | — | billing.CommitCacheHit settlement_source=response_cache_l2;migration 0043 — **HUAKAI 独有** | 无 |
| BILL-069 | reconciliation worker (pending 补算/崩溃恢复) | 🟡(deferred) | ✗ | ✗ | 已完成 | — | `billing/reconciliation_worker.go` + lease_sweep.go;migration 0032;settlementrecovery pkg | 无 |
| BILL-070 | settlement source 判别值 | 🟡 | ✗ | ✗ | 已完成 | — | settler settlement_source{provider_upstream/response_cache_l2};migration 0043/0083 | 无 |
| BILL-071 | billing settings (enforcement mode mandatory/opt-in) | ✗ | ✗ | ✗ | 已完成 | — | `billing/settings_policy.go` BalanceEnforcementMode;migration 0064;`/admin/v1/billing/settings` — **HUAKAI 独有** | 补前端设置面板 |
| BILL-072 | billing settings 审计轨迹 (原子 + admin audit) | ✗ | ✗ | ✗ | 已完成 | — | `gatewayhttp/admin_billing_settings_audit_tx.go` | 无 |
| BILL-073 | per-request cost 分桶明细 (cache split) | ✓ | 🟡 | ✗ | 已完成 | — | `chat_completions_pricing.go` actualCompletionCost→completionCostBreakdown(CacheCreationCost/CacheReadCost) | 无 |
| BILL-074 | append-only money-path 触发器 (不可变账本) | ✗ | ✗ | ✗ | 已完成 | — | migration 0027_ledger_append_only_trigger + 0039_money_path_append_only_triggers — **HUAKAI 独有** | 无 |
| BILL-075 | 余额由 billing_events 派生 SUM (无可变余额列) | ✗(可变列) | ✗(可变列) | ✗ | 已完成 | — | payment/types.go: 余额由 billing_events(payment_credited) 派生 SUM;migration 0039 — **HUAKAI 独有/领先** | 无 |
| BILL-076 | audit mismatch 检测 → 自动退款挂起 | 🟡 | ✗ | ✗ | 已完成 | — | `audit/mismatch_detector.go` + `audit/refund_worker.go`;migration 0032 | 无 |
| BILL-077 | billing 多租户隔离 (复合 FK 防跨租) | (隐含) | (隐含) | ✗ | 已完成 | — | `billing/fk_regression_integration_test.go`;claim_gate tenant-scope guard;tenant_id on every table | 无 |
| BILL-078 | provider-account in-flight 并发 slot 追踪 | ✗ | ✗ | ✗ | 已完成 | — | `db/billing/pool_slot_acquisitions.sql.go`;settler Tx2 释放 slot | 无 |

## D. Token 计量 / 计数 (每个机制一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| BILL-079 | input token 追踪 (上游响应实数) | ✓ | ✓ | 🟡(透传) | 已完成 | — | `proto/accounting.go`;usage_records.tokens_input | 无 |
| BILL-080 | output token 追踪 | ✓ | ✓ | 🟡 | 已完成 | — | proto/accounting.go;usage_records.tokens_output | 无 |
| BILL-081 | cache-creation token 追踪 (5m+1h split) | ✓ | ✓ | ✗ | 已完成 | — | migration 0002: cache_creation_5m_tokens / cache_creation_1h_tokens | 无 |
| BILL-082 | cache-read token 追踪 | ✓ | 🟡 | ✗ | 已完成 | — | migration 0002 cache_read_tokens | 无 |
| BILL-083 | reasoning/thinking token 追踪 | ✗ | 🟡 | ✗ | 已完成 | — | proto/accounting.go:16;forwarder_types.go:110;canonicalReasoningEstimate() (追踪✓但未单独计价,见 BILL-019) | 无 |
| BILL-084 | image-output token 追踪 | ✗ | 🟡 | ✗ | 已完成 | — | migration 0002 image_output_tokens | 无 |
| BILL-085 | 预请求启发式 token 估算 (len/4 + max_tokens) | ✓ | ✓ | ✗ | 已完成 | — | `chat_completions_pricing.go` 输入 len/4,输出 max_tokens;HeuristicEstimator | 无 |
| BILL-086 | 真实 tokenizer (tiktoken/vendor SDK 计数) | ✗ | ✗ | ✗ | 缺失 | P2 | grep tiktoken/cl100k/o200k 零命中(三 refs 均无真 tokenizer);估算 ±30% 误差 | 真缺口:接 tiktoken/vendor 计数器降低预留误差 |
| BILL-087 | tool/function-call schema token 计量 | ✗ | ✗ | ✗ | 缺失 | P3 | grep tool_tokens 零命中;tool schema 上游算 input 但未单独追踪 | 低优先;上游 input 已含 |

## E. 配额强制 knob (每个限额维度一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| BILL-088 | metric: USD/cost 余额上限 | ✓ | ✓ | ✗ | 已完成 | — | quota MetricCostUSD numeric(20,8) + balancehold | 无 |
| BILL-089 | metric: requests 计数限额 | ✓ | 🟡 | ✗ | 已完成 | — | quota MetricRequests | 无 |
| BILL-090 | metric: tokens_estimated 预估限额 | 🟡 | ✓ | ✗ | 已完成 | — | quota MetricTokensEstimated | 无 |
| BILL-091 | metric: concurrency 并发限额 | ✓ | 🟡 | ✗ | 已完成 | — | quota MetricConcurrency + ConcurrencySlot;service_integration_test.go:413 | 无 |
| BILL-092 | RPM (每分钟请求) | ✓ | ✓ | ✗ | 已完成 | — | budget CounterRPM + LimitPair.RPM | 无 |
| BILL-093 | TPM (每分钟 token) | ✓ | ✓ | ✗ | 已完成 | — | budget CounterTPM + LimitPair.TPM | 无 |
| BILL-094 | RPD/TPD (每日) | ✓ | ✓ | ✗ | 已完成 | — | quota WindowCalendarDay + counter | 无 |
| BILL-095 | 5h/7d 滚动窗口 | ✓ | 🟡 | ✗ | 部分完成 | P3 | sub2api api_key rate_limit_5h/7d;HUAKAI quota WindowFixed 可表达但无专用 5h/7d 常量档 | 评估:WindowFixed 已可表达任意窗口 |
| BILL-096 | per-key USD 上限 | ✓ | ✓ | ✗ | 已完成 | — | quota ScopeAPIKey;`userkeycontrols` SetKeyQuotaRequest.LimitUSD;`/v1/api-keys/{id}/quota` | 无 |
| BILL-097 | scope: global | 🟡 | 🟡 | ✗ | 已完成 | — | quota ScopeGlobal ("*") | 无 |
| BILL-098 | scope: user | ✓ | ✓ | ✗ | 已完成 | — | quota ScopeUser | 无 |
| BILL-099 | scope: api_key | ✓ | ✓ | ✗ | 已完成 | — | quota ScopeAPIKey | 无 |
| BILL-100 | scope: channel | ✓ | ✓ | ✗ | 已完成 | — | quota ScopeChannel | 无 |
| BILL-101 | scope: pool_group | ✓ | ✗ | ✗ | 已完成 | — | quota ScopePoolGroup | 无 |
| BILL-102 | scope: provider_account (egress cap) | ✓ | ✗ | ✗ | 部分完成 | P2 | quota ScopeProviderAccount(Slice A 只读);provider_accounts.cap_quota_daily/weekly/total 字段被 SQL 读但 pool dispatcher 未强制 | 真缺口:在 pool dispatcher 强制 provider-account egress cap |
| BILL-103 | mode: enforce | ✓ | ✓ | ✗ | 已完成 | — | quota ModeEnforce | 无 |
| BILL-104 | mode: observe (只观察不拒) | 🟡 | ✗ | ✗ | 已完成 | — | quota ModeObserve→DecisionObserveOnly — **领先** | 无 |
| BILL-105 | mode: manual_first | ✗ | ✗ | ✗ | 已完成 | — | quota ModeManualFirst — **HUAKAI 独有** | 无 |
| BILL-106 | mode: disabled | 🟡 | ✗ | ✗ | 已完成 | — | quota ModeDisabled | 无 |
| BILL-107 | window: fixed | ✓ | ✓ | ✗ | 已完成 | — | quota WindowFixed | 无 |
| BILL-108 | window: calendar_day | ✓ | ✓ | ✗ | 已完成 | — | quota WindowCalendarDay | 无 |
| BILL-109 | window: calendar_week | ✓ | 🟡 | ✗ | 已完成 | — | quota WindowCalendarWeek | 无 |
| BILL-110 | window: calendar_month | ✓ | ✓ | ✗ | 已完成 | — | quota WindowCalendarMonth;migration 0072 | 无 |
| BILL-111 | window: manual | ✗ | ✗ | ✗ | 已完成 | — | quota WindowManual — **HUAKAI 独有** | 无(注:无 admin 手动 reset 端点,见 BILL-122) |
| BILL-112 | fail-open 降级 | 🟡 | ✗ | ✗ | 已完成 | — | budget FailModeOpen + budget_fail_open_total expvar | 无 |
| BILL-113 | fail-closed 降级 | ✗ | ✗ | ✗ | 已完成 | — | budget FailModeClosed | 无 |
| BILL-114 | memory-fallback 降级 | 🟡 | ✗ | ✗ | 已完成 | — | budget FailModeMemoryFallback — **HUAKAI 独有** | 无 |
| BILL-115 | 预留 ledger + lease TTL 清扫 | 🟡 | ✗ | ✗ | 已完成 | — | quota/reservation.go ReservationStatus(reserved/settled/released/expired/reconciliation_needed) + quotaenforce DefaultLeaseTTL 90s + lease_sweep.go | 无 |
| BILL-116 | quota reconciliation job (后台补偿) | 🟡 | ✗ | ✗ | 已完成 | — | quota/reconciler.go ReconciliationJob | 无 |
| BILL-117 | decision: requires_reconciliation | ✗ | ✗ | ✗ | 已完成 | — | quota DecisionRequiresReconciliation — **HUAKAI 独有** | 无 |
| BILL-118 | budget limits per-model 覆盖 | ✓ | ✓ | ✗ | 已完成 | — | budget LimitSpec.Models map[model]LimitPair;StaticLimitsProvider(Users/Keys/PoolGroups) | 无 |
| BILL-119 | per-binding RPM/TPM/max-parallel 限额 | ✓ | ✓ | ✗ | 已完成 | — | migration 0008 rpm_limit/tpm_limit/max_parallel_requests;BindingMetadata | 无 |
| BILL-120 | model cooldown (上游 429 追踪) | ✓ | ✓ | ✗ | 已完成 | — | `rate/model_cooldown.go` RecordModelRateLimit(provider_account+model→reset_at) | 无 |
| BILL-121 | **quota 强制接入 gateway 热路径** | ✓ | ✓ | ✗ | 缺失 | P0 | grep: 零非测试文件 import internal/quota;claim_gate 仅 import balancehold 非 quota → window/concurrency/token quota **运行时零效果** | **最高优先真缺口**:在 claim_gate/dispatch 调用 quota.Service.Reserve() |
| BILL-122 | quota admin CRUD API (建/改/删策略) | ✓ | ✓ | ✗ | 缺失 | P1 | grep 无 /admin/v1/quota-policies 路由;仅订阅激活间接写 quota_policies | 真缺口:加 quota policy admin API + 手动 reset(BILL-111) |
| BILL-123 | per-key quota 强制 (dispatch 路径) | ✓ | ✓ | ✗ | 缺失 | P1 | ScopeAPIKey 定义但 gateway 无代码检查;依赖 BILL-121 | 随 BILL-121 接入热路径 |
| BILL-124 | per-channel / pool-group quota 强制 | ✓ | ✗ | ✗ | 缺失 | P2 | ScopeChannel/ScopePoolGroup 定义但无 router gate | 随 BILL-121 接入;pool-group dispatcher 检查 |
| BILL-125 | 用户可见 quota 状态 API (剩余/重置时间) | 🟡 | 🟡 | ✗ | 缺失 | P1 | `/v1/me/usage` 有(用量记录)但无 /v1/me/quota 显示剩余/窗口重置/策略上限 | 真缺口:加 /v1/me/quota(F-TRUST 透明原则) |

## F. 用量日志列 (每列一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| BILL-126 | request_id / upstream_request_id 列 | ✓ | ✓ | 🟡 | 已完成 | — | usage_records request_id + upstream_request_id | 无 |
| BILL-127 | model / requested_model / upstream_model 列 | ✓ | ✓ | 🟡 | 已完成 | — | usage_records 三列 | 无 |
| BILL-128 | input/output tokens 列 | ✓ | ✓ | 🟡 | 已完成 | — | InsertUsageRecordParams PromptTokens/CompletionTokens | 无 |
| BILL-129 | cache_creation/read tokens 列 (+5m/1h split) | ✓ | 🟡 | ✗ | 已完成 | — | cache_creation_tokens/cache_read_tokens/cache_creation_5m_tokens/1h_tokens | 无 |
| BILL-130 | per-bucket cost 列 (input/output/cache_create/cache_read/image_output) | ✓ | 🟡(总 Quota) | ✗ | 已完成 | — | migration 0002:5 个 cost 桶列 | 无 |
| BILL-131 | total_cost / actual_cost 列 | ✓ | ✓ | ✗ | 已完成 | — | settler actualCost | 无 |
| BILL-132 | rate_multiplier / account_rate_multiplier 列 | ✓ | 🟡 | ✗ | 部分完成 | P3 | HUAKAI group_ratio 存于 cost_snapshot,无独立 rate_multiplier 列 | 评估:cost_snapshot JSON 已含,无需独立列 |
| BILL-133 | billing_tier / billing_mode 列 (token/per_request/image) | ✓ | 🟡 | ✗ | 已完成 | — | settlement_source 区分 | 无 |
| BILL-134 | stream / duration_ms / first_token_ms 列 | ✓ | ✓ | ✗ | 部分完成 | P3 | HUAKAI 有 end_class;无 first_token_ms 列 | 评估补 first_token_ms(性能分析用) |
| BILL-135 | end_class / stream_state 列 | 🟡 | 🟡 | ✗ | 已完成 | — | end_class + stream_state(state.go) | 无 |
| BILL-136 | cost_snapshot (定价快照 JSON) + snapshot_version | 🟡 | ✗ | ✗ | 已完成 | — | cost_snapshot text(migration 0083)+ pricing version in snapshot — **HUAKAI 独有** | 无 |
| BILL-137 | image_count/size/breakdown 列 | ✓ | 🟡 | ✗ | 部分完成 | P3 | imagepricing 计算但 usage_records 无独立 image 列 | 评估补 image 维度列(sub2api 有 5 列) |
| BILL-138 | group_id/channel_id/api_key_id/account_id scope 列 | ✓ | ✓ | 🟡 | 已完成 | — | tenant/user/key/account scope | 无 |
| BILL-139 | subscription_id 列 | ✓ | 🟡 | ✗ | 已完成 | — | 经 subscription_policy_links | 无 |
| BILL-140 | ip_address / user_agent 列 | ✓ | ✓ | ✗ | 部分完成 | P3 | 151 §L 标"部分" | 确认是否落库 |
| BILL-141 | log type 枚举 (topup/consume/manage/system/error/refund) | 🟡 | ✓ | ✗ | 已完成 | — | billing_events kind | 无 |

## G. 用量分析 / 对账 / 争议 (每个机制一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| BILL-142 | 用量明细落库 | ✓ | ✓ | 🟡(透传无落库) | 已完成 | — | settler InsertUsageRecord | 无 |
| BILL-143 | 用户自用量查询 self-view API | ✓ | ✓ | 🟡 | 已完成 | — | `meusagehttp/{handler,generation_handler}.go`;`/v1/me/usage` | 补前端用量页(10A:今日/本月用量卡片 缺失/部分) |
| BILL-144 | 用户用量 time-series 图表 API | 🟡 | 🟡 | ✗ | 后端有·前端缺 | P2 | `/v1/me/analytics/time-series`(151 §C 已完成/部分);10A 图表时间范围切换=缺失/部分 | 补前端图表 |
| BILL-145 | dashboard 聚合/趋势 (admin) | ✓ | ✓ | ✗ | 后端有·前端缺 | P2 | `usageanalyticshttp/{handler,overview,performance}` TimeSeries/granularity;`/v1/admin/usage/overview` | 补 admin 用量 dashboard UI |
| BILL-146 | leaderboard 排行 | 🟡 | ✓ | ✗ | 后端有·前端缺 | P3 | `usageanalyticshttp/leaderboard_handler.go`;`/v1/admin/usage/leaderboard` | 补前端 |
| BILL-147 | 性能分析 (latency/throughput) | 🟡 | 🟡 | 🟡 | 后端有·前端缺 | P3 | `/v1/admin/usage/performance` | 补前端 |
| BILL-148 | 时区感知聚合 | 🟡 | 🟡 | ✗ | 已完成 | — | usage_analytics_tz_integration_test.go + 时区窗口 | 无 |
| BILL-149 | admin 用量/claims/audit 查询 (过滤) | ✓ | ✓ | ✗ | 后端有·前端缺 | P2 | `/admin/v1/usage` + `/admin/v1/billing/claims` + `/admin/v1/audit-events`(filter tenant/model/key/time) | 补前端 |
| BILL-150 | usage cleanup / 保留期 worker | ✓ | 🟡 | ✗ | 部分完成 | P3 | HUAKAI DLQ/audit 留存;无专用 usage cleanup worker | 评估:加保留期清扫 |
| BILL-151 | cost receipt 收据序列 (签名可验) | ✗ | ✗ | ✗ | 已完成 | — | user_cost_receipts(migration 0028/0033/0052)append-only;`/v1/receipts/{request_id}/verify` — **HUAKAI 独有/领先** | 无 |
| BILL-152 | 用户成本争议 (cost dispute 状态机) | ✗ | ✗ | ✗ | 已完成 | — | cost_disputes(migration 0084)dispute_id/reason/status{open/reviewing/resolved/rejected};`/v1/receipts/{id}/disputes` + `/v1/admin/disputes/{id}/resolve` — **HUAKAI 独有** | 补前端争议入口 |
| BILL-153 | DLQ + admin replay (settlement/billing/usage-record) | ✓(部分) | ✗ | ✗ | 已完成 | — | `dlq/`;`/admin/v1/dlq/{handler}/replay` + `/admin/v1/usage-record-dlq/{id}/replay` | 无 |
| BILL-154 | trust receipt / Merkle audit ledger | ✗ | ✗ | ✗ | 已完成 | — | `trustreceipt/` + `auditledger/`;`/v1/audit/merkle-tree.json` + `/v1/audit/verify` — **HUAKAI 独有/领先** | 无 |
| BILL-155 | 计费成本不匹配检测 + 自动退款 worker | 🟡 | ✗ | ✗ | 已完成 | — | `audit/mismatch_detector.go` + `audit/refund_worker.go`(见 BILL-076) | 无 |

## H. 计量↔订阅/激励接缝 (跨子系统,本表只列喂给计量/计费/配额的接口)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| BILL-156 | 订阅 cap → quota 日历窗口 (daily/weekly/monthly USD cap) | ✓ | 🟡 | ✗ | 已完成 | — | subscription_plans.daily/weekly/monthly_cap_usd → CapWindow{calendar_day/week/month} → quota 自动重置;subscription_policy_links(quota_policy_id) — **接缝在本模块** | 无(订阅本体由 D-money 负责) |
| BILL-157 | 订阅多桶计费 (sub 先扣→余额兜底) | ✓ | ✓ | ✗ | 部分完成 | P2 | sub2api usage_billing SubscriptionCost+BalanceCost;HUAKAI 订阅=cap guardrail 零碰 billing_events(**设计差异非缺陷**) | 论证设计差异 or 评估额度桶模型 |
| BILL-158 | 媒体异步任务计费 (estimate→hold→settle/refund) | ✗ | ✓ | ✗ | 已完成 | — | `mediatask` pricing.go EstimateCents via pricingeval per_unit;CompleteSuccess ErrActualExceedsEstimate + billing.Capture;failed→billing.Release;migration 0099 | 无 |
| BILL-159 | media task per-type default_estimated_cents | 🟡 | ✓ | ✗ | 已完成 | — | Config.DefaultEstimatedCents map[taskType]int64;KeyMediaTaskDefaultEstimatedCents | 无 |
| BILL-160 | aff_quota (邀请剩余额度独立桶) | ✓ | ✓ | ✗ | 部分完成 | P3 | new-api user.AffQuota;HUAKAI referral reward 入余额无独立 aff_quota 桶(**返佣主属 D-money**) | 与 D-money 对齐;非计量核心 |
| BILL-161 | 违规罚费 (CSAM/content surcharge 扣账) | ✗ | ✓ | ✗ | 缺失 | P3 | new-api violation_fee.go;HUAKAI 有审核(moderation 0082/0090)无罚费扣账 | 评估:审核命中→负向 billing_event |

---

## 总结

**计量/计费/配额/定价 — 真缺口 (refs 有具体实现, HUAKAI 缺,按优先级)**:
- **P0 BILL-121**: quota 引擎已全实现+集成测试但零生产代码 import → window/concurrency/token quota 运行时零效果。最高优先,接入 claim_gate/dispatch 热路径后 BILL-123/124 随之生效。
- **P1 定价缺口**: tool-call 按次计费目录(BILL-035~039 web_search/file_search/google_search +model-prefix override)、long-context 整次倍率(BILL-044)、priority service-tier 双价(BILL-045)+service-tier 系数(BILL-016)、reasoning token 独立定价(BILL-019)、pricing 运行时写 API(BILL-050)。
- **P1 配额/透明**: quota admin CRUD API(BILL-122)、用户可见 quota 状态 API(BILL-125)。
- **P2**: group_group_ratio 二维矩阵(BILL-008)、per-tenant 价表写 API(BILL-025)、tiered request-aware DSL(BILL-043)、provider-account egress cap 强制(BILL-102)、真 tokenizer(BILL-086)、rerank 计费(BILL-040)、用量分析前端(BILL-144~149)。

**HUAKAI 独有 / 领先 (refs 全无或仅 1 家)**:
- append-only money-path 触发器 + billing_events 派生 SUM 余额(无可变余额表)(BILL-074/075);cost receipts(BILL-151)+ cost disputes 状态机(BILL-152)+ trust/Merkle audit(BILL-154)+ audit-mismatch-refund(BILL-076/155)。
- stream-state 4-态机精确计费(只 partial 收费,AmbiguousUsage→0)(BILL-064);cache-hit L2 零成本仍写 usage(BILL-068);cost_snapshot 定价快照(BILL-136);固定行锁序 serializable(BILL-063)。
- quota: observe/manual_first/disabled mode + fail-open/closed/memory-fallback + 6-scope + requires_reconciliation decision + WindowManual + lease TTL 清扫(BILL-104~117)。
- imagepricing amount_range + output_token_upper_bound(BILL-030/031);audiopricing per_char + per_second 方案(BILL-033/034);billing enforcement-mode mandatory/opt-in(BILL-071)。

**设计差异 (非缺陷)**: 订阅=quota cap guardrail 零碰 billing_events(非额度桶先扣)(BILL-157);周期重置走 quota 日历窗口引擎自动重置无 reset worker;余额 micro-USD/numeric(20,8) 非 new-api int quota 倍率制。

**cliproxy**: 纯转发代理,全程无货币计费/配额/定价子系统;仅上游 quota-exhausted 探测 + 用量队列透传(per-key 计数),全表 ✗(仅用量透传 🟡)。

**⚠ 合并状态**: 上述后端能力在 `origin/fix/hermes-phase-1-e33d940`(183 paths);**main 仅 83 paths**,大量计费/配额/定价端点(pricing ratios、billing settings、media-task、disputes、quota)**未合并 main**。


# ====================  模块 F  ====================
# 标杆 · 支付/订阅/voucher/签到/返佣

> CANONICAL BENCHMARK feature tree — module-group: 支付/钱包/订单/PSP/provider-instance + 订阅 + voucher/兑换 + 签到 + 返佣/affiliate/邀请/增长.
> Merge of 3 sources (deduped): (1) 大功能树 payment-monetization.md + growth-ux.md; (2) 字段级细树 05-billing-fine.md + 08-keys-notify-frontend-deploy-fine.md; (3) 151-ref 12D/04/06/12E/12A.
> HUAKAI baseline: `origin/fix/hermes-phase-1-e33d940@e89d7fce` (2026-06-06). refs: sub2api `635ad81`, new-api `adc390c5`, CLIProxyAPI `3abfc83` (纯转发代理, 无货币子系统 → cliproxy 列一律 ✗).
>
> **refs cells**: ✓ = 已上架/有原生实现, 🟡 = 部分/间接, ✗ = 无.
> **HUAKAI状态 六级 (禁虚标)**: `已完成` / `后端有·前端缺` (backend route exists, no frontend) / `部分完成` (manual/taobao/hmac/test framework; 高级动作未确认) / `缺失` (真实 PSP per PROVIDERS.md 暂不实现; provider-instance CRUD; affiliate payout/transfer; promo) / `设计差异` (HUAKAI 有意不同模型, 非缺陷) / `领先` (HUAKAI 独有或仅 HUAKAI 有).
> **优先级**: P0 (商业闭环必须) / P1 (成熟后台) / P2 (生态对标) / P3 (合规/长尾) — P0 标注取自 12D.

---

## 1. 支付 PSP adapter — 真实支付渠道 (每渠道一行)

每个真实 PSP adapter = create-intent + webhook-verify + query + refund + cancel + 签名/密钥。HUAKAI PROVIDERS.md 明确: 真实 PSP 暂不实现, 只保留框架 → 全部 `缺失` (有意 park, 缺商户号/SDK/沙箱/资质)。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| PAY-001 | Payment Provider interface (CreateIntent/QueryOrder/Refund/Cancel) | ✓ | ✓ | ✗ | 已完成 | P0 | `payment/provider.go` Provider iface + CallbackVerifier | — 框架完备, 等 adapter 落地 |
| PAY-002 | manual provider (管理员手动确认) | 🟡 | ✓(AdminCompleteTopUp) | ✗ | 部分完成 | P0 | `payment/provider.go` manualProvider 生产可用, 不碰商户密钥 | 补用户端人工支付指引 UI |
| PAY-003 | test provider (本地 HMAC 签名回调) | 🟡 | 🟡 | ✗ | 部分完成 | P0 | `payment/provider.go` testProvider + SignTestCallback (默认密钥, 仅测试) | 生产不可当真实支付 |
| PAY-004 | HMAC bridge provider (HTTP-HMAC 桥, 可桥 epay 风格回调) | ✗ | ✗ | ✗ | 部分完成 | P0 | `payment/provider.go` hmacProvider + `paymenthttp/provider_hmac.go` 常量时间验签 | 桥接框架, 非具体 PSP |
| PAY-005 | Taobao/闲鱼 manual-redirect provider (checkout_url 人工对账) | ✗ | ✗ | ✗ | 领先 | P0 | `payment/provider.go` taobaoProvider, 无商户密钥 — HUAKAI UNIQUE | — |
| PAY-006 | Stripe adapter (checkout/intent + webhook + refund/cancel/query) | ✓(provider/stripe.go TypeStripe/Card/Link) | ✓(payment_stripe.go ApiSecret/WebhookSecret/PriceId/UnitPrice=8.0/MinTopUp=1/PromotionCodes) | ✗ | 缺失 | P0 | `payment/PROVIDERS.md:4` 暂不实现, 只保留框架 | 等商户资质后接 Stripe SDK |
| PAY-007 | Alipay (支付宝) adapter | ✓(provider/alipay.go TypeAlipay/AlipayDirect) | 🟡(经 epay 间接) | ✗ | 缺失 | P0 | `PROVIDERS.md` 暂不实现 | CN 市场必备, 待落地 |
| PAY-008 | WeChat Pay (微信) adapter (含 certSerial) | ✓(provider/wxpay.go TypeWxpay/WxpayDirect) | 🟡(经 epay) | ✗ | 缺失 | P0 | `PROVIDERS.md` 暂不实现 | CN 市场必备, 待落地 |
| PAY-009 | EasyPay/EPay (易支付聚合) adapter | ✓(provider/easypay.go TypeEasyPay) | ✓(epay.go EpayId/EpayKey/PayAddress/PayMethods; RequestEpay/EpayNotify) | ✗ | 缺失 | P0 | `PROVIDERS.md` 暂不实现 | CN 聚合渠道, 待落地 |
| PAY-010 | Airwallex adapter | ✓(provider/airwallex.go TypeAirwallex) | ✗ | ✗ | 缺失 | P0 | `PROVIDERS.md` 暂不实现 | 全球收单, 待落地 |
| PAY-011 | Creem adapter | ✗ | ✓(payment_creem.go ApiKey/Products/TestMode/WebhookSecret; topup_creem.go HMAC) | ✗ | 缺失 | P1 | refs only | 视市场可选 |
| PAY-012 | Waffo adapter | ✗ | ✓(payment_waffo.go ApiKey/PrivateKey/PublicCert/Sandbox/MerchantId/Notify/Return/Currency/UnitPrice=1.0) | ✗ | 缺失 | P1 | refs only | 视市场可选 |
| PAY-013 | Waffo-Pancake adapter | ✗ | ✓(payment_waffo_pancake.go UnitPrice=1.0/MinTopUp=1) | ✗ | 缺失 | P1 | refs only | 视市场可选 |
| PAY-014 | provider CallbackVerifier (常量时间验签, 失败零入账) | ✓(payment_webhook_provider.go) | ✓(各 provider HMAC) | ✗ | 已完成 | P0 | `provider.go` CallbackVerifier; 验签失败 ErrCallbackUnverified→零入账 | — |
| PAY-015 | 自动 webhook 回调入账路径 (P2a) | ✓ | ✓ | ✗ | 部分完成 | P0 | `payment/webhook.go` ConfirmPaidByCallback (HMAC-SHA256); 仅 test/hmac, 真 PSP 回调未接 | 真 PSP adapter 落地后接通 |
| PAY-016 | provider fee_rate (手续费率) | ✓(fee.go CalculatePayAmountForCurrency; payment_order.fee_rate) | ✗ | ✗ | 缺失 | P1 | refs only | 接真 PSP 时补 |
| PAY-017 | QR code 生成 (扫码支付) | ✓(qr_code + qr_code_img) | ✗ | ✗ | 缺失 | P1 | 无 qr_code 列/生成 | Alipay/WeChat 落地时补 |
| PAY-018 | 支付状态轮询 (async 确认) | ✓ | 🟡(stripe session) | ✗ | 缺失 | P1 | 无 polling loop/status-check 端点 | webhook 不可靠时补 |

## 2. 支付 provider 多实例 CRUD / 负载 / 限额 / 退款策略 (每能力一行)

sub2api `payment_provider_instances` (migration 096) 是多商户/多实例核心。HUAKAI 只有 `/providers/{provider}/config` 窄运行时配置 → instance CRUD `缺失`。AI provider-account CRUD ≠ 支付 PSP instance (不拿来抵扣)。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| PAY-019 | provider instance table/entity (多商户) | ✓(migration 096_payment_provider_instances + ent paymentproviderinstance) | 🟡(全局开关) | ✗ | 缺失 | P0 | 未见 instance table; 仅 KeyPaymentProviderConfig JSON{manual,taobao} | 建 payment_provider_instances 表 |
| PAY-020 | instance create/list/get/update/delete (CRUD) | ✓ | 🟡 | ✗ | 缺失 | P0 | `paymenthttp/handler.go:190-191` 仅 GET/PUT config, 无 CRUD | 建 admin instance CRUD 路由 |
| PAY-021 | instance enable/disable | ✓(enabled) | 🟡 | ✗ | 缺失 | P0 | 无 instance enable | — |
| PAY-022 | instance encrypted config payload | ✓(config + supported_types) | 🟡 | ✗ | 缺失 | P0 | 无加密 config payload | 接真 PSP 密钥时必备 |
| PAY-023 | instance secret rotation | ✓(成熟模式) | ✗ | ✗ | 缺失 | P1 | 无密钥轮换 | — |
| PAY-024 | instance weight / load-balance strategy | ✓(load_balance_strategy + sort_order) | ✗ | ✗ | 缺失 | P1 | 无支付池化负载 | — |
| PAY-025 | instance health / test | ✓ | ✗ | ✗ | 缺失 | P1 | AI provider health 不算 | — |
| PAY-026 | instance daily/monthly limits | ✓(limits) | ✗ | ✗ | 缺失 | P1 | 无 instance 限额 | — |
| PAY-027 | instance refund_enabled / allow_user_refund 策略 | ✓(refund_enabled + allow_user_refund) | ✗ | ✗ | 缺失 | P1 | 无 instance 退款策略 | — |
| PAY-028 | instance payment_mode / sort_order | ✓(payment_mode + sort_order) | ✗ | ✗ | 缺失 | P1 | — | — |
| PAY-029 | webhook per-instance routing | ✓ | ✗ | ✗ | 缺失 | P1 | 当前按 provider kind 路由 | — |
| PAY-030 | provider config 读取/更新 (runtime, 窄) | ✓ | 🟡 | ✗ | 已完成 | P0 | `paymenthttp/handler.go:190-191` GET/PUT /providers/{provider}/config | — (但非 instance CRUD) |

## 3. 钱包 / 余额 字段 (每列一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| PAY-031 | user balance 列 | ✓(billing_cache UserBalance) | ✓(user.go Quota) | ✗ | 已完成 | P0 | user_balances.balance numeric(20,8) (migration 0060/0065); GetBalance() | — |
| PAY-032 | 余额来源: 派生 SUM 不可变事件 (无可变余额列) | ✗(可变列) | ✗(可变列) | ✗ | 领先 | P0 | 余额由 billing_events(payment_credited) 派生 SUM; migration 0039 append-only — HUAKAI UNIQUE | — |
| PAY-033 | user held 列 (预留锁, capture/release) | ✓(多桶 hold) | ✗ | ✗ | 已完成 | P0 | user_balances.held numeric(20,8) CHECK>=0; balancehold Reserve/Capture/Release; migration 0060 state{held/captured/released} | — |
| PAY-034 | balance version (乐观锁) | 🟡 | ✗ | ✗ | 已完成 | P1 | user_balances.version bigint | — |
| PAY-035 | 余额预留 lease TTL 清扫 (崩溃安全释放) | 🟡(cleanup) | ✗ | ✗ | 已完成 | P1 | `billing/lease_sweep.go`; quotaenforce DefaultLeaseTTL 90s | — |
| PAY-036 | balance_enforcement_mode (mandatory/opt-in/strict/soft/disabled) | ✗ | ✗ | ✗ | 领先 | P1 | balancehold EnforcementModeMandatory/OptIn; migration 0064; claim_gate.go — HUAKAI UNIQUE | — |
| PAY-037 | 管理员手动 credit/debit + 审计 | ✓(admin_service_update_balance) | ✓(ManageUser quota) | ✗ | 已完成 | P0 | `payment/admin_credit.go` AdminAdjustBalance + `paymenthttp/admin_actions.go` | — |
| PAY-038 | 批量用户余额调整 | 🟡 | 🟡 | ✗ | 缺失 | P1 | AdminAdjustBalance 单用户 only | 补 bulk 端点 |
| PAY-039 | 后台手动余额调整页 (UI) | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | `/admin/v1/balances/adjustments` 后端有, 无 page | 建 admin balance UI |
| PAY-040 | 后台用户余额历史页 | ✓(balance-history) | 🟡 | ✗ | 后端有·前端缺 | P1 | 派生自 billing_events, 无 history UI | 建 balance-history UI |
| PAY-041 | aff_quota (邀请剩余额度独立桶) | ✓(affiliate) | ✓(user.go AffQuota/AffHistoryQuota) | ✗ | 设计差异 | P2 | referral reward 入余额, 无独立 aff_quota 桶 | — |
| PAY-042 | 低余额告警 (阈值邮件) | ✓(balance_notify_service.go 阈值+extra_emails+fixed/percentage) | 🟡(QuotaWarningThreshold) | ✗ | 部分完成 | P1 | user_notification_settings (migration 0089) BalanceThreshold default 5.00; 无专用低余额 mailer; 无 percentage/extra_emails | 接通低余额 mailer |
| PAY-043 | 注册赠额 / 新用户欢迎积分 | 🟡 | ✓(QuotaForNewUser) | ✗ | 部分完成 | P2 | 仅经 admin credit; 无自动 signup bonus | 补注册自动赠额 |

## 4. 充值订单 字段 + 生命周期 (每列/动作一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| PAY-044 | 创建充值订单 (用户自助) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `paymenthttp/handler.go:206` POST /orders; CreateOrder() | 建充值页 |
| PAY-045 | 订单状态机 (pending→paid→recharging→completed; refunded/expired/cancelled/failed) | ✓ | ✓ | ✗ | 已完成 | P0 | `payment/types.go` OrderStatus; recharging 显式断点恢复 | — |
| PAY-046 | out_trade_no 幂等键 (唯一) | ✓(out_trade_no unique) | ✓(TradeNo unique) | ✗ | 已完成 | P0 | migration 0071 unique (tenant_id,out_trade_no); ErrIdempotencyConflict | — |
| PAY-047 | amount / amount_cents (CHECK>0) | ✓(amount + pay_amount) | ✓(Amount + Money) | ✗ | 已完成 | P0 | payment_orders.amount_cents bigint CHECK>0 | — |
| PAY-048 | provider_kind / payment_type | ✓ | ✓ | ✗ | 已完成 | P0 | provider_kind{manual/test/hmac/taobao} | — |
| PAY-049 | provider_order_ref + provider_snapshot (jsonb) | ✓(payment_trade_no + provider_snapshot) | 🟡 | ✗ | 已完成 | P1 | provider_order_ref text + provider_snapshot jsonb | — |
| PAY-050 | request_fingerprint | ✓ | 🟡 | ✗ | 已完成 | P1 | request_fingerprint text | — |
| PAY-051 | pay_url / qr_code 列 | ✓(pay_url+qr_code+qr_code_img) | ✓(genStripeLink) | ✗ | 部分完成 | P1 | taobao checkout_url; 无 qr_code 列 | 接扫码 PSP 时补 |
| PAY-052 | order_type (recharge/subscription) + plan link | ✓(order_type+plan_id+subscription_group_id+days) | ✓(subscription_payment_*) | ✗ | 已完成 | P0 | SubscriptionGrant; subscription 订单 PG-only | — |
| PAY-053 | created_by_admin_id / confirmed_by_admin_id / confirm_reason | 🟡 | ✓(admin 补单) | ✗ | 已完成 | P1 | created_by_admin_id + confirmed_by_admin_id + confirm_reason | — |
| PAY-054 | failure_code / failure_message | ✓(failed_reason) | 🟡 | ✗ | 已完成 | P1 | failure_code + failure_message | — |
| PAY-055 | timestamps (expires/paid/recharging/completed/failed) | ✓ | ✓ | ✗ | 已完成 | P1 | expires_at/paid_at/recharging_at/completed_at/failed_at | — |
| PAY-056 | 两阶段履约 (recharging→completed, 幂等) | ✓ | ✓ | ✗ | 已完成 | P0 | Fulfill()/BeginFulfill/CompleteFulfill | — |
| PAY-057 | 订单过期主动清扫 (sweeper) | ✓(payment_order_expiry_service.go) | 🟡(stripe session) | ✗ | 已完成 | P1 | `payment/expire_sweeper.go` + store_postgres_expire.go | — |
| PAY-058 | pending 数限额 (反刷, default 3) | ✓(payment_config_limits.go) | ✗ | ✗ | 已完成 | P1 | MaxPendingPerUser; ErrPendingLimit | — |
| PAY-059 | daily 累计金额限额 ($500) | ✓ | ✗ | ✗ | 已完成 | P1 | DailyAmountLimit; ErrDailyAmountLimit | — |
| PAY-060 | 用户订单列表 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `handler.go:205` GET /orders; ListOrders() | 建订单中心页 |
| PAY-061 | 用户订单详情 / 状态轮询 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `handler.go:207` GET /orders/{id} | 建详情/轮询页 |
| PAY-062 | 用户取消订单 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `handler.go:208` POST /orders/{id}/cancel; 真 PSP cancel 缺 | 建取消按钮 |
| PAY-063 | 用户余额查询 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `handler.go:210` GET /balance | 建钱包页 |
| PAY-064 | 用户支付配置 (config) | ✓(config/checkout-info/plans/channels/limits) | ✓(topup info/self) | ✗ | 后端有·前端缺 | P0 | `handler.go:211` GET /config (manual/taobao, 更窄) | 建充值页 |
| PAY-065 | 后台确认支付 (manual 路径) | 🟡(admin 改余额) | ✓(AdminCompleteTopUp) | ✗ | 后端有·前端缺 | P0 | `handler.go:198` POST /{id}/confirm; AdminConfirmPaid() | 建后台订单页 |
| PAY-066 | 后台订单 retry | ✓ | 🟡 | ✗ | 后端有·前端缺 | P1 | `handler.go:196` POST /{id}/retry | — |
| PAY-067 | 后台订单 audit | ✓(payment_audit_log) | 🟡 | ✗ | 后端有·前端缺 | P1 | `handler.go:195` GET /{id}/audit | — |
| PAY-068 | 后台订单列表 + 筛选 (tenant/status/date/user) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `handler.go:188` GET / | 建后台订单页+筛选 |
| PAY-069 | 后台 payment dashboard | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `handler.go:189` GET /dashboard | 建后台 dashboard 页 |
| PAY-070 | 后台手动建单 | 🟡 | ✓ | ✗ | 后端有·前端缺 | P1 | `handler.go:187` POST / | — |
| PAY-071 | amount_options 预设档 | ✓(payment_amounts.go) | ✓({10,20,50,100,200,500}) | ✗ | 部分完成 | P2 | 由前端/平台配置, 无后端 amount_options 字段 | — |
| PAY-072 | amount_discount (金额折扣) | 🟡 | ✓(AmountDiscount map; getPayMoney) | ✗ | 缺失 | P2 | USD-only, 无折扣字段 | — |
| PAY-073 | min_topup | 🟡 | ✓(MinTopUp + per-provider) | ✗ | 部分完成 | P2 | amount_cents CHECK>0; 无 min 配置 | — |
| PAY-074 | topup_group_ratio (充值分组折扣倍率) | 🟡(plan-level) | ✓(GetTopupGroupRatio) | ✗ | 缺失 | P2 | 无充值分组倍率 | — |
| PAY-075 | compliance 确认 (terms version/at/by/ip) | 🟡 | ✓(ComplianceConfirmed/TermsVersion/At/By/IP) | ✗ | 缺失 | P3 | 无合规版本字段 | EU/合规需要时补 |
| PAY-076 | 多币种 (currency minor-unit) | ✓(currency.go DefaultCurrency=CNY; zero/two/three-decimal) | 🟡(USD/CNY/TOKENS) | ✗ | 缺失 | P1 | USD-only; ErrUnsupportedCurrency "P1 ledger USD-only" | 非 USD 市场前置 |
| PAY-077 | OpenRecharge 兼容桥 (legacy) | ✗ | ✗ | ✗ | 已完成 | P3 | `payment/order.go` OpenRecharge() | — |

## 5. 退款 / 争议 字段 (每列一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| PAY-078 | refund_amount / refund_reason | ✓(refund_amount+refund_reason) | 🟡 | ✗ | 已完成 | P1 | RefundRecord.amount_cents (migration 0092) + store_postgres_refund.go | — |
| PAY-079 | refund idempotency-key (防双扣) | 🟡 | ✗ | ✗ | 已完成 | P1 | RefundOrderInput.IdempotencyKey 必填; RefundResult.Idempotent | — |
| PAY-080 | refund 超额拒绝 (balance guard) | ✓ | ✗ | ✗ | 已完成 | P1 | ErrRefundExceedsAvailable; targeted test 通过 | — |
| PAY-081 | force_refund 标志 | ✓(force_refund) | ✗ | ✗ | 部分完成 | P2 | partial | — |
| PAY-082 | 用户自助退款申请 + 后台审批 | ✓(refund_requested_at/reason/by; allow_user_refund) | ✗ | ✗ | 后端有·前端缺 | P0 | `handler.go:209` POST /orders/{id}/refund-request; `refund_request_admin.go`; migration 0096; 用户非直接退款 | 建退款申请按钮 |
| PAY-083 | 后台退款申请列表 + approve/reject | ✓ | ✗ | ✗ | 后端有·前端缺 | P0 | `handler.go:192-194` GET /refund-requests + approve + reject | 建退款队列 UI |
| PAY-084 | 后台直接退款 (admin refund) | ✓ | 🟡 | ✗ | 后端有·前端缺 | P0 | `handler.go:200` POST /{id}/refund; 真 PSP refund 取决于 adapter | 建退款按钮 |
| PAY-085 | provider refund 调用 (真 PSP) | ✓(payment_refund.go provider.Refund) | 🟡 | ✗ | 缺失 | P0 | `provider.go` Refund(); manual/taobao ErrRefundUnsupportedKind; 真 PSP adapter 缺 | adapter 落地后接通 |
| PAY-086 | 真 PSP query / cancel | ✓ | ✓ | ✗ | 缺失 | P0 | 框架有 QueryOrder/Cancel iface, 真 adapter 缺 | adapter 落地后接通 |
| PAY-087 | refund 状态同步 / PSP 对账 | ✓ | 🟡 | ✗ | 缺失 | P1 | 与 PSP 退款状态同步未完成 | adapter 落地后补 |
| PAY-088 | audit mismatch refund (负向 reconciliation, DLQ) | 🟡 | ✗ | ✗ | 领先 | P1 | `audit/refund_worker.go` + migration 0032; reconciliation_worker.go — HUAKAI UNIQUE | — |
| PAY-089 | 用户成本争议 (cost dispute 状态机) | ✗ | ✗ | ✗ | 领先 | P2 | cost_disputes (migration 0084) status{open/reviewing/resolved/rejected}; `controlhttp/dispute_handler.go`; /v1/me/disputes, /v1/admin/disputes/{id}/resolve — HUAKAI UNIQUE | 补 dispute UI |
| PAY-090 | 支付审计事件 (脱敏 payload) | ✓(payment_audit_log) | 🟡 | ✗ | 已完成 | P1 | payment_audit_events (migration 0062/0071) event_type/actor/reason_class/redacted_payload jsonb; payment/privacy.go | — |
| PAY-091 | chargeback / 拒付处理 | ✗ | ✗ | ✗ | 缺失 | P3 | 无 chargeback 流 | 真 PSP 落地后补 |
| PAY-092 | 财务订单导出 (CSV/XLSX) | 🟡 | ✓(history) | ✗ | 缺失 | P1 | 无 export 端点 | 建财务导出 |
| PAY-093 | invoice / 账单 PDF | ✗ | ✗ | ✗ | 缺失 | P3 | 无 invoice 生成 | B2B 需要时补 |
| PAY-094 | tax / VAT / GST 计算 | ✗ | ✗ | ✗ | 缺失 | P3 | grep tax/vat/gst → 0 | EU/IN/AU 合规需要时补 |

## 6. 用户支付门户页面 (每屏一行) — 全部 `后端有·前端缺`

12E 证据: 云端 frontend/app 无 payment/wallet/orders/subscription/voucher/redeem/affiliate 目录。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| PAY-095 | 钱包页 | 🟡 | ✓(/wallet) | ✗ | 后端有·前端缺 | P0 | balance API 有, 无 wallet 页 | 建 /wallet |
| PAY-096 | 充值/购买页 | ✓(PaymentView) | ✓(TopUp) | ✗ | 后端有·前端缺 | P0 | createOrder API 有, 无页面 | 建充值页 |
| PAY-097 | 金额输入 + 限额提示 | ✓(AmountInput) | ✓ | ✗ | 后端有·前端缺 | P0 | 后端限额有, 前端缺 | — |
| PAY-098 | 支付方式选择器 | ✓(PaymentMethodSelector) | ✓ | ✗ | 缺失 | P0 | 前端缺 | — |
| PAY-099 | 支付 provider 卡片/列表 | ✓(ProviderCard/List/Dialog) | ✓ | ✗ | 缺失 | P0 | 前端缺 | — |
| PAY-100 | 人工支付指引 (manual/taobao) | 🟡 | ✗ | ✗ | 后端有·前端缺 | P0 | 后端有 instruction; 适合先补 | 建人工支付页 |
| PAY-101 | 订单列表页 | ✓(UserOrdersView/OrderTable/StatusBadge) | ✓ | ✗ | 后端有·前端缺 | P0 | 后端 list 有 | 建订单中心 |
| PAY-102 | 订单详情 / 轮询页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端 detail 有 | — |
| PAY-103 | 支付结果页 | ✓(PaymentResultView) | ✓ | ✗ | 缺失 | P0 | 前端缺 | — |
| PAY-104 | QR 支付页 | ✓(PaymentQRCodeView) | ✗ | ✗ | 缺失 | P1 | 真实扫码 PSP 缺 | — |
| PAY-105 | Stripe 支付页 | ✓(StripePayment/Popup) | ✓ | ✗ | 缺失 | P1 | 真实 Stripe 缺 | — |
| PAY-106 | Airwallex 支付页 | ✓(AirwallexPayment) | ✗ | ✗ | 缺失 | P1 | 真实 Airwallex 缺 | — |
| PAY-107 | 退款申请按钮 (用户) | ✓ | ✗ | ✗ | 后端有·前端缺 | P0 | 后端 refund-request 有 | — |
| PAY-108 | 支付品牌图标资产 (wxpay/stripe/easypay/alipay/airwallex svg) | ✓ | 🟡 | ✗ | 缺失 | P2 | 前端无支付品牌资产 | — |
| PAY-109 | public verify / resume-token resolve 页 | ✓(resume token resolve + public verify) | ✗ | ✗ | 缺失 | P2 | 未确认等价接口 | — |
| PAY-110 | 可退款 provider 列表 (refund-eligible) | ✓(refund-eligible-providers) | ✗ | ✗ | 缺失 | P2 | 未确认等价接口 | — |

## 7. 后台支付面板页面 (每屏一行) — 全部 `后端有·前端缺` (除 instance 缺失)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| PAY-111 | 后台支付订单页 | ✓(AdminOrdersView) | ✓(features/wallet) | ✗ | 后端有·前端缺 | P0 | paymenthttp 后端有, 无 page | 建后台订单页 |
| PAY-112 | 后台支付 dashboard 页 | ✓(AdminPaymentDashboardView) | ✓ | ✗ | 后端有·前端缺 | P0 | dashboard API 有 | 建 dashboard 页 |
| PAY-113 | 后台订单筛选 (tenant/status/date/user) | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | 后端部分有 | — |
| PAY-114 | 后台订单详情 audit 页 | ✓ | 🟡 | ✗ | 后端有·前端缺 | P1 | /{id}/audit 后端有 | — |
| PAY-115 | 后台 confirm/cancel/refund/retry UI | ✓ | 🟡 | ✗ | 后端有·前端缺 | P0 | 后端动作齐全 | — |
| PAY-116 | 后台 refund-request approve/reject UI | ✓ | ✗ | ✗ | 后端有·前端缺 | P0 | 后端 approve/reject 有 | — |
| PAY-117 | 后台 PSP config 页 | ✓ | ✓(payment compliance) | ✗ | 后端有·前端缺 | P1 | 后端窄 config | 建 config 页 |
| PAY-118 | 后台 PSP instance 页 | ✓(provider instances CRUD) | ✗ | ✗ | 缺失 | P0 | instance CRUD 后端缺 | 先补后端 (见 §2) |
| PAY-119 | 后台支付套餐 (payment plans) 页 | ✓(AdminPaymentPlansView+PlanEditDialog) | ✓ | ✗ | 后端有·前端缺 | P1 | 订阅 plans 后端有 | — |
| PAY-120 | PSP 对账 / settlement dashboard | ✓ | ✗ | ✗ | 缺失 | P1 | 成熟支付后台能力, 缺 | 真 PSP 落地后补 |
| PAY-121 | payment webhook replay 产品入口 | 🟡 | ✗ | ✗ | 部分完成 | P2 | DLQ replay (/admin/v1/dlq/{id}/replay) ≠ 支付回调重放产品 | — |

## 8. 订阅 — 套餐字段 + 用户/后台动作 (每字段/动作一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SUB-001 | plan 名称/描述 | ✓ | ✓(Title/Subtitle) | ✗ | 已完成 | P1 | subscription_plans.name+description (migration 0073) | — |
| SUB-002 | plan price_cents (CHECK>=0) | ✓(price+original_price) | ✓(PriceAmount decimal+Currency) | ✗ | 已完成 | P1 | price_cents bigint | — |
| SUB-003 | plan validity_days (≤36500) | ✓(validity_days+unit) | ✓(DurationUnit+Value+CustomSeconds) | ✗ | 已完成 | P1 | validity_days CHECK>0 | — |
| SUB-004 | plan granted/upgrade group | ✓(group_id) | ✓(UpgradeGroup) | ✗ | 已完成 | P1 | granted_group (= users.user_group, PRIMARY entitlement) | — |
| SUB-005 | plan daily/weekly/monthly cap USD | ✓(via window) | 🟡 | ✗ | 已完成 | P1 | daily/weekly/monthly_cap_usd numeric(20,8) nullable | — |
| SUB-006 | plan for_sale / enabled / sort_order | ✓ | ✓(Enabled+SortOrder) | ✗ | 已完成 | P1 | for_sale + enabled + sort_order | — |
| SUB-007 | plan total_amount (额度桶) | 🟡 | ✓(TotalAmount) | ✗ | 设计差异 | P2 | HUAKAI 订阅=cap guardrail, 非额度桶 | — |
| SUB-008 | plan max_purchase_per_user | ✗ | ✓(default 0=∞) | ✗ | 部分完成 | P2 | partial | — |
| SUB-009 | plan stripe_price_id / creem_product_id | 🟡 | ✓ | ✗ | 缺失 | P2 | 无真渠道 | 真 PSP 落地后补 |
| SUB-010 | quota_reset_period (daily/weekly/monthly) | ✓(window_start) | ✓(QuotaResetPeriod+CustomSeconds) | ✗ | 设计差异 | P2 | CapWindow → quota 日历窗口自动重置 (无 reset worker) | — |
| SUB-011 | subscription_policy_links (订阅↔quota策略) | ✗ | ✗ | ✗ | 领先 | P2 | subscription_policy_links: quota_policy_id+window_kind+status — HUAKAI UNIQUE | — |
| SUB-012 | user-sub start/expires/status | ✓ | ✓ | ✗ | 已完成 | P1 | starts_at+expires_at+status (migration 0073/0075) | — |
| SUB-013 | user-sub source (admin/order/voucher) | 🟡(assigned_by) | 🟡 | ✗ | 已完成 | P1 | source{admin/order/voucher}; FulfillmentEffect.SourceKind | — |
| SUB-014 | user-sub auto_renew | 🟡(maintenance queue) | ✗ | ✗ | 已完成 | P1 | UserSubscription.AutoRenew; migration 0094 | — |
| SUB-015 | user-sub prev_user_group (到期降级还原) | ✗ | ✓(PrevUserGroup) | ✗ | 已完成 | P1 | prev_user_group default 'default' | — |
| SUB-016 | 订阅自动续费 (recurring charge trigger) | 🟡 | ✗ | ✗ | 部分完成 | P1 | AutoRenew flag 有; ExpiryWorker 只标过期, 无 recurring 支付触发 | 接 PSP 后补 recurring |
| SUB-017 | 到期降级 enforce (worker) | ✓(subscription_expiry_service.go) | 🟡 | ✗ | 已完成 | P1 | `subscription/worker.go` ExpiryWorker; subscriptionenforce/gate.go; migration 0075 | — |
| SUB-018 | 到期提醒邮件 | ✗ | ✗ | ✗ | 领先 | P1 | `subscription/reminder_mailer.go`+reminder_worker.go; migration 0074 — HUAKAI UNIQUE | — |
| SUB-019 | 订阅幂等装配 (order/voucher/admin source) | ✓ | 🟡 | ✗ | 已完成 | P1 | `subscription/activation.go`+order_fulfillment.go+voucher_fulfillment.go | — |
| SUB-020 | proration (按比例退差) | ✗ | 🟡(custom seconds) | ✗ | 部分完成 | P2 | 叠买累加 MaxExpiresAt 防溢出; 无 proration | — |
| SUB-021 | grace period | ✗ | ✗ | ✗ | 缺失 | P2 | 无 grace_period 字段 | — |
| SUB-022 | 订阅 pause/freeze | ✗ | ✗ | ✗ | 缺失 | P2 | 无 pause 状态 | — |
| SUB-023 | trial subscription 试用期 | ✗ | ✗ | ✗ | 缺失 | P2 | 无 trial_period_days | — |
| **用户动作** |||||||||
| SUB-024 | 用户可购套餐列表 | ✓ | ✓(plans/self/preference) | ✗ | 后端有·前端缺 | P0 | `subscriptionhttp/handler.go:257` GET /plans | 建订阅页 |
| SUB-025 | 用户当前订阅状态 (/me) | ✓(active) | ✓ | ✗ | 后端有·前端缺 | P0 | `handler.go:256` GET /me | — |
| SUB-026 | 用户订阅列表 | ✓(list) | ✓ | ✗ | 后端有·前端缺 | P0 | `handler.go:255` GET / | — |
| SUB-027 | 用户自助购买订阅 | ✓ | ✓(balance/epay/stripe/creem pay) | ✗ | 后端有·前端缺 | P0 | `handler.go:259` POST /purchase | 建购买流 |
| SUB-028 | 余额购买订阅 | ✓ | ✓(balance pay) | ✗ | 后端有·前端缺 | P0 | 走 payment order; 前端缺 | — |
| SUB-029 | PSP 支付买订阅 | ✓ | ✓(多 PSP) | ✗ | 缺失 | P1 | 真 PSP 缺 | adapter 落地后补 |
| SUB-030 | 用户取消自动续订 | 🟡 | ✗ | ✗ | 后端有·前端缺 | P0 | `handler.go:258` POST /cancel-renew | — |
| SUB-031 | 订阅 progress / summary | ✓(progress+summary) | ✗ | ✗ | 缺失 | P1 | 未确认等价接口 | — |
| SUB-032 | 升级/降级/换套餐 lifecycle | 🟡 | ✓(upgrade group) | ✗ | 缺失 | P1 | ErrDowngradeNotAllowed; 无完整 change-plan; 不要虚标 | 补 change-plan |
| SUB-033 | 套餐卡片 UI | ✓(SubscriptionPlanCard) | ✓ | ✗ | 缺失 | P0 | 前端缺 | — |
| SUB-034 | 用户订阅页 (UI) | ✓(SubscriptionsView) | ✓(features/subscriptions) | ✗ | 后端有·前端缺 | P0 | renew/page.tsx 部分; 无完整订阅页 | 建 /subscriptions |
| **后台动作** |||||||||
| SUB-035 | 后台套餐 create/list/get | ✓ | ✓(plan create/update/status) | ✗ | 后端有·前端缺 | P1 | `handler.go:242-244` POST/GET /plans, GET /plans/{id} | 建后台套餐页 |
| SUB-036 | 后台套餐 disable | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | `handler.go:245` POST /plans/{id}/disable | — |
| SUB-037 | 后台套餐 update | ✓ | ✓ | ✗ | 缺失 | P1 | 仅见 disable, update 未确认 | 确认/补 update |
| SUB-038 | 后台订阅分配 (assign) | ✓ | ✓(create) | ✗ | 后端有·前端缺 | P1 | `handler.go:246` POST /assignments | — |
| SUB-039 | 后台分配列表/详情 | ✓ | 🟡 | ✗ | 后端有·前端缺 | P1 | `handler.go:247-248` GET /assignments + /{id} | — |
| SUB-040 | 后台订阅取消 (cancel) | ✓ | ✓(invalidate/delete) | ✗ | 后端有·前端缺 | P1 | `handler.go:249` POST /assignments/{id}/cancel | — |
| SUB-041 | 后台批量分配 (bulk-assign) | ✓ | ✗ | ✗ | 缺失 | P1 | 未确认 | 补 bulk-assign |
| SUB-042 | 后台延期 (extend) | ✓ | ✗ | ✗ | 缺失 | P1 | 未确认 | 补 extend |
| SUB-043 | 后台重置配额 (reset-quota) | ✓ | ✗ | ✗ | 缺失 | P1 | 未确认 | 补 reset-quota |
| SUB-044 | 后台撤销 (revoke/invalidate) | ✓ | ✓(invalidate) | ✗ | 缺失 | P1 | cancel ≠ revoke/invalidate | 补 revoke |
| SUB-045 | 后台 by-group/by-user 查询 | ✓ | 🟡 | ✗ | 缺失 | P1 | 未确认/不完整 | 补查询 |
| SUB-046 | 后台创建订阅券 | 🟡 | ✗ | ✗ | 后端有·前端缺 | P1 | `handler.go:250` POST /vouchers | — |
| SUB-047 | 后台订阅页 (UI) | ✓(SubscriptionsView admin) | ✓(features/subscriptions) | ✗ | 后端有·前端缺 | P1 | subscriptionhttp 后端有, 无 page | 建后台订阅页 |

## 9. Voucher / 兑换码 / promo 字段 (每列一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| VCH-001 | code 哈希存储 (不存原码) | ✓(哈希) | ✓(Key char(32) unique) | ✗ | 已完成 | P1 | voucher.code_fingerprint text; CodeHash bytea (migration 0023) | — |
| VCH-002 | amount_cents (CHECK>0) | ✓(value) | ✓(Quota default 100) | ✗ | 已完成 | P1 | voucher.amount_cents bigint | — |
| VCH-003 | status (active/expired/exhausted/revoked) | ✓ | ✓(Status) | ✗ | 已完成 | P1 | VoucherStatus enum | — |
| VCH-004 | valid_from / valid_until | 🟡(expires_at) | ✓(ExpiredTime) | ✗ | 已完成 | P1 | valid_from+valid_until timestamptz | — |
| VCH-005 | max_redemptions (CHECK>0) | 🟡(单用) | 🟡 | ✗ | 已完成 | P1 | max_redemptions integer | — |
| VCH-006 | redeemed_count | ✓(used_count) | 🟡 | ✗ | 已完成 | P1 | redeemed_count CHECK>=0 | — |
| VCH-007 | single_use_per_user (唯一索引) | ✓(promo) | 🟡 | ✗ | 已完成 | P1 | single_use_per_user default true; unique (tenant,voucher,user) | — |
| VCH-008 | eligible_user_id (限定用户) | 🟡 | ✗ | ✗ | 已完成 | P1 | eligible_user_id bigint nullable | — |
| VCH-009 | grant_kind: balance | ✓ | ✓ | ✗ | 已完成 | P1 | GrantKindBalance → billing_events | — |
| VCH-010 | grant_kind: subscription (码→激活订阅) | 🟡(group_id) | ✗ | ✗ | 领先 | P1 | GrantKindSubscription+SubscriptionPlanID; voucher_fulfillment.go — HUAKAI UNIQUE | — |
| VCH-011 | validity_days (兑换后授予时长) | ✓ | ✗ | ✗ | 已完成 | P2 | 经 plan ValidityDays | — |
| VCH-012 | batch 批量生成 (requested/created_count/status) | 🟡 | ✓(AddRedemption count) | ✗ | 已完成 | P1 | voucher_batch status{active/completed/failed/revoked} | — |
| VCH-013 | 反欺诈 / burst 限流 | ✓(redeemMaxErrorsPerHour=20) | ✗ | ✗ | 已完成 | P1 | `voucher/anti_fraud.go` ErrBurstLimited + idempotency.go + voucher_burst_block | — |
| VCH-014 | 兑换幂等 (防重) | ✓ | 🟡 | ✗ | 已完成 | P1 | `voucher/idempotency.go` | — |
| VCH-015 | 审计码不泄漏防护 + PII 脱敏 | 🟡 | ✗ | ✗ | 领先 | P1 | `voucher/audit.go` ErrAuditCodeLeakBlocked + privacy.go | — |
| VCH-016 | admin 撤销 (revoke + 原因 + admin) | 🟡 | ✓(DeleteRedemption) | ✗ | 已完成 | P1 | status=revoked + revoked_by_admin_id + revoked_reason + revoked_at | — |
| VCH-017 | promo/折扣码 (bonus, 区别于充值码) | ✓(promo_code.go bonus_amount/max_uses/used_count/status) | 🟡(redemption 兼) | ✗ | 缺失 | P1 | KeyPromoEnabled flag 存在; 无 promo 发码 pkg; voucher≠promo | 建 promo pkg |
| VCH-018 | 折扣/百分比券 (percentage-off) | ✗ | ✗ | ✗ | 缺失 | P2 | voucher 仅固定额度 grant | — |
| VCH-019 | 限时闪购 / campaign | ✗ | ✗ | ✗ | 缺失 | P2 | 无 flash-sale/campaign 类型 | — |
| **用户/后台动作** |||||||||
| VCH-020 | 用户兑换 (redeem) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `voucher_handler.go:85` POST /redeem; /v1/users/me/vouchers/redeem | 建兑换页 |
| VCH-021 | 用户充值记录 (recharges) | ✓(redeem history) | 🟡 | ✗ | 后端有·前端缺 | P1 | /v1/users/me/recharges | — |
| VCH-022 | 用户兑换历史 | ✓ | 🟡 | ✗ | 缺失 | P1 | 未确认用户兑换历史 | — |
| VCH-023 | 后台券 create / batch / batch 查询 | 🟡 | ✓ | ✗ | 后端有·前端缺 | P1 | `voucher_handler.go:78-81`; /v1/admin/vouchers + /batch + /batches/{id} | 建后台券页 |
| VCH-024 | 后台券 revoke | 🟡 | ✓ | ✗ | 后端有·前端缺 | P1 | /v1/admin/vouchers/{id}/revoke | — |
| VCH-025 | 用户兑换页 (UI) | ✓(RedeemView) | ✓(redemption-codes) | ✗ | 后端有·前端缺 | P0 | 后端有, 无 page | 建 /redeem |
| VCH-026 | 后台 redeem/promo codes 页 (UI) | ✓(RedeemView+PromoCodesView) | ✓ | ✗ | 后端有·前端缺 | P1 | voucher admin 后端有, 无 page | — |

## 10. 每日签到 (每机制一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CHK-001 | 签到 enabled 开关 | ✗ | ✓(checkin_setting.go default false) | ✗ | 已完成 | P2 | platformsettings KeyCheckinEnabled default false | — |
| CHK-002 | 签到奖励 min/max | ✗ | ✓(MinQuota=1000/MaxQuota=10000) | ✗ | 已完成 | P2 | KeyCheckinMinCents=1 / MaxCents=20 | — |
| CHK-003 | 签到随机奖励算法 (crypto/rand) | ✗ | ✓(rand.Intn, math/rand) | ✗ | 领先 | P2 | `checkin/service.go` randomRewardCents (crypto/rand big.Int) — 更强随机 | — |
| CHK-004 | 签到去重 (per-day unique) | ✗ | ✓(uniqueIndex user+date) | ✗ | 已完成 | P2 | migration 0097_daily_checkin | — |
| CHK-005 | 签到入账 | ✗ | ✓(IncreaseUserQuota) | ✗ | 已完成 | P2 | `payment/checkin_reward.go` ApplyCheckinReward | — |
| CHK-006 | 签到累计统计 (total_checkins/sum) | ✗ | ✓(SUM quota_awarded) | ✗ | 已完成 | P2 | checkin GetStatus | — |
| CHK-007 | 用户签到动作 (do checkin + status) | ✗ | ✓(DoCheckin/GetCheckinStatus) | ✗ | 后端有·前端缺 | P2 | `checkinhttp/handler.go:53`; /v1/me/checkin (targeted test) | 建签到 UI |
| CHK-008 | 签到连续/streak 乘数 | ✗ | 🟡(monthly stats, no multiplier) | ✗ | 缺失 | P2 | random per day, 无 streak | 补 streak |
| CHK-009 | 签到 UI (前端页) | ✗ | ✓(React check-in page) | ✗ | 后端有·前端缺 | P2 | backend+http only | 建 /checkin |

## 11. 返佣 / affiliate / 邀请 / 增长 (每机制/动作一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| AFF-001 | 邀请码生成 (random) | 🟡(AffCode=invite) | ✓(AffCode) | ✗ | 后端有·前端缺 | P1 | `community/invitation/service.go` Generate (code_generator); /v1/invitations, /v1/me/invitations | 建邀请 UI |
| AFF-002 | 邀请码绑定/引荐关系 (referrals 表) | ✓(BindInviter) | ✓(GetUserIdByAffCode+InviterId) | ✗ | 已完成 | P1 | referral bind via auditreceipt hook; migration 0034 referrals+referral_rewards | — |
| AFF-003 | 邀请 max-usage 上限 | ✗ | ✗ | ✗ | 已完成 | P2 | MaxUsage per code | — |
| AFF-004 | 邀请过期 (expiry) | ✗ | ✗ | ✗ | 已完成 | P2 | maxExpiresDays=90; expiresAt | — |
| AFF-005 | 邀请月度租户配额 (monthly tenant quota) | ✗ | ✗ | ✗ | 已完成 | P2 | MonthlyTenantQuota=100 (checkTenantMonthlyQuota) | — |
| AFF-006 | 推荐资格判定 (pending→qualified on billing) | ✓(freeze hours; rebate on order/redeem) | 🟡 | ✗ | 已完成 | P1 | `referral_qualification.go` QualifyPendingReferral | — |
| AFF-007 | 推荐奖励 payout 入账 (qualify 时同事务) | ✓(applyAffiliateRebateForOrder/AccrueQuota) | ✓(TransferAffQuota) | ✗ | 已完成 | P1 | `referral_reward_store.go`→`payment/referral_reward.go`; migration 0095/0100 (idempotent same-tx) | — |
| AFF-008 | 推荐奖励金额配置 (RewardUSDMicros) | 🟡 | ✗ | ✗ | 已完成 | P1 | referral_reward_config RewardUSDMicros; KeyReferralRewardCents=50 | — |
| AFF-009 | 推荐奖励 enabled 开关 | 🟡 | ✗ | ✗ | 已完成 | P1 | KeyReferralRewardEnabled | — |
| AFF-010 | 推荐 tier: silver/gold/platinum (3级) | 🟡(无 tier) | ✗ | ✗ | 领先 | P2 | TierThresholds Silver=3/Gold=10/Platinum=50 (env 可配); tierForQualifiedReferralCount() — HUAKAI UNIQUE | — |
| AFF-011 | 注册新用户赠额 | 🟡 | ✓(QuotaForNewUser) | ✗ | 部分完成 | P2 | 仅经 admin credit | 补自动赠额 |
| AFF-012 | 返佣提现/转账 (cashout/transfer) | ✓(aff transfer; transfer 明细) | ✓(aff_transfer/TransferAffQuota) | ✗ | 缺失 | P1 | 4 项目奖励一律入余额不可提现; HUAKAI 无 transfer 闭环 | 补 transfer ledger |
| AFF-013 | 违规罚费 (CSAM/content surcharge) | ✗ | ✓(violation_fee.go CSAM marker) | ✗ | 缺失 | P3 | HUAKAI 有审核 (migration 0082/0090) 无罚费扣账 | — |
| **用户/后台动作** |||||||||
| AFF-014 | 用户邀请码生成 (UI) | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | 后端有, 前端缺 | 建返佣页 |
| AFF-015 | 我的邀请统计 (summary) | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | summary 后端有 | — |
| AFF-016 | 用户返佣页 (UI) | ✓(AffiliateView) | ✓(rankings/aff) | ✗ | 后端有·前端缺 | P1 | 前端缺 | 建 /affiliate |
| AFF-017 | 邀请人列表 (admin invites) | ✓(AdminAffiliateInvitesView) | 🟡 | ✗ | 缺失 | P1 | 未确认等价后台列表 | 建 admin invites |
| AFF-018 | 被邀请人转化状态 | ✓ | 🟡 | ✗ | 缺失 | P1 | 未确认 | — |
| AFF-019 | rebate 明细 (admin rebates) | ✓(AdminAffiliateRebatesView) | 🟡 | ✗ | 缺失 | P1 | 未确认完整后台 | 建 admin rebates |
| AFF-020 | transfer 明细 (admin transfers) | ✓(AdminAffiliateTransfersView) | 🟡 | ✗ | 缺失 | P1 | 确认缺失 | 建 admin transfers |
| AFF-021 | 返佣后台 records 表 | ✓(AdminAffiliateRecordsTable) | 🟡 | ✗ | 缺失 | P1 | 确认缺失 | — |
| AFF-022 | admin 批量设置返佣 rate (batch-rate) | ✓(batch-rate) | ✗ | ✗ | 缺失 | P1 | 未确认 | 补 batch-rate |
| AFF-023 | admin user overview / update / clear settings | ✓ | ✗ | ✗ | 缺失 | P1 | 确认缺失前端 | — |
| AFF-024 | 返佣后台 overview 页 (UI) | ✓ | 🟡 | ✗ | 缺失 | P1 | 确认缺失前端 | 建 admin overview |
| AFF-025 | referral fraud/risk rule | 🟡 | ✗ | ✗ | 缺失 | P2 | 未确认 | — |
| AFF-026 | 排行榜 (leaderboard, 增长侧) | 🟡(rankings.go) | ✓(GetRankings week) | ✗ | 后端有·前端缺 | P2 | `usageanalyticshttp/leaderboard_handler.go` (window≤90d, TTL30s); 无 UI | 建排行榜 UI |
| AFF-027 | 用户侧 outbound webhook (事件订阅) | ✗ | ✓(NotifyTypeWebhook+SSRF) | ✗ | 部分完成 | P2 | notify TypeWebhook (signWebhook HMAC + SSRF guard); 非通用 API 事件订阅 | — |
| AFF-028 | 积分/成就/游戏化 | ✗ | ✗ | ✗ | 缺失 | P3 | grep points/gamif/badge → 0 | — |

---

## 总结 (merged gap summary)

**三条断层 (12D 结论)**:
1. **前端闭环断层** — 支付/钱包/订单/订阅/voucher/redeem/affiliate/checkin 后端路由大面积齐全, 但 frontend/app 无对应页面 (9 页中仅 dashboard/audit 活链接, accounts/api-keys/usage/settings 导航 disabled)。最大量级缺口。
2. **真实 PSP + provider-instance 断层** — PROVIDERS.md 明确真实 PSP (Stripe/Alipay/WeChat/EasyPay/Airwallex/Creem/Waffo) 暂不实现, 只保留 manual/test/hmac/taobao 框架; payment_provider_instances 表/CRUD/负载/限额/退款策略/密钥轮换全缺。有意 park。
3. **订阅后台高级动作 + affiliate 后台 + promo 断层** — bulk-assign/extend/reset-quota/revoke/change-plan 未确认; affiliate invites/rebates/transfers/overview/batch-rate + 提现闭环缺; promo/折扣码缺。

**HUAKAI 领先 (refs 全无或仅 HUAKAI 有)**:
- 余额: billing_events 派生 SUM (无可变余额列) + append-only money-path 触发器 (migration 0027/0039); balance_enforcement_mode mandatory/opt-in; lease TTL 清扫。
- 支付: Taobao/闲鱼 manual-redirect provider; refund idempotency-key + 用户自助退款审批; audit-mismatch-refund DLQ; cost_disputes 状态机 (open/reviewing/resolved/rejected)。
- 订阅: 到期提醒邮件 + auto_renew + prev_user_group 还原 + subscription_policy_links + grant_kind=subscription voucher。
- voucher: grant_kind=subscription; audit-code-leak 防护; anti-fraud burst 限流。
- 签到: crypto/rand 奖励 (强于 new-api math/rand)。
- 返佣: 3-tier silver=3/gold=10/platinum=50 (env 可配); qualify 同事务 idempotent 入账 (migration 0100)。

**HUAKAI 设计差异 (非缺陷)**: 订阅=quota cap guardrail (零碰 billing_events), 非"额度桶先扣"; 周期重置走 quota 日历窗口引擎 (无 reset worker); 余额 micro-USD/numeric(20,8), 非 new-api int quota 倍率制。

**cliproxy**: 纯转发代理, 无任何货币计费/钱包/支付/订阅/voucher/签到/返佣子系统 → 全表 ✗。


# ====================  模块 G  ====================
# 标杆 · 审计/可观测 + 安全/隐私/反封禁

> CANONICAL BENCHMARK feature tree — 由 3 个来源合并去重:
> 1. 大功能树 (`observability-analytics.md` + `security-abuse.md`)
> 2. 字段级细树 (`06-audit-obs-fine.md` + `07-security-fine.md`)
> 3. 151-ref (`06-huakai-endpoint-status-tree.md` F/L 段 + `12D-...priority-tree.md` §10 OPS)
>
> HUAKAI 云端基线: `origin/fix/hermes-phase-1-e33d940@e89d7fce` (183 OpenAPI paths; main 只有 83)。
> 参考基线: sub2api `635ad81`, new-api `adc390c5`, CLIProxyAPI `3abfc83d`。
>
> 行 schema: `| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |`
> - refs 列: ✓ 有 / 🟡 部分 / ✗ 无。
> - HUAKAI状态 六级(禁虚标): 已完成 / 部分完成 / 后端有·前端缺 / 缺失 / 未做 / 未合并main。
> - 优先级: P0–P3 / — (— = HUAKAI 已强于参照, 无推进动作 / 仅监控)。
>
> 两节互不混排: 【第一节 审计/信任链/可观测/运维/可靠性 OBS-xxx】【第二节 安全/隐私/反封禁/网络策略/内容审计 SEC-xxx】。

---

# 第一节 · 审计 / 信任链 / 可观测 / 运维 / 可靠性 (OBS-xxx)

## 1.1 请求/中继日志 — 每列一行

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-001 | 每请求日志记录(model/provider/latency/tokens/cost/status) | ✓ | ✓ | 🟡 | 已完成 | — | eventbus/types.go:58-77 RequestCompletionEvent; obs/repository.go:51-75 UsageRow | 监控 |
| OBS-002 | 结构化日志字段(typed schema, 非裸字符串) | 🟡 | 🟡 | 🟡 | 已完成 | — | obs/repository.go:51-75 sqlc typed; eventbus/types.go | 监控 |
| OBS-003 | 输入 token 计数 | ✓ | ✓ | 🟡 | 已完成 | — | gateway/forwarder_types.go:84 TokensInput; obs/repository.go:59 | — |
| OBS-004 | 输出 token 计数 | ✓ | ✓ | 🟡 | 已完成 | — | forwarder_types.go:85 TokensOutput; obs/repository.go:60 | — |
| OBS-005 | 缓存 token 计数(creation/read) | ✓ | 🟡 | ✗ | 已完成 | — | forwarder_types.go:87-89 CacheCreation/ReadTokens; cachemetrics; obs/repository.go:61-62 | — |
| OBS-006 | 缓存 5m / 1h 分层 token | ✓ | ✗ | ✗ | 部分完成 | P3 | sub2api usage_log.go:76,78; HUAKAI cacheplan pkg(无 5m/1h 列) | 评估是否需 5m/1h 拆列入账 |
| OBS-007 | 每请求成本计算(USD/credits, micros) | 🟡 | 🟡 | ✗ | 已完成 | — | forwarder_types.go:91 ActualCost; billing/settler.go; cost_usd_micros(0028) | — |
| OBS-008 | 成本拆分(input/output/cache_creation/cache_read/total/actual) | ✓ | 🟡 | ✗ | 已完成 | — | sub2api usage_log.go:82-97; HUAKAI cost_usd_micros + billing_events actual_cost_signed | — |
| OBS-009 | rate_multiplier / account_rate_multiplier 双倍率 | ✓ | ✗ | ✗ | 部分完成 | P2 | sub2api usage_log.go:100,105; HUAKAI pricing_ratio_audit_log(0088) 有审计无双倍率列 | 在 receipt/usage 落 account 级倍率快照 |
| OBS-010 | 累计成本(user/api_key/channel) | ✓ | ✓ | ✗ | 已完成 | — | meusagehttp/handler.go GET /v1/me/usage; admin_observability_handler.go GET /admin/v1/usage | — |
| OBS-011 | 每用户配额消费追踪(scope-aware) | ✓ | ✓ | ✗ | 已完成 | — | quota/types.go Scope/MetricCostUSD/Reservation; quota/service.go ReserveRequest | — |
| OBS-012 | 每模型用量拆分 | ✓ | ✓ | 🟡 | 已完成 | — | obs/repository.go RequestedModel; db/billing/observability.sql.go Model filter | — |
| OBS-013 | 请求计数(by model/channel/user) | ✓ | ✓ | ✗ | 已完成 | — | obs/repository.go ListUsage; admin_observability_handler.go Model filter | — |
| OBS-014 | 错误率/错误码拆分(EndClass enum) | ✓ | 🟡 | 🟡 | 已完成 | — | forwarder_types.go:18-35 StreamEndClass(graceful/upstream_4xx/5xx/timeout); obs/repository.go:64 EndClass | — |
| OBS-015 | 请求体日志(prompt content) | 🟡 opt-in | 🟡 Other JSON | ✓ 全量 | 缺失(设计性) | P2 | privacy/middleware.go body 读后清零, 仅 hash ref; eventbus/types.go:70 RedactedBodyRef | 可选 permission-gated opt-in prompt log(合规/调试) |
| OBS-016 | 响应体日志(completion content) | 🟡 | ✗ | ✓ | 部分完成 | P3 | trustreceipt/builder.go signed receipt(含 completion); audit/receipt_storage.go; usage_records 主表无 | 评估是否将 completion 入主表(默认仍脱敏) |
| OBS-017 | 每渠道/每 provider 用量计数表 | 🟡 | ✓ | ✗ | 部分完成 | P2 | pool/router/metrics.go; channelhealth/service.go; admin_observability_handler.go Provider filter(聚合, 无专表) | 建专用 per-channel 用量计数表(req/token/cost) |
| OBS-018 | log.UseTime(请求延迟 s/ms) 列 | ✓ | ✓ | 🟡 | 缺失 | P2 | sub2api usage_log duration_ms; new-api log.go:32 UseTime; HUAKAI 无延迟列 | 在 usage/receipt 落 latency 列 |
| OBS-019 | IsStream 流式标记 | ✓ | ✓ | ✓ | 已完成 | — | sub2api usage_log stream; new-api log.go:33; HUAKAI billing_events stream_state | — |
| OBS-020 | log.Ip(客户端 IP) 列 | ✓ | ✓ | ✗ | 缺失(设计性) | — | sub2api usage_log ip_address; new-api log.go:38; HUAKAI 按设计脱敏不存 PII | 监控(隐私优先, 故意不存) |
| OBS-021 | log.Username/TokenName/ChannelName join 列 | ✗ | ✓ | ✗ | 缺失 | P3 | new-api log.go:26,27,35; HUAKAI 用 id 不冗余存名 | 评估读模型 join 即可, 不一定入表 |
| OBS-022 | log.Group(分组)列 | ✓ | ✓ | ✗ | 缺失 | P3 | sub2api usage_log group_id; new-api log.go:37 | 若引入分组计费再补 |
| OBS-023 | UpstreamRequestId/upstream_model 列 | ✓ | ✓ | 🟡 | 已完成 | — | new-api log.go:40; HUAKAI AuditLedgerResult.UpstreamProvider/Model(result.go) | — |
| OBS-024 | image_count/size/breakdown 列 | ✓ | ✗ | ✗ | 部分完成 | P3 | sub2api usage_log.go:131-149; HUAKAI imageshttp/imagepricing(无完整 breakdown 列) | 图像计费场景补 breakdown 列 |
| OBS-025 | usage-statistics-enabled / request-log 开关 | ✓ | ✓ LogConsumeEnabled | ✓ sdk_config.go:32 | 部分完成 | P3 | HUAKAI 有 HUAKAI_METRICS_PROMETHEUS 开关, 无 per-channel log-body 开关 | 加 per-tenant/channel log opt-in 开关 |

## 1.2 审计事件账本 — append-only / hash-chain / signature (每字段一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-026 | audit_ledger ledger_id(ULID UNIQUE) | ✗ | ✗ | ✗ | 已完成(强于全部参照) | — | auditledger/types.go LedgerEntry.LedgerID; sql 0013 | — |
| OBS-027 | occurred_at(timestamptz, idx DESC) | ✗ | ✗ | ✗ | 已完成 | — | LedgerEntry.Timestamp RFC3339Nano; 0013 | — |
| OBS-028 | request_id(UNIQUE, 一请求一条) | ✓ | ✓ | ✓ | 已完成 | — | RequestID; 0013 UNIQUE; ledger.go ErrDuplicateRequestID | — |
| OBS-029 | tenant_id + tenant_scope_ref(隐私安全租户引用) | ✗ | ✗ | ✗ | 已完成 | — | TenantID/TenantScopeRef; GetByRequestIDAndTenantScope | — |
| OBS-030 | hop_chain(jsonb, 6 hops, redact-allowlist) | ✗ | ✗ | ✗ | 已完成 | — | HopChain []proto.HopAttestation; 0013 | — |
| OBS-031 | model_chain(jsonb {requested/route_decided/upstream_reported}) | 🟡 | ✗ | ✗ | 已完成 | — | ModelChain *proto.ModelChain(IsConsistent) | — |
| OBS-032 | prev_merkle_root + merkle_root = sha256(prev‖entry_hash) | ✗ | ✗ | ✗ | 已完成 | — | PrevMerkleRoot/MerkleRoot [32]byte; merkle.go NextMerkleRoot; 0013 CHECK len=32 | — |
| OBS-033 | pubkey_fingerprint(text len=16, sha256(pk)[:8]) | ✗ | ✗ | ✗ | 已完成 | — | PubkeyFingerprint; 0013 | — |
| OBS-034 | signature(ed25519/base64, len>0) | ✗ | ✗ | ✗ | 已完成 | — | Signature; signer.go LocalEd25519Signer | — |
| OBS-035 | append-only DB trigger(UPDATE/DELETE 拒) | ✗ | ✗ | ✗ | 已完成 | — | sql 0027 enforce_audit_append_only; 0028 trigger | — |
| OBS-036 | hash-chain link verify(VerifyChain/ChainError) | ✗ | ✗ | ✗ | 已完成 | — | auditledger/merkle.go VerifyChain | — |
| OBS-037 | ed25519 signer iface(Local + KMS-ready) | ✗ | ✗ | ✗ | 已完成 | — | auditledger/signer.go Signer | — |
| OBS-038 | Postgres advisory-lock 串行 append | ✗ | ✗ | ✗ | 已完成 | — | auditledger/postgres.go PostgresLedger tenant adv-lock | — |
| OBS-039 | Memory/Noop/Postgres ledger 多实现 | ✗ | ✗ | ✗ | 已完成 | — | ledger.go MemoryLedger/NoopLedger; postgres.go | — |
| OBS-040 | 持久化 payload 脱敏哨兵 | 🟡 | 🟡 | ✗ | 已完成 | — | auditledger/privacy.go + ErrLedgerSanitizeUnusable | — |
| OBS-041 | admin 操作审计日志(事务内同提交) | 🟡 | 🟡 | ✗ | 已完成 | — | db/admin/admin_audit.sql admin_audit_events; issuer.go 事务内写 | — |

## 1.3 admin_audit_events — 每个 action/target enum 一行 (sql 0047/0049)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-042 | action: issue_api_key / revoke_api_key / list_api_keys | 🟡 | 🟡 free-text | ✗ | 已完成 | — | admin_audit_events(0047) | — |
| OBS-043 | action: issue_admin_token / revoke_admin_token | ✗ | 🟡 | ✗ | 已完成 | — | (0047) | — |
| OBS-044 | action: admin_login | 🟡 | 🟡 | ✗ | 已完成 | — | (0047) | — |
| OBS-045 | action: create/disable/enable/delete_provider_account | ✗ | 🟡 | ✗ | 已完成 | — | (0047) | — |
| OBS-046 | action: create/rotate/disable/delete/list_account_credential(s) | ✗ | 🟡 | ✗ | 已完成 | — | (0047) | — |
| OBS-047 | action: credential_acquisition_started/completed/failed/cancelled | ✗ | ✗ | ✗ | 已完成 | — | (0047) | — |
| OBS-048 | action: update_billing_settings | ✗ | 🟡 | ✗ | 已完成 | — | (0047) | — |
| OBS-049 | action: create_pool_group / update_pool_group | ✗ | ✗ | ✗ | 已完成 | — | (0049) | — |
| OBS-050 | target_type: api_key/admin_token/tenant/user | ✗ | 🟡 | ✗ | 已完成 | — | (0047) | — |
| OBS-051 | target_type: provider_account/account_credential/billing_setting/pool_group | ✗ | 🟡 | ✗ | 已完成 | — | (0047/0049) | — |
| OBS-052 | API key 创建/撤销 lifecycle 事件 | ✓ | ✓ | ✗ | 已完成 | — | admin/issuer.go; admin/revoker.go; auditledger lifecycle; route /admin/v1/api-keys | — |
| OBS-053 | 认证事件(login/token refresh) | ✓ | ✓ | ✗ | 已完成 | — | userauth/service.go; hermes/audit.go; usersession/ | — |

## 1.4 oauth_refresh_audit_events.outcome — 每枚举一行 (sql 0055)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-054 | outcome: cache_hit | ✗ | ✗ | ✗ | 已完成 | — | (0055) | — |
| OBS-055 | outcome: refresh_lock_held | ✗ | ✗ | ✗ | 已完成 | — | (0055) | — |
| OBS-056 | outcome: refresh_succeeded | ✗ | ✗ | ✗ | 已完成 | — | (0055) | — |
| OBS-057 | outcome: refresh_token_rotated | ✗ | ✗ | ✗ | 已完成 | — | (0055) | — |
| OBS-058 | outcome: db_version_conflict | ✗ | ✗ | ✗ | 已完成 | — | (0055) | — |
| OBS-059 | outcome: invalid_grant_race_recovered | ✗ | ✗ | ✗ | 已完成 | — | (0055) | — |
| OBS-060 | outcome: storm_budget_exhausted | ✗ | ✗ | ✗ | 已完成 | — | (0055) | — |
| OBS-061 | outcome: cas_lost | ✗ | ✗ | ✗ | 已完成 | — | (0055) | — |
| OBS-062 | outcome: token_malformed | ✗ | ✗ | ✗ | 已完成 | — | (0055) | — |
| OBS-063 | outcome: oauth_401_force_refresh | ✗ | ✗ | ✗ | 已完成 | — | (0055) | — |
| OBS-064 | outcome: permanent_disable | ✗ | ✗ | ✗ | 已完成 | — | (0055) | — |
| OBS-065 | outcome: mimicry_applied | ✗ | ✗ | ✗ | 已完成 | — | (0055) | — |
| OBS-066 | outcome: auth_expired | ✗ | ✗ | ✗ | 已完成 | — | (0055) | — |
| OBS-067 | outcome: rate_limit_exceeded | ✗ | ✗ | ✗ | 已完成 | — | (0055) | — |
| OBS-068 | outcome: risk_control_triggered | ✗ | ✗ | ✗ | 已完成 | — | (0055) | — |
| OBS-069 | outcome: account_disabled | ✗ | ✗ | ✗ | 已完成 | — | (0055) | — |
| OBS-070 | obs AuditFilter.EventClass: pool_routing/rate_limit/oauth_refresh | 🟡 | ✗ | ✗ | 已完成 | — | obs/obs.go:42 | — |

## 1.5 pricing_ratio_audit_log / payment_audit_log / billing_events — money-path 审计 (sql 0062/0088/0092)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-071 | pricing_ratio_audit_log: actor_id/role + action(upsert/delete) | 🟡 | ✗ | ✗ | 已完成 | — | (0088) | — |
| OBS-072 | pricing_ratio: old_ratio/new_ratio(shape CHECK) | ✗ | ✗ | ✗ | 已完成 | — | (0088) | — |
| OBS-073 | pricing_ratio: prev_hash+entry_hash(len=32) hash-chain | ✗ | ✗ | ✗ | 已完成 | — | (0088) | — |
| OBS-074 | pricing_ratio: signature(len=64)+key_id ed25519 | ✗ | ✗ | ✗ | 已完成 | — | (0088) | — |
| OBS-075 | payment_audit_log(脱敏回调审计表) | ✓ | 🟡 | ✗ | 已完成 | — | (0062) ent payment_audit_log(order_id/action/detail/operator) | — |
| OBS-076 | payment_audit outcome: ACCEPTED/REJECTED/REPLAY_NOOP | 🟡 | ✗ | ✗ | 已完成 | — | (0062) | — |
| OBS-077 | payment_audit reason: COMPLETED/_REPLAY/_AMOUNT_MISMATCH/_PROVIDER_MISMATCH/_ORDER_NOT_FOUND/_ORDER_STATE_MISMATCH | ✗ | ✗ | ✗ | 已完成 | — | (0062) | — |
| OBS-078 | payment_audit: paid_amount vs expected_amount(防篡改) | ✗ | ✗ | ✗ | 已完成 | — | (0062) | — |
| OBS-079 | payment_audit_events.event_type: order_created/paid_confirmed/fulfillment_started/credited | 🟡 | 🟡 | ✗ | 已完成 | — | (0092) | — |
| OBS-080 | payment_audit_events: fulfillment_failed/idempotent_replay/order_expired/cancelled/refunded | ✗ | ✗ | ✗ | 已完成 | — | (0092) | — |
| OBS-081 | billing_events.event_type: claim_committed/aborted/reconciliation_appended | ✗ | ✗ | ✗ | 已完成 | — | (0092); obs ListBillingEvents filter | — |
| OBS-082 | billing_events: voucher_redeemed / balance_recharged / payment_credited / payment_refunded(claim_or_voucher XOR) | 🟡 | 🟡 | ✗ | 已完成 | — | (0092) | — |
| OBS-083 | 计费/topup/credit 事件 | ✓ | ✓ | ✗ | 已完成 | — | payment/audit.go; auditledger/; subscription/; billing/settler.go | — |

## 1.6 用户可验证签名成本收据 — 每字段/状态一行 (sql 0028/0032/0033/0052/0066/0084/0092/0096)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-084 | user_cost_receipts: request_id(UNIQUE) | ✓ | ✓ | ✓ | 已完成 | — | (0028) | — |
| OBS-085 | receipt_sequence(每请求多收据) | ✗ | ✗ | ✗ | 已完成 | — | (0033); CostReceipt.ReceiptSequence | — |
| OBS-086 | receipt: model/input_tokens/output_tokens/cached_tokens | ✓ | ✓ | 🟡 | 已完成 | — | (0028) | — |
| OBS-087 | cost_usd_micros(CHECK ≥0) | 🟡 float | 🟡 int | ✗ | 已完成 | — | (0028) | — |
| OBS-088 | rate_table_snapshot_id(价格出处) | 🟡 | ✗ | ✗ | 已完成 | — | (0028); PriceSnapshot.RateTableSnapshotID | — |
| OBS-089 | owner_source(成本收据归属) | ✗ | ✗ | ✗ | 已完成 | — | CostReceipt.OwnerSource; sql 0052 | — |
| OBS-090 | signer_fingerprint(bytea) | ✗ | ✗ | ✗ | 已完成 | — | (0028) | — |
| OBS-091 | signed_hash(bytea, ed25519) | ✗ | ✗ | ✗ | 已完成 | — | (0028) | — |
| OBS-092 | validation_state(6+1 值 CHECK): valid/provisional/mismatch_pending/mismatch_refunded/not_billable/receipt_unavailable/unknown | ✗ | ✗ | ✗ | 已完成 | — | (0032/0036); receipt_formatter.go:30 | — |
| OBS-093 | verdict(4 值): match/substitution_refund/mismatch_refund_pending/unknown | ✗ | ✗ | ✗ | 已完成 | — | (0032); receipt_formatter.go:39 | — |
| OBS-094 | adjustment_refs(jsonb array, append-only) | ✗ | ✗ | ✗ | 已完成 | — | (0032) | — |
| OBS-095 | receipt schema 版本 v1(legacy)+v2(current) | ✗ | ✗ | ✗ | 已完成 | — | ReceiptSchemaVersionV1/V2 | — |
| OBS-096 | 签名用 canonical 序列化 | ✗ | ✗ | ✗ | 已完成 | — | trustreceipt/canonical.go; ReceiptCanonicalPayloadV1 | — |
| OBS-097 | receipt_id = requestID:seq + display receipt_<hash> | ✗ | ✗ | ✗ | 已完成 | — | trustreceipt/payload.go:18; DisplayReceiptID | — |
| OBS-098 | TokenCounts{Input,Output,Cached} + PriceSnapshot{RateTableSnapshotID,SnapshotVersion,CurrencyCode} | 🟡 | 🟡 | ✗ | 已完成 | — | trustreceipt/payload.go:8,14 | — |
| OBS-099 | receipt append worker(Tx2 hook) + 追加存储(pgx, append-only) | ✗ | ✗ | ✗ | 已完成 | — | audit/receipt_worker.go; receipt_storage_pgx.go | — |
| OBS-100 | DetectReceiptMismatch(derived,submitted) | ✗ | ✗ | ✗ | 已完成 | — | audit/mismatch_detector.go | — |
| OBS-101 | MismatchDirection: over_charge/under_charge/equal | ✗ | ✗ | ✗ | 已完成 | — | mismatch_detector.go:19 | — |
| OBS-102 | RefundEligible(over_charge+delta>0+pending) | ✗ | ✗ | ✗ | 已完成 | — | mismatch_detector.go | — |
| OBS-103 | audit_refund_pending 表(幂等, 一 claim 一 refund) | 🟡 | 🟡 | ✗ | 已完成 | — | (0032) | — |
| OBS-104 | refund_worker(AuditMismatchRefundReason="audit_mismatch_v1") | ✗ | 🟡 | ✗ | 已完成 | — | audit/refund_worker.go | — |
| OBS-105 | cost_disputes 表 + status(open/reviewing/resolved/rejected) | ✗ | ✗ | ✗ | 已完成 | — | (0084); audit/dispute_store.go:27 | — |
| OBS-106 | dispute uq(tenant,user,request) 一请求一争议 + operator_note/resolved_at | ✗ | ✗ | ✗ | 已完成 | — | (0084) | — |
| OBS-107 | payment_refunds(amount_cents/idempotency_key UNIQUE/actor_kind) → billing_events payment_refunded | 🟡 | 🟡 | ✗ | 已完成 | — | (0092) | — |
| OBS-108 | payment_refund_requests(pending/approved/rejected 工作流) | ✗ | ✗ | ✗ | 已完成 | — | (0096) | — |

## 1.7 公开 verify 端点 + .well-known JWK + CRL + 离线 CLI — 每字段一行

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-109 | /v1/trust/verify 端点(匿名限流) | ✗ | ✗ | ✗ | 已完成 | — | trusthttp/verify_handler.go; routes.go:88; 151 F 段 /v1/trust/verify | — |
| OBS-110 | VerifyResponse.valid / status(signed-only/mismatch/unverified/missing) | ✗ | ✗ | ✗ | 已完成 | — | verify_handler.go:37-38 | — |
| OBS-111 | VerifyResponse.signature_valid / key_status | ✗ | ✗ | ✗ | 已完成 | — | verify_handler.go:39-40 | — |
| OBS-112 | VerifyResponse.reason(unknown_signer/invalid_signature/signature_outside_key_window/...) | ✗ | ✗ | ✗ | 已完成 | — | verify_handler.go:41 | — |
| OBS-113 | VerifyResponse.fields_mismatch[] / canonical_hash / schema_version | ✗ | ✗ | ✗ | 已完成 | — | verify_handler.go:42-44 | — |
| OBS-114 | verifyRequest{payload,signature,pubkey_fingerprint} + key-window 强制 | ✗ | ✗ | ✗ | 已完成 | — | verify_handler.go:47; verify_handler_keywindow_test.go | — |
| OBS-115 | /.well-known/huakai-pubkey.json(schema_version/generated_at/next_rotation_after) | ✗ 仅消费 OIDC | ✗ | ✗ | 已完成 | — | wellknown_handler.go:33-35; routes.go:87; 151 F 段 | — |
| OBS-116 | well-known keys[](JWK) + current + revoked[] | ✗ | ✗ | ✗ | 已完成 | — | wellknown:36-38 | — |
| OBS-117 | JWK.kty/crv/kid/x/alg/use/status + effective_from/to + revoked_at/reason_class | ✗ | ✗ | ✗ | 已完成 | — | wellknown:41-52 | — |
| OBS-118 | audit_signer_pubkeys 注册表(历史 key; sql 0035, 单活分区唯一索引) | ✗ | ✗ | ✗ | 已完成 | — | (0035); pubkey_registry.go | — |
| OBS-119 | pubkey fingerprint len=16 / public_key len=32 / algorithm='ed25519' CHECK | ✗ | ✗ | ✗ | 已完成 | — | (0035) | — |
| OBS-120 | CRL/撤销(env+file) | ✗ | ✗ | ✗ | 已完成 | — | trusthttp/revocation.go | — |
| OBS-121 | 离线 verify CLI(拉 well-known + 验签) | ✗ | ✗ | ✗ | 已完成 | — | cmd/huakai-verify/main.go | — |
| OBS-122 | /v1/audit/* 公开端点族(pubkey/pubkeys/verify/merkle-tree.json) | ✗ | ✗ | ✗ | 已完成 | — | 151 F 段 /v1/audit/pubkey{,/{fp}},/pubkeys,/verify,/merkle-tree.json; /admin/v1/audit-events | — |
| OBS-123 | /v1/receipts/{request_id}{,/verify,/disputes} + /v1/me/disputes + /v1/admin/disputes/{id}/resolve | ✗ | ✗ | ✗ | 已完成 | — | 151 F 段 receipts/disputes 路由 | — |

## 1.8 结构化日志 + request-id 传播

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-124 | 结构化分级 logger(privacy.LogSystem) | ✓ | 🟡 text | ✓ logrus | 已完成 | — | privacy/logger.go | — |
| OBS-125 | SystemEvent.Severity/Component/RequestID/ErrorClass | ✓ | 🟡 | ✓ | 已完成 | — | logger.go:22-25 | — |
| OBS-126 | Severity enum debug/info/warn/error/critical | 🟡 | 🟡 3级 | ✓ | 已完成 | — | logger.go:14-18 | — |
| OBS-127 | 每行 DefaultRedactor 脱敏 | ✓ | ✗ | 🟡 | 已完成 | — | logger.go:88 | — |
| OBS-128 | request_id ctx 传播 + X-Request-Id 头 | ✓ | ✓ | ✓ | 已完成 | — | proto RequestMeta.RequestID; meusagehttp/handler.go:63,168 | — |
| OBS-129 | 异步/缓冲日志写(非阻塞请求路径) | ✓ | 🟡 | ✗ | 已完成 | — | observability/billing_persister_handler.go; eventbus/bus.go Tier worker pools | — |
| OBS-130 | 上游 attempt ↔ 日志记录关联(AttemptSeq) | ✓ | 🟡 | 🟡 | 已完成 | — | gateway/forwarder.go AttemptSeq; eventbus AuditLedgerID+DLQRef | — |
| OBS-131 | Gin 风格 access-log formatter(request-id+latency) | ✗ | ✓ | ✓ gin_logger.go | 部分完成 | P3 | HUAKAI chi + LogSystem, 无 gin formatter | 可选 chi access-log formatter |

## 1.9 Prometheus / metrics — 每 metric 名一行 (otelbridge/expvarbridge.go)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-132 | /metrics scrape 端点(promhttp) | ✗ otel//indirect | ✗ client_golang//indirect 未用 | ✗ 无 prom | 部分完成 | P1 | otelbridge/provider.go(默认关 HUAKAI_METRICS_PROMETHEUS); 大树 F-OBS-19 | 默认开 + Grafana/Alertmanager dashboard |
| OBS-133 | metric: huakai_billing_resolver_db_fail_total | ✗ | ✗ | ✗ | 已完成 | — | expvarbridge.go:69 | — |
| OBS-134 | metric: huakai_billing_resolver_stale_total | ✗ | ✗ | ✗ | 已完成 | — | expvarbridge.go:74 | — |
| OBS-135 | metric: huakai_dispatch_mode_default_total | ✗ | ✗ | ✗ | 已完成 | — | expvarbridge.go:79 | — |
| OBS-136 | metric: huakai_dispatch_mode_shadow_total | ✗ | ✗ | ✗ | 已完成 | — | expvarbridge.go:84 | — |
| OBS-137 | metric: huakai_dispatch_mode_canary_total | ✗ | ✗ | ✗ | 已完成 | — | expvarbridge.go:89 | — |
| OBS-138 | metric: huakai_dispatch_mode_pasr_primary_total | ✗ | ✗ | ✗ | 已完成 | — | expvarbridge.go:94 | — |
| OBS-139 | metric: huakai_dispatch_mode_pasr_strict_total | ✗ | ✗ | ✗ | 已完成 | — | expvarbridge.go:99 | — |
| OBS-140 | metric: huakai_cache_creation_total | ✗ | ✗ | ✗ | 已完成 | — | expvarbridge.go:104 | — |
| OBS-141 | metric: huakai_cache_read_total | ✗ | ✗ | ✗ | 已完成 | — | expvarbridge.go:109 | — |
| OBS-142 | metric: huakai_group_policy_failopen_total | ✗ | ✗ | ✗ | 已完成 | — | expvarbridge.go:114 | — |
| OBS-143 | metric: huakai_group_policy_failclosed_total | ✗ | ✗ | ✗ | 已完成 | — | expvarbridge.go:119 | — |
| OBS-144 | expvar→OTel observable-counter bridge + WithoutCounterSuffixes exporter | ✓ | 🟡 | 🟡 | 已完成 | — | expvarbridge.go RegisterBridge; provider.go:30 | — |
| OBS-145 | clientid_request_count(每身份 expvar 计数) | ✗ | ✓ activeConnections | ✗ | 已完成 | — | clientid/metrics.go:36 IncrementRequestCount | — |
| OBS-146 | activeConnections gauge | ✗ | ✓ stats.go:11 | ✗ | 已完成 | — | otel observable | — |
| OBS-147 | 全局延迟直方图(P50/P95/P99) | ✓ ops_histograms | ✗ | ✗ | 缺失 | P1 | 大树 F-OBS-21; channelhealth 有 per-channel P99, 无全局请求级直方图 | 加请求级 latency histogram + P50/95/99 端点 |
| OBS-148 | token 吞吐(tokens/sec)实时 metric | ✓ | ✗ | ✗ | 部分完成 | P2 | cachemetrics; proto/stream_billing_state.go DeliveredTokenCount(事后可算, 无实时) | 暴露实时 tokens/sec |
| OBS-149 | OpenTelemetry / 分布式 tracing(span 导出 OTLP/Jaeger) | ✗ | ✗ | ✗ | 缺失 | P1 | 大树 F-OBS-35; otelbridge 仅 metrics(MeterProvider), 无 trace span | 接 OTel SDK + W3C traceparent + span 导出 |
| OBS-150 | 速率限制命中计数器 | ✓ | 🟡 | ✗ | 已完成 | — | quota/types.go AuditEvent(DecisionCode/RetryAfterSeconds); quota_audit_events 表 | — |

## 1.10 每模型 perf metrics (new-api perf_metric.go)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-151 | PerfMetric upsert 表(model+group+bucket_ts unique) | 🟡 | ✓ perf_metric.go:11 | ✗ | 部分完成 | P2 | observability/metrics_aggregator_handler.go | 建 per-model perf 桶表 |
| OBS-152 | perf.RequestCount / SuccessCount | ✓ | ✓ perf_metric.go:16-17 | ✗ | 部分完成 | P2 | metrics_aggregator Count()(无 SuccessCount) | 补 SuccessCount |
| OBS-153 | perf.TotalLatencyMs | ✓ | ✓ perf_metric.go:18 | ✗ | 缺失 | P2 | HUAKAI 无延迟聚合 | 聚合 TotalLatencyMs |
| OBS-154 | perf.TtftSumMs + TtftCount(TTFT 首字延迟) | ✓ first_token_ms | ✓ perf_metric.go:19-20 | 🟡 | 缺失 | P1 | HUAKAI 无 TTFT 聚合 | 采集并聚合 TTFT |
| OBS-155 | perf.GenerationMs(吞吐) | ✓ | ✓ perf_metric.go:22 | ✗ | 缺失 | P2 | — | 聚合 generation time |
| OBS-156 | GetPerfMetricsSummary / by-bucket API | ✓ | ✓ perf_metrics.go:14,38 | ✗ | 部分完成 | P2 | 内部聚合, 无对外 by-bucket API | 暴露 summary + by-bucket 端点 |

## 1.11 运维仪表盘 (sub2api 强项)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-157 | 健康分 0-100(business 70%+infra 30%) | ✓ ops_health_score.go:15 | ✗ | ✗ | 缺失 | P2 | 大树/细树 I; 151 §10 OPS-001 部分 | 移植 health-score 计算 |
| OBS-158 | business health = errorScore 50% + ttftScore 50% | ✓ ops_health_score.go:66 | ✗ | ✗ | 缺失 | P2 | — | 同上(依赖 TTFT OBS-154) |
| OBS-159 | infra health = storage+compute(DB fail=critical/Redis=degraded) | ✓ ops_health_score.go:71-90 | ✗ | ✗ | 缺失 | P2 | — | 同上 |
| OBS-160 | 实时流量仪表盘 | ✓ ops_realtime_traffic.go | 🟡 /status | ✗ | 缺失 | P2 | 151 §10 OPS-001 | 实时流量页 |
| OBS-161 | 趋势/窗口统计 | ✓ ops_trends.go/ops_window_stats.go | 🟡 | ✗ | 缺失 | P2 | — | 趋势页 |
| OBS-162 | 延迟直方图(bucketed)仪表 | ✓ ops_histograms.go | ✗ | ✗ | 缺失 | P2 | 关联 OBS-147 | 直方图页 |
| OBS-163 | 仪表盘总览聚合 | ✓ ops_dashboard.go | ✓ perf summary | ✗ | 部分完成 | P2 | metrics_aggregator 内部聚合 | 总览端点 |
| OBS-164 | WebSocket 实时 ops feed | ✓ ops_ws_handler.go | ✗ | ✗ | 缺失 | P3 | — | 可选 WS feed |
| OBS-165 | Snapshot v2 端点 | ✓ ops_snapshot_v2_handler.go | ✗ | ✗ | 缺失 | P3 | — | 可选 snapshot |
| OBS-166 | system logs / request drilldown 页 | ✓ | 🟡 | 🟡 | 部分完成 | P2 | 151 §10 OPS-003; HUAKAI 有 audit/usage 但 UI 不同 | request drilldown UI |

## 1.12 渠道状态监控

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-167 | provider 健康指标(success rate/latency per upstream) | ✓ | 🟡 | ✗ | 已完成 | — | channelhealth/service.go(FailedAttempts/LatencyP99MS/RateLimitHitRate); pool/router/metrics.go | — |
| OBS-168 | 每渠道 last-error/error-count(WindowSummary) | ✓ | 🟡 | ✗ | 已完成 | — | channelhealth/types.go:271-293(FailedAttempts/LastSignalClass/LastSignalAt/RampFailureCount) | — |
| OBS-169 | 渠道 disable/enable 事件记录(EventDisabled/Recovered/RampStarted) | 🟡 | ✓ | ✗ | 已完成 | — | channelhealth/types.go AuditEventType; service.go:112 emitTransitionEvents | — |
| OBS-170 | provider 延迟阈值告警(auto disable/cooldown/ramp) | 🟡 | ✓ | ✗ | 已完成 | — | channelhealth/types.go:119-164 Policy(LatencyP99/ErrorRate/RateLimitHitRate Threshold) | — |
| OBS-171 | 定时渠道健康监控(per-monitor ticker + pool) | ✓ channel_monitor_service.go | ✓ channel-test.go | ✗ | 部分完成 | P2 | obs/account_health_probe_handler.go(事件驱动, 非 cron) | 加 cron 定时探活 |
| OBS-172 | 关键字自动禁用渠道(AcSearch) | 🟡 | ✓ channel.go:63 AutomaticDisableKeywords | ✗ | 部分完成 | P3 | circuitbreaker pkg | 可选关键字禁用 |
| OBS-173 | 状态码区间自动禁用 | ✗ | ✓ status_code_ranges.go | ✗ | 部分完成 | P3 | circuitbreaker | 可选状态码禁用 |
| OBS-174 | 恢复自动重启用(half-open) | 🟡 | ✓ EnableChannel | ✗ | 已完成 | — | circuitbreaker half-open | — |
| OBS-175 | 渠道测试模板 CRUD | ✓ channel_monitor_template_service.go | ✗ | ✗ | 缺失 | P3 | — | 可选模板 CRUD |

## 1.13 告警 (sub2api OpsAlertRule/Event/Silence)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-176 | Alert rule CRUD | ✓ ops_alerts.go | ✗ | ✗ | 缺失 | P1 | 大树/细树 K; 151 §10 OPS-002 | 建 alert-rule CRUD |
| OBS-177 | rule.metric_type | ✓ ops_alert_models.go:24 | ✗ | ✗ | 缺失 | P1 | — | 同上 |
| OBS-178 | rule.operator(gt/lt) | ✓ :25 | ✗ | ✗ | 缺失 | P1 | — | 同上 |
| OBS-179 | rule.threshold | ✓ :26 | ✗ | ✗ | 缺失 | P1 | — | 同上 |
| OBS-180 | rule.window_minutes | ✓ :28 | ✗ | ✗ | 缺失 | P1 | — | 同上 |
| OBS-181 | rule.sustained_minutes(持续突破) | ✓ :29 | ✗ | ✗ | 缺失 | P1 | — | 同上 |
| OBS-182 | rule.cooldown_minutes | ✓ :30 | ✗ | ✗ | 缺失 | P1 | — | 同上 |
| OBS-183 | rule.severity | ✓ :22 | ✗ | ✗ | 缺失 | P1 | — | 同上 |
| OBS-184 | rule.notify_email | ✓ :32 | 🟡 webhook | ✗ | 部分完成 | P1 | HUAKAI notify pkg | 接 alert→notify |
| OBS-185 | rule.filters(维度 map) | ✓ :33 | ✗ | ✗ | 缺失 | P1 | — | 同上 |
| OBS-186 | rule.last_triggered_at | ✓ :36 | ✗ | ✗ | 缺失 | P1 | — | 同上 |
| OBS-187 | metric_type evaluator: cpu_usage_percent | ✓ :445 | 🟡 MonitorCPUThreshold | ✗ | 缺失 | P1 | — | evaluator |
| OBS-188 | metric_type evaluator: success_rate | ✓ :568 | ✗ | ✗ | 缺失 | P1 | — | evaluator |
| OBS-189 | metric_type evaluator: error_rate | ✓ :573 | ✗ | ✗ | 缺失 | P1 | — | evaluator |
| OBS-190 | OpsAlertEvent.status firing/resolved/manual_resolved | ✓ :11-13 | ✗ | ✗ | 缺失 | P1 | — | event lifecycle |
| OBS-191 | event.metric_value vs threshold_value + dimensions | ✓ :51-54 | ✗ | ✗ | 缺失 | P1 | — | 同上 |
| OBS-192 | event.fired_at/resolved_at/email_sent | ✓ :55-58 | ✗ | ✗ | 缺失 | P1 | — | 同上 |
| OBS-193 | evaluator redis leader-lock + cooldown | ✓ | ✗ | ✗ | 部分完成 | P1 | HUAKAI dlq lease(可复用) | evaluator leader-lock |
| OBS-194 | OpsAlertSilence: platform/group_id/region scope + until/reason/created_by | ✓ :67-74 | ✗ | ✗ | 缺失 | P2 | — | silence CRUD |
| OBS-195 | Webhook notify HMAC-sha256(X-Webhook-Signature) | 🟡 | ✓ webhook.go:28,76 | ✗ | 部分完成 | P1 | HUAKAI notify pkg | webhook 通知签名 |
| OBS-196 | Root-user notify / 邮件告警(滑窗限流) | ✗/✓ | ✓ NotifyRootUser | ✗ | 部分完成 | P2 | HUAKAI notify/email pkg | 接告警邮件 |
| OBS-197 | 配额/余额预警阈值 | 🟡 | ✓ QuotaWarningThreshold | ✗ | 已完成 | — | budget/budgetenforce | — |
| OBS-198 | 实时用量阈值 webhook / SSE 推送 | ✓ | ✓ | ✗ | 缺失 | P1 | 大树 F-OBS-30; 无订阅式阈值通知 | quota/cost 阈值实时 webhook/SSE |
| OBS-199 | risk control config/status/logs/unban 页 | ✓ | ✗ | ✗ | 部分完成 | P2 | 151 §10 OPS-004; HUAKAI mixedchannelrisk/tlsfpadmin 有后端 | 风控配置/解封 UI |

## 1.14 合成监控 / 定时测试 (sub2api)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-200 | 定时测试计划(5字段 cron parser) | ✓ scheduled_test_service.go:11 | 🟡 | ✗ | 缺失 | P2 | 大树/细树 L; 151 §10 | 建 cron synthetic monitor |
| OBS-201 | plan.cron_expression + computeNextRun(next_run_at) | ✓ :31-32 | ✗ | ✗ | 缺失 | P2 | — | 同上 |
| OBS-202 | plan.max_results(默认50, 历史上限) | ✓ :38 | ✗ | ✗ | 缺失 | P2 | — | 同上 |
| OBS-203 | plans by account(ListPlansByAccount) | ✓ :51 | ✗ | ✗ | 缺失 | P2 | — | 同上 |
| OBS-204 | 测试结果历史 + 保留 | ✓ scheduled_test_repo MaxResults | 🟡 内存 | ✗ | 缺失 | P2 | — | 同上 |

## 1.15 DLQ — 每列/event-kind/lane/status/retry-knob 一行 (sql 0015/0026)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-205 | usage_record_dlq.event_kind(text CHECK) | ✗ | ✗ | 🟡 | 已完成 | — | (0015) | — |
| OBS-206 | dlq.lane(HIGH/MED/LOW CHECK) | ✗ | ✗ | ✗ | 已完成 | — | (0015); dlq/types.go Lane | — |
| OBS-207 | dlq.status(6 值 CHECK) | ✗ | ✗ | ✗ | 已完成 | — | (0015); dlq/types.go Status | — |
| OBS-208 | dlq.next_retry_at | 🟡 | ✗ | ✗ | 已完成 | — | (0015) | — |
| OBS-209 | dlq.lease_ttl(默认30s) + lease_owner/lease_until | ✗ | ✗ | ✗ | 已完成 | — | (0015) | — |
| OBS-210 | dlq.replica_status(none/pending/delivered/failed) + replica_target(独立 PG DSN) + replica_committed_at | ✗ | ✗ | ✗ | 已完成 | — | (0015) | — |
| OBS-211 | dlq.idempotency_key(uq tenant+kind+key+target) | ✓ | ✗ | ✗ | 已完成 | — | (0015) | — |
| OBS-212 | dlq.source_table/source_id + operator_review_at | ✗ | ✗ | ✗ | 已完成 | — | (0015) | — |
| OBS-213 | outbox_events.attempt_count + failure_reason/dead_reason(脱敏) | 🟡 | ✗ | ✗ | 已完成 | — | (0026) | — |
| OBS-214 | EventKind: usage_record(LaneHigh) | 🟡 | ✗ | ✗ | 已完成 | — | dlq/types.go:13 EventKindUsageRecord | — |
| OBS-215 | EventKind: billing_event_replica / audit_event_replica(LaneHigh) | ✗ | ✗ | ✗ | 已完成 | — | dlq/types.go | — |
| OBS-216 | EventKind: audit_mismatch_refund(0032, LaneHigh) | ✗ | ✗ | ✗ | 已完成 | — | EventKindAuditMismatchRefund | — |
| OBS-217 | EventKind: audit_ledger_entry(0050, LaneHigh) | ✗ | ✗ | ✗ | 已完成 | — | EventKindAuditLedgerEntry | — |
| OBS-218 | EventKind: account_health(LaneMed) | 🟡 | ✗ | ✗ | 已完成 | — | EventKindAccountHealth | — |
| OBS-219 | EventKind: metrics(LaneLow, drain-on-shutdown) | 🟡 | ✗ | ✗ | 已完成 | — | EventKindMetrics | — |
| OBS-220 | EventKind: post_delivery_settlement(0053, LaneHigh) | ✗ | ✗ | ✗ | 已完成 | — | EventKindPostDeliverySettlement | — |
| OBS-221 | EventKind: cost_receipt_append(0066, LaneHigh) | ✗ | ✗ | ✗ | 已完成 | — | EventKindCostReceiptAppend | — |
| OBS-222 | status: pending/inflight/delivered/operator_review/dlq/quarantined | 🟡 | ✗ | ✗ | 已完成 | — | dlq/types.go:43 | — |
| OBS-223 | RetryPolicy.BaseBackoff(1s)/CapBackoff(5m)/MaxAttempts(10)/DLQAfter(15m)/NextFailure 指数 | 🟡 | ✗ | 🟡 | 已完成 | — | dlq/retry.go:21-24,44 | — |
| OBS-224 | operator replay 端点(Replay(id,actor)) | ✗ | ✗ | ✗ | 已完成 | — | dlq/service.go; obs.ReplayDLQ; 路由 /admin/v1/dlq/{handler},{id}/replay,/usage-record-dlq/{id}/replay | — |
| OBS-225 | per-lane workers + Postgres+memory DLQ store | ✗ | ✗ | ✗ | 已完成 | — | dlq/worker.go; dlq/store.go; obs/dlq/store_postgres.go | — |
| OBS-226 | outbox_events 通用(0026, priority default/high/critical) + dlq_events 持久行 | ✓ scheduler_outbox | ✗ | ✗ | 已完成 | — | obs/dlq/outbox.go; (0026) | — |
| OBS-227 | Refund DLQ worker(EventTypeAuditRefund) | ✗ | 🟡 | ✗ | 已完成 | — | obs/dlq/refund_worker.go | — |
| OBS-228 | scheduler outbox lag/backlog 重建阈值 | ✓ scheduler_outbox.go SchedulerOutboxEventFullRebuild | ✗ | ✗ | 部分完成 | P3 | HUAKAI DLQ depth, 无 lag-rebuild | 可选 lag-rebuild 阈值 |

## 1.16 结算恢复 / 对账 — 三证防双扣

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-229 | 投递后结算恢复(durable intent) | ✗ | ✗ | ✗ | 已完成 | — | settlementrecovery/handler.go(经 DLQ 重放 billing.Settler.Settle) | — |
| OBS-230 | 三证 CommittedProof.IsCommitted | ✗ | ✗ | ✗ | 已完成 | — | settlementrecovery/proof.go | — |
| OBS-231 | proof1 billing_ledger_claims.status='committed' | ✗ | ✗ | ✗ | 已完成 | — | proof.go; postgres_proof.go | — |
| OBS-232 | proof2 usage_records 同 tenant+claim | ✗ | ✗ | ✗ | 已完成 | — | proof.go | — |
| OBS-233 | proof3 billing_events claim_committed | ✗ | ✗ | ✗ | 已完成 | — | proof.go | — |
| OBS-234 | enqueue recovery intent | ✗ | ✗ | ✗ | 已完成 | — | settlementrecovery/enqueue.go | — |
| OBS-235 | DualRunReconciler(legacy vs async 对比) | 🟡 | ✗ | 🟡 | 已完成 | — | observability/reconciliation_handler.go:43 | — |
| OBS-236 | pending-reconciliation 用量查询 + reconciliation_appended 事件 | ✓ | ✗ | ✗ | 已完成 | — | obs UsageFilter.PendingReconciliationOnly; billing_events reconciliation_appended(0092) | — |

## 1.17 事件总线 / outbox — money-path audit-ref fail-closed

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-237 | 进程内事件总线(Register/handlers/drop hook/clock) | 🟡 | ✗ | ✗ | 已完成 | — | eventbus/bus.go | — |
| OBS-238 | RequestCompletionEvent typed 事件 + Handler Tier | 🟡 | ✗ | ✗ | 已完成 | — | eventbus/types.go:30,58 | — |
| OBS-239 | Handler.Critical()(critical-fail 中止) | ✗ | ✗ | ✗ | 已完成 | — | types.go:83; bus.go:102 ErrCriticalHandler | — |
| OBS-240 | Handler.DLQKind()(per-handler 路由) + Event→DLQ sink | ✗ | ✗ | ✗ | 已完成 | — | types.go:85; bus.go:262; WithDLQ | — |
| OBS-241 | ValidateMoneyPathAuditRef(prod fail-closed) + AuditRefPolicy | ✗ | ✗ | ✗ | 已完成 | — | eventbus/audit_ref.go:13,20 ErrAuditRefMissing | — |

## 1.18 错误分类 — 每 clienterr 码一行 (clienterr/catalog.go)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-242 | 34 个 typed 错误码(固定消息, 无内部泄漏)含 registry_unknown_error/reserve_error/insufficient_balance/no_capacity/claim_race/audit_ledger_error/audit_ref_missing/content_policy_violation 等 | 🟡 | 🟡 | 🟡 | 已完成 | — | clienterr/catalog.go:41-74 | — |
| OBS-243 | header-only abort/forward/settle("internal settlement failed") | ✗ | ✗ | ✗ | 已完成 | — | catalog.go:72-74 | — |
| OBS-244 | normalizeOpsErrorType / 错误类映射(无原始 err 文本) | ✓ | 🟡 | ✗ | 已完成 | — | privacy.ErrorClassFor | — |
| OBS-245 | classifyOpsPhase / isBusinessLimited 分类 | ✓ ops_error_logger.go | ✗ | ✗ | 部分完成 | P3 | clienterr event_class | 可选 ops phase 分类 |
| OBS-246 | 异步批量错误日志(worker pool + drop counter) | ✓ | ✗ | ✗ | 部分完成 | P3 | dlq async | 可选异步批量 ops error logger |

## 1.19 杂项可靠性 + 日志保留

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OBS-247 | 日志保留策略 / TTL / purge | ✓ usage_cleanup_service | ✓ DeletePerfMetricsBefore+rotation | ✓ log_dir_cleaner size-cap GC | 部分完成 | P2 | 大树 F-OBS-05; HUAKAI usagecleanup + dlq retention(usage_records/quota_audit/channel_health 无自动 purge) | 补 usage/audit 表 TTL purge job |
| OBS-248 | log-dir size-capped GC(maxTotalSizeMB+protectedPath) | ✓ | 🟡 | ✓ log_dir_cleaner.go:74 | 缺失 | P3 | HUAKAI 无文件日志 GC(DB 为主) | n/a(无文件日志); 监控 |
| OBS-249 | 幂等日志记录(dedup on retry, Tx2) | ✓ | ✗ | ✗ | 已完成 | — | billing/settler.go Tx2; receipt_formatter 确定性 hash | — |
| OBS-250 | health/liveness 端点 | ✓ /health | ✓ /status,/uptime | ✓ /healthz GET+HEAD | 部分完成 | P2 | 151; HUAKAI trust/obs scope 内无顶层 /healthz | 加顶层 /healthz | 
| OBS-251 | singleton leader election | ✓ redis lock | ✗ | ✗ | 已完成 | — | dlq claim-lease; auditledger advisory lock | — |
| OBS-252 | mid-stream SSE token 计数 | ✓ | 🟡 | 🟡 | 已完成 | — | proto/anthropic_messages_stream.go; openai/sse.go; obs/repository.go:68 DeliveredTokenCount | — |

---

# 第二节 · 安全 / 隐私 / 反封禁 / 网络策略 / 内容审计 (SEC-xxx)

## 2.1 隐私 / 日志脱敏 — DATA-CLASS 标签 (每标签一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-001 | data-class 标签 NEVER_PERSIST | ✗ | ✗ | ✗ | 已完成(强于全部参照) | — | privacy/types.go NEVER_PERSIST | — |
| SEC-002 | data-class 标签 SECRET_MATERIAL | ✗ | ✗ | ✗ | 已完成 | — | privacy/types.go | — |
| SEC-003 | data-class 标签 SENSITIVE_PII | ✗ | ✗ | ✗ | 已完成 | — | privacy/types.go | — |
| SEC-004 | data-class 标签 SAFE_METADATA | ✗ | ✗ | ✗ | 已完成 | — | privacy/types.go | — |
| SEC-005 | data-class 标签 OPT_IN_PROOF | ✗ | ✗ | ✗ | 已完成 | — | privacy/types.go | — |
| SEC-006 | redaction-result enum(clean/redacted/blocked) | ✗ | ✗ | ✗ | 已完成 | — | privacy/types.go RedactionResultClean/Redacted/Blocked | — |
| SEC-007 | 日志条目 schema-version 戳(privacy.log.v1) | ✗ | ✗ | ✗ | 已完成 | — | privacy/types.go SchemaVersion | — |

## 2.2 隐私脱敏 — 敏感 KEY 标记 (每标记一行, default_redactor.go sensitiveKey)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-008 | block key: access_token / refresh_token / id_token | 🟡 cred-key set | 🟡 ad-hoc | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-009 | block key: bearer / authorization / cookie | 🟡 | ✗ | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-010 | block key: password / secret | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-011 | block key: api_key / apikey / credential_bytes / credentials | 🟡 | 🟡 | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-012 | block key: prompt / completion | 🟡 excerpt | ✗ | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-013 | block key: raw_body / raw_request / raw_response / upstream_body | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-014 | block key: tool_input / tool_output / tool_result | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-015 | block key: html_body / message_content | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-016 | block 裸 body/content/message/details/evidence + _content 后缀 | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-017 | block 裸 token + _token 后缀 | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-018 | 安全子串豁免(credential_id/fingerprint/token_count/source_ip_hash 不 block) | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go sensitiveKey safe-list loop | — |

## 2.3 隐私脱敏 — 禁止 STRING 扫描标记 (每标记一行, containsForbiddenString)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-019 | forbidden literal: sk-(OpenAI key 前缀) | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-020 | forbidden literal: toolu_(Anthropic tool-use id) | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-021 | forbidden literal: aiv_ / gho_ / ant- 前缀 | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-022 | forbidden literal: "bearer " / "authorization:" | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-023 | forbidden literal: access_token/refresh_token/id_token 子串 | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-024 | forbidden literal: cookie= / cookie: | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-025 | forbidden literal: credential 子串 | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-026 | forbidden literal: raw user prompt / prompt_sentinel / completion_sentinel | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-027 | forbidden literal: password / secret= | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go | — |
| SEC-028 | 错误消息净化(err→class, 无原始文本; timeout/rate_limit/forbidden/credential_error...) | 🟡 | ✗ | ✗ | 已完成 | — | default_redactor.go SanitizeError | — |

## 2.4 隐私 — allowlist 模型 & 结构防护 (每防护一行)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-029 | 正向 deny-by-default 精确字段 allowlist(~130 字段) | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go exactAllowlist; redact/allowlist.go systemLogSafeFields | — |
| SEC-030 | allowlist 后缀规则(_id/_tokens/_micro_usd/_ms/_state/_at...) | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go allowlistField suffix loop | — |
| SEC-031 | boot/CI 字段漂移快照 | ✗ | ✗ | ✗ | 已完成 | — | redact/allowlist.go SystemLogSafeFieldsSnapshot | — |
| SEC-032 | 严格单 JSON 值解码(拒尾随 token 防走私) | ✗ | ✗ | ✗ | 已完成 | — | privacy/strict_decode.go StrictDecodeJSON | — |
| SEC-033 | max-string cap→sha256-ref+截断(默认256B) | 🟡 excerpt cap | ✗ | ✗ | 已完成 | — | default_redactor.go sanitizeString defaultMaxString | — |
| SEC-034 | 可选 panic-on-long-string(fail-loud 测试) | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go WithPanicOnLongString | — |
| SEC-035 | 嵌套 map/array 递归脱敏 + redaction_result=blocked 标记 | ✗ | ✗ | ✗ | 已完成 | — | default_redactor.go sanitizeValue | — |
| SEC-036 | 请求体常量时间清零(subtle.ConstantTimeCopy) | ✗ | ✗ | ✗ | 已完成 | — | privacy/zeroize.go Zeroize; middleware.go zeroizingReadCloser | — |
| SEC-037 | panic recoverer 把 stack 脱敏入系统日志(无 body, panic_class) | ✗ | 🟡 gin 裸日志 | ✗ | 已完成 | — | privacy/middleware.go Recoverer | — |
| SEC-038 | 请求元数据抽取(model/msg-count/max-tokens, 无内容) | ✗ | ✗ | ✗ | 已完成 | — | privacy/middleware.go parseRequestMetadata | — |
| SEC-039 | payload-summary = bytes+sha256-prefix+脱敏 snippet(160B/4096B cap) | 🟡 | ✗ | ✗ | 已完成 | — | redact/payload_summary.go | — |
| SEC-040 | opt-in content-binding HMAC proof(默认 OFF) | ✗ | ✗ | ✗ | 已完成 | — | privacy/proof.go OptInContentProof; ContentBindingDefaultEnabled=false | — |

## 2.5 隐私 — AUDIENCE 分级脱敏 (每受众层一行, redact/audience.go)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-041 | public 字段集(request_id/model/token_count_total/status/signature/merkle_root/pubkey_fp) | ✗ | ✗ | ✗ | 已完成 | — | audience.go publicAudienceFields | — |
| SEC-042 | tenant_operator 字段集(+tenant_id/route_id/pool_id/cache_hit_ratio/latencies/error_class/hop_chain) | ✗ | ✗ | ✗ | 已完成 | — | audience.go tenantOperatorAudienceFields | — |
| SEC-043 | platform_admin 字段集(+account_id_hash/upstream_model_reported/error_code/provider/ingress_path) | ✗ | ✗ | ✗ | 已完成 | — | audience.go platformAdminAudienceFields | — |
| SEC-044 | internal(SRE)层(不裁剪, 仍 drop forbidden) | ✗ | ✗ | ✗ | 已完成 | — | audience.go internalAudienceFields; RedactForAudience | — |
| SEC-045 | 跨全部受众 forbidden 字段硬拒(prompt/completion/messages/content/tool_*/system/thinking/reasoning_summary/user_email/user_name/api_key/authorization/token/password/secret/request_body/response_body) | ✗ | ✗ | ✗ | 已完成 | — | audience.go forbiddenAudienceFields | — |
| SEC-046 | tenant hop-chain 子脱敏(仅留 hop/name/ts) + dropped-fields 诊断 | ✗ | ✗ | ✗ | 已完成 | — | audience.go redactTenantHopChain; DroppedFieldsForAudience | — |

## 2.6 凭证响应脱敏

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-047 | API 响应敏感 cred-key 拒绝列表 | ✓ account_credentials_redact.go SensitiveCredentialKeys | 🟡 ad-hoc | ✗ | 已完成 | — | default_redactor.go sensitiveKey markers | — |
| SEC-048 | merge-preserving PUT(缺失敏感键保留旧值) | ✓ MergePreservingSensitiveCreds | ✗ | ✗ | 部分完成 | P3 | credentialstore 服务端保密(postgres_store.go) | 评估 merge-preserve PUT 语义 |

## 2.7 密钥静态加密 — AAD 字段 (每 AAD 绑定一行, credentialstore/crypto.go)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-049 | AES-256-GCM(全部上游凭证) | ✅ 仅 TOTP | ✗ channel key 明文 | ✗ OAuth token 明文 | 已完成(强于全部参照) | — | crypto.go EncryptionSchemeAES256GCM | — |
| SEC-050 | AAD bind tenant_id | ✗ | ✗ | ✗ | 已完成 | — | crypto.go AAD.TenantID | — |
| SEC-051 | AAD bind provider_account_id | ✗ | ✗ | ✗ | 已完成 | — | crypto.go AAD.ProviderAccountID | — |
| SEC-052 | AAD bind vendor(normalized) | ✗ | ✗ | ✗ | 已完成 | — | crypto.go AAD.Vendor | — |
| SEC-053 | AAD bind auth_mode(normalized) | ✗ | ✗ | ✗ | 已完成 | — | crypto.go AAD.AuthMode | — |
| SEC-054 | AAD bind version | ✗ | ✗ | ✗ | 已完成 | — | crypto.go AAD.Version | — |
| SEC-055 | AAD bind key_id(自动取自 current key) | ✗ | ✗ | ✗ | 已完成 | — | crypto.go AAD.KeyID | — |
| SEC-056 | AAD-hash 解密失配拒绝(hmac.Equal) | ✗ | ✗ | ✗ | 已完成 | — | crypto.go Decrypt AADHash check | — |
| SEC-057 | 每记录随机 nonce(crypto/rand) | 🟡 TOTP | ✗ | ✗ | 已完成 | — | crypto.go Encrypt | — |
| SEC-058 | scheme-mismatch 解密拒绝 | ✗ | ✗ | ✗ | 已完成 | — | crypto.go Decrypt scheme guard | — |
| SEC-059 | key 材料用后清零(defer Zeroize) | ✗ | ✗ | ✗ | 已完成 | — | crypto.go defer privacy.Zeroize | — |
| SEC-060 | KeyProvider 抽象(CurrentKey + Key-by-id, 轮换接口) | ✗ 单静态 key | ✗ | ✗ | 已完成 | — | crypto.go KeyProvider | — |
| SEC-061 | static-key 32字节校验 fail-loud + 多编码 decode(base64/raw/hex) | 🟡 | ✗ | ✗ | 已完成 | — | crypto.go NewStaticKeyProvider; DecodeKeyMaterial | — |
| SEC-062 | HMAC 凭证指纹(不可逆日志标识) | ✗ | ✗ | ✗ | 已完成 | — | crypto.go HMACFingerprint | — |
| SEC-063 | forward-proxy auth_secret 加密信封(独立 AAD vendor=huakai_forward_proxy) | ✗ 明文 | ✗ 明文含 creds | ✗ 明文 config | 已完成 | — | proxysecret/secret.go Encode/Decode; envelope huakai-proxy-secret-v1: | — |
| SEC-064 | 对称密钥自动轮换计划(graceful phaseout) | ✗ | ✗ | ✗ | 部分完成 | P2 | 大树 E-11; KeyProvider 支持版本, AES key 仍手动 env 替换 | 加定时轮换计划(SOC2/ISO27001) |

## 2.8 SSRF 出站防护 — 每条规则一行 (HUAKAI 主缺口)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-065 | SSRF: block loopback 127.0.0.0/8 + ::1/128 | ✓ channel_monitor_ssrf.go | ✓ common/ssrf_protection.go | ✗ | 已完成(passthrough 路径) | — | provider/passthrough_endpoint_guard.go(19 special-use CIDR); 默认出站 transport/factory.go 仅 Proxy=nil | — |
| SEC-066 | SSRF: block RFC1918 10/8,172.16/12,192.168/16 | ✓ | ✓ | ✗ | 部分完成 | P1 | passthrough_endpoint_guard.go 覆盖; 通用默认出站无 private-IP block | 把 passthrough SSRF 守卫推广到所有 admin-supplied URL 路径 |
| SEC-067 | SSRF: block link-local 169.254.0.0/16(含 metadata 169.254.169.254) | ✓ | ✓ | ✗ | 部分完成 | P1 | passthrough guard 含 metadata; 默认出站无 | 同上 |
| SEC-068 | SSRF: block CGNAT 100.64.0.0/10 | ✓ | ✓ | ✗ | 部分完成 | P1 | passthrough guard 部分 | 补全 CGNAT |
| SEC-069 | SSRF: block "this network" 0.0.0.0/8 + unspecified | ✓ | ✓ | ✗ | 部分完成 | P1 | — | 补全 |
| SEC-070 | SSRF: block IPv6 ULA fc00::/7 | ✓ | ✓ | ✗ | 部分完成 | P1 | passthrough guard | 补全 v6 |
| SEC-071 | SSRF: block IPv6 link-local fe80::/10 | ✓ | ✓ | ✗ | 部分完成 | P1 | — | 补全 |
| SEC-072 | SSRF: block IPv6 unspecified ::/128 | ✓ | ✓ | ✗ | 部分完成 | P1 | — | 补全 |
| SEC-073 | SSRF: block multicast 224.0.0.0/4 + ff00::/8 | 🟡 | ✓ | ✗ | 缺失 | P1 | new-api ssrf_protection.go 全; HUAKAI 默认出站无 | 补 multicast |
| SEC-074 | SSRF: block reserved 240.0.0.0/4 + 255.255.255.255/32 | ✗ | ✓ | ✗ | 缺失 | P2 | new-api 全 IANA | 补 reserved |
| SEC-075 | SSRF: block IETF/test nets(192.0.0.0/24, TEST-NET-1/2/3, 198.18/15) | ✗ | ✓ | ✗ | 缺失 | P2 | new-api 全 IANA registry | 补 IETF/test |
| SEC-076 | SSRF: block IPv4-mapped/translation(::ffff:0:0/96, 64:ff9b::/96, 100::/64, 2001::/23, 2001:db8::/32) | ✗ | ✓ | ✗ | 缺失 | P2 | new-api privateIPv6Nets | 补 v6 mapped/translation |
| SEC-077 | SSRF: cloud-metadata 主机名拒绝(metadata.google.internal/metadata.goog/instance-data/instance-data.ec2.internal) | ✓ monitorBlockedHostnames | 🟡 仅 IP | ✗ | 部分完成 | P1 | passthrough guard 含 metadata.google.internal/.local/.internal/.lan | 通用化主机名拒绝列表 |
| SEC-078 | SSRF: localhost/localhost.localdomain 主机名拒绝 | ✓ | 🟡 | ✗ | 部分完成 | P2 | passthrough guard | 同上 |
| SEC-079 | SSRF: DNS-rebinding dial 层 real-IP 复核 | ✓ safeDialContext | 🟡 validate-time opt-in | ✗ | 部分完成 | P1 | passthrough_endpoint_guard publicPassthroughNetAddr(dial 复核); 默认出站无 | 通用 dial-time 复核 |
| SEC-080 | SSRF: scheme 限制(http/https only) | 🟡 | ✓ | 🟡 | 部分完成 | P2 | passthrough guard non-HTTPS 拒 | 通用 scheme guard |
| SEC-081 | SSRF: 端口 allowlist + range parse("8000-9000") | ✗ | ✓ parsePortRanges | ✗ | 缺失 | P2 | new-api isAllowedPort | 补端口 allowlist |
| SEC-082 | SSRF: domain allow/deny + wildcard *.example.com | ✗ | ✓ isDomainAllowed | ✗ | 缺失 | P2 | new-api | 补 domain 列表 |
| SEC-083 | SSRF: domain/ip filter MODE toggle(whitelist vs blacklist) | ✗ | ✓ DomainFilterMode/IpFilterMode | ✗ | 缺失 | P3 | new-api | 可选 mode 切换 |
| SEC-084 | SSRF: AllowPrivateIp opt-out 逃生门 + master enable/disable toggle | ✗ | ✓ ValidateURLWithFetchSetting | ✗ | 缺失 | P3 | new-api | 可选 toggle |
| SEC-085 | SSRF 出站(OAuth token 交换)防护 | 🟡 | 🟡 | ✗ | 已完成 | — | auth/antigravity_token_provider.go:362-457 NewSSRFProtectedOAuthClient(私网/loopback/link-local + DNS rebind) | — |
| SEC-086 | env-proxy 劫持隔离(strip HTTP(S)_PROXY, Proxy=nil) | 🟡 | ✗ http.ProxyFromEnvironment | ✓ NewDirectTransport | 已完成 | — | transport/factory.go standardRoundTripper cloned.Proxy=nil | — |

## 2.9 Header firewall — 每条内建 deny 一行 (headerfirewall/firewall.go)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-087 | deny Set-Cookie / Set-Cookie2 | ✓/🟡 | ✗ | ✗ | 已完成 | — | firewall.go exactRule | — |
| SEC-088 | deny Authorization | 🟡 | ✗ | ✗ | 已完成 | — | firewall.go exactRule | — |
| SEC-089 | deny Proxy-Authenticate / Proxy-Authorization | 🟡 | ✗ | ✗ | 已完成 | — | firewall.go exactRule | — |
| SEC-090 | deny WWW-Authenticate(sub2api 实际放行!) | ✗(在 defaultAllowed) | ✗ | ✗ | 已完成 | — | firewall.go exactRule | — |
| SEC-091 | deny X-Real-IP / X-Forwarded-For/Host/Proto/Port | 🟡 | ✗ | ✗ | 已完成 | — | firewall.go exactRule | — |
| SEC-092 | deny X-Cloud-Trace-Context / Server | 🟡/✓ | ✗ | ✗ | 已完成 | — | firewall.go exactRule | — |
| SEC-093 | prefix-deny CF-(Cloudflare) | 🟡 | ✗ | ✗ | 已完成 | — | firewall.go prefixRule | — |
| SEC-094 | prefix-deny X-Amz- / X-Amzn-(AWS) | 🟡 | ✗ | ✗ | 已完成 | — | firewall.go prefixRule | — |
| SEC-095 | admin 动态 extra-deny(KeyResponseHeaderDenyExtra) | ✓ ForceRemove | ✗ | ✗ | 已完成 | — | firewall.go ExtraDeny | — |
| SEC-096 | admin 动态 allow-override(KeyResponseHeaderAllowOverride) | ✓ AdditionalAllowed | ✗ | ✗ | 已完成 | — | firewall.go AllowOverride | — |
| SEC-097 | 动态规则前缀检测(尾 - = prefix rule) | ✗ | ✗ | ✗ | 已完成 | — | firewall.go dynamicRule | — |
| SEC-098 | hop-by-hop strip(content-length/transfer-encoding/connection) | ✓ | 🟡 | ✗ | 部分完成 | P3 | Go http lib 处理, 无显式列表 | 可选显式 hop-by-hop strip |
| SEC-099 | 正向 default-allow allowlist 模型 | ✓ defaultAllowed(~20) | ✗ | ✗ | 部分完成 | P3 | HUAKAI 用 deny-list 模型 | 评估正向 allowlist 模型 |
| SEC-100 | 每渠道出站 request-header override | 🟡 | ✓ GetHeaderOverride JSON | 🟡 | 部分完成 | P2 | mimicry HTTPLayer.HeaderOrder | per-channel header override JSON |

## 2.10 H2 桥接反请求走私 + panic 恢复 (传输/协议安全)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-101 | H2 桥重复 Content-Length 拒绝(反走私) | ✗ | ✗ | ✗ | 已完成 | — | rust-core-gateway/tls-sidecar/h2_bridge.rs:161-166 | — |
| SEC-102 | H2 桥重复 Host 头拒绝 | ✗ | ✗ | ✗ | 已完成 | — | h2_bridge.rs:558 | — |
| SEC-103 | panic 恢复无 stack trace 入响应(client 仅 generic 500) | ✓ | 🟡 | ✓ | 已完成 | — | privacy/middleware.go Recoverer; cmd/gateway/middleware.go:72 | — |
| SEC-104 | 非 panic 错误体信息泄漏审计(validation/quota/auth 路径) | 🟡 | 🟡 | ✓ | 部分完成 | P2 | 大树 J-04; panic 路径安全, 非 panic 路径未统一审计 | 统一审计非 panic 错误体无内部泄漏 |
| SEC-105 | HSTS(X-Forwarded-Proto: https 才发) | ✓ | ✗ | ✗ | 已完成 | — | cors_security_headers_test.go:50-56 | — |
| SEC-106 | CORS exact-origin allowlist(无通配) + preflight 403 | ✓ | ✗ | ✗ | 已完成 | — | cors_security_headers parseAllowedOrigins; corsMiddleware 403 | — |
| SEC-107 | X-Content-Type-Options:nosniff / X-Frame-Options:DENY / Referrer-Policy:no-referrer / CSP default-src 'none' / Vary:Origin | ✓ | ✗ | ✗ | 已完成 | — | cors_security_headers_test.go:29-32,100 | — |

## 2.11 请求体审计 opt-in — 每开关一行

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-108 | per-tenant enable toggle(默认 OFF) | ✓ ContentModerationConfig.Enabled | 🟡 全局 CheckSensitiveEnabled | ✗ | 已完成 | — | moderation/types.go ModerationConfig.Enabled DefaultConfig=false | — |
| SEC-109 | group/tenant scoping | ✓ AllGroups/GroupIDs | ✗ | ✗ | 已完成 | — | per-tenant(TenantID keyed) | — |
| SEC-110 | sample-rate knob(hash-sample) | ✓ SampleRate/shouldSample | ✗ | ✗ | 已完成 | — | types.go SampleRatePct; sampler.go ShouldSample(FNV) | — |
| SEC-111 | fail-closed vs fail-open(可配) | 🟡 硬 fail-open | ✗ | ✗ | 已完成 | — | types.go FailClosed(DefaultConfig=true); screener.go backendResult | — |
| SEC-112 | body size cap→413 | ✓ | 🟡 | ✗ | 已完成 | — | privacy/middleware.go maxBodyBytes(默认8MB)→413 | — |
| SEC-113 | 审计日志 hash+match-meta only, 无 raw body | 🟡 excerpt+scores | ✗ | ✗ | 已完成 | — | moderation/sql_store.go(payload_hash+decision+matched IDs) | — |
| SEC-114 | pre-dispatch(pre-block) vs observe 放置 | ✓ Mode pre_block/observe/off | 🟡 prompt-only | ✗ | 部分完成 | P3 | 同步 screen + sampler 审计 | 可选 observe/pre_block 显式模式 |

## 2.12 输入校验 / 请求过滤

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-115 | 请求体大小限制(8MB gateway-wide) + 清零 | ✓ | 🟡 | ✗ | 已完成 | — | privacy/middleware.go:42-53 io.LimitReader(8<<20+1)+zeroize | — |
| SEC-116 | per-endpoint body size 限制(1MB chat) | ✓ | 🟡 | ✗ | 已完成 | — | chat_completions_validate.go:78 MaxBytesReader(1<<20) | — |
| SEC-117 | 废弃/移除字段拒绝(rejectRemovedBodyFields) | ✓ | ✗ | ✗ | 已完成 | — | chat_completions_validate.go:87-96 | — |
| SEC-118 | JSON 解析/畸形体拒绝 | 🟡 | 🟡 | 🟡 | 已完成 | — | chat_completions_validate.go:52 | — |
| SEC-119 | client protocol/ingress path 校验(404 未注册) | 🟡 | 🟡 | 🟡 | 已完成 | — | chat_completions_validate.go:99-118 ClientProtocolByIngressPath | — |

## 2.13 TLS 指纹 admin profile — 每字段一行 (tlsfpadmin/types.go)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-120 | profile.cipher_suites | ✓ | ✗ | ✓ 固定 | 已完成 | — | tlsfpadmin/types.go CipherSuites []int32 | — |
| SEC-121 | profile.supported_curves | ✓ | ✗ | ✗ | 已完成 | — | types.go SupportedCurves | — |
| SEC-122 | profile.ec_point_formats | 🟡 | ✗ | ✗ | 已完成 | — | types.go EcPointFormats | — |
| SEC-123 | profile.signature_algorithms | ✓ | ✗ | ✗ | 已完成 | — | types.go SignatureAlgorithms | — |
| SEC-124 | profile.alpn_protocols | ✓ | ✗ | ✗ | 已完成 | — | types.go AlpnProtocols | — |
| SEC-125 | profile.tls_supported_versions | ✓ | ✗ | ✗ | 已完成 | — | types.go TLSSupportedVersions | — |
| SEC-126 | profile.key_share_groups | ✓ | ✗ | ✗ | 已完成 | — | types.go KeyShareGroups | — |
| SEC-127 | profile.psk_modes | ✓ | ✗ | ✗ | 已完成 | — | types.go PskModes | — |
| SEC-128 | profile.extensions_order(顺序关键) | ✓ | ✗ | ✗ | 已完成 | — | types.go ExtensionsOrder | — |
| SEC-129 | profile.grease_enabled | ✓ | 🟡 | 已完成 | — | types.go GreaseEnabled | — | — |
| SEC-130 | profile.expected_ja3_hash(漂移基线) | 🟡 | ✗ | ✗ | 已完成 | — | types.go ExpectedJA3Hash | — |
| SEC-131 | profile.last_validated_at(drift worker 写) | ✗ | ✗ | ✗ | 已完成 | — | types.go LastValidatedAt | — |
| SEC-132 | status 状态机(active/disabled/drift_detected, drift worker-only) | ✗ | ✗ | ✗ | 已完成 | — | types.go adminSettableStatuses | — |
| SEC-133 | drift "clear" admin-override(set active 刷新 last_validated) | ✗ | ✗ | ✗ | 已完成 | — | tlsfpadmin service SetStatus | — |
| SEC-134 | DB-backed CRUD 表(migration 0037) | ✓ | ✗ | ✗ | 已完成 | — | 0037_tls_fingerprint_profiles + sqlc | — |
| SEC-135 | admin CRUD 端点 list/create/get/update/set-status/delete(soft) | ✓ | ✗ | ✗ | 后端有·前端缺 | P2 | tlsfphttp/handler.go 全部; 151 §10/AI-019 后端有前端缺 | TLS-FP profile 管理 UI |
| SEC-136 | update 拒走私 status(DisallowUnknownFields) + set-status 专路径 | 🟡 | ✗ | ✗ | 已完成 | — | tlsfphttp updateHandler/setStatusHandler | — |
| SEC-137 | platform_admin-only gate(403) | 🟡 | ✗ | ✗ | 已完成 | — | tlsfphttp handler.go | — |
| SEC-138 | profile-not-found fail-closed(无静默默认) | 🟡 fallback | ✗ | ✗ | 已完成 | — | transport/factory.go mimicryTemplate(除非 PHASE_A_FALLBACK) | — |
| SEC-139 | random / per-account profile 绑定 | ✓ ResolveTLSProfile(-1=random) | ✗ | ✗ | 部分完成 | P3 | per-mode 模板注册, 非 random-per-account | 可选 random/per-account 绑定 |

## 2.14 uTLS mimicry / 伪装 — 每 vendor 目标一行 (transport/policy.go)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-140 | impersonate Claude Code(Node.js JA3) | ✓ 默认 profile | ✗ | ✓ 固定 HelloChrome | 已完成 | — | policy.go TransportModeMimicryClaudeCode; AnthropicCLIMimicryV1Template | — |
| SEC-141 | impersonate ChatGPT web / Codex CLI | ✗ | ✗ | ✗ | 已完成 | — | policy.go TransportModeMimicryChatGPT | — |
| SEC-142 | impersonate Cursor IDE | ✗ | ✗ | ✗ | 已完成 | — | policy.go TransportModeMimicryCursor | — |
| SEC-143 | impersonate GitHub Copilot | ✗ | ✗ | ✗ | 已完成 | — | policy.go TransportModeMimicryCopilot | — |
| SEC-144 | impersonate Codeium Windsurf | ✗ | ✗ | ✗ | 已完成 | — | policy.go TransportModeMimicryWindsurf | — |
| SEC-145 | impersonate AWS Kiro | ✗ | ✗ | ✗ | 已完成 | — | policy.go TransportModeMimicryKiro | — |
| SEC-146 | impersonate Gemini Advanced | ✗ | ✗ | ✗ | 已完成 | — | policy.go TransportModeMimicryGeminiAdvanced | — |
| SEC-147 | impersonate Google Antigravity | 🟡 无 TLS mimicry | ✗ | ✗ | 已完成 | — | policy.go TransportModeMimicryAntigravity | — |
| SEC-148 | diagnostics-only safe mode | ✗ | ✗ | ✗ | 已完成 | — | policy.go TransportModeDiagnosticsOnly | — |
| SEC-149 | uTLS HelloCustom from spec(非固定 preset) | ✓ | ✗ | ✗ 固定 Chrome | 已完成 | — | transport/mimicry/utls_dialer.go utlsSpec | — |
| SEC-150 | provider×mode allow-matrix(拒非法组合, 无 fail-open) | ✗ | ✗ | ✗ | 已完成 | — | policy.go allowedModesByProvider; ValidateModeForProvider | — |
| SEC-151 | per-provider 隔离(OpenAI/Vertex/Bedrock 默认 standard) | ✗ | ✗ | ✗ | 已完成 | — | policy.go | — |
| SEC-152 | GREASE 注入 / ALPN spoof / EC-point / key-share / PSK / padding / early-data 模板字段 | ✓/🟡 | ✗ | 🟡 | 已完成 | — | mimicry/template.go GREASE/ALPNProtocols/ECPointFormats/KeyShareGroups/PSKModes/PaddingLen/EarlyDataEnabled | — |
| SEC-153 | JA3 string capture + parse-validate(5字段) | ✓ | ✗ | ✗ | 已完成 | — | template.go JA3 + parseJA3 | — |
| SEC-154 | JA4 hash capture + required-when-real | 🟡 | ✗ | ✗ | 已完成 | — | template.go JA4 | — |
| SEC-155 | HTTP/2 SETTINGS / header-order 指纹 | 🟡 | ✗ | 🟡 | 已完成 | — | template.go HTTPLayer.HeaderOrder + utls_dialer http2 | — |
| SEC-156 | HTTP-layer UA/endpoint/refresh-endpoint + Auth-layer(占位) 模板 | ✗ | ✗ | ✗ | 已完成 | — | template.go HTTPLayer/AuthLayer | — |
| SEC-157 | Rust/BoringSSL sidecar transport(unix socket, 更强 mimicry) | ✗ | ✗ | ✗ | 已完成 | — | transport/factory.go sidecarRoundTripper; mimicry/sidecar_client.go | — |
| SEC-158 | per-mode mandatory-sidecar flag | ✗ | ✗ | ✗ | 已完成 | — | factory.go SetMandatorySidecarMode | — |
| SEC-159 | sidecar fail-closed 默认 + opt-in Go-native fallback + count metric | ✗ | ✗ | ✗ | 已完成 | — | factory.go SidecarFallbackEnabled(默认关); SidecarFallbackCount | — |
| SEC-160 | sidecar health-probe(profile-availability ACK 解析) | ✗ | ✗ | ✗ | 已完成 | — | registry.go ProbeSidecarForMode | — |
| SEC-161 | Phase-A fallback 模板(ECH GREASE ext 65037, opt-in env) | ✗ | ✗ | ✗ | 已完成 | — | template.go PhaseADefaultTemplate(HUAKAI_TRANSPORT_PHASE_A_FALLBACK) | — |
| SEC-162 | proxy-aware mimicry dialer(CONNECT/SOCKS5 before uTLS) | ✓ | ✗ | ✓ | 部分完成 | P3 | dispatcher.applyProxy + factory | 整合 CONNECT/SOCKS5 前置 |
| SEC-163 | mimicry 前端管理页 | ✗ | ✗ | ✗ | 缺失(前端 mock) | P3 | 12D §0.1: frontend/lib/api/mimicry.ts 全 mock(后端无端点) | 评估 mimicry 管理端点 + UI |

## 2.15 入站客户端身份 / client-shape — 每信号一行 (clientid/clientid.go)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-164 | detect Cursor(X-Cursor-* / cursor/ UA) | ✗ | ✗ | ✗ | 已完成 | — | clientid.go(conf 1.0/0.9) | — |
| SEC-165 | detect Claude Code(claude-cli/claude-code/ UA, X-Client-Name) | ✓ claude_code_validator.go | ✗ | 🟡 | 已完成 | — | clientid.go | — |
| SEC-166 | detect Cody(Sourcegraph)(X-Cody-* / cody/ UA) | ✗ | ✗ | ✗ | 已完成 | — | clientid.go | — |
| SEC-167 | detect 通用 chat-UI(OpenWebUI/LobeChat/LibreChat/Jan/Chatbox)(Origin/Referer suffix) | ✗ | ✗ | ✗ | 已完成 | — | clientid.go chatUIDomainSuffixes | — |
| SEC-168 | detect script/curl(curl/wget/python-requests/go-http/node-fetch/axios/okhttp) | ✗ | ✗ | ✗ | 已完成 | — | clientid.go isScriptUserAgent | — |
| SEC-169 | X-Client-* 头收集 cap(防内存爆, cap 16) | ✗ | ✗ | ✗ | 已完成 | — | clientid.go xClientCardinalityCap | — |
| SEC-170 | 多信号决策树 + 置信分(0.0-1.0) + 确定性优先级 | 🟡 boolean | ✗ | ✗ | 已完成 | — | clientid.go Detect; detectFromXClient | — |
| SEC-171 | 身份→ctx 传播 + 每身份 expvar 计数 + fail-safe Unknown | ✗ | ✗ | ✗ | 已完成 | — | clientid/context.go; metrics.go; IdentityUnknown fallback | — |
| SEC-172 | Claude Code system-prompt 相似度 gate(Dice 0.5) | ✓ claude_code_validator.go diceCoefficient | ✗ | ✗ | 部分完成 | P3 | clientid 仅 UA/header, 无 system-prompt 相似度 | 可选 system-prompt 相似度 gate |
| SEC-173 | Claude Code metadata.user_id 格式校验 | ✓ | ✗ | ✗ | 缺失 | P3 | — | 可选 user_id 校验 |

## 2.16 客户端 IP 反欺骗解析器 — 每规则一行 (clientip/clientip.go)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-174 | 默认安全: 无 trusted proxy→仅 RemoteAddr, 忽略 XFF | ✗ gin ClientIP | ✗ gin ClientIP | ✗ | 已完成 | — | clientip.go ClientIP | — |
| SEC-175 | trusted-proxy CIDR allowlist gate on socket peer | ✗ | ✗ | ✗ | 已完成 | — | clientip.go isTrusted | — |
| SEC-176 | XFF 右→左 walk, skip trusted, first untrusted=client | ✗ | ✗ | ✗ | 已完成 | — | clientip.go ClientIP loop | — |
| SEC-177 | multi-field XFF join(RFC7230 重复头)反欺骗 | ✗ | ✗ | ✗ | 已完成 | — | clientip.go req.Header.Values join | — |
| SEC-178 | malformed-hop→停在 trusted 边界(不可欺骗返回) | ✗ | ✗ | ✗ | 已完成 | — | clientip.go(parse-err→peer) | — |
| SEC-179 | bare-IP trusted→单主机 /32-/128 + boot fail-loud on bad CIDR | ✗ | ✗ | ✗ | 已完成 | — | clientip.go NewResolver | — |
| SEC-180 | IPv4-mapped unmap 归一化 | ✗ | ✗ | ✗ | 已完成 | — | clientip.go addr.Unmap() | — |
| SEC-181 | trusted proxy IP 解析(X-Forwarded-For 加固) | 🟡 | 🟡 | ✗ | 已完成 | — | clientid/clientid.go; cmd/gateway/rate_limit.go:316-328 | — |

## 2.17 API-key IP allowlist (入站) — 每规则一行

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-182 | per-key IP/CIDR allowlist 在 auth 强制 | ✗ | ✓ token.go AllowIps; auth.go IsIpInCIDRList | ✗ | 后端有·前端缺 | P2 | apikeyipallow/allowlist.go AllowsCSV; migration 0085; 151 H 段 /v1/api-keys/{id}/ip-allowlist; 12D P0-U-016 | 单 key IP allowlist 前端 |
| SEC-183 | empty list=allow-all 默认 | n/a | ✓ | n/a | 已完成 | — | allowlist.go AllowsCSV(nil/empty→true) | — |
| SEC-184 | CIDR+bare-IP normalize(masked canonical) + dedup + CSV 存储 | 🟡 | 🟡 | ✗ | 已完成 | — | allowlist.go normalizeEntry; Normalize; StorageText | — |
| SEC-185 | IPv4-mapped unmap(entry + client) + invalid client IP→deny(fail-closed) | ✗ | 🟡 | ✗ | 已完成 | — | allowlist.go addr.Unmap(); AllowsCSV(parse-err→false) | — |

## 2.18 内容审计 — 每能力 KNOB 一行 (moderation/)

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-186 | Mode off/observe/pre_block | ✓ | 🟡 enable bool | ✗ | 部分完成 | P3 | Enabled bool + sampler async-audit(screener.go) | 显式 mode 三态 |
| SEC-187 | Action enum(allow/block/hash_block/keyword_block/error) | ✓ | ✗ | ✗ | 部分完成 | P3 | Decision enum(pass/block_keyword/block_hash/block_backend/fee_charged) | — |
| SEC-188 | protocol 覆盖(anthropic_messages/openai_responses/openai_chat/gemini/openai_images) | ✓ 5协议 | ✗ | ✗ | 部分完成 | P3 | raw body screen(protocol-agnostic) | — |
| SEC-189 | 关键字阻断列表(per-tenant store) | ✓ | ✓ Aho-Corasick | ✗ | 已完成 | — | moderation/keyword_store.go; screener containsKeyword | — |
| SEC-190 | max keyword count(10000) / length(200 runes) cap | ✓ | ✗ | ✗ | 缺失 | P3 | DB-bound 无 cap | 加 count/length cap |
| SEC-191 | keyword replace(**###**) vs stop-on-hit | ✗/✓ | ✓ SensitiveWordReplace/StopOnSensitiveEnabled | ✗ | 部分完成 | P3 | block-only, 无 inline replace | 可选 replace 模式 |
| SEC-192 | token-boundary 整词匹配(英文) | 🟡 | 🟡 | ✗ | 已完成 | — | screener.go containsKeywordTokens | — |
| SEC-193 | CJK/无空格脚本 substring fallback(Han/Hiragana/Katakana/Hangul/Thai/Lao/Khmer/Myanmar) | ✗ | ✗ | ✗ | 已完成 | — | screener.go containsNoBoundaryScript | — |
| SEC-194 | Unicode NFKC 归一化 + zero-width 字符剥离(200b/c/d/feff/2060) | 🟡 lower | ✗ | ✗ | 已完成 | — | screener.go normalizeModerationText(NFKC); isZeroWidthRune | — |
| SEC-195 | keyword cache(TTL-LRU) | 🟡 | ✗ | ✗ | 已完成 | — | keyword_store.go CachedKeywordStore | — |
| SEC-196 | 已知坏 payload-hash 阻断 + hash cache(TTL-LRU) | ✓ | ✗ | ✗ | 已完成 | — | moderation/hash_store.go CachedHashStore; screener checkHash | — |
| SEC-197 | input-hash 计算(text+image sha256) | ✓ | ✗ | ✗ | 已完成 | — | ScreenRequest.PayloadHash | — |
| SEC-198 | 外部 moderation API 调用(omni-moderation) | ✓ callModeration | ✗ | ✗ | 未做 | P2 | moderation/doc.go 明确 classifiers "need later" | 接外部分类器(可选) |
| SEC-199 | 外部 API 默认 base_url(api.openai.com)+model(omni-moderation-latest) | ✓ | ✗ | ✗ | 未做 | P2 | — | 同上 |
| SEC-200 | 外部 API 多 key pool(append/replace)+ per-key health/freeze(rate-limit 1m/auth 10m/http-err 10s) | ✓ | ✗ | ✗ | 未做 | P3 | — | 同上 |
| SEC-201 | 外部 API per-category 阈值 map + 分数/highest-category eval + canonical 顺序 | ✓ | ✗ | ✗ | 未做 | P3 | — | 同上 |
| SEC-202 | 外部 API timeout(3000ms/max30000)+retry(2/max5) | ✓ | ✗ | ✗ | 未做 | P3 | — | 同上 |
| SEC-203 | 图像审计(count+audit, test-image byte caps 8MB/12MB, data-URL 验证) | ✓ | 🟡 TODO stub | ✗ | 未做 | P3 | — | 可选图像审计 |
| SEC-204 | async observe queue + workers(4/max32) + QueueSize(32768/max100k) | ✓ enqueueAsync | ✗ | ✗ | 部分完成 | P3 | sampler-based async-audit(同步 screen) | 可选异步队列 |
| SEC-205 | sample rate knob | ✓ | ✗ | ✗ | 已完成 | — | SampleRatePct; sampler ShouldSample(FNV) | — |
| SEC-206 | cleanup interval/timeout/delay(24h/30m/5m) | ✓ | ✗ | ✗ | 缺失 | P3 | — | 可选 cleanup 调度 |
| SEC-207 | auto-ban enable + threshold(count) | ✓(默认10) | ✗ | ✗ | 已完成 | — | moderation/ban_counter.go; types.go BanThreshold(默认3) | — |
| SEC-208 | violation window | ✓ ViolationWindowHours(720h) | ✗ | ✗ | 已完成 | — | types.go BanWindowSeconds(默认3600s) | — |
| SEC-209 | ban target(user vs key) | ✓ 禁 USER | ✗ | ✗ | 已完成 | — | ban_counter.go DisableAPIKey(禁 KEY); migration 0090_moderation_violation_events | — |
| SEC-210 | 仅 block-keyword/block-hash 计入 ban | ✗ | ✗ | ✗ | 已完成 | — | ban_counter.go decision filter | — |
| SEC-211 | violation fee charge | ✗ | ✓ ChargeViolationFeeIfNeeded(Grok CSAM) | ✗ | 部分完成 | P3 | ViolationFeeUSD config + DecisionFeeCharged/BillingEventID(charge wiring 外部) | 完整 fee-charge wiring |
| SEC-212 | CSAM marker detect(SAFETY_CHECK_TYPE) + 稳定 fee error-code + skip-retry + fee quota 计算 | ✗ | ✓ violation_fee.go | ✗ | 缺失 | P3 | ViolationFeeUSD decimal only | 可选 CSAM marker |
| SEC-213 | violation email(违规/账号禁用 2 模板) | ✓ content_moderation_email.go | ✗ | ✗ | 缺失 | P3 | — | 可选违规邮件 |
| SEC-214 | hit-retention(180/max3650) / non-hit-retention(3/max3) days | ✓ | ✗ | ✗ | 缺失 | P3 | — | 可选审计保留配置 |
| SEC-215 | block HTTP status(403)+message config | ✓ | ✗ | ✗ | 部分完成 | P3 | 固定 decision | 可配 block status/message |
| SEC-216 | admin CRUD: keywords/hashes/config(list/create/delete/get/update) | ✓ | 🟡 global | ✗ | 部分完成 | P2 | moderationhttp/mount.go GET/POST/DELETE /keywords,/hashes; GET/PUT /config; 151 L 段 /admin/v1/moderation/* | moderation 管理前端 |
| SEC-217 | platform_admin-only gate | 🟡 | ✗ | ✗ | 已完成 | — | moderationhttp admin_config_handler.go | — |
| SEC-218 | 决策审计日志(hash+match-meta only) + 失败 metric 无内容泄漏 | 🟡 excerpt+scores | ✗ | ✗ | 已完成 | — | moderation/audit_log.go; sql_store.go; screener reportModerationFailure | — |

## 2.19 Prompt-injection / token / mixed-channel risk

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-219 | tool-output→text injection 主动过滤 | 🟡 ack 无过滤 | ✗ | 🟡 canonical system-prompt enforce | 未做 | P1 | 大树 D-06; 四项目均无主动过滤 | pluggable prompt-injection 过滤中间件 |
| SEC-220 | prompt injection detection(role override/jailbreak/encoding) | ✗ | ✗ | ✗ | 缺失 | P1 | 大树 D-06; gateway protocol-transparent | regex/embedding-distance 规则层 |
| SEC-221 | PII/secret detection in inbound prompts | ✗ | ✗ | ✗ | 缺失 | P2 | 大树 D-08; AllowlistRedactor 仅响应 | 可配入站 redactor + PII 模式 |
| SEC-222 | mixed-channel risk: source 分歧 | ✗ | ✗ | ✗ | 已完成 | — | mixedchannelrisk/risk.go compareDimension "source" | — |
| SEC-223 | mixed-channel risk: vendor 分歧 | ✗ | ✗ | ✗ | 已完成 | — | risk.go "vendor" dimension | — |
| SEC-224 | mixed-channel risk: credential-type(auth_mode) 分歧 | ✗ | ✗ | ✗ | 已完成 | — | risk.go credentialType dimension | — |
| SEC-225 | mixed-channel risk: same-channel scoping+dedup + 非密元数据only(不读 cred) | ✗ | ✗ | ✗ | 已完成 | — | risk.go Evaluate(ChannelID filter)+dedupe | — |
| SEC-226 | token cross-check(reported vs estimated) | ✗ | 🟡 估算仅计费 | ✗ | 已完成 | — | tokencheck/crosscheck.go CrossCheck | — |
| SEC-227 | token drift verdict 分层(5% warn / 20% fail) + verdict-unknown guard | ✗ | ✗ | ✗ | 已完成 | — | tokencheck/types.go DefaultThresholds; crosscheck.go VerdictUnknown | — |
| SEC-228 | cache-evidence cross-verify(read-tokens/hit-ratio vs usage) | ✗ | ✗ | ✗ | 已完成 | — | tokencheck/cache_verify.go; CacheVerifyResult | — |

## 2.20 认证 / 授权 / 滥用与异常检测

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-229 | API key bcrypt hash(永不明文存; cost10; 仅 prefix) | ✓ | 🟡 | 🟡 inline | 已完成 | — | auth/api_key_resolver.go:145 bcrypt.CompareHashAndPassword | — |
| SEC-230 | key prefix O(1) lookup + bcrypt fan-out cap(16/MaxBcryptFanout=5) | ✓ | ✗ | ✗ | 已完成 | — | api_key_resolver.go:59-64 | — |
| SEC-231 | key expiry TTL / status(active/revoked/disabled) 强制 | ✓ | 🟡 | 🟡 | 已完成 | — | api_key_resolver.go:130,133 | — |
| SEC-232 | user/tenant status 强制(deleted_at/status='active' join) | ✓ | 🟡 | ✗ | 已完成 | — | api_key_resolver.go:139,142 | — |
| SEC-233 | admin 独立 bcrypt(hk_admin_ prefix)+RBAC(platform_admin/tenant_operator) | ✓ | 🟡 | ✗ | 已完成 | — | admin/operator_auth.go:81,119-131 | — |
| SEC-234 | admin key issuance rate limit(30/hour, per-actor PG) | ✓ | ✗ | ✗ | 已完成 | — | admin/issuer.go:75-76 | — |
| SEC-235 | OAuth2 state CSRF + PKCE S256 | ✓ | ✗ | 🟡 | 已完成 | — | credentialacq/oauth_authorization_code.go; oauth.go | — |
| SEC-236 | JWT session 签名+过期校验 | ✓ | 🟡 | ✗ | 已完成 | — | usersession/; hermes/jwt.go | — |
| SEC-237 | 2FA setup/enable/disable/status/backup-codes + passkey 注册/登录 | ✓ | ✓ | ✗ | 已完成 | — | 151 G 段 /v1/auth/2fa/*, /v1/me/passkeys/*; 前端缺(P0-U-006/007/009) | (后端完成; 前端见 P0-U) |
| SEC-238 | login lockout(per-account failed-attempt threshold) | 🟡 | 🟡 | ✗ | 已完成 | — | userauth/service.go:172-176 FailedLoginCount/LockedUntil | — |
| SEC-239 | 专用 login throttle / brute-force limiter | 🟡 generic | 🟡 generic | ✗ | 已完成 | — | loginthrottle/limiter.go | — |
| SEC-240 | captcha / Turnstile gate | ✓ | ✓ | ✗ | 已完成 | — | captcha/verifier.go | — |
| SEC-241 | session anomaly / drift detection(DriftHigh/Med/Low) | ✗ | ✗ | ✗ | 部分完成 | P2 | usersession/anomaly.go:22-44 DetectDrift(IP/16+UA class); 无自动 alert 路径 | 接 drift→alert/response 路径 |
| SEC-242 | 实时欺诈/spike 告警(outbound alert path) | 🟡 | ✗ | ✗ | 部分完成 | P1 | usersession/anomaly.go + mixedchannelrisk; 无 email/webhook 出口 | 接异常→告警出口(见 OBS 告警节) |
| SEC-243 | geofencing / country-based blocking(MaxMind/GeoLite) | ✗ | ✗ | ✗ | 缺失 | P2 | 大树 F-05; 0 命中 | 接 GeoIP 国家封禁(合规) |

## 2.21 速率限制 / 节流 / 配额

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-244 | per-IP 全局限流(180req/180s 默认) | ✓ | ✓ | ✗ | 已完成 | — | cmd/gateway/rate_limit.go:46-47 ipBucketRegistry | — |
| SEC-245 | per-IP auth-strict 限流(/auth/*) + per-path class(login20/register5/reset5/oauth20 per min) | ✓ | ✓ | ✗ | 已完成 | — | rate_limit.go:146-207 authStrict | — |
| SEC-246 | 可配限额(env vars) + Retry-After 头(derived) | ✓ | ✓ | ✗ | 已完成 | — | rate_limit.go:185-219; retryAfterForRate() | — |
| SEC-247 | IP bucket registry bounded(maxBucketsPerTier=50000, overflow reset) | ✓ | ✗ | ✗ | 已完成 | — | rate_limit.go:60 | — |
| SEC-248 | 限流拒绝审计日志(IP/tier/method/path) | ✓ | ✗ | ✗ | 已完成 | — | rate_limit.go:253-260 denied() | — |
| SEC-249 | per-API-key RPM(requests/min)限流 | ✓ | ✓ | ✗ | 缺失 | P0 | 大树 B-09; IP-based only, 单 key 可换 IP 绕过 | rate-limit store keyed on api_key_id |
| SEC-250 | per-API-key TPM(tokens/min)限流 | ✓ | ✓ | ✗ | 缺失 | P0 | 大树 B-10; token 计数仅计费不限流 | quota-service per-key token-minute |
| SEC-251 | per-API-key 并发请求 cap | 🟡 | ✓ | ✗ | 部分完成 | P0 | quota/service.go:52 NeedConcurrencySlot; pg_store.go:355 AcquireConcurrencySlot; quota_concurrency_slots 表 — NeedConcurrencySlot 从不置 true(dormant) | 在 gatewayhttp handler 置 NeedConcurrencySlot=true 激活 |
| SEC-252 | 硬配额强制(deny on exhaustion) + spend cap/budget ceiling | ✓ | ✓ | ✗ | 已完成 | — | quota/service.go:66-158 DenyError; PredictedCost check | — |
| SEC-253 | per-channel 配额隔离 + 预扣(reserve before forward) | ✓ | ✓ | ✗ | 已完成 | — | quota/types.go Scopes; service.go GetReservationByClaimForUpdate | — |
| SEC-254 | 配额 reserve 幂等/重放去重(RequestFingerprint) | ✓ | ✗ | ✗ | 已完成 | — | quota/service.go:49 RequestFingerprint | — |

## 2.22 密钥/凭证安全 + webhook + 滥用控制

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| SEC-255 | API key 环境隔离(Live/Test/Admin: hk_live_/hk_test_/hk_admin_) | ✓ | 🟡 | ✗ | 已完成 | — | admin/keygen.go:34-44 | — |
| SEC-256 | API key 读/写 scope(permission bits) | ✗ | ✗ | ✗ | 缺失 | P2 | 大树 E-09; 所有客户 key 同权限 | api_keys.scope 列(read/write/admin-read) |
| SEC-257 | IssueResult 明文经 String() 脱敏 | ✗ | ✗ | ✗ | 已完成 | — | admin/issuer.go:60-68 | — |
| SEC-258 | API key 明文永不入日志(仅 prefix) | 🟡 | 🟡 | 🟡 | 已完成 | — | api_key_resolver.go:15(CMB-5); IssueResult.String() | — |
| SEC-259 | 用户账户挂起强制 + key 撤销带 operator reason(soft-delete) | ✓ | 🟡 | ✗ | 已完成 | — | userauth/service.go:166; db/admin/admin_api_keys.sql:64-65 revoked_reason | — |
| SEC-260 | refresh storm 保护(singleflight + token bucket, 3-scope) | ✗ | ✗ | ✗ | 已完成 | — | gateway/storm_policy.go | — |
| SEC-261 | webhook HMAC-SHA256 验签(timestamp+raw body) | ✓ | 🟡 | ✗ | 已完成 | — | paymenthttp/provider_hmac.go:93-124 VerifyWebhook | — |
| SEC-262 | webhook timestamp replay window(5min 默认, 可配) | ✓ | ✗ | ✗ | 已完成 | — | provider_hmac.go:109 | — |
| SEC-263 | webhook 幂等(dedup on RequestFingerprint) | ✓ | ✗ | ✗ | 已完成 | — | voucher/idempotency.go; payment/callback.go | — |
| SEC-264 | mock payment provider 生产禁用(fail-closed) | ✗ | ✗ | ✗ | 已完成 | — | provider_hmac.go:147-149 ErrMockProviderForbidden | — |
| SEC-265 | 每客户端高频滥用信号(rule engine) | ✗ | ✗ | ✗ | 部分完成 | P3 | clientid/metrics.go 计数(无规则引擎) | 可选高频滥用规则引擎 |
| SEC-266 | channel/account 异常(mixed-cred + TLS drift) | 🟡 | ✗ | ✗ | 已完成 | — | mixedchannelrisk + tlsfpadmin drift status | — |
| SEC-267 | 设备/硬件指纹绑定身份 | ✗ | 🟡 ionet HW(无关) | ✗ | 缺失 | P3 | 大树/细树 N; 四项目均无 | 可选设备指纹绑定 |


# ====================  模块 H  ====================
# 标杆 · API-key/通知/后台/前端/部署/Rust/i18n

> 单模块标杆功能树。三源合并去重: (1) 大功能树骨架 `feature-tree/admin-ops-platform.md`(63 行 + 15 缺失排名),(2) 字段级细树 `feature-audit/08-keys-notify-frontend-deploy-fine.md`(A-J 段:key 字段/通知渠道/配置逐项/管理面板逐屏/用户门户逐屏/部署逐目标/Rust/i18n/杂项),(3) 151 第三方对标 `151-ref/`(12D §7 ADM/§8 AI/§10 OPS + 10C 开发者网关 + 10D 安全运维数据文档 + 09A 产品 SaaS + 09E 数据分析文档)。
>
> HUAKAI 云端基线: `origin/fix/hermes-phase-1-e33d940@e89d7fce`(183 OpenAPI paths)。**`origin/main` 仅 83 paths,落后 fix 分支**。本表只做源码确认,不写"运行验证通过"(本轮未跑 Docker)。
>
> **HUAKAI状态 六级(禁止虚标)**: `已完成`(后端+前端闭环,或无需 UI 的内部安全/运维原语已就绪) / `部分完成`(后端全+前端部分,或后端有缺高级动作/缺硬化) / `后端有·前端缺`(路由/服务存在但无前端页面或导航 disabled) / `缺失`(参考有、HUAKAI 后端也无) / `未做`(从未实现,无设计) / `未合并main`(fix 分支有,main 尚未合并)。
>
> ref 列: ✓=该参考已上架, ✗=无, 🟡=部分/不完全等价。cliproxy 为单租户 CLI 代理(无平台用户账户/支付/订阅),平台商业类几乎全 ✗(by-design);其优势在 YAML 配置面/TUI/credential 文件管理。
>
> 段落: A=API-key 字段+操作, B=通知渠道+偏好+硬化, C=管理面板逐屏, D=用户门户逐屏, E=配置逐项, F=部署逐目标, G=Rust 高性能, H=i18n 逐语言, I=文档/测试/发布/运维 SOP, J=运维/告警/风控/导出/数据保留, K=杂项(MCP/TUI/mgmt-API/passkey/captcha)。

---

## A. API-key 字段级 (每 key 字段/操作一行)

证据根: HUAKAI `userkey/userkey.go` + `userkeycontrols/types.go` + `userkeyhttp` + `userkeycontrolshttp`;migrations 0085(ip-allowlist)。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| KEY-001 | key name / label | ✓ | ✓ | ✗ | 已完成 | — | IssueRequest.Name MaxNameLen=128→ErrInvalidName | — |
| KEY-002 | key value / secret string | ✓ | ✓ | ✓ | 已完成 | — | plaintext 仅 create 返回;bcrypt cost10 at-rest | — 强于 cliproxy(明文存) |
| KEY-003 | custom-key (用户自带值) | ✓ CustomKey | ✗ | ✓ | 缺失 | P3 | HUAKAI 始终服务端生成 | 评估 custom-key 支持(信任链 stance 可能拒) |
| KEY-004 | key prefix (脱敏展示) | ✗(handler mask) | ✓ MaskTokenKey | ✗ | 已完成 | — | KeyPrefix in KeyDescriptor(GET 唯一返回) | — |
| KEY-005 | status (active/revoked) | ✓ | ✓ | ✗ | 已完成 | — | Status 字段;Revoke→revoked | — |
| KEY-006 | quota / spend-cap (USD) | ✓ decimal | ✓ RemainQuota | ✗ | 已完成 | — | userkeycontrols SetKeyQuota LimitUSD decimal | — |
| KEY-007 | quota used / spent | ✓ quota_used | ✓ UsedQuota | ✗ | 部分完成 | P2 | quota pkg policy 跟踪,非 key 行 | 在 key descriptor 暴露 used/spent |
| KEY-008 | unlimited-quota flag | 🟡(=0 sentinel) | ✓ UnlimitedQuota | n/a | 部分完成 | P3 | 省略 LimitUSD = unlimited | 显式 unlimited 布尔 |
| KEY-009 | reset-quota action | ✓ ResetQuota | ✗ | ✗ | 缺失 | P3 | 无 | 增 quota 重置子动作 |
| KEY-010 | expiry (expires_at/never) | ✓ | ✓ -1=never | ✗ | 已完成 | — | ExpiresAt *time.Time nil=never;future-only ErrInvalidExpiry | — |
| KEY-011 | last-used timestamp | ✓ | ✓ AccessedTime | ✗ | 已完成 | — | KeyDescriptor.LastUsedAt | — |
| KEY-012 | group binding | ✓ group_id | ✓ Token.Group | ✗ | 已完成 | — | SetKeyGroup PUT /{id}/group | — |
| KEY-013 | cross-group retry | ✗ | ✓ CrossGroupRetry | ✗ | 缺失 | P3 | 无 | 评估自动跨组重试 |
| KEY-014 | model allow-list (每 key) | 🟡(组级) | ✓ ModelLimits | ✓ models[]/excluded[] | 部分完成 | P2 | 模型 allow-list 在 provider-account 级,非 key 级 | 增 per-key model allow-list |
| KEY-015 | IP allow/whitelist | ✓ JSON CIDR | ✓ AllowIps | ✗ | 已完成 | — | SetKeyIPAllowlist;apikeyipallow;PUT /{id}/ip-allowlist(0085) | — |
| KEY-016 | IP blacklist | ✓ ip_blacklist | ✗ | ✗ | 缺失 | P3 | 无 | 评估 IP 黑名单 |
| KEY-017 | rate-limit 5h/1d/7d 三窗口 | ✓(含 usage+window_start) | 🟡(model 级) | ✗ | 部分完成 | P2 | quota window kind,非 RPM/三窗口 | 对齐 sub2api 三窗口 RPM 限流 |
| KEY-018 | reset-rate-limit-usage action | ✓ | ✗ | ✗ | 缺失 | P3 | 无 | 增限流用量重置 |
| KEY-019 | per-key proxy/base-url/headers/prefix/disable-cooling | ✗ | ✗ | ✓(逐项) | 缺失 | P3 | cliproxy upstream-cred 维度,与 user-key 正交 | by-design 不同维度;非缺口 |
| KEY-020 | 软删除/撤销审计保留 | ✓ SoftDelete | ✓ gorm.DeletedAt | ✗ | 已完成 | — | revoked_at + revoked_reason(审计保留) | — |
| KEY-021 | active-key cap per user | ✗ | 🟡 MaxUserTokens 全局 | ✗ | 已完成 | — | MaxActiveKeysPerUser=20 in-tx ErrActiveKeyCapHit | — 强于两参考 |
| KEY-022 | environment (live/test) | ✗ | ✗ | ✗ | 已完成 | — | Environment EnvLive/EnvTest(拒 EnvAdmin) | — HUAKAI 独有 |
| KEY-023 | create | ✓ | ✓ | ✓ | 已完成 | — | POST /v1/api-keys/ | — |
| KEY-024 | list (含 revoked) | ✓ | ✓ | ✓ | 已完成 | — | GET /v1/api-keys/ | — |
| KEY-025 | get one (owner-scoped) | ✓ | ✓ | n/a | 已完成 | — | GET /v1/api-keys/{id}→ErrNotFound | — |
| KEY-026 | update (name/status patch) | ✓ | ✓ UpdateToken | ✓ | 部分完成 | P2 | 仅 quota/group/ip 子资源,无 name/status patch | 增通用 PATCH(name/status) |
| KEY-027 | delete/revoke (幂等) | ✓ | ✓ | ✓ | 已完成 | — | DELETE /v1/api-keys/{id} 幂等 | — |
| KEY-028 | batch delete/revoke | ✗ | ✓ DeleteTokenBatch | 🟡 | 缺失 | P2 | 单删 only | 增批量撤销端点 |
| KEY-029 | full-key reveal (create 后) | 🟡(create 返回) | ✓ GetTokenKey | n/a | 缺失 | P2 | 无 reveal 端点(信任链 stance) | 评估一次性 reveal-proof 端点 |
| KEY-030 | batch full-key reveal | ✗ | ✓ cap100 | n/a | 缺失 | P3 | 无 | 同上,批量 |
| KEY-031 | set quota/group/ip 子资源端点 | ✗ | ✗ | ✗ | 已完成 | — | PUT /{id}/quota,/group,/ip-allowlist 各独立 | — HUAKAI 独有粒度 |
| KEY-032 | API key 列表页 (前端) | ✓ KeysView | ✓ features/keys | ✗ | 后端有·前端缺 | P0 | userkeyhttp 后端;/api-keys sidebar disabled:true 无 page.tsx | 新建 frontend/app/api-keys/page.tsx |
| KEY-033 | key 创建/显示/复制 modal (前端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端 create 返回 plaintext;无 UI | key 创建 modal + 一次性 copy |
| KEY-034 | key 删除/撤销 (前端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端 DELETE;无 UI | key action 按钮 |
| KEY-035 | 单 key quota/group/IP-allowlist 配置 (前端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 三子资源后端有;无 UI | key settings 面板 |
| KEY-036 | 单 key model-limits UI | 🟡 | ✓ | ✗ | 缺失 | P2 | per-key model allow-list 后端也缺(见 KEY-014) | 后端补 + UI |
| KEY-037 | key 使用日志/用量 (前端) | ✓ KeyUsageView | ✓ | ✗ | 后端有·前端缺 | P1 | meusagehttp 后端;无 UI | per-key 用量页 |

---

## B. 通知渠道 + 偏好 + 投递硬化 (每渠道/字段一行)

证据根: HUAKAI `notify/types.go` + `notify/notifier.go` send() switch + migration 0089。四方真 push 渠道 = {email,webhook,bark,gotify}。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| NOTIF-001 | Email 渠道 | ✓ | ✓ | ✗ | 已完成 | — | TypeEmail→sendEmail(smtp_sender) | — |
| NOTIF-002 | Webhook (HMAC 签名) | ✗ | ✓ | ✗ | 已完成 | — | TypeWebhook→sendWebhook;WebhookSecret 必填;signWebhook HMAC | — |
| NOTIF-003 | Bark | ✗ | ✓ | ✗ | 已完成 | — | TypeBark→sendBark;BarkURL;SSRF-guarded | — |
| NOTIF-004 | Gotify | ✗ | ✓ | ✗ | 已完成 | — | TypeGotify→sendGotify;URL+Token+Priority(1-10 default5) | — |
| NOTIF-005 | Telegram push | ✗ | ✗(仅 OAuth 登录) | ✗ | 缺失 | P3 | 四方皆无真 push | 评估 Telegram 推送渠道 |
| NOTIF-006 | Discord push | ✗ | ✗(OAuth only) | ✗ | 缺失 | P3 | 四方皆无 | 评估 Discord 推送 |
| NOTIF-007 | WeChat/企业微信/Server酱/WxPusher | ✗ | ✗(OAuth only) | ✗ | 缺失 | P3 | 四方皆无 | 评估微信系推送 |
| NOTIF-008 | DingTalk push | ✗ | ✗ | ✗ | 缺失 | P3 | 四方皆无 | 评估钉钉推送 |
| NOTIF-009 | In-app inbox / 站内信 | ✓ announcement+read+notify-mode | 🟡 全局 banner | ✗ | 缺失 | P2 | HUAKAI 仅出站,无站内信收件箱 | 建站内信表 + 收件箱 UI |
| NOTIF-010 | "none" / opt-out | 🟡 | 🟡 | n/a | 已完成 | — | TypeNone(短路 DB,typecache) | — |
| NOTIF-011 | per-user 渠道选择 | ✓ | ✓ NotifyType | ✗ | 已完成 | — | notify/store Settings.NotifyType per (tenant,user) | — |
| NOTIF-012 | balance-low 阈值 (值) | ✓ | ✓ QuotaWarning | ✗ | 已完成 | — | BalanceThreshold decimal default 5.00 | — |
| NOTIF-013 | 阈值类型 (fixed/percentage) | ✓(mig102) | ✗(fixed) | ✗ | 缺失 | P3 | HUAKAI fixed only | 增 percentage 阈值类型 |
| NOTIF-014 | extra recipient emails | ✓(mig104) | ✗ | ✗ | 缺失 | P3 | 无 | 增额外收件人邮箱 |
| NOTIF-015 | model-update notify (admin) | ✗ | ✓ | ✗ | 缺失 | P3 | 无 | 评估模型更新通知 |
| NOTIF-016 | language pref (通知语言) | ✓(前端) | ✓ zh/en | ✗ | 缺失 | P2 | 无(且后端无 i18n,见 H 段) | 依赖后端 i18n |
| NOTIF-017 | HMAC webhook 签名硬化 | ✗ | ✓ | ✗ | 已完成 | — | signWebhook | — |
| NOTIF-018 | SSRF 出站守卫 | ✗ | ✗ | ✗ | 已完成 | — | validateOutboundURL(webhook/bark/gotify) | — HUAKAI 独有 |
| NOTIF-019 | CRLF header-injection 拒绝 | ✗ | ✗ | ✗ | 已完成 | — | rejectHeaderInjection(email/gotify-token) | — HUAKAI 独有 |
| NOTIF-020 | rate-limit / dedup | ✗ | 🟡 cooldown | ✗ | 已完成 | — | RateLimiter Allow(tenant,user,event) | — |
| NOTIF-021 | first-crossing-only (反 spam) | ✓ crossedDownward | 🟡 | ✗ | 已完成 | — | settler.go crossing detect | — |
| NOTIF-022 | DLQ / retry | 🟡 email_queue | ✗ | ✗ | 已完成 | — | email/dlq_worker + obs_dlq | — |
| NOTIF-023 | inactive-field scrub on save | ✗ | ✗ | ✗ | 已完成 | — | store scrubs GotifyPriority when type≠gotify | — HUAKAI 独有 |
| NOTIF-024 | 通知偏好 UI (前端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | notify/store 后端;无 /notifications 页(12D OPS-013) | 建通知偏好设置页 |
| NOTIF-025 | 阈值告警系统 (admin 运营级) | ✗(用户级) | ✗ | ✗ | 缺失 | P2 | 骨架 #46:无低余额/渠道失败率/配额耗尽/DLQ 深度 admin 告警 | 见 J 段 ALERT-* |

---

## C. 管理面板逐屏 (每屏一行)

证据根: HUAKAI Next.js `frontend/app/*/page.tsx`(9 个实存页) + `Sidebar.tsx` navItems(disabled 标记)。sub2api=Vue admin views;new-api=React features。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| ADMIN-001 | Dashboard (运营总览) | ✓ | ✓ | ✓ TUI | 部分完成 | P1 | app/dashboard/page.tsx 活;但消费 mock/stub 数据(骨架 #52)+ 无聚合 rollup 端点(#3) | 接真聚合 API;补 rollup-by-tenant/model/period 端点 |
| ADMIN-002 | Accounts/Channels (AI provider) | ✓ | ✓ | ✓ mgmt | 部分完成 | P0 | app/accounts/page.tsx 存在但 sidebar disabled:true | 启用导航 + 补完整 CRUD/test/health UI |
| ADMIN-003 | Channel-monitor | ✓ | ✓ | ✗ | 部分完成 | P1 | channelhealth 后端;并入 accounts 页 | 独立监控视图 |
| ADMIN-004 | Users (用户管理) | ✓ UsersView | ✓ features/users | ✗ | 后端有·前端缺 | P0 | adminhttp 后端;无 /users 页;且 admin 用户列表/CRUD 路由也缺(骨架 #8-12, ADM-011/012) | 后端补 admin 用户 CRUD + 用户管理页 |
| ADMIN-005 | Usage/Logs | ✓ | ✓ | ✓ mgmt | 部分完成 | P1 | app/audit + app/observability 活;但无 per-request 日志搜索(骨架 #7) | 补 request-log 搜索页 |
| ADMIN-006 | Pricing/ratio | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | pricingcataloghttp 后端;无 pricing 页 | 价格/倍率管理页 |
| ADMIN-007 | Settings (系统设置) | ✓ | ✓ | ✓ mgmt | 后端有·前端缺 | P0 | platformsettings 后端;/settings sidebar disabled | 系统设置页(model/billing/auth 分页) |
| ADMIN-008 | Groups (用户组) | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | routeadmin 后端;无 page | 用户组管理页 |
| ADMIN-009 | Announcements (公告) | ✓ AnnouncementsView | 🟡 notice | ✗ | 缺失 | P2 | 骨架 #49:无 announcement CRUD | 公告 CRUD 后端 + 后台/用户公告页 |
| ADMIN-010 | Promo codes (推广码) | ✓ PromoCodesView | ✓ | ✗ | 缺失 | P2 | KeyPromoEnabled flag 有;无 promo 发行 pkg | promo 发行后端 + 后台页 |
| ADMIN-011 | Redeem codes (兑换/礼品码) | ✓ RedeemView | ✓ | ✗ | 部分完成 | P2 | voucher pkg(支付邻近);无社区 redeem UI | redeem 用户页 + 后台页 |
| ADMIN-012 | Vouchers (券管理) | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | voucher_handler.go:76-85(批量≤1000);无后台 voucher 页(ADM-017) | 后台 voucher 管理页 |
| ADMIN-013 | Subscriptions (订阅) | ✓ SubscriptionsView | ✓ features/subscriptions | ✗ | 后端有·前端缺 | P1 | subscriptionhttp 后端;无 page(ADM-018) | 后台订阅管理页 |
| ADMIN-014 | Proxies (代理管理) | ✓ ProxiesView | ✗ | ✓ config | 后端有·前端缺 | P2 | proxysecret 后端;无 page | 代理管理页 |
| ADMIN-015 | Risk-control (风控) | ✓ RiskControlView | ✗ | ✗ | 后端有·前端缺 | P2 | mixedchannelrisk 后端;无 page | 风控页(见 J 段 RISK-*) |
| ADMIN-016 | Backup (备份) | ✓ BackupView | ✗ | ✗ | 缺失 | P2 | 骨架/12D OPS-005:无 S3 备份/调度/恢复 | 备份配置/调度/恢复后端 + 页 |
| ADMIN-017 | Orders dashboard (订单后台) | ✓ AdminOrdersView | ✓ features/wallet | ✗ | 后端有·前端缺 | P1 | paymenthttp 后端(ADM-001);无后台订单页 | 后台支付订单页 + 筛选 |
| ADMIN-018 | Payment dashboard (支付后台) | ✓ AdminPaymentDashboardView | ✓ | ✗ | 后端有·前端缺 | P1 | 后端 admin payment API(ADM-002);无页 | 后台支付 dashboard 页 |
| ADMIN-019 | Payment plans (套餐) | ✓ AdminPaymentPlansView | ✓ | ✗ | 后端有·前端缺 | P1 | 后端有;无页 | 套餐编辑页 |
| ADMIN-020 | Refund 审核 (approve/reject) | ✓ admin refund | 🟡 | ✗ | 后端有·前端缺 | P1 | 后端 refund-request + admin approve(ADM-007/008);无页;且无专用 refund 状态台账(骨架 #29) | 后台退款审核页 + 专用退款台账 |
| ADMIN-021 | Affiliate invites/rebates/transfers/records (后台) | ✓(4 子屏) | 🟡 | ✗ | 缺失 | P2 | 骨架/12D ADM-019:返佣后台四子屏全缺 | 返佣后台四视图 |
| ADMIN-022 | Ops dashboard (运维) | ✓ OpsDashboard | ✓ performance-metrics | ✗ | 部分完成 | P2 | observability page 活;但多子页不全(OPS-001) | 补 ops 多子页 |
| ADMIN-023 | 2FA/TOTP admin | ✓ | ✓ | ✗ | 已完成 | — | controlhttp/twofa_handler | — |
| ADMIN-024 | Model capabilities admin | 🟡 | ✓ | ✓ mgmt | 部分完成 | P2 | controlhttp/model_admin_capabilities_handler 后端;UI 有限 | model 元数据 CRUD UI |
| ADMIN-025 | Dispute resolve (争议) | ✗ | ✗ | ✗ | 后端有·前端缺 | P2 | controlhttp/dispute_handler;无完整 UI(OPS-014/015) | 争议工单列表/解决 UI |
| ADMIN-026 | Route admin (路由) | ✗ | ✗ | ✗ | 后端有·前端缺 | P1 | controlhttp/routeadmin_handler;无 page(AI-018) | 路由规则管理页 |
| ADMIN-027 | TLS fingerprint profile UI | ✓ | ✗ | ✗ | 后端有·前端缺 | P2 | 后端有(AI-019);无 UI | 指纹 profile 页 |
| ADMIN-028 | 手动余额调整页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | balance_credit_handler POST /admin/v1/balances/adjustments;无页(ADM-014) | 后台余额调整页 |
| ADMIN-029 | Provider account CRUD/test/health (后端) | ✓ | ✓ | ✓ | 部分完成 | P1 | admin_pool_accounts/provider_account_test/health handlers;但 client 无 delete(AI-002) | 补 delete + 完整 UI |
| ADMIN-030 | Credential 获取/轮换/续期 (后端) | 🟡 | 🟡 | ✓ | 部分完成 | P1 | admin_credentials + acquisition(5 import 模式 + OAuth);renew-status;前端有限 | credential 管理向导 UI |
| ADMIN-031 | DLQ list/replay | 🟡 | ✗ | ✗ | 已完成 | — | admin_dlq_handler;handler+usage-record DLQ replay | — |
| ADMIN-032 | L2 cache stats/get/delete | ✗ | ✓ | ✓ | 已完成 | — | admin_cache_l2_handler(tenant-scoped) | — |
| ADMIN-033 | Email settings get/update/test | ✓ | ✓ | ✗ | 已完成 | — | admin_email_settings_handler(SMTP + test send) | — |
| ADMIN-034 | Model catalog sync (trigger) | ✓ | ✓ | ✓ | 已完成 | — | model_sync_handler POST /admin/v1/model-sync | — |
| ADMIN-035 | Model enable/disable per channel | ✗ | ✓ | ✗ | 缺失 | P2 | 骨架 #37:无 per-channel model toggle | 增模型启停端点 + UI |
| ADMIN-036 | Model pricing 配置 (admin-set rates) | ✗ | ✓ | ✗ | 缺失 | P2 | 骨架 #38:仅 static config read-path,无 write 端点 | 增 per-model 定价写端点 |
| ADMIN-037 | Rate-limit 配置 (global/user/tenant) | ✗ | ✓ | ✗ | 缺失 | P2 | 骨架 #39:仅 provider clear-rate-limit | 增 RL policy 配置/查看端点 |
| ADMIN-038 | Quota override/view per-user (admin) | ✗ | ✓ | ✗ | 缺失 | P1 | 骨架 #25/#26:quota pkg(mig0070)全实现但 0 HTTP admin 端点 | 暴露 quota admin GET/PUT 端点 |
| ADMIN-039 | Feature-flag/account-mode mutation | ✗ | ✓ | ✗ | 部分完成 | P2 | account_modes_handler 仅 GET 只读(骨架 #43/#44) | 增 per-tenant toggle 端点 |
| ADMIN-040 | Tenant management (create/list/configure) | 🟡 | ✓ | ✗ | 缺失 | P2 | 骨架 #50:tenant 仅 DB 行,无 admin CRUD | tenant 供给 admin API |
| ADMIN-041 | Admin impersonation / act-as-user | ✓ | ✗ | ✗ | 缺失 | P3 | 骨架 #62 | 评估 impersonate(支持调试) |
| ADMIN-042 | System health aggregate 端点 | ✗ | ✓ /api/status | ✗ | 缺失 | P2 | 骨架 #45:无单一系统健康端点 | 聚合 pool/DLQ/worker-lag 健康端点 |
| ADMIN-043 | Webhook 管理 (admin 出站 hooks) | ✗ | ✗ | ✗ | 缺失 | P3 | 骨架 #48:仅入站支付 webhook,无 admin 可配出站 | 评估出站 webhook 管理 |
| ADMIN-044 | Setup wizard (后台首次引导) | ✓ SetupWizardView | ✓ routes/setup | ✗(config) | 部分完成 | P2 | admin bootstrap token(无 UI)(门户 F 段) | setup 向导 UI |
| ADMIN-045 | Panel auth whoami (角色解析) | ✓ | ✓ | ✗ | 已完成 | — | panelauthhttp GET /v1/auth/me(deny soft-deleted) | — |
| ADMIN-046 | Bulk user 操作 (批量封禁/配额重置) | 🟡 | 🟡 | ✗ | 部分完成 | P3 | 骨架 #63:仅 voucher batch | 批量用户状态/配额操作 |

---

## D. 用户门户逐屏 (每屏一行)

证据根: HUAKAI `frontend/app` 9 页 + 12D §0.1 缺失目录清单 + 12D §2 P0-U 行。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| FE-001 | 登录页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | auth_handler 后端;无 login 目录(12D §0.1, P0-U-001) | frontend/app/login/page.tsx |
| FE-002 | 注册页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | POST /v1/auth/register;无 register 页(P0-U-002) | frontend/app/register/page.tsx |
| FE-003 | 忘记/重置密码页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端有;无页(P0-U-003) | auth flow 页 |
| FE-004 | 邮箱验证页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端有;无页(P0-U-004) | email verify 页 |
| FE-005 | OAuth callback 前端 | ✓ | ✓ | ✓ mgmt | 后端有·前端缺 | P0 | 后端有;无 /oauth/[provider](P0-U-005) | oauth callback 页 |
| FE-006 | 2FA 登录挑战页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端有;无 login 2FA step(P0-U-006) | login 2FA 步骤 |
| FE-007 | Passkey 登录按钮/流程 | ✗ | ✓ | ✗ | 后端有·前端缺 | P0 | passkeyhttp 后端;无 UI(P0-U-007) | login/profile passkey 流程 |
| FE-008 | 用户资料页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | /v1/auth/me;无 /profile(P0-U-008) | frontend/app/profile/page.tsx |
| FE-009 | 安全设置页 (2FA/passkey) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 2FA/passkey 后端;无页(P0-U-009) | /profile/security |
| FE-010 | session/设备管理页 | ✗ | ✓ | ✓ mgmt | 后端有·前端缺 | P1 | session_handler 后端;无页(P0-U-010) | /profile/sessions |
| FE-011 | 用户 dashboard 商业版 | ✓ | ✓ | 🟡 TUI | 部分完成 | P0 | 当前 dashboard 是运维面非商业面(P0-U-020) | 拆分用户/管理员 dashboard |
| FE-012 | 用户用量首页 | ✓ | ✓ | ✓ mgmt | 后端有·前端缺 | P0 | meusagehttp;/usage sidebar disabled 无 page(P0-U-017) | frontend/app/usage/page.tsx |
| FE-013 | 用量趋势/图表 | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | time-series 后端;无图表(P0-U-018) | usage charts |
| FE-014 | 模型/key 维度用量筛选 | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | 部分后端;无 UI(P0-U-019, 10C §3) | usage filters |
| FE-015 | 钱包页 | ✗ | ✓ /wallet | ✗ | 后端有·前端缺 | P0 | 后端有支付 API;无 wallet 目录(P0-P-001) | frontend/app/wallet/page.tsx |
| FE-016 | 充值/购买页 + 金额/方式选择器 | ✓ | ✓ TopUp | ✗ | 后端有·前端缺 | P0 | paymenthttp;无页(P0-P-002~005) | 充值开单页 |
| FE-017 | 订单列表/详情/轮询页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | list/detail 后端;无页(P0-P-007/008) | orders 页 |
| FE-018 | 取消订单 + 退款申请按钮 | ✓ | 🟡 | ✗ | 后端有·前端缺 | P0 | cancel/refund-request 后端;无 UI(P0-P-009/010) | 订单操作按钮 |
| FE-019 | 支付结果/QR/Stripe/Airwallex 页 | ✓ | ✓ | ✗ | 缺失 | P1 | 真实 PSP adapter 明确暂不实现(P0-P-011~017);前端也缺 | 视商户号决定;先补结果页 |
| FE-020 | 收据/发票下载 | ✓ | 🟡 | ✗ | 缺失 | P2 | 09A §6:无 invoice(ADM-021) | invoice 生成 + 下载 |
| FE-021 | 订阅用户页 (套餐卡/购买/续订/取消) | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | plans/purchase/cancel-renew 后端;无页(SUB-001~008) | frontend/app/subscriptions/page.tsx |
| FE-022 | 用户返佣/邀请页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | /v1/me/invitations 后端;无页(AFF-001~005) | frontend/app/affiliate/page.tsx |
| FE-023 | 返佣余额/明细/提现入口 | ✓ | 🟡 transfer | ✗ | 缺失 | P2 | 仅余额奖励;无 payout/transfer 闭环(AFF-008/009) | 返佣明细 + 提现(需后端) |
| FE-024 | 兑换码用户页 | ✓ RedeemView | ✓ | ✗ | 后端有·前端缺 | P2 | voucher 后端;无 redeem 页(AFF-015) | redeem 用户页 |
| FE-025 | 签到页 (前端) | ✗ | ✓ React | ✗ | 后端有·前端缺 | P2 | checkinhttp 后端;无 UI(细树 C 段) | 签到 UI |
| FE-026 | 通知中心/收件箱 (前端) | ✓ | 🟡 | ✗ | 后端有·前端缺 | P1 | notify 后端出站;无 /notifications 页 + 无站内信(NOTIF-009/024) | 通知中心页 |
| FE-027 | 可用渠道/状态页 | ✓ | ✓ | ✗ | 已完成 | — | accounts + selection page 活 | — |
| FE-028 | Chat playground (前端) | ✗ | ✓ | ✗ | 部分完成 | P2 | app/chat/page.tsx 调试器;非完整产品 playground(OPS-018, 10C §1) | 完善 playground(参数/示例/费用预估) |
| FE-029 | Bindings (OAuth 绑定) | ✗ | ✓ | ✓ mgmt | 已完成 | — | app/bindings/page.tsx 活 | — 注:源码注释真实 binding 后端仍需补 |
| FE-030 | Mimicry/fingerprint (用户) | ✗ | ✗ | ✗ | 部分完成 | P3 | app/mimicry/page.tsx 但 lib/api/mimicry.ts 全 mock(12D §0.1) | 接真后端端点(去 mock) |
| FE-031 | Media task center (前端) | ✗ | ✓ | ✗ | 后端有·前端缺 | P2 | mediatask 后端(platformsettings);无 UI(OPS-017, 10C §8) | 媒体任务中心页 |
| FE-032 | 公共站点 (首页/价格/状态/法务) | ✓ | ✓ | ✗ | 缺失 | P2 | 09A §1:无价值主张/价格/状态/ToS/隐私页 | 公共营销/法务页 |
| FE-033 | 用户 onboarding 流程 | 🟡 | ✓ | ✗ | 缺失 | P2 | 09A §2:无欢迎流程/quickstart/任务清单 | onboarding 引导 |
| FE-034 | 组织/团队能力 (members/projects/SSO) | 🟡 | 🟡 | ✗ | 缺失 | P3 | 09A §5:无 org model/team/SSO/SCIM(企业级) | 视企业需求评估 |
| FE-035 | 工单/客服中心 (用户侧) | ✗ | ✗ | ✗ | 缺失 | P2 | 09A §11:无工单模型/列表/回复 | 工单系统(用户提单) |

---

## E. 配置选项 (每配置项一行)

证据根: HUAKAI `platformsettings/types.go`(31 keys + 类型化默认 + 逐键验证 + 审计 + eventbus) + `config/config.go`(env)。cliproxy ~50 YAML 顶层选项为配置面最广。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| CFG-001 | YAML 配置文件 | ✓ | 🟡 | ✓ full(~50 keys) | 缺失 | P2 | config.go 注释明确 YAML deferred | 评估 YAML 配置层(对齐 cliproxy) |
| CFG-002 | env-var 配置 (fail-loud) | ✓ | ✓ | ✓ | 已完成 | — | HUAKAI_* fail-loud(config/config.go) | — |
| CFG-003 | DB-backed live settings | ✓ | ✓ options | ✗ | 已完成 | — | platformsettings(31 keys) | — |
| CFG-004 | file-watch hot-reload | 🟡 | 🟡 polled | ✓ watcher + PutConfigYAML | 部分完成 | P3 | 无 file-watch;eventbus 替代 | 视 YAML 决定是否加 file-watch |
| CFG-005 | cross-node 传播 | ✗ | ✓ Redis | ✓ redisqueue | 已完成 | — | config/eventbus.go(Redis/PG) | — |
| CFG-006 | L2 in-proc cache | ✗ | ✓ | ✓ | 已完成 | — | config/cache_l2.go(64MB TTL) | — |
| CFG-007 | settings 审计 trail | 🟡 | 🟡 | ✗ | 已完成 | — | platformsettings/audit_sink + audithttp | — |
| CFG-008 | typed per-key 验证 | ✓ | 🟡 | ✓ | 已完成 | — | ValidateSettings per key(ErrInvalidValue) | — |
| CFG-009 | GitOps 面板自动更新 | ✗ | ✗ | ✓ managementasset/updater | 缺失 | P3 | 无 | 评估(cliproxy 独有) |
| CFG-010 | 注册/邀请/验证码开关族 | ✓ | ✓ | ✗ | 已完成 | — | registration_enabled/invitation_required/captcha_*(7 keys) | — |
| CFG-011 | 2FA/passkey/oauth 开关族 | ✓ | ✓ | ✗ | 已完成 | — | two_factor_enabled/passkey_*(6 keys)/oauth_providers_enabled | — |
| CFG-012 | 流/冷却超时配置 | 🟡 | 🟡 | ✓ | 已完成 | — | stream_timeout/cooldown_429/cooldown_529 | — |
| CFG-013 | response header allow/deny 策略 | ✗ | ✗ | ✓ passthrough | 已完成 | — | response_header_deny_extra/allow_override | — |
| CFG-014 | model fallback chains 配置 | ✗ | 🟡 | ✓ | 已完成 | — | model_fallback_chains(eventbus 热更) | — |
| CFG-015 | budget limits 配置 | ✗ | 🟡 | ✗ | 已完成 | — | budget_limits | — |
| CFG-016 | payment provider config | ✓ | ✓ | ✗ | 部分完成 | P1 | payment_provider_config {manual,taobao};真实 PSP config 窄(P0-PSP-018) | 扩 PSP provider config |
| CFG-017 | checkin/referral 配置族 | ✗ | ✓ | ✗ | 已完成 | — | checkin_enabled/min_cents/max_cents/referral_reward_*(5 keys) | — |
| CFG-018 | mediatask 配置族 | ✗ | 🟡 | ✗ | 已完成 | — | mediatask_enabled/base_url/poll/timeout/estimated(5 keys) | — |
| CFG-019 | smtp_password (write-only) | ✓ | ✓ | ✗ | 已完成 | — | 专用 route(不回读) | — |
| CFG-020 | routing strategy/affinity 配置 | 🟡 | 🟡 | ✓ round-robin/fill-first/sticky | 部分完成 | P2 | 后端有策略;无前端配置(PROTO-019) | 路由策略配置 UI |
| CFG-021 | settings UI (系统设置页) | ✓ | ✓ | ✓ panel | 后端有·前端缺 | P0 | /settings sidebar disabled(见 ADMIN-007) | 系统设置页 |

---

## F. 部署目标 (每目标一行)

证据根: HUAKAI `backend/Dockerfile`(golang:1.25 alpine, dev-grade) + `docker-compose.dev.yml` + `go build ./cmd/gateway`(CGO=0)。四方均无 Helm/K8s。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| DEPLOY-001 | Dockerfile (prod) | ✓(+goreleaser) | ✓ | ✓(+build 脚本) | 部分完成 | P1 | backend/Dockerfile dev-grade(golang:1.25 alpine) | 加固 prod 多阶段镜像 |
| DEPLOY-002 | docker-compose (base prod) | ✓ | ✓ | ✓ | 部分完成 | P1 | 仅 docker-compose.dev.yml(dev only) | 补 prod compose |
| DEPLOY-003 | docker-compose.dev | ✓ | ✓ | ✗ | 已完成 | — | docker-compose.dev.yml | — |
| DEPLOY-004 | docker-compose.standalone/local | ✓(2 变体) | ✗ | ✗ | 缺失 | P3 | 无 | 视需求评估 |
| DEPLOY-005 | docker-compose.cluster | ✗ | ✗ | ✓(HOME_JWT) | 缺失 | P3 | 无 | 集群 compose(cliproxy 独有) |
| DEPLOY-006 | docker-entrypoint / deploy 脚本 | ✓ | ✗ | ✓ | 缺失 | P2 | 无 entrypoint/deploy 脚本 | 补 entrypoint + 部署脚本 |
| DEPLOY-007 | K8s / Helm chart | ✗ | ✗ | ✗ | 缺失 | P2 | 四方皆无 | Helm chart(差异化机会) |
| DEPLOY-008 | 单静态二进制 | ✓ goreleaser | ✓ | ✓ | 已完成 | — | go build ./cmd/gateway(CGO=0) | — |
| DEPLOY-009 | systemd unit | ✓(2 unit) | ✓ | ✗ | 缺失 | P2 | 无 | 补 systemd unit |
| DEPLOY-010 | reverse-proxy 配置 (Caddy/nginx) | ✓ Caddyfile(TLS+cache) | ✗ | ✗ | 缺失 | P2 | 无 | 补反代示例配置 |
| DEPLOY-011 | one-line install 脚本 | ✓(i18n) | ✗ | ✗ | 缺失 | P3 | 无 | 一键安装脚本 |
| DEPLOY-012 | Electron 桌面 app | ✗ | ✓(tray/mac entitlements) | ✗ | 缺失 | P3 | 无 | 视需求(new-api 独有) |
| DEPLOY-013 | TUI binary | ✗ | ✗ | ✓ internal/tui | 缺失 | P3 | 无(见 K 段 MISC-002) | 评估 TUI |
| DEPLOY-014 | Healthcheck (compose) | 🟡 | ✓ /api/status | ✗ | 缺失 | P1 | Phase-8 deferred(细树 G) | 补 compose healthcheck + /health 端点 |
| DEPLOY-015 | Makefile | ✓ | ✓ | ✓ | 已完成 | — | Makefile 存在 | — |
| DEPLOY-016 | DB: PostgreSQL | ✓ | ✓ | n/a | 已完成 | — | pgx | — |
| DEPLOY-017 | DB: MySQL/SQLite (多 DB) | ✗ | ✓(MySQL+SQLite) | n/a | 缺失 | P3 | 仅 PG | 视需求(new-api 三 DB) |
| DEPLOY-018 | DB: Redis | ✓ | ✓ | ✓ | 已完成 | — | eventbus/cache | — |
| DEPLOY-019 | 部署前检查清单 (env/migration/build) | 🟡 | 🟡 | 🟡 | 缺失 | P1 | 10D §4:无必填 env/migration-pending/build 检查脚本 | 部署 preflight 检查脚本 |
| DEPLOY-020 | 迁移前备份 + rollback artifact | ✗ | ✗ | ✗ | 缺失 | P2 | 10D §4:无 backup-before-migration / rollback artifact | 备份 + 回滚产物流程 |

---

## G. Rust 高性能网关 (每组件一行)

证据根: HUAKAI `exploratory/rust-core-gateway`(opt-in,非 prod 默认) + `transport/mimicry/sidecar_client.go`(prod 接线)。仅 HUAKAI 有,三参考全无。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| RUST-001 | Rust TLS-fingerprint sidecar | ✗ | ✗ | ✗ | 部分完成 | P2 | exploratory tls-sidecar(boring/tokio-boring, ja4.rs, h2_bridge) opt-in | 生产化 sidecar(基准 + 灰度) |
| RUST-002 | Rust core gateway engine | ✗ | ✗ | ✗ | 部分完成 | P3 | exploratory core_gateway(proxy_engine/circuit_breaker/stream_pipeline/account_planner) | 评估是否上 prod 路径 |
| RUST-003 | Go→sidecar transport client | ✗ | ✗ | ✗ | 已完成 | — | transport/mimicry/sidecar_client.go(prod 接线, fail-closed) | — |
| RUST-004 | Prod TLS mimicry (Go uTLS) | ✓(Go) | ✗ | ✗ | 已完成 | — | Go uTLS prod 默认;sidecar opt-in via HUAKAI_TRANSPORT_SIDECAR_SOCKET | — |
| RUST-005 | Rust sidecar 基准/性能验证 | ✗ | ✗ | ✗ | 缺失 | P2 | 无公开基准(opt-in 未默认开) | 补吞吐/延迟基准对比 Go uTLS |

---

## H. i18n 逐语言 (每语言/框架一行)

证据根: HUAKAI 后端英文错误串硬编码,前端 zh 硬编码无 i18n 框架。new-api 最强(3 语言 + 后端 ApiErrorI18n)。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| I18N-001 | 后端 i18n 框架 | ✗ | ✓ i18n.go+keys.go | ✓ tui T()/SetLocale | 缺失 | P2 | 英文错误串硬编码 | 引入后端 i18n 框架 |
| I18N-002 | 本地化 API 错误 | ✗ | ✓ ApiErrorI18n | 🟡 TUI only | 缺失 | P2 | 无 | 错误码 i18n 表 |
| I18N-003 | 前端 i18n 框架 | ✓ Vue i18n | ✓ React i18n+switcher | ✓ panel | 缺失 | P1 | zh 硬编码标签,无框架 | 引入前端 i18n + 语言切换器 |
| I18N-004 | English (en) | ✓ | ✓ | ✓ | 部分完成 | P2 | 后端错误串 en | 完整 en locale |
| I18N-005 | 简体中文 (zh-CN) | ✓ | ✓ | ✓ | 部分完成 | P1 | UI 标签 zh 硬编码 | 抽取为 zh-CN locale |
| I18N-006 | 繁体中文 (zh-TW) | ✗ | ✓ | ✗ | 缺失 | P3 | 无 | 视市场评估 zh-TW |
| I18N-007 | 语言切换器 (前端) | ✓ | ✓ language-switcher.tsx | ✓ | 缺失 | P2 | 无 | 语言切换 UI(依赖 I18N-003) |

---

## I. 文档 / API 契约 / 测试 / 发布 (每项一行)

证据根: 10D §7(文档)/§8(测试)/§9(运维 SOP) + 09E §4(文档体系)/§5(API 契约治理)。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| DOC-001 | Quickstart (create key/first request/streaming) | 🟡 | ✓ | ✓ | 缺失 | P1 | 10D §7:无 quickstart | 写 quickstart 文档 |
| DOC-002 | API auth guide + key security guide | 🟡 | ✓ | ✓ | 缺失 | P1 | 10D §7:部分/缺 | auth + key 安全指南 |
| DOC-003 | error code reference 表 | ✗ | 🟡 | ✗ | 缺失 | P2 | 10D §7 / 09E §4:无错误码表 | 错误码参考表(依赖 I18N-002) |
| DOC-004 | billing/subscription/refund/affiliate docs | 🟡 | 🟡 | ✗ | 缺失 | P2 | 10D §7:全缺 | 商业文档族 |
| DOC-005 | admin finance SOP + incident runbook | ✗ | ✗ | ✗ | 缺失 | P2 | 10D §7/§9 + 09E §6:无 | 财务 SOP + 事故 runbook |
| DOC-006 | deployment/migration guide | 🟡 | 🟡 | ✓ | 缺失 | P1 | 09E §4:部分/缺;Linux-only 待核实 | 部署 + 迁移指南 |
| DOC-007 | OpenAPI reference (source of truth) | 🟡 | ✓ | ✓ | 部分完成 | P1 | 09E §5:OpenAPI 部分(183 paths fix 分支) | 确立 OpenAPI 单一真源 |
| DOC-008 | OpenAPI drift test | ✗ | 🟡 | ✗ | 部分完成 | P2 | 09E §5 / 10D §4:部分 | OpenAPI drift CI 检查 |
| DOC-009 | API 版本/废弃/向后兼容策略 | ✗ | 🟡 | ✗ | 缺失 | P3 | 09E §5:无 versioning/deprecation policy | 制定 API 治理策略 |
| DOC-010 | 标准: pagination/idempotency/request-id/rate-limit headers | 🟡 | 🟡 | 🟡 | 部分完成 | P2 | 09E §5:request-id 部分;其余部分/缺 | 统一契约标准文档 |
| DOC-011 | docs/API contract portal (前端) | 🟡 | ✓ | ✓ | 缺失 | P2 | OPS-019:无文档门户 | 文档门户页 |
| DOC-012 | SDK examples + Postman/OpenAPI explorer | ✗ | 🟡 | ✓ SDK | 缺失 | P3 | 09A §4:无 SDK/Postman/explorer | SDK + collection |
| TEST-001 | 注册/登录/2FA/passkey E2E | ✗ | ✗ | ✗ | 缺失 | P1 | 10D §8:全缺 | 认证 E2E 套件 |
| TEST-002 | API key create/use/revoke E2E | ✗ | ✗ | ✗ | 缺失 | P1 | 10D §8 | key 生命周期 E2E |
| TEST-003 | 支付/webhook 入账/退款 E2E | ✗ | ✗ | ✗ | 缺失 | P1 | 10D §8 | 支付闭环 E2E |
| TEST-004 | 订阅/取消续费/返佣/签到 E2E | ✗ | ✗ | ✗ | 缺失 | P2 | 10D §8 | 商业流 E2E |
| TEST-005 | provider failover / streaming 断连 E2E | ✗ | ✗ | ✗ | 缺失 | P2 | 10D §8 | 网关韧性 E2E |
| TEST-006 | 前端 empty/error/loading state 测试 | ✗ | ✗ | ✗ | 缺失 | P2 | 10D §8 | 前端状态测试 |
| TEST-007 | admin RBAC / export permission 测试 | ✗ | ✗ | ✗ | 部分完成 | P2 | 10D §8:需核实 | RBAC + 导出权限测试 |
| TEST-008 | migration rollback 测试 | ✗ | ✗ | ✗ | 缺失 | P2 | 10D §8 | 迁移回滚测试 |
| REL-001 | 发布前 checklist | 🟡 | ✗ | ✗ | 缺失 | P1 | 10D §9 / 09E §4:部分/缺 | 发布前检查清单 |
| REL-002 | 发布后 smoke checklist | ✗ | ✗ | ✗ | 缺失 | P1 | 10D §9 | smoke checklist |
| REL-003 | 事故复盘模板 + 值班升级矩阵 | ✗ | ✗ | ✗ | 缺失 | P2 | 10D §9 / 09E §8:无 | 复盘模板 + escalation matrix |
| REL-004 | 运营流程 SOP 族 (支付未到/退款失败/密钥泄露…) | ✗ | ✗ | ✗ | 缺失 | P2 | 10D §9(11 个 SOP) / 09E §6 | 运营 SOP 文档族 |

---

## J. 运维 / 告警 / 风控 / 数据导出 / 数据保留 (每项一行)

证据根: 10D §1(安全)/§2(风控)/§3(告警)/§5(导出)/§6(保留) + 12D §10 OPS + 骨架 #46/#47。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| OPS-001 | 告警系统 (阈值-based) | 🟡 alert rules | ✗ | ✗ | 缺失 | P2 | 骨架 #46 + 10D §3:无低余额/渠道失败率/配额耗尽/DLQ 深度告警 | 告警规则引擎 |
| OPS-002 | provider 错误率/延迟阈值告警 | 🟡 | ✗ | ✗ | 缺失 | P2 | 10D §3:部分/缺 | provider 健康告警 |
| OPS-003 | DLQ 堆积阈值告警 | 🟡 | ✗ | ✗ | 部分完成 | P2 | 10D §3:DLQ 数据有(ADMIN-031),阈值告警缺 | DLQ 深度阈值告警 |
| OPS-004 | 告警静默/确认/升级 | 🟡 | ✗ | ✗ | 缺失 | P3 | 10D §3 / OPS-002 | 告警生命周期管理 |
| OPS-005 | 数据导出 (用户/订单/退款/账务/用量/审计 CSV) | 🟡 | 🟡 history | ✗ | 缺失 | P1 | 骨架 #47 + 10D §5:无导出端点 | 异步导出任务 + 权限审计 |
| OPS-006 | 财务对账导入/报表 (PSP settlement) | ✗ | ✗ | ✗ | 缺失 | P2 | ADM-023/025 + 10D §5 | 对账导入 + 结算报表 |
| OPS-007 | invoice / chargeback 处理 | 🟡 | 🟡 | ✗ | 缺失 | P2 | ADM-021/022 | invoice + chargeback 流程 |
| OPS-008 | 数据保留/删除策略 (日志/审计/PII/GDPR) | 🟡 | 🟡 | ✗ | 缺失 | P2 | 10D §6:保留天数/PII 删除/注销处理缺 | 保留策略 + PII 删除流程 |
| OPS-009 | 用户风险分 + 风险标签 (手动/自动) | ✓ RiskControl | ✗ | ✗ | 缺失 | P2 | 10D §2 + ADMIN-015 | 风险评分 + 标签系统 |
| OPS-010 | 充值/退款/邀请防刷规则配置 | ✓ | ✗ | ✗ | 缺失 | P2 | 10D §2:同 IP/设备/支付身份检测缺 | 防刷规则引擎 |
| OPS-011 | 临时冻结用户/提现 + 风控审核队列 | 🟡 | ✗ | ✗ | 缺失 | P2 | 10D §2:需核实/缺 | 冻结 + 审核队列 |
| OPS-012 | 财务操作 step-up / production secret 审批 | ✗ | ✗ | ✗ | 缺失 | P2 | 10D §1 + 09E §7:无 step-up/审批流 | 财务/密钥访问审批 |
| OPS-013 | admin session timeout/revoke + IP allowlist | 🟡 | 🟡 | ✗ | 部分完成 | P2 | 10D §1:需核实 | admin 会话/IP 加固 |
| OPS-014 | API key 泄露检测 + 异常用量提醒 | ✗ | ✗ | ✗ | 缺失 | P3 | 10D §1 | key 泄露/异常检测 |
| OPS-015 | 审计日志防篡改验证 UI | ✗ | ✗ | ✗ | 已完成 | — | app/audit page(HopChainTimeline/MerkleProofPanel 信任链) | — HUAKAI 独有 |
| OPS-016 | 系统 version/update/rollback/restart | 🟡 | ✗ | ✗ | 缺失 | P3 | 12D OPS-006:无 | 系统自更新/回滚 |
| OPS-017 | BI dashboard + 商业指标 (MRR/churn/ARPU/LTV) | ✗ | ✗ | ✗ | 缺失 | P3 | 09E §1/§2:无分析层 | 指标定义 + BI 看板 |
| OPS-018 | 产品实验 (feature flags UI/A-B/pricing 实验) | 🟡 | ✗ | ✗ | 部分完成 | P3 | 09E §3:account-modes 只读(ADMIN-039) | 实验框架 |

---

## K. 杂项 (MCP / TUI / mgmt-API / passkey / captcha / model-sync)

证据根: 细树 J 段 + 12D PROTO 段。

| ID | 功能 | sub2api | new-api | cliproxy | HUAKAI状态 | 优先级 | 证据 | 推进动作 |
|---|---|---|---|---|---|---|---|---|
| MISC-001 | MCP gateway server | ✗ | ✗ | ✗ | 缺失 | P3 | 四方皆无 | 评估 MCP 网关(差异化) |
| MISC-002 | TUI admin | ✗ | ✗ | ✓(auth/config/keys/logs/oauth tabs) | 缺失 | P3 | 无 | 评估 TUI(cliproxy 独有) |
| MISC-003 | Mgmt REST API (独立平面) | 🟡 | 🟡 | ✓ /v0/management/* | 已完成 | — | controlhttp/* + /v1/admin/*(platform-settings/routes/disputes/models) | — |
| MISC-004 | CLI admin flags / bootstrap | 🟡 grpc | 🟡 migrate | ✓ -home-jwt/-login | 部分完成 | P3 | cmd/gateway + bootstrap token | 完善 CLI admin 体验 |
| MISC-005 | Passkey / WebAuthn | ✗ | ✓ | ✗ | 已完成 | — | passkey/passkeyhttp + 5 settings(mig0098) | — 后端;前端见 FE-007 |
| MISC-006 | Captcha | ✓ turnstile | ✓ | ✗ | 已完成 | — | captcha pkg + captcha_provider/site_key | — |
| MISC-007 | Model catalog auto-sync | ✓ | ✓ | ✓ | 已完成 | — | modelsync/scheduler(anthropic/openai/gemini) | — |
| MISC-008 | auth-file import/export/download (CLI 凭证) | ✗ | ✗ | ✓ | 缺失 | P3 | PROTO-013 / AI-006/007:无 | 评估 auth-file 管理 |
| MISC-009 | Amp provider aliases + management proxy | ✗ | ✗ | ✓ | 缺失 | P3 | PROTO-010/011 | 评估 Amp 集成 |
| MISC-010 | reusable Go SDK | ✗ | ✗ | ✓ | 缺失 | P3 | PROTO-015 | 评估官方 Go SDK |
| MISC-011 | realtime websocket | ✗ | ✓ | ✓ | 缺失 | P2 | PROTO-004:/v1/realtime 返回 realtime_not_available | realtime WS 实现 |
| MISC-012 | Mobile/Desktop app | ✗ | ✓ electron | ✗ | 缺失 | P3 | 无(见 DEPLOY-012) | 视需求 |

---

## 合并方法学 & 关键发现

**去重规则**: 三源同一功能合并为一行,证据/优先级取最具体者;骨架行号(#N)、细树段(A-J)、151 ID(P0-U/PSP/SUB/AFF/ADM/AI/PROTO/OPS)交叉引用写入证据列。`08-keys-notify-frontend-deploy.md`(151 文件名)实际不存在于 151-ref/,等价内容由 12D/10C/10D/09A/09E 提供——已全部并入。

**六级状态分布(去重后)**: 大量 `后端有·前端缺`(用户商业前端 + 后台运营前端两条断层) + `缺失`(告警/风控/导出/i18n/文档/测试/SOP/真实 PSP)。HUAKAI 独有强项: KEY-022 environment(live/test)、KEY-021 active-key-cap、KEY-031 子资源端点粒度、NOTIF-018/019/023 通知三重硬化(SSRF/CRLF/scrub)、OPS-015 信任链审计 UI、RUST-* Rust sidecar、MISC-005 passkey。

**三条核心断层(对标 sub2api/new-api/cliproxy)**:
1. **前端闭环断层**(P0): 9 个运维页存在,但 login/register/profile/security/sessions/api-keys/usage/wallet/orders/subscriptions/affiliate/redeem/checkin/notifications 全缺;accounts/settings sidebar disabled。后端大量已就绪,差 UI。
2. **运营成熟度断层**(P1-P2): 告警系统、风控、数据导出、对账、财务 SOP、E2E 测试、发布 checklist、i18n 框架全缺。
3. **配置/部署/生态断层**(P1-P3): 无 prod Dockerfile/compose、无 K8s/Helm/systemd/healthcheck、无 YAML 配置层、无 TUI/SDK/Amp/realtime(cliproxy 生态)。


# ==================== 附录: P0 立即推进待办 ====================
（从全模块抽取 HUAKAI状态≠已完成 且 优先级=P0 的行）

| 模块 | 行 |
|---|---|
| A |> 校准证据: 12E §7/§8(auth/2FA/passkey/key 路由后端存在) + 12E §2/§3(前端页面树无、Sidebar disabled) + 06 §G/§H(端点状态) + 12D P0-U 行(前端缺=P0)。
| A || AUTH-006 | 注册页 (前端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端 `POST /v1/auth/register`(auth_handler.go:148);12E §2 无 register 页 | 新建 `frontend/app/register/page.tsx` 接 register API |
| A || AUTH-040 | 邮箱验证页 (前端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端 `POST /v1/auth/verify-email`(auth_handler.go:151);P0-U-004 | 新建邮箱验证落地页 |
| A || AUTH-048 | 忘记/重置密码端点 (非鉴权) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | RequestPasswordReset/ResetPassword;`POST /v1/auth/reset-password`(auth_handler.go:152);P0-U-003 | 补忘记/重置密码前端页 |
| A || AUTH-073 | OAuth callback 前端页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端 `POST /v1/auth/oauth-callback`(auth_handler.go:154);P0-U-005 | 建 `/oauth/[provider]` 回调页 |
| A || AUTH-090 | 注册 begin/finish (后端) | ✗ | ✓ | ✗ | 后端有·前端缺 | P0 | `/v1/me/passkeys/register/begin|finish`(06 §G,targeted test);无安全设置页 | 接前端 passkey 注册流 |
| A || AUTH-091 | 可发现登录 begin/finish (后端) | ✗ | ✓ | ✗ | 后端有·前端缺 | P0 | BeginDiscoverableLogin(webauthn.go:68);P0-U-007 | 登录页加 passkey 按钮 |
| A || AUTH-092 | 列出 / 删除凭证 (后端) | ✗ | ✓ | ✗ | 后端有·前端缺 | P0 | GET / + DELETE /{id}(06 §G) | 安全设置页列/删 passkey |
| A || AUTH-105 | 登录态 2FA 挑战 token (HMAC 签名) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | StartLoginChallenge;`POST /v1/auth/login/2fa`(auth_handler.go:150);P0-U-006 | 建 2FA 登录挑战页 |
| A || AUTH-139 | session refresh/list/revoke 端点 (后端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `/v1/sessions/{refresh,list,revoke}`(session_handler.go:40-45);P0-U-010 | 建 `/profile/sessions` 设备管理页 |
| A || AUTH-141 | session me 端点 (who am I) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `GET /v1/auth/me`(panelauthhttp);P0-U-008 资料页 | 建 `/profile` 资料页接 /auth/me |
| A || AUTH-144 | 登录页 (前端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端 `POST /v1/auth/login`(auth_handler.go:149);P0-U-001 | 新建 `frontend/app/login/page.tsx` |
| A || AUTH-145 | 安全设置页 (聚合 2FA/passkey/改密/会话) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 各后端就绪;无 `/profile/security`(12E §2;P0-U-009) | 建安全设置页聚合 AUTH-052/106/092/139 |
| A || AUTH-146 | 用户自助 API key 签发 (后端) | ✓ | ✓ | ✓(static AccessProvider) | 后端有·前端缺 | P0 | `POST /v1/api-keys`(userkeyhttp/handlers.go:41);明文仅一次;P0-U-012 | 建 API key 创建/复制 modal |
| A || AUTH-147 | API key 列表 (后端) | ✓ | ✓ | 🟡 | 后端有·前端缺 | P0 | `GET /v1/api-keys`(handlers.go:42);Sidebar `/api-keys` disabled(12E §3);P0-U-011 | 上架 API key 列表页 + 启用导航 |
| A || AUTH-148 | API key 撤销 (用户/管理员) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `DELETE /v1/api-keys/{id}`(handlers.go:44);admin DELETE;P0-U-013 | 列表页接撤销动作 |
| A || AUTH-153 | per-key 配额设置/读取 (后端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `PUT/GET /v1/api-keys/{id}/quota`(userkeycontrolshttp/mount.go:25);P0-U-014 | key 设置页接 quota(纠正骨架旧"MISSING") |
| A || AUTH-154 | per-key group 设置/读取 (后端) | ✓ | 🟡 | ✗ | 后端有·前端缺 | P0 | `PUT/GET /v1/api-keys/{id}/group`(mount.go:27);P0-U-015 | key 设置页接 group |
| A || AUTH-155 | per-key IP allowlist 设置/读取 (后端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `PUT/GET /v1/api-keys/{id}/ip-allowlist`(mount.go:29);P0-U-016 | key 设置页接 IP allowlist(纠正骨架旧"MISSING") |
| A |**P0 (商业闭环必补 — 12D P0-U;全部为"后端有·前端缺"):**
| C || CRED-288 | 计划性自动 API-key 轮转(scheduled) | ✓operator 流 | ✓operator 流 | ✗ | 缺失 | P0 | `needs_rotation` state 存在(types.go:52)但**无后台 job 处理=死信号**;scheduler 仅处理 OAuth refresh(骨架 F-02) | **补 rotation_schedule/rotation_due_at + 后台轮转 job**(P0:90d/1y key TTL,否则静默宕机) |
| C |2. **P0 真缺口(直接商业/可用性阻塞)**:
| D || ROUTE-121 | 本地预检避免上游 429 (local pre-check) | ✓(ratelimit_service PreCheckUsage Gemini RPM minute window) | ✗ | ✗ | 缺失 | P0 | grep `rpm_limit` in pool/ → 0; `gates.go:210-229 modelRateLimitGate` 仅查 RateLimitResetAt(429 后写),无主动滑动窗计数 | **最高价值**: 实现主动 RPM/TPM 预算滑动窗计数器,打满限额前绕开,每次 429 = 已失败请求+延迟 |
| D |## 优先级汇总 (HUAKAI 缺口 ranked, 仅列 P0-P2)
| D || **P0** | ROUTE-121 | 主动 RPM/TPM 预算追踪 (local pre-check) | 缺失 | **最高**: 无滑动窗计数,限额前无法预防性绕开;每次 429=用户可感知错误/延迟。sub2api PreCheckUsage / new-api channel_limit rpm 计数器为标杆 |
| D |- HUAKAI **核心缺口集中在**: (1) 主动限额预检 (ROUTE-121, P0); (2) 权重/能力/上下文窗口门控未实际生效 (ROUTE-002/024/023, P1); (3) 正向偏好路由 (latency/cost, ROUTE-028/029, P2); (4) 边缘 IP 限流 (ROUTE-096, P1); (5) 全套路由/健康 admin 前端 UI (后端有·前端缺, P2)。
| E |> **优先级** P0-P3 / —。HUAKAI 目标库 = `origin/fix/hermes-phase-1-e33d940`(183 paths;main 仅 83 paths,带 ⚠未合并main 标注)。
| E || BILL-121 | **quota 强制接入 gateway 热路径** | ✓ | ✓ | ✗ | 缺失 | P0 | grep: 零非测试文件 import internal/quota;claim_gate 仅 import balancehold 非 quota → window/concurrency/token quota **运行时零效果** | **最高优先真缺口**:在 claim_gate/dispatch 调用 quota.Service.Reserve() |
| E |- **P0 BILL-121**: quota 引擎已全实现+集成测试但零生产代码 import → window/concurrency/token quota 运行时零效果。最高优先,接入 claim_gate/dispatch 热路径后 BILL-123/124 随之生效。
| F |> **优先级**: P0 (商业闭环必须) / P1 (成熟后台) / P2 (生态对标) / P3 (合规/长尾) — P0 标注取自 12D.
| F || PAY-002 | manual provider (管理员手动确认) | 🟡 | ✓(AdminCompleteTopUp) | ✗ | 部分完成 | P0 | `payment/provider.go` manualProvider 生产可用, 不碰商户密钥 | 补用户端人工支付指引 UI |
| F || PAY-003 | test provider (本地 HMAC 签名回调) | 🟡 | 🟡 | ✗ | 部分完成 | P0 | `payment/provider.go` testProvider + SignTestCallback (默认密钥, 仅测试) | 生产不可当真实支付 |
| F || PAY-004 | HMAC bridge provider (HTTP-HMAC 桥, 可桥 epay 风格回调) | ✗ | ✗ | ✗ | 部分完成 | P0 | `payment/provider.go` hmacProvider + `paymenthttp/provider_hmac.go` 常量时间验签 | 桥接框架, 非具体 PSP |
| F || PAY-005 | Taobao/闲鱼 manual-redirect provider (checkout_url 人工对账) | ✗ | ✗ | ✗ | 领先 | P0 | `payment/provider.go` taobaoProvider, 无商户密钥 — HUAKAI UNIQUE | — |
| F || PAY-006 | Stripe adapter (checkout/intent + webhook + refund/cancel/query) | ✓(provider/stripe.go TypeStripe/Card/Link) | ✓(payment_stripe.go ApiSecret/WebhookSecret/PriceId/UnitPrice=8.0/MinTopUp=1/PromotionCodes) | ✗ | 缺失 | P0 | `payment/PROVIDERS.md:4` 暂不实现, 只保留框架 | 等商户资质后接 Stripe SDK |
| F || PAY-007 | Alipay (支付宝) adapter | ✓(provider/alipay.go TypeAlipay/AlipayDirect) | 🟡(经 epay 间接) | ✗ | 缺失 | P0 | `PROVIDERS.md` 暂不实现 | CN 市场必备, 待落地 |
| F || PAY-008 | WeChat Pay (微信) adapter (含 certSerial) | ✓(provider/wxpay.go TypeWxpay/WxpayDirect) | 🟡(经 epay) | ✗ | 缺失 | P0 | `PROVIDERS.md` 暂不实现 | CN 市场必备, 待落地 |
| F || PAY-009 | EasyPay/EPay (易支付聚合) adapter | ✓(provider/easypay.go TypeEasyPay) | ✓(epay.go EpayId/EpayKey/PayAddress/PayMethods; RequestEpay/EpayNotify) | ✗ | 缺失 | P0 | `PROVIDERS.md` 暂不实现 | CN 聚合渠道, 待落地 |
| F || PAY-010 | Airwallex adapter | ✓(provider/airwallex.go TypeAirwallex) | ✗ | ✗ | 缺失 | P0 | `PROVIDERS.md` 暂不实现 | 全球收单, 待落地 |
| F || PAY-015 | 自动 webhook 回调入账路径 (P2a) | ✓ | ✓ | ✗ | 部分完成 | P0 | `payment/webhook.go` ConfirmPaidByCallback (HMAC-SHA256); 仅 test/hmac, 真 PSP 回调未接 | 真 PSP adapter 落地后接通 |
| F || PAY-019 | provider instance table/entity (多商户) | ✓(migration 096_payment_provider_instances + ent paymentproviderinstance) | 🟡(全局开关) | ✗ | 缺失 | P0 | 未见 instance table; 仅 KeyPaymentProviderConfig JSON{manual,taobao} | 建 payment_provider_instances 表 |
| F || PAY-020 | instance create/list/get/update/delete (CRUD) | ✓ | 🟡 | ✗ | 缺失 | P0 | `paymenthttp/handler.go:190-191` 仅 GET/PUT config, 无 CRUD | 建 admin instance CRUD 路由 |
| F || PAY-021 | instance enable/disable | ✓(enabled) | 🟡 | ✗ | 缺失 | P0 | 无 instance enable | — |
| F || PAY-022 | instance encrypted config payload | ✓(config + supported_types) | 🟡 | ✗ | 缺失 | P0 | 无加密 config payload | 接真 PSP 密钥时必备 |
| F || PAY-032 | 余额来源: 派生 SUM 不可变事件 (无可变余额列) | ✗(可变列) | ✗(可变列) | ✗ | 领先 | P0 | 余额由 billing_events(payment_credited) 派生 SUM; migration 0039 append-only — HUAKAI UNIQUE | — |
| F || PAY-044 | 创建充值订单 (用户自助) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `paymenthttp/handler.go:206` POST /orders; CreateOrder() | 建充值页 |
| F || PAY-060 | 用户订单列表 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `handler.go:205` GET /orders; ListOrders() | 建订单中心页 |
| F || PAY-061 | 用户订单详情 / 状态轮询 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `handler.go:207` GET /orders/{id} | 建详情/轮询页 |
| F || PAY-062 | 用户取消订单 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `handler.go:208` POST /orders/{id}/cancel; 真 PSP cancel 缺 | 建取消按钮 |
| F || PAY-063 | 用户余额查询 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `handler.go:210` GET /balance | 建钱包页 |
| F || PAY-064 | 用户支付配置 (config) | ✓(config/checkout-info/plans/channels/limits) | ✓(topup info/self) | ✗ | 后端有·前端缺 | P0 | `handler.go:211` GET /config (manual/taobao, 更窄) | 建充值页 |
| F || PAY-065 | 后台确认支付 (manual 路径) | 🟡(admin 改余额) | ✓(AdminCompleteTopUp) | ✗ | 后端有·前端缺 | P0 | `handler.go:198` POST /{id}/confirm; AdminConfirmPaid() | 建后台订单页 |
| F || PAY-068 | 后台订单列表 + 筛选 (tenant/status/date/user) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `handler.go:188` GET / | 建后台订单页+筛选 |
| F || PAY-069 | 后台 payment dashboard | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `handler.go:189` GET /dashboard | 建后台 dashboard 页 |
| F || PAY-082 | 用户自助退款申请 + 后台审批 | ✓(refund_requested_at/reason/by; allow_user_refund) | ✗ | ✗ | 后端有·前端缺 | P0 | `handler.go:209` POST /orders/{id}/refund-request; `refund_request_admin.go`; migration 0096; 用户非直接退款 | 建退款申请按钮 |
| F || PAY-083 | 后台退款申请列表 + approve/reject | ✓ | ✗ | ✗ | 后端有·前端缺 | P0 | `handler.go:192-194` GET /refund-requests + approve + reject | 建退款队列 UI |
| F || PAY-084 | 后台直接退款 (admin refund) | ✓ | 🟡 | ✗ | 后端有·前端缺 | P0 | `handler.go:200` POST /{id}/refund; 真 PSP refund 取决于 adapter | 建退款按钮 |
| F || PAY-085 | provider refund 调用 (真 PSP) | ✓(payment_refund.go provider.Refund) | 🟡 | ✗ | 缺失 | P0 | `provider.go` Refund(); manual/taobao ErrRefundUnsupportedKind; 真 PSP adapter 缺 | adapter 落地后接通 |
| F || PAY-086 | 真 PSP query / cancel | ✓ | ✓ | ✗ | 缺失 | P0 | 框架有 QueryOrder/Cancel iface, 真 adapter 缺 | adapter 落地后接通 |
| F || PAY-095 | 钱包页 | 🟡 | ✓(/wallet) | ✗ | 后端有·前端缺 | P0 | balance API 有, 无 wallet 页 | 建 /wallet |
| F || PAY-096 | 充值/购买页 | ✓(PaymentView) | ✓(TopUp) | ✗ | 后端有·前端缺 | P0 | createOrder API 有, 无页面 | 建充值页 |
| F || PAY-097 | 金额输入 + 限额提示 | ✓(AmountInput) | ✓ | ✗ | 后端有·前端缺 | P0 | 后端限额有, 前端缺 | — |
| F || PAY-098 | 支付方式选择器 | ✓(PaymentMethodSelector) | ✓ | ✗ | 缺失 | P0 | 前端缺 | — |
| F || PAY-099 | 支付 provider 卡片/列表 | ✓(ProviderCard/List/Dialog) | ✓ | ✗ | 缺失 | P0 | 前端缺 | — |
| F || PAY-100 | 人工支付指引 (manual/taobao) | 🟡 | ✗ | ✗ | 后端有·前端缺 | P0 | 后端有 instruction; 适合先补 | 建人工支付页 |
| F || PAY-101 | 订单列表页 | ✓(UserOrdersView/OrderTable/StatusBadge) | ✓ | ✗ | 后端有·前端缺 | P0 | 后端 list 有 | 建订单中心 |
| F || PAY-102 | 订单详情 / 轮询页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端 detail 有 | — |
| F || PAY-103 | 支付结果页 | ✓(PaymentResultView) | ✓ | ✗ | 缺失 | P0 | 前端缺 | — |
| F || PAY-107 | 退款申请按钮 (用户) | ✓ | ✗ | ✗ | 后端有·前端缺 | P0 | 后端 refund-request 有 | — |
| F || PAY-111 | 后台支付订单页 | ✓(AdminOrdersView) | ✓(features/wallet) | ✗ | 后端有·前端缺 | P0 | paymenthttp 后端有, 无 page | 建后台订单页 |
| F || PAY-112 | 后台支付 dashboard 页 | ✓(AdminPaymentDashboardView) | ✓ | ✗ | 后端有·前端缺 | P0 | dashboard API 有 | 建 dashboard 页 |
| F || PAY-115 | 后台 confirm/cancel/refund/retry UI | ✓ | 🟡 | ✗ | 后端有·前端缺 | P0 | 后端动作齐全 | — |
| F || PAY-116 | 后台 refund-request approve/reject UI | ✓ | ✗ | ✗ | 后端有·前端缺 | P0 | 后端 approve/reject 有 | — |
| F || PAY-118 | 后台 PSP instance 页 | ✓(provider instances CRUD) | ✗ | ✗ | 缺失 | P0 | instance CRUD 后端缺 | 先补后端 (见 §2) |
| F || SUB-024 | 用户可购套餐列表 | ✓ | ✓(plans/self/preference) | ✗ | 后端有·前端缺 | P0 | `subscriptionhttp/handler.go:257` GET /plans | 建订阅页 |
| F || SUB-025 | 用户当前订阅状态 (/me) | ✓(active) | ✓ | ✗ | 后端有·前端缺 | P0 | `handler.go:256` GET /me | — |
| F || SUB-026 | 用户订阅列表 | ✓(list) | ✓ | ✗ | 后端有·前端缺 | P0 | `handler.go:255` GET / | — |
| F || SUB-027 | 用户自助购买订阅 | ✓ | ✓(balance/epay/stripe/creem pay) | ✗ | 后端有·前端缺 | P0 | `handler.go:259` POST /purchase | 建购买流 |
| F || SUB-028 | 余额购买订阅 | ✓ | ✓(balance pay) | ✗ | 后端有·前端缺 | P0 | 走 payment order; 前端缺 | — |
| F || SUB-030 | 用户取消自动续订 | 🟡 | ✗ | ✗ | 后端有·前端缺 | P0 | `handler.go:258` POST /cancel-renew | — |
| F || SUB-033 | 套餐卡片 UI | ✓(SubscriptionPlanCard) | ✓ | ✗ | 缺失 | P0 | 前端缺 | — |
| F || SUB-034 | 用户订阅页 (UI) | ✓(SubscriptionsView) | ✓(features/subscriptions) | ✗ | 后端有·前端缺 | P0 | renew/page.tsx 部分; 无完整订阅页 | 建 /subscriptions |
| F || VCH-020 | 用户兑换 (redeem) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | `voucher_handler.go:85` POST /redeem; /v1/users/me/vouchers/redeem | 建兑换页 |
| F || VCH-025 | 用户兑换页 (UI) | ✓(RedeemView) | ✓(redemption-codes) | ✗ | 后端有·前端缺 | P0 | 后端有, 无 page | 建 /redeem |
| G |> - 优先级: P0–P3 / — (— = HUAKAI 已强于参照, 无推进动作 / 仅监控)。
| G || SEC-182 | per-key IP/CIDR allowlist 在 auth 强制 | ✗ | ✓ token.go AllowIps; auth.go IsIpInCIDRList | ✗ | 后端有·前端缺 | P2 | apikeyipallow/allowlist.go AllowsCSV; migration 0085; 151 H 段 /v1/api-keys/{id}/ip-allowlist; 12D P0-U-016 | 单 key IP allowlist 前端 |
| G || SEC-249 | per-API-key RPM(requests/min)限流 | ✓ | ✓ | ✗ | 缺失 | P0 | 大树 B-09; IP-based only, 单 key 可换 IP 绕过 | rate-limit store keyed on api_key_id |
| G || SEC-250 | per-API-key TPM(tokens/min)限流 | ✓ | ✓ | ✗ | 缺失 | P0 | 大树 B-10; token 计数仅计费不限流 | quota-service per-key token-minute |
| G || SEC-251 | per-API-key 并发请求 cap | 🟡 | ✓ | ✗ | 部分完成 | P0 | quota/service.go:52 NeedConcurrencySlot; pg_store.go:355 AcquireConcurrencySlot; quota_concurrency_slots 表 — NeedConcurrencySlot 从不置 true(dormant) | 在 gatewayhttp handler 置 NeedConcurrencySlot=true 激活 |
| H || KEY-032 | API key 列表页 (前端) | ✓ KeysView | ✓ features/keys | ✗ | 后端有·前端缺 | P0 | userkeyhttp 后端;/api-keys sidebar disabled:true 无 page.tsx | 新建 frontend/app/api-keys/page.tsx |
| H || KEY-033 | key 创建/显示/复制 modal (前端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端 create 返回 plaintext;无 UI | key 创建 modal + 一次性 copy |
| H || KEY-034 | key 删除/撤销 (前端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端 DELETE;无 UI | key action 按钮 |
| H || KEY-035 | 单 key quota/group/IP-allowlist 配置 (前端) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 三子资源后端有;无 UI | key settings 面板 |
| H || ADMIN-002 | Accounts/Channels (AI provider) | ✓ | ✓ | ✓ mgmt | 部分完成 | P0 | app/accounts/page.tsx 存在但 sidebar disabled:true | 启用导航 + 补完整 CRUD/test/health UI |
| H || ADMIN-004 | Users (用户管理) | ✓ UsersView | ✓ features/users | ✗ | 后端有·前端缺 | P0 | adminhttp 后端;无 /users 页;且 admin 用户列表/CRUD 路由也缺(骨架 #8-12, ADM-011/012) | 后端补 admin 用户 CRUD + 用户管理页 |
| H || ADMIN-007 | Settings (系统设置) | ✓ | ✓ | ✓ mgmt | 后端有·前端缺 | P0 | platformsettings 后端;/settings sidebar disabled | 系统设置页(model/billing/auth 分页) |
| H |证据根: HUAKAI `frontend/app` 9 页 + 12D §0.1 缺失目录清单 + 12D §2 P0-U 行。
| H || FE-001 | 登录页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | auth_handler 后端;无 login 目录(12D §0.1, P0-U-001) | frontend/app/login/page.tsx |
| H || FE-002 | 注册页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | POST /v1/auth/register;无 register 页(P0-U-002) | frontend/app/register/page.tsx |
| H || FE-003 | 忘记/重置密码页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端有;无页(P0-U-003) | auth flow 页 |
| H || FE-004 | 邮箱验证页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端有;无页(P0-U-004) | email verify 页 |
| H || FE-005 | OAuth callback 前端 | ✓ | ✓ | ✓ mgmt | 后端有·前端缺 | P0 | 后端有;无 /oauth/[provider](P0-U-005) | oauth callback 页 |
| H || FE-006 | 2FA 登录挑战页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 后端有;无 login 2FA step(P0-U-006) | login 2FA 步骤 |
| H || FE-007 | Passkey 登录按钮/流程 | ✗ | ✓ | ✗ | 后端有·前端缺 | P0 | passkeyhttp 后端;无 UI(P0-U-007) | login/profile passkey 流程 |
| H || FE-008 | 用户资料页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | /v1/auth/me;无 /profile(P0-U-008) | frontend/app/profile/page.tsx |
| H || FE-009 | 安全设置页 (2FA/passkey) | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | 2FA/passkey 后端;无页(P0-U-009) | /profile/security |
| H || FE-010 | session/设备管理页 | ✗ | ✓ | ✓ mgmt | 后端有·前端缺 | P1 | session_handler 后端;无页(P0-U-010) | /profile/sessions |
| H || FE-011 | 用户 dashboard 商业版 | ✓ | ✓ | 🟡 TUI | 部分完成 | P0 | 当前 dashboard 是运维面非商业面(P0-U-020) | 拆分用户/管理员 dashboard |
| H || FE-012 | 用户用量首页 | ✓ | ✓ | ✓ mgmt | 后端有·前端缺 | P0 | meusagehttp;/usage sidebar disabled 无 page(P0-U-017) | frontend/app/usage/page.tsx |
| H || FE-013 | 用量趋势/图表 | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | time-series 后端;无图表(P0-U-018) | usage charts |
| H || FE-014 | 模型/key 维度用量筛选 | ✓ | ✓ | ✗ | 后端有·前端缺 | P1 | 部分后端;无 UI(P0-U-019, 10C §3) | usage filters |
| H || FE-015 | 钱包页 | ✗ | ✓ /wallet | ✗ | 后端有·前端缺 | P0 | 后端有支付 API;无 wallet 目录(P0-P-001) | frontend/app/wallet/page.tsx |
| H || FE-016 | 充值/购买页 + 金额/方式选择器 | ✓ | ✓ TopUp | ✗ | 后端有·前端缺 | P0 | paymenthttp;无页(P0-P-002~005) | 充值开单页 |
| H || FE-017 | 订单列表/详情/轮询页 | ✓ | ✓ | ✗ | 后端有·前端缺 | P0 | list/detail 后端;无页(P0-P-007/008) | orders 页 |
| H || FE-018 | 取消订单 + 退款申请按钮 | ✓ | 🟡 | ✗ | 后端有·前端缺 | P0 | cancel/refund-request 后端;无 UI(P0-P-009/010) | 订单操作按钮 |
| H || FE-019 | 支付结果/QR/Stripe/Airwallex 页 | ✓ | ✓ | ✗ | 缺失 | P1 | 真实 PSP adapter 明确暂不实现(P0-P-011~017);前端也缺 | 视商户号决定;先补结果页 |
| H || CFG-016 | payment provider config | ✓ | ✓ | ✗ | 部分完成 | P1 | payment_provider_config {manual,taobao};真实 PSP config 窄(P0-PSP-018) | 扩 PSP provider config |
| H || CFG-021 | settings UI (系统设置页) | ✓ | ✓ | ✓ panel | 后端有·前端缺 | P0 | /settings sidebar disabled(见 ADMIN-007) | 系统设置页 |
| H |**去重规则**: 三源同一功能合并为一行,证据/优先级取最具体者;骨架行号(#N)、细树段(A-J)、151 ID(P0-U/PSP/SUB/AFF/ADM/AI/PROTO/OPS)交叉引用写入证据列。`08-keys-notify-frontend-deploy.md`(151 文件名)实际不存在于 151-ref/,等价内容由 12D/10C/10D/09A/09E 提供——已全部并入。
| H |1. **前端闭环断层**(P0): 9 个运维页存在,但 login/register/profile/security/sessions/api-keys/usage/wallet/orders/subscriptions/affiliate/redeem/checkin/notifications 全缺;accounts/settings sidebar disabled。后端大量已就绪,差 UI。
