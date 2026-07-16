import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { evictL2Key, getL2Stats } from './api'
import {
  aggregateMetrics,
  capacityPercent,
  capacityTone,
  enabledLabel,
  enabledTone,
  formatBytes,
  formatTTL,
  hitRatePercent,
  mapCacheEntryRows,
  validateEvictKey,
  type CacheEntryTableRow,
} from './cachemonitor'
import type { L2StatsResponse } from './types'

/*
 * L2 响应缓存监控。管线第 7 站(系统)下的纯运维(不动钱)。
 * 后端 gatewayhttp/admin_cache_l2_handler.go,挂 /admin/v1/cache/l2(admin token):
 *   - GET    /stats        命中/容量/TTL/条目统计(只读,handler.go:34)
 *   - DELETE /{key}        按 key 逐条驱逐(破坏性,handler.go:35)
 * 后端只回安全元数据(无 response body 明文,store.go:25);租户操作员只见自身租户
 * 条目且 metrics 为空(handler.go:53),platform_admin 见全量 + 命中/未命中指标。
 * 驱逐前后端再校验存在性 + 租户作用域(handler.go:79-87),前端仍对该破坏性动作做二次确认。
 * 本页不碰任何 pool/registry/gateway 等碰撞包模块。
 */

export function CacheMonitorPage() {
  const [stats, setStats] = useState<L2StatsResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [evictKeyInput, setEvictKeyInput] = useState('')
  const [evicting, setEvicting] = useState(false)

  const load = useCallback(
    (signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      getL2Stats(signal)
        .then((resp) => setStats(resp))
        .catch((e: unknown) => {
          if (signal?.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载缓存统计失败')
        })
        .finally(() => {
          if (!signal?.aborted) setLoading(false)
        })
    },
    [],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  const onEvict = (e: React.FormEvent) => {
    e.preventDefault()
    const v = validateEvictKey(evictKeyInput)
    if (!v.ok) {
      setError(v.error)
      setNotice(null)
      return
    }
    // 破坏性运维:逐条驱逐会让下一次相同请求穿透到上游(产生上游成本/延迟),二次确认。
    if (
      !window.confirm(
        `确认驱逐缓存 key:\n\n${v.value}\n\n` +
          '驱逐后该条响应缓存立即失效,下一次相同请求将穿透到上游重新计算。此操作不可撤销。',
      )
    ) {
      return
    }
    setEvicting(true)
    setError(null)
    setNotice(null)
    evictL2Key(v.value)
      .then((resp) => {
        setNotice(
          resp.deleted
            ? `已驱逐 key:${resp.key}`
            : `key 已不在缓存中(可能已过期或被淘汰):${resp.key}`,
        )
        setEvictKeyInput('')
        load()
      })
      .catch((err: unknown) =>
        setError(err instanceof ApiError ? `驱逐失败:${err.message}(${err.code})` : '驱逐失败'),
      )
      .finally(() => setEvicting(false))
  }

  const totals = stats ? aggregateMetrics(stats.metrics) : null
  const entries = stats?.entries ?? []
  const tableRows = mapCacheEntryRows(entries)

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>L2 响应缓存监控</h1>
          <p className="hk-sub">
            只读运维:查看 non-streaming 响应缓存的启用态、容量占用、TTL、命中指标与条目元数据。
            按 key 逐条驱逐属破坏性动作,会让相同请求穿透到上游;后端只暴露安全元数据,不回显响应正文。
          </p>
        </div>
      </header>

      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      {/* 概览卡组 */}
      <section style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: 'var(--hk-space-4)' }}>
        <StatCard label="缓存状态">
          {stats ? (
            <StatusBadge tone={enabledTone(stats.enabled)}>{enabledLabel(stats.enabled)}</StatusBadge>
          ) : (
            <Dim>—</Dim>
          )}
        </StatCard>
        <StatCard label="容量占用">
          {stats ? (
            <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
              <strong style={{ fontSize: 18 }}>{formatBytes(stats.size_bytes)}</strong>
              <StatusBadge tone={capacityTone(stats.size_bytes, stats.max_size_bytes)}>
                {capacityPercent(stats.size_bytes, stats.max_size_bytes)}
              </StatusBadge>
            </span>
          ) : (
            <Dim>—</Dim>
          )}
          <Sub>上限 {stats ? formatBytes(stats.max_size_bytes) : '—'}</Sub>
        </StatCard>
        <StatCard label="TTL">
          <strong style={{ fontSize: 18 }}>{stats ? formatTTL(stats.ttl_seconds) : '—'}</strong>
        </StatCard>
        <StatCard label="条目数">
          <strong style={{ fontSize: 18 }}>{stats ? entries.length : '—'}</strong>
        </StatCard>
        <StatCard label="命中率">
          <strong style={{ fontSize: 18 }}>{stats ? hitRatePercent(stats) : '—'}</strong>
          <Sub>
            {totals && totals.rows > 0
              ? `命中 ${totals.hit} · 未命中 ${totals.miss}`
              : 'platform_admin 可见指标'}
          </Sub>
        </StatCard>
      </section>

      {/* 按 key 驱逐(破坏性) */}
      <section className="hk-card">
        <div className="hk-card__head">
          <h3>按 key 驱逐</h3>
          <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--hk-ink-300)' }}>破坏性 · 二次确认</span>
        </div>
        <form onSubmit={onEvict} style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', flexWrap: 'wrap', padding: 'var(--hk-space-4)' }}>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)', flex: 1, minWidth: 280 }}>
            缓存 key(精确匹配)
            <input
              value={evictKeyInput}
              onChange={(e) => setEvictKeyInput(e.target.value)}
              placeholder="从下表条目复制完整 key 粘贴到此处"
              style={inp}
            />
          </label>
          <button type="submit" disabled={evicting} className="hk-btn hk-btn--danger" style={evicting ? { opacity: 0.55, cursor: 'not-allowed' } : undefined}>
            {evicting ? '驱逐中…' : '驱逐该 key'}
          </button>
        </form>
      </section>

      {/* 条目元数据表 */}
      <section className="hk-card">
        <div className="hk-card__head">
          <h3>缓存条目</h3>
          <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--hk-ink-300)' }}>共 {entries.length} 条</span>
        </div>
        {loading && !stats ? (
          <EmptyState title="正在加载缓存条目" hint="请稍候。" />
        ) : entries.length === 0 ? (
          <EmptyState title="当前无缓存条目" hint="缓存可能未启用、为空，或当前作用域内没有可见条目。" />
        ) : (
          <DataListTable
            label="缓存条目"
            rows={tableRows}
            rowKey={(row) => row.key}
            columns={cacheColumns}
            actions={[{
              label: '驱逐',
              tone: 'danger',
              disabled: evicting,
              onClick: (row) => {
                setEvictKeyInput(row.key)
                quickEvict(row.key)
              },
            }]}
          />
        )}
      </section>
    </div>
  )

  // 行内「驱逐」直达:复用同一二次确认 + 调用,避免重复 setState 编排。
  function quickEvict(key: string) {
    const v = validateEvictKey(key)
    if (!v.ok) {
      setError(v.error)
      return
    }
    if (
      !window.confirm(
        `确认驱逐缓存 key:\n\n${v.value}\n\n` +
          '驱逐后该条响应缓存立即失效,下一次相同请求将穿透到上游重新计算。此操作不可撤销。',
      )
    ) {
      return
    }
    setEvicting(true)
    setError(null)
    setNotice(null)
    evictL2Key(v.value)
      .then((resp) => {
        setNotice(
          resp.deleted
            ? `已驱逐 key:${resp.key}`
            : `key 已不在缓存中(可能已过期或被淘汰):${resp.key}`,
        )
        setEvictKeyInput('')
        load()
      })
      .catch((err: unknown) =>
        setError(err instanceof ApiError ? `驱逐失败:${err.message}(${err.code})` : '驱逐失败'),
      )
      .finally(() => setEvicting(false))
  }
}

