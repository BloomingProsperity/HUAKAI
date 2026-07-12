import { describe, expect, it } from 'vitest'
import {
  buildEmailSettingsUpdate,
  buildSmtpTest,
  displayCommit,
  displayVersion,
  EMPTY_SMTP_SETTINGS,
  isDevBuild,
  isPlaceholder,
  settingsToForm,
  type SmtpSettingsForm,
} from './version'
import type { BuildInfo, EmailSettingsResponse } from './types'

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

describe('settingsToForm', () => {
  it('回填非口令字段,口令永不回显,布尔字符串被解析', () => {
    const resp: EmailSettingsResponse = {
      tenant_id: 3,
      settings: [
        { key: 'smtp_host', value: 'smtp.example.com', updated_at: '', updated_by: 'op' },
        { key: 'smtp_port', value: '587', updated_at: '', updated_by: 'op' },
        { key: 'smtp_username', value: 'mailer', updated_at: '', updated_by: 'op' },
        // 后端掩码:口令 value 恒空 + configured 标志。
        { key: 'smtp_password', value: '', configured: true, updated_at: '', updated_by: 'op' },
        { key: 'smtp_from', value: 'noreply@example.com', updated_at: '', updated_by: 'op' },
        { key: 'smtp_from_name', value: 'HUAKAI', updated_at: '', updated_by: 'op' },
        { key: 'smtp_use_tls', value: 'true', updated_at: '', updated_by: 'op' },
        { key: 'email_verify_enabled', value: 'false', updated_at: '', updated_by: 'op' },
      ],
    }
    const { form, passwordConfigured } = settingsToForm(resp)
    // 判别核心(凭证不回显):口令必须为空串。变异(改成回填 pwItem.value)——但 value 本就是 ''——
    // 故用 configured=true 但断言 password==='' 双保险:若实现误把 configured 当 value 写入,本断言 RED。
    expect(form.password).toBe('')
    expect(passwordConfigured).toBe(true)
    expect(form.host).toBe('smtp.example.com')
    expect(form.port).toBe('587')
    expect(form.username).toBe('mailer')
    expect(form.from).toBe('noreply@example.com')
    expect(form.fromName).toBe('HUAKAI')
    // 判别核心(布尔解析):'true'→true、'false'→false。变异(恒返回 true)→ verifyEmail 断言 RED。
    expect(form.useTls).toBe(true)
    expect(form.verifyEmail).toBe(false)
  })

  it('缺口令项 → passwordConfigured=false', () => {
    // 判别核心:无 smtp_password 项视为从未配置。变异(默认 true)→ 本断言 RED。
    const resp: EmailSettingsResponse = { tenant_id: 1, settings: [] }
    expect(settingsToForm(resp).passwordConfigured).toBe(false)
  })
})

describe('buildEmailSettingsUpdate', () => {
  const full: SmtpSettingsForm = {
    host: 'smtp.example.com',
    port: '465',
    username: 'mailer',
    password: 'secret-pw',
    from: 'noreply@example.com',
    fromName: 'HUAKAI',
    useTls: true,
    verifyEmail: true,
  }

  it('完整表单 → 全字段下发,含口令与开关', () => {
    const r = buildEmailSettingsUpdate(full, '3')
    expect(r).toEqual({
      tenant_id: 3,
      smtp_host: 'smtp.example.com',
      smtp_port: 465,
      smtp_username: 'mailer',
      smtp_from: 'noreply@example.com',
      smtp_from_name: 'HUAKAI',
      smtp_password: 'secret-pw',
      smtp_use_tls: true,
      email_verify_enabled: true,
    })
  })

  it('口令留空 → 省略 smtp_password(保留原口令,绝不发空串)', () => {
    // 判别核心(凭证安全 + 误清除防护):口令空时请求体不得含 smtp_password 键。
    // 变异(改成 `if (form.password.length >= 0)` 或无条件 `req.smtp_password = form.password`)
    // → 会写入 '',下面 'smtp_password' in r 断言 RED,且 r.smtp_password 不为 undefined。
    const r = buildEmailSettingsUpdate({ ...full, password: '' }, '3') as Record<string, unknown>
    expect('smtp_password' in r).toBe(false)
    expect(r.smtp_password).toBeUndefined()
    // 其余字段照常下发。
    expect(r.smtp_host).toBe('smtp.example.com')
  })

  it('文本字段留空 → 省略对应键(留空=不改)', () => {
    // 判别核心:host/username/from/fromName 空白时不得出现在请求体。
    // 变异(无条件赋值)→ 这些键会带空串出现,本断言 RED。
    const r = buildEmailSettingsUpdate(
      { ...EMPTY_SMTP_SETTINGS, port: '587' },
      '0',
    ) as Record<string, unknown>
    expect('smtp_host' in r).toBe(false)
    expect('smtp_username' in r).toBe(false)
    expect('smtp_from' in r).toBe(false)
    expect('smtp_from_name' in r).toBe(false)
    // 端口非空仍下发;开关始终下发(此处默认 false)。
    expect(r.smtp_port).toBe(587)
    expect(r.smtp_use_tls).toBe(false)
    expect(r.email_verify_enabled).toBe(false)
  })

  it('端口留空 → 省略 smtp_port', () => {
    const r = buildEmailSettingsUpdate({ ...full, port: '  ' }, '1') as Record<string, unknown>
    expect('smtp_port' in r).toBe(false)
  })

  it('端口越界/非整数 → 错误', () => {
    // 判别核心:范围校验。变异(去掉范围判断)→ 会返回请求体而非 error,本断言 RED。
    expect(buildEmailSettingsUpdate({ ...full, port: '70000' }, '1')).toEqual({ error: '端口须为 1–65535 的整数' })
    expect(buildEmailSettingsUpdate({ ...full, port: '0' }, '1')).toEqual({ error: '端口须为 1–65535 的整数' })
    expect(buildEmailSettingsUpdate({ ...full, port: 'abc' }, '1')).toEqual({ error: '端口须为 1–65535 的整数' })
  })

  it('非法租户号 → 错误', () => {
    expect(buildEmailSettingsUpdate(full, '-1')).toEqual({ error: '租户号须为非负整数' })
    expect(buildEmailSettingsUpdate(full, 'x')).toEqual({ error: '租户号须为非负整数' })
  })

  it('开关始终随当前状态显式下发(关闭也发 false)', () => {
    // 判别核心:useTls/verifyEmail 无「留空保留」语义,关闭态也必须发 false。
    // 变异(改成仅 true 才下发)→ 这两键缺失,本断言 RED。
    const r = buildEmailSettingsUpdate({ ...full, useTls: false, verifyEmail: false }, '2') as Record<string, unknown>
    expect(r.smtp_use_tls).toBe(false)
    expect(r.email_verify_enabled).toBe(false)
  })
})
