import { describe, expect, it } from 'vitest'
import {
  buildRoutingOverrideCreate,
  buildRoutingOverrideUpdate,
  editRoutingOverrideForm,
  EMPTY_ROUTING_OVERRIDE_FORM,
} from './routingOverrideSelection'
import type { ModelRoutingOverride } from './types'

const override: ModelRoutingOverride = {
  id: 17,
  pool_group_id: 9,
  model: 'gpt-pin',
  provider_account_ids: [11, 13],
  enabled: true,
  created_at: '2026-07-15T01:00:00Z',
  updated_at: '2026-07-15T02:00:00Z',
}

describe('强制 pin 表单映射', () => {
  it('创建时修剪模型、解析账号并按首现顺序去重', () => {
    const result = buildRoutingOverrideCreate({
      ...EMPTY_ROUTING_OVERRIDE_FORM,
      poolGroupId: ' 9 ',
      model: ' gpt-pin ',
      providerAccountIDs: '11, 13  11',
      enabled: false,
    })
    expect(result).toEqual({
      pool_group_id: 9,
      model: 'gpt-pin',
      provider_account_ids: [11, 13],
      enabled: false,
    })
  })

  it('空账号、零值与非整数在请求前被拒绝', () => {
    const base = { ...EMPTY_ROUTING_OVERRIDE_FORM, poolGroupId: '9', model: 'gpt-pin' }
    expect(buildRoutingOverrideCreate(base)).toEqual({ error: '请填写至少一个有效的 provider_account_id' })
    expect(buildRoutingOverrideCreate({ ...base, providerAccountIDs: '11,0' })).toEqual({
      error: 'provider_account_ids 只能包含正整数',
    })
    expect(buildRoutingOverrideCreate({ ...base, providerAccountIDs: '11.5' })).toEqual({
      error: 'provider_account_ids 只能包含正整数',
    })
  })

  it('编辑回填并精确构造账号数组与 enabled PATCH', () => {
    const form = editRoutingOverrideForm(override)
    expect(form).toMatchObject({ poolGroupId: '9', model: 'gpt-pin', providerAccountIDs: '11, 13', enabled: true })
    expect(buildRoutingOverrideUpdate({ ...form, providerAccountIDs: '13, 21', enabled: false })).toEqual({
      provider_account_ids: [13, 21],
      enabled: false,
    })
  })
})
