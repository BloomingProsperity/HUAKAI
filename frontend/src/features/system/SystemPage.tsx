import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { listSettings, updateSetting } from './api'
import { buildSettingUpdate, displayValue, isReadOnly, isSecretKey, sourceLabel } from './settings'
import type { PlatformSetting } from './types'

/*
 * 系统设置(运维台,P0)。管线第 7 站。/v1/admin/platform-settings 列表 + 单项编辑。
 * 严守后端 secret-mask:密钥类只显已配置/未配置、编辑空输入=不修改;env 来源只读。
 */
export function SystemPage() {
  const [settings, setSettings] = useState<PlatformSetting[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [editing, setEditing] = useState<PlatformSetting | null>(null)

  const load = useCallback((signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    listSettings(signal)
      .then((resp) => setSettings(resp.items))
      .catch((e: unknown) => {
        if (signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载系统设置失败')
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

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>系统设置</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          管线第 7 站 · 平台级配置。密钥项不回显明文(仅显是否已配置);环境变量项只读。共 {settings.length} 项。
        </p>
      </header>

      {editing && (
        <EditModal
          setting={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            setRefreshNonce((n) => n + 1)
          }}
        />
      )}

      {error && <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }}>{error}</div>}

      <div style={{ background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }}>
        {loading && settings.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : settings.length === 0 ? (
          <Empty>没有可见的系统设置。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['配置项', '当前值', '来源', '更新', ''].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {settings.map((s) => (
                  <tr key={s.key} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={td}>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
                        <code style={{ fontFamily: 'var(--hk-font-mono)', fontSize: 12, color: 'var(--hk-ink-900)' }}>{s.key}</code>
                        {isSecretKey(s) && <StatusBadge tone="info">密钥</StatusBadge>}
                      </div>
                    </td>
                    <td style={td}>
                      <span style={{ color: isSecretKey(s) ? 'var(--hk-ink-500)' : 'var(--hk-ink-900)' }}>{displayValue(s)}</span>
                      {s.health && s.health.status !== 'ok' && s.health.issue && (
                        <div style={{ fontSize: 11, color: '#8f322a' }}>⚠ {s.health.issue}</div>
                      )}
                    </td>
                    <td style={td}>{sourceLabel(s.source)}</td>
                    <td style={td}>
                      <div style={{ display: 'flex', flexDirection: 'column', fontSize: 11, color: 'var(--hk-ink-300)' }}>
                        <span>{s.updated_by || '—'}</span>
                        <span>{s.updated_at ? fmt(s.updated_at) : ''}</span>
                      </div>
                    </td>
                    <td style={{ ...td, textAlign: 'right' }}>
                      {isReadOnly(s) ? (
                        <span style={{ fontSize: 12, color: 'var(--hk-ink-300)' }}>只读</span>
                      ) : (
                        <button type="button" onClick={() => setEditing(s)} style={linkBtn}>
                          编辑
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}

function EditModal({ setting, onClose, onSaved }: { setting: PlatformSetting; onClose: () => void; onSaved: () => void }) {
  const secret = isSecretKey(setting)
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
    <div onClick={onClose} style={{ position: 'fixed', inset: 0, background: 'rgba(28,38,34,0.4)', display: 'flex', alignItems: 'flex-start', justifyContent: 'center', padding: 'var(--hk-space-6)', zIndex: 'var(--hk-z-overlay)' as unknown as number, overflowY: 'auto' }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 'min(460px,100%)', background: 'var(--hk-surface)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-3)', padding: 'var(--hk-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
        <h2 style={{ fontSize: 16 }}>
          编辑 <code style={{ fontFamily: 'var(--hk-font-mono)', fontSize: 14 }}>{setting.key}</code>
        </h2>
        <Field label={secret ? '新值(密钥;留空=不修改)' : '值'}>
          {secret ? (
            <input type="password" value={value} onChange={(e) => setValue(e.target.value)} placeholder="留空保持原密钥不变" autoComplete="new-password" style={inp} />
          ) : (
            <input value={value} onChange={(e) => setValue(e.target.value)} style={inp} />
          )}
        </Field>
        <Field label="变更原因(可选,写入审计)">
          <input value={reason} onChange={(e) => setReason(e.target.value)} style={inp} />
        </Field>
        {error && <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }}>{error}</div>}
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', justifyContent: 'flex-end' }}>
          <button type="button" onClick={onClose} style={ghostBtn}>
            取消
          </button>
          <button type="button" disabled={busy} onClick={submit} style={primaryBtn}>
            {busy ? '保存中…' : '保存'}
          </button>
        </div>
      </div>
    </div>
  )
}

function fmt(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString('zh-CN', { hour12: false })
}
function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}
function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'top' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const linkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
