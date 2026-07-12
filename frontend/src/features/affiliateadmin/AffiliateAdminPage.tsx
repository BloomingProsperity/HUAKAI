import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../../lib/api'
import { StatusBadge } from '../../ui/StatusBadge'
import {
  getAdminReferralOverview,
  listAdminReferralRewards,
  listAdminReferrals,
} from './api'
import { formatUsd, statusCount, statusLabel, statusTone } from './affiliateadmin'
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
  const [draft, setDraft] = useState<AffiliateFilters>(EMPTY_AFFILIATE_FILTERS)
  const [filters, setFilters] = useState<AffiliateFilters>(EMPTY_AFFILIATE_FILTERS)
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

  // 概览随筛选(主要是租户号)变化重载;与列表解耦,独立错误位。
  useEffect(() => {
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
    setDraft(EMPTY_AFFILIATE_FILTERS)
    setReferrerDraft('')
    setReferrer('')
    setRecordsOffset(0)
    setRewardsOffset(0)
    setFilters(EMPTY_AFFILIATE_FILTERS)
  }
  const setD = <K extends keyof AffiliateFilters>(k: K, v: AffiliateFilters[K]) =>
    setDraft((f) => ({ ...f, [k]: v }))

  const total = tab === 'records' ? recordsTotal : rewardsTotal
  const offset = tab === 'records' ? recordsOffset : rewardsOffset
  const setOffset = tab === 'records' ? setRecordsOffset : setRewardsOffset
  const pageStart = total === 0 ? 0 : offset + 1
  const pageEnd = Math.min(offset + PAGE_SIZE, total)

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
        <StatCard label="累计返利(USD)" value={overview ? formatUsd(overview.total_reward_usd) : '—'} accent />
        <StatCard label="已发返利笔数" value={overview ? String(overview.reward_count) : '—'} />
        <StatCard label="待定 / 已达标" value={overview ? `${statusCount(overview.counts_by_status, 'pending')} / ${statusCount(overview.counts_by_status, 'qualified')}` : '—'} />
        <StatCard label="已返利 / 已驳回" value={overview ? `${statusCount(overview.counts_by_status, 'rewarded')} / ${statusCount(overview.counts_by_status, 'rejected')}` : '—'} />
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
          <Empty>加载中…</Empty>
        ) : tab === 'records' ? (
          records.length === 0 ? (
            <Empty>没有匹配的分销记录。</Empty>
          ) : (
            <RecordsTable rows={records} />
          )
        ) : rewards.length === 0 ? (
          <Empty>没有匹配的返利记录。</Empty>
        ) : (
          <RewardsTable rows={rewards} />
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

function RecordsTable({ rows }: { rows: AdminReferralItem[] }) {
  return (
    <div className="hk-tablewrap">
      <table className="hk-table">
        <thead>
          <tr>
            {['记录 ID', '邀请人', '被邀请人', '状态', '创建时间'].map((h) => (
              <th key={h}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.id}>
              <td className="hk-mono">#{r.id}</td>
              <td className="hk-mono">#{r.referrer_user_id}</td>
              <td className="hk-mono">#{r.referee_user_id}</td>
              <td>
                <StatusBadge tone={statusTone(r.status)}>{statusLabel(r.status)}</StatusBadge>
              </td>
              <td className="hk-mono">{fmt(r.created_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function RewardsTable({ rows }: { rows: AdminReferralRewardItem[] }) {
  return (
    <div className="hk-tablewrap">
      <table className="hk-table">
        <thead>
          <tr>
            {['流水 ID', '分销记录', '邀请人', '类型', '金额(USD)', '发放时间'].map((h) => (
              <th key={h}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.id}>
              <td className="hk-mono">#{r.id}</td>
              <td className="hk-mono">#{r.referral_id}</td>
              <td className="hk-mono">#{r.referrer_user_id}</td>
              <td>{r.reward_type || '—'}</td>
              <td className="hk-mono" style={{ fontWeight: 600, color: 'var(--hk-ink-900)' }}>{formatUsd(r.amount_usd)}</td>
              <td className="hk-mono">{fmt(r.issued_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function StatCard({ label, value, accent }: { label: string; value: string; accent?: boolean }) {
  return (
    <div className="hk-metric">
      <span className="hk-metric__label">{label}</span>
      <div className="hk-metric__v hk-mono" style={{ color: accent ? 'var(--hk-primary-700)' : 'var(--hk-ink-900)' }}>{value}</div>
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
function Empty({ children }: { children: React.ReactNode }) {
  return <div className="hk-empty">{children}</div>
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
function fmt(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN', { hour12: false })
}

const inp: React.CSSProperties = { height: 32, padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-sm)', fontSize: 13, background: 'var(--hk-surface)', color: 'var(--hk-ink-900)', width: '100%' }
