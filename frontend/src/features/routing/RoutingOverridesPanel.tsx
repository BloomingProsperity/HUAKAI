import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { deleteRoutingOverride, listRoutingOverrides } from './api'
import { RoutingOverrideModal } from './RoutingOverrideModal'
import type { ModelRoutingOverride } from './types'

export function RoutingOverridesPanel({ tenantId }: { tenantId: number }) {
  const [items, setItems] = useState<ModelRoutingOverride[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [busyID, setBusyID] = useState<number | null>(null)
  const [modalItem, setModalItem] = useState<ModelRoutingOverride | null | undefined>(undefined)

  const load = useCallback((signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    listRoutingOverrides(tenantId, signal)
      .then((response) => setItems(response.items))
      .catch((caught: unknown) => {
        if (signal.aborted) return
        setError(caught instanceof ApiError ? `${caught.message}(${caught.code})` : '加载强制 pin 失败')
      })
      .finally(() => {
        if (!signal.aborted) setLoading(false)
      })
  }, [tenantId])

  useEffect(() => {
    const controller = new AbortController()
    load(controller.signal)
    return () => controller.abort()
  }, [load, refreshNonce])

  const refresh = () => setRefreshNonce((value) => value + 1)
  const remove = async (item: ModelRoutingOverride) => {
    if (!window.confirm(`确认删除 #${item.id}（${item.model} · pool #${item.pool_group_id}）？`)) return
    setBusyID(item.id)
    setError(null)
    try {
      await deleteRoutingOverride(item.id, tenantId)
      refresh()
    } catch (caught) {
      setError(caught instanceof ApiError ? `${caught.message}(${caught.code})` : '删除强制 pin 失败')
    } finally {
      setBusyID(null)
    }
  }

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <div style={panelHeader}>
        <div>
          <h2 style={{ margin: 0, fontSize: 16 }}>池内模型账号强制 pin</h2>
          <p style={panelHint}>命中后只保留配置账号与当前可用候选的交集；不会改变模型→池绑定。</p>
        </div>
        <button type="button" className="hk-btn hk-btn--green" onClick={() => setModalItem(null)}>＋ 新建强制 pin</button>
      </div>

      {error && <div style={errorBox}>{error}</div>}
      <div className="hk-card">
        <RoutingOverrideList
          items={items}
          loading={loading}
          busyID={busyID}
          onEdit={setModalItem}
          onDelete={(item) => void remove(item)}
        />
      </div>

      {modalItem !== undefined && (
        <RoutingOverrideModal
          tenantId={tenantId}
          item={modalItem}
          onClose={() => setModalItem(undefined)}
          onSaved={refresh}
        />
      )}
    </section>
  )
}

export function RoutingOverrideList({
  items,
  loading,
  busyID,
  onEdit,
  onDelete,
}: {
  items: ModelRoutingOverride[]
  loading: boolean
  busyID: number | null
  onEdit: (item: ModelRoutingOverride | null) => void
  onDelete: (item: ModelRoutingOverride) => void
}) {
  if (loading && items.length === 0) {
    return <EmptyState title="正在加载强制 pin" hint="请稍候。" />
  }
  if (items.length === 0) {
    return (
      <EmptyState
        title="没有强制 pin"
        hint="新建强制 pin，把一个模型在指定池组内收窄到明确账号子集。"
        action={{ label: '新建强制 pin', onClick: () => onEdit(null) }}
      />
    )
  }
  return (
    <DataListTable
      label="模型账号强制 pin 列表"
      rows={items}
      rowKey={(item) => item.id}
      columns={routingOverrideColumns}
      actions={[
        { label: '编辑', disabled: (item) => busyID === item.id, onClick: onEdit },
        { label: '删除', tone: 'danger', disabled: (item) => busyID === item.id, onClick: onDelete },
      ]}
    />
  )
}

const routingOverrideColumns: DataListColumn<ModelRoutingOverride>[] = [
  { key: 'model', label: '模型', render: (item) => <span className="hk-mono">{item.model}</span> },
  { key: 'pool', label: '池组', render: (item) => <span className="hk-mono">#{item.pool_group_id}</span> },
  {
    key: 'accounts',
    label: '账号子集',
    render: (item) => <span className="hk-mono">{item.provider_account_ids.map((id) => `#${id}`).join('、')}</span>,
  },
  {
    key: 'status',
    label: '状态',
    badge: true,
    render: (item) => <StatusBadge tone={item.enabled ? 'ok' : 'muted'}>{item.enabled ? '启用' : '停用'}</StatusBadge>,
  },
  { key: 'updated', label: '更新时间', render: (item) => <span className="hk-mono">{formatTimestamp(item.updated_at)}</span> },
]

function formatTimestamp(value: string): string {
  const parsed = new Date(value)
  return Number.isNaN(parsed.getTime()) ? value : parsed.toLocaleString('zh-CN', { hour12: false })
}

const panelHeader: React.CSSProperties = { display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--hk-space-4)', flexWrap: 'wrap' }
const panelHint: React.CSSProperties = { margin: '4px 0 0', color: 'var(--hk-ink-500)', fontSize: 12, lineHeight: 1.6 }
const errorBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', border: '1px solid var(--hk-danger-soft)', background: 'var(--hk-danger-soft)', color: 'var(--hk-danger)', fontSize: 13 }
