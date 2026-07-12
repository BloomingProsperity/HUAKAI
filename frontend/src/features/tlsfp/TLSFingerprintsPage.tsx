import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import {
  createProfile,
  deleteProfile,
  listProfiles,
  setProfileStatus,
  updateProfile,
} from './api'
import {
  nextStatus,
  profileToForm,
  statusLabel,
  statusTone,
  toCreateRequest,
  validateForm,
} from './tlsfp'
import { EMPTY_FORM, type ProfileForm, type TLSFingerprintProfile } from './types'

/*
 * TLS 指纹 profile(出口拟真)管理台。管线「路由与池」下的出口拟真配置。
 * 后端 /v1/admin/tls-fingerprint-profiles(platform_admin token):
 *   - 列表 / 新建 / 全字段更新 / 改状态(active↔disabled)/ 软删除
 *   - profile 描述 TLS ClientHello 的各项码点(加密套件/曲线/扩展顺序/ALPN/GREASE 等),
 *     是网关出口对上游伪装真实客户端指纹的基线;drift worker 会比对实际 JA3 并标记漂移。
 * 注意:platform_admin 角色下后端要求 tenant_id 必填(handler.go:101),故本页先要租户 ID。
 * 账号侧「把某账号绑定到某 profile」属 accounts 目录(PATCH provider-accounts/{id}/fingerprint-profile),
 * 本页不涉及。本页不碰任何 pool/registry/gateway 等碰撞包模块。
 */

export function TLSFingerprintsPage() {
  const [tenantInput, setTenantInput] = useState('')
  const [tenantId, setTenantId] = useState<number | null>(null)

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>TLS 指纹 Profile</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          出口拟真:管理网关向上游伪装的 TLS ClientHello 指纹基线(加密套件 / 曲线 / 扩展顺序 / ALPN / GREASE)。
          先指定租户 ID。
        </p>
      </header>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          const v = Number(tenantInput.trim())
          setTenantId(Number.isInteger(v) && v > 0 ? v : null)
        }}
        style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)' }}
      >
        <Field label="租户 ID(tenant_id)">
          <input
            value={tenantInput}
            onChange={(e) => setTenantInput(e.target.value)}
            inputMode="numeric"
            placeholder="如 1"
            style={{ ...inp, width: 160 }}
          />
        </Field>
        <button type="submit" style={primaryBtn}>
          加载
        </button>
      </form>

      {tenantId == null ? (
        <Empty>请输入正整数租户 ID 后点击「加载」。</Empty>
      ) : (
        <ProfileManager key={tenantId} tenantId={tenantId} />
      )}
    </div>
  )
}

