'use client';

// admin 用户管理控制台 —— 管理 token 轨（lib/api/adminUsers.ts，从 localStorage huakai_admin_token
// 取 Bearer，非 session 用户面）。功能：用户列表（分页 + 邮箱搜索 + 状态过滤）+ 2FA 普及率卡 +
// 行操作（启停 / 调余额 / 余额历史 / 改备注 / 改分组 / 解锁）+ 余额调整弹窗（增加余额 + 备注）。
//
// 端点全部读后端 admin handler 真码确认（见 lib/api/adminUsers.ts 头注）。tenant_id：platform_admin
// 必带（页内输入，单租户默认 1）；tenant_operator 可省（用自身 scope，但此处仍传以兼容两种角色）。
//
// 借鉴（CLEAN-ROOM，CLAUDE.md §11/§12，仅功能/字段/动作形态，未抄码）：
//   - sub2api(LGPL) views/admin/UsersView.vue + components/admin/user/*：列表 page+search+status 过滤、
//     行动作集合、余额历史弹窗「类型/金额/时间」、余额调整弹窗「金额 + 备注」。
//   - new-api(AGPL) 用户管理页：状态徽章 + 启停 + 备注/分组运营形态。
//   三态骨架 / 徽章配色 / 卡片/表格样式沿用 HUAKAI 自有 app/subscriptions/page.tsx。

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertCircle,
  CheckCircle2,
  History,
  Loader2,
  Lock,
  RefreshCw,
  Search,
  ShieldCheck,
  Tag,
  Unlock,
  UserCog,
  Users,
  Wallet,
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
  adjustBalance,
  formatBalance,
  formatDateTime,
  getBalanceHistory,
  getTwoFAStats,
  listUsers,
  newIdempotencyKey,
  setGroup,
  setRemark,
  setStatus,
  statusBadgeVariant,
  statusLabel,
  unlockUser,
  type AdminUser,
  type BalanceHistoryEntry,
  type TwoFAStats,
} from '@/lib/api/adminUsers';
import { cn } from '@/lib/utils';

const PAGE_SIZE = 20;
const DEFAULT_TENANT_ID = 1; // 单租户部署默认（siteConfig.DEFAULT_TENANT_ID 同值）

type StatusFilter = 'all' | 'active' | 'disabled';

// ---- 主页面 ----

