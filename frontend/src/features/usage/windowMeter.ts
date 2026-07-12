/*
 * 额度窗口"分段方格条"的纯逻辑(可单测)。
 * 参照 Claude/Codex 速率窗口的填格显示:把一个百分比映射成"填满几格 / 共几格",
 * 再给重置倒计时文案。金额/时间由调用方传入,本文件不读运行时钟(now 作参数)。
 */

/** 把 0-100 的百分比映射成填充格数(共 total 格,四舍五入,钳制 [0,total])。 */
export function meterCells(pct: number, total = 24): number {
  if (!Number.isFinite(pct) || total <= 0) return 0
  const filled = Math.round((Math.max(0, Math.min(pct, 100)) / 100) * total)
  return Math.max(0, Math.min(filled, total))
}

/**
 * 重置倒计时文案:windowEnd 相对 now 还剩多久。
 * 已过期(end<=now)返回"已结束";无效时间返回空串(调用方隐藏)。
 */
export function resetCountdown(windowEndISO: string, nowMs: number): string {
  const end = Date.parse(windowEndISO)
  if (!Number.isFinite(end) || !Number.isFinite(nowMs)) return ''
  const diff = end - nowMs
  if (diff <= 0) return '已结束'
  const mins = Math.floor(diff / 60000)
  const days = Math.floor(mins / 1440)
  if (days >= 1) {
    const h = Math.floor((mins - days * 1440) / 60)
    return h > 0 ? `重置 ${days}天${h}h 后` : `重置 ${days}天 后`
  }
  const h = Math.floor(mins / 60)
  const m = mins % 60
  if (h >= 1) return `重置 ${h}h${m > 0 ? `${m}m` : ''} 后`
  return `重置 ${Math.max(1, m)}m 后`
}

/**
 * 缓存命中率(占比,0-100)。口径:命中 token / (命中 + 未命中输入)。
 * time-series 的 input 桶是"送给上游计价的输入 token",cache_read 是其中被缓存命中的部分,
 * 二者相加=总提示 token;命中率=cache_read/(cache_read+input)。分母 0 时返回 null(无数据)。
 */
export function cacheHitRate(cacheRead: number, input: number): number | null {
  const cr = Number.isFinite(cacheRead) && cacheRead > 0 ? cacheRead : 0
  const inp = Number.isFinite(input) && input > 0 ? input : 0
  const denom = cr + inp
  if (denom <= 0) return null
  return (cr / denom) * 100
}
