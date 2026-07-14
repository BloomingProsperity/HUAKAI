import { describe, expect, it } from 'vitest'
import {
  UPSTREAM_PROTOCOLS,
  buildCatalogQuery,
  isKnownProtocol,
  mapChannelCatalogRows,
  mapProviderCatalogRows,
  validateChannel,
  validateProviderCreate,
  validateProviderUpdate,
} from './catalogs'

describe('buildCatalogQuery', () => {
  it('tenant_id/limit/offset 必带', () => {
    const q = buildCatalogQuery(7, 100, 50)
    expect(q).toEqual({ tenant_id: 7, limit: 100, offset: 50 })
    // 判别核心:tenant_id 不可缺(后端 platform_admin 必填)。变异(漏 tenant_id)→ RED。
    expect('tenant_id' in q).toBe(true)
  })
})

describe('isKnownProtocol', () => {
  it('白名单内 true,白名单外 false,trim 后判定', () => {
    expect(isKnownProtocol('anthropic_messages')).toBe(true)
    expect(isKnownProtocol('  openai_chat  ')).toBe(true)
    // 判别核心:未知协议必须 false(镜像后端 invalid_upstream_protocol 400)。
    // 变异(恒 true)→ 此行 RED。
    expect(isKnownProtocol('totally_made_up')).toBe(false)
    expect(isKnownProtocol('')).toBe(false)
  })

  it('白名单覆盖 antigravity 与 session 族(后补族集对称)', () => {
    expect(isKnownProtocol('antigravity')).toBe(true)
    expect(isKnownProtocol('cursor_session')).toBe(true)
    expect(UPSTREAM_PROTOCOLS.length).toBeGreaterThan(30)
  })
})

describe('validateProviderCreate', () => {
  const base = { code: 'anthropic', displayName: 'Anthropic', upstreamProtocol: 'anthropic_messages', enabled: true, reason: '' }

  it('合法输入产出可提交请求体(reason 空则省略)', () => {
    const v = validateProviderCreate(base)
    expect(v.ok).toBe(true)
    if (v.ok) {
      expect(v.value).toEqual({ code: 'anthropic', display_name: 'Anthropic', upstream_protocol: 'anthropic_messages', enabled: true })
      // 判别核心:reason 空串不得出现在请求体。变异(无条件塞 reason)→ RED。
      expect('reason' in v.value).toBe(false)
    }
  })

  it('reason 非空时下发(trim 后)', () => {
    const v = validateProviderCreate({ ...base, reason: '  接入新供应商  ' })
    expect(v.ok && v.value.reason).toBe('接入新供应商')
  })

  it('code 空(trim 后)即拒', () => {
    // 判别核心:code 必填。变异(跳过 code 校验)→ ok 变 true → RED。
    expect(validateProviderCreate({ ...base, code: '   ' }).ok).toBe(false)
  })

  it('display_name 空即拒', () => {
    expect(validateProviderCreate({ ...base, displayName: '' }).ok).toBe(false)
  })

  it('协议不在白名单即拒', () => {
    // 判别核心:协议白名单守卫。变异(去掉 isKnownProtocol 检查)→ ok 变 true → RED。
    expect(validateProviderCreate({ ...base, upstreamProtocol: 'bogus_proto' }).ok).toBe(false)
  })
})

describe('validateProviderUpdate', () => {
  it('更新不校验 code,只校验 display_name + 协议', () => {
    const v = validateProviderUpdate({ displayName: 'New Name', upstreamProtocol: 'openai_chat', enabled: false, reason: '' })
    expect(v.ok).toBe(true)
    if (v.ok) {
      // 判别核心:更新请求体不含 code(code 走 URL path)。变异(塞入 code)→ RED。
      expect('code' in v.value).toBe(false)
      expect(v.value).toEqual({ display_name: 'New Name', upstream_protocol: 'openai_chat', enabled: false })
    }
  })

  it('display_name 空即拒', () => {
    expect(validateProviderUpdate({ displayName: ' ', upstreamProtocol: 'openai_chat', enabled: true, reason: '' }).ok).toBe(false)
  })
})

describe('validateChannel', () => {
  const base = { name: '主通道', poolGroupId: 1, enabled: true, reason: '' }

  it('合法输入只提交现存有效字段,不夹带 failover_status_codes', () => {
    const v = validateChannel(base)
    expect(v.ok).toBe(true)
    if (v.ok) {
      expect(v.value).toEqual({ pool_group_id: 1, name: '主通道', enabled: true })
      // 判别核心:当前界面永不下发仅存储字段。变异(重新塞入该字段)→ RED。
      expect('failover_status_codes' in v.value).toBe(false)
    }
  })

  it('name 空即拒', () => {
    expect(validateChannel({ ...base, name: '  ' }).ok).toBe(false)
  })

  it('pool_group_id 非正即拒', () => {
    // 判别核心:pool_group_id 必须为正整数(后端 *PoolGroupID<=0 即 400)。变异(去掉守卫)→ RED。
    expect(validateChannel({ ...base, poolGroupId: 0 }).ok).toBe(false)
    expect(validateChannel({ ...base, poolGroupId: -3 }).ok).toBe(false)
    expect(validateChannel({ ...base, poolGroupId: 1.5 }).ok).toBe(false)
  })

  it('reason 非空时下发(trim)', () => {
    const v = validateChannel({ ...base, reason: '  调整  ' })
    expect(v.ok && v.value.reason).toBe('调整')
  })
})

describe('目录表格列映射', () => {
  it('provider 映射保留动作源对象并生成状态语义', () => {
    const provider = { id: 3, code: 'anthropic', display_name: 'Anthropic', upstream_protocol: 'anthropic_messages', enabled: false, created_at: 'bad-date' }
    const [row] = mapProviderCatalogRows([provider])
    expect(row).toMatchObject({ id: 3, code: 'anthropic', displayName: 'Anthropic', status: '停用', statusTone: 'muted', createdAt: 'bad-date' })
    // 判别核心:动作必须收到原 DTO；变异(复制或遗漏源对象)会使引用断开并证红。
    expect(row.provider).toBe(provider)
  })

  it('channel 映射忽略旧响应中的失败转移码并保留有效列', () => {
    const channel = { id: 9, pool_group_id: 4, name: '主通道', failover_status_codes: [401, 429], enabled: true, created_at: undefined }
    const [row] = mapChannelCatalogRows([channel])
    expect(row).toMatchObject({ displayId: '#9', poolGroupId: 4, status: '启用', statusTone: 'ok', createdAt: '—' })
    // 判别核心:把仅存储字段重新映射进列表行会立即转红。
    expect('failoverCodes' in row).toBe(false)
    expect(row.channel).toBe(channel)
  })
})
