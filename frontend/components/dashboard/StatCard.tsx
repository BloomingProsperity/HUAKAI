import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { cn } from '@/lib/utils';
import { LucideIcon } from 'lucide-react';

interface StatCardProps {
  title: string;
  value: string;
  icon: LucideIcon;
  description?: string;
  detail?: string;
  tone?: 'primary' | 'blue' | 'emerald' | 'amber' | 'red' | 'slate';
}

const toneClassName: Record<NonNullable<StatCardProps['tone']>, string> = {
  primary: 'bg-primary-50 text-primary-700 ring-primary-100 dark:bg-primary-950/50 dark:text-primary-300 dark:ring-primary-900/70',
  blue: 'bg-blue-50 text-blue-700 ring-blue-100 dark:bg-blue-950/40 dark:text-blue-300 dark:ring-blue-900/60',
  emerald: 'bg-emerald-50 text-emerald-700 ring-emerald-100 dark:bg-emerald-950/40 dark:text-emerald-300 dark:ring-emerald-900/60',
  amber: 'bg-amber-50 text-amber-700 ring-amber-100 dark:bg-amber-950/40 dark:text-amber-300 dark:ring-amber-900/60',
  red: 'bg-red-50 text-red-700 ring-red-100 dark:bg-red-950/40 dark:text-red-300 dark:ring-red-900/60',
  slate: 'bg-accent-100 text-accent-700 ring-accent-200 dark:bg-accent-900 dark:text-accent-300 dark:ring-accent-800',
};

export function StatCard({
  title,
  value,
  icon: Icon,
  description,
  detail,
  tone = 'primary',
}: StatCardProps) {
  return (
    <Card className="group border-accent-200 bg-white shadow-card transition-all duration-200 hover:-translate-y-0.5 hover:shadow-card-hover dark:border-accent-800 dark:bg-accent-900/70">
      <CardHeader className="flex flex-row items-start justify-between gap-3 p-4 pb-3">
        <div className="min-w-0">
          <CardTitle className="truncate text-sm font-medium text-accent-500 dark:text-accent-400">{title}</CardTitle>
          {description && (
            <p className="mt-1 truncate text-xs text-accent-400 dark:text-accent-500">{description}</p>
          )}
        </div>
        <div className={cn('flex size-9 shrink-0 items-center justify-center rounded-lg ring-1', toneClassName[tone])}>
          <Icon className="size-4" />
        </div>
      </CardHeader>
      <CardContent className="p-4 pt-0">
        <div className="text-2xl font-bold tracking-normal text-accent-950 dark:text-white">{value}</div>
        {detail && (
          <p className="mt-2 min-h-5 text-xs leading-5 text-accent-500 dark:text-accent-400">{detail}</p>
        )}
      </CardContent>
    </Card>
  );
}
