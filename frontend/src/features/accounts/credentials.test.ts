import { describe, expect, it } from 'vitest'
import {
  AUTH_MODE_OPTIONS,
  CREDENTIAL_STATES,
  VENDOR_OPTIONS,
  buildCreateBody,
  buildRotateBody,
  buildStateBody,
  credentialStateLabel,
  credentialStateTone,
  externalAccountLabel,
  fmtTime,
  isValidCredentialState,
  validateSecretJSON,
  validateVendorAuthMode,
} from './credentials'
import type { CredentialMetadata } from './credentials'

/*
 * 账号凭证纯逻辑单测(§14 变异法:每个断言能在对应缺陷被引入时变红)。
 * 重点护住 SECRET-MASK 与请求体校验:空/非对象 secret 必拒;state 未知值必拒;
 * tenant_id 非正必拒;构造出的 body 里 credentials 是嵌套对象(不是字符串)。
 */

describe('validateSecretJSON', () => {
  it('合法非空 JSON 对象通过', () => {
    expect(validateSecretJSON('{"api_key":"sk-x"}')).toBeNull()
  })
  it('空串 / 纯空白拒绝', () => {
    // 变异(删 trimmed===''守卫)→ 空串本应报错却放行,断言 RED。
    expect(validateSecretJSON('')).not.toBeNull()
    expect(validateSecretJSON('   ')).not.toBeNull()
  })
  it('非法 JSON 拒绝', () => {
    // 变异(去掉 try/catch 直接当合法)→ 此断言 RED。
    expect(validateSecretJSON('{not json')).not.toBeNull()
  })
  it('JSON 但非对象(数组/标量/null)拒绝', () => {
    // 判别核心:secret 必须是键值对对象;数组/标量会被后端 ValidatePayload 拒。
    // 变异(去掉 Array.isArray / typeof 检查)→ 这些本应拒却放行,断言 RED。
    expect(validateSecretJSON('[1,2,3]')).not.toBeNull()
    expect(validateSecretJSON('"sk-x"')).not.toBeNull()
    expect(validateSecretJSON('42')).not.toBeNull()
    expect(validateSecretJSON('null')).not.toBeNull()
  })
  it('空对象拒绝', () => {
    // 变异(去掉 keys.length===0 检查)→ {} 本应拒却放行,断言 RED。
    expect(validateSecretJSON('{}')).not.toBeNull()
  })
})

describe('validateVendorAuthMode', () => {
  it('两者非空才通过;任一空拒绝', () => {
    expect(validateVendorAuthMode('anthropic', 'api_key')).toBeNull()
    // 变异(删 vendor 空检查)→ 空 vendor 本应报错却放行,断言 RED。
    expect(validateVendorAuthMode('', 'api_key')).not.toBeNull()
    expect(validateVendorAuthMode('anthropic', '  ')).not.toBeNull()
  })
})

describe('isValidCredentialState', () => {
  it('已知态通过,未知态拒绝', () => {
    expect(isValidCredentialState('active')).toBe(true)
    expect(isValidCredentialState('revoked')).toBe(true)
    // 判别核心:未知态必须拒(避免发无效 PATCH)。变异(永真返回)→ 此断言 RED。
    expect(isValidCredentialState('totally_unknown')).toBe(false)
    expect(isValidCredentialState('')).toBe(false)
  })
  it('CREDENTIAL_STATES 与后端 8 个 State* 常量一一对齐', () => {
    expect([...CREDENTIAL_STATES].sort()).toEqual(
      [
        'active',
        'expired',
        'needs_rotation',
        'operator_attention',
        'refreshing',
        'refreshing_with_grace',
        'revoked',
        'temp_unschedulable',
      ].sort(),
    )
  })
})

describe('credentialStateTone / Label', () => {
  it('active→ok,refreshing→info,needs_rotation→warn,revoked→danger,未知→muted', () => {
    expect(credentialStateTone('active')).toBe('ok')
    expect(credentialStateTone('refreshing')).toBe('info')
    expect(credentialStateTone('refreshing_with_grace')).toBe('info')
    expect(credentialStateTone('needs_rotation')).toBe('warn')
    expect(credentialStateTone('temp_unschedulable')).toBe('warn')
    // 判别核心:已吊销/过期/需人工是危险态,不可与 active 同级。变异(归到 ok)→ RED。
    expect(credentialStateTone('revoked')).toBe('danger')
    expect(credentialStateTone('expired')).toBe('danger')
    expect(credentialStateTone('operator_attention')).toBe('danger')
    expect(credentialStateTone('weird')).toBe('muted')
  })
  it('给出中文标签,未知回退原值', () => {
    expect(credentialStateLabel('active')).toBe('活跃')
    expect(credentialStateLabel('revoked')).toBe('已吊销')
    expect(credentialStateLabel('weird')).toBe('weird')
    expect(credentialStateLabel('')).toBe('—')
  })
})

