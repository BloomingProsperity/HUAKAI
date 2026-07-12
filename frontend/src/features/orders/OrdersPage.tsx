import { useCallback, useEffect, useMemo, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import {
  cancelMyOrder,
  downloadReceiptText,
  fetchOrderReceipt,
  getMyOrder,
  listMyOrders,
  requestOrderRefund,
} from './api'
import {
  ORDER_STATUSES,
  buildTimeline,
  cancellable,
  clampLimit,
  filterByStatus,
  formatMoney,
  hasUserAction,
  orderKindLabel,
  providerLabel,
  receiptEligible,
  refundRequestable,
  statusCounts,
  statusLabel,
  statusTone,
} from './orders'
import type { UserOrder } from './types'

/*
 * 我的订单(user 壳)。/v1/users/me/payments/orders 列表 + /{id} 详情(状态机时间线)。
 * 写动作(均 money/状态敏感,二次确认):
 *   - 撤单:仅 pending 单可撤(付款前撤回),POST /orders/{id}/cancel;
 *   - 申请退款:仅「已完成的充值单」可申请,POST /orders/{id}/refund-request(只建 pending 记录待 admin 审批,不即时动钱)。
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
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>我的订单</h1>
          <p className="hk-sub">
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
          <button type="button" onClick={() => setRefreshNonce((n) => n + 1)} className="hk-btn">
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

      <div className="hk-card">
        {loading && orders.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : visible.length === 0 ? (
          <Empty>{orders.length === 0 ? '还没有订单记录。' : '当前筛选下没有订单。'}</Empty>
        ) : (
          <div className="hk-tablewrap">
            <table className="hk-table">
              <thead>
                <tr>
                  {['订单号', '类型', '金额', '渠道', '状态', '创建时间', ''].map((h) => (
                    <th key={h}>{h}</th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {visible.map((o) => (
                  <tr key={o.id}>
                    <td>
                      <code style={{ fontSize: 12, color: 'var(--hk-ink-700)' }}>{o.out_trade_no}</code>
                    </td>
                    <td>{orderKindLabel(o.order_kind)}</td>
                    <td className="hk-mono" style={{ textAlign: 'right', fontWeight: 600, color: 'var(--hk-ink-900)' }}>
                      {formatMoney(o.amount_cents, o.currency_code)}
                    </td>
                    <td>{providerLabel(o.provider_kind)}</td>
                    <td>
                      <StatusBadge tone={statusTone(o.status)}>{statusLabel(o.status)}</StatusBadge>
                    </td>
                    <td className="hk-mono">{fmt(o.created_at)}</td>
                    <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                      <div style={{ display: 'inline-flex', gap: 'var(--hk-space-2)', alignItems: 'center' }}>
                        {/* 行内入口:可撤单/可退款时直接给出对应动作入口,点开即落到详情抽屉的二次确认流程,
                            避免在表格行直接触发 money/破坏性动作(确认与执行集中在抽屉一处)。 */}
                        {cancellable(o) && (
                          <button type="button" onClick={() => setDetailId(o.id)} className="hk-btn hk-btn--sm hk-btn--danger">
                            撤单
                          </button>
                        )}
                        {refundRequestable(o) && (
                          <button type="button" onClick={() => setDetailId(o.id)} className="hk-btn hk-btn--sm">
                            申请退款
                          </button>
                        )}
                        <button type="button" onClick={() => setDetailId(o.id)} className="hk-btn hk-btn--sm">
                          详情
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {detailId != null && (
        <OrderDetailDrawer
          orderId={detailId}
          onClose={() => setDetailId(null)}
          onChanged={() => setRefreshNonce((n) => n + 1)}
        />
      )}
    </div>
  )
}

/* ---------------- 订单详情抽屉(状态机时间线) ---------------- */

function OrderDetailDrawer({
  orderId,
  onClose,
  onChanged,
}: {
  orderId: number
  onClose: () => void
  onChanged: () => void
}) {
  const [order, setOrder] = useState<UserOrder | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [reloadNonce, setReloadNonce] = useState(0)

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
  }, [orderId, reloadNonce])

  // 写动作成功后:刷新抽屉内详情(看到新状态)+ 通知列表重拉。
  const afterAction = useCallback(() => {
    setReloadNonce((n) => n + 1)
    onChanged()
  }, [onChanged])

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

            {/* 用户自助动作:撤单(pending)/ 申请退款(已完成充值单)。均二次确认。 */}
            {hasUserAction(order) && <OrderActions order={order} onChanged={afterAction} />}

            {/* 收据下载:仅对「已完成的充值/订阅订单」可得(与后端 invoicehttp 资格判定一致) */}
            {receiptEligible(order) && <ReceiptSection order={order} />}

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

/* ---------------- 订单自助动作(撤单 / 申请退款,均二次确认) ---------------- */

