'use client';

// admin 配额策略管理控制台 —— 反滥用「运营配置」（不碰 user_balances / 计费账本）。管理 token 轨
// （lib/api/adminQuotaPolicies.ts，从 localStorage huakai_admin_token 取 Bearer）。CRUD：列表(过滤) + 新建/编辑 + 删除。
//
// 端点读后端真码 adminquotahttp（/admin/v1/quota-policies）。鉴权 resolveTenantIdentity：platform_admin 必带
// tenant_id；tenant_operator 用自身 scope。单租户部署默认 tenant=1（页内可改）。前端只接线测功能，不追设计。
//
// 借鉴（CLEAN-ROOM，§11/§12/§16，仅功能/字段/动作形态，未抄码）：sub2api 配额表 scope 硬编码、仅 USD、无 observe；
// new-api 配额内嵌实体、lifetime 无窗口；CLIProxyAPI 无持久配额策略。HUAKAI delta：独立通用 policy 对象
// （6 scope × 4 metric × 5 window × 4 mode + priority + 有效期 + burst），observe(dry-run) 两家皆无。
// 三态骨架 / 徽章 / 卡片表格 / 弹窗外壳沿用 HUAKAI 自有 app/admin/operations 页样式。

import { useCallback, useEffect, useState } from 'react';
import { AlertCircle, Ban, CheckCircle2, Loader2, Pencil, Plus, RefreshCw, ShieldAlert, Trash2, X } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { friendlyMessage } from '@/lib/api/errors';
import {
  createQuotaPolicy,
  deleteQuotaPolicy,
  listQuotaPolicies,
  quotaModeLabel,
  quotaModeVariant,
  updateQuotaPolicy,
  type QuotaPolicy,
} from '@/lib/api/adminQuotaPolicies';
import {
  METRICS,
  MODES,
  SCOPE_KINDS,
  WINDOW_KINDS,
  validateQuotaPolicyForm,
  type QuotaPolicyFormInput,
} from '@/lib/api/quota-policy-form';
import { cn } from '@/lib/utils';

const DEFAULT_TENANT_ID = 1;
const PAGE_SIZE = 50;

const labelCls = 'text-xs text-accent-500 dark:text-accent-400';
const inputCls = 'h-9 rounded-md border border-input bg-background px-3 text-sm';
const selectCls = 'h-9 rounded-md border border-input bg-background px-2 text-sm';

