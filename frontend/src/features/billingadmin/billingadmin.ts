import type { ClaimFilters, UsageFilters } from './types'

/*
 * 计费运营观测纯逻辑(可单测):过滤条件 → query 参数构造、money 十进制字符串安全渲染、
 * 时间本地值 → RFC3339、claim 状态/信任态配色。
 *
 * 后端约束(parseObsQuery,admin_observability_handler.go:131):
 *   - limit ∈ [1,200];
 *   - pool_id / api_key_id / provider_account_id 须为正整数(parseIntFilter:非正即 400);
 *   - usage 的 outcome 须 ∈ {success,error,all};空串等价 all,故空时省略不下发;
 *   - 空白过滤字段一律省略(strPtr 把空串转 nil,但前端不下发空串更稳妥)。
 */
export type QueryValue = string | number | boolean | undefined

/** datetime-local(无时区)→ ISO8601;空串/非法 → 空串(调用方据此省略)。 */
export function toIso(local: string): string {
  const v = local.trim()
  if (!v) return ''
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? '' : d.toISOString()
}

/**
 * 把一个【正整数】过滤字段写入 query;非正/非法/空白一律省略。
 * 后端 parseIntFilter 对 <=0 或非数字直接 400,故前端先收敛,绝不下发非法值。
 */
function putPositiveInt(q: Record<string, QueryValue>, key: string, raw: string): void {
  const v = raw.trim()
  if (!v) return
  // 仅接受纯数字且 > 0;否则省略(避免把 "abc" / "-1" / "0" 下发触发 400)。
  if (!/^\d+$/.test(v)) return
  const n = Number(v)
  if (!Number.isFinite(n) || n <= 0) return
  q[key] = v
}

/** 写入一个非空字符串过滤字段(trim 后空则省略)。 */
function putStr(q: Record<string, QueryValue>, key: string, raw: string): void {
  const v = raw.trim()
  if (v) q[key] = v
}

/**
 * 据原始用量过滤条件构造 GET /admin/v1/usage 的 query。
 * 空白字段省略;outcome 仅 success/error 才下发(all/空 省略,后端默认即 all);
 * pending_only 仅 true 才下发 pending_reconciliation_only=true。
 */
export function buildUsageQuery(filters: UsageFilters, cursor?: string): Record<string, QueryValue> {
  const q: Record<string, QueryValue> = {}
  putStr(q, 'provider', filters.provider)
  putStr(q, 'model', filters.model)
  putPositiveInt(q, 'pool_id', filters.poolId)
  putPositiveInt(q, 'api_key_id', filters.apiKeyId)
  putPositiveInt(q, 'provider_account_id', filters.providerAccountId)
  const outcome = filters.outcome.trim()
  if (outcome === 'success' || outcome === 'error') {
    q.outcome = outcome
  }
  if (filters.pendingOnly) {
    q.pending_reconciliation_only = 'true'
  }
  const from = toIso(filters.from)
  if (from) q.from = from
  const to = toIso(filters.to)
  if (to) q.to = to
  if (cursor && cursor.trim()) q.cursor = cursor.trim()
  return q
}

/**
 * 据 claim 过滤条件构造 GET /admin/v1/billing/claims 的 query。
 * claim 多了 status 过滤(自由字符串,空则省略);无 outcome / pending 维度。
 */
export function buildClaimQuery(filters: ClaimFilters, cursor?: string): Record<string, QueryValue> {
  const q: Record<string, QueryValue> = {}
  putStr(q, 'status', filters.status)
  putStr(q, 'provider', filters.provider)
  putStr(q, 'model', filters.model)
  putPositiveInt(q, 'pool_id', filters.poolId)
  putPositiveInt(q, 'api_key_id', filters.apiKeyId)
  putPositiveInt(q, 'provider_account_id', filters.providerAccountId)
  const from = toIso(filters.from)
  if (from) q.from = from
  const to = toIso(filters.to)
  if (to) q.to = to
  if (cursor && cursor.trim()) q.cursor = cursor.trim()
  return q
}

/**
 * money 安全渲染:后端 decimal 序列化为十进制字符串,前端【原样】展示,绝不 parseFloat。
 * 仅做空值兜底(null/undefined/空串 → 占位符),并可选拼货币代码。不做任何数值运算。
 */
export function formatMoney(value: string | null | undefined, currency?: string | null): string {
  if (value == null) return '—'
  const v = value.trim()
  if (!v) return '—'
  const cur = (currency ?? '').trim()
  return cur ? `${v} ${cur}` : v
}

export type ClaimTone = 'ok' | 'info' | 'warn' | 'danger' | 'muted'

/**
 * claim 状态 → 配色档。settled/committed=已落定(ok),pending=进行中(info),
 * aborted/failed=异常(danger),其余中性。状态为后端自由字符串,这里做大小写不敏感归类。
 */
export function claimStatusTone(status: string): ClaimTone {
  switch (status.trim().toLowerCase()) {
    case 'settled':
    case 'committed':
      return 'ok'
    case 'pending':
    case 'reserved':
      return 'info'
    case 'aborted':
    case 'failed':
    case 'voided':
      return 'danger'
    default:
      return 'muted'
  }
}

/**
 * 信任态 → 配色档(usage.trust_status,trust.ResponseStatus)。
 * verified/ok=可信(ok),mismatch/tampered=危险,missing/unknown=中性。
 */
export function trustStatusTone(status: string): ClaimTone {
  switch (status.trim().toLowerCase()) {
    case 'verified':
    case 'ok':
    case 'trusted':
      return 'ok'
    case 'mismatch':
    case 'tampered':
    case 'invalid':
      return 'danger'
    case 'unverified':
    case 'degraded':
      return 'warn'
    default:
      return 'muted'
  }
}

/** 把 RFC3339 时间字符串转本地可读;空/null/非法 → 占位符。 */
export function formatTime(iso: string | null | undefined): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN', { hour12: false })
}

/** 长标识截断显示(request_id / idempotency_key 等),保留首尾便于辨识。 */
export function shortId(s: string): string {
  const v = (s ?? '').trim()
  if (!v) return '—'
  return v.length > 16 ? `${v.slice(0, 8)}…${v.slice(-4)}` : v
}
