import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const apiClient = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))

vi.mock('../../lib/api', async () => {
  const actual = await vi.importActual<typeof import('../../lib/api')>('../../lib/api')
  return { ...actual, apiGet: apiClient.get, apiSend: apiClient.send }
})

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
import {
  buildChatRequest,
  buildMessages,
  buildProtocolRequest,
  canSend,
  DEFAULT_PROTOCOL_FORM,
  extractImageSources,
  extractProtocolText,
  extractReply,
  extractSSEContent,
  formatUsage,
  parseProtocolSSELine,
  sortedRerankResults,
  summarizeEmbeddings,
} from './playground'
import type { ParsedSSEEvent, ProtocolFormState } from './types'

const form = (patch: Partial<ProtocolFormState> = {}): ProtocolFormState => ({ ...DEFAULT_PROTOCOL_FORM, ...patch })

describe('基础展示逻辑', () => {
  it('提取 chat 或 completions 首条文本，缺失时返回空串', () => {
    expect(extractReply({ choices: [{ message: { content: '你好' } }] })).toBe('你好')
    expect(extractReply({ choices: [{ text: '补全文本' }] })).toBe('补全文本')
    expect(extractReply({})).toBe('')
  })

  it('用量缺 total 时由输入与输出相加', () => {
    expect(formatUsage({ prompt_tokens: 3, completion_tokens: 4 })).toBe('输入 3 · 输出 4 · 合计 7 tokens')
    expect(formatUsage({ input_tokens: 5, output_tokens: 2 })).toContain('合计 7')
    expect(formatUsage(undefined)).toBe('')
  })

  it('三个必填任一空白都禁止旧版 chat 发送', () => {
    expect(canSend('hk_x', 'gpt-4o', '你好')).toBe(true)
    expect(canSend('', 'gpt-4o', '你好')).toBe(false)
    expect(canSend('hk_x', ' ', '你好')).toBe(false)
    expect(canSend('hk_x', 'gpt-4o', '')).toBe(false)
  })

  it('system 只在非空时前置', () => {
    expect(buildMessages('你是助手', '你好').map((message) => message.role)).toEqual(['system', 'user'])
    expect(buildMessages('  ', '你好').map((message) => message.role)).toEqual(['user'])
    expect(buildChatRequest(' gpt-4o ', '', '你好', true)).toMatchObject({ model: 'gpt-4o', stream: true })
  })
})

