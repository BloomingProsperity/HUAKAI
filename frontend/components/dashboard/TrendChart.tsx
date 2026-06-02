'use client';

import { useEffect, useState } from 'react';
import {
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from 'recharts';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import type { DashboardChartPoint } from '@/lib/api/dashboard';

interface TrendChartProps {
  data: DashboardChartPoint[];
  isLoading?: boolean;
}

export function TrendChart({ data, isLoading = false }: TrendChartProps) {
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    setMounted(true);
  }, []);

  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardHeader className="p-5 pb-3">
        <CardTitle className="text-base font-semibold tracking-normal text-accent-950 dark:text-white">
          24h 缓存命中率趋势
        </CardTitle>
      </CardHeader>
      <CardContent className="h-[280px] min-h-[280px] p-5 pt-0">
        {!mounted || isLoading ? (
          <div className="flex h-full items-center justify-center rounded-lg border border-dashed border-accent-200 bg-accent-50 text-sm text-accent-400 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-500">
            图表加载中
          </div>
        ) : data.length < 2 ? (
          <div className="flex h-full items-center justify-center rounded-lg border border-dashed border-accent-200 bg-accent-50 px-4 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
            真实 usage 记录不足，暂不绘制趋势；不会用本地假曲线补齐。
          </div>
        ) : (
          <ResponsiveContainer width="100%" height="100%" minWidth={320} minHeight={220}>
            <LineChart data={data}>
              <CartesianGrid strokeDasharray="3 3" stroke="rgba(148, 163, 184, 0.28)" vertical={false} />
              <XAxis dataKey="time" tickLine={false} axisLine={false} />
              <YAxis domain={[0, 100]} tickLine={false} axisLine={false} tickFormatter={(value) => `${value}%`} />
              <Tooltip
                formatter={(value) => [`${value}%`, '缓存读取占比']}
                labelFormatter={(label) => `${label} 时`}
              />
              <Line
                type="monotone"
                name="缓存读取占比"
                dataKey="hit_rate"
                stroke="#14b8a6"
                strokeWidth={2}
                dot={false}
              />
            </LineChart>
          </ResponsiveContainer>
        )}
      </CardContent>
    </Card>
  );
}
