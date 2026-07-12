/*
 * 个人资料·安全 前端类型 —— 镜像后端 JSON DTO。端点全为已登录用户自助态,
 * 身份取自 session,前端绝不传 user_id/tenant_id(后端只信 session)。
 *
 * 端点与 DTO 来源(真码 file:line):
 *  - GET    /v1/auth/me                     controlhttp/panelauth_handler.go:57  meResponse
 *  - PUT    /v1/auth/me/profile             controlhttp/panelauth_handler.go:68  profileResponse
 *  - POST   /v1/auth/me/password            controlhttp/self_account_handler.go:81 {changed, sessions_revoked}
 *  - DELETE /v1/auth/me                     controlhttp/self_account_handler.go:112 {deleted, sessions_revoked}
 *  - GET    /v1/auth/2fa/status             controlhttp/twofa_handler.go:136
 *  - POST   /v1/auth/2fa/setup              twofa/types.go:60  SetupResult
 *  - POST   /v1/auth/2fa/enable|disable     twofa/types.go:77  Status
 *  - POST   /v1/auth/2fa/backup-codes/regenerate twofa/types.go:84 BackupCodesResult
 *  - GET    /v1/me/passkeys/                passkey/types.go:79  CredentialSummary[]
 *  - POST   /v1/me/passkeys/register/begin  passkey/types.go:102 BeginResponse
 *  - POST   /v1/me/passkeys/register/finish passkeyhttp/handler.go:33 {passkey}
 *  - DELETE /v1/me/passkeys/{id}            {deleted}
 *  - GET    /v1/users/me/oauth-bindings     controlhttp/oauth_bindings_handler.go:33 {bindings}
 *  - DELETE /v1/users/me/oauth-bindings/{provider} {unlinked}
 */

/** GET /v1/auth/me 响应 —— 面板归属 + 自身 id + 显示名(不含敏感字段)。 */
export interface MeResponse {
  panel: string
  user_id: number
  tenant_id: number
  display_name: string
}

/** PUT /v1/auth/me/profile 响应。 */
export interface ProfileResponse {
  user_id: number
  tenant_id: number
  display_name: string
}

/** POST /v1/auth/me/password 响应。 */
export interface ChangePasswordResponse {
  changed: boolean
  sessions_revoked: number
}

/** DELETE /v1/auth/me 响应。 */
export interface DeleteSelfResponse {
  deleted: boolean
  sessions_revoked: number
}

/** GET /v1/auth/2fa/status 响应。available=平台开关;enabled=本人是否已开。 */
export interface TwoFAStatus {
  available: boolean
  enabled: boolean
  backup_codes_remaining: number
  locked_until?: string
  last_used_at?: string
}

/** POST /v1/auth/2fa/setup 响应(twofa.SetupResult)。secret/qr_data 仅一次性展示,绝不持久化。 */
export interface TwoFASetupResult {
  secret: string
  qr_data: string
  backup_codes: string[]
}

/** POST /v1/auth/2fa/backup-codes/regenerate 响应(twofa.BackupCodesResult)。 */
export interface BackupCodesResult {
  backup_codes: string[]
}

/** 通行密钥摘要(passkey.CredentialSummary)。 */
export interface PasskeyItem {
  id: number
  name?: string
  transports?: string[]
  attestation_type?: string
  clone_warning: boolean
  sign_count: number
  created_at: string
  last_used_at?: string
}

/** GET /v1/me/passkeys/ 响应。 */
export interface PasskeyListResponse {
  passkeys: PasskeyItem[]
}

/** Passkey 敏感动作的二次验证。密码与两步验证码二选一。 */
export interface PasskeyStepUp {
  password?: string
  two_factor_code?: string
}

/** 注册第一步响应。public_key 是浏览器 WebAuthn creation options。 */
export interface PasskeyRegisterBeginResponse {
  session_id: string
  public_key: unknown
  expires_at: string
}

/** 注册完成响应。 */
export interface PasskeyRegisterFinishResponse {
  passkey: PasskeyItem
}

/** 单条社交登录绑定(oauthBindingResponse)。subject 已在后端脱敏。 */
export interface OAuthBinding {
  provider: string
  subject: string
  linked_at: string
}

/** GET /v1/users/me/oauth-bindings 响应。 */
export interface OAuthBindingsResponse {
  bindings: OAuthBinding[]
}
