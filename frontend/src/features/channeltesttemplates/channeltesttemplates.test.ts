import { describe, expect, it } from 'vitest'
import {
  buildListQuery,
  buildTenantQuery,
  headerCount,
  headersToText,
  isAllowedMethod,
  isCredentialHeaderName,
  mapChannelTemplateRows,
  parseHeaders,
  templateToForm,
  validateForm,
} from './channeltesttemplates'
import type { ChannelTestTemplate, TemplateForm } from './types'

/*
 * §14 变异测试:每个用例都带「判别核心」,说明它打红哪类缺陷。
 * 镜像后端约束(channel_test_template_handler.go:300-352),前端校验不得比后端松。
 */

describe('buildListQuery', () => {
  it('tenant_id/limit/offset 必带且原样', () => {
    expect(buildListQuery(7, 50, 100)).toEqual({ tenant_id: 7, limit: 50, offset: 100 })
  })
})

describe('buildTenantQuery', () => {
  it('只带 tenant_id', () => {
    expect(buildTenantQuery(9)).toEqual({ tenant_id: 9 })
  })
})

describe('isAllowedMethod', () => {
  it('白名单 5 方法(大小写不敏感)通过', () => {
    expect(isAllowedMethod('GET')).toBe(true)
    expect(isAllowedMethod('post')).toBe(true)
    expect(isAllowedMethod(' put ')).toBe(true)
    expect(isAllowedMethod('PATCH')).toBe(true)
    expect(isAllowedMethod('DELETE')).toBe(true)
  })
  it('白名单外一律拒(判别核心:不能放行 HEAD/OPTIONS/空)', () => {
    expect(isAllowedMethod('HEAD')).toBe(false)
    expect(isAllowedMethod('OPTIONS')).toBe(false)
    expect(isAllowedMethod('')).toBe(false)
  })
})

describe('isCredentialHeaderName', () => {
  it('凭证类 header 名命中(大小写不敏感),含全部 6 个', () => {
    // 判别核心:任一凭证 header 名漏判都会让密钥写入模板,必须 RED。
    for (const n of [
      'authorization',
      'Authorization',
      'PROXY-AUTHORIZATION',
      'Cookie',
      'x-api-key',
      'API-Key',
      'X-Auth-Token',
    ]) {
      expect(isCredentialHeaderName(n)).toBe(true)
    }
  })
  it('普通 header 名不命中', () => {
    expect(isCredentialHeaderName('X-Trace')).toBe(false)
    expect(isCredentialHeaderName('Accept')).toBe(false)
    expect(isCredentialHeaderName('Content-Type')).toBe(false)
  })
})

describe('parseHeaders', () => {
  it('空白 → 空对象', () => {
    expect(parseHeaders('')).toEqual({ ok: true, value: {} })
    expect(parseHeaders('   ')).toEqual({ ok: true, value: {} })
  })
  it('合法 JSON 对象通过', () => {
    const r = parseHeaders('{"X-Foo":"bar"}')
    expect(r).toEqual({ ok: true, value: { 'X-Foo': 'bar' } })
  })
  it('非法 JSON 拒(判别核心:坏 JSON 不能当空对象放行)', () => {
    expect(parseHeaders('{not json').ok).toBe(false)
  })
  it('数组 / null / 标量都拒(判别核心:后端 unmarshal 到 map 会失败)', () => {
    expect(parseHeaders('[1,2]').ok).toBe(false)
    expect(parseHeaders('null').ok).toBe(false)
    expect(parseHeaders('"x"').ok).toBe(false)
    expect(parseHeaders('42').ok).toBe(false)
  })
  it('含凭证 header 名拒(判别核心:authorization 必须被前端拦下)', () => {
    const r = parseHeaders('{"Authorization":"Bearer secret"}')
    expect(r.ok).toBe(false)
  })
})

