'use client';

import { FormEvent, Suspense, useCallback, useEffect, useState } from 'react';
import { KeyRound, RefreshCw, Search, ShieldAlert, ShieldCheck } from 'lucide-react';
import { useRouter, useSearchParams } from 'next/navigation';
import { HopChainTimeline } from '@/components/audit/HopChainTimeline';
import { MerkleProofPanel } from '@/components/audit/MerkleProofPanel';
import { ModelChainCard } from '@/components/audit/ModelChainCard';
import { VerifyStatusBadge } from '@/components/audit/VerifyStatusBadge';
import type { AuditBundle, AuditVerifyResult } from '@/lib/audit-api';
import {
  createDispute,
  disputeStatusLabel,
  fetchAuditPubkey,
  fetchMyDisputes,
  formatMicroUSD,
  loadAuditBundleForRequest,
  receiptStatusLabel,
  verifyStoredReceipt,
  type AuditPubkey,
  type CostDispute,
  type ReceiptVerifyResponse,
  type UserCostReceipt,
} from '@/lib/api/audit';
import { friendlyMessage } from '@/lib/api/errors';

function formatDateTime(value?: string) {
  if (!value) return '未记录';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false });
}

function Stat({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="min-w-0 rounded-lg border border-accent-200 bg-accent-50 p-3 dark:border-accent-800 dark:bg-accent-950/70">
      <div className="text-xs text-accent-500 dark:text-accent-400">{label}</div>
      <div className="mt-1 break-all font-mono text-sm font-semibold text-accent-950 dark:text-accent-50">{value || '未记录'}</div>
    </div>
  );
}

function SectionCard({
  title,
  icon,
  children,
}: {
  title: string;
  icon?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="rounded-lg border border-accent-200 bg-white p-5 shadow-card dark:border-accent-800 dark:bg-accent-900/70">
      <h2 className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
        {icon}
        {title}
      </h2>
      <div className="mt-3">{children}</div>
    </section>
  );
}

export default function AuditPage() {
  return (
    <Suspense fallback={<div className="rounded-lg border border-accent-200 bg-white p-5 text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-900/70 dark:text-accent-400">审计页面加载中…</div>}>
      <AuditPageClient />
    </Suspense>
  );
}

