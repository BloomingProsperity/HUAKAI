import type {
  ChatMessage,
  ChatRequest,
  ChatResponse,
  ChatUsage,
  JSONRecord,
  ParsedSSEEvent,
  PlaygroundProtocol,
  ProtocolFormState,
  ProtocolRequestPlan,
  RerankResultView,
} from './types'

export const DEFAULT_PROTOCOL_FORM: ProtocolFormState = {
  system: '',
  input: '',
  query: '',
  documents: '',
  maxTokens: '1024',
  topN: '',
  imageCount: '1',
  imageSize: '1024x1024',
  imageQuality: '',
  imageFormat: '',
  voice: 'alloy',
  audioFormat: 'mp3',
  geminiAction: 'generateContent',
  rawJSON: '{\n  "contents": [\n    {\n      "role": "user",\n      "parts": [{ "text": "你好" }]\n    }\n  ]\n}',
}

export function extractReply(resp: ChatResponse): string {
  return resp.choices?.[0]?.message?.content ?? resp.choices?.[0]?.text ?? ''
}

export function formatUsage(usage?: ChatUsage): string {
  if (!usage) return ''
  const p = usage.prompt_tokens ?? usage.input_tokens ?? 0
  const c = usage.completion_tokens ?? usage.output_tokens ?? 0
  const t = usage.total_tokens ?? p + c
  return `输入 ${p} · 输出 ${c} · 合计 ${t} tokens`
}

export function canSend(apiKey: string, model: string, message: string): boolean {
  return apiKey.trim() !== '' && model.trim() !== '' && message.trim() !== ''
}

export function buildMessages(system: string, message: string): ChatMessage[] {
  const messages: ChatMessage[] = []
  if (system.trim() !== '') messages.push({ role: 'system', content: system })
  messages.push({ role: 'user', content: message })
  return messages
}

export function buildChatRequest(model: string, system: string, message: string, stream = false): ChatRequest {
  return { model: required(model, '模型'), messages: buildMessages(system, requiredText(message, '消息')), stream }
}

export function buildProtocolRequest(
  protocol: PlaygroundProtocol,
  model: string,
  form: ProtocolFormState,
  streaming: boolean,
): ProtocolRequestPlan {
  const selectedModel = required(model, '模型')
  switch (protocol) {
    case 'chat':
      return plan(protocol, '/v1/chat/completions', { ...buildChatRequest(selectedModel, form.system, form.input, streaming) }, streaming)
    case 'completions':
      return plan(protocol, '/v1/completions', {
        model: selectedModel,
        prompt: requiredText(form.input, 'Prompt'),
        stream: false,
      })
    case 'messages': {
      const body: JSONRecord = {
        model: selectedModel,
        max_tokens: positiveInteger(form.maxTokens, 'Max tokens'),
        messages: [{ role: 'user', content: requiredText(form.input, '消息') }],
        stream: streaming,
      }
      if (form.system.trim()) body.system = form.system
      return plan(protocol, '/v1/messages', body, streaming)
    }
    case 'responses': {
      const body: JSONRecord = {
        model: selectedModel,
        input: requiredText(form.input, '输入'),
        stream: streaming,
      }
      if (form.system.trim()) body.instructions = form.system
      return plan(protocol, '/v1/responses', body, streaming)
    }
    case 'embeddings':
      return plan(protocol, '/v1/embeddings', { model: selectedModel, input: requiredText(form.input, '输入文本') })
    case 'rerank': {
      const documents = form.documents
        .split('\n')
        .map((item) => item.trim())
        .filter(Boolean)
      if (documents.length === 0) throw new Error('至少输入一篇候选文档')
      if (documents.length > 1000) throw new Error('候选文档不能超过 1000 篇')
      const body: JSONRecord = {
        model: selectedModel,
        query: requiredText(form.query, '查询文本'),
        documents,
        return_documents: true,
      }
      if (form.topN.trim()) body.top_n = positiveInteger(form.topN, 'Top N')
      return plan(protocol, '/v1/rerank', body)
    }
    case 'images': {
      const body: JSONRecord = {
        model: selectedModel,
        prompt: requiredText(form.input, '图片描述'),
        n: positiveInteger(form.imageCount, '图片数量'),
      }
      if (form.imageFormat) body.response_format = form.imageFormat
      if (form.imageSize.trim()) body.size = form.imageSize.trim()
      if (form.imageQuality.trim()) body.quality = form.imageQuality.trim()
      return plan(protocol, '/v1/images/generations', body)
    }
    case 'speech': {
      const input = requiredText(form.input, '朗读文本')
      if (Array.from(input).length > 4096) throw new Error('朗读文本不能超过 4096 字符')
      return plan(protocol, '/v1/audio/speech', {
        model: selectedModel,
        input,
        voice: required(form.voice, '音色'),
        response_format: form.audioFormat,
      })
    }
    case 'gemini': {
      if (selectedModel.includes('\\') || selectedModel.split('/').some((segment) => segment === '.' || segment === '..')) {
        throw new Error('Gemini 模型路径不能包含路径穿越片段')
      }
      return plan(
        protocol,
        `/v1beta/models/${encodeURIComponent(selectedModel)}:${form.geminiAction}`,
        parseJSONObject(form.rawJSON, 'Gemini 请求 JSON'),
      )
    }
  }
}

