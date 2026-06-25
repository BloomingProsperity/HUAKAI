import { describe, expect, it } from 'vitest'
import {
  buildSmtpTest,
  displayCommit,
  displayVersion,
  isDevBuild,
  isPlaceholder,
} from './version'
import type { BuildInfo } from './types'

describe('isPlaceholder', () => {
  it('dev/unknown/空 视为占位,真实值不视为占位', () => {
    // 判别核心:占位集合判定。变异(恒返回 false)→ 第一组断言 RED;变异(恒返回 true)→ 末断言 RED。
    expect(isPlaceholder('dev')).toBe(true)
    expect(isPlaceholder('UNKNOWN')).toBe(true)
    expect(isPlaceholder('  ')).toBe(true)
    expect(isPlaceholder('v1.2.3')).toBe(false)
  })
})

describe('displayVersion', () => {
  it('占位→开发构建,真实版本原样', () => {
    expect(displayVersion('dev')).toBe('开发构建')
    expect(displayVersion('v2.0.1')).toBe('v2.0.1')
  })
})

describe('displayCommit', () => {
  it('真实哈希截前 12 位,占位→未知', () => {
    // 判别核心:截断长度。变异(slice(0,8))→ 本断言 RED。
    expect(displayCommit('0123456789abcdef0123')).toBe('0123456789ab')
    expect(displayCommit('unknown')).toBe('未知')
  })
})

describe('isDevBuild', () => {
  it('三字段全占位→true,任一真实→false', () => {
    const dev: BuildInfo = { version: 'dev', commit: 'unknown', build_time: 'unknown', go_version: 'go1.22' }
    const real: BuildInfo = { version: 'v1.0.0', commit: 'unknown', build_time: 'unknown', go_version: 'go1.22' }
    expect(isDevBuild(dev)).toBe(true)
    expect(isDevBuild(real)).toBe(false)
  })
})

describe('buildSmtpTest', () => {
  it('收件人空 → 错误', () => {
    expect(buildSmtpTest({ to: '   ', tenantId: '' })).toEqual({ error: '请填写测试收件邮箱' })
  })

  it('收件人非邮箱形态 → 错误', () => {
    // 判别核心:缺 @ 必拦。变异(去掉邮箱校验)→ 会返回 request 而非 error,本断言 RED。
    expect(buildSmtpTest({ to: 'not-an-email', tenantId: '' })).toEqual({ error: '收件邮箱格式不正确' })
  })

  it('合法邮箱 + 空租户号 → tenant_id 默认 0', () => {
    expect(buildSmtpTest({ to: 'ops@example.com', tenantId: '' })).toEqual({
      tenant_id: 0,
      to: 'ops@example.com',
    })
  })

  it('显式租户号被解析为整数', () => {
    expect(buildSmtpTest({ to: 'ops@example.com', tenantId: '7' })).toEqual({
      tenant_id: 7,
      to: 'ops@example.com',
    })
  })

  it('非法租户号 → 错误', () => {
    expect(buildSmtpTest({ to: 'ops@example.com', tenantId: '-1' })).toEqual({ error: '租户号须为非负整数' })
    expect(buildSmtpTest({ to: 'ops@example.com', tenantId: 'abc' })).toEqual({ error: '租户号须为非负整数' })
  })
})
