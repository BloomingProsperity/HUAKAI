'use client';

import { Suspense, useEffect, useState } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { KeyRound, Loader2, LogIn, Mail, ShieldCheck, UserPlus } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { login, register, isTwoFactor, verifyLoginTwoFactor } from '@/lib/api/auth';
import { sanitizeOtp, isOtpComplete, OTP_LENGTH } from '@/lib/api/otp';
import { fetchSiteConfig, DEFAULT_TENANT_ID } from '@/lib/api/siteConfig';
import { friendlyMessage } from '@/lib/api/errors';
import type { SessionUser } from '@/lib/auth/session';
import { cn } from '@/lib/utils';

type Mode = 'login' | 'register';

function LoginInner() {
  const router = useRouter();
  const params = useSearchParams();
  const next = params.get('next') || '/dashboard';

  const [mode, setMode] = useState<Mode>('login');
  const [tenantId, setTenantId] = useState<number>(DEFAULT_TENANT_ID);
  const [email, setEmail] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  // 2FA 挑战态:登录命中 2FA 后转入此步(challengeId + 第一步带回的 user,/login/2fa 只返 session)。
  const [twoFactor, setTwoFactor] = useState<{ challengeId: string; user: SessionUser; email: string } | null>(null);
  const [code, setCode] = useState('');

  useEffect(() => {
    void fetchSiteConfig().then((c) => setTenantId(c.tenant_id));
  }, []);

  async function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setNotice(null);
    setLoading(true);
    try {
      if (mode === 'login') {
        const r = await login({ tenant_id: tenantId, email: email.trim(), password });
        if (isTwoFactor(r)) {
          // 转入 2FA 验证码步骤;user 取自 202 响应,留待验证成功后随 session 一并存储。
          setTwoFactor({ challengeId: r.challenge_id, user: r.user, email: r.user.email });
          setCode('');
          return;
        }
        router.push(next);
      } else {
        const r = await register({ tenant_id: tenantId, email: email.trim(), display_name: displayName.trim() || email.trim(), password });
        if (r.verification_required) {
          setNotice('注册成功，请前往邮箱完成验证后再登录。');
          setMode('login');
        } else {
          // 后端若已直接放行，则尝试自动登录。
          const lr = await login({ tenant_id: tenantId, email: email.trim(), password });
          if (!isTwoFactor(lr)) router.push(next);
        }
      }
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setLoading(false);
    }
  }

  // 提交 2FA 验证码:满 6 位才提交,成功跳转,失败清空便于重输(吸收 sub2api 的失败清空体感)。
  async function submit2fa(rawCode: string) {
    if (!twoFactor) return;
    const c = sanitizeOtp(rawCode);
    if (!isOtpComplete(c)) return;
    setError(null);
    setLoading(true);
    try {
      await verifyLoginTwoFactor({ challenge_id: twoFactor.challengeId, code: c, user: twoFactor.user });
      router.push(next);
    } catch (err) {
      setError(friendlyMessage(err));
      setCode('');
    } finally {
      setLoading(false);
    }
  }

  // 输入即清洗(只留数字、截断 6 位);满位自动提交(吸收 sub2api 的 one-time-code 自动填充+满位自动提交)。
  function onOtpChange(raw: string) {
    const c = sanitizeOtp(raw);
    setCode(c);
    if (isOtpComplete(c)) void submit2fa(c);
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-accent-50 px-4 dark:bg-accent-950">
      <div className="w-full max-w-md">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <span className="flex size-14 items-center justify-center rounded-2xl bg-primary-500 text-lg font-bold text-white shadow-glow">
            HK
          </span>
          <div>
            <h1 className="text-2xl font-bold text-accent-950 dark:text-white">HUAKAI 控制台</h1>
            <p className="mt-1 text-sm text-accent-500 dark:text-accent-400">
              {mode === 'login' ? '登录以管理你的 API Key、用量与额度' : '创建账号,开始接入 HUAKAI 网关'}
            </p>
          </div>
        </div>

        <div className="rounded-xl border border-accent-200 bg-white p-6 shadow-card dark:border-accent-800 dark:bg-accent-900">
          {twoFactor && (
            <TwoFactorStep
              email={twoFactor.email}
              code={code}
              onChange={onOtpChange}
              onSubmit={() => void submit2fa(code)}
              onBack={() => { setTwoFactor(null); setCode(''); setError(null); }}
              loading={loading}
              error={error}
            />
          )}
          {!twoFactor && (
          <>
          {/* 模式切换 */}
          <div className="mb-6 grid grid-cols-2 gap-1 rounded-lg bg-accent-100 p-1 dark:bg-accent-800">
            {(['login', 'register'] as Mode[]).map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => { setMode(m); setError(null); setNotice(null); }}
                className={cn(
                  'flex items-center justify-center gap-1.5 rounded-md py-2 text-sm font-medium transition-colors',
                  mode === m
                    ? 'bg-white text-primary-700 shadow-sm dark:bg-accent-950 dark:text-primary-300'
                    : 'text-accent-500 hover:text-accent-700 dark:text-accent-400 dark:hover:text-accent-200'
                )}
              >
                {m === 'login' ? <LogIn className="size-4" /> : <UserPlus className="size-4" />}
                {m === 'login' ? '登录' : '注册'}
              </button>
            ))}
          </div>

          <form onSubmit={onSubmit} className="flex flex-col gap-4">
            {mode === 'register' && (
              <Field label="昵称" icon={<UserRoundIcon />}>
                <input
                  className={inputClass}
                  value={displayName}
                  onChange={(e) => setDisplayName(e.target.value)}
                  placeholder="如何称呼你(可选)"
                  autoComplete="nickname"
                />
              </Field>
            )}
            <Field label="邮箱" icon={<Mail className="size-4 text-accent-400" />}>
              <input
                className={inputClass}
                type="email"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="you@example.com"
                autoComplete="email"
              />
            </Field>
            <Field label="密码" icon={<KeyRound className="size-4 text-accent-400" />}>
              <input
                className={inputClass}
                type="password"
                required
                minLength={6}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={mode === 'register' ? '至少 6 位' : '••••••••'}
                autoComplete={mode === 'login' ? 'current-password' : 'new-password'}
              />
            </Field>

            {error && (
              <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
                {error}
              </div>
            )}
            {notice && (
              <div className="rounded-lg border border-primary-200 bg-primary-50 px-3 py-2 text-sm text-primary-700 dark:border-primary-900/60 dark:bg-primary-950/40 dark:text-primary-300">
                {notice}
              </div>
            )}

            <Button type="submit" disabled={loading} className="mt-1 w-full">
              {loading ? <Loader2 className="size-4 animate-spin" /> : mode === 'login' ? <LogIn className="size-4" /> : <UserPlus className="size-4" />}
              {mode === 'login' ? '登录' : '创建账号'}
            </Button>
          </form>
          </>
          )}

          <div className="mt-5 flex items-center justify-center gap-1.5 text-xs text-accent-400 dark:text-accent-500">
            <ShieldCheck className="size-3.5" />
            <span>租户 #{tenantId} · 会话令牌受 HUAKAI 鉴权中间件保护</span>
          </div>
        </div>
      </div>
    </div>
  );
}

