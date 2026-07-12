import type { BadgeTone } from '../../ui/StatusBadge'
import { RECONCILE_STATUSES, type ReconcileRequest, type ReconcileStatus } from './types'

/*
 * 媒体任务孤儿对账页纯逻辑(可单测,无 DOM / 网络副作用):
 *   - 列表 query 构造(tenant_id 仅在正整数时下发,limit 透传)
 *   - 对账请求体构造(money 守卫:back_charge 仅 status=reconciled 合法,镜像后端 reconcile.go:177)
 *   - 对账状态 → 徽章语气 + 中文标签
 *   - 金额(分)展示
 *   - 追扣结果(back_charge_outcome)→ 中文解释
 * 全为同步纯函数,便于 §14 变异测试打红。
 */

export type QueryValue = string | number | undefined

/**
 * 构造孤儿列表 query。tenant_id 仅在为正整数时下发(后端 platform_admin 角色 tenant_id
 * 为【可选收窄】,缺省 = 跨租户全局扫;且后端要求 tenant_id 必须为正整数,否则 400
 * invalid_tenant_id,见 routes.go:137)。limit 仅在正整数时下发(后端 invalid_limit 400)。
 *
 * 判别核心:tenantId<=0 / 非整数时绝不下发 tenant_id(否则 platform_admin 反而被 400);
 * limit<=0 / 非整数时绝不下发 limit。
 */
export function buildListQuery(
  tenantId: number | null,
  limit: number,
): Record<string, QueryValue> {
  const q: Record<string, QueryValue> = {}
  if (tenantId != null && Number.isInteger(tenantId) && tenantId > 0) {
    q.tenant_id = tenantId
  }
  if (Number.isInteger(limit) && limit > 0) {
    q.limit = limit
  }
  return q
}

/** 解析租户输入框文本为 tenant_id 过滤值:空串=不过滤(null);正整数=过滤;非法=null。 */
export function parseTenantFilter(raw: string): number | null {
  const t = raw.trim()
  if (t === '') return null
  if (!/^[1-9][0-9]*$/.test(t)) return null
  return Number(t)
}

/** 对账请求体构造结果:ok 时携可提交体,否则带中文错误说明。 */
export type ReconcileBuild =
  | { ok: true; value: ReconcileRequest }
  | { ok: false; error: string }

/**
 * 构造对账请求体。**money 守卫**(镜像后端 reconcile.go:177 的硬约束):
 *   - back_charge 仅当 status=reconciled 时合法;cancelled/ignored 表示判定不追,
 *     若仍带 back_charge=true,后端会回 400 invalid_orphan_status。前端先拦,避免无谓 400,
 *     更避免操作员误以为「取消时也能追扣」。
 *   - reason 为空串时省略(后端 reason 可选,空串与不传等价)。
 *
 * 判别核心:status !== 'reconciled' 且 backCharge === true 必须被拒。
 */
export function buildReconcileRequest(
  status: string,
  backCharge: boolean,
  reason: string,
): ReconcileBuild {
  if (!(RECONCILE_STATUSES as readonly string[]).includes(status)) {
    return { ok: false, error: '终态必须是 reconciled / cancelled / ignored 之一' }
  }
  if (backCharge && status !== 'reconciled') {
    return { ok: false, error: '追扣(back_charge)仅在终态为 reconciled 时合法' }
  }
  const body: ReconcileRequest = { status, back_charge: backCharge }
  const r = reason.trim()
  if (r !== '') body.reason = r
  return { ok: true, value: body }
}

/** 该(status, backCharge)组合是否需要 money 二次确认(只有真追扣才动钱)。 */
export function needsBackChargeConfirm(status: string, backCharge: boolean): boolean {
  return backCharge && status === 'reconciled'
}

/**
 * 对账状态 → 徽章语气。pending=待处置(warn,需关注);reconciled=已对账(ok);
 * cancelled / ignored=已关闭不追(muted);未知=中性。
 */
export function statusTone(status: string): BadgeTone {
  switch (status) {
    case 'pending':
      return 'warn'
    case 'reconciled':
      return 'ok'
    case 'cancelled':
    case 'ignored':
      return 'muted'
    default:
      return 'muted'
  }
}

/** 对账状态 → 中文标签。 */
export function statusLabel(status: string): string {
  switch (status) {
    case 'pending':
      return '待处置'
    case 'reconciled':
      return '已对账'
    case 'cancelled':
      return '已取消'
    case 'ignored':
      return '已忽略'
    default:
      return status || '—'
  }
}

/** 终态选项的中文标签(给下拉用)。 */
export function reconcileStatusLabel(status: ReconcileStatus): string {
  switch (status) {
    case 'reconciled':
      return '对账(可追扣)'
    case 'cancelled':
      return '取消(不追扣)'
    case 'ignored':
      return '忽略(不追扣)'
  }
}

/**
 * 金额(分)→ 美元展示。后端金额单位是 cents(int64),这里换算成两位小数美元。
 * 判别核心:必须 /100(分→元),且负数/0 也要正确处理。非有限数回退 '—'。
 */
export function formatCents(cents: number): string {
  if (!Number.isFinite(cents)) return '—'
  return `$${(cents / 100).toFixed(2)}`
}

/**
 * 追扣结果码(back_charge_outcome)→ 中文解释。captured=真扣到;其余=未扣到、孤儿保持 pending
 * (镜像后端 reconcile.go:38 的注释与 store_orphan_backcharge.go:24 的码集)。
 */
export function outcomeLabel(outcome?: string): string {
  switch (outcome) {
    case 'captured':
      return '已追扣到余额'
    case 'hold_not_held':
      return '原预扣已结算/释放,未追扣(孤儿保持待处置)'
    case 'task_archived':
      return '原任务行已归档,未追扣(孤儿保持待处置)'
    case 'no_estimate':
      return '无有效预估额,未追扣(孤儿保持待处置)'
    case 'holdref_unparseable':
      return '预扣引用不可解析,未追扣(孤儿保持待处置)'
    case undefined:
    case '':
      return ''
    default:
      return outcome
  }
}

/**
 * 把一次对账响应归纳成一句给操作员看的中文结果。
 *   - 真追扣到钱:强调金额。
 *   - 追扣未发生(409 路径):提示孤儿保持 pending + 原因。
 *   - 仅标记/取消/忽略:说明已推进或已是终态(advanced=false 表示之前已是终态,幂等)。
 */
export function summarizeReconcile(resp: {
  advanced: boolean
  back_charged: boolean
  captured_cents: number
  status: string
  back_charge_outcome?: string
}): string {
  if (resp.back_charged && resp.captured_cents > 0) {
    return `已追扣 ${formatCents(resp.captured_cents)} 至用户余额,孤儿已对账。`
  }
  const outcome = resp.back_charge_outcome
  if (outcome && outcome !== 'captured' && outcome !== '') {
    // 请求了追扣但未扣到:后端把孤儿保持 pending(409 路径)。
    return `未追扣到费用:${outcomeLabel(outcome)}。`
  }
  if (!resp.advanced) {
    return '该孤儿此前已处于终态,本次无状态变化(幂等)。'
  }
  return `已将孤儿标记为「${statusLabel(resp.status)}」(未追扣)。`
}
