'use client';

// 邮箱验证落地页。注册后邮件里的链接带 ?token=… 跳到这里;本页自动 POST /v1/auth/verify-email 完成验证。
// 三态:处理中 / 成功(去登录)/ 失败(令牌无效或过期)。tenant 取自 site config。
import { Suspense, useEffect, useRef, useState } from 'react';
import { useSearchParams } from 'next/navigation';
import Link from 'next/link';
import { AlertCircle, CheckCircle2, Loader2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { verifyEmail } from '@/lib/api/auth';
import { fetchSiteConfig, DEFAULT_TENANT_ID } from '@/lib/api/siteConfig';
import { friendlyMessage } from '@/lib/api/errors';

function VerifyInner() {
  const params = useSearchParams();
  const [state, setState] = useState<'pending' | 'ok' | 'error'>('pending');
  const [error, setError] = useState<string | null>(null);
  const ran = useRef(false);

  useEffect(() => {
    if (ran.current) return;
    ran.current = true;
    const token = params.get('token') ?? '';
    if (!token) {
      setError('验证链接缺少令牌,请使用邮件中的完整链接。');
      setState('error');
      return;
    }
    void (async () => {
      try {
        const cfg = await fetchSiteConfig();
        await verifyEmail(cfg.tenant_id ?? DEFAULT_TENANT_ID, token);
        setState('ok');
      } catch (err) {
        setError(friendlyMessage(err));
        setState('error');
      }
    })();
  }, [params]);

  return (
    <Shell>
      {state === 'pending' && (
        <div className="flex flex-col items-center gap-3">
          <Loader2 className="size-6 animate-spin text-primary-500" />
          <p className="text-sm text-accent-500 dark:text-accent-400">正在验证邮箱…</p>
        </div>
      )}
      {state === 'ok' && (
        <div className="flex flex-col items-center gap-4 text-center">
          <CheckCircle2 className="size-9 text-emerald-500" />
          <div>
            <h1 className="text-base font-semibold text-accent-950 dark:text-white">邮箱已验证</h1>
            <p className="mt-1.5 text-sm text-accent-500 dark:text-accent-400">现在可以登录你的账号了。</p>
          </div>
          <Link href="/login" className="w-full">
            <Button className="w-full">去登录</Button>
          </Link>
        </div>
      )}
      {state === 'error' && (
        <div className="flex flex-col items-center gap-4 text-center">
          <AlertCircle className="size-9 text-red-500" />
          <div>
            <h1 className="text-base font-semibold text-accent-950 dark:text-white">验证未完成</h1>
            <p className="mt-1.5 text-sm text-accent-500 dark:text-accent-400">{error}</p>
          </div>
          <Link href="/login" className="w-full">
            <Button variant="outline" className="w-full">返回登录</Button>
          </Link>
        </div>
      )}
    </Shell>
  );
}

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-accent-50 px-4 dark:bg-accent-950">
      <div className="w-full max-w-md rounded-xl border border-accent-200 bg-white p-8 shadow-card dark:border-accent-800 dark:bg-accent-900">
        {children}
      </div>
    </div>
  );
}

export default function VerifyEmailPage() {
  return (
    <Suspense fallback={<div className="flex min-h-screen items-center justify-center text-accent-400">加载中…</div>}>
      <VerifyInner />
    </Suspense>
  );
}