describe('协议表单请求体组装', () => {
  it('chat 组装 system/user 且流式开关进入 body；空消息拒绝', () => {
    const built = buildProtocolRequest('chat', ' gpt-4o ', form({ system: '规则', input: '问题' }), true)
    expect(built).toEqual({
      protocol: 'chat', path: '/v1/chat/completions', stream: true,
      body: { model: 'gpt-4o', messages: [{ role: 'system', content: '规则' }, { role: 'user', content: '问题' }], stream: true },
    })
    expect(() => buildProtocolRequest('chat', 'gpt-4o', form({ input: '  ' }), false)).toThrow('请填写消息')
  })

  it('completions 只发非流式 prompt；空 prompt 拒绝', () => {
    expect(buildProtocolRequest('completions', 'legacy', form({ input: '继续写' }), true)).toMatchObject({
      path: '/v1/completions', stream: false, body: { model: 'legacy', prompt: '继续写', stream: false },
    })
    expect(() => buildProtocolRequest('completions', 'legacy', form({ input: '' }), false)).toThrow('请填写Prompt')
  })

  it('messages 组装顶层 system、max_tokens 与消息；非正 max_tokens 拒绝', () => {
    expect(buildProtocolRequest('messages', 'claude', form({ system: '简洁', input: '你好', maxTokens: '256' }), true).body).toEqual({
      model: 'claude', max_tokens: 256, messages: [{ role: 'user', content: '你好' }], stream: true, system: '简洁',
    })
    expect(() => buildProtocolRequest('messages', 'claude', form({ input: '你好', maxTokens: '0' }), false)).toThrow('Max tokens必须是正整数')
  })

  it('responses 将 system 投影为 instructions；空 input 拒绝', () => {
    expect(buildProtocolRequest('responses', 'gpt-5', form({ system: '简洁', input: '回答' }), false).body).toEqual({
      model: 'gpt-5', input: '回答', instructions: '简洁', stream: false,
    })
    expect(() => buildProtocolRequest('responses', 'gpt-5', form({ input: ' ' }), false)).toThrow('请填写输入')
  })

  it('embeddings 使用 input 文本；空文本拒绝', () => {
    expect(buildProtocolRequest('embeddings', 'embed', form({ input: ' 向量化我 ' }), false)).toMatchObject({
      path: '/v1/embeddings', body: { model: 'embed', input: ' 向量化我 ' }, stream: false,
    })
    expect(() => buildProtocolRequest('embeddings', 'embed', form({ input: '' }), false)).toThrow('请填写输入文本')
  })

  it('rerank 按非空行组装 documents 与 top_n；空文档和超上限拒绝', () => {
    expect(buildProtocolRequest('rerank', 'ranker', form({ query: '相关性', documents: '文档 A\n\n 文档 B ', topN: '1' }), false).body).toEqual({
      model: 'ranker', query: '相关性', documents: ['文档 A', '文档 B'], return_documents: true, top_n: 1,
    })
    expect(() => buildProtocolRequest('rerank', 'ranker', form({ query: 'q', documents: '' }), false)).toThrow('至少输入一篇')
    expect(() => buildProtocolRequest('rerank', 'ranker', form({ query: 'q', documents: Array(1001).fill('d').join('\n') }), false)).toThrow('不能超过 1000')
  })

  it('images 组装 prompt、数量和响应格式；零张拒绝', () => {
    expect(buildProtocolRequest('images', 'image-1', form({ input: '一只猫', imageCount: '2', imageFormat: 'b64_json' }), false).body).toMatchObject({
      model: 'image-1', prompt: '一只猫', n: 2, response_format: 'b64_json', size: '1024x1024',
    })
    expect(() => buildProtocolRequest('images', 'image-1', form({ input: '猫', imageCount: '0' }), false)).toThrow('图片数量必须是正整数')
  })

  it('speech 组装 voice/格式；超过后端 4096 字符边界拒绝', () => {
    expect(buildProtocolRequest('speech', 'tts-1', form({ input: '你好', voice: 'alloy', audioFormat: 'mp3' }), false)).toMatchObject({
      path: '/v1/audio/speech', body: { model: 'tts-1', input: '你好', voice: 'alloy', response_format: 'mp3' },
    })
    expect(() => buildProtocolRequest('speech', 'tts-1', form({ input: 'a'.repeat(4097) }), false)).toThrow('不能超过 4096')
  })

  it('Gemini 模型进入 URL、原始 JSON 原形进入 body；坏 JSON 和数组拒绝', () => {
    expect(buildProtocolRequest('gemini', 'gemini/pro', form({ geminiAction: 'countTokens', rawJSON: '{"contents":[{"parts":[{"text":"hi"}]}]}' }), false)).toEqual({
      protocol: 'gemini', path: '/v1beta/models/gemini%2Fpro:countTokens', stream: false,
      body: { contents: [{ parts: [{ text: 'hi' }] }] },
    })
    expect(() => buildProtocolRequest('gemini', 'gemini-pro', form({ rawJSON: '{' }), false)).toThrow('不是合法 JSON')
    expect(() => buildProtocolRequest('gemini', 'gemini-pro', form({ rawJSON: '[]' }), false)).toThrow('必须是 JSON 对象')
    expect(() => buildProtocolRequest('gemini', '../gemini-pro', form(), false)).toThrow('路径穿越')
  })
})

