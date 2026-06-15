'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertTriangle,
  Check,
  ChevronDown,
  Copy,
  KeyRound,
  Loader2,
  Plus,
  RefreshCw,
  Search,
  ShieldAlert,
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
import {
  createApiKey,
  getKeyUsageSummary,
  listApiKeys,
  revokeApiKey,
  type ApiKeyEnvironment,
  type ApiKeyStatus,
  type ApiKeyView,
  type CreateApiKeyResponse,
  type KeyUsageSummary,
} from '@/lib/api/apiKeys';
import { cn } from '@/lib/utils';

// ---- 格式化辅助 ----

function formatDateTime(value: string | null | undefined): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  return date.toLocaleString('zh-CN', { hour12: false });
}

function statusLabel(status: ApiKeyStatus): string {
  if (status === 'active') return '生效中';
  if (status === 'revoked') return '已撤销';
  return status;
}

function statusTone(status: ApiKeyStatus): 'default' | 'secondary' | 'destructive' {
  if (status === 'active') return 'default';
  return 'secondary';
}

type StatusFilter = 'all' | 'active' | 'revoked';
type SortKey = 'created' | 'last_used' | 'name';

// ---- 主页面 ----

export default function ApiKeysPage() {
  const [keys, setKeys] = useState<ApiKeyView[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [createOpen, setCreateOpen] = useState(false);
  const [created, setCreated] = useState<CreateApiKeyResponse | null>(null);

  // 筛选/搜索/排序（客户端;后端 list 不带这些维度）。
  const [query, setQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [sortKey, setSortKey] = useState<SortKey>('created');

  const refresh = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const resp = await listApiKeys({ limit: 100 });
      setKeys(resp.api_keys ?? []);
    } catch (err: unknown) {
      setError(friendlyMessage(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const handleCreated = useCallback((resp: CreateApiKeyResponse) => {
    setCreateOpen(false);
    setCreated(resp);
    void refresh();
  }, [refresh]);

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    let rows = keys;
    if (q) rows = rows.filter((k) => k.name.toLowerCase().includes(q) || k.key_prefix.toLowerCase().includes(q));
    if (statusFilter !== 'all') rows = rows.filter((k) => k.status === statusFilter);
    const sorted = [...rows].sort((a, b) => {
      if (sortKey === 'name') return a.name.localeCompare(b.name);
      const av = sortKey === 'created' ? a.created_at : a.last_used_at || '';
      const bv = sortKey === 'created' ? b.created_at : b.last_used_at || '';
      return (bv || '').localeCompare(av || ''); // 倒序：新→旧
    });
    return sorted;
  }, [keys, query, statusFilter, sortKey]);

  const activeCount = keys.filter((k) => k.status === 'active').length;

  return (
    <div className="space-y-6">
      <section className="flex flex-col justify-between gap-4 rounded-lg border border-accent-200 bg-white px-5 py-4 shadow-card dark:border-accent-800 dark:bg-accent-900/70 md:flex-row md:items-center">
        <div className="min-w-0">
          <p className="text-xs font-medium text-primary-700 dark:text-primary-300">账号设置</p>
          <h1 className="mt-1 flex items-center gap-2 text-2xl font-bold tracking-normal text-accent-950 dark:text-white">
            <KeyRound className="size-6 text-primary-600 dark:text-primary-300" />
            API Keys
          </h1>
          <p className="mt-2 text-sm text-accent-500 dark:text-accent-400">
            管理你的访问密钥。密钥明文仅在创建时展示一次，请妥善保存。
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <Button disabled={loading} onClick={refresh} size="sm" variant="outline">
            <RefreshCw className={cn('size-4', loading && 'animate-spin')} />
            刷新
          </Button>
          <Button onClick={() => setCreateOpen(true)} size="sm">
            <Plus className="size-4" />
            创建 Key
          </Button>
        </div>
      </section>

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-col gap-3 p-5 pb-3 lg:flex-row lg:items-center lg:justify-between">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            密钥列表
            {!loading && (
              <span className="text-sm font-normal text-accent-400 dark:text-accent-500">
                （生效 {activeCount} / 共 {keys.length}）
              </span>
            )}
          </CardTitle>
          {/* toolbar: 搜索 + 状态筛选 + 排序 */}
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-accent-400" />
              <input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="搜索名称 / 前缀"
                className="h-9 w-44 rounded-md border border-accent-200 bg-white pl-8 pr-2 text-sm text-accent-900 outline-none focus:border-primary-400 focus:ring-2 focus:ring-primary-100 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-100 dark:focus:ring-primary-900/40"
              />
            </div>
            <div className="flex rounded-md border border-accent-200 p-0.5 dark:border-accent-800">
              {(['all', 'active', 'revoked'] as StatusFilter[]).map((s) => (
                <button
                  key={s}
                  type="button"
                  onClick={() => setStatusFilter(s)}
                  className={cn(
                    'rounded px-2.5 py-1 text-xs font-medium transition-colors',
                    statusFilter === s
                      ? 'bg-primary-500 text-white'
                      : 'text-accent-500 hover:text-accent-800 dark:text-accent-400 dark:hover:text-accent-100',
                  )}
                >
                  {s === 'all' ? '全部' : s === 'active' ? '生效中' : '已撤销'}
                </button>
              ))}
            </div>
            <select
              value={sortKey}
              onChange={(e) => setSortKey(e.target.value as SortKey)}
              className="h-9 rounded-md border border-accent-200 bg-white px-2 text-sm text-accent-700 outline-none focus:border-primary-400 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-200"
            >
              <option value="created">按创建时间</option>
              <option value="last_used">按最后使用</option>
              <option value="name">按名称</option>
            </select>
          </div>
        </CardHeader>
        <CardContent className="p-0">
          {error && (
            <div role="alert" className="mx-5 mb-4 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-300">
              <span className="flex items-center gap-2">
                <AlertTriangle className="size-4 shrink-0" />
                加载失败：{error}
              </span>
            </div>
          )}

          <Table>
            <TableHeader>
              <TableRow className="border-accent-200 dark:border-accent-800">
                <TableHead className="text-accent-500 dark:text-accent-400">名称</TableHead>
                <TableHead className="text-accent-500 dark:text-accent-400">前缀</TableHead>
                <TableHead className="text-accent-500 dark:text-accent-400">状态</TableHead>
                <TableHead className="text-accent-500 dark:text-accent-400">过期</TableHead>
                <TableHead className="text-accent-500 dark:text-accent-400">创建时间</TableHead>
                <TableHead className="text-accent-500 dark:text-accent-400">最后使用</TableHead>
                <TableHead className="text-right text-accent-500 dark:text-accent-400">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading && keys.length === 0 ? (
                <TableRow className="border-accent-200 dark:border-accent-800">
                  <TableCell colSpan={7} className="py-10 text-center text-accent-500 dark:text-accent-400">
                    <span className="inline-flex items-center gap-2">
                      <Loader2 className="size-4 animate-spin" />
                      加载中…
                    </span>
                  </TableCell>
                </TableRow>
              ) : keys.length === 0 ? (
                <TableRow className="border-accent-200 dark:border-accent-800">
                  <TableCell colSpan={7} className="py-12 text-center text-accent-500 dark:text-accent-400">
                    <div className="flex flex-col items-center gap-3">
                      <KeyRound className="size-8 text-accent-300 dark:text-accent-600" />
                      <div>还没有任何 API Key。</div>
                      <Button onClick={() => setCreateOpen(true)} size="sm" variant="outline">
                        <Plus className="size-4" />
                        创建第一个 Key
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ) : visible.length === 0 ? (
                <TableRow className="border-accent-200 dark:border-accent-800">
                  <TableCell colSpan={7} className="py-10 text-center text-sm text-accent-500 dark:text-accent-400">
                    没有匹配「{query || statusFilter}」的密钥。
                  </TableCell>
                </TableRow>
              ) : (
                visible.map((key) => (
                  <ApiKeyRow key={key.api_key_id} apiKey={key} onRevoked={refresh} />
                ))
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {createOpen && (
        <CreateKeyModal onClose={() => setCreateOpen(false)} onCreated={handleCreated} />
      )}
      {created && (
        <PlaintextModal created={created} onClose={() => setCreated(null)} />
      )}
    </div>
  );
}

// ---- 复制按钮 ----

function CopyButton({ value, label }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1500);
        } catch { /* 剪贴板不可用时静默 */ }
      }}
      className="inline-flex items-center gap-1 text-accent-400 hover:text-primary-600 dark:hover:text-primary-300"
      aria-label={label ?? '复制'}
      title={label ?? '复制'}
    >
      {copied ? <Check className="size-3.5 text-emerald-500" /> : <Copy className="size-3.5" />}
    </button>
  );
}

