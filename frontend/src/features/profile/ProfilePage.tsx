import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import {
  bindTelegram,
  changePassword,
  deletePasskey,
  deleteSelf,
  disableTwoFA,
  enableTwoFA,
  getMe,
  getTwoFAStatus,
  listOAuthBindings,
  listPasskeys,
  regenerateBackupCodes,
  setupTwoFA,
  unlinkOAuthBinding,
  updateProfile,
} from './api'
import { fetchSiteConfig, FALLBACK_SITE_CONFIG, type SiteConfig } from '../../auth/siteConfig'
import { TelegramLoginWidget } from '../../auth/TelegramLoginWidget'
import {
  buildChangePassword,
  EMPTY_CHANGE_PASSWORD,
  isValidTotpCode,
  passkeyLabel,
  providerLabel,
  validateDisplayName,
  viewTwoFA,
  type ChangePasswordForm,
} from './profile'
import { clearAll } from '../../auth/store'
import { NotificationPrefsCard } from './NotificationPrefsCard'
import { ActiveSessionsCard } from './ActiveSessionsCard'
import type { MeResponse, OAuthBinding, PasskeyItem, TwoFASetupResult, TwoFAStatus } from './types'

/*
 * 个人资料·安全(user 壳)。已登录用户自助管理:资料 / 改密 / 两步验证 / 通行密钥 / 社交绑定 / 注销。
 * 所有写动作均为账户自助态(非 money),身份取自 session,前端绝不传 user_id。
 * 安全纪律:secret/备用码/密码仅在内存态一次性展示,绝不写日志、绝不持久化(0 console)。
 */
export function ProfilePage() {
  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-5)', maxWidth: 760 }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>个人资料 · 安全</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>管理你的账号资料、登录密码、两步验证、通行密钥与社交绑定。</p>
      </header>
      <ProfileCard />
      <PasswordCard />
      <TwoFACard />
      <ActiveSessionsCard />
      <PasskeyCard />
      <BindingsCard />
      <NotificationPrefsCard />
      <DangerCard />
    </div>
  )
}

