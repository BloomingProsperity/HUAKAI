import { describe, expect, it } from 'vitest'
import {
  canReplay,
  eventKindLabel,
  formatPayload,
  formatTs,
  isKnownEventKind,
  isMoneySensitiveKind,
  laneTone,
  LIMIT_MAX,
  shortReason,
  statusLabel,
  statusTone,
  validateLimit,
} from './dlq'

describe('validateLimit', () => {
  it('拒 0 / 拒超上限 / 放行边界值', () => {
    // 变异(删 n<1 守卫)→ 0 本应拒却放行,首断言 RED。
    expect(validateLimit(0)).toEqual({ ok: false, error: '条数必须是 ≥1 的整数' })
    // 变异(把 >LIMIT_MAX 改成 >=LIMIT_MAX 或删守卫)→ 201 本应拒,此断言 RED。
    expect(validateLimit(LIMIT_MAX + 1)).toEqual({ ok: false, error: `条数最多 ${LIMIT_MAX}` })
    // 边界 200 必须放行;变异(阈值改成 >=200)→ 此处 RED。
    expect(validateLimit(LIMIT_MAX)).toEqual({ ok: true, value: 200 })
    expect(validateLimit(1)).toEqual({ ok: true, value: 1 })
  })
  it('拒非整数', () => {
    // 变异(删 Number.isInteger 检查)→ 1.5 本应拒,此断言 RED。
    expect(validateLimit(1.5).ok).toBe(false)
  })
})

describe('statusTone', () => {
  it('死信/隔离=danger,已投递=ok,待人工=warn,处理中=info', () => {
    // 判别核心:不同状态映射不同语气;变异(把 dlq 归入默认 muted)→ 首断言 RED。
    expect(statusTone('dlq')).toBe('danger')
    expect(statusTone('quarantined')).toBe('danger')
    expect(statusTone('delivered')).toBe('ok')
    expect(statusTone('operator_review')).toBe('warn')
    expect(statusTone('pending')).toBe('info')
    expect(statusTone('inflight')).toBe('info')
    expect(statusTone('weird')).toBe('muted')
  })
})

describe('statusLabel', () => {
  it('已知状态给中文,未知回退原值', () => {
    expect(statusLabel('dlq')).toBe('死信')
    expect(statusLabel('operator_review')).toBe('待人工审阅')
    // 变异(未知分支返回固定串而非原值)→ 此断言 RED。
    expect(statusLabel('xyz')).toBe('xyz')
    expect(statusLabel('')).toBe('—')
  })
})

describe('laneTone', () => {
  it('HIGH=danger,MED=warn,LOW=muted', () => {
    // 变异(HIGH 错配成 warn)→ 首断言 RED。
    expect(laneTone('HIGH')).toBe('danger')
    expect(laneTone('MED')).toBe('warn')
    expect(laneTone('LOW')).toBe('muted')
  })
})

describe('eventKindLabel', () => {
  it('已知 kind 给中文,未知回退原值', () => {
    expect(eventKindLabel('post_delivery_settlement')).toBe('交付后结算恢复')
    expect(eventKindLabel('usage_record')).toBe('用量记录')
    expect(eventKindLabel('nope')).toBe('nope')
  })
})

describe('isMoneySensitiveKind', () => {
  it('结算/退款/计费/账本/凭证=true,指标/账号健康/审计副本=false', () => {
    // 判别核心:money 类 true,非 money 类 false;变异(全返 true 或全返 false)→ 必有断言 RED。
    expect(isMoneySensitiveKind('post_delivery_settlement')).toBe(true)
    expect(isMoneySensitiveKind('billing_event_replica')).toBe(true)
    expect(isMoneySensitiveKind('audit_mismatch_refund')).toBe(true)
    expect(isMoneySensitiveKind('audit_ledger_entry')).toBe(true)
    expect(isMoneySensitiveKind('cost_receipt_append')).toBe(true)
    expect(isMoneySensitiveKind('usage_record')).toBe(true)
    // 非 money:这三类不直接动余额。变异(默认分支返 true)→ 这些断言 RED。
    expect(isMoneySensitiveKind('metrics')).toBe(false)
    expect(isMoneySensitiveKind('account_health')).toBe(false)
    expect(isMoneySensitiveKind('audit_event_replica')).toBe(false)
  })
})

describe('isKnownEventKind', () => {
  it('已知集合内 true,集合外 false', () => {
    expect(isKnownEventKind('metrics')).toBe(true)
    expect(isKnownEventKind('not_a_kind')).toBe(false)
  })
})

describe('formatTs', () => {
  it('空串/null/undefined → 破折号', () => {
    // 变异(删空串守卫)→ new Date('') 得 Invalid Date,本会回退 iso 即空串,此断言 RED。
    expect(formatTs('')).toBe('—')
    expect(formatTs(null)).toBe('—')
    expect(formatTs(undefined)).toBe('—')
  })
  it('合法时间产出非破折号的本地串', () => {
    const out = formatTs('2026-06-29T10:20:30Z')
    expect(out).not.toBe('—')
    expect(out).not.toBe('2026-06-29T10:20:30Z') // 已被本地化,非原样
  })
  it('非法但非空的串原样回退', () => {
    expect(formatTs('not-a-date')).toBe('not-a-date')
  })
})

describe('formatPayload', () => {
  it('对象缩进 JSON,空/未定义 → {}', () => {
    expect(formatPayload({ a: 1 })).toBe('{\n  "a": 1\n}')
    // 变异(null/undefined 不回退 {})→ 这两断言 RED。
    expect(formatPayload(null)).toBe('{}')
    expect(formatPayload(undefined)).toBe('{}')
  })
})

describe('canReplay', () => {
  it('delivered 不可重放,其余可重放', () => {
    // 判别核心:已投递禁用重放;变异(去掉 delivered 判定恒 true)→ 首断言 RED。
    expect(canReplay({ status: 'delivered' })).toBe(false)
    expect(canReplay({ status: 'dlq' })).toBe(true)
    expect(canReplay({ status: 'pending' })).toBe(true)
  })
})

describe('shortReason', () => {
  it('空 → 破折号,超长截断加省略号,短串原样', () => {
    expect(shortReason('')).toBe('—')
    expect(shortReason('   ')).toBe('—')
    expect(shortReason('短原因')).toBe('短原因')
    const long = 'x'.repeat(70)
    // 变异(不截断直接返回)→ 长度 70 > 64,此断言 RED。
    expect(shortReason(long)).toBe(`${'x'.repeat(64)}…`)
  })
})
