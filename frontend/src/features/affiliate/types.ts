/*
 * 推广(邀请返利)前端类型 —— 镜像后端 JSON 形态,字段名严格对齐真码 json tag。
 *
 * 端点(均 session 鉴权,挂在 /v1/me 组下):
 *   GET /v1/me/invitation-code        gatewayhttp.myReferralCodeResponse
 *   GET /v1/me/referrals              referralhttp.referralListResponse
 *   GET /v1/me/referrals/rewards      referralhttp.rewardLedgerResponse
 *   GET /v1/me/invitations            gatewayhttp.invitationSummaryResponse(累计计数/返利分)
 *
 * 注:total_reward_usd / amount_usd 为 shopspring decimal,JSON 编码为字符串(非数字),
 *     故这里统一声明 string,避免精度丢失。
 */

/** GET /v1/me/invitation-code:用户专属、稳定的邀请码。 */
export interface MyInvitationCode {
  code: string
  inviter_user_id: number
}

/**
 * POST /v1/invitations 请求体(gatewayhttp.invitationCreateRequest)。
 * 三字段均可选(后端有默认值);此处只送数值两项,不暴露 client_idempotency_key
 * 给用户(保留前缀由服务端管控,前端无须也不应让用户自填)。
 */
export interface MintInvitationRequest {
  max_usage?: number
  expires_in_days?: number
}

/**
 * POST /v1/invitations 响应(gatewayhttp.invitationCreateResponse)。
 * 注意:此响应不含任何 secret —— code 是可分享的邀请码(非凭证),其余皆元数据。
 * expires_at 为 RFC3339 时间字符串。
 */
export interface MintInvitationResponse {
  code: string
  inviter_user_id: number
  expires_at: string
  max_usage: number
}

/** GET /v1/me/invitations:邀请汇总(只读计数)。 */
export interface InvitationSummary {
  qualified_count: number
  rewarded_count: number
  /** 累计返利金额,单位:分(cents)。 */
  rewards_earned_cents: number
}

/** 被邀请人列表项(userReferralItem)。status ∈ pending|qualified|rewarded|rejected。 */
export interface ReferralItem {
  referral_id: number
  referee_user_id: number
  status: string
  created_at: string
  rewarded_at?: string | null
}

export interface ReferralListResponse {
  object: string
  items: ReferralItem[]
  total: number
  limit: number
  offset: number
}

/** 返利流水项(rewardLedgerItem)。amount_usd 为 decimal 字符串。 */
export interface RewardLedgerItem {
  referral_id: number
  reward_type: string
  amount_usd: string
  created_at: string
}

export interface RewardLedgerResponse {
  object: string
  items: RewardLedgerItem[]
  total: number
  /** 累计返利(USD,decimal 字符串)。 */
  total_reward_usd: string
  limit: number
  offset: number
}
