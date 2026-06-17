'use client';

// admin 渠道测试模板管理控制台 —— 可复用的上游渠道连通性测试请求定义（管理 token 轨，
// lib/api/adminChannelTestTemplates.ts）。CRUD：列表(分页) + 新建/编辑 + 删除。前端只接线测功能,不追设计。
//
// 端点读后端真码 channel_test_template_handler.go（/admin/v1/channel-test-templates）。鉴权 platform_admin 必带
// tenant_id；tenant_operator 用 scope。单租户部署默认 tenant=1（页内可改）。
//
// 借鉴（CLEAN-ROOM，§11/§12/§16，仅功能/字段/动作形态，未抄码）：sub2api 有可复用渠道监控模板 CRUD（tiebreaker，
// 头部黑名单挡 HTTP 层头非凭证头）；new-api 无模板（硬编码探测）；CLIProxyAPI 无等价物。
// HUAKAI delta：通用 HTTP 请求形模板 + **凭证头拒绝（防密钥写入测试配置）**。骨架/徽章/卡片表格沿用 app/admin/operations 样式。

import { useCallback, useEffect, useMemo, useState } from 'react';
import { AlertCircle, Ban, CheckCircle2, FlaskConical, Loader2, Pencil, Plus, RefreshCw, Trash2, X } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { friendlyMessage } from '@/lib/api/errors';
import {
  createChannelTestTemplate,
  deleteChannelTestTemplate,
  listChannelTestTemplates,
  updateChannelTestTemplate,
  type ChannelTestTemplate,
} from '@/lib/api/adminChannelTestTemplates';
import {
  TEMPLATE_METHODS,
  validateChannelTestTemplateForm,
  type ChannelTestTemplateFormInput,
} from '@/lib/api/channel-test-template-form';
import { cn } from '@/lib/utils';

const DEFAULT_TENANT_ID = 1;
const PAGE_SIZE = 50;
const labelCls = 'text-xs text-accent-500 dark:text-accent-400';
const inputCls = 'h-9 rounded-md border border-input bg-background px-3 text-sm';
const selectCls = 'h-9 rounded-md border border-input bg-background px-2 text-sm';

