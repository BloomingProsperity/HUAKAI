import { useSyncExternalStore } from 'react'
import { apiGet, ApiError } from '../lib/api'
import { getAuthUser, getTokens } from './store'
import type { NavSection, Shell } from '../app/nav'

/*
 * 登录身份(role/panel)状态 —— role 制单登录的前端切壳权威来源。
 *
 * 设计要点:
 *  - 权威来源是后端 /v1/auth/me 的 panel 字段(来自 users.role,绝不信前端声明)。
 *  - getMe 走 best-effort:只在已登录后由壳挂载触发,失败绝不阻断登录或白屏。
 *    这也让「登录方式无关」成立——无论邮箱密码还是 OAuth 回调(OAuthCallbackPage
 *    的 setSessionTokens)进来,壳一挂载就会拉 panel,OAuth admin 不会漏判。
 *  - deny-by-default:唯有确切拿到 panel==='admin' 才启用运营台;加载中 / 降级 /
 *    user / none / 空 一律仅用户壳。降级绝不默认 admin 壳(防提权),也绝不空壳(防白屏)。
 *
 * 前端裁剪仅为体验,不是授权边界:真正的边界是后端每个 admin 端点的独立鉴权。
 */

/** /v1/auth/me 响应 —— 仅面板归属与自身 id,无敏感字段。镜像 controlhttp meResponse。 */
export interface MeResponse {
  panel: string
  user_id: number
  tenant_id: number
  display_name: string
}

/** me 拉取状态:idle(未登录/未拉)、loading(在途)、ready(已得 panel)、degraded(拉取失败,deny-by-default)。 */
export type MeStatus = 'idle' | 'loading' | 'ready' | 'degraded'

export interface MeState {
  status: MeStatus
  /** 后端 panel:"admin" / "user" / "none";未知为 null。 */
  panel: string | null
  userId: number | null
  tenantId: number | null
  displayName: string | null
}

/** 据 me 状态推导的壳访问权:可见哪些壳 + 是否启用运营台能力。 */
export interface ShellAccess {
  operatorEnabled: boolean
  visibleShells: Shell[]
}

const EMPTY: MeState = { status: 'idle', panel: null, userId: null, tenantId: null, displayName: null }

let state: MeState = { ...EMPTY }
// 产生当前 state 的登录者 user_id(身份指纹)。用于区分「同人 token 轮换(重验,不清态)」
// 与「换人登录(必须先清陈旧 admin,防跨身份残留提权)」。
let stateUserId: number | null = null
const listeners = new Set<() => void>()

function emit() {
  for (const l of listeners) l()
}

function setState(next: MeState) {
  state = next
  emit()
}

/** me 拉取结果三态,喂给纯状态迁移函数。 */
export type MeFetchResult =
  | { kind: 'ok'; me: MeResponse }
  | { kind: 'error' }
  | { kind: 'no-token' }

/**
 * nextMeState 据【上一个状态 + 拉取结果】算下一个 MeState(纯函数,便于变异测试)。
 * - no-token → idle(未登录)。
 * - ok → ready + panel(无论此前是什么,拿到确证就升到 ready)。
 * - error:分两种——
 *   · 首拉失败(prev 非 ready,尚无确证身份)→ degraded 且清空 panel(deny-by-default,
 *     降到最小只读用户壳:不提权、不白屏)。
 *   · 重验失败(prev 已是 ready,有已知良好身份)→ 保留 prev,避免瞬时 5xx 抖掉在用会话的壳。
 * 关键不变量:degraded 分支绝不残留 panel;error 绝不把非 admin 抬成 admin。
 */
export function nextMeState(prev: MeState, result: MeFetchResult): MeState {
  switch (result.kind) {
    case 'no-token':
      return { ...EMPTY }
    case 'ok':
      return {
        status: 'ready',
        panel: result.me.panel,
        userId: result.me.user_id,
        tenantId: result.me.tenant_id,
        displayName: result.me.display_name,
      }
    case 'error':
      if (prev.status === 'ready') return prev
      return { status: 'degraded', panel: null, userId: null, tenantId: null, displayName: null }
  }
}

/**
 * isSameIdentity:判断当前 me 状态是否属于「同一位已登录者」的可复用态(纯函数)。
 * 仅当已 ready、且产生它的 user_id 与当前登录者一致(均非空)时为真——此时是同人 token 轮换/重验,
 * 保持壳不闪。换人登录(user_id 变了)/尚无确证(非 ready)/身份不明(任一为 null)一律为假,
 * 触发先清态(deny-by-default),防跨身份残留 admin。
 */
