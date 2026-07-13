import { useCallback, useEffect, useState } from 'react'
import { useMe } from '../../auth/me'
import { ApiError } from '../../lib/api'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { EmptyState } from '../../ui/EmptyState'
import { StatCard } from '../../ui/StatCard'
import { StatusBadge } from '../../ui/StatusBadge'
import {
  getAdminReferralOverview,
  listAdminReferralRewards,
  listAdminReferrals,
} from './api'
import {
  formatUsd,
  mapAffiliateStats,
  mapReferralTableRows,
  mapRewardTableRows,
  statusLabel,
  statusTone,
  withTenantContext,
  type ReferralTableRow,
  type RewardTableRow,
} from './affiliateadmin'
import {
  EMPTY_AFFILIATE_FILTERS,
  type AdminReferralItem,
  type AdminReferralOverview,
  type AdminReferralRewardItem,
  type AffiliateFilters,
  type ReferralStatus,
} from './types'

/*
 * 分销管理(运营台,admin 壳)。只读为主:返利链上游=邀请人→被邀请人→达标→返利。
 * 三块端点(均 admin token,routes.go:1054-1062):
 *   概览统计(各状态计数 + 累计返利 USD + 已发笔数)
 *   分销记录列表(分页 + 状态筛选)
 *   返利账本(分页,带累计 total_reward_usd,可按 referrer 过滤)
 *
 * 鉴权约定(referralhttp.resolveAdminTenant):platform_admin 必须显式填租户号,
 * 否则后端 400 invalid_request;tenant_operator 留空走自身 scope。UI 把租户号做成
 * 顶部统一筛选项,400 时给出补租户号提示。涉及钱(money-gated):纯展示,不写。
 */

const PAGE_SIZE = 20
const STATUS_OPTIONS: ReferralStatus[] = ['pending', 'qualified', 'rewarded', 'rejected']
type Tab = 'records' | 'rewards'

