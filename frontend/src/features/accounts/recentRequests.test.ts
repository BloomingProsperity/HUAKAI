import { describe, expect, it } from 'vitest'
import { fmtLatency, fmtTtft, recentModelDisplay, recentStatusTone } from './recentRequests'
import type { AccountRecentRequestItem } from './types'

function item(p: Partial<AccountRecentRequestItem>): AccountRecentRequestItem {
  return { at: '2026-07-12T00:00:00Z', model: 'gpt-5', upstream_model: null, status: 'success', latency_ms: 120, ttft_ms: null, tokens_in: 10, tokens_out: 20, stream: false, attempt_seq: 1, ...p }
}

describe('fmtLatency / fmtTtft', () => {
  it('毫秒取整显示', () => {
    expect(fmtLatency(120.7)).toBe('121 ms')
    expect(fmtLatency(0)).toBe('0 ms')
  })
  it('TTFT 为 null 显示占位,不当 0(变异:若 null→0 会误显 0 ms)', () => {
    expect(fmtTtft(null)).toBe('—')
    expect(fmtTtft(88)).toBe('88 ms')
  })
  it('latency 非法显示占位', () => {
    expect(fmtLatency(null)).toBe('—')
    expect(fmtLatency(NaN)).toBe('—')
  })
})

describe('recentStatusTone', () => {
  it('success→ok,其余→danger', () => {
    expect(recentStatusTone('success')).toBe('ok')
    expect(recentStatusTone('error')).toBe('danger')
  })
})

describe('recentModelDisplay', () => {
  it('上游模型不同时显示 req → upstream', () => {
    expect(recentModelDisplay(item({ model: 'gpt-5', upstream_model: 'gpt-5-2026' }))).toBe('gpt-5 → gpt-5-2026')
  })
  it('上游模型相同或缺失时只显示请求模型', () => {
    expect(recentModelDisplay(item({ model: 'gpt-5', upstream_model: 'gpt-5' }))).toBe('gpt-5')
    expect(recentModelDisplay(item({ model: 'gpt-5', upstream_model: null }))).toBe('gpt-5')
  })
})
