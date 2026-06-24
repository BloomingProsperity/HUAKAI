import { describe, expect, it } from 'vitest'
import { tokenForPath } from './tokenForPath'

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
