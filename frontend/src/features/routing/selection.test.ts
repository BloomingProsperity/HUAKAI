import { describe, expect, it } from 'vitest'
import {
  buildBindingCreate,
  buildBindingUpdate,
  editFormFromBinding,
  enabledModelIdsWithoutNormal,
  EMPTY_CREATE_BINDING,
  FALLBACK_CLASSES,
  fallbackClassError,
  filterBindingRows,
  hasBindingChanges,
  isFallbackClass,
  mapBindingRows,
} from './selection'
import type { BindingCreateForm, BindingEditForm } from './selection'
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

describe('buildBindingUpdate(回填全部运行时字段)', () => {
  it('AT-BFC-011：API 回显 quota，编辑 priority 后 PATCH 仍精确回填 quota', () => {
    const hydrated = editFormFromBinding(binding)
    expect(hydrated.fallbackClass).toBe('quota')

    const req = buildBindingUpdate(binding, { ...hydrated, priority: '7' })
    expect(req.priority).toBe(7)
    expect(req.selection_mode).toBe('strict_priority')
    expect(req.enabled).toBe(true)
    expect(req.max_parallel_requests).toBe(37)
    // 变异：删除 PATCH 回填后这里得到 undefined，直接证明编辑其它字段会丢 class。
    expect(req.fallback_class).toBe('quota')
    // weight 仍不由当前界面管理。
    expect('weight' in req).toBe(false)
  })

  it('回填可空字段当前值(provider_model_id_override/rpm/tpm),不被清空', () => {
    // 判别核心:富字段绑定改 priority 时必须回填 override/rpm/tpm,否则后端整行覆盖会清空。
    const req = buildBindingUpdate(richBinding, { ...editFormFromBinding(richBinding), priority: '3' })
    expect(req.priority).toBe(3)
    expect(req.provider_model_id_override).toBe('gpt-4o-real')
    expect(req.rpm_limit).toBe(60)
    expect(req.tpm_limit).toBe(90000)
    // 变异⑤：删掉 PATCH 回填会让该断言立即转红，防编辑其它字段静默清空上限。
    expect(req.max_parallel_requests).toBe(37)
    expect(req.fallback_class).toBe('quota')
  })

  it('priority 表单非法 → 回退原值(不写非法值)', () => {
    const req = buildBindingUpdate(binding, { ...editFormFromBinding(binding), priority: 'abc' })
    expect(req.priority).toBe(0)
  })

  it('旧响应缺 fallback_class 按 normal 回填，PATCH 不省略默认值', () => {
    const legacy = { ...binding, fallback_class: undefined }
    const hydrated = editFormFromBinding(legacy)
    expect(hydrated.fallbackClass).toBe('normal')
    const req = buildBindingUpdate(legacy, { ...hydrated, priority: '2' })
    expect(req.fallback_class).toBe('normal')
  })

  it('非法 edit class 不会进入 PATCH，构造器保留原 quota', () => {
    const invalid = { ...editFormFromBinding(binding), fallbackClass: 'emergency' } as unknown as BindingEditForm
    expect(fallbackClassError(invalid.fallbackClass)).toBe('降级类必须是 normal、context_window、safety、quota 或 manual')
    expect(buildBindingUpdate(binding, invalid).fallback_class).toBe('quota')
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
    expect(hasBindingChanges(binding, { ...editFormFromBinding(binding), fallbackClass: 'manual' })).toBe(true)
    expect(hasBindingChanges(binding, { ...editFormFromBinding(binding), maxParallelRequests: '5' })).toBe(true)
  })

  it('旧响应缺值与显式 normal 等价，不制造伪 dirty', () => {
    const legacy = { ...binding, fallback_class: undefined }
    expect(hasBindingChanges(legacy, editFormFromBinding(legacy))).toBe(false)
  })
})

