import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { UserUsageCard } from './UserUsageCard'
import type { UserUsageRecord, UserUsageResponse } from './detail'

function record(over: Partial<UserUsageRecord> = {}): UserUsageRecord {
  return {
    requested_model: 'gpt-test',
    upstream_model: 'gpt-test-upstream',
    actual_cost: '0.10000000',
    tokens: { input: 100, output: 20, cache_creation: 3, cache_read: 5 },
    ledger_id: 'ledger-1',
    verify_hint: { trust_verify_path: '/v1/trust/verify', trust_verify_method: 'POST' },
    created_at: '2026-07-12T00:00:00Z',
    status: 'success',
    stream: false,
    ...over,
  }
}

describe('用户用量卡关键分支', () => {
  it('展示请求、Token、定点费用，并在有游标时明确不是全量', () => {
    const response: UserUsageResponse = {
      items: [record(), record({ actual_cost: '0.20000000', tokens: { input: 200, output: 30 }, status: 'error' })],
      next_cursor: 'opaque-next',
    }
    const html = renderToStaticMarkup(<UserUsageCard response={response} loading={false} error={null} />)
    expect(html).toContain('请求数')
    expect(html).toContain('输入 Token')
    expect(html).toContain('300')
    expect(html).toContain('输出 Token')
    expect(html).toContain('50')
    expect(html).toContain('0.30000000')
    expect(html).toContain('成功 1 / 错误 1 / 其他 0')
    expect(html).toContain('不代表全量历史')
  })

  it('空态与错误态可区分', () => {
    const empty = renderToStaticMarkup(
      <UserUsageCard response={{ items: [], next_cursor: '' }} loading={false} error={null} />,
    )
    expect(empty).toContain('暂无用量记录')

    const failed = renderToStaticMarkup(<UserUsageCard response={null} loading={false} error="usage_failed" />)
    expect(failed).toContain('usage_failed')
    expect(failed).not.toContain('暂无用量记录')
  })
})