// ---- 资料卡 ----
function ProfileCard() {
  const [me, setMe] = useState<MeResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [editing, setEditing] = useState(false)
  const [draft, setDraft] = useState('')
  const [busy, setBusy] = useState(false)
  const [flash, setFlash] = useState<string | null>(null)

  const load = useCallback((signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    getMe(signal)
      .then((r) => {
        setMe(r)
        setDraft(r.display_name)
      })
      .catch((e: unknown) => {
        if (signal.aborted) return
        setError(errMsg(e, '加载资料失败'))
      })
      .finally(() => {
        if (!signal.aborted) setLoading(false)
      })
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  const save = async () => {
    const v = validateDisplayName(draft)
    if ('error' in v) {
      setError(v.error)
      return
    }
    setBusy(true)
    setError(null)
    try {
      const r = await updateProfile(v.value)
      setMe((prev) => (prev ? { ...prev, display_name: r.display_name } : prev))
      setEditing(false)
      setFlash('显示名已更新')
    } catch (e) {
      setError(errMsg(e, '更新失败'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card title="账号资料">
      {error && <ErrBox>{error}</ErrBox>}
      {flash && <OkBox>{flash}</OkBox>}
      {loading && !me ? (
        <Muted>加载中…</Muted>
      ) : me ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
          <Row label="面板">
            <StatusBadge tone="info">{panelLabel(me.panel)}</StatusBadge>
          </Row>
          <Row label="账号 ID">
            <span style={mono}>{me.user_id}</span>
          </Row>
          <Row label="显示名">
            {editing ? (
              <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center', flexWrap: 'wrap' }}>
                <input value={draft} onChange={(e) => setDraft(e.target.value)} style={{ ...inp, width: 240 }} />
                <button type="button" disabled={busy} onClick={save} style={primaryBtn}>
                  {busy ? '保存中…' : '保存'}
                </button>
                <button type="button" onClick={() => { setEditing(false); setDraft(me.display_name); setError(null) }} style={ghostBtn}>
                  取消
                </button>
              </div>
            ) : (
              <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center' }}>
                <span>{me.display_name || '（未设置）'}</span>
                <button type="button" onClick={() => { setEditing(true); setFlash(null) }} style={linkBtn}>
                  修改
                </button>
              </div>
            )}
          </Row>
        </div>
      ) : null}
    </Card>
  )
}

// ---- 改密卡 ----
function PasswordCard() {
  const [form, setForm] = useState<ChangePasswordForm>(EMPTY_CHANGE_PASSWORD)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [flash, setFlash] = useState<string | null>(null)
  const set = <K extends keyof ChangePasswordForm>(k: K, v: ChangePasswordForm[K]) => setForm((f) => ({ ...f, [k]: v }))

  const submit = async () => {
    const built = buildChangePassword(form)
    if ('error' in built) {
      setError(built.error)
      return
    }
    setBusy(true)
    setError(null)
    setFlash(null)
    try {
      const r = await changePassword(built.old_password, built.new_password)
      setForm(EMPTY_CHANGE_PASSWORD)
      setFlash(`密码已更新${r.sessions_revoked > 0 ? `,已登出其它 ${r.sessions_revoked} 个会话` : ''}`)
    } catch (e) {
      setError(errMsg(e, '改密失败'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card title="登录密码">
      <p style={hint}>修改密码后,除当前会话外的所有登录会话都会被强制登出。</p>
      {error && <ErrBox>{error}</ErrBox>}
      {flash && <OkBox>{flash}</OkBox>}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', maxWidth: 360 }}>
        <Field label="当前密码">
          <input type="password" autoComplete="current-password" value={form.oldPassword} onChange={(e) => set('oldPassword', e.target.value)} style={inp} />
        </Field>
        <Field label="新密码(≥8 位)">
          <input type="password" autoComplete="new-password" value={form.newPassword} onChange={(e) => set('newPassword', e.target.value)} style={inp} />
        </Field>
        <Field label="确认新密码">
          <input type="password" autoComplete="new-password" value={form.confirmPassword} onChange={(e) => set('confirmPassword', e.target.value)} style={inp} />
        </Field>
        <div>
          <button type="button" disabled={busy} onClick={submit} style={primaryBtn}>
            {busy ? '提交中…' : '修改密码'}
          </button>
        </div>
      </div>
    </Card>
  )
}

// ---- 两步验证卡 ----
function TwoFACard() {
  const [status, setStatus] = useState<TwoFAStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [flash, setFlash] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [busy, setBusy] = useState(false)
  // 绑定流程:setup 返回的一次性 secret/qr/备用码(仅内存态,绝不持久化)。
  const [setupData, setSetupData] = useState<TwoFASetupResult | null>(null)
  const [code, setCode] = useState('')
  // 关闭 / 重生成备用码用到的验证码。
  const [actionCode, setActionCode] = useState('')
  const [backupCodes, setBackupCodes] = useState<string[] | null>(null)

  const load = useCallback((signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    getTwoFAStatus(signal)
      .then(setStatus)
      .catch((e: unknown) => {
        if (signal.aborted) return
        setError(errMsg(e, '加载两步验证状态失败'))
      })
      .finally(() => {
        if (!signal.aborted) setLoading(false)
      })
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, refreshNonce])

  const refresh = () => setRefreshNonce((n) => n + 1)
  const view = status ? viewTwoFA(status) : null

  const beginSetup = async () => {
    setBusy(true)
    setError(null)
    setFlash(null)
    try {
      const r = await setupTwoFA()
      setSetupData(r)
    } catch (e) {
      setError(errMsg(e, '发起绑定失败'))
    } finally {
      setBusy(false)
    }
  }

  const confirmEnable = async () => {
    if (!isValidTotpCode(code)) {
      setError('请输入 6 位验证码')
      return
    }
    setBusy(true)
    setError(null)
    try {
      await enableTwoFA(code.trim())
      setSetupData(null)
      setCode('')
      setFlash('两步验证已开启,请妥善保存备用码')
      refresh()
    } catch (e) {
      setError(errMsg(e, '开启失败'))
    } finally {
      setBusy(false)
    }
  }

  const confirmDisable = async () => {
    if (!isValidTotpCode(actionCode)) {
      setError('请输入 6 位验证码或备用码')
      return
    }
    setBusy(true)
    setError(null)
    try {
      await disableTwoFA(actionCode.trim())
      setActionCode('')
      setFlash('两步验证已关闭')
      refresh()
    } catch (e) {
      setError(errMsg(e, '关闭失败'))
    } finally {
      setBusy(false)
    }
  }

  const regenerate = async () => {
    if (!isValidTotpCode(actionCode)) {
      setError('请输入 6 位验证码')
      return
    }
    setBusy(true)
    setError(null)
    try {
      const r = await regenerateBackupCodes(actionCode.trim())
      setBackupCodes(r.backup_codes)
      setActionCode('')
      setFlash('备用码已重新生成,旧备用码已失效')
    } catch (e) {
      setError(errMsg(e, '重生成失败'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card title="两步验证(TOTP)">
      <p style={hint}>开启后,登录时除密码外还需输入验证器生成的 6 位动态码。</p>
      {error && <ErrBox>{error}</ErrBox>}
      {flash && <OkBox>{flash}</OkBox>}
      {loading && !status ? (
        <Muted>加载中…</Muted>
      ) : view ? (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
          <Row label="状态">
            <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center', flexWrap: 'wrap' }}>
              <StatusBadge tone={view.tone}>{view.label}</StatusBadge>
              {view.enabled && (
                <span style={{ fontSize: 12, color: view.lowBackupCodes ? '#8a5e0f' : 'var(--hk-ink-500)' }}>
                  剩余备用码:{view.backupCodesRemaining}
                  {view.lowBackupCodes ? '(偏少,建议重新生成)' : ''}
                </span>
              )}
            </div>
          </Row>

          {!view.available && <Muted>平台未启用两步验证功能。</Muted>}

          {/* 未开启:发起绑定 → 展示 secret/二维码占位 + 备用码 → 输码确认 */}
          {view.available && !view.enabled && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
              {!setupData ? (
                <div>
                  <button type="button" disabled={busy} onClick={beginSetup} style={primaryBtn}>
                    {busy ? '处理中…' : '开启两步验证'}
                  </button>
                </div>
              ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-4)', background: 'var(--hk-surface-sunken)', borderRadius: 'var(--hk-radius-md)', border: '1px solid var(--hk-line)' }}>
                  <p style={{ margin: 0, fontSize: 13 }}>用验证器 App 扫码或手动添加密钥,然后输入 6 位动态码确认。</p>
                  <Row label="密钥">
                    <code style={{ ...mono, userSelect: 'all', wordBreak: 'break-all' }}>{setupData.secret}</code>
                  </Row>
                  <Row label="配置 URI">
                    <code style={{ ...mono, userSelect: 'all', wordBreak: 'break-all', fontSize: 11 }}>{setupData.qr_data}</code>
                  </Row>
                  {setupData.backup_codes.length > 0 && (
                    <Row label="备用码">
                      <BackupCodeList codes={setupData.backup_codes} />
                    </Row>
                  )}
                  <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center', flexWrap: 'wrap' }}>
                    <input value={code} onChange={(e) => setCode(e.target.value)} placeholder="6 位动态码" inputMode="numeric" style={{ ...inp, width: 140 }} />
                    <button type="button" disabled={busy} onClick={confirmEnable} style={primaryBtn}>
                      确认开启
                    </button>
                    <button type="button" onClick={() => { setSetupData(null); setCode('') }} style={ghostBtn}>
                      取消
                    </button>
                  </div>
                </div>
              )}
            </div>
          )}

          {/* 已开启:关闭 + 重生成备用码 */}
          {view.available && view.enabled && (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
              <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center', flexWrap: 'wrap' }}>
                <input value={actionCode} onChange={(e) => setActionCode(e.target.value)} placeholder="6 位动态码 / 备用码" inputMode="numeric" style={{ ...inp, width: 180 }} />
                <button type="button" disabled={busy} onClick={confirmDisable} style={dangerBtn}>
                  关闭两步验证
                </button>
                <button type="button" disabled={busy} onClick={regenerate} style={ghostBtn}>
                  重新生成备用码
                </button>
              </div>
              {backupCodes && (
                <div style={{ padding: 'var(--hk-space-4)', background: 'var(--hk-surface-sunken)', borderRadius: 'var(--hk-radius-md)', border: '1px solid var(--hk-line)' }}>
                  <p style={{ margin: '0 0 var(--hk-space-2)', fontSize: 13 }}>新备用码(仅此一次显示,请立即保存):</p>
                  <BackupCodeList codes={backupCodes} />
                </div>
              )}
            </div>
          )}
        </div>
      ) : null}
    </Card>
  )
}

// ---- 通行密钥卡 ----
function PasskeyCard() {
  const [items, setItems] = useState<PasskeyItem[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [flash, setFlash] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback((signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    listPasskeys(signal)
      .then((r) => setItems(r.passkeys ?? []))
      .catch((e: unknown) => {
        if (signal.aborted) return
        setError(errMsg(e, '加载通行密钥失败'))
      })
      .finally(() => {
        if (!signal.aborted) setLoading(false)
      })
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, refreshNonce])

  const remove = async (p: PasskeyItem) => {
    setBusyId(p.id)
    setError(null)
    setFlash(null)
    try {
      await deletePasskey(p.id)
      setFlash('通行密钥已删除')
      setRefreshNonce((n) => n + 1)
    } catch (e) {
      // 后端可能要求近期密码 / 2FA 的 step-up 证明(passkey_step_up_required)。
      setError(errMsg(e, '删除失败'))
    } finally {
      setBusyId(null)
    }
  }

  return (
    <Card title="通行密钥(Passkey)">
      <p style={hint}>
        通行密钥用设备生物识别或硬件密钥免密登录。
        {/* 注:新增通行密钥需 WebAuthn 浏览器交互(navigator.credentials)且后端要求 step-up 证明,
            该注册流程未在本页实现,留作占位;此处仅做列表与删除。 */}
        新增通行密钥需在登录页 / 专用注册流程中完成。
      </p>
      {error && <ErrBox>{error}</ErrBox>}
      {flash && <OkBox>{flash}</OkBox>}
      {loading && items.length === 0 ? (
        <Muted>加载中…</Muted>
      ) : items.length === 0 ? (
        <Muted>尚未添加任何通行密钥。</Muted>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
          {items.map((p) => (
            <div key={p.id} style={listRow}>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                <span style={{ fontWeight: 600 }}>{passkeyLabel(p)}</span>
                <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>
                  添加于 {fmt(p.created_at)}
                  {p.last_used_at ? ` · 最近使用 ${fmt(p.last_used_at)}` : ''}
                  {p.clone_warning ? ' · ⚠ 检测到克隆风险' : ''}
                </span>
              </div>
              <button type="button" disabled={busyId === p.id} onClick={() => remove(p)} style={dangerLinkBtn}>
                删除
              </button>
            </div>
          ))}
        </div>
      )}
    </Card>
  )
}

// ---- 社交绑定卡 ----
function BindingsCard() {
  const [items, setItems] = useState<OAuthBinding[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [flash, setFlash] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [busy, setBusy] = useState<string | null>(null)
  // 站点配置:取 telegram bot 用户名 + 是否启用 telegram,用于决定是否渲染「绑定 Telegram」widget。
  const [site, setSite] = useState<SiteConfig>(FALLBACK_SITE_CONFIG)
  useEffect(() => {
    let alive = true
    fetchSiteConfig()
      .then((c) => { if (alive) setSite(c) })
      .catch(() => { /* 取站点配置失败则不渲染 telegram 绑定,不影响其余绑定管理 */ })
    return () => { alive = false }
  }, [])

  const load = useCallback((signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    listOAuthBindings(signal)
      .then((r) => setItems(r.bindings ?? []))
      .catch((e: unknown) => {
        if (signal.aborted) return
        setError(errMsg(e, '加载社交绑定失败'))
      })
      .finally(() => {
        if (!signal.aborted) setLoading(false)
      })
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, refreshNonce])

  const unlink = async (b: OAuthBinding) => {
    setBusy(b.provider)
    setError(null)
    setFlash(null)
    try {
      await unlinkOAuthBinding(b.provider)
      setFlash(`已解绑 ${providerLabel(b.provider)}`)
      setRefreshNonce((n) => n + 1)
    } catch (e) {
      // 末位登录方式由后端保护(409 last_login_method)。
      setError(errMsg(e, '解绑失败'))
    } finally {
      setBusy(null)
    }
  }

  // 绑定 Telegram(「先绑定后登录」):widget onauth → bind 端点 → 刷新列表。
  const onBindTelegram = async (params: Record<string, string>) => {
    setBusy('telegram')
    setError(null)
    setFlash(null)
    try {
      await bindTelegram(params)
      setFlash('已绑定 Telegram,现在可以在登录页用 Telegram 直接登录了。')
      setRefreshNonce((n) => n + 1)
    } catch (e) {
      if (e instanceof ApiError && e.code === 'social_identity_already_bound') {
        setError('这个 Telegram 账号已被其他账户绑定,无法重复绑定。')
      } else {
        setError(errMsg(e, '绑定 Telegram 失败'))
      }
    } finally {
      setBusy(null)
    }
  }

  // 渲染绑定入口的条件:运营启用 telegram + 配了公开 bot 用户名 + 本人尚未绑定 telegram。
  const telegramEnabled = site.oauthProviders.includes('telegram') && site.telegramBotUsername.length > 0
  const telegramBound = items.some((b) => b.provider === 'telegram')

  return (
    <Card title="社交账号绑定">
      <p style={hint}>已绑定的第三方登录方式。无法解绑唯一的登录方式。</p>
      {error && <ErrBox>{error}</ErrBox>}
      {flash && <OkBox>{flash}</OkBox>}
      {loading && items.length === 0 ? (
        <Muted>加载中…</Muted>
      ) : items.length === 0 ? (
        <Muted>尚未绑定任何社交账号。</Muted>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
          {items.map((b) => (
            <div key={b.provider} style={listRow}>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                <span style={{ fontWeight: 600 }}>{providerLabel(b.provider)}</span>
                <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>
                  {b.subject || '—'} · 绑定于 {b.linked_at}
                </span>
              </div>
              <button type="button" disabled={busy === b.provider} onClick={() => unlink(b)} style={dangerLinkBtn}>
                解绑
              </button>
            </div>
          ))}
        </div>
      )}
      {/* 绑定 Telegram(先绑定后登录):仅在运营启用 telegram 且本人尚未绑定时显示官方 Login Widget。 */}
      {telegramEnabled && !telegramBound && (
        <div style={{ marginTop: 'var(--hk-space-3)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
          <p style={hint}>绑定 Telegram 后,即可在登录页用 Telegram 直接登录。</p>
          <TelegramLoginWidget botUsername={site.telegramBotUsername} onAuth={onBindTelegram} />
        </div>
      )}
    </Card>
  )
}

// ---- 危险区:注销账号 ----
function DangerCard() {
  const [confirming, setConfirming] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const doDelete = async () => {
    setBusy(true)
    setError(null)
    try {
      await deleteSelf()
      // 删号成功:本地清空全部 token,跳回登录(后端已撤全部 session)。
      clearAll()
      window.location.assign('/login')
    } catch (e) {
      // 末位管理员由后端保护(409 last_admin_protected)。
      setError(errMsg(e, '注销失败'))
      setBusy(false)
    }
  }

  return (
    <Card title="注销账号" tone="danger">
      <p style={hint}>注销后账号将被停用,所有 API Key 与登录会话立即失效。此操作不可撤销。</p>
      {error && <ErrBox>{error}</ErrBox>}
      {!confirming ? (
        <div>
          <button type="button" onClick={() => setConfirming(true)} style={dangerBtn}>
            注销我的账号
          </button>
        </div>
      ) : (
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center', flexWrap: 'wrap' }}>
          <span style={{ fontSize: 13, color: 'var(--hk-danger)' }}>确认要永久注销账号吗?</span>
          <button type="button" disabled={busy} onClick={doDelete} style={dangerBtn}>
            {busy ? '处理中…' : '确认注销'}
          </button>
          <button type="button" onClick={() => setConfirming(false)} style={ghostBtn}>
            取消
          </button>
        </div>
      )}
    </Card>
  )
}

// ---- 复用小组件 ----
function Card({ title, tone, children }: { title: string; tone?: 'danger'; children: React.ReactNode }) {
  return (
    <section
      style={{
        background: 'var(--hk-surface)',
        border: `1px solid ${tone === 'danger' ? 'var(--hk-danger-soft)' : 'var(--hk-line)'}`,
        borderRadius: 'var(--hk-radius-lg)',
        boxShadow: 'var(--hk-shadow-1)',
        padding: 'var(--hk-space-5)',
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--hk-space-3)',
      }}
    >
      <h2 style={{ fontSize: 16, margin: 0, color: tone === 'danger' ? 'var(--hk-danger)' : 'var(--hk-ink-900)' }}>{title}</h2>
      {children}
    </section>
  )
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-start' }}>
      <span style={{ width: 90, flexShrink: 0, fontSize: 13, color: 'var(--hk-ink-500)', paddingTop: 2 }}>{label}</span>
      <div style={{ flex: 1, fontSize: 13 }}>{children}</div>
    </div>
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

function BackupCodeList({ codes }: { codes: string[] }) {
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-2)' }}>
      {codes.map((c) => (
        <code key={c} style={{ ...mono, userSelect: 'all', padding: '2px 8px', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)' }}>
          {c}
        </code>
      ))}
    </div>
  )
}

function Muted({ children }: { children: React.ReactNode }) {
  return <div style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>{children}</div>
}
function ErrBox({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{children}</div>
}
function OkBox({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#0b6553', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }}>{children}</div>
}

function errMsg(e: unknown, fallback: string): string {
  return e instanceof ApiError ? `${e.message}(${e.code})` : fallback
}
function fmt(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false })
}
function panelLabel(panel: string): string {
  switch (panel) {
    case 'admin':
      return '运维台'
    case 'user':
      return '用户台'
    default:
      return panel || '—'
  }
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const mono: React.CSSProperties = { fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)' }
const hint: React.CSSProperties = { margin: 0, fontSize: 12, color: 'var(--hk-ink-500)' }
const listRow: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-3) var(--hk-space-4)', background: 'var(--hk-surface-sunken)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const dangerBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid #d98178', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-danger-soft)', color: 'var(--hk-danger)', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const linkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
const dangerLinkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-danger)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)', flexShrink: 0 }
