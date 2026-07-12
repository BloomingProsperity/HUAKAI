import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { createOrderForUser } from './api'
import {
  buildCreateOrderRequest,
  EMPTY_CREATE_ORDER_FORM,
  formatCents,
  PROVIDER_KINDS,
  providerKindLabel,
  type CreateOrderForm,
} from './ordersadmin'
import { errBox, Field, ghostBtn, inp, panel, primaryBtn } from './ui'

/*
 * 代客建单(运营台 · admin · money 敏感)。把 POST /v1/admin/payments(handler.go:249
 * newAdminCreateOrderHandler)接到运营台,供运营替指定用户创建一张充值挂单。
 *
 * money 姿态(§3):
 *   - 建单本身只创建 pending 挂单(不入账、不动余额);真正给用户加额发生在后续
 *     「确认到账并履约」动作。但仍按 money 敏感处理:提交前 window.confirm 明示
 *     「给【租户/用户】创建【金额】【渠道】充值挂单」,让运营对充值对象与金额一目了然。
 *   - out_trade_no 由前端按建单意图稳定生成(防双账幂等);currency 固定 USD(账本仅 USD)。
 *   - 仅支持充值单(topup);订阅单金额来自套餐快照、需 PG store,不在此覆盖。
 * 成功后清空金额输入(finally 不留意图),并把新建订单号回显给运营去「确认到账」。
 */
export function AdminCreateOrder({ onCreated }: { onCreated?: () => void }) {
  const [form, setForm] = useState<CreateOrderForm>(EMPTY_CREATE_ORDER_FORM)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [ok, setOk] = useState<string | null>(null)

  const set = <K extends keyof CreateOrderForm>(k: K, v: CreateOrderForm[K]) =>
    setForm((f) => ({ ...f, [k]: v }))

  const submit = () => {
    setError(null)
    setOk(null)
    const built = buildCreateOrderRequest(form, Date.now())
    if ('error' in built) {
      setError(built.error)
      return
    }
    // 二次确认(money 敏感):明示给谁(租户/用户)创建多少金额、何种渠道的充值挂单。
    if (
      !window.confirm(
        `确认为租户 #${built.tenantId} 的用户 #${built.userId} 创建一张` +
          `${formatCents(built.amountCents, 'USD')} 的${providerKindLabel(built.providerKind)}充值挂单?\n\n` +
          '说明:此操作仅创建待支付挂单(不会立即给用户加额),' +
          '真正到账需在订单详情里执行「确认到账并履约」。',
      )
    ) {
      return
    }
    setBusy(true)
    createOrderForUser({
      tenant_id: built.tenantId,
      user_id: built.userId,
      amount_cents: built.amountCents,
      out_trade_no: built.outTradeNo,
      provider_kind: built.providerKind,
      order_kind: 'topup',
    })
      .then((resp) => {
        const replay = resp.idempotent ? '(幂等命中:该意图此前已建单)' : ''
        setOk(`已创建充值挂单 #${resp.order.id}(${resp.order.out_trade_no})${replay}。请在订单列表中查询并「确认到账并履约」。`)
        // 成功后清空金额,避免误重复建单;租户/用户/渠道保留,便于连续给同一用户建单。
        set('amount', '')
        onCreated?.()
      })
      .catch((e: unknown) => {
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '建单失败')
      })
      .finally(() => setBusy(false))
  }

  return (
    <div style={{ ...panel, padding: 'var(--hk-space-5)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', maxWidth: 560 }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
        <h3 style={{ fontSize: 16, margin: 0 }}>代客建单(money 敏感)</h3>
        <p style={{ margin: 0, fontSize: 12, color: 'var(--hk-ink-500)' }}>
          替指定用户创建一张充值挂单(仅 topup,币种 USD)。建单只生成待支付挂单,真正到账需后续「确认到账并履约」。提交前会二次确认。
        </p>
      </header>

      <Field label="租户 ID(必填)">
        <input value={form.tenantId} onChange={(e) => set('tenantId', e.target.value)} placeholder="如 1" inputMode="numeric" style={inp} />
      </Field>
      <Field label="用户 ID(必填)">
        <input value={form.userId} onChange={(e) => set('userId', e.target.value)} placeholder="目标用户" inputMode="numeric" style={inp} />
      </Field>
      <Field label="金额(美元 USD)">
        <input value={form.amount} onChange={(e) => set('amount', e.target.value)} placeholder="如 10.00" inputMode="decimal" style={inp} />
      </Field>
      <Field label="支付渠道">
        <select value={form.providerKind} onChange={(e) => set('providerKind', e.target.value as CreateOrderForm['providerKind'])} style={inp}>
          {PROVIDER_KINDS.map((p) => (
            <option key={p.value} value={p.value}>
              {p.label}
            </option>
          ))}
        </select>
      </Field>

      {error && <div style={errBox}>{error}</div>}
      {ok && <div style={{ fontSize: 13, color: 'var(--hk-primary-700)' }}>{ok}</div>}

      <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center' }}>
        <button type="button" disabled={busy} onClick={submit} style={primaryBtn}>
          {busy ? '建单中…' : '创建充值挂单'}
        </button>
        <button
          type="button"
          disabled={busy}
          onClick={() => {
            setForm(EMPTY_CREATE_ORDER_FORM)
            setError(null)
            setOk(null)
          }}
          style={ghostBtn}
        >
          重置
        </button>
      </div>
    </div>
  )
}
