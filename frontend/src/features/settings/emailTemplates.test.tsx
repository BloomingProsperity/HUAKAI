import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))
vi.mock('../../lib/api', async () => {
  const actual = await vi.importActual<typeof import('../../lib/api')>('../../lib/api')
  return { ...actual, apiGet: client.get, apiSend: client.send }
})

import { previewEmailTemplate } from './emailApi'
import {
  TEMPLATE_KINDS,
  credentialPlaceholder,
  overrideFromRows,
  templateSettingKeys,
  validateTemplateDraft,
} from './emailTemplates'
import { EmailTemplatesSection } from './EmailTemplatesSection'

describe('模板设置键与覆盖提取', () => {
  it('键形态镜像后端 email_template.<kind>.subject/.body(变异:改错前缀 → 红)', () => {
    expect(templateSettingKeys('verification')).toEqual({
      subjectKey: 'email_template.verification.subject',
      bodyKey: 'email_template.verification.body',
    })
  })

  it('从 GET 行提取覆盖;无覆盖回空串', () => {
    const rows = [
      { key: 'smtp_host', value: 'smtp.example.com' },
      { key: 'email_template.verification.subject', value: '自定义主题' },
      { key: 'email_template.verification.body', value: '<p>{{token}}</p>' },
    ]
    expect(overrideFromRows(rows, 'verification')).toEqual({ subject: '自定义主题', body: '<p>{{token}}</p>' })
    expect(overrideFromRows(rows, 'password_reset')).toEqual({ subject: '', body: '' })
  })
})

describe('validateTemplateDraft(镜像后端校验)', () => {
  it('未知占位符拒绝;正文缺凭证占位符拒绝;合法通过(变异:放行未知/砍凭证校验 → 红)', () => {
    expect(validateTemplateDraft('verification', '', '点 {{link}} 或 {{token}}')).toBeNull()
    expect(validateTemplateDraft('verification', '', '{{nope}} {{token}}')).toMatch(/不支持的占位符/)
    expect(validateTemplateDraft('verification', '', '没有凭证')).toMatch(/必须包含 \{\{token\}\}/)
    expect(validateTemplateDraft('oauth_code', '', '码 {{code}}')).toBeNull()
    expect(validateTemplateDraft('oauth_code', '', '{{token}}')).toMatch(/不支持的占位符/)
    // 双清空 = 恢复默认,放行。
    expect(validateTemplateDraft('verification', '', '')).toBeNull()
  })

  it('凭证占位符:oauth_code 用 code,其余用 token', () => {
    expect(credentialPlaceholder('oauth_code')).toBe('code')
    expect(credentialPlaceholder('verification')).toBe('token')
    expect(TEMPLATE_KINDS).toHaveLength(4)
  })
})

describe('preview API', () => {
  it('锁定路径与请求体(变异:改错路径/漏 kind → 红)', async () => {
    client.send.mockReset()
    client.send.mockResolvedValue({ kind: 'verification', subject: 's', html: '<p>h</p>' })
    await previewEmailTemplate(1, 'verification', '主题', '正文 {{token}}')
    expect(client.send).toHaveBeenCalledWith('POST', '/v1/admin/email/templates/preview', {
      tenant_id: 1,
      kind: 'verification',
      subject: '主题',
      body: '正文 {{token}}',
    })
  })
})

describe('渲染冒烟', () => {
  it('分区初始渲染含四类型 tab 与占位符提示', () => {
    client.get.mockReset()
    client.get.mockResolvedValue({ tenant_id: 1, settings: [] })
    const html = renderToStaticMarkup(<EmailTemplatesSection />)
    expect(html).toContain('鉴权邮件模板')
    expect(html).toContain('邮箱验证')
    expect(html).toContain('补邮箱验证码')
    expect(html).toContain('{{token}}')
    expect(html).toContain('恢复内置默认')
  })
})
