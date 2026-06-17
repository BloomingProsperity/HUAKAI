// admin 公告（announcement）CRUD API 封装（管理 token 轨）。
// 端点形状以后端真码 announcementhttp/handlers.go 为准（禁止凭记忆；路由前缀 /v1/admin/announcements，
// 注意是 /v1/admin 而非 /admin/v1）。鉴权 platform_admin 或 tenant_operator；
//   GET    /v1/admin/announcements?tenant_id&limit&offset     → {object,items,limit,offset}
//   POST   /v1/admin/announcements        body 带 tenant_id   → item(201)
//   PUT    /v1/admin/announcements/{id}?tenant_id             → item
//   DELETE /v1/admin/announcements/{id}?tenant_id             → {id,deleted}
// tenant 维度：create 在 **body**（platform_admin 必带、tenant_operator 省略用 scope）；
//   list/update/delete 在 **query**（同上规则）。分页 limit 1-100 默认 50。
//
// 走 client.ts 的 apiGet/apiPost（buildHeaders 注入 huakai_admin_token）+ 本模块自带 adminPut/adminDelete
// （client.ts 未导出 PUT/DELETE 且不可改它）。沿 lib/api/adminChannelTestTemplates.ts 同一助手约定。

import { ApiError, apiGet, apiPost } from './client';
import type { APIError } from './types';
import {
  buildCreateBody,
  buildUpdateBody,
  type AnnouncementFormInput,
} from './announcement-form';

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

// ---- 类型（对齐 announcementResponse）----

export interface Announcement {
  id: number;
  tenant_id: number;
  title: string;
  body: string;
  severity: string;
  active: boolean;
  published_at: string;
  expires_at?: string;
  created_by_admin?: number;
  created_at: string;
  updated_at: string;
}

export interface AnnouncementListResponse {
  object: string;
  items: Announcement[];
  limit: number;
  offset: number;
}

export interface AnnouncementDeleteResponse {
  id: number;
  deleted: boolean;
}

// ---- CRUD ----

// listAnnouncements — GET /v1/admin/announcements。limit 1-100 缺省 50。
export function listAnnouncements(opts: {
  tenant_id: number;
  limit?: number;
  offset?: number;
}): Promise<AnnouncementListResponse> {
  return apiGet<AnnouncementListResponse>('/v1/admin/announcements', {
    tenant_id: opts.tenant_id,
    limit: opts.limit,
    offset: opts.offset,
  });
}

// createAnnouncement — POST /v1/admin/announcements。tenant_id 经 buildCreateBody 放入 body。
export function createAnnouncement(tenantId: number, input: AnnouncementFormInput): Promise<Announcement> {
  return apiPost<Announcement>('/v1/admin/announcements', buildCreateBody(input, tenantId));
}

// updateAnnouncement — PUT /v1/admin/announcements/{id}（全量编辑，tenant 在 query，体不带 tenant_id/id）。
export function updateAnnouncement(id: number, tenantId: number, input: AnnouncementFormInput): Promise<Announcement> {
  return adminPut<Announcement>(`/v1/admin/announcements/${id}${tenantQuery(tenantId)}`, buildUpdateBody(input));
}

// deleteAnnouncement — DELETE /v1/admin/announcements/{id}（tenant 在 query）。
export function deleteAnnouncement(id: number, tenantId: number): Promise<AnnouncementDeleteResponse> {
  return adminDelete<AnnouncementDeleteResponse>(`/v1/admin/announcements/${id}${tenantQuery(tenantId)}`);
}
