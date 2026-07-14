import { describe, expect, it } from 'vitest'
import {
  buildAccountUpdate,
  buildTempUnschedulableRules,
  formFromAccount,
  parseErrorCodes,
  parseExtraJson,
  parseTags,
  proxyGroupBindingWarning,
  proxyModeFromAccount,
  rulesToForm,
  selectedProxyGroupSummary,
  summarizeProxyGroups,
  type AccountEditForm,
} from './edit'
import type { Proxy } from '../proxies/types'
import type { ProviderAccount } from './types'

const base = {
  id: 1,
  priority: 10,
  static_weight: 100,
  cap_concurrency: 5,
  tags: ['prod', 'us'],
  extra: {},
  probe_model: null,
  model_allow_list: [],
  capability_flags: [],
  custom_error_codes_enabled: false,
  custom_error_codes: [],
  pool_mode: false,
  temp_unschedulable_enabled: false,
  proxy_id: null,
  proxy_group_id: null,
} as unknown as ProviderAccount

function form(over: Partial<AccountEditForm>): AccountEditForm {
  return { ...formFromAccount(base), ...over }
}

function proxy(id: number, groupId: string | null, status: string): Proxy {
  return {
    id,
    name: `p-${id}`,
    protocol: 'http',
    host: 'proxy.example',
    port: 3128,
    auth_username: null,
    group_id: groupId,
    status,
    last_check_at: null,
    created_at: '',
    updated_at: '',
  }
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

describe('parseExtraJson', () => {
  it('合法 JSON 对象通过,语法错误与非对象拒绝', () => {
    expect(parseExtraJson('{"region":"us"}')).toEqual({ region: 'us' })
    // 判别核心:后端只接受对象。变异为只做 JSON.parse 时,数组分支会错误通过。
    expect(parseExtraJson('{oops')).toEqual({ error: '扩展 JSON 格式无效' })
    expect(parseExtraJson('[]')).toEqual({ error: '扩展 JSON 必须是 JSON 对象' })
    expect(parseExtraJson('null')).toEqual({ error: '扩展 JSON 必须是 JSON 对象' })
  })
})

describe('buildTempUnschedulableRules', () => {
  it('按真实 schema 组装,关键词留空表示通配且说明可省略', () => {
    expect(
      buildTempUnschedulableRules([
        { errorCode: '403', keywords: ' unusual activity，risk control ', durationMinutes: '30', description: ' 风控 ' },
        { errorCode: '529', keywords: '', durationMinutes: '5', description: '' },
      ]),
    ).toEqual([
      { error_code: 403, keywords: ['unusual activity', 'risk control'], duration_minutes: 30, description: '风控' },
      { error_code: 529, keywords: [], duration_minutes: 5 },
    ])
  })

  it('非法错误码或非正时长拒绝', () => {
    // 判别核心:若删除前端 schema 校验,两个坏规则会进入 PATCH 体。
    expect(buildTempUnschedulableRules([{ errorCode: '99', keywords: '', durationMinutes: '5', description: '' }])).toEqual({
      error: '第 1 条规则的错误码须为 100-599 的整数',
    })
    expect(buildTempUnschedulableRules([{ errorCode: '403', keywords: '', durationMinutes: '0', description: '' }])).toEqual({
      error: '第 1 条规则的停调时长须为正整数分钟',
    })
  })
})

describe('proxyModeFromAccount', () => {
  it('proxy_id 优先于 group,再到 direct', () => {
    expect(proxyModeFromAccount({ ...base, proxy_id: 7 } as ProviderAccount)).toBe('proxy')
    expect(proxyModeFromAccount({ ...base, proxy_group_id: 'g1' } as ProviderAccount)).toBe('group')
    expect(proxyModeFromAccount(base)).toBe('direct')
  })
})

describe('代理组可用性汇总与预警', () => {
  it('按组汇总总成员，active 计数只认严格 active，未分组不入候选', () => {
    expect(summarizeProxyGroups([
      proxy(1, 'group-a', 'active'),
      proxy(2, 'group-a', 'disabled'),
      proxy(3, 'group-b', 'dead'),
      proxy(4, 'group-b', 'ACTIVE'),
      proxy(5, null, 'active'),
    ])).toEqual([
      { groupId: 'group-a', total: 2, active: 1 },
      { groupId: 'group-b', total: 2, active: 0 },
    ])
  })

  it('未知组按零成员处理；零 active 显示危险文案，非零不显示', () => {
    const groups = summarizeProxyGroups([proxy(1, 'healthy', 'active')])
    const unknown = selectedProxyGroupSummary(groups, 'missing')
    expect(unknown).toEqual({ groupId: 'missing', total: 0, active: 0 })
    expect(proxyGroupBindingWarning(unknown)).toContain('fail-closed')
    expect(proxyGroupBindingWarning(unknown)).toContain('不会直连')
    expect(proxyGroupBindingWarning(selectedProxyGroupSummary(groups, 'healthy'))).toBeNull()
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
    expect('pool_mode' in (r as object)).toBe(false)
    expect('temp_unschedulable_rules' in (r as object)).toBe(false)
    expect('extra' in (r as object)).toBe(false)
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

  it('池模式三态:不改不发送,开/关仅在改变原值时发送', () => {
    expect(buildAccountUpdate(base, form({ poolMode: 'unchanged' }))).toEqual({ noop: true })
    expect(buildAccountUpdate(base, form({ poolMode: 'enabled' }))).toEqual({ pool_mode: true })
    expect(buildAccountUpdate(base, form({ poolMode: 'disabled' }))).toEqual({ noop: true })

    const enabled = { ...base, pool_mode: true } as ProviderAccount
    expect(buildAccountUpdate(enabled, { ...formFromAccount(enabled), poolMode: 'disabled' })).toEqual({ pool_mode: false })
    expect(buildAccountUpdate(enabled, { ...formFromAccount(enabled), poolMode: 'enabled' })).toEqual({ noop: true })
  })

  it('临时停调规则默认不发送,明确替换才发送完整数组或空数组', () => {
    expect(buildAccountUpdate(base, form({ priority: '20', tempRulesMode: 'unchanged' }))).toEqual({ priority: 20 })
    expect(
      buildAccountUpdate(
        base,
        form({
          tempRulesMode: 'replace',
          tempUnschedulableRules: [
            { errorCode: '403', keywords: 'risk control', durationMinutes: '30', description: '风控' },
          ],
        }),
      ),
    ).toEqual({
      temp_unschedulable_rules: [
        { error_code: 403, keywords: ['risk control'], duration_minutes: 30, description: '风控' },
      ],
    })
    expect(buildAccountUpdate(base, form({ tempRulesMode: 'replace', tempUnschedulableRules: [] }))).toEqual({
      temp_unschedulable_rules: [],
    })
  })

  it('规则替换遇到非法 schema 时不组装 PATCH', () => {
    expect(
      buildAccountUpdate(
        base,
        form({
          tempRulesMode: 'replace',
          tempUnschedulableRules: [{ errorCode: '403', keywords: '', durationMinutes: 'x', description: '' }],
        }),
      ),
    ).toEqual({ error: '第 1 条规则的停调时长须为正整数分钟' })
  })

  it('扩展 JSON 内容变化才发送,格式或键顺序变化不发送,非法 JSON 拒绝', () => {
    const withExtra = { ...base, extra: { region: 'us', nested: { enabled: true } } } as ProviderAccount
    const initial = formFromAccount(withExtra)
    expect(buildAccountUpdate(withExtra, { ...initial, extraJson: '{ "nested": {"enabled": true}, "region": "us" }' })).toEqual({ noop: true })
    expect(buildAccountUpdate(withExtra, { ...initial, extraJson: '{"region":"eu"}' })).toEqual({ extra: { region: 'eu' } })
    expect(buildAccountUpdate(withExtra, { ...initial, extraJson: '{bad' })).toEqual({ error: '扩展 JSON 格式无效' })
    expect(buildAccountUpdate(withExtra, { ...initial, extraJson: '[]' })).toEqual({ error: '扩展 JSON 必须是 JSON 对象' })
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

describe('rulesToForm(停调规则预填)', () => {
  it('详情规则数组 → 表单行(keywords 逗号串,数字转字符串)', () => {
    const form = rulesToForm([
      { error_code: 403, keywords: ['risk', 'unusual'], duration_minutes: 30, description: '风控' },
      { error_code: 429, keywords: [], duration_minutes: 15 },
    ])
    expect(form).toHaveLength(2)
    expect(form[0]).toEqual({ errorCode: '403', keywords: 'risk, unusual', durationMinutes: '30', description: '风控' })
    expect(form[1]).toEqual({ errorCode: '429', keywords: '', durationMinutes: '15', description: '' })
  })
  it('缺省/非数组 → 空数组(不预填出脏行)', () => {
    expect(rulesToForm(undefined)).toEqual([])
  })
  it('formFromAccount 预填现值(变异:若丢弃 rules 则替换列表为空,会误清空)', () => {
    const acct = { ...base, temp_unschedulable_rules: [{ error_code: 403, keywords: ['x'], duration_minutes: 20 }] } as ProviderAccount
    expect(formFromAccount(acct).tempUnschedulableRules).toHaveLength(1)
  })
})
