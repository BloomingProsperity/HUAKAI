/*
 * 会话 token 生命周期纯逻辑(可单测)。
 *
 * 背景(核源码确认的契约):
 *  - 登录(/v1/auth/login、/login/2fa)与刷新(POST /v1/sessions/refresh,body {refresh_token})
 *    都返回后端 IssuedTokens:{session_token, refresh_token, session_expires_at, refresh_expires_at, ...}。
 *  - session token TTL 短(默认 15 分钟),refresh token 长(默认 30 天);前端必须在 session
 *    到期前用 refresh token 换新,否则用户 15 分钟就被踢。
 *  - 刷新失败错误码(session_handler.go writeSessionError)分两类:不可恢复(family 受损/重放/
 *    过期/异常)→ 强制登出;后端瞬时 → 保留会话稍后再试。这里把这些判定抽成纯函数便于测试。
 */

/** 前端持久化的会话 token 形态。 */
export interface SessionTokens {
  sessionToken: string
  refreshToken: string | null
  /** session token 到期时刻(RFC3339);用于主动刷新判定。 */
  sessionExpiresAt: string | null
}

/** 后端 IssuedTokens 的 JSON 子集(login/refresh 的 session 字段)。 */
export interface RawIssuedTokens {
  session_token?: string
  refresh_token?: string
  session_expires_at?: string
}

/**
 * 从后端 IssuedTokens 抽出前端持久化形态;缺 session_token 视为无效返回 null
 * (避免把空会话写进 store)。
 */
export function parseIssuedTokens(raw: RawIssuedTokens | null | undefined): SessionTokens | null {
  if (!raw || !raw.session_token) return null
  return {
    sessionToken: raw.session_token,
    refreshToken: raw.refresh_token ?? null,
    sessionExpiresAt: raw.session_expires_at ?? null,
  }
}

/**
 * 是否该在到期前主动刷新:now ≥ 到期时刻 - buffer。
 * 无到期信息或无法解析 → false(不盲目刷新,避免无谓的刷新风暴)。
 */
export function shouldRefresh(sessionExpiresAt: string | null, nowMs: number, bufferMs: number): boolean {
  if (!sessionExpiresAt) return false
  const exp = Date.parse(sessionExpiresAt)
  if (Number.isNaN(exp)) return false
  return nowMs >= exp - bufferMs
}

export type RefreshFailureAction = 'logout' | 'transient'

/**
 * 据刷新失败错误码判定后续动作:
 *  - logout:不可恢复(refresh token 重放/跨用户/过期、family 撤销、会话异常、设备门、请求非法)
 *    → 必须清态重新登录;
 *  - transient:后端瞬时故障 / 网络 / 未知 → 保留会话,稍后再试。
 * 默认走 transient(保守:不因未知错误把用户踢出)。
 */
export function classifyRefreshFailure(code: string): RefreshFailureAction {
  switch (code) {
    case 'refresh_token_replay':
    case 'refresh_token_cross_user_attempt':
    case 'refresh_token_expired':
    case 'session_token_not_found':
    case 'session_family_revoked':
    case 'session_anomaly_rejected':
    case 'invalid_session_request':
    case 'session_device_limit_exceeded':
    case 'session_device_confirmation_required':
      return 'logout'
    default:
      return 'transient'
  }
}
