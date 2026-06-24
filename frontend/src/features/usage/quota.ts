/*
 * 配额展示的纯逻辑(可单测)。后端 cap/consumed 是十进制字符串(可能带小数)。
 * 进度=consumed/cap;cap≤0 或非数 视为"无上限"(不画进度条)。
 */

export interface QuotaProgress {
  /** 无上限(cap 非正/非数)。 */
  unlimited: boolean
  /** 0-100 的百分比(unlimited 时为 0);超额 clamp 到 100 用于条宽,但 over 标记单独给。 */
  pct: number
  /** 是否超额(consumed > cap)。 */
  over: boolean
  /** 语气:正常/接近(≥80%)/超额。 */
  tone: 'ok' | 'warn' | 'danger'
}

function toNum(s: string): number {
  const n = Number(String(s).trim())
  return Number.isFinite(n) ? n : NaN
}

export function quotaProgress(consumed: string, cap: string): QuotaProgress {
  const capN = toNum(cap)
  const conN = toNum(consumed)
  if (!Number.isFinite(capN) || capN <= 0) {
    return { unlimited: true, pct: 0, over: false, tone: 'ok' }
  }
  const ratio = (Number.isFinite(conN) ? conN : 0) / capN
  const over = ratio > 1
  const pct = Math.max(0, Math.min(ratio * 100, 100))
  const tone: QuotaProgress['tone'] = over ? 'danger' : ratio >= 0.8 ? 'warn' : 'ok'
  return { unlimited: false, pct, over, tone }
}

const METRIC_LABEL: Record<string, string> = {
  cost: '花费(USD)',
  requests: '请求数',
  tokens: 'Token 数',
}
const WINDOW_LABEL: Record<string, string> = {
  daily: '每日',
  weekly: '每周',
  monthly: '每月',
  fixed: '固定窗口',
  rolling: '滚动窗口',
}

export function metricLabel(metric: string): string {
  return METRIC_LABEL[metric] ?? metric
}
export function windowLabel(kind: string): string {
  return WINDOW_LABEL[kind] ?? kind
}
