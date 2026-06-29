import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { listDisputes, resolveDispute } from './api'
import {
  DEFAULT_PAGE_SIZE,
  isResolvable,
  shortDisputeID,
  shortRequestID,
  statusLabel,
  statusTone,
  validateResolve,
} from './disputes'
import {
  DISPUTE_STATUSES,
  EMPTY_DISPUTE_FILTERS,
  type DisputeFilters,
  type DisputeStatus,
  type DisputeView,
} from './types'

/*
 * 退款/扣费争议台(money 敏感)。计费运营(stage5)下,运营对用户发起的费用争议做人工裁决。
 * 后端 /v1/admin/disputes(admin token,dispute_handler.go):
 *   - 列表:按 tenant_id(platform_admin 必填)+ status 过滤、分页(GET /v1/admin/disputes)
 *   - 裁决:POST /v1/admin/disputes/{id}/resolve {tenant_id, status, operator_note}
 * 裁决是对一笔已计费请求的退款/维持决断,属破坏性/改动型动作 → window.confirm 二次确认。
 * 本页不碰任何 pool/registry/gateway 等碰撞包模块。
 */

const PAGE_SIZE = DEFAULT_PAGE_SIZE

export function DisputesAdminPage() {
  const [tenantInput, setTenantInput] = useState('')
  const [tenantId, setTenantId] = useState<number | null>(null)

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>退款/扣费争议</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          计费运营:用户对某笔已计费请求发起的费用争议。运营可裁决「支持退款」或「驳回维持扣费」。
          裁决会改动该争议状态,属 money 敏感动作,提交前需二次确认。先指定租户 ID。
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
        <DisputesCard tenantId={tenantId} />
      )}
    </div>
  )
}

