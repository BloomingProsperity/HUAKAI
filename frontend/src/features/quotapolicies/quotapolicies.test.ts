import { describe, expect, it } from 'vitest'
import {
  WINDOW_KINDS,
  buildListQuery,
  emptyPolicyForm,
  formatDecimal,
  isEnforce,
  isRFC3339,
  mapQuotaPolicyRows,
  metricLabel,
  modeLabel,
  modeTone,
  policyToForm,
  scopeKindLabel,
  validatePolicyForm,
  windowKindLabel,
} from './quotapolicies'
import { EMPTY_FILTERS, type PolicyFilters, type PolicyForm, type QuotaPolicy, type WindowKind } from './types'

// ── buildListQuery ───────────────────────────────────────────────────────────
describe('buildListQuery', () => {
  it('空筛选只带 limit/offset;tenant_id>0 才下发', () => {
    const q = buildListQuery(7, EMPTY_FILTERS, 50, 100)
    expect(q).toEqual({ tenant_id: 7, limit: 50, offset: 100 })
  })

  it('tenant_id<=0 一律省略(operator 自身作用域)', () => {
    // 判别核心:tenant_id=0 不得进 query。变异(无条件赋值/改成 >=0)→ 含 tenant_id=0→RED。
    expect('tenant_id' in buildListQuery(0, EMPTY_FILTERS, 50, 0)).toBe(false)
    expect('tenant_id' in buildListQuery(-1, EMPTY_FILTERS, 50, 0)).toBe(false)
  })

  it('scope_kind/metric 空串省略、非空下发', () => {
    const filters: PolicyFilters = { scopeKind: 'api_key', metric: 'cost_usd', enabled: '' }
    const q = buildListQuery(7, filters, 50, 0)
    expect(q.scope_kind).toBe('api_key')
    expect(q.metric).toBe('cost_usd')
    // 判别核心:enabled='' 不得出现。变异(无条件赋值)→ 含 enabled→RED。
    expect('enabled' in q).toBe(false)
  })

  it('enabled 三态:true/false 下发,空省略', () => {
    expect(buildListQuery(7, { ...EMPTY_FILTERS, enabled: 'true' }, 50, 0).enabled).toBe('true')
    expect(buildListQuery(7, { ...EMPTY_FILTERS, enabled: 'false' }, 50, 0).enabled).toBe('false')
    expect('enabled' in buildListQuery(7, { ...EMPTY_FILTERS, enabled: '' }, 50, 0)).toBe(false)
  })
})

// ── 标签 / 语气 ──────────────────────────────────────────────────────────────
describe('枚举中文标签', () => {
  it('scope_kind/metric/window_kind/mode 给中文,未知回退原值', () => {
    expect(scopeKindLabel('provider_account')).toBe('上游账号')
    expect(scopeKindLabel('weird')).toBe('weird')
    expect(metricLabel('tokens_estimated')).toBe('预估 Token')
    expect(windowKindLabel('calendar_week')).toBe('自然周')
    expect(modeLabel('manual_first')).toBe('先人工')
    expect(modeLabel('')).toBe('—')
  })

  it('calendar_month 进入窗口白名单并映射为自然月', () => {
    // 变异:从 WINDOW_KINDS 移除 calendar_month 会同时打红下拉枚举与提交映射。
    const calendarMonth: WindowKind = 'calendar_month'
    expect(WINDOW_KINDS).toContain(calendarMonth)
    expect(windowKindLabel(calendarMonth)).toBe('自然月')
    const r = validatePolicyForm({ ...baseForm(), windowKind: calendarMonth, windowSeconds: '0' })
    expect(r.ok).toBe(true)
    if (r.ok) expect(r.value.window_kind).toBe('calendar_month')
  })
})

describe('modeTone / isEnforce', () => {
  it('enforce→danger,observe→info,manual_first→warn,disabled→muted', () => {
    expect(modeTone('enforce')).toBe('danger')
    expect(modeTone('observe')).toBe('info')
    expect(modeTone('manual_first')).toBe('warn')
    expect(modeTone('disabled')).toBe('muted')
    expect(modeTone('unknown')).toBe('muted')
  })

  it('isEnforce 仅 enforce 为真(高影响,触发二次确认)', () => {
    // 判别核心:只有 enforce 真会拦请求。变异(永远返回 true / 漏判)→ observe 误触确认或 enforce 漏确认→RED。
    expect(isEnforce('enforce')).toBe(true)
    expect(isEnforce('observe')).toBe(false)
    expect(isEnforce('disabled')).toBe(false)
  })
})

// ── formatDecimal(原样,防精度丢失)──────────────────────────────────────────
describe('formatDecimal', () => {
  it('裁尾随 0、整数不留小数点、非法/超大精度原样', () => {
    expect(formatDecimal('1.50000000')).toBe('1.5')
    expect(formatDecimal('2.00')).toBe('2')
    expect(formatDecimal('100')).toBe('100')
    // 判别核心:超大精度十进制不得被 Number() 化丢精度,展示裁尾后仍是原数字串。
    expect(formatDecimal('12345678901234567890.12300')).toBe('12345678901234567890.123')
    expect(formatDecimal('abc')).toBe('abc')
  })
})

