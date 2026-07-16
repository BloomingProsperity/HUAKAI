import { useCallback, useEffect, useState } from 'react'
import type { CSSProperties } from 'react'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatusBadge, type BadgeTone } from '../../ui/StatusBadge'
import {
  cancelRenew,
  changePlan,
  getCurrentSubscription,
  getProgress,
  listPlans,
  listSubscriptionHistory,
  purchasePlan,
} from './api'
import {
  buildPurchaseRequest,
  cancelRenewGuidance,
  changeablePlans,
  clampBarPercent,
  formatCaps,
  formatDate,
  formatPrice,
  formatResetCountdown,
  formatValidity,
  friendlyChangePlanError,
  isOverLimit,
  isSubscriptionActive,
  mapSubscriptionHistoryRows,
  purchaseGuidance,
  sortProgressWindows,
  subscriptionStatusLabel,
  subscriptionStatusTone,
  validateChangePlan,
  validatePurchasable,
  windowLabel,
  type SubTone,
  type SubscriptionHistoryTableRow,
} from './subscriptions'
import type {
  CurrentSubscriptionResponse,
  PlanView,
  SubscriptionProgressResponse,
  SubscriptionProgressView,
  SubscriptionView,
} from './types'

/*
 * 订阅 · 套餐与用量(user 壳)。
 * 上:我的当前订阅(状态/有效期/自动续订)+ 配额用量进度(日/周/月 %、重置倒计时、超额提示)。
 * 下:在售套餐卡片网格,购买按钮 money-gated(走 POST /purchase 建支付订单,履约后才生效)。
 * 端点全部 session 鉴权,挂载在 /v1/users/me/subscriptions(详见 ./api.ts 端点注释)。
 */
