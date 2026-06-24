import { describe, expect, it } from 'vitest'
import { accountAvailableActions } from './detail'
import type { ProviderAccount } from './types'

function fixture(over: Partial<ProviderAccount>): ProviderAccount {
  return {
    id: 1, tenant_id: 1, provider_id: 1, channel_id: 1, name: 'a', account_type: 'oauth',
    enabled: true, expires_at: null, health_state: 'active', credential_state: 'active',
    cap_concurrency: 4, in_flight_count: 0, priority: 0, static_weight: 1, probe_model: null,
    tags: [], last_dispatch_at: null, last_probe_latency_ms: null, last_probe_at: null,
    model_allow_list: [], capability_flags: [], rate_limited_at: null, rate_limit_reset_at: null,
    rate_limit_reason: null, overload_until: null, temp_unschedulable_until: null,
    token_version: 1, last_refresh_at: null, last_refresh_outcome: null,
    ...over,
  }
}

describe('accountAvailableActions', () => {
  it('非限流态:不展示清除限流', () => {
    // 判别核心:健康 active + 无 rate_limited_at → isRateLimited 必须 false。
    // 变异(把判定改成恒 true)→ 本断言 RED。
    expect(accountAvailableActions(fixture({})).isRateLimited).toBe(false)
  })

  it('有 rate_limited_at 时间戳 → 限流态', () => {
    expect(accountAvailableActions(fixture({ rate_limited_at: '2026-06-24T10:00:00Z' })).isRateLimited).toBe(true)
  })

  it('health_state=rate_limited → 限流态(即便无时间戳)', () => {
    expect(accountAvailableActions(fixture({ health_state: 'rate_limited' })).isRateLimited).toBe(true)
  })

  it('启停目标随 enabled 翻转', () => {
    expect(accountAvailableActions(fixture({ enabled: true })).toggleTo).toBe('disable')
    expect(accountAvailableActions(fixture({ enabled: false })).toggleTo).toBe('enable')
  })
})
