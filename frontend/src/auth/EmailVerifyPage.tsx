import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { StatusBadge } from '../ui/StatusBadge'
import { verifyEmail } from './emailVerifyApi'
import {
  parseVerifyParams,
  validateVerifyParams,
  verifyErrorMessage,
  type VerifyParams,
} from './emailVerify'

/*
 * 邮箱验证页(public 壳,AppShell 之外)。从 URL query 读 token / tenant_id,挂载即自动校验,
 * 呈现 校验中 / 成功 / 失败 三态,提供「去登录」。
 *
 * 形态:HUAKAI 后端只有「凭链接 token 确认验证」(POST /v1/auth/verify-email),没有独立发码端点
 * (发码在注册时由后端 SendVerification 发邮件),故本页是「点链接确认」形态,不做 6 位码输入。
 */
type Phase = 'verifying' | 'success' | 'error'

export function EmailVerifyPage() {
  const nav = useNavigate()
  const [phase, setPhase] = useState<Phase>('verifying')
  const [error, setError] = useState<string | null>(null)
  // 严格模式下 effect 会跑两次;用 ref 守一次性,避免重复 POST 同一 token(第二次必返回 token 已用)。
  const ran = useRef(false)

  useEffect(() => {
    if (ran.current) return
    ran.current = true

    const params: VerifyParams = parseVerifyParams(window.location.search)
    const invalid = validateVerifyParams(params)
    if (invalid) {
      setError(invalid)
      setPhase('error')
      return
    }

    let alive = true
    verifyEmail(params.tenantId, params.token)
      .then(() => {
        if (alive) setPhase('success')
      })
      .catch((e) => {
        if (!alive) return
        setError(verifyErrorMessage(e))
        setPhase('error')
      })
    return () => {
      alive = false
    }
  }, [])

  const goLogin = () => nav('/login', { replace: true })

  return (
    <div style={page}>
      <div style={card}>
        <div style={head}>
          <span aria-hidden style={logo} />
          <h1 style={{ fontSize: 20, fontWeight: 700, margin: 0 }}>邮箱验证</h1>
        </div>

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
