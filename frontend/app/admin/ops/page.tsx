'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Activity,
  AlertTriangle,
  BellRing,
  DollarSign,
  Gauge,
  HeartPulse,
  RefreshCw,
  Timer,
  TrendingUp,
  Trophy,
  Users,
  Zap,
} from 'lucide-react';
import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { StatCard } from '@/components/dashboard/StatCard';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import {
  USAGE_WINDOWS,
  getHealthScore,
  getPerfMetricsSummary,
  getUsageLeaderboard,
  getUsageOverview,
  getUsagePerformance,
  listAlertEvents,
  manualResolveAlertEvent,
  type AlertEvent,
  type HealthScoreResponse,
  type LeaderboardBy,
  type LeaderboardResponse,
  type OverviewResponse,
  type PerfMetricsSummaryResponse,
  type PerformanceBy,
  type PerformanceResponse,
  type UsageWindow,
} from '@/lib/api/adminOps';
import { friendlyMessage } from '@/lib/api/errors';
import { cn } from '@/lib/utils';

const LEADERBOARD_OPTIONS: { value: LeaderboardBy; label: string }[] = [
  { value: 'user', label: '按用户' },
  { value: 'model', label: '按模型' },
  { value: 'provider_account', label: '按供应商账号' },
];

const PERFORMANCE_OPTIONS: { value: PerformanceBy; label: string }[] = [
  { value: 'model', label: '按模型' },
  { value: 'provider_account', label: '按供应商账号' },
];

interface OpsSnapshot {
  overview: OverviewResponse;
  leaderboard: LeaderboardResponse;
  performance: PerformanceResponse;
  perfMetrics: PerfMetricsSummaryResponse;
  healthScore: HealthScoreResponse;
  alertEvents: AlertEvent[];
  alertEventsError: string | null;
}

function formatInt(value: number): string {
  return value.toLocaleString('zh-CN');
}

// 后端金额是 8 位定点字符串；本地只做展示截断，不做币种换算。
function formatMoney(raw: string): string {
  const parsed = Number(raw);
  if (!Number.isFinite(parsed)) return raw;
  return parsed.toLocaleString('zh-CN', { minimumFractionDigits: 4, maximumFractionDigits: 4 });
}

// success_rate / error_rate 是 0~1 的 4 位定点字符串。
function formatRate(raw: string): string {
  const parsed = Number(raw);
  if (!Number.isFinite(parsed)) return raw;
  return `${(parsed * 100).toFixed(2)}%`;
}

function formatMs(value: number): string {
  if (!Number.isFinite(value)) return '—';
  if (value >= 1000) return `${(value / 1000).toFixed(2)}s`;
  return `${value.toFixed(0)}ms`;
}

function formatDecimal(raw: string): string {
  const parsed = Number(raw);
  if (!Number.isFinite(parsed)) return raw;
  return parsed.toLocaleString('zh-CN', { maximumFractionDigits: 2 });
}

function formatDateTime(value: string | undefined): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString('zh-CN', { hour12: false });
}

function healthTone(score: number): 'emerald' | 'amber' | 'red' {
  if (score >= 85) return 'emerald';
  if (score >= 60) return 'amber';
  return 'red';
}

function severityVariant(severity: string): 'default' | 'secondary' | 'destructive' | 'outline' {
  const normalized = severity.toLowerCase();
  if (normalized === 'critical' || normalized === 'page') return 'destructive';
  if (normalized === 'warning' || normalized === 'warn') return 'secondary';
  return 'outline';
}

function eventStateVariant(state: string): 'default' | 'secondary' | 'destructive' | 'outline' {
  const normalized = state.toLowerCase();
  if (normalized === 'firing' || normalized === 'active') return 'destructive';
  if (normalized === 'resolved') return 'outline';
  return 'secondary';
}

async function loadOpsSnapshot(window: UsageWindow, leaderboardBy: LeaderboardBy, performanceBy: PerformanceBy): Promise<OpsSnapshot> {
  const [overview, leaderboard, performance, perfMetrics, healthScore] = await Promise.all([
    getUsageOverview(window),
    getUsageLeaderboard(leaderboardBy, window, { limit: 10 }),
    getUsagePerformance(performanceBy, window, { limit: 10 }),
    getPerfMetricsSummary(window),
    getHealthScore(window),
  ]);

  // 告警事件对平台 admin 需要 tenant_id；缺省时后端返回 400，这里降级为可见提示而非整页失败。
  let alertEvents: AlertEvent[] = [];
  let alertEventsError: string | null = null;
  try {
    const events = await listAlertEvents({ limit: 20 });
    alertEvents = events.items;
  } catch (err) {
    alertEventsError = friendlyMessage(err);
  }

  return { overview, leaderboard, performance, perfMetrics, healthScore, alertEvents, alertEventsError };
}

