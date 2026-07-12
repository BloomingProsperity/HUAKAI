# HUAKAI 前端逐页提示词（for Claude Design）

> **用途。** 这是 HUAKAI Web 前端**逐页、填好实参、可直接粘贴**给 Claude Design 的提示词集。
> 每一页一个提示词块,里面的 endpoint / 请求字段 / 响应字段 / 错误码 / 分页参数**全部实读自**
> `docs/openapi/openapi.yaml`(16511 行,41 tag)——不是模板占位,是真实契约。视觉/UX 设计由 Claude Design 负责。
>
> **配套文档**:跨切面规则(auth 模型 §3 / 错误码→toast §5.1 / 钱 micro-USD §5.2 / streaming §5.3 /
> 分页 §5.4 / 幂等 §5.5 / i18n §5.6 / 乐观更新禁区 §5.7 / PSP-stub §6)见 `FRONTEND-BUILD-SPEC.md`。
> 本文档每页提示词只**引用**规则编号,不重复粘贴。
>
> **铁律**:后端是真相源,UI 适配 API,绝不反向。所有类型来自 `lib/api/schema.d.ts`
> (由 `openapi-typescript` 从 `docs/openapi/openapi.yaml` 生成),**禁止手写任何 request/response shape**。
>
> **怎么用**:按 §构建顺序(S1→S8)逐页推进,每页把对应提示词块整段粘给 Claude Design,它产出
> 页面组件 + `lib/api/<area>.ts` typed 调用 + 正确 `app/(group)/` 路由段。一页一 PR,各自可发布。
>
> 生成方式:8 路并行 agent 实读 openapi 真实契约(本轮 ~120 万 token);后端 landing `fix/h-fixes`。

---

## 构建顺序（vertical slices,每片可发布）

1. **S1** 认证与账户外壳(login/register/account + app 外壳 + 三 guard)
2. **S2** 用户核心(dashboard/keys/playground/usage)
3. **S3** 钱循环(billing/subscriptions/referrals/checkin)
4. **S4** 用户审计/通知 + 公开站(notifications/audit/pricing/rankings/trust)
5. **S5** Admin 运营核心1(providers/channels/accounts)
6. **S6** Admin 运营核心2(pools/credentials/model-sync/cache)
7. **S7** Admin 钱/审计(billing/pricing/payments/audit/dlq/alerting)
8. **S8** Admin 平台(cockpit/users/keys/moderation/notifications/settings)

---

## S1 — 认证与账户外壳（auth · user-passkeys · sessions）

> 切片范围：`/login`、`/register`、`/account`（profile / password / passkey / 2FA / sessions）、app 外壳（role-aware nav + 三 guard）。
> 真相源：`docs/openapi/openapi.yaml` 的 `auth`、`user-passkeys`、`sessions` 三个 tag（行 3450–4380、schema 行 10805–11320）。
> 跨切面规则引用 `FRONTEND-BUILD-SPEC.md` §3（auth 模型）、§5.1（错误码→toast）、§5.6（i18n）、§5.7（money/admin 不乐观更新）。
>
> **本切片关键事实（贯穿四页）**
> - 多租户：`/v1/auth/*` 的 public 端点（register/login/login-2fa/passkey-login/verify-email/reset-password）请求体**必须带 `tenant_id`（int64）**——不是从 session 推断。前端需有一个租户解析策略（子域名 / 环境变量 / 隐藏字段），见各页 ⚠ 标注。
> - 登录后端发的是 **session token bundle**（`SessionTokenBundle`：`session_token` / `refresh_token` / `session_expires_at` / `refresh_expires_at` / `family` / `generation`），不是 `hk_` API key。portal 管理类调用用 `sessionBearerAuth`（`Authorization: Bearer <session_token>`）；playground 推理才用 `hk_` key（§3，本切片不涉及）。
> - 401 登录失败被后端**统一折叠成单一 `invalid_credentials`**（账户枚举安全），UI 不得区分"邮箱不存在/密码错/被锁"——一律同一句文案。
> - 错误响应统一 `ErrorResponse`：`{ error: { code, message, request_id?, retry_after_seconds?, details? } }`。429/503 带 `Retry-After` 头与 `retry_after_seconds`。永远用 `error.code` 映射本地化文案，禁止直出 `message` 原文或 500 body（§5.1）。

---

### /register

> **目标 / 受众 / guard**：HUAKAI 平台用户自助注册页。受众 **public**，guard = `public`（`app/(public)/register`，已登录则重定向到 `/dashboard`）。支持邮箱+密码注册、可选邀请码、注册后进入"查收验证邮件"态。
>
> **Endpoints**（只列 openapi 确有的）
> - `POST /v1/auth/register`（operationId `authRegister`，`security: []`）——创建用户。请求体 `AuthRegisterRequest`：必填 `tenant_id`(int64)、`email`(email)、`password`(string, **minLength 8**)；可选 `display_name`、`invite_code`。201 返回 `AuthUserResponse`：`user`(AuthUser)、`verification_required`(bool)、`email_verified`(bool)、`verification_token`(**仅 dev 模式 `HUAKAI_DEV_AUTH_RETURN_TOKEN=true` 才有，生产无**——UI 不要依赖它，正常走邮件)。
> - `POST /v1/auth/validate-invitation-code`（operationId `authValidateInvitationCode`，`security: []`）——在不消耗的前提下校验邀请码，用于注册表单的实时校验。请求体 `AuthValidateInvitationCodeRequest`：`tenant_id`、`invite_code`（minLength 1）。200 返回 `AuthValidateInvitationCodeResponse`：`valid`(bool)、`reason`(enum: `disabled` / `not_found` / `used_or_exhausted` / `expired` / `valid`)。注意 `reason=disabled` 且 `valid=true` 表示平台关闭了"邀请码必填"——此时邀请码字段应隐藏/置为可选。
> - `POST /v1/auth/verify-email`（operationId `authVerifyEmail`，`security: []`）——消费邮件里的验证 token。请求体 `AuthVerifyEmailRequest`：`tenant_id`、`token`。200 返回 `AuthUserResponse`（激活后）。建议在 `/register` 下挂一个 `verify-email?token=...` 落地子路由处理邮件回链。
>
> **States**
> - loading：提交按钮 spinner，禁用重复提交。
> - empty：N/A（表单页）。
> - error（本页真实错误码，按 §5.1 映射本地化 toast/inline）：
>   - `400 BadRequest`（密码 <8 位、邮箱格式、字段缺失）→ 内联字段级错误。
>   - `403 Forbidden`（注册被平台关闭 / 邀请码必填但未通过 / tenant 禁注册）→ 明确文案"当前不开放注册"。
>   - `503 ServiceBusy`（带 `Retry-After`）→ "服务繁忙，请稍后再试"。
>   - `429 RateLimited`（仅 validate-invitation-code 有）→ 按 `Retry-After` 退避。
> - success：切到"已发送验证邮件到 <email>，请查收"态（依据 `verification_required`/`email_verified`）。
> - 分页：N/A。
>
> **UX goals**
> - `tenant_id` ⚠ 无 public bootstrap 端点暴露"当前站点 tenant_id / 是否开放注册 / 是否需要邀请码"——这些只在 admin-only `GET /v1/admin/platform-settings`（key: `registration_enabled` / `invitation_required` / `passkey_enabled` …，platform_admin 角色）里。**需后端补一个匿名可读的 site-config/bootstrap 端点，或由部署侧把 tenant_id 注入前端 env**。在拿到之前，tenant_id 走 `process.env.HUAKAI_TENANT_ID`（或子域名映射），邀请码字段默认显示、用 validate 端点的 `reason=disabled` 动态隐藏。
> - 邀请码输入框做防抖 validate 实时反馈（绿勾/红叉 + reason 文案）。
> - 密码强度提示（minLength 8 是硬约束，前端可加更强提示但不得放宽）。
> - 注册成功不自动登录（后端只返回 user、不返回 session）；引导去验证邮箱。
> - i18n：zh-CN 默认 + en（§5.6）。
>
> 类型来自 `lib/api/schema.d.ts`（由 openapi 生成），禁止手写 shape；交付页面组件 + `lib/api/auth.ts` typed 调用 + 正确 `app/(public)/register` 路由段。

---

### /login

> **目标 / 受众 / guard**：登录页。受众 **public**，guard = `public`（`app/(public)/login`，已有有效 session 则重定向）。支持三种登录：密码（含 2FA 二段挑战）、discoverable passkey（WebAuthn）、忘记密码入口。社交/OAuth/Telegram 登录端点存在但属可选增强（见下）。
>
> **Endpoints**
> - `POST /v1/auth/login`（operationId `authLogin`，`security: []`）——密码登录。请求体 `AuthLoginRequest`：`tenant_id`、`email`、`password`、可选 `device_info`(object)。响应分两态：
>   - `200` → `AuthSessionResponse`：`user`(AuthUser) + `session`(SessionTokenBundle)。**直接落 session、跳 dashboard。**
>   - `202` → `AuthTwoFactorChallengeResponse`：`user`、`two_factor_required:true`、`challenge_id`、`challenge_expires_at`(date-time)。**进入 2FA 二段输入态，不发 session。**
> - `POST /v1/auth/login/2fa`（operationId `authLoginTwoFactor`，`security: []`）——完成密码登录的 2FA 挑战。请求体 `AuthTwoFactorLoginRequest`：`challenge_id`、`code`（6 位 TOTP 或一次性备份码）、可选 `device_info`。`200` → `AuthSessionOnlyResponse`：`session`(SessionTokenBundle)。
> - `POST /v1/auth/passkey/login/begin`（operationId `authPasskeyLoginBegin`，`security: []`）——开始 discoverable passkey 登录。请求体 `PasskeyLoginBeginRequest`：仅 `tenant_id`。`201` → `PasskeyBeginResponse`：`session_id`、`public_key`(WebAuthn PublicKeyCredentialRequestOptions，直接喂 `navigator.credentials.get`)、`expires_at`。
> - `POST /v1/auth/passkey/login/finish`（operationId `authPasskeyLoginFinish`，`security: []`）——完成 passkey 登录。请求体 `PasskeyLoginFinishRequest`：`tenant_id`、`session_id`（来自 begin）、`credential`(WebAuthnCredentialJSON，浏览器断言)、可选 `device_info`。`200` → `AuthSessionResponse`（同密码登录）。
> - `POST /v1/auth/reset-password`（operationId `authResetPassword`，`security: []`）——忘记密码。**同一端点两态**：仅 `tenant_id`+`email`（省略 `token`）= 请求重置挑战，返回 `202` `AuthResetRequestResponse`（`reset_requested:true`，`reset_token` 仅 dev）；带 `token`+`new_password`(minLength 8) = 完成重置并撤销所有 session，返回 `200` `AuthUserResponse`。请求体 schema `AuthResetPasswordRequest`：`tenant_id`(必填)、`email`、`token`、`new_password`。
> - 可选社交登录（端点确有，按需暴露按钮）：`POST /v1/auth/oauth-init`(`authOAuthInit`，返回 `auth_url`+`state`，set-cookie `huakai_oauth_state`) → `POST /v1/auth/oauth-callback`(`authOAuthCallback`，cookie+body `state`/`code`，→ `AuthSessionResponse`)；`POST /v1/auth/telegram-login`(`authTelegramLogin`，200 发 session / 202 表示缺验证邮箱需走 pending-email)。provider enum：`google,github,qq,dingtalk,nodeseek,discord`（oauth-init）。
>
> **States**
> - loading：每个登录方式独立 spinner；passkey ceremony 进行中显示"等待安全密钥…"。
> - empty：N/A。
> - error（§5.1 映射）：
>   - `401 Unauthorized`（密码错/2FA 码错/passkey 断言失败）→ **统一一句** `invalid_credentials` 文案；**严禁**区分"账户不存在/被锁/未验证"（后端故意折叠，账户枚举安全）。
>   - `400 BadRequest` → 字段级。
>   - `403 Forbidden`（passkey 被平台关闭 / oauth provider 未配置 / 账户被禁）→ 明确文案。
>   - `429 RateLimited`（登录按 IP 限流，**先于密码哈希**触发，带粗粒度 `Retry-After`）→ 倒计时禁用提交。
>   - `409 Conflict`（reset-password token 复用）→ "链接已失效，请重新申请"。
>   - `503 ServiceBusy` → 退避重试。
>   - 2FA 态：`challenge_expires_at` 到期后 → 回退到密码态并提示"挑战已过期，请重新登录"。
> - 分页：N/A。
>
> **UX goals**
> - 三步式 2FA：密码 → 若 202 → 切到 code 输入（同时显示"用备份码"切换，二者都打 `/login/2fa`，code 字段兼收）；用 `challenge_expires_at` 跑倒计时。
> - passkey 走标准 WebAuthn：begin 返回的 `public_key` 经 base64url 解码后传 `navigator.credentials.get()`，断言再 base64url 编码回 `credential` 打 finish。RP id/origins 由后端在 `public_key` 内下发，前端**不需**单独配置。
> - "记住设备"可选填 `device_info`（与 sessions 页的设备列表对应）。
> - 忘记密码做成两步独立子流程（请求邮件 → 邮件回链落到 `reset-password?token=...` 填新密码），完成后提示"已登出所有设备，请重新登录"。
> - ⚠ tenant_id 同 `/register`：无匿名 bootstrap 端点，走 env/子域名；passkey/oauth 是否可用也只在 admin platform-settings 里，前端先全显示、按 `403` 优雅降级。
> - money/streaming：本页不涉及。
>
> 类型来自 `lib/api/schema.d.ts`（由 openapi 生成），禁止手写 shape；交付页面组件 + `lib/api/auth.ts`（含 `lib/api/passkey.ts` 的 WebAuthn 编解码 helper）typed 调用 + 正确 `app/(public)/login` 路由段。

---

### /account（profile · password · passkey · 2FA · sessions）

> **目标 / 受众 / guard**：账户安全中心，分 5 个 tab。受众 **user**，guard = `user`（`app/(portal)/account`，全部用 `sessionBearerAuth`）。覆盖资料改名、社交绑定解绑、2FA 启停/备份码、passkey 增删、设备会话管理。⚠ 改密码见下方专门标注。
>
> **Endpoints（按 tab 分组，均 `security: sessionBearerAuth`）**
>
> Profile tab（tag `auth`）
> - `GET /v1/auth/me`（`authMe`）——解析当前用户面板权限与资料：返回 `panel`(enum `admin`|`user`)、`user_id`、`tenant_id`、`display_name`。**这也是 app 外壳判定 admin/user guard 的权威来源（见外壳页）。**
> - `PUT /v1/auth/me/profile`（`updateAuthMeProfile`）——改显示名。请求体仅 `display_name`(string, minLength 1, maxLength 100，trim、拒控制字符)。200 返回 `user_id`/`tenant_id`/`display_name`。租户与用户由 session 派生，**body 不传 tenant/user**。
> - `DELETE /v1/auth/account-bindings/{provider}`（`unlinkAuthAccountBinding`）——解绑社交登录。path `provider` enum：`google,github,wechat,dingtalk,linuxdo,oidc`。200 返回 `{ unlinked: bool }`。**后端拒绝解绑"最后一个登录方式"（无本地密码时）→ 409。**
>
> Password tab
> - ⚠ **无后端契约——需后端补或换方案**：openapi 中**不存在已登录用户改密码端点**（grep `change-password`/`/v1/me/password` 全空）。唯一改密路径是 `POST /v1/auth/reset-password`（token 制，走邮件，且会**撤销所有 session**），它 `security: []` 属忘记密码流。account 页的"修改密码"要么 (a) 复用 reset-password 走"给我发重置邮件"按钮（体验差、会登出全设备），要么 (b) 让后端补一个 `sessionBearerAuth` 的 `POST /v1/auth/me/password`（带 `current_password`+`new_password`）。**先按 (a) 实现并显著标注"将通过邮件重置并登出所有设备"，同时挂 TODO 等后端补 (b)。**
>
> 2FA tab（tag `auth`）
> - `GET /v1/auth/2fa/status`（`authTwoFactorStatus`）——`AuthTwoFactorStatus`：`available`(bool)、`enabled`(bool)、`backup_codes_remaining`(int)、`locked_until`(nullable date-time)、`last_used_at`(nullable)。
> - `POST /v1/auth/2fa/setup`（`authTwoFactorSetup`，body 可选 `account_name`）——`201` `AuthTwoFactorSetupResponse`：`secret`(base32)、`qr_data`(otpauth URI)、`backup_codes`(string[])。**这三项一生只此一次，不再返回。**
> - `POST /v1/auth/2fa/enable`（`authTwoFactorEnable`，body `AuthTwoFactorCodeRequest`:`code`）——验码后启用，200 → `AuthTwoFactorStatus`。
> - `POST /v1/auth/2fa/disable`（`authTwoFactorDisable`，无 body）——200 → `AuthTwoFactorDisableResponse`(`enabled:false`)。
> - `POST /v1/auth/2fa/backup-codes/regenerate`（`authTwoFactorRegenerateBackupCodes`，body `code`）——200 → `AuthTwoFactorBackupCodesResponse`(`backup_codes`)，旧未用码作废。
>
> Passkey tab（tag `user-passkeys`）
> - `GET /v1/me/passkeys`（`listMyPasskeys`）——`PasskeyListResponse.passkeys[]`，每项 `PasskeyCredentialSummary`：`id`、`name`、`transports[]`、`attestation_type`、`clone_warning`(bool)、`sign_count`、`created_at`、`last_used_at`(nullable)。
> - `POST /v1/me/passkeys/register/begin`（`beginMyPasskeyRegistration`）——请求体 `PasskeyRegisterBeginRequest`：必填 `step_up`(PasskeyStepUpProof:`password` 或 `two_factor_code`，**二选一近期证明**)、可选 `name`(maxLength 120)。`201` → `PasskeyBeginResponse`（`session_id`/`public_key`=CreationOptions/`expires_at`）。
> - `POST /v1/me/passkeys/register/finish`（`finishMyPasskeyRegistration`）——请求体 `PasskeyRegisterFinishRequest`：`session_id`、`credential`(WebAuthnCredentialJSON)、`step_up`、可选 `name`。`201` → `PasskeyResponse.passkey`(summary)。
> - `DELETE /v1/me/passkeys/{id}`（`deleteMyPasskey`，path `id` int64）——请求体 `PasskeyDeleteRequest`：必填 `step_up`。200 → `{ deleted: true }`。
>
> Sessions tab（tag `sessions`）
> - `POST /v1/sessions/list`（`sessionList`，body `SessionListRequest` 空对象 `{}`）——200 → `{ families: SessionFamily[] }`。`SessionFamily`：`id`(uuid)、`user_id`、`tenant_id`、`status`(enum `active`/`revoked`/`expired`/`suspicious`/`replaced`)、`generation`、`created_at`、`last_active_at`、`device_info`(obj)、`ip_baseline`、`revoked_at`(nullable)、`revoked_reason`(nullable)。
> - `POST /v1/sessions/revoke`（`sessionRevoke`，body `SessionRevokeRequest`：`family_id`(uuid) | `session_token` | `refresh_token` | `reason`，任选定位一个或一族）——200 → `{ revoked: int64 }`。撤销整族传 `family_id`。
> - （刷新令牌 `POST /v1/sessions/refresh`，operationId `sessionRefresh`，`security: []`，body `refresh_token` → `SessionTokenResponse.session`：归 `lib/api/client.ts` 的静默续期拦截器，不在本页 UI。）
>
> **States**
> - loading：每 tab 独立骨架；`GET /v1/me/passkeys`、`/v1/auth/2fa/status`、`POST /v1/sessions/list` 各自加载。
> - empty：passkey 列表空 → "尚未添加通行密钥"+ 引导添加；sessions 仅当前会话 → 正常单行。
> - error（§5.1 映射）：
>   - `401 Unauthorized`（session 过期）→ 触发 client 续期或踢回 `/login`。
>   - `403 Forbidden`（step_up 证明无效/过期、2FA 平台关闭、passkey 注册被关）→ 重新要求 step_up 或提示能力不可用。
>   - `400 BadRequest`（2FA code 格式、display_name 控制字符、step_up 缺失）→ 字段级。
>   - `409 Conflict`（passkey finish 凭证已存在；解绑最后登录方式）→ 明确文案"无法解绑最后的登录方式，请先设置密码"。
>   - `429 RateLimited`（2FA enable / backup-codes regenerate 限流）→ 退避。
>   - `404 NotFound`（解绑不存在的 binding，幂等可当成功）。
>   - `503 ServiceBusy` → 退避。
> - 分页：本切片的列表（passkeys / sessions）**无 cursor/limit 参数**，是全量返回，前端纯客户端排序即可（与 §5.4 的服务端分页表格不同——此处不需要）。
>
> **UX goals**
> - **一次性密文只显示一次**：2FA setup 的 `secret`/`qr_data`/`backup_codes`、regenerate 的新 `backup_codes`——渲染 QR + 可复制明文 + "我已保存"确认后即从内存清除，绝不二次拉取。
> - **step_up 门**：passkey 注册/删除前弹 step_up 对话框（输密码或 2FA code），把证明塞进 begin/finish/delete 的 `step_up`。
> - **destructive 确认对话框**（§5.7，不乐观更新）：删 passkey、disable 2FA、revoke session、解绑社交——均需确认对话框 + 等服务端确认后再刷新列表。
> - sessions 表把 `device_info`/`ip_baseline`/`last_active_at` 友好化，当前会话打标"本设备"，提供"登出此设备"(传该族 `family_id`) 与"登出其它所有设备"。`status` 用状态徽章（active/suspicious 高亮）。
> - `clone_warning=true` 的 passkey 显示告警徽章（疑似克隆）。
> - 2FA `locked_until` 非空时禁用相关操作并显示解锁倒计时；`backup_codes_remaining` 低于阈值时提示 regenerate。
> - WebAuthn 编解码 helper 与 `/login` passkey 复用同一 `lib/api/passkey.ts`。
> - money/streaming：本页不涉及。
>
> 类型来自 `lib/api/schema.d.ts`（由 openapi 生成），禁止手写 shape；交付页面组件 + `lib/api/auth.ts` / `lib/api/passkey.ts` / `lib/api/sessions.ts` typed 调用 + 正确 `app/(portal)/account` 路由段。

---

### app 外壳（role-aware nav + 三 guard）

> **目标 / 受众 / guard**：全站布局外壳——顶栏 + 角色感知侧栏 + 三套路由 guard（`public` / `user` / `admin`）。不是单一 route，而是 `app/(public)`、`app/(portal)`、`app/(admin)` 三个 route group 的 layout + 中间件，是其它所有切片的容器。
>
> **Endpoints（外壳自身只依赖 auth tag 的两个）**
> - `GET /v1/auth/me`（`authMe`，`sessionBearerAuth`）——**外壳判权威**：返回 `panel`(enum `admin`|`user`)、`user_id`、`tenant_id`、`display_name`。用它决定渲染 user 还是 admin 侧栏、是否放行 `(admin)` 段。`401` → 视为未登录（public）。**admin 与否以 `panel` 字段为准，不要前端硬编码角色。**
> - `POST /v1/auth/logout`（`authLogout`，`sessionBearerAuth`）——顶栏"退出"。无 body（family id 取自 SessionIdentity）。200 → `{ revoked: int64 }`。退出后清本地 session、跳 `/login`。
> - 静默续期：`POST /v1/sessions/refresh`（`sessionRefresh`，body `refresh_token` → `SessionTokenResponse.session` = SessionTokenBundle）。装在 `lib/api/client.ts` 拦截器：401 时用 `refresh_token` 续 `session_token`，失败则登出。
>
> **三 guard（§3）**
> - `public`：无需 auth（`app/(public)`：landing/pricing/login/register/...）。已登录访问 login/register → 重定向到 dashboard。
> - `user`：需有效 session（`app/(portal)`）。无 session 或 `/v1/auth/me` 401 → 跳 `/login?next=...`。
> - `admin`：需 `/v1/auth/me` 返回 `panel==='admin'`（`app/(admin)`）。普通 user 访问 → 404/重定向，且**侧栏绝不渲染任何 admin 项**（§3 + 验收清单"admin surface invisible to non-admins"）。
>
> **States**
> - loading：首屏拉 `/v1/auth/me` 时显示外壳骨架（避免 nav 闪烁/越权闪现）。
> - error：
>   - `/v1/auth/me` `401` → 未登录态（public 外壳）。
>   - `403` → 已登录但越权（admin 段对 user）→ 重定向 `/dashboard`。
>   - `503` → 全局"服务暂时不可用"占位，保留重试。
> - empty / 分页：N/A。
>
> **UX goals**
> - 角色感知导航：依 `panel` 渲染 user 侧栏（dashboard/playground/keys/usage/billing/.../account）或 admin 侧栏（admin/...，见 §4C），两者互斥；顶栏放 `display_name`、租户标识、主题切换、语言切换（zh-CN 默认 / en，§5.6）、退出。
> - session 生命周期：`session_expires_at` 临近用 `refresh_token` 静默续期；refresh `409 Conflict`（令牌轮换冲突/疑似重放）→ 强制登出并提示"会话已失效，请重新登录"。
> - 把 `/v1/auth/me` 结果缓存进 TanStack Query（外壳级 provider），供各页读 `tenant_id`/`user_id`/`panel`，避免重复请求。
> - 全局 toast 系统统一消费 `ErrorResponse.error.code`，集中维护 code→本地化文案表（§5.1），任何页面不得直出后端 message 或裸 500。
> - ⚠ 顶栏可能想显示"余额/未读通知"等——那些来自其它切片的 endpoint（user-quota / user-notifications），外壳只预留插槽，不在本切片造契约。
> - money/streaming：外壳不涉及。
>
> 类型来自 `lib/api/schema.d.ts`（由 openapi 生成），禁止手写 shape；交付三个 route-group 的 layout 组件 + guard 中间件 + `lib/api/auth.ts`（me/logout）与 `lib/api/client.ts`（Bearer 注入 + refresh 拦截）typed 调用 + 正确 `app/(public)` / `app/(portal)` / `app/(admin)` 路由段骨架。

