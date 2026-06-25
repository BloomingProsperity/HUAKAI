import { describe, expect, it } from 'vitest'
import {
  bindingsWithout,
  buildChangePassword,
  EMPTY_CHANGE_PASSWORD,
  isValidTotpCode,
  passkeyLabel,
  providerLabel,
  validateDisplayName,
  viewTwoFA,
  type ChangePasswordForm,
} from './profile'
import type { OAuthBinding, PasskeyItem, TwoFAStatus } from './types'

function pwForm(over: Partial<ChangePasswordForm>): ChangePasswordForm {
  return { ...EMPTY_CHANGE_PASSWORD, ...over }
}

describe('buildChangePassword', () => {
  it('新密空 / 过短 / 不一致 / 与旧密相同 各报错', () => {
    // 判别核心:新密为空必须报错(对齐后端早返 400)。变异(放行空)→ 此断言 RED。
    expect(buildChangePassword(pwForm({ newPassword: '   ', confirmPassword: '   ' }))).toEqual({ error: '请填写新密码' })
    expect(buildChangePassword(pwForm({ newPassword: 'abc', confirmPassword: 'abc' }))).toEqual({ error: '新密码至少 8 位' })
    expect(buildChangePassword(pwForm({ newPassword: 'abcd1234', confirmPassword: 'xxxx0000' }))).toEqual({ error: '两次输入的新密码不一致' })
    expect(buildChangePassword(pwForm({ oldPassword: 'samepass1', newPassword: 'samepass1', confirmPassword: 'samepass1' }))).toEqual({ error: '新密码不能与旧密码相同' })
  })

  it('齐全 → 正确请求体(透传旧密、新密)', () => {
    const r = buildChangePassword(pwForm({ oldPassword: 'oldpass99', newPassword: 'newpass88', confirmPassword: 'newpass88' }))
    expect(r).toEqual({ old_password: 'oldpass99', new_password: 'newpass88' })
  })
})

describe('validateDisplayName', () => {
  it('空 / 过长 / 含控制符 报错;正常去空白', () => {
    expect(validateDisplayName('   ')).toEqual({ error: '显示名不能为空' })
    expect(validateDisplayName('x'.repeat(101))).toEqual({ error: '显示名最多 100 个字符' })
    // 判别核心:含控制字符(0x07 响铃)必须被拒。变异(去掉控制符校验)→ 此断言 RED。
    expect(validateDisplayName('坏' + String.fromCharCode(7) + '名')).toEqual({ error: '显示名不能包含控制字符' })
    expect(validateDisplayName('  小开  ')).toEqual({ ok: true, value: '小开' })
  })
})

describe('isValidTotpCode', () => {
  it('仅 6 位数字合法', () => {
    // 判别核心:必须恰好 6 位数字。变异(改成 .length>0)→ '12' 通过 → RED。
    expect(isValidTotpCode('123456')).toBe(true)
    expect(isValidTotpCode(' 654321 ')).toBe(true)
    expect(isValidTotpCode('12')).toBe(false)
    expect(isValidTotpCode('1234567')).toBe(false)
    expect(isValidTotpCode('abcdef')).toBe(false)
  })
})

describe('viewTwoFA', () => {
  const base: TwoFAStatus = { available: true, enabled: false, backup_codes_remaining: 0 }

  it('平台关 → 平台未启用(muted)', () => {
    expect(viewTwoFA({ ...base, available: false }).label).toBe('平台未启用')
    expect(viewTwoFA({ ...base, available: false }).tone).toBe('muted')
  })

  it('平台开 + 本人开 → 已启用;备用码<3 标低', () => {
    // 判别核心:enabled=true 必须映射成「已启用」。变异(恒返回未启用)→ RED。
    const v = viewTwoFA({ available: true, enabled: true, backup_codes_remaining: 2 })
    expect(v.label).toBe('已启用')
    expect(v.tone).toBe('ok')
    expect(v.lowBackupCodes).toBe(true)
    expect(viewTwoFA({ available: true, enabled: true, backup_codes_remaining: 5 }).lowBackupCodes).toBe(false)
  })

  it('平台开 + 本人未开 → 未启用(warn)', () => {
    const v = viewTwoFA({ ...base, enabled: false })
    expect(v.label).toBe('未启用')
    expect(v.tone).toBe('warn')
  })
})

describe('passkeyLabel', () => {
  const p = (over: Partial<PasskeyItem>): PasskeyItem => ({ id: 7, clone_warning: false, sign_count: 0, created_at: '2026-01-01T00:00:00Z', ...over })
  it('有名用名,无名兜底为「通行密钥 #id」', () => {
    expect(passkeyLabel(p({ name: 'MacBook' }))).toBe('MacBook')
    expect(passkeyLabel(p({ name: '   ' }))).toBe('通行密钥 #7')
    expect(passkeyLabel(p({}))).toBe('通行密钥 #7')
  })
})

describe('providerLabel', () => {
  it('已知 provider 中文/品牌名,未知原样', () => {
    expect(providerLabel('github')).toBe('GitHub')
    expect(providerLabel('GOOGLE')).toBe('Google')
    expect(providerLabel('weird')).toBe('weird')
  })
})

describe('bindingsWithout', () => {
  const mk = (provider: string): OAuthBinding => ({ provider, subject: '***', linked_at: 'now' })
  it('剔除指定 provider(大小写无关)', () => {
    const list = [mk('github'), mk('google')]
    // 判别核心:剔除必须大小写无关。变异(改成全等比较)→ 'GitHub' 不被剔 → RED。
    expect(bindingsWithout(list, 'GitHub').map((b) => b.provider)).toEqual(['google'])
    expect(bindingsWithout(list, 'telegram')).toHaveLength(2)
  })
})
