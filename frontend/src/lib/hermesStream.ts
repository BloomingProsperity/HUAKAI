/*
 * Hermes 流式对话客户端(SSE)。
 *
 * lib/api.ts 不支持 SSE,故这里单独实现:带 admin Bearer 的同源 fetch + ReadableStream 逐块读取。
 * 解析逻辑(分块累积 → 按空行切事件 → 解出 event/data)抽成纯函数,便于 vitest 变异测试:
 *  - parseSSEBlocks(buffer):把缓冲区按事件边界(空行)切出已完整的事件块,剩余不完整尾部回传 rest;
 *  - dispatchEvent(event, handlers):按 event 名分派到对应 handler;未知 event 静默忽略(不抛、不崩)。
 *
 * 已亲核的后端契约(SSE,由 Hermes runner 经网关 /v1/hermes/chat 透传):
 *  - event: conversation / data: {"id": <int64>}  —— 新会话 ID,保存供后续轮带 conversation_id
 *  - event: token        / data: {"delta": "<块>"} —— 流式助手文本,累加渲染
 *  - event: done         / data: {"total_tokens": <int>} —— 本轮结束
 *  - event: error        / data: {"code":"...","message":"..."} —— 错误
 * 工具调用由服务端内部回调执行,不保证以 SSE 露出,故本面板只渲染流式文本,无工具气泡逻辑。
 *
 * 鉴权:/v1/hermes/* 不匹配 tokenForPath 的 admin 前缀(它只认 /admin/* 与 /v1/admin/*),
 * 若回落到 session token 会被后端恒 401。故本模块所有请求都显式带 admin token 作 Bearer。
 */

// API_BASE 同源(生产期前端由网关 go:embed 提供,与 API 同源)。
const API_BASE = ''

// SSE 累积缓冲的上限保护:畸形/恶意流若一直不发事件边界(\n\n),buffer 会无限增长直至 OOM。
// 缺省 8M 字符——正常 Hermes 响应远小于此;超过即中止并经 onError 报错。仅测试需要时通过
// StreamChatParams.maxBufferChars 下调。
const DEFAULT_MAX_SSE_BUFFER_CHARS = 8 * 1024 * 1024

/** 解析出的单个 SSE 事件:event 名 + 原始 data 文本(未 JSON 解析)。 */
export interface SSEEvent {
  /** event: 行的值;缺省按 SSE 规范为空字符串(此时多按 "message" 处理,这里保留空)。 */
  event: string
  /** data: 行拼接后的原始文本(多行 data 以 \n 连接)。 */
  data: string
}

/**
 * parseSSEBlocks 把累积缓冲区按事件边界切分。SSE 以空行(\n\n)分隔事件;一个事件可能跨多次
 * fetch read 到达,故只切出"已完整"的事件块,把最后一段不完整尾部作为 rest 回传给下一轮拼接。
 *
 * 解析规则(对每个事件块逐行扫描):
 *  - `event:` 行 → 设事件名(去掉前缀与一个可选前导空格);
 *  - `data:`  行 → 收集为 data(多条 data 行以 \n 连接,符合 SSE 规范);
 *  - `:` 开头 → 注释行,忽略;其余未知字段行忽略。
 * 没有任何 data 行的事件块跳过(纯注释 / 心跳),避免产出空事件。
 */
export function parseSSEBlocks(buffer: string): { events: SSEEvent[]; rest: string } {
  // 统一换行(部分实现用 \r\n),便于按 \n\n 切块。
  const normalized = buffer.replace(/\r\n/g, '\n')
  const events: SSEEvent[] = []
  let rest = normalized
  for (;;) {
    const sep = rest.indexOf('\n\n')
    if (sep === -1) break // 余下尾部尚不完整,留待下一轮
    const block = rest.slice(0, sep)
    rest = rest.slice(sep + 2)
    const ev = parseSSEEventBlock(block)
    if (ev) events.push(ev)
  }
  return { events, rest }
}

