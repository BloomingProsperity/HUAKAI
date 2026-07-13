import { useEffect, useState, type CSSProperties, type ReactNode } from 'react'
import { Link } from 'react-router-dom'
import { useMe } from '../../auth/me'
import { DataListTable, type DataListColumn } from '../../ui/DataListTable'
import { Donut } from '../../ui/Donut'
import { EmptyState } from '../../ui/EmptyState'
import { ResourceCard } from '../../ui/ResourceCard'
import { Skeleton } from '../../ui/Skeleton'
import { Sparkline } from '../../ui/Sparkline'
import { StatCard } from '../../ui/StatCard'
import type { AccountHealthSummary } from '../accounts/types'
import type { AlertEvent } from '../alerting/types'
import type { AuditEvent } from '../audit/types'
import type { ChannelHealthSummary } from '../channelhealth/types'
import type { LeaderboardResponse, OverviewResponse } from '../ops/types'
import type { QuotaPolicy } from '../quotapolicies/types'
import {
  fetchAccountSummary,
  fetchAllFiringAlerts,
  fetchAllQuotaPolicies,
  fetchModelLeaderboard,
  fetchPoolInventoryCount,
  fetchPoolSummary,
  fetchPricingModelCount,
  fetchRecentAuditEvents,
  fetchUsageOverview,
} from './api'
import {
  accountResource,
  accountStat,
  auditRows,
  firingAlertCount,
  firingAlertStat,
  gatewayAvailabilityStat,
  modelDistribution,
  modelResource,
  modelStat,
  pendingItems,
  poolResource,
  quickLinks,
  quotaResource,
  requestTrend,
  requestVolumeStat,
  type AuditRow,
  type OverviewStat,
  type PendingItem,
  type ResourceItem,
} from './overview'

type LoadState<T> = { status: 'loading' } | { status: 'ok'; value: T } | { status: 'unavailable' }

