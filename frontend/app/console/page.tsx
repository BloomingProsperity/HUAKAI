'use client';

// 推理多协议调试台：Playground（/chat）只做 chat，本页覆盖其余推理协议。
// 鉴权与 chat 一致——hk_ 客户 API key（localStorage 'huakai_api_key'）作 Bearer 直连
// 推理端点（见 lib/api/inference.ts + lib/api/models.ts），不走 session / admin token。
// 三个协议 tab：Embeddings / Images / Rerank，各自输入区 + 发送 + 结果展示 + 用量。
// 错误统一经 toFriendly()（还原 ApiError）→ friendlyMessage 翻成中文。
// 对照：sub2api / new-api 前端均无多协议调试台（sub2api 仅在账号弹窗用 embedding/rerank
// 作模型类型标签，new-api/web 无 playground 目录），故本页为 HUAKAI 自有面，无抄码对照。

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  AlertTriangle,
  Binary,
  Hash,
  Image as ImageIcon,
  KeyRound,
  ListOrdered,
  Loader2,
  RefreshCw,
  Send,
  Square,
} from 'lucide-react';
import { listModels, type ModelObject } from '@/lib/api/models';
import {
  postEmbeddings,
  postImageGenerations,
  postRerank,
  type EmbeddingsResponse,
  type ImagesResponse,
  type RerankResponse,
} from '@/lib/api/inference';
import { ApiError } from '@/lib/api/client';
import { friendlyMessage } from '@/lib/api/errors';
import type { APIError } from '@/lib/api/types';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { cn } from '@/lib/utils';

const API_KEY_STORAGE = 'huakai_api_key';

type ProtocolTab = 'embeddings' | 'images' | 'rerank';

const TAB_META: Record<ProtocolTab, { label: string; endpoint: string; icon: typeof Binary }> = {
  embeddings: { label: 'Embeddings', endpoint: 'POST /v1/embeddings', icon: Binary },
  images: { label: 'Images', endpoint: 'POST /v1/images/generations', icon: ImageIcon },
  rerank: { label: 'Rerank', endpoint: 'POST /v1/rerank', icon: ListOrdered },
};

const IMAGE_SIZES = ['1024x1024', '512x512', '256x256', '1792x1024', '1024x1792'];

// 拉取失败时的手填回落项（按协议给些常见模型名占位，仅作下拉默认）。
const FALLBACK_MODELS: Record<ProtocolTab, string[]> = {
  embeddings: ['text-embedding-3-small', 'text-embedding-3-large'],
  images: ['dall-e-3', 'dall-e-2', 'gpt-image-1'],
  rerank: ['rerank-english-v3.0', 'rerank-multilingual-v3.0'],
};

// 把 inference.ts 抛出的 `HTTP <status>: <body>` 还原成 ApiError，
// 以便 friendlyMessage 按 code/status 翻译；解析不出来则落到 status 映射。
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

function rerankDocumentText(doc: RerankResponse['results'][number]['document']): string {
  if (typeof doc === 'string') return doc;
  if (doc && typeof doc === 'object' && typeof doc.text === 'string') return doc.text;
  return '';
}

