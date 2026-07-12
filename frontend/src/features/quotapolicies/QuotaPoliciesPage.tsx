import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import { deleteQuotaPolicy, listQuotaPolicies } from './api'
import { QuotaPolicyForm } from './QuotaPolicyForm'
import {
  METRICS,
  SCOPE_KINDS,
  emptyPolicyForm,
  formatDecimal,
  metricLabel,
  modeLabel,
  modeTone,
  policyToForm,
  scopeKindLabel,
  windowKindLabel,
} from './quotapolicies'
import { EMPTY_FILTERS, type PolicyFilters, type PolicyForm, type QuotaPolicy } from './types'

/*
 * 配额策略(防滥用限流)运营台。管线「路由与池」下的限流规则配置。
 * 后端 /admin/v1/quota-policies(admin token,quota_policy_crud.go + cmd/gateway/routes.go:905-909):
 *   列表(scope_kind/metric/enabled 筛选 + 分页)、新建、编辑、删除。
 * 这是防滥用运维配置,绝不触碰余额/计费账本(routes.go 包注释)。
 *
 * tenant_id:platform_admin 必填(routes.go:124);tenant_operator 留空走自身作用域。
 * 故本页提供一个可选的租户 ID 输入:留空=用 operator 自身作用域;platform_admin 须填。
 * limit_value/burst_value 全程字符串原样渲染(formatDecimal 仅裁展示尾随 0),防精度丢失。
 *
 * 高影响动作:enforce 模式保存二次确认(在表单内)、删除二次确认(本页 prompt)。
 */

const PAGE_SIZE = 50

export function QuotaPoliciesPage() {
  const [tenantInput, setTenantInput] = useState('')
  // tenantId:0 表示「用 operator 自身作用域」(不下发 tenant_id);>0 表示显式指定。
  const [tenantId, setTenantId] = useState<number>(0)
  // loaded:是否已点击「加载」(避免进页面就空查;首次须显式触发)。
  const [loaded, setLoaded] = useState(false)

  return (
    <div style={{ padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>配额策略</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          防滥用限流:按作用域(全局/用户/Key/渠道/池分组/上游账号)对请求数、Token、成本、并发设上限。
          platform_admin 须填租户 ID;tenant_operator 留空即用自身作用域。
        </p>
      </header>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          const raw = tenantInput.trim()
          const v = raw === '' ? 0 : Number(raw)
          setTenantId(Number.isInteger(v) && v >= 0 ? v : 0)
          setLoaded(true)
        }}
        style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)' }}
      >
        <Field label="租户 ID(tenant_id,operator 可留空)">
          <input value={tenantInput} onChange={(e) => setTenantInput(e.target.value)} inputMode="numeric" placeholder="留空=自身作用域" style={{ ...inp, width: 200 }} />
        </Field>
        <button type="submit" style={primaryBtn}>
          加载
        </button>
      </form>

      {!loaded ? (
        <Empty>点击「加载」按当前租户作用域列出配额策略。</Empty>
      ) : (
        <PolicyList tenantId={tenantId} />
      )}
    </div>
  )
}

