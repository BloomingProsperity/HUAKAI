import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { AdminCreateOrder } from './AdminCreateOrder'
import { cancelOrder, confirmOrder, getOrder, listOrders, refundOrder, retryOrder } from './api'
import { DashboardCards } from './DashboardCards'
import { ExportToolbar } from './ExportToolbar'
import { ProviderConfigPanel } from './ProviderConfigPanel'
import {
  buildOrderListQuery,
  canRefund,
  EMPTY_ORDER_FILTER,
  formatCents,
  hasAnyAction,
  ORDER_STATUSES,
  orderActions,
  parseRefundAmount,
  statusLabel,
  statusTone,
  type OrderFilterForm,
} from './ordersadmin'
import { RefundRequestsTab } from './RefundRequestsTab'
import {
  dangerBtn,
  errBox,
  Empty,
  Field,
  fmt,
  ghostBtn,
  inp,
  linkBtn,
  panel,
  primaryBtn,
  Row,
  td,
  tdNum,
  th,
} from './ui'
import type { AdminOrder, OrderAuditEvent } from './types'

type TabKey = 'orders' | 'refund-requests' | 'create-order' | 'provider-config'

/*
 * 订单管理台(运营台,admin 壳)。管线之「钱」侧只读+卡单处置。
 * 端点前缀 /v1/admin/payments(admin token)。多维筛选(租户/用户/状态/时间)+ 分页 +
 * 详情抽屉(状态机展示 + 审计轨迹)+ 卡单动作(确认/取消/重试,均接已有 admin 端点)。
 * money 不在此发起任何支付,仅做运营处置(确认到账/撤单/重试履约)。
 */
const PAGE_SIZE = 50

export function OrdersAdminPage() {
  const [tab, setTab] = useState<TabKey>('orders')
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
          运营台 · 充值/订阅订单的多维查询、状态机查看、卡单处置与退款工单审批(均接已有 admin 端点)。
        </p>
      </header>

      {/* 页顶仪表盘汇总(已查询某租户时显示)。 */}
      <DashboardCards tenantId={tenantId} />

      {/* Tab 切换:订单列表 / 退款工单。 */}
      <div style={{ display: 'flex', gap: 'var(--hk-space-1)', borderBottom: '1px solid var(--hk-line)' }}>
        {([
          { key: 'orders' as TabKey, label: '订单列表' },
          { key: 'refund-requests' as TabKey, label: '退款工单' },
          { key: 'create-order' as TabKey, label: '代客建单' },
          { key: 'provider-config' as TabKey, label: '支付商配置' },
        ]).map((t) => (
          <button
            key={t.key}
            type="button"
            onClick={() => setTab(t.key)}
            style={{
              height: 36,
              padding: '0 var(--hk-space-4)',
              border: 'none',
              borderBottom: tab === t.key ? '2px solid var(--hk-primary-600)' : '2px solid transparent',
              background: 'transparent',
              color: tab === t.key ? 'var(--hk-ink-900)' : 'var(--hk-ink-500)',
              fontSize: 14,
              fontWeight: tab === t.key ? 600 : 400,
              cursor: 'pointer',
            }}
          >
            {t.label}
          </button>
        ))}
      </div>

      {tab === 'refund-requests' ? (
        <RefundRequestsTab tenantId={tenantId} />
      ) : tab === 'create-order' ? (
        <AdminCreateOrder onCreated={refresh} />
      ) : tab === 'provider-config' ? (
        <ProviderConfigPanel />
      ) : (
        <OrdersTab
          draft={draft}
          applied={applied}
          page={page}
          orders={orders}
          loading={loading}
          error={error}
          setDetailId={setDetailId}
          submitFilter={submitFilter}
          set={set}
          setDraft={setDraft}
          setApplied={setApplied}
          setOrders={setOrders}
          setError={setError}
          setPage={setPage}
        />
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

function OrdersTab({
  draft,
  applied,
  page,
  orders,
  loading,
  error,
  setDetailId,
  submitFilter,
  set,
  setDraft,
  setApplied,
  setOrders,
  setError,
  setPage,
}: {
  draft: OrderFilterForm
  applied: OrderFilterForm | null
  page: number
  orders: AdminOrder[]
  loading: boolean
  error: string | null
  setDetailId: (id: number | null) => void
  submitFilter: () => void
  set: <K extends keyof OrderFilterForm>(k: K, v: OrderFilterForm[K]) => void
  setDraft: (f: OrderFilterForm) => void
  setApplied: (f: OrderFilterForm | null) => void
  setOrders: (o: AdminOrder[]) => void
  setError: (e: string | null) => void
  setPage: React.Dispatch<React.SetStateAction<number>>
}) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      {/* CSV 导出工具栏(只读)。 */}
      <ExportToolbar />

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
  // 退款专用输入(money 敏感):金额(元,空=全额)+ 退款理由。
  const [refundAmount, setRefundAmount] = useState('')
  const [refundReason, setRefundReason] = useState('')

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
  const refundable = order ? canRefund(order.status) : false

  // doRefund:money 敏感 + 破坏性。先用 parseRefundAmount 本地校验(空=全额、超额先拦),
  // 再二次确认,确认后生成幂等 key 调用退款端点。
  const doRefund = () => {
    if (!order) return
    const parsed = parseRefundAmount(refundAmount, order.amount_cents)
    if ('error' in parsed) {
      setActErr(parsed.error)
      return
    }
    const isFull = parsed.amountCents === 0
    const amountLabel = isFull
      ? `全额(${formatCents(order.amount_cents, order.currency_code)})`
      : formatCents(parsed.amountCents, order.currency_code)
    if (
      !window.confirm(
        `确认对订单 #${order.id} 退款 ${amountLabel}?\n将退回上游并扣减用户 #${order.user_id} 余额,此操作动钱且不可撤销。`,
      )
    ) {
      return
    }
    // 幂等 key:同订单 + 金额 + 时间戳;后端按 key 去重,避免误点重复退款。
    const idempotencyKey = `admin-refund:${order.id}:${parsed.amountCents}:${Date.now()}`
    void act(() => refundOrder(order.id, tenantId, parsed.amountCents, idempotencyKey, refundReason))
  }

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

            {refundable && (
              <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', borderTop: '1px solid var(--hk-line)', paddingTop: 'var(--hk-space-4)' }}>
                <h3 style={{ fontSize: 14, color: '#8f322a' }}>退款(动钱 · 不可撤销)</h3>
                <p style={{ margin: 0, fontSize: 12, color: 'var(--hk-ink-500)' }}>
                  仅已到账(completed)的充值订单可退。退款将退回上游并扣减用户 #{order.user_id} 余额。
                  金额留空=全额退,订单原额 {formatCents(order.amount_cents, order.currency_code)}。
                </p>
                <Field label="退款金额(元,留空=全额)">
                  <input
                    value={refundAmount}
                    onChange={(e) => setRefundAmount(e.target.value)}
                    placeholder={`≤ ${formatCents(order.amount_cents, order.currency_code)}`}
                    inputMode="decimal"
                    style={inp}
                  />
                </Field>
                <Field label="退款原因(可选,记入审计)">
                  <input value={refundReason} onChange={(e) => setRefundReason(e.target.value)} placeholder="如:用户申请 / 重复支付" style={inp} />
                </Field>
                {actErr && <div style={errBox}>{actErr}</div>}
                <div>
                  <button type="button" disabled={busy} onClick={doRefund} style={dangerBtn}>
                    退款
                  </button>
                </div>
              </section>
            )}
          </>
        ) : null}
      </div>
    </div>
  )
}