/** parseSSEEventBlock 解析单个事件块文本;无 data 行返回 null(纯注释/心跳跳过)。 */
function parseSSEEventBlock(block: string): SSEEvent | null {
  let event = ''
  const dataLines: string[] = []
  let sawData = false
  for (const rawLine of block.split('\n')) {
    const line = rawLine
    if (line === '' || line.startsWith(':')) continue // 空行/注释行
    if (line.startsWith('event:')) {
      event = stripFieldValue(line.slice('event:'.length))
      continue
    }
    if (line.startsWith('data:')) {
      dataLines.push(stripFieldValue(line.slice('data:'.length)))
      sawData = true
      continue
    }
    // 其余字段(id:/retry: 等)本面板不需要,忽略。
  }
  if (!sawData) return null
  return { event, data: dataLines.join('\n') }
}

/** stripFieldValue 去掉字段值的一个可选前导空格(SSE 规范:`field: value` 的冒号后一个空格不计入值)。 */
function stripFieldValue(v: string): string {
  return v.startsWith(' ') ? v.slice(1) : v
}

/** 各 SSE 事件类型对应的解析后载荷与回调。未知 event 不在此列,会被 dispatchEvent 忽略。 */
export interface SSEHandlers {
  /** event: conversation —— 新会话 ID。 */
  onConversation?: (id: number) => void
  /** event: token —— 流式助手文本增量(累加渲染)。 */
  onToken?: (delta: string) => void
  /** event: done —— 本轮结束(可携带 total_tokens)。 */
  onDone?: (totalTokens: number | undefined) => void
  /** event: error —— 错误。 */
  onError?: (code: string, message: string) => void
}

/**
 * dispatchEvent 按事件名解析 data(JSON)并分派到对应 handler。
 *  - 未知 event:静默忽略(优雅,不崩);
 *  - data 非合法 JSON:对 token/conversation/done 跳过(不崩);对 error 退化为以原始 data 作 message。
 * 返回是否为终止事件(done / error),供调用方决定是否停止读取。
 */
export function dispatchEvent(ev: SSEEvent, handlers: SSEHandlers): { terminal: boolean } {
  switch (ev.event) {
    case 'conversation': {
      const obj = safeParse(ev.data)
      const id = obj && typeof obj.id === 'number' ? obj.id : undefined
      if (id !== undefined && handlers.onConversation) handlers.onConversation(id)
      return { terminal: false }
    }
    case 'token': {
      const obj = safeParse(ev.data)
      const delta = obj && typeof obj.delta === 'string' ? obj.delta : ''
      if (delta && handlers.onToken) handlers.onToken(delta)
      return { terminal: false }
    }
    case 'done': {
      const obj = safeParse(ev.data)
      const total = obj && typeof obj.total_tokens === 'number' ? obj.total_tokens : undefined
      if (handlers.onDone) handlers.onDone(total)
      return { terminal: true }
    }
    case 'error': {
      const obj = safeParse(ev.data)
      const code = obj && typeof obj.code === 'string' ? obj.code : 'hermes_error'
      const message = obj && typeof obj.message === 'string' ? obj.message : ev.data || '请求失败'
      if (handlers.onError) handlers.onError(code, message)
      return { terminal: true }
    }
    default:
      // 未知 event:优雅忽略。
      return { terminal: false }
  }
}

/**
 * parseErrorEnvelope 解析非流式失败时的错误体,产出 {code, message}。后端有两种形态:
 *  - 嵌套:{"error":{"code":"...","message":"..."}}(多数端点);
 *  - 扁平:{"error":"hermes_disabled"}(如 chat 在 Hermes 关闭时)。
 * 必须先判扁平字符串再判嵌套对象——否则 if(error) 对字符串先为真,会把扁平 code 丢成 http_<status>。
 * 解析失败 / 无 error 字段时退回 HTTP 状态文案。导出供单测覆盖这条易错分支。
 */
export function parseErrorEnvelope(
  text: string,
  status: number,
  statusText: string,
): { code: string; message: string } {
  let code = `http_${status}`
  let message = statusText || '请求失败'
  try {
    const b = JSON.parse(text) as { error?: unknown }
    const err = b?.error
    if (typeof err === 'string' && err.trim() !== '') {
      code = err
      message = err
    } else if (err && typeof err === 'object') {
      const e = err as { code?: unknown; message?: unknown }
      if (typeof e.code === 'string' && e.code !== '') code = e.code
      if (typeof e.message === 'string' && e.message !== '') message = e.message
    }
  } catch {
    /* 非 JSON 错误体,沿用状态文案 */
  }
  return { code, message }
}

