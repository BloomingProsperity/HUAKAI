'use client';

// 订阅页：当前订阅卡（分组/状态/到期 + 日/周/月配额进度条）+ 进度明细 + 我的订阅列表 + 在售套餐。
// 端点全部走 lib/api/subscriptions.ts（session 鉴权）。loading / 空 / 错误态齐全。
//
// 借鉴（CLEAN-ROOM，仅形态，未抄码）：
//   - sub2api(LGPL) user/SubscriptionsView.vue：loading→空→列表 的三态骨架、状态徽章
//     (active/expired/revoked 配色)、到期时间着色、日/周/月用量进度条、active 订阅上的动作按钮。
//   - new-api(copyleft)：无 windowed 订阅页；其套餐卡 + 购买按钮的电商化呈现作「在售套餐」区参考。
//   - 进度条离散档位宽度写法沿用 HUAKAI 自有 app/usage/QuotaWindowsCard.tsx
//     （Tailwind 不支持运行时 % 类名）。

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertCircle,
  ArrowUpCircle,
  CalendarClock,
  CheckCircle2,
  CreditCard,
  Gauge,
  Layers,
  Loader2,
  RefreshCw,
  Sparkles,
  XCircle,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
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
import { friendlyMessage } from '@/lib/api/errors';
import {
  cancelRenew,
  changePlan,
  daysRemaining,
  formatResetIn,
  getCurrentSubscription,
  getSubscriptionProgress,
  listPlans,
  listSubscriptions,
  parseUsd,
  purchasePlan,
  statusLabel,
  windowLabel,
  type CurrentSubscriptionResponse,
  type PlanView,
  type SubscriptionProgressWindow,
  type SubscriptionStatus,
  type SubscriptionView,
} from '@/lib/api/subscriptions';
import { cn } from '@/lib/utils';

// ---- 格式化 ----

function fmtDateTime(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false });
}

function fmtDate(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleDateString('zh-CN');
}

function fmtUsd(value: string | null | undefined): string {
  return `$${parseUsd(value).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 4 })}`;
}

// 套餐价：price_cents 是最小货币单位整数。
function fmtPrice(cents: number, currency: string): string {
  const amount = (cents / 100).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  return `${(currency || 'USD').toUpperCase()} ${amount}`;
}

function statusBadgeVariant(status: SubscriptionStatus): 'default' | 'secondary' | 'destructive' {
  if (status === 'active') return 'default';
  if (status === 'revoked') return 'destructive';
  return 'secondary';
}

// 到期临近着色：≤3 天红、≤7 天琥珀、其余常规。
function expiryToneClass(expiresAt: string | null | undefined): string {
  const d = daysRemaining(expiresAt);
  if (d === null) return 'text-accent-500 dark:text-accent-400';
  if (d <= 3) return 'text-red-600 dark:text-red-400';
  if (d <= 7) return 'text-amber-600 dark:text-amber-400';
  return 'text-accent-600 dark:text-accent-300';
}

// 进度条宽度离散档位（沿用 QuotaWindowsCard：Tailwind 无运行时 % 类名）。
function widthClass(pct: number): string {
  const r = pct / 100;
  if (r <= 0) return 'w-0';
  if (r < 0.1) return 'w-[8%]';
  if (r < 0.25) return 'w-1/4';
  if (r < 0.4) return 'w-2/5';
  if (r < 0.55) return 'w-1/2';
  if (r < 0.7) return 'w-2/3';
  if (r < 0.85) return 'w-4/5';
  if (r < 1) return 'w-[92%]';
  return 'w-full';
}

function barTone(pct: number, overLimit: boolean): string {
  if (overLimit || pct >= 100) return 'bg-red-500';
  if (pct >= 85) return 'bg-amber-500';
  return 'bg-primary-500 shadow-glow';
}

// ---- 子组件：单条配额窗口进度 ----