// ---- 列表行（展开看用量摘要 + 撤销带 reason） ----

function ApiKeyRow({ apiKey, onRevoked }: { apiKey: ApiKeyView; onRevoked: () => void }) {
  const [confirming, setConfirming] = useState(false);
  const [reason, setReason] = useState('');
  const [revoking, setRevoking] = useState(false);
  const [rowError, setRowError] = useState('');
  const [expanded, setExpanded] = useState(false);
  const [summary, setSummary] = useState<KeyUsageSummary | null>(null);
  const [summaryLoading, setSummaryLoading] = useState(false);
  const [summaryError, setSummaryError] = useState('');
  const isRevoked = apiKey.status === 'revoked';

  const doRevoke = useCallback(async () => {
    setRevoking(true);
    setRowError('');
    try {
      await revokeApiKey(apiKey.api_key_id, reason);
      setConfirming(false);
      onRevoked();
    } catch (err: unknown) {
      setRowError(friendlyMessage(err));
    } finally {
      setRevoking(false);
    }
  }, [apiKey.api_key_id, reason, onRevoked]);

  const toggleExpand = useCallback(async () => {
    const next = !expanded;
    setExpanded(next);
    if (next && !summary && !summaryLoading) {
      setSummaryLoading(true);
      setSummaryError('');
      try {
        setSummary(await getKeyUsageSummary(apiKey.api_key_id));
      } catch (err: unknown) {
        setSummaryError(friendlyMessage(err));
      } finally {
        setSummaryLoading(false);
      }
    }
  }, [expanded, summary, summaryLoading, apiKey.api_key_id]);

  return (
    <>
      <TableRow className="border-accent-200 dark:border-accent-800">
        <TableCell className="font-medium text-accent-900 dark:text-accent-100">
          <button type="button" onClick={toggleExpand} className="flex items-center gap-1.5 text-left hover:text-primary-600 dark:hover:text-primary-300">
            <ChevronDown className={cn('size-4 shrink-0 text-accent-400 transition-transform', expanded && 'rotate-180')} />
            <span className="max-w-[200px] truncate">{apiKey.name || '—'}</span>
          </button>
          {rowError && <div className="mt-1 pl-5 text-xs font-normal text-red-600 dark:text-red-400">{rowError}</div>}
        </TableCell>
        <TableCell className="text-accent-600 dark:text-accent-300">
          <span className="inline-flex items-center gap-1.5">
            <code className="font-mono">{apiKey.key_prefix}</code>
            <CopyButton value={apiKey.key_prefix} label="复制前缀" />
          </span>
        </TableCell>
        <TableCell>
          <Badge variant={statusTone(apiKey.status)}>{statusLabel(apiKey.status)}</Badge>
        </TableCell>
        <TableCell className="text-accent-600 dark:text-accent-300">
          {apiKey.expires_at ? formatDateTime(apiKey.expires_at) : <span className="text-accent-400">永不</span>}
        </TableCell>
        <TableCell className="text-accent-600 dark:text-accent-300">{formatDateTime(apiKey.created_at)}</TableCell>
        <TableCell className="text-accent-600 dark:text-accent-300">{formatDateTime(apiKey.last_used_at)}</TableCell>
        <TableCell className="text-right">
          {isRevoked ? (
            <span className="text-xs text-accent-400 dark:text-accent-500">
              已撤销{apiKey.revoked_reason ? `：${apiKey.revoked_reason}` : ''}
            </span>
          ) : confirming ? (
            <div className="flex items-center justify-end gap-2">
              <input
                value={reason}
                onChange={(e) => setReason(e.target.value)}
                placeholder="撤销原因(可选)"
                className="h-8 w-36 rounded-md border border-accent-200 bg-white px-2 text-xs text-accent-900 outline-none focus:border-red-400 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-100"
              />
              <Button disabled={revoking} onClick={doRevoke} size="sm" variant="destructive">
                {revoking ? <Loader2 className="size-4 animate-spin" /> : <Check className="size-4" />}
                确认
              </Button>
              <Button disabled={revoking} onClick={() => setConfirming(false)} size="sm" variant="ghost">取消</Button>
            </div>
          ) : (
            <Button onClick={() => setConfirming(true)} size="sm" variant="ghost" className="text-red-600 hover:text-red-700 dark:text-red-400">
              <Trash2 className="size-4" />
              撤销
            </Button>
          )}
        </TableCell>
      </TableRow>
      {expanded && (
        <TableRow className="border-accent-200 bg-accent-50/60 dark:border-accent-800 dark:bg-accent-950/40">
          <TableCell colSpan={7} className="py-3">
            {summaryLoading ? (
              <span className="inline-flex items-center gap-2 pl-5 text-xs text-accent-500"><Loader2 className="size-3.5 animate-spin" /> 加载用量摘要…</span>
            ) : summaryError ? (
              <span className="pl-5 text-xs text-red-600 dark:text-red-400">{summaryError}</span>
            ) : summary ? (
              <div className="grid grid-cols-2 gap-x-6 gap-y-1.5 pl-5 text-xs text-accent-600 dark:text-accent-300 sm:grid-cols-3 lg:grid-cols-6">
                <SummaryStat label="累计花费" value={`$${Number.parseFloat(summary.total_cost || '0').toFixed(6)}`} strong />
                <SummaryStat label="请求数" value={summary.request_count.toLocaleString('zh-CN')} />
                <SummaryStat label="输入 Token" value={summary.total_tokens_input.toLocaleString('zh-CN')} />
                <SummaryStat label="输出 Token" value={summary.total_tokens_output.toLocaleString('zh-CN')} />
                <SummaryStat label="缓存读" value={summary.total_cache_read_tokens.toLocaleString('zh-CN')} />
                <SummaryStat label="缓存写" value={summary.total_cache_creation_tokens.toLocaleString('zh-CN')} />
              </div>
            ) : null}
          </TableCell>
        </TableRow>
      )}
    </>
  );
}

