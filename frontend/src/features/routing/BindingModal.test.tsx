import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { DataListTable } from '../../ui/DataListTable'
import { BindingModal } from './BindingModal'
import { bindingColumns } from './RoutingPage'
import { mapBindingRows } from './selection'
import type { PoolBinding } from './types'

const legacyBinding: PoolBinding = {
  id: 71,
  model_id: 11,
  pool_group_id: 22,
  priority: 3,
  weight: 9473,
  selection_mode: 'priority_weighted',
  max_parallel_requests: 37,
  fallback_class: 'quota',
  enabled: true,
}

describe('路由绑定 UI 只暴露真实生效字段', () => {
  it('创建与编辑表单均展示并发上限，不出现两个仅存储字段', () => {
    const createHTML = renderToStaticMarkup(
      <BindingModal tenantId={7} binding={null} onClose={() => undefined} onSaved={() => undefined} />,
    )
    const editHTML = renderToStaticMarkup(
      <BindingModal tenantId={7} binding={legacyBinding} onClose={() => undefined} onSaved={() => undefined} />,
    )

    for (const html of [createHTML, editHTML]) {
      expect(html).toContain('优先级')
      expect(html).toContain('选号策略')
      expect(html).not.toContain('权重(priority_weighted 时生效)')
      expect(html).not.toContain('兜底类')
      expect(html).toContain('最大并发请求数')
      expect(html).toContain('0 或留空表示不限')
    }
  })

  it('列表 DOM 忽略旧响应中的权重与兜底类,仍展示有效列', () => {
    const rows = mapBindingRows([legacyBinding])
    const html = renderToStaticMarkup(
      <DataListTable label="路由绑定列表" rows={rows} rowKey={(row) => row.id} columns={bindingColumns} />,
    )

    expect(html).toContain('优先级')
    expect(html).toContain('选号策略')
    expect(html).toContain('按权重加权')
    // 变异:把任一死列加回 bindingColumns,独特 fixture 值或列名会使断言转红。
    expect(html).not.toContain('9473')
    expect(html).not.toContain('兜底类')
    expect(html).not.toContain('配额')
  })
})
