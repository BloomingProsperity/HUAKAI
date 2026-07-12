import { describe, expect, it } from 'vitest'
import { buildRiskCards, parseTenantInput, totalRiskSignals } from './risk'
import type { RiskOverview } from './types'

function ov(p: Partial<RiskOverview>): RiskOverview {
  return {
    object: 'risk_overview',
    tenant_id: 1,
    disabled_keys: 0,
    firing_alerts: 0,
    disabled_users: 0,
    ip_blacklisted_keys: 0,
    ...p,
  }
}

describe('buildRiskCards', () => {
  it('计数 > 0 的卡片 tone=alert,计数 0 的 tone=ok(变异:若 tone 恒定则此断言红)', () => {
    const cards = buildRiskCards(ov({ disabled_keys: 3, firing_alerts: 0 }))
    const disabled = cards.find((c) => c.key === 'disabled_keys')!
    const alerts = cards.find((c) => c.key === 'firing_alerts')!
    // 判别性:同一份数据里一个 alert 一个 ok,任何"恒返回同一 tone"的实现都会让这对断言至少红一个。
    expect(disabled.tone).toBe('alert')
    expect(disabled.count).toBe(3)
    expect(alerts.tone).toBe('ok')
    expect(alerts.count).toBe(0)
  })

  it('每张卡映射到对应计数字段(变异:若字段错配则计数对不上)', () => {
    const cards = buildRiskCards(ov({ disabled_keys: 1, firing_alerts: 2, disabled_users: 3, ip_blacklisted_keys: 4 }))
    expect(cards.map((c) => [c.key, c.count])).toEqual([
      ['disabled_keys', 1],
      ['firing_alerts', 2],
      ['disabled_users', 3],
      ['ip_blacklisted_keys', 4],
    ])
  })

  it('每张卡都带「去处理」跳转到已有运维页(非空 actionPath)', () => {
    const cards = buildRiskCards(ov({}))
    for (const c of cards) {
      expect(c.actionPath.startsWith('/')).toBe(true)
      expect(c.actionLabel.length).toBeGreaterThan(0)
    }
    // 禁用 key → 内容审核台,触发告警 → 告警控制台(防把跳转目标搞混)。
    expect(cards.find((c) => c.key === 'disabled_keys')!.actionPath).toBe('/admin/moderation')
    expect(cards.find((c) => c.key === 'firing_alerts')!.actionPath).toBe('/admin/alerting')
  })
})

describe('totalRiskSignals', () => {
  it('汇总四项之和(变异:漏加任一项则总数偏小)', () => {
    expect(totalRiskSignals(ov({ disabled_keys: 1, firing_alerts: 2, disabled_users: 4, ip_blacklisted_keys: 8 }))).toBe(15)
  })
})

describe('parseTenantInput', () => {
  it('正整数取之,非法/非正回退默认租户 1', () => {
    expect(parseTenantInput('7')).toBe(7)
    expect(parseTenantInput('0')).toBe(1)
    expect(parseTenantInput('-3')).toBe(1)
    expect(parseTenantInput('abc')).toBe(1)
  })
})