export function isSameIdentity(s: MeState, prevUserId: number | null, currentUserId: number | null): boolean {
  return s.status === 'ready' && prevUserId !== null && prevUserId === currentUserId
}

/**
 * refreshMe 拉取 /v1/auth/me 并写入状态(best-effort)。
 * 无 session token → 归为 idle(未登录),不发请求。
 * 换人/首拉(非同人重验)→ 先清成 loading 且清空 panel(deny-by-default,绝不残留上一位 admin);
 * 同人 token 轮换(重验)→ 保持当前态直到新结果,避免壳闪烁。
 * 请求异常/5xx → 交由 nextMeState 决定(换人/首拉降级到 degraded / 同人重验保留),绝不抛错、绝不阻断调用方。
 * signal 供组件卸载/token 变更时取消;取消(AbortError)不改状态,保留上一次结果。
 */
export async function refreshMe(signal?: AbortSignal): Promise<void> {
  const session = getTokens().sessionToken
  if (!session) {
    setState(nextMeState(state, { kind: 'no-token' }))
    stateUserId = null
    return
  }
  const currentUserId = getAuthUser()?.user_id ?? null
  // 换人/首拉:先把壳降到最小用户壳(loading 清空 panel),确保跨身份切换从 deny-by-default 起步,
  // 且即便随后拉取 5xx(nextMeState 遇非 ready 走 degraded),也绝不残留上一位的 admin。
  if (!isSameIdentity(state, stateUserId, currentUserId)) {
    setState({ status: 'loading', panel: null, userId: null, tenantId: null, displayName: null })
  }
  try {
    // /v1/auth/me 在 /v1/auth/* 前缀下,tokenForPath 刻意返回 null,必须显式带 session bearer。
    const me = await apiGet<MeResponse>('/v1/auth/me', { bearer: session, signal })
    setState(nextMeState(state, { kind: 'ok', me }))
    stateUserId = me.user_id
  } catch (e) {
    // 组件卸载/token 变更触发的取消:不改状态。
    if (isAbort(e)) return
    setState(nextMeState(state, { kind: 'error' }))
    stateUserId = currentUserId
  }
}

/** resetMe 复位为未登录态(登出 / session 被清时调用),连带清身份指纹。 */
export function resetMe(): void {
  stateUserId = null
  setState({ ...EMPTY })
}

function isAbort(e: unknown): boolean {
  if (e instanceof DOMException && e.name === 'AbortError') return true
  // fetch abort 在部分环境抛普通 Error(name==='AbortError');ApiError 不是取消。
  if (e instanceof ApiError) return false
  return e instanceof Error && e.name === 'AbortError'
}

function subscribe(cb: () => void): () => void {
  listeners.add(cb)
  return () => listeners.delete(cb)
}

function snapshot(): MeState {
  return state
}

/** 读当前 me 状态(非 hook,供纯逻辑/测试)。 */
export function getMeState(): MeState {
  return state
}

/**
 * deriveShellAccess:据 me 状态推导壳访问权(纯函数,便于变异测试)。
 * 唯有 status==='ready' 且 panel==='admin' 才启用运营台并可见两壳;
 * 其余(loading/degraded/user/none/null)一律 operatorEnabled=false + 仅用户壳。
 * 这条判定同时守住两个不变量:降级/加载不提权(不默认 admin 壳)、且永远至少有用户壳(不白屏)。
 */
export function deriveShellAccess(s: MeState): ShellAccess {
  const isAdmin = s.status === 'ready' && s.panel === 'admin'
  return isAdmin
    ? { operatorEnabled: true, visibleShells: ['user', 'operator'] }
    : { operatorEnabled: false, visibleShells: ['user'] }
}

/** visibleNavSections:按壳访问权过滤 nav sections(纯函数)。仅保留 visibleShells 内的壳。 */
export function visibleNavSections(sections: NavSection[], access: ShellAccess): NavSection[] {
  const allowed = new Set(access.visibleShells)
  return sections.filter((s) => allowed.has(s.shell))
}

export interface MeView extends MeState {
  access: ShellAccess
}

/**
 * useMe:仅订阅 me 状态(不触发拉取)。任意组件可安全并发使用,不会引起重复请求。
 * 拉取的唯一触发点是壳(AppShell 的 bootstrap effect),对邮箱密码与 OAuth 两条登录路径一视同仁。
 */
export function useMe(): MeView {
  const s = useSyncExternalStore(subscribe, snapshot)
  return { ...s, access: deriveShellAccess(s) }
}
