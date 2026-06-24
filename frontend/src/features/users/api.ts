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
