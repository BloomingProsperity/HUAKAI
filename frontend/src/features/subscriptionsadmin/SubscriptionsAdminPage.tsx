import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import {
  assignSubscription,
  cancelSubscription,
  createPlan,
  disablePlan,
  listAssignments,
  listPlans,
  resetQuota,
  updatePlan,
} from './api'
import {
  buildPlanRequest,
  centsToUsd,
  planStatusLabel,
  planToForm,
  planTone,
  subscriptionTone,
} from './subscriptions'
import {
  DEFAULT_TENANT_ID,
  EMPTY_PLAN_FORM,
  type AdminSubscription,
  type Plan,
  type PlanFormState,
} from './types'

/*
 * 套餐管理(运营台/admin)。管线计费侧:运营者维护订阅套餐(配额/价格/周期)并给用户分配/取消/重置配额。
 * 端点根 /v1/admin/subscriptions(admin token,真码 subscriptionhttp/handler.go:251)。
 * 单租户部署默认租户 DEFAULT_TENANT_ID;所有写动作 money-gated,走既有 admin 端点。
 */
export function SubscriptionsAdminPage() {
  const tenantID = DEFAULT_TENANT_ID
  const [plans, setPlans] = useState<Plan[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [editing, setEditing] = useState<{ plan: Plan | null } | null>(null)

  const loadPlans = useCallback(
    (signal?: AbortSignal) => {
      setLoading(true)
      setError(null)
      listPlans(tenantID, signal)
        .then((resp) => setPlans(resp.plans ?? []))
        .catch((e: unknown) => {
          if (signal?.aborted) return
          setError(toMsg(e, '加载套餐失败'))
        })
        .finally(() => {
          if (!signal?.aborted) setLoading(false)
        })
    },
    [tenantID],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    loadPlans(ctrl.signal)
    return () => ctrl.abort()
  }, [loadPlans])

  const onDisable = async (p: Plan) => {
    setError(null)
    setNotice(null)
    try {
      await disablePlan(p.id, tenantID)
      setNotice(`已停用套餐「${p.name}」`)
      loadPlans()
    } catch (e) {
      setError(toMsg(e, '停用套餐失败'))
    }
  }

  const onSaved = (msg: string) => {
    setEditing(null)
    setNotice(msg)
    loadPlans()
  }

  return (
    <div style={page}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <h1 style={{ fontSize: 22 }}>套餐管理</h1>
        <p style={{ color: 'var(--hk-ink-500)', margin: 0, fontSize: 13 }}>
          订阅套餐(配额 / 价格 / 周期)维护与用户分配。共 {plans.length} 个套餐。
        </p>
      </header>

      {error && <Banner tone="danger">{error}</Banner>}
      {notice && <Banner tone="info">{notice}</Banner>}

      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <button type="button" style={primaryBtn} onClick={() => setEditing({ plan: null })}>
          + 新建套餐
        </button>
      </div>

      <div style={card}>
        {loading && plans.length === 0 ? (
          <Empty>加载中…</Empty>
        ) : plans.length === 0 ? (
          <Empty>暂无套餐,点「新建套餐」创建第一个。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={table}>
              <thead>
                <tr>
                  {['套餐', '价格', '周期', '日/周/月封顶(USD)', '用户组', '状态', '操作'].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {plans.map((p) => (
                  <tr key={p.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={td}>
                      <div style={{ display: 'flex', flexDirection: 'column' }}>
                        <span style={{ fontWeight: 600, color: 'var(--hk-ink-900)' }}>{p.name}</span>
                        {p.description && (
                          <span style={{ fontSize: 11, color: 'var(--hk-ink-300)' }}>{p.description}</span>
                        )}
                      </div>
                    </td>
                    <td style={tdMono}>
                      {centsToUsd(p.price_cents)} {p.currency_code}
                    </td>
                    <td style={td}>{p.validity_days} 天</td>
                    <td style={tdMono}>{capCol(p)}</td>
                    <td style={td}>{p.granted_group || '—'}</td>
                    <td style={td}>
                      <StatusBadge tone={planTone(p)}>{planStatusLabel(p)}</StatusBadge>
                    </td>
                    <td style={{ ...td, whiteSpace: 'nowrap' }}>
                      <button type="button" style={linkBtn} onClick={() => setEditing({ plan: p })}>
                        编辑
                      </button>
                      {p.enabled && (
                        <button type="button" style={linkBtnDanger} onClick={() => onDisable(p)}>
                          停用
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      <AssignmentPanel
        tenantID={tenantID}
        plans={plans}
        onError={(m) => setError(m)}
        onNotice={(m) => setNotice(m)}
      />

      {editing && (
        <PlanModal
          tenantID={tenantID}
          plan={editing.plan}
          onClose={() => setEditing(null)}
          onSaved={onSaved}
        />
      )}
    </div>
  )
}

/* ---- 套餐建/改模态 ---- */
function PlanModal({
  tenantID,
  plan,
  onClose,
  onSaved,
}: {
  tenantID: number
  plan: Plan | null
  onClose: () => void
  onSaved: (msg: string) => void
}) {
  const [f, setF] = useState<PlanFormState>(plan ? planToForm(plan) : EMPTY_PLAN_FORM)
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const set = <K extends keyof PlanFormState>(k: K, v: PlanFormState[K]) => setF((s) => ({ ...s, [k]: v }))

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    const built = buildPlanRequest(f, tenantID)
    if (!built.ok) {
      setErr(built.error)
      return
    }
    setBusy(true)
    setErr(null)
    try {
      if (plan) {
        await updatePlan(plan.id, built.request)
        onSaved(`已更新套餐「${built.request.name}」`)
      } else {
        await createPlan(built.request)
        onSaved(`已创建套餐「${built.request.name}」`)
      }
    } catch (e2) {
      setErr(toMsg(e2, '保存失败'))
      setBusy(false)
    }
  }

  return (
    <Overlay onClose={onClose}>
      <form onSubmit={submit} style={modal}>
        <h2 style={{ fontSize: 18, margin: 0 }}>{plan ? '编辑套餐' : '新建套餐'}</h2>
        {err && <Banner tone="danger">{err}</Banner>}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--hk-space-3)' }}>
          <Field label="套餐名称 *">
            <input value={f.name} onChange={(e) => set('name', e.target.value)} style={inp} />
          </Field>
          <Field label="货币代码">
            <input value={f.currencyCode} onChange={(e) => set('currencyCode', e.target.value)} style={inp} />
          </Field>
          <Field label="价格(USD)">
            <input value={f.priceUsd} onChange={(e) => set('priceUsd', e.target.value)} inputMode="decimal" placeholder="如 19.99" style={inp} />
          </Field>
          <Field label="有效天数 *">
            <input value={f.validityDays} onChange={(e) => set('validityDays', e.target.value)} inputMode="numeric" style={inp} />
          </Field>
          <Field label="日封顶(USD)">
            <input value={f.dailyCapUsd} onChange={(e) => set('dailyCapUsd', e.target.value)} inputMode="decimal" placeholder="留空=不限" style={inp} />
          </Field>
          <Field label="周封顶(USD)">
            <input value={f.weeklyCapUsd} onChange={(e) => set('weeklyCapUsd', e.target.value)} inputMode="decimal" placeholder="留空=不限" style={inp} />
          </Field>
          <Field label="月封顶(USD)">
            <input value={f.monthlyCapUsd} onChange={(e) => set('monthlyCapUsd', e.target.value)} inputMode="decimal" placeholder="留空=不限" style={inp} />
          </Field>
          <Field label="授予用户组">
            <input value={f.grantedGroup} onChange={(e) => set('grantedGroup', e.target.value)} style={inp} />
          </Field>
          <Field label="排序值">
            <input value={f.sortOrder} onChange={(e) => set('sortOrder', e.target.value)} inputMode="numeric" style={inp} />
          </Field>
          <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: 'var(--hk-ink-700)', alignSelf: 'flex-end', height: 32 }}>
            <input type="checkbox" checked={f.forSale} onChange={(e) => set('forSale', e.target.checked)} />
            对用户上架可售
          </label>
        </div>
        <Field label="描述">
          <input value={f.description} onChange={(e) => set('description', e.target.value)} style={inp} />
        </Field>
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--hk-space-2)' }}>
          <button type="button" onClick={onClose} style={ghostBtn}>
            取消
          </button>
          <button type="submit" disabled={busy} style={primaryBtn}>
            {busy ? '保存中…' : '保存'}
          </button>
        </div>
      </form>
    </Overlay>
  )
}

/* ---- 用户订阅分配面板 ---- */
function AssignmentPanel({
  tenantID,
  plans,
  onError,
  onNotice,
}: {
  tenantID: number
  plans: Plan[]
  onError: (m: string) => void
  onNotice: (m: string) => void
}) {
  const [userIdRaw, setUserIdRaw] = useState('')
  const [planIdRaw, setPlanIdRaw] = useState('')
  const [subs, setSubs] = useState<AdminSubscription[]>([])
  const [busy, setBusy] = useState(false)
  const [queriedUser, setQueriedUser] = useState<number | null>(null)

  const userID = (): number => Math.trunc(Number(userIdRaw.trim()))
  const validUser = Number.isInteger(userID()) && userID() > 0

  const load = async () => {
    if (!validUser) {
      onError('请输入有效的用户 ID')
      return
    }
    setBusy(true)
    onError('')
    try {
      const resp = await listAssignments(tenantID, userID())
      setSubs(resp.subscriptions ?? [])
      setQueriedUser(userID())
    } catch (e) {
      onError(toMsg(e, '加载用户订阅失败'))
    } finally {
      setBusy(false)
    }
  }

  const assign = async () => {
    const pid = Math.trunc(Number(planIdRaw.trim()))
    if (!validUser) {
      onError('请输入有效的用户 ID')
      return
    }
    if (!Number.isInteger(pid) || pid <= 0) {
      onError('请选择套餐')
      return
    }
    setBusy(true)
    onError('')
    try {
      const resp = await assignSubscription(tenantID, userID(), pid)
      onNotice(resp.idempotent ? '该用户已有此套餐(幂等)' : `已为用户 #${userID()} 分配套餐`)
      await load()
    } catch (e) {
      onError(toMsg(e, '分配套餐失败'))
      setBusy(false)
    }
  }

  const act = async (
    fn: (id: number, tid: number) => Promise<unknown>,
    sub: AdminSubscription,
    label: string,
  ) => {
    setBusy(true)
    onError('')
    try {
      await fn(sub.id, tenantID)
      onNotice(`${label}成功(订阅 #${sub.id})`)
      await load()
    } catch (e) {
      onError(toMsg(e, `${label}失败`))
      setBusy(false)
    }
  }

  return (
    <div style={card}>
      <div style={{ padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)' }}>
        <h2 style={{ fontSize: 16, margin: '0 0 var(--hk-space-3)' }}>用户订阅分配</h2>
        <div style={{ display: 'flex', gap: 'var(--hk-space-3)', flexWrap: 'wrap', alignItems: 'flex-end' }}>
          <Field label="用户 ID">
            <input value={userIdRaw} onChange={(e) => setUserIdRaw(e.target.value)} inputMode="numeric" placeholder="数字" style={{ ...inp, width: 140 }} />
          </Field>
          <Field label="套餐">
            <select value={planIdRaw} onChange={(e) => setPlanIdRaw(e.target.value)} style={{ ...inp, width: 220 }}>
              <option value="">选择套餐…</option>
              {plans.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}({centsToUsd(p.price_cents)} {p.currency_code})
                </option>
              ))}
            </select>
          </Field>
          <button type="button" style={ghostBtn} disabled={busy} onClick={load}>
            查询订阅
          </button>
          <button type="button" style={primaryBtn} disabled={busy} onClick={assign}>
            分配套餐
          </button>
        </div>
      </div>

      {queriedUser != null &&
        (subs.length === 0 ? (
          <Empty>用户 #{queriedUser} 暂无订阅。</Empty>
        ) : (
          <div style={{ overflowX: 'auto' }}>
            <table style={table}>
              <thead>
                <tr>
                  {['订阅 ID', '套餐 ID', '状态', '生效', '到期', '操作'].map((h) => (
                    <th key={h} style={th}>
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {subs.map((s) => (
                  <tr key={s.id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={tdMono}>#{s.id}</td>
                    <td style={tdMono}>{s.plan_id}</td>
                    <td style={td}>
                      <StatusBadge tone={subscriptionTone(s.status)}>{s.status}</StatusBadge>
                    </td>
                    <td style={tdMono}>{fmt(s.starts_at)}</td>
                    <td style={tdMono}>{fmt(s.expires_at)}</td>
                    <td style={{ ...td, whiteSpace: 'nowrap' }}>
                      <button type="button" style={linkBtn} disabled={busy} onClick={() => act(resetQuota, s, '重置配额')}>
                        重置配额
                      </button>
                      <button type="button" style={linkBtnDanger} disabled={busy} onClick={() => act(cancelSubscription, s, '取消订阅')}>
                        取消
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ))}
    </div>
  )
}

/* ---- 小工具 ---- */
function capCol(p: Plan): string {
  const d = p.daily_cap_usd ?? '∞'
  const w = p.weekly_cap_usd ?? '∞'
  const m = p.monthly_cap_usd ?? '∞'
  return `${d} / ${w} / ${m}`
}

function toMsg(e: unknown, fallback: string): string {
  return e instanceof ApiError ? `${e.message}(${e.code})` : fallback
}

function fmt(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString('zh-CN')
}

function Overlay({ children, onClose }: { children: React.ReactNode; onClose: () => void }) {
  return (
    <div
      onClick={onClose}
      style={{ position: 'fixed', inset: 0, background: 'rgba(28,38,34,0.32)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 'var(--hk-z-overlay)' as unknown as number, padding: 'var(--hk-space-4)' }}
    >
      <div onClick={(e) => e.stopPropagation()}>{children}</div>
    </div>
  )
}

function Banner({ tone, children }: { tone: 'danger' | 'info'; children: React.ReactNode }) {
  const s =
    tone === 'danger'
      ? { color: '#8f322a', background: '#fbe9e7', border: '1px solid #f2cdc8' }
      : { color: '#235a82', background: '#e8f1f8', border: '1px solid #cfe0ee' }
  return <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...s }}>{children}</div>
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label style={{ display: 'flex', flexDirection: 'column', gap: 4, fontSize: 12, color: 'var(--hk-ink-500)' }}>
      {label}
      {children}
    </label>
  )
}

function Empty({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-8)', textAlign: 'center', color: 'var(--hk-ink-500)', fontSize: 13 }}>{children}</div>
}

const page: React.CSSProperties = { padding: 'var(--hk-space-6)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }
const card: React.CSSProperties = { background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-1)', overflow: 'hidden' }
const table: React.CSSProperties = { width: '100%', borderCollapse: 'collapse', fontSize: 13 }
const modal: React.CSSProperties = { background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', boxShadow: 'var(--hk-shadow-3)', padding: 'var(--hk-space-5)', width: 'min(640px, 92vw)', display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)', maxHeight: '90vh', overflowY: 'auto' }
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
const th: React.CSSProperties = { textAlign: 'left', padding: 'var(--hk-space-3) var(--hk-space-4)', fontSize: 12, fontWeight: 600, color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', whiteSpace: 'nowrap' }
const td: React.CSSProperties = { padding: 'var(--hk-space-3) var(--hk-space-4)', verticalAlign: 'top' }
const tdMono: React.CSSProperties = { ...td, fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-700)', whiteSpace: 'nowrap' }
const primaryBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-primary-500)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
const ghostBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontSize: 13, cursor: 'pointer' }
const linkBtn: React.CSSProperties = { border: 'none', background: 'transparent', color: 'var(--hk-primary-700)', fontSize: 13, cursor: 'pointer', padding: '0 var(--hk-space-2)' }
const linkBtnDanger: React.CSSProperties = { ...linkBtn, color: '#8f322a' }
