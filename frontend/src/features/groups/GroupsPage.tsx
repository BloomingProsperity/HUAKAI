import { useCallback, useEffect, useState } from 'react'
import { useMe } from '../../auth/me'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge, healthTone } from '../../ui/StatusBadge'
import { createPool, listPoolMembers, listPools, updatePool } from './api'
import {
  buildCreatePool,
  buildUpdatePool,
  CAPABILITY_DEFAULTS,
  EMPTY_POOL_FORM,
  mapPoolRows,
  toggleEnabledTarget,
  type PoolForm,
  type PoolTableRow,
} from './groups'
import type { PoolGroup, PoolMemberAccount } from './types'

/*
 * 分组管理(池组 / pool_group,运维台 admin 壳)。/admin/v1/pools 列表 + 新建 + 编辑 + 启停,
 * 并按池组只读展开成员账号(/admin/v1/provider-accounts?pool_group_id=)。
 *
 * 设计取舍(均依后端真码):
 *  - 后端无 DELETE 端点 → "删除"以禁用(PATCH enabled=false)表达,保留路由历史(软停用)。
 *  - pool_groups schema 无 description/tags 列 → 表单不暴露,避免写无消费的死字段。
 *  - 成员账号此处只读;增删成员是账号中心(provider-accounts)的职责,不在本页。
 */
export function GroupsPage() {
  const tenantId = useMe().tenantId
  const [pools, setPools] = useState<PoolGroup[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [createOpen, setCreateOpen] = useState(false)
  const [editing, setEditing] = useState<PoolGroup | null>(null)
  const [expandedId, setExpandedId] = useState<number | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback((signal: AbortSignal) => {
    if (tenantId == null) return
    setLoading(true)
    setError(null)
    listPools(tenantId, 200, signal)
      .then((resp) => setPools(resp.items ?? []))
      .catch((e: unknown) => {
        if (signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载分组列表失败')
      })
      .finally(() => {
        if (!signal.aborted) setLoading(false)
      })
  }, [tenantId])

  useEffect(() => {
    if (tenantId == null) return
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, refreshNonce])

  const refresh = () => setRefreshNonce((n) => n + 1)

  const toggleEnabled = async (p: PoolGroup) => {
    setBusyId(p.id)
    setError(null)
    try {
      if (tenantId == null) return
      await updatePool(p.id, { enabled: toggleEnabledTarget(p.enabled) }, tenantId)
      refresh()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '操作失败')
    } finally {
      setBusyId(null)
    }
  }
  const rows = mapPoolRows(pools)

  if (tenantId == null) {
    return <EmptyState title="正在加载租户上下文" hint="请稍候。" />
  }

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>分组管理</h1>
          <p className="hk-sub">
            池组(逻辑容量分组)· 路由策略默认值与成员账号。共 {pools.length} 组。
          </p>
        </div>
        <button type="button" onClick={() => setCreateOpen(true)} className="hk-btn hk-btn--green">
          ＋ 新建分组
        </button>
      </header>

      {createOpen && <PoolFormModal mode="create" tenantID={tenantId} onClose={() => setCreateOpen(false)} onDone={refresh} />}
      {editing && <PoolFormModal mode="edit" tenantID={tenantId} pool={editing} onClose={() => setEditing(null)} onDone={refresh} />}

      {error && (
        <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{error}</div>
      )}

      <div className="hk-card">
        {loading && pools.length === 0 ? (
          <EmptyState title="正在加载分组" hint="请稍候。" />
        ) : pools.length === 0 ? (
          <EmptyState title="还没有分组" hint="点击右上角「新建分组」开始配置。" />
        ) : (
          <>
            <DataListTable
              label="分组列表"
              rows={rows}
              rowKey={(row) => row.id}
              columns={poolColumns}
              actions={[
                { label: (row) => expandedId === row.id ? '收起成员' : '查看成员', onClick: (row) => setExpandedId((cur) => cur === row.id ? null : row.id) },
                { label: '编辑', disabled: (row) => busyId === row.id, onClick: (row) => setEditing(row.pool) },
                { label: (row) => row.pool.enabled ? '禁用' : '启用', disabled: (row) => busyId === row.id, onClick: (row) => void toggleEnabled(row.pool) },
              ]}
            />
            {expandedId !== null && pools.find((pool) => pool.id === expandedId) && (
              <MemberPanel
                poolID={expandedId}
                tenantID={tenantId}
              />
            )}
          </>
        )}
      </div>
    </div>
  )
}

