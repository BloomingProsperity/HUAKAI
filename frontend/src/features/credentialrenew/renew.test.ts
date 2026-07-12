import { describe, expect, it } from 'vitest'
import {
  buildRenewStatusQuery,
  clampRenewLimit,
  DEFAULT_RENEW_LIMIT,
  failureSummary,
  humanizeDuration,
  MAX_RENEW_LIMIT,
  parseTime,
  relativeTime,
  renewHealth,
  renewHealthLabel,
  renewHealthTone,
  SOON_WINDOW_MS,
} from './renew'
import type { RenewStatusRow } from './types'

// 构造一条「健康」基线行,各用例只覆写需要的字段(便于隔离判别)。
function row(over: Partial<RenewStatusRow> = {}): RenewStatusRow {
  return {
    id: 1,
    tenant_id: 1,
    tenant_name: 't',
    account_id: 1,
    account_name: 'a',
    vendor: 'anthropic',
    auth_mode: 'oauth',
    state: 'active',
    credential_version: 3,
    access_expires_at: null,
    refresh_before_at: null,
    last_refresh_at: null,
    last_refresh_outcome: null,
    failure_class: null,
    failure_count: 0,
    ...over,
  }
}

const NOW = Date.UTC(2026, 5, 29, 12, 0, 0)
function iso(offsetMs: number): string {
  return new Date(NOW + offsetMs).toISOString()
}

describe('clampRenewLimit', () => {
  it('钳进 [1,500],缺省/NaN 用默认 100', () => {
    expect(clampRenewLimit(undefined)).toBe(DEFAULT_RENEW_LIMIT)
    expect(clampRenewLimit(NaN)).toBe(DEFAULT_RENEW_LIMIT)
    // 判别核心:0/负数必须抬到 1(变异:直接返回入参 → 0→RED)。
    expect(clampRenewLimit(0)).toBe(1)
    expect(clampRenewLimit(-5)).toBe(1)
    // 判别核心:超过 500 必须压回 500(变异:不设上限 → 999→RED)。
    expect(clampRenewLimit(999)).toBe(MAX_RENEW_LIMIT)
    expect(clampRenewLimit(250)).toBe(250)
    // 非整数向下取整。
    expect(clampRenewLimit(50.9)).toBe(50)
  })
})

describe('buildRenewStatusQuery', () => {
  it('limit 始终下发并钳;tenant_id 非正省略;cursor 空省略', () => {
    expect(buildRenewStatusQuery({})).toEqual({ limit: DEFAULT_RENEW_LIMIT })
    // 判别核心:正整数 tenant_id 才下发(变异:无条件赋值 → tenant_id 出现→RED)。
    expect(buildRenewStatusQuery({ tenantId: 8 })).toEqual({ limit: DEFAULT_RENEW_LIMIT, tenant_id: 8 })
    expect('tenant_id' in buildRenewStatusQuery({ tenantId: 0 })).toBe(false)
    expect('tenant_id' in buildRenewStatusQuery({ tenantId: -3 })).toBe(false)
    expect('tenant_id' in buildRenewStatusQuery({ tenantId: 1.5 })).toBe(false)
  })

  it('cursor 非空才下发,limit 经钳', () => {
    const q = buildRenewStatusQuery({ cursor: 'abc', limit: 999 })
    expect(q.cursor).toBe('abc')
    // 判别核心:limit 必须被钳到 500,而非透传 999。
    expect(q.limit).toBe(MAX_RENEW_LIMIT)
    // 判别核心:空/空白 cursor 不下发(变异:无条件赋值 → cursor 出现→RED)。
    expect('cursor' in buildRenewStatusQuery({ cursor: '' })).toBe(false)
    expect('cursor' in buildRenewStatusQuery({ cursor: '   ' })).toBe(false)
  })
})