export default function AdminChannelTestTemplatesPage() {
  const [tenantId, setTenantId] = useState(DEFAULT_TENANT_ID);
  const [items, setItems] = useState<ChannelTestTemplate[]>([]);
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [actionId, setActionId] = useState<number | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [editTarget, setEditTarget] = useState<ChannelTestTemplate | null>(null);

  const offset = page * PAGE_SIZE;

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      // 多取 1 条判断是否有下一页。
      const res = await listChannelTestTemplates({ tenant_id: tenantId, limit: PAGE_SIZE + 1, offset });
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

  const visible = useMemo(() => items.slice(0, PAGE_SIZE), [items]);
  const hasNext = items.length > PAGE_SIZE;

  const handleDelete = useCallback(
    async (t: ChannelTestTemplate) => {
      setActionId(t.id);
      setError(null);
      setNotice(null);
      try {
        await deleteChannelTestTemplate(t.id, tenantId);
        setNotice(`模板「${t.name}」已删除。`);
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
        <h1 className="text-xl font-bold text-accent-950 dark:text-white">渠道测试模板</h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          可复用的上游渠道连通性测试请求定义（method / path / body / headers）。凭证头（Authorization 等）禁止写入。需 platform_admin 或 tenant_operator；platform_admin 须指定租户 ID，tenant_operator 用自身租户范围。
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
              onChange={(e) => {
                setTenantId(Math.max(1, Number(e.target.value) || 1));
                setPage(0);
              }}
              className={cn(inputCls, 'w-20 tabular-nums')}
            />
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
              新建模板
            </Button>
          </div>
        </CardContent>
      </Card>

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-row items-center justify-between p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold text-accent-950 dark:text-white">
            <FlaskConical className="size-4 text-primary-600 dark:text-primary-300" />
            模板列表
          </CardTitle>
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {loading && items.length === 0 ? (
            <div className="flex items-center justify-center gap-2 py-12 text-sm text-accent-400">
              <Loader2 className="size-5 animate-spin" /> 加载模板中…
            </div>
          ) : visible.length === 0 ? (
            <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
              当前租户暂无渠道测试模板，点击「新建模板」创建。
            </div>
          ) : (
            <>
              <div className="overflow-x-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>名称</TableHead>
                      <TableHead>method / path</TableHead>
                      <TableHead>headers</TableHead>
                      <TableHead>创建时间</TableHead>
                      <TableHead className="text-right">操作</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {visible.map((t) => (
                      <TableRow key={t.id}>
                        <TableCell>
                          <div className="font-medium text-accent-900 dark:text-accent-100">{t.name}</div>
                          <div className="text-[11px] text-accent-400">#{t.id}</div>
                        </TableCell>
                        <TableCell className="text-xs">
                          <Badge variant="outline">{t.method}</Badge>
                          <span className="ml-2 font-mono text-[11px] text-accent-600 dark:text-accent-300">{t.path}</span>
                        </TableCell>
                        <TableCell className="text-xs tabular-nums text-accent-500">
                          {Object.keys(t.headers ?? {}).length} 个
                        </TableCell>
                        <TableCell className="whitespace-nowrap text-xs text-accent-400">{fmtTime(t.created_at)}</TableCell>
                        <TableCell className="text-right">
                          <div className="flex items-center justify-end gap-1.5">
                            <Button
                              size="sm"
                              variant="outline"
                              onClick={() => {
                                setEditTarget(t);
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
                              onClick={() => void handleDelete(t)}
                              disabled={actionId !== null}
                              title="删除"
                            >
                              {actionId === t.id ? <Loader2 className="size-4 animate-spin" /> : <Trash2 />}
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
              <div className="mt-4 flex items-center justify-between">
                <Button size="sm" variant="outline" onClick={() => setPage((p) => Math.max(0, p - 1))} disabled={page === 0 || loading}>
                  上一页
                </Button>
                <span className="text-xs text-accent-400">第 {page + 1} 页</span>
                <Button size="sm" variant="outline" onClick={() => setPage((p) => p + 1)} disabled={!hasNext || loading}>
                  下一页
                </Button>
              </div>
            </>
          )}
        </CardContent>
      </Card>

      {showForm && (
        <TemplateFormModal
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

function fmtTime(v?: string): string {
  if (!v) return '—';
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false });
}

function TemplateFormModal({
  tenantId,
  existing,
  onClose,
  onDone,
}: {
  tenantId: number;
  existing: ChannelTestTemplate | null;
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const [name, setName] = useState(existing?.name ?? '');
  const [method, setMethod] = useState(existing?.method ?? 'POST');
  const [path, setPath] = useState(existing?.path ?? '/v1/models');
  const [bodyTemplate, setBodyTemplate] = useState(existing?.body_template ?? '');
  const [headersRaw, setHeadersRaw] = useState(
    existing && existing.headers && Object.keys(existing.headers).length > 0 ? JSON.stringify(existing.headers, null, 2) : '',
  );
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  async function submit() {
    const input: ChannelTestTemplateFormInput = {
      name,
      method,
      path,
      body_template: bodyTemplate,
      headers_raw: headersRaw,
    };
    const err = validateChannelTestTemplateForm(input);
    if (err) {
      setLocalError(err);
      return;
    }
    setSubmitting(true);
    setLocalError(null);
    try {
      if (existing) {
        await updateChannelTestTemplate(existing.id, tenantId, input);
        onDone(`模板「${name.trim()}」已更新。`);
      } else {
        const created = await createChannelTestTemplate(tenantId, input);
        onDone(`模板「${created.name}」已创建。`);
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
            <FlaskConical className="size-4 text-primary-600 dark:text-primary-300" />
            {existing ? `编辑模板 #${existing.id}` : '新建渠道测试模板'}
          </div>
          <Button size="icon" variant="ghost" onClick={onClose} className="size-8">
            <X />
          </Button>
        </div>
        <div className="max-h-[70vh] overflow-y-auto p-4">
          <div className="flex flex-col gap-3">
            <Field label="名称（≤128，租户内唯一）">
              <input value={name} onChange={(e) => setName(e.target.value)} placeholder="例：OpenAI 连通性" className={inputCls} />
            </Field>
            <div className="grid grid-cols-3 gap-3">
              <Field label="method">
                <select value={method} onChange={(e) => setMethod(e.target.value)} className={selectCls}>
                  {TEMPLATE_METHODS.map((m) => (
                    <option key={m} value={m}>
                      {m}
                    </option>
                  ))}
                </select>
              </Field>
              <div className="col-span-2">
                <Field label="path（须以 / 开头）">
                  <input value={path} onChange={(e) => setPath(e.target.value)} placeholder="/v1/chat/completions" className={cn(inputCls, 'w-full font-mono')} />
                </Field>
              </div>
            </div>
            <Field label="body_template（请求体，可含占位符；自由文本）">
              <textarea
                rows={4}
                value={bodyTemplate}
                onChange={(e) => setBodyTemplate(e.target.value)}
                placeholder='{"model":"gpt-4o","messages":[{"role":"user","content":"ping"}]}'
                className={cn(inputCls, 'h-auto py-2 font-mono')}
              />
            </Field>
            <Field label="headers（JSON 对象；凭证头 Authorization/x-api-key/cookie 等禁止）">
              <textarea
                rows={3}
                value={headersRaw}
                onChange={(e) => setHeadersRaw(e.target.value)}
                placeholder='{"Content-Type":"application/json"}'
                className={cn(inputCls, 'h-auto py-2 font-mono')}
              />
            </Field>
          </div>
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
