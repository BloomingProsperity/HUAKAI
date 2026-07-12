import { apiGet } from '../../lib/api'
import type { PricingItem } from './types'

/*
 * 模型与定价数据访问层。端点 GET /v1/pricing/page(公开,无需鉴权)。
 * 响应是裸数组 pricingItem[]。
 */
export async function listPricing(signal?: AbortSignal): Promise<PricingItem[]> {
  return apiGet<PricingItem[]>('/v1/pricing/page', { signal })
}
