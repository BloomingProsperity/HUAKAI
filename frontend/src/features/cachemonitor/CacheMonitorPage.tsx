import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
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
  shortKey,
  validateEvictKey,
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

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>L2 响应缓存监控</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          只读运维:查看 non-streaming 响应缓存的启用态、容量占用、TTL、命中指标与条目元数据。
          按 key 逐条驱逐属破坏性动作,会让相同请求穿透到上游;后端只暴露安全元数据,不回显响应正文。
        </p>
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
      <section style={card}>
        <div style={cardHead}>
          <h2 style={{ fontSize: 15, margin: 0 }}>按 key 驱逐</h2>
          <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>破坏性 · 二次确认</span>
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
          <button type="submit" disabled={evicting} style={evicting ? disabledBtn : dangerBtn}>
            {evicting ? '驱逐中…' : '驱逐该 key'}
          </button>
        </form>
      </section>

      {/* 条目元数据表 */}
      <section style={card}>
        <div style={cardHead}>
          <h2 style={{ fontSize: 15, margin: 0 }}>缓存条目</h2>
          <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>共 {entries.length} 条</span>
        </div>
        {loading && !stats ? (
          <Empty>加载中…</Empty>
        ) : entries.length === 0 ? (
          <Empty>当前无缓存条目(缓存未启用、为空,或当前作用域内无可见条目)。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
              <thead>
                <tr>
                  {['key', '租户', '厂商', '模型', '状态码', '大小', '存入时间', '过期时间', ''].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {entries.map((row) => (
                  <tr key={row.key} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={tdMono} title={row.key}>
                      {shortKey(row.key)}
                    </td>
                    <td style={tdMono}>#{row.tenant_id}</td>
                    <td style={td}>{row.vendor || '—'}</td>
                    <td style={td}>{row.model || '—'}</td>
                    <td style={tdMono}>{row.status}</td>
                    <td style={tdMono}>{formatBytes(row.size_bytes)}</td>
                    <td style={tdMono}>{formatTs(row.stored_at)}</td>
                    <td style={tdMono}>{formatTs(row.expires_at)}</td>
                    <td style={{ ...td, textAlign: 'right' }}>
                      <button
                        type="button"
                        disabled={evicting}
                        onClick={() => {
                          setEvictKeyInput(row.key)
                          quickEvict(row.key)
                        }}
                        style={evicting ? disabledBtn : dangerSmallBtn}
                        title="驱逐该条目"
                      >
                        驱逐
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
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
    <div style={{ ...card, padding: 'var(--hk-space-4)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
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
      : { color: '#0b6553', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
  return <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>{children}</div>
}
function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

/** 时间串展示:把 RFC3339 转本地可读;空/非法原样回退 —。本页只读不参与校验,放在组件内。 */
function formatTs(ts: string | null | undefined): string {
  if (!ts) return '—'
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  return d.toLocaleString()
}

const card: React.CSSProperties = { background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }
const cardHead: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)', background: 'var(--hk-surface-sunken)' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
const tdMono: React.CSSProperties = { ...td, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
const dangerBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid #c2453a', borderRadius: 'var(--hk-radius-md)', background: '#d9534f', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const dangerSmallBtn: React.CSSProperties = { height: 28, padding: '0 var(--hk-space-3)', border: '1px solid #c2453a', borderRadius: 'var(--hk-radius-md)', background: 'transparent', color: 'var(--hk-danger)', fontSize: 12, cursor: 'pointer' }
const disabledBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface-sunken)', color: 'var(--hk-ink-300)', fontSize: 13, cursor: 'not-allowed' }