export default function ConsolePage() {
  const [tab, setTab] = useState<ProtocolTab>('embeddings');

  // hk_ key 状态
  const [apiKey, setApiKey] = useState('');
  const [keyLoaded, setKeyLoaded] = useState(false);

  // 模型列表（真拉取，与 Playground 共用 listModels）
  const [models, setModels] = useState<ModelObject[]>([]);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [modelsError, setModelsError] = useState('');
  const [model, setModel] = useState('');

  // 各协议输入
  const [embedInput, setEmbedInput] = useState('');
  const [imagePrompt, setImagePrompt] = useState('');
  const [imageN, setImageN] = useState(1);
  const [imageSize, setImageSize] = useState(IMAGE_SIZES[0]);
  const [rerankQuery, setRerankQuery] = useState('');
  const [rerankDocs, setRerankDocs] = useState('');
  const [rerankTopN, setRerankTopN] = useState('');

  // 结果
  const [embedResult, setEmbedResult] = useState<EmbeddingsResponse | null>(null);
  const [imageResult, setImageResult] = useState<ImagesResponse | null>(null);
  const [rerankResult, setRerankResult] = useState<RerankResponse | null>(null);

  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [durationMs, setDurationMs] = useState<number | null>(null);

  const abortRef = useRef<AbortController | null>(null);

  // 初次加载：从 localStorage 读 hk_ key
  useEffect(() => {
    const stored =
      typeof window !== 'undefined' ? localStorage.getItem(API_KEY_STORAGE) ?? '' : '';
    setApiKey(stored);
    setKeyLoaded(true);
  }, []);

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

  function persistKey(next: string) {
    setApiKey(next);
    if (typeof window !== 'undefined') {
      if (next.trim()) localStorage.setItem(API_KEY_STORAGE, next.trim());
      else localStorage.removeItem(API_KEY_STORAGE);
    }
  }

  function switchTab(t: ProtocolTab) {
    if (t === tab || loading) return;
    setTab(t);
    setError('');
    setDurationMs(null);
    if (models.length === 0) setModel(FALLBACK_MODELS[t][0]);
  }

  const hasKey = apiKey.trim().length > 0;
  const usingRealModels = models.length > 0;
  const modelOptions = usingRealModels ? models.map((m) => m.id) : FALLBACK_MODELS[tab];

  // 解析 rerank 文档：按行切，去空行。
  const parsedRerankDocs = useMemo(
    () => rerankDocs.split('\n').map((l) => l.trim()).filter(Boolean),
    [rerankDocs],
  );

  const canSend = useMemo(() => {
    if (!hasKey || !model.trim()) return false;
    if (tab === 'embeddings') return embedInput.trim().length > 0;
    if (tab === 'images') return imagePrompt.trim().length > 0 && imageN > 0;
    return rerankQuery.trim().length > 0 && parsedRerankDocs.length > 0;
  }, [hasKey, model, tab, embedInput, imagePrompt, imageN, rerankQuery, parsedRerankDocs]);

  async function handleSend() {
    if (!canSend || loading) return;
    setLoading(true);
    setError('');
    setDurationMs(null);
    abortRef.current?.abort();
    const ctrl = new AbortController();
    abortRef.current = ctrl;
    const startedAt = Date.now();

    try {
      if (tab === 'embeddings') {
        setEmbedResult(null);
        // 多行输入 → string[]，单行 → string。
        const lines = embedInput.split('\n').map((l) => l.trim()).filter(Boolean);
        const input = lines.length > 1 ? lines : lines[0] ?? embedInput.trim();
        const res = await postEmbeddings({ model, input }, ctrl.signal);
        setEmbedResult(res);
      } else if (tab === 'images') {
        setImageResult(null);
        const res = await postImageGenerations(
          { model, prompt: imagePrompt.trim(), n: imageN, size: imageSize },
          ctrl.signal,
        );
        setImageResult(res);
      } else {
        setRerankResult(null);
        const topN = rerankTopN.trim() ? Number(rerankTopN.trim()) : undefined;
        const res = await postRerank(
          {
            model,
            query: rerankQuery.trim(),
            documents: parsedRerankDocs,
            return_documents: true,
            ...(topN && topN > 0 ? { top_n: topN } : {}),
          },
          ctrl.signal,
        );
        setRerankResult(res);
      }
      setDurationMs(Date.now() - startedAt);
    } catch (err: unknown) {
      if (err instanceof Error && err.name === 'AbortError') return;
      setError(toFriendly(err));
    } finally {
      setLoading(false);
    }
  }

  function handleStop() {
    abortRef.current?.abort();
    setLoading(false);
  }

  const cardCls =
    'border-accent-200 bg-white shadow-card dark:border-accent-800 dark:bg-accent-900/70';
  const inputCls =
    'w-full rounded-lg border border-accent-200 bg-white px-3 py-2 text-sm text-accent-900 outline-none transition-colors placeholder:text-accent-400 focus:border-primary-500 focus:ring-2 focus:ring-primary-500/20 disabled:cursor-not-allowed disabled:opacity-60 dark:border-accent-800 dark:bg-accent-950/60 dark:text-accent-100';
  const labelCls = 'text-xs font-medium text-accent-600 dark:text-accent-300';

  return (
    <div className="space-y-6">
      {/* 标题 + 引导 */}
      <section className="rounded-lg border border-accent-200 bg-white px-5 py-4 shadow-card dark:border-accent-800 dark:bg-accent-900/70">
        <p className="text-xs font-medium text-primary-700 dark:text-primary-300">Console</p>
        <h1 className="mt-1 text-2xl font-bold tracking-normal text-accent-950 dark:text-white">
          多协议推理调试台
        </h1>
        <p className="mt-2 text-sm text-accent-500 dark:text-accent-400">
          Embeddings / Images / Rerank 协议调试。在
          <a
            href="/api-keys"
            className="mx-1 font-medium text-primary-600 underline-offset-2 hover:underline dark:text-primary-300"
          >
            API Keys
          </a>
          页创建 hk_ 密钥 → 这里粘贴 → 选模型 → 发送。与
          <a
            href="/chat"
            className="mx-1 font-medium text-primary-600 underline-offset-2 hover:underline dark:text-primary-300"
          >
            Playground（chat）
          </a>
          共用同一把 hk_ key，独立于登录会话。
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

      {/* 主体：左输入 + 右结果 */}
      <div className="grid gap-6 lg:grid-cols-[360px_1fr]">
        {/* 左：协议 / 模型 / 各协议输入区 */}
        <Card className={cn(cardCls, 'h-fit')}>
          <CardContent className="space-y-4 p-5">
            {/* tab 切换 */}
            <div className="inline-flex w-full rounded-lg border border-accent-200 bg-accent-50 p-1 dark:border-accent-800 dark:bg-accent-950/60">
              {(Object.keys(TAB_META) as ProtocolTab[]).map((t) => {
                const Icon = TAB_META[t].icon;
                return (
                  <button
                    key={t}
                    type="button"
                    onClick={() => switchTab(t)}
                    disabled={loading}
                    className={cn(
                      'flex flex-1 items-center justify-center gap-1 rounded-md px-2 py-1.5 text-[11px] font-medium transition-colors disabled:cursor-not-allowed',
                      tab === t
                        ? 'bg-primary-600 text-white shadow-sm'
                        : 'text-accent-600 hover:text-accent-900 dark:text-accent-300 dark:hover:text-white',
                    )}
                  >
                    <Icon className="size-3.5" />
                    {TAB_META[t].label}
                  </button>
                );
              })}
            </div>
            <p className="font-mono text-[11px] text-accent-400 dark:text-accent-500">
              {TAB_META[tab].endpoint}
            </p>

            {/* 模型选择 */}
            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <label className={labelCls}>模型</label>
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

            {/* 各协议输入区 */}
            {tab === 'embeddings' && (
              <div className="space-y-1.5">
                <label className={labelCls}>输入文本（每行一条 → 批量向量化）</label>
                <textarea
                  rows={6}
                  value={embedInput}
                  disabled={loading}
                  onChange={(e) => setEmbedInput(e.target.value)}
                  placeholder={'要嵌入的文本…\n多行将作为字符串数组批量提交'}
                  className={cn(inputCls, 'resize-y')}
                />
              </div>
            )}

            {tab === 'images' && (
              <>
                <div className="space-y-1.5">
                  <label className={labelCls}>提示词（Prompt）</label>
                  <textarea
                    rows={5}
                    value={imagePrompt}
                    disabled={loading}
                    onChange={(e) => setImagePrompt(e.target.value)}
                    placeholder="a serene teal lake at dawn, watercolor"
                    className={cn(inputCls, 'resize-y')}
                  />
                </div>
                <div className="grid grid-cols-2 gap-3">
                  <div className="space-y-1.5">
                    <label className={labelCls}>张数（n）</label>
                    <input
                      type="number"
                      min={1}
                      max={10}
                      value={imageN}
                      disabled={loading}
                      onChange={(e) => setImageN(Math.max(1, Number(e.target.value) || 1))}
                      className={inputCls}
                    />
                  </div>
                  <div className="space-y-1.5">
                    <label className={labelCls}>尺寸（size）</label>
                    <select
                      value={imageSize}
                      disabled={loading}
                      onChange={(e) => setImageSize(e.target.value)}
                      className={cn(inputCls, 'font-mono')}
                    >
                      {IMAGE_SIZES.map((s) => (
                        <option key={s} value={s}>
                          {s}
                        </option>
                      ))}
                    </select>
                  </div>
                </div>
              </>
            )}

            {tab === 'rerank' && (
              <>
                <div className="space-y-1.5">
                  <label className={labelCls}>查询（Query）</label>
                  <textarea
                    rows={2}
                    value={rerankQuery}
                    disabled={loading}
                    onChange={(e) => setRerankQuery(e.target.value)}
                    placeholder="检索意图，如：如何重置密码"
                    className={cn(inputCls, 'resize-y')}
                  />
                </div>
                <div className="space-y-1.5">
                  <label className={labelCls}>
                    候选文档（每行一条，{parsedRerankDocs.length} 条）
                  </label>
                  <textarea
                    rows={5}
                    value={rerankDocs}
                    disabled={loading}
                    onChange={(e) => setRerankDocs(e.target.value)}
                    placeholder={'文档一\n文档二\n文档三'}
                    className={cn(inputCls, 'resize-y')}
                  />
                </div>
                <div className="space-y-1.5">
                  <label className={labelCls}>top_n（可选，留空返回全部）</label>
                  <input
                    type="number"
                    min={1}
                    value={rerankTopN}
                    disabled={loading}
                    onChange={(e) => setRerankTopN(e.target.value)}
                    placeholder="如 3"
                    className={inputCls}
                  />
                </div>
              </>
            )}

            {/* 发送 / 停止 */}
            <div className="flex items-center justify-between border-t border-accent-200 pt-4 dark:border-accent-800">
              <span className="text-xs text-accent-400 dark:text-accent-500">
                {hasKey ? 'hk_ key 直连推理端点' : '需要先填入 hk_ 密钥'}
              </span>
              {!loading ? (
                <Button onClick={handleSend} disabled={!canSend}>
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
          </CardContent>
        </Card>

        {/* 右：结果区 */}
        <Card className={cn(cardCls, 'flex flex-col')}>
          <CardHeader className="flex flex-row items-center justify-between p-5 pb-3">
            <CardTitle className="text-base font-semibold tracking-normal text-accent-950 dark:text-white">
              {TAB_META[tab].label} 结果
            </CardTitle>
            {durationMs !== null && (
              <span className="text-xs text-accent-400 dark:text-accent-500">{durationMs} ms</span>
            )}
          </CardHeader>
          <CardContent className="flex flex-1 flex-col gap-4 p-5 pt-0">
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

            {loading && (
              <div className="flex min-h-[16rem] flex-col items-center justify-center gap-2 text-accent-400 dark:text-accent-500">
                <Loader2 className="size-6 animate-spin" />
                <p className="text-sm">请求中…</p>
              </div>
            )}

            {/* Embeddings 结果 */}
            {!loading && tab === 'embeddings' && (
              <EmbeddingsResultView result={embedResult} />
            )}
            {/* Images 结果 */}
            {!loading && tab === 'images' && <ImagesResultView result={imageResult} />}
            {/* Rerank 结果 */}
            {!loading && tab === 'rerank' && <RerankResultView result={rerankResult} />}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

// ── 结果子组件 ──────────────────────────────────────────────────────────────

function EmptyResult({ icon: Icon, text }: { icon: typeof Binary; text: string }) {
  return (
    <div className="flex min-h-[16rem] flex-col items-center justify-center gap-2 text-center text-accent-400 dark:text-accent-500">
      <Icon className="size-6 opacity-40" />
      <p className="text-sm">{text}</p>
    </div>
  );
}

function UsageRow({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-center gap-2 rounded-lg border border-accent-200 bg-accent-50/60 px-3 py-2 dark:border-accent-800 dark:bg-accent-950/40">
      {children}
    </div>
  );
}

function EmbeddingsResultView({ result }: { result: EmbeddingsResponse | null }) {
  if (!result) {
    return <EmptyResult icon={Binary} text="输入文本并发送，将展示向量维度与前几维数值。" />;
  }
  const items = result.data ?? [];
  return (
    <div className="space-y-3">
      <UsageRow>
        <Badge variant="outline" className="gap-1 text-[11px]">
          <Hash className="size-3" />
          {items.length} 条向量
        </Badge>
        {items[0]?.embedding && (
          <Badge variant="secondary" className="text-[11px]">
            维度 {items[0].embedding.length}
          </Badge>
        )}
        {result.usage?.prompt_tokens != null && (
          <Badge variant="outline" className="text-[11px]">
            prompt_tokens {result.usage.prompt_tokens}
          </Badge>
        )}
        {result.usage?.total_tokens != null && (
          <Badge variant="outline" className="text-[11px]">
            total_tokens {result.usage.total_tokens}
          </Badge>
        )}
        {result.model && (
          <span className="font-mono text-[11px] text-accent-400">{result.model}</span>
        )}
      </UsageRow>
      <div className="space-y-2">
        {items.map((item, i) => {
          const vec = item.embedding ?? [];
          const head = vec.slice(0, 8);
          return (
            <div
              key={item.index ?? i}
              className="rounded-lg border border-accent-200 bg-white p-3 dark:border-accent-800 dark:bg-accent-950/40"
            >
              <div className="mb-1 flex items-center justify-between text-xs text-accent-500 dark:text-accent-400">
                <span>#{item.index ?? i}</span>
                <span>{vec.length} 维</span>
              </div>
              <p className="break-all font-mono text-xs text-accent-700 dark:text-accent-300">
                [{head.map((n) => n.toFixed(5)).join(', ')}
                {vec.length > head.length ? ', …' : ''}]
              </p>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function ImagesResultView({ result }: { result: ImagesResponse | null }) {
  if (!result) {
    return <EmptyResult icon={ImageIcon} text="输入提示词并发送，将展示生成的图片或链接。" />;
  }
  const items = result.data ?? [];
  const imgTokens = result.usage?.input_tokens_details?.image_tokens;
  return (
    <div className="space-y-3">
      {(result.usage || items.length > 0) && (
        <UsageRow>
          <Badge variant="outline" className="text-[11px]">
            {items.length} 张
          </Badge>
          {result.usage?.input_tokens != null && (
            <Badge variant="outline" className="text-[11px]">
              input_tokens {result.usage.input_tokens}
            </Badge>
          )}
          {result.usage?.output_tokens != null && (
            <Badge variant="outline" className="text-[11px]">
              output_tokens {result.usage.output_tokens}
            </Badge>
          )}
          {imgTokens != null && (
            <Badge variant="secondary" className="text-[11px]">
              image_tokens {imgTokens}
            </Badge>
          )}
        </UsageRow>
      )}
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        {items.map((item, i) => {
          const src = item.url
            ? item.url
            : item.b64_json
            ? `data:image/png;base64,${item.b64_json}`
            : '';
          return (
            <div
              key={i}
              className="space-y-2 rounded-lg border border-accent-200 bg-white p-2 dark:border-accent-800 dark:bg-accent-950/40"
            >
              {src ? (
                // eslint-disable-next-line @next/next/no-img-element
                <img
                  src={src}
                  alt={`generated-${i}`}
                  className="h-48 w-full rounded-md object-cover"
                />
              ) : (
                <div className="flex h-48 items-center justify-center rounded-md bg-accent-100 text-xs text-accent-400 dark:bg-accent-900">
                  无图像数据
                </div>
              )}
              {item.url && (
                <a
                  href={item.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="block truncate text-[11px] text-primary-600 underline-offset-2 hover:underline dark:text-primary-300"
                >
                  {item.url}
                </a>
              )}
              {item.revised_prompt && (
                <p className="text-[11px] text-accent-400 dark:text-accent-500">
                  改写：{item.revised_prompt}
                </p>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function RerankResultView({ result }: { result: RerankResponse | null }) {
  if (!result) {
    return <EmptyResult icon={ListOrdered} text="填写查询与候选文档并发送，将按相关性分数排序展示。" />;
  }
  const results = [...(result.results ?? [])].sort(
    (a, b) => (b.relevance_score ?? 0) - (a.relevance_score ?? 0),
  );
  const max = results[0]?.relevance_score ?? 0;
  return (
    <div className="space-y-3">
      <UsageRow>
        <Badge variant="outline" className="gap-1 text-[11px]">
          <ListOrdered className="size-3" />
          {results.length} 条结果
        </Badge>
        {result.model && (
          <span className="font-mono text-[11px] text-accent-400">{result.model}</span>
        )}
      </UsageRow>
      <ol className="space-y-2">
        {results.map((r, rank) => {
          const score = r.relevance_score ?? 0;
          const pct = max > 0 ? Math.round((score / max) * 100) : 0;
          const text = rerankDocumentText(r.document);
          return (
            <li
              key={`${r.index}-${rank}`}
              className="rounded-lg border border-accent-200 bg-white p-3 dark:border-accent-800 dark:bg-accent-950/40"
            >
              <div className="mb-1.5 flex items-center justify-between gap-2">
                <span className="flex items-center gap-2 text-xs text-accent-500 dark:text-accent-400">
                  <Badge variant="secondary" className="text-[11px]">
                    #{rank + 1}
                  </Badge>
                  原序 index {r.index}
                </span>
                <span className="font-mono text-xs font-semibold text-primary-700 dark:text-primary-300">
                  {score.toFixed(4)}
                </span>
              </div>
              <div className="mb-1.5 h-1.5 w-full overflow-hidden rounded-full bg-accent-100 dark:bg-accent-900">
                <div
                  className="h-full rounded-full bg-primary-500"
                  style={{ width: `${pct}%` }}
                />
              </div>
              {text && (
                <p className="text-xs text-accent-700 dark:text-accent-300">{text}</p>
              )}
            </li>
          );
        })}
      </ol>
    </div>
  );
}
