// 兑换码（voucher）用户面 API 封装。
// 端点形状以 HUAKAI 后端真码为准：
//   - POST /v1/users/me/vouchers/redeem  (gatewayhttp/voucher_handler.go: newVoucherRedeemHandler)
//   - GET  /v1/me/voucher-redemptions    (voucherhttp/redemption_history.go: NewRedemptionHistoryHandler)
// 二者均走 session 鉴权（SessionMiddleware，tenant/user 从会话身份注入，不取自请求体）。
// 借鉴：sub2api src/api/redeem.ts 提供「redeem + history」两段式 API 与「到账面额/类型」回显形态；
//       字段集合则完全对齐 HUAKAI 后端的 RedeemResult / redemptionView，不照搬上游字段名。
import { userGet, userPost } from './userClient';

// ---- 兑换 POST 请求 / 响应 ----

// 后端只接受 {code, idempotency_key?}（voucherRedeemRequest）。多余字段会被忽略，
// tenant/user 由 session 注入，前端绝不上送。
export interface RedeemRequest {
  code: string;
  idempotency_key?: string;
}

// 与 voucher.Voucher（types.go）对齐，仅保留前端展示需要的字段。
export interface VoucherSummary {
  id: number;
  amount_cents: number;
  currency_code: string;
  grant_kind: string; // "balance" | "subscription"
  status: string; // active / expired / exhausted / revoked
}

// 与 voucher.Redemption（types.go）对齐，仅取展示字段。
export interface RedemptionSummary {
  voucher_id: number;
  amount_cents: number;
  currency_code: string;
  status?: string;
  redeemed_at: string;
}

// 订阅券授予摘要（voucher.SubscriptionGrant）；余额券为 undefined。
export interface SubscriptionGrant {
  user_subscription_id: number;
  plan_id: number;
  result_kind: string; // created / renewed
  new_expires_at: string;
  applied_validity_days: number;
}

// 与 voucher.RedeemResult（types.go）对齐。
export interface RedeemResult {
  voucher: VoucherSummary;
  redemption: RedemptionSummary;
  balance_cents: number; // 兑换后账户余额（订阅券不计入，为 0）
  idempotent: boolean; // 幂等命中（重复兑换同一码）时为 true
  subscription?: SubscriptionGrant;
}

// ---- 兑换历史 GET ----

// 与 voucherhttp.redemptionView 对齐（注意历史接口不返回 grant_kind / voucher 面额种类）。
export interface RedemptionHistoryItem {
  voucher_id: number;
  amount_cents: number;
  currency_code: string;
  status: string; // 来自 voucher.Redemption.Status
  redeemed_at: string;
  billing_event_id: number; // 余额券有值；订阅券为 0
}

interface RedemptionHistoryResponse {
  redemptions: RedemptionHistoryItem[] | null;
}

// 兑换一张码。成功返回到账面额 / 类型 / 兑换后余额。
export function redeemVoucher(body: RedeemRequest): Promise<RedeemResult> {
  return userPost<RedeemResult>('/v1/users/me/vouchers/redeem', body);
}

// 拉取当前用户的兑换历史（最新在前由后端决定）。limit 1..200，缺省 50。
export async function fetchRedemptionHistory(limit = 50): Promise<RedemptionHistoryItem[]> {
  const resp = await userGet<RedemptionHistoryResponse>('/v1/me/voucher-redemptions', { limit });
  return resp.redemptions ?? [];
}

// ---- 展示辅助 ----

// 后端金额一律以「分」（cents）存储，展示前换算成主币种单位。
export function formatAmount(amountCents: number, currencyCode: string): string {
  const value = (amountCents / 100).toLocaleString('zh-CN', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  });
  const code = currencyCode ? currencyCode.toUpperCase() : '';
  return code ? `${value} ${code}` : value;
}

// grant_kind -> 中文类型标签。
export function grantKindLabel(kind: string): string {
  switch (kind) {
    case 'subscription':
      return '订阅券';
    case 'balance':
      return '余额券';
    default:
      return kind || '余额券';
  }
}
