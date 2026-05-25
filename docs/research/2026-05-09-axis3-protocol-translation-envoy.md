# Axis-3 协议翻译 — envoy-ai-gateway 源读 (specifier lane)

## 元信息
- **Lane**: specifier (axis-3 协议翻译聚焦)
- **Agent**: general-purpose
- **UTC timestamp**: 2026-05-09T14:51Z
- **Reference**: envoyproxy/ai-gateway@4d3eae8b (Apache-2.0)
- **First-cite recency check**: HEAD `4d3eae8b35c4ccc41643d94bb5f69280846561b0` `pushed_at` 2026-05-08 18:07Z (UTC-4) → < 24h, recency OK; 仓库非 archived。
- **License posture**: Apache-2.0；MIT-compatible (允许借鉴 mechanism, 但本 lane **只输出语义抽象**, 不复制函数名 / 字段名 / 注释 / 文件路径 / 算法行序)。
- **Prior lane delta**: `docs/research/2026-05-09-source-read-helicone-envoy-allapihub.md` (Lane C) 覆盖了 envoy-ai-gateway 的 retry/failover (BackendTrafficPolicy 委托) 与 endpoint picker；本 lane 完全聚焦协议翻译矩阵、CRD schema、ext_proc 数据面、流式与多模态、计费抽象 6 项轴-3 维度。

## 0. 一句话定位

envoy-ai-gateway 把**协议翻译做成 Envoy ext_proc 外挂的 gRPC 服务**：Envoy 数据面只负责 HTTP/Cluster routing + retry，**所有 vendor schema 转换都在 ext_proc Go 进程里完成**，由 K8s CRD 声明 input schema (AIGatewayRoute) 与 output schema (AIServiceBackend.spec.schema)，框架按 (input, output) 二元组在请求时挑选 translator 实例。

它的轴-3 抽象是**两层泛型 + 一张方阵**:
- 第一层泛型 `Translator[ReqT, SpanT]` — 8 类 endpoint × ReqT；
- 第二层 dispatch — `Spec.GetTranslator(schema, modelOverride)` 在 ext_proc 设置后端时返回具体 (Canonical → Vendor) translator。

参见后述 §2.

---

## 1. 协议翻译在哪一层 (Q1)

### 1.1 数据面 vs 控制面分工
- **Envoy 数据面**: 走 HTTP filter chain + cluster, 负责 retry (BackendTrafficPolicy)、超时、负载均衡、route match (含 `x-ai-eg-model` header 提取)。本 lane **不重复**这一层 — 见 prior lane。
- **ext_proc 服务 (Go 进程)**: 通过 gRPC `ExternalProcessor.Process` stream 接收 HeaderMap / Body chunks，**所有 vendor 翻译在此完成**。这是一个 **out-of-process sidecar**, 不是 in-stream byte transform，也不是 Envoy 原生 C++ filter。
  - 入口: `internal/extproc/server.go:128` 的 `(s *Server) Process(stream)`。
  - 数据流：每收到一个 `ProcessingRequest`，按 `RequestHeaders / RequestBody / ResponseHeaders / ResponseBody` 分派到 `Processor` 接口的 4 个方法 (`internal/extproc/processor.go:25`)。

### 1.2 Filter 是 buffer-then-rewrite，不是 in-stream byte transform
- 请求方向: Envoy 把 body 累积到 ext_proc, **整段缓冲**后调 `Translator.RequestBody(raw, parsedBody, forceFlag)` 返回 `(newHeaders, mutatedBody)`。然后 ext_proc 用 `CommonResponse.Status = CONTINUE_AND_REPLACE` 一次性下发 (`internal/extproc/processor_impl.go:387-398`)。
  - 实质: **整 body 替换**, 而非 streaming 改写。
  - 设计后果: request body 必须能放进单进程内存。
- 响应方向 (流式): `ProcessResponseHeaders` 时如果是 stream 且 status==200, **覆盖 ProcessingMode 为 `STREAMED`** (`internal/extproc/processor_impl.go:425-434`)。之后每收到一个 chunk 就走 `ProcessResponseBody → translator.ResponseBody(... endOfStream)`，translator 内部维护流状态 (e.g., AWS Bedrock 累积 buffer 的 `bufferedBody []byte`，OpenAI→Anthropic 的 `streamState` SSE re-parser)。
  - 这是 **chunked rewrite + stateful translator**, 不是 byte-level pass-through.

