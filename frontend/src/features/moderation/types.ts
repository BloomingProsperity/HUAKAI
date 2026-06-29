/*
 * 内容审核(风控)运营台前端类型 —— 镜像后端 internal/moderationhttp 的 JSON 形态。
 *
 * 端点(均 admin token 鉴权,挂在 /admin/v1/moderation,见 cmd/gateway/routes.go:1094):
 *   GET  /admin/v1/moderation/config?tenant_id=N      取审核配置(admin_config_handler.go:33)
 *   PUT  /admin/v1/moderation/config                  upsert 配置(body 带 tenant_id,handler:52)
 *   GET  /admin/v1/moderation/logs?tenant_id=N&...     命中日志列表(admin_visibility_handler.go:69)
 *   GET  /admin/v1/moderation/banned?tenant_id=N&...   被封 Key 列表(admin_visibility_handler.go:105)
 *
 * 注意:platform_admin 角色下 tenant_id query 必填(helpers.go:42 tenantFromQuery),
 * 故本页所有读取都先要一个租户 ID。本页仅做配置(开关/范围)+ 命中日志(只读),
 * 不触碰关键词/哈希规则的增删(那是另一块,且涉及写,本切片不做)。
 */

/** 审核配置 DTO(镜像 configResponse,admin_config_handler.go:21)。 */
export interface ModerationConfig {
  tenant_id: number
  /** 总开关:租户级是否启用内容审核。 */
  enabled: boolean
  /** fail-closed:审核后端异常时是放行还是拦截(true=拦截更安全)。 */
  fail_closed: boolean
  /** 采样率百分比 0~100:抽多少比例的请求过审。 */
  sample_rate_pct: number
  /** 自动封禁阈值:窗口内命中多少次后封 Key。 */
  ban_threshold: number
  /** 自动封禁统计窗口(秒)。 */
  ban_window_seconds: number
  /** 违规罚款(USD,十进制字符串,后端 StringFixed(8))。 */
  violation_fee_usd: string
  updated_by?: string
  updated_at?: string
}

/** 配置 upsert 请求体(镜像 configRequest,admin_config_handler.go:11)。 */
export interface ModerationConfigUpdate {
  tenant_id: number
  enabled: boolean
  fail_closed: boolean
  sample_rate_pct: number
  ban_threshold: number
  ban_window_seconds: number
  violation_fee_usd: string
}

/** 单条命中日志 DTO(镜像 moderationLogResponse,admin_visibility_handler.go:13)。 */
export interface ModerationLog {
  id: number
  tenant_id: number
  api_key_id: number
  user_id: number
  request_id?: string
  payload_hash: string
  /** 审核判定:pass / block_keyword / block_hash / block_external / block_backend / fee_charged。 */
  decision: string
  reason_code: string
  matched_keyword_id?: number | null
  matched_hash_id?: number | null
  violation_fee_usd: string
  billing_event_id?: number | null
  occurred_at?: string
}

/** 日志列表响应(object="moderation_logs_list")。 */
export interface ModerationLogListResponse {
  object: string
  items: ModerationLog[]
  limit: number
  offset: number
}

/** 命中日志过滤条件(前端表单态;空串=不过滤)。 */
export interface LogFilters {
  /** 仅看某条 API Key 的命中(可选)。 */
  apiKeyId: string
}

export const EMPTY_LOG_FILTERS: LogFilters = {
  apiKeyId: '',
}

// ── 关键词黑名单(admin_keywords_handler.go)──────────────────────────────────

/** 关键词规则 DTO(镜像 keywordResponse,admin_keywords_handler.go:20)。 */
export interface KeywordRule {
  id: number
  tenant_id: number
  keyword: string
  reason_code: string
  enabled: boolean
  created_at?: string
  updated_at?: string
}

/** 关键词列表响应(object="moderation_keywords_list")。 */
export interface KeywordListResponse {
  object: string
  items: KeywordRule[]
  limit: number
  offset: number
}

/** 新建关键词请求体(keywordCreateRequest,admin_keywords_handler.go:13)。 */
export interface KeywordCreateRequest {
  tenant_id: number
  keyword: string
  reason_code: string
  enabled?: boolean
}

// ── 哈希黑名单(admin_hashes_handler.go;hash_hex 须 64 位小写 hex)──────────────

/** 哈希规则 DTO(镜像 hashResponse,admin_hashes_handler.go:19)。 */
export interface HashRule {
  id: number
  tenant_id: number
  hash_hex: string
  reason_code: string
  enabled: boolean
  created_at?: string
  updated_at?: string
}

/** 哈希列表响应(object="moderation_hashes_list")。 */
export interface HashListResponse {
  object: string
  items: HashRule[]
  limit: number
  offset: number
}

/** 新建哈希请求体(hashCreateRequest,admin_hashes_handler.go:12)。 */
export interface HashCreateRequest {
  tenant_id: number
  hash_hex: string
  reason_code: string
  enabled?: boolean
}

// ── 批量导入结果(moderation.BulkCreateResult,types.go:219;上限 1000)───────────

/** 批量项错误(BulkItemError,types.go:214)。index 为 0 基项序号。 */
export interface BulkItemError {
  index: number
  reason: string
}

/** 批量导入结果(accepted / skipped_duplicate / errors)。 */
export interface BulkCreateResult {
  accepted: number
  skipped_duplicate: number
  errors: BulkItemError[]
}

// ── 被封 API Key(admin_visibility_handler.go)─────────────────────────────────

/** 被封 Key DTO(镜像 bannedAPIKeyResponse,admin_visibility_handler.go:36)。 */
export interface BannedAPIKey {
  id: number
  tenant_id: number
  user_id: number
  name: string
  key_prefix: string
  status: string
  violation_count: number
  last_violation_at?: string
  created_at?: string
  updated_at?: string
}

/** 被封 Key 列表响应(object="moderation_banned_keys_list")。 */
export interface BannedAPIKeyListResponse {
  object: string
  items: BannedAPIKey[]
  limit: number
  offset: number
}

/** 解封响应(unbanAPIKeyResponse,admin_visibility_handler.go:61)。 */
export interface UnbanAPIKeyResult {
  api_key_id: number
  tenant_id: number
  status: string
  audit_log_id: number
  updated_at?: string
}
