import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { getRiskOverview } from './api'
import { buildRiskCards, DEFAULT_TENANT_ID, parseTenantInput, totalRiskSignals } from './risk'
import type { RiskOverview } from './types'

/*
 * 风控只读总览(运营台 · 安全审计)。把散落的已接线风控信号聚合成一张计数表:
 * 已禁用 Key / 触发中告警 / 已封禁用户 / IP 黑名单 Key。**零处置、零写入**——每张卡只读,
 * 「去处理」跳转到已有运维页(审核台 / 告警台 / 用户 / 密钥)执行真正的处置动作。
 * 数据走 GET /admin/v1/risk/overview(admin token + 后端强 tenant 隔离)。
 * 真码:backend/internal/riskoverviewhttp、backend/cmd/gateway/routes_risk.go。
 */

export function RiskOverviewPage() {
  const [tenantId, setTenantId] = useState(DEFAULT_TENANT_ID)
  const [data, setData] = useState<RiskOverview | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    const ac = new AbortController()
    setLoading(true)
    setError(null)
    getRiskOverview(tenantId, ac.signal)
      .then((d) => setData(d))
      .catch((e: unknown) => {
        if (ac.signal.aborted) return
        setError(e instanceof Error ? e.message : '加载风控总览失败')
        setData(null)
      })
      .finally(() => {
        if (!ac.signal.aborted) setLoading(false)
      })
    return () => ac.abort()
  }, [tenantId])

  const cards = data ? buildRiskCards(data) : []

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>风控总览</h1>
          <p className="hk-sub">
            运营台 · 已接线风控信号的只读聚合。本页不执行处置,点各卡「去处理」到对应运维页操作。
            {data ? ` 当前共 ${totalRiskSignals(data)} 个风险信号。` : ''}
          </p>
        </div>
        <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
          租户 ID
          <input
            value={tenantId}
            inputMode="numeric"
            onChange={(e) => setTenantId(parseTenantInput(e.target.value))}
            style={{ width: 96, padding: '6px 8px', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)' }}
          />
        </label>
      </header>

      {loading && <p style={{ color: 'var(--hk-ink-500)' }}>加载中…</p>}
      {error && (
        <p style={{ color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-sm)' }}>
          {error}
        </p>
      )}

      {!loading && !error && data && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(220px, 1fr))', gap: 'var(--hk-space-4)' }}>
          {cards.map((c) => {
            const alert = c.tone === 'alert'
            return (
              <div
                key={c.key}
                className="hk-card"
                style={{
                  borderLeft: `4px solid ${alert ? 'var(--hk-danger)' : 'var(--hk-success)'}`,
                  padding: 'var(--hk-space-4)',
                  display: 'flex',
                  flexDirection: 'column',
                  gap: 'var(--hk-space-2)',
                }}
              >
                <span style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>{c.label}</span>
                <span style={{ fontSize: 30, fontWeight: 600, color: alert ? 'var(--hk-danger)' : 'var(--hk-ink-900)' }}>
                  {c.count}
                </span>
                <Link to={c.actionPath} style={{ fontSize: 12, color: 'var(--hk-primary-600)', textDecoration: 'none' }}>
                  去处理 · {c.actionLabel} →
                </Link>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
