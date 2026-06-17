'use client';

// admin 告警规则管理控制台（管理 token 轨，lib/api/adminAlertRules.ts）。前端只接线测功能,不追设计。
// 收口 alerting 面：events/silences 已覆盖，本刀补 rules 写 CRUD（create/get/update/delete）。
//
// 端点读后端真码 alertinghttp/rule_handlers.go（/v1/admin/alert-rules）。鉴权 platform_admin 或 tenant_operator；
// platform_admin 必带 tenant_id，tenant_operator 用 scope。单租户默认 tenant=1。
// 借鉴对照详见 lib/api/alert-rule-form.ts 头与 plan（sub2api 有 alert-rules CRUD；new-api/CLIProxyAPI 无）。
// HUAKAI delta：按租户隔离 + 指标阈值规则。骨架沿用 app/admin 样式。不动 Sidebar.tsx（避让 proxies 分支）。

import { useCallback, useEffect, useMemo, useState } from 'react';
import { AlertCircle, BellRing, CheckCircle2, Loader2, Pencil, Plus, RefreshCw, Trash2, X } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { friendlyMessage } from '@/lib/api/errors';
import {
  createAlertRule,
  deleteAlertRule,
  listAlertRules,
  updateAlertRule,
  type AlertRule,
} from '@/lib/api/adminAlertRules';
import {
  COMPARATORS,
  METRIC_TYPES,
  RULE_SEVERITIES,
  validateAlertRuleForm,
  type AlertRuleFormInput,
} from '@/lib/api/alert-rule-form';
import { cn } from '@/lib/utils';

const DEFAULT_TENANT_ID = 1;
const PAGE_SIZE = 50;
const labelCls = 'text-xs text-accent-500 dark:text-accent-400';
const inputCls = 'h-9 rounded-md border border-input bg-background px-3 text-sm';
const selectCls = 'h-9 rounded-md border border-input bg-background px-2 text-sm';

function emptyForm(): AlertRuleFormInput {
  return {
    name: '',
    metric_type: '',
    metric: '',
    comparator: 'gt',
    threshold_raw: '',
    severity: 'info',
    window_seconds_raw: '300',
    sustained_seconds_raw: '',
    cooldown_seconds_raw: '',
    notify_email: false,
    filters_raw: '',
    enabled: true,
  };
}

function fromRule(r: AlertRule): AlertRuleFormInput {
  return {
    name: r.name,
    metric_type: r.metric_type ?? '',
    metric: r.metric ?? '',
    comparator: r.comparator,
    threshold_raw: String(r.threshold),
    severity: r.severity,
    window_seconds_raw: String(r.window_seconds),
    sustained_seconds_raw: String(r.sustained_seconds),
    cooldown_seconds_raw: String(r.cooldown_seconds),
    notify_email: r.notify_email,
    filters_raw: r.filters && Object.keys(r.filters).length > 0 ? JSON.stringify(r.filters) : '',
    enabled: r.enabled,
  };
}