function ProgressBar({ w }: { w: SubscriptionProgressWindow }) {
  const pct = Number.isFinite(w.usage_percent) ? w.usage_percent : 0;
  const clamped = Math.max(0, Math.min(pct, 100));
  return (
    <div>
      <div className="flex items-end justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="text-sm font-semibold text-accent-900 dark:text-accent-100">{windowLabel(w.window_kind)}</span>
            {w.over_limit && <Badge variant="destructive">已超额</Badge>}
          </div>
          <div className="mt-1 text-xs text-accent-500 dark:text-accent-400">
            已用 {fmtUsd(w.consumed)} / 上限 {fmtUsd(w.cap)} · 剩余 {fmtUsd(w.remaining)}
            {w.over_limit && parseUsd(w.over_limit_amount) > 0 && (
              <span className="ml-1 text-red-500">（超 {fmtUsd(w.over_limit_amount)}）</span>
            )}
          </div>
          <div className="mt-0.5 flex items-center gap-1 text-[11px] text-accent-400 dark:text-accent-500">
            <CalendarClock className="size-3" />
            {formatResetIn(w.resets_in_seconds)} · {w.request_count.toLocaleString('zh-CN')} 次请求
          </div>
        </div>
        <div className="shrink-0 text-right">
          <div className="text-lg font-bold tabular-nums text-accent-950 dark:text-white">{pct.toFixed(0)}%</div>
        </div>
      </div>
      <div className="mt-2 h-2.5 overflow-hidden rounded-full bg-accent-100 dark:bg-accent-800">
        <div className={cn('h-full rounded-full', barTone(pct, w.over_limit), widthClass(clamped))} />
      </div>
    </div>
  );
}

// ---- 主页面 ----

