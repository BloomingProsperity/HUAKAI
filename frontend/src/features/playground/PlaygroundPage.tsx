import { useEffect, useMemo, useRef, useState } from 'react'
import { ApiError } from '../../lib/api'
import {
  listModels,
  sendChat,
  sendChatStream,
  sendCompletion,
  sendEmbeddings,
  sendGemini,
  sendImageGeneration,
  sendMessages,
  sendMessagesStream,
  sendRerank,
  sendResponses,
  sendResponsesStream,
  sendSpeech,
} from './api'
import { ProtocolForms } from './ProtocolForms'
import {
  buildProtocolRequest,
  DEFAULT_PROTOCOL_FORM,
  extractImageSources,
  extractProtocolText,
  formatUsage,
  protocolFormError,
  sortedRerankResults,
  summarizeEmbeddings,
} from './playground'
import type {
  ChatUsage,
  ParsedSSEEvent,
  PlaygroundProtocol,
  ProtocolFormState,
  ProtocolRequestPlan,
} from './types'

const PROTOCOLS: Array<{ value: PlaygroundProtocol; label: string }> = [
  { value: 'chat', label: 'Chat' },
  { value: 'completions', label: 'Completions' },
  { value: 'messages', label: 'Claude Messages' },
  { value: 'responses', label: 'Responses' },
  { value: 'embeddings', label: 'Embeddings' },
  { value: 'rerank', label: 'Rerank' },
  { value: 'images', label: 'Images' },
  { value: 'speech', label: 'Speech' },
  { value: 'gemini', label: 'Gemini v1beta' },
]

