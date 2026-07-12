import { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { getBuildInfo, getEmailSettings, saveEmailSettings, sendSmtpTest } from './api'
import {
  buildEmailSettingsUpdate,
  buildSmtpTest,
  displayBuildTime,
  displayCommit,
  displayVersion,
  EMPTY_SMTP_SETTINGS,
  EMPTY_SMTP_TEST,
  isDevBuild,
  settingsToForm,
  type SmtpSettingsForm,
  type SmtpTestForm,
} from './version'
import type { BuildInfo } from './types'

/*
 * 版本与维护(运维台/admin)。面板:
 *  1) 构建版本信息卡:GET /v1/admin/version → version/commit/build_time/go_version。
 *  2) SMTP 配置卡:GET/PUT /v1/admin/email/settings,读回填 + 保存 host/port/账号/口令/发件人。
 *     口令是凭证:GET 不回显明文(后端掩码),只显示「已配置/未配置」;口令留空=保留原口令(请求体省略该字段)。
 *  3) SMTP 连接测试:POST /v1/admin/email/test,用当前已配 SMTP 设置向指定邮箱发一封测试信。
 * 零 console、不打印口令。
 */
export function VersionPage() {
  const [info, setInfo] = useState<BuildInfo | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setLoadError(null)
    getBuildInfo(ctrl.signal)
      .then((b) => setInfo(b))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setLoadError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载版本信息失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [])

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>版本与维护</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          运维台 · 查看当前网关构建版本,验证 SMTP 邮件链路是否可用。
        </p>
      </header>

      <BuildInfoCard info={info} loading={loading} error={loadError} />
      <SmtpSettingsCard />
      <SmtpTestCard />
    </div>
  )
}

/*
 * SMTP 配置卡:读回填(GET /settings)+ 保存(PUT /settings)。
 * 凭证安全:口令输入框永不预填后端值;留空提交=保留原口令(buildEmailSettingsUpdate 会省略 smtp_password)。
 * 仅当用户主动输入新口令时才覆盖。保存动作弹二次确认(写入平台级邮件凭证)。
 */
