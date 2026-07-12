import { useCallback, useEffect, useState } from 'react'
import type { CSSProperties, ReactNode } from 'react'
import { ApiError } from '../../lib/api'
import { getEmailSettings, sendTestEmail, updateEmailSettings } from './emailApi'
import type { EmailSettingsUpdate } from './emailApi'

/*
 * 邮件服务(SMTP)配置分区。挂在设置中心「邮件」tab 下,消费 email 子系统自有 admin 端点
 * (/v1/admin/email/*),与 platform-settings 是两套 API,故独立成分区。
 * 严守 secret-mask:密码不回显明文(GET 只回 configured 布尔),留空=不修改。
 * 单实例默认租户 1(多租户运维台后续可加租户选择器,此处不引入以免超范围)。
 */

const TENANT_ID = 1

interface FormState {
  smtpHost: string
  smtpPort: string
  smtpUsername: string
  smtpPassword: string
  smtpFrom: string
  smtpFromName: string
  smtpUseTls: boolean
  emailVerifyEnabled: boolean
}

const EMPTY_FORM: FormState = {
  smtpHost: '',
  smtpPort: '',
  smtpUsername: '',
  smtpPassword: '',
  smtpFrom: '',
  smtpFromName: '',
  smtpUseTls: true,
  emailVerifyEnabled: false,
}

