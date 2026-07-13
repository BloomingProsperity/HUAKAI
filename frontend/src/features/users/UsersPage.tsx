import { useCallback, useEffect, useState, type CSSProperties } from 'react'
import { Link } from 'react-router-dom'
import { useMe } from '../../auth/me'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { SkeletonRows } from '../../ui/Skeleton'
import { StatCard } from '../../ui/StatCard'
import { StatusBadge } from '../../ui/StatusBadge'
import { createUser, getTwoFAAdoptionStats, listUsers, setUserStatus, unlockUser } from './api'
import type { TwoFAAdoptionStats } from './actions'
import {
  buildCreateUser,
  CREATE_USER_ROLES,
  EMPTY_CREATE_USER,
  mapUserPagination,
  mapUserRows,
  mapUserStats,
  toggleStatusTarget,
  USERS_PAGE_LIMIT,
  USERS_PAGE_LIMIT_OPTIONS,
  type CreateUserForm,
  type UserTableRow,
} from './users'
import type { AdminUser } from './types'

/*
 * 用户管理(运维台,P0)。管线第 5 站。/admin/v1/users 列表 + 搜索 + 创建 + 启停 + 解锁。
 * 余额列只读展示(money 只读,不在此动钱)。
 */
export function UsersPage() {
  const tenantId = useMe().tenantId
  const [users, setUsers] = useState<AdminUser[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [draftQ, setDraftQ] = useState('')
  const [q, setQ] = useState('')
  const [offset, setOffset] = useState(0)
  const [limit, setLimit] = useState(USERS_PAGE_LIMIT)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [createOpen, setCreateOpen] = useState(false)
  const [busyId, setBusyId] = useState<number | null>(null)
  const [twoFA, setTwoFA] = useState<TwoFAAdoptionStats | null>(null)
  const [twoFALoading, setTwoFALoading] = useState(true)

  const load = useCallback(
    (signal: AbortSignal) => {
      if (tenantId == null) return
      setLoading(true)
      setError(null)
      setUsers([])
      listUsers(tenantId, q, offset, limit, signal)
        .then((resp) => setUsers(resp.items))
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载用户列表失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [limit, offset, q, tenantId],
  )

  useEffect(() => {
    if (tenantId == null) return
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, refreshNonce])

  // 2FA 普及率统计独立加载,失败静默(只是少一张统计卡,不连累列表)。
  useEffect(() => {
    if (tenantId == null) return
    const ctrl = new AbortController()
    if (!twoFA) setTwoFALoading(true)
    getTwoFAAdoptionStats(tenantId, ctrl.signal)
      .then((s) => setTwoFA(s))
      .catch(() => {
        /* 统计失败不提示 */
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setTwoFALoading(false)
      })
    return () => ctrl.abort()
  }, [refreshNonce, tenantId])

  const refresh = () => setRefreshNonce((n) => n + 1)

  const act = async (id: number, fn: () => Promise<unknown>) => {
    setBusyId(id)
    setError(null)
    try {
      await fn()
      refresh()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '操作失败')
    } finally {
      setBusyId(null)
    }
  }

  const rows = mapUserRows(users)
  const statCards = mapUserStats(twoFA)
  const pagination = mapUserPagination({
    offset,
    limit,
    returnedCount: users.length,
    totalUsers: twoFA?.total_users ?? null,
    searching: q !== '',
  })
  const totalCopy = twoFA
    ? `全租户共 ${twoFA.total_users.toLocaleString('zh-CN')} 人。`
    : twoFALoading
      ? '全租户总数加载中。'
      : '全租户总数暂不可用。'
  const columns: DataListColumn<UserTableRow>[] = [
    {
      key: 'email',
      label: '邮箱',
      render: (row) => <Link to={`/users/${row.id}`} style={emailLinkStyle}>{row.email}</Link>,
    },
    { key: 'role', label: '角色', render: (row) => row.role },
    {
      key: 'status',
      label: '状态',
      badge: true,
      render: (row) => <StatusBadge tone={row.statusTone}>{row.statusText}</StatusBadge>,
    },
    { key: 'group', label: '用户组', render: (row) => row.userGroup },
    {
      key: 'remark',
      label: '备注',
      render: (row) => <span title={row.remark} style={ellipsisStyle}>{row.remark}</span>,
    },
    {
      key: 'balance',
      label: '余额',
      render: (row) => <span className="hk-mono" style={numericStyle}>{row.balance}</span>,
    },
    { key: 'created', label: '创建时间', render: (row) => <span className="hk-mono">{row.createdAt}</span> },
  ]

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>用户管理</h1>
          <p className="hk-sub">管线第 5 站 · 租户内用户。{totalCopy}</p>
        </div>
        <button type="button" onClick={() => setCreateOpen(true)} className="hk-btn hk-btn--green">
          ＋ 新建用户
        </button>
      </header>

      {createOpen && tenantId != null && <CreateUserModal tenantId={tenantId} onClose={() => setCreateOpen(false)} onCreated={refresh} />}

      <section aria-label="用户统计" style={statsGridStyle}>
        {statCards.map((card) => (
          <StatCard
            key={card.label}
            label={card.label}
            value={card.value}
            hint={twoFALoading && !twoFA ? '全租户统计加载中' : card.hint}
            tone={card.label === '2FA 普及率' && twoFA && twoFA.enabled_rate >= 0.8 ? 'ok' : 'default'}
          />
        ))}
      </section>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          const nextQ = draftQ.trim()
          setOffset(0)
          if (nextQ === q && offset === 0) refresh()
          else setQ(nextQ)
        }}
        style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)' }}
      >
        <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)', flex: 1 }}>
          搜索(邮箱/名称)
          <input value={draftQ} onChange={(e) => setDraftQ(e.target.value)} placeholder="按邮箱或显示名搜索" style={inp} />
        </label>
        <button type="submit" style={primaryBtn}>
          查询
        </button>
        <button type="button" onClick={() => { setDraftQ(''); setOffset(0); if (q === '' && offset === 0) refresh(); else setQ('') }} style={ghostBtn}>
          重置
        </button>
      </form>

      {error && <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{error}</div>}

      <div className="hk-card">
        {loading ? (
          <div style={skeletonWrapStyle}><SkeletonRows rows={6} cols={8} /></div>
        ) : rows.length === 0 ? (
          <EmptyState
            title={offset > 0 ? '当前页没有更多用户' : q ? '没有匹配的用户' : '暂无用户'}
            hint={offset > 0 ? '返回上一页继续查看。' : q ? '请调整邮箱或名称关键词后重试。' : '新建用户后会在这里显示。'}
            action={offset > 0
              ? { label: '返回上一页', onClick: () => setOffset(Math.max(0, offset - limit)) }
              : q
                ? { label: '清除搜索', onClick: () => { setDraftQ(''); setQ(''); setOffset(0) } }
                : { label: '新建用户', onClick: () => setCreateOpen(true) }}
          />
        ) : (
          <DataListTable
            label="用户列表"
            rows={rows}
            rowKey={(row) => row.id}
            columns={columns}
            actions={[
              { label: '余额/调额', to: (row) => `/users/${row.id}` },
              {
                label: (row) => row.status === 'active' ? '停用' : '启用',
                onClick: (row) => { if (tenantId != null) void act(row.id, () => setUserStatus(tenantId, row.id, toggleStatusTarget(row.status))) },
                disabled: (row) => busyId === row.id,
              },
              {
                label: '解锁',
                onClick: (row) => { if (tenantId != null) void act(row.id, () => unlockUser(tenantId, row.id)) },
                disabled: (row) => row.status !== 'locked' || busyId === row.id,
              },
            ]}
          />
        )}
      </div>

      <nav aria-label="用户列表分页" style={paginationStyle}>
        <span>{pagination.scopeText} · 第 {pagination.page} 页</span>
        <div style={paginationActionsStyle}>
          <label style={pageSizeLabelStyle}>
            每页
            <select
              aria-label="每页用户数"
              value={limit}
              disabled={loading}
              onChange={(event) => { setLimit(Number(event.target.value)); setOffset(0) }}
              className="hk-input"
              style={pageSizeSelectStyle}
            >
              {USERS_PAGE_LIMIT_OPTIONS.map((size) => <option key={size} value={size}>{size} 人</option>)}
            </select>
          </label>
          <button type="button" className="hk-btn hk-btn--sm" disabled={loading || !pagination.canPrevious} onClick={() => setOffset(Math.max(0, offset - limit))}>上一页</button>
          <button type="button" className="hk-btn hk-btn--sm" disabled={loading || !pagination.canNext} onClick={() => setOffset(offset + limit)}>下一页</button>
        </div>
      </nav>
    </div>
  )
}

