import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { confirmIrreversible } from '../../ui/confirmDanger'
import { listDisputes, resolveDispute } from './api'
import {
  DEFAULT_PAGE_SIZE,
  mapDisputeTableRows,
  statusLabel,
  statusTone,
  validateResolve,
  type DisputeTableRow,
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
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>退款/扣费争议</h1>
          <p className="hk-sub">
            计费运营:用户对某笔已计费请求发起的费用争议。运营可裁决「支持退款」或「驳回维持扣费」。
            裁决会改动该争议状态,属 money 敏感动作,提交前需二次确认。先指定租户 ID。
          </p>
        </div>
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
        <button type="submit" className="hk-btn hk-btn--green">
          加载
        </button>
      </form>

      {tenantId == null ? (
        <EmptyState title="尚未加载争议" hint="请输入正整数租户 ID 后点击「加载」。" />
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
  const [openId, setOpenId] = useState<number | null>(null)

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
  const tableRows = mapDisputeTableRows(rows)
  const openRow = tableRows.find((row) => row.id === openId)

  return (
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>争议列表</h3>
        <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--hk-ink-300)' }}>已载 {rows.length} 条</span>
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
        <button type="submit" className="hk-btn hk-btn--green">
          查询
        </button>
        <button
          type="button"
          onClick={() => {
            setDraft(EMPTY_DISPUTE_FILTERS)
            setFilters(EMPTY_DISPUTE_FILTERS)
          }}
          className="hk-btn"
        >
          重置
        </button>
      </form>

      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      {loading && rows.length === 0 ? (
        <EmptyState title="正在加载争议记录" hint="请稍候。" />
      ) : rows.length === 0 ? (
        <EmptyState title="该租户暂无争议记录" hint="当前筛选范围内没有需要查看的费用争议。" tone="positive" />
      ) : (
        <>
          <DataListTable
            label="争议列表"
            rows={tableRows}
            rowKey={(row) => row.id}
            columns={disputeColumns}
            actions={[{
              label: (row) => row.resolvable ? (openId === row.id ? '收起' : '裁决') : '已终态',
              disabled: (row) => !row.resolvable,
              onClick: (row) => setOpenId((current) => current === row.id ? null : row.id),
            }]}
          />
          {openRow?.resolvable && (
            <DisputeResolutionPanel
              key={openRow.id}
              row={openRow.source}
              tenantId={tenantId}
              onResolved={(msg) => {
                setNotice(msg)
                setOpenId(null)
                reload()
              }}
            />
          )}
        </>
      )}

      {hasMore && (
        <div className="hk-loadmore" onClick={() => { if (!loading) void fetchPage(offset, true) }}>
          {loading ? '加载中…' : '加载更多'}
        </div>
      )}
    </section>
  )
}

function DisputeResolutionPanel({
  row,
  tenantId,
  onResolved,
}: {
  row: DisputeView
  tenantId: number
  onResolved: (msg: string) => void
}) {
  // 裁决=落定终态,只在 resolved(支持退款)/rejected(维持扣费)二选一;
  // money 安全默认 rejected(不动钱、维持现状),要退款须运营显式切换 + 二次确认,避免默认就退款。
  const [status, setStatus] = useState<DisputeStatus>('rejected')
  const [note, setNote] = useState('')
  const [busy, setBusy] = useState(false)
  const [rowError, setRowError] = useState<string | null>(null)
  const submit = () => {
    const v = validateResolve(tenantId, status, note)
    if (!v.ok) {
      setRowError(v.error)
      return
    }
    // money 敏感:裁决落定该笔费用争议的退款/维持结论(不可逆),二次确认并明示无法撤销。
    const verb = status === 'resolved' ? '支持退款' : status === 'rejected' ? '驳回(维持扣费)' : statusLabel(status)
    if (
      !confirmIrreversible(
        `将争议「${row.dispute_id}」裁决为「${verb}」`,
        '裁决为终态,将落定这笔已计费请求的费用结论。',
      )
    ) {
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
    <div style={{ padding: 'var(--hk-space-4)', borderTop: '1px solid var(--hk-line)', background: 'var(--hk-surface-sunken)' }}>
      {rowError && <Banner kind="error">{rowError}</Banner>}
      <div style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', flexWrap: 'wrap' }}>
        <Field label="裁决结果">
          {/* 裁决只列终态:rejected(维持扣费)/ resolved(支持退款);非终态不作为裁决目标。 */}
          <select value={status} onChange={(e) => setStatus(e.target.value as DisputeStatus)} style={{ ...inp, width: 220 }}>
            {(['rejected', 'resolved'] as DisputeStatus[]).map((s) => (
              <option key={s} value={s}>{statusLabel(s)}</option>
            ))}
          </select>
        </Field>
        <Field label="运营备注(operator_note,可选,≤4000)">
          <input value={note} onChange={(e) => setNote(e.target.value)} placeholder="供审计:裁决依据" style={{ ...inp, width: 360 }} />
        </Field>
        <button type="button" disabled={busy} onClick={submit} className="hk-btn hk-btn--green">
          {busy ? '提交中…' : '提交裁决'}
        </button>
      </div>
    </div>
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
      ? { color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
      : { color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
  return (
    <div style={{ margin: 'var(--hk-space-4)', marginBottom: 'var(--hk-space-2)', padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>
      {children}
    </div>
  )
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }

const disputeColumns: DataListColumn<DisputeTableRow>[] = [
  { key: 'disputeId', label: '争议 ID', render: (row) => <span className="hk-mono" title={row.disputeTitle}>{row.disputeId}</span> },
  { key: 'status', label: '状态', render: (row) => <StatusBadge tone={statusTone(row.status)}>{statusLabel(row.status)}</StatusBadge> },
  { key: 'userId', label: '用户', render: (row) => <span className="hk-mono">{row.userId}</span> },
  { key: 'requestId', label: 'request_id', render: (row) => <span className="hk-mono" title={row.requestTitle}>{row.requestId}</span> },
  { key: 'reason', label: '原因', render: (row) => <span style={{ color: 'var(--hk-ink-700)' }}>{row.reason}</span> },
  { key: 'operatorNote', label: '运营备注', render: (row) => <span style={{ color: 'var(--hk-ink-500)' }}>{row.operatorNote}</span> },
  { key: 'createdAt', label: '创建', render: (row) => <span className="hk-mono">{row.createdAt}</span> },
  { key: 'resolvedAt', label: '裁决', render: (row) => <span className="hk-mono">{row.resolvedAt}</span> },
]
