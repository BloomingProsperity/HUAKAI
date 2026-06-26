import { describe, expect, it } from 'vitest'
import { buildChatRequest, buildMessages, canSend, extractReply, extractSSEContent, formatUsage } from './playground'
import type { ChatResponse } from './types'

describe('extractReply', () => {
  it('取首个 choice 的 content(变异:若取错索引/字段则对不上)', () => {
    const resp: ChatResponse = { choices: [{ message: { role: 'assistant', content: '你好' } }] }
    expect(extractReply(resp)).toBe('你好')
  })
  it('缺 choices/message 时返空串(不抛、不渲染 undefined)', () => {
    expect(extractReply({})).toBe('')
    expect(extractReply({ choices: [] })).toBe('')
    expect(extractReply({ choices: [{}] })).toBe('')
  })
})

describe('formatUsage', () => {
  it('有用量 → 含三段数值(变异:漏字段则数对不上)', () => {
    expect(formatUsage({ prompt_tokens: 10, completion_tokens: 5, total_tokens: 15 })).toBe(
      '输入 10 · 输出 5 · 合计 15 tokens',
    )
  })
  it('缺 total 时由 输入+输出 兜底', () => {
    expect(formatUsage({ prompt_tokens: 3, completion_tokens: 4 })).toContain('合计 7')
  })
  it('无用量 → 空串', () => {
    expect(formatUsage(undefined)).toBe('')
  })
})

describe('canSend', () => {
  it('三必填非空 → true;任一空白 → false(变异:若只查部分字段则漏空)', () => {
    expect(canSend('hk_x', 'gpt-4o', '你好')).toBe(true)
    expect(canSend('', 'gpt-4o', '你好')).toBe(false)
    expect(canSend('hk_x', '   ', '你好')).toBe(false)
    expect(canSend('hk_x', 'gpt-4o', '')).toBe(false)
  })
})

describe('buildChatRequest', () => {
  it('有 system → [system, user] 两条;model trim;stream=false', () => {
    const req = buildChatRequest('  gpt-4o  ', '你是助手', '你好')
    expect(req.model).toBe('gpt-4o')
    expect(req.stream).toBe(false)
    expect(req.messages.map((m) => m.role)).toEqual(['system', 'user'])
    expect(req.messages[1].content).toBe('你好')
  })
  it('无 system → 仅 [user](变异:若总加 system 则长度=2)', () => {
    const req = buildChatRequest('gpt-4o', '   ', '你好')
    expect(req.messages.map((m) => m.role)).toEqual(['user'])
  })
  it('stream=true 时请求体 stream 为 true', () => {
    expect(buildChatRequest('gpt-4o', '', '你好', true).stream).toBe(true)
  })
})

describe('buildMessages', () => {
  it('有 system 前置 system,再 user', () => {
    expect(buildMessages('你是助手', '你好').map((m) => m.role)).toEqual(['system', 'user'])
  })
  it('空 system 仅 user', () => {
    expect(buildMessages('  ', '你好').map((m) => m.role)).toEqual(['user'])
  })
})

describe('extractSSEContent', () => {
  it('data + delta.content → 取增量(变异:若不读 delta.content 则空)', () => {
    expect(extractSSEContent('data: {"choices":[{"delta":{"content":"你"}}]}')).toEqual({ done: false, content: '你' })
  })
  it('data: [DONE] → done=true', () => {
    expect(extractSSEContent('data: [DONE]')).toEqual({ done: true, content: '' })
  })
  it('非 data 行 / 空 / 无 delta / 坏 JSON → 健壮空增量不抛', () => {
    expect(extractSSEContent(': comment')).toEqual({ done: false, content: '' })
    expect(extractSSEContent('')).toEqual({ done: false, content: '' })
    expect(extractSSEContent('data: {"choices":[{"delta":{"role":"assistant"}}]}')).toEqual({ done: false, content: '' })
    expect(extractSSEContent('data: {not json')).toEqual({ done: false, content: '' })
  })
})
