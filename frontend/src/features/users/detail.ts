/*
 * 用户详情 / 余额历史纯逻辑(可单测)。余额变动方向由 amount 字符串符号判定(贷记/借记),
 * 用于台账配色与符号展示。纯只读金额展示,不做任何金额变更。
 */
export interface UserDetail {
  id: number
  email: string
  role: string
  status: string
  user_group: string
  remark: string
  balance: string
  created_at: string
}

export interface BalanceHistoryEntry {
  id: number
  event_type: string
  amount: string
  fingerprint: string
  source_type: string
  source_id: number
  occurred_at: string
}

export interface BalanceHistoryResponse {
  items: BalanceHistoryEntry[]
  limit: number
  offset: number
}

export interface UserUsageTokens {
  input: number
  output: number
  cache_creation?: number
  cache_read?: number
}

export interface UserUsageVerifyHint {
  ledger_id?: string
  trust_verify_path: string
  trust_verify_method: string
  audit_verify_path?: string
  audit_verify_method?: string
  request_id?: string
  tenant_scope_ref?: string
}

export interface UserUsageRecord {
  requested_model: string
  upstream_model: string
  actual_cost: string
  tokens: UserUsageTokens
  provider?: string
  provider_account_id?: number
  ledger_id: string
  verify_hint: UserUsageVerifyHint
  created_at: string
  status: string
  request_id?: string
  stream: boolean
  stream_terminated_reason?: string
  requested_at?: string
}

export interface UserUsageResponse {
  items: UserUsageRecord[]
  next_cursor: string
}

export interface UserUsageSummary {
  requestCount: number
  inputTokens: number
  outputTokens: number
  cacheCreationTokens: number
  cacheReadTokens: number
  successCount: number
  errorCount: number
  otherCount: number
  actualCost: string | null
}

export type BalanceDirection = 'credit' | 'debit' | 'zero'

/** 据金额字符串符号判方向:正=贷记(进账)、负=借记(出账)、零/非法=zero。 */
export function balanceDirection(amount: string): BalanceDirection {
  const n = Number(amount)
  if (!Number.isFinite(n) || n === 0) return 'zero'
  return n > 0 ? 'credit' : 'debit'
}

/** 带显式符号的金额展示:贷记加 "+",借记保留负号,零原样。 */
export function signedAmount(amount: string): string {
  const dir = balanceDirection(amount)
  const trimmed = amount.trim()
  if (dir === 'credit' && !trimmed.startsWith('+')) return `+${trimmed}`
  return trimmed
}

export function eventTypeLabel(eventType: string): string {
  switch (eventType) {
    case 'admin_credit':
      return '管理员充值'
    case 'admin_debit':
      return '管理员扣减'
    case 'usage_charge':
      return '用量扣费'
    case 'topup':
      return '充值'
    case 'refund':
      return '退款'
    default:
      return eventType
  }
}

/** 聚合当前接口批次；next_cursor 非空时调用方必须明确提示结果并非全量。 */
export function summarizeUserUsage(response: UserUsageResponse): UserUsageSummary {
  let inputTokens = 0
  let outputTokens = 0
  let cacheCreationTokens = 0
  let cacheReadTokens = 0
  let successCount = 0
  let errorCount = 0

  for (const item of response.items) {
    inputTokens += item.tokens.input
    outputTokens += item.tokens.output
    cacheCreationTokens += item.tokens.cache_creation ?? 0
    cacheReadTokens += item.tokens.cache_read ?? 0
    if (item.status === 'success') successCount += 1
    else if (item.status === 'error') errorCount += 1
  }

  return {
    requestCount: response.items.length,
    inputTokens,
    outputTokens,
    cacheCreationTokens,
    cacheReadTokens,
    successCount,
    errorCount,
    otherCount: response.items.length - successCount - errorCount,
    actualCost: sumFixed8(response.items.map((item) => item.actual_cost)),
  }
}

/** 金额保持 8 位定点精度；任一非法值都返回 null，避免静默少算。 */
export function sumFixed8(values: string[]): string | null {
  let total = 0n
  for (const raw of values) {
    const match = raw.trim().match(/^([+-]?)(\d+)(?:\.(\d{1,8}))?$/)
    if (!match) return null
    const fraction = (match[3] ?? '').padEnd(8, '0')
    const scaled = BigInt(match[2]) * 100_000_000n + BigInt(fraction || '0')
    total += match[1] === '-' ? -scaled : scaled
  }
  const sign = total < 0n ? '-' : ''
  const absolute = total < 0n ? -total : total
  const integer = absolute / 100_000_000n
  const fraction = String(absolute % 100_000_000n).padStart(8, '0')
  return `${sign}${integer}.${fraction}`
}
