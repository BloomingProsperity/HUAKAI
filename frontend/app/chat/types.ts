// Playground 页内本地状态类型（不参与后端契约；后端请求体仍由 lib/api/chat.ts 的
// ChatCompletionsRequest / AnthropicMessagesRequest 约束）。

import type { UsageBlock } from '@/lib/api/types';

// 控制台支持的两种入站协议
export type TabMode = 'openai' | 'anthropic';

// 一条会话轮次。用户轮只含 role/content；助手轮额外带 usage/耗时/错误标记。
export interface ChatTurn {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  // 助手轮专属
  usage?: UsageBlock;
  durationMs?: number;
  error?: string;
}

// 采样参数 + 各自启停开关（关闭则不写入请求体，避免给上游下发默认值）。
// temperature/max_tokens 经 /v1/chat/completions 透传到上游（HUAKAI 后端不做字段白名单，
// 仅拒绝 pool_group_id）；top_p 同样透传，作为可选增强。
export interface SamplingParams {
  temperature: number;
  maxTokens: number;
  topP: number;
}

export interface ParamEnabled {
  temperature: boolean;
  maxTokens: boolean;
  topP: boolean;
}
