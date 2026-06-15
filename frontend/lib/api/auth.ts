// 鉴权 API：对接 HUAKAI /v1/auth/* + /v1/sessions/*。
// 登录成功返回 { user, session{ session_token, refresh_token, ... } }；
// 注册返回 { user, verification_required }（无 token，需邮箱验证后再登录）；
// 登录命中 2FA 时后端回 202 { user, two_factor_required, challenge_id }。
import { userGet, userPost } from './userClient';
import { storeSession, storeUser, getStoredUser, clearSession, type SessionUser, type SessionTokens } from '@/lib/auth/session';

export interface LoginRequest {
  tenant_id: number;
  email: string;
  password: string;
}

export interface LoginSuccess {
  user: SessionUser;
  session: SessionTokens;
  two_factor_required?: false;
}

export interface LoginTwoFactor {
  user: SessionUser;
  two_factor_required: true;
  challenge_id: string;
  challenge_expires_at?: string;
}

export type LoginResponse = LoginSuccess | LoginTwoFactor;

export function isTwoFactor(r: LoginResponse): r is LoginTwoFactor {
  return (r as LoginTwoFactor).two_factor_required === true;
}

export async function login(req: LoginRequest): Promise<LoginResponse> {
  const resp = await fetch('/v1/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
  const data = (await resp.json().catch(() => ({}))) as Record<string, unknown>;
  if (!resp.ok) {
    const err = (data as { error?: { code?: string; message?: string } }).error;
    throw Object.assign(new Error(err?.message ?? `HTTP ${resp.status}`), { status: resp.status, code: err?.code ?? 'login_failed' });
  }
  const r = data as unknown as LoginResponse;
  if (!isTwoFactor(r) && r.session?.session_token) {
    storeSession(r.session, r.user);
  }
  return r;
}

export interface RegisterRequest {
  tenant_id: number;
  email: string;
  display_name: string;
  password: string;
  invite_code?: string;
}

export interface RegisterResponse {
  user: SessionUser;
  verification_required: boolean;
}

export async function register(req: RegisterRequest): Promise<RegisterResponse> {
  const resp = await fetch('/v1/auth/register', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  });
  const data = (await resp.json().catch(() => ({}))) as Record<string, unknown>;
  if (!resp.ok) {
    const err = (data as { error?: { code?: string; message?: string } }).error;
    throw Object.assign(new Error(err?.message ?? `HTTP ${resp.status}`), { status: resp.status, code: err?.code ?? 'register_failed' });
  }
  return data as unknown as RegisterResponse;
}

// GET /v1/auth/me 实际返回 { panel, user_id, tenant_id, display_name }（无 email，
// 字段是 user_id 不是 id —— 已由 frontend_wiring_test 对真后端核对）。映射到 SessionUser，
// 并保留登录时存下的 email（me 不返回 email）。
interface AuthMeResponse {
  panel?: string;
  user_id?: number;
  id?: number;
  tenant_id?: number;
  display_name?: string;
  email?: string;
}

export async function fetchMe(): Promise<SessionUser> {
  const data = await userGet<AuthMeResponse>('/v1/auth/me');
  const stored = getStoredUser();
  const user: SessionUser = {
    id: data.user_id ?? data.id ?? stored?.id ?? 0,
    tenant_id: data.tenant_id ?? stored?.tenant_id ?? 0,
    email: data.email ?? stored?.email ?? '',
    display_name: data.display_name ?? stored?.display_name ?? '',
  };
  storeUser(user);
  return user;
}

export async function logout(): Promise<void> {
  try {
    await userPost('/v1/auth/logout', {});
  } catch {
    // 即便后端登出失败，本地也要清干净。
  } finally {
    clearSession();
  }
}
