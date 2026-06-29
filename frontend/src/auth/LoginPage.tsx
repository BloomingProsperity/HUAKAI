import { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ApiError } from '../lib/api'
import {
  login,
  loginTwoFactor,
  oauthInit,
  passkeyLoginBegin,
  passkeyLoginFinish,
  register,
  validateInvitationCode,
} from './api'
import { setAdminToken, setSessionTokens, useAuth, type AuthUser } from './store'
import { fetchSiteConfig, FALLBACK_SITE_CONFIG, type SiteConfig } from './siteConfig'
import {
  captchaWidgetRenderable,
  deriveAffordances,
  inviteHintFromResult,
  INVITE_HINT_IDLE,
  providerLabel,
  serializeAssertion,
  shouldValidateInvite,
  toPublicKeyRequestOptions,
  validateRegisterForm,
  webAuthnSupported,
  type InviteHint,
} from './loginEnhance'
import { writePendingOAuth } from './oauthCallback'

/*
 * 登录 / 注册页。基础三流程(邮箱密码登录 / 2FA / 注册)保持原样,只在其上【叠加】增强:
 *   - 进站拉 /v1/site/config 决定显示哪些方式;失败静默回退到只显邮箱密码登录;
 *   - 社交登录按钮行(oauth-init → 跳转)、通行密钥登录(WebAuthn)、人机验证(Turnstile)、
 *     确认密码 + 邀请码必填、找回密码 / 条款链接。
 * 铁律:任一增强加载失败或后端未开启都不得影响基础邮箱密码登录。
 */
type Mode = 'login' | 'register' | '2fa'

