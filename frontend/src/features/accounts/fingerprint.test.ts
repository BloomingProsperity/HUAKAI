import { describe, expect, it } from 'vitest'
import {
  UNBIND_VALUE,
  bindResultMessage,
  currentSelectionValue,
  optionText,
  selectionToProfileId,
  statusLabel,
} from './fingerprint'
import type { FingerprintBindResult, FingerprintProfileOption } from './types'

function opt(p: Partial<FingerprintProfileOption>): FingerprintProfileOption {
  return { id: 1, name: 'Claude Code', status: 'active', ...p }
}

describe('statusLabel', () => {
  it('已知状态映射中文', () => {
    expect(statusLabel('disabled')).toBe('已停用')
    expect(statusLabel('drift_detected')).toBe('指纹漂移')
  })
  it('未知状态兜底原值', () => {
    expect(statusLabel('weird_x')).toBe('weird_x')
  })
})

describe('optionText', () => {
  it('active 不赘述状态', () => {
    expect(optionText(opt({ id: 7, name: 'Safari', status: 'active' }))).toBe('Safari(#7)')
  })
  it('disabled 显式标注状态(变异:若从不标注状态则此条转红)', () => {
    const t = optionText(opt({ id: 9, name: 'Edge', status: 'disabled' }))
    expect(t).toContain('Edge(#9)')
    expect(t).toContain('已停用')
  })
  it('drift_detected 标注漂移', () => {
    expect(optionText(opt({ status: 'drift_detected' }))).toContain('指纹漂移')
  })
})

describe('selectionToProfileId', () => {
  it("空串 → null(解绑)(变异:若把 '' 当 NaN/0 下发则此条转红)", () => {
    expect(selectionToProfileId(UNBIND_VALUE)).toBeNull()
  })
  it('正整数字符串 → 数字', () => {
    expect(selectionToProfileId('42')).toBe(42)
  })
  it('非正整数 / 非数字 → 抛错(变异:若不校验则后端 400 漏到运行时)', () => {
    expect(() => selectionToProfileId('abc')).toThrow()
    expect(() => selectionToProfileId('0')).toThrow()
    expect(() => selectionToProfileId('-3')).toThrow()
    expect(() => selectionToProfileId('1.5')).toThrow()
  })
})

describe('bindResultMessage', () => {
  function res(p: Partial<FingerprintBindResult>): FingerprintBindResult {
    return { id: 1, tls_fingerprint_profile_id: null, ...p }
  }
  it('null → 解绑文案(变异:若忽略 null 分支恒报绑定则转红)', () => {
    expect(bindResultMessage(res({ tls_fingerprint_profile_id: null }))).toContain('解绑')
  })
  it('有 id → 绑定文案含该 id', () => {
    const m = bindResultMessage(res({ tls_fingerprint_profile_id: 88 }))
    expect(m).toContain('绑定')
    expect(m).toContain('88')
  })
})

describe('currentSelectionValue', () => {
  it('未知(null/undefined)→ 默认解绑项(变异:若把 undefined 当已知 0 则会误选 id)', () => {
    expect(currentSelectionValue(undefined)).toBe(UNBIND_VALUE)
    expect(currentSelectionValue(null)).toBe(UNBIND_VALUE)
  })
  it('已知正整数 → 对应字符串值', () => {
    expect(currentSelectionValue(5)).toBe('5')
  })
  it('非正整数 → 解绑项', () => {
    expect(currentSelectionValue(0)).toBe(UNBIND_VALUE)
  })
})
