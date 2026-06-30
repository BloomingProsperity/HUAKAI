import { describe, expect, it } from 'vitest'
import { ApiError } from '../lib/api'
import {
  DEFAULT_TENANT_ID,
  deviceConfirmErrorMessage,
  parseDeviceConfirmParams,
  validateDeviceConfirmParams,
} from './deviceConfirm'

describe('parseDeviceConfirmParams', () => {
  it('解析 token + tenant_id;token 去空白', () => {
    const p = parseDeviceConfirmParams('?token=%20abc%20&tenant_id=7')
    expect(p.token).toBe('abc')
    expect(p.tenantId).toBe(7)
  })
  it('tenant_id 缺省/非法/<=0 回退默认租户(不发 0 给后端)', () => {
    // 变异(不回退、直接用 0/NaN)→ 这些断言 RED;0 会被后端 400。
    expect(parseDeviceConfirmParams('token=x').tenantId).toBe(DEFAULT_TENANT_ID)
    expect(parseDeviceConfirmParams('token=x&tenant_id=0').tenantId).toBe(DEFAULT_TENANT_ID)
    expect(parseDeviceConfirmParams('token=x&tenant_id=-3').tenantId).toBe(DEFAULT_TENANT_ID)
  })
})

describe('validateDeviceConfirmParams', () => {
  it('空 token 报错、有 token 通过', () => {
    // 变异(删 token 空判断)→ 空 token 本应报错却放行,首断言 RED。
    expect(validateDeviceConfirmParams({ token: '', tenantId: 1 })).toMatch(/缺少 token/)
    expect(validateDeviceConfirmParams({ token: 'abc', tenantId: 1 })).toBeNull()
  })
})

describe('deviceConfirmErrorMessage', () => {
  it('invalid / expired 各自映射专属中文,不并入兜底', () => {
    // 判别核心:invalid 与 expired 文案必须不同(用户分清重登 vs 重发)。变异(并入兜底)→ RED。
    const invalid = deviceConfirmErrorMessage(new ApiError(401, 'device_confirmation_invalid', 'x'))
    const expired = deviceConfirmErrorMessage(new ApiError(401, 'device_confirmation_expired', 'x'))
    expect(invalid).toMatch(/无效或已被使用/)
    expect(expired).toMatch(/已过期/)
    expect(invalid).not.toBe(expired)
  })
  it('未知错误码走兜底;非 ApiError 走通用文案', () => {
    expect(deviceConfirmErrorMessage(new ApiError(503, 'device_confirmation_backend_error', 'down'))).toMatch(/设备确认失败/)
    expect(deviceConfirmErrorMessage(new Error('boom'))).toMatch(/请稍后重试/)
  })
})
