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

/**
 * 更新(PATCH)。**重要**:后端把 PATCH 当整行覆盖,省略字段会被重置成硬编码默认值
 * (priority→100/weight→1/selection_mode→strict_priority/fallback_class→normal/enabled→true,
 * 且 provider_model_id_override/rpm_limit/tpm_limit 等省略即清空)。因此前端必须回填【当前全部
 * 已知字段】再带上改动,不能只发 diff,否则单字段编辑会静默抹掉其它字段(数据损坏)。
 */
export interface UpdateBindingRequest {
  priority: number
  weight: number
  selection_mode: string
  fallback_class: string
  enabled: boolean
  // 可空字段也回填当前值,避免被后端清空(null 表示当前即为空)。
  provider_model_id_override?: string | null
  rpm_limit?: number | null
  tpm_limit?: number | null
}
