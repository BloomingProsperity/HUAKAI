/*
 * 分组管理(池组 / pool_group)运维台前端类型 —— 镜像后端 dbbilling.PoolGroup 的 JSON
 * (internal/db/billing/models.go:23)与 admin_pools_handler.go 的请求体。
 *
 * 端点(admin token 鉴权,/admin/ 前缀由 tokenForPath 自动注入 admin token):
 *   - GET    /admin/v1/pools?tenant_id&limit         列表 {items: PoolGroup[]}
 *   - POST   /admin/v1/pools                          新建
 *   - GET    /admin/v1/pools/{id}?tenant_id           详情
 *   - PATCH  /admin/v1/pools/{id}                     编辑(含 enabled 启停)
 *
 * 注:后端无 DELETE 端点,池组"删除"以 PATCH enabled=false(禁用)表达;详见 groups.ts。
 * 注:pool_groups schema 无 description / tags 列(handler 接受 description 但不落库),
 *     故本类型与表单都不暴露这两个字段,避免写无消费的死字段。
 */

/** 池组实体 —— 直接镜像 admin_pools_handler 的响应(writeAuditJSON(w, pool)=整个 PoolGroup)。 */
export interface PoolGroup {
  id: number
  tenant_id: number
  name: string
  routing_policy_version: string
  top_k_default: number
  capability_default: string
  allow_tenant_operator_force: boolean
  allow_last_resort: boolean
  sticky_wait_max_waiting: number
  fallback_wait_max_waiting: number
  sticky_wait_timeout_ms: number
  fallback_wait_timeout_ms: number
  forced_route_rate_limit_per_hour: number
  enabled: boolean
  created_at: string
  updated_at: string
}

/** 列表响应:list handler 返回 {items: [...]}(无分页元数据)。 */
export interface PoolListResponse {
  items: PoolGroup[]
}

/** 新建请求体 —— 镜像 adminPoolCreateRequest(仅落库字段)。 */
export interface CreatePoolRequest {
  name: string
  top_k_default?: number
  capability_default?: string
  allow_last_resort?: boolean
  tenant_id?: number
}

/** 编辑请求体 —— 镜像 adminPoolUpdateRequest(仅落库字段;PATCH 语义=present 才改)。 */
export interface UpdatePoolRequest {
  name?: string
  top_k_default?: number
  capability_default?: string
  allow_last_resort?: boolean
  enabled?: boolean
}

/*
 * 成员账号(provider account)只读视图 —— 经 GET /admin/v1/provider-accounts?pool_group_id=
 * 列出某池组下的成员账号(admin_pool_accounts_handler 支持 pool_group_id 过滤)。
 * 本页只读展示成员,不在此新增/解绑(那是账号中心的职责)。仅取展示所需字段。
 */
export interface PoolMemberAccount {
  id: number
  name: string
  account_type: string
  enabled: boolean
  health_state: string
}

export interface PoolMemberListResponse {
  items: PoolMemberAccount[]
}
