'use client';

// 账户安全:两步验证 (2FA/TOTP) · Passkey (通行密钥) · 第三方账号绑定。
// 全部端点走 session 鉴权 (lib/api/security.ts -> userClient)。三区卡片各自独立 loading/空/错误/503 容错,
// 一处不可用 (后端服务在最小 dev 装配里可能 nil → 结构化 503) 不拖垮整页。
// 借鉴 (仅功能/字段/布局形态,clean-room 非抄码):
//   - sub2api src/api/totp.ts + ProfileTotpCard/TotpSetupModal: 2FA 状态卡 + setup 出 secret/QR/备份码 + enable 校验码的形态。
//   - sub2api ProfileIdentityBindingsSection.vue: 第三方身份绑定列表 + 解绑按钮的布局形态。
//   - new-api 个人设置安全区: 2FA 启停 + 备份码展示的功能形态。
import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertCircle,
  CheckCircle2,
  Copy,
  Fingerprint,
  KeyRound,
  Link2,
  Loader2,
  Lock,
  RefreshCw,
  ShieldCheck,
  ShieldOff,
  Trash2,
  Unlink,
} from 'lucide-react';
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
import { cn } from '@/lib/utils';
import { ApiError } from '@/lib/api/client';
import { friendlyMessage } from '@/lib/api/errors';
import {
  disable2FA,
  deletePasskey,
  enable2FA,
  fetch2FAStatus,
  fetchOAuthBindings,
  fetchPasskeys,
  fmtDateTime,
  providerLabel,
  regenerateBackupCodes,
  setup2FA,
  transportLabel,
  unlinkOAuthBinding,
  type OAuthBinding,
  type PasskeySummary,
  type TwoFASetupResult,
  type TwoFAStatus,
} from '@/lib/api/security';

// 区块加载失败时,区分「暂不可用 (503)」与一般错误,供卡片显示不同语气。
function describeError(err: unknown): { message: string; unavailable: boolean } {
  if (err instanceof ApiError && err.status === 503) {
    return { message: '该功能当前暂不可用,请稍后再试或联系管理员开通。', unavailable: true };
  }
  return { message: friendlyMessage(err), unavailable: false };
}

function SectionCard({
  icon: Icon,
  title,
  hint,
  action,
  children,
}: {
  icon: typeof Lock;
  title: string;
  hint?: string;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardHeader className="flex flex-row items-start justify-between gap-3 p-5 pb-3">
        <div className="flex min-w-0 items-start gap-2.5">
          <span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary-50 text-primary-600 ring-1 ring-primary-100 dark:bg-primary-950/50 dark:text-primary-300 dark:ring-primary-900/70">
            <Icon className="size-4" />
          </span>
          <div className="min-w-0">
            <CardTitle className="text-base font-semibold tracking-normal text-accent-950 dark:text-white">{title}</CardTitle>
            {hint && <p className="mt-0.5 text-xs text-accent-400 dark:text-accent-500">{hint}</p>}
          </div>
        </div>
        {action}
      </CardHeader>
      <CardContent className="p-5 pt-0">{children}</CardContent>
    </Card>
  );
}

function Loading({ label = '加载中…' }: { label?: string }) {
  return (
    <div className="flex items-center justify-center gap-2 py-8 text-sm text-accent-400">
      <Loader2 className="size-4 animate-spin" /> {label}
    </div>
  );
}

function Empty({ children }: { children: React.ReactNode }) {
  return (
    <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-8 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
      {children}
    </div>
  );
}

function ErrorLine({ children, tone = 'red' }: { children: React.ReactNode; tone?: 'red' | 'amber' }) {
  const cls =
    tone === 'amber'
      ? 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-900/60 dark:bg-amber-950/40 dark:text-amber-300'
      : 'border-red-200 bg-red-50 text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300';
  return (
    <div className={cn('flex items-start gap-2 rounded-lg border px-3 py-2.5 text-sm', cls)}>
      <AlertCircle className="mt-0.5 size-4 shrink-0" />
      <span>{children}</span>
    </div>
  );
}

function NoticeLine({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex items-start gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2.5 text-sm text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">
      <CheckCircle2 className="mt-0.5 size-4 shrink-0" />
      <span>{children}</span>
    </div>
  );
}

