import type { BadgeTone } from '../../ui/StatusBadge'
import { formatMicroUSD } from '../usagerecords/usagerecords'
import {
  DISPUTE_STATUSES,
  type DisputeFilters,
  type DisputeResolveRequest,
	type DisputeResolveResponse,
	type DisputeResolutionStatus,
  type DisputeStatus,
  type DisputeView,
} from './types'

/*
 * 退款/扣费争议台纯逻辑(可单测,无 DOM/网络副作用):
 *   - 列表 query 构造(tenant_id 必带;status 仅在合法枚举时下发;limit/offset 分页)
 *   - 争议状态 → 徽章语气 + 中文标签
 *   - 裁决表单校验(镜像后端 validateResolveDispute,dispute_store.go:206)
 *   - 短 ID / 时间展示辅助
 * 全部为同步纯函数,便于 §14 变异测试打红。
 */

export type QueryValue = string | number | undefined

/** 后端默认/上限分页(镜像 disputeDefaultListLimit/disputeMaxListLimit,dispute_handler.go:22-24)。 */
export const DEFAULT_PAGE_SIZE = 100
export const MAX_PAGE_SIZE = 500
/** operator_note 上限,镜像后端 validateResolveDispute 的 4000(dispute_store.go:214)。 */
export const OPERATOR_NOTE_MAX = 4000

/**
 * 构造列表 query。tenant_id 必带(后端 platform_admin 角色必填,dispute_handler.go:281);
 * status 仅当是合法枚举(open/reviewing/resolved/rejected)才下发——空串或非法一律省略,
 * 避免给后端发 status=非法(ListForAdmin 会判 invalid status 返 400,dispute_store.go:142)。
 * limit/offset 直接透传(调用方保证范围)。
 */
export function buildListQuery(
  tenantId: number,
  filters: DisputeFilters,
  limit: number,
  offset: number,
): Record<string, QueryValue> {
  const q: Record<string, QueryValue> = {
    tenant_id: tenantId,
    limit,
    offset,
  }
  // 判别核心:仅当 status 落在合法枚举集才下发;空串/未知一律省略。
  if (filters.status !== '' && isDisputeStatus(filters.status)) {
    q.status = filters.status
  }
  return q
}

/** 是否为后端认可的争议状态枚举。 */
export function isDisputeStatus(value: string): value is DisputeStatus {
  return (DISPUTE_STATUSES as string[]).includes(value)
}

/** resolve 端点不允许把争议重新写回 open。 */
export function isDisputeResolutionStatus(value: string): value is DisputeResolutionStatus {
	return value === 'reviewing' || value === 'resolved' || value === 'rejected'
}

/**
 * 争议状态 → 徽章语气。open=待处理(warn);reviewing=审核中(info);
 * resolved=已裁决退款/支持(ok);rejected=驳回维持扣费(danger);未知=中性。
 */
export function statusTone(status: string): BadgeTone {
  switch (status) {
    case 'open':
      return 'warn'
    case 'reviewing':
      return 'info'
    case 'resolved':
      return 'ok'
    case 'rejected':
      return 'danger'
    default:
      return 'muted'
  }
}

/** 争议状态 → 中文标签。 */
export function statusLabel(status: string): string {
  switch (status) {
    case 'open':
      return '待处理'
    case 'reviewing':
      return '审核中'
    case 'resolved':
      return '已裁决(支持退款)'
    case 'rejected':
      return '已驳回(维持扣费)'
    default:
      return status || '—'
  }
}

/** 裁决校验结果:ok 时携带可提交的请求体,否则带中文错误说明。 */
export type ResolveValidation =
  | { ok: true; value: DisputeResolveRequest }
  | { ok: false; error: string }

/**
 * 校验裁决表单(镜像后端 validateResolveDispute,dispute_store.go:206):
 *   - tenant_id > 0
 *   - status ∈ {reviewing, resolved, rejected}
 *   - operator_note trim 后 ≤ 4000
 * 前端先拦,避免无谓 400;后端仍是权威。运营备注允许为空(后端不强制)。
 */
export function validateResolve(
  tenantId: number,
  status: string,
  operatorNote: string,
): ResolveValidation {
  if (!Number.isInteger(tenantId) || tenantId <= 0) {
    return { ok: false, error: 'tenant_id 必须为正整数' }
  }
  // 判别核心:status 必须是合法枚举,否则后端 invalid status 400。
  if (!isDisputeResolutionStatus(status)) {
	return { ok: false, error: '裁决状态非法(须为 reviewing/resolved/rejected)' }
  }
  const note = operatorNote.trim()
  // 判别核心:备注超 4000 即拒(镜像后端 len(TrimSpace)>4000)。
  if (note.length > OPERATOR_NOTE_MAX) {
    return { ok: false, error: `运营备注最多 ${OPERATOR_NOTE_MAX} 字符` }
  }
  return {
    ok: true,
    value: {
      tenant_id: tenantId,
      status,
      operator_note: note,
    },
  }
}

/** 争议是否仍可被裁决(open/reviewing 可改;resolved/rejected 已终态)。 */
export function isResolvable(status: string): boolean {
  return status === 'open' || status === 'reviewing'
}

/** dispute_id 缩写展示(头 12 + 尾 4),用于列表。 */
export function shortDisputeID(id: string): string {
  if (!id) return '—'
  return id.length > 18 ? `${id.slice(0, 12)}…${id.slice(-4)}` : id
}

/** request_id 缩写展示(可能含 host/tail 形态,保头尾)。 */
export function shortRequestID(id: string): string {
  if (!id) return '—'
  return id.length > 20 ? `${id.slice(0, 10)}…${id.slice(-6)}` : id
}

export interface DisputeTableRow {
  id: number
  disputeId: string
  disputeTitle: string
  status: string
  userId: string
  requestId: string
  requestTitle: string
  reason: string
  operatorNote: string
  createdAt: string
  resolvedAt: string
	refundedAmount: string
  resolvable: boolean
  source: DisputeView
}

/** 争议响应到列表列的纯映射，不改变裁决状态或请求体。 */
export function mapDisputeTableRows(items: DisputeView[]): DisputeTableRow[] {
  return items.map((item) => ({
    id: item.id,
    disputeId: shortDisputeID(item.dispute_id),
    disputeTitle: item.dispute_id,
    status: item.status,
    userId: `#${item.user_id}`,
    requestId: shortRequestID(item.request_id),
    requestTitle: item.request_id,
    reason: item.reason || '—',
    operatorNote: item.operator_note || '—',
    createdAt: formatDisputeTime(item.created_at),
	resolvedAt: formatDisputeTime(item.resolved_at),
	refundedAmount: item.status === 'resolved' ? formatMicroUSD(item.refunded_micro_usd ?? 0) : '—',
    resolvable: isResolvable(item.status),
    source: item,
  }))
}

/** 裁决成功提示以服务端真实退款结果为准，禁止仅凭 resolved 状态声称已退款。 */
export function resolveSuccessNotice(response: DisputeResolveResponse): string {
	if (response.dispute.status === 'resolved') {
		if (response.refund_idempotent) return '此前已退款,未重复退款'
		return `已裁决并退款 ${formatMicroUSD(response.refund_micro_usd ?? 0)} 到用户余额`
	}
	return `已裁决 ${response.dispute.dispute_id} → ${statusLabel(response.dispute.status)}`
}

function formatDisputeTime(iso?: string): string {
  if (!iso) return '—'
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString('zh-CN', { hour12: false })
}
