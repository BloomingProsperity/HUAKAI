'use client';

// admin 凭证 + 出站代理控制台 —— 管理 token 轨（lib/api/adminCredentials.ts，从 localStorage
// huakai_admin_token 取 Bearer，非 session 用户面）。两区：
//   ① 凭证：续期状态列表（renew-status：账号 / vendor / 状态 / 过期窗 / 续期窗口 / 失败计数，游标分页）
//      + 导入入口（paste / cli / csv / json 四法 + OAuth 起步）。导入 / OAuth 起步均需 platform_admin。
//   ② 代理：出站代理池列表 / 新建 / 编辑 / 删除 / 启停（set-status）。
//
// 端点全部读后端 admin handler 真码确认（见 lib/api/adminCredentials.ts 头注）：
//   - 续期状态读路径复用 lib/api/renew.ts（GET /admin/v1/credentials/renew-status，游标分页）。
//   - 导入 helper：POST /admin/v1/credentials/{paste,cli-import,csv-import,json-import}（platform_admin）。
//   - OAuth 起步：POST /admin/v1/credentials/oauth-init（platform_admin）。
//   - 代理：/admin/v1/proxies CRUD + PUT /{id}/status（tenant_operator 省 tenant_id；platform_admin 必带）。
//
// 借鉴（CLEAN-ROOM，CLAUDE.md §11/§12，仅功能/字段/动作/布局形态，未抄码；逐源注明）：
//   - sub2api(LGPL)@e34ad2b views/admin/ProxiesView.vue：代理列表 protocol+status 过滤、列集合
//     （protocol / 地址 host:port / 状态徽章 / 操作）、行动作（编辑 / 删除）、create 表单字段
//     （name/protocol/host/port/username/password）。HUAKAI 后端代理无 expires_at / test / quality
//     字段，故不照搬 sub2api 的过期窗 / 探测列。
//   - sub2api(LGPL)@e34ad2b views/admin/AccountsView.vue + components/account/CreateAccountModal.vue：
//     凭证导入入口（paste / cli / csv / json 多法）+ 按 vendor 起 OAuth 的形态、账号凭证「平台 / 状态 /
//     过期」列形态 → 映射到 HUAKAI renew-status 的 vendor/auth_mode/state/access_expires_at 等字段。
//   三态骨架 / 徽章配色 / 卡片 / 表格 / ModalShell 样式沿用 HUAKAI 自有 app/admin/users/page.tsx。

import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertCircle,
  CheckCircle2,
  Cloud,
  ExternalLink,
  KeyRound,
  Loader2,
  RefreshCw,
  ShieldAlert,
  Upload,
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
  expiryHint,
  formatDateTime,
  importCredentials,
  initOAuth,
  listRenewStatus,
  renewStateBadgeVariant,
  renewStateLabel,
  VENDORS,
  type AuthCredentialRenewStatus,
  type ImportKind,
} from '@/lib/api/adminCredentials';
import { cn } from '@/lib/utils';

const DEFAULT_TENANT_ID = 1; // 单租户部署默认
const RENEW_PAGE_SIZE = 50;

// ---- 主页面 ----

export default function AdminCredentialsPage() {
  const [tenantId, setTenantId] = useState<number>(DEFAULT_TENANT_ID);

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-5">
      <PageHeader />

      {/* 租户选择 */}
      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardContent className="flex flex-wrap items-end gap-4 p-5">
          <div className="flex flex-col gap-1">
            <label className="text-xs text-accent-500 dark:text-accent-400">租户 ID（platform_admin 必填）</label>
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

      <CredentialsSection tenantId={tenantId} />
    </div>
  );
}

function PageHeader() {
  return (
    <div className="flex flex-col gap-1">
      <h1 className="text-xl font-bold text-accent-950 dark:text-white">凭证续期</h1>
      <p className="text-sm text-accent-500 dark:text-accent-400">
        管理上游账号凭证续期状态 + 导入新凭证（粘贴 / CLI / CSV / JSON / OAuth）。走管理 token。出站代理池已迁至「代理池」页。
      </p>
    </div>
  );
}

// =====================================================================================
//  区 ① 凭证续期状态 + 导入
// =====================================================================================

