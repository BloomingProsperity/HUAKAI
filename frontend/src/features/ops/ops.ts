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

/**
 * 成功率配色:≥99% 绿、≥95% 警、否则危。
 * 入参是后端 overview 的 success_rate —— 0~1 的小数(successCount/requestCount 经 StringFixed(4),
 * 如 "0.9950" 表示 99.5%),不是百分数。故阈值按 0~1 标度(0.99 / 0.95)比较。
 * 判别点:若误按 0~100 标度(v>=99)比,则 0.9950 恒落 danger —— 即便 100% 成功也永远显示告警。
 */
export function successRateTone(successRateFraction: string): RateTone {
  const v = Number(successRateFraction)
  if (!Number.isFinite(v)) return 'warn'
  if (v >= 0.99) return 'ok'
  if (v >= 0.95) return 'warn'
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

// ── 用量性能分析 4 端点的纯逻辑(§14 变异法,均可测)──────────────────────────────

/**
 * 把大屏的 window 选项(24h/7d/30d)换算成 provider-account-counts 端点要求的 [from,to]
 * RFC3339 区间(to=now、from=now-跨度)。该端点不收 window、只收绝对时间戳(handler 要求
 * RFC3339 且 to>from、跨度 ≤90 天),所以前端必须自己折算。未知 window 回退 7 天,绝不
 * 返回 to≤from 的非法区间(否则后端 400 invalid_window)。now 注入以便测试。
 */
export function windowToRange(window: string, now: Date): { from: string; to: string } {
  const spanMs = WINDOW_SPAN_MS[window] ?? WINDOW_SPAN_MS['7d']
  const to = now
  const from = new Date(now.getTime() - spanMs)
  return { from: from.toISOString(), to: to.toISOString() }
}

const DAY_MS = 24 * 60 * 60 * 1000
const WINDOW_SPAN_MS: Record<string, number> = {
  '24h': DAY_MS,
  '7d': 7 * DAY_MS,
  '30d': 30 * DAY_MS,
}

/**
 * 后端 performance / perf-metrics / health-score 的 error_rate 是 0~1 的小数(StringFixed(4)),
 * 不是百分数。展示前乘 100 并保留 2 位小数加 % 号(如 "0.0123" → "1.23%")。非法输入回退 "—"。
 * 判别点:必须乘 100;若直接拼 % 会把 0.0123 显示成 "0.0123%"(差 100 倍)。
 */
export function fmtFractionPct(fraction: string): string {
  const v = Number(fraction)
  if (!Number.isFinite(v)) return '—'
  return `${(v * 100).toFixed(2)}%`
}

/**
 * 健康分(0~100)配色带:≥90 绿、≥70 警、否则危。入参是后端的整数分。
 * 非数字回退 warn(与 successRateTone 的降级口径一致)。
 */
export function healthScoreTone(score: number): RateTone {
  if (!Number.isFinite(score)) return 'warn'
  if (score >= 90) return 'ok'
  if (score >= 70) return 'warn'
  return 'danger'
}

/**
 * provider 账号聚合行的「总 Token」=输入+输出之和(后端只分别给了 in/out 两列,不给合计)。
 * 抽成纯函数便于在表格与合计行复用,并可被变异测试钉住(漏加某一列即 RED)。
 */
export function totalTokens(row: { total_input_tokens: number; total_output_tokens: number }): number {
  return row.total_input_tokens + row.total_output_tokens
}