### 1.3 流式响应的具体路径
三类 vendor 的流式 codec 不一样, 框架按 vendor translator **各自实现解码再重编码**:
- **AWS Bedrock binary EventStream**: 用 `aws-sdk-go-v2/aws/protocol/eventstream.Decoder` 累积 chunk 解析消息帧, 再转 OpenAI SSE; content-type 从 `application/vnd.amazon.eventstream` 改写为 `text/event-stream` (`internal/translator/openai_awsbedrock.go:592-599, 827-852`)。
- **OpenAI SSE → Anthropic 事件**: translator 自维护 SSE 解析状态机 (`anthropicToOpenAIV1ChatCompletionTranslator.streamState`, `internal/translator/anthropic_openai.go:80-86, 154-184`)，跨多个 chunk 凑出完整事件后，翻成 Anthropic `event: message_start / content_block_delta / ...` 帧。
  - **关键设计**: 即使本 chunk 凑不出完整 Anthropic 事件，也返回**非 nil 空 body**, 让 Envoy 把原 OpenAI chunk 抑制掉, 不直通透 (`anthropic_openai.go:172`)。
- **gzip 压缩流**: ext_proc 自行 stateful 解压 — 累积 raw compressed bytes, 每次从头解压, 只回送增量 (`processor_impl.go:551-572`)。这绕开了 mid-stream 创建 gzip decoder 的缺陷。

### 1.4 总结 Q1
**HUAKAI 视角**: envoy-ai-gateway 走 Envoy + ext_proc out-of-process 是 K8s 部署习惯，**HUAKAI 是 in-process gateway**, 这一层完全不直接借鉴 — 但 §2 的接口分层和 §6 的二元组 dispatch 表是可移植的设计骨架。

---

## 2. Provider 表达 (Q2)

### 2.1 双层 CRD：Route × Backend × AuthPolicy
- `AIGatewayRoute` (`api/v1alpha1/ai_gateway_route.go:37`) — 用户 facing input schema, 定义客户端访问的 endpoint 形式 + LLMRequestCost 计费规则。
- `AIServiceBackend` (`api/v1alpha1/ai_service_backend.go:28-81`) — 单后端的 output schema 描述, 含:
  - `Spec.APISchema` → `VersionedAPISchema {Name, Version, Prefix}` (`shared_types.go:15-44`)
  - `Spec.BackendRef` → 指向 Envoy Gateway `Backend` 资源 (网络层 endpoint)
  - `Spec.HeaderMutation` / `Spec.BodyMutation` → 后端粒度 header/body 改写
- `BackendSecurityPolicy` (`backendsecurity_policy.go:15-100`) — 鉴权独立 CRD, 6 种 type: `APIKey`, `AWSCredentials`, `AzureAPIKey`, `AzureCredentials`, `GCPCredentials`, `AnthropicAPIKey`，每种对应一个 `BackendAuthHandler` 实现 (`internal/backendauth/*.go`)。

### 2.2 描述 dimension 矩阵

| Dimension | 字段位置 | 影响 |
|---|---|---|
| 协议 schema 名 | `VersionedAPISchema.Name` enum (`shared_types.go:18`) | 决定 translator 选型 (8 个 enum 值: OpenAI/Cohere/AWSBedrock/AzureOpenAI/GCPVertexAI/GCPAnthropic/Anthropic/AWSAnthropic) |
| 协议版本 | `VersionedAPISchema.Version` 自由字符串 | Azure / AWSAnthropic 用其作 API version 路径段 |
| 路径前缀 | `VersionedAPISchema.Prefix` | 同 schema 的不同 prefix 路径 (e.g., Gemini OpenAI 兼容用 `/v1beta/openai`, Cohere 用 `/compatibility/v1`) |
| Model 重命名 | `BackendRef.ModelNameOverride` (`ai_gateway_route.go:319`) | 替换请求里的 model 名,不复制原 model |
| Header/body 改写 | `HeaderMutation` / `BodyMutation` (`ai_service_backend.go:69-77`, `ai_gateway_route.go:327-336`) | route-level 优先, backend-level 次之 |
| 鉴权 | 关联 `BackendSecurityPolicy` 引用 | 独立 CRD, 路由策略时 attach |
| 网络层 endpoint | `BackendRef → gateway.envoyproxy.io/Backend` | hostname / TLS / SNI 不在本 CRD |
| 计费规则 | `AIGatewayRoute.LLMRequestCosts[]` (`ai_gateway_route.go:193`) + `GatewayConfig.GlobalLLMRequestCosts` | 7 种 cost type + CEL 自定义表达式 |

### 2.3 Schema-vs-code 比例
- 加新 vendor schema = **CRD enum 值** + **新 translator 文件** + **endpointspec.go 里 switch case 一行**:
  - CRD enum 改动: `shared_types.go:18` 的 kubebuilder validation 行
  - translator: 类似 `openai_<vendor>.go` 一对 (request 转换, response 转换, error 转换, redactor 接口)
  - dispatch: `endpointspec.go` 各 endpoint 的 `GetTranslator()` switch 加 case
