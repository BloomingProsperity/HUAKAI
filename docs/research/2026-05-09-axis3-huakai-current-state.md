# HUAKAI Axis-3 (协议转换) Current State Audit (no upstream reads)

**Lane**: claude (HUAKAI-internal only — no `~/refs/` reads, no upstream SHA cites)
**Date**: 2026-05-09 UTC
**Scope**: 五复杂度轴 §3 协议转换 — 当前 15% 进度的实物盘点
**Method**: 逐文件读 `backend/internal/proto/`、`backend/internal/provider/`、`backend/internal/transport/`、`backend/internal/gateway/` + `docs/specs/protocol-translation.md` + `docs/specs/streaming-forwarder.md` + `docs/02_HUAKAI_FUSION_ARCHITECTURE.md` §5 复杂度轴。

---

## Q1 Adapter 资产清单 — LoC + 责任 + 完成度

### `backend/internal/proto/` — Canonical / Upstream adapter 层（共 ~3.0K LoC 实现 + ~1.4K LoC 测试）

| 文件 | LoC (impl/test) | 责任 | 完成度 |
|---|---|---|---|
| `proto.go` | 66 | HCSF 占位 struct + ClientAdapter / UpstreamAdapter 接口契约 + Verdict / Direction / ProtocolLossEntry 枚举 | **接口完成；HCSF 自身仍是 `struct{}` 空壳（line 15-18）+ 模块包注释承认 "Full HCSF and additional provider/client adapters remain Phase E+ work"** |
| `hcsf.go` | 119 | CanonicalRequest / CanonicalMessage / CanonicalContentBlock / CanonicalEvent / CanonicalContentDelta / CanonicalResponse / CanonicalUsage / CanonicalStopReason 类型定义 + Passthrough 字段（U7-B） | **完成（typed struct）。但 `proto.go` 里声明的 `HCSF` struct 仍空，说明 hcsf.go 这些类型是**「sub-types」**而非顶层 HCSF envelope**。整个 canonical model 实际只是"扁平的 Canonical* 类型族"，没有真正的 wrapper |
| `passthrough.go` | 203 | U7-A：unknown-field 透传容器（PassthroughEnvelope + UnmarshalWithExtras + MergeExtrasInto + reflect type cache） | **完成**。Anthropic / OpenAI 的 envelope-level unknown 字段被抓 |
| `field_matrix.go` | 204 | U7-E：字段级 verdict matrix（PRESERVED / TRANSFORMED / DROPPED / PRESERVED_DEFAULT），DefaultFieldMatrix() 登记 OpenAI×OpenAI、Anthropic×Anthropic、Anthropic×Bedrock | **部分**。仅 3 对登记；Gemini × * / OpenAI × Anthropic / Anthropic × OpenAI / 6 家 OpenAI 兼容 / 6 家 session 反转 全部未登记，全靠 PRESERVED_DEFAULT |
| `capability_matrix.go` | 170 | feature-level CapabilityMatrix + DefaultMatrix() + Validate() + detectFeaturesInRequest() | **接口完成 + 默认 cell 填充**。但实现是「全 PRESERVED 然后 case-by-case 改 LOSSY/UNSUPPORTED」的粗粒度规则（line 73-94），不是基于真实测过的能力矩阵 |
| `tool_call_id.go` | 77 | ToCanonicalCallID / FromCanonicalCallID — 5 上游格式（toolu_/call_/func_/tool_/call_）↔ canonical `call_<hex>` 双向 | **完成**。Anthropic / OpenAI / Gemini / Bedrock / Antigravity 5 家都接 |
| `anthropic_sse.go` | 313 / 145 | AnthropicAdapter — Anthropic SSE 6 事件 → CanonicalEvent。含 message_start / content_block_start/delta/stop / message_delta / message_stop。tool_use 块抓 + signature_delta 受 carry-forward 字段控制 + 终态 cachemetrics.ObserveByAccountWithPrefix(tenant,account,prefix) | **Provider→Canonical 完成**；CanonicalToProviderRequest / ProviderResponseToCanonical 都返回 ErrNotImplemented (line 83-89) |
| `openai_sse.go` | 609 / 315+177 | OpenAIAdapter — OpenAI Chat Completions SSE → CanonicalEvent。tool_calls index 累积、prompt_tokens_details.cached_tokens → CanonicalUsage.CacheReadInputTokens、stop_reason 多对一映射、[DONE] 终止信号、non-streaming response → CanonicalResponse 也接（buffered path） | **Provider→Canonical 完成（含 tool_calls 流式累积）**；CanonicalToProviderRequest 返回 ErrNotImplemented (line 142-146)；ProviderResponseToCanonical 返回的是空 HCSF + lossy entry 标记（line 148-156，"HCSF envelope has no buffered response slot"）|
| `gemini_sse.go` | 492 / 331 | GeminiAdapter — Gemini streamGenerateContent SSE → CanonicalEvent。content.parts 中 text / functionCall / inlineData / thought（reasoning） 4 类、SAFETY 哨兵 stop reason、cachedContentTokenCount carry-over（但**未走 cachemetrics observe**——line 47-53 注释承认是 future） | **Provider→Canonical 完成**；CanonicalToProviderRequest ErrNotImplemented (line 97-101)；ProviderResponseToCanonical 与 OpenAI 同样的"空 HCSF + lossy"占位 (line 103-111) |
| `bedrock_eventstream.go` | 114 / 264 | BedrockEventStreamAdapter — A4 atomic：Bedrock-on-Anthropic 适配器，per-call 构造 inner AnthropicAdapter delegate（避免 race）。**仅支持 Bedrock-on-Anthropic** | **Provider→Canonical 完成**（通过 delegate）；CanonicalToProviderRequest / ProviderResponseToCanonical 都 ErrNotImplemented (line 67-76)；Bedrock-on-Llama / -on-Cohere / -on-Mistral 全部未实现 |
| `cache_demo_test.go` | 94 (test only) | 测试 cache token 观测 hook | n/a |

