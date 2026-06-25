/*
 * 忘记密码页纯逻辑:邮箱/租户校验 + 重置请求 payload 构造。
 *
 * 抽成无副作用函数便于单测:不碰 fetch、不读 store、不打印任何敏感值。
 * 后端契约(auth_handler.go:119 authResetPasswordRequest / :463 newAuthResetPasswordHandler):
 *   无 token 分支 = 「请求重置邮件」,请求体 {tenant_id, email},成功返回 202 {reset_requested:true}。
 *   captcha:reset-password 端点当前未接 captcha 校验(只有 register/login 走 verifyAuthCaptcha),
 *   但请求体里预留 captcha_token 字段(decodeAdminPoolJSON 不拒未知字段,缺省不传即可),
 *   以便运维侧将来对外公开页开启 captcha 时无需改前端契约。
 */

/** 重置邮件请求体(对应后端 authResetPasswordRequest 的无 token 分支)。 */
export interface ResetEmailRequest {
  tenant_id: number
  email: string
  /** 预留位:仅当公开页配了 captcha 才带;后端当前忽略,留作前向兼容。 */
  captcha_token?: string
}

// 朴素邮箱形态校验:本地部分@域名部分,域名至少一个点。仅做友好前置提示,
// 真正判定以后端为准(且成功态不泄露邮箱是否存在)。
const EMAIL_RE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/

export function normalizeEmail(raw: string): string {
  return raw.trim().toLowerCase()
}

/** 把租户输入(字符串)解析成正整数 ID;非法/<=0 返回 0(由校验拦截)。 */
export function parseTenantId(raw: string): number {
  const n = Number(raw.trim())
  if (!Number.isInteger(n) || n <= 0) return 0
  return n
}

/**
 * 表单前置校验:返回首条错误中文文案,或 null 表示通过。
 * 判别核心:邮箱缺失 / 形态非法 / 租户非正整数 三类分别给不同文案。
 */
export function validateForgotForm(emailRaw: string, tenantRaw: string): string | null {
  const email = normalizeEmail(emailRaw)
  if (!email) return '请填写邮箱'
  if (!EMAIL_RE.test(email)) return '邮箱格式不正确'
  if (parseTenantId(tenantRaw) === 0) return '租户 ID 必须为正整数'
  return null
}

/**
 * 构造重置邮件请求体。email 归一化(trim+小写),captcha 仅在非空时带上。
 * 判别核心:email 必须是归一化后的值;captcha 为空时字段必须缺席(不能带空串)。
 */
export function buildResetEmailRequest(
  emailRaw: string,
  tenantRaw: string,
  captchaToken?: string,
): ResetEmailRequest {
  const req: ResetEmailRequest = {
    tenant_id: parseTenantId(tenantRaw),
    email: normalizeEmail(emailRaw),
  }
  const cap = captchaToken?.trim()
  if (cap) req.captcha_token = cap
  return req
}
