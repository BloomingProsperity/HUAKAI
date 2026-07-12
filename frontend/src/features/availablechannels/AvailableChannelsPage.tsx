import { useEffect, useMemo, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { listAvailableChannels } from './api'
import {
  buildChannels,
  capabilityList,
  filterCatalog,
  formatPrice,
  formatPriceRange,
  type Channel,
  type PriceUnit,
} from './channels'
import type { PricingItem } from './types'

/*
 * 可用渠道目录(user 壳)。消费 GET /v1/pricing/page(公开无鉴权)。
 * 与「模型与定价」扁平广场不同:本页以「厂商 = 渠道」聚合,先看渠道(价目区间/模型数/能力),
 * 再展开看渠道内各模型的逐条价目。纯只读,不含任何写动作(目录无写端点,money 无涉及)。
 * 注:公开端点不暴露分组倍率(ratio 仅在 DB pricingcatalog 层未进公开 DTO),故以价目区间替代。
 */
export function AvailableChannelsPage() {
  const [items, setItems] = useState<PricingItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [query, setQuery] = useState('')
  const [unit, setUnit] = useState<PriceUnit>('mtok')
  const [expanded, setExpanded] = useState<Set<string>>(new Set())

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError(null)
    listAvailableChannels(ctrl.signal)
      .then((data) => setItems(data))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载可用渠道目录失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [])

  const filtered = useMemo(() => filterCatalog(items, query), [items, query])
  const channels = useMemo(() => buildChannels(filtered), [filtered])

  const unitLabel = unit === 'mtok' ? '每百万 token' : '每 token'
  const toggle = (name: string) =>
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>可用渠道目录</h1>
          <p className="hk-sub">
            按厂商聚合的可用模型渠道与价目(公开价目表,{unitLabel})。共 {channels.length} 个渠道 ·{' '}
            {filtered.length} 个模型。
          </p>
        </div>
      </header>

      <div style={toolbar}>
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="按模型名 / canonical / 厂商搜索"
          style={{ ...inp, flex: '1 1 220px', maxWidth: 320 }}
        />
        <div style={{ flex: 1 }} />
        <Toggle
          options={[
            { v: 'mtok', l: '$/1M' },
            { v: 'token', l: '$/token' },
          ]}
          value={unit}
          onChange={(v) => setUnit(v as PriceUnit)}
        />
      </div>

      {error && <div style={errorBox}>{error}</div>}

      {loading && items.length === 0 ? (
        <Empty>加载中…</Empty>
      ) : channels.length === 0 ? (
        <Empty>没有匹配的渠道。</Empty>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
          {channels.map((ch) => (
            <ChannelCard
              key={ch.name}
              channel={ch}
              unit={unit}
              open={expanded.has(ch.name)}
              onToggle={() => toggle(ch.name)}
            />
          ))}
        </div>
      )}
    </div>
  )
}

function ChannelCard({
  channel,
  unit,
  open,
  onToggle,
}: {
  channel: Channel
  unit: PriceUnit
  open: boolean
  onToggle: () => void
}) {
  return (
    <div className="hk-card">
      <button type="button" onClick={onToggle} style={cardHead} aria-expanded={open}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 2, minWidth: 0, textAlign: 'left' }}>
          <span style={{ fontSize: 15, fontWeight: 600, color: 'var(--hk-ink-900)' }}>{channel.name}</span>
          <span style={{ fontSize: 12, color: 'var(--hk-ink-300)' }}>
            {channel.modelCount} 个模型 · 输出价 {formatPriceRange(channel.outputPriceRange)} / 1M
          </span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
          {channel.capabilities.slice(0, 4).map((c) => (
            <StatusBadge key={c} tone="info">
              {c}
            </StatusBadge>
          ))}
          <span style={{ color: 'var(--hk-ink-500)', fontSize: 13, width: 14, textAlign: 'center' }}>
            {open ? '▾' : '▸'}
          </span>
        </div>
      </button>

      {open && (
        <div className="hk-tablewrap" style={{ borderTop: '1px solid var(--hk-line)' }}>
          <table className="hk-table">
            <thead>
              <tr>
                {['模型', '模式', '输入价', '输出价', '上下文', '能力'].map((h) => (
                  <th key={h}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {channel.models.map((m) => (
                <ModelRow key={m.model} m={m} unit={unit} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

function ModelRow({ m, unit }: { m: PricingItem; unit: PriceUnit }) {
  const caps = capabilityList(m.capabilities)
  return (
    <tr>
      <td>
        <div style={{ display: 'flex', flexDirection: 'column' }}>
          <code style={{ fontFamily: 'var(--hk-font-mono)', fontSize: 12, color: 'var(--hk-ink-900)' }}>{m.model}</code>
          {m.canonical_id && m.canonical_id !== m.model && (
            <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{m.canonical_id}</span>
          )}
        </div>
      </td>
      <td>{m.mode || '—'}</td>
      <td className="hk-mono" style={{ textAlign: 'right' }}>{formatPrice(m.input_price_per_token, unit)}</td>
      <td className="hk-mono" style={{ textAlign: 'right' }}>{formatPrice(m.output_price_per_token, unit)}</td>
      <td className="hk-mono" style={{ textAlign: 'right' }}>{m.context_length ? fmtTokens(m.context_length) : '—'}</td>
      <td>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
          {caps.length > 0 ? (
            caps.map((c) => (
              <StatusBadge key={c} tone="info">
                {c}
              </StatusBadge>
            ))
          ) : (
            <span style={{ color: 'var(--hk-ink-300)' }}>—</span>
          )}
        </div>
      </td>
    </tr>
  )
}

function Toggle({
  options,
  value,
  onChange,
}: {
  options: Array<{ v: string; l: string }>
  value: string
  onChange: (v: string) => void
}) {
  return (
    <div style={{ display: 'inline-flex', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', overflow: 'hidden' }}>
      {options.map((o) => (
        <button
          key={o.v}
          type="button"
          onClick={() => onChange(o.v)}
          style={{
            height: 32,
            padding: '0 var(--hk-space-3)',
            fontSize: 12,
            cursor: 'pointer',
            border: 'none',
            background: value === o.v ? 'var(--hk-primary-500)' : 'var(--hk-surface)',
            color: value === o.v ? '#fff' : 'var(--hk-ink-700)',
          }}
        >
          {o.l}
        </button>
      ))}
    </div>
  )
}

function fmtTokens(n: number): string {
  if (n >= 1000) return `${Math.round(n / 1000)}K`
  return String(n)
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div className="hk-empty">{children}</div>
}

const toolbar: React.CSSProperties = {
  display: 'flex',
  flexWrap: 'wrap',
  alignItems: 'center',
  gap: 'var(--hk-space-2)',
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
  padding: 'var(--hk-space-3)',
}
const inp: React.CSSProperties = {
  height: 32,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-sm)',
  fontSize: 13,
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
  width: '100%',
}
const cardHead: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  gap: 'var(--hk-space-3)',
  width: '100%',
  padding: 'var(--hk-space-4)',
  background: 'var(--hk-surface)',
  border: 'none',
  cursor: 'pointer',
}
const errorBox: React.CSSProperties = {
  padding: 'var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  color: 'var(--hk-danger)',
  background: 'var(--hk-danger-soft)',
  border: '1px solid var(--hk-danger-soft)',
}