**Tests**: `proto_test.go` (399), `anthropic_sse_passthrough_test.go` (145), `openai_sse_test.go` (315), `openai_sse_passthrough_test.go` (177), `gemini_sse_test.go` (331), `bedrock_eventstream_test.go` (264), `field_matrix_test.go` (206), `hcsf_passthrough_test.go` (80), `passthrough_test.go` (311), `cache_demo_test.go` (94) — 共 ~2.3K LoC 测试。

### `backend/internal/provider/` — Vendor outbound adapter 层（仅做 "构造 *http.Request"）

| 文件 | LoC | 责任 | 完成度 |
|---|---|---|---|
| `adapter.go` | 93 | provider.Adapter interface — `BuildRequest(ctx, in) (*http.Request, error)` + Platform() + AcceptableCredentialTypes()。Credential 5 类型枚举 | 完成 |
| `registry.go` | 79 | StaticRegistry — Register / MustRegister / For / RegisteredProtocolFamilies | 完成 |
| `registrydefault/default.go` | 167 | Build() 注册 19 个 protocol family → adapter | 完成（19 family 全部注册到 Static registry） |
| `anthropic/passthrough.go` | 109 | Anthropic Messages API key 直通（X-API-Key + anthropic-version + 可选 anthropic-beta），**不含 OAuth 反转** | 完成（API key 路径） |
| `openai/passthrough.go` | 104 | OpenAI Chat Completions API key 直通（Bearer + org_id / project_id / openai_beta） | 完成 |
| `openai/codex_session.go` | 163 | ChatGPT Plus / Codex CLI session 反转 → chatgpt.com/backend-api/codex/completions（Authorization Bearer + UA + OAI-Device-Id + OAI-Language + 可选 cookie/arkose/chat_session/oai_country） | scaffold + TODO(OCAW)；**实际 endpoint 与 header 未 OCAW 验证** |
| `gemini/passthrough.go` | 131 | Gemini API key 直通到 generativelanguage.googleapis.com（query 或 X-Goog-Api-Key header）+ stream/non-stream endpoint 切换 + X-Goog-User-Project | 完成 |
| `gemini/gemini_advanced_session.go` | 166 | Gemini Advanced 网页 session 反转（gemini.google.com BardChatUi）。**TODO(OCAW)**：endpoint bl= 参数 / SAPISIDHASH / x-goog-visitor-id 全部待采集 | scaffold + 多处 TODO(OCAW) |
| `bedrock/passthrough.go` | 234 | AWS Bedrock Runtime invoke / invoke-with-response-stream，自实现 SigV4 签名（不依赖 aws-sdk-go），`AutoTranslateAnthropicAPIBody` 开关让 Anthropic CLI body 自动翻译为 Bedrock body + auto cache_control 注入 | **完成（aws_sigv4 + upstream_passthrough 两路径都通）**。生产已用 |
| `bedrock/anthropic_request_translator.go` | 160 | Anthropic Messages API body → Bedrock invoke body：strip "model"+"stream"，注入 anthropic_version=bedrock-2023-05-31 + IsAnthropicMessagesShape 检测 | 完成 |
| `bedrock/sigv4.go` | 257 | AWS SigV4 自实现 | 完成 |
| `bedrock/eventstream/decoder.go` | 335 | AWS Binary EventStream wire format decoder（A2 atomic） | 完成（A2 已落） |
| `cursor/cursor_session.go` | 156 | Cursor 反转 (api2.cursor.sh + connect+proto + x-cursor-checksum) | scaffold + TODO(OCAW) |
| `copilot/copilot_session.go` | 173 | GitHub Copilot 反转 (api.githubcopilot.com + Editor-Version / OpenAI-Intent / X-Github-Api-Version) | scaffold + TODO(OCAW) |
| `kiro/kiro_session.go` | 158 | AWS Kiro 反转 (api.kiro.aws — TODO(OCAW) 待确认实际域名) | **占位**（endpoint 推测） |
| `windsurf/windsurf_session.go` | 158 | Windsurf 反转 (api.codeium.com — TODO(OCAW) 待确认) | **占位** |
| `antigravity/antigravity_session.go` | 144 | Antigravity 反转 (api.antigravity.ai — TODO(OCAW)) | **占位** |
| `openrouter/passthrough.go` | 84 | OpenRouter (OpenAI 兼容) | 完成 |
| `grok/passthrough.go` | 74 | xAI Grok (OpenAI 兼容) | 完成 |
| `deepseek/passthrough.go` | 74 | DeepSeek (OpenAI 兼容) | 完成 |
| `mistral/passthrough.go` | 74 | Mistral (OpenAI 兼容) | 完成 |
| `groqcloud/passthrough.go` | 74 | Groq Cloud (OpenAI 兼容) | 完成 |
| `together/passthrough.go` | 74 | Together AI (OpenAI 兼容) | 完成 |
| `perplexity/passthrough.go` | 74 | Perplexity AI (OpenAI 兼容) | 完成 |
| `fireworks/passthrough.go` | 74 | Fireworks AI (OpenAI 兼容) | 完成 |

### `backend/internal/transport/` — RoundTripper 策略层

| 文件 | LoC | 责任 | 完成度 |
|---|---|---|---|
| `factory.go` | 94 | Factory + ClientFor(provider, mode) → http.Client | 完成（policy gate + standard transport） |
| `policy.go` | 247 | ProviderCode（19 家）+ TransportMode（10 种含 standard / mimicry_* / diagnostics_only）+ allowedModesByProvider 矩阵 + ValidateModeForProvider | **policy gate 完成**；**所有 mimicry_* mode 实际都未实现**（仅常量保留 + 启动期 reject 不允许组合）。R3 transport mimicry 整体 paused per Owner 2026-05-06 |