export default function SubscriptionsPage() {
  const [current, setCurrent] = useState<CurrentSubscriptionResponse | null>(null);
  const [progress, setProgress] = useState<SubscriptionProgressWindow[]>([]);
  const [progressUnavailable, setProgressUnavailable] = useState(false);
  const [subs, setSubs] = useState<SubscriptionView[]>([]);
  const [plans, setPlans] = useState<PlanView[]>([]);

  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [actionId, setActionId] = useState<string | null>(null); // 当前进行中的动作标识（防重复点击）

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      // 进度依赖后端 quota store，可能 503；单独 catch，不让它拖垮整页。
      const [cur, list, plansResp, prog] = await Promise.all([
        getCurrentSubscription(),
        listSubscriptions(),
        listPlans().catch(() => ({ plans: [] as PlanView[] })),
        getSubscriptionProgress()
          .then((r) => {
            setProgressUnavailable(false);
            return r.progress;
          })
          .catch(() => {
            setProgressUnavailable(true);
            return [] as SubscriptionProgressWindow[];
          }),
      ]);
      setCurrent(cur);
      setSubs(list.subscriptions ?? []);
      setPlans((plansResp.plans ?? []).slice().sort((a, b) => a.sort_order - b.sort_order || a.price_cents - b.price_cents));
      setProgress(prog);
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const handleCancelRenew = useCallback(async () => {
    setActionId('cancel-renew');
    setError(null);
    setNotice(null);
    try {
      await cancelRenew();
      setNotice('已关闭自动续订，当前已生效权益不受影响。');
      await load();
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setActionId(null);
    }
  }, [load]);

  const handleChangePlan = useCallback(
    async (planId: number, planName: string) => {
      setActionId(`change-${planId}`);
      setError(null);
      setNotice(null);
      try {
        await changePlan(planId);
        setNotice(`已切换到「${planName}」。自助换套餐仅支持升级，降级请联系管理员。`);
        await load();
      } catch (err) {
        setError(friendlyMessage(err));
      } finally {
        setActionId(null);
      }
    },
    [load],
  );

  const handlePurchase = useCallback(
    async (planId: number, planName: string) => {
      setActionId(`purchase-${planId}`);
      setError(null);
      setNotice(null);
      try {
        const res = await purchasePlan(planId);
        setNotice(
          `已为「${planName}」生成订单 ${res.order.out_trade_no}（${res.order.status}）。完成支付后订阅自动生效。`,
        );
        await load();
      } catch (err) {
        setError(friendlyMessage(err));
      } finally {
        setActionId(null);
      }
    },
    [load],
  );

  const currentSub = current?.subscription ?? null;
  const autoRenew = current?.auto_renew ?? false;
  const currentPlanId = currentSub?.plan_id ?? null;

  // 升级候选：在售启用、且非当前套餐（降级后端会拒，UI 不预判金额，仅排除当前套餐自身）。
  const upgradeCandidates = useMemo(
    () => plans.filter((p) => p.enabled && p.for_sale && p.id !== currentPlanId),
    [plans, currentPlanId],
  );

  if (loading) {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-5">
        <PageHeader />
        <div className="flex items-center justify-center gap-2 py-20 text-sm text-accent-400">
          <Loader2 className="size-5 animate-spin" /> 加载订阅信息中…
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-5">
      <div className="flex items-start justify-between gap-3">
        <PageHeader />
        <Button onClick={() => void load()} size="sm" variant="outline" disabled={actionId !== null}>
          <RefreshCw />
          刷新
        </Button>
      </div>

      {error && (
        <div className="flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
          <AlertCircle className="mt-0.5 size-4 shrink-0" />
          <span>{error}</span>
        </div>
      )}
      {notice && (
        <div className="flex items-start gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">
          <CheckCircle2 className="mt-0.5 size-4 shrink-0" />
          <span>{notice}</span>
        </div>
      )}

      {/* 当前订阅卡 */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <Sparkles className="size-4 text-primary-600 dark:text-primary-300" />
            当前订阅
          </CardTitle>
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {!currentSub ? (
            <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
              当前没有生效中的订阅。从下方「可购套餐」选购或升级一个套餐。
            </div>
          ) : (
            <div className="flex flex-col gap-4">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-lg font-bold text-accent-950 dark:text-white">
                      {currentSub.granted_group || `套餐 #${currentSub.plan_id}`}
                    </span>
                    <Badge variant={statusBadgeVariant(currentSub.status)}>{statusLabel(currentSub.status)}</Badge>
                    <Badge variant={autoRenew ? 'default' : 'outline'}>
                      {autoRenew ? '自动续订开启' : '自动续订关闭'}
                    </Badge>
                  </div>
                  <div className="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
                    <span className="text-accent-500 dark:text-accent-400">套餐 ID #{currentSub.plan_id}</span>
                    <span className="text-accent-500 dark:text-accent-400">生效 {fmtDate(currentSub.starts_at)}</span>
                    <span className={cn('flex items-center gap-1 font-medium', expiryToneClass(currentSub.expires_at))}>
                      <CalendarClock className="size-3.5" />
                      到期 {fmtDateTime(currentSub.expires_at)}
                      {(() => {
                        const dr = daysRemaining(currentSub.expires_at);
                        return dr !== null ? <span className="ml-1">（剩 {dr} 天）</span> : null;
                      })()}
                    </span>
                  </div>
                </div>
                {currentSub.status === 'active' && autoRenew && (
                  <Button
                    onClick={() => void handleCancelRenew()}
                    size="sm"
                    variant="outline"
                    disabled={actionId !== null}
                  >
                    {actionId === 'cancel-renew' ? <Loader2 className="size-4 animate-spin" /> : <XCircle />}
                    关闭自动续订
                  </Button>
                )}
              </div>

              {/* 套餐配额上限速览 */}
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                {[
                  { label: '日上限', cap: currentSub.daily_cap_usd },
                  { label: '周上限', cap: currentSub.weekly_cap_usd },
                  { label: '月上限', cap: currentSub.monthly_cap_usd },
                ].map((c) => (
                  <div
                    key={c.label}
                    className="rounded-lg border border-accent-200 bg-accent-50 p-3 dark:border-accent-800 dark:bg-accent-950/40"
                  >
                    <div className="text-[11px] text-accent-500 dark:text-accent-400">{c.label}</div>
                    <div className="mt-0.5 text-sm font-semibold tabular-nums text-accent-900 dark:text-accent-100">
                      {c.cap ? fmtUsd(c.cap) : '不限'}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      {/* 配额进度（仅当前订阅存在时有意义） */}
      {currentSub && (
        <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
          <CardHeader className="p-5 pb-3">
            <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
              <Gauge className="size-4 text-primary-600 dark:text-primary-300" />
              配额进度
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-5 p-5 pt-0">
            {progressUnavailable ? (
              <div className="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-700 dark:border-amber-900/60 dark:bg-amber-950/40 dark:text-amber-300">
                <AlertCircle className="mt-0.5 size-4 shrink-0" />
                <span>配额进度暂时不可用（后端进度服务未就绪），请稍后刷新。</span>
              </div>
            ) : progress.length === 0 ? (
              <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
                当前订阅未设置日/周/月配额窗口，或本周期尚无用量。
              </div>
            ) : (
              progress.map((w) => <ProgressBar key={w.window_kind} w={w} />)
            )}
          </CardContent>
        </Card>
      )}

      {/* 我的订阅列表（含历史/已过期/已取消） */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-row items-center justify-between p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <Layers className="size-4 text-primary-600 dark:text-primary-300" />
            订阅记录
          </CardTitle>
          {subs.length > 0 && (
            <span className="text-[11px] text-accent-400 dark:text-accent-500">共 {subs.length} 条</span>
          )}
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {subs.length === 0 ? (
            <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
              暂无任何订阅记录。
            </div>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>分组 / 套餐</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>日 / 周 / 月上限</TableHead>
                    <TableHead>生效</TableHead>
                    <TableHead>到期</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {subs.map((s) => (
                    <TableRow key={s.id}>
                      <TableCell>
                        <div className="font-medium text-accent-900 dark:text-accent-100">
                          {s.granted_group || `套餐 #${s.plan_id}`}
                        </div>
                        <div className="text-[11px] text-accent-400">ID #{s.id} · 套餐 #{s.plan_id}</div>
                      </TableCell>
                      <TableCell>
                        <Badge variant={statusBadgeVariant(s.status)}>{statusLabel(s.status)}</Badge>
                      </TableCell>
                      <TableCell className="text-xs tabular-nums text-accent-600 dark:text-accent-300">
                        {s.daily_cap_usd ? fmtUsd(s.daily_cap_usd) : '不限'} /{' '}
                        {s.weekly_cap_usd ? fmtUsd(s.weekly_cap_usd) : '不限'} /{' '}
                        {s.monthly_cap_usd ? fmtUsd(s.monthly_cap_usd) : '不限'}
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                        {fmtDate(s.starts_at)}
                      </TableCell>
                      <TableCell className={cn('whitespace-nowrap text-xs font-medium', expiryToneClass(s.expires_at))}>
                        {fmtDateTime(s.expires_at)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>

      {/* 可购套餐（升级 / 购买） */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <CreditCard className="size-4 text-primary-600 dark:text-primary-300" />
            可购套餐
          </CardTitle>
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {plans.length === 0 ? (
            <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
              当前没有在售套餐。
            </div>
          ) : (
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {plans.map((p) => {
                const isCurrent = p.id === currentPlanId;
                const canUpgrade = currentSub != null && currentSub.status === 'active' && !isCurrent;
                const busy = actionId === `change-${p.id}` || actionId === `purchase-${p.id}`;
                return (
                  <div
                    key={p.id}
                    className={cn(
                      'flex flex-col gap-3 rounded-xl border p-4',
                      isCurrent
                        ? 'border-primary-300 bg-primary-50/50 dark:border-primary-700 dark:bg-primary-950/20'
                        : 'border-accent-200 bg-white dark:border-accent-800 dark:bg-accent-950/40',
                    )}
                  >
                    <div className="flex items-start justify-between gap-2">
                      <div className="min-w-0">
                        <div className="font-semibold text-accent-950 dark:text-white">{p.name}</div>
                        {p.granted_group && (
                          <div className="text-[11px] text-accent-400">分组 {p.granted_group}</div>
                        )}
                      </div>
                      {isCurrent && <Badge variant="default">当前</Badge>}
                    </div>
                    {p.description && (
                      <p className="text-xs leading-relaxed text-accent-500 dark:text-accent-400">{p.description}</p>
                    )}
                    <div className="text-lg font-bold tabular-nums text-accent-950 dark:text-white">
                      {fmtPrice(p.price_cents, p.currency_code)}
                      <span className="ml-1 text-xs font-normal text-accent-400">/ {p.validity_days} 天</span>
                    </div>
                    <ul className="space-y-1 text-xs text-accent-600 dark:text-accent-300">
                      <li>日上限 {p.daily_cap_usd ? fmtUsd(p.daily_cap_usd) : '不限'}</li>
                      <li>周上限 {p.weekly_cap_usd ? fmtUsd(p.weekly_cap_usd) : '不限'}</li>
                      <li>月上限 {p.monthly_cap_usd ? fmtUsd(p.monthly_cap_usd) : '不限'}</li>
                    </ul>
                    <div className="mt-auto flex flex-col gap-2 pt-1">
                      {isCurrent ? (
                        <Button size="sm" variant="outline" disabled>
                          当前套餐
                        </Button>
                      ) : (
                        <>
                          {canUpgrade && (
                            <Button
                              size="sm"
                              onClick={() => void handleChangePlan(p.id, p.name)}
                              disabled={actionId !== null}
                            >
                              {busy && actionId === `change-${p.id}` ? (
                                <Loader2 className="size-4 animate-spin" />
                              ) : (
                                <ArrowUpCircle />
                              )}
                              升级到此套餐
                            </Button>
                          )}
                          <Button
                            size="sm"
                            variant={canUpgrade ? 'outline' : 'default'}
                            onClick={() => void handlePurchase(p.id, p.name)}
                            disabled={actionId !== null}
                          >
                            {busy && actionId === `purchase-${p.id}` ? (
                              <Loader2 className="size-4 animate-spin" />
                            ) : (
                              <CreditCard />
                            )}
                            购买
                          </Button>
                        </>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          )}
          {upgradeCandidates.length === 0 && currentSub && plans.length > 0 && (
            <p className="mt-4 text-[11px] text-accent-400 dark:text-accent-500">
              自助换套餐仅支持升级；如需降级请联系管理员。
            </p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function PageHeader() {
  return (
    <div className="flex flex-col gap-1">
      <h1 className="text-xl font-bold text-accent-950 dark:text-white">订阅</h1>
      <p className="text-sm text-accent-500 dark:text-accent-400">
        查看当前订阅与日/周/月配额进度，管理自动续订，升级或购买套餐。
      </p>
    </div>
  );
}
