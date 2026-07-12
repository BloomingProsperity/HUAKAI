/*
 * 请求路由规则(routes 表)管理的 DTO 类型。
 * 镜像后端 controlhttp/routeadmin_handler.go 的 routeView / *Request 形态(snake_case)。
 * 后端 file:line:
 *   - routeView          → routeadmin_handler.go:72-84
 *   - createRouteRequest → routeadmin_handler.go:46-53
 *   - updateRouteRequest → routeadmin_handler.go:57-63(不含 tenant_id,租户走 query)
 *   - setRouteEnabledRequest → routeadmin_handler.go:68-70
 */

/** 一条路由规则的管理视图(GET/POST/PUT 响应里的 route 字段)。 */
export interface Route {
  id: number
  tenant_id: number
  name: string
  /** 用户组匹配键(必填,空串后端拒)。 */
  user_group_match: string
  /** 模型模式匹配:''/'*' 全匹配、'prefix*' 前缀、'exact' 精确(详见 validateModelPattern)。 */
  model_pattern_match: string
  /** 目标 pool_group id(必填且 >0)。 */
  pool_group_id: number
  /** 选路优先级(数值,DB 默认 100;列表按此升序展示)。 */
  match_priority: number
  enabled: boolean
  created_at: string
  updated_at: string
}

/** 列表响应:{ routes: [...] }(routeadmin_handler.go:154)。 */
export interface RouteListResponse {
  routes: Route[]
}

/** 单条响应:{ route: {...} }(create/get/update/enabled/delete 均此形)。 */
export interface RouteResponse {
  route: Route
}

/** 新建请求体(POST /v1/admin/routes,tenant_id 在 body)。 */
export interface CreateRouteRequest {
  tenant_id: number
  name: string
  user_group_match: string
  /** 省略=空串=全匹配。 */
  model_pattern_match?: string
  pool_group_id: number
  /** 省略=后端回落 DB 默认 100。 */
  match_priority?: number
}

/**
 * 编辑请求体(PUT /v1/admin/routes/{id}?tenant_id=N)。**不含 tenant_id**——
 * 租户走 query、行由 path id 定位,body 出现 tenant_id 会被后端 DisallowUnknownFields 拒。
 * 全替换语义:match_priority 始终显式带上(read-modify-write),防 read-omit-write 静默重置到 100。
 */
export interface UpdateRouteRequest {
  name: string
  user_group_match: string
  model_pattern_match?: string
  pool_group_id: number
  match_priority: number
}
