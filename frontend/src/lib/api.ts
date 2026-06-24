/*
 * HUAKAI 前端 API 客户端基座。
 *
 * 统一封装对网关的 fetch:同源 BASE(生产期前端由网关 go:embed 提供,与 API 同源)、
 * JSON 编解码、错误归一化(把网关的 {error:{code,message}} 形态抽成 ApiError)、
 * session/hk_key 鉴权头透传。各业务模块只调 apiGet/apiSend,不直接碰 fetch。
 *
 * 鉴权:HUAKAI 网关支持 session(cookie)与 hk_key(Bearer)混合鉴权。默认带 cookie
 * (credentials:include);若上层显式传 bearer 则加 Authorization 头。
 */

// API_BASE 默认空串=同源相对路径(生产期);dev 期 vite proxy 把 /api 转发到本地网关。
const API_BASE = ''

export interface ApiErrorShape {
  code: string
  message: string
}

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

export interface RequestOptions {
  /** 显式 Bearer(hk_key);省略则仅靠 cookie session。 */
  bearer?: string
  /** 查询参数。 */
  query?: Record<string, string | number | boolean | undefined>
  signal?: AbortSignal
}

function buildURL(path: string, query?: RequestOptions['query']): string {
  const url = new URL(API_BASE + path, window.location.origin)
  if (query) {
    for (const [k, v] of Object.entries(query)) {
      if (v !== undefined) {
        url.searchParams.set(k, String(v))
      }
    }
  }
  // 返回相对路径(同源),保留 query。
  return url.pathname + url.search
}

async function parse<T>(resp: Response): Promise<T> {
  const text = await resp.text()
  const body = text ? JSON.parse(text) : undefined
  if (!resp.ok) {
    // 网关错误约定:{ error: { code, message } };做防御性回退。
    const errShape = (body && (body.error as ApiErrorShape)) || undefined
    throw new ApiError(
      resp.status,
      errShape?.code || `http_${resp.status}`,
      errShape?.message || resp.statusText || '请求失败',
    )
  }
  return body as T
}

function authHeaders(bearer?: string): HeadersInit {
  return bearer ? { Authorization: `Bearer ${bearer}` } : {}
}

export async function apiGet<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const resp = await fetch(buildURL(path, opts.query), {
    method: 'GET',
    credentials: 'include',
    headers: { Accept: 'application/json', ...authHeaders(opts.bearer) },
    signal: opts.signal,
  })
  return parse<T>(resp)
}

export async function apiSend<T>(
  method: 'POST' | 'PATCH' | 'PUT' | 'DELETE',
  path: string,
  payload?: unknown,
  opts: RequestOptions = {},
): Promise<T> {
  const resp = await fetch(buildURL(path, opts.query), {
    method,
    credentials: 'include',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
      ...authHeaders(opts.bearer),
    },
    body: payload === undefined ? undefined : JSON.stringify(payload),
    signal: opts.signal,
  })
  return parse<T>(resp)
}
