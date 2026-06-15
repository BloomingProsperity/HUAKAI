// 用户自助订阅：封装 /v1/users/me/subscriptions/* 端点（session 鉴权，走 userClient）。
// 字段形状对齐后端 internal/subscriptionhttp/{handler.go,purchase.go}
// （MountSubscriptionUserRoutes，session bearer token 鉴权）。
//
// 借鉴来源（CLEAN-ROOM，仅提取功能/字段形态，未抄码）：
//   - sub2api(LGPL) src/api/subscriptions.ts + src/types/index.ts：
//     list/active/progress/summary 的「当前订阅 + 日/周/月进度（used/limit/percentage/
//     reset_in_seconds）+ 到期/剩余天数」概念形态。HUAKAI 后端把这些拆成
//     /  (列表)、/me (当前) 、/me/progress (按 quota 窗口的进度) 三个端点，字段名/
//     单位以 HUAKAI handler 真码为准（cap/consumed/remaining/usage_percent/
//     resets_in_seconds/over_limit，金额为 USD decimal 字符串）。
//   - new-api(copyleft)：无 windowed 订阅模块（topup/兑换/额度计数器模型），故窗口进度
//     形态不借鉴 new-api；仅其「套餐卡 + 购买动作」的电商化呈现思路作参考。

import { userGet, userPost } from './userClient';

// ---- 后端枚举（与 handler/domain 一致） ----

// internal/subscription/types.go：active / expired / cancelled / revoked
export type SubscriptionStatus = 'active' | 'expired' | 'cancelled' | 'revoked' | (string & {});

// internal/quota/types.go WindowKind（progress 仅产出日/周/月这三种）
export type WindowKind =
  | 'calendar_day'
  | 'calendar_week'
  | 'calendar_month'
  | 'none'
  | 'fixed'
  | 'manual'
  | (string & {});

// ---- DTO（snake_case，对齐 handler.go subscriptionView / planView） ----

// subscriptionView（handler.go）：用户列表与当前订阅共用。金额上限为 USD decimal 字符串。
export interface SubscriptionView {
  id: number;
  plan_id: number;
  granted_group?: string;
  daily_cap_usd?: string | null;
  weekly_cap_usd?: string | null;
  monthly_cap_usd?: string | null;
  status: SubscriptionStatus;
  starts_at: string;
  expires_at: string;
  cancelled_at?: string | null;
  created_at: string;
}

// planView（handler.go）：在售套餐。price_cents 为最小货币单位整数。
export interface PlanView {
  id: number;
  tenant_id: number;
  name: string;
  description?: string;
  price_cents: number;
  currency_code: string;
  validity_days: number;
  granted_group?: string;
  daily_cap_usd?: string | null;
  weekly_cap_usd?: string | null;
  monthly_cap_usd?: string | null;
  for_sale: boolean;
  enabled: boolean;
  sort_order: number;
  created_at: string;
  updated_at: string;
}

// subscriptionProgressView（purchase.go）：当前订阅按 quota 窗口的用量进度。
// 所有金额（cap/consumed/remaining/overage/over_limit_amount）为同一 USD decimal 字符串单位。
export interface SubscriptionProgressWindow {
  window_kind: WindowKind;
  cap: string;
  consumed: string;
  remaining: string;
  overage: string;
  request_count: number;
  window_start: string;
  window_end: string;
  // 后端派生字段（纯读侧计算）
  usage_percent: number; // consumed/cap*100；cap==0 时为 0；超额时可 >100
  resets_in_seconds: number; // max(0, window_end − now)；窗口已过为 0
  over_limit: boolean; // consumed > cap
  over_limit_amount: string; // max(0, consumed − cap)，同 USD decimal 单位
}

// ---- 端点信封类型 ----

// GET /  → newUserListSubscriptionsHandler
export interface SubscriptionListResponse {
  subscriptions: SubscriptionView[];
}

// GET /me → newUserCurrentSubscriptionHandler（subscription 可能为 null）
export interface CurrentSubscriptionResponse {
  subscription: SubscriptionView | null;
  auto_renew: boolean;
}

// GET /me/progress → newUserSubscriptionProgressHandler（无当前订阅时 subscription=null、progress=[]）
export interface SubscriptionProgressResponse {
  subscription: SubscriptionView | null;
  progress: SubscriptionProgressWindow[];
}

// GET /plans → newUserListPlansHandler（仅在售启用）
export interface PlanListResponse {
  plans: PlanView[];
}