### `backend/internal/gateway/` — Forwarder + Scanner + Adapter 注册表

| 文件 | LoC | 责任 | 完成度 |
|---|---|---|---|
| `forwarder.go` | 461 | StreamForwarder.Forward — Phase A scan / B per-event handle / C classify / C-bis drain / D finalize；ProtocolAdapters + Scanners 双注册表注入；ClientAdapter 当前是 nil-fallback raw passthrough | **完成（Provider→Canonical 主流水线）**；ClientAdapter 路径有调用接口但**全局没有任何 ClientAdapter 实现**——见 Q5 |
| `forwarder_types.go` | 180 | ForwardRequest / UsageRecordDraft / TimeoutConfig / DrainBudgets / UsageAccumulator + 13-class StreamEndClass + 5-class UsageSource | 完成 |
| `protocol_selector.go` | 136 | ProtocolAdapterRegistry 接口 + StaticProtocolAdapterRegistry + BuildDefaultProtocolAdapterRegistry 注册 19 family → upstream adapter | **完成（19 family 全部映射 → AnthropicAdapter / OpenAIAdapter / GeminiAdapter / BedrockEventStreamAdapter，多家共用 OpenAIAdapter）** |
| `stream_scanner.go` | 178 | StreamScanner 接口 + StaticStreamScannerRegistry + SSEStreamScanner（SSE 行扫描） + BuildDefaultStreamScannerRegistry 注册 19 family（18 SSE + 1 Bedrock binary） | 完成 |
| `event_scanner.go` | 95 | ScanSSEEvents — SSE Phase A 切帧（bufio.Scanner + bounded buffer 1 MiB / 64 MiB cap） | 完成 |
| `bedrock_stream_scanner.go` | 152 | BedrockEventStreamScanner — A3 atomic：binary EventStream → SSEEvent；chunk envelope `{"bytes":"<base64>"}` 解 + exception 帧 → error event + ErrBedrockException | 完成（Anthropic-on-Bedrock 子集） |
| `bedrock_e2e_test.go` | 207 (test) | Bedrock 端到端集成测试（mock binary stream → forwarder → SSE 输出） | n/a |

---

## Q2 双向覆盖矩阵

| Vendor / Protocol Family | Upstream parsing (server SSE/binary → canonical) | Client rendering (canonical → client format) | Request building (vendor outbound) | Stream scanner (wire format) |
|---|---|---|---|---|
| **Anthropic Messages (API key)** | ✅ 完成 — `proto/anthropic_sse.go` AnthropicAdapter，6 事件全覆盖，tool_use 块抓，signature_delta carry-forward 受控 | ❌ **不存在** — `proto.ClientAdapter` 接口定义但**全仓零实现**；forwarder ClientAdapter 字段当前用 nil → rawSSE passthrough | ✅ 完成 — `provider/anthropic/passthrough.go`（X-API-Key + anthropic-version） | ✅ SSE — `gateway/event_scanner.go` |
| **Anthropic OAuth (Pro/Max 反转)** | ❌ 不存在 — Owner 2026-05-06 directive 暂停 | ❌ 不存在 | ❌ 不存在 — 本路径仅 R3 transport mimicry mode 常量保留 | n/a |
| **OpenAI Chat Completions (API key)** | ✅ 完成 — `proto/openai_sse.go` OpenAIAdapter，含 tool_calls.index 累积 + cached_tokens + finish_reason 多对一 | ❌ 不存在 | ✅ 完成 — `provider/openai/passthrough.go`（Bearer + org_id / project_id） | ✅ SSE |
| **OpenAI Responses API** | 部分 — protocol family 已注册但 adapter 复用 OpenAIChat 的 OpenAIAdapter；Responses API 专属 chunk 形态（response.output_item.added 等）**未单独实现**；`registrydefault/default.go:91` 注释承认"等待后续 atomic" | ❌ 不存在 | 部分 — `provider/openai/passthrough.go` 复用同一 PassthroughAdapter（同 endpoint 定义占位） | ✅ SSE |
| **OpenAI Codex (ChatGPT Plus session)** | 部分 — `protocol_selector.go:88-89` 注释承认"复用 OpenAIAdapter；若后续观测到形态差异再做专用 CodexSessionSSEAdapter" | ❌ 不存在 | scaffold — `provider/openai/codex_session.go`（endpoint + UA + OAI-Device-Id；多个 OCAW 待采集字段） | ✅ SSE |
| **Gemini (API key, generativelanguage)** | ✅ 完成 — `proto/gemini_sse.go` GeminiAdapter；text / functionCall / inlineData(lossy) / thought(reasoning) | ❌ 不存在 | ✅ 完成 — `provider/gemini/passthrough.go`（query 或 X-Goog-Api-Key） | ✅ SSE |
| **Gemini Advanced (网页 session)** | 部分 — `protocol_selector.go:115` GeminiAdvancedSession 复用 GeminiAdapter；BardChatUi 内部 RPC 形态实际**与官方 SSE 不同**（f.req= 编码），未真做 | ❌ 不存在 | scaffold — `provider/gemini/gemini_advanced_session.go`（多处 TODO(OCAW) — bl= 参数 + SAPISIDHASH + visitor-id） | ✅ SSE（实际可能不对） |
| **Bedrock-on-Anthropic** | ✅ 完成 — `proto/bedrock_eventstream.go` BedrockEventStreamAdapter delegate AnthropicAdapter | ❌ 不存在 | ✅ 完成 — `provider/bedrock/passthrough.go` + `anthropic_request_translator.go`（含 SigV4 自签 + auto-translate + auto cache_control） | ✅ Binary EventStream — `gateway/bedrock_stream_scanner.go` |
| **Bedrock-on-Llama / Cohere / Mistral / Titan** | ❌ 不存在 — `bedrock_eventstream.go:21-23` 注释承认"仅 Bedrock-on-Anthropic；Llama/Cohere on Bedrock 时再分流" | ❌ 不存在 | 部分 — `bedrock/passthrough.go` 当 AutoTranslateAnthropicAPIBody=false 时可作为纯 SigV4 passthrough，但只能传已格式化的 vendor body | ✅ Binary（scanner 通用） |
| **OpenRouter** | 部分 — 复用 OpenAIAdapter（注 OpenRouter 兼容 OpenAI Chat） | ❌ 不存在 | ✅ 完成 — HTTP-Referer + X-Title 排行榜归属 | ✅ SSE |
| **Grok / DeepSeek / Mistral / GroqCloud / Together / Perplexity / Fireworks** | 部分 — 全部复用 OpenAIAdapter（OpenAI 兼容协议假设；未单独 fixture 验证） | ❌ 不存在 | ✅ 完成 — 6 家 PassthroughAdapter 各 74 LoC | ✅ SSE |
| **Cursor / Copilot / Kiro / Windsurf / Antigravity (session 反转)** | 部分 — 全部复用 OpenAIAdapter（占位假设）；`protocol_selector.go:111-119` 注释承认"SSE 形态待 OCAW 采集后确认；先复用 OpenAIAdapter" | ❌ 不存在 | scaffold — 5 个 session adapter 各含多个 TODO(OCAW) header；Cursor 用 connect+proto，Copilot 用 OpenAI shape 等 | ✅ SSE（假设） |