describe('renewHealth 优先级判定', () => {
  it('failure 压过一切(即便未过期且 active)', () => {
    const r = row({ failure_count: 2, access_expires_at: iso(10 * 60 * 60 * 1000) })
    // 判别核心:有失败必须 failing,不能因「未过期」掉到 healthy/soon。
    expect(renewHealth(r, NOW)).toBe('failing')
    // failure_class 非空、count 为 0 也算 failing。
    expect(renewHealth(row({ failure_class: 'invalid_grant' }), NOW)).toBe('failing')
  })

  it('非 active state → disabled(优先于到期判定)', () => {
    const r = row({ state: 'disabled', access_expires_at: iso(-1000) })
    // 判别核心:停用应报 disabled,而非 expired(变异:跳过 state 检查 → expired→RED)。
    expect(renewHealth(r, NOW)).toBe('disabled')
  })

  it('已过 access_expires_at → expired', () => {
    expect(renewHealth(row({ access_expires_at: iso(-1000) }), NOW)).toBe('expired')
  })

  it('进入 refresh_before 窗口但未过期 → due', () => {
    const r = row({ refresh_before_at: iso(-1000), access_expires_at: iso(2 * SOON_WINDOW_MS) })
    expect(renewHealth(r, NOW)).toBe('due')
  })

  it('距到期 ≤ soon 窗口 → soon;远 → healthy', () => {
    // 判别核心:刚好在窗口内算 soon,远超窗口算 healthy(变异:窗口边界翻转可被这两断言抓到)。
    expect(renewHealth(row({ access_expires_at: iso(SOON_WINDOW_MS - 1000) }), NOW)).toBe('soon')
    expect(renewHealth(row({ access_expires_at: iso(SOON_WINDOW_MS + 10 * 60 * 60 * 1000) }), NOW)).toBe('healthy')
  })

  it('无过期/续期信息 → na', () => {
    expect(renewHealth(row(), NOW)).toBe('na')
  })
})

describe('renewHealthTone / Label', () => {
  it('语气分级:healthy=ok,soon/due=warn,failing/expired/disabled=danger,na=muted', () => {
    expect(renewHealthTone('healthy')).toBe('ok')
    expect(renewHealthTone('soon')).toBe('warn')
    expect(renewHealthTone('due')).toBe('warn')
    // 判别核心:expired/failing/disabled 必须 danger(不可与 soon 同级 warn)。
    expect(renewHealthTone('expired')).toBe('danger')
    expect(renewHealthTone('failing')).toBe('danger')
    expect(renewHealthTone('disabled')).toBe('danger')
    expect(renewHealthTone('na')).toBe('muted')
  })

  it('中文标签可辨', () => {
    expect(renewHealthLabel('healthy')).toBe('健康')
    expect(renewHealthLabel('failing')).toBe('续期失败')
    expect(renewHealthLabel('na')).toBe('不适用')
  })
})

describe('parseTime', () => {
  it('非法/空/null → null(不能当 0,否则误判已过期)', () => {
    // 判别核心:非法时间返回 null(变异:返回 0 → 会让 renewHealth 误判 expired)。
    expect(parseTime(null)).toBeNull()
    expect(parseTime('')).toBeNull()
    expect(parseTime('not-a-date')).toBeNull()
    expect(parseTime('2026-06-29T00:00:00Z')).toBe(Date.parse('2026-06-29T00:00:00Z'))
  })
})

describe('relativeTime', () => {
  it('未来「后」、过去「前」、null「—」', () => {
    expect(relativeTime(null, NOW)).toBe('—')
    // 判别核心:方向必须区分(变异:恒返回「前」→ 未来用例 RED)。
    expect(relativeTime(iso(2 * 60 * 60 * 1000), NOW)).toBe('2 小时后')
    expect(relativeTime(iso(-2 * 60 * 60 * 1000), NOW)).toBe('2 小时前')
  })
})

describe('humanizeDuration', () => {
  it('选最粗粒度', () => {
    expect(humanizeDuration(30 * 1000)).toBe('30 秒')
    expect(humanizeDuration(5 * 60 * 1000)).toBe('5 分钟')
    expect(humanizeDuration(3 * 60 * 60 * 1000)).toBe('3 小时')
    expect(humanizeDuration(2 * 24 * 60 * 60 * 1000)).toBe('2 天')
  })
})

describe('failureSummary', () => {
  it('无失败 →「—」,有失败拼 class×count', () => {
    expect(failureSummary(row())).toBe('—')
    expect(failureSummary(row({ failure_class: 'network', failure_count: 3 }))).toBe('network ×3')
    // 判别核心:class 缺失但有计数时给「未知」,不能漏 count。
    expect(failureSummary(row({ failure_count: 2 }))).toBe('未知 ×2')
  })
})
