import { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatCard } from '../../ui/StatCard'
import { mapAccountStats } from './accounts'
import { getProviderAccountHealthSummary } from './api'
import type { AccountHealthSummary } from './types'

/*
 * 账号池巡检概览卡(B9)。GET /provider-accounts/health-summary 跨整个租户池聚合,
 * 一眼看清总数/启用/停用/需关注 + 各健康态计数。不是分页统计,是全池真值。
 * 需关注(non-healthy 或被停用)用 warn 高亮;健康态 pill 精确联动当前游标页筛选。
 */

/** health_state 中文标签(枚举以后端 CHECK 为准)。 */
export function healthStateLabel(state: string): string {
  const map: Record<string, string> = {
    healthy: '健康',
    throttled: '限流中',
    cooldown: '冷却中',
    revoked: '已吊销',
  }
  return map[state] ?? state
}

/** 健康态是否属于"需关注"(非 healthy)。 */
export function isAttentionState(state: string): boolean {
  return state !== 'healthy'
}

export function HealthSummaryCard({
  tenantId,
  activeHealthState = '',
  refreshNonce = 0,
  onHealthStateChange,
}: {
  tenantId: number | null
  activeHealthState?: string
  refreshNonce?: number
  onHealthStateChange?: (state: string) => void
}) {
  const [data, setData] = useState<AccountHealthSummary | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    setError(null)
    if (tenantId == null) return () => ctrl.abort()
    getProviderAccountHealthSummary(tenantId, ctrl.signal)
      .then((d) => setData(d))
      .catch((e) => {
        if (!ctrl.signal.aborted) setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载池健康聚合失败')
      })
    return () => ctrl.abort()
  }, [refreshNonce, tenantId])

  if (error) return <div className="hk-errorbox" style={{ marginBottom: 'var(--hk-space-3)' }}>{error}</div>
  if (!data) return null

  const stats = mapAccountStats(data)
  return (
    <section aria-label="账号池巡检概览" style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      <div style={statsGridStyle}>
        {stats.map((stat) => (
          <StatCard key={stat.label} label={stat.label} value={stat.value} hint={stat.hint} tone={stat.tone} />
        ))}
      </div>
      {data.states.length > 0 && (
        <div style={stateRowStyle}>
          <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>健康态 · 点击筛当前页</span>
          {data.states.map((state) => {
            const active = activeHealthState === state.health_state
            return (
              <button
                key={state.health_state}
                type="button"
                aria-pressed={active}
                className={`hk-pill ${isAttentionState(state.health_state) ? 'hk-pill--crit' : 'hk-pill--ok'}`}
                title={`${state.health_state} · 点击${active ? '清除' : '筛选'}`}
                onClick={() => onHealthStateChange?.(active ? '' : state.health_state)}
                style={{ border: active ? '1px solid currentColor' : '1px solid transparent', cursor: 'pointer' }}
              >
                {healthStateLabel(state.health_state)} · {state.count}
              </button>
            )
          })}
          {activeHealthState && (
            <button type="button" className="hk-btn hk-btn--sm" onClick={() => onHealthStateChange?.('')}>
              清除健康态筛选
            </button>
          )}
        </div>
      )}
    </section>
  )
}

const statsGridStyle: React.CSSProperties = { display: 'grid', gridTemplateColumns: 'repeat(4, minmax(0, 1fr))', gap: 'var(--hk-space-3)' }
const stateRowStyle: React.CSSProperties = { display: 'flex', alignItems: 'center', flexWrap: 'wrap', gap: 'var(--hk-space-2)', padding: 'var(--hk-space-2) var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)' }
