import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { completeOAuth, oauthPendingComplete, oauthPendingSendCode } from './api'
import { setSessionTokens } from './store'
import {
  callbackErrorMessage,
  clearPendingOAuth,
  decideCallbackOutcome,
  parseCallbackParams,
  pendingErrorMessage,
  readPendingOAuth,
} from './oauthCallback'

/*
 * 社交登录回调页(OAuth 多步编排第二步)。挂在公开路由 /oauth/callback(壳外、无需鉴权)。
 * 上游授权完成后回跳到此(各 provider 在后端配置的固定 redirect_uri 指向本页),URL 携带 code+state。
 * 主流程:解析 URL → 取回发起时暂存的 {provider,tenant,state} → 判定 → POST /v1/auth/oauth-callback
 *   → 成功 setSessionTokens 进主界面。
 * 补邮箱分支(QQ/无验证邮箱 GitHub 等,回调返回 202 pending_token):切到「填邮箱→发码→验码建号」三步,
 *   全程 fail-closed(任一异常只提示重试/回登录,绝不静默放行)。
 */

type Phase =
  | { step: 'completing' }
  | { step: 'error'; message: string }
  | { step: 'email'; pendingToken: string; busy: boolean; message?: string }
  | { step: 'code'; challengeToken: string; email: string; busy: boolean; message?: string }

export function OAuthCallbackPage() {
  const nav = useNavigate()
  const [phase, setPhase] = useState<Phase>({ step: 'completing' })
  const [emailInput, setEmailInput] = useState('')
  const [codeInput, setCodeInput] = useState('')
  // StrictMode 下 effect 会跑两次;用 ref 保证回调只完成一次(避免 code 被重复消费)。
  const ran = useRef(false)

  useEffect(() => {
    if (ran.current) return
    ran.current = true

    const params = parseCallbackParams(window.location.search)
    const pending = readPendingOAuth()
    const outcome = decideCallbackOutcome({ params, pending })
    if (outcome.kind === 'error') {
      clearPendingOAuth()
      setPhase({ step: 'error', message: outcome.message })
      return
    }

    completeOAuth(outcome.tenantId, outcome.provider, outcome.state, outcome.code)
      .then((r) => {
        clearPendingOAuth()
        if (r.kind === 'pending_email') {
          // 身份缺已验证邮箱:进入补邮箱流程(不是错误,不引导回登录)。
          setPhase({ step: 'email', pendingToken: r.pendingToken, busy: false })
          return
        }
        if (r.kind !== 'ok') {
          setPhase({ step: 'error', message: '社交登录返回异常,请重新登录' })
          return
        }
        setSessionTokens(r.tokens, r.user ?? undefined)
        nav('/', { replace: true })
      })
      .catch((e: unknown) => {
        clearPendingOAuth()
        setPhase({ step: 'error', message: callbackErrorMessage(e) })
      })
  }, [nav])

  function onSendCode(pendingToken: string) {
    const email = emailInput.trim()
    if (!email) {
      setPhase({ step: 'email', pendingToken, busy: false, message: '请填写邮箱' })
      return
    }
    setPhase({ step: 'email', pendingToken, busy: true })
    oauthPendingSendCode(pendingToken, email)
      .then((challengeToken) => {
        setCodeInput('')
        setPhase({ step: 'code', challengeToken, email, busy: false })
      })
      .catch((e: unknown) => {
        setPhase({ step: 'email', pendingToken, busy: false, message: pendingErrorMessage(e) })
      })
  }

  function onSubmitCode(challengeToken: string, email: string) {
    const code = codeInput.trim()
    if (!code) {
      setPhase({ step: 'code', challengeToken, email, busy: false, message: '请填写验证码' })
      return
    }
    setPhase({ step: 'code', challengeToken, email, busy: true })
    oauthPendingComplete(challengeToken, code)
      .then((r) => {
        if (r.kind !== 'ok') {
          setPhase({ step: 'code', challengeToken, email, busy: false, message: '验证返回异常,请重试' })
          return
        }
        setSessionTokens(r.tokens, r.user ?? undefined)
        nav('/', { replace: true })
      })
      .catch((e: unknown) => {
        setPhase({ step: 'code', challengeToken, email, busy: false, message: pendingErrorMessage(e) })
      })
  }

  return (
    <div style={page}>
      <div style={card}>
        {phase.step === 'error' && (
          <>
            <h1 style={title}>社交登录未完成</h1>
            <p style={hint}>{phase.message}</p>
            <button type="button" onClick={() => nav('/login', { replace: true })} style={primary}>
              返回登录
            </button>
          </>
        )}

        {phase.step === 'completing' && (
          <>
            <span aria-hidden style={spinner} />
            <h1 style={title}>正在完成社交登录…</h1>
            <p style={hint}>正在与上游确认你的身份,请稍候。</p>
          </>
        )}

        {phase.step === 'email' && (
          <form
            style={form}
            onSubmit={(e) => {
              e.preventDefault()
              onSendCode(phase.pendingToken)
            }}
          >
            <h1 style={title}>补全邮箱以完成注册</h1>
            <p style={hint}>该登录方式未提供已验证邮箱。填写你的邮箱,我们会发送一个验证码。</p>
            <input
              type="email"
              value={emailInput}
              onChange={(e) => setEmailInput(e.target.value)}
              placeholder="you@example.com"
              autoFocus
              required
              disabled={phase.busy}
              style={input}
            />
            {phase.message && <p style={errText}>{phase.message}</p>}
            <button type="submit" disabled={phase.busy} style={primary}>
              {phase.busy ? '发送中…' : '发送验证码'}
            </button>
          </form>
        )}

        {phase.step === 'code' && (
          <form
            style={form}
            onSubmit={(e) => {
              e.preventDefault()
              onSubmitCode(phase.challengeToken, phase.email)
            }}
          >
            <h1 style={title}>输入验证码</h1>
            <p style={hint}>验证码已发送到 {phase.email}。输入邮件里的码以完成注册。</p>
            <input
              type="text"
              value={codeInput}
              onChange={(e) => setCodeInput(e.target.value)}
              placeholder="邮件中的验证码"
              autoFocus
              required
              disabled={phase.busy}
              autoComplete="one-time-code"
              style={input}
            />
            {phase.message && <p style={errText}>{phase.message}</p>}
            <button type="submit" disabled={phase.busy} style={primary}>
              {phase.busy ? '验证中…' : '完成注册并登录'}
            </button>
          </form>
        )}
      </div>
    </div>
  )
}

