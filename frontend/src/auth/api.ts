import { apiSend } from '../lib/api'
import { parseIssuedTokens, type RawIssuedTokens, type SessionTokens } from './refresh'
import type { AuthUser } from './store'

/*
 * 认证数据访问层。端点 /v1/auth/*(公开)+ /v1/sessions/revoke(登出,需 session)。
 * 登录成功返回 {user, session:{session_token,refresh_token,session_expires_at,...}};
 * 需 2FA 时返回 {two_factor_required, challenge_id}。
 */

interface PublicUser {
  user_id?: number
  id?: number
  email: string
  display_name?: string
}

interface LoginSuccess {
  user: PublicUser
  session: RawIssuedTokens
}

interface LoginTwoFactor {
  two_factor_required: true
  challenge_id: string
  user?: PublicUser
}

export type LoginResult =
  | { kind: 'ok'; tokens: SessionTokens; user: AuthUser | null }
  // 2FA 第一步返回 user(后端 auth_handler.go:297 含 user),需带回供第二步完成后写入 store
  // —— 因为 2FA 完成响应(auth_handler.go:365)只回 {session} 不含 user。
  | { kind: '2fa'; challengeId: string; user: AuthUser | null }

function normUser(u: PublicUser | undefined): AuthUser | null {
  if (!u) return null
  return { user_id: u.user_id ?? u.id ?? 0, email: u.email, display_name: u.display_name }
}

/** 从登录响应的 session 字段抽出完整 token 组;后端契约保证含 session_token,缺失则报错。 */
function tokensFromSession(session: RawIssuedTokens): SessionTokens {
  const tokens = parseIssuedTokens(session)
  if (!tokens) throw new Error('登录响应缺少会话 token')
  return tokens
}

export async function login(
  tenantId: number,
  email: string,
  password: string,
  // 可选 captcha token:仅在站点启用人机验证且前端拿到 token 时传(后端 authLoginRequest.captcha_token,omitempty)。
  // 省略/空白则该字段缺席,基础邮箱密码登录的请求体与增强前完全一致。
  captchaToken?: string,
): Promise<LoginResult> {
  const body = await apiSend<LoginSuccess | LoginTwoFactor>('POST', '/v1/auth/login', {
    tenant_id: tenantId,
    email,
    password,
    captcha_token: captchaToken?.trim() || undefined,
  })
  if ('two_factor_required' in body && body.two_factor_required) {
    return { kind: '2fa', challengeId: body.challenge_id, user: normUser(body.user) }
  }
  const ok = body as LoginSuccess
  return { kind: 'ok', tokens: tokensFromSession(ok.session), user: normUser(ok.user) }
}

/**
 * 完成 2FA 登录。后端响应只含 {session} 不含 user(auth_handler.go:365),故这里只回 tokens;
 * user 由第一步(challenge)的 LoginResult.user 带过来在 store 写入。
 */
export async function loginTwoFactor(challengeId: string, code: string): Promise<{ tokens: SessionTokens }> {
  const body = await apiSend<{ session: RawIssuedTokens }>('POST', '/v1/auth/login/2fa', {
    challenge_id: challengeId,
    code,
  })
  return { tokens: tokensFromSession(body.session) }
}

export async function register(
  tenantId: number,
  email: string,
  password: string,
  displayName: string,
  inviteCode: string,
  // 可选 captcha token:同 login,仅启用人机验证且拿到 token 时传(authRegisterRequest.captcha_token,omitempty)。
  captchaToken?: string,
): Promise<void> {
  await apiSend<unknown>('POST', '/v1/auth/register', {
    tenant_id: tenantId,
    email,
    password,
    display_name: displayName.trim() || undefined,
    invite_code: inviteCode.trim() || undefined,
    captcha_token: captchaToken?.trim() || undefined,
  })
}

/*
 * 邀请码实时预校验响应。后端 invitevalidatehttp/handler.go:30 的 validateResponse:
 *   { valid: bool, reason: string }
 * reason 取自 userauth.InviteCodeStatus(types.go:97):
 *   valid / not_found / disabled / expired / used_or_exhausted。
 * 这是【只读】端点(handler.go:36,挂 routes_invitevalidate.go:20),不发证、不消费邀请码、
 * 不建立 session、不改任何鉴权状态;仅用于注册前给用户即时有效性提示。
 */
export interface InviteCodeValidation {
  valid: boolean
  reason: string
}

/**
 * 邀请码实时预校验(只读):POST /v1/auth/validate-invitation-code {tenant_id, invite_code}
 * → {valid, reason}。后端 backend/internal/invitevalidatehttp/handler.go:36。
 *
 * 仅做有效性提示,【不阻断】注册提交 —— 注册时后端 register 仍是权威校验。该端点不带 token
 * (公开预校验,不登录/不发 token/不改 session);若站点未开启邀请门,后端恒返回
 * {valid:true, reason:"disabled"}。本函数不吞异常:网络/服务错误由调用方按"提示不可用"处理。
 */
