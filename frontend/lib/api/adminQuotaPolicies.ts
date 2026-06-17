// admin 配额策略 CRUD API 封装（管理 token 轨）—— 反滥用「运营配置」，不碰 user_balances / 计费账本。
// 走 client.ts 的 apiGet/apiPost + 本模块内自带的 adminPut/adminDelete（client.ts 未导出 PUT/DELETE 助手，
// 且按硬约束不可改它，故复用同一「localStorage huakai_admin_token → Bearer」约定 + 复用 client.ts 导出的
// ApiError，使 errors.ts friendlyMessage 仍可统一翻译）。沿 lib/api/adminCredentials.ts 同一助手约定。
//
// 端点形状全部以后端真码 adminquotahttp 为准（路由前缀 /admin/v1/...，用户管理 admin 轨；与兑换码/订阅的
// /v1/admin/... 不同）。鉴权 resolveTenantIdentity：platform_admin 必带 ?tenant_id；tenant_operator 用自身 scope。
//   GET    /admin/v1/quota-policies?tenant_id&scope_kind&scope_id&metric&enabled&limit&offset  newListHandler   → {object,items,limit,offset}
//   POST   /admin/v1/quota-policies?tenant_id                                                  newCreateHandler → item(201)
//   GET    /admin/v1/quota-policies/{id}?tenant_id                                             newGetHandler    → item
//   PUT    /admin/v1/quota-policies/{id}?tenant_id                                             newUpdateHandler → item
//   DELETE /admin/v1/quota-policies/{id}?tenant_id                                             newDeleteHandler → {object,id,deleted}
//
// 借鉴（CLEAN-ROOM，§11/§12/§16，仅功能/字段/动作形态，未抄码；融合未抄）：sub2api 有独立配额表但 scope 硬编码、
// 仅 USD、无 observe/priority；new-api 配额内嵌实体、lifetime 无窗口；CLIProxyAPI 无持久配额策略（无等价物）。
// HUAKAI delta：独立通用 policy 对象（6 scope × 4 metric × 5 window × 4 mode + priority + 有效期 + burst）。

import { ApiError, apiGet, apiPost } from './client';
import type { APIError } from './types';
import { buildQuotaPolicyBody, type QuotaPolicyFormInput } from './quota-policy-form';

// ---- 共享：管理 token 取用 + PUT/DELETE（client.ts 未提供这两个动词，且不可改它）----

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

// 拼 ?tenant_id（platform_admin 必带；tenant_operator 省略用自身 scope）。
function tenantQuery(tenantId?: number): string {
  return tenantId != null ? `?tenant_id=${tenantId}` : '';
}

// ---- 类型（对齐 adminquotahttp.quotaPolicyItem；数值上限渲染为十进制字符串以免精度丢失）----

export interface QuotaPolicy {
  id: number;
  tenant_id: number;
  scope_kind: string;
  scope_id: string;
  metric: string;
  window_kind: string;
  window_seconds: number;
  limit_value: string;
  burst_value: string;
  mode: string;
  priority: number;
  enabled: boolean;
  valid_from: string;
  valid_until?: string;
  created_by_actor?: string;
  last_modified_by_actor?: string;
  created_at: string;
  updated_at: string;
}

export interface QuotaPolicyListResponse {
  object: string;
  items: QuotaPolicy[];
  limit: number;
  offset: number;
}

export interface QuotaPolicyDeleteResponse {
  object: string;
  id: number;
  deleted: boolean;
}

// ---- CRUD ----

// listQuotaPolicies — GET /admin/v1/quota-policies。过滤 scope_kind/scope_id/metric/enabled 均可选；limit 缺省50封顶100。
export function listQuotaPolicies(opts: {
  tenant_id: number;
  scope_kind?: string;
  scope_id?: string;
  metric?: string;
  enabled?: boolean;
  limit?: number;
  offset?: number;
}): Promise<QuotaPolicyListResponse> {
  return apiGet<QuotaPolicyListResponse>('/admin/v1/quota-policies', {
    tenant_id: opts.tenant_id,
    scope_kind: opts.scope_kind && opts.scope_kind !== 'all' ? opts.scope_kind : undefined,
    scope_id: opts.scope_id && opts.scope_id.trim() !== '' ? opts.scope_id.trim() : undefined,
    metric: opts.metric && opts.metric !== 'all' ? opts.metric : undefined,
    enabled: opts.enabled,
    limit: opts.limit,
    offset: opts.offset,
  });
}

// getQuotaPolicy — GET /admin/v1/quota-policies/{id}。
export function getQuotaPolicy(id: number, tenantId: number): Promise<QuotaPolicy> {
  return apiGet<QuotaPolicy>(`/admin/v1/quota-policies/${id}`, { tenant_id: tenantId });
}

// createQuotaPolicy — POST /admin/v1/quota-policies。请求体经 buildQuotaPolicyBody 构造（省略语义对齐后端 *指针字段）。
export function createQuotaPolicy(tenantId: number, input: QuotaPolicyFormInput): Promise<QuotaPolicy> {
  return apiPost<QuotaPolicy>(`/admin/v1/quota-policies${tenantQuery(tenantId)}`, buildQuotaPolicyBody(input));
}

// updateQuotaPolicy — PUT /admin/v1/quota-policies/{id}（全量更新，复用同一请求体构造）。
export function updateQuotaPolicy(id: number, tenantId: number, input: QuotaPolicyFormInput): Promise<QuotaPolicy> {
  return adminPut<QuotaPolicy>(`/admin/v1/quota-policies/${id}${tenantQuery(tenantId)}`, buildQuotaPolicyBody(input));
}

// deleteQuotaPolicy — DELETE /admin/v1/quota-policies/{id}。后端可 409 quota_policy_in_use（有 live 窗口不可删）。
export function deleteQuotaPolicy(id: number, tenantId: number): Promise<QuotaPolicyDeleteResponse> {
  return adminDelete<QuotaPolicyDeleteResponse>(`/admin/v1/quota-policies/${id}${tenantQuery(tenantId)}`);
}

// ---- 展示辅助 ----

export function quotaModeLabel(mode: string): string {
  switch (mode) {
    case 'enforce':
      return '强制';
    case 'observe':
      return '观察(仅记录)';
    case 'manual_first':
      return '人工优先';
    case 'disabled':
      return '停用';
    default:
      return mode || '未知';
  }
}

export function quotaModeVariant(mode: string): 'default' | 'secondary' | 'destructive' | 'outline' {
  if (mode === 'enforce') return 'default';
  if (mode === 'disabled') return 'destructive';
  if (mode === 'observe') return 'secondary';
  return 'outline';
}
