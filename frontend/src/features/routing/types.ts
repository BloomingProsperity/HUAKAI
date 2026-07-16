/*
 * 路由绑定(model→pool)前端类型 —— 镜像 modelbindingadminhttp 的 JSON。
 * 端点:/admin/v1/model-pool-bindings(admin 鉴权,session + 租户 scope)。
 */

export type FallbackClass = 'normal' | 'context_window' | 'safety' | 'quota' | 'manual'

export interface PoolBinding {
  id: number
  model_id: number
  pool_group_id: number
  priority: number
  /** 旧客户端兼容字段；当前界面不展示也不下发。 */
  weight?: number
  /** strict_priority | priority_weighted */
  selection_mode: string
  provider_model_id_override?: string | null
  rpm_limit?: number | null
  tpm_limit?: number | null
  /** 绑定级全局并发上限；null/0 表示不限。 */
  max_parallel_requests?: number | null
  /** 运行时降级类别；兼容旧响应缺值，界面按 normal 主类解释。 */
  fallback_class?: FallbackClass
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
  /** 旧客户端兼容字段；当前界面不下发。 */
  weight?: number
  selection_mode?: string
  /** 绑定级全局并发上限；null/0 表示不限。 */
  max_parallel_requests?: number | null
  /** 运行时降级类别；省略时后端默认 normal。 */
  fallback_class?: FallbackClass
}

/**
 * 更新(PATCH)。后端按整行覆盖，界面必须回填 max_parallel_requests 与其它可空字段。
 */
export interface UpdateBindingRequest {
  priority: number
  selection_mode: string
  enabled: boolean
  /** 旧客户端兼容字段；当前界面不下发。 */
  weight?: number
  /** 绑定级全局并发上限；null/0 表示不限。 */
  max_parallel_requests?: number | null
  /** 运行时降级类别；后端整行覆盖，界面必须回填当前值。 */
  fallback_class: FallbackClass
  // 可空字段也回填当前值,避免被后端清空(null 表示当前即为空)。
  provider_model_id_override?: string | null
  rpm_limit?: number | null
  tpm_limit?: number | null
}

/** 池组内模型→账号子集的 Layer-1 强制 pin。 */
export interface ModelRoutingOverride {
  id: number
  pool_group_id: number
  model: string
  provider_account_ids: number[]
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface RoutingOverrideListResponse {
  items: ModelRoutingOverride[]
}

export interface CreateRoutingOverrideRequest {
  pool_group_id: number
  model: string
  provider_account_ids: number[]
  enabled: boolean
}

/** PATCH 只发送界面明确编辑的账号数组与启停状态。 */
export interface UpdateRoutingOverrideRequest {
  provider_account_ids: number[]
  enabled: boolean
}
