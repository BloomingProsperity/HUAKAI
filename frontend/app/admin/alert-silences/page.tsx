'use client';

// admin 告警静默管理控制台（管理 token 轨，lib/api/adminAlertSilences.ts）。前端只接线测功能,不追设计。
//
// 端点读后端真码 alertinghttp/silence_handlers.go（/v1/admin/alert-silences）。鉴权 platform_admin 或
// tenant_operator；platform_admin 必带 tenant_id，tenant_operator 用 scope。单租户默认 tenant=1。
// 仅 list/create/delete（后端无 update）。借鉴对照详见 lib/api/alert-silence-form.ts 头与 plan artifact
// （sub2api 有作用域告警静默；new-api/CLIProxyAPI 无）。HUAKAI delta：租户隔离 + starts/ends 时间窗。骨架沿用 app/admin 样式。

import { useCallback, useEffect, useMemo, useState } from 'react';
import { AlertCircle, BellOff, CheckCircle2, Loader2, Plus, RefreshCw, Trash2, X } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { friendlyMessage } from '@/lib/api/errors';
import {
  createAlertSilence,
  deleteAlertSilence,
  listAlertSilences,
  type AlertSilence,
} from '@/lib/api/adminAlertSilences';
import { validateAlertSilenceForm, type AlertSilenceFormInput } from '@/lib/api/alert-silence-form';
import { cn } from '@/lib/utils';

const DEFAULT_TENANT_ID = 1;
const PAGE_SIZE = 50;
const labelCls = 'text-xs text-accent-500 dark:text-accent-400';
const inputCls = 'h-9 rounded-md border border-input bg-background px-3 text-sm';

function emptyForm(): AlertSilenceFormInput {
  return { reason: '', starts_at_raw: '', ends_at_raw: '', rule_id_raw: '', platform: '', group_id: '', region: '' };
}

