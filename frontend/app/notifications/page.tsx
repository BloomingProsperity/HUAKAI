'use client';

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertCircle,
  AlertTriangle,
  Bell,
  BellRing,
  CheckCheck,
  CheckCircle2,
  Inbox,
  Info,
  Loader2,
  Megaphone,
  RefreshCw,
  Settings2,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent } from '@/components/ui/card';
import { friendlyMessage } from '@/lib/api/errors';
import {
  fetchNotifySettings,
  fetchUnreadCount,
  listAnnouncements,
  listNotifications,
  markNotificationRead,
  notifyTypeLabel,
  saveNotifySettings,
  severityLabel,
  type Announcement,
  type Notification,
  type NotifySettings,
  type NotifySettingsRequest,
  type NotifyType,
  type Severity,
} from '@/lib/api/notifications';
import { cn } from '@/lib/utils';

const PAGE_SIZE = 50;

type Tab = 'inbox' | 'announcements';

// ---- 格式化 ----

function fmtTime(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? value : d.toLocaleString('zh-CN', { hour12: false });
}

// severity → 徽章配色（teal/accent 暗色体系）。
function severityRing(sev: Severity): string {
  switch (sev) {
    case 'critical':
      return 'bg-red-50 text-red-700 ring-red-200 dark:bg-red-950/40 dark:text-red-300 dark:ring-red-900/60';
    case 'warning':
      return 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-950/40 dark:text-amber-300 dark:ring-amber-900/60';
    default:
      return 'bg-primary-50 text-primary-700 ring-primary-200 dark:bg-primary-950/40 dark:text-primary-300 dark:ring-primary-900/60';
  }
}

function SeverityIcon({ sev, className }: { sev: Severity; className?: string }) {
  if (sev === 'critical') return <AlertTriangle className={className} />;
  if (sev === 'warning') return <AlertCircle className={className} />;
  return <Info className={className} />;
}

function SeverityBadge({ sev }: { sev: Severity }) {
  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-xs font-medium ring-1 ring-inset',
        severityRing(sev),
      )}
    >
      <SeverityIcon sev={sev} className="size-3" />
      {severityLabel(sev)}
    </span>
  );
}

// ---- 提示条 ----

function Banner({ tone, children }: { tone: 'error' | 'success'; children: React.ReactNode }) {
  const cls =
    tone === 'error'
      ? 'border-red-200 bg-red-50 text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300'
      : 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300';
  const Icon = tone === 'error' ? AlertCircle : CheckCircle2;
  return (
    <div className={cn('flex items-start gap-2 rounded-lg border px-4 py-3 text-sm', cls)}>
      <Icon className="mt-0.5 size-4 shrink-0" />
      <span>{children}</span>
    </div>
  );
}

function EmptyState({ icon: Icon, children }: { icon: typeof Inbox; children: React.ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-2 rounded-lg border border-dashed border-accent-200 bg-accent-50 py-12 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
      <Icon className="size-6 text-accent-300 dark:text-accent-600" />
      {children}
    </div>
  );
}

function LoadingBlock() {
  return (
    <div className="flex items-center justify-center gap-2 py-12 text-sm text-accent-400">
      <Loader2 className="size-4 animate-spin" /> 加载中…
    </div>
  );
}

// ===== 主页面 =====

