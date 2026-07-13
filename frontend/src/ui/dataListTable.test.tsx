import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { DataListTable } from './DataListTable'

describe('DataListTable', () => {
  it('复用列定义渲染徽标列与逐行操作', () => {
    const html = renderToStaticMarkup(<MemoryRouter><DataListTable
      label="待处理事项"
      rows={[{ id: 7, priority: '高', title: '异常告警' }]}
      rowKey={(row) => row.id}
      columns={[
        { key: 'priority', label: '优先级', badge: true, render: (row) => <span className="hk-pill--crit">{row.priority}</span> },
        { key: 'title', label: '事项', render: (row) => row.title },
      ]}
      action={{ label: '处理', to: (row) => `/alerts/${row.id}` }}
    /></MemoryRouter>)
    expect(html).toContain('aria-label="待处理事项"')
    expect(html).toContain('hk-pill--crit')
    expect(html).toContain('href="/alerts/7"')
  })
})
