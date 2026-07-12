import { describe, expect, it } from 'vitest'
import { buildSnippets, keyPlaceholder } from './snippets'

describe('buildSnippets', () => {
  it('三个客户端片段:Claude Code / OpenAI SDK / curl', () => {
    const s = buildSnippets('https://gw.example/v1', 'hk_abc')
    expect(s.map((x) => x.id)).toEqual(['claude-code', 'openai-sdk', 'curl'])
  })

  it('填入真实 Key 后片段含该 Key,不含占位符', () => {
    const s = buildSnippets('https://gw.example/v1', 'hk_secret')
    expect(s.every((x) => x.body.includes('hk_secret'))).toBe(true)
    expect(s.some((x) => x.body.includes(keyPlaceholder))).toBe(false)
  })

  it('Key 留空用占位符(变异:若不填占位会漏 Key 位)', () => {
    const s = buildSnippets('https://gw.example/v1', '')
    expect(s.every((x) => x.body.includes(keyPlaceholder))).toBe(true)
  })

  it('Claude Code 的 ANTHROPIC_BASE_URL 去掉尾部 /v1(它自带版本路径)', () => {
    const cc = buildSnippets('https://gw.example/v1', 'k').find((x) => x.id === 'claude-code')!
    expect(cc.body).toContain('ANTHROPIC_BASE_URL="https://gw.example"')
    expect(cc.body).not.toContain('gw.example/v1"')
  })

  it('OpenAI base_url 用完整地址(含 /v1),尾部斜杠归一', () => {
    const o = buildSnippets('https://gw.example/v1/', 'k').find((x) => x.id === 'openai-sdk')!
    expect(o.body).toContain('base_url="https://gw.example/v1"')
  })

  it('地址留空用占位地址', () => {
    const s = buildSnippets('', 'k')
    expect(s.some((x) => x.body.includes('你的网关地址'))).toBe(true)
  })
})
