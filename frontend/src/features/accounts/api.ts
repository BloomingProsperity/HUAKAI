import { apiGet } from '../../lib/api'
import { buildAccountListQuery, type AccountListFilters } from './query'
import type { ProviderAccountListResponse } from './types'

/*
 * 账号中心数据访问层。封装对 admin provider-accounts 端点的调用,页面只依赖本文件。
 * 端点:GET /admin/v1/provider-accounts(admin 鉴权,session + 租户 scope)。
 */
const ACCOUNTS_PATH = '/admin/v1/provider-accounts'

export async function listProviderAccounts(
  filters: AccountListFilters,
  signal?: AbortSignal,
): Promise<ProviderAccountListResponse> {
  return apiGet<ProviderAccountListResponse>(ACCOUNTS_PATH, {
    query: buildAccountListQuery(filters),
    signal,
  })
}