---

## S2 — 用户核心(/dashboard · /keys · /playground · /usage)

> 切片范围:User portal 核心日用闭环。auth guard 统一 **user**(session bearer,`sessionBearerAuth`;playground 的推理调用走用户自选的 `hk_` key)。涉及 openapi tags:`user-quota`、`user-api-key-controls`、`user-api-keys`、`gateway`(`/v1/models` + chat/messages/responses)、`user-audit`(`/v1/me/usage`、analytics、generation、key usage-summary)。
> 跨切面规则引用见 FRONTEND-BUILD-SPEC §5:错误码映射 §5.1、Money(numeric(20,8) / micro-USD,decimal string,禁浮点)§5.2、Streaming(text/event-stream)§5.3、分页/过滤 §5.4、幂等 §5.5、i18n §5.6、money 不做乐观 UI §5.7。所有 path/字段名均取自 `docs/openapi/openapi.yaml` 真值。

### /dashboard

> 设计并实现 HUAKAI user portal 的 **`/dashboard`** 页(Next.js 15 App Router + Tailwind 4 + TanStack Query)。受众 **user**;auth guard **user**(session bearer)。一眼概览页:当前 cost_usd 配额窗口剩余、最近消费记录、按模型的用量时序图。
>
> **Endpoints**
> - `GET /v1/me/quota`(`getMyQuota`)— 当前用户 cost_usd 配额窗口状态。响应 `MeQuotaStatusResponse{ items[] }`,每个 `MeQuotaWindowStatus`:`window_kind`(枚举 `none|fixed|calendar_day|calendar_week|calendar_month|manual`)、`cap`、`consumed`、`remaining`、`overage`(**均为 numeric(20,8) decimal string**)、`request_count`(int64)、`window_start`、`window_end`(date-time)。注意:此 v1 投影**只暴露 cost_usd 维度**,无 tokens/concurrency 窗口——不要在此页画 token 配额条。无分页、无 query 参数(scope 只来自 session,客户端传 user_id/tenant_id 会被忽略)。
> - `GET /v1/me/usage`(`listMyUsage`)— 最近消费记录(取前 N 条做「最近活动」卡)。query:`limit`(1–200,默认 100)、`cursor`、`from`、`to`(date-time)。响应 `MeUsageListResponse{ items[], next_cursor }`;每条 `MeUsageRecord`:`requested_model`、`upstream_model`、`actual_cost`(decimal string)、`ledger_id`、`verify_hint`、`created_at`、`status`、可选 `provider`、`request_id`。dashboard 只取首页(传 `limit=5~10`),「查看全部」跳 `/usage`。
> - `GET /v1/me/analytics/time-series`(`getMyUsageTimeSeries`)— 概览迷你图。query **必填** `from`、`to`(RFC3339,运行时上限 31 天),可选 `granularity`(枚举 `day|week|month`,默认 `day`)。响应 `MeUsageTimeSeriesResponse{ items[], period{from,to} }`;每个 `MeUsageTimeSeriesPoint`:`day`(YYYY-MM-DD)、`requested_model`、`total_cost`(decimal string)、`tokens{ input, output, cache_read, cache_creation }`(int64)、`request_count`(int64)。
>
> **States**
> - loading:三块 skeleton(配额卡 / 最近消费表 / 时序图)。
> - empty:无任何用量 → `items` 为空 → 引导「去 /playground 发起第一个请求」或「去 /keys 创建密钥」。
> - error(§5.1 映射):`401 Unauthorized`(`ErrorResponse.error.code`,如 token 失效)→ 跳登录;`400 BadRequest`(time-series 的 from/to 非法或超 31 天)→ inline 校正窗口;`503 ServiceBusy`(`OVERLOADED`/`NO_ELIGIBLE_ACCOUNT`,带 `Retry-After`)→ 退避重试 toast。错误体永远读 `error.code` + `error.message`,禁止裸 500。
> - 分页:dashboard 不分页;时序窗口默认「近 30 天」(注意 ≤31 天上限)。
>
> **Money/Streaming**(§5.2)
> - 配额 `cap/consumed/remaining/overage` 与消费 `actual_cost`、时序 `total_cost` 全是 numeric(20,8) **decimal string**。直接对字符串用 BigNumber/Intl 格式化为带币种的金额(2–6 位有效数字),**禁止 parseFloat 后做算术**。`remaining` 已是服务端算好的 `max(cap-consumed,0)`,直接展示,不要前端重算。
>
> **UX goals**
> - 顶部 stat 卡:每个配额窗口一张(剩余额度大字 + cap 做进度环 + `overage>0` 红色告警徽标 + 窗口 `window_start→window_end` 副标)。
> - 「最近消费」紧凑表(`requested_model` / `actual_cost` / `status` / `created_at` 相对时间),点击行可跳 `/usage` 定位。
> - 按 `requested_model` 分组堆叠的 cost 时序图(recharts/visx),granularity 切换 day/week/month。
> - 时间窗选择器复用,把 from/to 同步给所有依赖窗口的卡。
>
> 类型来自 `lib/api/schema.d.ts`(由 openapi 生成),禁止手写 shape;交付页面组件 + `lib/api/dashboard.ts` typed 调用 + 正确 `app/(portal)/dashboard/` 路由段。

### /keys

> 设计并实现 HUAKAI user portal 的 **`/keys`** 页(Next.js 15 + Tailwind 4 + TanStack Query)。受众 **user**;auth guard **user**(session bearer)。功能:列出/创建/改名/启停/撤销自己的 `hk_` API 密钥,并管理每把密钥的细粒度控制(USD 配额、分组、IP 允许/黑名单、模型允许名单)。
>
> **Endpoints — 密钥本体(tag `user-api-keys`)**
> - `POST /v1/api-keys`(`createUserAPIKey`)— 签发。请求 `UserAPIKeyCreateRequest{ name(必填,≤128), environment(枚举 `live|test`,默认 live → 前缀 `hk_live_*`/`hk_test_*`,admin 拒绝), expires_at(RFC3339,须未来,缺省永不过期) }`。201 返回 `UserAPIKeyCreateResponse{ api_key_id, plaintext, key_prefix, status('active'), expires_at, created_at, notice }`。**`plaintext` 只此一次返回**,List/Get 永不再含。
> - `GET /v1/api-keys`(`listUserAPIKeys`)— 列表(含 revoked)。query 分页 `offset`(默认 0)、`limit`(1–200,默认 50)。响应 `UserAPIKeyListResponse{ api_keys[]: UserAPIKeyView, count }`。`UserAPIKeyView`:`api_key_id`、`name`、`key_prefix`、`status`(枚举 `active|revoked|disabled|expired`)、`expires_at`、`last_used_at`、`revoked_at`、`revoked_reason`、`created_at`、`updated_at`(永不含 plaintext/hash)。
> - `GET /v1/api-keys/{id}`(`getUserAPIKey`)— 单条元数据(404 不区分「不存在/非本人」防枚举)。
> - `PATCH /v1/api-keys/{id}`(`patchUserAPIKey`)— 改名或改状态。请求 `{ name?(≤128), status?(枚举 `active|revoked`) }`,只改提供的字段。
> - `DELETE /v1/api-keys/{id}`(`revokeUserAPIKey`)— 撤销(幂等)。请求可选 `UserAPIKeyRevokeRequest{ reason(≤256) }`。响应 `UserAPIKeyRevokeResponse{ api_key_id, already_revoked }`(幂等命中 already_revoked=true)。
> - `POST /v1/api-keys/batch-revoke`(`batchRevokeUserAPIKeys`)— 批量撤销。请求 `{ ids[](int64, 1–200 条), reason? }`。响应 `{ revoked[], not_found[] }`(非本人的 id 落 not_found 防枚举)。
> - `GET /v1/me/keys/{id}/usage-summary`(`getMyAPIKeyUsageSummary`,tag user-audit)— 单把密钥用量汇总(行展开/详情抽屉)。query `from`、`to`(date-time,可选)。响应 `MeAPIKeyUsageSummary{ api_key_id, total_cost(decimal string), total_tokens_input, total_tokens_output, total_cache_read_tokens, total_cache_creation_tokens, request_count, from, to }`。
>
> **Endpoints — 每把密钥的控制(tag `user-api-key-controls`,均 GET 读 / PUT 写,scope=本人密钥)**
> - `GET|PUT /v1/api-keys/{id}/quota` — per-key 成本配额。PUT `UserAPIKeyQuotaUpdateRequest{ limit_usd(必填,decimal string ^[0-9]+(\.[0-9]{1,8})?$), window_kind(枚举 `fixed|calendar_day|calendar_week|calendar_month`,默认 calendar_day), window_seconds(仅 fixed 必填), mode(枚举 `enforce|observe|manual_first|disabled`,默认 enforce) }`。响应 `UserAPIKeyQuotaResponse{ api_key_id, policy_id, limit_usd, scope_kind('api_key'), scope_id, metric('cost_usd'), window_kind, window_seconds, mode, valid_from }`。
> - `GET|PUT /v1/api-keys/{id}/group` — 分组分配。PUT `UserAPIKeyGroupUpdateRequest{ group_id(int64 或 null;null/省略=清除) }`。响应 `UserAPIKeyGroupResponse{ api_key_id, group_id?, group_name?, group_description?, group_enabled? }`(无分组时 group_id 缺省)。
> - `GET|PUT /v1/api-keys/{id}/ip-allowlist` — IP 允许名单。PUT `UserAPIKeyIPAllowlistUpdateRequest{ ip_allowlist[](CIDR/裸 IP;空/省略=清除限制) }`。响应 `UserAPIKeyIPAllowlistResponse{ api_key_id, ip_allowlist[](规范化 CIDR) }`。
> - `GET|PUT /v1/api-keys/{id}/ip-blacklist` — IP 黑名单。PUT `{ ip_blacklist[] }` → 响应 `{ api_key_id, ip_blacklist[] }`(空=清除)。
> - `GET|PUT /v1/api-keys/{id}/model-allowlist` — 模型允许名单。PUT `{ allowed_models[] }` → 响应 `{ allowed_models[] }`(空=不限制)。模型候选可用 `GET /v1/models`(见 /playground)填下拉。
>
> **States**
> - loading:列表 skeleton 行。
> - empty:无密钥 → 大号「创建第一把 API 密钥」CTA。
> - error(§5.1):`401`→登录;`400 BadRequest`(如 expires_at 非未来、limit_usd 格式错、CIDR 非法)→ 表单 inline 错误,读 `error.code`/`error.message`;`409 Conflict`(创建冲突)→ 提示重试;`404`(改/读不存在或非本人,**不暴露存在性**)→ 通用「未找到」;`503`→ 退避。
> - 分页:`offset`/`limit` 服务端分页;表格列对应 `UserAPIKeyView` 字段名(name / key_prefix / status / last_used_at / created_at)。
>
> **Money**(§5.2/§5.5/§5.7)
> - per-key `limit_usd`、usage-summary `total_cost` 是 decimal string;格式化展示不做浮点。创建/撤销/改配额是 money/安全敏感操作,**不做乐观 UI**(§5.7),必须等服务端确认。
>
> **UX goals**
> - 创建成功对话框:**plaintext 只显示一次**——大号 reveal/copy 卡,强提示 `notice` 文案,「我已保存」勾选后才能关闭;关闭后列表里只剩 `key_prefix`。
> - 撤销/批量撤销是 destructive → 必弹确认对话框(展示将影响的 key_prefix 列表),支持填 `reason`;幂等命中(already_revoked)以中性提示而非报错呈现。
> - 行展开抽屉聚合该 key 的 5 个控制(quota/group/ip-allow/ip-black/model-allow)各一个 GET+PUT 编辑器;quota window_kind=fixed 时才显示 window_seconds 输入;mode 选择给 enforce/observe/manual_first/disabled 的语义说明。
> - 状态徽标按 `status` 枚举着色(active/expired/revoked/disabled)。
>
> 类型来自 `lib/api/schema.d.ts`(由 openapi 生成),禁止手写 shape;交付页面组件 + `lib/api/keys.ts` typed 调用(本体 + 5 个 controls)+ 正确 `app/(portal)/keys/` 路由段。

### /playground

> 设计并实现 HUAKAI user portal 的 **`/playground`** 页(Next.js 15 + Tailwind 4)。受众 **user**;auth guard **user**(页面访问走 session;**实际推理调用必须用用户自选的某把 `hk_` 明文 key** 作 `Authorization: Bearer hk_...`,见 §3)。功能:多协议流式聊天测试台 + 模型选择器。
>
> **Endpoints**
> - `GET /v1/models`(`listModels`,tag gateway)— 模型选择器数据源。响应 `ModelsListResponse{ object('list'), data[]: ModelObject }`;`ModelObject{ id, object('model'), created(int64), owned_by, capabilities{ [flag]:bool 如 vision/function_calling/tool_choice/reasoning/prompt_caching/response_schema }, max_output_tokens?, mode?(如 chat/embedding) }`。只列当前 tenant 现在可路由的 alias。
> - `GET /v1/models/{model}`(`getModel`)— 选定模型的能力详情(用 capabilities 决定 UI:有 vision 才允许贴图、有 tool_choice 才显示工具配置)。未知模型返回 `model_not_found`。
> - `POST /v1/chat/completions`(`postChatCompletions`)— OpenAI Chat 协议。请求 `ChatCompletionsRequest{ model(必填), messages[](必填,`ChatMessage{ role: system|user|assistant|tool|function, content(string 或 content-parts 数组), tool_calls?, tool_call_id? }`), stream(默认 false), max_tokens?, temperature?, tools[]?, tool_choice?, stream_options{ include_usage } }`。`stream:true` 时响应 `text/event-stream`(`SSEFrame`)。响应头 `X-HUAKAI-Protocol-Loss`(本次翻译有损的特性逗号列表,空=完整保真)、`X-HUAKAI-Idempotency-Hit`。
> - `POST /v1/messages`(`postAnthropicMessages`)— Anthropic Messages 协议(`AnthropicMessagesRequest` → json 或 SSE)。
> - `POST /v1/responses`(`postResponses`)— OpenAI Responses 协议(`ResponsesRequest` → json 或 SSE)。三者同一 hub-and-spoke 编排,只是客户端 adapter 不同。
> - 幂等(§5.5):三个推理端点都接受 `Idempotency-Key` header(派生 idempotency_key = header+tenant+payload hash);重放相同指纹返回缓存结果并打 `X-HUAKAI-Idempotency-Hit: true`。playground 重发同一条时复用同一 key,避免重复扣费。
>
> **States**
> - loading:模型列表加载 → 选择器 skeleton;请求进行中 → transcript 流式 typing 指示。
> - empty:无可用模型(`data` 空)→ 提示「当前 tenant 无可路由模型」;无密钥 → 引导去 `/keys`。
> - error(§5.1,推理端点错误码全集):`400 BadRequest`(如 `invalid_request_error`、prompt_too_long)→ inline;`401 Unauthorized`(`TOKEN_PERMANENTLY_REVOKED` 等)→ 提示换 key;`402 PaymentRequired`(`QUOTA_EXHAUSTED`/insufficient_balance,**无 Retry-After**,需充值)→ 引导 `/billing`;`409 Conflict`(`FINGERPRINT_CONFLICT` 幂等指纹不符)→ 提示重放冲突;`429 RateLimited`(带 `Retry-After`,`RATE_LIMIT_5H_EXCEEDED`)→ 倒计时退避;`503 ServiceBusy`(`NO_ELIGIBLE_ACCOUNT`/`OVERLOADED`,带 `Retry-After`)→ 退避重试。错误体读 `error.code`/`error.message`/`error.retry_after_seconds`/`error.protocol_loss`。
>
> **Streaming**(§5.3,核心)
> - `stream:true` 时以 Fetch streaming body 消费 `text/event-stream`,逐帧渲染 `SSEFrame`。SSE 的 `event:`/`data:` 语义跟随所选客户端协议公开契约(Chat Completions / Responses / Anthropic Messages 各自不同)——按当前选定协议解析 delta、content_block_start/delta/stop、tool_calls,以及 image-capable 模型的 image 输出块。流结束读 usage(若请求了 `stream_options.include_usage`)。
> - 每次响应顶部展示 `X-HUAKAI-Protocol-Loss` 徽标:空=「完整保真」绿;非空=列出有损特性名。
>
> **UX goals**
> - 协议切换 tab(Chat Completions / Messages / Responses)→ 切换目标端点与请求体形状,但共享同一 transcript UI。
> - 密钥选择器:列出本人 active key(`key_prefix`),选中后该 key 明文用于本次调用(明文需用户在 /keys 创建时已保存,playground 让其粘贴/选择)。
> - 模型选择器按 `capabilities` 动态启用控件(vision→图片上传、tool_choice→工具 JSON 编辑器、reasoning→显示思维链区);`max_output_tokens` 作 max_tokens 上限提示。
> - 参数面板:temperature、max_tokens、stream 开关;stream 默认开。
> - 可复制的 cURL/请求体预览(便于用户把 playground 配置带到自己代码)。
>
> 类型来自 `lib/api/schema.d.ts`(由 openapi 生成),禁止手写 shape;交付页面组件 + `lib/api/playground.ts` typed 调用(models + 三协议推理 + SSE 解析器)+ 正确 `app/(portal)/playground/` 路由段。

### /usage

> 设计并实现 HUAKAI user portal 的 **`/usage`** 页(Next.js 15 + Tailwind 4 + TanStack Query)。受众 **user**;auth guard **user**(session bearer)。功能:消费历史明细 + 用量分析图表 + 单次生成查询 + CSV/JSON 导出。
>
> **Endpoints**
> - `GET /v1/me/usage`(`listMyUsage`)— 消费明细表(主体)。**cursor 分页**:query `cursor`(用上一页返回的 `next_cursor`)、`limit`(1–200,默认 100)、`from`、`to`(date-time)。响应 `MeUsageListResponse{ items[], next_cursor(空串=无下一页) }`;`MeUsageRecord{ requested_model, upstream_model, actual_cost(decimal string), provider?, provider_account_id?, ledger_id, verify_hint{ trust_verify_path:'/v1/trust/verify', trust_verify_method:'POST', audit_verify_path:'/v1/audit/verify', request_id?, ... }, created_at, status, request_id? }`。响应**故意省略** tenant_id/api_key_id/user_id/prompt/body/消息内容。
> - `GET /v1/me/analytics/time-series`(`getMyUsageTimeSeries`)— 分析图表。query **必填** `from`/`to`(RFC3339,≤31 天),可选 `granularity`(`day|week|month`,默认 day)。响应 `MeUsageTimeSeriesResponse{ items[]: { day, requested_model, total_cost(string), tokens{input,output,cache_read,cache_creation}, request_count }, period{from,to} }`。
> - `GET /v1/generation`(`getGeneration`,tag user-audit)— 单次生成查询(按 request_id 反查一条 usage)。query **必填** `id`(1–256 字符,即原网关响应里的 request_id)。响应同 `MeUsageRecord` 投影。404 表示不存在或非本人(不泄露存在性)。
> - `GET /v1/me/usage/export.csv`(`exportMyUsageCSV`)— 导出。query **必填** `from`/`to`(date-time)、可选 `format`(枚举 `csv|json`,默认 csv)。CSV 列:`request_id,model,tokens_input,tokens_output,cost_usd,created_at,status`;JSON 形态 `{ items[]:{request_id,model,tokens_input,tokens_output,cost_usd(string),created_at,status}, truncated, row_limit }`。响应头 `Content-Disposition`(下载文件名)、`X-Truncated: true`(命中 10 万行上限时,并附尾行提示)。
>
> **States**
> - loading:表 + 图 skeleton。
> - empty:窗口内无记录 → 「该时间段无消费」,提供放宽时间窗的快捷。
> - error(§5.1):`400 BadRequest`(time-series/export 缺 from/to 或超 31 天、generation 的 id 非法)→ inline;`401`→登录;`403 Forbidden`(generation 越权)→ 通用拒绝;`404`(generation 未找到/非本人,不区分)→「未找到该 request_id」;`503`→ 退避。读 `error.code`/`error.message`。
> - 分页:**cursor-based**(§5.4)——「下一页」用 `next_cursor`;`next_cursor==""` 时禁用「下一页」。表格列名严格对应 `MeUsageRecord` 字段(requested_model / upstream_model / actual_cost / status / created_at;可展开看 ledger_id + verify_hint)。
>
> **Money**(§5.2)
> - 明细 `actual_cost`、时序 `total_cost`、导出 `cost_usd` 全是 numeric(20,8) **decimal string**;直接字符串格式化(2–6 位有效数字 + 币种),合计行也用 BigNumber 累加,禁浮点。tokens 字段是 int64,正常数字格式化。
>
> **UX goals**
> - 顶部时间窗 + granularity 选择器,联动「分析图表」(按 requested_model 分组的 cost / tokens / request_count 多图)与「明细表」。
> - 明细表行展开:显示 `ledger_id` + verify_hint(给「去 /trust 验证」深链到 `trust_verify_path`),体现可验证账本卖点。
> - 单次生成查询框:粘贴 request_id 调 `/v1/generation`,弹出该次的只读 receipt 投影。
> - 导出按钮:弹窗选时间窗 + 格式(csv/json),走 `export.csv`;命中 `X-Truncated` 时明显提示「已截断至 row_limit 行」。
> - tokens 维度:input/output/cache_read/cache_creation 分列展示(cache 命中是省钱卖点,可视化突出)。
>
> 类型来自 `lib/api/schema.d.ts`(由 openapi 生成),禁止手写 shape;交付页面组件 + `lib/api/usage.ts` typed 调用(usage 列表 + time-series + generation + export)+ 正确 `app/(portal)/usage/` 路由段。

---

## S3 钱循环(账单 / 充值 / 订阅 / 凭证)

> 切片范围:用户门户「钱循环」四页 —— `/billing`、`/subscriptions`、`/referrals`、`/checkin`。
> 覆盖 openapi tag:`user-recharges`、`user-vouchers`、`user-payments`、`invitations`、`user-checkin`、`user-subscriptions`、`pricing`。
> 全部页面 auth guard = **user**(`sessionBearerAuth`,会话 token,非 `hk_` key)。
> 跨切面规则引用:框架 §5.1(错误码映射)、§5.2(钱字段)、§5.5(幂等)、§5.7(钱操作不做乐观更新)、§6(PSP 仅占位)。
> 关键钱单位提醒:本切片**绝大多数钱字段是整数 USD 分(`*_cents`,int64)** —— `amount_cents`、`balance_cents`、`reward_cents`、`min/max_cents`、`rewards_earned_cents`、`new_balance`;**唯一例外是充值/webhook 用 decimal string USD**(`amount`、`new_balance` 形如 `"12.34000000"`,正则 `^[0-9]+(\.[0-9]{1,8})?$`)。两套单位**不要混算**,一律从服务端字段直接格式化,禁止前端浮点重算(§5.2)。

---

### /billing

