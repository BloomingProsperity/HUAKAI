import { describe, expect, it } from 'vitest'
import { pathNeedsAdmin, tokenForPath } from './tokenForPath'

const tokens = { sessionToken: 'sess-abc', adminToken: 'adm-xyz' }

describe('tokenForPath', () => {
  it('/v1/auth 公开端点(登录/注册/找回/验证/OAuth/Passkey登录)不带 token', () => {
    // 判别核心:换取 token 前的端点必须不带 token。变异(去掉该分支)→ 会带 sessionToken,RED。
    expect(tokenForPath('/v1/auth/login', tokens)).toBeNull()
    expect(tokenForPath('/v1/auth/login/2fa', tokens)).toBeNull()
    expect(tokenForPath('/v1/auth/register', tokens)).toBeNull()
    expect(tokenForPath('/v1/auth/reset-password', tokens)).toBeNull()
    expect(tokenForPath('/v1/auth/verify-email', tokens)).toBeNull()
    expect(tokenForPath('/v1/auth/oauth-callback', tokens)).toBeNull()
    expect(tokenForPath('/v1/auth/passkey/login/begin', tokens)).toBeNull()
  })

  it('/v1/auth 的 session 端点(me/2fa/logout)带 session token,不是公开', () => {
    // 判别核心:这些是登录后端点,必须带 session token。变异(整段 /v1/auth→null)→ 个人资料/2FA 恒 401,RED。
    expect(tokenForPath('/v1/auth/me', tokens)).toBe('sess-abc')
    expect(tokenForPath('/v1/auth/me/password', tokens)).toBe('sess-abc')
    expect(tokenForPath('/v1/auth/me/profile', tokens)).toBe('sess-abc')
    expect(tokenForPath('/v1/auth/2fa/status', tokens)).toBe('sess-abc')
    expect(tokenForPath('/v1/auth/2fa/enable', tokens)).toBe('sess-abc')
    expect(tokenForPath('/v1/auth/logout', tokens)).toBe('sess-abc')
  })

  it('/admin/* 用 admin token', () => {
    expect(tokenForPath('/admin/v1/provider-accounts', tokens)).toBe('adm-xyz')
    expect(tokenForPath('/admin/v1/model-pool-bindings', tokens)).toBe('adm-xyz')
  })

  it('admin 路径没有手贴 admin token 时回落 session token', () => {
    // 判别核心:登录态 admin 用户可由后端按 session 角色放行。变异(去掉 ?? sessionToken)→ 返回 null,RED。
    expect(tokenForPath('/admin/v1/provider-accounts', { sessionToken: 'sess-abc', adminToken: null })).toBe('sess-abc')
    expect(tokenForPath('/v1/admin/usage/overview', { sessionToken: 'sess-abc', adminToken: null })).toBe('sess-abc')
  })

  it('admin 路径两种 token 都有时优先手贴 admin token', () => {
    // 判别核心:手贴 admin token 是显式覆盖。变异(直接用 sessionToken)→ 本断言 RED。
    expect(tokenForPath('/admin/v1/provider-accounts', { sessionToken: 'sess-abc', adminToken: 'adm-xyz' })).toBe('adm-xyz')
  })

  it('/v1/admin/* 也用 admin token(后端同属 adminGate,曾误发 session token 致 401)', () => {
    // 判别核心:这几个 /v1/admin/* 端点是 admin-token 鉴权,必须带 admin token。
    // 变异(去掉 /v1/admin/ 分支)→ 退回 sessionToken,本断言 RED(正是 Ops#144/Settings#135 的真 bug)。
    expect(tokenForPath('/v1/admin/usage/overview', tokens)).toBe('adm-xyz')
    expect(tokenForPath('/v1/admin/platform-settings', tokens)).toBe('adm-xyz')
    expect(tokenForPath('/v1/admin/system/health', tokens)).toBe('adm-xyz')
  })

  it('pathNeedsAdmin 覆盖两种 admin 前缀,用户态为否', () => {
    expect(pathNeedsAdmin('/admin/v1/users')).toBe(true)
    expect(pathNeedsAdmin('/v1/admin/usage/overview')).toBe(true)
    expect(pathNeedsAdmin('/v1/api-keys')).toBe(false)
    expect(pathNeedsAdmin('/v1/auth/login')).toBe(false)
  })

  it('用户态端点用 session token', () => {
    expect(tokenForPath('/v1/api-keys', tokens)).toBe('sess-abc')
    expect(tokenForPath('/v1/me/quota', tokens)).toBe('sess-abc')
    expect(tokenForPath('/v1/me/keys/3/usage-summary', tokens)).toBe('sess-abc')
  })

  it('token 缺失时返回 null(不伪造)', () => {
    expect(tokenForPath('/admin/v1/x', { sessionToken: null, adminToken: null })).toBeNull()
    expect(tokenForPath('/v1/api-keys', { sessionToken: null, adminToken: 'a' })).toBeNull()
  })
})
