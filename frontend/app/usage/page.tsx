'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertCircle,
  BarChart3,
  Coins,
  Hash,
  KeyRound,
  Loader2,
  RefreshCw,
  Wallet,
} from 'lucide-react';
import { StatCard } from '@/components/dashboard/StatCard';
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
  aggregateTimeSeries,
  defaultWindow,
  fetchQuota,
  fetchTimeSeries,
  fetchUsage,
  getStoredApiKey,
  storeApiKey,
  type MeUsageRecord,
  type QuotaWindow,
  type TrendPoint,
  type UsageTotals,
} from '@/lib/api/usage';
import { QuotaWindowsCard } from './QuotaWindowsCard';
import { UsageTrendChart } from './UsageTrendChart';

const EMPTY_TOTALS: UsageTotals = { total_cost: 0, total_tokens: 0, total_requests: 0, today_cost: 0 };

function fmtCost(v: number): string {
  return `$${v.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 4 })}`;
}
function fmtNum(v: number): string {
  return v.toLocaleString('zh-CN');
}
function fmtTime(iso: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN');
}

function statusTone(status: string): 'emerald' | 'amber' | 'red' | 'slate' {
  if (status === 'committed' || status === 'settled') return 'emerald';
  if (status === 'reserving' || status === 'pending') return 'amber';
  if (status === 'aborted' || status === 'failed') return 'red';
  return 'slate';
}

