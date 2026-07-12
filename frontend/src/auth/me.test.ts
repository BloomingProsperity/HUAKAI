import { describe, it, expect } from 'vitest'
import { deriveShellAccess, isSameIdentity, nextMeState, visibleNavSections, type MeState } from './me'
import type { NavSection } from '../app/nav'

/*
 * role 制单登录前端切壳的安全判定测试。核心不变量(deny-by-default):
 *  - 唯有确切 ready+admin 才启用运营台并可见两壳;
 *  - loading / degraded / user / none / null 一律仅用户壳(不提权 + 不白屏);
 *  - 拉取失败(degraded)必须清空 panel,绝不残留上一次的 admin。
 */

const READY_ADMIN: MeState = { status: 'ready', panel: 'admin', userId: 1, tenantId: 1, displayName: 'a' }
const READY_USER: MeState = { status: 'ready', panel: 'user', userId: 2, tenantId: 1, displayName: 'u' }

describe('deriveShellAccess:deny-by-default', () => {
  it('ready+admin → 启用运营台 + 可见两壳', () => {
    const a = deriveShellAccess(READY_ADMIN)
    expect(a.operatorEnabled).toBe(true)
    expect(a.visibleShells).toEqual(['user', 'operator'])
  })

  it('ready+user → 仅用户壳,不启用运营台', () => {
    const a = deriveShellAccess(READY_USER)
    expect(a.operatorEnabled).toBe(false)
    expect(a.visibleShells).toEqual(['user'])
  })

  it('panel==="none" → 仅用户壳(非 admin 一律降到用户壳)', () => {
    const a = deriveShellAccess({ ...READY_ADMIN, panel: 'none' })
    expect(a.operatorEnabled).toBe(false)
    expect(a.visibleShells).toEqual(['user'])
  })

  it('loading 态即便 panel 恰为 admin 也不启用运营台(未确证不提权)', () => {
    // 关键:status 必须是 ready;loading 时不认 panel(防抢跑提权)。
    const a = deriveShellAccess({ status: 'loading', panel: 'admin', userId: 1, tenantId: 1, displayName: 'a' })
    expect(a.operatorEnabled).toBe(false)
    expect(a.visibleShells).toEqual(['user'])
  })

  it('degraded 态一律仅用户壳(降级不提权、不白屏)', () => {
    const a = deriveShellAccess({ status: 'degraded', panel: null, userId: null, tenantId: null, displayName: null })
    expect(a.operatorEnabled).toBe(false)
    // 关键:即便降级也永远至少有用户壳,绝不空数组(白屏)。
    expect(a.visibleShells).toEqual(['user'])
    expect(a.visibleShells.length).toBeGreaterThan(0)
  })

  it('idle 态仅用户壳', () => {
    const a = deriveShellAccess({ status: 'idle', panel: null, userId: null, tenantId: null, displayName: null })
    expect(a.operatorEnabled).toBe(false)
    expect(a.visibleShells).toEqual(['user'])
  })
})

const IDLE: MeState = { status: 'idle', panel: null, userId: null, tenantId: null, displayName: null }
const LOADING: MeState = { status: 'loading', panel: null, userId: null, tenantId: null, displayName: null }

describe('nextMeState:上一状态 + 拉取结果 → 状态迁移', () => {
  it('ok → ready + 带回 panel/id(无论此前状态)', () => {
    const s = nextMeState(IDLE, { kind: 'ok', me: { panel: 'admin', user_id: 7, tenant_id: 3, display_name: 'x' } })
    expect(s).toEqual({ status: 'ready', panel: 'admin', userId: 7, tenantId: 3, displayName: 'x' })
  })

  it('ok 能把 degraded 升到 ready(重验成功恢复)', () => {
    const degraded: MeState = { status: 'degraded', panel: null, userId: null, tenantId: null, displayName: null }
    const s = nextMeState(degraded, { kind: 'ok', me: { panel: 'user', user_id: 5, tenant_id: 1, display_name: 'u' } })
    expect(s.status).toBe('ready')
    expect(s.panel).toBe('user')
  })

  it('首拉失败(prev 非 ready)→ degraded 且 panel 清空(deny-by-default,防提权)', () => {
    const s = nextMeState(LOADING, { kind: 'error' })
    expect(s.status).toBe('degraded')
    expect(s.panel).toBeNull()
    expect(deriveShellAccess(s).operatorEnabled).toBe(false)
  })

  it('重验失败(prev 已 ready)→ 保留上次良好态(瞬时 5xx 不抖掉在用 admin 的壳)', () => {
    const s = nextMeState(READY_ADMIN, { kind: 'error' })
    expect(s).toEqual(READY_ADMIN)
    expect(deriveShellAccess(s).operatorEnabled).toBe(true)
  })

  it('no-token → idle 空态(无论此前是否 admin)', () => {
    const s = nextMeState(READY_ADMIN, { kind: 'no-token' })
    expect(s).toEqual({ status: 'idle', panel: null, userId: null, tenantId: null, displayName: null })
  })
})

describe('isSameIdentity:同人重验 vs 换人(防跨身份残留提权)', () => {
  it('同人(ready + user_id 相同)→ true(视为重验,不清陈旧壳)', () => {
    expect(isSameIdentity(READY_ADMIN, 7, 7)).toBe(true)
  })

  it('换人(user_id 不同)→ false(必须先清态,防 B 沿用 A 的 admin 壳)', () => {
    // 关键提权防线:A(admin,id=7)未登出,B(id=8)在同标签页登录。
    expect(isSameIdentity(READY_ADMIN, 7, 8)).toBe(false)
  })

  it('尚无确证身份(prev user_id 为 null)→ false(不认陈旧,重新拉)', () => {
    expect(isSameIdentity(READY_ADMIN, null, 7)).toBe(false)
  })

  it('当前登录者身份不明(currentUserId 为 null)→ false(deny-by-default,不复用)', () => {
    expect(isSameIdentity(READY_ADMIN, 7, null)).toBe(false)
  })

  it('非 ready 态(loading/degraded)一律 false(未确证不复用)', () => {
    expect(isSameIdentity(LOADING, 7, 7)).toBe(false)
    const degraded: MeState = { status: 'degraded', panel: null, userId: null, tenantId: null, displayName: null }
    expect(isSameIdentity(degraded, 7, 7)).toBe(false)
  })
})

describe('visibleNavSections:按壳过滤', () => {
  const NAV: NavSection[] = [
    { stage: 1, key: 'u1', shell: 'user', label: '概览', hint: '', items: [{ path: '/overview', label: '概览', built: true }] },
    { stage: 1, key: 'o1', shell: 'operator', label: '账号池', hint: '', items: [{ path: '/accounts', label: '账号', built: true }] },
  ]

  it('admin(两壳)→ 保留全部 section', () => {
    const out = visibleNavSections(NAV, deriveShellAccess(READY_ADMIN))
    expect(out.map((s) => s.key)).toEqual(['u1', 'o1'])
  })

  it('user(仅用户壳)→ 滤掉 operator section', () => {
    const out = visibleNavSections(NAV, deriveShellAccess(READY_USER))
    expect(out.map((s) => s.key)).toEqual(['u1'])
  })

  it('degraded → 同样滤掉 operator(降级看不到运营台入口)', () => {
    const degraded: MeState = { status: 'degraded', panel: null, userId: null, tenantId: null, displayName: null }
    const out = visibleNavSections(NAV, deriveShellAccess(degraded))
    expect(out.map((s) => s.key)).toEqual(['u1'])
  })
})
