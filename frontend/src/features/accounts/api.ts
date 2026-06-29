import { apiGet, apiSend } from '../../lib/api'
import { buildAccountListQuery, type AccountListFilters } from './query'
import type {
  AccountHealth,
  AccountTestResult,
  BulkByTagResult,
  ProviderAccount,
  ProviderAccountListResponse,
  UpstreamModelsResult,
} from './types'

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

/**
 * 账号凭证试运行:POST /admin/v1/provider-accounts/{id}/test。
 * 后端做 dry-run 校验(不计费、进审计),回 {ok, error_class, message}。
 * 真码:backend/internal/adminhttp/provider_account_test_handler.go:57。
 */
export async function testProviderAccount(id: number): Promise<AccountTestResult> {
  return apiSend<AccountTestResult>('POST', `${ACCOUNTS_PATH}/${id}/test`)
}

/**
 * 账号实时健康:GET /admin/v1/provider-accounts/{id}/health。
 * 字段严格对齐 handler(health_state / failure_count / session_window_5h_* / recent_requests…)。
 * 真码:backend/internal/adminhttp/provider_account_health_handler.go:67。
 */
export async function getProviderAccountHealth(id: number, signal?: AbortSignal): Promise<AccountHealth> {
  return apiGet<AccountHealth>(`${ACCOUNTS_PATH}/${id}/health`, { signal })
}

/**
 * 上游可用模型:GET /admin/v1/provider-accounts/{id}/upstream-models。
 * 仅 upstream_passthrough(upstream_static)凭证支持;否则后端 422。回 {models, count}。
 * 真码:backend/internal/adminhttp/provider_account_upstream_models_handler.go:68。
 */
export async function getProviderAccountUpstreamModels(id: number): Promise<UpstreamModelsResult> {
  return apiGet<UpstreamModelsResult>(`${ACCOUNTS_PATH}/${id}/upstream-models`)
}

/**
 * 按标签批量调参:POST /admin/v1/provider-accounts/bulk-by-tag。
 * tag 必填;enabled/priority/static_weight 至少一项(只下发非 undefined 的字段),
 * 后端逐条更新并写审计,回 {affected_ids, count}。
 * 真码:backend/internal/adminhttp/provider_account_bulk_handler.go:48。
 */
export async function bulkUpdateAccountsByTag(body: {
  tag: string
  enabled?: boolean
  priority?: number
  static_weight?: number
}): Promise<BulkByTagResult> {
  return apiSend<BulkByTagResult>('POST', `${ACCOUNTS_PATH}/bulk-by-tag`, body)
}