/* ——— 本文件私有小组件 / 样式 ——— */
function StatCard({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="hk-card" style={{ padding: 'var(--hk-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
      <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>{label}</span>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>{children}</div>
    </div>
  )
}
function Sub({ children }: { children: React.ReactNode }) {
  return <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{children}</span>
}
function Dim({ children }: { children: React.ReactNode }) {
  return <span style={{ color: 'var(--hk-ink-300)' }}>{children}</span>
}
function Banner({ kind, children }: { kind: 'error' | 'ok'; children: React.ReactNode }) {
  const palette =
    kind === 'error'
      ? { color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
      : { color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
  return <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>{children}</div>
}
const cacheColumns: DataListColumn<CacheEntryTableRow>[] = [
  { key: 'key', label: 'key', render: (row) => <span className="hk-mono" title={row.key}>{row.keyLabel}</span> },
  { key: 'tenant', label: '租户', render: (row) => <span className="hk-mono">{row.tenant}</span> },
  { key: 'vendor', label: '厂商', render: (row) => row.vendor },
  { key: 'model', label: '模型', render: (row) => row.model },
  { key: 'status', label: '状态码', render: (row) => <span className="hk-mono">{row.status}</span> },
  { key: 'size', label: '大小', render: (row) => <span className="hk-mono">{row.size}</span> },
  { key: 'stored-at', label: '存入时间', render: (row) => <span className="hk-mono">{row.storedAt}</span> },
  { key: 'expires-at', label: '过期时间', render: (row) => <span className="hk-mono">{row.expiresAt}</span> },
]

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
