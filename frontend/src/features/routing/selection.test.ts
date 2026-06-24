import { describe, expect, it } from 'vitest'
import { buildBindingCreate, buildBindingUpdate, editFormFromBinding, EMPTY_CREATE_BINDING } from './selection'
import type { PoolBinding } from './types'

const binding: PoolBinding = {
  id: 1,
  model_id: 10,
  pool_group_id: 20,
  priority: 0,
  weight: 1,
  selection_mode: 'strict_priority',
  fallback_class: 'normal',
  enabled: true,
}

describe('buildBindingUpdate', () => {
  it('未改任何字段 → 空 PATCH(不无谓写入)', () => {
    const req = buildBindingUpdate(binding, editFormFromBinding(binding))
    expect(req).toEqual({})
  })

  it('只发改了的字段:切到加权 + 改权重', () => {
    const req = buildBindingUpdate(binding, {
      priority: '0',
      weight: '5',
      selectionMode: 'priority_weighted',
      fallbackClass: 'normal',
      enabled: true,
    })
    // 判别核心:只带 weight + selection_mode,不带 priority/fallback/enabled(未变)。
    expect(req).toEqual({ weight: 5, selection_mode: 'priority_weighted' })
    expect('priority' in req).toBe(false)
  })

  it('停用 → 只带 enabled:false', () => {
    const req = buildBindingUpdate(binding, { ...editFormFromBinding(binding), enabled: false })
    expect(req).toEqual({ enabled: false })
  })
})

describe('buildBindingCreate', () => {
  it('缺 model_id/pool_group_id 报错', () => {
    expect(buildBindingCreate(EMPTY_CREATE_BINDING)).toEqual({ error: '请填写有效的 model_id' })
    expect(buildBindingCreate({ ...EMPTY_CREATE_BINDING, modelId: '10' })).toEqual({ error: '请填写有效的 pool_group_id' })
  })

  it('齐全 → 正确请求体', () => {
    const req = buildBindingCreate({ ...EMPTY_CREATE_BINDING, modelId: '10', poolGroupId: '20', selectionMode: 'priority_weighted', weight: '3' })
    expect(req).toEqual({
      model_id: 10,
      pool_group_id: 20,
      selection_mode: 'priority_weighted',
      fallback_class: 'normal',
      priority: 0,
      weight: 3,
    })
  })
})
