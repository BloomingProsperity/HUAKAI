import { describe, expect, it } from 'vitest'
import {
  buildAdjustmentRequest,
  directionLabel,
  MAX_ADJUST_AMOUNT_USD,
  newAdjustmentKey,
  validateAdjustment,
  validateAmount,
  validateReason,
} from './balance'

describe('validateAmount', () => {
  it('放行正的、≤2 位小数的金额', () => {
    expect(validateAmount('10')).toEqual({ ok: true, magnitude: '10' })
    expect(validateAmount('9.99')).toEqual({ ok: true, magnitude: '9.99' })
    expect(validateAmount(' 5.5 ')).toEqual({ ok: true, magnitude: '5.5' })
  })

  it('拒空/0/负数/非数字(变异:放行 0 或负数即 RED)', () => {
    expect(validateAmount('').ok).toBe(false)
    // 判别核心:0 必须拒(amount 非零是后端硬约束 handler.go:82)。
    expect(validateAmount('0').ok).toBe(false)
    expect(validateAmount('0.00').ok).toBe(false)
    // 判别核心:负号不能从金额框进入(方向由 direction 决定,不允许这里夹带符号)。
    expect(validateAmount('-5').ok).toBe(false)
    expect(validateAmount('abc').ok).toBe(false)
    expect(validateAmount('1e3').ok).toBe(false)
  })

  it('拒超过 2 位小数(变异:放宽到 3 位即 RED)', () => {
    // 判别核心:美分粒度,第 3 位小数后端会判 invalid(order.go:146)。
    expect(validateAmount('1.234').ok).toBe(false)
    expect(validateAmount('1.23').ok).toBe(true)
  })

  it('拒超过上限(变异:去掉上限校验即 RED)', () => {
    expect(validateAmount(String(MAX_ADJUST_AMOUNT_USD)).ok).toBe(true)
    expect(validateAmount(String(MAX_ADJUST_AMOUNT_USD + 1)).ok).toBe(false)
  })
})

describe('validateReason', () => {
  it('非空通过,空/纯空白拒(变异:不校验空即 RED)', () => {
    expect(validateReason('客服补偿')).toBeNull()
    // 判别核心:reason 是审计必填字段,空必须拒(handler.go:82)。
    expect(validateReason('')).not.toBeNull()
    expect(validateReason('   ')).not.toBeNull()
  })
})

describe('directionLabel', () => {
  it('credit→加款,debit→扣款', () => {
    expect(directionLabel('credit')).toBe('加款')
    expect(directionLabel('debit')).toBe('扣款')
  })
})

describe('validateAdjustment', () => {
  it('加款保持正号,扣款加负号(符号即方向)', () => {
    const credit = validateAdjustment(1, 7, 'credit', '10.00', '补偿')
    expect(credit).toEqual({ ok: true, signedAmount: '10.00', magnitude: '10.00' })
    // 判别核心:扣款必须产出负号金额,否则后端会当成加款 → 方向反了。
    // 变异(debit 不加负号)→ signedAmount 变成 '10.00' → RED。
    const debit = validateAdjustment(1, 7, 'debit', '10.00', '误扣回退')
    expect(debit).toEqual({ ok: true, signedAmount: '-10.00', magnitude: '10.00' })
  })

  it('tenant_id / user_id 必须为正(变异:放行 <=0 即 RED)', () => {
    expect(validateAdjustment(0, 7, 'credit', '10', '补偿').ok).toBe(false)
    expect(validateAdjustment(-1, 7, 'credit', '10', '补偿').ok).toBe(false)
    expect(validateAdjustment(1, 0, 'credit', '10', '补偿').ok).toBe(false)
    expect(validateAdjustment(1.5, 7, 'credit', '10', '补偿').ok).toBe(false)
  })

  it('金额非法 / 原因为空都拒', () => {
    expect(validateAdjustment(1, 7, 'credit', '0', '补偿').ok).toBe(false)
    expect(validateAdjustment(1, 7, 'credit', '10', '   ').ok).toBe(false)
  })
})

describe('buildAdjustmentRequest', () => {
  it('带上带符号金额 + 复用幂等键 + trim 原因', () => {
    const body = buildAdjustmentRequest(2, 9, '-3.50', '  误扣回退  ', 'fixed-key-1')
    expect(body).toEqual({
      tenant_id: 2,
      user_id: 9,
      amount: '-3.50',
      reason: '误扣回退',
      idempotency_key: 'fixed-key-1',
    })
  })

  it('缺省幂等键时自动生成非空 key(变异:返回空 key 即 RED)', () => {
    const body = buildAdjustmentRequest(1, 1, '5', '补偿')
    // 判别核心:幂等键不能为空,否则重复提交无法被后端合并。
    expect(typeof body.idempotency_key).toBe('string')
    expect(body.idempotency_key.length).toBeGreaterThan(0)
  })
})

describe('newAdjustmentKey', () => {
  it('两次生成不相同(避免重复意图被错误合并)', () => {
    expect(newAdjustmentKey()).not.toBe(newAdjustmentKey())
  })
})