export function LoginPage() {
  const nav = useNavigate()
  const auth = useAuth()
  const [mode, setMode] = useState<Mode>('login')
  const [tenantId, setTenantId] = useState('1')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [displayName, setDisplayName] = useState('')
  const [inviteCode, setInviteCode] = useState('')
  // 邀请码实时预校验提示(只读,不阻断提交)。仅注册态、站点开启邀请门时失焦触发。
  const [inviteHint, setInviteHint] = useState<InviteHint>(INVITE_HINT_IDLE)
  // 预校验请求序号:失焦可能连发,只采纳最后一次结果,丢弃过期响应(防竞态闪烁)。
  const inviteSeqRef = useRef(0)
  const [code, setCode] = useState('')
  const [challengeId, setChallengeId] = useState('')
  // 2FA 第一步拿到的 user,完成第二步时写入 store(2FA 完成响应本身不含 user)。
  const [pendingUser, setPendingUser] = useState<AuthUser | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [adminTokenDraft, setAdminTokenDraft] = useState('')

  // 站点配置:初始用回退(只显密码登录),进站异步拉取覆盖。加载失败保持回退,绝不阻断登录。
  const [site, setSite] = useState<SiteConfig>(FALLBACK_SITE_CONFIG)
  // 人机验证 token(Turnstile 回调写入);仅在 captcha 启用且可渲染时使用。
  const [captchaToken, setCaptchaToken] = useState('')

  const tid = () => Number(tenantId.trim()) || 0
  const af = deriveAffordances(site)
  const canRenderCaptcha = captchaWidgetRenderable(site)

  // 进站拉站点配置;组件卸载后丢弃结果。失败静默(回退已是默认),不打扰用户。
  useEffect(() => {
    let alive = true
    fetchSiteConfig()
      .then((cfg) => {
        if (alive) setSite(cfg)
      })
      .catch(() => {
        /* 加载失败:保持 FALLBACK_SITE_CONFIG(只显密码登录),不影响基础流程 */
      })
    return () => {
      alive = false
    }
  }, [])

  // ===== 基础流程:邮箱密码登录(保持原调用 login → setSessionTokens) =====
  const onLogin = async () => {
    setBusy(true)
    setError(null)
    try {
      // captcha token 仅在站点要求且已获取时传;否则缺席,请求体与增强前一致。
      const r = await login(tid(), email.trim(), password, af.showCaptcha ? captchaToken : undefined)
      if (r.kind === '2fa') {
        setChallengeId(r.challengeId)
        setPendingUser(r.user)
        setMode('2fa')
        return
      }
      setSessionTokens(r.tokens, r.user ?? undefined)
      nav('/', { replace: true })
    } catch (e) {
      setError(authErr(e))
    } finally {
      setBusy(false)
    }
  }

  // ===== 基础流程:2FA 验证码(保持原调用 loginTwoFactor → setSessionTokens) =====
  const onTwoFactor = async () => {
    setBusy(true)
    setError(null)
    try {
      const r = await loginTwoFactor(challengeId, code.trim())
      setSessionTokens(r.tokens, pendingUser ?? undefined)
      nav('/', { replace: true })
    } catch (e) {
      setError(authErr(e))
    } finally {
      setBusy(false)
    }
  }

  // ===== 基础流程:注册(保持原调用 register;增强=确认密码 + 邀请码必填的前置校验) =====
  const onRegister = async () => {
    // 增强前置校验:两次密码一致 + invitation_required 时邀请码必填。校验失败不发请求。
    const validationError = validateRegisterForm({
      email,
      password,
      confirmPassword,
      inviteCode,
      invitationRequired: site.invitationRequired,
    })
    if (validationError) {
      setError(validationError)
      return
    }
    setBusy(true)
    setError(null)
    try {
      await register(tid(), email.trim(), password, displayName, inviteCode, af.showCaptcha ? captchaToken : undefined)
      setNotice('注册成功,请登录(若开启邮箱验证,请先完成验证)。')
      setMode('login')
    } catch (e) {
      setError(authErr(e))
    } finally {
      setBusy(false)
    }
  }

  // ===== 增强:邀请码失焦实时预校验(只读提示,绝不阻断提交;后端 register 仍权威) =====
  // 调 /v1/auth/validate-invitation-code(公开只读端点:不登录/不发 token/不消费邀请码/
  // 不改任何鉴权状态)。空码或站点未开启邀请门时不触发,复位为 idle。
  const onInviteBlur = async () => {
    const value = inviteCode.trim()
    if (!shouldValidateInvite(value, site.invitationRequired)) {
      // 清空或无需校验:复位提示(避免遗留上一次的结果)。不发请求。
      inviteSeqRef.current += 1 // 作废在途请求
      setInviteHint(INVITE_HINT_IDLE)
      return
    }
    const seq = ++inviteSeqRef.current
    setInviteHint({ status: 'checking', message: '正在校验邀请码…' })
    try {
      const result = await validateInvitationCode(tid(), value)
      // 仅当本次仍是最新请求时才采纳结果(防多次失焦的过期响应覆盖)。
      if (seq !== inviteSeqRef.current) return
      setInviteHint(inviteHintFromResult(result))
    } catch {
      // 校验服务不可用:给中性提示,绝不阻断提交(后端 register 权威校验)。
      if (seq !== inviteSeqRef.current) return
      setInviteHint({ status: 'unavailable', message: '邀请码校验暂不可用,可直接提交注册' })
    }
  }

  // ===== 增强:社交登录(oauth-init → 跳转上游授权页) =====
  const onOauth = async (provider: string) => {
    setBusy(true)
    setError(null)
    try {
      const { authUrl, state } = await oauthInit(tid(), provider)
      // 上游回跳只带 code+state,不带 provider/tenant;先暂存上下文,回调页(/oauth/callback)取回完成换会话。
      writePendingOAuth({ provider, tenantId: tid(), state })
      // 跳转到上游授权页;回调由后端 /v1/auth/oauth-callback 处理后再回前端。
      window.location.assign(authUrl)
    } catch (e) {
      setError(authErr(e))
      setBusy(false) // 跳转成功则页面已离开,无需复位;失败才复位。
    }
  }

  // ===== 增强:通行密钥登录(begin → navigator.credentials.get → finish → setSessionTokens) =====
  const onPasskey = async () => {
    if (!webAuthnSupported()) {
      setError('当前浏览器不支持通行密钥登录')
      return
    }
    setBusy(true)
    setError(null)
    try {
      const begin = await passkeyLoginBegin(tid())
      const options = toPublicKeyRequestOptions(begin.public_key)
      const credential = (await navigator.credentials.get({ publicKey: options })) as PublicKeyCredential | null
      if (!credential) {
        setError('通行密钥验证已取消')
        return
      }
      const r = await passkeyLoginFinish(tid(), begin.session_id, serializeAssertion(credential))
      // passkey finish 后端直接发会话(不走 2FA),恒为 ok;narrow 以满足类型并防御异常分支。
      if (r.kind !== 'ok') {
        setError('通行密钥登录返回异常')
        return
      }
      setSessionTokens(r.tokens, r.user ?? undefined)
      nav('/', { replace: true })
    } catch (e) {
      // WebAuthn 用户取消会抛 DOMException;归一成友好提示,不污染控制台。
      if (e instanceof DOMException) setError('通行密钥验证未完成')
      else setError(authErr(e))
    } finally {
      setBusy(false)
    }
  }

  const passkeyAvailable = af.showPasskey && webAuthnSupported()

  return (
    <div style={page}>
      <div style={card}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', marginBottom: 'var(--hk-space-2)' }}>
          <span aria-hidden style={logo} />
          <h1 style={{ fontSize: 20, fontWeight: 700 }}>{site.siteName || 'HUAKAI 控制台'}</h1>
        </div>

        {mode !== '2fa' && (
          <div style={{ display: 'flex', gap: 'var(--hk-space-2)', marginBottom: 'var(--hk-space-2)' }}>
            <Tab active={mode === 'login'} onClick={() => setMode('login')}>
              登录
            </Tab>
            {/* 注册入口受 registration_enabled + password_register_enabled 门控 */}
            {af.showRegister && (
              <Tab active={mode === 'register'} onClick={() => setMode('register')}>
                注册
              </Tab>
            )}
          </div>
        )}

        {notice && <Banner tone="ok">{notice}</Banner>}
        {error && <Banner tone="danger">{error}</Banner>}

        {mode === '2fa' ? (
          <>
            <p style={{ fontSize: 13, color: 'var(--hk-ink-500)', margin: 0 }}>请输入两步验证码。</p>
            <Field label="验证码">
              <input value={code} onChange={(e) => setCode(e.target.value)} inputMode="numeric" autoFocus style={inp} />
            </Field>
            <button type="button" disabled={busy} onClick={onTwoFactor} style={primary}>
              {busy ? '校验中…' : '验证并登录'}
            </button>
            <button type="button" onClick={() => setMode('login')} style={linkBtn}>
              ← 返回
            </button>
          </>
        ) : (
          <form
            onSubmit={(e) => {
              e.preventDefault()
              mode === 'login' ? onLogin() : onRegister()
            }}
            style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}
          >
            <Field label="租户 ID">
              <input value={tenantId} onChange={(e) => setTenantId(e.target.value)} inputMode="numeric" style={inp} />
            </Field>

            {/* 密码登录/注册区:password_login_enabled=false 时登录态隐藏(只留社交/passkey);
                注册态始终显示表单(注册本就需要密码)。 */}
            {(mode === 'register' || af.showPasswordLogin) && (
              <>
                <Field label="邮箱">
                  <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" style={inp} />
                </Field>
                {mode === 'register' && (
                  <Field label="显示名(可选)">
                    <input value={displayName} onChange={(e) => setDisplayName(e.target.value)} style={inp} />
                  </Field>
                )}
                <Field label="密码">
                  <input
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
                    style={inp}
                  />
                </Field>
                {/* 增强:确认密码(仅注册) */}
                {mode === 'register' && (
                  <Field label="确认密码">
                    <input
                      type="password"
                      value={confirmPassword}
                      onChange={(e) => setConfirmPassword(e.target.value)}
                      autoComplete="new-password"
                      style={inp}
                    />
                  </Field>
                )}
                {mode === 'register' && (
                  <Field label={site.invitationRequired ? '邀请码(必填)' : '邀请码(可选)'}>
                    <input
                      value={inviteCode}
                      onChange={(e) => {
                        setInviteCode(e.target.value)
                        // 编辑即复位提示,避免旧结果与新输入不符;失焦后才重新校验。
                        if (inviteHint.status !== 'idle') {
                          inviteSeqRef.current += 1
                          setInviteHint(INVITE_HINT_IDLE)
                        }
                      }}
                      onBlur={onInviteBlur}
                      style={inp}
                    />
                    {inviteHint.status !== 'idle' && (
                      <span style={inviteHintStyle(inviteHint.status)}>{inviteHint.message}</span>
                    )}
                  </Field>
                )}
              </>
            )}

            {/* 增强:人机验证(Turnstile)。仅 captcha 启用且可渲染时显示;无法渲染则占位提示,
                绝不阻断基础登录。 */}
            {af.showCaptcha && (
              <Captcha
                renderable={canRenderCaptcha}
                provider={site.captchaProvider}
                siteKey={site.captchaSiteKey}
                onToken={setCaptchaToken}
              />
            )}

            {/* 登录态显示密码登录区时才有提交按钮;注册态始终有。 */}
            {(mode === 'register' || af.showPasswordLogin) && (
              <button type="submit" disabled={busy} style={primary}>
                {busy ? '处理中…' : mode === 'login' ? '登录' : '注册'}
              </button>
            )}
          </form>
        )}

        {/* 增强:社交登录 + 通行密钥(仅登录态,且后端开启对应能力时) */}
        {mode === 'login' && (af.showOauth || af.showPasskey) && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)', marginTop: 'var(--hk-space-2)' }}>
            <Divider>或使用其他方式</Divider>
            {af.showOauth && (
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-2)' }}>
                {af.oauthProviders.map((p) => (
                  <button key={p} type="button" disabled={busy} onClick={() => onOauth(p)} style={socialBtn}>
                    {providerLabel(p)}
                  </button>
                ))}
              </div>
            )}
            {af.showPasskey && (
              <button
                type="button"
                disabled={busy || !passkeyAvailable}
                onClick={onPasskey}
                style={ghost}
                title={passkeyAvailable ? '' : '当前浏览器不支持通行密钥'}
              >
                {passkeyAvailable ? '使用通行密钥登录' : '通行密钥(浏览器不支持)'}
              </button>
            )}
          </div>
        )}

        {/* 增强:找回密码 + 条款链接(登录态) */}
        {mode === 'login' && (
          <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 'var(--hk-space-2)', fontSize: 12 }}>
            <a href="/forgot-password" style={linkAnchor}>
              忘记密码?
            </a>
            {site.siteDocUrl ? (
              <a href={site.siteDocUrl} target="_blank" rel="noreferrer" style={linkAnchor}>
                帮助文档
              </a>
            ) : (
              <a href="/legal" style={linkAnchor}>
                服务条款
              </a>
            )}
          </div>
        )}

        <details style={{ marginTop: 'var(--hk-space-3)', fontSize: 12, color: 'var(--hk-ink-500)' }}>
          <summary style={{ cursor: 'pointer' }}>运维者:配置 admin token{auth.hasAdminToken ? '(已配置)' : ''}</summary>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)', marginTop: 'var(--hk-space-2)' }}>
            <p style={{ margin: 0 }}>运维端点(账号池 / 路由)用独立 admin token 鉴权。粘贴你的 admin token:</p>
            <input type="password" value={adminTokenDraft} onChange={(e) => setAdminTokenDraft(e.target.value)} placeholder="admin token" style={inp} />
            <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
              <button type="button" onClick={() => { setAdminToken(adminTokenDraft); setAdminTokenDraft(''); setNotice('admin token 已保存。') }} style={ghost}>
                保存
              </button>
              {auth.hasAdminToken && (
                <button type="button" onClick={() => setAdminToken(null)} style={ghost}>
                  清除
                </button>
              )}
            </div>
          </div>
        </details>
      </div>
    </div>
  )
}

