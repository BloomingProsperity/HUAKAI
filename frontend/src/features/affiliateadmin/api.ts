import { apiGet } from '../../lib/api'
import {
  buildOverviewQuery,
  buildReferralQuery,
  buildRewardsQuery,
} from './affiliateadmin'
import type {
  AdminReferralListResponse,
  AdminReferralOverview,
  AdminReferralRewardsResponse,
  AffiliateFilters,
} from './types'

/*
 * 分销管理(运营台)数据访问层。全部只读、admin token 鉴权
 * (lib/api.ts → tokenForPath 对 /v1/admin/* 自动注入 admin token)。
 * 路径挂在 routes.go:1054-1062。涉及钱(money-gated):仅 GET 查询,绝不写。
 */

/** GET /v1/admin/referrals:运营台分销记录列表(分页 + 状态/租户筛选)。routes.go:1054 */
export async function listAdminReferrals(
  filters: AffiliateFilters,
  limit: number,
  offset: number,
  signal?: AbortSignal,
): Promise<AdminReferralListResponse> {
  return apiGet<AdminReferralListResponse>('/v1/admin/referrals', {
    query: buildReferralQuery(filters, limit, offset),
    signal,
  })
}

/** GET /v1/admin/referrals/rewards:返利账本(分页,带累计 total_reward_usd)。routes.go:1058 */
export async function listAdminReferralRewards(
  filters: AffiliateFilters,
  limit: number,
  offset: number,
  referrerUserId?: string,
  signal?: AbortSignal,
): Promise<AdminReferralRewardsResponse> {
  return apiGet<AdminReferralRewardsResponse>('/v1/admin/referrals/rewards', {
    query: buildRewardsQuery(filters, limit, offset, referrerUserId),
    signal,
  })
}

/** GET /v1/admin/referrals/overview:分销概览(各状态计数 + 累计返利 + 笔数)。routes.go:1062 */
export async function getAdminReferralOverview(
  filters: AffiliateFilters,
  signal?: AbortSignal,
): Promise<AdminReferralOverview> {
  return apiGet<AdminReferralOverview>('/v1/admin/referrals/overview', {
    query: buildOverviewQuery(filters),
    signal,
  })
}
