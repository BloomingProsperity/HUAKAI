/*
 * Hermes 运营台「改动型」子系统的类型定义(配置 + 工具执行)。
 *
 * 安全建模铁律:这些 DTO 全部对应后端只读 / 引用型响应,绝不含任何 secret / key / token /
 * client_secret 字段。Profile 只回 FK 引用(api_key_id / pool_group_id),后端响应本就不返
 * 机密(见 backend/internal/hermes/types.go:80 Profile 结构,无 secret 字段);前端照此建模,
 * 任何 secret 只能单向从输入框流向 POST 请求体,永不进入这些类型 / 状态 / 持久层。
 */

// ── api_source 取值(镜像后端 backend/internal/hermes/types.go:20-21)──────────────
export const API_SOURCE_MANAGED = 'managed_huakai_api'
export const API_SOURCE_DEDICATED = 'dedicated_group'
export type HermesAPISource = typeof API_SOURCE_MANAGED | typeof API_SOURCE_DEDICATED

/**
 * per-user Hermes 配置(对应后端 hermes.Settings,types.go:70)。
 * 启停=该 user 的配置,非全局 KNOB。响应无 secret。
 */
export interface HermesSettings {
  tenant_id: number
  user_id: number
  enabled: boolean
  api_source: string
  profile_id?: number | null
  created_at?: string
  updated_at?: string
}

/**
 * 一个 Hermes API profile(对应后端 hermes.Profile,types.go:80)。
 * kind ∈ {managed_huakai_api, dedicated_group};managed 禁 pool_group_id,
 * dedicated 必需 pool_group_id。响应只含 FK 引用(api_key_id / pool_group_id),
 * 绝不含底层凭证 secret。
 */
export interface HermesProfile {
  id: number
  tenant_id: number
  owner_user_id: number
  name: string
  kind: string
  api_key_id?: number | null
  pool_group_id?: number | null
  created_at?: string
  updated_at?: string
}

/** 列 profile 响应(对应后端 profileListResponse,profiles_handler.go:20)。 */
export interface HermesProfileListResponse {
  profiles: HermesProfile[]
  count: number
}

/** 创建 profile 的请求体(对应后端 createProfileRequest,profiles_handler.go:13)。 */
export interface CreateProfileRequest {
  name: string
  kind: HermesAPISource
  api_key_id?: number
  pool_group_id?: number
}

/** 启用配置请求体(对应后端 enableSettingsRequest,settings_handler.go:10)。 */
export interface EnableSettingsRequest {
  api_source?: HermesAPISource
  profile_id?: number
}

// ── 工具发现 + 执行(/v1/hermes/tools、/v1/hermes/tool-execute)──────────────────

/**
 * 一条工具描述(对应后端 toolDescriptor,tools_handler.go:16)。
 * read_only / mutating / requires_confirmation 决定执行路径:
 *   - read_only && !mutating:直接执行
 *   - mutating:必须走 dry-run(confirm=false)→ 看 preview → 强确认(confirm=true)
 */
export interface HermesToolDescriptor {
  name: string
  category: string
  description: string
  read_only: boolean
  mutating: boolean
  requires_confirmation: boolean
  required_role: string
  input_schema?: Record<string, string>
}

interface ToolsResponse {
  tools: HermesToolDescriptor[]
}
export type { ToolsResponse as HermesToolsResponse }

/**
 * 只读工具的执行结果(对应后端 executeTool 成功响应,tools_handler.go:154)。
 * result 是脱敏后的 summary;read_only=true。
 */
export interface ReadOnlyToolResult {
  tool_name: string
  result?: Record<string, unknown> | null
  error_class?: string | null
  read_only: boolean
}

/**
 * mutating 工具 dry-run 的预览响应(对应后端 previewMutation,tools_mutate_handler.go:125)。
 * 它绝不改任何状态:只回 correlation_id(5 分钟 TTL)+ preview(将改动什么)。
 * operator 看完 preview 再带 correlation_id + confirm=true 执行。
 */
export interface MutationPreview {
  tool_name: string
  mutating: true
  dry_run: true
  requires_confirmation: true
  correlation_id: string
  expires_in_seconds: number
  preview?: Record<string, unknown> | null
}

/**
 * mutating 工具已确认执行的结果(对应后端 confirmMutation 成功响应,tools_mutate_handler.go:223)。
 * dry_run=false 表示这是真正落库后的结果。
 */
export interface MutationResult {
  tool_name: string
  mutating: true
  dry_run: false
  result?: Record<string, unknown> | null
  error_class?: string | null
  target_type?: string
  target_id?: number
}
