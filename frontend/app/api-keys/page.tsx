'use client';

import { useCallback, useEffect, useState } from 'react';
import {
  AlertTriangle,
  Check,
  Copy,
  KeyRound,
  Loader2,
  Plus,
  RefreshCw,
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
  listApiKeys,
  revokeApiKey,
  type ApiKeyEnvironment,
  type ApiKeyStatus,
  type ApiKeyView,
  type CreateApiKeyResponse,
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
  if (status === 'revoked') return 'secondary';
  return 'secondary';
}

// ---- 主页面 ----

export default function ApiKeysPage() {
  const [keys, setKeys] = useState<ApiKeyView[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [createOpen, setCreateOpen] = useState(false);
  // 创建成功后一次性展示的明文密钥
  const [created, setCreated] = useState<CreateApiKeyResponse | null>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const resp = await listApiKeys();
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
        <CardHeader className="p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            密钥列表
            {!loading && keys.length > 0 && (
              <span className="text-sm font-normal text-accent-400 dark:text-accent-500">
                （共 {keys.length} 个）
              </span>
            )}
          </CardTitle>
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
                <TableHead className="text-accent-500 dark:text-accent-400">创建时间</TableHead>
                <TableHead className="text-accent-500 dark:text-accent-400">最后使用</TableHead>
                <TableHead className="text-right text-accent-500 dark:text-accent-400">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading && keys.length === 0 ? (
                <TableRow className="border-accent-200 dark:border-accent-800">
                  <TableCell colSpan={6} className="py-10 text-center text-accent-500 dark:text-accent-400">
                    <span className="inline-flex items-center gap-2">
                      <Loader2 className="size-4 animate-spin" />
                      加载中…
                    </span>
                  </TableCell>
                </TableRow>
              ) : keys.length === 0 ? (
                <TableRow className="border-accent-200 dark:border-accent-800">
                  <TableCell colSpan={6} className="py-12 text-center text-accent-500 dark:text-accent-400">
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
              ) : (
                keys.map((key) => (
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

// ---- 列表行（含撤销二次确认） ----

function ApiKeyRow({ apiKey, onRevoked }: { apiKey: ApiKeyView; onRevoked: () => void }) {
  const [confirming, setConfirming] = useState(false);
  const [revoking, setRevoking] = useState(false);
  const [rowError, setRowError] = useState('');
  const isRevoked = apiKey.status === 'revoked';

  const doRevoke = useCallback(async () => {
    setRevoking(true);
    setRowError('');
    try {
      await revokeApiKey(apiKey.api_key_id);
      setConfirming(false);
      onRevoked();
    } catch (err: unknown) {
      setRowError(friendlyMessage(err));
    } finally {
      setRevoking(false);
    }
  }, [apiKey.api_key_id, onRevoked]);

  return (
    <TableRow className="border-accent-200 dark:border-accent-800">
      <TableCell className="font-medium text-accent-900 dark:text-accent-100">
        <div className="max-w-[220px] truncate">{apiKey.name || '—'}</div>
        {rowError && (
          <div className="mt-1 text-xs font-normal text-red-600 dark:text-red-400">{rowError}</div>
        )}
      </TableCell>
      <TableCell className="font-mono text-accent-600 dark:text-accent-300">{apiKey.key_prefix}</TableCell>
      <TableCell>
        <Badge variant={statusTone(apiKey.status)}>{statusLabel(apiKey.status)}</Badge>
      </TableCell>
      <TableCell className="text-accent-600 dark:text-accent-300">{formatDateTime(apiKey.created_at)}</TableCell>
      <TableCell className="text-accent-600 dark:text-accent-300">{formatDateTime(apiKey.last_used_at)}</TableCell>
      <TableCell className="text-right">
        {isRevoked ? (
          <span className="text-xs text-accent-400 dark:text-accent-500">已撤销</span>
        ) : confirming ? (
          <div className="flex items-center justify-end gap-2">
            <span className="text-xs text-accent-500 dark:text-accent-400">确认撤销？</span>
            <Button disabled={revoking} onClick={doRevoke} size="sm" variant="destructive">
              {revoking ? <Loader2 className="size-4 animate-spin" /> : <Check className="size-4" />}
              确认
            </Button>
            <Button disabled={revoking} onClick={() => setConfirming(false)} size="sm" variant="ghost">
              取消
            </Button>
          </div>
        ) : (
          <Button onClick={() => setConfirming(true)} size="sm" variant="ghost" className="text-red-600 hover:text-red-700 dark:text-red-400">
            <Trash2 className="size-4" />
            撤销
          </Button>
        )}
      </TableCell>
    </TableRow>
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

// ---- 创建表单 Modal ----

function CreateKeyModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (resp: CreateApiKeyResponse) => void;
}) {
  const [name, setName] = useState('');
  const [environment, setEnvironment] = useState<ApiKeyEnvironment>('live');
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState('');

  const submit = useCallback(async () => {
    const trimmed = name.trim();
    if (!trimmed) {
      setFormError('请填写名称');
      return;
    }
    setSubmitting(true);
    setFormError('');
    try {
      const resp = await createApiKey({ name: trimmed, environment });
      onCreated(resp);
    } catch (err: unknown) {
      setFormError(friendlyMessage(err));
    } finally {
      setSubmitting(false);
    }
  }, [name, environment, onCreated]);

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
            onKeyDown={(e) => { if (e.key === 'Enter') void submit(); }}
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
    } catch {
      // 剪贴板不可用时静默：用户仍可手动选中文本复制
    }
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
