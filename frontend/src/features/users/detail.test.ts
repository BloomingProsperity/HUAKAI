import { describe, expect, it } from 'vitest'
import {
  balanceDirection,
  eventTypeLabel,
  signedAmount,
  summarizeUserUsage,
  sumFixed8,
  type UserUsageRecord,
} from './detail'

describe('balanceDirection', () => {
  it('正=credit、负=debit、零/非法=zero', () => {
    // 判别核心:负数必须是 debit。变异(恒 credit)→ 借记断言 RED(台账配色会反)。
    expect(balanceDirection('5.00')).toBe('credit')
    expect(balanceDirection('-3.00')).toBe('debit')
    expect(balanceDirection('0')).toBe('zero')
    expect(balanceDirection('abc')).toBe('zero')
  })
})

describe('signedAmount', () => {
  it('贷记加 +,借记留负号,已带符号不重复', () => {
    expect(signedAmount('5.00')).toBe('+5.00')
    expect(signedAmount('-3.00')).toBe('-3.00')
    expect(signedAmount('+5.00')).toBe('+5.00')
    expect(signedAmount('0')).toBe('0')
  })
})

describe('eventTypeLabel', () => {
  it('已知类型给中文,未知原样', () => {
    expect(eventTypeLabel('admin_credit')).toBe('管理员充值')
    expect(eventTypeLabel('usage_charge')).toBe('用量扣费')
    expect(eventTypeLabel('weird_event')).toBe('weird_event')
  })
})

function usage(over: Partial<UserUsageRecord> = {}): UserUsageRecord {
  return {
    requested_model: 'm',
    upstream_model: 'u',
    actual_cost: '0.10000000',
    tokens: { input: 10, output: 4, cache_creation: 2, cache_read: 3 },
    ledger_id: 'l',
    verify_hint: { trust_verify_path: '/v1/trust/verify', trust_verify_method: 'POST' },
    created_at: '2026-07-12T00:00:00Z',
    status: 'success',
    stream: false,
    ...over,
  }
}

describe('用户用量聚合', () => {
  it('请求、四类 Token、状态与费用按当前批次准确求和', () => {
    const summary = summarizeUserUsage({
      items: [
        usage(),
        usage({ actual_cost: '0.20000000', tokens: { input: 20, output: 6 }, status: 'error' }),
        usage({ actual_cost: '1.00000000', tokens: { input: 1, output: 1 }, status: 'pending_reconciliation' }),
      ],
      next_cursor: 'next',
    })
    expect(summary).toEqual({
      requestCount: 3,
      inputTokens: 31,
      outputTokens: 11,
      cacheCreationTokens: 2,
      cacheReadTokens: 3,
      successCount: 1,
      errorCount: 1,
      otherCount: 1,
      actualCost: '1.30000000',
    })
  })

  it('费用使用 8 位定点运算；非法值不静默少算', () => {
    expect(sumFixed8(['0.10000000', '0.20000000'])).toBe('0.30000000')
    expect(sumFixed8(['999999999999.99999999', '0.00000001'])).toBe('1000000000000.00000000')
    expect(sumFixed8(['0.10000000', '坏值'])).toBeNull()
  })
})
