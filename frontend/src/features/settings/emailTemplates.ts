import type { EmailSettingRow } from './emailApi'

/*
 * 鉴权邮件模板编辑的纯逻辑层:kind 枚举、设置键映射、占位符提示、从 GET 行提取覆盖。
 * 与后端 internal/email/templates.go 的键形态(email_template.<kind>.subject/.body)一一对应。
 */

export const TEMPLATE_KINDS = [
  { kind: 'verification', label: '邮箱验证', placeholders: ['link', 'token'] },
  { kind: 'password_reset', label: '重置密码', placeholders: ['link', 'token', 'email'] },
  { kind: 'device_confirmation', label: '新设备确认', placeholders: ['link', 'token'] },
  { kind: 'oauth_code', label: '补邮箱验证码', placeholders: ['code'] },
] as const

export type TemplateKind = (typeof TEMPLATE_KINDS)[number]['kind']

/** 每 kind 必含的凭证占位符(始终有值;link 可能因未配前端 base URL 为空)。 */
export function credentialPlaceholder(kind: TemplateKind): string {
  return kind === 'oauth_code' ? 'code' : 'token'
}

export function templateSettingKeys(kind: TemplateKind): { subjectKey: string; bodyKey: string } {
  return {
    subjectKey: `email_template.${kind}.subject`,
    bodyKey: `email_template.${kind}.body`,
  }
}

export interface TemplateOverride {
  subject: string
  body: string
}

/** 从 GET /settings 的行列表提取某 kind 的当前覆盖(无覆盖 → 空串)。 */
export function overrideFromRows(rows: EmailSettingRow[], kind: TemplateKind): TemplateOverride {
  const { subjectKey, bodyKey } = templateSettingKeys(kind)
  let subject = ''
  let body = ''
  for (const row of rows) {
    if (row.key === subjectKey) subject = row.value
    if (row.key === bodyKey) body = row.value
  }
  return { subject, body }
}

/**
 * 保存前的本地校验,镜像后端规则让错误在提交前就可见:
 * 正文非空必含凭证占位符;未知占位符拒绝。返回 null = 通过。
 */
export function validateTemplateDraft(kind: TemplateKind, subject: string, body: string): string | null {
  const spec = TEMPLATE_KINDS.find((k) => k.kind === kind)
  if (!spec) return `未知模板类型 ${kind}`
  const allowed = new Set<string>(spec.placeholders)
  const pattern = /\{\{\s*([a-zA-Z][a-zA-Z0-9_]*)\s*\}\}/g
  for (const text of [subject, body]) {
    for (const m of text.matchAll(pattern)) {
      if (!allowed.has(m[1])) return `不支持的占位符 {{${m[1]}}};本类型可用:${spec.placeholders.map((p) => `{{${p}}}`).join(' ')}`
    }
  }
  const cred = credentialPlaceholder(kind)
  if (body.trim() !== '' && !new RegExp(`\\{\\{\\s*${cred}\\s*\\}\\}`).test(body)) {
    return `正文必须包含 {{${cred}}},否则收件人拿不到可操作凭证`
  }
  return null
}
