import { describe, expect, it } from 'vitest'
import { canRevoke, deviceSummary, familyStatusLabel, familyStatusTone, sortFamilies } from './sessions'
import type { SessionFamily } from './sessionsTypes'

function fam(over: Partial<SessionFamily>): SessionFamily {
  return {
    id: 'f1',
    user_id: 1,
    tenant_id: 1,
    status: 'active',
    generation: 1,
    created_at: '2026-06-20T00:00:00Z',
    last_active_at: '2026-06-25T00:00:00Z',
    ...over,
  }
}

describe('familyStatusLabel / Tone', () => {
  it('active → 活跃/ok,suspicious → 可疑/warn', () => {
    expect(familyStatusLabel('active')).toBe('活跃')
    expect(familyStatusTone('active')).toBe('ok')
    // 判别核心:可疑必须是 warn(变异成 ok → 用户忽视风险 → RED)。
    expect(familyStatusLabel('suspicious')).toBe('可疑')
    expect(familyStatusTone('suspicious')).toBe('warn')
  })
  it('未知状态原样回显', () => {
    expect(familyStatusLabel('weird')).toBe('weird')
  })
})

describe('canRevoke', () => {
  it('仅 active 可撤销', () => {
    // 判别核心:非 active 必须 false(变异成恒 true → 对已失效族露撤销按钮 → RED)。
    expect(canRevoke(fam({ status: 'active' }))).toBe(true)
    expect(canRevoke(fam({ status: 'revoked' }))).toBe(false)
    expect(canRevoke(fam({ status: 'expired' }))).toBe(false)
  })
})

describe('deviceSummary', () => {
  it('从 device_info 提炼设备 + IP', () => {
    const s = deviceSummary(fam({ device_info: { platform: 'macOS', label: 'MacBook' }, ip_baseline: '1.2.3.4' }))
    expect(s).toContain('MacBook')
    expect(s).toContain('macOS')
    expect(s).toContain('1.2.3.4')
  })
  it('缺失 device_info → 未知设备(不抛错)', () => {
    expect(deviceSummary(fam({ device_info: null }))).toContain('未知设备')
  })
})

describe('sortFamilies', () => {
  it('活跃在前,组内按最近活跃倒序', () => {
    const rows = [
      fam({ id: 'old', status: 'revoked', last_active_at: '2026-06-26T00:00:00Z' }),
      fam({ id: 'a1', status: 'active', last_active_at: '2026-06-24T00:00:00Z' }),
      fam({ id: 'a2', status: 'active', last_active_at: '2026-06-27T00:00:00Z' }),
    ]
    // 判别核心:active 必须排在 revoked 之前(变异成不分组 → revoked 因时间新而排前 → RED)。
    expect(sortFamilies(rows).map((f) => f.id)).toEqual(['a2', 'a1', 'old'])
  })
})