- 不需要改 ext_proc 主流程, 不需要改 Envoy CRD。**插件性是有的, 但不是运行时插件** — 是编译期插件 (Go switch case 闭集合)。

### 2.4 总结 Q2
- **架构升级**: 协议描述 = `Name + Version + Prefix` 三元组 + 独立 BackendSecurityPolicy + 独立 ModelNameOverride/HeaderMutation/BodyMutation, 这种**关注点分离**比 OneAPI 的"渠道一张大表"清楚一个量级。HUAKAI 已有的 vendor 抽象可以借鉴这种 Schema 三元组结构 (Name/Version/Prefix)。
- **算法升级 (无)**: 这只是分层不是算法。
- **生态升级**: BodyMutation 用 JSON path + JSON value 字符串, 用户可以不写代码静态注入 service_tier 这类 vendor-specific 字段 (`ai_gateway_route.go:373-446`)。HUAKAI 可以借鉴这种 declarative body mutation 而非 hardcode。

---

## 3. Canonical 选型 (Q3)

### 3.1 Canonical 不是单一 OpenAI — 是按 endpoint 分

`internal/translator/translator.go:100-117` 定义 8 个 endpoint translator 类型别名:

| Endpoint | Canonical Request 类型 | Canonical Span 类型 |
|---|---|---|
| `/v1/chat/completions` | `openai.ChatCompletionRequest` | `tracingapi.ChatCompletionSpan` |
| `/v1/embeddings` | `openai.EmbeddingRequest` | `tracingapi.EmbeddingsSpan` |
| `/v1/completions` | `openai.CompletionRequest` | `tracingapi.CompletionSpan` |
| `/v1/messages` | `anthropic.MessagesRequest` | `tracingapi.MessageSpan` |
| `/v1/responses` | `openai.ResponseRequest` | `tracingapi.ResponsesSpan` |
| `/v1/images/generations` | `openai.ImageGenerationRequest` | `tracingapi.ImageGenerationSpan` |
| `/v1/audio/speech` | `openai.SpeechRequest` | `tracingapi.SpeechSpan` |
| `/v2/rerank` | `cohere.RerankV2Request` | `tracingapi.RerankSpan` |

**关键发现**: 不是"全部转成 OpenAI 然后再分发"。而是**按 endpoint 选 canonical**:
- chat/completions endpoint **canonical = OpenAI**, 但 messages endpoint **canonical = Anthropic** (`endpointspec.go:339-353`)。
- rerank endpoint **canonical = Cohere** (`endpointspec.go:374-381`)。

### 3.2 同 endpoint 内的二元组方阵

以 `/v1/chat/completions` 为例 (`endpointspec.go:128-145`):

| Backend schema | 选用 translator | 实质操作 |
|---|---|---|
| OpenAI | `NewChatCompletionOpenAIToOpenAITranslator` | passthrough (315 行) |
| AWSBedrock | `NewChatCompletionOpenAIToAWSBedrockTranslator` | OpenAI → Converse JSON (1075 行) |
| AWSAnthropic | `NewChatCompletionOpenAIToAWSAnthropicTranslator` | OpenAI → Anthropic on Bedrock |
| AzureOpenAI | `NewChatCompletionOpenAIToAzureOpenAITranslator` | passthrough + path/auth 调整 |
| GCPVertexAI | `NewChatCompletionOpenAIToGCPVertexAITranslator` | OpenAI → Vertex generateContent |
| GCPAnthropic | `NewChatCompletionOpenAIToGCPAnthropicTranslator` | OpenAI → Anthropic on Vertex |

`/v1/messages` 反向 (`endpointspec.go:339-353`):
| Backend schema | 选用 translator |
|---|---|
| Anthropic | passthrough (`NewAnthropicToAnthropicTranslator`, 323 行) |
| GCPAnthropic | header/path 调整 |
| AWSAnthropic | header/path 调整 |
| OpenAI | **Anthropic → OpenAI** (`anthropicToOpenAIV1ChatCompletionTranslator`, 280 行的 wrapper, 实际转换发生在 `anthropic_helper.go:1078` 等) |

### 3.3 不可保真字段的处理 — Anthropic thinking/reasoning

**OpenAI → Bedrock 路径** (`openai_awsbedrock.go:57-78, 129-135`):
- OpenAI 的 `Thinking` union (`OfEnabled / OfDisabled`) 被映射到 Bedrock 的 `AdditionalModelRequestFields["thinking"]` map, **保留语义但用 vendor-extension 路径承载**。

