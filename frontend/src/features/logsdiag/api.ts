import { apiGet, apiSend } from '../../lib/api'
import type { LogLevelResponse, LogLevelUpdate } from './types'

/*
 * 日志与诊断数据访问层。端点 GET/PUT /v1/admin/loglevel(platform_admin 鉴权,
 * 由 lib/api 按 /v1/admin/* 前缀自动注入 admin Bearer)。
 * 后端在鉴权后委派 zap AtomicLevel.ServeHTTP:GET 读当前级别,PUT 运行时热调。
 */

/** 读取当前进程级日志级别。返回 {"level":"info"} 形态。 */
export async function getLogLevel(signal?: AbortSignal): Promise<LogLevelResponse> {
  return apiGet<LogLevelResponse>('/v1/admin/loglevel', { signal })
}

/** 运行时热调日志级别(无需重启)。成功返回新级别。 */
export async function setLogLevel(level: string): Promise<LogLevelResponse> {
  const body: LogLevelUpdate = { level }
  return apiSend<LogLevelResponse>('PUT', '/v1/admin/loglevel', body)
}

/*
 * 运行日志查询(GET /v1/admin/ops/runtime-logs 族):后端异步 sink 把 warn+ 两栈
 * (zap+slog)日志批量入库;键集分页 id 降序,「实时」= 前端短间隔轮询首页。
 */

export interface RuntimeLogRow {
  id: number
  created_at: string
  level: 'warn' | 'error'
  component: string
  message: string
  request_id?: string | null
  attrs: Record<string, unknown>
}

export interface RuntimeLogsResponse {
  items: RuntimeLogRow[]
  next_before_id: number
}

export interface RuntimeLogsQuery {
  level?: string
  component?: string
  request_id?: string
  before_id?: number
  limit?: number
}

export function listRuntimeLogs(q: RuntimeLogsQuery, signal?: AbortSignal): Promise<RuntimeLogsResponse> {
  const query: Record<string, string | number> = {}
  if (q.level) query.level = q.level
  if (q.component) query.component = q.component
  if (q.request_id) query.request_id = q.request_id
  if (q.before_id) query.before_id = q.before_id
  if (q.limit) query.limit = q.limit
  return apiGet<RuntimeLogsResponse>('/v1/admin/ops/runtime-logs', { query, signal })
}

export interface RuntimeLogSinkHealth {
  queue_len: number
  inserted: number
  dropped: number
  last_flush_at?: string
}

export function getRuntimeLogSinkHealth(signal?: AbortSignal): Promise<RuntimeLogSinkHealth> {
  return apiGet<RuntimeLogSinkHealth>('/v1/admin/ops/runtime-logs/health', { signal })
}

export function cleanupRuntimeLogs(before: string): Promise<{ deleted: number }> {
  return apiSend<{ deleted: number }>('POST', '/v1/admin/ops/runtime-logs/cleanup', { before })
}