export default function NotificationsPage() {
  const [tab, setTab] = useState<Tab>('inbox');

  // 收件箱状态
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [unreadCount, setUnreadCount] = useState(0);
  const [unreadOnly, setUnreadOnly] = useState(false);
  const [inboxLoading, setInboxLoading] = useState(true);
  const [inboxError, setInboxError] = useState('');
  const [markingId, setMarkingId] = useState<number | null>(null);
  const [markingAll, setMarkingAll] = useState(false);

  // 公告状态
  const [announcements, setAnnouncements] = useState<Announcement[]>([]);
  const [annLoading, setAnnLoading] = useState(true);
  const [annError, setAnnError] = useState('');

  const [notice, setNotice] = useState('');

  const loadInbox = useCallback(async (only: boolean) => {
    setInboxLoading(true);
    setInboxError('');
    try {
      const [list, count] = await Promise.all([
        listNotifications({ limit: PAGE_SIZE, unreadOnly: only }),
        fetchUnreadCount(),
      ]);
      setNotifications(list.items ?? []);
      setUnreadCount(count.count ?? 0);
    } catch (err) {
      setInboxError(friendlyMessage(err));
    } finally {
      setInboxLoading(false);
    }
  }, []);

  const loadAnnouncements = useCallback(async () => {
    setAnnLoading(true);
    setAnnError('');
    try {
      const list = await listAnnouncements({ limit: PAGE_SIZE });
      setAnnouncements(list.items ?? []);
    } catch (err) {
      setAnnError(friendlyMessage(err));
    } finally {
      setAnnLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadInbox(unreadOnly);
  }, [loadInbox, unreadOnly]);

  useEffect(() => {
    void loadAnnouncements();
  }, [loadAnnouncements]);

  const markOne = useCallback(
    async (n: Notification) => {
      if (n.read_at) return;
      setMarkingId(n.id);
      setNotice('');
      try {
        const updated = await markNotificationRead(n.id);
        setNotifications((prev) =>
          unreadOnly
            ? prev.filter((x) => x.id !== n.id)
            : prev.map((x) => (x.id === n.id ? updated : x)),
        );
        setUnreadCount((c) => Math.max(0, c - 1));
      } catch (err) {
        setInboxError(friendlyMessage(err));
      } finally {
        setMarkingId(null);
      }
    },
    [unreadOnly],
  );

  // 「全部已读」：后端无批量端点，对当前列表中的未读逐条调用（借鉴 sub2api 的全读概念，HUAKAI 自研串行实现）。
  const markAll = useCallback(async () => {
    const unread = notifications.filter((n) => !n.read_at);
    if (unread.length === 0) return;
    setMarkingAll(true);
    setInboxError('');
    setNotice('');
    let done = 0;
    try {
      for (const n of unread) {
        await markNotificationRead(n.id);
        done += 1;
      }
      setNotice(`已将 ${done} 条通知标记为已读。`);
    } catch (err) {
      setInboxError(friendlyMessage(err));
    } finally {
      setMarkingAll(false);
      await loadInbox(unreadOnly);
    }
  }, [notifications, unreadOnly, loadInbox]);

  const hasUnreadInView = useMemo(() => notifications.some((n) => !n.read_at), [notifications]);

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-bold text-accent-950 dark:text-white">通知 &amp; 公告</h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          站内通知收件箱与平台公告。通知可标记已读；公告为平台广播，按发布时间展示生效中的内容。
        </p>
      </div>

      {/* Tabs */}
      <div className="flex items-center gap-1 border-b border-accent-200 dark:border-accent-800">
        <TabButton active={tab === 'inbox'} onClick={() => setTab('inbox')} icon={Bell}>
          通知收件箱
          {unreadCount > 0 && (
            <span className="ml-1.5 inline-flex min-w-5 items-center justify-center rounded-full bg-primary-600 px-1.5 text-[11px] font-semibold text-white dark:bg-primary-500">
              {unreadCount > 99 ? '99+' : unreadCount}
            </span>
          )}
        </TabButton>
        <TabButton active={tab === 'announcements'} onClick={() => setTab('announcements')} icon={Megaphone}>
          公告
        </TabButton>
      </div>

      {notice && <Banner tone="success">{notice}</Banner>}

      {tab === 'inbox' ? (
        <InboxTab
          notifications={notifications}
          loading={inboxLoading}
          error={inboxError}
          unreadOnly={unreadOnly}
          markingId={markingId}
          markingAll={markingAll}
          hasUnreadInView={hasUnreadInView}
          onToggleUnreadOnly={() => setUnreadOnly((v) => !v)}
          onRefresh={() => void loadInbox(unreadOnly)}
          onMarkOne={markOne}
          onMarkAll={markAll}
        />
      ) : (
        <AnnouncementsTab
          announcements={announcements}
          loading={annLoading}
          error={annError}
          onRefresh={() => void loadAnnouncements()}
        />
      )}

      {/* 通知设置（始终展示在 inbox 语境下方；公告 tab 时收起，避免干扰） */}
      {tab === 'inbox' && <NotifySettingsCard onError={setInboxError} />}
    </div>
  );
}

// ---- Tab 按钮 ----

