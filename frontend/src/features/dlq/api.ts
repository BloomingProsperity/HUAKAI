import { apiGet, apiSend } from '../../lib/api'
import type {
  DlqListResponse,
  DlqRecord,
  DlqReplayResponse,
  ObsDlqListResponse,
  ObsDlqReplayResponse,
} from './types'

/*
 * 死信队列(DLQ)运营台数据访问层。
 * 三个端点都挂在 /admin/v1(经 tokenForPath 自动带 admin Bearer,平台运维 platform_admin 角色):
 *   - GET  /admin/v1/dlq/{handler}              列某 event_kind 的死信(backend routes.go:1116)
 *   - POST /admin/v1/dlq/{id}/replay            重放一条死信(routes.go:1117)
 *   - POST /admin/v1/usage-record-dlq/{id}/replay  重放一条用量记录死信(routes.go:1118)
 * 后两者共用 NewAdminDLQReplayHandler(admin_dlq_handler.go:66),行为一致,仅路由前缀不同。
 *
 * replay 触发 settle 重入 / 计费恢复(money 敏感):重放走完整 idempotency 路径(claim/usage/
 * billing_event 三证 proof 防重复扣费),即幂等。即便如此,UI 仍对该动作二次确认。
 */

/**
 * 列某 handler(event_kind)的死信记录。GET /admin/v1/dlq/{handler}?status=&limit=。
 * handler 是路径段(usage_record / billing_event_replica / ...);status 空则不筛;
 * limit 范围 1..200(后端校验,见 admin_dlq_handler.go:40-47),默认 100。
 */
export async function listDlq(
  handler: string,
  opts: { status?: string; limit?: number; signal?: AbortSignal } = {},
): Promise<DlqListResponse> {
  const query: Record<string, string | number | undefined> = {}
  if (opts.status) query.status = opts.status
  if (opts.limit !== undefined) query.limit = opts.limit
  return apiGet<DlqListResponse>(`/admin/v1/dlq/${encodeURIComponent(handler)}`, {
    query,
    signal: opts.signal,
  })
}

/**
 * 重放一条死信(money 敏感:触发 settle 重入 / 计费恢复,幂等)。
 * POST /admin/v1/dlq/{id}/replay。id 须正整数(后端校验 id<=0 即 400)。
 */
export async function replayDlq(id: number): Promise<DlqReplayResponse> {
  return apiSend<DlqReplayResponse>('POST', `/admin/v1/dlq/${id}/replay`)
}

/**
 * 重放一条用量记录死信(money 敏感,幂等)。POST /admin/v1/usage-record-dlq/{id}/replay。
 * 与 replayDlq 共用同一后端 handler,仅供按用量记录主键(source_id)重放时的便捷入口。
 */
export async function replayUsageRecordDlq(id: number): Promise<DlqReplayResponse> {
  return apiSend<DlqReplayResponse>('POST', `/admin/v1/usage-record-dlq/${id}/replay`)
}

/** 列出观测 outbox 死信；tenant 查询参数名由后端固定为 tenant。 */
export async function listObsDlq(
  opts: {
    tenantId?: number
    eventType?: string
    from?: string
    to?: string
    limit?: number
    signal?: AbortSignal
  } = {},
): Promise<ObsDlqListResponse> {
  return apiGet<ObsDlqListResponse>('/admin/v1/obs-dlq', {
    query: {
      tenant: opts.tenantId,
      event_type: opts.eventType || undefined,
      from: opts.from || undefined,
      to: opts.to || undefined,
      limit: opts.limit,
    },
    signal: opts.signal,
  })
}

/** 把观测死信重新放回 outbox，ID 是不透明字符串。 */
export async function replayObsDlq(id: string): Promise<ObsDlqReplayResponse> {
  return apiSend<ObsDlqReplayResponse>('POST', `/admin/v1/obs-dlq/${encodeURIComponent(id)}/replay`)
}

export type { DlqRecord }