export default function AdminAlertSilencesPage() {
  const [tenantId, setTenantId] = useState(DEFAULT_TENANT_ID);
  const [items, setItems] = useState<AlertSilence[]>([]);
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [actionId, setActionId] = useState<number | null>(null);
  const [showForm, setShowForm] = useState(false);

  const offset = page * PAGE_SIZE;

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      // 多取 1 条判断是否有下一页。
      const res = await listAlertSilences({ tenant_id: tenantId, limit: PAGE_SIZE + 1, offset });
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

  async function onDelete(s: AlertSilence) {
    if (!window.confirm(`删除静默 #${s.id}？`)) return;
    setActionId(s.id);
    setError(null);
    setNotice(null);
    try {
      await deleteAlertSilence(s.id, tenantId);
      setNotice(`已删除静默 #${s.id}。`);
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
        <h1 className="text-xl font-bold text-accent-950 dark:text-white">告警静默</h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          在时间窗内抑制告警（按规则 / 平台 / 分组 / 地域作用域）。需 platform_admin 或 tenant_operator。
        </p>
      </div>

      {error && <Banner kind="error" text={error} />}
      {notice && <Banner kind="ok" text={notice} />}

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
          <CardTitle className="flex items-center gap-2 text-base">
            <BellOff className="size-4" /> 静默列表
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
                // floor 取整：tenant_id 须为正整数，否则 '1.5'/'1e3' 透传 → 后端 strconv.ParseInt 400。
                // （留空即时 clamp 回 1 是有意为之的过滤器行为，非 bug。）
                setTenantId(Math.max(1, Math.floor(Number(e.target.value)) || 1));
              }}
            />
            <Button variant="outline" onClick={() => void load()} className="gap-1.5">
              <RefreshCw className="size-4" /> 刷新
            </Button>
            <Button onClick={() => setShowForm(true)} className="gap-1.5">
              <Plus className="size-4" /> 新建静默
            </Button>
          </div>
        </CardHeader>
        <CardContent>
          {loading ? (
            <p className="flex items-center gap-2 py-8 text-sm text-accent-400">
              <Loader2 className="size-4 animate-spin" /> 加载中…
            </p>
          ) : visible.length === 0 ? (
            <p className="py-8 text-center text-sm text-accent-400 dark:text-accent-500">暂无静默。</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <TableHead>原因</TableHead>
                  <TableHead>作用域</TableHead>
                  <TableHead>开始</TableHead>
                  <TableHead>结束</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visible.map((s) => (
                  <TableRow key={s.id}>
                    <TableCell className="font-medium">#{s.id}</TableCell>
                    <TableCell className="max-w-xs truncate" title={s.reason}>{s.reason || '—'}</TableCell>
                    <TableCell><ScopeBadges silence={s} /></TableCell>
                    <TableCell className="text-xs text-accent-500">{fmtTime(s.starts_at)}</TableCell>
                    <TableCell className="text-xs text-accent-500">{fmtTime(s.ends_at)}</TableCell>
                    <TableCell className="text-right">
                      <Button
                        variant="outline"
                        size="sm"
                        className="gap-1 text-red-600"
                        disabled={actionId === s.id}
                        onClick={() => void onDelete(s)}
                      >
                        {actionId === s.id ? <Loader2 className="size-3.5 animate-spin" /> : <Trash2 className="size-3.5" />}
                        删除
                      </Button>
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
        <SilenceForm
          tenantId={tenantId}
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

function SilenceForm({
  tenantId,
  onClose,
  onSaved,
}: {
  tenantId: number;
  onClose: () => void;
  onSaved: (msg: string) => void;
}) {
  const [form, setForm] = useState<AlertSilenceFormInput>(emptyForm);
  const [saving, setSaving] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  function set<K extends keyof AlertSilenceFormInput>(key: K, value: AlertSilenceFormInput[K]) {
    setForm((f) => ({ ...f, [key]: value }));
  }

  async function onSubmit() {
    const v = validateAlertSilenceForm(form);
    if (v) {
      setLocalError(v);
      return;
    }
    setLocalError(null);
    setSaving(true);
    try {
      const created = await createAlertSilence(tenantId, form);
      onSaved(`已创建静默 #${created.id}。`);
    } catch (err) {
      setLocalError(friendlyMessage(err));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" onClick={onClose}>
      <Card
        className="w-full max-w-lg border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900"
        onClick={(e) => e.stopPropagation()}
      >
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="text-base">新建静默</CardTitle>
          <Button variant="ghost" size="sm" onClick={onClose}><X className="size-4" /></Button>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {localError && <Banner kind="error" text={localError} />}
          <label className="flex flex-col gap-1">
            <span className={labelCls}>原因（可选）</span>
            <input className={inputCls} value={form.reason} onChange={(e) => set('reason', e.target.value)} />
          </label>
          <div className="flex flex-wrap gap-3">
            <label className="flex flex-col gap-1">
              <span className={labelCls}>开始时间</span>
              <input
                type="datetime-local"
                step={1}
                className={inputCls}
                value={form.starts_at_raw}
                onChange={(e) => set('starts_at_raw', e.target.value)}
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className={labelCls}>结束时间</span>
              <input
                type="datetime-local"
                step={1}
                className={inputCls}
                value={form.ends_at_raw}
                onChange={(e) => set('ends_at_raw', e.target.value)}
              />
            </label>
          </div>
          <div className="flex flex-wrap gap-3">
            <label className="flex flex-col gap-1">
              <span className={labelCls}>规则 ID（可选，正整数）</span>
              <input
                className={cn(inputCls, 'w-36')}
                value={form.rule_id_raw ?? ''}
                onChange={(e) => set('rule_id_raw', e.target.value)}
                placeholder="留空=不限规则"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className={labelCls}>平台（可选）</span>
              <input className={cn(inputCls, 'w-36')} value={form.platform ?? ''} onChange={(e) => set('platform', e.target.value)} />
            </label>
          </div>
          <div className="flex flex-wrap gap-3">
            <label className="flex flex-col gap-1">
              <span className={labelCls}>分组 ID（可选）</span>
              <input className={cn(inputCls, 'w-36')} value={form.group_id ?? ''} onChange={(e) => set('group_id', e.target.value)} />
            </label>
            <label className="flex flex-col gap-1">
              <span className={labelCls}>地域（可选）</span>
              <input className={cn(inputCls, 'w-36')} value={form.region ?? ''} onChange={(e) => set('region', e.target.value)} />
            </label>
          </div>
          <div className="flex justify-end gap-2 pt-1">
            <Button variant="outline" onClick={onClose}>取消</Button>
            <Button onClick={() => void onSubmit()} disabled={saving} className="gap-1.5">
              {saving && <Loader2 className="size-4 animate-spin" />}
              创建
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}

function ScopeBadges({ silence }: { silence: AlertSilence }) {
  const parts: string[] = [];
  if (silence.rule_id != null) parts.push(`规则 #${silence.rule_id}`);
  if (silence.platform) parts.push(silence.platform);
  if (silence.group_id) parts.push(`组 ${silence.group_id}`);
  if (silence.region) parts.push(silence.region);
  if (parts.length === 0) return <span className="text-accent-400">全部</span>;
  return (
    <div className="flex flex-wrap gap-1">
      {/* 用 index 入 key：platform 与 region 是独立自由串，可能相等（如都为 'us'），仅用值会撞 React key。 */}
      {parts.map((p, i) => (
        <Badge key={`${i}-${p}`} variant="outline">{p}</Badge>
      ))}
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

function fmtTime(v?: string): string {
  if (!v) return '—';
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false });
}
