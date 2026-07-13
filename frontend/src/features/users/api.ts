import { apiGet, apiSend } from '../../lib/api'
import type {
  AdminNotifyResponse,
  AdminNotifyUpdate,
  AdminUser,
  BalanceAdjustmentRequest,
  BalanceAdjustmentResult,
  CreateUserRequest,
  UserListResponse,
} from './types'

/*
 * 用户管理数据访问层。端点 /admin/v1/users(admin token 鉴权)。
 */
const PATH = '/admin/v1/users'

export async function listUsers(
  tenantId: number,
  q: string,
  offset = 0,
  limit = 50,
  signal?: AbortSignal,
): Promise<UserListResponse> {
  return apiGet<UserListResponse>(PATH, {
    query: { tenant_id: tenantId, q: q.trim() || undefined, offset, limit },
    signal,
  })
}

export async function createUser(tenantId: number, body: CreateUserRequest): Promise<AdminUser> {
  return apiSend<AdminUser>('POST', PATH, body, { query: { tenant_id: tenantId } })
}

/** 设用户状态(active/disabled 等):PUT /{id}/status {status}。 */
export async function setUserStatus(tenantId: number, id: number, status: string): Promise<unknown> {
  return apiSend<unknown>('PUT', `${PATH}/${id}/status`, { status }, { query: { tenant_id: tenantId } })
}

/** 解锁(清登录失败锁):POST /{id}/unlock。 */
export async function unlockUser(tenantId: number, id: number): Promise<unknown> {
  return apiSend<unknown>('POST', `${PATH}/${id}/unlock`, {}, { query: { tenant_id: tenantId } })
}

/** 用户详情:GET /admin/v1/users/{id}。 */
export async function getUser(tenantId: number, id: number, signal?: AbortSignal): Promise<import('./detail').UserDetail> {
  return apiGet<import('./detail').UserDetail>(`${PATH}/${id}`, { query: { tenant_id: tenantId }, signal })
}

/** 余额历史(只读台账):GET /admin/v1/users/{id}/balance-history。 */
export async function getBalanceHistory(
  tenantId: number,
  id: number,
  offset = 0,
  limit = 50,
  signal?: AbortSignal,
): Promise<import('./detail').BalanceHistoryResponse> {
  return apiGet<import('./detail').BalanceHistoryResponse>(`${PATH}/${id}/balance-history`, {
    query: { tenant_id: tenantId, offset, limit },
    signal,
  })
}

/** 用户用量明细：GET /admin/v1/users/{id}/usage，卡片聚合当前批次。 */
export async function getUserUsage(
  tenantId: number,
  id: number,
  limit = 200,
  signal?: AbortSignal,
): Promise<import('./detail').UserUsageResponse> {
  return apiGet<import('./detail').UserUsageResponse>(`${PATH}/${id}/usage`, {
    query: { tenant_id: tenantId, limit },
    signal,
  })
}

// ── 用户运维动作(adminuserhttp.MountRoutes,均含审计 + 租户隔离) ──────────────

/** 强制清除用户 TOTP 2FA:POST /{id}/2fa/force-disable。返回 {id, two_factor_enabled:false}。 */
export async function forceDisable2FA(tenantId: number, id: number): Promise<unknown> {
  return apiSend<unknown>('POST', `${PATH}/${id}/2fa/force-disable`, {}, { query: { tenant_id: tenantId } })
}

/** 清空用户全部通行密钥(passkey):DELETE /{id}/passkeys。返回 {id, cleared:<n>}。 */
export async function resetPasskeys(tenantId: number, id: number): Promise<{ id: number; cleared: number }> {
  return apiSend<{ id: number; cleared: number }>('DELETE', `${PATH}/${id}/passkeys`, undefined, { query: { tenant_id: tenantId } })
}

/** 设用户组(路由权益/计费倍率随组变):PUT /{id}/group {group}。 */
export async function setUserGroup(tenantId: number, id: number, group: string): Promise<unknown> {
  return apiSend<unknown>('PUT', `${PATH}/${id}/group`, { group }, { query: { tenant_id: tenantId } })
}