export function PlaygroundPage() {
  const [protocol, setProtocol] = useState<PlaygroundProtocol>('chat')
  const [apiKey, setApiKey] = useState('')
  const [model, setModel] = useState('')
  const [models, setModels] = useState<string[]>([])
  const [form, setForm] = useState<ProtocolFormState>({ ...DEFAULT_PROTOCOL_FORM })
  const [streaming, setStreaming] = useState(true)
  const [loadingModels, setLoadingModels] = useState(false)
  const [sending, setSending] = useState(false)
  const [response, setResponse] = useState<unknown | null>(null)
  const [streamText, setStreamText] = useState('')
  const [streamEvents, setStreamEvents] = useState<unknown[]>([])
  const [streamEventCount, setStreamEventCount] = useState(0)
  const [audioURL, setAudioURL] = useState<string | null>(null)
  const [audioType, setAudioType] = useState('')
  const [error, setError] = useState<string | null>(null)
  const requestRef = useRef<AbortController | null>(null)
  const modelRequestRef = useRef<AbortController | null>(null)
  const audioURLRef = useRef<string | null>(null)

  useEffect(() => () => {
    const request = requestRef.current
    const modelRequest = modelRequestRef.current
    requestRef.current = null
    modelRequestRef.current = null
    request?.abort()
    modelRequest?.abort()
    if (audioURLRef.current) URL.revokeObjectURL(audioURLRef.current)
  }, [])

  const validationError = useMemo(
    () => protocolFormError(protocol, model, form, streaming),
    [protocol, model, form, streaming],
  )

  const setFormField = <K extends keyof ProtocolFormState>(key: K, value: ProtocolFormState[K]) => {
    setForm((current) => ({ ...current, [key]: value }))
  }

  const loadModels = async () => {
    if (!apiKey.trim()) {
      setError('请先填入 API Key')
      return
    }
    modelRequestRef.current?.abort()
    setLoadingModels(true)
    setError(null)
    const controller = new AbortController()
    modelRequestRef.current = controller
    try {
      const result = await listModels(apiKey, controller.signal)
      if (modelRequestRef.current !== controller) return
      const next = (result.data ?? []).map((item) => item.id).filter(Boolean)
      setModels(next)
      if (!model && next.length > 0) setModel(next[0])
    } catch (cause) {
      if (modelRequestRef.current === controller && !controller.signal.aborted) setError(formatError(cause, '加载模型失败'))
    } finally {
      if (modelRequestRef.current === controller) {
        modelRequestRef.current = null
        setLoadingModels(false)
      }
    }
  }

  const clearOutput = () => {
    setResponse(null)
    setStreamText('')
    setStreamEvents([])
    setStreamEventCount(0)
    if (audioURLRef.current) URL.revokeObjectURL(audioURLRef.current)
    audioURLRef.current = null
    setAudioURL(null)
    setAudioType('')
  }

  const chooseProtocol = (next: PlaygroundProtocol) => {
    const active = requestRef.current
    requestRef.current = null
    active?.abort()
    setSending(false)
    setProtocol(next)
    setError(null)
    clearOutput()
  }

  const onStreamEvent = (event: ParsedSSEEvent) => {
    if (event.content) setStreamText((current) => current + event.content)
    if (event.payload !== undefined) {
      setStreamEventCount((count) => count + 1)
      setStreamEvents((events) => [...events, event.payload].slice(-200))
    }
  }

  const send = async () => {
    let plan: ProtocolRequestPlan
    try {
      plan = buildProtocolRequest(protocol, model, form, streaming)
    } catch (cause) {
      setError(formatError(cause, '请求参数无效'))
      return
    }

    clearOutput()
    setSending(true)
    setError(null)
    const controller = new AbortController()
    requestRef.current = controller
    try {
      if (plan.stream) {
        await dispatchStream(apiKey, plan, (event) => {
          if (requestRef.current === controller) onStreamEvent(event)
        }, controller.signal)
      } else if (plan.protocol === 'speech') {
        const audio = await sendSpeech(apiKey, plan.body, controller.signal)
        if (requestRef.current !== controller) return
        const url = URL.createObjectURL(audio.blob)
        audioURLRef.current = url
        setAudioURL(url)
        setAudioType(audio.contentType)
      } else {
        const result = await dispatchJSON(apiKey, plan, controller.signal)
        if (requestRef.current === controller) setResponse(result)
      }
    } catch (cause) {
      if (requestRef.current === controller) {
        if (controller.signal.aborted) setError('请求已取消')
        else setError(formatError(cause, '请求失败'))
      }
    } finally {
      if (requestRef.current === controller) {
        requestRef.current = null
        setSending(false)
      }
    }
  }

  const ready = apiKey.trim() !== '' && validationError === null && !sending
  const receivingStream = streaming && (protocol === 'chat' || protocol === 'messages' || protocol === 'responses')

  return (
    <div className="hk-page" style={{ maxWidth: 1040 }}>
      <header className="hk-pagehead">
        <div>
          <h1>Playground 多协议调试台</h1>
          <p className="hk-sub">使用自己的 API Key 直调网关，核对请求、流式事件与原始响应；Key 仅停留在当前页面内存。</p>
        </div>
      </header>

      <div style={warningStyle}>
        ⚠ 发送会发起<strong>真实上游调用</strong>，按该 Key 正常计费并消耗余额。
      </div>

      <div className="hk-seg" role="tablist" aria-label="调试协议" style={{ alignSelf: 'flex-start', flexWrap: 'wrap' }}>
        {PROTOCOLS.map((item) => (
          <button
            key={item.value}
            type="button"
            role="tab"
            aria-selected={protocol === item.value}
            className={protocol === item.value ? 'is-on' : undefined}
            onClick={() => chooseProtocol(item.value)}
          >
            {item.label}
          </button>
        ))}
      </div>

      <section className="hk-card">
        <div className="hk-card__head"><h3>认证与模型</h3></div>
        <div className="hk-card__body" style={{ display: 'flex', gap: 'var(--hk-space-3)', flexWrap: 'wrap', alignItems: 'flex-end' }}>
          <Field label="API Key" grow={2}>
            <input
              type="password"
              value={apiKey}
              placeholder="hk_..."
              autoComplete="off"
              onChange={(event) => {
                const active = modelRequestRef.current
                modelRequestRef.current = null
                active?.abort()
                setLoadingModels(false)
                setModels([])
                setApiKey(event.target.value)
              }}
              style={inputStyle}
            />
          </Field>
          <Field label="模型" grow={1}>
            <input list="pg-models" value={model} placeholder="选择或输入模型" onChange={(event) => setModel(event.target.value)} style={inputStyle} />
            <datalist id="pg-models">{models.map((item) => <option key={item} value={item} />)}</datalist>
          </Field>
          <button type="button" onClick={loadModels} disabled={loadingModels} className="hk-btn">
            {loadingModels ? '加载中…' : '加载可用模型'}
          </button>
        </div>
      </section>

      <section className="hk-card">
        <div className="hk-card__head"><h3>{PROTOCOLS.find((item) => item.value === protocol)?.label} 请求</h3></div>
        <div className="hk-card__body">
          <ProtocolForms
            protocol={protocol}
            form={form}
            streaming={streaming}
            onChange={setFormField}
            onStreaming={setStreaming}
          />
        </div>
      </section>

      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
        <button type="button" onClick={send} disabled={!ready} className="hk-btn hk-btn--green" style={ready ? undefined : disabledStyle}>
          {sending ? '发送中…' : '发送请求'}
        </button>
        {sending && <button type="button" className="hk-btn" onClick={() => requestRef.current?.abort()}>取消</button>}
        {!sending && apiKey.trim() && validationError && <span style={{ fontSize: 12, color: 'var(--hk-ink-300)' }}>{validationError}</span>}
      </div>

      {error && <Notice>{error}</Notice>}

      {(streamText || streamEvents.length > 0 || (sending && receivingStream)) && (
        <StreamResponse text={streamText} events={streamEvents} totalEvents={streamEventCount} receiving={sending} />
      )}
      {response !== null && <JSONResponse protocol={protocol} response={response} />}
      {audioURL && (
        <section className="hk-card">
          <div className="hk-card__head"><h3>音频响应 · {audioType}</h3></div>
          <div className="hk-card__body"><audio controls src={audioURL} style={{ width: '100%' }}>浏览器不支持音频播放。</audio></div>
        </section>
      )}
    </div>
  )
}

