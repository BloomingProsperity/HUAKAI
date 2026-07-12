import { describe, expect, it } from 'vitest'
import { buildAccountUpdate, formFromAccount, parseErrorCodes, parseTags, proxyModeFromAccount, type AccountEditForm } from './edit'
import type { ProviderAccount } from './types'

const base = {
  id: 1,
  priority: 10,
  static_weight: 100,
  cap_concurrency: 5,
  tags: ['prod', 'us'],
  probe_model: null,
  model_allow_list: [],
  capability_flags: [],
  custom_error_codes_enabled: false,
  custom_error_codes: [],
  temp_unschedulable_enabled: false,
  proxy_id: null,
  proxy_group_id: null,
} as unknown as ProviderAccount

function form(over: Partial<AccountEditForm>): AccountEditForm {
  return { ...formFromAccount(base), ...over }
}

describe('parseTags', () => {
  it('逗号分隔去空白去空项', () => {
    expect(parseTags(' a , b ,, c ')).toEqual(['a', 'b', 'c'])
    expect(parseTags('')).toEqual([])
  })
})

describe('parseErrorCodes', () => {
  it('解析合法状态码', () => {
    expect(parseErrorCodes('429, 529')).toEqual([429, 529])
    expect(parseErrorCodes('')).toEqual([])
  })
  it('越界/非整数报错', () => {
    // 判别核心:99/600/abc 都非法。变异(去掉范围校验)→ 本断言 RED。
    expect(parseErrorCodes('99')).toEqual({ error: '自定义错误码须为 100-599 的整数:99' })
    expect(parseErrorCodes('600')).toEqual({ error: '自定义错误码须为 100-599 的整数:600' })
    expect(parseErrorCodes('abc')).toEqual({ error: '自定义错误码须为 100-599 的整数:abc' })
  })
})

describe('proxyModeFromAccount', () => {
  it('proxy_id 优先于 group,再到 direct', () => {
    expect(proxyModeFromAccount({ ...base, proxy_id: 7 } as ProviderAccount)).toBe('proxy')
    expect(proxyModeFromAccount({ ...base, proxy_group_id: 'g1' } as ProviderAccount)).toBe('group')
    expect(proxyModeFromAccount(base)).toBe('direct')
  })
})

describe('buildAccountUpdate', () => {
  it('只改一个字段 → 体里只含该字段(+reason),不含未改字段', () => {
    // 判别核心:部分更新只发改动项。变异(无脑全量赋值)→ body 会含 static_weight/cap_concurrency/proxy_binding→本断言 RED。
    const r = buildAccountUpdate(base, form({ priority: '20', reason: '提权' }))
    expect(r).toEqual({ priority: 20, reason: '提权' })
    expect('static_weight' in (r as object)).toBe(false)
    expect('cap_concurrency' in (r as object)).toBe(false)
    expect('tags' in (r as object)).toBe(false)
    expect('proxy_binding' in (r as object)).toBe(false)
    expect('probe_model' in (r as object)).toBe(false)
  })

  it('标签变更被收录,顺序变化也算变更', () => {
    expect(buildAccountUpdate(base, form({ tags: 'prod, eu' }))).toEqual({ tags: ['prod', 'eu'] })
    expect(buildAccountUpdate(base, form({ tags: 'us, prod' }))).toEqual({ tags: ['us', 'prod'] })
  })

  it('全无改动 → noop(不发空 PATCH)', () => {
    expect(buildAccountUpdate(base, form({}))).toEqual({ noop: true })
  })

  it('数字非法 → 报错', () => {
    expect(buildAccountUpdate(base, form({ priority: '-1' }))).toEqual({ error: '优先级必须是非负整数' })
    expect(buildAccountUpdate(base, form({ capConcurrency: 'x' }))).toEqual({ error: '并发上限必须是非负整数' })
  })

  it('reason 仅在有改动时附带,无改动不带 reason', () => {
    // noop 优先于 reason:即便填了 reason,无字段改动仍是 noop。
    expect(buildAccountUpdate(base, form({ reason: '随便写写' }))).toEqual({ noop: true })
  })

  it('探测模型:改动收录,填回原值(空)不收录', () => {
    expect(buildAccountUpdate(base, form({ probeModel: 'haiku' }))).toEqual({ probe_model: 'haiku' })
    // 探测模型清空:原为 null→'',仍填空 → 无改动 noop。
    expect(buildAccountUpdate(base, form({ probeModel: '  ' }))).toEqual({ noop: true })
  })

  it('模型白名单 / 能力标记:逗号解析后收录', () => {
    expect(buildAccountUpdate(base, form({ modelAllowList: 'gpt-4o, claude' }))).toEqual({ model_allow_list: ['gpt-4o', 'claude'] })
    expect(buildAccountUpdate(base, form({ capabilityFlags: 'vision' }))).toEqual({ capability_flags: ['vision'] })
  })

  it('自定义错误码:开关 + 列表分别收录,非法列表报错', () => {
    expect(buildAccountUpdate(base, form({ customErrorCodesEnabled: true }))).toEqual({ custom_error_codes_enabled: true })
    expect(buildAccountUpdate(base, form({ customErrorCodes: '429, 529' }))).toEqual({ custom_error_codes: [429, 529] })
    expect(buildAccountUpdate(base, form({ customErrorCodes: '999' }))).toEqual({ error: '自定义错误码须为 100-599 的整数:999' })
  })

  it('临时不可调度开关:翻转收录', () => {
    expect(buildAccountUpdate(base, form({ tempUnschedulableEnabled: true }))).toEqual({ temp_unschedulable_enabled: true })
  })

  it('出站代理:direct→proxy 需选到代理,未选报错', () => {
    // 判别核心:proxy 模式必须有正整数 proxy_id。变异(不校验 proxyId)→ 会漏发/发非法值,本断言 RED。
    expect(buildAccountUpdate(base, form({ proxyMode: 'proxy' }))).toEqual({ error: '请选择一个出站代理' })
    expect(buildAccountUpdate(base, form({ proxyMode: 'proxy', proxyId: '7' }))).toEqual({ proxy_binding: { mode: 'proxy', proxy_id: 7 } })
  })

  it('出站代理:direct→group 需非空组标识', () => {
    expect(buildAccountUpdate(base, form({ proxyMode: 'group' }))).toEqual({ error: '请填写代理组标识(proxy_group_id)' })
    expect(buildAccountUpdate(base, form({ proxyMode: 'group', proxyGroupId: 'us-res' }))).toEqual({ proxy_binding: { mode: 'group', proxy_group_id: 'us-res' } })
  })

  it('出站代理:已绑代理改回直连 → 发 direct;填回同一代理 → 无改动', () => {
    const bound = { ...base, proxy_id: 7 } as ProviderAccount
    // 现状 proxy=7,表单选 direct → 解绑。
    expect(buildAccountUpdate(bound, { ...formFromAccount(bound), proxyMode: 'direct' })).toEqual({ proxy_binding: { mode: 'direct' } })
    // 现状 proxy=7,表单仍是 proxy=7(初值)→ 无改动。
    expect(buildAccountUpdate(bound, formFromAccount(bound))).toEqual({ noop: true })
  })
})