function SummaryStat({ label, value, strong }: { label: string; value: string; strong?: boolean }) {
  return (
    <div>
      <div className="text-[11px] text-accent-400 dark:text-accent-500">{label}</div>
      <div className={cn('tabular-nums', strong ? 'font-semibold text-accent-900 dark:text-accent-100' : 'text-accent-700 dark:text-accent-200')}>{value}</div>
    </div>
  );
}

// ---- 通用 Modal 外壳 ----

function ModalShell({
  title,
  icon,
  onClose,
  closable = true,
  children,
}: {
  title: string;
  icon?: React.ReactNode;
  onClose: () => void;
  closable?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div
      role="dialog"
      aria-modal="true"
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4 backdrop-blur-sm"
      onClick={closable ? onClose : undefined}
    >
      <div
        className="w-full max-w-lg rounded-lg border border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between border-b border-accent-200 p-5 dark:border-accent-800">
          <h2 className="flex items-center gap-2 text-lg font-semibold text-accent-950 dark:text-white">
            {icon}
            {title}
          </h2>
          {closable && (
            <Button onClick={onClose} size="icon" variant="ghost" aria-label="关闭">
              <X className="size-4" />
            </Button>
          )}
        </div>
        {children}
      </div>
    </div>
  );
}

// ---- 创建表单 Modal（名称 + 环境 + 有效期） ----

