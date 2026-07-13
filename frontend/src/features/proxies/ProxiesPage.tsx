import { useEffect, useState } from 'react'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { deleteProxy, listProxies, setProxyStatus, testProxy } from './api'
import { EditProxyForm } from './EditProxyForm'
import { DEFAULT_TENANT_ID, mapProxyRows, parseTenantInput, probeSummary, STATUSES, statusTone, type ProbeSummary, type ProxyTableRow } from './proxies'
import { ProxyCreateForm } from './ProxyCreateForm'
import type { Proxy } from './types'

/*
 * 出口代理池(运营台 · 路由与池)。列出租户出口代理 + 新建 + 行内删除/状态切换/编辑 + 「测试连通」主动质检
 * (经该代理建隧道到服务端固定 canary,测真实出站连通 + 延迟,区别于被动 TCP 存活)。
 * 编辑(PATCH /{id}):改 name/protocol/host/port/认证;auth_secret 留空=不改密钥(避免误清空)。
 * 数据走 /admin/v1/proxies(admin token)。真码:backend/internal/proxyadminhttp、
 * backend/cmd/gateway/routes_proxy_probe.go(双 SSRF 守卫)。
 */

type RowProbe = { testing: boolean; summary?: ProbeSummary }

const toneColor: Record<string, string> = {
  ok: 'var(--hk-ok, var(--hk-success))',
  fail: 'var(--hk-danger, var(--hk-danger))',
  muted: 'var(--hk-ink-500)',
}

export function ProxiesPage() {
  const [tenantId, setTenantId] = useState(DEFAULT_TENANT_ID)
  const [proxies, setProxies] = useState<Proxy[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [probes, setProbes] = useState<Record<number, RowProbe>>({})
  const [editingId, setEditingId] = useState<number | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const reload = () => setReloadKey((k) => k + 1)
  const rows = mapProxyRows(proxies)
  const columns: DataListColumn<ProxyTableRow>[] = [
    { key: 'name', label: '名称', render: (row) => row.name },
    { key: 'protocol', label: '协议', render: (row) => row.protocol },
    { key: 'address', label: '地址', render: (row) => <span className="hk-mono">{row.address}</span> },
    {
      key: 'status',
      label: '状态',
      render: (row) => (
        <select
          value={row.status}
          onChange={(e) => void onStatusChange(row.proxy, e.target.value)}
          style={{ color: toneColor[statusTone(row.status)], border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', padding: '2px 6px', fontSize: 12, background: 'transparent' }}
        >
          {STATUSES.map((status) => <option key={status} value={status}>{status}</option>)}
        </select>
      ),
    },
    {
      key: 'probe',
      label: '连通性',
      render: (row) => {
        const probe = probes[row.id]
        return <span style={{ color: probe?.summary ? toneColor[probe.summary.tone] : 'var(--hk-ink-500)' }}>{probe?.testing ? '测试中…' : probe?.summary ? probe.summary.label : '—'}</span>
      },
    },
  ]

  useEffect(() => {
    const ac = new AbortController()
    setLoading(true)
    setError(null)
    setProbes({})
    setEditingId(null)
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
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>出口代理池</h1>
          <p className="hk-sub">
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
            style={{ width: 96, padding: '6px 8px', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)' }}
          />
        </label>
      </header>

      <ProxyCreateForm tenantId={tenantId} onCreated={reload} />

      {loading && <p style={{ color: 'var(--hk-ink-500)' }}>加载中…</p>}
      {error && (
        <p style={{ color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-sm)' }}>
          {error}
        </p>
      )}
      {!loading && !error && proxies.length === 0 && (
        <EmptyState title="该租户暂无出口代理" hint="可使用上方表单创建第一个出口代理。" />
      )}

      {!loading && !error && proxies.length > 0 && (
        <div className="hk-card">
          <DataListTable
            label="出口代理列表"
            rows={rows}
            rowKey={(row) => row.id}
            columns={columns}
            actions={[
              { label: (row) => probes[row.id]?.testing ? '测试中' : '测试连通', disabled: (row) => probes[row.id]?.testing ?? false, onClick: (row) => void runTest(row.id) },
              { label: (row) => editingId === row.id ? '收起编辑' : '编辑', onClick: (row) => setEditingId((id) => id === row.id ? null : row.id) },
              { label: '删除', tone: 'danger', onClick: (row) => void onDelete(row.proxy) },
            ]}
          />
          {editingId !== null && proxies.find((proxy) => proxy.id === editingId) && (
            <div style={{ padding: 'var(--hk-space-3) 12px', background: 'var(--hk-surface-sunken, transparent)' }}>
              <EditProxyForm
                tenantId={tenantId}
                proxy={proxies.find((proxy) => proxy.id === editingId)!}
                onCancel={() => setEditingId(null)}
                onSaved={() => {
                  setEditingId(null)
                  reload()
                }}
              />
            </div>
          )}
        </div>
      )}
    </div>
  )
}
