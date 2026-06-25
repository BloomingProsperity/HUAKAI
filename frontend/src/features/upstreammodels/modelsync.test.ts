import { describe, expect, it } from 'vitest'
import { buildSyncRequest, hasChanges, isReasonTooLong, itemSummary, itemTone } from './modelsync'
import type { ModelSyncResult, ModelSyncResultItem } from './types'

function item(over: Partial<ModelSyncResultItem>): ModelSyncResultItem {
  return { vendor: 'v', added: 0, updated: 0, reactivated: 0, disabled: 0, unchanged: 0, snapshot_bumps: 0, ...over }
}
function result(over: Partial<ModelSyncResult>): ModelSyncResult {
  return { object: 'admin_model_sync_result', completed_at: '', total_added: 0, total_updated: 0, total_disabled: 0, results: [], ...over }
}

describe('isReasonTooLong', () => {
  it('200 码点边界:200 内不超长,201 超长(按 trim 后码点计)', () => {
    // 判别核心:阈值是 200(>200 才超长)。变异(改成 >=200 或换成 .length 把表情符算两格)→本断言 RED。
    expect(isReasonTooLong('a'.repeat(200))).toBe(false)
    expect(isReasonTooLong('a'.repeat(201))).toBe(true)
    expect(isReasonTooLong(`  ${'a'.repeat(200)}  `)).toBe(false) // trim 后正好 200
  })
})

describe('buildSyncRequest', () => {
  it('空白 reason → 省略字段(不下发空串);非空 → trim 后下发', () => {
    // 判别核心:空白必须省略 reason。变异(无条件 { reason })→ 第一条断言 RED。
    expect(buildSyncRequest('   ')).toEqual({})
    expect(buildSyncRequest('')).toEqual({})
    expect(buildSyncRequest('  目录扩容  ')).toEqual({ reason: '目录扩容' })
  })
})

describe('hasChanges', () => {
  it('任一总量非零 → true;全零 → false', () => {
    expect(hasChanges(result({}))).toBe(false)
    expect(hasChanges(result({ total_disabled: 1 }))).toBe(true)
    expect(hasChanges(result({ total_added: 3 }))).toBe(true)
  })
})

describe('itemTone', () => {
  it('disabled 优先 warn,纯增/更新/重启用 ok,全无变化 muted', () => {
    // 判别核心:disabled 优先级高于 added。变异(把 disabled 分支删掉或后置)→ 第一条 RED。
    expect(itemTone(item({ disabled: 1, added: 5 }))).toBe('warn')
    expect(itemTone(item({ added: 2 }))).toBe('ok')
    expect(itemTone(item({ reactivated: 1 }))).toBe('ok')
    expect(itemTone(item({ unchanged: 9 }))).toBe('muted')
  })
})

describe('itemSummary', () => {
  it('拼接各非零计数;全零 → 无变化', () => {
    expect(itemSummary(item({ added: 2, updated: 1, disabled: 1 }))).toBe('+2 新增 · 1 更新 · 1 停用')
    expect(itemSummary(item({ unchanged: 4 }))).toBe('无变化')
  })
})
