// admin 运营管理 API 封装（管理 token 轨）—— 兑换码 / 订阅套餐 / 推荐总览 / 支付订单。
// 走 client.ts 的 apiGet/apiPost + 本模块内自带的 adminPut（client.ts 未导出 PUT/DELETE 助手，
// 且按硬约束不可改它，故复用同一「localStorage huakai_admin_token → Bearer」约定 + 复用 client.ts
// 导出的 ApiError，使 errors.ts friendlyMessage 仍可统一翻译）。参考 lib/api/adminUsers.ts 做法。
//
// 端点形状全部以 HUAKAI 后端真码为准（逐条标注 handler）。注意路由前缀是 /v1/admin/...（兑换码/订阅/
// 推荐/支付），与用户管理的 /admin/v1/... 不同。
//
//   兑换码（voucher_handler.go · MountVoucherAdminRoutes，挂在 /v1/admin/vouchers）—— platform_admin 限定：
//     GET    /v1/admin/vouchers?tenant_id&limit              newVoucherListHandler   → {vouchers:[Voucher]}
//     POST   /v1/admin/vouchers                              newVoucherCreateHandler → CreateResult{voucher,code}
//     POST   /v1/admin/vouchers/batch                        newVoucherBatchCreateHandler → BatchCreateResult{batch,vouchers,codes}
//     GET    /v1/admin/vouchers/batches/{batch_id}?tenant_id newVoucherGetBatchHandler → GetBatchResult{batch,vouchers}
//     POST   /v1/admin/vouchers/{id}/revoke                  newVoucherRevokeHandler → {voucher}
//
//   订阅（subscriptionhttp/handler.go · MountSubscriptionAdminRoutes，挂在 /v1/admin/subscriptions）—— platform_admin 限定：
//     GET    /v1/admin/subscriptions/plans?tenant_id&for_sale  newAdminListPlansHandler → {plans:[planView]}
//     POST   /v1/admin/subscriptions/plans                     newAdminCreatePlanHandler → {plan}
//     POST   /v1/admin/subscriptions/plans/{id}/disable        newAdminDisablePlanHandler → {disabled:true}
//     POST   /v1/admin/subscriptions/assignments              newAdminAssignHandler → {subscription, idempotent}
//     GET    /v1/admin/subscriptions/assignments?tenant_id&user_id|group&limit newAdminListAssignmentsHandler → {subscriptions}
//
//   推荐（referralhttp/handler.go，单独 r.Get 挂载）—— tenant_operator 用自身 scope / platform_admin 必带 ?tenant_id：
//     GET    /v1/admin/referrals/overview?tenant_id           NewAdminReferralOverviewHandler → overviewResponse
//     GET    /v1/admin/referrals?tenant_id&status&limit&offset NewAdminReferralsHandler → adminReferralListResponse
//     GET    /v1/admin/referrals/rewards?tenant_id&referrer_user_id&limit&offset NewAdminReferralRewardsHandler → adminReferralRewardsResponse
//
//   支付（paymenthttp/handler.go · MountPaymentAdminRoutes，挂在 /v1/admin/payments）—— platform_admin 限定：
//     GET    /v1/admin/payments?tenant_id&user_id&status&created_from&created_to&limit&offset newAdminListOrdersHandler → {orders:[adminOrderView]}
//     GET    /v1/admin/payments/dashboard?tenant_id&created_from&created_to newAdminDashboardHandler → dashboardStatsView
//     POST   /v1/admin/payments/{id}/confirm  body{tenant_id, confirm_reason?}  newAdminConfirmHandler
//     POST   /v1/admin/payments/{id}/cancel   body{tenant_id, reason?}          newAdminCancelHandler → {order}
//
// 鉴权要点（读 handler resolveAdmin / resolveVoucherAdmin / resolveAdmin / resolveAdminTenant 确认）：
//   - 兑换码 / 订阅 / 支付 → 三者 resolveAdmin 都强制 ident.Role==platform_admin，否则 403 admin_forbidden。
//     且 tenant_id 取自 query（GET）或 body（部分 POST），不取自 scope —— 故 platform_admin 必须显式带 tenant_id。
//   - 推荐 → resolveAdminTenant 支持 tenant_operator（用 ScopeTenantID，省 ?tenant_id）或 platform_admin（必带 ?tenant_id）。
//
// 借鉴（CLEAN-ROOM，CLAUDE.md §11/§12，仅提取功能/字段/动作/布局形态，未抄码；逐条注来源）：
//   - sub2api(LGPL)@e34ad2b src/api/admin/redeem.ts（list page/pageSize/filters{type,status,search} + generate(count,type,value,
//     validityDays,expiresInDays) 形态）、subscriptions.ts（list filters{status,user_id,group_id} + assign/bulkAssign/extend 动作集）、
//     affiliates.ts（listInviteRecords / listRebateRecords / getUserOverview 推荐总览形态）、payment.ts（dashboard(stats) +
//     orders(list) + plans 形态）。注：sub2api 是分页信封 PaginatedResponse{page,page_size}；HUAKAI 后端是 limit/offset + 裸数组，
//     字段集完全对齐 HUAKAI handler DTO（Voucher / planView / adminReferralItem / adminOrderView），不照搬上游字段名。
//   - new-api(AGPL)@1ac0f58 兑换码（redemption）/ 充值（topup）/ 订单运营页：生成弹窗 + 列表 + 状态徽章的运营形态。
//   CLIProxyAPI@21fad9db 为纯中继代理，无 voucher/订阅/支付/推荐模块（无等价物），不适用。

