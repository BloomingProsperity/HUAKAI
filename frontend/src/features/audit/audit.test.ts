import { describe, expect, it } from 'vitest'
import { appendQuery, buildAuditQuery, buildExportQuery, severityTone, toIso } from './audit'
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

describe('buildExportQuery', () => {
  it('request_ids 优先于时间段,且 trim+去空', () => {
    // 判别核心:给了 request_ids 时,绝不能再下发 from/to(后端会 400 歧义)。
    // 变异(把 ids 分支去掉、改成无条件走时间段)→ 结果会含 from/to 而非 request_ids → 本断言 RED。
    const q = buildExportQuery({ from: '2026-06-24T00:00', to: '2026-06-25T00:00', requestIds: [' a ', '', 'b'] })
    expect(q).toEqual({ request_ids: 'a,b' })
    expect('from' in q).toBe(false)
    expect('to' in q).toBe(false)
  })

  it('无 request_ids 时下发 from/to(已转 ISO)', () => {
    const q = buildExportQuery({ from: '2026-06-24T00:00', to: '2026-06-25T00:00' })
    expect(Object.keys(q).sort()).toEqual(['from', 'to'])
    expect((q.from as string).includes('2026-06-24')).toBe(true)
    expect('request_ids' in q).toBe(false)
  })

  it('既无 request_ids、from/to 又不齐 → 抛错(不下发歧义/缺参)', () => {
    // 判别核心:缺 to 必须抛错而非静默下发只有 from 的查询(后端 to_required 400)。
    // 变异(去掉 !from||!to 守卫、直接返回 {from,to})→ 不再抛错 → 本断言 RED。
    expect(() => buildExportQuery({ from: '2026-06-24T00:00', to: '' })).toThrow()
    expect(() => buildExportQuery({ from: '', to: '' })).toThrow()
    // 空白 request_ids 不算有效,仍回退到时间段校验。
    expect(() => buildExportQuery({ from: '', to: '', requestIds: ['  ', ''] })).toThrow()
  })
})

describe('appendQuery', () => {
  it('空 query → 原路径;非空 → 拼 encode 后的 ?k=v', () => {
    expect(appendQuery('/v1/audit/export', {})).toBe('/v1/audit/export')
    // 判别核心:值必须 URL-encode(逗号/特殊字符),否则注入风险/参数错位。
    // 变异(去掉 encodeURIComponent)→ 'a,b' 不会被编码成 'a%2Cb' → 本断言 RED。
    expect(appendQuery('/v1/audit/export', { request_ids: 'a,b' })).toBe('/v1/audit/export?request_ids=a%2Cb')
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