function CreateUserModal({ tenantId, onClose, onCreated }: { tenantId: number; onClose: () => void; onCreated: () => void }) {
  const [form, setForm] = useState<CreateUserForm>(EMPTY_CREATE_USER)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const set = <K extends keyof CreateUserForm>(k: K, v: CreateUserForm[K]) => setForm((f) => ({ ...f, [k]: v }))

  const submit = async () => {
    const built = buildCreateUser(form)
    if ('error' in built) {
      setError(built.error)
      return
    }
    setBusy(true)
    setError(null)
    try {
      await createUser(tenantId, built)
      onCreated()
      onClose()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '创建失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div onClick={onClose} style={{ position: 'fixed', inset: 0, background: 'rgba(28,38,34,0.4)', display: 'flex', alignItems: 'flex-start', justifyContent: 'center', padding: 'var(--hk-space-6)', zIndex: 'var(--hk-z-overlay)' as unknown as number, overflowY: 'auto' }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 'min(440px,100%)', background: 'var(--hk-surface)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-3)', padding: 'var(--hk-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
        <h2 style={{ fontSize: 18 }}>新建用户</h2>
        <Field label="邮箱">
          <input type="email" value={form.email} onChange={(e) => set('email', e.target.value)} style={inp} />
        </Field>
        <Field label="初始密码(≥8 位)">
          <input type="password" value={form.password} onChange={(e) => set('password', e.target.value)} style={inp} />
        </Field>
        <Field label="显示名(可选)">
          <input value={form.displayName} onChange={(e) => set('displayName', e.target.value)} style={inp} />
        </Field>
        <Field label="角色">
          <select value={form.role} onChange={(e) => set('role', e.target.value)} style={inp}>
            {CREATE_USER_ROLES.map((r) => (
              <option key={r.value} value={r.value}>
                {r.label}
              </option>
            ))}
          </select>
        </Field>
        {error && <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{error}</div>}
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', justifyContent: 'flex-end' }}>
          <button type="button" onClick={onClose} style={ghostBtn}>
            取消
          </button>
          <button type="button" disabled={busy} onClick={submit} style={primaryBtn}>
            {busy ? '创建中…' : '创建'}
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
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const statsGridStyle: CSSProperties = { display: 'grid', gridTemplateColumns: 'repeat(2, minmax(0, 1fr))', gap: 'var(--hk-space-3)' }
const emailLinkStyle: CSSProperties = { fontWeight: 600, color: 'var(--hk-primary-700)', textDecoration: 'none' }
const ellipsisStyle: CSSProperties = { display: 'block', maxWidth: 220, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }
const numericStyle: CSSProperties = { color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
const skeletonWrapStyle: CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)' }
const paginationStyle: CSSProperties = { display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 'var(--hk-space-3)', fontSize: 13, color: 'var(--hk-ink-500)' }
const paginationActionsStyle: CSSProperties = { display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }
const pageSizeLabelStyle: CSSProperties = { display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }
const pageSizeSelectStyle: CSSProperties = { width: 92, minHeight: 30, padding: '0 var(--hk-space-2)' }