describe('validateForm', () => {
  const base: TemplateForm = {
    name: '探测',
    method: 'GET',
    path: '/v1/messages',
    bodyTemplate: '',
    headersText: '',
  }

  it('合法表单返回归一化请求体(method 大写、headers 解析成对象)', () => {
    const r = validateForm({ ...base, method: 'post', headersText: '{"X-Trace":"1"}' })
    expect(r.ok).toBe(true)
    if (r.ok) {
      expect(r.value).toEqual({
        name: '探测',
        method: 'POST',
        path: '/v1/messages',
        body_template: '',
        headers: { 'X-Trace': '1' },
      })
    }
  })

  it('名称空白拒', () => {
    expect(validateForm({ ...base, name: '   ' }).ok).toBe(false)
  })

  it('名称超 128 拒(判别核心:边界 129 必须 RED,128 放行)', () => {
    expect(validateForm({ ...base, name: 'a'.repeat(129) }).ok).toBe(false)
    expect(validateForm({ ...base, name: 'a'.repeat(128) }).ok).toBe(true)
  })

  it('方法不在白名单拒', () => {
    expect(validateForm({ ...base, method: 'HEAD' }).ok).toBe(false)
  })

  it('路径必须以 / 开头(判别核心:相对路径不能放行)', () => {
    expect(validateForm({ ...base, path: 'v1/messages' }).ok).toBe(false)
    expect(validateForm({ ...base, path: '' }).ok).toBe(false)
    expect(validateForm({ ...base, path: '/' }).ok).toBe(true)
  })

  it('路径超 2048 拒', () => {
    expect(validateForm({ ...base, path: '/' + 'a'.repeat(2048) }).ok).toBe(false)
  })

  it('请求头含凭证 header 名拒(判别核心:密钥不得写入模板)', () => {
    expect(validateForm({ ...base, headersText: '{"x-api-key":"sk-1"}' }).ok).toBe(false)
  })

  it('body_template 原样透传不 trim(判别核心:不得吞掉空白内容)', () => {
    const r = validateForm({ ...base, bodyTemplate: '  {"a":1}  ' })
    expect(r.ok).toBe(true)
    if (r.ok) expect(r.value.body_template).toBe('  {"a":1}  ')
  })
})

describe('headersToText / headerCount / templateToForm', () => {
  it('空对象渲染成空串(便于占位提示)', () => {
    expect(headersToText({})).toBe('')
    expect(headersToText(null)).toBe('')
  })
  it('非空对象渲染成缩进 JSON', () => {
    expect(headersToText({ A: '1' })).toBe('{\n  "A": "1"\n}')
  })
  it('headerCount 统计键数', () => {
    expect(headerCount({ A: 1, B: 2 })).toBe(2)
    expect(headerCount(null)).toBe(0)
  })
  it('templateToForm 拍平 DTO(判别核心:headers 转回文本、字段不错位)', () => {
    const t: ChannelTestTemplate = {
      id: 5,
      tenant_id: 1,
      name: 'n',
      method: 'PUT',
      path: '/p',
      body_template: 'b',
      headers: { 'X-A': 'v' },
      created_at: '2026-06-29T00:00:00Z',
    }
    expect(templateToForm(t)).toEqual({
      name: 'n',
      method: 'PUT',
      path: '/p',
      bodyTemplate: 'b',
      headersText: '{\n  "X-A": "v"\n}',
    })
  })
})

describe('mapChannelTemplateRows', () => {
  it('完整映射模板列表列(删请求头数/请求体/时间任一映射→红)', () => {
    const template: ChannelTestTemplate = {
      id: 6, tenant_id: 2, name: '探测', method: 'POST', path: '/v1/test',
      body_template: '{}', headers: { A: '1', B: '2' }, created_at: 'bad-date',
    }
    expect(mapChannelTemplateRows([template])[0]).toMatchObject({
      id: 6, name: '探测', method: 'POST', path: '/v1/test', headerCount: 2,
      body: '有', createdAt: 'bad-date',
    })
  })
})
