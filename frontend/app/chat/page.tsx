'use client';

import { useState, useRef } from 'react';
import {
  postChatCompletionsJSON,
  postChatCompletionsStream,
  postAnthropicMessagesJSON,
  postAnthropicMessagesStream,
} from '../../lib/api/chat';
import { parseSSEStream } from '../../lib/sse';
import type { UsageBlock } from '../../lib/api/types';

// 控制台支持的两种 tab
type TabMode = 'openai' | 'anthropic';

// 支持的模型
const OPENAI_MODELS = [
  'gpt-4o',
  'gpt-4o-mini',
  'gpt-4-turbo',
];
const ANTHROPIC_MODELS = [
  'anthropic.claude-3-5-sonnet-20241022-v2:0',
  'anthropic.claude-3-5-haiku-20241022-v1:0',
  'claude-3-5-sonnet-20241022',
  'claude-3-opus-20240229',
];

export default function ChatDebugPage() {
  const [tab, setTab] = useState<TabMode>('openai');
  const [model, setModel] = useState(OPENAI_MODELS[0]);
  const [systemPrompt, setSystemPrompt] = useState('');
  const [userMessage, setUserMessage] = useState('');
  const [streamEnabled, setStreamEnabled] = useState(true);
  const [responseText, setResponseText] = useState('');
  const [usage, setUsage] = useState<UsageBlock | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const abortRef = useRef<AbortController | null>(null);
  // 用 ref 暂存流式 usage，保证 error/abort 路径也能 commit
  const pendingUsageRef = useRef<UsageBlock | null>(null);

  // 切换 tab 时重置 model
  function switchTab(t: TabMode) {
    setTab(t);
    setModel(t === 'openai' ? OPENAI_MODELS[0] : ANTHROPIC_MODELS[0]);
    setResponseText('');
    setUsage(null);
    setError('');
  }

  async function handleSend() {
    if (!userMessage.trim()) return;
    setLoading(true);
    setError('');
    setResponseText('');
    setUsage(null);
    pendingUsageRef.current = null;
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;

    try {
      if (tab === 'openai') {
        await sendOpenAI(ctrl.signal);
      } else {
        await sendAnthropic(ctrl.signal);
      }
    } catch (err: unknown) {
      if (err instanceof Error && err.name === 'AbortError') return;
      setError(String(err));
    } finally {
      // 无论正常结束、出错还是 abort，只要积累了 usage 就提交显示
      if (pendingUsageRef.current) setUsage(pendingUsageRef.current);
      setLoading(false);
    }
  }

  // OpenAI Chat Completions
  async function sendOpenAI(signal: AbortSignal) {
    const body = {
      model,
      messages: [
        ...(systemPrompt.trim() ? [{ role: 'system' as const, content: systemPrompt.trim() }] : []),
        { role: 'user' as const, content: userMessage.trim() },
      ],
      stream: streamEnabled,
      max_tokens: 4096,
    };

    if (!streamEnabled) {
      const res = await postChatCompletionsJSON(body, signal);
      // choices[0].message.content 可能是 string
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const content = (res.choices?.[0] as any)?.message?.content ?? '';
      setResponseText(typeof content === 'string' ? content : JSON.stringify(content));
      if (res.usage) setUsage(res.usage);
      return;
    }

    // 流式：SSE 解析 OpenAI delta
    const resp = await postChatCompletionsStream(body, signal);
    let accumulated = '';

    await parseSSEStream(
      resp,
      (evt) => {
        if (evt.data === '[DONE]') return;
        try {
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          const chunk: any = JSON.parse(evt.data);
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          const delta = chunk?.choices?.[0]?.delta?.content as any;
          if (typeof delta === 'string') {
            accumulated += delta;
            setResponseText(accumulated);
          }
          // OpenAI stream_options.include_usage 最后一帧带 usage；写入 ref 供 finally 提交
          if (chunk?.usage) {
            pendingUsageRef.current = {
              input_tokens: chunk.usage.prompt_tokens ?? 0,
              output_tokens: chunk.usage.completion_tokens ?? 0,
            };
          }
        } catch { /* 忽略解析失败 */ }
      },
      signal,
      undefined,
      (err) => setError(String(err)),
    );
  }

  // Anthropic Messages
  async function sendAnthropic(signal: AbortSignal) {
    const body = {
      model,
      max_tokens: 4096,
      messages: [{ role: 'user' as const, content: userMessage.trim() }],
      ...(systemPrompt.trim() ? { system: systemPrompt.trim() } : {}),
      stream: streamEnabled,
    };

    if (!streamEnabled) {
      const res = await postAnthropicMessagesJSON(body, signal);
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      const text = (res.content ?? []).filter((b: any) => b.type === 'text').map((b: any) => b.text as string).join('');
      setResponseText(text);
      if (res.usage) setUsage(res.usage);
      return;
    }

    // 流式：Anthropic SSE
    const resp = await postAnthropicMessagesStream(body, signal);
    let accumulated = '';
    let currentEvent = '';
    // 用局部变量积累，每次更新同步写入 ref，保证 error/abort 路径也能 commit
    let localUsage: UsageBlock = { input_tokens: 0, output_tokens: 0, cache_creation_input_tokens: 0, cache_read_input_tokens: 0 };

    await parseSSEStream(
      resp,
      (evt) => {
        currentEvent = evt.type || currentEvent;
        try {
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          const data: any = JSON.parse(evt.data);
          const evtType = currentEvent || data?.type || '';
          if (evtType === 'content_block_delta') {
            if (data?.delta?.type === 'text_delta') {
              accumulated += (data.delta.text as string) ?? '';
              setResponseText(accumulated);
            }
          } else if (evtType === 'message_start') {
            const u = data?.message?.usage;
            if (u) {
              localUsage = { input_tokens: u.input_tokens ?? 0, output_tokens: u.output_tokens ?? 0, cache_creation_input_tokens: u.cache_creation_input_tokens ?? 0, cache_read_input_tokens: u.cache_read_input_tokens ?? 0 };
              pendingUsageRef.current = localUsage;
            }
          } else if (evtType === 'message_delta') {
            const u = data?.usage;
            if (u) {
              localUsage = { ...localUsage, output_tokens: u.output_tokens ?? localUsage.output_tokens };
              pendingUsageRef.current = localUsage;
            }
          }
        } catch { /* 忽略 */ }
      },
      signal,
      undefined,
      (err) => setError(String(err)),
    );
  }

  function handleStop() {
    abortRef.current?.abort();
    setLoading(false);
  }

  const models = tab === 'openai' ? OPENAI_MODELS : ANTHROPIC_MODELS;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      <h1 style={{ fontSize: '1rem', color: '#e6edf3', fontWeight: 600 }}>
        面板 3 — Chat 调试器
      </h1>

      {/* Tab 切换 */}
      <div style={{ display: 'flex', gap: '0.5rem' }}>
        {(['openai', 'anthropic'] as TabMode[]).map((t) => (
          <button key={t} onClick={() => switchTab(t)}
            style={{
              background: tab === t ? '#238636' : '#21262d',
              fontSize: '0.8rem', padding: '0.3rem 0.8rem',
            }}>
            {t === 'openai' ? 'POST /v1/chat/completions (OpenAI)' : 'POST /v1/messages (Anthropic)'}
          </button>
        ))}
      </div>

      {/* 模型选择 */}
      <div>
        <label>model</label>
        <select value={model} onChange={(e) => setModel(e.target.value)} disabled={loading}>
          {models.map((m) => <option key={m} value={m}>{m}</option>)}
        </select>
      </div>

      {/* System prompt */}
      <div>
        <label>System Prompt（可选）</label>
        <textarea rows={2} value={systemPrompt} disabled={loading}
          onChange={(e) => setSystemPrompt(e.target.value)}
          placeholder="You are a helpful assistant." />
      </div>

      {/* User message */}
      <div>
        <label>User Message</label>
        <textarea rows={4} value={userMessage} disabled={loading}
          onChange={(e) => setUserMessage(e.target.value)}
          placeholder="输入你的消息…" />
      </div>

      {/* 控制行 */}
      <div className="row-controls">
        <label className="checkbox-row">
          <input type="checkbox" checked={streamEnabled} disabled={loading}
            onChange={(e) => setStreamEnabled(e.target.checked)} />
          Stream（SSE）
        </label>
        {!loading ? (
          <button onClick={handleSend} disabled={!userMessage.trim()}>Send</button>
        ) : (
          <button onClick={handleStop} style={{ background: '#da3633' }}>Stop</button>
        )}
      </div>

      {error && <div className="error-msg">{error}</div>}

      {/* 响应展示 */}
      <div>
        <label>Response</label>
        <div className="response-box">
          {responseText || (loading ? '▌' : <span style={{ color: '#484f58' }}>（空）</span>)}
        </div>
      </div>

      {/* Usage 统计 */}
      {usage && (
        <div className="usage-row">
          <span>input <strong>{usage.input_tokens ?? 0}</strong></span>
          <span>output <strong>{usage.output_tokens ?? 0}</strong></span>
          {usage.cache_creation_input_tokens !== undefined && (
            <span>cache_creation <strong>{usage.cache_creation_input_tokens}</strong></span>
          )}
          {usage.cache_read_input_tokens !== undefined && (
            <span>cache_read <strong>{usage.cache_read_input_tokens}</strong></span>
          )}
        </div>
      )}
    </div>
  );
}
