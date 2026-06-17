'use client';

// admin 模型目录同步触发控制台（管理 token 轨，lib/api/adminModelSync.ts）。前端只接线测功能,不追设计。
//
// 端点读后端真码 model_sync_handler.go / cmd/gateway/routes.go（POST /admin/v1/model-sync）。
// 鉴权 platform_admin only、无 tenant_id（全局模型目录，影响所有继承 global catalog 的租户）。
// 触发即返回【本次】逐厂商差量结算；后端【无】同步历史/状态 GET、无调度端点 → 见页内 roadmap 提示。
//
// 借鉴对照详见 lib/api/model-sync-form.ts 头与 plan artifact（new-api 有跨渠道 apply-all + 定时 ticker 但仅
// 逐渠道 add/remove；sub2api 有按账号 sync-upstream 取扁平列表无差量；CLIProxyAPI 无管理员触发的上游账号目录拉取）。
// HUAKAI delta：单 platform_admin【全局目录】触发 + 逐厂商【新增/更新/复活/停用/未变/快照递增】差量结算 + reason
// 审计 actor。注：后端 total_added 已含复活（service.go:127），故顶部用「新增/复活」与逐厂商两列对账。骨架沿用 app/admin 样式。

import { useState } from 'react';
import { AlertCircle, CheckCircle2, Info, Loader2, RefreshCw } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { friendlyMessage } from '@/lib/api/errors';
import { triggerModelSync, type ModelSyncResult } from '@/lib/api/adminModelSync';
import {
  MAX_SYNC_REASON_LEN,
  syncHadChanges,
  validateModelSyncReason,
  vendorChangeCount,
} from '@/lib/api/model-sync-form';
import { cn } from '@/lib/utils';

const labelCls = 'text-xs text-accent-500 dark:text-accent-400';
const inputCls = 'h-9 w-full rounded-md border border-input bg-background px-3 text-sm';

export default function AdminModelSyncPage() {
  const [reason, setReason] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [result, setResult] = useState<ModelSyncResult | null>(null);

  // trim 后码点数（与后端 RuneCount 同口径），用于计数器与超限提示。
  const reasonLen = [...reason.trim()].length;
  const overLimit = reasonLen > MAX_SYNC_REASON_LEN;

  async function handleTrigger() {
    const v = validateModelSyncReason(reason);
    if (v) {
      setFormError(v);
      return;
    }
    setFormError(null);
    setLoading(true);
    setError(null);
    setNotice(null);
    try {
      const res = await triggerModelSync(reason);
      setResult(res);
      // 后端 total_added 已含复活（service.go:127 TotalAdded += Added + Reactivated），故文案用「新增/复活」
      // 与逐厂商表的 新增 + 复活 两列对账，避免同名「新增」跨面歧义。
      setNotice(
        syncHadChanges(res)
          ? `同步完成：新增/复活 ${res.total_added} · 更新 ${res.total_updated} · 停用 ${res.total_disabled}。`
          : '同步完成：模型目录无变更。',
      );
    } catch (err) {
      // 触发失败时清掉上一次成功结果，避免「本次结果」卡片在红色错误下仍展示旧数据（误导）。
      setResult(null);
      setError(friendlyMessage(err));
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="mx-auto flex max-w-5xl flex-col gap-5">
      <div className="flex flex-col gap-1">
        <h1 className="text-xl font-bold text-accent-950 dark:text-white">模型目录同步</h1>
        <p className="text-sm text-accent-500 dark:text-accent-400">
          从上游厂商拉取并对账全局模型目录，逐厂商结算新增 / 更新 / 复活 / 停用。需 platform_admin（全局目录，影响所有继承全局目录的租户）。
        </p>
      </div>

      {error && <Banner kind="error" text={error} />}
      {notice && <Banner kind="ok" text={notice} />}

      <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <CardHeader>
          <CardTitle className="text-base">触发同步</CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <label className="flex flex-col gap-1">
            <span className={labelCls}>同步原因（可选，记入审计；留空记为 admin_manual）</span>
            <input
              className={cn(inputCls, overLimit && 'border-red-400')}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="例如：上游发布新模型，手动对账"
              disabled={loading}
            />
            <span className={cn('text-xs', overLimit ? 'text-red-600 dark:text-red-400' : labelCls)}>
              {reasonLen} / {MAX_SYNC_REASON_LEN} 字符
            </span>
          </label>
          {formError && <span className="text-xs text-red-600 dark:text-red-400">{formError}</span>}
          <div className="flex items-center gap-3">
            <Button onClick={handleTrigger} disabled={loading || overLimit} className="gap-2">
              {loading ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
              {loading ? '同步中…' : '触发同步'}
            </Button>
            {result && (
              <span className={labelCls}>上次完成：{fmtTime(result.completed_at)}</span>
            )}
          </div>
          <p className="flex items-start gap-1.5 text-xs text-accent-400 dark:text-accent-500">
            <Info className="mt-0.5 size-3.5 shrink-0" />
            同步历史 / 自动调度暂未提供（后端无对应端点），此处仅触发并展示【本次】结果；历史与定时同步已登记后续 roadmap。
          </p>
        </CardContent>
      </Card>

      {result && (
        <Card className="border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70">
          <CardHeader className="flex flex-row items-center justify-between gap-2">
            <CardTitle className="text-base">本次结果</CardTitle>
            <div className="flex flex-wrap gap-2">
              <Badge variant="outline">新增/复活 {result.total_added}</Badge>
              <Badge variant="outline">更新 {result.total_updated}</Badge>
              <Badge variant="outline">停用 {result.total_disabled}</Badge>
            </div>
          </CardHeader>
          <CardContent>
            {result.results.length === 0 ? (
              <p className="py-6 text-center text-sm text-accent-400 dark:text-accent-500">无厂商参与本次同步。</p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>厂商</TableHead>
                    <TableHead className="text-right">新增</TableHead>
                    <TableHead className="text-right">更新</TableHead>
                    <TableHead className="text-right">复活</TableHead>
                    <TableHead className="text-right">停用</TableHead>
                    <TableHead className="text-right">未变</TableHead>
                    <TableHead className="text-right">快照递增</TableHead>
                    <TableHead className="text-right">变更合计</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {result.results.map((item) => {
                    const changed = vendorChangeCount(item);
                    return (
                      <TableRow key={item.vendor}>
                        <TableCell className="font-medium">{item.vendor}</TableCell>
                        <TableCell className="text-right">{item.added}</TableCell>
                        <TableCell className="text-right">{item.updated}</TableCell>
                        <TableCell className="text-right">{item.reactivated}</TableCell>
                        <TableCell className="text-right">{item.disabled}</TableCell>
                        <TableCell className="text-right text-accent-400">{item.unchanged}</TableCell>
                        <TableCell className="text-right text-accent-400">{item.snapshot_bumps}</TableCell>
                        <TableCell className="text-right">
                          {changed > 0 ? (
                            <Badge variant="outline">{changed}</Badge>
                          ) : (
                            <span className="text-accent-400">0</span>
                          )}
                        </TableCell>
                      </TableRow>
                    );
                  })}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      )}
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

function fmtTime(v?: string): string {
  if (!v) return '—';
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false });
}