export default function AdminAlertRulesPage() {
  const [tenantId, setTenantId] = useState(DEFAULT_TENANT_ID);
  const [items, setItems] = useState<AlertRule[]>([]);
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [actionId, setActionId] = useState<number | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [editTarget, setEditTarget] = useState<AlertRule | null>(null);

  const offset = page * PAGE_SIZE;

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await listAlertRules({ tenant_id: tenantId, limit: PAGE_SIZE + 1, offset });
      setItems(res.items ?? []);
    } catch (err) {
      setError(friendlyMessage(err));
      setItems([]);
    } finally {
      setLoading(false);
    }
  }, [tenantId, offset]);

  useEffect(() => {
    void load();
  }, [load]);

  const hasNext = items.length > PAGE_SIZE;
  const visible = useMemo(() => items.slice(0, PAGE_SIZE), [items]);

  async function onDelete(r: AlertRule) {
    if (!window.confirm(`删除告警规则「${r.name}」？`)) return;
    setActionId(r.id);
    setError(null);
    setNotice(null);
    try {
      await deleteAlertRule(r.id, tenantId);
      setNotice(`已删除规则「${r.name}」。`);
      await load();
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setActionId(null);
    }
  }

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-bold text-accent-950 dark:text-white">告警规则</h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          指标阈值告警规则（metric / 比较符 / 阈值 / 窗口 / 分级）。需 platform_admin 或 tenant_operator。
        </p>
      </div>

      {error && <Banner kind="error" text={error} />}
      {notice && <Banner kind="ok" text={notice} />}

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
          <CardTitle className="flex items-center gap-2 text-base">
            <BellRing className="size-4" /> 规则列表
          </CardTitle>
          <div className="flex flex-wrap items-center gap-2">
            <label className={labelCls}>租户 ID</label>
            <input
              type="number"
              className={cn(inputCls, 'w-24')}
              value={tenantId}
              min={1}
              onChange={(e) => {
                setPage(0);
                setTenantId(Math.max(1, Math.floor(Number(e.target.value)) || 1));
              }}
            />
            <Button variant="outline" onClick={() => void load()} className="gap-1.5">
              <RefreshCw className="size-4" /> 刷新
            </Button>
            <Button
              onClick={() => {
                setEditTarget(null);
                setShowForm(true);
              }}
              className="gap-1.5"
            >
              <Plus className="size-4" /> 新建
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          {loading ? (
            <p className="flex items-center gap-2 py-8 text-sm text-accent-400">
              <Loader2 className="size-4 animate-spin" /> 加载中…
            </p>
          ) : visible.length === 0 ? (
            <p className="py-8 text-center text-sm text-accent-400 dark:text-accent-500">暂无规则。</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>名称</TableHead>
                  <TableHead>条件</TableHead>
                  <TableHead>分级</TableHead>
                  <TableHead>窗口(s)</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visible.map((r) => (
                  <TableRow key={r.id}>
                    <TableCell className="max-w-xs truncate font-medium" title={r.name}>{r.name}</TableCell>
                    <TableCell className="text-xs text-accent-500">
                      {(r.metric_type || r.metric)} {r.comparator} {r.threshold}
                    </TableCell>
                    <TableCell><SeverityBadge severity={r.severity} /></TableCell>
                    <TableCell className="text-xs text-accent-500">{r.window_seconds}</TableCell>
                    <TableCell>
                      {r.enabled ? <Badge variant="outline">启用</Badge> : <span className="text-accent-400">停用</span>}
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-1.5">
                        <Button
                          variant="outline"
                          size="sm"
                          className="gap-1"
                          onClick={() => {
                            setEditTarget(r);
                            setShowForm(true);
                          }}
                        >
                          <Pencil className="size-3.5" /> 编辑
                        </Button>
                        <Button
                          variant="outline"
                          size="sm"
                          className="gap-1 text-red-600"
                          disabled={actionId === r.id}
                          onClick={() => void onDelete(r)}
                        >
                          {actionId === r.id ? <Loader2 className="size-3.5 animate-spin" /> : <Trash2 className="size-3.5" />}
                          删除
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          <div className="mt-4 flex items-center justify-between">
            <Button variant="outline" size="sm" disabled={page === 0} onClick={() => setPage((p) => Math.max(0, p - 1))}>
              上一页
            </Button>
            <span className={labelCls}>第 {page + 1} 页</span>
            <Button variant="outline" size="sm" disabled={!hasNext} onClick={() => setPage((p) => p + 1)}>
              下一页
            </Button>
          </div>
        </CardContent>
      </Card>

      {showForm && (
        <RuleForm
          tenantId={tenantId}
          target={editTarget}
          onClose={() => setShowForm(false)}
          onSaved={(msg) => {
            setShowForm(false);
            setNotice(msg);
            void load();
          }}
        />
      )}
    </div>
  );
}

function RuleForm({
  tenantId,
  target,
  onClose,
  onSaved,
}: {
  tenantId: number;
  target: AlertRule | null;
  onClose: () => void;
  onSaved: (msg: string) => void;
}) {
  const [form, setForm] = useState<AlertRuleFormInput>(() => (target ? fromRule(target) : emptyForm()));
  const [saving, setSaving] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  function set<K extends keyof AlertRuleFormInput>(key: K, value: AlertRuleFormInput[K]) {
    setForm((f) => ({ ...f, [key]: value }));
  }

  async function onSubmit() {
    const v = validateAlertRuleForm(form);
    if (v) {
      setLocalError(v);
      return;
    }
    setLocalError(null);
    setSaving(true);
    try {
      if (target) {
        await updateAlertRule(target.id, tenantId, form);
        onSaved(`已更新规则「${form.name.trim()}」。`);
      } else {
        await createAlertRule(tenantId, form);
        onSaved(`已创建规则「${form.name.trim()}」。`);
      }
    } catch (err) {
      setLocalError(friendlyMessage(err));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={onClose}>
      <Card
        className="max-h-[90vh] w-full max-w-lg overflow-y-auto border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900"
        onClick={(e) => e.stopPropagation()}
      >
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="text-base">{target ? '编辑规则' : '新建规则'}</CardTitle>
          <Button variant="ghost" size="sm" onClick={onClose}><X className="size-4" /></Button>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {localError && <Banner kind="error" text={localError} />}
          <label className="flex flex-col gap-1">
            <span className={labelCls}>名称</span>
            <input className={inputCls} value={form.name} onChange={(e) => set('name', e.target.value)} />
          </label>
          <div className="flex flex-wrap gap-3">
            <label className="flex flex-col gap-1">
              <span className={labelCls}>指标类型（枚举，可选）</span>
              <select className={selectCls} value={form.metric_type} onChange={(e) => set('metric_type', e.target.value)}>
                <option value="">（自定义 metric）</option>
                {METRIC_TYPES.map((m) => (
                  <option key={m} value={m}>{m}</option>
                ))}
                {/* 存量 metric_type 若不在已知枚举内，保留为选项，避免编辑时被 select 静默改成首项（数据损坏）。 */}
                {form.metric_type !== '' && !(METRIC_TYPES as readonly string[]).includes(form.metric_type) && (
                  <option key={form.metric_type} value={form.metric_type}>{form.metric_type}（存量）</option>
                )}
              </select>
            </label>
            <label className="flex flex-col gap-1">
              <span className={labelCls}>metric（自定义键，二者至少一个）</span>
              <input className={inputCls} value={form.metric} onChange={(e) => set('metric', e.target.value)} />
            </label>
          </div>
          <div className="flex flex-wrap gap-3">
            <label className="flex flex-col gap-1">
              <span className={labelCls}>比较符</span>
              <select className={selectCls} value={form.comparator} onChange={(e) => set('comparator', e.target.value)}>
                {COMPARATORS.map((c) => (
                  <option key={c} value={c}>{c}</option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1">
              <span className={labelCls}>阈值</span>
              <input className={cn(inputCls, 'w-28')} value={form.threshold_raw} onChange={(e) => set('threshold_raw', e.target.value)} />
            </label>
            <label className="flex flex-col gap-1">
              <span className={labelCls}>分级</span>
              <select className={selectCls} value={form.severity} onChange={(e) => set('severity', e.target.value)}>
                {RULE_SEVERITIES.map((s) => (
                  <option key={s} value={s}>{s}</option>
                ))}
              </select>
            </label>
          </div>
          <div className="flex flex-wrap gap-3">
            <label className="flex flex-col gap-1">
              <span className={labelCls}>窗口秒(&gt;0)</span>
              <input className={cn(inputCls, 'w-28')} value={form.window_seconds_raw} onChange={(e) => set('window_seconds_raw', e.target.value)} />
            </label>
            <label className="flex flex-col gap-1">
              <span className={labelCls}>持续秒(≥0)</span>
              <input className={cn(inputCls, 'w-28')} value={form.sustained_seconds_raw} onChange={(e) => set('sustained_seconds_raw', e.target.value)} placeholder="0" />
            </label>
            <label className="flex flex-col gap-1">
              <span className={labelCls}>冷却秒(≥0)</span>
              <input className={cn(inputCls, 'w-28')} value={form.cooldown_seconds_raw} onChange={(e) => set('cooldown_seconds_raw', e.target.value)} placeholder="0" />
            </label>
          </div>
          <label className="flex flex-col gap-1">
            <span className={labelCls}>filters（JSON 对象，值须字符串，可选）</span>
            <textarea
              className="min-h-16 rounded-md border border-input bg-background px-3 py-2 font-mono text-xs"
              value={form.filters_raw}
              onChange={(e) => set('filters_raw', e.target.value)}
              placeholder='{"platform":"anthropic"}'
            />
          </label>
          <div className="flex flex-wrap gap-4">
            <label className="flex items-center gap-2">
              <input type="checkbox" checked={form.notify_email} onChange={(e) => set('notify_email', e.target.checked)} />
              <span className={labelCls}>邮件通知</span>
            </label>
            <label className="flex items-center gap-2">
              <input type="checkbox" checked={form.enabled} onChange={(e) => set('enabled', e.target.checked)} />
              <span className={labelCls}>启用</span>
            </label>
          </div>
          <div className="flex justify-end gap-2 pt-1">
            <Button variant="outline" onClick={onClose}>取消</Button>
            <Button onClick={() => void onSubmit()} disabled={saving} className="gap-1.5">
              {saving && <Loader2 className="size-4 animate-spin" />}
              {target ? '保存' : '创建'}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

function SeverityBadge({ severity }: { severity: string }) {
  const cls =
    severity === 'critical'
      ? 'border-red-300 text-red-700 dark:border-red-800 dark:text-red-300'
      : severity === 'warning'
        ? 'border-amber-300 text-amber-700 dark:border-amber-800 dark:text-amber-300'
        : 'border-accent-300 text-accent-600 dark:border-accent-700 dark:text-accent-300';
  return <Badge variant="outline" className={cls}>{severity}</Badge>;
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