// ── isRFC3339 ────────────────────────────────────────────────────────────────
describe('isRFC3339', () => {
  it('须含 T 与时区标记且可解析', () => {
    expect(isRFC3339('2026-01-02T03:04:05Z')).toBe(true)
    expect(isRFC3339('2026-01-02T03:04:05+08:00')).toBe(true)
    // 判别核心:缺时区/缺 T/纯日期一律拒(后端 time.Parse(RFC3339) 会拒)。
    expect(isRFC3339('2026-01-02T03:04:05')).toBe(false)
    expect(isRFC3339('2026-01-02')).toBe(false)
    expect(isRFC3339('not-a-time')).toBe(false)
  })
})

// ── validatePolicyForm(镜像后端 validateRequest)────────────────────────────
function baseForm(): PolicyForm {
  return {
    scopeKind: 'user',
    scopeId: 'u-42',
    metric: 'requests',
    windowKind: 'fixed',
    windowSeconds: '3600',
    limitValue: '1000',
    burstValue: '',
    mode: 'enforce',
    priority: '100',
    enabled: true,
    validFrom: '',
    validUntil: '',
    reason: '',
  }
}

describe('validatePolicyForm', () => {
  it('合法表单 → ok,且产出请求体(空可选字段不进 body)', () => {
    const r = validatePolicyForm(baseForm())
    expect(r.ok).toBe(true)
    if (r.ok) {
      expect(r.value).toEqual({
        scope_kind: 'user',
        scope_id: 'u-42',
        metric: 'requests',
        window_kind: 'fixed',
        window_seconds: 3600,
        limit_value: '1000',
        mode: 'enforce',
        enabled: true,
        priority: 100,
      })
      // 判别核心:未填的 burst/valid_from/valid_until/reason 不进 body(交后端套默认)。
      expect('burst_value' in r.value).toBe(false)
      expect('valid_from' in r.value).toBe(false)
      expect('reason' in r.value).toBe(false)
    }
  })

  it('limit_value 原样字符串保留(超大精度不丢精度)', () => {
    const big = '99999999999999999999.123456789'
    const r = validatePolicyForm({ ...baseForm(), limitValue: big })
    expect(r.ok).toBe(true)
    // 判别核心:limit_value 必须原样字符串透传,绝不 Number()。变异(Number(limitRaw))→ 精度丢失断言 RED。
    if (r.ok) expect(r.value.limit_value).toBe(big)
  })

  it('scope_id 空 → 拒;超 255 → 拒', () => {
    expect(validatePolicyForm({ ...baseForm(), scopeId: '   ' }).ok).toBe(false)
    // 判别核心:255 合法、256 非法(边界打在 MAX_SCOPE_ID_LEN)。变异(去掉上界/改 >256)→ 256 放行断言 RED。
    expect(validatePolicyForm({ ...baseForm(), scopeId: 'a'.repeat(255) }).ok).toBe(true)
    expect(validatePolicyForm({ ...baseForm(), scopeId: 'a'.repeat(256) }).ok).toBe(false)
  })

  it('枚举非法 → 拒(scope_kind/metric/window_kind/mode)', () => {
    expect(validatePolicyForm({ ...baseForm(), scopeKind: 'nope' as never }).ok).toBe(false)
    expect(validatePolicyForm({ ...baseForm(), metric: 'nope' as never }).ok).toBe(false)
    expect(validatePolicyForm({ ...baseForm(), windowKind: 'nope' as never }).ok).toBe(false)
    expect(validatePolicyForm({ ...baseForm(), mode: 'nope' as never }).ok).toBe(false)
  })

  it('fixed 窗口必须 window_seconds>0;非 fixed 允许 0', () => {
    // 判别核心:fixed + window_seconds=0 必须拒(镜像后端 validate.go:75)。变异(去掉 fixed 检查)→ 放行断言 RED。
    expect(validatePolicyForm({ ...baseForm(), windowKind: 'fixed', windowSeconds: '0' }).ok).toBe(false)
    expect(validatePolicyForm({ ...baseForm(), windowKind: 'fixed', windowSeconds: '' }).ok).toBe(false)
    // 非 fixed 窗口允许 0/空(走窗口的另一种语义)。
    expect(validatePolicyForm({ ...baseForm(), windowKind: 'calendar_day', windowSeconds: '0' }).ok).toBe(true)
    expect(validatePolicyForm({ ...baseForm(), windowKind: 'none', windowSeconds: '' }).ok).toBe(true)
  })

  it('limit_value 必填;limit/burst 非负十进制(负号/非数字拒)', () => {
    expect(validatePolicyForm({ ...baseForm(), limitValue: '' }).ok).toBe(false)
    expect(validatePolicyForm({ ...baseForm(), limitValue: '-1' }).ok).toBe(false)
    expect(validatePolicyForm({ ...baseForm(), limitValue: 'abc' }).ok).toBe(false)
    expect(validatePolicyForm({ ...baseForm(), burstValue: '-5' }).ok).toBe(false)
    expect(validatePolicyForm({ ...baseForm(), burstValue: '10.5' }).ok).toBe(true)
  })

  it('priority 须整数(可负);非整数拒', () => {
    expect(validatePolicyForm({ ...baseForm(), priority: '-3' }).ok).toBe(true)
    expect(validatePolicyForm({ ...baseForm(), priority: '1.5' }).ok).toBe(false)
    expect(validatePolicyForm({ ...baseForm(), priority: 'x' }).ok).toBe(false)
  })

  it('valid_until 须严格晚于 valid_from(两者都给出时本地校验)', () => {
    const from = '2026-01-01T00:00:00Z'
    const earlier = '2025-12-31T00:00:00Z'
    const later = '2026-02-01T00:00:00Z'
    // 判别核心:until <= from 必须拒(镜像后端 !until.After(from),validate.go:137)。
    // 变异(把 > 改成 >=,或删此校验)→ until==from / until<from 放行断言 RED。
    expect(validatePolicyForm({ ...baseForm(), validFrom: from, validUntil: earlier }).ok).toBe(false)
    expect(validatePolicyForm({ ...baseForm(), validFrom: from, validUntil: from }).ok).toBe(false)
    expect(validatePolicyForm({ ...baseForm(), validFrom: from, validUntil: later }).ok).toBe(true)
  })

  it('非法 RFC3339 时间 → 拒', () => {
    expect(validatePolicyForm({ ...baseForm(), validFrom: '2026-01-01' }).ok).toBe(false)
    expect(validatePolicyForm({ ...baseForm(), validUntil: 'soon' }).ok).toBe(false)
  })
})

