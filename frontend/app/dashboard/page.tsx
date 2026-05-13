import {
  Activity,
  Clock3,
  DatabaseZap,
  DollarSign,
  Gauge,
  HeartPulse,
  Layers,
  ShieldAlert,
  Zap,
} from 'lucide-react';
import { StatCard } from '@/components/dashboard/StatCard';
import { TrendChart } from '@/components/dashboard/TrendChart';
import { Badge } from '@/components/ui/badge';
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
  MOCK_PROVIDER_ACCOUNTS,
  MOCK_USAGE,
  type ProviderAccountMock,
} from '@/lib/dashboard-mock';
import { cn } from '@/lib/utils';

function formatNumber(value: number) {
  return value.toLocaleString('zh-CN');
}

function formatPercent(value: number) {
  return `${(value * 100).toFixed(1)}%`;
}

function formatDateTime(value: Date) {
  return value.toLocaleString('zh-CN', {
    hour12: false,
  });
}

function getHealthLabel(state: ProviderAccountMock['health_state']) {
  const labels: Record<ProviderAccountMock['health_state'], string> = {
    operational: '健康',
    degraded: '降级',
    failed: '失败',
    cooling_down: '冷却中',
  };

  return labels[state];
}

function getHealthTone(state: ProviderAccountMock['health_state']) {
  if (state === 'operational') return 'default';
  if (state === 'degraded' || state === 'cooling_down') return 'secondary';
  return 'destructive';
}

function getQuotaLabel(status: ProviderAccountMock['quota_status']) {
  return status === 'active' ? '可用' : '已耗尽';
}

function getHealthWidthClass(ratio: number) {
  if (ratio >= 0.95) return 'w-[95%]';
  if (ratio >= 0.85) return 'w-[86%]';
  if (ratio >= 0.7) return 'w-[72%]';
  if (ratio >= 0.5) return 'w-[55%]';
  return 'w-[36%]';
}

function AlertRulesCard({ accounts }: { accounts: ProviderAccountMock[] }) {
  const riskyAccounts = accounts.filter((account) => (
    account.health_state !== 'operational'
    || account.quota_status === 'exhausted'
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
                并发 {account.in_flight}/{account.cap} · 额度 {getQuotaLabel(account.quota_status)}
              </div>
            </div>
          ))
        )}
      </CardContent>
    </Card>
  );
}

export default function DashboardPage() {
  const usage = MOCK_USAGE;
  const accounts = MOCK_PROVIDER_ACCOUNTS.slice(0, 5);
  const tokensToday = usage.input_tokens + usage.output_tokens + usage.cache_tokens;
  const healthyRatio = usage.health_stats.total > 0
    ? usage.health_stats.healthy / usage.health_stats.total
    : 0;
  const lastDispatchAt = accounts.reduce((latest, account) => {
    const current = new Date(account.last_dispatch_at);
    return current > latest ? current : latest;
  }, new Date(0));

  const metricCards = [
    {
      title: '今日 Token 用量',
      value: formatNumber(tokensToday),
      icon: DatabaseZap,
      description: '输入、输出与缓存读取合计',
      detail: `输入 ${formatNumber(usage.input_tokens)} / 输出 ${formatNumber(usage.output_tokens)}`,
      tone: 'primary' as const,
    },
    {
      title: '今日成本',
      value: `$${usage.cost_usd.toFixed(2)}`,
      icon: DollarSign,
      description: '按当前模拟汇总计算',
      detail: `折合约 ${usage.cost_rmb.toFixed(2)} RMB`,
      tone: 'emerald' as const,
    },
    {
      title: '请求数',
      value: formatNumber(usage.request_count),
      icon: Zap,
      description: '今日网关请求总数',
      detail: `P50 延迟 ${usage.latency_p50}ms`,
      tone: 'blue' as const,
    },
    {
      title: 'P95 延迟',
      value: `${usage.latency_p95}ms`,
      icon: Clock3,
      description: '尾部延迟观察值',
      detail: `P99 延迟 ${usage.latency_p99}ms`,
      tone: 'amber' as const,
    },
    {
      title: '并发数',
      value: formatNumber(usage.in_flight),
      icon: Activity,
      description: '当前飞行中请求',
      detail: `容量上限 ${formatNumber(usage.total_cap_concurrency)}`,
      tone: 'slate' as const,
    },
    {
      title: '缓存命中率',
      value: formatPercent(usage.cache_hit_ratio),
      icon: Gauge,
      description: '缓存读取占比',
      detail: `缓存 Token ${formatNumber(usage.cache_tokens)}`,
      tone: 'primary' as const,
    },
  ];

  return (
    <div className="space-y-6">
      <section className="flex flex-col justify-between gap-4 rounded-lg border border-accent-200 bg-white px-5 py-4 shadow-card dark:border-accent-800 dark:bg-accent-900/70 md:flex-row md:items-center">
        <div className="min-w-0">
          <p className="text-xs font-medium text-primary-700 dark:text-primary-300">P1 总览</p>
          <h1 className="mt-1 text-2xl font-bold tracking-normal text-accent-950 dark:text-white">运营总览</h1>
          <p className="mt-2 text-sm text-accent-500 dark:text-accent-400">账号池健康、成本、延迟与缓存效率集中视图</p>
        </div>
        <div className="grid gap-2 text-sm text-accent-600 dark:text-accent-300 sm:grid-cols-2 md:min-w-[360px]">
          <div className="rounded-lg border border-accent-200 bg-accent-50 px-3 py-2 dark:border-accent-800 dark:bg-accent-950/70">
            <span className="block text-xs text-accent-400 dark:text-accent-500">当前时间</span>
            <span className="mt-1 block font-mono tabular-nums">{formatDateTime(new Date())}</span>
          </div>
          <div className="rounded-lg border border-accent-200 bg-accent-50 px-3 py-2 dark:border-accent-800 dark:bg-accent-950/70">
            <span className="block text-xs text-accent-400 dark:text-accent-500">数据更新时间</span>
            <span className="mt-1 block font-mono tabular-nums">{formatDateTime(lastDispatchAt)}</span>
          </div>
        </div>
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

      <TrendChart />

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
                  <TableHead className="text-accent-500 dark:text-accent-400">健康状态</TableHead>
                  <TableHead className="text-accent-500 dark:text-accent-400">并发</TableHead>
                  <TableHead className="text-accent-500 dark:text-accent-400">额度</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {accounts.map((account) => (
                  <TableRow key={account.id} className="border-accent-200 dark:border-accent-800">
                    <TableCell className="font-medium text-accent-900 dark:text-accent-100">{account.name}</TableCell>
                    <TableCell className="text-accent-600 dark:text-accent-300">{account.provider}</TableCell>
                    <TableCell>
                      <Badge variant={getHealthTone(account.health_state)}>
                        {getHealthLabel(account.health_state)}
                      </Badge>
                    </TableCell>
                    <TableCell className="font-mono text-accent-700 dark:text-accent-200">
                      {account.in_flight}/{account.cap}
                    </TableCell>
                    <TableCell>
                      <Badge variant={account.quota_status === 'active' ? 'outline' : 'destructive'} className={cn(account.quota_status === 'active' && 'border-primary-200 text-primary-700 dark:border-primary-900 dark:text-primary-300')}>
                        {getQuotaLabel(account.quota_status)}
                      </Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>

        <div className="flex flex-col gap-6">
          <AlertRulesCard accounts={accounts} />

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
                      {usage.health_stats.healthy} / {usage.health_stats.total} 健康
                    </div>
                  </div>
                  <Badge variant="secondary">
                    降级 {usage.health_stats.degraded} · 失败 {usage.health_stats.failed}
                  </Badge>
                </div>
                <div className="mt-4 h-3 overflow-hidden rounded-full bg-accent-100 dark:bg-accent-800">
                  <div className={cn('h-full rounded-full bg-primary-500 shadow-glow', getHealthWidthClass(healthyRatio))} />
                </div>
              </div>
              <div className="rounded-lg border border-accent-200 bg-accent-50 p-3 text-sm text-accent-600 dark:border-accent-800 dark:bg-accent-950/70 dark:text-accent-300">
                当前模拟数据中，大多数账号处于可调度状态；失败账号应进入隔离或人工复核队列。
              </div>
            </CardContent>
          </Card>
        </div>
      </section>
    </div>
  );
}
