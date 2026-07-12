import { describe, expect, it } from 'vitest'
import {
  buildCreateRule,
  buildCreateSilence,
  buildUpdateRule,
  comparatorSymbol,
  eventStateLabel,
  eventStateTone,
  EMPTY_RULE_FORM,
  EMPTY_SILENCE_FORM,
  filtersToText,
  isFiring,
  localToISO,
  parseFilters,
  severityLabel,
  severityTone,
  silenceActive,
  type RuleForm,
  type SilenceForm,
} from './alerting'

const rule = (over: Partial<RuleForm> = {}): RuleForm => ({ ...EMPTY_RULE_FORM, ...over })
const silence = (over: Partial<SilenceForm> = {}): SilenceForm => ({ ...EMPTY_SILENCE_FORM, ...over })

describe('枚举标签 / 语气', () => {
  it('comparatorSymbol 映射四个比较符,未知原样', () => {
    expect(comparatorSymbol('gt')).toBe('>')
    expect(comparatorSymbol('gte')).toBe('≥')
    expect(comparatorSymbol('lt')).toBe('<')
    expect(comparatorSymbol('lte')).toBe('≤')
    expect(comparatorSymbol('zz')).toBe('zz')
  })

  it('severityLabel/Tone:三级 + 未知回退', () => {
    expect(severityLabel('critical')).toBe('严重')
    expect(severityTone('critical')).toBe('danger')
    expect(severityTone('warning')).toBe('warn')
    expect(severityTone('info')).toBe('info')
    expect(severityTone('???')).toBe('muted')
  })

  it('eventStateLabel/Tone:firing=触发中/danger,resolved=ok,手动=muted', () => {
    expect(eventStateLabel('firing')).toBe('触发中')
    expect(eventStateTone('firing')).toBe('danger')
    expect(eventStateTone('resolved')).toBe('ok')
    expect(eventStateTone('manual_resolved')).toBe('muted')
    expect(eventStateTone('weird')).toBe('muted')
  })

  it('isFiring 仅对 firing 为真', () => {
    expect(isFiring('firing')).toBe(true)
    expect(isFiring('resolved')).toBe(false)
    expect(isFiring('manual_resolved')).toBe(false)
  })
})

describe('parseFilters / filtersToText', () => {
  it('解析多行 键=值,trim,空行忽略', () => {
    const out = parseFilters('platform=anthropic\n  region = us  \n\n')
    expect(out).toEqual({ ok: true, filters: { platform: 'anthropic', region: 'us' } })
  })

  it('值允许含 =(只按首个 = 切)', () => {
    const out = parseFilters('token=a=b=c')
    expect(out.ok && out.filters).toEqual({ token: 'a=b=c' })
  })

  it('键恰好叫 error 不被误判成错误(判别式联合的意义)', () => {
    const out = parseFilters('error=oops')
    expect(out.ok).toBe(true)
    expect(out.ok && out.filters).toEqual({ error: 'oops' })
  })

  it('缺少 = 报错', () => {
    expect(parseFilters('platform anthropic').ok).toBe(false)
  })

  it('键为空报错', () => {
    expect(parseFilters('=value').ok).toBe(false)
  })

  it('空文本 → 空 map(合法)', () => {
    const out = parseFilters('   \n  ')
    expect(out).toEqual({ ok: true, filters: {} })
  })

  it('filtersToText 按键排序回填,可与 parseFilters 往返', () => {
    expect(filtersToText({ region: 'us', platform: 'a' })).toBe('platform=a\nregion=us')
    expect(filtersToText(undefined)).toBe('')
  })
})

