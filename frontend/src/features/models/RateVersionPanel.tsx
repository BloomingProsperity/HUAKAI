import { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { getRateSnapshot, listRateSnapshots } from './rateTableApi'
import {
  formatEffectiveRange,
  formatTime,
  isActiveSnapshot,
  parsePricingRows,
  prettyJSON,
  snapshotList,
  summarizeValue,
  type RateTable,
  type RateTableSnapshot,
} from './rateTable'

/*
 * 费率版本 / 快照(公开只读)面板。嵌入「模型与定价」页作为第二视图(费率版本透明)。
 * 左列=历史快照列表(GET /v1/pricing/snapshots),点选某版本后右抽屉拉取该快照详情
 * (GET /v1/pricing/snapshots/{id}),展示生效区间 + pricing_data 表格化/原始 JSON。
 * 纯只读,无任何写动作。
 */
export function RateVersionPanel() {
  const [snapshots, setSnapshots] = useState<RateTableSnapshot[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [selected, setSelected] = useState<RateTableSnapshot | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError(null)
    listRateSnapshots(ctrl.signal)
      .then((resp) => setSnapshots(snapshotList(resp)))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载费率快照失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [])

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
        费率版本透明 · 公开只读。每次价格调整都会留存一份不可变快照,共 {snapshots.length} 个版本。点选查看该版本生效区间与逐模型费率。
      </p>

      {error && <div style={errorBox}>{error}</div>}

      {loading && snapshots.length === 0 ? (
        <EmptyState title="正在加载费率快照" hint="请稍候。" />
      ) : snapshots.length === 0 ? (
        <EmptyState title="暂无费率快照" hint="价格调整后会在此留存历史版本。" />
      ) : (
        <div style={tableWrap}>
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['版本', '状态', '生效区间', '创建时间', ''].map((h, i) => (
                    <th key={h || `col-${i}`} style={th}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {snapshots.map((s) => (
                  <tr key={s.id} onClick={() => setSelected(s)} style={{ borderTop: '1px solid var(--hk-line)', cursor: 'pointer' }}>
                    <td style={td}>
                      <code style={{ fontFamily: 'var(--hk-font-mono)', fontSize: 12, color: 'var(--hk-ink-900)' }}>{s.version}</code>
                    </td>
                    <td style={td}>
                      {isActiveSnapshot(s) ? (
                        <StatusBadge tone="ok">当前生效</StatusBadge>
                      ) : (
                        <StatusBadge tone="muted">历史</StatusBadge>
                      )}
                    </td>
                    <td style={td}>{formatEffectiveRange(s)}</td>
                    <td style={td}>{formatTime(s.created_at)}</td>
                    <td style={{ ...td, textAlign: 'right', color: 'var(--hk-ink-300)' }}>查看 ›</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {selected && <SnapshotDrawer snapshot={selected} onClose={() => setSelected(null)} />}
    </div>
  )
}

/* 选中某快照后的详情抽屉:拉取 GET /v1/pricing/snapshots/{id}。 */
function SnapshotDrawer({ snapshot, onClose }: { snapshot: RateTableSnapshot; onClose: () => void }) {
  const [table, setTable] = useState<RateTable | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [rawOpen, setRawOpen] = useState(false)

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError(null)
    setTable(null)
    getRateSnapshot(snapshot.id, ctrl.signal)
      .then((t) => setTable(t))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载费率详情失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [snapshot.id])

  const rows = table ? parsePricingRows(table.pricing_data) : []

  return (
    <div style={drawerOverlay} onClick={onClose}>
      <aside style={drawer} onClick={(e) => e.stopPropagation()}>
        <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--hk-space-2)' }}>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
            <code style={{ fontFamily: 'var(--hk-font-mono)', fontSize: 15, color: 'var(--hk-ink-900)' }}>{snapshot.version}</code>
            <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>快照 #{snapshot.id}</span>
          </div>
          <button type="button" onClick={onClose} style={iconBtn} aria-label="关闭">✕</button>
        </header>

        <DetailRow label="状态">
          {isActiveSnapshot(snapshot) ? <StatusBadge tone="ok">当前生效</StatusBadge> : <StatusBadge tone="muted">历史版本</StatusBadge>}
        </DetailRow>
        <DetailRow label="生效区间">{formatEffectiveRange(snapshot)}</DetailRow>
        <DetailRow label="创建时间">{formatTime(snapshot.created_at)}</DetailRow>

        {error && <div style={{ ...errorBox, marginTop: 'var(--hk-space-3)' }}>{error}</div>}

        {loading ? (
          <EmptyState title="正在加载费率明细" hint="请稍候。" />
        ) : table ? (
          <div style={{ marginTop: 'var(--hk-space-3)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>逐模型费率({rows.length} 行)</span>
              <button type="button" onClick={() => setRawOpen((v) => !v)} style={linkBtn}>
                {rawOpen ? '隐藏原始 JSON' : '查看原始 JSON'}
              </button>
            </div>
            {rows.length > 0 ? (
              <div style={tableWrap}>
                <div style={{ overflowX: 'auto', maxHeight: 320 }}>
                  <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
                    <thead>
                      <tr>
                        {['模型', '费率'].map((h) => (
                          <th key={h} style={th}>{h}</th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {rows.map((r) => (
                        <tr key={r.model} style={{ borderTop: '1px solid var(--hk-line)' }}>
                          <td style={td}><code style={mono}>{r.model}</code></td>
                          <td style={{ ...td, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', wordBreak: 'break-all' }}>{summarizeValue(r.value)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            ) : (
              <span style={{ fontSize: 12, color: 'var(--hk-ink-300)' }}>该版本定价数据非表格结构,见下方原始 JSON。</span>
            )}
            {(rawOpen || rows.length === 0) && (
              <pre style={pre}>{prettyJSON(table.pricing_data)}</pre>
            )}
          </div>
        ) : null}
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

const tableWrap: React.CSSProperties = {
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
  boxShadow: 'var(--hk-shadow-1)',
  overflow: 'hidden',
}
const errorBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
const mono: React.CSSProperties = { fontFamily: 'var(--hk-font-mono)', fontSize: 12 }
const iconBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-ink-500)', fontSize: 16, cursor: 'pointer' }
const linkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-500)', fontSize: 12, cursor: 'pointer', padding: 0 }
const pre: React.CSSProperties = {
  margin: 0,
  padding: 'var(--hk-space-3)',
  background: 'var(--hk-surface-sunken)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  fontFamily: 'var(--hk-font-mono)',
  fontSize: 11,
  color: 'var(--hk-ink-700)',
  overflowX: 'auto',
  maxHeight: 320,
  whiteSpace: 'pre-wrap',
  wordBreak: 'break-all',
}
const drawerOverlay: React.CSSProperties = { position: 'fixed', inset: 0, background: 'rgba(28,38,34,0.4)', display: 'flex', justifyContent: 'flex-end', zIndex: 'var(--hk-z-overlay)' as unknown as number }
const drawer: React.CSSProperties = {
  width: 'min(520px, 100%)',
  height: '100%',
  overflowY: 'auto',
  background: 'var(--hk-surface)',
  boxShadow: 'var(--hk-shadow-3)',
  padding: 'var(--hk-space-5)',
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-1)',
}
