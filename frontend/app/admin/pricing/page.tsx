'use client';

// 分组定价倍率 admin 页 —— 管理 token 轨(@/lib/api/pricingRatios,走
// /admin/v1/pricing/ratios)。按 pool_group 维度设倍率覆盖(写入后端即精确失效
// ratio resolver 缓存,billing 立即生效)。补上接线审计 #1 的剩余半边:后端 CRUD
// 完整、前端缺面板。
//
// 注意:后端对 public_ratio=false 的私有倍率【不回显 ratio 值】(omitempty),故私有项
// 列表只显示"私有",编辑时需重新输入(后端设计如此,前端如实标注)。
// 借鉴(真 sha):new-api@1ac0f58 群组倍率独立页;sub2api@e34ad2b user-within-group 更细。
// HUAKAI delta:pool_group 粒度 + public_ratio + 显式缓存失效。

import { useCallback, useEffect, useMemo, useState } from 'react';
import { AlertCircle, CheckCircle2, Loader2, Pencil, Percent, Plus, RefreshCw, Trash2, X } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { friendlyMessage } from '@/lib/api/errors';
import { listPoolGroups } from '@/lib/api/pools';
import type { PoolGroup } from '@/lib/api/types';
import { deleteRatio, fmtDateTime, listRatios, upsertRatio, type PricingRatio } from '@/lib/api/pricingRatios';
import { cn } from '@/lib/utils';

const DEFAULT_TENANT_ID = 1;

