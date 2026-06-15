'use client';

// admin 系统 / 审核控制台 —— 管理 token 轨（lib/api/adminSystem.ts，从 localStorage huakai_admin_token
// 取 Bearer，非 session 用户面）。多区面板：
//   · 系统健康（health-score + 各组件状态徽章）—— 需 platform_admin（adminGate）
//   · 模块清单（合并身份 + 能力 + 静态目录 + 运行时探针；纯只读知识面，无启停端点）—— 需 platform_admin
//   · 构建版本（version / commit / build_time / go_version）—— tenant_operator | platform_admin 可读
//   · 日志级别（GET 展示；PUT 切换，仅 platform_admin）
//   · 内容审核配置（enabled / fail_closed / 采样率 / 封禁阈值 / 封禁窗口 / 违规费）—— 走 ?tenant_id
//   · 审核日志（决策 / 原因 / 命中 / 违规费 / 时间）—— 走 ?tenant_id
//   · 被封 API Key（只读展示）—— 走 ?tenant_id
//   · 计费设置（只读展示 stream_input_only_interrupted_policy）—— 走 ?tenant_id
//
// 端点全部读后端 admin handler 真码确认（见 lib/api/adminSystem.ts 头注）。tenant_id：审核 / 计费区
// platform_admin 必带（页内输入，单租户默认 1）；tenant_operator 可省（用自身 scope）。系统健康 / 模块 /
// 版本 / 日志级别不需要 tenant_id。
//
// 借鉴（CLEAN-ROOM，CLAUDE.md §11/§12，仅功能 / 字段 / 布局形态，未抄码）：
//   - sub2api(LGPL) views/admin/RiskControlView.vue：内容审核「采样率 / 自动封禁 + 阈值 / 关键词模式」
//     配置面 + 审核日志列表 + 自动封禁 / 解封形态（HUAKAI 字段为准，未照搬上游字段名）。
//   - sub2api(LGPL) views/admin/SettingsView.vue：系统设置「分区卡片」运营布局。
//   - new-api(AGPL) pages/Setting（多 Tab 分区）+ pages/Log（日志列形态）+ components/settings/OperationSetting。
//   三态骨架 / 徽章配色 / 卡片 / 表格样式沿用 HUAKAI 自有 app/admin/users/page.tsx + ui 设计系统。

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Activity,
  AlertCircle,
  Ban,
  Boxes,
  CheckCircle2,
  CreditCard,
  FileText,
  Gauge,
  Loader2,
  RefreshCw,
  Save,
  ScrollText,
  ShieldAlert,
  SlidersHorizontal,
  Tag,
  Terminal,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { friendlyMessage } from '@/lib/api/errors';
import {
  LOG_LEVELS,
  type BannedAPIKey,
  type BillingSetting,
  type BuildInfo,
  type HealthComponent,
  type HealthResponse,
  type HealthStatus,
  type LogLevel,
  type ModerationConfig,
  type ModerationLog,
  type ModuleView,
  type ProbeStatus,
  decisionLabel,
  formatDateTime,
  getBillingSettings,
  getLogLevel,
  getModerationConfig,
  getSystemHealth,
  getVersion,
  healthStatusLabel,
  listBannedApiKeys,
  listModerationLogs,
  listModules,
  probeStatusLabel,
  setLogLevel,
  trimDecimal,
  updateModerationConfig,
} from '@/lib/api/adminSystem';
import { cn } from '@/lib/utils';

const DEFAULT_TENANT_ID = 1; // 单租户部署默认
const LOG_PAGE_SIZE = 20;

// ---- 徽章配色映射 ----

function healthBadgeVariant(s: HealthStatus): 'default' | 'secondary' | 'destructive' | 'outline' {
  switch (s) {
    case 'healthy':
      return 'default';
    case 'degraded':
      return 'secondary';
    case 'unhealthy':
      return 'destructive';
    default:
      return 'outline';
  }
}

