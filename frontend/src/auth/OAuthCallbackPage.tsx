import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ApiError } from '../lib/api'
import { completeOAuth } from './api'
import { setSessionTokens } from './store'
import { clearPendingOAuth, decideCallbackOutcome, parseCallbackParams, readPendingOAuth } from './oauthCallback'

/*
 * 社交登录回调页(OAuth 多步编排第二步)。挂在公开路由 /oauth/callback(壳外、无需鉴权)。
 * 上游授权完成后回跳到此(各 provider 在后端配置的固定 redirect_uri 指向本页),URL 携带 code+state。
 * 流程:解析 URL → 取回发起时暂存的 {provider,tenant,state} → 判定 → POST /v1/auth/oauth-callback
 *       → 成功 setSessionTokens 进主界面,失败展示错误并引导回登录页。
 * 铁律:任何异常都 fail-closed(不发请求/不写会话),只引导用户回登录页重试,绝不静默放行。
 */
export function OAuthCallbackPage() {
  const nav = useNavigate()
  const [error, setError] = useState<string | null>(null)
  // StrictMode 下 effect 会跑两次;用 ref 保证回调只完成一次(避免 code 被重复消费)。
  const ran = useRef(false)

  useEffect(() => {
    // 只用 ran ref 一道闸防 code 被重复消费(StrictMode 下 effect 跑两次、第二次直接 return)。
    // 不再用 cleanup 翻转的局部 alive 标志:那会让 StrictMode 的 cleanup#1 把第一次在飞 promise
    // 的 alive 置 false,导致成功(setSessionTokens+nav)与失败(setError)两条路径全被吞、永久卡
    // spinner(开发期自测必现)。真正卸载后 setState 在 React18 是静默 no-op,无需 alive 兜底。
    if (ran.current) return
    ran.current = true

    const params = parseCallbackParams(window.location.search)
    const pending = readPendingOAuth()
    const outcome = decideCallbackOutcome({ params, pending })
    if (outcome.kind === 'error') {
      clearPendingOAuth()
      setError(outcome.message)
      return
    }

    completeOAuth(outcome.tenantId, outcome.provider, outcome.state, outcome.code)
      .then((r) => {
        clearPendingOAuth()
        // 社交登录回调后端直接发会话(不走 2FA),恒为 ok;narrow 以满足类型并防御异常分支。
        if (r.kind !== 'ok') {
          setError('社交登录返回异常,请重新登录')
          return
        }
        setSessionTokens(r.tokens, r.user ?? undefined)
        nav('/', { replace: true })
      })
      .catch((e: unknown) => {
        clearPendingOAuth()
        setError(
          e instanceof ApiError
            ? e.status === 401
              ? '社交登录验证失败,请重新登录'
              : `${e.message}(${e.code})`
            : '社交登录完成失败,请重新登录',
        )
      })
  }, [nav])

  return (
    <div style={page}>
      <div style={card}>
        {error ? (
          <>
            <h1 style={{ fontSize: 18, fontWeight: 700, color: 'var(--hk-ink-900)' }}>社交登录未完成</h1>
            <p style={{ fontSize: 13, color: 'var(--hk-ink-500)', margin: 0 }}>{error}</p>
            <button type="button" onClick={() => nav('/login', { replace: true })} style={primary}>
              返回登录
            </button>
          </>
        ) : (
          <>
            <span aria-hidden style={spinner} />
            <h1 style={{ fontSize: 18, fontWeight: 700, color: 'var(--hk-ink-900)' }}>正在完成社交登录…</h1>
            <p style={{ fontSize: 13, color: 'var(--hk-ink-500)', margin: 0 }}>正在与上游确认你的身份,请稍候。</p>
          </>
        )}
      </div>
    </div>
  )
}

const page: React.CSSProperties = { minHeight: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'var(--hk-canvas)', padding: 'var(--hk-space-5)' }
const card: React.CSSProperties = { width: 'min(400px, 100%)', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-2)', padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 'var(--hk-space-3)', textAlign: 'center' }
const spinner: React.CSSProperties = { width: 28, height: 28, borderRadius: '50%', border: '3px solid var(--hk-line)', borderTopColor: 'var(--hk-primary-500)', animation: 'hk-spin 0.8s linear infinite' }
const primary: React.CSSProperties = { height: 38, padding: '0 var(--hk-space-5)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 14, fontWeight: 600, cursor: 'pointer' }