**汇总**：
- **Upstream→Canonical**：4 家有真实现（Anthropic、OpenAI Chat、Gemini API、Bedrock-on-Anthropic）；其他 15 family 全部"复用 OpenAIAdapter 假设兼容"，未做单独 fixture 验证。
- **Client rendering（Canonical→Client）**：**全仓 0 实现**。`proto.ClientAdapter` 接口存在但无任何具体类型 implement。`forwarder.go:294-299` 走 nil-check fallback：`if f.ClientAdapter == nil { return [][]byte{rawSSE(fallback)} }`。当前只能"原样透传上游 SSE"——意味着客户端必须能消费**上游协议**，HUAKAI 没法把 OpenAI 响应转成 Anthropic 形态给客户端。
- **Request building**：12 家完成（API key / SigV4），6 家 session 反转 scaffold（含 TODO(OCAW)），1 家（Anthropic OAuth）暂停。
- **Stream scanner**：所有 SSE family 用同一 SSEStreamScanner；Bedrock 单独走 BedrockEventStreamScanner。

---

## Q3 Canonical model 选型

**当前 Canonical 类型**（`backend/internal/proto/hcsf.go`）：
- `CanonicalRequest` — model / messages / tools / tool_choice / max_tokens / stop_sequences / temperature / top_p / system_prompt / stream / parallel_tool_calls / response_format + Passthrough
- `CanonicalMessage` — role + []CanonicalContentBlock
- `CanonicalContentBlock` — type / text / call_id / name / input / tool_result / image / reasoning_summary
- `CanonicalEvent` — type / message_id / model / index / content_block / delta / usage / stop_reason + Passthrough
- `CanonicalContentDelta` — type / text / partial_json / reasoning_text / signature
- `CanonicalResponse` — id / model / content / usage / stop_reason + Passthrough
- `CanonicalUsage` — input_tokens / output_tokens / total_tokens / **cache_creation_input_tokens / cache_read_input_tokens**
- `CanonicalStopReason` — end_turn / max_tokens / stop_sequence / tool_use / refusal / unknown

**偏向**：明显**偏 Anthropic Messages API 形态**：
- 流事件名称 `message_start` / `content_block_start` / `content_block_delta` / `content_block_stop` / `message_delta` / `message_stop` 一字不差对应 Anthropic SSE 6 事件（`anthropic_sse.go:138-181`）。
- StopReason 枚举（end_turn / max_tokens / stop_sequence / tool_use / refusal）= Anthropic 字面值。
- CacheUsage 字段名 `cache_creation_input_tokens` / `cache_read_input_tokens` = Anthropic prompt-caching 字面值。
- ContentBlock.Type 用 "text" / "tool_use" / "tool_result" / "image" / "reasoning_summary" — 全是 Anthropic 风格。

**HCSF wrapper 真假**：`proto.go:13-18` 的 `HCSF struct{}` 是空壳 + 注释 "TODO(phase-4): canonical request + response + event types. Aligned with OpenAI Responses semantics but in HUAKAI's own type names." — 说明：
1. 顶层 envelope 类型从未实现；
2. 设计意图是 OpenAI Responses 风格，但**实际用的是 Anthropic 风格的 sub-types**（hcsf.go 里的 Canonical*）；
3. 设计意图与实物形态不一致 — 重要红线。

