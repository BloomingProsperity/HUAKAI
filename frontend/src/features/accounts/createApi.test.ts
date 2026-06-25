import { describe, expect, it } from 'vitest'
import { createRequestHeaders } from './createApi'

describe('createRequestHeaders', () => {
  it('有 token → 带 Authorization: Bearer(否则后端 admin 端点恒 401)', () => {
    // 判别核心:这正是此前漏注 token 的 S1 bug。变异(去掉 Authorization 注入)→ 本断言 RED。
    const h = createRequestHeaders('adm-xyz') as Record<string, string>
    expect(h.Authorization).toBe('Bearer adm-xyz')
    expect(h['Content-Type']).toBe('application/json')
  })

  it('无 token(null)→ 不带 Authorization,不伪造', () => {
    const h = createRequestHeaders(null) as Record<string, string>
    expect('Authorization' in h).toBe(false)
    expect(h.Accept).toBe('application/json')
  })
})
