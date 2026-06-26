/*
 * Playground(在线调试台)类型。对应 OpenAI 兼容 relay:
 *  - GET  /v1/models            → {object:"list", data:[{id,...}]}(controlhttp model_list_handler.go)
 *  - POST /v1/chat/completions  → 标准 chat completion(gatewayhttp ChatCompletionsHandler)
 * 用 BYO-key(用户粘自己的 API Key)直调,走现有 relay + 计费,无新后端/auth/money 路径。
 */

export interface ModelObject {
  id: string
  owned_by?: string
}

export interface ModelListResponse {
  object: string
  data: ModelObject[]
}

export type ChatRole = 'system' | 'user' | 'assistant'

export interface ChatMessage {
  role: ChatRole
  content: string
}

export interface ChatRequest {
  model: string
  messages: ChatMessage[]
  stream: false
}

export interface ChatUsage {
  prompt_tokens?: number
  completion_tokens?: number
  total_tokens?: number
}

export interface ChatChoice {
  message?: { role?: string; content?: string }
  finish_reason?: string
}

export interface ChatResponse {
  id?: string
  model?: string
  choices?: ChatChoice[]
  usage?: ChatUsage
}
