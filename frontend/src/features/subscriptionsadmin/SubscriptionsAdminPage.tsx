import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import {
  assignSubscription,
  bulkAssign,
  cancelSubscription,
  changePlan,
  createPlan,
  createSubscriptionVoucher,
  disablePlan,
  extendSubscription,
  getAssignment,
  getPlan,
  listAssignments,
  listPlans,
  resetQuota,
  revokeSubscription,
  updatePlan,
} from './api'
import {
  actorLabel,
  auditEventLabel,
  buildExtendRequest,
  buildPlanRequest,
  buildVoucherRequest,
  centsToUsd,
  EMPTY_VOUCHER_FORM,
  parseBulkUserIDs,
  planStatusLabel,
  planToForm,
  planTone,
  subscriptionTone,
  type ExtendMode,
  type VoucherFormState,
} from './subscriptions'
import {
  DEFAULT_TENANT_ID,
  EMPTY_PLAN_FORM,
  type AdminSubscription,
  type AuditEvent,
  type BulkAssignUserResult,
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
  // 套餐详情:走独立的 GET /plans/{id} 端点拉取(列表已有数据,但详情端点单独验证)。
  const [detailId, setDetailId] = useState<number | null>(null)

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
                      <button type="button" style={linkBtn} onClick={() => setDetailId(p.id)}>
                        详情
                      </button>
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

      <VoucherPanel
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

      {detailId != null && (
        <PlanDetailModal tenantID={tenantID} planId={detailId} onClose={() => setDetailId(null)} />
      )}
    </div>
  )
}

/* ---- 套餐详情(独立 GET /plans/{id} 端点) ---- */
function PlanDetailModal({ tenantID, planId, onClose }: { tenantID: number; planId: number; onClose: () => void }) {
  const [plan, setPlan] = useState<Plan | null>(null)
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    getPlan(planId, tenantID, ctrl.signal)
      .then((resp) => setPlan(resp.plan))
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setErr(toMsg(e, '加载套餐详情失败'))
      })
    return () => ctrl.abort()
  }, [planId, tenantID])

  return (
    <Overlay onClose={onClose}>
      <div style={{ ...modal, width: 'min(440px, 92vw)' }}>
        <h2 style={{ fontSize: 18, margin: 0 }}>套餐详情 #{planId}</h2>
        {err && <Banner tone="danger">{err}</Banner>}
        {!plan && !err ? (
          <div style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>加载中…</div>
        ) : plan ? (
          <dl style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '6px 16px', fontSize: 13, margin: 0 }}>
            <DetailRow k="名称" v={plan.name} />
            <DetailRow k="描述" v={plan.description || '—'} />
            <DetailRow k="价格" v={`${centsToUsd(plan.price_cents)} ${plan.currency_code}`} />
            <DetailRow k="有效天数" v={`${plan.validity_days} 天`} />
            <DetailRow k="授予用户组" v={plan.granted_group || '—'} />
            <DetailRow k="日/周/月封顶(USD)" v={capCol(plan)} />
            <DetailRow k="状态" v={planStatusLabel(plan)} />
            <DetailRow k="排序值" v={String(plan.sort_order)} />
            <DetailRow k="创建时间" v={fmt(plan.created_at)} />
            <DetailRow k="更新时间" v={fmt(plan.updated_at)} />
          </dl>
        ) : null}
        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <button type="button" onClick={onClose} style={ghostBtn}>关闭</button>
        </div>
      </div>
    </Overlay>
  )
}

