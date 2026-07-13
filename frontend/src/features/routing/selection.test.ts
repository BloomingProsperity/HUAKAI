import { describe, expect, it } from 'vitest'
import {
  buildBindingCreate,
  buildBindingUpdate,
  editFormFromBinding,
  EMPTY_CREATE_BINDING,
  hasBindingChanges,
  mapBindingRows,
} from './selection'
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

// 带可空字段的绑定:用于验证回填不会清空它们。
const richBinding: PoolBinding = {
  ...binding,
  provider_model_id_override: 'gpt-4o-real',
  rpm_limit: 60,
  tpm_limit: 90000,
}

describe('buildBindingUpdate(整行回填,防后端覆盖重置)', () => {
  it('只改权重 → 仍回填其它当前字段(防 selection_mode/fallback/enabled 被重置默认)', () => {
    const req = buildBindingUpdate(binding, {
      priority: '0',
      weight: '5',
      selectionMode: 'strict_priority',
      fallbackClass: 'normal',
      enabled: true,
    })
    // 判别核心:即便只改 weight,其它字段也必须回填当前值,不能省略(省略=后端重置默认)。
    expect(req.weight).toBe(5)
    expect(req.priority).toBe(0)
    expect(req.selection_mode).toBe('strict_priority')
    expect(req.fallback_class).toBe('normal')
    expect(req.enabled).toBe(true)
  })

  it('回填可空字段当前值(provider_model_id_override/rpm/tpm),不被清空', () => {
    // 判别核心:富字段绑定改 priority 时必须回填 override/rpm/tpm,否则后端整行覆盖会清空。
    const req = buildBindingUpdate(richBinding, { ...editFormFromBinding(richBinding), priority: '3' })
    expect(req.priority).toBe(3)
    expect(req.provider_model_id_override).toBe('gpt-4o-real')
    expect(req.rpm_limit).toBe(60)
    expect(req.tpm_limit).toBe(90000)
  })

  it('priority/weight 表单非法 → 回退原值(不写非法值)', () => {
    const req = buildBindingUpdate(binding, { ...editFormFromBinding(binding), priority: 'abc', weight: '-2' })
    expect(req.priority).toBe(0)
    expect(req.weight).toBe(1)
  })
})

describe('hasBindingChanges', () => {
  it('完全未改 → false', () => {
    expect(hasBindingChanges(binding, editFormFromBinding(binding))).toBe(false)
  })
  it('改了任一可见字段 → true', () => {
    expect(hasBindingChanges(binding, { ...editFormFromBinding(binding), weight: '5' })).toBe(true)
    expect(hasBindingChanges(binding, { ...editFormFromBinding(binding), enabled: false })).toBe(true)
    expect(hasBindingChanges(binding, { ...editFormFromBinding(binding), selectionMode: 'priority_weighted' })).toBe(true)
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

describe('mapBindingRows', () => {
  it('完整映射路由表展示列与状态语气', () => {
    // 判别核心:每个业务字段都来自对应 DTO 字段;删列、错字段或错 tone 均会转红。
    const row = mapBindingRows([{ ...binding, id: 7, model_id: 11, pool_group_id: 22, priority: 3, weight: 9, selection_mode: 'priority_weighted', fallback_class: 'quota', enabled: false }])[0]
    expect(row).toMatchObject({
      id: 7,
      model: '#11',
      pool: '#22',
      priority: 3,
      weight: 9,
      selectionMode: '按权重加权',
      selectionTone: 'info',
      fallbackClass: '配额',
      status: '停用',
      statusTone: 'muted',
    })
    expect(row.binding.id).toBe(7)
  })
})