/** 运营首页各源独立加载；一个端点失败只影响对应格子，不把错误伪装成零。 */
export function OperatorOverview() {
  const me = useMe()
  const tenantId = me.tenantId
  const [window, setWindow] = useState('24h')
  const [refreshSeq, setRefreshSeq] = useState(0)
  const [usage, setUsage] = useState<LoadState<OverviewResponse>>({ status: 'loading' })
  const [leaderboard, setLeaderboard] = useState<LoadState<LeaderboardResponse>>({ status: 'loading' })
  const [accounts, setAccounts] = useState<LoadState<AccountHealthSummary>>({ status: 'loading' })
  const [poolInventory, setPoolInventory] = useState<LoadState<number>>({ status: 'loading' })
  const [pools, setPools] = useState<LoadState<ChannelHealthSummary>>({ status: 'loading' })
  const [policies, setPolicies] = useState<LoadState<QuotaPolicy[]>>({ status: 'loading' })
  const [alerts, setAlerts] = useState<LoadState<AlertEvent[]>>({ status: 'loading' })
  const [audits, setAudits] = useState<LoadState<AuditEvent[]>>({ status: 'loading' })
  const [models, setModels] = useState<LoadState<number>>({ status: 'loading' })

  useEffect(() => {
    const ctrl = new AbortController()
    load(fetchUsageOverview(window, ctrl.signal), setUsage, ctrl.signal)
    load(fetchModelLeaderboard(window, ctrl.signal), setLeaderboard, ctrl.signal)
    return () => ctrl.abort()
  }, [window, refreshSeq])

  useEffect(() => {
    if (!tenantId) return
    const ctrl = new AbortController()
    load(fetchAccountSummary(tenantId, ctrl.signal), setAccounts, ctrl.signal)
    load(fetchPoolInventoryCount(tenantId, ctrl.signal), setPoolInventory, ctrl.signal)
    load(fetchPoolSummary(tenantId, ctrl.signal), setPools, ctrl.signal)
    load(fetchAllQuotaPolicies(tenantId, ctrl.signal), setPolicies, ctrl.signal)
    load(fetchAllFiringAlerts(tenantId, ctrl.signal), setAlerts, ctrl.signal)
    load(fetchRecentAuditEvents(tenantId, ctrl.signal).then((result) => result.items), setAudits, ctrl.signal)
    load(fetchPricingModelCount(ctrl.signal), setModels, ctrl.signal)
    return () => ctrl.abort()
  }, [tenantId])

  const refreshWindow = () => {
    setRefreshSeq((value) => value + 1)
  }

  const firing = alerts.status === 'ok' ? firingAlertCount(alerts.value) : 0
  const pending = alerts.status === 'ok' && accounts.status === 'ok' && pools.status === 'ok'
    ? pendingItems(alerts.value, accounts.value, pools.value)
    : null

  return (
    <section className="hk-operator-overview" aria-label="运营总览">
      <div className="hk-overview-toolbar">
        <div><h1>控制台总览</h1><p>网关资源、请求态势与待处理事项</p></div>
        <label className="hk-field" style={{ width: 132 }}>统计窗口
          <select className="hk-input" value={window} onChange={(event) => setWindow(event.target.value)} aria-label="总览时间窗口">
            <option value="24h">近 24 小时</option><option value="7d">近 7 天</option>
          </select>
        </label>
      </div>

      <div className="hk-overview-stats">
        <StatSlot state={usage} map={gatewayAvailabilityStat} />
        <StatSlot state={usage} map={requestVolumeStat} />
        <StatSlot state={accounts} map={accountStat} />
        <StatSlot state={models} map={modelStat} label="在线模型" />
        <StatSlot state={alerts} map={firingAlertStat} label="异常告警" />
      </div>

      <div className="hk-overview-grid">
        <Panel title="网关资源概览" className="hk-overview-span-8">
          <div className="hk-resource-grid">
            <ResourceSlot state={models} map={modelResource} title="在线模型数" action={{ label: '管理模型服务', to: '/models' }} />
            <ResourceSlot state={accounts} map={accountResource} title="上游账号" action={{ label: '管理上游账号', to: '/accounts' }} />
            <PoolResourceSlot inventory={poolInventory} health={pools} />
            <ResourceSlot state={policies} map={quotaResource} title="流量控制" action={{ label: '配置限流策略', to: '/routing' }} />
          </div>
        </Panel>
        <Panel title="今日待处理事项" className="hk-overview-span-4" aside={<span className="hk-panel-meta">{pending?.length ?? '—'} 项</span>}>
          {pending === null ? <Unavailable title="待办数据暂不可用" hint="三路数据完整后才生成待办，避免遗漏风险。" /> : pending.length === 0 ? <EmptyState icon="✓" title="今日无待处理事项" tone="positive" /> : <PendingTable rows={pending} />}
          {pending && pending.length > 0 && <div className="hk-panel-foot">查看全部任务（{pending.length}）</div>}
        </Panel>

        <Panel title="请求趋势" className="hk-overview-span-5" aside={<button type="button" className="hk-act" onClick={refreshWindow}>刷新</button>}>
          {usage.status === 'loading' ? <Skeleton height={156} /> : usage.status === 'unavailable' ? <Unavailable title="请求趋势暂不可用" /> : <TrendPanel overview={usage.value} />}
        </Panel>
        <Panel title="模型调用分布" className="hk-overview-span-4">
          {leaderboard.status === 'loading' ? <Skeleton height={180} /> : leaderboard.status === 'unavailable' ? <Unavailable title="模型分布暂不可用" /> : <DistributionPanel response={leaderboard.value} />}
        </Panel>
        <Panel title="快捷入口" className="hk-overview-span-3">
          <div className="hk-quick-grid">{quickLinks(firing).map((item) => <Link key={item.label} to={item.to} className="hk-quick-link"><span aria-hidden="true" className="hk-quick-icon">{item.icon}</span><span>{item.label}</span>{item.badge != null && <b>{item.badge}</b>}</Link>)}</div>
        </Panel>

        <Panel title="告警" className="hk-overview-span-5" aside={<Link className="hk-act" to="/admin/alerting">查看全部告警</Link>}>
          {alerts.status === 'loading' ? <Skeleton height={92} /> : alerts.status === 'unavailable' ? <Unavailable title="告警数据暂不可用" /> : firing === 0 ? <EmptyState icon="✓" title="当前无告警" tone="positive" /> : <div className="hk-alert-summary"><span className="hk-alert-icon">!</span><div><strong>{firing}</strong><span>条告警正在触发</span></div><Link to="/admin/alerting">立即处理</Link></div>}
        </Panel>
        <Panel title="最近变更与审计事件" className="hk-overview-span-7" aside={<Link className="hk-act" to="/activity">查看全部审计事件</Link>}>
          {audits.status === 'loading' ? <Skeleton height={168} /> : audits.status === 'unavailable' ? <Unavailable title="审计事件暂不可用" /> : audits.value.length === 0 ? <EmptyState title="暂无审计事件" hint="发生受审计的运营操作后会显示在这里。" /> : <AuditTable rows={auditRows(audits.value, 8)} />}
        </Panel>
      </div>
    </section>
  )
}

function load<T>(promise: Promise<T>, setState: (state: LoadState<T>) => void, signal: AbortSignal) {
  setState({ status: 'loading' })
  promise.then((value) => { if (!signal.aborted) setState({ status: 'ok', value }) }).catch(() => { if (!signal.aborted) setState({ status: 'unavailable' }) })
}

