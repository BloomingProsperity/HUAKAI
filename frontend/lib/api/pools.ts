// Pool Group CRUD — 对应 OpenAPI operationId:
//   listPoolGroups / createPoolGroup / getPoolGroup / updatePoolGroup
// 所有调用均为 REAL（call backend）

import { apiGet, apiPatch, apiPost } from './client';
import type {
  PoolGroup,
  PoolGroupCreate,
  PoolGroupList,
  PoolGroupUpdate,
} from './types';

const BASE = '/admin/v1/pools';

// listPoolGroups — GET /admin/v1/pools
export function listPoolGroups(opts?: { cursor?: string; limit?: number }): Promise<PoolGroupList> {
  return apiGet<PoolGroupList>(BASE, { cursor: opts?.cursor, limit: opts?.limit });
}

// createPoolGroup — POST /admin/v1/pools
export function createPoolGroup(body: PoolGroupCreate): Promise<PoolGroup> {
  return apiPost<PoolGroup>(BASE, body);
}

// getPoolGroup — GET /admin/v1/pools/{id}
export function getPoolGroup(id: number): Promise<PoolGroup> {
  return apiGet<PoolGroup>(`${BASE}/${id}`);
}

// updatePoolGroup — PATCH /admin/v1/pools/{id}
export function updatePoolGroup(id: number, body: PoolGroupUpdate): Promise<PoolGroup> {
  return apiPatch<PoolGroup>(`${BASE}/${id}`, body);
}
