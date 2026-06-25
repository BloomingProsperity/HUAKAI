import { describe, expect, it } from 'vitest'
import { classifyRefreshFailure, parseIssuedTokens, shouldRefresh } from './refresh'

describe('parseIssuedTokens', () => {
  it('抽出三字段', () => {
    expect(
      parseIssuedTokens({ session_token: 'hus_a', refresh_token: 'husr_b', session_expires_at: '2026-06-25T10:00:00Z' }),
    ).toEqual({ sessionToken: 'hus_a', refreshToken: 'husr_b', sessionExpiresAt: '2026-06-25T10:00:00Z' })
  })
  it('缺 refresh/expiry 用 null 兜底', () => {
    expect(parseIssuedTokens({ session_token: 'hus_a' })).toEqual({
      sessionToken: 'hus_a',
      refreshToken: null,
      sessionExpiresAt: null,
    })
  })
  it('缺 session_token / 空 / undefined → null(不写空会话)', () => {
    // 判别核心:无 session_token 必须返回 null。变异(无条件返回对象)→ RED。
    expect(parseIssuedTokens({ refresh_token: 'husr_b' })).toBeNull()
    expect(parseIssuedTokens(null)).toBeNull()
    expect(parseIssuedTokens(undefined)).toBeNull()
  })
})

describe('shouldRefresh', () => {
  const exp = '2026-06-25T10:00:00Z'
  const expMs = Date.parse(exp)
  const buffer = 120_000 // 2 分钟

  it('距到期超过 buffer → 不刷新', () => {
    expect(shouldRefresh(exp, expMs - 5 * 60_000, buffer)).toBe(false)
  })
  it('进入 buffer 窗口(到期前 2 分钟内)→ 刷新', () => {
    // 判别核心:now ≥ exp - buffer。变异(去掉 -buffer 或恒 false)→ RED。
    expect(shouldRefresh(exp, expMs - 60_000, buffer)).toBe(true)
  })
  it('已过期 → 刷新', () => {
    expect(shouldRefresh(exp, expMs + 60_000, buffer)).toBe(true)
  })
  it('恰好在 buffer 边界(now === exp - buffer)→ 刷新', () => {
    expect(shouldRefresh(exp, expMs - buffer, buffer)).toBe(true)
  })
  it('无到期 / 非法时刻 → 不刷新(不盲目刷新)', () => {
    expect(shouldRefresh(null, expMs, buffer)).toBe(false)
    expect(shouldRefresh('not-a-date', expMs, buffer)).toBe(false)
  })
})

describe('classifyRefreshFailure', () => {
  it('不可恢复错误码 → logout', () => {
    // 判别核心:这些码必须 logout。变异(默认全 transient)→ RED。
    for (const code of [
      'refresh_token_replay',
      'refresh_token_cross_user_attempt',
      'refresh_token_expired',
      'session_token_not_found',
      'session_family_revoked',
      'session_anomaly_rejected',
      'invalid_session_request',
      'session_device_limit_exceeded',
      'session_device_confirmation_required',
    ]) {
      expect(classifyRefreshFailure(code)).toBe('logout')
    }
  })
  it('后端瞬时 / 未知码 → transient(保守不踢人)', () => {
    expect(classifyRefreshFailure('session_backend_error')).toBe('transient')
    expect(classifyRefreshFailure('http_500')).toBe('transient')
    expect(classifyRefreshFailure('whatever')).toBe('transient')
  })
})