export function SubscriptionsPage() {
  const [current, setCurrent] = useState<CurrentSubscriptionResponse | null>(null)
  const [progress, setProgress] = useState<SubscriptionProgressResponse | null>(null)
  const [plans, setPlans] = useState<PlanView[]>([])
  const [history, setHistory] = useState<SubscriptionView[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)

  const [buyingId, setBuyingId] = useState<number | null>(null)
  const [buyError, setBuyError] = useState<string | null>(null)
  const [buyNotice, setBuyNotice] = useState<string | null>(null)

  const load = useCallback((signal: AbortSignal) => {
    setLoading(true)
    setError(null)
    // 四个只读端点并发拉取;progress 在无生效订阅时正常回空数组,不当作错误。
    // history(订阅历史)同为只读,身份取自 session,无生效订阅也可能有历史记录。
    Promise.all([
      getCurrentSubscription(signal),
      getProgress(signal),
      listPlans(signal),
      listSubscriptionHistory(signal),
    ])
      .then(([cur, prog, planList, hist]) => {
        if (signal.aborted) return
        setCurrent(cur)
        setProgress(prog)
        setPlans(planList.plans ?? [])
        setHistory(hist.subscriptions ?? [])
      })
      .catch((e: unknown) => {
        if (signal.aborted) return
        setError(e instanceof ApiError ? `${e.message}(${e.code})` : '加载订阅信息失败')
      })
      .finally(() => {
        if (!signal.aborted) setLoading(false)
      })
  }, [])

  useEffect(() => {
    const ctrl = new AbortController()
    load(ctrl.signal)
    return () => ctrl.abort()
  }, [load, refreshNonce])

  const buy = async (plan: PlanView) => {
    const invalid = validatePurchasable(plan)
    if (invalid) {
      setBuyError(invalid)
      setBuyNotice(null)
      return
    }
    if (!window.confirm(`确认购买套餐「${plan.name}」(${formatPrice(plan.price_cents, plan.currency_code)})?将创建一笔待支付订单。`)) {
      return
    }
    setBuyingId(plan.id)
    setBuyError(null)
    setBuyNotice(null)
    try {
      const resp = await purchasePlan(buildPurchaseRequest(plan.id))
      setBuyNotice(purchaseGuidance(resp.order.out_trade_no, resp.idempotent))
      // 建单后刷新当前订阅/进度(履约前不会变,但保持数据新鲜)。
      setRefreshNonce((n) => n + 1)
    } catch (e) {
      setBuyError(e instanceof ApiError ? friendlyPurchaseError(e.code, e.message) : '下单失败,请稍后再试')
    } finally {
      setBuyingId(null)
    }
  }

  const sub = current?.subscription ?? null
  const active = isSubscriptionActive(sub)
  const windows = sortProgressWindows(progress?.progress ?? [])
  // 订阅历史按创建时间倒序(最新在前),纯逻辑已变异测试。
  const historyRows = mapSubscriptionHistoryRows(history)
  const historyColumns: DataListColumn<SubscriptionHistoryTableRow>[] = [
    { key: 'planId', label: '套餐 ID', render: (row) => <span className="hk-mono">{row.planId}</span> },
    { key: 'status', label: '状态', render: (row) => <StatusBadge tone={toBadgeTone(row.tone)}>{row.status}</StatusBadge> },
    { key: 'group', label: '权益组', render: (row) => row.group },
    { key: 'startsAt', label: '生效时间', render: (row) => <span className="hk-mono">{row.startsAt}</span> },
    { key: 'expiresAt', label: '到期时间', render: (row) => <span className="hk-mono">{row.expiresAt}</span> },
    { key: 'cancelledAt', label: '取消时间', render: (row) => <span className="hk-mono">{row.cancelledAt}</span> },
    { key: 'createdAt', label: '创建时间', render: (row) => <span className="hk-mono">{row.createdAt}</span> },
  ]

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>订阅</h1>
          <p className="hk-sub">
            查看当前订阅与日/周/月配额用量,或选购在售套餐。购买后需完成支付,订单确认后订阅自动生效。
          </p>
        </div>
      </header>

      {error && <Banner tone="danger">{error}</Banner>}

      {/* 我的当前订阅 + 用量进度 */}
      <section className="hk-card">
        <div className="hk-card__head"><h3>我的订阅</h3></div>
        <div className="hk-card__body">
          {loading && !current ? (
            <EmptyState title="正在加载订阅" hint="请稍候。" />
          ) : !active ? (
            <EmptyState title="当前没有生效中的订阅" hint="可在下方选购一个套餐开始使用。" />
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-4)' }}>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-5)', alignItems: 'center' }}>
                <Field label="状态">
                  <StatusBadge tone={toBadgeTone(subscriptionStatusTone(sub!.status))}>
                    {subscriptionStatusLabel(sub!.status)}
                  </StatusBadge>
                </Field>
                <Field label="有效期至">{formatDate(sub!.expires_at) || '—'}</Field>
                <Field label="自动续订">{current?.auto_renew ? '已开启' : '未开启'}</Field>
                {sub!.granted_group && <Field label="权益组">{sub!.granted_group}</Field>}
              </div>

              {/* 配额用量进度 */}
              {windows.length === 0 ? (
                <p style={{ margin: 0, fontSize: 13, color: 'var(--hk-ink-500)' }}>
                  该订阅未设置日/周/月用量上限(不限额)。
                </p>
              ) : (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
                  {windows.map((w) => (
                    <QuotaBar key={w.window_kind} row={w} />
                  ))}
                </div>
              )}

              {/* 订阅自助:关闭自动续订 + 换套餐(money 相关,均二次确认) */}
              <SubscriptionSelfService
                sub={sub!}
                autoRenew={current?.auto_renew ?? false}
                plans={plans}
                onChanged={() => setRefreshNonce((n) => n + 1)}
              />
            </div>
          )}
        </div>
      </section>

      {/* 订阅历史(只读):本人全部订阅记录,含已过期/已取消/待生效 */}
      <section className="hk-card">
        <div className="hk-card__head"><h3>订阅历史</h3></div>
        {loading && history.length === 0 ? (
          <div className="hk-card__body"><EmptyState title="正在加载订阅历史" hint="请稍候。" /></div>
        ) : historyRows.length === 0 ? (
          <div className="hk-card__body"><EmptyState title="暂无订阅记录" hint="购买并完成支付后，订阅记录会显示在这里。" /></div>
        ) : (
          <DataListTable label="订阅历史" rows={historyRows} rowKey={(row) => row.id} columns={historyColumns} />
        )}
      </section>

      {/* 在售套餐 */}
      <section style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
        <h2 style={sectionTitle}>在售套餐</h2>

        {buyNotice && <Banner tone="ok">{buyNotice}</Banner>}
        {buyError && <Banner tone="danger">{buyError}</Banner>}

        {loading && plans.length === 0 ? (
          <div className="hk-card">
            <EmptyState title="正在加载在售套餐" hint="请稍候。" />
          </div>
        ) : plans.length === 0 ? (
          <div className="hk-card">
            <EmptyState title="当前没有在售套餐" hint="请稍后再来查看。" />
          </div>
        ) : (
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))',
              gap: 'var(--hk-space-4)',
            }}
          >
            {plans.map((plan) => (
              <PlanCard
                key={plan.id}
                plan={plan}
                busy={buyingId === plan.id}
                anyBusy={buyingId !== null}
                onBuy={() => buy(plan)}
              />
            ))}
          </div>
        )}
      </section>
    </div>
  )
}

