'use client';

// 模型→pool 绑定 admin 页 —— 管理 token 轨（@/lib/api/modelBindings，走顶层
// /admin/v1/model-pool-bindings）。补上"能力建了却够不着"的最后一环:之前列+resolver
// 都有、却没有 admin 写入路径,现在能在界面增删改一条 模型→pool 绑定。
//
// D3:pool 走下拉(listPoolGroups);model_id 暂手输(后端无 admin model-list,降级,见
// 计划 D3-B)。D5:selection_mode 暴露 priority_weighted 但灰标"加权执行未启用,当前按
// 优先级"(后端存而不执行,符合 Feature Preservation,不缩功能也不给错觉)。

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertCircle,
  CheckCircle2,
  Link2,
  Loader2,
  Pencil,
  Plus,
  Power,
  PowerOff,
  RefreshCw,
  Trash2,
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
import { listPoolGroups } from '@/lib/api/pools';
import type { PoolGroup } from '@/lib/api/types';
import {
  createBinding,
  deleteBinding,
  listBindings,
  updateBinding,
  fmtDateTime,
  FALLBACK_CLASS_LABEL,
  SELECTION_MODE_LABEL,
  type CreateBindingInput,
  type FallbackClass,
  type ModelPoolBinding,
  type SelectionMode,
} from '@/lib/api/modelBindings';
import { cn } from '@/lib/utils';

const DEFAULT_TENANT_ID = 1; // 单租户部署默认

