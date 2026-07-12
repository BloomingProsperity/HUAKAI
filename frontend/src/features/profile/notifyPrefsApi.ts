import { apiGet, apiSend } from '../../lib/api'
import type { NotifyPrefsResponse, NotifyPrefsUpdate } from './notifyPrefsTypes'

/*
 * 通知偏好数据访问层。端点 /v1/users/me/notifications(session 鉴权)。
 * 真码:backend/internal/controlhttp/notify_handler.go:72-73(MountNotifyUserRoutes),
 *       挂载于 backend/cmd/gateway/routes_notifications.go:34 的 SessionMiddleware 组。
 * 路径不在 /v1/auth/* 前缀下,session token 由 lib/api 的 tokenForPath 自动注入,无需显式 bearer。
 * 身份(tenant/user)由后端从 session 上下文派生,前端绝不传用户标识。
 */
const PATH = '/v1/users/me/notifications'

/** 读取本人通知偏好。secret/token 后端只回 *_configured 标志,不回显明文。 */
export function getNotifyPrefs(signal?: AbortSignal): Promise<NotifyPrefsResponse> {
  return apiGet<NotifyPrefsResponse>(PATH, { signal })
}

/** 更新本人通知偏好(read-modify-write;空 secret/token 由调用方剔除,不覆盖现有密钥)。 */
export function updateNotifyPrefs(body: NotifyPrefsUpdate): Promise<NotifyPrefsResponse> {
  return apiSend<NotifyPrefsResponse>('PUT', PATH, body)
}
