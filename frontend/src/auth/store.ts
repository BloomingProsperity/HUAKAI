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
const SESSION_SYNC_CHANNEL = 'huakai_session_token_sync'
const SESSION_SYNC_STORAGE = 'huakai_session_token_sync_event'

const EMPTY: AuthState = { sessionToken: null, refreshToken: null, sessionExpiresAt: null, adminToken: null, user: null }

interface SessionTokenSyncMessage {
  type: 'session_tokens'
  sourceId: string
  tokens: SessionTokens
}

const STORE_INSTANCE_ID = `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
let sessionSyncReady = false
let sessionSyncChannel: BroadcastChannel | null = null

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

function browserWindow(): (Window & typeof globalThis) | null {
  return typeof window === 'undefined' ? null : window
}

function isSessionTokens(value: unknown): value is SessionTokens {
  if (!value || typeof value !== 'object') return false
  const tokens = value as Partial<SessionTokens>
  return (
    typeof tokens.sessionToken === 'string' &&
    (typeof tokens.refreshToken === 'string' || tokens.refreshToken === null) &&
    (typeof tokens.sessionExpiresAt === 'string' || tokens.sessionExpiresAt === null)
  )
}

function applyRemoteSessionTokens(data: unknown) {
  const msg = data as Partial<SessionTokenSyncMessage>
  if (msg?.type !== 'session_tokens' || msg.sourceId === STORE_INSTANCE_ID || !isSessionTokens(msg.tokens)) return
  state = {
    ...state,
    sessionToken: msg.tokens.sessionToken,
    refreshToken: msg.tokens.refreshToken,
    sessionExpiresAt: msg.tokens.sessionExpiresAt,
  }
  persist()
}

function publishViaStorage(win: Window & typeof globalThis, msg: SessionTokenSyncMessage) {
  try {
    win.localStorage.setItem(SESSION_SYNC_STORAGE, JSON.stringify(msg))
    win.localStorage.removeItem(SESSION_SYNC_STORAGE)
  } catch {
    /* storage 不可用时跨标签同步降级为 no-op */
  }
}

function initSessionTokenSync() {
  if (sessionSyncReady) return
  const win = browserWindow()
  if (!win) return
  sessionSyncReady = true

  try {
    if (typeof win.BroadcastChannel === 'function') {
      sessionSyncChannel = new win.BroadcastChannel(SESSION_SYNC_CHANNEL)
      sessionSyncChannel.onmessage = (ev) => applyRemoteSessionTokens(ev.data)
    }
  } catch {
    sessionSyncChannel = null
  }

  try {
    win.addEventListener('storage', (ev: StorageEvent) => {
      if (ev.key !== SESSION_SYNC_STORAGE || !ev.newValue) return
      try {
        applyRemoteSessionTokens(JSON.parse(ev.newValue))
      } catch {
        /* 非本模块消息,忽略 */
      }
    })
  } catch {
    /* 无 window 事件能力时降级为 no-op */
  }
}

function broadcastSessionTokens(tokens: SessionTokens) {
  const win = browserWindow()
  if (!win) return
  initSessionTokenSync()
  const msg: SessionTokenSyncMessage = { type: 'session_tokens', sourceId: STORE_INSTANCE_ID, tokens }
  if (sessionSyncChannel) {
    try {
      sessionSyncChannel.postMessage(msg)
      return
    } catch {
      /* BroadcastChannel 发送失败时走 storage 回退 */
    }
  }
  publishViaStorage(win, msg)
}

initSessionTokenSync()

export function getTokens(): TokenSet {
  return { sessionToken: state.sessionToken, adminToken: state.adminToken }
}

/** 当前已登录用户(非 hook);无则 null。供 me 壳判定「同人 token 轮换 vs 换人登录」。 */
export function getAuthUser(): AuthUser | null {
  return state.user
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
  broadcastSessionTokens(tokens)
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
