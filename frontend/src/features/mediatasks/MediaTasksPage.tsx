import { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { listMediaTasks } from './api'
import { formatTaskCost, isActive, statusLabel, statusTone, taskTypeLabel } from './mediatasks'
import type { MediaTask } from './types'

/*
 * 媒体任务记录(用户门户)。AI 绘图/视频/音频异步任务的历史与状态,只读(列表+进度+计费)。
 * session 鉴权,不触钱不翻默认。提交新任务会触发真实生成与计费,属 Owner-gated,不在本页。
 * 后端 GET /v1/media-tasks。
 */
export function MediaTasksPage() {
  const [tasks, setTasks] = useState<MediaTask[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshKey, setRefreshKey] = useState(0)

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError(null)
    listMediaTasks(50, ctrl.signal)
      .then((r) => {
        if (!ctrl.signal.aborted) setTasks(r.items ?? [])
      })
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载任务记录失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [refreshKey])

  const activeCount = tasks.filter((t) => isActive(t.status)).length

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
          <h1 style={{ fontSize: 22 }}>媒体任务记录</h1>
          <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
            绘图 / 视频 / 音频异步任务的历史与状态{activeCount > 0 ? ` · ${activeCount} 个进行中` : ''}。
          </p>
        </div>
        <button type="button" onClick={() => setRefreshKey((k) => k + 1)} style={refreshBtn} disabled={loading}>
          {loading ? '刷新中…' : '刷新'}
        </button>
      </header>

      {error && (
        <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>
          {error}
        </div>
      )}

      {loading && tasks.length === 0 ? (
        <Empty>加载中…</Empty>
      ) : tasks.length === 0 ? (
        <Empty>还没有媒体任务记录。绘图/视频/音频任务通过 API 提交后会在此列出。</Empty>
      ) : (
        <div style={{ background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', overflow: 'hidden' }}>
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['任务', '类型', '提供方', '状态', '进度', '计费', '提交时间'].map((h) => (
                    <th key={h} style={th}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {tasks.map((t) => (
                  <tr key={t.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={td}><code style={{ fontSize: 12 }}>#{t.id}</code></td>
                    <td style={td}>{taskTypeLabel(t.task_type)}</td>
                    <td style={td}>{t.provider || '—'}</td>
                    <td style={td}>
                      <StatusBadge tone={statusTone(t.status)}>{statusLabel(t.status)}</StatusBadge>
                      {t.status === 'failed' && t.error_class ? (
                        <span style={{ marginLeft: 6, fontSize: 11, color: 'var(--hk-ink-500)' }}>{t.error_class}</span>
                      ) : null}
                    </td>
                    <td style={td}>{isActive(t.status) ? <Progress value={t.progress} /> : t.status === 'succeeded' ? '100%' : '—'}</td>
                    <td style={tdNum}>{formatTaskCost(t)}</td>
                    <td style={td}>{new Date(t.created_at).toLocaleString()}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}

function Progress({ value }: { value: number }) {
  const pct = Math.max(0, Math.min(100, value))
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', minWidth: 90 }}>
      <div style={{ flex: 1, height: 6, background: 'var(--hk-surface-sunken)', borderRadius: 999, overflow: 'hidden' }}>
        <div style={{ width: `${pct}%`, height: '100%', background: 'var(--hk-primary-500)' }} />
      </div>
      <span style={{ fontSize: 11, color: 'var(--hk-ink-500)', fontFamily: 'var(--hk-font-mono)' }}>{pct}%</span>
    </div>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

const refreshBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
const tdNum: React.CSSProperties = { ...td, textAlign: 'right', fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
