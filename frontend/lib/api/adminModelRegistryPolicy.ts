// 租户目录继承策略(inherit_global_catalog)admin 数据层(管理 token 轨, platform_admin via adminGate)。零覆盖面接线。
// 后端 controlhttp.NewAdminTenantPolicy{Get,Set}Handler(挂 routes.go, /v1/admin/model-registry-policy):
//   GET ?tenant_id                         → {policy}
//   PUT ?tenant_id  body{inherit_global_catalog} → {policy}
// tenant: 走 **query**(目标租户, 不可经 body 改, 防走私)。走 client.ts apiGet(发 admin token) + 本模块自带 adminPut
// (client.ts 未导出 PUT); 沿 adminRoutes.ts/adminModelCapabilities.ts 助手约定。
import { ApiError, apiGet } from './client';
import type { APIError } from './types';
import { buildTenantPolicyBody, validateSetTenantPolicy } from './model-registry-policy-form';

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

const BASE = '/v1/admin/model-registry-policy';

// 读 DTO(tenantPolicyView 逐字段)。
export interface TenantPolicy {
  tenant_id: number;
  inherit_global_catalog: boolean;
  updated_at?: string;
  updated_by_actor?: string;
}

export interface TenantPolicyResponse {
  policy: TenantPolicy;
}

// 读一个租户的目录继承策略(无策略行后端返默认 inherit=false)。
export function getTenantPolicy(tenantId: number): Promise<TenantPolicyResponse> {
  return apiGet<TenantPolicyResponse>(BASE, { tenant_id: tenantId });
}

// 翻转一个租户的 inherit_global_catalog(tenant 走 query, body 仅含 inherit)。发请求前先 validate。
export function setTenantInheritGlobal(tenantId: number, inherit: boolean): Promise<TenantPolicyResponse> {
  const invalid = validateSetTenantPolicy(tenantId);
  if (invalid) return Promise.reject(new Error(invalid));
  return adminPut<TenantPolicyResponse>(`${BASE}?tenant_id=${tenantId}`, buildTenantPolicyBody(inherit));
}
