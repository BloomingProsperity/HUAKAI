import { describe, expect, it } from 'vitest'
import {
  buildCreatePool,
  buildUpdatePool,
  EMPTY_POOL_FORM,
  mapPoolRows,
  runeCount,
  toggleEnabledTarget,
  validatePoolName,
  type PoolForm,
} from './groups'
import type { PoolGroup } from './types'

function form(over: Partial<PoolForm>): PoolForm {
  return { ...EMPTY_POOL_FORM, ...over }
}

describe('validatePoolName', () => {
  it('空 / 超 64 rune 报错;中文按 rune 计', () => {
    // 判别核心:超过 64 个码点必须报错。变异(去掉长度上限判断)→ 长名通过 → RED。
    expect(validatePoolName('   ')).toBe('请填写分组名称')
    expect(validatePoolName('字'.repeat(65))).toBe('名称不能超过 64 个字符')
    expect(validatePoolName('字'.repeat(64))).toBeNull()
    expect(validatePoolName(' 主力池 ')).toBeNull()
  })
})

describe('runeCount', () => {
  it('按码点计数(emoji / 中文各算 1,不按 UTF-16 code unit)', () => {
    // 判别核心:与后端 utf8.RuneCountInString 对齐。变异(用 s.length)→ emoji 算 2 → RED。
    expect(runeCount('😀')).toBe(1)
    expect(runeCount('中文')).toBe(2)
  })
})

describe('buildCreatePool', () => {
  it('top_k 越界 / 能力非法 各报错', () => {
    // 判别核心:top_k 必须落在 1..10。变异(放开上界)→ 11 通过 → RED。
    expect(buildCreatePool(form({ name: 'p', topKDefault: 11 }))).toEqual({ error: 'top_k_default 取值需在 1..10' })
    expect(buildCreatePool(form({ name: 'p', topKDefault: 0 }))).toEqual({ error: 'top_k_default 取值需在 1..10' })
    expect(buildCreatePool(form({ name: 'p', capabilityDefault: 'wild' }))).toEqual({ error: '能力默认值非法' })
  })

  it('齐全 → 仅落库字段的请求', () => {
    const r = buildCreatePool(form({ name: ' 主力池 ', topKDefault: 3, allowLastResort: true }))
    expect(r).toEqual({
      name: '主力池',
      top_k_default: 3,
      capability_default: 'exact_capability_only',
      allow_last_resort: true,
    })
    // 判别核心:不能泄漏 description/tags 等无落库字段(schema 无该列)。
    expect('description' in (r as object)).toBe(false)
    expect('tags' in (r as object)).toBe(false)
  })
})

describe('buildUpdatePool', () => {
  const original = form({ name: '旧名', topKDefault: 2, capabilityDefault: 'exact_capability_only', allowLastResort: false })

  it('只发生变化的字段进 patch(PATCH 语义)', () => {
    // 判别核心:未变字段不能进 patch。变异(总是塞 name)→ 未改名时 patch 含 name → RED。
    const r = buildUpdatePool(form({ name: '旧名', topKDefault: 5, capabilityDefault: 'exact_capability_only' }), original)
    expect(r).toEqual({ top_k_default: 5 })
    expect('name' in (r as object)).toBe(false)
  })

  it('完全无改动 → 报错(避免后端 admin_bad_request)', () => {
    expect(buildUpdatePool(form({ name: '旧名', topKDefault: 2 }), original)).toEqual({ error: '没有需要保存的改动' })
  })

  it('改名 + 翻 allow_last_resort 同时进 patch', () => {
    const r = buildUpdatePool(form({ name: '新名', topKDefault: 2, allowLastResort: true }), original)
    expect(r).toEqual({ name: '新名', allow_last_resort: true })
  })
})

describe('toggleEnabledTarget', () => {
  it('enabled=true → false(禁用),false → true(启用)', () => {
    // 判别核心:启用态必须翻成禁用。变异(恒返回 true)→ 第一断言 RED。
    expect(toggleEnabledTarget(true)).toBe(false)
    expect(toggleEnabledTarget(false)).toBe(true)
  })
})

describe('mapPoolRows', () => {
  it('完整映射池组展示列(删兜底/状态/能力任一映射→红)', () => {
    const pool: PoolGroup = {
      id: 3, tenant_id: 2, name: '主池', routing_policy_version: 'v1', top_k_default: 4,
      capability_default: 'safe_equivalent_allowed', allow_tenant_operator_force: false,
      allow_last_resort: true, sticky_wait_max_waiting: 0, fallback_wait_max_waiting: 0,
      sticky_wait_timeout_ms: 0, fallback_wait_timeout_ms: 0, forced_route_rate_limit_per_hour: 0,
      enabled: false, created_at: 'bad-date', updated_at: '',
    }
    expect(mapPoolRows([pool])[0]).toMatchObject({
      id: 3, name: '主池', capability: '允许安全等价', topK: 4,
      fallback: '允许兜底', fallbackTone: 'info', status: '已禁用', statusTone: 'muted', createdAt: '—',
    })
  })
})
