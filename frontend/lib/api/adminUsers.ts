// admin 用户管理 API 封装（管理 token 轨，走 client.ts 的 apiGet/apiPost + 本模块内
// 的 adminPut/adminDelete —— client.ts 未导出 PUT/DELETE 助手，且按硬约束不可改动它，
// 故在本模块内复用同一「localStorage huakai_admin_token → Bearer」约定 + 复用 client.ts
// 导出的 ApiError，使 errors.ts friendlyMessage 仍可统一翻译）。
//
// 端点形状全部以 HUAKAI 后端真码为准（读 handler 确认，逐条标注）：
//   GET    /admin/v1/users                         adminuserhttp.newListHandler
//   GET    /admin/v1/users/{id}                     adminuserhttp.newGetHandler
//   GET    /admin/v1/users/{id}/balance-history     adminuserhttp.newBalanceHistoryHandler
//   GET    /admin/v1/users/2fa-adoption-stats       adminuserhttp.newTwoFAStatsHandler
//   PUT    /admin/v1/users/{id}/status              adminuserhttp.newSetUserStatusHandler
//   PUT    /admin/v1/users/{id}/remark              adminuserhttp.newSetUserRemarkHandler
//   PUT    /admin/v1/users/{id}/group               adminuserhttp.newSetUserGroupHandler
//   POST   /admin/v1/users/{id}/unlock              adminuserhttp.newUnlockHandler
//   POST   /admin/v1/users                          adminuserhttp.newCreateUserHandler
//   POST   /admin/v1/balances/adjustments           adminhttp.newBalanceCreditHandler（充值入账，platform_admin 限定）
//
// 鉴权：管理 token。tenant_operator 可省 ?tenant_id（用自身 scope）；platform_admin 必带
// ?tenant_id（resolveTenantIdentity：tenantFromQueryOrScope）。单租户部署默认 tenant=1。
//
// 借鉴（CLEAN-ROOM，CLAUDE.md §11/§12，仅提取功能/字段/动作形态，未抄码）：
//   - sub2api(LGPL) src/api/admin/users.ts + views/admin/UsersView.vue：列表「page+search+status 过滤」
//     形态、行动作集合（查看/启停/余额历史/调余额）、余额历史「类型+金额+时间」列形态、
//     余额调整弹窗「金额 + 备注」形态。注:sub2api updateBalance 走 set-new-balance 语义
//     （POST /admin/users/{id}/balance {balance,notes}）；HUAKAI 后端没有 set/subtract，
//     只有「增量入账」(POST /admin/v1/balances/adjustments {amount>0,reason,idempotency_key})，
//     debit 后端 gated（admin_debit_not_yet_supported）—— 故本面只做 add credit，不照搬 set/subtract。
//   - new-api(AGPL) 用户管理页：状态徽章 + 启停 + 备注/分组编辑的运营形态。
//   字段集合完全对齐 HUAKAI 后端 userBody / balanceHistoryBody / balanceCreditRequestBody，不照搬上游字段名。

import { ApiError, apiGet, apiPost } from './client';
import type { APIError } from './types';

// ---- 共享：管理 token 取用 + PUT/DELETE（client.ts 未提供这两个动词，且不可改它）----

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

async function adminPut<T>(path: string, body: unknown): Promise<T> {
  const resp = await fetch(path, { method: 'PUT', headers: adminHeaders(), body: JSON.stringify(body) });
  return parse<T>(resp);
}

// ---- 类型（对齐后端 JSON 形态）----

// adminuserhttp.userBody（routes.go）。balance 是 numeric(20,8) 文本，前端不做精度运算。
export interface AdminUser {
  id: number;
  email: string;
  role: string;
  status: string; // active / disabled / locked …
  user_group: string;
  remark: string;
  balance: string; // 文本十进制，例 "12.34000000"
  created_at: string; // RFC3339
}

