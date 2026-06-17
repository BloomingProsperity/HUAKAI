// 内容审核——黑名单管理 admin 客户端(管理 token 轨,huakai_admin_token)。
// 仅覆盖后端 /admin/v1/moderation 中【尚无前端面】的部分:关键词黑名单 + 哈希黑名单的
// CRUD(list/create/bulk/delete)。补接线审计 #7 的真实剩余缺口。
//
// 边界(避免与 lib/api/adminSystem.ts 重复/类型漂移):审核配置(config)、审核日志(logs)、
// 封禁列表(banned)已由 adminSystem.ts + /admin/system 页实现,本模块【不重复声明】。
// 解封(unban)后端刻意延后(系统页注明"避免误操作"),本切片不纳入。
//
// 字段逐一对齐后端 DTO(moderationhttp.keywordResponse / hashResponse / BulkCreateResult)。
// 鉴权:GET/POST 走 client.ts(已挂 admin Bearer),DELETE 本地补(同样挂 admin token)。

import { apiGet, apiPost, ApiError } from './client';
import type { APIError } from './types';

// 纯解析/校验辅助由零依赖的 moderation-bulk.ts 提供(可独立单测),此处统一 re-export。
export { isValidHashHex, parseBulkLines, fmtDateTime } from './moderation-bulk';

// ── 类型(对齐后端 DTO)──────────────────────────────────────────────

// 关键词黑名单条目(keywordResponse)。reason_code 是审核命中时回写日志的原因码。
export interface ModerationKeyword {
  id: number;
  tenant_id: number;
  keyword: string;
  reason_code: string;
  enabled: boolean;
  created_at?: string;
  updated_at?: string;
}

// 哈希黑名单条目(hashResponse)。hash_hex 是 64 位小写十六进制(payload 的 sha256)。
export interface ModerationHash {
  id: number;
  tenant_id: number;
  hash_hex: string;
  reason_code: string;
  enabled: boolean;
  created_at?: string;
  updated_at?: string;
}

// 列表包装(后端统一 {object, items, limit, offset})。
export interface ModerationList<T> {
  object: string;
  items: T[];
  limit: number;
  offset: number;
}

// 批量导入结果(BulkCreateResult)。errors[].index 指向请求 items 的下标。
export interface BulkCreateResult {
  accepted: number;
  skipped_duplicate: number;
  errors: Array<{ index: number; reason: string }>;
}

// ── 写请求体 ────────────────────────────────────────────────────────

export interface CreateKeywordInput {
  tenant_id: number;
  keyword: string;
  reason_code: string;
  enabled?: boolean;
}

export interface CreateHashInput {
  tenant_id: number;
  hash_hex: string;
  reason_code: string;
  enabled?: boolean;
}

export interface BulkKeywordItem {
  keyword: string;
  reason_code: string;
  enabled?: boolean;
}

export interface BulkHashItem {
  hash_hex: string;
  reason_code: string;
  enabled?: boolean;
}

const BASE = '/admin/v1/moderation';

// 拼 ?tenant_id(platform_admin 必带;tenant_operator 省略用自身 scope)。
function tenantQuery(tenantId?: number): string {
  return tenantId != null ? `?tenant_id=${tenantId}` : '';
}

// client.ts 只导出 GET/POST/PATCH;本地补 DELETE(同样挂 admin token)。
function adminToken(): string {
  if (typeof window === 'undefined') return '';
  return localStorage.getItem('huakai_admin_token') ?? '';
}

async function adminDelete(path: string): Promise<void> {
  const token = adminToken();
  const resp = await fetch(path, {
    method: 'DELETE',
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
  });
  if (resp.status === 204 || resp.ok) return;
  let payload: APIError;
  try {
    payload = (await resp.json()) as APIError;
  } catch {
    throw new Error(`HTTP ${resp.status}`);
  }
  throw new ApiError(resp.status, payload);
}

// ── 关键词黑名单 ────────────────────────────────────────────────────

// GET /keywords?tenant_id&limit&offset
export function listKeywords(tenantId?: number, limit = 200, offset = 0): Promise<ModerationList<ModerationKeyword>> {
  return apiGet<ModerationList<ModerationKeyword>>(`${BASE}/keywords`, { tenant_id: tenantId, limit, offset });
}

// POST /keywords
export function createKeyword(input: CreateKeywordInput): Promise<ModerationKeyword> {
  return apiPost<ModerationKeyword>(`${BASE}/keywords`, input);
}

// POST /keywords/bulk —— items 上限 1000(后端校验),超量后端返回 bulk_import_too_large。
export function bulkCreateKeywords(tenantId: number, items: BulkKeywordItem[]): Promise<BulkCreateResult> {
  return apiPost<BulkCreateResult>(`${BASE}/keywords/bulk`, { tenant_id: tenantId, items });
}

// DELETE /keywords/{id}?tenant_id
export function deleteKeyword(id: number, tenantId?: number): Promise<void> {
  return adminDelete(`${BASE}/keywords/${id}${tenantQuery(tenantId)}`);
}

// ── 哈希黑名单 ──────────────────────────────────────────────────────

// GET /hashes?tenant_id&limit&offset
export function listHashes(tenantId?: number, limit = 200, offset = 0): Promise<ModerationList<ModerationHash>> {
  return apiGet<ModerationList<ModerationHash>>(`${BASE}/hashes`, { tenant_id: tenantId, limit, offset });
}

// POST /hashes
export function createHash(input: CreateHashInput): Promise<ModerationHash> {
  return apiPost<ModerationHash>(`${BASE}/hashes`, input);
}

// POST /hashes/bulk
export function bulkCreateHashes(tenantId: number, items: BulkHashItem[]): Promise<BulkCreateResult> {
  return apiPost<BulkCreateResult>(`${BASE}/hashes/bulk`, { tenant_id: tenantId, items });
}

// DELETE /hashes/{id}?tenant_id
export function deleteHash(id: number, tenantId?: number): Promise<void> {
  return adminDelete(`${BASE}/hashes/${id}${tenantQuery(tenantId)}`);
}

