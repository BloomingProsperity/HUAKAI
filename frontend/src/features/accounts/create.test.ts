import { describe, expect, it } from 'vitest'
import {
  assembleCredentials,
  buildCreateRequest,
  EMPTY_CREATE_FORM,
  validateCreateForm,
  type CreateAccountForm,
} from './create'
import type { AccountMode, FieldSpec } from './createTypes'

function field(name: string, required = true, one_of_group?: string): FieldSpec {
  return { name, kind: 'secret', required, one_of_group, redaction: 'secret', group: 'credential' }
}

const mode: AccountMode = {
  vendor: 'anthropic',
  auth_mode: 'api_key',
  flow_kind: 'manual',
  client_identity_source: '',
  manual_first: true,
  long_lived_toggle: false,
  allowed_helpers: [],
  required_fields: [field('api_key'), field('base_url', false)],
  is_enabled: true,
  is_experimental: false,
  feature_flag: '',
  risk_level: 'low',
  risk_reasons: [],
}

function form(over: Partial<CreateAccountForm>): CreateAccountForm {
  return { ...EMPTY_CREATE_FORM, ...over }
}

describe('assembleCredentials', () => {
  it('只收非空值,去空白,空字段省略(防把空可选塞成空串)', () => {
    const creds = assembleCredentials(mode.required_fields, { api_key: '  sk-123 ', base_url: '   ' })
    expect(creds).toEqual({ api_key: 'sk-123' })
    // 判别核心:base_url 是空白 → 必须不在结果里。变异(收集空值)→ RED。
    expect('base_url' in creds).toBe(false)
  })
})

describe('buildCreateRequest', () => {
  it('选模式时带 vendor/auth_mode + 凭据;空可选参数省略', () => {
    const req = buildCreateRequest(
      form({ providerId: '3', channelId: '7', name: ' 主号 ', accountType: 'api_key', credentialValues: { api_key: 'sk-x' } }),
      mode,
      false,
    )
    expect(req.provider_id).toBe(3)
    expect(req.channel_id).toBe(7)
    expect(req.name).toBe('主号')
    expect(req.vendor).toBe('anthropic')
    expect(req.auth_mode).toBe('api_key')
    expect(req.credentials).toEqual({ api_key: 'sk-x' })
    // 空可选参数必须省略(后端对空/非正会 400)。
    expect(req.priority).toBeUndefined()
    expect(req.cap_concurrency).toBeUndefined()
    expect(req.confirm).toBeUndefined()
  })

  it('confirm=true 时带 confirm(混合风险二次提交)', () => {
    const req = buildCreateRequest(form({ providerId: '1', channelId: '1', name: 'a', accountType: 'api_key', credentialValues: { api_key: 'k' } }), mode, true)
    expect(req.confirm).toBe(true)
  })

  it('正参数带上,非正/非整省略', () => {
    const req = buildCreateRequest(form({ providerId: '1', channelId: '1', name: 'a', accountType: 'api_key', priority: '5', staticWeight: '0', capConcurrency: 'x', credentialValues: { api_key: 'k' } }), mode, false)
    expect(req.priority).toBe(5)
    expect(req.static_weight).toBeUndefined() // 0 非正 → 省略
    expect(req.cap_concurrency).toBeUndefined() // 非整 → 省略
  })
})

describe('validateCreateForm', () => {
  it('必填缺失逐项报错', () => {
    expect(validateCreateForm(form({}), null)).toContain('provider')
    expect(validateCreateForm(form({ providerId: '1' }), null)).toContain('channel')
    expect(validateCreateForm(form({ providerId: '1', channelId: '1' }), null)).toContain('名称')
    expect(validateCreateForm(form({ providerId: '1', channelId: '1', name: 'a' }), null)).toContain('账号类型')
  })

  it('选模式时必填凭据字段缺失报错', () => {
    const f = form({ providerId: '1', channelId: '1', name: 'a', accountType: 'api_key' })
    expect(validateCreateForm(f, mode)).toContain('api_key')
  })

  it('齐全则通过', () => {
    const f = form({ providerId: '1', channelId: '1', name: 'a', accountType: 'api_key', credentialValues: { api_key: 'k' } })
    expect(validateCreateForm(f, mode)).toBeNull()
  })
})
