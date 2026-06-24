import { useSyncExternalStore } from 'react'
import type { TokenSet } from './tokenForPath'

/*
 * 前端 auth 状态:双 token(用户 session + 运维 admin)+ 当前用户,localStorage 持久化。
 * 模块级 store + useSyncExternalStore,任意组件经 useAuth() 订阅。lib/api 经 getTokens() 读注入。
 * 安全:token 存 localStorage(单页运维台可接受;XSS 风险由 CSP/依赖审计兜底),绝不打印到日志。
 */
export interface AuthUser {
  user_id: number
  email: string
  display_name?: string
}

interface AuthState {
  sessionToken: string | null
  adminToken: string | null
  user: AuthUser | null
}

const LS_SESSION = 'hk_session_token'
const LS_ADMIN = 'hk_admin_token'
const LS_USER = 'hk_user'

function readLS(): AuthState {
  try {
    const userRaw = localStorage.getItem(LS_USER)
    return {
      sessionToken: localStorage.getItem(LS_SESSION),
      adminToken: localStorage.getItem(LS_ADMIN),
      user: userRaw ? (JSON.parse(userRaw) as AuthUser) : null,
    }
  } catch {
    return { sessionToken: null, adminToken: null, user: null }
  }
}

let state: AuthState = readLS()
const listeners = new Set<() => void>()

function emit() {
  for (const l of listeners) l()
}

function persist() {
  try {
    state.sessionToken ? localStorage.setItem(LS_SESSION, state.sessionToken) : localStorage.removeItem(LS_SESSION)
    state.adminToken ? localStorage.setItem(LS_ADMIN, state.adminToken) : localStorage.removeItem(LS_ADMIN)
    state.user ? localStorage.setItem(LS_USER, JSON.stringify(state.user)) : localStorage.removeItem(LS_USER)
  } catch {
    /* localStorage 不可用时仅内存态,忽略 */
  }
}

export function getTokens(): TokenSet {
  return { sessionToken: state.sessionToken, adminToken: state.adminToken }
}

export function setSession(token: string, user: AuthUser) {
  state = { ...state, sessionToken: token, user }
  persist()
  emit()
}

export function setAdminToken(token: string | null) {
  state = { ...state, adminToken: token && token.trim() ? token.trim() : null }
  persist()
  emit()
}

export function clearSession() {
  state = { ...state, sessionToken: null, user: null }
  persist()
  emit()
}

export function clearAll() {
  state = { sessionToken: null, adminToken: null, user: null }
  persist()
  emit()
}

function subscribe(cb: () => void): () => void {
  listeners.add(cb)
  return () => listeners.delete(cb)
}

function snapshot(): AuthState {
  return state
}

export interface AuthView extends AuthState {
  isLoggedIn: boolean
  hasAdminToken: boolean
}

export function useAuth(): AuthView {
  const s = useSyncExternalStore(subscribe, snapshot)
  return { ...s, isLoggedIn: s.sessionToken != null, hasAdminToken: s.adminToken != null }
}
