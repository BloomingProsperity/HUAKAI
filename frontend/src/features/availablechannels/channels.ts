import type { PricingItem } from './types'

/*
 * 可用渠道目录纯逻辑(可单测)。
 * 核心 = 把扁平的模型列表按「厂商(owned_by)= 渠道」聚合成渠道卡片,并算出每渠道的
 * 价目区间(最低/最高 输出价)与模型数。这是目录页的关键展示逻辑;另有价格换算与搜索过滤。
 */

/** 价格展示单位:每百万 token / 每 token。 */
export type PriceUnit = 'mtok' | 'token'

/** 无 owned_by 的模型归入此渠道名。 */
export const OTHER_CHANNEL = '其他'

/**
 * 每-token 美元价字符串 → 每-百万-token 数值(美元)。
 * 空 / 非法 / 负 → null(目录中缺价模型已被后端过滤,这里兜底)。
 */
export function perMillion(perToken?: string): number | null {
  if (!perToken) return null
  const n = Number(perToken)
  if (!Number.isFinite(n) || n < 0) return null
  return n * 1_000_000
}

/**
 * 按单位把每-token 价字符串格式化为展示串。
 *  - mtok:每百万 token,×1e6 后 $X.XX(极小值用 4 位,$0 特例);
 *  - token:每 token 原始极小数(最多 8 位,去尾零)。
 * 空 / 非法 / 负 → "—"。
 */
export function formatPrice(perToken: string | undefined, unit: PriceUnit): string {
  if (!perToken) return '—'
  const n = Number(perToken)
  if (!Number.isFinite(n) || n < 0) return '—'
  if (unit === 'mtok') {
    const perM = n * 1_000_000
    if (perM === 0) return '$0'
    const decimals = perM >= 0.01 ? 2 : 4
    return `$${perM.toFixed(decimals)}`
  }
  if (n === 0) return '$0'
  // 去尾零:toFixed 同时修正 IEEE754 浮点尾巴。
  return `$${n.toFixed(8).replace(/\.?0+$/, '')}`
}

/** 能力 map → 已开启能力名数组(值为 true 的键),保持插入序。 */
export function capabilityList(caps?: Record<string, boolean>): string[] {
  if (!caps) return []
  return Object.keys(caps).filter((k) => caps[k])
}

/** 按模型名 / canonical_id / 厂商 大小写不敏感过滤;空查询返回原集(同一引用)。 */
export function filterCatalog(items: PricingItem[], query: string): PricingItem[] {
  const q = query.trim().toLowerCase()
  if (!q) return items
  return items.filter(
    (it) =>
      it.model.toLowerCase().includes(q) ||
      (it.canonical_id ?? '').toLowerCase().includes(q) ||
      (it.owned_by ?? '').toLowerCase().includes(q),
  )
}

/** 单个渠道(= 一个厂商)聚合后的视图模型。 */
export interface Channel {
  /** 渠道名(owned_by;空归入「其他」)。 */
  name: string
  /** 该渠道下的模型(保持原序)。 */
  models: PricingItem[]
  /** 模型数。 */
  modelCount: number
  /** 该渠道内出现过的能力名去重(并集,排序)。 */
  capabilities: string[]
  /** 输出价区间(每百万 token,美元);无任何有效价时为 null。 */
  outputPriceRange: { min: number; max: number } | null
}

/**
 * 把扁平模型列表聚合成渠道列表(目录核心)。
 *  - 按 owned_by 分组(空 → 「其他」),分组顺序按首次出现序保持稳定;
 *  - 每渠道算输出价区间(每百万 token)与能力并集。
 * 这是「可用渠道目录」区别于扁平模型广场的关键聚合判别逻辑。
 */
export function buildChannels(items: PricingItem[]): Channel[] {
  const order: string[] = []
  const groups = new Map<string, PricingItem[]>()
  for (const it of items) {
    const name = it.owned_by || OTHER_CHANNEL
    if (!groups.has(name)) {
      groups.set(name, [])
      order.push(name)
    }
    groups.get(name)!.push(it)
  }
  return order.map((name) => {
    const models = groups.get(name)!
    const caps = new Set<string>()
    let min = Infinity
    let max = -Infinity
    for (const m of models) {
      for (const c of capabilityList(m.capabilities)) caps.add(c)
      const p = perMillion(m.output_price_per_token)
      if (p !== null) {
        if (p < min) min = p
        if (p > max) max = p
      }
    }
    return {
      name,
      models,
      modelCount: models.length,
      capabilities: [...caps].sort((a, b) => a.localeCompare(b)),
      outputPriceRange: min === Infinity ? null : { min, max },
    }
  })
}

/** 把每百万 token 价区间格式化为展示串("$3.00" / "$3.00 – $15.00" / "—")。 */
export function formatPriceRange(range: { min: number; max: number } | null): string {
  if (!range) return '—'
  const fmt = (v: number) => `$${v.toFixed(v >= 0.01 || v === 0 ? 2 : 4)}`
  if (range.min === range.max) return fmt(range.min)
  return `${fmt(range.min)} – ${fmt(range.max)}`
}
