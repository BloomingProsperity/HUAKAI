import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { StatusBadge } from '../ui/StatusBadge'
import { verifyEmail } from './emailVerifyApi'
import { parseVerifyParams, verifyErrorMessage, withManualVerifyToken } from './emailVerify'

/*
 * 邮箱验证页(public 壳,AppShell 之外)。从 URL query 读 token / tenant_id;URL 带 token 则挂载即自动校验,
 * URL 无 token 则展示「手动粘贴 token」表单(验证邮件只投递裸 token、不含链接,用户需把 token 粘进来)。
 * 呈现 输入 / 校验中 / 成功 / 失败 四态,提供「去登录」。
 */
type Phase = 'input' | 'verifying' | 'success' | 'error'

export function EmailVerifyPage() {
  const nav = useNavigate()
  const urlParams = useMemo(() => parseVerifyParams(window.location.search), [])
  const urlHasToken = urlParams.token.length > 0
  const [phase, setPhase] = useState<Phase>(urlHasToken ? 'verifying' : 'input')
  const [error, setError] = useState<string | null>(null)
  const [manualToken, setManualToken] = useState('')
  // 严格模式下 effect 会跑两次;用 ref 守一次性,避免重复 POST 同一 token(第二次必返回 token 已用)。
  const ran = useRef(false)

  const submit = (token: string) => {
    setPhase('verifying')
    setError(null)
    verifyEmail(urlParams.tenantId, token)
      .then(() => setPhase('success'))
      .catch((e) => {
        setError(verifyErrorMessage(e))
        setPhase('error')
      })
  }

  useEffect(() => {
    if (ran.current || !urlHasToken) return
    ran.current = true
    submit(urlParams.token)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const onManualVerify = () => {
    const p = withManualVerifyToken(urlParams, manualToken)
    if (!p.token) {
      setError('请粘贴验证邮件里收到的 token。')
      return
    }
    submit(p.token)
  }

  const goLogin = () => nav('/login', { replace: true })

  return (
    <div style={page}>
      <div style={card}>
        <div style={head}>
          <span aria-hidden style={logo} />
          <h1 style={{ fontSize: 20, fontWeight: 700, margin: 0 }}>邮箱验证</h1>
        </div>

        {phase === 'input' && (
          <div style={body}>
            <p style={lead}>把注册/验证邮件里收到的一次性 token 粘贴到这里完成邮箱验证。</p>
            {error && <p style={{ ...lead, color: '#8f322a' }}>{error}</p>}
            <input
              type="text"
              value={manualToken}
              onChange={(e) => setManualToken(e.target.value)}
              autoComplete="one-time-code"
              autoFocus
              placeholder="粘贴验证 token"
              style={inputBox}
            />
            <button type="button" onClick={onManualVerify} style={primary}>
              验证邮箱
            </button>
          </div>
        )}

        {phase === 'verifying' && (
          <div style={body}>
            <StatusBadge tone="info">校验中</StatusBadge>
            <p style={lead}>正在确认你的邮箱验证链接,请稍候…</p>
          </div>
        )}

        {phase === 'success' && (
          <div style={body}>
            <StatusBadge tone="ok">验证成功</StatusBadge>
            <p style={lead}>你的邮箱已验证完成,现在可以登录控制台了。</p>
            <button type="button" onClick={goLogin} style={primary}>
              去登录
            </button>
          </div>
        )}

        {phase === 'error' && (
          <div style={body}>
            <StatusBadge tone="danger">验证失败</StatusBadge>
            <p style={lead}>{error}</p>
            <button type="button" onClick={goLogin} style={primary}>
              去登录
            </button>
          </div>
        )}
      </div>
    </div>
  )
}

const page: React.CSSProperties = {
  minHeight: '100%',
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  background: 'var(--hk-canvas)',
  padding: 'var(--hk-space-5)',
}
const card: React.CSSProperties = {
  width: 'min(400px, 100%)',
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
  boxShadow: 'var(--hk-shadow-2)',
  padding: 'var(--hk-space-6)',
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-4)',
}
const head: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 'var(--hk-space-2)',
}
const logo: React.CSSProperties = {
  width: 24,
  height: 24,
  borderRadius: 'var(--hk-radius-sm)',
  background: 'linear-gradient(135deg, var(--hk-primary-500), var(--hk-primary-700))',
  display: 'inline-block',
}
const body: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  alignItems: 'flex-start',
  gap: 'var(--hk-space-3)',
}
const lead: React.CSSProperties = {
  margin: 0,
  fontSize: 13,
  lineHeight: 1.6,
  color: 'var(--hk-ink-700)',
}
const primary: React.CSSProperties = {
  height: 38,
  padding: '0 var(--hk-space-4)',
  border: '1px solid var(--hk-primary-600)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-primary-500)',
  color: '#fff',
  fontSize: 14,
  fontWeight: 600,
  cursor: 'pointer',
}
const inputBox: React.CSSProperties = {
  height: 38,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
  fontSize: 14,
  width: '100%',
}
