import { apiGet, apiSend } from '../../lib/api'
import type {
  Announcement,
  AnnouncementListResponse,
  CreateAnnouncementRequest,
  DeleteAnnouncementResponse,
  UpdateAnnouncementRequest,
} from './types'

/*
 * 公告管理数据访问层。端点全部走 /v1/admin/announcements*(tokenForPath 注入 admin token)。
 * 真码:backend/internal/announcementhttp/handlers.go:122(MountAdminRoutes)、
 *       backend/cmd/gateway/routes.go:1077(MountAdminRoutes 接线)。
 * 列表/编辑/删除需 tenant_id query;新建 tenant_id 在 body。
 */

const BASE = '/v1/admin/announcements'

/** 列出公告(含未生效/已过期)。limit 1-100,offset>=0。 */
export async function listAnnouncements(
  tenantId: number,
  limit = 100,
  offset = 0,
  signal?: AbortSignal,
): Promise<AnnouncementListResponse> {
  return apiGet<AnnouncementListResponse>(BASE, {
    query: { tenant_id: tenantId, limit, offset },
    signal,
  })
}

/** 新建公告。返回 201 + 新公告。tenant_id 已在 body 内。 */
export async function createAnnouncement(req: CreateAnnouncementRequest): Promise<Announcement> {
  return apiSend<Announcement>('POST', BASE, req)
}

/** 编辑公告。需 tenant_id query 定位租户作用域。 */
export async function updateAnnouncement(
  tenantId: number,
  id: number,
  req: UpdateAnnouncementRequest,
): Promise<Announcement> {
  return apiSend<Announcement>('PUT', `${BASE}/${id}`, req, { query: { tenant_id: tenantId } })
}

/** 删除公告。需 tenant_id query。 */
export async function deleteAnnouncement(tenantId: number, id: number): Promise<DeleteAnnouncementResponse> {
  return apiSend<DeleteAnnouncementResponse>('DELETE', `${BASE}/${id}`, undefined, {
    query: { tenant_id: tenantId },
  })
}