function DetailRow({ k, v }: { k: string; v: string }) {
  return (
    <>
      <dt style={{ color: 'var(--hk-ink-500)' }}>{k}</dt>
      <dd style={{ margin: 0, color: 'var(--hk-ink-900)' }}>{v}</dd>
    </>
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
  // 批量分配:用户 ID 多值文本 + 套餐;结果逐用户展示。
  const [bulkOpen, setBulkOpen] = useState(false)
  const [bulkResults, setBulkResults] = useState<BulkAssignUserResult[] | null>(null)
  // 订阅级动作模态(延长 / 改套餐 / 撤销)。
  const [acting, setActing] = useState<{ sub: AdminSubscription; kind: SubAction } | null>(null)
  // 单条分配详情模态(只读):展示完整字段 + 审计事件流。
  const [detailSubId, setDetailSubId] = useState<number | null>(null)

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
          <button type="button" style={ghostBtn} disabled={busy} onClick={() => setBulkOpen(true)}>
            批量分配
          </button>
        </div>
      </div>

      {bulkResults && (
        <div style={{ padding: 'var(--hk-space-4)', borderBottom: '1px solid var(--hk-line)' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 'var(--hk-space-2)' }}>
            <h3 style={{ fontSize: 14, margin: 0 }}>批量分配结果</h3>
            <button type="button" style={linkBtn} onClick={() => setBulkResults(null)}>关闭</button>
          </div>
          <div style={{ overflowX: 'auto' }}>
            <table style={table}>
              <thead>
                <tr>{['用户 ID', '结果', '说明'].map((h) => <th key={h} style={th}>{h}</th>)}</tr>
              </thead>
              <tbody>
                {bulkResults.map((r) => (
                  <tr key={r.user_id} style={{ borderTop: '1px solid var(--hk-line)' }}>
                    <td style={tdMono}>#{r.user_id}</td>
                    <td style={td}>
                      <StatusBadge tone={r.ok ? 'ok' : 'danger'}>{r.ok ? (r.idempotent ? '已存在(幂等)' : '成功') : '失败'}</StatusBadge>
                    </td>
                    <td style={{ ...td, color: 'var(--hk-ink-500)' }}>{r.ok ? `订阅 #${r.subscription?.id ?? '—'}` : r.error || '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

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
                      <button type="button" style={linkBtn} onClick={() => setDetailSubId(s.id)}>
                        详情
                      </button>
                      <button type="button" style={linkBtn} disabled={busy} onClick={() => act(resetQuota, s, '重置配额')}>
                        重置配额
                      </button>
                      <button type="button" style={linkBtn} disabled={busy} onClick={() => setActing({ sub: s, kind: 'extend' })}>
                        延长
                      </button>
                      <button type="button" style={linkBtn} disabled={busy} onClick={() => setActing({ sub: s, kind: 'change-plan' })}>
                        改套餐
                      </button>
                      <button type="button" style={linkBtnDanger} disabled={busy} onClick={() => act(cancelSubscription, s, '取消订阅')}>
                        取消
                      </button>
                      <button type="button" style={linkBtnDanger} disabled={busy} onClick={() => setActing({ sub: s, kind: 'revoke' })}>
                        撤销
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ))}

      {bulkOpen && (
        <BulkAssignModal
          tenantID={tenantID}
          plans={plans}
          onClose={() => setBulkOpen(false)}
          onDone={(results) => {
            setBulkOpen(false)
            setBulkResults(results)
            onNotice(`批量分配完成:${results.filter((r) => r.ok).length}/${results.length} 成功`)
            if (validUser) void load()
          }}
        />
      )}

      {acting && (
        <SubActionModal
          tenantID={tenantID}
          sub={acting.sub}
          kind={acting.kind}
          plans={plans}
          onClose={() => setActing(null)}
          onDone={(msg) => {
            setActing(null)
            onNotice(msg)
            void load()
          }}
        />
      )}

      {detailSubId != null && (
        <AssignmentDetailModal tenantID={tenantID} subId={detailSubId} onClose={() => setDetailSubId(null)} />
      )}
    </div>
  )
}

/* ---- 单条分配详情(只读):完整字段 + 审计事件流 ---- */
function AssignmentDetailModal({
  tenantID,
  subId,
  onClose,
}: {
  tenantID: number
  subId: number
  onClose: () => void
}) {
  const [sub, setSub] = useState<AdminSubscription | null>(null)
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [err, setErr] = useState<string | null>(null)

  useEffect(() => {
    const ctrl = new AbortController()
    getAssignment(subId, tenantID, ctrl.signal)
      .then((resp) => {
        setSub(resp.subscription)
        setEvents(resp.audit_events ?? [])
      })
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setErr(toMsg(e, '加载订阅详情失败'))
      })
    return () => ctrl.abort()
  }, [subId, tenantID])

  return (
    <Overlay onClose={onClose}>
      <div style={{ ...modal, width: 'min(560px, 92vw)' }}>
        <h2 style={{ fontSize: 18, margin: 0 }}>订阅详情 #{subId}</h2>
        {err && <Banner tone="danger">{err}</Banner>}
        {!sub && !err ? (
          <div style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>加载中…</div>
        ) : sub ? (
          <>
            <dl style={{ display: 'grid', gridTemplateColumns: 'auto 1fr', gap: '6px 16px', fontSize: 13, margin: 0 }}>
              <DetailRow k="用户 ID" v={`#${sub.user_id}`} />
              <DetailRow k="套餐 ID" v={`#${sub.plan_id}`} />
              <DetailRow k="状态" v={sub.status} />
              <DetailRow k="来源" v={subSourceLabel(sub.source)} />
              <DetailRow k="授予用户组" v={sub.granted_group || '—'} />
              <DetailRow k="日/周/月封顶(USD)" v={subCapCol(sub)} />
              <DetailRow k="生效时间" v={fmt(sub.starts_at)} />
              <DetailRow k="到期时间" v={fmt(sub.expires_at)} />
              <DetailRow k="取消时间" v={sub.cancelled_at ? fmt(sub.cancelled_at) : '—'} />
              <DetailRow k="创建时间" v={fmt(sub.created_at)} />
              <DetailRow k="分配管理员" v={sub.assigned_by_admin_id ? `#${sub.assigned_by_admin_id}` : '—'} />
              <DetailRow k="原用户组" v={sub.prev_user_group || '—'} />
            </dl>

            <div>
              <h3 style={{ fontSize: 14, margin: '0 0 var(--hk-space-2)' }}>审计事件({events.length})</h3>
              {events.length === 0 ? (
                <div style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>暂无审计事件。</div>
              ) : (
                <div style={{ overflowX: 'auto' }}>
                  <table style={table}>
                    <thead>
                      <tr>
                        {['事件', '操作者', '原因', '发生时间'].map((h) => (
                          <th key={h} style={th}>
                            {h}
                          </th>
                        ))}
                      </tr>
                    </thead>
                    <tbody>
                      {events.map((ev, i) => (
                        <tr key={`${ev.event_type}-${ev.occurred_at}-${i}`} style={{ borderTop: '1px solid var(--hk-line)' }}>
                          <td style={td}>{auditEventLabel(ev.event_type)}</td>
                          <td style={td}>{actorLabel(ev.actor_kind, ev.actor_id)}</td>
                          <td style={{ ...td, color: 'var(--hk-ink-500)' }}>{ev.reason_class || '—'}</td>
                          <td style={tdMono}>{fmt(ev.occurred_at)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          </>
        ) : null}
        <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
          <button type="button" onClick={onClose} style={ghostBtn}>关闭</button>
        </div>
      </div>
    </Overlay>
  )
}

/** 订阅来源 → 中文。字面值对齐后端 internal/subscription/types.go:34(SourceAdmin 等)。 */
function subSourceLabel(source: string): string {
  switch (source) {
    case 'admin':
      return '管理员分配'
    case 'order':
      return '订单购买'
    case 'voucher':
      return '兑换券'
    default:
      return source || '—'
  }
}

/** 订阅级日/周/月封顶展示(null=不限,∞)。与 capCol(套餐版)同形,但取订阅字段。 */
function subCapCol(s: AdminSubscription): string {
  const d = s.daily_cap_usd ?? '∞'
  const w = s.weekly_cap_usd ?? '∞'
  const m = s.monthly_cap_usd ?? '∞'
  return `${d} / ${w} / ${m}`
}

/* ---- 订阅级动作:延长 / 改套餐 / 撤销(均 money,改权益) ---- */
type SubAction = 'extend' | 'change-plan' | 'revoke'

const SUB_ACTION_LABEL: Record<SubAction, string> = {
  extend: '延长有效期',
  'change-plan': '改套餐',
  revoke: '撤销订阅',
}

function SubActionModal({
  tenantID,
  sub,
  kind,
  plans,
  onClose,
  onDone,
}: {
  tenantID: number
  sub: AdminSubscription
  kind: SubAction
  plans: Plan[]
  onClose: () => void
  onDone: (msg: string) => void
}) {
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  // extend
  const [extMode, setExtMode] = useState<ExtendMode>('days')
  const [days, setDays] = useState('30')
  const [until, setUntil] = useState('')
  // change-plan
  const [newPlanId, setNewPlanId] = useState('')
  const [allowDowngrade, setAllowDowngrade] = useState(false)
  // revoke
  const [reason, setReason] = useState('')

  const submit = async () => {
    setErr(null)
    try {
      if (kind === 'extend') {
        const built = buildExtendRequest(extMode, tenantID, days, until)
        if ('error' in built) {
          setErr(built.error)
          return
        }
        setBusy(true)
        await extendSubscription(sub.id, built.request)
        onDone(`已延长订阅 #${sub.id} 有效期`)
        return
      }
      if (kind === 'change-plan') {
        const pid = Math.trunc(Number(newPlanId.trim()))
        if (!Number.isInteger(pid) || pid <= 0) {
          setErr('请选择目标套餐')
          return
        }
        setBusy(true)
        await changePlan(sub.id, { tenant_id: tenantID, new_plan_id: pid, allow_downgrade: allowDowngrade })
        onDone(`已为订阅 #${sub.id} 切换套餐`)
        return
      }
      // revoke:破坏性,二次确认
      const r = reason.trim()
      if (r === '') {
        setErr('撤销原因必填(将写入审计)')
        return
      }
      if (!window.confirm(`确认撤销订阅 #${sub.id}(用户 #${sub.user_id})?\n此动作硬性终止订阅,不可恢复。`)) {
        return
      }
      setBusy(true)
      await revokeSubscription(sub.id, { tenant_id: tenantID, reason: r })
      onDone(`已撤销订阅 #${sub.id}`)
    } catch (e) {
      setErr(toMsg(e, `${SUB_ACTION_LABEL[kind]}失败`))
      setBusy(false)
    }
  }

  return (
    <Overlay onClose={onClose}>
      <div style={{ ...modal, width: 'min(480px, 92vw)' }}>
        <h2 style={{ fontSize: 18, margin: 0 }}>
          {SUB_ACTION_LABEL[kind]}(订阅 #{sub.id} · 用户 #{sub.user_id})
        </h2>
        <MoneyHint>
          {kind === 'revoke'
            ? '撤销将硬性终止该订阅,影响用户权益与计费,且不可恢复。'
            : '该动作改变订阅权益(有效期 / 套餐配额),影响计费,谨慎执行。'}
        </MoneyHint>
        {err && <Banner tone="danger">{err}</Banner>}

        {kind === 'extend' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
            <Field label="延长方式">
              <select value={extMode} onChange={(e) => setExtMode(e.target.value as ExtendMode)} style={inp}>
                <option value="days">按天数</option>
                <option value="until">按到期时间</option>
              </select>
            </Field>
            {extMode === 'days' ? (
              <Field label="延长天数(正整数)">
                <input value={days} onChange={(e) => setDays(e.target.value)} inputMode="numeric" style={inp} />
              </Field>
            ) : (
              <Field label="新到期时间(ISO,如 2026-12-31T00:00:00Z)">
                <input value={until} onChange={(e) => setUntil(e.target.value)} placeholder="须晚于当前时间" style={inp} />
              </Field>
            )}
          </div>
        )}

        {kind === 'change-plan' && (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
            <Field label="目标套餐">
              <select value={newPlanId} onChange={(e) => setNewPlanId(e.target.value)} style={inp}>
                <option value="">选择套餐…</option>
                {plans.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}({centsToUsd(p.price_cents)} {p.currency_code})
                  </option>
                ))}
              </select>
            </Field>
            <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: 'var(--hk-ink-700)' }}>
              <input type="checkbox" checked={allowDowngrade} onChange={(e) => setAllowDowngrade(e.target.checked)} />
              允许降级(目标套餐权益更低时需勾选)
            </label>
          </div>
        )}

        {kind === 'revoke' && (
          <Field label="撤销原因 *(写入审计)">
            <input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="如:违规 / 退款" style={inp} />
          </Field>
        )}

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--hk-space-2)' }}>
          <button type="button" onClick={onClose} style={ghostBtn}>取消</button>
          <button type="button" disabled={busy} onClick={submit} style={kind === 'revoke' ? dangerBtn : primaryBtn}>
            {busy ? '提交中…' : `确认${SUB_ACTION_LABEL[kind]}`}
          </button>
        </div>
      </div>
    </Overlay>
  )
}

/* ---- 批量分配模态 ---- */
function BulkAssignModal({
  tenantID,
  plans,
  onClose,
  onDone,
}: {
  tenantID: number
  plans: Plan[]
  onClose: () => void
  onDone: (results: BulkAssignUserResult[]) => void
}) {
  const [userIds, setUserIds] = useState('')
  const [planId, setPlanId] = useState('')
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const submit = async () => {
    setErr(null)
    const parsed = parseBulkUserIDs(userIds)
    if ('error' in parsed) {
      setErr(parsed.error)
      return
    }
    const pid = Math.trunc(Number(planId.trim()))
    if (!Number.isInteger(pid) || pid <= 0) {
      setErr('请选择套餐')
      return
    }
    setBusy(true)
    try {
      const resp = await bulkAssign({ tenant_id: tenantID, user_ids: parsed.ids, plan_id: pid })
      onDone(resp.results ?? [])
    } catch (e) {
      setErr(toMsg(e, '批量分配失败'))
      setBusy(false)
    }
  }

  return (
    <Overlay onClose={onClose}>
      <div style={{ ...modal, width: 'min(480px, 92vw)' }}>
        <h2 style={{ fontSize: 18, margin: 0 }}>批量分配套餐</h2>
        <MoneyHint>批量为多名用户分配同一套餐,逐用户结算,部分可能失败(逐条返回结果)。</MoneyHint>
        {err && <Banner tone="danger">{err}</Banner>}
        <Field label="用户 ID 列表(逗号 / 空格 / 换行分隔)">
          <textarea
            value={userIds}
            onChange={(e) => setUserIds(e.target.value)}
            rows={3}
            placeholder="如:101, 102, 103"
            style={{ ...inp, height: 'auto', padding: 'var(--hk-space-2) var(--hk-space-3)', fontFamily: 'var(--hk-font-mono)' }}
          />
        </Field>
        <Field label="套餐">
          <select value={planId} onChange={(e) => setPlanId(e.target.value)} style={inp}>
            <option value="">选择套餐…</option>
            {plans.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}({centsToUsd(p.price_cents)} {p.currency_code})
              </option>
            ))}
          </select>
        </Field>
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--hk-space-2)' }}>
          <button type="button" onClick={onClose} style={ghostBtn}>取消</button>
          <button type="button" disabled={busy} onClick={submit} style={primaryBtn}>
            {busy ? '提交中…' : '确认批量分配'}
          </button>
        </div>
      </div>
    </Overlay>
  )
}

