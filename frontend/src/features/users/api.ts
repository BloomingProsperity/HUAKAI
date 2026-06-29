import { apiGet, apiSend } from '../../lib/api'
import type { AdminUser, CreateUserRequest, UserListResponse } from './types'

/*
 * 用户管理数据访问层。端点 /admin/v1/users(admin token 鉴权)。
 */
const PATH = '/admin/v1/users'

export async function listUsers(
  q: string,
  offset = 0,
  limit = 50,
  signal?: AbortSignal,
): Promise<UserListResponse> {
  return apiGet<UserListResponse>(PATH, {
    query: { q: q.trim() || undefined, offset, limit },
    signal,
  })
}

export async function createUser(body: CreateUserRequest): Promise<AdminUser> {
  return apiSend<AdminUser>('POST', PATH, body)
}

/** 设用户状态(active/disabled 等):PUT /{id}/status {status}。 */
export async function setUserStatus(id: number, status: string): Promise<unknown> {
  return apiSend<unknown>('PUT', `${PATH}/${id}/status`, { status })
}

/** 解锁(清登录失败锁):POST /{id}/unlock。 */
export async function unlockUser(id: number): Promise<unknown> {
  return apiSend<unknown>('POST', `${PATH}/${id}/unlock`, {})
}

/** 用户详情:GET /admin/v1/users/{id}。 */
export async function getUser(id: number, signal?: AbortSignal): Promise<import('./detail').UserDetail> {
  return apiGet<import('./detail').UserDetail>(`${PATH}/${id}`, { signal })
}

/** 余额历史(只读台账):GET /admin/v1/users/{id}/balance-history。 */
export async function getBalanceHistory(
  id: number,
  offset = 0,
  limit = 50,
  signal?: AbortSignal,
): Promise<import('./detail').BalanceHistoryResponse> {
  return apiGet<import('./detail').BalanceHistoryResponse>(`${PATH}/${id}/balance-history`, {
    query: { offset, limit },
    signal,
  })
}

// ── 用户运维动作(adminuserhttp.MountRoutes,均含审计 + 租户隔离) ──────────────

/** 强制清除用户 TOTP 2FA:POST /{id}/2fa/force-disable。返回 {id, two_factor_enabled:false}。 */
export async function forceDisable2FA(id: number): Promise<unknown> {
  return apiSend<unknown>('POST', `${PATH}/${id}/2fa/force-disable`, {})
}

/** 清空用户全部通行密钥(passkey):DELETE /{id}/passkeys。返回 {id, cleared:<n>}。 */
export async function resetPasskeys(id: number): Promise<{ id: number; cleared: number }> {
  return apiSend<{ id: number; cleared: number }>('DELETE', `${PATH}/${id}/passkeys`)
}

/** 设用户组(路由权益/计费倍率随组变):PUT /{id}/group {group}。 */
export async function setUserGroup(id: number, group: string): Promise<unknown> {
  return apiSend<unknown>('PUT', `${PATH}/${id}/group`, { group })
}

/** 设用户备注:PUT /{id}/remark {remark}。 */
export async function setUserRemark(id: number, remark: string): Promise<unknown> {
  return apiSend<unknown>('PUT', `${PATH}/${id}/remark`, { remark })
}

/** 软删用户(deleted_at=now,撤会话):DELETE /{id}。返回 {id, deleted:true}。 */
export async function softDeleteUser(id: number): Promise<unknown> {
  return apiSend<unknown>('DELETE', `${PATH}/${id}`)
}

/** 解绑某社交登录绑定:DELETE /{id}/account-bindings/{provider}。返回 {unlinked:<n>}。 */
export async function unlinkSocialIdentity(id: number, provider: string): Promise<{ unlinked: number }> {
  return apiSend<{ unlinked: number }>('DELETE', `${PATH}/${id}/account-bindings/${encodeURIComponent(provider)}`)
}

/** 2FA 普及率统计(只读):GET /admin/v1/users/2fa-adoption-stats。 */
export async function getTwoFAAdoptionStats(signal?: AbortSignal): Promise<import('./actions').TwoFAAdoptionStats> {
  return apiGet<import('./actions').TwoFAAdoptionStats>(`${PATH}/2fa-adoption-stats`, { signal })
}