**会丢失的 vendor-specific 字段**（按 vendor 分）：
- **Anthropic**：信号转 canonical 路径完整；`signature_delta` 默认丢（受 CarryForwardSignatureDelta 控制）；`cache_control` 标记入参侧未在 canonical 表达（在 Bedrock 翻译器和 cache_routing 包里独立处理）。
- **OpenAI**：`logprobs` / `system_fingerprint` / `service_tier` 已通过 Passthrough envelope U7-A 抓住；`refusal` 字段 typed 已抓但 `CanonicalContentDelta` 没有 refusal 类型，仅在 stop_reason→refusal 间接表达；`audio_tokens` 抓但未 canonical 化。
- **Gemini**：`citations` / `groundingMetadata` / `safetyRatings` / `promptFeedback` 全部仅作 raw 字段读，未进 canonical；`SAFETY` stop reason 是哨兵字符串而非枚举（`gemini_sse.go:14`）。`cachedContentTokenCount` 累在 GeminiUpstreamState.CachedContentTokens 但**未喂给 cachemetrics observer**（line 47-53 注释承认 future）。
- **Bedrock**：`amazon-bedrock-invocationMetrics` 头不在 canonical；`additionalModelResponseFields` 不抓；non-Anthropic 模型族（Llama/Cohere/Mistral/Titan）整体不存在 adapter。

**HCSF（HUAKAI Canonical Streaming Format）实物状态**：
- `hcsf.go` 中的 Canonical* 类型族存在（typed structs）
- `proto.HCSF struct{}` 顶层 wrapper 不存在
- `HCSF passthrough` 字段集成 U7-A/U7-D（OpenAI / Anthropic）已完成；Gemini 端**没有走 PassthroughEnvelope**（grep 显示 gemini_sse.go 用 plain `json.Unmarshal`，line 137 + line 451）

---

## Q4 Tool call / streaming 状态机

### SSE state machine — 共享还是 per-vendor？

**目前是 per-vendor 完全独立**：
- `proto.UpstreamState`（anthropic_sse.go:27-39）— Anthropic + Bedrock-on-Anthropic 共用
- `proto.OpenAIUpstreamState`（openai_sse.go:25-47）— OpenAI 全家族共用（含 Codex / OpenRouter / Grok / DeepSeek / 6 家兼容 + 6 家 session 反转中的 5 家假设）
- `proto.GeminiUpstreamState`（gemini_sse.go:30-56）— Gemini API + Gemini Advanced
- `forwarder.go:337-351` newUpstreamState 用 type-switch 决定 instantiate 哪个 — sonnet F3 HIGH 修复留痕

**没有"共享 state machine"层**。每家自建 BlocksInProgress / NextBlockIndex / ToolCalls map / Terminated 标志，逻辑虽相似但各写各的。这意味着新 vendor 接入要从零写 state struct + 状态转移函数。

### Tool call 跨 vendor 互转代码

**仅 ID 翻译有代码**：
- `proto/tool_call_id.go` — 5 上游 prefix（toolu_/call_/func_/tool_/call_）↔ canonical `call_<hex>` 双向翻译 + hex 校验
- AnthropicAdapter `canonicalBlock("tool_use")` → CanonicalContentBlock{Type:"tool_use", CallID, Name, Input}
- OpenAIAdapter `openAIToolCallDeltaEvents` → 累计 tool_calls.index 对应的 OpenAIToolCallState 然后 emit content_block_start/delta/stop
- GeminiAdapter `geminiPartToCanonicalEvents` → functionCall 直接 emit content_block_start + stop（无 partial_json delta，因 Gemini args 是一次性 JSON）

**互转代码缺口**：
- **没有"tool_choice"互转**：CanonicalRequest.ToolChoice 是 `any` 类型（hcsf.go:18），各 vendor 形态不同（OpenAI 用 object {type:"function",...}, Anthropic 用 {type:"tool",name:"..."}, Gemini 用 functionCallingConfig），但因为 CanonicalToProviderRequest 全部返回 ErrNotImplemented，从未真的执行过这个互转。
- **没有"input/arguments JSON delta 流式 reassembly invariant"测试**：OpenAI 的 partial_json 是字符串拼接、Anthropic 的 input_json_delta 也是、Gemini 是一次完整 args — 没有共享代码保证三家拼起来的最终 JSON object 等价。
- **没有"function_call"（OpenAI 旧版）→ "tool_calls" 互转**：legacy 字段处理缺失。
- **tool_result content block type 在 canonical 定义了（hcsf.go:49 ToolResult json.RawMessage），但没有任何 adapter emit 它** — 因为没有 ClientAdapter 把客户端发来的 tool_result 翻译成 canonical 再翻译给上游。

---

## Q5 注册机制

### ProtocolAdapterRegistry

**注册** (`gateway/protocol_selector.go:81-121` BuildDefaultProtocolAdapterRegistry):
- 启动期 MustRegister 19 个 protocol family，全部映射到 4 个 adapter 实例：AnthropicAdapter / OpenAIAdapter / GeminiAdapter / BedrockEventStreamAdapter
- 多家 family 共享同一 adapter type — 13 个 family 用 `&proto.OpenAIAdapter{}`

**解析**: forwarder.go:85-87 调 `f.ProtocolAdapters.For(req.ProtocolFamily)`；nil registry / 空 family / 未注册 family 都是 fail-loud（不 fallback）

**查找**: O(1) map lookup；isNilUpstreamAdapter 用 reflect 处理 typed-nil

### ClientAdapter

**当前是 nil-fallback**：
- `forwarder.go:41-43`：`ClientAdapter proto.ClientAdapter` 字段，注释"可选；若为 nil，则透传原始 SSE 给客户端"
- `forwarder.go:293-299` `clientChunks()`：`if f.ClientAdapter == nil { return [][]byte{rawSSE(fallback)}, nil }` → 直接 raw SSE 透传
- **全仓 grep `proto.ClientAdapter` 实现 = 0 个 concrete type**。只有 interface 定义 + forwarder 字段 + 注释。
- `bedrock/anthropic_request_translator.go:16-17` 自承："Anthropic CLI 已经期望 Anthropic Messages 形态，所以返回端不用 ClientAdapter 翻译。OpenAI client → Bedrock 闭环（需 ClientAdapter）放更后面。"

