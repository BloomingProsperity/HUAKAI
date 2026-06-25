import { describe, expect, it } from 'vitest'
import { canSubmit, isSelectableLevel, levelTone, normalizeLevel } from './logsdiag'

describe('normalizeLevel', () => {
  it('去空白转小写,空值兜底空串', () => {
    expect(normalizeLevel('  DEBUG ')).toBe('debug')
    expect(normalizeLevel(undefined)).toBe('')
    expect(normalizeLevel(null)).toBe('')
  })
})

describe('isSelectableLevel', () => {
  it('仅 debug/info/warn/error 属可选档;dpanic/未知不属', () => {
    // 判别核心:可选集合只含这四档。变异(把 dpanic 也纳入)→ 第二行断言 RED。
    expect(isSelectableLevel('warn')).toBe(true)
    expect(isSelectableLevel('dpanic')).toBe(false)
    expect(isSelectableLevel('verbose')).toBe(false)
  })
})

describe('levelTone', () => {
  it('debug→muted,info→info,warn→warn,error/dpanic/fatal→danger', () => {
    expect(levelTone('debug')).toBe('muted')
    expect(levelTone('info')).toBe('info')
    expect(levelTone('WARN')).toBe('warn')
    expect(levelTone('error')).toBe('danger')
    // 更高级别亦视为异常态(danger),而非中性。
    expect(levelTone('fatal')).toBe('danger')
  })
})

describe('canSubmit', () => {
  it('目标=当前 → 不可提交(避免无谓写)', () => {
    // 判别核心:相同级别拒发。变异(去掉相等检查恒返回 true)→ 本断言 RED。
    expect(canSubmit('info', 'info')).toBe(false)
    expect(canSubmit('INFO', ' info ')).toBe(false)
  })

  it('目标合法且不同 → 可提交', () => {
    expect(canSubmit('info', 'debug')).toBe(true)
  })

  it('非法目标 → 一律拒发', () => {
    expect(canSubmit('info', 'dpanic')).toBe(false)
    expect(canSubmit('info', '')).toBe(false)
  })
})
