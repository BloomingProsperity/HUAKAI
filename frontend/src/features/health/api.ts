import { apiGet } from '../../lib/api'
import type { HealthResponse } from './types'

/*
 * 系统健康数据访问层。端点 GET /v1/admin/system/health(admin token 鉴权,只读)。
 * 走 /v1/admin/* 前缀,tokenForPath 已修为带 admin token。
 */
export async function getSystemHealth(signal?: AbortSignal): Promise<HealthResponse> {
  return apiGet<HealthResponse>('/v1/admin/system/health', { signal })
}
