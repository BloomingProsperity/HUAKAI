import type { BadgeTone } from '../../ui/StatusBadge'
import { EVENT_KINDS, type DlqRecord } from './types'

/*
 * 死信队列页纯逻辑(可单测,无 DOM/网络副作用):
 *   - limit 校验(镜像后端 1..200,见 admin_dlq_handler.go:42)
 *   - status / lane / event_kind → 徽章语气 + 中文标签
 *   - event_kind 是否触及计费(决定 replay 的二次确认措辞强度)
 *   - 时间戳格式化(空串/非法回退破折号)
 *   - payload 美化展示
 * 全部同步纯函数,便于变异测试打红。
 */

/** limit 上限,镜像后端 admin_dlq_handler.go:42(n<1 || n>200 即 400)。 */
export const LIMIT_MAX = 200
/** 列表默认 limit,镜像后端 admin_dlq_handler.go:39。 */
export const LIMIT_DEFAULT = 100

export type LimitValidation = { ok: true; value: number } | { ok: false; error: string }

/**
 * 校验 limit ∈ [1, 200](镜像后端硬约束)。
 * 判别核心:0 与 201 都必须拒;非整数拒;前端先拦避免无谓 400。
 */
export function validateLimit(n: number): LimitValidation {
  if (!Number.isInteger(n) || n < 1) return { ok: false, error: '条数必须是 ≥1 的整数' }
  if (n > LIMIT_MAX) return { ok: false, error: `条数最多 ${LIMIT_MAX}` }
  return { ok: true, value: n }
}

/**
 * 处理状态 → 徽章语气。
 * delivered=已投递(ok);dlq/quarantined=进死信/隔离(danger);
 * operator_review=待人工(warn);pending/inflight=处理中(info);未知=中性。
 */
export function statusTone(status: string): BadgeTone {
  switch (status) {
    case 'delivered':
      return 'ok'
    case 'dlq':
    case 'quarantined':
      return 'danger'
    case 'operator_review':
      return 'warn'
    case 'pending':
    case 'inflight':
      return 'info'
    default:
      return 'muted'
  }
}

/** 处理状态 → 中文标签。 */
export function statusLabel(status: string): string {
  switch (status) {
    case 'pending':
      return '待处理'
    case 'inflight':
      return '处理中'
    case 'delivered':
      return '已投递'
    case 'operator_review':
      return '待人工审阅'
    case 'dlq':
      return '死信'
    case 'quarantined':
      return '已隔离'
    default:
      return status || '—'
  }
}

/** 泳道 → 徽章语气(HIGH=danger 凸显高优;MED=warn;LOW=muted)。 */
export function laneTone(lane: string): BadgeTone {
  switch (lane) {
    case 'HIGH':
      return 'danger'
    case 'MED':
      return 'warn'
    case 'LOW':
      return 'muted'
    default:
      return 'muted'
  }
}

/** event_kind → 中文标签(镜像 backend/internal/dlq/types.go:14-30)。 */
export function eventKindLabel(kind: string): string {
  switch (kind) {
    case 'usage_record':
      return '用量记录'
    case 'billing_event_replica':
      return '计费事件副本'
    case 'audit_event_replica':
      return '审计事件副本'
    case 'audit_mismatch_refund':
      return '审计差错退款'
    case 'audit_ledger_entry':
      return '审计账本分录'
    case 'account_health':
      return '账号健康'
    case 'metrics':
      return '指标'
    case 'post_delivery_settlement':
      return '交付后结算恢复'
    case 'cost_receipt_append':
      return '成本凭证补写'
    default:
      return kind || '—'
  }
}

/**
 * 该 event_kind 重放是否会触及金钱(结算/退款/计费/凭证)。
 * 命中者 replay 的二次确认措辞更强烈地提示「会触发计费恢复」。
 * 判别核心:结算/退款/计费/账本/成本凭证类=true;纯指标/账号健康=false。
 * (重放本身幂等,但 money 类影响余额,需更显眼的提示。)
 */
export function isMoneySensitiveKind(kind: string): boolean {
  switch (kind) {
    case 'usage_record':
    case 'billing_event_replica':
    case 'audit_mismatch_refund':
    case 'audit_ledger_entry':
    case 'post_delivery_settlement':
    case 'cost_receipt_append':
      return true
    default:
      // audit_event_replica / account_health / metrics 不直接动余额。
      return false
  }
}

/** 是否为已知的 event_kind(供 handler 输入校验/提示)。 */
export function isKnownEventKind(kind: string): boolean {
  return (EVENT_KINDS as readonly string[]).includes(kind)
}

/**
 * 时间戳展示:后端是 RFC3339Nano 字符串,无效字段为空串。
 * 空串/非法 → 破折号;合法 → 本地化(24 小时制)。
 */
export function formatTs(iso: string | null | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN', { hour12: false })
}

/**
 * payload 美化:对象 → 缩进 JSON;字符串原样;空/未定义 → "{}"。
 * 仅用于详情展示,失败时回退 String()。
 */
export function formatPayload(payload: unknown): string {
  if (payload === undefined || payload === null) return '{}'
  try {
    return JSON.stringify(payload, null, 2)
  } catch {
    return String(payload)
  }
}

/**
 * 该记录当前是否可重放(给按钮置灰用)。
 * 判别核心:已投递(delivered)的记录无需重放,按钮禁用;其余可重放。
 * (后端不阻止重放 delivered,但 UI 层避免误触重复结算入口。)
 */
export function canReplay(record: Pick<DlqRecord, 'status'>): boolean {
  return record.status !== 'delivered'
}

/** failure_reason 行内缩略(过长截断加省略号),用于列表单元格。 */
export function shortReason(reason: string, max = 64): string {
  const r = (reason || '').trim()
  if (r === '') return '—'
  return r.length > max ? `${r.slice(0, max)}…` : r
}