function OrderActions({ order, onChanged }: { order: UserOrder; onChanged: () => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [ok, setOk] = useState<string | null>(null)
  const canCancel = cancellable(order)
  const canRefund = refundRequestable(order)

  const doCancel = () => {
    setError(null)
    setOk(null)
    // 撤单二次确认:明示这是不可逆的撤回(订单将变为已取消)。pending 单从未入账,撤单不动钱。
    if (
      !window.confirm(
        `确认撤销订单 ${order.out_trade_no}(${formatMoney(order.amount_cents, order.currency_code)})?\n\n` +
          '撤销后该「待支付」订单将作废且不可恢复;若你尚未付款,可放心撤回。',
      )
    ) {
      return
    }
    setBusy(true)
    cancelMyOrder(order.id)
      .then(() => {
        setOk('订单已撤销。')
        onChanged()
      })
      .catch((e: unknown) => {
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '撤单失败')
      })
      .finally(() => setBusy(false))
  }

  const doRefund = () => {
    setError(null)
    setOk(null)
    // 退款申请:reason 选填,供管理员审批审计。明示「只是发起申请,不会立即退款」。
    const reason = window.prompt(
      `对订单 ${order.out_trade_no}(${formatMoney(order.amount_cents, order.currency_code)})申请退款。\n` +
        '这只是发起一条退款申请,需管理员审批后才会实际退款,不会立即到账。\n\n请填写退款原因(选填):',
      '',
    )
    // prompt 返回 null = 用户取消;此时不发起请求。空串=确认但不填原因,允许继续。
    if (reason === null) return
    setBusy(true)
    requestOrderRefund(order.id, { reason })
      .then(() => {
        setOk('退款申请已提交,等待管理员审批。')
        onChanged()
      })
      .catch((e: unknown) => {
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '提交退款申请失败')
      })
      .finally(() => setBusy(false))
  }

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
      <h3 style={{ fontSize: 13, color: 'var(--hk-ink-500)', margin: 0 }}>订单操作</h3>
      {error && <div style={errorBox}>{error}</div>}
      {ok && (
        <div
          style={{
            padding: 'var(--hk-space-2) var(--hk-space-3)',
            borderRadius: 'var(--hk-radius-md)',
            fontSize: 13,
            color: 'var(--hk-primary-700)',
            background: 'var(--hk-primary-50, var(--hk-primary-50))',
            border: '1px solid var(--hk-line)',
          }}
        >
          {ok}
        </div>
      )}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-2)' }}>
        {canCancel && (
          <button type="button" disabled={busy} onClick={doCancel} className="hk-btn hk-btn--sm hk-btn--danger">
            {busy ? '处理中…' : '撤销订单'}
          </button>
        )}
        {canRefund && (
          <button type="button" disabled={busy} onClick={doRefund} className="hk-btn hk-btn--sm">
            {busy ? '处理中…' : '申请退款'}
          </button>
        )}
      </div>
      {canRefund && (
        <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>
          退款申请提交后由管理员审批,审批通过才会实际退款,不会立即到账。
        </span>
      )}
    </section>
  )
}

/* ---------------- 订单收据(获取文本 + 预览 + 下载) ---------------- */

function ReceiptSection({ order }: { order: UserOrder }) {
  const [content, setContent] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const load = async () => {
    setLoading(true)
    setError(null)
    try {
      const text = await fetchOrderReceipt(order.id)
      setContent(text)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '获取收据失败')
    } finally {
      setLoading(false)
    }
  }

  const download = () => {
    if (content == null) return
    downloadReceiptText(order.id, order.out_trade_no, content)
  }

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
      <h3 style={{ fontSize: 13, color: 'var(--hk-ink-500)', margin: 0 }}>收据</h3>
      {error && <div style={errorBox}>{error}</div>}
      {content == null ? (
        <div>
          <button type="button" onClick={load} disabled={loading} className="hk-btn hk-btn--sm">
            {loading ? '获取中…' : '查看收据'}
          </button>
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
          <pre
            style={{
              margin: 0,
              padding: 'var(--hk-space-3)',
              fontSize: 12,
              fontFamily: 'var(--hk-font-mono)',
              color: 'var(--hk-ink-700)',
              background: 'var(--hk-surface-sunken)',
              border: '1px solid var(--hk-line)',
              borderRadius: 'var(--hk-radius-md)',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
            }}
          >
            {content}
          </pre>
          <div>
            <button type="button" onClick={download} className="hk-btn hk-btn--sm">
              下载收据(.txt)
            </button>
          </div>
        </div>
      )}
    </section>
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
  return <div className="hk-empty">{children}</div>
}

function fmt(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString('zh-CN', { hour12: false })
}

const selectStyle: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-2)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13 }
const closeBtn: React.CSSProperties = { width: 28, height: 28, border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-surface)', color: 'var(--hk-ink-500)', fontSize: 14, cursor: 'pointer', lineHeight: 1 }
const errorBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
const overlay: React.CSSProperties = { position: 'fixed', inset: 0, background: 'rgba(15, 23, 27, 0.32)', zIndex: 'var(--hk-z-overlay)' as unknown as number, display: 'flex', justifyContent: 'flex-end' }
const drawer: React.CSSProperties = { width: 'min(440px, 92vw)', height: '100%', background: 'var(--hk-surface)', boxShadow: 'var(--hk-shadow-3)', padding: 'var(--hk-space-6)', overflowY: 'auto' }
