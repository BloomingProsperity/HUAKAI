import { describe, expect, it } from 'vitest'
import type { AccountHealthSummary } from '../accounts/types'
import type { AlertEvent } from '../alerting/types'
import type { AuditEvent } from '../audit/types'
import type { ChannelHealthSummary } from '../channelhealth/types'
import type { OverviewResponse } from '../ops/types'
import type { QuotaPolicy } from '../quotapolicies/types'
import {
  abnormalPoolCount,
  accountResource,
  accountStat,
  auditRows,
  firingAlertCount,
  firingAlertStat,
  gatewayAvailabilityStat,
  modelDistribution,
  modelResource,
  modelStat,
  pendingItems,
  poolResource,
  quickLinks,
  quotaResource,
  requestTrend,
  requestVolumeStat,
} from './overview'

const overview: OverviewResponse = {
  window: '24h',
  totals: { requests: 18200, total_cost: '12.40', total_tokens: 960000, active_users: 86, active_api_keys: 41, success_count: 18000, error_count: 200, success_rate: '0.9890' },
  trend: [{ day: '2026-07-10', requests: 5000, cost: '3.10' }, { day: '2026-07-11', requests: 6200, cost: '4.20' }],
}
const accounts: AccountHealthSummary = { total: 5, enabled: 5, disabled: 0, needs_attention: 1, states: [{ health_state: 'healthy', count: 4 }, { health_state: 'cooldown', count: 1 }] }
const pools: ChannelHealthSummary = { total: 23, by_state: { active: 19, degraded: 2, cooling_down: 1, disabled: 1 } }
const alert = (id: number, state = 'firing'): AlertEvent => ({ id, tenant_id: 1, rule_id: 7, state, observed_value: 1.2, fired_at: '2026-07-12T15:04:00Z', email_sent: false })

describe('顶部指标映射', () => {
  it('可用率、请求量与趋势分别取正确字段', () => {
    expect(gatewayAvailabilityStat(overview).value).toBe('98.90%')
    const requests = requestVolumeStat(overview)
    expect(requests.value).toBe('18,200')
    expect(requests.sparkline).toEqual([5000, 6200])
  })

  it('零请求显示诚实空值，非零请求全部失败仍显示 0.00%', () => {
    const empty = { ...overview, totals: { ...overview.totals, requests: 0, success_rate: '0' } }
    expect(gatewayAvailabilityStat(empty)).toMatchObject({ value: '—', hint: '暂无请求' })
    const allFailed = { ...empty, totals: { ...empty.totals, requests: 9 } }
    expect(gatewayAvailabilityStat(allFailed).value).toBe('0.00%')
  })

  it('上游账号以真正可用数而非 enabled 数计算 4/5', () => {
    expect(accountStat(accounts).value).toBe('4/5')
    expect(accountStat(accounts).hint).toContain('1 需关注')
  })

  it('模型数与 firing 告警只取对应值', () => {
    expect(modelStat(17)).toMatchObject({ value: '17', hint: '按价目模型数' })
    expect(firingAlertStat([alert(1), alert(2, 'resolved')]).value).toBe('1')
  })
})

describe('资源概览映射', () => {
  it('C1 只给在线模型总数，不伪造健康拆分', () => {
    const item = modelResource(12)
    expect(item.value).toBe('12')
    expect(item.badges?.map((badge) => `${badge.label}:${badge.value}`)).toEqual(['口径:按价目模型数'])
  })

  it('账号卡显示 4/5 与两枚真实分解徽标', () => {
    const item = accountResource(accounts)
    expect(item.value).toBe('4/5')
    expect(item.badges?.map((badge) => badge.value)).toEqual(['4', '1'])
  })

  it('账号池异常严格合计三种施工图状态', () => {
    expect(abnormalPoolCount({ ...pools, by_state: { ...pools.by_state, manual_paused: 9 } })).toBe(4)
    expect(poolResource(23, pools).badges?.map((badge) => badge.value)).toEqual(['19', '4'])
  })

  it('账号池主数只取库存，健康为空或失败时分解明确未上报', () => {
    const noHealth: ChannelHealthSummary = { total: 0, by_state: {} }
    expect(poolResource(23, noHealth)).toMatchObject({ value: '23' })
    expect(poolResource(23, noHealth).badges?.map((badge) => badge.value)).toEqual(['未上报', '未上报'])
    expect(poolResource(23).badges?.map((badge) => badge.value)).toEqual(['未上报', '未上报'])
  })

  it('流量控制按 enabled 分组', () => {
    const policy = (id: number, enabled: boolean) => ({ id, enabled } as QuotaPolicy)
    const item = quotaResource([policy(1, true), policy(2, false), policy(3, true)])
    expect(item.value).toBe('3')
    expect(item.badges?.map((badge) => badge.value)).toEqual(['2', '1'])
  })
})

describe('待办、趋势、分布与快捷入口', () => {
  it('待办只合并一期三源且按高到中排序', () => {
    const rows = pendingItems([alert(1), alert(2, 'resolved')], accounts, pools)
    expect(rows.map((row) => row.key)).toEqual(['alerts', 'accounts', 'pools'])
    expect(rows.map((row) => row.detail)).toEqual(['1 条告警待处理', '1 个账号不可用', '4 个账号池存在异常'])
  })

  it('E 单序列保留日期和按日请求量', () => {
    expect(requestTrend(overview.trend)).toEqual([{ label: '2026-07-10', value: 5000 }, { label: '2026-07-11', value: 6200 }])
  })

  it('模型分布前五以外合并其他且总量不丢', () => {
    const entries = [6, 5, 4, 3, 2, 1].map((request_count, index) => ({ rank: index + 1, key: `m${index}`, request_count, total_cost: '0', total_tokens: 0 }))
    const result = modelDistribution(entries, 2)
    expect(result.total).toBe(21)
    expect(result.segments.map((segment) => [segment.label, segment.value])).toEqual([['m0', 6], ['m1', 5], ['其他', 10]])
  })

  it('快捷入口固定六项且仅有真实 firing 数时带角标', () => {
    expect(quickLinks(3)).toHaveLength(6)
    expect(quickLinks(3).find((item) => item.label === '处理告警')?.badge).toBe(3)
    expect(quickLinks(0).find((item) => item.label === '处理告警')?.badge).toBeUndefined()
  })
})

describe('告警与审计映射', () => {
  it('H1 只统计 firing 总数', () => {
    expect(firingAlertCount([alert(1), alert(2, 'resolved'), alert(3)])).toBe(2)
  })

  it('审计行选对象、操作人、详情与严重度，不展开 payload', () => {
    const event: AuditEvent = { id: 9, tenant_id: 1, event_class: 'account', event_type: 'account_disabled', severity: 'error', provider_account_id: 44, actor_id: 7, actor_role: 'platform_admin', reason: '健康检查失败', payload: { secret: '不可展示' }, created_at: '2026-07-12T15:04:00Z' }
    const row = auditRows([event], 5)[0]
    expect(row.object).toBe('上游账号 #44')
    expect(row.actor).toBe('platform_admin #7')
    expect(row.detail).toBe('健康检查失败')
    expect(row.tone).toBe('danger')
    expect(JSON.stringify(row)).not.toContain('不可展示')
  })
})
