/*
 * 社交登录回调(OAuth 多步编排的第二步)纯逻辑。与 React/DOM/网络解耦,便于变异测试。
 *
 * 背景:登录页发起社交登录时 window.location 跳到上游授权页(oauth-init 返回的 auth_url),
 * 上游授权完成后回跳到各 provider 在后端配置的固定 redirect_uri(约定指向本前端 /oauth/callback),
 * 回跳 URL 只携带 ?code=&state=(被拒时携带 ?error=)。但后端 /v1/auth/oauth-callback 还需要
 * provider 与 tenant_id —— 上游回跳不带这两者,故发起时把它们暂存进 sessionStorage,回跳时取回。
 *
 * 安全:后端已对 state 做 cookie 比对(setOAuthStateCookie/requireOAuthStateCookie)防 CSRF;
 * 这里再用暂存的 state 与回跳 state 交叉校验,作为额外一层防护(state 不一致直接判错不发请求)。
 * sessionStorage 仅存 provider/tenantId/state 这类非密钥引导信息,不含任何 token/凭据。
 */

/** 发起社交登录时暂存、回调时取回的待完成上下文。 */
export interface PendingOAuth {
  provider: string
  tenantId: number
  /** oauth-init 返回的 state;用于回跳时与 URL state 交叉校验。 */
  state: string
}

/** sessionStorage 键名(单租户单标签页内有效)。 */
export const PENDING_OAUTH_KEY = 'hk_oauth_pending'

/** 从回跳 URL 的 query 串解析出的参数(全部可缺省)。 */
export interface CallbackParams {
  code: string | null
  state: string | null
  /** 上游拒绝/出错时携带的错误码(如 access_denied)。 */
  error: string | null
  errorDescription: string | null
}

/**
 * 解析回跳 URL 的 query 串(传入 location.search,带或不带前导 ? 均可)。
 * 纯函数:不读 window,便于单测。
 */
export function parseCallbackParams(search: string): CallbackParams {
  const q = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search)
  const get = (k: string) => {
    const v = q.get(k)
    return v != null && v.trim() !== '' ? v : null
  }
  return {
    code: get('code'),
    state: get('state'),
    error: get('error'),
    errorDescription: get('error_description'),
  }
}

/** 回调判定结果:要么去完成(带齐 provider/tenant/state/code),要么报错(带中文文案)。 */
export type CallbackOutcome =
  | { kind: 'complete'; provider: string; tenantId: number; state: string; code: string }
  | { kind: 'error'; message: string }

/**
 * 核心纯逻辑:综合回跳参数与暂存上下文,判定该完成换取会话还是报错。
 * 判定顺序(fail-closed,任何异常都不发请求,引导用户回登录页重试):
 *  1) 上游回跳带 error → 报错(展示上游错误)。
 *  2) 缺 code 或 state → 报错(回跳不完整)。
 *  3) 无暂存上下文 → 报错(无法得知 provider/tenant,通常是直接打开本页或换了标签页)。
 *  4) 暂存 state 与回跳 state 不一致 → 报错(CSRF/串流,拒绝)。
 *  5) 以上全过 → complete。
 */
export function decideCallbackOutcome(input: {
  params: CallbackParams
  pending: PendingOAuth | null
}): CallbackOutcome {
  const { params, pending } = input
  if (params.error) {
    return { kind: 'error', message: oauthErrorMessage(params.error, params.errorDescription) }
  }
  if (!params.code || !params.state) {
    return { kind: 'error', message: '社交登录回调缺少必要参数,请重新登录' }
  }
  if (!pending || !pending.provider || pending.tenantId <= 0) {
    return { kind: 'error', message: '找不到登录上下文,请回到登录页重新发起社交登录' }
  }
  // 判别核心:暂存 state 与回跳 state 必须一致,防止授权码被串到其它会话。
  if (pending.state && pending.state !== params.state) {
    return { kind: 'error', message: '登录状态校验失败(state 不一致),请重新登录' }
  }
  return {
    kind: 'complete',
    provider: pending.provider,
    tenantId: pending.tenantId,
    state: params.state,
    code: params.code,
  }
}

/** 把上游 OAuth 错误码翻译成友好中文;未知错误码原样附在括号里。 */
export function oauthErrorMessage(error: string, description: string | null): string {
  const e = error.trim().toLowerCase()
  if (e === 'access_denied') return '你取消了授权,未完成社交登录'
  if (e === 'invalid_request' || e === 'invalid_scope') return '社交登录请求无效,请重试或联系管理员'
  if (e === 'server_error' || e === 'temporarily_unavailable') return '上游授权服务暂时不可用,请稍后重试'
  const desc = description && description.trim() ? `:${description.trim()}` : ''
  return `社交登录失败(${error}${desc})`
}

/* ===== sessionStorage 薄封装(副作用隔离在此,纯逻辑在上方) ===== */

/** 发起社交登录前暂存上下文;sessionStorage 不可用时静默忽略(回调阶段会因无上下文而报错引导重试)。 */
export function writePendingOAuth(p: PendingOAuth): void {
  try {
    sessionStorage.setItem(PENDING_OAUTH_KEY, JSON.stringify(p))
  } catch {
    /* 隐私模式/禁用 storage:忽略,不阻断发起 */
  }
}

/** 回调阶段取回暂存上下文;缺失/损坏 → null(交给 decideCallbackOutcome 判错)。 */
export function readPendingOAuth(): PendingOAuth | null {
  try {
    const raw = sessionStorage.getItem(PENDING_OAUTH_KEY)
    if (!raw) return null
    const obj = JSON.parse(raw) as Partial<PendingOAuth>
    if (typeof obj.provider !== 'string' || typeof obj.tenantId !== 'number') return null
    return { provider: obj.provider, tenantId: obj.tenantId, state: typeof obj.state === 'string' ? obj.state : '' }
  } catch {
    return null
  }
}

/** 完成或失败后清除暂存,避免陈旧上下文污染下一次登录。 */
export function clearPendingOAuth(): void {
  try {
    sessionStorage.removeItem(PENDING_OAUTH_KEY)
  } catch {
    /* 忽略 */
  }
}
