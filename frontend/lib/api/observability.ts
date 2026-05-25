// 观测接口：
//   getDebugVars — GET /debug/vars (expvar — REAL)
//   listUsageRecords — GET /admin/v1/usage (REAL)
//   listBillingClaims — GET /admin/v1/billing/claims (REAL)

import { apiGet } from './client';
import type { BillingLedgerClaimList, UsageRecordList } from './types';

// getDebugVars：返回原始 expvar JSON 对象（结构动态）
export async function getDebugVars(): Promise<Record<string, unknown>> {
  const resp = await fetch('/debug/vars', { cache: 'no-store' });
  if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
  return resp.json() as Promise<Record<string, unknown>>;
}

// listUsageRecords — GET /admin/v1/usage
export function listUsageRecords(opts?: {
  cursor?: string;
  limit?: number;
  from?: string;
  to?: string;
  api_key_id?: number;
  provider_account_id?: number;
  model?: string;
  pending_reconciliation_only?: boolean;
}): Promise<UsageRecordList> {
  return apiGet<UsageRecordList>('/admin/v1/usage', {
    cursor: opts?.cursor,
    limit: opts?.limit,
    from: opts?.from,
    to: opts?.to,
    api_key_id: opts?.api_key_id,
    provider_account_id: opts?.provider_account_id,
    model: opts?.model,
    pending_reconciliation_only: opts?.pending_reconciliation_only,
  });
}

// listBillingClaims — GET /admin/v1/billing/claims
export function listBillingClaims(opts?: {
  cursor?: string;
  limit?: number;
  status?: 'reserving' | 'committed' | 'aborted';
  from?: string;
}): Promise<BillingLedgerClaimList> {
  return apiGet<BillingLedgerClaimList>('/admin/v1/billing/claims', {
    cursor: opts?.cursor,
    limit: opts?.limit,
    status: opts?.status,
    from: opts?.from,
  });
}
