'use client';

// admin 平台设置控制台 —— 管理 token 轨（lib/api/adminSettings.ts，从 localStorage huakai_admin_token
// 取 Bearer，非 session 用户面）。三个分区：
//   ① 平台设置：按运营语义分组卡片（注册/安全/站点/稳定性/签到推荐/运维），逐项 get → 改 → PUT 单 key。
//      **需 platform_admin**（非 platform_admin → 403 admin_forbidden）。
//   ② 邮件设置（SMTP）：单租户表单（host/port/账号密码/发件人/TLS/邮箱验证）+ 发测试信。platform/tenant 均可。
//   ③ TLS 指纹画像：只读列表（CRUD 写入留待后续切片）。需 platform_admin。
//
// 端点全部读后端 admin handler 真码确认（见 lib/api/adminSettings.ts 头注，含 file:line）。
//
// 借鉴（CLEAN-ROOM，CLAUDE.md §11/§12，仅功能/字段/布局形态，未抄码）：
//   - sub2api(LGPL) views/admin/SettingsView.vue：设置中心「分 Tab + 分组卡片」IA。HUAKAI 后端是 per-key
//     get/update（非整表 batch save），故每项独立保存，不照搬其单 form 整体提交。
//   - new-api 系统设置页：开关卡 + 文本配置项分组形态。
//   三态骨架 / 徽章配色 / 卡片样式沿用 HUAKAI 自有 app/admin/users/page.tsx。

import { useCallback, useEffect, useState } from 'react';
import {
  AlertCircle,
  CheckCircle2,
  Database,
  Fingerprint,
  Loader2,
  Mail,
  RefreshCw,
  Save,
  Send,
  Settings2,
  ShieldCheck,
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
  EMAIL_KEYS,
  MANAGED_SETTING_KEYS,
  SETTINGS_GROUPS,
  formatDateTime,
  getEmailSettings,
  isTrue,
  listPlatformSettings,
  listTLSProfiles,
  sendEmailTest,
  sourceLabel,
  updateEmailSettings,
  updatePlatformSetting,
  validateIntInput,
  type EmailSettingsUpdate,
  type PlatformSetting,
  type SettingDescriptor,
  type TLSFingerprintProfile,
} from '@/lib/api/adminSettings';
import { cn } from '@/lib/utils';

const DEFAULT_TENANT_ID = 1; // 单租户部署默认

type TabKey = 'platform' | 'email' | 'tls';

const TABS: { key: TabKey; label: string; icon: React.ReactNode }[] = [
  { key: 'platform', label: '平台设置', icon: <Settings2 className="size-4" /> },
  { key: 'email', label: '邮件设置', icon: <Mail className="size-4" /> },
  { key: 'tls', label: 'TLS 指纹画像', icon: <Fingerprint className="size-4" /> },
];

export default function AdminSettingsPage() {
  const [tab, setTab] = useState<TabKey>('platform');

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-bold text-accent-950 dark:text-white">平台设置</h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          配置注册 / 安全 / 站点 / 稳定性 / 邮件 / TLS 指纹。走管理 token；平台设置与 TLS 列表需 platform_admin 角色。
        </p>
      </div>

      {/* Tab 导航 */}
      <div className="flex flex-wrap gap-2 border-b border-accent-200 pb-2 dark:border-accent-800">
        {TABS.map((t) => (
          <button
            key={t.key}
            type="button"
            onClick={() => setTab(t.key)}
            className={cn(
              'flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm font-medium transition-colors',
              tab === t.key
                ? 'bg-primary-600 text-white'
                : 'text-accent-600 hover:bg-accent-100 dark:text-accent-300 dark:hover:bg-accent-800',
            )}
          >
            {t.icon}
            {t.label}
          </button>
        ))}
      </div>

      {tab === 'platform' && <PlatformSettingsTab />}
      {tab === 'email' && <EmailSettingsTab />}
      {tab === 'tls' && <TLSProfilesTab />}
    </div>
  );
}