import { ApiError, apiGet, apiPost } from './client';
import type { APIError } from './types';
import {
  buildBulkAssignBody,
  buildChangePlanBody,
  buildExtendBody,
  buildRevokeBody,
  buildSubscriptionVoucherBody,
  type ExtendInput,
  type SubscriptionVoucherInput,
} from './subscription-lifecycle';

// ---- 共享：管理 token + PUT（client.ts 未提供 PUT，且不可改它）----

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

// adminPut：订阅套餐编辑（PUT /v1/admin/subscriptions/plans/{id}）用，遵循 adminUsers.ts 的同一约定
// （client.ts 无 PUT 助手且不可改它，故本模块内自带；复用同一 Bearer + ApiError 以让 friendlyMessage 统一翻译）。
async function adminPut<T>(path: string, body: unknown): Promise<T> {
  const resp = await fetch(path, { method: 'PUT', headers: adminHeaders(), body: JSON.stringify(body) });
  return parse<T>(resp);
}

// adminPostIdem：带 X-Request-Id 幂等头的 POST（client.ts apiPost 不支持自定义头且不可改它，故本模块内自带）。
// 订阅生命周期写操作用：同一 requestId 重试时后端视为 no-op（handler.go requestID → service RequestID）。
async function adminPostIdem<T>(path: string, body: unknown, requestId?: string): Promise<T> {
  const headers = adminHeaders();
  if (requestId && requestId.trim() !== '') headers['X-Request-Id'] = requestId;
  const resp = await fetch(path, { method: 'POST', headers, body: JSON.stringify(body) });
  return parse<T>(resp);
}

// =====================================================================================
// 兑换码（Voucher）
// =====================================================================================

// 对齐 voucher.Voucher（types.go）。amount_cents 为美分整数（USD），前端按分→元换算显示。
export interface Voucher {
  id: number;
  tenant_id: number;
  batch_id?: number;
  code_fingerprint: string;
  amount_cents: number;
  currency_code: string;
  valid_from: string; // RFC3339
  valid_until: string; // RFC3339
  max_redemptions: number;
  redeemed_count: number;
  single_use_per_user: boolean;
  eligible_user_id?: number;
  grant_kind: string; // balance / subscription
  subscription_plan_id?: number;
  status: string; // active / expired / exhausted / revoked
  created_by_admin_id?: number;
  revoked_reason?: string;
  created_at: string;
  updated_at: string;
  revoked_at?: string;
}

// voucher.Batch（types.go）。
export interface VoucherBatch {
  id: number;
  tenant_id: number;
  requested_count: number;
  created_count: number;
  amount_cents: number;
  currency_code: string;
  valid_from: string;
  valid_until: string;
  max_redemptions: number;
  single_use_per_user: boolean;
  status: string; // active / completed / failed / revoked
  created_at: string;
}

