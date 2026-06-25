import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { cancelOrder, confirmOrder, getOrder, listOrders, retryOrder } from './api'
import {
  buildOrderListQuery,
  EMPTY_ORDER_FILTER,
  formatCents,
  hasAnyAction,
  ORDER_STATUSES,
  orderActions,
  statusLabel,
  statusTone,
  type OrderFilterForm,
} from './ordersadmin'
import type { AdminOrder, OrderAuditEvent } from './types'

/*
 * 订单管理台(运营台,admin 壳)。管线之「钱」侧只读+卡单处置。
 * 端点前缀 /v1/admin/payments(admin token)。多维筛选(租户/用户/状态/时间)+ 分页 +
 * 详情抽屉(状态机展示 + 审计轨迹)+ 卡单动作(确认/取消/重试,均接已有 admin 端点)。
 * money 不在此发起任何支付,仅做运营处置(确认到账/撤单/重试履约)。
 */
const PAGE_SIZE = 50

export function OrdersAdminPage() {
  const [draft, setDraft] = useState<OrderFilterForm>(EMPTY_ORDER_FILTER)
  const [applied, setApplied] = useState<OrderFilterForm | null>(null)
  const [page, setPage] = useState(0)
  const [orders, setOrders] = useState<AdminOrder[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [detailId, setDetailId] = useState<number | null>(null)

  const tenantId = applied ? Number(applied.tenantId.trim()) : 0

  const load = useCallback(
    (signal: AbortSignal) => {
      if (!applied) return
      const built = buildOrderListQuery(applied, PAGE_SIZE, page * PAGE_SIZE)
      if ('error' in built) {
        setError(built.error)
        return
      }
      setLoading(true)
      setError(null)
      listOrders(built.query, signal)
        .then((resp) => setOrders(resp.orders ?? []))
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载订单列表失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [applied, page],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, refreshNonce])

  const refresh = () => setRefreshNonce((n) => n + 1)

  const submitFilter = () => {
    const built = buildOrderListQuery(draft, PAGE_SIZE, 0)
    if ('error' in built) {
      setError(built.error)
      return
    }
    setError(null)
    setPage(0)
    setApplied(draft)
  }

  const set = <K extends keyof OrderFilterForm>(k: K, v: OrderFilterForm[K]) =>
    setDraft((f) => ({ ...f, [k]: v }))

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>订单管理台</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          运营台 · 充值/订阅订单的多维查询、状态机查看与卡单处置(确认到账 / 撤单 / 重试履约)。
        </p>
      </header>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          submitFilter()
        }}
        style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(160px,1fr))', gap: 'var(--hk-space-3)', alignItems: 'flex-end', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)' }}
      >
        <Field label="租户 ID(必填)">
          <input value={draft.tenantId} onChange={(e) => set('tenantId', e.target.value)} placeholder="如 1" inputMode="numeric" style={inp} />
        </Field>
        <Field label="用户 ID(可选)">
          <input value={draft.userId} onChange={(e) => set('userId', e.target.value)} placeholder="按用户筛选" inputMode="numeric" style={inp} />
        </Field>
        <Field label="状态">
          <select value={draft.status} onChange={(e) => set('status', e.target.value)} style={inp}>
            <option value="">全部</option>
            {ORDER_STATUSES.map((s) => (
              <option key={s.value} value={s.value}>
                {s.label}
              </option>
            ))}
          </select>
        </Field>
        <Field label="创建时间(起)">
          <input type="datetime-local" value={draft.createdFrom} onChange={(e) => set('createdFrom', e.target.value)} style={inp} />
        </Field>
        <Field label="创建时间(止)">
          <input type="datetime-local" value={draft.createdTo} onChange={(e) => set('createdTo', e.target.value)} style={inp} />
        </Field>
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
          <button type="submit" style={primaryBtn}>
            查询
          </button>
          <button
            type="button"
            onClick={() => {
              setDraft(EMPTY_ORDER_FILTER)
              setApplied(null)
              setOrders([])
              setError(null)
            }}
            style={ghostBtn}
          >
            重置
          </button>
        </div>
      </form>

      {error && <div style={errBox}>{error}</div>}

      {!applied ? (
        <div style={panel}>
          <Empty>请填写租户 ID 后查询订单。</Empty>
        </div>
      ) : (
        <div style={panel}>
          {loading && orders.length === 0 ? (
            <Empty>加载中…</Empty>
          ) : orders.length === 0 ? (
            <Empty>该筛选条件下没有订单。</Empty>
          ) : (
            <div style={{ overflowX: 'auto' }}>
              <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
                <thead>
                  <tr>
                    {['订单号', '用户', '金额', '类型', '渠道', '状态', '创建时间', ''].map((h) => (
                      <th key={h} style={th}>
                        {h}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {orders.map((o) => (
                    <tr key={o.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                      <td style={td}>
                        <button type="button" onClick={() => setDetailId(o.id)} style={{ ...linkBtn, fontWeight: 600 }}>
                          {o.out_trade_no || `#${o.id}`}
                        </button>
                      </td>
                      <td style={tdNum}>{o.user_id}</td>
                      <td style={tdNum}>{formatCents(o.amount_cents, o.currency_code)}</td>
                      <td style={td}>{o.order_kind || '充值'}</td>
                      <td style={td}>{o.provider_kind || '—'}</td>
                      <td style={td}>
                        <StatusBadge tone={statusTone(o.status)}>{statusLabel(o.status)}</StatusBadge>
                      </td>
                      <td style={td}>{fmt(o.created_at)}</td>
                      <td style={{ ...td, textAlign: 'right', whiteSpace: 'nowrap' }}>
                        <button type="button" onClick={() => setDetailId(o.id)} style={linkBtn}>
                          详情
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}

      {applied && (
        <div style={{ display: 'flex', justifyContent: 'flex-end', alignItems: 'center', gap: 'var(--hk-space-3)', fontSize: 13, color: 'var(--hk-ink-500)' }}>
          <span>第 {page + 1} 页</span>
          <button type="button" disabled={page === 0 || loading} onClick={() => setPage((p) => Math.max(0, p - 1))} style={ghostBtn}>
            上一页
          </button>
          <button type="button" disabled={orders.length < PAGE_SIZE || loading} onClick={() => setPage((p) => p + 1)} style={ghostBtn}>
            下一页
          </button>
        </div>
      )}

      {detailId !== null && tenantId > 0 && (
        <OrderDetailDrawer
          id={detailId}
          tenantId={tenantId}
          onClose={() => setDetailId(null)}
          onActed={() => {
            setDetailId(null)
            refresh()
          }}
        />
      )}
    </div>
  )
}

function OrderDetailDrawer({
  id,
  tenantId,
  onClose,
  onActed,
}: {
  id: number
  tenantId: number
  onClose: () => void
  onActed: () => void
}) {
  const [order, setOrder] = useState<AdminOrder | null>(null)
  const [events, setEvents] = useState<OrderAuditEvent[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [actErr, setActErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [reason, setReason] = useState('')

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError(null)
    getOrder(id, tenantId, ctrl.signal)
      .then((resp) => {
        setOrder(resp.order)
        setEvents(resp.audit_events ?? [])
      })
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载订单详情失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [id, tenantId])

  const act = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    setActErr(null)
    try {
      await fn()
      onActed()
    } catch (e) {
      setActErr(e instanceof ApiError ? `${e.message}(${e.code})` : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  const actions = order ? orderActions(order.status) : { canConfirm: false, canCancel: false, canRetry: false }

  return (
    <div onClick={onClose} style={{ position: 'fixed', inset: 0, background: 'rgba(28,38,34,0.4)', display: 'flex', justifyContent: 'flex-end', zIndex: 'var(--hk-z-overlay)' as unknown as number }}>
      <div onClick={(e) => e.stopPropagation()} style={{ width: 'min(520px,100%)', height: '100%', background: 'var(--hk-surface)', boxShadow: 'var(--hk-shadow-3)', padding: 'var(--hk-space-5)', overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
        <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <h2 style={{ fontSize: 18 }}>订单 #{id}</h2>
          <button type="button" onClick={onClose} style={ghostBtn}>
            关闭
          </button>
        </header>

        {loading ? (
          <Empty>加载中…</Empty>
        ) : error ? (
          <div style={errBox}>{error}</div>
        ) : order ? (
          <>
            <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
              <Row label="状态">
                <StatusBadge tone={statusTone(order.status)}>{statusLabel(order.status)}</StatusBadge>
              </Row>
              <Row label="订单号">{order.out_trade_no || '—'}</Row>
              <Row label="用户 ID">{order.user_id}</Row>
              <Row label="金额">{formatCents(order.amount_cents, order.currency_code)}</Row>
              <Row label="类型">{order.order_kind || '充值'}</Row>
              <Row label="渠道">{order.provider_kind || '—'}</Row>
              <Row label="创建时间">{fmt(order.created_at)}</Row>
              <Row label="更新时间">{fmt(order.updated_at)}</Row>
              {order.paid_at && <Row label="支付时间">{fmt(order.paid_at)}</Row>}
              {order.completed_at && <Row label="完成时间">{fmt(order.completed_at)}</Row>}
              {order.confirmed_by_admin_id ? <Row label="确认管理员">{order.confirmed_by_admin_id}</Row> : null}
              {order.failure_code && <Row label="失败码">{order.failure_code}</Row>}
              {order.failure_message && <Row label="失败信息">{order.failure_message}</Row>}
            </section>

            <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
              <h3 style={{ fontSize: 14, color: 'var(--hk-ink-700)' }}>审计轨迹</h3>
              {events.length === 0 ? (
                <Empty>暂无审计事件。</Empty>
              ) : (
                <ol style={{ listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
                  {events.map((ev, i) => (
                    <li key={i} style={{ borderLeft: '2px solid var(--hk-line)', paddingLeft: 'var(--hk-space-3)', fontSize: 13 }}>
                      <div style={{ fontWeight: 600 }}>{ev.event_type}</div>
                      <div style={{ color: 'var(--hk-ink-500)', fontSize: 12 }}>
                        {ev.actor_kind}
                        {ev.actor_id ? ` #${ev.actor_id}` : ''} · {fmt(ev.occurred_at)}
                        {ev.reason_class ? ` · ${ev.reason_class}` : ''}
                      </div>
                    </li>
                  ))}
                </ol>
              )}
            </section>

            {hasAnyAction(order.status) && (
              <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', borderTop: '1px solid var(--hk-line)', paddingTop: 'var(--hk-space-4)' }}>
                <h3 style={{ fontSize: 14, color: 'var(--hk-ink-700)' }}>卡单处置</h3>
                <Field label="原因(可选,记入审计)">
                  <input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="如:运营人工核对到账 / 用户超时未支付" style={inp} />
                </Field>
                {actErr && <div style={errBox}>{actErr}</div>}
                <div style={{ display: 'flex', gap: 'var(--hk-space-2)', flexWrap: 'wrap' }}>
                  {actions.canConfirm && (
                    <button type="button" disabled={busy} onClick={() => act(() => confirmOrder(order.id, tenantId, reason))} style={primaryBtn}>
                      确认到账并履约
                    </button>
                  )}
                  {actions.canRetry && (
                    <button type="button" disabled={busy} onClick={() => act(() => retryOrder(order.id, tenantId))} style={ghostBtn}>
                      重试履约
                    </button>
                  )}
                  {actions.canCancel && (
                    <button type="button" disabled={busy} onClick={() => act(() => cancelOrder(order.id, tenantId, reason))} style={dangerBtn}>
                      取消订单
                    </button>
                  )}
                </div>
              </section>
            )}
          </>
        ) : null}
      </div>
    </div>
  )
}

function fmt(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false })
}
function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}
function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 'var(--hk-space-3)', fontSize: 13 }}>
      <span style={{ color: 'var(--hk-ink-500)' }}>{label}</span>
      <span style={{ color: 'var(--hk-ink-900)', textAlign: 'right' }}>{children}</span>
    </div>
  )
}
function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
const tdNum: React.CSSProperties = { ...td, textAlign: 'right', fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)' }
const panel: React.CSSProperties = { background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }
const errBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const dangerBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid #f2cdc8', borderRadius: 'var(--hk-radius-md)', background: '#fbe9e7', color: '#8f322a', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const linkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
