import { describe, expect, it } from 'vitest'
import { pathNeedsAdmin, tokenForPath } from './tokenForPath'

const tokens = { sessionToken: 'sess-abc', adminToken: 'adm-xyz' }

describe('tokenForPath', () => {
  it('/v1/auth/* 不带 token(登录注册本身换取 token)', () => {
    // 判别核心:登录端点必须不带 token。变异(去掉该分支)→ 会带 sessionToken,本断言 RED。
    expect(tokenForPath('/v1/auth/login', tokens)).toBeNull()
    expect(tokenForPath('/v1/auth/register', tokens)).toBeNull()
  })

  it('/admin/* 用 admin token', () => {
    expect(tokenForPath('/admin/v1/provider-accounts', tokens)).toBe('adm-xyz')
    expect(tokenForPath('/admin/v1/model-pool-bindings', tokens)).toBe('adm-xyz')
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
    expect(tokenForPath('/admin/v1/x', { sessionToken: 's', adminToken: null })).toBeNull()
    expect(tokenForPath('/v1/api-keys', { sessionToken: null, adminToken: 'a' })).toBeNull()
  })
})
