import type { ChatMessage, ChatRequest, ChatResponse, ChatUsage } from './types'

/*
 * Playground 纯逻辑(与 React/网络解耦,便于 vitest 变异测试)。
 */

/** extractReply 取首个 choice 的 assistant 文本;缺失则空串(防 undefined 渲染)。 */
export function extractReply(resp: ChatResponse): string {
  return resp.choices?.[0]?.message?.content ?? ''
}

/** formatUsage 把 token 用量转成展示串;无用量则空串。 */
export function formatUsage(usage?: ChatUsage): string {
  if (!usage) return ''
  const p = usage.prompt_tokens ?? 0
  const c = usage.completion_tokens ?? 0
  const t = usage.total_tokens ?? p + c
  return `输入 ${p} · 输出 ${c} · 合计 ${t} tokens`
}

/** canSend 校验三个必填(key/model/message 非空白)。任一空则禁止发送(防空请求白扣)。 */
export function canSend(apiKey: string, model: string, message: string): boolean {
  return apiKey.trim() !== '' && model.trim() !== '' && message.trim() !== ''
}

/** buildMessages 组装消息数组:system 非空时前置一条 system 消息,再附 user 消息。 */
export function buildMessages(system: string, message: string): ChatMessage[] {
  const messages: ChatMessage[] = []
  if (system.trim() !== '') {
    messages.push({ role: 'system', content: system })
  }
  messages.push({ role: 'user', content: message })
  return messages
}

/** buildChatRequest 组装 chat 请求体。stream 默认 false(非流式)。 */
export function buildChatRequest(model: string, system: string, message: string, stream = false): ChatRequest {
  return { model: model.trim(), messages: buildMessages(system, message), stream }
}

/**
 * extractSSEContent 解析一行 SSE `data:` 文本,取增量内容。OpenAI 流式:
 * `data: {"choices":[{"delta":{"content":"x"}}]}` → {done:false, content:"x"};`data: [DONE]` → {done:true}。
 * 非 data 行 / 空 / 无 delta / 解析失败 → {done:false, content:""}(健壮跳过,不抛)。
 */
export function extractSSEContent(line: string): { done: boolean; content: string } {
  const trimmed = line.trim()
  if (!trimmed.startsWith('data:')) return { done: false, content: '' }
  const payload = trimmed.slice('data:'.length).trim()
  if (payload === '') return { done: false, content: '' }
  if (payload === '[DONE]') return { done: true, content: '' }
  try {
    const obj = JSON.parse(payload)
    const content = obj?.choices?.[0]?.delta?.content
    return { done: false, content: typeof content === 'string' ? content : '' }
  } catch {
    return { done: false, content: '' }
  }
}
