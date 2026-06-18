'use client';

import { Gauge } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { cn } from '@/lib/utils';
import type { QuotaWindow, QuotaWindowKind } from '@/lib/api/usage';
import { quotaMetricLabel, quotaWindowKey } from '@/lib/api/quota-window-display';

const WINDOW_LABELS: Record<string, string> = {
  none: '无窗口限制',
  fixed: '固定窗口',
  calendar_day: '日额度',
  calendar_week: '周额度',
  calendar_month: '月额度',
};

function windowLabel(kind: QuotaWindowKind): string {
  return WINDOW_LABELS[kind] ?? kind;
}

// 进度条宽度用离散档位（Tailwind 不支持任意运行时 % 类名）。
function widthClass(ratio: number): string {
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

function barTone(ratio: number, overage: boolean): string {
  if (overage || ratio >= 1) return 'bg-red-500';
  if (ratio >= 0.85) return 'bg-amber-500';
  return 'bg-primary-500 shadow-glow';
}

function num(value: string): number {
  return Number.parseFloat(value) || 0;
}

function formatNum(value: number): string {
  return value.toLocaleString('zh-CN', { maximumFractionDigits: 4 });
}

interface QuotaWindowsCardProps {
  windows: QuotaWindow[];
}

export function QuotaWindowsCard({ windows }: QuotaWindowsCardProps) {
  // 隐藏 none（无限制）窗口的进度条意义不大，但仍展示一行说明。
  const meaningful = windows.filter((w) => w.window_kind !== 'none');

  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardHeader className="p-5 pb-3">
        <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
          <Gauge className="size-4 text-primary-600 dark:text-primary-300" />
          额度窗口
        </CardTitle>
      </CardHeader>
      <CardContent className="space-y-5 p-5 pt-0">
        {windows.length === 0 ? (
          <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 p-4 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
            后端未返回任何额度窗口（可能尚未配置配额策略）。
          </div>
        ) : (
          meaningful.map((w) => {
            const cap = num(w.cap);
            const consumed = num(w.consumed);
            const overage = num(w.overage) > 0;
            const ratio = cap > 0 ? consumed / cap : overage ? 1 : 0;
            const pct = Math.min(ratio, 1) * 100;
            return (
              <div key={quotaWindowKey(w)}>
                <div className="flex items-end justify-between gap-3">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="text-sm font-semibold text-accent-900 dark:text-accent-100">
                        {quotaMetricLabel(w.metric)} · {windowLabel(w.window_kind)}
                      </span>
                      {overage && <Badge variant="destructive">已超额</Badge>}
                    </div>
                    <div className="mt-1 text-xs text-accent-500 dark:text-accent-400">
                      已用 {formatNum(consumed)} / 上限 {cap > 0 ? formatNum(cap) : '不限'} · 剩余 {formatNum(num(w.remaining))}
                    </div>
                  </div>
                  <div className="shrink-0 text-right">
                    <div className="text-lg font-bold tabular-nums text-accent-950 dark:text-white">
                      {cap > 0 ? `${pct.toFixed(0)}%` : '—'}
                    </div>
                    <div className="text-[11px] text-accent-400 dark:text-accent-500">{w.request_count} 次请求</div>
                  </div>
                </div>
                <div className="mt-2 h-2.5 overflow-hidden rounded-full bg-accent-100 dark:bg-accent-800">
                  <div className={cn('h-full rounded-full', barTone(ratio, overage), widthClass(ratio))} />
                </div>
              </div>
            );
          })
        )}
        {windows.some((w) => w.window_kind === 'none') && (
          <div className="rounded-lg border border-accent-200 bg-accent-50 p-3 text-xs text-accent-500 dark:border-accent-800 dark:bg-accent-950/70 dark:text-accent-400">
            另有 {windows.filter((w) => w.window_kind === 'none').length} 个无窗口限制（none）的配额项，不计入进度。
          </div>
        )}
      </CardContent>
    </Card>
  );
}
