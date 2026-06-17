// admin 告警规则（alert-rule）CRUD API 封装（管理 token 轨）。
// 端点形状以后端真码 alertinghttp/rule_handlers.go + mount.go 为准（禁止凭记忆；前缀 /v1/admin/alert-rules）。
// 鉴权 platform_admin 或 tenant_operator；platform_admin 必带 tenant_id，tenant_operator 用 scope。
//   GET    /v1/admin/alert-rules?tenant_id&limit&offset   → {object,items,limit,offset}
//   POST   /v1/admin/alert-rules   body 带 tenant_id      → item(201)
//   GET    /v1/admin/alert-rules/{id}?tenant_id           → item
//   PUT    /v1/admin/alert-rules/{id}?tenant_id           → item
//   DELETE /v1/admin/alert-rules/{id}?tenant_id           → 204 No Content
// tenant：create 在 **body**；list/get/update/delete 在 **query**。分页 limit 1-500 默认 50。
//
// 走 client.ts 的 apiGet/apiPost + 本模块自带 adminPut/adminDelete（client.ts 未导出 PUT/DELETE）。
// 沿 lib/api/adminAlertSilences.ts / adminAnnouncements.ts 助手约定。注：adminOps.ts 另有一个【只读且未被页面引用】
// 的 listAlertRules（dead code），本模块为收口 alerting 面的专属写 CRUD 客户端。

import { ApiError, apiGet, apiPost } from './client';
import type { APIError } from './types';
import { buildCreateBody, buildUpdateBody, type AlertRuleFormInput } from './alert-rule-form';

// ---- 共享：管理 token + PUT/DELETE ----

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

async function adminPut<T>(path: string, body: unknown): Promise<T> {
  const resp = await fetch(path, { method: 'PUT', headers: adminHeaders(), body: JSON.stringify(body) });
  if (resp.ok) return (await resp.json()) as T;
  let payload: APIError;
  try {
    payload = (await resp.json()) as APIError;
  } catch {
    throw new Error(`HTTP ${resp.status}`);
  }
  throw new ApiError(resp.status, payload);
}

// adminDelete：DELETE 返回 204 无体 → void。
async function adminDelete(path: string): Promise<void> {
  const resp = await fetch(path, { method: 'DELETE', headers: adminHeaders() });
  if (resp.ok) return;
  let payload: APIError;
  try {
    payload = (await resp.json()) as APIError;
  } catch {
    throw new Error(`HTTP ${resp.status}`);
  }
  throw new ApiError(resp.status, payload);
}

// 拼 ?tenant_id（platform_admin 必带；tenant_operator 省略用自身 scope）。
function tenantQuery(tenantId?: number): string {
  return tenantId != null ? `?tenant_id=${tenantId}` : '';
}

// ---- 类型（对齐 ruleResponse）----

export interface AlertRule {
  id: number;
  tenant_id: number;
  name: string;
  metric: string;
  metric_type?: string;
  comparator: string;
  threshold: number;
  severity: string;
  window_seconds: number;
  sustained_seconds: number;
  cooldown_seconds: number;
  notify_email: boolean;
  filters?: Record<string, string>;
  last_triggered_at?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface AlertRuleListResponse {
  object: string;
  items: AlertRule[];
  limit: number;
  offset: number;
}

// ---- CRUD ----

// listAlertRules — GET /v1/admin/alert-rules。limit 1-500 缺省 50。
export function listAlertRules(opts: {
  tenant_id: number;
  limit?: number;
  offset?: number;
}): Promise<AlertRuleListResponse> {
  return apiGet<AlertRuleListResponse>('/v1/admin/alert-rules', {
    tenant_id: opts.tenant_id,
    limit: opts.limit,
    offset: opts.offset,
  });
}

// getAlertRule — GET /v1/admin/alert-rules/{id}。
export function getAlertRule(id: number, tenantId: number): Promise<AlertRule> {
  return apiGet<AlertRule>(`/v1/admin/alert-rules/${id}`, { tenant_id: tenantId });
}

// createAlertRule — POST /v1/admin/alert-rules。tenant_id 经 buildCreateBody 放入 body。
export function createAlertRule(tenantId: number, input: AlertRuleFormInput): Promise<AlertRule> {
  return apiPost<AlertRule>('/v1/admin/alert-rules', buildCreateBody(input, tenantId));
}

// updateAlertRule — PUT /v1/admin/alert-rules/{id}（全量编辑，tenant 在 query，体不带 tenant_id/id）。
export function updateAlertRule(id: number, tenantId: number, input: AlertRuleFormInput): Promise<AlertRule> {
  return adminPut<AlertRule>(`/v1/admin/alert-rules/${id}${tenantQuery(tenantId)}`, buildUpdateBody(input));
}

// deleteAlertRule — DELETE /v1/admin/alert-rules/{id}（tenant 在 query）。
export function deleteAlertRule(id: number, tenantId: number): Promise<void> {
  return adminDelete(`/v1/admin/alert-rules/${id}${tenantQuery(tenantId)}`);
}