**OpenAI → Bedrock 响应中 reasoning** (`openai_awsbedrock.go:798-804, 938-957`):
- Bedrock `ReasoningContent` 被翻成 OpenAI 的 `ReasoningContentUnion` (注: OpenAI 主线没这字段，是项目自己加在 `apischema/openai` 包里的 vendor 扩展)。
- `RedactedContent` 在不同方向有强制类型: bedrock 必须是 `[]byte`, anthropic 把 string 转为 base64 包裹的 `RedactedContent: []byte(output.Data)` (`anthropic_helper.go:1246`)。

**Anthropic → OpenAI 路径**: 反向时 thinking 通过 `anthropic_helper.go:637-654` 的 `mapReasoningEffortToOutputConfigEffort` 映射 OpenAI 的 `reasoning_effort` (low/medium/high/xhigh) → Anthropic 的 `output_config.effort`。

**关键设计**: 项目用一个**扩展过的 OpenAI schema** 作 canonical (在 `internal/apischema/openai/openai.go` 里定义 8400+ 行), 把 Anthropic / Bedrock 的 vendor-specific 概念都补进 OpenAI 类型 (e.g., `ReasoningContent`, `AnthropicContentFields`, `Thinking`, `CachePoint`)。这是个**胖 canonical 模型**, 牺牲 OpenAI 纯度, 换"任何 vendor 字段都有地方放"。

### 3.4 总结 Q3
- **架构升级**: per-endpoint canonical (chat 用 OpenAI, messages 用 Anthropic) 比"统一 OpenAI" 更诚实 — Anthropic 用户用 `/v1/messages` 时 client SDK 不需要改。HUAKAI 已是双协议入口 (OpenAI + Anthropic), 这种 per-endpoint canonical 思路可以借鉴：**协议入口决定 canonical, 不要强行统一**。
- **算法升级 (无)**: 还是 schema 转 schema, 没新算法。
- **生态升级**: 胖 canonical (OpenAI ⊕ vendor extensions) 是显式选择, 不是污染。HUAKAI 如果统一到 OpenAI 路径, 也需要做这个 trade-off：要么扩字段 (envoy 路线), 要么吞字段 (litellm/portkey 路线), 不能两全。

---

## 4. Tool call & multi-modal (Q4)

### 4.1 Tool call 跨协议互转：data plane (translator) 内做
- **OpenAI tool_calls → Bedrock ToolUse block** (`openai_awsbedrock.go:298-419`):
  - tool_calls.arguments 字符串 → JSON unmarshal → Bedrock `ToolUseBlock.Input` map[string]any
  - tool_choice 三态 (`auto / required / specific`) 映射到 Bedrock 的三种 ToolChoice 子类型, **特例**: 当 model 名含 "anthropic" 且 "claude" 时, string tool_choice 被解释为 Anthropic 的 specific tool name (`openai_awsbedrock.go:202-213`) — 这是个 vendor-on-vendor 的特判。
- **OpenAI tool_calls → Bedrock 流式**: tool_call 是按 index 串接的 SSE chunk, 用 `o.toolIndex` 累计 (`openai_awsbedrock.go:933, 962-981, 995-1000`)。
- **Bedrock ToolUse → OpenAI tool_call** (`openai_awsbedrock.go:623-641`): 反向转 string `Arguments`，类型固定 `function`。

**关键设计**: tool call **完全在 translator 内做** (data plane), 不在 control plane (CRD) — 控制面不感知 tool 语义。

### 4.2 多模态 — 图片
- 入口在 `openai_awsbedrock.go:256-291`: OpenAI `OfImageURL.ImageURL.URL` 被 `parseDataURI` 解析出 `(contentType, []byte)` —— 仅支持 data URI, **不下载远程 URL**。
- 然后映射 4 种 image format (png/jpeg/gif/webp), 写入 Bedrock `ImageBlock.Source.Bytes`。其他 contentType 直接报 `ErrInvalidRequestBody`。
- 还会跟 `getCachePoint(AnthropicContentFields)` 配套, 在图片块后追加 cache 标记块 (`openai_awsbedrock.go:285-290`)。

### 4.3 文件上传
- `endpointspec.go:580-588` 看 redact 逻辑表明 OpenAI `OfFile.File.FileData` 被识别 (是 base64 字符串)。但具体 vendor translator 里**没看到**专门处理 `OfFile` 的 case (Bedrock translator user message 分支只识别 `OfText` / `OfImageURL`, `openai_awsbedrock.go:243-292`)。
- 推断: 文件类型在大多数 vendor 上**还没接通**, 走默认 fallthrough 不报错 (具体行为 TODO，需要更深读)。

