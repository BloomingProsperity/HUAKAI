'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import {
  Activity,
  Ban,
  BarChart3,
  Bell,
  Bot,
  ChevronLeft,
  CornerUpLeft,
  Database,
  FileCheck2,
  GaugeCircle,
  KeyRound,
  LayoutDashboard,
  Link2,
  MessageSquare,
  Package,
  Percent,
  Settings,
  ShieldAlert,
  ShieldCheck,
  Smartphone,
  Tags,
  Terminal,
  Ticket,
  UserCircle,
  Wallet,
} from 'lucide-react';
import { cn } from '@/lib/utils';

interface SidebarProps {
  collapsed?: boolean;
  onToggle?: () => void;
}

const navItems = [
  { icon: LayoutDashboard, label: '概览', href: '/dashboard', active: false, disabled: false },
  { icon: MessageSquare, label: 'Playground', href: '/chat', active: false, disabled: false },
  { icon: Terminal, label: '调试台', href: '/console', active: false, disabled: false },
  { icon: KeyRound, label: 'API Keys', href: '/api-keys', active: false, disabled: false },
  { icon: BarChart3, label: '用量', href: '/usage', active: false, disabled: false },
  { icon: UserCircle, label: '账户', href: '/account', active: false, disabled: false },
  { icon: ShieldCheck, label: '安全', href: '/security', active: false, disabled: false },
  { icon: Smartphone, label: '会话', href: '/sessions', active: false, disabled: false },
  { icon: Wallet, label: '充值', href: '/billing', active: false, disabled: false },
  { icon: Ticket, label: '兑换', href: '/redeem', active: false, disabled: false },
  { icon: Package, label: '订阅', href: '/subscriptions', active: false, disabled: false },
  { icon: Bell, label: '通知', href: '/notifications', active: false, disabled: false },
  { icon: FileCheck2, label: '审计', href: '/audit', active: false, disabled: false },
  { icon: Tags, label: '定价', href: '/pricing', active: false, disabled: false },
  { icon: Settings, label: '管理后台', href: '/admin/ops', active: false, disabled: false },
];

// admin 控制台导航树（在 /admin/* 路由下展示）。
const adminNavItems = [
  { icon: CornerUpLeft, label: '返回用户端', href: '/dashboard', active: false, disabled: false },
  { icon: GaugeCircle, label: '运营总览', href: '/admin/ops', active: false, disabled: false },
  { icon: UserCircle, label: '用户管理', href: '/admin/users', active: false, disabled: false },
  { icon: Database, label: '账号池', href: '/admin/accounts', active: false, disabled: false },
  { icon: Activity, label: '渠道健康', href: '/admin/channels', active: false, disabled: false },
  { icon: Link2, label: '模型绑定', href: '/admin/models/bindings', active: false, disabled: false },
  { icon: Percent, label: '定价倍率', href: '/admin/pricing', active: false, disabled: false },
  { icon: KeyRound, label: '凭证代理', href: '/admin/credentials', active: false, disabled: false },
  { icon: Ticket, label: '运营管理', href: '/admin/operations', active: false, disabled: false },
  { icon: Settings, label: '平台设置', href: '/admin/settings', active: false, disabled: false },
  { icon: ShieldAlert, label: '审核系统', href: '/admin/system', active: false, disabled: false },
  { icon: Ban, label: '审核黑名单', href: '/admin/moderation', active: false, disabled: false },
  { icon: Bot, label: 'Hermes 助手', href: '/admin/hermes', active: false, disabled: false },
];

const Sidebar = (_props: SidebarProps) => {
  const pathname = usePathname();
  const collapsed = _props.collapsed ?? false;
  const onToggle = _props.onToggle;
  const isAdmin = pathname === '/admin' || pathname.startsWith('/admin/');
  const items = isAdmin ? adminNavItems : navItems;

  return (
    <aside
      className={cn(
        'fixed inset-y-0 left-0 z-20 hidden flex-col border-r border-accent-200 bg-white shadow-card transition-[width] duration-300 dark:border-accent-800 dark:bg-accent-950 lg:flex',
        collapsed ? 'w-[72px]' : 'w-64'
      )}
    >
      <div className="flex h-16 items-center justify-between border-b border-accent-200 px-3 dark:border-accent-800">
        <Link href={isAdmin ? '/admin/ops' : '/dashboard'} className="flex min-w-0 items-center gap-3" aria-label="HUAKAI 控制台总览">
          <span className={cn('flex size-10 shrink-0 items-center justify-center rounded-lg text-sm font-bold text-white shadow-glow', isAdmin ? 'bg-accent-800 dark:bg-accent-700' : 'bg-primary-500')}>
            HK
          </span>
          <span className={cn('min-w-0', collapsed && 'hidden')}>
            <span className="block text-base font-bold tracking-normal text-accent-950 dark:text-white">HUAKAI</span>
            <span className="block truncate text-xs text-accent-500 dark:text-accent-400">{isAdmin ? '管理控制台' : '控制台'}</span>
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
      <nav className="flex-1 p-3" aria-label={isAdmin ? '管理导航' : '主导航'}>
        <ul className="flex flex-col gap-1">
          {items.map((item) => {
            const isActive = item.active || pathname === item.href || pathname.startsWith(`${item.href}/`);
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
