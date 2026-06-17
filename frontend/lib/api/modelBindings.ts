// 模型→pool 绑定（model_pool_bindings）admin 客户端 —— 走管理 token
// （localStorage['huakai_admin_token'] 的 Bearer）。对应后端顶层资源
// /admin/v1/model-pool-bindings（modelbindingadminhttp，双角色门）。
// 字段逐一对齐后端 bindingResponse / create/update 请求体。
//
// 借鉴（CLEAN-ROOM，CLAUDE.md §11/§12，仅功能/字段形态，未抄码；真 sha 当前 HEAD）：
//   - sub2api@e34ad2b backend/internal/server/routes/admin.go:610-621 顶层 /admin/channels CRUD、
//     绑定/定价内嵌渠道 payload；new-api@1ac0f58 router/api-router.go:228-266 顶层 /channel CRUD、
//     model→group 经 Channel.Group。两家均"顶层资源"，故 HUAKAI 也做顶层独立 binding CRUD。
//   - HUAKAI delta：per-binding priority/weight/selection_mode/rpm-tpm/max_parallel/fallback_class/
//     生效窗（两家皆无），定价刻意分离。CLIProxyAPI 无等价（纯中继）。

import { apiGet, apiPost, apiPatch, ApiError } from './client';
import type { APIError } from './types';

// ── 枚举（对齐后端 CHECK 约束） ─────────────────────────────────────
export type SelectionMode = 'strict_priority' | 'priority_weighted';
export type FallbackClass = 'normal' | 'context_window' | 'safety' | 'quota' | 'manual';

// ── 读 DTO（bindingResponse 逐字段） ────────────────────────────────
export interface ModelPoolBinding {
  id: number;
  model_id: number;
  pool_group_id: number;
  priority: number;
  weight: number;
  selection_mode: SelectionMode;
  provider_model_id_override: string | null;
  rpm_limit: number | null;
  tpm_limit: number | null;
  max_parallel_requests: number | null;
  fallback_class: FallbackClass;
  enabled: boolean;
  disabled_reason: string | null;
  effective_from: string | null;
  effective_until: string | null;
  reason: string;
  created_at: string;
  updated_at: string;
}

export interface ModelPoolBindingList {
  items: ModelPoolBinding[];
}

// ── 写请求体 ────────────────────────────────────────────────────────
// 创建：model_id + pool_group_id 必填（定义绑定身份）；其余可选，后端有默认值。
export interface CreateBindingInput {
  model_id: number;
  pool_group_id: number;
  priority?: number;
  weight?: number;
  selection_mode?: SelectionMode;
  provider_model_id_override?: string;
  rpm_limit?: number;
  tpm_limit?: number;
  max_parallel_requests?: number;
  fallback_class?: FallbackClass;
  enabled?: boolean;
  disabled_reason?: string;
  effective_from?: string;
  effective_until?: string;
  reason?: string;
}

// 更新：不含 model_id/pool_group_id（身份不可变，改身份走删+建）。
export type UpdateBindingInput = Omit<CreateBindingInput, 'model_id' | 'pool_group_id'>;

const BASE = '/admin/v1/model-pool-bindings';

// 拼 ?tenant_id（platform_admin 必带；tenant_operator 省略用自身 scope）。
function tenantQuery(tenantId?: number): string {
  return tenantId != null ? `?tenant_id=${tenantId}` : '';
}

// client.ts 只导出 GET/POST/PATCH，没有 DELETE 助手；本地补一个（管理 token，204 无体）。
function adminToken(): string {
  if (typeof window === 'undefined') return '';
  return localStorage.getItem('huakai_admin_token') ?? '';
}

async function adminDelete(path: string): Promise<void> {
  const token = adminToken();
  const resp = await fetch(path, {
    method: 'DELETE',
    headers: token ? { Authorization: `Bearer ${token}` } : {},
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

// ── 端点 ────────────────────────────────────────────────────────────

// listBindings — GET /admin/v1/model-pool-bindings?tenant_id=&model_id=&pool_group_id=
export function listBindings(opts: {
  tenant_id?: number;
  model_id?: number;
  pool_group_id?: number;
}): Promise<ModelPoolBindingList> {
  return apiGet<ModelPoolBindingList>(BASE, {
    tenant_id: opts.tenant_id,
    model_id: opts.model_id,
    pool_group_id: opts.pool_group_id,
  });
}

// createBinding — POST /admin/v1/model-pool-bindings?tenant_id
export function createBinding(input: CreateBindingInput, tenantId?: number): Promise<ModelPoolBinding> {
  return apiPost<ModelPoolBinding>(`${BASE}${tenantQuery(tenantId)}`, input);
}

// updateBinding — PATCH /admin/v1/model-pool-bindings/{id}?tenant_id
export function updateBinding(id: number, input: UpdateBindingInput, tenantId?: number): Promise<ModelPoolBinding> {
  return apiPatch<ModelPoolBinding>(`${BASE}/${id}${tenantQuery(tenantId)}`, input);
}

// deleteBinding — DELETE /admin/v1/model-pool-bindings/{id}?tenant_id（204）
export function deleteBinding(id: number, tenantId?: number): Promise<void> {
  return adminDelete(`${BASE}/${id}${tenantQuery(tenantId)}`);
}

// ── 展示辅助（纯前端） ──────────────────────────────────────────────

export const SELECTION_MODE_LABEL: Record<SelectionMode, string> = {
  strict_priority: '严格优先级',
  priority_weighted: '优先级+加权',
};

export const FALLBACK_CLASS_LABEL: Record<FallbackClass, string> = {
  normal: '常规',
  context_window: '上下文超限',
  safety: '安全',
  quota: '配额',
  manual: '手动',
};

export function fmtDateTime(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false });
}
