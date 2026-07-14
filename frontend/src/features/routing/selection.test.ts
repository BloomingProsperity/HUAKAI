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
  weight: 9473,
  selection_mode: 'strict_priority',
  max_parallel_requests: 37,
  fallback_class: 'quota',
  enabled: true,
}

// 带可空字段的绑定:用于验证回填不会清空它们。
const richBinding: PoolBinding = {
  ...binding,
  provider_model_id_override: 'gpt-4o-real',
  rpm_limit: 60,
  tpm_limit: 90000,
}

describe('buildBindingUpdate(仅提交真实生效字段)', () => {
  it('有效字段精确回填,三个仅存储字段不进入 payload', () => {
    const req = buildBindingUpdate(binding, {
      priority: '7',
      selectionMode: 'priority_weighted',
      enabled: false,
    })
    expect(req.priority).toBe(7)
    expect(req.selection_mode).toBe('priority_weighted')
    expect(req.enabled).toBe(false)
    // 判别核心:把任一欺骗控件重新接回 payload,对应 key 断言立即转红。
    expect('weight' in req).toBe(false)
    expect('max_parallel_requests' in req).toBe(false)
    expect('fallback_class' in req).toBe(false)
  })

  it('回填可空字段当前值(provider_model_id_override/rpm/tpm),不被清空', () => {
    // 判别核心:富字段绑定改 priority 时必须回填 override/rpm/tpm,否则后端整行覆盖会清空。
    const req = buildBindingUpdate(richBinding, { ...editFormFromBinding(richBinding), priority: '3' })
    expect(req.priority).toBe(3)
    expect(req.provider_model_id_override).toBe('gpt-4o-real')
    expect(req.rpm_limit).toBe(60)
    expect(req.tpm_limit).toBe(90000)
  })

  it('priority 表单非法 → 回退原值(不写非法值)', () => {
    const req = buildBindingUpdate(binding, { ...editFormFromBinding(binding), priority: 'abc' })
    expect(req.priority).toBe(0)
  })
})

describe('hasBindingChanges', () => {
  it('完全未改 → false', () => {
    expect(hasBindingChanges(binding, editFormFromBinding(binding))).toBe(false)
  })
  it('改了任一可见字段 → true', () => {
    expect(hasBindingChanges(binding, { ...editFormFromBinding(binding), priority: '5' })).toBe(true)
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
    const req = buildBindingCreate({ ...EMPTY_CREATE_BINDING, modelId: '10', poolGroupId: '20', priority: '6', selectionMode: 'priority_weighted' })
    expect(req).toEqual({
      model_id: 10,
      pool_group_id: 20,
      selection_mode: 'priority_weighted',
      priority: 6,
    })
    expect('weight' in req).toBe(false)
    expect('max_parallel_requests' in req).toBe(false)
    expect('fallback_class' in req).toBe(false)
  })
})

describe('mapBindingRows', () => {
  it('完整映射路由表展示列与状态语气', () => {
    // 判别核心:每个业务字段都来自对应 DTO 字段;删列、错字段或错 tone 均会转红。
    const row = mapBindingRows([{ ...binding, id: 7, model_id: 11, pool_group_id: 22, priority: 3, selection_mode: 'priority_weighted', enabled: false }])[0]
    expect(row).toMatchObject({
      id: 7,
      model: '#11',
      pool: '#22',
      priority: 3,
      selectionMode: '按权重加权',
      selectionTone: 'info',
      status: '停用',
      statusTone: 'muted',
    })
    expect('weight' in row).toBe(false)
    expect('fallbackClass' in row).toBe(false)
    expect(row.binding.id).toBe(7)
  })
})
