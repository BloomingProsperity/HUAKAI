'use client';

// 内容审核——黑名单管理 admin 页(管理 token 轨,@/lib/api/moderation,走 /admin/v1/moderation)。
// 覆盖关键词黑名单 + 哈希黑名单的 CRUD:列表 / 单条添加 / 批量导入(每行一项)/ 删除。
// 补接线审计 #7 的真实剩余缺口 —— 审核【配置 / 日志 / 封禁】已在 /admin/system 页实现,
// 本页只补尚缺的【黑名单维护】,两页交叉链接,不重复。
//
// 借鉴(CLEAN-ROOM,CLAUDE.md §11/§16,真 sha):
//   - sub2api@e34ad2b frontend/src/views/admin/RiskControlView.vue:内容审核含「屏蔽关键词
//     textarea(每行一项)+ 哈希黑名单维护」;new-api@1ac0f58 sensitive-words-section.tsx 仅
//     关键词 textarea(无哈希、无原因码、无逐条启用)。本页字段以 HUAKAI 后端为准,未照搬上游字段名。
//   - HUAKAI delta:关键词/哈希双表 + 逐条 reason_code + 逐条 enabled + 批量结果回显
//     (accepted/skipped_duplicate/errors),粒度高于两家(架构:黑名单与配置/日志分面;生态:批量结果可见)。

import { useCallback, useEffect, useState } from 'react';
import Link from 'next/link';
import {
  AlertCircle,
  CheckCircle2,
  ExternalLink,
  Hash,
  ListPlus,
  Loader2,
  Plus,
  RefreshCw,
  ShieldAlert,
  Trash2,
  Type,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { friendlyMessage } from '@/lib/api/errors';
import {
  bulkCreateHashes,
  bulkCreateKeywords,
  createHash,
  createKeyword,
  deleteHash,
  deleteKeyword,
  fmtDateTime,
  isValidHashHex,
  listHashes,
  listKeywords,
  parseBulkLines,
  type BulkCreateResult,
  type ModerationHash,
  type ModerationKeyword,
} from '@/lib/api/moderation';
import { cn } from '@/lib/utils';

const DEFAULT_TENANT_ID = 1;

export default function ModerationBlacklistPage() {
  const [tenantId, setTenantId] = useState<number>(DEFAULT_TENANT_ID);
  const [keywords, setKeywords] = useState<ModerationKeyword[]>([]);
  const [hashes, setHashes] = useState<ModerationHash[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [kw, hs] = await Promise.all([listKeywords(tenantId), listHashes(tenantId)]);
      setKeywords(kw.items);
      setHashes(hs.items);
    } catch (e) {
      setError(friendlyMessage(e));
    } finally {
      setLoading(false);
    }
  }, [tenantId]);

  useEffect(() => {
    void load();
  }, [load]);

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h1 className="flex items-center gap-2 text-xl font-bold text-accent-950 dark:text-white">
          <ShieldAlert className="size-5 text-primary-600 dark:text-primary-300" />
          内容审核 · 黑名单
        </h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          维护关键词黑名单与负载哈希(sha256)黑名单:命中则按审核配置拦截 / 计费。审核【配置 / 日志 / 封禁】在
          <Link href="/admin/system" className="mx-1 inline-flex items-center gap-0.5 text-primary-600 hover:underline dark:text-primary-300">
            系统 / 审核控制台 <ExternalLink className="size-3" />
          </Link>
          。走管理 token。
        </p>
      </div>

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardContent className="flex flex-wrap items-end gap-3 p-4">
          <div className="flex flex-col gap-1">
            <label className="text-[11px] text-accent-500 dark:text-accent-400">租户 ID</label>
            <input
              type="number"
              min={1}
              value={tenantId}
              onChange={(e) => setTenantId(Math.max(1, Number(e.target.value) || 1))}
              className="h-9 w-24 rounded-md border border-input bg-background px-3 text-sm tabular-nums"
            />
          </div>
          <p className="text-[11px] text-accent-400">
            platform_admin 必带;tenant_operator 可省(此处仍传以兼容两种角色)。黑名单随该租户切换。
          </p>
          <Button size="sm" variant="outline" onClick={() => void load()} disabled={loading || busy} className="ml-auto">
            <RefreshCw className={cn(loading && 'animate-spin')} />
            刷新
          </Button>
        </CardContent>
      </Card>

      {error && <Banner tone="error" message={error} />}
      {notice && <Banner tone="notice" message={notice} />}

      <BlacklistSection
        kind="keyword"
        tenantId={tenantId}
        items={keywords}
        loading={loading}
        busy={busy}
        setBusy={setBusy}
        onError={setError}
        onNotice={setNotice}
        onReload={load}
      />

      <BlacklistSection
        kind="hash"
        tenantId={tenantId}
        items={hashes}
        loading={loading}
        busy={busy}
        setBusy={setBusy}
        onError={setError}
        onNotice={setNotice}
        onReload={load}
      />
    </div>
  );
}

