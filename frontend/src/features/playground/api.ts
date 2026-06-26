import { apiGet, apiSend } from '../../lib/api'
import { buildChatRequest } from './playground'
import type { ChatResponse, ModelListResponse } from './types'

/*
 * Playground 数据访问层。**BYO-key**:用用户显式传入的 API Key 作 Bearer 直调 relay,
 * 不依赖 session token、不持久化 Key(只在内存随请求发出)。
 * 真码:cmd/gateway/routes.go:92(/v1/chat/completions)、:115(/v1/models),
 *       均经 handler 内 inboundAuth 解析 hk_key Bearer → 走现有计费(消耗该 Key 余额)。
 */

/** listModels 用该 Key 拉可用模型(OpenAI {data:[{id}]} 形态)。 */
export async function listModels(apiKey: string, signal?: AbortSignal): Promise<ModelListResponse> {
  return apiGet<ModelListResponse>('/v1/models', { bearer: apiKey.trim(), signal })
}

/**
 * sendChat 用该 Key 发非流式 chat 请求。⚠ 这是**真实上游调用**,会消耗该 Key 的余额。
 * Key 仅作本次请求的 Bearer,不落库、不写日志。
 */
export async function sendChat(
  apiKey: string,
  model: string,
  system: string,
  message: string,
  signal?: AbortSignal,
): Promise<ChatResponse> {
  return apiSend<ChatResponse>('POST', '/v1/chat/completions', buildChatRequest(model, system, message), {
    bearer: apiKey.trim(),
    signal,
  })
}
