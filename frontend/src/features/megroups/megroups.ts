import type { MeGroupItem } from './types'

/*
 * 我的分组与倍率的纯逻辑(可单测):倍率展示(公开才显、格式化去尾零、未公开显「未公开」)、
 * 用户等级中文化。后端 ratio 是原始小数串(如 "1.50000000"),展示需收敛为 "1.5×"。
 * 严格遵守后端不泄露策略:未公开倍率绝不臆造默认值。
 */

/** 把后端原始倍率串格式化为简洁数字串("1.50000000"→"1.5");非数字原样 trim。 */
export function formatRatio(raw: string): string {
  const v = raw.trim()
  if (!v) return ''
  const n = Number(v)
  return Number.isFinite(n) ? String(n) : v
}

/**
 * 倍率展示文案:仅当后端标记公开且 ratio 存在才显「N×」;否则「未公开」。
 * 防御:has_public_ratio 为真但 ratio 缺失/空 → 仍按未公开处理(不显空 ×)。
 */
export function ratioDisplay(item: Pick<MeGroupItem, 'ratio' | 'has_public_ratio'>): string {
  if (!item.has_public_ratio) return '未公开'
  const formatted = item.ratio ? formatRatio(item.ratio) : ''
  return formatted ? `${formatted}×` : '未公开'
}

/** 倍率徽章语气:公开=info(中性信息),未公开=muted。 */
export function ratioTone(item: Pick<MeGroupItem, 'ratio' | 'has_public_ratio'>): 'info' | 'muted' {
  return item.has_public_ratio && item.ratio ? 'info' : 'muted'
}

/** 常见内建等级 → 中文标签;运维自定义等级原样(trim)。空 → 默认。 */
const GROUP_LABELS: Record<string, string> = {
  default: '默认等级',
  vip: 'VIP',
  svip: 'SVIP',
  pro: '专业版',
  enterprise: '企业版',
}

export function userGroupLabel(group: string): string {
  const v = group.trim()
  if (!v) return '默认等级'
  return GROUP_LABELS[v.toLowerCase()] ?? v
}