function PolicyList({ tenantId }: { tenantId: number }) {
  const [rows, setRows] = useState<QuotaPolicy[]>([])
  const [offset, setOffset] = useState(0)
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [busyId, setBusyId] = useState<number | null>(null)
  const [draft, setDraft] = useState<PolicyFilters>(EMPTY_FILTERS)
  const [filters, setFilters] = useState<PolicyFilters>(EMPTY_FILTERS)
  // 编辑/新建表单态:open=是否展示;existing=编辑目标(null=新建);initial=表单初值。
  const [editor, setEditor] = useState<{ existing: QuotaPolicy | null; initial: PolicyForm } | null>(null)

  const fetchPage = useCallback(
    async (off: number, append: boolean, signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      try {
        const resp = await listQuotaPolicies(tenantId, filters, PAGE_SIZE, off, signal)
        const items = resp.items ?? []
        setRows((prev) => (append ? [...prev, ...items] : items))
        setOffset(off + items.length)
        setHasMore(items.length === PAGE_SIZE)
      } catch (e) {
        if (signal?.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载配额策略失败')
      } finally {
        if (!signal?.aborted) setLoading(false)
      }
    },
    [tenantId, filters],
  )

  // tenantId / filters 变更:从头加载。
  useEffect(() => {
    const ctrl = new AbortController()
    setRows([])
    setOffset(0)
    setNotice(null)
    void fetchPage(0, false, ctrl.signal)
    return () => ctrl.abort()
  }, [fetchPage])

  const reload = () => {
    setNotice(null)
    void fetchPage(0, false)
  }

  const remove = (p: QuotaPolicy) => {
    if (!window.confirm(`删除配额策略 #${p.id}(${scopeKindLabel(p.scope_kind)} / ${metricLabel(p.metric)})?此操作不可撤销。`)) {
      return
    }
    const reason = window.prompt('删除原因(供审计,可留空):', '') ?? ''
    setBusyId(p.id)
    setError(null)
    setNotice(null)
    deleteQuotaPolicy(p.id, tenantId, reason)
      .then(() => {
        setNotice(`已删除 #${p.id}`)
        reload()
      })
      .catch((e: unknown) => setError(e instanceof ApiError ? `${e.message}(${e.code})` : '删除失败'))
      .finally(() => setBusyId(null))
  }

  return (
    <section style={card}>
      <div style={cardHead}>
        <h2 style={{ fontSize: 15, margin: 0 }}>策略列表</h2>
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)' }}>
          <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>已载 {rows.length} 条</span>
          <button type="button" onClick={() => setEditor({ existing: null, initial: emptyPolicyForm() })} style={primaryBtn}>
            新建策略
          </button>
        </div>
      </div>

      {/* 筛选条 */}
      <form
        onSubmit={(e) => {
          e.preventDefault()
          setFilters(draft)
        }}
        style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)', flexWrap: 'wrap' }}
      >
        <Field label="作用域类型">
          <select value={draft.scopeKind} onChange={(e) => setDraft({ ...draft, scopeKind: e.target.value as PolicyFilters['scopeKind'] })} style={{ ...inp, width: 160 }}>
            <option value="">全部</option>
            {SCOPE_KINDS.map((v) => (
              <option key={v} value={v}>
                {scopeKindLabel(v)}
              </option>
            ))}
          </select>
        </Field>
        <Field label="指标">
          <select value={draft.metric} onChange={(e) => setDraft({ ...draft, metric: e.target.value as PolicyFilters['metric'] })} style={{ ...inp, width: 160 }}>
            <option value="">全部</option>
            {METRICS.map((v) => (
              <option key={v} value={v}>
                {metricLabel(v)}
              </option>
            ))}
          </select>
        </Field>
        <Field label="启用态">
          <select value={draft.enabled} onChange={(e) => setDraft({ ...draft, enabled: e.target.value as PolicyFilters['enabled'] })} style={{ ...inp, width: 120 }}>
            <option value="">全部</option>
            <option value="true">已启用</option>
            <option value="false">已停用</option>
          </select>
        </Field>
        <button type="submit" style={primaryBtn}>
          查询
        </button>
        <button
          type="button"
          onClick={() => {
            setDraft(EMPTY_FILTERS)
            setFilters(EMPTY_FILTERS)
          }}
          style={ghostBtn}
        >
          重置
        </button>
      </form>

      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      {loading && rows.length === 0 ? (
        <Empty>加载中…</Empty>
      ) : rows.length === 0 ? (
        <Empty>当前作用域暂无配额策略。</Empty>
      ) : (
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr>
                {['ID', '作用域', '作用域 ID', '指标', '窗口', '上限', '突发', '模式', '优先级', '状态', ''].map((h) => (
                  <th key={h} style={th}>
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((p) => (
                <tr key={p.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                  <td style={tdMono}>#{p.id}</td>
                  <td style={td}>{scopeKindLabel(p.scope_kind)}</td>
                  <td style={tdMono}>{p.scope_id}</td>
                  <td style={td}>{metricLabel(p.metric)}</td>
                  <td style={td}>
                    {windowKindLabel(p.window_kind)}
                    {p.window_kind === 'fixed' && p.window_seconds > 0 ? ` · ${p.window_seconds}s` : ''}
                  </td>
                  {/* 十进制原样字符串,仅裁展示尾随 0,绝不 Number() 化。 */}
                  <td style={tdMono}>{formatDecimal(p.limit_value)}</td>
                  <td style={tdMono}>{formatDecimal(p.burst_value)}</td>
                  <td style={td}>
                    <StatusBadge tone={modeTone(p.mode)}>{modeLabel(p.mode)}</StatusBadge>
                  </td>
                  <td style={tdMono}>{p.priority}</td>
                  <td style={td}>
                    <StatusBadge tone={p.enabled ? 'ok' : 'muted'}>{p.enabled ? '启用' : '停用'}</StatusBadge>
                  </td>
                  <td style={{ ...td, textAlign: 'right', whiteSpace: 'nowrap' }}>
                    <button type="button" disabled={busyId === p.id} onClick={() => setEditor({ existing: p, initial: policyToForm(p) })} style={linkBtn}>
                      编辑
                    </button>
                    <button type="button" disabled={busyId === p.id} onClick={() => remove(p)} style={dangerLink}>
                      {busyId === p.id ? '处理中…' : '删除'}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {hasMore && (
        <div style={{ padding: 'var(--hk-space-4)', display: 'flex', justifyContent: 'center' }}>
          <button type="button" disabled={loading} onClick={() => void fetchPage(offset, true)} style={ghostBtn}>
            {loading ? '加载中…' : '加载更多'}
          </button>
        </div>
      )}

      {editor && (
        <QuotaPolicyForm
          tenantId={tenantId}
          existing={editor.existing}
          initial={editor.initial}
          onClose={() => setEditor(null)}
          onSaved={(saved) => {
            setEditor(null)
            setNotice(editor.existing ? `已更新 #${saved.id}` : `已创建 #${saved.id}`)
            reload()
          }}
        />
      )}
    </section>
  )
}

/* ——— 本页私有小组件 / 样式 ——— */
function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}
function Banner({ kind, children }: { kind: 'error' | 'ok'; children: React.ReactNode }) {
  const palette =
    kind === 'error'
      ? { color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
      : { color: '#0b6553', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
  return <div style={{ margin: 'var(--hk-space-4)', marginBottom: 0, padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>{children}</div>
}
function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

const card: React.CSSProperties = { background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }
const cardHead: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)', background: 'var(--hk-surface-sunken)' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'middle' }
const tdMono: React.CSSProperties = { ...td, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const linkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-600)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
const dangerLink: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-danger)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
