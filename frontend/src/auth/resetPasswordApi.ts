import { apiSend } from '../lib/api'
import { buildResetPayload, type ResetLinkParams } from './resetPassword'

/*
 * 重置密码数据访问层。端点 POST /v1/auth/reset-password(公开,/v1/auth/* 不带 token,
 * 见 tokenForPath.ts)。带 token 分支:后端校验一次性 token 并落新口令,成功撤销该用户全部 session。
 *
 * 端点真码:backend/internal/gatewayhttp/auth_handler.go:166(mount)/:514-565(token 确认分支),
 * 成功响应 200 { user, sessions_revoked, session_revocation }(:565);本前端不依赖响应体细节,
 * 仅以「不抛错」表示成功。请求体字段名见 resetPassword.ts/buildResetPayload。
 */

/** 提交新密码完成重置。成功 resolve,失败抛 ApiError(由页面区分 token 失效等)。 */
export async function submitPasswordReset(params: ResetLinkParams, newPassword: string): Promise<void> {
  await apiSend<unknown>('POST', '/v1/auth/reset-password', buildResetPayload(params, newPassword))
}