function authErr(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.code === 'invalid_credentials' || e.status === 401) return '邮箱或密码错误'
    if (e.code === 'captcha_required') return '人机验证未通过,请重试'
    return `${e.message}(${e.code})`
  }
  return '请求失败,请稍后重试'
}

/*
 * Turnstile 人机验证容器。
 *
 * 现状:渲染占位 + 异步加载 Cloudflare Turnstile 脚本。脚本加载或渲染失败都【不抛错、不阻断】
 * 基础登录 —— 只是 token 拿不到;后端 captcha 校验若启用会返回 captcha_required,由 authErr 提示。
 * 这里采用动态 script 注入而非新增 npm 依赖(避免引入运行时依赖,符合 Owner-gated 纪律);
 * 全局 turnstile 对象存在即用其显式渲染 API,token 通过回调写回。
 */
function Captcha({
  renderable,
  provider,
  siteKey,
  onToken,
}: {
  renderable: boolean
  provider: string
  siteKey: string
  onToken: (token: string) => void
}) {
  const ref = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    if (!renderable) return
    let cancelled = false
    const SCRIPT_ID = 'cf-turnstile-script'
    const SCRIPT_SRC = 'https://challenges.cloudflare.com/turnstile/v0/api.js'

    function tryRender() {
      if (cancelled || !ref.current) return
      const ts = (window as unknown as { turnstile?: TurnstileApi }).turnstile
      if (!ts) return
      try {
        ts.render(ref.current, {
          sitekey: siteKey,
          callback: (token: string) => onToken(token),
          'error-callback': () => onToken(''),
          'expired-callback': () => onToken(''),
        })
      } catch {
        /* 渲染失败:token 保持空,不阻断基础登录 */
      }
    }

    const existing = document.getElementById(SCRIPT_ID)
    if (existing) {
      tryRender()
    } else {
      const s = document.createElement('script')
      s.id = SCRIPT_ID
      s.src = SCRIPT_SRC
      s.async = true
      s.defer = true
      s.onload = tryRender
      // 脚本加载失败静默:不阻断登录(后端按其策略决定是否放行)。
      document.head.appendChild(s)
    }
    return () => {
      cancelled = true
    }
  }, [renderable, siteKey, onToken])

  if (!renderable) {
    // 启用了 captcha 但前端无法渲染(非 turnstile 或缺 site_key):提示占位,不阻断登录。
    return (
      <div style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>
        本站启用了人机验证({provider || '未知提供方'}),如提交被拒请按提示重试。
      </div>
    )
  }
  // Turnstile 显式渲染容器。
  return <div ref={ref} style={{ minHeight: 65 }} />
}

