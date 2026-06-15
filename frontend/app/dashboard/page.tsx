'use client';

// 用户门户「概览」落地页（session 鉴权为主，趋势一路可选用 hk_ key）。
// 聚合余额 / 今日用量 / API Key 数 / 额度 / 近 7 天趋势 / 签到，给登录后第一屏一个总览 + 快捷入口。
//
// 路由: /dashboard（Sidebar「概览」）。数据层: lib/api/overview.ts（聚合复用 billing/account/apiKeys/usage）。
// 趋势图复用 app/usage/UsageTrendChart（自绘，吃 TrendPoint[]）；StatCard 复用 components/dashboard。
//
// CLEAN-ROOM（CLAUDE.md §11/§12）: 落地页「Stats 行 → 趋势图 → 快捷入口 + 签到」形态借鉴 sub2api
// 用户 DashboardView（Stats/Charts/RecentUsage/QuickActions 组装），仅功能/布局形态，未抄码。
// 字段/单位以 HUAKAI 后端 handler 为准（余额/签到为 cents，趋势/额度为 decimal USD 字符串）。

import { useCallback, useEffect, useMemo, useState } from 'react';
import Link from 'next/link';
import {
  AlertCircle,
  CalendarCheck,
  CheckCircle2,
  Coins,
  Gauge,
  KeyRound,
  Loader2,
  MessageSquare,
  RefreshCw,
  TrendingUp,
  Wallet,
} from 'lucide-react';
import { StatCard } from '@/components/dashboard/StatCard';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { ApiError } from '@/lib/api/client';
import { friendlyMessage } from '@/lib/api/errors';
import { doCheckin, formatCents, formatUsd, quotaPercent, windowKindLabel } from '@/lib/api/account';
import { fmtCents } from '@/lib/api/billing';
import { loadOverview, pickPrimaryQuota, type OverviewSnapshot } from '@/lib/api/overview';
import { UsageTrendChart } from '../usage/UsageTrendChart';

const PLACEHOLDER = '—';

function fmtNum(v: number): string {
  return v.toLocaleString('zh-CN');
}

function fmtUsdNum(v: number): string {
  return `$${v.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 4 })}`;
}

// 快捷入口卡：去 Playground / 建 Key / 充值。路由按 Sidebar 实际项。
const QUICK_ACTIONS = [
  {
    href: '/chat',
    icon: MessageSquare,
    title: '去 Playground',
    desc: '在线对话调试模型与参数',
    tone: 'primary' as const,
  },
  {
    href: '/api-keys',
    icon: KeyRound,
    title: '创建 API Key',
    desc: '生成 hk_ 密钥接入你的应用',
    tone: 'blue' as const,
  },
  {
    href: '/billing',
    icon: Wallet,
    title: '充值余额',
    desc: '扫码 / 淘宝下单，人工确认入账',
    tone: 'emerald' as const,
  },
];

const QUICK_TONE_RING: Record<'primary' | 'blue' | 'emerald', string> = {
  primary: 'bg-primary-50 text-primary-700 ring-primary-100 dark:bg-primary-950/50 dark:text-primary-300 dark:ring-primary-900/70',
  blue: 'bg-blue-50 text-blue-700 ring-blue-100 dark:bg-blue-950/40 dark:text-blue-300 dark:ring-blue-900/60',
  emerald: 'bg-emerald-50 text-emerald-700 ring-emerald-100 dark:bg-emerald-950/40 dark:text-emerald-300 dark:ring-emerald-900/60',
};

