import { describe, expect, it } from 'vitest'
import {
  buildInviteLink,
  formatCents,
  formatUsd,
  referralStatusLabel,
  referralStatusTone,
  refereeDisplay,
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
