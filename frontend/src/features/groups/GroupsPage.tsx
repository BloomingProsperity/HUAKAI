import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge, healthTone } from '../../ui/StatusBadge'
import { createPool, listPoolMembers, listPools, updatePool } from './api'
import {
  buildCreatePool,
  buildUpdatePool,
  CAPABILITY_DEFAULTS,
  capabilityLabel,
  EMPTY_POOL_FORM,
  toggleEnabledTarget,
  type PoolForm,
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
  const [pools, setPools] = useState<PoolGroup[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [createOpen, setCreateOpen] = useState(false)
  const [editing, setEditing] = useState<PoolGroup | null>(null)
  const [expandedId, setExpandedId] = useState<number | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback((signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    listPools(undefined, 200, signal)
      .then((resp) => setPools(resp.items ?? []))
      .catch((e: unknown) => {
        if (signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载分组列表失败')
      })
      .finally(() => {
        if (!signal.aborted) setLoading(false)
      })
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, refreshNonce])

  const refresh = () => setRefreshNonce((n) => n + 1)

  const toggleEnabled = async (p: PoolGroup) => {
    setBusyId(p.id)
    setError(null)
    try {
      await updatePool(p.id, { enabled: toggleEnabledTarget(p.enabled) })
      refresh()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '操作失败')
    } finally {
      setBusyId(null)
    }
  }

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--hk-space-3)' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
          <h1 style={{ fontSize: 22 }}>分组管理</h1>
          <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
            池组(逻辑容量分组)· 路由策略默认值与成员账号。共 {pools.length} 组。
          </p>
        </div>
        <button type="button" onClick={() => setCreateOpen(true)} style={newBtn}>
          ＋ 新建分组
        </button>
      </header>

      {createOpen && <PoolFormModal mode="create" onClose={() => setCreateOpen(false)} onDone={refresh} />}
      {editing && <PoolFormModal mode="edit" pool={editing} onClose={() => setEditing(null)} onDone={refresh} />}

      {error && (
        <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }}>{error}</div>
      )}

      <div style={{ background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }}>
        {loading && pools.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : pools.length === 0 ? (
          <Empty>还没有分组。点击右上角新建。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['名称', '能力默认', 'TopK', '兜底', '状态', '创建时间', ''].map((h) => (
                    <th key={h} style={th}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {pools.map((p) => (
                  <PoolRow
                    key={p.id}
                    pool={p}
                    busy={busyId === p.id}
                    expanded={expandedId === p.id}
                    onToggleExpand={() => setExpandedId((cur) => (cur === p.id ? null : p.id))}
                    onEdit={() => setEditing(p)}
                    onToggleEnabled={() => toggleEnabled(p)}
                  />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}

function PoolRow({
  pool,
  busy,
  expanded,
  onToggleExpand,
  onEdit,
  onToggleEnabled,
}: {
  pool: PoolGroup
  busy: boolean
  expanded: boolean
  onToggleExpand: () => void
  onEdit: () => void
  onToggleEnabled: () => void
}) {
  return (
    <>
      <tr style={{ borderTop: '1px solid var(--hk-line)' }}>
        <td style={td}>
          <button type="button" onClick={onToggleExpand} style={{ ...linkBtn, fontWeight: 600, color: 'var(--hk-primary-700)', padding: 0 }}>
            {expanded ? '▾ ' : '▸ '}
            {pool.name}
          </button>
        </td>
        <td style={td}>{capabilityLabel(pool.capability_default)}</td>
        <td style={tdNum}>{pool.top_k_default}</td>
        <td style={td}>
          <StatusBadge tone={pool.allow_last_resort ? 'info' : 'muted'}>
            {pool.allow_last_resort ? '允许兜底' : '关闭'}
          </StatusBadge>
        </td>
        <td style={td}>
          <StatusBadge tone={pool.enabled ? 'ok' : 'muted'}>{pool.enabled ? '启用' : '已禁用'}</StatusBadge>
        </td>
        <td style={td}>{fmt(pool.created_at)}</td>
        <td style={{ ...td, textAlign: 'right', whiteSpace: 'nowrap' }}>
          <button type="button" disabled={busy} onClick={onEdit} style={linkBtn}>编辑</button>
          <button type="button" disabled={busy} onClick={onToggleEnabled} style={linkBtn}>
            {pool.enabled ? '禁用' : '启用'}
          </button>
        </td>
      </tr>
      {expanded && (
        <tr style={{ borderTop: '1px solid var(--hk-line)' }}>
          <td colSpan={7} style={{ padding: 0, background: 'var(--hk-surface-sunken)' }}>
            <MemberPanel poolID={pool.id} tenantID={pool.tenant_id} />
          </td>
        </tr>
      )}
    </>
  )
}

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
        <div style={{ fontSize: 13, color: '#8f322a' }}>{error}</div>
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
  pool,
  onClose,
  onDone,
}: {
  mode: 'create' | 'edit'
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
        await createPool(built)
      } else if (pool) {
        const built = buildUpdatePool(form, initial)
        if ('error' in built) {
          setError(built.error)
          return
        }
        setBusy(true)
        await updatePool(pool.id, built, pool.tenant_id)
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
          <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }}>{error}</div>
        )}
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', justifyContent: 'flex-end' }}>
          <button type="button" onClick={onClose} style={ghostBtn}>取消</button>
          <button type="button" disabled={busy} onClick={submit} style={primaryBtn}>
            {busy ? '保存中…' : '保存'}
          </button>
        </div>
      </div>
    </div>
  )
}

function fmt(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false })
}
function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}
function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
const tdNum: React.CSSProperties = { ...td, textAlign: 'right', fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const newBtn: React.CSSProperties = { height: 36, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 14, fontWeight: 600, cursor: 'pointer', flexShrink: 0 }
const linkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