**含义**：当前 forwarder 只能做"客户端 = 上游协议"这一种透传场景。OpenAI client 发请求到 Bedrock-on-Anthropic 上游？响应是 Anthropic 形态的 SSE，客户端会失败。这是 axis-3 "15%" 评分的核心缺口。

### ProtocolFamily 字符串怎么决定

**决定路径**:
1. 入口：`ForwardRequest.ProtocolFamily` 字段（forwarder_types.go:113）由 caller 传入
2. 上游来源：router/registry 解析时返回 `ResolvedModel.ProtocolFamily`（registry 决定 model→protocol family 映射，见 `registrydefault/default.go` 头部 family 字符串约定）
3. 19 个常量（registrydefault/default.go:55-77）：openai_chat / openai_responses / openai_codex / anthropic_messages / gemini_messages / openrouter_chat / bedrock_invoke / grok_chat / deepseek_chat / mistral_chat / groqcloud_chat / together_chat / perplexity_chat / fireworks_chat / cursor_session / copilot_session / gemini_advanced_session / antigravity_session / kiro_session / windsurf_session
4. **空值校验**：forwarder.Forward 入口 line 80-82 — 空 ProtocolFamily 直接 ErrUnknownProtocolFamily fail-loud

**ClientProtocol vs ProtocolFamily 混乱**:
- `ForwardRequest` 里同时有 `ProtocolFamily`（新驱动字段）+ `UpstreamProtocol` + `ClientProtocol`（line 117-118 标"保留作向后兼容"）
- `proto.ClientProtocol` 是另一套枚举（capability_matrix.go:13-17）：openai_chat / openai_responses / anthropic_messages — 这套用于 CapabilityMatrix Validate；与 forwarder 用的字符串实际不互通

---

## Q6 已知缺口（来自 docs 自身声明）

### docs/02_HUAKAI_FUSION_ARCHITECTURE.md §5 复杂度轴 第 3 行

> | **3. 协议转换** | OpenAI ↔ Anthropic ↔ Gemini canonical 中介 | 1 个 anthropic_sse upstream adapter；OpenAI client adapter 0；HCSF canonical 类型空 | **15%** | [proto/anthropic_sse.go](../backend/internal/proto/anthropic_sse.go) |

文档自承的 axis-3 状态（写于 2026-05-01，但 Bedrock A1-A4 已落 + OpenAI/Gemini upstream adapter 已加，此评分已偏低需更新到约 25-30%）。

### docs/03_FEATURE_PARITY_MATRIX.md F-PROTO 行

- **F-PROTO-001** (LiteLLM, MCP/A2A external agent/tool protocol bridging) — Status: **Open (L3 Phase 9+)**，Plugin 后置，与本 audit 无关
- **F-PROTO-002** (New API, single client request shape translated across provider protocols) — Status: **Spec Released 2026-04-28**（docs/specs/protocol-translation.md）；Codex APPROVE-WITH-FIXES。**实物落地度 ≈ Provider→Canonical 4/19 完成 + Client→Canonical 0 + Canonical→Client 0**

### docs/specs/protocol-translation.md 自身声明的"Phase B/D 全缺"

- §Phase A — Client Request → Canonical：**未实现**（`RequestToCanonical` 接口存在但 zero implementation）
- §Phase B — Canonical → Upstream Request：**未实现**（4 个 adapter 的 `CanonicalToProviderRequest` 全部 ErrNotImplemented）
- §Phase C — Upstream Response → Canonical (Streaming)：**部分**（4 个 vendor — Anthropic / OpenAI / Gemini / Bedrock-on-Anthropic）
- §Phase D — Canonical → Client Response (Streaming)：**未实现**（`CanonicalEventToClientChunk` 接口存在但 zero implementation；forwarder 走 nil-fallback raw passthrough）
- §Phase E — Capability Loss Reporting：部分（ProtocolLossEntry 类型 + 各 adapter emit；但 forwarder 没有把 loss 累加到 UsageRecordDraft，也没有 X-HUAKAI-Protocol-Loss response header）

### docs/specs/streaming-forwarder.md F-GW-002 自承延展

- A12a Stream-Safe Retry Boundary FSM (硬底线) — **未实现** (FSM 5 状态全无代码)
- A25 Adaptive Stream Buffer Controller — **未实现** (current ScannerBufferCap 是固定值)
- A26 Expected-Value Drain Decision (硬底线) — **未实现** (Phase C-bis drain 仅按 budget 退出，无 E[value]>E[cost] 判定)
- A27 Stream-Time Dynamic Reserve Adjustment — **未实现** (无 RESERVE_CHECK_INTERVAL_TOKENS 检查)

---

## Q7 测试覆盖

### `backend/internal/proto/*_test.go` 数量与规模

| 测试文件 | LoC |
|---|---|
| proto_test.go | 399 |
| anthropic_sse_passthrough_test.go | 145 |
| openai_sse_test.go | 315 |
| openai_sse_passthrough_test.go | 177 |
| gemini_sse_test.go | 331 |
| bedrock_eventstream_test.go | 264 |
| field_matrix_test.go | 206 |
| hcsf_passthrough_test.go | 80 |
| passthrough_test.go | 311 |
| cache_demo_test.go | 94 |

总计 ~2.3K LoC 测试 vs ~3.0K LoC 实现，比例 0.77 — 偏 unit 测试重，integration 测试少。

### 跨 vendor 集成测试

