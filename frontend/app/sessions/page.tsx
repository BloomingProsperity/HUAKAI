'use client';

// 会话/设备管理：列出当前账户的活跃会话族（每个 family = 一次登录/一台设备），
// 支持逐个「撤销」与「登出全部设备」。全部走 session 鉴权（lib/api/sessions.ts -> userClient）。
// 设计约束（已核对真后端 backend/internal/gatewayhttp/session_handler.go + usersession/*）：
//   - /v1/sessions/list、/v1/sessions/revoke 均为 POST + session bearer；list 响应 { families }（last_active_at DESC）。
//   - 后端无「撤销其它全部」单端点，且当前 family_id 未在前端持久化（session.ts 只存 token），
//     故无法可靠标注「当前设备」；「登出全部设备」诚实地撤销所有会话族（含本机）后清本地会话并回登录页。
//   - 撤销正在使用的那台设备会令本机下次请求 401 → userClient 刷新失败 → 自动踢回登录页。
// 借鉴（功能/字段/布局形态，非抄码）：new-api profile/passkey-card.tsx 的「安全条目列表 + 状态徽章 +
//   最近活跃时间 + 行内危险操作经 AlertDialog 二次确认」布局。sub2api/new-api 均无独立活跃会话页，本页为 HUAKAI 自有面。
import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertCircle,
  Globe,
  Loader2,
  LogOut,
  Monitor,
  RefreshCw,
  ShieldAlert,
  ShieldCheck,
  Trash2,
} from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { cn } from '@/lib/utils';
import { friendlyMessage } from '@/lib/api/errors';
import { clearSession } from '@/lib/auth/session';
import {
  deviceLabel,
  familyStatusLabel,
  familyStatusTone,
  fetchSessions,
  isRevocable,
  revokeSessionFamily,
  type SessionFamily,
  type StatusTone,
} from '@/lib/api/sessions';

function toneRing(tone: StatusTone): string {
  switch (tone) {
    case 'emerald':
      return 'bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-950/40 dark:text-emerald-300 dark:ring-emerald-900/60';
    case 'amber':
      return 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-950/40 dark:text-amber-300 dark:ring-amber-900/60';
    case 'red':
      return 'bg-red-50 text-red-700 ring-red-200 dark:bg-red-950/40 dark:text-red-300 dark:ring-red-900/60';
    default:
      return 'bg-accent-100 text-accent-600 ring-accent-200 dark:bg-accent-800 dark:text-accent-300 dark:ring-accent-700';
  }
}

function fmtDateTime(iso: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString('zh-CN');
}

