// Hermes 运维助手 admin 数据层 —— 全部走管理 token（client.ts 同款 huakai_admin_token Bearer）。
//
// 后端: /v1/hermes/*，挂载于 backend/cmd/gateway/routes.go:368-369
//   r.With(hermesAuth).Mount("/v1/hermes", hermeshttp.NewRouterWithDeps(...))
// WAVE H1 起 Hermes 重定位为 admin/operator 运维助手：当 HUAKAI_HERMES_ADMIN_ONLY 为真
// （默认），鉴权走 hermeshttp.AdminAuthMiddleware(adminAuth)（admin_auth.go），即 admin token 轨。
//
// 鉴权关键（admin_auth.go AdminAuthMiddleware / deriveAdminTenantID / parseAsUserID）：
//   - 所有请求必带 ?as_user_id：指定操作员代入的"租户用户" Hermes 上下文（写进
//     sessionauth.Identity.UserID，(tenant_id,user_id) 复合 FK 须命中真实 users 行）。
//   - platform_admin：必带 ?tenant_id（无隐式租户，省略 = 400 hermes_admin_tenant_required）。
//   - tenant_operator：?tenant_id 可省（默认自身 scope）；带了则须与 scope 一致（否则 403）。
//   故前端统一带上 tenant_id + as_user_id 两个 query 参数，兼容两种角色。
//
// 端点形状逐字段对照后端 handler 真码（router.go 注册 + 各 *_handler.go）。
//   GET  /settings                         settings_handler.go getSettings         → Settings
//   POST /chat                             chat_handler.go startChat               → SSE（event: conversation/token/done）
//   GET  /conversations?limit&offset       conversations_handler.go listConversations → {conversations,limit,offset}
//   GET  /conversations/{id}/messages      conversations_handler.go listConversationMessages → {messages,limit,offset}
//   DELETE /conversations/{id}             conversations_handler.go deleteConversation → 204
//   GET  /tools                            tools_handler.go listTools              → {tools:[toolDescriptor]}
//   GET  /context                          context_handler.go getModuleContext     → {modules:[...]}
//   POST /tool-execute                     tools_handler.go executeTool            → {tool_name,result,error_class,read_only}
//
// dev 未装配该服务（hermesService/hermesRunner 为 nil）→ 路由不挂载（routes.go:320），
// 或 chat 因 settings 未启用返回 403 hermes_disabled。前端友好降级，不崩。

import { apiGet, ApiError } from './client';
import type { APIError } from './types';

// ---- 鉴权 scope（admin token 轨需带 tenant_id + as_user_id）----

export interface HermesScope {
  // 目标租户 ID（platform_admin 必带；tenant_operator 可与自身 scope 一致）
  tenantId: number;
  // 代入的租户用户 ID（所有请求必带，admin_auth.go parseAsUserID）
  asUserId: number;
}

function scopeParams(scope: HermesScope): Record<string, number> {
  return { tenant_id: scope.tenantId, as_user_id: scope.asUserId };
}

// 流式 fetch 用：与 client.ts 同款从 localStorage 取 admin token。
function adminToken(): string {
  if (typeof window === 'undefined') return '';
  return localStorage.getItem('huakai_admin_token') ?? '';
}

function buildScopedURL(path: string, scope: HermesScope, extra?: Record<string, string | number>): string {
  const qs = new URLSearchParams();
  qs.set('tenant_id', String(scope.tenantId));
  qs.set('as_user_id', String(scope.asUserId));
  if (extra) {
    for (const [k, v] of Object.entries(extra)) qs.set(k, String(v));
  }
  return `${path}?${qs.toString()}`;
}

// ---- 类型（对照 backend/internal/hermes/types.go + 各 handler 出参）----

// GET /settings → hermes.Settings（types.go:70）。enabled=false 时 chat 走 hermes_disabled。
export interface HermesSettings {
  tenant_id: number;
  user_id: number;
  enabled: boolean;
  api_source: string;
  profile_id?: number;
  created_at: string;
  updated_at: string;
}

