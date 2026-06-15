'use client';

// 充值 & 余额页：余额卡 + 充值入口（金额/预设/渠道选择 → 创建订单 → 显示人工支付指引）+ 我的订单表。
// 端点全部走 lib/api/billing.ts（session 鉴权）。loading / 空 / 错误 / 每 section 独立 503 容错齐全。
//
// Owner 指令：不接真实支付 SDK —— 创建订单后只展示该渠道的人工支付指引文案（manual 转账/扫码、
// taobao 闲鱼下单），不跳第三方收银台；待 admin 确认入账。
//
// 借鉴（CLEAN-ROOM，CLAUDE.md §11/§12，仅形态，未抄码）：
//   - sub2api(LGPL)：充值页「余额 + 金额输入 + 创建订单 + 我的订单(状态徽章/可撤销)」骨架；
//     OrderStatus 大写枚举配色映射到 HUAKAI 小写枚举。
//   - new-api(AGPL) wallet/recharge-form-card：预设金额格 + 自定义金额输入 + 渠道选择的充值表单形态；
//     仅提取功能/字段形态，未抄码。
//   - 三态骨架 / 徽章配色 / 卡片样式 / 进度色板沿用 HUAKAI 自有 app/subscriptions/page.tsx。

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertCircle,
  CheckCircle2,
  Coins,
  CreditCard,
  Info,
  Loader2,
  Receipt,
  RefreshCw,
  Wallet,
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
  cancelOrder,
  createTopup,
  fmtCents,
  getBalance,
  getConfig,
  isCancellable,
  listOrders,
  providerLabel,
  statusLabel,
  type Balance,
  type OrderStatus,
  type PaymentOrder,
  type PortalConfig,
  type PortalProviderConfig,
  type ProviderKind,
} from '@/lib/api/billing';
import { cn } from '@/lib/utils';

// ---- 格式化 ----

function fmtDateTime(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false });
}

// 状态徽章配色：完成=绿(default)、失败/过期=红(destructive)、其余(待支付/入账中)=中性。
function statusBadgeVariant(status: OrderStatus): 'default' | 'secondary' | 'destructive' | 'outline' {
  if (status === 'completed') return 'default';
  if (status === 'failed' || status === 'expired' || status === 'cancelled') return 'destructive';
  if (status === 'pending') return 'outline';
  return 'secondary';
}

// ---- 主页面 ----

