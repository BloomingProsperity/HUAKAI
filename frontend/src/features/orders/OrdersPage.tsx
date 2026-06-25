import { useCallback, useEffect, useMemo, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { getMyOrder, listMyOrders } from './api'
import {
  ORDER_STATUSES,
  buildTimeline,
  clampLimit,
  filterByStatus,
  formatMoney,
  orderKindLabel,
  providerLabel,
  statusCounts,
  statusLabel,
  statusTone,
} from './orders'
import type { UserOrder } from './types'

/*
 * 我的订单(user 壳)。/v1/users/me/payments/orders 列表 + /{id} 详情(状态机时间线)。
 * 纯只读:本页只 GET,不发起取消/退款等写动作。
 * 后端列表端点只收 limit、不收 status/offset,故状态筛选在前端对窗口内做。
 */

const LIMIT_OPTIONS = [50, 100, 200]

export function OrdersPage() {
  const [orders, setOrders] = useState<UserOrder[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [limit, setLimit] = useState(50)
  const [statusFilter, setStatusFilter] = useState('')
  const [refreshNonce, setRefreshNonce] = useState(0)
  const [detailId, setDetailId] = useState<number | null>(null)

  const load = useCallback(
    (signal: AbortSignal) => {
      setLoading(true)
      setError(null)
      listMyOrders(clampLimit(limit), signal)
        .then((resp) => setOrders(resp.orders ?? []))
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载订单列表失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [limit],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, refreshNonce])

  const counts = useMemo(() => statusCounts(orders), [orders])
  const visible = useMemo(() => filterByStatus(orders, statusFilter), [orders, statusFilter])

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--hk-space-3)' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
          <h1 style={{ fontSize: 22 }}>我的订单</h1>
          <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
            充值与订阅订单的历史与状态。共 {orders.length} 条{statusFilter ? `,当前筛选 ${visible.length} 条` : ''}。
          </p>
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
          <label style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>显示</label>
          <select
            value={limit}
            onChange={(e) => setLimit(Number(e.target.value))}
            aria-label="每页条数"
            style={selectStyle}
          >
            {LIMIT_OPTIONS.map((n) => (
              <option key={n} value={n}>
                最近 {n} 条
              </option>
            ))}
          </select>
          <button type="button" onClick={() => setRefreshNonce((n) => n + 1)} style={ghostBtn}>
            刷新
          </button>
        </div>
      </header>

      {error && (
        <div style={errorBox}>{error}</div>
      )}

      {/* 状态筛选条(客户端过滤当前窗口) */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-2)' }}>
        <FilterChip active={statusFilter === ''} onClick={() => setStatusFilter('')}>
          全部 {orders.length}
        </FilterChip>
        {ORDER_STATUSES.map((s) => (
          <FilterChip key={s.value} active={statusFilter === s.value} onClick={() => setStatusFilter(s.value)}>
            {s.label} {counts[s.value] ?? 0}
          </FilterChip>
        ))}
      </div>

      <div style={cardStyle}>
        {loading && orders.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : visible.length === 0 ? (
          <Empty>{orders.length === 0 ? '还没有订单记录。' : '当前筛选下没有订单。'}</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['订单号', '类型', '金额', '渠道', '状态', '创建时间', ''].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {visible.map((o) => (
                  <tr key={o.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={td}>
                      <code style={{ fontSize: 12, color: 'var(--hk-ink-700)' }}>{o.out_trade_no}</code>
                    </td>
                    <td style={td}>{orderKindLabel(o.order_kind)}</td>
                    <td style={{ ...td, fontWeight: 600, color: 'var(--hk-ink-900)' }}>
                      {formatMoney(o.amount_cents, o.currency_code)}
                    </td>
                    <td style={td}>{providerLabel(o.provider_kind)}</td>
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

      {detailId != null && <OrderDetailDrawer orderId={detailId} onClose={() => setDetailId(null)} />}
    </div>
  )
}

/* ---------------- 订单详情抽屉(状态机时间线) ---------------- */

function OrderDetailDrawer({ orderId, onClose }: { orderId: number; onClose: () => void }) {
  const [order, setOrder] = useState<UserOrder | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    setError(null)
    getMyOrder(orderId, ctrl.signal)
      .then((resp) => setOrder(resp.order))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载订单详情失败')
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [orderId])

  const timeline = order ? buildTimeline(order) : []

  return (
    <div style={overlay} onClick={onClose}>
      <aside style={drawer} onClick={(e) => e.stopPropagation()} role="dialog" aria-label="订单详情">
        <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--hk-space-4)' }}>
          <h2 style={{ fontSize: 18, margin: 0 }}>订单详情</h2>
          <button type="button" onClick={onClose} aria-label="关闭" style={closeBtn}>
            ✕
          </button>
        </header>

        {loading ? (
          <p style={{ color: 'var(--hk-ink-500)', fontSize: 13 }}>加载中…</p>
        ) : error ? (
          <div style={errorBox}>{error}</div>
        ) : order ? (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-5)' }}>
            <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
              <Field label="订单号" value={<code style={{ fontSize: 12 }}>{order.out_trade_no}</code>} />
              <Field label="金额" value={<strong>{formatMoney(order.amount_cents, order.currency_code)}</strong>} />
              <Field label="类型" value={orderKindLabel(order.order_kind)} />
              <Field label="渠道" value={providerLabel(order.provider_kind)} />
              <Field label="当前状态" value={<StatusBadge tone={statusTone(order.status)}>{statusLabel(order.status)}</StatusBadge>} />
              {order.expires_at && <Field label="过期时间" value={fmt(order.expires_at)} />}
            </section>

            <section>
              <h3 style={{ fontSize: 13, color: 'var(--hk-ink-500)', margin: '0 0 var(--hk-space-3)' }}>状态流转</h3>
              <ol style={{ listStyle: 'none', margin: 0, padding: 0, display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
                {timeline.map((step) => (
                  <li key={step.key} style={{ display: 'flex', alignItems: 'flex-start', gap: 'var(--hk-space-3)' }}>
                    <span
                      aria-hidden
                      style={{
                        marginTop: 4,
                        width: 10,
                        height: 10,
                        borderRadius: 'var(--hk-radius-pill)',
                        flexShrink: 0,
                        background: step.done ? 'var(--hk-primary-500)' : 'var(--hk-surface-sunken)',
                        border: `1px solid ${step.done ? 'var(--hk-primary-600)' : 'var(--hk-line)'}`,
                      }}
                    />
                    <div style={{ display: 'flex', flexDirection: 'column' }}>
                      <span style={{ fontSize: 13, color: step.done ? 'var(--hk-ink-900)' : 'var(--hk-ink-300)' }}>
                        {step.label}
                      </span>
                      <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>
                        {step.at ? fmt(step.at) : '尚未发生'}
                      </span>
                    </div>
                  </li>
                ))}
              </ol>
            </section>
          </div>
        ) : (
          <Empty>未找到订单。</Empty>
        )}
      </aside>
    </div>
  )
}

function Field({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between', gap: 'var(--hk-space-4)', fontSize: 13 }}>
      <span style={{ color: 'var(--hk-ink-500)' }}>{label}</span>
      <span style={{ color: 'var(--hk-ink-700)', textAlign: 'right' }}>{value}</span>
    </div>
  )
}

function FilterChip({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        height: 30,
        padding: '0 var(--hk-space-3)',
        fontSize: 12,
        cursor: 'pointer',
        borderRadius: 'var(--hk-radius-pill)',
        border: `1px solid ${active ? 'var(--hk-primary-500)' : 'var(--hk-line)'}`,
        background: active ? 'var(--hk-primary-50)' : 'var(--hk-surface)',
        color: active ? 'var(--hk-primary-700)' : 'var(--hk-ink-700)',
        whiteSpace: 'nowrap',
      }}
    >
      {children}
    </button>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

function fmt(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString('zh-CN', { hour12: false })
}

const cardStyle: React.CSSProperties = {
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
  boxShadow: 'var(--hk-shadow-1)',
  overflow: 'hidden',
}
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle', whiteSpace: 'nowrap', color: 'var(--hk-ink-700)' }
const selectStyle: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-2)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13 }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const linkBtn: React.CSSProperties = { height: 28, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-primary-700)', fontSize: 12, cursor: 'pointer' }
const closeBtn: React.CSSProperties = { width: 28, height: 28, border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-500)', fontSize: 14, cursor: 'pointer', lineHeight: 1 }
const errorBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }
const overlay: React.CSSProperties = { position: 'fixed', inset: 0, background: 'rgba(15, 23, 27, 0.32)', zIndex: 'var(--hk-z-overlay)' as unknown as number, display: 'flex', justifyContent: 'flex-end' }
const drawer: React.CSSProperties = { width: 'min(440px, 92vw)', height: '100%', background: 'var(--hk-surface)', boxShadow: 'var(--hk-shadow-3)', padding: 'var(--hk-space-6)', overflowY: 'auto' }
