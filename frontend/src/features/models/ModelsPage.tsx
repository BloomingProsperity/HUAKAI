import { useEffect, useMemo, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { listPricing } from './api'
import { RateVersionPanel } from './RateVersionPanel'
import {
  applyFilters,
  capabilityList,
  collectCapabilities,
  collectModes,
  collectOwners,
  EMPTY_MODEL_FILTERS,
  formatPrice,
  mapModelTableRows,
  type ModelFilters,
  type ModelTableRow,
  type PriceUnit,
} from './pricing'
import type { PricingItem } from './types'

/*
 * 模型与定价(P0,公开广场页)。管线第 6 站。消费 GET /v1/pricing/page(公开无鉴权)。
 * 增强:多维筛选(搜索 / 厂商 / 模式 / 能力)+ 卡片·表格双视图 + 价格单位切换($/MTok ↔ $/token)
 * + 模型详情抽屉。纯只读对外门面。注:该端点仅返回已配置定价的模型,故不做"无价"维度。
 */
type ViewMode = 'cards' | 'table'
/** 页签:模型目录(当前价目)/ 费率版本(历史快照透明)。 */
type Tab = 'catalog' | 'versions'

export function ModelsPage() {
  const [tab, setTab] = useState<Tab>('catalog')
  const [items, setItems] = useState<PricingItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [filters, setFilters] = useState<ModelFilters>(EMPTY_MODEL_FILTERS)
  const [view, setView] = useState<ViewMode>('cards')
  const [unit, setUnit] = useState<PriceUnit>('mtok')
  const [selected, setSelected] = useState<PricingItem | null>(null)

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

  const owners = useMemo(() => collectOwners(items), [items])
  const modes = useMemo(() => collectModes(items), [items])
  const capabilities = useMemo(() => collectCapabilities(items), [items])
  const filtered = useMemo(() => applyFilters(items, filters), [items, filters])

  const setF = <K extends keyof ModelFilters>(k: K, v: ModelFilters[K]) => setFilters((f) => ({ ...f, [k]: v }))
  const unitLabel = unit === 'mtok' ? '/ 1M' : '/ token'

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>模型与定价</h1>
          <p className="hk-sub">
            {tab === 'catalog'
              ? `管线第 6 站 · 公开价目表。共 ${items.length} 个模型${filtered.length !== items.length ? `,筛选出 ${filtered.length} 个` : ''}。`
              : '费率版本透明 · 历史价格快照只读查询。'}
          </p>
        </div>
        <Toggle
          options={[{ v: 'catalog', l: '模型目录' }, { v: 'versions', l: '费率版本' }]}
          value={tab}
          onChange={(v) => setTab(v as Tab)}
        />
      </header>

      {tab === 'versions' ? (
        <RateVersionPanel />
      ) : (
      <>
      <div style={toolbar}>
        <input value={filters.query} onChange={(e) => setF('query', e.target.value)} placeholder="按模型名 / canonical / 厂商搜索" style={{ ...inp, flex: '1 1 220px', maxWidth: 320 }} />
        <Select value={filters.owner} onChange={(v) => setF('owner', v)} allLabel="全部厂商" options={owners} />
        <Select value={filters.mode} onChange={(v) => setF('mode', v)} allLabel="全部模式" options={modes} />
        <Select value={filters.capability} onChange={(v) => setF('capability', v)} allLabel="全部能力" options={capabilities} />
        <div style={{ flex: 1 }} />
        <Toggle options={[{ v: 'mtok', l: '$/1M' }, { v: 'token', l: '$/token' }]} value={unit} onChange={(v) => setUnit(v as PriceUnit)} />
        <Toggle options={[{ v: 'cards', l: '卡片' }, { v: 'table', l: '表格' }]} value={view} onChange={(v) => setView(v as ViewMode)} />
      </div>

      {error && <div style={errorBox}>{error}</div>}

      {loading && items.length === 0 ? (
        <EmptyState title="正在加载模型目录" hint="请稍候。" />
      ) : filtered.length === 0 ? (
        <EmptyState title="没有匹配的模型" hint="请调整搜索词或筛选条件。" />
      ) : view === 'cards' ? (
        <div style={cardGrid}>
          {filtered.map((m) => (
            <ModelCard key={m.model} m={m} unit={unit} unitLabel={unitLabel} onOpen={() => setSelected(m)} />
          ))}
        </div>
      ) : (
        <ModelTable items={filtered} unit={unit} unitLabel={unitLabel} onOpen={setSelected} />
      )}

      {selected && <ModelDrawer m={selected} unit={unit} onClose={() => setSelected(null)} />}
      </>
      )}
    </div>
  )
}

