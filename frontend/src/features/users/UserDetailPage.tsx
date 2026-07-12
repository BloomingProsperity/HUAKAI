import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { getBalanceHistory, getUser, getUserUsage } from './api'
import { balanceDirection, eventTypeLabel, signedAmount, type BalanceHistoryEntry, type UserDetail, type UserUsageResponse } from './detail'
import { roleLabel, statusLabel } from './users'
import { UserAdminActions } from './UserAdminActions'
import { UserBalanceAdjust } from './UserBalanceAdjust'
import { UserNotifyPrefs } from './UserNotifyPrefs'
import { UserUsageCard } from './UserUsageCard'

/*
 * 用户详情(运维台,P1)。GET /admin/v1/users/{id} 用户信息 + GET /{id}/balance-history 余额台账。
 * 余额台账纯只读展示(贷记/借记按金额符号配色),不做任何金额变更。
 */
export function UserDetailPage() {
  const params = useParams()
  const id = Number(params.id)
  const [user, setUser] = useState<UserDetail | null>(null)
  const [history, setHistory] = useState<BalanceHistoryEntry[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [historyError, setHistoryError] = useState<string | null>(null)
  const [usage, setUsage] = useState<UserUsageResponse | null>(null)
  const [usageLoading, setUsageLoading] = useState(true)
  const [usageError, setUsageError] = useState<string | null>(null)
  // 运维动作(改组/备注/2FA/passkey/解绑/软删)成功后自增,触发重新拉详情。
  const [nonce, setNonce] = useState(0)

  useEffect(() => {
    if (!Number.isInteger(id) || id <= 0) {
      setError('非法用户 ID')
      setLoading(false)
      setUsageLoading(false)
      return
    }
    const ctrl = new AbortController()
    setLoading(true)
    setError(null)
    setUsage(null)
    setUsageLoading(true)
    setUsageError(null)
    getUser(id, ctrl.signal)
      .then((u) => setUser(u))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载用户详情失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    // 余额历史独立加载:失败只提示该卡,不连累用户信息。
    getBalanceHistory(id, 0, 100, ctrl.signal)
      .then((resp) => setHistory(resp.items))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setHistoryError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载余额历史失败')
      })
    // 用量独立加载，失败只影响本卡；limit=200 与后端单页上限一致。
    getUserUsage(id, 200, ctrl.signal)
      .then((response) => setUsage(response))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setUsageError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载用户用量失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setUsageLoading(false)
      })
    return () => ctrl.abort()
  }, [id, nonce])

  if (loading) return <Center>加载中…</Center>
  if (error && !user) return <Center tone="danger">{error}</Center>
  if (!user) return <Center tone="danger">用户不存在</Center>

  return (
    <div className="hk-page" style={{ maxWidth: 1000 }}>
      <Link to="/users" style={{ fontSize: 13 }}>
        ← 返回用户列表
      </Link>
      <header className="hk-pagehead">
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
          <h1>{user.email}</h1>
          <StatusBadge tone={user.status === 'active' ? 'ok' : user.status === 'locked' ? 'danger' : 'muted'}>
            {statusLabel(user.status)}
          </StatusBadge>
          <span className="hk-mono" style={{ fontSize: 12, color: 'var(--hk-ink-300)' }}>#{user.id}</span>
        </div>
      </header>

      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 'var(--hk-space-3)' }}>
        <Stat label="余额" value={user.balance} mono />
        <Stat label="角色" value={roleLabel(user.role)} />
        <Stat label="用户组" value={user.user_group || '—'} />
        <Stat label="创建时间" value={fmt(user.created_at)} />
      </div>

      <UserUsageCard response={usage} loading={usageLoading} error={usageError} />

      {/* 手动调额(money 敏感):成功后复用同一 nonce 刷新机制,重新拉余额 + 台账。 */}
      <UserBalanceAdjust user={user} onChanged={() => setNonce((n) => n + 1)} />

      <UserAdminActions user={user} onChanged={() => setNonce((n) => n + 1)} />

      {/* 通知偏好(代管):GET 回填 + PUT 保存,独立加载自身 tenant/user,不连累上方卡片。 */}
      <UserNotifyPrefs userId={user.id} />

      <section className="hk-card">
        <div className="hk-card__head">
          <h3>余额历史(台账)</h3>
        </div>
        {historyError ? (
          <div style={{ margin: 'var(--hk-space-3) var(--hk-space-4)', padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{historyError}</div>
        ) : history.length === 0 ? (
          <div className="hk-empty">暂无余额变动记录。</div>
        ) : (
          <div className="hk-tablewrap">
            <table className="hk-table">
              <thead>
                <tr>
                  {['时间', '事件', '金额', '来源'].map((h) => (
                    <th key={h}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {history.map((e) => {
                  const dir = balanceDirection(e.amount)
                  return (
                    <tr key={e.id}>
                      <td className="hk-mono">{fmt(e.occurred_at)}</td>
                      <td>{eventTypeLabel(e.event_type)}</td>
                      <td className="hk-mono" style={{ color: dir === 'credit' ? 'var(--hk-primary-700)' : dir === 'debit' ? 'var(--hk-danger)' : 'var(--hk-ink-500)', fontWeight: 600 }}>
                        {signedAmount(e.amount)}
                      </td>
                      <td>
                        {e.source_type}
                        {e.source_id ? ` #${e.source_id}` : ''}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  )
}

function Stat({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="hk-statcard">
      <span className="hk-statcard__t">{label}</span>
      <div className="hk-statcard__v" style={{ fontFamily: mono ? 'var(--hk-font-mono)' : undefined, color: 'var(--hk-ink-900)' }}>{value}</div>
    </div>
  )
}
function Center({ children, tone }: { children: React.ReactNode; tone?: 'danger' }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: tone === 'danger' ? 'var(--hk-danger)' : 'var(--hk-ink-500)', fontSize: 14 }}>{children}</div>
}
function fmt(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? (iso || '—') : d.toLocaleString('zh-CN', { hour12: false })
}
