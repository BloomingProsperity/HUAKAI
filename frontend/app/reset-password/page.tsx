'use client';

// 重置密码落地页。找回密码邮件里的链接带 ?token=… 跳到这里;输入新密码两次 → POST /v1/auth/reset-password
// (带 token = 完成模式)。成功后引导去登录。tenant 取自 site config。
import { Suspense, useEffect, useState } from 'react';
import { useSearchParams } from 'next/navigation';
import Link from 'next/link';
import { CheckCircle2, KeyRound, Loader2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { resetPassword } from '@/lib/api/auth';
import { validateNewPassword, MIN_PASSWORD_LEN } from '@/lib/api/password-reset';
import { fetchSiteConfig, DEFAULT_TENANT_ID } from '@/lib/api/siteConfig';
import { friendlyMessage } from '@/lib/api/errors';

function ResetInner() {
  const params = useSearchParams();
  const [tenantId, setTenantId] = useState<number>(DEFAULT_TENANT_ID);
  const [token, setToken] = useState('');
  const [pw, setPw] = useState('');
  const [confirm, setConfirm] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);

  useEffect(() => {
    setToken(params.get('token') ?? '');
    void fetchSiteConfig().then((c) => setTenantId(c.tenant_id ?? DEFAULT_TENANT_ID));
  }, [params]);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (!token) {
      setError('重置链接缺少令牌,请使用邮件中的完整链接。');
      return;
    }
    const invalid = validateNewPassword(pw, confirm);
    if (invalid) {
      setError(invalid);
      return;
    }
    setLoading(true);
    try {
      await resetPassword(tenantId, token, pw);
      setDone(true);
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-accent-50 px-4 dark:bg-accent-950">
      <div className="w-full max-w-md rounded-xl border border-accent-200 bg-white p-8 shadow-card dark:border-accent-800 dark:bg-accent-900">
        {done ? (
          <div className="flex flex-col items-center gap-4 text-center">
            <CheckCircle2 className="size-9 text-emerald-500" />
            <div>
              <h1 className="text-base font-semibold text-accent-950 dark:text-white">密码已重置</h1>
              <p className="mt-1.5 text-sm text-accent-500 dark:text-accent-400">请用新密码登录。</p>
            </div>
            <Link href="/login" className="w-full">
              <Button className="w-full">去登录</Button>
            </Link>
          </div>
        ) : (
          <form onSubmit={onSubmit} className="flex flex-col gap-4">
            <div className="flex flex-col items-center gap-1.5 text-center">
              <KeyRound className="size-7 text-primary-500" />
              <h1 className="text-base font-semibold text-accent-950 dark:text-white">设置新密码</h1>
              <p className="text-sm text-accent-500 dark:text-accent-400">至少 {MIN_PASSWORD_LEN} 位。</p>
            </div>
            <input
              className={inputClass}
              type="password"
              required
              minLength={MIN_PASSWORD_LEN}
              value={pw}
              onChange={(e) => setPw(e.target.value)}
              placeholder="新密码"
              autoComplete="new-password"
            />
            <input
              className={inputClass}
              type="password"
              required
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              placeholder="再次输入新密码"
              autoComplete="new-password"
            />
            {error && (
              <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
                {error}
              </div>
            )}
            <Button type="submit" disabled={loading} className="w-full">
              {loading ? <Loader2 className="size-4 animate-spin" /> : <KeyRound className="size-4" />}
              重置密码
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

const inputClass =
  'w-full rounded-lg border border-accent-200 bg-white px-3 py-2.5 text-sm text-accent-900 outline-none transition-colors placeholder:text-accent-400 focus:border-primary-400 focus:ring-2 focus:ring-primary-100 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-100 dark:focus:ring-primary-900/40';

export default function ResetPasswordPage() {
  return (
    <Suspense fallback={<div className="flex min-h-screen items-center justify-center text-accent-400">加载中…</div>}>
      <ResetInner />
    </Suspense>
  );
}
