import type {
  ClaimFilters,
  RepriceForm,
  RepriceItem,
  RepriceRequest,
  UsageFilters,
} from './types'

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

// ── 按当前价表重算 ──────────────────────────────────────────────────────────

export const REPRICE_MAX_LIMIT = 100
export type RepriceIntent = 'preview' | 'apply'

function positiveSafeInt(raw: string): number | null {
  const value = raw.trim()
  if (!/^\d+$/.test(value)) return null
  const parsed = Number(value)
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null
}

/** 前端先挡住 handler 会拒绝的范围，并额外强制填写操作原因。 */
export function validateRepriceForm(form: RepriceForm): string | null {
  if (!form.reason.trim()) return '请填写重算原因'
  if (form.scope === 'record') {
    return positiveSafeInt(form.usageRecordId) === null ? '用量记录 ID 必须是正整数' : null
  }
  if (positiveSafeInt(form.tenantId) === null) return '租户 ID 必须是正整数'
  const from = toIso(form.from)
  const to = toIso(form.to)
  if (!from || !to) return '请选择有效的起止时间'
  if (Date.parse(from) >= Date.parse(to)) return '开始时间必须早于结束时间'
  const limit = positiveSafeInt(form.limit)
  if (limit === null || limit > REPRICE_MAX_LIMIT) return `记录上限必须是 1–${REPRICE_MAX_LIMIT} 的整数`
  return null
}

export function canStartReprice(form: RepriceForm): boolean {
  return form.acknowledged && validateRepriceForm(form) === null
}

/** 构造 handler 的真实 body；reason/acknowledged 只属于 UI 闸门，绝不下发假字段。 */
export function buildRepriceRequest(form: RepriceForm, dryRun: boolean): RepriceRequest {
  const invalid = validateRepriceForm(form)
  if (invalid) throw new Error(invalid)
  if (form.scope === 'record') {
    return { usage_record_id: positiveSafeInt(form.usageRecordId) as number, dry_run: dryRun }
  }
  return {
    tenant_id: positiveSafeInt(form.tenantId) as number,
    from: toIso(form.from),
    to: toIso(form.to),
    limit: positiveSafeInt(form.limit) as number,
    dry_run: dryRun,
  }
}

/** 发送层再次守闸，避免仅靠 disabled 属性保护触钱操作。 */
export async function executeRepriceGuarded<T>(
  form: RepriceForm,
  intent: RepriceIntent,
  confirmed: boolean,
  send: (request: RepriceRequest) => Promise<T>,
): Promise<T> {
  const invalid = validateRepriceForm(form)
  if (invalid) throw new Error(invalid)
  if (!form.acknowledged) throw new Error('请先勾选已了解重算会改写计费记录')
  if (intent === 'apply' && !confirmed) throw new Error('实际重算必须完成影响范围二次确认')
  return send(buildRepriceRequest(form, intent === 'preview'))
}

export function repriceScopeSummary(form: RepriceForm): string {
  if (form.scope === 'record') return `用量记录 #${form.usageRecordId.trim() || '—'}`
  return `租户 #${form.tenantId.trim() || '—'}，${form.from || '—'} 至 ${form.to || '—'}，最多 ${form.limit || '—'} 条`
}

function parseFixed8(value: string): bigint | null {
  const matched = value.trim().match(/^([+-]?)(\d+)(?:\.(\d{0,8}))?$/)
  if (!matched) return null
  const fraction = (matched[3] ?? '').padEnd(8, '0')
  const scaled = BigInt(matched[2]) * 100_000_000n + BigInt(fraction || '0')
  return matched[1] === '-' ? -scaled : scaled
}

function formatFixed8(value: bigint): string {
  const negative = value < 0n
  const absolute = negative ? -value : value
  const whole = absolute / 100_000_000n
  const fraction = String(absolute % 100_000_000n).padStart(8, '0')
  return `${negative ? '-' : ''}${whole}.${fraction}`
}

/** 后端逐条返回八位小数差额；用 BigInt 定点求和，避免浮点改钱。 */
export function sumRepriceCostDelta(items: Pick<RepriceItem, 'cost_delta'>[]): string | null {
  let total = 0n
  for (const item of items) {
    const value = parseFixed8(item.cost_delta)
    if (value === null) return null
    total += value
  }
  return formatFixed8(total)
}
