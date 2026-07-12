import { apiGet, apiSend } from '../../lib/api'

/*
 * SMTP 邮件配置数据访问层。这是 email 子系统自有的 admin 端点(不属于 platform-settings):
 *   GET  /v1/admin/email/settings?tenant_id=N  读取(密码脱敏,只回 configured 布尔)
 *   PUT  /v1/admin/email/settings              写入(只下发有变更的字段)
 *   POST /v1/admin/email/test                  发送测试邮件
 * 路径命中 /v1/admin/*,api 基座的 tokenForPath 会自动挂 admin Bearer,无需手贴。
 * 密码语义:后端字段 smtp_password 为可选(*string),不传=不修改,传空串=清空,故留空时不下发本字段。
 */

/** GET 返回的单行设置。password 行 value 恒为空、由 configured 表示是否已配置。 */
export interface EmailSettingRow {
  key: string
  value: string
  /** 仅 smtp_password 行出现:是否已配置(明文不回吐)。 */
  configured?: boolean
  updated_at?: string | null
  updated_by?: string | null
}

export interface EmailSettingsResponse {
  tenant_id: number
  settings: EmailSettingRow[]
}

/** PUT 请求体。省略某字段=不修改;smtp_password 省略=保持原密钥。 */
export interface EmailSettingsUpdate {
  tenant_id: number
  smtp_host?: string
  smtp_port?: number
  smtp_username?: string
  smtp_password?: string
  smtp_from?: string
  smtp_from_name?: string
  smtp_use_tls?: boolean
  email_verify_enabled?: boolean
  /** 按 kind 的鉴权邮件模板覆盖。字段省略=不修改,空串=清除覆盖(回退内置默认)。 */
  templates?: Record<string, EmailTemplateInput>
}

export interface EmailTemplateInput {
  subject?: string
  body?: string
}

export interface EmailTemplatePreviewResult {
  kind: string
  subject: string
  html: string
}

export interface EmailSettingsUpdateResult {
  tenant_id: number
  updated: number
}

export interface EmailTestResult {
  tenant_id: number
  sent: boolean
}

const PATH = '/v1/admin/email'

export function getEmailSettings(tenantId: number, signal?: AbortSignal): Promise<EmailSettingsResponse> {
  return apiGet<EmailSettingsResponse>(`${PATH}/settings`, { query: { tenant_id: tenantId }, signal })
}

export function updateEmailSettings(body: EmailSettingsUpdate): Promise<EmailSettingsUpdateResult> {
  return apiSend<EmailSettingsUpdateResult>('PUT', `${PATH}/settings`, body)
}

export function sendTestEmail(tenantId: number, to: string): Promise<EmailTestResult> {
  return apiSend<EmailTestResult>('POST', `${PATH}/test`, { tenant_id: tenantId, to })
}

/** 用样例值服务端渲染模板(不发信),供编辑器预览。 */
export function previewEmailTemplate(
  tenantId: number,
  kind: string,
  subject: string,
  body: string,
): Promise<EmailTemplatePreviewResult> {
  return apiSend<EmailTemplatePreviewResult>('POST', `${PATH}/templates/preview`, {
    tenant_id: tenantId,
    kind,
    subject,
    body,
  })
}
