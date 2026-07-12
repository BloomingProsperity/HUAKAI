/*
 * 凭证续期监控(只读)类型层。
 *
 * 端点:GET /admin/v1/credentials/renew-status(游标分页)。
 * 后端真实契约见 backend/internal/gatewayhttp/admin_credentials_handler.go:79-127
 * (路由)+ backend/internal/credentialstore/postgres_store.go:150-167(RenewStatusMetadata 结构)。
 *
 * 注意:后端 RenewStatusMetadata.UpdatedAt 的 json tag 为 "-",不参与序列化;
 * 它只用于编码不透明游标(next_cursor)。前端不依赖该字段,仅靠 next_cursor 翻页。
 */

/** 单条凭证续期状态(对应后端 RenewStatusMetadata 的 JSON 形态)。 */
export interface RenewStatusRow {
  /** 凭证 ID(json:"id")。 */
  id: number
  tenant_id: number
  tenant_name: string
  account_id: number
  account_name: string
  vendor: string
  auth_mode: string
  /** 凭证状态机文本(如 active / disabled / error 等)。 */
  state: string
  /** 凭证版本号(每次轮换 +1)。 */
  credential_version: number
  /** access token 过期时刻(无则 null,如 api_key 模式)。 */
  access_expires_at: string | null
  /** 进入「应当提前续期」的时刻(refresh-before 窗口起点)。 */
  refresh_before_at: string | null
  /** 最近一次刷新发生时刻。 */
  last_refresh_at: string | null
  /** 最近一次刷新结果(如 success / failure 文本;无则 null)。 */
  last_refresh_outcome: string | null
  /** 最近失败归类(如 invalid_grant / network 等;无则 null)。 */
  failure_class: string | null
  /** 连续失败计数。 */
  failure_count: number
}

/** 列表响应:items + 不透明游标。next_cursor 为 null 表示无更多页。 */
export interface RenewStatusListResponse {
  items: RenewStatusRow[]
  next_cursor: string | null
}

/** 列表查询参数(全部可选)。 */
export interface RenewStatusQueryParams {
  /** 仅 platform_admin 无 scope 时有意义:过滤到指定租户(正整数)。 */
  tenantId?: number
  /** 每页条数(1~500)。 */
  limit?: number
  /** 上一页返回的不透明游标。 */
  cursor?: string
}