// voucher.CreatedCode（批量返回的明文码，仅生成时一次性回传）。
export interface CreatedCode {
  voucher_id: number;
  code: string;
  code_fingerprint: string;
}

// CreateResult（单张生成）：voucher + 明文 code。
export interface VoucherCreateResult {
  voucher: Voucher;
  code?: string;
}

// BatchCreateResult（批量生成）：batch + vouchers + 明文 codes。
export interface VoucherBatchCreateResult {
  batch: VoucherBatch;
  vouchers: Voucher[];
  codes: CreatedCode[];
}

// listVouchers — GET /v1/admin/vouchers?tenant_id&limit（后端 limit 1..200，缺省 50）。
// 后端要求 tenant_id 为正（platform_admin 必带）。
export function listVouchers(opts: { tenant_id: number; limit?: number }): Promise<{ vouchers: Voucher[] }> {
  return apiGet<{ vouchers: Voucher[] }>('/v1/admin/vouchers', {
    tenant_id: opts.tenant_id,
    limit: opts.limit,
  });
}

// createVoucher — POST /v1/admin/vouchers（单张）。grant_kind=balance（余额券）。
// 后端 voucherCreateRequest（DisallowUnknownFields）：tenant_id / amount_cents / valid_from / valid_until 必填；
// code 省略后端自动生成；currency_code 省略默认 USD（后端只支持 USD）；max_redemptions 省略=1。
export function createVoucher(input: {
  tenant_id: number;
  amount_cents: number;
  valid_from: string; // RFC3339
  valid_until: string; // RFC3339
  code?: string;
  currency_code?: string;
  max_redemptions?: number;
  single_use_per_user?: boolean;
  eligible_user_id?: number;
}): Promise<VoucherCreateResult> {
  return apiPost<VoucherCreateResult>('/v1/admin/vouchers', buildVoucherBody(input));
}

// createVoucherBatch — POST /v1/admin/vouchers/batch（批量 count 张）。
export function createVoucherBatch(input: {
  tenant_id: number;
  count: number;
  amount_cents: number;
  valid_from: string;
  valid_until: string;
  currency_code?: string;
  max_redemptions?: number;
  single_use_per_user?: boolean;
  eligible_user_id?: number;
}): Promise<VoucherBatchCreateResult> {
  const body = buildVoucherBody(input);
  body.count = input.count;
  return apiPost<VoucherBatchCreateResult>('/v1/admin/vouchers/batch', body);
}

// 共享请求体构造：只放后端 DisallowUnknownFields 接受的字段，省略空值。
function buildVoucherBody(input: {
  tenant_id: number;
  amount_cents: number;
  valid_from: string;
  valid_until: string;
  code?: string;
  currency_code?: string;
  max_redemptions?: number;
  single_use_per_user?: boolean;
  eligible_user_id?: number;
}): Record<string, unknown> {
  const body: Record<string, unknown> = {
    tenant_id: input.tenant_id,
    amount_cents: input.amount_cents,
    valid_from: input.valid_from,
    valid_until: input.valid_until,
  };
  if (input.code && input.code.trim() !== '') body.code = input.code.trim();
  if (input.currency_code && input.currency_code.trim() !== '') body.currency_code = input.currency_code.trim();
  if (input.max_redemptions != null && input.max_redemptions > 0) body.max_redemptions = input.max_redemptions;
  if (input.single_use_per_user != null) body.single_use_per_user = input.single_use_per_user;
  if (input.eligible_user_id != null && input.eligible_user_id > 0) body.eligible_user_id = input.eligible_user_id;
  return body;
}

// revokeVoucher — POST /v1/admin/vouchers/{id}/revoke  body{tenant_id, reason?}。
export function revokeVoucher(id: number, input: { tenant_id: number; reason?: string }): Promise<{ voucher: Voucher }> {
  const body: Record<string, unknown> = { tenant_id: input.tenant_id };
  if (input.reason && input.reason.trim() !== '') body.reason = input.reason.trim();
  return apiPost<{ voucher: Voucher }>(`/v1/admin/vouchers/${id}/revoke`, body);
}

// =====================================================================================
// 订阅套餐（Plan）+ 指派
// =====================================================================================