async function dispatchJSON(apiKey: string, plan: ProtocolRequestPlan, signal: AbortSignal): Promise<unknown> {
  switch (plan.protocol) {
    case 'chat': return sendChat(apiKey, plan.body, signal)
    case 'completions': return sendCompletion(apiKey, plan.body, signal)
    case 'messages': return sendMessages(apiKey, plan.body, signal)
    case 'responses': return sendResponses(apiKey, plan.body, signal)
    case 'embeddings': return sendEmbeddings(apiKey, plan.body, signal)
    case 'rerank': return sendRerank(apiKey, plan.body, signal)
    case 'images': return sendImageGeneration(apiKey, plan.body, signal)
    case 'gemini': return sendGemini(apiKey, plan.path, plan.body, signal)
    case 'speech': throw new Error('音频请求必须使用二进制响应通道')
  }
}

async function dispatchStream(
  apiKey: string,
  plan: ProtocolRequestPlan,
  onEvent: (event: ParsedSSEEvent) => void,
  signal: AbortSignal,
): Promise<void> {
  switch (plan.protocol) {
    case 'chat': return sendChatStream(apiKey, plan.body, onEvent, signal)
    case 'messages': return sendMessagesStream(apiKey, plan.body, onEvent, signal)
    case 'responses': return sendResponsesStream(apiKey, plan.body, onEvent, signal)
    default: throw new Error('该协议不支持流式调试')
  }
}

