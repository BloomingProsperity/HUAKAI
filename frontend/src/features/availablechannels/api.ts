import { apiGet } from '../../lib/api'
import type { PricingItem } from './types'

/*
 * 可用渠道目录数据访问层。
 * 端点 GET /v1/pricing/page(公开,无需鉴权;路径不在 /admin/* 与 /v1/auth/* 内,
 * 由 api.ts 自动按 session 处理,公开端点忽略鉴权头)。响应为裸数组 PricingItem[]。
 */
export async function listAvailableChannels(signal?: AbortSignal): Promise<PricingItem[]> {
  return apiGet<PricingItem[]>('/v1/pricing/page', { signal })
}
