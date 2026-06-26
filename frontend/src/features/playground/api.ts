import { ApiError, apiGet, apiSend } from '../../lib/api'
import { buildChatRequest, extractSSEContent } from './playground'
import type { ChatResponse, ModelListResponse } from './types'

/*
 * Playground 数据访问层。**BYO-key**:用用户显式传入的 API Key 作 Bearer 直调 relay,
 * 不依赖 session token、不持久化 Key(只在内存随请求发出)。
 * 真码:cmd/gateway/routes.go:92(/v1/chat/completions)、:115(/v1/models),
 *       均经 handler 内 inboundAuth 解析 hk_key Bearer → 走现有计费(消耗该 Key 余额)。
 */

/** listModels 用该 Key 拉可用模型(OpenAI {data:[{id}]} 形态)。 */
export async function listModels(apiKey: string, signal?: AbortSignal): Promise<ModelListResponse> {
  return apiGet<ModelListResponse>('/v1/models', { bearer: apiKey.trim(), signal })
}

/**
 * sendChat 用该 Key 发非流式 chat 请求。⚠ 这是**真实上游调用**,会消耗该 Key 的余额。
 * Key 仅作本次请求的 Bearer,不落库、不写日志。
 */
export async function sendChat(
  apiKey: string,
  model: string,
  system: string,
  message: string,
  signal?: AbortSignal,
): Promise<ChatResponse> {
  return apiSend<ChatResponse>('POST', '/v1/chat/completions', buildChatRequest(model, system, message), {
    bearer: apiKey.trim(),
    signal,
  })
}

/**
 * sendChatStream 用该 Key 发**流式** chat 请求,逐增量回调 onDelta。⚠ 真实上游调用,消耗余额。
 * 用带 Bearer 的同源 fetch(lib/api 无流式),Key 仅作 Authorization 头、不落库/日志。SSE 解析走纯函数
 * extractSSEContent(已变异测试);跨 chunk 不完整尾行用 buffer 暂存。
 */
export async function sendChatStream(
  apiKey: string,
  model: string,
  system: string,
  message: string,
  onDelta: (text: string) => void,
  signal?: AbortSignal,
): Promise<void> {
  const resp = await fetch('/v1/chat/completions', {
    method: 'POST',
    credentials: 'include',
    headers: {
      Accept: 'text/event-stream',
      'Content-Type': 'application/json',
      Authorization: `Bearer ${apiKey.trim()}`,
    },
    body: JSON.stringify(buildChatRequest(model, system, message, true)),
    signal,
  })
  if (!resp.ok || !resp.body) {
    const text = await resp.text().catch(() => '')
    let code = `http_${resp.status}`
    let msg = resp.statusText || '请求失败'
    try {
      const b = JSON.parse(text)
      if (b?.error) {
        code = b.error.code ?? code
        msg = b.error.message ?? msg
      }
    } catch {
      /* 非 JSON 错误体,沿用状态文案 */
    }
    throw new ApiError(resp.status, code, msg)
  }

  const reader = resp.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  for (;;) {
    const { value, done } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    const lines = buffer.split('\n')
    buffer = lines.pop() ?? '' // 不完整尾行留到下一轮
    for (const line of lines) {
      const ev = extractSSEContent(line)
      if (ev.done) return
      if (ev.content) onDelta(ev.content)
    }
  }
  const tail = extractSSEContent(buffer)
  if (!tail.done && tail.content) onDelta(tail.content)
}