describe('buildCreateRule 校验(镜像 validateRule)', () => {
  it('合法表单产出请求,数值解析正确,空可选字段不带 metric_type/filters', () => {
    const out = buildCreateRule(rule({ name: '高错误率', metric: 'error_rate', threshold: '0.5', windowSeconds: '120' }), 7)
    expect('error' in out).toBe(false)
    if ('error' in out) return
    expect(out.tenant_id).toBe(7)
    expect(out.metric).toBe('error_rate')
    expect(out.threshold).toBe(0.5)
    expect(out.window_seconds).toBe(120)
    expect(out.metric_type).toBeUndefined()
    expect(out.filters).toBeUndefined()
    expect(out.enabled).toBe(true)
  })

  it('name 空 → 报错', () => {
    expect('error' in buildCreateRule(rule({ name: '   ', metric: 'm', threshold: '1' }), 1)).toBe(true)
  })

  it('metric 与 metricType 同时空 → 报错(metricKeyForRule 为空后端会 400)', () => {
    const out = buildCreateRule(rule({ name: 'r', metric: '', metricType: '', threshold: '1' }), 1)
    expect('error' in out).toBe(true)
  })

  it('仅 metricType 非空也合法(优先作为 metricKey)', () => {
    const out = buildCreateRule(rule({ name: 'r', metric: '', metricType: 'cpu_usage_percent', threshold: '90' }), 1)
    expect('error' in out).toBe(false)
    if ('error' in out) return
    expect(out.metric_type).toBe('cpu_usage_percent')
  })

  it('阈值非有限数 → 报错', () => {
    expect('error' in buildCreateRule(rule({ name: 'r', metric: 'm', threshold: 'abc' }), 1)).toBe(true)
    expect('error' in buildCreateRule(rule({ name: 'r', metric: 'm', threshold: '' }), 1)).toBe(true)
  })

  it('阈值可为 0 与负数(有限即可)', () => {
    const z = buildCreateRule(rule({ name: 'r', metric: 'm', threshold: '0' }), 1)
    expect('error' in z).toBe(false)
    const neg = buildCreateRule(rule({ name: 'r', metric: 'm', threshold: '-3.5' }), 1)
    expect('error' in neg).toBe(false)
    if ('error' in neg) return
    expect(neg.threshold).toBe(-3.5)
  })

  it('window_seconds 必须为正整数(0 / 负 / 非整 报错)', () => {
    expect('error' in buildCreateRule(rule({ name: 'r', metric: 'm', threshold: '1', windowSeconds: '0' }), 1)).toBe(true)
    expect('error' in buildCreateRule(rule({ name: 'r', metric: 'm', threshold: '1', windowSeconds: '-5' }), 1)).toBe(true)
    expect('error' in buildCreateRule(rule({ name: 'r', metric: 'm', threshold: '1', windowSeconds: '1.5' }), 1)).toBe(true)
  })

  it('sustained/cooldown 空 → 兜底 0;负数报错', () => {
    const out = buildCreateRule(rule({ name: 'r', metric: 'm', threshold: '1', sustainedSeconds: '', cooldownSeconds: '' }), 1)
    expect('error' in out).toBe(false)
    if ('error' in out) return
    expect(out.sustained_seconds).toBe(0)
    expect(out.cooldown_seconds).toBe(0)
    expect('error' in buildCreateRule(rule({ name: 'r', metric: 'm', threshold: '1', sustainedSeconds: '-1' }), 1)).toBe(true)
  })

  it('filters 文本带入请求;非法 filters 报错', () => {
    const out = buildCreateRule(rule({ name: 'r', metric: 'm', threshold: '1', filtersText: 'platform=anthropic' }), 1)
    expect('error' in out).toBe(false)
    if ('error' in out) return
    expect(out.filters).toEqual({ platform: 'anthropic' })
    expect('error' in buildCreateRule(rule({ name: 'r', metric: 'm', threshold: '1', filtersText: 'bad line' }), 1)).toBe(true)
  })

  it('notify_email / enabled 透传', () => {
    const out = buildCreateRule(rule({ name: 'r', metric: 'm', threshold: '1', notifyEmail: true, enabled: false }), 1)
    if ('error' in out) throw new Error('应合法')
    expect(out.notify_email).toBe(true)
    expect(out.enabled).toBe(false)
  })
})

