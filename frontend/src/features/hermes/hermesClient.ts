/*
 * Hermes 非流式 API。复用 lib/api 的 apiGet/apiSend,并显式传 admin token 作 Bearer。
 *
 * 鉴权要点:/v1/hermes/* 不匹配 tokenForPath 的 admin 前缀(只认 /admin/* 与 /v1/admin/*),
 * 若不显式覆盖 bearer 会回落到 session token → 后端恒 401。故每个调用都强制传入 adminToken。
 *
 * 本文件提供「读」面:列会话、列工具(只读发现)、读模块上下文、软删自己的会话。
 * 改动型(Hermes per-user 配置启停 + api-profile CRUD + mutating 工具 dry-run→confirm 执行)
 * 在同簇的 hermesAdminApi.ts 里,经 Owner 授权后接入;它复用本文件导出的 buildAuthQuery 走同款
 * 鉴权(显式 admin Bearer + as_user_id/tenant_id query),并对每个 mutating 工具强制走
 * 「dry-run 取 correlation_id + preview → operator 看 preview → confirm=true 执行」的安全门
 * (correlation_id 5 分钟 TTL,一次性消费),secret 仍只写不回显。
 */

import { apiGet, apiSend } from '../../lib/api'

/** 列会话所需 query:operator 必须用 as_user_id 指明所操作的 tenant user 上下文。 */
export interface HermesAuthQuery {
  asUserId: number
  tenantId?: number
}

/** 一条会话(对应后端 Conversation 形态的只读子集)。 */
export interface HermesConversation {
  id: number
  title?: string | null
  created_at?: string
  updated_at?: string
  last_message_at?: string | null
}

interface ConversationsResponse {
  conversations: HermesConversation[]
  limit: number
  offset: number
}

/**
 * 一条会话消息(对应后端 hermes.Message 的只读子集,见
 * backend/internal/hermes/types.go:103)。content 是 json.RawMessage,后端原样回传,
 * 可能是字符串 / 对象 / 数组,前端按 unknown 处理、渲染时再防御性提取文本。
 */
export interface HermesMessage {
  id: number
  conversation_id: number
  role: string
  content: unknown
  token_count?: number | null
  completed_at?: string | null
  created_at?: string
}

interface MessagesResponse {
  messages: HermesMessage[]
  limit: number
  offset: number
}

/**
 * 一个模块的合并知识视图(对应后端 modulehttp.ModuleView 的只读子集,见
 * backend/internal/modulehttp/view.go:23)。仅含模块身份 + 能力 + 实时探针枚举状态,
 * 绝不含密钥或用户数据。
 */
export interface HermesModuleView {
  id: string
  category: string
  title: string
  capabilities?: string[]
  catalog?: {
    section?: string
    feature_id?: string
    status?: string
    parity?: string
    pkgs?: string[]
  } | null
  live_probe: {
    status: string
    detail?: string
  }
}

interface ContextResponse {
  modules: HermesModuleView[]
}

/** 一条工具描述(对应后端 toolDescriptor 的只读子集)。本面板仅用于"发现只读工具",不据此触发执行。 */
export interface HermesTool {
  name: string
  category: string
  description: string
  read_only: boolean
  mutating: boolean
  required_role: string
}

interface ToolsResponse {
  tools: HermesTool[]
}

/**
 * buildAuthQuery 组装 as_user_id / tenant_id query(供 apiGet/apiSend 的 opts.query)。
 * 导出供同簇 hermesAdminApi.ts 复用,使改动型调用与只读调用走逐字节一致的鉴权 query。
 */
export function buildAuthQuery(auth: HermesAuthQuery): Record<string, string | number> {
  const q: Record<string, string | number> = { as_user_id: auth.asUserId }
  if (auth.tenantId !== undefined) q.tenant_id = auth.tenantId
  return q
}

/** listConversations 列当前操作身份的历史会话(只读)。显式带 admin Bearer。 */
export async function listConversations(
  adminToken: string,
  auth: HermesAuthQuery,
  signal?: AbortSignal,
): Promise<HermesConversation[]> {
  const resp = await apiGet<ConversationsResponse>('/v1/hermes/conversations', {
    bearer: adminToken,
    query: buildAuthQuery(auth),
    signal,
  })
  return resp.conversations ?? []
}

/** listTools 列已注册工具(只读发现)。只展示工具清单,绝不据此提供改动型 UI。显式带 admin Bearer。 */
export async function listTools(
  adminToken: string,
  auth: HermesAuthQuery,
  signal?: AbortSignal,
): Promise<HermesTool[]> {
  const resp = await apiGet<ToolsResponse>('/v1/hermes/tools', {
    bearer: adminToken,
    query: buildAuthQuery(auth),
    signal,
  })
  return resp.tools ?? []
}

/**
 * getConversation 读单个会话元数据(只读回看)。
 * GET /v1/hermes/conversations/{id}(backend conversations_handler.go:59)。显式带 admin Bearer。
 */
export async function getConversation(
  adminToken: string,
  conversationId: number,
  auth: HermesAuthQuery,
  signal?: AbortSignal,
): Promise<HermesConversation> {
  return apiGet<HermesConversation>(`/v1/hermes/conversations/${conversationId}`, {
    bearer: adminToken,
    query: buildAuthQuery(auth),
    signal,
  })
}

/**
 * listConversationMessages 载入某会话的消息(只读回看)。
 * GET /v1/hermes/conversations/{id}/messages(backend conversations_handler.go:34)。显式带 admin Bearer。
 */
export async function listConversationMessages(
  adminToken: string,
  conversationId: number,
  auth: HermesAuthQuery,
  signal?: AbortSignal,
): Promise<HermesMessage[]> {
  const resp = await apiGet<MessagesResponse>(
    `/v1/hermes/conversations/${conversationId}/messages`,
    {
      bearer: adminToken,
      query: buildAuthQuery(auth),
      signal,
    },
  )
  return resp.messages ?? []
}

/**
 * deleteConversation 软删除一条会话(破坏性,调用方须做二次确认)。
 * DELETE /v1/hermes/conversations/{id}(backend conversations_handler.go:76,返回 204 无 body)。
 * 这是本只读面板里唯一允许的「改动」——仅删除用户自己的会话记录,绝不触达系统状态 / 计费 / 配置。
 * 显式带 admin Bearer。
 */
export async function deleteConversation(
  adminToken: string,
  conversationId: number,
  auth: HermesAuthQuery,
  signal?: AbortSignal,
): Promise<void> {
  await apiSend<void>('DELETE', `/v1/hermes/conversations/${conversationId}`, undefined, {
    bearer: adminToken,
    query: buildAuthQuery(auth),
    signal,
  })
}

/**
 * getModuleContext 读合并后的模块知识视图(只读模块上下文)。
 * GET /v1/hermes/context(backend context_handler.go:15)。仅含模块身份 + 能力 + 实时探针枚举状态,
 * 绝不含密钥或用户数据。显式带 admin Bearer。
 */
export async function getModuleContext(
  adminToken: string,
  auth: HermesAuthQuery,
  signal?: AbortSignal,
): Promise<HermesModuleView[]> {
  const resp = await apiGet<ContextResponse>('/v1/hermes/context', {
    bearer: adminToken,
    query: buildAuthQuery(auth),
    signal,
  })
  return resp.modules ?? []
}
