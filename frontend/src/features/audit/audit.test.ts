import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { clearAll, getRefreshToken, setSessionTokens } from '../../auth/store'
import { exportAuditChain } from './api'
import { appendQuery, buildAuditQuery, buildExportQuery, mapAuditTableRows, severityTone, toIso } from './audit'
import { EMPTY_AUDIT_FILTERS, type AuditEvent, type AuditFilters } from './types'

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

describe('mapAuditTableRows', () => {
  it('逐列映射审计事件并保留完整详情来源', () => {
    const source: AuditEvent = {
      id: 17,
      tenant_id: 3,
      event_class: 'security',
      event_type: 'token.revoked',
      severity: 'warn',
      request_id: 'request-1234567890-tail',
      actor_id: 9,
      actor_role: 'platform_admin',
      reason: 'manual',
      payload: { safe: true },
      created_at: 'invalid-time',
    }
    const [row] = mapAuditTableRows([source])

    // 判别核心:每一列使用独立哨兵值，互换、漏映射或错误截断都会使断言变红。
    expect(row).toEqual({
      id: 17,
      createdAt: 'invalid-time',
      eventType: 'token.revoked',
      eventClass: 'security',
      severity: 'warn',
      actor: 'platform_admin #9',
      reason: 'manual',
      requestID: 'request-1234567890-tail',
      requestIDLabel: 'request-…tail',
      detail: {
        id: 17,
        tenant_id: 3,
        ledger_id: undefined,
        claim_id: undefined,
        provider_account_id: undefined,
        pool_group_id: undefined,
        request_id: 'request-1234567890-tail',
        payload: { safe: true },
      },
      source,
    })
  })
})

describe('审计下载主动刷新接线', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-07-05T10:00:00.000Z'))
    clearAll()
    setSessionTokens({
      sessionToken: 'hus_old',
      refreshToken: 'husr_old',
      sessionExpiresAt: '2026-07-05T10:01:00.000Z',
    })
  })

  afterEach(() => {
    clearAll()
    vi.unstubAllGlobals()
    vi.useRealTimers()
  })

  it('session 临近到期时先刷新,再用新 token 下载审计导出', async () => {
    const f = vi.fn(async (input: RequestInfo | URL, _init?: RequestInit) => {
      const url = String(input)
      if (url === '/v1/sessions/refresh') {
        return {
          ok: true,
          status: 200,
          json: async () => ({
            session: {
              session_token: 'hus_new',
              refresh_token: 'husr_new',
              session_expires_at: '2026-07-05T10:15:00Z',
            },
          }),
        } as Response
      }
      return {
        ok: false,
        status: 401,
        statusText: 'Unauthorized',
        text: async () => JSON.stringify({ error: { code: 'session_expired', message: '会话已过期' } }),
      } as Response
    })
    vi.stubGlobal('fetch', f)

    await expect(exportAuditChain({ from: '', to: '', requestIds: ['req_a'] })).rejects.toMatchObject({
      status: 401,
      code: 'session_expired',
    })

    // 判别核心:下载前必须先刷新。变异(去掉 ensureFreshSessionForPath)→ 只有下载请求,本断言 RED。
    expect(f).toHaveBeenCalledTimes(2)
    expect(f.mock.calls[0][0]).toBe('/v1/sessions/refresh')
    expect(JSON.parse((f.mock.calls[0][1] as RequestInit).body as string)).toEqual({ refresh_token: 'husr_old' })
    expect(f.mock.calls[1][0]).toBe('/v1/audit/export?request_ids=req_a')
    expect((f.mock.calls[1][1] as RequestInit).headers).toEqual({ Authorization: 'Bearer hus_new' })
    expect(getRefreshToken()).toBe('husr_new')
  })
})
