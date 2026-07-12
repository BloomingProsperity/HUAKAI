import type { BadgeTone } from '../../ui/StatusBadge'
import type { L2MetricsRow, L2StatsResponse } from './types'

/*
 * L2 响应缓存监控页纯逻辑(可单测,无 DOM/网络副作用):
 *   - 字节量人类可读化(B/KB/MB/GB)
 *   - TTL 秒数 → 中文时长
 *   - 容量占用比 + 语气(逼近上限给 warn/danger)
 *   - 驱逐前 key 的前端校验(空串拒,避免发 DELETE /stats 误打统计端点)
 *   - metrics 聚合:总命中/未命中 + 命中率
 *   - 启用态 → 徽章语气/标签
 * 全部同步纯函数,便于 §14 变异打红。
 */

/** 1 KB = 1024 B(二进制单位,与后端 size_bytes 统计口径一致)。 */
const KB = 1024
const MB = KB * 1024
const GB = MB * 1024

/**
 * 字节量人类可读化。判别核心:阈值用二进制 1024,且各档位有不同后缀,
 * 负数/非有限值回退 "—"(避免展示 NaN B)。
 */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '—'
  if (bytes < KB) return `${bytes} B`
  if (bytes < MB) return `${(bytes / KB).toFixed(1)} KB`
  if (bytes < GB) return `${(bytes / MB).toFixed(1)} MB`
  return `${(bytes / GB).toFixed(2)} GB`
}

/**
 * TTL 秒数 → 中文时长。判别核心:>=3600 用「时」、>=60 用「分」、否则「秒」,
 * <=0 视为「未设置」(后端 TTLSeconds 为 0 表示无 TTL 配置)。
 */
export function formatTTL(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) return '未设置'
  if (seconds >= 3600) {
    const h = seconds / 3600
    return `${Number.isInteger(h) ? h : h.toFixed(1)} 小时`
  }
  if (seconds >= 60) {
    const m = seconds / 60
    return `${Number.isInteger(m) ? m : m.toFixed(1)} 分钟`
  }
  return `${seconds} 秒`
}

/**
 * 容量占用比(0~1)。判别核心:max<=0 时返回 0(不能除零,避免 Infinity/NaN);
 * size 超过 max 时夹到 1。
 */
export function capacityRatio(sizeBytes: number, maxSizeBytes: number): number {
  if (!Number.isFinite(maxSizeBytes) || maxSizeBytes <= 0) return 0
  if (!Number.isFinite(sizeBytes) || sizeBytes <= 0) return 0
  const r = sizeBytes / maxSizeBytes
  return r > 1 ? 1 : r
}

/** 占用比 → 百分比展示串(整数四舍五入)。 */
export function capacityPercent(sizeBytes: number, maxSizeBytes: number): string {
  return `${Math.round(capacityRatio(sizeBytes, maxSizeBytes) * 100)}%`
}

/**
 * 占用比 → 徽章语气。判别核心:>=0.9 danger(逼近上限,新写入会触发淘汰),
 * >=0.7 warn,否则 ok。无上限(ratio 恒 0)落 ok。
 */
export function capacityTone(sizeBytes: number, maxSizeBytes: number): BadgeTone {
  const r = capacityRatio(sizeBytes, maxSizeBytes)
  if (r >= 0.9) return 'danger'
  if (r >= 0.7) return 'warn'
  return 'ok'
}

/** 启用态 → 徽章语气。enabled=true→ok,false→muted(缓存关闭,统计为空)。 */
export function enabledTone(enabled: boolean): BadgeTone {
  return enabled ? 'ok' : 'muted'
}

/** 启用态 → 中文标签。 */
export function enabledLabel(enabled: boolean): string {
  return enabled ? '已启用' : '未启用'
}

/** 驱逐 key 的校验结果。 */
export type KeyValidation = { ok: true; value: string } | { ok: false; error: string }

/**
 * 校验待驱逐的 key。判别核心:trim 后为空必须拒——否则 `DELETE /admin/v1/cache/l2/`
 * 会落到 /stats 之外的空段或路由错配,且空 key 不可能命中任何条目。
 * 返回 trim 后的值(后端按精确 key 匹配,不应带首尾空白)。
 */
export function validateEvictKey(key: string): KeyValidation {
  const v = key.trim()
  if (v === '') return { ok: false, error: '请输入要驱逐的缓存 key' }
  return { ok: true, value: v }
}

/** metrics 聚合结果。 */
export interface MetricsTotals {
  hit: number
  miss: number
  /** 命中率(0~1);hit+miss=0 时为 0。 */
  hitRate: number
  /** 参与聚合的 label 行数。 */
  rows: number
}

/**
 * 把 metrics(label → 行)聚合成总命中/未命中 + 命中率。
 * 判别核心:命中率分母是 hit+miss,而非仅 hit 或仅请求数;两者皆 0 时返回 0
 * (避免 0/0=NaN)。空 metrics(租户操作员)→ 全 0。
 */
export function aggregateMetrics(metrics: Record<string, L2MetricsRow>): MetricsTotals {
  let hit = 0
  let miss = 0
  let rows = 0
  for (const row of Object.values(metrics ?? {})) {
    hit += row.hit_total
    miss += row.miss_total
    rows += 1
  }
  const denom = hit + miss
  return { hit, miss, hitRate: denom > 0 ? hit / denom : 0, rows }
}

/** 命中率(0~1)→ 百分比展示串。 */
export function hitRatePercent(stats: Pick<L2StatsResponse, 'metrics'>): string {
  const { hit, miss, hitRate } = aggregateMetrics(stats.metrics)
  if (hit + miss === 0) return '—'
  return `${(hitRate * 100).toFixed(1)}%`
}

/**
 * key 缩写展示(头 16 + 尾 6),用于条目表;过短则原样。
 * 判别核心:仅当长度 > 24 才缩,避免把短 key 也截断。
 */
export function shortKey(key: string): string {
  if (!key) return '—'
  return key.length > 24 ? `${key.slice(0, 16)}…${key.slice(-6)}` : key
}