function probeBadgeVariant(s: ProbeStatus): 'default' | 'secondary' | 'destructive' | 'outline' {
  switch (s) {
    case 'ok':
      return 'default';
    case 'degraded':
      return 'secondary';
    case 'error':
      return 'destructive';
    case 'unknown':
    default:
      return 'outline';
  }
}

// ---- 主页面 ----

export default function AdminSystemPage() {
  const [tenantId, setTenantId] = useState<number>(DEFAULT_TENANT_ID);

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-5">
      <PageHeader />

      <HealthSection />
      <ModulesSection />

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
        <VersionSection />
        <LogLevelSection />
      </div>

      {/* 审核 / 计费区：走 tenant_id */}
      <TenantBar tenantId={tenantId} onChange={setTenantId} />
      <ModerationConfigSection tenantId={tenantId} />
      <ModerationLogsSection tenantId={tenantId} />
      <BannedKeysSection tenantId={tenantId} />
      <BillingSettingsSection tenantId={tenantId} />
    </div>
  );
}

function PageHeader() {
  return (
    <div className="flex flex-col gap-1">
      <h1 className="text-xl font-bold text-accent-950 dark:text-white">系统 / 审核控制台</h1>
      <p className="text-sm text-accent-500 dark:text-accent-400">
        系统健康、模块探针、构建版本、运行时日志级别，以及内容审核配置 / 日志 / 封禁 / 计费设置。走管理
        token；系统健康 / 模块需 platform_admin，审核 / 计费区需指定租户 ID。
      </p>
    </div>
  );
}

// ---- 通用：错误 / 提示条 ----

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

function SectionCard({
  title,
  icon,
  action,
  children,
}: {
  title: string;
  icon: React.ReactNode;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardHeader className="flex flex-row items-center justify-between p-5 pb-3">
        <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
          {icon}
          {title}
        </CardTitle>
        {action}
      </CardHeader>
      <CardContent className="p-5 pt-0">{children}</CardContent>
    </Card>
  );
}

function LoadingRow({ label }: { label: string }) {
  return (
    <div className="flex items-center justify-center gap-2 py-10 text-sm text-accent-400">
      <Loader2 className="size-5 animate-spin" /> {label}
    </div>
  );
}

function EmptyRow({ label }: { label: string }) {
  return (
    <div className="rounded-lg border border-dashed border-accent-200 bg-accent-50 py-10 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-950/40 dark:text-accent-400">
      {label}
    </div>
  );
}

// ============================================================
// 系统健康
// ============================================================

function HealthSection() {
  const [data, setData] = useState<HealthResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setData(await getSystemHealth());
    } catch (err) {
      setError(friendlyMessage(err));
      setData(null);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <SectionCard
      title="系统健康"
      icon={<Activity className="size-4 text-primary-600 dark:text-primary-300" />}
      action={
        <div className="flex items-center gap-2">
          {data && (
            <Badge variant={healthBadgeVariant(data.status)}>总体：{healthStatusLabel(data.status)}</Badge>
          )}
          <Button onClick={() => void load()} size="sm" variant="outline" disabled={loading}>
            <RefreshCw className={cn(loading && 'animate-spin')} />
            刷新
          </Button>
        </div>
      }
    >
      {error && <ErrorBanner message={error} />}
      {loading && !data ? (
        <LoadingRow label="加载系统健康中…" />
      ) : !data ? (
        !error && <EmptyRow label="健康数据暂不可用。" />
      ) : (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {data.components.map((c: HealthComponent) => (
            <div
              key={c.name}
              className="rounded-lg border border-accent-200 bg-accent-50 p-3 dark:border-accent-800 dark:bg-accent-950/40"
            >
              <div className="flex items-center justify-between gap-2">
                <span className="text-sm font-medium text-accent-900 dark:text-accent-100">{c.name}</span>
                <Badge variant={healthBadgeVariant(c.status)}>{healthStatusLabel(c.status)}</Badge>
              </div>
              {c.detail && <div className="mt-1.5 break-all font-mono text-[11px] text-accent-400">{c.detail}</div>}
            </div>
          ))}
        </div>
      )}
    </SectionCard>
  );
}

