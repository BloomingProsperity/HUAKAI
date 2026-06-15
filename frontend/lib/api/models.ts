// Playground 模型发现：GET /v1/models（OpenAI 风格）。
// 用 hk_ 客户 API key 作 Bearer（localStorage 'huakai_api_key'），与 chat 调用一致；
// 不走 userClient（那是 session token），也不走 admin token client。
// 失败时抛 ApiError，让页面用 friendlyMessage 翻译成中文。

import { ApiError } from './client';
import type { APIError } from './types';

// 后端 controlhttp.modelObject 的形状（见 backend/internal/controlhttp/model_list_handler.go）：
//   { id, object, created, owned_by, context_length?, capabilities?, max_output_tokens?, mode?, pricing? }
export interface ModelObject {
  id: string;
  object?: string;
  created?: number;
  owned_by?: string;
  context_length?: number;
  capabilities?: Record<string, boolean>;
  max_output_tokens?: number;
  mode?: string;
  pricing?: {
    input_per_token?: string;
    output_per_token?: string;
  };
}

// 响应外层：{ object: "list", data: [...] }
export interface ModelListResponse {
  object?: string;
  data: ModelObject[];
}

// 从 localStorage 读客户 API key（与 lib/api/chat.ts 用同一把 key）
function getCustomerAPIKey(): string {
  if (typeof window === 'undefined') return '';
  return localStorage.getItem('huakai_api_key') ?? '';
}

// 拉取当前 hk_ key 可见的模型/alias 列表。
// 成功返回 ModelObject[]（已按后端顺序）；失败抛 ApiError 或 Error。
export async function listModels(signal?: AbortSignal): Promise<ModelObject[]> {
  const token = getCustomerAPIKey();
  const resp = await fetch('/v1/models', {
    method: 'GET',
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    cache: 'no-store',
    signal,
  });

  if (!resp.ok) {
    // 后端错误信封统一为 {"error":{"code","message"}}，转成 ApiError 供 friendlyMessage 翻译
    let payload: APIError | null = null;
    try {
      payload = (await resp.json()) as APIError;
    } catch {
      throw new Error(`HTTP ${resp.status}`);
    }
    if (payload?.error?.code) {
      throw new ApiError(resp.status, payload);
    }
    throw new Error(`HTTP ${resp.status}`);
  }

  const body = (await resp.json()) as ModelListResponse;
  return Array.isArray(body?.data) ? body.data : [];
}
