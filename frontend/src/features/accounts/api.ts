import { apiGet, apiSend } from '../../lib/api'
import { buildAccountListQuery, type AccountListFilters } from './query'
import type {
  AccountHealth,
  AccountTestResult,
  BulkByTagResult,
  DeleteAccountResult,
  FingerprintBindResult,
  FingerprintProfileOption,
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
 * 硬删账号:DELETE /admin/v1/provider-accounts/{id}。这是不可逆的删除操作,
 * 与运维动作里的「停用账号」(PATCH /{id}/enabled,可恢复软停)语义截然不同。
 * 后端做 SoftDeleteProviderAccount(从可调度池移除并写审计),账号从此不再出现在池中。
 *
 * 请求体:复用后端 mutateProviderAccountRequest {tenant_id?, enabled?, reason?}。
 * tenant_id 由后端从鉴权 scope 推导(此处不传,避免与 scope 冲突);reason 进 admin 审计,
 * 为空则不下发(后端默认中文文案「删除 provider account」)。
 * 响应:{id, deleted:true}(handler:695)。
 * 真码:backend/internal/gatewayhttp/admin_pool_accounts_handler.go:665
 *      (newDeleteProviderAccountHandler)+ :172(MountAdminPoolAccountRoutes 挂 DELETE /{id})。
 */
export async function deleteProviderAccount(id: number, reason: string): Promise<DeleteAccountResult> {
  const body: { reason?: string } = {}
  const trimmed = reason.trim()
  if (trimmed) body.reason = trimmed
  return apiSend<DeleteAccountResult>('DELETE', `${ACCOUNTS_PATH}/${id}`, body)
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

/*
 * ---- 账号 TLS 指纹 profile 绑定/解绑(出口拟真,非 money)----
 * 后端独立包 accountfphttp:
 *   PATCH /admin/v1/provider-accounts/{id}/fingerprint-profile
 *   真码:backend/internal/accountfphttp/fingerprint_handler.go:48(MountRoutes)
 *        + cmd/gateway/routes.go:940(accountfphttp.MountRoutes)+:975(组挂载)。
 * 请求体 {profile_id:int64|null}:正整数=绑定该 profile;null=解绑(回内置默认)。
 * 后端从鉴权 scope 推导 tenant_id;body 若带 tenant_id 必须与 scope 一致(此处不传,交后端推)。
 * 响应:{id, tls_fingerprint_profile_id:int64|null}(fingerprint_handler.go:117)。
 * profile 不存在 / 跨租户 → 400 invalid_fingerprint_profile(FK 23503 / 触发器 P0001)。
 */
export async function setAccountFingerprintProfile(
  id: number,
  profileId: number | null,
  reason?: string,
): Promise<FingerprintBindResult> {
  // profile_id 必须显式出现(含 null):后端 setFingerprintRequest.ProfileID 是 *int64,
  // 缺省与显式 null 不可混淆——null 表示解绑。reason 为空则不下发(后端默认中文文案)。
  const body: { profile_id: number | null; reason?: string } = { profile_id: profileId }
  const trimmed = reason?.trim()
  if (trimmed) body.reason = trimmed
  return apiSend<FingerprintBindResult>('PATCH', `${ACCOUNTS_PATH}/${id}/fingerprint-profile`, body)
}

/*
 * 列某租户可绑定的 TLS 指纹 profile(供绑定下拉用)。
 * 后端 tlsfphttp:GET /v1/admin/tls-fingerprint-profiles?tenant_id=N
 *   真码:backend/internal/tlsfphttp/handler.go:96(listHandler)+ cmd/gateway/routes.go:1104。
 * 这里只取下拉需要的轻量字段(id/name/status),不复用 tlsfp 包的全量 DTO(避免跨切片耦合)。
 * platform_admin 角色下 tenant_id 必填且须为正整数(handler.go:101)。
 */
export async function listFingerprintProfileOptions(
  tenantId: number,
  signal?: AbortSignal,
): Promise<FingerprintProfileOption[]> {
  const resp = await apiGet<{ object: string; items: FingerprintProfileOption[] }>(
    '/v1/admin/tls-fingerprint-profiles',
    { query: { tenant_id: tenantId }, signal },
  )
  return resp.items ?? []
}