function SmtpSettingsCard() {
  const [tenantId, setTenantId] = useState('0')
  const [form, setForm] = useState<SmtpSettingsForm>(EMPTY_SMTP_SETTINGS)
  const [pwConfigured, setPwConfigured] = useState(false)
  const [loading, setLoading] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [result, setResult] = useState<{ tone: 'ok' | 'danger'; text: string } | null>(null)

  const set = <K extends keyof SmtpSettingsForm>(k: K, v: SmtpSettingsForm[K]) =>
    setForm((f) => ({ ...f, [k]: v }))

  // 按当前租户号拉取已存设置回填表单。口令永不回显(后端掩码 + settingsToForm 强制空串)。
  const load = async () => {
    const raw = tenantId.trim()
    const n = Number(raw || '0')
    if (!Number.isInteger(n) || n < 0) {
      setLoadError('租户号须为非负整数')
      return
    }
    const ctrl = new AbortController()
    setLoading(true)
    setLoadError(null)
    setResult(null)
    try {
      const resp = await getEmailSettings(n, ctrl.signal)
      const mapped = settingsToForm(resp)
      setForm(mapped.form)
      setPwConfigured(mapped.passwordConfigured)
    } catch (e) {
      setLoadError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载 SMTP 设置失败')
    } finally {
      setLoading(false)
    }
  }

  const onSave = async (e: React.FormEvent) => {
    e.preventDefault()
    setResult(null)
    const built = buildEmailSettingsUpdate(form, tenantId)
    if ('error' in built) {
      setResult({ tone: 'danger', text: built.error })
      return
    }
    // 二次确认:写入平台级 SMTP 凭证。明确告知口令留空=保留原值,避免误清除。
    const pwNote =
      form.password.length > 0
        ? '将覆盖 SMTP 口令为新输入值。'
        : pwConfigured
          ? '口令留空,保留当前已配置口令不变。'
          : '尚未配置口令(留空,本次不写入口令)。'
    if (!window.confirm(`确认保存租户 ${built.tenant_id} 的 SMTP 设置?\n${pwNote}`)) return
    setSaving(true)
    try {
      const resp = await saveEmailSettings(built)
      setResult({ tone: 'ok', text: `已保存(租户 ${resp.tenant_id},写入 ${resp.updated} 项)。` })
      // 保存成功后清空口令输入框(避免残留),并重新拉取以刷新「已配置」状态。
      set('password', '')
      await load()
    } catch (err) {
      setResult({
        tone: 'danger',
        text: err instanceof ApiError ? `保存失败:${err.message}(${err.code})` : '保存失败',
      })
    } finally {
      setSaving(false)
    }
  }

  return (
    <section style={card}>
      <div style={cardHead}>
        <h2 style={cardTitle}>SMTP 配置</h2>
        <StatusBadge tone={pwConfigured ? 'ok' : 'warn'}>{pwConfigured ? '口令已配置' : '口令未配置'}</StatusBadge>
      </div>
      <p style={{ margin: 0, fontSize: 13, color: 'var(--hk-ink-500)' }}>
        配置平台发信使用的 SMTP 服务。口令为凭证不回显;留空保存=保留当前口令不变,填入新值才覆盖。
        文本字段留空同样保留原值,开关项始终按当前状态保存。
      </p>

      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-3)', alignItems: 'flex-end' }}>
        <label style={fieldLabel}>
          租户号(留空=0)
          <input
            value={tenantId}
            onChange={(e) => setTenantId(e.target.value)}
            inputMode="numeric"
            placeholder="0"
            style={{ ...inp, width: 120 }}
          />
        </label>
        <button type="button" onClick={load} disabled={loading} style={secondaryBtn}>
          {loading ? '加载中…' : '读取设置'}
        </button>
      </div>

      {loadError && <div style={errBox}>{loadError}</div>}

      <form onSubmit={onSave} style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-3)' }}>
          <label style={fieldLabel}>
            SMTP 主机
            <input
              value={form.host}
              onChange={(e) => set('host', e.target.value)}
              placeholder="smtp.example.com"
              style={{ ...inp, minWidth: 240 }}
            />
          </label>
          <label style={fieldLabel}>
            端口
            <input
              value={form.port}
              onChange={(e) => set('port', e.target.value)}
              inputMode="numeric"
              placeholder="587"
              style={{ ...inp, width: 120 }}
            />
          </label>
        </div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-3)' }}>
          <label style={fieldLabel}>
            账号(用户名)
            <input
              value={form.username}
              onChange={(e) => set('username', e.target.value)}
              autoComplete="off"
              placeholder="mailer@example.com"
              style={{ ...inp, minWidth: 240 }}
            />
          </label>
          <label style={fieldLabel}>
            口令(凭证 · 留空=保留)
            <input
              type="password"
              value={form.password}
              onChange={(e) => set('password', e.target.value)}
              autoComplete="new-password"
              placeholder={pwConfigured ? '••••••(留空保留)' : '未配置,填入以设置'}
              style={{ ...inp, minWidth: 240 }}
            />
          </label>
        </div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-3)' }}>
          <label style={fieldLabel}>
            发件地址
            <input
              type="email"
              value={form.from}
              onChange={(e) => set('from', e.target.value)}
              placeholder="noreply@example.com"
              style={{ ...inp, minWidth: 240 }}
            />
          </label>
          <label style={fieldLabel}>
            发件人名称
            <input
              value={form.fromName}
              onChange={(e) => set('fromName', e.target.value)}
              placeholder="HUAKAI"
              style={{ ...inp, minWidth: 200 }}
            />
          </label>
        </div>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-5)', alignItems: 'center' }}>
          <label style={checkRow}>
            <input type="checkbox" checked={form.useTls} onChange={(e) => set('useTls', e.target.checked)} />
            启用 TLS
          </label>
          <label style={checkRow}>
            <input
              type="checkbox"
              checked={form.verifyEmail}
              onChange={(e) => set('verifyEmail', e.target.checked)}
            />
            要求邮箱验证
          </label>
        </div>
        <div>
          <button type="submit" disabled={saving} style={primaryBtn}>
            {saving ? '保存中…' : '保存 SMTP 设置'}
          </button>
        </div>
      </form>

      {result && (
        <div
          style={{
            padding: 'var(--hk-space-3)',
            borderRadius: 'var(--hk-radius-md)',
            fontSize: 13,
            ...(result.tone === 'ok'
              ? { color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
              : { color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }),
          }}
        >
          {result.text}
        </div>
      )}
    </section>
  )
}

