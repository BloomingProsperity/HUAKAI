import { useState } from 'react'
import {
  getVideoTask,
  getVideoTaskByQuery,
  listVideoTasks,
  submitVideoTask,
} from './api'
import {
  buildVideoSubmitRequest,
  DEFAULT_VIDEO_FORM,
  filterMediaTasksByProvider,
  parseTaskID,
  parseTaskLimit,
} from './compatibility'
import {
  apiFailure,
  CompatibilityTaskPanel,
  ConsoleCard,
  Field,
  formGridStyle,
  imageFilesAsDataURLs,
  InlineNotice,
  inputStyle,
  textareaStyle,
  useCompatibilityTasks,
} from './CompatibilityTaskPanel'
import { newMediaTaskRequestID, statusLabel } from './mediatasks'
import type { CompatibilityQueryMode, CreateMediaTaskResponse, VideoSubmitForm } from './types'

export function VideoConsole() {
  const [form, setForm] = useState<VideoSubmitForm>({ ...DEFAULT_VIDEO_FORM })
  const [requestID, setRequestID] = useState(newMediaTaskRequestID)
  const [taskID, setTaskID] = useState('')
  const [queryMode, setQueryMode] = useState<CompatibilityQueryMode>('path')
  const [listLimit, setListLimit] = useState('50')
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const { tasks, upsert, replace } = useCompatibilityTasks(getVideoTask)

  const setFormValue = <K extends keyof VideoSubmitForm>(key: K, value: VideoSubmitForm[K]) => {
    setForm((current) => ({ ...current, [key]: value }))
  }

  const acceptTask = async (response: CreateMediaTaskResponse) => {
    setTaskID(String(response.task_id))
    setNotice(`视频任务 #${response.task_id} 已受理，当前状态：${statusLabel(response.status)}`)
    try {
      upsert(await getVideoTask(response.task_id))
    } catch {
      // 已受理时不因详情暂不可读而误报提交失败。
    }
  }

  const submit = async () => {
    setError(null)
    setNotice(null)
    let body
    try {
      body = buildVideoSubmitRequest(form, requestID)
    } catch (cause) {
      setError(apiFailure(cause, '视频参数无效'))
      return
    }
    setBusy('submit')
    try {
      const response = await submitVideoTask(body)
      await acceptTask(response)
      setRequestID(newMediaTaskRequestID())
    } catch (cause) {
      setError(apiFailure(cause, '视频任务提交失败'))
    } finally {
      setBusy(null)
    }
  }

  const query = async () => {
    setError(null)
    setNotice(null)
    let id
    try {
      id = parseTaskID(taskID)
    } catch (cause) {
      setError(apiFailure(cause, '任务 ID 无效'))
      return
    }
    setBusy('query')
    try {
      const task = await (queryMode === 'path' ? getVideoTask(id) : getVideoTaskByQuery(id))
      upsert(task)
      setNotice(`已通过${queryMode === 'path' ? '路径' : '查询参数'}端点读取视频任务 #${id}`)
    } catch (cause) {
      setError(apiFailure(cause, '视频任务查询失败'))
    } finally {
      setBusy(null)
    }
  }

  const list = async () => {
    setError(null)
    setNotice(null)
    let limit
    try {
      limit = parseTaskLimit(listLimit)
    } catch (cause) {
      setError(apiFailure(cause, '列表数量无效'))
      return
    }
    setBusy('list')
    try {
      const response = await listVideoTasks(limit)
      const returned = response.items ?? []
      const relevant = filterMediaTasksByProvider(returned, 'video')
      replace(relevant)
      setNotice(`列表端点返回 ${returned.length} 条任务，其中 ${relevant.length} 条属于视频兼容端`)
    } catch (cause) {
      setError(apiFailure(cause, '视频任务列表加载失败'))
    } finally {
      setBusy(null)
    }
  }

  const addImage = async (files: FileList | null) => {
    try {
      const urls = await imageFilesAsDataURLs(files)
      if (urls[0]) setFormValue('image', urls[0])
    } catch (cause) {
      setError(apiFailure(cause, '图片读取失败'))
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <InlineNotice>使用当前登录 session；提交会触发真实视频生成与平台计费，不需要粘贴 API Key。</InlineNotice>
      {error && <InlineNotice tone="error">{error}</InlineNotice>}
      {notice && <InlineNotice tone="ok">{notice}</InlineNotice>}

      <ConsoleCard title="提交视频任务" subtitle="POST /video/submit · 请求 JSON 原样进入异步媒体任务">
        <div style={formGridStyle}>
          <Field label="模型"><input value={form.model} placeholder="如 kling-v1 / sora-video" onChange={(event) => setFormValue('model', event.target.value)} style={inputStyle} /></Field>
          <Field label="时长"><input inputMode="decimal" value={form.duration} onChange={(event) => setFormValue('duration', event.target.value)} style={inputStyle} /></Field>
        </div>
        <Field label="Prompt"><textarea value={form.prompt} rows={5} onChange={(event) => setFormValue('prompt', event.target.value)} style={textareaStyle} /></Field>
        <Field label="参考图（image，可填 https URL 或 Base64）">
          <textarea value={form.image} rows={4} placeholder="https://… 或 data:image/png;base64,…" onChange={(event) => setFormValue('image', event.target.value)} style={{ ...textareaStyle, fontFamily: 'var(--hk-font-mono)' }} />
          <input type="file" accept="image/png,image/jpeg,image/webp,image/gif,image/avif" onChange={(event) => void addImage(event.target.files)} />
        </Field>
        <details>
          <summary style={{ cursor: 'pointer', color: 'var(--hk-ink-500)', fontSize: 12 }}>尺寸、帧率与输出参数</summary>
          <div style={{ ...formGridStyle, marginTop: 'var(--hk-space-3)' }}>
            <Field label="宽度"><input inputMode="numeric" value={form.width} onChange={(event) => setFormValue('width', event.target.value)} style={inputStyle} /></Field>
            <Field label="高度"><input inputMode="numeric" value={form.height} onChange={(event) => setFormValue('height', event.target.value)} style={inputStyle} /></Field>
            <Field label="FPS"><input inputMode="numeric" value={form.fps} onChange={(event) => setFormValue('fps', event.target.value)} style={inputStyle} /></Field>
            <Field label="种子"><input inputMode="numeric" value={form.seed} onChange={(event) => setFormValue('seed', event.target.value)} style={inputStyle} /></Field>
            <Field label="生成数量（n）"><input inputMode="numeric" value={form.count} onChange={(event) => setFormValue('count', event.target.value)} style={inputStyle} /></Field>
            <Field label="响应格式"><input value={form.responseFormat} placeholder="url / b64_json" onChange={(event) => setFormValue('responseFormat', event.target.value)} style={inputStyle} /></Field>
          </div>
        </details>
        <button type="button" className="hk-btn hk-btn--green" onClick={submit} disabled={busy !== null}>{busy === 'submit' ? '提交中…' : '提交视频任务'}</button>
      </ConsoleCard>

      <ConsoleCard title="查询与列出视频任务" subtitle="有 id 时返回单任务；GET /video/fetch 无 id 时按 limit 返回列表">
        <div style={{ display: 'flex', alignItems: 'flex-end', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
          <Field label="任务 ID" grow={1}><input inputMode="numeric" value={taskID} onChange={(event) => setTaskID(event.target.value)} style={inputStyle} /></Field>
          <div>
            <div style={{ color: 'var(--hk-ink-500)', fontSize: 12, marginBottom: 4 }}>单项端点</div>
            <div className="hk-seg" role="tablist" aria-label="视频查询端点">
              <button type="button" role="tab" aria-selected={queryMode === 'path'} className={queryMode === 'path' ? 'is-on' : undefined} onClick={() => setQueryMode('path')}>路径式</button>
              <button type="button" role="tab" aria-selected={queryMode === 'query'} className={queryMode === 'query' ? 'is-on' : undefined} onClick={() => setQueryMode('query')}>参数式</button>
            </div>
          </div>
          <button type="button" className="hk-btn" onClick={query} disabled={busy !== null}>{busy === 'query' ? '查询中…' : '查询任务'}</button>
        </div>
        <div style={{ display: 'flex', alignItems: 'flex-end', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
          <Field label="列表数量（1–200）" grow={1}><input inputMode="numeric" value={listLimit} onChange={(event) => setListLimit(event.target.value)} style={inputStyle} /></Field>
          <button type="button" className="hk-btn" onClick={list} disabled={busy !== null}>{busy === 'list' ? '加载中…' : '加载最近任务'}</button>
        </div>
      </ConsoleCard>

      <CompatibilityTaskPanel title="视频查询结果" tasks={tasks} preferredKind="video" />
    </div>
  )
}