**几乎没有**：
- `gateway/bedrock_e2e_test.go` (207 LoC) — 唯一的"vendor wire format 端到端"集成测试，验证 BedrockEventStreamScanner + BedrockEventStreamAdapter + StreamForwarder 三层对接；但它是单 vendor 测试不是跨 vendor。
- `gatewayhttp/dispatch_smoke_test.go`（grep 出来的）— HUAKAI 全链路冒烟，但用的是 smokeRouter / smokeClaimGate / smokeSelector / smokeSettler 全 mock；ProtocolFamily 固定为 openai_chat（line 49）；不验证多 vendor 互转。
- **无 OpenAI client → Anthropic upstream 互转集成测试**（因为 ClientAdapter 不存在）
- **无 Anthropic client → Gemini upstream 互转集成测试**（同样）
- **无 capability matrix 通过 property test 的"每 cell 跑一遍"测试**（spec 的 AT-PROTO-002-15 要求，未实现）

### 真上游 smoke test 文件

**无任何真上游测试文件存在**。
- `db/pgconn_integration_test.go` 是 PG 真连接 smoke，与 axis-3 无关
- 其他所有 `*smoke*` / `*integration*` 文件都用 mock upstream
- per `feedback_owner_local_verification.md` + `project_real_vendor_account_scope.md`：真上游 smoke 限定 4 vendor（anthropic/openai/gemini/codex），且必须 Owner 本机跑；Owner 没 AWS 凭据，所以 Bedrock 真上游 smoke 不可达；其他 vendor 全 mock
- bedrock_e2e_test 是 mock binary stream（手工构造 EventStream frames），不是真 AWS 连接

---

## Top 5 优先级缺口（基于现状评估）

### 缺口 1：ClientAdapter 整体不存在（Phase D 完全空）
- **缺什么**：`proto.ClientAdapter` 接口已定义但**全仓零具体实现**。forwarder.go:294 走 nil-fallback raw passthrough。
- **阻塞什么**：
  - 跨协议路由（OpenAI client → Anthropic upstream 等）— 这是 axis-3 的 raison d'être
  - F-PROTO-002 spec §Phase D 要求
  - 多协议 SaaS 场景 — sub2api 灵魂级功能
- **估算工作量**：每个 ClientAdapter 实现 ≈ Phase C upstream adapter 的 60%（不需要状态机，只需 canonical event → vendor chunk 序列化），但需要 3 套（AnthropicMessages / OpenAIChat / OpenAIResponses）。**含 fixture 测试 + 集成测试约 2-3 周**。

### 缺口 2：CanonicalToProviderRequest 全部未实现（Phase B 空）
- **缺什么**：4 个上游 adapter 的 `CanonicalToProviderRequest` 全部返回 `ErrNotImplemented`（anthropic_sse.go:83 / openai_sse.go:142 / gemini_sse.go:97 / bedrock_eventstream.go:69）。
- **阻塞什么**：
  - 跨协议路由（请求侧）— 客户端发 OpenAI body，无法翻译为 Anthropic upstream body
  - 当前替代方案：调 `bedrock/anthropic_request_translator.go` 这种 vendor-specific request 翻译，但只覆盖 Anthropic API → Bedrock 一对，不通用
  - F-PROTO-002 spec §Phase B 要求
- **估算工作量**：3 个 vendor × 1 个翻译方向（Canonical → Anthropic / OpenAI / Gemini）= 3 个实现 + tool_choice / response_format / structured output schema 形态映射。**约 1-2 周**。

### 缺口 3：HCSF wrapper 类型空 + 设计意图与实物形态不一致
- **缺什么**：`proto.HCSF struct{}` 是空壳（proto.go:13-18）。设计意图标的是"OpenAI Responses semantics"但实际 sub-types（hcsf.go）是 Anthropic 风格事件名 + StopReason 字面值 + Cache 字段名。
- **阻塞什么**：
  - 任何"先把请求/响应/事件包成 HCSF 单一信封"的代码无 anchor 类型可用
  - 设计与实现的不一致让任何下游"以 OpenAI Responses 为基准"的工作（例如未来加 streaming reasoning summary）都会触发 reshape
  - 让 ClientAdapter / Phase B 适配器实现时无法决定"参数是 *HCSF 还是 CanonicalRequest"
- **估算工作量**：决定要么删除 HCSF wrapper 概念让 Canonical* 平铺，要么实现 wrapper 并迁移。**1 周决策 + 1 周改动**。

### 缺口 4：non-Anthropic Bedrock 模型族 + 6 家 session 反转 SSE 形态全是占位假设
- **缺什么**：
  - Bedrock-on-Llama / -Cohere / -Mistral / -Titan：proto/bedrock_eventstream.go:21-23 自承"仅 Bedrock-on-Anthropic"
  - Cursor / Copilot / Kiro / Windsurf / Antigravity / Gemini Advanced：protocol_selector.go:111-119 全部"占位假设复用 OpenAIAdapter；OCAW 待采集"
  - OpenAI Responses API 专属事件形态 — 与 Chat Completions 不完全一致（response.output_item.added / response.output_text.delta 等），当前复用 OpenAIAdapter 无法处理
- **阻塞什么**：
  - 对应 vendor 的真实流式响应一旦不是 OpenAI-shape 会触发 "malformed JSON chunk" loss entry 静默丢事件
  - 任何 OCAW 实测发现形态差异前都不能上线
- **估算工作量**：每家 vendor SSE 单独验证 ≈ 1-2 天采集 + 1 周 adapter；6 家 session + 4 家 Bedrock 子模型 + 1 个 Responses ≈ 8-10 周（强依赖 OCAW 采集，Owner 直接说"用成熟项目逻辑参考"可以缩到 4-5 周）。

