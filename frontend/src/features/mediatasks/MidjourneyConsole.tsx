import { useState } from 'react'
import {
  getMidjourneyImageSeed,
  getMidjourneyTask,
  listMidjourneyTasks,
  submitMidjourneySwap,
  submitMidjourneyTask,
} from './api'
import {
  buildMidjourneySubmitRequest,
  buildMidjourneySwapRequest,
  DEFAULT_MIDJOURNEY_FORM,
  DEFAULT_MIDJOURNEY_SWAP_FORM,
  filterMediaTasksByProvider,
  MIDJOURNEY_ACTIONS,
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
import type { CreateMediaTaskResponse, MidjourneySubmitForm, MidjourneySwapForm } from './types'

export function MidjourneyConsole() {
  const [form, setForm] = useState<MidjourneySubmitForm>({ ...DEFAULT_MIDJOURNEY_FORM })
  const [swap, setSwap] = useState<MidjourneySwapForm>({ ...DEFAULT_MIDJOURNEY_SWAP_FORM })
  const [submitRequestID, setSubmitRequestID] = useState(newMediaTaskRequestID)
  const [swapRequestID, setSwapRequestID] = useState(newMediaTaskRequestID)
  const [taskID, setTaskID] = useState('')
  const [listLimit, setListLimit] = useState('50')
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const { tasks, upsert, replace } = useCompatibilityTasks(getMidjourneyTask)

  const setFormValue = <K extends keyof MidjourneySubmitForm>(key: K, value: MidjourneySubmitForm[K]) => {
    setForm((current) => ({ ...current, [key]: value }))
  }
  const setSwapValue = <K extends keyof MidjourneySwapForm>(key: K, value: MidjourneySwapForm[K]) => {
    setSwap((current) => ({ ...current, [key]: value }))
  }

  const acceptTask = async (response: CreateMediaTaskResponse, label: string) => {
    setTaskID(String(response.task_id))
    setNotice(`${label} #${response.task_id} 已受理，当前状态：${statusLabel(response.status)}`)
    try {
      upsert(await getMidjourneyTask(response.task_id))
    } catch {
      // 已受理时详情暂不可读不应误报提交失败，轮询或手动查询可恢复。
    }
  }

  const submit = async () => {
    setError(null)
    setNotice(null)
    let request
    try {
      request = buildMidjourneySubmitRequest(form, submitRequestID)
    } catch (cause) {
      setError(apiFailure(cause, 'Midjourney 参数无效'))
      return
    }
    setBusy('submit')
    try {
      const response = await submitMidjourneyTask(request.action, request.body)
      await acceptTask(response, 'Midjourney 任务')
      setSubmitRequestID(newMediaTaskRequestID())
    } catch (cause) {
      setError(apiFailure(cause, 'Midjourney 提交失败'))
    } finally {
      setBusy(null)
    }
  }

  const submitSwap = async () => {
    setError(null)
    setNotice(null)
    let body
    try {
      body = buildMidjourneySwapRequest(swap, swapRequestID)
    } catch (cause) {
      setError(apiFailure(cause, '换脸参数无效'))
      return
    }
    setBusy('swap')
    try {
      const response = await submitMidjourneySwap(body)
      await acceptTask(response, '换脸任务')
      setSwapRequestID(newMediaTaskRequestID())
    } catch (cause) {
      setError(apiFailure(cause, '换脸任务提交失败'))
    } finally {
      setBusy(null)
    }
  }

  const fetchTask = async (seed: boolean) => {
    setError(null)
    setNotice(null)
    let id
    try {
      id = parseTaskID(taskID)
    } catch (cause) {
      setError(apiFailure(cause, '任务 ID 无效'))
      return
    }
    setBusy(seed ? 'seed' : 'fetch')
    try {
      const task = await (seed ? getMidjourneyImageSeed(id) : getMidjourneyTask(id))
      upsert(task)
      setNotice(seed ? `已通过 image-seed 端点读取任务 #${id}` : `已刷新任务 #${id}`)
    } catch (cause) {
      setError(apiFailure(cause, seed ? '种子查询失败' : '任务查询失败'))
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
      const response = await listMidjourneyTasks(limit)
      const returned = response.items ?? []
      const relevant = filterMediaTasksByProvider(returned, 'midjourney')
      replace(relevant)
      setNotice(`条件端点返回 ${returned.length} 条任务，其中 ${relevant.length} 条属于 Midjourney`)
    } catch (cause) {
      setError(apiFailure(cause, '条件列表加载失败'))
    } finally {
      setBusy(null)
    }
  }

  const addImages = async (files: FileList | null, key: 'base64Images' | 'maskBase64') => {
    try {
      const urls = await imageFilesAsDataURLs(files)
      if (key === 'base64Images') {
        setFormValue(key, [form.base64Images.trim(), ...urls].filter(Boolean).join('\n'))
      } else if (urls[0]) {
        setFormValue(key, urls[0])
      }
    } catch (cause) {
      setError(apiFailure(cause, '图片读取失败'))
    }
  }

  const addSwapImage = async (files: FileList | null, key: keyof MidjourneySwapForm) => {
    try {
      const urls = await imageFilesAsDataURLs(files)
      if (urls[0]) setSwapValue(key, urls[0])
    } catch (cause) {
      setError(apiFailure(cause, '图片读取失败'))
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <InlineNotice>使用当前登录 session；提交会触发真实生成与平台计费，不需要粘贴 API Key。</InlineNotice>
      {error && <InlineNotice tone="error">{error}</InlineNotice>}
      {notice && <InlineNotice tone="ok">{notice}</InlineNotice>}

      <div className="hk-grid hk-grid--2">
        <ConsoleCard title="提交 Midjourney 任务" subtitle="POST /mj/submit/{action} · 11 个后端白名单动作">
          <div style={formGridStyle}>
            <Field label="动作">
              <select value={form.action} onChange={(event) => setFormValue('action', event.target.value as MidjourneySubmitForm['action'])} style={inputStyle}>
                {MIDJOURNEY_ACTIONS.map((action) => <option key={action} value={action}>{action}</option>)}
              </select>
            </Field>
            <Field label="模式（botType）">
              <input list="mj-bot-types" value={form.botType} onChange={(event) => setFormValue('botType', event.target.value)} style={inputStyle} />
              <datalist id="mj-bot-types"><option value="MID_JOURNEY" /><option value="NIJI_JOURNEY" /></datalist>
            </Field>
          </div>
          <Field label="Prompt" hint="imagine 必填；describe / blend / upload-discord-images 以图片为主。">
            <textarea value={form.prompt} rows={4} onChange={(event) => setFormValue('prompt', event.target.value)} style={textareaStyle} />
          </Field>
          <Field label="Base64 图片（base64Array，每行一张）">
            <textarea value={form.base64Images} rows={5} placeholder="data:image/png;base64,…" onChange={(event) => setFormValue('base64Images', event.target.value)} style={{ ...textareaStyle, fontFamily: 'var(--hk-font-mono)' }} />
            <input type="file" accept="image/png,image/jpeg,image/webp,image/gif,image/avif" multiple onChange={(event) => void addImages(event.target.files, 'base64Images')} />
          </Field>
          <details>
            <summary style={{ cursor: 'pointer', color: 'var(--hk-ink-500)', fontSize: 12 }}>动作兼容字段</summary>
            <div style={{ ...formGridStyle, marginTop: 'var(--hk-space-3)' }}>
              <Field label="customId"><input value={form.customID} onChange={(event) => setFormValue('customID', event.target.value)} style={inputStyle} /></Field>
              <Field label="notifyHook"><input value={form.notifyHook} placeholder="https://…" onChange={(event) => setFormValue('notifyHook', event.target.value)} style={inputStyle} /></Field>
              <Field label="action（body）"><input value={form.commandAction} onChange={(event) => setFormValue('commandAction', event.target.value)} style={inputStyle} /></Field>
              <Field label="state"><input value={form.state} onChange={(event) => setFormValue('state', event.target.value)} style={inputStyle} /></Field>
              <Field label="index JSON"><input value={form.indexJSON} placeholder="1 或 {&quot;slot&quot;:2}" onChange={(event) => setFormValue('indexJSON', event.target.value)} style={inputStyle} /></Field>
            </div>
            <Field label="maskBase64">
              <textarea value={form.maskBase64} rows={3} onChange={(event) => setFormValue('maskBase64', event.target.value)} style={{ ...textareaStyle, fontFamily: 'var(--hk-font-mono)' }} />
              <input type="file" accept="image/png,image/jpeg,image/webp,image/gif,image/avif" onChange={(event) => void addImages(event.target.files, 'maskBase64')} />
            </Field>
          </details>
          <button type="button" className="hk-btn hk-btn--green" onClick={submit} disabled={busy !== null}>{busy === 'submit' ? '提交中…' : '提交任务'}</button>
        </ConsoleCard>

        <ConsoleCard title="InsightFace 换脸" subtitle="POST /mj/insight-face/swap · 双图均为 Base64">
          <Field label="源图片（sourceBase64）">
            <textarea value={swap.sourceBase64} rows={5} onChange={(event) => setSwapValue('sourceBase64', event.target.value)} style={{ ...textareaStyle, fontFamily: 'var(--hk-font-mono)' }} />
            <input type="file" accept="image/png,image/jpeg,image/webp,image/gif,image/avif" onChange={(event) => void addSwapImage(event.target.files, 'sourceBase64')} />
          </Field>
          <Field label="目标图片（targetBase64）">
            <textarea value={swap.targetBase64} rows={5} onChange={(event) => setSwapValue('targetBase64', event.target.value)} style={{ ...textareaStyle, fontFamily: 'var(--hk-font-mono)' }} />
            <input type="file" accept="image/png,image/jpeg,image/webp,image/gif,image/avif" onChange={(event) => void addSwapImage(event.target.files, 'targetBase64')} />
          </Field>
          <button type="button" className="hk-btn hk-btn--green" onClick={submitSwap} disabled={busy !== null}>{busy === 'swap' ? '提交中…' : '提交换脸任务'}</button>
        </ConsoleCard>
      </div>

      <ConsoleCard title="任务、种子与条件列表" subtitle="fetch 与 image-seed 当前都返回完整媒体任务；条件列表 body 只读取 limit">
        <div style={{ ...formGridStyle, alignItems: 'end' }}>
          <Field label="任务 ID"><input inputMode="numeric" value={taskID} onChange={(event) => setTaskID(event.target.value)} style={inputStyle} /></Field>
          <button type="button" className="hk-btn" onClick={() => void fetchTask(false)} disabled={busy !== null}>{busy === 'fetch' ? '查询中…' : '查询状态'}</button>
          <button type="button" className="hk-btn" onClick={() => void fetchTask(true)} disabled={busy !== null}>{busy === 'seed' ? '查询中…' : '查询种子'}</button>
          <Field label="列表数量（1–200）"><input inputMode="numeric" value={listLimit} onChange={(event) => setListLimit(event.target.value)} style={inputStyle} /></Field>
          <button type="button" className="hk-btn" onClick={list} disabled={busy !== null}>{busy === 'list' ? '加载中…' : '加载条件列表'}</button>
        </div>
      </ConsoleCard>

      <CompatibilityTaskPanel title="Midjourney 查询结果" tasks={tasks} preferredKind="image" />
    </div>
  )
}
