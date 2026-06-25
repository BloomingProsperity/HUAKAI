/*
 * Ops 大屏纯逻辑(可单测)。核心是把趋势点序列换算成内联 SVG 折线的 points 串(不引图表库),
 * 以及成功率/错误率配色与数值格式化。
 */

/**
 * 趋势值序列 → SVG polyline 的 points 串。Y 轴翻转(值越大越靠上=y 越小),按 [min,max] 归一化
 * 铺满 [pad, 高-pad]。空序列返回空串;单点居中。这是大屏趋势图的核心,务必可测。
 */
export function sparklinePoints(values: number[], width: number, height: number, pad = 2): string {
  const n = values.length
  if (n === 0) return ''
  const max = Math.max(...values)
  const min = Math.min(...values)
  const flat = max === min // 全等值(含单点):画居中平线,而非压到底/顶。
  const stepX = n > 1 ? (width - 2 * pad) / (n - 1) : 0
  return values
    .map((v, i) => {
      const x = n > 1 ? pad + i * stepX : width / 2
      // 翻转:高值→顶部(y 小);全等值居中。
      const y = flat ? height / 2 : pad + (height - 2 * pad) * (1 - (v - min) / (max - min))
      return `${round1(x)},${round1(y)}`
    })
    .join(' ')
}

function round1(n: number): number {
  return Math.round(n * 10) / 10
}

export type RateTone = 'ok' | 'warn' | 'danger'

/** 成功率配色:≥99% 绿、≥95% 警、否则危。入参是百分比字符串(如 "99.5")。 */
export function successRateTone(successRatePct: string): RateTone {
  const v = Number(successRatePct)
  if (!Number.isFinite(v)) return 'warn'
  if (v >= 99) return 'ok'
  if (v >= 95) return 'warn'
  return 'danger'
}

/** 千分位整数。 */
export function fmtInt(n: number): string {
  return n.toLocaleString('en-US')
}

/** 毫秒延迟展示:≥1000 显示秒,否则毫秒(整数)。 */
export function fmtLatencyMs(ms: number): string {
  if (!Number.isFinite(ms)) return '—'
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`
  return `${Math.round(ms)}ms`
}