const page: React.CSSProperties = { minHeight: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'var(--hk-canvas)', padding: 'var(--hk-space-5)' }
const card: React.CSSProperties = { width: 'min(400px, 100%)', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-2)', padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 'var(--hk-space-3)', textAlign: 'center' }
const form: React.CSSProperties = { display: 'flex', flexDirection: 'column', alignItems: 'stretch', gap: 'var(--hk-space-3)', width: '100%' }
const title: React.CSSProperties = { fontSize: 18, fontWeight: 700, color: 'var(--hk-ink-900)', margin: 0, textAlign: 'center' }
const hint: React.CSSProperties = { fontSize: 13, color: 'var(--hk-ink-500)', margin: 0, textAlign: 'center' }
const errText: React.CSSProperties = { fontSize: 12, color: 'var(--hk-danger-600, #c0392b)', margin: 0, textAlign: 'center' }
const input: React.CSSProperties = { height: 38, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', fontSize: 14 }
const spinner: React.CSSProperties = { width: 28, height: 28, borderRadius: '50%', border: '3px solid var(--hk-line)', borderTopColor: 'var(--hk-primary-500)', animation: 'hk-spin 0.8s linear infinite' }
const primary: React.CSSProperties = { height: 38, padding: '0 var(--hk-space-5)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 14, fontWeight: 600, cursor: 'pointer' }
