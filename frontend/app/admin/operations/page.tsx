'use client';

// admin 运营管理控制台 —— 管理 token 轨（lib/api/adminOperations.ts，从 localStorage huakai_admin_token
// 取 Bearer，非 session 用户面）。多 tab：兑换码 · 订阅套餐 · 推荐总览 · 支付订单。
//
// 端点全部读后端 admin handler 真码确认（见 lib/api/adminOperations.ts 头注）。鉴权要点：
//   - 兑换码 / 订阅 / 支付 → resolveAdmin 强制 platform_admin（否则 403 admin_forbidden），tenant_id 必带。
//   - 推荐 → resolveAdminTenant：platform_admin 必带 tenant_id；tenant_operator 用自身 scope。
//   单租户部署默认 tenant=1（页内可改）。
//
// 借鉴（CLEAN-ROOM，CLAUDE.md §11/§12，仅功能/字段/动作/布局形态，未抄码；详见 lib 头注 + 下方逐 tab 注）：
//   - sub2api(LGPL)@e34ad2b views/admin/RedeemView.vue（生成弹窗 count/value/有效期 + 列表）、SubscriptionsView.vue
//     （套餐列表 + 指派）、views/admin/affiliates/*（推荐总览卡 + 记录列表）、views/admin/orders/*（订单列表 + 状态过滤）。
//   - new-api(AGPL)@1ac0f58 兑换码/充值/订单运营页（生成 + 列表 + 状态徽章形态）。
//   - CLIProxyAPI@21fad9db 纯中继代理，无等价模块。
//   三态骨架 / 徽章配色 / 卡片表格 / 弹窗外壳沿用 HUAKAI 自有 app/admin/users/page.tsx。

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertCircle,
  ArrowLeftRight,
  Ban,
  CalendarClock,
  CheckCircle2,
  Copy,
  Gift,
  Layers,
  Loader2,
  Plus,
  RefreshCw,
  RotateCcw,
  Search,
  ShieldX,
  Ticket,
  TrendingUp,
  UserPlus,
  Users,
  Wallet,
  X,
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
  assignSubscription,
  bulkAssignSubscription,
  cancelOrder,
  cancelSubscription,
  changeSubscriptionPlan,
  confirmOrder,
  createPlan,
  createSubscriptionVoucher,
  createVoucher,
  createVoucherBatch,
  disablePlan,
  extendSubscription,
  formatCents,
  formatDate,
  formatDateTime,
  formatUSDString,
  getPaymentDashboard,
  getReferralOverview,
  listAssignmentsByUser,
  listOrders,
  listPlans,
  listReferralRewards,
  listReferrals,
  listVouchers,
  nowRfc3339,
  orderStatusLabel,
  orderStatusVariant,
  resetSubscriptionQuota,
  revokeSubscription,
  revokeVoucher,
  rfc3339FromNow,
  subscriptionStatusLabel,
  subscriptionStatusVariant,
  voucherStatusLabel,
  voucherStatusVariant,
  type AdminOrder,
  type AdminReferral,
  type AdminReferralReward,
  type AdminSubscription,
  type BulkAssignResult,
  type CreatedCode,
  type PaymentDashboard,
  type ReferralOverview,
  type SubscriptionPlan,
  type Voucher,
} from '@/lib/api/adminOperations';
import {
  newRequestId,
  parseBulkUserIds,
  validateChangePlan,
  validateExtendInput,
  validateRevokeReason,
} from '@/lib/api/subscription-lifecycle';
import { cn } from '@/lib/utils';

const DEFAULT_TENANT_ID = 1; // 单租户部署默认
const PAGE_SIZE = 50;

type TabKey = 'vouchers' | 'subscriptions' | 'referrals' | 'payments';

const TABS: { key: TabKey; label: string; icon: React.ReactNode }[] = [
  { key: 'vouchers', label: '兑换码', icon: <Ticket className="size-4" /> },
  { key: 'subscriptions', label: '订阅套餐', icon: <Layers className="size-4" /> },
  { key: 'referrals', label: '推荐总览', icon: <Gift className="size-4" /> },
  { key: 'payments', label: '支付订单', icon: <Wallet className="size-4" /> },
];

// ---- 主页面 ----

export default function AdminOperationsPage() {
  const [tenantId, setTenantId] = useState<number>(DEFAULT_TENANT_ID);
  const [tab, setTab] = useState<TabKey>('vouchers');

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-bold text-accent-950 dark:text-white">运营管理</h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          兑换码生成 · 订阅套餐与指派 · 推荐总览 · 支付订单。走管理 token；兑换码 / 订阅 / 支付需 platform_admin，须指定租户 ID。
        </p>
      </div>

      {/* 租户 + tab 控制条 */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardContent className="flex flex-wrap items-center gap-3 p-4">
          <div className="flex items-center gap-2">
            <label className="text-xs text-accent-500 dark:text-accent-400">租户 ID</label>
            <input
              type="number"
              min={1}
              value={tenantId}
              onChange={(e) => setTenantId(Math.max(1, Number(e.target.value) || 1))}
              className="h-9 w-20 rounded-md border border-input bg-background px-3 text-sm tabular-nums"
            />
          </div>
          <div className="flex flex-wrap gap-1.5">
            {TABS.map((t) => (
              <Button
                key={t.key}
                size="sm"
                variant={tab === t.key ? 'default' : 'outline'}
                onClick={() => setTab(t.key)}
              >
                {t.icon}
                {t.label}
              </Button>
            ))}
          </div>
        </CardContent>
      </Card>

      {tab === 'vouchers' && <VouchersTab tenantId={tenantId} />}
      {tab === 'subscriptions' && <SubscriptionsTab tenantId={tenantId} />}
      {tab === 'referrals' && <ReferralsTab tenantId={tenantId} />}
      {tab === 'payments' && <PaymentsTab tenantId={tenantId} />}
    </div>
  );
}

// ---- 共享 UI ----

function Banner({ kind, text }: { kind: 'error' | 'ok'; text: string }) {
  if (kind === 'error') {
    return (
      <div className="flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
        <AlertCircle className="mt-0.5 size-4 shrink-0" />
        <span>{text}</span>
      </div>
    );
  }
  return (
    <div className="flex items-start gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">
      <CheckCircle2 className="mt-0.5 size-4 shrink-0" />
      <span>{text}</span>
    </div>
  );
}

function SectionCard({
  title,
  icon,
  action,
  children,
}: {
  title: string;
  icon: React.ReactNode;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardHeader className="flex flex-row items-center justify-between p-5 pb-3">
        <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
          {icon}
          {title}
        </CardTitle>
        {action}
      </CardHeader>
      <CardContent className="p-5 pt-0">{children}</CardContent>
    </Card>
  );
}

function EmptyRow({ text }: { text: string }) {
  return (
    <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
      {text}
    </div>
  );
}

function LoadingRow({ text }: { text: string }) {
  return (
    <div className="flex items-center justify-center gap-2 py-12 text-sm text-accent-400">
      <Loader2 className="size-5 animate-spin" /> {text}
    </div>
  );
}

