// admin 渠道测试模板 CRUD API 封装（管理 token 轨）—— 可复用的上游渠道连通性测试请求定义。
// 走 client.ts 的 apiGet/apiPost + 本模块内自带的 adminPut/adminDelete（client.ts 未导出 PUT/DELETE 助手，
// 且按硬约束不可改它，故复用同一「localStorage huakai_admin_token → Bearer」约定 + 复用 client.ts 导出的
// ApiError，使 errors.ts friendlyMessage 仍可统一翻译）。沿 lib/api/adminCredentials.ts 同一助手约定。
//
// 端点形状以后端真码 channel_test_template_handler.go 为准（路由前缀 /admin/v1/channel-test-templates，
// 用户管理 admin 轨）。鉴权 resolveChannelTestTemplateAdmin：platform_admin 或 tenant_operator；
// parseAdminCatalogTenant：platform_admin 必带 ?tenant_id，tenant_operator 用 scope。分页 limit 1-500 默认 50。
//   GET    /admin/v1/channel-test-templates?tenant_id&limit&offset  → {object,items,limit,offset}
//   POST   /admin/v1/channel-test-templates?tenant_id               → item(201)
//   GET    /admin/v1/channel-test-templates/{id}?tenant_id          → item
//   PUT    /admin/v1/channel-test-templates/{id}?tenant_id          → item
//   DELETE /admin/v1/channel-test-templates/{id}?tenant_id          → {object,id,deleted}
//
// 借鉴（CLEAN-ROOM，§11/§12/§16，仅功能/字段/动作形态，未抄码）：sub2api 有可复用渠道监控模板 CRUD（tiebreaker，
// 但头部黑名单挡 HTTP 层头非凭证头）；new-api 无模板（硬编码探测）；CLIProxyAPI 无等价物。
// HUAKAI delta：通用 HTTP 请求形模板 + 凭证头拒绝（防密钥写入测试配置）。

import { ApiError, apiGet, apiPost } from './client';
import type { APIError } from './types';
import { buildChannelTestTemplateBody, type ChannelTestTemplateFormInput } from './channel-test-template-form';

// ---- 共享：管理 token + PUT/DELETE（client.ts 未提供这两个动词，且不可改它）----

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

// ---- 类型（对齐 channelTestTemplateItem）----

export interface ChannelTestTemplate {
  id: number;
  tenant_id: number;
  name: string;
  method: string;
  path: string;
  body_template: string;
  headers: Record<string, unknown>; // 后端 json.RawMessage（JSON 对象）
  created_at: string;
}

export interface ChannelTestTemplateListResponse {
  object: string;
  items: ChannelTestTemplate[];
  limit: number;
  offset: number;
}

export interface ChannelTestTemplateDeleteResponse {
  object: string;
  id: number;
  deleted: boolean;
}

// ---- CRUD ----

// listChannelTestTemplates — GET /admin/v1/channel-test-templates。limit 1-500 缺省 50。
export function listChannelTestTemplates(opts: {
  tenant_id: number;
  limit?: number;
  offset?: number;
}): Promise<ChannelTestTemplateListResponse> {
  return apiGet<ChannelTestTemplateListResponse>('/admin/v1/channel-test-templates', {
    tenant_id: opts.tenant_id,
    limit: opts.limit,
    offset: opts.offset,
  });
}

// getChannelTestTemplate — GET /admin/v1/channel-test-templates/{id}。
export function getChannelTestTemplate(id: number, tenantId: number): Promise<ChannelTestTemplate> {
  return apiGet<ChannelTestTemplate>(`/admin/v1/channel-test-templates/${id}`, { tenant_id: tenantId });
}

// createChannelTestTemplate — POST /admin/v1/channel-test-templates。请求体经 buildChannelTestTemplateBody 构造。
export function createChannelTestTemplate(tenantId: number, input: ChannelTestTemplateFormInput): Promise<ChannelTestTemplate> {
  return apiPost<ChannelTestTemplate>(`/admin/v1/channel-test-templates${tenantQuery(tenantId)}`, buildChannelTestTemplateBody(input));
}

// updateChannelTestTemplate — PUT /admin/v1/channel-test-templates/{id}（全量更新，复用同一请求体构造）。
export function updateChannelTestTemplate(id: number, tenantId: number, input: ChannelTestTemplateFormInput): Promise<ChannelTestTemplate> {
  return adminPut<ChannelTestTemplate>(`/admin/v1/channel-test-templates/${id}${tenantQuery(tenantId)}`, buildChannelTestTemplateBody(input));
}

// deleteChannelTestTemplate — DELETE /admin/v1/channel-test-templates/{id}。
export function deleteChannelTestTemplate(id: number, tenantId: number): Promise<ChannelTestTemplateDeleteResponse> {
  return adminDelete<ChannelTestTemplateDeleteResponse>(`/admin/v1/channel-test-templates/${id}${tenantQuery(tenantId)}`);
}