function BuildInfoCard({ info, loading, error }: { info: BuildInfo | null; loading: boolean; error: string | null }) {
  return (
    <section style={card}>
      <div style={cardHead}>
        <h2 style={cardTitle}>构建版本</h2>
        {info && isDevBuild(info) && <StatusBadge tone="warn">未打标本地构建</StatusBadge>}
      </div>
      {error && <div style={errBox}>{error}</div>}
      {loading ? (
        <div style={emptyBox}>加载中…</div>
      ) : info ? (
        <dl style={{ display: 'grid', gridTemplateColumns: 'max-content 1fr', gap: 'var(--hk-space-2) var(--hk-space-5)', margin: 0 }}>
          <Row label="版本">{displayVersion(info.version)}</Row>
          <Row label="Commit" mono>
            {displayCommit(info.commit)}
          </Row>
          <Row label="构建时间">{displayBuildTime(info.build_time)}</Row>
          <Row label="Go 运行时" mono>
            {info.go_version || '—'}
          </Row>
        </dl>
      ) : (
        !error && <div style={emptyBox}>暂无版本信息。</div>
      )}
    </section>
  )
}

function SmtpTestCard() {
  const [form, setForm] = useState<SmtpTestForm>(EMPTY_SMTP_TEST)
  const [sending, setSending] = useState(false)
  const [result, setResult] = useState<{ tone: 'ok' | 'danger'; text: string } | null>(null)

  const set = <K extends keyof SmtpTestForm>(k: K, v: SmtpTestForm[K]) => setForm((f) => ({ ...f, [k]: v }))

  const onSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setResult(null)
    const built = buildSmtpTest(form)
    if ('error' in built) {
      setResult({ tone: 'danger', text: built.error })
      return
    }
    setSending(true)
    try {
      const resp = await sendSmtpTest(built)
      setResult({
        tone: 'ok',
        text: resp.sent ? `测试信已发送至 ${built.to}(租户 ${resp.tenant_id})。` : '请求成功但未确认发送。',
      })
    } catch (err) {
      setResult({
        tone: 'danger',
        text: err instanceof ApiError ? `发送失败:${err.message}(${err.code})` : '发送失败',
      })
    } finally {
      setSending(false)
    }
  }

  return (
    <section style={card}>
      <div style={cardHead}>
        <h2 style={cardTitle}>SMTP 连接测试</h2>
      </div>
      <p style={{ margin: 0, fontSize: 13, color: 'var(--hk-ink-500)' }}>
        用当前已保存的 SMTP 配置向下方邮箱发一封测试信。本页不修改 SMTP 设置,仅验证链路。
      </p>
      <form onSubmit={onSubmit} style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-3)', alignItems: 'flex-end' }}>
        <label style={fieldLabel}>
          收件邮箱
          <input
            type="email"
            value={form.to}
            onChange={(e) => set('to', e.target.value)}
            placeholder="ops@example.com"
            style={{ ...inp, minWidth: 240 }}
          />
        </label>
        <label style={fieldLabel}>
          租户号(留空=0)
          <input
            value={form.tenantId}
            onChange={(e) => set('tenantId', e.target.value)}
            inputMode="numeric"
            placeholder="0"
            style={{ ...inp, width: 120 }}
          />
        </label>
        <button type="submit" disabled={sending} style={primaryBtn}>
          {sending ? '发送中…' : '发送测试信'}
        </button>
      </form>
      {result && (
        <div
          style={{
            padding: 'var(--hk-space-3)',
            borderRadius: 'var(--hk-radius-md)',
            fontSize: 13,
            ...(result.tone === 'ok'
              ? { color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
              : { color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }),
          }}
        >
          {result.text}
        </div>
      )}
    </section>
  )
}

function Row({ label, mono, children }: { label: string; mono?: boolean; children: React.ReactNode }) {
  return (
    <>
      <dt style={{ fontSize: 12, color: 'var(--hk-ink-500)', alignSelf: 'center' }}>{label}</dt>
      <dd
        style={{
          margin: 0,
          fontSize: 13,
          color: 'var(--hk-ink-900)',
          fontFamily: mono ? 'var(--hk-font-mono)' : undefined,
          wordBreak: 'break-all',
        }}
      >
        {children}
      </dd>
    </>
  )
}

const card: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-3)',
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
  boxShadow: 'var(--hk-shadow-1)',
  padding: 'var(--hk-space-5)',
}
const cardHead: React.CSSProperties = { display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--hk-space-3)' }
const cardTitle: React.CSSProperties = { margin: 0, fontSize: 15, fontWeight: 600, color: 'var(--hk-ink-900)' }
const errBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
const emptyBox: React.CSSProperties = { padding: 'var(--hk-space-6)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }
const fieldLabel: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const secondaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const checkRow: React.CSSProperties = { display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', fontSize: 13, color: 'var(--hk-ink-900)', cursor: 'pointer' }
