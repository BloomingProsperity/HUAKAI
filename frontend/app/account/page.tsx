'use client';

// 个人中心:可用分组 + 我的邀请码/邀请记录 + 每日签到 + 推荐奖励账本 + 配额概览。
// 全部端点走 session 鉴权 (lib/api/account.ts -> userClient)。分区卡片式,各区独立 loading/空/错误态。
// 借鉴 (功能/字段/布局形态,非抄码):
//   - sub2api groups.ts: 可用分组列表 + 公开/非公开倍率区分 (has_public_ratio)。
//   - new-api 个人中心: 签到领奖按钮 (今日是否已签 + 领取金额) + 邀请码复制 + 推荐奖励账本。
import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertCircle,
  CalendarCheck,
  CheckCircle2,
  Copy,
  Gauge,
  Gift,
  Layers,
  Loader2,
  RefreshCw,
  Sparkles,
  Ticket,
  Users,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table';
import { cn } from '@/lib/utils';
import { ApiError } from '@/lib/api/client';
import { friendlyMessage } from '@/lib/api/errors';
import {
  doCheckin,
  fetchCheckinStatus,
  fetchGroups,
  fetchInvitationSummary,
  fetchMyReferralCode,
  fetchQuota,
  fetchReferralRewards,
  fetchReferrals,
  formatCents,
  formatUsd,
  quotaPercent,
  referralStatusLabel,
  referralStatusTone,
  windowKindLabel,
  type CheckinStatus,
  type InvitationSummary,
  type MeGroup,
  type MeQuotaWindow,
  type MyReferralCode,
  type ReferralListResponse,
  type ReferralRewardsResponse,
  type StatusTone,
} from '@/lib/api/account';

const REFERRAL_PAGE = 20;

function toneRing(tone: StatusTone): string {
  switch (tone) {
    case 'emerald':
      return 'bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-950/40 dark:text-emerald-300 dark:ring-emerald-900/60';
    case 'blue':
      return 'bg-blue-50 text-blue-700 ring-blue-200 dark:bg-blue-950/40 dark:text-blue-300 dark:ring-blue-900/60';
    case 'amber':
      return 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-950/40 dark:text-amber-300 dark:ring-amber-900/60';
    case 'red':
      return 'bg-red-50 text-red-700 ring-red-200 dark:bg-red-950/40 dark:text-red-300 dark:ring-red-900/60';
    default:
      return 'bg-accent-100 text-accent-600 ring-accent-200 dark:bg-accent-800 dark:text-accent-300 dark:ring-accent-700';
  }
}

function fmtDateTime(iso: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN');
}

function SectionCard({
  icon: Icon,
  title,
  hint,
  action,
  children,
}: {
  icon: typeof Layers;
  title: string;
  hint?: string;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardHeader className="flex flex-row items-start justify-between gap-3 p-5 pb-3">
        <div className="flex min-w-0 items-start gap-2.5">
          <span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary-50 text-primary-600 ring-1 ring-primary-100 dark:bg-primary-950/50 dark:text-primary-300 dark:ring-primary-900/70">
            <Icon className="size-4" />
          </span>
          <div className="min-w-0">
            <CardTitle className="text-base font-semibold tracking-normal text-accent-950 dark:text-white">{title}</CardTitle>
            {hint && <p className="mt-0.5 text-xs text-accent-400 dark:text-accent-500">{hint}</p>}
          </div>
        </div>
        {action}
      </CardHeader>
      <CardContent className="p-5 pt-0">{children}</CardContent>
    </Card>
  );
}

function Loading({ label = '加载中…' }: { label?: string }) {
  return (
    <div className="flex items-center justify-center gap-2 py-8 text-sm text-accent-400">
      <Loader2 className="size-4 animate-spin" /> {label}
    </div>
  );
}

function Empty({ children }: { children: React.ReactNode }) {
  return (
    <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-8 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
      {children}
    </div>
  );
}

function ErrorLine({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-3 py-2.5 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
      <AlertCircle className="mt-0.5 size-4 shrink-0" />
      <span>{children}</span>
    </div>
  );
}

