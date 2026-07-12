import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { KeyGenerationDetail, KeyUsageChart, KeyUsageTable, UsageHeatPanels } from './KeyUsageAnalytics'
import { HeatMap, MeterCells } from './Heatmaps'
import { buildHeatGrid, pickCost } from './heatmap'
import type { KeyUsageRecord, KeyUsageTimeSeriesResponse } from './types'

const record: KeyUsageRecord = {
  requested_model: 'gpt-5',
  upstream_model: 'gpt-5-2026',
  actual_cost: '0.01230000',
  tokens: { input: 10, output: 20, cache_read: 3 },
  provider: 'openai',
  provider_account_id: 8,
  ledger_id: 'ledger-1',
  verify_hint: { trust_verify_path: '/v1/trust/verify', trust_verify_method: 'POST' },
  created_at: '2026-07-12T10:00:00Z',
  requested_at: '2026-07-12T09:59:59Z',
  status: 'non_streaming',
  request_id: 'req-123',
  stream: false,
}

const series: KeyUsageTimeSeriesResponse = {
  period: { from: '2026-07-01T00:00:00Z', to: '2026-07-13T00:00:00Z' },
  items: [
    {
      day: '2026-07-12',
      requested_model: 'gpt-5',
      total_cost: '0.01230000',
      tokens: { input: 10, output: 20, cache_read: 3, cache_creation: 0 },
      request_count: 2,
    },
  ],
}

describe('Key 级分析关键渲染', () => {
  it('时间序列使用既有 hk-bar，展示费用、请求数与 Token', () => {
    const html = renderToStaticMarkup(<KeyUsageChart response={series} loading={false} />)
    expect(html).toContain('class="hk-bar"')
    expect(html).toContain('2026-07-12')
    expect(html).toContain('2 请求 · 33 Token')
    expect(html).toContain('$0.0123')
  })

  it('逐笔结果使用 hk-table，存在游标时出现 hk-loadmore', () => {
    const html = renderToStaticMarkup(
      <KeyUsageTable
        records={[record]}
        analyzed
        loading={false}
        nextCursor="opaque"
        loadingMore={false}
        onLoadMore={vi.fn()}
      />,
    )
    expect(html).toContain('class="hk-table"')
    expect(html).toContain('class="hk-loadmore"')
    expect(html).toContain('req-123')
    expect(html).toContain('gpt-5 → gpt-5-2026')
  })

  it('单笔详情使用 hk-kv，完整展示作用域内核心字段', () => {
    const html = renderToStaticMarkup(<KeyGenerationDetail record={record} />)
    expect(html).toContain('class="hk-kv"')
    expect(html).toContain('req-123')
    expect(html).toContain('ledger-1')
    expect(html).toContain('openai')
    expect(html).toContain('$0.0123')
  })
})

describe('小方格显示', () => {
  it('分段方格条按百分比填格(50% → 12 个 is-on,共 24)', () => {
    const html = renderToStaticMarkup(<MeterCells pct={50} />)
    // 收紧:cell 后跟空格或引号,避免匹配到容器 hk-meter__cells
    const total = (html.match(/hk-meter__cell[ "]/g) || []).length
    const on = (html.match(/is-on/g) || []).length
    expect(total).toBe(24)
    expect(on).toBe(12)
  })

  it('热力网格每天一格,带强度档 class 与坐标', () => {
    const grid = buildHeatGrid(series.items, pickCost)
    const html = renderToStaticMarkup(<HeatMap grid={grid} title="用量热力" formatValue={(v) => `$${v.toFixed(4)}`} unit="" />)
    expect(html).toContain('class="hk-heat"')
    expect(html).toContain('hk-heat__cell--l4') // 单点即最大值 → 满档
    expect(html).toContain('2026-07-12')
  })

  it('有 time-series 时渲染用量与缓存两张热力 + 命中方格条', () => {
    const html = renderToStaticMarkup(<UsageHeatPanels response={series} />)
    expect(html).toContain('用量热力')
    expect(html).toContain('缓存热力')
    expect(html).toContain('缓存命中占比')
  })

  it('无 series 不渲染(null)', () => {
    expect(renderToStaticMarkup(<UsageHeatPanels response={null} />)).toBe('')
  })
})
