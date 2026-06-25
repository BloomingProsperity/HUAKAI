import { apiGet } from '../../lib/api'
import type {
  InvitationSummary,
  MyInvitationCode,
  ReferralListResponse,
  RewardLedgerResponse,
} from './types'

/*
 * 推广(邀请返利)数据访问层。全部 session 鉴权,只读。
 * 路径挂在 /v1/me 组下(backend/cmd/gateway/routes.go:184 起)。
 */

/** GET /v1/me/invitation-code:取(或惰性生成)当前用户专属邀请码。 */
export async function getMyInvitationCode(signal?: AbortSignal): Promise<MyInvitationCode> {
  return apiGet<MyInvitationCode>('/v1/me/invitation-code', { signal })
}

/** GET /v1/me/invitations:邀请汇总(合格/已返利计数 + 累计返利分)。 */
export async function getInvitationSummary(signal?: AbortSignal): Promise<InvitationSummary> {
  return apiGet<InvitationSummary>('/v1/me/invitations', { signal })
}

/** GET /v1/me/referrals:被邀请人列表(分页)。 */
export async function listReferrals(offset = 0, limit = 20, signal?: AbortSignal): Promise<ReferralListResponse> {
  return apiGet<ReferralListResponse>('/v1/me/referrals', { query: { offset, limit }, signal })
}

/** GET /v1/me/referrals/rewards:返利流水(分页,带累计 total_reward_usd)。 */
export async function listReferralRewards(offset = 0, limit = 20, signal?: AbortSignal): Promise<RewardLedgerResponse> {
  return apiGet<RewardLedgerResponse>('/v1/me/referrals/rewards', { query: { offset, limit }, signal })
}
