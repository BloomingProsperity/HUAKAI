import { apiGet, apiSend } from '../../lib/api'
import type { BroadcastRequest, BroadcastResult, WorkerStats } from './types'

/*
 * 站内信广播 + 通知 worker 统计数据访问层。
 * 两端点均挂在网关根路由(routes_notifications.go:28/41),前缀 /v1/admin/...,
 * tokenForPath 对 /v1/admin/ 前缀自动注入 admin Bearer(tokenForPath.ts:17),
 * 不手动设 token。两端点都需 platform_admin(广播亦放行 tenant_operator)。
 */

/**
 * 群发站内信:POST /v1/admin/notifications/broadcast。
 * 后端 usernoticehttp.adminHandler.broadcast(handlers.go:171);向目标租户全体用户写入站内信,
 * 返回 {object, tenant_id, inserted}(inserted=收信用户数)。createdByAdmin 由后端从鉴权
 * 上下文取(handlers.go:184-187),前端不传。这是改动型动作,调用方负责发送前二次确认。
 */
export async function sendBroadcast(body: BroadcastRequest): Promise<BroadcastResult> {
  return apiSend<BroadcastResult>('POST', '/v1/admin/notifications/broadcast', body)
}

/**
 * 通知 worker 运行统计(只读):GET /v1/admin/notifications/worker-stats。
 * 后端 subscriptionhttp.NewAdminWorkerStatsHandler(admin_worker_stats.go:42);
 * 返回进程内的订阅提醒/到期 worker 计数器(reminder/expiry 各 tick/sent|expired/failed)。
 * 仅 platform_admin 可读(admin_worker_stats.go:57)。计数是进程内累计,重启清零。
 */
export async function getWorkerStats(signal?: AbortSignal): Promise<WorkerStats> {
  return apiGet<WorkerStats>('/v1/admin/notifications/worker-stats', { signal })
}
