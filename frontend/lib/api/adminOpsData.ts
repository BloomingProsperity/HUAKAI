// admin 运维数据面 API 封装（管理 token 轨）—— 审计事件查看 / DLQ 死信查看+重放 / 缓存 L2 检视+清除。
// 走 client.ts 的 apiGet/apiPost + 本模块内自带的 adminDelete（client.ts 未导出 DELETE 助手且不可改它，
// 故复用同一「localStorage huakai_admin_token → Bearer」约定 + 复用 client.ts 导出的 ApiError，使
// errors.ts friendlyMessage 仍可统一翻译）。沿 lib/api/adminUsers.ts / adminSystem.ts 同一约定。
//
// 端点全部读后端真码确认（understand workflow 实读 gatewayhttp admin_*_handler）：
//   GET    /admin/v1/audit-events?tenant_id&from&to&event_class&event_type&severity&ledger_id&actor_id&limit&cursor
//          NewAuditEventsHandler → {items,next_cursor,total}（keyset 游标分页）
//   GET    /admin/v1/dlq/{handler}?limit&status                NewAdminDLQListHandler   → {items}
//   POST   /admin/v1/dlq/{id}/replay                           NewAdminDLQReplayHandler → {item,replayed}
//   POST   /admin/v1/usage-record-dlq/{id}/replay             （同一 handler 别名）     → {item,replayed}
//   GET    /admin/v1/cache/l2/stats                            newAdminL2StatsHandler   → L2CacheStats
//   DELETE /admin/v1/cache/l2/{key}                            newAdminL2DeleteHandler  → {key,deleted}
//
// 鉴权（understand workflow 实读 handler）：审计/缓存=platform_admin 或 tenant_operator(限自身 scope)；
// **DLQ=platform_admin 限定**（不按租户过滤，跨租户可见；tenant_operator 403）。DLQ replay 后端不幂等且无客户端
// X-Request-Id → 前端不造幂等键（页面用二次确认 + 重放中禁连点兜一层）。
//
// 借鉴（CLEAN-ROOM，§11/§12/§16，仅功能/字段/动作形态，未抄码）：sub2api indexed system-logs 富过滤（审计 tiebreaker）、
// 无 DLQ replay、无 cache purge；new-api cache admin 最强（stats/全清/GC）、无 DLQ；CLIProxyAPI 无审计无 DLQ。
// HUAKAI delta：DLQ 死信查看+逐条 replay（两家皆无,生态）、keyset 游标审计分页（架构）、按 key 选择性清缓存（算法）。

import { ApiError, apiGet, apiPost } from './client';
import type { APIError } from './types';
import { buildAuditEventsQuery, encodeCacheKey, type AuditEventsQueryInput } from './ops-data-form';

// ---- 共享：管理 token + DELETE（client.ts 未提供 DELETE，且不可改它）----

function adminToken(): string {
  if (typeof window === 'undefined') return '';
  return localStorage.getItem('huakai_admin_token') ?? '';
}

function adminHeaders(): Record<string, string> {
  const token = adminToken();
  return {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  };
}

async function parse<T>(resp: Response): Promise<T> {
  if (resp.ok) {
    if (resp.status === 204) return undefined as T;
    return (await resp.json()) as T;
  }
  let payload: APIError;
  try {
    payload = (await resp.json()) as APIError;
  } catch {
    throw new Error(`HTTP ${resp.status}`);
  }
  throw new ApiError(resp.status, payload);
}

async function adminDelete<T>(path: string): Promise<T> {
  const resp = await fetch(path, { method: 'DELETE', headers: adminHeaders() });
  return parse<T>(resp);
}

// ---- 类型 ----

// 对齐 gatewayhttp 审计事件 DTO（mapAuditRow）。payload 各事件类结构不同，前端按 object 透传展示。
export interface AuditEvent {
  id: number;
  tenant_id: number;
  event_class: string; // billing | pool_routing | rate_limit | oauth_refresh
  event_type: string;
  severity: string; // info | warning | error
  ledger_id: string;
  claim_id?: number | null;
  provider_account_id?: number | null;
  pool_group_id?: number | null;
  request_id?: string | null;
  actor_id?: string | null;
  actor_role?: string | null;
  reason?: string | null;
  payload: Record<string, unknown>;
  created_at: string; // RFC3339Nano
}

