import { AlertTriangle, CheckCircle2 } from 'lucide-react';
import { cn } from '@/lib/utils';
import type { ModelChain } from '@/lib/audit-api';

const COLUMNS = [
  { key: 'requested', label: 'Requested', caption: '用户请求模型' },
  { key: 'route_decided', label: 'RouteDecided', caption: '路由决策模型' },
  { key: 'upstream_reported', label: 'UpstreamReported', caption: '上游回报模型' },
] as const;

function isConsistent(model?: ModelChain | null) {
  if (!model?.requested || !model.route_decided) return false;
  if (model.requested !== model.route_decided) return false;
  return !model.upstream_reported || model.upstream_reported === model.requested;
}

export function ModelChainCard({ model }: { model?: ModelChain | null }) {
  const consistent = isConsistent(model);

  return (
    <section className="rounded-lg border border-accent-200 bg-white p-5 shadow-card dark:border-accent-800 dark:bg-accent-900/70" aria-label="模型三方比对">
      <div className="flex flex-col justify-between gap-3 sm:flex-row sm:items-center">
        <div>
          <h2 className="text-base font-semibold tracking-normal text-accent-950 dark:text-white">ModelChain 三方比对</h2>
          <p className="mt-1 text-sm text-accent-500 dark:text-accent-400">请求、路由与上游回报必须一致。</p>
        </div>
        <div
          className={cn(
            'inline-flex min-h-8 items-center gap-2 rounded-lg border px-3 text-sm font-medium',
            consistent
              ? 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/70 dark:bg-emerald-950/30 dark:text-emerald-300'
              : 'border-red-200 bg-red-50 text-red-700 dark:border-red-900/70 dark:bg-red-950/30 dark:text-red-300'
          )}
        >
          {consistent ? <CheckCircle2 className="size-4" /> : <AlertTriangle className="size-4" />}
          <span>{consistent ? '一致' : '不一致'}</span>
        </div>
      </div>

      <div className="mt-4 grid gap-3 md:grid-cols-3">
        {COLUMNS.map((item) => {
          const value = model?.[item.key] || '未记录';
          const mismatch = Boolean(model?.requested && value !== '未记录' && value !== model.requested);
          return (
            <div
              key={item.key}
              className={cn(
                'min-w-0 rounded-lg border bg-accent-50 p-4 dark:bg-accent-950/70',
                mismatch ? 'border-red-300 dark:border-red-900/70' : 'border-accent-200 dark:border-accent-800'
              )}
            >
              <div className="text-xs font-medium text-accent-500 dark:text-accent-400">{item.label}</div>
              <div className="mt-2 break-all font-mono text-sm font-semibold text-accent-950 dark:text-accent-50">{value}</div>
              <div className="mt-2 text-xs text-accent-500 dark:text-accent-400">{item.caption}</div>
            </div>
          );
        })}
      </div>

      {!consistent && (
        <div className="mt-4 rounded-lg border border-red-300 bg-red-50 p-3 text-sm text-red-700 dark:border-red-900/70 dark:bg-red-950/30 dark:text-red-300">
          模型链存在差异，用户请求、HUAKAI 路由决策或上游回报至少一处不一致。
        </div>
      )}
    </section>
  );
}
