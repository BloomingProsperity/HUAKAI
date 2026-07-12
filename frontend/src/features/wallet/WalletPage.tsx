import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { listMyOrders } from '../orders/api'
import type { UserOrder } from '../orders/types'
import { createTopupOrder, getMyBalance, getPortalConfig } from './api'
import type { PortalProviderConfig, PortalTopupConfig } from './types'
import {
  completedTopupCents,
  formatCentsRange,
  formatMoney,
  orderStatusLabel,
  orderStatusTone,
  parseTopupAmount,
  providerLabel,
} from './wallet'

/*
 * 钱包与充值(用户门户)。余额卡(GET /v1/users/me/payments/balance)+ 累计已完成充值 +
 * 自助充值开单(POST /v1/users/me/payments/orders:金额输入按 config 区间校验 + 选支付方式 →
 * 建 pending 单 + 展示人工支付指引)+ 最近订单(复用我的订单列表)。
 * wallet.ts 的 Tone('ok'|'warn'|'danger'|'muted')与 StatusBadge 的 BadgeTone 同集,直接传。
 * money 立场:开单为 money 敏感动作,提交前二次确认明示金额与渠道;manual 渠道是 pending 单不即时入账。
 */

export function WalletPage() {
  const [balanceCents, setBalanceCents] = useState<number | null>(null)
  const [orders, setOrders] = useState<UserOrder[]>([])
  const [loading, setLoading] = useState(true)
  const [balanceErr, setBalanceErr] = useState<string | null>(null)
  const [config, setConfig] = useState<PortalTopupConfig | null>(null)
  const [configErr, setConfigErr] = useState<string | null>(null)
  // 开单成功后刷新余额/订单:轮换 nonce 触发 effect 重拉。
  const [refreshNonce, setRefreshNonce] = useState(0)

  useEffect(() => {
    const ctrl = new AbortController()
    setLoading(true)
    // 余额、配置、订单各自独立加载:任一失败不连累其它块。
    getMyBalance(ctrl.signal)
      .then((r) => setBalanceCents(r.balance.amount_cents))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setBalanceErr(e instanceof ApiError ? `${e.message}(${e.code})` : '加载余额失败')
      })
    // 充值额度与支付方式配置(只读)。失败仅在该卡内提示,不阻断余额/订单。
    getPortalConfig(ctrl.signal)
      .then((r) => setConfig(r.config))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setConfigErr(e instanceof ApiError ? `${e.message}(${e.code})` : '加载充值配置失败')
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
  }, [refreshNonce])

  const reload = useCallback(() => setRefreshNonce((n) => n + 1), [])
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
            <span style={{ color: 'var(--hk-danger-600, var(--hk-danger))', fontSize: 13 }}>{balanceErr}</span>
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

      {/* 自助充值开单卡:金额输入(按 config 区间校验)+ 选支付方式 → 建 pending 单 + 展示人工支付指引。 */}
      <div style={{ ...card, gap: 'var(--hk-space-3)' }}>
        <span style={{ fontSize: 14, fontWeight: 600 }}>充值</span>
        {configErr ? (
          <span style={{ color: 'var(--hk-danger-600, var(--hk-danger))', fontSize: 13 }}>{configErr}</span>
        ) : config === null ? (
          <span style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>加载中…</span>
        ) : (
          <TopupForm config={config} onCreated={reload} />
        )}
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

/* ---------------- 自助充值开单表单(money 敏感) ---------------- */

