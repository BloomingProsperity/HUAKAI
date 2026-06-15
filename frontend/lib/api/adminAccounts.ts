// Admin 账号池控制台数据层 —— 走 admin token 轨(client.ts 从 localStorage
// 'huakai_admin_token' 取 Bearer);不要用 userClient(那是 session 用户面)。
//
// 后端 handler 真码来源(已读确认形状,避免 DisallowUnknownFields 400):
//   - backend/internal/gatewayhttp/admin_pool_accounts_handler.go
//       GET/POST/PATCH /admin/v1/provider-accounts(列表/详情/更新/启停/清 RL)
//   - backend/internal/adminhttp/provider_account_health_handler.go
//       GET /admin/v1/provider-accounts/{id}/health(健康快照)
//   - backend/internal/adminhttp/provider_account_test_handler.go
//       POST /admin/v1/provider-accounts/{id}/test(测试连通)
//   - backend/internal/gatewayhttp/admin_pools_handler.go
//       GET /admin/v1/pools(池组列表,用于按池分组过滤)
//   挂载点: backend/cmd/gateway/routes.go mountProviderAccountAdminRoutes()
//
// 字段/列/动作借鉴(CLEAN-ROOM,只取功能不抄码):
//   - sub2api frontend/src/views/admin/AccountsView.vue:列布局(name/平台类型/
//     容量/状态/可调度)、行内动作(test/启停/clear-rate-limit)
//   - sub2api frontend/src/api/admin/accounts.ts:GET /admin/accounts、
//     POST .../{id}/test、POST .../{id}/clear-rate-limit 端点划分
//   均为功能映射,无代码复制。

import { apiGet, apiPatch, apiPost } from './client';
import type {
  PoolGroup,
  PoolGroupList,
  ProviderAccount,
  ProviderAccountList,
  ProviderAccountUpdate,
} from './types';

const ACCOUNTS_BASE = '/admin/v1/provider-accounts';
const POOLS_BASE = '/admin/v1/pools';

// ---- 列表/详情 ----

// listAdminProviderAccounts — GET /admin/v1/provider-accounts
// 后端 newListProviderAccountsHandler:支持 limit / cursor / pool_group_id /
// state_filter / tag,返回 { items, page }。
export function listAdminProviderAccounts(opts?: {
  poolGroupId?: number;
  stateFilter?: ProviderAccountStateFilter;
  tag?: string;
  cursor?: string;
  limit?: number;
}): Promise<ProviderAccountList> {
  return apiGet<ProviderAccountList>(ACCOUNTS_BASE, {
    pool_group_id: opts?.poolGroupId,
    state_filter: opts?.stateFilter,
    tag: opts?.tag,
    cursor: opts?.cursor,
    limit: opts?.limit,
  });
}

// getAdminProviderAccount — GET /admin/v1/provider-accounts/{id}
export function getAdminProviderAccount(id: number): Promise<ProviderAccount> {
  return apiGet<ProviderAccount>(`${ACCOUNTS_BASE}/${id}`);
}

// state_filter 取值与后端 parseProviderAccountStateFilter 一一对应(空串=全部)。
export type ProviderAccountStateFilter =
  | ''
  | 'active'
  | 'error'
  | 'disabled'
  | 'rate_limited'
  | 'overloaded'
  | 'temp_unschedulable';

// ---- 启停 ----

// setAdminProviderAccountEnabled — PATCH /admin/v1/provider-accounts/{id}/enabled
// 后端 newUpdateProviderAccountEnabledHandler:body 必须含 enabled,响应
// { id, enabled }。reason 可选(写进 audit)。
export interface ProviderAccountEnabledResult {
  id: number;
  enabled: boolean;
}

export function setAdminProviderAccountEnabled(
  id: number,
  enabled: boolean,
  reason?: string,
): Promise<ProviderAccountEnabledResult> {
  return apiPatch<ProviderAccountEnabledResult>(`${ACCOUNTS_BASE}/${id}/enabled`, {
    enabled,
    ...(reason ? { reason } : {}),
  });
}

// updateAdminProviderAccount — PATCH /admin/v1/provider-accounts/{id}
// 后端 newUpdateProviderAccountHandler:部分字段更新,返回完整 account DTO。
export function updateAdminProviderAccount(
  id: number,
  body: ProviderAccountUpdate,
): Promise<ProviderAccount> {
  return apiPatch<ProviderAccount>(`${ACCOUNTS_BASE}/${id}`, body);
}

// ---- 清除 rate limit ----

// clearAdminProviderAccountRateLimit — POST /admin/v1/provider-accounts/{id}/clear-rate-limit
// 后端 newClearProviderAccountRateLimitHandler:返回解除 bench 后的完整
// account DTO(200,不是 204),便于 UI 直接刷新该行。
export function clearAdminProviderAccountRateLimit(id: number): Promise<ProviderAccount> {
  return apiPost<ProviderAccount>(`${ACCOUNTS_BASE}/${id}/clear-rate-limit`);
}

// ---- 健康快照 ----

// providerAccountHealthResponseBody 真码字段(adminhttp/provider_account_health_handler.go):
// 比 types.ts 的 ProviderAccountHealthSnapshot 多 last_probe_* / model_sync /
// session_window_5h / recent_requests,故本模块单独声明全量形状。
export interface ProviderAccountRecentRequests {
  total: number;
  success: number;
  failure: number;
  last_at?: string;
}

export interface ProviderAccountHealthDetail {
  id: number;
  health_state: string;
  health_state_until?: string;
  last_probe_latency_ms: number | null;
  last_probe_at: string | null;
  model_sync_last_check_at: string | null;
  session_window_5h_start: string | null;
  session_window_5h_end: string | null;
  session_window_5h_status: string | null;
  last_refresh_at: string | null;
  last_refresh_outcome: string | null;
  failure_class: string | null;
  failure_count: number;
  enabled: boolean;
  requires_action: boolean;
  updated_at: string;
  recent_requests?: ProviderAccountRecentRequests;
}

// getAdminProviderAccountHealth — GET /admin/v1/provider-accounts/{id}/health
export function getAdminProviderAccountHealth(id: number): Promise<ProviderAccountHealthDetail> {
  return apiGet<ProviderAccountHealthDetail>(`${ACCOUNTS_BASE}/${id}/health`);
}

// ---- 测试连通 ----

// providerAccountTestResponseBody 真码字段(adminhttp/provider_account_test_handler.go):
// { ok, error_class, message }。dry-run 凭据验证,不计费、不污染调度。
export interface ProviderAccountTestResult {
  ok: boolean;
  error_class: string | null;
  message: string;
}

// testAdminProviderAccount — POST /admin/v1/provider-accounts/{id}/test
export function testAdminProviderAccount(id: number): Promise<ProviderAccountTestResult> {
  return apiPost<ProviderAccountTestResult>(`${ACCOUNTS_BASE}/${id}/test`);
}

// ---- 池组(用于按池过滤 + 容量上下文)----

// listAdminPoolGroups — GET /admin/v1/pools
// 后端 newListPoolsHandler 返回 { items: PoolGroup[] }(无 page 包裹)。
export interface AdminPoolGroupListResponse {
  items: PoolGroup[];
}

export function listAdminPoolGroups(opts?: { limit?: number }): Promise<AdminPoolGroupListResponse> {
  return apiGet<AdminPoolGroupListResponse>(POOLS_BASE, { limit: opts?.limit });
}

// 重导出页面会用到的共享类型,避免页面直接耦合 types.ts 路径。
export type { PoolGroup, PoolGroupList, ProviderAccount, ProviderAccountList, ProviderAccountUpdate };