export default function AccountPage() {
  // 各分区独立状态,互不阻塞:一处失败不影响其它卡片。
  const [groups, setGroups] = useState<MeGroup[] | null>(null);
  const [userGroup, setUserGroup] = useState('');
  const [groupsErr, setGroupsErr] = useState<string | null>(null);
  const [groupsLoading, setGroupsLoading] = useState(true);

  const [quota, setQuota] = useState<MeQuotaWindow[] | null>(null);
  const [quotaErr, setQuotaErr] = useState<string | null>(null);
  const [quotaLoading, setQuotaLoading] = useState(true);

  const [code, setCode] = useState<MyReferralCode | null>(null);
  const [codeErr, setCodeErr] = useState<string | null>(null);
  const [summary, setSummary] = useState<InvitationSummary | null>(null);
  const [inviteLoading, setInviteLoading] = useState(true);
  const [copied, setCopied] = useState(false);

  const [checkin, setCheckin] = useState<CheckinStatus | null>(null);
  const [checkinErr, setCheckinErr] = useState<string | null>(null);
  const [checkinLoading, setCheckinLoading] = useState(true);
  const [claiming, setClaiming] = useState(false);
  const [claimNotice, setClaimNotice] = useState<string | null>(null);

  const [referrals, setReferrals] = useState<ReferralListResponse | null>(null);
  const [referralsErr, setReferralsErr] = useState<string | null>(null);
  const [referralsLoading, setReferralsLoading] = useState(true);

  const [rewards, setRewards] = useState<ReferralRewardsResponse | null>(null);
  const [rewardsErr, setRewardsErr] = useState<string | null>(null);
  const [rewardsLoading, setRewardsLoading] = useState(true);

  const loadGroups = useCallback(async () => {
    setGroupsLoading(true);
    setGroupsErr(null);
    try {
      const r = await fetchGroups();
      setGroups(r.items);
      setUserGroup(r.user_group);
    } catch (err) {
      setGroupsErr(friendlyMessage(err));
    } finally {
      setGroupsLoading(false);
    }
  }, []);

  const loadQuota = useCallback(async () => {
    setQuotaLoading(true);
    setQuotaErr(null);
    try {
      const r = await fetchQuota();
      setQuota(r.items);
    } catch (err) {
      setQuotaErr(friendlyMessage(err));
    } finally {
      setQuotaLoading(false);
    }
  }, []);

  const loadInvite = useCallback(async () => {
    setInviteLoading(true);
    setCodeErr(null);
    try {
      // 邀请码与概要并行;概要失败不应吞掉邀请码。
      const [c, s] = await Promise.allSettled([fetchMyReferralCode(), fetchInvitationSummary()]);
      if (c.status === 'fulfilled') setCode(c.value);
      else setCodeErr(friendlyMessage(c.reason));
      if (s.status === 'fulfilled') setSummary(s.value);
    } catch (err) {
      setCodeErr(friendlyMessage(err));
    } finally {
      setInviteLoading(false);
    }
  }, []);

  const loadCheckin = useCallback(async () => {
    setCheckinLoading(true);
    setCheckinErr(null);
    try {
      const r = await fetchCheckinStatus();
      setCheckin(r);
    } catch (err) {
      setCheckinErr(friendlyMessage(err));
    } finally {
      setCheckinLoading(false);
    }
  }, []);

  const loadReferrals = useCallback(async () => {
    setReferralsLoading(true);
    setReferralsErr(null);
    try {
      const r = await fetchReferrals({ limit: REFERRAL_PAGE, offset: 0 });
      setReferrals(r);
    } catch (err) {
      setReferralsErr(friendlyMessage(err));
    } finally {
      setReferralsLoading(false);
    }
  }, []);

  const loadRewards = useCallback(async () => {
    setRewardsLoading(true);
    setRewardsErr(null);
    try {
      const r = await fetchReferralRewards({ limit: REFERRAL_PAGE, offset: 0 });
      setRewards(r);
    } catch (err) {
      setRewardsErr(friendlyMessage(err));
    } finally {
      setRewardsLoading(false);
    }
  }, []);

  const loadAll = useCallback(() => {
    void loadGroups();
    void loadQuota();
    void loadInvite();
    void loadCheckin();
    void loadReferrals();
    void loadRewards();
  }, [loadGroups, loadQuota, loadInvite, loadCheckin, loadReferrals, loadRewards]);

  useEffect(() => {
    loadAll();
  }, [loadAll]);

  const anyLoading = groupsLoading || quotaLoading || inviteLoading || checkinLoading || referralsLoading || rewardsLoading;

  const handleCheckin = useCallback(async () => {
    setClaiming(true);
    setCheckinErr(null);
    setClaimNotice(null);
    try {
      const res = await doCheckin();
      setClaimNotice(`签到成功,领取 ${formatCents(res.reward_cents)},当前余额 ${formatCents(res.new_balance)}。`);
      await loadCheckin();
    } catch (err) {
      // 已签到 (409) 走友好提示并刷新状态,而非红色错误条。
      if (err instanceof ApiError && err.code === 'daily_checkin_already_claimed') {
        setClaimNotice('今日已签到。');
        await loadCheckin();
      } else {
        setCheckinErr(friendlyMessage(err));
      }
    } finally {
      setClaiming(false);
    }
  }, [loadCheckin]);

  const handleCopy = useCallback(async () => {
    if (!code?.code) return;
    try {
      await navigator.clipboard.writeText(code.code);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  }, [code]);

  const checkinDisabled = checkin ? !checkin.enabled : false;
  const checkedInToday = checkin?.checked_in_today ?? false;
  const rewardRange = useMemo(() => {
    if (!checkin) return '';
    if (checkin.min_cents === checkin.max_cents) return formatCents(checkin.min_cents);
    return `${formatCents(checkin.min_cents)} ~ ${formatCents(checkin.max_cents)}`;
  }, [checkin]);

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-5">
      <div className="flex flex-col gap-1 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="text-xl font-bold text-accent-950 dark:text-white">个人中心</h1>
          <p className="mt-1 text-sm text-accent-500 dark:text-accent-400">
            分组权限、邀请奖励、每日签到、推荐账本与配额概览,均按当前登录账户(会话鉴权)展示。
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={loadAll} disabled={anyLoading}>
          <RefreshCw className={anyLoading ? 'animate-spin' : ''} />
          刷新
        </Button>
      </div>

      {/* 配额概览 */}
      <SectionCard
        icon={Gauge}
        title="配额概览"
        hint="当前窗口的用量上限与剩余 (USD)。"
        action={
          quota && quota.length > 0 ? (
            <span className="shrink-0 text-[11px] text-accent-400 dark:text-accent-500">{quota.length} 个窗口</span>
          ) : null
        }
      >
        {quotaLoading ? (
          <Loading />
        ) : quotaErr ? (
          <ErrorLine>{quotaErr}</ErrorLine>
        ) : !quota || quota.length === 0 ? (
          <Empty>当前账户未设置配额窗口,用量仅受余额约束。</Empty>
        ) : (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {quota.map((q) => {
              const pct = quotaPercent(q.consumed, q.cap);
              const overage = Number.parseFloat(q.overage) > 0;
              return (
                <div
                  key={`${q.window_kind}-${q.window_start}`}
                  className="rounded-lg border border-accent-200 bg-accent-50/60 p-3.5 dark:border-accent-800 dark:bg-accent-950/40"
                >
                  <div className="flex items-center justify-between">
                    <span className="text-sm font-medium text-accent-700 dark:text-accent-200">{windowKindLabel(q.window_kind)}</span>
                    <span className="font-mono text-xs tabular-nums text-accent-500 dark:text-accent-400">{q.request_count} 次请求</span>
                  </div>
                  <div className="mt-2 flex items-baseline gap-1.5">
                    <span className="font-mono text-lg font-bold tabular-nums text-accent-950 dark:text-white">{formatUsd(q.remaining)}</span>
                    <span className="text-xs text-accent-400 dark:text-accent-500">/ {formatUsd(q.cap)} 剩余</span>
                  </div>
                  <div className="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-accent-200 dark:bg-accent-800">
                    <div
                      className={cn('h-full rounded-full', overage ? 'bg-red-500' : 'bg-primary-500')}
                      style={{ width: `${pct}%` }}
                    />
                  </div>
                  <div className="mt-1.5 flex items-center justify-between text-[11px] text-accent-400 dark:text-accent-500">
                    <span>已用 {formatUsd(q.consumed)}</span>
                    {overage && <span className="text-red-500">超额 {formatUsd(q.overage)}</span>}
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </SectionCard>

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
        {/* 我的邀请码 + 邀请概要 */}
        <SectionCard icon={Ticket} title="我的邀请码" hint="分享给好友,达标后双方可获奖励。">
          {inviteLoading ? (
            <Loading />
          ) : (
            <div className="flex flex-col gap-4">
              {codeErr ? (
                <ErrorLine>{codeErr}</ErrorLine>
              ) : (
                <div className="flex items-center gap-2 rounded-lg border border-accent-200 bg-accent-50 px-3 py-2.5 dark:border-accent-800 dark:bg-accent-950/40">
                  <Sparkles className="size-4 shrink-0 text-primary-500" />
                  <code className="min-w-0 flex-1 truncate font-mono text-sm font-semibold text-accent-900 dark:text-accent-100">
                    {code?.code || '—'}
                  </code>
                  <Button size="sm" variant="outline" onClick={handleCopy} disabled={!code?.code}>
                    {copied ? <CheckCircle2 className="size-4 text-emerald-500" /> : <Copy className="size-4" />}
                    {copied ? '已复制' : '复制'}
                  </Button>
                </div>
              )}
              <div className="grid grid-cols-3 gap-3">
                <div className="rounded-lg bg-accent-50 p-3 text-center dark:bg-accent-950/40">
                  <div className="text-lg font-bold tabular-nums text-accent-950 dark:text-white">{summary?.qualified_count ?? 0}</div>
                  <div className="mt-0.5 text-[11px] text-accent-400 dark:text-accent-500">达标邀请</div>
                </div>
                <div className="rounded-lg bg-accent-50 p-3 text-center dark:bg-accent-950/40">
                  <div className="text-lg font-bold tabular-nums text-accent-950 dark:text-white">{summary?.rewarded_count ?? 0}</div>
                  <div className="mt-0.5 text-[11px] text-accent-400 dark:text-accent-500">已发奖</div>
                </div>
                <div className="rounded-lg bg-emerald-50 p-3 text-center dark:bg-emerald-950/30">
                  <div className="text-lg font-bold tabular-nums text-emerald-700 dark:text-emerald-300">
                    {formatCents(summary?.rewards_earned_cents ?? 0)}
                  </div>
                  <div className="mt-0.5 text-[11px] text-emerald-600/70 dark:text-emerald-400/70">累计奖励</div>
                </div>
              </div>
            </div>
          )}
        </SectionCard>

        {/* 每日签到 */}
        <SectionCard
          icon={CalendarCheck}
          title="每日签到"
          hint={checkin ? `本月 ${checkin.month}·已签 ${checkin.records.length} 天` : '每日签到领取随机奖励。'}
        >
          {checkinLoading ? (
            <Loading />
          ) : (
            <div className="flex flex-col gap-3">
              {checkinErr && <ErrorLine>{checkinErr}</ErrorLine>}
              {claimNotice && (
                <div className="flex items-start gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2.5 text-sm text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">
                  <CheckCircle2 className="mt-0.5 size-4 shrink-0" />
                  <span>{claimNotice}</span>
                </div>
              )}
              {checkinDisabled ? (
                <Empty>每日签到当前未开放。</Empty>
              ) : (
                <>
                  <div className="flex items-center justify-between rounded-lg border border-accent-200 bg-accent-50 px-3.5 py-3 dark:border-accent-800 dark:bg-accent-950/40">
                    <div>
                      <div className="text-sm font-medium text-accent-700 dark:text-accent-200">
                        {checkedInToday ? '今日已签到' : '今日尚未签到'}
                      </div>
                      <div className="mt-0.5 text-xs text-accent-400 dark:text-accent-500">奖励区间 {rewardRange}</div>
                    </div>
                    <Button onClick={handleCheckin} disabled={claiming || checkedInToday} size="sm">
                      {claiming ? <Loader2 className="size-4 animate-spin" /> : <Gift className="size-4" />}
                      {checkedInToday ? '已签到' : '立即签到'}
                    </Button>
                  </div>
                  {checkin && checkin.records.length > 0 ? (
                    <div className="max-h-40 overflow-y-auto rounded-lg border border-accent-200 dark:border-accent-800">
                      <Table>
                        <TableHeader>
                          <TableRow>
                            <TableHead>日期</TableHead>
                            <TableHead className="text-right">奖励</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {checkin.records.map((rec) => (
                            <TableRow key={rec.checkin_date}>
                              <TableCell className="text-accent-700 dark:text-accent-200">{rec.checkin_date}</TableCell>
                              <TableCell className="text-right font-mono tabular-nums text-emerald-600 dark:text-emerald-400">
                                +{formatCents(rec.reward_cents)}
                              </TableCell>
                            </TableRow>
                          ))}
                        </TableBody>
                      </Table>
                    </div>
                  ) : (
                    <Empty>本月暂无签到记录。</Empty>
                  )}
                </>
              )}
            </div>
          )}
        </SectionCard>
      </div>

      {/* 可用分组 */}
      <SectionCard
        icon={Layers}
        title="可用分组"
        hint="当前账户可绑定到 API Key 的渠道分组。"
        action={
          userGroup ? (
            <span className="shrink-0 rounded-md bg-primary-50 px-2 py-0.5 text-[11px] font-medium text-primary-700 ring-1 ring-inset ring-primary-100 dark:bg-primary-950/50 dark:text-primary-300 dark:ring-primary-900/70">
              当前层级:{userGroup}
            </span>
          ) : null
        }
      >
        {groupsLoading ? (
          <Loading />
        ) : groupsErr ? (
          <ErrorLine>{groupsErr}</ErrorLine>
        ) : !groups || groups.length === 0 ? (
          <Empty>暂无可用分组,请联系管理员开通。</Empty>
        ) : (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {groups.map((g) => (
              <div
                key={g.pool_group_id}
                className="flex items-center justify-between rounded-lg border border-accent-200 bg-accent-50/60 px-3.5 py-3 dark:border-accent-800 dark:bg-accent-950/40"
              >
                <div className="min-w-0">
                  <div className="truncate text-sm font-medium text-accent-800 dark:text-accent-100">
                    {g.name || `分组 #${g.pool_group_id}`}
                  </div>
                  <div className="mt-0.5 text-[11px] text-accent-400 dark:text-accent-500">ID {g.pool_group_id}</div>
                </div>
                {g.has_public_ratio ? (
                  <span className="shrink-0 rounded-md bg-blue-50 px-2 py-0.5 font-mono text-xs font-medium text-blue-700 ring-1 ring-inset ring-blue-200 dark:bg-blue-950/40 dark:text-blue-300 dark:ring-blue-900/60">
                    ×{g.ratio}
                  </span>
                ) : (
                  <span className="shrink-0 text-[11px] text-accent-400 dark:text-accent-500">倍率不公开</span>
                )}
              </div>
            ))}
          </div>
        )}
      </SectionCard>

      {/* 推荐记录 + 奖励账本 */}
      <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
        <SectionCard
          icon={Users}
          title="推荐记录"
          hint="通过你的邀请码注册的用户及其达标状态。"
          action={referrals ? <span className="shrink-0 text-[11px] text-accent-400 dark:text-accent-500">共 {referrals.total} 条</span> : null}
        >
          {referralsLoading ? (
            <Loading />
          ) : referralsErr ? (
            <ErrorLine>{referralsErr}</ErrorLine>
          ) : !referrals || referrals.items.length === 0 ? (
            <Empty>还没有推荐记录,分享邀请码邀请好友吧。</Empty>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>被推荐用户</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>注册时间</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {referrals.items.map((r) => (
                    <TableRow key={r.referral_id}>
                      <TableCell className="font-mono text-xs text-accent-600 dark:text-accent-300">#{r.referee_user_id}</TableCell>
                      <TableCell>
                        <span className={cn('inline-flex items-center rounded-md px-2 py-0.5 text-xs font-medium ring-1 ring-inset', toneRing(referralStatusTone(r.status)))}>
                          {referralStatusLabel(r.status)}
                        </span>
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">{fmtDateTime(r.created_at)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
              {referrals.total > referrals.items.length && (
                <p className="mt-3 text-center text-[11px] text-accent-400 dark:text-accent-500">
                  仅显示最近 {referrals.items.length} / {referrals.total} 条
                </p>
              )}
            </div>
          )}
        </SectionCard>

        <SectionCard
          icon={Gift}
          title="推荐奖励账本"
          hint="推荐达标后发放的奖励明细 (USD)。"
          action={
            rewards ? (
              <span className="shrink-0 rounded-md bg-emerald-50 px-2 py-0.5 text-[11px] font-semibold text-emerald-700 ring-1 ring-inset ring-emerald-200 dark:bg-emerald-950/30 dark:text-emerald-300 dark:ring-emerald-900/60">
                累计 {formatUsd(rewards.total_reward_usd)}
              </span>
            ) : null
          }
        >
          {rewardsLoading ? (
            <Loading />
          ) : rewardsErr ? (
            <ErrorLine>{rewardsErr}</ErrorLine>
          ) : !rewards || rewards.items.length === 0 ? (
            <Empty>暂无奖励发放记录。</Empty>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>类型</TableHead>
                    <TableHead className="text-right">金额</TableHead>
                    <TableHead>时间</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {rewards.items.map((r, i) => (
                    <TableRow key={`${r.referral_id}-${i}`}>
                      <TableCell className="text-xs text-accent-600 dark:text-accent-300">{r.reward_type || '—'}</TableCell>
                      <TableCell className="text-right font-mono tabular-nums text-emerald-600 dark:text-emerald-400">
                        +{formatUsd(r.amount_usd)}
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">{fmtDateTime(r.created_at)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
              {rewards.total > rewards.items.length && (
                <p className="mt-3 text-center text-[11px] text-accent-400 dark:text-accent-500">
                  仅显示最近 {rewards.items.length} / {rewards.total} 条
                </p>
              )}
            </div>
          )}
        </SectionCard>
      </div>
    </div>
  );
}
