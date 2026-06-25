import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge, type BadgeTone } from '../../ui/StatusBadge'
import { listUsageRecords } from './api'
import { formatCost, hasMore, modelDisplay, statusLabel, statusTone, tokensSummary } from './usagerecords'
import type { UsageRecord } from './types'

const PAGE_LIMIT = 50

/*
 * 用量明细 / 请求日志(用户门户 · 用量与配额)。只读列出当前用户跨全部 API Key 的逐请求用量
 * (模型/状态/费用/token/时间/请求 ID),游标分页「加载更多」。session 鉴权,身份后端从会话派生。
 * 区别于 /usage(聚合配额视图):这里是行级逐请求日志。
 * 真码端点:backend/internal/meusagehttp/session_handler.go:19、backend/cmd/gateway/routes.go:192。
 */
export function UsageRecordsPage() {
  const [items, setItems] = useState<UsageRecord[]>([])
  const [cursor, setCursor] = useState('')
  const [more, setMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)

  const loadFirst = useCallback(
    (signal: AbortSignal) => {
      setLoading(true)
      setError(null)
      listUsageRecords({ limit: PAGE_LIMIT }, signal)
        .then((resp) => {
          setItems(resp.items)
          setCursor(resp.next_cursor)
          setMore(hasMore(resp.next_cursor))
        })
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载用量明细失败')
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
    if (!cursor) return
    setLoadingMore(true)
    setError(null)
    try {
      const resp = await listUsageRecords({ limit: PAGE_LIMIT, cursor })
      setItems((prev) => [...prev, ...resp.items])
      setCursor(resp.next_cursor)
      setMore(hasMore(resp.next_cursor))
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载更多失败')
    } finally {
      setLoadingMore(false)
    }
  }

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--hk-space-3)' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
          <h1 style={{ fontSize: 22 }}>用量明细</h1>
          <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
            你账户跨全部 API Key 的逐请求记录(模型 / 状态 / 费用 / token)。已加载 {items.length} 条。
          </p>
        </div>
        <button type="button" onClick={() => setRefreshNonce((n) => n + 1)} style={ghostBtn} disabled={loading}>
          刷新
        </button>
      </header>

      {error && <div style={errBox}>{error}</div>}

      <div style={card}>
        {loading && items.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : items.length === 0 ? (
          <Empty>暂无请求记录。发起 API 调用后这里会显示逐请求用量。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['时间', '模型', '状态', '费用', 'Token', '请求 ID'].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {items.map((r, i) => (
                  <tr key={r.request_id || r.ledger_id || `${r.created_at}-${i}`} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={tdTime}>{fmt(r.created_at)}</td>
                    <td style={td}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                        <span style={{ color: 'var(--hk-ink-900)' }}>{modelDisplay(r)}</span>
                        {r.stream && <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>流式</span>}
                      </div>
                    </td>
                    <td style={td}>
                      <StatusBadge tone={statusTone(r.status) as BadgeTone}>{statusLabel(r.status)}</StatusBadge>
                    </td>
                    <td style={{ ...td, whiteSpace: 'nowrap' }}>
                      <code style={{ fontSize: 12, color: 'var(--hk-ink-700)' }}>{formatCost(r.actual_cost)}</code>
                    </td>
                    <td style={{ ...td, color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }}>{tokensSummary(r.tokens)}</td>
                    <td style={td}>
                      {r.request_id ? (
                        <code style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{r.request_id}</code>
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
          <button type="button" onClick={loadMore} disabled={loadingMore || loading} style={ghostBtn}>
            {loadingMore ? '加载中…' : '加载更多'}
          </button>
        </div>
      )}
    </div>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

/** RFC3339(Nano)→ 本地可读串(24 小时制)。非法/空原样或占位。 */
function fmt(iso: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN', { hour12: false })
}

const card: React.CSSProperties = { background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
const tdTime: React.CSSProperties = { ...td, color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const errBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }
