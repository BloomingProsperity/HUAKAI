/*
 * 重置密码页纯逻辑(可单测,无 DOM / 无网络)。
 *
 * 职责拆三块:
 *  1) 从 URL query 解析重置链接参数(email / token / tenant_id);
 *  2) 校验用户输入的新密码 + 确认密码(长度 ≥8、两次一致);
 *  3) 构造提交给 POST /v1/auth/reset-password 的请求体,并把后端错误码归一成本页文案。
 *
 * 端点真码依据:
 *  - 路由 backend/internal/gatewayhttp/auth_handler.go:166 mount POST /reset-password(挂在 /v1/auth)。
 *  - 带 token 分支请求体 struct authResetPasswordRequest(auth_handler.go:119-124):
 *      { tenant_id int64, token string, new_password string }。
 *  - token 无效/过期/已用 → 400 error.code = "auth_token_invalid"(auth_handler.go:1013-1014)。
 *  - 空输入 → 400 "invalid_auth_request"(service.go:443 守 tenant_id<=0 || token=="" || new_password=="")。
 *  说明:HUAKAI 的重置邮件正文只给一次性 token(email/sender_factory.go:245),不含带 query 的完整链接,
 *  故本页作为链接落地页,从 URL 读 email/token/tenant;tenant 缺省按单租户部署惯例取 1(与登录页一致)。
 */

/** 新密码最短长度。后端口令策略可能更严,但前端这条是给用户的即时反馈下限。 */
export const MIN_PASSWORD_LENGTH = 8

/** 后端「token 无效/过期/已用」专属错误码 —— 用于切到「链接失效」专门文案。 */
export const TOKEN_INVALID_CODE = 'auth_token_invalid'

export interface ResetLinkParams {
  email: string
  token: string
  tenantId: number
}

/**
 * 从 URL query 解析重置链接参数。容忍 email/tenant_id 缺省;token 是命门(缺失则链接无效)。
 * tenant_id 兼容 `tenant_id` 与 `tenant` 两种键名;非正整数一律回落 1。
 */
export function parseResetLink(search: string): ResetLinkParams {
  const q = new URLSearchParams(search)
  const token = (q.get('token') ?? '').trim()
  const email = (q.get('email') ?? '').trim()
  const rawTenant = (q.get('tenant_id') ?? q.get('tenant') ?? '').trim()
  const parsed = Number(rawTenant)
  const tenantId = Number.isInteger(parsed) && parsed > 0 ? parsed : 1
  return { email, token, tenantId }
}

/** 链接是否有效:必须带非空 token(无 token 无法走重置确认)。 */
export function hasResetToken(params: ResetLinkParams): boolean {
  return params.token.length > 0
}

/**
 * 用用户手动粘贴的 token 兜底 URL 里缺失的 token。
 *
 * 背景(端到端缺陷修复):后端重置邮件正文只投递裸 token、不含带 query 的链接
 * (email/sender_factory.go:271 buildPasswordResetBody),用户收到 token 却无法构造
 * 带 ?token= 的落地 URL。本 helper 让页面在 URL 无 token 时接受手动粘贴的 token,
 * 使重置流程不再因「token 无处可填」而端到端死锁。
 *
 * 语义:URL 已带非空 token 时一切照旧(manual 被忽略);URL 无 token 时用 trim 后的
 * manual token 覆盖。manual 也为空则保持空 token(页面据此禁用提交)。
 */
export function withManualToken(urlParams: ResetLinkParams, manualToken: string): ResetLinkParams {
  if (urlParams.token.length > 0) return urlParams
  const t = manualToken.trim()
  return t ? { ...urlParams, token: t } : urlParams
}

export type PasswordCheck =
  | { ok: true }
  | { ok: false; reason: 'too_short' | 'mismatch' | 'empty' }

/**
 * 校验新密码 + 确认密码。判别优先级:空 → 太短 → 两次不一致 → 通过。
 * 这是「提交可用性」的唯一判定来源,页面据此禁用按钮 / 出错提示。
 */
export function checkNewPassword(password: string, confirm: string): PasswordCheck {
  if (password.length === 0) return { ok: false, reason: 'empty' }
  if (password.length < MIN_PASSWORD_LENGTH) return { ok: false, reason: 'too_short' }
  if (password !== confirm) return { ok: false, reason: 'mismatch' }
  return { ok: true }
}

/** 把密码校验失败原因翻成中文提示(空态不出红,交给按钮禁用即可)。 */
export function passwordCheckMessage(check: PasswordCheck): string | null {
  if (check.ok) return null
  switch (check.reason) {
    case 'too_short':
      return `密码至少需要 ${MIN_PASSWORD_LENGTH} 个字符`
    case 'mismatch':
      return '两次输入的密码不一致'
    case 'empty':
      return null
  }
}

export interface ResetPasswordPayload {
  tenant_id: number
  token: string
  new_password: string
}

/**
 * 构造 reset-password 请求体。字段名严格对齐后端 struct(snake_case),不多塞 email
 * (确认分支后端只读 tenant_id/token/new_password,email 仅用于展示)。
 */
export function buildResetPayload(params: ResetLinkParams, newPassword: string): ResetPasswordPayload {
  return {
    tenant_id: params.tenantId,
    token: params.token,
    new_password: newPassword,
  }
}

/** 后端错误是否为「token 无效/过期/已用」—— 据此切到链接失效专门文案。 */
export function isTokenInvalidError(code: string, status: number): boolean {
  return code === TOKEN_INVALID_CODE || status === 410
}
