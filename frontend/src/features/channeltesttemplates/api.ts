import { apiGet, apiSend } from '../../lib/api'
import { buildListQuery, buildTenantQuery } from './channeltesttemplates'
import type {
  ChannelTestTemplate,
  ChannelTestTemplateDeleteResponse,
  ChannelTestTemplateListResponse,
  ChannelTestTemplateRequest,
} from './types'

/*
 * 渠道测试模板数据访问层。端点挂在 /admin/v1/channel-test-templates,
 * 经 tokenForPath 自动带 admin Bearer。
 * 端点真实性:backend/cmd/gateway/routes.go:921-925 +
 *   backend/internal/adminhttp/channel_test_template_handler.go(各 handler 构造器)。
 * 后端 platform_admin 角色要求 tenant_id 必带(parseAdminCatalogTenant),
 * 故所有调用都透传 tenantId。
 */

/** 列模板。GET /admin/v1/channel-test-templates?tenant_id=N&limit=&offset=。 */
export async function listChannelTestTemplates(
  tenantId: number,
  limit: number,
  offset: number,
  signal?: AbortSignal,
): Promise<ChannelTestTemplateListResponse> {
  return apiGet<ChannelTestTemplateListResponse>('/admin/v1/channel-test-templates', {
    query: buildListQuery(tenantId, limit, offset),
    signal,
  })
}

/** 取单条。GET /admin/v1/channel-test-templates/{id}?tenant_id=N。 */
export async function getChannelTestTemplate(
  id: number,
  tenantId: number,
  signal?: AbortSignal,
): Promise<ChannelTestTemplate> {
  return apiGet<ChannelTestTemplate>(`/admin/v1/channel-test-templates/${id}`, {
    query: buildTenantQuery(tenantId),
    signal,
  })
}

/**
 * 新建。POST /admin/v1/channel-test-templates?tenant_id=N。
 * 后端 tenant_id 取自 query(parseAdminCatalogTenant),不在 body;body 仅含字段。
 */
export async function createChannelTestTemplate(
  tenantId: number,
  body: ChannelTestTemplateRequest,
): Promise<ChannelTestTemplate> {
  return apiSend<ChannelTestTemplate>('POST', '/admin/v1/channel-test-templates', body, {
    query: buildTenantQuery(tenantId),
  })
}

/** 更新。PUT /admin/v1/channel-test-templates/{id}?tenant_id=N。 */
export async function updateChannelTestTemplate(
  id: number,
  tenantId: number,
  body: ChannelTestTemplateRequest,
): Promise<ChannelTestTemplate> {
  return apiSend<ChannelTestTemplate>('PUT', `/admin/v1/channel-test-templates/${id}`, body, {
    query: buildTenantQuery(tenantId),
  })
}

/**
 * 删除(破坏性,UI 须二次确认)。DELETE /admin/v1/channel-test-templates/{id}?tenant_id=N。
 * 后端回 {object,id,deleted}。
 */
export async function deleteChannelTestTemplate(
  id: number,
  tenantId: number,
): Promise<ChannelTestTemplateDeleteResponse> {
  return apiSend<ChannelTestTemplateDeleteResponse>(
    'DELETE',
    `/admin/v1/channel-test-templates/${id}`,
    undefined,
    { query: buildTenantQuery(tenantId) },
  )
}
