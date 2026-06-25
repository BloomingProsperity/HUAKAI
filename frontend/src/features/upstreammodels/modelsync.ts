import type { BadgeTone } from '../../ui/StatusBadge'
import { REASON_MAX, type ModelSyncRequest, type ModelSyncResult, type ModelSyncResultItem } from './types'

/*
 * 厂商模型同步纯逻辑(可单测):reason 校验/请求体构造、同步结果汇总派生、厂商行配色。
 * 不触网、不打印密钥;仅做输入归一化与展示派生,供 UI 与 api 层复用。
 */

/** reason 是否超长(按 Unicode 码点计,与后端 utf8.RuneCountInString 一致,trim 后判定)。 */
export function isReasonTooLong(reason: string): boolean {
  return [...reason.trim()].length > REASON_MAX
}

/**
 * 构造同步请求体:trim reason,空串则省略 reason 字段(交后端兜底 admin_manual),
 * 而非下发空串。判别核心:空白 reason 不应进入请求体。
 */
export function buildSyncRequest(reason: string): ModelSyncRequest {
  const r = reason.trim()
  return r ? { reason: r } : {}
}

/** 一次同步是否产生了任何实际变更(新增/更新/停用任一非零)。 */
export function hasChanges(result: ModelSyncResult): boolean {
  return result.total_added > 0 || result.total_updated > 0 || result.total_disabled > 0
}

/**
 * 单厂商净变化的配色语气:有停用→warn(目录收缩需留意),仅新增/更新/重新启用→ok,
 * 全无变化→muted。判别核心:disabled 优先于 added/updated。
 */
export function itemTone(item: ModelSyncResultItem): BadgeTone {
  if (item.disabled > 0) return 'warn'
  if (item.added > 0 || item.updated > 0 || item.reactivated > 0) return 'ok'
  return 'muted'
}

/** 厂商行的人类可读摘要,如「+2 新增 · 1 更新 · 1 停用」;全无变化→「无变化」。 */
export function itemSummary(item: ModelSyncResultItem): string {
  const parts: string[] = []
  if (item.added > 0) parts.push(`+${item.added} 新增`)
  if (item.updated > 0) parts.push(`${item.updated} 更新`)
  if (item.reactivated > 0) parts.push(`${item.reactivated} 重启用`)
  if (item.disabled > 0) parts.push(`${item.disabled} 停用`)
  return parts.length > 0 ? parts.join(' · ') : '无变化'
}
