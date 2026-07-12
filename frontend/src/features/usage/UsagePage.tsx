import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { listApiKeys } from '../keys/api'
import type { ApiKeyView } from '../keys/types'
import { getKeyUsageSummary, getQuota } from './api'
import { metricLabel, quotaProgress, windowLabel } from './quota'
import type { KeyUsageSummary, QuotaWindow } from './types'

/*
 * 用量与配额(P0)。管线第 4 站。session 鉴权:
 *  - 配额窗口(/v1/me/quota)→ 每个 metric×window 的 cap/consumed/remaining + 进度条;
 *  - per-key 用量(列 active key,各取 /v1/me/keys/{id}/usage-summary)→ 花费/请求数/tokens。
 * 说明:per-request 请求日志(/v1/me/usage)是 API key 鉴权、session 不可达,故本页用 quota+按 key 汇总。
 */
export function UsagePage() {
  const [windows, setWindows] = useState<QuotaWindow[]>([])
  const [rows, setRows] = useState<Array<{ key: ApiKeyView; summary: KeyUsageSummary | null }>>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async (signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    try {
      const [quota, keys] = await Promise.all([getQuota(signal), listApiKeys(0, 100, signal)])
      if (signal.aborted) return
      setWindows(quota.items)
      const active = keys.api_keys.filter((k) => k.status === 'active')
      const summaries = await Promise.all(
        active.map((k) =>
          getKeyUsageSummary(k.api_key_id, signal)
            .then((s) => ({ key: k, summary: s }))
            .catch(() => ({ key: k, summary: null })),
        ),
      )
      if (!signal.aborted) setRows(summaries)
    } catch (e) {
      if (signal.aborted) return
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载用量失败')
    } finally {
      if (!signal.aborted) setLoading(false)
    }
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    void load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>用量与配额</h1>
          <p className="hk-sub">管线第 4 站 · 配额窗口与各密钥用量。</p>
        </div>
      </header>

      {error && <Banner>{error}</Banner>}

      <Card title="配额窗口">
        {loading && windows.length === 0 ? (
          <Muted>加载中…</Muted>
        ) : windows.length === 0 ? (
          <Muted>当前账户未配置配额限制(无上限)。</Muted>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
            {windows.map((w, i) => (
              <QuotaBar key={`${w.metric}-${w.window_kind}-${i}`} w={w} />
            ))}
          </div>
        )}
      </Card>

      <Card title="各密钥用量汇总">
        {loading && rows.length === 0 ? (
          <Muted>加载中…</Muted>
        ) : rows.length === 0 ? (
          <Muted>没有活跃密钥可统计。</Muted>
        ) : (
          <div className="hk-tablewrap">
            <table className="hk-table">
              <thead>
                <tr>
                  {['密钥', '花费(USD)', '请求数', '输入 Token', '输出 Token', '缓存读/写'].map((h) => (
                    <th key={h}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map(({ key, summary }) => (
                  <tr key={key.api_key_id}>
                    <td>
                      <div style={{ display: 'flex', flexDirection: 'column' }}>
                        <span style={{ fontWeight: 600, color: 'var(--hk-ink-900)' }}>{key.name}</span>
                        <code style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{key.key_prefix}</code>
                      </div>
                    </td>
                    {summary ? (
                      <>
                        <td className="hk-mono" style={{ textAlign: 'right' }}>{summary.total_cost}</td>
                        <td className="hk-mono" style={{ textAlign: 'right' }}>{summary.request_count}</td>
                        <td className="hk-mono" style={{ textAlign: 'right' }}>{summary.total_tokens_input}</td>
                        <td className="hk-mono" style={{ textAlign: 'right' }}>{summary.total_tokens_output}</td>
                        <td className="hk-mono" style={{ textAlign: 'right' }}>
                          {summary.total_cache_read_tokens}/{summary.total_cache_creation_tokens}
                        </td>
                      </>
                    ) : (
                      <td style={{ color: 'var(--hk-ink-300)' }} colSpan={5}>
                        汇总不可用
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  )
}

function QuotaBar({ w }: { w: QuotaWindow }) {
  const p = quotaProgress(w.consumed, w.cap)
  const barColor = p.tone === 'danger' ? 'var(--hk-danger)' : p.tone === 'warn' ? 'var(--hk-warn)' : 'var(--hk-primary-500)'
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13 }}>
        <span style={{ color: 'var(--hk-ink-700)' }}>
          {metricLabel(w.metric)} · {windowLabel(w.window_kind)}
          {w.request_count > 0 ? ` · ${w.request_count} 次请求` : ''}
        </span>
        <span className="hk-mono" style={{ color: p.over ? 'var(--hk-danger)' : 'var(--hk-ink-500)' }}>
          {w.consumed} / {p.unlimited ? '无上限' : w.cap}
          {p.over ? `(超额 ${w.overage})` : ''}
        </span>
      </div>
      {!p.unlimited && (
        <div style={{ height: 8, background: 'var(--hk-surface-sunken)', borderRadius: 'var(--hk-radius-pill)', overflow: 'hidden' }}>
          <div style={{ width: `${p.pct}%`, height: '100%', background: barColor, transition: 'width .2s' }} />
        </div>
      )}
    </div>
  )
}

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>{title}</h3>
      </div>
      <div className="hk-card__body">{children}</div>
    </section>
  )
}

function Muted({ children }: { children: React.ReactNode }) {
  return <div className="hk-empty">{children}</div>
}

function Banner({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>
      {children}
    </div>
  )
}