function ModelCard({ m, unit, unitLabel, onOpen }: { m: PricingItem; unit: PriceUnit; unitLabel: string; onOpen: () => void }) {
  const caps = capabilityList(m.capabilities)
  return (
    <button type="button" onClick={onOpen} style={card}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 2, minWidth: 0 }}>
        <code style={{ fontFamily: 'var(--hk-font-mono)', fontSize: 13, color: 'var(--hk-ink-900)', wordBreak: 'break-all' }}>{m.model}</code>
        <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{m.owned_by || '其他'}{m.mode ? ` · ${m.mode}` : ''}</span>
      </div>
      <div style={{ display: 'flex', gap: 'var(--hk-space-4)', fontSize: 12 }}>
        <PriceCell label={`输入 ${unitLabel}`} value={formatPrice(m.input_price_per_token, unit)} />
        <PriceCell label={`输出 ${unitLabel}`} value={formatPrice(m.output_price_per_token, unit)} />
      </div>
      {caps.length > 0 && (
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
          {caps.map((c) => (
            <StatusBadge key={c} tone="info">{c}</StatusBadge>
          ))}
        </div>
      )}
    </button>
  )
}

function PriceCell({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column' }}>
      <span style={{ fontSize: 10, color: 'var(--hk-ink-300)' }}>{label}</span>
      <span style={{ fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)' }}>{value}</span>
    </div>
  )
}

function ModelTable({ items, unit, unitLabel, onOpen }: { items: PricingItem[]; unit: PriceUnit; unitLabel: string; onOpen: (m: PricingItem) => void }) {
  const rows = mapModelTableRows(items, unit)
  const columns = modelColumns(unitLabel)
  return (
    <div className="hk-card">
      <DataListTable
        label="模型目录"
        rows={rows}
        rowKey={(row) => row.id}
        columns={columns}
        actions={[{ label: '查看', onClick: (row) => onOpen(row.item) }]}
      />
    </div>
  )
}

function ModelDrawer({ m, unit, onClose }: { m: PricingItem; unit: PriceUnit; onClose: () => void }) {
  const caps = capabilityList(m.capabilities)
  const unitLabel = unit === 'mtok' ? '每百万 token' : '每 token'
  return (
    <div style={drawerOverlay} onClick={onClose}>
      <aside style={drawer} onClick={(e) => e.stopPropagation()}>
        <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--hk-space-2)' }}>
          <code style={{ fontFamily: 'var(--hk-font-mono)', fontSize: 15, color: 'var(--hk-ink-900)', wordBreak: 'break-all' }}>{m.model}</code>
          <button type="button" onClick={onClose} style={iconBtn} aria-label="关闭">✕</button>
        </header>
        <DetailRow label="厂商">{m.owned_by || '其他'}</DetailRow>
        {m.canonical_id && <DetailRow label="canonical_id"><code style={mono}>{m.canonical_id}</code></DetailRow>}
        {m.mode && <DetailRow label="模式">{m.mode}</DetailRow>}
        <DetailRow label={`输入价(${unitLabel})`}><span style={mono}>{formatPrice(m.input_price_per_token, unit)}</span></DetailRow>
        <DetailRow label={`输出价(${unitLabel})`}><span style={mono}>{formatPrice(m.output_price_per_token, unit)}</span></DetailRow>
        <DetailRow label="上下文窗口">{m.context_length ? `${m.context_length.toLocaleString()} tokens` : '—'}</DetailRow>
        <DetailRow label="最大输出">{m.max_output_tokens ? `${m.max_output_tokens.toLocaleString()} tokens` : '—'}</DetailRow>
        <DetailRow label="能力">
          {caps.length > 0 ? (
            <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
              {caps.map((c) => (
                <StatusBadge key={c} tone="info">{c}</StatusBadge>
              ))}
            </div>
          ) : (
            <span style={{ color: 'var(--hk-ink-300)' }}>无</span>
          )}
        </DetailRow>
      </aside>
    </div>
  )
}

function DetailRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2, padding: 'var(--hk-space-2) 0', borderBottom: '1px solid var(--hk-line)' }}>
      <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>{label}</span>
      <div style={{ fontSize: 13, color: 'var(--hk-ink-900)' }}>{children}</div>
    </div>
  )
}

function Select({ value, onChange, allLabel, options }: { value: string; onChange: (v: string) => void; allLabel: string; options: string[] }) {
  return (
    <select value={value} onChange={(e) => onChange(e.target.value)} style={{ ...inp, width: 'auto', minWidth: 120 }}>
      <option value="">{allLabel}</option>
      {options.map((o) => (
        <option key={o} value={o}>{o}</option>
      ))}
    </select>
  )
}

function Toggle({ options, value, onChange }: { options: Array<{ v: string; l: string }>; value: string; onChange: (v: string) => void }) {
  return (
    <div className="hk-seg">
      {options.map((o) => (
        <button
          key={o.v}
          type="button"
          className={value === o.v ? 'is-on' : undefined}
          onClick={() => onChange(o.v)}
        >
          {o.l}
        </button>
      ))}
    </div>
  )
}

function modelColumns(unitLabel: string): DataListColumn<ModelTableRow>[] {
  return [
    { key: 'model', label: '模型', render: (row) => <div style={{ display: 'flex', flexDirection: 'column' }}><code style={modelCode}>{row.model}</code>{row.canonicalId && <span style={secondaryText}>{row.canonicalId}</span>}</div> },
    { key: 'owner', label: '厂商', render: (row) => row.owner },
    { key: 'input', label: `输入 ${unitLabel}`, render: (row) => <span className="hk-mono">{row.inputPrice}</span> },
    { key: 'output', label: `输出 ${unitLabel}`, render: (row) => <span className="hk-mono">{row.outputPrice}</span> },
    { key: 'context', label: '上下文', render: (row) => <span className="hk-mono">{row.contextLength}</span> },
    { key: 'capabilities', label: '能力', render: (row) => <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>{row.capabilities.map((capability) => <StatusBadge key={capability} tone="info">{capability}</StatusBadge>)}</div> },
  ]
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
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const cardGrid: React.CSSProperties = { display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))', gap: 'var(--hk-space-3)' }
const card: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-2)',
  alignItems: 'stretch',
  textAlign: 'left',
  padding: 'var(--hk-space-4)',
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
  boxShadow: 'var(--hk-shadow-1)',
  cursor: 'pointer',
}
const errorBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
const mono: React.CSSProperties = { fontFamily: 'var(--hk-font-mono)', fontSize: 12 }
const modelCode: React.CSSProperties = { fontFamily: 'var(--hk-font-mono)', fontSize: 12, color: 'var(--hk-ink-900)' }
const secondaryText: React.CSSProperties = { fontSize: 11, color: 'var(--hk-ink-300)' }
const iconBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-ink-500)', fontSize: 16, cursor: 'pointer' }
const drawerOverlay: React.CSSProperties = { position: 'fixed', inset: 0, background: 'rgba(28,38,34,0.4)', display: 'flex', justifyContent: 'flex-end', zIndex: 'var(--hk-z-overlay)' as unknown as number }
const drawer: React.CSSProperties = {
  width: 'min(420px, 100%)',
  height: '100%',
  overflowY: 'auto',
  background: 'var(--hk-surface)',
  boxShadow: 'var(--hk-shadow-3)',
  padding: 'var(--hk-space-5)',
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-1)',
}
