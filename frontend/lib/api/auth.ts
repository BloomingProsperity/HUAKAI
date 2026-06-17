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

// 提交 2FA 挑战码换正式会话(登录命中 2FA 后的第二步)。
// 后端 POST /v1/auth/login/2fa {challenge_id, code} 成功只返 { session }(不回 user),
// 故 user 取自第一步 login 的 202 响应一并存入会话——漏带 user 会导致会话缺用户信息。
export interface LoginTwoFactorVerifyInput {
  challenge_id: string;
  code: string;
  user: SessionUser;
}

export async function verifyLoginTwoFactor(input: LoginTwoFactorVerifyInput): Promise<SessionUser> {
  const resp = await fetch('/v1/auth/login/2fa', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ challenge_id: input.challenge_id, code: input.code }),
  });
  const data = (await resp.json().catch(() => ({}))) as Record<string, unknown>;
  if (!resp.ok) {
    const err = (data as { error?: { code?: string; message?: string } }).error;
    throw Object.assign(new Error(err?.message ?? `HTTP ${resp.status}`), {
      status: resp.status,
      code: err?.code ?? 'two_factor_failed',
    });
  }
  const session = (data as { session?: SessionTokens }).session;
  if (!session?.session_token) {
    throw Object.assign(new Error('两步验证响应缺少会话令牌'), { status: resp.status, code: 'two_factor_no_session' });
  }
  storeSession(session, input.user);
  return input.user;
}

// ── 社交 / OAuth 登录(两步:init 拿授权地址跳转 → callback 换会话)─────────

// 第一步:发起 OAuth。后端 POST /v1/auth/oauth-init {tenant_id, provider, redirect_uri}
// 返 {provider, state, auth_url} 并【设 state cookie 做 CSRF】,故 credentials 必须带上,
// 否则回调时 cookie 缺失、后端 state 校验失败。前端拿到 auth_url 后整页跳转到第三方授权页。
export interface OAuthInitResult {
  provider: string;
  state: string;
  auth_url: string;
}

export async function startOAuth(input: { tenant_id: number; provider: string; redirect_uri: string }): Promise<OAuthInitResult> {
  const resp = await fetch('/v1/auth/oauth-init', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    body: JSON.stringify(input),
  });
  const data = (await resp.json().catch(() => ({}))) as Record<string, unknown>;
  if (!resp.ok) {
    const err = (data as { error?: { code?: string; message?: string } }).error;
    throw Object.assign(new Error(err?.message ?? `HTTP ${resp.status}`), { status: resp.status, code: err?.code ?? 'oauth_init_failed' });
  }
  const r = data as unknown as OAuthInitResult;
  if (!r.auth_url) {
    throw Object.assign(new Error('OAuth 初始化未返回授权地址'), { status: resp.status, code: 'oauth_no_auth_url' });
  }
  return r;
}

// 第二步:第三方授权后回跳到我们的回调页,带 code+state。POST /v1/auth/oauth-callback
// {tenant_id, provider, state, code} 成功返 {user, session}(后端自动建/认新用户),直接存会话。
// credentials 必须带上以回传 state cookie 供后端比对。
export async function completeOAuth(input: { tenant_id: number; provider: string; state: string; code: string }): Promise<SessionUser> {
  const resp = await fetch('/v1/auth/oauth-callback', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    body: JSON.stringify(input),
  });
  const data = (await resp.json().catch(() => ({}))) as Record<string, unknown>;
  if (!resp.ok) {
    const err = (data as { error?: { code?: string; message?: string } }).error;
    throw Object.assign(new Error(err?.message ?? `HTTP ${resp.status}`), { status: resp.status, code: err?.code ?? 'oauth_callback_failed' });
  }
  const r = data as { user?: SessionUser; session?: SessionTokens };
  if (!r.session?.session_token || !r.user) {
    throw Object.assign(new Error('OAuth 回调响应缺少会话或用户'), { status: resp.status, code: 'oauth_no_session' });
  }
  storeSession(r.session, r.user);
  return r.user;
}

// ── Passkey 免密登录(login/begin 拿挑战 → 浏览器 get → login/finish 换会话)────────

// 登录前调用,无 session(走裸 fetch)。begin 返回标准请求选项(challenge+allowCredentials),
// finish 返 {user, session}(后端自动认人),直接存会话。
export interface PasskeyLoginBeginResponse {
  session_id: string;
  public_key: { publicKey: Record<string, unknown> };
  expires_at?: string;
}

export async function passkeyLoginBegin(tenantId: number): Promise<PasskeyLoginBeginResponse> {
  const resp = await fetch('/v1/auth/passkey/login/begin', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    body: JSON.stringify({ tenant_id: tenantId }),
  });
  const data = (await resp.json().catch(() => ({}))) as Record<string, unknown>;
  if (!resp.ok) {
    const err = (data as { error?: { code?: string; message?: string } }).error;
    throw Object.assign(new Error(err?.message ?? `HTTP ${resp.status}`), { status: resp.status, code: err?.code ?? 'passkey_login_begin_failed' });
  }
  const r = data as unknown as PasskeyLoginBeginResponse;
  if (!r.session_id || !r.public_key) {
    throw Object.assign(new Error('通行密钥登录初始化响应不完整'), { status: resp.status, code: 'passkey_login_no_options' });
  }
  return r;
}

export async function passkeyLoginFinish(input: { tenant_id: number; session_id: string; credential: unknown }): Promise<SessionUser> {
  const resp = await fetch('/v1/auth/passkey/login/finish', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    credentials: 'same-origin',
    body: JSON.stringify(input),
  });
  const data = (await resp.json().catch(() => ({}))) as Record<string, unknown>;
  if (!resp.ok) {
    const err = (data as { error?: { code?: string; message?: string } }).error;
    throw Object.assign(new Error(err?.message ?? `HTTP ${resp.status}`), { status: resp.status, code: err?.code ?? 'passkey_login_failed' });
  }
  const r = data as { user?: SessionUser; session?: SessionTokens };
  if (!r.session?.session_token || !r.user) {
    throw Object.assign(new Error('通行密钥登录响应缺少会话或用户'), { status: resp.status, code: 'passkey_login_no_session' });
  }
  storeSession(r.session, r.user);
  return r.user;
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
