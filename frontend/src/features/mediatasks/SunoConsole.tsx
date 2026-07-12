import { useState } from 'react'
import {
  getSunoTask,
  getSunoTaskByQuery,
  submitSunoAction,
  submitSunoTask,
} from './api'
import {
  buildSunoActionRequest,
  buildSunoSubmitRequest,
  DEFAULT_SUNO_ACTION_FORM,
  DEFAULT_SUNO_FORM,
  parseTaskID,
} from './compatibility'
import {
  apiFailure,
  CompatibilityTaskPanel,
  ConsoleCard,
  Field,
  formGridStyle,
  InlineNotice,
  inputStyle,
  textareaStyle,
  useCompatibilityTasks,
} from './CompatibilityTaskPanel'
import { newMediaTaskRequestID, statusLabel } from './mediatasks'
import type {
  CompatibilityQueryMode,
  CreateMediaTaskResponse,
  SunoActionForm,
  SunoSubmitForm,
} from './types'

export function SunoConsole() {
  const [form, setForm] = useState<SunoSubmitForm>({ ...DEFAULT_SUNO_FORM })
  const [actionForm, setActionForm] = useState<SunoActionForm>({ ...DEFAULT_SUNO_ACTION_FORM })
  const [submitRequestID, setSubmitRequestID] = useState(newMediaTaskRequestID)
  const [actionRequestID, setActionRequestID] = useState(newMediaTaskRequestID)
  const [taskID, setTaskID] = useState('')
  const [queryMode, setQueryMode] = useState<CompatibilityQueryMode>('path')
  const [busy, setBusy] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const { tasks, upsert } = useCompatibilityTasks(getSunoTask)

  const setFormValue = <K extends keyof SunoSubmitForm>(key: K, value: SunoSubmitForm[K]) => {
    setForm((current) => ({ ...current, [key]: value }))
  }
  const setActionValue = <K extends keyof SunoActionForm>(key: K, value: SunoActionForm[K]) => {
    setActionForm((current) => ({ ...current, [key]: value }))
  }
  const setActionField = <K extends keyof SunoSubmitForm>(key: K, value: SunoSubmitForm[K]) => {
    setActionForm((current) => ({ ...current, [key]: value }))
  }

  const acceptTask = async (response: CreateMediaTaskResponse, label: string) => {
    setTaskID(String(response.task_id))
    setNotice(`${label} #${response.task_id} 已受理，当前状态：${statusLabel(response.status)}`)
    try {
      upsert(await getSunoTask(response.task_id))
    } catch {
      // 提交已成功时保留受理结果，详情可由下一轮查询恢复。
    }
  }

  const submit = async () => {
    setError(null)
    setNotice(null)
    let body
    try {
      body = buildSunoSubmitRequest(form, submitRequestID)
    } catch (cause) {
      setError(apiFailure(cause, 'Suno 参数无效'))
      return
    }
    setBusy('submit')
    try {
      const response = await submitSunoTask(body)
      await acceptTask(response, 'Suno 任务')
      setSubmitRequestID(newMediaTaskRequestID())
    } catch (cause) {
      setError(apiFailure(cause, 'Suno 提交失败'))
    } finally {
      setBusy(null)
    }
  }

  const submitAction = async () => {
    setError(null)
    setNotice(null)
    let request
    try {
      request = buildSunoActionRequest(actionForm, actionRequestID)
    } catch (cause) {
      setError(apiFailure(cause, 'Suno 动作参数无效'))
      return
    }
    setBusy('action')
    try {
      const response = await submitSunoAction(request.action, request.body)
      await acceptTask(response, `Suno 动作 ${request.action}`)
      setActionRequestID(newMediaTaskRequestID())
    } catch (cause) {
      setError(apiFailure(cause, 'Suno 动作提交失败'))
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
      const task = await (queryMode === 'path' ? getSunoTask(id) : getSunoTaskByQuery(id))
      upsert(task)
      setNotice(`已通过${queryMode === 'path' ? '路径' : '查询参数'}端点读取任务 #${id}`)
    } catch (cause) {
      setError(apiFailure(cause, 'Suno 任务查询失败'))
    } finally {
      setBusy(null)
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <InlineNotice>使用当前登录 session；提交会触发真实音乐生成与平台计费，不需要粘贴 API Key。</InlineNotice>
      {error && <InlineNotice tone="error">{error}</InlineNotice>}
      {notice && <InlineNotice tone="ok">{notice}</InlineNotice>}

      <div className="hk-grid hk-grid--2">
        <ConsoleCard title="提交 Suno 任务" subtitle="POST /suno/submit · 普通与 custom_mode 两种任务类型">
          <SunoFields form={form} onChange={setFormValue} />
          <button type="button" className="hk-btn hk-btn--green" onClick={submit} disabled={busy !== null}>{busy === 'submit' ? '提交中…' : '提交音乐任务'}</button>
        </ConsoleCard>

        <ConsoleCard title="Suno 动作端点" subtitle="POST /suno/submit/{action} · 后端接受字母、数字、-、_，没有固定枚举">
          <Field label="动作" hint="例如已部署 provider 支持的动作名；控制台不臆造后端未声明的动作清单。">
            <input value={actionForm.action} placeholder="动作名" onChange={(event) => setActionValue('action', event.target.value)} style={inputStyle} />
          </Field>
          <SunoFields form={actionForm} onChange={setActionField} actionMode />
          <button type="button" className="hk-btn hk-btn--green" onClick={submitAction} disabled={busy !== null}>{busy === 'action' ? '提交中…' : '提交动作任务'}</button>
        </ConsoleCard>
      </div>

      <ConsoleCard title="查询 Suno 任务" subtitle="同时支持 GET /suno/fetch/{id} 与 GET /suno/fetch?id={id}">
        <div style={{ display: 'flex', alignItems: 'flex-end', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
          <Field label="任务 ID" grow={1}><input inputMode="numeric" value={taskID} onChange={(event) => setTaskID(event.target.value)} style={inputStyle} /></Field>
          <div>
            <div style={{ color: 'var(--hk-ink-500)', fontSize: 12, marginBottom: 4 }}>端点形态</div>
            <div className="hk-seg" role="tablist" aria-label="Suno 查询端点">
              <button type="button" role="tab" aria-selected={queryMode === 'path'} className={queryMode === 'path' ? 'is-on' : undefined} onClick={() => setQueryMode('path')}>路径式</button>
              <button type="button" role="tab" aria-selected={queryMode === 'query'} className={queryMode === 'query' ? 'is-on' : undefined} onClick={() => setQueryMode('query')}>参数式</button>
            </div>
          </div>
          <button type="button" className="hk-btn" onClick={query} disabled={busy !== null}>{busy === 'query' ? '查询中…' : '查询任务'}</button>
        </div>
      </ConsoleCard>

      <CompatibilityTaskPanel title="Suno 查询结果" tasks={tasks} preferredKind="audio" />
    </div>
  )
}

function SunoFields(props: {
  form: SunoSubmitForm
  onChange: <K extends keyof SunoSubmitForm>(key: K, value: SunoSubmitForm[K]) => void
  actionMode?: boolean
}) {
  return (
    <>
      <Field label="歌词 / 生成描述" hint={props.actionMode ? '动作任务可改为只填写下方续写片段 ID。' : '普通模式发送 prompt，自定义模式发送 input。'}>
        <textarea value={props.form.lyrics} rows={6} onChange={(event) => props.onChange('lyrics', event.target.value)} style={textareaStyle} />
      </Field>
      <div style={formGridStyle}>
        <Field label="风格（tags）"><input value={props.form.style} onChange={(event) => props.onChange('style', event.target.value)} style={inputStyle} /></Field>
        <Field label="标题"><input value={props.form.title} onChange={(event) => props.onChange('title', event.target.value)} style={inputStyle} /></Field>
      </div>
      <div style={{ display: 'flex', gap: 'var(--hk-space-4)', flexWrap: 'wrap' }}>
        <label style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--hk-space-2)', color: 'var(--hk-ink-500)', fontSize: 12 }}>
          <input type="checkbox" checked={props.form.instrumental} onChange={(event) => props.onChange('instrumental', event.target.checked)} />
          纯器乐（make_instrumental）
        </label>
        <label style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--hk-space-2)', color: 'var(--hk-ink-500)', fontSize: 12 }}>
          <input type="checkbox" checked={props.form.customMode} onChange={(event) => props.onChange('customMode', event.target.checked)} />
          自定义模式（custom_mode）
        </label>
      </div>
      <details>
        <summary style={{ cursor: 'pointer', color: 'var(--hk-ink-500)', fontSize: 12 }}>模型、描述与续写字段</summary>
        <div style={{ ...formGridStyle, marginTop: 'var(--hk-space-3)' }}>
          <Field label="GPT 描述"><input value={props.form.description} onChange={(event) => props.onChange('description', event.target.value)} style={inputStyle} /></Field>
          <Field label="notify_hook"><input value={props.form.notifyHook} placeholder="https://…" onChange={(event) => props.onChange('notifyHook', event.target.value)} style={inputStyle} /></Field>
          <Field label="mv"><input value={props.form.mv} onChange={(event) => props.onChange('mv', event.target.value)} style={inputStyle} /></Field>
          <Field label="model_version"><input value={props.form.modelVersion} onChange={(event) => props.onChange('modelVersion', event.target.value)} style={inputStyle} /></Field>
          <Field label="continue_clip_id"><input value={props.form.continueClipID} onChange={(event) => props.onChange('continueClipID', event.target.value)} style={inputStyle} /></Field>
          <Field label="continue_at"><input inputMode="decimal" value={props.form.continueAt} onChange={(event) => props.onChange('continueAt', event.target.value)} style={inputStyle} /></Field>
        </div>
      </details>
    </>
  )
}
