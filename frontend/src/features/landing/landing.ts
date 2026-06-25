import type { PricingItem, SiteConfig } from './types'

/*
 * 落地首页纯逻辑(可单测)。
 * 核心三件:
 *  1) 品牌兜底:site_name 缺省时给出可读默认,避免空标题;
 *  2) 每-token 价 → 每-百万-token 可读串(对外营销用每百万 token 报价更直观);
 *  3) 价目亮点挑选:从公开价目里挑「有完整输入+输出价」的前 N 条做首页预览。
 * 不触网、不依赖 React,便于变异测试。
 */

/** 落地页默认品牌名(site_name 缺省时用)。 */
export const DEFAULT_SITE_NAME = 'HUAKAI 中转站'

/** 落地页默认副标题(site_subtitle 缺省时用)。 */
export const DEFAULT_SITE_SUBTITLE = '统一聚合上游模型,稳定中转,按量计费'

/**
 * 品牌名兜底:site_name 去空白后为空 → 回退到 DEFAULT_SITE_NAME。
 * 判别核心:空/纯空白必须回退,非空必须原样返回(trim 后)。
 */
export function brandName(cfg?: Pick<SiteConfig, 'site_name'> | null): string {
  const raw = (cfg?.site_name ?? '').trim()
  return raw || DEFAULT_SITE_NAME
}

/** 副标题兜底:同 brandName 规则。 */
export function brandSubtitle(cfg?: Pick<SiteConfig, 'site_subtitle'> | null): string {
  const raw = (cfg?.site_subtitle ?? '').trim()
  return raw || DEFAULT_SITE_SUBTITLE
}

/**
 * 文档链接是否可展示:非空且是 http(s) 绝对地址才放行(防止把任意串当链接渲染)。
 * 判别核心:空串/相对路径/非 http 协议都要拒绝。
 */
export function docLinkOrNull(cfg?: Pick<SiteConfig, 'site_doc_url'> | null): string | null {
  const raw = (cfg?.site_doc_url ?? '').trim()
  if (!raw) return null
  if (!/^https?:\/\//i.test(raw)) return null
  return raw
}

/**
 * 每-token 美元价字符串 → 每-百万-token 展示串(如 "0.000003" → "$3.00")。
 * 空/非法/负 → "—"。≥0.01 用 2 位小数,更小用 4 位以免显示成 $0.00。
 * 判别核心:必须 ×1e6 换算(变异成不乘则 "0.000003" 会显示成 $0.0000 而非 $3.00)。
 */
export function pricePerMillion(perToken?: string): string {
  if (!perToken) return '—'
  const n = Number(perToken)
  if (!Number.isFinite(n) || n < 0) return '—'
  const perM = n * 1_000_000
  if (perM === 0) return '$0'
  const decimals = perM >= 0.01 ? 2 : 4
  return `$${perM.toFixed(decimals)}`
}

/**
 * 挑选价目亮点:仅保留同时有输入与输出价的模型,取前 limit 条做首页预览。
 * 判别核心:缺任一价的项必须被剔除(变异成只判输入价会放进缺输出价的项)。
 */
export function pricingHighlights(items: PricingItem[], limit = 6): PricingItem[] {
  const priced = items.filter(
    (it) => !!it.input_price_per_token && !!it.output_price_per_token,
  )
  return priced.slice(0, Math.max(0, limit))
}

/** 厂商展示名:owned_by 为空 → 「其他」。 */
export function ownerLabel(item: Pick<PricingItem, 'owned_by'>): string {
  return (item.owned_by ?? '').trim() || '其他'
}
