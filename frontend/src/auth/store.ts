import { useSyncExternalStore } from 'react'
import type { SessionTokens } from './refresh'
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
  /** refresh token(husr_),用于在 session 到期前换新;长 TTL。 */
  refreshToken: string | null
  /** session token 到期时刻(RFC3339),驱动主动刷新。 */
  sessionExpiresAt: string | null
  adminToken: string | null
  user: AuthUser | null
}

const LS_SESSION = 'hk_session_token'
const LS_REFRESH = 'hk_refresh_token'
const LS_EXPIRY = 'hk_session_expiry'
const LS_ADMIN = 'hk_admin_token'
const LS_USER = 'hk_user'

const EMPTY: AuthState = { sessionToken: null, refreshToken: null, sessionExpiresAt: null, adminToken: null, user: null }

function readLS(): AuthState {
  try {
    const userRaw = localStorage.getItem(LS_USER)
    return {
      sessionToken: localStorage.getItem(LS_SESSION),
      refreshToken: localStorage.getItem(LS_REFRESH),
      sessionExpiresAt: localStorage.getItem(LS_EXPIRY),
      adminToken: localStorage.getItem(LS_ADMIN),
      user: userRaw ? (JSON.parse(userRaw) as AuthUser) : null,
    }
  } catch {
    return { ...EMPTY }
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
    state.refreshToken ? localStorage.setItem(LS_REFRESH, state.refreshToken) : localStorage.removeItem(LS_REFRESH)
    state.sessionExpiresAt ? localStorage.setItem(LS_EXPIRY, state.sessionExpiresAt) : localStorage.removeItem(LS_EXPIRY)
    state.adminToken ? localStorage.setItem(LS_ADMIN, state.adminToken) : localStorage.removeItem(LS_ADMIN)
    state.user ? localStorage.setItem(LS_USER, JSON.stringify(state.user)) : localStorage.removeItem(LS_USER)
  } catch {
    /* localStorage 不可用时仅内存态,忽略 */
  }
}

export function getTokens(): TokenSet {
  return { sessionToken: state.sessionToken, adminToken: state.adminToken }
}

/** 当前 refresh token(供刷新客户端读取);无则 null。 */
export function getRefreshToken(): string | null {
  return state.refreshToken
}

/** 当前 session token 到期时刻(RFC3339);供主动刷新判定。 */
export function getSessionExpiry(): string | null {
  return state.sessionExpiresAt
}

/**
 * 写入登录/刷新返回的完整会话 token 组。user 省略时保留现有 user
 * (刷新响应不带 user,只换 token)。
 */
export function setSessionTokens(tokens: SessionTokens, user?: AuthUser) {
  state = {
    ...state,
    sessionToken: tokens.sessionToken,
    refreshToken: tokens.refreshToken,
    sessionExpiresAt: tokens.sessionExpiresAt,
    user: user ?? state.user,
  }
  persist()
  emit()
}

export function setAdminToken(token: string | null) {
  state = { ...state, adminToken: token && token.trim() ? token.trim() : null }
  persist()
  emit()
}

export function clearSession() {
  state = { ...state, sessionToken: null, refreshToken: null, sessionExpiresAt: null, user: null }
  persist()
  emit()
}

export function clearAll() {
  state = { ...EMPTY }
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
