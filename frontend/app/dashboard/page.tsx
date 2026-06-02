'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Activity,
  Clock3,
  DatabaseZap,
  DollarSign,
  Gauge,
  HeartPulse,
  Layers,
  RefreshCw,
  ShieldAlert,
  Zap,
} from 'lucide-react';
import { DashboardError, DashboardLoading } from '@/components/dashboard/DashboardLoadState';
import { StatCard } from '@/components/dashboard/StatCard';
import { TrendChart } from '@/components/dashboard/TrendChart';
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
  loadDashboardSnapshot,
  type DashboardAccountRow,
  type DashboardHealthState,
  type DashboardScheduleStatus,
  type DashboardSnapshot,
} from '@/lib/api/dashboard';
import { cn } from '@/lib/utils';

function formatNumber(value: number) {
  return value.toLocaleString('zh-CN');
}

function formatPercent(value: number | null) {
  if (value === null) return '—';
  return `${(value * 100).toFixed(1)}%`;
}

function formatDateTime(value: string | Date | null) {
  if (!value) return '—';
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  return date.toLocaleString('zh-CN', {
    hour12: false,
  });
}

function formatDurationMs(value: number | null) {
  if (value === null) return '—';
  if (value >= 1000) return `${(value / 1000).toFixed(2)}s`;
  return `${value}ms`;
}

function formatCost(value: number) {
  return value.toLocaleString('zh-CN', {
    minimumFractionDigits: 4,
    maximumFractionDigits: 4,
  });
}

function formatCompactList(values: string[], limit = 3) {
  if (values.length === 0) return '后端未返回';
  const visible = values.slice(0, limit).join('、');
  return values.length > limit ? `${visible} +${values.length - limit}` : visible;
}

function getHealthLabel(state: DashboardHealthState) {
  const labels: Record<DashboardHealthState, string> = {
    operational: '健康',
    degraded: '降级',
    failed: '失败',
    cooling_down: '冷却中',
    error: '错误',
    unknown: '未知',
  };

  return labels[state];
}

function getHealthTone(state: DashboardHealthState) {
  if (state === 'operational') return 'default';
  if (state === 'degraded' || state === 'cooling_down') return 'secondary';
  return 'destructive';
}

function getScheduleLabel(status: DashboardScheduleStatus) {
  const labels: Record<DashboardScheduleStatus, string> = {
    active: '可调度',
    limited: '受限',
    disabled: '停用',
    requires_action: '需处理',
  };

  return labels[status];
}

function getScheduleTone(status: DashboardScheduleStatus) {
  if (status === 'active') return 'outline';
  if (status === 'requires_action') return 'destructive';
  return 'secondary';
}

function getHealthWidthClass(ratio: number) {
  if (ratio <= 0) return 'w-0';
  if (ratio >= 0.95) return 'w-[95%]';
  if (ratio >= 0.85) return 'w-[86%]';
  if (ratio >= 0.7) return 'w-[72%]';
  if (ratio >= 0.5) return 'w-[55%]';
  return 'w-[36%]';
}

function AlertRulesCard({ accounts }: { accounts: DashboardAccountRow[] }) {
  const riskyAccounts = accounts.filter((account) => (
    account.health_state !== 'operational'
    || account.schedule_status !== 'active'
    || account.in_flight >= account.cap
  ));

  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardHeader className="p-5 pb-3">
        <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
          <ShieldAlert className="size-4 text-amber-600 dark:text-amber-300" />
          异常告警条件
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 p-5 pt-0">
        {riskyAccounts.length === 0 ? (
          <div className="rounded-lg border border-emerald-200 bg-emerald-50 p-3 text-sm text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/30 dark:text-emerald-300">
            当前没有触发告警条件的账号。
          </div>
        ) : (
          riskyAccounts.map((account) => (
            <div
              key={account.id}
              className="rounded-lg border border-accent-200 bg-accent-50 p-3 text-sm dark:border-accent-800 dark:bg-accent-950/70"
            >
              <div className="flex items-center justify-between gap-3">
                <span className="min-w-0 truncate font-medium text-accent-900 dark:text-accent-100">{account.name}</span>
                <Badge variant={getHealthTone(account.health_state)}>{getHealthLabel(account.health_state)}</Badge>
              </div>
              <div className="mt-2 text-xs leading-5 text-accent-500 dark:text-accent-400">
                并发 {account.in_flight}/{account.cap} · 调度 {getScheduleLabel(account.schedule_status)}
                {account.failure_count > 0 ? ` · 失败计数 ${account.failure_count}` : ''}
              </div>
            </div>
          ))
        )}
      </CardContent>
    </Card>
  );
}

