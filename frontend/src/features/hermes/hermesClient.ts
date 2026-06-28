/*
 * Hermes 非流式只读 API(列会话 / 列工具)。复用 lib/api 的 apiGet,并显式传 admin token 作 Bearer。
 *
 * 鉴权要点:/v1/hermes/* 不匹配 tokenForPath 的 admin 前缀(只认 /admin/* 与 /v1/admin/*),
 * 若不显式覆盖 bearer 会回落到 session token → 后端恒 401。故每个调用都强制传入 adminToken。
 * 这里只做"读":列会话、列工具(只读发现),绝不调用任何改动型 / 提议 / 确认端点。
 */

import { apiGet } from '../../lib/api'

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

/** buildAuthQuery 组装 as_user_id / tenant_id query(供 apiGet 的 opts.query)。 */
function buildAuthQuery(auth: HermesAuthQuery): Record<string, string | number> {
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