describe('响应摘要与 SSE 解析', () => {
  it('分别提取 chat、messages、responses 文本', () => {
    expect(extractProtocolText('chat', { choices: [{ message: { content: 'chat' } }] })).toBe('chat')
    expect(extractProtocolText('messages', { content: [{ type: 'text', text: 'Claude' }] })).toBe('Claude')
    expect(extractProtocolText('responses', { output: [{ content: [{ type: 'output_text', text: 'Response' }] }] })).toBe('Response')
  })

  it('向量摘要保留真实维度但只预览前若干值', () => {
    expect(summarizeEmbeddings({ data: [{ index: 2, embedding: [0.1, 0.2, 0.3] }] }, 2)).toEqual([{ index: 2, dimensions: 3, preview: [0.1, 0.2] }])
  })

  it('rerank 按分数降序而不是相信上游顺序', () => {
    expect(sortedRerankResults({ results: [{ index: 0, relevance_score: 0.2 }, { index: 1, relevance_score: 0.9 }] }).map((item) => item.index)).toEqual([1, 0])
  })

  it('图片同时识别 URL 与 b64_json', () => {
    expect(extractImageSources({ data: [{ url: 'https://img.test/a.png' }, { b64_json: 'YWJj' }] })).toEqual(['https://img.test/a.png', 'data:image/png;base64,YWJj'])
  })

  it('三种流式协议读取各自增量字段与结束事件', () => {
    expect(extractSSEContent('data: {"choices":[{"delta":{"content":"你"}}]}')).toEqual({ done: false, content: '你' })
    expect(parseProtocolSSELine('messages', 'data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"好"}}').content).toBe('好')
    expect(parseProtocolSSELine('messages', 'data: {"type":"message_stop"}').done).toBe(true)
    expect(parseProtocolSSELine('responses', 'data: {"type":"response.output_text.delta","delta":"呀"}').content).toBe('呀')
    expect(parseProtocolSSELine('responses', 'data: {"type":"response.completed"}').done).toBe(true)
    expect(extractSSEContent('data: [DONE]')).toEqual({ done: true, content: '' })
    expect(extractSSEContent('data: {broken')).toEqual({ done: false, content: '' })
  })
})

