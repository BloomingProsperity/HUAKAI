import { apiGet, apiSend } from '../../lib/api'
import type {
  BuildInfo,
  EmailSettingsResponse,
  EmailSettingsUpdateRequest,
  EmailSettingsUpdateResponse,
  SmtpTestRequest,
  SmtpTestResponse,
} from './types'

/*
 * 版本与维护数据访问层。
 *  - GET  /v1/admin/version        —— 构建版本信息(admin token,只读)
 *  - GET  /v1/admin/email/settings —— SMTP 设置回填(admin token,只读;口令掩码不回显)
 *  - PUT  /v1/admin/email/settings —— 保存 SMTP 设置(admin token)
 *  - POST /v1/admin/email/test     —— SMTP 连接测试(admin token,发一封测试信验证 SMTP 可用)
 * 全部路径前缀 /v1/admin/*,api.ts 自动注入 admin Bearer。
 */

/** 读取构建版本快照。 */
export async function getBuildInfo(signal?: AbortSignal): Promise<BuildInfo> {
  return apiGet<BuildInfo>('/v1/admin/version', { signal })
}

/**
 * 读取指定租户的 SMTP 设置(掩码视图)。tenant_id 作为查询参数透传给后端;
 * 单租户运营默认传 0(后端对 tenant_operator 角色留空时回退自身 scope,但平台 admin 必须显式给正值)。
 * 后端 handler:57 要求 tenant_id 为正整数(平台 admin 角色),故这里要求调用方传有效值。
 */
export async function getEmailSettings(tenantId: number, signal?: AbortSignal): Promise<EmailSettingsResponse> {
  return apiGet<EmailSettingsResponse>('/v1/admin/email/settings', {
    query: { tenant_id: tenantId },
    signal,
  })
}

/**
 * 保存 SMTP 设置。请求体由纯逻辑层 buildEmailSettingsUpdate 构造(已剔除留空字段、按需省略口令)。
 * money 不涉及;但口令为凭证,务必由调用方保证「留空=省略 smtp_password」语义,绝不发空串。
 */
export async function saveEmailSettings(
  req: EmailSettingsUpdateRequest,
  signal?: AbortSignal,
): Promise<EmailSettingsUpdateResponse> {
  return apiSend<EmailSettingsUpdateResponse>('PUT', '/v1/admin/email/settings', req, { signal })
}

/** 触发 SMTP 测试:用当前已配置的 SMTP 设置向 to 发一封测试信。 */
export async function sendSmtpTest(req: SmtpTestRequest, signal?: AbortSignal): Promise<SmtpTestResponse> {
  return apiSend<SmtpTestResponse>('POST', '/v1/admin/email/test', req, { signal })
}
