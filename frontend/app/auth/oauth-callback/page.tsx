'use client';

// 社交/OAuth 登录回调页。第三方授权后回跳到这里(带 code+state);本页换会话后跳转。
// provider 来自登录页发起时暂存的 sessionStorage(第三方回跳不带 provider),兼容回调 URL 里也带的情况。
// 三态:处理中(转圈)/ 成功(replace 到登录前目的地)/ 失败(显错 + 返回登录)。
import { Suspense, useEffect, useRef, useState } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { AlertCircle, Loader2, ShieldCheck } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { completeOAuth } from '@/lib/api/auth';
import { fetchSiteConfig, DEFAULT_TENANT_ID } from '@/lib/api/siteConfig';
import { friendlyMessage } from '@/lib/api/errors';

function CallbackInner() {
  const router = useRouter();
  const params = useSearchParams();
  const [error, setError] = useState<string | null>(null);
  // OAuth 授权码一次性:React 重渲染/StrictMode 双跑会拿同一个 code 二次换码必失败,故加单次执行守卫。
  const ran = useRef(false);

  useEffect(() => {
    if (ran.current) return;
    ran.current = true;
    const code = params.get('code') ?? '';
    const state = params.get('state') ?? '';
    const provider = params.get('provider') ?? sessionStorage.getItem('huakai_oauth_provider') ?? '';
    if (!provider || !code || !state) {
      setError('回调参数缺失(provider/code/state),请重新登录。');
      return;
    }
    void (async () => {
      try {
        const cfg = await fetchSiteConfig();
        await completeOAuth({ tenant_id: cfg.tenant_id ?? DEFAULT_TENANT_ID, provider, state, code });
        const next = sessionStorage.getItem('huakai_oauth_next') || '/dashboard';
        sessionStorage.removeItem('huakai_oauth_provider');
        sessionStorage.removeItem('huakai_oauth_next');
        router.replace(next);
      } catch (err) {
        setError(friendlyMessage(err));
      }
    })();
  }, [params, router]);

  return (
    <div className="flex min-h-screen items-center justify-center bg-accent-50 px-4 dark:bg-accent-950">
      <div className="w-full max-w-md rounded-xl border border-accent-200 bg-white p-8 text-center shadow-card dark:border-accent-800 dark:bg-accent-900">
        {error ? (
          <div className="flex flex-col items-center gap-4">
            <AlertCircle className="size-9 text-red-500" />
            <div>
              <h1 className="text-base font-semibold text-accent-950 dark:text-white">登录未完成</h1>
              <p className="mt-1.5 text-sm text-accent-500 dark:text-accent-400">{error}</p>
            </div>
            <Button onClick={() => router.replace('/login')} className="w-full">
              返回登录
            </Button>
          </div>
        ) : (
          <div className="flex flex-col items-center gap-3">
            <span className="relative flex size-12 items-center justify-center">
              <ShieldCheck className="size-7 text-primary-500" />
            </span>
            <Loader2 className="size-5 animate-spin text-primary-500" />
            <p className="text-sm text-accent-500 dark:text-accent-400">正在完成登录…</p>
          </div>
        )}
      </div>
    </div>
  );
}

export default function OAuthCallbackPage() {
  return (
    <Suspense fallback={<div className="flex min-h-screen items-center justify-center text-accent-400">加载中…</div>}>
      <CallbackInner />
    </Suspense>
  );
}
