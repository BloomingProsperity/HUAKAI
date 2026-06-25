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