// newListHandler 响应体：{ items, limit, offset }。
export interface AdminUserListResponse {
  items: AdminUser[];
  limit: number;
  offset: number;
}

// adminuserhttp.balanceHistoryBody（routes.go）。
export interface BalanceHistoryEntry {
  id: number;
  event_type: string;
  amount: string; // 文本十进制（可正可负）
  fingerprint: string;
  source_type: string;
  source_id: number;
  occurred_at: string; // RFC3339
}

export interface BalanceHistoryResponse {
  items: BalanceHistoryEntry[];
  limit: number;
  offset: number;
}

// adminuserhttp.twoFAStatsBody（routes.go）。
export interface TwoFAStats {
  enabled_users: number;
  total_users: number;
  enabled_rate: number; // 0..1
}

// PUT /status 响应：{ id, status }。
export interface SetStatusResult {
  id: number;
  status: string;
}

// PUT /remark 响应：{ id, remark }。
export interface SetRemarkResult {
  id: number;
  remark: string;
}

// PUT /group 响应：{ id, user_group }。
export interface SetGroupResult {
  id: number;
  user_group: string;
}

// POST /unlock 响应：unlockUserBody{ id, status }。
export interface UnlockResult {
  id: number;
  status: string;
}

// adminhttp.balanceCreditResponseBody（balance_credit_handler.go）。
export interface BalanceAdjustResult {
  tenant_id: number;
  user_id: number;
  net_balance: string; // StringFixed(8)
  currency_code: string;
  recharge_order_id?: number;
}

// ---- 列表 / 详情 ----

// listUsers — GET /admin/v1/users?q&limit&offset&tenant_id
// q 走后端 AdminListUsersForTenantParams.Query（email 模糊）。limit 后端封顶 100，缺省 50。
// tenant_id：platform_admin 必带；tenant_operator 省略则用自身 scope。
export function listUsers(opts?: {
  q?: string;
  limit?: number;
  offset?: number;
  tenant_id?: number;
}): Promise<AdminUserListResponse> {
  return apiGet<AdminUserListResponse>('/admin/v1/users', {
    q: opts?.q && opts.q.trim() !== '' ? opts.q.trim() : undefined,
    limit: opts?.limit,
    offset: opts?.offset,
    tenant_id: opts?.tenant_id,
  });
}

// getUser — GET /admin/v1/users/{id}?tenant_id
export function getUser(id: number, tenantId?: number): Promise<AdminUser> {
  return apiGet<AdminUser>(`/admin/v1/users/${id}`, { tenant_id: tenantId });
}

// getBalanceHistory — GET /admin/v1/users/{id}/balance-history?limit&offset&tenant_id
export function getBalanceHistory(
  id: number,
  opts?: { limit?: number; offset?: number; tenant_id?: number },
): Promise<BalanceHistoryResponse> {
  return apiGet<BalanceHistoryResponse>(`/admin/v1/users/${id}/balance-history`, {
    limit: opts?.limit,
    offset: opts?.offset,
    tenant_id: opts?.tenant_id,
  });
}

// getTwoFAStats — GET /admin/v1/users/2fa-adoption-stats?tenant_id
export function getTwoFAStats(tenantId?: number): Promise<TwoFAStats> {
  return apiGet<TwoFAStats>('/admin/v1/users/2fa-adoption-stats', { tenant_id: tenantId });
}

// ---- 行操作 ----

// setStatus — PUT /admin/v1/users/{id}/status  body{status, reason?}
// 后端 DisallowUnknownFields，只接受 status ∈ {active,disabled} + 可选 reason；其余字段 400。
// tenant_id 经 query 传（platform_admin 必带）。
export function setStatus(
  id: number,
  status: 'active' | 'disabled',
  opts?: { reason?: string; tenant_id?: number },
): Promise<SetStatusResult> {
  const qs = opts?.tenant_id != null ? `?tenant_id=${opts.tenant_id}` : '';
  const body: { status: string; reason?: string } = { status };
  if (opts?.reason && opts.reason.trim() !== '') body.reason = opts.reason.trim();
  return adminPut<SetStatusResult>(`/admin/v1/users/${id}/status${qs}`, body);
}

