import { describe, expect, it } from 'vitest'
import { buildBulkPayload, healthRows, testSummary, type BulkByTagForm } from './diagnostics'
import type { AccountHealth, AccountTestResult } from './types'

function testResult(p: Partial<AccountTestResult>): AccountTestResult {
  return { ok: false, error_class: null, message: '', ...p }
}

function bulkForm(p: Partial<BulkByTagForm>): BulkByTagForm {
  return { tag: 'prod', enabled: '', priority: '', staticWeight: '', ...p }
}

function health(p: Partial<AccountHealth>): AccountHealth {
  return {
    id: 1,
    health_state: 'active',
    last_probe_latency_ms: null,
    last_probe_at: null,
    model_sync_last_check_at: null,
    session_window_5h_start: null,
    session_window_5h_end: null,
    session_window_5h_status: null,
    last_refresh_at: null,
    last_refresh_outcome: null,
    failure_class: null,
    failure_count: 0,
    enabled: true,
    requires_action: false,
    updated_at: '2026-06-29T00:00:00Z',
    ...p,
  }
}

describe('testSummary', () => {
  it('ok=true → tone=ok,带 message', () => {
    const s = testSummary(testResult({ ok: true, message: 'credential validation completed' }))
    expect(s.tone).toBe('ok')
    expect(s.label).toContain('credential')
  })

  it('ok=true 但 message 空 → 兜底"连通正常"', () => {
    expect(testSummary(testResult({ ok: true, message: '' })).label).toBe('连通正常')
  })

  it('ok=false → tone=fail,error_class 映射中文(变异:若忽略 ok 恒判 ok 则此断言红)', () => {
    const s = testSummary(testResult({ ok: false, error_class: 'permanent' }))
    expect(s.tone).toBe('fail')
    expect(s.label).toContain('永久失效')
  })

  it('ok=false 未知 error_class → 兜底含原始码', () => {
    const s = testSummary(testResult({ ok: false, error_class: 'weird_x' }))
    expect(s.tone).toBe('fail')
    expect(s.label).toContain('weird_x')
  })
})

describe('buildBulkPayload', () => {
  it('tag 为空 → 报错(变异:去掉 tag 校验则此条放过 → 红)', () => {
    const r = buildBulkPayload(bulkForm({ tag: '   ' }))
    expect('error' in r && r.error).toBe('标签必填')
  })

  it('三项全空 → 报错"至少改一项"(变异:去掉该守卫则会下发空 payload → 后端 no_field_to_set)', () => {
    const r = buildBulkPayload(bulkForm({ tag: 'prod' }))
    expect('error' in r && r.error).toContain('至少改一项')
  })

  it('enabled=true 单项即可成单,payload 只含 tag+enabled', () => {
    const r = buildBulkPayload(bulkForm({ enabled: 'true' }))
    expect('payload' in r).toBe(true)
    if ('payload' in r) {
      expect(r.payload).toEqual({ tag: 'prod', enabled: true })
      // 判别核心:priority/static_weight 未填则不得出现在 payload(否则误清空)。
      expect('priority' in r.payload).toBe(false)
      expect('static_weight' in r.payload).toBe(false)
    }
  })

  it('enabled=false 显式下发布尔 false(不可被当作"未填")', () => {
    const r = buildBulkPayload(bulkForm({ enabled: 'false' }))
    expect('payload' in r && r.payload.enabled).toBe(false)
  })

  it('priority 非整数 → 报错(变异:去掉整数校验则 NaN 漏过)', () => {
    expect('error' in buildBulkPayload(bulkForm({ priority: 'abc' }))).toBe(true)
    expect('error' in buildBulkPayload(bulkForm({ priority: '3.5' }))).toBe(true)
  })

  it('合法 priority/static_weight 整数才进 payload', () => {
    const r = buildBulkPayload(bulkForm({ priority: '5', staticWeight: '10' }))
    expect('payload' in r && r.payload.priority).toBe(5)
    expect('payload' in r && r.payload.static_weight).toBe(10)
  })

  it('static_weight 负数 → 报错(变异:去掉 <0 守卫则负权重漏过)', () => {
    expect('error' in buildBulkPayload(bulkForm({ staticWeight: '-1' }))).toBe(true)
  })
})

describe('healthRows', () => {
  it('requires_action=true 标注需人工(变异:若漏读该字段则告警丢失)', () => {
    const rows = healthRows(health({ requires_action: true }))
    const need = rows.find(([k]) => k === '需人工处理')
    expect(need?.[1]).toContain('是')
  })

  it('recent_requests 缺省时不追加该行;存在时追加计数', () => {
    expect(healthRows(health({})).some(([k]) => k === '近期请求')).toBe(false)
    const rows = healthRows(health({ recent_requests: { total: 9, success: 7, failure: 2 } }))
    const r = rows.find(([k]) => k === '近期请求')
    expect(r?.[1]).toContain('9')
    expect(r?.[1]).toContain('7')
    expect(r?.[1]).toContain('2')
  })

  it('failure_count 始终渲染(数字 0 也显示,不被当作空)', () => {
    const rows = healthRows(health({ failure_count: 0 }))
    expect(rows.find(([k]) => k === '失败次数')?.[1]).toBe('0')
  })
})
