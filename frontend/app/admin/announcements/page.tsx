'use client';

// admin 公告管理控制台（管理 token 轨，lib/api/adminAnnouncements.ts）。前端只接线测功能,不追设计。
//
// 端点读后端真码 announcementhttp/handlers.go（/v1/admin/announcements，注意 /v1/admin 非 /admin/v1）。
// 鉴权 platform_admin 或 tenant_operator；platform_admin 必带 tenant_id，tenant_operator 用 scope。单租户默认 tenant=1。
// 借鉴对照详见 lib/api/announcement-form.ts 头与 plan artifact（new-api/sub2api 有公告；CLIProxyAPI 无）。
// HUAKAI delta：按租户隔离 + severity 分级 + active 开关 + published/expires 时间窗。骨架沿用 app/admin 样式。

import { useCallback, useEffect, useMemo, useState } from 'react';
import { AlertCircle, CheckCircle2, Loader2, Megaphone, Pencil, Plus, RefreshCw, Trash2, X } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { friendlyMessage } from '@/lib/api/errors';
import {
  createAnnouncement,
  deleteAnnouncement,
  listAnnouncements,
  updateAnnouncement,
  type Announcement,
} from '@/lib/api/adminAnnouncements';
import {
  SEVERITIES,
  validateAnnouncementForm,
  type AnnouncementFormInput,
} from '@/lib/api/announcement-form';
import { cn } from '@/lib/utils';

const DEFAULT_TENANT_ID = 1;
const PAGE_SIZE = 50;
const labelCls = 'text-xs text-accent-500 dark:text-accent-400';
const inputCls = 'h-9 rounded-md border border-input bg-background px-3 text-sm';
const selectCls = 'h-9 rounded-md border border-input bg-background px-2 text-sm';

function emptyForm(): AnnouncementFormInput {
  return { title: '', body: '', severity: 'info', active: true, published_at_raw: '', expires_at_raw: '' };
}

