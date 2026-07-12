import { useCallback, useEffect, useRef, useState } from 'react'
import { fetchSiteConfig } from '../../auth/siteConfig'
import { ApiError } from '../../lib/api'
import { StatusBadge, type BadgeTone } from '../../ui/StatusBadge'
import { listRedemptions, redeemVoucher } from './api'
import {
  buildRedeemRequest,
  formatMoney,
  friendlyRedeemError,
  isRateLimited,
  newIdempotencyKey,
  summarizeRedeem,
  validateCode,
} from './redeem'
import type { RedemptionHistoryItem } from './types'

/*
 * 兑换码 · 我的兑换(user 壳)。
 * 上:券码输入 + 提交(带 idempotency_key, 重复点击合并入账)+ 限流软提示 + 到账金额。
 * 下:兑换历史(GET /v1/me/voucher-redemptions)。money-adjacent 只读展示 + 兑换动作。
 */
export function RedeemPage() {
  const [code, setCode] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [success, setSuccess] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [rateLimited, setRateLimited] = useState(false)
  const [history, setHistory] = useState<RedemptionHistoryItem[]>([])
  const [historyLoading, setHistoryLoading] = useState(true)
  const [historyError, setHistoryError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)
  // promo 总开关:默认 true 避免加载时闪烁;仅站点配置**显式** false 才关闭兑换入口(行为保持)。
  const [promoEnabled, setPromoEnabled] = useState(true)

  useEffect(() => {
    let alive = true
    fetchSiteConfig()
      .then((cfg) => {
        if (alive) setPromoEnabled(cfg.promoEnabled)
      })
      .catch(() => {
        /* 取站点配置失败不拦兑换(后端门控仍是最终防线) */
      })
    return () => {
      alive = false
    }
  }, [])

  // 同一次输入意图绑定同一个幂等键:输入框内容变化前复用, 提交成功后轮换。
  // 这样「连点提交」走同一个 key 被后端合并, 而「兑完再兑下一张」拿到新 key。
  const idemKeyRef = useRef<string>(newIdempotencyKey())

  const loadHistory = useCallback((signal: AbortSignal) => {
    setHistoryLoading(true)
    setHistoryError(null)
    listRedemptions(100, signal)
      .then((resp) => setHistory(resp.redemptions ?? []))
      .catch((e: unknown) => {
        if (signal.aborted) return
        setHistoryError(e instanceof ApiError ? friendlyRedeemError(e.code, e.message) : '加载兑换历史失败')
      })
      .finally(() => {
        if (!signal.aborted) setHistoryLoading(false)
      })
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    loadHistory(ctrl.signal)
    return () => ctrl.abort()
  }, [loadHistory, refreshNonce])

  const submit = async () => {
    const invalid = validateCode(code)
    if (invalid) {
      setError(invalid)
      setSuccess(null)
      setRateLimited(false)
      return
    }
    setSubmitting(true)
    setError(null)
    setSuccess(null)
    setRateLimited(false)
    try {
      const req = buildRedeemRequest(code, idemKeyRef.current)
      const result = await redeemVoucher(req)
      setSuccess(summarizeRedeem(result))
      setCode('')
      // 成功后轮换幂等键, 下一张券是独立一次。
      idemKeyRef.current = newIdempotencyKey()
      setRefreshNonce((n) => n + 1)
    } catch (e) {
      if (e instanceof ApiError) {
        setError(friendlyRedeemError(e.code, e.message))
        setRateLimited(isRateLimited(e.code))
      } else {
        setError('兑换失败, 请稍后再试')
      }
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>兑换码</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          输入兑换码为账户充值或开通订阅。每张码兑换后即生效, 不可重复。
        </p>
      </header>

      {/* 兑换卡片 */}
      <section
        style={{
          background: 'var(--hk-surface)',
          border: '1px solid var(--hk-line)',
          borderRadius: 'var(--hk-radius-lg)',
          boxShadow: 'var(--hk-shadow-1)',
          padding: 'var(--hk-space-5)',
          display: 'flex',
          flexDirection: 'column',
          gap: 'var(--hk-space-3)',
          maxWidth: 560,
        }}
      >
        <label htmlFor="redeem-code" style={{ fontSize: 13, fontWeight: 600, color: 'var(--hk-ink-700)' }}>
          兑换码
        </label>
        {!promoEnabled && (
          <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-warn-bg, #fff8e1)', border: '1px solid var(--hk-warn, #b8860b)', color: 'var(--hk-ink-700)' }}>
            兑换功能当前已由运营者关闭,暂不可兑换。
          </div>
        )}
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'stretch' }}>
          <input
            id="redeem-code"
            value={code}
            disabled={submitting || !promoEnabled}
            placeholder="粘贴或输入兑换码"
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setCode(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !submitting) submit()
            }}
            style={{
              flex: 1,
              height: 38,
              padding: '0 var(--hk-space-3)',
              border: '1px solid var(--hk-line)',
              borderRadius: 'var(--hk-radius-md)',
              background: 'var(--hk-surface)',
              color: 'var(--hk-ink-900)',
              fontSize: 14,
              fontFamily: 'var(--hk-font-mono)',
              letterSpacing: '0.04em',
            }}
          />
          <button type="button" disabled={submitting || !promoEnabled} onClick={submit} style={submitBtn}>
            {submitting ? '兑换中…' : '兑换'}
          </button>
        </div>

        {success && (
          <div
            style={{
              padding: 'var(--hk-space-3)',
              borderRadius: 'var(--hk-radius-md)',
              fontSize: 13,
              color: 'var(--hk-primary-600)',
              background: 'var(--hk-primary-50)',
              border: '1px solid var(--hk-primary-100)',
            }}
          >
            {success}
          </div>
        )}
        {error && (
          <div
            style={{
              padding: 'var(--hk-space-3)',
              borderRadius: 'var(--hk-radius-md)',
              fontSize: 13,
              // 限流是「稍候」语气, 用 info/暖黄而非危险红, 避免吓到用户。
              color: rateLimited ? 'var(--hk-warn)' : 'var(--hk-danger)',
              background: rateLimited ? 'var(--hk-warn-soft)' : 'var(--hk-danger-soft)',
              border: rateLimited ? '1px solid var(--hk-warn-soft)' : '1px solid var(--hk-danger-soft)',
            }}
          >
            {error}
          </div>
        )}
      </section>

      {/* 兑换历史 */}
      <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
        <h2 style={{ fontSize: 15, margin: 0, color: 'var(--hk-ink-700)' }}>兑换历史</h2>
        <div
          style={{
            background: 'var(--hk-surface)',
            border: '1px solid var(--hk-line)',
            borderRadius: 'var(--hk-radius-lg)',
            boxShadow: 'var(--hk-shadow-1)',
            overflow: 'hidden',
          }}
        >
          {historyError ? (
            <Empty>{historyError}</Empty>
          ) : historyLoading && history.length === 0 ? (
            <Empty>加载中…</Empty>
          ) : history.length === 0 ? (
            <Empty>还没有兑换记录。兑换成功后会显示在这里。</Empty>
          ) : (
            <div style={{ overflowX: 'auto' }}>
              <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
                <thead>
                  <tr>
                    {['到账金额', '状态', '兑换时间', '券 ID'].map((h) => (
                      <th key={h} style={th}>
                        {h}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {history.map((row, i) => (
                    <tr key={`${row.voucher_id}-${row.redeemed_at}-${i}`} style={{ borderTop: '1px solid var(--hk-line)' }}>
                      <td style={{ ...td, fontWeight: 600, color: 'var(--hk-ink-900)' }}>
                        {formatMoney(row.amount_cents, row.currency_code)}
                      </td>
                      <td style={td}>
                        <StatusBadge tone={redemptionTone(row.status)}>{redemptionLabel(row.status)}</StatusBadge>
                      </td>
                      <td style={td}>{fmtTime(row.redeemed_at)}</td>
                      <td style={td}>
                        <code style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>#{row.voucher_id}</code>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </section>
    </div>
  )
}

function redemptionTone(status: string): BadgeTone {
  switch (status) {
    case 'redeemed':
    case 'success':
    case 'completed':
      return 'ok'
    case 'reversed':
    case 'refunded':
      return 'warn'
    case 'failed':
      return 'danger'
    default:
      // 后端历史项 status 多为「已兑换」语义, 缺省按成功展示。
      return status ? 'muted' : 'ok'
  }
}

function redemptionLabel(status: string): string {
  switch (status) {
    case 'redeemed':
    case 'success':
    case 'completed':
      return '已到账'
    case 'reversed':
      return '已冲正'
    case 'refunded':
      return '已退回'
    case 'failed':
      return '失败'
    case '':
      return '已到账'
    default:
      return status
  }
}

function fmtTime(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString('zh-CN', { hour12: false })
}

function Empty({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>
      {children}
    </div>
  )
}

const th: React.CSSProperties = {
  textAlign: 'left',
  padding: 'var(--hk-space-3) var(--hk-space-4)',
  fontSize: 12,
  fontWeight: 600,
  color: 'var(--hk-ink-500)',
  background: 'var(--hk-surface-sunken)',
  whiteSpace: 'nowrap',
}
const td: React.CSSProperties = {
  padding: 'var(--hk-space-3) var(--hk-space-4)',
  verticalAlign: 'middle',
  whiteSpace: 'nowrap',
  color: 'var(--hk-ink-700)',
}
const submitBtn: React.CSSProperties = {
  height: 38,
  padding: '0 var(--hk-space-5)',
  border: '1px solid var(--hk-primary-600)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-primary-500)',
  color: '#fff',
  fontSize: 14,
  fontWeight: 600,
  cursor: 'pointer',
  flexShrink: 0,
}
