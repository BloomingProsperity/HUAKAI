import { apiGet, apiSend } from '../../lib/api'
import type { L2EvictResponse, L2StatsResponse } from './types'

/*
 * L2 响应缓存监控数据访问层。
 * 端点挂在 /admin/v1/cache/l2 前缀(routes.go:1119),admin token 鉴权
 * (tokenForPath 对 /admin/v1 自动注入 admin Bearer)。
 *   - GET    /stats        只读统计(handler.go:34 → newAdminL2StatsHandler)
 *   - DELETE /{key}        按 key 逐条驱逐(handler.go:35 → newAdminL2DeleteHandler,破坏性)
 */
const PATH = '/admin/v1/cache/l2'

/** 读取 L2 缓存统计快照(命中/容量/TTL/条目)。只读,不触碰任何条目。 */
export async function getL2Stats(signal?: AbortSignal): Promise<L2StatsResponse> {
  return apiGet<L2StatsResponse>(`${PATH}/stats`, { signal })
}

/**
 * 按 key 驱逐单条缓存(破坏性运维)。后端先 Get 校验存在 + 租户作用域
 * (handler.go:79-87),再 Delete;返回 {key, deleted}。key 不存在回 404
 * cache_l2_not_found,跨租户回 403 admin_forbidden。
 * 注:key 走 path 段,必须 encodeURIComponent 以容纳 key 中的特殊字符。
 */
export async function evictL2Key(key: string): Promise<L2EvictResponse> {
  return apiSend<L2EvictResponse>('DELETE', `${PATH}/${encodeURIComponent(key)}`)
}
