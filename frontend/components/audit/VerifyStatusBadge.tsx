import { ShieldCheck, ShieldX, TriangleAlert } from 'lucide-react';
import { cn } from '@/lib/utils';
import type { VerifyStatus } from '@/lib/audit-api';

const STATUS_META = {
  verified: {
    label: '已验证',
    desc: '签名与链路 proof 通过',
    icon: ShieldCheck,
    className: 'border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-900/70 dark:bg-emerald-950/40 dark:text-emerald-300',
  },
  partial: {
    label: '部分可信',
    desc: 'proof 可读但未完成浏览器验签',
    icon: TriangleAlert,
    className: 'border-amber-300 bg-amber-50 text-amber-700 dark:border-amber-900/70 dark:bg-amber-950/40 dark:text-amber-300',
  },
  tampered: {
    label: '疑似篡改',
    desc: '字段、Merkle 或签名不匹配',
    icon: ShieldX,
    className: 'border-red-300 bg-red-50 text-red-700 dark:border-red-900/70 dark:bg-red-950/40 dark:text-red-300',
  },
} satisfies Record<VerifyStatus, {
  label: string;
  desc: string;
  icon: typeof ShieldCheck;
  className: string;
}>;

export function VerifyStatusBadge({ status }: { status: VerifyStatus }) {
  const meta = STATUS_META[status];
  const Icon = meta.icon;

  return (
    <span
      className={cn('inline-flex min-h-9 items-center gap-2 rounded-lg border px-3 text-sm font-semibold', meta.className)}
      title={meta.desc}
    >
      <Icon className="size-4 shrink-0" aria-hidden="true" />
      <span>{meta.label}</span>
    </span>
  );
}
