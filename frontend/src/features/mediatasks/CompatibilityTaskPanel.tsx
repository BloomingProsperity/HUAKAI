import { useCallback, useEffect, useMemo, useState } from 'react'
import type { CSSProperties, ReactNode } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { extractMediaResources, formatMediaResult, mergeMediaTasks } from './compatibility'
import { formatTaskCost, isActive, pollMediaTaskUpdates, statusLabel, statusTone } from './mediatasks'
import type { MediaResource, MediaResourceKind, MediaTask } from './types'

type TaskLoader = (id: number, signal?: AbortSignal) => Promise<MediaTask>

export const COMPATIBILITY_POLL_INTERVAL_MS = 5000
const SAFE_IMAGE_MIME_TYPES = new Set(['image/png', 'image/jpeg', 'image/webp', 'image/gif', 'image/avif'])

export function useCompatibilityTasks(loadTask: TaskLoader) {
  const [tasks, setTasks] = useState<MediaTask[]>([])

  const upsert = useCallback((incoming: MediaTask | MediaTask[]) => {
    const rows = Array.isArray(incoming) ? incoming : [incoming]
    setTasks((current) => mergeMediaTasks(current, rows))
  }, [])

  const replace = useCallback((incoming: MediaTask[]) => setTasks(incoming), [])
  const activeKey = useMemo(
    () => tasks.filter((task) => isActive(task.status)).map((task) => task.id).join(','),
    [tasks],
  )

  useEffect(() => {
    if (!activeKey) return
    const ids = activeKey.split(',').map(Number)
    const controller = new AbortController()
    let inFlight = false
    const timer = window.setInterval(async () => {
      if (inFlight) return
      inFlight = true
      try {
        const updated = await pollMediaTaskUpdates(ids, (id) => loadTask(id, controller.signal))
        if (!controller.signal.aborted && updated.length > 0) {
          setTasks((current) => mergeMediaTasks(current, updated))
        }
      } finally {
        inFlight = false
      }
    }, COMPATIBILITY_POLL_INTERVAL_MS)
    return () => {
      controller.abort()
      window.clearInterval(timer)
    }
  }, [activeKey, loadTask])

  return { tasks, upsert, replace }
}

export function CompatibilityTaskPanel(props: {
  title: string
  tasks: MediaTask[]
  preferredKind: MediaResourceKind
  emptyText?: string
}) {
  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--hk-space-2)' }}>
        <h2 style={{ fontSize: 16 }}>{props.title}</h2>
        {props.tasks.some((task) => isActive(task.status)) && (
          <span style={{ fontSize: 12, color: 'var(--hk-ink-300)' }}>每 5 秒自动刷新进行中任务</span>
        )}
      </div>
      {props.tasks.length === 0 ? (
        <div className="hk-empty">{props.emptyText ?? '提交或查询任务后，结果会显示在这里。'}</div>
      ) : props.tasks.map((task) => (
        <TaskCard key={task.id} task={task} preferredKind={props.preferredKind} />
      ))}
    </section>
  )
}

export function ConsoleCard(props: { title: string; subtitle?: string; children: ReactNode }) {
  return (
    <section className="hk-card">
      <div className="hk-card__head">
        <div>
          <h3>{props.title}</h3>
          {props.subtitle && <p style={{ margin: 'var(--hk-space-1) 0 0', color: 'var(--hk-ink-300)', fontSize: 11 }}>{props.subtitle}</p>}
        </div>
      </div>
      <div className="hk-card__body" style={columnStyle}>{props.children}</div>
    </section>
  )
}

export function Field(props: { label: string; hint?: string; children: ReactNode; grow?: number }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0, flex: props.grow }}>
      <span style={{ color: 'var(--hk-ink-500)', fontSize: 12 }}>{props.label}</span>
      {props.children}
      {props.hint && <span style={{ color: 'var(--hk-ink-300)', fontSize: 11 }}>{props.hint}</span>}
    </label>
  )
}

export function InlineNotice(props: { tone?: 'ok' | 'error' | 'info'; children: ReactNode }) {
  const tone = props.tone ?? 'info'
  const color = tone === 'error' ? 'var(--hk-danger)' : tone === 'ok' ? 'var(--hk-primary-600)' : 'var(--hk-ink-500)'
  const background = tone === 'error' ? 'var(--hk-danger-soft)' : tone === 'ok' ? 'var(--hk-primary-50)' : 'var(--hk-canvas)'
  const border = tone === 'error' ? 'var(--hk-danger-soft)' : tone === 'ok' ? 'var(--hk-primary-100)' : 'var(--hk-line)'
  return <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 12, color, background, border: `1px solid ${border}` }}>{props.children}</div>
}

export function apiFailure(cause: unknown, fallback: string): string {
  return cause instanceof ApiError ? `${cause.message}（${cause.code}）` : cause instanceof Error ? cause.message : fallback
}

export async function imageFilesAsDataURLs(files: FileList | null): Promise<string[]> {
  if (!files || files.length === 0) return []
  return Promise.all(Array.from(files).map((file) => new Promise<string>((resolve, reject) => {
    if (!SAFE_IMAGE_MIME_TYPES.has(file.type.toLowerCase())) {
      reject(new Error(`${file.name} 不是受支持的栅格图片`))
      return
    }
    const reader = new FileReader()
    reader.onerror = () => reject(new Error(`${file.name} 读取失败`))
    reader.onload = () => typeof reader.result === 'string'
      ? resolve(reader.result)
      : reject(new Error(`${file.name} 读取失败`))
    reader.readAsDataURL(file)
  })))
}