export default function AdminOpsPage() {
  const [window, setWindow] = useState<UsageWindow>('24h');
  const [leaderboardBy, setLeaderboardBy] = useState<LeaderboardBy>('user');
  const [performanceBy, setPerformanceBy] = useState<PerformanceBy>('model');
  const [snapshot, setSnapshot] = useState<OpsSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [resolvingId, setResolvingId] = useState<number | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const next = await loadOpsSnapshot(window, leaderboardBy, performanceBy);
      setSnapshot(next);
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setLoading(false);
    }
  }, [window, leaderboardBy, performanceBy]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const handleResolve = useCallback(async (id: number) => {
    setResolvingId(id);
    try {
      await manualResolveAlertEvent(id);
      await refresh();
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setResolvingId(null);
    }
  }, [refresh]);

  const trendData = useMemo(() => {
    if (!snapshot) return [];
    return snapshot.overview.trend.map((point) => ({
      day: point.day,
      requests: point.requests,
      cost: Number(point.cost),
    }));
  }, [snapshot]);

  return (
    <div className="space-y-6">
      {/* 标题 + 过滤器 */}
      <section className="flex flex-col gap-4 rounded-lg border border-accent-200 bg-white px-5 py-4 shadow-card dark:border-accent-800 dark:bg-accent-900/70 lg:flex-row lg:items-center lg:justify-between">
        <div className="min-w-0">
          <p className="text-xs font-medium text-primary-700 dark:text-primary-300">运营总览</p>
          <h1 className="mt-1 text-2xl font-bold tracking-normal text-accent-950 dark:text-white">Ops 运营看板</h1>
          <p className="mt-2 text-sm text-accent-500 dark:text-accent-400">
            用量概览、Top 排行、延迟性能与告警事件集中视图，全部来自真实后端 admin 端点。
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <div className="flex overflow-hidden rounded-md border border-accent-200 dark:border-accent-700">
            {USAGE_WINDOWS.map((option) => (
              <button
                key={option.value}
                type="button"
                onClick={() => setWindow(option.value)}
                className={cn(
                  'px-3 py-1.5 text-xs font-medium transition-colors',
                  window === option.value
                    ? 'bg-primary-600 text-white'
                    : 'bg-white text-accent-600 hover:bg-accent-50 dark:bg-accent-900 dark:text-accent-300 dark:hover:bg-accent-800',
                )}
              >
                {option.label}
              </button>
            ))}
          </div>
          <Button disabled={loading} onClick={refresh} size="sm" variant="outline">
            <RefreshCw className={cn('size-4', loading && 'animate-spin')} />
            刷新
          </Button>
        </div>
      </section>

      {error && (
        <div role="alert" className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-200">
          {snapshot ? `刷新失败：${error}；下方仍为上次成功的后端响应。` : `加载失败：${error}`}
        </div>
      )}

      {!snapshot && loading && (
        <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
          <CardContent className="p-6 text-sm text-accent-500 dark:text-accent-400">
            正在读取运营总览、排行榜、性能指标与告警事件…
          </CardContent>
        </Card>
      )}

      {snapshot && (
        <>
          {/* 概览 KPI */}
          <section className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-6" aria-label="用量概览">
            <StatCard
              title="总请求数"
              value={formatInt(snapshot.overview.totals.requests)}
              icon={Zap}
              description={`窗口 ${snapshot.overview.window}`}
              detail={`成功率 ${formatRate(snapshot.overview.totals.success_rate)}`}
              tone="blue"
            />
            <StatCard
              title="总花费"
              value={formatMoney(snapshot.overview.totals.total_cost)}
              icon={DollarSign}
              description="actual_cost 汇总"
              detail="未做本地币种换算"
              tone="emerald"
            />
            <StatCard
              title="总 Token"
              value={formatInt(snapshot.overview.totals.total_tokens)}
              icon={TrendingUp}
              description="窗口内累计"
              detail="输入+输出+缓存"
              tone="primary"
            />
            <StatCard
              title="活跃用户"
              value={formatInt(snapshot.overview.totals.active_users)}
              icon={Users}
              description="有结算用量的用户"
              detail={`活跃 Key ${formatInt(snapshot.overview.totals.active_api_keys)}`}
              tone="slate"
            />
            <StatCard
              title="健康分"
              value={String(snapshot.healthScore.overall_score)}
              icon={HeartPulse}
              description={`业务 ${snapshot.healthScore.business_score} / 基础 ${snapshot.healthScore.infra_score}`}
              detail={`错误率 ${formatRate(snapshot.healthScore.signals.error_rate)}`}
              tone={healthTone(snapshot.healthScore.overall_score)}
            />
            <StatCard
              title="TTFT P99"
              value={formatMs(snapshot.perfMetrics.latency_percentiles_ms.p99)}
              icon={Timer}
              description="首字延迟 p99"
              detail={`p95 ${formatMs(snapshot.perfMetrics.latency_percentiles_ms.p95)} / p50 ${formatMs(snapshot.perfMetrics.latency_percentiles_ms.p50)}`}
              tone="amber"
            />
          </section>

          {/* 用量趋势 */}
          <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
            <CardHeader className="p-5 pb-3">
              <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
                <Activity className="size-4 text-primary-600 dark:text-primary-300" />
                逐日请求与花费趋势
              </CardTitle>
            </CardHeader>
            <CardContent className="h-[300px] min-h-[300px] p-5 pt-0">
              {trendData.length < 2 ? (
                <div className="flex h-full items-center justify-center rounded-lg border border-dashed border-accent-200 bg-accent-50 px-4 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
                  后端逐日聚合点不足，暂不绘制趋势；不会用本地假曲线补齐。
                </div>
              ) : (
                <ResponsiveContainer width="100%" height="100%" minWidth={320} minHeight={240}>
                  <AreaChart data={trendData}>
                    <defs>
                      <linearGradient id="opsRequests" x1="0" y1="0" x2="0" y2="1">
                        <stop offset="5%" stopColor="#14b8a6" stopOpacity={0.32} />
                        <stop offset="95%" stopColor="#14b8a6" stopOpacity={0} />
                      </linearGradient>
                    </defs>
                    <CartesianGrid strokeDasharray="3 3" stroke="rgba(148, 163, 184, 0.28)" vertical={false} />
                    <XAxis dataKey="day" tickLine={false} axisLine={false} />
                    <YAxis tickLine={false} axisLine={false} allowDecimals={false} />
                    <Tooltip
                      formatter={(value, name) => {
                        if (name === 'cost') return [formatMoney(String(value)), '花费'];
                        return [formatInt(Number(value)), '请求数'];
                      }}
                      labelFormatter={(label) => `日期 ${label}`}
                    />
                    <Area type="monotone" name="requests" dataKey="requests" stroke="#14b8a6" strokeWidth={2} fill="url(#opsRequests)" />
                  </AreaChart>
                </ResponsiveContainer>
              )}
            </CardContent>
          </Card>

          {/* 排行榜 + 性能 */}
          <section className="grid gap-6 xl:grid-cols-2">
            {/* 排行榜 */}
            <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
              <CardHeader className="flex flex-row items-center justify-between gap-3 p-5 pb-3">
                <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
                  <Trophy className="size-4 text-amber-500 dark:text-amber-300" />
                  花费排行榜 Top 10
                </CardTitle>
                <div className="flex overflow-hidden rounded-md border border-accent-200 dark:border-accent-700">
                  {LEADERBOARD_OPTIONS.map((option) => (
                    <button
                      key={option.value}
                      type="button"
                      onClick={() => setLeaderboardBy(option.value)}
                      className={cn(
                        'px-2.5 py-1 text-xs font-medium transition-colors',
                        leaderboardBy === option.value
                          ? 'bg-primary-600 text-white'
                          : 'bg-white text-accent-600 hover:bg-accent-50 dark:bg-accent-900 dark:text-accent-300 dark:hover:bg-accent-800',
                      )}
                    >
                      {option.label}
                    </button>
                  ))}
                </div>
              </CardHeader>
              <CardContent className="p-0">
                <Table>
                  <TableHeader>
                    <TableRow className="border-accent-200 dark:border-accent-800">
                      <TableHead className="w-12 text-accent-500 dark:text-accent-400">#</TableHead>
                      <TableHead className="text-accent-500 dark:text-accent-400">标识</TableHead>
                      <TableHead className="text-right text-accent-500 dark:text-accent-400">花费</TableHead>
                      <TableHead className="text-right text-accent-500 dark:text-accent-400">请求</TableHead>
                      <TableHead className="text-right text-accent-500 dark:text-accent-400">Token</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {snapshot.leaderboard.entries.length === 0 ? (
                      <TableRow className="border-accent-200 dark:border-accent-800">
                        <TableCell colSpan={5} className="py-8 text-center text-accent-500 dark:text-accent-400">
                          窗口内无聚合数据。
                        </TableCell>
                      </TableRow>
                    ) : (
                      snapshot.leaderboard.entries.map((entry) => (
                        <TableRow key={`${entry.rank}-${entry.key}`} className="border-accent-200 dark:border-accent-800">
                          <TableCell className="font-mono text-accent-500 dark:text-accent-400">{entry.rank}</TableCell>
                          <TableCell className="max-w-[200px] truncate font-medium text-accent-900 dark:text-accent-100">{entry.key || '—'}</TableCell>
                          <TableCell className="text-right font-mono text-emerald-700 dark:text-emerald-300">{formatMoney(entry.total_cost)}</TableCell>
                          <TableCell className="text-right font-mono text-accent-700 dark:text-accent-200">{formatInt(entry.request_count)}</TableCell>
                          <TableCell className="text-right font-mono text-accent-600 dark:text-accent-300">{formatInt(entry.total_tokens)}</TableCell>
                        </TableRow>
                      ))
                    )}
                  </TableBody>
                </Table>
              </CardContent>
            </Card>

            {/* 性能排行 */}
            <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
              <CardHeader className="flex flex-row items-center justify-between gap-3 p-5 pb-3">
                <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
                  <Gauge className="size-4 text-primary-600 dark:text-primary-300" />
                  性能指标 Top 10
                </CardTitle>
                <div className="flex overflow-hidden rounded-md border border-accent-200 dark:border-accent-700">
                  {PERFORMANCE_OPTIONS.map((option) => (
                    <button
                      key={option.value}
                      type="button"
                      onClick={() => setPerformanceBy(option.value)}
                      className={cn(
                        'px-2.5 py-1 text-xs font-medium transition-colors',
                        performanceBy === option.value
                          ? 'bg-primary-600 text-white'
                          : 'bg-white text-accent-600 hover:bg-accent-50 dark:bg-accent-900 dark:text-accent-300 dark:hover:bg-accent-800',
                      )}
                    >
                      {option.label}
                    </button>
                  ))}
                </div>
              </CardHeader>
              <CardContent className="p-0">
                <Table>
                  <TableHeader>
                    <TableRow className="border-accent-200 dark:border-accent-800">
                      <TableHead className="text-accent-500 dark:text-accent-400">标识</TableHead>
                      <TableHead className="text-right text-accent-500 dark:text-accent-400">TTFT</TableHead>
                      <TableHead className="text-right text-accent-500 dark:text-accent-400">TPS</TableHead>
                      <TableHead className="text-right text-accent-500 dark:text-accent-400">请求</TableHead>
                      <TableHead className="text-right text-accent-500 dark:text-accent-400">错误率</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {snapshot.performance.entries.length === 0 ? (
                      <TableRow className="border-accent-200 dark:border-accent-800">
                        <TableCell colSpan={5} className="py-8 text-center text-accent-500 dark:text-accent-400">
                          窗口内无性能数据。
                        </TableCell>
                      </TableRow>
                    ) : (
                      snapshot.performance.entries.map((entry) => (
                        <TableRow key={`${entry.rank}-${entry.key}`} className="border-accent-200 dark:border-accent-800">
                          <TableCell className="max-w-[200px] truncate font-medium text-accent-900 dark:text-accent-100">{entry.key || '—'}</TableCell>
                          <TableCell className="text-right font-mono text-accent-700 dark:text-accent-200">{formatDecimal(entry.avg_ttft_ms)}ms</TableCell>
                          <TableCell className="text-right font-mono text-accent-700 dark:text-accent-200">{formatDecimal(entry.avg_tps)}</TableCell>
                          <TableCell className="text-right font-mono text-accent-600 dark:text-accent-300">{formatInt(entry.request_count)}</TableCell>
                          <TableCell className="text-right">
                            <span className={cn('font-mono', Number(entry.error_rate) > 0.05 ? 'text-red-600 dark:text-red-300' : 'text-accent-600 dark:text-accent-300')}>
                              {formatRate(entry.error_rate)}
                            </span>
                          </TableCell>
                        </TableRow>
                      ))
                    )}
                  </TableBody>
                </Table>
              </CardContent>
            </Card>
          </section>

          {/* 告警事件 */}
          <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
            <CardHeader className="p-5 pb-3">
              <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
                <BellRing className="size-4 text-amber-500 dark:text-amber-300" />
                告警事件
              </CardTitle>
            </CardHeader>
            <CardContent className="p-0">
              {snapshot.alertEventsError ? (
                <div className="flex items-start gap-2 p-5 text-sm text-amber-700 dark:text-amber-300">
                  <AlertTriangle className="mt-0.5 size-4 shrink-0" />
                  <span>告警事件读取失败：{snapshot.alertEventsError}（平台 admin 需在后端 alert-events 端点提供 tenant_id；tenant-operator 自动使用其 scope）。</span>
                </div>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow className="border-accent-200 dark:border-accent-800">
                      <TableHead className="text-accent-500 dark:text-accent-400">规则</TableHead>
                      <TableHead className="text-accent-500 dark:text-accent-400">状态</TableHead>
                      <TableHead className="text-right text-accent-500 dark:text-accent-400">观测值</TableHead>
                      <TableHead className="text-right text-accent-500 dark:text-accent-400">阈值</TableHead>
                      <TableHead className="text-accent-500 dark:text-accent-400">触发时间</TableHead>
                      <TableHead className="text-right text-accent-500 dark:text-accent-400">操作</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {snapshot.alertEvents.length === 0 ? (
                      <TableRow className="border-accent-200 dark:border-accent-800">
                        <TableCell colSpan={6} className="py-8 text-center text-accent-500 dark:text-accent-400">
                          当前没有告警事件。
                        </TableCell>
                      </TableRow>
                    ) : (
                      snapshot.alertEvents.map((event) => {
                        const firing = event.state.toLowerCase() === 'firing' || event.state.toLowerCase() === 'active';
                        return (
                          <TableRow key={event.id} className="border-accent-200 dark:border-accent-800">
                            <TableCell className="font-medium text-accent-900 dark:text-accent-100">
                              <div>规则 #{event.rule_id}</div>
                              {event.dimensions && Object.keys(event.dimensions).length > 0 && (
                                <div className="mt-1 text-xs font-normal text-accent-400 dark:text-accent-500">
                                  {Object.entries(event.dimensions).map(([k, v]) => `${k}=${v}`).join(' · ')}
                                </div>
                              )}
                            </TableCell>
                            <TableCell>
                              <Badge variant={eventStateVariant(event.state)}>{event.state}</Badge>
                            </TableCell>
                            <TableCell className="text-right font-mono text-accent-700 dark:text-accent-200">
                              {event.observed_value.toLocaleString('zh-CN', { maximumFractionDigits: 4 })}
                            </TableCell>
                            <TableCell className="text-right font-mono text-accent-600 dark:text-accent-300">
                              {event.threshold_value !== undefined
                                ? event.threshold_value.toLocaleString('zh-CN', { maximumFractionDigits: 4 })
                                : '—'}
                            </TableCell>
                            <TableCell className="font-mono text-xs text-accent-600 dark:text-accent-300">{formatDateTime(event.fired_at)}</TableCell>
                            <TableCell className="text-right">
                              {firing ? (
                                <Button
                                  disabled={resolvingId === event.id}
                                  onClick={() => handleResolve(event.id)}
                                  size="sm"
                                  variant="outline"
                                >
                                  {resolvingId === event.id ? '处理中…' : '手动消解'}
                                </Button>
                              ) : (
                                <span className="text-xs text-accent-400 dark:text-accent-500">{formatDateTime(event.resolved_at)}</span>
                              )}
                            </TableCell>
                          </TableRow>
                        );
                      })
                    )}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>

          {/* severity 图例 + 数据来源 */}
          <div className="flex flex-wrap items-center gap-3 text-xs text-accent-500 dark:text-accent-400">
            <span className="inline-flex items-center gap-1">
              <Badge variant={severityVariant('critical')}>critical</Badge>
              <Badge variant={severityVariant('warning')}>warning</Badge>
              <Badge variant={severityVariant('info')}>info</Badge>
            </span>
            <span>数据来源：/v1/admin/usage/* 与 /v1/admin/alert-events（管理 token，只读，无本地 mock 兜底）。</span>
          </div>
        </>
      )}
    </div>
  );
}
