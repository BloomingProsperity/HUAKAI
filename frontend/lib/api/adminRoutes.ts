// 订阅档→pool_group 路由规则(routes 表)admin 数据层(管理 token 轨)。零覆盖面接线。
// 后端 controlhttp.MountRouteAdminRoutes(挂 /v1/admin/routes, platform_admin only):
//   POST   /            body 带 tenant_id                         → {route}(201)
//   GET    /?tenant_id                                            → {routes}
//   GET    /{id}?tenant_id                                        → {route}
//   PUT    /{id}?tenant_id  body 无 tenant_id, 全替换可编辑字段    → {route}
//   DELETE /{id}?tenant_id                                        → {route}(删前快照)
// tenant: create 在 **body**; get/update/delete 在 **query**(不可经 body 改, 防跨租户搬移)。
// 走 client.ts apiGet/apiPost + 本模块自带 adminPut/adminDelete(client.ts 未导出 PUT/DELETE);
// 沿 adminTLSProfiles.ts/adminAnnouncements.ts 助手约定。

import { ApiError, apiGet, apiPost } from './client';
import type { APIError } from './types';
import {
  buildCreateRouteBody,
  buildUpdateRouteBody,
  validateRouteInput,
  validateRouteUpdateInput,
  type RouteInput,
  type RouteUpdateInput,
} from './routes-form';

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

const BASE = '/v1/admin/routes';

// 读 DTO(routeView 逐字段)。
export interface Route {
  id: number;
  tenant_id: number;
  name: string;
  user_group_match: string;
  model_pattern_match: string;
  pool_group_id: number;
  match_priority: number;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface RouteResponse {
  route: Route;
}

export interface RouteListResponse {
  routes: Route[];
}

// 创建一条 route。tenant_id 在 body。发请求前先 validate(必填 + model_pattern 形态)。
export function createRoute(tenantId: number, input: RouteInput): Promise<RouteResponse> {
  const invalid = validateRouteInput(input);
  if (invalid) return Promise.reject(new Error(invalid));
  return apiPost<RouteResponse>(BASE, buildCreateRouteBody(tenantId, input));
}

// 列出该租户全部未软删 route。
export function listRoutes(tenantId: number): Promise<RouteListResponse> {
  return apiGet<RouteListResponse>(BASE, { tenant_id: tenantId });
}

// 读取单条 route。
export function getRoute(id: number, tenantId: number): Promise<RouteResponse> {
  return apiGet<RouteResponse>(`${BASE}/${id}${tenantQuery(tenantId)}`);
}

// 更新(全替换)一条 route。tenant 走 query(不入 body); match_priority 必填(防静默重置)。发请求前先 validate。
export function updateRoute(id: number, tenantId: number, input: RouteUpdateInput): Promise<RouteResponse> {
  const invalid = validateRouteUpdateInput(input);
  if (invalid) return Promise.reject(new Error(invalid));
  return adminPut<RouteResponse>(`${BASE}/${id}${tenantQuery(tenantId)}`, buildUpdateRouteBody(input));
}

// 软删一条 route(返回删前快照)。
export function deleteRoute(id: number, tenantId: number): Promise<RouteResponse> {
  return adminDelete<RouteResponse>(`${BASE}/${id}${tenantQuery(tenantId)}`);
}
