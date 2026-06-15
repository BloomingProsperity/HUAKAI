'use client';

import { useCallback, useEffect, useState } from 'react';
import {
  AlertCircle,
  CheckCircle2,
  Gift,
  Loader2,
  RefreshCw,
  Ticket,
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
import { friendlyMessage } from '@/lib/api/errors';
import {
  fetchRedemptionHistory,
  formatAmount,
  grantKindLabel,
  redeemVoucher,
  type RedeemResult,
  type RedemptionHistoryItem,
} from '@/lib/api/vouchers';

function fmtTime(iso: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN');
}

// 兑换记录状态 -> 配色（兑换历史的 status 来自 voucher.Redemption.Status，多为 redeemed/已入账）。
function statusRing(status: string): string {
  const s = (status || '').toLowerCase();
  if (s === 'redeemed' || s === 'settled' || s === 'committed' || s === 'active') {
    return 'bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-950/40 dark:text-emerald-300 dark:ring-emerald-900/60';
  }
  if (s === 'pending' || s === 'reserving') {
    return 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-950/40 dark:text-amber-300 dark:ring-amber-900/60';
  }
  if (s === 'failed' || s === 'reversed' || s === 'revoked') {
    return 'bg-red-50 text-red-700 ring-red-200 dark:bg-red-950/40 dark:text-red-300 dark:ring-red-900/60';
  }
  return 'bg-accent-100 text-accent-600 ring-accent-200 dark:bg-accent-800 dark:text-accent-300 dark:ring-accent-700';
}

function statusLabel(status: string): string {
  const s = (status || '').toLowerCase();
  switch (s) {
    case 'redeemed':
    case 'settled':
    case 'committed':
      return '已入账';
    case 'active':
      return '生效中';
    case 'pending':
    case 'reserving':
      return '处理中';
    case 'failed':
      return '失败';
    case 'reversed':
      return '已回退';
    case 'revoked':
      return '已作废';
    default:
      return status || '—';
  }
}

export default function RedeemPage() {
  const [code, setCode] = useState('');
  const [redeeming, setRedeeming] = useState(false);
  const [result, setResult] = useState<RedeemResult | null>(null);
  const [error, setError] = useState<string | null>(null);

  const [history, setHistory] = useState<RedemptionHistoryItem[]>([]);
  const [historyLoading, setHistoryLoading] = useState(true);
  const [historyError, setHistoryError] = useState<string | null>(null);

  const loadHistory = useCallback(async () => {
    setHistoryLoading(true);
    setHistoryError(null);
    try {
      const rows = await fetchRedemptionHistory(50);
      setHistory(rows);
    } catch (err) {
      setHistoryError(friendlyMessage(err));
    } finally {
      setHistoryLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadHistory();
  }, [loadHistory]);

  const handleRedeem = useCallback(async () => {
    const trimmed = code.trim();
    if (!trimmed || redeeming) return;
    setRedeeming(true);
    setError(null);
    setResult(null);
    try {
      const res = await redeemVoucher({ code: trimmed });
      setResult(res);
      setCode('');
      // 兑换成功后刷新历史，让新记录出现在表里。
      void loadHistory();
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setRedeeming(false);
    }
  }, [code, redeeming, loadHistory]);

  // 成功提示文案：区分订阅券 / 余额券，幂等命中时提示「已兑换过」。
  function successText(res: RedeemResult): string {
    const amount = formatAmount(res.voucher.amount_cents, res.voucher.currency_code);
    if (res.subscription) {
      const kind = res.subscription.result_kind === 'renewed' ? '续期' : '开通';
      const days = res.subscription.applied_validity_days;
      const dayText = days > 0 ? `，有效期 ${days} 天` : '';
      return res.idempotent
        ? `该订阅券已兑换过，订阅状态未重复变更（${kind}${dayText}）。`
        : `订阅${kind}成功${dayText}。`;
    }
    const balance = formatAmount(res.balance_cents, res.voucher.currency_code);
    return res.idempotent
      ? `该兑换码此前已兑换，未重复入账。当前余额 ${balance}。`
      : `兑换成功，到账 ${amount}，当前余额 ${balance}。`;
  }

  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-bold text-accent-950 dark:text-white">兑换码</h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          输入兑换码为账户充值或开通订阅。兑换走会话鉴权，到账即时生效。
        </p>
      </div>

      {/* 兑换输入条 */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardContent className="flex flex-col gap-3 p-4 sm:flex-row sm:items-center">
          <div className="flex items-center gap-2 text-sm font-medium text-accent-600 dark:text-accent-300">
            <Ticket className="size-4 text-primary-600 dark:text-primary-300" />
            兑换码
          </div>
          <input
            type="text"
            value={code}
            onChange={(e) => setCode(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') void handleRedeem();
            }}
            placeholder="请输入兑换码"
            autoComplete="off"
            spellCheck={false}
            className="min-w-0 flex-1 rounded-lg border border-accent-200 bg-white px-3 py-2 font-mono text-sm tracking-wide text-accent-900 outline-none focus:border-primary-400 focus:ring-2 focus:ring-primary-100 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-100 dark:focus:ring-primary-900/40"
          />
          <Button onClick={() => void handleRedeem()} size="sm" disabled={redeeming || code.trim() === ''}>
            {redeeming ? <Loader2 className="size-4 animate-spin" /> : <Gift className="size-4" />}
            立即兑换
          </Button>
        </CardContent>
      </Card>

      {/* 成功 / 错误态 */}
      {result && (
        <div className="flex items-start gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">
          <CheckCircle2 className="mt-0.5 size-4 shrink-0" />
          <div className="flex flex-col gap-0.5">
            <span className="font-medium">{successText(result)}</span>
            <span className="text-xs text-emerald-600/80 dark:text-emerald-400/80">
              类型：{grantKindLabel(result.voucher.grant_kind)} · 面额 {formatAmount(result.voucher.amount_cents, result.voucher.currency_code)}
            </span>
          </div>
        </div>
      )}

      {error && (
        <div className="flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
          <AlertCircle className="mt-0.5 size-4 shrink-0" />
          <span>{error}</span>
        </div>
      )}

      {/* 兑换历史 */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-row items-center justify-between p-5 pb-3">
          <CardTitle className="text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            兑换历史
          </CardTitle>
          <Button onClick={() => void loadHistory()} size="sm" variant="outline" disabled={historyLoading}>
            <RefreshCw className={historyLoading ? 'size-4 animate-spin' : 'size-4'} />
            刷新
          </Button>
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {historyError ? (
            <div className="flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              <span>{historyError}</span>
            </div>
          ) : historyLoading ? (
            <div className="flex items-center justify-center gap-2 py-10 text-sm text-accent-400">
              <Loader2 className="size-4 animate-spin" /> 加载中…
            </div>
          ) : history.length === 0 ? (
            <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
              暂无兑换记录。兑换一张码后会显示在这里。
            </div>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>时间</TableHead>
                    <TableHead>类型</TableHead>
                    <TableHead>面额</TableHead>
                    <TableHead>状态</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {history.map((row, i) => {
                    // 历史接口不返回 grant_kind；用 billing_event_id 区分余额券(有事件)与订阅券(为 0)。
                    const kind = row.billing_event_id > 0 ? '余额券' : '订阅券';
                    return (
                      <TableRow key={`${row.voucher_id}-${row.redeemed_at}-${i}`}>
                        <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                          {fmtTime(row.redeemed_at)}
                        </TableCell>
                        <TableCell className="text-xs text-accent-600 dark:text-accent-300">{kind}</TableCell>
                        <TableCell className="font-mono tabular-nums text-accent-900 dark:text-accent-100">
                          {formatAmount(row.amount_cents, row.currency_code)}
                        </TableCell>
                        <TableCell>
                          <span
                            className={`inline-flex items-center rounded-md px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${statusRing(row.status)}`}
                          >
                            {statusLabel(row.status)}
                          </span>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