export interface AuditEventListResponse {
  items: AuditEvent[];
  next_cursor: string | null; // keyset 游标；null=无更多
  total: number;
}

// 对齐 dlq.Record（mapDLQRecord）。
export interface DLQRecord {
  id: number;
  tenant_id: number;
  claim_id?: number | null;
  event_kind: string;
  lane: string; // HIGH | MED | LOW
  status: string; // pending | inflight | delivered | operator_review | dlq | quarantined
  payload: Record<string, unknown>;
  failure_reason: string;
  failure_at: string;
  replay_attempts: number;
  last_replay_at?: string | null;
  replayed_at?: string | null;
  replay_failure_reason?: string | null;
  next_retry_at: string;
  lease_owner?: string | null;
  lease_until?: string | null;
  replica_status: string;
  replica_target: string;
  replica_committed_at?: string | null;
  idempotency_key: string;
  source_table: string;
  source_id?: number | null;
  operator_review_at?: string | null;
}

export interface DLQListResponse {
  items: DLQRecord[];
}

export interface DLQReplayResponse {
  item: DLQRecord;
  replayed: boolean;
}

// 对齐 cache.EntryStats。
export interface L2EntryStats {
  key: string;
  tenant_id: number;
  vendor: string;
  model: string;
  status: number;
  size_bytes: number;
  stored_at: string;
  expires_at: string;
}

// 对齐 cachemetrics.L2SnapshotRow（按 "vendor=V,model=M" 标签）。
export interface L2Metric {
  hit_total: number;
  miss_total: number;
  size_bytes: number;
}

export interface L2CacheStats {
  enabled: boolean;
  size_bytes: number;
  max_size_bytes: number;
  ttl_seconds: number;
  entries: L2EntryStats[];
  metrics: Record<string, L2Metric>; // tenant_operator 抹空 {}
}

export interface L2DeleteResponse {
  key: string;
  deleted: boolean;
}

// ---- 审计事件 ----

// listAuditEvents — GET /admin/v1/audit-events。keyset 游标分页：next_cursor 作下一页 cursor 传入。
export function listAuditEvents(input: AuditEventsQueryInput): Promise<AuditEventListResponse> {
  return apiGet<AuditEventListResponse>('/admin/v1/audit-events', buildAuditEventsQuery(input));
}

// ---- DLQ 死信队列 ----

// listDLQ — GET /admin/v1/dlq/{handler}（handler=EventKind 枚举；调用前应 isValidEventKind 守门）。platform_admin 限定。
export function listDLQ(handler: string, opts?: { limit?: number; status?: string }): Promise<DLQListResponse> {
  return apiGet<DLQListResponse>(`/admin/v1/dlq/${handler}`, {
    limit: opts?.limit,
    status: opts?.status && opts.status !== 'all' ? opts.status : undefined,
  });
}

// replayDLQ — POST /admin/v1/dlq/{id}/replay。不幂等；404 dlq_not_found / 409 dlq_handler_missing / 503 dlq_replay_failed。
export function replayDLQ(id: number): Promise<DLQReplayResponse> {
  return apiPost<DLQReplayResponse>(`/admin/v1/dlq/${id}/replay`);
}

// replayUsageRecordDLQ — POST /admin/v1/usage-record-dlq/{id}/replay（同一 handler 别名）。
export function replayUsageRecordDLQ(id: number): Promise<DLQReplayResponse> {
  return apiPost<DLQReplayResponse>(`/admin/v1/usage-record-dlq/${id}/replay`);
}

// ---- 缓存 L2 ----

// getL2CacheStats — GET /admin/v1/cache/l2/stats（无参；tenant_operator 仅见本租户 entries，metrics 抹空）。
export function getL2CacheStats(): Promise<L2CacheStats> {
  return apiGet<L2CacheStats>('/admin/v1/cache/l2/stats');
}

// evictL2CacheKey — DELETE /admin/v1/cache/l2/{key}。key 须 URL 编码（可含 '/'、':'）；404 cache_l2_not_found。
export function evictL2CacheKey(key: string): Promise<L2DeleteResponse> {
  return adminDelete<L2DeleteResponse>(`/admin/v1/cache/l2/${encodeCacheKey(key)}`);
}
