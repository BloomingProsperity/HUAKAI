import type { RankingEntry } from './types'

/*
 * 模型排行纯逻辑(可单测)。核心三块:
 *  ① limit 夹紧 —— 镜像后端 publicrankinghttp 的区间规则(默认 20 / 上限 100 / 正整数);
 *  ② 多指标客户端重排 —— 后端只按调用次数降序,这里支持切换到「总 token / 占比」维度;
 *  ③ 展示格式化 —— 千分位计数、占比字符串→百分比、相对条形比例。
 * 全为只读派生,无副作用、无 IO。
 */

/** 后端允许的排行条数区间(镜像 publicrankinghttp 常量)。 */
export const DEFAULT_RANKINGS_LIMIT = 20
export const MAX_RANKINGS_LIMIT = 100

/** 可选的展示档位(供 UI 下拉)。 */
export const LIMIT_CHOICES = [10, 20, 50, 100] as const

/**
 * 把任意输入夹紧到后端允许的 limit:
 *  - 非有限 / ≤0 / 非整数 → 回落默认 20;
 *  - >100 → 截到 100;
 *  - 其余取整后原样。
 * 判别核心:>100 必须截到 MAX,且 0/负必须回落默认(而非透传 0 让后端 400)。
 */
export function clampRankingsLimit(raw: number): number {
  if (!Number.isFinite(raw)) return DEFAULT_RANKINGS_LIMIT
  const n = Math.floor(raw)
  if (n <= 0) return DEFAULT_RANKINGS_LIMIT
  if (n > MAX_RANKINGS_LIMIT) return MAX_RANKINGS_LIMIT
  return n
}

/** 客户端可切换的排序指标。 */
export type RankMetric = 'request_count' | 'token_total' | 'request_share'

/** 把 request_share 字符串安全解析成 0~1 的数;非法 → 0。 */
export function parseShare(share: string | undefined): number {
  if (!share) return 0
  const n = Number(share)
  if (!Number.isFinite(n) || n < 0) return 0
  return n
}

/** 取某条目在某指标下的可比数值(request_share 走解析,其余取计数字段,负值兜底为 0)。 */
export function metricValue(entry: RankingEntry, metric: RankMetric): number {
  if (metric === 'request_share') return parseShare(entry.request_share)
  const raw = metric === 'token_total' ? entry.token_total : entry.request_count
  if (!Number.isFinite(raw) || raw < 0) return 0
  return raw
}

/**
 * 按指定指标降序重排并回填名次(rank=1..N)。稳定:同值按模型名升序。
 * 不改原数组。回填的 rank 反映「当前展示排序」,故切到 token 维度时名次会随之变。
 * 判别核心:必须按所选 metric 降序(变异成升序或恒按 request_count 排 → 顺序错 → RED)。
 */
export function rankBy(entries: RankingEntry[], metric: RankMetric): RankingEntry[] {
  const sorted = [...entries].sort((a, b) => {
    const diff = metricValue(b, metric) - metricValue(a, metric)
    if (diff !== 0) return diff
    return a.model.localeCompare(b.model)
  })
  return sorted.map((e, i) => ({ ...e, rank: i + 1 }))
}

/** 千分位整数格式化;非有限 → "—",负值按 0。 */
export function formatCount(n: number): string {
  if (!Number.isFinite(n)) return '—'
  const v = n < 0 ? 0 : Math.floor(n)
  return v.toLocaleString('en-US')
}

/**
 * request_share 字符串 → 百分比展示(如 "0.123456" → "12.35%")。
 * 判别核心:必须 ×100(变异去掉 → "0.12%" 而非 "12.35%" → RED)。空/非法 → "0.00%"。
 */
export function formatShare(share: string | undefined): string {
  const n = parseShare(share)
  return `${(n * 100).toFixed(2)}%`
}

/**
 * 相对条形比例(0~1):某条目在某指标下相对全榜最大值的占比,用于画进度条。
 * 全为 0 或空 → 0(避免除零)。
 */
export function barRatio(entry: RankingEntry, entries: RankingEntry[], metric: RankMetric): number {
  let max = 0
  for (const e of entries) {
    const v = metricValue(e, metric)
    if (v > max) max = v
  }
  if (max <= 0) return 0
  const cur = metricValue(entry, metric)
  return cur <= 0 ? 0 : cur / max
}

/** 指标的中文标签(给 UI 复用,避免散落硬编码)。 */
export function metricLabel(metric: RankMetric): string {
  switch (metric) {
    case 'token_total':
      return '总 Token'
    case 'request_share':
      return '调用占比'
    default:
      return '调用次数'
  }
}