type ExpiryMode = 'never' | 'date';

function CreateKeyModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (resp: CreateApiKeyResponse) => void;
}) {
  const [name, setName] = useState('');
  const [environment, setEnvironment] = useState<ApiKeyEnvironment>('live');
  const [expiryMode, setExpiryMode] = useState<ExpiryMode>('never');
  const [expiryAt, setExpiryAt] = useState(''); // datetime-local 值
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState('');

  const submit = useCallback(async () => {
    const trimmed = name.trim();
    if (!trimmed) {
      setFormError('请填写名称');
      return;
    }
    let expires_at: string | undefined;
    if (expiryMode === 'date') {
      if (!expiryAt) {
        setFormError('请选择过期时间');
        return;
      }
      const d = new Date(expiryAt);
      if (Number.isNaN(d.getTime()) || d.getTime() <= Date.now()) {
        setFormError('过期时间须晚于当前');
        return;
      }
      expires_at = d.toISOString();
    }
    setSubmitting(true);
    setFormError('');
    try {
      onCreated(await createApiKey({ name: trimmed, environment, expires_at }));
    } catch (err: unknown) {
      setFormError(friendlyMessage(err));
    } finally {
      setSubmitting(false);
    }
  }, [name, environment, expiryMode, expiryAt, onCreated]);

  return (
    <ModalShell title="创建 API Key" icon={<KeyRound className="size-5 text-primary-600 dark:text-primary-300" />} onClose={submitting ? () => {} : onClose} closable={!submitting}>
      <div className="space-y-4 p-5">
        <div className="space-y-1.5">
          <label htmlFor="key-name" className="block text-sm font-medium text-accent-700 dark:text-accent-200">
            名称 <span className="text-red-500">*</span>
          </label>
          <input
            id="key-name"
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            onKeyDown={(e) => { if (e.key === 'Enter' && expiryMode === 'never') void submit(); }}
            placeholder="例如：生产环境后端服务"
            maxLength={128}
            autoFocus
            className="w-full rounded-md border border-accent-300 bg-white px-3 py-2 text-sm text-accent-900 outline-none focus:border-primary-500 focus:ring-2 focus:ring-primary-500/30 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-100"
          />
        </div>

        <div className="space-y-1.5">
          <span className="block text-sm font-medium text-accent-700 dark:text-accent-200">环境</span>
          <div className="flex gap-2">
            {(['live', 'test'] as const).map((env) => (
              <button
                key={env}
                type="button"
                onClick={() => setEnvironment(env)}
                className={cn(
                  'flex-1 rounded-md border px-3 py-2 text-sm font-medium transition-colors',
                  environment === env
                    ? 'border-primary-500 bg-primary-50 text-primary-700 dark:border-primary-600 dark:bg-primary-950/40 dark:text-primary-300'
                    : 'border-accent-300 text-accent-600 hover:bg-accent-50 dark:border-accent-700 dark:text-accent-300 dark:hover:bg-accent-800/50',
                )}
              >
                {env === 'live' ? '正式 (live)' : '测试 (test)'}
              </button>
            ))}
          </div>
        </div>

        <div className="space-y-1.5">
          <span className="block text-sm font-medium text-accent-700 dark:text-accent-200">有效期</span>
          <div className="flex gap-2">
            {(['never', 'date'] as ExpiryMode[]).map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => setExpiryMode(m)}
                className={cn(
                  'flex-1 rounded-md border px-3 py-2 text-sm font-medium transition-colors',
                  expiryMode === m
                    ? 'border-primary-500 bg-primary-50 text-primary-700 dark:border-primary-600 dark:bg-primary-950/40 dark:text-primary-300'
                    : 'border-accent-300 text-accent-600 hover:bg-accent-50 dark:border-accent-700 dark:text-accent-300 dark:hover:bg-accent-800/50',
                )}
              >
                {m === 'never' ? '永不过期' : '指定时间'}
              </button>
            ))}
          </div>
          {expiryMode === 'date' && (
            <input
              type="datetime-local"
              value={expiryAt}
              onChange={(e) => setExpiryAt(e.target.value)}
              className="mt-2 w-full rounded-md border border-accent-300 bg-white px-3 py-2 text-sm text-accent-900 outline-none focus:border-primary-500 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-100"
            />
          )}
        </div>

        {formError && (
          <div role="alert" className="rounded-md border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-300">
            {formError}
          </div>
        )}
      </div>

      <div className="flex justify-end gap-2 border-t border-accent-200 p-5 dark:border-accent-800">
        <Button disabled={submitting} onClick={onClose} variant="outline">取消</Button>
        <Button disabled={submitting} onClick={submit}>
          {submitting ? <Loader2 className="size-4 animate-spin" /> : <Plus className="size-4" />}
          创建
        </Button>
      </div>
    </ModalShell>
  );
}

