import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { approveRefundRequest, listRefundRequests, rejectRefundRequest } from './api'
import { mapRefundRequestRows, type RefundRequestTableRow } from './ordersadmin'
import {
  dangerBtn,
  errBox,
  ghostBtn,
  inp,
  panel,
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
  const [busyId, setBusyId] = useState<number | null>(null)
  const [rejectingId, setRejectingId] = useState<number | null>(null)
  const [reason, setReason] = useState('')

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

  const run = async (id: number, fn: () => Promise<unknown>) => {
    setBusyId(id)
    setError(null)
    try {
      await fn()
      setRejectingId(null)
      setReason('')
      setNonce((n) => n + 1)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '操作失败')
    } finally {
      setBusyId(null)
    }
  }

  const onApprove = (row: RefundRequestTableRow) => {
    const rr = row.source
    // money 敏感 + 破坏性:保留既有二次确认,确认后才调用原审批端点。
    if (
      !window.confirm(
        `确认通过退款工单 #${rr.id}?\n将以订单 #${rr.order_id} 原额退款并扣减用户 #${rr.user_id ?? '?'} 余额,此操作动钱不可轻易撤销。`,
      )
    ) {
      return
    }
    void run(rr.id, () => approveRefundRequest(rr.id, tenantId))
  }

  const rowsForTable = mapRefundRequestRows(rows)
  const columns: DataListColumn<RefundRequestTableRow>[] = [
    { key: 'request', label: '工单', render: (row) => <span className="hk-mono">#{row.id}</span> },
    { key: 'order', label: '订单', render: (row) => <span className="hk-mono">#{row.orderId}</span> },
    { key: 'user', label: '用户', render: (row) => <span className="hk-mono">{row.userId ?? '—'}</span> },
    {
      key: 'status',
      label: '状态',
      badge: true,
      render: (row) => <StatusBadge tone={row.statusTone}>{row.statusText}</StatusBadge>,
    },
    { key: 'reason', label: '原因', render: (row) => row.reason },
    { key: 'created-at', label: '创建时间', render: (row) => <span className="hk-mono">{row.createdAt}</span> },
  ]

  if (tenantId <= 0) {
    return (
      <div style={panel}>
        <EmptyState title="尚未选择租户" hint="请先在「订单列表」Tab 填写并查询租户 ID，退款工单按该租户加载。" />
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
          <EmptyState title="正在加载退款工单" hint="请稍候。" />
        ) : rows.length === 0 ? (
          <EmptyState title="暂无待审批退款工单" hint="该租户当前没有需要运营处置的退款申请。" tone="positive" />
        ) : (
          <DataListTable
            label="退款工单列表"
            rows={rowsForTable}
            rowKey={(row) => row.id}
            columns={columns}
            actions={[
              {
                label: '通过退款',
                onClick: onApprove,
                disabled: (row) => row.source.status !== 'pending' || busyId !== null,
              },
              {
                label: '驳回',
                tone: 'danger',
                onClick: (row) => {
                  setRejectingId((current) => current === row.id ? null : row.id)
                  setReason('')
                },
                disabled: (row) => row.source.status !== 'pending' || busyId !== null,
              },
            ]}
          />
        )}
      </div>
      {rejectingId !== null && (
        <section style={{ ...panel, padding: 'var(--hk-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
          <strong style={{ fontSize: 13 }}>驳回退款工单 #{rejectingId}</strong>
          <div style={{ display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center', flexWrap: 'wrap' }}>
            <input
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="驳回原因(可选,记入工单)"
              style={{ ...inp, maxWidth: 360 }}
            />
            <button
              type="button"
              disabled={busyId !== null}
              onClick={() => { void run(rejectingId, () => rejectRefundRequest(rejectingId, tenantId, reason)) }}
              style={dangerBtn}
            >
              确认驳回
            </button>
            <button type="button" disabled={busyId !== null} onClick={() => setRejectingId(null)} style={ghostBtn}>
              取消
            </button>
          </div>
        </section>
      )}
    </div>
  )
}