// setRemark — PUT /admin/v1/users/{id}/remark  body{remark}（<=1024 字，可空串清除）
export function setRemark(id: number, remark: string, tenantId?: number): Promise<SetRemarkResult> {
  const qs = tenantId != null ? `?tenant_id=${tenantId}` : '';
  return adminPut<SetRemarkResult>(`/admin/v1/users/${id}/remark${qs}`, { remark });
}

// setGroup — PUT /admin/v1/users/{id}/group  body{group}（1..64 字，路由分组权益）
export function setGroup(id: number, group: string, tenantId?: number): Promise<SetGroupResult> {
  const qs = tenantId != null ? `?tenant_id=${tenantId}` : '';
  return adminPut<SetGroupResult>(`/admin/v1/users/${id}/group${qs}`, { group });
}

// unlockUser — POST /admin/v1/users/{id}/unlock（清除登录失败锁定）
export function unlockUser(id: number, tenantId?: number): Promise<UnlockResult> {
  const path = tenantId != null ? `/admin/v1/users/${id}/unlock?tenant_id=${tenantId}` : `/admin/v1/users/${id}/unlock`;
  return apiPost<UnlockResult>(path);
}

// ---- 余额调整（充值入账）----
// POST /admin/v1/balances/adjustments —— 注意:这是独立的 adminhttp 端点（不在 /admin/v1/users 下），
// 要求 platform_admin 角色，且 body 必须显式带 tenant_id + user_id（不取自路径/会话）。
// amount 为带符号十进制文本；后端要求非零;debit(负数)当前 gated（admin_debit_not_yet_supported）→
// 本面只暴露「增加余额」(正数 credit)。reason + idempotency_key 必填（幂等去重 + 审计）。
export function adjustBalance(input: {
  tenant_id: number;
  user_id: number;
  amount: string; // 正数文本，例 "10.00"
  reason: string;
  idempotency_key: string;
  currency_code?: string;
}): Promise<BalanceAdjustResult> {
  const body: Record<string, unknown> = {
    tenant_id: input.tenant_id,
    user_id: input.user_id,
    amount: input.amount,
    reason: input.reason,
    idempotency_key: input.idempotency_key,
  };
  if (input.currency_code && input.currency_code.trim() !== '') body.currency_code = input.currency_code.trim();
  return apiPost<BalanceAdjustResult>('/admin/v1/balances/adjustments', body);
}

// ---- 展示辅助 ----

// status -> 中文标签。
export function statusLabel(status: string): string {
  switch (status) {
    case 'active':
      return '正常';
    case 'disabled':
      return '已停用';
    case 'locked':
      return '已锁定';
    case 'deleted':
      return '已删除';
    default:
      return status || '未知';
  }
}

// status -> Badge variant。
export function statusBadgeVariant(status: string): 'default' | 'secondary' | 'destructive' | 'outline' {
  if (status === 'active') return 'default';
  if (status === 'disabled' || status === 'deleted') return 'destructive';
  if (status === 'locked') return 'secondary';
  return 'outline';
}

// balance 文本（numeric(20,8)）-> 友好显示（去尾零，保留 2~8 位）。
export function formatBalance(balance: string | null | undefined): string {
  if (!balance) return '0.00';
  const n = Number(balance);
  if (!Number.isFinite(n)) return balance;
  return n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 8 });
}

// RFC3339 -> 本地时间显示。
export function formatDateTime(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false });
}

// 生成幂等键（余额调整去重，避免重复点击双重入账）。
export function newIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return `admin-bal-${crypto.randomUUID()}`;
  }
  return `admin-bal-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`;
}
