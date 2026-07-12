import { useState } from 'react'
import { ApiError } from '../../lib/api'
import { downloadExportCsv, type ExportKind } from './api'
import { buildExportRange, defaultExportRange, type ExportRangeForm } from './ordersadmin'
import { errBox, Field, ghostBtn, inp, panel, primaryBtn } from './ui'

/*
 * CSV 导出工具栏。四个只读导出端点(payments / orders / refunds / usage 的 export.csv),
 * 走 admin token 的 blob 下载。注意:这些端点【不接受 tenant_id】,租户由 admin 凭据
 * ScopeTenantID 推导;from/to 为必填 RFC3339(前端用 datetime-local + buildExportRange 校验),
 * status 仅 payments/orders 接受(refunds / usage 不消费)。
 * usage 用量明细导出真码 export.go:70 路由 + export.go:141 NewUsageExportHandler。
 */
const KINDS: Array<{ kind: ExportKind; label: string }> = [
  { kind: 'payments', label: '支付订单 CSV' },
  { kind: 'orders', label: '订单 CSV' },
  { kind: 'refunds', label: '退款 CSV' },
  { kind: 'usage', label: '用量明细 CSV' },
]

export function ExportToolbar() {
  const [range, setRange] = useState<ExportRangeForm>(() => defaultExportRange(new Date()))
  const [busy, setBusy] = useState<ExportKind | null>(null)
  const [error, setError] = useState<string | null>(null)

  const onExport = async (kind: ExportKind) => {
    const built = buildExportRange(range.from, range.to)
    if ('error' in built) {
      setError(built.error)
      return
    }
    setError(null)
    setBusy(kind)
    try {
      await downloadExportCsv(kind, built.from, built.to)
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '导出失败')
    } finally {
      setBusy(null)
    }
  }

  return (
    <div
      style={{
        ...panel,
        padding: 'var(--hk-space-4)',
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--hk-space-3)',
      }}
    >
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <h3 style={{ fontSize: 14, margin: 0, color: 'var(--hk-ink-700)' }}>CSV 导出(只读)</h3>
        <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>
          按 admin 凭据所属租户导出,时间窗 ≤ 366 天
        </span>
      </div>
      <div
        style={{
          display: 'grid',
          gridTemplateColumns: 'repeat(auto-fit,minmax(180px,1fr))',
          gap: 'var(--hk-space-3)',
          alignItems: 'flex-end',
        }}
      >
        <Field label="起始时间(必填)">
          <input
            type="datetime-local"
            value={range.from}
            onChange={(e) => setRange((r) => ({ ...r, from: e.target.value }))}
            style={inp}
          />
        </Field>
        <Field label="截止时间(必填)">
          <input
            type="datetime-local"
            value={range.to}
            onChange={(e) => setRange((r) => ({ ...r, to: e.target.value }))}
            style={inp}
          />
        </Field>
      </div>
      {error && <div style={errBox}>{error}</div>}
      <div style={{ display: 'flex', gap: 'var(--hk-space-2)', flexWrap: 'wrap' }}>
        {KINDS.map((k) => (
          <button
            key={k.kind}
            type="button"
            disabled={busy !== null}
            onClick={() => void onExport(k.kind)}
            style={k.kind === 'payments' ? primaryBtn : ghostBtn}
          >
            {busy === k.kind ? '导出中…' : k.label}
          </button>
        ))}
      </div>
    </div>
  )
}
