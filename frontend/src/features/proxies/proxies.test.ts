import { describe, expect, it } from 'vitest'
import { parseTenantInput, probeSummary, statusTone, validateCreateForm, type CreateProxyForm } from './proxies'
import type { ProbeResult } from './types'

function form(p: Partial<CreateProxyForm>): CreateProxyForm {
  return { name: 'p1', protocol: 'http', host: '1.2.3.4', port: '8080', auth_username: '', auth_secret: '', status: 'active', ...p }
}

function result(p: Partial<ProbeResult>): ProbeResult {
  return { object: 'proxy_probe', ok: false, latency_ms: 0, probed_at: '2026-06-26T00:00:00Z', ...p }
}

describe('probeSummary', () => {
  it('ok=true → 连通 + 延迟 + tone=ok(变异:若忽略 ok 恒 fail 则此断言红)', () => {
    const s = probeSummary(result({ ok: true, latency_ms: 42 }))
    expect(s.tone).toBe('ok')
    expect(s.label).toContain('42')
    expect(s.label).toContain('连通')
  })

  it('各 error_class 映射到对应中文短语 + tone=fail(变异:若错配则短语对不上)', () => {
    expect(probeSummary(result({ ok: false, error_class: 'tls_fail' }))).toEqual({ label: 'TLS 握手失败', tone: 'fail' })
    expect(probeSummary(result({ ok: false, error_class: 'unsafe_proxy_host' })).label).toContain('内网')
    expect(probeSummary(result({ ok: false, error_class: 'dial_timeout' })).label).toContain('超时')
  })

  it('未知 error_class 兜底(不抛、含原始码便于排错)', () => {
    const s = probeSummary(result({ ok: false, error_class: 'weird_new_code' }))
    expect(s.tone).toBe('fail')
    expect(s.label).toContain('weird_new_code')
  })
})

describe('statusTone', () => {
  it('active=ok / dead=fail / 其余=muted(变异:若恒返回同值则三者至少错一个)', () => {
    expect(statusTone('active')).toBe('ok')
    expect(statusTone('dead')).toBe('fail')
    expect(statusTone('disabled')).toBe('muted')
  })
})

describe('parseTenantInput', () => {
  it('正整数取之,非法/非正回退默认 1', () => {
    expect(parseTenantInput('9')).toBe(9)
    expect(parseTenantInput('0')).toBe(1)
    expect(parseTenantInput('x')).toBe(1)
  })
})

describe('validateCreateForm', () => {
  it('合法表单 → null', () => {
    expect(validateCreateForm(form({}))).toBeNull()
    expect(validateCreateForm(form({ status: '' }))).toBeNull() // status 可空
    expect(validateCreateForm(form({ port: '65535' }))).toBeNull()
  })
  it('各非法输入返回对应错误(变异:任一校验缺失则该非法漏过返 null)', () => {
    expect(validateCreateForm(form({ name: '  ' }))).toContain('名称')
    expect(validateCreateForm(form({ protocol: 'ftp' }))).toContain('协议')
    expect(validateCreateForm(form({ host: '' }))).toContain('主机')
    expect(validateCreateForm(form({ port: '0' }))).toContain('端口')
    expect(validateCreateForm(form({ port: '70000' }))).toContain('端口')
    expect(validateCreateForm(form({ port: 'abc' }))).toContain('端口')
    expect(validateCreateForm(form({ status: 'weird' }))).toContain('状态')
  })
})
