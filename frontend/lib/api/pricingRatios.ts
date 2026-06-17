// 分组定价倍率(pool_group_pricing_ratios)admin 客户端 —— 走管理 token,对应后端
// /admin/v1/pricing/ratios(pricingcataloghttp)。倍率按 pool_group 维度覆盖,写入后
// 后端会精确失效 ratio resolver 缓存(billing 热路径立即生效)。
// 字段逐一对齐后端 ratioResponseBody / upsert 请求体。
//
// 借鉴(CLEAN-ROOM,CLAUDE.md §11/§12,仅功能/字段形态,真 sha 当前 HEAD):
//   - new-api@1ac0f58 web/.../models/group-ratio-form.tsx 把群组倍率做成 System Settings
//     下的独立页(visual+JSON 双编辑);sub2api@e34ad2b service/user_group_rate.go 是
//     user-within-group 更细粒度(PUT /groups/:id/rate-multipliers)。
//   - HUAKAI delta:pool_group 维度 + public_ratio 可见性开关 + 显式 resolver 缓存失效
//     (两家皆靠配置/DB 直生效,无显式失效)。CLIProxyAPI 无等价(纯中继无计费倍率)。

import { apiGet, ApiError } from './client';
import type { APIError } from './types';

// 单条倍率(ratioResponseBody)。ratio 仅在 public_ratio=true 时后端才返回(omitempty)。
export interface PricingRatio {
  object: string;
  id: number;
  tenant_id: number;
  pool_group_id: number;
  ratio?: string; // decimal 字符串;private 时后端省略
  public_ratio: boolean;
  created_by?: string;
  updated_by?: string;
  created_at?: string;
  updated_at?: string;
}

export interface PricingRatioList {
  object: string;
  items: PricingRatio[];
  limit: number;
  offset: number;
}

// upsert 请求体(PUT /{pool_group_id})。ratio 是 decimal 字符串,后端校验 (0, MAX](默认 100)。
export interface UpsertRatioInput {
  ratio: string;
  public_ratio: boolean;
}

const BASE = '/admin/v1/pricing/ratios';

// 拼 ?tenant_id(platform_admin 必带;tenant_operator 省略用自身 scope)。
function tenantQuery(tenantId?: number): string {
  return tenantId != null ? `?tenant_id=${tenantId}` : '';
}

// client.ts 只导出 GET/POST/PATCH;本地补 PUT/DELETE(管理 token)。
function adminToken(): string {
  if (typeof window === 'undefined') return '';
  return localStorage.getItem('huakai_admin_token') ?? '';
}

async function adminWrite<T>(method: 'PUT' | 'DELETE', path: string, body?: unknown): Promise<T> {
  const token = adminToken();
  const resp = await fetch(path, {
    method,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (resp.status === 204) return undefined as T;
  if (resp.ok) return (await resp.json()) as T;
  let payload: APIError;
  try {
    payload = (await resp.json()) as APIError;
  } catch {
    throw new Error(`HTTP ${resp.status}`);
  }
  throw new ApiError(resp.status, payload);
}

// ── 端点 ────────────────────────────────────────────────────────────

// listRatios — GET /admin/v1/pricing/ratios?tenant_id=
export function listRatios(tenantId?: number): Promise<PricingRatioList> {
  return apiGet<PricingRatioList>(BASE, { tenant_id: tenantId });
}

// upsertRatio — PUT /admin/v1/pricing/ratios/{pool_group_id}?tenant_id
export function upsertRatio(poolGroupId: number, input: UpsertRatioInput, tenantId?: number): Promise<PricingRatio> {
  return adminWrite<PricingRatio>('PUT', `${BASE}/${poolGroupId}${tenantQuery(tenantId)}`, input);
}

// deleteRatio — DELETE /admin/v1/pricing/ratios/{pool_group_id}?tenant_id
export function deleteRatio(poolGroupId: number, tenantId?: number): Promise<void> {
  return adminWrite<void>('DELETE', `${BASE}/${poolGroupId}${tenantQuery(tenantId)}`);
}

// ── 展示辅助 ────────────────────────────────────────────────────────

export function fmtDateTime(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false });
}
