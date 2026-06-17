// admin per-user 通知设置数据层（管理 token 轨）。
// 后端真码 controlhttp/notify_handler.go notifyAdminHandler：
//   GET /v1/admin/users/{user_id}/notifications?tenant_id  → notifySettingsResponse
//   PUT /v1/admin/users/{user_id}/notifications?tenant_id  body 9 字段(DisallowUnknownFields) → settings
// user_id 在路径；tenant_id 在 query（platform_admin 必带，tenant_operator 可省用 scope）。
// 走 client.ts apiGet（buildHeaders 注入 huakai_admin_token）+ 本模块自带 adminPut
// （client.ts 未导出 PUT 且不可改它）；沿 adminAnnouncements.ts 同一助手约定。
import { ApiError, apiGet } from './client';
import type { NotifySettings, NotifySettingsRequest } from './notifications';
import type { APIError } from './types';
import { buildNotifySettingsBody, validateNotifySettings } from './notify-settings-form';

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

// tenant_id 仅 platform_admin 必带；tenant_operator 省略用 scope。
function tenantQuery(tenantId?: number): string {
  return tenantId ? `?tenant_id=${tenantId}` : '';
}

// 读取某用户的通知设置（admin）。
export function getAdminUserNotifySettings(userId: number, tenantId?: number): Promise<NotifySettings> {
  return apiGet<NotifySettings>(`/v1/admin/users/${userId}/notifications${tenantQuery(tenantId)}`);
}

// 保存某用户的通知设置（admin）。发请求前先按 notify_type 条件校验 fail-fast。
export function saveAdminUserNotifySettings(
  userId: number,
  body: NotifySettingsRequest,
  tenantId?: number,
): Promise<NotifySettings> {
  const invalid = validateNotifySettings(body);
  if (invalid) return Promise.reject(new Error(invalid));
  return adminPut<NotifySettings>(
    `/v1/admin/users/${userId}/notifications${tenantQuery(tenantId)}`,
    buildNotifySettingsBody(body),
  );
}
