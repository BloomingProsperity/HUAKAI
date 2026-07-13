import { describe, expect, it } from 'vitest'
import { filterAccountRows, mapAccountRows, mapAccountStats } from './accounts'
import type { ProviderAccount } from './types'

describe('账号列表数据映射', () => {
  it('需关注大于零时使用 warn，归零时恢复 ok', () => {
    const warning = mapAccountStats({ total: 51, enabled: 5, disabled: 46, needs_attention: 46, states: [] })
    const healthy = mapAccountStats({ total: 5, enabled: 5, disabled: 0, needs_attention: 0, states: [] })

    // 判别核心：把需关注误用 danger/default，或忽略计数恒为 warn，都会使断言变红。
    expect(warning[3]).toMatchObject({ label: '需关注', value: '46', tone: 'warn' })
    expect(healthy[3]).toMatchObject({ label: '需关注', value: '0', tone: 'ok' })
    expect(warning[0]).toMatchObject({ label: '账号总数', value: '51' })
  })

  it('十列表格映射保留操作源并区分状态与非法时间', () => {
    const source = account({
      id: 9,
      enabled: false,
      health_state: 'throttled',
      credential_state: 'revoked',
      last_dispatch_at: 'not-a-date',
    })
    const row = mapAccountRows([source])[0]

    // 判别核心：状态若被写死为正常，以下三种语义会同时失真。
    expect(row.source).toBe(source)
    expect(row.enabledText).toBe('已停用')
    expect(row.enabledTone).toBe('muted')
    expect(row.healthTone).not.toBe('ok')
    expect(row.credentialTone).toBe('danger')
    expect(row.lastDispatchAt).toBe('—')
  })

  it('名称与健康态组合过滤且不改写原数组', () => {
    const rows = mapAccountRows([
      account({ id: 1, name: 'Prod Alpha', health_state: 'healthy' }),
      account({ id: 2, name: 'Prod Beta', health_state: 'cooldown' }),
      account({ id: 3, name: 'Dev Alpha', health_state: 'cooldown' }),
    ])

    // 判别核心：若两项条件误用 OR，会错误返回三行；若忽略健康态，会返回两行。
    expect(filterAccountRows(rows, ' prod ', 'cooldown').map((row) => row.id)).toEqual([2])
    expect(filterAccountRows(rows, '', 'healthy').map((row) => row.id)).toEqual([1])
    expect(rows).toHaveLength(3)
  })
})

function account(overrides: Partial<ProviderAccount>): ProviderAccount {
  return {
    id: 1,
    name: '账号',
    account_type: 'openai',
    enabled: true,
    health_state: 'healthy',
    credential_state: 'active',
    tags: ['prod'],
    in_flight_count: 2,
    priority: 10,
    static_weight: 100,
    cap_concurrency: 5,
    last_dispatch_at: '2026-07-13T00:00:00Z',
    ...overrides,
  } as ProviderAccount
}
