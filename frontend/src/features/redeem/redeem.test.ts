import { describe, expect, it } from 'vitest'
import {
  buildRedeemRequest,
  formatMoney,
  friendlyRedeemError,
  isRateLimited,
  newIdempotencyKey,
  normalizeCode,
  summarizeRedeem,
  validateCode,
} from './redeem'
import type { RedeemResult } from './types'

describe('normalizeCode', () => {
  it('去空白 + 转大写', () => {
    // 判别核心:内部空白也要去掉(粘贴常带换行)。变异成只 trim → RED。
    expect(normalizeCode('  ab cd\n ef ')).toBe('ABCDEF')
  })
})

describe('validateCode', () => {
  it('空 → 提示填写', () => {
    expect(validateCode('   ')).toContain('输入')
  })
  it('超长 → 提示过长', () => {
    expect(validateCode('x'.repeat(65))).toContain('超过')
  })
  it('合法 → null', () => {
    expect(validateCode('VOUCHER-2026')).toBeNull()
  })
})

describe('newIdempotencyKey', () => {
  it('两次生成不相等且非空', () => {
    const a = newIdempotencyKey()
    const b = newIdempotencyKey()
    expect(a).toBeTruthy()
    expect(a).not.toBe(b)
  })
})

describe('buildRedeemRequest', () => {
  it('带规范化 code + 复用传入的 idempotency_key', () => {
    const req = buildRedeemRequest(' my code ', 'fixed-key')
    expect(req.code).toBe('MYCODE')
    // 判别核心:必须复用传入的 key(变异成每次新生成 → 重复提交防护失效 → RED)。
    expect(req.idempotency_key).toBe('fixed-key')
  })
  it('未传 key 时自动生成非空 key', () => {
    const req = buildRedeemRequest('abc')
    expect(req.idempotency_key).toBeTruthy()
  })
})

describe('formatMoney', () => {
  it('分 → 两位小数(除以 100)', () => {
    // 判别核心:1000 分 = 10.00。变异成除 1 或除 1000 → RED。
    expect(formatMoney(1000, 'USD')).toBe('$10.00')
  })
  it('CNY 用 ¥ 符号', () => {
    expect(formatMoney(550, 'CNY')).toBe('¥5.50')
  })
  it('未知币种回退为「数额 币种」', () => {
    expect(formatMoney(1234, 'JPY')).toBe('12.34 JPY')
  })
})

const balanceResult: RedeemResult = {
  voucher: { id: 1, amount_cents: 1000, currency_code: 'USD', status: 'active' },
  redemption: { voucher_id: 1, amount_cents: 1000, currency_code: 'USD', redeemed_at: '2026-06-25T00:00:00Z' },
  balance_cents: 3000,
  idempotent: false,
}

describe('summarizeRedeem', () => {
  it('余额券成功:报到账金额 + 当前余额', () => {
    const s = summarizeRedeem(balanceResult)
    expect(s).toContain('$10.00')
    expect(s).toContain('$30.00')
  })
  it('幂等命中:强调未重复入账', () => {
    // 判别核心:idempotent=true 不能说「到账」(会误导用户)。变异成忽略 idempotent → RED。
    const s = summarizeRedeem({ ...balanceResult, idempotent: true })
    expect(s).toContain('未重复入账')
    expect(s).not.toContain('到账')
  })
  it('订阅券:报续期/开通天数', () => {
    const s = summarizeRedeem({
      ...balanceResult,
      subscription: {
        user_subscription_id: 9,
        plan_id: 2,
        result_kind: 'renewed',
        new_expires_at: '2026-07-25T00:00:00Z',
        applied_validity_days: 30,
      },
    })
    expect(s).toContain('续期')
    expect(s).toContain('30')
  })
})

describe('friendlyRedeemError', () => {
  it('限流码 → 含「稍后」的软提示', () => {
    // 判别核心:限流必须给等待语气文案, 不能原样回显错误码。变异成返回 code → RED。
    const msg = friendlyRedeemError('voucher_attempt_limited')
    expect(msg).toContain('稍后')
    expect(msg).not.toContain('voucher_attempt_limited')
  })
  it('未知码回退到 fallbackMessage', () => {
    expect(friendlyRedeemError('weird_code', '原始消息')).toBe('原始消息')
  })
  it('无效码有专属文案', () => {
    expect(friendlyRedeemError('voucher_not_found')).toContain('无效')
  })
})

describe('isRateLimited', () => {
  it('仅限流码为 true', () => {
    expect(isRateLimited('voucher_attempt_limited')).toBe(true)
    expect(isRateLimited('voucher_not_found')).toBe(false)
  })
})