> 设计并实现 **`/billing`** 页(HUAKAI 用户门户,Next.js 15 App Router + Tailwind 4 + TanStack Query)。页面目标:让登录用户看到钱包余额、发起充值(PSP 占位)、查看支付订单历史、兑换凭证(voucher)并查看兑换记录。受众 = **user**;auth guard = **user**(所有调用走 `sessionBearerAuth`,会话 Bearer)。
>
> **Endpoints**(只接以下真实契约,字段名严格照抄 openapi):
> - 余额卡片:`GET /v1/users/me/payments/balance`(`getMyPaymentBalance`)→ `balance.{tenant_id,user_id,amount_cents}`(int64 分)。
> - 充值配置:`GET /v1/users/me/payments/config`(`getMyPaymentPortalConfig`)→ `config.{min_topup_cents,max_topup_cents,preset_amount_cents[],currency_code,providers[]}`;用 `preset_amount_cents` 渲染快捷金额按钮,`min/max_topup_cents` 做输入校验,`providers[]` 渲染可选支付方式。
> - 发起充值(钱包 recharge,decimal 路径):`POST /v1/users/me/recharges`(`createUserRecharge`)。请求体 `UserRechargeCreateRequest`:`amount`(**decimal string USD**,正则 `^[0-9]+(\.[0-9]{1,8})?$`)、`currency`(枚举仅 `USD`)、`provider`(string,生产禁 `mock`)、可选 `return_url`(uri)。201 响应 `UserRechargeCreateResponse.order` = `UserRechargeOrder{id,external_trade_no,recharge_ref,status,amount,currency,provider,created_at}`,`status` 枚举 `PENDING|PAID|CREDITING|COMPLETED|FAILED|EXPIRED|CANCELLED`。
> - 自助 top-up 订单(整数分路径,带支付指令):`POST /v1/users/me/payments/orders`(`createMyTopupOrder`)。请求体 `{amount_cents(int64,≥1), provider(枚举 manual|taobao)}`;201/200 响应 `{order, idempotent, payment_instruction}`(200 = 幂等重放)。
> - 订单历史表:`GET /v1/users/me/payments/orders`(`listMyPaymentOrders`),query `limit`(1–200,默认 50,无 cursor/offset)→ `{orders[]}`,服务端 newest-first。
> - 订单详情/轮询:`GET /v1/users/me/payments/orders/{id}`(`getMyPaymentOrder`)→ `{order}`;跨用户 id 返回 404。
> - 取消待支付订单:`POST /v1/users/me/payments/orders/{id}/cancel`(`cancelMyPaymentOrder`)→ `{order}`(已取消则幂等)。
> - 申请退款(不动钱,记 pending 待管理员审批):`POST /v1/users/me/payments/orders/{id}/refund-request`(`requestMyPaymentRefund`),可选体 `{reason}`,202 → `{refund_request}`。
> - 兑换凭证:`POST /v1/users/me/vouchers/redeem`(`redeemVoucher`)。请求体 `VoucherRedeemRequest`:`code`(必填)、`idempotency_key`(≤128,选填→应自动生成,§5.5)。200 响应 `VoucherRedeemResponse{voucher,redemption,balance_cents(int64),idempotent}`。
> - 兑换记录表:`GET /v1/me/voucher-redemptions`(`listMyVoucherRedemptions`),query `limit`(1–200,默认 50)→ `VoucherRedemptionHistoryResponse{redemptions[]}`,每条 `VoucherRedemptionHistoryItem{voucher_id,amount_cents,currency_code,status('succeeded'),redeemed_at,billing_event_id}`,newest-first。
>
> **States**:loading(余额/配置骨架屏);empty(无订单 / 无兑换记录的空态);error 按 §5.1 映射本页真实错误码 —— `400 BadRequest`(金额超 min/max 范围、currency/amount 格式非法、voucher code 缺失)、`401 Unauthorized`、`409 Conflict`(充值 `idempotency_conflict` 指纹冲突 / voucher 重复兑换或已耗尽 / 订单状态冲突无法取消或退款)、`429 RateLimited`(voucher 兑换限流,读 `Retry-After`)、`503 ServiceBusy`(读 `Retry-After`);永不显示原始 5xx body。订单列表 + 兑换列表用 `limit` 服务端分页(本组列表**仅 limit,无 cursor/offset**,做"加载更多上限 200"或调大 limit,别造 offset 参数)。
> **Money**(§5.2):余额/订单/voucher 金额绝大多数为整数分(`amount_cents`、`balance_cents`),按 `currency_code` 格式化为 `$x.xx`,**不要 ×/÷ 浮点**;唯一 decimal-string 路径是 `POST /v1/users/me/recharges` 的 `amount` 与 `UserRechargeOrder.amount`(原样字符串展示)。两条充值入口(decimal `recharges` vs 整数分 `payments/orders`)在 UI 上要明确区分用途,别混用金额单位。
> **PSP 占位**(§6):发起充值/创建 top-up 后,实际支付握手一律渲染**清晰标注的占位/跳转 mock**(显示 `payment_instruction` 文案 + return_url),真实 Stripe/支付宝/淘宝接入是 Owner-gated,不要实现真支付。
> **UX goals**:① 所有钱操作(充值、兑换、取消、退款)等服务端确认后才更新 UI,**禁乐观更新**(§5.7);② 取消订单、申请退款是 destructive/钱操作,必须弹确认对话框;③ 充值与兑换自动生成并随请求带 `idempotency_key`,防重复扣款(§5.5);④ 订单表服务端分页,列名用真实字段(`external_trade_no`、`status`、`amount`/`amount_cents`、`provider`、`created_at`);⑤ 订单创建后对 `PENDING/CREDITING` 状态轮询 `GET .../orders/{id}` 直到 `COMPLETED`/终态;⑥ voucher `code` 输入框,兑换成功后用响应里的 `balance_cents` 刷新余额并提示 `idempotent` 是否为重放。
> 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/billing.ts typed 调用 + 正确 app/(portal)/billing 路由段。

---

### /subscriptions

> 设计并实现 **`/subscriptions`** 页(HUAKAI 用户门户,Next.js 15 App Router + Tailwind 4 + TanStack Query)。页面目标:展示用户当前生效订阅及其配额进度,浏览可购买套餐,购买/升级套餐,取消自动续订。受众 = **user**;auth guard = **user**(`sessionBearerAuth`)。
>
> **Endpoints**(`user-subscriptions` tag,字段照抄):
> - 当前订阅:`GET /v1/users/me/subscriptions/me`(`getMyCurrentSubscription`)→ `{subscription(object|null), auto_renew(bool)}`;null = 无生效订阅。
> - 配额进度:`GET /v1/users/me/subscriptions/me/progress`(`getMySubscriptionProgress`)→ `{subscription(object|null), progress[]}`,每条 `{window_kind(枚举 calendar_day|calendar_week|calendar_month), cap, consumed, remaining, overage, request_count(int64), window_start, window_end}`;**`cap/consumed/remaining/overage` 全是 decimal USD 字符串**(注意:这里是 decimal,不是 cents)。
> - 全部订阅历史:`GET /v1/users/me/subscriptions`(`listMySubscriptions`)→ `{subscriptions[]}`。
> - 可售套餐:`GET /v1/users/me/subscriptions/plans`(`listAvailableSubscriptionPlans`)→ `{plans[]}`(本租户 for-sale 套餐)。
> - 购买套餐(创建 subscription-kind 支付订单,**不直接激活**):`POST /v1/users/me/subscriptions/purchase`(`purchaseSubscription`)。请求体 `{plan_id(int64,≥1)}`;201/200 → `{order, idempotent, payment_instruction}`(金额/币种取自套餐快照,非客户端);订阅在订单 confirm(管理员确认或 webhook)后才授予。
> - 自助换套餐(**仅升级**,降配 409 拒绝):`POST /v1/users/me/subscriptions/change-plan`(`changeMySubscriptionPlan`),请求体 `{new_plan_id(int64,≥1)}` → `{subscription}`。
> - 取消自动续订(订阅保持 active 至 expires_at,不退当前权益):`POST /v1/users/me/subscriptions/cancel-renew`(`cancelMySubscriptionRenewal`)→ `{subscription, auto_renew:false}`。
>
> **States**:loading(当前订阅 + 进度骨架);empty(`subscription:null` → 「暂无订阅,去选购套餐」CTA + plans 列表);error 按 §5.1:`400 BadRequest`(`new_plan_id`/`plan_id` 非法)、`401 Unauthorized`、`404 NotFound`(无当前订阅却调 change-plan/cancel-renew / 套餐不存在)、`409 Conflict`(降配被拒 / 已有 active 订单幂等重放)、`503 ServiceBusy`。本页**无分页参数**,均为一次性集合返回。
> **Money**(§5.2):配额进度 `cap/consumed/remaining/overage` 是 **decimal USD 字符串**,原样格式化展示进度条(`consumed/cap`),不要解析成浮点再算;套餐价格来自 plans/order 快照,原样展示。
> **PSP 占位**(§6):`purchase` 返回 `payment_instruction` → 渲染清晰标注的占位/跳转 mock,购买后对订单状态轮询(复用 `/billing` 的 `GET /v1/users/me/payments/orders/{id}`),订阅只在 confirm 后出现于 `GET .../subscriptions/me`。
> **UX goals**:① change-plan 文案明确「仅可升级,降配会被拒绝(409)」,提交前确认对话框;② cancel-renew 是钱相关操作,弹确认并说明「当前权益保留至 expires_at」,成功后据 `auto_renew` 更新开关 UI;③ 三个进度窗口(日/周/月)各自一张进度卡,`overage` 高亮告警色;④ 购买/换套餐等服务端确认,禁乐观更新(§5.7);⑤ plans 卡片对比当前订阅高亮「当前套餐/可升级」。
> 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/subscriptions.ts typed 调用 + 正确 app/(portal)/subscriptions 路由段。

---

### /referrals

> 设计并实现 **`/referrals`** 页(HUAKAI 用户门户,Next.js 15 App Router + Tailwind 4 + TanStack Query)。页面目标:展示用户邀请(referral)奖励汇总、被邀请人列表与状态、奖励台账,鼓励邀请好友。受众 = **user**;auth guard = **user**(`sessionBearerAuth`)。
>
> **Endpoints**(`invitations` tag,字段照抄):
> - 邀请奖励汇总卡:`GET /v1/me/invitations`(`getMyInvitationSummary`)→ `InvitationSummaryResponse{qualified_count(int64),rewarded_count(int64),rewards_earned_cents(int64)}`(分)。
> - 被邀请人列表(分页):`GET /v1/me/referrals`(`listMyReferrals`),query `limit`(int)、`offset`(int)→ `{}`(响应为通用 object:被邀请人 referee + status + 时间戳;字段未在 schema 强约束,渲染时容错)。
> - 奖励台账:`GET /v1/me/referrals/rewards`(`listMyReferralRewards`)→ `{}`(通用 object,含 USD 奖励总额;无分页参数)。
>
> **States**:loading(汇总卡 + 列表骨架);empty(`qualified_count==0` / 无 referral → 「还没有邀请记录,分享你的邀请码」空态);error 按 §5.1:`401 Unauthorized`、`503 ServiceBusy`(这三个端点仅定义 401/503);无 400/404。`GET /v1/me/referrals` 用 **`limit`+`offset` 服务端分页**(本页是切片内唯一 offset 分页端点),`rewards` 与 `summary` 为一次性返回。
> **Money**(§5.2):`rewards_earned_cents` 是整数 USD 分,格式化为 `$x.xx`,不浮点重算;rewards 台账内的 USD 总额按服务端返回字段原样展示。
> **UX goals**:① 顶部三连汇总卡(已合格 `qualified_count` / 已发奖 `rewarded_count` / 累计奖励 `rewards_earned_cents`);② 被邀请人表服务端 `limit/offset` 分页,列含 referee + status + 时间戳(status 用状态徽章);③ 邀请码/邀请链接区(若邀请码来自其它 tag 端点而本页无对应契约,见下方告警),提供一键复制。
> **⚠ 无后端契约**:本页「生成/获取我的邀请码或邀请链接」**在本切片负责的 `invitations` me-端点中无对应 endpoint**(`/v1/me/invitations` 仅返回奖励汇总计数,不含邀请码;`/v1/invitations`、`/v1/auth/validate-invitation-code` 属其它切片)。邀请码展示需后端补 me-邀请码端点或由 Auth/账户切片提供,**不要编造 code 字段**;在此前用占位/复制按钮指向已有来源。`GET /v1/me/referrals` 与 `/v1/me/referrals/rewards` 响应为通用 object(schema 未细化字段),按服务端实际返回容错渲染,字段缺失不崩。
> 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/referrals.ts typed 调用 + 正确 app/(portal)/referrals 路由段。

---

### /checkin

> 设计并实现 **`/checkin`** 页(HUAKAI 用户门户,Next.js 15 App Router + Tailwind 4 + TanStack Query)。页面目标:每日签到领奖励,展示当月签到日历、今日是否已签、领取后即时反映余额。受众 = **user**;auth guard = **user**(`sessionBearerAuth`)。
>
> **Endpoints**(`user-checkin` tag,字段照抄):
> - 签到状态 + 当月记录:`GET /v1/me/checkin`(`getMyDailyCheckinStatus`),query `month`(选填,正则 `^[0-9]{4}-[0-9]{2}$`,默认当前 UTC 月)→ `DailyCheckinStatusResponse{enabled(bool),min_cents(int64),max_cents(int64),month,checked_in_today(bool),records[]}`;每条 `DailyCheckinRecord{checkin_date(date),reward_cents(int64),currency_code('USD'),billing_event_id,created_at}`。
> - 领取今日奖励:`POST /v1/me/checkin`(`claimMyDailyCheckinReward`)→ 200 `DailyCheckinClaimResponse{reward_cents(int64),checkin_date(date),new_balance(int64,领奖后支付源余额 USD 分)}`。
>
> **States**:loading(日历骨架);empty(`enabled:false` 或 `records` 为空 → 当月暂无签到记录);error 按 §5.1:`400 BadRequest`(`month` 格式非法)、`401 Unauthorized`、`404`(**签到功能在本部署被禁用** —— claim 返回 `ErrorResponse`,文案提示功能不可用)、`409`(**今日已签** —— `ErrorResponse`,把签到按钮置灰/显示已领取)、`503 ServiceBusy`;永不显示原始 5xx body。无分页;切月用 `month` query 重取。
> **Money**(§5.2):`reward_cents`、`min_cents`、`max_cents`、`new_balance` 均为整数 USD 分,按 `currency_code('USD')` 格式化为 `$x.xx`,**禁浮点重算**;奖励范围用 `min_cents`–`max_cents` 展示「随机 $x.xx–$y.yy」。
> **UX goals**:① 当月日历网格,据 `records[].checkin_date` 标记已签日;② 主签到按钮:`checked_in_today==true` 时禁用并显示「今日已签」,`enabled==false` 时整页显示功能未开启;③ 点击领取走 `POST`,成功后用 `new_balance` 即时刷新余额、用 `reward_cents` 弹奖励 toast;④ 处理 409(已签)与 404(禁用)各自的友好文案;⑤ 月份切换器(上/下月)通过 `month` 参数,默认当前 UTC 月,UTC 口径文案提示「按 UTC 日历日,每日仅一次」;⑥ 签到是钱相关 mutation,等服务端确认再更新余额,禁乐观更新(§5.7)。
> 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/checkin.ts typed 调用 + 正确 app/(portal)/checkin 路由段。

---

> **切片实现备注(给前端工程):**
> - `pricing` tag 端点(`GET /v1/pricing/page`、`/v1/pricing/rate-table?version=`、`/v1/pricing/snapshots`、`/v1/pricing/snapshots/{snapshot_id}`)均为 **public、`security:[]`**,主要服务公开 `/pricing` 页与 `/audit` 回执校验;本钱循环四页不直接渲染定价目录,故未在上方各页展开。若 `/billing` 需要在收据旁显示历史 rate table(`RateTable{id,version,pricing_data,effective_from,effective_to,created_at}` / `RateTableSnapshotList{snapshots[]}`),可只读消费这些 public 端点,无需鉴权。
> - 充值有**两条独立入口**且单位不同,务必区分:`POST /v1/users/me/recharges`(`user-recharges`,**decimal string** amount,泛 provider)与 `POST /v1/users/me/payments/orders`(`user-payments`,**amount_cents** 整数分,provider 仅 manual|taobao)。按后端实际启用的 provider 走其一,UI 不要把两套金额单位混算。
> - 整切片所有写操作(recharge / top-up / voucher redeem / subscribe / change-plan / cancel-renew / refund-request / checkin claim)都属钱循环,统一遵守 §5.5 幂等(带 idempotency_key,recharge/voucher 显式支持)、§5.7 服务端确认后更新、destructive 操作确认对话框。

---

## S4 用户审计 / 通知 / 公开页提示词

> 切片范围:`/notifications`(user)、`/audit`(user)、公开 `/pricing`、`/rankings`、`/trust`。
> openapi tags 实读自 `~/wt/h-fixes/docs/openapi/openapi.yaml`:`user-notifications`、`announcements`、`user-audit`、`audit`、`trust`、`pricing`(+ `/v1/public/rankings` 在 openapi 里实际打 `public` tag,但它是 `/rankings` 页唯一契约,故归本切片)。
> 框架规则引用自 `FRONTEND-BUILD-SPEC.md`:§3 auth 三守卫、§5.1 错误码映射、§5.2 钱、§5.4 分页/过滤、§5.6 i18n。所有 path/字段/枚举均取自契约真值,未猜测。

---

### /notifications

> 设计并实现 HUAKAI **user-portal** 的 **`/notifications`** 页面(Next.js 15 App Router + Tailwind 4 + TanStack Query)。受众:已登录付费用户。Auth guard:**user**(§3 session/key)。页面目标:统一的「站内通知收件箱 + 租户公告 + 通知渠道设置」三合一中心,左侧/上方是收件箱与未读角标,右侧/下方是公告流,单独 tab 是通知投递设置。
>
> **Endpoints**
> - `GET /v1/notifications` (`listMyNotifications`) — 用户收件箱,newest first。查询参数(§5.4 真实名):`unread_only`(boolean, 默认 false)、`limit`(1–100, 默认 50)、`offset`(≥0, 默认 50→实为 0)。响应 `UserNotificationListResponse`:`object`("notification_list")、`items[]`、`limit`、`offset`;每条 `UserNotification`:`id`、`tenant_id`、`user_id`、`title`、`body`、`severity`(枚举 `info|warning|critical`)、`read_at`(可空,未读时缺省)、`created_by_admin`、`created_at`。**注意是 offset 分页,不是 cursor。**
> - `GET /v1/notifications/unread-count` (`getMyNotificationUnreadCount`) — 顶部铃铛角标。响应 `UserNotificationUnreadCountResponse`:`object`("notification_unread_count")、`count`(int64)。
> - `POST /v1/notifications/{id}/read` (`markMyNotificationRead`) — 标记单条已读;`id` 为 int64 path。响应回 `UserNotification`(带 `read_at`)。无「全部已读」端点,逐条调用。
> - `GET /v1/announcements` (`listAnnouncements`) — 当前租户活跃公告(已发布、未过期),按 `published_at` 倒序。`security: []` 但有 session 时用 session 租户;否则需传 `tenant_id`(int64 query)。其余参数 `limit`(1–100, 50)、`offset`。响应 `AnnouncementListResponse`:`object`("announcement_list")、`items[]`(`Announcement`:`id`、`title`、`body`、`severity`、`active`、`published_at`、`expires_at`、`created_at`、`updated_at`)。
> - `GET /v1/users/me/notifications` (`getMyNotificationSettings`) — 读取本人通知投递设置。响应 `NotificationSettings`:`notify_type`(枚举 `none|email|webhook|bark|gotify`)、`webhook_url`、`webhook_secret_configured`(bool,密钥永不回传)、`notification_email`、`bark_url`、`gotify_url`、`gotify_token_configured`(bool)、`gotify_priority`(0–10, 默认 5)、`balance_threshold`(decimal string `^[0-9]+(\.[0-9]{1,8})?$`,低余额触发阈值)、`updated_at`、`updated_by`。
> - `PUT /v1/users/me/notifications` (`updateMyNotificationSettings`) — 整体替换设置。请求体 `NotificationSettingsUpdate`:必填 `notify_type`;`webhook_secret`/`gotify_token` 为 `writeOnly`(只写不回读);切到某渠道需带对应目标字段。
>
> **States**
> - loading:收件箱/公告骨架行;铃铛角标独立轻量轮询。
> - empty:无通知/无公告分别给空态文案。
> - error(§5.1 映射):`400`(BadRequest,参数越界)、`401`(Unauthorized → 跳登录)、`404`(标记已读时通知不存在)、`503`(ServiceBusy → "服务繁忙,稍后重试" toast)。绝不展示原始 500 body。
> - 分页:收件箱与公告均为 **offset/limit**;表/列表底部「加载更多」或页码,服务端分页参数透传。
>
> **Money/Streaming**:仅设置页含 1 个钱字段 —— `balance_threshold` 是 numeric(20,8) decimal string(§5.2),表单按字符串收发,禁止 float 解析;校验 `^[0-9]+(\.[0-9]{1,8})?$`。无流式。
>
> **UX goals**:① 未读以 `severity` 配色徽章(info/warning/critical)区分,点开即调 `POST .../read` 并乐观更新角标(注:钱无关,可乐观,§5.7);② 公告与个人通知视觉区隔(公告是租户级广播,只读);③ 通知设置切换 `notify_type` 时动态显示对应目标字段(webhook 显 url+secret,gotify 显 url+token+priority,bark 显 url),`*_configured`/`writeOnly` 语义:已配置只显示「已设置」占位,留空不改、填入则覆盖;④ `balance_threshold` 旁注明「余额低于此值时按所选渠道通知」。
>
> 类型来自 `lib/api/schema.d.ts`(由 openapi 生成),禁止手写 shape;交付页面组件 + `lib/api/notifications.ts` typed 调用 + 正确 `app/(portal)/notifications/` 路由段。

---

### /audit

