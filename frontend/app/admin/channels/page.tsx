'use client';

// Admin 渠道健康面板（F-CH-002 运维读写面）。
// 走管理 token（lib/api/adminChannels.ts → client.ts apiGet/apiPost，Bearer huakai_admin_token）。
//
// 布局（自研，借鉴功能形态，CLEAN-ROOM CLAUDE.md §11/§12）：
//   - 顶部 tenant_id 输入 + 刷新（后端所有渠道健康端点都强制 tenant_id 正整数）。
//   - 汇总卡片行：各健康态计数(active/degraded/cooling_down/ramping/disabled/manual_paused)
//     + 总数 + 最早冷却时间。对照 sub2api ChannelMonitorView 的「状态分布 + 可用率」概览形态。
//   - 渠道健康表：state 徽章 + score + 冷却倒计时 + vendor/credential + 最近信号。
//     对照 new-api channel ColumnDefs 的「状态 + 响应/信号」列形态，映射到 HUAKAI 真实状态机。
//   - 每行动作：手动暂停 / 解除暂停 / 强制激活（均需填 reason，后端强制非空）。
//   - 详情抽屉：单渠道 state + 审计事件流（getChannelHealthDetail）。
//
// 三态容错（loading/空/错误）齐全；倒计时本地每秒自刷。

import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react';
import {
  Activity,
  AlertTriangle,
  Ban,
  ChevronRight,
  Clock,
  Loader2,
  Pause,
  Play,
  RefreshCw,
  ShieldCheck,
  Snowflake,
  TrendingUp,
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
  STATE_LABEL,
  cooldownRemainingSeconds,
  forceActiveChannel,
  formatCountdown,
  fmtDateTime,
  getChannelHealthDetail,
  getChannelHealthSummary,
  listChannelHealth,
  pauseChannel,
  resumeChannel,
  stateBadgeVariant,
  type ChannelHealthDetailResponse,
  type ChannelHealthOverrideRequest,
  type ChannelHealthRecord,
  type ChannelHealthStateName,
  type ChannelHealthSummary,
} from '@/lib/api/adminChannels';
import { cn } from '@/lib/utils';

// 汇总卡片要展示的状态顺序与图标。
const SUMMARY_STATES: { state: ChannelHealthStateName; icon: typeof Activity }[] = [
  { state: 'active', icon: ShieldCheck },
  { state: 'degraded', icon: AlertTriangle },
  { state: 'cooling_down', icon: Snowflake },
  { state: 'ramping', icon: TrendingUp },
  { state: 'disabled', icon: Ban },
  { state: 'manual_paused', icon: Pause },
];

type OverrideAction = 'pause' | 'resume' | 'force-active';

const ACTION_LABEL: Record<OverrideAction, string> = {
  pause: '手动暂停',
  resume: '解除暂停',
  'force-active': '强制激活',
};