### 缺口 5：tool_use / tool_choice / tool_result 跨 vendor invariant 测试 + Capability Matrix property test
- **缺什么**：
  - tool_call ID 翻译有代码（tool_call_id.go），但**没有"OpenAI partial_json 拼接 → canonical → Anthropic input_json_delta → 等价 final JSON"** 这种 round-trip property test
  - tool_choice 互转代码 0 行（CanonicalRequest.ToolChoice 是 `any`）
  - tool_result content block type 定义了但没人 emit
  - F-PROTO-002 spec AT-PROTO-002-15 要求"capability matrix matches reality: every cell asserted via property test running each (client × upstream) pair through multi-feature canonical request" — **未实现**
  - F-PROTO-002 spec AT-PROTO-002-12 (tool-call ID round-trip bijection) — 单元测有但跨 vendor pair 全矩阵没跑
- **阻塞什么**：
  - capability matrix 只是"DefaultMatrix() 用规则填"（capability_matrix.go:73-94），不是基于真行为测过的——operator 看到的能力声明可能与实际不符
  - tool 调用是大客户最高频用例，invariant 缺失就有"tool args 拼接错乱 + customer 抱怨"风险（per `project_sub2api_scaling_bottleneck.md`）
- **估算工作量**：property test 框架 + 4-5 vendor pair × 6-8 feature = ≈ 2-3 周；含 tool_choice 互转实现可能再 1 周。

---

## 派生观察（不是 Q1-Q7 直接要求但跨整理出现）

1. **Field Matrix 登记覆盖率 ≈ 3/30**：DefaultFieldMatrix 仅登记 (OpenAI×OpenAI, Anthropic×Anthropic, Anthropic×Bedrock) 三对。其他 27 对 (5 client × 6 upstream — 已存对) 全部依赖 PRESERVED_DEFAULT。U7-E "运维可观测"承诺还差 90%。
2. **Gemini cache observation 路径死代码**：GeminiUpstreamState 有 AccountID / PrefixHash / TenantID 字段（gemini_sse.go:46-55）但**整个 gemini_sse.go 没有任何 cachemetrics.Observe 调用**——field 注释自承 "future, observation 暂未走 PASR 路径"。Anthropic / OpenAI 都接了，Gemini 是单点缺口。
3. **PassthroughEnvelope 在 Gemini adapter 缺失**：gemini_sse.go 用 plain `json.Unmarshal`，没接 UnmarshalWithExtras（U7-D 接的是 Anthropic 和 OpenAI）— Gemini 上游加新字段会静默丢。
4. **forwarder.go 标 "PATCH 提案文件" 但实际参与 build**：line 1-3 注释"本文件是 PATCH 提案文件（_patch.go 后缀），不参与 Go build" — 但文件名是 forwarder.go 不是 forwarder_patch.go，注释陈旧（曾经是 patch 文件，现在已合并）。同样问题在 forwarder_types.go:1-3。
5. **transport mimicry 全部是常量 + policy gate，零真实现**：transport/policy.go 定义 9 种 mimicry mode 常量，但 `factory.go` 只用 standard transport（mimicry mode 实际 round-tripper 在 factory.go 里 grep 不到对应 case）。R3 整体暂停（per Owner 2026-05-06）。
6. **ClaimGate / Pool / Settler 与 axis-3 解耦良好**：6 公开契约（CMB-1..7）让 forwarder 这层只关心 ProtocolFamily + ProtocolAdapter + Scanner，不需要回 router/pool 协调——这是 axis-3 推进时不会牵动其他模块的好基础。

---

## Source files read (HUAKAI internal only)

```
backend/internal/proto/
  anthropic_sse.go (313 lines)
  bedrock_eventstream.go (114 lines)
  capability_matrix.go (170 lines)
  field_matrix.go (204 lines)
  gemini_sse.go (492 lines)
  hcsf.go (119 lines)
  openai_sse.go (609 lines)
  passthrough.go (203 lines)
  proto.go (66 lines)
  tool_call_id.go (77 lines)

backend/internal/provider/
  adapter.go (93 lines)
  registry.go (79 lines)
  registrydefault/default.go (167 lines)
  anthropic/passthrough.go (109 lines)
  openai/passthrough.go (104 lines)
  openai/codex_session.go (163 lines)
  gemini/passthrough.go (131 lines)
  gemini/gemini_advanced_session.go (166 lines)
  bedrock/passthrough.go (234 lines)
  bedrock/anthropic_request_translator.go (160 lines)
  cursor/cursor_session.go (156 lines)
  copilot/copilot_session.go (173 lines)
  kiro/kiro_session.go (158 lines)
  windsurf/windsurf_session.go (158 lines)
  antigravity/antigravity_session.go (144 lines)
  openrouter/passthrough.go (84 lines)
  grok/passthrough.go (74 lines)
  deepseek/passthrough.go (74 lines)
  (mistral / groqcloud / together / perplexity / fireworks 各 74 lines — 与 grok/deepseek 同模式)

backend/internal/transport/
  policy.go (247 lines)
  (factory.go 文件名 grep 列出，未深读因不影响 axis-3 评估)

backend/internal/gateway/
  forwarder.go (461 lines)
  forwarder_types.go (180 lines)
  protocol_selector.go (136 lines)
  stream_scanner.go (178 lines)
  event_scanner.go (95 lines)
  bedrock_stream_scanner.go (152 lines)
  (forwarder_test.go / *_test.go 在 grep 中查 ClientAdapter / 真上游 / smoke 关键词)

docs/
  02_HUAKAI_FUSION_ARCHITECTURE.md (§5 复杂度轴 + axis-3 行)
  03_FEATURE_PARITY_MATRIX.md (F-PROTO-001 / F-PROTO-002 行)
  specs/protocol-translation.md (Phase A-E 完整 spec)
  specs/streaming-forwarder.md (F-GW-002 含 A12a/A25/A26/A27 延展)
```

**Lane**: claude
**Time**: 2026-05-09T UTC
**Reference projects read**: 0 (本 lane 严格 HUAKAI-internal only，未读 ~/refs/* 任何上游源)