function AuditPageClient() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const queryRequestId = searchParams.get('request_id') ?? '';

  const [requestId, setRequestId] = useState(queryRequestId);

  // ---- Section 1：回执查验 + hop 链 + Merkle ----
  const [receipt, setReceipt] = useState<UserCostReceipt | null>(null);
  const [bundle, setBundle] = useState<AuditBundle | null>(null);
  const [result, setResult] = useState<AuditVerifyResult | null>(null);
  const [signedVerify, setSignedVerify] = useState<ReceiptVerifyResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [lookupError, setLookupError] = useState('');

  // ---- Section 2：我的争议 ----
  const [disputes, setDisputes] = useState<CostDispute[]>([]);
  const [disputesError, setDisputesError] = useState('');
  const [disputesLoading, setDisputesLoading] = useState(false);
  const [reason, setReason] = useState('');
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState('');
  const [createOk, setCreateOk] = useState('');

  // ---- Section 3：公开公钥 ----
  const [pubkey, setPubkey] = useState<AuditPubkey | null>(null);
  const [pubkeyError, setPubkeyError] = useState('');

  const loadLookup = useCallback(async (rawRequestId: string) => {
    const nextRequestId = rawRequestId.trim();
    setReceipt(null);
    setBundle(null);
    setResult(null);
    setSignedVerify(null);
    setLookupError('');
    if (!nextRequestId) return;

    setLoading(true);
    try {
      const loaded = await loadAuditBundleForRequest(nextRequestId);
      setReceipt(loaded.receipt);
      setBundle(loaded.bundle);
      setResult(loaded.result);
      // 已存回执签名校验（独立容错，失败不影响 hop 链展示）。
      try {
        setSignedVerify(await verifyStoredReceipt(nextRequestId));
      } catch {
        setSignedVerify(null);
      }
    } catch (err) {
      setLookupError(friendlyMessage(err));
    } finally {
      setLoading(false);
    }
  }, []);

  const loadDisputes = useCallback(async () => {
    setDisputesLoading(true);
    setDisputesError('');
    try {
      setDisputes(await fetchMyDisputes());
    } catch (err) {
      setDisputesError(friendlyMessage(err));
    } finally {
      setDisputesLoading(false);
    }
  }, []);

  // 进入页面即拉「我的争议」与「公开公钥」，各自独立 503 容错。
  useEffect(() => {
    void loadDisputes();
    fetchAuditPubkey()
      .then((key) => {
        setPubkey(key);
        setPubkeyError('');
      })
      .catch((err) => setPubkeyError(friendlyMessage(err)));
  }, [loadDisputes]);

  useEffect(() => {
    setRequestId(queryRequestId);
    void loadLookup(queryRequestId);
  }, [loadLookup, queryRequestId]);

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const next = requestId.trim();
    router.replace(next ? `/audit?request_id=${encodeURIComponent(next)}` : '/audit');
  }

  async function handleCreateDispute(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const next = queryRequestId.trim();
    const trimmedReason = reason.trim();
    setCreateError('');
    setCreateOk('');
    if (!next) {
      setCreateError('请先查询一条回执后再发起争议。');
      return;
    }
    if (!trimmedReason) {
      setCreateError('请填写争议理由。');
      return;
    }
    setCreating(true);
    try {
      await createDispute(next, { reason: trimmedReason });
      setReason('');
      setCreateOk('争议已提交，等待运营处理。');
      await loadDisputes();
    } catch (err) {
      setCreateError(friendlyMessage(err));
    } finally {
      setCreating(false);
    }
  }

  const entry = bundle?.verify.ledger_entry;
  const proof = bundle?.verify.chain_proof;
  const tree = bundle?.tree;

  return (
    <div className="space-y-6">
      {/* 标题 + 查询表单 */}
      <section className="rounded-lg border border-accent-200 bg-white p-5 shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <div className="flex flex-col justify-between gap-4 xl:flex-row xl:items-start">
          <div className="min-w-0">
            <p className="text-xs font-medium text-primary-700 dark:text-primary-300">T6 信任链</p>
            <h1 className="mt-1 text-2xl font-bold tracking-normal text-accent-950 dark:text-white">审计与回执验查</h1>
            <p className="mt-2 max-w-3xl text-sm leading-6 text-accent-500 dark:text-accent-400">
              输入 request_id 查验单次请求的成本回执、六跳链路、模型三方比对与 Merkle 链 proof，并在浏览器内尝试 ed25519 验签。下方可查看我的争议、对回执发起争议，以及当前公开审计公钥。
            </p>
          </div>
          {result && <VerifyStatusBadge status={result.status} />}
        </div>

        <form className="mt-5 flex flex-col gap-3 md:flex-row" onSubmit={handleSubmit}>
          <label className="sr-only" htmlFor="request_id">request_id</label>
          <input
            id="request_id"
            className="min-h-11 flex-1 rounded-lg border border-accent-200 bg-accent-50 px-3 font-mono text-sm text-accent-950 outline-none ring-primary-500/20 transition focus:ring-4 dark:border-accent-800 dark:bg-accent-950/70 dark:text-accent-50"
            onChange={(event) => setRequestId(event.target.value)}
            placeholder="req_xxx"
            value={requestId}
          />
          <button className="inline-flex min-h-11 items-center gap-2 rounded-lg bg-primary-600 px-4 text-sm font-semibold text-white hover:bg-primary-700 disabled:cursor-wait disabled:opacity-70" disabled={loading} type="submit">
            {loading ? <RefreshCw className="size-4 animate-spin" /> : <Search className="size-4" />}
            查询
          </button>
        </form>
      </section>

      {/* Section 1：回执 + hop 链 + Merkle */}
      {lookupError && (
        <div className="rounded-lg border border-amber-300 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-900/70 dark:bg-amber-950/30 dark:text-amber-300">
          回执查验暂不可用：{lookupError}
        </div>
      )}

      {receipt && (
        <SectionCard title="成本回执" icon={<ShieldCheck className="size-4 text-primary-600 dark:text-primary-300" />}>
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
            <Stat label="RequestID" value={receipt.request_id} />
            <Stat label="模型" value={receipt.cost.model} />
            <Stat label="成本" value={formatMicroUSD(receipt.cost.cost_total_micro_usd)} />
            <Stat label="发生时间" value={formatDateTime(receipt.occurred_at)} />
            <Stat label="输入 token" value={receipt.cost.input_tokens} />
            <Stat label="输出 token" value={receipt.cost.output_tokens} />
            <Stat label="缓存 token" value={receipt.cost.cached_tokens} />
            <Stat label="校验状态" value={receipt.validation_state} />
          </div>

          <div className="mt-4 space-y-2 text-sm">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-accent-500 dark:text-accent-400">回执签名：</span>
              {signedVerify ? (
                <span
                  className={
                    signedVerify.valid
                      ? 'inline-flex items-center gap-1 rounded-lg border border-emerald-300 bg-emerald-50 px-2 py-0.5 text-emerald-700 dark:border-emerald-900/70 dark:bg-emerald-950/40 dark:text-emerald-300'
                      : 'inline-flex items-center gap-1 rounded-lg border border-amber-300 bg-amber-50 px-2 py-0.5 text-amber-700 dark:border-amber-900/70 dark:bg-amber-950/40 dark:text-amber-300'
                  }
                >
                  {signedVerify.valid ? <ShieldCheck className="size-3.5" /> : <ShieldAlert className="size-3.5" />}
                  {receiptStatusLabel(signedVerify.status)}
                  {signedVerify.reason ? ` · ${signedVerify.reason}` : ''}
                </span>
              ) : (
                <span className="text-accent-400 dark:text-accent-500">无签名校验结果</span>
              )}
            </div>
            {result && (
              <ul className="space-y-1 text-accent-600 dark:text-accent-300">
                {result.details.map((detail) => <li key={detail}>● {detail}</li>)}
              </ul>
            )}
          </div>
        </SectionCard>
      )}

      {entry && proof && tree && (
        <>
          <HopChainTimeline hops={entry.hop_chain} />
          <ModelChainCard model={entry.model_chain} />
          <MerkleProofPanel proof={proof} tree={tree} />
        </>
      )}

      {!receipt && !lookupError && !loading && (
        <section className="rounded-lg border border-dashed border-accent-300 bg-white p-8 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-900/70 dark:text-accent-400">
          输入 request_id 查询单次请求的回执与信任链路。
        </section>
      )}

      {/* Section 2：我的争议 + 发起争议 */}
      <SectionCard title="我的争议" icon={<ShieldAlert className="size-4 text-primary-600 dark:text-primary-300" />}>
        <form className="flex flex-col gap-2 sm:flex-row sm:items-start" onSubmit={handleCreateDispute}>
          <div className="min-w-0 flex-1">
            <label className="sr-only" htmlFor="dispute_reason">争议理由</label>
            <textarea
              id="dispute_reason"
              className="min-h-11 w-full resize-y rounded-lg border border-accent-200 bg-accent-50 px-3 py-2 text-sm text-accent-950 outline-none ring-primary-500/20 transition focus:ring-4 dark:border-accent-800 dark:bg-accent-950/70 dark:text-accent-50"
              maxLength={4000}
              onChange={(event) => setReason(event.target.value)}
              placeholder={queryRequestId ? `对回执 ${queryRequestId} 发起争议，填写理由…` : '请先在上方查询一条回执'}
              rows={2}
              value={reason}
            />
          </div>
          <button
            className="inline-flex min-h-11 shrink-0 items-center gap-2 rounded-lg bg-primary-600 px-4 text-sm font-semibold text-white hover:bg-primary-700 disabled:cursor-not-allowed disabled:opacity-60"
            disabled={creating || !queryRequestId.trim()}
            type="submit"
          >
            {creating ? <RefreshCw className="size-4 animate-spin" /> : <ShieldAlert className="size-4" />}
            发起争议
          </button>
        </form>
        {createError && <p className="mt-2 text-sm text-red-600 dark:text-red-400">{createError}</p>}
        {createOk && <p className="mt-2 text-sm text-emerald-600 dark:text-emerald-400">{createOk}</p>}

        <div className="mt-4">
          {disputesError ? (
            <p className="text-sm text-amber-700 dark:text-amber-300">争议列表暂不可用：{disputesError}</p>
          ) : disputesLoading ? (
            <p className="text-sm text-accent-500 dark:text-accent-400">加载中…</p>
          ) : disputes.length === 0 ? (
            <p className="text-sm text-accent-500 dark:text-accent-400">暂无争议记录。</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="min-w-[720px] w-full text-left text-sm">
                <thead className="text-xs text-accent-500 dark:text-accent-400">
                  <tr className="border-b border-accent-200 dark:border-accent-800">
                    <th className="py-2 pr-4 font-medium">争议号</th>
                    <th className="py-2 pr-4 font-medium">RequestID</th>
                    <th className="py-2 pr-4 font-medium">理由</th>
                    <th className="py-2 pr-4 font-medium">状态</th>
                    <th className="py-2 pr-4 font-medium">创建时间</th>
                  </tr>
                </thead>
                <tbody>
                  {disputes.map((dispute) => (
                    <tr key={dispute.id} className="border-b border-accent-100 last:border-0 dark:border-accent-900">
                      <td className="py-2 pr-4 font-mono text-xs text-accent-800 dark:text-accent-200">{dispute.dispute_id}</td>
                      <td className="py-2 pr-4 font-mono text-xs text-accent-800 dark:text-accent-200">{dispute.request_id}</td>
                      <td className="max-w-[260px] truncate py-2 pr-4 text-accent-700 dark:text-accent-300" title={dispute.reason}>{dispute.reason}</td>
                      <td className="py-2 pr-4">{disputeStatusLabel(dispute.status)}</td>
                      <td className="py-2 pr-4 text-accent-600 dark:text-accent-400">{formatDateTime(dispute.created_at)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </SectionCard>

      {/* Section 3：公开审计公钥 + Merkle 根 */}
      <SectionCard title="公开审计签名密钥" icon={<KeyRound className="size-4 text-primary-600 dark:text-primary-300" />}>
        {pubkeyError ? (
          <p className="text-sm text-amber-700 dark:text-amber-300">公钥暂不可用：{pubkeyError}</p>
        ) : pubkey ? (
          <div className="grid gap-3 md:grid-cols-2">
            <Stat label="算法" value={pubkey.algorithm} />
            <Stat label="状态" value={pubkey.key_status ?? '未记录'} />
            <Stat label="指纹" value={pubkey.pubkey_fingerprint || pubkey.fingerprint} />
            <Stat label="生效起" value={formatDateTime(pubkey.effective_from)} />
            <div className="md:col-span-2">
              <Stat label="公钥（base64）" value={pubkey.public_key_base64} />
            </div>
            {tree && (
              <div className="md:col-span-2">
                <Stat label="当前 Merkle 根" value={tree.latest_merkle_root} />
              </div>
            )}
          </div>
        ) : (
          <p className="text-sm text-accent-500 dark:text-accent-400">加载中…</p>
        )}
      </SectionCard>
    </div>
  );
}
