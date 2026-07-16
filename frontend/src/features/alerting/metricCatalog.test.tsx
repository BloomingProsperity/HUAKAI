import { renderToStaticMarkup } from 'react-dom/server'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  createRule: vi.fn(),
  deleteRule: vi.fn(),
  fetchMetricCatalog: vi.fn(),
  listRules: vi.fn(),
  updateRule: vi.fn(),
}))
vi.mock('./api', () => api)

import { loadMetricCatalogState, MetricCatalogSelect } from './RulesTab'
import type { AlertMetricCatalogEntry } from './types'

const entries: AlertMetricCatalogEntry[] = [
  {
    name: 'usage.request_count',
    label: '请求总数',
    unit: '次',
    description: '统计窗口内请求总数。',
    is_prefix: false,
  },
  {
    name: 'account.unhealthy_',
    label: '按状态统计异常账号',
    unit: '个',
    description: '后接健康状态。',
    is_prefix: true,
  },
]

describe('告警指标目录下拉', () => {
  beforeEach(() => api.fetchMetricCatalog.mockReset())

  it('下拉渲染 fetch mock 返回的目录项且不含无生产者的 CPU 指标', async () => {
    api.fetchMetricCatalog.mockResolvedValueOnce(entries)
    const state = await loadMetricCatalogState()
    const html = renderToStaticMarkup(
      <MetricCatalogSelect entries={state.entries} metric="usage.request_count" warning={state.warning} onMetricChange={() => {}} />,
    )
    expect(html).toContain('自定义(用指标名)')
    expect(html).toContain('请求总数')
    expect(html).toContain('usage.request_count')
    expect(html).toContain('account.unhealthy_')
    expect(html).not.toContain('cpu_usage_percent')
  })

  it('目录 fetch 失败时仅保留自定义选项并显示轻提示', async () => {
    api.fetchMetricCatalog.mockRejectedValueOnce(new Error('offline'))
    const state = await loadMetricCatalogState()
    const html = renderToStaticMarkup(
      <MetricCatalogSelect entries={state.entries} metric="" warning={state.warning} onMetricChange={() => {}} />,
    )
    expect(state.entries).toEqual([])
    expect(html).toContain('自定义(用指标名)')
    expect(html).toContain('指标目录加载失败')
    expect(html.match(/<option/g)).toHaveLength(1)
  })
})
