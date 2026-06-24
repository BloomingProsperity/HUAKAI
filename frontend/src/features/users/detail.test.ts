import { describe, expect, it } from 'vitest'
import { balanceDirection, eventTypeLabel, signedAmount } from './detail'

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