// 备份码块:一次性展示 + 一键复制全部。用户必须当场保存,后端不再回显。
function BackupCodes({ codes }: { codes: string[] }) {
  const [copied, setCopied] = useState(false);
  const copyAll = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(codes.join('\n'));
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  }, [codes]);
  return (
    <div className="rounded-lg border border-amber-200 bg-amber-50/70 p-3.5 dark:border-amber-900/60 dark:bg-amber-950/30">
      <div className="flex items-center justify-between">
        <span className="text-sm font-semibold text-amber-800 dark:text-amber-200">备份恢复码</span>
        <Button size="sm" variant="outline" onClick={copyAll}>
          {copied ? <CheckCircle2 className="size-4 text-emerald-500" /> : <Copy className="size-4" />}
          {copied ? '已复制' : '复制全部'}
        </Button>
      </div>
      <p className="mt-1 text-xs text-amber-700/80 dark:text-amber-300/70">
        请立即妥善保存,每个码仅可使用一次,丢失验证器时用于登录。此后不会再次显示。
      </p>
      <div className="mt-2.5 grid grid-cols-2 gap-1.5 sm:grid-cols-3">
        {codes.map((c) => (
          <code
            key={c}
            className="rounded-md bg-white px-2 py-1 text-center font-mono text-xs tabular-nums text-accent-800 ring-1 ring-inset ring-amber-200 dark:bg-accent-900 dark:text-accent-100 dark:ring-amber-900/60"
          >
            {c}
          </code>
        ))}
      </div>
    </div>
  );
}

function CopyField({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  const copy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      setCopied(false);
    }
  }, [value]);
  return (
    <div>
      <div className="mb-1 text-xs text-accent-400 dark:text-accent-500">{label}</div>
      <div className="flex items-center gap-2 rounded-lg border border-accent-200 bg-accent-50 px-3 py-2 dark:border-accent-800 dark:bg-accent-950/40">
        <code className="min-w-0 flex-1 break-all font-mono text-xs text-accent-800 dark:text-accent-100">{value}</code>
        <Button size="sm" variant="outline" onClick={copy} className="shrink-0">
          {copied ? <CheckCircle2 className="size-4 text-emerald-500" /> : <Copy className="size-4" />}
        </Button>
      </div>
    </div>
  );
}

