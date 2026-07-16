import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge } from '../../ui/StatusBadge'
import { deleteQuotaPolicy, listQuotaPolicies } from './api'
import { QuotaPolicyForm } from './QuotaPolicyForm'
import {
  METRICS,
  SCOPE_KINDS,
  emptyPolicyForm,
  mapQuotaPolicyRows,
  metricLabel,
  policyToForm,
  scopeKindLabel,
  type QuotaPolicyTableRow,
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
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>配额策略</h1>
          <p className="hk-sub">
            防滥用限流:按作用域(全局/用户/Key/渠道/池分组/上游账号)对请求数、Token、成本、并发设上限。
            platform_admin 须填租户 ID;tenant_operator 留空即用自身作用域。
          </p>
        </div>
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
        <button type="submit" className="hk-btn hk-btn--green">
          加载
        </button>
      </form>

      {!loaded ? (
        <EmptyState title="尚未加载配额策略" hint="点击「加载」按当前租户作用域列出策略。" />
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
  const tableRows = mapQuotaPolicyRows(rows)

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
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>策略列表</h3>
        <div style={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)' }}>
          <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>已载 {rows.length} 条</span>
          <button type="button" onClick={() => setEditor({ existing: null, initial: emptyPolicyForm() })} className="hk-btn hk-btn--green hk-btn--sm">
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
        <button type="submit" className="hk-btn hk-btn--green">
          查询
        </button>
        <button
          type="button"
          onClick={() => {
            setDraft(EMPTY_FILTERS)
            setFilters(EMPTY_FILTERS)
          }}
          className="hk-btn"
        >
          重置
        </button>
      </form>

      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      {loading && rows.length === 0 ? (
        <EmptyState title="正在加载配额策略" hint="请稍候。" />
      ) : rows.length === 0 ? (
        <EmptyState title="当前作用域暂无配额策略" hint="可点击「新建策略」添加限流规则。" />
      ) : (
        <DataListTable
          label="配额策略列表"
          rows={tableRows}
          rowKey={(row) => row.id}
          columns={quotaPolicyColumns}
          actions={[
            { label: '编辑', disabled: (row) => busyId === row.id, onClick: (row) => setEditor({ existing: row.policy, initial: policyToForm(row.policy) }) },
            { label: (row) => busyId === row.id ? '处理中…' : '删除', tone: 'danger', disabled: (row) => busyId === row.id, onClick: (row) => remove(row.policy) },
          ]}
        />
      )}

      {hasMore && (
        <div style={{ padding: 'var(--hk-space-4)', display: 'flex', justifyContent: 'center' }}>
          <button type="button" disabled={loading} onClick={() => void fetchPage(offset, true)} className="hk-btn">
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
      : { color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', border: '1px solid var(--hk-primary-100)' }
  return <div style={{ margin: 'var(--hk-space-4)', marginBottom: 0, padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...palette }}>{children}</div>
}

const quotaPolicyColumns: DataListColumn<QuotaPolicyTableRow>[] = [
  { key: 'id', label: 'ID', render: (row) => <span className="hk-mono">#{row.id}</span> },
  { key: 'scope', label: '作用域', render: (row) => row.scope },
  { key: 'scope-id', label: '作用域 ID', render: (row) => <span className="hk-mono">{row.scopeId}</span> },
  { key: 'metric', label: '指标', render: (row) => row.metric },
  { key: 'window', label: '窗口', render: (row) => row.window },
  { key: 'limit', label: '上限', render: (row) => <span className="hk-mono">{row.limit}</span> },
  { key: 'burst', label: '突发', render: (row) => <span className="hk-mono">{row.burst}</span> },
  { key: 'mode', label: '模式', badge: true, render: (row) => <StatusBadge tone={row.modeTone}>{row.mode}</StatusBadge> },
  { key: 'priority', label: '优先级', render: (row) => <span className="hk-mono">{row.priority}</span> },
  { key: 'status', label: '状态', badge: true, render: (row) => <StatusBadge tone={row.statusTone}>{row.status}</StatusBadge> },
]

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
