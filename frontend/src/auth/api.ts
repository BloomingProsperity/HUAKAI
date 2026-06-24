import { apiSend } from '../lib/api'
import type { AuthUser } from './store'

/*
 * 认证数据访问层。端点 /v1/auth/*(公开)+ /v1/sessions/revoke(登出,需 session)。
 * 登录成功返回 {user, session:{session_token,...}};需 2FA 时返回 {two_factor_required, challenge_id}。
 */

interface SessionTokens {
  session_token: string
  refresh_token?: string
  session_expires_at?: string
}

interface PublicUser {
  user_id?: number
  id?: number
  email: string
  display_name?: string
}

interface LoginSuccess {
  user: PublicUser
  session: SessionTokens
}

interface LoginTwoFactor {
  two_factor_required: true
  challenge_id: string
  user?: PublicUser
}

export type LoginResult =
  | { kind: 'ok'; token: string; user: AuthUser }
  | { kind: '2fa'; challengeId: string }

function normUser(u: PublicUser): AuthUser {
  return { user_id: u.user_id ?? u.id ?? 0, email: u.email, display_name: u.display_name }
}

export async function login(tenantId: number, email: string, password: string): Promise<LoginResult> {
  const body = await apiSend<LoginSuccess | LoginTwoFactor>('POST', '/v1/auth/login', {
    tenant_id: tenantId,
    email,
    password,
  })
  if ('two_factor_required' in body && body.two_factor_required) {
    return { kind: '2fa', challengeId: body.challenge_id }
  }
  const ok = body as LoginSuccess
  return { kind: 'ok', token: ok.session.session_token, user: normUser(ok.user) }
}

export async function loginTwoFactor(challengeId: string, code: string): Promise<{ token: string; user: AuthUser }> {
  const body = await apiSend<LoginSuccess>('POST', '/v1/auth/login/2fa', {
    challenge_id: challengeId,
    code,
  })
  return { token: body.session.session_token, user: normUser(body.user) }
}

export async function register(
  tenantId: number,
  email: string,
  password: string,
  displayName: string,
  inviteCode: string,
): Promise<void> {
  await apiSend<unknown>('POST', '/v1/auth/register', {
    tenant_id: tenantId,
    email,
    password,
    display_name: displayName.trim() || undefined,
    invite_code: inviteCode.trim() || undefined,
  })
}

export async function logout(): Promise<void> {
  // 撤销当前 session;失败也不阻断前端清态(本地登出优先)。
  try {
    await apiSend<unknown>('POST', '/v1/sessions/revoke', {})
  } catch {
    /* 忽略:本地清态在 store 侧仍执行 */
  }
}
