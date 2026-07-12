import { useCallback, useEffect, useMemo, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { listModules } from './api'
import {
  countByProbe,
  extractCategories,
  groupByCategory,
  probeLabel,
  probeTone,
} from './moduleregistry'
import type { ModuleView } from './types'

/*
 * 模块知识脊柱总览(只读)。后端 GET /admin/v1/modules(platform_admin 门控,
 * routes_modules.go:21/31),把运行时 descriptor(身份/能力 + 实时探针)与静态
 * feature-tree catalog(section/feature_id/parity 状态/所属包)合并成运维视图。
 * 本页仅做只读展示 + 按 category 客户端/服务端过滤,不触碰任何写路径。
 * 该接口面只携带模块身份、枚举状态与简短诊断 detail —— 后端约定绝不含密钥或用户数据。
 */

export function ModuleRegistryPage() {
  // 已加载的全量模块(无过滤时)。用于客户端抽取 category 选项,避免反复请求。
  const [allModules, setAllModules] = useState<ModuleView[] | null>(null)
  // 当前显示的模块(可能是过滤后的子集)。
  const [modules, setModules] = useState<ModuleView[]>([])
  const [category, setCategory] = useState('')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(
    (cat: string, signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      listModules(cat, signal)
        .then((resp) => {
          const items = resp.modules ?? []
          setModules(items)
          // 仅在「全量」加载时缓存,以便从中抽取完整 category 选项列表。
          if (cat.trim() === '') setAllModules(items)
        })
        .catch((e: unknown) => {
          if (signal?.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载模块清单失败')
        })
        .finally(() => {
          if (!signal?.aborted) setLoading(false)
        })
    },
    [],
  )

  // 首次加载全量;之后 category 变化时重新拉(后端支持 ?category= 精确过滤)。
  useEffect(() => {
    const ctrl = new AbortController()
    load(category, ctrl.signal)
    return () => ctrl.abort()
  }, [load, category])

  // category 选项来自首次全量加载(过滤态不会丢失选项)。
  const categoryOptions = useMemo(
    () => extractCategories(allModules ?? modules),
    [allModules, modules],
  )
  const groups = useMemo(() => groupByCategory(modules), [modules])
  const counts = useMemo(() => countByProbe(modules), [modules])

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>模块知识脊柱</h1>
          <p className="hk-sub">
            每个子系统的身份 + 能力 + 静态 parity 状态 + 实时只读探针。只读总览,用于运维分诊与根因定位;此面不含任何密钥或用户数据。
          </p>
        </div>
      </header>

      {/* 过滤 + 健康概览 */}
      <div style={{ display: 'flex', gap: 'var(--hk-space-4)', alignItems: 'stretch', flexWrap: 'wrap' }}>
        <div className="hk-card" style={{ padding: 'var(--hk-space-4)', display: 'flex', alignItems: 'flex-end', gap: 'var(--hk-space-3)' }}>
          <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
            类别过滤(category)
            <select
              value={category}
              onChange={(e) => setCategory(e.target.value)}
              style={{ ...inp, width: 220 }}
            >
              <option value="">全部类别</option>
              {categoryOptions.map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </select>
          </label>
          <button type="button" onClick={() => load(category)} disabled={loading} className="hk-btn">
            {loading ? '刷新中…' : '刷新'}
          </button>
        </div>

        <div className="hk-card" style={{ padding: 'var(--hk-space-4)', display: 'flex', gap: 'var(--hk-space-5)', alignItems: 'center', flexWrap: 'wrap' }}>
          <Stat label="模块总数" value={counts.total} tone="muted" />
          <Stat label="正常" value={counts.ok} tone="ok" />
          <Stat label="降级" value={counts.degraded} tone="warn" />
          <Stat label="失败" value={counts.error} tone="danger" />
          <Stat label="未知" value={counts.unknown} tone="muted" />
        </div>
      </div>

      {error && <Banner kind="error">{error}</Banner>}

      {loading && modules.length === 0 ? (
        <Empty>加载中…</Empty>
      ) : modules.length === 0 ? (
        <Empty>{category ? `类别「${category}」下暂无模块。` : '暂无已注册模块。'}</Empty>
      ) : (
        groups.map((g) => (
          <section key={g.category} className="hk-card">
            <div className="hk-card__head">
              <h3>{g.category}</h3>
              <span style={{ marginLeft: 'auto', fontSize: 11, color: 'var(--hk-ink-300)' }}>共 {g.modules.length} 个模块</span>
            </div>
            <div className="hk-tablewrap">
              <table className="hk-table">
                <thead>
                  <tr>
                    {['模块', '能力', '探针', 'Parity / 状态', 'Section'].map((h) => (
                      <th key={h}>{h}</th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {g.modules.map((m) => (
                    <tr key={m.id}>
                      <td>
                        <div style={{ fontWeight: 600, color: 'var(--hk-ink-900)' }}>{m.title || m.id}</div>
                        <div style={{ fontFamily: 'var(--hk-font-mono)', fontSize: 11, color: 'var(--hk-ink-500)' }}>{m.id}</div>
                      </td>
                      <td>
                        {m.capabilities && m.capabilities.length > 0 ? (
                          <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
                            {m.capabilities.map((c) => (
                              <span key={c} className="hk-tag hk-tag--muted">
                                {c}
                              </span>
                            ))}
                          </div>
                        ) : (
                          <span style={{ color: 'var(--hk-ink-300)' }}>—</span>
                        )}
                      </td>
                      <td>
                        <div style={{ display: 'flex', flexDirection: 'column', gap: 4, alignItems: 'flex-start' }}>
                          <StatusBadge tone={probeTone(m.live_probe.status)}>
                            {probeLabel(m.live_probe.status)}
                          </StatusBadge>
                          {m.live_probe.detail && (
                            <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>{m.live_probe.detail}</span>
                          )}
                        </div>
                      </td>
                      <td className="hk-mono">
                        {m.catalog ? (
                          <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                            <span>
                              {m.catalog.status || '—'}
                              {m.catalog.parity ? ` · ${m.catalog.parity}` : ''}
                            </span>
                            {m.catalog.feature_id && (
                              <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>{m.catalog.feature_id}</span>
                            )}
                            {m.catalog.pkgs && m.catalog.pkgs.length > 0 && (
                              <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{m.catalog.pkgs.join(', ')}</span>
                            )}
                          </div>
                        ) : (
                          <span style={{ color: 'var(--hk-ink-300)' }}>纯实时</span>
                        )}
                      </td>
                      <td>{m.catalog?.section || '—'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        ))
      )}
    </div>
  )
}

/* ——— 本文件私有小组件 / 样式 ——— */
function Stat({ label, value, tone }: { label: string; value: number; tone: 'ok' | 'warn' | 'danger' | 'muted' }) {
  const color =
    tone === 'ok' ? 'var(--hk-primary-600)' : tone === 'warn' ? 'var(--hk-warn)' : tone === 'danger' ? 'var(--hk-danger)' : 'var(--hk-ink-900)'
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2, minWidth: 56 }}>
      <span style={{ fontSize: 20, fontWeight: 700, color }}>{value}</span>
      <span style={{ fontSize: 11, color: 'var(--hk-ink-500)' }}>{label}</span>
    </div>
  )
}
function Banner({ kind, children }: { kind: 'error' | 'ok'; children: React.ReactNode }) {
  const palette =
    kind === 'error'
      ? { color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
      : { color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
  return <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>{children}</div>
}
function Empty({ children }: { children: React.ReactNode }) {
  return <div className="hk-empty">{children}</div>
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)' }
