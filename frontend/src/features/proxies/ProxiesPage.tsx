import { useEffect, useState } from 'react'
import { deleteProxy, listProxies, setProxyStatus, testProxy } from './api'
import { DEFAULT_TENANT_ID, parseTenantInput, probeSummary, STATUSES, statusTone, type ProbeSummary } from './proxies'
import { ProxyCreateForm } from './ProxyCreateForm'
import type { Proxy } from './types'

/*
 * 出口代理池(运营台 · 路由与池)。列出租户出口代理 + 新建 + 行内删除/状态切换 + 「测试连通」主动质检
 * (经该代理建隧道到服务端固定 canary,测真实出站连通 + 延迟,区别于被动 TCP 存活)。
 * 编辑(改 host/凭据)留作后续切片(update 的 auth_secret 留空会清空凭据,需专门 UX 处理)。
 * 数据走 /admin/v1/proxies(admin token)。真码:backend/internal/proxyadminhttp、
 * backend/cmd/gateway/routes_proxy_probe.go(双 SSRF 守卫)。
 */

type RowProbe = { testing: boolean; summary?: ProbeSummary }

const toneColor: Record<string, string> = {
  ok: 'var(--hk-ok, #2e7d32)',
  fail: 'var(--hk-danger, #c0392b)',
  muted: 'var(--hk-ink-500)',
}

export function ProxiesPage() {
  const [tenantId, setTenantId] = useState(DEFAULT_TENANT_ID)
  const [proxies, setProxies] = useState<Proxy[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [probes, setProbes] = useState<Record<number, RowProbe>>({})
  const [reloadKey, setReloadKey] = useState(0)
  const reload = () => setReloadKey((k) => k + 1)

  useEffect(() => {
    const ac = new AbortController()
    setLoading(true)
    setError(null)
    setProbes({})
    listProxies(tenantId, ac.signal)
      .then((d) => setProxies(d.items ?? []))
      .catch((e: unknown) => {
        if (ac.signal.aborted) return
        setError(e instanceof Error ? e.message : '加载代理列表失败')
        setProxies([])
      })
      .finally(() => {
        if (!ac.signal.aborted) setLoading(false)
      })
    return () => ac.abort()
  }, [tenantId, reloadKey])

  async function onDelete(p: Proxy) {
    if (!window.confirm(`确定删除代理「${p.name}」(${p.host}:${p.port})?`)) return
    try {
      await deleteProxy(tenantId, p.id)
      reload()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : '删除失败')
    }
  }

  async function onStatusChange(p: Proxy, status: string) {
    try {
      await setProxyStatus(tenantId, p.id, status)
      reload()
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : '状态更新失败')
    }
  }

  async function runTest(id: number) {
    setProbes((m) => ({ ...m, [id]: { testing: true } }))
    try {
      const res = await testProxy(tenantId, id)
      setProbes((m) => ({ ...m, [id]: { testing: false, summary: probeSummary(res) } }))
    } catch (e: unknown) {
      setProbes((m) => ({
        ...m,
        [id]: { testing: false, summary: { label: e instanceof Error ? e.message : '请求失败', tone: 'fail' } },
      }))
    }
  }

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: 'var(--hk-space-3)' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
          <h1 style={{ fontSize: 22 }}>出口代理池</h1>
          <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
            运营台 · 租户出口代理。新建 / 删除 / 切换状态;点「测试连通」经该代理建隧道到固定探测目标,
            验真实出站连通 + 延迟(非裸 TCP 存活)。
          </p>
        </div>
        <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
          租户 ID
          <input
            value={tenantId}
            inputMode="numeric"
            onChange={(e) => setTenantId(parseTenantInput(e.target.value))}
            style={{ width: 96, padding: '6px 8px', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-2)' }}
          />
        </label>
      </header>

      <ProxyCreateForm tenantId={tenantId} onCreated={reload} />

      {loading && <p style={{ color: 'var(--hk-ink-500)' }}>加载中…</p>}
      {error && (
        <p style={{ color: 'var(--hk-danger, #c0392b)', background: 'var(--hk-danger-bg, #fdecea)', padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-2)' }}>
          {error}
        </p>
      )}
      {!loading && !error && proxies.length === 0 && (
        <p style={{ color: 'var(--hk-ink-500)' }}>该租户暂无出口代理。</p>
      )}

      {!loading && !error && proxies.length > 0 && (
        <div style={{ border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-2)', overflow: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr style={{ textAlign: 'left', background: 'var(--hk-surface, #fff)' }}>
                <th style={th}>名称</th>
                <th style={th}>协议</th>
                <th style={th}>地址</th>
                <th style={th}>状态</th>
                <th style={th}>连通性</th>
                <th style={{ ...th, textAlign: 'right' }}>操作</th>
              </tr>
            </thead>
            <tbody>
              {proxies.map((p) => {
                const probe = probes[p.id]
                return (
                  <tr key={p.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={td}>{p.name}</td>
                    <td style={td}>{p.protocol}</td>
                    <td style={{ ...td, fontVariantNumeric: 'tabular-nums' }}>{p.host}:{p.port}</td>
                    <td style={td}>
                      <select
                        value={p.status}
                        onChange={(e) => onStatusChange(p, e.target.value)}
                        style={{ color: toneColor[statusTone(p.status)], border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-2)', padding: '2px 6px', fontSize: 12, background: 'transparent' }}
                      >
                        {STATUSES.map((s) => <option key={s} value={s}>{s}</option>)}
                      </select>
                    </td>
                    <td style={{ ...td, color: probe?.summary ? toneColor[probe.summary.tone] : 'var(--hk-ink-500)' }}>
                      {probe?.testing ? '测试中…' : probe?.summary ? probe.summary.label : '—'}
                    </td>
                    <td style={{ ...td, textAlign: 'right' }}>
                      <button
                        type="button"
                        disabled={probe?.testing}
                        onClick={() => runTest(p.id)}
                        style={{
                          padding: '4px 10px',
                          border: '1px solid var(--hk-line)',
                          borderRadius: 'var(--hk-radius-2)',
                          background: 'transparent',
                          cursor: probe?.testing ? 'default' : 'pointer',
                          fontSize: 12,
                        }}
                      >
                        {probe?.testing ? '测试中' : '测试连通'}
                      </button>
                      <button
                        type="button"
                        onClick={() => onDelete(p)}
                        style={{
                          marginLeft: 8,
                          padding: '4px 10px',
                          border: '1px solid var(--hk-danger, #c0392b)',
                          borderRadius: 'var(--hk-radius-2)',
                          background: 'transparent',
                          color: 'var(--hk-danger, #c0392b)',
                          cursor: 'pointer',
                          fontSize: 12,
                        }}
                      >
                        删除
                      </button>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}

const th: React.CSSProperties = { padding: '8px 12px', fontWeight: 600, color: 'var(--hk-ink-700)' }
const td: React.CSSProperties = { padding: '6px 12px' }
