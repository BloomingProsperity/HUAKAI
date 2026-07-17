import { describe, expect, it } from 'vitest'
import {
  buildInviteLink,
  formatCents,
  formatUsd,
  mapReferralRows,
  mapRewardRows,
  referralStatusLabel,
  referralStatusTone,
  refereeDisplay,
  validateMintForm,
} from './affiliate'

describe('buildInviteLink', () => {
  it('拼出 register?invite= 链接,去尾斜杠 + 编码 code', () => {
    // 判别核心:必须落在 /register?invite= 且 code 被 encodeURIComponent。
    // 变异(改成不编码 / 换 query key)→ RED。
    expect(buildInviteLink('https://hk.example.com', 'ABC123')).toBe('https://hk.example.com/register?invite=ABC123')
    expect(buildInviteLink('https://hk.example.com/', 'ABC123')).toBe('https://hk.example.com/register?invite=ABC123')
    expect(buildInviteLink('https://hk.example.com', 'a b/c')).toBe('https://hk.example.com/register?invite=a%20b%2Fc')
  })

  it('空 code → 空串(调用方隐藏复制)', () => {
    expect(buildInviteLink('https://hk.example.com', '')).toBe('')
    expect(buildInviteLink('https://hk.example.com', '   ')).toBe('')
  })
})

describe('formatCents', () => {
  it('分 → ¥ 两位小数', () => {
    // 判别核心:1 分 = 0.01,除以 100。变异(除以 10 或 1000)→ RED。
    expect(formatCents(0)).toBe('¥0.00')
    expect(formatCents(1)).toBe('¥0.01')
    expect(formatCents(12345)).toBe('¥123.45')
  })

  it('负数 / 非有限 → ¥0.00 回退', () => {
    expect(formatCents(-5)).toBe('¥0.00')
    expect(formatCents(Number.NaN)).toBe('¥0.00')
  })
})

describe('formatUsd', () => {
  it('decimal 字符串 → $ 两位小数', () => {
    expect(formatUsd('0')).toBe('$0.00')
    expect(formatUsd('1.5')).toBe('$1.50')
    expect(formatUsd('12.005')).toBe('$12.01')
  })

  it('不可解析 → $0.00', () => {
    expect(formatUsd('')).toBe('$0.00')
    expect(formatUsd('abc')).toBe('$0.00')
  })
})

describe('referralStatusLabel', () => {
  it('四态中文标签,未知原样', () => {
    expect(referralStatusLabel('pending')).toBe('待合格')
    expect(referralStatusLabel('qualified')).toBe('已合格')
    expect(referralStatusLabel('rewarded')).toBe('已返利')
    expect(referralStatusLabel('rejected')).toBe('已驳回')
    expect(referralStatusLabel('weird')).toBe('weird')
  })
})

describe('referralStatusTone', () => {
  it('rewarded=ok,rejected=danger,其余各有语气', () => {
    // 判别核心:rewarded 必须是 ok(成功绿)。变异(rewarded→muted/danger)→ RED。
    expect(referralStatusTone('rewarded')).toBe('ok')
    expect(referralStatusTone('qualified')).toBe('info')
    expect(referralStatusTone('pending')).toBe('warn')
    expect(referralStatusTone('rejected')).toBe('danger')
    expect(referralStatusTone('unknown')).toBe('muted')
  })
})

describe('refereeDisplay', () => {
  it('脱敏成 用户 #id', () => {
    expect(refereeDisplay(42)).toBe('用户 #42')
  })
})

describe('推广列表纯映射', () => {
  it('被邀请人四列完整映射且未返利明确显示占位符', () => {
    const createdAt = '2026-07-01T08:00:00Z'
    // 判别核心:用户脱敏、状态标签/语气、邀请时间和空返利时间均不可漏列。
    expect(mapReferralRows([{
      referral_id: 19,
      referee_user_id: 42,
      status: 'qualified',
      created_at: createdAt,
      rewarded_at: null,
    }])).toEqual([{
      id: 19,
      referee: '用户 #42',
      statusLabel: '已合格',
      statusTone: 'info',
      invitedAt: new Date(createdAt).toLocaleString('zh-CN', { hour12: false }),
      rewardedAt: '—',
    }])
  })

  it('返利流水四列完整映射且重复关联行键仍唯一', () => {
    const createdAt = '2026-07-02T09:00:00Z'
    const rows = mapRewardRows([
      { referral_id: 5, reward_type: 'qualified', amount_usd: '1.5', created_at: createdAt },
      { referral_id: 5, reward_type: 'bonus', amount_usd: '2', created_at: createdAt },
    ])
    // 判别核心:金额必须使用 USD 两位格式，不能误用汇总金额或丢失第二条同关联记录。
    expect(rows).toEqual([
      { id: '5-0', referral: '#5', type: 'qualified', amount: '$1.50', createdAt: new Date(createdAt).toLocaleString('zh-CN', { hour12: false }) },
      { id: '5-1', referral: '#5', type: 'bonus', amount: '$2.00', createdAt: new Date(createdAt).toLocaleString('zh-CN', { hour12: false }) },
    ])
  })
})

describe('validateMintForm', () => {
  it('合法值通过并规范化为整数', () => {
    const v = validateMintForm('5', '30')
    expect(v.ok).toBe(true)
    expect(v.maxUsage).toBe(5)
    expect(v.expiresInDays).toBe(30)
    expect(v.error).toBe('')
  })

  it('两端边界都通过(镜像后端闭区间 [1,100] / [1,90])', () => {
    // 判别核心:1/1 与 100/90 必须通过。变异(把 <= 写成 <、或上界改成 99/89)→ RED。
    expect(validateMintForm('1', '1').ok).toBe(true)
    expect(validateMintForm('100', '90').ok).toBe(true)
  })

  it('max_usage 越上界(101)被拒,ok=false 且不返回数值', () => {
    // 判别核心:101 必须 RED。变异(上界放宽到 101 / 去掉上界检查)→ 此断言 RED。
    const v = validateMintForm('101', '30')
    expect(v.ok).toBe(false)
    expect(v.maxUsage).toBe(0)
    expect(v.error).toContain('使用次数')
  })

  it('max_usage 越下界(0)被拒', () => {
    expect(validateMintForm('0', '30').ok).toBe(false)
  })

  it('expires_in_days 越上界(91)被拒', () => {
    // 判别核心:91 必须 RED。变异(上界放宽到 91)→ RED。
    const v = validateMintForm('5', '91')
    expect(v.ok).toBe(false)
    expect(v.error).toContain('有效天数')
  })

  it('expires_in_days 越下界(0)被拒', () => {
    expect(validateMintForm('5', '0').ok).toBe(false)
  })

  it('小数被拒(必须整数)', () => {
    // 判别核心:"1.5" 不是整数。变异(用 parseFloat 接受小数)→ RED。
    expect(validateMintForm('1.5', '30').ok).toBe(false)
    expect(validateMintForm('5', '30.5').ok).toBe(false)
  })

  it('空串 / 非数字被拒', () => {
    expect(validateMintForm('', '30').ok).toBe(false)
    expect(validateMintForm('abc', '30').ok).toBe(false)
    expect(validateMintForm('5', '').ok).toBe(false)
  })

  it('前后空白被容忍并裁剪', () => {
    expect(validateMintForm(' 5 ', ' 30 ').ok).toBe(true)
  })
})
