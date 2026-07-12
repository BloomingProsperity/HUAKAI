import { apiGet } from '../../lib/api'
import type { PricingItem, SiteConfig } from './types'

/*
 * 落地首页数据访问层。两个公开端点,均无需鉴权(/v1/* 非 admin → lib/api 不注入 token)。
 *  - GET /v1/site/config:站点品牌/开关(sitepublichttp,routes_siteconfig.go:19)。
 *  - GET /v1/pricing/page:公开价目数组(pricingpublichttp,routes.go:226)。
 * 纯只读,落地页不做任何写动作。
 */

/** 拉站点公开配置(品牌/简介/文档/注册开关)。 */
export async function fetchSiteConfig(signal?: AbortSignal): Promise<SiteConfig> {
  return apiGet<SiteConfig>('/v1/site/config', { signal })
}

/** 拉公开价目表(裸数组,仅含已定价模型)。 */
export async function fetchPricing(signal?: AbortSignal): Promise<PricingItem[]> {
  return apiGet<PricingItem[]>('/v1/pricing/page', { signal })
}
