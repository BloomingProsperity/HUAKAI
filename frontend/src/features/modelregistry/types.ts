/*
 * 模型注册(运维台·admin 壳)前端类型 —— 镜像 controlhttp 模型管理 admin 面的 JSON 契约。
 * 后端端点(均经 adminGate 鉴权,/v1/admin/* 由 api.ts 自动注入 admin Bearer):
 *  - PUT  /v1/admin/models/{id}/capabilities          能力矩阵(任意 key→bool)+ 上限/模式
 *  - POST /v1/admin/models/aliases/bulk-import         别名批量导入(逐行结果)
 *  - GET  /v1/admin/models/{id}/capability-bindings    读 per-scope 能力绑定
 *  - PUT  /v1/admin/models/{id}/capability-bindings    upsert 单条能力绑定(白名单能力)
 *  - GET  /v1/admin/model-registry-policy?tenant_id    读租户目录继承策略
 *  - PUT  /v1/admin/model-registry-policy?tenant_id    翻转 inherit_global_catalog
 *
 * 注:模型用数字 DB id 定位(后端 path {id} 为正 int64);公开 /v1/models 只回字符串别名不回数字 id,
 * 故本页由运维者直接输入数字模型 id(与后端契约一致)。
 */

// ── 能力矩阵 PUT /v1/admin/models/{id}/capabilities ──
// 镜像 capabilitiesRequestBody / capabilitiesResponseBody。
export interface CapabilitiesRequest {
  capabilities: Record<string, boolean>
  max_output_tokens?: number | null
  model_mode?: string | null
}

export interface CapabilitiesResponse {
  object: string
  id: number
  capabilities: Record<string, boolean>
  max_output_tokens?: number | null
  mode?: string
}

// ── 能力绑定 GET/PUT /v1/admin/models/{id}/capability-bindings ──
// 镜像 registry.ModelCapabilityBinding。
export interface CapabilityBinding {
  model_id: number
  tenant_id?: number | null
  scope: string
  capability: string
  capability_value?: string | null
  capability_params?: unknown
  enabled: boolean
  source: string
}

export interface CapabilityBindingsResponse {
  object: string
  model_id: number
  data: CapabilityBinding[]
}

// 镜像 capabilityBindingUpsertRequest —— source 不可由前端设(服务端强制 "operator")。
export interface CapabilityBindingUpsertRequest {
  scope: string
  capability: string
  capability_value?: string
  enabled: boolean
  tenant_id?: number
}

export interface CapabilityBindingUpsertResponse {
  object: string
  binding: CapabilityBinding
}

// ── 别名批量导入 POST /v1/admin/models/aliases/bulk-import ──
// 镜像 registry.ModelAliasImport / BulkImportModelAliasesParams。actor 不由前端设(服务端取认证身份)。
export interface AliasImportRow {
  model_id: number
  alias: string
  scope?: string
  tenant_id?: number
  display?: string
  status?: string
}

export interface AliasBulkImportRequest {
  aliases: AliasImportRow[]
  reason?: string
}

// 镜像 registry.ModelAliasImportResult。
export interface AliasImportResult {
  index: number
  alias: string
  model_id?: number
  status: string
  error?: string
}

export interface AliasBulkImportResponse {
  object: string
  results: AliasImportResult[]
}

// ── 租户目录继承策略 GET/PUT /v1/admin/model-registry-policy ──
// 镜像 tenantPolicyView。
export interface TenantPolicyView {
  tenant_id: number
  inherit_global_catalog: boolean
  updated_at?: string
  updated_by_actor?: string
}

export interface TenantPolicyEnvelope {
  policy: TenantPolicyView
}

export interface TenantPolicySetRequest {
  inherit_global_catalog: boolean
}
