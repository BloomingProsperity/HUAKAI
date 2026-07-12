import { useState } from 'react'
import { listModels, sendChat, sendChatStream } from './api'
import { canSend, extractReply, formatUsage } from './playground'
import type { ChatResponse } from './types'

/*
 * Playground 在线调试台(BYO-key)。用户**粘自己的 API Key** + 选模型 + 输入 prompt,
 * 直调现有 relay /v1/chat/completions(非流式)看回复 + 用量。Key 仅在内存随请求发出,
 * 不持久化、不写日志(type=password)。⚠ 这是**真实上游调用,消耗该 Key 余额**——页内明示。
 * 走现有 relay + 计费,无新后端/auth/money 路径。真码:cmd/gateway/routes.go:92/115。
 */

export function PlaygroundPage() {
  const [apiKey, setApiKey] = useState('')
  const [model, setModel] = useState('')
  const [system, setSystem] = useState('')
  const [message, setMessage] = useState('')
  const [models, setModels] = useState<string[]>([])
  const [loadingModels, setLoadingModels] = useState(false)
  const [sending, setSending] = useState(false)
  const [streaming, setStreaming] = useState(true)
  const [reply, setReply] = useState<ChatResponse | null>(null)
  const [streamText, setStreamText] = useState('')
  const [error, setError] = useState<string | null>(null)

  async function loadModels() {
    if (apiKey.trim() === '') {
      setError('请先填入 API Key')
      return
    }
    setLoadingModels(true)
    setError(null)
    try {
      const res = await listModels(apiKey)
      setModels((res.data ?? []).map((m) => m.id).filter(Boolean))
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : '加载模型失败')
    } finally {
      setLoadingModels(false)
    }
  }

  async function send() {
    setSending(true)
    setError(null)
    setReply(null)
    setStreamText('')
    try {
      if (streaming) {
        await sendChatStream(apiKey, model, system, message, (t) => setStreamText((prev) => prev + t))
      } else {
        setReply(await sendChat(apiKey, model, system, message))
      }
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : '请求失败')
    } finally {
      setSending(false)
    }
  }

  const ready = canSend(apiKey, model, message) && !sending

  return (
    <div className="hk-page" style={{ maxWidth: 920 }}>
      <header className="hk-pagehead">
        <div>
          <h1>Playground 调试台</h1>
          <p className="hk-sub">
            用你自己的 API Key 在浏览器内直接调用模型,看回复与 token 用量。Key 只在本次请求中使用,不保存。
          </p>
        </div>
      </header>

      {/* 计费警示条:警告色为风险语义,刻意保留以提示会消耗真实余额 */}
      <div style={{ background: 'var(--hk-warn-soft)', border: '1px solid var(--hk-warn)', borderRadius: 'var(--hk-radius-sm)', padding: 'var(--hk-space-3)', fontSize: 12, color: 'var(--hk-ink-700)' }}>
        ⚠ 发送会发起<strong>真实上游调用</strong>,按该 Key 正常计费、消耗其余额——与你在外部用该 Key 调用一致。
      </div>

      <div style={{ display: 'flex', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
        <label style={col(2)}>
          <span style={lbl}>API Key</span>
          <input type="password" value={apiKey} placeholder="hk_..." autoComplete="off"
            onChange={(e) => setApiKey(e.target.value)} style={inp} />
        </label>
        <label style={col(1)}>
          <span style={lbl}>模型</span>
          <input list="pg-models" value={model} placeholder="如 gpt-4o"
            onChange={(e) => setModel(e.target.value)} style={inp} />
          <datalist id="pg-models">
            {models.map((m) => <option key={m} value={m} />)}
          </datalist>
        </label>
        <button type="button" onClick={loadModels} disabled={loadingModels}
          className="hk-btn" style={{ alignSelf: 'flex-end' }}>
          {loadingModels ? '加载中…' : '加载可用模型'}
        </button>
      </div>

      <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        <span style={lbl}>System(可选)</span>
        <textarea value={system} rows={2} onChange={(e) => setSystem(e.target.value)} style={{ ...inp, resize: 'vertical' }} />
      </label>

      <label style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        <span style={lbl}>消息</span>
        <textarea value={message} rows={5} placeholder="输入要发送的内容…"
          onChange={(e) => setMessage(e.target.value)} style={{ ...inp, resize: 'vertical' }} />
      </label>

      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)' }}>
        <button type="button" onClick={send} disabled={!ready}
          className="hk-btn hk-btn--green" style={{ opacity: ready ? 1 : 0.5, cursor: ready ? 'pointer' : 'default' }}>
          {sending ? '发送中…' : '发送'}
        </button>
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: 'var(--hk-ink-500)' }}>
          <input type="checkbox" checked={streaming} onChange={(e) => setStreaming(e.target.checked)} />
          流式逐字显示
        </label>
      </div>

      {error && (
        <p style={{ color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-sm)' }}>
          {error}
        </p>
      )}

      {streaming && (streamText || sending) && (
        <section className="hk-card">
          <div className="hk-card__head"><h3>回复{sending ? '(流式接收中…)' : ''}</h3></div>
          <div className="hk-card__body">
            <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 0, fontFamily: 'inherit', fontSize: 14 }}>
              {streamText || (sending ? '…' : '(空回复)')}
            </pre>
          </div>
        </section>
      )}

      {!streaming && reply && (
        <section className="hk-card">
          <div className="hk-card__head">
            <h3>回复{reply.model ? ` · ${reply.model}` : ''}{formatUsage(reply.usage) ? ` · ${formatUsage(reply.usage)}` : ''}</h3>
          </div>
          <div className="hk-card__body">
            <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word', margin: 0, fontFamily: 'inherit', fontSize: 14 }}>
              {extractReply(reply) || '(空回复)'}
            </pre>
          </div>
        </section>
      )}
    </div>
  )
}

const lbl: React.CSSProperties = { fontSize: 12, color: 'var(--hk-ink-500)' }
const inp: React.CSSProperties = { padding: '8px 10px', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, width: '100%' }
function col(grow: number): React.CSSProperties {
  return { display: 'flex', flexDirection: 'column', gap: 4, flex: grow, minWidth: 180 }
}