/** 单个配额窗口进度条:百分比夹取宽度 + 已用/上限 + 重置倒计时;超额时切红。 */
function QuotaBar({ row }: { row: SubscriptionProgressView }) {
  const over = isOverLimit(row)
  const width = clampBarPercent(row.usage_percent)
  // 超额时百分比可能 >100,文案展示原始百分比(取整),让用户看到真实超额程度。
  const shownPercent = Math.round(row.usage_percent)
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13, color: 'var(--hk-ink-700)' }}>
        <span style={{ fontWeight: 600 }}>
          {windowLabel(row.window_kind)}用量
          {over && (
            <span style={{ marginLeft: 'var(--hk-space-2)' }}>
              <StatusBadge tone="danger">已超额</StatusBadge>
            </span>
          )}
        </span>
        <span style={{ color: 'var(--hk-ink-500)' }}>
          ${row.consumed} / ${row.cap}（{shownPercent}%）
        </span>
      </div>
      <div
        style={{
          height: 8,
          borderRadius: 'var(--hk-radius-pill)',
          background: 'var(--hk-surface-sunken)',
          overflow: 'hidden',
        }}
      >
        <div
          style={{
            height: '100%',
            width: `${width}%`,
            borderRadius: 'var(--hk-radius-pill)',
            background: over ? 'var(--hk-danger)' : 'var(--hk-primary-500)',
            transition: 'width 0.2s ease',
          }}
        />
      </div>
      <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>{formatResetCountdown(row.resets_in_seconds)}</span>
    </div>
  )
}

/**
 * 订阅自助操作区:关闭自动续订 + 换套餐。
 * - 关闭自动续订:POST /cancel-renew(只置 auto_renew=false,当前权益保留到到期)。二次确认。
 * - 换套餐:POST /change-plan {new_plan_id}(money 相关,后端仅允许升级)。下拉选目标 + 二次确认。
 * 所有写动作身份取自 session,前端绝不传 user_id;成功后刷新当前订阅。
 */
