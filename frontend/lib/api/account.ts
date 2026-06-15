// 个人中心 (账户 / 分组 / 邀请 / 签到 / 推荐 / 配额) 数据层。
// 全部走用户面 session 鉴权 (userClient 注入 session_token + 401 刷新)。
// 端点形状均按 HUAKAI 后端 handler 真码确认:
//   GET  /v1/me/groups            megroupshttp.NewHandler
//   GET  /v1/me/quota             mequotahttp.NewHandler
//   GET  /v1/me/invitations       gatewayhttp.NewInvitationSummaryHandler
//   GET  /v1/me/invitation-code   gatewayhttp.NewMyReferralCodeHandler
//   GET  /v1/me/checkin           checkinhttp.newStatusHandler
//   POST /v1/me/checkin           checkinhttp.newPostHandler
//   GET  /v1/me/referrals         referralhttp.NewUserReferralsHandler
//   GET  /v1/me/referrals/rewards referralhttp.NewUserReferralRewardsHandler
// 借鉴 (功能/字段形态,非抄码): sub2api groups.ts (getAvailable + 分组倍率公开/非公开),
// new-api 个人中心 (签到领奖 + 邀请码 + 推荐账本)。
import { userGet, userPost } from './userClient';

// ---------- 可用分组 (GET /v1/me/groups) ----------
// 后端 megroupshttp listResponse: { object, user_group, items[] }。
// ratio 仅在运营标记公开 (has_public_ratio) 时下发,内部成本倍率不外泄。
export interface MeGroup {
  pool_group_id: number;
  name: string;
  ratio?: string; // 公开倍率字符串 (decimal),非公开时缺省
  has_public_ratio: boolean;
}

export interface MeGroupsResponse {
  object: string;
  user_group: string;
  items: MeGroup[];
}

export function fetchGroups(): Promise<MeGroupsResponse> {
  return userGet<MeGroupsResponse>('/v1/me/groups');
}

// ---------- 配额概览 (GET /v1/me/quota) ----------
// mequotahttp listResponse: { items[] }。金额均为 decimal 字符串 (USD)。
export type QuotaWindowKind = string;

export interface MeQuotaWindow {
  window_kind: QuotaWindowKind;
  cap: string; // 上限 (decimal 字符串)
  consumed: string; // 已用 (settled + reserved)
  remaining: string; // 剩余 (夹紧 >= 0)
  overage: string; // 超额
  request_count: number;
  window_start: string; // RFC3339
  window_end: string;
}

export interface MeQuotaResponse {
  items: MeQuotaWindow[];
}

export function fetchQuota(): Promise<MeQuotaResponse> {
  return userGet<MeQuotaResponse>('/v1/me/quota');
}

// ---------- 邀请概要 + 我的邀请码 ----------
// 邀请概要 invitationSummaryResponse: 全部为整数计数 / cents。
export interface InvitationSummary {
  qualified_count: number;
  rewarded_count: number;
  rewards_earned_cents: number;
}

export function fetchInvitationSummary(): Promise<InvitationSummary> {
  return userGet<InvitationSummary>('/v1/me/invitations');
}

// myReferralCodeResponse: 稳定的自助邀请码,首次访问惰性生成,不受月度活动配额限制。
export interface MyReferralCode {
  code: string;
  inviter_user_id: number;
}

export function fetchMyReferralCode(): Promise<MyReferralCode> {
  return userGet<MyReferralCode>('/v1/me/invitation-code');
}

// ---------- 每日签到 (GET / POST /v1/me/checkin) ----------
// statusResponse: 奖励区间与余额单位均为 cents(美分,/100 = USD)。
export interface CheckinRecord {
  checkin_date: string; // YYYY-MM-DD
  reward_cents: number;
  currency_code: string;
  billing_event_id?: number;
  created_at: string; // RFC3339
}

export interface CheckinStatus {
  enabled: boolean;
  min_cents: number;
  max_cents: number;
  month: string; // YYYY-MM
  checked_in_today: boolean;
  records: CheckinRecord[];
}

