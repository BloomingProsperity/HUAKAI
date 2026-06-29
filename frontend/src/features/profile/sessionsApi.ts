import { apiSend } from '../../lib/api'
import type { SessionListResponse, SessionRevokeResponse } from './sessionsTypes'

/*
 * 活跃会话数据访问层。端点 /v1/sessions/*(session 鉴权,身份后端从会话派生)。
 * 真码:backend/internal/gatewayhttp/session_handler.go:46-47,挂载 backend/cmd/gateway/routes.go:281。
 * 注意:这两个端点是 POST(list 用 POST 是后端约定,见 MountSessionProtectedRoutes)。
 * tokenForPath 对 /v1/sessions/* 返回 session token(自动注入);lib/api 对该前缀刻意跳过主动刷新,
 * 不影响 token 携带。撤销的归属(family 属当前用户)由后端 sessionFamilyBelongsToCurrentUser 强校验。
 */

/** 列出当前用户的全部登录设备族。 */
export function listSessions(signal?: AbortSignal): Promise<SessionListResponse> {
  return apiSend<SessionListResponse>('POST', '/v1/sessions/list', {}, { signal })
}

/**
 * 撤销指定设备族(强制登出该设备/谱系)。reason 可选,审计用。
 * 破坏性动作:调用方须二次确认。撤销当前会话所在的族会把自己登出。
 */
export function revokeSessionFamily(familyId: string, reason?: string): Promise<SessionRevokeResponse> {
  return apiSend<SessionRevokeResponse>('POST', '/v1/sessions/revoke', {
    family_id: familyId,
    reason: reason ?? 'user_self_revoke',
  })
}
