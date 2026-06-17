// TLS 指纹画像（反 ban / mimicry uTLS 配置）admin 写/详情数据层（管理 token 轨）。
// 列表 GET 已在 adminSettings.ts(listTLSProfiles)；本模块补 5 个零覆盖写/详情端点。
// 后端 tlsfphttp/handler.go MountTLSFPAdminRoutes（挂 /v1/admin/tls-fingerprint-profiles）：
//   POST   /                 body 带 tenant_id   → {profile}(201)
//   GET    /{id}?tenant_id                       → {profile}
//   PUT    /{id}?tenant_id    body 无 tenant_id/status → {profile}
//   POST   /{id}/status?tenant_id  body {status} → {profile}
//   DELETE /{id}?tenant_id                       → {deleted,id}
// tenant：create 在 **body**；get/update/status/delete 在 **query**。走 client.ts apiGet/apiPost +
// 本模块自带 adminPut/adminDelete（client.ts 未导出 PUT/DELETE）；沿 adminAnnouncements.ts 助手约定。
import { ApiError, apiGet, apiPost } from './client';
import type { TLSFingerprintProfile } from './adminSettings';
import type { APIError } from './types';
import {
  buildTLSProfileCreateBody,
  buildTLSProfileUpdateBody,
  isValidTLSProfileStatus,
  validateTLSProfileInput,
  type TLSProfileInput,
} from './tls-profile-form';

function adminToken(): string {
  if (typeof window === 'undefined') return '';
  return localStorage.getItem('huakai_admin_token') ?? '';
}

function adminHeaders(): Record<string, string> {
  const token = adminToken();
  return {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  };
}

async function parse<T>(resp: Response): Promise<T> {
  if (resp.ok) {
    if (resp.status === 204) return undefined as T;
    return (await resp.json()) as T;
  }
  let payload: APIError;
  try {
    payload = (await resp.json()) as APIError;
  } catch {
    throw new Error(`HTTP ${resp.status}`);
  }
  throw new ApiError(resp.status, payload);
}

async function adminPut<T>(path: string, body: unknown): Promise<T> {
  const resp = await fetch(path, { method: 'PUT', headers: adminHeaders(), body: JSON.stringify(body) });
  return parse<T>(resp);
}

async function adminDelete<T>(path: string): Promise<T> {
  const resp = await fetch(path, { method: 'DELETE', headers: adminHeaders() });
  return parse<T>(resp);
}

function tenantQuery(tenantId: number): string {
  return `?tenant_id=${tenantId}`;
}

const BASE = '/v1/admin/tls-fingerprint-profiles';

export interface TLSProfileResponse {
  profile: TLSFingerprintProfile;
}

export interface TLSProfileDeleteResponse {
  deleted: boolean;
  id: number;
}

// 创建画像。tenant_id 在 body。发请求前先 validate（name 必填）。
export function createTLSProfile(tenantId: number, input: TLSProfileInput): Promise<TLSProfileResponse> {
  const invalid = validateTLSProfileInput(input);
  if (invalid) return Promise.reject(new Error(invalid));
  return apiPost<TLSProfileResponse>(BASE, buildTLSProfileCreateBody(tenantId, input));
}

// 读取单个画像。
export function getTLSProfile(id: number, tenantId: number): Promise<TLSProfileResponse> {
  return apiGet<TLSProfileResponse>(`${BASE}/${id}${tenantQuery(tenantId)}`);
}

// 更新画像（不改状态——状态走 setTLSProfileStatus）。发请求前先 validate。
export function updateTLSProfile(id: number, tenantId: number, input: TLSProfileInput): Promise<TLSProfileResponse> {
  const invalid = validateTLSProfileInput(input);
  if (invalid) return Promise.reject(new Error(invalid));
  return adminPut<TLSProfileResponse>(`${BASE}/${id}${tenantQuery(tenantId)}`, buildTLSProfileUpdateBody(input));
}

// 设置画像状态（active / disabled）。发请求前先校验 status allowlist。
export function setTLSProfileStatus(id: number, tenantId: number, status: string): Promise<TLSProfileResponse> {
  if (!isValidTLSProfileStatus(status)) return Promise.reject(new Error('status 必须是 active 或 disabled'));
  return apiPost<TLSProfileResponse>(`${BASE}/${id}/status${tenantQuery(tenantId)}`, { status });
}

// 删除画像。
export function deleteTLSProfile(id: number, tenantId: number): Promise<TLSProfileDeleteResponse> {
  return adminDelete<TLSProfileDeleteResponse>(`${BASE}/${id}${tenantQuery(tenantId)}`);
}