export default function DashboardPage() {
  const [snapshot, setSnapshot] = useState<OverviewSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // 签到独立状态：领取是写操作，结果（金额/新余额）单独提示，不刷掉整页。
  const [checkinBusy, setCheckinBusy] = useState(false);
  const [checkinNotice, setCheckinNotice] = useState<string | null>(null);
  const [checkinError, setCheckinError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setSnapshot(await loadOverview());
    } catch (err) {
      // loadOverview 内部各路已隔离，整体失败仅在极端情况（如 Promise 编排异常）才走这里。
      setError(friendlyMessage(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const handleCheckin = useCallback(async () => {
    setCheckinBusy(true);
    setCheckinError(null);
    setCheckinNotice(null);
    try {
      const res = await doCheckin();
      setCheckinNotice(`签到成功，获得 ${formatCents(res.reward_cents)}，当前余额 ${formatCents(res.new_balance)}。`);
      await refresh();
    } catch (err) {
      // 已签到（409）/ 未开启（404）由后端区分，friendlyMessage 兜底成中文。
      if (err instanceof ApiError && err.code === 'daily_checkin_already_claimed') {
        setCheckinError('今天已经签到过了，明天再来。');
      } else {
        setCheckinError(friendlyMessage(err));
      }
    } finally {
      setCheckinBusy(false);
    }
  }, [refresh]);

  if (loading && !snapshot) {
    return (
      <div className="flex min-h-[60vh] items-center justify-center text-sm text-accent-400">
        <Loader2 className="mr-2 size-4 animate-spin" /> 加载概览中…
      </div>
    );
  }

  if (!snapshot) {
    return (
      <div className="mx-auto flex max-w-6xl flex-col items-center gap-4 py-16 text-center">
        <AlertCircle className="size-8 text-red-500" />
        <p className="text-sm text-accent-600 dark:text-accent-300">{error ?? '概览加载失败'}</p>
        <Button onClick={() => void refresh()} size="sm" variant="outline">
          <RefreshCw className="size-4" /> 重试
        </Button>
      </div>
    );
  }

  return (
    <OverviewContent
      busy={loading}
      checkinBusy={checkinBusy}
      checkinError={checkinError}
      checkinNotice={checkinNotice}
      onCheckin={handleCheckin}
      onRefresh={refresh}
      snapshot={snapshot}
    />
  );
}

function OverviewContent({
  busy,
  checkinBusy,
  checkinError,
  checkinNotice,
  onCheckin,
  onRefresh,
  snapshot,
}: {
  busy: boolean;
  checkinBusy: boolean;
  checkinError: string | null;
  checkinNotice: string | null;
  onCheckin: () => void;
  onRefresh: () => void;
  snapshot: OverviewSnapshot;
}) {
  const { balance, quota, apiKeys, checkin, trend } = snapshot;

  const primaryQuota = useMemo(
    () => (quota.ok && quota.data ? pickPrimaryQuota(quota.data) : null),
    [quota],
  );

  const statCards = useMemo(() => {
    const balanceValue = balance.ok && balance.data ? fmtCents(balance.data.amount_cents) : PLACEHOLDER;
    const todayCost = trend.ok && trend.data ? fmtUsdNum(trend.data.totals.today_cost) : PLACEHOLDER;
    const keyValue = apiKeys.ok && apiKeys.data ? fmtNum(apiKeys.data.total) : PLACEHOLDER;
    const keyDetail = apiKeys.ok && apiKeys.data ? `${apiKeys.data.active} 个启用中` : '未取到 Key 数';
    const quotaValue = primaryQuota
      ? Number.parseFloat(primaryQuota.cap) > 0
        ? formatUsd(primaryQuota.remaining)
        : '不限'
      : PLACEHOLDER;
    const quotaDetail = primaryQuota
      ? `${windowKindLabel(primaryQuota.window_kind)} · 已用 ${formatUsd(primaryQuota.consumed)}`
      : quota.ok
        ? '未配置额度窗口'
        : '未取到额度';

    return [
      {
        title: '账户余额',
        value: balanceValue,
        icon: Coins,
        tone: 'emerald' as const,
        detail: balance.ok ? '可用于推理计费' : '余额未取到',
      },
      {
        title: '今日用量',
        value: todayCost,
        icon: TrendingUp,
        tone: 'primary' as const,
        detail: trend.hasKey ? (trend.ok ? '近 7 天窗口内当日花费' : '趋势未取到') : '填 API Key 后展示',
      },
      {
        title: 'API Key',
        value: keyValue,
        icon: KeyRound,
        tone: 'blue' as const,
        detail: keyDetail,
      },
      {
        title: '剩余额度',
        value: quotaValue,
        icon: Gauge,
        tone: 'amber' as const,
        detail: quotaDetail,
      },
    ];
  }, [apiKeys, balance, primaryQuota, quota.ok, trend]);

  const trendPoints = trend.ok && trend.data ? trend.data.points : [];

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-5">
      {/* 标题条 + 刷新 */}
      <div className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
        <div className="flex flex-col gap-1">
          <h1 className="text-xl font-bold text-accent-950 dark:text-white">概览</h1>
          <p className="text-sm text-accent-500 dark:text-accent-400">
            你的余额、用量、密钥与额度一览；趋势按 Playground / 用量页所用的 API Key 统计。
          </p>
        </div>
        <Button onClick={onRefresh} size="sm" variant="outline" disabled={busy}>
          <RefreshCw className={busy ? 'animate-spin' : ''} />
          刷新
        </Button>
      </div>

      {/* 顶部 StatCard 行 */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {statCards.map((s) => (
          <StatCard key={s.title} title={s.title} value={s.value} icon={s.icon} tone={s.tone} detail={s.detail} />
        ))}
      </div>

      {/* 趋势图（占主列）+ 签到卡（侧列） */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <div className="lg:col-span-2">
          {trend.hasKey ? (
            <UsageTrendChart data={trendPoints} isLoading={busy} />
          ) : (
            <Card className="flex h-full min-h-[280px] flex-col items-center justify-center gap-3 border-dashed border-accent-200 bg-accent-50 p-8 text-center dark:border-accent-800 dark:bg-accent-950/40">
              <TrendingUp className="size-7 text-accent-300 dark:text-accent-600" />
              <p className="text-sm text-accent-500 dark:text-accent-400">
                填入 API Key 后即可在此查看近 7 天用量趋势。
              </p>
              <Link href="/api-keys" className="text-sm font-medium text-primary-600 underline dark:text-primary-300">
                去创建 / 管理 API Key
              </Link>
            </Card>
          )}
        </div>

        <CheckinCard
          busy={checkinBusy}
          error={checkinError}
          notice={checkinNotice}
          onCheckin={onCheckin}
          section={checkin}
        />
      </div>

      {/* 额度细节进度条（主额度窗口） */}
      {primaryQuota && Number.parseFloat(primaryQuota.cap) > 0 && (
        <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
          <CardHeader className="p-5 pb-3">
            <CardTitle className="text-base font-semibold tracking-normal text-accent-950 dark:text-white">
              {windowKindLabel(primaryQuota.window_kind)}额度
            </CardTitle>
          </CardHeader>
          <CardContent className="p-5 pt-0">
            <div className="flex items-end justify-between text-sm">
              <span className="text-accent-500 dark:text-accent-400">
                已用 {formatUsd(primaryQuota.consumed)} / {formatUsd(primaryQuota.cap)}
              </span>
              <span className="font-mono tabular-nums text-accent-700 dark:text-accent-200">
                剩余 {formatUsd(primaryQuota.remaining)}
              </span>
            </div>
            <div className="mt-2 h-2.5 overflow-hidden rounded-full bg-accent-100 dark:bg-accent-800">
              <div
                className="h-full rounded-full bg-primary-500"
                style={{ width: `${quotaPercent(primaryQuota.consumed, primaryQuota.cap)}%` }}
              />
            </div>
          </CardContent>
        </Card>
      )}

      {/* 快捷入口 */}
      <div>
        <h2 className="mb-3 text-sm font-semibold text-accent-700 dark:text-accent-200">快捷入口</h2>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          {QUICK_ACTIONS.map((a) => (
            <Link
              key={a.href}
              href={a.href}
              className="group flex items-center gap-4 rounded-xl border border-accent-200 bg-white p-4 shadow-card transition-all duration-200 hover:-translate-y-0.5 hover:shadow-card-hover dark:border-accent-800 dark:bg-accent-900/70"
            >
              <span className={`flex size-10 shrink-0 items-center justify-center rounded-lg ring-1 ${QUICK_TONE_RING[a.tone]}`}>
                <a.icon className="size-5" />
              </span>
              <span className="min-w-0">
                <span className="block truncate text-sm font-semibold text-accent-900 dark:text-accent-100">{a.title}</span>
                <span className="block truncate text-xs text-accent-500 dark:text-accent-400">{a.desc}</span>
              </span>
            </Link>
          ))}
        </div>
      </div>
    </div>
  );
}

function CheckinCard({
  busy,
  error,
  notice,
  onCheckin,
  section,
}: {
  busy: boolean;
  error: string | null;
  notice: string | null;
  onCheckin: () => void;
  section: OverviewSnapshot['checkin'];
}) {
  const status = section.ok ? section.data : null;
  const enabled = status?.enabled ?? false;
  const checkedIn = status?.checked_in_today ?? false;

  return (
    <Card className="flex flex-col border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardHeader className="p-5 pb-3">
        <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
          <CalendarCheck className="size-4 text-primary-600 dark:text-primary-300" />
          每日签到
        </CardTitle>
      </CardHeader>
      <CardContent className="flex flex-1 flex-col gap-3 p-5 pt-0">
        {!section.ok ? (
          <p className="text-sm text-accent-400 dark:text-accent-500">签到状态未取到，可稍后刷新重试。</p>
        ) : !enabled ? (
          <p className="text-sm text-accent-400 dark:text-accent-500">当前未开启每日签到活动。</p>
        ) : (
          <>
            <p className="text-sm text-accent-500 dark:text-accent-400">
              每日签到可领取 {formatCents(status?.min_cents ?? 0)} – {formatCents(status?.max_cents ?? 0)} 随机奖励，直接入余额。
            </p>
            {checkedIn ? (
              <div className="flex items-center gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">
                <CheckCircle2 className="size-4 shrink-0" /> 今天已签到，明天再来。
              </div>
            ) : (
              <Button onClick={onCheckin} disabled={busy} className="w-full">
                {busy ? <Loader2 className="size-4 animate-spin" /> : <CalendarCheck className="size-4" />}
                立即签到
              </Button>
            )}
          </>
        )}

        {notice && (
          <div className="flex items-start gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2 text-xs text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">
            <CheckCircle2 className="mt-0.5 size-3.5 shrink-0" />
            <span>{notice}</span>
          </div>
        )}
        {error && (
          <div className="flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
            <AlertCircle className="mt-0.5 size-3.5 shrink-0" />
            <span>{error}</span>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
