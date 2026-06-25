import { describe, expect, it } from 'vitest'
import {
  buildResetEmailRequest,
  normalizeEmail,
  parseTenantId,
  validateForgotForm,
} from './forgot'

describe('normalizeEmail', () => {
  it('trim + 转小写', () => {
    // 判别核心:大小写与首尾空白都被归一。变异(漏 toLowerCase 或漏 trim)→ RED。
    expect(normalizeEmail('  User@Example.COM ')).toBe('user@example.com')
  })
})

describe('parseTenantId', () => {
  it('正整数原样返回', () => {
    expect(parseTenantId(' 7 ')).toBe(7)
  })
  it('0/负数/小数/非数字 → 0', () => {
    // 判别核心:非正整数一律 0。变异(放过 0 或负数)→ RED。
    expect(parseTenantId('0')).toBe(0)
    expect(parseTenantId('-3')).toBe(0)
    expect(parseTenantId('1.5')).toBe(0)
    expect(parseTenantId('abc')).toBe(0)
  })
})

describe('validateForgotForm', () => {
  it('邮箱为空 → 提示填写', () => {
    expect(validateForgotForm('   ', '1')).toContain('填写邮箱')
  })
  it('邮箱形态非法 → 格式错误', () => {
    // 判别核心:无 @ 或无域名点都判非法。变异(放行裸字符串)→ RED。
    expect(validateForgotForm('not-an-email', '1')).toContain('格式')
    expect(validateForgotForm('a@b', '1')).toContain('格式')
  })
  it('租户非正整数 → 提示', () => {
    expect(validateForgotForm('a@b.com', '0')).toContain('租户')
  })
  it('齐全通过 → null', () => {
    expect(validateForgotForm('a@b.com', '1')).toBeNull()
  })
})

describe('buildResetEmailRequest', () => {
  it('email 归一化 + tenant 解析,默认不带 captcha', () => {
    const req = buildResetEmailRequest('  A@B.COM ', ' 2 ')
    expect(req).toEqual({ tenant_id: 2, email: 'a@b.com' })
    // 判别核心:无 captcha 时字段必须缺席,不能是空串。变异(恒带 captcha_token)→ RED。
    expect('captcha_token' in req).toBe(false)
  })
  it('captcha 非空才带,空白被丢弃', () => {
    expect(buildResetEmailRequest('a@b.com', '1', '  ').captcha_token).toBeUndefined()
    expect(buildResetEmailRequest('a@b.com', '1', 'tok-123').captcha_token).toBe('tok-123')
  })
})
