import { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatCard } from '../../ui/StatCard'
import { getDashboard } from './api'
import { mapOrderDashboardCards } from './ordersadmin'
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
    setStats(null)
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
  const cards = mapOrderDashboardCards(stats)

  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(auto-fit,minmax(160px,1fr))',
        gap: 'var(--hk-space-3)',
      }}
    >
      {cards.map((c) => (
        <StatCard key={c.label} label={c.label} value={c.value} hint={c.hint} tone={c.tone} />
      ))}
    </div>
  )
}
