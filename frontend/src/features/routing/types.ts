/*
 * 路由绑定(model→pool)前端类型 —— 镜像 modelbindingadminhttp 的 JSON。
 * 端点:/admin/v1/model-pool-bindings(admin 鉴权,session + 租户 scope)。
 */

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
  /** 旧客户端兼容字段；当前界面不展示也不下发。 */
  max_parallel_requests?: number | null
  /** 旧客户端兼容字段；当前界面不展示也不下发。 */
  fallback_class?: string
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
  /** 旧客户端兼容字段；当前界面不下发。 */
  max_parallel_requests?: number | null
  /** 旧客户端兼容字段；当前界面不下发。 */
  fallback_class?: string
}

/**
 * 更新(PATCH)。后端仍接受旧客户端携带的三个兼容字段；当前界面只回填仍有运行时消费的
 * 字段与既有可空字段，不下发仅存储字段。
 */
export interface UpdateBindingRequest {
  priority: number
  selection_mode: string
  enabled: boolean
  /** 旧客户端兼容字段；当前界面不下发。 */
  weight?: number
  /** 旧客户端兼容字段；当前界面不下发。 */
  max_parallel_requests?: number | null
  /** 旧客户端兼容字段；当前界面不下发。 */
  fallback_class?: string
  // 可空字段也回填当前值,避免被后端清空(null 表示当前即为空)。
  provider_model_id_override?: string | null
  rpm_limit?: number | null
  tpm_limit?: number | null
}
