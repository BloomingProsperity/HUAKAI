/*
 * 模型注册数据访问层。全部命中 /v1/admin/* 端点 —— api.ts 的 tokenForPath 据前缀自动注入 admin Bearer。
 * 仅做 fetch 编排,业务校验在 modelregistry.ts(纯逻辑)与后端(权威)。
 */
import { apiGet, apiSend } from '../../lib/api'
import type {
  AliasBulkImportRequest,
  AliasBulkImportResponse,
  CapabilitiesRequest,
  CapabilitiesResponse,
  CapabilityBindingsResponse,
  CapabilityBindingUpsertRequest,
  CapabilityBindingUpsertResponse,
  TenantPolicyEnvelope,
  TenantPolicySetRequest,
} from './types'

/** PUT /v1/admin/models/{id}/capabilities —— 整体替换能力矩阵 + 可选 max_output_tokens / model_mode。 */
export async function updateCapabilities(modelId: number, body: CapabilitiesRequest): Promise<CapabilitiesResponse> {
  return apiSend<CapabilitiesResponse>('PUT', `/v1/admin/models/${modelId}/capabilities`, body)
}

/** GET /v1/admin/models/{id}/capability-bindings —— 读 per-scope 能力绑定列表。 */
export async function listCapabilityBindings(modelId: number, signal?: AbortSignal): Promise<CapabilityBindingsResponse> {
  return apiGet<CapabilityBindingsResponse>(`/v1/admin/models/${modelId}/capability-bindings`, { signal })
}

/** PUT /v1/admin/models/{id}/capability-bindings —— upsert 单条白名单能力绑定。source 由服务端强制 operator。 */
export async function upsertCapabilityBinding(
  modelId: number,
  body: CapabilityBindingUpsertRequest,
): Promise<CapabilityBindingUpsertResponse> {
  return apiSend<CapabilityBindingUpsertResponse>('PUT', `/v1/admin/models/${modelId}/capability-bindings`, body)
}

/** POST /v1/admin/models/aliases/bulk-import —— 别名批量导入,回逐行结果。actor 由服务端取认证身份。 */
export async function bulkImportAliases(body: AliasBulkImportRequest): Promise<AliasBulkImportResponse> {
  return apiSend<AliasBulkImportResponse>('POST', '/v1/admin/models/aliases/bulk-import', body)
}

/** GET /v1/admin/model-registry-policy?tenant_id —— 读租户目录继承策略(无行回默认 inherit=false)。 */
export async function getTenantPolicy(tenantId: number, signal?: AbortSignal): Promise<TenantPolicyEnvelope> {
  return apiGet<TenantPolicyEnvelope>('/v1/admin/model-registry-policy', { query: { tenant_id: tenantId }, signal })
}

/** PUT /v1/admin/model-registry-policy?tenant_id —— 翻转 inherit_global_catalog(platform_admin only)。 */
export async function setTenantPolicy(tenantId: number, body: TenantPolicySetRequest): Promise<TenantPolicyEnvelope> {
  return apiSend<TenantPolicyEnvelope>('PUT', '/v1/admin/model-registry-policy', body, { query: { tenant_id: tenantId } })
}