// GET /conversations → {conversations,limit,offset}，元素为 hermes.Conversation（types.go:92）。
export interface HermesConversation {
  id: number;
  tenant_id: number;
  owner_user_id: number;
  title?: string;
  created_at: string;
  updated_at: string;
  last_message_at?: string;
  deleted_at?: string;
}

export interface ListConversationsResponse {
  conversations: HermesConversation[] | null;
  limit: number;
  offset: number;
}

// GET /conversations/{id}/messages → {messages,limit,offset}，元素为 hermes.Message（types.go:104）。
// content 为解密后的 json.RawMessage：可能是字符串，也可能是结构化 content blocks。
export interface HermesMessage {
  id: number;
  tenant_id: number;
  conversation_id: number;
  role: string;
  content: unknown;
  token_count?: number;
  completed_at?: string;
  created_at: string;
}

export interface ListMessagesResponse {
  messages: HermesMessage[] | null;
  limit: number;
  offset: number;
}

// GET /tools → {tools:[toolDescriptor]}（tools_handler.go:16）。
export interface HermesTool {
  name: string;
  category: string;
  description: string;
  read_only: boolean;
  mutating: boolean;
  requires_confirmation: boolean;
  required_role: string;
  input_schema: Record<string, string>;
}

export interface ListToolsResponse {
  tools: HermesTool[] | null;
}

// GET /context → {modules:[modulehttp.ContextView]}（context_handler.go:24）。
// 字段宽松（modulehttp.ContextSummary 输出模块身份 + 枚举状态 + 短文本），按需读取。
export interface HermesModuleView {
  [key: string]: unknown;
}

export interface ModuleContextResponse {
  modules: HermesModuleView[] | null;
}

// POST /chat 请求体（bridge_request.go validateChatPayload：messages 非空数组，可选 conversation_id）。
export interface HermesChatMessage {
  role: string;
  content: string;
}

export interface HermesChatRequest {
  messages: HermesChatMessage[];
  conversation_id?: number;
}

// ---- JSON 端点（apiGet 自动注入 admin Bearer；DELETE 用下方 apiPostNoContentMethod）----

export function getHermesSettings(scope: HermesScope): Promise<HermesSettings> {
  return apiGet<HermesSettings>('/v1/hermes/settings', scopeParams(scope));
}

export function listHermesConversations(
  scope: HermesScope,
  opts?: { limit?: number; offset?: number },
): Promise<ListConversationsResponse> {
  return apiGet<ListConversationsResponse>('/v1/hermes/conversations', {
    ...scopeParams(scope),
    limit: opts?.limit,
    offset: opts?.offset,
  });
}

export function listHermesMessages(
  scope: HermesScope,
  conversationId: number,
  opts?: { limit?: number; offset?: number },
): Promise<ListMessagesResponse> {
  return apiGet<ListMessagesResponse>(`/v1/hermes/conversations/${conversationId}/messages`, {
    ...scopeParams(scope),
    limit: opts?.limit,
    offset: opts?.offset,
  });
}

export function deleteHermesConversation(scope: HermesScope, conversationId: number): Promise<void> {
  return apiPostNoContentMethod(
    'DELETE',
    buildScopedURL(`/v1/hermes/conversations/${conversationId}`, scope),
  );
}

export function listHermesTools(scope: HermesScope): Promise<ListToolsResponse> {
  return apiGet<ListToolsResponse>('/v1/hermes/tools', scopeParams(scope));
}

export function getHermesContext(scope: HermesScope): Promise<ModuleContextResponse> {
  return apiGet<ModuleContextResponse>('/v1/hermes/context', scopeParams(scope));
}

