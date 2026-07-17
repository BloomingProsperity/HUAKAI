import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatCard } from '../../ui/StatCard'
import { listApiKeys } from '../keys/api'
import type { ApiKeyView } from '../keys/types'
import { getKeyUsageSummary, getQuota } from './api'
import { metricLabel, quotaProgress, windowLabel } from './quota'
import { resetCountdown } from './windowMeter'
import { MeterCells } from './Heatmaps'
import type { KeyUsageSummary, QuotaWindow } from './types'
import { KeyUsageAnalytics } from './KeyUsageAnalytics'
import { mapKeyUsageRows, mapUsageStats, type KeyUsageTableRow } from './usage'

/*
 * 用量与配额(P0)。管线第 4 站:
 *  - 配额窗口(/v1/me/quota)→ 每个 metric×window 的 cap/consumed/remaining + 进度条;
 *  - per-key 用量(列 active key,各取 /v1/me/keys/{id}/usage-summary)→ 花费/请求数/tokens。
 * 以上走 session；Key 级深度分析三端点走用户临时粘贴的 API Key Bearer。
 */
export function UsagePage() {
  const [windows, setWindows] = useState<QuotaWindow[]>([])
  const [rows, setRows] = useState<Array<{ key: ApiKeyView; summary: KeyUsageSummary | null }>>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async (signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    try {
      const [quota, keys] = await Promise.all([getQuota(signal), listApiKeys(0, 100, signal)])
      if (signal.aborted) return
      setWindows(quota.items)
      const active = keys.api_keys.filter((k) => k.status === 'active')
      const summaries = await Promise.all(
        active.map((k) =>
          getKeyUsageSummary(k.api_key_id, signal)
            .then((s) => ({ key: k, summary: s }))
            .catch(() => ({ key: k, summary: null })),
        ),
      )
      if (!signal.aborted) setRows(summaries)
    } catch (e) {
      if (signal.aborted) return
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载用量失败')
    } finally {
      if (!signal.aborted) setLoading(false)
    }
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    void load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  const usageRows = mapKeyUsageRows(rows)
  const stats = mapUsageStats(rows, loading, error !== null)
  const columns: DataListColumn<KeyUsageTableRow>[] = [
    {
      key: 'key',
      label: '密钥',
      render: (row) => (
        <span style={{ display: 'flex', flexDirection: 'column' }}>
          <strong style={{ color: 'var(--hk-ink-900)' }}>{row.name}</strong>
          <code style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{row.prefix}</code>
          {!row.available && <span style={{ fontSize: 11, color: 'var(--hk-warn)' }}>汇总不可用</span>}
        </span>
      ),
    },
    { key: 'cost', label: '花费(USD)', render: (row) => <span className="hk-mono">{row.cost}</span> },
    { key: 'requests', label: '请求数', render: (row) => <span className="hk-mono">{row.requests}</span> },
    { key: 'input', label: '输入 Token', render: (row) => <span className="hk-mono">{row.inputTokens}</span> },
    { key: 'output', label: '输出 Token', render: (row) => <span className="hk-mono">{row.outputTokens}</span> },
    { key: 'cache', label: '缓存读/写', render: (row) => <span className="hk-mono">{row.cacheTokens}</span> },
  ]

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>用量与配额</h1>
          <p className="hk-sub">管线第 4 站 · 配额窗口与各密钥用量。</p>
        </div>
      </header>

      {error && <Banner>{error}</Banner>}

      <section aria-label="当前页用量统计" style={statsGrid}>
        {stats.map((stat) => (
          <StatCard key={stat.label} label={stat.label} value={stat.value} hint={stat.hint} />
        ))}
      </section>

      <Card title="配额窗口">
        {loading && windows.length === 0 ? (
          <EmptyState title="正在加载配额窗口" hint="请稍候。" />
        ) : error && windows.length === 0 ? (
          <EmptyState title="配额窗口暂不可用" hint="请稍后重新打开本页。" tone="unavailable" />
        ) : windows.length === 0 ? (
          <EmptyState title="当前账户无配额上限" hint="系统仍会记录实际消耗。" tone="positive" />
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
            {windows.map((w, i) => (
              <QuotaBar key={`${w.metric}-${w.window_kind}-${i}`} w={w} />
            ))}
          </div>
        )}
      </Card>

      <Card title="各密钥用量汇总">
        {loading && rows.length === 0 ? (
          <EmptyState title="正在加载密钥用量" hint="请稍候。" />
        ) : error && rows.length === 0 ? (
          <EmptyState title="密钥用量暂不可用" hint="请稍后重新打开本页。" tone="unavailable" />
        ) : rows.length === 0 ? (
          <EmptyState title="没有活跃密钥可统计" hint="创建并启用 Key 后，用量汇总会显示在这里。" />
        ) : (
          <DataListTable label="各密钥用量汇总" rows={usageRows} rowKey={(row) => row.id} columns={columns} />
        )}
      </Card>

      <KeyUsageAnalytics />
    </div>
  )
}

function QuotaBar({ w }: { w: QuotaWindow }) {
  const p = quotaProgress(w.consumed, w.cap)
  // 分段方格条(参照 Claude/Codex 速率窗口):按 consumed/cap 填格 + 重置倒计时。
  const countdown = resetCountdown(w.window_end, Date.now())
  return (
    <div className="hk-meter">
      <div className="hk-meter__head">
        <span style={{ color: 'var(--hk-ink-700)' }}>
          {metricLabel(w.metric)} · {windowLabel(w.window_kind)}
          {w.request_count > 0 ? ` · ${w.request_count} 次请求` : ''}
        </span>
        <span className="hk-mono" style={{ color: p.over ? 'var(--hk-danger)' : 'var(--hk-ink-500)' }}>
          {w.consumed} / {p.unlimited ? '无上限' : w.cap}
          {p.over ? `(超额 ${w.overage})` : p.unlimited ? '' : ` · ${Math.round(p.pct)}%`}
        </span>
      </div>
      {p.unlimited ? (
        <div style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>无上限窗口,仅记录消耗。</div>
      ) : (
        <MeterCells pct={p.pct} tone={p.tone} />
      )}
      {countdown && <div style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{countdown}</div>}
    </div>
  )
}

function Card({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>{title}</h3>
      </div>
      <div className="hk-card__body">{children}</div>
    </section>
  )
}

function Banner({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>
      {children}
    </div>
  )
}

const statsGrid: React.CSSProperties = { display: 'grid', gridTemplateColumns: 'repeat(3, minmax(0, 1fr))', gap: 'var(--hk-space-3)' }
