import { describe, expect, it } from 'vitest'
import { buildAuditQuery, severityTone, toIso } from './audit'
import { EMPTY_AUDIT_FILTERS, type AuditFilters } from './types'

function f(over: Partial<AuditFilters>): AuditFilters {
  return { ...EMPTY_AUDIT_FILTERS, ...over }
}

describe('buildAuditQuery', () => {
  it('空过滤 → 空 query(不下发空串字段)', () => {
    // 判别核心:空白字段必须省略。变异(改成无条件赋值)→ query 会含 event_class:''→本断言 RED。
    expect(buildAuditQuery(EMPTY_AUDIT_FILTERS)).toEqual({})
  })

  it('非空字段 trim 后下发,空白仍省略', () => {
    const q = buildAuditQuery(f({ eventClass: ' billing ', severity: '', actorId: '42' }))
    expect(q).toEqual({ event_class: 'billing', actor_id: '42' })
    expect('severity' in q).toBe(false)
  })

  it('cursor 非空才带', () => {
    expect('cursor' in buildAuditQuery(EMPTY_AUDIT_FILTERS, '')).toBe(false)
    expect(buildAuditQuery(EMPTY_AUDIT_FILTERS, 'abc123').cursor).toBe('abc123')
  })

  it('from/to 转 ISO', () => {
    const q = buildAuditQuery(f({ from: '2026-06-24T00:00' }))
    expect(typeof q.from).toBe('string')
    expect((q.from as string).includes('2026-06-2')).toBe(true)
  })
})

describe('toIso', () => {
  it('空串/非法 → 空串', () => {
    expect(toIso('')).toBe('')
    expect(toIso('not-a-date')).toBe('')
  })
})

describe('severityTone', () => {
  it('critical/error→danger,warn→warn,info→info,其余→muted', () => {
    expect(severityTone('critical')).toBe('danger')
    expect(severityTone('ERROR')).toBe('danger')
    expect(severityTone('warn')).toBe('warn')
    expect(severityTone('info')).toBe('info')
    expect(severityTone('debug')).toBe('muted')
  })
})
