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
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>凭证续期监控</h1>
          <p className="hk-sub">
            只读:各上游账号凭证的临期与续期健康态。失败/已过期/待续期会高亮,便于运营提前介入。
          </p>
        </div>
      </header>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          applyTenantFilter()
        }}
        className="hk-card"
        style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', padding: 'var(--hk-space-4)' }}
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
        <button type="submit" className="hk-btn hk-btn--green">
          应用
        </button>
        <button
          type="button"
          onClick={() => {
            setTenantDraft('')
            setTenantFilter(undefined)
          }}
          className="hk-btn"
        >
          重置
        </button>
      </form>

      <section className="hk-card">
        <div className="hk-card__head">
          <h3>续期状态</h3>
          <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--hk-ink-300)' }}>
            已载 {rows.length} 条{cursor !== null ? '(还有更多)' : ''}
          </span>
        </div>

        {error && <Banner kind="error">{error}</Banner>}

        {loading && rows.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : rows.length === 0 ? (
          <Empty>暂无凭证续期记录。</Empty>
        ) : (
          <div className="hk-tablewrap">
            <table className="hk-table">
              <thead>
                <tr>
                  {['续期状态', '租户', '账号', '厂商 / 模式', '版本', '距到期', '续期窗口', '最近刷新', '失败'].map((h) => (
                    <th key={h}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => {
                  const h = renewHealth(row, nowMs)
                  return (
                    <tr key={row.id}>
                      <td>
                        <StatusBadge tone={renewHealthTone(h)}>{renewHealthLabel(h)}</StatusBadge>
                      </td>
                      <td>
                        <div style={{ color: 'var(--hk-ink-900)' }}>{row.tenant_name || '—'}</div>
                        <div className="hk-mono" style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>#{row.tenant_id}</div>
                      </td>
                      <td>
                        <div style={{ color: 'var(--hk-ink-900)' }}>{row.account_name || '—'}</div>
                        <div className="hk-mono" style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>#{row.account_id}</div>
                      </td>
                      <td>
                        <div style={{ color: 'var(--hk-ink-700)' }}>{row.vendor || '—'}</div>
                        <div style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{row.auth_mode || '—'}</div>
                      </td>
                      <td className="hk-mono">v{row.credential_version}</td>
                      <td className="hk-mono">{relativeTime(row.access_expires_at, nowMs)}</td>
                      <td className="hk-mono">{relativeTime(row.refresh_before_at, nowMs)}</td>
                      <td className="hk-mono">{relativeTime(row.last_refresh_at, nowMs)}</td>
                      <td style={{ color: h === 'failing' ? 'var(--hk-danger)' : 'var(--hk-ink-500)' }}>{failureSummary(row)}</td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}

        {cursor !== null && rows.length > 0 && (
          <div style={{ padding: 'var(--hk-space-4)', display: 'flex', justifyContent: 'center' }}>
            <button type="button" disabled={loadingMore} onClick={loadMore} className="hk-btn">
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
      ? { color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
      : { color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
  return <div style={{ margin: 'var(--hk-space-4)', marginBottom: 0, padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>{children}</div>
}
function Empty({ children }: { children: React.ReactNode }) {
  return <div className="hk-empty">{children}</div>
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
