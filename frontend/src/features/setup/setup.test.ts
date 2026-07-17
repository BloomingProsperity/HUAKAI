import { describe, expect, it } from 'vitest'
import { installErrorText, validateInstallForm } from './setup'

describe('validateInstallForm —— 与后端同口径的前置校验', () => {
  it('合法输入放行', () => {
    expect(validateInstallForm('boss@example.com', 'Str0ngPass!', 'Str0ngPass!')).toBeNull()
  })
  // 变异:砍掉任一分支 → 对应用例红。
  it('坏邮箱/短口令/超长口令/两次不一致逐一拦截', () => {
    expect(validateInstallForm('not-an-email', 'Str0ngPass!', 'Str0ngPass!')).toBe('请输入合法邮箱')
    expect(validateInstallForm('a@b.co', 'short', 'short')).toBe('密码至少 8 位')
    expect(validateInstallForm('a@b.co', 'x'.repeat(129), 'x'.repeat(129))).toBe('密码最长 128 位')
    expect(validateInstallForm('a@b.co', 'Str0ngPass!', 'Different1!')).toBe('两次输入的密码不一致')
  })
})

describe('installErrorText —— 后端错误码映射', () => {
  it('已装/邮箱占用给专属文案,未知码回退通用', () => {
    expect(installErrorText('already_installed')).toContain('已完成安装')
    expect(installErrorText('email_taken')).toContain('已被占用')
    expect(installErrorText('whatever')).toContain('安装失败')
  })
})
