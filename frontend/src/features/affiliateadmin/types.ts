/*
 * 分销管理(运营台)前端类型 —— 严格镜像后端 referralhttp JSON 形态,字段名对齐真码 json tag。
 *
 * 端点(均 admin token 鉴权,tokenForPath 对 /v1/admin/* 自动注入 admin token):
 *   GET /v1/admin/referrals          referralhttp.adminReferralListResponse   (routes.go:1054)
 *   GET /v1/admin/referrals/rewards  referralhttp.adminReferralRewardsResponse (routes.go:1058)
 *   GET /v1/admin/referrals/overview referralhttp.overviewResponse            (routes.go:1062)
 *
 * 鉴权细节(referralhttp.resolveAdminTenant):
 *   - platform_admin 必须显式带 tenant_id(否则 400 invalid_request)。
 *   - tenant_operator 锁死自身 scope;带的 tenant_id 必须等于其 scope,否则 403。
 * 故前端把 tenant_id 做成可选筛选;留空时 platform_admin 会收到 400,UI 提示补租户号。
 *
 * 金额:total_reward_usd / amount_usd 为 shopspring decimal,JSON 编码为字符串(非数字),
 *       统一声明 string,避免 JS number 精度丢失。涉及钱(money-gated):纯展示、绝不计算改写。
 */

/** 分销状态。对齐后端 invitation.ValidReferralStatus(referral_records.go:15-18)。 */
export type ReferralStatus = 'pending' | 'qualified' | 'rewarded' | 'rejected'

/** 运营台分销记录项(referralhttp.adminReferralItem)。 */
export interface AdminReferralItem {
  id: number
  referrer_user_id: number
  referee_user_id: number
  status: string
  created_at: string
}

/** GET /v1/admin/referrals 响应(referralhttp.adminReferralListResponse)。 */
export interface AdminReferralListResponse {
  object: string
  items: AdminReferralItem[]
  total: number
  limit: number
  offset: number
}

/** 运营台返利账本项(referralhttp.adminReferralRewardItem)。amount_usd 为 decimal 字符串。 */
export interface AdminReferralRewardItem {
  id: number
  referral_id: number
  referrer_user_id: number
  reward_type: string
  amount_usd: string
  issued_at: string
}

/** GET /v1/admin/referrals/rewards 响应(referralhttp.adminReferralRewardsResponse)。 */
export interface AdminReferralRewardsResponse {
  object: string
  items: AdminReferralRewardItem[]
  total: number
  /** 当前筛选范围累计返利(USD,decimal 字符串)。 */
  total_reward_usd: string
  limit: number
  offset: number
}

/** GET /v1/admin/referrals/overview 响应(referralhttp.overviewResponse)。 */
export interface AdminReferralOverview {
  object: string
  /** 各状态计数。键 ∈ pending|qualified|rewarded|rejected。 */
  counts_by_status: Record<string, number>
  /** 累计返利(USD,decimal 字符串)。 */
  total_reward_usd: string
  /** 已发放返利笔数。 */
  reward_count: number
}

/** 列表筛选条件(草稿态)。空串=不下发该 query。 */
export interface AffiliateFilters {
  /** 租户号(platform_admin 必填;tenant_operator 留空走自身 scope)。 */
  tenantId: string
  /** 分销状态过滤(仅列表用)。空=全部。 */
  status: string
}

export const EMPTY_AFFILIATE_FILTERS: AffiliateFilters = {
  tenantId: '',
  status: '',
}
