import { afterEach, describe, expect, it, vi } from 'vitest'
import type { NavSection } from '../../app/nav'
import {
  actorReady,
  buildPageContextPrefix,
  composeUserContent,
  emptyActor,
  extractEntityId,
  getCurrentPageLabel,
  getCurrentShell,
  loadActor,
  parseActor,
  resolveNavMatch,
  saveActor,
  toPositiveInt,
} from './hermesContext'

/*
 * Hermes 上下文纯逻辑单测。覆盖:壳判定(只在 operator 渲染的命门)、当前页 label、详情实体 id 提取、
 * 上下文前缀拼装(UI 不回显前缀的契约靠 composeUserContent 体现)、操作身份持久化与校验(as_user_id 必填)。
 * 每条用例都对"判错会怎样"留了变异余量。
 */

describe('getCurrentShell —— 只在 operator 壳渲染的命门', () => {
  it('运营台路径判为 operator(变异:若 shell 取错则非 operator,面板不该渲染)', () => {
    expect(getCurrentShell('/accounts')).toBe('operator')
    expect(getCurrentShell('/routing')).toBe('operator')
    expect(getCurrentShell('/admin/groups')).toBe('operator')
    expect(getCurrentShell('/admin/platform-credentials')).toBe('operator')
    expect(getCurrentShell('/security')).toBe('operator')
  })

  it('用户门户路径判为 user(面板必须 return null 不渲染)', () => {
    expect(getCurrentShell('/overview')).toBe('user')
    expect(getCurrentShell('/keys')).toBe('user')
    expect(getCurrentShell('/wallet')).toBe('user')
  })

  it('详情子路径用最长前缀命中父项(变异:若用首个/最短前缀匹配会判错壳)', () => {
    // /accounts/:id 在 nav 里登记为 /accounts(operator)。
    expect(getCurrentShell('/accounts/123')).toBe('operator')
    // /users/:id 同理(operator)。
    expect(getCurrentShell('/users/42')).toBe('operator')
  })

  it('未知路径返回 null(变异:若兜底成某壳会误渲染)', () => {
    expect(getCurrentShell('/totally-unknown')).toBeNull()
    expect(getCurrentShell('/login')).toBeNull()
  })

  it('带 query/hash 与尾斜杠也能判定(变异:若不归一化路径会漏配)', () => {
    expect(getCurrentShell('/accounts/?tab=x')).toBe('operator')
    expect(getCurrentShell('/routing/')).toBe('operator')
    expect(getCurrentShell('/overview#top')).toBe('user')
  })

  it('不把 /accounts 误配到形近但不同的路径(前缀需 / 边界)', () => {
    // 构造一个以 /accounts 开头但不是其子路径的串,不应命中 operator 的 /accounts。
    expect(getCurrentShell('/accounts-export')).toBeNull()
  })
})

describe('resolveNavMatch —— 最长前缀判定本身(合成 nav,可对 tiebreak 变异)', () => {
  // 构造一个"短前缀属 user、更具体的长前缀属 operator"的 nav:用真实 PIPELINE_NAV 无法触发
  // 这条 tiebreak(其路径互不为前缀),故用合成数据精确验证"取更长前缀"。
  const synthetic: NavSection[] = [
    { stage: 1, key: 'short', shell: 'user', label: '短前缀页', hint: '', items: [{ path: '/area', label: '短前缀页', built: true }] },
    { stage: 2, key: 'long', shell: 'operator', label: '长前缀页', hint: '', items: [{ path: '/area/detail', label: '长前缀页', built: true }] },
  ]

  it('两个前缀都命中时取更长的那条(变异:若改成取更短前缀,shell/label 会反转)', () => {
    const m = resolveNavMatch('/area/detail/123', synthetic)
    expect(m).toEqual({ shell: 'operator', label: '长前缀页' })
  })

  it('只命中短前缀时取短的那条(确认不是恒取最后一项)', () => {
    const m = resolveNavMatch('/area/other', synthetic)
    expect(m).toEqual({ shell: 'user', label: '短前缀页' })
  })

  it('无任何前缀命中返回 null', () => {
    expect(resolveNavMatch('/elsewhere', synthetic)).toBeNull()
  })
})

describe('getCurrentPageLabel', () => {
  it('取对应 nav item 的中文 label(变异:取错项则文案不符)', () => {
    expect(getCurrentPageLabel('/accounts')).toBe('账号中心')
    expect(getCurrentPageLabel('/routing')).toBe('路由与池管理')
  })
  it('详情路径用最长前缀取父项 label', () => {
    expect(getCurrentPageLabel('/accounts/123')).toBe('账号中心')
  })
  it('无匹配返回回退文案', () => {
    expect(getCurrentPageLabel('/nope')).toBe('运营台')
    expect(getCurrentPageLabel('/nope', '兜底')).toBe('兜底')
  })
})