function SubscriptionSelfService({
  sub,
  autoRenew,
  plans,
  onChanged,
}: {
  sub: SubscriptionView
  autoRenew: boolean
  plans: PlanView[]
  onChanged: () => void
}) {
  const [busy, setBusy] = useState<'cancel' | 'change' | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [targetPlanId, setTargetPlanId] = useState<number>(0)

  // 可换的目标套餐:剔除当前套餐 + 只留可购(纯逻辑,已变异测试)。
  const options = changeablePlans(plans, sub.plan_id) as PlanView[]

  const doCancelRenew = async () => {
    if (!window.confirm('确认关闭自动续订?当前订阅在到期前仍可正常使用,但到期后不会自动续费。')) {
      return
    }
    setBusy('cancel')
    setError(null)
    setNotice(null)
    try {
      const resp = await cancelRenew()
      setNotice(cancelRenewGuidance(resp.subscription?.expires_at))
      onChanged()
    } catch (e) {
      setError(e instanceof ApiError ? friendlyChangePlanError(e.code, e.message) : '关闭自动续订失败,请稍后再试')
    } finally {
      setBusy(null)
    }
  }

  const doChangePlan = async () => {
    const invalid = validateChangePlan(targetPlanId, sub.plan_id)
    if (invalid) {
      setError(invalid)
      setNotice(null)
      return
    }
    const target = options.find((p) => p.id === targetPlanId)
    const label = target ? `「${target.name}」(${formatPrice(target.price_cents, target.currency_code)})` : ''
    // money 提示:换套餐可能产生新的计费窗口与权益变更,需用户明确确认。
    if (!window.confirm(`确认将套餐更换为${label}?换套餐可能改变你的配额上限与计费窗口,且仅支持升级。`)) {
      return
    }
    setBusy('change')
    setError(null)
    setNotice(null)
    try {
      await changePlan(targetPlanId)
      setNotice('套餐已更换,新的权益与配额已生效。')
      setTargetPlanId(0)
      onChanged()
    } catch (e) {
      setError(e instanceof ApiError ? friendlyChangePlanError(e.code, e.message) : '换套餐失败,请稍后再试')
    } finally {
      setBusy(null)
    }
  }

  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--hk-space-3)',
        paddingTop: 'var(--hk-space-4)',
        borderTop: '1px solid var(--hk-line)',
      }}
    >
      <h3 style={{ margin: 0, fontSize: 13, color: 'var(--hk-ink-500)' }}>订阅自助</h3>

      {error && <Banner tone="danger">{error}</Banner>}
      {notice && <Banner tone="ok">{notice}</Banner>}

      {/* 关闭自动续订:仅在当前已开启时展示 */}
      {autoRenew ? (
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-3)', flexWrap: 'wrap' }}>
          <span style={{ fontSize: 13, color: 'var(--hk-ink-700)' }}>自动续订当前已开启。</span>
          <button type="button" disabled={busy !== null} onClick={doCancelRenew} className="hk-btn hk-btn--sm">
            {busy === 'cancel' ? '处理中…' : '关闭自动续订'}
          </button>
        </div>
      ) : (
        <span style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>自动续订未开启,到期后需手动续订。</span>
      )}

      {/* 换套餐:有可换目标时展示下拉 + 按钮 */}
      {options.length > 0 ? (
        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', flexWrap: 'wrap' }}>
          <label style={{ fontSize: 13, color: 'var(--hk-ink-700)' }}>换套餐:</label>
          <select
            value={targetPlanId}
            onChange={(e) => setTargetPlanId(Number(e.target.value))}
            aria-label="选择目标套餐"
            disabled={busy !== null}
            style={selectActionStyle}
          >
            <option value={0}>选择目标套餐…</option>
            {options.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}（{formatPrice(p.price_cents, p.currency_code)}）
              </option>
            ))}
          </select>
          <button
            type="button"
            disabled={busy !== null || targetPlanId <= 0}
            onClick={doChangePlan}
            className="hk-btn hk-btn--sm"
          >
            {busy === 'change' ? '更换中…' : '更换套餐'}
          </button>
          <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>仅支持升级,降级请联系管理员。</span>
        </div>
      ) : (
        <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>暂无可更换的其它在售套餐。</span>
      )}
    </div>
  )
}

