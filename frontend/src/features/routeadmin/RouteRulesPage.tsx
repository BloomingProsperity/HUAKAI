import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import {
  createRoute,
  deleteRoute,
  listRoutes,
  setRouteEnabled,
  updateRoute,
} from './api'
import {
  displayModelPattern,
  emptyForm,
  routeToForm,
  sortRoutes,
  validateCreate,
  validateUpdate,
  type RouteForm,
} from './routeadmin'
import type { Route } from './types'

/*
 * 请求路由规则引擎(routes 表)运营台。管线第 2 站(路由与池)。
 * 后端 /v1/admin/routes(admin token,platform_admin):
 *   GET/POST /v1/admin/routes、GET/PUT/DELETE /v1/admin/routes/{id}、PUT /v1/admin/routes/{id}/enabled
 *   见 controlhttp/routeadmin_handler.go:104-111 + cmd/gateway/routes.go:1098。
 * 规则把(user_group_match × model_pattern_match)映射到目标 pool_group,按 match_priority 升序裁决。
 * platform_admin 下后端要求 tenant_id 必填(query),故本页先要租户 ID 再加载。
 * 本页不碰任何 pool/registry/gateway 等碰撞包模块,只调 routes 管理端点。
 */

export function RouteRulesPage() {
  const [tenantInput, setTenantInput] = useState('')
  const [tenantId, setTenantId] = useState<number | null>(null)

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>请求路由规则</h1>
          <p className="hk-sub">
            按(用户组 × 模型模式)把请求路由到目标 pool_group,按优先级(数值小=先生效)裁决。先指定租户 ID。
          </p>
        </div>
      </header>

      <form
        onSubmit={(e) => {
          e.preventDefault()
          const v = Number(tenantInput.trim())
          setTenantId(Number.isInteger(v) && v > 0 ? v : null)
        }}
        style={{ display: 'flex', gap: 'var(--hk-space-3)', alignItems: 'flex-end', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)' }}
      >
        <Field label="租户 ID(tenant_id)">
          <input value={tenantInput} onChange={(e) => setTenantInput(e.target.value)} inputMode="numeric" placeholder="如 1" style={{ ...inp, width: 160 }} />
        </Field>
        <button type="submit" className="hk-btn hk-btn--green">加载</button>
      </form>

      {tenantId == null ? (
        <Empty>请输入正整数租户 ID 后点击「加载」。</Empty>
      ) : (
        <RulesCard tenantId={tenantId} />
      )}
    </div>
  )
}

