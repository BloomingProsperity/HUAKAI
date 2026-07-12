import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { getSystemHealth } from './api'
import { componentLabel, fmtBytes, fmtInt, fmtUptime, statusLabel, statusTone, type Tone } from './health'
import type { HealthResponse } from './types'

/*
 * 系统健康(运维台,只读)。GET /v1/admin/system/health 聚合:
 * 顶层状态 + 子系统组件(数据库/渠道健康/死信队列/告警)+ 网关运行时(Go 版本/协程/堆/GC/uptime)。
 * 纯只读诊断,无计费副作用。提供手动刷新。
 */
export function HealthPage() {
  const [data, setData] = useState<HealthResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshedAt, setRefreshedAt] = useState<string>('')

  const load = useCallback((signal?: AbortSignal) => {
    setLoading(true)
    setError(null)
    getSystemHealth(signal)
      .then((d) => {
        setData(d)
        setRefreshedAt(new Date().toLocaleTimeString())
      })
      .catch((e: unknown) => {
        if (signal?.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载系统健康失败')
      })
      .finally(() => {
        if (!signal?.aborted) setLoading(false)
      })
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  const rt = data?.runtime

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>系统健康</h1>
          <p className="hk-sub">子系统状态与网关运行时聚合(只读)。{refreshedAt && `更新于 ${refreshedAt}`}</p>
        </div>
        <button type="button" onClick={() => load()} disabled={loading} className="hk-btn">
          {loading ? '刷新中…' : '刷新'}
        </button>
      </header>

      {error && <div style={errorBox}>{error}</div>}

      {data && (
        <div style={{ ...statusBar, background: toneBox(statusTone(data.status)).background, borderColor: toneBox(statusTone(data.status)).border }}>
          <span style={{ fontSize: 14, fontWeight: 600, color: toneBox(statusTone(data.status)).color }}>总体状态</span>
          <StatusPill status={data.status} large />
        </div>
      )}

      {loading && !data ? (
        <Empty>加载中…</Empty>
      ) : data ? (
        <>
          <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
            <h2 style={sectionTitle}>子系统组件</h2>
            <div style={cardGrid}>
              {data.components.map((c) => (
                <div key={c.name} className="hk-metric">
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--hk-space-2)' }}>
                    <span style={{ fontSize: 13, fontWeight: 600 }}>{componentLabel(c.name)}</span>
                    <StatusPill status={c.status} />
                  </div>
                  <span style={{ fontSize: 12, color: 'var(--hk-ink-500)', minHeight: 16, display: 'block' }}>{c.detail || '—'}</span>
                </div>
              ))}
            </div>
          </section>

          {rt && (
            <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)' }}>
              <h2 style={sectionTitle}>网关运行时</h2>
              <div style={cardGrid}>
                <Stat label="运行时长" value={fmtUptime(rt.uptime_seconds)} />
                <Stat label="Go 版本" value={rt.go_version} mono />
                <Stat label="协程数" value={fmtInt(rt.num_goroutine)} />
                <Stat label="GC 次数" value={fmtInt(rt.num_gc)} />
                <Stat label="堆已分配" value={fmtBytes(rt.heap_alloc_bytes)} />
                <Stat label="堆系统占用" value={fmtBytes(rt.heap_sys_bytes)} />
                {rt.binary_size_bytes !== undefined && rt.binary_size_bytes > 0 && (
                  <Stat label="二进制大小" value={fmtBytes(rt.binary_size_bytes)} />
                )}
              </div>
            </section>
          )}
        </>
      ) : (
        !error && <Empty>暂无数据。</Empty>
      )}
    </div>
  )
}

function StatusPill({ status, large }: { status: HealthResponse['status']; large?: boolean }) {
  const t = toneBox(statusTone(status))
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 6,
        padding: large ? '4px var(--hk-space-3)' : '2px var(--hk-space-2)',
        borderRadius: 'var(--hk-radius-pill, 999px)',
        fontSize: large ? 14 : 12,
        fontWeight: 600,
        color: t.color,
        background: t.background,
        border: `1px solid ${t.border}`,
      }}
    >
      <span style={{ width: 8, height: 8, borderRadius: '50%', background: t.color }} />
      {statusLabel(status)}
    </span>
  )
}

function Stat({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="hk-metric">
      <div className="hk-metric__label">{label}</div>
      <div className={mono ? 'hk-metric__v hk-mono' : 'hk-metric__v'} style={{ wordBreak: 'break-all' }}>{value}</div>
    </div>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div className="hk-empty">{children}</div>
}

function toneBox(tone: Tone): { color: string; background: string; border: string } {
  switch (tone) {
    case 'ok':
      return { color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50, #e8f5ef)', border: 'var(--hk-primary-100, #cfe9df)' }
    case 'warn':
      return { color: 'var(--hk-warn)', background: 'var(--hk-warn-soft)', border: 'var(--hk-warn-soft)' }
    case 'danger':
      return { color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: 'var(--hk-danger-soft)' }
  }
}

const sectionTitle: React.CSSProperties = { fontSize: 14, color: 'var(--hk-ink-700)' }
const cardGrid: React.CSSProperties = { display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))', gap: 'var(--hk-space-3)' }
const statusBar: React.CSSProperties = {
  display: 'flex',
  justifyContent: 'space-between',
  alignItems: 'center',
  padding: 'var(--hk-space-3) var(--hk-space-4)',
  borderRadius: 'var(--hk-radius-lg)',
  border: '1px solid',
}
const errorBox: React.CSSProperties = { padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
