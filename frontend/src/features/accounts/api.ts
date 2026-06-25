import { apiGet, apiSend } from '../../lib/api'
import { buildAccountListQuery, type AccountListFilters } from './query'
import type { ProviderAccount, ProviderAccountListResponse } from './types'

/*
 * 账号中心数据访问层。封装对 admin provider-accounts 端点的调用,页面只依赖本文件。
 * 端点:GET/PATCH/POST /admin/v1/provider-accounts*(admin 鉴权,session + 租户 scope)。
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

/** 取单个账号详情:GET /admin/v1/provider-accounts/{id}。 */
export async function getProviderAccount(id: number, signal?: AbortSignal): Promise<ProviderAccount> {
  return apiGet<ProviderAccount>(`${ACCOUNTS_PATH}/${id}`, { signal })
}

/**
 * 启用/停用账号:PATCH /admin/v1/provider-accounts/{id}/enabled。reason 进审计。
 * 注:该端点只回精简 {id, enabled}(非完整账号 DTO),不能直接拿来替换详情页的 account
 * (否则 tags/model_allow_list/capability_flags 等字段丢失,渲染读 .length 会崩)。
 * 故启停成功后重新拉取完整账号返回,保证调用方拿到完整 DTO。
 */
export async function setAccountEnabled(id: number, enabled: boolean, reason: string): Promise<ProviderAccount> {
  await apiSend<{ id: number; enabled: boolean }>('PATCH', `${ACCOUNTS_PATH}/${id}/enabled`, {
    enabled,
    reason: reason.trim() || undefined,
  })
  return getProviderAccount(id)
}

/** 清除账号限流态:POST /admin/v1/provider-accounts/{id}/clear-rate-limit。reason 进审计。 */
export async function clearAccountRateLimit(id: number, reason: string): Promise<ProviderAccount> {
  return apiSend<ProviderAccount>('POST', `${ACCOUNTS_PATH}/${id}/clear-rate-limit`, {
    reason: reason.trim() || undefined,
  })
}

/** 通用编辑账号(池调优旋钮):PATCH /admin/v1/provider-accounts/{id}。仅下发改动字段。 */
export async function updateProviderAccount(id: number, body: object): Promise<ProviderAccount> {
  return apiSend<ProviderAccount>('PATCH', `${ACCOUNTS_PATH}/${id}`, body)
}
