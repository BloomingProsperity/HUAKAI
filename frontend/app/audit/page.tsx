'use client';

import { FormEvent, Suspense, useCallback, useEffect, useMemo, useState } from 'react';
import { RefreshCw, Search, ShieldCheck } from 'lucide-react';
import { useRouter, useSearchParams } from 'next/navigation';
import { HopChainTimeline } from '@/components/audit/HopChainTimeline';
import { MerkleProofPanel } from '@/components/audit/MerkleProofPanel';
import { ModelChainCard } from '@/components/audit/ModelChainCard';
import { VerifyStatusBadge } from '@/components/audit/VerifyStatusBadge';
import {
  fetchAuditBundle,
  verifyAuditProofInBrowser,
  type AuditBundle,
  type AuditVerifyResult,
} from '@/lib/audit-api';
import { getMockAuditBundle } from '@/lib/audit-mock';

const EMPTY_RESULT: AuditVerifyResult = {
  status: 'partial',
  details: ['请输入 request_id 后查询。'],
};
const MOCK_RESULT: AuditVerifyResult = {
  status: 'partial',
  details: ['当前为本地 mock 数据，未执行真实 ed25519 验签。'],
};

function formatDateTime(value: string) {
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
  const initialMockBundle = queryRequestId.startsWith('mock') ? getMockAuditBundle(queryRequestId) : null;
  const [requestId, setRequestId] = useState(queryRequestId);
  const [bundle, setBundle] = useState<AuditBundle | null>(initialMockBundle);
  const [result, setResult] = useState<AuditVerifyResult>(initialMockBundle ? MOCK_RESULT : EMPTY_RESULT);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const loadAudit = useCallback(async (rawRequestId: string) => {
    const nextRequestId = rawRequestId.trim();
    if (!nextRequestId) {
      setBundle(null);
      setResult(EMPTY_RESULT);
      setError('');
      return;
    }
    setLoading(true);
    setError('');
    try {
      const nextBundle = nextRequestId.startsWith('mock')
        ? getMockAuditBundle(nextRequestId)
        : await fetchAuditBundle(nextRequestId);
      const nextResult = await verifyAuditProofInBrowser(nextBundle);
      setBundle(nextBundle);
      setResult(nextResult);
    } catch (err) {
      setBundle(null);
      setResult({ status: 'partial', details: ['后端 audit endpoint 暂不可用。'] });
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    setRequestId(queryRequestId);
    void loadAudit(queryRequestId);
  }, [loadAudit, queryRequestId]);

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const nextRequestId = requestId.trim();
    router.replace(nextRequestId ? `/audit?request_id=${encodeURIComponent(nextRequestId)}` : '/audit');
  }

  const entry = bundle?.verify.ledger_entry;
  const proof = bundle?.verify.chain_proof;
  const tree = bundle?.tree;
  const sourceLabel = bundle?.source === 'mock' ? 'MOCK' : 'REAL';
  const endpointList = useMemo(() => [
    'GET /v1/audit/verify?request_id=...',
    'GET /v1/audit/merkle-tree.json',
    'GET /.well-known/huakai-pubkey.json',
  ], []);

  return (
    <div className="space-y-6">
      <section className="rounded-lg border border-accent-200 bg-white p-5 shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <div className="flex flex-col justify-between gap-4 xl:flex-row xl:items-start">
          <div className="min-w-0">
            <p className="text-xs font-medium text-primary-700 dark:text-primary-300">T6 信任链</p>
            <h1 className="mt-1 text-2xl font-bold tracking-normal text-accent-950 dark:text-white">我的审计 / 我的消费链路</h1>
            <p className="mt-2 max-w-3xl text-sm leading-6 text-accent-500 dark:text-accent-400">
              输入 request_id 后读取 LedgerEntry、六跳链路、模型三方比对、Merkle 链 proof，并在浏览器内尝试校验签名。
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-3">
            <VerifyStatusBadge status={result.status} />
            {bundle && (
              <span className="inline-flex min-h-9 items-center rounded-lg border border-accent-200 bg-accent-50 px-3 text-sm font-medium text-accent-700 dark:border-accent-800 dark:bg-accent-950/70 dark:text-accent-200">
                数据源 {sourceLabel}
              </span>
            )}
          </div>
        </div>

        <form className="mt-5 flex flex-col gap-3 md:flex-row" onSubmit={handleSubmit}>
          <label className="sr-only" htmlFor="request_id">request_id</label>
          <input
            id="request_id"
            className="min-h-11 flex-1 rounded-lg border border-accent-200 bg-accent-50 px-3 font-mono text-sm text-accent-950 outline-none ring-primary-500/20 transition focus:ring-4 dark:border-accent-800 dark:bg-accent-950/70 dark:text-accent-50"
            onChange={(event) => setRequestId(event.target.value)}
            placeholder="req_xxx 或 mock1"
            value={requestId}
          />
          <div className="flex gap-2">
            <button className="inline-flex min-h-11 items-center gap-2 rounded-lg bg-primary-600 px-4 text-sm font-semibold text-white hover:bg-primary-700 disabled:cursor-wait disabled:opacity-70" disabled={loading} type="submit">
              {loading ? <RefreshCw className="size-4 animate-spin" /> : <Search className="size-4" />}
              查询
            </button>
            <button className="inline-flex min-h-11 items-center rounded-lg border border-accent-200 bg-white px-4 text-sm font-semibold text-accent-700 hover:bg-accent-50 dark:border-accent-800 dark:bg-accent-950 dark:text-accent-100 dark:hover:bg-accent-900" onClick={() => router.replace('/audit?request_id=mock1')} type="button">
              载入 mock1
            </button>
          </div>
        </form>

        <div className="mt-4 flex flex-col gap-2 text-xs text-accent-500 dark:text-accent-400 md:flex-row md:flex-wrap">
          {endpointList.map((endpoint) => <code key={endpoint} className="rounded bg-accent-100 px-2 py-1 dark:bg-accent-950">{endpoint}</code>)}
        </div>
      </section>

      {error && (
        <div className="rounded-lg border border-amber-300 bg-amber-50 p-3 text-sm text-amber-800 dark:border-amber-900/70 dark:bg-amber-950/30 dark:text-amber-300">
          {error}。开发环境可使用 request_id=mock1 查看完整页面。
        </div>
      )}

      {entry && proof && tree ? (
        <>
          <section className="grid gap-3 md:grid-cols-2 xl:grid-cols-4" aria-label="LedgerEntry 基础信息">
            <Stat label="RequestID" value={entry.request_id} />
            <Stat label="LedgerID" value={entry.ledger_id} />
            <Stat label="TenantID" value={entry.tenant_id} />
            <Stat label="Timestamp" value={formatDateTime(entry.timestamp)} />
          </section>

          <section className="rounded-lg border border-accent-200 bg-white p-5 shadow-card dark:border-accent-800 dark:bg-accent-900/70">
            <h2 className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
              <ShieldCheck className="size-4 text-primary-600 dark:text-primary-300" />
              签名 verify 状态
            </h2>
            <ul className="mt-3 space-y-2 text-sm text-accent-600 dark:text-accent-300">
              {result.details.map((detail) => <li key={detail}>● {detail}</li>)}
            </ul>
          </section>

          <HopChainTimeline hops={entry.hop_chain} />
          <ModelChainCard model={entry.model_chain} />
          <MerkleProofPanel proof={proof} tree={tree} />
        </>
      ) : (
        <section className="rounded-lg border border-dashed border-accent-300 bg-white p-8 text-center text-sm text-accent-500 dark:border-accent-800 dark:bg-accent-900/70 dark:text-accent-400">
          输入 request_id 查询真实 ledger；无后端时使用 mock1。
        </section>
      )}
    </div>
  );
}
