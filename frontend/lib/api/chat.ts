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

function getAdminToken(): string {
  if (typeof window === 'undefined') return '';
  return localStorage.getItem('huakai_admin_token') ?? '';
}

function chatHeaders(): Record<string, string> {
  const token = getAdminToken();
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