> 设计并实现 HUAKAI **user-portal** 的 **`/audit`** 页面(Next.js 15 App Router + Tailwind 4 + TanStack Query)。受众:已登录付费用户。Auth guard:**user**(`sessionBearerAuth`,§3)。页面目标:用户的「可验证收据 + 成本争议 + 密钥管理审计」自助中心 —— 按 `request_id` 拉取签名成本收据并本地/远端验签,对收据发起争议,查看自己的争议状态,并审阅本人 API key 签发/吊销审计流水。
>
> **Endpoints**
> - `GET /v1/me/audit-events` (`listMyAuditEvents`) — 本人 API key 管理审计事件(append-only)。`sessionBearerAuth`;参数 `limit`(1–200, 默认 50)、`offset`(≥0)。响应 `UserAuditEventListResponse`:`audit_events[]`、`count`;每条 `UserAuditEvent`:`id`、`action`(枚举 `issue_api_key|revoke_api_key`)、`outcome`(枚举 `committed|denied|error`)、`api_key_id`(可空)、`key_prefix`(仅非密前缀,绝无明文/hash)、`reason`、`request_id`、`occurred_at`。
> - `GET /v1/receipts/{request_id}` (`getCostReceipt`) — 按 `request_id`(path, ≤256)拉签名收据。`sessionBearerAuth`。响应 `UserCostReceipt`:`schema_version`(`audit.receipt.v1|v2`)、`request_id`、`receipt_sequence`(int32,退款用更高序号)、`tenant_scope_ref`、`occurred_at`、`cost`(`UserReceiptCost`:`model`、`input_tokens`、`output_tokens`、`cached_tokens`、`cost_total_micro_usd` int64、`rate_table_snapshot_id`)、`validation_state`(枚举 `valid|provisional|mismatch_pending|mismatch_refunded|not_billable|receipt_unavailable`)、`verdict`(枚举 `match|substitution_refund|mismatch_refund_pending|unknown`)、`adjustment_refs[]`、`canonical_hash`(hex SHA-256)、`signature`(base64 ed25519)、`pubkey_fingerprint`。**`202`**:收据事实存在但终态用量未就绪(返回 `ErrorResponse`,UI 显示「结算中,稍后再看」)。
> - `POST /v1/receipts/{request_id}/verify` (`verifyCostReceipt`) — 对调用方提供的收据字节做脱离式验签(`security: []`,仅验字节不查存在性)。请求体 = `UserCostReceipt`。响应 `ReceiptVerifyResponse`:`valid`(bool)、`key_status`(`active|rotated|revoked|unknown`)、`reason`(枚举:`schema_unsupported|receipt_unsigned|invalid_signature|invalid_public_key|unsupported_algorithm|unknown_signer|signature_mismatch|key_revoked|canonical_hash_mismatch|signature_outside_key_window`)、`age_seconds`、`receipt_sequence`、`verdict`、`state`、`fields_mismatch[]`/`mismatch_fields[]`(枚举:`cost_total_micro_usd|model|input_tokens|output_tokens|cached_tokens|rate_table_snapshot_id`)、`delta_micro_usd`、`refund_event_id`。`413`:体超 10KB。
> - `POST /v1/receipts/{request_id}/disputes` (`createCostDispute`) — 对本人收据开成本争议(不退款、不改账本)。`sessionBearerAuth`;请求体 `CreateCostDisputeRequest`:必填 `reason`(1–4000 字符)。`201` 返回 `{ dispute: CostDispute }`。`409`:该 tenant/user/request 已有争议。
> - `GET /v1/me/disputes` (`listMyCostDisputes`) — 列本人争议。`sessionBearerAuth`;参数 `limit`(1–500, 默认 100)。响应 `{ disputes: CostDispute[] }`;`CostDispute`:`id`、`dispute_id`、`request_id`、`reason`、`status`(枚举 `open|reviewing|resolved|rejected`)、`operator_note`、`created_at`、`resolved_at`(可空)。
> - 可选交叉验签锚点(同属 `audit` tag,本页用于「验真」按钮的公钥/链证据,均 `security: []`):`GET /v1/audit/pubkey`(`getAuditPubkey` → `AuditPubkeyResponse`:`algorithm`("ed25519")、`fingerprint`、`pubkey_fingerprint`、`public_key_base64`、`key_status`)、`GET /v1/audit/verify?request_id=&tenant_scope_ref=`(`getAuditVerify` → `AuditVerifyResponse`:`ledger_entry`{`ledger_id`、`timestamp`、`request_id`、`tenant_scope_ref`、`hop_chain[]`、`model_chain`}、`chain_proof`{`prev_merkle_root`、`merkle_root`、`signature`、`pubkey_fingerprint`、`signature_valid`、`key_status`})、`GET /v1/audit/proof/{request_id}.json`(`downloadAuditProof`,带 `Content-Disposition` 下载)。
>
> **States**
> - loading:审计流水表骨架;按 request_id 查收据时单卡加载态。
> - empty:无审计事件 / 无争议 / 输入 request_id 但 `404` 未找到收据。
> - error(§5.1):`400`、`401`(→登录)、`404`(NotFound,收据/请求不存在)、`409`(争议已存在 → 引导查看已有争议)、`413`(验签体过大)、`503`;收据接口的 `202` 当作「结算中」非错误态展示。绝不裸露原始错误体。
> - 分页:审计事件用 **offset/limit**;争议列表用 **limit only**(无 offset,服务端截断到 100/500)。
>
> **Money/Streaming**:收据成本字段为 **micro-USD**(§5.2):`cost.cost_total_micro_usd` 是 int64 micro-USD,展示时除以 1e6 取 USD 并保留 2–6 有效位,严禁在 UI 重算 token×rate(后端已给终值);`delta_micro_usd` 同理。无流式。
>
> **UX goals**:① 收据卡明显展示 `validation_state`/`verdict` 徽章 + 「本地验签」按钮(调 `verify`,把 `reason`/`fields_mismatch` 翻成人话);② `canonical_hash`/`signature`/`pubkey_fingerprint` 用等宽 + 一键复制,并提供「下载证明 bundle」(`/v1/audit/proof/{id}.json`);③ 发起争议是 destructive-ish 写操作,需确认对话框 + 等服务端确认(§5.7,非乐观),`409` 时不报错而是跳到已有争议;④ key 审计流水用状态徽章区分 `outcome`,`key_prefix` 只读不可还原明文。
>
> 类型来自 `lib/api/schema.d.ts`(由 openapi 生成),禁止手写 shape;交付页面组件 + `lib/api/audit.ts` typed 调用 + 正确 `app/(portal)/audit/` 路由段。

---

### /pricing

> 设计并实现 HUAKAI **public** 的 **`/pricing`** 页面(Next.js 15 App Router + Tailwind 4 + TanStack Query)。受众:匿名访客(无需登录)。Auth guard:**public**(所有端点 `security: []`)。页面目标:面向客户的公开模型价目表 —— 列出启用模型别名、规范 id、公开单价、上下文窗口;并提供「历史费率表版本」入口供老收据校验。
>
> **Endpoints**
> - `GET /v1/pricing/page` (`getPublicPricingPage`) — 公开价目页主数据,无需 token、无 `version` 参数。响应是 `PublicPricingPageItem[]`:`model`(公开别名)、`canonical_id`(规范模型 id)、`input_price_per_token`(decimal string `^[0-9]+(\.[0-9]+)?$`,USD/token)、`output_price_per_token`(同型)、`context_length`(int)。**只暴露这几项,绝无 actual cost / 内部 ratio / provider 信息。**
> - `GET /v1/pricing/snapshots` (`listPricingSnapshots`) — 历史费率表版本列表(用于老收据验证)。响应 `RateTableSnapshotList`:`snapshots[]`,每条 `RateTableSnapshot`:`id`(int64)、`version`、`effective_from`、`effective_to`(可空)、`created_at`。
> - `GET /v1/pricing/snapshots/{snapshot_id}` (`getPricingSnapshot`) — 按 `snapshot_id`(int64 path)取历史费率表。响应 `RateTable`:`id`、`version`、`pricing_data`(任意对象)、`effective_from`、`effective_to`(可空)、`created_at`。
> - `GET /v1/pricing/rate-table?version=` (`getPricingRateTable`) — 按 `version`(string, 必填)取历史费率表。响应同 `RateTable`。
>
> **States**
> - loading:价目表骨架行。
> - empty:无启用模型时友好空态。
> - error(§5.1):`400`(BadRequest,如缺 `version`)、`404`(NotFound,版本/快照不存在)、`503`(ServiceBusy → "价格暂不可用,稍后重试";对应框架 §5.1 提到的 `pricing_unavailable` 语义)。无 401(公开页)。
> - 分页:价目主页无分页(返回全量数组);snapshots 列表为全量返回,前端可本地搜索/排序。
>
> **Money/Streaming**:价格为 **decimal string per token**(§5.2):`input_price_per_token`/`output_price_per_token` 按字符串展示,可乘 1e6 换算成「USD / 1M tokens」更直观,但**展示用字符串格式化,禁止 float 重算导致精度丢失**;明确标注币种 USD。无流式。
>
> **UX goals**:① 价目表服务端排序不可用 → 前端按 model/价格本地排序 + 搜索框过滤;② 每行同时给「per token」与「per 1M tokens」两种读数,清晰标注 USD;③ 提供「历史费率表」抽屉/页签:选 version 或 snapshot_id 拉 `RateTable`,把 `pricing_data` 用 JSON viewer 展示(供用户核对老收据);④ 公开页强调可信任、对比清晰(§8 public 风格:bold/clear)。
>
> 类型来自 `lib/api/schema.d.ts`(由 openapi 生成),禁止手写 shape;交付页面组件 + `lib/api/pricing.ts` typed 调用 + 正确 `app/(public)/pricing/` 路由段。

---

### /rankings

> 设计并实现 HUAKAI **public** 的 **`/rankings`** 页面(Next.js 15 App Router + Tailwind 4 + TanStack Query)。受众:匿名访客。Auth guard:**public**(`security: []`)。页面目标:平台级公开「模型使用排行榜」,只展示相对使用量(请求数/token 总量/请求占比),作为公信力与热度展示。
>
> **Endpoints**
> - `GET /v1/public/rankings` (`getPublicRankings`) — 公开模型使用排名,无需 token。参数 `limit`(1–100, 默认 20,>100 截断到 100)。响应(openapi 内联,非 $ref):`scope`(枚举固定 `platform`)、`metric`(枚举固定 `request_count`)、`rankings[]`,每条:`rank`(≥1)、`model`(string)、`request_count`(int64)、`token_total`(int64)、`request_share`(decimal string,六位定点 `0.000000`–`1.000000`,占返回集请求量的份额)。**绝无 actual_cost / total_cost / user/api-key/provider 身份字段。**
>   - ⚠ 该端点在 openapi 里实际打的是 `public` tag(非本切片标称的六个 tag 之一),但它是 `/rankings` 页唯一后端契约,故按页归此切片;`lib/api/` 仍按生成类型调用。
>
> **States**
> - loading:排行榜骨架行/条形图骨架。
> - empty:无使用数据时空态。
> - error(§5.1):`400`(BadRequest,limit 非法)、`503`(ServiceBusy)。无 401/404。
> - 分页:无 cursor/offset,仅 `limit` 控制条数;前端给 20/50/100 档位切换。
>
> **Money/Streaming**:**无钱字段**(端点刻意不暴露成本)。`request_share` 是六位定点 decimal string,按字符串渲染百分比(×100 仅用于展示),不参与金额计算。无流式。
>
> **UX goals**:① 排行榜以横向条形图/带 rank 徽章的表格呈现,`request_share` 直观转成百分比并配进度条;② `request_count`/`token_total` 千分位格式化(纯展示,非金额);③ 顶部标注 "scope=platform · metric=request_count" 让口径透明;④ limit 档位切换即时重查;⑤ public 视觉(§8 bold/clear),可作为信任背书放首页引流。
>
> 类型来自 `lib/api/schema.d.ts`(由 openapi 生成),禁止手写 shape;交付页面组件 + `lib/api/rankings.ts` typed 调用 + 正确 `app/(public)/rankings/` 路由段。

---

### /trust

> 设计并实现 HUAKAI **public** 的 **`/trust`** 页面(Next.js 15 App Router + Tailwind 4 + TanStack Query)。受众:匿名访客 / 任何想独立核验的人。Auth guard:**public**(`security: []`)。页面目标:透明度 / 可验证审计中心 —— 展示当前签名公钥(JWK)、公开 Merkle 链头,并提供「粘贴收据 → 脱离式验签」工具,让任何人无需账号即可验证 HUAKAI 出具的 trust receipt 真伪。
>
> **Endpoints**
> - `GET /.well-known/huakai-pubkey.json` (`getWellKnownHuakaiPubkey`) — 公钥发现文档(供客户端验签)。带 `Cache-Control: public, max-age=300, ...`。响应 `WellKnownPubkeyDocument`:`schema_version`("huakai.pubkey.v1")、`generated_at`、`next_rotation_after`、`keys[]`(`WellKnownPubkeyJWK`:`kty`("OKP")、`crv`("Ed25519")、`kid`、`x`(base64url 公钥)、`alg`("EdDSA")、`use`("sig")、`status`(`active|rotated|revoked|unknown`)、`effective_from`、`effective_to`、`revoked_at`、`reason_class`)、`current`(活跃 key 指纹)、`revoked[]`(`WellKnownPubkeyRevocation`:`fingerprint`、`revoked_at`、`reason_class`)。
> - `POST /v1/trust/verify` (`verifyTrustReceiptSignature`) — 脱离式验签 trust 收据。请求体 `TrustVerifyRequest`:必填 `payload`(收据对象 或 base64 canonical 字节,oneOf)、`signature`(base64)、`pubkey_fingerprint`(16 位 hex)。响应 `TrustVerifyResponse`:`valid`(bool)、`status`(枚举 `signed-only|mismatch|missing|unverified`)、`signature_valid`(bool)、`key_status`(`active|rotated|revoked|unknown`)、`reason`(枚举:`invalid_json|required_field_missing|payload_invalid|unknown_signer|invalid_signature|invalid_public_key|signature_mismatch|key_revoked|signature_outside_key_window`)、`fields_mismatch[]`、`canonical_hash`(`^$|^[0-9a-f]{64}$`)、`schema_version`("trust.receipt.v1")。
> - `GET /v1/audit/merkle-tree.json` (`getAuditMerkleTree`,`audit` tag,`security: []`) — 公开 Merkle 链头。响应 `AuditMerkleTree`:`latest_merkle_root`(64-hex)、`size`(append-only 账本条数)。
> - 辅助公钥端点(`audit` tag, `security: []`):`GET /v1/audit/pubkeys` (`listAuditPubkeys` → `AuditPubkeysResponse.keys[]` of `AuditPubkeyResponse`),`GET /v1/audit/pubkey/{fingerprint_hex}` (`getAuditPubkeyByFingerprint`,`fingerprint_hex` 须 16-hex)。
>
> **States**
> - loading:公钥卡 / Merkle 头卡 / 验签结果区分别骨架。
> - empty:`current` 为空时提示「当前无活跃签名 key」。
> - error(§5.1,trust/audit 端点用专属 `TrustErrorResponse`{`error`,`message`} 或 `ErrorResponse`):验签 `400`(请求体无法解码)、`413`(体超 10KB)、`429`(匿名验签限流 → "请求过于频繁,请稍后" toast);公钥/Merkle 端点 `503`。注意 `verifyTrustReceiptSignature` 即便字段缺失也返回 `200` + 负面 verdict(`status=missing`),UI 要把 `200` 的失败 verdict 当「验证未通过」展示而非报错。
> - 分页:无(单文档/单结果)。
>
> **Money/Streaming**:**无钱字段**。无流式。
>
> **UX goals**:① 验签工具:粘贴收据 JSON(或 base64)+ signature + fingerprint,提交后用大号 `valid` 通过/失败徽章 + `status`/`reason`/`key_status` 人话化解释,`fields_mismatch` 列出差异字段,`canonical_hash` 等宽可复制;② 公钥区把 `keys[]` 以指纹卡列出,`current` 高亮、`revoked` 灰显并标 `reason_class`;③ Merkle 头展示 `latest_merkle_root`(可复制)+ `size`,配「append-only 账本」说明,体现不可篡改;④ 整页强调「无需账号即可独立验证」的信任叙事(§8 trust 主题);⑤ 验签是匿名接口,处理 `429` 限流时给冷却倒计时,避免连点。
>
> 类型来自 `lib/api/schema.d.ts`(由 openapi 生成),禁止手写 shape;交付页面组件 + `lib/api/trust.ts` typed 调用 + 正确 `app/(public)/trust/` 路由段。

---

## S5 — Admin 运营核心1:Providers / Channels / Accounts

> 切片范围:`/admin/providers`、`/admin/channels`、`/admin/accounts`。负责 openapi tags:`admin-models`(实际驱动这三页的目录/账户 path 落在 `admin-provider-catalog`、`admin-channel-catalog`、`admin-accounts`、`admin-channel-health` 四个 tag 下 —— §4C 的页面映射用了 catalog 端点,这里照真实端点接线)、`admin-channel-health`、`admin-accounts`。
>
> 全切片通用约束(每页提示词均已内联引用):auth guard = **admin**(§3,整个 admin surface 对非 admin 不渲染);所有 list 端点服务端分页/过滤(§5.4);钱/destructive mutation 必须等服务端确认、不做乐观 UI(§5.7);错误码按 §5.1 映射到本地化 toast/inline,禁止裸 500;**类型来自 `lib/api/schema.d.ts`(由 openapi 生成),禁止手写 shape**。
>
> 关于 `tenant_id`:本切片每个端点都接受 `tenant_id` query —— **platform_admin 必须传**,`tenant_operator` 可省略(用其 admin token 的租户 scope)。UI 需要一个全局「当前租户选择器」(platform_admin 用)并把选中的 `tenant_id` 注入所有调用;tenant_operator 隐藏该选择器。`channel-health` 的几个 `/v1/admin/channel-health*` 端点 `tenant_id` 为 **required**(连 tenant_operator 也得带)。

---

### /admin/providers