### 4.4 总结 Q4
- **架构升级**: tool call 逻辑放在 translator 而非 control plane 是合理的 — control plane 不应理解 tool 语义。HUAKAI 的 tool call 反向同样应该在每个 vendor adapter 里做。
- **算法升级 (vendor-on-vendor 特判)**: AWSBedrock translator 在 `tool_choice` 字符串值上做 model-name 嗅探 (`anthropic`+`claude` → specific tool, `openai_awsbedrock.go:207`) 是 fragile 的; HUAKAI 不要复制这种 if-model-name 嗅探, 应该走 schema 显式声明。
- **生态升级 (cache point 平行轨)**: 每个 message/image/tool 块都伴随一个可选 cache marker 块 (`getCachePoint(AnthropicContentFields)`)，把 Anthropic 的 prompt cache 控制 block 化、可组合。HUAKAI 的 PASR cache-aware 当前只在 routing 层；这种 inline cache marker 机制可考虑在 attempt 内传递 cache hint。

---

## 5. Failover 与 protocol 关系 (Q5)

### 5.1 Failover 的语义层级
- Envoy 数据面 retry 由 `BackendTrafficPolicy` 控制 (Envoy Gateway 原生), **跨同 schema 后端的 retry 是免费的** — HTTP 层重发即可。
- 跨 schema failover (e.g., Anthropic → OpenAI fallback): **不在 data plane 做**，而是通过 `routerProcessor.upstreamFilterCount` 计数器和 `forceBodyMutation` 标志控制 (`internal/extproc/processor_impl.go:94, 99-101, 297-299, 326-327`)。

### 5.2 重试时 body 的命运
关键实现 `(u *upstreamProcessor).ProcessRequestHeaders` (`processor_impl.go:307-398`):
- 每次 upstream filter 进入时:
  - `u.parent.upstreamFilterCount++` (在 `SetBackend`, line 585)
  - `forceBodyMutation := u.onRetry() || u.parent.forceBodyMutation` (line 326)
  - **关键**: 重新拿 `u.parent.originalRequestBodyRaw` (路由层缓存的原始客户端 body) 调 `u.translator.RequestBody(...)` 重新翻译 (line 327)
- 选 translator 在 `SetBackend` (line 599): `u.translator, err = u.parent.eh.GetTranslator(backend.Backend.Schema, u.modelNameOverride)`

**结论**: 当 Envoy 重选 backend 时 (failover, 不论同 schema 还是跨 schema), ext_proc 用**新 backend 的 schema** 重新生成 translator, 然后**用客户端原始 body 重新跑 RequestBody 转换**。**body 是按新协议重写的, 不保留前一次的格式**。

### 5.3 边界情况
- 如果 retry 仍是同 schema (e.g., OpenAI primary → OpenAI secondary), translator 还是 OpenAI→OpenAI passthrough, body 几乎不变 (除非 ModelNameOverride 不同)。
- 如果跨 schema (e.g., Bedrock → OpenAI), translator 实例换, body 重新整张构造, 这是为什么 `originalRequestBodyRaw` 一直保留。

### 5.4 总结 Q5
- **架构升级**: 重试时 raw body 持久 + translator 重选 = "记忆原始意图, 按新协议复述" — HUAKAI 的多 vendor failover 应该走同样模式 (`原始客户端 body` 而非 `上一次翻译后 body`)，否则跨 vendor fallback 会反复地 vendor-A → vendor-B → vendor-C 重塑请求, 误差累积。
- **算法升级 (流式中段失败)**: envoy-ai-gateway 这条路径只能在 RequestHeaders 阶段重试; 如果**响应已开始流式输出再失败**, framework **不做 mid-stream fallback**。HUAKAI 的 mid-stream fallback continuation prompt (我们差异化卖点) 在 envoy 里不存在 — 这是个真实 delta。
- **生态升级**: `forceBodyMutation` 标志在两种情况都置位 (重试 OR streaming + IncludeUsage 注入) (`processor_impl.go:325`) — 把"必须改 body"的两个不相关场景合并成一个布尔。HUAKAI 类似 retry 标志可以借这个简化点。

---

## 6. Token 计费 normalize (Q6)

### 6.1 Cost type 闭集合 + CEL 逃生口
- 7 种内置 cost type (`shared_types.go:138-152`): InputToken / OutputToken / CachedInputToken / CacheCreationInputToken / TotalToken / ReasoningToken / CEL。
- 每个 vendor translator 都返回 `metrics.TokenUsage` 结构, **每种 cost 是独立的可选 setter** (`processor_impl.go:706-756`):
  - InputTokens / CachedInputTokens / CacheCreationInputTokens / OutputTokens / TotalTokens / ReasoningTokens — 分别 6 个 getter。
