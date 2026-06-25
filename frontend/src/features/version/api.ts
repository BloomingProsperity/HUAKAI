import { apiGet, apiSend } from '../../lib/api'
import type { BuildInfo, SmtpTestRequest, SmtpTestResponse } from './types'

/*
 * 版本与维护数据访问层。
 *  - GET  /v1/admin/version    —— 构建版本信息(admin token,只读)
 *  - POST /v1/admin/email/test —— SMTP 连接测试(admin token,发一封测试信验证 SMTP 可用)
 * 两端点路径前缀 /v1/admin/*,api.ts 自动注入 admin Bearer。
 */

/** 读取构建版本快照。 */
export async function getBuildInfo(signal?: AbortSignal): Promise<BuildInfo> {
  return apiGet<BuildInfo>('/v1/admin/version', { signal })
}

/** 触发 SMTP 测试:用当前已配置的 SMTP 设置向 to 发一封测试信。 */
export async function sendSmtpTest(req: SmtpTestRequest, signal?: AbortSignal): Promise<SmtpTestResponse> {
  return apiSend<SmtpTestResponse>('POST', '/v1/admin/email/test', req, { signal })
}
