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

/* ─────────────────── 模型广场增强(P0):单位换算 + 多维筛选 facet ─────────────────── */

/** 价格展示单位:每百万 token / 每 token。 */
export type PriceUnit = 'mtok' | 'token'

/**
 * 去尾零格式化:把数字格式化为最多 maxDecimals 位小数并去掉无意义尾零,
 * 同时借 toFixed 修正 IEEE754 误差(如 0.000003*1e6 的浮点尾巴)。
 */
export function formatScaled(n: number, maxDecimals: number): string {
  if (!Number.isFinite(n)) return '—'
  const fixed = n.toFixed(maxDecimals)
  // 去掉尾部多余的 0 与可能孤立的小数点
  return fixed.replace(/\.?0+$/, '')
}

/**
 * 每-token 价字符串 → 按单位的展示串。
 *  - mtok:每百万 token,×1e6 后 $X.XX(极小值用 4 位);
 *  - token:每 token 原始极小数,去尾零展示(修浮点)。
 * 空/非法/负 → "—"。
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
  return `$${formatScaled(n, 8)}`
}

function uniqueSorted(values: string[]): string[] {
  return [...new Set(values)].sort((a, b) => a.localeCompare(b))
}

/** 收集所有厂商(owned_by)去重排序;无 owned_by 计入「其他」。 */
export function collectOwners(items: PricingItem[]): string[] {
  return uniqueSorted(items.map((i) => i.owned_by || '其他'))
}

/** 收集所有模式(mode)去重排序;空 mode 忽略。 */
export function collectModes(items: PricingItem[]): string[] {
  return uniqueSorted(items.map((i) => i.mode).filter((m): m is string => !!m))
}

/** 收集所有出现过的能力名去重排序(仅取值为 true 的能力)。 */
export function collectCapabilities(items: PricingItem[]): string[] {
  const set = new Set<string>()
  for (const it of items) for (const c of capabilityList(it.capabilities)) set.add(c)
  return [...set].sort((a, b) => a.localeCompare(b))
}

export interface ModelFilters {
  /** 自由文本(模型名 / canonical_id / 厂商)。 */
  query: string
  /** 厂商精确匹配;'' = 全部。 */
  owner: string
  /** 模式精确匹配;'' = 全部。 */
  mode: string
  /** 能力名;'' = 全部,否则要求模型具备该能力。 */
  capability: string
}

export const EMPTY_MODEL_FILTERS: ModelFilters = { query: '', owner: '', mode: '', capability: '' }

/**
 * 组合多维筛选:复用已测的 filterModels(自由文本)再叠加厂商/模式/能力三维精确过滤。
 * 任一维为空串表示该维不约束。
 */
export function applyFilters(items: PricingItem[], f: ModelFilters): PricingItem[] {
  let out = filterModels(items, f.query)
  if (f.owner) out = out.filter((i) => (i.owned_by || '其他') === f.owner)
  if (f.mode) out = out.filter((i) => i.mode === f.mode)
  if (f.capability) out = out.filter((i) => capabilityList(i.capabilities).includes(f.capability))
  return out
}

export interface ModelTableRow {
  id: string
  model: string
  canonicalId: string | null
  owner: string
  inputPrice: string
  outputPrice: string
  contextLength: string
  capabilities: string[]
  item: PricingItem
}

/** 当前价格 DTO 到模型主表展示行的纯映射。 */
export function mapModelTableRows(items: PricingItem[], unit: PriceUnit): ModelTableRow[] {
  return items.map((item) => ({
    id: item.model,
    model: item.model,
    canonicalId: item.canonical_id && item.canonical_id !== item.model ? item.canonical_id : null,
    owner: item.owned_by || '其他',
    inputPrice: formatPrice(item.input_price_per_token, unit),
    outputPrice: formatPrice(item.output_price_per_token, unit),
    contextLength: item.context_length ? formatModelTokens(item.context_length) : '—',
    capabilities: capabilityList(item.capabilities),
    item,
  }))
}

export function formatModelTokens(value: number): string {
  if (value >= 1000) return `${Math.round(value / 1000)}K`
  return String(value)
}