- vendor 没的 cost 字段就不 set, 读时返回 0 (e.g., OpenAI 没 CacheCreationInputTokens, 就只返回 InputTokens 和 OutputTokens)。

### 6.2 vendor → canonical 映射点
- AWSBedrock `Usage` (`openai_awsbedrock.go:712-714, 875-877`): `metrics.ExtractTokenUsageFromExplicitCaching(InputTokens, OutputTokens, CacheReadInputTokens, CacheWriteInputTokens)` 直接四参数, framework 内部把 `CacheRead → CachedInputTokens`, `CacheWrite → CacheCreationInputTokens`。
- Anthropic / Anthropic-on-Vertex / Anthropic-on-Bedrock 也都走 `ExtractTokenUsageFromExplicitCaching`, 字段名一致。
- OpenAI `Usage.PromptTokens / CompletionTokens` 通过同函数 nil/nil 缓存字段。

### 6.3 CEL 逃生
- `LLMRequestCostTypeCEL` 让用户写 CEL 表达式 (`shared_types.go:108-132`), 暴露 model / backend / 5 个 token counter 变量, 输出 uint64 — 用来表达 "input + output*0.5 if model=='llama'" 类自定义计费。
- 每条 cost 规则可以是固定 type **或** CEL, 在 `evalCost` switch 里分发 (`processor_impl.go:706-756`)。

### 6.4 总结 Q6
- **架构升级**: 计费 cost type 是 enum + CEL 逃生口, 既约束又能扩展。HUAKAI 的计费层 (per attempt) 已经存在, 但是否暴露 CEL 让用户表达 "premium tier 加 25%" 这种规则是个有意思的抽象升级方向。
- **算法升级 (cache token 三分法)**: `CachedInputToken` (读) + `CacheCreationInputToken` (写) 分开计是 Anthropic prompt cache 的精度 — HUAKAI 的 PASR cache-aware A2 (locality+headroom) 是 routing 信号; cost 维度的 cache token 三分法在 HUAKAI 是否落账尚未明示, 这里是 envoy 的成熟点。
- **生态升级**: Cost 通过 Envoy DynamicMetadata `io.envoy.ai_gateway` namespace 写出, 任何 BackendTrafficPolicy 的 Global rate limit 规则可以直接消费——这是把 LLM 计费"标准化为 Envoy 的限流原语"。HUAKAI 自己持有 quota / billing 模块, 不走这条路, 但**把 token cost 表达为可被多个下游消费的 metadata** 这个 pattern 值得借鉴 (e.g., HUAKAI internal observability + billing 共用同一份 token usage 结构)。

---

## 7. Envoy 模式 vs HUAKAI in-process 兼容性

### 7.1 不可借鉴的部分
- ext_proc gRPC 服务化 — HUAKAI in-process 不需要这层 RPC 边界, 直接函数调用即可。
- xDS / EnvoyPatchPolicy / HTTPRouteFilter — K8s controller plane 跟 HUAKAI 部署模型不匹配。
- BackendTrafficPolicy retry — Envoy 提供, HUAKAI 自己写。
- DynamicMetadata bridge — Envoy 特定, HUAKAI 内部直接 in-memory pass。
- ProcessingMode 协议 — 是 ext_proc gRPC 协议特殊机制, in-process 没必要。

### 7.2 强兼容、可直接借鉴的 mechanism