function fmtRelative(iso: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  const ms = Date.now() - d.getTime();
  if (Number.isNaN(ms)) return '';
  const min = Math.floor(ms / 60000);
  if (min < 1) return '刚刚';
  if (min < 60) return `${min} 分钟前`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} 小时前`;
  const day = Math.floor(hr / 24);
  if (day < 30) return `${day} 天前`;
  return '';
}

export default function SessionsPage() {
  const [families, setFamilies] = useState<SessionFamily[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<string | null>(null);

  // 行内撤销状态：记录正在撤销的 family_id。
  const [revoking, setRevoking] = useState<string | null>(null);
  const [rowErr, setRowErr] = useState<string | null>(null);

  // 「登出全部设备」确认弹层 + 进行中状态。
  const [confirmAllOpen, setConfirmAllOpen] = useState(false);
  const [signingOutAll, setSigningOutAll] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setErr(null);
    setRowErr(null);
    try {
      const list = await fetchSessions();
      setFamilies(list);
    } catch (e) {
      setErr(friendlyMessage(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const revocable = useMemo(
    () => (families ?? []).filter((f) => isRevocable(f.status)),
    [families],
  );

  const handleRevoke = useCallback(
    async (familyID: string) => {
      setRevoking(familyID);
      setRowErr(null);
      try {
        await revokeSessionFamily(familyID);
        await load();
      } catch (e) {
        setRowErr(friendlyMessage(e));
      } finally {
        setRevoking(null);
      }
    },
    [load],
  );

  // 登出全部设备：逐个撤销可撤会话族（含本机），完成后清本地会话并回登录页。
  // 即使某条撤销失败也继续，最终强制清本地会话以保证本机确实登出。
  const handleSignOutAll = useCallback(async () => {
    setSigningOutAll(true);
    setRowErr(null);
    try {
      const targets = revocable;
      for (const f of targets) {
        try {
          await revokeSessionFamily(f.id);
        } catch {
          // 单条失败不应阻断整体登出；本机会话仍会在 finally 被清掉。
        }
      }
    } finally {
      clearSession();
      if (typeof window !== 'undefined') {
        window.location.href = '/login';
      }
    }
  }, [revocable]);

  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-5">
      <div className="flex flex-col gap-1 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="text-xl font-bold text-accent-950 dark:text-white">会话与设备</h1>
          <p className="mt-1 text-sm text-accent-500 dark:text-accent-400">
            当前账户的活跃登录会话（按设备/客户端区分），可单独撤销或一键登出全部设备。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={load} disabled={loading}>
            <RefreshCw className={loading ? 'animate-spin' : ''} />
            刷新
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setConfirmAllOpen(true)}
            disabled={loading || revocable.length === 0 || signingOutAll}
          >
            {signingOutAll ? <Loader2 className="size-4 animate-spin" /> : <LogOut className="size-4" />}
            登出全部设备
          </Button>
        </div>
      </div>

      {/* 安全提示：撤销当前设备 = 本机登出。 */}
      <div className="flex items-start gap-2.5 rounded-lg border border-accent-200 bg-accent-50/70 px-3.5 py-3 text-sm text-accent-600 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-300">
        <ShieldCheck className="mt-0.5 size-4 shrink-0 text-primary-500" />
        <span>
          若发现陌生设备或异常登录，可立即撤销对应会话。撤销当前正在使用的这台设备会令本机退出登录；
          「登出全部设备」会撤销所有会话并将本机退回登录页。
        </span>
      </div>

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-row items-center justify-between gap-3 p-5 pb-3">
          <div className="flex min-w-0 items-center gap-2.5">
            <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary-50 text-primary-600 ring-1 ring-primary-100 dark:bg-primary-950/50 dark:text-primary-300 dark:ring-primary-900/70">
              <Monitor className="size-4" />
            </span>
            <CardTitle className="text-base font-semibold tracking-normal text-accent-950 dark:text-white">
              活跃会话
            </CardTitle>
          </div>
          {families && families.length > 0 ? (
            <span className="shrink-0 text-[11px] text-accent-400 dark:text-accent-500">
              {families.length} 条会话 · {revocable.length} 条活跃
            </span>
          ) : null}
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {rowErr && (
            <div className="mb-3 flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-3 py-2.5 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              <span>{rowErr}</span>
            </div>
          )}

          {loading ? (
            <div className="flex items-center justify-center gap-2 py-10 text-sm text-accent-400">
              <Loader2 className="size-4 animate-spin" /> 加载中…
            </div>
          ) : err ? (
            <div className="flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-3 py-2.5 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
              <AlertCircle className="mt-0.5 size-4 shrink-0" />
              <span>{err}</span>
            </div>
          ) : !families || families.length === 0 ? (
            <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
              暂无活跃会话记录。
            </div>
          ) : (
            <ul className="flex flex-col gap-3">
              {families.map((f) => {
                const tone = familyStatusTone(f.status);
                const canRevoke = isRevocable(f.status);
                const rel = fmtRelative(f.last_active_at);
                const busy = revoking === f.id;
                return (
                  <li
                    key={f.id}
                    className="flex flex-col gap-3 rounded-lg border border-accent-200 bg-accent-50/60 p-3.5 sm:flex-row sm:items-center sm:justify-between dark:border-accent-800 dark:bg-accent-950/40"
                  >
                    <div className="flex min-w-0 items-start gap-3">
                      <span
                        className={cn(
                          'mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-lg ring-1 ring-inset',
                          f.status === 'suspicious'
                            ? 'bg-amber-50 text-amber-600 ring-amber-200 dark:bg-amber-950/40 dark:text-amber-300 dark:ring-amber-900/60'
                            : 'bg-white text-accent-500 ring-accent-200 dark:bg-accent-900 dark:text-accent-300 dark:ring-accent-700',
                        )}
                      >
                        {f.status === 'suspicious' ? (
                          <ShieldAlert className="size-4" />
                        ) : (
                          <Monitor className="size-4" />
                        )}
                      </span>
                      <div className="min-w-0">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="text-sm font-semibold text-accent-900 dark:text-accent-100">
                            {deviceLabel(f)}
                          </span>
                          <span
                            className={cn(
                              'inline-flex items-center rounded-md px-2 py-0.5 text-[11px] font-medium ring-1 ring-inset',
                              toneRing(tone),
                            )}
                          >
                            {familyStatusLabel(f.status)}
                          </span>
                        </div>
                        <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-accent-400 dark:text-accent-500">
                          <span className="inline-flex items-center gap-1">
                            <Globe className="size-3" />
                            {f.ip_baseline || 'IP 未知'}
                          </span>
                          <span>最后活跃 {fmtDateTime(f.last_active_at)}{rel ? `（${rel}）` : ''}</span>
                          <span>登录于 {fmtDateTime(f.created_at)}</span>
                        </div>
                      </div>
                    </div>
                    <div className="shrink-0 self-end sm:self-auto">
                      {canRevoke ? (
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => handleRevoke(f.id)}
                          disabled={busy || signingOutAll}
                          className="border-red-200 text-red-600 hover:bg-red-50 hover:text-red-700 dark:border-red-900/60 dark:text-red-300 dark:hover:bg-red-950/40"
                        >
                          {busy ? <Loader2 className="size-4 animate-spin" /> : <Trash2 className="size-4" />}
                          撤销
                        </Button>
                      ) : (
                        <span className="text-[11px] text-accent-400 dark:text-accent-500">已失效</span>
                      )}
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
        </CardContent>
      </Card>

      {/* 登出全部设备二次确认。 */}
      {confirmAllOpen && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
          role="dialog"
          aria-modal="true"
          onClick={() => {
            if (!signingOutAll) setConfirmAllOpen(false);
          }}
        >
          <div
            className="w-full max-w-sm rounded-xl border border-accent-200 bg-white p-5 shadow-card dark:border-accent-800 dark:bg-accent-900"
            onClick={(e) => e.stopPropagation()}
          >
            <div className="flex items-start gap-3">
              <span className="mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-lg bg-red-50 text-red-600 ring-1 ring-inset ring-red-200 dark:bg-red-950/40 dark:text-red-300 dark:ring-red-900/60">
                <LogOut className="size-4" />
              </span>
              <div>
                <h2 className="text-base font-semibold text-accent-950 dark:text-white">登出全部设备？</h2>
                <p className="mt-1 text-sm text-accent-500 dark:text-accent-400">
                  将撤销当前账户的所有活跃会话（包含本机）。完成后本机会被退回登录页，需重新登录。
                </p>
              </div>
            </div>
            <div className="mt-5 flex justify-end gap-2">
              <Button
                variant="outline"
                size="sm"
                onClick={() => setConfirmAllOpen(false)}
                disabled={signingOutAll}
              >
                取消
              </Button>
              <Button
                size="sm"
                onClick={handleSignOutAll}
                disabled={signingOutAll}
                className="bg-red-600 text-white hover:bg-red-700"
              >
                {signingOutAll ? <Loader2 className="size-4 animate-spin" /> : <LogOut className="size-4" />}
                登出全部设备
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