describe('buildBindingCreate', () => {
  it('缺 model_id/pool_group_id 报错', () => {
    expect(buildBindingCreate(EMPTY_CREATE_BINDING)).toEqual({ error: '请填写有效的 model_id' })
    expect(buildBindingCreate({ ...EMPTY_CREATE_BINDING, modelId: '10' })).toEqual({ error: '请填写有效的 pool_group_id' })
  })

  it('齐全 → 正确请求体', () => {
    const req = buildBindingCreate({ ...EMPTY_CREATE_BINDING, modelId: '10', poolGroupId: '20', priority: '6', selectionMode: 'priority_weighted', maxParallelRequests: '4' })
    expect(req).toEqual({
      model_id: 10,
      pool_group_id: 20,
      selection_mode: 'priority_weighted',
      fallback_class: 'normal',
      priority: 6,
      max_parallel_requests: 4,
    })
    expect('weight' in req).toBe(false)
  })

  it('AT-BFC-011：创建选择 context_window 后 POST 体携带选择值', () => {
    const req = buildBindingCreate({
      ...EMPTY_CREATE_BINDING,
      modelId: '10',
      poolGroupId: '20',
      fallbackClass: 'context_window',
    })
    expect(req).toMatchObject({ model_id: 10, pool_group_id: 20, fallback_class: 'context_window' })
  })

  it('并发上限非法值拒绝，空值保持不限且不下发', () => {
    expect(buildBindingCreate({ ...EMPTY_CREATE_BINDING, modelId: '10', poolGroupId: '20', maxParallelRequests: '-1' })).toEqual({
      error: '最大并发请求数必须是大于等于 0 的整数，留空表示不限',
    })
    const req = buildBindingCreate({ ...EMPTY_CREATE_BINDING, modelId: '10', poolGroupId: '20' })
    expect('max_parallel_requests' in req).toBe(false)
  })

  it('非法 class 在前端构造请求前被拒绝', () => {
    const invalid = {
      ...EMPTY_CREATE_BINDING,
      modelId: '10',
      poolGroupId: '20',
      fallbackClass: 'emergency',
    } as unknown as BindingCreateForm
    expect(buildBindingCreate(invalid)).toEqual({
      error: '降级类必须是 normal、context_window、safety、quota 或 manual',
    })
    expect(fallbackClassError('emergency')).toBe('降级类必须是 normal、context_window、safety、quota 或 manual')
  })
})

describe('fallback_class 五枚举边界', () => {
  it('只接受已批准五值，不允许前端自造第六值', () => {
    const values = FALLBACK_CLASSES.map((item) => item.value)
    expect(values).toEqual(['normal', 'context_window', 'safety', 'quota', 'manual'])
    for (const value of values) expect(isFallbackClass(value)).toBe(true)
    expect(isFallbackClass('emergency')).toBe(false)
    expect(isFallbackClass('')).toBe(false)
  })

  it('context_window 提示明确要求管理员确认窗口且系统不代验', () => {
    expect(FALLBACK_CLASSES.find((item) => item.value === 'context_window')?.hint).toContain(
      '需管理员确认目标池/模型确有更大窗口，系统不代验',
    )
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
      fallbackClass: 'quota',
      fallbackClassLabel: 'quota · 限流配额',
      fallbackClassTone: 'warn',
      status: '停用',
      statusTone: 'muted',
    })
    expect('weight' in row).toBe(false)
    expect(row.binding.id).toBe(7)
  })

  it('缺值显示为弱色 normal，class 筛选精确保留目标行', () => {
    const rows = mapBindingRows([
      { ...binding, id: 1, fallback_class: 'quota' },
      { ...binding, id: 2, fallback_class: undefined },
      { ...binding, id: 3, fallback_class: 'safety' },
    ])
    expect(rows[1]).toMatchObject({ fallbackClass: 'normal', fallbackClassLabel: 'normal · 主类', fallbackClassTone: 'muted' })
    expect(filterBindingRows(rows, 'quota').map((row) => row.id)).toEqual([1])
    expect(filterBindingRows(rows, 'normal').map((row) => row.id)).toEqual([2])
    expect(filterBindingRows(rows, '').map((row) => row.id)).toEqual([1, 2, 3])
  })

  it('只报告有启用绑定但缺少启用 normal 主类的模型', () => {
    expect(
      enabledModelIdsWithoutNormal([
        { ...binding, id: 1, model_id: 10, fallback_class: 'normal' },
        { ...binding, id: 2, model_id: 10, fallback_class: 'quota' },
        { ...binding, id: 3, model_id: 11, fallback_class: 'manual' },
        { ...binding, id: 4, model_id: 12, fallback_class: 'quota', enabled: false },
        { ...binding, id: 5, model_id: 13, fallback_class: 'normal', enabled: false },
        { ...binding, id: 6, model_id: 13, fallback_class: 'safety' },
      ]),
    ).toEqual([11, 13])
  })
})
