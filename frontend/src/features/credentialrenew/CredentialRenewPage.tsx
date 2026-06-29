import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { listRenewStatus } from './api'
import {
  DEFAULT_RENEW_LIMIT,
  failureSummary,
  relativeTime,
  renewHealth,
  renewHealthLabel,
  renewHealthTone,
} from './renew'
import type { RenewStatusRow } from './types'

/*
 * 凭证续期监控(只读)。管线第 1 站(上游账号池)下的凭证临期/续期健康面板。
 * 后端:GET /admin/v1/credentials/renew-status(游标分页),挂 /admin/v1/credentials(admin token)。
 *   - 展示各账号凭证的:续期健康度、状态、版本、距 access token 到期、续期窗口、最近刷新、失败摘要。
 *   - 健康度由前端 renewHealth 综合判定(失败/停用/已过期/待续期/即将到期/健康),临期高亮。
 *   - 游标翻页:next_cursor 非 null 时显示「加载更多」,追加下一页。
 * 纯只读,不做任何改动型操作;不碰 pool/registry/gateway 等碰撞包。
 * 可选 tenant_id 过滤(platform_admin 无 scope 时把视图收敛到单租户)。
 */

const PAGE_SIZE = DEFAULT_RENEW_LIMIT

export function CredentialRenewPage() {
  const [rows, setRows] = useState<RenewStatusRow[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // 租户过滤:草稿(输入框)与已生效值分离,点「应用」才触发重载。
  const [tenantDraft, setTenantDraft] = useState('')
  const [tenantFilter, setTenantFilter] = useState<number | undefined>(undefined)

  // 当前时刻:进页/每次重载时刷新一次,作为所有「距到期/相对时间」判定的基准。
  const [nowMs, setNowMs] = useState(() => Date.now())

  // 防止「加载更多」的旧响应在重载后污染列表:每次重载自增,过期响应被丢弃。
  const loadSeq = useRef(0)

  const loadFirst = useCallback(
    (signal?: AbortSignal) => {
      const seq = ++loadSeq.current
      setLoading(true)
      setError(null)
      const now = Date.now()
      setNowMs(now)
      listRenewStatus({ tenantId: tenantFilter, limit: PAGE_SIZE }, signal)
        .then((resp) => {
          if (signal?.aborted || seq !== loadSeq.current) return
          setRows(resp.items ?? [])
          setCursor(resp.next_cursor)
        })
        .catch((e: unknown) => {
          if (signal?.aborted || seq !== loadSeq.current) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载凭证续期状态失败')
        })
        .finally(() => {
          if (!signal?.aborted && seq === loadSeq.current) setLoading(false)
        })
    },
    [tenantFilter],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    loadFirst(ctrl.signal)
    return () => ctrl.abort()
  }, [loadFirst])

  const loadMore = () => {
    if (cursor === null) return
    const seq = loadSeq.current
    setLoadingMore(true)
    setError(null)
    listRenewStatus({ tenantId: tenantFilter, limit: PAGE_SIZE, cursor })
      .then((resp) => {
        // 翻页期间若发生重载(seq 变了),丢弃这批,避免追加进已被替换的列表。
        if (seq !== loadSeq.current) return
        setRows((prev) => [...prev, ...(resp.items ?? [])])
        setCursor(resp.next_cursor)
      })
      .catch((e: unknown) => {
        if (seq !== loadSeq.current) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载更多失败')
      })
      .finally(() => {
        if (seq === loadSeq.current) setLoadingMore(false)
      })
  }

  const applyTenantFilter = () => {
    const raw = tenantDraft.trim()
    if (raw === '') {
      setTenantFilter(undefined)
      return
    }
    const v = Number(raw)
    setTenantFilter(Number.isInteger(v) && v > 0 ? v : undefined)
  }

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>凭证续期监控</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          只读:各上游账号凭证的临期与续期健康态。失败/已过期/待续期会高亮,便于运营提前介入。
        </p>
      </header>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          applyTenantFilter()
        }}
        style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)' }}
      >
        <Field label="按租户过滤(tenant_id,可选)">
          <input
            value={tenantDraft}
            onChange={(e) => setTenantDraft(e.target.value)}
            inputMode="numeric"
            placeholder="留空=按身份范围"
            style={{ ...inp, width: 180 }}
          />
        </Field>
        <button type="submit" style={primaryBtn}>
          应用
        </button>
        <button
          type="button"
          onClick={() => {
            setTenantDraft('')
            setTenantFilter(undefined)
          }}
          style={ghostBtn}
        >
          重置
        </button>
      </form>

      <section style={card}>
        <div style={cardHead}>
          <h2 style={{ fontSize: 15, margin: 0 }}>续期状态</h2>
          <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>
            已载 {rows.length} 条{cursor !== null ? '(还有更多)' : ''}
          </span>
        </div>

        {error && <Banner kind="error">{error}</Banner>}

        {loading && rows.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : rows.length === 0 ? (
          <Empty>暂无凭证续期记录。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['续期状态', '租户', '账号', '厂商 / 模式', '版本', '距到期', '续期窗口', '最近刷新', '失败'].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => {
                  const h = renewHealth(row, nowMs)
                  return (
                    <tr key={row.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                      <td style={td}>
                        <StatusBadge tone={renewHealthTone(h)}>{renewHealthLabel(h)}</StatusBadge>
                      </td>
                      <td style={td}>
                        <div style={{ color: 'var(--hk-ink-900)' }}>{row.tenant_name || '—'}</div>
                        <div style={{ fontSize: 11, color: 'var(--hk-ink-300)', fontFamily: 'var(--hk-font-mono)' }}>#{row.tenant_id}</div>
                      </td>
                      <td style={td}>
                        <div style={{ color: 'var(--hk-ink-900)' }}>{row.account_name || '—'}</div>
                        <div style={{ fontSize: 11, color: 'var(--hk-ink-300)', fontFamily: 'var(--hk-font-mono)' }}>#{row.account_id}</div>
                      </td>
                      <td style={td}>
                        <div style={{ color: 'var(--hk-ink-700)' }}>{row.vendor || '—'}</div>
                        <div style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{row.auth_mode || '—'}</div>
                      </td>
                      <td style={tdMono}>v{row.credential_version}</td>
                      <td style={tdMono}>{relativeTime(row.access_expires_at, nowMs)}</td>
                      <td style={tdMono}>{relativeTime(row.refresh_before_at, nowMs)}</td>
                      <td style={tdMono}>{relativeTime(row.last_refresh_at, nowMs)}</td>
                      <td style={{ ...td, color: h === 'failing' ? '#8f322a' : 'var(--hk-ink-500)' }}>{failureSummary(row)}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}

        {cursor !== null && rows.length > 0 && (
          <div style={{ padding: 'var(--hk-space-4)', display: 'flex', justifyContent: 'center' }}>
            <button type="button" disabled={loadingMore} onClick={loadMore} style={ghostBtn}>
              {loadingMore ? '加载中…' : '加载更多'}
            </button>
          </div>
        )}
      </section>
    </div>
  )
}

/* ——— 本页私有小组件 / 样式 ——— */
function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}
function Banner({ kind, children }: { kind: 'error' | 'ok'; children: React.ReactNode }) {
  const palette =
    kind === 'error'
      ? { color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }
      : { color: '#0b6553', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
  return <div style={{ margin: 'var(--hk-space-4)', marginBottom: 0, padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>{children}</div>
}
function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

const card: React.CSSProperties = { background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }
const cardHead: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)', background: 'var(--hk-surface-sunken)' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'top' }
const tdMono: React.CSSProperties = { ...td, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
