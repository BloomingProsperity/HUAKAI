/*
 * 路由绑定(model→pool)前端类型 —— 镜像 modelbindingadminhttp 的 JSON。
 * 端点:/admin/v1/model-pool-bindings(admin 鉴权,session + 租户 scope)。
 */

export interface PoolBinding {
  id: number
  model_id: number
  pool_group_id: number
  priority: number
  weight: number
  /** strict_priority | priority_weighted */
  selection_mode: string
  provider_model_id_override?: string | null
  rpm_limit?: number | null
  tpm_limit?: number | null
  /** normal | context_window | safety | quota | manual */
  fallback_class: string
  enabled: boolean
}

export interface BindingListResponse {
  items: PoolBinding[]
}

/** 创建请求(POST):model_id/pool_group_id 必填,其余可选(后端有默认)。 */
export interface CreateBindingRequest {
  model_id: number
  pool_group_id: number
  priority?: number
  weight?: number
  selection_mode?: string
  fallback_class?: string
}

/** 局部更新(PATCH):只发改了的字段。 */
export interface UpdateBindingRequest {
  priority?: number
  weight?: number
  selection_mode?: string
  fallback_class?: string
  enabled?: boolean
}
