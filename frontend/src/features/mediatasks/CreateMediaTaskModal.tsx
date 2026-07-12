import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { createMediaTask } from './api'
import { buildMediaTaskRequest, DEFAULT_MEDIA_TASK_FORM, newMediaTaskRequestID } from './mediatasks'
import type { CreateMediaTaskResponse, MediaTaskCreateForm, MediaTaskKind } from './types'

const KINDS: Array<{ value: MediaTaskKind; label: string }> = [
  { value: 'image', label: '图片' },
  { value: 'music', label: '音乐' },
  { value: 'video', label: '视频' },
]

export function CreateMediaTaskModal(props: {
  onClose: () => void
  onCreated: (response: CreateMediaTaskResponse) => void
}) {
  const [form, setForm] = useState<MediaTaskCreateForm>({ ...DEFAULT_MEDIA_TASK_FORM })
  const [requestID] = useState(newMediaTaskRequestID)
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const set = <K extends keyof MediaTaskCreateForm>(key: K, value: MediaTaskCreateForm[K]) => {
    setForm((current) => ({ ...current, [key]: value }))
  }

  const submit = async () => {
    let body
    try {
      body = buildMediaTaskRequest(form, requestID)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : '任务参数无效')
      return
    }
    setSubmitting(true)
    setError(null)
    try {
      const response = await createMediaTask(body)
      props.onCreated(response)
      props.onClose()
    } catch (cause) {
      setError(cause instanceof ApiError ? `${cause.message}（${cause.code}）` : '创建媒体任务失败')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div
      role="presentation"
      onClick={submitting ? undefined : props.onClose}
      style={overlayStyle}
    >
      <div role="dialog" aria-modal="true" aria-labelledby="create-media-task-title" style={panelStyle} onClick={(event) => event.stopPropagation()}>
        <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <div>
            <h2 id="create-media-task-title" style={{ fontSize: 18 }}>新建媒体任务</h2>
            <p style={{ margin: 'var(--hk-space-1) 0 0', fontSize: 12, color: 'var(--hk-ink-300)' }}>创建后进入异步队列，可在列表中查看进度。</p>
          </div>
          <button type="button" onClick={props.onClose} disabled={submitting} style={iconButtonStyle} aria-label="关闭">✕</button>
        </header>

        <div style={warningStyle}>任务会发起真实媒体生成并按平台配置预扣、结算费用。</div>

        <Group label="任务类型">
          <div className="hk-seg" role="tablist" aria-label="媒体任务类型">
            {KINDS.map((kind) => (
              <button key={kind.value} type="button" role="tab" aria-selected={form.taskKind === kind.value} className={form.taskKind === kind.value ? 'is-on' : undefined} onClick={() => set('taskKind', kind.value)}>
                {kind.label}
              </button>
            ))}
          </div>
        </Group>
        <Field label="模型">
          <input value={form.model} placeholder="如 gpt-image-1 / suno-v4 / veo-3" onChange={(event) => set('model', event.target.value)} style={inputStyle} />
        </Field>
        <Field label="Prompt">
          <textarea value={form.prompt} rows={6} placeholder="描述要创建的媒体内容…" onChange={(event) => set('prompt', event.target.value)} style={{ ...inputStyle, height: 'auto', padding: 'var(--hk-space-2) var(--hk-space-3)', resize: 'vertical' }} />
        </Field>

        <details style={{ border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', padding: 'var(--hk-space-3)' }}>
          <summary style={{ cursor: 'pointer', color: 'var(--hk-ink-500)', fontSize: 12 }}>高级参数 JSON</summary>
          <textarea
            value={form.parametersJSON}
            rows={8}
            aria-label="高级参数 JSON"
            placeholder={'{\n  "duration": 5,\n  "aspect_ratio": "16:9"\n}'}
            onChange={(event) => set('parametersJSON', event.target.value)}
            style={{ ...inputStyle, height: 'auto', marginTop: 'var(--hk-space-2)', padding: 'var(--hk-space-2) var(--hk-space-3)', resize: 'vertical', fontFamily: 'var(--hk-font-mono)' }}
          />
          <p style={{ margin: 'var(--hk-space-2) 0 0', fontSize: 11, color: 'var(--hk-ink-300)' }}>只填写额外参数；model 与 prompt 由上方固定字段提供。</p>
        </details>

        {error && <div style={errorStyle}>{error}</div>}

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--hk-space-2)' }}>
          <button type="button" onClick={props.onClose} disabled={submitting} className="hk-btn">取消</button>
          <button type="button" onClick={submit} disabled={submitting} className="hk-btn hk-btn--green">{submitting ? '创建中…' : '创建任务'}</button>
        </div>
      </div>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>{label}{children}</label>
}

function Group({ label, children }: { label: string; children: React.ReactNode }) {
  return <div style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}><span>{label}</span>{children}</div>
}

const overlayStyle: React.CSSProperties = { position: 'fixed', inset: 0, background: 'rgba(28,38,34,0.4)', display: 'flex', alignItems: 'flex-start', justifyContent: 'center', padding: 'var(--hk-space-6)', zIndex: 'var(--hk-z-overlay)' as unknown as number, overflowY: 'auto' }
const panelStyle: React.CSSProperties = { width: 'min(600px, 100%)', background: 'var(--hk-surface)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-3)', padding: 'var(--hk-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }
const inputStyle: React.CSSProperties = { minHeight: 34, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const iconButtonStyle: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-ink-500)', fontSize: 16, cursor: 'pointer' }
const warningStyle: React.CSSProperties = { padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 12, color: 'var(--hk-warn)', background: 'var(--hk-warn-soft)', border: '1px solid var(--hk-warn-soft)' }
const errorStyle: React.CSSProperties = { padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
