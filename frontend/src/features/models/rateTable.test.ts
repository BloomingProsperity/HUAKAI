import { describe, expect, it } from 'vitest'
import {
  formatEffectiveRange,
  formatTime,
  isActiveSnapshot,
  parsePricingRows,
  prettyJSON,
  snapshotList,
  summarizeValue,
  type RateTableSnapshot,
} from './rateTable'

function snap(over: Partial<RateTableSnapshot>): RateTableSnapshot {
  return { id: 1, version: 'v1', effective_from: '2026-01-01T00:00:00Z', created_at: '2026-01-01T00:00:00Z', ...over }
}

describe('snapshotList', () => {
  it('从响应取出数组', () => {
    expect(snapshotList({ snapshots: [snap({ id: 7 })] })).toHaveLength(1)
  })
  it('snapshots 为 null / 响应为空时返回空数组(不抛)', () => {
    // 判别:后端无数据时 snapshots=null。变异(直接 return resp.snapshots)→ 这里会得到 null,
    // 上层 .map 崩溃;断言长度为 0 可红。
    expect(snapshotList({ snapshots: null })).toEqual([])
    expect(snapshotList(null)).toEqual([])
    expect(snapshotList(undefined)).toEqual([])
  })
})

describe('isActiveSnapshot', () => {
  it('effective_to 为空 = 当前生效', () => {
    expect(isActiveSnapshot(snap({ effective_to: null }))).toBe(true)
    expect(isActiveSnapshot(snap({ effective_to: '' }))).toBe(true)
  })
  it('有 effective_to = 已下线', () => {
    // 判别:变异(恒返回 true)→ 这条会从 false 翻成 true→RED。
    expect(isActiveSnapshot(snap({ effective_to: '2026-02-01T00:00:00Z' }))).toBe(false)
  })
})

describe('formatTime', () => {
  it('ISO → 本地化短格式(年月日时分,补零)', () => {
    // 用固定本地时区无关的判别:只校验结构(含 "-" 与 ":" 且非 "—")。
    const out = formatTime('2026-01-09T03:05:00Z')
    expect(out).not.toBe('—')
    expect(out).toMatch(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/)
  })
  it('空 / 非法时间 → —', () => {
    // 判别:变异(去掉 NaN 检查直接 toLocale)→ 非法串会产出 "Invalid Date" 而非 "—"→RED。
    expect(formatTime('')).toBe('—')
    expect(formatTime(null)).toBe('—')
    expect(formatTime('not-a-date')).toBe('—')
  })
})

describe('formatEffectiveRange', () => {
  it('无 effective_to 显示「→ 至今」', () => {
    expect(formatEffectiveRange(snap({ effective_to: null }))).toMatch(/→ 至今$/)
  })
  it('有 effective_to 显示两端区间(不含「至今」)', () => {
    // 判别:变异(忽略 effective_to 恒走至今分支)→ 这条会以「至今」结尾→RED。
    const out = formatEffectiveRange(snap({ effective_to: '2026-02-01T00:00:00Z' }))
    expect(out).not.toMatch(/至今/)
    expect(out).toMatch(/→/)
  })
})

describe('parsePricingRows', () => {
  it('对象 map 形态 → 每键一行', () => {
    const rows = parsePricingRows({ 'gpt-4': { in: 1 }, 'claude-3': { in: 2 } })
    expect(rows.map((r) => r.model).sort()).toEqual(['claude-3', 'gpt-4'])
  })
  it('数组形态 → 取 model/name/id 作行名', () => {
    // 判别:变异(只认对象 map 不认数组)→ 返回空数组,这条期望 2 行会 RED。
    const rows = parsePricingRows([{ model: 'a', in: 1 }, { name: 'b' }])
    expect(rows.map((r) => r.model)).toEqual(['a', 'b'])
  })
  it('标量 / null / 无名条目 → 跳过或空', () => {
    expect(parsePricingRows(42)).toEqual([])
    expect(parsePricingRows(null)).toEqual([])
    // 数组里无 model/name/id 的对象被丢弃
    expect(parsePricingRows([{ foo: 1 }])).toEqual([])
  })
})

describe('summarizeValue', () => {
  it('对象 → 紧凑 JSON', () => {
    expect(summarizeValue({ a: 1 })).toBe('{"a":1}')
  })
  it('标量 → 字符串;null → —', () => {
    expect(summarizeValue(3)).toBe('3')
    expect(summarizeValue(null)).toBe('—')
  })
})

describe('prettyJSON', () => {
  it('美化缩进', () => {
    expect(prettyJSON({ a: 1 })).toBe('{\n  "a": 1\n}')
  })
  it('null → {}', () => {
    expect(prettyJSON(null)).toBe('{}')
  })
})
