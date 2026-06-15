'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import {
  AlertTriangle,
  Eraser,
  KeyRound,
  Loader2,
  RefreshCw,
  Send,
  Square,
} from 'lucide-react';
import {
  postChatCompletionsJSON,
  postChatCompletionsStream,
  postAnthropicMessagesJSON,
  postAnthropicMessagesStream,
} from '@/lib/api/chat';
import { listModels, type ModelObject } from '@/lib/api/models';
import { parseSSEStream } from '@/lib/sse';
import { ApiError } from '@/lib/api/client';
import { friendlyMessage } from '@/lib/api/errors';
import type { APIError, ChatMessage, UsageBlock } from '@/lib/api/types';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { cn } from '@/lib/utils';
import { MessageBubble } from './message-bubble';
import { ParamsPanel } from './params-panel';
import type { ChatTurn, ParamEnabled, SamplingParams, TabMode } from './types';

const API_KEY_STORAGE = 'huakai_api_key';

// 拉取失败时的回落模型（手填下拉的预置项）
const FALLBACK_OPENAI_MODELS = ['gpt-4o', 'gpt-4o-mini', 'gpt-4-turbo'];
const FALLBACK_ANTHROPIC_MODELS = [
  'claude-3-5-sonnet-20241022',
  'claude-3-5-haiku-20241022',
  'claude-3-opus-20240229',
];

const DEFAULT_PARAMS: SamplingParams = { temperature: 0.7, maxTokens: 4096, topP: 1 };
const DEFAULT_ENABLED: ParamEnabled = { temperature: true, maxTokens: true, topP: false };

let turnSeq = 0;
function newTurnId(): string {
  turnSeq += 1;
  return `t-${Date.now()}-${turnSeq}`;
}

// 把 chat.ts 抛出的 `HTTP <status>: <body>` 字符串还原成 ApiError，
// 以便 friendlyMessage 按 code/status 翻译；解析不出来则原样返回。
function toFriendly(err: unknown): string {
  if (err instanceof ApiError) return friendlyMessage(err);
  if (err instanceof Error) {
    const m = /^HTTP (\d+):\s*([\s\S]*)$/.exec(err.message);
    if (m) {
      const status = Number(m[1]);
      const rawBody = m[2];
      try {
        const payload = JSON.parse(rawBody) as APIError;
        if (payload?.error?.code) {
          return friendlyMessage(new ApiError(status, payload));
        }
      } catch {
        /* body 非 JSON，落到 status 映射 */
      }
      return friendlyMessage({ status });
    }
  }
  return friendlyMessage(err);
}

