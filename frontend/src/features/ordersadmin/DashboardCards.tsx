import { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { getDashboard } from './api'
import { formatCents } from './ordersadmin'
import { errBox } from './ui'
import type { DashboardStats } from './types'

/*
 * 订单台仪表盘汇总卡(页顶)。读 GET /v1/admin/payments/dashboard?tenant_id=,
 * 展示总额 / 总单数 / 今日单数 / 笔均额(均后端 *_cents,前端只读展示不动钱)。
 * tenant 变化时自动重拉;无 tenant 不渲染。
 */
export function DashboardCards({ tenantId }: { tenantId: number }) {
  const [stats, setStats] = useState<DashboardStats | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (tenantId <= 0) {
      setStats(null)
      return
    }
    const ctrl = new AbortController()
    setError(null)
    getDashboard(tenantId, ctrl.signal)
      .then((s) => setStats(s))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载仪表盘失败')
      })
    return () => ctrl.abort()
  }, [tenantId])

  if (tenantId <= 0) return null
  if (error) return <div style={errBox}>{error}</div>
  if (!stats) return null

  // 笔均额币种未由 dashboard 给出,展示时用空币种(纯数字);各订单币种以列表为准。
  const cards: Array<{ label: string; value: string }> = [
    { label: '累计金额', value: formatCents(stats.total_amount_cents, '') },
    { label: '累计订单数', value: String(stats.total_count) },
    { label: '今日订单数', value: String(stats.today_count) },
    { label: '笔均金额', value: formatCents(stats.average_amount_cents, '') },
  ]

  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fit,minmax(160px,1fr))',
        gap: 'var(--hk-space-3)',
      }}
    >
      {cards.map((c) => (
        <div
          key={c.label}
          style={{
            background: 'var(--hk-surface)',
            border: '1px solid var(--hk-line)',
            borderRadius: 'var(--hk-radius-lg)',
            boxShadow: 'var(--hk-shadow-1)',
            padding: 'var(--hk-space-4)',
            display: 'flex',
            flexDirection: 'column',
            gap: 4,
          }}
        >
          <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>{c.label}</span>
          <span style={{ fontSize: 20, fontWeight: 700, color: 'var(--hk-ink-900)', fontFamily: 'var(--hk-font-mono)' }}>
            {c.value}
          </span>
        </div>
      ))}
    </div>
  )
}
