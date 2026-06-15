'use client';

import { useEffect, useState } from 'react';
import { useRouter } from 'next/navigation';
import { Clock3, LogIn, LogOut, Moon } from 'lucide-react';
import { cn } from '@/lib/utils';
import { getStoredUser, type SessionUser } from '@/lib/auth/session';
import { fetchMe, logout } from '@/lib/api/auth';

interface HeaderProps {
  collapsed?: boolean;
}

type BackendState = 'checking' | 'online' | 'offline';

function formatNow(value: Date | null) {
  if (!value) return '同步中';
  return value.toLocaleString('zh-CN');
}

const Header = (_props: HeaderProps) => {
  const router = useRouter();
  const [now, setNow] = useState<Date | null>(null);
  const [backendState, setBackendState] = useState<BackendState>('checking');
  const [latency, setLatency] = useState<number | null>(null);
  const [user, setUser] = useState<SessionUser | null>(null);

  useEffect(() => {
    setUser(getStoredUser());
    // 后台静默刷新一次用户信息（带 session token；未登录则忽略）。
    void fetchMe().then(setUser).catch(() => { /* 未登录 */ });
  }, []);

  async function onLogout() {
    await logout();
    setUser(null);
    router.push('/login');
  }

  useEffect(() => {
    setNow(new Date());
    const timer = window.setInterval(() => setNow(new Date()), 1000);

    return () => {
      window.clearInterval(timer);
    };
  }, []);

  useEffect(() => {
    async function pingBackend() {
      const startedAt = performance.now();
      const controller = new AbortController();
      const abortTimer = window.setTimeout(() => controller.abort(), 3000);

      try {
        // /debug/vars 走 adminGate, 必须带 admin bearer 否则 401。
        // 之前没带 token, 即使后端正常也永远显示 offline。
        const adminToken =
          typeof window !== 'undefined'
            ? window.localStorage.getItem('huakai_admin_token') ?? ''
            : '';
        const headers: Record<string, string> = adminToken
          ? { Authorization: `Bearer ${adminToken}` }
          : {};
        const response = await fetch('/debug/vars', {
          cache: 'no-store',
          signal: controller.signal,
          headers,
        });
        if (!response.ok) throw new Error('backend not ready');
        setLatency(Math.round(performance.now() - startedAt));
        setBackendState('online');
      } catch {
        setLatency(null);
        setBackendState('offline');
      } finally {
        window.clearTimeout(abortTimer);
      }
    }

    void pingBackend();
    const timer = window.setInterval(() => {
      void pingBackend();
    }, 5000);

    return () => {
      window.clearInterval(timer);
    };
  }, []);

  const backendLabel = backendState === 'online' ? '已连接' : backendState === 'offline' ? '离线' : '检测中';

  return (
    <header className="sticky top-0 z-10 flex min-h-16 items-center justify-between gap-3 border-b border-accent-200 bg-white/90 px-4 backdrop-blur-xl dark:border-accent-800 dark:bg-accent-950/90 md:px-6 lg:px-8">
      <div className="flex min-w-0 items-center gap-2 rounded-lg border border-accent-200 bg-accent-50 px-3 py-2 text-xs text-accent-600 dark:border-accent-800 dark:bg-accent-900 dark:text-accent-300">
        <Clock3 className="size-3.5 shrink-0" />
        <span className="truncate font-mono tabular-nums">{formatNow(now)}</span>
      </div>

      <div
        className={cn(
          'hidden items-center gap-2 rounded-lg border px-3 py-2 text-xs font-medium sm:flex',
          backendState === 'online' && 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300',
          backendState === 'offline' && 'border-red-200 bg-red-50 text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300',
          backendState === 'checking' && 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900/60 dark:bg-amber-950/40 dark:text-amber-300'
        )}
      >
        <span
          className={cn(
            'size-2 rounded-full',
            backendState === 'online' && 'bg-emerald-500 shadow-[0_0_0_3px_rgba(16,185,129,0.18)]',
            backendState === 'offline' && 'bg-red-500 shadow-[0_0_0_3px_rgba(239,68,68,0.18)]',
            backendState === 'checking' && 'bg-amber-500 shadow-[0_0_0_3px_rgba(245,158,11,0.18)]'
          )}
        />
        <span>后端心跳</span>
        <span>{backendLabel}</span>
        <span className="font-mono tabular-nums">{latency === null ? '--' : `${latency}ms`}</span>
      </div>

      <div className="flex shrink-0 items-center gap-2">
        <button
          type="button"
          disabled
          className="flex size-9 items-center justify-center rounded-lg border border-accent-200 bg-accent-50 text-accent-500 dark:border-accent-800 dark:bg-accent-900 dark:text-accent-400"
          aria-label="主题切换占位"
        >
          <Moon className="size-4" />
        </button>
        {user ? (
          <div className="flex items-center gap-2 rounded-lg border border-accent-200 bg-accent-50 px-2.5 py-2 text-xs text-accent-600 dark:border-accent-800 dark:bg-accent-900 dark:text-accent-300">
            <span className="flex size-5 items-center justify-center rounded-full bg-primary-500 text-[10px] font-bold uppercase text-white">
              {(user.display_name || user.email || 'H').charAt(0)}
            </span>
            <span className="hidden max-w-[10rem] truncate sm:inline">{user.display_name || user.email}</span>
            <button
              type="button"
              onClick={onLogout}
              className="ml-0.5 flex items-center gap-1 rounded-md px-1.5 py-1 text-accent-500 hover:bg-red-50 hover:text-red-600 dark:text-accent-400 dark:hover:bg-red-950/40 dark:hover:text-red-300"
              aria-label="登出"
              title="登出"
            >
              <LogOut className="size-3.5" />
            </button>
          </div>
        ) : (
          <a
            href="/login"
            className="flex items-center gap-1.5 rounded-lg border border-primary-200 bg-primary-50 px-3 py-2 text-xs font-medium text-primary-700 hover:bg-primary-100 dark:border-primary-900/60 dark:bg-primary-950/40 dark:text-primary-300"
          >
            <LogIn className="size-3.5" />
            <span>登录</span>
          </a>
        )}
      </div>
    </header>
  );
};

export default Header;
