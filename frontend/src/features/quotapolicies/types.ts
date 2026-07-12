/*
 * 配额策略(防滥用限流)运营台前端类型 —— 镜像后端 internal/adminquotahttp 的 JSON 形态。
 *
 * 端点(均 admin token 鉴权,挂在 /admin/v1/quota-policies,见 cmd/gateway/routes.go:905-909):
 *   GET    /admin/v1/quota-policies?tenant_id=N&scope_kind=&metric=&enabled=&limit=&offset=
 *                                                          列表+筛选(quota_policy_crud.go:111 newListHandler)
 *   GET    /admin/v1/quota-policies/{id}?tenant_id=N       取单条(quota_policy_crud.go:163 newGetHandler)
 *   POST   /admin/v1/quota-policies                        新建(body 带字段,quota_policy_crud.go:189 newCreateHandler)
 *   PUT    /admin/v1/quota-policies/{id}                   更新(quota_policy_crud.go:222 newUpdateHandler)
 *   DELETE /admin/v1/quota-policies/{id}                   删除(可带 reason body,quota_policy_crud.go:259 newDeleteHandler)
 *
 * 注意:platform_admin 角色下 tenant_id query 必填(routes.go:124 tenantFromQueryOrScope);
 * tenant_operator 省略则用自身作用域。本页所有读写都先要一个租户 ID。
 *
 * 关键:limit_value / burst_value 后端以十进制字符串渲染(quota_policy_crud.go:51-52、itemFromRow:337),
 * 防 JSON 数值精度丢失;前端必须原样字符串渲染/回传,绝不 Number() 化。
 *
 * 本页是防滥用运维配置:绝不触碰 user_balances 或计费账本(routes.go 包注释明示)。
 */

/** scope_kind 枚举(镜像 validScopeKinds,validate.go:21)。 */
export type ScopeKind = 'global' | 'user' | 'api_key' | 'channel' | 'pool_group' | 'provider_account'

/** metric 枚举(镜像 validMetrics,validate.go:25)。 */
export type Metric = 'requests' | 'tokens_estimated' | 'cost_usd' | 'concurrency'

/** window_kind 枚举(镜像 validWindowKinds,validate.go:28)。 */
export type WindowKind = 'none' | 'fixed' | 'calendar_day' | 'calendar_week' | 'manual'

/** mode 枚举(镜像 validModes,validate.go:31)。 */
export type Mode = 'enforce' | 'observe' | 'manual_first' | 'disabled'

/** 单条配额策略 DTO(镜像 quotaPolicyItem,quota_policy_crud.go:43)。 */
export interface QuotaPolicy {
  id: number
  tenant_id: number
  scope_kind: string
  scope_id: string
  metric: string
  window_kind: string
  window_seconds: number
  /** 上限,十进制字符串(原样渲染,防精度丢失)。 */
  limit_value: string
  /** 突发上限,十进制字符串。 */
  burst_value: string
  mode: string
  priority: number
  enabled: boolean
  /** RFC3339 生效起始时间。 */
  valid_from: string
  /** RFC3339 失效时间(可空=永久)。 */
  valid_until?: string | null
  created_by_actor?: string | null
  last_modified_by_actor?: string | null
  created_at: string
  updated_at: string
}

/** 列表响应(object="admin_quota_policies_list",quota_policy_crud.go:64)。 */
export interface QuotaPolicyListResponse {
  object: string
  items: QuotaPolicy[]
  limit: number
  offset: number
}

/** 删除响应(object="admin_quota_policy_deleted",quota_policy_crud.go:71)。 */
export interface QuotaPolicyDeleteResponse {
  object: string
  id: number
  deleted: boolean
}

/**
 * 新建/更新请求体(镜像 quotaPolicyRequest,quota_policy_crud.go:79)。
 * window_seconds/burst_value/priority/enabled/valid_from/valid_until 后端为可选指针,
 * 省略即套默认值;故前端请求体里这些字段也可省略。
 * limit_value/burst_value 为十进制字符串。reason 供审计(可空)。
 */
export interface QuotaPolicyRequest {
  scope_kind: string
  scope_id: string
  metric: string
  window_kind: string
  window_seconds?: number
  limit_value: string
  burst_value?: string
  mode: string
  priority?: number
  enabled?: boolean
  valid_from?: string
  valid_until?: string
  reason?: string
}

/** 列表筛选条件(前端表单态;空串/未选=不过滤,镜像后端 filterValue/enabledFilter)。 */
export interface PolicyFilters {
  /** 作用域类型筛选(空=全部)。 */
  scopeKind: '' | ScopeKind
  /** 指标筛选(空=全部)。 */
  metric: '' | Metric
  /** 启用态筛选('' = 全部 / 'true' / 'false')。 */
  enabled: '' | 'true' | 'false'
}

export const EMPTY_FILTERS: PolicyFilters = {
  scopeKind: '',
  metric: '',
  enabled: '',
}

/**
 * 编辑/新建表单态(全部字符串/布尔,贴近输入控件;提交时经 validatePolicyForm 转请求体)。
 * limit_value/burst_value 保持字符串原样(防精度丢失)。
 */
export interface PolicyForm {
  scopeKind: ScopeKind
  scopeId: string
  metric: Metric
  windowKind: WindowKind
  /** 窗口秒数,字符串输入(fixed 时必须 >0)。 */
  windowSeconds: string
  limitValue: string
  burstValue: string
  mode: Mode
  priority: string
  enabled: boolean
  /** RFC3339 或空串(空=立即生效)。 */
  validFrom: string
  /** RFC3339 或空串(空=永久)。 */
  validUntil: string
  /** 审计原因(可空)。 */
  reason: string
}