export default function ModelBindingsPage() {
  const [tenantId, setTenantId] = useState<number>(DEFAULT_TENANT_ID);
  const [bindings, setBindings] = useState<ModelPoolBinding[]>([]);
  const [pools, setPools] = useState<PoolGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [actionKey, setActionKey] = useState<string | null>(null);
  const [modelFilter, setModelFilter] = useState('');
  const [editTarget, setEditTarget] = useState<ModelPoolBinding | null>(null);
  const [createOpen, setCreateOpen] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const modelID = modelFilter.trim() ? Number(modelFilter.trim()) : undefined;
      const [b, p] = await Promise.all([
        listBindings({ tenant_id: tenantId, model_id: Number.isFinite(modelID) ? modelID : undefined }),
        listPoolGroups({ limit: 200 }).catch(() => ({ items: [] as PoolGroup[] })),
      ]);
      setBindings(b.items ?? []);
      setPools(p.items ?? []);
    } catch (err) {
      setError(friendlyMessage(err));
      setBindings([]);
    } finally {
      setLoading(false);
    }
  }, [tenantId, modelFilter]);

  useEffect(() => {
    void load();
  }, [load]);

  const poolName = useMemo(() => {
    const m = new Map<number, string>();
    pools.forEach((p) => m.set(p.id, p.name ?? `#${p.id}`));
    return m;
  }, [pools]);

  const handleToggle = useCallback(
    async (b: ModelPoolBinding) => {
      setActionKey(`status-${b.id}`);
      setError(null);
      setNotice(null);
      try {
        await updateBinding(b.id, { enabled: !b.enabled }, tenantId);
        setNotice(`绑定 #${b.id} 已${b.enabled ? '停用' : '启用'}。`);
        await load();
      } catch (err) {
        setError(friendlyMessage(err));
      } finally {
        setActionKey(null);
      }
    },
    [tenantId, load],
  );

  const handleDelete = useCallback(
    async (b: ModelPoolBinding) => {
      if (typeof window !== 'undefined' && !window.confirm(`确认删除绑定 #${b.id}（model ${b.model_id} → pool ${b.pool_group_id}）？`)) return;
      setActionKey(`del-${b.id}`);
      setError(null);
      setNotice(null);
      try {
        await deleteBinding(b.id, tenantId);
        setNotice(`绑定 #${b.id} 已删除。`);
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
    <div className="mx-auto flex max-w-6xl flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-bold text-accent-950 dark:text-white">模型绑定</h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          管理 模型 → 资源池(pool) 的路由绑定:优先级 / 权重 / 选择模式 / 限额 / 回退类 / 生效窗。走管理 token。
        </p>
      </div>

      {error && <ErrorBanner message={error} />}
      {notice && <NoticeBanner message={notice} />}

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <Link2 className="size-4 text-primary-600 dark:text-primary-300" />
            绑定列表
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
            <div className="flex flex-col gap-1">
              <label className="text-[11px] text-accent-500 dark:text-accent-400">按 model_id 过滤</label>
              <input
                type="number"
                min={1}
                value={modelFilter}
                onChange={(e) => setModelFilter(e.target.value)}
                placeholder="全部"
                className="h-9 w-28 rounded-md border border-input bg-background px-3 text-sm tabular-nums"
              />
            </div>
            <Button size="sm" onClick={() => setCreateOpen(true)} className="self-end">
              <Plus />
              新建绑定
            </Button>
            <Button size="sm" variant="outline" onClick={() => void load()} disabled={loading} className="self-end">
              <RefreshCw className={cn(loading && 'animate-spin')} />
            </Button>
          </div>
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {loading && bindings.length === 0 ? (
            <CenterLoader text="加载绑定中…" />
          ) : bindings.length === 0 ? (
            <EmptyState text="当前租户暂无 模型→pool 绑定。" />
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>model → pool</TableHead>
                    <TableHead>优先级 / 权重</TableHead>
                    <TableHead>选择模式</TableHead>
                    <TableHead>回退类</TableHead>
                    <TableHead>限额(rpm/tpm/并发)</TableHead>
                    <TableHead>生效窗</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {bindings.map((b) => {
                    const busy = actionKey === `status-${b.id}` || actionKey === `del-${b.id}`;
                    return (
                      <TableRow key={b.id}>
                        <TableCell>
                          <div className="font-medium text-accent-900 dark:text-accent-100">
                            model {b.model_id} → {poolName.get(b.pool_group_id) ?? `pool ${b.pool_group_id}`}
                          </div>
                          <div className="text-[11px] text-accent-400">
                            #{b.id}
                            {b.provider_model_id_override ? ` · override: ${b.provider_model_id_override}` : ''}
                          </div>
                        </TableCell>
                        <TableCell className="tabular-nums text-sm text-accent-700 dark:text-accent-200">
                          {b.priority} / {b.weight}
                        </TableCell>
                        <TableCell className="text-xs text-accent-600 dark:text-accent-300">
                          {SELECTION_MODE_LABEL[b.selection_mode]}
                          {b.selection_mode === 'priority_weighted' && (
                            <span className="ml-1 text-[10px] text-amber-500" title="后端存储但加权执行尚未启用,当前按优先级路由">
                              (未启用)
                            </span>
                          )}
                        </TableCell>
                        <TableCell className="text-xs text-accent-600 dark:text-accent-300">
                          {FALLBACK_CLASS_LABEL[b.fallback_class]}
                        </TableCell>
                        <TableCell className="text-xs tabular-nums text-accent-500 dark:text-accent-400">
                          {b.rpm_limit ?? '—'} / {b.tpm_limit ?? '—'} / {b.max_parallel_requests ?? '—'}
                        </TableCell>
                        <TableCell className="whitespace-nowrap text-[11px] text-accent-500 dark:text-accent-400">
                          {b.effective_from || b.effective_until ? (
                            <>
                              {fmtDateTime(b.effective_from)}
                              <br />→ {fmtDateTime(b.effective_until)}
                            </>
                          ) : (
                            '永久'
                          )}
                        </TableCell>
                        <TableCell>
                          <Badge variant={b.enabled ? 'default' : 'destructive'}>
                            {b.enabled ? '启用' : '停用'}
                          </Badge>
                        </TableCell>
                        <TableCell>
                          <div className="flex items-center justify-end gap-1.5">
                            <Button
                              size="sm"
                              variant={b.enabled ? 'outline' : 'default'}
                              onClick={() => void handleToggle(b)}
                              disabled={actionKey !== null}
                              title={b.enabled ? '停用' : '启用'}
                            >
                              {busy && actionKey === `status-${b.id}` ? (
                                <Loader2 className="size-4 animate-spin" />
                              ) : b.enabled ? (
                                <PowerOff />
                              ) : (
                                <Power />
                              )}
                            </Button>
                            <Button size="sm" variant="ghost" onClick={() => { setEditTarget(b); setError(null); setNotice(null); }} disabled={actionKey !== null} title="编辑">
                              <Pencil />
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              onClick={() => void handleDelete(b)}
                              disabled={actionKey !== null}
                              title="删除"
                              className="text-red-600 hover:text-red-700 dark:text-red-400"
                            >
                              {busy && actionKey === `del-${b.id}` ? <Loader2 className="size-4 animate-spin" /> : <Trash2 />}
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
        <BindingFormModal
          tenantId={tenantId}
          pools={pools}
          onClose={() => setCreateOpen(false)}
          onDone={(msg) => {
            setCreateOpen(false);
            setNotice(msg);
            void load();
          }}
        />
      )}
      {editTarget && (
        <BindingFormModal
          tenantId={tenantId}
          pools={pools}
          binding={editTarget}
          onClose={() => setEditTarget(null)}
          onDone={(msg) => {
            setEditTarget(null);
            setNotice(msg);
            void load();
          }}
        />
      )}
    </div>
  );
}

// ---- 新建 / 编辑弹窗 ----

const SELECTION_MODES: SelectionMode[] = ['strict_priority', 'priority_weighted'];
const FALLBACK_CLASSES: FallbackClass[] = ['normal', 'context_window', 'safety', 'quota', 'manual'];

function BindingFormModal({
  tenantId,
  pools,
  binding,
  onClose,
  onDone,
}: {
  tenantId: number;
  pools: PoolGroup[];
  binding?: ModelPoolBinding;
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const editing = binding != null;
  const [modelID, setModelID] = useState(binding ? String(binding.model_id) : '');
  const [poolID, setPoolID] = useState(binding ? String(binding.pool_group_id) : '');
  const [priority, setPriority] = useState(binding ? String(binding.priority) : '100');
  const [weight, setWeight] = useState(binding ? String(binding.weight) : '1');
  const [selectionMode, setSelectionMode] = useState<SelectionMode>(binding?.selection_mode ?? 'strict_priority');
  const [fallbackClass, setFallbackClass] = useState<FallbackClass>(binding?.fallback_class ?? 'normal');
  const [override, setOverride] = useState(binding?.provider_model_id_override ?? '');
  const [rpm, setRpm] = useState(binding?.rpm_limit != null ? String(binding.rpm_limit) : '');
  const [tpm, setTpm] = useState(binding?.tpm_limit != null ? String(binding.tpm_limit) : '');
  const [parallel, setParallel] = useState(binding?.max_parallel_requests != null ? String(binding.max_parallel_requests) : '');
  const [enabled, setEnabled] = useState(binding?.enabled ?? true);
  const [reason, setReason] = useState(binding?.reason ?? '');
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  function optNum(s: string): number | undefined {
    const t = s.trim();
    if (t === '') return undefined;
    const n = Number(t);
    return Number.isFinite(n) ? n : undefined;
  }

  async function submit() {
    const m = Number(modelID.trim());
    const p = Number(poolID.trim());
    if (!editing && (!Number.isInteger(m) || m <= 0)) {
      setLocalError('model_id 需为正整数。');
      return;
    }
    if (!editing && (!Number.isInteger(p) || p <= 0)) {
      setLocalError('请选择 pool。');
      return;
    }
    const w = Number(weight.trim());
    if (!Number.isInteger(w) || w <= 0) {
      setLocalError('权重需为正整数。');
      return;
    }
    setSubmitting(true);
    setLocalError(null);
    const common = {
      priority: optNum(priority) ?? 100,
      weight: w,
      selection_mode: selectionMode,
      fallback_class: fallbackClass,
      provider_model_id_override: override.trim() || undefined,
      rpm_limit: optNum(rpm),
      tpm_limit: optNum(tpm),
      max_parallel_requests: optNum(parallel),
      enabled,
      reason: reason.trim() || undefined,
    };
    try {
      if (editing && binding) {
        await updateBinding(binding.id, common, tenantId);
        onDone(`绑定 #${binding.id} 已更新。`);
      } else {
        const input: CreateBindingInput = { model_id: m, pool_group_id: p, ...common };
        await createBinding(input, tenantId);
        onDone(`绑定 model ${m} → pool ${p} 已创建。`);
      }
    } catch (err) {
      setLocalError(friendlyMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <ModalShell title={editing ? '编辑绑定' : '新建绑定'} icon={<Link2 className="size-4 text-primary-600 dark:text-primary-300" />} onClose={onClose} wide>
      <div className="flex flex-col gap-3">
        <div className="grid grid-cols-2 gap-3">
          <Field label="model_id">
            <input type="number" min={1} value={modelID} disabled={editing} onChange={(e) => setModelID(e.target.value)} placeholder="如 42" className="h-9 rounded-md border border-input bg-background px-3 text-sm disabled:opacity-60" />
          </Field>
          <Field label="pool（资源池）">
            <select value={poolID} disabled={editing} onChange={(e) => setPoolID(e.target.value)} className="h-9 rounded-md border border-input bg-background px-3 text-sm disabled:opacity-60">
              <option value="">选择 pool…</option>
              {pools.map((p) => (
                <option key={p.id} value={p.id}>
                  #{p.id} {p.name ?? ''}
                </option>
              ))}
            </select>
          </Field>
        </div>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <Field label="优先级"><input type="number" value={priority} onChange={(e) => setPriority(e.target.value)} className="h-9 rounded-md border border-input bg-background px-3 text-sm" /></Field>
          <Field label="权重"><input type="number" min={1} value={weight} onChange={(e) => setWeight(e.target.value)} className="h-9 rounded-md border border-input bg-background px-3 text-sm" /></Field>
          <Field label="选择模式">
            <select value={selectionMode} onChange={(e) => setSelectionMode(e.target.value as SelectionMode)} className="h-9 rounded-md border border-input bg-background px-3 text-sm">
              {SELECTION_MODES.map((m) => (
                <option key={m} value={m}>{SELECTION_MODE_LABEL[m]}{m === 'priority_weighted' ? '(未启用)' : ''}</option>
              ))}
            </select>
          </Field>
          <Field label="回退类">
            <select value={fallbackClass} onChange={(e) => setFallbackClass(e.target.value as FallbackClass)} className="h-9 rounded-md border border-input bg-background px-3 text-sm">
              {FALLBACK_CLASSES.map((f) => (
                <option key={f} value={f}>{FALLBACK_CLASS_LABEL[f]}</option>
              ))}
            </select>
          </Field>
        </div>
        {selectionMode === 'priority_weighted' && (
          <p className="text-[11px] text-amber-500">提示:加权执行尚未启用,当前仍按严格优先级路由(存储不丢)。</p>
        )}
        <Field label="上游模型名 override(可选)">
          <input type="text" value={override} onChange={(e) => setOverride(e.target.value)} placeholder="留空=用 model 原名" className="h-9 rounded-md border border-input bg-background px-3 text-sm" />
        </Field>
        <div className="grid grid-cols-3 gap-3">
          <Field label="RPM 上限(可选)"><input type="number" min={0} value={rpm} onChange={(e) => setRpm(e.target.value)} className="h-9 rounded-md border border-input bg-background px-3 text-sm" /></Field>
          <Field label="TPM 上限(可选)"><input type="number" min={0} value={tpm} onChange={(e) => setTpm(e.target.value)} className="h-9 rounded-md border border-input bg-background px-3 text-sm" /></Field>
          <Field label="并发上限(可选)"><input type="number" min={0} value={parallel} onChange={(e) => setParallel(e.target.value)} className="h-9 rounded-md border border-input bg-background px-3 text-sm" /></Field>
        </div>
        <Field label="原因(审计,可选)">
          <input type="text" value={reason} onChange={(e) => setReason(e.target.value)} className="h-9 rounded-md border border-input bg-background px-3 text-sm" />
        </Field>
        <label className="flex items-center gap-2 text-sm text-accent-700 dark:text-accent-200">
          <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
          启用该绑定
        </label>
        {localError && <InlineError message={localError} />}
        <div className="flex justify-end gap-2 pt-1">
          <Button size="sm" variant="outline" onClick={onClose} disabled={submitting}>取消</Button>
          <Button size="sm" onClick={() => void submit()} disabled={submitting}>
            {submitting ? <Loader2 className="size-4 animate-spin" /> : <CheckCircle2 />}
            {editing ? '保存' : '创建'}
          </Button>
        </div>
      </div>
    </ModalShell>
  );
}

// ---- 共享小组件(本页自带,沿用 admin 页约定) ----

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <label className="text-xs text-accent-500 dark:text-accent-400">{label}</label>
      {children}
    </div>
  );
}

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
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onClick={onClose} role="presentation">
      <div
        className={cn('w-full rounded-xl border border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900', wide ? 'max-w-2xl' : 'max-w-md')}
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
