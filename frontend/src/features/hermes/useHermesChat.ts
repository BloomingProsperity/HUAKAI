/*
 * Hermes 对话状态 hook。管理消息列表、conversation_id、流式累加与发送。
 * 纯只读:只发 chat(流式)+ 渲染文本,绝不触碰任何改动型 / 提议 / 确认逻辑。
 *
 * 发送时把"当前页上下文前缀"注入到发给后端的 content,但消息列表里只保存用户真正输入的文本
 * (displayText),保证 UI 不回显前缀。流式 token 累加到最后一条 assistant 消息。
 */

import { useCallback, useRef, useState } from 'react'
import { streamChat, type SSEHandlers } from '../../lib/hermesStream'
import { composeUserContent } from './hermesContext'

/** 面板内一条对话消息(仅渲染所需字段)。assistant 的 streaming 标记用于显示"正在输入"。 */
export interface PanelMessage {
  role: 'user' | 'assistant'
  /** 渲染文本。user=用户原始输入(不含上下文前缀);assistant=累加的流式文本。 */
  text: string
  /** assistant 是否仍在流式接收中。 */
  streaming?: boolean
}

/** sendArgs:一次发送所需的全部参数。contextPrefix 由调用方按当前页算好传入。 */
export interface SendArgs {
  adminToken: string
  asUserId: number
  tenantId?: number
  /** 用户在输入框真正键入的文本(UI 展示用)。 */
  userInput: string
  /** 当前页上下文前缀(注入后端 content,不展示)。 */
  contextPrefix: string
}

export interface HermesChatState {
  messages: PanelMessage[]
  conversationId: number | null
  sending: boolean
  error: { code: string; message: string } | null
  send: (args: SendArgs) => Promise<void>
  reset: () => void
  /** 取消进行中的流式请求(关闭面板/重置时调用)。 */
  abort: () => void
}

export function useHermesChat(): HermesChatState {
  const [messages, setMessages] = useState<PanelMessage[]>([])
  const [conversationId, setConversationId] = useState<number | null>(null)
  const [sending, setSending] = useState(false)
  const [error, setError] = useState<{ code: string; message: string } | null>(null)
  const abortRef = useRef<AbortController | null>(null)
  // 用 ref 持有最新 conversationId,避免闭包捕获旧值(连续多轮时仍带对的会话 ID)。
  const convIdRef = useRef<number | null>(null)

  const abort = useCallback(() => {
    abortRef.current?.abort()
    abortRef.current = null
  }, [])

  const reset = useCallback(() => {
    abort()
    setMessages([])
    setConversationId(null)
    convIdRef.current = null
    setError(null)
    setSending(false)
  }, [abort])

  const send = useCallback(async (args: SendArgs) => {
    const trimmed = args.userInput.trim()
    if (trimmed === '') return
    setError(null)
    setSending(true)

    // 追加用户消息(展示原始输入)+ 一条空的 assistant 占位(流式累加目标)。
    setMessages((prev) => [
      ...prev,
      { role: 'user', text: trimmed },
      { role: 'assistant', text: '', streaming: true },
    ])

    const controller = new AbortController()
    abortRef.current = controller

    // 发给后端的 content = 上下文前缀 + 用户输入(UI 不展示前缀)。
    const content = composeUserContent(args.contextPrefix, trimmed)

    // 把流式增量累加到最后一条 assistant 消息。
    const appendDelta = (delta: string) => {
      setMessages((prev) => {
        const next = prev.slice()
        for (let i = next.length - 1; i >= 0; i--) {
          if (next[i].role === 'assistant') {
            next[i] = { ...next[i], text: next[i].text + delta }
            break
          }
        }
        return next
      })
    }

    const finishStreaming = () => {
      setMessages((prev) => {
        const next = prev.slice()
        for (let i = next.length - 1; i >= 0; i--) {
          if (next[i].role === 'assistant') {
            next[i] = { ...next[i], streaming: false }
            break
          }
        }
        return next
      })
    }

    const handlers: SSEHandlers = {
      onConversation: (id) => {
        convIdRef.current = id
        setConversationId(id)
      },
      onToken: (delta) => appendDelta(delta),
      onDone: () => finishStreaming(),
      onError: (code, message) => {
        setError({ code, message })
        finishStreaming()
      },
    }

    try {
      await streamChat({
        adminToken: args.adminToken,
        asUserId: args.asUserId,
        tenantId: args.tenantId,
        messages: [{ role: 'user', content }],
        conversationId: convIdRef.current,
        handlers,
        signal: controller.signal,
      })
    } catch (e) {
      // AbortError(主动取消)不算错误;其余异常转成可展示错误。
      if (!(e instanceof DOMException && e.name === 'AbortError')) {
        const message = e instanceof Error ? e.message : '流式请求失败'
        setError({ code: 'stream_failed', message })
      }
      finishStreaming()
    } finally {
      setSending(false)
      abortRef.current = null
    }
  }, [])

  return { messages, conversationId, sending, error, send, reset, abort }
}
