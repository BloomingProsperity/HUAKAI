import type { CSSProperties, ReactNode } from 'react'
import { Link } from 'react-router-dom'

export interface DataListColumn<T> {
  key: string
  label: string
  render: (row: T) => ReactNode
  width?: string | number
  badge?: boolean
}

export interface DataListAction<T> {
  label: string | ((row: T) => string)
  to: (row: T) => string
}

export interface DataListLinkAction<T> {
  label: string | ((row: T) => string)
  to: string | ((row: T) => string)
  onClick?: never
}

export interface DataListButtonAction<T> {
  label: string | ((row: T) => string)
  onClick: (row: T) => void
  tone?: 'danger' | 'neutral'
  disabled?: boolean | ((row: T) => boolean)
  to?: never
}

export type DataListRowAction<T> = DataListLinkAction<T> | DataListButtonAction<T>

export interface DataListSelection<T> {
  selectedIds: Set<string | number>
  onToggle: (id: string | number) => void
  onToggleAll: (ids: Array<string | number>) => void
  isSelectable?: (row: T) => boolean
}

export interface DataListTableProps<T> {
  columns: DataListColumn<T>[]
  rows: T[]
  rowKey: (row: T) => string | number
  action?: DataListAction<T>
  actions?: DataListRowAction<T>[]
  selectable?: DataListSelection<T>
  label: string
}

export function DataListTable<T>({ columns, rows, rowKey, action, actions = [], selectable, label }: DataListTableProps<T>) {
  const selectableIds = selectable
    ? rows.filter((row) => selectable.isSelectable?.(row) ?? true).map(rowKey)
    : []
  const allSelected = selectableIds.length > 0 && selectableIds.every((id) => selectable?.selectedIds.has(id))
  const someSelected = !allSelected && selectableIds.some((id) => selectable?.selectedIds.has(id))
  const hasActions = Boolean(action || actions.length)

  return (
    <div className="hk-tablewrap">
      <table className="hk-table" aria-label={label}>
        <thead><tr>
          {selectable && <th style={selectionCellStyle}><input type="checkbox" aria-label="全选可选行" aria-checked={someSelected ? 'mixed' : allSelected} checked={allSelected} disabled={selectableIds.length === 0} onChange={() => selectable.onToggleAll(selectableIds)} /></th>}
          {columns.map((column) => <th key={column.key} style={{ width: column.width }}>{column.label}</th>)}
          {hasActions && <th>操作</th>}
        </tr></thead>
        <tbody>
          {rows.map((row) => {
            const id = rowKey(row)
            const rowSelectable = selectable?.isSelectable?.(row) ?? true
            return (
              <tr key={id}>
                {selectable && <td style={selectionCellStyle}><input type="checkbox" aria-label={`选择行 ${String(id)}`} checked={rowSelectable && selectable.selectedIds.has(id)} disabled={!rowSelectable} onChange={() => selectable.onToggle(id)} /></td>}
                {columns.map((column) => <td key={column.key}>{column.badge ? <span style={badgeCellStyle}>{column.render(row)}</span> : column.render(row)}</td>)}
                {hasActions && <td><span style={actionsStyle}>
                  {action && <Link to={action.to(row)} style={actionStyle}>{resolveValue(action.label, row)}</Link>}
                  {actions.map((item, index) => isLinkAction(item)
                    ? <Link key={index} to={resolveValue(item.to, row)} style={actionStyle}>{resolveValue(item.label, row)}</Link>
                    : <button key={index} type="button" className={`hk-btn hk-btn--sm${item.tone === 'danger' ? ' hk-btn--danger' : ''}`} disabled={resolveValue(item.disabled ?? false, row)} onClick={() => item.onClick(row)}>{resolveValue(item.label, row)}</button>)}
                </span></td>}
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

function resolveValue<T, V>(value: V | ((row: T) => V), row: T): V {
  return typeof value === 'function' ? (value as (item: T) => V)(row) : value
}

function isLinkAction<T>(action: DataListRowAction<T>): action is DataListLinkAction<T> {
  return action.to !== undefined
}

const badgeCellStyle: CSSProperties = { display: 'inline-flex', alignItems: 'center' }
const actionStyle: CSSProperties = { fontSize: 12, fontWeight: 600, whiteSpace: 'nowrap' }
const actionsStyle: CSSProperties = { display: 'inline-flex', alignItems: 'center', flexWrap: 'wrap', gap: 'var(--hk-space-2)' }
const selectionCellStyle: CSSProperties = { width: 36, textAlign: 'center' }
