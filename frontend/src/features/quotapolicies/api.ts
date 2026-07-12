import { apiGet, apiSend } from '../../lib/api'
import { buildListQuery } from './quotapolicies'
import type {
  PolicyFilters,
  QuotaPolicy,
  QuotaPolicyDeleteResponse,
  QuotaPolicyListResponse,
  QuotaPolicyRequest,
} from './types'

/*
 * 配额策略(防滥用限流)数据访问层。所有端点挂在 /admin/v1/quota-policies,
 * 经 tokenForPath 自动注入 admin Bearer(/admin/v1 前缀)。
 * 端点真实性见 backend/internal/adminquotahttp/quota_policy_crud.go + cmd/gateway/routes.go:905-909。
 *
 * tenant_id:platform_admin 必须显式带 ?tenant_id(routes.go:124);tenant_operator 可省略走自身作用域。
 * 本层把 tenantId 统一作为参数透传到 query,由页面决定是否填(operator 可填 0 表示省略)。
 */

/** 把租户 ID 拼进 query(tenantId>0 才下发,=0/省略时交后端用 operator 自身作用域)。 */
function tenantQuery(tenantId: number): Record<string, string | number> {
  return tenantId > 0 ? { tenant_id: tenantId } : {}
}

/**
 * 列出配额策略(带筛选 + 分页)。
 * GET /admin/v1/quota-policies?tenant_id=N&scope_kind=&metric=&enabled=&limit=&offset=。
 */
export async function listQuotaPolicies(
  tenantId: number,
  filters: PolicyFilters,
  limit: number,
  offset: number,
  signal?: AbortSignal,
): Promise<QuotaPolicyListResponse> {
  return apiGet<QuotaPolicyListResponse>('/admin/v1/quota-policies', {
    query: buildListQuery(tenantId, filters, limit, offset),
    signal,
  })
}

/** 取单条配额策略。GET /admin/v1/quota-policies/{id}?tenant_id=N。 */
export async function getQuotaPolicy(
  id: number,
  tenantId: number,
  signal?: AbortSignal,
): Promise<QuotaPolicy> {
  return apiGet<QuotaPolicy>(`/admin/v1/quota-policies/${id}`, {
    query: tenantQuery(tenantId),
    signal,
  })
}

/**
 * 新建配额策略。POST /admin/v1/quota-policies(字段在 body)。
 * 注意:tenant_id 不在 body —— 后端从认证身份/?tenant_id 解析作用域(quota_policy_crud.go:191),
 * 故 platform_admin 仍需把 tenant_id 放在 query。
 */
export async function createQuotaPolicy(
  tenantId: number,
  body: QuotaPolicyRequest,
): Promise<QuotaPolicy> {
  return apiSend<QuotaPolicy>('POST', '/admin/v1/quota-policies', body, {
    query: tenantQuery(tenantId),
  })
}

/** 更新配额策略。PUT /admin/v1/quota-policies/{id}。 */
export async function updateQuotaPolicy(
  id: number,
  tenantId: number,
  body: QuotaPolicyRequest,
): Promise<QuotaPolicy> {
  return apiSend<QuotaPolicy>('PUT', `/admin/v1/quota-policies/${id}`, body, {
    query: tenantQuery(tenantId),
  })
}

/**
 * 删除配额策略(破坏性,UI 须二次确认)。DELETE /admin/v1/quota-policies/{id}。
 * 可在 body 带 { reason } 供审计(后端 decodeOptionalJSON,quota_policy_crud.go:270)。
 */
export async function deleteQuotaPolicy(
  id: number,
  tenantId: number,
  reason: string,
): Promise<QuotaPolicyDeleteResponse> {
  const trimmed = reason.trim()
  return apiSend<QuotaPolicyDeleteResponse>(
    'DELETE',
    `/admin/v1/quota-policies/${id}`,
    trimmed === '' ? undefined : { reason: trimmed },
    { query: tenantQuery(tenantId) },
  )
}
