import { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { listMyOrders } from '../orders/api'
import type { UserOrder } from '../orders/types'
import { getMyBalance } from './api'
import { completedTopupCents, formatMoney, orderStatusLabel, orderStatusTone } from './wallet'

/*
 * 钱包与充值(用户门户)。余额卡(GET /v1/users/me/payments/balance)+ 累计已完成充值 +
 * 充值说明(本平台手动充值:自助开单或联系管理员)+ 最近订单(复用我的订单列表)。纯只读。
 * wallet.ts 的 Tone('ok'|'warn'|'danger'|'muted')与 StatusBadge 的 BadgeTone 同集,直接传。
 */

export function WalletPage() {
  const [balanceCents, setBalanceCents] = useState<number | null>(null)
  const [orders, setOrders] = useState<UserOrder[]>([])
  const [loading, setLoading] = useState(true)
  const [balanceErr, setBalanceErr] = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    // 余额与订单各自独立加载:任一失败不连累另一块。
    getMyBalance(ctrl.signal)
      .then((r) => setBalanceCents(r.balance.amount_cents))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setBalanceErr(e instanceof ApiError ? `${e.message}(${e.code})` : '加载余额失败')
      })
    listMyOrders(20, ctrl.signal)
      .then((r) => setOrders(r.orders))
      .catch(() => {
        /* 订单加载失败不阻断余额展示 */
      })
      .finally(() => {
        if (!ctrl.signal.aborted) setLoading(false)
      })
    return () => ctrl.abort()
  }, [])

  const topupTotal = completedTopupCents(orders)

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>钱包与充值</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>余额、充值与订单。</p>
      </header>

      <div style={cardGrid}>
        <div style={{ ...card, gridColumn: 'span 2' }}>
          <span style={statLabel}>当前余额(USD)</span>
          {balanceErr ? (
            <span style={{ color: 'var(--hk-danger-600, #8f322a)', fontSize: 13 }}>{balanceErr}</span>
          ) : (
            <span style={{ fontSize: 30, fontWeight: 700, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-900)' }}>
              ${balanceCents === null ? '—' : formatMoney(balanceCents)}
            </span>
          )}
        </div>
        <div style={card}>
          <span style={statLabel}>累计已充值(USD)</span>
          <span style={{ fontSize: 22, fontWeight: 600, fontFamily: 'var(--hk-font-mono)' }}>${formatMoney(topupTotal)}</span>
        </div>
      </div>

      <div style={{ ...card, gap: 'var(--hk-space-2)' }}>
        <span style={{ fontSize: 14, fontWeight: 600 }}>如何充值</span>
        <p style={{ margin: 0, fontSize: 13, color: 'var(--hk-ink-500)', lineHeight: 1.6 }}>
          当前支持<strong>手动充值</strong>:在「订阅/兑换」自助开单后按指引完成支付,或联系管理员为你的账户充值。
          充值到账后余额会即时更新。真在线支付网关(自动入账)为后续能力。
        </p>
      </div>

      <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
        <h2 style={{ fontSize: 14, color: 'var(--hk-ink-700)' }}>最近订单</h2>
        {loading && orders.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : orders.length === 0 ? (
          <Empty>还没有订单。</Empty>
        ) : (
          <div style={{ background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', overflow: 'hidden' }}>
            <div style={{ overflowX: 'auto' }}>
              <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
                <thead>
                  <tr>
                    {['单号', '类型', '金额', '状态', '时间'].map((h) => (
                      <th key={h} style={th}>{h}</th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {orders.map((o) => (
                    <tr key={o.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                      <td style={td}><code style={{ fontSize: 12 }}>{o.out_trade_no}</code></td>
                      <td style={td}>{o.order_kind === 'topup' ? '充值' : o.order_kind === 'subscription' ? '订阅' : o.order_kind}</td>
                      <td style={tdNum}>${formatMoney(o.amount_cents)}</td>
                      <td style={td}>
                        <StatusBadge tone={orderStatusTone(o.status)}>{orderStatusLabel(o.status)}</StatusBadge>
                      </td>
                      <td style={td}>{new Date(o.created_at).toLocaleString()}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        )}
      </section>
    </div>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

const cardGrid: React.CSSProperties = { display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(180px, 1fr))', gap: 'var(--hk-space-3)' }
const card: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-1)',
  padding: 'var(--hk-space-4)',
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
  boxShadow: 'var(--hk-shadow-1)',
}
const statLabel: React.CSSProperties = { fontSize: 11, color: 'var(--hk-ink-500)' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
const tdNum: React.CSSProperties = { ...td, textAlign: 'right', fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
