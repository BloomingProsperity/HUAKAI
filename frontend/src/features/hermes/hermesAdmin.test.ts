import { describe, expect, it } from 'vitest'
import { API_SOURCE_DEDICATED, API_SOURCE_MANAGED } from './hermesAdminTypes'
import {
  apiSourceLabel,
  canRunDirectly,
  confirmExecuteMessage,
  isProfileInUse,
  parseToolArgs,
  previewEntries,
  toolExecutionMode,
  validateEnable,
  validateProfile,
} from './hermesAdmin'

/*
 * Hermes 改动型子系统纯逻辑单测。
 * 每条用例按 §14 留变异余量:故意删守卫 / 翻条件时,断言必须转红。
 */

describe('validateProfile —— managed 禁 pool_group_id', () => {
  it('managed 不带 pool_group_id 合法(变异:若误把 managed 也要求 pool_group_id 会判 ok:false)', () => {
    const v = validateProfile({ name: 'p1', kind: API_SOURCE_MANAGED, apiKeyId: '', poolGroupId: '' })
    expect(v).toEqual({ ok: true, value: { name: 'p1', kind: API_SOURCE_MANAGED } })
  })

  it('managed 带 pool_group_id 必须被拒(变异:若漏掉这条守卫,会错误地放行后端会 400 的请求)', () => {
    const v = validateProfile({ name: 'p1', kind: API_SOURCE_MANAGED, apiKeyId: '', poolGroupId: '7' })
    expect(v.ok).toBe(false)
  })
})

describe('validateProfile —— dedicated 必需 pool_group_id', () => {
  it('dedicated 带 pool_group_id 合法且回填(变异:若不带入 pool_group_id,后端会判缺字段)', () => {
    const v = validateProfile({ name: 'g', kind: API_SOURCE_DEDICATED, apiKeyId: '', poolGroupId: '5' })
    expect(v).toEqual({ ok: true, value: { name: 'g', kind: API_SOURCE_DEDICATED, pool_group_id: 5 } })
  })

  it('dedicated 缺 pool_group_id 必须被拒(变异:若漏掉这条守卫,会放行后端 400 的请求)', () => {
    const v = validateProfile({ name: 'g', kind: API_SOURCE_DEDICATED, apiKeyId: '', poolGroupId: '' })
    expect(v.ok).toBe(false)
  })
})

describe('validateProfile —— 基础校验', () => {
  it('name 全空白被拒(变异:若不 trim 会把 "  " 当合法名提交)', () => {
    const v = validateProfile({ name: '   ', kind: API_SOURCE_MANAGED, apiKeyId: '', poolGroupId: '' })
    expect(v.ok).toBe(false)
  })

  it('api_key_id 非正整数被拒(变异:若放过 0 / 负数,后端拒)', () => {
    expect(validateProfile({ name: 'p', kind: API_SOURCE_MANAGED, apiKeyId: '0', poolGroupId: '' }).ok).toBe(false)
    expect(validateProfile({ name: 'p', kind: API_SOURCE_MANAGED, apiKeyId: 'x', poolGroupId: '' }).ok).toBe(false)
  })

  it('api_key_id 为正整数时回填(变异:若丢弃 api_key_id 字段会绑不上 key)', () => {
    const v = validateProfile({ name: 'p', kind: API_SOURCE_MANAGED, apiKeyId: '9', poolGroupId: '' })
    expect(v).toEqual({ ok: true, value: { name: 'p', kind: API_SOURCE_MANAGED, api_key_id: 9 } })
  })
})

describe('validateEnable —— api_source 与 profile 关系', () => {
  it('managed 不带 profile 合法(变异:若误要求 managed 也绑 profile 会判 false)', () => {
    expect(validateEnable({ apiSource: API_SOURCE_MANAGED, profileId: null })).toEqual({
      ok: true,
      apiSource: API_SOURCE_MANAGED,
    })
  })

  it('managed 带 profile 必须被拒(变异:漏这条守卫会发后端会 400 的 managed+profile_id)', () => {
    expect(validateEnable({ apiSource: API_SOURCE_MANAGED, profileId: 3 }).ok).toBe(false)
  })

  it('dedicated 缺 profile 必须被拒(变异:漏这条守卫会发缺 profile_id 的 dedicated)', () => {
    expect(validateEnable({ apiSource: API_SOURCE_DEDICATED, profileId: null }).ok).toBe(false)
  })

  it('dedicated 带 profile 合法且回填(变异:若丢 profileId 会绑不上分组)', () => {
    expect(validateEnable({ apiSource: API_SOURCE_DEDICATED, profileId: 4 })).toEqual({
      ok: true,
      apiSource: API_SOURCE_DEDICATED,
      profileId: 4,
    })
  })
})

