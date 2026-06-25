import { apiGet, apiSend } from '../../lib/api'
import type {
  CacheOverride,
  CacheOverrideListResponse,
  CacheOverrideScope,
  PricingRatio,
  PricingRatioListResponse,
  RatioAuditVerifyResponse,
  SetCacheOverrideRequest,
  UpsertRatioRequest,
} from './types'

/*
 * 模型定价设置数据访问层。两套运维端点(均走 admin token,由 lib/api 按路径前缀自动注入):
 *  - 分组倍率   /admin/v1/pricing/ratios        (mountPricingCatalogRoutes → routes_pricing.go)
 *  - 缓存价覆盖 /v1/admin/cache-price-overrides  (MountCacheOverrideAdminRoutes → routes.go:1041)
 * 写动作(PUT/DELETE)money-gated:后端要求 RolePlatformAdmin,直接动计费倍率,谨慎调用。
 */
const RATIO_PATH = '/admin/v1/pricing/ratios'
const CACHE_OVERRIDE_PATH = '/v1/admin/cache-price-overrides'

// --- 分组倍率 ---

/** 列出某租户的分组倍率。tenant_id 必填(平台管理员视角需显式指定租户)。 */
export async function listPricingRatios(
  tenantId: number,
  offset = 0,
  limit = 100,
  signal?: AbortSignal,
): Promise<PricingRatioListResponse> {
  return apiGet<PricingRatioListResponse>(RATIO_PATH, {
    query: { tenant_id: tenantId, offset, limit },
    signal,
  })
}

/** 写入/更新某组倍率(money-gated:仅 platform_admin)。 */
export async function upsertPricingRatio(
  tenantId: number,
  poolGroupId: number,
  body: UpsertRatioRequest,
): Promise<PricingRatio> {
  return apiSend<PricingRatio>('PUT', `${RATIO_PATH}/${poolGroupId}`, body, {
    query: { tenant_id: tenantId },
  })
}

/** 删除某组倍率(回到默认;money-gated)。 */
export async function deletePricingRatio(
  tenantId: number,
  poolGroupId: number,
): Promise<unknown> {
  return apiSend<unknown>('DELETE', `${RATIO_PATH}/${poolGroupId}`, undefined, {
    query: { tenant_id: tenantId },
  })
}

/** 倍率审计哈希链完整性证明(只读)。 */
export async function verifyRatioAudit(
  tenantId: number,
  signal?: AbortSignal,
): Promise<RatioAuditVerifyResponse> {
  return apiGet<RatioAuditVerifyResponse>(`${RATIO_PATH}/audit/verify`, {
    query: { tenant_id: tenantId },
    signal,
  })
}

// --- 缓存价覆盖 ---

/** 列出所有缓存价覆盖(未列出的 scope 走官方价)。 */
export async function listCacheOverrides(signal?: AbortSignal): Promise<CacheOverrideListResponse> {
  return apiGet<CacheOverrideListResponse>(CACHE_OVERRIDE_PATH, { signal })
}

/**
 * 设置某 scope 缓存价倍率(money-gated)。model scope 需 model;tenant scope 需 tenantId。
 * 这两个限定值走 query(对齐后端 parseCacheOverrideKey)。
 */
export async function setCacheOverride(
  scope: CacheOverrideScope,
  body: SetCacheOverrideRequest,
  qualifier?: { model?: string; tenantId?: number },
): Promise<{ override: CacheOverride }> {
  const query: Record<string, string | number> = {}
  if (qualifier?.model) query.model = qualifier.model
  if (qualifier?.tenantId) query.tenant_id = qualifier.tenantId
  return apiSend<{ override: CacheOverride }>('PUT', `${CACHE_OVERRIDE_PATH}/${scope}`, body, { query })
}

/** 清除某 scope 缓存价倍率(回到官方价;money-gated)。 */
export async function deleteCacheOverride(
  scope: CacheOverrideScope,
  qualifier?: { model?: string; tenantId?: number },
): Promise<unknown> {
  const query: Record<string, string | number> = {}
  if (qualifier?.model) query.model = qualifier.model
  if (qualifier?.tenantId) query.tenant_id = qualifier.tenantId
  return apiSend<unknown>('DELETE', `${CACHE_OVERRIDE_PATH}/${scope}`, undefined, { query })
}