export default function PricingRatiosPage() {
  const [tenantId, setTenantId] = useState<number>(DEFAULT_TENANT_ID);
  const [ratios, setRatios] = useState<PricingRatio[]>([]);
  const [pools, setPools] = useState<PoolGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [actionKey, setActionKey] = useState<string | null>(null);
  const [editTarget, setEditTarget] = useState<PricingRatio | null>(null);
  const [createOpen, setCreateOpen] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [r, p] = await Promise.all([
        listRatios(tenantId),
        listPoolGroups({ limit: 200 }).catch(() => ({ items: [] as PoolGroup[] })),
      ]);
      setRatios(r.items ?? []);
      setPools(p.items ?? []);
    } catch (err) {
      setError(friendlyMessage(err));
      setRatios([]);
    } finally {
      setLoading(false);
    }
  }, [tenantId]);

  useEffect(() => {
    void load();
  }, [load]);

  const poolName = useMemo(() => {
    const m = new Map<number, string>();
    pools.forEach((p) => m.set(p.id, p.name ?? `#${p.id}`));
    return m;
  }, [pools]);

  const handleDelete = useCallback(
    async (r: PricingRatio) => {
      if (typeof window !== 'undefined' && !window.confirm(`确认删除 pool ${r.pool_group_id} 的倍率覆盖?(将回退到默认 1.0)`)) return;
      setActionKey(`del-${r.pool_group_id}`);
      setError(null);
      setNotice(null);
      try {
        await deleteRatio(r.pool_group_id, tenantId);
        setNotice(`pool ${r.pool_group_id} 的倍率覆盖已删除。`);
        await load();
      } catch (err) {
        setError(friendlyMessage(err));
      } finally {
        setActionKey(null);
      }
    },
    [tenantId, load],
  );

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-bold text-accent-950 dark:text-white">定价倍率</h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          按资源池(pool)设倍率覆盖,作用于该 pool 的计费(默认 1.0)。写入后立即对 billing 生效。走管理 token。
        </p>
      </div>

      {error && <ErrorBanner message={error} />}
      {notice && <NoticeBanner message={notice} />}

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <Percent className="size-4 text-primary-600 dark:text-primary-300" />
            倍率覆盖
          </CardTitle>
          <div className="flex flex-wrap items-center gap-2">
            <div className="flex flex-col gap-1">
              <label className="text-[11px] text-accent-500 dark:text-accent-400">租户 ID</label>
              <input
                type="number"
                min={1}
                value={tenantId}
                onChange={(e) => setTenantId(Math.max(1, Number(e.target.value) || 1))}
                className="h-9 w-24 rounded-md border border-input bg-background px-3 text-sm tabular-nums"
              />
            </div>
            <Button size="sm" onClick={() => setCreateOpen(true)} className="self-end">
              <Plus />
              设置倍率
            </Button>
            <Button size="sm" variant="outline" onClick={() => void load()} disabled={loading} className="self-end">
              <RefreshCw className={cn(loading && 'animate-spin')} />
            </Button>
          </div>
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {loading && ratios.length === 0 ? (
            <CenterLoader text="加载倍率中…" />
          ) : ratios.length === 0 ? (
            <EmptyState text="当前租户暂无 pool 倍率覆盖(均按默认 1.0)。" />
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>资源池(pool)</TableHead>
                    <TableHead>倍率</TableHead>
                    <TableHead>可见性</TableHead>
                    <TableHead>更新者</TableHead>
                    <TableHead>更新时间</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {ratios.map((r) => {
                    const busy = actionKey === `del-${r.pool_group_id}`;
                    return (
                      <TableRow key={r.pool_group_id}>
                        <TableCell>
                          <div className="font-medium text-accent-900 dark:text-accent-100">
                            {poolName.get(r.pool_group_id) ?? `pool ${r.pool_group_id}`}
                          </div>
                          <div className="text-[11px] text-accent-400">#{r.pool_group_id}</div>
                        </TableCell>
                        <TableCell className="tabular-nums text-sm text-accent-800 dark:text-accent-100">
                          {r.public_ratio ? `${r.ratio ?? '—'}x` : <span className="text-accent-400">私有(值不回显)</span>}
                        </TableCell>
                        <TableCell>
                          <Badge variant={r.public_ratio ? 'default' : 'secondary'}>
                            {r.public_ratio ? '对用户公开' : '私有'}
                          </Badge>
                        </TableCell>
                        <TableCell className="text-xs text-accent-500 dark:text-accent-400">{r.updated_by || '—'}</TableCell>
                        <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                          {fmtDateTime(r.updated_at)}
                        </TableCell>
                        <TableCell>
                          <div className="flex items-center justify-end gap-1.5">
                            <Button size="sm" variant="ghost" onClick={() => { setEditTarget(r); setError(null); setNotice(null); }} disabled={actionKey !== null} title="编辑">
                              <Pencil />
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              onClick={() => void handleDelete(r)}
                              disabled={actionKey !== null}
                              title="删除(回退默认)"
                              className="text-red-600 hover:text-red-700 dark:text-red-400"
                            >
                              {busy ? <Loader2 className="size-4 animate-spin" /> : <Trash2 />}
                            </Button>
                          </div>
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

      {createOpen && (
        <RatioFormModal
          tenantId={tenantId}
          pools={pools}
          existingPoolIds={new Set(ratios.map((r) => r.pool_group_id))}
          onClose={() => setCreateOpen(false)}
          onDone={(msg) => { setCreateOpen(false); setNotice(msg); void load(); }}
        />
      )}
      {editTarget && (
        <RatioFormModal
          tenantId={tenantId}
          pools={pools}
          ratio={editTarget}
          onClose={() => setEditTarget(null)}
          onDone={(msg) => { setEditTarget(null); setNotice(msg); void load(); }}
        />
      )}
    </div>
  );
}

// ---- 设置 / 编辑倍率弹窗(PUT upsert) ----

function RatioFormModal({
  tenantId,
  pools,
  ratio,
  existingPoolIds,
  onClose,
  onDone,
}: {
  tenantId: number;
  pools: PoolGroup[];
  ratio?: PricingRatio;
  existingPoolIds?: Set<number>;
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const editing = ratio != null;
  const [poolID, setPoolID] = useState(ratio ? String(ratio.pool_group_id) : '');
  const [value, setValue] = useState(ratio?.ratio ?? ''); // 私有项 ratio 为空,需重输
  const [publicRatio, setPublicRatio] = useState(ratio?.public_ratio ?? true);
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  // 创建模式:只列还没设过倍率的 pool(已有的走编辑,避免误覆盖)。
  const selectablePools = editing ? pools : pools.filter((p) => !existingPoolIds?.has(p.id));

  async function submit() {
    const p = Number(poolID.trim());
    if (!Number.isInteger(p) || p <= 0) {
      setLocalError('请选择 pool。');
      return;
    }
    const v = Number(value.trim());
    if (!Number.isFinite(v) || v <= 0 || v > 100) {
      setLocalError('倍率需为 (0, 100] 的数字。');
      return;
    }
    setSubmitting(true);
    setLocalError(null);
    try {
      await upsertRatio(p, { ratio: value.trim(), public_ratio: publicRatio }, tenantId);
      onDone(`pool ${p} 倍率已设为 ${value.trim()}x。`);
    } catch (err) {
      setLocalError(friendlyMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <ModalShell title={editing ? '编辑倍率' : '设置倍率'} icon={<Percent className="size-4 text-primary-600 dark:text-primary-300" />} onClose={onClose}>
      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <label className="text-xs text-accent-500 dark:text-accent-400">资源池(pool)</label>
          <select value={poolID} disabled={editing} onChange={(e) => setPoolID(e.target.value)} className="h-9 rounded-md border border-input bg-background px-3 text-sm disabled:opacity-60">
            <option value="">选择 pool…</option>
            {selectablePools.map((p) => (
              <option key={p.id} value={p.id}>#{p.id} {p.name ?? ''}</option>
            ))}
          </select>
        </div>
        <div className="flex flex-col gap-1">
          <label className="text-xs text-accent-500 dark:text-accent-400">倍率(0, 100]</label>
          <input type="number" step="0.01" min={0} max={100} value={value} onChange={(e) => setValue(e.target.value)} placeholder={editing && !ratio?.ratio ? '私有项,请重新输入' : '如 0.95'} className="h-9 rounded-md border border-input bg-background px-3 text-sm tabular-nums" />
          {editing && !ratio?.ratio && <p className="text-[11px] text-amber-500">该项为私有,后端不回显旧值,保存即覆盖。</p>}
        </div>
        <label className="flex items-center gap-2 text-sm text-accent-700 dark:text-accent-200">
          <input type="checkbox" checked={publicRatio} onChange={(e) => setPublicRatio(e.target.checked)} />
          对终端用户公开此倍率(关=私有,定价页不展示)
        </label>
        {localError && <InlineError message={localError} />}
        <div className="flex justify-end gap-2 pt-1">
          <Button size="sm" variant="outline" onClick={onClose} disabled={submitting}>取消</Button>
          <Button size="sm" onClick={() => void submit()} disabled={submitting}>
            {submitting ? <Loader2 className="size-4 animate-spin" /> : <CheckCircle2 />}
            保存
          </Button>
        </div>
      </div>
    </ModalShell>
  );
}

// ---- 共享小组件(本页自带,沿用 admin 页约定) ----

function ErrorBanner({ message }: { message: string }) {
  return (
    <div className="flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
      <AlertCircle className="mt-0.5 size-4 shrink-0" />
      <span>{message}</span>
    </div>
  );
}

function NoticeBanner({ message }: { message: string }) {
  return (
    <div className="flex items-start gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">
      <CheckCircle2 className="mt-0.5 size-4 shrink-0" />
      <span>{message}</span>
    </div>
  );
}

function InlineError({ message }: { message: string }) {
  return (
    <div className="flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
      <AlertCircle className="mt-0.5 size-3.5 shrink-0" />
      <span>{message}</span>
    </div>
  );
}

function CenterLoader({ text }: { text: string }) {
  return (
    <div className="flex items-center justify-center gap-2 py-16 text-sm text-accent-400">
      <Loader2 className="size-5 animate-spin" /> {text}
    </div>
  );
}

function EmptyState({ text }: { text: string }) {
  return (
    <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
      {text}
    </div>
  );
}

function ModalShell({ title, icon, onClose, children }: { title: string; icon: React.ReactNode; onClose: () => void; children: React.ReactNode }) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onClick={onClose} role="presentation">
      <div className="w-full max-w-md rounded-xl border border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900" onClick={(e) => e.stopPropagation()} role="dialog" aria-modal="true">
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