export function AffiliateAdminPage() {
  const contextTenantId = useMe().tenantId
  const [draft, setDraft] = useState<AffiliateFilters>(() => withTenantContext(EMPTY_AFFILIATE_FILTERS, contextTenantId))
  const [filters, setFilters] = useState<AffiliateFilters>(() => withTenantContext(EMPTY_AFFILIATE_FILTERS, contextTenantId))
  const [referrerDraft, setReferrerDraft] = useState('')
  const [referrer, setReferrer] = useState('')
  const [tab, setTab] = useState<Tab>('records')

  const [overview, setOverview] = useState<AdminReferralOverview | null>(null)
  const [overviewErr, setOverviewErr] = useState<string | null>(null)

  const [records, setRecords] = useState<AdminReferralItem[]>([])
  const [recordsTotal, setRecordsTotal] = useState(0)
  const [recordsOffset, setRecordsOffset] = useState(0)

  const [rewards, setRewards] = useState<AdminReferralRewardItem[]>([])
  const [rewardsTotal, setRewardsTotal] = useState(0)
  const [rewardsSum, setRewardsSum] = useState('0')
  const [rewardsOffset, setRewardsOffset] = useState(0)

  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (contextTenantId == null) return
    setDraft((current) => withTenantContext(current, contextTenantId))
    setFilters((current) => withTenantContext(current, contextTenantId))
  }, [contextTenantId])

  // 概览随筛选(主要是租户号)变化重载;与列表解耦,独立错误位。
  useEffect(() => {
    if (!filters.tenantId.trim()) return
    const ctrl = new AbortController()
    setOverviewErr(null)
    getAdminReferralOverview(filters, ctrl.signal)
      .then(setOverview)
      .catch((e: unknown) => {
        if (ctrl.signal.aborted) return
        setOverview(null)
        setOverviewErr(errMsg(e, '加载概览失败'))
      })
    return () => ctrl.abort()
  }, [filters])

  // 列表加载(分页/筛选/页签切换均触发)。
  const loadList = useCallback(
    (signal: AbortSignal) => {
      if (!filters.tenantId.trim()) return
      setLoading(true)
      setError(null)
      if (tab === 'records') {
        listAdminReferrals(filters, PAGE_SIZE, recordsOffset, signal)
          .then((resp) => {
            setRecords(resp.items)
            setRecordsTotal(resp.total)
          })
          .catch((e: unknown) => {
            if (signal.aborted) return
            setError(errMsg(e, '加载分销记录失败'))
          })
          .finally(() => {
            if (!signal.aborted) setLoading(false)
          })
      } else {
        listAdminReferralRewards(filters, PAGE_SIZE, rewardsOffset, referrer, signal)
          .then((resp) => {
            setRewards(resp.items)
            setRewardsTotal(resp.total)
            setRewardsSum(resp.total_reward_usd)
          })
          .catch((e: unknown) => {
            if (signal.aborted) return
            setError(errMsg(e, '加载返利账本失败'))
          })
          .finally(() => {
            if (!signal.aborted) setLoading(false)
          })
      }
    },
    [tab, filters, recordsOffset, rewardsOffset, referrer],
  )

  useEffect(() => {
    const ctrl = new AbortController()
    loadList(ctrl.signal)
    return () => ctrl.abort()
  }, [loadList])

  // 应用筛选:重置分页与 referrer。
  const applyFilters = () => {
    setRecordsOffset(0)
    setRewardsOffset(0)
    setReferrer(referrerDraft.trim())
    setFilters(draft)
  }
  const resetFilters = () => {
    const reset = withTenantContext(EMPTY_AFFILIATE_FILTERS, contextTenantId)
    setDraft(reset)
    setReferrerDraft('')
    setReferrer('')
    setRecordsOffset(0)
    setRewardsOffset(0)
    setFilters(reset)
  }
  const setD = <K extends keyof AffiliateFilters>(k: K, v: AffiliateFilters[K]) =>
    setDraft((f) => ({ ...f, [k]: v }))

  const total = tab === 'records' ? recordsTotal : rewardsTotal
  const offset = tab === 'records' ? recordsOffset : rewardsOffset
  const setOffset = tab === 'records' ? setRecordsOffset : setRewardsOffset
  const pageStart = total === 0 ? 0 : offset + 1
  const pageEnd = Math.min(offset + PAGE_SIZE, total)
  const overviewStats = mapAffiliateStats(overview)
  const recordRows = mapReferralTableRows(records)
  const rewardRows = mapRewardTableRows(rewards)

  return (
    <div className="hk-page">
      <header className="hk-pagehead">
        <div>
          <h1>分销管理</h1>
          <p className="hk-sub">
            邀请返利链(只读)。平台管理员请先填租户号;租户操作员留空走自身范围。
          </p>
        </div>
      </header>

      {/* 概览统计卡 */}
      <section style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: 'var(--hk-space-3)' }}>
        {overviewStats.map((stat) => <StatCard key={stat.label} label={stat.label} value={stat.value} tone={stat.tone} />)}
      </section>
      {overviewErr && <Banner>{overviewErr}</Banner>}

      {/* 筛选条 */}
      <form
        onSubmit={(e) => {
          e.preventDefault()
          applyFilters()
        }}
        style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(150px, 1fr))', gap: 'var(--hk-space-3)', alignItems: 'flex-end', background: 'var(--hk-surface)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', padding: 'var(--hk-space-4)' }}
      >
        <Field label="租户号(tenant_id)">
          <input value={draft.tenantId} onChange={(e) => setD('tenantId', e.target.value)} inputMode="numeric" placeholder="平台管理员必填" style={inp} />
        </Field>
        <Field label="分销状态">
          <select value={draft.status} onChange={(e) => setD('status', e.target.value)} style={inp}>
            <option value="">全部</option>
            {STATUS_OPTIONS.map((s) => (
              <option key={s} value={s}>
                {statusLabel(s)}
              </option>
            ))}
          </select>
        </Field>
        <Field label="邀请人 ID(仅账本)">
          <input value={referrerDraft} onChange={(e) => setReferrerDraft(e.target.value)} inputMode="numeric" placeholder="留空=全部" style={inp} />
        </Field>
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
          <button type="submit" className="hk-btn hk-btn--green">
            查询
          </button>
          <button type="button" onClick={resetFilters} className="hk-btn">
            重置
          </button>
        </div>
      </form>

      {/* 页签 */}
      <div style={{ display: 'flex', gap: 'var(--hk-space-2)', borderBottom: '1px solid var(--hk-line)' }}>
        <TabBtn active={tab === 'records'} onClick={() => setTab('records')}>
          分销记录
        </TabBtn>
        <TabBtn active={tab === 'rewards'} onClick={() => setTab('rewards')}>
          返利账本
        </TabBtn>
      </div>

      {error && <Banner>{error}</Banner>}

      {tab === 'rewards' && (
        <div style={{ fontSize: 13, color: 'var(--hk-ink-500)' }}>
          当前范围累计返利:<span style={{ fontFamily: 'var(--hk-font-mono)', color: 'var(--hk-ink-900)', fontWeight: 600 }}>{formatUsd(rewardsSum)} USD</span>
        </div>
      )}

      <div className="hk-card">
        {loading && (tab === 'records' ? records.length === 0 : rewards.length === 0) ? (
          <EmptyState title="正在加载分销数据" hint="请稍候。" />
        ) : tab === 'records' ? (
          records.length === 0 ? (
            <EmptyState title="没有匹配的分销记录" hint="可调整租户或状态筛选后重新查询。" />
          ) : (
            <DataListTable label="分销记录列表" rows={recordRows} rowKey={(row) => row.id} columns={referralColumns} />
          )
        ) : rewards.length === 0 ? (
          <EmptyState title="没有匹配的返利记录" hint="可调整租户或邀请人筛选后重新查询。" />
        ) : (
          <DataListTable label="返利账本列表" rows={rewardRows} rowKey={(row) => row.id} columns={rewardColumns} />
        )}
      </div>

      {/* 分页 */}
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', fontSize: 13, color: 'var(--hk-ink-500)' }}>
        <span>
          第 {pageStart}–{pageEnd} 条 / 共 {total} 条
        </span>
        <div style={{ display: 'flex', gap: 'var(--hk-space-2)' }}>
          <button type="button" disabled={loading || offset <= 0} onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))} className="hk-btn hk-btn--sm">
            上一页
          </button>
          <button type="button" disabled={loading || offset + PAGE_SIZE >= total} onClick={() => setOffset(offset + PAGE_SIZE)} className="hk-btn hk-btn--sm">
            下一页
          </button>
        </div>
      </div>
    </div>
  )
}