/** 单个套餐卡:名称/简介/价格/有效期/三档上限 + 购买按钮(money-gated)。 */
function PlanCard({
  plan,
  busy,
  anyBusy,
  onBuy,
}: {
  plan: PlanView
  busy: boolean
  anyBusy: boolean
  onBuy: () => void
}) {
  const caps = formatCaps(plan)
  return (
    <div
      className="hk-card"
      style={{
        display: 'flex',
        flexDirection: 'column',
        gap: 'var(--hk-space-3)',
        padding: 'var(--hk-space-5)',
      }}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)' }}>
        <span style={{ fontSize: 16, fontWeight: 700, color: 'var(--hk-ink-900)' }}>{plan.name}</span>
        {plan.description && (
          <span style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>{plan.description}</span>
        )}
      </div>

      <div style={{ display: 'flex', alignItems: 'baseline', gap: 'var(--hk-space-2)' }}>
        <span style={{ fontSize: 24, fontWeight: 700, color: 'var(--hk-primary-700)' }}>
          {formatPrice(plan.price_cents, plan.currency_code)}
        </span>
        <span style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>/ {formatValidity(plan.validity_days)}</span>
      </div>

      <dl style={{ margin: 0, display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)', fontSize: 13 }}>
        <CapRow label="每日上限" value={caps.daily} />
        <CapRow label="每周上限" value={caps.weekly} />
        <CapRow label="每月上限" value={caps.monthly} />
        {plan.granted_group && <CapRow label="权益组" value={plan.granted_group} />}
      </dl>

      <button
        type="button"
        disabled={busy || anyBusy}
        onClick={onBuy}
        className={busy || anyBusy ? 'hk-btn' : 'hk-btn hk-btn--green'}
        style={{ marginTop: 'auto', justifyContent: 'center' }}
      >
        {busy ? '下单中…' : '购买'}
      </button>
    </div>
  )
}

function CapRow({ label, value }: { label: string; value: string }) {
  return (
    <div style={{ display: 'flex', justifyContent: 'space-between' }}>
      <dt style={{ color: 'var(--hk-ink-500)' }}>{label}</dt>
      <dd style={{ margin: 0, color: 'var(--hk-ink-700)', fontWeight: 600 }}>{value}</dd>
    </div>
  )
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      <span style={{ fontSize: 12, color: 'var(--hk-ink-500)' }}>{label}</span>
      <span style={{ fontSize: 14, color: 'var(--hk-ink-900)' }}>{children}</span>
    </div>
  )
}

function Banner({ tone, children }: { tone: 'ok' | 'danger'; children: React.ReactNode }) {
  const ok = tone === 'ok'
  return (
    <div
      style={{
        padding: 'var(--hk-space-3)',
        borderRadius: 'var(--hk-radius-md)',
        fontSize: 13,
        color: ok ? 'var(--hk-primary-600)' : 'var(--hk-danger)',
        background: ok ? 'var(--hk-primary-50)' : 'var(--hk-danger-soft)',
        border: ok ? '1px solid var(--hk-primary-100)' : '1px solid var(--hk-danger-soft)',
      }}
    >
      {children}
    </div>
  )
}

// 把纯逻辑的 SubTone 适配到 StatusBadge 的 BadgeTone(两者命名一致,显式映射避免耦合)。
function toBadgeTone(t: SubTone): BadgeTone {
  return t
}

/** 购买路径后端错误码 → 友好中文(对照 backend/internal/subscriptionhttp/purchase.go writePaymentOrderError)。 */
function friendlyPurchaseError(code: string, fallback?: string): string {
  switch (code) {
    case 'plan_not_for_sale':
      return '该套餐当前不可购买'
    case 'invalid_plan':
    case 'invalid_purchase_request':
      return '套餐参数有误,请刷新后重试'
    case 'unsupported_currency':
      return '该套餐币种暂不支持下单'
    case 'out_trade_no_conflict':
      return '订单号冲突,请稍后重试'
    case 'subscription_order_requires_pg':
    case 'gateway_not_configured':
    case 'payment_backend_error':
      return '订阅下单服务暂时不可用,请稍后再试'
    case 'session_token_required':
      return '登录状态已失效,请重新登录后再购买'
    default:
      return fallback || '下单失败,请稍后再试'
  }
}

const sectionTitle: CSSProperties = { fontSize: 15, margin: 0, color: 'var(--hk-ink-700)' }

const selectActionStyle: CSSProperties = {
  height: 32,
  padding: '0 var(--hk-space-2)',
  border: '1px solid var(--hk-line)',
  borderRadius: 'var(--hk-radius-md)',
  background: 'var(--hk-surface)',
  color: 'var(--hk-ink-700)',
  fontSize: 13,
}
