import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { getUnreadCount, listNotifications, markNotificationRead } from './api'
import { countUnread, isUnread, severityLabel, severityTone } from './notifications'
import type { UserNotification } from './types'

/*
 * 站内信收件箱(用户门户)。列表(全部/未读切换)+ 未读角标 + 标记已读。纯读 + 标记,
 * session 鉴权,不触钱不翻默认行为。后端 GET /v1/notifications(+unread-count、{id}/read)。
 */
export function NotificationsPage() {
  const [items, setItems] = useState<UserNotification[]>([])
  const [unread, setUnread] = useState<number | null>(null)
  const [onlyUnread, setOnlyUnread] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback((unreadOnly: boolean, signal?: AbortSignal) => {
    setLoading(true)
    setError(null)
    listNotifications({ limit: 50, unreadOnly }, signal)
      .then((r) => {
        if (!signal?.aborted) setItems(r.items)
      })
      .catch((e: unknown) => {
        if (signal?.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载通知失败')
      })
      .finally(() => {
        if (!signal?.aborted) setLoading(false)
      })
    // 未读数独立拉(失败不连累列表)。
    getUnreadCount(signal)
      .then((r) => {
        if (!signal?.aborted) setUnread(r.count)
      })
      .catch(() => {
        /* 未读数失败:用列表本地兜底 */
      })
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    load(onlyUnread, ctrl.signal)
    return () => ctrl.abort()
  }, [onlyUnread, load])

  const onMarkRead = async (id: number) => {
    // 乐观更新:先本地标已读,失败再回滚并提示。
    const prev = items
    const now = new Date().toISOString()
    setItems((list) => list.map((n) => (n.id === id ? { ...n, read_at: now } : n)))
    setUnread((u) => (u != null && u > 0 ? u - 1 : u))
    try {
      await markNotificationRead(id)
    } catch (e) {
      setItems(prev) // 回滚
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '标记已读失败')
    }
  }

  const localUnread = countUnread(items)
  const unreadBadge = unread ?? localUnread

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1 style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
            站内信
            {unreadBadge > 0 && (
              <span style={badge}>{unreadBadge > 99 ? '99+' : unreadBadge} 未读</span>
            )}
          </h1>
          <p className="hk-sub">平台公告与系统通知。</p>
        </div>
        <div className="hk-seg">
          <Seg active={!onlyUnread} onClick={() => setOnlyUnread(false)}>全部</Seg>
          <Seg active={onlyUnread} onClick={() => setOnlyUnread(true)}>未读</Seg>
        </div>
      </header>

      {error && (
        <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>
          {error}
        </div>
      )}

      {loading && items.length === 0 ? (
        <Empty>加载中…</Empty>
      ) : items.length === 0 ? (
        <Empty>{onlyUnread ? '没有未读通知。' : '还没有通知。'}</Empty>
      ) : (
        <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
          {items.map((n) => {
            const unreadItem = isUnread(n)
            return (
              <li
                key={n.id}
                style={{
                  display: 'flex',
                  gap: 'var(--hk-space-3)',
                  padding: 'var(--hk-space-4)',
                  background: 'var(--hk-surface)',
                  border: '1px solid var(--hk-line)',
                  borderLeft: `3px solid ${unreadItem ? 'var(--hk-primary-500)' : 'transparent'}`,
                  borderRadius: 'var(--hk-radius-lg)',
                  boxShadow: 'var(--hk-shadow-1)',
                }}
              >
                <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', flexWrap: 'wrap' }}>
                    <StatusBadge tone={severityTone(n.severity)}>{severityLabel(n.severity)}</StatusBadge>
                    <span style={{ fontSize: 14, fontWeight: unreadItem ? 700 : 500, color: 'var(--hk-ink-900)' }}>{n.title}</span>
                  </div>
                  <p style={{ margin: 0, fontSize: 13, color: 'var(--hk-ink-700)', lineHeight: 1.6, whiteSpace: 'pre-wrap' }}>{n.body}</p>
                  <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>{new Date(n.created_at).toLocaleString()}</span>
                </div>
                {unreadItem && (
                  <button type="button" onClick={() => onMarkRead(n.id)} className="hk-btn hk-btn--sm" style={{ alignSelf: 'flex-start' }}>
                    标记已读
                  </button>
                )}
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}

// 分段切换按钮:包裹在 .hk-seg 里,选中态用 .is-on。
function Seg({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button type="button" onClick={onClick} className={active ? 'is-on' : undefined}>
      {children}
    </button>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div className="hk-empty">{children}</div>
}

const badge: React.CSSProperties = {
  fontSize: 11,
  fontWeight: 600,
  color: '#fff',
  background: 'var(--hk-primary-500)',
  borderRadius: 999,
  padding: '2px 8px',
}
