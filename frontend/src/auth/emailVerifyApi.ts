import { apiSend } from '../lib/api'

/*
 * 邮箱验证数据访问层。端点 POST /v1/auth/verify-email(公开,/v1/auth/* 不带 token)。
 * 后端 auth_handler.go:444 newAuthVerifyEmailHandler:
 *   请求 {tenant_id:int64, token:string}(authVerifyEmailRequest,:114)
 *   成功 200 {user:{...}, email_verified:true}(:459)
 *   失败 auth_token_invalid(400,token 无效/过期/已用)/ invalid_auth_request(400)/ auth_backend_error(503)。
 *
 * 形态说明:HUAKAI 后端只有「凭链接 token 确认验证」一条路径,没有独立的「发 6 位码」端点
 * (发码在注册时由后端 SendVerification 走邮件,auth_handler.go:208),故本页做「点链接确认」形态,
 * 不做 6 位码输入。这是与部分竞品(发码 + 输码)的差异。
 */

/** 验证成功后回显的用户(json tag 同后端 publicUser;字段可能缺省,做防御性可选)。 */
export interface VerifiedUser {
  user_id?: number
  id?: number
  email?: string
  display_name?: string
}

export interface VerifyEmailResponse {
  user?: VerifiedUser
  email_verified?: boolean
}

/** 凭 token 验证邮箱。tenantId 必须 > 0(后端 VerifyEmail 对 tenantID<=0 返回 ErrInvalidInput)。 */
export async function verifyEmail(tenantId: number, token: string): Promise<VerifyEmailResponse> {
  return apiSend<VerifyEmailResponse>('POST', '/v1/auth/verify-email', {
    tenant_id: tenantId,
    token,
  })
}
