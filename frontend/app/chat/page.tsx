'use client';

import { useCallback, useEffect, useRef, useState } from 'react';
import {
  AlertTriangle,
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
import type { APIError, UsageBlock } from '@/lib/api/types';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { cn } from '@/lib/utils';

const API_KEY_STORAGE = 'huakai_api_key';

// 控制台支持的两种协议 tab
type TabMode = 'openai' | 'anthropic';

// 拉取失败时的回落模型（手填下拉的预置项）
const FALLBACK_OPENAI_MODELS = ['gpt-4o', 'gpt-4o-mini', 'gpt-4-turbo'];
const FALLBACK_ANTHROPIC_MODELS = [
  'claude-3-5-sonnet-20241022',
  'claude-3-5-haiku-20241022',
  'claude-3-opus-20240229',
];

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

  // 表单
  const [systemPrompt, setSystemPrompt] = useState('');
  const [userMessage, setUserMessage] = useState('');
  const [streamEnabled, setStreamEnabled] = useState(true);

  // 响应
  const [responseText, setResponseText] = useState('');
  const [usage, setUsage] = useState<UsageBlock | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  const abortRef = useRef<AbortController | null>(null);
  // 用 ref 暂存流式 usage，保证 error/abort 路径也能 commit
  const pendingUsageRef = useRef<UsageBlock | null>(null);

  // 初次加载：从 localStorage 读 hk_ key
  useEffect(() => {
    const stored = typeof window !== 'undefined' ? localStorage.getItem(API_KEY_STORAGE) ?? '' : '';
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
      // 若当前选中的 model 不在新列表里，重置为首个
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
    // 仅在 key 就绪/变化时触发（apiKey 变化由 onBlur 持久化驱动）
  }, [keyLoaded, apiKey, fetchModels]);

  // 持久化 hk_ key
  function persistKey(next: string) {
    setApiKey(next);
    if (typeof window !== 'undefined') {
      if (next.trim()) localStorage.setItem(API_KEY_STORAGE, next.trim());
      else localStorage.removeItem(API_KEY_STORAGE);
    }
  }

  // 切 tab：清结果，按需把 model 重置成对应回落首项（若拉取列表为空）
  function switchTab(t: TabMode) {
    if (t === tab) return;
    setTab(t);
    setResponseText('');
    setUsage(null);
    setError('');
    // 列表为空时给个对应协议的回落默认值
    if (models.length === 0) {
      setModel(t === 'openai' ? FALLBACK_OPENAI_MODELS[0] : FALLBACK_ANTHROPIC_MODELS[0]);
    }
  }

  const hasKey = apiKey.trim().length > 0;
  const fallbackModels = tab === 'openai' ? FALLBACK_OPENAI_MODELS : FALLBACK_ANTHROPIC_MODELS;
  const usingRealModels = models.length > 0;
  const modelOptions = usingRealModels ? models.map((m) => m.id) : fallbackModels;

  async function handleSend() {
    if (!userMessage.trim() || !model.trim()) return;
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
      setError(toFriendly(err));
    } finally {
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
      const choice = res.choices?.[0] as { message?: { content?: unknown } } | undefined;
      const content = choice?.message?.content ?? '';
      setResponseText(typeof content === 'string' ? content : JSON.stringify(content));
      if (res.usage) setUsage(res.usage);
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
            setResponseText(accumulated);
          }
          // OpenAI stream_options.include_usage 最后一帧带 usage；写入 ref 供 finally 提交
          if (chunk?.usage) {
            pendingUsageRef.current = {
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
      (err) => setError(toFriendly(err)),
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
      const blocks = (res.content ?? []) as Array<{ type?: string; text?: string }>;
      const text = blocks.filter((b) => b.type === 'text').map((b) => b.text ?? '').join('');
      setResponseText(text);
      if (res.usage) setUsage(res.usage);
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
              setResponseText(accumulated);
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
              pendingUsageRef.current = localUsage;
            }
          } else if (evtType === 'message_delta') {
            const u = data?.usage;
            if (u) {
              localUsage = { ...localUsage, output_tokens: u.output_tokens ?? localUsage.output_tokens };
              pendingUsageRef.current = localUsage;
            }
          }
        } catch {
          /* 忽略解析失败的帧 */
        }
      },
      signal,
      undefined,
      (err) => setError(toFriendly(err)),
    );
  }

  function handleStop() {
    abortRef.current?.abort();
    setLoading(false);
  }

  const totalTokens = usage
    ? (usage.input_tokens ?? 0) + (usage.output_tokens ?? 0)
    : 0;

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
          页创建 hk_ 密钥 → 这里粘贴 → 选模型 → 发送。Playground 用 hk_ key 直连推理端点，与登录会话分离。
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

      {/* 协议 tab + 模型 + 表单 */}
      <Card className={cardCls}>
        <CardContent className="space-y-4 p-5">
          {/* tab 切换 */}
          <div className="inline-flex rounded-lg border border-accent-200 bg-accent-50 p-1 dark:border-accent-800 dark:bg-accent-950/60">
            {(['openai', 'anthropic'] as TabMode[]).map((t) => (
              <button
                key={t}
                type="button"
                onClick={() => switchTab(t)}
                disabled={loading}
                className={cn(
                  'rounded-md px-3 py-1.5 text-xs font-medium transition-colors disabled:cursor-not-allowed',
                  tab === t
                    ? 'bg-primary-600 text-white shadow-sm'
                    : 'text-accent-600 hover:text-accent-900 dark:text-accent-300 dark:hover:text-white',
                )}
              >
                {t === 'openai' ? 'OpenAI · /v1/chat/completions' : 'Anthropic · /v1/messages'}
              </button>
            ))}
          </div>

          {/* 模型选择 */}
          <div className="space-y-1.5">
            <div className="flex items-center justify-between">
              <label className="text-xs font-medium text-accent-600 dark:text-accent-300">模型</label>
              {modelsLoading ? (
                <span className="flex items-center gap-1 text-xs text-accent-400">
                  <Loader2 className="size-3 animate-spin" /> 拉取中
                </span>
              ) : usingRealModels ? (
                <Badge variant="outline" className="text-[11px]">真实列表 · {models.length}</Badge>
              ) : (
                <Badge variant="secondary" className="text-[11px]">回落手填</Badge>
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
              rows={2}
              value={systemPrompt}
              disabled={loading}
              onChange={(e) => setSystemPrompt(e.target.value)}
              placeholder="You are a helpful assistant."
              className={cn(inputCls, 'resize-y')}
            />
          </div>

          {/* User message */}
          <div className="space-y-1.5">
            <label className="text-xs font-medium text-accent-600 dark:text-accent-300">
              User Message
            </label>
            <textarea
              rows={4}
              value={userMessage}
              disabled={loading}
              onChange={(e) => setUserMessage(e.target.value)}
              placeholder="输入你的消息…"
              className={cn(inputCls, 'resize-y')}
            />
          </div>

          {/* 控制行 */}
          <div className="flex flex-wrap items-center justify-between gap-3">
            <label className="flex items-center gap-2 text-sm text-accent-600 dark:text-accent-300">
              <input
                type="checkbox"
                checked={streamEnabled}
                disabled={loading}
                onChange={(e) => setStreamEnabled(e.target.checked)}
                className="size-4 rounded border-accent-300 text-primary-600 focus:ring-primary-500/30 dark:border-accent-700"
              />
              流式（SSE）
            </label>
            {!loading ? (
              <Button
                onClick={handleSend}
                disabled={!userMessage.trim() || !model.trim() || !hasKey}
              >
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

          {error && (
            <div
              role="alert"
              className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/30 dark:text-red-300"
            >
              {error}
            </div>
          )}
        </CardContent>
      </Card>

      {/* 响应 */}
      <Card className={cardCls}>
        <CardHeader className="p-5 pb-3">
          <CardTitle className="text-base font-semibold tracking-normal text-accent-950 dark:text-white">
            响应
          </CardTitle>
        </CardHeader>
        <CardContent className="p-5 pt-0">
          <div className="min-h-[8rem] whitespace-pre-wrap break-words rounded-lg border border-accent-200 bg-accent-50 p-4 text-sm leading-6 text-accent-800 dark:border-accent-800 dark:bg-accent-950/60 dark:text-accent-100">
            {responseText ? (
              <>
                {responseText}
                {loading && <span className="ml-0.5 animate-pulse text-primary-500">▌</span>}
              </>
            ) : loading ? (
              <span className="animate-pulse text-primary-500">▌</span>
            ) : (
              <span className="text-accent-400 dark:text-accent-500">（暂无输出）</span>
            )}
          </div>
        </CardContent>
      </Card>

      {/* usage 面板 */}
      {usage && (
        <Card className={cardCls}>
          <CardHeader className="p-5 pb-3">
            <CardTitle className="text-base font-semibold tracking-normal text-accent-950 dark:text-white">
              本次用量
            </CardTitle>
          </CardHeader>
          <CardContent className="p-5 pt-0">
            <div className="grid grid-cols-3 gap-3 sm:grid-cols-5">
              <UsageStat label="输入 token" value={usage.input_tokens ?? 0} />
              <UsageStat label="输出 token" value={usage.output_tokens ?? 0} />
              <UsageStat label="合计 token" value={totalTokens} highlight />
              {usage.cache_creation_input_tokens !== undefined && (
                <UsageStat label="缓存创建" value={usage.cache_creation_input_tokens} />
              )}
              {usage.cache_read_input_tokens !== undefined && (
                <UsageStat label="缓存读取" value={usage.cache_read_input_tokens} />
              )}
            </div>
            <p className="mt-3 text-xs text-accent-400 dark:text-accent-500">
              数值取自最终 SSE usage 块（OpenAI 用 stream_options.include_usage，Anthropic 用 message_delta）。
            </p>
          </CardContent>
        </Card>
      )}
    </div>
  );
}

function UsageStat({
  label,
  value,
  highlight,
}: {
  label: string;
  value: number;
  highlight?: boolean;
}) {
  return (
    <div
      className={cn(
        'rounded-lg border px-3 py-2.5',
        highlight
          ? 'border-primary-200 bg-primary-50 dark:border-primary-900 dark:bg-primary-950/40'
          : 'border-accent-200 bg-accent-50 dark:border-accent-800 dark:bg-accent-950/60',
      )}
    >
      <div className="text-xs text-accent-500 dark:text-accent-400">{label}</div>
      <div
        className={cn(
          'mt-1 font-mono text-lg font-semibold tabular-nums',
          highlight
            ? 'text-primary-700 dark:text-primary-300'
            : 'text-accent-900 dark:text-accent-100',
        )}
      >
        {value.toLocaleString('zh-CN')}
      </div>
    </div>
  );
}