// month 可选 (YYYY-MM);省略则取当前 UTC 月。
export function fetchCheckinStatus(month?: string): Promise<CheckinStatus> {
  return userGet<CheckinStatus>('/v1/me/checkin', month ? { month } : undefined);
}

// postResponse: 本次领取金额 + 日期 + 领取后余额(均 cents)。
export interface CheckinResult {
  reward_cents: number;
  checkin_date: string;
  new_balance: number;
}

export function doCheckin(): Promise<CheckinResult> {
  return userPost<CheckinResult>('/v1/me/checkin');
}

// ---------- 推荐记录 + 奖励账本 ----------
// referralListResponse: 分页 (limit/offset),status ∈ pending|qualified|rewarded|rejected。
export type ReferralStatus = 'pending' | 'qualified' | 'rewarded' | 'rejected' | string;

export interface ReferralRecord {
  referral_id: number;
  referee_user_id: number;
  status: ReferralStatus;
  created_at: string; // RFC3339
  rewarded_at?: string | null;
}

export interface ReferralListResponse {
  object: string;
  items: ReferralRecord[];
  total: number;
  limit: number;
  offset: number;
}

export function fetchReferrals(params?: { limit?: number; offset?: number }): Promise<ReferralListResponse> {
  return userGet<ReferralListResponse>('/v1/me/referrals', params);
}

// rewardLedgerResponse: amount_usd / total_reward_usd 为 decimal 字符串 (后端 shopspring 默认带引号)。
export interface ReferralRewardItem {
  referral_id: number;
  reward_type: string;
  amount_usd: string;
  created_at: string; // RFC3339
}

export interface ReferralRewardsResponse {
  object: string;
  items: ReferralRewardItem[];
  total: number;
  total_reward_usd: string;
  limit: number;
  offset: number;
}

export function fetchReferralRewards(params?: { limit?: number; offset?: number }): Promise<ReferralRewardsResponse> {
  return userGet<ReferralRewardsResponse>('/v1/me/referrals/rewards', params);
}

// ---------- 展示辅助 ----------
const REFERRAL_STATUS_LABEL: Record<string, string> = {
  pending: '待达标',
  qualified: '已达标',
  rewarded: '已发奖',
  rejected: '已驳回',
};

export function referralStatusLabel(status: string): string {
  return REFERRAL_STATUS_LABEL[status] ?? status;
}

export type StatusTone = 'emerald' | 'amber' | 'blue' | 'red' | 'slate';

export function referralStatusTone(status: string): StatusTone {
  switch (status) {
    case 'rewarded':
      return 'emerald';
    case 'qualified':
      return 'blue';
    case 'pending':
      return 'amber';
    case 'rejected':
      return 'red';
    default:
      return 'slate';
  }
}

// cents -> "$x.xx"。后端余额/奖励以美分整数下发。
export function formatCents(cents: number): string {
  return `$${(cents / 100).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`;
}

// decimal 字符串 USD -> "$x.xx…"。容错非数字时原样回退。
export function formatUsd(value: string): string {
  const n = Number.parseFloat(value);
  if (!Number.isFinite(n)) return value || '$0.00';
  return `$${n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 4 })}`;
}

const WINDOW_KIND_LABEL: Record<string, string> = {
  minute: '每分钟',
  hour: '每小时',
  day: '每日',
  week: '每周',
  month: '每月',
  total: '总额',
};

export function windowKindLabel(kind: string): string {
  return WINDOW_KIND_LABEL[kind] ?? kind;
}

// 已用占上限百分比 (0-100),上限为 0 或非法时返回 0。
export function quotaPercent(consumed: string, cap: string): number {
  const c = Number.parseFloat(consumed);
  const total = Number.parseFloat(cap);
  if (!Number.isFinite(c) || !Number.isFinite(total) || total <= 0) return 0;
  return Math.min(100, Math.max(0, (c / total) * 100));
}
