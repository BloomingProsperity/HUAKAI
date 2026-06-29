import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { approveRefundRequest, listRefundRequests, rejectRefundRequest } from './api'
import { refundRequestStatusLabel, refundRequestStatusTone } from './ordersadmin'
import {
  dangerBtn,
  errBox,
  Empty,
  fmt,
  ghostBtn,
  inp,
  panel,
  primaryBtn,
  td,
  tdNum,
  th,
} from './ui'
import type { RefundRequestView } from './types'

/*
 * 退款工单 Tab(运营台)。列出某租户【待审批】的退款工单(用户发起的退款申请),
 * 支持逐条「通过」/「驳回」。
 *   - 通过(approve):money 敏感 —— 后端会以订单原额走 RefundOrder 资金路径并扣减用户余额,
 *     故必须二次确认。
 *   - 驳回(reject):不动钱,仅置状态 rejected,可附原因。
 * 端点:GET/POST /v1/admin/payments/refund-requests*(admin token)。
 */
export function RefundRequestsTab({ tenantId }: { tenantId: number }) {
  const [rows, setRows] = useState<RefundRequestView[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [nonce, setNonce] = useState(0)

  const load = useCallback(
    (signal: AbortSignal) => {
      if (tenantId <= 0) return
      setLoading(true)
      setError(null)
      listRefundRequests(tenantId, signal)
        .then((resp) => setRows(resp.refund_requests ?? []))
        .catch((e: unknown) => {
          if (signal.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载退款工单失败')
        })
        .finally(() => {
          if (!signal.aborted) setLoading(false)
        })
    },
    [tenantId],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, nonce])

  if (tenantId <= 0) {
    return (
      <div style={panel}>
        <Empty>请先在「订单列表」Tab 填写并查询租户 ID,退款工单按该租户加载。</Empty>
      </div>
    )
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <p style={{ margin: 0, fontSize: 13, color: 'var(--hk-ink-500)' }}>
          租户 #{tenantId} 的待审批退款工单。通过将以订单原额退款并扣减用户余额。
        </p>
        <button type="button" onClick={() => setNonce((n) => n + 1)} disabled={loading} style={ghostBtn}>
          刷新
        </button>
      </div>
      {error && <div style={errBox}>{error}</div>}
      <div style={panel}>
        {loading && rows.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : rows.length === 0 ? (
          <Empty>该租户暂无待审批退款工单。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['工单', '订单', '用户', '状态', '原因', '创建时间', ''].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((rr) => (
                  <RefundRequestRow
                    key={rr.id}
                    rr={rr}
                    tenantId={tenantId}
                    onDone={() => setNonce((n) => n + 1)}
                  />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  )
}

function RefundRequestRow({
  rr,
  tenantId,
  onDone,
}: {
  rr: RefundRequestView
  tenantId: number
  onDone: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [actErr, setActErr] = useState<string | null>(null)
  const [rejecting, setRejecting] = useState(false)
  const [reason, setReason] = useState('')

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    setActErr(null)
    try {
      await fn()
      onDone()
    } catch (e) {
      setActErr(e instanceof ApiError ? `${e.message}(${e.code})` : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  const onApprove = () => {
    // money 敏感 + 破坏性:二次确认。
    if (
      !window.confirm(
        `确认通过退款工单 #${rr.id}?\n将以订单 #${rr.order_id} 原额退款并扣减用户 #${rr.user_id ?? '?'} 余额,此操作动钱不可轻易撤销。`,
      )
    ) {
      return
    }
    void run(() => approveRefundRequest(rr.id, tenantId))
  }

  const onReject = () => {
    void run(() => rejectRefundRequest(rr.id, tenantId, reason)).then(() => setRejecting(false))
  }

  const pending = rr.status === 'pending'

  return (
    <>
      <tr style={{ borderTop: '1px solid var(--hk-line)' }}>
        <td style={tdNum}>#{rr.id}</td>
        <td style={tdNum}>#{rr.order_id}</td>
        <td style={tdNum}>{rr.user_id ?? '—'}</td>
        <td style={td}>
          <StatusBadge tone={refundRequestStatusTone(rr.status)}>
            {refundRequestStatusLabel(rr.status)}
          </StatusBadge>
        </td>
        <td style={td}>{rr.reason || '—'}</td>
        <td style={td}>{fmt(rr.created_at)}</td>
        <td style={{ ...td, textAlign: 'right', whiteSpace: 'nowrap' }}>
          {pending ? (
            <div style={{ display: 'inline-flex', gap: 'var(--hk-space-2)' }}>
              <button type="button" disabled={busy} onClick={onApprove} style={primaryBtn}>
                通过退款
              </button>
              <button
                type="button"
                disabled={busy}
                onClick={() => setRejecting((v) => !v)}
                style={dangerBtn}
              >
                驳回
              </button>
            </div>
          ) : (
            <span style={{ color: 'var(--hk-ink-500)' }}>已处置</span>
          )}
        </td>
      </tr>
      {(rejecting || actErr) && pending && (
        <tr>
          <td colSpan={7} style={{ ...td, background: 'var(--hk-surface-sunken)' }}>
            {actErr && <div style={{ ...errBox, marginBottom: 'var(--hk-space-2)' }}>{actErr}</div>}
            {rejecting && (
              <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center' }}>
                <input
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                  placeholder="驳回原因(可选,记入工单)"
                  style={{ ...inp, maxWidth: 360 }}
                />
                <button type="button" disabled={busy} onClick={onReject} style={dangerBtn}>
                  确认驳回
                </button>
                <button type="button" disabled={busy} onClick={() => setRejecting(false)} style={ghostBtn}>
                  取消
                </button>
              </div>
            )}
          </td>
        </tr>
      )}
    </>
  )
}