export default function AdminQuotaPoliciesPage() {
  const [tenantId, setTenantId] = useState<number>(DEFAULT_TENANT_ID);
  const [policies, setPolicies] = useState<QuotaPolicy[]>([]);
  const [scopeFilter, setScopeFilter] = useState('all');
  const [metricFilter, setMetricFilter] = useState('all');
  const [enabledFilter, setEnabledFilter] = useState('all'); // all | true | false
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [actionId, setActionId] = useState<number | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [editTarget, setEditTarget] = useState<QuotaPolicy | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await listQuotaPolicies({
        tenant_id: tenantId,
        scope_kind: scopeFilter,
        metric: metricFilter,
        enabled: enabledFilter === 'all' ? undefined : enabledFilter === 'true',
        limit: PAGE_SIZE,
      });
      setPolicies(res.items ?? []);
    } catch (err) {
      setError(friendlyMessage(err));
      setPolicies([]);
    } finally {
      setLoading(false);
    }
  }, [tenantId, scopeFilter, metricFilter, enabledFilter]);

  useEffect(() => {
    void load();
  }, [load]);

  const handleDelete = useCallback(
    async (p: QuotaPolicy) => {
      setActionId(p.id);
      setError(null);
      setNotice(null);
      try {
        await deleteQuotaPolicy(p.id, tenantId);
        setNotice(`策略 #${p.id} 已删除。`);
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
    <div className="mx-auto flex max-w-6xl flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-bold text-accent-950 dark:text-white">配额策略</h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          反滥用运营配置（不碰余额/计费）。按 scope × metric × window 定限额；mode=observe 仅记录不拦截。需 platform_admin，须指定租户 ID。
        </p>
      </div>

      {error && <Banner kind="error" text={error} />}
      {notice && <Banner kind="ok" text={notice} />}

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardContent className="flex flex-wrap items-center gap-3 p-4">
          <div className="flex items-center gap-2">
            <label className={labelCls}>租户 ID</label>
            <input
              type="number"
              min={1}
              value={tenantId}
              onChange={(e) => setTenantId(Math.max(1, Number(e.target.value) || 1))}
              className={cn(inputCls, 'w-20 tabular-nums')}
            />
          </div>
          <div className="flex items-center gap-2">
            <label className={labelCls}>scope</label>
            <select value={scopeFilter} onChange={(e) => setScopeFilter(e.target.value)} className={selectCls}>
              <option value="all">全部</option>
              {SCOPE_KINDS.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </div>
          <div className="flex items-center gap-2">
            <label className={labelCls}>metric</label>
            <select value={metricFilter} onChange={(e) => setMetricFilter(e.target.value)} className={selectCls}>
              <option value="all">全部</option>
              {METRICS.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
              ))}
            </select>
          </div>
          <div className="flex items-center gap-2">
            <label className={labelCls}>enabled</label>
            <select value={enabledFilter} onChange={(e) => setEnabledFilter(e.target.value)} className={selectCls}>
              <option value="all">全部</option>
              <option value="true">启用</option>
              <option value="false">停用</option>
            </select>
          </div>
          <div className="ml-auto flex gap-1.5">
            <Button size="sm" variant="outline" onClick={() => void load()} disabled={loading || actionId !== null}>
              <RefreshCw className={cn(loading && 'animate-spin')} />
              刷新
            </Button>
            <Button
              size="sm"
              onClick={() => {
                setEditTarget(null);
                setShowForm(true);
                setError(null);
                setNotice(null);
              }}
              disabled={actionId !== null}
            >
              <Plus />
              新建策略
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-row items-center justify-between p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold text-accent-950 dark:text-white">
            <ShieldAlert className="size-4 text-primary-600 dark:text-primary-300" />
            策略列表
          </CardTitle>
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {loading && policies.length === 0 ? (
            <div className="flex items-center justify-center gap-2 py-12 text-sm text-accent-400">
              <Loader2 className="size-5 animate-spin" /> 加载配额策略中…
            </div>
          ) : policies.length === 0 ? (
            <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
              当前条件下暂无配额策略，点击「新建策略」创建。
            </div>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>ID</TableHead>
                    <TableHead>scope</TableHead>
                    <TableHead>metric / window</TableHead>
                    <TableHead className="text-right">limit / burst</TableHead>
                    <TableHead>mode</TableHead>
                    <TableHead>优先级</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {policies.map((p) => (
                    <TableRow key={p.id}>
                      <TableCell className="font-mono text-xs tabular-nums">#{p.id}</TableCell>
                      <TableCell className="text-xs text-accent-700 dark:text-accent-200">
                        <div className="font-medium">{p.scope_kind}</div>
                        <div className="font-mono text-[11px] text-accent-400">{p.scope_id}</div>
                      </TableCell>
                      <TableCell className="text-xs text-accent-600 dark:text-accent-300">
                        {p.metric}
                        <div className="text-[11px] text-accent-400">
                          {p.window_kind}
                          {p.window_kind === 'fixed' ? ` · ${p.window_seconds}s` : ''}
                        </div>
                      </TableCell>
                      <TableCell className="text-right font-mono text-xs tabular-nums text-accent-900 dark:text-accent-100">
                        {p.limit_value}
                        {p.burst_value && p.burst_value !== '0' ? ` (+${p.burst_value})` : ''}
                      </TableCell>
                      <TableCell>
                        <Badge variant={quotaModeVariant(p.mode)}>{quotaModeLabel(p.mode)}</Badge>
                      </TableCell>
                      <TableCell className="text-xs tabular-nums text-accent-500 dark:text-accent-400">{p.priority}</TableCell>
                      <TableCell>
                        <Badge variant={p.enabled ? 'default' : 'secondary'}>{p.enabled ? '启用' : '停用'}</Badge>
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex items-center justify-end gap-1.5">
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => {
                              setEditTarget(p);
                              setShowForm(true);
                              setError(null);
                              setNotice(null);
                            }}
                            disabled={actionId !== null}
                            title="编辑"
                          >
                            <Pencil />
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            onClick={() => void handleDelete(p)}
                            disabled={actionId !== null}
                            title="删除"
                          >
                            {actionId === p.id ? <Loader2 className="size-4 animate-spin" /> : <Trash2 />}
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>

      {showForm && (
        <QuotaPolicyFormModal
          tenantId={tenantId}
          existing={editTarget}
          onClose={() => setShowForm(false)}
          onDone={(msg) => {
            setShowForm(false);
            setNotice(msg);
            void load();
          }}
        />
      )}
    </div>
  );
}

function Banner({ kind, text }: { kind: 'error' | 'ok'; text: string }) {
  const isErr = kind === 'error';
  return (
    <div
      className={cn(
        'flex items-start gap-2 rounded-lg border px-4 py-3 text-sm',
        isErr
          ? 'border-red-200 bg-red-50 text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300'
          : 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300',
      )}
    >
      {isErr ? <AlertCircle className="mt-0.5 size-4 shrink-0" /> : <CheckCircle2 className="mt-0.5 size-4 shrink-0" />}
      <span>{text}</span>
    </div>
  );
}

// RFC3339 → datetime-local input 值（'YYYY-MM-DDTHH:mm'）。空/非法 → ''。
function toLocalInput(rfc?: string): string {
  if (!rfc) return '';
  const d = new Date(rfc);
  if (Number.isNaN(d.getTime())) return '';
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// datetime-local 值 → RFC3339（空 → ''）。
function toRfc3339(local: string): string {
  if (!local) return '';
  const d = new Date(local);
  return Number.isNaN(d.getTime()) ? '' : d.toISOString();
}

function QuotaPolicyFormModal({
  tenantId,
  existing,
  onClose,
  onDone,
}: {
  tenantId: number;
  existing: QuotaPolicy | null;
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const [scopeKind, setScopeKind] = useState(existing?.scope_kind ?? 'global');
  const [scopeId, setScopeId] = useState(existing?.scope_id ?? '*');
  const [metric, setMetric] = useState(existing?.metric ?? 'requests');
  const [windowKind, setWindowKind] = useState(existing?.window_kind ?? 'fixed');
  const [windowSeconds, setWindowSeconds] = useState(existing ? String(existing.window_seconds) : '3600');
  const [limitValue, setLimitValue] = useState(existing?.limit_value ?? '');
  const [burstValue, setBurstValue] = useState(existing && existing.burst_value !== '0' ? existing.burst_value : '');
  const [mode, setMode] = useState(existing?.mode ?? 'enforce');
  const [priority, setPriority] = useState(existing ? String(existing.priority) : '100');
  const [enabled, setEnabled] = useState(existing?.enabled ?? true);
  const [validFrom, setValidFrom] = useState(toLocalInput(existing?.valid_from));
  const [validUntil, setValidUntil] = useState(toLocalInput(existing?.valid_until));
  const [reason, setReason] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  async function submit() {
    const input: QuotaPolicyFormInput = {
      scope_kind: scopeKind,
      scope_id: scopeId,
      metric,
      window_kind: windowKind,
      window_seconds: windowSeconds,
      limit_value: limitValue,
      burst_value: burstValue,
      mode,
      priority,
      enabled,
      valid_from: toRfc3339(validFrom),
      valid_until: toRfc3339(validUntil),
      reason,
    };
    const err = validateQuotaPolicyForm(input);
    if (err) {
      setLocalError(err);
      return;
    }
    setSubmitting(true);
    setLocalError(null);
    try {
      if (existing) {
        await updateQuotaPolicy(existing.id, tenantId, input);
        onDone(`策略 #${existing.id} 已更新。`);
      } else {
        const created = await createQuotaPolicy(tenantId, input);
        onDone(`策略 #${created.id} 已创建。`);
      }
    } catch (err2) {
      setLocalError(friendlyMessage(err2));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onClick={onClose} role="presentation">
      <div
        className="w-full max-w-2xl rounded-xl border border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900"
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
      >
        <div className="flex items-center justify-between border-b border-accent-200 p-4 dark:border-accent-800">
          <div className="flex items-center gap-2 text-base font-semibold text-accent-950 dark:text-white">
            <ShieldAlert className="size-4 text-primary-600 dark:text-primary-300" />
            {existing ? `编辑策略 #${existing.id}` : '新建配额策略'}
          </div>
          <Button size="icon" variant="ghost" onClick={onClose} className="size-8">
            <X />
          </Button>
        </div>
        <div className="max-h-[70vh] overflow-y-auto p-4">
          <div className="grid grid-cols-2 gap-3">
            <Field label="scope_kind">
              <select value={scopeKind} onChange={(e) => setScopeKind(e.target.value)} className={selectCls}>
                {SCOPE_KINDS.map((s) => (
                  <option key={s} value={s}>
                    {s}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="scope_id（global 用 *）">
              <input value={scopeId} onChange={(e) => setScopeId(e.target.value)} className={inputCls} placeholder="*" />
            </Field>
            <Field label="metric">
              <select value={metric} onChange={(e) => setMetric(e.target.value)} className={selectCls}>
                {METRICS.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="window_kind">
              <select value={windowKind} onChange={(e) => setWindowKind(e.target.value)} className={selectCls}>
                {WINDOW_KINDS.map((w) => (
                  <option key={w} value={w}>
                    {w}
                  </option>
                ))}
              </select>
            </Field>
            {windowKind === 'fixed' && (
              <Field label="window_seconds（fixed 必填 >0）">
                <input type="number" min={1} value={windowSeconds} onChange={(e) => setWindowSeconds(e.target.value)} className={cn(inputCls, 'tabular-nums')} />
              </Field>
            )}
            <Field label="limit_value（非负，必填）">
              <input value={limitValue} onChange={(e) => setLimitValue(e.target.value)} className={cn(inputCls, 'tabular-nums')} placeholder="100" />
            </Field>
            <Field label="burst_value（非负，可选）">
              <input value={burstValue} onChange={(e) => setBurstValue(e.target.value)} className={cn(inputCls, 'tabular-nums')} placeholder="0" />
            </Field>
            <Field label="mode">
              <select value={mode} onChange={(e) => setMode(e.target.value)} className={selectCls}>
                {MODES.map((m) => (
                  <option key={m} value={m}>
                    {quotaModeLabel(m)}
                  </option>
                ))}
              </select>
            </Field>
            <Field label="priority">
              <input type="number" value={priority} onChange={(e) => setPriority(e.target.value)} className={cn(inputCls, 'tabular-nums')} />
            </Field>
            <Field label="valid_from（可选）">
              <input type="datetime-local" value={validFrom} onChange={(e) => setValidFrom(e.target.value)} className={inputCls} />
            </Field>
            <Field label="valid_until（可选，须晚于 from）">
              <input type="datetime-local" value={validUntil} onChange={(e) => setValidUntil(e.target.value)} className={inputCls} />
            </Field>
            <Field label="reason（审计备注，可选）">
              <input value={reason} onChange={(e) => setReason(e.target.value)} className={inputCls} placeholder="变更原因" />
            </Field>
          </div>

          <label className="mt-3 flex items-center gap-2 text-xs text-accent-600 dark:text-accent-300">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />
            启用（enabled）
          </label>

          {localError && (
            <div className="mt-3">
              <Banner kind="error" text={localError} />
            </div>
          )}
          <div className="mt-4 flex justify-end gap-2">
            <Button size="sm" variant="outline" onClick={onClose} disabled={submitting}>
              <Ban />
              取消
            </Button>
            <Button size="sm" onClick={() => void submit()} disabled={submitting}>
              {submitting ? <Loader2 className="size-4 animate-spin" /> : <CheckCircle2 />}
              {existing ? '保存' : '创建'}
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <label className={labelCls}>{label}</label>
      {children}
    </div>
  );
}