describe('toolExecutionMode / canRunDirectly —— mutating 绝不可直接执行', () => {
  it('read_only 且非 mutating ⇒ 可直接跑(变异:若误归为 mutating 会逼用户走多余确认)', () => {
    expect(toolExecutionMode({ read_only: true, mutating: false })).toBe('read_only')
    expect(canRunDirectly({ read_only: true, mutating: false })).toBe(true)
  })

  it('mutating ⇒ 必须走 confirm,即便同时标了 read_only(变异:若优先看 read_only 会把改动型当只读直接执行,危险)', () => {
    expect(toolExecutionMode({ read_only: true, mutating: true })).toBe('mutating')
    expect(canRunDirectly({ read_only: true, mutating: true })).toBe(false)
  })

  it('既非 read_only 也非 mutating ⇒ 保守归 mutating(变异:若默认当只读直接跑会绕过确认)', () => {
    expect(canRunDirectly({ read_only: false, mutating: false })).toBe(false)
  })
})

describe('parseToolArgs —— args 必须是 JSON 对象', () => {
  it('空串 ⇒ 空 args(变异:若空串报错会让无参工具无法执行)', () => {
    expect(parseToolArgs('   ')).toEqual({ ok: true, args: {} })
  })

  it('合法对象 ⇒ 解析(变异:若不解析会丢参数)', () => {
    expect(parseToolArgs('{"account_id": 7}')).toEqual({ ok: true, args: { account_id: 7 } })
  })

  it('非法 JSON ⇒ 拒(变异:若放过会把坏 JSON 发给后端)', () => {
    expect(parseToolArgs('{not json}').ok).toBe(false)
  })

  it('数组 / 数字 / null ⇒ 拒(变异:若只 try/catch 不校验对象类型,会放过数组当 args)', () => {
    expect(parseToolArgs('[1,2]').ok).toBe(false)
    expect(parseToolArgs('42').ok).toBe(false)
    expect(parseToolArgs('null').ok).toBe(false)
  })
})

describe('previewEntries —— preview 拍平', () => {
  it('对象按 key 排序拍平(变异:若不排序渲染不稳定;若只取首键会丢字段)', () => {
    expect(previewEntries({ to: 'paused', from: 'active' })).toEqual([
      { key: 'from', value: 'active' },
      { key: 'to', value: 'paused' },
    ])
  })

  it('值为对象时 JSON 串化、null 显示破折号(变异:若直接 String(obj) 会得 [object Object])', () => {
    expect(previewEntries({ meta: { a: 1 }, empty: null })).toEqual([
      { key: 'empty', value: '—' },
      { key: 'meta', value: '{"a":1}' },
    ])
  })

  it('null / 非对象 ⇒ 空列表(变异:若不防御会在渲染时崩)', () => {
    expect(previewEntries(null)).toEqual([])
    expect(previewEntries(undefined)).toEqual([])
    expect(previewEntries([1, 2] as unknown as Record<string, unknown>)).toEqual([])
  })
})

describe('confirmExecuteMessage —— 二次确认明示影响', () => {
  it('含工具名与 preview 改动明细(变异:若只回固定文案会让 operator 看不到将改什么)', () => {
    const msg = confirmExecuteMessage('account_pause', { from: 'active', to: 'paused' })
    expect(msg).toContain('account_pause')
    expect(msg).toContain('from:active')
    expect(msg).toContain('to:paused')
  })

  it('无 preview 时仍明示是改动型(变异:若 preview 空就静默会丢失风险提示)', () => {
    const msg = confirmExecuteMessage('dlq_replay', null)
    expect(msg).toContain('dlq_replay')
    expect(msg).toContain('改变系统状态')
  })
})

describe('isProfileInUse —— 删除前提示绑定', () => {
  it('当前配置正引用该 profile ⇒ true(变异:若恒 false 会漏掉「将断开绑定」提示)', () => {
    const settings = { tenant_id: 1, user_id: 1, enabled: true, api_source: API_SOURCE_DEDICATED, profile_id: 5 }
    expect(isProfileInUse(settings, 5)).toBe(true)
    expect(isProfileInUse(settings, 6)).toBe(false)
  })

  it('无配置 / 未绑定 ⇒ false(变异:若不判 null 会抛异常)', () => {
    expect(isProfileInUse(null, 5)).toBe(false)
    expect(
      isProfileInUse({ tenant_id: 1, user_id: 1, enabled: false, api_source: API_SOURCE_MANAGED, profile_id: null }, 5),
    ).toBe(false)
  })
})

describe('apiSourceLabel —— 展示标签', () => {
  it('已知值映射中文(变异:若返回原始枚举会泄露内部标识符给运维)', () => {
    expect(apiSourceLabel(API_SOURCE_MANAGED)).toBe('托管 HUAKAI API')
    expect(apiSourceLabel(API_SOURCE_DEDICATED)).toBe('专用分组')
  })
})
