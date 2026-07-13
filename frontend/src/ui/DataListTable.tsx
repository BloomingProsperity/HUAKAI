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

export interface DataListTableProps<T> {
  columns: DataListColumn<T>[]
  rows: T[]
  rowKey: (row: T) => string | number
  action?: DataListAction<T>
  label: string
}

export function DataListTable<T>({ columns, rows, rowKey, action, label }: DataListTableProps<T>) {
  return (
    <div className="hk-tablewrap">
      <table className="hk-table" aria-label={label}>
        <thead><tr>{columns.map((column) => <th key={column.key} style={{ width: column.width }}>{column.label}</th>)}{action && <th>操作</th>}</tr></thead>
        <tbody>
          {rows.map((row) => (
            <tr key={rowKey(row)}>
              {columns.map((column) => <td key={column.key}>{column.badge ? <span style={badgeCellStyle}>{column.render(row)}</span> : column.render(row)}</td>)}
              {action && <td><Link to={action.to(row)} style={actionStyle}>{typeof action.label === 'function' ? action.label(row) : action.label}</Link></td>}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

const badgeCellStyle: CSSProperties = { display: 'inline-flex', alignItems: 'center' }
const actionStyle: CSSProperties = { fontSize: 12, fontWeight: 600, whiteSpace: 'nowrap' }
