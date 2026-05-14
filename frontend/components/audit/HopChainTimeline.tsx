import { cn } from '@/lib/utils';
import type { HopAttestation, HopName } from '@/lib/audit-api';

const HOPS: Array<{ key: HopName; label: string; desc: string }> = [
  { key: 'ingress', label: 'Ingress', desc: '入口' },
  { key: 'router', label: 'Router', desc: '路由' },
  { key: 'pool', label: 'Pool', desc: '账号池' },
  { key: 'account', label: 'Account', desc: '账号' },
  { key: 'provider', label: 'Provider', desc: '上游' },
  { key: 'response', label: 'Response', desc: '响应' },
];

function formatTime(value?: string) {
  if (!value) return '未记录';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleTimeString('zh-CN', { hour12: false });
}

function statusOf(hop?: HopAttestation) {
  if (!hop) return { label: '缺失', className: 'bg-red-500' };
  const detailStatus = detailText(hop.detail, 'status');
  if (detailStatus && detailStatus !== 'ok') return { label: detailStatus, className: 'bg-red-500' };
  if ((hop.duration_ms ?? 0) > 1000) return { label: '慢', className: 'bg-amber-500' };
  return { label: '完成', className: 'bg-emerald-500' };
}

function detailText(detail: unknown, key: string) {
  if (typeof detail !== 'object' || detail === null) return '';
  const value = (detail as Record<string, unknown>)[key];
  return typeof value === 'string' || typeof value === 'number' ? String(value) : '';
}

function hopMeta(hop?: HopAttestation) {
  if (!hop) return '等待 proof';
  return hop.provider || hop.pool_id || hop.route_id || hop.account_id_hash || detailText(hop.detail, 'selector') || hop.endpoint || '已记录';
}

export function HopChainTimeline({ hops }: { hops: HopAttestation[] }) {
  return (
    <section className="rounded-lg border border-accent-200 bg-white p-5 shadow-card dark:border-accent-800 dark:bg-accent-900/70" aria-label="六跳信任链">
      <div>
        <h2 className="text-base font-semibold tracking-normal text-accent-950 dark:text-white">HopChain 六跳链路</h2>
        <p className="mt-1 text-sm text-accent-500 dark:text-accent-400">ingress → router → pool → account → provider → response</p>
      </div>

      <div className="mt-5 overflow-x-auto pb-1">
        <ol className="grid min-w-[980px] grid-cols-6 gap-3">
          {HOPS.map((item, index) => {
            const hop = hops.find((entry) => entry.hop === item.key);
            const status = statusOf(hop);
            return (
              <li key={item.key} className="relative rounded-lg border border-accent-200 bg-accent-50 p-3 dark:border-accent-800 dark:bg-accent-950/70">
                {index < HOPS.length - 1 && (
                  <span className="absolute left-[calc(100%-0.35rem)] top-6 h-px w-4 bg-accent-300 dark:bg-accent-700" aria-hidden="true" />
                )}
                <div className="flex items-center gap-2">
                  <span className={cn('size-2.5 shrink-0 rounded-full', status.className)} aria-hidden="true" />
                  <span className="truncate text-sm font-semibold text-accent-950 dark:text-accent-50">{item.label}</span>
                </div>
                <div className="mt-1 text-xs text-accent-500 dark:text-accent-400">{item.desc} · {status.label}</div>
                <dl className="mt-3 space-y-2 text-xs">
                  <div>
                    <dt className="text-accent-400 dark:text-accent-500">ts</dt>
                    <dd className="mt-0.5 font-mono text-accent-800 dark:text-accent-200">{formatTime(hop?.ts)}</dd>
                  </div>
                  <div>
                    <dt className="text-accent-400 dark:text-accent-500">duration</dt>
                    <dd className="mt-0.5 font-mono text-accent-800 dark:text-accent-200">{hop?.duration_ms ?? 0}ms</dd>
                  </div>
                  <div>
                    <dt className="text-accent-400 dark:text-accent-500">status</dt>
                    <dd className="mt-0.5 min-h-8 break-all text-accent-800 dark:text-accent-200">{hopMeta(hop)}</dd>
                  </div>
                </dl>
              </li>
            );
          })}
        </ol>
      </div>
    </section>
  );
}
