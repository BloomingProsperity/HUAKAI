import { describe, expect, it } from 'vitest'
import {
  buildConnectLink,
  buildIntegrations,
  normalizeOrigin,
  relayBaseFor,
} from './integrations'

describe('normalizeOrigin', () => {
  it('去掉末尾斜杠', () => {
    expect(normalizeOrigin('https://relay.example.com/')).toBe('https://relay.example.com')
    expect(normalizeOrigin('https://relay.example.com///')).toBe('https://relay.example.com')
  })
  it('无斜杠原样返回', () => {
    expect(normalizeOrigin('https://relay.example.com')).toBe('https://relay.example.com')
  })
})

describe('relayBaseFor', () => {
  const origin = 'https://relay.example.com'
  it('Claude Code 用根地址,不追加 /v1(它自己追加 /v1/messages)', () => {
    const base = relayBaseFor(origin, 'claude-code')
    expect(base).toBe('https://relay.example.com')
    // 正确性锚点:claude-code base 末尾绝不能带 /v1
    expect(base.endsWith('/v1')).toBe(false)
  })
  it('OpenAI 兼容客户端 base 追加 /v1', () => {
    expect(relayBaseFor(origin, 'openai')).toBe('https://relay.example.com/v1')
  })
  it('先归一化再拼,末尾斜杠不会产生 //v1', () => {
    expect(relayBaseFor('https://relay.example.com/', 'openai')).toBe('https://relay.example.com/v1')
  })
})

describe('buildConnectLink', () => {
  it('拼出 huakai://connect 深链并百分号编码各参数', () => {
    const link = buildConnectLink({
      endpoint: 'https://relay.example.com',
      token: 'hk_live_abc123',
      name: '生产主key',
      client: 'claude-code',
    })
    expect(link.startsWith('huakai://connect?')).toBe(true)
    const q = new URLSearchParams(link.split('?')[1])
    expect(q.get('endpoint')).toBe('https://relay.example.com')
    expect(q.get('token')).toBe('hk_live_abc123')
    expect(q.get('name')).toBe('生产主key')
    expect(q.get('client')).toBe('claude-code')
  })
  it('中文名/特殊字符被编码(原文不直接出现在 URL 串里)', () => {
    const link = buildConnectLink({
      endpoint: 'https://relay.example.com/v1',
      token: 'hk_live_x',
      name: '主 key & 测试',
      client: 'openai',
    })
    // 空格与 & 必须被编码,不能裸出现破坏 query 结构
    expect(link).not.toContain('主 key & 测试')
    const q = new URLSearchParams(link.split('?')[1])
    expect(q.get('name')).toBe('主 key & 测试')
  })
})

describe('buildIntegrations', () => {
  const integ = buildIntegrations('https://relay.example.com/', 'hk_live_secret', '生产主key')
  const claude = integ.find((i) => i.id === 'claude-code')!
  const openai = integ.find((i) => i.id === 'openai')!

  it('产出 claude-code 与 openai 两套配置', () => {
    expect(integ.map((i) => i.id).sort()).toEqual(['claude-code', 'openai'])
  })

  it('Claude Code:BASE_URL=根地址(无 /v1)、AUTH_TOKEN=明文且标 secret', () => {
    const baseField = claude.fields.find((f) => f.label === 'ANTHROPIC_BASE_URL')!
    const tokenField = claude.fields.find((f) => f.label === 'ANTHROPIC_AUTH_TOKEN')!
    expect(baseField.value).toBe('https://relay.example.com')
    expect(baseField.value.endsWith('/v1')).toBe(false)
    expect(tokenField.value).toBe('hk_live_secret')
    expect(tokenField.secret).toBe(true)
  })

  it('Claude Code 提示强调用 AUTH_TOKEN 不用 API_KEY(避 x-api-key 401 坑)', () => {
    expect(claude.hint).toContain('ANTHROPIC_AUTH_TOKEN')
    expect(claude.hint).toContain('401')
  })

  it('Claude Code snippet 含 export 两行 + claude,且 BASE_URL 无 /v1', () => {
    expect(claude.snippet).toContain('export ANTHROPIC_BASE_URL="https://relay.example.com"')
    expect(claude.snippet).toContain('export ANTHROPIC_AUTH_TOKEN="hk_live_secret"')
    expect(claude.snippet).not.toContain('/v1/messages')
    expect(claude.snippet.trim().endsWith('claude')).toBe(true)
  })

  it('OpenAI:Base URL 带 /v1、API Key=明文且标 secret', () => {
    const baseField = openai.fields.find((f) => f.label === 'Base URL')!
    const keyField = openai.fields.find((f) => f.label === 'API Key')!
    expect(baseField.value).toBe('https://relay.example.com/v1')
    expect(keyField.value).toBe('hk_live_secret')
    expect(keyField.secret).toBe(true)
  })

  it('OpenAI snippet 用 Authorization: Bearer 带明文 + /v1/chat/completions', () => {
    expect(openai.snippet).toContain('Authorization: Bearer hk_live_secret')
    expect(openai.snippet).toContain('https://relay.example.com/v1/chat/completions')
  })

  it('每套都带 huakai://connect 深链且 token 编码在内', () => {
    for (const i of integ) {
      expect(i.deepLink.startsWith('huakai://connect?')).toBe(true)
      const q = new URLSearchParams(i.deepLink.split('?')[1])
      expect(q.get('token')).toBe('hk_live_secret')
      expect(q.get('client')).toBe(i.id)
    }
  })

  it('空 key 名回退为 HUAKAI(深链 name 不为空)', () => {
    const fallback = buildIntegrations('https://r.example.com', 'hk_live_y', '   ')
    const q = new URLSearchParams(fallback[0].deepLink.split('?')[1])
    expect(q.get('name')).toBe('HUAKAI')
  })
})