describe('buildUpdateRule', () => {
  it('总是显式传 metric_type(空串=清空内建类型)', () => {
    const out = buildUpdateRule(rule({ name: 'r', metric: 'm', metricType: '', threshold: '2' }))
    expect('error' in out).toBe(false)
    if ('error' in out) return
    expect(out.metric_type).toBe('')
    expect(out.filters).toEqual({})
  })

  it('沿用同一校验(window 0 报错)', () => {
    expect('error' in buildUpdateRule(rule({ name: 'r', metric: 'm', threshold: '1', windowSeconds: '0' }))).toBe(true)
  })
})

describe('localToISO', () => {
  it('空 → null;非法 → undefined;合法 → ISO', () => {
    expect(localToISO('  ')).toBeNull()
    expect(localToISO('not-a-date')).toBeUndefined()
    const iso = localToISO('2026-06-25T10:30')
    expect(typeof iso).toBe('string')
    expect(iso).toContain('2026-06-25')
  })
})

describe('buildCreateSilence 校验(镜像 validateSilence)', () => {
  const okTimes = { startsAt: '2026-06-25T10:00', endsAt: '2026-06-25T12:00' }

  it('合法表单产出请求,仅带非空可选字段', () => {
    const out = buildCreateSilence(silence({ reason: '维护', ...okTimes, platform: 'anthropic' }), 3)
    expect('error' in out).toBe(false)
    if ('error' in out) return
    expect(out.tenant_id).toBe(3)
    expect(out.reason).toBe('维护')
    expect(out.platform).toBe('anthropic')
    expect(out.rule_id).toBeUndefined()
    expect(out.group_id).toBeUndefined()
  })

  it('原因空 → 报错', () => {
    expect('error' in buildCreateSilence(silence({ reason: ' ', ...okTimes }), 1)).toBe(true)
  })

  it('起止缺失 → 报错', () => {
    expect('error' in buildCreateSilence(silence({ reason: 'r', startsAt: '', endsAt: '2026-06-25T12:00' }), 1)).toBe(true)
    expect('error' in buildCreateSilence(silence({ reason: 'r', startsAt: '2026-06-25T10:00', endsAt: '' }), 1)).toBe(true)
  })

  it('结束须严格晚于开始(相等也报错)', () => {
    expect('error' in buildCreateSilence(silence({ reason: 'r', startsAt: '2026-06-25T12:00', endsAt: '2026-06-25T10:00' }), 1)).toBe(true)
    expect('error' in buildCreateSilence(silence({ reason: 'r', startsAt: '2026-06-25T12:00', endsAt: '2026-06-25T12:00' }), 1)).toBe(true)
  })

  it('rule_id 非正整数报错;正整数带入', () => {
    expect('error' in buildCreateSilence(silence({ reason: 'r', ...okTimes, ruleId: '0' }), 1)).toBe(true)
    expect('error' in buildCreateSilence(silence({ reason: 'r', ...okTimes, ruleId: 'x' }), 1)).toBe(true)
    const out = buildCreateSilence(silence({ reason: 'r', ...okTimes, ruleId: '42' }), 1)
    if ('error' in out) throw new Error('应合法')
    expect(out.rule_id).toBe(42)
  })
})

describe('silenceActive', () => {
  const s = { starts_at: '2026-06-25T10:00:00Z', ends_at: '2026-06-25T12:00:00Z' }
  it('now 在窗口内为真,窗外为假', () => {
    expect(silenceActive(s, Date.parse('2026-06-25T11:00:00Z'))).toBe(true)
    expect(silenceActive(s, Date.parse('2026-06-25T09:59:00Z'))).toBe(false)
    expect(silenceActive(s, Date.parse('2026-06-25T13:00:00Z'))).toBe(false)
  })
  it('边界:start 含、end 不含', () => {
    expect(silenceActive(s, Date.parse('2026-06-25T10:00:00Z'))).toBe(true)
    expect(silenceActive(s, Date.parse('2026-06-25T12:00:00Z'))).toBe(false)
  })
  it('非法时间为假', () => {
    expect(silenceActive({ starts_at: 'x', ends_at: 'y' }, 0)).toBe(false)
  })
})
