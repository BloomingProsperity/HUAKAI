import { apiGet } from '../../lib/api'
import type { MeGroupListResponse } from './types'

/*
 * 我的分组与倍率数据访问层。端点 GET /v1/me/groups(session 鉴权,tokenForPath 走 session token)。
 * 身份由后端从会话上下文派生(handler.go:78 Resolve→SessionFromContext),前端不传任何用户标识,杜绝越权(CMB-5)。
 * 无 query 参数。真码:backend/internal/megroupshttp/handler.go:66、backend/cmd/gateway/routes.go:208。
 */

/** 取当前用户可达的模型分组 + 倍率。 */
export async function getMyGroups(signal?: AbortSignal): Promise<MeGroupListResponse> {
  return apiGet<MeGroupListResponse>('/v1/me/groups', { signal })
}