function TopupForm({ config, onCreated }: { config: PortalTopupConfig; onCreated: () => void }) {
  const providers = config.providers
  const [amount, setAmount] = useState('')
  const [provider, setProvider] = useState(providers[0]?.provider ?? '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  // 开单成功后展示的人工支付指引(manual/taobao);成功路径才赋值。
  const [instruction, setInstruction] = useState<PortalProviderConfig | null>(null)

  // providers 异步到达后,若当前未选中任一渠道则默认选第一个。
  useEffect(() => {
    if (provider === '' && providers.length > 0) setProvider(providers[0].provider)
  }, [providers, provider])

  const cur = config.currency_code
  const sym = cur === 'USD' ? '$' : ''
  const suffix = cur === 'USD' ? '' : ` ${cur}`

  const submit = () => {
    setError(null)
    setInstruction(null)
    const parsed = parseTopupAmount(amount, config.min_topup_cents, config.max_topup_cents, cur)
    if (!parsed.ok) {
      setError(parsed.error)
      return
    }
    if (provider === '') {
      setError('请选择支付方式')
      return
    }
    const pretty = `${sym}${formatMoney(parsed.amountCents)}${suffix}`
    // money 敏感:二次确认明示金额 + 渠道 + 「建待支付单需人工确认入账」的性质。
    if (
      !window.confirm(
        `确认创建 ${pretty} 的充值订单?\n支付方式:${providerLabel(provider)}\n\n` +
          '创建后将得到一张「待支付」订单与人工支付指引,需按指引完成支付并等待管理员确认入账(不会即时扣款/到账)。',
      )
    ) {
      return
    }
    setBusy(true)
    createTopupOrder({ amount_cents: parsed.amountCents, provider })
      .then((res) => {
        // 成功:展示后端返回的该渠道支付指引,清空金额输入(下一单重新填)。
        setInstruction(res.payment_instruction)
        setAmount('')
        onCreated()
      })
      .catch((e: unknown) => {
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '创建充值订单失败')
      })
      .finally(() => setBusy(false))
  }

  if (providers.length === 0) {
    return <span style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>暂无可用支付渠道,请联系管理员。</span>
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        <span style={statLabel}>充值金额({cur})</span>
        <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
          <input
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            inputMode="decimal"
            placeholder="如 10.00"
            aria-label="充值金额"
            style={{ ...inp, maxWidth: 160 }}
          />
          <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>
            可充 {formatCentsRange(config.min_topup_cents, config.max_topup_cents, cur)}
          </span>
        </div>
        {config.preset_amount_cents.length > 0 && (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-2)', marginTop: 4 }}>
            {config.preset_amount_cents.map((c) => (
              <button
                key={c}
                type="button"
                onClick={() => setAmount((c / 100).toFixed(2))}
                style={presetBtn}
              >
                {sym}
                {formatMoney(c)}
                {suffix}
              </button>
            ))}
          </div>
        )}
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        <span style={statLabel}>支付方式</span>
        <select
          value={provider}
          onChange={(e) => setProvider(e.target.value)}
          aria-label="支付方式"
          style={{ ...inp, maxWidth: 220 }}
        >
          {providers.map((p) => (
            <option key={p.provider} value={p.provider}>
              {providerLabel(p.provider)}
            </option>
          ))}
        </select>
      </div>

      {error && (
        <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>
          {error}
        </div>
      )}

      <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)' }}>
        <button type="button" disabled={busy} onClick={submit} style={primaryBtn}>
          {busy ? '提交中…' : '创建充值订单'}
        </button>
        <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>提交前会二次确认金额与渠道。</span>
      </div>

      {/* 开单成功后:展示该渠道的人工支付指引(后端 payment_instruction)。 */}
      {instruction && (
        <div
          style={{
            display: 'flex',
            flexDirection: 'column',
            gap: 6,
            padding: 'var(--hk-space-3)',
            background: 'var(--hk-primary-50, var(--hk-primary-50))',
            border: '1px solid var(--hk-line)',
            borderRadius: 'var(--hk-radius-md)',
          }}
        >
          <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--hk-ink-900)' }}>
            订单已创建 · {providerLabel(instruction.provider)}
          </span>
          {instruction.instruction && (
            <span style={{ fontSize: 12, color: 'var(--hk-ink-700)', lineHeight: 1.6 }}>{instruction.instruction}</span>
          )}
          <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>
            可在「我的订单」查看该订单状态;在付款前可撤销该待支付订单。
          </span>
        </div>
      )}
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
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', minWidth: 120 }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer', flexShrink: 0 }
const presetBtn: React.CSSProperties = { height: 28, padding: '0 var(--hk-space-3)', fontSize: 12, fontFamily: 'var(--hk-font-mono)', cursor: 'pointer', borderRadius: 'var(--hk-radius-md)', border: '1px solid var(--hk-line)', background: 'var(--hk-surface-sunken)', color: 'var(--hk-ink-700)' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
const tdNum: React.CSSProperties = { ...td, textAlign: 'right', fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
