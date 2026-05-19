// Chat 接口：
//   postChatCompletions — POST /v1/chat/completions (OpenAI 形)
//   postAnthropicMessages — POST /v1/messages (Anthropic 形)
// 两者均支持 stream=true 时返回 Response 对象（调用方自行消费 SSE）
// 所有调用均为 REAL（call backend）

import type {
  AnthropicMessagesRequest,
  AnthropicMessagesResponse,
  ChatCompletionsRequest,
  ChatCompletionsResponse,
} from './types';

// chat 端点走 auth.APIKeyResolver, 显式拒绝 hk_admin_ token (CMB-1 隔离),
// 必须用 hk_live_* / hk_test_* 客户 API key。localStorage key 跟 admin 分离,
// 避免 landing-page 引导用户把 admin token 塞进 chat 调用导致 401
// (codex review P2 2026-05-19)。
function getCustomerAPIKey(): string {
  if (typeof window === 'undefined') return '';
  return localStorage.getItem('huakai_api_key') ?? '';
}

function chatHeaders(): Record<string, string> {
  const token = getCustomerAPIKey();
  return {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  };
}

// 非流式 OpenAI Chat
export async function postChatCompletionsJSON(
  body: ChatCompletionsRequest,
  signal?: AbortSignal,
): Promise<ChatCompletionsResponse> {
  const resp = await fetch('/v1/chat/completions', {
    method: 'POST',
    headers: chatHeaders(),
    body: JSON.stringify({ ...body, stream: false }),
    signal,
  });
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(`HTTP ${resp.status}: ${text}`);
  }
  return resp.json() as Promise<ChatCompletionsResponse>;
}

// 流式 OpenAI Chat：返回原始 Response，由调用方用 parseSSEStream 消费
export async function postChatCompletionsStream(
  body: ChatCompletionsRequest,
  signal?: AbortSignal,
): Promise<Response> {
  const resp = await fetch('/v1/chat/completions', {
    method: 'POST',
    headers: {
      ...chatHeaders(),
      // OpenAI stream 需要 include_usage
      'Accept': 'text/event-stream',
    },
    body: JSON.stringify({
      ...body,
      stream: true,
      stream_options: { include_usage: true },
    }),
    signal,
  });
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(`HTTP ${resp.status}: ${text}`);
  }
  return resp;
}

// 非流式 Anthropic Messages
export async function postAnthropicMessagesJSON(
  body: AnthropicMessagesRequest,
  signal?: AbortSignal,
): Promise<AnthropicMessagesResponse> {
  const resp = await fetch('/v1/messages', {
    method: 'POST',
    headers: chatHeaders(),
    body: JSON.stringify({ ...body, stream: false }),
    signal,
  });
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(`HTTP ${resp.status}: ${text}`);
  }
  return resp.json() as Promise<AnthropicMessagesResponse>;
}

// 流式 Anthropic Messages：返回原始 Response
export async function postAnthropicMessagesStream(
  body: AnthropicMessagesRequest,
  signal?: AbortSignal,
): Promise<Response> {
  const resp = await fetch('/v1/messages', {
    method: 'POST',
    headers: {
      ...chatHeaders(),
      'Accept': 'text/event-stream',
    },
    body: JSON.stringify({ ...body, stream: true }),
    signal,
  });
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(`HTTP ${resp.status}: ${text}`);
  }
  return resp;
}