// ── 黑名单分区(关键词 / 哈希 通用)────────────────────────────────────

interface SectionProps {
  kind: 'keyword' | 'hash';
  tenantId: number;
  items: Array<ModerationKeyword | ModerationHash>;
  loading: boolean;
  busy: boolean;
  setBusy: (b: boolean) => void;
  onError: (m: string | null) => void;
  onNotice: (m: string | null) => void;
  onReload: () => Promise<void>;
}

function BlacklistSection({ kind, tenantId, items, loading, busy, setBusy, onError, onNotice, onReload }: SectionProps) {
  const isHash = kind === 'hash';
  const title = isHash ? '哈希黑名单' : '关键词黑名单';
  const valueLabel = isHash ? '哈希(64 位小写 hex)' : '关键词';
  const Icon = isHash ? Hash : Type;

  const [value, setValue] = useState('');
  const [reason, setReason] = useState('');
  const [bulkText, setBulkText] = useState('');
  const [bulkOpen, setBulkOpen] = useState(false);
  const [delId, setDelId] = useState<number | null>(null);

  const getValue = (it: ModerationKeyword | ModerationHash): string =>
    isHash ? (it as ModerationHash).hash_hex : (it as ModerationKeyword).keyword;

  // 单条添加
  const handleCreate = async () => {
    const v = value.trim();
    if (!v) {
      onError(isHash ? '请输入哈希。' : '请输入关键词。');
      return;
    }
    if (isHash && !isValidHashHex(v)) {
      onError('哈希需为 64 位小写十六进制(sha256)。');
      return;
    }
    setBusy(true);
    onError(null);
    onNotice(null);
    try {
      if (isHash) {
        await createHash({ tenant_id: tenantId, hash_hex: v, reason_code: reason.trim(), enabled: true });
      } else {
        await createKeyword({ tenant_id: tenantId, keyword: v, reason_code: reason.trim(), enabled: true });
      }
      setValue('');
      setReason('');
      onNotice(`已添加 1 条${isHash ? '哈希' : '关键词'}。`);
      await onReload();
    } catch (e) {
      onError(friendlyMessage(e));
    } finally {
      setBusy(false);
    }
  };

  // 批量导入(每行一项;哈希先本地校验剔除非法)
  const handleBulk = async () => {
    const lines = parseBulkLines(bulkText);
    if (lines.length === 0) {
      onError('批量内容为空。');
      return;
    }
    if (lines.length > 1000) {
      onError(`一次最多 1000 行,当前 ${lines.length} 行。`);
      return;
    }
    let invalid = 0;
    let valid = lines;
    if (isHash) {
      valid = lines.filter((l) => isValidHashHex(l));
      invalid = lines.length - valid.length;
      if (valid.length === 0) {
        onError('没有合法的 64 位小写 hex 行。');
        return;
      }
    }
    setBusy(true);
    onError(null);
    onNotice(null);
    try {
      const rc = reason.trim();
      let res: BulkCreateResult;
      if (isHash) {
        res = await bulkCreateHashes(
          tenantId,
          valid.map((hash_hex) => ({ hash_hex, reason_code: rc, enabled: true })),
        );
      } else {
        res = await bulkCreateKeywords(
          tenantId,
          valid.map((keyword) => ({ keyword, reason_code: rc, enabled: true })),
        );
      }
      const parts = [`接受 ${res.accepted}`, `跳过重复 ${res.skipped_duplicate}`];
      if (res.errors.length > 0) parts.push(`错误 ${res.errors.length}`);
      if (invalid > 0) parts.push(`本地剔除非法 ${invalid}`);
      onNotice(`批量导入完成:${parts.join(' / ')}。`);
      setBulkText('');
      setBulkOpen(false);
      await onReload();
    } catch (e) {
      onError(friendlyMessage(e));
    } finally {
      setBusy(false);
    }
  };

  const handleDelete = async (id: number) => {
    setBusy(true);
    setDelId(id);
    onError(null);
    onNotice(null);
    try {
      if (isHash) await deleteHash(id, tenantId);
      else await deleteKeyword(id, tenantId);
      onNotice('已删除 1 条。');
      await onReload();
    } catch (e) {
      onError(friendlyMessage(e));
    } finally {
      setBusy(false);
      setDelId(null);
    }
  };

  return (
    <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-3 p-5 pb-3">
        <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
          <Icon className="size-4 text-primary-600 dark:text-primary-300" />
          {title}
          <Badge variant="secondary">{items.length}</Badge>
        </CardTitle>
        <Button size="sm" variant="outline" onClick={() => setBulkOpen((v) => !v)} disabled={busy}>
          <ListPlus />
          批量导入
        </Button>
      </CardHeader>
      <CardContent className="flex flex-col gap-4 p-5 pt-0">
        {/* 单条添加 */}
        <div className="flex flex-wrap items-end gap-2">
          <div className="flex min-w-[14rem] flex-1 flex-col gap-1">
            <label className="text-[11px] text-accent-500 dark:text-accent-400">{valueLabel}</label>
            <input
              value={value}
              onChange={(e) => setValue(e.target.value)}
              placeholder={isHash ? '64 位小写 hex' : '要屏蔽的关键词'}
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
            />
          </div>
          <div className="flex w-40 flex-col gap-1">
            <label className="text-[11px] text-accent-500 dark:text-accent-400">原因码(可选)</label>
            <input
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="如 policy"
              className="h-9 rounded-md border border-input bg-background px-3 text-sm"
            />
          </div>
          <Button size="sm" onClick={() => void handleCreate()} disabled={busy}>
            <Plus />
            添加
          </Button>
        </div>

        {/* 批量导入面板 */}
        {bulkOpen && (
          <div className="flex flex-col gap-2 rounded-md border border-dashed border-accent-300 bg-accent-50/50 p-3 dark:border-accent-700 dark:bg-accent-900/40">
            <label className="text-[11px] text-accent-500 dark:text-accent-400">
              每行一项{isHash ? '(非法的 64 位 hex 行会被本地剔除)' : ''},共享上方原因码,最多 1000 行
            </label>
            <textarea
              value={bulkText}
              onChange={(e) => setBulkText(e.target.value)}
              rows={5}
              placeholder={isHash ? '每行一个 64 位小写 hex' : '每行一个关键词'}
              className="rounded-md border border-input bg-background p-2 font-mono text-xs"
            />
            <div className="flex items-center gap-2">
              <Button size="sm" onClick={() => void handleBulk()} disabled={busy}>
                {busy ? <Loader2 className="size-4 animate-spin" /> : <ListPlus />}
                导入
              </Button>
              <span className="text-[11px] text-accent-400">解析后 {parseBulkLines(bulkText).length} 行</span>
            </div>
          </div>
        )}

        {/* 列表 */}
        {loading && items.length === 0 ? (
          <CenterLoader text="加载中…" />
        ) : items.length === 0 ? (
          <EmptyState text={`当前租户暂无${title}条目。`} />
        ) : (
          <div className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{isHash ? '哈希' : '关键词'}</TableHead>
                  <TableHead>原因码</TableHead>
                  <TableHead>状态</TableHead>
                  <TableHead>创建时间</TableHead>
                  <TableHead className="text-right">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((it) => (
                  <TableRow key={it.id}>
                    <TableCell
                      className={cn(
                        'text-sm text-accent-900 dark:text-accent-100',
                        isHash && 'max-w-[20rem] truncate font-mono text-xs',
                      )}
                      title={getValue(it)}
                    >
                      {getValue(it)}
                    </TableCell>
                    <TableCell className="text-xs text-accent-500 dark:text-accent-400">{it.reason_code || '—'}</TableCell>
                    <TableCell>
                      <Badge variant={it.enabled ? 'default' : 'secondary'}>{it.enabled ? '启用' : '停用'}</Badge>
                    </TableCell>
                    <TableCell className="whitespace-nowrap text-xs text-accent-500 dark:text-accent-400">
                      {fmtDateTime(it.created_at)}
                    </TableCell>
                    <TableCell>
                      <div className="flex items-center justify-end">
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => void handleDelete(it.id)}
                          disabled={busy}
                          title="删除"
                          className="text-red-600 hover:text-red-700 dark:text-red-400"
                        >
                          {delId === it.id ? <Loader2 className="size-4 animate-spin" /> : <Trash2 />}
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

// ── 局部辅助组件 ────────────────────────────────────────────────────

function Banner({ tone, message }: { tone: 'error' | 'notice'; message: string }) {
  const error = tone === 'error';
  return (
    <div
      className={cn(
        'flex items-center gap-2 rounded-md border px-3 py-2 text-sm',
        error
          ? 'border-red-200 bg-red-50 text-red-700 dark:border-red-900/50 dark:bg-red-950/40 dark:text-red-300'
          : 'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-900/50 dark:bg-emerald-950/40 dark:text-emerald-300',
      )}
    >
      {error ? <AlertCircle className="size-4 shrink-0" /> : <CheckCircle2 className="size-4 shrink-0" />}
      {message}
    </div>
  );
}

function CenterLoader({ text }: { text: string }) {
  return (
    <div className="flex items-center justify-center gap-2 py-8 text-sm text-accent-400">
      <Loader2 className="size-4 animate-spin" />
      {text}
    </div>
  );
}

function EmptyState({ text }: { text: string }) {
  return <div className="py-8 text-center text-sm text-accent-400">{text}</div>;
}
