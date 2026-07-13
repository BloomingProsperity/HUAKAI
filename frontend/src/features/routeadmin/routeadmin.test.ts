import { describe, expect, it } from 'vitest'
import {
  DEFAULT_MATCH_PRIORITY,
  displayModelPattern,
  mapRouteRows,
  sortRoutes,
  validateCreate,
  validateModelPattern,
  validateUpdate,
} from './routeadmin'
import type { Route } from './types'

/*
 * 请求路由规则纯逻辑测试。每个用例都做 §14 变异自检:把被测守卫删掉/翻转后
 * 该断言必须转红,故 fixture 经过判别性挑选(broken 路径的输出与 correct 不同)。
 */

describe('validateModelPattern', () => {
  it('全匹配/精确/前缀放行', () => {
    expect(validateModelPattern('')).toBeNull()
    expect(validateModelPattern('*')).toBeNull()
    expect(validateModelPattern('claude-3-opus')).toBeNull() // 纯精确串
    expect(validateModelPattern('claude-*')).toBeNull() // 末尾后缀
  })
  it('中段/前导/多通配一律拒', () => {
    // 变异(去掉「恰好一个且在末尾」判别,直接 return null)→ 下面本应报错却放行,断言 RED。
    expect(validateModelPattern('a*b')).not.toBeNull() // 中段
    expect(validateModelPattern('*x')).not.toBeNull() // 前导
    expect(validateModelPattern('a**')).not.toBeNull() // 多个 '*'
    expect(validateModelPattern('cla*ude-*')).not.toBeNull() // 两个 '*'
  })
})

describe('validateCreate', () => {
  const base = {
    name: 'vip-claude',
    userGroupMatch: 'vip',
    modelPatternMatch: 'claude-*',
    poolGroupId: '7',
    matchPriority: '10',
  }
  it('合法表单组装出正确请求体', () => {
    const r = validateCreate(3, base)
    expect(r.ok).toBe(true)
    if (!r.ok) return
    // 判别核心:tenant_id 来自参数、pool_group_id 转成数字、优先级取自输入。
    expect(r.value).toEqual({
      tenant_id: 3,
      name: 'vip-claude',
      user_group_match: 'vip',
      pool_group_id: 7,
      match_priority: 10,
      model_pattern_match: 'claude-*',
    })
  })
  it('model_pattern 空时省略该字段(后端按全匹配)', () => {
    // 变异(无条件写入 model_pattern_match)→ 这里会多出 model_pattern_match:'' 字段,断言 RED。
    const r = validateCreate(3, { ...base, modelPatternMatch: '   ' })
    expect(r.ok).toBe(true)
    if (!r.ok) return
    expect('model_pattern_match' in r.value).toBe(false)
  })
  it('优先级留空回落默认 100', () => {
    // 变异(空串不回落默认而当 0 或 NaN)→ 期望 100 的断言 RED。
    const r = validateCreate(3, { ...base, matchPriority: '' })
    expect(r.ok).toBe(true)
    if (!r.ok) return
    expect(r.value.match_priority).toBe(DEFAULT_MATCH_PRIORITY)
  })
  it('tenant_id 非正拒绝', () => {
    expect(validateCreate(0, base).ok).toBe(false)
    expect(validateCreate(-1, base).ok).toBe(false)
  })
  it('name / user_group / pool_group 缺失拒绝', () => {
    // 变异(删 name 非空守卫)→ 空名本应拒却放行,首断言 RED。
    expect(validateCreate(3, { ...base, name: '   ' }).ok).toBe(false)
    expect(validateCreate(3, { ...base, userGroupMatch: '' }).ok).toBe(false)
    // pool_group 0 / 非正整数拒(后端 PoolGroupID<=0 → ErrInvalidInput)。
    expect(validateCreate(3, { ...base, poolGroupId: '0' }).ok).toBe(false)
    expect(validateCreate(3, { ...base, poolGroupId: 'abc' }).ok).toBe(false)
  })
  it('非法模型模式拒(中段通配)', () => {
    expect(validateCreate(3, { ...base, modelPatternMatch: 'a*b' }).ok).toBe(false)
  })
  it('优先级负数/小数拒', () => {
    // 变异(允许负数/小数)→ 本应拒却放行,断言 RED。
    expect(validateCreate(3, { ...base, matchPriority: '-1' }).ok).toBe(false)
    expect(validateCreate(3, { ...base, matchPriority: '1.5' }).ok).toBe(false)
  })
  it('优先级 0 合法(非负)', () => {
    // 判别核心:0 是合法优先级(后端无 >0 约束),不可被正整数校验误拒。
    const r = validateCreate(3, { ...base, matchPriority: '0' })
    expect(r.ok).toBe(true)
    if (r.ok) expect(r.value.match_priority).toBe(0)
  })
})

describe('validateUpdate', () => {
  it('编辑体不含 tenant_id 且始终带 match_priority', () => {
    const r = validateUpdate({
      name: 'r1',
      userGroupMatch: 'g',
      modelPatternMatch: '',
      poolGroupId: '2',
      matchPriority: '', // 留空
    })
    expect(r.ok).toBe(true)
    if (!r.ok) return
    // 判别核心:全替换语义——即便留空,也必须显式带 match_priority=默认 100(防 read-omit-write 静默重置)。
    expect('tenant_id' in r.value).toBe(false)
    expect(r.value.match_priority).toBe(DEFAULT_MATCH_PRIORITY)
  })
})

describe('sortRoutes', () => {
  const mk = (id: number, p: number): Route => ({
    id,
    tenant_id: 1,
    name: `r${id}`,
    user_group_match: 'g',
    model_pattern_match: '',
    pool_group_id: 1,
    match_priority: p,
    enabled: true,
    created_at: '',
    updated_at: '',
  })
  it('按 match_priority 升序、同优先级按 id 升序', () => {
    const input = [mk(5, 100), mk(2, 10), mk(9, 10), mk(1, 50)]
    // 判别核心:期望顺序由 priority 主导(10<50<100),同 10 内 id 2<9。
    // 变异(去掉 priority 比较只按 id)→ 顺序变 [1,2,5,9],与期望 [2,9,1,5] 不同,断言 RED。
    expect(sortRoutes(input).map((r) => r.id)).toEqual([2, 9, 1, 5])
  })
  it('不改原数组(纯函数)', () => {
    const input = [mk(2, 10), mk(1, 5)]
    sortRoutes(input)
    expect(input.map((r) => r.id)).toEqual([2, 1])
  })
})

describe('displayModelPattern', () => {
  it('空/星号显示全部模型,其余原样', () => {
    // 变异(去掉空/* 归一)→ '全部模型' 断言 RED。
    expect(displayModelPattern('')).toBe('全部模型')
    expect(displayModelPattern('*')).toBe('全部模型')
    expect(displayModelPattern('claude-*')).toBe('claude-*')
  })
})

describe('mapRouteRows', () => {
  it('完整映射列表列与状态语气(删 modelPattern/status 映射→红)', () => {
    const route: Route = {
      id: 8, tenant_id: 2, name: 'vip', user_group_match: 'gold', model_pattern_match: '',
      pool_group_id: 7, match_priority: 9, enabled: false, created_at: '', updated_at: '',
    }
    expect(mapRouteRows([route])).toEqual([{
      id: 8, priority: 9, name: 'vip', userGroup: 'gold', modelPattern: '全部模型',
      poolGroup: '#7', status: '停用', statusTone: 'muted', route,
    }])
  })
})