// POST /change-plan → newUserChangePlanHandler（升级；降级需 admin）
export interface ChangePlanResponse {
  subscription: SubscriptionView;
}

// POST /cancel-renew → newUserCancelRenewHandler（仅关自动续订）
export interface CancelRenewResponse {
  subscription: SubscriptionView | null;
  auto_renew: boolean;
}

// POST /purchase → newUserPurchaseHandler（建一张 subscription 订单，履约后才生效）
export interface PurchaseOrderView {
  id: number;
  out_trade_no: string;
  status: string;
  amount_cents: number;
  currency_code: string;
  order_kind: string;
  subscription_plan_id?: number | null;
}

export interface PurchaseResponse {
  order: PurchaseOrderView;
  idempotent: boolean;
  payment_instruction: string;
}

const BASE_PATH = '/v1/users/me/subscriptions';

// 列出当前用户的全部订阅（任意状态）。
export function listSubscriptions(): Promise<SubscriptionListResponse> {
  return userGet<SubscriptionListResponse>(`${BASE_PATH}/`);
}

// 当前生效订阅（status=active 且 now 在窗口内，多条取最长权益）+ auto_renew。
export function getCurrentSubscription(): Promise<CurrentSubscriptionResponse> {
  return userGet<CurrentSubscriptionResponse>(`${BASE_PATH}/me`);
}

// 当前订阅按 quota 窗口（日/周/月）的用量进度。
export function getSubscriptionProgress(): Promise<SubscriptionProgressResponse> {
  return userGet<SubscriptionProgressResponse>(`${BASE_PATH}/me/progress`);
}

// 在售启用套餐（用于升级/购买选择）。
export function listPlans(): Promise<PlanListResponse> {
  return userGet<PlanListResponse>(`${BASE_PATH}/plans`);
}

// 自助换套餐（仅升级；new_plan_id 必填正整数，否则后端 400）。
export function changePlan(newPlanId: number): Promise<ChangePlanResponse> {
  return userPost<ChangePlanResponse>(`${BASE_PATH}/change-plan`, { new_plan_id: newPlanId });
}

// 关闭自动续订（不取消当前已生效权益）。
export function cancelRenew(): Promise<CancelRenewResponse> {
  return userPost<CancelRenewResponse>(`${BASE_PATH}/cancel-renew`);
}

// 自助购买：建一张 subscription 订单，返回支付指引；履约后订阅才生效。
export function purchasePlan(planId: number): Promise<PurchaseResponse> {
  return userPost<PurchaseResponse>(`${BASE_PATH}/purchase`, { plan_id: planId });
}

// ---- 读侧派生辅助（纯前端计算，不新增端点） ----

export const WINDOW_LABELS: Record<string, string> = {
  calendar_day: '日额度',
  calendar_week: '周额度',
  calendar_month: '月额度',
  none: '无窗口限制',
  fixed: '固定窗口',
  manual: '手动窗口',
};

export function windowLabel(kind: WindowKind): string {
  return WINDOW_LABELS[kind] ?? kind;
}

const STATUS_LABELS: Record<string, string> = {
  active: '生效中',
  expired: '已过期',
  cancelled: '已取消',
  revoked: '已吊销',
};

export function statusLabel(status: SubscriptionStatus): string {
  return STATUS_LABELS[status] ?? status;
}

// 解析 USD decimal 字符串为 number（仅用于展示/排序，不做金额运算）。
export function parseUsd(value: string | null | undefined): number {
  if (!value) return 0;
  const n = Number.parseFloat(value);
  return Number.isFinite(n) ? n : 0;
}

// 距到期天数：向上取整，已过期返回 0；无 expires_at 返回 null。
export function daysRemaining(expiresAt: string | null | undefined, now: Date = new Date()): number | null {
  if (!expiresAt) return null;
  const exp = new Date(expiresAt);
  if (Number.isNaN(exp.getTime())) return null;
  const ms = exp.getTime() - now.getTime();
  if (ms <= 0) return 0;
  return Math.ceil(ms / (24 * 60 * 60 * 1000));
}

// 把 resets_in_seconds 渲染成「Nd Nh / Nh Nm / Nm」紧凑中文。
export function formatResetIn(seconds: number): string {
  if (seconds <= 0) return '即将重置';
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return `${d} 天 ${h} 小时后重置`;
  if (h > 0) return `${h} 小时 ${m} 分后重置`;
  if (m > 0) return `${m} 分后重置`;
  return '不到 1 分钟后重置';
}
