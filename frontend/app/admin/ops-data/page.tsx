'use client';

// admin 运维数据面 —— 基础设施可观测性 / 恢复工具（管理 token 轨，lib/api/adminOpsData.ts）。
// 三 tab：审计事件查看 · DLQ 死信查看+重放 · 缓存 L2 检视+清除。前端只接线测功能，不追设计。
//
// 端点读后端真码（understand workflow 实读 gatewayhttp admin_*_handler）。鉴权：审计/缓存=platform_admin
// 或 tenant_operator(限 scope)；DLQ=platform_admin 限定。DLQ replay 后端不幂等无客户端键 → 二次确认 + 重放中禁连点。
//
// 借鉴（CLEAN-ROOM，§11/§12/§16，仅功能/字段/动作形态，未抄码）：sub2api system-logs 富过滤（审计 tiebreaker）、
// new-api cache admin（stats/清/GC）；两家皆无 DLQ replay。HUAKAI delta：DLQ 死信查看+逐条重放（生态）、
// keyset 游标审计分页（架构）、按 key 选择性清缓存（算法）。骨架/徽章/卡片表格沿用 app/admin/operations 样式。

import { useCallback, useState } from 'react';
import {
  AlertCircle,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  HardDrive,
  Inbox,
  Loader2,
  Play,
  RefreshCw,
  ScrollText,
  Search,
  Trash2,
  X,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { friendlyMessage } from '@/lib/api/errors';
import {
  evictL2CacheKey,
  getL2CacheStats,
  listAuditEvents,
  listDLQ,
  replayDLQ,
  type AuditEvent,
  type DLQRecord,
  type L2CacheStats,
} from '@/lib/api/adminOpsData';
import {
  DLQ_EVENT_KINDS,
  DLQ_STATUSES,
  EVENT_CLASSES,
  SEVERITIES,
  dlqStatusLabel,
  dlqStatusVariant,
  severityLabel,
  severityVariant,
} from '@/lib/api/ops-data-form';
import { cn } from '@/lib/utils';

const DEFAULT_TENANT_ID = 1;
const labelCls = 'text-xs text-accent-500 dark:text-accent-400';
const inputCls = 'h-9 rounded-md border border-input bg-background px-3 text-sm';
const selectCls = 'h-9 rounded-md border border-input bg-background px-2 text-sm';

type TabKey = 'audit' | 'dlq' | 'cache';
const TABS: { key: TabKey; label: string; icon: React.ReactNode }[] = [
  { key: 'audit', label: '审计事件', icon: <ScrollText className="size-4" /> },
  { key: 'dlq', label: 'DLQ 死信', icon: <Inbox className="size-4" /> },
  { key: 'cache', label: '缓存 L2', icon: <HardDrive className="size-4" /> },
];

export default function AdminOpsDataPage() {
  const [tab, setTab] = useState<TabKey>('audit');
  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-bold text-accent-950 dark:text-white">运维数据面</h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          基础设施可观测性与恢复：审计事件 · DLQ 死信重放 · 缓存 L2。需 platform_admin（DLQ 限定）。
        </p>
      </div>
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardContent className="flex flex-wrap gap-1.5 p-4">
          {TABS.map((t) => (
            <Button key={t.key} size="sm" variant={tab === t.key ? 'default' : 'outline'} onClick={() => setTab(t.key)}>
              {t.icon}
              {t.label}
            </Button>
          ))}
        </CardContent>
      </Card>
      {tab === 'audit' && <AuditTab />}
      {tab === 'dlq' && <DlqTab />}
      {tab === 'cache' && <CacheTab />}
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

function SectionCard({ title, icon, action, children }: { title: string; icon: React.ReactNode; action?: React.ReactNode; children: React.ReactNode }) {
  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardHeader className="flex flex-row items-center justify-between p-5 pb-3">
        <CardTitle className="flex items-center gap-2 text-base font-semibold text-accent-950 dark:text-white">
          {icon}
          {title}
        </CardTitle>
        {action}
      </CardHeader>
      <CardContent className="p-5 pt-0">{children}</CardContent>
    </Card>
  );
}

// RFC3339 → 本地时间显示。
function fmtTime(v?: string | null): string {
  if (!v) return '—';
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false });
}

