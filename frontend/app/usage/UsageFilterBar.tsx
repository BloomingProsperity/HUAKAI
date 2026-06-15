'use client';

import { CalendarRange, Download, Loader2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { cn } from '@/lib/utils';
import type { ExportFormat, UsageGranularity } from '@/lib/api/usage';

// 快捷区间预设（天）。来源行为：sub2api DateRangePicker 预设 + new-api TIME_RANGE_PRESETS（24h/7d/14d/30d）。
// 上限 31 受 time-series 后端窗口约束（window_too_large=400）。
export const RANGE_PRESETS: { days: number; label: string }[] = [
  { days: 1, label: '24 小时' },
  { days: 7, label: '7 天' },
  { days: 14, label: '14 天' },
  { days: 30, label: '30 天' },
];

const GRANULARITIES: { key: UsageGranularity; label: string }[] = [
  { key: 'day', label: '按天' },
  { key: 'week', label: '按周' },
  { key: 'month', label: '按月' },
];

interface UsageFilterBarProps {
  fromDate: string;
  toDate: string;
  granularity: UsageGranularity;
  presetDays: number | null;
  exporting: boolean;
  canExport: boolean;
  loading: boolean;
  onPreset: (days: number) => void;
  onFromChange: (v: string) => void;
  onToChange: (v: string) => void;
  onGranularity: (g: UsageGranularity) => void;
  onExport: (format: ExportFormat) => void;
}

export function UsageFilterBar({
  fromDate,
  toDate,
  granularity,
  presetDays,
  exporting,
  canExport,
  loading,
  onPreset,
  onFromChange,
  onToChange,
  onGranularity,
  onExport,
}: UsageFilterBarProps) {
  const dateInputCls =
    'rounded-lg border border-accent-200 bg-white px-2.5 py-1.5 text-sm text-accent-900 outline-none focus:border-primary-400 focus:ring-2 focus:ring-primary-100 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-100 dark:focus:ring-primary-900/40';

  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardContent className="flex flex-col gap-3 p-4">
        <div className="flex flex-wrap items-center gap-x-4 gap-y-3">
          {/* 快捷区间 */}
          <div className="flex items-center gap-2">
            <CalendarRange className="size-4 text-primary-600 dark:text-primary-300" />
            <div className="flex rounded-lg border border-accent-200 p-0.5 dark:border-accent-800">
              {RANGE_PRESETS.map((p) => (
                <button
                  key={p.days}
                  type="button"
                  onClick={() => onPreset(p.days)}
                  className={cn(
                    'rounded-md px-2.5 py-1 text-xs font-medium transition-colors',
                    presetDays === p.days
                      ? 'bg-primary-500 text-white shadow-sm'
                      : 'text-accent-500 hover:text-accent-800 dark:text-accent-400 dark:hover:text-accent-100',
                  )}
                >
                  {p.label}
                </button>
              ))}
            </div>
          </div>

          {/* 自定义日期范围 */}
          <div className="flex items-center gap-2 text-sm text-accent-500 dark:text-accent-400">
            <input
              type="date"
              value={fromDate}
              max={toDate}
              onChange={(e) => onFromChange(e.target.value)}
              className={dateInputCls}
              aria-label="起始日期"
            />
            <span className="text-accent-400">至</span>
            <input
              type="date"
              value={toDate}
              min={fromDate}
              onChange={(e) => onToChange(e.target.value)}
              className={dateInputCls}
              aria-label="结束日期"
            />
          </div>

          {/* 粒度切换 */}
          <div className="flex items-center gap-2">
            <span className="text-xs text-accent-400 dark:text-accent-500">粒度</span>
            <div className="flex rounded-lg border border-accent-200 p-0.5 dark:border-accent-800">
              {GRANULARITIES.map((g) => (
                <button
                  key={g.key}
                  type="button"
                  onClick={() => onGranularity(g.key)}
                  className={cn(
                    'rounded-md px-2.5 py-1 text-xs font-medium transition-colors',
                    granularity === g.key
                      ? 'bg-primary-500 text-white shadow-sm'
                      : 'text-accent-500 hover:text-accent-800 dark:text-accent-400 dark:hover:text-accent-100',
                  )}
                >
                  {g.label}
                </button>
              ))}
            </div>
          </div>

          {/* 导出（session 范围，与所选 Key 无关） */}
          <div className="ml-auto flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => onExport('csv')}
              disabled={!canExport || exporting || loading}
              title="按账户范围导出当前时间窗口（会话鉴权）"
            >
              {exporting ? <Loader2 className="size-4 animate-spin" /> : <Download className="size-4" />}
              导出 CSV
            </Button>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => onExport('json')}
              disabled={!canExport || exporting || loading}
            >
              JSON
            </Button>
          </div>
        </div>
        <p className="text-[11px] text-accent-400 dark:text-accent-500">
          时间窗口同时作用于趋势图、模型维度与明细表（最长 31 天）；导出按账户范围（非单个 Key）。
        </p>
      </CardContent>
    </Card>
  );
}
