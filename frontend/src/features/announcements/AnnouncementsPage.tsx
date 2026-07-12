import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge, type BadgeTone } from '../../ui/StatusBadge'
import { createAnnouncement, deleteAnnouncement, listAnnouncements, updateAnnouncement } from './api'
import {
  buildCreate,
  buildUpdate,
  DEFAULT_TENANT_ID,
  displayState,
  displayStateLabel,
  displayStateTone,
  EMPTY_ANNOUNCEMENT_FORM,
  SEVERITIES,
  severityLabel,
  severityTone,
  toggleActiveTarget,
  type AnnouncementForm,
} from './announcements'
import type { Announcement } from './types'

/*
 * 公告管理(运营台)。/v1/admin/announcements 列表(级别/生效态徽章)+ 客户端筛选(级别/启停)
 * + 新建/编辑(标题/正文/级别/生效起止/启停)+ 行内启停 + 删除(二次确认)。
 * 真码端点:backend/internal/announcementhttp/handlers.go:122(MountAdminRoutes)。
 * 多租户:管理端点需 tenant_id;单租户部署默认 1,顶栏可改。
 */
export function AnnouncementsPage() {
  const [tenantId, setTenantId] = useState(DEFAULT_TENANT_ID)
  const [items, setItems] = useState<Announcement[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [filterSeverity, setFilterSeverity] = useState('')
  const [filterActive, setFilterActive] = useState('')
  const [editing, setEditing] = useState<Announcement | 'new' | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)

  const load = useCallback(
    (signal: AbortSignal) => {
      setLoading(true)
      setError(null)
      listAnnouncements(tenantId, 100, 0, signal)
        .then((resp) => setItems(resp.items))
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载公告列表失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [tenantId, refreshNonce],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  const refresh = () => setRefreshNonce((n) => n + 1)

  const act = async (id: number, fn: () => Promise<unknown>) => {
    setBusyId(id)
    setError(null)
    try {
      await fn()
      refresh()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '操作失败')
    } finally {
      setBusyId(null)
    }
  }

  const onToggle = (a: Announcement) =>
    act(a.id, () => updateAnnouncement(tenantId, a.id, { active: toggleActiveTarget(a.active) }))

  const onDelete = (a: Announcement) => {
    // 删除二次确认(浏览器原生),避免误删生效公告。
    if (!window.confirm(`确认删除公告「${a.title}」?此操作不可撤销。`)) return
    void act(a.id, () => deleteAnnouncement(tenantId, a.id))
  }

  const filtered = items.filter((a) => {
    if (filterSeverity && a.severity !== filterSeverity) return false
    if (filterActive === 'active' && !a.active) return false
    if (filterActive === 'inactive' && a.active) return false
    return true
  })

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>公告管理</h1>
          <p className="hk-sub">
            运营台 · 站内公告(级别/生效起止/启停)。当前 {filtered.length} / 共 {items.length} 条。
          </p>
        </div>
        <button type="button" onClick={() => setEditing('new')} className="hk-btn hk-btn--green">
          ＋ 新建公告
        </button>
      </header>

      <form
        onSubmit={(e) => e.preventDefault()}
        style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-3)', alignItems: 'flex-end', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)' }}
      >
        <Field label="租户 ID">
          <input
            value={tenantId}
            inputMode="numeric"
            onChange={(e) => {
              const v = Number.parseInt(e.target.value, 10)
              setTenantId(Number.isInteger(v) && v > 0 ? v : DEFAULT_TENANT_ID)
            }}
            style={{ ...inp, width: 96 }}
          />
        </Field>
        <Field label="级别">
          <select value={filterSeverity} onChange={(e) => setFilterSeverity(e.target.value)} style={inp}>
            <option value="">全部</option>
            {SEVERITIES.map((s) => (
              <option key={s.value} value={s.value}>
                {s.label}
              </option>
            ))}
          </select>
        </Field>
        <Field label="启停">
          <select value={filterActive} onChange={(e) => setFilterActive(e.target.value)} style={inp}>
            <option value="">全部</option>
            <option value="active">已启用</option>
            <option value="inactive">已停用</option>
          </select>
        </Field>
        <button type="button" onClick={refresh} className="hk-btn">
          刷新
        </button>
      </form>

      {error && <div style={errBox}>{error}</div>}

      <div className="hk-card">
        {loading && items.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : filtered.length === 0 ? (
          <Empty>没有匹配的公告。</Empty>
        ) : (
          <div className="hk-tablewrap">
            <table className="hk-table">
              <thead>
                <tr>
                  {['标题', '级别', '状态', '生效时间', '过期时间', ''].map((h) => (
                    <th key={h}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {filtered.map((a) => {
                  const ds = displayState(a)
                  return (
                    <tr key={a.id}>
                      <td>
                        <div style={{ display: 'flex', flexDirection: 'column' }}>
                          <span style={{ fontWeight: 600, color: 'var(--hk-ink-900)' }}>{a.title}</span>
                          <span style={{ fontSize: 11, color: 'var(--hk-ink-300)', maxWidth: 360, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                            {a.body}
                          </span>
                        </div>
                      </td>
                      <td>
                        <StatusBadge tone={severityTone(a.severity) as BadgeTone}>{severityLabel(a.severity)}</StatusBadge>
                      </td>
                      <td>
                        <StatusBadge tone={displayStateTone(ds) as BadgeTone}>{displayStateLabel(ds)}</StatusBadge>
                      </td>
                      <td className="hk-mono">{fmt(a.published_at)}</td>
                      <td className="hk-mono">{a.expires_at ? fmt(a.expires_at) : '—'}</td>
                      <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                        <button type="button" disabled={busyId === a.id} onClick={() => setEditing(a)} style={linkBtn}>
                          编辑
                        </button>
                        <button type="button" disabled={busyId === a.id} onClick={() => onToggle(a)} style={linkBtn}>
                          {a.active ? '停用' : '启用'}
                        </button>
                        <button type="button" disabled={busyId === a.id} onClick={() => onDelete(a)} style={dangerLinkBtn}>
                          删除
                        </button>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {editing && (
        <AnnouncementModal
          tenantId={tenantId}
          existing={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            refresh()
          }}
        />
      )}
    </div>
  )
}

function AnnouncementModal({
  tenantId,
  existing,
  onClose,
  onSaved,
}: {
  tenantId: number
  existing: Announcement | null
  onClose: () => void
  onSaved: () => void
}) {
  const [form, setForm] = useState<AnnouncementForm>(() => toForm(existing))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const set = <K extends keyof AnnouncementForm>(k: K, v: AnnouncementForm[K]) => setForm((f) => ({ ...f, [k]: v }))

  const submit = async () => {
    setError(null)
    try {
      // 新建/编辑分别构造,各自校验;'error' 形态先挡住给中文提示,不发请求。
      if (existing) {
        const built = buildUpdate(form)
        if ('error' in built) {
          setError(built.error)
          return
        }
        setBusy(true)
        await updateAnnouncement(tenantId, existing.id, built)
      } else {
        const built = buildCreate(form, tenantId)
        if ('error' in built) {
          setError(built.error)
          return
        }
        setBusy(true)
        await createAnnouncement(built)
      }
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
        <h2 style={{ fontSize: 18 }}>{existing ? '编辑公告' : '新建公告'}</h2>
        <Field label="标题">
          <input value={form.title} onChange={(e) => set('title', e.target.value)} style={inp} />
        </Field>
        <Field label="正文">
          <textarea value={form.body} onChange={(e) => set('body', e.target.value)} rows={4} style={{ ...inp, height: 'auto', padding: 'var(--hk-space-2) var(--hk-space-3)', resize: 'vertical' }} />
        </Field>
        <div style={{ display: 'flex', gap: 'var(--hk-space-3)' }}>
          <Field label="级别">
            <select value={form.severity} onChange={(e) => set('severity', e.target.value as AnnouncementForm['severity'])} style={inp}>
              {SEVERITIES.map((s) => (
                <option key={s.value} value={s.value}>
                  {s.label}
                </option>
              ))}
            </select>
          </Field>
          <Field label="启用">
            <select value={form.active ? '1' : '0'} onChange={(e) => set('active', e.target.value === '1')} style={inp}>
              <option value="1">启用</option>
              <option value="0">停用</option>
            </select>
          </Field>
        </div>
        <div style={{ display: 'flex', gap: 'var(--hk-space-3)' }}>
          <Field label="生效时间(可选,留空=立即)">
            <input type="datetime-local" value={form.publishedAt} onChange={(e) => set('publishedAt', e.target.value)} style={inp} />
          </Field>
          <Field label="过期时间(可选,须晚于生效)">
            <input type="datetime-local" value={form.expiresAt} onChange={(e) => set('expiresAt', e.target.value)} style={inp} />
          </Field>
        </div>
        {error && <div style={errBox}>{error}</div>}
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', justifyContent: 'flex-end' }}>
          <button type="button" onClick={onClose} className="hk-btn">
            取消
          </button>
          <button type="button" disabled={busy} onClick={submit} className="hk-btn hk-btn--green">
            {busy ? '保存中…' : '保存'}
          </button>
        </div>
      </div>
    </div>
  )
}

/** 把已有公告(RFC3339 UTC)回填成表单(datetime-local 本地串)。新建则用空表单。 */
function toForm(a: Announcement | null): AnnouncementForm {
  if (!a) return EMPTY_ANNOUNCEMENT_FORM
  return {
    title: a.title,
    body: a.body,
    severity: (SEVERITIES.find((s) => s.value === a.severity)?.value ?? 'info'),
    active: a.active,
    publishedAt: toLocalInput(a.published_at),
    expiresAt: a.expires_at ? toLocalInput(a.expires_at) : '',
  }
}

/** RFC3339 → datetime-local 输入值(本地时区,去秒)。非法则空串。 */
function toLocalInput(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function fmt(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN', { hour12: false })
}
function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)', flex: 1 }}>
      {label}
      {children}
    </label>
  )
}
function Empty({ children }: { children: React.ReactNode }) {
  return <div className="hk-empty">{children}</div>
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const linkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
const dangerLinkBtn: React.CSSProperties = { ...linkBtn, color: 'var(--hk-danger)' }
const errBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
const overlay: React.CSSProperties = { position: 'fixed', inset: 0, background: 'rgba(28,38,34,0.4)', display: 'flex', alignItems: 'flex-start', justifyContent: 'center', padding: 'var(--hk-space-6)', zIndex: 'var(--hk-z-overlay)' as unknown as number, overflowY: 'auto' }
const modal: React.CSSProperties = { width: 'min(560px,100%)', background: 'var(--hk-surface)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-3)', padding: 'var(--hk-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }
