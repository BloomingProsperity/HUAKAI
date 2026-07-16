/*
 * 上游目录(provider 目录 + channel 目录)运营台前端类型 —— 镜像后端 adminhttp 的 JSON 形态。
 *
 * 端点均由 admin token 鉴权并挂在 /admin/v1：
 *   provider 目录:
 *     GET    /admin/v1/providers?tenant_id=N&limit=&offset=  列表
 *     POST   /admin/v1/providers                             新建
 *     PUT    /admin/v1/providers/{code}                      更新
 *     DELETE /admin/v1/providers/{code}                      软删
 *   channel 目录:
 *     GET    /admin/v1/channels?tenant_id=N&limit=&offset=   列表
 *     POST   /admin/v1/channels                              新建
 *     GET    /admin/v1/channels/{id}                         单条
 *     PUT    /admin/v1/channels/{id}                         更新
 *     DELETE /admin/v1/channels/{id}                         软删
 *
 * 注意：platform_admin 角色下后端 tenant_id query 必填；
 * 故本页所有读写都先要一个租户 ID。
 *
 * money 说明:两份目录都【不含】任何计费/倍率/金额字段。channel 目录只承载
 * pool_group_id 与启用开关,无 money;provider 目录只承载
 * code/display_name/upstream_protocol/enabled,无 money。本切片不触碰任何计费面。
 */

// ── provider 目录 ─────────────────────────────────────────────────────────────

/** provider 目录项 DTO，与后端 provider 目录项的 JSON 形态一致。 */
export interface ProviderCatalogItem {
  id: number
  code: string
  display_name: string
  /** 上游协议族(白名单,见 catalogs.ts UPSTREAM_PROTOCOLS,镜像后端 knownProviderCatalogProtocols)。 */
  upstream_protocol: string
  enabled: boolean
  created_at?: string
}

/** provider 列表响应(object="admin_providers_list")。 */
export interface ProviderCatalogListResponse {
  object: string
  items: ProviderCatalogItem[]
  limit: number
  offset: number
}

/**
 * provider 新建/更新请求体，与后端 provider 写接口 DTO 保持一致。
 * 新建:code/display_name/upstream_protocol/enabled 均必填;
 * 更新:code 来自 URL path,body 只用 display_name/upstream_protocol/enabled;
 * reason 可选(写入审计)。
 */
export interface ProviderCatalogMutationRequest {
  code?: string
  display_name: string
  upstream_protocol: string
  enabled: boolean
  reason?: string
}

/** provider 删除响应(object="admin_provider_deleted")。 */
export interface ProviderCatalogDeleteResponse {
  object: string
  id: number
  code: string
  deleted: boolean
}

// ── channel 目录 ──────────────────────────────────────────────────────────────

/** channel 目录项 DTO，与后端 channel 目录项的 JSON 形态一致。 */
export interface ChannelCatalogItem {
  id: number
  pool_group_id: number
  name: string
  /** 旧客户端兼容字段；当前界面不展示也不下发。 */
  failover_status_codes?: number[]
  body_param_strips: string[]
  param_override: Record<string, unknown>
  sensitive_words: string[]
  enabled: boolean
  created_at?: string
}

/** channel 列表响应(object="admin_channels_list")。 */
export interface ChannelCatalogListResponse {
  object: string
  items: ChannelCatalogItem[]
  limit: number
  offset: number
}

/**
 * channel 新建/更新请求体，与后端写接口 DTO 保持一致。
 * 新建/更新:name + pool_group_id 必填,enabled 必填;
 * 更新时 id 来自 URL path;reason 可选(写入审计)。
 */
export interface ChannelCatalogMutationRequest {
  pool_group_id: number
  name: string
  /** 旧客户端兼容字段；当前界面不下发。 */
  failover_status_codes?: number[]
  /** 创建时省略等价于空数组；更新时省略表示保留。 */
  body_param_strips?: string[]
  /** 创建时省略等价于空对象；更新时省略表示保留。 */
  param_override?: Record<string, unknown>
  /** 创建时省略等价于空数组；更新时省略表示保留。 */
  sensitive_words?: string[]
  enabled: boolean
  reason?: string
}

/** channel 删除响应(object="admin_channel_deleted")。 */
export interface ChannelCatalogDeleteResponse {
  object: string
  id: number
  deleted: boolean
}