describe('Playground API 请求契约', () => {
  beforeEach(() => {
    apiClient.get.mockReset().mockResolvedValue({ object: 'list', data: [] })
    apiClient.send.mockReset().mockResolvedValue({ ok: true })
  })

  afterEach(() => vi.unstubAllGlobals())

  it('模型列表锁定 GET 路径并只把所选 Key 作为 bearer', async () => {
    const controller = new AbortController()
    await listModels(' hk_test ', controller.signal)
    expect(apiClient.get).toHaveBeenCalledWith('/v1/models', { bearer: 'hk_test', signal: controller.signal })
  })

  it('七种 JSON 非流式调用锁定 POST、路径、body 与 bearer', async () => {
    const bodies = {
      chat: { model: 'chat', messages: [{ role: 'user', content: 'x' }], stream: false },
      completion: { model: 'legacy', prompt: 'x', stream: false },
      messages: { model: 'claude', max_tokens: 32, messages: [{ role: 'user', content: 'x' }], stream: false },
      responses: { model: 'gpt', input: 'x', stream: false },
      embeddings: { model: 'embed', input: 'x' },
      rerank: { model: 'rank', query: 'q', documents: ['d'] },
      images: { model: 'image', prompt: 'x', n: 1 },
    }
    await sendChat(' hk_key ', bodies.chat)
    await sendCompletion('hk_key', bodies.completion)
    await sendMessages('hk_key', bodies.messages)
    await sendResponses('hk_key', bodies.responses)
    await sendEmbeddings('hk_key', bodies.embeddings)
    await sendRerank('hk_key', bodies.rerank)
    await sendImageGeneration('hk_key', bodies.images)
    await sendGemini('hk_key', '/v1beta/models/gemini:generateContent', { contents: [] })
    expect(apiClient.send.mock.calls).toEqual([
      ['POST', '/v1/chat/completions', bodies.chat, { bearer: 'hk_key', signal: undefined }],
      ['POST', '/v1/completions', bodies.completion, { bearer: 'hk_key', signal: undefined }],
      ['POST', '/v1/messages', bodies.messages, { bearer: 'hk_key', signal: undefined }],
      ['POST', '/v1/responses', bodies.responses, { bearer: 'hk_key', signal: undefined }],
      ['POST', '/v1/embeddings', bodies.embeddings, { bearer: 'hk_key', signal: undefined }],
      ['POST', '/v1/rerank', bodies.rerank, { bearer: 'hk_key', signal: undefined }],
      ['POST', '/v1/images/generations', bodies.images, { bearer: 'hk_key', signal: undefined }],
      ['POST', '/v1beta/models/gemini:generateContent', { contents: [] }, { bearer: 'hk_key', signal: undefined }],
    ])
  })

  it.each([
    ['chat', '/v1/chat/completions', sendChatStream, 'data: {"choices":[{"delta":{"content":"a"}}]}\n\ndata: [DONE]\n\n'],
    ['messages', '/v1/messages', sendMessagesStream, 'data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"b"}}\n\ndata: {"type":"message_stop"}\n\n'],
    ['responses', '/v1/responses', sendResponsesStream, 'data: {"type":"response.output_text.delta","delta":"c"}\n\ndata: {"type":"response.completed"}\n\n'],
  ] as const)('%s 流式调用锁定路径、POST、stream=true 与 Bearer', async (_name, path, sender, sse) => {
    const fetchMock = vi.fn().mockResolvedValue(streamResponse(sse))
    vi.stubGlobal('fetch', fetchMock)
    const events: ParsedSSEEvent[] = []
    await sender(' hk_stream ', { model: 'm', stream: false }, (event) => events.push(event))
    const [calledPath, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(calledPath).toBe(path)
    expect(init.method).toBe('POST')
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer hk_stream')
    expect(JSON.parse(init.body as string)).toMatchObject({ model: 'm', stream: true })
    expect(events.some((event) => event.content !== '')).toBe(true)
    expect(events.at(-1)?.done).toBe(true)
  })

  it('SSE JSON 与结束标记跨网络 chunk 时仍完整解析', async () => {
    const fetchMock = vi.fn().mockResolvedValue(chunkedStreamResponse([
      'data: {"choices":[{"delta":{"cont',
      'ent":"跨块"}}]}\n\ndata: [DO',
      'NE]\n\n',
    ]))
    vi.stubGlobal('fetch', fetchMock)
    const events: ParsedSSEEvent[] = []
    await sendChatStream('hk_stream', { model: 'm' }, (event) => events.push(event))
    expect(events.map((event) => event.content).join('')).toBe('跨块')
    expect(events.at(-1)?.done).toBe(true)
  })

  it('speech 走二进制 POST 并保留真实 Content-Type', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(new Uint8Array([1, 2, 3]), { status: 200, headers: { 'Content-Type': 'audio/mpeg' } }))
    vi.stubGlobal('fetch', fetchMock)
    const result = await sendSpeech(' hk_audio ', { model: 'tts-1', input: 'hi', voice: 'alloy', response_format: 'mp3' })
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/v1/audio/speech')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body as string)).toMatchObject({ model: 'tts-1', input: 'hi', voice: 'alloy' })
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer hk_audio')
    expect(result.contentType).toBe('audio/mpeg')
    expect(result.blob.size).toBe(3)
  })

  it('HTTP 200 但无流体时返回可诊断错误，不假装成功', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 200 })))
    await expect(sendChatStream('hk_stream', { model: 'm' }, () => undefined)).rejects.toMatchObject({
      status: 502,
      code: 'empty_stream',
    })
  })
})

function streamResponse(sse: string): Response {
  const bytes = new TextEncoder().encode(sse)
  return new Response(new ReadableStream({ start(controller) { controller.enqueue(bytes); controller.close() } }), {
    status: 200,
    headers: { 'Content-Type': 'text/event-stream' },
  })
}

function chunkedStreamResponse(chunks: string[]): Response {
  const encoder = new TextEncoder()
  return new Response(new ReadableStream({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk))
      controller.close()
    },
  }), { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
}