export default function PlaygroundPage() {
  const [tab, setTab] = useState<TabMode>('openai');

  // hk_ key 状态
  const [apiKey, setApiKey] = useState('');
  const [keyLoaded, setKeyLoaded] = useState(false);

  // 模型列表（真拉取）
  const [models, setModels] = useState<ModelObject[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [modelsError, setModelsError] = useState('');
  const [model, setModel] = useState('');

  // 会话
  const [systemPrompt, setSystemPrompt] = useState('');
  const [input, setInput] = useState('');
  const [streamEnabled, setStreamEnabled] = useState(true);
  const [turns, setTurns] = useState<ChatTurn[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  // 采样参数
  const [params, setParams] = useState<SamplingParams>(DEFAULT_PARAMS);
  const [enabled, setEnabled] = useState<ParamEnabled>(DEFAULT_ENABLED);

  const abortRef = useRef<AbortController | null>(null);
  const scrollRef = useRef<HTMLDivElement | null>(null);

  // 初次加载：从 localStorage 读 hk_ key
  useEffect(() => {
    const stored =
      typeof window !== 'undefined' ? localStorage.getItem(API_KEY_STORAGE) ?? '' : '';
    setApiKey(stored);
    setKeyLoaded(true);
  }, []);

  // 拉取模型列表：依赖 hk_ key（chat.ts/models.ts 内部都读 localStorage）
  const fetchModels = useCallback(async () => {
    setModelsLoading(true);
    setModelsError('');
    try {
      const list = await listModels();
      setModels(list);
      setModel((prev) => {
        if (prev && list.some((m) => m.id === prev)) return prev;
        return list.length > 0 ? list[0].id : prev;
      });
    } catch (err: unknown) {
      setModelsError(toFriendly(err));
      setModels([]);
    } finally {
      setModelsLoading(false);
    }
  }, []);

  // key 加载完且非空时自动拉一次模型
  useEffect(() => {
    if (!keyLoaded) return;
    if (apiKey.trim()) {
      void fetchModels();
    } else {
      setModels([]);
      setModelsError('');
    }
  }, [keyLoaded, apiKey, fetchModels]);

  // 新增/更新消息时滚到底部
  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight, behavior: 'smooth' });
  }, [turns]);

  function persistKey(next: string) {
    setApiKey(next);
    if (typeof window !== 'undefined') {
      if (next.trim()) localStorage.setItem(API_KEY_STORAGE, next.trim());
      else localStorage.removeItem(API_KEY_STORAGE);
    }
  }

  function switchTab(t: TabMode) {
    if (t === tab || loading) return;
    setTab(t);
    setError('');
    if (models.length === 0) {
      setModel(t === 'openai' ? FALLBACK_OPENAI_MODELS[0] : FALLBACK_ANTHROPIC_MODELS[0]);
    }
  }

  const hasKey = apiKey.trim().length > 0;
  const fallbackModels = tab === 'openai' ? FALLBACK_OPENAI_MODELS : FALLBACK_ANTHROPIC_MODELS;
  const usingRealModels = models.length > 0;
  const modelOptions = usingRealModels ? models.map((m) => m.id) : fallbackModels;
  const selectedModel = models.find((m) => m.id === model);

  function updateTurn(id: string, patch: Partial<ChatTurn>) {
    setTurns((prev) => prev.map((t) => (t.id === id ? { ...t, ...patch } : t)));
  }

  // 把 UI 轮次序列翻译成请求 messages（system 提示在 OpenAI 走 messages 首项，
  // Anthropic 走顶层 system 字段；见各自 send 实现）。
  function buildHistoryMessages(history: ChatTurn[]): ChatMessage[] {
    return history.map((t) => ({ role: t.role, content: t.content }));
  }

  // 核心发送：把 history（已含本轮 user）发出，回写到 assistantId 这条占位助手轮。
  async function runCompletion(history: ChatTurn[], assistantId: string) {
    setLoading(true);
    setError('');
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    const startedAt = Date.now();
    const pendingUsage: { current: UsageBlock | null } = { current: null };

    try {
      if (tab === 'openai') {
        await sendOpenAI(history, assistantId, ctrl.signal, pendingUsage);
      } else {
        await sendAnthropic(history, assistantId, ctrl.signal, pendingUsage);
      }
    } catch (err: unknown) {
      if (err instanceof Error && err.name === 'AbortError') {
        updateTurn(assistantId, { durationMs: Date.now() - startedAt });
        return;
      }
      const msg = toFriendly(err);
      setError(msg);
      updateTurn(assistantId, { error: msg, durationMs: Date.now() - startedAt });
    } finally {
      updateTurn(assistantId, {
        ...(pendingUsage.current ? { usage: pendingUsage.current } : {}),
        durationMs: Date.now() - startedAt,
      });
      setLoading(false);
    }
  }

  async function handleSend() {
    if (!input.trim() || !model.trim() || loading) return;
    const userTurn: ChatTurn = { id: newTurnId(), role: 'user', content: input.trim() };
    const assistantTurn: ChatTurn = { id: newTurnId(), role: 'assistant', content: '' };
    const history = [...turns, userTurn];
    setTurns([...history, assistantTurn]);
    setInput('');
    await runCompletion(history, assistantTurn.id);
  }

  // 重答：丢弃该助手轮之后所有轮次，用其之前的历史（截至上一条 user）重新生成。
  async function handleRegenerate(assistantId: string) {
    if (loading) return;
    const idx = turns.findIndex((t) => t.id === assistantId);
    if (idx < 0) return;
    const history = turns.slice(0, idx).filter((t) => t.content || t.role === 'user');
    if (history.length === 0) return;
    const fresh: ChatTurn = { id: newTurnId(), role: 'assistant', content: '' };
    const next = [...turns.slice(0, idx), fresh];
    setTurns(next);
    await runCompletion(history, fresh.id);
  }

  function handleDelete(id: string) {
    if (loading) return;
    setTurns((prev) => prev.filter((t) => t.id !== id));
  }

  function handleClear() {
    if (loading) return;
    setTurns([]);
    setError('');
  }

  // OpenAI Chat Completions：system 提示作为 messages 首项透传。
  async function sendOpenAI(
    history: ChatTurn[],
    assistantId: string,
    signal: AbortSignal,
    pendingUsage: { current: UsageBlock | null },
  ) {
    const body = {
      model,
      messages: [
        ...(systemPrompt.trim()
          ? [{ role: 'system' as const, content: systemPrompt.trim() }]
          : []),
        ...buildHistoryMessages(history),
      ],
      stream: streamEnabled,
      ...(enabled.temperature ? { temperature: params.temperature } : {}),
      ...(enabled.maxTokens ? { max_tokens: params.maxTokens } : {}),
      ...(enabled.topP ? { top_p: params.topP } : {}),
    };

    if (!streamEnabled) {
      const res = await postChatCompletionsJSON(body, signal);
      const choice = res.choices?.[0] as { message?: { content?: unknown } } | undefined;
      const content = choice?.message?.content ?? '';
      updateTurn(assistantId, {
        content: typeof content === 'string' ? content : JSON.stringify(content),
      });
      if (res.usage) pendingUsage.current = res.usage;
      return;
    }

    const resp = await postChatCompletionsStream(body, signal);
    let accumulated = '';

    await parseSSEStream(
      resp,
      (evt) => {
        if (evt.data === '[DONE]') return;
        try {
          const chunk = JSON.parse(evt.data) as {
            choices?: Array<{ delta?: { content?: unknown } }>;
            usage?: { prompt_tokens?: number; completion_tokens?: number };
          };
          const delta = chunk?.choices?.[0]?.delta?.content;
          if (typeof delta === 'string') {
            accumulated += delta;
            updateTurn(assistantId, { content: accumulated });
          }
          if (chunk?.usage) {
            pendingUsage.current = {
              input_tokens: chunk.usage.prompt_tokens ?? 0,
              output_tokens: chunk.usage.completion_tokens ?? 0,
            };
          }
        } catch {
          /* 忽略解析失败的帧 */
        }
      },
      signal,
      undefined,
      (err) => {
        const msg = toFriendly(err);
        setError(msg);
        updateTurn(assistantId, { error: msg });
      },
    );
  }

  // Anthropic Messages：system 走顶层字段；多轮 messages 透传（仅 user/assistant）。
  async function sendAnthropic(
    history: ChatTurn[],
    assistantId: string,
    signal: AbortSignal,
    pendingUsage: { current: UsageBlock | null },
  ) {
    const body = {
      model,
      max_tokens: enabled.maxTokens ? params.maxTokens : 4096,
      messages: buildHistoryMessages(history),
      ...(systemPrompt.trim() ? { system: systemPrompt.trim() } : {}),
      ...(enabled.temperature ? { temperature: params.temperature } : {}),
      ...(enabled.topP ? { top_p: params.topP } : {}),
      stream: streamEnabled,
    };

    if (!streamEnabled) {
      const res = await postAnthropicMessagesJSON(body, signal);
      const blocks = (res.content ?? []) as Array<{ type?: string; text?: string }>;
      const text = blocks.filter((b) => b.type === 'text').map((b) => b.text ?? '').join('');
      updateTurn(assistantId, { content: text });
      if (res.usage) pendingUsage.current = res.usage;
      return;
    }

    const resp = await postAnthropicMessagesStream(body, signal);
    let accumulated = '';
    let currentEvent = '';
    let localUsage: UsageBlock = {
      input_tokens: 0,
      output_tokens: 0,
      cache_creation_input_tokens: 0,
      cache_read_input_tokens: 0,
    };

    await parseSSEStream(
      resp,
      (evt) => {
        currentEvent = evt.type || currentEvent;
        try {
          const data = JSON.parse(evt.data) as {
            type?: string;
            delta?: { type?: string; text?: string };
            message?: { usage?: UsageBlock };
            usage?: UsageBlock;
          };
          const evtType = currentEvent || data?.type || '';
          if (evtType === 'content_block_delta') {
            if (data?.delta?.type === 'text_delta') {
              accumulated += data.delta.text ?? '';
              updateTurn(assistantId, { content: accumulated });
            }
          } else if (evtType === 'message_start') {
            const u = data?.message?.usage;
            if (u) {
              localUsage = {
                input_tokens: u.input_tokens ?? 0,
                output_tokens: u.output_tokens ?? 0,
                cache_creation_input_tokens: u.cache_creation_input_tokens ?? 0,
                cache_read_input_tokens: u.cache_read_input_tokens ?? 0,
              };
              pendingUsage.current = localUsage;
            }
          } else if (evtType === 'message_delta') {
            const u = data?.usage;
            if (u) {
              localUsage = {
                ...localUsage,
                output_tokens: u.output_tokens ?? localUsage.output_tokens,
              };
              pendingUsage.current = localUsage;
            }
          }
        } catch {
          /* 忽略解析失败的帧 */
        }
      },
      signal,
      undefined,
      (err) => {
        const msg = toFriendly(err);
        setError(msg);
        updateTurn(assistantId, { error: msg });
      },
    );
  }

  function handleStop() {
    abortRef.current?.abort();
    setLoading(false);
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      void handleSend();
    }
  }

  const cardCls =
    'border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70';
  const inputCls =
    'w-full rounded-lg border border-accent-200 bg-white px-3 py-2 text-sm text-accent-900 outline-none transition-colors placeholder:text-accent-400 focus:border-primary-500 focus:ring-2 focus:ring-primary-500/20 disabled:cursor-not-allowed disabled:opacity-60 dark:border-accent-800 dark:bg-accent-950/60 dark:text-accent-100';

  return (
    <div className="space-y-6">
      {/* 标题 + 引导 */}
      <section className="rounded-lg border border-accent-200 bg-white px-5 py-4 shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <p className="text-xs font-medium text-primary-700 dark:text-primary-300">Playground</p>
        <h1 className="mt-1 text-2xl font-bold tracking-normal text-accent-950 dark:text-white">
          推理调试台
        </h1>
        <p className="mt-2 text-sm text-accent-500 dark:text-accent-400">
          在
          <a
            href="/api-keys"
            className="mx-1 font-medium text-primary-600 underline-offset-2 hover:underline dark:text-primary-300"
          >
            API Keys
          </a>
          页创建 hk_ 密钥 → 这里粘贴 → 选模型 → 多轮对话。Playground 用 hk_ key 直连推理端点，与登录会话分离。
        </p>
      </section>

      {/* hk_ key 输入 */}
      <Card className={cardCls}>
        <CardHeader className="p-5 pb-3">
          <CardTitle className="flex items-center gap-2 text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            <KeyRound className="size-4 text-primary-600 dark:text-primary-300" />
            hk_ API Key
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3 p-5 pt-0">
          <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
            <input
              type="password"
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              onBlur={(e) => persistKey(e.target.value)}
              placeholder="hk_live_... 或 hk_test_..."
              autoComplete="off"
              spellCheck={false}
              className={cn(inputCls, 'font-mono sm:flex-1')}
            />
            <Button
              variant="outline"
              size="sm"
              disabled={!hasKey || modelsLoading}
              onClick={() => {
                persistKey(apiKey);
                void fetchModels();
              }}
              className="shrink-0"
            >
              <RefreshCw className={cn('size-4', modelsLoading && 'animate-spin')} />
              刷新模型
            </Button>
          </div>
          {!hasKey && keyLoaded && (
            <div className="flex items-start gap-2 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800 dark:border-amber-900/60 dark:bg-amber-950/30 dark:text-amber-200">
              <AlertTriangle className="mt-0.5 size-4 shrink-0" />
              <span>
                还没有 hk_ 密钥。请先去
                <a href="/api-keys" className="mx-1 font-medium underline underline-offset-2">
                  API Keys
                </a>
                页创建一把，再粘贴到这里。
              </span>
            </div>
          )}
        </CardContent>
      </Card>

      {/* 主体：左侧设置 + 右侧会话 */}
      <div className="grid gap-6 lg:grid-cols-[320px_1fr]">
        {/* 左：协议 / 模型 / system / 参数 */}
        <Card className={cn(cardCls, 'h-fit')}>
          <CardContent className="space-y-4 p-5">
            {/* tab 切换 */}
            <div className="inline-flex w-full rounded-lg border border-accent-200 bg-accent-50 p-1 dark:border-accent-800 dark:bg-accent-950/60">
              {(['openai', 'anthropic'] as TabMode[]).map((t) => (
                <button
                  key={t}
                  type="button"
                  onClick={() => switchTab(t)}
                  disabled={loading}
                  className={cn(
                    'flex-1 rounded-md px-2 py-1.5 text-[11px] font-medium transition-colors disabled:cursor-not-allowed',
                    tab === t
                      ? 'bg-primary-600 text-white shadow-sm'
                      : 'text-accent-600 hover:text-accent-900 dark:text-accent-300 dark:hover:text-white',
                  )}
                >
                  {t === 'openai' ? 'OpenAI' : 'Anthropic'}
                </button>
              ))}
            </div>
            <p className="text-[11px] text-accent-400 dark:text-accent-500">
              {tab === 'openai' ? 'POST /v1/chat/completions' : 'POST /v1/messages'}
            </p>

            {/* 模型选择 */}
            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <label className="text-xs font-medium text-accent-600 dark:text-accent-300">
                  模型
                </label>
                {modelsLoading ? (
                  <span className="flex items-center gap-1 text-xs text-accent-400">
                    <Loader2 className="size-3 animate-spin" /> 拉取中
                  </span>
                ) : usingRealModels ? (
                  <Badge variant="outline" className="text-[11px]">
                    真实列表 · {models.length}
                  </Badge>
                ) : (
                  <Badge variant="secondary" className="text-[11px]">
                    回落手填
                  </Badge>
                )}
              </div>
              <select
                value={model}
                onChange={(e) => setModel(e.target.value)}
                disabled={loading}
                className={cn(inputCls, 'font-mono')}
              >
                {model && !modelOptions.includes(model) && (
                  <option value={model}>{model}（自定义）</option>
                )}
                {modelOptions.length === 0 && <option value="">（无可用模型）</option>}
                {modelOptions.map((m) => (
                  <option key={m} value={m}>
                    {m}
                  </option>
                ))}
              </select>
              {modelsError && (
                <p className="text-xs text-amber-600 dark:text-amber-300">
                  模型列表拉取失败：{modelsError}（已回落到手填默认项）
                </p>
              )}
            </div>

            {/* System prompt */}
            <div className="space-y-1.5">
              <label className="text-xs font-medium text-accent-600 dark:text-accent-300">
                System Prompt（可选）
              </label>
              <textarea
                rows={3}
                value={systemPrompt}
                disabled={loading}
                onChange={(e) => setSystemPrompt(e.target.value)}
                placeholder="You are a helpful assistant."
                className={cn(inputCls, 'resize-y')}
              />
            </div>

            {/* 参数面板 */}
            <div className="border-t border-accent-200 pt-4 dark:border-accent-800">
              <ParamsPanel
                params={params}
                enabled={enabled}
                onParamChange={(k, v) => setParams((prev) => ({ ...prev, [k]: v }))}
                onToggle={(k) => setEnabled((prev) => ({ ...prev, [k]: !prev[k] }))}
                selectedModel={selectedModel}
                disabled={loading}
              />
            </div>

            <label className="flex items-center gap-2 border-t border-accent-200 pt-4 text-sm text-accent-600 dark:border-accent-800 dark:text-accent-300">
              <input
                type="checkbox"
                checked={streamEnabled}
                disabled={loading}
                onChange={(e) => setStreamEnabled(e.target.checked)}
                className="size-4 rounded border-accent-300 text-primary-600 focus:ring-primary-500/30 dark:border-accent-700"
              />
              流式（SSE）
            </label>
          </CardContent>
        </Card>

        {/* 右：会话区 */}
        <Card className={cn(cardCls, 'flex flex-col')}>
          <CardHeader className="flex flex-row items-center justify-between p-5 pb-3">
            <CardTitle className="text-base font-semibold tracking-normal text-accent-950 dark:text-white">
              对话
              {turns.length > 0 && (
                <span className="ml-2 text-xs font-normal text-accent-400">
                  {turns.filter((t) => t.role === 'user').length} 轮
                </span>
              )}
            </CardTitle>
            <Button
              variant="ghost"
              size="sm"
              onClick={handleClear}
              disabled={loading || turns.length === 0}
              className="text-accent-500 hover:text-red-600"
            >
              <Eraser className="size-4" />
              清空
            </Button>
          </CardHeader>
          <CardContent className="flex flex-1 flex-col gap-4 p-5 pt-0">
            {/* 消息列表 */}
            <div
              ref={scrollRef}
              className="min-h-[20rem] max-h-[28rem] space-y-4 overflow-y-auto rounded-lg border border-accent-200 bg-accent-50/50 p-4 dark:border-accent-800 dark:bg-accent-950/40"
            >
              {turns.length === 0 ? (
                <div className="flex h-full min-h-[18rem] flex-col items-center justify-center gap-2 text-center text-accent-400 dark:text-accent-500">
                  <Send className="size-6 opacity-40" />
                  <p className="text-sm">输入消息开始多轮对话。</p>
                  <p className="text-xs">历史会随每次发送一并提交，助手回复支持 markdown 渲染。</p>
                </div>
              ) : (
                turns.map((t) => (
                  <MessageBubble
                    key={t.id}
                    turn={t}
                    streaming={loading && t.role === 'assistant' && t.id === turns[turns.length - 1]?.id}
                    onRegenerate={
                      t.role === 'assistant' ? () => void handleRegenerate(t.id) : undefined
                    }
                    onDelete={() => handleDelete(t.id)}
                  />
                ))
              )}
            </div>

            {/* 错误条 */}
            {error && (
              <div
                role="alert"
                className="flex items-start gap-2 rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-300"
              >
                <AlertTriangle className="mt-0.5 size-4 shrink-0" />
                {error}
              </div>
            )}

            {/* 输入区 */}
            <div className="space-y-2">
              <textarea
                rows={3}
                value={input}
                disabled={loading}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={handleKeyDown}
                placeholder="输入你的消息…（Ctrl/Cmd + Enter 发送）"
                className={cn(inputCls, 'resize-y')}
              />
              <div className="flex items-center justify-between">
                <span className="text-xs text-accent-400 dark:text-accent-500">
                  {hasKey ? 'Ctrl/Cmd + Enter 快捷发送' : '需要先填入 hk_ 密钥'}
                </span>
                {!loading ? (
                  <Button onClick={handleSend} disabled={!input.trim() || !model.trim() || !hasKey}>
                    <Send className="size-4" />
                    发送
                  </Button>
                ) : (
                  <Button variant="destructive" onClick={handleStop}>
                    <Square className="size-4" />
                    停止
                  </Button>
                )}
              </div>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
