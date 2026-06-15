'use client';

import { Boxes } from 'lucide-react';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { cn } from '@/lib/utils';
import type { ModelStatRow } from '@/lib/api/usage';

function fmtCost(v: number): string {
  return `$${v.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 4 })}`;
}
function fmtNum(v: number): string {
  return v.toLocaleString('zh-CN');
}

// 占比条用离散档位（Tailwind 不接受运行时任意 % 类名）。
function shareWidth(ratio: number): string {
  if (ratio <= 0) return 'w-0';
  if (ratio < 0.1) return 'w-[8%]';
  if (ratio < 0.25) return 'w-1/4';
  if (ratio < 0.4) return 'w-2/5';
  if (ratio < 0.55) return 'w-1/2';
  if (ratio < 0.7) return 'w-2/3';
  if (ratio < 0.85) return 'w-4/5';
  if (ratio < 1) return 'w-[92%]';
  return 'w-full';
}

interface ModelStatsCardProps {
  rows: ModelStatRow[];
  isLoading?: boolean;
  hasKey: boolean;
  topN?: number;
}

// 模型维度统计：按 requested_model 聚合的 Top 模型表 + 花费占比。
// 来源行为：sub2api getDashboardModels(ModelStat 表) + new-api 消耗分布图（按模型配额占比 + 总计）。
export function ModelStatsCard({ rows, isLoading = false, hasKey, topN = 8 }: ModelStatsCardProps) {
  const shown = rows.slice(0, topN);
  const restCount = rows.length - shown.length;

  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardHeader className="flex flex-row items-center justify-between p-5 pb-3">
        <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
          <Boxes className="size-4 text-primary-600 dark:text-primary-300" />
          模型维度
        </CardTitle>
        {rows.length > 0 && (
          <span className="text-[11px] text-accent-400 dark:text-accent-500">{rows.length} 个模型 · 按花费排序</span>
        )}
      </CardHeader>
      <CardContent className="p-5 pt-0">
        {isLoading ? (
          <div className="space-y-3 py-2">
            {[0, 1, 2].map((i) => (
              <div key={i} className="h-9 animate-pulse rounded-lg bg-accent-100 dark:bg-accent-800/60" />
            ))}
          </div>
        ) : !hasKey ? (
          <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
            填入 API Key 后展示模型维度消耗。
          </div>
        ) : shown.length === 0 ? (
          <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
            所选窗口内暂无模型消耗。
          </div>
        ) : (
          <ul className="space-y-3">
            {shown.map((r) => (
              <li key={r.model}>
                <div className="flex items-baseline justify-between gap-3">
                  <span className="min-w-0 truncate text-sm font-medium text-accent-900 dark:text-accent-100" title={r.model}>
                    {r.model}
                  </span>
                  <span className="shrink-0 font-mono text-sm tabular-nums text-accent-900 dark:text-accent-100">
                    {fmtCost(r.cost)}
                  </span>
                </div>
                <div className="mt-1 flex items-center gap-3">
                  <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-accent-100 dark:bg-accent-800">
                    <div className={cn('h-full rounded-full bg-primary-500', shareWidth(r.costShare))} />
                  </div>
                  <span className="w-10 shrink-0 text-right text-[11px] tabular-nums text-accent-500 dark:text-accent-400">
                    {(r.costShare * 100).toFixed(1)}%
                  </span>
                </div>
                <div className="mt-0.5 text-[11px] text-accent-400 dark:text-accent-500">
                  {fmtNum(r.requests)} 次请求 · {fmtNum(r.tokens)} token
                </div>
              </li>
            ))}
            {restCount > 0 && (
              <li className="pt-1 text-[11px] text-accent-400 dark:text-accent-500">另有 {restCount} 个模型未显示。</li>
            )}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
