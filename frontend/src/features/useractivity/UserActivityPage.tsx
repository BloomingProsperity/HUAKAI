import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { listMyAuditEvents } from './api'
import { hasMore, mapActivityRows, nextOffset, PAGE_LIMIT, type ActivityTableRow } from './useractivity'
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
  const rows = mapActivityRows(items)

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
          <EmptyState title="正在加载安全日志" hint="请稍候。" />
        ) : items.length === 0 ? (
          <EmptyState title="暂无安全日志记录" hint="账户发生敏感操作后会显示在这里。" />
        ) : (
          <DataListTable label="安全日志" rows={rows} rowKey={(row) => row.id} columns={activityColumns} />
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

const errBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
const activityColumns: DataListColumn<ActivityTableRow>[] = [
  { key: 'time', label: '时间', render: (row) => <span className="hk-mono">{row.occurredAt}</span> },
  { key: 'action', label: '动作', render: (row) => <span style={{ fontWeight: 600, color: 'var(--hk-ink-900)' }}>{row.action}</span> },
  { key: 'outcome', label: '结果', badge: true, render: (row) => <StatusBadge tone={row.outcomeTone}>{row.outcome}</StatusBadge> },
  { key: 'api-key', label: 'API Key', render: (row) => <code className="hk-mono" style={{ color: 'var(--hk-ink-700)' }}>{row.keyPrefix}</code> },
  { key: 'reason', label: '原因', render: (row) => <span style={{ color: 'var(--hk-ink-700)' }}>{row.reason}</span> },
  { key: 'request-id', label: '请求 ID', render: (row) => <code className="hk-mono" style={{ color: 'var(--hk-ink-300)' }}>{row.requestID}</code> },
]