/** safeParse 容错 JSON 解析:失败返回 null(不抛),让上层退化处理。 */
function safeParse(text: string): Record<string, unknown> | null {
  const t = text.trim()
  if (t === '') return null
  try {
    const v = JSON.parse(t)
    return v && typeof v === 'object' ? (v as Record<string, unknown>) : null
  } catch {
    return null
  }
}

/** streamChat 的入参。adminToken 必填(无则上层走"需运维者 token"空状态,不应调用本函数)。 */
export interface StreamChatParams {
  adminToken: string
  /** ?as_user_id=<正整数>,必填(缺则后端 400 hermes_admin_user_required)。 */
  asUserId: number
  /** ?tenant_id=<可选>;platform_admin 必填,tenant_operator 可省。 */
  tenantId?: number
  /** 完整 messages 数组(含本面板注入的页面上下文前缀)。 */
  messages: Array<{ role: string; content: string }>
  /** 续聊时带上轮返回的会话 ID;首轮 null。 */
  conversationId: number | null
  handlers: SSEHandlers
  signal?: AbortSignal
  /** SSE 累积缓冲上限(字符数);缺省 DEFAULT_MAX_SSE_BUFFER_CHARS,超过即中止报 hermes_stream_overflow。仅测试需要时下调。 */
  maxBufferChars?: number
}

/**
 * streamChat 发起一轮流式对话。带 admin Bearer 的同源 fetch → 读 body reader → 跨 chunk 缓冲 →
 * parseSSEBlocks 切事件 → dispatchEvent 分派。HTTP 非 2xx 时尽力解析错误信封并经 onError 上报。
 *
 * 安全:adminToken 只作 Authorization 头,绝不写日志/不渲染明文。
 */
export async function streamChat(params: StreamChatParams): Promise<void> {
  const { adminToken, asUserId, tenantId, messages, conversationId, handlers, signal } = params
  const maxBufferChars = params.maxBufferChars ?? DEFAULT_MAX_SSE_BUFFER_CHARS
  const query = new URLSearchParams()
  query.set('as_user_id', String(asUserId))
  if (tenantId !== undefined) query.set('tenant_id', String(tenantId))
  const url = `${API_BASE}/v1/hermes/chat?${query.toString()}`

  const resp = await fetch(url, {
    method: 'POST',
    credentials: 'include',
    headers: {
      Accept: 'text/event-stream',
      'Content-Type': 'application/json',
      Authorization: `Bearer ${adminToken}`,
    },
    body: JSON.stringify({ messages, conversation_id: conversationId }),
    signal,
  })

  if (!resp.ok || !resp.body) {
    const text = await resp.text().catch(() => '')
    const { code, message } = parseErrorEnvelope(text, resp.status, resp.statusText)
    handlers.onError?.(code, message)
    return
  }

  const reader = resp.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  for (;;) {
    const { value, done } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    const { events, rest } = parseSSEBlocks(buffer)
    buffer = rest
    // 上限保护:残留缓冲超过上限说明流畸形(始终无事件边界),中止防 OOM。
    if (buffer.length > maxBufferChars) {
      handlers.onError?.('hermes_stream_overflow', '响应流过大,已中止')
      try {
        await reader.cancel()
      } catch {
        /* 取消时的异常无害,忽略 */
      }
      return
    }
    for (const ev of events) {
      const { terminal } = dispatchEvent(ev, handlers)
      if (terminal) {
        // 终止事件后取消底层读取并返回(忽略取消异常)。
        try {
          await reader.cancel()
        } catch {
          /* 取消时的异常无害,忽略 */
        }
        return
      }
    }
  }
  // 流自然结束:尝试 flush 残留缓冲(可能有最后一个不带尾随空行的事件块)。
  const flushed = decoder.decode()
  if (flushed) buffer += flushed
  const tail = parseSSEBlocks(buffer + '\n\n')
  for (const ev of tail.events) {
    if (dispatchEvent(ev, handlers).terminal) return
  }
}