export default function AdminAnnouncementsPage() {
  const [tenantId, setTenantId] = useState(DEFAULT_TENANT_ID);
  const [items, setItems] = useState<Announcement[]>([]);
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [actionId, setActionId] = useState<number | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [editTarget, setEditTarget] = useState<Announcement | null>(null);

  const offset = page * PAGE_SIZE;

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      // 多取 1 条判断是否有下一页。
      const res = await listAnnouncements({ tenant_id: tenantId, limit: PAGE_SIZE + 1, offset });
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

  function openCreate() {
    setEditTarget(null);
    setShowForm(true);
  }
  function openEdit(a: Announcement) {
    setEditTarget(a);
    setShowForm(true);
  }

  async function onDelete(a: Announcement) {
    if (!window.confirm(`删除公告「${a.title}」？`)) return;
    setActionId(a.id);
    setError(null);
    setNotice(null);
    try {
      await deleteAnnouncement(a.id, tenantId);
      setNotice(`已删除公告「${a.title}」。`);
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
        <h1 className="text-xl font-bold text-accent-950 dark:text-white">公告管理</h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          按租户发布分级运维通告（info / warning / critical），可设发布与过期时间窗、启停。需 platform_admin 或 tenant_operator。
        </p>
      </div>

      {error && <Banner kind="error" text={error} />}
      {notice && <Banner kind="ok" text={notice} />}

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3">
          <CardTitle className="flex items-center gap-2 text-base">
            <Megaphone className="size-4" /> 公告列表
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
                setTenantId(Math.max(1, Number(e.target.value) || 1));
              }}
            />
            <Button variant="outline" onClick={() => void load()} className="gap-1.5">
              <RefreshCw className="size-4" /> 刷新
            </Button>
            <Button onClick={openCreate} className="gap-1.5">
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
            <p className="py-8 text-center text-sm text-accent-400 dark:text-accent-500">暂无公告。</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>标题</TableHead>
                  <TableHead>分级</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>发布</TableHead>
                  <TableHead>过期</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visible.map((a) => (
                  <TableRow key={a.id}>
                    <TableCell className="max-w-xs truncate font-medium" title={a.title}>{a.title}</TableCell>
                    <TableCell><SeverityBadge severity={a.severity} /></TableCell>
                    <TableCell>
                      {a.active ? (
                        <Badge variant="outline">启用</Badge>
                      ) : (
                        <span className="text-accent-400">停用</span>
                      )}
                    </TableCell>
                    <TableCell className="text-xs text-accent-500">{fmtTime(a.published_at)}</TableCell>
                    <TableCell className="text-xs text-accent-500">{fmtTime(a.expires_at)}</TableCell>
                    <TableCell className="text-right">
                      <div className="flex justify-end gap-1.5">
                        <Button variant="outline" size="sm" className="gap-1" onClick={() => openEdit(a)}>
                          <Pencil className="size-3.5" /> 编辑
                        </Button>
                        <Button
                          variant="outline"
                          size="sm"
                          className="gap-1 text-red-600"
                          disabled={actionId === a.id}
                          onClick={() => void onDelete(a)}
                        >
                          {actionId === a.id ? <Loader2 className="size-3.5 animate-spin" /> : <Trash2 className="size-3.5" />}
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
        <AnnouncementForm
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

function AnnouncementForm({
  tenantId,
  target,
  onClose,
  onSaved,
}: {
  tenantId: number;
  target: Announcement | null;
  onClose: () => void;
  onSaved: (msg: string) => void;
}) {
  const [form, setForm] = useState<AnnouncementFormInput>(() =>
    target
      ? {
          title: target.title,
          body: target.body,
          severity: target.severity,
          active: target.active,
          published_at_raw: toLocalInput(target.published_at),
          expires_at_raw: toLocalInput(target.expires_at),
        }
      : emptyForm(),
  );
  const [saving, setSaving] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  function set<K extends keyof AnnouncementFormInput>(key: K, value: AnnouncementFormInput[K]) {
    setForm((f) => ({ ...f, [key]: value }));
  }

  async function onSubmit() {
    const v = validateAnnouncementForm(form);
    if (v) {
      setLocalError(v);
      return;
    }
    setLocalError(null);
    setSaving(true);
    try {
      if (target) {
        await updateAnnouncement(target.id, tenantId, form);
        onSaved(`已更新公告「${form.title.trim()}」。`);
      } else {
        await createAnnouncement(tenantId, form);
        onSaved(`已创建公告「${form.title.trim()}」。`);
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
        className="w-full max-w-lg border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900"
        onClick={(e) => e.stopPropagation()}
      >
        <CardHeader className="flex flex-row items-center justify-between">
          <CardTitle className="text-base">{target ? '编辑公告' : '新建公告'}</CardTitle>
          <Button variant="ghost" size="sm" onClick={onClose}><X className="size-4" /></Button>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {localError && <Banner kind="error" text={localError} />}
          <label className="flex flex-col gap-1">
            <span className={labelCls}>标题</span>
            <input className={inputCls} value={form.title} onChange={(e) => set('title', e.target.value)} />
          </label>
          <label className="flex flex-col gap-1">
            <span className={labelCls}>正文</span>
            <textarea
              className="min-h-24 rounded-md border border-input bg-background px-3 py-2 text-sm"
              value={form.body}
              onChange={(e) => set('body', e.target.value)}
            />
          </label>
          <div className="flex flex-wrap gap-3">
            <label className="flex flex-col gap-1">
              <span className={labelCls}>分级</span>
              <select className={selectCls} value={form.severity} onChange={(e) => set('severity', e.target.value)}>
                {SEVERITIES.map((s) => (
                  <option key={s} value={s}>{s}</option>
                ))}
              </select>
            </label>
            <label className="flex items-center gap-2 self-end pb-1.5">
              <input type="checkbox" checked={form.active} onChange={(e) => set('active', e.target.checked)} />
              <span className={labelCls}>启用</span>
            </label>
          </div>
          <div className="flex flex-wrap gap-3">
            <label className="flex flex-col gap-1">
              <span className={labelCls}>发布时间（留空=立即）</span>
              <input
                type="datetime-local"
                step={1}
                className={inputCls}
                value={form.published_at_raw ?? ''}
                onChange={(e) => set('published_at_raw', e.target.value)}
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className={labelCls}>过期时间（留空=不过期）</span>
              <input
                type="datetime-local"
                step={1}
                className={inputCls}
                value={form.expires_at_raw ?? ''}
                onChange={(e) => set('expires_at_raw', e.target.value)}
              />
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

function fmtTime(v?: string): string {
  if (!v) return '—';
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false });
}

// toLocalInput：RFC3339 → datetime-local 输入值（YYYY-MM-DDTHH:mm:ss，本地时区）。
// 必须带【秒】：否则编辑时回填会把秒截断到 :00，重发时静默回退原记录的秒（最多 59s 漂移），
// 且若原记录 published/expires 同分钟内相差秒级，截断后会触发「expires 必须晚于 published」而锁死无法编辑。
function toLocalInput(v?: string): string {
  if (!v) return '';
  const d = new Date(v);
  if (Number.isNaN(d.getTime())) return '';
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}
