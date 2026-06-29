import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { ApiError } from '../../lib/api'
import { StatusBadge, type BadgeTone } from '../../ui/StatusBadge'
import { createUser, getTwoFAAdoptionStats, listUsers, setUserStatus, unlockUser } from './api'
import { formatAdoptionRate, type TwoFAAdoptionStats } from './actions'
import {
  buildCreateUser,
  CREATE_USER_ROLES,
  EMPTY_CREATE_USER,
  roleLabel,
  statusLabel,
  toggleStatusTarget,
  type CreateUserForm,
} from './users'
import type { AdminUser } from './types'

/*
 * 用户管理(运维台,P0)。管线第 5 站。/admin/v1/users 列表 + 搜索 + 创建 + 启停 + 解锁。
 * 余额列只读展示(money 只读,不在此动钱)。
 */
export function UsersPage() {
  const [users, setUsers] = useState<AdminUser[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [draftQ, setDraftQ] = useState('')
  const [q, setQ] = useState('')
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [createOpen, setCreateOpen] = useState(false)
  const [busyId, setBusyId] = useState<number | null>(null)
  const [twoFA, setTwoFA] = useState<TwoFAAdoptionStats | null>(null)

  const load = useCallback(
    (signal: AbortSignal) => {
      setLoading(true)
      setError(null)
      listUsers(q, 0, 100, signal)
        .then((resp) => setUsers(resp.items))
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载用户列表失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [q],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, refreshNonce])

  // 2FA 普及率统计独立加载,失败静默(只是少一张统计卡,不连累列表)。
  useEffect(() => {
    const ctrl = new AbortController()
    getTwoFAAdoptionStats(ctrl.signal)
      .then((s) => setTwoFA(s))
      .catch(() => {
        /* 统计失败不提示 */
      })
    return () => ctrl.abort()
  }, [refreshNonce])

  const refresh = () => setRefreshNonce((n) => n + 1)

  const act = async (u: AdminUser, fn: () => Promise<unknown>) => {
    setBusyId(u.id)
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

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--hk-space-3)' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
          <h1 style={{ fontSize: 22 }}>用户管理</h1>
          <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
            管线第 5 站 · 租户内用户。共 {users.length} 人。
          </p>
        </div>
        <button type="button" onClick={() => setCreateOpen(true)} style={newBtn}>
          ＋ 新建用户
        </button>
      </header>

      {createOpen && <CreateUserModal onClose={() => setCreateOpen(false)} onCreated={refresh} />}

      {twoFA && (
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'baseline', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 13 }}>
          <span style={{ color: 'var(--hk-ink-500)' }}>两步验证(2FA)普及率</span>
          <strong style={{ fontSize: 18, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-900)' }}>{formatAdoptionRate(twoFA)}</strong>
          <span style={{ color: 'var(--hk-ink-500)' }}>
            {twoFA.enabled_users} / {twoFA.total_users} 名用户已开启
          </span>
        </div>
      )}

      <form
        onSubmit={(e) => {
          e.preventDefault()
          setQ(draftQ)
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
        <button type="button" onClick={() => { setDraftQ(''); setQ('') }} style={ghostBtn}>
          重置
        </button>
      </form>

      {error && <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }}>{error}</div>}

      <div style={{ background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }}>
        {loading && users.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : users.length === 0 ? (
          <Empty>没有用户。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['邮箱', '角色', '状态', '余额', '创建时间', ''].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {users.map((u) => (
                  <tr key={u.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={td}>
                      <div style={{ display: 'flex', flexDirection: 'column' }}>
                        <Link to={`/users/${u.id}`} style={{ fontWeight: 600, color: 'var(--hk-primary-700)', textDecoration: 'none' }}>
                          {u.email}
                        </Link>
                      </div>
                    </td>
                    <td style={td}>{roleLabel(u.role)}</td>
                    <td style={td}>
                      <StatusBadge tone={statusTone(u.status)}>{statusLabel(u.status)}</StatusBadge>
                    </td>
                    <td style={tdNum}>{u.balance}</td>
                    <td style={td}>{fmt(u.created_at)}</td>
                    <td style={{ ...td, textAlign: 'right', whiteSpace: 'nowrap' }}>
                      <button type="button" disabled={busyId === u.id} onClick={() => act(u, () => setUserStatus(u.id, toggleStatusTarget(u.status)))} style={linkBtn}>
                        {u.status === 'active' ? '停用' : '启用'}
                      </button>
                      {u.status === 'locked' && (
                        <button type="button" disabled={busyId === u.id} onClick={() => act(u, () => unlockUser(u.id))} style={linkBtn}>
                          解锁
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}

function CreateUserModal({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
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
      await createUser(built)
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
        {error && <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }}>{error}</div>}
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

function statusTone(status: string): BadgeTone {
  switch (status) {
    case 'active':
      return 'ok'
    case 'disabled':
      return 'muted'
    case 'locked':
      return 'danger'
    default:
      return 'muted'
  }
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