// ==================== 2FA 区 ====================
function TwoFASection() {
  const [status, setStatus] = useState<TwoFAStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<{ message: string; unavailable: boolean } | null>(null);

  // setup 流程:setup -> 展示 secret/URI/备份码 -> 输入验证器码 enable。
  const [setupData, setSetupData] = useState<TwoFASetupResult | null>(null);
  const [enableCode, setEnableCode] = useState('');
  const [working, setWorking] = useState(false);
  const [actionErr, setActionErr] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  // disable 流程:输入有效 TOTP/备份码。
  const [disabling, setDisabling] = useState(false);
  const [disableCode, setDisableCode] = useState('');

  // 重新生成备份码:输入有效码 -> 展示新码。
  const [regen, setRegen] = useState(false);
  const [regenCode, setRegenCode] = useState('');
  const [freshCodes, setFreshCodes] = useState<string[] | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setErr(null);
    try {
      setStatus(await fetch2FAStatus());
    } catch (e) {
      setErr(describeError(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const resetFlows = useCallback(() => {
    setSetupData(null);
    setEnableCode('');
    setDisabling(false);
    setDisableCode('');
    setRegen(false);
    setRegenCode('');
    setActionErr(null);
  }, []);

  const beginSetup = useCallback(async () => {
    setWorking(true);
    setActionErr(null);
    setNotice(null);
    setFreshCodes(null);
    try {
      setSetupData(await setup2FA());
    } catch (e) {
      setActionErr(friendlyMessage(e));
    } finally {
      setWorking(false);
    }
  }, []);

  const confirmEnable = useCallback(async () => {
    setWorking(true);
    setActionErr(null);
    try {
      const s = await enable2FA(enableCode.trim());
      setStatus(s);
      resetFlows();
      setNotice('两步验证已开启。其他设备的登录会话已被注销,请重新登录。');
    } catch (e) {
      setActionErr(friendlyMessage(e));
    } finally {
      setWorking(false);
    }
  }, [enableCode, resetFlows]);

  const confirmDisable = useCallback(async () => {
    setWorking(true);
    setActionErr(null);
    try {
      await disable2FA(disableCode.trim());
      resetFlows();
      setNotice('两步验证已关闭。');
      await load();
    } catch (e) {
      setActionErr(friendlyMessage(e));
    } finally {
      setWorking(false);
    }
  }, [disableCode, resetFlows, load]);

  const confirmRegen = useCallback(async () => {
    setWorking(true);
    setActionErr(null);
    try {
      const r = await regenerateBackupCodes(regenCode.trim());
      setFreshCodes(r.backup_codes);
      setRegen(false);
      setRegenCode('');
      setNotice('已生成新的备份码,旧备份码全部作废。');
      await load();
    } catch (e) {
      setActionErr(friendlyMessage(e));
    } finally {
      setWorking(false);
    }
  }, [regenCode, load]);

  const enabled = status?.enabled ?? false;
  const available = status?.available ?? false;

  return (
    <SectionCard
      icon={ShieldCheck}
      title="两步验证 (2FA)"
      hint="使用验证器 App (如 Google Authenticator) 在登录时输入动态码,显著提升账户安全。"
      action={
        status ? (
          <span
            className={cn(
              'inline-flex shrink-0 items-center gap-1 rounded-md px-2 py-0.5 text-[11px] font-medium ring-1 ring-inset',
              enabled
                ? 'bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-950/40 dark:text-emerald-300 dark:ring-emerald-900/60'
                : 'bg-accent-100 text-accent-600 ring-accent-200 dark:bg-accent-800 dark:text-accent-300 dark:ring-accent-700',
            )}
          >
            {enabled ? <ShieldCheck className="size-3" /> : <ShieldOff className="size-3" />}
            {enabled ? '已开启' : '未开启'}
          </span>
        ) : null
      }
    >
      {loading ? (
        <Loading />
      ) : err ? (
        <ErrorLine tone={err.unavailable ? 'amber' : 'red'}>{err.message}</ErrorLine>
      ) : (
        <div className="flex flex-col gap-4">
          {notice && <NoticeLine>{notice}</NoticeLine>}
          {actionErr && <ErrorLine>{actionErr}</ErrorLine>}

          {!available && !enabled && (
            <ErrorLine tone="amber">平台当前未开放两步验证功能。</ErrorLine>
          )}

          {/* 状态概览 */}
          <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
            <div className="rounded-lg bg-accent-50 p-3 text-center dark:bg-accent-950/40">
              <div className="text-sm font-bold text-accent-950 dark:text-white">{enabled ? '已启用' : '未启用'}</div>
              <div className="mt-0.5 text-[11px] text-accent-400 dark:text-accent-500">当前状态</div>
            </div>
            <div className="rounded-lg bg-accent-50 p-3 text-center dark:bg-accent-950/40">
              <div className="text-lg font-bold tabular-nums text-accent-950 dark:text-white">{status?.backup_codes_remaining ?? 0}</div>
              <div className="mt-0.5 text-[11px] text-accent-400 dark:text-accent-500">剩余备份码</div>
            </div>
            <div className="col-span-2 rounded-lg bg-accent-50 p-3 text-center dark:bg-accent-950/40 sm:col-span-1">
              <div className="truncate text-sm font-medium text-accent-700 dark:text-accent-200">{fmtDateTime(status?.last_used_at)}</div>
              <div className="mt-0.5 text-[11px] text-accent-400 dark:text-accent-500">上次使用</div>
            </div>
          </div>

          {freshCodes && <BackupCodes codes={freshCodes} />}

          {/* 未启用:开启流程 */}
          {!enabled && available && (
            <div className="flex flex-col gap-3">
              {!setupData ? (
                <Button onClick={beginSetup} disabled={working} size="sm" className="self-start">
                  {working ? <Loader2 className="size-4 animate-spin" /> : <Lock className="size-4" />}
                  开启两步验证
                </Button>
              ) : (
                <div className="flex flex-col gap-3 rounded-lg border border-accent-200 bg-accent-50/60 p-4 dark:border-accent-800 dark:bg-accent-950/40">
                  <p className="text-sm text-accent-700 dark:text-accent-200">
                    在验证器 App 中手动添加密钥 (或用 App 扫码功能录入下方 otpauth 链接),然后输入 App 显示的 6 位动态码完成绑定。
                  </p>
                  <CopyField label="密钥 (手动录入)" value={setupData.secret} />
                  <CopyField label="otpauth 链接 (扫码录入)" value={setupData.qr_data} />
                  <BackupCodes codes={setupData.backup_codes} />
                  <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
                    <input
                      value={enableCode}
                      onChange={(e) => setEnableCode(e.target.value.replace(/\s/g, ''))}
                      placeholder="输入 6 位动态码"
                      inputMode="numeric"
                      autoComplete="one-time-code"
                      className="w-full rounded-lg border border-accent-200 bg-white px-3 py-2 font-mono text-sm tabular-nums text-accent-900 outline-none focus:border-primary-400 focus:ring-2 focus:ring-primary-100 dark:border-accent-700 dark:bg-accent-900 dark:text-accent-100 dark:focus:ring-primary-900/40 sm:max-w-44"
                    />
                    <div className="flex gap-2">
                      <Button onClick={confirmEnable} disabled={working || enableCode.trim().length < 6} size="sm">
                        {working ? <Loader2 className="size-4 animate-spin" /> : <CheckCircle2 className="size-4" />}
                        确认开启
                      </Button>
                      <Button variant="outline" size="sm" onClick={resetFlows} disabled={working}>
                        取消
                      </Button>
                    </div>
                  </div>
                </div>
              )}
            </div>
          )}

          {/* 已启用:重新生成备份码 + 关闭 */}
          {enabled && (
            <div className="flex flex-col gap-3">
              {/* 重新生成备份码 */}
              {!regen ? (
                <Button variant="outline" size="sm" className="self-start" onClick={() => { setRegen(true); setActionErr(null); }}>
                  <RefreshCw className="size-4" />
                  重新生成备份码
                </Button>
              ) : (
                <div className="flex flex-col gap-2 rounded-lg border border-accent-200 bg-accent-50/60 p-3.5 dark:border-accent-800 dark:bg-accent-950/40 sm:flex-row sm:items-center">
                  <input
                    value={regenCode}
                    onChange={(e) => setRegenCode(e.target.value.replace(/\s/g, ''))}
                    placeholder="输入动态码或备份码"
                    autoComplete="one-time-code"
                    className="w-full rounded-lg border border-accent-200 bg-white px-3 py-2 font-mono text-sm text-accent-900 outline-none focus:border-primary-400 focus:ring-2 focus:ring-primary-100 dark:border-accent-700 dark:bg-accent-900 dark:text-accent-100 dark:focus:ring-primary-900/40 sm:max-w-52"
                  />
                  <div className="flex gap-2">
                    <Button onClick={confirmRegen} disabled={working || !regenCode.trim()} size="sm">
                      {working ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
                      生成
                    </Button>
                    <Button variant="outline" size="sm" onClick={() => { setRegen(false); setRegenCode(''); }} disabled={working}>
                      取消
                    </Button>
                  </div>
                </div>
              )}

              {/* 关闭 2FA */}
              {!disabling ? (
                <Button variant="outline" size="sm" className="self-start border-red-200 text-red-600 hover:bg-red-50 dark:border-red-900/60 dark:text-red-300 dark:hover:bg-red-950/40" onClick={() => { setDisabling(true); setActionErr(null); }}>
                  <ShieldOff className="size-4" />
                  关闭两步验证
                </Button>
              ) : (
                <div className="flex flex-col gap-2 rounded-lg border border-red-200 bg-red-50/60 p-3.5 dark:border-red-900/60 dark:bg-red-950/30 sm:flex-row sm:items-center">
                  <input
                    value={disableCode}
                    onChange={(e) => setDisableCode(e.target.value.replace(/\s/g, ''))}
                    placeholder="输入动态码或备份码确认"
                    autoComplete="one-time-code"
                    className="w-full rounded-lg border border-accent-200 bg-white px-3 py-2 font-mono text-sm text-accent-900 outline-none focus:border-red-400 focus:ring-2 focus:ring-red-100 dark:border-accent-700 dark:bg-accent-900 dark:text-accent-100 dark:focus:ring-red-900/40 sm:max-w-52"
                  />
                  <div className="flex gap-2">
                    <Button onClick={confirmDisable} disabled={working || !disableCode.trim()} size="sm" className="bg-red-600 hover:bg-red-700">
                      {working ? <Loader2 className="size-4 animate-spin" /> : <ShieldOff className="size-4" />}
                      确认关闭
                    </Button>
                    <Button variant="outline" size="sm" onClick={() => { setDisabling(false); setDisableCode(''); }} disabled={working}>
                      取消
                    </Button>
                  </div>
                </div>
              )}
            </div>
          )}
        </div>
      )}
    </SectionCard>
  );
}

// ==================== Passkey 区 ====================
function PasskeySection() {
  const [items, setItems] = useState<PasskeySummary[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<{ message: string; unavailable: boolean } | null>(null);
  const [deletingId, setDeletingId] = useState<number | null>(null);
  const [actionErr, setActionErr] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setErr(null);
    try {
      const r = await fetchPasskeys();
      setItems(r.passkeys ?? []);
    } catch (e) {
      setErr(describeError(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const remove = useCallback(async (id: number) => {
    setDeletingId(id);
    setActionErr(null);
    setNotice(null);
    try {
      await deletePasskey(id);
      setNotice('通行密钥已删除。');
      await load();
    } catch (e) {
      // 删除走 step-up 校验:dev 装配 step-up 依赖未配置 → 403/503;给出友好提示而非红屏。
      setActionErr(friendlyMessage(e));
    } finally {
      setDeletingId(null);
    }
  }, [load]);

  return (
    <SectionCard
      icon={Fingerprint}
      title="通行密钥 (Passkey)"
      hint="使用设备生物识别 / 安全密钥免密登录。注册新通行密钥需浏览器 WebAuthn 交互。"
      action={items ? <span className="shrink-0 text-[11px] text-accent-400 dark:text-accent-500">{items.length} 个</span> : null}
    >
      {loading ? (
        <Loading />
      ) : err ? (
        <ErrorLine tone={err.unavailable ? 'amber' : 'red'}>{err.message}</ErrorLine>
      ) : (
        <div className="flex flex-col gap-3">
          {notice && <NoticeLine>{notice}</NoticeLine>}
          {actionErr && <ErrorLine>{actionErr}</ErrorLine>}

          {/* 注册新通行密钥:WebAuthn 全流程 (navigator.credentials.create + register/begin+finish)
              较复杂,留待后续切片实现;此区先提供列表与删除。 */}
          <div className="flex items-start gap-2 rounded-lg border border-dashed border-accent-200 bg-accent-50/60 px-3 py-2.5 text-xs text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
            <KeyRound className="mt-0.5 size-3.5 shrink-0" />
            <span>注册新通行密钥 (浏览器 WebAuthn 交互) 即将上线。当前可查看与删除已绑定的通行密钥。</span>
          </div>

          {!items || items.length === 0 ? (
            <Empty>尚未绑定任何通行密钥。</Empty>
          ) : (
            <div className="overflow-x-auto rounded-lg border border-accent-200 dark:border-accent-800">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>名称</TableHead>
                    <TableHead>传输方式</TableHead>
                    <TableHead>创建时间</TableHead>
                    <TableHead>上次使用</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {items.map((p) => (
                    <TableRow key={p.id}>
                      <TableCell className="font-medium text-accent-800 dark:text-accent-100">
                        <div className="flex items-center gap-2">
                          <span className="truncate">{p.name || `通行密钥 #${p.id}`}</span>
                          {p.clone_warning && (
                            <span className="inline-flex items-center rounded-md bg-red-50 px-1.5 py-0.5 text-[10px] font-medium text-red-600 ring-1 ring-inset ring-red-200 dark:bg-red-950/40 dark:text-red-300 dark:ring-red-900/60">
                              克隆告警
                            </span>
                          )}
                        </div>
                      </TableCell>
                      <TableCell className="text-xs text-accent-500 dark:text-accent-400">
                        {p.transports && p.transports.length > 0 ? p.transports.map(transportLabel).join(' / ') : '—'}
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">{fmtDateTime(p.created_at)}</TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">{fmtDateTime(p.last_used_at)}</TableCell>
                      <TableCell className="text-right">
                        <Button
                          variant="outline"
                          size="sm"
                          className="border-red-200 text-red-600 hover:bg-red-50 dark:border-red-900/60 dark:text-red-300 dark:hover:bg-red-950/40"
                          onClick={() => remove(p.id)}
                          disabled={deletingId === p.id}
                        >
                          {deletingId === p.id ? <Loader2 className="size-4 animate-spin" /> : <Trash2 className="size-4" />}
                          删除
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </div>
      )}
    </SectionCard>
  );
}

// ==================== 第三方绑定区 ====================
function OAuthBindingsSection() {
  const [items, setItems] = useState<OAuthBinding[] | null>(null);
  const [loading, setLoading] = useState(true);
  const [err, setErr] = useState<{ message: string; unavailable: boolean } | null>(null);
  const [unlinking, setUnlinking] = useState<string | null>(null);
  const [actionErr, setActionErr] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setErr(null);
    try {
      const r = await fetchOAuthBindings();
      setItems(r.bindings ?? []);
    } catch (e) {
      setErr(describeError(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const unlink = useCallback(async (provider: string) => {
    setUnlinking(provider);
    setActionErr(null);
    setNotice(null);
    try {
      const r = await unlinkOAuthBinding(provider);
      // service 层:not-linked → unlinked:false (200 no-op);已解绑 → true。
      setNotice(r.unlinked ? `已解绑 ${providerLabel(provider)}。` : `${providerLabel(provider)} 当前未绑定。`);
      await load();
    } catch (e) {
      // 末位登录方式保护:无密码且唯一绑定 → 409。friendlyMessage 给「操作冲突」提示。
      if (e instanceof ApiError && e.status === 409) {
        setActionErr('这是你唯一的登录方式,无法解绑。请先设置密码或绑定其他登录方式。');
      } else {
        setActionErr(friendlyMessage(e));
      }
    } finally {
      setUnlinking(null);
    }
  }, [load]);

  return (
    <SectionCard
      icon={Link2}
      title="第三方账号绑定"
      hint="已绑定的社交登录方式 (OAuth)。解绑后将无法用该方式登录。"
      action={items ? <span className="shrink-0 text-[11px] text-accent-400 dark:text-accent-500">{items.length} 个</span> : null}
    >
      {loading ? (
        <Loading />
      ) : err ? (
        <ErrorLine tone={err.unavailable ? 'amber' : 'red'}>{err.message}</ErrorLine>
      ) : (
        <div className="flex flex-col gap-3">
          {notice && <NoticeLine>{notice}</NoticeLine>}
          {actionErr && <ErrorLine>{actionErr}</ErrorLine>}

          {!items || items.length === 0 ? (
            <Empty>尚未绑定任何第三方账号。</Empty>
          ) : (
            <div className="flex flex-col gap-2.5">
              {items.map((b) => (
                <div
                  key={b.provider}
                  className="flex items-center justify-between gap-3 rounded-lg border border-accent-200 bg-accent-50/60 px-3.5 py-3 dark:border-accent-800 dark:bg-accent-950/40"
                >
                  <div className="flex min-w-0 items-center gap-2.5">
                    <span className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-primary-50 text-primary-600 ring-1 ring-primary-100 dark:bg-primary-950/50 dark:text-primary-300 dark:ring-primary-900/70">
                      <KeyRound className="size-4" />
                    </span>
                    <div className="min-w-0">
                      <div className="truncate text-sm font-medium text-accent-800 dark:text-accent-100">{providerLabel(b.provider)}</div>
                      <div className="mt-0.5 truncate font-mono text-[11px] text-accent-400 dark:text-accent-500">
                        {b.subject || '—'} · 绑定于 {fmtDateTime(b.linked_at)}
                      </div>
                    </div>
                  </div>
                  <Button
                    variant="outline"
                    size="sm"
                    className="shrink-0 border-red-200 text-red-600 hover:bg-red-50 dark:border-red-900/60 dark:text-red-300 dark:hover:bg-red-950/40"
                    onClick={() => unlink(b.provider)}
                    disabled={unlinking === b.provider}
                  >
                    {unlinking === b.provider ? <Loader2 className="size-4 animate-spin" /> : <Unlink className="size-4" />}
                    解绑
                  </Button>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </SectionCard>
  );
}

export default function SecurityPage() {
  // 三区彼此独立;此处仅整页标题 + 一个总刷新 (通过 key 重挂载触发各区自身 load)。
  const [refreshKey, setRefreshKey] = useState(0);
  const refresh = useCallback(() => setRefreshKey((k) => k + 1), []);
  const sections = useMemo(
    () => (
      <div key={refreshKey} className="flex flex-col gap-5">
        <TwoFASection />
        <PasskeySection />
        <OAuthBindingsSection />
      </div>
    ),
    [refreshKey],
  );

  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-5">
      <div className="flex flex-col gap-1 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="text-xl font-bold text-accent-950 dark:text-white">账户安全</h1>
          <p className="mt-1 text-sm text-accent-500 dark:text-accent-400">
            管理两步验证、通行密钥与第三方账号绑定。各区互不影响,某项暂不可用不影响其他设置。
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={refresh}>
          <RefreshCw className="size-4" />
          刷新
        </Button>
      </div>
      {sections}
    </div>
  );
}