/** 设用户备注:PUT /{id}/remark {remark}。 */
export async function setUserRemark(tenantId: number, id: number, remark: string): Promise<unknown> {
  return apiSend<unknown>('PUT', `${PATH}/${id}/remark`, { remark }, { query: { tenant_id: tenantId } })
}

/** 软删用户(deleted_at=now,撤会话):DELETE /{id}。返回 {id, deleted:true}。 */
export async function softDeleteUser(tenantId: number, id: number): Promise<unknown> {
  return apiSend<unknown>('DELETE', `${PATH}/${id}`, undefined, { query: { tenant_id: tenantId } })
}

/** 解绑某社交登录绑定:DELETE /{id}/account-bindings/{provider}。返回 {unlinked:<n>}。 */
export async function unlinkSocialIdentity(tenantId: number, id: number, provider: string): Promise<{ unlinked: number }> {
  return apiSend<{ unlinked: number }>('DELETE', `${PATH}/${id}/account-bindings/${encodeURIComponent(provider)}`, undefined, { query: { tenant_id: tenantId } })
}

/** 2FA 普及率统计(只读):GET /admin/v1/users/2fa-adoption-stats。 */
export async function getTwoFAAdoptionStats(tenantId: number, signal?: AbortSignal): Promise<import('./actions').TwoFAAdoptionStats> {
  return apiGet<import('./actions').TwoFAAdoptionStats>(`${PATH}/2fa-adoption-stats`, { query: { tenant_id: tenantId }, signal })
}

/**
 * 管理员手动调额(money 敏感):POST /admin/v1/balances/adjustments。
 * 该端点挂在 /admin/v1/balances 前缀(routes.go:1025),由 adminhttp.MountBalanceCreditRoutes
 * 的 r.Post("/adjustments") 拼成全路径(balance_credit_handler.go:34)。仅 platform_admin 可调
 * (handler.go:66)。amount 符号即方向;返回入账后净余额。注:走 /admin/v1 前缀,tokenForPath
 * 自动注入 admin Bearer,不手动设 token。
 */
export async function adjustBalance(body: BalanceAdjustmentRequest): Promise<BalanceAdjustmentResult> {
  return apiSend<BalanceAdjustmentResult>('POST', '/admin/v1/balances/adjustments', body)
}

// ── 通知偏好(管理员代管)──────────────────────────────────────────────────────
// 端点 /v1/admin/users/{user_id}/notifications(controlhttp notifyAdminHandler,admin token 鉴权)。
// 真码:backend/internal/controlhttp/notify_handler.go:78-79(MountNotifyAdminRoutes),
//       挂载于 backend/cmd/gateway/routes_notifications.go:37(NotifyAdminDeps)。
// 路径以 /v1/admin 前缀打头,lib/api 的 tokenForPath 自动注入 admin Bearer,无需显式 bearer。
// 目标用户身份走 path 参数 {user_id};目标租户走 ?tenant_id=(notifyAdminTarget,notify_handler.go:194):
// platform_admin 必填,tenant_operator 省略则回落自身 scope。

/** 读取某用户的通知偏好(代管)。secret/token 后端只回 *_configured 标志,不回显明文。 */
export async function getAdminUserNotify(
  userId: number,
  tenantId?: number,
  signal?: AbortSignal,
): Promise<AdminNotifyResponse> {
  return apiGet<AdminNotifyResponse>(`/v1/admin/users/${userId}/notifications`, {
    query: { tenant_id: tenantId && tenantId > 0 ? tenantId : undefined },
    signal,
  })
}

/** 保存某用户的通知偏好(代管)。空 secret/token 由调用方剔除,但留空=后端清除现有密钥(见 store.go:209/213)。 */
export async function putAdminUserNotify(
  userId: number,
  body: AdminNotifyUpdate,
  tenantId?: number,
): Promise<AdminNotifyResponse> {
  return apiSend<AdminNotifyResponse>('PUT', `/v1/admin/users/${userId}/notifications`, body, {
    query: { tenant_id: tenantId && tenantId > 0 ? tenantId : undefined },
  })
}
