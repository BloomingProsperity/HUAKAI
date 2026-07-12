import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import type { HermesAuthQuery } from './hermesClient'
import {
  createProfile,
  deleteProfile,
  disableSettings,
  enableSettings,
  getSettings,
  listProfiles,
} from './hermesAdminApi'
import {
  apiSourceLabel,
  isProfileInUse,
  profileKindLabel,
  validateEnable,
  validateProfile,
  type ProfileForm,
} from './hermesAdmin'
import {
  API_SOURCE_DEDICATED,
  API_SOURCE_MANAGED,
  type HermesAPISource,
  type HermesProfile,
  type HermesSettings,
} from './hermesAdminTypes'

/*
 * Hermes 配置卡:per-user 配置启停 + api-profile CRUD。
 *
 * 配置启停=该用户的 Hermes 配置(非全局 KNOB):
 *   - api_source=托管 HUAKAI API(managed):不绑 profile
 *   - api_source=专用分组(dedicated_group):必须绑一个 kind=dedicated_group 的 profile
 * api-profile:列 / 建 / 删。响应只含 FK 引用(api_key_id/pool_group_id),无 secret。
 * 删 profile 时若其正被配置引用,后端返回 409 profile_in_use,本卡如实提示。
 */
export function HermesSettingsCard({ adminToken, auth }: { adminToken: string; auth: HermesAuthQuery }) {
  const [settings, setSettings] = useState<HermesSettings | null>(null)
  const [profiles, setProfiles] = useState<HermesProfile[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [ok, setOk] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  // 启用表单
  const [apiSource, setApiSource] = useState<HermesAPISource>(API_SOURCE_MANAGED)
  const [profileId, setProfileId] = useState<string>('')

  const reload = useCallback(
    async (signal?: AbortSignal) => {
      setError(null)
      try {
        const [s, ps] = await Promise.all([
          getSettings(adminToken, auth, signal),
          listProfiles(adminToken, auth, signal),
        ])
        setSettings(s)
        setProfiles(ps)
        // 表单初值跟随当前配置。
        setApiSource(s.api_source === API_SOURCE_DEDICATED ? API_SOURCE_DEDICATED : API_SOURCE_MANAGED)
        setProfileId(s.profile_id != null ? String(s.profile_id) : '')
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return
        setError(describeErr(e))
      } finally {
        setLoading(false)
      }
    },
    [adminToken, auth],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    void reload(ctrl.signal)
    return () => ctrl.abort()
  }, [reload])

  const onEnable = () => {
    setError(null)
    setOk(null)
    // 解析 profile 输入:空串=不绑定(null);否则取数值(非正整数交给 validateEnable 兜底拒)。
    const raw = profileId.trim()
    const parsedProfile = raw === '' ? null : Number(raw)
    const v = validateEnable({ apiSource, profileId: parsedProfile })
    if (!v.ok) {
      setError(v.error)
      return
    }
    setBusy(true)
    enableSettings(adminToken, auth, { api_source: v.apiSource, profile_id: v.profileId })
      .then((s) => {
        setSettings(s)
        setOk(`已启用(${apiSourceLabel(s.api_source)}${s.profile_id ? ` · profile #${s.profile_id}` : ''})`)
      })
      .catch((e) => setError(describeErr(e)))
      .finally(() => setBusy(false))
  }

  const onDisable = () => {
    setError(null)
    setOk(null)
    if (!window.confirm('确认停用该用户的 Hermes 配置?停用后其将无法使用 Hermes。')) return
    setBusy(true)
    disableSettings(adminToken, auth)
      .then((s) => {
        setSettings(s)
        setOk('已停用')
      })
      .catch((e) => setError(describeErr(e)))
      .finally(() => setBusy(false))
  }

  const onDeleteProfile = (p: HermesProfile) => {
    setError(null)
    setOk(null)
    const inUse = isProfileInUse(settings, p.id)
    const warn = inUse
      ? '\n\n注意:该 profile 正被当前配置引用,后端会拒绝删除(409 profile_in_use)。请先改用其它来源再删。'
      : ''
    if (!window.confirm(`确认删除 API profile「${p.name}」(#${p.id})?此操作不可撤销。${warn}`)) return
    setBusy(true)
    deleteProfile(adminToken, auth, p.id)
      .then(() => {
        setProfiles((prev) => prev.filter((x) => x.id !== p.id))
        setOk(`已删除 profile #${p.id}`)
      })
      .catch((e) => setError(describeErr(e)))
      .finally(() => setBusy(false))
  }

  return (
    <section style={card}>
      <h2 style={h2}>Hermes 配置</h2>

      {error && <Banner tone="danger">{error}</Banner>}
      {ok && <Banner tone="ok">{ok}</Banner>}

      {loading ? (
        <p style={muted}>加载中…</p>
      ) : (
        <>
          {/* 当前状态 */}
          <div style={statusRow}>
            <span style={{ fontSize: 13, color: 'var(--hk-ink-700)' }}>
              当前:{settings?.enabled ? '已启用' : '已停用'}
              {settings?.enabled ? ` · ${apiSourceLabel(settings.api_source)}` : ''}
              {settings?.enabled && settings.profile_id ? ` · profile #${settings.profile_id}` : ''}
            </span>
          </div>

          {/* 启用配置表单 */}
          <div style={formBox}>
            <Row label="API 来源(api_source)">
              <select
                value={apiSource}
                onChange={(e) => {
                  const next = e.target.value as HermesAPISource
                  setApiSource(next)
                  // 切回托管时清掉 profile(托管禁绑 profile)。
                  if (next === API_SOURCE_MANAGED) setProfileId('')
                }}
                style={{ ...inp, maxWidth: 220 }}
              >
                <option value={API_SOURCE_MANAGED}>{apiSourceLabel(API_SOURCE_MANAGED)}</option>
                <option value={API_SOURCE_DEDICATED}>{apiSourceLabel(API_SOURCE_DEDICATED)}</option>
              </select>
            </Row>

            {apiSource === API_SOURCE_DEDICATED && (
              <Row label="绑定 profile(专用分组必选)">
                <select value={profileId} onChange={(e) => setProfileId(e.target.value)} style={{ ...inp, maxWidth: 320 }}>
                  <option value="">— 请选择一个专用分组 profile —</option>
                  {profiles
                    .filter((p) => p.kind === API_SOURCE_DEDICATED)
                    .map((p) => (
                      <option key={p.id} value={p.id}>
                        #{p.id} · {p.name}
                      </option>
                    ))}
                </select>
              </Row>
            )}

            <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
              <button type="button" disabled={busy} onClick={onEnable} style={primaryBtn}>
                {settings?.enabled ? '更新配置' : '启用'}
              </button>
              {settings?.enabled && (
                <button type="button" disabled={busy} onClick={onDisable} style={dangerBtn}>
                  停用
                </button>
              )}
            </div>
          </div>

          {/* api-profile 列表 + 新建 */}
          <ProfilesSection
            profiles={profiles}
            settings={settings}
            busy={busy}
            onDelete={onDeleteProfile}
            onCreate={(body) => {
              setError(null)
              setOk(null)
              setBusy(true)
              return createProfile(adminToken, auth, body)
                .then((p) => {
                  setProfiles((prev) => [...prev, p])
                  setOk(`已创建 profile #${p.id}「${p.name}」`)
                })
                .catch((e) => {
                  setError(describeErr(e))
                  throw e
                })
                .finally(() => setBusy(false))
            }}
          />
        </>
      )}
    </section>
  )
}

// ── api-profile 区 ──

function ProfilesSection({
  profiles,
  settings,
  busy,
  onDelete,
  onCreate,
}: {
  profiles: HermesProfile[]
  settings: HermesSettings | null
  busy: boolean
  onDelete: (p: HermesProfile) => void
  onCreate: (body: { name: string; kind: HermesAPISource; api_key_id?: number; pool_group_id?: number }) => Promise<void>
}) {
  const [showForm, setShowForm] = useState(false)

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h3 style={{ fontSize: 14, color: 'var(--hk-ink-700)', margin: 0 }}>API Profiles({profiles.length})</h3>
        <button type="button" style={ghostBtn} onClick={() => setShowForm((v) => !v)}>
          {showForm ? '收起' : '新建 profile'}
        </button>
      </div>

      {showForm && (
        <ProfileForm
          busy={busy}
          onSubmit={(body) =>
            onCreate(body).then(
              () => setShowForm(false),
              () => {
                /* 失败保留表单供修正 */
              },
            )
          }
        />
      )}

      {profiles.length === 0 ? (
        <p style={muted}>暂无 profile。专用分组配置前需先创建一个 kind=专用分组 的 profile。</p>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
          {profiles.map((p) => (
            <div key={p.id} style={profileRow}>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 2, minWidth: 0 }}>
                <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--hk-ink-900)' }}>
                  #{p.id} · {p.name}
                  {isProfileInUse(settings, p.id) && <span style={inUseTag}>使用中</span>}
                </span>
                <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>
                  {profileKindLabel(p.kind)}
                  {p.api_key_id ? ` · api_key #${p.api_key_id}` : ''}
                  {p.pool_group_id ? ` · pool_group #${p.pool_group_id}` : ''}
                </span>
              </div>
              <button type="button" disabled={busy} onClick={() => onDelete(p)} style={dangerGhostBtn}>
                删除
              </button>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function ProfileForm({
  busy,
  onSubmit,
}: {
  busy: boolean
  onSubmit: (body: { name: string; kind: HermesAPISource; api_key_id?: number; pool_group_id?: number }) => void
}) {
  const [name, setName] = useState('')
  const [kind, setKind] = useState<HermesAPISource>(API_SOURCE_MANAGED)
  const [apiKeyId, setApiKeyId] = useState('')
  const [poolGroupId, setPoolGroupId] = useState('')
  const [err, setErr] = useState<string | null>(null)

  const submit = () => {
    setErr(null)
    const form: ProfileForm = { name, kind, apiKeyId, poolGroupId }
    const v = validateProfile(form)
    if (!v.ok) {
      setErr(v.error)
      return
    }
    onSubmit(v.value)
  }

  return (
    <div style={formBox}>
      {err && <Banner tone="danger">{err}</Banner>}
      <Row label="名称(name)">
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="如 主力专用分组" style={inp} />
      </Row>
      <Row label="类型(kind)">
        <select
          value={kind}
          onChange={(e) => {
            const next = e.target.value as HermesAPISource
            setKind(next)
            // 切到托管时清掉 pool_group_id(托管禁设)。
            if (next === API_SOURCE_MANAGED) setPoolGroupId('')
          }}
          style={{ ...inp, maxWidth: 220 }}
        >
          <option value={API_SOURCE_MANAGED}>{apiSourceLabel(API_SOURCE_MANAGED)}</option>
          <option value={API_SOURCE_DEDICATED}>{apiSourceLabel(API_SOURCE_DEDICATED)}</option>
        </select>
      </Row>
      <Row label="api_key_id(可选,绑定一个 API Key)">
        <input value={apiKeyId} onChange={(e) => setApiKeyId(e.target.value)} placeholder="留空=不绑定" inputMode="numeric" style={{ ...inp, maxWidth: 200 }} />
      </Row>
      {kind === API_SOURCE_DEDICATED && (
        <Row label="pool_group_id(专用分组必填)">
          <input value={poolGroupId} onChange={(e) => setPoolGroupId(e.target.value)} placeholder="分组 ID" inputMode="numeric" style={{ ...inp, maxWidth: 200 }} />
        </Row>
      )}
      <div>
        <button type="button" disabled={busy} onClick={submit} style={primaryBtn}>
          {busy ? '创建中…' : '创建 profile'}
        </button>
      </div>
    </div>
  )
}

// ── 小组件 + 样式 ──

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--hk-ink-700)' }}>{label}</span>
      <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center' }}>{children}</div>
    </div>
  )
}

function Banner({ tone, children }: { tone: 'danger' | 'ok'; children: React.ReactNode }) {
  const danger = tone === 'danger'
  return (
    <div
      style={{
        padding: 'var(--hk-space-2) var(--hk-space-3)',
        borderRadius: 'var(--hk-radius-md)',
        fontSize: 13,
        color: danger ? 'var(--hk-danger)' : 'var(--hk-primary-700)',
        background: danger ? 'var(--hk-danger-soft)' : 'var(--hk-primary-50, #eef7f2)',
        border: `1px solid ${danger ? 'var(--hk-danger-soft)' : 'var(--hk-line)'}`,
      }}
    >
      {children}
    </div>
  )
}

function describeErr(e: unknown): string {
  if (e instanceof ApiError) {
    // 删 profile 的 409 后端返回非标准 {error:"profile_in_use"} 信封,lib/api 取不到 code(回退 http_409),
    // 这里按 status 显式给中文文案,避免只显示 http_409。
    if (e.status === 409) return '该 profile 正被某条 Hermes 配置引用,无法删除;请先把配置改用其它来源再删。'
    return `${e.message}(${e.code})`
  }
  if (e instanceof Error) return e.message
  return '操作失败'
}

const card: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-4)', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)' }
const h2: React.CSSProperties = { fontSize: 15, color: 'var(--hk-ink-700)', margin: 0 }
const muted: React.CSSProperties = { fontSize: 13, color: 'var(--hk-ink-500)', margin: 0 }
const statusRow: React.CSSProperties = { padding: 'var(--hk-space-2) var(--hk-space-3)', background: 'var(--hk-bg, #f7f8fa)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)' }
const formBox: React.CSSProperties = { display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)' }
const profileRow: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--hk-space-2)', padding: 'var(--hk-space-2) var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)' }
const inUseTag: React.CSSProperties = { marginLeft: 8, fontSize: 10, fontWeight: 600, color: 'var(--hk-primary-700)', padding: '1px 6px', border: '1px solid var(--hk-line)', borderRadius: 999 }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', flex: 1, minWidth: 120 }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer', flexShrink: 0 }
const dangerBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid #c0392b', borderRadius: 'var(--hk-radius-md)', background: '#c0392b', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer', flexShrink: 0 }
const dangerGhostBtn: React.CSSProperties = { height: 28, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-danger-soft)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-danger)', fontSize: 12, cursor: 'pointer', flexShrink: 0 }
const ghostBtn: React.CSSProperties = { height: 28, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 12, cursor: 'pointer' }