// 对齐 subscriptionhttp.planView（handler.go）。
export interface SubscriptionPlan {
  id: number;
  tenant_id: number;
  name: string;
  description?: string;
  price_cents: number;
  currency_code: string;
  validity_days: number;
  granted_group?: string;
  daily_cap_usd?: string;
  weekly_cap_usd?: string;
  monthly_cap_usd?: string;
  for_sale: boolean;
  enabled: boolean;
  sort_order: number;
  created_at: string;
  updated_at: string;
}

// 对齐 subscriptionhttp.adminSubscriptionView（subscriptionView + admin 字段）。
export interface AdminSubscription {
  id: number;
  plan_id: number;
  granted_group?: string;
  daily_cap_usd?: string;
  weekly_cap_usd?: string;
  monthly_cap_usd?: string;
  status: string; // active / expired / cancelled / revoked …
  starts_at: string;
  expires_at: string;
  cancelled_at?: string;
  created_at: string;
  user_id: number;
  source: string;
  assigned_by_admin_id?: number;
  prev_user_group?: string;
}

// listPlans — GET /v1/admin/subscriptions/plans?tenant_id&for_sale。
export function listPlans(opts: { tenant_id: number; for_sale?: boolean }): Promise<{ plans: SubscriptionPlan[] }> {
  return apiGet<{ plans: SubscriptionPlan[] }>('/v1/admin/subscriptions/plans', {
    tenant_id: opts.tenant_id,
    for_sale: opts.for_sale ? 'true' : undefined,
  });
}

// createPlan — POST /v1/admin/subscriptions/plans。
// 后端 createPlanRequest（DisallowUnknownFields）：name / validity_days 必填；price_cents 省略=0（免费套餐）;
// currency_code 省略后端默认；for_sale 省略=true（上架）。caps 为非负十进制字符串（省略=不限）。
export function createPlan(input: {
  tenant_id: number;
  name: string;
  validity_days: number;
  description?: string;
  price_cents?: number;
  currency_code?: string;
  granted_group?: string;
  daily_cap_usd?: string;
  weekly_cap_usd?: string;
  monthly_cap_usd?: string;
  for_sale?: boolean;
  sort_order?: number;
}): Promise<{ plan: SubscriptionPlan }> {
  const body: Record<string, unknown> = {
    tenant_id: input.tenant_id,
    name: input.name,
    validity_days: input.validity_days,
  };
  if (input.description && input.description.trim() !== '') body.description = input.description.trim();
  if (input.price_cents != null) body.price_cents = input.price_cents;
  if (input.currency_code && input.currency_code.trim() !== '') body.currency_code = input.currency_code.trim();
  if (input.granted_group && input.granted_group.trim() !== '') body.granted_group = input.granted_group.trim();
  if (input.daily_cap_usd && input.daily_cap_usd.trim() !== '') body.daily_cap_usd = input.daily_cap_usd.trim();
  if (input.weekly_cap_usd && input.weekly_cap_usd.trim() !== '') body.weekly_cap_usd = input.weekly_cap_usd.trim();
  if (input.monthly_cap_usd && input.monthly_cap_usd.trim() !== '') body.monthly_cap_usd = input.monthly_cap_usd.trim();
  if (input.for_sale != null) body.for_sale = input.for_sale;
  if (input.sort_order != null) body.sort_order = input.sort_order;
  return apiPost<{ plan: SubscriptionPlan }>('/v1/admin/subscriptions/plans', body);
}

// disablePlan — POST /v1/admin/subscriptions/plans/{id}/disable  body{tenant_id}（下架/停用套餐）。
export function disablePlan(id: number, tenantId: number): Promise<{ disabled: boolean }> {
  return apiPost<{ disabled: boolean }>(`/v1/admin/subscriptions/plans/${id}/disable`, { tenant_id: tenantId });
}

