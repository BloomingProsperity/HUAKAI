import { apiSend } from '../lib/api'
import { buildResetEmailRequest, type ResetEmailRequest } from './forgot'

/*
 * 忘记密码数据访问层。端点 POST /v1/auth/reset-password(公开,/v1/auth/* 不带 token)。
 * 无 token 分支 = 发送重置邮件(auth_handler.go:463 起,成功 202 {reset_requested:true})。
 *
 * 安全:成功态统一回「邮件已发送」,不向调用方暴露邮箱是否存在(后端对存在/不存在
 * 均返回 202),前端也不据响应区分。本层不打印任何邮箱/token 明文。
 */

interface ResetRequestedResponse {
  reset_requested?: boolean
}

/**
 * 请求发送密码重置邮件。校验已由调用方(纯逻辑 validateForgotForm)前置完成,
 * 这里只负责构造请求体并发起请求;成功返回即视为已受理(不解析也不泄露邮箱存在性)。
 */
export async function requestPasswordResetEmail(
  emailRaw: string,
  tenantRaw: string,
  captchaToken?: string,
): Promise<void> {
  const body: ResetEmailRequest = buildResetEmailRequest(emailRaw, tenantRaw, captchaToken)
  await apiSend<ResetRequestedResponse>('POST', '/v1/auth/reset-password', body)
}