function CredentialsSection({ tenantId }: { tenantId: number }) {
  const [items, setItems] = useState<AuthCredentialRenewStatus[]>([]);
  const [cursorStack, setCursorStack] = useState<string[]>([]); // 已访问页的游标（用于「上一页」）
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [vendorFilter, setVendorFilter] = useState<string>('all');
  const [importOpen, setImportOpen] = useState(false);
  const [oauthOpen, setOAuthOpen] = useState(false);

  const currentCursor = cursorStack.length > 0 ? cursorStack[cursorStack.length - 1] : undefined;

  const load = useCallback(
    async (cursor?: string) => {
      setLoading(true);
      setError(null);
      try {
        const resp = await listRenewStatus({ limit: RENEW_PAGE_SIZE, cursor, tenantId });
        setItems(resp.items ?? []);
        setNextCursor(resp.next_cursor ?? null);
      } catch (err) {
        setError(friendlyMessage(err));
        setItems([]);
        setNextCursor(null);
      } finally {
        setLoading(false);
      }
    },
    [tenantId],
  );

  // 租户变化 → 回到首页。
  useEffect(() => {
    setCursorStack([]);
    void load(undefined);
  }, [load]);

  const visibleItems = useMemo(() => {
    if (vendorFilter === 'all') return items;
    return items.filter((it) => it.vendor === vendorFilter);
  }, [items, vendorFilter]);

  // 当前页出现过的 vendor 集合（用于过滤下拉）。
  const vendorsOnPage = useMemo(() => {
    const set = new Set<string>();
    items.forEach((it) => it.vendor && set.add(it.vendor));
    return Array.from(set).sort();
  }, [items]);

  function goNext() {
    if (!nextCursor) return;
    setCursorStack((s) => [...s, nextCursor]);
    void load(nextCursor);
  }

  function goPrev() {
    setCursorStack((s) => {
      const copy = s.slice(0, -1);
      void load(copy.length > 0 ? copy[copy.length - 1] : undefined);
      return copy;
    });
  }

  return (
    <>
      {error && <ErrorBanner message={error} />}
      {notice && <NoticeBanner message={notice} />}

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <KeyRound className="size-4 text-primary-600 dark:text-primary-300" />
            凭证续期状态
          </CardTitle>
          <div className="flex flex-wrap items-center gap-2">
            <select
              value={vendorFilter}
              onChange={(e) => setVendorFilter(e.target.value)}
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
              title="按 vendor 过滤本页"
            >
              <option value="all">全部 vendor</option>
              {vendorsOnPage.map((v) => (
                <option key={v} value={v}>
                  {v}
                </option>
              ))}
            </select>
            <Button size="sm" variant="outline" onClick={() => setOAuthOpen(true)}>
              <Cloud />
              OAuth 起步
            </Button>
            <Button size="sm" onClick={() => setImportOpen(true)}>
              <Upload />
              导入凭证
            </Button>
            <Button size="sm" variant="outline" onClick={() => void load(currentCursor)} disabled={loading}>
              <RefreshCw className={cn(loading && 'animate-spin')} />
            </Button>
          </div>
        </CardHeader>
        <CardContent className="p-5 pt-0">
          {loading && items.length === 0 ? (
            <CenterLoader text="加载续期状态中…" />
          ) : visibleItems.length === 0 ? (
            <EmptyState text={vendorFilter !== 'all' ? '本页没有该 vendor 的凭证。' : '当前租户暂无凭证续期记录。'} />
          ) : (
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>账号 / vendor</TableHead>
                    <TableHead>鉴权模式</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>访问令牌到期</TableHead>
                    <TableHead>续期窗口</TableHead>
                    <TableHead>最近续期</TableHead>
                    <TableHead className="text-right">失败</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {visibleItems.map((it) => {
                    const accessHint = expiryHint(it.access_expires_at);
                    const refreshHint = expiryHint(it.refresh_before_at);
                    return (
                      <TableRow key={it.id}>
                        <TableCell>
                          <div className="font-medium text-accent-900 dark:text-accent-100">
                            {it.account_name || `账号 #${it.account_id}`}
                          </div>
                          <div className="text-[11px] text-accent-400">
                            #{it.id} · {it.vendor} · v{it.credential_version}
                          </div>
                        </TableCell>
                        <TableCell className="text-xs text-accent-600 dark:text-accent-300">{it.auth_mode}</TableCell>
                        <TableCell>
                          <Badge variant={renewStateBadgeVariant(it.state)}>{renewStateLabel(it.state)}</Badge>
                        </TableCell>
                        <TableCell className="whitespace-nowrap text-xs">
                          <div className="text-accent-600 dark:text-accent-300">{formatDateTime(it.access_expires_at)}</div>
                          {accessHint && (
                            <div className={cn('text-[11px]', accessHint.urgent ? 'text-red-500' : 'text-accent-400')}>
                              {accessHint.label}
                            </div>
                          )}
                        </TableCell>
                        <TableCell className="whitespace-nowrap text-xs">
                          <div className="text-accent-600 dark:text-accent-300">{formatDateTime(it.refresh_before_at)}</div>
                          {refreshHint && (
                            <div className={cn('text-[11px]', refreshHint.urgent ? 'text-red-500' : 'text-accent-400')}>
                              {refreshHint.label}
                            </div>
                          )}
                        </TableCell>
                        <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                          <div>{formatDateTime(it.last_refresh_at)}</div>
                          {it.last_refresh_outcome && (
                            <div className="text-[11px] text-accent-400">{it.last_refresh_outcome}</div>
                          )}
                        </TableCell>
                        <TableCell className="text-right">
                          {it.failure_count > 0 ? (
                            <div className="flex items-center justify-end gap-1 text-red-500" title={it.failure_class ?? undefined}>
                              <ShieldAlert className="size-3.5" />
                              <span className="font-mono text-sm tabular-nums">{it.failure_count}</span>
                            </div>
                          ) : (
                            <span className="text-xs text-accent-400">0</span>
                          )}
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            </div>
          )}

          {/* 游标分页 */}
          <div className="mt-4 flex items-center justify-between">
            <Button size="sm" variant="outline" onClick={goPrev} disabled={cursorStack.length === 0 || loading}>
              上一页
            </Button>
            <span className="text-xs text-accent-400">第 {cursorStack.length + 1} 页 · 本页 {items.length} 条</span>
            <Button size="sm" variant="outline" onClick={goNext} disabled={!nextCursor || loading}>
              下一页
            </Button>
          </div>
        </CardContent>
      </Card>

      {importOpen && (
        <ImportCredentialModal
          tenantId={tenantId}
          onClose={() => setImportOpen(false)}
          onDone={(msg) => {
            setImportOpen(false);
            setNotice(msg);
            setCursorStack([]);
            void load(undefined);
          }}
        />
      )}
      {oauthOpen && (
        <OAuthInitModal
          tenantId={tenantId}
          onClose={() => setOAuthOpen(false)}
          onDone={(msg) => {
            setNotice(msg);
          }}
        />
      )}
    </>
  );
}

// ---- 导入凭证弹窗（paste / cli / csv / json）----

const IMPORT_KINDS: { value: ImportKind; label: string; usesContent: boolean; hint: string }[] = [
  { value: 'paste', label: '粘贴 JSON', usesContent: false, hint: '粘贴单个凭证 JSON 对象，例如 {"api_key":"sk-..."}。' },
  { value: 'cli-import', label: 'CLI 文件', usesContent: true, hint: '粘贴 CLI 凭证文件原文（如 auth.json / credentials 文件内容）。' },
  { value: 'csv-import', label: 'CSV', usesContent: true, hint: '粘贴 CSV 文本，每行一条凭证。' },
  { value: 'json-import', label: 'JSON 批量', usesContent: true, hint: '粘贴 JSON 数组 / 多条，后端解析为多个候选。' },
];

function ImportCredentialModal({
  tenantId,
  onClose,
  onDone,
}: {
  tenantId: number;
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const [kind, setKind] = useState<ImportKind>('paste');
  const [accountId, setAccountId] = useState('');
  const [vendor, setVendor] = useState<string>('anthropic');
  const [authMode, setAuthMode] = useState('');
  const [text, setText] = useState('');
  const [finalize, setFinalize] = useState(true);
  const [reason, setReason] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  const kindMeta = IMPORT_KINDS.find((k) => k.value === kind) ?? IMPORT_KINDS[0];

  async function submit() {
    const acct = Number(accountId.trim());
    if (!Number.isInteger(acct) || acct <= 0) {
      setLocalError('请填写有效的上游账号 ID（provider_account_id）。');
      return;
    }
    if (text.trim() === '') {
      setLocalError('请粘贴凭证内容。');
      return;
    }
    if (kind === 'paste') {
      try {
        JSON.parse(text);
      } catch {
        setLocalError('粘贴 JSON 模式要求合法的 JSON 对象。');
        return;
      }
    }
    setSubmitting(true);
    setLocalError(null);
    try {
      const res = await importCredentials({
        kind,
        tenant_id: tenantId,
        provider_account_id: acct,
        vendor,
        auth_mode: authMode.trim() || undefined,
        content: kindMeta.usesContent ? text : undefined,
        credentials: kind === 'paste' ? text : undefined,
        finalize,
        reason: reason.trim() || undefined,
      });
      const flowCount = res.flows?.length ?? 0;
      const finCount = res.finalized?.length ?? 0;
      onDone(
        finalize
          ? `已导入并落库 ${finCount} 条凭证（建立 ${flowCount} 个会话）。`
          : `已建立 ${flowCount} 个导入会话（未落库，待后续 finalize）。`,
      );
    } catch (err) {
      setLocalError(friendlyMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <ModalShell title="导入凭证" icon={<Upload className="size-4 text-primary-600 dark:text-primary-300" />} onClose={onClose} wide>
      <div className="flex flex-col gap-3">
        {/* 导入方式切换 */}
        <div className="flex flex-wrap gap-1.5">
          {IMPORT_KINDS.map((k) => (
            <button
              key={k.value}
              type="button"
              onClick={() => setKind(k.value)}
              className={cn(
                'rounded-md border px-3 py-1.5 text-xs font-medium transition-colors',
                kind === k.value
                  ? 'border-primary-500 bg-primary-50 text-primary-700 dark:border-primary-500 dark:bg-primary-950/40 dark:text-primary-300'
                  : 'border-accent-200 text-accent-600 hover:bg-accent-100 dark:border-accent-800 dark:text-accent-300 dark:hover:bg-accent-800',
              )}
            >
              {k.label}
            </button>
          ))}
        </div>

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <div className="flex flex-col gap-1">
            <label className="text-xs text-accent-500 dark:text-accent-400">上游账号 ID</label>
            <input
              type="number"
              min={1}
              value={accountId}
              onChange={(e) => setAccountId(e.target.value)}
              placeholder="provider_account_id"
              className="h-9 rounded-md border border-input bg-background px-3 text-sm tabular-nums"
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs text-accent-500 dark:text-accent-400">vendor</label>
            <select
              value={vendor}
              onChange={(e) => setVendor(e.target.value)}
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
            >
              {VENDORS.map((v) => (
                <option key={v} value={v}>
                  {v}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs text-accent-500 dark:text-accent-400">auth_mode（可选）</label>
            <input
              type="text"
              value={authMode}
              onChange={(e) => setAuthMode(e.target.value)}
              placeholder="api_key / codex_cli_oauth …"
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
            />
          </div>
        </div>

        <div className="flex flex-col gap-1">
          <label className="text-xs text-accent-500 dark:text-accent-400">凭证内容</label>
          <textarea
            rows={6}
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder={kind === 'paste' ? '{"api_key":"sk-..."}' : '粘贴原始文件 / CSV / JSON 文本'}
            className="rounded-md border border-input bg-background px-3 py-2 font-mono text-xs"
          />
          <p className="text-[11px] text-accent-400">{kindMeta.hint}</p>
        </div>

        <div className="flex flex-wrap items-center justify-between gap-3">
          <label className="flex items-center gap-2 text-xs text-accent-600 dark:text-accent-300">
            <input type="checkbox" checked={finalize} onChange={(e) => setFinalize(e.target.checked)} />
            立即落库（finalize）
          </label>
          <input
            type="text"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            placeholder="操作原因（审计，可选）"
            className="h-9 flex-1 rounded-md border border-input bg-background px-3 text-sm"
          />
        </div>

        <p className="text-[11px] text-accent-400">说明：导入端点要求 platform_admin 角色。</p>
        {localError && <InlineError message={localError} />}
        <div className="flex justify-end gap-2 pt-1">
          <Button size="sm" variant="outline" onClick={onClose} disabled={submitting}>
            取消
          </Button>
          <Button size="sm" onClick={() => void submit()} disabled={submitting}>
            {submitting ? <Loader2 className="size-4 animate-spin" /> : <Upload />}
            导入
          </Button>
        </div>
      </div>
    </ModalShell>
  );
}

// ---- OAuth 起步弹窗 ----

function OAuthInitModal({
  tenantId,
  onClose,
  onDone,
}: {
  tenantId: number;
  onClose: () => void;
  onDone: (msg: string) => void;
}) {
  const [accountId, setAccountId] = useState('');
  const [vendor, setVendor] = useState<string>('openai');
  const [authMode, setAuthMode] = useState('chatgpt_oauth');
  const [clientId, setClientId] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);
  const [authorizeUrl, setAuthorizeUrl] = useState<string | null>(null);

  async function submit() {
    const acct = Number(accountId.trim());
    if (!Number.isInteger(acct) || acct <= 0) {
      setLocalError('请填写有效的上游账号 ID（provider_account_id）。');
      return;
    }
    if (vendor.trim() === '' || authMode.trim() === '') {
      setLocalError('vendor 与 auth_mode 必填。');
      return;
    }
    setSubmitting(true);
    setLocalError(null);
    try {
      const res = await initOAuth({
        tenant_id: tenantId,
        provider_account_id: acct,
        vendor: vendor.trim(),
        auth_mode: authMode.trim(),
        oauth_client: clientId.trim() ? { client_id: clientId.trim() } : undefined,
      });
      if (res.authorize_url) {
        setAuthorizeUrl(res.authorize_url);
        onDone(`已发起 OAuth 流程（会话 ${res.flow.id.slice(0, 8)}…），请打开授权链接完成授权。`);
      } else {
        onDone(`已创建 OAuth 会话 ${res.flow.id.slice(0, 8)}…（无授权链接，可能为设备码流程）。`);
        onClose();
      }
    } catch (err) {
      setLocalError(friendlyMessage(err));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <ModalShell title="OAuth 起步" icon={<Cloud className="size-4 text-primary-600 dark:text-primary-300" />} onClose={onClose}>
      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <label className="text-xs text-accent-500 dark:text-accent-400">上游账号 ID</label>
          <input
            type="number"
            min={1}
            value={accountId}
            onChange={(e) => setAccountId(e.target.value)}
            placeholder="provider_account_id"
            className="h-9 rounded-md border border-input bg-background px-3 text-sm tabular-nums"
          />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label className="text-xs text-accent-500 dark:text-accent-400">vendor</label>
            <select
              value={vendor}
              onChange={(e) => setVendor(e.target.value)}
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
            >
              {VENDORS.map((v) => (
                <option key={v} value={v}>
                  {v}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs text-accent-500 dark:text-accent-400">auth_mode</label>
            <input
              type="text"
              value={authMode}
              onChange={(e) => setAuthMode(e.target.value)}
              placeholder="chatgpt_oauth / claude_ai_oauth …"
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
            />
          </div>
        </div>
        <div className="flex flex-col gap-1">
          <label className="text-xs text-accent-500 dark:text-accent-400">client_id（可选；Gemini / ChatGPT 的密钥由后端注入）</label>
          <input
            type="text"
            value={clientId}
            onChange={(e) => setClientId(e.target.value)}
            placeholder="自定义 OAuth client_id"
            className="h-9 rounded-md border border-input bg-background px-3 text-sm"
          />
        </div>

        {authorizeUrl && (
          <a
            href={authorizeUrl}
            target="_blank"
            rel="noreferrer noopener"
            className="flex items-center justify-center gap-2 rounded-md border border-primary-300 bg-primary-50 px-3 py-2 text-sm font-medium text-primary-700 hover:bg-primary-100 dark:border-primary-700 dark:bg-primary-950/40 dark:text-primary-300"
          >
            <ExternalLink className="size-4" />
            打开授权链接
          </a>
        )}

        <p className="text-[11px] text-accent-400">说明：OAuth 起步要求 platform_admin 角色。授权完成后由后端回调落库凭证。</p>
        {localError && <InlineError message={localError} />}
        <div className="flex justify-end gap-2 pt-1">
          <Button size="sm" variant="outline" onClick={onClose} disabled={submitting}>
            {authorizeUrl ? '关闭' : '取消'}
          </Button>
          {!authorizeUrl && (
            <Button size="sm" onClick={() => void submit()} disabled={submitting}>
              {submitting ? <Loader2 className="size-4 animate-spin" /> : <Cloud />}
              发起
            </Button>
          )}
        </div>
      </div>
    </ModalShell>
  );
}

// =====================================================================================
//  共享小组件
// =====================================================================================

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