// ── policyToForm / emptyPolicyForm ───────────────────────────────────────────
describe('policyToForm', () => {
  it('DTO 拍平为表单,十进制裁尾,reason 清空,未知枚举回退安全值', () => {
    const p: QuotaPolicy = {
      id: 1,
      tenant_id: 7,
      scope_kind: 'channel',
      scope_id: 'ch-9',
      metric: 'cost_usd',
      window_kind: 'calendar_day',
      window_seconds: 0,
      limit_value: '100.00000000',
      burst_value: '0',
      mode: 'observe',
      priority: 50,
      enabled: false,
      valid_from: '2026-01-01T00:00:00Z',
      valid_until: null,
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    }
    const f = policyToForm(p)
    expect(f.scopeKind).toBe('channel')
    expect(f.metric).toBe('cost_usd')
    expect(f.windowKind).toBe('calendar_day')
    // 判别核心:limit_value 裁尾随 0 展示为 '100'(但回传仍走表单原值,提交时校验)。
    expect(f.limitValue).toBe('100')
    expect(f.mode).toBe('observe')
    expect(f.enabled).toBe(false)
    expect(f.validUntil).toBe('')
    expect(f.reason).toBe('')
  })
})

describe('emptyPolicyForm', () => {
  it('缺省值贴近后端默认(fixed/enforce/priority 100),且自身可通过校验', () => {
    const f = emptyPolicyForm()
    expect(f.windowKind).toBe('fixed')
    expect(f.mode).toBe('enforce')
    expect(f.priority).toBe('100')
    // 缺 scope_id 与 limit_value,空表单本身不应通过(二者均必填)。
    expect(validatePolicyForm(f).ok).toBe(false)
    // 仅补 limit 仍缺 scope_id → 仍不通过(判别核心:scope_id 必填,不可被 limit 掩盖)。
    expect(validatePolicyForm({ ...f, limitValue: '60' }).ok).toBe(false)
    // 补齐 scope_id + limit 后应通过。
    expect(validatePolicyForm({ ...f, scopeId: 'u-1', limitValue: '60' }).ok).toBe(true)
  })
})

describe('mapQuotaPolicyRows', () => {
  it('完整映射策略展示列且保持十进制字符串精度', () => {
    const policy: QuotaPolicy = {
      id: 8, tenant_id: 7, scope_kind: 'provider_account', scope_id: 'acct-9', metric: 'cost_usd',
      window_kind: 'fixed', window_seconds: 60, limit_value: '12345678901234567890.1200', burst_value: '5.00',
      mode: 'enforce', priority: 12, enabled: false, valid_from: '', valid_until: null, created_at: '', updated_at: '',
    }
    // 判别核心:列值、danger 语气和大十进制展示逐项锁定;Number 化或字段错配会转红。
    const row = mapQuotaPolicyRows([policy])[0]
    expect(row).toMatchObject({
      id: 8,
      scope: '上游账号',
      scopeId: 'acct-9',
      metric: '成本(USD)',
      window: '固定窗口 · 60s',
      limit: '12345678901234567890.12',
      burst: '5',
      mode: '强制拦截',
      modeTone: 'danger',
      priority: 12,
      status: '停用',
      statusTone: 'muted',
    })
    expect(row.policy).toBe(policy)
  })
})
