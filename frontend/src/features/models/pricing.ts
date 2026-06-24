import type { PricingItem } from './types'

/*
 * 模型与定价纯逻辑(可单测)。核心=每-token 价(极小数字符串)换算成每-百万-token 价(可读),
 * 这是定价目录的关键展示逻辑;另有能力清单与按名/厂商过滤。
 */

/**
 * 每-token 美元价字符串 → 每-百万-token 展示串(如 "0.000003" → "$3.00")。
 * 空/非法 → "—"(目录里缺价模型本就被后端过滤,这里兜底)。保留至多 2 位小数,极小值用更多位。
 */
export function pricePerMillion(perToken?: string): string {
  if (!perToken) return '—'
  const n = Number(perToken)
  if (!Number.isFinite(n) || n < 0) return '—'
  const perM = n * 1_000_000
  if (perM === 0) return '$0'
  // 大于等于 0.01 用 2 位;更小用 4 位,避免显示成 $0.00。
  const decimals = perM >= 0.01 ? 2 : 4
  return `$${perM.toFixed(decimals)}`
}

/** 能力 map → 已开启能力名数组(值为 true 的键)。 */
export function capabilityList(caps?: Record<string, boolean>): string[] {
  if (!caps) return []
  return Object.keys(caps).filter((k) => caps[k])
}

/** 按模型名 / canonical_id / 厂商(owned_by)大小写不敏感过滤;空查询返回原集。 */
export function filterModels(items: PricingItem[], query: string): PricingItem[] {
  const q = query.trim().toLowerCase()
  if (!q) return items
  return items.filter(
    (it) =>
      it.model.toLowerCase().includes(q) ||
      (it.canonical_id ?? '').toLowerCase().includes(q) ||
      (it.owned_by ?? '').toLowerCase().includes(q),
  )
}

/** 按厂商(owned_by)分组,保持原序;无 owned_by 归入「其他」。 */
export function groupByOwner(items: PricingItem[]): Array<{ owner: string; models: PricingItem[] }> {
  const order: string[] = []
  const map = new Map<string, PricingItem[]>()
  for (const it of items) {
    const owner = it.owned_by || '其他'
    if (!map.has(owner)) {
      map.set(owner, [])
      order.push(owner)
    }
    map.get(owner)!.push(it)
  }
  return order.map((owner) => ({ owner, models: map.get(owner)! }))
}