export default function AdminUsersPage() {
  // tenant_id：platform_admin 必带；输入框默认 1。
  const [tenantId, setTenantId] = useState<number>(DEFAULT_TENANT_ID);
  const [searchInput, setSearchInput] = useState('');
  const [query, setQuery] = useState(''); // 已提交的搜索词
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [page, setPage] = useState(0); // 0-based

  const [users, setUsers] = useState<AdminUser[]>([]);
  const [twoFA, setTwoFA] = useState<TwoFAStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [actionKey, setActionKey] = useState<string | null>(null); // 进行中的行动作标识

  // 弹窗 / 抽屉
  const [balanceTarget, setBalanceTarget] = useState<AdminUser | null>(null);
  const [historyTarget, setHistoryTarget] = useState<AdminUser | null>(null);
  const [editTarget, setEditTarget] = useState<AdminUser | null>(null);

  const offset = page * PAGE_SIZE;

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      // 列表请求 limit 多取 1 条用于「是否有下一页」判定（后端无 total）。
      const [list, stats] = await Promise.all([
        listUsers({ q: query, limit: PAGE_SIZE + 1, offset, tenant_id: tenantId }),
        getTwoFAStats(tenantId).catch(() => null), // 2FA 卡可独立失败，不拖垮列表
      ]);
      setUsers(list.items ?? []);
      setTwoFA(stats);
    } catch (err) {
      setError(friendlyMessage(err));
      setUsers([]);
    } finally {
      setLoading(false);
    }
  }, [query, offset, tenantId]);

  useEffect(() => {
    void load();
  }, [load]);

  // 客户端状态过滤（后端列表端点不支持 status 过滤参数 —— 仅对当前页生效）。
  const visibleUsers = useMemo(() => {
    if (statusFilter === 'all') return users.slice(0, PAGE_SIZE);
    return users.filter((u) => u.status === statusFilter).slice(0, PAGE_SIZE);
  }, [users, statusFilter]);

  const hasNextPage = users.length > PAGE_SIZE;

  function submitSearch() {
    setQuery(searchInput.trim());
    setPage(0);
  }

  const handleToggleStatus = useCallback(
    async (user: AdminUser) => {
      const next = user.status === 'active' ? 'disabled' : 'active';
      setActionKey(`status-${user.id}`);
      setError(null);
      setNotice(null);
      try {
        await setStatus(user.id, next, { tenant_id: tenantId });
        setNotice(`用户 #${user.id} 已${next === 'active' ? '启用' : '停用'}。`);
        await load();
      } catch (err) {
        setError(friendlyMessage(err));
      } finally {
        setActionKey(null);
      }
    },
    [tenantId, load],
  );

  const handleUnlock = useCallback(
    async (user: AdminUser) => {
      setActionKey(`unlock-${user.id}`);
      setError(null);
      setNotice(null);
      try {
        await unlockUser(user.id, tenantId);
        setNotice(`用户 #${user.id} 的登录锁定已解除。`);
        await load();
      } catch (err) {
        setError(friendlyMessage(err));
      } finally {
        setActionKey(null);
      }
    },
    [tenantId, load],
  );

  if (loading && users.length === 0) {
    return (
      <div className="mx-auto flex max-w-6xl flex-col gap-5">
        <PageHeader />
        <div className="flex items-center justify-center gap-2 py-20 text-sm text-accent-400">
          <Loader2 className="size-5 animate-spin" /> 加载用户列表中…
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-5">
      <div className="flex items-start justify-between gap-3">
        <PageHeader />
        <Button onClick={() => void load()} size="sm" variant="outline" disabled={actionKey !== null}>
          <RefreshCw className={cn(loading && 'animate-spin')} />
          刷新
        </Button>
      </div>

      {error && (
        <div className="flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
          <AlertCircle className="mt-0.5 size-4 shrink-0" />
          <span>{error}</span>
        </div>
      )}
      {notice && (
        <div className="flex items-start gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">
          <CheckCircle2 className="mt-0.5 size-4 shrink-0" />
          <span>{notice}</span>
        </div>
      )}

      {/* 2FA 普及率 + 控制条 */}
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
          <CardHeader className="p-5 pb-3">
            <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
              <ShieldCheck className="size-4 text-primary-600 dark:text-primary-300" />
              两步验证普及率
            </CardTitle>
          </CardHeader>
          <CardContent className="p-5 pt-0">
            {twoFA ? (
              <div className="flex items-end gap-3">
                <div className="text-3xl font-bold tabular-nums text-accent-950 dark:text-white">
                  {(twoFA.enabled_rate * 100).toFixed(0)}%
                </div>
                <div className="pb-1 text-xs text-accent-500 dark:text-accent-400">
                  {twoFA.enabled_users.toLocaleString('zh-CN')} / {twoFA.total_users.toLocaleString('zh-CN')} 名用户已开启
                </div>
              </div>
            ) : (
              <div className="text-xs text-accent-400">统计暂不可用</div>
            )}
          </CardContent>
        </Card>

        <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70 lg:col-span-2">
          <CardContent className="flex flex-wrap items-end gap-3 p-5">
            <div className="flex flex-col gap-1">
              <label className="text-xs text-accent-500 dark:text-accent-400">租户 ID</label>
              <input
                type="number"
                min={1}
                value={tenantId}
                onChange={(e) => setTenantId(Math.max(1, Number(e.target.value) || 1))}
                onBlur={() => {
                  setPage(0);
                }}
                className="h-9 w-24 rounded-md border border-input bg-background px-3 text-sm tabular-nums"
              />
            </div>
            <div className="flex min-w-[12rem] flex-1 flex-col gap-1">
              <label className="text-xs text-accent-500 dark:text-accent-400">搜索邮箱</label>
              <div className="flex gap-2">
                <input
                  type="text"
                  value={searchInput}
                  onChange={(e) => setSearchInput(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') submitSearch();
                  }}
                  placeholder="user@example.com"
                  className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm"
                />
                <Button size="sm" variant="outline" onClick={submitSearch}>
                  <Search />
                </Button>
              </div>
            </div>
            <div className="flex flex-col gap-1">
              <label className="text-xs text-accent-500 dark:text-accent-400">状态（仅过滤本页）</label>
              <select
                value={statusFilter}
                onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}
                className="h-9 rounded-md border border-input bg-background px-3 text-sm"
              >
                <option value="all">全部</option>
                <option value="active">正常</option>
                <option value="disabled">已停用</option>
              </select>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* 用户列表 */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-row items-center justify-between p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <Users className="size-4 text-primary-600 dark:text-primary-300" />
            用户列表
          </CardTitle>
          <span className="text-[11px] text-accent-400 dark:text-accent-500">
            第 {page + 1} 页 · 本页 {visibleUsers.length} 条
          </span>
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {visibleUsers.length === 0 ? (
            <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
              {query || statusFilter !== 'all' ? '没有匹配的用户。' : '当前租户暂无用户。'}
            </div>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>ID / 邮箱</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>分组 / 备注</TableHead>
                    <TableHead className="text-right">余额</TableHead>
                    <TableHead>注册时间</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {visibleUsers.map((u) => {
                    const busy = actionKey === `status-${u.id}` || actionKey === `unlock-${u.id}`;
                    return (
                      <TableRow key={u.id}>
                        <TableCell>
                          <div className="font-medium text-accent-900 dark:text-accent-100">{u.email || '（无邮箱）'}</div>
                          <div className="text-[11px] text-accent-400">
                            #{u.id} · {u.role}
                          </div>
                        </TableCell>
                        <TableCell>
                          <Badge variant={statusBadgeVariant(u.status)}>{statusLabel(u.status)}</Badge>
                        </TableCell>
                        <TableCell className="max-w-[16rem]">
                          <div className="flex items-center gap-1 text-xs text-accent-600 dark:text-accent-300">
                            <Tag className="size-3 shrink-0" />
                            {u.user_group || 'default'}
                          </div>
                          {u.remark && (
                            <div className="mt-0.5 truncate text-[11px] text-accent-400" title={u.remark}>
                              {u.remark}
                            </div>
                          )}
                        </TableCell>
                        <TableCell className="text-right font-mono text-sm tabular-nums text-accent-900 dark:text-accent-100">
                          {formatBalance(u.balance)}
                        </TableCell>
                        <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                          {formatDateTime(u.created_at)}
                        </TableCell>
                        <TableCell>
                          <div className="flex flex-wrap items-center justify-end gap-1.5">
                            <Button
                              size="sm"
                              variant={u.status === 'active' ? 'outline' : 'default'}
                              onClick={() => void handleToggleStatus(u)}
                              disabled={actionKey !== null}
                              title={u.status === 'active' ? '停用' : '启用'}
                            >
                              {busy && actionKey === `status-${u.id}` ? (
                                <Loader2 className="size-4 animate-spin" />
                              ) : u.status === 'active' ? (
                                <Lock />
                              ) : (
                                <Unlock />
                              )}
                              {u.status === 'active' ? '停用' : '启用'}
                            </Button>
                            <Button
                              size="sm"
                              variant="outline"
                              onClick={() => {
                                setBalanceTarget(u);
                                setError(null);
                                setNotice(null);
                              }}
                              disabled={actionKey !== null}
                            >
                              <Wallet />
                              调余额
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              onClick={() => {
                                setEditTarget(u);
                                setError(null);
                                setNotice(null);
                              }}
                              disabled={actionKey !== null}
                              title="改分组 / 备注"
                            >
                              <UserCog />
                            </Button>
                            <Button
                              size="sm"
                              variant="ghost"
                              onClick={() => setHistoryTarget(u)}
                              disabled={actionKey !== null}
                              title="余额历史"
                            >
                              <History />
                            </Button>
                            {u.status === 'locked' && (
                              <Button
                                size="sm"
                                variant="secondary"
                                onClick={() => void handleUnlock(u)}
                                disabled={actionKey !== null}
                                title="解除登录锁定"
                              >
                                {busy && actionKey === `unlock-${u.id}` ? (
                                  <Loader2 className="size-4 animate-spin" />
                                ) : (
                                  <Unlock />
                                )}
                                解锁
                              </Button>
                            )}
                          </div>
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
          )}

          {/* 分页 */}
          <div className="mt-4 flex items-center justify-between">
            <Button
              size="sm"
              variant="outline"
              onClick={() => setPage((p) => Math.max(0, p - 1))}
              disabled={page === 0 || loading}
            >
              上一页
            </Button>
            <span className="text-xs text-accent-400">第 {page + 1} 页</span>
            <Button size="sm" variant="outline" onClick={() => setPage((p) => p + 1)} disabled={!hasNextPage || loading}>
              下一页
            </Button>
          </div>
        </CardContent>
      </Card>

      {/* 余额调整弹窗 */}
      {balanceTarget && (
        <BalanceAdjustModal
          user={balanceTarget}
          tenantId={tenantId}
          onClose={() => setBalanceTarget(null)}
          onDone={(msg) => {
            setBalanceTarget(null);
            setNotice(msg);
            void load();
          }}
        />
      )}

      {/* 改分组 / 备注弹窗 */}
      {editTarget && (
        <EditUserModal
          user={editTarget}
          tenantId={tenantId}
          onClose={() => setEditTarget(null)}
          onDone={(msg) => {
            setEditTarget(null);
            setNotice(msg);
            void load();
          }}
        />
      )}

      {/* 余额历史抽屉 */}
      {historyTarget && (
        <BalanceHistoryModal user={historyTarget} tenantId={tenantId} onClose={() => setHistoryTarget(null)} />
      )}
    </div>
  );
}

function PageHeader() {
  return (
    <div className="flex flex-col gap-1">
      <h1 className="text-xl font-bold text-accent-950 dark:text-white">用户管理</h1>
      <p className="text-sm text-accent-500 dark:text-accent-400">
        管理终端用户：搜索、启停、调余额、查余额历史、改分组 / 备注。走管理 token，需指定租户 ID。
      </p>
    </div>
  );
}

// ---- 弹窗外壳 ----

function ModalShell({
  title,
  icon,
  onClose,
  children,
  wide,
}: {
  title: string;
  icon: React.ReactNode;
  onClose: () => void;
  children: React.ReactNode;
  wide?: boolean;
}) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onClick={onClose}
      role="presentation"
    >
      <div
        className={cn(
          'w-full rounded-xl border border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900',
          wide ? 'max-w-2xl' : 'max-w-md',
        )}
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

// ---- 余额调整弹窗（增加余额）----

function BalanceAdjustModal({
  user,
  tenantId,
  onClose,
  onDone,
}: {
  user: AdminUser;
  tenantId: number;
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const [amount, setAmount] = useState('');
  const [reason, setReason] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  async function submit() {
    const amt = amount.trim();
    const parsed = Number(amt);
    if (!amt || !Number.isFinite(parsed) || parsed <= 0) {
      setLocalError('请输入大于 0 的入账金额（后端暂不支持扣减，只能充值）。');
      return;
    }
    if (reason.trim() === '') {
      setLocalError('请填写调整原因（用于审计与幂等记录）。');
      return;
    }
    setSubmitting(true);
    setLocalError(null);
    try {
      const res = await adjustBalance({
        tenant_id: tenantId,
        user_id: user.id,
        amount: amt,
        reason: reason.trim(),
        idempotency_key: newIdempotencyKey(),
      });
      onDone(`已为用户 #${user.id} 入账 ${amt}，最新余额 ${formatBalance(res.net_balance)} ${res.currency_code}。`);
    } catch (err) {
      setLocalError(friendlyMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <ModalShell title="调整余额（充值入账）" icon={<Wallet className="size-4 text-primary-600 dark:text-primary-300" />} onClose={onClose}>
      <div className="flex flex-col gap-3">
        <div className="rounded-lg border border-accent-200 bg-accent-50 p-3 text-xs dark:border-accent-800 dark:bg-accent-950/40">
          <div className="text-accent-600 dark:text-accent-300">用户 {user.email || `#${user.id}`}</div>
          <div className="mt-0.5 text-accent-400">
            当前余额 <span className="font-mono tabular-nums">{formatBalance(user.balance)}</span>
          </div>
        </div>
        <div className="flex flex-col gap-1">
          <label className="text-xs text-accent-500 dark:text-accent-400">入账金额（正数，例 10.00）</label>
          <input
            type="text"
            inputMode="decimal"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            placeholder="10.00"
            className="h-9 rounded-md border border-input bg-background px-3 text-sm tabular-nums"
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className="text-xs text-accent-500 dark:text-accent-400">调整原因（必填，审计用）</label>
          <textarea
            rows={2}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="例：人工充值 / 客服补偿"
            className="rounded-md border border-input bg-background px-3 py-2 text-sm"
          />
        </div>
        <p className="text-[11px] text-accent-400">
          说明：后端余额调整为「增量入账」（仅正数），扣减当前未开放（admin_debit_not_yet_supported）。该端点要求 platform_admin 角色。
        </p>
        {localError && (
          <div className="flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
            <AlertCircle className="mt-0.5 size-3.5 shrink-0" />
            <span>{localError}</span>
          </div>
        )}
        <div className="flex justify-end gap-2 pt-1">
          <Button size="sm" variant="outline" onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button size="sm" onClick={() => void submit()} disabled={submitting}>
            {submitting ? <Loader2 className="size-4 animate-spin" /> : <Wallet />}
            确认入账
          </Button>
        </div>
      </div>
    </ModalShell>
  );
}

// ---- 改分组 / 备注弹窗 ----

function EditUserModal({
  user,
  tenantId,
  onClose,
  onDone,
}: {
  user: AdminUser;
  tenantId: number;
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const [group, setGroupValue] = useState(user.user_group || '');
  const [remark, setRemarkValue] = useState(user.remark || '');
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  async function submit() {
    const g = group.trim();
    const r = remark.trim();
    if (g !== '' && g.length > 64) {
      setLocalError('分组名需为 1..64 字。');
      return;
    }
    if (r.length > 1024) {
      setLocalError('备注需 ≤ 1024 字。');
      return;
    }
    setSubmitting(true);
    setLocalError(null);
    try {
      // 仅提交有变化的字段（分组后端要求非空 1..64，故空值不提交；备注允许清空）。
      const changedGroup = g !== '' && g !== (user.user_group || '');
      const changedRemark = r !== (user.remark || '');
      if (changedGroup) await setGroup(user.id, g, tenantId);
      if (changedRemark) await setRemark(user.id, r, tenantId);
      if (!changedGroup && !changedRemark) {
        setLocalError('没有需要保存的改动。');
        setSubmitting(false);
        return;
      }
      onDone(`用户 #${user.id} 的资料已更新。`);
    } catch (err) {
      setLocalError(friendlyMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <ModalShell title="编辑用户资料" icon={<UserCog className="size-4 text-primary-600 dark:text-primary-300" />} onClose={onClose}>
      <div className="flex flex-col gap-3">
        <div className="rounded-lg border border-accent-200 bg-accent-50 p-3 text-xs text-accent-600 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-300">
          {user.email || `#${user.id}`}
        </div>
        <div className="flex flex-col gap-1">
          <label className="text-xs text-accent-500 dark:text-accent-400">路由分组（user_group，1..64 字）</label>
          <input
            type="text"
            value={group}
            onChange={(e) => setGroupValue(e.target.value)}
            placeholder="default / premium …"
            className="h-9 rounded-md border border-input bg-background px-3 text-sm"
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className="text-xs text-accent-500 dark:text-accent-400">管理备注（≤ 1024 字，可清空）</label>
          <textarea
            rows={3}
            value={remark}
            onChange={(e) => setRemarkValue(e.target.value)}
            placeholder="内部备注，仅运营可见"
            className="rounded-md border border-input bg-background px-3 py-2 text-sm"
          />
        </div>
        {localError && (
          <div className="flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
            <AlertCircle className="mt-0.5 size-3.5 shrink-0" />
            <span>{localError}</span>
          </div>
        )}
        <div className="flex justify-end gap-2 pt-1">
          <Button size="sm" variant="outline" onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button size="sm" onClick={() => void submit()} disabled={submitting}>
            {submitting ? <Loader2 className="size-4 animate-spin" /> : <CheckCircle2 />}
            保存
          </Button>
        </div>
      </div>
    </ModalShell>
  );
}

// ---- 余额历史抽屉 ----

function BalanceHistoryModal({
  user,
  tenantId,
  onClose,
}: {
  user: AdminUser;
  tenantId: number;
  onClose: () => void;
}) {
  const [entries, setEntries] = useState<BalanceHistoryEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [localError, setLocalError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setLocalError(null);
    getBalanceHistory(user.id, { limit: 50, tenant_id: tenantId })
      .then((resp) => {
        if (alive) setEntries(resp.items ?? []);
      })
      .catch((err) => {
        if (alive) setLocalError(friendlyMessage(err));
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [user.id, tenantId]);

  return (
    <ModalShell
      title={`余额历史 · ${user.email || `#${user.id}`}`}
      icon={<History className="size-4 text-primary-600 dark:text-primary-300" />}
      onClose={onClose}
      wide
    >
      {loading ? (
        <div className="flex items-center justify-center gap-2 py-12 text-sm text-accent-400">
          <Loader2 className="size-5 animate-spin" /> 加载中…
        </div>
      ) : localError ? (
        <div className="flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
          <AlertCircle className="mt-0.5 size-3.5 shrink-0" />
          <span>{localError}</span>
        </div>
      ) : entries.length === 0 ? (
        <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
          暂无余额变动记录。
        </div>
      ) : (
        <div className="max-h-[60vh] overflow-y-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>类型</TableHead>
                <TableHead className="text-right">金额</TableHead>
                <TableHead>来源</TableHead>
                <TableHead>时间</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {entries.map((e) => {
                const amt = Number(e.amount);
                const positive = Number.isFinite(amt) ? amt >= 0 : !e.amount.startsWith('-');
                return (
                  <TableRow key={e.id}>
                    <TableCell className="text-xs text-accent-700 dark:text-accent-200">{e.event_type}</TableCell>
                    <TableCell
                      className={cn(
                        'text-right font-mono text-sm tabular-nums',
                        positive ? 'text-emerald-600 dark:text-emerald-400' : 'text-red-600 dark:text-red-400',
                      )}
                    >
                      {positive ? '+' : ''}
                      {formatBalance(e.amount)}
                    </TableCell>
                    <TableCell className="text-xs text-accent-500 dark:text-accent-400">
                      {e.source_type}
                      {e.source_id ? ` #${e.source_id}` : ''}
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                      {formatDateTime(e.occurred_at)}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}
    </ModalShell>
  );
}