export function EmailSmtpSection() {
  const [form, setForm] = useState<FormState>(EMPTY_FORM)
  const [passwordConfigured, setPasswordConfigured] = useState(false)
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [saveMsg, setSaveMsg] = useState<{ tone: 'ok' | 'err'; text: string } | null>(null)
  const [testTo, setTestTo] = useState('')
  const [testing, setTesting] = useState(false)
  const [testMsg, setTestMsg] = useState<{ tone: 'ok' | 'err'; text: string } | null>(null)

  const load = useCallback((signal: AbortSignal) => {
    setLoading(true)
    setLoadError(null)
    getEmailSettings(TENANT_ID, signal)
      .then((resp) => {
        const byKey = new Map(resp.settings.map((s) => [s.key, s]))
        const val = (k: string) => byKey.get(k)?.value ?? ''
        const pw = byKey.get('smtp_password')
        setPasswordConfigured(pw?.configured === true)
        setForm({
          smtpHost: val('smtp_host'),
          smtpPort: val('smtp_port'),
          smtpUsername: val('smtp_username'),
          smtpPassword: '', // 明文不回吐,留空=不修改
          smtpFrom: val('smtp_from'),
          smtpFromName: val('smtp_from_name'),
          smtpUseTls: byKey.has('smtp_use_tls') ? val('smtp_use_tls') === 'true' : true,
          emailVerifyEnabled: val('email_verify_enabled') === 'true',
        })
      })
      .catch((e: unknown) => {
        if (signal.aborted) return
        setLoadError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载邮件配置失败')
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

  const set = <K extends keyof FormState>(k: K, v: FormState[K]) => setForm((f) => ({ ...f, [k]: v }))

  const submit = async () => {
    setSaving(true)
    setSaveMsg(null)
    // 只下发非空文本字段;布尔开关总是下发(反映当前 UI 态)。密码仅在用户输入时下发。
    const body: EmailSettingsUpdate = {
      tenant_id: TENANT_ID,
      smtp_use_tls: form.smtpUseTls,
      email_verify_enabled: form.emailVerifyEnabled,
    }
    if (form.smtpHost.trim()) body.smtp_host = form.smtpHost.trim()
    if (form.smtpPort.trim()) {
      const port = Number(form.smtpPort)
      if (!Number.isInteger(port) || port < 1 || port > 65535) {
        setSaving(false)
        setSaveMsg({ tone: 'err', text: '端口须为 1-65535 的整数' })
        return
      }
      body.smtp_port = port
    }
    if (form.smtpUsername.trim()) body.smtp_username = form.smtpUsername.trim()
    if (form.smtpFrom.trim()) body.smtp_from = form.smtpFrom.trim()
    if (form.smtpFromName.trim()) body.smtp_from_name = form.smtpFromName.trim()
    if (form.smtpPassword !== '') body.smtp_password = form.smtpPassword
    try {
      const res = await updateEmailSettings(body)
      setSaveMsg({ tone: 'ok', text: `已保存(更新 ${res.updated} 项)` })
      if (form.smtpPassword !== '') setPasswordConfigured(true)
      set('smtpPassword', '')
    } catch (e) {
      setSaveMsg({ tone: 'err', text: e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败' })
    } finally {
      setSaving(false)
    }
  }

  const runTest = async () => {
    if (testTo.trim() === '') {
      setTestMsg({ tone: 'err', text: '请填写测试收件人' })
      return
    }
    setTesting(true)
    setTestMsg(null)
    try {
      await sendTestEmail(TENANT_ID, testTo.trim())
      setTestMsg({ tone: 'ok', text: '测试邮件已发送,请检查收件箱' })
    } catch (e) {
      setTestMsg({ tone: 'err', text: e instanceof ApiError ? `${e.message}(${e.code})` : '发送失败' })
    } finally {
      setTesting(false)
    }
  }

  return (
    <section className="hk-card" style={{ marginTop: 'var(--hk-space-4)', padding: 'var(--hk-space-5)' }}>
      <div style={{ marginBottom: 'var(--hk-space-4)' }}>
        <h2 style={{ fontSize: 15, margin: 0, color: 'var(--hk-ink-900)' }}>邮件服务(SMTP)</h2>
        <p style={{ fontSize: 12, color: 'var(--hk-ink-500)', margin: '4px 0 0' }}>
          发信服务器配置,用于发送验证/重置/通知邮件。密码不回显明文,留空即保持原密钥不变。
        </p>
      </div>

      {loading ? (
        <div className="hk-empty">加载中…</div>
      ) : loadError ? (
        <div style={errBox}>{loadError}</div>
      ) : (
        <>
          <div style={grid}>
            <Field label="SMTP 主机">
              <input value={form.smtpHost} onChange={(e) => set('smtpHost', e.target.value)} placeholder="smtp.example.com" style={inp} />
            </Field>
            <Field label="SMTP 端口">
              <input type="number" value={form.smtpPort} onChange={(e) => set('smtpPort', e.target.value)} placeholder="465 / 587" style={inp} />
            </Field>
            <Field label="用户名">
              <input value={form.smtpUsername} onChange={(e) => set('smtpUsername', e.target.value)} autoComplete="off" style={inp} />
            </Field>
            <Field label={passwordConfigured ? '密码(已配置,留空=不修改)' : '密码'}>
              <input
                type="password"
                value={form.smtpPassword}
                onChange={(e) => set('smtpPassword', e.target.value)}
                placeholder={passwordConfigured ? '留空保持原密码不变' : '发信密码 / 授权码'}
                autoComplete="new-password"
                style={inp}
              />
            </Field>
            <Field label="发件人地址">
              <input value={form.smtpFrom} onChange={(e) => set('smtpFrom', e.target.value)} placeholder="noreply@example.com" style={inp} />
            </Field>
            <Field label="发件人名称">
              <input value={form.smtpFromName} onChange={(e) => set('smtpFromName', e.target.value)} placeholder="HUAKAI" style={inp} />
            </Field>
          </div>

          <div style={toggleRow}>
            <ToggleField label="启用 TLS" hint="加密连接(465 端口通常需开启)" on={form.smtpUseTls} onToggle={() => set('smtpUseTls', !form.smtpUseTls)} />
            <ToggleField label="要求邮箱验证" hint="开启后新用户须验证邮箱" on={form.emailVerifyEnabled} onToggle={() => set('emailVerifyEnabled', !form.emailVerifyEnabled)} />
          </div>

          {saveMsg && <div style={saveMsg.tone === 'ok' ? okBox : errBox}>{saveMsg.text}</div>}
          <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 'var(--hk-space-3)' }}>
            <button type="button" disabled={saving} onClick={submit} className="hk-btn hk-btn--green">
              {saving ? '保存中…' : '保存邮件配置'}
            </button>
          </div>

          <div style={{ borderTop: '1px solid var(--hk-line)', margin: 'var(--hk-space-4) 0', paddingTop: 'var(--hk-space-4)' }}>
            <div style={{ fontSize: 13, color: 'var(--hk-ink-900)', fontWeight: 500, marginBottom: 6 }}>发送测试邮件</div>
            <p style={{ fontSize: 12, color: 'var(--hk-ink-500)', margin: '0 0 var(--hk-space-3)' }}>
              使用当前已保存的配置向指定收件人发一封测试信,验证发信链路是否正常。
            </p>
            <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'flex-start', flexWrap: 'wrap' }}>
              <input
                value={testTo}
                onChange={(e) => setTestTo(e.target.value)}
                placeholder="收件人邮箱"
                style={{ ...inp, flex: 1, minWidth: 200 }}
              />
              <button type="button" disabled={testing} onClick={runTest} className="hk-btn">
                {testing ? '发送中…' : '发送测试邮件'}
              </button>
            </div>
            {testMsg && <div style={{ ...(testMsg.tone === 'ok' ? okBox : errBox), marginTop: 'var(--hk-space-2)' }}>{testMsg.text}</div>}
          </div>
        </>
      )}
    </section>
  )
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}

function ToggleField({ label, hint, on, onToggle }: { label: string; hint: string; on: boolean; onToggle: () => void }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)' }}>
      <button
        type="button"
        onClick={onToggle}
        aria-pressed={on}
        style={{
          ...toggle,
          background: on ? 'var(--hk-primary-500)' : 'var(--hk-surface-sunken)',
          borderColor: on ? 'var(--hk-primary-600)' : 'var(--hk-line)',
          justifyContent: on ? 'flex-end' : 'flex-start',
        }}
      >
        <span style={{ ...toggleKnob, background: on ? '#fff' : 'var(--hk-ink-300)' }} />
      </button>
      <div>
        <div style={{ fontSize: 13, color: 'var(--hk-ink-900)' }}>{label}</div>
        <div style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>{hint}</div>
      </div>
    </div>
  )
}