| envoy-ai-gateway mechanism | 引用 | HUAKAI 借鉴方式 |
|---|---|---|
| `Translator[ReqT, SpanT]` 泛型接口 (4 方法: RequestBody / ResponseHeaders / ResponseBody / ResponseError) | `translator/translator.go:41-76` | 替换 HUAKAI 现有 vendor adapter 接口为同形泛型, 强制每个 vendor 实现 4 钩子 |
| `Spec[ReqT, RespT, RespChunkT]` endpoint × schema 二元组 dispatch | `endpointspec/endpointspec.go:37-78` | HUAKAI 的协议入口 (OpenAI/Anthropic) × backend schema (各 vendor) 也是二元组, 用同样 switch 编译期闭集合 |
| Per-endpoint canonical (chat=OpenAI, messages=Anthropic) | `endpointspec.go:128, 339` | HUAKAI 已是双入口, 验证我们走对了, 不要为了"统一"再做 OpenAI 单 canonical 折腾 |
| 胖 canonical (OpenAI ⊕ vendor extensions like ReasoningContent / AnthropicContentFields / Thinking) | `apischema/openai/openai.go` (8405 行) | HUAKAI 如要扩 OpenAI 类型承载 anthropic 字段, 这个权衡(显式扩字段) 比 silently drop 好 |
| `forceBodyMutation` 标志 (重试 OR streaming usage 注入两路并 1 标志) | `processor_impl.go:325` | 简化 HUAKAI 重试路径的"是否重写 body" 判断 |
| `originalRequestBodyRaw` 在 router 层缓存 (跨重试不变) | `processor_impl.go:92, 246` | HUAKAI 的 attempt 跨 vendor fallback 时, 必须保留客户端原始 body, 不要用上一 attempt 的翻译后 body |
| Cache point 内联 marker (`getCachePoint(AnthropicContentFields)`) 跟在 message/image 块后 | `openai_awsbedrock.go:251-254, 285-290` | HUAKAI 已实现路由层 cache locality, 但请求 body 内的 cache hint 控制可借鉴, 让用户精细标 cache 边界 |
| `ProcessingMode_STREAMED` mode override 仅在 stream + 200 时启用 | `processor_impl.go:425-428` | HUAKAI in-process: 类比是"流式翻译只在流式响应启用"的早期判断, 可少一次 buffering |
| 整 body 替换 (CONTINUE_AND_REPLACE 语义) | `processor_impl.go:392` | 我们 in-process 反而更容易 — 但**记忆这个边界**: in-process 也应该把"翻译"整体当成原子操作, 不要部分翻译部分 passthrough |
| Stateful gzip stream decompression (累积 + 重头解压 + 增量返回) | `processor_impl.go:551-572` | HUAKAI 暂未支持 vendor 端 gzip 响应, 真要支持时这个模式直接抄 |
| AWS EventStream binary frame decoder (`aws-sdk-go-v2/aws/protocol/eventstream`) | `openai_awsbedrock.go:19, 832` | 如果 HUAKAI 要支持 Bedrock 流式, **直接用同一个 SDK 包**, 不要自己写 binary frame parser |
| Cost type 7 enum + CEL 逃生 | `shared_types.go:138-152` | 计费规则的"内建 + DSL 逃生"模式可借鉴; CEL 比自定义 DSL 安全得多 (有官方实现) |

### 7.3 不应借鉴 (反面教材)
- 在 translator 里按 model 名做字符串嗅探 (`if strings.Contains(model, "anthropic") && strings.Contains(model, "claude")`, `openai_awsbedrock.go:207`) — fragile, model 命名规则一变就坏。HUAKAI 必须在 schema 字段里显式声明能力。
- 整 body buffer + 替换的内存代价 — in-process 的 HUAKAI 走 streaming 翻译会更省内存, 但这是 mechanism 不同, 不是教训。

---

## 8. 三维分类 (架构 / 算法 / 生态)

| 借鉴点 | 维度 | HUAKAI delta |
|---|---|---|
| Translator 泛型接口 4 钩子 | 架构 | 强制 vendor adapter 形态; HUAKAI 当前接口可能更松散 |
| Endpoint × schema 二元组 dispatch | 架构 | 把 dispatch 表显式化, 编译期可枚举 |
| Per-endpoint canonical | 架构 | 验证 HUAKAI 双入口设计 |
| 胖 canonical 模型 | 架构 | 决策点：扩字段 vs 吞字段 |
| forceBodyMutation 单标志 | 算法 | 简化重试判断 |
| originalRequestBodyRaw 跨重试持久 | 算法 | 跨 vendor fallback 必须保留原始 body, 防止误差累积 |
| 流式中段不可回退 | 算法 | envoy 没有, HUAKAI 的 mid-stream fallback continuation prompt 是真差异 |
| Bedrock binary EventStream 复用 SDK | 算法 | 别自己写 frame parser |
| Cost type 7 枚举 + CEL | 算法 + 生态 | 内建 + DSL 逃生 |
| Cache point 内联 marker | 算法 + 生态 | 请求体内细粒度 cache hint |
| BackendSecurityPolicy 独立 CRD | 生态 | HUAKAI 的鉴权可以同样独立配置, 不混在 backend 描述里 |
| Header/Body Mutation 声明式 | 生态 | 让用户不写代码静态注入 vendor 字段 |
| LLMRequestCost 通过 metadata 暴露 | 生态 | 把 token usage 标准化为多消费者数据 |

---

## 9. 关键差异化对照 (HUAKAI vs envoy-ai-gateway)

