// 用户面 fetch 封装：注入 session_token，401 时单飞刷新（/v1/sessions/refresh）后重试，
// 刷新失败则清会话并踢回 /login。与 client.ts（admin token）平行，互不影响现有管理页。
import type { APIError } from './types';
import { ApiError } from './client';
import {
  getSessionToken,
  getRefreshToken,
  storeSession,
  clearSession,
} from '@/lib/auth/session';

function authHeaders(extra?: Record<string, string>): Record<string, string> {
  const token = getSessionToken();
  return {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...extra,
  };
}

// ---- 单飞刷新：并发 401 只触发一次刷新 ----
let refreshing: Promise<boolean> | null = null;

async function refreshSession(): Promise<boolean> {
  const refresh_token = getRefreshToken();
  if (!refresh_token) return false;
  try {
    const resp = await fetch('/v1/sessions/refresh', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token }),
    });
    if (!resp.ok) return false;
    const data = (await resp.json()) as {
      session?: { session_token: string; refresh_token: string };
      session_token?: string;
      refresh_token?: string;
    };
    const session = data.session ?? (data.session_token ? { session_token: data.session_token, refresh_token: data.refresh_token ?? refresh_token } : null);
    if (!session?.session_token) return false;
    storeSession({ session_token: session.session_token, refresh_token: session.refresh_token });
    return true;
  } catch {
    return false;
  }
}

function redirectToLogin(): void {
  if (typeof window === 'undefined') return;
  clearSession();
  const here = window.location.pathname;
  if (here !== '/login') {
    window.location.href = `/login?next=${encodeURIComponent(here)}`;
  }
}

async function request<T>(method: string, path: string, body?: unknown, retried = false): Promise<T> {
  const resp = await fetch(path, {
    method,
    headers: authHeaders(),
    body: body !== undefined ? JSON.stringify(body) : undefined,
    cache: 'no-store',
  });

  if (resp.status === 401 && !retried) {
    // 单飞刷新：同一时刻多个 401 共用一个刷新 Promise。
    if (!refreshing) refreshing = refreshSession().finally(() => { refreshing = null; });
    const ok = await refreshing;
    if (ok) return request<T>(method, path, body, true);
    redirectToLogin();
    throw new ApiError(401, { error: { code: 'session_expired', message: '会话已过期，请重新登录' } });
  }

  return parse<T>(resp);
}

async function parse<T>(resp: Response): Promise<T> {
  if (resp.ok) {
    if (resp.status === 204) return undefined as T;
    return resp.json() as Promise<T>;
  }
  let payload: APIError;
  try {
    payload = (await resp.json()) as APIError;
  } catch {
    throw new ApiError(resp.status, { error: { code: 'http_error', message: `HTTP ${resp.status}` } });
  }
  throw new ApiError(resp.status, payload);
}

export function userGet<T>(path: string, params?: Record<string, string | number | boolean | undefined>): Promise<T> {
  let url = path;
  if (params) {
    const filtered = Object.entries(params).filter(([, v]) => v !== undefined) as [string, string | number | boolean][];
    if (filtered.length > 0) {
      url = `${path}?${new URLSearchParams(filtered.map(([k, v]) => [k, String(v)])).toString()}`;
    }
  }
  return request<T>('GET', url);
}

export const userPost = <T>(path: string, body?: unknown): Promise<T> => request<T>('POST', path, body);
export const userPut = <T>(path: string, body?: unknown): Promise<T> => request<T>('PUT', path, body);
export const userPatch = <T>(path: string, body?: unknown): Promise<T> => request<T>('PATCH', path, body);
export const userDelete = <T>(path: string, body?: unknown): Promise<T> => request<T>('DELETE', path, body);
