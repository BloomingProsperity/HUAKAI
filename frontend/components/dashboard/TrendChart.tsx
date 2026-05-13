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
import { MOCK_CHART_DATA } from '@/lib/dashboard-mock';

export function TrendChart() {
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
        {!mounted ? (
          <div className="flex h-full items-center justify-center rounded-lg border border-dashed border-accent-200 bg-accent-50 text-sm text-accent-400 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-500">
            图表加载中
          </div>
        ) : (
          <ResponsiveContainer width="100%" height="100%" minWidth={320} minHeight={220}>
            <LineChart data={MOCK_CHART_DATA}>
              <CartesianGrid strokeDasharray="3 3" stroke="rgba(148, 163, 184, 0.28)" vertical={false} />
              <XAxis dataKey="time" tickLine={false} axisLine={false} />
              <YAxis domain={[0, 50]} tickLine={false} axisLine={false} tickFormatter={(value) => `${value}%`} />
              <Tooltip
                formatter={(value) => [`${value}%`, '缓存命中率']}
                labelFormatter={(label) => `${label} 时`}
              />
              <Line
                type="monotone"
                name="缓存命中率"
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
