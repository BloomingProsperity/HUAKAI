import { describe, expect, it } from 'vitest'
import {
  buildCreateKeyRequest,
  EMPTY_KEY_FORM,
  resolveExpiresAt,
  validateCreateKeyForm,
  type CreateKeyForm,
} from './create'

// 固定 now,避免 Date.now 不确定性。
const NOW = new Date('2026-06-24T00:00:00.000Z')

function form(over: Partial<CreateKeyForm>): CreateKeyForm {
  return { ...EMPTY_KEY_FORM, ...over }
}

describe('resolveExpiresAt', () => {
  it('never → undefined(不传 expires_at)', () => {
    expect(resolveExpiresAt('never', '', NOW)).toBeUndefined()
  })

  it('7d → now + 7 天的 RFC3339', () => {
    // 判别核心:必须是 now + 7*86400000。变异(算成 now 或漏乘天数)→ RED。
    expect(resolveExpiresAt('7d', '', NOW)).toBe('2026-07-01T00:00:00.000Z')
  })

  it('90d → now + 90 天', () => {
    expect(resolveExpiresAt('90d', '', NOW)).toBe('2026-09-22T00:00:00.000Z')
  })

  it('custom 空日期 → undefined', () => {
    expect(resolveExpiresAt('custom', '', NOW)).toBeUndefined()
  })
})

describe('buildCreateKeyRequest', () => {
  it('never:只带 name+environment,不带 expires_at', () => {
    const req = buildCreateKeyRequest(form({ name: ' 生产 ', environment: 'live' }), NOW)
    expect(req).toEqual({ name: '生产', environment: 'live' })
    expect('expires_at' in req).toBe(false)
  })

  it('7d:带 expires_at', () => {
    const req = buildCreateKeyRequest(form({ name: 'k', expiryPreset: '7d' }), NOW)
    expect(req.expires_at).toBe('2026-07-01T00:00:00.000Z')
  })
})

describe('validateCreateKeyForm', () => {
  it('名称必填、超长报错', () => {
    expect(validateCreateKeyForm(form({}), NOW)).toContain('名称')
    expect(validateCreateKeyForm(form({ name: 'x'.repeat(129) }), NOW)).toContain('128')
  })

  it('custom 须选未来日期', () => {
    expect(validateCreateKeyForm(form({ name: 'k', expiryPreset: 'custom', customDate: '' }), NOW)).toContain('日期')
    expect(validateCreateKeyForm(form({ name: 'k', expiryPreset: 'custom', customDate: '2020-01-01' }), NOW)).toContain('未来')
  })

  it('齐全通过', () => {
    expect(validateCreateKeyForm(form({ name: 'k', expiryPreset: '30d' }), NOW)).toBeNull()
  })
})