function TaskCard({ task, preferredKind }: { task: MediaTask; preferredKind: MediaResourceKind }) {
  const resources = extractMediaResources(task.result, preferredKind)
  const progress = Math.max(0, Math.min(100, task.progress || 0))
  return (
    <article className="hk-card">
      <div className="hk-card__head">
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', flexWrap: 'wrap' }}>
          <h3>任务 #{task.id}</h3>
          <StatusBadge tone={statusTone(task.status)}>{statusLabel(task.status)}</StatusBadge>
          <span className="hk-mono" style={{ color: 'var(--hk-ink-300)', fontSize: 11 }}>{task.task_type}</span>
        </div>
        <span className="hk-mono" style={{ color: 'var(--hk-ink-500)', fontSize: 11 }}>{formatTaskCost(task)}</span>
      </div>
      <div className="hk-card__body" style={columnStyle}>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 'var(--hk-space-2)', fontSize: 11, color: 'var(--hk-ink-500)' }}>
          <span>提供方：{task.provider || '—'}</span>
          <span>上游任务：{task.provider_task_id || '待分配'}</span>
          <span className="hk-mono">request_id：{task.request_id || '—'}</span>
          <span>更新时间：{formatDate(task.updated_at)}</span>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
          <div className={task.status === 'failed' ? 'hk-bar hk-bar--warn' : 'hk-bar'} style={{ flex: 1 }}>
            <span style={{ width: `${progress}%` }} />
          </div>
          <span className="hk-mono" style={{ color: 'var(--hk-ink-500)', fontSize: 11 }}>{progress}%</span>
        </div>
        {task.error_class && <InlineNotice tone="error">错误分类：{task.error_class}</InlineNotice>}
        {resources.length > 0 && <MediaResources resources={resources} />}
        {task.result !== undefined && task.result !== null && (
          <details>
            <summary style={{ cursor: 'pointer', color: 'var(--hk-ink-500)', fontSize: 12 }}>结构化结果</summary>
            <pre style={jsonStyle}>{formatMediaResult(task.result)}</pre>
          </details>
        )}
      </div>
    </article>
  )
}

function MediaResources({ resources }: { resources: MediaResource[] }) {
  const images = resources.filter((item) => item.kind === 'image')
  const audio = resources.filter((item) => item.kind === 'audio')
  const videos = resources.filter((item) => item.kind === 'video')
  return (
    <div style={columnStyle}>
      {images.length > 0 && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(160px, 1fr))', gap: 'var(--hk-space-3)' }}>
          {images.map((resource, index) => (
            <a key={`${resource.src}-${index}`} href={resource.src} target="_blank" rel="noreferrer" style={{ color: 'var(--hk-ink-500)', fontSize: 11 }}>
              <img src={resource.src} alt={`生成图片 ${index + 1}`} loading="lazy" style={{ display: 'block', width: '100%', maxHeight: 360, objectFit: 'contain', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-canvas)' }} />
              <span>{resource.label}</span>
            </a>
          ))}
        </div>
      )}
      {audio.map((resource, index) => (
        <div key={`${resource.src}-${index}`}>
          <div style={{ marginBottom: 'var(--hk-space-1)', color: 'var(--hk-ink-300)', fontSize: 11 }}>{resource.label}</div>
          <audio controls preload="metadata" src={resource.src} style={{ width: '100%' }}>浏览器不支持音频播放。</audio>
        </div>
      ))}
      {videos.map((resource, index) => (
        <div key={`${resource.src}-${index}`}>
          <div style={{ marginBottom: 'var(--hk-space-1)', color: 'var(--hk-ink-300)', fontSize: 11 }}>{resource.label}</div>
          <video controls preload="metadata" src={resource.src} style={{ display: 'block', width: '100%', maxHeight: 520, borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-canvas)' }}>浏览器不支持视频播放。</video>
        </div>
      ))}
    </div>
  )
}

function formatDate(raw: string): string {
  const date = new Date(raw)
  return Number.isNaN(date.getTime()) ? raw || '—' : date.toLocaleString()
}

export const inputStyle: CSSProperties = {
  minHeight: 34,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
  width: '100%',
}

export const textareaStyle: CSSProperties = {
  ...inputStyle,
  minHeight: 92,
  height: 'auto',
  padding: 'var(--hk-space-2) var(--hk-space-3)',
  resize: 'vertical',
}

export const formGridStyle: CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))',
  gap: 'var(--hk-space-3)',
}

const columnStyle: CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-3)',
}

const jsonStyle: CSSProperties = {
  margin: 'var(--hk-space-2) 0 0',
  padding: 'var(--hk-space-3)',
  maxHeight: 320,
  overflow: 'auto',
  whiteSpace: 'pre-wrap',
  overflowWrap: 'anywhere',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-canvas)',
  color: 'var(--hk-ink-500)',
  fontFamily: 'var(--hk-font-mono)',
  fontSize: 11,
}