function JSONResponse({ protocol, response }: { protocol: PlaygroundProtocol; response: unknown }) {
  const text = extractProtocolText(protocol, response)
  const embeddings = protocol === 'embeddings' ? summarizeEmbeddings(response) : []
  const reranked = protocol === 'rerank' ? sortedRerankResults(response) : []
  const images = protocol === 'images' ? extractImageSources(response) : []
  const usage = record(response)?.usage as ChatUsage | undefined
  return (
    <section className="hk-card">
      <div className="hk-card__head"><h3>响应{formatUsage(usage) ? ` · ${formatUsage(usage)}` : ''}</h3></div>
      <div className="hk-card__body" style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
        {text && <pre style={answerStyle}>{text}</pre>}
        {embeddings.length > 0 && (
          <div className="hk-tablewrap">
            <table className="hk-table"><thead><tr><th>Index</th><th>维度</th><th>前 8 个值</th></tr></thead><tbody>
              {embeddings.map((item) => <tr key={item.index}><td>{item.index}</td><td>{item.dimensions}</td><td className="hk-mono">{item.preview.join(', ')}</td></tr>)}
            </tbody></table>
          </div>
        )}
        {reranked.length > 0 && (
          <div className="hk-tablewrap">
            <table className="hk-table"><thead><tr><th>排序</th><th>原索引</th><th>分数</th><th>文档</th></tr></thead><tbody>
              {reranked.map((item, position) => <tr key={`${item.index}-${position}`}><td>{position + 1}</td><td>{item.index}</td><td className="hk-mono">{item.score}</td><td>{compactJSON(item.document)}</td></tr>)}
            </tbody></table>
          </div>
        )}
        {images.length > 0 && (
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: 'var(--hk-space-3)' }}>
            {images.map((source, index) => <img key={`${index}-${source.slice(0, 32)}`} src={source} alt={`生成结果 ${index + 1}`} loading="lazy" referrerPolicy="no-referrer" style={{ width: '100%', maxHeight: 480, objectFit: 'contain', borderRadius: 'var(--hk-radius-md)', border: '1px solid var(--hk-line)', background: 'var(--hk-canvas)' }} />)}
          </div>
        )}
        <details open={!text && embeddings.length === 0 && reranked.length === 0 && images.length === 0}>
          <summary style={summaryStyle}>原始 JSON</summary>
          <pre style={jsonStyle}>{prettyResponse(response)}</pre>
        </details>
      </div>
    </section>
  )
}

function StreamResponse({ text, events, totalEvents, receiving }: { text: string; events: unknown[]; totalEvents: number; receiving: boolean }) {
  return (
    <section className="hk-card">
      <div className="hk-card__head"><h3>流式响应{receiving ? ' · 接收中…' : ''}</h3></div>
      <div className="hk-card__body" style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
        <pre style={answerStyle}>{text || (receiving ? '…' : '（没有文本增量）')}</pre>
        <details>
          <summary style={summaryStyle}>原始 SSE 事件 · {totalEvents} 条{totalEvents > events.length ? `（仅保留最后 ${events.length} 条）` : ''}</summary>
          <pre style={jsonStyle}>{JSON.stringify(events, null, 2)}</pre>
        </details>
      </div>
    </section>
  )
}

function Field({ label, grow, children }: { label: string; grow: number; children: React.ReactNode }) {
  return <label style={{ display: 'flex', flexDirection: 'column', gap: 4, flex: grow, minWidth: 200, fontSize: 12, color: 'var(--hk-ink-500)' }}>{label}{children}</label>
}

function Notice({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{children}</div>
}

function formatError(cause: unknown, fallback: string): string {
  if (cause instanceof ApiError) return `${cause.message}（${cause.code}）`
  return cause instanceof Error ? cause.message : fallback
}

function record(value: unknown): Record<string, unknown> | null {
  return typeof value === 'object' && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : null
}

function compactJSON(value: unknown): string {
  if (typeof value === 'string') return value
  if (value === undefined) return '—'
  const text = JSON.stringify(value)
  return text.length > 180 ? `${text.slice(0, 177)}…` : text
}

function prettyResponse(value: unknown): string {
  return JSON.stringify(value, (key, item) => key === 'b64_json' && typeof item === 'string' ? `[base64 ${item.length} 字符]` : item, 2)
}

const inputStyle: React.CSSProperties = { minHeight: 34, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', fontSize: 13, width: '100%' }
const warningStyle: React.CSSProperties = { background: 'var(--hk-warn-soft)', border: '1px solid var(--hk-warn)', borderRadius: 'var(--hk-radius-sm)', padding: 'var(--hk-space-3)', fontSize: 12, color: 'var(--hk-ink-700)' }
const disabledStyle: React.CSSProperties = { opacity: 0.5, cursor: 'not-allowed' }
const answerStyle: React.CSSProperties = { whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 0, fontFamily: 'inherit', fontSize: 14, lineHeight: 1.6 }
const jsonStyle: React.CSSProperties = { whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 'var(--hk-space-2) 0 0', padding: 'var(--hk-space-3)', maxHeight: 440, overflow: 'auto', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-canvas)', color: 'var(--hk-ink-700)', fontFamily: 'var(--hk-font-mono)', fontSize: 12 }
const summaryStyle: React.CSSProperties = { cursor: 'pointer', color: 'var(--hk-ink-500)', fontSize: 12 }
