// admin 告警静默（alert-silence）CRUD API 封装（管理 token 轨）。
// 端点形状以后端真码 alertinghttp/silence_handlers.go + mount.go 为准（禁止凭记忆；前缀 /v1/admin/alert-silences）。
// 鉴权 platform_admin 或 tenant_operator；platform_admin 必带 tenant_id，tenant_operator 用 scope。
//   GET    /v1/admin/alert-silences?tenant_id&limit&offset   → {object,items,limit,offset}
//   POST   /v1/admin/alert-silences   body 带 tenant_id      → item(201)
//   DELETE /v1/admin/alert-silences/{id}?tenant_id           → 204 No Content
// tenant 维度：create 在 **body**；list/delete 在 **query**。分页 limit 1-500 默认 50。
//
// 走 client.ts 的 apiGet/apiPost（buildHeaders 注入 huakai_admin_token）+ 本模块自带 adminDelete
// （client.ts 未导出 DELETE，且不可改它；delete 返回 204 无体）。沿 lib/api/adminAnnouncements.ts 助手约定。

import { ApiError, apiGet, apiPost } from './client';
import type { APIError } from './types';
import { buildSilenceBody, type AlertSilenceFormInput } from './alert-silence-form';

// ---- 共享：管理 token + DELETE ----

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

// adminDelete：DELETE 返回 204 无体 → void；非 2xx 解析错误体抛 ApiError（保 friendlyMessage 可译）。
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

// ---- 类型（对齐 silenceResponse）----

export interface AlertSilence {
  id: number;
  tenant_id: number;
  rule_id?: number;
  reason: string;
  starts_at: string;
  ends_at: string;
  platform?: string;
  group_id?: string;
  region?: string;
  created_at: string;
}

export interface AlertSilenceListResponse {
  object: string;
  items: AlertSilence[];
  limit: number;
  offset: number;
}

// ---- CRUD ----

// listAlertSilences — GET /v1/admin/alert-silences。limit 1-500 缺省 50。
export function listAlertSilences(opts: {
  tenant_id: number;
  limit?: number;
  offset?: number;
}): Promise<AlertSilenceListResponse> {
  return apiGet<AlertSilenceListResponse>('/v1/admin/alert-silences', {
    tenant_id: opts.tenant_id,
    limit: opts.limit,
    offset: opts.offset,
  });
}

// createAlertSilence — POST /v1/admin/alert-silences。tenant_id 经 buildSilenceBody 放入 body。
export function createAlertSilence(tenantId: number, input: AlertSilenceFormInput): Promise<AlertSilence> {
  return apiPost<AlertSilence>('/v1/admin/alert-silences', buildSilenceBody(input, tenantId));
}

// deleteAlertSilence — DELETE /v1/admin/alert-silences/{id}（tenant 在 query）。
export function deleteAlertSilence(id: number, tenantId: number): Promise<void> {
  return adminDelete(`/v1/admin/alert-silences/${id}${tenantQuery(tenantId)}`);
}