const poolColumns: DataListColumn<PoolTableRow>[] = [
  { key: 'name', label: '名称', render: (row) => <span style={{ fontWeight: 600 }}>{row.name}</span> },
  { key: 'capability', label: '能力默认', render: (row) => row.capability },
  { key: 'top-k', label: 'TopK', render: (row) => <span className="hk-mono">{row.topK}</span> },
  { key: 'fallback', label: '兜底', badge: true, render: (row) => <StatusBadge tone={row.fallbackTone}>{row.fallback}</StatusBadge> },
  { key: 'status', label: '状态', badge: true, render: (row) => <StatusBadge tone={row.statusTone}>{row.status}</StatusBadge> },
  { key: 'created-at', label: '创建时间', render: (row) => <span className="hk-mono">{row.createdAt}</span> },
]

function MemberPanel({ poolID, tenantID }: { poolID: number; tenantID: number }) {
  const [members, setMembers] = useState<PoolMemberAccount[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError(null)
    listPoolMembers(poolID, tenantID, ctrl.signal)
      .then((resp) => setMembers(resp.items ?? []))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载成员账号失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [poolID, tenantID])

  return (
    <div style={{ padding: 'var(--hk-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
      <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)' }}>
        成员账号(只读 · 增删请到账号中心)
      </div>
      {error ? (
        <div style={{ fontSize: 13, color: 'var(--hk-danger)' }}>{error}</div>
      ) : loading ? (
        <div style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>加载中…</div>
      ) : members.length === 0 ? (
        <div style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>该分组下暂无成员账号。</div>
      ) : (
        <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
          {members.map((m) => (
            <li key={m.id} style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)', fontSize: 13 }}>
              <span style={{ minWidth: 160, fontWeight: 500 }}>{m.name}</span>
              <span style={{ color: 'var(--hk-ink-500)' }}>{m.account_type}</span>
              <StatusBadge tone={healthTone(m.health_state)}>{m.health_state}</StatusBadge>
              {!m.enabled && <StatusBadge tone="muted">已禁用</StatusBadge>}
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

function PoolFormModal({
  mode,
  tenantID,
  pool,
  onClose,
  onDone,
}: {
  mode: 'create' | 'edit'
  tenantID: number
  pool?: PoolGroup
  onClose: () => void
  onDone: () => void
}) {
  const initial: PoolForm = pool
    ? {
        name: pool.name,
        topKDefault: pool.top_k_default,
        capabilityDefault: pool.capability_default,
        allowLastResort: pool.allow_last_resort,
      }
    : EMPTY_POOL_FORM
  const [form, setForm] = useState<PoolForm>(initial)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const set = <K extends keyof PoolForm>(k: K, v: PoolForm[K]) => setForm((f) => ({ ...f, [k]: v }))

  const submit = async () => {
    setError(null)
    try {
      if (mode === 'create') {
        const built = buildCreatePool(form)
        if ('error' in built) {
          setError(built.error)
          return
        }
        setBusy(true)
        await createPool(built, tenantID)
      } else if (pool) {
        const built = buildUpdatePool(form, initial)
        if ('error' in built) {
          setError(built.error)
          return
        }
        setBusy(true)
        await updatePool(pool.id, built, tenantID)
      }
      onDone()
      onClose()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div onClick={onClose} style={{ position: 'fixed', inset: 0, background: 'rgba(28,38,34,0.4)', display: 'flex', alignItems: 'flex-start', justifyContent: 'center', padding: 'var(--hk-space-6)', zIndex: 'var(--hk-z-overlay)' as unknown as number, overflowY: 'auto' }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 'min(460px,100%)', background: 'var(--hk-surface)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-3)', padding: 'var(--hk-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
        <h2 style={{ fontSize: 18 }}>{mode === 'create' ? '新建分组' : '编辑分组'}</h2>
        <Field label="分组名称(≤64 字)">
          <input value={form.name} onChange={(e) => set('name', e.target.value)} style={inp} />
        </Field>
        <Field label="TopK 默认(1..10)">
          <input
            type="number"
            min={1}
            max={10}
            value={form.topKDefault}
            onChange={(e) => set('topKDefault', Number(e.target.value))}
            style={inp}
          />
        </Field>
        <Field label="能力默认">
          <select value={form.capabilityDefault} onChange={(e) => set('capabilityDefault', e.target.value)} style={inp}>
            {CAPABILITY_DEFAULTS.map((c) => (
              <option key={c.value} value={c.value}>{c.label}</option>
            ))}
          </select>
        </Field>
        <label style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', fontSize: 13, color: 'var(--hk-ink-700)' }}>
          <input type="checkbox" checked={form.allowLastResort} onChange={(e) => set('allowLastResort', e.target.checked)} />
          允许兜底路由(allow_last_resort)
        </label>
        {error && (
          <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{error}</div>
        )}
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', justifyContent: 'flex-end' }}>
          <button type="button" onClick={onClose} className="hk-btn">取消</button>
          <button type="button" disabled={busy} onClick={submit} className="hk-btn hk-btn--green">
            {busy ? '保存中…' : '保存'}
          </button>
        </div>
      </div>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