// updatePlan — PUT /v1/admin/subscriptions/plans/{id}（编辑套餐）。后端 newAdminUpdatePlanHandler 复用
// createPlanRequest 但 **要求 for_sale 必填**（决定上/下架），故本函数强制带 for_sale。走自带 adminPut
// （client.ts 无 PUT 助手），复用同一 Bearer 约定 + 复用 ApiError 让 friendlyMessage 统一翻译。
export function updatePlan(
  id: number,
  input: {
    tenant_id: number;
    name: string;
    validity_days: number;
    for_sale: boolean;
    description?: string;
    price_cents?: number;
    currency_code?: string;
    granted_group?: string;
    daily_cap_usd?: string;
    weekly_cap_usd?: string;
    monthly_cap_usd?: string;
    sort_order?: number;
  },
): Promise<{ plan: SubscriptionPlan }> {
  const body: Record<string, unknown> = {
    tenant_id: input.tenant_id,
    name: input.name,
    validity_days: input.validity_days,
    for_sale: input.for_sale,
  };
  if (input.description && input.description.trim() !== '') body.description = input.description.trim();
  if (input.price_cents != null) body.price_cents = input.price_cents;
  if (input.currency_code && input.currency_code.trim() !== '') body.currency_code = input.currency_code.trim();
  if (input.granted_group && input.granted_group.trim() !== '') body.granted_group = input.granted_group.trim();
  if (input.daily_cap_usd && input.daily_cap_usd.trim() !== '') body.daily_cap_usd = input.daily_cap_usd.trim();
  if (input.weekly_cap_usd && input.weekly_cap_usd.trim() !== '') body.weekly_cap_usd = input.weekly_cap_usd.trim();
  if (input.monthly_cap_usd && input.monthly_cap_usd.trim() !== '') body.monthly_cap_usd = input.monthly_cap_usd.trim();
  if (input.sort_order != null) body.sort_order = input.sort_order;
  return adminPut<{ plan: SubscriptionPlan }>(`/v1/admin/subscriptions/plans/${id}`, body);
}

// assignSubscription — POST /v1/admin/subscriptions/assignments  body{tenant_id, user_id, plan_id}（运营手动指派订阅）。
export function assignSubscription(input: {
  tenant_id: number;
  user_id: number;
  plan_id: number;
}): Promise<{ subscription: AdminSubscription; idempotent: boolean }> {
  return apiPost<{ subscription: AdminSubscription; idempotent: boolean }>('/v1/admin/subscriptions/assignments', {
    tenant_id: input.tenant_id,
    user_id: input.user_id,
    plan_id: input.plan_id,
  });
}

// listAssignmentsByUser — GET /v1/admin/subscriptions/assignments?tenant_id&user_id（查某用户的订阅）。
export function listAssignmentsByUser(opts: {
  tenant_id: number;
  user_id: number;
}): Promise<{ subscriptions: AdminSubscription[] }> {
  return apiGet<{ subscriptions: AdminSubscription[] }>('/v1/admin/subscriptions/assignments', {
    tenant_id: opts.tenant_id,
    user_id: opts.user_id,
  });
}

// listAssignmentsByGroup — GET /v1/admin/subscriptions/assignments?tenant_id&group&limit（按分组查订阅）。
export function listAssignmentsByGroup(opts: {
  tenant_id: number;
  group: string;
  limit?: number;
}): Promise<{ subscriptions: AdminSubscription[] }> {
  return apiGet<{ subscriptions: AdminSubscription[] }>('/v1/admin/subscriptions/assignments', {
    tenant_id: opts.tenant_id,
    group: opts.group,
    limit: opts.limit,
  });
}

// =====================================================================================
// 订阅生命周期 admin 写操作（cancel / extend / reset-quota / change-plan / revoke / bulk + 订阅券）
// 端点 /v1/admin/subscriptions/assignments/{id}/...（读 subscriptionhttp/handler.go:262-267 真码）。
// 全部走 adminPostIdem 带 X-Request-Id 幂等头；请求体校验/构造见 subscription-lifecycle.ts（纯逻辑层，已单测）。
// =====================================================================================

// bulkAssign 逐用户结果（对齐 subscriptionhttp.bulkAssignUserView）。
export interface BulkAssignResult {
  user_id: number;
  ok: boolean;
  error?: string;
  idempotent?: boolean;
  subscription?: AdminSubscription;
}

// cancelSubscription — POST /assignments/{id}/cancel  body{tenant_id}（软取消：关配额 + 降级）。
export function cancelSubscription(
  id: number,
  tenantId: number,
  requestId?: string,
): Promise<{ subscription: AdminSubscription }> {
  return adminPostIdem<{ subscription: AdminSubscription }>(
    `/v1/admin/subscriptions/assignments/${id}/cancel`,
    { tenant_id: tenantId },
    requestId,
  );
}