describe('buildCreateBody', () => {
  const base = {
    tenantId: 1,
    vendor: 'anthropic',
    authMode: 'api_key',
    secretJSON: '{"api_key":"sk-abc"}',
  }

  it('合法输入 → ok,credentials 为嵌套对象(非字符串)', () => {
    const r = buildCreateBody(base)
    expect(r.ok).toBe(true)
    if (r.ok) {
      expect(r.value.tenant_id).toBe(1)
      expect(r.value.vendor).toBe('anthropic')
      expect(r.value.auth_mode).toBe('api_key')
      // 判别核心(SECRET-MASK 出口形态):credentials 必须是对象,后端 json.RawMessage 期望对象。
      // 变异(直接塞 secretJSON 字符串)→ 这里 typeof 为 string,断言 RED。
      expect(typeof r.value.credentials).toBe('object')
      expect(r.value.credentials).toEqual({ api_key: 'sk-abc' })
      // 可选字段未给则不应出现。
      expect('external_account_id' in r.value).toBe(false)
      expect('reason' in r.value).toBe(false)
    }
  })

  it('tenant_id 非正 → 报错', () => {
    expect(buildCreateBody({ ...base, tenantId: 0 }).ok).toBe(false)
    expect(buildCreateBody({ ...base, tenantId: -1 }).ok).toBe(false)
  })

  it('空 secret / 非法 secret → 报错(不进入构造)', () => {
    // 变异(跳过 validateSecretJSON)→ 空 secret 本应拒却 ok,断言 RED。
    expect(buildCreateBody({ ...base, secretJSON: '' }).ok).toBe(false)
    expect(buildCreateBody({ ...base, secretJSON: '[1]' }).ok).toBe(false)
  })

  it('vendor/auth_mode 空 → 报错', () => {
    expect(buildCreateBody({ ...base, vendor: '' }).ok).toBe(false)
    expect(buildCreateBody({ ...base, authMode: '' }).ok).toBe(false)
  })

  it('external account / reason 给了才带,且 trim', () => {
    const r = buildCreateBody({
      ...base,
      externalAccountId: '  ext-1 ',
      externalAccountEmail: ' a@b.com ',
      reason: ' 手动导入 ',
    })
    expect(r.ok).toBe(true)
    if (r.ok) {
      expect(r.value.external_account_id).toBe('ext-1')
      expect(r.value.external_account_email).toBe('a@b.com')
      expect(r.value.reason).toBe('手动导入')
    }
  })

  it('external account 为纯空白 → 省略(不下发空串)', () => {
    const r = buildCreateBody({ ...base, externalAccountId: '   ' })
    expect(r.ok).toBe(true)
    // 变异(无条件赋值)→ 空白 external_account_id 混入,断言 RED。
    if (r.ok) expect('external_account_id' in r.value).toBe(false)
  })
})

describe('buildRotateBody', () => {
  it('合法 → ok,只含 tenant_id+credentials(+reason);credentials 为对象', () => {
    const r = buildRotateBody({ tenantId: 2, secretJSON: '{"access_token":"t"}' })
    expect(r.ok).toBe(true)
    if (r.ok) {
      expect(r.value.tenant_id).toBe(2)
      expect(r.value.credentials).toEqual({ access_token: 't' })
      // 轮换不带 vendor/auth_mode(后端沿用既有)。
      expect('vendor' in r.value).toBe(false)
      expect('auth_mode' in r.value).toBe(false)
    }
  })
  it('tenant_id 非正 / secret 非法 → 报错', () => {
    expect(buildRotateBody({ tenantId: 0, secretJSON: '{"a":1}' }).ok).toBe(false)
    expect(buildRotateBody({ tenantId: 2, secretJSON: '' }).ok).toBe(false)
  })
})

describe('buildStateBody', () => {
  it('合法 state → ok', () => {
    const r = buildStateBody({ tenantId: 1, state: 'revoked', reason: 'r' })
    expect(r.ok).toBe(true)
    if (r.ok) expect(r.value).toEqual({ tenant_id: 1, state: 'revoked', reason: 'r' })
  })
  it('未知 state → 报错', () => {
    // 判别核心:未知态必须拒。变异(不校验 state)→ ok,断言 RED。
    expect(buildStateBody({ tenantId: 1, state: 'bogus' }).ok).toBe(false)
  })
  it('tenant_id 非正 → 报错', () => {
    expect(buildStateBody({ tenantId: 0, state: 'active' }).ok).toBe(false)
  })
  it('reason 空白 → 省略', () => {
    const r = buildStateBody({ tenantId: 1, state: 'active', reason: '  ' })
    expect(r.ok).toBe(true)
    if (r.ok) expect('reason' in r.value).toBe(false)
  })
})

describe('externalAccountLabel', () => {
  const meta = (over: Partial<CredentialMetadata>): CredentialMetadata => ({
    id: 1,
    tenant_id: 1,
    provider_account_id: 1,
    vendor: 'anthropic',
    auth_mode: 'api_key',
    state: 'active',
    credential_version: 1,
    failure_count: 0,
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    ...over,
  })
  it('优先 email,其次 id,都无则破折号', () => {
    expect(externalAccountLabel(meta({ external_account_email: 'a@b.com', external_account_id: 'x' }))).toBe(
      'a@b.com',
    )
    // 判别核心:无 email 时才回退 id。变异(总用 id)→ 第一断言 RED。
    expect(externalAccountLabel(meta({ external_account_id: 'acct-9' }))).toBe('acct-9')
    expect(externalAccountLabel(meta({}))).toBe('—')
  })
})

describe('fmtTime', () => {
  it('空/无效返回破折号或原串,有效串可解析', () => {
    expect(fmtTime(null)).toBe('—')
    expect(fmtTime(undefined)).toBe('—')
    expect(fmtTime('not-a-date')).toBe('not-a-date')
    expect(fmtTime('2026-01-01T00:00:00Z')).not.toBe('—')
  })
})

describe('下拉选项完整性', () => {
  it('vendor / auth_mode 选项非空且 value 唯一', () => {
    expect(VENDOR_OPTIONS.length).toBeGreaterThan(0)
    expect(AUTH_MODE_OPTIONS.length).toBeGreaterThan(0)
    const vv = VENDOR_OPTIONS.map((o) => o.value)
    expect(new Set(vv).size).toBe(vv.length)
    const av = AUTH_MODE_OPTIONS.map((o) => o.value)
    expect(new Set(av).size).toBe(av.length)
  })
})
