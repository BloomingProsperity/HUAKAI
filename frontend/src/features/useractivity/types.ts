/*
 * 用户安全日志(用户门户 · 账户)前端类型 —— 镜像 userauditloghttp 的 JSON DTO。
 * 端点(session 鉴权,身份从会话上下文派生、不读请求体):
 *   GET /v1/me/audit-events?offset=&limit=   列出当前用户自己的审计事件
 * 真码:backend/internal/userauditloghttp/handlers.go:27(MountRoutes,挂在 /v1/me 会话组)、
 *       backend/cmd/gateway/routes.go:192。响应结构 handlers.go:31-44。
 * 分页:limit 默认 50、上限 200(userauditlog/store.go:26);offset>=0。
 */

/** 单条审计事件视图(auditEventView,handlers.go:36)。occurred_at 为 RFC3339Nano 串。 */
export interface UserAuditEvent {
  id: number
  action: string
  outcome: string
  api_key_id?: number | null
  key_prefix?: string
  reason?: string
  request_id?: string
  occurred_at: string
}

/** 列表响应(auditEventsResponse,handlers.go:31)。count = 本页返回条数(非总数)。 */
export interface UserAuditEventsResponse {
  audit_events: UserAuditEvent[]
  count: number
}