export async function validateInvitationCode(
  tenantId: number,
  inviteCode: string,
): Promise<InviteCodeValidation> {
  return apiSend<InviteCodeValidation>('POST', '/v1/auth/validate-invitation-code', {
    tenant_id: tenantId,
    invite_code: inviteCode,
  })
}

/*
 * ============================ 增强:社交登录 / 通行密钥 ============================
 * 以下端点均为登录前的公开端点(tokenForPath 对 /v1/auth/oauth、/v1/auth/passkey 返回 null,
 * 不带 token),独立于基础邮箱密码登录;任一失败只影响该增强按钮,不影响基础流程。
 */

/** oauth-init 响应(auth_handler.go newAuthOAuthInitHandler → userauth.OAuthInitResult)。 */
interface OAuthInitResponse {
  provider: string
  state: string
  auth_url: string
  expires_at?: string
}

/**
 * 发起社交登录:POST /v1/auth/oauth-init {tenant_id, provider, redirect_uri?} → 返回授权跳转 URL。
 * 调用方拿到 auth_url 后 window.location 跳转到上游授权页;后端已 set state cookie 防 CSRF。
 */
export async function oauthInit(
  tenantId: number,
  provider: string,
  redirectUri?: string,
): Promise<{ authUrl: string; provider: string; state: string }> {
  const body = await apiSend<OAuthInitResponse>('POST', '/v1/auth/oauth-init', {
    tenant_id: tenantId,
    provider,
    redirect_uri: redirectUri?.trim() || undefined,
  })
  return { authUrl: body.auth_url, provider: body.provider, state: body.state }
}

/**
 * 完成社交登录(OAuth 回调第二步):POST /v1/auth/oauth-callback {tenant_id, provider, state, code}
 * → {user, session}。后端用 state cookie 比对(防 CSRF)后用 code 向上游换取身份并建立本地会话。
 * 返回 tokens + user 供 setSessionTokens。state/code 来自上游回跳 URL,provider/tenant 来自发起时暂存。
 */
export async function completeOAuth(
  tenantId: number,
  provider: string,
  state: string,
  code: string,
): Promise<LoginResult> {
  const body = await apiSend<LoginSuccess>('POST', '/v1/auth/oauth-callback', {
    tenant_id: tenantId,
    provider,
    state,
    code,
  })
  return { kind: 'ok', tokens: tokensFromSession(body.session), user: normUser(body.user) }
}

/**
 * Telegram 登录(凭既有绑定):POST /v1/auth/telegram-login {tenant_id, params, device_info?}
 * → {user, session}。params 是 Telegram Login Widget 回传的字段集;后端用 bot token HMAC 校验,
 * 并在「先绑定后登录」模型下凭既有绑定登录(未绑定的 telegram 身份会被后端拒/挂起,前端按错误提示引导先绑定)。
 * 公开端点(/v1/auth/*),不带 token;身份凭证就是 widget 数据本身的 HMAC 签名。
 */
export async function telegramLogin(
  tenantId: number,
  params: Record<string, string>,
  deviceInfo?: Record<string, unknown>,
): Promise<LoginResult> {
  const body = await apiSend<LoginSuccess>('POST', '/v1/auth/telegram-login', {
    tenant_id: tenantId,
    params,
    device_info: deviceInfo,
  })
  return { kind: 'ok', tokens: tokensFromSession(body.session), user: normUser(body.user) }
}

/** passkey login begin 响应(passkey.BeginResponse:session_id + public_key(WebAuthn 选项 JSON))。 */
export interface PasskeyLoginBegin {
  session_id: string
  /** WebAuthn PublicKeyCredentialRequestOptions(内部 challenge/id 为 base64url 串)。 */
  public_key: unknown
  expires_at?: string
}

/**
 * 通行密钥登录第一步:POST /v1/auth/passkey/login/begin {tenant_id} → WebAuthn 选项 + session_id。
 * 公开端点(登录前),不带 token。
 */
export async function passkeyLoginBegin(tenantId: number): Promise<PasskeyLoginBegin> {
  return apiSend<PasskeyLoginBegin>('POST', '/v1/auth/passkey/login/begin', { tenant_id: tenantId })
}

/**
 * 通行密钥登录第二步:POST /v1/auth/passkey/login/finish {tenant_id, session_id, credential}
 * → {user, session}(session 同登录的 IssuedTokens)。返回 tokens + user 供 setSessionTokens。
 */
export async function passkeyLoginFinish(
  tenantId: number,
  sessionId: string,
  credential: unknown,
): Promise<LoginResult> {
  const body = await apiSend<LoginSuccess>('POST', '/v1/auth/passkey/login/finish', {
    tenant_id: tenantId,
    session_id: sessionId,
    credential,
  })
  return { kind: 'ok', tokens: tokensFromSession(body.session), user: normUser(body.user) }
}

export async function logout(): Promise<void> {
  // 撤销当前 session;失败也不阻断前端清态(本地登出优先)。
  try {
    await apiSend<unknown>('POST', '/v1/sessions/revoke', {})
  } catch {
    /* 忽略:本地清态在 store 侧仍执行 */
  }
}