// Turnstile 全局对象的最小类型(只用到 render)。
interface TurnstileApi {
  render: (
    el: HTMLElement,
    opts: {
      sitekey: string
      callback: (token: string) => void
      'error-callback'?: () => void
      'expired-callback'?: () => void
    },
  ) => string
}

function Tab({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button type="button" onClick={onClick} style={{ flex: 1, height: 34, border: 'none', borderBottom: `2px solid ${active ? 'var(--hk-primary-500)' : 'transparent'}`, background: 'transparent', color: active ? 'var(--hk-primary-700)' : 'var(--hk-ink-500)', fontWeight: active ? 600 : 400, fontSize: 14, cursor: 'pointer' }}>
      {children}
    </button>
  )
}
function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}
function Banner({ tone, children }: { tone: 'ok' | 'danger'; children: React.ReactNode }) {
  const ok = tone === 'ok'
  return <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: ok ? '#0b6553' : '#8f322a', background: ok ? 'var(--hk-primary-50)' : '#fbe9e7', border: `1px solid ${ok ? 'var(--hk-primary-100)' : '#f2cdc8'}` }}>{children}</div>
}
// 分隔线带居中文案(社交登录区上方)。
function Divider({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', color: 'var(--hk-ink-500)', fontSize: 12 }}>
      <span style={{ flex: 1, height: 1, background: 'var(--hk-line)' }} />
      {children}
      <span style={{ flex: 1, height: 1, background: 'var(--hk-line)' }} />
    </div>
  )
}