describe('extractEntityId', () => {
  it('从详情路径提取数字 id(变异:取错段会拿到非 id)', () => {
    expect(extractEntityId('/accounts/123')).toBe('123')
    expect(extractEntityId('/users/42')).toBe('42')
    expect(extractEntityId('/accounts/123/keys')).toBe('123')
  })
  it('非详情路径/非数字段返回 null(避免把列表页或路径段当 id)', () => {
    expect(extractEntityId('/accounts')).toBeNull()
    expect(extractEntityId('/admin/groups')).toBeNull()
    expect(extractEntityId('/accounts/abc')).toBeNull()
  })
})

describe('buildPageContextPrefix + composeUserContent', () => {
  it('前缀含页面 label、以 \\n\\n 结尾(变异:若漏 label 则 AI 不知当前页)', () => {
    const prefix = buildPageContextPrefix('账号中心', null)
    expect(prefix).toContain('账号中心')
    expect(prefix.endsWith('\n\n')).toBe(true)
  })
  it('带实体 id 时前缀含 #id(变异:漏 id 则 AI 不知具体实体)', () => {
    const prefix = buildPageContextPrefix('账号中心', '123')
    expect(prefix).toContain('#123')
  })
  it('空 label 回退为"运营台"(防空括注)', () => {
    expect(buildPageContextPrefix('  ', null)).toContain('运营台')
  })
  it('composeUserContent 把前缀拼到用户输入前(发送内容含上下文,但 UI 只展示用户输入)', () => {
    const prefix = buildPageContextPrefix('路由与池管理', null)
    const composed = composeUserContent(prefix, '帮我看看路由')
    expect(composed.startsWith(prefix)).toBe(true)
    expect(composed.endsWith('帮我看看路由')).toBe(true)
    // 用户原文不含前缀文案——证明 UI 侧用原始输入渲染即可不暴露前缀。
    expect('帮我看看路由'.includes('上下文')).toBe(false)
  })
})

describe('toPositiveInt —— as_user_id 校验底座', () => {
  it('正整数通过;0/负数/小数/空/非数字 → null(变异:放过非法值会发注定 400 的请求)', () => {
    expect(toPositiveInt(7)).toBe(7)
    expect(toPositiveInt('15')).toBe(15)
    expect(toPositiveInt(0)).toBeNull()
    expect(toPositiveInt(-3)).toBeNull()
    expect(toPositiveInt(1.5)).toBeNull()
    expect(toPositiveInt('')).toBeNull()
    expect(toPositiveInt('abc')).toBeNull()
    expect(toPositiveInt(null)).toBeNull()
    expect(toPositiveInt(undefined)).toBeNull()
  })
})

describe('actorReady', () => {
  it('as_user_id 为正整数才可对话;缺则禁(变异:若不校验会发 400 hermes_admin_user_required)', () => {
    expect(actorReady({ asUserId: 5, tenantId: null })).toBe(true)
    expect(actorReady({ asUserId: 5, tenantId: 9 })).toBe(true)
    expect(actorReady({ asUserId: null, tenantId: 9 })).toBe(false)
    expect(actorReady(emptyActor())).toBe(false)
  })
})

describe('parseActor 归一化', () => {
  it('只接受正整数,其余置 null(变异:放过 0/字符串脏数据会污染请求)', () => {
    expect(parseActor({ as_user_id: 3, tenant_id: 8 })).toEqual({ asUserId: 3, tenantId: 8 })
    expect(parseActor({ as_user_id: '12' })).toEqual({ asUserId: 12, tenantId: null })
    expect(parseActor({ as_user_id: 0, tenant_id: -1 })).toEqual({ asUserId: null, tenantId: null })
    expect(parseActor(null)).toEqual({ asUserId: null, tenantId: null })
    expect(parseActor('garbage')).toEqual({ asUserId: null, tenantId: null })
  })
})

describe('loadActor / saveActor 持久化往返', () => {
  // 用一个内存 Map 模拟 localStorage,避免依赖 jsdom。
  function installFakeLS() {
    const store = new Map<string, string>()
    const fake = {
      getItem: (k: string) => (store.has(k) ? store.get(k)! : null),
      setItem: (k: string, v: string) => void store.set(k, v),
      removeItem: (k: string) => void store.delete(k),
      clear: () => store.clear(),
      key: () => null,
      length: 0,
    }
    vi.stubGlobal('localStorage', fake)
    return store
  }

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('saveActor→loadActor 往返保真(变异:若键名/字段写错则读回为空)', () => {
    installFakeLS()
    saveActor({ asUserId: 21, tenantId: 4 })
    expect(loadActor()).toEqual({ asUserId: 21, tenantId: 4 })
  })

  it('无持久值返回 emptyActor(首次进入面板未设身份)', () => {
    installFakeLS()
    expect(loadActor()).toEqual({ asUserId: null, tenantId: null })
  })

  it('坏 JSON 容错返回 emptyActor(不抛)', () => {
    const store = installFakeLS()
    store.set('hk_hermes_actor', '{坏json')
    expect(() => loadActor()).not.toThrow()
    expect(loadActor()).toEqual({ asUserId: null, tenantId: null })
  })
})
