import { useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { getProviderAccountHealthSummary } from './api'
import type { AccountHealthSummary } from './types'

/*
 * 账号池巡检概览卡(B9)。GET /provider-accounts/health-summary 跨整个租户池聚合,
 * 一眼看清总数/启用/停用/需关注 + 各健康态计数。不是分页统计,是全池真值。
 * 需关注(non-healthy 或被停用)高亮;点计数可跳到对应筛选(交给现有 state_filter)。
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

export function HealthSummaryCard() {
  const [data, setData] = useState<AccountHealthSummary | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    getProviderAccountHealthSummary(ctrl.signal)
      .then((d) => setData(d))
      .catch((e) => {
        if (!ctrl.signal.aborted) setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载池健康聚合失败')
      })
    return () => ctrl.abort()
  }, [])

  if (error) return <div className="hk-errorbox" style={{ marginBottom: 'var(--hk-space-3)' }}>{error}</div>
  if (!data) return null

  return (
    <section className="hk-card" style={{ marginBottom: 'var(--hk-space-3)' }}>
      <div className="hk-card__head"><h3>池巡检概览</h3><span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--hk-ink-300)' }}>全池真值 · 非分页</span></div>
      <div className="hk-card__body">
        <div className="hk-posture">
          <Stat label="账号总数" value={data.total} />
          <Stat label="已启用" value={data.enabled} />
          <Stat label="已停用" value={data.disabled} tone={data.disabled > 0 ? 'warn' : undefined} />
          <Stat label="需关注" value={data.needs_attention} tone={data.needs_attention > 0 ? 'danger' : 'ok'} />
        </div>
        {data.states.length > 0 && (
          <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginTop: 'var(--hk-space-3)' }}>
            {data.states.map((s) => (
              <span
                key={s.health_state}
                className={`hk-pill ${isAttentionState(s.health_state) ? 'hk-pill--crit' : 'hk-pill--ok'}`}
                title={s.health_state}
              >
                {healthStateLabel(s.health_state)} · {s.count}
              </span>
            ))}
          </div>
        )}
      </div>
    </section>
  )
}

function Stat({ label, value, tone }: { label: string; value: number; tone?: 'ok' | 'warn' | 'danger' }) {
  const color = tone === 'danger' ? 'var(--hk-danger)' : tone === 'warn' ? 'var(--hk-warn)' : tone === 'ok' ? 'var(--hk-success)' : 'var(--hk-ink-900)'
  return (
    <div className="hk-statcard">
      <div className="hk-statcard__v" style={{ color }}>{value}</div>
      <div className="hk-statcard__s">{label}</div>
    </div>
  )
}
