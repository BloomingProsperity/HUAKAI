import { ApiError, apiGet, apiSend } from '../../lib/api'
import { parseProtocolSSELine } from './playground'
import type {
  AudioResponse,
  JSONRecord,
  ModelListResponse,
  ParsedSSEEvent,
  PlaygroundProtocol,
} from './types'

export async function listModels(apiKey: string, signal?: AbortSignal): Promise<ModelListResponse> {
  return apiGet<ModelListResponse>('/v1/models', { bearer: apiKey.trim(), signal })
}

export function sendChat(apiKey: string, body: JSONRecord, signal?: AbortSignal): Promise<unknown> {
  return sendJSON(apiKey, '/v1/chat/completions', body, signal)
}

export function sendCompletion(apiKey: string, body: JSONRecord, signal?: AbortSignal): Promise<unknown> {
  return sendJSON(apiKey, '/v1/completions', body, signal)
}

export function sendMessages(apiKey: string, body: JSONRecord, signal?: AbortSignal): Promise<unknown> {
  return sendJSON(apiKey, '/v1/messages', body, signal)
}

export function sendResponses(apiKey: string, body: JSONRecord, signal?: AbortSignal): Promise<unknown> {
  return sendJSON(apiKey, '/v1/responses', body, signal)
}

export function sendEmbeddings(apiKey: string, body: JSONRecord, signal?: AbortSignal): Promise<unknown> {
  return sendJSON(apiKey, '/v1/embeddings', body, signal)
}

export function sendRerank(apiKey: string, body: JSONRecord, signal?: AbortSignal): Promise<unknown> {
  return sendJSON(apiKey, '/v1/rerank', body, signal)
}

export function sendImageGeneration(apiKey: string, body: JSONRecord, signal?: AbortSignal): Promise<unknown> {
  return sendJSON(apiKey, '/v1/images/generations', body, signal)
}

export function sendGemini(
  apiKey: string,
  path: string,
  body: JSONRecord,
  signal?: AbortSignal,
): Promise<unknown> {
  return sendJSON(apiKey, path, body, signal)
}

export function sendChatStream(
  apiKey: string,
  body: JSONRecord,
  onEvent: (event: ParsedSSEEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  return sendEventStream(apiKey, '/v1/chat/completions', 'chat', body, onEvent, signal)
}

export function sendMessagesStream(
  apiKey: string,
  body: JSONRecord,
  onEvent: (event: ParsedSSEEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  return sendEventStream(apiKey, '/v1/messages', 'messages', body, onEvent, signal)
}

export function sendResponsesStream(
  apiKey: string,
  body: JSONRecord,
  onEvent: (event: ParsedSSEEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  return sendEventStream(apiKey, '/v1/responses', 'responses', body, onEvent, signal)
}

export async function sendSpeech(
  apiKey: string,
  body: JSONRecord,
  signal?: AbortSignal,
): Promise<AudioResponse> {
  const resp = await fetch('/v1/audio/speech', requestInit(apiKey, body, 'audio/*', signal))
  if (!resp.ok) throw await responseError(resp)
  return {
    blob: await resp.blob(),
    contentType: resp.headers.get('Content-Type') || 'application/octet-stream',
  }
}

async function sendJSON(apiKey: string, path: string, body: JSONRecord, signal?: AbortSignal): Promise<unknown> {
  return apiSend<unknown>('POST', path, body, { bearer: apiKey.trim(), signal })
}

async function sendEventStream(
  apiKey: string,
  path: string,
  protocol: PlaygroundProtocol,
  body: JSONRecord,
  onEvent: (event: ParsedSSEEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  const resp = await fetch(path, requestInit(apiKey, { ...body, stream: true }, 'text/event-stream', signal))
  if (!resp.ok) throw await responseError(resp)
  if (!resp.body) throw new ApiError(502, 'empty_stream', '流式响应体为空')

  const reader = resp.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  for (;;) {
    const { value, done } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    const lines = buffer.split('\n')
    buffer = lines.pop() ?? ''
    for (const line of lines) {
      const event = parseProtocolSSELine(protocol, line)
      if (event.payload !== undefined || event.done) onEvent(event)
      if (event.done) {
        await reader.cancel().catch(() => undefined)
        return
      }
    }
  }
  buffer += decoder.decode()
  const tail = parseProtocolSSELine(protocol, buffer)
  if (tail.payload !== undefined || tail.done) onEvent(tail)
}

function requestInit(apiKey: string, body: JSONRecord, accept: string, signal?: AbortSignal): RequestInit {
  return {
    method: 'POST',
    credentials: 'include',
    headers: {
      Accept: accept,
      'Content-Type': 'application/json',
      Authorization: `Bearer ${apiKey.trim()}`,
    },
    body: JSON.stringify(body),
    signal,
  }
}

async function responseError(resp: Response): Promise<ApiError> {
  const text = await resp.text().catch(() => '')
  let code = `http_${resp.status}`
  let message = resp.statusText || '请求失败'
  try {
    const parsed = JSON.parse(text) as { error?: { code?: string; message?: string } }
    code = parsed.error?.code || code
    message = parsed.error?.message || message
  } catch {
    // 二进制或纯文本错误体没有结构化错误码。
  }
  return new ApiError(resp.status, code, message)
}
