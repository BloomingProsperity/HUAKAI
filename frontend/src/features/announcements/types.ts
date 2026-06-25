/*
 * 公告管理(运营台)前端类型 —— 镜像 announcementhttp 的 JSON DTO。
 * 端点(admin token 鉴权):
 *   GET    /v1/admin/announcements        列出(含未生效/已过期,需 tenant_id query)
 *   POST   /v1/admin/announcements        新建(tenant_id 在 body)
 *   PUT    /v1/admin/announcements/{id}    编辑(需 tenant_id query)
 *   DELETE /v1/admin/announcements/{id}    删除(需 tenant_id query)
 * 真码:backend/internal/announcementhttp/handlers.go:99-127(DTO+路由)。
 */

/** 公告级别。后端 announcement.Severity 枚举(types.go:11),非此三值一律 400。 */
export type Severity = 'info' | 'warning' | 'critical'

/** 列表/详情响应项(announcementResponse,handlers.go:99)。时间均为 RFC3339 串。 */
export interface Announcement {
  id: number
  tenant_id: number
  title: string
  body: string
  severity: string
  active: boolean
  published_at: string
  expires_at?: string | null
  created_by_admin?: number | null
  created_at: string
  updated_at: string
}

/** 列表响应(announcementListResponse,handlers.go:92)。 */
export interface AnnouncementListResponse {
  object: string
  items: Announcement[]
  limit: number
  offset: number
}

/** 新建请求体(createAnnouncementRequest,handlers.go:49)。 */
export interface CreateAnnouncementRequest {
  tenant_id: number
  title: string
  body: string
  severity?: Severity
  active?: boolean
  published_at?: string
  expires_at?: string | null
}

/**
 * 编辑请求体(updateAnnouncementRequest,handlers.go:59),全字段可选(局部更新)。
 * expires_at 用三态:undefined=不动;null=清空;字符串=设置(后端 optionalTime 解析)。
 */
export interface UpdateAnnouncementRequest {
  title?: string
  body?: string
  severity?: Severity
  active?: boolean
  published_at?: string
  expires_at?: string | null
}

/** 删除响应(deleteResponse,handlers.go:113)。 */
export interface DeleteAnnouncementResponse {
  id: number
  deleted: boolean
}
