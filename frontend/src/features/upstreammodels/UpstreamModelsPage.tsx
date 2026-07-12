import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { triggerModelSync } from './api'
import { buildSyncRequest, hasChanges, isReasonTooLong, itemSummary, itemTone } from './modelsync'
import { REASON_MAX, type ModelSyncResult } from './types'

/*
 * 厂商模型同步(运维台,admin)。POST /admin/v1/model-sync 手动触发全局模型目录同步:
 * 填可选 reason(≤200)→ 拉取各厂商最新模型目录,展示每厂商 added/updated/reactivated/
 * disabled/unchanged 汇总。高权操作(仅 platform_admin),影响所有继承 global catalog 的租户。
 */
export function UpstreamModelsPage() {
  const [reason, setReason] = useState('')
  const [running, setRunning] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [result, setResult] = useState<ModelSyncResult | null>(null)

  const tooLong = isReasonTooLong(reason)
  const remaining = REASON_MAX - [...reason.trim()].length

  const run = async () => {
    if (running || tooLong) return
    setRunning(true)
    setError(null)
    try {
      const res = await triggerModelSync(buildSyncRequest(reason))
      setResult(res)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '触发模型同步失败')
    } finally {
      setRunning(false)
    }
  }

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>厂商模型同步</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          手动触发全局模型目录同步 · 拉取各厂商最新模型并更新平台目录(高权操作,影响全部租户)。
        </p>
      </header>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          void run()
        }}
        style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)' }}
      >
        <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
          原因(reason,可选,记入审计)
          <input
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="如:接入新模型 / 定期目录刷新"
            style={{ ...inp, borderColor: tooLong ? 'var(--hk-danger)' : 'var(--hk-line)' }}
          />
        </label>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--hk-space-3)' }}>
          <span style={{ fontSize: 12, color: tooLong ? 'var(--hk-danger)' : 'var(--hk-ink-300)' }}>
            {tooLong ? `超出 ${-remaining} 字` : `剩余 ${remaining} 字`}
          </span>
          <button type="submit" disabled={running || tooLong} style={{ ...primaryBtn, opacity: running || tooLong ? 0.6 : 1, cursor: running || tooLong ? 'not-allowed' : 'pointer' }}>
            {running ? '同步中…' : '触发同步'}
          </button>
        </div>
      </form>

      {error && (
        <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>
          {error}
        </div>
      )}

      {result && <SyncResultView result={result} />}
    </div>
  )
}

function SyncResultView({ result }: { result: ModelSyncResult }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-3)', alignItems: 'center' }}>
        <StatusBadge tone={hasChanges(result) ? 'ok' : 'muted'}>
          {hasChanges(result) ? '已更新目录' : '无变化'}
        </StatusBadge>
        <Metric label="新增" value={result.total_added} />
        <Metric label="更新" value={result.total_updated} />
        <Metric label="停用" value={result.total_disabled} />
        <span style={{ fontSize: 12, color: 'var(--hk-ink-300)', marginLeft: 'auto' }}>
          完成于 {fmt(result.completed_at)}
        </span>
      </div>

      <div style={{ background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }}>
        {result.results.length === 0 ? (
          <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>
            本次同步未返回任何厂商明细。
          </div>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['厂商', '概况', '新增', '更新', '重启用', '停用', '未变', '快照更新'].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {result.results.map((it) => (
                  <tr key={it.vendor} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={{ ...td, fontWeight: 600, color: 'var(--hk-ink-900)' }}>{it.vendor}</td>
                    <td style={td}>
                      <StatusBadge tone={itemTone(it)}>{itemSummary(it)}</StatusBadge>
                    </td>
                    <td style={tdNum}>{it.added}</td>
                    <td style={tdNum}>{it.updated}</td>
                    <td style={tdNum}>{it.reactivated}</td>
                    <td style={tdNum}>{it.disabled}</td>
                    <td style={tdNum}>{it.unchanged}</td>
                    <td style={tdNum}>{it.snapshot_bumps}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}

function Metric({ label, value }: { label: string; value: number }) {
  return (
    <span style={{ display: 'inline-flex', alignItems: 'baseline', gap: 6 }}>
      <span style={{ fontSize: 18, fontWeight: 700, color: 'var(--hk-ink-900)' }}>{value}</span>
      <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>{label}</span>
    </span>
  )
}

function fmt(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso || '—' : d.toLocaleString('zh-CN', { hour12: false })
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'top' }
const tdNum: React.CSSProperties = { ...td, textAlign: 'right', fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600 }
