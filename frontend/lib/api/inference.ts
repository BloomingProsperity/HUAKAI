// 推理多协议调试台数据层（Embeddings / Images / Rerank）。
// 鉴权：与 chat.ts / models.ts 一致——用 hk_ 客户 API key 作 Bearer
//   （localStorage 'huakai_api_key'），不走 userClient（session）也不走 admin token client。
// 错误：沿用 chat.ts 约定，失败抛 `HTTP <status>: <body>` 字符串，页面侧用
//   toFriendly() 还原成 ApiError 再经 friendlyMessage 翻译成中文。
// 端点形状均按后端 handler 真码确认（DisallowUnknownFields 不在这些 handler 上，
//   但仍只下发后端会消费的字段，避免无谓负载）：
//   - POST /v1/embeddings        请求 {model,input}            响应 {object,data:[{embedding,index}],model,usage}
//     （backend/internal/embeddingshttp/request.go:embeddingRequest{Model,Input}；
//      响应为上游 OpenAI 风原样透传，见 handler_test.go 断言 data[].embedding + usage.prompt_tokens）
//   - POST /v1/images/generations 请求 {model,prompt,n?,size?,quality?}
//     响应 {created?,data:[{url|b64_json}],usage?}
//     （backend/internal/imageshttp/request.go:imageRequest{Model,Prompt,N,Size,Quality}；
//      响应见 imageshttp/handler_test.go 断言 data[].url / data[].b64_json）
//   - POST /v1/rerank            请求 {model,query,documents[],top_n?,return_documents?}
//     响应 {results:[{index,relevance_score,document?:{text}}]}
//     （backend/internal/rerankhttp/request.go:rerankRequest；
//      响应见 rerankhttp/handler_test.go 断言 results[].relevance_score + .index + .document.text）

// 从 localStorage 读客户 API key（与 chat.ts / models.ts 用同一把 key）
function getCustomerAPIKey(): string {
  if (typeof window === 'undefined') return '';
  return localStorage.getItem('huakai_api_key') ?? '';
}

function inferenceHeaders(): Record<string, string> {
  const token = getCustomerAPIKey();
  return {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  };
}

// 统一 POST：成功返回已解析 JSON；失败抛 `HTTP <status>: <body>`（与 chat.ts 对齐）。
async function postJSON<T>(path: string, body: unknown, signal?: AbortSignal): Promise<T> {
  const resp = await fetch(path, {
    method: 'POST',
    headers: inferenceHeaders(),
    body: JSON.stringify(body),
    signal,
  });
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error(`HTTP ${resp.status}: ${text}`);
  }
  return resp.json() as Promise<T>;
}

// ── Embeddings ────────────────────────────────────────────────────────────
// input 后端接受 string 或 string[]（embeddingshttp/request.go:parseInput）。
export interface EmbeddingsRequest {
  model: string;
  input: string | string[];
}

export interface EmbeddingItem {
  object?: string;
  embedding: number[];
  index?: number;
}

export interface EmbeddingsUsage {
  prompt_tokens?: number;
  total_tokens?: number;
}

export interface EmbeddingsResponse {
  object?: string;
  data: EmbeddingItem[];
  model?: string;
  usage?: EmbeddingsUsage;
}

export function postEmbeddings(
  body: EmbeddingsRequest,
  signal?: AbortSignal,
): Promise<EmbeddingsResponse> {
  return postJSON<EmbeddingsResponse>('/v1/embeddings', body, signal);
}

// ── Images（generations）──────────────────────────────────────────────────
// 后端必填 model + prompt；n/size/quality 可选（imageshttp/request.go）。
// 不下发 stream（后端显式拒 stream:true）。
export interface ImagesRequest {
  model: string;
  prompt: string;
  n?: number;
  size?: string;
  quality?: string;
}

export interface ImageItem {
  url?: string;
  b64_json?: string;
  revised_prompt?: string;
}

// 上游可能回 token 计费明细（imageshttp/request.go:parseTokenImageUsage）。
export interface ImagesUsage {
  input_tokens?: number;
  output_tokens?: number;
  input_tokens_details?: {
    image_tokens?: number;
  };
}

export interface ImagesResponse {
  created?: number;
  data: ImageItem[];
  usage?: ImagesUsage;
}

export function postImageGenerations(
  body: ImagesRequest,
  signal?: AbortSignal,
): Promise<ImagesResponse> {
  return postJSON<ImagesResponse>('/v1/images/generations', body, signal);
}

// ── Rerank ────────────────────────────────────────────────────────────────
// 后端必填 model + query + documents（1..1000 条，rerankhttp/request.go）。
// documents 元素后端接受 string 或对象（json.RawMessage），这里发 string[] 即可。
export interface RerankRequest {
  model: string;
  query: string;
  documents: string[];
  top_n?: number;
  return_documents?: boolean;
}

export interface RerankResultItem {
  index: number;
  relevance_score: number;
  document?: { text?: string } | string;
}

export interface RerankResponse {
  results: RerankResultItem[];
  model?: string;
}

export function postRerank(
  body: RerankRequest,
  signal?: AbortSignal,
): Promise<RerankResponse> {
  return postJSON<RerankResponse>('/v1/rerank', body, signal);
}
