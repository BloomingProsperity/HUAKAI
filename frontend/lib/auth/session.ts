// 用户会话存储（user-portal 鉴权环）。
// HUAKAI 后端登录返回 { user, session{ session_token, refresh_token, ... } }，
// 用户面调用以 session_token 作 Bearer（auth.SessionMiddleware）。
// 注意：这与管理控制台的 huakai_admin_token、Playground 推理用的 huakai_api_key 是三条独立凭证。

export const SESSION_TOKEN_KEY = 'huakai_session_token';
export const REFRESH_TOKEN_KEY = 'huakai_refresh_token';
export const USER_KEY = 'huakai_user';

export interface SessionUser {
  id: number;
  tenant_id: number;
  email: string;
  display_name: string;
  email_verified?: boolean;
  status?: string;
}

export interface SessionTokens {
  session_token: string;
  refresh_token: string;
  session_expires_at?: string;
  refresh_expires_at?: string;
  family?: string;
  generation?: number;
}

export function getSessionToken(): string {
  if (typeof window === 'undefined') return '';
  return localStorage.getItem(SESSION_TOKEN_KEY) ?? '';
}

export function getRefreshToken(): string {
  if (typeof window === 'undefined') return '';
  return localStorage.getItem(REFRESH_TOKEN_KEY) ?? '';
}

export function getStoredUser(): SessionUser | null {
  if (typeof window === 'undefined') return null;
  const raw = localStorage.getItem(USER_KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as SessionUser;
  } catch {
    return null;
  }
}

export function storeSession(tokens: SessionTokens, user?: SessionUser): void {
  if (typeof window === 'undefined') return;
  localStorage.setItem(SESSION_TOKEN_KEY, tokens.session_token);
  localStorage.setItem(REFRESH_TOKEN_KEY, tokens.refresh_token);
  if (user) localStorage.setItem(USER_KEY, JSON.stringify(user));
}

export function storeUser(user: SessionUser): void {
  if (typeof window === 'undefined') return;
  localStorage.setItem(USER_KEY, JSON.stringify(user));
}

export function clearSession(): void {
  if (typeof window === 'undefined') return;
  localStorage.removeItem(SESSION_TOKEN_KEY);
  localStorage.removeItem(REFRESH_TOKEN_KEY);
  localStorage.removeItem(USER_KEY);
}

export function isLoggedIn(): boolean {
  return getSessionToken() !== '';
}