function TabButton({
  active,
  onClick,
  icon: Icon,
  children,
}: {
  active: boolean;
  onClick: () => void;
  icon: typeof Bell;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        '-mb-px flex items-center gap-1.5 border-b-2 px-4 py-2.5 text-sm font-medium transition-colors',
        active
          ? 'border-primary-600 text-primary-700 dark:border-primary-400 dark:text-primary-300'
          : 'border-transparent text-accent-500 hover:text-accent-800 dark:text-accent-400 dark:hover:text-accent-200',
      )}
    >
      <Icon className="size-4" />
      {children}
    </button>
  );
}

// ===== 收件箱 Tab =====

function InboxTab({
  notifications,
  loading,
  error,
  unreadOnly,
  markingId,
  markingAll,
  hasUnreadInView,
  onToggleUnreadOnly,
  onRefresh,
  onMarkOne,
  onMarkAll,
}: {
  notifications: Notification[];
  loading: boolean;
  error: string;
  unreadOnly: boolean;
  markingId: number | null;
  markingAll: boolean;
  hasUnreadInView: boolean;
  onToggleUnreadOnly: () => void;
  onRefresh: () => void;
  onMarkOne: (n: Notification) => void;
  onMarkAll: () => void;
}) {
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          variant={unreadOnly ? 'default' : 'outline'}
          onClick={onToggleUnreadOnly}
          disabled={loading}
        >
          <BellRing />
          {unreadOnly ? '只看未读 · 开' : '只看未读 · 关'}
        </Button>
        <Button size="sm" variant="outline" onClick={onMarkAll} disabled={markingAll || !hasUnreadInView}>
          {markingAll ? <Loader2 className="animate-spin" /> : <CheckCheck />}
          全部已读
        </Button>
        <div className="flex-1" />
        <Button size="sm" variant="outline" onClick={onRefresh} disabled={loading}>
          <RefreshCw className={loading ? 'animate-spin' : ''} />
          刷新
        </Button>
      </div>

      {error && <Banner tone="error">{error}</Banner>}

      {loading ? (
        <LoadingBlock />
      ) : notifications.length === 0 ? (
        <EmptyState icon={Inbox}>{unreadOnly ? '没有未读通知。' : '暂无通知。'}</EmptyState>
      ) : (
        <div className="flex flex-col gap-2">
          {notifications.map((n) => {
            const read = Boolean(n.read_at);
            return (
              <Card
                key={n.id}
                className={cn(
                  'border-accent-200 bg-white shadow-card transition-colors dark:border-accent-800 dark:bg-accent-900/70',
                  !read && 'border-l-4 border-l-primary-500 dark:border-l-primary-400',
                )}
              >
                <CardContent className="flex flex-col gap-2 p-4">
                  <div className="flex items-start justify-between gap-3">
                    <div className="flex items-center gap-2">
                      {!read && <span className="size-2 shrink-0 rounded-full bg-primary-500" aria-label="未读" />}
                      <SeverityBadge sev={n.severity} />
                      <span
                        className={cn(
                          'text-sm font-semibold',
                          read
                            ? 'text-accent-700 dark:text-accent-300'
                            : 'text-accent-950 dark:text-white',
                        )}
                      >
                        {n.title || '(无标题)'}
                      </span>
                    </div>
                    {!read && (
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => onMarkOne(n)}
                        disabled={markingId === n.id}
                        className="shrink-0"
                      >
                        {markingId === n.id ? <Loader2 className="animate-spin" /> : <CheckCheck />}
                        标记已读
                      </Button>
                    )}
                  </div>
                  {n.body && (
                    <p className="whitespace-pre-wrap text-sm text-accent-600 dark:text-accent-300">{n.body}</p>
                  )}
                  <div className="flex items-center gap-3 text-[11px] text-accent-400 dark:text-accent-500">
                    <span>{fmtTime(n.created_at)}</span>
                    {read && n.read_at && <span>已读于 {fmtTime(n.read_at)}</span>}
                  </div>
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}
    </div>
  );
}

// ===== 公告 Tab =====

function AnnouncementsTab({
  announcements,
  loading,
  error,
  onRefresh,
}: {
  announcements: Announcement[];
  loading: boolean;
  error: string;
  onRefresh: () => void;
}) {
  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-end">
        <Button size="sm" variant="outline" onClick={onRefresh} disabled={loading}>
          <RefreshCw className={loading ? 'animate-spin' : ''} />
          刷新
        </Button>
      </div>

      {error && <Banner tone="error">{error}</Banner>}

      {loading ? (
        <LoadingBlock />
      ) : announcements.length === 0 ? (
        <EmptyState icon={Megaphone}>当前没有生效中的公告。</EmptyState>
      ) : (
        <div className="flex flex-col gap-2">
          {announcements.map((a) => (
            <Card
              key={a.id}
              className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70"
            >
              <CardContent className="flex flex-col gap-2 p-4">
                <div className="flex items-start justify-between gap-3">
                  <div className="flex items-center gap-2">
                    <SeverityBadge sev={a.severity} />
                    <span className="text-sm font-semibold text-accent-950 dark:text-white">
                      {a.title || '(无标题)'}
                    </span>
                  </div>
                  {a.expires_at && (
                    <span className="shrink-0 text-[11px] text-accent-400 dark:text-accent-500">
                      截止 {fmtTime(a.expires_at)}
                    </span>
                  )}
                </div>
                {a.body && (
                  <p className="whitespace-pre-wrap text-sm text-accent-600 dark:text-accent-300">{a.body}</p>
                )}
                <div className="text-[11px] text-accent-400 dark:text-accent-500">
                  发布于 {fmtTime(a.published_at)}
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}

// ===== 通知设置卡片 =====

const NOTIFY_TYPES: { value: NotifyType; label: string }[] = [
  { value: 'none', label: '关闭' },
  { value: 'email', label: '邮件' },
  { value: 'webhook', label: 'Webhook' },
  { value: 'bark', label: 'Bark' },
  { value: 'gotify', label: 'Gotify' },
];

interface SettingsForm {
  notify_type: NotifyType;
  webhook_url: string;
  webhook_secret: string;
  notification_email: string;
  bark_url: string;
  gotify_url: string;
  gotify_token: string;
  gotify_priority: string;
  balance_threshold: string;
}

function settingsToForm(s: NotifySettings): SettingsForm {
  const t = NOTIFY_TYPES.some((x) => x.value === s.notify_type)
    ? (s.notify_type as NotifyType)
    : 'none';
  return {
    notify_type: t,
    webhook_url: s.webhook_url ?? '',
    webhook_secret: '',
    notification_email: s.notification_email ?? '',
    bark_url: s.bark_url ?? '',
    gotify_url: s.gotify_url ?? '',
    gotify_token: '',
    gotify_priority: s.gotify_priority ? String(s.gotify_priority) : '5',
    balance_threshold: s.balance_threshold ?? '',
  };
}

// 前端兜住后端 ValidateSettings 的硬约束，避免 400。返回错误文案或 null。
function validateForm(f: SettingsForm, settings: NotifySettings | null): string | null {
  switch (f.notify_type) {
    case 'email':
      if (!/.+@.+\..+/.test(f.notification_email.trim())) return '请填写合法的通知邮箱。';
      return null;
    case 'webhook': {
      // secret 为只写：已配置则可留空沿用旧值；未配置且留空则必须填。
      const hasSecret = f.webhook_secret.trim() !== '' || settings?.webhook_secret_configured;
      if (!hasSecret) return 'Webhook 需要填写签名密钥（webhook_secret）。';
      if (!/^https?:\/\/.+/.test(f.webhook_url.trim())) return '请填写合法的 Webhook URL（http/https）。';
      return null;
    }
    case 'bark':
      if (!/^https?:\/\/.+/.test(f.bark_url.trim())) return '请填写合法的 Bark URL（http/https）。';
      return null;
    case 'gotify': {
      const hasToken = f.gotify_token.trim() !== '' || settings?.gotify_token_configured;
      if (!hasToken) return 'Gotify 需要填写 Token。';
      const p = Number(f.gotify_priority);
      if (!Number.isInteger(p) || p < 1 || p > 10) return 'Gotify 优先级需为 1–10 的整数。';
      if (!/^https?:\/\/.+/.test(f.gotify_url.trim())) return '请填写合法的 Gotify URL（http/https）。';
      return null;
    }
    case 'none':
    default:
      return null;
  }
}

// 把表单收敛成后端请求体：只发当前渠道相关字段 + secret/token 留空则不发（沿用旧值）。
function formToRequest(f: SettingsForm): NotifySettingsRequest {
  const body: NotifySettingsRequest = { notify_type: f.notify_type };
  if (f.balance_threshold.trim() !== '') body.balance_threshold = f.balance_threshold.trim();
  switch (f.notify_type) {
    case 'email':
      body.notification_email = f.notification_email.trim();
      break;
    case 'webhook':
      body.webhook_url = f.webhook_url.trim();
      if (f.webhook_secret.trim() !== '') body.webhook_secret = f.webhook_secret.trim();
      break;
    case 'bark':
      body.bark_url = f.bark_url.trim();
      break;
    case 'gotify':
      body.gotify_url = f.gotify_url.trim();
      if (f.gotify_token.trim() !== '') body.gotify_token = f.gotify_token.trim();
      body.gotify_priority = Number(f.gotify_priority);
      break;
    case 'none':
    default:
      break;
  }
  return body;
}

function NotifySettingsCard({ onError }: { onError: (msg: string) => void }) {
  const [open, setOpen] = useState(false);
  const [settings, setSettings] = useState<NotifySettings | null>(null);
  const [form, setForm] = useState<SettingsForm | null>(null);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [localError, setLocalError] = useState('');
  const [saved, setSaved] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    setLocalError('');
    try {
      const s = await fetchNotifySettings();
      setSettings(s);
      setForm(settingsToForm(s));
    } catch (err) {
      setLocalError(friendlyMessage(err));
    } finally {
      setLoading(false);
    }
  }, []);

  // 展开时懒加载。
  useEffect(() => {
    if (open && !settings && !loading) void load();
  }, [open, settings, loading, load]);

  const update = useCallback(<K extends keyof SettingsForm>(key: K, value: SettingsForm[K]) => {
    setForm((prev) => (prev ? { ...prev, [key]: value } : prev));
    setSaved('');
  }, []);

  const save = useCallback(async () => {
    if (!form) return;
    const vErr = validateForm(form, settings);
    if (vErr) {
      setLocalError(vErr);
      return;
    }
    setSaving(true);
    setLocalError('');
    setSaved('');
    try {
      const s = await saveNotifySettings(formToRequest(form));
      setSettings(s);
      setForm(settingsToForm(s));
      setSaved('通知设置已保存。');
    } catch (err) {
      const msg = friendlyMessage(err);
      setLocalError(msg);
      onError(msg);
    } finally {
      setSaving(false);
    }
  }, [form, settings, onError]);

  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardContent className="p-0">
        <button
          type="button"
          onClick={() => setOpen((v) => !v)}
          className="flex w-full items-center justify-between gap-2 p-4 text-left"
        >
          <div className="flex items-center gap-2">
            <Settings2 className="size-4 text-primary-600 dark:text-primary-300" />
            <span className="text-sm font-semibold text-accent-950 dark:text-white">通知推送设置</span>
            {settings && (
              <Badge variant="secondary" className="text-[11px]">
                当前：{notifyTypeLabel(settings.notify_type)}
              </Badge>
            )}
          </div>
          <span className="text-xs text-accent-400 dark:text-accent-500">{open ? '收起' : '展开'}</span>
        </button>

        {open && (
          <div className="flex flex-col gap-4 border-t border-accent-200 p-4 dark:border-accent-800">
            <p className="text-xs text-accent-500 dark:text-accent-400">
              配置余额低/事件告警的推送渠道。密钥/Token 为只写：已配置时留空即沿用旧值。
            </p>

            {localError && <Banner tone="error">{localError}</Banner>}
            {saved && <Banner tone="success">{saved}</Banner>}

            {loading || !form ? (
              <LoadingBlock />
            ) : (
              <>
                {/* 渠道选择 */}
                <div className="flex flex-col gap-1.5">
                  <Label>推送渠道</Label>
                  <div className="flex flex-wrap gap-1.5">
                    {NOTIFY_TYPES.map((t) => (
                      <button
                        key={t.value}
                        type="button"
                        onClick={() => update('notify_type', t.value)}
                        className={cn(
                          'rounded-lg border px-3 py-1.5 text-sm font-medium transition-colors',
                          form.notify_type === t.value
                            ? 'border-primary-500 bg-primary-50 text-primary-700 dark:border-primary-400 dark:bg-primary-950/40 dark:text-primary-300'
                            : 'border-accent-200 bg-white text-accent-600 hover:border-accent-300 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-300',
                        )}
                      >
                        {t.label}
                      </button>
                    ))}
                  </div>
                </div>

                {/* 渠道相关字段 */}
                {form.notify_type === 'email' && (
                  <Field label="通知邮箱">
                    <TextInput
                      type="email"
                      value={form.notification_email}
                      onChange={(v) => update('notification_email', v)}
                      placeholder="you@example.com"
                    />
                  </Field>
                )}

                {form.notify_type === 'webhook' && (
                  <>
                    <Field label="Webhook URL">
                      <TextInput
                        value={form.webhook_url}
                        onChange={(v) => update('webhook_url', v)}
                        placeholder="https://example.com/hook"
                      />
                    </Field>
                    <Field
                      label="签名密钥"
                      hint={settings?.webhook_secret_configured ? '已配置，留空沿用旧值' : '必填'}
                    >
                      <TextInput
                        type="password"
                        value={form.webhook_secret}
                        onChange={(v) => update('webhook_secret', v)}
                        placeholder={settings?.webhook_secret_configured ? '••••••（不修改请留空）' : '签名密钥'}
                      />
                    </Field>
                  </>
                )}

                {form.notify_type === 'bark' && (
                  <Field label="Bark URL">
                    <TextInput
                      value={form.bark_url}
                      onChange={(v) => update('bark_url', v)}
                      placeholder="https://api.day.app/your_key"
                    />
                  </Field>
                )}

                {form.notify_type === 'gotify' && (
                  <>
                    <Field label="Gotify URL">
                      <TextInput
                        value={form.gotify_url}
                        onChange={(v) => update('gotify_url', v)}
                        placeholder="https://gotify.example.com"
                      />
                    </Field>
                    <Field
                      label="Gotify Token"
                      hint={settings?.gotify_token_configured ? '已配置，留空沿用旧值' : '必填'}
                    >
                      <TextInput
                        type="password"
                        value={form.gotify_token}
                        onChange={(v) => update('gotify_token', v)}
                        placeholder={settings?.gotify_token_configured ? '••••••（不修改请留空）' : 'App Token'}
                      />
                    </Field>
                    <Field label="优先级" hint="1–10">
                      <TextInput
                        type="number"
                        value={form.gotify_priority}
                        onChange={(v) => update('gotify_priority', v)}
                        placeholder="5"
                      />
                    </Field>
                  </>
                )}

                {/* 余额阈值（所有渠道通用） */}
                <Field label="余额告警阈值" hint="低于此值（美元）时推送；留空用系统默认">
                  <TextInput
                    type="number"
                    value={form.balance_threshold}
                    onChange={(v) => update('balance_threshold', v)}
                    placeholder="如 5"
                  />
                </Field>

                <div className="flex items-center justify-end gap-2 pt-1">
                  <Button size="sm" variant="outline" onClick={() => void load()} disabled={saving || loading}>
                    重置
                  </Button>
                  <Button size="sm" onClick={save} disabled={saving}>
                    {saving ? <Loader2 className="animate-spin" /> : null}
                    保存设置
                  </Button>
                </div>
              </>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  );
}

// ---- 表单原子组件 ----

function Label({ children }: { children: React.ReactNode }) {
  return <span className="text-xs font-medium text-accent-600 dark:text-accent-300">{children}</span>;
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center gap-2">
        <Label>{label}</Label>
        {hint && <span className="text-[11px] text-accent-400 dark:text-accent-500">{hint}</span>}
      </div>
      {children}
    </div>
  );
}

function TextInput({
  value,
  onChange,
  placeholder,
  type = 'text',
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  type?: 'text' | 'password' | 'email' | 'number';
}) {
  return (
    <input
      type={type}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={placeholder}
      className="w-full rounded-lg border border-accent-200 bg-white px-3 py-2 text-sm text-accent-900 outline-none focus:border-primary-400 focus:ring-2 focus:ring-primary-100 dark:border-accent-700 dark:bg-accent-950 dark:text-accent-100 dark:focus:ring-primary-900/40"
    />
  );
}