export default function BillingPage() {
  const [balance, setBalance] = useState<Balance | null>(null);
  const [balanceUnavailable, setBalanceUnavailable] = useState(false);
  const [config, setConfig] = useState<PortalConfig | null>(null);
  const [configUnavailable, setConfigUnavailable] = useState(false);
  const [orders, setOrders] = useState<PaymentOrder[]>([]);
  const [ordersUnavailable, setOrdersUnavailable] = useState(false);

  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  // 充值表单本地状态。
  const [amountInput, setAmountInput] = useState(''); // 用户输入的美元金额（字符串，避免受控数字坑）
  const [provider, setProvider] = useState<ProviderKind | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [cancellingId, setCancellingId] = useState<number | null>(null);
  // 最近一次创建成功后的支付指引（拿到即止，不跳第三方）。
  const [lastInstruction, setLastInstruction] = useState<{ order: PaymentOrder; instruction: PortalProviderConfig } | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      // 每个 section 独立 503 容错：某依赖未就绪只让该卡显示「暂不可用」，不拖垮整页。
      await Promise.all([
        getBalance()
          .then((r) => {
            setBalance(r.balance);
            setBalanceUnavailable(false);
          })
          .catch(() => {
            setBalance(null);
            setBalanceUnavailable(true);
          }),
        getConfig()
          .then((r) => {
            setConfig(r.config);
            setConfigUnavailable(false);
            // 默认选中第一个启用渠道（若尚未选）。
            setProvider((prev) => prev ?? r.config.providers[0]?.provider ?? null);
          })
          .catch(() => {
            setConfig(null);
            setConfigUnavailable(true);
          }),
        listOrders()
          .then((r) => {
            setOrders(r.orders ?? []);
            setOrdersUnavailable(false);
          })
          .catch(() => {
            setOrders([]);
            setOrdersUnavailable(true);
          }),
      ]);
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const currency = config?.currency_code ?? 'USD';
  const minCents = config?.min_topup_cents ?? 0;
  const maxCents = config?.max_topup_cents ?? 0;
  const presets = config?.preset_amount_cents ?? [];
  const providers = config?.providers ?? [];

  // 解析输入金额为分（仅用于提交与校验，金额运算交后端裁决）。
  const amountCents = useMemo(() => {
    const n = Number.parseFloat(amountInput);
    if (!Number.isFinite(n) || n <= 0) return 0;
    return Math.round(n * 100);
  }, [amountInput]);

  // 前端轻校验，仅用于按钮禁用与提示；最终区间由后端裁决（400 时回显友好文案）。
  const amountValid = amountCents >= minCents && amountCents <= maxCents && minCents > 0;
  const canSubmit = amountValid && provider != null && !submitting;

  const selectPreset = useCallback((cents: number) => {
    setAmountInput((cents / 100).toString());
  }, []);

  const handleCreate = useCallback(async () => {
    if (provider == null) return;
    setSubmitting(true);
    setError(null);
    setNotice(null);
    try {
      const res = await createTopup({ amount_cents: amountCents, provider });
      setLastInstruction({ order: res.order, instruction: res.payment_instruction });
      setNotice(
        `已生成充值订单 ${res.order.out_trade_no}（${statusLabel(res.order.status)}）。请按下方指引完成支付，待管理员确认后入账。`,
      );
      setAmountInput('');
      // 刷新订单列表与余额（余额需 admin 确认后才变，但刷新无害）。
      await load();
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setSubmitting(false);
    }
  }, [amountCents, provider, load]);

  const handleCancel = useCallback(
    async (order: PaymentOrder) => {
      setCancellingId(order.id);
      setError(null);
      setNotice(null);
      try {
        await cancelOrder(order.id);
        setNotice(`已撤销订单 ${order.out_trade_no}。`);
        if (lastInstruction?.order.id === order.id) setLastInstruction(null);
        await load();
      } catch (err) {
        setError(friendlyMessage(err));
      } finally {
        setCancellingId(null);
      }
    },
    [load, lastInstruction],
  );

  if (loading) {
    return (
      <div className="mx-auto flex max-w-5xl flex-col gap-5">
        <PageHeader />
        <div className="flex items-center justify-center gap-2 py-20 text-sm text-accent-400">
          <Loader2 className="size-5 animate-spin" /> 加载充值与余额信息中…
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-5">
      <div className="flex items-start justify-between gap-3">
        <PageHeader />
        <Button onClick={() => void load()} size="sm" variant="outline" disabled={submitting || cancellingId !== null}>
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

      {/* 余额卡 */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <Wallet className="size-4 text-primary-600 dark:text-primary-300" />
            账户余额
          </CardTitle>
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {balanceUnavailable ? (
            <SectionUnavailable text="余额暂时不可用（后端支付服务未就绪），请稍后刷新。" />
          ) : (
            <div className="flex flex-wrap items-end justify-between gap-3">
              <div>
                <div className="text-xs text-accent-500 dark:text-accent-400">可用余额</div>
                <div className="mt-1 text-3xl font-bold tabular-nums text-accent-950 dark:text-white">
                  {fmtCents(balance?.amount_cents ?? 0, currency)}
                </div>
              </div>
              <div className="flex items-center gap-1.5 text-[11px] text-accent-400 dark:text-accent-500">
                <Coins className="size-3.5" />
                由充值入账累计，充值订单经管理员确认后计入。
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      {/* 充值入口 */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <CreditCard className="size-4 text-primary-600 dark:text-primary-300" />
            充值
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-5 p-5 pt-0">
          {configUnavailable ? (
            <SectionUnavailable text="充值配置暂时不可用（后端支付服务未就绪），请稍后刷新。" />
          ) : providers.length === 0 ? (
            <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
              当前未开放自助充值渠道，请联系管理员。
            </div>
          ) : (
            <>
              {/* 预设金额 */}
              {presets.length > 0 && (
                <div>
                  <div className="mb-2 text-xs font-medium text-accent-600 dark:text-accent-300">快捷金额</div>
                  <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
                    {presets.map((cents) => {
                      const active = amountCents === cents;
                      return (
                        <button
                          key={cents}
                          type="button"
                          onClick={() => selectPreset(cents)}
                          className={cn(
                            'rounded-lg border px-3 py-2.5 text-sm font-semibold tabular-nums transition-colors',
                            active
                              ? 'border-primary-400 bg-primary-50 text-primary-700 dark:border-primary-600 dark:bg-primary-950/30 dark:text-primary-200'
                              : 'border-accent-200 bg-white text-accent-800 hover:border-primary-300 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-200 dark:hover:border-primary-700',
                          )}
                        >
                          {fmtCents(cents, currency)}
                        </button>
                      );
                    })}
                  </div>
                </div>
              )}

              {/* 自定义金额 */}
              <div>
                <label htmlFor="topup-amount" className="mb-2 block text-xs font-medium text-accent-600 dark:text-accent-300">
                  充值金额（{currency.toUpperCase()}）
                </label>
                <input
                  id="topup-amount"
                  type="number"
                  inputMode="decimal"
                  min={minCents / 100}
                  max={maxCents / 100}
                  step="0.01"
                  value={amountInput}
                  onChange={(e) => setAmountInput(e.target.value)}
                  placeholder={`${(minCents / 100).toFixed(2)} ~ ${(maxCents / 100).toFixed(2)}`}
                  className="w-full rounded-lg border border-accent-200 bg-white px-3 py-2.5 text-sm tabular-nums text-accent-900 outline-none focus:border-primary-400 focus:ring-2 focus:ring-primary-100 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-100 dark:focus:ring-primary-900/40"
                />
                <p className="mt-1.5 text-[11px] text-accent-400 dark:text-accent-500">
                  单笔范围 {fmtCents(minCents, currency)} ~ {fmtCents(maxCents, currency)}。
                  {amountInput !== '' && !amountValid && (
                    <span className="ml-1 text-amber-600 dark:text-amber-400">金额需落在上述范围内。</span>
                  )}
                </p>
              </div>

              {/* 渠道选择 */}
              <div>
                <div className="mb-2 text-xs font-medium text-accent-600 dark:text-accent-300">支付渠道</div>
                <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
                  {providers.map((p) => {
                    const active = provider === p.provider;
                    return (
                      <button
                        key={p.provider}
                        type="button"
                        onClick={() => setProvider(p.provider)}
                        className={cn(
                          'flex flex-col items-start gap-1 rounded-lg border px-3 py-2.5 text-left transition-colors',
                          active
                            ? 'border-primary-400 bg-primary-50 dark:border-primary-600 dark:bg-primary-950/30'
                            : 'border-accent-200 bg-white hover:border-primary-300 dark:border-accent-800 dark:bg-accent-950/40 dark:hover:border-primary-700',
                        )}
                      >
                        <span className="flex items-center gap-2 text-sm font-semibold text-accent-900 dark:text-accent-100">
                          {providerLabel(p.provider)}
                          {active && <Badge variant="default">已选</Badge>}
                        </span>
                        <span className="line-clamp-2 text-[11px] text-accent-500 dark:text-accent-400">{p.instruction}</span>
                      </button>
                    );
                  })}
                </div>
              </div>

              <div className="flex flex-wrap items-center justify-between gap-3 border-t border-accent-100 pt-4 dark:border-accent-800">
                <div className="text-sm text-accent-500 dark:text-accent-400">
                  本次充值
                  <span className="ml-1.5 text-lg font-bold tabular-nums text-accent-950 dark:text-white">
                    {fmtCents(amountValid ? amountCents : 0, currency)}
                  </span>
                </div>
                <Button onClick={() => void handleCreate()} disabled={!canSubmit}>
                  {submitting ? <Loader2 className="size-4 animate-spin" /> : <CreditCard />}
                  创建充值订单
                </Button>
              </div>

              <p className="flex items-start gap-1.5 text-[11px] text-accent-400 dark:text-accent-500">
                <Info className="mt-0.5 size-3.5 shrink-0" />
                自助充值为人工确认入账：下单后请按所选渠道指引线下/扫码完成支付，管理员核对后金额计入余额。
              </p>
            </>
          )}
        </CardContent>
      </Card>

      {/* 最近一次创建的支付指引（拿到即止，不跳第三方收银台） */}
      {lastInstruction && (
        <Card className="border-primary-300 bg-primary-50/50 shadow-card dark:border-primary-700 dark:bg-primary-950/20">
          <CardHeader className="p-5 pb-3">
            <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
              <Receipt className="size-4 text-primary-600 dark:text-primary-300" />
              支付指引
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-3 p-5 pt-0 text-sm">
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-xs">
              <span className="text-accent-500 dark:text-accent-400">
                订单号 <span className="font-mono text-accent-800 dark:text-accent-200">{lastInstruction.order.out_trade_no}</span>
              </span>
              <span className="text-accent-500 dark:text-accent-400">
                金额 <span className="font-semibold tabular-nums text-accent-900 dark:text-accent-100">{fmtCents(lastInstruction.order.amount_cents, lastInstruction.order.currency_code)}</span>
              </span>
              <Badge variant={statusBadgeVariant(lastInstruction.order.status)}>{statusLabel(lastInstruction.order.status)}</Badge>
              <Badge variant="outline">{providerLabel(lastInstruction.instruction.provider)}</Badge>
            </div>
            <p className="rounded-lg border border-primary-200 bg-white/70 px-4 py-3 leading-relaxed text-accent-700 dark:border-primary-800 dark:bg-accent-950/40 dark:text-accent-200">
              {lastInstruction.instruction.instruction}
            </p>
            {isCancellable(lastInstruction.order.status) && (
              <Button
                size="sm"
                variant="outline"
                onClick={() => void handleCancel(lastInstruction.order)}
                disabled={cancellingId !== null}
              >
                {cancellingId === lastInstruction.order.id ? <Loader2 className="size-4 animate-spin" /> : <XCircle />}
                撤销此订单
              </Button>
            )}
          </CardContent>
        </Card>
      )}

      {/* 我的订单 */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-row items-center justify-between p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <Receipt className="size-4 text-primary-600 dark:text-primary-300" />
            我的订单
          </CardTitle>
          {orders.length > 0 && (
            <span className="text-[11px] text-accent-400 dark:text-accent-500">共 {orders.length} 条</span>
          )}
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {ordersUnavailable ? (
            <SectionUnavailable text="订单记录暂时不可用（后端支付服务未就绪），请稍后刷新。" />
          ) : orders.length === 0 ? (
            <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
              暂无订单记录。完成一次充值后会显示在这里。
            </div>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>订单号</TableHead>
                    <TableHead>类型 / 渠道</TableHead>
                    <TableHead className="text-right">金额</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead className="whitespace-nowrap">创建时间</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {orders.map((o) => (
                    <TableRow key={o.id}>
                      <TableCell>
                        <div className="font-mono text-xs text-accent-800 dark:text-accent-200">{o.out_trade_no}</div>
                        <div className="text-[11px] text-accent-400">ID #{o.id}</div>
                      </TableCell>
                      <TableCell className="text-xs text-accent-600 dark:text-accent-300">
                        <div>{o.order_kind === 'subscription' ? '购订阅' : '充值'}</div>
                        <div className="text-[11px] text-accent-400">{providerLabel(o.provider_kind)}</div>
                      </TableCell>
                      <TableCell className="text-right font-semibold tabular-nums text-accent-900 dark:text-accent-100">
                        {fmtCents(o.amount_cents, o.currency_code)}
                      </TableCell>
                      <TableCell>
                        <Badge variant={statusBadgeVariant(o.status)}>{statusLabel(o.status)}</Badge>
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                        {fmtDateTime(o.created_at)}
                      </TableCell>
                      <TableCell className="text-right">
                        {isCancellable(o.status) ? (
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => void handleCancel(o)}
                            disabled={cancellingId !== null}
                          >
                            {cancellingId === o.id ? <Loader2 className="size-4 animate-spin" /> : <XCircle />}
                            撤销
                          </Button>
                        ) : (
                          <span className="text-[11px] text-accent-300 dark:text-accent-600">—</span>
                        )}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function SectionUnavailable({ text }: { text: string }) {
  return (
    <div className="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-700 dark:border-amber-900/60 dark:bg-amber-950/40 dark:text-amber-300">
      <AlertCircle className="mt-0.5 size-4 shrink-0" />
      <span>{text}</span>
    </div>
  );
}

function PageHeader() {
  return (
    <div className="flex flex-col gap-1">
      <h1 className="text-xl font-bold text-accent-950 dark:text-white">充值 & 余额</h1>
      <p className="text-sm text-accent-500 dark:text-accent-400">
        查看账户余额，自助创建充值订单（按渠道指引线下支付，管理员确认后入账），并跟踪订单状态。
      </p>
    </div>
  );
}
