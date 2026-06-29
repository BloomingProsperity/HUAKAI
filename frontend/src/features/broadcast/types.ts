/*
 * 站内信广播 + 通知 worker 统计的类型定义。镜像后端两端点的请求/响应 DTO:
 *   - POST /v1/admin/notifications/broadcast(usernoticehttp/handlers.go:45/52)
 *   - GET  /v1/admin/notifications/worker-stats(subscriptionhttp/admin_worker_stats.go:23)
 * 仅声明前端真正使用的字段;后端字段命名(json tag)逐一对齐,避免回显错位。
 */

/** 广播严重级别。镜像后端 usernotice.Severity(types.go:12-14):info/warning/critical。 */
export type Severity = 'info' | 'warning' | 'critical'

/**
 * 广播请求体。镜像 usernoticehttp.broadcastRequest(handlers.go:45):
 *   - tenant_id 可选:platform_admin 必须显式给目标租户;tenant_operator 留空则后端
 *     回落到自身 scope 租户(handlers.go:244-247)。本表单始终带正整数 tenant_id。
 *   - title / body 必填(后端 trim 后非空,service.go:114)。
 *   - severity 可选,留空后端默认 info(service.go:111-112);只接受三枚举值之一。
 * 注:后端用 DisallowUnknownFields(handlers.go:305),故不可下发未声明字段。
 */
export interface BroadcastRequest {
  tenant_id: number
  title: string
  body: string
  severity?: Severity
}

/**
 * 广播响应。镜像 usernoticehttp.broadcastResponse(handlers.go:52):
 * object 固定 "notification_broadcast";tenant_id 为实际落库租户;inserted 为本次写入条数
 * (即收到该站内信的用户数)。
 */
export interface BroadcastResult {
  object: string
  tenant_id: number
  inserted: number
}

/**
 * 提醒 worker 计数。镜像 subscriptionhttp.ReminderWorkerStats(admin_worker_stats.go:29):
 * tick_count=调度轮次;sent_total=累计发出的到期提醒数;failed_ticks=失败轮次。
 */
export interface ReminderWorkerStats {
  tick_count: number
  sent_total: number
  failed_ticks: number
}

/**
 * 到期 worker 计数。镜像 subscriptionhttp.ExpiryWorkerStats(admin_worker_stats.go:35):
 * tick_count=调度轮次;expired_total=累计处理的到期订阅数;failed_ticks=失败轮次。
 */
export interface ExpiryWorkerStats {
  tick_count: number
  expired_total: number
  failed_ticks: number
}

/** worker 统计响应。镜像 subscriptionhttp.WorkerStats(admin_worker_stats.go:23)。 */
export interface WorkerStats {
  reminder: ReminderWorkerStats
  expiry: ExpiryWorkerStats
}
