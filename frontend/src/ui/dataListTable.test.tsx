import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'
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

  it('混排逐行链接与按钮并保留危险操作语气', () => {
    const onDelete = vi.fn()
    const html = renderToStaticMarkup(<MemoryRouter><DataListTable
      label="账号列表"
      rows={[{ id: 3, locked: true }]}
      rowKey={(row) => row.id}
      columns={[{ key: 'id', label: 'ID', render: (row) => row.id }]}
      actions={[
        { label: '详情', to: (row) => `/accounts/${row.id}` },
        { label: '删除', onClick: onDelete, tone: 'danger' },
        { label: '启用', onClick: () => undefined, disabled: (row) => row.locked },
      ]}
    /></MemoryRouter>)

    expect(html).toContain('href="/accounts/3"')
    expect(html).toContain('class="hk-btn hk-btn--sm hk-btn--danger"')
    expect(html).toMatch(/<button[^>]*disabled=""[^>]*>启用<\/button>/)

    const row = { id: 3, locked: true }
    const tree = DataListTable({
      label: '账号列表',
      rows: [row],
      rowKey: (item) => item.id,
      columns: [{ key: 'id', label: 'ID', render: (item) => item.id }],
      actions: [{ label: '删除', onClick: onDelete, tone: 'danger' }],
    })
    const clickDelete = findElements(tree, 'button')[0].props.onClick as () => void
    clickDelete()
    expect(onDelete).toHaveBeenCalledWith(row)
  })

  it('逐行动作可按行隐藏，危险动作不会泄露到不合格行', () => {
    const html = renderToStaticMarkup(<DataListTable
      label="订单"
      rows={[{ id: 1, cancellable: true }, { id: 2, cancellable: false }]}
      rowKey={(row) => row.id}
      columns={[{ key: 'id', label: 'ID', render: (row) => row.id }]}
      actions={[{ label: '撤单', tone: 'danger', visible: (row) => row.cancellable, onClick: () => undefined }]}
    />)
    expect(html.match(/>撤单<\/button>/g)).toHaveLength(1)
    expect(html).toContain('hk-btn--danger')
  })

  it('只为可选行提供可用勾选框且全选范围排除不可选行', () => {
    const onToggle = vi.fn()
    const onToggleAll = vi.fn()
    const rows = [{ id: 1, locked: false }, { id: 2, locked: true }]
    const html = renderToStaticMarkup(<DataListTable
      label="批量账号"
      rows={rows}
      rowKey={(row) => row.id}
      columns={[{ key: 'id', label: 'ID', render: (row) => row.id }]}
      selectable={{
        selectedIds: new Set<string | number>([1, 2]),
        onToggle: () => undefined,
        onToggleAll,
        isSelectable: (row) => !row.locked,
      }}
    />)

    expect(html).toMatch(/aria-label="选择行 1"[^>]*checked=""/)
    expect(html).toMatch(/aria-label="选择行 2"[^>]*disabled=""/)
    expect(html).not.toMatch(/aria-label="选择行 2"[^>]*checked=""/)

    const tree = DataListTable({
      label: '批量账号',
      rows,
      rowKey: (row) => row.id,
      columns: [{ key: 'id', label: 'ID', render: (row) => row.id }],
      selectable: {
        selectedIds: new Set<string | number>(),
        onToggle,
        onToggleAll,
        isSelectable: (row) => !row.locked,
      },
    })
    const checkboxes = findElements(tree, 'input')
    const headerCheckbox = checkboxes[0]
    const toggleAll = headerCheckbox.props.onChange as () => void
    toggleAll()
    expect(onToggleAll).toHaveBeenCalledWith([1])
    const toggleFirstRow = checkboxes[1].props.onChange as () => void
    toggleFirstRow()
    expect(onToggle).toHaveBeenCalledWith(1)
  })

  it('不传 selectable 时完全不渲染勾选列', () => {
    const html = renderToStaticMarkup(<DataListTable
      label="普通列表"
      rows={[{ id: 1 }]}
      rowKey={(row) => row.id}
      columns={[{ key: 'id', label: 'ID', render: (row) => row.id }]}
    />)
    expect(html).not.toContain('type="checkbox"')
  })
})

function findElements(node: unknown, type: string): Array<{ props: Record<string, unknown> }> {
  if (!node || typeof node !== 'object') return []
  if (Array.isArray(node)) return node.flatMap((child) => findElements(child, type))
  const element = node as { type?: unknown; props?: { children?: unknown } }
  const own = element.type === type ? [element as { props: Record<string, unknown> }] : []
  return own.concat(findElements(element.props?.children, type))
}