export default function UsagePage() {
  const [apiKey, setApiKey] = useState('');
  const [keyDraft, setKeyDraft] = useState('');

  const [quota, setQuota] = useState<QuotaWindow[]>([]);
  const [points, setPoints] = useState<TrendPoint[]>([]);
  const [totals, setTotals] = useState<UsageTotals>(EMPTY_TOTALS);
  const [records, setRecords] = useState<MeUsageRecord[]>([]);
  const [cursor, setCursor] = useState('');

  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const k = getStoredApiKey();
    setApiKey(k);
    setKeyDraft(k);
  }, []);

  const load = useCallback(async (key: string) => {
    setLoading(true);
    setError(null);
    try {
      // 额度走 session（与 key 无关），用量/趋势走 hk_ key。
      const quotaPromise = fetchQuota().then((r) => r.items).catch(() => [] as QuotaWindow[]);
      if (key) {
        const win = defaultWindow(30);
        const [q, ts, usage] = await Promise.all([
          quotaPromise,
          fetchTimeSeries(key, { ...win, granularity: 'day' }),
          fetchUsage(key, { limit: 20 }),
        ]);
        const agg = aggregateTimeSeries(ts);
        setQuota(q);
        setPoints(agg.points);
        setTotals(agg.totals);
        setRecords(usage.items);
        setCursor(usage.next_cursor);
      } else {
        setQuota(await quotaPromise);
        setPoints([]);
        setTotals(EMPTY_TOTALS);
        setRecords([]);
        setCursor('');
      }
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load(apiKey);
  }, [apiKey, load]);

  const loadMore = useCallback(async () => {
    if (!cursor || !apiKey) return;
    setLoadingMore(true);
    try {
      const more = await fetchUsage(apiKey, { limit: 20, cursor });
      setRecords((prev) => [...prev, ...more.items]);
      setCursor(more.next_cursor);
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setLoadingMore(false);
    }
  }, [cursor, apiKey]);

  function saveKey() {
    storeApiKey(keyDraft);
    setApiKey(keyDraft.trim());
  }

  const hasKey = apiKey !== '';
  const statCards = useMemo(
    () => [
      { title: '总花费', value: fmtCost(totals.total_cost), icon: Coins, tone: 'primary' as const, detail: '最近 30 天' },
      { title: '今日花费', value: fmtCost(totals.today_cost), icon: Wallet, tone: 'emerald' as const, detail: '当日累计' },
      { title: '总 Token', value: fmtNum(totals.total_tokens), icon: Hash, tone: 'blue' as const, detail: '输入+输出+缓存' },
      { title: '总请求', value: fmtNum(totals.total_requests), icon: BarChart3, tone: 'amber' as const, detail: '最近 30 天' },
    ],
    [totals],
  );

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-bold text-accent-950 dark:text-white">用量 & 额度</h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          额度窗口来自你的账户（会话鉴权）；用量明细与趋势按所选 API Key 统计。
        </p>
      </div>

      {/* hk_ key 输入条（与 Playground 共用 huakai_api_key） */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardContent className="flex flex-col gap-3 p-4 sm:flex-row sm:items-center">
          <div className="flex items-center gap-2 text-sm font-medium text-accent-600 dark:text-accent-300">
            <KeyRound className="size-4 text-primary-600 dark:text-primary-300" />
            API Key
          </div>
          <input
            type="password"
            value={keyDraft}
            onChange={(e) => setKeyDraft(e.target.value)}
            placeholder="hk_live_… 或 hk_test_…（用于拉取用量明细）"
            className="min-w-0 flex-1 rounded-lg border border-accent-200 bg-white px-3 py-2 text-sm text-accent-900 outline-none focus:border-primary-400 focus:ring-2 focus:ring-primary-100 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-100 dark:focus:ring-primary-900/40"
          />
          <Button onClick={saveKey} size="sm" disabled={keyDraft.trim() === apiKey}>
            保存并刷新
          </Button>
          <Button onClick={() => void load(apiKey)} size="sm" variant="outline" disabled={loading}>
            <RefreshCw className={loading ? 'animate-spin' : ''} />
            刷新
          </Button>
        </CardContent>
      </Card>

      {!hasKey && (
        <div className="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-700 dark:border-amber-900/60 dark:bg-amber-950/40 dark:text-amber-300">
          <AlertCircle className="mt-0.5 size-4 shrink-0" />
          <span>未填写 API Key,仅展示账户额度窗口。填入 hk_ 密钥后可看用量明细与趋势。还没有?去 <a href="/api-keys" className="font-medium underline">API Keys</a> 页创建。</span>
        </div>
      )}

      {error && (
        <div className="flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
          <AlertCircle className="mt-0.5 size-4 shrink-0" />
          <span>{error}</span>
        </div>
      )}

      {/* StatCards */}
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {statCards.map((s) => (
          <StatCard key={s.title} title={s.title} value={hasKey ? s.value : '—'} icon={s.icon} tone={s.tone} detail={s.detail} />
        ))}
      </div>

      {/* 额度 + 趋势 */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <QuotaWindowsCard windows={quota} />
        <UsageTrendChart data={points} isLoading={loading} />
      </div>

      {/* 用量明细表 */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="p-5 pb-3">
          <CardTitle className="text-base font-semibold tracking-normal text-accent-950 dark:text-white">用量明细</CardTitle>
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {loading ? (
            <div className="flex items-center justify-center gap-2 py-10 text-sm text-accent-400">
              <Loader2 className="size-4 animate-spin" /> 加载中…
            </div>
          ) : !hasKey ? (
            <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
              填入 API Key 后展示该 Key 的调用明细。
            </div>
          ) : records.length === 0 ? (
            <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
              该 Key 暂无用量记录。
            </div>
          ) : (
            <>
              <div className="overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>模型</TableHead>
                      <TableHead>输入 / 输出</TableHead>
                      <TableHead>花费</TableHead>
                      <TableHead>状态</TableHead>
                      <TableHead>时间</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {records.map((r, i) => (
                      <TableRow key={`${r.ledger_id}-${i}`}>
                        <TableCell>
                          <div className="font-medium text-accent-900 dark:text-accent-100">{r.requested_model || '—'}</div>
                          {r.upstream_model && r.upstream_model !== r.requested_model && (
                            <div className="text-[11px] text-accent-400">→ {r.upstream_model}</div>
                          )}
                        </TableCell>
                        <TableCell className="tabular-nums text-accent-600 dark:text-accent-300">
                          {fmtNum(r.tokens.input || 0)} / {fmtNum(r.tokens.output || 0)}
                        </TableCell>
                        <TableCell className="font-mono tabular-nums text-accent-900 dark:text-accent-100">
                          ${Number.parseFloat(r.actual_cost || '0').toFixed(6)}
                        </TableCell>
                        <TableCell>
                          <span className={`inline-flex items-center rounded-md px-2 py-0.5 text-xs font-medium ring-1 ring-inset ${toneRing(statusTone(r.status))}`}>
                            {r.status}
                          </span>
                        </TableCell>
                        <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">{fmtTime(r.created_at)}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
              {cursor && (
                <div className="mt-4 flex justify-center">
                  <Button variant="outline" size="sm" onClick={loadMore} disabled={loadingMore}>
                    {loadingMore ? <Loader2 className="size-4 animate-spin" /> : null}
                    加载更多
                  </Button>
                </div>
              )}
            </>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function toneRing(tone: 'emerald' | 'amber' | 'red' | 'slate'): string {
  switch (tone) {
    case 'emerald':
      return 'bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-950/40 dark:text-emerald-300 dark:ring-emerald-900/60';
    case 'amber':
      return 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-950/40 dark:text-amber-300 dark:ring-amber-900/60';
    case 'red':
      return 'bg-red-50 text-red-700 ring-red-200 dark:bg-red-950/40 dark:text-red-300 dark:ring-red-900/60';
    default:
      return 'bg-accent-100 text-accent-600 ring-accent-200 dark:bg-accent-800 dark:text-accent-300 dark:ring-accent-700';
  }
}