const grid: CSSProperties = {
  display: 'grid',
  gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))',
  gap: 'var(--hk-space-3)',
}
const toggleRow: CSSProperties = {
  display: 'flex',
  gap: 'var(--hk-space-6)',
  flexWrap: 'wrap',
  marginTop: 'var(--hk-space-4)',
}
const inp: CSSProperties = {
  height: 32,
  padding: '0 var(--hk-space-3)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-sm)',
  fontSize: 13,
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-900)',
  width: '100%',
}
const toggle: CSSProperties = {
  position: 'relative',
  display: 'inline-flex',
  alignItems: 'center',
  width: 52,
  height: 26,
  borderRadius: 'var(--hk-radius-pill)',
  border: '1px solid',
  padding: 3,
  cursor: 'pointer',
  flexShrink: 0,
}
const toggleKnob: CSSProperties = { width: 18, height: 18, borderRadius: '50%', flexShrink: 0 }
const errBox: CSSProperties = {
  padding: 'var(--hk-space-2) var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  color: 'var(--hk-danger)',
  background: 'var(--hk-danger-soft)',
  border: '1px solid var(--hk-danger-soft)',
  marginTop: 'var(--hk-space-3)',
}
const okBox: CSSProperties = {
  padding: 'var(--hk-space-2) var(--hk-space-3)',
  borderRadius: 'var(--hk-radius-md)',
  fontSize: 13,
  color: 'var(--hk-primary-700)',
  background: 'var(--hk-primary-50)',
  border: '1px solid var(--hk-primary-50)',
  marginTop: 'var(--hk-space-3)',
}
