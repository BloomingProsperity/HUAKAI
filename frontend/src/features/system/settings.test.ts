import { describe, expect, it } from 'vitest'
import { buildSettingUpdate, displayValue, isReadOnly, isSecretKey } from './settings'
import type { PlatformSetting } from './types'

function setting(over: Partial<PlatformSetting>): PlatformSetting {
  return { key: 'k', value: null, source: 'db', ...over }
}

describe('isSecretKey / displayValue', () => {
  it('密钥类(value_configured 出现)绝不显明文,只显已配置/未配置', () => {
    // 判别核心:密钥类必须走 value_configured 文案,不能回显 value。
    // 变异(isSecretKey 恒 false)→ displayValue 会尝试显 value→本断言 RED。
    const secret = setting({ value: null, value_configured: true })
    expect(isSecretKey(secret)).toBe(true)
    expect(displayValue(secret)).toBe('已配置')
    expect(displayValue(setting({ value_configured: false }))).toBe('未配置')
  })

  it('普通键(无 value_configured)显原值', () => {
    expect(isSecretKey(setting({ value: 'on' }))).toBe(false)
    expect(displayValue(setting({ value: 'on' }))).toBe('on')
    expect(displayValue(setting({ value: null }))).toBe('(空)')
  })
})

describe('buildSettingUpdate', () => {
  it('env 来源只读 → 报错', () => {
    expect(buildSettingUpdate(setting({ source: 'env', value: 'x' }), 'y', '')).toEqual({ error: '该项来自环境变量,只读不可改' })
    expect(isReadOnly(setting({ source: 'env' }))).toBe(true)
  })

  it('密钥类空输入 → noop(不空串覆盖已配置密钥)', () => {
    // 判别核心:密钥空输入禁止下发空串。变异(去掉 noop 分支)→ 会下发 value:''→本断言 RED。
    expect(buildSettingUpdate(setting({ value_configured: true }), '   ', '')).toEqual({ noop: true })
  })

  it('密钥类有输入 → 正常下发', () => {
    expect(buildSettingUpdate(setting({ value_configured: false }), 'new-secret', '轮换')).toEqual({ value: 'new-secret', reason: '轮换' })
  })

  it('普通键允许设空串,reason 空白省略', () => {
    expect(buildSettingUpdate(setting({ value: 'on' }), '', '')).toEqual({ value: '' })
  })
})
