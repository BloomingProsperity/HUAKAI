import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { adjustBalance } from './api'
import {
  buildAdjustmentRequest,
  directionLabel,
  newAdjustmentKey,
  validateAdjustment,
  type AdjustDirection,
} from './balance'
import type { UserDetail } from './detail'

/*
 * 管理员手动调额卡(money 敏感,运维台)。把 adminhttp/balance_credit_handler.go 的
 * POST /admin/v1/balances/adjustments 接到用户详情余额卡旁。
 *
 * 设计要点:
 *   - amount 符号即方向:运维只输正的绝对值,方向由「加款/扣款」选择决定(扣款=负号)。
 *   - 扣款当前被后端 gated(ErrAdminDebitNotSupported),UI 明确告知可能被拒,但不隐藏入口
 *     (保留 Feature,后端放开即可用),避免运维误以为前端缺功能。
 *   - tenant_id 用户详情体不返回,故需运维显式提供(默认 1=单租户运营者租户,可改),
 *     与内容审核页同款做法(platform_admin 必须指明目标租户)。
 *   - reason 必填(审计);idempotency_key 同一次提交意图复用同一 key,重复点击合并为一次入账。
 *   - 二次确认明确展示「将给 {email} {加/扣} {金额} 美元」,money 影响一目了然。
 */
export function UserBalanceAdjust({ user, onChanged }: { user: UserDetail; onChanged: () => void }) {
  const [tenantInput, setTenantInput] = useState('1')
  const [direction, setDirection] = useState<AdjustDirection>('credit')
  const [amount, setAmount] = useState('')
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [ok, setOk] = useState<string | null>(null)
  // 幂等键:每完成一次成功入账后轮换,保证下一次是新意图;失败保留以便重试合并。
  const [idemKey, setIdemKey] = useState(() => newAdjustmentKey())

  const submit = () => {
    setError(null)
    setOk(null)
    const tenantId = Number(tenantInput.trim())
    const v = validateAdjustment(tenantId, user.id, direction, amount, reason)
    if (!v.ok) {
      setError(v.error)
      return
    }
    // 二次确认:money 敏感,明确展示对象 + 方向 + 金额。
    const verb = directionLabel(direction)
    if (
      !window.confirm(
        `确认将给 ${user.email} ${verb} ${v.magnitude} 美元?\n原因:${reason.trim()}\n\n` +
          (direction === 'debit'
            ? '注意:扣款当前可能被后端拒绝(暂未开放手动扣款)。'
            : '该操作会立即变更用户余额并记入台账。'),
      )
    ) {
      return
    }
    setBusy(true)
    const body = buildAdjustmentRequest(tenantId, user.id, v.signedAmount, reason, idemKey)
    adjustBalance(body)
      .then((res) => {
        setOk(`已${verb} ${v.magnitude} 美元,当前净余额 ${res.net_balance} ${res.currency_code}`)
        setAmount('')
        setReason('')
        // 成功后轮换幂等键,准备下一次独立调额。
        setIdemKey(newAdjustmentKey())
        onChanged()
      })
      .catch((e: unknown) => {
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '调额失败')
      })
      .finally(() => setBusy(false))
  }

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <h2 style={{ fontSize: 15, color: 'var(--hk-ink-700)' }}>手动调额(money 敏感)</h2>

      {error && <Banner tone="danger">{error}</Banner>}
      {ok && <Banner tone="ok">{ok}</Banner>}

      <div style={card}>
        <Row label="目标租户 ID(tenant_id)" hint="用户详情不含租户,需指明;单租户运营者通常为 1">
          <input
            value={tenantInput}
            onChange={(e) => setTenantInput(e.target.value)}
            inputMode="numeric"
            placeholder="如 1"
            style={{ ...inp, maxWidth: 120 }}
          />
        </Row>

        <Row label="方向" hint="加款=贷记余额;扣款=借记余额(扣款暂可能被后端拒)">
          <select value={direction} onChange={(e) => setDirection(e.target.value as AdjustDirection)} style={{ ...inp, maxWidth: 140 }}>
            <option value="credit">加款(+)</option>
            <option value="debit">扣款(−)</option>
          </select>
        </Row>

        <Row label="金额(美元)" hint="正数,最多 2 位小数">
          <input
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            inputMode="decimal"
            placeholder="如 10.00"
            style={{ ...inp, maxWidth: 160 }}
          />
        </Row>

        <Row label="原因(审计必填)" hint="记入审计台账,务必写清">
          <input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="如 客服补偿 / 误扣回退" style={inp} />
        </Row>

        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center' }}>
          <button type="button" disabled={busy} onClick={submit} style={direction === 'debit' ? dangerSolid : primaryBtn}>
            {busy ? '提交中…' : direction === 'debit' ? '确认扣款' : '确认加款'}
          </button>
          <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>提交前会二次确认。</span>
        </div>
      </div>
    </section>
  )
}

function Row({ label, hint, children }: { label: string; hint?: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 'var(--hk-space-2)' }}>
        <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--hk-ink-700)' }}>{label}</span>
        {hint && <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>{hint}</span>}
      </div>
      <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center' }}>{children}</div>
    </div>
  )
}

function Banner({ tone, children }: { tone: 'danger' | 'ok'; children: React.ReactNode }) {
  const danger = tone === 'danger'
  return (
    <div
      style={{
        padding: 'var(--hk-space-2) var(--hk-space-3)',
        borderRadius: 'var(--hk-radius-md)',
        fontSize: 13,
        color: danger ? '#8f322a' : 'var(--hk-primary-700)',
        background: danger ? '#fbe9e7' : 'var(--hk-primary-50, #eef7f2)',
        border: `1px solid ${danger ? '#f2cdc8' : 'var(--hk-line)'}`,
      }}
    >
      {children}
    </div>
  )
}

const card: React.CSSProperties = {
  display: 'flex',
  flexDirection: 'column',
  gap: 'var(--hk-space-3)',
  padding: 'var(--hk-space-4)',
  background: 'var(--hk-surface)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-lg)',
}
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', flex: 1, minWidth: 120 }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer', flexShrink: 0 }
const dangerSolid: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid #c0392b', borderRadius: 'var(--hk-radius-md)', background: '#c0392b', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer', flexShrink: 0 }