function DisputesCard({ tenantId }: { tenantId: number }) {
  const [rows, setRows] = useState<DisputeView[]>([])
  const [offset, setOffset] = useState(0)
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [draft, setDraft] = useState<DisputeFilters>(EMPTY_DISPUTE_FILTERS)
  const [filters, setFilters] = useState<DisputeFilters>(EMPTY_DISPUTE_FILTERS)

  const fetchPage = useCallback(
    async (off: number, append: boolean, signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      try {
        const resp = await listDisputes(tenantId, filters, PAGE_SIZE, off, signal)
        const items = resp.disputes ?? []
        setRows((prev) => (append ? [...prev, ...items] : items))
        setOffset(off + items.length)
        setHasMore(items.length === PAGE_SIZE)
      } catch (e) {
        if (signal?.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载争议列表失败')
      } finally {
        if (!signal?.aborted) setLoading(false)
      }
    },
    [tenantId, filters],
  )

  // filters / tenantId 变更:从头加载。
  useEffect(() => {
    const ctrl = new AbortController()
    setRows([])
    setOffset(0)
    void fetchPage(0, false, ctrl.signal)
    return () => ctrl.abort()
  }, [fetchPage])

  const reload = () => {
    setNotice(null)
    void fetchPage(0, false)
  }

  return (
    <section style={card}>
      <div style={cardHead}>
        <h2 style={{ fontSize: 15, margin: 0 }}>争议列表</h2>
        <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>已载 {rows.length} 条</span>
      </div>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          setFilters(draft)
        }}
        style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)' }}
      >
        <Field label="按状态过滤(可选)">
          <select
            value={draft.status}
            onChange={(e) => setDraft({ status: e.target.value as DisputeFilters['status'] })}
            style={{ ...inp, width: 200 }}
          >
            <option value="">全部状态</option>
            {DISPUTE_STATUSES.map((s) => (
              <option key={s} value={s}>
                {statusLabel(s)}
              </option>
            ))}
          </select>
        </Field>
        <button type="submit" style={primaryBtn}>
          查询
        </button>
        <button
          type="button"
          onClick={() => {
            setDraft(EMPTY_DISPUTE_FILTERS)
            setFilters(EMPTY_DISPUTE_FILTERS)
          }}
          style={ghostBtn}
        >
          重置
        </button>
      </form>

      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      {loading && rows.length === 0 ? (
        <Empty>加载中…</Empty>
      ) : rows.length === 0 ? (
        <Empty>该租户暂无争议记录。</Empty>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr>
                {['争议 ID', '状态', '用户', 'request_id', '原因', '运营备注', '创建', '裁决', ''].map((h) => (
                  <th key={h} style={th}>
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <DisputeRow
                  key={row.id}
                  row={row}
                  tenantId={tenantId}
                  onResolved={(msg) => {
                    setNotice(msg)
                    reload()
                  }}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}

      {hasMore && (
        <div style={{ padding: 'var(--hk-space-4)', display: 'flex', justifyContent: 'center' }}>
          <button type="button" disabled={loading} onClick={() => void fetchPage(offset, true)} style={ghostBtn}>
            {loading ? '加载中…' : '加载更多'}
          </button>
        </div>
      )}
    </section>
  )
}

function DisputeRow({
  row,
  tenantId,
  onResolved,
}: {
  row: DisputeView
  tenantId: number
  onResolved: (msg: string) => void
}) {
  const [open, setOpen] = useState(false)
  // 裁决=落定终态,只在 resolved(支持退款)/rejected(维持扣费)二选一;
  // money 安全默认 rejected(不动钱、维持现状),要退款须运营显式切换 + 二次确认,避免默认就退款。
  const [status, setStatus] = useState<DisputeStatus>('rejected')
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [rowError, setRowError] = useState<string | null>(null)
  const resolvable = isResolvable(row.status)

  const submit = () => {
    const v = validateResolve(tenantId, status, note)
    if (!v.ok) {
      setRowError(v.error)
      return
    }
    // money 敏感:裁决会落定该笔费用争议的退款/维持结论,二次确认。
    const verb = status === 'resolved' ? '支持退款' : status === 'rejected' ? '驳回(维持扣费)' : statusLabel(status)
    if (!window.confirm(`确认将争议「${row.dispute_id}」裁决为「${verb}」?该动作影响一笔已计费请求的费用结论。`)) {
      return
    }
    setBusy(true)
    setRowError(null)
    resolveDispute(row.id, v.value)
      .then((resp) => onResolved(`已裁决 ${resp.dispute.dispute_id} → ${statusLabel(resp.dispute.status)}`))
      .catch((e: unknown) => setRowError(e instanceof ApiError ? `${e.message}(${e.code})` : '裁决失败'))
      .finally(() => setBusy(false))
  }

  return (
    <>
      <tr style={{ borderTop: '1px solid var(--hk-line)' }}>
        <td style={tdMono} title={row.dispute_id}>{shortDisputeID(row.dispute_id)}</td>
        <td style={td}>
          <StatusBadge tone={statusTone(row.status)}>{statusLabel(row.status)}</StatusBadge>
        </td>
        <td style={tdMono}>#{row.user_id}</td>
        <td style={tdMono} title={row.request_id}>{shortRequestID(row.request_id)}</td>
        <td style={{ ...td, maxWidth: 260, whiteSpace: 'normal', color: 'var(--hk-ink-700)' }}>{row.reason || '—'}</td>
        <td style={{ ...td, maxWidth: 220, whiteSpace: 'normal', color: 'var(--hk-ink-500)' }}>{row.operator_note || '—'}</td>
        <td style={tdMono}>{fmt(row.created_at)}</td>
        <td style={tdMono}>{fmt(row.resolved_at)}</td>
        <td style={{ ...td, textAlign: 'right' }}>
          {resolvable ? (
            <button type="button" onClick={() => setOpen((o) => !o)} style={ghostBtn}>
              {open ? '收起' : '裁决'}
            </button>
          ) : (
            <span style={{ fontSize: 12, color: 'var(--hk-ink-300)' }}>已终态</span>
          )}
        </td>
      </tr>
      {open && resolvable && (
        <tr style={{ background: 'var(--hk-surface-sunken)' }}>
          <td colSpan={9} style={{ padding: 'var(--hk-space-4)' }}>
            {rowError && <Banner kind="error">{rowError}</Banner>}
            <div style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', flexWrap: 'wrap' }}>
              <Field label="裁决结果">
                {/* 裁决只列终态:rejected(维持扣费)/ resolved(支持退款);非终态不作为裁决目标。 */}
                <select value={status} onChange={(e) => setStatus(e.target.value as DisputeStatus)} style={{ ...inp, width: 220 }}>
                  {(['rejected', 'resolved'] as DisputeStatus[]).map((s) => (
                    <option key={s} value={s}>
                      {statusLabel(s)}
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="运营备注(operator_note,可选,≤4000)">
                <input
                  value={note}
                  onChange={(e) => setNote(e.target.value)}
                  placeholder="供审计:裁决依据"
                  style={{ ...inp, width: 360 }}
                />
              </Field>
              <button type="button" disabled={busy} onClick={submit} style={primaryBtn}>
                {busy ? '提交中…' : '提交裁决'}
              </button>
            </div>
          </td>
        </tr>
      )}
    </>
  )
}

/* ——— 小工具组件 / 样式(本页私有) ——— */

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
      ? { color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }
      : { color: '#0b6553', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
  return (
    <div style={{ margin: 'var(--hk-space-4)', marginBottom: 'var(--hk-space-2)', padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>
      {children}
    </div>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

function fmt(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN', { hour12: false })
}

const card: React.CSSProperties = { background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }
const cardHead: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)', background: 'var(--hk-surface-sunken)' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'top' }
const tdMono: React.CSSProperties = { ...td, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
