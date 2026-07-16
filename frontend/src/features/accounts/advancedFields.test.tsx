import { readFileSync } from 'node:fs'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { AccountAdvancedSettings } from './AccountAdvancedSettings'
import {
  ACCOUNT_ADVANCED_FIELD_SPECS,
  advancedFormFromAccount,
  buildAdvancedCreate,
  buildAdvancedUpdate,
  emptyAdvancedForm,
  type AccountAdvancedFormState,
} from './advancedFields'
import type { ProviderAccount } from './types'

function account(overrides: Partial<ProviderAccount> = {}): ProviderAccount {
  return {
    id: 10,
    tenant_id: 7,
    provider_id: 8,
    channel_id: 9,
    name: 'acct',
    account_type: 'api_key',
    enabled: true,
    expires_at: '2026-09-01T00:00:00Z',
    rpm_limit: 10,
    tpm_limit: 2000,
    window_cost_limit_cents: 300,
    max_sessions: 4,
    disable_cooling: true,
    refresh_lead_seconds: 60,
    tls_fingerprint_rotate: true,
    health_state: 'operational',
    credential_state: 'valid',
    cap_concurrency: 4,
    in_flight_count: 0,
    priority: 100,
    static_weight: 1,
    probe_model: null,
    tags: [],
    extra: {},
    last_dispatch_at: null,
    last_probe_latency_ms: null,
    last_probe_at: null,
    model_allow_list: [],
    capability_flags: [],
    rate_limited_at: null,
    rate_limit_reset_at: null,
    rate_limit_reason: null,
    overload_until: null,
    temp_unschedulable_until: null,
    token_version: 1,
    last_refresh_at: null,
    last_refresh_outcome: null,
    custom_error_codes_enabled: true,
    custom_error_codes: [429],
    pool_mode: true,
    temp_unschedulable_enabled: true,
    temp_unschedulable_rules: [{ error_code: 529, keywords: ['busy'], duration_minutes: 5 }],
    proxy_id: 12,
    proxy_group_id: null,
    proxy_binding: { mode: 'proxy', proxy_id: 12 },
    ...overrides,
  }
}

describe('账号高级字段静态 mirror', () => {
  it('与后端唯一规格逐项一致', () => {
    const backend = JSON.parse(
      readFileSync(
        new URL('../../../../backend/internal/gatewayhttp/accountadvanced/fields.json', import.meta.url),
        'utf8',
      ),
    )
    expect(ACCOUNT_ADVANCED_FIELD_SPECS).toEqual(backend)
  })

  it('create/edit 都从清单渲染全部字段且每个 key 只出现一次', () => {
    const noop = <K extends keyof AccountAdvancedFormState>(_key: K, _value: AccountAdvancedFormState[K]) => undefined
    const createHTML = renderToStaticMarkup(
      <AccountAdvancedSettings mode="create" tenantId={null} form={emptyAdvancedForm()} onChange={noop} defaultOpen />,
    )
    const current = account()
    const editHTML = renderToStaticMarkup(
      <AccountAdvancedSettings mode="edit" tenantId={null} form={advancedFormFromAccount(current)} onChange={noop} current={current} defaultOpen />,
    )
    for (const spec of ACCOUNT_ADVANCED_FIELD_SPECS) {
      const marker = `data-advanced-field="${spec.key}"`
      expect(createHTML.split(marker)).toHaveLength(2)
      expect(editHTML.split(marker)).toHaveLength(2)
    }
  })
})

describe('账号高级字段 payload', () => {
  it('create 精确提交全部设置值，显式 0/false 语义不靠 truthy 丢失', () => {
    const form: AccountAdvancedFormState = {
      ...emptyAdvancedForm(),
      rpmLimit: '0',
      tpmLimit: '1200',
      windowCostLimitCents: '345',
      maxSessions: '6',
      disableCooling: true,
      refreshLeadMode: 'value',
      refreshLeadSeconds: '90',
      expiresAtMode: 'value',
      expiresAt: '2026-08-01T00:00:00Z',
      tlsFingerprintRotate: true,
      customErrorCodesEnabled: true,
      customErrorCodes: '429, 529',
      poolMode: 'enabled',
      tempUnschedulableEnabled: true,
      tempRulesMode: 'replace',
      tempUnschedulableRules: [{ errorCode: '529', keywords: 'busy', durationMinutes: '5', description: '拥塞' }],
      proxyMode: 'proxy',
      proxyId: '77',
    }
    expect(buildAdvancedCreate(form)).toEqual({
      rpm_limit: 0,
      tpm_limit: 1200,
      window_cost_limit_cents: 345,
      max_sessions: 6,
      disable_cooling: true,
      refresh_lead_seconds: 90,
      expires_at: '2026-08-01T00:00:00.000Z',
      tls_fingerprint_rotate: true,
      custom_error_codes_enabled: true,
      custom_error_codes: [429, 529],
      pool_mode: true,
      temp_unschedulable_enabled: true,
      temp_unschedulable_rules: [{ error_code: 529, keywords: ['busy'], duration_minutes: 5, description: '拥塞' }],
      proxy_binding: { mode: 'proxy', proxy_id: 77 },
    })
  })

  it('edit 只提交改动与显式 clear，不误带其它字段', () => {
    const original = account()
    const form: AccountAdvancedFormState = {
      ...advancedFormFromAccount(original),
      rpmLimit: '0',
      disableCooling: false,
      refreshLeadMode: 'clear',
      expiresAtMode: 'clear',
      customErrorCodes: '',
      poolMode: 'disabled',
      tempRulesMode: 'replace',
      tempUnschedulableRules: [],
      proxyMode: 'direct',
    }
    expect(buildAdvancedUpdate(original, form)).toEqual({
      rpm_limit: 0,
      disable_cooling: false,
      refresh_lead_seconds: null,
      expires_at: null,
      custom_error_codes: [],
      pool_mode: false,
      temp_unschedulable_rules: [],
      proxy_binding: { mode: 'direct' },
    })
  })

  it('拒绝负数、int32 越界和 JavaScript 非安全 int64', () => {
    expect(buildAdvancedCreate({ ...emptyAdvancedForm(), rpmLimit: '-1' })).toEqual({
      error: '每分钟请求上限须为 0 到 9007199254740991 的安全整数',
    })
    expect(buildAdvancedCreate({ ...emptyAdvancedForm(), maxSessions: '2147483648' })).toEqual({
      error: '最大并行会话数须为 0 到 2147483647 的安全整数',
    })
    expect(buildAdvancedCreate({ ...emptyAdvancedForm(), rpmLimit: '9007199254740992' })).toEqual({
      error: '每分钟请求上限须为 0 到 9007199254740991 的安全整数',
    })
  })
})