function ModalShell({
  title,
  icon,
  onClose,
  children,
  wide,
}: {
  title: string;
  icon: React.ReactNode;
  onClose: () => void;
  children: React.ReactNode;
  wide?: boolean;
}) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onClick={onClose}
      role="presentation"
    >
      <div
        className={cn(
          'w-full rounded-xl border border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900',
          wide ? 'max-w-2xl' : 'max-w-md',
        )}
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
      >
        <div className="flex items-center justify-between border-b border-accent-200 p-4 dark:border-accent-800">
          <div className="flex items-center gap-2 text-base font-semibold text-accent-950 dark:text-white">
            {icon}
            {title}
          </div>
          <Button size="icon" variant="ghost" onClick={onClose} className="size-8">
            <X />
          </Button>
        </div>
        <div className="p-4">{children}</div>
      </div>
    </div>
  );
}

const labelCls = 'text-xs text-accent-500 dark:text-accent-400';
const inputCls = 'h-9 rounded-md border border-input bg-background px-3 text-sm';

// =====================================================================================
// 兑换码 tab
// 借鉴：sub2api RedeemView 生成弹窗（count/value/有效期）+ 列表；new-api 兑换码运营页。
// =====================================================================================

function VouchersTab({ tenantId }: { tenantId: number }) {
  const [vouchers, setVouchers] = useState<Voucher[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [actionId, setActionId] = useState<number | null>(null);
  const [showGen, setShowGen] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await listVouchers({ tenant_id: tenantId, limit: 200 });
      setVouchers(res.vouchers ?? []);
    } catch (err) {
      setError(friendlyMessage(err));
      setVouchers([]);
    } finally {
      setLoading(false);
    }
  }, [tenantId]);

  useEffect(() => {
    void load();
  }, [load]);

  const handleRevoke = useCallback(
    async (v: Voucher) => {
      setActionId(v.id);
      setError(null);
      setNotice(null);
      try {
        await revokeVoucher(v.id, { tenant_id: tenantId, reason: '运营作废' });
        setNotice(`券 #${v.id} 已作废。`);
        await load();
      } catch (err) {
        setError(friendlyMessage(err));
      } finally {
        setActionId(null);
      }
    },
    [tenantId, load],
  );

  return (
    <div className="flex flex-col gap-4">
      {error && <Banner kind="error" text={error} />}
      {notice && <Banner kind="ok" text={notice} />}

      <SectionCard
        title="兑换码"
        icon={<Ticket className="size-4 text-primary-600 dark:text-primary-300" />}
        action={
          <div className="flex gap-1.5">
            <Button size="sm" variant="outline" onClick={() => void load()} disabled={loading || actionId !== null}>
              <RefreshCw className={cn(loading && 'animate-spin')} />
              刷新
            </Button>
            <Button size="sm" onClick={() => setShowGen(true)} disabled={actionId !== null}>
              <Plus />
              生成
            </Button>
          </div>
        }
      >
        {loading && vouchers.length === 0 ? (
          <LoadingRow text="加载兑换码中…" />
        ) : vouchers.length === 0 ? (
          <EmptyRow text="当前租户暂无兑换码，点击「生成」创建。" />
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID / 指纹</TableHead>
                  <TableHead className="text-right">面额</TableHead>
                  <TableHead>类型</TableHead>
                  <TableHead>核销</TableHead>
                  <TableHead>有效期</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {vouchers.map((v) => (
                  <TableRow key={v.id}>
                    <TableCell>
                      <div className="font-medium text-accent-900 dark:text-accent-100">#{v.id}</div>
                      <div className="font-mono text-[11px] text-accent-400" title={v.code_fingerprint}>
                        {v.code_fingerprint.slice(0, 12)}…
                      </div>
                    </TableCell>
                    <TableCell className="text-right font-mono text-sm tabular-nums text-accent-900 dark:text-accent-100">
                      {formatCents(v.amount_cents, v.currency_code)}
                    </TableCell>
                    <TableCell className="text-xs text-accent-600 dark:text-accent-300">
                      {v.grant_kind === 'subscription' ? '订阅券' : '余额券'}
                    </TableCell>
                    <TableCell className="text-xs tabular-nums text-accent-600 dark:text-accent-300">
                      {v.redeemed_count} / {v.max_redemptions}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                      {formatDate(v.valid_from)} ~ {formatDate(v.valid_until)}
                    </TableCell>
                    <TableCell>
                      <Badge variant={voucherStatusVariant(v.status)}>{voucherStatusLabel(v.status)}</Badge>
                    </TableCell>
                    <TableCell className="text-right">
                      {v.status === 'active' ? (
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => void handleRevoke(v)}
                          disabled={actionId !== null}
                          title="作废券码"
                        >
                          {actionId === v.id ? <Loader2 className="size-4 animate-spin" /> : <Ban />}
                          作废
                        </Button>
                      ) : (
                        <span className="text-[11px] text-accent-400">—</span>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </SectionCard>

      {showGen && (
        <GenerateVoucherModal
          tenantId={tenantId}
          onClose={() => setShowGen(false)}
          onDone={(msg) => {
            setShowGen(false);
            setNotice(msg);
            void load();
          }}
        />
      )}
    </div>
  );
}

// 生成兑换码弹窗（单张 / 批量）。后端要求：tenant_id / amount_cents>0 / valid_from / valid_until>valid_from。
function GenerateVoucherModal({
  tenantId,
  onClose,
  onDone,
}: {
  tenantId: number;
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const [count, setCount] = useState('1');
  const [amount, setAmount] = useState(''); // 元（USD），提交时换算分
  const [validityDays, setValidityDays] = useState('30');
  const [maxRedemptions, setMaxRedemptions] = useState('1');
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);
  const [generatedCodes, setGeneratedCodes] = useState<string[] | null>(null);

  async function submit() {
    const n = parseInt(count, 10);
    const amt = Number(amount);
    const days = parseInt(validityDays, 10);
    const maxR = parseInt(maxRedemptions, 10);
    if (!Number.isInteger(n) || n < 1 || n > 1000) {
      setLocalError('生成数量需为 1..1000。');
      return;
    }
    if (!Number.isFinite(amt) || amt <= 0) {
      setLocalError('面额（USD）需大于 0。');
      return;
    }
    if (!Number.isInteger(days) || days < 1) {
      setLocalError('有效天数需为正整数。');
      return;
    }
    if (!Number.isInteger(maxR) || maxR < 1) {
      setLocalError('每码核销上限需为正整数。');
      return;
    }
    setSubmitting(true);
    setLocalError(null);
    const amountCents = Math.round(amt * 100);
    const validFrom = nowRfc3339();
    const validUntil = rfc3339FromNow(days);
    try {
      if (n === 1) {
        const res = await createVoucher({
          tenant_id: tenantId,
          amount_cents: amountCents,
          valid_from: validFrom,
          valid_until: validUntil,
          max_redemptions: maxR,
        });
        setGeneratedCodes(res.code ? [res.code] : []);
      } else {
        const res = await createVoucherBatch({
          tenant_id: tenantId,
          count: n,
          amount_cents: amountCents,
          valid_from: validFrom,
          valid_until: validUntil,
          max_redemptions: maxR,
        });
        setGeneratedCodes((res.codes ?? []).map((c: CreatedCode) => c.code));
      }
    } catch (err) {
      setLocalError(friendlyMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  // 生成成功 → 展示明文码（仅此一次可见），用户复制后关闭。
  if (generatedCodes) {
    const text = generatedCodes.join('\n');
    return (
      <ModalShell
        title="生成成功"
        icon={<CheckCircle2 className="size-4 text-emerald-600 dark:text-emerald-400" />}
        onClose={() => onDone(`已生成 ${generatedCodes.length} 张兑换码。`)}
        wide
      >
        <div className="flex flex-col gap-3">
          <p className="text-xs text-accent-500 dark:text-accent-400">
            明文券码仅此一次可见（后端只存指纹/哈希），请立即复制保存。
          </p>
          <textarea
            readOnly
            rows={Math.min(10, Math.max(2, generatedCodes.length))}
            value={text}
            className="rounded-md border border-input bg-accent-50 px-3 py-2 font-mono text-xs dark:bg-accent-950/40"
          />
          <div className="flex justify-end gap-2">
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                if (typeof navigator !== 'undefined' && navigator.clipboard) {
                  void navigator.clipboard.writeText(text);
                }
              }}
            >
              <Copy />
              复制全部
            </Button>
            <Button size="sm" onClick={() => onDone(`已生成 ${generatedCodes.length} 张兑换码。`)}>
              完成
            </Button>
          </div>
        </div>
      </ModalShell>
    );
  }

  return (
    <ModalShell
      title="生成兑换码"
      icon={<Ticket className="size-4 text-primary-600 dark:text-primary-300" />}
      onClose={onClose}
    >
      <div className="flex flex-col gap-3">
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label className={labelCls}>生成数量（1..1000）</label>
            <input type="number" min={1} max={1000} value={count} onChange={(e) => setCount(e.target.value)} className={cn(inputCls, 'tabular-nums')} />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelCls}>面额（USD，例 10）</label>
            <input type="text" inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} placeholder="10" className={cn(inputCls, 'tabular-nums')} />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelCls}>有效天数</label>
            <input type="number" min={1} value={validityDays} onChange={(e) => setValidityDays(e.target.value)} className={cn(inputCls, 'tabular-nums')} />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelCls}>每码核销上限</label>
            <input type="number" min={1} value={maxRedemptions} onChange={(e) => setMaxRedemptions(e.target.value)} className={cn(inputCls, 'tabular-nums')} />
          </div>
        </div>
        <p className="text-[11px] text-accent-400">
          余额券（grant_kind=balance），币种 USD（后端仅支持 USD）。有效起始为当前时刻。
        </p>
        {localError && <Banner kind="error" text={localError} />}
        <div className="flex justify-end gap-2 pt-1">
          <Button size="sm" variant="outline" onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button size="sm" onClick={() => void submit()} disabled={submitting}>
            {submitting ? <Loader2 className="size-4 animate-spin" /> : <Plus />}
            生成
          </Button>
        </div>
      </div>
    </ModalShell>
  );
}

// =====================================================================================
// 订阅套餐 tab
// 借鉴：sub2api SubscriptionsView（套餐列表 + 指派动作）。
// =====================================================================================

function SubscriptionsTab({ tenantId }: { tenantId: number }) {
  const [plans, setPlans] = useState<SubscriptionPlan[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [actionId, setActionId] = useState<number | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [assignTarget, setAssignTarget] = useState<SubscriptionPlan | null>(null);
  const [bulkTarget, setBulkTarget] = useState<SubscriptionPlan | null>(null);
  const [voucherTarget, setVoucherTarget] = useState<SubscriptionPlan | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await listPlans({ tenant_id: tenantId });
      setPlans(res.plans ?? []);
    } catch (err) {
      setError(friendlyMessage(err));
      setPlans([]);
    } finally {
      setLoading(false);
    }
  }, [tenantId]);

  useEffect(() => {
    void load();
  }, [load]);

  const handleDisable = useCallback(
    async (p: SubscriptionPlan) => {
      setActionId(p.id);
      setError(null);
      setNotice(null);
      try {
        await disablePlan(p.id, tenantId);
        setNotice(`套餐「${p.name}」已停用。`);
        await load();
      } catch (err) {
        setError(friendlyMessage(err));
      } finally {
        setActionId(null);
      }
    },
    [tenantId, load],
  );

  return (
    <div className="flex flex-col gap-4">
      {error && <Banner kind="error" text={error} />}
      {notice && <Banner kind="ok" text={notice} />}

      <SectionCard
        title="订阅套餐"
        icon={<Layers className="size-4 text-primary-600 dark:text-primary-300" />}
        action={
          <div className="flex gap-1.5">
            <Button size="sm" variant="outline" onClick={() => void load()} disabled={loading || actionId !== null}>
              <RefreshCw className={cn(loading && 'animate-spin')} />
              刷新
            </Button>
            <Button size="sm" onClick={() => setShowCreate(true)} disabled={actionId !== null}>
              <Plus />
              新建套餐
            </Button>
          </div>
        }
      >
        {loading && plans.length === 0 ? (
          <LoadingRow text="加载套餐中…" />
        ) : plans.length === 0 ? (
          <EmptyRow text="当前租户暂无订阅套餐，点击「新建套餐」创建。" />
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>套餐</TableHead>
                  <TableHead className="text-right">价格</TableHead>
                  <TableHead>时长 / 分组</TableHead>
                  <TableHead>日/周/月限额(USD)</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {plans.map((p) => (
                  <TableRow key={p.id}>
                    <TableCell>
                      <div className="font-medium text-accent-900 dark:text-accent-100">{p.name}</div>
                      <div className="text-[11px] text-accent-400">
                        #{p.id}
                        {p.description ? ` · ${p.description}` : ''}
                      </div>
                    </TableCell>
                    <TableCell className="text-right font-mono text-sm tabular-nums text-accent-900 dark:text-accent-100">
                      {p.price_cents > 0 ? formatCents(p.price_cents, p.currency_code) : '免费'}
                    </TableCell>
                    <TableCell className="text-xs text-accent-600 dark:text-accent-300">
                      {p.validity_days} 天
                      {p.granted_group ? ` · ${p.granted_group}` : ''}
                    </TableCell>
                    <TableCell className="text-xs tabular-nums text-accent-500 dark:text-accent-400">
                      {p.daily_cap_usd ?? '—'} / {p.weekly_cap_usd ?? '—'} / {p.monthly_cap_usd ?? '—'}
                    </TableCell>
                    <TableCell>
                      <Badge variant={p.enabled ? (p.for_sale ? 'default' : 'secondary') : 'destructive'}>
                        {p.enabled ? (p.for_sale ? '在售' : '已下架') : '已停用'}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex items-center justify-end gap-1.5">
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => {
                            setAssignTarget(p);
                            setError(null);
                            setNotice(null);
                          }}
                          disabled={actionId !== null}
                          title="指派给用户"
                        >
                          <UserPlus />
                          指派
                        </Button>
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => {
                            setBulkTarget(p);
                            setError(null);
                            setNotice(null);
                          }}
                          disabled={actionId !== null}
                          title="批量指派给多个用户"
                        >
                          <Users />
                          批量
                        </Button>
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => {
                            setVoucherTarget(p);
                            setError(null);
                            setNotice(null);
                          }}
                          disabled={actionId !== null}
                          title="生成订阅兑换券"
                        >
                          <Ticket />
                          订阅券
                        </Button>
                        {p.enabled && (
                          <Button
                            size="sm"
                            variant="ghost"
                            onClick={() => void handleDisable(p)}
                            disabled={actionId !== null}
                            title="停用套餐"
                          >
                            {actionId === p.id ? <Loader2 className="size-4 animate-spin" /> : <Ban />}
                          </Button>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </SectionCard>

      {showCreate && (
        <CreatePlanModal
          tenantId={tenantId}
          onClose={() => setShowCreate(false)}
          onDone={(msg) => {
            setShowCreate(false);
            setNotice(msg);
            void load();
          }}
        />
      )}

      {assignTarget && (
        <AssignPlanModal
          plan={assignTarget}
          tenantId={tenantId}
          onClose={() => setAssignTarget(null)}
          onDone={(msg) => {
            setAssignTarget(null);
            setNotice(msg);
          }}
        />
      )}

      {bulkTarget && (
        <BulkAssignModal
          plan={bulkTarget}
          tenantId={tenantId}
          onClose={() => setBulkTarget(null)}
          onDone={(msg) => {
            setBulkTarget(null);
            setNotice(msg);
          }}
        />
      )}

      {voucherTarget && (
        <SubscriptionVoucherModal
          plan={voucherTarget}
          tenantId={tenantId}
          onClose={() => setVoucherTarget(null)}
          onDone={(msg) => {
            setVoucherTarget(null);
            setNotice(msg);
          }}
        />
      )}

      {/* 订阅生命周期：按用户查其订阅 → 逐行 续期/重置配额/改套餐/取消/撤销 */}
      <SubscriptionLifecyclePanel tenantId={tenantId} plans={plans} />
    </div>
  );
}

function CreatePlanModal({
  tenantId,
  onClose,
  onDone,
}: {
  tenantId: number;
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const [name, setName] = useState('');
  const [price, setPrice] = useState(''); // 元
  const [validityDays, setValidityDays] = useState('30');
  const [grantedGroup, setGrantedGroup] = useState('');
  const [dailyCap, setDailyCap] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  async function submit() {
    if (name.trim() === '') {
      setLocalError('套餐名必填。');
      return;
    }
    const days = parseInt(validityDays, 10);
    if (!Number.isInteger(days) || days < 1) {
      setLocalError('有效天数需为正整数。');
      return;
    }
    let priceCents = 0;
    if (price.trim() !== '') {
      const p = Number(price);
      if (!Number.isFinite(p) || p < 0) {
        setLocalError('价格需为非负数。');
        return;
      }
      priceCents = Math.round(p * 100);
    }
    setSubmitting(true);
    setLocalError(null);
    try {
      await createPlan({
        tenant_id: tenantId,
        name: name.trim(),
        validity_days: days,
        price_cents: priceCents,
        granted_group: grantedGroup.trim() || undefined,
        daily_cap_usd: dailyCap.trim() || undefined,
      });
      onDone(`套餐「${name.trim()}」已创建。`);
    } catch (err) {
      setLocalError(friendlyMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <ModalShell title="新建订阅套餐" icon={<Layers className="size-4 text-primary-600 dark:text-primary-300" />} onClose={onClose}>
      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelCls}>套餐名（必填）</label>
          <input type="text" value={name} onChange={(e) => setName(e.target.value)} placeholder="例：专业版月付" className={inputCls} />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label className={labelCls}>价格（USD，省略=免费）</label>
            <input type="text" inputMode="decimal" value={price} onChange={(e) => setPrice(e.target.value)} placeholder="0" className={cn(inputCls, 'tabular-nums')} />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelCls}>时长（天）</label>
            <input type="number" min={1} value={validityDays} onChange={(e) => setValidityDays(e.target.value)} className={cn(inputCls, 'tabular-nums')} />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelCls}>授予分组（可选）</label>
            <input type="text" value={grantedGroup} onChange={(e) => setGrantedGroup(e.target.value)} placeholder="premium" className={inputCls} />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelCls}>日限额 USD（可选）</label>
            <input type="text" inputMode="decimal" value={dailyCap} onChange={(e) => setDailyCap(e.target.value)} placeholder="不限" className={cn(inputCls, 'tabular-nums')} />
          </div>
        </div>
        <p className="text-[11px] text-accent-400">默认上架（for_sale=true）。币种由后端默认。</p>
        {localError && <Banner kind="error" text={localError} />}
        <div className="flex justify-end gap-2 pt-1">
          <Button size="sm" variant="outline" onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button size="sm" onClick={() => void submit()} disabled={submitting}>
            {submitting ? <Loader2 className="size-4 animate-spin" /> : <Plus />}
            创建
          </Button>
        </div>
      </div>
    </ModalShell>
  );
}

function AssignPlanModal({
  plan,
  tenantId,
  onClose,
  onDone,
}: {
  plan: SubscriptionPlan;
  tenantId: number;
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const [userId, setUserId] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  async function submit() {
    const uid = parseInt(userId, 10);
    if (!Number.isInteger(uid) || uid <= 0) {
      setLocalError('请输入有效的用户 ID。');
      return;
    }
    setSubmitting(true);
    setLocalError(null);
    try {
      const res = await assignSubscription({ tenant_id: tenantId, user_id: uid, plan_id: plan.id });
      onDone(
        res.idempotent
          ? `用户 #${uid} 已有此订阅（幂等命中）。`
          : `已为用户 #${uid} 指派套餐「${plan.name}」，到期 ${formatDate(res.subscription.expires_at)}。`,
      );
    } catch (err) {
      setLocalError(friendlyMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <ModalShell title="指派订阅" icon={<UserPlus className="size-4 text-primary-600 dark:text-primary-300" />} onClose={onClose}>
      <div className="flex flex-col gap-3">
        <div className="rounded-lg border border-accent-200 bg-accent-50 p-3 text-xs text-accent-600 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-300">
          套餐「{plan.name}」 · {plan.validity_days} 天 · {plan.price_cents > 0 ? formatCents(plan.price_cents, plan.currency_code) : '免费'}
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelCls}>目标用户 ID</label>
          <input type="number" min={1} value={userId} onChange={(e) => setUserId(e.target.value)} placeholder="例：1024" className={cn(inputCls, 'tabular-nums')} />
        </div>
        <p className="text-[11px] text-accent-400">手动指派（admin source），不经支付。重复指派同套餐为幂等。</p>
        {localError && <Banner kind="error" text={localError} />}
        <div className="flex justify-end gap-2 pt-1">
          <Button size="sm" variant="outline" onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button size="sm" onClick={() => void submit()} disabled={submitting}>
            {submitting ? <Loader2 className="size-4 animate-spin" /> : <UserPlus />}
            指派
          </Button>
        </div>
      </div>
    </ModalShell>
  );
}

// 批量指派弹窗：多用户 ID（逗号/空白/换行分隔）→ 一个套餐。后端逐用户软失败，结果表展示。
// 借鉴：sub2api bulk-assign 逐用户 status map。HUAKAI delta：统一 X-Request-Id 幂等。
function BulkAssignModal({
  plan,
  tenantId,
  onClose,
  onDone,
}: {
  plan: SubscriptionPlan;
  tenantId: number;
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const [raw, setRaw] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);
  const [results, setResults] = useState<BulkAssignResult[] | null>(null);

  async function submit() {
    const parsed = parseBulkUserIds(raw);
    if (parsed.error) {
      setLocalError(parsed.error);
      return;
    }
    setSubmitting(true);
    setLocalError(null);
    try {
      const res = await bulkAssignSubscription(
        { tenant_id: tenantId, user_ids: parsed.ids, plan_id: plan.id },
        newRequestId(),
      );
      setResults(res.results ?? []);
    } catch (err) {
      setLocalError(friendlyMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  if (results) {
    const okCount = results.filter((r) => r.ok).length;
    return (
      <ModalShell
        title="批量指派结果"
        icon={<Users className="size-4 text-primary-600 dark:text-primary-300" />}
        onClose={() => onDone(`批量指派完成：成功 ${okCount} / ${results.length}。`)}
        wide
      >
        <div className="flex flex-col gap-3">
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>用户</TableHead>
                  <TableHead>结果</TableHead>
                  <TableHead>说明</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {results.map((r) => (
                  <TableRow key={r.user_id}>
                    <TableCell className="font-mono text-xs tabular-nums">#{r.user_id}</TableCell>
                    <TableCell>
                      <Badge variant={r.ok ? (r.idempotent ? 'secondary' : 'default') : 'destructive'}>
                        {r.ok ? (r.idempotent ? '已存在' : '已指派') : '失败'}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-xs text-accent-500 dark:text-accent-400">{r.error ?? '—'}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          <div className="flex justify-end">
            <Button size="sm" onClick={() => onDone(`批量指派完成：成功 ${okCount} / ${results.length}。`)}>
              完成
            </Button>
          </div>
        </div>
      </ModalShell>
    );
  }

  return (
    <ModalShell title="批量指派订阅" icon={<Users className="size-4 text-primary-600 dark:text-primary-300" />} onClose={onClose}>
      <div className="flex flex-col gap-3">
        <div className="rounded-lg border border-accent-200 bg-accent-50 p-3 text-xs text-accent-600 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-300">
          套餐「{plan.name}」 · {plan.validity_days} 天
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelCls}>用户 ID 列表（逗号 / 空格 / 换行分隔）</label>
          <textarea
            rows={4}
            value={raw}
            onChange={(e) => setRaw(e.target.value)}
            placeholder="1024, 1025, 1026"
            className={cn(inputCls, 'h-auto py-2 font-mono')}
          />
        </div>
        <p className="text-[11px] text-accent-400">逐用户处理：已有同套餐者幂等命中，无效 ID 单独标失败，不影响其它。</p>
        {localError && <Banner kind="error" text={localError} />}
        <div className="flex justify-end gap-2 pt-1">
          <Button size="sm" variant="outline" onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button size="sm" onClick={() => void submit()} disabled={submitting}>
            {submitting ? <Loader2 className="size-4 animate-spin" /> : <Users />}
            批量指派
          </Button>
        </div>
      </div>
    </ModalShell>
  );
}

// 生成订阅券弹窗：建一张 grant_kind=subscription 的兑换券（兑换后授予该套餐）。
// 借鉴：sub2api redeem-codes generate type=subscription。HUAKAI delta：复用 voucher 子系统 + 幂等头。
function SubscriptionVoucherModal({
  plan,
  tenantId,
  onClose,
  onDone,
}: {
  plan: SubscriptionPlan;
  tenantId: number;
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const [amount, setAmount] = useState(''); // 名义价 USD（信息性）
  const [validityDays, setValidityDays] = useState('30'); // 券码兑换窗口
  const [maxRedemptions, setMaxRedemptions] = useState('1');
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);
  const [code, setCode] = useState<string | null>(null);

  async function submit() {
    const amt = Number(amount);
    const days = parseInt(validityDays, 10);
    const maxR = parseInt(maxRedemptions, 10);
    if (!Number.isFinite(amt) || amt < 0) {
      setLocalError('名义价（USD）需为非负数。');
      return;
    }
    if (!Number.isInteger(days) || days < 1) {
      setLocalError('券码有效天数需为正整数。');
      return;
    }
    if (!Number.isInteger(maxR) || maxR < 1) {
      setLocalError('每码核销上限需为正整数。');
      return;
    }
    setSubmitting(true);
    setLocalError(null);
    try {
      const res = await createSubscriptionVoucher(
        {
          tenant_id: tenantId,
          plan_id: plan.id,
          amount_cents: Math.round(amt * 100),
          valid_from: nowRfc3339(),
          valid_until: rfc3339FromNow(days),
          max_redemptions: maxR,
        },
        newRequestId(),
      );
      setCode(res.code ?? '');
    } catch (err) {
      setLocalError(friendlyMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  if (code !== null) {
    return (
      <ModalShell
        title="订阅券已生成"
        icon={<CheckCircle2 className="size-4 text-emerald-600 dark:text-emerald-400" />}
        onClose={() => onDone(`已生成套餐「${plan.name}」订阅券。`)}
      >
        <div className="flex flex-col gap-3">
          <p className="text-xs text-accent-500 dark:text-accent-400">明文券码仅此一次可见，请立即复制保存。</p>
          <input readOnly value={code} className={cn(inputCls, 'font-mono')} />
          <div className="flex justify-end gap-2">
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                if (typeof navigator !== 'undefined' && navigator.clipboard) void navigator.clipboard.writeText(code);
              }}
            >
              <Copy />
              复制
            </Button>
            <Button size="sm" onClick={() => onDone(`已生成套餐「${plan.name}」订阅券。`)}>
              完成
            </Button>
          </div>
        </div>
      </ModalShell>
    );
  }

  return (
    <ModalShell title="生成订阅券" icon={<Ticket className="size-4 text-primary-600 dark:text-primary-300" />} onClose={onClose}>
      <div className="flex flex-col gap-3">
        <div className="rounded-lg border border-accent-200 bg-accent-50 p-3 text-xs text-accent-600 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-300">
          套餐「{plan.name}」 · 兑换后授予 {plan.validity_days} 天
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label className={labelCls}>名义价（USD，信息性）</label>
            <input type="text" inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} placeholder="0" className={cn(inputCls, 'tabular-nums')} />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelCls}>券码有效天数</label>
            <input type="number" min={1} value={validityDays} onChange={(e) => setValidityDays(e.target.value)} className={cn(inputCls, 'tabular-nums')} />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelCls}>每码核销上限</label>
            <input type="number" min={1} value={maxRedemptions} onChange={(e) => setMaxRedemptions(e.target.value)} className={cn(inputCls, 'tabular-nums')} />
          </div>
        </div>
        <p className="text-[11px] text-accent-400">名义价仅信息展示，兑换时不入余额；兑换授予的是套餐时长/配额。</p>
        {localError && <Banner kind="error" text={localError} />}
        <div className="flex justify-end gap-2 pt-1">
          <Button size="sm" variant="outline" onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button size="sm" onClick={() => void submit()} disabled={submitting}>
            {submitting ? <Loader2 className="size-4 animate-spin" /> : <Plus />}
            生成
          </Button>
        </div>
      </div>
    </ModalShell>
  );
}

// 订阅生命周期面板：按用户 ID 查其订阅 → 逐行 续期/重置配额/改套餐/取消/撤销。
// 借鉴：sub2api 按用户管理订阅生命周期。HUAKAI delta：cancel(软)与 revoke(硬+reason)分立 + 统一幂等头。
type LifecycleAction = 'extend' | 'reset-quota' | 'change-plan' | 'cancel' | 'revoke';

function SubscriptionLifecyclePanel({ tenantId, plans }: { tenantId: number; plans: SubscriptionPlan[] }) {
  const [userIdInput, setUserIdInput] = useState('');
  const [queriedUserId, setQueriedUserId] = useState<number | null>(null);
  const [subs, setSubs] = useState<AdminSubscription[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [target, setTarget] = useState<{ action: LifecycleAction; sub: AdminSubscription } | null>(null);

  const reload = useCallback(
    async (uid: number) => {
      setLoading(true);
      setError(null);
      try {
        const res = await listAssignmentsByUser({ tenant_id: tenantId, user_id: uid });
        setSubs(res.subscriptions ?? []);
        setQueriedUserId(uid);
      } catch (err) {
        setError(friendlyMessage(err));
        setSubs([]);
      } finally {
        setLoading(false);
      }
    },
    [tenantId],
  );

  function query() {
    const uid = parseInt(userIdInput, 10);
    if (!Number.isInteger(uid) || uid <= 0) {
      setError('请输入有效的用户 ID。');
      return;
    }
    setNotice(null);
    void reload(uid);
  }

  return (
    <SectionCard title="订阅生命周期" icon={<ArrowLeftRight className="size-4 text-primary-600 dark:text-primary-300" />}>
      <div className="flex flex-col gap-4">
        {error && <Banner kind="error" text={error} />}
        {notice && <Banner kind="ok" text={notice} />}

        <div className="flex items-end gap-2">
          <div className="flex flex-col gap-1">
            <label className={labelCls}>用户 ID</label>
            <input
              type="number"
              min={1}
              value={userIdInput}
              onChange={(e) => setUserIdInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') query();
              }}
              placeholder="例：1024"
              className={cn(inputCls, 'w-32 tabular-nums')}
            />
          </div>
          <Button size="sm" onClick={query} disabled={loading}>
            {loading ? <Loader2 className="size-4 animate-spin" /> : <Search />}
            查询订阅
          </Button>
        </div>

        {queriedUserId !== null &&
          (loading ? (
            <LoadingRow text="加载用户订阅中…" />
          ) : subs.length === 0 ? (
            <EmptyRow text={`用户 #${queriedUserId} 暂无订阅记录。`} />
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>订阅</TableHead>
                    <TableHead>套餐</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>有效期</TableHead>
                    <TableHead className="text-right">生命周期操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {subs.map((s) => (
                    <TableRow key={s.id}>
                      <TableCell className="font-mono text-xs tabular-nums">#{s.id}</TableCell>
                      <TableCell className="text-xs text-accent-600 dark:text-accent-300">
                        #{s.plan_id}
                        {s.granted_group ? ` · ${s.granted_group}` : ''}
                      </TableCell>
                      <TableCell>
                        <Badge variant={subscriptionStatusVariant(s.status)}>{subscriptionStatusLabel(s.status)}</Badge>
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                        {formatDate(s.starts_at)} ~ {formatDate(s.expires_at)}
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex flex-wrap items-center justify-end gap-1.5">
                          <Button size="sm" variant="outline" onClick={() => setTarget({ action: 'extend', sub: s })} title="续期">
                            <CalendarClock />
                            续期
                          </Button>
                          <Button size="sm" variant="outline" onClick={() => setTarget({ action: 'reset-quota', sub: s })} title="重置配额">
                            <RotateCcw />
                            重置
                          </Button>
                          <Button size="sm" variant="outline" onClick={() => setTarget({ action: 'change-plan', sub: s })} title="改套餐">
                            <ArrowLeftRight />
                            改套餐
                          </Button>
                          <Button size="sm" variant="ghost" onClick={() => setTarget({ action: 'cancel', sub: s })} title="取消（软）">
                            <Ban />
                            取消
                          </Button>
                          <Button size="sm" variant="ghost" onClick={() => setTarget({ action: 'revoke', sub: s })} title="撤销（硬，需原因）">
                            <ShieldX />
                            撤销
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          ))}
      </div>

      {target && (
        <LifecycleActionModal
          action={target.action}
          subscription={target.sub}
          plans={plans}
          tenantId={tenantId}
          onClose={() => setTarget(null)}
          onDone={(msg) => {
            setTarget(null);
            setNotice(msg);
            if (queriedUserId !== null) void reload(queriedUserId);
          }}
        />
      )}
    </SectionCard>
  );
}

const LIFECYCLE_META: Record<LifecycleAction, { title: string; verb: string }> = {
  extend: { title: '续期订阅', verb: '续期' },
  'reset-quota': { title: '重置配额', verb: '重置配额' },
  'change-plan': { title: '更换套餐', verb: '更换套餐' },
  cancel: { title: '取消订阅', verb: '取消' },
  revoke: { title: '撤销订阅', verb: '撤销' },
};

// 单一参数化弹窗，按 action 渲染对应字段；提交前用 subscription-lifecycle.ts 的校验器把关，再带新幂等键调对应 client。
function LifecycleActionModal({
  action,
  subscription,
  plans,
  tenantId,
  onClose,
  onDone,
}: {
  action: LifecycleAction;
  subscription: AdminSubscription;
  plans: SubscriptionPlan[];
  tenantId: number;
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const [extendMode, setExtendMode] = useState<'days' | 'until'>('days');
  const [days, setDays] = useState('30');
  const [until, setUntil] = useState(''); // datetime-local
  const [newPlanId, setNewPlanId] = useState<string>('');
  const [allowDowngrade, setAllowDowngrade] = useState(false);
  const [reason, setReason] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  async function submit() {
    setLocalError(null);
    try {
      let msg = '';
      if (action === 'extend') {
        const input =
          extendMode === 'days'
            ? { days: parseInt(days, 10) }
            : { until: until ? new Date(until).toISOString() : '' };
        const err = validateExtendInput(input);
        if (err) {
          setLocalError(err);
          return;
        }
        setSubmitting(true);
        await extendSubscription(subscription.id, tenantId, input, newRequestId());
        msg = `订阅 #${subscription.id} 已续期。`;
      } else if (action === 'reset-quota') {
        setSubmitting(true);
        await resetSubscriptionQuota(subscription.id, tenantId, newRequestId());
        msg = `订阅 #${subscription.id} 配额已重置。`;
      } else if (action === 'change-plan') {
        const pid = parseInt(newPlanId, 10);
        const err = validateChangePlan(pid);
        if (err) {
          setLocalError(err);
          return;
        }
        setSubmitting(true);
        await changeSubscriptionPlan(subscription.id, tenantId, pid, allowDowngrade, newRequestId());
        msg = `订阅 #${subscription.id} 已更换套餐。`;
      } else if (action === 'cancel') {
        setSubmitting(true);
        await cancelSubscription(subscription.id, tenantId, newRequestId());
        msg = `订阅 #${subscription.id} 已取消。`;
      } else {
        const err = validateRevokeReason(reason);
        if (err) {
          setLocalError(err);
          return;
        }
        setSubmitting(true);
        await revokeSubscription(subscription.id, tenantId, reason, newRequestId());
        msg = `订阅 #${subscription.id} 已撤销。`;
      }
      onDone(msg);
    } catch (err) {
      setLocalError(friendlyMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  const meta = LIFECYCLE_META[action];

  return (
    <ModalShell title={meta.title} icon={<ArrowLeftRight className="size-4 text-primary-600 dark:text-primary-300" />} onClose={onClose}>
      <div className="flex flex-col gap-3">
        <div className="rounded-lg border border-accent-200 bg-accent-50 p-3 text-xs text-accent-600 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-300">
          订阅 #{subscription.id} · 套餐 #{subscription.plan_id} · 到期 {formatDate(subscription.expires_at)}
        </div>

        {action === 'extend' && (
          <div className="flex flex-col gap-3">
            <div className="flex gap-1.5">
              <Button size="sm" variant={extendMode === 'days' ? 'default' : 'outline'} onClick={() => setExtendMode('days')}>
                按天数
              </Button>
              <Button size="sm" variant={extendMode === 'until' ? 'default' : 'outline'} onClick={() => setExtendMode('until')}>
                到指定时间
              </Button>
            </div>
            {extendMode === 'days' ? (
              <div className="flex flex-col gap-1">
                <label className={labelCls}>延长天数（&gt;0；缩短请用「到指定时间」）</label>
                <input type="number" min={1} value={days} onChange={(e) => setDays(e.target.value)} className={cn(inputCls, 'tabular-nums')} />
              </div>
            ) : (
              <div className="flex flex-col gap-1">
                <label className={labelCls}>新到期时间</label>
                <input type="datetime-local" value={until} onChange={(e) => setUntil(e.target.value)} className={inputCls} />
              </div>
            )}
          </div>
        )}

        {action === 'change-plan' && (
          <div className="flex flex-col gap-3">
            <div className="flex flex-col gap-1">
              <label className={labelCls}>目标套餐</label>
              <select value={newPlanId} onChange={(e) => setNewPlanId(e.target.value)} className="h-9 rounded-md border border-input bg-background px-3 text-sm">
                <option value="">选择套餐…</option>
                {plans
                  .filter((p) => p.id !== subscription.plan_id)
                  .map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name}（#{p.id} · {p.validity_days} 天）
                    </option>
                  ))}
              </select>
            </div>
            <label className="flex items-center gap-2 text-xs text-accent-600 dark:text-accent-300">
              <input type="checkbox" checked={allowDowngrade} onChange={(e) => setAllowDowngrade(e.target.checked)} />
              允许降级（目标套餐权益低于当前时仍执行）
            </label>
          </div>
        )}

        {action === 'revoke' && (
          <div className="flex flex-col gap-1">
            <label className={labelCls}>撤销原因（必填）</label>
            <input type="text" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="例：违规使用" className={inputCls} />
          </div>
        )}

        {(action === 'cancel' || action === 'reset-quota') && (
          <p className="text-xs text-accent-500 dark:text-accent-400">
            {action === 'cancel' ? '软取消：关闭配额并降级，记录可查。' : '按套餐快照重建全部配额窗口（日/周/月）。'}
          </p>
        )}

        {localError && <Banner kind="error" text={localError} />}
        <div className="flex justify-end gap-2 pt-1">
          <Button size="sm" variant="outline" onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button size="sm" onClick={() => void submit()} disabled={submitting}>
            {submitting ? <Loader2 className="size-4 animate-spin" /> : <CheckCircle2 />}
            确认{meta.verb}
          </Button>
        </div>
      </div>
    </ModalShell>
  );
}

// =====================================================================================
// 推荐总览 tab
// 借鉴：sub2api affiliates 总览卡 + 记录列表。tenant_operator 可省 tenant_id；platform_admin 必带。
// =====================================================================================

function ReferralsTab({ tenantId }: { tenantId: number }) {
  const [overview, setOverview] = useState<ReferralOverview | null>(null);
  const [referrals, setReferrals] = useState<AdminReferral[]>([]);
  const [rewards, setRewards] = useState<AdminReferralReward[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [ov, refs, rew] = await Promise.all([
        getReferralOverview(tenantId).catch(() => null),
        listReferrals({ tenant_id: tenantId, limit: PAGE_SIZE }),
        listReferralRewards({ tenant_id: tenantId, limit: PAGE_SIZE }).catch(() => null),
      ]);
      setOverview(ov);
      setReferrals(refs.items ?? []);
      setRewards(rew?.items ?? []);
    } catch (err) {
      setError(friendlyMessage(err));
      setReferrals([]);
      setRewards([]);
    } finally {
      setLoading(false);
    }
  }, [tenantId]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <div className="flex flex-col gap-4">
      {error && <Banner kind="error" text={error} />}

      {/* 总览卡 */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
          <CardContent className="flex flex-col gap-1 p-5">
            <span className="text-xs text-accent-500 dark:text-accent-400">累计推荐</span>
            <span className="text-2xl font-bold tabular-nums text-accent-950 dark:text-white">
              {overview ? Object.values(overview.counts_by_status).reduce((a, b) => a + b, 0).toLocaleString('zh-CN') : '—'}
            </span>
          </CardContent>
        </Card>
        <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
          <CardContent className="flex flex-col gap-1 p-5">
            <span className="text-xs text-accent-500 dark:text-accent-400">奖励发放笔数</span>
            <span className="text-2xl font-bold tabular-nums text-accent-950 dark:text-white">
              {overview ? overview.reward_count.toLocaleString('zh-CN') : '—'}
            </span>
          </CardContent>
        </Card>
        <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
          <CardContent className="flex flex-col gap-1 p-5">
            <span className="text-xs text-accent-500 dark:text-accent-400">奖励总额</span>
            <span className="text-2xl font-bold tabular-nums text-emerald-600 dark:text-emerald-400">
              {overview ? formatUSDString(overview.total_reward_usd) : '—'}
            </span>
          </CardContent>
        </Card>
      </div>

      {overview && Object.keys(overview.counts_by_status).length > 0 && (
        <div className="flex flex-wrap gap-2">
          {Object.entries(overview.counts_by_status).map(([status, n]) => (
            <Badge key={status} variant="outline">
              {status}: {n.toLocaleString('zh-CN')}
            </Badge>
          ))}
        </div>
      )}

      <SectionCard
        title="推荐记录"
        icon={<Gift className="size-4 text-primary-600 dark:text-primary-300" />}
        action={
          <Button size="sm" variant="outline" onClick={() => void load()} disabled={loading}>
            <RefreshCw className={cn(loading && 'animate-spin')} />
            刷新
          </Button>
        }
      >
        {loading && referrals.length === 0 ? (
          <LoadingRow text="加载推荐记录中…" />
        ) : referrals.length === 0 ? (
          <EmptyRow text="当前租户暂无推荐记录。" />
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <TableHead>推荐人</TableHead>
                  <TableHead>被推荐人</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>时间</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {referrals.map((r) => (
                  <TableRow key={r.id}>
                    <TableCell className="text-xs text-accent-500 dark:text-accent-400">#{r.id}</TableCell>
                    <TableCell className="font-mono text-xs tabular-nums">#{r.referrer_user_id}</TableCell>
                    <TableCell className="font-mono text-xs tabular-nums">#{r.referee_user_id}</TableCell>
                    <TableCell>
                      <Badge variant="outline">{r.status}</Badge>
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                      {formatDateTime(r.created_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </SectionCard>

      <SectionCard title="奖励账本" icon={<TrendingUp className="size-4 text-primary-600 dark:text-primary-300" />}>
        {rewards.length === 0 ? (
          <EmptyRow text="暂无奖励记录。" />
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>推荐人</TableHead>
                  <TableHead>类型</TableHead>
                  <TableHead className="text-right">金额</TableHead>
                  <TableHead>发放时间</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rewards.map((rw) => (
                  <TableRow key={rw.id}>
                    <TableCell className="font-mono text-xs tabular-nums">#{rw.referrer_user_id}</TableCell>
                    <TableCell className="text-xs text-accent-600 dark:text-accent-300">{rw.reward_type}</TableCell>
                    <TableCell className="text-right font-mono text-sm tabular-nums text-emerald-600 dark:text-emerald-400">
                      {formatUSDString(rw.amount_usd)}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                      {formatDateTime(rw.issued_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </SectionCard>
    </div>
  );
}

// =====================================================================================
// 支付订单 tab
// 借鉴：sub2api orders 列表 + 状态过滤 + dashboard 统计卡。
// =====================================================================================

const ORDER_STATUS_OPTIONS = ['all', 'pending', 'paid', 'recharging', 'completed', 'cancelled', 'failed', 'refunded'];

function PaymentsTab({ tenantId }: { tenantId: number }) {
  const [orders, setOrders] = useState<AdminOrder[]>([]);
  const [dashboard, setDashboard] = useState<PaymentDashboard | null>(null);
  const [statusFilter, setStatusFilter] = useState('all');
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [actionId, setActionId] = useState<number | null>(null);

  const offset = page * PAGE_SIZE;

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [list, dash] = await Promise.all([
        listOrders({ tenant_id: tenantId, status: statusFilter, limit: PAGE_SIZE + 1, offset }),
        getPaymentDashboard(tenantId).catch(() => null),
      ]);
      setOrders(list.orders ?? []);
      setDashboard(dash);
    } catch (err) {
      setError(friendlyMessage(err));
      setOrders([]);
    } finally {
      setLoading(false);
    }
  }, [tenantId, statusFilter, offset]);

  useEffect(() => {
    void load();
  }, [load]);

  const visibleOrders = useMemo(() => orders.slice(0, PAGE_SIZE), [orders]);
  const hasNextPage = orders.length > PAGE_SIZE;

  const handleConfirm = useCallback(
    async (o: AdminOrder) => {
      setActionId(o.id);
      setError(null);
      setNotice(null);
      try {
        await confirmOrder(o.id, { tenant_id: tenantId, confirm_reason: '运营确认收款' });
        setNotice(`订单 #${o.id} 已确认收款并履约。`);
        await load();
      } catch (err) {
        setError(friendlyMessage(err));
      } finally {
        setActionId(null);
      }
    },
    [tenantId, load],
  );

  const handleCancel = useCallback(
    async (o: AdminOrder) => {
      setActionId(o.id);
      setError(null);
      setNotice(null);
      try {
        await cancelOrder(o.id, { tenant_id: tenantId, reason: '运营撤单' });
        setNotice(`订单 #${o.id} 已取消。`);
        await load();
      } catch (err) {
        setError(friendlyMessage(err));
      } finally {
        setActionId(null);
      }
    },
    [tenantId, load],
  );

  return (
    <div className="flex flex-col gap-4">
      {error && <Banner kind="error" text={error} />}
      {notice && <Banner kind="ok" text={notice} />}

      {/* dashboard 统计卡 */}
      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        <DashStat label="累计金额" value={dashboard ? formatCents(dashboard.total_amount_cents) : '—'} />
        <DashStat label="累计订单" value={dashboard ? dashboard.total_count.toLocaleString('zh-CN') : '—'} />
        <DashStat label="今日订单" value={dashboard ? dashboard.today_count.toLocaleString('zh-CN') : '—'} />
        <DashStat label="客单价" value={dashboard ? formatCents(dashboard.average_amount_cents) : '—'} />
      </div>

      <SectionCard
        title="支付订单"
        icon={<Wallet className="size-4 text-primary-600 dark:text-primary-300" />}
        action={
          <div className="flex items-center gap-2">
            <select
              value={statusFilter}
              onChange={(e) => {
                setStatusFilter(e.target.value);
                setPage(0);
              }}
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
            >
              {ORDER_STATUS_OPTIONS.map((s) => (
                <option key={s} value={s}>
                  {s === 'all' ? '全部状态' : orderStatusLabel(s)}
                </option>
              ))}
            </select>
            <Button size="sm" variant="outline" onClick={() => void load()} disabled={loading || actionId !== null}>
              <RefreshCw className={cn(loading && 'animate-spin')} />
              刷新
            </Button>
          </div>
        }
      >
        {loading && orders.length === 0 ? (
          <LoadingRow text="加载订单中…" />
        ) : visibleOrders.length === 0 ? (
          <EmptyRow text="没有匹配的订单。" />
        ) : (
          <>
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>订单号</TableHead>
                    <TableHead>用户</TableHead>
                    <TableHead className="text-right">金额</TableHead>
                    <TableHead>类型 / 渠道</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>创建时间</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {visibleOrders.map((o) => (
                    <TableRow key={o.id}>
                      <TableCell>
                        <div className="font-mono text-xs text-accent-900 dark:text-accent-100" title={o.out_trade_no}>
                          {o.out_trade_no.length > 18 ? `${o.out_trade_no.slice(0, 18)}…` : o.out_trade_no}
                        </div>
                        <div className="text-[11px] text-accent-400">#{o.id}</div>
                      </TableCell>
                      <TableCell className="font-mono text-xs tabular-nums">#{o.user_id}</TableCell>
                      <TableCell className="text-right font-mono text-sm tabular-nums text-accent-900 dark:text-accent-100">
                        {formatCents(o.amount_cents, o.currency_code)}
                      </TableCell>
                      <TableCell className="text-xs text-accent-600 dark:text-accent-300">
                        {o.order_kind === 'subscription' ? '订阅' : '充值'} · {o.provider_kind}
                      </TableCell>
                      <TableCell>
                        <Badge variant={orderStatusVariant(o.status)}>{orderStatusLabel(o.status)}</Badge>
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                        {formatDateTime(o.created_at)}
                      </TableCell>
                      <TableCell className="text-right">
                        {o.status === 'pending' ? (
                          <div className="flex items-center justify-end gap-1.5">
                            <Button
                              size="sm"
                              onClick={() => void handleConfirm(o)}
                              disabled={actionId !== null}
                              title="确认收款并履约"
                            >
                              {actionId === o.id ? <Loader2 className="size-4 animate-spin" /> : <CheckCircle2 />}
                              确认
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              onClick={() => void handleCancel(o)}
                              disabled={actionId !== null}
                              title="撤单"
                            >
                              <Ban />
                            </Button>
                          </div>
                        ) : (
                          <span className="text-[11px] text-accent-400">—</span>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            <div className="mt-4 flex items-center justify-between">
              <Button size="sm" variant="outline" onClick={() => setPage((p) => Math.max(0, p - 1))} disabled={page === 0 || loading}>
                上一页
              </Button>
              <span className="text-xs text-accent-400">第 {page + 1} 页</span>
              <Button size="sm" variant="outline" onClick={() => setPage((p) => p + 1)} disabled={!hasNextPage || loading}>
                下一页
              </Button>
            </div>
          </>
        )}
      </SectionCard>
    </div>
  );
}

function DashStat({ label, value }: { label: string; value: string }) {
  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardContent className="flex flex-col gap-1 p-5">
        <span className="text-xs text-accent-500 dark:text-accent-400">{label}</span>
        <span className="text-xl font-bold tabular-nums text-accent-950 dark:text-white">{value}</span>
      </CardContent>
    </Card>
  );
}
