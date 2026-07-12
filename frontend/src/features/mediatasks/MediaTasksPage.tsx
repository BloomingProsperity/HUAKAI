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
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>媒体任务记录</h1>
          <p className="hk-sub">
            绘图 / 视频 / 音频异步任务的历史与状态{activeCount > 0 ? ` · ${activeCount} 个进行中` : ''}。
          </p>
        </div>
        <button type="button" onClick={() => setRefreshKey((k) => k + 1)} className="hk-btn" disabled={loading}>
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
        <div className="hk-card">
          <div className="hk-tablewrap">
            <table className="hk-table">
              <thead>
                <tr>
                  {['任务', '类型', '提供方', '状态', '进度', '计费', '提交时间'].map((h) => (
                    <th key={h}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {tasks.map((t) => (
                  <tr key={t.id}>
                    <td className="hk-mono">#{t.id}</td>
                    <td>{taskTypeLabel(t.task_type)}</td>
                    <td>{t.provider || '—'}</td>
                    <td>
                      <StatusBadge tone={statusTone(t.status)}>{statusLabel(t.status)}</StatusBadge>
                      {t.status === 'failed' && t.error_class ? (
                        <span style={{ marginLeft: 6, fontSize: 11, color: 'var(--hk-ink-500)' }}>{t.error_class}</span>
                      ) : null}
                    </td>
                    <td>{isActive(t.status) ? <Progress value={t.progress} /> : t.status === 'succeeded' ? '100%' : '—'}</td>
                    <td className="hk-mono">{formatTaskCost(t)}</td>
                    <td className="hk-mono">{new Date(t.created_at).toLocaleString()}</td>
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
      <div className="hk-bar" style={{ flex: 1 }}>
        <span style={{ width: `${pct}%` }} />
      </div>
      <span style={{ fontSize: 11, color: 'var(--hk-ink-500)', fontFamily: 'var(--hk-font-mono)' }}>{pct}%</span>
    </div>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div className="hk-empty">{children}</div>
}
