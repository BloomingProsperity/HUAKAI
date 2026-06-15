'use client';

// 定价（public）页：模型费率表 + 可选历史版本切换。
// 数据来自三个 public 端点（lib/api/pricing.ts 注明形状/鉴权/503）：
//   /v1/pricing/page      —— 当前客户面单位价表（输入/输出价 + context）。
//   /v1/pricing/snapshots —— 历史 version 列表（驱动版本切换器）。
//   /v1/pricing/rate-table?version=X —— 选中版本的完整费率（含 cache 价 / 倍率,page 端点不暴露）。
// 每 section 独立 503/空态容错,任一不可用不拖垮整页。

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertCircle,
  Coins,
  Database,
  History,
  Loader2,
  RefreshCw,
  Search,
  Tag,
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
  fetchPricingPage,
  fetchPricingSnapshots,
  fetchRateTable,
  fmtContext,
  fmtSnapshotTime,
  microUsdPerMillion,
  ownerFromCanonical,
  parseRateTableModels,
  perTokenToPerMillion,
  type PricingPageItem,
  type RateTableModelRow,
  type RateTableSnapshot,
} from '@/lib/api/pricing';

// section 状态机：loading → ok | empty | error（503/网络等统一进 error,显示「暂不可用」）。
type SectionState = 'loading' | 'ok' | 'empty' | 'error';

