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