// ---- 明文密钥一次性展示 Modal ----

function PlaintextModal({
  created,
  onClose,
}: {
  created: CreateApiKeyResponse;
  onClose: () => void;
}) {
  const [copied, setCopied] = useState(false);

  const copy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(created.plaintext);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch { /* 剪贴板不可用时静默 */ }
  }, [created.plaintext]);

  return (
    <ModalShell
      title="密钥创建成功"
      icon={<ShieldAlert className="size-5 text-amber-600 dark:text-amber-300" />}
      onClose={onClose}
    >
      <div className="space-y-4 p-5">
        <div className="flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 px-3 py-3 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-200">
          <AlertTriangle className="mt-0.5 size-4 shrink-0" />
          <span>请妥善保存以下密钥，<strong>关闭后将无法再次查看</strong>。后续仅能看到前缀。</span>
        </div>

        <div className="space-y-1.5">
          <span className="block text-sm font-medium text-accent-700 dark:text-accent-200">完整密钥</span>
          <div className="flex items-stretch gap-2">
            <code className="min-w-0 flex-1 break-all rounded-md border border-accent-300 bg-accent-50 px-3 py-2 font-mono text-sm text-accent-900 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-100">
              {created.plaintext}
            </code>
            <Button onClick={copy} variant={copied ? 'secondary' : 'default'} className="shrink-0">
              {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
              {copied ? '已复制' : '复制'}
            </Button>
          </div>
        </div>

        <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
          <dt className="text-accent-400 dark:text-accent-500">前缀</dt>
          <dd className="font-mono text-accent-700 dark:text-accent-200">{created.key_prefix}</dd>
          <dt className="text-accent-400 dark:text-accent-500">状态</dt>
          <dd className="text-accent-700 dark:text-accent-200">{statusLabel(created.status)}</dd>
          <dt className="text-accent-400 dark:text-accent-500">过期时间</dt>
          <dd className="text-accent-700 dark:text-accent-200">
            {created.expires_at ? formatDateTime(created.expires_at) : '永不过期'}
          </dd>
        </dl>
      </div>

      <div className="flex justify-end border-t border-accent-200 p-5 dark:border-accent-800">
        <Button onClick={onClose}>我已保存，关闭</Button>
      </div>
    </ModalShell>
  );
}
