/*
 * 会话刷新客户端:用 refresh token 调 POST /v1/sessions/refresh 换新 session token。
 *
 * 设计要点:
 *  - 用 single-flight 合并并发刷新为一次在途请求(避免同一 refresh token 被并发重复消费,
 *    触发后端 family 重放检测撤销整条会话族)。
 *  - 刻意用裸 fetch(不走 lib/api 的 apiSend):①apiSend 会做「主动刷新」前置,若刷新自身再走
 *    apiSend 就会递归触发 single-flight 自等待形成死锁;②刷新是会话地基,不该被业务封装影响。
 *  - 成功 → 写回完整 token 组;失败按 classifyRefreshFailure 分类:不可恢复 → clearAll 强制登出,
 *    瞬时 → 保留会话静默返回 false(调用方继续用现 token,后续请求会自然 401/重试)。
 */
import { classifyRefreshFailure, parseIssuedTokens } from './refresh'
import { createSingleFlight } from './singleFlight'
import { clearAll, getRefreshToken, setSessionTokens } from './store'

const REFRESH_PATH = '/v1/sessions/refresh'

interface RefreshResponse {
  session?: {
    session_token?: string
    refresh_token?: string
    session_expires_at?: string
  }
}

/** 从网关错误响应里取 code(约定形态 {error:{code,message}});取不到给空串。 */
async function errorCodeOf(resp: Response): Promise<string> {
  try {
    const body = (await resp.json()) as { error?: { code?: string } }
    return body?.error?.code ?? ''
  } catch {
    return ''
  }
}

async function doRefresh(): Promise<boolean> {
  const refreshToken = getRefreshToken()
  if (!refreshToken) return false

  let resp: Response
  try {
    resp = await fetch(REFRESH_PATH, {
      method: 'POST',
      credentials: 'include',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refreshToken }),
    })
  } catch {
    return false // 网络错误:瞬时,保留会话
  }

  if (!resp.ok) {
    const code = await errorCodeOf(resp)
    if (classifyRefreshFailure(code) === 'logout') {
      clearAll() // 不可恢复:清态,RequireAuth 守卫会跳登录
    }
    return false
  }

  const body = (await resp.json().catch(() => null)) as RefreshResponse | null
  const tokens = parseIssuedTokens(body?.session)
  if (!tokens) return false
  setSessionTokens(tokens) // 保留现有 user,只换 token + 到期
  return true
}

/**
 * 确保会话新鲜:并发调用合并为一次在途刷新。
 * 返回 true=已刷新出新 token;false=无 refresh token / 瞬时失败 / 已登出。
 */
export const ensureFreshSession = createSingleFlight(doRefresh)
