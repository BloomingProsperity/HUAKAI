// 模型能力 / 别名 admin 数据层（管理 token 轨, platform_admin via adminGate）。零覆盖面接线。
// 后端 controlhttp（挂 routes.go:792-800, 全局 model registry, **无 tenant_id query**）:
//   PUT  /v1/admin/models/{id}/capabilities        body{capabilities,max_output_tokens?,model_mode?} → {object,id,capabilities,...}
//   POST /v1/admin/models/aliases/bulk-import       body{aliases:[...],reason?}                      → {object,results:[{index,alias,model_id?,status,error?}]}
//   GET  /v1/admin/models/{id}/capability-bindings                                                   → {object,model_id,data:[binding]}
// 走 client.ts apiGet/apiPost + 本模块自带 adminPut(client.ts 未导出 PUT); 沿 adminTLSProfiles.ts 助手约定。

import { ApiError, apiGet, apiPost } from './client';
import type { APIError } from './types';
import {
  buildAliasBulkBody,
  buildCapabilitiesBody,
  validateAliasBulk,
  validateCapabilitiesInput,
  type AliasRow,
  type CapabilitiesInput,
} from './model-capabilities-form';

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

const BASE = '/v1/admin/models';

// ── 读 DTO ──────────────────────────────────────────────────────────────
export interface ModelCapabilitiesResponse {
  object: string;
  id: number;
  capabilities: Record<string, boolean>;
  max_output_tokens?: number;
  mode?: string;
}

export interface ModelAliasImportResult {
  index: number;
  alias: string;
  model_id?: number;
  status: string;
  error?: string;
}

export interface AliasBulkImportResponse {
  object: string;
  results: ModelAliasImportResult[];
}

export interface ModelCapabilityBinding {
  model_id: number;
  tenant_id?: number;
  scope: string;
  capability: string;
  capability_value?: string;
  capability_params?: unknown;
  enabled: boolean;
  source: string;
}

export interface CapabilityBindingsResponse {
  object: string;
  model_id: number;
  data: ModelCapabilityBinding[];
}

// ── 端点 ────────────────────────────────────────────────────────────────

// 设置一个模型的能力（全替换 capabilities map + 可选 max_output_tokens/model_mode）。发请求前先 validate。
export function updateModelCapabilities(modelId: number, input: CapabilitiesInput): Promise<ModelCapabilitiesResponse> {
  const invalid = validateCapabilitiesInput(input);
  if (invalid) return Promise.reject(new Error(invalid));
  return adminPut<ModelCapabilitiesResponse>(`${BASE}/${modelId}/capabilities`, buildCapabilitiesBody(input));
}

// 批量导入模型别名（逐行部分成功, 返回 per-row results）。发请求前先 validate(≥1 行 + 每行形态)。
// 注意: 后端【总是返回 HTTP 200】, 单行失败体现在 results[].status==='failed' + results[].error。
// 调用方【必须】逐行检查 results(如 res.results.some(r => r.status === 'failed')), 不可把 2xx 当全部成功。
export function bulkImportModelAliases(rows: AliasRow[], reason?: string): Promise<AliasBulkImportResponse> {
  const invalid = validateAliasBulk(rows);
  if (invalid) return Promise.reject(new Error(invalid));
  return apiPost<AliasBulkImportResponse>(`${BASE}/aliases/bulk-import`, buildAliasBulkBody(rows, reason));
}

// 读一个模型的能力绑定（只读）。
export function getModelCapabilityBindings(modelId: number): Promise<CapabilityBindingsResponse> {
  return apiGet<CapabilityBindingsResponse>(`${BASE}/${modelId}/capability-bindings`);
}
