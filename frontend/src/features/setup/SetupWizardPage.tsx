import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { ApiError } from '../../lib/api'
import { fetchSetupStatus, installAdmin, installErrorText, validateInstallForm } from './setup'

/*
 * /setup 首装向导(壳外公开路由,sub2api 形态):
 * 未安装 → 建管理员表单 → 完成页 → 跳登录(不自动登录,与 sub2 收尾一致)。
 * 已安装(或后端判定关死)→ 直接送去登录页;授权边界在后端 fail-closed 守卫。
 */
type Phase = 'checking' | 'form' | 'done'

export function SetupWizardPage() {
  const nav = useNavigate()
  const [phase, setPhase] = useState<Phase>('checking')
  const [email, setEmail] = useState('')
  const [name, setName] = useState('')
  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    const ctrl = new AbortController()
    fetchSetupStatus(ctrl.signal)
      .then((s) => {
        if (ctrl.signal.aborted) return
        if (s.needs_setup) setPhase('form')
        else nav('/login', { replace: true })
      })
      .catch(() => {
        if (!ctrl.signal.aborted) nav('/login', { replace: true })
      })
    return () => ctrl.abort()
  }, [nav])

  const submit = async (ev: React.FormEvent) => {
    ev.preventDefault()
    const invalid = validateInstallForm(email, password, confirm)
    if (invalid) {
      setError(invalid)
      return
    }
    setBusy(true)
    setError('')
    try {
      await installAdmin({ email: email.trim(), password, display_name: name.trim() || undefined })
      setPhase('done')
    } catch (e) {
      setError(e instanceof ApiError ? installErrorText(e.code) : '安装失败,请稍后重试')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div
      style={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: 'var(--hk-bg)',
        padding: 'var(--hk-space-6)',
      }}
    >
      <div
        style={{
          width: 420,
          maxWidth: '100%',
          background: 'var(--hk-surface)',
          border: '1px solid var(--hk-line)',
          borderRadius: 'var(--hk-radius-lg)',
          boxShadow: 'var(--hk-shadow-2)',
          padding: 'var(--hk-space-6)',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--hk-space-4)',
        }}
      >
        <header style={{ textAlign: 'center' }}>
          <h1 style={{ fontSize: 22, margin: 0 }}>初始化 HUAKAI</h1>
          <p style={{ fontSize: 13, color: 'var(--hk-ink-500)', margin: 'var(--hk-space-2) 0 0' }}>
            {phase === 'done' ? '安装完成' : '首次部署:创建第一个管理员账号'}
          </p>
        </header>

        {/* 步骤条:管理员 → 完成 */}
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', justifyContent: 'center' }}>
          {(['管理员', '完成'] as const).map((label, i) => {
            const active = phase === 'done' ? true : i === 0
            return (
              <div key={label} style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
                <span
                  style={{
                    width: 22,
                    height: 22,
                    borderRadius: '50%',
                    display: 'inline-flex',
                    alignItems: 'center',
                    justifyContent: 'center',
                    fontSize: 12,
                    fontWeight: 700,
                    background: active ? 'var(--hk-primary-500)' : 'var(--hk-line-soft)',
                    color: active ? '#fff' : 'var(--hk-ink-500)',
                  }}
                >
                  {i + 1}
                </span>
                <span style={{ fontSize: 12, color: active ? 'var(--hk-ink-900)' : 'var(--hk-ink-500)' }}>{label}</span>
                {i === 0 && <span style={{ width: 40, height: 2, background: phase === 'done' ? 'var(--hk-primary-500)' : 'var(--hk-line)' }} />}
              </div>
            )
          })}
        </div>

        {phase === 'checking' && (
          <p style={{ textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>检查安装状态…</p>
        )}

        {phase === 'form' && (
          <form onSubmit={submit} style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
            <label style={fieldLabel}>
              管理员邮箱
              <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required style={fieldInput} />
            </label>
            <label style={fieldLabel}>
              显示名(可选)
              <input type="text" value={name} onChange={(e) => setName(e.target.value)} maxLength={64} style={fieldInput} />
            </label>
            <label style={fieldLabel}>
              密码(至少 8 位)
              <input type="password" value={password} onChange={(e) => setPassword(e.target.value)} required style={fieldInput} />
            </label>
            <label style={fieldLabel}>
              确认密码
              <input type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required style={fieldInput} />
            </label>
            {error && (
              <p role="alert" style={{ color: 'var(--hk-danger-600, #b42318)', fontSize: 13, margin: 0 }}>
                {error}
              </p>
            )}
            <button
              type="submit"
              disabled={busy}
              style={{
                padding: 'var(--hk-space-3)',
                borderRadius: 'var(--hk-radius-md)',
                border: 0,
                background: 'var(--hk-primary-500)',
                color: '#fff',
                fontSize: 15,
                fontWeight: 600,
                cursor: busy ? 'wait' : 'pointer',
              }}
            >
              {busy ? '正在创建…' : '创建管理员并完成安装'}
            </button>
          </form>
        )}

        {phase === 'done' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', textAlign: 'center' }}>
            <p style={{ fontSize: 14, margin: 0 }}>
              管理员 <strong>{email.trim()}</strong> 创建成功。
            </p>
            <Link
              to="/login"
              style={{
                padding: 'var(--hk-space-3)',
                borderRadius: 'var(--hk-radius-md)',
                background: 'var(--hk-primary-500)',
                color: '#fff',
                fontSize: 15,
                fontWeight: 600,
                textDecoration: 'none',
              }}
            >
              去登录 →
            </Link>
          </div>
        )}
      </div>
    </div>
  )
}

const fieldLabel: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 6,
  fontSize: 13,
  color: 'var(--hk-ink-700)',
}

const fieldInput: React.CSSProperties = {
  padding: '10px 12px',
  borderRadius: 'var(--hk-radius-md)',
  border: '1px solid var(--hk-line)',
  fontSize: 14,
  fontFamily: 'inherit',
}
