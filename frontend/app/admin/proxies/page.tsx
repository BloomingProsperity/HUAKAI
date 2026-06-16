'use client';

// admin 出站代理池独立页 —— 从「凭证与代理」页(app/admin/credentials)的「出站代理」tab
// 提升为独立 nav 槽(融合 IA:一个功能一个槽)。读写走管理 token 轨
// (lib/api/adminCredentials.ts 的 proxy client,从 localStorage huakai_admin_token 取 Bearer)。
//
// 端点(proxyadminhttp.MountRoutes,挂 /admin/v1/proxies,读 DTO secret-free,auth_secret write-only):
//   GET /admin/v1/proxies → {items} · POST 新建 · PATCH /{id} 改 · DELETE /{id} 删 · PUT /{id}/status 启停。
//   tenant_operator 省 tenant_id 走自身 scope;platform_admin 必带 ?tenant_id。
//
// 借鉴(CLEAN-ROOM,CLAUDE.md §11/§12,仅 IA / 字段 / 控件形态,未抄源码):
//   - sub2api(LGPL)@e34ad2b 的独立代理视图:代理列表「协议 + 状态」过滤、地址 host:port 列、
//     状态徽章、行内编辑/删除、create 表单(name/protocol/host/port/username/password)的布局形态 —— 是本页
//     成为「独立槽」的 IA 依据。HUAKAI 后端代理无 expires_at / 探测质量字段,故不照搬其过期窗 / 质量列。
//   - new-api(AGPL):无独立代理页 —— 其出站代理(worker-proxy)塞在 operations 系统设置节内,
//     不是独立菜单(no-equivalent for a standalone page),故本页 IA 取 sub2api 形态。
//   细颗粒升级(本页相对原 tab 的 delta,生态维度):①按状态计数的汇总卡 ②名称/主机模糊搜索
//   ③创建时间列。均为纯前端聚合,不引入后端没有的字段。
//   三态骨架 / 徽章 / 卡片 / 表格 / ModalShell 样式沿用 HUAKAI 自有 app/admin/users/page.tsx。
//
// 待跟进:per-proxy fallback_mode(运维开关 A-1,feat/operator-switches 合并 + 后端代理 DTO 暴露该列后,
//   在此页表格 + 编辑表单各加一个 reject/direct 下拉)。

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertCircle,
  CheckCircle2,
  Loader2,
  Network,
  Pencil,
  Plus,
  Power,
  PowerOff,
  RefreshCw,
  Search,
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
  createProxy,
  deleteProxy,
  formatDateTime,
  listProxies,
  proxyStatusBadgeVariant,
  proxyStatusLabel,
  setProxyStatus,
  updateProxy,
  type Proxy,
  type ProxyStatus,
} from '@/lib/api/adminCredentials';
import { cn } from '@/lib/utils';

const DEFAULT_TENANT_ID = 1; // 单租户部署默认

// ---- 主页面 ----

export default function AdminProxiesPage() {
  const [tenantId, setTenantId] = useState<number>(DEFAULT_TENANT_ID);

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-bold text-accent-950 dark:text-white">出站代理池</h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          管理上游出站代理(协议 / 地址 / 鉴权 / 启停)。走管理 token;鉴权密钥写入后不回读(后端 write-only)。
        </p>
      </div>

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardContent className="flex flex-wrap items-end gap-4 p-5">
          <div className="flex flex-col gap-1">
            <label className="text-xs text-accent-500 dark:text-accent-400">租户 ID(platform_admin 必填)</label>
            <input
              type="number"
              min={1}
              value={tenantId}
              onChange={(e) => setTenantId(Math.max(1, Number(e.target.value) || 1))}
              className="h-9 w-28 rounded-md border border-input bg-background px-3 text-sm tabular-nums"
            />
          </div>
        </CardContent>
      </Card>

      <ProxiesPanel tenantId={tenantId} />
    </div>
  );
}

// ---- 代理池主面板(列表 + 汇总 + 过滤 + CRUD) ----

