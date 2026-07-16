import { useCallback, useEffect, useMemo, useState } from 'react'
import type { CSSProperties, ReactNode } from 'react'
import { ApiError } from '../../lib/api'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { EmailSmtpSection } from './EmailSmtpSection'
import { EmailTemplatesSection } from './EmailTemplatesSection'
import { listSettings, updateSetting } from './api'
import {
  TAB_GROUPS,
  buildSettingUpdate,
  isReadOnly,
  isSecretSetting,
} from './settings'
import type { SettingItemMeta, SettingsTabKey } from './settings'
import type { PlatformSetting } from './types'

/*
 * 设置中心(运维台)。sub2 风格分签设置中心:横向 tab(玉青 active 态)+ 每 tab 一组设置卡。
 * 消费 /v1/admin/platform-settings(admin token,tokenForPath 自动挂 Bearer)。
 * 严守后端 secret-mask:密钥类只显已配置/未配置、编辑空输入=不修改(不空串覆盖);env 来源只读禁用。
 * 所有色值/间距/圆角只引 var(--hk-*) token,复用 ui/StatusBadge,不引新色系。
 */
export function SettingsCenterPage() {
  const [settings, setSettings] = useState<PlatformSetting[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [activeTab, setActiveTab] = useState<SettingsTabKey>('general')
  const [editing, setEditing] = useState<{ meta: SettingItemMeta; setting: PlatformSetting } | null>(null)

  const load = useCallback((signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    listSettings(signal)
      .then((resp) => setSettings(resp.items))
      .catch((e: unknown) => {
        if (signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载设置失败')
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

  // key -> 后端返回的 setting,便于按 tab 归组渲染时 O(1) 取值。
  const byKey = useMemo(() => {
    const m = new Map<string, PlatformSetting>()
    for (const s of settings) m.set(s.key, s)
    return m
  }, [settings])

  const tab = TAB_GROUPS.find((t) => t.key === activeTab) ?? TAB_GROUPS[0]

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>设置中心</h1>
          <p className="hk-sub">平台级配置 · 按分签归类。密钥项不回显明文(仅显是否已配置);环境变量项只读。共 {settings.length} 项。</p>
        </div>
      </header>

      {editing && (
        <EditModal
          meta={editing.meta}
          setting={editing.setting}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            setRefreshNonce((n) => n + 1)
          }}
        />
      )}

      {error && (
        <div style={errorBox}>{error}</div>
      )}

      {/* 横向 tab 栏(玉青风格,active 态玉青下划线 + 玉青底色) */}
      <div role="tablist" aria-label="设置分签" style={tabBar}>
        {TAB_GROUPS.map((t) => {
          const active = t.key === activeTab
          return (
            <button
              key={t.key}
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => setActiveTab(t.key)}
              style={{
                ...tabBtn,
                color: active ? 'var(--hk-primary-700)' : 'var(--hk-ink-500)',
                background: active ? 'var(--hk-primary-50)' : 'transparent',
                borderBottomColor: active ? 'var(--hk-primary-500)' : 'transparent',
                fontWeight: active ? 600 : 500,
              }}
            >
              <span aria-hidden style={{ fontSize: 14 }}>{t.icon}</span>
              {t.label}
            </button>
          )
        })}
      </div>

      {/* 当前 tab 的设置卡 */}
      <div className="hk-card">
        {loading && settings.length === 0 ? (
          <EmptyState title="正在加载设置" hint="请稍候。" />
        ) : tab.items.length === 0 ? (
          <EmptyState title="该分签暂无可配置项" />
        ) : (
          <div>
            {tab.items.map((meta, idx) => {
              const setting = byKey.get(meta.key)
              return (
                <SettingRow
                  key={meta.key}
                  meta={meta}
                  setting={setting}
                  first={idx === 0}
                  onEdit={() => setting && setEditing({ meta, setting })}
                />
              )
            })}
          </div>
        )}
      </div>

      {/* 邮件 tab 追加 SMTP 发信配置 + 鉴权邮件模板分区(走 email 子系统自有端点,非 platform-settings) */}
      {activeTab === 'email' && (
        <>
          <EmailSmtpSection />
          <EmailTemplatesSection />
        </>
      )}
    </div>
  )
}

/** 单个设置项一行:左标签+说明,右当前值+编辑入口。 */
function SettingRow({
  meta,
  setting,
  first,
  onEdit,
}: {
  meta: SettingItemMeta
  setting: PlatformSetting | undefined
  first: boolean
  onEdit: () => void
}) {
  const secret = setting ? isSecretSetting(setting) : meta.control === 'secret'
  const readOnly = setting ? isReadOnly(setting) : false
  return (
    <div style={{ ...rowStyle, borderTop: first ? 'none' : '1px solid var(--hk-line)' }}>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', flexWrap: 'wrap' }}>
          <span style={{ fontSize: 14, color: 'var(--hk-ink-900)', fontWeight: 500 }}>{meta.label}</span>
          <code style={{ fontFamily: 'var(--hk-font-mono)', fontSize: 11, color: 'var(--hk-ink-300)' }}>{meta.key}</code>
          {secret && <StatusBadge tone="info">密钥</StatusBadge>}
          {readOnly && <StatusBadge tone="muted">环境变量</StatusBadge>}
        </div>
        {meta.hint && <div style={{ fontSize: 12, color: 'var(--hk-ink-500)', marginTop: 2 }}>{meta.hint}</div>}
        {setting?.health && setting.health.status !== 'ok' && setting.health.issue && (
          <div style={{ fontSize: 11, color: 'var(--hk-danger)', marginTop: 2 }}>⚠ {setting.health.issue}</div>
        )}
      </div>
      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)', flexShrink: 0 }}>
        <ValueDisplay meta={meta} setting={setting} secret={secret} />
        {setting && (
          readOnly ? (
            <span style={{ fontSize: 12, color: 'var(--hk-ink-300)' }}>只读</span>
          ) : (
            <button type="button" onClick={onEdit} style={linkBtn}>编辑</button>
          )
        )}
      </div>
    </div>
  )
}

/** 当前值的只读展示:密钥脱敏、布尔成"开/关"徽章、长值截断。 */
function ValueDisplay({
  meta,
  setting,
  secret,
}: {
  meta: SettingItemMeta
  setting: PlatformSetting | undefined
  secret: boolean
}) {
  if (!setting) return <span style={{ fontSize: 12, color: 'var(--hk-ink-300)' }}>—</span>
  if (secret) {
    const configured = setting.value_configured === true
    return <StatusBadge tone={configured ? 'ok' : 'muted'}>{configured ? '已配置' : '未配置'}</StatusBadge>
  }
  const raw = setting.value ?? ''
  if (meta.control === 'bool') {
    const on = raw === 'true'
    return <StatusBadge tone={on ? 'ok' : 'muted'}>{on ? '开' : '关'}</StatusBadge>
  }
  if (raw === '') return <span style={{ fontSize: 12, color: 'var(--hk-ink-300)' }}>(空)</span>
  const shown = raw.length > 48 ? `${raw.slice(0, 48)}…` : raw
  return (
    <span style={{ fontSize: 13, color: 'var(--hk-ink-900)', fontFamily: meta.control === 'json' || meta.control === 'number' ? 'var(--hk-font-mono)' : 'inherit', maxWidth: 260, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
      {shown}
    </span>
  )
}

/** 编辑弹窗:按控件类型渲染输入(布尔开关/数字/字符串/JSON 文本域/密钥密文)。 */
function EditModal({
  meta,
  setting,
  onClose,
  onSaved,
}: {
  meta: SettingItemMeta
  setting: PlatformSetting
  onClose: () => void
  onSaved: () => void
}) {
  const secret = isSecretSetting(setting)
  // 密钥类初值留空(明文不回吐;留空=不修改);其余回填当前值。
  const [value, setValue] = useState(secret ? '' : setting.value ?? '')
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async () => {
    const built = buildSettingUpdate(setting, value, reason)
    if ('error' in built) {
      setError(built.error)
      return
    }
    if ('noop' in built) {
      onClose()
      return
    }
    setBusy(true)
    setError(null)
    try {
      await updateSetting(setting.key, built)
      onSaved()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div onClick={onClose} style={overlay}>
      <div onClick={(e) => e.stopPropagation()} style={modal}>
        <h2 style={{ fontSize: 16, margin: 0 }}>
          编辑 · {meta.label}
        </h2>
        <code style={{ fontFamily: 'var(--hk-font-mono)', fontSize: 12, color: 'var(--hk-ink-300)' }}>{setting.key}</code>
        {meta.hint && <p style={{ fontSize: 12, color: 'var(--hk-ink-500)', margin: 0 }}>{meta.hint}</p>}

        <Field label={controlFieldLabel(meta, secret)}>
          <ControlInput meta={meta} secret={secret} value={value} onChange={setValue} />
        </Field>
        <Field label="变更原因(可选,写入审计)">
          <input value={reason} onChange={(e) => setReason(e.target.value)} style={inp} />
        </Field>
        {error && <div style={errorBoxSmall}>{error}</div>}
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', justifyContent: 'flex-end' }}>
          <button type="button" onClick={onClose} className="hk-btn">取消</button>
          <button type="button" disabled={busy} onClick={submit} className="hk-btn hk-btn--green">
            {busy ? '保存中…' : '保存'}
          </button>
        </div>
      </div>
    </div>
  )
}

/** 按控件类型渲染输入控件。 */
function ControlInput({
  meta,
  secret,
  value,
  onChange,
}: {
  meta: SettingItemMeta
  secret: boolean
  value: string
  onChange: (v: string) => void
}) {
  if (secret) {
    // 密钥:密文输入;留空=不修改(buildSettingUpdate 会拦成 noop,绝不空串覆盖)。
    return (
      <input
        type="password"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="留空保持原密钥不变"
        autoComplete="new-password"
        style={inp}
      />
    )
  }
  if (meta.control === 'bool') {
    const on = value === 'true'
    return (
      <button
        type="button"
        onClick={() => onChange(on ? 'false' : 'true')}
        aria-pressed={on}
        style={{
          ...toggle,
          background: on ? 'var(--hk-primary-500)' : 'var(--hk-surface-sunken)',
          borderColor: on ? 'var(--hk-primary-600)' : 'var(--hk-line)',
          justifyContent: on ? 'flex-end' : 'flex-start',
        }}
      >
        <span style={{ ...toggleKnob, background: on ? '#fff' : 'var(--hk-ink-300)' }} />
        <span style={{ position: 'absolute', left: on ? 10 : 'auto', right: on ? 'auto' : 10, fontSize: 11, color: on ? '#fff' : 'var(--hk-ink-500)' }}>
          {on ? '开' : '关'}
        </span>
      </button>
    )
  }
  if (meta.control === 'number') {
    return <input type="number" value={value} onChange={(e) => onChange(e.target.value)} style={inp} />
  }
  if (meta.control === 'json' || meta.control === 'multiline') {
    return (
      <textarea
        value={value}
        onChange={(e) => onChange(e.target.value)}
        rows={meta.control === 'json' ? 6 : 4}
        style={{ ...inp, height: 'auto', padding: 'var(--hk-space-2) var(--hk-space-3)', fontFamily: meta.control === 'json' ? 'var(--hk-font-mono)' : 'inherit', resize: 'vertical' }}
      />
    )
  }
  return <input value={value} onChange={(e) => onChange(e.target.value)} style={inp} />
}

function controlFieldLabel(meta: SettingItemMeta, secret: boolean): string {
  if (secret) return '新值(密钥;留空=不修改)'
  if (meta.control === 'json') return '值(JSON)'
  if (meta.control === 'bool') return '开关'
  return '值'
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}
const tabBar: CSSProperties = {
  display: 'flex',
  gap: 'var(--hk-space-1)',
  borderBottom: '1px solid var(--hk-line)',
  overflowX: 'auto',
  flexWrap: 'wrap',
}
const tabBtn: CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 6,
  padding: 'var(--hk-space-2) var(--hk-space-4)',
  border: 'none',
  borderBottom: '2px solid transparent',
  borderTopLeftRadius: 'var(--hk-radius-sm)',
  borderTopRightRadius: 'var(--hk-radius-sm)',
  fontSize: 13,
  cursor: 'pointer',
  whiteSpace: 'nowrap',
}
const rowStyle: CSSProperties = {
  display: 'flex',
  alignItems: 'flex-start',
  justifyContent: 'space-between',
  gap: 'var(--hk-space-4)',
  padding: 'var(--hk-space-4)',
}
const inp: CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const overlay: CSSProperties = { position: 'fixed', inset: 0, background: 'rgba(28,38,34,0.4)', display: 'flex', alignItems: 'flex-start', justifyContent: 'center', padding: 'var(--hk-space-6)', zIndex: 'var(--hk-z-overlay)' as unknown as number, overflowY: 'auto' }
const modal: CSSProperties = { width: 'min(480px,100%)', background: 'var(--hk-surface)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-3)', padding: 'var(--hk-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }
const errorBox: CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
const errorBoxSmall: CSSProperties = { padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
const linkBtn: CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
const toggle: CSSProperties = { position: 'relative', display: 'inline-flex', alignItems: 'center', width: 64, height: 28, borderRadius: 'var(--hk-radius-pill)', border: '1px solid', padding: 3, cursor: 'pointer' }
const toggleKnob: CSSProperties = { width: 20, height: 20, borderRadius: '50%', flexShrink: 0 }
