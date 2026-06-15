'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertCircle,
  BarChart3,
  CheckCircle2,
  Coins,
  Hash,
  KeyRound,
  Loader2,
  RefreshCw,
  Wallet,
} from 'lucide-react';
import { StatCard } from '@/components/dashboard/StatCard';
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
import { ApiError } from '@/lib/api/client';
import { friendlyMessage } from '@/lib/api/errors';
import {
  aggregateByModel,
  aggregateTimeSeries,
  downloadUsageExport,
  fetchQuota,
  fetchTimeSeries,
  fetchUsage,
  getStoredApiKey,
  MAX_TIMESERIES_DAYS,
  rangeFromDates,
  storeApiKey,
  toDateInput,
  type ExportFormat,
  type MeUsageRecord,
  type ModelStatRow,
  type QuotaWindow,
  type TrendPoint,
  type UsageGranularity,
  type UsageTotals,
} from '@/lib/api/usage';
import { ModelStatsCard } from './ModelStatsCard';
import { QuotaWindowsCard } from './QuotaWindowsCard';
import { RANGE_PRESETS, UsageFilterBar } from './UsageFilterBar';
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
  if (status === 'reserving' || status === 'pending' || status === 'pending_reconciliation') return 'amber';
  if (status === 'aborted' || status === 'failed') return 'red';
  return 'slate';
}

// 初始默认区间：最近 30 天（贴近 31 天窗口上限）。
function initialRange(): { fromDate: string; toDate: string } {
  const to = new Date();
  const from = new Date(to.getTime() - 30 * 24 * 60 * 60 * 1000);
  return { fromDate: toDateInput(from), toDate: toDateInput(to) };
}

function daysBetween(fromDate: string, toDate: string): number {
  const from = new Date(`${fromDate}T00:00:00`);
  const to = new Date(`${toDate}T00:00:00`);
  return Math.round((to.getTime() - from.getTime()) / (24 * 60 * 60 * 1000)) + 1;
}

