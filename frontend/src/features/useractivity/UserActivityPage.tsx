import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge, type BadgeTone } from '../../ui/StatusBadge'
import { listMyAuditEvents } from './api'
import { actionLabel, hasMore, nextOffset, outcomeLabel, outcomeTone, PAGE_LIMIT } from './useractivity'
import type { UserAuditEvent } from './types'

/*
 * 用户安全日志(用户门户 · 账户)。只读列出当前用户自己的审计事件(签发/撤销 Key、登录、2FA、
 * 通行密钥等),含动作/结果/Key 前缀/原因/请求 ID/时间。session 鉴权,身份后端从会话派生。
 * 翻页用 offset/limit「加载更多」(后端无总数,据本页返回数推断是否还有下一页)。
 * 真码端点:backend/internal/userauditloghttp/handlers.go:27、backend/cmd/gateway/routes.go:192。
 */
export function UserActivityPage() {
  const [items, setItems] = useState<UserAuditEvent[]>([])
  const [offset, setOffset] = useState(0)
  const [more, setMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)

  // 首屏 / 刷新:重置到第一页。翻页(加载更多)单独走 loadMore,不经此 effect。
  const loadFirst = useCallback(
    (signal: AbortSignal) => {
      setLoading(true)
      setError(null)
      listMyAuditEvents(PAGE_LIMIT, 0, signal)
        .then((resp) => {
          setItems(resp.audit_events)
          setOffset(0)
          setMore(hasMore(resp.count, PAGE_LIMIT))
        })
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载安全日志失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [refreshNonce],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    loadFirst(ctrl.signal)
    return () => ctrl.abort()
  }, [loadFirst])

  const loadMore = async () => {
    const next = nextOffset(offset, PAGE_LIMIT)
    setLoadingMore(true)
    setError(null)
    try {
      const resp = await listMyAuditEvents(PAGE_LIMIT, next)
      setItems((prev) => [...prev, ...resp.audit_events])
      setOffset(next)
      setMore(hasMore(resp.count, PAGE_LIMIT))
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载更多失败')
    } finally {
      setLoadingMore(false)
    }
  }

  const refresh = () => setRefreshNonce((n) => n + 1)

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>安全日志</h1>
          <p className="hk-sub">
            你账户的敏感操作记录(签发/撤销 Key、登录、两步验证、通行密钥等)。已加载 {items.length} 条。
          </p>
        </div>
        <button type="button" onClick={refresh} className="hk-btn" disabled={loading}>
          刷新
        </button>
      </header>

      {error && <div style={errBox}>{error}</div>}

      <div className="hk-card">
        {loading && items.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : items.length === 0 ? (
          <Empty>暂无安全日志记录。</Empty>
        ) : (
          <div className="hk-tablewrap">
            <table className="hk-table">
              <thead>
                <tr>
                  {['时间', '动作', '结果', 'API Key', '原因', '请求 ID'].map((h) => (
                    <th key={h}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {items.map((ev) => (
                  <tr key={ev.id}>
                    <td className="hk-mono">{fmt(ev.occurred_at)}</td>
                    <td>
                      <span style={{ fontWeight: 600, color: 'var(--hk-ink-900)' }}>{actionLabel(ev.action)}</span>
                    </td>
                    <td>
                      <StatusBadge tone={outcomeTone(ev.outcome) as BadgeTone}>{outcomeLabel(ev.outcome)}</StatusBadge>
                    </td>
                    <td>
                      {ev.key_prefix ? (
                        <code className="hk-mono" style={{ color: 'var(--hk-ink-700)' }}>{ev.key_prefix}…</code>
                      ) : (
                        <span style={{ color: 'var(--hk-ink-300)' }}>—</span>
                      )}
                    </td>
                    <td style={{ color: 'var(--hk-ink-700)', maxWidth: 280, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {ev.reason || '—'}
                    </td>
                    <td>
                      {ev.request_id ? (
                        <code className="hk-mono" style={{ color: 'var(--hk-ink-300)' }}>{ev.request_id}</code>
                      ) : (
                        <span style={{ color: 'var(--hk-ink-300)' }}>—</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {more && (
        <div style={{ display: 'flex', justifyContent: 'center' }}>
          {/* 刷新进行中也禁用,避免与首屏重拉并发追加导致列表瞬时错位 */}
          <button type="button" onClick={loadMore} disabled={loadingMore || loading} className="hk-btn">
            {loadingMore ? '加载中…' : '加载更多'}
          </button>
        </div>
      )}
    </div>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div className="hk-empty">{children}</div>
}

/** RFC3339(Nano)→ 本地可读串(24 小时制)。非法/空原样或占位。 */
function fmt(iso: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN', { hour12: false })
}

const errBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