// ---- 通用提示条 ----

function Banner({ kind, text }: { kind: 'error' | 'success'; text: string }) {
  if (kind === 'error') {
    return (
      <div className="flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
        <AlertCircle className="mt-0.5 size-4 shrink-0" />
        <span>{text}</span>
      </div>
    );
  }
  return (
    <div className="flex items-start gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-4 py-3 text-sm text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">
      <CheckCircle2 className="mt-0.5 size-4 shrink-0" />
      <span>{text}</span>
    </div>
  );
}

// ============================================================================
// Tab ①：平台设置（分组卡片，逐项 PUT 单 key）
// ============================================================================

function PlatformSettingsTab() {
  const [byKey, setByKey] = useState<Record<string, PlatformSetting>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const items = await listPlatformSettings();
      const map: Record<string, PlatformSetting> = {};
      for (const item of items) {
        if (MANAGED_SETTING_KEYS.has(item.key)) map[item.key] = item;
      }
      setByKey(map);
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // 单 key 保存后局部刷新该项（避免整页重载）。
  const onSaved = useCallback((updated: PlatformSetting, msg: string) => {
    setByKey((prev) => ({ ...prev, [updated.key]: updated }));
    setNotice(msg);
    setError(null);
  }, []);

  if (loading && Object.keys(byKey).length === 0) {
    return (
      <div className="flex items-center justify-center gap-2 py-20 text-sm text-accent-400">
        <Loader2 className="size-5 animate-spin" /> 加载平台设置中…
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-end">
        <Button size="sm" variant="outline" onClick={() => void load()}>
          <RefreshCw className={cn(loading && 'animate-spin')} />
          刷新
        </Button>
      </div>

      {error && <Banner kind="error" text={error} />}
      {notice && <Banner kind="success" text={notice} />}

      {SETTINGS_GROUPS.map((group) => (
        <Card
          key={group.id}
          className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70"
        >
          <CardHeader className="p-5 pb-3">
            <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
              <ShieldCheck className="size-4 text-primary-600 dark:text-primary-300" />
              {group.title}
            </CardTitle>
            {group.description && (
              <p className="text-xs text-accent-500 dark:text-accent-400">{group.description}</p>
            )}
          </CardHeader>
          <CardContent className="flex flex-col gap-3 p-5 pt-0">
            {group.keys.map((desc) => {
              const setting = byKey[desc.key];
              if (!setting) {
                return (
                  <div
                    key={desc.key}
                    className="rounded-lg border border-dashed border-accent-200 px-3 py-2 text-xs text-accent-400 dark:border-accent-800"
                  >
                    {desc.label}（后端未返回此项 · {desc.key}）
                  </div>
                );
              }
              return (
                <SettingRow
                  key={desc.key}
                  desc={desc}
                  setting={setting}
                  onSaved={onSaved}
                  onError={(m) => {
                    setError(m);
                    setNotice(null);
                  }}
                />
              );
            })}
          </CardContent>
        </Card>
      ))}
    </div>
  );
}

function SettingRow({
  desc,
  setting,
  onSaved,
  onError,
}: {
  desc: SettingDescriptor;
  setting: PlatformSetting;
  onSaved: (updated: PlatformSetting, msg: string) => void;
  onError: (msg: string) => void;
}) {
  // 受控草稿值；外部 setting 变化时重置。
  const [draft, setDraft] = useState(setting.value);
  const [saving, setSaving] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  useEffect(() => {
    setDraft(setting.value);
  }, [setting.value]);

  const dirty = draft !== setting.value;

  async function save(nextValue: string) {
    if (desc.control === 'int') {
      const err = validateIntInput(nextValue, desc.intMin);
      if (err) {
        setLocalError(err);
        return;
      }
    }
    setSaving(true);
    setLocalError(null);
    try {
      const updated = await updatePlatformSetting(desc.key, nextValue);
      onSaved(updated, `已更新「${desc.label}」。`);
    } catch (err) {
      onError(friendlyMessage(err));
    } finally {
      setSaving(false);
    }
  }

  // bool 用即时切换；其余用「编辑 + 保存」。
  if (desc.control === 'bool') {
    const on = isTrue(setting.value);
    return (
      <div className="flex items-center justify-between gap-3 rounded-lg border border-accent-100 px-3 py-2.5 dark:border-accent-800/60">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-medium text-accent-900 dark:text-accent-100">
            {desc.label}
            {setting.health?.status === 'degraded' && (
              <Badge variant="destructive" className="text-[10px]">
                {setting.health.issue || '降级'}
              </Badge>
            )}
            <SourceTag setting={setting} />
          </div>
          {desc.help && <p className="mt-0.5 text-[11px] text-accent-400">{desc.help}</p>}
          {localError && <p className="mt-0.5 text-[11px] text-red-500">{localError}</p>}
        </div>
        <Button
          size="sm"
          variant={on ? 'default' : 'outline'}
          disabled={saving}
          onClick={() => void save(on ? 'false' : 'true')}
          className="shrink-0"
        >
          {saving ? <Loader2 className="size-4 animate-spin" /> : on ? '已开启' : '已关闭'}
        </Button>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-1.5 rounded-lg border border-accent-100 px-3 py-2.5 dark:border-accent-800/60">
      <div className="flex items-center gap-2 text-sm font-medium text-accent-900 dark:text-accent-100">
        {desc.label}
        <SourceTag setting={setting} />
      </div>
      {desc.help && <p className="text-[11px] text-accent-400">{desc.help}</p>}
      <div className="flex items-start gap-2">
        {desc.control === 'textarea' ? (
          <textarea
            rows={2}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            className="flex-1 rounded-md border border-input bg-background px-3 py-2 text-sm"
          />
        ) : (
          <input
            type={desc.control === 'int' ? 'number' : 'text'}
            inputMode={desc.control === 'int' ? 'numeric' : undefined}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            className="h-9 flex-1 rounded-md border border-input bg-background px-3 text-sm"
          />
        )}
        <Button size="sm" disabled={saving || !dirty} onClick={() => void save(draft)} className="shrink-0">
          {saving ? <Loader2 className="size-4 animate-spin" /> : <Save />}
          保存
        </Button>
      </div>
      {localError && <p className="text-[11px] text-red-500">{localError}</p>}
    </div>
  );
}

function SourceTag({ setting }: { setting: PlatformSetting }) {
  return (
    <span className="text-[10px] text-accent-400" title={setting.updated_at ? `更新于 ${formatDateTime(setting.updated_at)}` : undefined}>
      {sourceLabel(setting.source)}
    </span>
  );
}

// ============================================================================
// Tab ②：邮件 SMTP 设置 + 测试
// ============================================================================

interface EmailFormState {
  smtp_host: string;
  smtp_port: string;
  smtp_username: string;
  smtp_password: string; // 空=不改（占位提示已配置）
  smtp_from: string;
  smtp_from_name: string;
  smtp_use_tls: boolean;
  email_verify_enabled: boolean;
}

const EMPTY_EMAIL_FORM: EmailFormState = {
  smtp_host: '',
  smtp_port: '',
  smtp_username: '',
  smtp_password: '',
  smtp_from: '',
  smtp_from_name: '',
  smtp_use_tls: true,
  email_verify_enabled: false,
};

function EmailSettingsTab() {
  const [tenantId, setTenantId] = useState<number>(DEFAULT_TENANT_ID);
  const [form, setForm] = useState<EmailFormState>(EMPTY_EMAIL_FORM);
  const [passwordConfigured, setPasswordConfigured] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testTo, setTestTo] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await getEmailSettings(tenantId);
      const next = { ...EMPTY_EMAIL_FORM };
      let pwConfigured = false;
      for (const row of resp.settings ?? []) {
        switch (row.key) {
          case EMAIL_KEYS.host:
            next.smtp_host = row.value;
            break;
          case EMAIL_KEYS.port:
            next.smtp_port = row.value;
            break;
          case EMAIL_KEYS.username:
            next.smtp_username = row.value;
            break;
          case EMAIL_KEYS.password:
            pwConfigured = row.configured === true; // value 被脱敏成空串
            break;
          case EMAIL_KEYS.from:
            next.smtp_from = row.value;
            break;
          case EMAIL_KEYS.fromName:
            next.smtp_from_name = row.value;
            break;
          case EMAIL_KEYS.tls:
            next.smtp_use_tls = row.value === 'true';
            break;
          case EMAIL_KEYS.verify:
            next.email_verify_enabled = row.value === 'true';
            break;
        }
      }
      setForm(next);
      setPasswordConfigured(pwConfigured);
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setLoading(false);
    }
  }, [tenantId]);

  useEffect(() => {
    void load();
  }, [load]);

  async function save() {
    setError(null);
    setNotice(null);
    // 端口前端轻校验（后端权威：1..65535）。
    if (form.smtp_port.trim() !== '') {
      const p = Number(form.smtp_port);
      if (!Number.isInteger(p) || p < 1 || p > 65535) {
        setError('SMTP 端口需为 1..65535 的整数。');
        return;
      }
    }
    const update: EmailSettingsUpdate = { tenant_id: tenantId };
    if (form.smtp_host.trim() !== '') update.smtp_host = form.smtp_host.trim();
    if (form.smtp_port.trim() !== '') update.smtp_port = Number(form.smtp_port);
    if (form.smtp_username.trim() !== '') update.smtp_username = form.smtp_username.trim();
    // 密码：仅当用户输入了内容才提交（空=保留原值）。
    if (form.smtp_password !== '') update.smtp_password = form.smtp_password;
    if (form.smtp_from.trim() !== '') update.smtp_from = form.smtp_from.trim();
    if (form.smtp_from_name.trim() !== '') update.smtp_from_name = form.smtp_from_name.trim();
    update.smtp_use_tls = form.smtp_use_tls;
    update.email_verify_enabled = form.email_verify_enabled;

    setSaving(true);
    try {
      const res = await updateEmailSettings(update);
      setNotice(`已保存邮件设置（更新 ${res.updated} 项）。`);
      setForm((f) => ({ ...f, smtp_password: '' })); // 清空密码输入框
      await load();
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setSaving(false);
    }
  }

  async function runTest() {
    if (testTo.trim() === '') {
      setError('请填写测试收件邮箱。');
      return;
    }
    setError(null);
    setNotice(null);
    setTesting(true);
    try {
      await sendEmailTest(tenantId, testTo.trim());
      setNotice(`测试邮件已发送至 ${testTo.trim()}。`);
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setTesting(false);
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-end justify-between gap-3">
        <div className="flex flex-col gap-1">
          <label className="text-xs text-accent-500 dark:text-accent-400">租户 ID</label>
          <input
            type="number"
            min={1}
            value={tenantId}
            onChange={(e) => setTenantId(Math.max(1, Number(e.target.value) || 1))}
            className="h-9 w-24 rounded-md border border-input bg-background px-3 text-sm tabular-nums"
          />
        </div>
        <Button size="sm" variant="outline" onClick={() => void load()}>
          <RefreshCw className={cn(loading && 'animate-spin')} />
          刷新
        </Button>
      </div>

      {error && <Banner kind="error" text={error} />}
      {notice && <Banner kind="success" text={notice} />}

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <Mail className="size-4 text-primary-600 dark:text-primary-300" />
            SMTP 邮件设置
          </CardTitle>
          <p className="text-xs text-accent-500 dark:text-accent-400">
            留空的字段不会改变；密码留空表示保留原值。只更新有内容的项。
          </p>
        </CardHeader>
        <CardContent className="grid grid-cols-1 gap-3 p-5 pt-0 md:grid-cols-2">
          {loading ? (
            <div className="col-span-full flex items-center justify-center gap-2 py-10 text-sm text-accent-400">
              <Loader2 className="size-5 animate-spin" /> 加载中…
            </div>
          ) : (
            <>
              <Field label="SMTP 主机">
                <input
                  type="text"
                  value={form.smtp_host}
                  onChange={(e) => setForm((f) => ({ ...f, smtp_host: e.target.value }))}
                  placeholder="smtp.example.com"
                  className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm"
                />
              </Field>
              <Field label="端口（1..65535）">
                <input
                  type="number"
                  value={form.smtp_port}
                  onChange={(e) => setForm((f) => ({ ...f, smtp_port: e.target.value }))}
                  placeholder="465"
                  className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm tabular-nums"
                />
              </Field>
              <Field label="用户名">
                <input
                  type="text"
                  value={form.smtp_username}
                  onChange={(e) => setForm((f) => ({ ...f, smtp_username: e.target.value }))}
                  placeholder="apikey / 账号"
                  className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm"
                />
              </Field>
              <Field label={passwordConfigured ? '密码（已配置 · 留空保留）' : '密码'}>
                <input
                  type="password"
                  value={form.smtp_password}
                  onChange={(e) => setForm((f) => ({ ...f, smtp_password: e.target.value }))}
                  placeholder={passwordConfigured ? '••••••（已设置）' : '输入 SMTP 密码'}
                  autoComplete="new-password"
                  className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm"
                />
              </Field>
              <Field label="发件地址">
                <input
                  type="text"
                  value={form.smtp_from}
                  onChange={(e) => setForm((f) => ({ ...f, smtp_from: e.target.value }))}
                  placeholder="noreply@example.com"
                  className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm"
                />
              </Field>
              <Field label="发件人名称">
                <input
                  type="text"
                  value={form.smtp_from_name}
                  onChange={(e) => setForm((f) => ({ ...f, smtp_from_name: e.target.value }))}
                  placeholder="HUAKAI"
                  className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm"
                />
              </Field>
              <ToggleField
                label="使用 TLS"
                checked={form.smtp_use_tls}
                onChange={(v) => setForm((f) => ({ ...f, smtp_use_tls: v }))}
              />
              <ToggleField
                label="要求邮箱验证"
                checked={form.email_verify_enabled}
                onChange={(v) => setForm((f) => ({ ...f, email_verify_enabled: v }))}
              />
              <div className="col-span-full flex justify-end pt-1">
                <Button size="sm" disabled={saving} onClick={() => void save()}>
                  {saving ? <Loader2 className="size-4 animate-spin" /> : <Save />}
                  保存邮件设置
                </Button>
              </div>
            </>
          )}
        </CardContent>
      </Card>

      {/* 发测试信 */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <Send className="size-4 text-primary-600 dark:text-primary-300" />
            发送测试邮件
          </CardTitle>
          <p className="text-xs text-accent-500 dark:text-accent-400">
            用当前已保存的 SMTP 配置发一封测试信，验证连通性。
          </p>
        </CardHeader>
        <CardContent className="flex flex-wrap items-end gap-3 p-5 pt-0">
          <div className="flex min-w-[16rem] flex-1 flex-col gap-1">
            <label className="text-xs text-accent-500 dark:text-accent-400">收件邮箱</label>
            <input
              type="email"
              value={testTo}
              onChange={(e) => setTestTo(e.target.value)}
              placeholder="you@example.com"
              className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm"
            />
          </div>
          <Button size="sm" variant="outline" disabled={testing} onClick={() => void runTest()}>
            {testing ? <Loader2 className="size-4 animate-spin" /> : <Send />}
            发送测试
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1">
      <label className="text-xs text-accent-500 dark:text-accent-400">{label}</label>
      {children}
    </div>
  );
}

function ToggleField({
  label,
  checked,
  onChange,
}: {
  label: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between rounded-lg border border-accent-100 px-3 py-2 dark:border-accent-800/60">
      <span className="text-sm font-medium text-accent-900 dark:text-accent-100">{label}</span>
      <Button size="sm" variant={checked ? 'default' : 'outline'} onClick={() => onChange(!checked)}>
        {checked ? '已开启' : '已关闭'}
      </Button>
    </div>
  );
}

// ============================================================================
// Tab ③：TLS 指纹画像（只读列表）
// ============================================================================

function TLSProfilesTab() {
  const [tenantId, setTenantId] = useState<number>(DEFAULT_TENANT_ID);
  const [profiles, setProfiles] = useState<TLSFingerprintProfile[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setProfiles(await listTLSProfiles(tenantId));
    } catch (err) {
      setError(friendlyMessage(err));
      setProfiles([]);
    } finally {
      setLoading(false);
    }
  }, [tenantId]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-end justify-between gap-3">
        <div className="flex flex-col gap-1">
          <label className="text-xs text-accent-500 dark:text-accent-400">租户 ID</label>
          <input
            type="number"
            min={1}
            value={tenantId}
            onChange={(e) => setTenantId(Math.max(1, Number(e.target.value) || 1))}
            className="h-9 w-24 rounded-md border border-input bg-background px-3 text-sm tabular-nums"
          />
        </div>
        <Button size="sm" variant="outline" onClick={() => void load()}>
          <RefreshCw className={cn(loading && 'animate-spin')} />
          刷新
        </Button>
      </div>

      {error && <Banner kind="error" text={error} />}

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <Fingerprint className="size-4 text-primary-600 dark:text-primary-300" />
            TLS 指纹画像（只读）
          </CardTitle>
          <p className="text-xs text-accent-500 dark:text-accent-400">
            出站 TLS 指纹（JA3）画像列表。创建 / 编辑留待后续切片，本面仅查看。
          </p>
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {loading ? (
            <div className="flex items-center justify-center gap-2 py-12 text-sm text-accent-400">
              <Loader2 className="size-5 animate-spin" /> 加载中…
            </div>
          ) : profiles.length === 0 ? (
            <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
              当前租户暂无 TLS 指纹画像。
            </div>
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>名称</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>JA3 / GREASE</TableHead>
                    <TableHead>ALPN</TableHead>
                    <TableHead>更新时间</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {profiles.map((p) => (
                    <TableRow key={p.id}>
                      <TableCell>
                        <div className="font-medium text-accent-900 dark:text-accent-100">{p.name}</div>
                        <div className="text-[11px] text-accent-400">#{p.id}</div>
                        {p.description && (
                          <div className="mt-0.5 max-w-[18rem] truncate text-[11px] text-accent-400" title={p.description}>
                            {p.description}
                          </div>
                        )}
                      </TableCell>
                      <TableCell>
                        <Badge variant={p.status === 'active' ? 'default' : 'destructive'}>
                          {p.status === 'active' ? '启用' : p.status === 'disabled' ? '已停用' : p.status}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <div className="flex items-center gap-1 font-mono text-[11px] text-accent-600 dark:text-accent-300">
                          <Database className="size-3 shrink-0" />
                          {p.expected_ja3_hash ? `${p.expected_ja3_hash.slice(0, 12)}…` : '—'}
                        </div>
                        <div className="text-[11px] text-accent-400">
                          GREASE {p.grease_enabled ? '开' : '关'}
                        </div>
                      </TableCell>
                      <TableCell className="text-[11px] text-accent-500 dark:text-accent-400">
                        {p.alpn_protocols.length > 0 ? p.alpn_protocols.join(', ') : '—'}
                      </TableCell>
                      <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                        {formatDateTime(p.updated_at)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
