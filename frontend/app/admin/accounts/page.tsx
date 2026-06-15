'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Activity,
  AlertTriangle,
  CheckCircle2,
  Gauge,
  HeartPulse,
  Layers,
  Power,
  RefreshCw,
  ShieldX,
  Timer,
  XCircle,
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
import {
  clearAdminProviderAccountRateLimit,
  getAdminProviderAccountHealth,
  listAdminPoolGroups,
  listAdminProviderAccounts,
  setAdminProviderAccountEnabled,
  testAdminProviderAccount,
  type PoolGroup,
  type ProviderAccount,
  type ProviderAccountHealthDetail,
  type ProviderAccountStateFilter,
  type ProviderAccountTestResult,
} from '@/lib/api/adminAccounts';
import { friendlyMessage } from '@/lib/api/errors';
import { cn } from '@/lib/utils';

// ---- 展示格式化 ----

function formatDateTime(value: string | null | undefined) {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  return date.toLocaleString('zh-CN', { hour12: false });
}

function formatLatency(ms: number | null | undefined) {
  if (ms === null || ms === undefined) return '—';
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`;
  return `${ms}ms`;
}

// 健康态 → 中文标签 + Badge 配色。键覆盖后端 admin_provider_accounts health_state
// 与 channelhealth 快照态;未知态回落原样展示。
const HEALTH_LABEL: Record<string, string> = {
  operational: '健康',
  active: '健康',
  healthy: '健康',
  degraded: '降级',
  throttled: '受限',
  cooling_down: '冷却中',
  cooldown: '冷却中',
  rate_limited: '限流',
  overloaded: '过载',
  temp_unschedulable: '暂不可调度',
  failed: '失败',
  error: '错误',
  disabled: '已停用',
  revoked: '凭据失效',
};

function healthLabel(state: string) {
  return HEALTH_LABEL[state] ?? state;
}

function healthTone(state: string): 'default' | 'secondary' | 'destructive' | 'outline' {
  if (state === 'operational' || state === 'active' || state === 'healthy') return 'default';
  if (state === 'failed' || state === 'error' || state === 'revoked') return 'destructive';
  if (state === 'disabled') return 'outline';
  return 'secondary';
}

const STATE_FILTERS: Array<{ value: ProviderAccountStateFilter; label: string }> = [
  { value: '', label: '全部' },
  { value: 'active', label: '健康' },
  { value: 'error', label: '错误' },
  { value: 'rate_limited', label: '限流' },
  { value: 'overloaded', label: '过载' },
  { value: 'temp_unschedulable', label: '暂不可调度' },
  { value: 'disabled', label: '已停用' },
];

// 测试连通结果(按账号 id 缓存,行内展示)。
type TestState =
  | { kind: 'running' }
  | { kind: 'result'; result: ProviderAccountTestResult }
  | { kind: 'error'; message: string };

// 健康快照(按账号 id 缓存,展开行展示)。
type HealthState =
  | { kind: 'loading' }
  | { kind: 'ready'; detail: ProviderAccountHealthDetail }
  | { kind: 'error'; message: string };

export default function AdminAccountsPage() {
  const [accounts, setAccounts] = useState<ProviderAccount[]>([]);
  const [pools, setPools] = useState<PoolGroup[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  // 过滤器
  const [stateFilter, setStateFilter] = useState<ProviderAccountStateFilter>('');
  const [poolFilter, setPoolFilter] = useState<number | ''>('');
  const [tagFilter, setTagFilter] = useState('');

  // 行内操作态
  const [busyId, setBusyId] = useState<number | null>(null);
  const [rowError, setRowError] = useState<Record<number, string>>({});
  const [tests, setTests] = useState<Record<number, TestState>>({});
  const [expandedId, setExpandedId] = useState<number | null>(null);
  const [healths, setHealths] = useState<Record<number, HealthState>>({});

  const poolNameById = useMemo(() => {
    const map = new Map<number, string>();
    pools.forEach((p) => map.set(p.id, p.name));
    return map;
  }, [pools]);

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [accountsRes, poolsRes] = await Promise.all([
        listAdminProviderAccounts({
          stateFilter: stateFilter || undefined,
          poolGroupId: poolFilter === '' ? undefined : poolFilter,
          tag: tagFilter.trim() || undefined,
          limit: 100,
        }),
        // 池组失败不应阻断账号列表;失败时退化为空池名映射。
        listAdminPoolGroups({ limit: 200 }).catch(() => ({ items: [] as PoolGroup[] })),
      ]);
      setAccounts(accountsRes.items);
      setPools(poolsRes.items);
    } catch (err: unknown) {
      setError(friendlyMessage(err));
    } finally {
      setLoading(false);
    }
  }, [stateFilter, poolFilter, tagFilter]);

  useEffect(() => {
    void load();
  }, [load]);

  function clearRowError(id: number) {
    setRowError((prev) => {
      if (!(id in prev)) return prev;
      const next = { ...prev };
      delete next[id];
      return next;
    });
  }

  function setRowErrorFor(id: number, message: string) {
    setRowError((prev) => ({ ...prev, [id]: message }));
  }

  // 启停:乐观更新后以服务端返回值为准;失败回滚并提示。
  async function handleToggleEnabled(account: ProviderAccount) {
    setBusyId(account.id);
    clearRowError(account.id);
    const next = !account.enabled;
    try {
      const res = await setAdminProviderAccountEnabled(account.id, next);
      setAccounts((prev) => prev.map((a) => (a.id === account.id ? { ...a, enabled: res.enabled } : a)));
    } catch (err: unknown) {
      setRowErrorFor(account.id, friendlyMessage(err));
    } finally {
      setBusyId(null);
    }
  }

  // 清除 rate limit:返回解除 bench 后的完整 account DTO,直接替换该行。
  async function handleClearRateLimit(account: ProviderAccount) {
    setBusyId(account.id);
    clearRowError(account.id);
    try {
      const updated = await clearAdminProviderAccountRateLimit(account.id);
      setAccounts((prev) => prev.map((a) => (a.id === account.id ? updated : a)));
    } catch (err: unknown) {
      setRowErrorFor(account.id, friendlyMessage(err));
    } finally {
      setBusyId(null);
    }
  }

  // 测试连通:dry-run 凭据验证,结果行内缓存。
  async function handleTest(account: ProviderAccount) {
    setTests((prev) => ({ ...prev, [account.id]: { kind: 'running' } }));
    try {
      const result = await testAdminProviderAccount(account.id);
      setTests((prev) => ({ ...prev, [account.id]: { kind: 'result', result } }));
    } catch (err: unknown) {
      setTests((prev) => ({ ...prev, [account.id]: { kind: 'error', message: friendlyMessage(err) } }));
    }
  }

  // 健康快照:展开/收起;首次展开拉取,失败可重试。
  async function handleToggleHealth(account: ProviderAccount) {
    if (expandedId === account.id) {
      setExpandedId(null);
      return;
    }
    setExpandedId(account.id);
    if (healths[account.id]?.kind === 'ready') return;
    setHealths((prev) => ({ ...prev, [account.id]: { kind: 'loading' } }));
    try {
      const detail = await getAdminProviderAccountHealth(account.id);
      setHealths((prev) => ({ ...prev, [account.id]: { kind: 'ready', detail } }));
    } catch (err: unknown) {
      setHealths((prev) => ({ ...prev, [account.id]: { kind: 'error', message: friendlyMessage(err) } }));
    }
  }

  const summary = useMemo(() => {
    const total = accounts.length;
    const healthy = accounts.filter(
      (a) => a.health_state === 'operational' || a.health_state === 'healthy',
    ).length;
    const benched = accounts.filter((a) => a.rate_limit_reset_at || a.overload_until || a.temp_unschedulable_until).length;
    const disabled = accounts.filter((a) => !a.enabled).length;
    const inFlight = accounts.reduce((acc, a) => acc + a.in_flight_count, 0);
    const cap = accounts.reduce((acc, a) => acc + a.cap_concurrency, 0);
    return { total, healthy, benched, disabled, inFlight, cap };
  }, [accounts]);

  return (
    <div className="space-y-6">
      <section className="flex flex-col justify-between gap-4 rounded-lg border border-accent-200 bg-white px-5 py-4 shadow-card dark:border-accent-800 dark:bg-accent-900/70 md:flex-row md:items-center">
        <div className="min-w-0">
          <p className="text-xs font-medium text-primary-700 dark:text-primary-300">账号池运维</p>
          <h1 className="mt-1 text-2xl font-bold tracking-normal text-accent-950 dark:text-white">Provider 账号池</h1>
          <p className="mt-2 text-sm text-accent-500 dark:text-accent-400">
            真实后端账号池:健康态、在途并发、容量、启停与连通测试集中视图。
          </p>
        </div>
        <Button className="shrink-0" disabled={loading} onClick={() => void load()} size="sm" variant="outline">
          <RefreshCw className={cn('size-4', loading && 'animate-spin')} />
          刷新
        </Button>
      </section>

      {error && (
        <div
          role="alert"
          className="flex items-center gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-300"
        >
          <AlertTriangle className="size-4 shrink-0" />
          加载失败:{error}
        </div>
      )}

      <section className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4" aria-label="账号池摘要">
        <SummaryCard icon={Layers} tone="primary" label="账号总数" value={String(summary.total)} detail={`${summary.healthy} 健康`} />
        <SummaryCard icon={Activity} tone="blue" label="在途 / 容量" value={`${summary.inFlight} / ${summary.cap}`} detail="当前并发占用" />
        <SummaryCard icon={Timer} tone="amber" label="冷却中" value={String(summary.benched)} detail="限流 / 过载 / 暂不可调度" />
        <SummaryCard icon={Power} tone="slate" label="已停用" value={String(summary.disabled)} detail="enabled = false" />
      </section>

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-col gap-3 p-5 pb-3 lg:flex-row lg:items-end lg:justify-between">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <HeartPulse className="size-4 text-primary-600 dark:text-primary-300" />
            账号列表
          </CardTitle>
          <div className="flex flex-wrap items-end gap-3">
            <label className="flex flex-col gap-1 text-xs text-accent-500 dark:text-accent-400">
              <span>状态</span>
              <select
                value={stateFilter}
                onChange={(e) => setStateFilter(e.target.value as ProviderAccountStateFilter)}
                className="h-9 rounded-md border border-accent-200 bg-white px-2 text-sm text-accent-900 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-100"
              >
                {STATE_FILTERS.map((f) => (
                  <option key={f.value || 'all'} value={f.value}>
                    {f.label}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-xs text-accent-500 dark:text-accent-400">
              <span>池组</span>
              <select
                value={poolFilter}
                onChange={(e) => setPoolFilter(e.target.value === '' ? '' : Number(e.target.value))}
                className="h-9 rounded-md border border-accent-200 bg-white px-2 text-sm text-accent-900 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-100"
              >
                <option value="">全部池组</option>
                {pools.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1 text-xs text-accent-500 dark:text-accent-400">
              <span>标签</span>
              <input
                value={tagFilter}
                onChange={(e) => setTagFilter(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') void load();
                }}
                placeholder="tag 精确匹配"
                className="h-9 rounded-md border border-accent-200 bg-white px-2 text-sm text-accent-900 placeholder:text-accent-400 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-100"
              />
            </label>
            <Button disabled={loading} onClick={() => void load()} size="sm" variant="secondary">
              应用筛选
            </Button>
          </div>
        </CardHeader>
        <CardContent className="p-0">
          <Table>
            <TableHeader>
              <TableRow className="border-accent-200 dark:border-accent-800">
                <TableHead className="text-accent-500 dark:text-accent-400">账号</TableHead>
                <TableHead className="text-accent-500 dark:text-accent-400">provider / channel</TableHead>
                <TableHead className="text-accent-500 dark:text-accent-400">健康态</TableHead>
                <TableHead className="text-accent-500 dark:text-accent-400">在途 / 容量</TableHead>
                <TableHead className="text-accent-500 dark:text-accent-400">优先级</TableHead>
                <TableHead className="text-accent-500 dark:text-accent-400">调度</TableHead>
                <TableHead className="text-right text-accent-500 dark:text-accent-400">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {accounts.length === 0 ? (
                <TableRow className="border-accent-200 dark:border-accent-800">
                  <TableCell colSpan={7} className="py-10 text-center text-accent-500 dark:text-accent-400">
                    {loading ? '加载中…' : '当前筛选无 provider account。'}
                  </TableCell>
                </TableRow>
              ) : (
                accounts.map((account) => {
                  const benched = Boolean(
                    account.rate_limit_reset_at || account.overload_until || account.temp_unschedulable_until,
                  );
                  const test = tests[account.id];
                  const expanded = expandedId === account.id;
                  return (
                    <RowGroup
                      key={account.id}
                      account={account}
                      benched={benched}
                      busy={busyId === account.id}
                      expanded={expanded}
                      health={healths[account.id]}
                      poolName={account.channel_id ? poolNameById.get(account.channel_id) : undefined}
                      rowError={rowError[account.id]}
                      test={test}
                      onClearRateLimit={() => void handleClearRateLimit(account)}
                      onTest={() => void handleTest(account)}
                      onToggleEnabled={() => void handleToggleEnabled(account)}
                      onToggleHealth={() => void handleToggleHealth(account)}
                    />
                  );
                })
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </div>
  );
}

// ---- 摘要卡 ----

const SUMMARY_TONE: Record<string, string> = {
  primary: 'text-primary-600 dark:text-primary-300',
  blue: 'text-blue-600 dark:text-blue-300',
  amber: 'text-amber-600 dark:text-amber-300',
  slate: 'text-accent-500 dark:text-accent-400',
};

function SummaryCard({
  icon: Icon,
  tone,
  label,
  value,
  detail,
}: {
  icon: typeof Layers;
  tone: keyof typeof SUMMARY_TONE | string;
  label: string;
  value: string;
  detail: string;
}) {
  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardContent className="flex items-start justify-between gap-3 p-5">
        <div className="min-w-0">
          <p className="text-xs font-medium text-accent-500 dark:text-accent-400">{label}</p>
          <p className="mt-2 text-2xl font-bold tracking-normal text-accent-950 dark:text-white">{value}</p>
          <p className="mt-1 truncate text-xs text-accent-400 dark:text-accent-500">{detail}</p>
        </div>
        <Icon className={cn('size-5 shrink-0', SUMMARY_TONE[tone] ?? SUMMARY_TONE.slate)} />
      </CardContent>
    </Card>
  );
}

// ---- 行 + 展开健康快照 ----

function RowGroup({
  account,
  benched,
  busy,
  expanded,
  health,
  poolName,
  rowError,
  test,
  onClearRateLimit,
  onTest,
  onToggleEnabled,
  onToggleHealth,
}: {
  account: ProviderAccount;
  benched: boolean;
  busy: boolean;
  expanded: boolean;
  health: HealthState | undefined;
  poolName: string | undefined;
  rowError: string | undefined;
  test: TestState | undefined;
  onClearRateLimit: () => void;
  onTest: () => void;
  onToggleEnabled: () => void;
  onToggleHealth: () => void;
}) {
  return (
    <>
      <TableRow className="border-accent-200 dark:border-accent-800">
        <TableCell className="font-medium text-accent-900 dark:text-accent-100">
          <div className="flex items-center gap-2">
            <span className="truncate">{account.name}</span>
            {!account.enabled && (
              <Badge variant="outline" className="text-accent-500 dark:text-accent-400">
                停用
              </Badge>
            )}
          </div>
          <div className="mt-1 font-mono text-xs font-normal text-accent-400 dark:text-accent-500">
            #{account.id} · {account.account_type}
          </div>
        </TableCell>
        <TableCell className="text-accent-600 dark:text-accent-300">
          <div className="font-mono text-xs tabular-nums">
            P{account.provider_id} / C{account.channel_id}
          </div>
          {poolName && <div className="mt-1 truncate text-xs text-accent-400 dark:text-accent-500">{poolName}</div>}
        </TableCell>
        <TableCell>
          <Badge variant={healthTone(account.health_state)}>{healthLabel(account.health_state)}</Badge>
          {account.credential_state && account.credential_state !== 'valid' && (
            <div className="mt-1 text-xs text-amber-600 dark:text-amber-300">凭据 {account.credential_state}</div>
          )}
        </TableCell>
        <TableCell className="font-mono tabular-nums text-accent-700 dark:text-accent-200">
          {account.in_flight_count} / {account.cap_concurrency}
        </TableCell>
        <TableCell className="font-mono tabular-nums text-accent-700 dark:text-accent-200">{account.priority}</TableCell>
        <TableCell>
          {benched ? (
            <Badge variant="destructive">冷却中</Badge>
          ) : account.enabled ? (
            <Badge variant="default">可调度</Badge>
          ) : (
            <Badge variant="outline" className="text-accent-500 dark:text-accent-400">
              停用
            </Badge>
          )}
        </TableCell>
        <TableCell>
          <div className="flex flex-wrap items-center justify-end gap-2">
            <Button disabled={busy} onClick={onTest} size="sm" variant="outline">
              <Gauge className="size-4" />
              测试
            </Button>
            <Button onClick={onToggleHealth} size="sm" variant="ghost">
              <HeartPulse className="size-4" />
              {expanded ? '收起' : '健康'}
            </Button>
            <Button disabled={busy} onClick={onToggleEnabled} size="sm" variant={account.enabled ? 'secondary' : 'default'}>
              <Power className="size-4" />
              {account.enabled ? '禁用' : '启用'}
            </Button>
            {benched && (
              <Button disabled={busy} onClick={onClearRateLimit} size="sm" variant="destructive">
                <ShieldX className="size-4" />
                清冷却
              </Button>
            )}
          </div>
        </TableCell>
      </TableRow>

      {(test || rowError) && (
        <TableRow className="border-accent-200 dark:border-accent-800">
          <TableCell colSpan={7} className="bg-accent-50/60 py-2 dark:bg-accent-950/40">
            <div className="flex flex-col gap-2">
              {rowError && (
                <div className="flex items-center gap-2 text-sm text-red-600 dark:text-red-300">
                  <XCircle className="size-4 shrink-0" />
                  操作失败:{rowError}
                </div>
              )}
              {test && <TestResultLine test={test} />}
            </div>
          </TableCell>
        </TableRow>
      )}

      {expanded && (
        <TableRow className="border-accent-200 dark:border-accent-800">
          <TableCell colSpan={7} className="bg-accent-50/60 py-4 dark:bg-accent-950/40">
            <HealthSnapshot health={health} onRetry={onToggleHealth} />
          </TableCell>
        </TableRow>
      )}
    </>
  );
}

function TestResultLine({ test }: { test: TestState }) {
  if (test.kind === 'running') {
    return (
      <div className="flex items-center gap-2 text-sm text-accent-500 dark:text-accent-400">
        <RefreshCw className="size-4 animate-spin" />
        正在测试连通…
      </div>
    );
  }
  if (test.kind === 'error') {
    return (
      <div className="flex items-center gap-2 text-sm text-red-600 dark:text-red-300">
        <XCircle className="size-4 shrink-0" />
        测试请求失败:{test.message}
      </div>
    );
  }
  const { result } = test;
  if (result.ok) {
    return (
      <div className="flex items-center gap-2 text-sm text-emerald-600 dark:text-emerald-300">
        <CheckCircle2 className="size-4 shrink-0" />
        连通正常:{result.message}
      </div>
    );
  }
  return (
    <div className="flex items-center gap-2 text-sm text-red-600 dark:text-red-300">
      <XCircle className="size-4 shrink-0" />
      连通失败{result.error_class ? `(${result.error_class})` : ''}:{result.message}
    </div>
  );
}

function HealthSnapshot({ health, onRetry }: { health: HealthState | undefined; onRetry: () => void }) {
  if (!health || health.kind === 'loading') {
    return (
      <div className="flex items-center gap-2 text-sm text-accent-500 dark:text-accent-400">
        <RefreshCw className="size-4 animate-spin" />
        加载健康快照…
      </div>
    );
  }
  if (health.kind === 'error') {
    return (
      <div className="flex items-center gap-3 text-sm text-red-600 dark:text-red-300">
        <XCircle className="size-4 shrink-0" />
        健康快照加载失败:{health.message}
        <Button onClick={onRetry} size="sm" variant="outline">
          重试
        </Button>
      </div>
    );
  }

  const d = health.detail;
  const items: Array<{ label: string; value: string; warn?: boolean }> = [
    { label: '健康态', value: healthLabel(d.health_state) },
    { label: '需处理', value: d.requires_action ? '是' : '否', warn: d.requires_action },
    { label: 'enabled', value: d.enabled ? 'true' : 'false' },
    { label: '失败计数', value: String(d.failure_count), warn: d.failure_count > 0 },
    { label: '失败类型', value: d.failure_class ?? '—' },
    { label: '最近探测延迟', value: formatLatency(d.last_probe_latency_ms) },
    { label: '最近探测时间', value: formatDateTime(d.last_probe_at) },
    { label: '最近刷新', value: formatDateTime(d.last_refresh_at) },
    { label: '刷新结果', value: d.last_refresh_outcome ?? '—' },
    { label: '5h 窗口状态', value: d.session_window_5h_status ?? '—' },
    { label: '快照更新', value: formatDateTime(d.updated_at) },
  ];

  return (
    <div className="space-y-3">
      <div className="grid grid-cols-2 gap-x-6 gap-y-2 sm:grid-cols-3 lg:grid-cols-4">
        {items.map((item) => (
          <div key={item.label} className="min-w-0">
            <div className="text-xs text-accent-400 dark:text-accent-500">{item.label}</div>
            <div
              className={cn(
                'mt-0.5 truncate text-sm font-medium',
                item.warn ? 'text-amber-600 dark:text-amber-300' : 'text-accent-800 dark:text-accent-100',
              )}
            >
              {item.value}
            </div>
          </div>
        ))}
      </div>
      {d.recent_requests && (
        <div className="flex flex-wrap items-center gap-2 rounded-lg border border-accent-200 bg-white px-3 py-2 text-xs text-accent-600 dark:border-accent-800 dark:bg-accent-900/70 dark:text-accent-300">
          <Activity className="size-3.5 text-primary-600 dark:text-primary-300" />
          近期请求 {d.recent_requests.total} 条 · 成功 {d.recent_requests.success} · 失败 {d.recent_requests.failure}
          {d.recent_requests.last_at ? ` · 最近 ${formatDateTime(d.recent_requests.last_at)}` : ''}
        </div>
      )}
    </div>
  );
}