// extendSubscription — POST /assignments/{id}/extend  body{tenant_id, days?|until?}（后端要求 days>0 XOR until）。
export function extendSubscription(
  id: number,
  tenantId: number,
  input: ExtendInput,
  requestId?: string,
): Promise<{ subscription: AdminSubscription }> {
  return adminPostIdem<{ subscription: AdminSubscription }>(
    `/v1/admin/subscriptions/assignments/${id}/extend`,
    buildExtendBody(tenantId, input),
    requestId,
  );
}

// resetSubscriptionQuota — POST /assignments/{id}/reset-quota  body{tenant_id}（按套餐快照重建全部配额窗口）。
export function resetSubscriptionQuota(
  id: number,
  tenantId: number,
  requestId?: string,
): Promise<{ subscription: AdminSubscription }> {
  return adminPostIdem<{ subscription: AdminSubscription }>(
    `/v1/admin/subscriptions/assignments/${id}/reset-quota`,
    { tenant_id: tenantId },
    requestId,
  );
}

// changeSubscriptionPlan — POST /assignments/{id}/change-plan  body{tenant_id, new_plan_id, allow_downgrade?}。
export function changeSubscriptionPlan(
  id: number,
  tenantId: number,
  newPlanId: number,
  allowDowngrade?: boolean,
  requestId?: string,
): Promise<{ subscription: AdminSubscription }> {
  return adminPostIdem<{ subscription: AdminSubscription }>(
    `/v1/admin/subscriptions/assignments/${id}/change-plan`,
    buildChangePlanBody(tenantId, newPlanId, allowDowngrade),
    requestId,
  );
}

// revokeSubscription — POST /assignments/{id}/revoke  body{tenant_id, reason}（硬结，reason 必填）。
export function revokeSubscription(
  id: number,
  tenantId: number,
  reason: string,
  requestId?: string,
): Promise<{ subscription: AdminSubscription }> {
  return adminPostIdem<{ subscription: AdminSubscription }>(
    `/v1/admin/subscriptions/assignments/${id}/revoke`,
    buildRevokeBody(tenantId, reason),
    requestId,
  );
}

// bulkAssignSubscription — POST /assignments/bulk  body{tenant_id, user_ids, plan_id}（逐用户软失败）。
export function bulkAssignSubscription(
  input: { tenant_id: number; user_ids: number[]; plan_id: number },
  requestId?: string,
): Promise<{ results: BulkAssignResult[] }> {
  return adminPostIdem<{ results: BulkAssignResult[] }>(
    '/v1/admin/subscriptions/assignments/bulk',
    buildBulkAssignBody(input.tenant_id, input.user_ids, input.plan_id),
    requestId,
  );
}

// createSubscriptionVoucher — POST /vouchers  body{tenant_id, plan_id, amount_cents, valid_from, valid_until, ...}。
// grant_kind=subscription 由端点强制（不传）；返回 {voucher, code}（201，明文 code 仅此一次可见）。
export function createSubscriptionVoucher(
  input: SubscriptionVoucherInput,
  requestId?: string,
): Promise<VoucherCreateResult> {
  return adminPostIdem<VoucherCreateResult>(
    '/v1/admin/subscriptions/vouchers',
    buildSubscriptionVoucherBody(input),
    requestId,
  );
}

// =====================================================================================
// 推荐（Referral）
// =====================================================================================

// overviewResponse（referralhttp）。
export interface ReferralOverview {
  object: string;
  counts_by_status: Record<string, number>;
  total_reward_usd: string; // decimal 序列化为字符串
  reward_count: number;
}

// adminReferralItem（referralhttp）。
export interface AdminReferral {
  id: number;
  referrer_user_id: number;
  referee_user_id: number;
  status: string;
  created_at: string;
}

export interface AdminReferralListResponse {
  object: string;
  items: AdminReferral[];
  total: number;
  limit: number;
  offset: number;
}

// adminReferralRewardItem（referralhttp）。
export interface AdminReferralReward {
  id: number;
  referral_id: number;
  referrer_user_id: number;
  reward_type: string;
  amount_usd: string;
  issued_at: string;
}

