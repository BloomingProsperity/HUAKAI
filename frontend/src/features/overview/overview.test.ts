import { describe, expect, it } from 'vitest'
import {
  buildUsageBars,
  formatCount,
  formatUsd,
  pickHeadlineQuota,
  quotaProgress,
  summarizeKeys,
  usageBarRatios,
} from './overview'
import type { ApiKeyView, KeyUsageSummary, QuotaWindow } from './types'

function qw(over: Partial<QuotaWindow>): QuotaWindow {
  return {
    metric: 'cost',
    window_kind: 'daily',
    cap: '0',
    consumed: '0',
    remaining: '0',
    overage: '0',
    request_count: 0,
    window_start: '2026-06-25T00:00:00Z',
    window_end: '2026-06-26T00:00:00Z',
    ...over,
  }
}

function key(id: number, status = 'active', name = `k${id}`): ApiKeyView {
  return { api_key_id: id, name, key_prefix: `hk_${id}`, status }
}

function summary(over: Partial<KeyUsageSummary>): KeyUsageSummary {
  return {
    api_key_id: 0,
    total_cost: '0',
    total_tokens_input: 0,
    total_tokens_output: 0,
    total_cache_read_tokens: 0,
    total_cache_creation_tokens: 0,
    request_count: 0,
    ...over,
  }
}

describe('quotaProgress', () => {
  it('cap<=0 视为无上限', () => {
    expect(quotaProgress('5', '0').unlimited).toBe(true)
    expect(quotaProgress('5', '-1').unlimited).toBe(true)
  })

  it('普通比例下 tone=ok、pct 准确', () => {
    const p = quotaProgress('5', '10')
    expect(p.unlimited).toBe(false)
    expect(p.over).toBe(false)
    // 判别核心:50% → pct=50。若实现把比例算错(如忘乘 100)此断言会红。
    expect(p.pct).toBe(50)
    expect(p.tone).toBe('ok')
  })

  it('≥80% 触发 warn,超额触发 danger 且 pct clamp 到 100', () => {
    // 判别核心:80% 阈值。若把 >= 写成 > 则 80% 会落 ok,此断言会红。
    expect(quotaProgress('8', '10').tone).toBe('warn')
    const over = quotaProgress('15', '10')
    expect(over.over).toBe(true)
    expect(over.tone).toBe('danger')
    expect(over.pct).toBe(100)
  })
})

describe('pickHeadlineQuota', () => {
  it('空数组返回 null', () => {
    expect(pickHeadlineQuota([])).toBeNull()
  })

  it('超额窗口胜过仅接近上限的窗口', () => {
    const warn = qw({ metric: 'tokens', consumed: '9', cap: '10' }) // 90% → warn
    const over = qw({ metric: 'cost', consumed: '20', cap: '10' }) // 超额 → danger
    // 判别核心:即使 warn 排在前面,也必须选超额那条(权重 danger>warn)。
    expect(pickHeadlineQuota([warn, over])?.metric).toBe('cost')
  })

  it('无超额无 warn 时优先有上限的窗口', () => {
    const unlimited = qw({ metric: 'tokens', cap: '0' }) // 无上限
    const capped = qw({ metric: 'cost', consumed: '1', cap: '100' }) // 有上限,1%
    expect(pickHeadlineQuota([unlimited, capped])?.metric).toBe('cost')
  })
})

describe('summarizeKeys', () => {
  it('只把 active 计入 active 计数', () => {
    const keys = [key(1, 'active'), key(2, 'revoked'), key(3, 'active'), key(4, 'expired')]
    const c = summarizeKeys(keys)
    // 判别核心:active=2(非 4)。若实现忘了过滤 status 则会算成 4。
    expect(c.active).toBe(2)
    expect(c.total).toBe(4)
  })

  it('reportedCount 大于已列出条数时取 reportedCount(分页少报防护)', () => {
    const keys = [key(1), key(2)]
    expect(summarizeKeys(keys, 9).total).toBe(9)
    // reportedCount 比列出的少时,不缩水
    expect(summarizeKeys(keys, 1).total).toBe(2)
  })
})

describe('buildUsageBars', () => {
  it('按 cost 降序排序并过滤全 0 的 Key', () => {
    const rows = [
      { key: key(1, 'active', '低'), summary: summary({ total_cost: '0.10', request_count: 3 }) },
      { key: key(2, 'active', '高'), summary: summary({ total_cost: '2.50', request_count: 9 }) },
      { key: key(3, 'active', '空'), summary: summary({ total_cost: '0', request_count: 0 }) },
      { key: key(4, 'active', '失败'), summary: null },
    ]
    const bars = buildUsageBars(rows)
    // 判别核心:全 0 与 null 都被剔除(剩 2 条),且按 cost 降序(高在前)。
    expect(bars.map((b) => b.label)).toEqual(['高', '低'])
    expect(bars[0].cost).toBe(2.5)
  })

  it('limit 截断', () => {
    const rows = Array.from({ length: 10 }, (_, i) => ({
      key: key(i + 1, 'active', `k${i}`),
      summary: summary({ total_cost: String(i + 1), request_count: 1 }),
    }))
    expect(buildUsageBars(rows, 3)).toHaveLength(3)
  })
})

describe('usageBarRatios', () => {
  it('ratio = cost / maxCost', () => {
    const bars = [
      { keyId: 1, label: 'a', cost: 4, requests: 1 },
      { keyId: 2, label: 'b', cost: 1, requests: 1 },
    ]
    const r = usageBarRatios(bars)
    // 判别核心:最大值归一化为 1,其它按比例。若用错基准(如固定 100)此断言会红。
    expect(r[0].ratio).toBe(1)
    expect(r[1].ratio).toBe(0.25)
  })

  it('全 0 花费时所有 ratio 为 0(避免除零)', () => {
    const r = usageBarRatios([{ keyId: 1, label: 'a', cost: 0, requests: 5 }])
    expect(r[0].ratio).toBe(0)
  })
})

describe('格式化', () => {
  it('formatUsd 定点 4 位', () => {
    expect(formatUsd(1.23456)).toBe('1.2346')
    expect(formatUsd(Number.NaN)).toBe('—')
  })
  it('formatCount 千分位并截断小数', () => {
    expect(formatCount(12345)).toBe('12,345')
    expect(formatCount(12.9)).toBe('12')
    expect(formatCount(Number.NaN)).toBe('—')
  })
})
