// Provider Account CRUD — 对应 OpenAPI operationId:
//   listProviderAccounts / createProviderAccount / getProviderAccount /
//   updateProviderAccount / clearProviderAccountRateLimit
// 所有调用均为 REAL（call backend）

import { apiGet, apiPatch, apiPost, apiPostNoContent } from './client';
import type {
  ProviderAccount,
  ProviderAccountCreate,
  ProviderAccountList,
  ProviderAccountUpdate,
} from './types';

const BASE = '/admin/v1/provider-accounts';

// listProviderAccounts — GET /admin/v1/provider-accounts
export function listProviderAccounts(opts?: {
  pool_group_id?: number;
  state_filter?: string;
  cursor?: string;
  limit?: number;
}): Promise<ProviderAccountList> {
  return apiGet<ProviderAccountList>(BASE, {
    pool_group_id: opts?.pool_group_id,
    state_filter: opts?.state_filter,
    cursor: opts?.cursor,
    limit: opts?.limit,
  });
}

// createProviderAccount — POST /admin/v1/provider-accounts
export function createProviderAccount(body: ProviderAccountCreate): Promise<ProviderAccount> {
  return apiPost<ProviderAccount>(BASE, body);
}

// getProviderAccount — GET /admin/v1/provider-accounts/{id}
export function getProviderAccount(id: number): Promise<ProviderAccount> {
  return apiGet<ProviderAccount>(`${BASE}/${id}`);
}

// updateProviderAccount — PATCH /admin/v1/provider-accounts/{id}
export function updateProviderAccount(id: number, body: ProviderAccountUpdate): Promise<ProviderAccount> {
  return apiPatch<ProviderAccount>(`${BASE}/${id}`, body);
}

// clearProviderAccountRateLimit — POST /admin/v1/provider-accounts/{id}/clear-rate-limit
// 响应 204 No Content
export function clearProviderAccountRateLimit(id: number): Promise<void> {
  return apiPostNoContent(`${BASE}/${id}/clear-rate-limit`);
}
