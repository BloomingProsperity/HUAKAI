import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { DataListTable } from '../../ui/DataListTable'
import { BindingModal } from './BindingModal'
import { bindingColumns } from './RoutingPage'
import { filterBindingRows, mapBindingRows } from './selection'
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

describe('路由绑定 UI 暴露全部运行时字段', () => {
  it('创建与编辑表单均展示五类选择、当前说明与并发上限', () => {
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
      expect(html).toContain('降级类 (fallback_class)')
      expect(html).toContain('normal · 主类')
      expect(html).toContain('context_window · 上下文')
      expect(html).toContain('safety · 内容安全')
      expect(html).toContain('quota · 限流配额')
      expect(html).toContain('manual · 瞬态兜底')
      expect(html).toContain('最大并发请求数')
      expect(html).toContain('0 或留空表示不限')
    }
    expect(createHTML).toContain('请求总从 normal 开始')
    expect(editHTML).toContain('承接绑定、账号或上游容量耗尽')

    const contextHTML = renderToStaticMarkup(
      <BindingModal tenantId={7} binding={{ ...legacyBinding, fallback_class: 'context_window' }} onClose={() => undefined} onSaved={() => undefined} />,
    )
    expect(contextHTML).toContain('需管理员确认目标池/模型确有更大窗口，系统不代验')
  })

  it('列表 DOM 渲染紧凑 class badge，并可用筛选结果只展示目标类', () => {
    const rows = filterBindingRows(
      mapBindingRows([legacyBinding, { ...legacyBinding, id: 72, fallback_class: undefined }]),
      'quota',
    )
    const html = renderToStaticMarkup(
      <DataListTable label="路由绑定列表" rows={rows} rowKey={(row) => row.id} columns={bindingColumns} />,
    )

    expect(html).toContain('优先级')
    expect(html).toContain('选号策略')
    expect(html).toContain('按权重加权')
    expect(html).toContain('降级类')
    expect(html).toContain('quota · 限流配额')
    expect(html).not.toContain('normal · 主类')
    // weight 仍不是可操作列，独特 fixture 值不得泄入 DOM。
    expect(html).not.toContain('9473')
  })
})