export interface AdminReferralRewardsResponse {
  object: string;
  items: AdminReferralReward[];
  total: number;
  total_reward_usd: string;
  limit: number;
  offset: number;
}

// getReferralOverview — GET /v1/admin/referrals/overview?tenant_id。
// 推荐端点 resolveAdminTenant：tenant_operator 可省 tenant_id（用 scope），platform_admin 必带。
export function getReferralOverview(tenantId?: number): Promise<ReferralOverview> {
  return apiGet<ReferralOverview>('/v1/admin/referrals/overview', { tenant_id: tenantId });
}

// listReferrals — GET /v1/admin/referrals?tenant_id&status&limit&offset。
export function listReferrals(opts: {
  tenant_id?: number;
  status?: string;
  limit?: number;
  offset?: number;
}): Promise<AdminReferralListResponse> {
  return apiGet<AdminReferralListResponse>('/v1/admin/referrals', {
    tenant_id: opts.tenant_id,
    status: opts.status && opts.status !== 'all' ? opts.status : undefined,
    limit: opts.limit,
    offset: opts.offset,
  });
}

// listReferralRewards — GET /v1/admin/referrals/rewards?tenant_id&referrer_user_id&limit&offset。
export function listReferralRewards(opts: {
  tenant_id?: number;
  referrer_user_id?: number;
  limit?: number;
  offset?: number;
}): Promise<AdminReferralRewardsResponse> {
  return apiGet<AdminReferralRewardsResponse>('/v1/admin/referrals/rewards', {
    tenant_id: opts.tenant_id,
    referrer_user_id: opts.referrer_user_id,
    limit: opts.limit,
    offset: opts.offset,
  });
}

// =====================================================================================
// 支付订单（Payment）
// =====================================================================================

// adminOrderView（paymenthttp/handler.go）。amount_cents 为美分整数（USD）。
export interface AdminOrder {
  id: number;
  out_trade_no: string;
  user_id: number;
  amount_cents: number;
  currency_code: string;
  status: string; // pending / paid / recharging / completed / cancelled / failed / refunded …
  provider_kind: string; // manual / taobao …
  order_kind: string; // topup / subscription
  subscription_plan_id?: number;
  created_at: string;
  updated_at: string;
  expires_at?: string;
  paid_at?: string;
  completed_at?: string;
  recharging_at?: string;
  failed_at?: string;
  created_by_admin_id?: number;
  confirmed_by_admin_id?: number;
  confirm_reason?: string;
  provider_order_ref?: string;
  failure_code?: string;
  failure_message?: string;
}

// dashboardStatsView（admin_panel.go）。
export interface PaymentDashboard {
  total_amount_cents: number;
  total_count: number;
  today_count: number;
  average_amount_cents: number;
  daily_series: { date: string; order_count: number; amount_cents: number }[];
}

// listOrders — GET /v1/admin/payments?tenant_id&user_id&status&created_from&created_to&limit&offset。
// 后端 parseOrderListFilter：tenant_id 必带；status 直传（空=全部）；时间为 RFC3339；limit 缺省 50（封顶 200）。
export function listOrders(opts: {
  tenant_id: number;
  user_id?: number;
  status?: string;
  limit?: number;
  offset?: number;
}): Promise<{ orders: AdminOrder[] }> {
  return apiGet<{ orders: AdminOrder[] }>('/v1/admin/payments', {
    tenant_id: opts.tenant_id,
    user_id: opts.user_id,
    status: opts.status && opts.status !== 'all' ? opts.status : undefined,
    limit: opts.limit,
    offset: opts.offset,
  });
}

// getPaymentDashboard — GET /v1/admin/payments/dashboard?tenant_id。
export function getPaymentDashboard(tenantId: number): Promise<PaymentDashboard> {
  return apiGet<PaymentDashboard>('/v1/admin/payments/dashboard', { tenant_id: tenantId });
}

