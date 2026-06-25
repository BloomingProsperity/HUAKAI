import { apiGet, apiSend } from '../../lib/api'
import type {
  CreatePoolRequest,
  PoolGroup,
  PoolListResponse,
  PoolMemberListResponse,
  UpdatePoolRequest,
} from './types'

/*
 * 分组管理(池组)数据访问层。
 * 端点 /admin/v1/pools(admin token 鉴权;路径 /admin/ 前缀由 lib/api 的 tokenForPath
 * 自动注入 admin Bearer,无需手动补 Authorization)。
 *
 * 成员账号经 /admin/v1/provider-accounts?pool_group_id= 只读列出(同 admin token)。
 */
const PATH = '/admin/v1/pools'
const ACCOUNTS_PATH = '/admin/v1/provider-accounts'

/** 列表:GET /admin/v1/pools。platform_admin 需带 tenant_id;tenant_operator 省略走自身 scope。 */
export async function listPools(
  tenantID?: number,
  limit = 200,
  signal?: AbortSignal,
): Promise<PoolListResponse> {
  return apiGet<PoolListResponse>(PATH, {
    query: { tenant_id: tenantID, limit },
    signal,
  })
}

/** 详情:GET /admin/v1/pools/{id}。 */
export async function getPool(id: number, tenantID?: number, signal?: AbortSignal): Promise<PoolGroup> {
  return apiGet<PoolGroup>(`${PATH}/${id}`, { query: { tenant_id: tenantID }, signal })
}

/** 新建:POST /admin/v1/pools。platform_admin 需在 query 带 tenant_id。 */
export async function createPool(body: CreatePoolRequest, tenantID?: number): Promise<PoolGroup> {
  return apiSend<PoolGroup>('POST', PATH, body, { query: { tenant_id: tenantID } })
}

/** 编辑(含启停):PATCH /admin/v1/pools/{id}。 */
export async function updatePool(
  id: number,
  body: UpdatePoolRequest,
  tenantID?: number,
): Promise<PoolGroup> {
  return apiSend<PoolGroup>('PATCH', `${PATH}/${id}`, body, { query: { tenant_id: tenantID } })
}

/**
 * 成员账号(只读):GET /admin/v1/provider-accounts?pool_group_id=。
 * 列出归属该池组的 provider account;本页只展示,不在此增删成员。
 */
export async function listPoolMembers(
  poolGroupID: number,
  tenantID?: number,
  signal?: AbortSignal,
): Promise<PoolMemberListResponse> {
  return apiGet<PoolMemberListResponse>(ACCOUNTS_PATH, {
    query: { pool_group_id: poolGroupID, tenant_id: tenantID, limit: 100 },
    signal,
  })
}