export default function DashboardPage() {
  const [snapshot, setSnapshot] = useState<DashboardSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const refreshDashboard = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const next = await loadDashboardSnapshot();
      setSnapshot(next);
    } catch (err: unknown) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refreshDashboard();
  }, [refreshDashboard]);

  if (!snapshot && loading) {
    return <DashboardLoading />;
  }

  if (!snapshot && error) {
    return <DashboardError error={error} loading={loading} onRetry={refreshDashboard} />;
  }

  if (!snapshot) {
    return <DashboardError error="dashboard snapshot missing" loading={loading} onRetry={refreshDashboard} />;
  }

  return (
    <DashboardContent
      error={error}
      loading={loading}
      onRefresh={refreshDashboard}
      snapshot={snapshot}
    />
  );
}

function DashboardContent({
  error,
  loading,
  onRefresh,
  snapshot,
}: {
  error: string;
  loading: boolean;
  onRefresh: () => void;
  snapshot: DashboardSnapshot;
}) {
  const usage = snapshot.usage;
  const accounts = snapshot.accounts;
  const tokensToday = usage.input_tokens + usage.output_tokens + usage.cache_creation_tokens + usage.cache_read_tokens;
  const healthyRatio = snapshot.health_stats.total > 0
    ? snapshot.health_stats.healthy / snapshot.health_stats.total
    : null;

  const metricCards = useMemo(() => [
    {
      title: '今日 Token 用量',
      value: formatNumber(tokensToday),
      icon: DatabaseZap,
      description: '输入、输出、缓存创建与读取合计',
      detail: `输入 ${formatNumber(usage.input_tokens)} / 输出 ${formatNumber(usage.output_tokens)}`,
      tone: 'primary' as const,
    },
    {
      title: '今日成本',
      value: formatCost(usage.actual_cost),
      icon: DollarSign,
      description: 'usage.actual_cost 汇总',
      detail: '未做本地币种换算',
      tone: 'emerald' as const,
    },
    {
      title: '请求数',
      value: formatNumber(usage.request_count),
      icon: Zap,
      description: '今日 usage 记录数',
      detail: usage.usage_has_more
        ? `分页截断，已统计 ${formatNumber(usage.request_count)} 条`
        : `待对账 ${formatNumber(usage.pending_reconciliation_count)} 条`,
      tone: 'blue' as const,
    },
    {
      title: 'P95 结算耗时',
      value: formatDurationMs(usage.settlement_p95_ms),
      icon: Clock3,
      description: 'settled_at - requested_at',
      detail: `P50 ${formatDurationMs(usage.settlement_p50_ms)} / P99 ${formatDurationMs(usage.settlement_p99_ms)}`,
      tone: 'amber' as const,
    },
    {
      title: '并发数',
      value: formatNumber(snapshot.in_flight),
      icon: Activity,
      description: '当前飞行中请求',
      detail: `容量上限 ${formatNumber(snapshot.total_cap_concurrency)}`,
      tone: 'slate' as const,
    },
    {
      title: '缓存读取占比',
      value: formatPercent(usage.cache_hit_ratio),
      icon: Gauge,
      description: 'read / (creation + read)',
      detail: `读取 ${formatNumber(usage.cache_read_tokens)} / 创建 ${formatNumber(usage.cache_creation_tokens)}`,
      tone: 'primary' as const,
    },
  ], [snapshot, tokensToday, usage]);

  return (
    <div className="space-y-6">
      {error && (
        <div role="alert" className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-200">
          刷新失败：{error}；下方仍为上次成功的真实后端响应。
        </div>
      )}
      {snapshot.source_warnings.map((warning) => (
        <div key={warning} role="status" className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-200">
          {warning}
        </div>
      ))}

      <section className="flex flex-col justify-between gap-4 rounded-lg border border-accent-200 bg-white px-5 py-4 shadow-card dark:border-accent-800 dark:bg-accent-900/70 md:flex-row md:items-center">
        <div className="min-w-0">
          <p className="text-xs font-medium text-primary-700 dark:text-primary-300">P1 总览</p>
          <h1 className="mt-1 text-2xl font-bold tracking-normal text-accent-950 dark:text-white">运营总览</h1>
          <p className="mt-2 text-sm text-accent-500 dark:text-accent-400">真实后端账号池健康、成本、用量与缓存效率集中视图</p>
        </div>
        <div className="grid gap-2 text-sm text-accent-600 dark:text-accent-300 sm:grid-cols-2 md:min-w-[560px] xl:grid-cols-4">
          <div className="rounded-lg border border-accent-200 bg-accent-50 px-3 py-2 dark:border-accent-800 dark:bg-accent-950/70">
            <span className="block text-xs text-accent-400 dark:text-accent-500">加载时间</span>
            <span className="mt-1 block font-mono tabular-nums">{formatDateTime(snapshot.loaded_at)}</span>
          </div>
          <div className="rounded-lg border border-accent-200 bg-accent-50 px-3 py-2 dark:border-accent-800 dark:bg-accent-950/70">
            <span className="block text-xs text-accent-400 dark:text-accent-500">数据更新时间</span>
            <span className="mt-1 block font-mono tabular-nums">{formatDateTime(snapshot.latest_backend_event_at)}</span>
          </div>
          <div className="rounded-lg border border-accent-200 bg-accent-50 px-3 py-2 dark:border-accent-800 dark:bg-accent-950/70">
            <span className="block text-xs text-accent-400 dark:text-accent-500">Provider catalog</span>
            <span className="mt-1 block font-mono tabular-nums">
              {snapshot.catalog.enabled_provider_count}/{snapshot.catalog.provider_count} enabled · tenant {snapshot.tenant_id ?? '—'}
            </span>
          </div>
          <div className="rounded-lg border border-accent-200 bg-accent-50 px-3 py-2 dark:border-accent-800 dark:bg-accent-950/70">
            <span className="block text-xs text-accent-400 dark:text-accent-500">可用模型</span>
            <span className="mt-1 block truncate">{formatCompactList(snapshot.catalog.available_models, 2)}</span>
          </div>
        </div>
        <Button
          className="shrink-0"
          disabled={loading}
          onClick={onRefresh}
          size="sm"
          variant="outline"
        >
          <RefreshCw className={cn('size-4', loading && 'animate-spin')} />
          刷新
        </Button>
      </section>

      <section className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-6" aria-label="核心指标">
        {metricCards.map((metric) => (
          <StatCard
            key={metric.title}
            title={metric.title}
            value={metric.value}
            icon={metric.icon}
            description={metric.description}
            detail={metric.detail}
            tone={metric.tone}
          />
        ))}
      </section>

      <TrendChart data={snapshot.chart_points} />

      <section className="grid gap-6 xl:grid-cols-[minmax(0,2fr)_minmax(280px,1fr)]">
        <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
          <CardHeader className="p-5 pb-3">
            <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
              <Layers className="size-4 text-primary-600 dark:text-primary-300" />
              Top 5 供应商账号
            </CardTitle>
          </CardHeader>
          <CardContent className="p-0">
            <Table>
              <TableHeader>
                <TableRow className="border-accent-200 dark:border-accent-800">
                  <TableHead className="text-accent-500 dark:text-accent-400">账号</TableHead>
                  <TableHead className="text-accent-500 dark:text-accent-400">供应商</TableHead>
                  <TableHead className="text-accent-500 dark:text-accent-400">模型</TableHead>
                  <TableHead className="text-accent-500 dark:text-accent-400">健康状态</TableHead>
                  <TableHead className="text-accent-500 dark:text-accent-400">并发</TableHead>
                  <TableHead className="text-accent-500 dark:text-accent-400">调度</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {accounts.length === 0 ? (
                  <TableRow className="border-accent-200 dark:border-accent-800">
                    <TableCell colSpan={6} className="py-8 text-center text-accent-500 dark:text-accent-400">
                      后端未返回 provider account。
                    </TableCell>
                  </TableRow>
                ) : (
                  accounts.map((account) => (
                    <TableRow key={account.id} className="border-accent-200 dark:border-accent-800">
                      <TableCell className="font-medium text-accent-900 dark:text-accent-100">
                        <div>{account.name}</div>
                        <div className="mt-1 text-xs font-normal text-accent-400 dark:text-accent-500">{account.channel}</div>
                      </TableCell>
                      <TableCell className="max-w-[180px] truncate text-accent-600 dark:text-accent-300">{account.provider}</TableCell>
                      <TableCell className="max-w-[180px] truncate text-accent-600 dark:text-accent-300">
                        {formatCompactList(account.models, 2)}
                      </TableCell>
                      <TableCell>
                        <Badge variant={getHealthTone(account.health_state)}>
                          {getHealthLabel(account.health_state)}
                        </Badge>
                      </TableCell>
                      <TableCell className="font-mono text-accent-700 dark:text-accent-200">
                        {account.in_flight}/{account.cap}
                      </TableCell>
                      <TableCell>
                        <Badge
                          className={cn(account.schedule_status === 'active' && 'border-primary-200 text-primary-700 dark:border-primary-900 dark:text-primary-300')}
                          variant={getScheduleTone(account.schedule_status)}
                        >
                          {getScheduleLabel(account.schedule_status)}
                        </Badge>
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </CardContent>
        </Card>

        <div className="flex flex-col gap-6">
          <AlertRulesCard accounts={snapshot.alert_accounts} />

          <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
            <CardHeader className="p-5 pb-3">
              <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
                <HeartPulse className="size-4 text-primary-600 dark:text-primary-300" />
                健康账号比例
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-5 p-5 pt-0">
              <div>
                <div className="flex items-end justify-between gap-3">
                  <div>
                    <div className="text-3xl font-bold tracking-normal text-accent-950 dark:text-white">
                      {formatPercent(healthyRatio)}
                    </div>
                    <div className="mt-1 text-sm text-accent-500 dark:text-accent-400">
                      {snapshot.health_stats.healthy} / {snapshot.health_stats.total} 健康
                    </div>
                  </div>
                  <Badge variant="secondary">
                    降级 {snapshot.health_stats.degraded} · 失败 {snapshot.health_stats.failed}
                  </Badge>
                </div>
                <div className="mt-4 h-3 overflow-hidden rounded-full bg-accent-100 dark:bg-accent-800">
                  <div className={cn('h-full rounded-full bg-primary-500 shadow-glow', getHealthWidthClass(healthyRatio ?? 0))} />
                </div>
              </div>
              <div className="rounded-lg border border-accent-200 bg-accent-50 p-3 text-sm text-accent-600 dark:border-accent-800 dark:bg-accent-950/70 dark:text-accent-300">
                健康比例来自 provider account 列表与逐账号 health snapshot；health 请求失败时页面进入错误态。
              </div>
            </CardContent>
          </Card>
        </div>
      </section>
    </div>
  );
}

function errorMessage(err: unknown) {
  if (err instanceof Error) return err.message;
  return String(err);
}