export default function PricingPage() {
  // —— 当前价表 section ——
  const [items, setItems] = useState<PricingPageItem[]>([]);
  const [pageState, setPageState] = useState<SectionState>('loading');
  const [pageError, setPageError] = useState<string | null>(null);

  // —— 历史版本 section ——
  const [snapshots, setSnapshots] = useState<RateTableSnapshot[]>([]);
  const [snapState, setSnapState] = useState<SectionState>('loading');

  // —— 选中版本的费率明细（含 cache 价/倍率）——
  const [selectedVersion, setSelectedVersion] = useState<string>('');
  const [rateRows, setRateRows] = useState<RateTableModelRow[]>([]);
  const [rateState, setRateState] = useState<SectionState>('loading');
  const [rateError, setRateError] = useState<string | null>(null);

  // —— 搜索 ——
  const [query, setQuery] = useState('');

  const loadPage = useCallback(async () => {
    setPageState('loading');
    setPageError(null);
    try {
      const data = await fetchPricingPage();
      setItems(data);
      setPageState(data.length === 0 ? 'empty' : 'ok');
    } catch (err) {
      setPageError(friendlyMessage(err));
      setPageState('error');
    }
  }, []);

  const loadSnapshots = useCallback(async () => {
    setSnapState('loading');
    try {
      const data = await fetchPricingSnapshots();
      setSnapshots(data);
      setSnapState(data.length === 0 ? 'empty' : 'ok');
      // 默认选「当前生效」版本（effective_to 为空）；没有则取第一条。
      const current = data.find((s) => !s.effective_to) ?? data[0];
      if (current) setSelectedVersion((prev) => prev || current.version);
    } catch {
      // 历史版本不可用不影响主表；静默降级为 error 态。
      setSnapState('error');
    }
  }, []);

  const loadRateTable = useCallback(async (version: string) => {
    if (!version) return;
    setRateState('loading');
    setRateError(null);
    try {
      const table = await fetchRateTable(version);
      const rows = parseRateTableModels(table.pricing_data);
      setRateRows(rows);
      setRateState(rows.length === 0 ? 'empty' : 'ok');
    } catch (err) {
      setRateError(friendlyMessage(err));
      setRateState('error');
    }
  }, []);

  useEffect(() => {
    void loadPage();
    void loadSnapshots();
  }, [loadPage, loadSnapshots]);

  useEffect(() => {
    if (selectedVersion) void loadRateTable(selectedVersion);
  }, [selectedVersion, loadRateTable]);

  const filteredItems = useMemo(() => {
    const q = query.trim().toLowerCase();
    const sorted = [...items].sort((a, b) => a.model.localeCompare(b.model));
    if (!q) return sorted;
    return sorted.filter(
      (it) =>
        it.model.toLowerCase().includes(q) ||
        (it.canonical_id ?? '').toLowerCase().includes(q),
    );
  }, [items, query]);

  const filteredRateRows = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return rateRows;
    return rateRows.filter((r) => r.model.toLowerCase().includes(q));
  }, [rateRows, query]);

  const selectedSnapshot = useMemo(
    () => snapshots.find((s) => s.version === selectedVersion),
    [snapshots, selectedVersion],
  );

  const refreshAll = useCallback(() => {
    void loadPage();
    void loadSnapshots();
    if (selectedVersion) void loadRateTable(selectedVersion);
  }, [loadPage, loadSnapshots, loadRateTable, selectedVersion]);

  const anyLoading = pageState === 'loading' || rateState === 'loading';

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-5">
      {/* 标题 */}
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-bold text-accent-950 dark:text-white">模型定价</h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          各模型的客户面单位价（每 100 万 token 美元）。价格公开,无需登录即可查看；可按版本切换查看历史费率与缓存价。
        </p>
      </div>

      {/* 搜索 + 刷新 */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardContent className="flex flex-col gap-3 p-4 sm:flex-row sm:items-center">
          <div className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-accent-400" />
            <input
              type="text"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="搜索模型名或提供方…"
              className="w-full rounded-lg border border-accent-200 bg-white py-2 pl-9 pr-3 text-sm text-accent-900 outline-none focus:border-primary-400 focus:ring-2 focus:ring-primary-100 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-100 dark:focus:ring-primary-900/40"
            />
          </div>
          <Button onClick={refreshAll} size="sm" variant="outline" disabled={anyLoading}>
            <RefreshCw className={anyLoading ? 'animate-spin' : ''} />
            刷新
          </Button>
        </CardContent>
      </Card>

      {/* 当前价表 */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-row items-center justify-between p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <Coins className="size-4 text-primary-600 dark:text-primary-300" />
            当前模型费率
          </CardTitle>
          {pageState === 'ok' && (
            <span className="text-[11px] text-accent-400 dark:text-accent-500">
              共 {filteredItems.length} / {items.length} 个模型
            </span>
          )}
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {pageState === 'loading' ? (
            <LoadingRow />
          ) : pageState === 'error' ? (
            <UnavailableRow message={pageError} />
          ) : pageState === 'empty' ? (
            <EmptyRow message="暂无公开费率。运营方配置并发布公开价表后,这里会列出可用模型及其单位价。" />
          ) : filteredItems.length === 0 ? (
            <EmptyRow message="没有匹配的模型,换个关键词试试。" />
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>模型</TableHead>
                    <TableHead>提供方</TableHead>
                    <TableHead className="text-right">输入 / 1M</TableHead>
                    <TableHead className="text-right">输出 / 1M</TableHead>
                    <TableHead className="text-right">上下文</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filteredItems.map((it) => (
                    <TableRow key={it.canonical_id || it.model}>
                      <TableCell>
                        <div className="font-medium text-accent-900 dark:text-accent-100">{it.model}</div>
                        {it.canonical_id && it.canonical_id !== it.model && (
                          <div className="font-mono text-[10px] text-accent-400 dark:text-accent-600">{it.canonical_id}</div>
                        )}
                      </TableCell>
                      <TableCell>
                        <span className="inline-flex items-center gap-1 rounded-md bg-accent-100 px-2 py-0.5 text-xs font-medium text-accent-600 ring-1 ring-inset ring-accent-200 dark:bg-accent-800 dark:text-accent-300 dark:ring-accent-700">
                          <Tag className="size-3" />
                          {ownerFromCanonical(it.canonical_id, it.model)}
                        </span>
                      </TableCell>
                      <TableCell className="text-right font-mono tabular-nums text-accent-900 dark:text-accent-100">
                        {perTokenToPerMillion(it.input_price_per_token)}
                      </TableCell>
                      <TableCell className="text-right font-mono tabular-nums text-accent-900 dark:text-accent-100">
                        {perTokenToPerMillion(it.output_price_per_token)}
                      </TableCell>
                      <TableCell className="text-right tabular-nums text-accent-600 dark:text-accent-300">
                        {fmtContext(it.context_length)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>

      {/* 历史版本费率明细（含缓存价 / 倍率）—— snapshots 可用时才展示版本切换器 */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-col gap-3 p-5 pb-3 sm:flex-row sm:items-center sm:justify-between">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <Database className="size-4 text-primary-600 dark:text-primary-300" />
            费率明细 · 按版本
          </CardTitle>
          {snapState === 'ok' && snapshots.length > 0 && (
            <div className="flex items-center gap-2">
              <History className="size-4 text-accent-400" />
              <select
                value={selectedVersion}
                onChange={(e) => setSelectedVersion(e.target.value)}
                className="rounded-lg border border-accent-200 bg-white px-3 py-1.5 text-sm text-accent-900 outline-none focus:border-primary-400 focus:ring-2 focus:ring-primary-100 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-100 dark:focus:ring-primary-900/40"
              >
                {snapshots.map((s) => (
                  <option key={s.id} value={s.version}>
                    {s.version}
                    {!s.effective_to ? '（当前）' : ''}
                  </option>
                ))}
              </select>
            </div>
          )}
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {selectedSnapshot && (
            <p className="mb-3 text-[11px] text-accent-400 dark:text-accent-500">
              生效自 {fmtSnapshotTime(selectedSnapshot.effective_from)}
              {selectedSnapshot.effective_to
                ? ` 至 ${fmtSnapshotTime(selectedSnapshot.effective_to)}`
                : '（仍生效）'}
              。该视图展示原始费率（每 1M token 美元）,含缓存价与分组倍率（若有）。
            </p>
          )}
          {snapState === 'error' ? (
            <UnavailableRow message="历史版本暂不可用。" />
          ) : snapState === 'empty' ? (
            <EmptyRow message="暂无历史费率版本。" />
          ) : rateState === 'loading' || snapState === 'loading' ? (
            <LoadingRow />
          ) : rateState === 'error' ? (
            <UnavailableRow message={rateError} />
          ) : rateState === 'empty' ? (
            <EmptyRow message="该版本未配置可识别的模型费率。" />
          ) : filteredRateRows.length === 0 ? (
            <EmptyRow message="没有匹配的模型,换个关键词试试。" />
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>模型</TableHead>
                    <TableHead className="text-right">输入 / 1M</TableHead>
                    <TableHead className="text-right">输出 / 1M</TableHead>
                    <TableHead className="text-right">缓存读 / 1M</TableHead>
                    <TableHead className="text-right">缓存写 / 1M</TableHead>
                    <TableHead className="text-right">倍率</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filteredRateRows.map((r) => (
                    <TableRow key={r.model}>
                      <TableCell className="font-medium text-accent-900 dark:text-accent-100">{r.model}</TableCell>
                      <TableCell className="text-right font-mono tabular-nums text-accent-900 dark:text-accent-100">
                        {microUsdPerMillion(r.inputMicroUsd)}
                      </TableCell>
                      <TableCell className="text-right font-mono tabular-nums text-accent-900 dark:text-accent-100">
                        {microUsdPerMillion(r.outputMicroUsd)}
                      </TableCell>
                      <TableCell className="text-right font-mono tabular-nums text-accent-500 dark:text-accent-400">
                        {microUsdPerMillion(r.cacheReadMicroUsd)}
                      </TableCell>
                      <TableCell className="text-right font-mono tabular-nums text-accent-500 dark:text-accent-400">
                        {microUsdPerMillion(r.cacheWriteMicroUsd)}
                      </TableCell>
                      <TableCell className="text-right tabular-nums text-accent-600 dark:text-accent-300">
                        {r.multiplier !== undefined ? `×${r.multiplier}` : '—'}
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

// —— 复用的 section 内联状态行 ——

function LoadingRow() {
  return (
    <div className="flex items-center justify-center gap-2 py-10 text-sm text-accent-400">
      <Loader2 className="size-4 animate-spin" /> 加载中…
    </div>
  );
}

function EmptyRow({ message }: { message: string }) {
  return (
    <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
      {message}
    </div>
  );
}

function UnavailableRow({ message }: { message: string | null }) {
  return (
    <div className="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-700 dark:border-amber-900/60 dark:bg-amber-950/40 dark:text-amber-300">
      <AlertCircle className="mt-0.5 size-4 shrink-0" />
      <span>{message || '该模块暂不可用,请稍后再试。'}</span>
    </div>
  );
}