export default function AdminChannelsPage() {
  // tenant_id 受控字符串（避免数字受控坑），有效后才发请求。
  const [tenantInput, setTenantInput] = useState('1');
  const tenantId = useMemo(() => {
    const n = Number(tenantInput.trim());
    return Number.isInteger(n) && n > 0 ? n : null;
  }, [tenantInput]);

  const [items, setItems] = useState<ChannelHealthRecord[]>([]);
  const [summary, setSummary] = useState<ChannelHealthSummary | null>(null);
  const [summaryUnavailable, setSummaryUnavailable] = useState(false);

  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  // 倒计时本地时钟（每秒一跳，驱动冷却剩余时间重算）。
  const [nowMs, setNowMs] = useState(() => Date.now());

  // 动作面板：当前操作的渠道 + 动作 + reason。
  const [actionFor, setActionFor] = useState<{ record: ChannelHealthRecord; action: OverrideAction } | null>(null);
  const [reason, setReason] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  // 详情抽屉。
  const [detail, setDetail] = useState<ChannelHealthDetailResponse | null>(null);
  const [detailFor, setDetailFor] = useState<string | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (tenantId === null) {
      setError('tenant_id 必须是正整数');
      setItems([]);
      setSummary(null);
      return;
    }
    setLoading(true);
    setError(null);
    try {
      // 列表为主依赖；汇总独立容错（某依赖未就绪只让汇总卡显示「暂不可用」）。
      const [listRes] = await Promise.all([
        listChannelHealth({ tenant_id: tenantId, limit: 200 }),
        getChannelHealthSummary(tenantId)
          .then((s) => {
            setSummary(s);
            setSummaryUnavailable(false);
          })
          .catch(() => {
            setSummary(null);
            setSummaryUnavailable(true);
          }),
      ]);
      setItems(listRes.items ?? []);
    } catch (err) {
      setError(friendlyMessage(err));
      setItems([]);
    } finally {
      setLoading(false);
    }
  }, [tenantId]);

  useEffect(() => {
    void load();
  }, [load]);

  // 倒计时时钟。
  useEffect(() => {
    const id = window.setInterval(() => setNowMs(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, []);

  // 打开动作面板。
  function openAction(record: ChannelHealthRecord, action: OverrideAction) {
    setActionFor({ record, action });
    setReason('');
    setActionError(null);
  }

  // 提交 override 动作。
  async function submitAction() {
    if (!actionFor || tenantId === null) return;
    const { record, action } = actionFor;
    const trimmedReason = reason.trim();
    if (!trimmedReason) {
      setActionError('reason 必填');
      return;
    }
    setSubmitting(true);
    setActionError(null);
    const body: ChannelHealthOverrideRequest = {
      tenant_id: tenantId,
      vendor: record.vendor,
      account_credential_id: record.account_credential_id,
      credential_version: record.credential_version,
      reason: trimmedReason,
    };
    try {
      if (action === 'pause') {
        await pauseChannel(record.provider_account_id, body);
      } else if (action === 'resume') {
        await resumeChannel(record.provider_account_id, body);
      } else {
        await forceActiveChannel(record.provider_account_id, body);
      }
      setNotice(`${ACTION_LABEL[action]}成功：${record.channel_id}`);
      setActionFor(null);
      await load();
    } catch (err) {
      setActionError(friendlyMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  // 打开详情抽屉。
  async function openDetail(record: ChannelHealthRecord) {
    if (tenantId === null) return;
    setDetailFor(record.channel_id);
    setDetail(null);
    setDetailError(null);
    setDetailLoading(true);
    try {
      const res = await getChannelHealthDetail(record.channel_id, tenantId);
      setDetail(res);
    } catch (err) {
      setDetailError(friendlyMessage(err));
    } finally {
      setDetailLoading(false);
    }
  }

  return (
    <div className="flex flex-col gap-6 p-6">
      {/* 标题行 + tenant 输入 + 刷新 */}
      <div className="flex flex-wrap items-end gap-4">
        <div>
          <h1 className="text-xl font-semibold text-foreground">渠道健康</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            各渠道/账号健康态、冷却倒计时与手动暂停 / 解除 / 强制激活
          </p>
        </div>
        <div className="ml-auto flex items-end gap-2">
          <div className="flex flex-col gap-1">
            <label className="text-xs text-muted-foreground">tenant_id</label>
            <input
              type="text"
              value={tenantInput}
              onChange={(e) => setTenantInput(e.target.value)}
              className={cn(
                'h-9 w-28 rounded-md border border-input bg-background px-3 text-sm',
                'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
              )}
              placeholder="1"
            />
          </div>
          <Button variant="outline" onClick={() => void load()} disabled={loading}>
            {loading ? <Loader2 className="animate-spin" /> : <RefreshCw />}
            刷新
          </Button>
        </div>
      </div>

      {error && (
        <div className="flex items-center gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          <AlertTriangle className="size-4" />
          {error}
        </div>
      )}
      {notice && (
        <div className="flex items-center gap-2 rounded-md border border-primary/40 bg-primary/10 px-3 py-2 text-sm text-primary">
          <ShieldCheck className="size-4" />
          {notice}
        </div>
      )}

      {/* 汇总卡片行 */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        {SUMMARY_STATES.map(({ state, icon: Icon }) => {
          const count = summary?.by_state?.[state] ?? 0;
          return (
            <Card key={state}>
              <CardContent className="flex items-center gap-3 p-4">
                <Icon className="size-5 text-muted-foreground" />
                <div className="min-w-0">
                  <div className="text-2xl font-semibold tabular-nums">
                    {summaryUnavailable ? '—' : count}
                  </div>
                  <div className="truncate text-xs text-muted-foreground">{STATE_LABEL[state]}</div>
                </div>
              </CardContent>
            </Card>
          );
        })}
      </div>

      {/* 总数 + 最早冷却 */}
      {summary && !summaryUnavailable && (
        <div className="flex flex-wrap gap-4 text-sm text-muted-foreground">
          <span>
            总计 <span className="font-semibold text-foreground tabular-nums">{summary.total}</span> 个渠道
          </span>
          {summary.oldest_cooldown_at && (
            <span className="flex items-center gap-1">
              <Snowflake className="size-4" />
              最早冷却起始 {fmtDateTime(summary.oldest_cooldown_at)}
            </span>
          )}
        </div>
      )}
      {summaryUnavailable && (
        <div className="text-sm text-muted-foreground">汇总暂不可用（端点未就绪）</div>
      )}

      {/* 渠道健康表 */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">渠道列表</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>状态</TableHead>
                <TableHead>渠道</TableHead>
                <TableHead>vendor</TableHead>
                <TableHead className="text-right">score</TableHead>
                <TableHead>冷却剩余</TableHead>
                <TableHead>最近信号</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.length === 0 && (
                <TableRow>
                  <TableCell colSpan={7} className="py-10 text-center text-sm text-muted-foreground">
                    {loading ? '加载中…' : '（无渠道健康记录）'}
                  </TableCell>
                </TableRow>
              )}
              {items.map((rec) => {
                const remaining = cooldownRemainingSeconds(rec.cooldown_until, nowMs);
                return (
                  <TableRow key={rec.channel_id}>
                    <TableCell>
                      <Badge variant={stateBadgeVariant(rec.state)}>
                        {STATE_LABEL[rec.state] ?? rec.state}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <button
                        onClick={() => void openDetail(rec)}
                        className="flex items-center gap-1 font-mono text-xs text-foreground hover:text-primary"
                      >
                        {rec.channel_id}
                        <ChevronRight className="size-3" />
                      </button>
                      <div className="text-[11px] text-muted-foreground">
                        cred #{rec.account_credential_id} · v{rec.credential_version} · acct #{rec.provider_account_id}
                      </div>
                    </TableCell>
                    <TableCell className="text-sm">{rec.vendor}</TableCell>
                    <TableCell className="text-right tabular-nums">{rec.score.toFixed(2)}</TableCell>
                    <TableCell>
                      {remaining !== null ? (
                        <span className="flex items-center gap-1 text-sm text-amber-500">
                          <Clock className="size-3.5" />
                          {formatCountdown(remaining)}
                        </span>
                      ) : (
                        <span className="text-muted-foreground">—</span>
                      )}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {rec.last_signal_class ? (
                        <span>
                          {rec.last_signal_class}
                          {rec.last_signal_at ? ` · ${fmtDateTime(rec.last_signal_at)}` : ''}
                        </span>
                      ) : (
                        '—'
                      )}
                    </TableCell>
                    <TableCell>
                      <div className="flex justify-end gap-1.5">
                        {rec.state === 'manual_paused' ? (
                          <Button size="sm" variant="outline" onClick={() => openAction(rec, 'resume')}>
                            <Play className="size-3.5" />
                            解除
                          </Button>
                        ) : (
                          <Button size="sm" variant="outline" onClick={() => openAction(rec, 'pause')}>
                            <Pause className="size-3.5" />
                            暂停
                          </Button>
                        )}
                        {(rec.state === 'cooling_down' ||
                          rec.state === 'disabled' ||
                          rec.state === 'degraded') && (
                          <Button size="sm" variant="secondary" onClick={() => openAction(rec, 'force-active')}>
                            <Activity className="size-3.5" />
                            强制激活
                          </Button>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {/* 动作面板（reason 输入） */}
      {actionFor && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4">
          <Card className="w-full max-w-md">
            <CardHeader className="flex flex-row items-center justify-between space-y-0">
              <CardTitle className="text-base">
                {ACTION_LABEL[actionFor.action]} · {actionFor.record.channel_id}
              </CardTitle>
              <button onClick={() => setActionFor(null)} className="text-muted-foreground hover:text-foreground">
                <X className="size-4" />
              </button>
            </CardHeader>
            <CardContent className="flex flex-col gap-3">
              <div className="text-xs text-muted-foreground">
                vendor {actionFor.record.vendor} · cred #{actionFor.record.account_credential_id} · v
                {actionFor.record.credential_version}
              </div>
              {actionFor.action === 'force-active' && (
                <div className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-600">
                  <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
                  强制激活会无视当前冷却/降级，将渠道立即拉回可用。请确认风险已知。
                </div>
              )}
              <div className="flex flex-col gap-1">
                <label className="text-xs text-muted-foreground">reason（必填，记入审计）</label>
                <textarea
                  rows={3}
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                  className={cn(
                    'rounded-md border border-input bg-background px-3 py-2 text-sm',
                    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
                  )}
                  placeholder="例如：上游已确认恢复，手动放量验证"
                />
              </div>
              {actionError && <div className="text-sm text-destructive">{actionError}</div>}
              <div className="flex justify-end gap-2">
                <Button variant="ghost" onClick={() => setActionFor(null)} disabled={submitting}>
                  取消
                </Button>
                <Button
                  variant={actionFor.action === 'force-active' ? 'default' : 'secondary'}
                  onClick={() => void submitAction()}
                  disabled={submitting || !reason.trim()}
                >
                  {submitting && <Loader2 className="animate-spin" />}
                  确认{ACTION_LABEL[actionFor.action]}
                </Button>
              </div>
            </CardContent>
          </Card>
        </div>
      )}

      {/* 详情抽屉 */}
      {detailFor && (
        <div className="fixed inset-0 z-40 flex justify-end bg-black/40">
          <div className="h-full w-full max-w-xl overflow-y-auto border-l border-border bg-background p-6">
            <div className="flex items-center justify-between">
              <h2 className="font-mono text-sm font-semibold text-foreground">{detailFor}</h2>
              <button
                onClick={() => {
                  setDetailFor(null);
                  setDetail(null);
                }}
                className="text-muted-foreground hover:text-foreground"
              >
                <X className="size-5" />
              </button>
            </div>

            {detailLoading && (
              <div className="mt-8 flex items-center gap-2 text-sm text-muted-foreground">
                <Loader2 className="size-4 animate-spin" />
                加载详情…
              </div>
            )}
            {detailError && <div className="mt-6 text-sm text-destructive">{detailError}</div>}

            {detail && (
              <>
                {/* 当前状态摘要 */}
                <div className="mt-4 grid grid-cols-2 gap-x-4 gap-y-2 rounded-md border border-border p-4 text-sm">
                  <DetailRow label="状态">
                    <Badge variant={stateBadgeVariant(detail.state.state)}>
                      {STATE_LABEL[detail.state.state] ?? detail.state.state}
                    </Badge>
                  </DetailRow>
                  <DetailRow label="score">{detail.state.score.toFixed(2)}</DetailRow>
                  <DetailRow label="reason_class">{detail.state.reason_class || '—'}</DetailRow>
                  <DetailRow label="confidence">{detail.state.confidence_tier}</DetailRow>
                  <DetailRow label="policy_version">{detail.state.policy_version}</DetailRow>
                  <DetailRow label="ramp_stage">{detail.state.ramp_stage_pct}%</DetailRow>
                  <DetailRow label="冷却至">{fmtDateTime(detail.state.cooldown_until)}</DetailRow>
                  <DetailRow label="最近迁移">{fmtDateTime(detail.state.last_transition_at)}</DetailRow>
                </div>

                {/* 审计事件流 */}
                <h3 className="mt-6 text-sm font-semibold text-foreground">审计事件</h3>
                <div className="mt-2 flex flex-col gap-2">
                  {detail.audit_events.length === 0 && (
                    <div className="text-sm text-muted-foreground">（无审计事件）</div>
                  )}
                  {detail.audit_events.map((ev, i) => (
                    <div key={`${ev.event_type}-${ev.occurred_at ?? i}`} className="rounded-md border border-border p-3 text-xs">
                      <div className="flex items-center justify-between">
                        <span className="font-semibold text-foreground">{ev.event_type}</span>
                        <span className="text-muted-foreground">{fmtDateTime(ev.occurred_at)}</span>
                      </div>
                      <div className="mt-1 text-muted-foreground">
                        {ev.previous_state ? `${ev.previous_state} → ` : ''}
                        {ev.new_state}
                        {ev.reason_class ? ` · ${ev.reason_class}` : ''}
                      </div>
                      {ev.actor_id && <div className="text-muted-foreground">actor {ev.actor_id}</div>}
                    </div>
                  ))}
                </div>
              </>
            )}
          </div>
        </div>
      )}
    </div>
  );
}

// 详情键值行。
function DetailRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="text-foreground">{children}</span>
    </div>
  );
}
