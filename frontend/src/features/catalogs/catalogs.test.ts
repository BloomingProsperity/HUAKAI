import { describe, expect, it } from 'vitest'
import {
  DEFAULT_FAILOVER_CODES,
  UPSTREAM_PROTOCOLS,
  buildCatalogQuery,
  formatFailoverCodes,
  isKnownProtocol,
  parseFailoverCodes,
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

describe('parseFailoverCodes', () => {
  it('空输入 → 空数组(后端回落默认)', () => {
    const r = parseFailoverCodes('   ')
    expect(r.ok && r.codes).toEqual([])
  })

  it('逗号/空白混合分隔解析,去重保序', () => {
    const r = parseFailoverCodes('401, 403 429,401')
    expect(r.ok && r.codes).toEqual([401, 403, 429])
  })

  it('超出 100~599 即拒', () => {
    // 判别核心:区间守卫(镜像后端 c<100||c>599)。变异(去掉区间检查)→ ok 变 true → RED。
    expect(parseFailoverCodes('99').ok).toBe(false)
    expect(parseFailoverCodes('600').ok).toBe(false)
    expect(parseFailoverCodes('100').ok).toBe(true)
    expect(parseFailoverCodes('599').ok).toBe(true)
  })

  it('非整数串即拒(防 Number 容忍 200.5 / 2e2 / 负号)', () => {
    expect(parseFailoverCodes('200.5').ok).toBe(false)
    expect(parseFailoverCodes('2e2').ok).toBe(false)
    expect(parseFailoverCodes('-401').ok).toBe(false)
    expect(parseFailoverCodes('abc').ok).toBe(false)
  })
})

describe('formatFailoverCodes', () => {
  it('数组 → 逗号分隔串;空/undefined → 空串', () => {
    expect(formatFailoverCodes([401, 429])).toBe('401, 429')
    expect(formatFailoverCodes([])).toBe('')
    expect(formatFailoverCodes(undefined)).toBe('')
  })

  it('默认码常量与后端一致', () => {
    expect([...DEFAULT_FAILOVER_CODES]).toEqual([401, 403, 429, 529])
  })
})

describe('validateChannel', () => {
  const base = { name: '主通道', poolGroupId: 1, failoverText: '', enabled: true, reason: '' }

  it('合法输入:failover 空则省略字段(后端回落默认)', () => {
    const v = validateChannel(base)
    expect(v.ok).toBe(true)
    if (v.ok) {
      expect(v.value).toEqual({ pool_group_id: 1, name: '主通道', enabled: true })
      // 判别核心:空 failover 不下发字段(交后端回落默认)。变异(总是下发)→ RED。
      expect('failover_status_codes' in v.value).toBe(false)
    }
  })

  it('failover 非空时下发解析后的数组', () => {
    const v = validateChannel({ ...base, failoverText: '401,500' })
    expect(v.ok && v.value.failover_status_codes).toEqual([401, 500])
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

  it('failover 文本非法即拒(透传 parseFailoverCodes 的错误)', () => {
    expect(validateChannel({ ...base, failoverText: '700' }).ok).toBe(false)
  })

  it('reason 非空时下发(trim)', () => {
    const v = validateChannel({ ...base, reason: '  调整  ' })
    expect(v.ok && v.value.reason).toBe('调整')
  })
})