function ProfileManager({ tenantId }: { tenantId: number }) {
  const [rows, setRows] = useState<TLSFingerprintProfile[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [busy, setBusy] = useState<number | null>(null) // 行级忙(状态切换/删除中的 profile id;-1 表示新建中)
  // 编辑器态:null=未打开;{id:null}=新建;{id:number}=编辑某 profile。
  const [editor, setEditor] = useState<{ id: number | null; form: ProfileForm } | null>(null)

  const load = useCallback(
    (signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      listProfiles(tenantId, signal)
        .then((resp) => setRows(resp.items ?? []))
        .catch((e: unknown) => {
          if (signal?.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载 profile 失败')
        })
        .finally(() => {
          if (!signal?.aborted) setLoading(false)
        })
    },
    [tenantId],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  // 行级改动型动作的统一执行壳:置忙、清提示、跑、刷新、报错。
  const runRow = async (id: number, fn: () => Promise<unknown>, okMsg: string) => {
    setBusy(id)
    setError(null)
    setNotice(null)
    try {
      await fn()
      setNotice(okMsg)
      load()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '操作失败')
    } finally {
      setBusy(null)
    }
  }

  // 提交编辑器(新建或更新)。校验在 EditorPanel 内已先做并传出内容体。
  const submitEditor = async (content: ReturnType<typeof validateForm>) => {
    if (!editor || !content.ok) return
    const id = editor.id
    setBusy(-1)
    setError(null)
    setNotice(null)
    try {
      if (id == null) {
        await createProfile(toCreateRequest(tenantId, content.value))
        setNotice('已新建 profile。')
      } else {
        await updateProfile(id, tenantId, content.value)
        setNotice(`已更新 profile #${id}。`)
      }
      setEditor(null)
      load()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '保存失败')
    } finally {
      setBusy(null)
    }
  }

  const toggleStatus = (p: TLSFingerprintProfile) => {
    const target = nextStatus(p.status)
    const verb = target === 'disabled' ? '停用' : '启用'
    // 改动型运维动作:二次确认(停用会让该 profile 不再被出口拟真选用)。
    if (!window.confirm(`确认${verb} profile「${p.name}」(#${p.id})?`)) return
    void runRow(p.id, () => setProfileStatus(p.id, tenantId, target), `已${verb} #${p.id}`)
  }

  const removeProfile = (p: TLSFingerprintProfile) => {
    // 破坏性动作:软删除,二次确认。
    if (!window.confirm(`删除 profile「${p.name}」(#${p.id})?此操作不可在本页撤销。`)) return
    void runRow(p.id, () => deleteProfile(p.id, tenantId), `已删除 #${p.id}`)
  }

  return (
    <section style={card}>
      <div style={cardHead}>
        <h2 style={{ fontSize: 15, margin: 0 }}>指纹 Profile(共 {rows.length} 个)</h2>
        <button
          type="button"
          disabled={busy !== null}
          onClick={() => setEditor({ id: null, form: { ...EMPTY_FORM } })}
          style={primaryBtn}
        >
          新建 Profile
        </button>
      </div>

      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      {editor && (
        <EditorPanel
          mode={editor.id == null ? 'create' : 'edit'}
          form={editor.form}
          busy={busy === -1}
          onChange={(form) => setEditor((e) => (e ? { ...e, form } : e))}
          onCancel={() => setEditor(null)}
          onSubmit={submitEditor}
          onError={(msg) => setError(msg)}
        />
      )}

      {loading && rows.length === 0 ? (
        <Empty>加载中…</Empty>
      ) : rows.length === 0 ? (
        <Empty>该租户暂无 TLS 指纹 profile。点「新建 Profile」创建。</Empty>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr>
                {['名称', '状态', 'GREASE', '套件数', 'ALPN', 'JA3 基线', '最近校验', ''].map((h) => (
                  <th key={h} style={th}>
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((p) => (
                <tr key={p.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                  <td style={td}>
                    <div style={{ fontWeight: 600, color: 'var(--hk-ink-900)' }}>{p.name}</div>
                    {p.description && (
                      <div style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>{p.description}</div>
                    )}
                  </td>
                  <td style={td}>
                    <StatusBadge tone={statusTone(p.status)}>{statusLabel(p.status)}</StatusBadge>
                  </td>
                  <td style={tdMono}>{p.grease_enabled ? '开' : '关'}</td>
                  <td style={tdMono}>{(p.cipher_suites ?? []).length}</td>
                  <td style={tdMono}>{(p.alpn_protocols ?? []).join(', ') || '—'}</td>
                  <td style={{ ...tdMono, color: 'var(--hk-ink-300)' }}>{short(p.expected_ja3_hash)}</td>
                  <td style={tdMono}>{fmt(p.last_validated_at)}</td>
                  <td style={{ ...td, textAlign: 'right', whiteSpace: 'nowrap' }}>
                    <button
                      type="button"
                      disabled={busy !== null}
                      onClick={() => setEditor({ id: p.id, form: profileToForm(p) })}
                      style={linkBtn}
                    >
                      编辑
                    </button>
                    <button
                      type="button"
                      disabled={busy !== null}
                      onClick={() => toggleStatus(p)}
                      style={linkBtn}
                    >
                      {busy === p.id ? '处理中…' : nextStatus(p.status) === 'disabled' ? '停用' : '启用'}
                    </button>
                    <button
                      type="button"
                      disabled={busy !== null}
                      onClick={() => removeProfile(p)}
                      style={dangerLink}
                    >
                      删除
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

/* ——— 新建 / 编辑面板 ——— */
function EditorPanel({
  mode,
  form,
  busy,
  onChange,
  onCancel,
  onSubmit,
  onError,
}: {
  mode: 'create' | 'edit'
  form: ProfileForm
  busy: boolean
  onChange: (form: ProfileForm) => void
  onCancel: () => void
  onSubmit: (content: ReturnType<typeof validateForm>) => void
  onError: (msg: string) => void
}) {
  const setF = <K extends keyof ProfileForm>(k: K, v: ProfileForm[K]) => onChange({ ...form, [k]: v })

  const submit = () => {
    const v = validateForm(form)
    if (!v.ok) {
      onError(v.error)
      return
    }
    onSubmit(v)
  }

  return (
    <div style={{ padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)', background: 'var(--hk-surface-sunken)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--hk-ink-700)' }}>
        {mode === 'create' ? '新建指纹 Profile' : '编辑指纹 Profile'}
      </div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(240px, 1fr))', gap: 'var(--hk-space-3)' }}>
        <Field label="名称(必填)">
          <input value={form.name} onChange={(e) => setF('name', e.target.value)} placeholder="如 chrome-131" style={inp} />
        </Field>
        <Field label="描述(可选)">
          <input value={form.description} onChange={(e) => setF('description', e.target.value)} placeholder="如 拟真 Chrome 131 桌面" style={inp} />
        </Field>
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: 'var(--hk-ink-700)', alignSelf: 'flex-end', height: 32 }}>
          <input type="checkbox" checked={form.greaseEnabled} onChange={(e) => setF('greaseEnabled', e.target.checked)} />
          启用 GREASE
        </label>
        <ListField label="加密套件(逗号/空白分隔的码点)" value={form.cipherSuites} onChange={(v) => setF('cipherSuites', v)} placeholder="如 4865, 4866, 4867" />
        <ListField label="支持曲线(supported_groups)" value={form.supportedCurves} onChange={(v) => setF('supportedCurves', v)} placeholder="如 29, 23, 24" />
        <ListField label="EC 点格式" value={form.ecPointFormats} onChange={(v) => setF('ecPointFormats', v)} placeholder="如 0" />
        <ListField label="签名算法" value={form.signatureAlgorithms} onChange={(v) => setF('signatureAlgorithms', v)} placeholder="如 1027, 2052, 1025" />
        <ListField label="ALPN 协议(字符串)" value={form.alpnProtocols} onChange={(v) => setF('alpnProtocols', v)} placeholder="如 h2, http/1.1" />
        <ListField label="TLS 版本(supported_versions)" value={form.tlsSupportedVersions} onChange={(v) => setF('tlsSupportedVersions', v)} placeholder="如 772, 771" />
        <ListField label="key_share 分组" value={form.keyShareGroups} onChange={(v) => setF('keyShareGroups', v)} placeholder="如 29, 23" />
        <ListField label="PSK 模式" value={form.pskModes} onChange={(v) => setF('pskModes', v)} placeholder="如 1" />
        <ListField label="扩展顺序(extensions_order)" value={form.extensionsOrder} onChange={(v) => setF('extensionsOrder', v)} placeholder="如 0, 23, 65281, 10" />
        <Field label="期望 JA3 哈希(可选,32 位 hex)">
          <input value={form.expectedJa3Hash} onChange={(e) => setF('expectedJa3Hash', e.target.value)} placeholder="留空=不设漂移基线" style={{ ...inp, fontFamily: 'var(--hk-font-mono)' }} />
        </Field>
      </div>
      <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
        <button type="button" disabled={busy} onClick={submit} style={primaryBtn}>
          {busy ? '保存中…' : mode === 'create' ? '创建' : '保存'}
        </button>
        <button type="button" disabled={busy} onClick={onCancel} style={ghostBtn}>
          取消
        </button>
      </div>
    </div>
  )
}

/* ——— 本页私有小组件 / 样式 ——— */
function ListField({ label, value, onChange, placeholder }: { label: string; value: string; onChange: (v: string) => void; placeholder?: string }) {
  return (
    <Field label={label}>
      <input value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} style={{ ...inp, fontFamily: 'var(--hk-font-mono)' }} />
    </Field>
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

function Banner({ kind, children }: { kind: 'error' | 'ok'; children: React.ReactNode }) {
  const palette =
    kind === 'error'
      ? { color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
      : { color: '#0b6553', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
  return (
    <div style={{ margin: 'var(--hk-space-4)', marginBottom: 0, padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>
      {children}
    </div>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

function short(s: string): string {
  if (!s) return '—'
  return s.length > 14 ? `${s.slice(0, 8)}…${s.slice(-4)}` : s
}
function fmt(iso?: string | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN', { hour12: false })
}

const card: React.CSSProperties = { background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }
const cardHead: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)', background: 'var(--hk-surface-sunken)' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
const tdMono: React.CSSProperties = { ...td, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const linkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-600)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
const dangerLink: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-danger)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
