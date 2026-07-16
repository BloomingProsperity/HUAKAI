import { apiGet, apiSend } from '../../lib/api'
import { buildCatalogQuery } from './catalogs'
import type {
  ChannelCatalogDeleteResponse,
  ChannelCatalogItem,
  ChannelCatalogListResponse,
  ChannelCatalogMutationRequest,
  ProviderCatalogDeleteResponse,
  ProviderCatalogItem,
  ProviderCatalogListResponse,
  ProviderCatalogMutationRequest,
} from './types'

/*
 * 上游目录(provider 目录 + channel 目录)写侧管理数据访问层。
 * 所有端点挂在 /admin/v1/{providers,channels},经 tokenForPath 自动带 admin token。
 * 端点真实性见 backend/cmd/gateway/routes.go:888-900 +
 *   provider_catalog_handler.go / provider_catalog_mutation_handler.go
 *   channel_catalog_handler.go / channel_catalog_mutation_handler.go
 *
 * 注:accounts/createApi.ts 把 GET providers/channels 当只读下拉用;本模块是写侧 CRUD。
 */

// ── provider 目录 ─────────────────────────────────────────────────────────────

/** 列 provider 目录。GET /admin/v1/providers?tenant_id=N&limit=&offset=。 */
export async function listProviders(
  tenantId: number,
  limit: number,
  offset: number,
  signal?: AbortSignal,
): Promise<ProviderCatalogListResponse> {
  return apiGet<ProviderCatalogListResponse>('/admin/v1/providers', {
    query: buildCatalogQuery(tenantId, limit, offset),
    signal,
  })
}

/** 新建 provider。POST /admin/v1/providers?tenant_id=N(body 含 code/display_name/...)。 */
export async function createProvider(
  tenantId: number,
  body: ProviderCatalogMutationRequest,
): Promise<ProviderCatalogItem> {
  return apiSend<ProviderCatalogItem>('POST', '/admin/v1/providers', body, {
    query: { tenant_id: tenantId },
  })
}

/** 更新 provider(按 code)。PUT /admin/v1/providers/{code}?tenant_id=N。 */
export async function updateProvider(
  tenantId: number,
  code: string,
  body: ProviderCatalogMutationRequest,
): Promise<ProviderCatalogItem> {
  return apiSend<ProviderCatalogItem>(
    'PUT',
    `/admin/v1/providers/${encodeURIComponent(code)}`,
    body,
    { query: { tenant_id: tenantId } },
  )
}

/** 软删 provider(破坏性,UI 须二次确认)。DELETE /admin/v1/providers/{code}?tenant_id=N。 */
export async function deleteProvider(
  tenantId: number,
  code: string,
  reason: string,
): Promise<ProviderCatalogDeleteResponse> {
  return apiSend<ProviderCatalogDeleteResponse>(
    'DELETE',
    `/admin/v1/providers/${encodeURIComponent(code)}`,
    reason.trim() !== '' ? { reason: reason.trim() } : undefined,
    { query: { tenant_id: tenantId } },
  )
}

// ── channel 目录 ──────────────────────────────────────────────────────────────

/** 列 channel 目录。GET /admin/v1/channels?tenant_id=N&limit=&offset=。 */
export async function listChannels(
  tenantId: number,
  limit: number,
  offset: number,
  signal?: AbortSignal,
): Promise<ChannelCatalogListResponse> {
  return apiGet<ChannelCatalogListResponse>('/admin/v1/channels', {
    query: buildCatalogQuery(tenantId, limit, offset),
    signal,
  })
}

/** 取单条 channel。GET /admin/v1/channels/{id}?tenant_id=N。 */
export async function getChannel(
  id: number,
  tenantId: number,
  signal?: AbortSignal,
): Promise<ChannelCatalogItem> {
  return apiGet<ChannelCatalogItem>(`/admin/v1/channels/${id}`, {
    query: { tenant_id: tenantId },
    signal,
  })
}

/** 新建 channel。POST /admin/v1/channels?tenant_id=N(body 含 pool_group_id/name/...)。 */
export async function createChannel(
  tenantId: number,
  body: ChannelCatalogMutationRequest,
): Promise<ChannelCatalogItem> {
  return apiSend<ChannelCatalogItem>('POST', '/admin/v1/channels', body, {
    query: { tenant_id: tenantId },
  })
}

/** 更新 channel(按 id)。PUT /admin/v1/channels/{id}?tenant_id=N。 */
export async function updateChannel(
  tenantId: number,
  id: number,
  body: ChannelCatalogMutationRequest,
): Promise<ChannelCatalogItem> {
  return apiSend<ChannelCatalogItem>('PUT', `/admin/v1/channels/${id}`, body, {
    query: { tenant_id: tenantId },
  })
}

/** 软删 channel(破坏性,UI 须二次确认)。DELETE /admin/v1/channels/{id}?tenant_id=N。 */
export async function deleteChannel(
  tenantId: number,
  id: number,
  reason: string,
): Promise<ChannelCatalogDeleteResponse> {
  return apiSend<ChannelCatalogDeleteResponse>(
    'DELETE',
    `/admin/v1/channels/${id}`,
    reason.trim() !== '' ? { reason: reason.trim() } : undefined,
    { query: { tenant_id: tenantId } },
  )
}
