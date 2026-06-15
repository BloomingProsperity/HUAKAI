'use client';

import { useEffect, useState } from 'react';
import {
  Bar,
  BarChart,
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { cn } from '@/lib/utils';
import type { TrendPoint } from '@/lib/api/usage';

type Metric = 'cost' | 'tokens' | 'requests';

const METRICS: { key: Metric; label: string }[] = [
  { key: 'cost', label: '花费' },
  { key: 'tokens', label: 'Token' },
  { key: 'requests', label: '请求数' },
];

function formatTick(value: number, metric: Metric): string {
  if (metric === 'cost') return value.toFixed(2);
  if (value >= 1000) return `${(value / 1000).toFixed(1)}k`;
  return String(value);
}

interface UsageTrendChartProps {
  data: TrendPoint[];
  isLoading?: boolean;
}

export function UsageTrendChart({ data, isLoading = false }: UsageTrendChartProps) {
  const [mounted, setMounted] = useState(false);
  const [metric, setMetric] = useState<Metric>('cost');
  const [shape, setShape] = useState<'line' | 'bar'>('bar');

  useEffect(() => {
    setMounted(true);
  }, []);

  const active = METRICS.find((m) => m.key === metric)!;

  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardHeader className="flex flex-col gap-3 p-5 pb-3 sm:flex-row sm:items-center sm:justify-between">
        <CardTitle className="text-base font-semibold tracking-normal text-accent-950 dark:text-white">
          用量趋势 · {active.label}
        </CardTitle>
        <div className="flex flex-wrap items-center gap-2">
          <div className="flex rounded-lg border border-accent-200 p-0.5 dark:border-accent-800">
            {METRICS.map((m) => (
              <button
                key={m.key}
                type="button"
                onClick={() => setMetric(m.key)}
                className={cn(
                  'rounded-md px-2.5 py-1 text-xs font-medium transition-colors',
                  metric === m.key
                    ? 'bg-primary-500 text-white shadow-sm'
                    : 'text-accent-500 hover:text-accent-800 dark:text-accent-400 dark:hover:text-accent-100',
                )}
              >
                {m.label}
              </button>
            ))}
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setShape((s) => (s === 'bar' ? 'line' : 'bar'))}
          >
            {shape === 'bar' ? '折线' : '柱状'}
          </Button>
        </div>
      </CardHeader>
      <CardContent className="h-[300px] min-h-[300px] p-5 pt-0">
        {!mounted || isLoading ? (
          <div className="flex h-full items-center justify-center rounded-lg border border-dashed border-accent-200 bg-accent-50 text-sm text-accent-400 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-500">
            图表加载中
          </div>
        ) : data.length === 0 ? (
          <div className="flex h-full items-center justify-center rounded-lg border border-dashed border-accent-200 bg-accent-50 px-4 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
            所选窗口内暂无用量记录。
          </div>
        ) : (
          <ResponsiveContainer width="100%" height="100%" minWidth={320} minHeight={240}>
            {shape === 'bar' ? (
              <BarChart data={data}>
                <CartesianGrid strokeDasharray="3 3" stroke="rgba(148, 163, 184, 0.28)" vertical={false} />
                <XAxis dataKey="label" tickLine={false} axisLine={false} fontSize={12} />
                <YAxis tickLine={false} axisLine={false} fontSize={12} tickFormatter={(v) => formatTick(Number(v), metric)} />
                <Tooltip
                  formatter={(value) => [formatTick(Number(value), metric), active.label]}
                  labelFormatter={(label) => `${label}`}
                />
                <Bar dataKey={metric} name={active.label} fill="#14b8a6" radius={[4, 4, 0, 0]} />
              </BarChart>
            ) : (
              <LineChart data={data}>
                <CartesianGrid strokeDasharray="3 3" stroke="rgba(148, 163, 184, 0.28)" vertical={false} />
                <XAxis dataKey="label" tickLine={false} axisLine={false} fontSize={12} />
                <YAxis tickLine={false} axisLine={false} fontSize={12} tickFormatter={(v) => formatTick(Number(v), metric)} />
                <Tooltip
                  formatter={(value) => [formatTick(Number(value), metric), active.label]}
                  labelFormatter={(label) => `${label}`}
                />
                <Line type="monotone" dataKey={metric} name={active.label} stroke="#14b8a6" strokeWidth={2} dot={false} />
              </LineChart>
            )}
          </ResponsiveContainer>
        )}
      </CardContent>
    </Card>
  );
}
