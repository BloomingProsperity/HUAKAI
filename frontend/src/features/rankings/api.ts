import { apiGet } from '../../lib/api'
import { clampRankingsLimit } from './rankings'
import type { RankingsResponse } from './types'

/*
 * 模型排行数据访问层。端点 GET /v1/public/rankings(公开,无需鉴权)。
 * 响应是包络对象 { scope, metric, rankings[] }(非裸数组)。
 * limit 在前端先夹紧到后端允许区间,避免无谓往返与后端二次纠正。
 */
export async function listRankings(limit: number, signal?: AbortSignal): Promise<RankingsResponse> {
  return apiGet<RankingsResponse>('/v1/public/rankings', {
    query: { limit: clampRankingsLimit(limit) },
    signal,
  })
}
