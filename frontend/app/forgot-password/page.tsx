'use client';

// 忘记密码-请求页。输入邮箱 → POST /v1/auth/reset-password {email}(请求模式)。
// 后端枚举安全:无论邮箱是否注册都返成功,故前端只显示统一文案,不暴露邮箱是否存在。
import { Suspense, useEffect, useState } from 'react';
import Link from 'next/link';
import { CheckCircle2, Loader2, Mail } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { requestPasswordReset } from '@/lib/api/auth';
import { fetchSiteConfig, DEFAULT_TENANT_ID } from '@/lib/api/siteConfig';
import { friendlyMessage } from '@/lib/api/errors';

function ForgotInner() {
  const [tenantId, setTenantId] = useState<number>(DEFAULT_TENANT_ID);
  const [email, setEmail] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sent, setSent] = useState(false);

  useEffect(() => {
    void fetchSiteConfig().then((c) => setTenantId(c.tenant_id ?? DEFAULT_TENANT_ID));
  }, []);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      await requestPasswordReset(tenantId, email.trim());
      // 枚举安全:成功与否都显示同样文案。
      setSent(true);
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-accent-50 px-4 dark:bg-accent-950">
      <div className="w-full max-w-md rounded-xl border border-accent-200 bg-white p-8 shadow-card dark:border-accent-800 dark:bg-accent-900">
        {sent ? (
          <div className="flex flex-col items-center gap-4 text-center">
            <CheckCircle2 className="size-9 text-emerald-500" />
            <div>
              <h1 className="text-base font-semibold text-accent-950 dark:text-white">请查收邮件</h1>
              <p className="mt-1.5 text-sm text-accent-500 dark:text-accent-400">
                若该邮箱已注册,我们已发送密码重置链接。请按邮件指引设置新密码。
              </p>
            </div>
            <Link href="/login" className="w-full">
              <Button variant="outline" className="w-full">返回登录</Button>
            </Link>
          </div>
        ) : (
          <form onSubmit={onSubmit} className="flex flex-col gap-4">
            <div className="flex flex-col items-center gap-1.5 text-center">
              <Mail className="size-7 text-primary-500" />
              <h1 className="text-base font-semibold text-accent-950 dark:text-white">找回密码</h1>
              <p className="text-sm text-accent-500 dark:text-accent-400">输入账号邮箱,我们将发送重置链接。</p>
            </div>
            <input
              className="w-full rounded-lg border border-accent-200 bg-white px-3 py-2.5 text-sm text-accent-900 outline-none transition-colors placeholder:text-accent-400 focus:border-primary-400 focus:ring-2 focus:ring-primary-100 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-100 dark:focus:ring-primary-900/40"
              type="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="you@example.com"
              autoComplete="email"
            />
            {error && (
              <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
                {error}
              </div>
            )}
            <Button type="submit" disabled={loading} className="w-full">
              {loading ? <Loader2 className="size-4 animate-spin" /> : <Mail className="size-4" />}
              发送重置链接
            </Button>
            <Link href="/login" className="text-center text-xs text-accent-400 hover:text-accent-600 dark:hover:text-accent-300">
              返回登录
            </Link>
          </form>
        )}
      </div>
    </div>
  );
}

export default function ForgotPasswordPage() {
  return (
    <Suspense fallback={<div className="flex min-h-screen items-center justify-center text-accent-400">加载中…</div>}>
      <ForgotInner />
    </Suspense>
  );
}
