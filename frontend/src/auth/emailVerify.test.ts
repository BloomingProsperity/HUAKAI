import { describe, expect, it } from 'vitest'
import { ApiError } from '../lib/api'
import {
  DEFAULT_TENANT_ID,
  parseVerifyParams,
  TOKEN_INVALID_CODE,
  validateVerifyParams,
  verifyErrorMessage,
} from './emailVerify'

describe('parseVerifyParams', () => {
  it('解析 token 与 tenant_id', () => {
    const p = parseVerifyParams('?token=abc123&tenant_id=7')
    expect(p.token).toBe('abc123')
    expect(p.tenantId).toBe(7)
  })

  it('裸 query(无 ?)也能解析', () => {
    const p = parseVerifyParams('token=xyz&tenant_id=3')
    expect(p.token).toBe('xyz')
    expect(p.tenantId).toBe(3)
  })

  it('token 两端空白被 trim', () => {
    expect(parseVerifyParams('?token=%20%20tok%20%20').token).toBe('tok')
  })

  it('缺 tenant_id → 回退默认租户', () => {
    // 判别核心:缺省必须回退 DEFAULT_TENANT_ID(1),否则会把 0/NaN 发给后端必然 ErrInvalidInput。
    // 变异(改成 0 或 NaN)→ RED。
    expect(parseVerifyParams('?token=t').tenantId).toBe(DEFAULT_TENANT_ID)
  })

  it('tenant_id 非正整数 → 回退默认租户', () => {
    expect(parseVerifyParams('?token=t&tenant_id=0').tenantId).toBe(DEFAULT_TENANT_ID)
    expect(parseVerifyParams('?token=t&tenant_id=-5').tenantId).toBe(DEFAULT_TENANT_ID)
    expect(parseVerifyParams('?token=t&tenant_id=abc').tenantId).toBe(DEFAULT_TENANT_ID)
    expect(parseVerifyParams('?token=t&tenant_id=2.5').tenantId).toBe(DEFAULT_TENANT_ID)
  })

  it('缺 token → 空串', () => {
    expect(parseVerifyParams('?tenant_id=1').token).toBe('')
  })
})

describe('validateVerifyParams', () => {
  it('缺 token → 中文错误', () => {
    const msg = validateVerifyParams({ token: '', tenantId: 1 })
    expect(msg).not.toBeNull()
    expect(msg).toContain('token')
  })

  it('齐全 → null', () => {
    expect(validateVerifyParams({ token: 'abc', tenantId: 1 })).toBeNull()
  })
})

describe('verifyErrorMessage', () => {
  it('auth_token_invalid → 专属「失效/已用」文案', () => {
    // 判别核心:token 失效码必须走专属分支(含「失效」字样),不能落到兜底。
    // 变异(把这条删掉 / 改成兜底)→ 此断言 RED。
    const msg = verifyErrorMessage(new ApiError(400, TOKEN_INVALID_CODE, 'token is invalid'))
    expect(msg).toContain('失效')
    expect(msg).not.toContain(TOKEN_INVALID_CODE)
  })

  it('invalid_auth_request → 请求无效文案', () => {
    const msg = verifyErrorMessage(new ApiError(400, 'invalid_auth_request', 'bad'))
    expect(msg).toContain('无效')
  })

  it('其它 ApiError → 兜底带 code', () => {
    const msg = verifyErrorMessage(new ApiError(503, 'auth_backend_error', '后端不可用'))
    expect(msg).toContain('auth_backend_error')
  })

  it('非 ApiError → 通用重试文案', () => {
    expect(verifyErrorMessage(new Error('boom'))).toContain('重试')
  })
})