> Design and implement the **`/admin/providers`** page for the HUAKAI **admin** console — Next.js 15 App Router + Tailwind 4 + TanStack Query, route under `app/(admin)/admin/providers/`. Audience: platform operators only; auth guard = **admin** (§3 — never render to non-admins). Purpose: a tenant-scoped **provider/vendor catalog CRUD** (the upstream-protocol directory that provider-accounts attach to). Catalog metadata only — it never exposes credentials/secrets.
>
> **Endpoints** (tag `admin-provider-catalog`; all accept `tenant_id` query — platform_admin required, tenant_operator may omit):
> - `GET /admin/v1/providers` (`listAdminProviders`) — paginated provider directory. Query params: `tenant_id`, `limit` (1–500, default 50), `offset` (≥0, default 0). Response `AdminProviderCatalogList`: `{ object: "admin_providers_list", items: AdminProviderCatalogItem[], limit, offset }`. Each item: `id`, `code`, `display_name`, `upstream_protocol`, `enabled`, `created_at`.
> - `POST /admin/v1/providers` (`createAdminProvider`) — create. Body `AdminProviderCatalogCreateRequest`: required `code` (minLen 1, unique per tenant among non-deleted), `display_name` (minLen 1), `upstream_protocol` (enum, see below), `enabled` (bool); optional `reason` (operator-visible audit reason). Returns 201 `AdminProviderCatalogItem`.
> - `PUT /admin/v1/providers/{code}` (`updateAdminProvider`) — update. Body `AdminProviderCatalogUpdateRequest`: required `display_name`, `upstream_protocol`, `enabled`; optional `reason`. Note: `code` is immutable (path key, not in body). Returns 200 `AdminProviderCatalogItem`.
> - `DELETE /admin/v1/providers/{code}` (`deleteAdminProvider`) — soft-delete. Optional body `{ reason }`. Returns `{ object: "admin_provider_deleted", id, code, deleted: true }`. **Guarded:** rejected with `409 Conflict` if the provider still has enabled, non-deleted provider-accounts (don't orphan references).
> - `upstream_protocol` enum (`AdminProviderUpstreamProtocol`) — render as a grouped select, exact values: `anthropic_messages`, `openai_chat`, `openai_responses`, `openai_codex`, `gemini`, `gemini_messages`, `bedrock`, `bedrock_invoke`, `openrouter_chat`, `grok_chat`, `deepseek_chat`, `mistral_chat`, `groqcloud_chat`, `together_chat`, `perplexity_chat`, `fireworks_chat`, `anthropic_claude_session`, `cursor_session`, `copilot_session`, `gemini_advanced_session`, `antigravity`, `antigravity_session`, `kiro_session`, `windsurf_session`.
>
> **States**:
> - loading: table skeleton.
> - empty: "尚无 Provider" CTA → 打开创建对话框。
> - error (§5.1 mapping, render localized toast/inline, never raw body): `400 BadRequest` → 表单字段校验 + inline；`401 Unauthorized` → 重新登录；`403 Forbidden` → "无权限/需 platform_admin"；`409 Conflict` on create → "code 已存在(同租户)",on delete → "该 Provider 仍有启用中的账户,先停用/迁移再删除";`503 ServiceBusy` → "服务繁忙,请稍后重试"。
> - pagination: server-side `limit`/`offset`(offset 翻页),表格底部页码 + 每页条数选择(default 50)。
>
> **Money/Streaming**: 不涉及(纯目录元数据)。
>
> **UX goals**:
> - 服务端分页表格,列:`code`(等宽 mono)、`display_name`、`upstream_protocol`(badge,按协议族着色:anthropic / openai / gemini / session 类)、`enabled`(开关式 badge)、`created_at`(本地化时间)。
> - 创建/编辑用同一抽屉/对话框;编辑时 `code` 只读。`reason` 作为可选「审计原因」单行输入,提示「成功的变更会写 admin 审计事件」。
> - `enabled` 的切换走 PUT(全量更新),切换前不需要二次确认;**删除是 destructive,必须确认对话框**,并在文案里说明它是软删除且若有启用账户会被拒绝(把 409 文案前置提示)。
> - tenant 选择器(platform_admin 可见)切换后刷新整张表;tenant_operator 不显示。
>
> 类型来自 `lib/api/schema.d.ts`(由 openapi 生成),禁止手写 shape;交付页面组件 + `lib/api/admin-providers.ts` typed 调用 + 正确的 `app/(admin)/admin/providers/` 路由段。

---

### /admin/channels

> Design and implement the **`/admin/channels`** page for the HUAKAI **admin** console — Next.js 15 App Router + Tailwind 4 + TanStack Query, route under `app/(admin)/admin/channels/`. Audience: platform operators only; auth guard = **admin** (§3). Purpose: tenant-scoped **channel catalog**(每个 channel = 一条 `pool_group → failover_status_codes` 路由项)+ **channel test templates**(运营自定义的健康检查请求模板,只存储不执行付费上游请求,永不返回凭据)。两块用 tab/分栏组织在同一页。
>
> **Endpoints** (tag `admin-channel-catalog`; all accept `tenant_id` query — platform_admin required, tenant_operator may omit):
>
> Channels:
> - `GET /admin/v1/channels` (`listAdminChannels`) — paginated. Query: `tenant_id`, `limit`(1–500, default 50), `offset`(≥0). Response `AdminChannelCatalogList`: `{ object: "admin_channels_list", items: AdminChannelCatalogItem[], limit, offset }`. Item: `id`, `pool_group_id`, `name`, `failover_status_codes` (int[]), `enabled`, `created_at`.
> - `POST /admin/v1/channels` (`createAdminChannel`) — body `AdminChannelCatalogCreateRequest`: required `pool_group_id`(int64 ≥1,必须属同租户)、`name`(minLen 1,unique per (tenant, pool_group))、`enabled`;可选 `failover_status_codes`(int[100–599],**省略时后端默认 [401, 403, 429, 529]**)、`reason`。返回 201 `AdminChannelCatalogItem`。
> - `PUT /admin/v1/channels/{id}` (`updateAdminChannel`) — body `AdminChannelCatalogUpdateRequest`(同 create:required `pool_group_id`、`name`、`enabled`;可选 `failover_status_codes`、`reason`)。返回 200。
> - `DELETE /admin/v1/channels/{id}` (`deleteAdminChannel`) — soft-delete,可选 `{ reason }`。返回 `{ object: "admin_channel_deleted", id, deleted: true }`。删除后 (tenant, pool_group, name) 元组可复用。
>
> Channel test templates:
> - `GET /admin/v1/channel-test-templates` (`listAdminChannelTestTemplates`) — paginated;同 `tenant_id`/`limit`/`offset`。Response `AdminChannelTestTemplateList`:`{ object: "admin_channel_test_templates_list", items[], limit, offset }`。Item `AdminChannelTestTemplateItem`:`id`、`tenant_id`、`name`、`method`(enum GET/POST/PUT/PATCH/DELETE)、`path`、`body_template`、`headers`(JSON object,非凭据头)、`created_at`。
> - `POST /admin/v1/channel-test-templates` (`createAdminChannelTestTemplate`) — body `AdminChannelTestTemplateRequest`:required `name`(1–128)、`method`(enum)、`path`(1–2048);可选 `body_template`(default "")、`headers`(object,default {})。**服务端拒绝携带凭据的头(Authorization、Cookie 等)** → 422/400。返回 201。
> - `GET /admin/v1/channel-test-templates/{id}` (`getAdminChannelTestTemplate`) — 单条详情。
> - `PUT /admin/v1/channel-test-templates/{id}` (`updateAdminChannelTestTemplate`) — 同 create body。
> - `DELETE /admin/v1/channel-test-templates/{id}` (`deleteAdminChannelTestTemplate`) — 返回 `{ object: "admin_channel_test_template_deleted", id, deleted: true }`。
> - ⚠ 无后端契约:模板**执行**(实际发起测试请求拿结果)目前无对应端点(openapi 明确「only reads stored templates; execution is an explicit future action」)。UI 只做模板的 CRUD,不要伪造一个「运行测试」按钮的后端;如需展示渠道健康用 `/admin/accounts` 页的 channel-health 数据。
>
> **States**:
> - loading: 两个 tab 各自表格 skeleton。
> - empty: channels 空 → "尚无渠道"CTA;templates 空 → "尚无测试模板"CTA。
> - error (§5.1): `400` → 表单 inline(尤其 templates 的凭据头被拒,文案明确「不能包含 Authorization/Cookie 等凭据头」);`401`/`403` 同 providers 页;`409 Conflict` on channel create/update → "同租户同 pool_group 下 name 重复" / "pool_group 不属于该租户";`503` → 繁忙重试。
> - pagination: 两个表格各自 `limit`/`offset` 服务端分页。
>
> **Money/Streaming**: 不涉及。
>
> **UX goals**:
> - Channels 表:列 `name`、`pool_group_id`(可链接到 `/admin/pools`)、`failover_status_codes`(渲染为 HTTP code chips,创建表单未填时灰色提示默认 [401,403,429,529])、`enabled`、`created_at`。
> - `failover_status_codes` 编辑用 chip/多值输入,校验 100–599;`pool_group_id` 用下拉(从 pools 列表拉,跨页可暂用数字输入 + 校验)。
> - Templates 表:列 `name`、`method`(badge)、`path`(mono)、`headers` 数量、`created_at`;编辑器里 `body_template` 用 JSON/文本区,`headers` 用 key-value 编辑器并**前端先拦截凭据头**(给出与后端一致的拒绝提示,避免来回 400)。
> - 删除均为 destructive → 确认对话框;`reason` 作为可选审计原因;提示成功变更写 admin 审计事件。
> - tenant 选择器同 providers 页规则。
>
> 类型来自 `lib/api/schema.d.ts`(由 openapi 生成),禁止手写 shape;交付页面组件 + `lib/api/admin-channels.ts` typed 调用 + 正确的 `app/(admin)/admin/channels/` 路由段。

---

### /admin/accounts

> Design and implement the **`/admin/accounts`** page for the HUAKAI **admin** console — Next.js 15 App Router + Tailwind 4 + TanStack Query, route under `app/(admin)/admin/accounts/`. Audience: platform operators only; auth guard = **admin** (§3). Purpose: **上游 provider-accounts**(运营核心)的列表/创建/编辑、启停、限额/并发、健康与凭据状态、按 tag 批量、cooldown 清理、干跑凭据校验,以及 channel-health 的人工覆盖(pause/resume/force-active)。**凭据字段写时一次性、GET 永不回显**(write-only),健康/详情端点永不返回密文。
>
> **Endpoints**:
>
> Accounts core (tag `admin-accounts`, role `tenant_operator`):
> - `GET /admin/v1/provider-accounts` (`listProviderAccounts`) — **cursor 分页**。Query: `cursor`(不透明 base64,取自上一页 `page.next_cursor`)、`limit`(1–100,default 50)、`pool_group_id`(int64,可选过滤)、`state_filter`(string,枚举之一:`active`/`error`/`disabled`/`rate_limited`/`overloaded`/`temp_unschedulable`)。Response `ProviderAccountList`:`{ items: ProviderAccount[], page: { cursor, next_cursor, has_more } }`。
> - `POST /admin/v1/provider-accounts` (`createProviderAccount`) — body `ProviderAccountCreate`:required `provider_id`、`channel_id`、`name`(minLen 1)、`account_type`(enum `oauth`/`api_key`/`service_account`/`upstream_static`/`session`)、`credentials`(object,**WRITE-ONLY**,按 account_type 不同字段,如 oauth:`access_token`/`refresh_token`/`expires_at` + provider 专属字段);可选 `vendor`+`auth_mode`(credential-v2,须成对)、`cap_concurrency`(≥1,default 4)、`priority`(default 100)、`model_allow_list`(string[])、`capability_flags`(string[])、`confirm`(bool)。Query `confirm=true` 仅在运营复核后用于确认高风险混渠道创建。返回 201 `ProviderAccount`;`400` 可能是 `ProviderAccountCreate` 校验错 **或** `MixedChannelRiskRequired`(见下)。
> - `GET /admin/v1/provider-accounts/{id}` (`getProviderAccount`) — 详情(credentials 永远 redacted)。
> - `PATCH /admin/v1/provider-accounts/{id}` (`updateProviderAccount`) — body `ProviderAccountUpdate`(全可选):`enabled`、`priority`、`cap_concurrency`(≥1)、`model_allow_list`、`capability_flags`、`custom_error_codes_enabled`、`custom_error_codes`(int[])、`pool_mode`、`temp_unschedulable_enabled`、`temp_unschedulable_rules`(每条 required `error_code`/`keywords`/`duration_minutes`≥1,可选 `description`)。
> - `DELETE /admin/v1/provider-accounts/{id}` (`deleteProviderAccount`) — 软删除(置 deleted_at,可恢复),可选 body `{ tenant_id, reason }`,写 `delete_provider_account` 审计。返回 `{ id, deleted }`。
> - `PATCH /admin/v1/provider-accounts/{id}/enabled` (`updateProviderAccountEnabled`) — body `ProviderAccountEnabledUpdate`:required `enabled`;可选 `tenant_id`、`reason`。返回 `{ id, enabled }`。(独立启停端点,优先用它做开关。)
> - `POST /admin/v1/provider-accounts/{id}/clear-rate-limit` (`clearProviderAccountRateLimit`) — 级联清 cooldown(`rate_limit_reset_at`+`overload_until`+`temp_unschedulable_until`+`model_rate_limits`+`openai_403_counter`),写审计,返回 **204**。
> - `POST /admin/v1/provider-accounts/{id}/test` (`testProviderAccountCredential`) — 对已存凭据做**干跑校验**。返回 `ProviderAccountTestResponse`:`ok`(bool)、`error_class`(安全枚举或 null:`invalid_grant`/`rate_limit_exceeded`/`risk_control_triggered`/`account_disabled`/`temporary`/`payload_invalid`/`operator_config_required`)、`message`(通用文案,无密文)。**注意:可能消耗上游配额/限额预算** —— UI 必须在按钮上明确警示。
> - `GET /admin/v1/provider-accounts/{id}/health` (`getProviderAccountHealth`) — `ProviderAccountHealthSnapshot`:`id`、`health_state`(enum `healthy`/`throttled`/`revoked`/`cooldown`)、`health_state_until`、`last_refresh_at`、`last_refresh_outcome`、`failure_class`、`failure_count`、`enabled`、`requires_action`(health=revoked 或 failure_count>3 为 true)、`updated_at`。
> - `POST /admin/v1/provider-accounts/bulk-by-tag` (`bulkUpdateProviderAccountsByTag`) — body `ProviderAccountBulkByTagRequest`:required `tag`;可选 `enabled`、`priority`、`static_weight`。返回 `{ affected_ids: int64[], count }`。
> - `GET /admin/v1/provider-accounts/{id}/upstream-models` (`listProviderAccountUpstreamModels`) — 按需拉该账户上游实际可服务的 model id(SSRF 防护)。返回 `{ models: string[], count }`;错误 `422`(地址被 SSRF 策略拦/base_url 非法)、`502`(上游错误/无法解析)。
> - 凭据元数据(role `platform_admin`,密文 write-only,GET 只回元数据):`GET .../{id}/credentials`(`listProviderAccountCredentials`,query `tenant_id` required → `AccountCredentialListResponse`/`AccountCredentialMetadata`:`vendor`/`auth_mode`/`state`/`credential_version`/`access_expires_at`/`refresh_before_at`/`failure_count`...)、`POST .../{id}/credentials`(`createProviderAccountCredential`)、`POST .../credentials/{credential_id}/rotate`(`rotateProviderAccountCredential`)、`PATCH .../credentials/{credential_id}/state`(`updateProviderAccountCredentialState`)、`DELETE .../credentials/{credential_id}`(`deleteProviderAccountCredential`)。
>
> Channel-health overrides (tag `admin-channel-health`, role `platform_admin`):
> - `POST /admin/v1/provider-accounts/{id}/channel-health/pause` (`pauseProviderAccountChannelHealth`) / `.../resume` (`resumeProviderAccountChannelHealth`) / `.../force-active` (`forceActiveProviderAccountChannelHealth`) — body `ChannelHealthOverrideRequest`:required `tenant_id`、`vendor`、`account_credential_id`、`credential_version`、`reason`(minLen 1)。返回 `ChannelHealthState`。
> - `GET /v1/admin/channel-health` (`listChannelHealth`) — query `tenant_id`(**required**)、`limit`(1–200,default 50)、`offset`。Response `ChannelHealthListResponse`:`{ items: ChannelHealthState[], limit, offset }`。
> - `GET /v1/admin/channel-health/summary` (`summarizeChannelHealth`) — query `tenant_id`(required)。Response `ChannelHealthSummaryResponse`:`{ by_state: { active, degraded, cooling_down, ramping, disabled, manual_paused }, total, oldest_cooldown_at }`。
> - `GET /v1/admin/channel-health/{channel_id}` (`getChannelHealth`) — query `tenant_id`(required)。`ChannelHealthDetailResponse`:`{ state: ChannelHealthState, audit_events: ChannelHealthAuditEvent[] (≤50, payload 已脱敏) }`。
> - `ChannelHealthState` 关键字段:`state`(enum `active`/`degraded`/`cooling_down`/`ramping`/`disabled`/`manual_paused`)、`score`(0–100)、`reason_class`(长枚举,如 `rate_limit`/`forbidden`/`token_revoked`/`account_disabled`/`manual_override`...)、`confidence_tier`(`observed`/`inferred`/`operator_override`)、`ramp_stage_pct`、`ramp_failure_count`、`cooldown_until`、`last_transition_at`。
>
> **ProviderAccount 读模型(列表/详情列)关键字段**:`id`、`tenant_id`、`provider_id`、`channel_id`、`name`、`account_type`、`enabled`、`health_state`(enum `operational`/`degraded`/`failed`/`cooling_down`/`error`)、`credential_state`(enum `valid`/`refreshing`/`refreshing_with_grace`/`refresh_failed`/`revoked`)、`cap_concurrency`、`in_flight_count`、`priority`、`last_dispatch_at`、`rate_limited_at`/`rate_limit_reset_at`/`rate_limit_reason`、`overload_until`、`temp_unschedulable_until`、`token_version`、`last_refresh_at`/`last_refresh_outcome`、`oauth_endpoint_health`(enum `operational`/`degraded`/`circuit_open`)、`pool_mode`、`model_allow_list`、`capability_flags`、`created_at`/`updated_at`。
>
> **States**:
> - loading: 表格 + 摘要卡 skeleton。
> - empty: "尚无账户"CTA → 创建抽屉。
> - error (§5.1,本地化,禁裸 body):`400` → 表单 inline;`400` 且 body 为 `MixedChannelRiskRequired`(`error: "mixed_channel_risk_confirmation_required"`,带 `risks[]`,每条 `dimension`(`source`/`vendor`/`credential_type`)+`existing_value`/`candidate_value`/`message`)→ **弹高风险确认对话框**逐条列出风险,确认后用 `confirm=true`(query)重发创建,**不要静默重试**;`401`/`403` → 重新登录/"需 platform_admin"(凭据与 channel-health 端点需 platform_admin);`404` → "账户不存在/已删除";`409`(凭据冲突场景)→ 对应文案;test/upstream-models 的 `422` → "上游地址被 SSRF 策略拦截或 base_url 非法",`502` → "上游不可达/响应无法解析";`503` → 繁忙重试。
> - pagination: **cursor 分页**(`page.next_cursor` + `has_more`,用「加载更多」或前进/后退 cursor 栈,**不是 offset**);保留 `pool_group_id` 与 `state_filter` 过滤器到 query。
>
> **Money/Streaming**: 不涉及计费金额;但涉及**配额/限额预算**——`/test` 与 `/upstream-models` 会触达上游,可能消耗 upstream 配额/触发限频,按钮需明确警示并加二次确认(§5.7:运营 mutation 等服务端确认、不乐观)。
>
> **UX goals**:
> - 顶部 channel-health 摘要卡(来自 `summary`:按 state 计数 + total + `oldest_cooldown_at`)。
> - 账户表:`name`、`account_type`(badge)、`provider_id`/`channel_id`(可链 providers/channels)、`enabled`(开关,走 `/enabled` 端点)、`health_state` + `credential_state`(状态 badge,色阶)、`in_flight_count / cap_concurrency`(并发占用)、`priority`、cooldown 指示(`rate_limit_reset_at`/`overload_until`/`temp_unschedulable_until` 有值时高亮 + 倒计时)。
> - 行操作:编辑(PATCH 限额/并发/allow-list/custom error codes/temp-unschedulable 规则)、启停、**清 cooldown**(`clear-rate-limit`,destructive 确认)、**干跑校验**(`/test`,按钮上写「会触达上游、可能消耗配额」并二次确认,结果按 `error_class` 映射本地化文案)、查看 upstream-models、凭据管理子面板(列元数据 + 创建/轮换/改状态/删除,**新建/轮换的明文一次性输入、永不回显**)、channel-health 覆盖(pause/resume/force-active,需 `vendor`+`account_credential_id`+`credential_version`+`reason`,destructive 确认)。
> - 创建抽屉:先选 `provider_id`/`channel_id`/`account_type`,再按类型动态渲染 `credentials` 字段(write-only,提交后清空、不回显);`confirm` 路径走风险对话框。
> - **按 tag 批量**:工具栏「按标签批量」对话框,填 `tag` + 可选 `enabled`/`priority`/`static_weight`,结果展示 `count` 与 `affected_ids`,先确认再执行。
> - tenant 选择器同前两页;注意 channel-health 与凭据端点的 `tenant_id` 为 required。
>
> 类型来自 `lib/api/schema.d.ts`(由 openapi 生成),禁止手写 shape;交付页面组件 + `lib/api/admin-accounts.ts` typed 调用 + 正确的 `app/(admin)/admin/accounts/` 路由段。

---

## S6 — Admin 运营核心2:Pools / Credentials / Model-Sync / Cache

> 切片范围:`app/(admin)` 下 4 个运营页面。openapi tags:`admin-pools`、`admin-credential-acquisition`、`admin-cache`、`admin-model-sync`。
> 全切片公共约定:auth guard 一律 **admin**(平台 admin 角色,见框架 §3),整个 admin 表面对非 admin 不渲染。错误码映射统一走框架 §5.1:错误体形如 `{ "error": { "code, message, request_id?, retry_after_seconds?, details? } }`,`code` 为机器可读枚举(如 `QUOTA_EXHAUSTED`、`NO_ELIGIBLE_ACCOUNT`、`OVERLOADED`、`RATE_LIMIT_5H_EXCEEDED`、`TOKEN_PERMANENTLY_REVOKED`),永不展示裸 500/原始错误体。列表分页统一用 `cursor`(query,来自上一页 `page.next_cursor`)+ `limit`(query,1..100,默认 50);`page` 对象含 `cursor`/`next_cursor`/`has_more`。钱/破坏性/凭证写操作必须等服务端确认(框架 §5.7,无乐观 UI)。多数写操作头部带 `Idempotency-Key`(框架 §5.5)。
> 注意角色细分:pools/cache 标注 `x-huakai-required-role: tenant_operator`(平台 admin 需在 query 显式带 `tenant_id`);credential-acquisition 与 model-sync 多为 `platform_admin`。UI 在 admin guard 内据此显示/隐藏破坏性按钮并在平台 admin 视角强制 `tenant_id` 选择器。

---

### /admin/pools

> 设计并实现 HUAKAI **admin** 表面的 **`/admin/pools`** 页面(Next.js 15 App Router + Tailwind 4 + TanStack Query)。页面目标:管理「池组(Pool Group)」与其路由策略——列出/创建/查看/编辑路由分组,调节粘性等待、回退等待、强制路由限速等 owner-locked 决策项。受众:**admin**(`tenant_operator` 可省略 `tenant_id` 用 token scope;平台 admin 必须在 query 显式带 `tenant_id`)。Auth guard:**admin**。
>
> **Endpoints**(tag `admin-pools`,全部 `x-huakai-spec-source: docs/specs/pool-routing.md`):
> - `GET /admin/v1/pools`(`listPoolGroups`)— 分页列出池组。query:`tenant_id`(int64,平台 admin 必填 / tenant_operator 可省)、`cursor`、`limit`。响应 `PoolGroupList`:`items[]`(`PoolGroup`)+ `page`(`PageMeta`)。
> - `POST /admin/v1/pools`(`createPoolGroup`)— 创建池组。query 可选 `tenant_id`(若 body 也带须一致)。请求体 `PoolGroupCreate` 必填字段:`name`(minLength 1);可选:`tenant_id`、`routing_policy_version`(默认 `"1.0"`)、`top_k_default`(1..10,默认 1)、`capability_default`(枚举 `exact_capability_only` | `safe_equivalent_allowed`,默认 `exact_capability_only`)、`allow_tenant_operator_force`、`allow_last_resort`、`allow_mid_stream_failover`(均 bool 默认 false)。响应 `201` → `PoolGroup`。
> - `GET /admin/v1/pools/{id}`(`getPoolGroup`)— 池组详情。path `id`(int64);query 可选 `tenant_id`。响应 `PoolGroup`。
> - `PATCH /admin/v1/pools/{id}`(`updatePoolGroup`)— 更新池组配置(部分字段)。请求体 `PoolGroupUpdate`(全可选):`routing_policy_version`、`top_k_default`、`capability_default`、`allow_tenant_operator_force`、`allow_last_resort`、`allow_mid_stream_failover`、`sticky_wait_max_waiting`、`fallback_wait_max_waiting`、`sticky_wait_timeout_ms`、`fallback_wait_timeout_ms`、`forced_route_rate_limit_per_hour`(均 ≥0)、`enabled`(bool)、`tenant_id`(显式 scope)。响应 `PoolGroup`。注意:强制路由策略改动在 Pool 的 `allow_tenant_operator_force=false` 时需 platform_admin。
>
> `PoolGroup` 关键展示字段:`id`、`tenant_id`、`name`、`routing_policy_version`、`top_k_default`、`capability_default`、`allow_tenant_operator_force`、`allow_last_resort`、`allow_mid_stream_failover`、`sticky_wait_max_waiting`、`fallback_wait_max_waiting`、`sticky_wait_timeout_ms`、`fallback_wait_timeout_ms`、`forced_route_rate_limit_per_hour`、`enabled`、`created_at`、`updated_at`。
>
> **States**:loading(表格 skeleton);empty(无池组 → 引导创建首个池组);error 按框架 §5.1 映射 `400 BadRequest`(校验失败,内联表单错误)、`401 Unauthorized`(踢回登录)、`403 Forbidden`(权限不足:平台 admin 漏带 `tenant_id`、或非 admin / 改强制路由权限不够 → 明确提示)、`404 NotFound`(池组不存在);分页用 `cursor`+`limit`,「加载更多」由 `page.has_more` 控制。
>
> **Money/Streaming**:本页不涉及钱与流式。
>
> **UX goals**:服务端分页表格,列名严格用真实字段(`name`/`top_k_default`/`capability_default`/`enabled`/`updated_at`);`enabled` 用状态徽章;`capability_default` 用枚举标签(精确能力 vs 安全等价)。编辑用抽屉/对话框,等待字段(`*_max_waiting`/`*_timeout_ms`)分组为「粘性等待」「回退等待」两块,数值带单位(ms);`forced_route_rate_limit_per_hour` 标注「强制路由限速/小时,0=不限」。`allow_tenant_operator_force` / `allow_last_resort` / `allow_mid_stream_failover` 用带说明 tooltip 的开关;切换破坏性/影响路由的开关需确认对话框。平台 admin 视角顶部强制 `tenant_id` 选择器,未选时禁用列表查询并提示。创建/编辑均等服务端 200/201 确认后再刷新缓存(无乐观)。
>
> 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/pools.ts typed 调用 + 正确 app/(admin)/pools 路由段。

---

### /admin/credentials

> 设计并实现 HUAKAI **admin** 表面的 **`/admin/credentials`** 页面(Next.js 15 App Router + Tailwind 4 + TanStack Query)。页面目标:凭证保险库的「获取/导入/续期监控」工作台——跨租户查看上游凭证续期状态,并通过获取流(OAuth / CLI 导入 / 粘贴 / CSV / JSON 导入)把上游账号凭证安全收编进加密的 `account_credentials`。受众:**admin**(续期状态读取 `platform_admin_or_tenant_operator`;获取/导入/最终化均 `platform_admin`)。Auth guard:**admin**。
>
> **Endpoints**:
> - 续期监控(tag `admin-accounts`,本页只读其状态):`GET /admin/v1/credentials/renew-status`(`listCredentialRenewStatus`)— 列出跨租户凭证续期元数据(**绝不返回加密载荷/指纹**)。query:`limit`(1..500,默认 100)、`cursor`(opaque,`ORDER BY updated_at DESC, id DESC`)、`tenant_id`(可选;tenant_operator 不能查别的租户)。响应 `AccountCredentialRenewStatusListResponse`:`items[]`(`AccountCredentialRenewStatus`)+ `next_cursor`(nullable)。`AccountCredentialRenewStatus` 字段:`id`、`tenant_id`、`tenant_name`、`account_id`、`account_name`、`vendor`、`auth_mode`、`state`、`credential_version`、`access_expires_at`、`refresh_before_at`、`last_refresh_at`、`last_refresh_outcome`、`failure_class`、`failure_count`。
> - 获取流(tag `admin-credential-acquisition`,全 `platform_admin`,`x-huakai-spec-source: docs/specs/credential-acquisition.md`):
>   - `POST /admin/v1/provider-accounts/{id}/credential-acquisitions`(`startCredentialAcquisition`)— 为某 provider account 启动获取流。头 `Idempotency-Key`(可选)。请求体 `CredentialAcquisitionStart` 必填:`tenant_id`、`vendor`(枚举 `anthropic`|`openai`|`gemini`)、`auth_mode`(枚举:`api_key`/`claude_ai_oauth`/`claude_code`/`bedrock`/`vertex_anthropic`/`chatgpt_oauth`/`codex_cli_oauth`/`azure`/`refresh_token`/`aistudio_api_key`/`vertex_sa`/`code_assist`/`google_one`/`antigravity`);可选:`provider_account_id`、`flow_kind`(枚举 `oauth`/`cli_import`/`paste`/`csv_import`/`json_import`/`cloud_bootstrap`/`token_exchange`/`setup_token`/`manual_first`)、`redirect_uri`、`requested_scopes[]`、`long_lived_requested`(默认 false,Anthropic setup-token 路径需显式启用)、`oauth_client`{`client_id`/`auth_url`/`token_url`/`redirect_uri`/`scopes`/`source`}、`reason`。响应 `201` `CredentialAcquisitionStartResponse`:`flow`(`CredentialAcquisitionFlow`)+ `authorize_url`/`state`/`code_challenge`。
>   - `GET /admin/v1/provider-accounts/{id}/credential-acquisitions/{flow_id}`(`getCredentialAcquisition`)— 查获取流状态(脱敏)。响应 `CredentialAcquisitionStatusResponse`{`flow`}。
>   - `POST .../{flow_id}/callback`(`callbackCredentialAcquisition`)— 消费 OAuth 回调或回调等价材料。请求体 `CredentialAcquisitionCallback` 必填 `state`、`code`(writeOnly);可选 `credentials`(writeOnly,非网络测试/operator 路径)。响应 `CredentialAcquisitionStatusResponse`。
>   - `POST .../{flow_id}/cancel`(`cancelCredentialAcquisition`)— 取消流。响应 `CredentialAcquisitionStatusResponse`。
>   - `POST .../{flow_id}/finalize`(`finalizeCredentialAcquisition`)— 把材料最终化为加密 `account_credentials`。头 `Idempotency-Key`。请求体 `CredentialAcquisitionFinalize` 必填 `credentials`(writeOnly,直交 finalizer,不进 session 元数据);可选 `reason`。响应 `CredentialAcquisitionFinalizeResponse`:`flow` + `credential`(object) + `already_finalized`(bool)。
>   - 批量/便捷导入(请求体均 `CredentialAcquisitionHelperImport`,必填 `tenant_id`、`provider_account_id`;可选 `vendor`、`auth_mode`、`flow_kind`、`content`(writeOnly:粘贴/上传的 JSON/JSON-lines/CSV/token 文本,**服务端绝不读本机路径**)、`credentials`(writeOnly)、`finalize`(默认 false,true 则立即最终化)、`redacted_context`、`reason`;`201` 返回 `CredentialAcquisitionHelperResponse`:`flows[]` + `finalized[]`):
>     `POST /admin/v1/credentials/paste`(`pasteCredentialAcquisition`)、`POST /admin/v1/credentials/cli-import`(`importCLICredentialAcquisition`)、`POST /admin/v1/credentials/csv-import`(`importCSVCredentialAcquisition`)、`POST /admin/v1/credentials/json-import`(`importJSONCredentialAcquisition`)。
>   - 浏览器 OAuth:`POST /admin/v1/credentials/oauth-init`(`initOAuthCredentialAcquisition`,请求体 `CredentialAcquisitionStart` → `201` `CredentialAcquisitionStartResponse`);`GET /admin/v1/credentials/oauth-callback`(`oauthCallbackCredentialAcquisition`,query 必填 `flow_id`(uuid)、`state`、`code`)。
> - 模式目录(跨 tag 依赖,tag `admin-account-modes`):`GET /admin/v1/account-modes`(`listAccountModes`)— 供获取向导填充 vendor×auth_mode×flow_kind×client_identity_source 选项(`AccountModeCatalogResponse.modes[]`)。
>
> `CredentialAcquisitionFlow` 状态枚举:`started`/`waiting_for_user`/`callback_received`/`validated`/`finalized`/`cancelled`/`expired`/`failed`;含 `client_identity_source`(`none`/`public_cli_client`/`operator_config`/`per_account_override`/`disabled_missing_config`)、`error_class`、`error_message_redacted`、`expires_at`、`result_account_credential_id`、`created_at`/`updated_at`。
>
> ⚠ 无后端契约:本切片**没有**通用 `GET /admin/v1/credentials` 凭证列表/CRUD。凭证保险库的「读」面 = `renew-status`(元数据 only)+ 获取流;凭证的**直接写/轮换/状态变更**(`AccountCredentialWriteRequest`/`AccountCredentialRotateRequest`/`AccountCredentialStateRequest`/`AccountCredentialPatchStateRequest`)挂在 `admin-accounts`(`/admin/v1/provider-accounts/{id}/...`)tag 下,属 `/admin/accounts` 页面职责——本页只做获取/导入/续期监控,凭证明文/指纹永不可读取。
>
> **States**:loading(续期表 skeleton + 流状态轮询时的 inline spinner);empty(无续期记录 / 无进行中获取流 → 引导启动获取向导);error 按 §5.1 映射 `400 BadRequest`(导入材料格式错 / 缺必填,内联)、`401`、`403 Forbidden`(tenant_operator 试图查/操作别的租户、或非 platform_admin 触发获取动作 → 明确文案)、`404 NotFound`(flow_id/account 不存在)、`409 Conflict`(callback/cancel/finalize 状态冲突 → 提示流已最终化/取消/过期,引导刷新状态)。续期列表分页用 `limit`+`cursor`(`next_cursor`);获取流状态用 TanStack Query 轮询直到进入 `finalized`/`cancelled`/`expired`/`failed` 终态。
>
> **Money/Streaming**:本页不涉及钱与流式。
>
> **UX goals**:**绝不展示任何明文凭证/token/指纹**——所有 `content`/`code`/`credentials` 字段都是 writeOnly,只上行不回显;输入框用 password 风格 + 「材料仅本次提交、服务端加密存储、永不回读」醒目提示。获取向导:用 `listAccountModes` 联动 vendor → auth_mode → flow_kind,按 `flow_kind` 切换不同输入(OAuth 走 `authorize_url` 跳转 + 回调消费;粘贴/CSV/JSON/CLI 走 `content` 文本框)。流状态机用时间线/步骤条可视化(`started → waiting_for_user → callback_received → validated → finalized`),失败态显示 `error_class` + `error_message_redacted`(已脱敏)。`long_lived_requested`(Anthropic setup-token)默认关、加风险确认。续期监控表:`failure_count>0` 高亮、`access_expires_at`/`refresh_before_at` 临期标红、`last_refresh_outcome`/`failure_class` 用徽章。start/finalize 带 `Idempotency-Key`(框架 §5.5)防重复收编;cancel/finalize 等服务端确认(无乐观)。`tenant_id` 选择器对 tenant_operator 锁定为自身 scope。
>
> 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/credentials.ts typed 调用 + 正确 app/(admin)/credentials 路由段。

---

### /admin/model-sync

> 设计并实现 HUAKAI **admin** 表面的 **`/admin/model-sync`** 页面(Next.js 15 App Router + Tailwind 4 + TanStack Query)。页面目标:手动触发「上游厂商模型目录同步」——把各厂商 model-list API 的最新模型刷进 HUAKAI 全局模型注册表(不改租户别名/池绑定/定价类),并展示本次同步的逐厂商增量结果。受众:**admin**(`platform_admin` only)。Auth guard:**admin**。
>
> **Endpoints**(tag `admin-model-sync`,`x-huakai-spec-source: docs/process/plans/2026-06-02-model-auto-sync-codex.md`):
> - `POST /admin/v1/model-sync`(`postAdminModelSync`)— 触发对所有已配置厂商的目录同步。请求体可选:`{ reason?: string (maxLength 200) }`。响应 `200`(内联 schema,`object: "admin_model_sync_result"`):顶层 `completed_at`(date-time)、`total_added`、`total_updated`、`total_disabled`(均 ≥0),以及 `results[]`,每项:`vendor`(枚举 `anthropic`|`openai`|`gemini`)、`added`、`updated`、`reactivated`、`disabled`、`unchanged`、`snapshot_bumps`(均 ≥0)。语义:上游消失的模型被标记 unavailable(disabled)而非删除。
>
> ⚠ 无后端契约:本 tag 下**只有这一个 POST 触发端点**,无独立的「同步配置查询 / 同步历史列表 / 厂商启用开关」GET。若页面想展示「已配置厂商列表 / 上次同步时间 / 历史记录」需后端补端点或换方案(可在前端缓存上次触发返回的结果作为「最近一次同步」展示,但跨会话历史无契约)。
>
> **States**:loading(触发后按钮进入运行态 spinner,同步可能耗时,用 TanStack Query mutation 的 isPending);empty(尚未在本会话触发过 → 展示说明 + 单一「立即同步」CTA);error 按 §5.1 映射 `400 BadRequest`(`reason` 超长等,内联)、`401`、`403 Forbidden`(非 platform_admin → 明确提示该操作仅平台 admin)、`503 ServiceBusy`(上游/服务繁忙 → 提示稍后重试,若错误体带 `retry_after_seconds` 则显示倒计时)。无分页。
>
> **Money/Streaming**:本页不涉及钱与流式。
>
> **UX goals**:单页操作面板,顶部一句话说明「同步只刷全局模型注册表,不动租户别名/池绑定/定价/配额/凭证」(消除误触担忧)。「立即同步」为破坏性级别动作 → 触发前确认对话框,可选填 `reason`(≤200 字)。同步完成后用 summary 卡片展示 `total_added`/`total_updated`/`total_disabled` 大数字 + `completed_at` 时间;下方 `results[]` 用按厂商分组的表格,列严格用真实字段名(`vendor`/`added`/`updated`/`reactivated`/`disabled`/`unchanged`/`snapshot_bumps`),`disabled>0` 高亮并 tooltip 解释「上游消失→标记不可用,非删除」。运行期禁用重复触发;结果展示「仅本次会话」,不伪造历史。
>
> 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/model-sync.ts typed 调用 + 正确 app/(admin)/model-sync 路由段。

---

### /admin/cache

> 设计并实现 HUAKAI **admin** 表面的 **`/admin/cache`** 页面(Next.js 15 App Router + Tailwind 4 + TanStack Query)。页面目标:L2 响应缓存的观测与运维——查看缓存总体统计与逐条目脱敏元数据、命中/未命中指标,并按 key 逐条驱逐缓存。受众:**admin**(`tenant_operator`;平台 admin 视角按 scope 处理)。Auth guard:**admin**。
>
> **Endpoints**(tag `admin-cache`,`x-huakai-spec-source: docs/03_FEATURE_PARITY_MATRIX.md, docs/specs/privacy-no-user-data-logs.md`):
> - `GET /admin/v1/cache/l2/stats`(`getAdminL2CacheStats`)— L2 缓存元数据 + 逐条目脱敏统计。响应 `AdminL2CacheStatsResponse`:`enabled`(bool)、`size_bytes`、`max_size_bytes`、`ttl_seconds`(int64 ≥0)、`entries[]`(`AdminL2CacheEntryStats`)、`metrics`(map<string, `AdminL2CacheMetric`>)。`AdminL2CacheEntryStats` 字段:`key`、`tenant_id`、`vendor`、`model`、`status`(int)、`size_bytes`、`stored_at`、`expires_at`。`AdminL2CacheMetric` 字段:`hit_total`、`miss_total`、`size_bytes`(均 int64 ≥0)。
> - `DELETE /admin/v1/cache/l2/{key}`(`deleteAdminL2CacheEntry`)— 驱逐单条 L2 缓存条目。path `key`(string,minLength 1)。响应 `AdminL2CacheDeleteResponse`:`key`、`deleted`(bool)。
>
> **States**:loading(stats 卡片 + 表格 skeleton);empty(`entries` 为空 → 「当前无缓存条目」,若 `enabled=false` 则醒目提示「L2 缓存已禁用」并解释驱逐不可用);error 按 §5.1 映射 `401 Unauthorized`、`403 Forbidden`(非 admin / scope 不足)、`404 NotFound`(驱逐时 key 不存在 → toast「条目已不存在/已过期」)、`503 ServiceBusy`(缓存后端繁忙 → 稍后重试)。本端点**无 cursor/limit 分页参数**——`entries[]` 一次性返回,前端做客户端搜索/排序/虚拟滚动即可(不要伪造服务端分页参数)。
>
> **Money/Streaming**:本页不涉及钱与流式。
>
> **UX goals**:顶部统计卡片:`enabled` 状态徽章、`size_bytes`/`max_size_bytes`(用占用率进度条,字节人性化为 KB/MB/GB)、`ttl_seconds`(人性化为时长)。`metrics` 渲染为按维度(map key)的命中率卡片:`hit_total`/`miss_total` 与计算出的命中率(展示用,非钱,允许前端百分比计算)。`entries[]` 表格列严格用真实字段(`key`/`tenant_id`/`vendor`/`model`/`status`/`size_bytes`/`stored_at`/`expires_at`),临期(`expires_at` 将到)标灰、`key` 可一键复制(长 key 截断 + tooltip)。每行「驱逐」按钮 → **破坏性操作确认对话框**(显示完整 key 与影响说明),等 `DELETE` 返回 `deleted=true` 后才从列表移除并刷新 stats(无乐观)。隐私:页面只展示脱敏元数据,绝不展示缓存的请求/响应正文。
>
> 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/cache.ts typed 调用 + 正确 app/(admin)/cache 路由段。

---

## S7 — Admin 钱/审计 (billing · pricing · payments · audit · dlq · alerting)

> 切片范围:`/admin/billing`、`/admin/pricing`、`/admin/payments`、`/admin/audit`、`/admin/dlq`、`/admin/alerting`。
> openapi tags:`admin-billing`、`admin-pricing`、`admin-vouchers`、`admin-usage`、`admin-audit`、`admin-dlq`、`admin-alerting`(payments 实际落在 `admin-payments` 与 `cache-price-overrides` 上)。
> 全部为 **admin 守卫**(`app/(admin)/`),非管理员永不渲染本切片导航。所有钱字段从服务端 decimal/cents 渲染,禁止前端浮点重算(§5.2)。
> **跨切面通用约定(本切片所有页适用)**:
> - 错误信封统一 `{ error: { code, message, request_id?, retry_after_seconds?, details? } }`,按 §5.1 映射本地化 toast/inline,绝不显示裸 500 body。本切片常见错误码 response:`400 BadRequest`、`401 Unauthorized`、`403 Forbidden`、`404 NotFound`、`409 Conflict`、`422 UnprocessableEntity`、`503 ServiceBusy`。
> - **两套分页并存,按端点真实参数实现**:① 游标式(`admin-usage`/`admin-billing`/`admin-audit`)用 query `cursor` + `limit`,响应 `page.next_cursor` / `page.has_more`(`PageMeta`);② offset 式(`admin-pricing`/`admin-payments`/`admin-vouchers`/`admin-alerting`/`disputes`)用 query `limit` + `offset`。不要混用。
> - **tenant_id 作用域**:多数 admin 端点要求 `tenant_id` query(platform_admin 必填;tenant_operator 可省走 token 作用域)。UI 顶部需有租户选择器,选中后注入到每个调用。
> - 钱/销毁类 mutation 必须等服务端确认(§5.7,无乐观 UI),并弹确认对话框。

---

### /admin/billing

> **目标 & 受众**:平台运营查看「计费账本 claims、用量记录、租户计费策略、手动余额调整」的对账与处置台。受众 **admin**(tenant_operator 读为主 + platform_admin 写)。Auth guard:**admin**。所有金额来自服务端 `numeric(20,8)` 字符串,按 §5.2 格式化(2–6 位有效数字 + 币种),禁止前端浮点。
>
> **Endpoints**
> - `GET /admin/v1/billing/claims`(`listBillingClaims`)— 计费账本预留/结算 claim 列表。游标分页 query:`cursor`、`limit`;过滤 query:`status`(enum `reserving|committed|aborted`)、`from`(date-time)。响应 `BillingLedgerClaimList`:`items[]`(`BillingLedgerClaim`:`id`、`tenant_id`、`idempotency_key`、`api_key_id`、`user_id`、`endpoint_family`、`requested_model`、`provider_account_id`、`attempt_seq`、`predicted_cost`(decimal string)、`actual_cost`(decimal string|null)、`currency_code`、`status`、`aborted_reason`、`reserved_at`、`settled_at`)+ `page`(`next_cursor`、`has_more`)。
> - `GET /admin/v1/usage`(`listUsageRecords`)— 已结算用量记录明细(对账核心)。游标分页 `cursor`/`limit`;过滤 query:`from`、`to`(date-time)、`api_key_id`、`provider_account_id`、`model`、`pending_reconciliation_only`(bool)、`outcome`(enum `success|error|all`,默认 `all`)。响应 `UsageRecordList`:`items[]`(`UsageRecord`:`id`、`tenant_id`、`claim_id`、`api_key_id`、`provider_account_id`、`tokens_input`、`tokens_output`、`cache_creation_tokens`、`cache_read_tokens`、`actual_cost`(decimal string)、`end_class`(15 值枚举,如 `stream_end_graceful`/`non_streaming`/`upstream_error_5xx`…)、`usage_source`(`reported|normalized|inferred|partial|ambiguous`)、`pending_reconciliation`、`requested_model`、`upstream_model`、`requested_at`、`settled_at`、`stream`)+ `page`。
> - `GET /admin/v1/billing/settings`(`getAdminBillingSettings`)— 读租户计费策略。query `tenant_id`(必填)。响应 `AdminBillingSettingsResponse`:`tenant_id`、`key`(枚举固定 `stream_input_only_interrupted_policy`)、`value`(`no_bill|no_bill_record`)、`source`(`tenant|default`)、`allowed_values[]`、`roadmap_values[]`(含 `bill_input`)、`updated_at`、`updated_by`。
> - `PUT /admin/v1/billing/settings`(`updateAdminBillingSettings`)— 更新计费策略并写 admin 审计。body `AdminBillingSettingsUpdate`:`tenant_id`、`stream_input_only_interrupted_policy`(`no_bill|no_bill_record|bill_input`,其中 `bill_input` 是 roadmap,提交会返回 `billing_policy_value_roadmap`)、`reason`(非空,必填)。错误:`400/401/403/404/409/503`。
> - `POST /admin/v1/balances/adjustments`(`adminAdjustBalance`,platform_admin only)— 手动余额调整。body `AdminBalanceAdjustmentRequest`:`tenant_id`、`user_id`、`amount`(带符号 decimal string;**负值当前返回 `admin_debit_not_yet_supported`**,UI 应预先禁用扣款或明确提示不支持)、`currency_code`(默认 `USD`)、`reason`(非空)、`idempotency_key`(1–128,稳定键,重试同体同键幂等)。响应 `AdminBalanceAdjustmentResponse`:`tenant_id`、`user_id`、`net_balance`(decimal string)、`currency_code`、`recharge_order_id`。
>
> **States**
> - loading:claims/usage 表骨架屏;settings 卡片骨架。
> - empty:无 claim / 无 usage 记录的空态文案 + 调整过滤建议。
> - error(§5.1 映射):`400`(过滤参数非法,如 date 顺序)、`401`(登录失效跳登录)、`403`(角色不足:tenant_operator 试图 PUT settings / POST adjustment → 隐藏写按钮)、`404`(tenant 不存在)、`409`(settings 并发冲突 / 调整幂等冲突)、`503`(`ServiceBusy` → 退避重试提示)。adjustment 负值业务错误码 `admin_debit_not_yet_supported` 与 settings 的 `billing_policy_value_roadmap` 要本地化为明确说明而非通用报错。
> - 分页:claims/usage 用游标(`page.next_cursor` 驱动「加载更多」/无限滚动;`page.has_more===false` 收尾),不可用页码跳转。
>
> **Money**(§5.2):claims 的 `predicted_cost`/`actual_cost`、usage 的 `actual_cost`、调整后的 `net_balance` 全为 `numeric(20,8)` 字符串,直接服务端取值格式化;token 列(`tokens_input/output`、`cache_*`)为整数。不要把 cost ÷ 1e6 之类换算施加到这些字段(它们已是 USD decimal)。
>
> **UX goals**
> - claims 与 usage 两个独立服务端分页表,列名严格用真实字段(`actual_cost`、`end_class`、`usage_source`、`pending_reconciliation`…),`end_class` 用状态徽章区分成功/各类错误终止。
> - `pending_reconciliation_only` 与 `outcome` 做成快捷过滤芯片;`from/to` 用日期范围选择器(发 RFC3339)。
> - 计费策略卡片:`source=default` 时明示「回退默认」,`allowed_values` 渲染为可选项,`roadmap_values`(`bill_input`)做成 disabled+「即将支持」标记;保存需填 `reason` 才可提交。
> - 余额调整:platform_admin 专属面板,默认仅允许正向充值;前端生成 `idempotency_key`(UUID)随请求发出防重复;提交前确认对话框展示 tenant/user/amount/reason;成功后展示 `net_balance` 与 `recharge_order_id`。
> - 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/billing.ts typed 调用 + 正确 app/(admin)/billing 路由段。

---

### /admin/pricing

> **目标 & 受众**:平台运营管理「租户 × 池组定价倍率(ratio)」及「缓存价覆盖倍率」。受众 **admin**(读 tenant_operator;写 ratio 与 cache override 均为 platform_admin)。Auth guard:**admin**。倍率为 decimal string,展示与编辑都按字符串处理(§5.2)。
>
> **Endpoints**
> - `GET /admin/v1/pricing/ratios`(`listAdminPricingRatios`)— 列出租户池组定价倍率。query:`tenant_id`(platform_admin 必填,tenant_operator 可省)、`limit`(1–500,默认 50)、`offset`(默认 0)。响应 `AdminPricingRatioListResponse`:`object="pricing_ratio_list"`、`items[]`(`AdminPricingRatio`:`object`、`id`、`tenant_id`、`pool_group_id`、`ratio`(decimal string)、`public_ratio`(bool)、`created_by`、`updated_by`、`created_at`、`updated_at`)、`limit`、`offset`。
> - `GET /admin/v1/pricing/ratios/{pool_group_id}`(`getAdminPricingRatio`)— 读单个池组倍率。path `pool_group_id`;query `tenant_id`(平台管理员必填)。响应 `AdminPricingRatio`。
> - `PUT /admin/v1/pricing/ratios/{pool_group_id}`(`upsertAdminPricingRatio`,platform_admin)— upsert 倍率。body `AdminPricingRatioRequest`:`ratio`(必填,正小数,pattern `^[0-9]+(\.[0-9]+)?$`)、`public_ratio`(bool,默认 false)。响应 `AdminPricingRatio`。
> - `DELETE /admin/v1/pricing/ratios/{pool_group_id}`(`deleteAdminPricingRatio`,platform_admin)— 删除倍率。响应 `AdminPricingRatioDeleteResponse`:`object="pricing_ratio_deleted"`、`tenant_id`、`pool_group_id`。
> - `GET /v1/admin/cache-price-overrides`(`adminListCachePriceOverrides`,platform_admin)— 列缓存价覆盖倍率(global / per-model / per-tenant)。响应 `{ overrides: object[] }`(未列出的 scope 按官方价 = 倍率 1.0)。
> - `PUT /v1/admin/cache-price-overrides/{scope}`(`adminSetCachePriceOverride`,platform_admin)— 设某 scope 覆盖倍率。path `scope`(enum `global|model|tenant`);query:`model`(scope=model 必填)、`tenant_id`(scope=tenant 必填);body `{ multiplier: string }`(相对官方价正小数,如 `"1.5"`)。计费优先级 **tenant > model > global > official**,变更记入签名审计哈希链。响应 `{ override: object }`。
> - `DELETE /v1/admin/cache-price-overrides/{scope}`(`adminDeleteCachePriceOverride`,platform_admin)— 清除覆盖回退官方价。query `model` / `tenant_id`(按 scope)。响应 `{ deleted: boolean }`。
>
> **States**
> - loading:ratios 表骨架 + overrides 卡片骨架。
> - empty:无 ratio(提示「该租户所有池组按官方价计费」);overrides 为空时显式说明「所有 scope 按官方价(1.0)」。
> - error(§5.1):`400`(`ratio`/`multiplier` 格式非法、缺 `model`/`tenant_id`)、`401`、`403`(tenant_operator 试图写 → 隐藏写操作)、`404`(`getAdminPricingRatio` 找不到 / delete override 不存在)、`503`。
> - 分页:ratios 用 offset 分页(`limit`+`offset`,响应回显 `limit`/`offset`),做经典页码或「上/下一页」。
>
> **Money/倍率**(§5.2):`ratio` 与 cache override `multiplier` 都是 decimal string,按字符串读取/回写,绝不 parseFloat 后再做四舍五入展示给后端;输入框做字符串校验(匹配 pattern)。
>
> **UX goals**
> - ratios 服务端分页表,列:`pool_group_id`、`ratio`、`public_ratio`(徽章)、`updated_by`/`updated_at`;行内「编辑」打开 upsert 表单(只两个字段 `ratio`/`public_ratio`),「删除」走确认对话框(钱相关 destructive)。
> - cache-price-overrides 分三段(global / model / tenant)可视化,清楚标注计费优先级链 `tenant > model > global > official`;每条覆盖显示当前 multiplier 与「恢复官方价」按钮;设置/删除均确认对话框,并提示「变更记入签名审计哈希链不可篡改」。
> - 写操作仅 platform_admin 可见可用;`public_ratio=true` 的项标注「对外公开倍率」。
> - 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/pricing.ts typed 调用 + 正确 app/(admin)/pricing 路由段。

---

### /admin/payments

> **目标 & 受众**:平台运营的支付订单运维台 —— 订单列表/详情/审计、手动确认/重试/取消/退款、退款申请审批、provider 配置、券(voucher)签发,以及支付看板。受众 **admin**(几乎全部 platform_admin)。Auth guard:**admin**。**PSP 真实结账冻结(§6)**:本页只对接既有订单/退款/webhook 端点,真实支付商交接处渲染「mock/跳转占位」。
>
> **Endpoints — 订单**
> - `GET /v1/admin/payments`(`adminListPaymentOrders`)— 订单列表。query:`tenant_id`(必填)、`user_id`、`status`(enum `pending|paid|recharging|completed|refunded|expired|cancelled|failed`)、`created_from`、`created_to`(date-time)、`limit`(1–200,默认 50)、`offset`。响应 `{ orders: object[] }`。
> - `POST /v1/admin/payments`(`adminCreatePaymentOrder`)— 创建 pending 订单(按 `out_trade_no` 幂等)。body:`tenant_id`、`user_id`、`amount_cents`(int64,1..1e11)、`currency_code`(`USD`)、`out_trade_no`(稳定幂等键,必填)、`provider_kind`(`manual|taobao|test`)。201 新建 / 200 幂等回放,响应 `{ order, idempotent }`。
> - `GET /v1/admin/payments/{id}`(`adminGetPaymentOrder`)— 订单 + 审计轨。query `tenant_id`(必填)。响应 `{ order, audit_events[] }`。
> - `GET /v1/admin/payments/{id}/audit`(`adminListPaymentOrderAuditEvents`)— 订单审计事件链。query `tenant_id`。响应 `{ audit_events[] }`(每条:`event_type`、`actor_kind`、`actor_id`、`reason_class`、`occurred_at`)。
> - `POST /v1/admin/payments/{id}/confirm`(`adminConfirmPaymentOrder`)— 手动确认并入账。body:`tenant_id`、`confirm_reason`。响应 `{ order, credit, balance_cents, idempotent }`。
> - `POST /v1/admin/payments/{id}/retry`(`adminRetryPaymentOrderFulfillment`)— 对 paid/recharging 订单重试履约。body `{ tenant_id }`。响应 `{ order, credit, subscription, balance_cents, idempotent }`。
> - `POST /v1/admin/payments/{id}/cancel`(`adminCancelPaymentOrder`)— 取消 pending 订单。body `{ tenant_id, reason }`。响应 `{ order }`。
> - `POST /v1/admin/payments/{id}/refund`(`adminRefundPaymentOrder`)— 退款已入账订单。body:`tenant_id`、`amount_cents`(≥1)、`idempotency_key`(非空)、`reason`。响应 `{ order, refund, balance_cents, idempotent }`(错误含 `422 UnprocessableEntity`)。
>
> **Endpoints — 退款申请审批 / 看板 / provider 配置**
> - `GET /v1/admin/payments/refund-requests`(`adminListPaymentRefundRequests`)— 待审退款申请队列。query `tenant_id`(必填)。响应 `{ refund_requests: object[] }`。
> - `POST /v1/admin/payments/refund-requests/{id}/approve`(`adminApprovePaymentRefundRequest`)— 批准并执行退款(用请求派生稳定幂等键)。body `{ tenant_id }`。响应 `{ refund_request }`(错误含 `409`、`422`)。
> - `POST /v1/admin/payments/refund-requests/{id}/reject`(`adminRejectPaymentRefundRequest`)— 拒绝(不动钱)。body `{ tenant_id, reason }`。响应 `{ refund_request }`。
> - `GET /v1/admin/payments/dashboard`(`adminGetPaymentDashboard`)— 支付看板。query:`tenant_id`(必填)、`created_from`、`created_to`。响应:`total_amount_cents`、`total_count`、`today_count`、`average_amount_cents`、`daily_series[]`(`date`、`order_count`、`amount_cents`)。
> - `GET /v1/admin/payments/providers/{provider}/config`(`adminGetPaymentProviderConfig`)/ `PUT …`(`adminPutPaymentProviderConfig`)— provider(`manual|taobao`)非机密运行配置:GET 返回 `{ provider:{ provider_kind, enabled, checkout_url, source, updated_by, updated_at } }`;PUT body `{ enabled, checkout_url? }`(`checkout_url`:启用 taobao 必填,manual 禁止)。
> - `GET /v1/admin/payments/export.csv`(`adminExportPaymentOrdersCSV`,tenant_scoped_admin)— 订单 CSV 导出。query:`from`、`to`(必填)、`status`(同上枚举)。返回 `text/csv`,header `order_id,user_id,provider,status,amount,currency,created_at,out_trade_no,order_kind`,`Content-Disposition` 附件名 + 命中 10w 行上限时 `X-Truncated: true`。
>
> **Endpoints — 券(admin-vouchers)**
> - `GET /v1/admin/vouchers`(`listVouchers`,platform_admin)— 租户券列表。query:`tenant_id`(必填)、`limit`(1–200,默认 50)。响应 `VoucherListResponse`:`vouchers[]`(`Voucher`:`id`、`tenant_id`、`batch_id`、`code_fingerprint`(非机密短哈希,不可兑)、`amount_cents`、`currency_code`、`valid_from`、`valid_until`、`max_redemptions`、`redeemed_count`、`single_use_per_user`、`eligible_user_id`、`status`(`active|expired|exhausted|revoked`)、`created_by_admin_id`、`revoked_*`、时间戳)。
> - `POST /v1/admin/vouchers`(`createVoucher`)— 创建单券。body `VoucherCreateRequest`:`tenant_id`、`code?`(可选原始码)、`amount_cents`、`currency_code`(默认 USD)、`valid_from`、`valid_until`、`max_redemptions`(默认 1)、`single_use_per_user`(默认 true)、`eligible_user_id?`。201 响应 `VoucherCreateResponse`:`{ voucher, code }`(**raw `code` 仅此一次返回**)。
> - `POST /v1/admin/vouchers/batch`(`createVoucherBatch`)— 原子批量。body `VoucherBatchCreateRequest`(同上 + `count` 1–1000)。201 `VoucherBatchCreateResponse`:`{ batch, vouchers[], codes[] }`(`codes[]` 内 `{ voucher_id, code, code_fingerprint }`,**raw codes 仅此一次**)。
> - `GET /v1/admin/vouchers/batches/{batch_id}`(`getVoucherBatch`)— 批次详情。path `batch_id`;query `tenant_id`(必填)。响应 `VoucherBatchDetailResponse`:`{ batch, vouchers[] }`。
> - `POST /v1/admin/vouchers/{id}/revoke`(`revokeVoucher`)— 吊销券(不退已成功兑换)。body `VoucherRevokeRequest`:`{ tenant_id, reason? }`。响应 `{ voucher }`。
>
> **States**
> - loading:订单表 / 看板卡片 / 券表 骨架。
> - empty:无订单、无待审退款、无券 三处独立空态。
> - error(§5.1):`400`(过滤/金额非法、`out_trade_no`/`idempotency_key` 缺失、taobao 缺 `checkout_url`)、`401`、`403`(角色不足)、`404`(订单/退款/券不存在)、`409`(订单状态冲突、退款幂等冲突、券 code 冲突)、`422`(退款/批准不可处理:金额超额等)、`503`。
> - 分页:订单与券用 offset(`limit`+`offset`);看板按时间窗。
>
> **Money/Idempotency**(§5.2 / §5.5):支付域金额为 **整数 cents**(`amount_cents`、`balance_cents`、`total_amount_cents`、`average_amount_cents`),展示时除以 100 仅用于显示格式化(不回写),回写一律用 cents 整数,绝不用浮点累加。所有写操作(create/confirm/retry/refund/approve)发送幂等键(`out_trade_no` 或 `idempotency_key`)防重复扣款;响应 `idempotent:true` 时提示「已是幂等回放,未重复入账」。券金额为 `amount_cents`。
>
> **UX goals**
> - 订单服务端分页表(列:`order_id/status/amount/provider/created_at/out_trade_no`),`status` 用彩色徽章;行展开/详情抽屉显示 `audit_events`(`event_type`/`actor_kind`/`reason_class`/`occurred_at` 时间线)。
> - confirm/retry/cancel/refund 全部走确认对话框(钱+destructive);退款表单要求填 `amount_cents`、`reason` 并前端生成 `idempotency_key`。
> - 退款申请队列单独 tab:每条带「批准(执行退款)」「拒绝(填 reason)」按钮,批准前二次确认并强调「将真实退款」。
> - 看板顶部展示 `total_amount_cents`/`total_count`/`today_count`/`average_amount_cents` stat 卡 + `daily_series` 折线。
> - provider 配置面板:`manual`/`taobao` 两段;启用 taobao 时强制填 `checkout_url`(就是 §6 的支付商跳转占位 URL),并明确标注「真实 PSP 结账为冻结占位」。
> - 券:创建/批量后用一次性弹窗显示明文 `code`/`codes`(可复制),关闭后只剩 `code_fingerprint`,绝不二次展示明文(对齐 §3 key-reveal 模式);吊销走确认对话框并提示「已成功兑换不退」。CSV 导出按钮触发浏览器下载,命中 `X-Truncated` 时提示「结果被截断(达 10 万行上限)」。
> - 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/payments.ts typed 调用 + 正确 app/(admin)/payments 路由段。

---

### /admin/audit

> **目标 & 受众**:平台运营查看「统一审计轨」(池路由 / 限流 / OAuth 刷新)并处置「成本争议(cost disputes)」。受众 **admin**(读 tenant_operator;争议处置带 adminBearerAuth)。Auth guard:**admin**。本页全部读 + 争议状态流转,**不退款、不改账本行**。
>
> **Endpoints**
> - `GET /admin/v1/audit-events`(`listAuditEvents`)— 跨 `pool_routing_audit_events`+`rate_limit_audit_events`+`oauth_refresh_audit_events` 的统一审计视图。游标分页 `cursor`/`limit`;过滤 query:`event_class`(enum `pool_routing|rate_limit|oauth_refresh`)、`actor_id`(string)、`from`(date-time)。响应 `AuditEventList`:`items[]`(`AuditEvent`:`id`、`tenant_id`、`event_class`、`event_type`、`provider_account_id`、`pool_group_id`、`claim_id`、`request_id`、`actor_id`、`actor_role`(`platform_admin|tenant_operator|null`)、`reason`、`payload`(object)、`occurred_at`)+ `page`(`next_cursor`、`has_more`)。
> - `GET /v1/admin/disputes`(`listAdminCostDisputes`,adminBearerAuth)— 租户内成本争议列表(只读,跨用户)。query:`tenant_id`(platform_admin 必填)、`status`(enum `open|reviewing|resolved|rejected`)、`limit`(1–500,默认 100)、`offset`。响应 `{ disputes: CostDispute[] }`(`CostDispute`:`id`、`dispute_id`、`tenant_id`、`user_id`、`request_id`、`reason`、`status`、`operator_note`、`created_at`、`resolved_at`)。
> - `POST /v1/admin/disputes/{id}/resolve`(`resolveCostDispute`,adminBearerAuth)— 解决/驳回/置审。path `id`;body `ResolveCostDisputeRequest`:`tenant_id`、`status`(enum `reviewing|resolved|rejected`)、`operator_note`(≤4000,必填)。响应 `{ dispute: CostDispute }`(明确「不退款不改账本」)。错误:`400/401/403/404/503`。
>
> **States**
> - loading:审计表 / 争议表 骨架。
> - empty:无审计事件 / 无争议 空态。
> - error(§5.1):`400`(过滤参数非法)、`401`、`403`(tenant_operator 越权处置 → 隐藏 resolve)、`404`(争议不存在)、`503`。
> - 分页:audit-events 用游标(`page.next_cursor`/`has_more`);disputes 用 offset(`limit`+`offset`)。
>
> **UX goals**
> - 审计统一表:`event_class` 三类用不同色徽章过滤;列展示 `event_type`、`actor_id`/`actor_role`、`request_id`、`occurred_at`;行点开 JSON viewer 展示 `payload`(只读、可复制)。`from` 用时间选择器,`actor_id` 文本过滤。
> - 争议工作台:`status` 标签过滤(open/reviewing/resolved/rejected);每行可跳到关联 `request_id`(联动用量记录);处置对话框选 `status`(reviewing/resolved/rejected)并强制填 `operator_note`,提交前明确提示「此操作仅改争议状态,不触发退款」。
> - 全只读字段不可编辑;`resolved_at` 为 null 时显示「未解决」。
> - 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/audit.ts typed 调用 + 正确 app/(admin)/audit 路由段。

---

### /admin/dlq

> **目标 & 受众**:平台运营巡检并重放死信队列(usage_record / billing/audit 复制 / 退款 / 账本 / 结算 / 回执 / 健康 / metrics 等 lane)。受众 **admin**(全部 platform_admin)。Auth guard:**admin**。
>
> **Endpoints**
> - `GET /admin/v1/dlq/{handler}`(`listObservabilityDLQ`,platform_admin)— 按 handler 列 DLQ 条目。path `handler`(enum `usage_record|billing_event_replica|audit_event_replica|audit_mismatch_refund|audit_ledger_entry|post_delivery_settlement|cost_receipt_append|account_health|metrics`);query:`status`(enum `pending|inflight|delivered|operator_review|dlq|quarantined`)、`limit`(`PageLimit`)。响应 `DLQEntryList`:`{ items: DLQEntry[] }`(`DLQEntry`:`id`、`tenant_id`、`claim_id`、`event_kind`、`lane`(`HIGH|MED|LOW`)、`status`、`payload`(object)、`failure_reason`、`failure_at`、`replay_attempts`、`last_replay_at`、`replayed_at`、`replay_failure_reason`、`next_retry_at`、`lease_owner`、`lease_until`、`replica_status`(`none|pending|delivered|failed`)、`replica_target`、`idempotency_key`、`source_table`、`source_id`、`operator_review_at`)。
> - `POST /admin/v1/dlq/{id}/replay`(`replayObservabilityDLQ`,platform_admin)— 重放一条通用 DLQ 条目。path `id`。响应 `DLQReplayResult`:`{ item: DLQEntry, replayed: boolean }`。错误:`404`、`409`。
> - `POST /admin/v1/usage-record-dlq/{id}/replay`(`replayUsageRecordDLQ`,platform_admin)— usage_record DLQ 重放(向后兼容别名,行为同上)。响应 `DLQReplayResult`。错误:`404`、`409`。
>
> **States**
> - loading:每个 handler 选中后的条目表骨架。
> - empty:该 handler 下无条目(对应 lane 健康)。
> - error(§5.1):`401`、`403`(非 platform_admin → 整页不可见)、`404`(重放目标已不存在/已被处理)、`409`(并发重放冲突 → 提示稍后重试/已被他人重放)、`503`。
> - 分页:`DLQEntryList` 只有 `items`(无 `page` 元),用 `limit` 控制单页量,做「加载更多/增大 limit」而非游标。
>
> **UX goals**
> - 顶部 handler 选择器(9 个枚举)+ `status` 过滤(`pending`/`inflight`/`operator_review`/`dlq`/`quarantined` 等),每个 handler 显示积压计数(可与 `/admin` cockpit 的 DLQ 深度联动)。
> - 条目表列:`event_kind`、`lane`(HIGH/MED/LOW 优先级徽章)、`status` 徽章、`replay_attempts`、`failure_reason`、`next_retry_at`、`replica_status`;行点开 JSON viewer 看 `payload` 与失败细节(`replay_failure_reason`)。
> - 「重放」按钮走确认对话框(可能影响计费/账本副本);单条 replay 用 `POST /admin/v1/dlq/{id}/replay`;usage_record lane 可用别名端点。重放成功后用响应里的 `item` 原地更新该行状态;`replayed:false` 提示未实际重放原因。
> - `quarantined`/`operator_review` 状态高亮,引导人工核查;`lease_owner`/`lease_until` 展示当前租约持有者避免并发重放。
> - 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/dlq.ts typed 调用 + 正确 app/(admin)/dlq 路由段。

---

### /admin/alerting

> **目标 & 受众**:平台运营管理「告警规则 / 告警事件 / 静默窗口」。受众 **admin**(`platform_admin_or_tenant_operator`)。Auth guard:**admin**。`tenant_id`:platform_admin 必传,tenant_operator 默认其作用域租户。
>
> **Endpoints — 规则**
> - `GET /v1/admin/alert-rules`(`adminListAlertRules`)— 列规则。query:`tenant_id?`、`limit`(1–500,默认 50)、`offset`。响应 `AlertRuleListResponse`:`object="alert_rules_list"`、`items[]`(`AlertRule`:`id`、`tenant_id`、`name`、`metric`、`metric_type`(`cpu_usage_percent`)、`filters`(map)、`comparator`(`gt|gte|lt|lte`)、`threshold`(double)、`severity`(`info|warning|critical`)、`window_seconds`、`sustained_seconds`、`cooldown_seconds`、`notify_email`、`last_triggered_at`、`enabled`、`created_at`、`updated_at`)、`limit`、`offset`。
> - `POST /v1/admin/alert-rules`(`adminCreateAlertRule`)— 建规则。body `AlertRuleCreateRequest`(必填 `name`、`metric`、`comparator`、`threshold`、`severity`、`window_seconds`;可选 `tenant_id`、`metric_type`、`filters`、`sustained_seconds`、`cooldown_seconds`、`notify_email`、`enabled`默认 true)。201 响应 `AlertRule`。错误含 `409`。
> - `GET /v1/admin/alert-rules/{id}`(`adminGetAlertRule`)/ `PUT …`(`adminUpdateAlertRule`,body `AlertRuleUpdateRequest` 全可选字段)/ `DELETE …`(`adminDeleteAlertRule`,204)。各带 `tenant_id?` query。
>
> **Endpoints — 事件**
> - `GET /v1/admin/alert-events`(`adminListAlertEvents`)— 列事件。query:`tenant_id?`、`rule_id?`、`state`(enum `firing|resolved|manual_resolved`)、`limit`(1–500,默认 50)、`offset`。响应 `AlertEventListResponse`:`object="alert_events_list"`、`items[]`(`AlertEvent`:`id`、`tenant_id`、`rule_id`、`state`、`observed_value`、`threshold_value`、`metric_value`、`dimensions`(map)、`fired_at`、`resolved_at`、`email_sent`)、`limit`、`offset`。
> - `POST /v1/admin/alert-events/{id}/manual-resolve`(`adminManualResolveAlertEvent`)— 人工标记解决。query `tenant_id?`。响应 `AlertEvent`(state→`manual_resolved`)。
>
> **Endpoints — 静默**
> - `GET /v1/admin/alert-silences`(`adminListAlertSilences`)— 列静默。query:`tenant_id?`、`limit`、`offset`。响应 `AlertSilenceListResponse`:`object="alert_silences_list"`、`items[]`(`AlertSilence`:`id`、`tenant_id`、`rule_id`(null=全部规则)、`reason`、`starts_at`、`ends_at`、`platform`、`group_id`、`region`、`created_at`)、`limit`、`offset`。
> - `POST /v1/admin/alert-silences`(`adminCreateAlertSilence`)— 建静默。body `AlertSilenceCreateRequest`(必填 `reason`、`starts_at`、`ends_at`(须晚于 starts_at);可选 `tenant_id`、`rule_id`(null=静默全部)、`platform`、`group_id`、`region`)。201 响应 `AlertSilence`。
> - `DELETE /v1/admin/alert-silences/{id}`(`adminDeleteAlertSilence`)— 删静默。query `tenant_id?`。204。
>
> **States**
> - loading:规则 / 事件 / 静默 三表骨架。
> - empty:无规则(引导创建)、无活跃事件(系统健康)、无静默窗口。
> - error(§5.1):`400`(阈值/时间窗非法、`ends_at` ≤ `starts_at`)、`401`、`403`、`404`(规则/事件/静默不存在)、`409`(规则重名/冲突)、`503`。
> - 分页:三表均 offset(`limit`+`offset`,回显于响应)。
>
> **UX goals**
> - 三段 tab:规则 / 事件 / 静默。规则表列 `name`、`metric`+`comparator`+`threshold`(渲染为 `metric > 0.9` 之类可读式)、`severity` 徽章、`window_seconds`/`sustained_seconds`/`cooldown_seconds`、`enabled` 开关、`last_triggered_at`;行编辑用 `AlertRuleUpdateRequest`(全可选,做 PATCH 式表单),删除走确认对话框。
> - 事件表:`state`(firing/resolved/manual_resolved)彩色徽章 + `severity`,显示 `observed_value` vs `threshold_value`、`fired_at`/`resolved_at`、`email_sent`;`firing` 行提供「人工解决」按钮(确认对话框)。可按 `rule_id`/`state` 过滤。
> - 静默:创建表单带时间范围校验(`ends_at` 必晚于 `starts_at`),`rule_id` 留空时明示「静默该租户全部规则」;活跃/已过期静默用视觉区分;删除确认。
> - 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/alerting.ts typed 调用 + 正确 app/(admin)/alerting 路由段。

---

## S8 — Admin 平台(users / keys / moderation / notifications / settings)

> 切片范围:`/admin`(ops cockpit)、`/admin/users`、`/admin/keys`、`/admin/moderation`、`/admin/notifications`、`/admin/settings`。
> 负责 openapi tags:`admin-users`、`admin-api-keys`、`admin-moderation`、`admin-notifications`、`admin-announcements`、`admin-email`、`admin-platform-settings`。
> 全切片 auth guard = **admin**(`app/(admin)/` 路由组,非 admin 角色不得渲染 admin 导航)。多数路由 `x-huakai-required-role: tenant_operator`(`platform_admin` 可越租户传 `tenant_id`,`tenant_operator` 省略则用 token 租户);广播/公告/通知设置为 `platform_admin_or_tenant_operator`;email 与 platform-settings 部分为 `platform_admin`。
> 跨切面规则全程引用 FRONTEND-BUILD-SPEC:错误码映射 §5.1、钱字段 §5.2、分页/过滤 §5.4、money/admin 变更必须等服务端确认(无乐观 UI)§5.7、PSP-stub §6、i18n §6。

---

### /admin (ops cockpit)

> **设计并实现 HUAKAI admin 控制台首页 `/admin`(ops cockpit),受众 admin,auth guard = admin(`app/(admin)/page.tsx`)。** Next.js 15 App Router + Tailwind 4 + TanStack Query。这是运维总览驾驶舱:平台用量快照 + 通知 worker 健康 + 内容审核活跃度入口卡片,作为各 admin 子页的着陆页。
>
> **Endpoints**(本切片 cockpit 仅聚合下列真实只读端点;providers/channels/accounts/billing 等深度卡片属其它切片,本页只放跳转入口,不在此 wire):
> - `GET /v1/admin/usage/overview`(`getAdminUsageOverview`,tag `admin`)— 平台用量总览快照卡(响应 schema `MeUsageTimeSeriesResponse` 系列,字段属 admin-usage 切片;本页只展示其顶层汇总数字)。⚠ 该 tag 归属其它切片,本页按只读聚合卡引用,不重复实现其过滤器。
> - `GET /v1/admin/notifications/worker-stats`(`getAdminNotificationWorkerStats`,tag `admin-notifications`,**platform_admin**)— 订阅提醒/到期 worker 计数器;响应 `AdminNotificationWorkerStats`:`reminder{tick_count,sent_total,failed_ticks}`、`expiry{tick_count,expired_total,failed_ticks}`、`auto_renew{enabled,tick_count,renewed_total,skipped_total,failed_ticks}`、`pending_reconciliation{usage_records,query_failed}`。用于"通知系统健康"卡(`failed_ticks>0` 标红)与 billing pending 对账入口(`usage_records>0` 跳转 `/admin/v1/usage?pending_reconciliation_only=true`)。
> - `GET /v1/admin/moderation/logs`(`listModerationLogs`,tag `admin-moderation`)— 取最近审核决策做"今日拦截"小卡(响应 `ModerationLogListResponse`,见 /admin/moderation;cockpit 只取计数与最近几行)。
>
> **States**:loading(骨架卡片);empty(worker 计数全 0 显示"尚无活动");error 按 §5.1 映射 `401`→跳登录、`403`→"需要 platform_admin 权限"(worker-stats 卡单独降级,不整页崩)、`503 ServiceBusy`→"系统繁忙,稍后重试";cockpit 卡片各自独立加载,单卡失败不连累整页。
> **Money/Streaming**:用量总览若展示收入/成本数字,按 §5.2 直接渲染服务端 decimal string(micro-USD / `numeric(20,8)`),禁止前端浮点重算。
> **UX goals**:卡片网格布局,每张卡一句标题 + 主数字 + 跳转到对应深度子页(`/admin/users` `/admin/moderation` `/admin/notifications` `/admin/settings` …);worker `failed_ticks` 非 0 用 warning/critical 徽章;`platform_admin` 专属卡(worker-stats)在 `tenant_operator` 下隐藏或显示"无权限"占位。
>
> 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/admin-cockpit.ts(typed 调用,聚合上述端点)+ 正确的 app/(admin)/page.tsx 路由段。

---

### /admin/users

> **设计并实现 HUAKAI admin 用户管理页 `/admin/users`,受众 admin,auth guard = admin。** Next.js 15 App Router + Tailwind 4 + TanStack Query。tag `admin-users`(`tenant_operator` 作用域)。服务端分页用户表 + 单用户详情抽屉 + 恢复类动作(unlock / 强制关 2FA / 清 passkey / 解绑社交登录)+ 分组/备注编辑 + 余额台账。
>
> **Endpoints**(全部真实 path/operationId/字段):
> - `GET /admin/v1/users`(`listAdminUsers`)— 分页用户列表;query:`limit`(1–100,默认 50)、`offset`(默认 0)、`q`(email/display-name 大小写不敏感子串过滤)。响应 `AdminUserList`:`{items[],limit,offset}`,`items[]`=`AdminUser`:`id`、`email`、`role`(enum `admin|user`)、`status`(enum `pending_verification|active|disabled|locked|reset_required|deleted`)、`balance`(decimal string)、`created_at`。
> - `GET /admin/v1/users/{id}`(`getAdminUser`)— 单用户详情(跨租户 id 返回 404 反枚举)。
> - `POST /admin/v1/users/{id}/unlock`(`adminUnlockUser`)— 清除锁定计数,`locked`→`active`,写 `unlock_user` 审计;响应 `{id,status}`。
> - `POST /admin/v1/users/{id}/2fa/force-disable`(`adminForceDisableUserTwoFA`)— 强制清除 TOTP 2FA;响应 `{id,two_factor_enabled}`。
> - `DELETE /admin/v1/users/{id}/passkeys`(`adminResetUserPasskeys`)— 清除该用户全部 passkey;响应 `{id,cleared}`(cleared=数量)。
> - `PUT /admin/v1/users/{id}/group`(`adminSetUserGroup`)— 设置路由分组;body `{group}`(1–64 字符);响应 `{id,user_group}`。
> - `PUT /admin/v1/users/{id}/remark`(`adminSetUserRemark`)— 设置管理员备注;body `{remark}`(≤1024);响应 `{id,remark}`。
> - `DELETE /admin/v1/users/{id}/account-bindings/{provider}`(`adminUnlinkUserAccountBinding`)— 解绑社交登录;`provider` path enum `google|github|wechat|dingtalk|linuxdo|oidc`;响应 `{unlinked}`;末个登录方式且无本地密码时返回 `409 Conflict`。
> - `GET /admin/v1/users/{id}/balance-history`(`listAdminUserBalanceHistory`)— 该用户 append-only 余额台账(最新优先);query `limit`(1–100,默认 50)、`offset`。响应 `AdminUserBalanceHistoryList`:`{items[],limit,offset}`,item=`{id,event_type,amount,fingerprint,source_type,source_id,occurred_at}`,`event_type` enum `claim_committed|claim_aborted|reconciliation_appended|voucher_redeemed|balance_recharged|payment_credited|payment_refunded`,`source_type` enum `payment_credit|payment_refund|voucher_redemption|recharge_order|billing_claim|billing_event`。
> - `GET /admin/v1/users/2fa-adoption-stats`(`getAdminTwoFAAdoptionStats`)— 租户 2FA 采用率卡;响应 `{enabled_users,total_users,enabled_rate}`。
>
> **States**:loading(表格骨架);empty(`items` 空 → "无用户" / 过滤无命中提示清除 `q`);error 按 §5.1:`400 BadRequest`→"参数无效"、`401`→跳登录、`403 Forbidden`→"无 tenant_operator 权限"、`404 NotFound`→详情/动作"用户不存在或不在本租户"、`409 Conflict`(解绑)→"不能移除最后一个登录方式"、`503 ServiceBusy`→稍后重试;分页用 `limit/offset`(服务端,显式上一页/下一页,offset 步进 = limit)。
> **Money/Streaming**:`AdminUser.balance` 与 balance-history `amount` 均为 `numeric(20,8)` decimal string,按 §5.2 直接格式化渲染(带正负号),禁止浮点重算;event_type / source_type 用本地化徽章。
> **UX goals**:表格服务端分页列 = id / email / role / status / balance / created_at;status 用状态徽章(`locked` 高亮并在行内露出 Unlock 按钮);所有恢复类动作(unlock、force-disable 2FA、reset passkeys、unlink binding)为破坏性操作,必须经确认对话框 + 成功后等服务端确认再刷新行(§5.7,无乐观 UI);group / remark 内联编辑提交后回填响应字段;2FA-adoption-stats 渲染为顶部小卡(`enabled_rate` 百分比)。
>
> 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/admin-users.ts typed 调用 + 正确的 app/(admin)/users/page.tsx 路由段。

---

### /admin/keys

> **设计并实现 HUAKAI 平台级 API Key 管理页 `/admin/keys`,受众 admin,auth guard = admin。** Next.js 15 App Router + Tailwind 4 + TanStack Query。tag `admin-api-keys`(`tenant_operator` 作用域)。签发新 key(明文仅显示一次)+ 按租户分页列出 key 元数据 + 幂等吊销。
>
> **Endpoints**:
> - `POST /admin/v1/api-keys`(`issueAPIKey`)— 签发新 key,**限速 30 次/小时/admin token**。body `APIKeyCreate`:`tenant_id`、`user_id`、`name`(必填)、`environment`(enum `live|test`,默认 `live`)、`expires_at`(可空 date-time,必须严格未来,过去值 400)、`reason`(可选)。响应 `201` `APIKeyIssued`:`id`、`tenant_id`、`user_id`、`name`、`key_prefix`、`status`(`active`)、`expires_at`、`created_at`、**`plaintext_bearer`(完整明文 `hk_live_...`,仅此一次返回,LIST 永不回显)**;成功响应头 `X-Huakai-Key-Display: once-only`。
> - `GET /admin/v1/api-keys`(`listAPIKeys`)— 列出某租户 key 元数据(永不含明文/`key_hash`)。query:`tenant_id`(**required**,int64≥1)、`limit`(1–500,默认 50)、`offset`(默认 0)。响应 `APIKeyList`:`{items[],limit,offset}`,item=`APIKey`:`id`、`tenant_id`、`user_id`、`name`、`key_prefix`(明文前 16 字符,如 `hk_live_xxxxxxxx`)、`status`(enum `active|disabled|revoked|expired`)、`expires_at`、`last_used_at`、`revoked_at`、`revoked_reason`、`created_at`。
> - `POST /admin/v1/api-keys/{id}/revoke`(`revokeAPIKey`)— 软吊销(`status=revoked`),**幂等**:已吊销再吊销返回 `200` 且 `already_revoked=true`。body `APIKeyRevokeRequest`:`tenant_id`(必填,防误吊销跨租户)、`reason`(可选)。响应 `APIKeyRevokeResponse`:`{id,already_revoked}`。
>
> **States**:loading(表格骨架 + 签发提交态);empty(某租户 `items` 空 → 引导签发);error 按 §5.1:`400`→"参数无效 / expires_at 必须为未来"、`401`→登录、`403`→无权限、`404`(revoke)→"key 不存在"、**`429 RateLimited`→"签发已达 30 次/小时上限,稍后再试"**、`503`→稍后重试;分页 `limit/offset` 服务端(必须先选定 `tenant_id` 才能列表)。
> **Money/Streaming**:本页无钱字段、无流式。
> **UX goals**:`tenant_id` 是列表必填前置过滤(顶部租户选择器,未选则提示先选租户);**key 签发后 `plaintext_bearer` 只显示一次** —— 用 reveal-copy 组件 + 醒目"此明文仅显示一次,请立即保存"告警,关闭对话框后不可再取;签发表单 react-hook-form + zod,`expires_at` 校验严格未来;吊销为破坏性操作,需确认对话框且强制带 `tenant_id`,成功等服务端确认(§5.7),`already_revoked=true` 时给"已吊销"轻提示;表格列 = key_prefix / name / user_id / status / last_used_at / expires_at / created_at,status 徽章区分 active/disabled/revoked/expired。
>
> 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/admin-keys.ts typed 调用 + 正确的 app/(admin)/keys/page.tsx 路由段。

---

### /admin/moderation

> **设计并实现 HUAKAI 内容审核控制台 `/admin/moderation`,受众 admin,auth guard = admin。** Next.js 15 App Router + Tailwind 4 + TanStack Query。tag `admin-moderation`(`tenant_operator` 作用域)。关键词规则 + SHA-256 哈希规则(均支持单条 + 批量导入)+ 审核配置 + 审核日志 + 被封 key 列表与解封。
>
> **Endpoints**:
> - 关键词:`GET /admin/v1/moderation/keywords`(`listModerationKeywords`;query `tenant_id?`、`limit` 1–500 默认 50、`offset`;响应 `ModerationKeywordListResponse`:`{object:"moderation_keywords_list",items[],limit,offset}`,item=`ModerationKeyword`:`id,tenant_id,keyword,reason_code,enabled,created_at,updated_at`)；`POST /admin/v1/moderation/keywords`(`createModerationKeyword`;body `ModerationKeywordCreateRequest`:`tenant_id`、`keyword`(必填)、`reason_code?`、`enabled`(默认 true);201 / `409 Conflict` 重复)；`POST /admin/v1/moderation/keywords/bulk`(`bulkCreateModerationKeywords`;body items≤1000;响应 `ModerationBulkCreateResponse`:`{accepted,skipped_duplicate,errors[]}`,error=`{index,reason}` reason enum `keyword_required|invalid_hash_hex`)；`DELETE /admin/v1/moderation/keywords/{id}`(`deleteModerationKeyword`;query `tenant_id?`;204)。
> - 哈希:`GET /admin/v1/moderation/hashes`(`listModerationHashes`;响应 `ModerationHashListResponse`,item=`ModerationHash`:`id,tenant_id,hash_hex(^[0-9a-f]{64}$),reason_code,enabled,created_at,updated_at`)；`POST /admin/v1/moderation/hashes`(`createModerationHash`;body:`tenant_id`、`hash_hex`(64 位 hex)、`reason_code?`、`enabled`)；`POST /admin/v1/moderation/hashes/bulk`(`bulkCreateModerationHashes`)；`DELETE /admin/v1/moderation/hashes/{id}`(`deleteModerationHash`;204)。
> - 配置:`GET /admin/v1/moderation/config`(`getModerationConfig`)/ `PUT /admin/v1/moderation/config`(`updateModerationConfig`);schema `ModerationConfig` / `ModerationConfigUpdateRequest`:`tenant_id`、`enabled`、`fail_closed`、`sample_rate_pct`(0–100)、`ban_threshold`(≥0)、`ban_window_seconds`(≥1)、`violation_fee_usd`(decimal string,默认 "0");GET 响应另含 `updated_by,updated_at`。
> - 日志:`GET /admin/v1/moderation/logs`(`listModerationLogs`;query `tenant_id?`、`api_key_id?`、`limit` 1–500 默认 50、`offset`;响应 `ModerationLogListResponse`,item=`ModerationLog`:`id,tenant_id,api_key_id,user_id,request_id,payload_hash,decision(enum pass|block_keyword|block_hash|block_backend|fee_charged),reason_code,matched_keyword_id,matched_hash_id,violation_fee_usd,billing_event_id,occurred_at`)。
> - 被封 key:`GET /admin/v1/moderation/banned`(`listModerationBannedKeys`;响应 `ModerationBannedKeyListResponse`,item=`ModerationBannedKey`:`id,tenant_id,user_id,name,key_prefix,status:"disabled",violation_count,last_violation_at,created_at,updated_at`;永不含 `key_hash`/明文)；`POST /admin/v1/moderation/api-keys/{id}/unban`(`unbanModerationAPIKey`;body `ModerationUnbanAPIKeyRequest`:`tenant_id`(必填)、`reason?`;响应 `ModerationUnbanAPIKeyResponse`:`{api_key_id,tenant_id,status:"active",audit_log_id,updated_at}`)。
>
> **States**:loading(各 Tab 骨架);empty(无规则 / 无日志 / 无被封 key 各自占位);error 按 §5.1:`400`→"参数无效(如 hash_hex 非 64 位 hex)"、`401`→登录、`403`→无权限、`404`→规则/key 不存在、`409 Conflict`→"重复关键词/哈希规则"、`503`→稍后;批量导入为部分成功语义 —— 用 `accepted/skipped_duplicate/errors` 渲染逐行结果,`errors[].index`+`reason` 不回滚其它有效行;分页 `limit/offset` 服务端。
> **Money/Streaming**:`ModerationConfig.violation_fee_usd` 与 `ModerationLog.violation_fee_usd` 为 `numeric(20,8)` decimal string,按 §5.2 直接渲染,禁浮点重算;无流式。
> **UX goals**:多 Tab(关键词 / 哈希 / 配置 / 日志 / 被封 key);规则增删为破坏性,删除需确认;批量导入支持粘贴/CSV → 调 bulk,展示 accepted/skipped/errors 汇总;config 表单(`fail_closed`、`sample_rate_pct` 滑块、`ban_threshold`/`ban_window_seconds`、`violation_fee_usd`)保存等服务端确认(§5.7);日志表服务端分页 + 按 `api_key_id` 过滤,`decision` 用颜色徽章;unban 为破坏性恢复动作,需确认对话框且强制带 `tenant_id`,成功后从被封列表移除并提示 `audit_log_id`。
>
> 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/admin-moderation.ts typed 调用 + 正确的 app/(admin)/moderation/page.tsx 路由段。

---

### /admin/notifications

> **设计并实现 HUAKAI 通知/公告/邮件运营页 `/admin/notifications`,受众 admin,auth guard = admin。** Next.js 15 App Router + Tailwind 4 + TanStack Query。tags `admin-notifications` + `admin-announcements` + `admin-email`(广播/公告为 `platform_admin_or_tenant_operator`,email 设置可为 `platform_admin`)。三块:站内广播通知、公告 CRUD、SMTP 邮件设置与测试。
>
> **Endpoints**:
> - 广播 / worker(`admin-notifications`):`POST /v1/admin/notifications/broadcast`(`adminBroadcastNotification`;body `UserNotificationBroadcastRequest`:`tenant_id?`(platform_admin 必填)、`title`(必填)、`body`(必填)、`severity`(enum `info|warning|critical`,默认 info);响应 `201` `UserNotificationBroadcastResponse`:`{object:"notification_broadcast",tenant_id,inserted}` — 每个活跃接收者插一行)；`GET /v1/admin/notifications/worker-stats`(`getAdminNotificationWorkerStats`,**platform_admin**;响应 `AdminNotificationWorkerStats`:`reminder{tick_count,sent_total,failed_ticks}`、`expiry{tick_count,expired_total,failed_ticks}`、`auto_renew{enabled,tick_count,renewed_total,skipped_total,failed_ticks}`、`pending_reconciliation{usage_records,query_failed}`)。
> - 单用户通知设置(`admin-notifications`):`GET /v1/admin/users/{user_id}/notifications`(`adminGetUserNotificationSettings`)/ `PUT`(`adminUpdateUserNotificationSettings`);query `tenant_id?`(platform_admin 必填);schema `NotificationSettings`(读,密钥已脱敏:`webhook_secret_configured`、`gotify_token_configured` 为布尔)/ `NotificationSettingsUpdate`(写);字段 `notify_type`(enum `none|email|webhook|bark|gotify`)、`webhook_url`、`notification_email`、`bark_url`、`gotify_url`、`gotify_priority`(0–10 默认 5)、`balance_threshold`(decimal string)、写侧 `webhook_secret`/`gotify_token`(writeOnly)。
> - 公告(`admin-announcements`):`GET /v1/admin/announcements`(`adminListAnnouncements`;query `tenant_id?`、`limit` 1–100 默认 50、`offset`;响应 `AnnouncementListResponse`:`{object:"announcement_list",items[],limit,offset}`,含 inactive/expired/future 行)；`POST /v1/admin/announcements`(`adminCreateAnnouncement`;body `AnnouncementCreateRequest`:`tenant_id?`、`title`、`body`(必填)、`severity`(enum `info|warning|critical` 默认 info)、`active`(默认 true)、`published_at?`(默认服务端时间)、`expires_at?`)；`PUT /v1/admin/announcements/{id}`(`adminUpdateAnnouncement`)；`DELETE /v1/admin/announcements/{id}`(`adminDeleteAnnouncement`;响应 `AnnouncementDeleteResponse`);`Announcement` 响应字段:`id,tenant_id,title,body,severity,active,published_at,expires_at,created_by_admin,created_at,updated_at`。
> - 邮件(`admin-email`):`GET /v1/admin/email/settings`(`getAdminEmailSettings`;query `tenant_id`(**required**);响应 `AdminEmailSettingsResponse`:`{tenant_id,settings[]}`,item `AdminEmailSettingItem`:`key`(enum `smtp_host|smtp_port|smtp_username|smtp_password|smtp_from|smtp_from_name|smtp_use_tls|email_verify_enabled`)、`value`(smtp_password 为空)、`configured`(仅 smtp_password,表是否已配)、`updated_at,updated_by`)；`PUT /v1/admin/email/settings`(`updateAdminEmailSettings`;body `AdminEmailSettingsUpdate`:`tenant_id`(必填)、`smtp_host,smtp_port(1–65535),smtp_username,smtp_password`(写侧、加密存储、GET 永不返回)、`smtp_from,smtp_from_name,smtp_use_tls,email_verify_enabled`;响应 `{tenant_id,updated}`)；`POST /v1/admin/email/test`(`testAdminEmailSettings`;body `{tenant_id,to}`;响应 `{tenant_id,sent}`)。
>
> **States**:loading(各区块骨架);empty(无公告 / 无通知设置占位 / SMTP 未配显示引导);error 按 §5.1:`400`→"参数无效"、`401`→登录、`403`→无权限(worker-stats 与 email 在非 platform_admin 时降级)、`404`→公告/用户不存在、`409 Conflict`(广播)→"广播冲突,稍后重试"、`503`→稍后;公告列表分页 `limit/offset` 服务端。
> **Money/Streaming**:`NotificationSettings.balance_threshold` 为 `numeric(20,8)` decimal string,按 §5.2 渲染,禁浮点;无流式。
> **UX goals**:广播为面向全租户活跃用户的破坏性/影响面操作,需确认对话框并预览 `severity`,提交后显示 `inserted` 条数;worker-stats 卡 `failed_ticks>0` 标红(platform_admin only);公告 CRUD 表格服务端分页(列 title/severity/active/published_at/expires_at),增改删均需确认 + 等服务端确认(§5.7),`severity` 用 info/warning/critical 徽章;**SMTP `smtp_password` 为 write-only**,UI 用占位掩码 + `configured` 指示"已配置",从不回显明文;"发送测试邮件"调 test 端点并以 `sent` 布尔反馈;通知设置中 `webhook_secret`/`gotify_token` 同样 write-only,用 `*_configured` 布尔显示状态。
>
> 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/admin-notifications.ts typed 调用 + 正确的 app/(admin)/notifications/page.tsx 路由段。

---

### /admin/settings

> **设计并实现 HUAKAI 平台设置页 `/admin/settings`,受众 admin,auth guard = admin。** Next.js 15 App Router + Tailwind 4 + TanStack Query。tag `admin-platform-settings`(**platform_admin**)。allow-list 平台开关/参数的读取与逐项更新(注册、邀请、验证码、2FA、OAuth、促销、流超时、冷却、passkey)。
>
> **Endpoints**:
> - `GET /v1/admin/platform-settings`(`listPlatformSettings`)— 列出全部 allow-listed 设置(未设置项用安全默认合并);响应 `PlatformSettingListResponse`:`{items[]}`,item=`PlatformSetting`:`key`、`value`(string)、`source`(enum `default|db`)、`updated_at`(可空)、`updated_by`(可空)。
> - `GET /v1/admin/platform-settings/{key}`(`getPlatformSetting`)— 读单项(`key` path enum,见下)。
> - `PUT /v1/admin/platform-settings/{key}`(`updatePlatformSetting`)— 更新单项;body `PlatformSettingUpdateRequest`:`value`(必填,布尔项用 `"true"`/`"false"`,超时/冷却用正整数秒字符串)、`reason?`(写入审计);响应 `PlatformSetting`;请求体 >64 KiB 返回 `413`。
> - `key` allow-list 枚举(GET 单项 / PUT path 与 `PlatformSetting.key` 同):`registration_enabled`、`invitation_required`、`captcha_enabled`、`two_factor_enabled`、`captcha_provider`、`captcha_site_key`、`oauth_providers_enabled`、`promo_enabled`、`stream_timeout_seconds`、`cooldown_429_seconds`、`cooldown_529_seconds`、`passkey_enabled`、`passkey_registration_enabled`、`passkey_rp_id`、`passkey_rp_display_name`、`passkey_rp_origins`。
>
> **States**:loading(设置列表骨架);empty(理论上恒返回默认合并,无真正 empty);error 按 §5.1:`400`→"取值无效(布尔须 true/false、秒须正整数)"、`401`→登录、`403 Forbidden`→"需要 platform_admin 权限"、`413`→"请求体超过 64 KiB"、`503`→稍后;无分页(全量 allow-list)。
> **Money/Streaming**:本页无钱字段、无流式。
> **UX goals**:按语义分组渲染表单(认证/注册:registration/invitation/2fa/captcha\*;OAuth:oauth_providers_enabled;运营:promo_enabled;网关运行时:stream_timeout_seconds/cooldown_429/cooldown_529;Passkey:passkey_\*);布尔型用开关、秒数用数字输入(提交时序列化为字符串 value)、字符串型(captcha_provider/site_key、passkey_rp_*)用文本框;每项显示 `source` 徽章(`default` vs `db`)与 `updated_by`/`updated_at` 出处;逐项 PUT(非整表提交),保存等服务端确认(§5.7),可选 `reason` 输入写入审计;改安全敏感项(registration_enabled、two_factor_enabled、passkey_*)弹确认对话框。
>
> ⚠ 注:本页仅覆盖 `admin-platform-settings` allow-list;TLS fingerprint profiles(`/v1/admin/tls-fingerprint-profiles`)属其它 tag/切片,不在本切片 wire,如同页展示需后端契约确认后另接。
>
> 类型来自 lib/api/schema.d.ts(由 openapi 生成),禁止手写 shape;交付页面组件 + lib/api/admin-settings.ts typed 调用 + 正确的 app/(admin)/settings/page.tsx 路由段。

---

## 附录 A:前端揪出的后端契约缺口（"完美对接"真发现 → 前端驱动的后端 backlog）

下面是产出逐页提示词时,agent 实读 openapi 后发现**前端需要但后端当前无契约**的点。这些不是猜测,是
grep 实证。建议作为加性后端小任务补齐(均非 money 账本/auth 语义破坏,多为 read-only 或自助端点):

- **S1** — ⚠ **无后端契约——需后端补或换方案**：openapi 中**不存在已登录用户改密码端点**（grep `change-password`/`/v1/me/password` 全空）。唯一改密路径是 `POST /v1/auth/reset-password`（token 制，走邮件，且会**撤销所有 session**），它 `security: []` 属忘记密码流。account 页的"修改密码"要么 (a) 复用 reset-password 走"给我发重置邮件"按钮（体验差、会登出全设备），要么 (b) 让后端补一个 `sessionBearerAuth` 的 `POST /v1/auth/me/password`（带 `current_password`+`new_password`）。**先按 (a) 实现并显著标注"将通过邮件重置并登出所有设备"，同时挂 TODO 等后端补 (b)。**
- **S3** — ⚠ 无后端契约**:本页「生成/获取我的邀请码或邀请链接」**在本切片负责的 `invitations` me-端点中无对应 endpoint**(`/v1/me/invitations` 仅返回奖励汇总计数,不含邀请码;`/v1/invitations`、`/v1/auth/validate-invitation-code` 属其它切片)。邀请码展示需后端补 me-邀请码端点或由 Auth/账户切片提供,**不要编造 code 字段**;在此前用占位/复制按钮指向已有来源。`GET /v1/me/referrals` 与 `/v1/me/referrals/rewards` 响应为通用 object(schema 未细化字段),按服务端实际返回容错渲染,字段缺失不崩。
- **S5** — ⚠ 无后端契约:模板**执行**(实际发起测试请求拿结果)目前无对应端点(openapi 明确「only reads stored templates; execution is an explicit future action」)。UI 只做模板的 CRUD,不要伪造一个「运行测试」按钮的后端;如需展示渠道健康用 `/admin/accounts` 页的 channel-health 数据。
- **S6** — ⚠ 无后端契约:本切片**没有**通用 `GET /admin/v1/credentials` 凭证列表/CRUD。凭证保险库的「读」面 = `renew-status`(元数据 only)+ 获取流;凭证的**直接写/轮换/状态变更**(`AccountCredentialWriteRequest`/`AccountCredentialRotateRequest`/`AccountCredentialStateRequest`/`AccountCredentialPatchStateRequest`)挂在 `admin-accounts`(`/admin/v1/provider-accounts/{id}/...`)tag 下,属 `/admin/accounts` 页面职责——本页只做获取/导入/续期监控,凭证明文/指纹永不可读取。
- **S6** — ⚠ 无后端契约:本 tag 下**只有这一个 POST 触发端点**,无独立的「同步配置查询 / 同步历史列表 / 厂商启用开关」GET。若页面想展示「已配置厂商列表 / 上次同步时间 / 历史记录」需后端补端点或换方案(可在前端缓存上次触发返回的结果作为「最近一次同步」展示,但跨会话历史无契约)。

> 处置:逐条核「三 refs 是否也缺」——refs 有则属 parity 必补;refs 也无则按产品需要评估。
> 典型可补:① 匿名 site-config/bootstrap 端点(tenant_id/注册开关/邀请码必填/passkey 可用性)
> ② 已登录用户自助改密码端点(现仅 reset-password 邮件流)③ "取我的邀请码/邀请链接"端点
> ④ channel 测试模板**执行**端点 ⑤ 通用 credentials 列表/轮换端点 ⑥ model-sync 配置查询/历史端点。

---

*真相源:`docs/openapi/openapi.yaml`。契约与本文档冲突时,契约赢——重生成类型并更新本文档。*