export function protocolFormError(
  protocol: PlaygroundProtocol,
  model: string,
  form: ProtocolFormState,
  streaming: boolean,
): string | null {
  try {
    buildProtocolRequest(protocol, model, form, streaming)
    return null
  } catch (error) {
    return error instanceof Error ? error.message : '请求参数无效'
  }
}

export function parseProtocolSSELine(protocol: PlaygroundProtocol, line: string): ParsedSSEEvent {
  const trimmed = line.trim()
  if (!trimmed.startsWith('data:')) return { done: false, content: '' }
  const raw = trimmed.slice('data:'.length).trim()
  if (!raw) return { done: false, content: '' }
  if (raw === '[DONE]') return { done: true, content: '' }
  try {
    const payload = JSON.parse(raw) as Record<string, any>
    if (protocol === 'messages') {
      return {
        done: payload.type === 'message_stop',
        content: typeof payload?.delta?.text === 'string' ? payload.delta.text : '',
        payload,
      }
    }
    if (protocol === 'responses') {
      return {
        done: payload.type === 'response.completed' || payload.type === 'response.failed',
        content: payload.type === 'response.output_text.delta' && typeof payload.delta === 'string' ? payload.delta : '',
        payload,
      }
    }
    return {
      done: false,
      content: typeof payload?.choices?.[0]?.delta?.content === 'string' ? payload.choices[0].delta.content : '',
      payload,
    }
  } catch {
    return { done: false, content: '' }
  }
}

export function extractSSEContent(line: string): { done: boolean; content: string } {
  const event = parseProtocolSSELine('chat', line)
  return { done: event.done, content: event.content }
}

export function extractProtocolText(protocol: PlaygroundProtocol, response: unknown): string {
  const root = asRecord(response)
  if (!root) return ''
  if (protocol === 'chat' || protocol === 'completions') return extractReply(root as ChatResponse)
  if (protocol === 'messages') {
    return asArray(root.content)
      .map((part) => asRecord(part)?.text)
      .filter((text): text is string => typeof text === 'string')
      .join('')
  }
  if (protocol === 'responses') {
    if (typeof root.output_text === 'string') return root.output_text
    return asArray(root.output)
      .flatMap((item) => asArray(asRecord(item)?.content))
      .map((part) => {
        const block = asRecord(part)
        return typeof block?.text === 'string' ? block.text : ''
      })
      .join('')
  }
  return ''
}

export function summarizeEmbeddings(response: unknown, previewSize = 8): Array<{ index: number; dimensions: number; preview: number[] }> {
  const data = asArray(asRecord(response)?.data)
  return data.map((item, position) => {
    const row = asRecord(item)
    const vector = asArray(row?.embedding).filter((value): value is number => typeof value === 'number')
    return {
      index: typeof row?.index === 'number' ? row.index : position,
      dimensions: vector.length,
      preview: vector.slice(0, previewSize),
    }
  })
}

export function sortedRerankResults(response: unknown): RerankResultView[] {
  const root = asRecord(response)
  const rows = asArray(root?.results ?? root?.data)
  return rows
    .map((item, position) => {
      const row = asRecord(item)
      const score = typeof row?.relevance_score === 'number' ? row.relevance_score : typeof row?.score === 'number' ? row.score : 0
      return {
        index: typeof row?.index === 'number' ? row.index : position,
        score,
        document: row?.document,
      }
    })
    .sort((a, b) => b.score - a.score)
}

export function extractImageSources(response: unknown): string[] {
  return asArray(asRecord(response)?.data)
    .map((item) => {
      const row = asRecord(item)
      if (typeof row?.url === 'string' && row.url.trim()) return row.url.trim()
      if (typeof row?.b64_json === 'string' && row.b64_json.trim()) return `data:image/png;base64,${row.b64_json.trim()}`
      return ''
    })
    .filter(Boolean)
}

function plan(
  protocol: PlaygroundProtocol,
  path: string,
  body: JSONRecord,
  stream = false,
): ProtocolRequestPlan {
  return { protocol, path, body, stream }
}

function required(value: string, label: string): string {
  const trimmed = value.trim()
  if (!trimmed) throw new Error(`请填写${label}`)
  return trimmed
}

function requiredText(value: string, label: string): string {
  if (!value.trim()) throw new Error(`请填写${label}`)
  return value
}

function positiveInteger(raw: string, label: string): number {
  const value = Number(raw)
  if (!Number.isInteger(value) || value <= 0) throw new Error(`${label}必须是正整数`)
  return value
}

function parseJSONObject(raw: string, label: string): JSONRecord {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    throw new Error(`${label}不是合法 JSON`)
  }
  const record = asRecord(parsed)
  if (!record) throw new Error(`${label}必须是 JSON 对象`)
  return record
}

function asRecord(value: unknown): Record<string, any> | null {
  return typeof value === 'object' && value !== null && !Array.isArray(value) ? (value as Record<string, any>) : null
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}