| 能力 | envoy-ai-gateway | HUAKAI 现状/规划 | delta |
|---|---|---|---|
| 协议翻译位置 | ext_proc gRPC out-of-process | in-process Go gateway | HUAKAI 减一层 RPC 边界, 延迟低; envoy 部署运维生态熟 |
| Canonical 选型 | per-endpoint (chat=OpenAI, messages=Anthropic) | 双协议入口 | 同设计哲学, 是 ✓ |
| 跨 schema fallback | 重选 translator + 重翻原始 body, 不支持 mid-stream | mid-stream continuation prompt 合成 (规划) | HUAKAI 算法升级真差异 |
| 流式 codec | Bedrock EventStream / SSE / gzip 各自实现 | 走 vendor 原生 SSE 直通为主 | HUAKAI 简单, envoy 全 |
| Tool call 翻译 | translator 内做, 含 vendor 嗅探 | translator 内做, schema 显式 | HUAKAI 显式声明 (无 model 名嗅探) 更稳 |
| 多模态图片 | data URI only, 不下载远程 URL | (待确认) | HUAKAI 应明确策略 |
| 计费 cost 表达 | 7 enum + CEL | 现有 token 三分法 + per attempt | CEL 表达式可借鉴 |
| Cache hint | inline marker (CachePoint) per content block | 路由层 PASR cache locality | 两层不同, 可叠加 |
| 鉴权抽象 | 独立 BackendSecurityPolicy CRD, 6 type handler | (待对照) | 独立性是好设计 |
| 协议描述 | (Name, Version, Prefix) 三元组 | vendor enum | 三元组更细粒度 |

**dimension-tagged delta 总结**:
- **架构升级**: per-endpoint canonical (验证 HUAKAI 设计)、(Name/Version/Prefix) 三元组、translator 4 钩子接口、独立鉴权 CRD。
- **算法升级**: originalRequestBodyRaw 跨 vendor 重翻、cost type CEL 逃生、Bedrock EventStream SDK 复用。
- **生态升级**: HeaderMutation/BodyMutation 声明式、cache point 内联 marker、token usage 标准化为多消费者 metadata、cost type 闭集合 + CEL。

---

## 10. 推荐的 HUAKAI 行动项 (按优先级)

1. **HIGH — 落地 translator 4 钩子接口**: 用 `Translator[ReqT, SpanT]` 这种泛型接口 (RequestBody / ResponseHeaders / ResponseBody / ResponseError) 重塑 HUAKAI vendor adapter, 让每个 vendor 实现强制对齐。引用 `internal/translator/translator.go:41-76` 作 reference, 不复制函数名/字段名。
2. **HIGH — endpoint × schema 二元组 dispatch 表显式化**: 类似 `endpointspec.go` 的每 endpoint 一个 GetTranslator switch, 强迫"加 vendor = 改 dispatch 表"显式而非反射。
3. **HIGH — 跨 vendor fallback 用 originalRequestBodyRaw**: HUAKAI fallback chain 必须从客户端原始 body 重新翻译, 不用上一 attempt 的翻译后 body。这是个 invariant 必须文档化。
4. **MEDIUM — 协议描述三元组化**: 把 vendor 字段拆成 (Name, Version, Prefix), 解决 Gemini OpenAI-compat / Cohere compat / DeepSeek 不同 prefix 问题。
5. **MEDIUM — 鉴权独立抽象**: 把 BackendAuthHandler 这种 `Do(ctx, headers, body) → headers` 接口剥离出 vendor adapter, 6 type 一一实现 (APIKey / AWS / Azure / GCP / Anthropic / OAuth)。
6. **MEDIUM — 计费 cost type 闭集合 + CEL 逃生**: Cost type 7 enum 已经覆盖 95% 场景, CEL 逃生承接剩余 5%。HUAKAI 引入 CEL (官方 cel-go 包) 比自定义 DSL 安全。
7. **LOW — Header/Body Mutation 声明式**: 让 K8s/yaml/admin UI 用户通过配置注入 vendor field, 不需要写 Go 代码。
8. **LOW — Bedrock 流式: 直接复用 aws-sdk-go-v2 EventStream**: 真要支持 Bedrock 时不要自己写 frame parser。

---

## Source files read

- `internal/translator/translator.go` (134 lines)
- `internal/extproc/processor.go` (68 lines)
- `internal/extproc/server.go` (611 lines)
- `internal/extproc/processor_impl.go` (839 lines)
- `internal/translator/openai_awsbedrock.go` (1075 lines)
- `internal/translator/anthropic_openai.go` (280 lines)
- `internal/endpointspec/endpointspec.go` (640 lines)
- `api/v1alpha1/ai_service_backend.go` (130 lines)
- `api/v1alpha1/ai_gateway_route.go` (447 lines)
- `api/v1alpha1/shared_types.go` (159 lines)
- (greps over) `internal/backendauth/*.go`, `internal/translator/anthropic_anthropic.go`, `internal/translator/openai_openai.go`, `internal/translator/anthropic_helper.go`, `api/v1alpha1/backendsecurity_policy.go`

Lane: specifier (axis-3 protocol translation focus)
Agent: general-purpose
UTC timestamp: 2026-05-09T14:51Z
