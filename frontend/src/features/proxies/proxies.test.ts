import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { EditProxyForm as EditProxyFormComponent } from './EditProxyForm'
import {
  buildCreateInput,
  buildUpdateInput,
  mapProxyRows,
  parseTenantInput,
  probeSummary,
  statusTone,
  validateCreateForm,
  validateEditForm,
  validateProxyGroupID,
  type CreateProxyForm,
  type EditProxyForm,
} from './proxies'
import type { ProbeResult, Proxy } from './types'

function editForm(p: Partial<EditProxyForm>): EditProxyForm {
  return { name: 'p1', protocol: 'http', host: '1.2.3.4', port: '8080', auth_username: '', auth_secret: '', group_id: '', ...p }
}

function form(p: Partial<CreateProxyForm>): CreateProxyForm {
  return { name: 'p1', protocol: 'http', host: '1.2.3.4', port: '8080', auth_username: '', auth_secret: '', group_id: '', status: 'active', ...p }
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
    expect(validateCreateForm(form({ group_id: 'bad group!' }))).toContain('代理组')
  })
})

describe('validateProxyGroupID', () => {
  it('空值与合法边界通过，怪字符和 65 字符拒绝', () => {
    expect(validateProxyGroupID('')).toBeNull()
    expect(validateProxyGroupID('az_AZ-09')).toBeNull()
    expect(validateProxyGroupID('a'.repeat(64))).toBeNull()
    expect(validateProxyGroupID('bad group!')).toContain('代理组')
    expect(validateProxyGroupID('a'.repeat(65))).toContain('代理组')
  })
})

describe('buildCreateInput', () => {
  it('有组发送规格化组名，未分组显式发送 null', () => {
    expect(buildCreateInput(form({ name: ' p2 ', host: ' proxy.example ', group_id: ' us-residential ', auth_username: ' user ', auth_secret: 'secret' }))).toEqual({
      name: 'p2',
      protocol: 'http',
      host: 'proxy.example',
      port: 8080,
      auth_username: 'user',
      auth_secret: 'secret',
      group_id: 'us-residential',
      status: 'active',
    })
    expect(buildCreateInput(form({ group_id: '   ' }))).toEqual({
      name: 'p1',
      protocol: 'http',
      host: '1.2.3.4',
      port: 8080,
      auth_username: undefined,
      auth_secret: undefined,
      group_id: null,
      status: 'active',
    })
  })
})

describe('validateEditForm', () => {
  it('合法表单 → null', () => {
    expect(validateEditForm(editForm({}))).toBeNull()
    expect(validateEditForm(editForm({ auth_secret: '' }))).toBeNull() // 密钥可留空
  })
  it('各非法输入返回对应错误(变异:任一校验缺失则该非法漏过返 null)', () => {
    expect(validateEditForm(editForm({ name: '  ' }))).toContain('名称')
    expect(validateEditForm(editForm({ protocol: 'ftp' }))).toContain('协议')
    expect(validateEditForm(editForm({ host: '' }))).toContain('主机')
    expect(validateEditForm(editForm({ port: '0' }))).toContain('端口')
    expect(validateEditForm(editForm({ port: '70000' }))).toContain('端口')
  })
})

describe('buildUpdateInput', () => {
  it('必带字段齐全,port 转 int,name/host 去空白', () => {
    const out = buildUpdateInput(editForm({ name: ' p2 ', host: ' 5.6.7.8 ', port: '3128' }))
    expect(out).toMatchObject({ name: 'p2', host: '5.6.7.8', port: 3128, protocol: 'http' })
  })

  it('auth_secret 留空 → 不下发该字段(后端缺省即清除,语义≠保留;变异:无条件下发则此断言红)', () => {
    const out = buildUpdateInput(editForm({ auth_secret: '' }))
    expect('auth_secret' in out).toBe(false)
  })

  it('auth_secret 非空 → 下发(本次改密钥)', () => {
    const out = buildUpdateInput(editForm({ auth_secret: 'newpass' }))
    expect(out.auth_secret).toBe('newpass')
  })

  it('auth_username 空白 → undefined(可清空);非空去空白后下发', () => {
    expect(buildUpdateInput(editForm({ auth_username: '   ' })).auth_username).toBeUndefined()
    expect(buildUpdateInput(editForm({ auth_username: ' user ' })).auth_username).toBe('user')
  })

  it('请求体不含 status 字段(后端 DisallowUnknownFields,带 status 会 400)', () => {
    const out = buildUpdateInput(editForm({}))
    expect('status' in out).toBe(false)
  })

  it('代理组非空精确下发，留空显式下发 null 以清组', () => {
    expect(buildUpdateInput(editForm({ name: ' p2 ', host: ' proxy.example ', group_id: ' eu-egress ', auth_username: ' user ', auth_secret: 'secret' }))).toEqual({
      name: 'p2',
      protocol: 'http',
      host: 'proxy.example',
      port: 8080,
      auth_username: 'user',
      auth_secret: 'secret',
      group_id: 'eu-egress',
    })
    const cleared = buildUpdateInput(editForm({ group_id: '' }))
    expect(cleared).toEqual({
      name: 'p1',
      protocol: 'http',
      host: '1.2.3.4',
      port: 8080,
      auth_username: undefined,
      group_id: null,
    })
  })
})

describe('mapProxyRows', () => {
  it('完整映射代理表展示列并保留动作源对象', () => {
    const proxy: Proxy = { id: 4, name: '出口一', protocol: 'socks5', host: 'proxy.example', port: 1080, auth_username: null, group_id: 'residential-a', status: 'dead', last_check_at: null, created_at: '', updated_at: '' }
    // 判别核心:地址必须由 host 与 port 组合;错列或漏端口会转红。
    const row = mapProxyRows([proxy])[0]
    expect(row).toMatchObject({ id: 4, name: '出口一', protocol: 'socks5', address: 'proxy.example:1080', group: 'residential-a', status: 'dead' })
    expect(row.proxy).toBe(proxy)
  })

  it('null 组稳定展示为未分组', () => {
    const proxy: Proxy = { id: 5, name: '出口二', protocol: 'http', host: 'proxy.example', port: 3128, auth_username: null, group_id: null, status: 'active', last_check_at: null, created_at: '', updated_at: '' }
    expect(mapProxyRows([proxy])[0].group).toBe('未分组')
  })
})

describe('代理编辑表单 SSR', () => {
  it('渲染代理组输入与字符约束提示', () => {
    const proxy: Proxy = { id: 6, name: '出口三', protocol: 'http', host: 'proxy.example', port: 3128, auth_username: null, group_id: 'group-a', status: 'active', last_check_at: null, created_at: '', updated_at: '' }
    const html = renderToStaticMarkup(createElement(EditProxyFormComponent, {
      tenantId: 1,
      proxy,
      onSaved: () => undefined,
      onCancel: () => undefined,
    }))
    expect(html).toContain('name="group_id"')
    expect(html).toContain('仅限字母、数字、下划线、短横线')
    expect(html).toContain('留空保存会清除分组')
  })
})