function StatSlot<T>({ state, map, label = '指标' }: { state: LoadState<T>; map: (value: T) => OverviewStat; label?: string }) {
  if (state.status === 'loading') return <div className="hk-overview-stat-skeleton"><Skeleton height={106} /></div>
  if (state.status === 'unavailable') return <StatCard label={label} value="—" hint="数据暂不可用" />
  const item = map(state.value)
  return <StatCard label={item.label} value={item.value} hint={item.hint} tone={item.tone} to={item.to} icon={item.icon} sparkline={item.sparkline && item.sparkline.length > 0 ? <Sparkline values={item.sparkline} label={`${item.label}趋势`} /> : undefined} />
}

function ResourceSlot<T>({ state, map, title, action }: { state: LoadState<T>; map: (value: T) => ResourceItem; title: string; action: { label: string; to: string } }) {
  if (state.status === 'loading') return <Skeleton height={176} />
  if (state.status === 'unavailable') return <ResourceCard title={title} value="—" action={action} />
  const item = map(state.value)
  return <ResourceCard title={item.title} value={item.value} icon={item.icon} badges={item.badges} action={item.action} />
}

function PoolResourceSlot({ inventory, health }: { inventory: LoadState<number>; health: LoadState<ChannelHealthSummary> }) {
  const action = { label: '管理账号池', to: '/accounts?tab=pool' }
  if (inventory.status === 'loading') return <Skeleton height={176} />
  if (inventory.status === 'unavailable') return <ResourceCard title="账号池" value="—" action={action} />
  const item = poolResource(inventory.value, health.status === 'ok' ? health.value : undefined)
  return <ResourceCard title={item.title} value={item.value} icon={item.icon} badges={item.badges} action={item.action} />
}

function Panel({ title, aside, className, children }: { title: string; aside?: ReactNode; className: string; children: ReactNode }) {
  return <section className={`hk-card hk-overview-panel ${className}`}><header className="hk-card__head"><h3>{title}</h3>{aside}</header><div className="hk-card__body">{children}</div></section>
}

function Unavailable({ title, hint }: { title: string; hint?: string }) {
  return <EmptyState icon="!" title={title} hint={hint ?? '请稍后重试或前往对应管理页检查。'} tone="unavailable" />
}

function TrendPanel({ overview }: { overview: OverviewResponse }) {
  const points = requestTrend(overview.trend)
  if (points.length === 0) return <EmptyState title="窗口内暂无调用" hint="产生真实请求后会显示按日请求量。" />
  return <div className="hk-trend"><Sparkline values={points.map((point) => point.value)} label="按日请求量趋势" height={140} /><div><span>{points[0].label}</span><strong>{overview.totals.requests.toLocaleString('zh-CN')} 次</strong><span>{points[points.length - 1].label}</span></div><small>按日请求量 · 单序列</small></div>
}

function DistributionPanel({ response }: { response: LeaderboardResponse }) {
  const distribution = modelDistribution(response.entries)
  if (distribution.total === 0) return <EmptyState title="窗口内暂无调用" hint="有模型调用后会显示真实占比。" />
  return <Donut segments={distribution.segments} total={distribution.total} label="模型调用分布" />
}

function PendingTable({ rows }: { rows: PendingItem[] }) {
  const columns: DataListColumn<PendingItem>[] = [
    { key: 'priority', label: '优先级', badge: true, render: (row) => <span className={`hk-pill ${row.priority === '高' ? 'hk-pill--crit' : 'hk-pill--warn'}`}>{row.priority}</span> },
    { key: 'title', label: '事项', render: (row) => row.title },
    { key: 'detail', label: '详情', render: (row) => row.detail },
  ]
  return <DataListTable label="今日待处理事项" rows={rows} rowKey={(row) => row.key} columns={columns} action={{ label: (row) => row.actionLabel, to: (row) => row.to }} />
}

function AuditTable({ rows }: { rows: AuditRow[] }) {
  const columns: DataListColumn<AuditRow>[] = [
    { key: 'time', label: '时间', width: 92, render: (row) => <span className="hk-mono">{row.time}</span> },
    { key: 'type', label: '类型', badge: true, render: (row) => <span className={`hk-pill ${toneClass(row.tone)}`}>{row.type}</span> },
    { key: 'object', label: '对象', render: (row) => row.object },
    { key: 'actor', label: '操作人', render: (row) => row.actor },
    { key: 'detail', label: '详情', render: (row) => <span title={row.detail} style={ellipsisStyle}>{row.detail}</span> },
  ]
  return <DataListTable label="最近变更与审计事件" rows={rows} rowKey={(row) => row.id} columns={columns} />
}

function toneClass(tone: AuditRow['tone']): string {
  if (tone === 'danger') return 'hk-pill--crit'
  if (tone === 'warn') return 'hk-pill--warn'
  if (tone === 'ok') return 'hk-pill--ok'
  return 'hk-pill--info'
}

const ellipsisStyle: CSSProperties = { display: 'block', maxWidth: 220, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }
