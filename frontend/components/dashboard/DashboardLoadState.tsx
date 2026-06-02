'use client';

import {
  Activity,
  AlertTriangle,
  Clock3,
  DatabaseZap,
  DollarSign,
  Gauge,
  RefreshCw,
  Zap,
} from 'lucide-react';
import { StatCard } from '@/components/dashboard/StatCard';
import { TrendChart } from '@/components/dashboard/TrendChart';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { cn } from '@/lib/utils';

export function DashboardLoading() {
  const loadingCards = [
    { title: '今日 Token 用量', icon: DatabaseZap, tone: 'primary' as const },
    { title: '今日成本', icon: DollarSign, tone: 'emerald' as const },
    { title: '请求数', icon: Zap, tone: 'blue' as const },
    { title: 'P95 结算耗时', icon: Clock3, tone: 'amber' as const },
    { title: '并发数', icon: Activity, tone: 'slate' as const },
    { title: '缓存读取占比', icon: Gauge, tone: 'primary' as const },
  ];

  return (
    <div className="space-y-6">
      <section className="flex flex-col justify-between gap-4 rounded-lg border border-accent-200 bg-white px-5 py-4 shadow-card dark:border-accent-800 dark:bg-accent-900/70 md:flex-row md:items-center">
        <div className="min-w-0">
          <p className="text-xs font-medium text-primary-700 dark:text-primary-300">P1 总览</p>
          <h1 className="mt-1 text-2xl font-bold tracking-normal text-accent-950 dark:text-white">运营总览</h1>
          <p className="mt-2 text-sm text-accent-500 dark:text-accent-400">正在读取真实后端运营数据</p>
        </div>
        <Badge variant="secondary">加载中</Badge>
      </section>
      <section className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-6" aria-label="核心指标加载中">
        {loadingCards.map((metric) => (
          <StatCard
            key={metric.title}
            description="等待后端响应"
            detail="—"
            icon={metric.icon}
            title={metric.title}
            tone={metric.tone}
            value="—"
          />
        ))}
      </section>
      <TrendChart data={[]} isLoading />
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardContent className="p-6 text-sm text-accent-500 dark:text-accent-400">
          provider account、health、usage、provider、channel 与 account mode 正在加载。
        </CardContent>
      </Card>
    </div>
  );
}

export function DashboardError({
  error,
  loading,
  onRetry,
}: {
  error: string;
  loading: boolean;
  onRetry: () => void;
}) {
  return (
    <div className="space-y-6">
      <section className="flex flex-col justify-between gap-4 rounded-lg border border-accent-200 bg-white px-5 py-4 shadow-card dark:border-accent-800 dark:bg-accent-900/70 md:flex-row md:items-center">
        <div className="min-w-0">
          <p className="text-xs font-medium text-primary-700 dark:text-primary-300">P1 总览</p>
          <h1 className="mt-1 text-2xl font-bold tracking-normal text-accent-950 dark:text-white">运营总览</h1>
          <p className="mt-2 text-sm text-accent-500 dark:text-accent-400">真实后端数据读取失败</p>
        </div>
        <Button disabled={loading} onClick={onRetry} size="sm" variant="outline">
          <RefreshCw className={cn('size-4', loading && 'animate-spin')} />
          重试
        </Button>
      </section>
      <Card className="border-red-200 bg-red-50 shadow-card dark:border-red-900/60 dark:bg-red-950/30">
        <CardHeader className="p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-red-800 dark:text-red-200">
            <AlertTriangle className="size-4" />
            后端读取失败
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3 p-5 pt-0 text-sm text-red-700 dark:text-red-200">
          <p className="font-mono break-words">{error}</p>
          <p>页面没有使用本地 mock 数据兜底。</p>
        </CardContent>
      </Card>
    </div>
  );
}
