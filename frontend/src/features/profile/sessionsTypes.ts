/*
 * 活跃会话(登录设备族)前端类型 —— 镜像后端 usersession.SessionFamily JSON 形态。
 * 端点(真码):
 *   - 列出 POST /v1/sessions/list    backend/internal/gatewayhttp/session_handler.go:47(MountSessionProtectedRoutes)
 *           → 响应 {families: SessionFamily[]}(session_handler.go:181)
 *   - 撤销 POST /v1/sessions/revoke  session_handler.go:46 → 按 family_id 撤销一族(归属后端校验)
 * 路由挂载于 backend/cmd/gateway/routes.go:281(SessionMiddleware 组,session 鉴权)。
 * SessionFamily 字段见 backend/internal/usersession/types.go:88。
 */

/** 一个登录设备族(SessionFamily)。一族=一次登录建立的会话谱系(刷新令牌轮换在族内)。 */
export interface SessionFamily {
  id: string
  user_id: number
  tenant_id: number
  /** active / revoked / expired / suspicious / replaced(usersession FamilyStatus)。 */
  status: string
  generation: number
  created_at: string
  last_active_at: string
  device_info?: Record<string, unknown> | null
  /** 该族建立时的基线 IP(异常检测用)。 */
  ip_baseline?: string
  revoked_at?: string | null
  revoked_reason?: string
}

/** POST /v1/sessions/list 响应。 */
export interface SessionListResponse {
  families: SessionFamily[]
}

/** POST /v1/sessions/revoke 响应({revoked: 撤销条数})。 */
export interface SessionRevokeResponse {
  revoked: number
}