function ProxiesPanel({ tenantId }: { tenantId: number }) {
  const [proxies, setProxies] = useState<Proxy[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [actionKey, setActionKey] = useState<string | null>(null);
  const [query, setQuery] = useState('');
  const [protocolFilter, setProtocolFilter] = useState('all');
  const [statusFilter, setStatusFilter] = useState<'all' | ProxyStatus>('all');
  const [editTarget, setEditTarget] = useState<Proxy | null>(null);
  const [createOpen, setCreateOpen] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await listProxies(tenantId);
      setProxies(resp.items ?? []);
    } catch (err) {
      setError(friendlyMessage(err));
      setProxies([]);
    } finally {
      setLoading(false);
    }
  }, [tenantId]);

  useEffect(() => {
    void load();
  }, [load]);

  const protocols = useMemo(() => {
    const set = new Set<string>();
    proxies.forEach((p) => p.protocol && set.add(p.protocol));
    return Array.from(set).sort();
  }, [proxies]);

  // 状态计数(细颗粒汇总卡数据源,纯前端聚合)。
  const counts = useMemo(() => {
    const c = { total: proxies.length, active: 0, disabled: 0, dead: 0 };
    proxies.forEach((p) => {
      if (p.status === 'active') c.active += 1;
      else if (p.status === 'disabled') c.disabled += 1;
      else if (p.status === 'dead') c.dead += 1;
    });
    return c;
  }, [proxies]);

  const visible = useMemo(() => {
    const q = query.trim().toLowerCase();
    return proxies.filter(
      (p) =>
        (protocolFilter === 'all' || p.protocol === protocolFilter) &&
        (statusFilter === 'all' || p.status === statusFilter) &&
        (q === '' || p.name.toLowerCase().includes(q) || p.host.toLowerCase().includes(q)),
    );
  }, [proxies, query, protocolFilter, statusFilter]);

  const handleToggle = useCallback(
    async (p: Proxy) => {
      const next: ProxyStatus = p.status === 'active' ? 'disabled' : 'active';
      setActionKey(`status-${p.id}`);
      setError(null);
      setNotice(null);
      try {
        await setProxyStatus(p.id, next, tenantId);
        setNotice(`代理「${p.name}」已${next === 'active' ? '启用' : '停用'}。`);
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
    async (p: Proxy) => {
      if (typeof window !== 'undefined' && !window.confirm(`确认删除代理「${p.name}」(${p.host}:${p.port})？`)) return;
      setActionKey(`del-${p.id}`);
      setError(null);
      setNotice(null);
      try {
        await deleteProxy(p.id, tenantId);
        setNotice(`代理「${p.name}」已删除。`);
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
    <>
      {error && <ErrorBanner message={error} />}
      {notice && <NoticeBanner message={notice} />}

      {/* 细颗粒:按状态汇总卡 */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <SummaryCard label="代理总数" value={counts.total} tone="neutral" />
        <SummaryCard label="启用" value={counts.active} tone="ok" />
        <SummaryCard label="已停用" value={counts.disabled} tone="muted" />
        <SummaryCard label="已失效" value={counts.dead} tone="bad" />
      </div>

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <Network className="size-4 text-primary-600 dark:text-primary-300" />
            出站代理池
          </CardTitle>
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-accent-400" />
              <input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="搜索名称 / 主机"
                className="h-9 w-44 rounded-md border border-input bg-background pl-8 pr-3 text-sm"
              />
            </div>
            <select
              value={protocolFilter}
              onChange={(e) => setProtocolFilter(e.target.value)}
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
            >
              <option value="all">全部协议</option>
              {protocols.map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
            <select
              value={statusFilter}
              onChange={(e) => setStatusFilter(e.target.value as 'all' | ProxyStatus)}
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
            >
              <option value="all">全部状态</option>
              <option value="active">启用</option>
              <option value="disabled">已停用</option>
              <option value="dead">已失效</option>
            </select>
            <Button size="sm" onClick={() => setCreateOpen(true)}>
              <Plus />
              新建代理
            </Button>
            <Button size="sm" variant="outline" onClick={() => void load()} disabled={loading}>
              <RefreshCw className={cn(loading && 'animate-spin')} />
            </Button>
          </div>
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {loading && proxies.length === 0 ? (
            <CenterLoader text="加载代理列表中…" />
          ) : visible.length === 0 ? (
            <EmptyState
              text={proxies.length === 0 ? '当前租户暂无出站代理。' : '没有匹配过滤条件的代理。'}
            />
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>名称 / 协议</TableHead>
                    <TableHead>地址</TableHead>
                    <TableHead>鉴权用户</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>最近检查</TableHead>
                    <TableHead>创建时间</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {visible.map((p) => {
                    const busy = actionKey === `status-${p.id}` || actionKey === `del-${p.id}`;
                    return (
                      <TableRow key={p.id}>
                        <TableCell>
                          <div className="font-medium text-accent-900 dark:text-accent-100">{p.name}</div>
                          <div className="text-[11px] uppercase text-accent-400">
                            #{p.id} · {p.protocol}
                          </div>
                        </TableCell>
                        <TableCell className="font-mono text-xs text-accent-700 dark:text-accent-200">
                          {p.host}:{p.port}
                        </TableCell>
                        <TableCell className="text-xs text-accent-500 dark:text-accent-400">
                          {p.auth_username || <span className="text-accent-300">—</span>}
                        </TableCell>
                        <TableCell>
                          <Badge variant={proxyStatusBadgeVariant(p.status)}>{proxyStatusLabel(p.status)}</Badge>
                        </TableCell>
                        <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                          {formatDateTime(p.last_check_at)}
                        </TableCell>
                        <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                          {formatDateTime(p.created_at)}
                        </TableCell>
                        <TableCell>
                          <div className="flex items-center justify-end gap-1.5">
                            <Button
                              size="sm"
                              variant={p.status === 'active' ? 'outline' : 'default'}
                              onClick={() => void handleToggle(p)}
                              disabled={actionKey !== null}
                              title={p.status === 'active' ? '停用' : '启用'}
                            >
                              {busy && actionKey === `status-${p.id}` ? (
                                <Loader2 className="size-4 animate-spin" />
                              ) : p.status === 'active' ? (
                                <PowerOff />
                              ) : (
                                <Power />
                              )}
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              onClick={() => {
                                setEditTarget(p);
                                setError(null);
                                setNotice(null);
                              }}
                              disabled={actionKey !== null}
                              title="编辑"
                            >
                              <Pencil />
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              onClick={() => void handleDelete(p)}
                              disabled={actionKey !== null}
                              title="删除"
                              className="text-red-600 hover:text-red-700 dark:text-red-400"
                            >
                              {busy && actionKey === `del-${p.id}` ? <Loader2 className="size-4 animate-spin" /> : <Trash2 />}
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
        <ProxyFormModal
          tenantId={tenantId}
          onClose={() => setCreateOpen(false)}
          onDone={(msg) => {
            setCreateOpen(false);
            setNotice(msg);
            void load();
          }}
        />
      )}
      {editTarget && (
        <ProxyFormModal
          tenantId={tenantId}
          proxy={editTarget}
          onClose={() => setEditTarget(null)}
          onDone={(msg) => {
            setEditTarget(null);
            setNotice(msg);
            void load();
          }}
        />
      )}
    </>
  );
}

// ---- 代理 新建 / 编辑弹窗 ----

function ProxyFormModal({
  tenantId,
  proxy,
  onClose,
  onDone,
}: {
  tenantId: number;
  proxy?: Proxy;
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const editing = proxy != null;
  const [name, setName] = useState(proxy?.name ?? '');
  const [protocol, setProtocol] = useState(proxy?.protocol ?? 'http');
  const [host, setHost] = useState(proxy?.host ?? '');
  const [port, setPort] = useState<string>(proxy ? String(proxy.port) : '');
  const [authUsername, setAuthUsername] = useState(proxy?.auth_username ?? '');
  const [authSecret, setAuthSecret] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  async function submit() {
    const p = Number(port.trim());
    if (name.trim() === '' || protocol.trim() === '' || host.trim() === '') {
      setLocalError('名称、协议、主机均必填。');
      return;
    }
    if (!Number.isInteger(p) || p <= 0 || p > 65535) {
      setLocalError('端口需为 1..65535 的整数。');
      return;
    }
    setSubmitting(true);
    setLocalError(null);
    try {
      if (editing && proxy) {
        await updateProxy(proxy.id, {
          name,
          protocol,
          host,
          port: p,
          auth_username: authUsername.trim() || undefined,
          auth_secret: authSecret || undefined,
          tenant_id: tenantId,
        });
        onDone(`代理「${name.trim()}」已更新。`);
      } else {
        await createProxy({
          name,
          protocol,
          host,
          port: p,
          auth_username: authUsername.trim() || undefined,
          auth_secret: authSecret || undefined,
          tenant_id: tenantId,
        });
        onDone(`代理「${name.trim()}」已创建。`);
      }
    } catch (err) {
      setLocalError(friendlyMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <ModalShell
      title={editing ? '编辑代理' : '新建代理'}
      icon={<Network className="size-4 text-primary-600 dark:text-primary-300" />}
      onClose={onClose}
    >
      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <label className="text-xs text-accent-500 dark:text-accent-400">名称</label>
          <input
            type="text"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="例：us-residential-1"
            className="h-9 rounded-md border border-input bg-background px-3 text-sm"
          />
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-[8rem_1fr_6rem]">
          <div className="flex flex-col gap-1">
            <label className="text-xs text-accent-500 dark:text-accent-400">协议</label>
            <input
              type="text"
              value={protocol}
              onChange={(e) => setProtocol(e.target.value)}
              placeholder="http / https / socks5"
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs text-accent-500 dark:text-accent-400">主机</label>
            <input
              type="text"
              value={host}
              onChange={(e) => setHost(e.target.value)}
              placeholder="proxy.example.com"
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs text-accent-500 dark:text-accent-400">端口</label>
            <input
              type="number"
              min={1}
              max={65535}
              value={port}
              onChange={(e) => setPort(e.target.value)}
              placeholder="8080"
              className="h-9 rounded-md border border-input bg-background px-3 text-sm tabular-nums"
            />
          </div>
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label className="text-xs text-accent-500 dark:text-accent-400">鉴权用户(可选)</label>
            <input
              type="text"
              value={authUsername}
              onChange={(e) => setAuthUsername(e.target.value)}
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs text-accent-500 dark:text-accent-400">
              鉴权密钥{editing ? '(留空不改)' : '(可选)'}
            </label>
            <input
              type="password"
              value={authSecret}
              onChange={(e) => setAuthSecret(e.target.value)}
              placeholder={editing ? '••••••••(写入用,不回读)' : '密码 / token'}
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
            />
          </div>
        </div>
        <p className="text-[11px] text-accent-400">
          说明:鉴权密钥写入后不回读(后端为 write-only)。状态新建后默认启用,可在列表中启停。
        </p>
        {localError && <InlineError message={localError} />}
        <div className="flex justify-end gap-2 pt-1">
          <Button size="sm" variant="outline" onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button size="sm" onClick={() => void submit()} disabled={submitting}>
            {submitting ? <Loader2 className="size-4 animate-spin" /> : <CheckCircle2 />}
            {editing ? '保存' : '创建'}
          </Button>
        </div>
      </div>
    </ModalShell>
  );
}

// =====================================================================================
//  共享小组件(沿用 HUAKAI 自有 admin 页惯例,各页自带一份)
// =====================================================================================

function SummaryCard({
  label,
  value,
  tone,
}: {
  label: string;
  value: number;
  tone: 'neutral' | 'ok' | 'muted' | 'bad';
}) {
  const toneCls =
    tone === 'ok'
      ? 'text-emerald-600 dark:text-emerald-300'
      : tone === 'bad'
        ? 'text-red-600 dark:text-red-300'
        : tone === 'muted'
          ? 'text-accent-400'
          : 'text-accent-900 dark:text-white';
  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardContent className="flex flex-col gap-1 p-4">
        <span className="text-xs text-accent-500 dark:text-accent-400">{label}</span>
        <span className={cn('text-2xl font-bold tabular-nums', toneCls)}>{value}</span>
      </CardContent>
    </Card>
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
}: {
  title: string;
  icon: React.ReactNode;
  onClose: () => void;
  children: React.ReactNode;
}) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onClick={onClose}
      role="presentation"
    >
      <div
        className="w-full max-w-md rounded-xl border border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900"
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