function RulesCard({ tenantId }: { tenantId: number }) {
  const [rows, setRows] = useState<Route[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  // 编辑态:null=新建表单收起;'new'=新建;数字=编辑该 id。
  const [editing, setEditing] = useState<number | 'new' | null>(null)
  const [form, setForm] = useState<RouteForm>(emptyForm())

  const load = useCallback(
    (signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      listRoutes(tenantId, signal)
        .then((items) => setRows(sortRoutes(items)))
        .catch((e: unknown) => {
          if (signal?.aborted) return
          setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载路由规则失败')
        })
        .finally(() => {
          if (!signal?.aborted) setLoading(false)
        })
    },
    [tenantId],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load])

  // run:统一包裹改动型操作(busy/错误归一/成功提示/重载)。
  const run = async (fn: () => Promise<unknown>, okMsg: string, after?: () => void) => {
    setBusy(true)
    setError(null)
    setNotice(null)
    try {
      await fn()
      setNotice(okMsg)
      after?.()
      load()
    } catch (e) {
      setError(e instanceof ApiError ? `${e.message}(${e.code})` : '操作失败')
    } finally {
      setBusy(false)
    }
  }

  const openNew = () => {
    setForm(emptyForm())
    setEditing('new')
    setError(null)
  }
  const openEdit = (r: Route) => {
    setForm(routeToForm(r))
    setEditing(r.id)
    setError(null)
  }
  const cancelEdit = () => setEditing(null)

  const submit = () => {
    if (editing === 'new') {
      const v = validateCreate(tenantId, form)
      if (!v.ok) {
        setError(v.error)
        return
      }
      void run(() => createRoute(v.value), '已新建路由规则', () => setEditing(null))
    } else if (typeof editing === 'number') {
      const v = validateUpdate(form)
      if (!v.ok) {
        setError(v.error)
        return
      }
      void run(() => updateRoute(editing, tenantId, v.value), '已保存路由规则', () => setEditing(null))
    }
  }

  const toggleEnabled = (r: Route) => {
    const verb = r.enabled ? '停用' : '启用'
    if (!window.confirm(`${verb}路由规则「${r.name}」?${r.enabled ? '停用后该规则将不再参与选路。' : ''}`)) return
    void run(() => setRouteEnabled(r.id, tenantId, !r.enabled), `已${verb}`)
  }

  const remove = (r: Route) => {
    if (!window.confirm(`删除路由规则「${r.name}」?此操作不可撤销(软删)。`)) return
    void run(() => deleteRoute(r.id, tenantId), '已删除')
  }

  return (
    <section className="hk-card">
      <div className="hk-card__head">
        <h3>路由规则(租户 #{tenantId})</h3>
        <div style={{ marginLeft: 'auto', display: 'flex', gap: 'var(--hk-space-2)', alignItems: 'center' }}>
          <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>共 {rows.length} 条</span>
          <button type="button" disabled={busy || editing === 'new'} onClick={openNew} className="hk-btn hk-btn--green hk-btn--sm">
            新建规则
          </button>
        </div>
      </div>

      {error && <Banner kind="error">{error}</Banner>}
      {notice && <Banner kind="ok">{notice}</Banner>}

      {editing === 'new' && (
        <RuleForm mode="new" form={form} setForm={setForm} busy={busy} onSubmit={submit} onCancel={cancelEdit} />
      )}

      {loading && rows.length === 0 ? (
        <Empty>加载中…</Empty>
      ) : rows.length === 0 ? (
        <Empty>该租户暂无路由规则。新建第一条以把请求按用户组/模型路由到 pool_group。</Empty>
      ) : (
        <div className="hk-tablewrap">
          <table className="hk-table">
            <thead>
              <tr>
                {['优先级', '规则名', '用户组匹配', '模型模式', 'pool_group', '状态', ''].map((h) => (
                  <th key={h}>{h}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <RuleRow
                  key={r.id}
                  route={r}
                  editing={editing === r.id}
                  form={form}
                  setForm={setForm}
                  busy={busy}
                  onEdit={() => openEdit(r)}
                  onCancel={cancelEdit}
                  onSubmit={submit}
                  onToggle={() => toggleEnabled(r)}
                  onDelete={() => remove(r)}
                />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

// 单行:非编辑态展示规则;编辑态内联展开一个 RuleForm(跨整行)。
function RuleRow({
  route, editing, form, setForm, busy, onEdit, onCancel, onSubmit, onToggle, onDelete,
}: {
  route: Route
  editing: boolean
  form: RouteForm
  setForm: (f: RouteForm) => void
  busy: boolean
  onEdit: () => void
  onCancel: () => void
  onSubmit: () => void
  onToggle: () => void
  onDelete: () => void
}) {
  if (editing) {
    return (
      <tr>
        <td colSpan={7} style={{ padding: 0 }}>
          <RuleForm mode="edit" form={form} setForm={setForm} busy={busy} onSubmit={onSubmit} onCancel={onCancel} />
        </td>
      </tr>
    )
  }
  return (
    <tr>
      <td className="hk-mono">{route.match_priority}</td>
      <td>{route.name}</td>
      <td className="hk-mono">{route.user_group_match}</td>
      <td className="hk-mono">{displayModelPattern(route.model_pattern_match)}</td>
      <td className="hk-mono">#{route.pool_group_id}</td>
      <td>
        <StatusBadge tone={route.enabled ? 'ok' : 'muted'}>{route.enabled ? '启用' : '停用'}</StatusBadge>
      </td>
      <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
        <button type="button" disabled={busy} onClick={onEdit} className="hk-btn hk-btn--sm">编辑</button>
        <button type="button" disabled={busy} onClick={onToggle} className="hk-btn hk-btn--sm" style={{ marginLeft: 'var(--hk-space-2)' }}>{route.enabled ? '停用' : '启用'}</button>
        <button type="button" disabled={busy} onClick={onDelete} className="hk-btn hk-btn--sm hk-btn--danger" style={{ marginLeft: 'var(--hk-space-2)' }}>删除</button>
      </td>
    </tr>
  )
}

// 新建/编辑共用表单。编辑态隐含全替换语义,优先级始终显式提交(留空回落默认 100)。
function RuleForm({
  mode, form, setForm, busy, onSubmit, onCancel,
}: {
  mode: 'new' | 'edit'
  form: RouteForm
  setForm: (f: RouteForm) => void
  busy: boolean
  onSubmit: () => void
  onCancel: () => void
}) {
  const setF = <K extends keyof RouteForm>(k: K, v: RouteForm[K]) => setForm({ ...form, [k]: v })
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-4)', background: 'var(--hk-surface-sunken)', borderBottom: '1px solid var(--hk-line)' }}>
      <div style={{ display: 'flex', gap: 'var(--hk-space-3)', flexWrap: 'wrap', alignItems: 'flex-end' }}>
        <Field label="规则名(name)">
          <input value={form.name} onChange={(e) => setF('name', e.target.value)} placeholder="如 vip-claude" style={{ ...inp, width: 200 }} />
        </Field>
        <Field label="用户组匹配(user_group_match)">
          <input value={form.userGroupMatch} onChange={(e) => setF('userGroupMatch', e.target.value)} placeholder="如 vip" style={{ ...inp, width: 160 }} />
        </Field>
        <Field label="模型模式(model_pattern_match,留空=全部)">
          <input value={form.modelPatternMatch} onChange={(e) => setF('modelPatternMatch', e.target.value)} placeholder="如 claude-*" style={{ ...inp, width: 180, fontFamily: 'var(--hk-font-mono)' }} />
        </Field>
        <Field label="目标 pool_group_id">
          <input value={form.poolGroupId} onChange={(e) => setF('poolGroupId', e.target.value)} inputMode="numeric" placeholder="如 7" style={{ ...inp, width: 120 }} />
        </Field>
        <Field label="优先级(match_priority,留空=100)">
          <input value={form.matchPriority} onChange={(e) => setF('matchPriority', e.target.value)} inputMode="numeric" placeholder="100" style={{ ...inp, width: 120 }} />
        </Field>
      </div>
      <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
        <button type="button" disabled={busy} onClick={onSubmit} className="hk-btn hk-btn--green">
          {busy ? '提交中…' : mode === 'new' ? '创建' : '保存'}
        </button>
        <button type="button" disabled={busy} onClick={onCancel} className="hk-btn">取消</button>
      </div>
    </div>
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
function Empty({ children }: { children: React.ReactNode }) {
  return <div className="hk-empty">{children}</div>
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
