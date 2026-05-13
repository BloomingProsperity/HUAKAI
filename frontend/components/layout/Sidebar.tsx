'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import {
  Activity,
  BarChart3,
  ChevronLeft,
  Database,
  KeyRound,
  LayoutDashboard,
  Settings,
  ShieldCheck,
} from 'lucide-react';
import { cn } from '@/lib/utils';

interface SidebarProps {
  collapsed?: boolean;
  onToggle?: () => void;
}

const navItems = [
  {
    icon: LayoutDashboard,
    label: '总览',
    href: '/dashboard',
    active: true,
    disabled: false,
  },
  {
    icon: Database,
    label: '账号池',
    href: '/accounts',
    active: false,
    disabled: true,
  },
  {
    icon: KeyRound,
    label: '密钥',
    href: '/api-keys',
    active: false,
    disabled: true,
  },
  {
    icon: BarChart3,
    label: '用量',
    href: '/usage',
    active: false,
    disabled: true,
  },
  {
    icon: Settings,
    label: '设置',
    href: '/settings',
    active: false,
    disabled: true,
  },
];

const Sidebar = (_props: SidebarProps) => {
  const pathname = usePathname();
  const collapsed = _props.collapsed ?? false;
  const onToggle = _props.onToggle;

  return (
    <aside
      className={cn(
        'fixed inset-y-0 left-0 z-20 hidden flex-col border-r border-accent-200 bg-white shadow-card transition-[width] duration-300 dark:border-accent-800 dark:bg-accent-950 lg:flex',
        collapsed ? 'w-[72px]' : 'w-64'
      )}
    >
      <div className="flex h-16 items-center justify-between border-b border-accent-200 px-3 dark:border-accent-800">
        <Link href="/dashboard" className="flex min-w-0 items-center gap-3" aria-label="HUAKAI 控制台总览">
          <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary-500 text-sm font-bold text-white shadow-glow">
            HK
          </span>
          <span className={cn('min-w-0', collapsed && 'hidden')}>
            <span className="block text-base font-bold tracking-normal text-accent-950 dark:text-white">HUAKAI</span>
            <span className="block truncate text-xs text-accent-500 dark:text-accent-400">控制台</span>
          </span>
        </Link>
        {onToggle && (
          <button
            aria-label={collapsed ? '展开侧边栏' : '折叠侧边栏'}
            className={cn(
              'flex size-8 items-center justify-center rounded-lg border border-accent-200 bg-white p-0 text-accent-500 hover:bg-accent-100 dark:border-accent-800 dark:bg-accent-900 dark:text-accent-300 dark:hover:bg-accent-800',
              collapsed && 'absolute -right-4 top-4 bg-white dark:bg-accent-900'
            )}
            onClick={onToggle}
            type="button"
          >
            <ChevronLeft className={cn('size-4 transition-transform duration-300', collapsed && 'rotate-180')} />
          </button>
        )}
      </div>
      <nav className="flex-1 p-3" aria-label="主导航">
        <ul className="flex flex-col gap-1">
          {navItems.map((item) => {
            const isActive = item.active || pathname === item.href;
            const itemClassName = cn(
              'flex min-h-11 w-full items-center gap-3 rounded-lg px-3 text-sm font-medium transition-colors',
              collapsed && 'justify-center px-0',
              isActive && 'bg-primary-50 text-primary-700 ring-1 ring-primary-100 dark:bg-accent-900 dark:text-primary-300 dark:ring-accent-800',
              !isActive && !item.disabled && 'text-accent-600 hover:bg-primary-50 hover:text-primary-700 dark:text-accent-400 dark:hover:bg-accent-900 dark:hover:text-primary-300',
              item.disabled && 'cursor-not-allowed text-accent-400 opacity-60 dark:text-accent-600'
            );

            return (
              <li key={item.href}>
                {item.disabled ? (
                  <span className={itemClassName} aria-disabled="true" title={collapsed ? item.label : undefined}>
                    <item.icon className="size-4 shrink-0" />
                    {!collapsed && <span className="truncate">{item.label}</span>}
                  </span>
                ) : (
                  <Link href={item.href} className={itemClassName} aria-current={isActive ? 'page' : undefined} title={collapsed ? item.label : undefined}>
                    <item.icon className="size-4 shrink-0" />
                    {!collapsed && <span className="truncate">{item.label}</span>}
                  </Link>
                )}
              </li>
            );
          })}
        </ul>
      </nav>
      <div className="border-t border-accent-200 p-3 dark:border-accent-800">
        <div className={cn('flex items-center gap-3 rounded-lg bg-accent-50 p-3 dark:bg-accent-900/70', collapsed && 'justify-center p-2')}>
          <ShieldCheck className="size-4 shrink-0 text-primary-600 dark:text-primary-300" />
          <div className={cn('min-w-0', collapsed && 'hidden')}>
            <div className="text-xs font-semibold text-accent-800 dark:text-accent-100">P1 总览</div>
            <div className="flex items-center gap-1.5 truncate text-[11px] text-accent-500 dark:text-accent-400">
              <Activity className="size-3" />
              <span>本地开发 · v0.1.0</span>
            </div>
          </div>
        </div>
      </div>
    </aside>
  );
};

export default Sidebar;
