import { describe, expect, it } from 'vitest'
import {
  buildOverviewQuery,
  buildReferralQuery,
  buildRewardsQuery,
  formatUsd,
  isReferralStatus,
  mapAffiliateStats,
  mapReferralTableRows,
  mapRewardTableRows,
  statusCount,
  statusLabel,
  statusTone,
  withTenantContext,
} from './affiliateadmin'
import { EMPTY_AFFILIATE_FILTERS, type AffiliateFilters } from './types'

function f(over: Partial<AffiliateFilters>): AffiliateFilters {
  return { ...EMPTY_AFFILIATE_FILTERS, ...over }
}

describe('buildReferralQuery', () => {
  it('空筛选 → 只带 limit/offset(不下发空 tenant_id/status)', () => {
    // 判别核心:空白字段必须省略。变异(改成无条件赋值)→ 会含 tenant_id:''→本断言 RED。
    expect(buildReferralQuery(EMPTY_AFFILIATE_FILTERS, 20, 0)).toEqual({ limit: 20, offset: 0 })
  })

  it('tenant_id trim 后下发;非法 status 被丢弃,合法 status 才带', () => {
    const q = buildReferralQuery(f({ tenantId: ' 7 ', status: 'rewarded' }), 50, 100)
    expect(q).toEqual({ limit: 50, offset: 100, tenant_id: '7', status: 'rewarded' })
  })

  it('非法 status 不下发(防 400)', () => {
    // 判别核心:status 必须经 isReferralStatus 把关。变异(去掉校验直接赋值)→ status:'bogus' 会出现→RED。
    const q = buildReferralQuery(f({ status: 'bogus' }), 20, 0)
    expect('status' in q).toBe(false)
  })
})

describe('buildRewardsQuery', () => {
  it('账本 query 不含 status,只透传 tenant_id + referrer_user_id', () => {
    const q = buildRewardsQuery(f({ tenantId: '3', status: 'rewarded' }), 20, 0, ' 42 ')
    expect(q).toEqual({ limit: 20, offset: 0, tenant_id: '3', referrer_user_id: '42' })
    expect('status' in q).toBe(false)
  })

  it('referrer 留空不带', () => {
    expect('referrer_user_id' in buildRewardsQuery(EMPTY_AFFILIATE_FILTERS, 20, 0, '')).toBe(false)
  })
})

describe('buildOverviewQuery', () => {
  it('仅 tenant_id;空则空对象', () => {
    expect(buildOverviewQuery(EMPTY_AFFILIATE_FILTERS)).toEqual({})
    expect(buildOverviewQuery(f({ tenantId: '9' }))).toEqual({ tenant_id: '9' })
  })
})

describe('isReferralStatus', () => {
  it('仅四个合法态返回 true', () => {
    expect(isReferralStatus('pending')).toBe(true)
    expect(isReferralStatus('qualified')).toBe(true)
    expect(isReferralStatus('rewarded')).toBe(true)
    expect(isReferralStatus('rejected')).toBe(true)
    expect(isReferralStatus('paid')).toBe(false)
    expect(isReferralStatus('')).toBe(false)
  })
})

describe('statusTone', () => {
  it('rewarded→ok,qualified→info,pending→warn,rejected→danger,其余→muted', () => {
    // 判别核心:rewarded 是落了钱的终态,必须 ok 绿;变异成别的语气→RED。
    expect(statusTone('rewarded')).toBe('ok')
    expect(statusTone('qualified')).toBe('info')
    expect(statusTone('pending')).toBe('warn')
    expect(statusTone('rejected')).toBe('danger')
    expect(statusTone('unknown')).toBe('muted')
  })
})

describe('statusLabel', () => {
  it('映射中文;未知原样', () => {
    expect(statusLabel('rewarded')).toBe('已返利')
    expect(statusLabel('weird')).toBe('weird')
  })
})

describe('formatUsd', () => {
  it('空/非数字 → 0;合法 decimal 原样保精度', () => {
    // 判别核心:绝不做浮点运算。变异(改成 String(Number(v)))会丢长尾精度→本断言 RED。
    expect(formatUsd('')).toBe('0')
    expect(formatUsd(null)).toBe('0')
    expect(formatUsd('abc')).toBe('0')
    expect(formatUsd(' 12.3400 ')).toBe('12.3400')
    expect(formatUsd('0.123456789012345678')).toBe('0.123456789012345678')
  })
})

describe('statusCount', () => {
  it('缺键/非有限 → 0;命中返回值', () => {
    expect(statusCount(null, 'pending')).toBe(0)
    expect(statusCount({}, 'rewarded')).toBe(0)
    expect(statusCount({ rewarded: 5 }, 'rewarded')).toBe(5)
    expect(statusCount({ pending: Number.NaN }, 'pending')).toBe(0)
  })
})

describe('租户上下文与展示列映射', () => {
  it('空租户采用 me 上下文，显式租户不被覆盖', () => {
    // 变异(忽略上下文)会让 platform_admin 首次请求缺 tenant_id 而使首断言 RED。
    expect(withTenantContext(EMPTY_AFFILIATE_FILTERS, 7)).toEqual({ tenantId: '7', status: '' })
    expect(withTenantContext(f({ tenantId: '9' }), 7).tenantId).toBe('9')
  })

  it('分销与返利列保持身份方向和 money 精度', () => {
    const [referral] = mapReferralTableRows([{ id: 1, referrer_user_id: 12, referee_user_id: 34, status: 'rewarded', created_at: 'invalid-time' }])
    expect(referral).toEqual({ id: 1, referrerUserId: '#12', refereeUserId: '#34', status: 'rewarded', createdAt: 'invalid-time' })
    const [reward] = mapRewardTableRows([{ id: 2, referral_id: 1, referrer_user_id: 12, reward_type: 'qualified', amount_usd: '0.123456789012345678', issued_at: 'invalid-time' }])
    // 变异(Number(amount_usd))会丢精度并打红。
    expect(reward.amountUsd).toBe('0.123456789012345678')
    expect(reward.referralId).toBe('#1')
  })

  it('概览指标固定映射累计、发放与四状态计数', () => {
    const stats = mapAffiliateStats({ object: 'overview', total_reward_usd: '12.3400', reward_count: 5, counts_by_status: { pending: 1, qualified: 2, rewarded: 3, rejected: 4 } })
    expect(stats.map((item) => item.value)).toEqual(['12.3400', '5', '1 / 2', '3 / 4'])
  })
})