const inputClass =
  'w-full rounded-lg border border-accent-200 bg-white px-3 py-2.5 text-sm text-accent-900 outline-none transition-colors placeholder:text-accent-400 focus:border-primary-400 focus:ring-2 focus:ring-primary-100 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-100 dark:focus:ring-primary-900/40';

function Field({ label, icon, children }: { label: string; icon: React.ReactNode; children: React.ReactNode }) {
  return (
    <label className="flex flex-col gap-1.5">
      <span className="flex items-center gap-1.5 text-xs font-medium text-accent-600 dark:text-accent-300">
        {icon}
        {label}
      </span>
      {children}
    </label>
  );
}

function UserRoundIcon() {
  return <UserPlus className="size-4 text-accent-400" />;
}

// 登录 2FA 验证码步骤。单个 one-time-code 输入框(吃密码管理器/短信自动填充)+ 满位自动提交;
// 6 格视觉分隔留作设计 follow-up(本切片只接线测功能,不追排版)。
function TwoFactorStep({
  email,
  code,
  onChange,
  onSubmit,
  onBack,
  loading,
  error,
}: {
  email: string;
  code: string;
  onChange: (raw: string) => void;
  onSubmit: () => void;
  onBack: () => void;
  loading: boolean;
  error: string | null;
}) {
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col items-center gap-1.5 text-center">
        <ShieldCheck className="size-8 text-primary-500" />
        <h2 className="text-base font-semibold text-accent-950 dark:text-white">两步验证</h2>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          输入验证器 App 上的 6 位动态码
          <br />
          账号 {email}
        </p>
      </div>
      <input
        className={cn(inputClass, 'text-center font-mono text-lg tracking-[0.4em]')}
        value={code}
        onChange={(e) => onChange(e.target.value)}
        inputMode="numeric"
        autoComplete="one-time-code"
        autoFocus
        maxLength={OTP_LENGTH}
        placeholder="••••••"
        aria-label="两步验证码"
      />
      {error && (
        <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
          {error}
        </div>
      )}
      <Button onClick={onSubmit} disabled={loading || !isOtpComplete(code)} className="w-full">
        {loading ? <Loader2 className="size-4 animate-spin" /> : <ShieldCheck className="size-4" />}
        验证
      </Button>
      <button
        type="button"
        onClick={onBack}
        className="text-center text-xs text-accent-400 transition-colors hover:text-accent-600 dark:hover:text-accent-300"
      >
        返回登录
      </button>
    </div>
  );
}

export default function LoginPage() {
  return (
    <Suspense fallback={<div className="flex min-h-screen items-center justify-center text-accent-400">加载中…</div>}>
      <LoginInner />
    </Suspense>
  );
}
