import { useEffect, useMemo, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { listPricing } from './api'
import { capabilityList, filterModels, groupByOwner, pricePerMillion } from './pricing'
import type { PricingItem } from './types'

/*
 * 模型与定价(P0)。管线第 6 站。公开定价目录 GET /v1/pricing/page:
 * 按厂商分组的模型卡片 + 每-百万-token 定价 + 上下文窗口 + 能力标签 + 搜索。纯只读公开目录。
 */
export function ModelsPage() {
  const [items, setItems] = useState<PricingItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [query, setQuery] = useState('')

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError(null)
    listPricing(ctrl.signal)
      .then((data) => setItems(data))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载定价目录失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [])

  const groups = useMemo(() => groupByOwner(filterModels(items, query)), [items, query])

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>模型与定价</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          管线第 6 站 · 公开价目表(每百万 token 计价)。共 {items.length} 个模型。
        </p>
      </header>

      <div style={{ background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)' }}>
        <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="按模型名或厂商搜索" style={{ ...inp, maxWidth: 360 }} />
      </div>

      {error && <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }}>{error}</div>}

      {loading && items.length === 0 ? (
        <Empty>加载中…</Empty>
      ) : groups.length === 0 ? (
        <Empty>没有匹配的模型。</Empty>
      ) : (
        groups.map((g) => (
          <section key={g.owner} style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
            <h2 style={{ fontSize: 14, color: 'var(--hk-ink-700)', display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
              {g.owner}
              <span style={{ fontSize: 12, color: 'var(--hk-ink-300)', fontWeight: 400 }}>({g.models.length})</span>
            </h2>
            <div style={{ background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }}>
              <div style={{ overflowX: 'auto' }}>
                <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
                  <thead>
                    <tr>
                      {['模型', '输入 / 1M', '输出 / 1M', '上下文', '能力'].map((h) => (
                        <th key={h} style={th}>
                          {h}
                        </th>
                      ))}
                    </tr>
                  </thead>
                  <tbody>
                    {g.models.map((m) => (
                      <tr key={m.model} style={{ borderTop: '1px solid var(--hk-line)' }}>
                        <td style={td}>
                          <div style={{ display: 'flex', flexDirection: 'column' }}>
                            <code style={{ fontFamily: 'var(--hk-font-mono)', fontSize: 12, color: 'var(--hk-ink-900)' }}>{m.model}</code>
                            {m.canonical_id && m.canonical_id !== m.model && (
                              <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{m.canonical_id}</span>
                            )}
                          </div>
                        </td>
                        <td style={tdNum}>{pricePerMillion(m.input_price_per_token)}</td>
                        <td style={tdNum}>{pricePerMillion(m.output_price_per_token)}</td>
                        <td style={tdNum}>{m.context_length ? fmtTokens(m.context_length) : '—'}</td>
                        <td style={td}>
                          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
                            {capabilityList(m.capabilities).map((c) => (
                              <StatusBadge key={c} tone="info">
                                {c}
                              </StatusBadge>
                            ))}
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </section>
        ))
      )}
    </div>
  )
}

function fmtTokens(n: number): string {
  if (n >= 1000) return `${Math.round(n / 1000)}K`
  return String(n)
}
function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
const tdNum: React.CSSProperties = { ...td, textAlign: 'right', fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