export default function UsagePage() {
  const [apiKey, setApiKey] = useState('');
  const [keyDraft, setKeyDraft] = useState('');

  const init = initialRange();
  const [fromDate, setFromDate] = useState(init.fromDate);
  const [toDate, setToDate] = useState(init.toDate);
  const [granularity, setGranularity] = useState<UsageGranularity>('day');
  const [presetDays, setPresetDays] = useState<number | null>(30);

  const [quota, setQuota] = useState<QuotaWindow[]>([]);
  const [points, setPoints] = useState<TrendPoint[]>([]);
  const [modelRows, setModelRows] = useState<ModelStatRow[]>([]);
  const [totals, setTotals] = useState<UsageTotals>(EMPTY_TOTALS);
  const [records, setRecords] = useState<MeUsageRecord[]>([]);
  const [cursor, setCursor] = useState('');

  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    const k = getStoredApiKey();
    setApiKey(k);
    setKeyDraft(k);
  }, []);

  // 当前 RFC3339 窗口（受日期选择器驱动），夹紧到 31 天上限以避免后端 400。
  const rangeTooLong = daysBetween(fromDate, toDate) > MAX_TIMESERIES_DAYS;
  const window = useMemo(() => rangeFromDates(fromDate, toDate), [fromDate, toDate]);

  const load = useCallback(
    async (key: string, win: { from: string; to: string }, gran: UsageGranularity) => {
      setLoading(true);
      setError(null);
      try {
        // 额度走 session（与 key 无关），用量/趋势/模型维度走 hk_ key。
        const quotaPromise = fetchQuota().then((r) => r.items).catch(() => [] as QuotaWindow[]);
        if (key) {
          const [q, ts, usage] = await Promise.all([
            quotaPromise,
            fetchTimeSeries(key, { ...win, granularity: gran }),
            fetchUsage(key, { limit: 20, from: win.from, to: win.to }),
          ]);
          const agg = aggregateTimeSeries(ts);
          const models = aggregateByModel(ts);
          setQuota(q);
          setPoints(agg.points);
          setTotals(agg.totals);
          setModelRows(models.rows);
          setRecords(usage.items);
          setCursor(usage.next_cursor);
        } else {
          setQuota(await quotaPromise);
          setPoints([]);
          setTotals(EMPTY_TOTALS);
          setModelRows([]);
          setRecords([]);
          setCursor('');
        }
      } catch (err) {
        setError(friendlyMessage(err));
      } finally {
        setLoading(false);
      }
    },
    [],
  );

  useEffect(() => {
    if (rangeTooLong) {
      setLoading(false);
      return;
    }
    void load(apiKey, window, granularity);
  }, [apiKey, window, granularity, rangeTooLong, load]);

  const loadMore = useCallback(async () => {
    if (!cursor || !apiKey) return;
    setLoadingMore(true);
    try {
      const more = await fetchUsage(apiKey, { limit: 20, cursor, from: window.from, to: window.to });
      setRecords((prev) => [...prev, ...more.items]);
      setCursor(more.next_cursor);
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setLoadingMore(false);
    }
  }, [cursor, apiKey, window]);

  function saveKey() {
    storeApiKey(keyDraft);
    setApiKey(keyDraft.trim());
  }

  function applyPreset(days: number) {
    const to = new Date();
    const from = new Date(to.getTime() - (days - 1) * 24 * 60 * 60 * 1000);
    setFromDate(toDateInput(from));
    setToDate(toDateInput(to));
    setPresetDays(days);
  }

  const handleExport = useCallback(
    async (format: ExportFormat) => {
      setExporting(true);
      setError(null);
      setNotice(null);
      try {
        await downloadUsageExport({ from: window.from, to: window.to, format });
        setNotice(`已导出 ${format.toUpperCase()}（账户范围，所选时间窗口）。`);
      } catch (err) {
        // export_truncated 是「文件已下载但被截断」的提示，不是失败。
        if (err instanceof ApiError && err.code === 'export_truncated') {
          setNotice(err.message);
        } else {
          setError(friendlyMessage(err));
        }
      } finally {
        setExporting(false);
      }
    },
    [window],
  );

  const hasKey = apiKey !== '';
  const statCards = useMemo(
    () => [
      { title: '总花费', value: fmtCost(totals.total_cost), icon: Coins, tone: 'primary' as const, detail: '所选时间窗口' },
      { title: '今日花费', value: fmtCost(totals.today_cost), icon: Wallet, tone: 'emerald' as const, detail: '当日累计' },
      { title: '总 Token', value: fmtNum(totals.total_tokens), icon: Hash, tone: 'blue' as const, detail: '输入+输出+缓存' },
      { title: '总请求', value: fmtNum(totals.total_requests), icon: BarChart3, tone: 'amber' as const, detail: '所选时间窗口' },
    ],
    [totals],
  );

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-bold text-accent-950 dark:text-white">用量 &amp; 额度</h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          额度窗口与导出来自你的账户（会话鉴权）；用量明细、趋势与模型维度按所选 API Key 统计。
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
          <Button onClick={() => void load(apiKey, window, granularity)} size="sm" variant="outline" disabled={loading}>
            <RefreshCw className={loading ? 'animate-spin' : ''} />
            刷新
          </Button>
        </CardContent>
      </Card>

      {!hasKey && (
        <div className="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-700 dark:border-amber-900/60 dark:bg-amber-950/40 dark:text-amber-300">
          <AlertCircle className="mt-0.5 size-4 shrink-0" />
          <span>未填写 API Key,仅展示账户额度窗口。填入 hk_ 密钥后可看用量明细、趋势与模型维度。还没有?去 <a href="/api-keys" className="font-medium underline">API Keys</a> 页创建。</span>
        </div>
      )}

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

      {/* 时间范围 + 粒度 + 导出 */}
      <UsageFilterBar
        fromDate={fromDate}
        toDate={toDate}
        granularity={granularity}
        presetDays={presetDays}
        exporting={exporting}
        canExport
        loading={loading}
        onPreset={applyPreset}
        onFromChange={(v) => {
          setFromDate(v);
          setPresetDays(null);
        }}
        onToChange={(v) => {
          setToDate(v);
          setPresetDays(null);
        }}
        onGranularity={setGranularity}
        onExport={handleExport}
      />

      {rangeTooLong && (
        <div className="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-700 dark:border-amber-900/60 dark:bg-amber-950/40 dark:text-amber-300">
          <AlertCircle className="mt-0.5 size-4 shrink-0" />
          <span>
            趋势与模型维度的时间窗口最长 {MAX_TIMESERIES_DAYS} 天，请缩小范围后再查看（可用上方快捷区间，如
            <button type="button" className="mx-1 font-medium underline" onClick={() => applyPreset(RANGE_PRESETS[3].days)}>
              30 天
            </button>
            ）。
          </span>
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
        <UsageTrendChart data={points} isLoading={loading && !rangeTooLong} />
      </div>

      {/* 模型维度 */}
      <ModelStatsCard rows={modelRows} isLoading={loading && !rangeTooLong} hasKey={hasKey} />

      {/* 用量明细表 */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-row items-center justify-between p-5 pb-3">
          <CardTitle className="text-base font-semibold tracking-normal text-accent-950 dark:text-white">用量明细</CardTitle>
          {hasKey && records.length > 0 && (
            <span className="text-[11px] text-accent-400 dark:text-accent-500">按所选时间窗口过滤</span>
          )}
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {loading && !rangeTooLong ? (
            <div className="flex items-center justify-center gap-2 py-10 text-sm text-accent-400">
              <Loader2 className="size-4 animate-spin" /> 加载中…
            </div>
          ) : !hasKey ? (
            <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
              填入 API Key 后展示该 Key 的调用明细。
            </div>
          ) : records.length === 0 ? (
            <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
              该 Key 在所选窗口内暂无用量记录。
            </div>
          ) : (
            <>
              <div className="overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>模型</TableHead>
                      <TableHead>提供方</TableHead>
                      <TableHead>输入 / 输出</TableHead>
                      <TableHead>缓存</TableHead>
                      <TableHead>花费</TableHead>
                      <TableHead>状态</TableHead>
                      <TableHead>时间</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {records.map((r, i) => {
                      const cacheRead = r.tokens.cache_read || 0;
                      const cacheCreation = r.tokens.cache_creation || 0;
                      const cacheTotal = cacheRead + cacheCreation;
                      return (
                        <TableRow key={`${r.ledger_id}-${i}`}>
                          <TableCell>
                            <div className="font-medium text-accent-900 dark:text-accent-100">{r.requested_model || '—'}</div>
                            {r.upstream_model && r.upstream_model !== r.requested_model && (
                              <div className="text-[11px] text-accent-400">→ {r.upstream_model}</div>
                            )}
                            {r.request_id && (
                              <div className="font-mono text-[10px] text-accent-300 dark:text-accent-600" title={r.request_id}>
                                {r.request_id.length > 18 ? `${r.request_id.slice(0, 18)}…` : r.request_id}
                              </div>
                            )}
                          </TableCell>
                          <TableCell className="text-xs text-accent-600 dark:text-accent-300">{r.provider || '—'}</TableCell>
                          <TableCell className="tabular-nums text-accent-600 dark:text-accent-300">
                            {fmtNum(r.tokens.input || 0)} / {fmtNum(r.tokens.output || 0)}
                          </TableCell>
                          <TableCell className="tabular-nums text-xs text-accent-500 dark:text-accent-400">
                            {cacheTotal > 0 ? fmtNum(cacheTotal) : '—'}
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
                      );
                    })}
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
