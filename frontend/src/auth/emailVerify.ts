/*
 * 邮箱验证页纯逻辑:从 URL query 解出 token / tenant_id,做提交前校验,把后端错误码归一成中文文案。
 * 不触碰 DOM / fetch / store,便于单测。
 */

import { ApiError } from '../lib/api'

/** 默认租户。单租户部署(运维者自跑实例)恒为 1;链接未带 tenant_id 时回退此值。 */
export const DEFAULT_TENANT_ID = 1

export interface VerifyParams {
  /** 验证 token(注册邮件链接中的 token,经 trim)。 */
  token: string
  /** 租户 ID;链接缺省或非法时回退 DEFAULT_TENANT_ID。 */
  tenantId: number
}

/**
 * 从 URL query 串解析验证参数。接受 '?token=...&tenant_id=...' 或不带 '?' 的裸 query。
 * token 经 trim;tenant_id 解析失败 / <=0 一律回退默认租户(避免把 0 发给后端必然 ErrInvalidInput)。
 */
export function parseVerifyParams(search: string): VerifyParams {
  const q = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search)
  const token = (q.get('token') ?? '').trim()
  const raw = q.get('tenant_id')
  const parsed = raw === null ? NaN : Number(raw)
  const tenantId = Number.isInteger(parsed) && parsed > 0 ? parsed : DEFAULT_TENANT_ID
  return { token, tenantId }
}

/**
 * 提交前校验。token 为空(链接缺 token)返回中文错误文案;合法返回 null。
 * tenantId 已在 parseVerifyParams 兜底为正整数,这里只需把守 token。
 */
export function validateVerifyParams(p: VerifyParams): string | null {
  if (!p.token) return '验证链接缺少 token,请使用注册邮件中的完整链接。'
  return null
}

/**
 * 用用户手动粘贴的 token 兜底 URL 里缺失的 token。
 * 背景(端到端缺陷修复):验证邮件正文只投递裸 token、不含带 query 的链接
 *(email/sender_factory.go:266 buildVerificationBody),用户收到 token 却无法构造带 ?token= 的落地 URL。
 * URL 已带 token 时一切照旧(manual 忽略);URL 无 token 时用 trim 后的 manual 覆盖;都空则保持空。
 */
export function withManualVerifyToken(urlParams: VerifyParams, manualToken: string): VerifyParams {
  if (urlParams.token.length > 0) return urlParams
  const t = manualToken.trim()
  return t ? { ...urlParams, token: t } : urlParams
}

/** 后端 auth_token_invalid 错误码:token 无效 / 过期 / 已使用(auth_handler.go:1013)。 */
export const TOKEN_INVALID_CODE = 'auth_token_invalid'

/**
 * 把验证失败的异常归一成中文文案。判别核心:auth_token_invalid 必须映射到「失效/已用」专属提示,
 * 否则用户分不清是链接问题还是服务问题。变异(把这条并入兜底)→ 测试 RED。
 */
export function verifyErrorMessage(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.code === TOKEN_INVALID_CODE) {
      return '链接已失效或已被使用,请重新登录以获取新的验证邮件。'
    }
    if (e.code === 'invalid_auth_request') {
      return '验证请求无效,请检查链接是否完整。'
    }
    return `验证失败:${e.message}(${e.code})`
  }
  return '验证失败,请稍后重试。'
}