// confirmOrder — POST /v1/admin/payments/{id}/confirm  body{tenant_id, confirm_reason?}（人工确认收款 + 履约入账）。
export function confirmOrder(id: number, input: { tenant_id: number; confirm_reason?: string }): Promise<unknown> {
  const body: Record<string, unknown> = { tenant_id: input.tenant_id };
  if (input.confirm_reason && input.confirm_reason.trim() !== '') body.confirm_reason = input.confirm_reason.trim();
  return apiPost<unknown>(`/v1/admin/payments/${id}/confirm`, body);
}

// cancelOrder — POST /v1/admin/payments/{id}/cancel  body{tenant_id, reason?}（运营撤单，仅 pending 单可撤）。
export function cancelOrder(id: number, input: { tenant_id: number; reason?: string }): Promise<{ order: AdminOrder }> {
  const body: Record<string, unknown> = { tenant_id: input.tenant_id };
  if (input.reason && input.reason.trim() !== '') body.reason = input.reason.trim();
  return apiPost<{ order: AdminOrder }>(`/v1/admin/payments/${id}/cancel`, body);
}

// =====================================================================================
// 展示辅助
// =====================================================================================

// 美分（USD）→ 友好金额显示，例 1099 → "$10.99"。
export function formatCents(cents: number | null | undefined, currency = 'USD'): string {
  if (cents == null || !Number.isFinite(cents)) return '—';
  const symbol = currency === 'USD' ? '$' : '';
  return `${symbol}${(cents / 100).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

// decimal 字符串（如推荐奖励 USD）→ 友好显示。
export function formatUSDString(value: string | null | undefined): string {
  if (!value) return '$0.00';
  const n = Number(value);
  if (!Number.isFinite(n)) return value;
  return `$${n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

// RFC3339 → 本地时间显示。
export function formatDateTime(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleString('zh-CN', { hour12: false });
}

// RFC3339 → 仅日期。
export function formatDate(value: string | null | undefined): string {
  if (!value) return '—';
  const d = new Date(value);
  return Number.isNaN(d.getTime()) ? '—' : d.toLocaleDateString('zh-CN');
}

// 把「天数」转成相对当前时间的 valid_until RFC3339（券码有效窗口）。
export function rfc3339FromNow(days: number): string {
  const d = new Date(Date.now() + days * 24 * 60 * 60 * 1000);
  return d.toISOString();
}

// 当前时刻 RFC3339（valid_from）。
export function nowRfc3339(): string {
  return new Date().toISOString();
}

// 券状态 → 中文。
export function voucherStatusLabel(status: string): string {
  switch (status) {
    case 'active':
      return '可用';
    case 'expired':
      return '已过期';
    case 'exhausted':
      return '已用尽';
    case 'revoked':
      return '已作废';
    default:
      return status || '未知';
  }
}

export function voucherStatusVariant(status: string): 'default' | 'secondary' | 'destructive' | 'outline' {
  if (status === 'active') return 'default';
  if (status === 'revoked' || status === 'expired') return 'destructive';
  if (status === 'exhausted') return 'secondary';
  return 'outline';
}

// 订阅状态 → 中文。
export function subscriptionStatusLabel(status: string): string {
  switch (status) {
    case 'active':
      return '生效中';
    case 'expired':
      return '已过期';
    case 'cancelled':
      return '已取消';
    case 'revoked':
      return '已撤销';
    default:
      return status || '未知';
  }
}

export function subscriptionStatusVariant(status: string): 'default' | 'secondary' | 'destructive' | 'outline' {
  if (status === 'active') return 'default';
  if (status === 'revoked' || status === 'expired') return 'destructive';
  if (status === 'cancelled') return 'secondary';
  return 'outline';
}

// 订单状态 → 中文。
export function orderStatusLabel(status: string): string {
  switch (status) {
    case 'pending':
      return '待支付';
    case 'paid':
      return '已支付';
    case 'recharging':
      return '入账中';
    case 'completed':
      return '已完成';
    case 'cancelled':
      return '已取消';
    case 'failed':
      return '已失败';
    case 'refunded':
      return '已退款';
    default:
      return status || '未知';
  }
}

export function orderStatusVariant(status: string): 'default' | 'secondary' | 'destructive' | 'outline' {
  if (status === 'completed' || status === 'paid') return 'default';
  if (status === 'failed' || status === 'cancelled') return 'destructive';
  if (status === 'pending' || status === 'recharging') return 'secondary';
  return 'outline';
}