// ============================================================
// 模块清单（只读探针）
// ============================================================

function ModulesSection() {
  const [modules, setModules] = useState<ModuleView[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [category, setCategory] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await listModules(category.trim() || undefined);
      setModules(resp.modules ?? []);
    } catch (err) {
      setError(friendlyMessage(err));
      setModules([]);
    } finally {
      setLoading(false);
    }
  }, [category]);

  useEffect(() => {
    void load();
  }, [load]);

  const categories = useMemo(() => {
    const set = new Set<string>();
    modules.forEach((m) => m.category && set.add(m.category));
    return Array.from(set).sort();
  }, [modules]);

  return (
    <SectionCard
      title="模块清单"
      icon={<Boxes className="size-4 text-primary-600 dark:text-primary-300" />}
      action={
        <div className="flex items-center gap-2">
          <input
            type="text"
            value={category}
            onChange={(e) => setCategory(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') void load();
            }}
            placeholder="按分类过滤，如 money-path"
            list="module-categories"
            className="h-9 w-48 rounded-md border border-input bg-background px-3 text-sm"
          />
          <datalist id="module-categories">
            {categories.map((c) => (
              <option key={c} value={c} />
            ))}
          </datalist>
          <Button onClick={() => void load()} size="sm" variant="outline" disabled={loading}>
            <RefreshCw className={cn(loading && 'animate-spin')} />
            刷新
          </Button>
        </div>
      }
    >
      {error && <ErrorBanner message={error} />}
      {loading && modules.length === 0 ? (
        <LoadingRow label="加载模块清单中…" />
      ) : modules.length === 0 ? (
        !error && <EmptyRow label="暂无模块（或当前分类无匹配）。" />
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>模块 / 分类</TableHead>
                <TableHead>能力</TableHead>
                <TableHead>探针</TableHead>
                <TableHead>目录身份</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {modules.map((m) => (
                <TableRow key={m.id}>
                  <TableCell>
                    <div className="font-medium text-accent-900 dark:text-accent-100">{m.title || m.id}</div>
                    <div className="text-[11px] text-accent-400">
                      <span className="font-mono">{m.id}</span>
                      {m.category && (
                        <>
                          {' · '}
                          <span className="inline-flex items-center gap-0.5">
                            <Tag className="size-2.5" />
                            {m.category}
                          </span>
                        </>
                      )}
                    </div>
                  </TableCell>
                  <TableCell className="max-w-[18rem]">
                    {m.capabilities && m.capabilities.length > 0 ? (
                      <div className="flex flex-wrap gap-1">
                        {m.capabilities.map((cap) => (
                          <span
                            key={cap}
                            className="rounded bg-accent-100 px-1.5 py-0.5 text-[10px] text-accent-600 dark:bg-accent-800 dark:text-accent-300"
                          >
                            {cap}
                          </span>
                        ))}
                      </div>
                    ) : (
                      <span className="text-xs text-accent-400">—</span>
                    )}
                  </TableCell>
                  <TableCell>
                    <Badge variant={probeBadgeVariant(m.live_probe.status)}>
                      {probeStatusLabel(m.live_probe.status)}
                    </Badge>
                    {m.live_probe.detail && (
                      <div className="mt-1 break-all text-[10px] text-accent-400">{m.live_probe.detail}</div>
                    )}
                  </TableCell>
                  <TableCell className="text-xs text-accent-500 dark:text-accent-400">
                    {m.catalog ? (
                      <div className="flex flex-col gap-0.5">
                        {m.catalog.section && <span>区段：{m.catalog.section}</span>}
                        {m.catalog.feature_id && <span className="font-mono text-[11px]">{m.catalog.feature_id}</span>}
                        {(m.catalog.status || m.catalog.parity) && (
                          <span className="text-[11px] text-accent-400">
                            {[m.catalog.status, m.catalog.parity].filter(Boolean).join(' · ')}
                          </span>
                        )}
                      </div>
                    ) : (
                      <span className="text-accent-400">—（仅运行时）</span>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
    </SectionCard>
  );
}

// ============================================================
// 构建版本
// ============================================================

function VersionSection() {
  const [info, setInfo] = useState<BuildInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let alive = true;
    setLoading(true);
    getVersion()
      .then((v) => {
        if (alive) setInfo(v);
      })
      .catch((err) => {
        if (alive) setError(friendlyMessage(err));
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, []);

  return (
    <SectionCard title="构建版本" icon={<FileText className="size-4 text-primary-600 dark:text-primary-300" />}>
      {error && <ErrorBanner message={error} />}
      {loading && !info ? (
        <LoadingRow label="加载版本信息中…" />
      ) : !info ? (
        !error && <EmptyRow label="版本信息暂不可用。" />
      ) : (
        <dl className="grid grid-cols-1 gap-x-4 gap-y-2 text-sm sm:grid-cols-[auto_1fr]">
          <KV k="版本" v={info.version} mono />
          <KV k="提交" v={info.commit} mono />
          <KV k="构建时间" v={info.build_time} />
          <KV k="Go 版本" v={info.go_version} mono />
        </dl>
      )}
    </SectionCard>
  );
}

function KV({ k, v, mono }: { k: string; v: string; mono?: boolean }) {
  return (
    <>
      <dt className="text-accent-500 dark:text-accent-400">{k}</dt>
      <dd className={cn('break-all text-accent-900 dark:text-accent-100', mono && 'font-mono text-[13px]')}>
        {v || '—'}
      </dd>
    </>
  );
}

// ============================================================
// 日志级别（GET 展示 + PUT 切换，PUT 仅 platform_admin）
// ============================================================

function LogLevelSection() {
  const [level, setLevel] = useState<string>('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await getLogLevel();
      setLevel(resp.level);
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const apply = useCallback(async (next: LogLevel) => {
    setSaving(next);
    setError(null);
    setNotice(null);
    try {
      const resp = await setLogLevel(next);
      setLevel(resp.level);
      setNotice(`日志级别已切换为 ${resp.level}。`);
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setSaving(null);
    }
  }, []);

  return (
    <SectionCard
      title="日志级别"
      icon={<Terminal className="size-4 text-primary-600 dark:text-primary-300" />}
      action={
        <Button onClick={() => void load()} size="sm" variant="outline" disabled={loading || saving !== null}>
          <RefreshCw className={cn(loading && 'animate-spin')} />
          刷新
        </Button>
      }
    >
      {error && <ErrorBanner message={error} />}
      {notice && <div className="mb-3">{<NoticeBanner message={notice} />}</div>}
      {loading && !level ? (
        <LoadingRow label="加载日志级别中…" />
      ) : (
        <div className="flex flex-col gap-3">
          <div className="text-sm text-accent-600 dark:text-accent-300">
            当前级别：
            <Badge variant="default" className="ml-2 font-mono uppercase">
              {level || '未知'}
            </Badge>
          </div>
          <div className="flex flex-wrap gap-2">
            {LOG_LEVELS.map((lv) => (
              <Button
                key={lv}
                size="sm"
                variant={lv === level ? 'default' : 'outline'}
                disabled={saving !== null || lv === level}
                onClick={() => void apply(lv)}
                className="font-mono uppercase"
              >
                {saving === lv ? <Loader2 className="size-3.5 animate-spin" /> : null}
                {lv}
              </Button>
            ))}
          </div>
          <p className="text-[11px] text-accent-400">切换日志级别为运行时操作，需 platform_admin 角色。</p>
        </div>
      )}
    </SectionCard>
  );
}

// ============================================================
// 租户输入条（审核 / 计费区共享）
// ============================================================

function TenantBar({ tenantId, onChange }: { tenantId: number; onChange: (v: number) => void }) {
  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardContent className="flex flex-wrap items-end gap-3 p-5">
        <div className="flex flex-col gap-1">
          <label className="text-xs text-accent-500 dark:text-accent-400">租户 ID（审核 / 计费区）</label>
          <input
            type="number"
            min={1}
            value={tenantId}
            onChange={(e) => onChange(Math.max(1, Number(e.target.value) || 1))}
            className="h-9 w-28 rounded-md border border-input bg-background px-3 text-sm tabular-nums"
          />
        </div>
        <p className="text-[11px] text-accent-400">
          platform_admin 必带；tenant_operator 可省（用自身 scope，此处仍传以兼容两种角色）。下方审核配置 /
          日志 / 封禁 / 计费设置随该租户切换。
        </p>
      </CardContent>
    </Card>
  );
}

// ============================================================
// 内容审核配置
// ============================================================

function ModerationConfigSection({ tenantId }: { tenantId: number }) {
  const [cfg, setCfg] = useState<ModerationConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  // 可编辑字段（本地草稿，保存时提交）
  const [enabled, setEnabled] = useState(false);
  const [failClosed, setFailClosed] = useState(false);
  const [sampleRate, setSampleRate] = useState('0');
  const [banThreshold, setBanThreshold] = useState('0');
  const [banWindow, setBanWindow] = useState('60');
  const [fee, setFee] = useState('0');

  const hydrate = useCallback((c: ModerationConfig) => {
    setCfg(c);
    setEnabled(c.enabled);
    setFailClosed(c.fail_closed);
    setSampleRate(String(c.sample_rate_pct));
    setBanThreshold(String(c.ban_threshold));
    setBanWindow(String(c.ban_window_seconds));
    setFee(trimDecimal(c.violation_fee_usd));
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    setNotice(null);
    try {
      hydrate(await getModerationConfig(tenantId));
    } catch (err) {
      setError(friendlyMessage(err));
      setCfg(null);
    } finally {
      setLoading(false);
    }
  }, [tenantId, hydrate]);

  useEffect(() => {
    void load();
  }, [load]);

  async function save() {
    const sr = Number(sampleRate);
    const bt = Number(banThreshold);
    const bw = Number(banWindow);
    if (!Number.isInteger(sr) || sr < 0 || sr > 100) {
      setError('采样率需为 0..100 的整数。');
      return;
    }
    if (!Number.isInteger(bt) || bt < 0) {
      setError('封禁阈值需为非负整数。');
      return;
    }
    if (!Number.isInteger(bw) || bw <= 0) {
      setError('封禁窗口（秒）需为正整数。');
      return;
    }
    const f = fee.trim() === '' ? '0' : fee.trim();
    if (!/^\d+(\.\d+)?$/.test(f)) {
      setError('违规费需为非负十进制数。');
      return;
    }
    setSaving(true);
    setError(null);
    setNotice(null);
    try {
      const updated = await updateModerationConfig({
        tenant_id: tenantId,
        enabled,
        fail_closed: failClosed,
        sample_rate_pct: sr,
        ban_threshold: bt,
        ban_window_seconds: bw,
        violation_fee_usd: f,
      });
      hydrate(updated);
      setNotice('审核配置已保存。');
    } catch (err) {
      setError(friendlyMessage(err));
    } finally {
      setSaving(false);
    }
  }

  return (
    <SectionCard
      title="内容审核配置"
      icon={<SlidersHorizontal className="size-4 text-primary-600 dark:text-primary-300" />}
      action={
        <Button onClick={() => void load()} size="sm" variant="outline" disabled={loading || saving}>
          <RefreshCw className={cn(loading && 'animate-spin')} />
          刷新
        </Button>
      }
    >
      {error && <ErrorBanner message={error} />}
      {notice && <div className="mb-3">{<NoticeBanner message={notice} />}</div>}
      {loading && !cfg ? (
        <LoadingRow label="加载审核配置中…" />
      ) : (
        <div className="flex flex-col gap-4">
          <div className="flex flex-wrap gap-4">
            <ToggleField label="启用内容审核" checked={enabled} onChange={setEnabled} />
            <ToggleField
              label="失败即拦截（fail_closed）"
              hint="审核后端异常时是否拒绝请求"
              checked={failClosed}
              onChange={setFailClosed}
            />
          </div>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <NumField label="采样率（%）" value={sampleRate} onChange={setSampleRate} min={0} max={100} />
            <NumField label="封禁阈值（次）" value={banThreshold} onChange={setBanThreshold} min={0} />
            <NumField label="封禁窗口（秒）" value={banWindow} onChange={setBanWindow} min={1} />
            <DecimalField label="违规费（USD）" value={fee} onChange={setFee} />
          </div>
          {cfg && (cfg.updated_by || cfg.updated_at) && (
            <div className="text-[11px] text-accent-400">
              最后更新：{cfg.updated_by ? `操作者 ${cfg.updated_by}` : ''}
              {cfg.updated_at ? ` · ${formatDateTime(cfg.updated_at)}` : ''}
            </div>
          )}
          <div className="flex justify-end">
            <Button size="sm" onClick={() => void save()} disabled={saving || loading}>
              {saving ? <Loader2 className="size-4 animate-spin" /> : <Save />}
              保存配置
            </Button>
          </div>
        </div>
      )}
    </SectionCard>
  );
}

function ToggleField({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string;
  hint?: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label className="flex cursor-pointer items-center gap-2">
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="size-4 rounded border-input accent-primary-600"
      />
      <span className="text-sm text-accent-700 dark:text-accent-200">{label}</span>
      {hint && <span className="text-[11px] text-accent-400">（{hint}）</span>}
    </label>
  );
}

function NumField({
  label,
  value,
  onChange,
  min,
  max,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  min?: number;
  max?: number;
}) {
  return (
    <div className="flex flex-col gap-1">
      <label className="text-xs text-accent-500 dark:text-accent-400">{label}</label>
      <input
        type="number"
        value={value}
        min={min}
        max={max}
        onChange={(e) => onChange(e.target.value)}
        className="h-9 rounded-md border border-input bg-background px-3 text-sm tabular-nums"
      />
    </div>
  );
}

function DecimalField({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
  return (
    <div className="flex flex-col gap-1">
      <label className="text-xs text-accent-500 dark:text-accent-400">{label}</label>
      <input
        type="text"
        inputMode="decimal"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="0.00"
        className="h-9 rounded-md border border-input bg-background px-3 text-sm tabular-nums"
      />
    </div>
  );
}

// ============================================================
// 审核日志
// ============================================================

function ModerationLogsSection({ tenantId }: { tenantId: number }) {
  const [logs, setLogs] = useState<ModerationLog[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [page, setPage] = useState(0);

  const offset = page * LOG_PAGE_SIZE;

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      // 多取 1 条用于「是否有下一页」判定（后端无 total）。
      const resp = await listModerationLogs({ tenant_id: tenantId, limit: LOG_PAGE_SIZE + 1, offset });
      setLogs(resp.items ?? []);
    } catch (err) {
      setError(friendlyMessage(err));
      setLogs([]);
    } finally {
      setLoading(false);
    }
  }, [tenantId, offset]);

  useEffect(() => {
    void load();
  }, [load]);

  // 租户变化时回到首页
  useEffect(() => {
    setPage(0);
  }, [tenantId]);

  const hasNext = logs.length > LOG_PAGE_SIZE;
  const visible = logs.slice(0, LOG_PAGE_SIZE);

  return (
    <SectionCard
      title="审核日志"
      icon={<ScrollText className="size-4 text-primary-600 dark:text-primary-300" />}
      action={
        <Button onClick={() => void load()} size="sm" variant="outline" disabled={loading}>
          <RefreshCw className={cn(loading && 'animate-spin')} />
          刷新
        </Button>
      }
    >
      {error && <ErrorBanner message={error} />}
      {loading && logs.length === 0 ? (
        <LoadingRow label="加载审核日志中…" />
      ) : visible.length === 0 ? (
        !error && <EmptyRow label="该租户暂无审核日志。" />
      ) : (
        <>
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>时间</TableHead>
                  <TableHead>判定</TableHead>
                  <TableHead>原因</TableHead>
                  <TableHead>API Key / 用户</TableHead>
                  <TableHead>命中</TableHead>
                  <TableHead className="text-right">违规费</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {visible.map((log) => (
                  <TableRow key={log.id}>
                    <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                      {formatDateTime(log.occurred_at)}
                    </TableCell>
                    <TableCell>
                      <Badge variant={log.decision === 'pass' || log.decision === 'allow' ? 'default' : 'destructive'}>
                        {decisionLabel(log.decision)}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-xs text-accent-600 dark:text-accent-300">
                      {log.reason_code || '—'}
                    </TableCell>
                    <TableCell className="text-[11px] text-accent-400">
                      <div>key #{log.api_key_id}</div>
                      <div>user #{log.user_id}</div>
                    </TableCell>
                    <TableCell className="text-[11px] text-accent-400">
                      {log.matched_keyword_id ? <div>关键词 #{log.matched_keyword_id}</div> : null}
                      {log.matched_hash_id ? <div>哈希 #{log.matched_hash_id}</div> : null}
                      {!log.matched_keyword_id && !log.matched_hash_id ? '—' : null}
                    </TableCell>
                    <TableCell className="text-right font-mono text-sm tabular-nums text-accent-900 dark:text-accent-100">
                      {trimDecimal(log.violation_fee_usd)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          <div className="mt-4 flex items-center justify-between">
            <Button
              size="sm"
              variant="outline"
              onClick={() => setPage((p) => Math.max(0, p - 1))}
              disabled={page === 0 || loading}
            >
              上一页
            </Button>
            <span className="text-xs text-accent-400">第 {page + 1} 页 · 本页 {visible.length} 条</span>
            <Button size="sm" variant="outline" onClick={() => setPage((p) => p + 1)} disabled={!hasNext || loading}>
              下一页
            </Button>
          </div>
        </>
      )}
    </SectionCard>
  );
}

// ============================================================
// 被封 API Key（只读）
// ============================================================

function BannedKeysSection({ tenantId }: { tenantId: number }) {
  const [keys, setKeys] = useState<BannedAPIKey[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await listBannedApiKeys({ tenant_id: tenantId, limit: 50 });
      setKeys(resp.items ?? []);
    } catch (err) {
      setError(friendlyMessage(err));
      setKeys([]);
    } finally {
      setLoading(false);
    }
  }, [tenantId]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <SectionCard
      title="被封 API Key"
      icon={<Ban className="size-4 text-primary-600 dark:text-primary-300" />}
      action={
        <Button onClick={() => void load()} size="sm" variant="outline" disabled={loading}>
          <RefreshCw className={cn(loading && 'animate-spin')} />
          刷新
        </Button>
      }
    >
      {error && <ErrorBanner message={error} />}
      {loading && keys.length === 0 ? (
        <LoadingRow label="加载封禁列表中…" />
      ) : keys.length === 0 ? (
        !error && <EmptyRow label="该租户暂无被封 API Key。" />
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Key</TableHead>
                <TableHead>用户</TableHead>
                <TableHead>状态</TableHead>
                <TableHead className="text-right">违规次数</TableHead>
                <TableHead>最近违规</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {keys.map((k) => (
                <TableRow key={k.id}>
                  <TableCell>
                    <div className="font-medium text-accent-900 dark:text-accent-100">{k.name || `#${k.id}`}</div>
                    <div className="font-mono text-[11px] text-accent-400">{k.key_prefix}…</div>
                  </TableCell>
                  <TableCell className="text-xs text-accent-500 dark:text-accent-400">#{k.user_id}</TableCell>
                  <TableCell>
                    <Badge variant="destructive">{k.status}</Badge>
                  </TableCell>
                  <TableCell className="text-right font-mono text-sm tabular-nums text-accent-900 dark:text-accent-100">
                    {k.violation_count}
                  </TableCell>
                  <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                    {formatDateTime(k.last_violation_at)}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          <p className="mt-3 text-[11px] text-accent-400">
            只读展示。解封操作（POST /moderation/api-keys/{'{id}'}/unban）暂未在本面提供，避免误操作。
          </p>
        </div>
      )}
    </SectionCard>
  );
}

// ============================================================
// 计费设置（只读）
// ============================================================

function BillingSettingsSection({ tenantId }: { tenantId: number }) {
  const [setting, setSetting] = useState<BillingSetting | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      setSetting(await getBillingSettings(tenantId));
    } catch (err) {
      setError(friendlyMessage(err));
      setSetting(null);
    } finally {
      setLoading(false);
    }
  }, [tenantId]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <SectionCard
      title="计费设置"
      icon={<CreditCard className="size-4 text-primary-600 dark:text-primary-300" />}
      action={
        <Button onClick={() => void load()} size="sm" variant="outline" disabled={loading}>
          <RefreshCw className={cn(loading && 'animate-spin')} />
          刷新
        </Button>
      }
    >
      {error && <ErrorBanner message={error} />}
      {loading && !setting ? (
        <LoadingRow label="加载计费设置中…" />
      ) : !setting ? (
        !error && <EmptyRow label="计费设置暂不可用。" />
      ) : (
        <div className="flex flex-col gap-4">
          <div className="rounded-lg border border-accent-200 bg-accent-50 p-4 dark:border-accent-800 dark:bg-accent-950/40">
            <div className="flex items-center justify-between gap-2">
              <span className="font-mono text-xs text-accent-600 dark:text-accent-300">{setting.key}</span>
              <Badge variant={setting.source === 'tenant' ? 'default' : 'outline'}>
                {setting.source === 'tenant' ? '租户覆盖' : '默认值'}
              </Badge>
            </div>
            <div className="mt-2 flex items-center gap-2">
              <Gauge className="size-4 text-primary-600 dark:text-primary-300" />
              <span className="font-mono text-sm font-semibold text-accent-900 dark:text-accent-100">
                {setting.value}
              </span>
            </div>
            {(setting.updated_by || setting.updated_at) && (
              <div className="mt-2 text-[11px] text-accent-400">
                {setting.updated_by ? `操作者 ${setting.updated_by}` : ''}
                {setting.updated_at ? ` · ${formatDateTime(setting.updated_at)}` : ''}
              </div>
            )}
          </div>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <ValueList title="可选值" icon={<CheckCircle2 className="size-3.5 text-emerald-500" />} values={setting.allowed_values} />
            <ValueList
              title="路线图值（暂未开放）"
              icon={<ShieldAlert className="size-3.5 text-amber-500" />}
              values={setting.roadmap_values}
            />
          </div>
          <p className="text-[11px] text-accent-400">
            只读展示。写入需 reason 且后端做租户存在性校验，本面不提供修改，避免误改计费策略。
          </p>
        </div>
      )}
    </SectionCard>
  );
}

function ValueList({ title, icon, values }: { title: string; icon: React.ReactNode; values: string[] }) {
  return (
    <div className="rounded-lg border border-accent-200 p-3 dark:border-accent-800">
      <div className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-accent-600 dark:text-accent-300">
        {icon}
        {title}
      </div>
      {values && values.length > 0 ? (
        <div className="flex flex-wrap gap-1">
          {values.map((v) => (
            <span
              key={v}
              className="rounded bg-accent-100 px-1.5 py-0.5 font-mono text-[10px] text-accent-600 dark:bg-accent-800 dark:text-accent-300"
            >
              {v}
            </span>
          ))}
        </div>
      ) : (
        <span className="text-[11px] text-accent-400">—</span>
      )}
    </div>
  );
}