const page: React.CSSProperties = { minHeight: '100%', display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'var(--hk-canvas)', padding: 'var(--hk-space-5)' }
const card: React.CSSProperties = { width: 'min(400px, 100%)', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-2)', padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }
const logo: React.CSSProperties = { width: 24, height: 24, borderRadius: 'var(--hk-radius-sm)', background: 'linear-gradient(135deg, var(--hk-primary-500), var(--hk-primary-700))', display: 'inline-block' }
const inp: React.CSSProperties = { height: 34, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const primary: React.CSSProperties = { height: 38, marginTop: 'var(--hk-space-2)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 14, fontWeight: 600, cursor: 'pointer' }
const ghost: React.CSSProperties = { height: 30, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 12, cursor: 'pointer' }
const socialBtn: React.CSSProperties = { flex: '1 1 auto', minWidth: 120, height: 34, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const linkBtn: React.CSSProperties = { alignSelf: 'flex-start', border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, cursor: 'pointer', padding: 0 }
const linkAnchor: React.CSSProperties = { color: 'var(--hk-primary-700)', textDecoration: 'none' }

// 邀请码预校验提示文案样式:有效=主色,无效=危险色,校验中/不可用=次要灰。
function inviteHintStyle(status: InviteHint['status']): React.CSSProperties {
  const color =
    status === 'ok' ? 'var(--hk-primary-700)' : status === 'invalid' ? '#8f322a' : 'var(--hk-ink-500)'
  return { fontSize: 12, color, marginTop: 2 }
}
