/*
 * 渠道测试模板的前端类型契约。
 *
 * 镜像后端 adminhttp channelTestTemplateItem / channelTestTemplateRequest
 * (backend/internal/adminhttp/channel_test_template_handler.go:41-58)。
 * 字段名严格对齐后端 json tag,不臆造。
 *
 * 业务含义:运营者为渠道(上游账号)连通性测试预存的 HTTP 请求模板
 * (方法 / 路径 / 请求体模板 / 自定义请求头),按租户隔离。
 * 后端硬约束:请求头里禁止凭证类 header(authorization 等),由后端拒绝。
 */

/** 单条渠道测试模板(后端 channelTestTemplateItem)。 */
export interface ChannelTestTemplate {
  id: number
  tenant_id: number
  name: string
  method: string
  path: string
  /** 请求体模板(任意字符串,可为空)。后端字段 body_template。 */
  body_template: string
  /** 自定义请求头,JSON 对象(后端回的是 json.RawMessage,这里收成已解析对象或原始值)。 */
  headers: Record<string, unknown>
  created_at: string
}

/** 列表响应(后端 channelTestTemplateListResponse)。 */
export interface ChannelTestTemplateListResponse {
  object: string
  items: ChannelTestTemplate[]
  limit: number
  offset: number
}

/** 创建 / 更新的请求体(后端 channelTestTemplateRequest)。 */
export interface ChannelTestTemplateRequest {
  name: string
  method: string
  path: string
  body_template: string
  /** JSON 对象;后端要求是对象(map),非对象会被判 invalid_template_headers。 */
  headers: Record<string, unknown>
}

/** 删除响应(后端 channelTestTemplateDeleteResponse)。 */
export interface ChannelTestTemplateDeleteResponse {
  object: string
  id: number
  deleted: boolean
}

/** 表单态(字符串化,headers 用多行文本编辑)。 */
export interface TemplateForm {
  name: string
  method: string
  path: string
  bodyTemplate: string
  /** headers 的原始 JSON 文本(用户直接编辑)。 */
  headersText: string
}

/** 后端允许的 HTTP 方法(isAllowedChannelTestTemplateMethod)。 */
export const ALLOWED_METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE'] as const
export type AllowedMethod = (typeof ALLOWED_METHODS)[number]

/** 空表单初值。 */
export const EMPTY_FORM: TemplateForm = {
  name: '',
  method: 'GET',
  path: '/',
  bodyTemplate: '',
  headersText: '',
}