/* ---- 订阅兑换券面板 ---- */
function VoucherPanel({
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
  const [open, setOpen] = useState(false)
  const [lastCode, setLastCode] = useState<string | null>(null)

  return (
    <div style={card}>
      <div style={{ padding: 'var(--hk-space-4)', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <div>
          <h2 style={{ fontSize: 16, margin: '0 0 2px' }}>订阅兑换券</h2>
          <p style={{ margin: 0, fontSize: 12, color: 'var(--hk-ink-500)' }}>
            生成可兑换为订阅套餐的券码;用户兑换后获得对应套餐权益。
          </p>
        </div>
        <button type="button" style={primaryBtn} onClick={() => setOpen(true)}>
          + 发兑换券
        </button>
      </div>

      {lastCode && (
        <div style={{ padding: '0 var(--hk-space-4) var(--hk-space-4)' }}>
          <Banner tone="info">
            券码已生成(仅此次显示,请复制保存):
            <code style={{ marginLeft: 8, fontFamily: 'var(--hk-font-mono)', fontWeight: 600 }}>{lastCode}</code>
          </Banner>
        </div>
      )}

      {open && (
        <VoucherModal
          tenantID={tenantID}
          plans={plans}
          onClose={() => setOpen(false)}
          onError={onError}
          onCreated={(code) => {
            setOpen(false)
            setLastCode(code ?? '(后端未回显明文,见券码列表)')
            onNotice('订阅兑换券已创建')
          }}
        />
      )}
    </div>
  )
}

function VoucherModal({
  tenantID,
  plans,
  onClose,
  onError,
  onCreated,
}: {
  tenantID: number
  plans: Plan[]
  onClose: () => void
  onError: (m: string) => void
  onCreated: (code?: string) => void
}) {
  const [f, setF] = useState<VoucherFormState>(EMPTY_VOUCHER_FORM)
  const [err, setErr] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const set = <K extends keyof VoucherFormState>(k: K, v: VoucherFormState[K]) => setF((s) => ({ ...s, [k]: v }))

  const submit = async () => {
    setErr(null)
    const built = buildVoucherRequest(f, tenantID)
    if ('error' in built) {
      setErr(built.error)
      return
    }
    setBusy(true)
    try {
      const resp = await createSubscriptionVoucher(built.request)
      onCreated(resp.code)
    } catch (e) {
      const msg = toMsg(e, '建券失败')
      setErr(msg)
      onError(msg)
      setBusy(false)
    }
  }

  return (
    <Overlay onClose={onClose}>
      <div style={modal}>
        <h2 style={{ fontSize: 18, margin: 0 }}>发订阅兑换券</h2>
        <MoneyHint>该券兑换后授予用户订阅套餐权益(money)。名义价仅信息展示,兑换不入余额。</MoneyHint>
        {err && <Banner tone="danger">{err}</Banner>}
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--hk-space-3)' }}>
          <Field label="套餐 *">
            <select value={f.planId} onChange={(e) => set('planId', e.target.value)} style={inp}>
              <option value="">选择套餐…</option>
              {plans.map((p) => (
                <option key={p.id} value={String(p.id)}>
                  {p.name}({centsToUsd(p.price_cents)} {p.currency_code})
                </option>
              ))}
            </select>
          </Field>
          <Field label="券码(留空=自动生成)">
            <input value={f.code} onChange={(e) => set('code', e.target.value)} style={inp} />
          </Field>
          <Field label="名义价(USD,信息性)">
            <input value={f.amountUsd} onChange={(e) => set('amountUsd', e.target.value)} inputMode="decimal" placeholder="如 19.99" style={inp} />
          </Field>
          <Field label="货币代码">
            <input value={f.currencyCode} onChange={(e) => set('currencyCode', e.target.value)} style={inp} />
          </Field>
          <Field label="生效时间 *(ISO)">
            <input value={f.validFrom} onChange={(e) => set('validFrom', e.target.value)} placeholder="2026-06-29T00:00:00Z" style={inp} />
          </Field>
          <Field label="失效时间 *(ISO)">
            <input value={f.validUntil} onChange={(e) => set('validUntil', e.target.value)} placeholder="2026-12-31T00:00:00Z" style={inp} />
          </Field>
          <Field label="最大兑换次数(留空=不限)">
            <input value={f.maxRedemptions} onChange={(e) => set('maxRedemptions', e.target.value)} inputMode="numeric" style={inp} />
          </Field>
          <Field label="限定用户 ID(留空=不限)">
            <input value={f.eligibleUserId} onChange={(e) => set('eligibleUserId', e.target.value)} inputMode="numeric" style={inp} />
          </Field>
        </div>
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: 'var(--hk-ink-700)' }}>
          <input type="checkbox" checked={f.singleUsePerUser} onChange={(e) => set('singleUsePerUser', e.target.checked)} />
          每用户限兑一次(single_use_per_user)
        </label>
        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--hk-space-2)' }}>
          <button type="button" onClick={onClose} style={ghostBtn}>取消</button>
          <button type="button" disabled={busy} onClick={submit} style={primaryBtn}>
            {busy ? '提交中…' : '确认发券'}
          </button>
        </div>
      </div>
    </Overlay>
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
      ? { color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }
      : { color: '#235a82', background: '#e8f1f8', border: '1px solid #cfe0ee' }
  return <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, ...s }}>{children}</div>
}

/** money 提示条:动权益 / 计费的动作统一加,提醒谨慎执行。 */
function MoneyHint({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ padding: 'var(--hk-space-2) var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 12, color: 'var(--hk-warn)', background: 'var(--hk-warn-soft)', border: '1px solid var(--hk-warn-soft)' }}>
      {children}
    </div>
  )
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
const linkBtnDanger: React.CSSProperties = { ...linkBtn, color: 'var(--hk-danger)' }
const dangerBtn: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-4)', border: '1px solid var(--hk-danger)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-danger)', color: '#fff', fontSize: 13, fontWeight: 600, cursor: 'pointer' }
