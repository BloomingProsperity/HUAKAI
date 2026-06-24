import { apiGet } from '../../lib/api'
import { buildAuditQuery } from './audit'
import type { AuditFilters, AuditListResponse } from './types'

/*
 * 审计查看器数据访问层。端点 GET /admin/v1/audit-events(admin token 鉴权,只读)。
 * 游标分页:首页不带 cursor,后续传上一页 next_cursor。
 */
export async function listAuditEvents(
  filters: AuditFilters,
  cursor?: string,
  limit = 100,
  signal?: AbortSignal,
): Promise<AuditListResponse> {
  return apiGet<AuditListResponse>('/admin/v1/audit-events', {
    query: { ...buildAuditQuery(filters, cursor), limit },
    signal,
  })
}