function TabBtn({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        border: 'none',
        background: 'transparent',
        padding: 'var(--hk-space-2) var(--hk-space-3)',
        fontSize: 14,
        fontWeight: active ? 600 : 400,
        color: active ? 'var(--hk-primary-700)' : 'var(--hk-ink-500)',
        borderBottom: active ? '2px solid var(--hk-primary-500)' : '2px solid transparent',
        cursor: 'pointer',
        marginBottom: -1,
      }}
    >
      {children}
    </button>
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
function Banner({ children }: { children: React.ReactNode }) {
  return <div style={{ padding: 'var(--hk-space-3)', borderRadius: 'var(--hk-radius-md)', fontSize: 13, color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', border: '1px solid var(--hk-danger-soft)' }}>{children}</div>
}

function errMsg(e: unknown, fallback: string): string {
  if (e instanceof ApiError) {
    // platform_admin 漏填租户号时后端 400 invalid_request:给出可操作提示。
    if (e.status === 400 && e.code === 'invalid_request') {
      return '请填写租户号(平台管理员必填)。'
    }
    return `${e.message}(${e.code})`
  }
  return fallback
}
const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }

const referralColumns: DataListColumn<ReferralTableRow>[] = [
  { key: 'id', label: '记录 ID', render: (row) => <span className="hk-mono">#{row.id}</span> },
  { key: 'referrer', label: '邀请人', render: (row) => <span className="hk-mono">{row.referrerUserId}</span> },
  { key: 'referee', label: '被邀请人', render: (row) => <span className="hk-mono">{row.refereeUserId}</span> },
  { key: 'status', label: '状态', render: (row) => <StatusBadge tone={statusTone(row.status)}>{statusLabel(row.status)}</StatusBadge> },
  { key: 'createdAt', label: '创建时间', render: (row) => <span className="hk-mono">{row.createdAt}</span> },
]

const rewardColumns: DataListColumn<RewardTableRow>[] = [
  { key: 'id', label: '流水 ID', render: (row) => <span className="hk-mono">#{row.id}</span> },
  { key: 'referral', label: '分销记录', render: (row) => <span className="hk-mono">{row.referralId}</span> },
  { key: 'referrer', label: '邀请人', render: (row) => <span className="hk-mono">{row.referrerUserId}</span> },
  { key: 'type', label: '类型', render: (row) => row.rewardType },
  { key: 'amount', label: '金额(USD)', render: (row) => <strong className="hk-mono">{row.amountUsd}</strong> },
  { key: 'issuedAt', label: '发放时间', render: (row) => <span className="hk-mono">{row.issuedAt}</span> },
]
