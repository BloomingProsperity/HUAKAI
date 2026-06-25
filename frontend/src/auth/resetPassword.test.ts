import { describe, expect, it } from 'vitest'
import {
  MIN_PASSWORD_LENGTH,
  buildResetPayload,
  checkNewPassword,
  hasResetToken,
  isTokenInvalidError,
  parseResetLink,
  passwordCheckMessage,
} from './resetPassword'

describe('parseResetLink', () => {
  it('解析 token / email / tenant_id', () => {
    const p = parseResetLink('?email=u%40x.test&token=tok-abc&tenant_id=7')
    expect(p.email).toBe('u@x.test')
    expect(p.token).toBe('tok-abc')
    expect(p.tenantId).toBe(7)
  })

  it('tenant 缺省回落 1,且兼容 tenant 键名', () => {
    expect(parseResetLink('?token=t').tenantId).toBe(1)
    // 判别核心:非正整数 tenant 必须回落 1(否则会把 0/NaN 发给后端致 invalid_auth_request)。
    // 变异(去掉正整数守卫,直接用 Number 结果)→ 这里得到 0,本断言 RED。
    expect(parseResetLink('?token=t&tenant=0').tenantId).toBe(1)
    expect(parseResetLink('?token=t&tenant=3').tenantId).toBe(3)
  })

  it('token/email 去首尾空白', () => {
    const p = parseResetLink('?token=%20tok%20&email=%20a%40b%20')
    expect(p.token).toBe('tok')
    expect(p.email).toBe('a@b')
  })
})

describe('hasResetToken', () => {
  it('有 token 为真,无 token 为假(链接无效判定)', () => {
    // 判别核心:无 token 必须判为无效。变异(恒返回 true)→ 缺 token 的链接被当有效,本断言 RED。
    expect(hasResetToken(parseResetLink('?token=x'))).toBe(true)
    expect(hasResetToken(parseResetLink('?email=a@b'))).toBe(false)
  })
})

describe('checkNewPassword', () => {
  it('空密码 → empty(不报红,交按钮禁用)', () => {
    expect(checkNewPassword('', '')).toEqual({ ok: false, reason: 'empty' })
  })

  it('短于下限 → too_short', () => {
    // 判别核心:长度边界。变异(用 <= 或去掉长度检查)→ 恰好 7 位会通过,本断言 RED。
    expect(checkNewPassword('a'.repeat(MIN_PASSWORD_LENGTH - 1), 'a'.repeat(MIN_PASSWORD_LENGTH - 1))).toEqual({
      ok: false,
      reason: 'too_short',
    })
  })

  it('达到下限且一致 → ok', () => {
    const pw = 'a'.repeat(MIN_PASSWORD_LENGTH)
    expect(checkNewPassword(pw, pw)).toEqual({ ok: true })
  })

  it('两次不一致 → mismatch', () => {
    // 判别核心:两次一致校验。变异(去掉 !== 比较)→ 不一致也通过,本断言 RED。
    expect(checkNewPassword('abcdefgh', 'abcdefgX')).toEqual({ ok: false, reason: 'mismatch' })
  })

  it('优先级:既空又不一致先报 empty', () => {
    expect(checkNewPassword('', 'x')).toEqual({ ok: false, reason: 'empty' })
  })
})

describe('passwordCheckMessage', () => {
  it('ok 无消息;空态无消息;太短/不一致出中文', () => {
    expect(passwordCheckMessage({ ok: true })).toBeNull()
    expect(passwordCheckMessage({ ok: false, reason: 'empty' })).toBeNull()
    expect(passwordCheckMessage({ ok: false, reason: 'too_short' })).toContain(String(MIN_PASSWORD_LENGTH))
    expect(passwordCheckMessage({ ok: false, reason: 'mismatch' })).toBe('两次输入的密码不一致')
  })
})

describe('buildResetPayload', () => {
  it('请求体字段名对齐后端 struct(snake_case),不含 email', () => {
    const params = parseResetLink('?email=a@b&token=tok-1&tenant_id=2')
    const payload = buildResetPayload(params, 'newpass12')
    // 判别核心:字段名/取值。变异(把 new_password 写成别的字段,或漏带 token)→ 后端拿不到,本断言 RED。
    expect(payload).toEqual({ tenant_id: 2, token: 'tok-1', new_password: 'newpass12' })
    expect('email' in payload).toBe(false)
  })
})

describe('isTokenInvalidError', () => {
  it('auth_token_invalid 或 410 判为链接失效,其它为否', () => {
    // 判别核心:专属错误码识别。变异(恒 false)→ token 失效无法切专门文案,本断言 RED。
    expect(isTokenInvalidError('auth_token_invalid', 400)).toBe(true)
    expect(isTokenInvalidError('anything', 410)).toBe(true)
    expect(isTokenInvalidError('invalid_auth_request', 400)).toBe(false)
    expect(isTokenInvalidError('auth_backend_error', 503)).toBe(false)
  })
})
