import { describe, expect, it } from 'vitest'
import { confirmPromptText, deleteResultMessage, nameMatchesConfirmation } from './dangerzone'
import type { DeleteAccountResult } from './types'

describe('confirmPromptText', () => {
  it('含账号名与 #id(变异:漏掉任一则运营者无法核对目标账号,此条转红)', () => {
    const t = confirmPromptText('claude-pool-7', 42)
    expect(t).toContain('claude-pool-7')
    expect(t).toContain('#42')
  })
  it('明确标注不可逆 / 无法恢复(变异:若文案不警示不可逆则转红)', () => {
    const t = confirmPromptText('acc', 1)
    expect(t).toContain('不可逆')
    expect(t).toContain('无法恢复')
  })
})

describe('nameMatchesConfirmation', () => {
  it('严格相等 → 放行', () => {
    expect(nameMatchesConfirmation('claude-pool-7', 'claude-pool-7')).toBe(true)
  })
  it('两端空白容差 → 放行(变异:若不 trim 则带首尾空格的正确名转红)', () => {
    expect(nameMatchesConfirmation('  claude-pool-7  ', 'claude-pool-7')).toBe(true)
  })
  it('大小写不同 → 不放行(变异:若大小写不敏感则此条转红,误删风险)', () => {
    expect(nameMatchesConfirmation('Claude-Pool-7', 'claude-pool-7')).toBe(false)
  })
  it('部分匹配 → 不放行(变异:若用 includes 则此条转红)', () => {
    expect(nameMatchesConfirmation('claude', 'claude-pool-7')).toBe(false)
  })
  it('空串 / 纯空白 → 不放行(变异:若空串当匹配则狂点直删)', () => {
    expect(nameMatchesConfirmation('', 'claude-pool-7')).toBe(false)
    expect(nameMatchesConfirmation('   ', 'claude-pool-7')).toBe(false)
  })
})

describe('deleteResultMessage', () => {
  function res(p: Partial<DeleteAccountResult>): DeleteAccountResult {
    return { id: 9, deleted: true, ...p }
  }
  it('deleted=true → 成功文案含名与 #id', () => {
    const m = deleteResultMessage(res({ id: 9, deleted: true }), 'acc-9')
    expect(m).toContain('acc-9')
    expect(m).toContain('#9')
    expect(m).toContain('已硬删')
  })
  it('deleted=false → 不报成功(变异:若忽略 deleted 字段恒报成功则此条转红)', () => {
    const m = deleteResultMessage(res({ id: 9, deleted: false }), 'acc-9')
    expect(m).not.toContain('已硬删')
    expect(m).toContain('未确认')
  })
})
