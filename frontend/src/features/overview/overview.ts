/*
 * 概览页纯逻辑(全部可单测、无 DOM/无网络)。
 * 后端的 cap/consumed/total_cost 是十进制字符串(可能带小数),这里统一做安全解析与格式化。
 */

import type { ApiKeyView, KeyUsageSummary, QuotaWindow } from './types'

/** 把可能为字符串/带空白的数值安全解析为 number;非有限值返回 NaN。 */
function toNum(s: string | number): number {
  const n = typeof s === 'number' ? s : Number(String(s).trim())
  return Number.isFinite(n) ? n : NaN
}

// ───────────────────────── 配额进度 ─────────────────────────

export interface QuotaProgress {
  /** 无上限(cap 非正/非数)。 */
  unlimited: boolean
  /** 0-100 的百分比(unlimited 时为 0);超额时条宽 clamp 到 100。 */
  pct: number
  /** 是否超额(consumed > cap)。 */
  over: boolean
  /** 语气:正常 / 接近(≥80%)/ 超额。 */
  tone: 'ok' | 'warn' | 'danger'
}

/**
 * 计算单个配额窗口的进度。
 * 判别核心:tone 的三档阈值(超额=danger、≥80%=warn、其余=ok)与 over 标记。
 */
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

/**
 * 从配额窗口里挑一条最该展示在概览首屏的"主配额"。
 * 优先级:超额 > 接近上限(warn) > 有上限 > 任意。空数组返回 null。
 * 判别核心:超额窗口必须胜过仅接近的窗口(排序权重 danger>warn>有上限)。
 */
export function pickHeadlineQuota(items: QuotaWindow[]): QuotaWindow | null {
  if (!items.length) return null
  const weight = (w: QuotaWindow): number => {
    const p = quotaProgress(w.consumed, w.cap)
    if (p.over) return 3
    if (p.tone === 'warn') return 2
    if (!p.unlimited) return 1
    return 0
  }
  let best = items[0]
  let bestW = weight(best)
  for (let i = 1; i < items.length; i++) {
    const wi = weight(items[i])
    if (wi > bestW) {
      best = items[i]
      bestW = wi
    }
  }
  return best
}

// ───────────────────────── Key 计数 ─────────────────────────

export interface KeyCounts {
  total: number
  active: number
}

/**
 * 统计 Key 总数与活跃数。total 取 list 的实际条数与 count 的较大值
 * (分页只取到一页时 count 反映全量;两者取 max 避免少报)。
 * 判别核心:active 仅计 status==='active' 的项。
 */
export function summarizeKeys(keys: ApiKeyView[], reportedCount?: number): KeyCounts {
  const active = keys.filter((k) => k.status === 'active').length
  const listed = keys.length
  const total = reportedCount !== undefined && reportedCount > listed ? reportedCount : listed
  return { total, active }
}

// ───────────────────────── 用量简图 ─────────────────────────

export interface UsageBar {
  keyId: number
  label: string
  cost: number
  requests: number
}

/**
 * 把每个 Key 的用量汇总折叠成"按花费排序"的简图数据。
 * - cost 用 total_cost(USD 十进制字符串)解析;
 * - 只保留至少有一次请求或有非零花费的 Key(全 0 的不进图,避免空条噪声);
 * - 降序排序后截断到 limit(默认 6),首屏简图不堆叠。
 * 判别核心:排序是按 cost 降序,且全 0 的 Key 被过滤掉。
 */
export function buildUsageBars(
  summaries: Array<{ key: ApiKeyView; summary: KeyUsageSummary | null }>,
  limit = 6,
): UsageBar[] {
  const bars: UsageBar[] = []
  for (const { key, summary } of summaries) {
    if (!summary) continue
    const cost = Math.max(0, Number.isFinite(toNum(summary.total_cost)) ? toNum(summary.total_cost) : 0)
    const requests = Math.max(0, summary.request_count | 0)
    if (cost <= 0 && requests <= 0) continue
    bars.push({ keyId: key.api_key_id, label: key.name || key.key_prefix, cost, requests })
  }
  bars.sort((a, b) => b.cost - a.cost)
  return bars.slice(0, Math.max(0, limit))
}

/**
 * 给一组用量条计算 SVG 画图所需的几何:每条按 cost / maxCost 给 0-1 的比例。
 * maxCost=0(全无花费)时所有比例为 0,调用方应转而显示空态。
 * 判别核心:ratio = cost / maxCost,且 max 取所有条里的最大 cost。
 */
export function usageBarRatios(bars: UsageBar[]): Array<{ bar: UsageBar; ratio: number }> {
  const maxCost = bars.reduce((m, b) => (b.cost > m ? b.cost : m), 0)
  return bars.map((bar) => ({
    bar,
    ratio: maxCost > 0 ? bar.cost / maxCost : 0,
  }))
}

/** 把 USD 数值格式化为定点 4 位小数字符串(微额也可见);非有限值回退 '—'。 */
export function formatUsd(value: number): string {
  if (!Number.isFinite(value)) return '—'
  return value.toFixed(4)
}

/** 整数千分位格式化;非有限值回退 '—'。 */
export function formatCount(value: number): string {
  if (!Number.isFinite(value)) return '—'
  return Math.trunc(value).toLocaleString('en-US')
}