// DELETE 没有 client.ts 通用封装，这里复用其语义（204 视为成功，否则解析 error.code）。
async function apiPostNoContentMethod(method: 'DELETE', url: string): Promise<void> {
  const resp = await fetch(url, {
    method,
    headers: {
      'Content-Type': 'application/json',
      ...(adminToken() ? { Authorization: `Bearer ${adminToken()}` } : {}),
    },
  });
  if (resp.status === 204 || resp.ok) return;
  let payload: APIError;
  try {
    payload = (await resp.json()) as APIError;
  } catch {
    throw new Error(`HTTP ${resp.status}`);
  }
  throw new ApiError(resp.status, payload);
}

// ---- 流式 chat（POST /chat，SSE）----
// chat_handler.go：先 GetSettings，未启用 → 403 hermes_disabled；否则把 runner 的 SSE 透传。
// bridge_sse.go 事件：event: conversation {id} / event: token {delta} / event: done {total_tokens}。
// 返回原始 Response，由调用方用 lib/sse.ts parseSSEStream 逐事件消费。
export async function startHermesChat(
  scope: HermesScope,
  body: HermesChatRequest,
  signal?: AbortSignal,
): Promise<Response> {
  const url = buildScopedURL('/v1/hermes/chat', scope);
  const resp = await fetch(url, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      Accept: 'text/event-stream',
      ...(adminToken() ? { Authorization: `Bearer ${adminToken()}` } : {}),
    },
    body: JSON.stringify(body),
    signal,
  });
  if (!resp.ok) {
    // 非 2xx：尝试解析后端 error 形（{"error":{"code,message}}} 或 {"error":"hermes_disabled"}）
    let payload: APIError | { error?: string };
    try {
      payload = (await resp.json()) as APIError | { error?: string };
    } catch {
      throw new Error(`HTTP ${resp.status}`);
    }
    if (payload && typeof payload === 'object' && 'error' in payload) {
      const err = (payload as { error: unknown }).error;
      if (typeof err === 'string') {
        // chat_handler.go writeHermesDisabled 形：{"error":"hermes_disabled"}
        throw new ApiError(resp.status, { error: { code: err, message: err } });
      }
      throw new ApiError(resp.status, payload as APIError);
    }
    throw new Error(`HTTP ${resp.status}`);
  }
  return resp;
}

// ---- SSE 事件 data 解析帮手（bridge_sse.go 各 data 形）----

// event: conversation → {"id": number}
export function parseConversationEvent(data: string): number | null {
  try {
    const p = JSON.parse(data) as { id?: number };
    return typeof p.id === 'number' ? p.id : null;
  } catch {
    return null;
  }
}

// event: token → {"delta": string}
export function parseTokenDelta(data: string): string {
  try {
    const p = JSON.parse(data) as { delta?: string };
    return typeof p.delta === 'string' ? p.delta : '';
  } catch {
    return '';
  }
}

// event: done → {"total_tokens": number}
export function parseDoneTotalTokens(data: string): number | null {
  try {
    const p = JSON.parse(data) as { total_tokens?: number };
    return typeof p.total_tokens === 'number' ? p.total_tokens : null;
  } catch {
    return null;
  }
}

// ---- 展示帮手 ----

// message.content 可能是字符串或结构化 content blocks，安全转成展示文本。
export function messageContentToText(content: unknown): string {
  if (typeof content === 'string') return content;
  if (Array.isArray(content)) {
    return content
      .map((part) => {
        if (typeof part === 'string') return part;
        if (part && typeof part === 'object' && 'text' in part) {
          const t = (part as { text?: unknown }).text;
          return typeof t === 'string' ? t : '';
        }
        return '';
      })
      .filter(Boolean)
      .join('');
  }
  if (content && typeof content === 'object' && 'text' in content) {
    const t = (content as { text?: unknown }).text;
    if (typeof t === 'string') return t;
  }
  if (content == null) return '';
  return JSON.stringify(content);
}

export function formatDateTime(iso?: string): string {
  if (!iso) return '—';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString('zh-CN', { hour12: false });
}