// datetime-local → RFC3339（空 → undefined）。
function toRfc(local: string): string | undefined {
  if (!local) return undefined;
  const d = new Date(local);
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString();
}

// =====================================================================================
// 审计事件 tab —— keyset 游标分页（next_cursor 翻页；cursor 栈支持回退）。
// =====================================================================================

function AuditTab() {
  const [tenantId, setTenantId] = useState(DEFAULT_TENANT_ID);
  const [eventClass, setEventClass] = useState('all');
  const [severity, setSeverity] = useState('all');
  const [eventType, setEventType] = useState('');
  const [from, setFrom] = useState('');
  const [to, setTo] = useState('');
  const [items, setItems] = useState<AuditEvent[]>([]);
  const [total, setTotal] = useState(0);
  const [cursorStack, setCursorStack] = useState<string[]>([]); // 已访问页的起始 cursor（栈顶=当前页）
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [queried, setQueried] = useState(false);

  const fetchPage = useCallback(
    async (cursor: string | undefined, stack: string[]) => {
      setLoading(true);
      setError(null);
      try {
        const res = await listAuditEvents({
          tenant_id: tenantId,
          event_class: eventClass,
          severity,
          event_type: eventType,
          from: toRfc(from),
          to: toRfc(to),
          cursor,
          limit: 50,
        });
        setItems(res.items ?? []);
        setTotal(res.total ?? 0);
        setNextCursor(res.next_cursor ?? null);
        setCursorStack(stack);
        setQueried(true);
      } catch (err) {
        setError(friendlyMessage(err));
        setItems([]);
      } finally {
        setLoading(false);
      }
    },
    [tenantId, eventClass, severity, eventType, from, to],
  );

  return (
    <div className="flex flex-col gap-4">
      {error && <Banner kind="error" text={error} />}
      <SectionCard
        title="审计事件"
        icon={<ScrollText className="size-4 text-primary-600 dark:text-primary-300" />}
        action={
          <Button size="sm" onClick={() => void fetchPage(undefined, [''])} disabled={loading}>
            {loading ? <Loader2 className="size-4 animate-spin" /> : <Search />}
            查询
          </Button>
        }
      >
        <div className="mb-4 flex flex-wrap items-end gap-2">
          <Filter label="租户 ID">
            <input type="number" min={1} value={tenantId} onChange={(e) => setTenantId(Math.max(1, Number(e.target.value) || 1))} className={cn(inputCls, 'w-20 tabular-nums')} />
          </Filter>
          <Filter label="event_class">
            <select value={eventClass} onChange={(e) => setEventClass(e.target.value)} className={selectCls}>
              <option value="all">全部</option>
              {EVENT_CLASSES.map((c) => (
                <option key={c} value={c}>
                  {c}
                </option>
              ))}
            </select>
          </Filter>
          <Filter label="severity">
            <select value={severity} onChange={(e) => setSeverity(e.target.value)} className={selectCls}>
              <option value="all">全部</option>
              {SEVERITIES.map((s) => (
                <option key={s} value={s}>
                  {severityLabel(s)}
                </option>
              ))}
            </select>
          </Filter>
          <Filter label="event_type">
            <input value={eventType} onChange={(e) => setEventType(e.target.value)} placeholder="可选" className={cn(inputCls, 'w-36')} />
          </Filter>
          <Filter label="from">
            <input type="datetime-local" value={from} onChange={(e) => setFrom(e.target.value)} className={inputCls} />
          </Filter>
          <Filter label="to">
            <input type="datetime-local" value={to} onChange={(e) => setTo(e.target.value)} className={inputCls} />
          </Filter>
        </div>

        {!queried ? (
          <Empty text="设置过滤后点「查询」加载审计事件。" />
        ) : loading && items.length === 0 ? (
          <LoadingRow text="加载审计事件中…" />
        ) : items.length === 0 ? (
          <Empty text="无匹配的审计事件。" />
        ) : (
          <>
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>时间</TableHead>
                    <TableHead>类/类型</TableHead>
                    <TableHead>级别</TableHead>
                    <TableHead>actor</TableHead>
                    <TableHead>ledger</TableHead>
                    <TableHead>payload</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((ev) => (
                    // 复合 key：审计 id 仅在各来源表内局部唯一（4 表 UNION），event_class 作来源判别避免跨表 id 撞 React key。
                    <TableRow key={`${ev.event_class}-${ev.id}`}>
                      <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">{fmtTime(ev.created_at)}</TableCell>
                      <TableCell className="text-xs text-accent-700 dark:text-accent-200">
                        <div className="font-medium">{ev.event_class}</div>
                        <div className="text-[11px] text-accent-400">{ev.event_type}</div>
                      </TableCell>
                      <TableCell>
                        <Badge variant={severityVariant(ev.severity)}>{severityLabel(ev.severity)}</Badge>
                      </TableCell>
                      <TableCell className="text-xs text-accent-600 dark:text-accent-300">
                        {ev.actor_id ?? '—'}
                        {ev.actor_role ? ` (${ev.actor_role})` : ''}
                      </TableCell>
                      <TableCell className="font-mono text-[11px] text-accent-500">{ev.ledger_id || '—'}</TableCell>
                      <TableCell className="max-w-xs truncate font-mono text-[11px] text-accent-400" title={JSON.stringify(ev.payload)}>
                        {JSON.stringify(ev.payload)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            <div className="mt-4 flex items-center justify-between">
              <span className="text-xs text-accent-400">共 {total.toLocaleString('zh-CN')} 条 · 本页 {items.length}</span>
              <div className="flex gap-2">
                <Button
                  size="sm"
                  variant="outline"
                  disabled={loading || cursorStack.length <= 1}
                  onClick={() => {
                    // 回退：弹出当前页，用上一页起始 cursor 重新拉。
                    const stack = cursorStack.slice(0, -1);
                    const prevCursor = stack[stack.length - 1] || undefined;
                    void fetchPage(prevCursor || undefined, stack);
                  }}
                >
                  <ChevronLeft />
                  上一页
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  disabled={loading || !nextCursor}
                  onClick={() => {
                    if (nextCursor) void fetchPage(nextCursor, [...cursorStack, nextCursor]);
                  }}
                >
                  下一页
                  <ChevronRight />
                </Button>
              </div>
            </div>
          </>
        )}
      </SectionCard>
    </div>
  );
}

// =====================================================================================
// DLQ 死信 tab —— 选 handler(EventKind) + status 列表 + 逐条重放（二次确认，不幂等故禁连点）。
// =====================================================================================

function DlqTab() {
  const [handler, setHandler] = useState<string>(DLQ_EVENT_KINDS[0]);
  const [status, setStatus] = useState('all');
  const [items, setItems] = useState<DLQRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [queried, setQueried] = useState(false);
  const [confirmId, setConfirmId] = useState<number | null>(null);
  const [replayingId, setReplayingId] = useState<number | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await listDLQ(handler, { limit: 100, status });
      setItems(res.items ?? []);
      setQueried(true);
    } catch (err) {
      setError(friendlyMessage(err));
      setItems([]);
    } finally {
      setLoading(false);
    }
  }, [handler, status]);

  const doReplay = useCallback(
    async (id: number) => {
      setReplayingId(id);
      setConfirmId(null);
      setError(null);
      setNotice(null);
      try {
        const res = await replayDLQ(id);
        setNotice(`记录 #${id} 重放${res.replayed ? '成功' : '已提交'}（状态：${res.item?.status ?? '未知'}）。`);
        await load();
      } catch (err) {
        setError(friendlyMessage(err));
      } finally {
        setReplayingId(null);
      }
    },
    [load],
  );

  return (
    <div className="flex flex-col gap-4">
      {error && <Banner kind="error" text={error} />}
      {notice && <Banner kind="ok" text={notice} />}
      <SectionCard
        title="DLQ 死信队列"
        icon={<Inbox className="size-4 text-primary-600 dark:text-primary-300" />}
        action={
          <Button size="sm" onClick={() => void load()} disabled={loading}>
            {loading ? <Loader2 className="size-4 animate-spin" /> : <Search />}
            查询
          </Button>
        }
      >
        <div className="mb-4 flex flex-wrap items-end gap-2">
          <Filter label="handler (EventKind)">
            <select value={handler} onChange={(e) => setHandler(e.target.value)} className={selectCls}>
              {DLQ_EVENT_KINDS.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </select>
          </Filter>
          <Filter label="status">
            <select value={status} onChange={(e) => setStatus(e.target.value)} className={selectCls}>
              <option value="all">全部</option>
              {DLQ_STATUSES.map((s) => (
                <option key={s} value={s}>
                  {dlqStatusLabel(s)}
                </option>
              ))}
            </select>
          </Filter>
        </div>

        {!queried ? (
          <Empty text="选择 handler 后点「查询」加载死信记录。" />
        ) : loading && items.length === 0 ? (
          <LoadingRow text="加载死信记录中…" />
        ) : items.length === 0 ? (
          <Empty text="该 handler 下无匹配记录。" />
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <TableHead>lane</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>失败原因</TableHead>
                  <TableHead>重试次</TableHead>
                  <TableHead>失败时间</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((r) => (
                  <TableRow key={r.id}>
                    <TableCell className="font-mono text-xs tabular-nums">#{r.id}</TableCell>
                    <TableCell className="text-xs text-accent-500">{r.lane}</TableCell>
                    <TableCell>
                      <Badge variant={dlqStatusVariant(r.status)}>{dlqStatusLabel(r.status)}</Badge>
                    </TableCell>
                    <TableCell className="max-w-xs truncate text-xs text-accent-600 dark:text-accent-300" title={r.failure_reason}>
                      {r.failure_reason || '—'}
                    </TableCell>
                    <TableCell className="text-xs tabular-nums text-accent-500">{r.replay_attempts}</TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-accent-400">{fmtTime(r.failure_at)}</TableCell>
                    <TableCell className="text-right">
                      {confirmId === r.id ? (
                        <div className="flex items-center justify-end gap-1.5">
                          <span className="text-[11px] text-accent-500">确认重放？</span>
                          <Button size="sm" onClick={() => void doReplay(r.id)} disabled={replayingId !== null}>
                            {replayingId === r.id ? <Loader2 className="size-4 animate-spin" /> : <CheckCircle2 />}
                            确认
                          </Button>
                          <Button size="sm" variant="ghost" onClick={() => setConfirmId(null)} disabled={replayingId !== null}>
                            <X />
                          </Button>
                        </div>
                      ) : (
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => setConfirmId(r.id)}
                          disabled={replayingId !== null}
                          title="重放（不幂等，请谨慎）"
                        >
                          <Play />
                          重放
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </SectionCard>
    </div>
  );
}

// =====================================================================================
// 缓存 L2 tab —— stats 卡 + 每 vendor/model 指标 + entries 表（按 key 清除，二次确认）。
// =====================================================================================

function CacheTab() {
  const [stats, setStats] = useState<L2CacheStats | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [confirmKey, setConfirmKey] = useState<string | null>(null);
  const [evictingKey, setEvictingKey] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await getL2CacheStats();
      setStats(res);
    } catch (err) {
      setError(friendlyMessage(err));
      setStats(null);
    } finally {
      setLoading(false);
    }
  }, []);

  const doEvict = useCallback(
    async (key: string) => {
      setEvictingKey(key);
      setConfirmKey(null);
      setError(null);
      setNotice(null);
      try {
        const res = await evictL2CacheKey(key);
        setNotice(`缓存键已${res.deleted ? '清除' : '不存在'}。`);
        await load();
      } catch (err) {
        setError(friendlyMessage(err));
      } finally {
        setEvictingKey(null);
      }
    },
    [load],
  );

  const fmtBytes = (n: number) => (n < 1024 ? `${n} B` : n < 1048576 ? `${(n / 1024).toFixed(1)} KB` : `${(n / 1048576).toFixed(2)} MB`);

  return (
    <div className="flex flex-col gap-4">
      {error && <Banner kind="error" text={error} />}
      {notice && <Banner kind="ok" text={notice} />}
      <SectionCard
        title="缓存 L2"
        icon={<HardDrive className="size-4 text-primary-600 dark:text-primary-300" />}
        action={
          <Button size="sm" onClick={() => void load()} disabled={loading}>
            {loading ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw />}
            加载
          </Button>
        }
      >
        {!stats ? (
          loading ? (
            <LoadingRow text="加载缓存统计中…" />
          ) : (
            <Empty text="点「加载」查看 L2 缓存统计与条目。" />
          )
        ) : (
          <div className="flex flex-col gap-4">
            <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
              <Stat label="启用" value={stats.enabled ? '是' : '否'} />
              <Stat label="占用 / 上限" value={`${fmtBytes(stats.size_bytes)} / ${fmtBytes(stats.max_size_bytes)}`} />
              <Stat label="TTL" value={`${stats.ttl_seconds}s`} />
              <Stat label="条目数" value={String(stats.entries?.length ?? 0)} />
            </div>

            {stats.metrics && Object.keys(stats.metrics).length > 0 && (
              <div className="overflow-x-auto">
                <div className="mb-1 text-xs text-accent-500">命中指标（vendor/model）</div>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>vendor=model</TableHead>
                      <TableHead className="text-right">命中</TableHead>
                      <TableHead className="text-right">未命中</TableHead>
                      <TableHead className="text-right">字节</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {Object.entries(stats.metrics).map(([label, m]) => (
                      <TableRow key={label}>
                        <TableCell className="font-mono text-[11px]">{label}</TableCell>
                        <TableCell className="text-right tabular-nums text-emerald-600 dark:text-emerald-400">{m.hit_total}</TableCell>
                        <TableCell className="text-right tabular-nums text-accent-500">{m.miss_total}</TableCell>
                        <TableCell className="text-right tabular-nums text-accent-500">{fmtBytes(m.size_bytes)}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}

            {stats.entries && stats.entries.length > 0 ? (
              <div className="overflow-x-auto">
                <div className="mb-1 text-xs text-accent-500">缓存条目</div>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>key</TableHead>
                      <TableHead>vendor / model</TableHead>
                      <TableHead className="text-right">大小</TableHead>
                      <TableHead>过期</TableHead>
                      <TableHead className="text-right">操作</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {stats.entries.map((e) => (
                      <TableRow key={e.key}>
                        <TableCell className="max-w-xs truncate font-mono text-[11px]" title={e.key}>
                          {e.key}
                        </TableCell>
                        <TableCell className="text-xs text-accent-600 dark:text-accent-300">
                          {e.vendor} / {e.model}
                        </TableCell>
                        <TableCell className="text-right tabular-nums text-accent-500">{fmtBytes(e.size_bytes)}</TableCell>
                        <TableCell className="whitespace-nowrap text-xs text-accent-400">{fmtTime(e.expires_at)}</TableCell>
                        <TableCell className="text-right">
                          {confirmKey === e.key ? (
                            <div className="flex items-center justify-end gap-1.5">
                              <span className="text-[11px] text-accent-500">确认清除？</span>
                              <Button size="sm" variant="destructive" onClick={() => void doEvict(e.key)} disabled={evictingKey !== null}>
                                {evictingKey === e.key ? <Loader2 className="size-4 animate-spin" /> : <CheckCircle2 />}
                                确认
                              </Button>
                              <Button size="sm" variant="ghost" onClick={() => setConfirmKey(null)} disabled={evictingKey !== null}>
                                <X />
                              </Button>
                            </div>
                          ) : (
                            <Button size="sm" variant="ghost" onClick={() => setConfirmKey(e.key)} disabled={evictingKey !== null} title="清除该缓存键">
                              <Trash2 />
                            </Button>
                          )}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            ) : (
              <Empty text="无缓存条目。" />
            )}
          </div>
        )}
      </SectionCard>
    </div>
  );
}

// ---- 共享小件 ----

function Filter({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <label className={labelCls}>{label}</label>
      {children}
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardContent className="flex flex-col gap-1 p-4">
        <span className="text-xs text-accent-500 dark:text-accent-400">{label}</span>
        <span className="text-base font-bold tabular-nums text-accent-950 dark:text-white">{value}</span>
      </CardContent>
    </Card>
  );
}

function Empty({ text }: { text: string }) {
  return (
    <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
      {text}
    </div>
  );
}

function LoadingRow({ text }: { text: string }) {
  return (
    <div className="flex items-center justify-center gap-2 py-12 text-sm text-accent-400">
      <Loader2 className="size-5 animate-spin" /> {text}
    </div>
  );
}
