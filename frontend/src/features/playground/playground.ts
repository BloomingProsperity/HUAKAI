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

/** buildChatRequest 组装非流式 chat 请求体。system 非空时前置一条 system 消息。 */
export function buildChatRequest(model: string, system: string, message: string): ChatRequest {
  const messages: ChatMessage[] = []
  if (system.trim() !== '') {
    messages.push({ role: 'system', content: system })
  }
  messages.push({ role: 'user', content: message })
  return { model: model.trim(), messages, stream: false }
}
