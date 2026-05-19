# 2026-05-12 P-2 ClientAdapter 切片计划（Codex 独立草案）

| 项 | 内容 |
| --- | --- |
| Owner directive | "Owner 让 P-2 ClientAdapter 起平行 plan" |
| Codex lane | 独立 plan 起草人；不读取 Claude 同名 plan |
| 文档目标 | 为 P-2 ClientAdapter 实施前 synthesis 提供 Codex 独立切片、测试、风险与决策点 |
| 范围 | 3 个 client adapter × 4 个 hookpoint |
| 不在范围 | 非 MIT reference project 源码读取；backend 业务实现；数据库 schema；auth / billing / quota core 修改 |
| Clean-room 状态 | 本草案只读 HUAKAI 内部代码 / specs 与 vendor 官方协议文档 |
| Fusion-upgrade delta | 架构升级：以 HCSF v0.4 capability graph 为唯一中间层，client adapter 不直连 provider dialect |
| Agent ID | Codex-GPT5-20260512-P2-ClientAdapter |

## 1. 目标

1. P-2 的目标是把 P-1 HCSF v0.4 从"schema + validator + upstream event IR"推进到"client boundary 可用"。
2. P-2 不直接扩 provider 覆盖面。
3. P-2 不重写 routing / pooling / dispatcher。
4. P-2 要补齐 client protocol 到 HCSF envelope 的入口。
5. P-2 要补齐 HCSF buffered response 到 client response 的出口。
6. P-2 要补齐 HCSF StreamEvent 到 client SSE chunk 的出口。
7. P-2 要定义流尾 finalize 对 cleanup / billing settle / cache mutate / loss audit 的边界。
8. P-2 的三个 client protocol 是 `anthropic_messages`、`openai_chat`、`openai_responses`。
9. P-2 的四个 hookpoint 沿用 `proto.ClientAdapter` 接口。
10. 当前接口已经存在：`RequestToCanonical`、`CanonicalToClientResponse`、`CanonicalEventToClientChunk`、`FinalizeClientStream`。[proto.go](/home/codex/HUAKAI/backend/internal/proto/proto.go:23)
11. P-2 应把接口签名从"存在但无实现"推进到"三个 client adapter 均可注册、可测试、可接入 forwarder"。
12. P-2 不应该把 client adapter 与 upstream adapter 混在同一个职责里。
13. Upstream adapter 的职责是 provider wire -> canonical。
14. Client adapter 的职责是 client wire <-> canonical。
15. 两者在 HCSFEnvelope / CanonicalEvent 边界相遇。
16. P-2 的核心验收不是"能透传 SSE"。
17. P-2 的核心验收是"client 看到自己协议的合法 response / chunk，同时 HCSF 内部保留 capability graph 与 loss audit"。
18. P-2 需要把当前 generic chat handler 的 body parsing 从轻量 `chatRequest` 升级为 adapter-driven parsing。
19. 现有 handler 只解析 `model/messages/stream` 三个字段。[chat_completions_handler.go](/home/codex/HUAKAI/backend/internal/gatewayhttp/chat_completions_handler.go:59)
20. 现有 handler 在 non-streaming 请求上直接返回 `non_streaming_unsupported`。[chat_completions_handler.go](/home/codex/HUAKAI/backend/internal/gatewayhttp/chat_completions_handler.go:122)
21. P-2 应解除这个结构性限制，但是否在同一切片打开 non-streaming HTTP route 需要 Owner/PM 决策。
22. 本计划建议 P-2 先实现 adapter 层 buffered 序列化，route 打开可作为后续小切片。
23. P-2 要对 HCSF v0.4 的必填字段负责。
24. HCSFEnvelope 的 `Version` 锁定为 `"0.4"`。[envelope.go](/home/codex/HUAKAI/backend/internal/proto/envelope.go:5)
25. HCSFEnvelope 包含 `RequestMeta`、`RequestControls`、`Messages`、`CapabilityGraph`、`ProviderProjection`、`StreamPlan`、`Accounting`、`Policy` 等 required 结构。[envelope.go](/home/codex/HUAKAI/backend/internal/proto/envelope.go:18)
26. P-2 入口 adapter 必须生成能通过完整 `ValidateEnvelope` 的 request envelope。
27. P-2 出口 adapter 对 buffered response 可以先只要求 `ValidateEnvelopeVersionGuard` + response shape 单测。
28. P-2 stream chunk adapter 不应要求每个事件都包成完整 envelope。
29. `CanonicalEvent` 当前是流式事件 union，带 `Type/MessageID/Model/Index/ContentBlock/Delta/Usage/StopReason/Passthrough`。[hcsf.go](/home/codex/HUAKAI/backend/internal/proto/hcsf.go:54)
30. P-2 要让 `CanonicalEvent.Passthrough` 回到 client wire，避免 U7 preserve-by-default 在出口丢失。
31. P-2 的 adapter state 需要独立于 upstream state。
32. 现有 forwarder 已为 client adapter 预留 `clientState any`。[forwarder.go](/home/codex/HUAKAI/backend/internal/gateway/forwarder.go:110)
33. 但当前 `clientState` 未按 adapter 初始化。
34. P-2 需要补一个 `NewClientState` 或等价轻量机制。
35. P-2 的最小上线目标是三协议 × 四 hookpoint 全部有实现或显式 no-op。
36. 显式 no-op 必须可测试，不允许 silent drop。
37. `ProtocolLossEntry` 是一等公民；v0.4 推荐填 `Severity/Reason/Code/Capability/NodeID/NativePath`。[protocol_loss.go](/home/codex/HUAKAI/backend/internal/proto/protocol_loss.go:16)
38. `ProtocolLossEntry.IsSilentDrop` 对 v0.4 severity 无 reason/code 的条目会判 silent drop。[protocol_loss.go](/home/codex/HUAKAI/backend/internal/proto/protocol_loss.go:73)
39. P-2 所有 lossy / unsupported / native_required 路径必须产出非 silent loss。
40. P-2 的差异化不是复刻任一 gateway 的转换细节。
41. P-2 的差异化是架构升级：client adapter 只投影 HCSF capability graph，不直接编码 provider-to-client 矩阵。
42. P-2 的算法升级是：field-level preserve-by-default + capability-level explicit loss 组合。
43. P-2 的生态升级是：每个 loss 可以进入 Usage Record / operator matrix / release gate。
44. 官方协议依据只用 vendor docs。
45. OpenAI Chat Completions 官方 OpenAPI 显示 `POST /chat/completions` 可返回 JSON completion 或 streamed chunk sequence。
46. OpenAI Responses 官方 OpenAPI 显示 `POST /responses` 可返回 JSON response 或 `text/event-stream` 的 response stream event。
47. Anthropic 官方 streaming docs 显示 Messages SSE 以 named event + matching JSON `type` 组成，事件流包括 `message_start`、content block lifecycle、`message_delta`、`message_stop`。
48. Anthropic 官方 docs 还说明 streaming usage 在 `message_delta` 中是累计 token count。
49. P-2 计划不把 vendor docs 中未稳定或未明确的字段当作 mandatory first-class capability。
50. 对不明确字段采用 passthrough + FieldMatrix audit + ProtocolLoss note。
51. 成功标准：P-2 完成后，三个 client protocol 都能从 raw request 生成 HCSF v0.4 envelope。
52. 成功标准：P-2 完成后，三个 client protocol 都能从 HCSF buffered response 生成合法 client response。
53. 成功标准：P-2 完成后，三个 client protocol 都能从 HCSF StreamEvent 生成合法 client SSE chunk。
54. 成功标准：P-2 完成后，stream finalize 可重复调用且不会重复 terminal chunk / loss audit / cache mutation。
55. 成功标准：P-2 完成后，P-1 35 fixture 不回归。
56. 成功标准：P-2 完成后，新增 client fixture 覆盖每个 adapter 每个 hookpoint 的 positive 与 negative。
57. 成功标准：P-2 完成后，`go test ./internal/proto ./internal/gateway ./internal/gatewayhttp` 至少通过。
58. 成功标准：P-2 完成后，doc-only / code commit 前按项目规则跑 `codex exec review --uncommitted --full-auto`。
59. 预估总工期：8.0 到 11.0 engineer-day。
60. 最大 blast radius：client HTTP compatibility、stream terminal、billing settle 触发点、cache observation、ProtocolLoss 审计。
61. 本草案不批准高风险操作。
62. 高风险操作包括 schema migration、billing ledger 修改、quota enforcement 修改、auth core 修改、新 runtime dependency。
63. 如实现发现这些是必要条件，必须停下交 Owner/PM 决策。

## 2. 当前状态分析

64. 当前 `backend/internal/proto` 已有 HCSF v0.4 顶层 envelope。
65. `HCSFVersion` 常量锁定 `"0.4"`。[envelope.go](/home/codex/HUAKAI/backend/internal/proto/envelope.go:5)
66. `NewEmptyEnvelope` 会填 Version、空 message slice、空 capability graph、空 projection、默认 stream plan、默认 policy。[envelope.go](/home/codex/HUAKAI/backend/internal/proto/envelope.go:57)
67. 但 `NewEmptyEnvelope` 的 `RequestMeta` 是零值，不满足完整 INV-5。
68. 完整 `ValidateEnvelope` 要求 `RequestMeta.RequestID/ClientProtocol/ProtocolFamily/Model/IngressPath` 非空。[envelope_validate.go](/home/codex/HUAKAI/backend/internal/proto/envelope_validate.go:126)
69. 因此 P-2 `RequestToCanonical` 不能只调用 `NewEmptyEnvelope` 后返回。
70. 它必须注入 request context 或设计 `RequestToCanonical` 的 metadata 输入方式。
71. 当前 `ClientAdapter` 接口的 `RequestToCanonical(ctx, raw []byte)` 没有显式传 request_id / ingress path。
72. P-2 要么从 context 读元信息，要么扩接口。
73. 扩接口属于非破坏 API contract 更新，但影响实现面。
74. 我建议先用 context value 小结构承载 `RequestMetaSeed`，避免改四个 hookpoint 签名。
75. 这个建议需要 Claude lane cross-discuss。
76. `RequestControls` 已覆盖 tools、tool_choice、max_tokens、stop、stop_sequences、temperature、top_p、system_prompt、parallel_tool_calls、response_format、seed、tool name hash algorithm。[request_meta.go](/home/codex/HUAKAI/backend/internal/proto/request_meta.go:69)
77. 现有 HTTP handler 尚未将这些字段解析进 HCSF。
78. 现有 HTTP handler 只保留 raw body 给 dispatcher。[chat_completions_handler.go](/home/codex/HUAKAI/backend/internal/gatewayhttp/chat_completions_handler.go:100)
79. 现有 handler 只用轻量 struct 做 model/messages/stream 解析。[chat_completions_handler.go](/home/codex/HUAKAI/backend/internal/gatewayhttp/chat_completions_handler.go:117)
80. 所以 P-1 schema 中 RequestControls 大部分尚未被 adapter 覆盖。
81. `CapabilityKind` 当前有 15 concrete node kind，口径是 14 capability families，其中 tool 拆成 tool_use/tool_result。[capability_graph.go](/home/codex/HUAKAI/backend/internal/proto/capability_graph.go:3)
82. 具体 kind 包含 text、tool_use、tool_result、thinking、cache_control、structured_output、computer_use、file、image、audio、video、live_session、batch、mcp_server、data_retention。[capability_graph.go](/home/codex/HUAKAI/backend/internal/proto/capability_graph.go:13)
83. `CapabilityNode` 是 tagged union，Kind 必须对应恰好一个 payload。[capability_graph.go](/home/codex/HUAKAI/backend/internal/proto/capability_graph.go:86)
84. P-2 RequestToCanonical 要从 client body 派生 capability nodes。
85. 当前 upstream event adapters 仍主要产出老 `CanonicalContentBlock` / `CanonicalEvent`，不是完整 graph。
86. 这是 P-2 的核心缺口：client request path 要创建 graph，client response path 要消费 response/event。
87. `ProviderProjection` 已有 capability-level verdict、node_id、protocol_loss、native_path。[projection.go](/home/codex/HUAKAI/backend/internal/proto/projection.go:20)
88. `validateProviderProjection` 要求非 preserved verdict 至少有一条 ProtocolLoss。[envelope_validate.go](/home/codex/HUAKAI/backend/internal/proto/envelope_validate.go:418)
89. P-2 应在 RequestToCanonical 先写 client-side projection baseline。
90. 上游 projection 可由后续 upstream adapter 或 router 补全。
91. `StreamPlan` 已表达 buffered / streaming / replay 三种 mode。[stream_plan.go](/home/codex/HUAKAI/backend/internal/proto/stream_plan.go:3)
92. `StreamPlan` 还包含 event_classes、flush_policy、terminal_required、synthetic_terminal_allowed、fallback_boundary、mid_stream_fallback_policy。[stream_plan.go](/home/codex/HUAKAI/backend/internal/proto/stream_plan.go:44)
93. Validator 当前要求 mode 合法，并强制 mid_stream_fallback_policy 为 none。[envelope_validate.go](/home/codex/HUAKAI/backend/internal/proto/envelope_validate.go:483)
94. P-2 不应改变 mid-stream fallback policy。
95. `Accounting` 已承载 CanonicalUsage、usage_source、reasoning_tokens、live_usage、batch_usage、evidence_label。[accounting.go](/home/codex/HUAKAI/backend/internal/proto/accounting.go:3)
96. P-2 response adapters 要把 buffered/stream usage 写回 client wire。
97. P-2 finalize 要把 usage/loss/cache mutation 的边界交给 forwarder/settler，不在 client adapter 内写 billing ledger。
98. `Policy` 已承载 data_retention/auth/audit/redaction/release_gate_threshold。[policy.go](/home/codex/HUAKAI/backend/internal/proto/policy.go:37)
99. P-2 RequestToCanonical 需要从 vendor request policy 字段派生 policy nodes。
100. 例如 OpenAI Responses `store` 与 Anthropic data retention/cache 相关字段不能藏在 extensions。
101. 但 policy 未明确字段时可使用 `DataRetentionUnknown` 默认。
102. P-1 validator 明确把 INV-44 / INV-48 / INV-50 推迟到 P-2 决定。[envelope_validate.go](/home/codex/HUAKAI/backend/internal/proto/envelope_validate.go:79)
103. P-1 validator 还把 Audio Transport 与 Locator.Kind 映射推迟到 P-2。[envelope_validate.go](/home/codex/HUAKAI/backend/internal/proto/envelope_validate.go:927)
104. P-1 validator 把 media type / locator / codec 守门也推迟到 P-2。[envelope_validate.go](/home/codex/HUAKAI/backend/internal/proto/envelope_validate.go:963)
105. `NodeSourceRef` 的 block index 上界也留给 P-2 处理。[cross_ref.go](/home/codex/HUAKAI/backend/internal/proto/cross_ref.go:103)
106. `DataRetention` 多 node merge 语义也留给 P-2。[cross_ref.go](/home/codex/HUAKAI/backend/internal/proto/cross_ref.go:402)
107. 这些 deferred 项决定 P-2 是否需要 v0.4.1。
108. 当前 fixture 数量为 35 个，分 envelope/event/edge_case/response/regression 五类。
109. 本地 `find backend/internal/proto/fixtures -type f` 观测为 35。
110. 本地 `go test ./internal/proto -list 'TestINV'` 观测到 67 个 TestINV 函数入口。
111. Owner 上下文提到 291 TestINV；本计划不重新解释该数字，实施时应以 P-1 完成分支最终测试报告为准。
112. 当前 `anthropic_sse.go` 是 upstream adapter，不是 client adapter。
113. 它定义 `AnthropicAdapter` 并实现 `UpstreamAdapter`。[anthropic_sse.go](/home/codex/HUAKAI/backend/internal/proto/anthropic_sse.go:41)
114. 它的 `CanonicalToProviderRequest` 未实现，直接返回 `ErrNotImplemented`。[anthropic_sse.go](/home/codex/HUAKAI/backend/internal/proto/anthropic_sse.go:83)
115. 它的 `ProviderResponseToCanonical` 未实现，直接返回 `ErrNotImplemented`。[anthropic_sse.go](/home/codex/HUAKAI/backend/internal/proto/anthropic_sse.go:87)
116. 它的 `ProviderEventToCanonicalEvents` 已实现 upstream event -> canonical event。[anthropic_sse.go](/home/codex/HUAKAI/backend/internal/proto/anthropic_sse.go:91)
117. 它处理 `message_start`、content block lifecycle、`message_delta`、`message_stop`、unknown event。[anthropic_sse.go](/home/codex/HUAKAI/backend/internal/proto/anthropic_sse.go:136)
118. 它会在 `message_stop` 与 synthetic finalize 路径记录 cachemetrics per account/prefix。[anthropic_sse.go](/home/codex/HUAKAI/backend/internal/proto/anthropic_sse.go:160)
119. 它的 `FinalizeUpstreamStream` 会补未关闭 block 和 `message_stop`。[anthropic_sse.go](/home/codex/HUAKAI/backend/internal/proto/anthropic_sse.go:184)
120. 它支持 signature_delta 默认跳过、配置后保留。[anthropic_sse.go](/home/codex/HUAKAI/backend/internal/proto/anthropic_sse.go:243)
121. P-2 不能把这个 upstream finalize 误当作 client finalize。
122. 当前 `openai_sse.go` 也是 upstream adapter。
123. `OpenAIAdapter` 面向 OpenAI Chat Completions SSE 转 HCSF 事件。[openai_sse.go](/home/codex/HUAKAI/backend/internal/proto/openai_sse.go:15)
124. 它的 `CanonicalToProviderRequest` 未实现。[openai_sse.go](/home/codex/HUAKAI/backend/internal/proto/openai_sse.go:142)
125. 它的 `ProviderResponseToCanonical` 已将 buffered response 包入最小 HCSF envelope。[openai_sse.go](/home/codex/HUAKAI/backend/internal/proto/openai_sse.go:148)
126. 该 envelope 仅填 Version + BufferedResponse，不保证完整 ValidateEnvelope。[openai_sse.go](/home/codex/HUAKAI/backend/internal/proto/openai_sse.go:154)
127. 它的 `ProviderEventToCanonicalEvents` 已实现 data chunk -> canonical event。[openai_sse.go](/home/codex/HUAKAI/backend/internal/proto/openai_sse.go:174)
128. 它识别 `[DONE]` 并 finalize。[openai_sse.go](/home/codex/HUAKAI/backend/internal/proto/openai_sse.go:206)
129. 它用 `UnmarshalWithExtras` 保存 unknown top-level fields 到 event passthrough。[openai_sse.go](/home/codex/HUAKAI/backend/internal/proto/openai_sse.go:215)
130. 它支持 text delta、tool_call delta、finish_reason、usage-only chunk。[openai_sse.go](/home/codex/HUAKAI/backend/internal/proto/openai_sse.go:235)
131. 它 finalize 时补 block stop、usage delta、message_stop，并记录 OpenAI prompt cache read。[openai_sse.go](/home/codex/HUAKAI/backend/internal/proto/openai_sse.go:411)
132. 它的 buffered helper 支持 usage、text、tool_calls。[openai_sse.go](/home/codex/HUAKAI/backend/internal/proto/openai_sse.go:546)
133. P-2 openai_chat client adapter 可以复用内部 canonical event vocabulary，但不能复用 upstream adapter 职责边界。
134. 当前 `gemini_sse.go` 也是 upstream adapter。
135. `GeminiAdapter` 面向 Gemini streamGenerateContent SSE 转 HCSF 事件。[gemini_sse.go](/home/codex/HUAKAI/backend/internal/proto/gemini_sse.go:16)
136. 它的 `CanonicalToProviderRequest` 未实现。[gemini_sse.go](/home/codex/HUAKAI/backend/internal/proto/gemini_sse.go:97)
137. 它的 `ProviderResponseToCanonical` 已将 buffered response 包入最小 HCSF envelope。[gemini_sse.go](/home/codex/HUAKAI/backend/internal/proto/gemini_sse.go:103)
138. 它的 event path 支持 stream sentinel / `[DONE]` finalize。[gemini_sse.go](/home/codex/HUAKAI/backend/internal/proto/gemini_sse.go:121)
139. 它支持 functionCall 转 tool_use、thought text 转 reasoning_delta、inlineData 输出跳过并记 loss。[gemini_sse.go](/home/codex/HUAKAI/backend/internal/proto/gemini_sse.go:214)
140. 它 finalize 时补 open block、usage delta、message_stop。[gemini_sse.go](/home/codex/HUAKAI/backend/internal/proto/gemini_sse.go:305)
141. P-2 不新增 gemini client adapter。
142. P-2 只需要保证 gemini upstream canonical event 能被三种 client adapter 序列化。
143. 当前 gateway `StreamForwarder` 已有 `ClientAdapter proto.ClientAdapter` 可选字段。[forwarder.go](/home/codex/HUAKAI/backend/internal/gateway/forwarder.go:41)
144. 当前如果 `ClientAdapter == nil`，`clientChunks` 直接 raw SSE fallback。[forwarder.go](/home/codex/HUAKAI/backend/internal/gateway/forwarder.go:293)
145. 当前如果有 `ClientAdapter`，只调用 `CanonicalEventToClientChunk`。[forwarder.go](/home/codex/HUAKAI/backend/internal/gateway/forwarder.go:297)
146. 当前 forwarder 不调用 `FinalizeClientStream`。
147. 当前 forwarder 不调用 `CanonicalToClientResponse`。
148. 当前 forwarder 不调用 `RequestToCanonical`。
149. 所以四个 ClientAdapter hookpoint 中只有一个有 wire-up 入口，且仍未有实现。
150. 当前 `BuildDefaultProtocolAdapterRegistry` 注册的是 upstream adapter registry。[protocol_selector.go](/home/codex/HUAKAI/backend/internal/gateway/protocol_selector.go:71)
151. 它把 `anthropic_messages`、`openai_chat`、`openai_responses` 注册到 upstream adapter。[protocol_selector.go](/home/codex/HUAKAI/backend/internal/gateway/protocol_selector.go:81)
152. P-2 需要新增 client adapter registry，不应复用 upstream registry。
153. 当前 scanner registry 覆盖 `anthropic_messages`、`openai_chat`、`openai_responses` 等 protocol family。
154. P-2 可复用 scanner 输出的 canonical events，不需要改 scanner。
155. 当前 `/v1/messages` handler 实际复用 chat completions handler，只改 endpoint family。[chat_completions_handler.go](/home/codex/HUAKAI/backend/internal/gatewayhttp/chat_completions_handler.go:389)
156. 这意味着 Anthropic Messages client body 当前走 `chatRequest` 解析，能力严重不足。
157. P-2 必须拆出 client protocol selector，否则 `/v1/messages` 无法真正成为 Anthropic Messages compatible。
158. 当前 official vendor docs 的主要影响：
159. OpenAI Chat streaming chunk 是 `chat.completion.chunk`，chunk 中有 `choices[].delta` 与 terminal `finish_reason`。
160. OpenAI Responses streaming 是 named events，例如 `response.created`、`response.output_text.delta`、`response.completed`。
161. Anthropic Messages streaming 是 named events，且 JSON data 的 `type` 与 event name 对齐。
162. 因此三种 ClientAdapter 的 `CanonicalEventToClientChunk` 不能共用一个 SSE shape。
163. 只有 HCSF event -> client event 的中间 builder 可共享。
164. 当前 HCSF response fields 尚未覆盖的 P-1 schema：
165. RequestMeta：TenantID/RouteID/AccountID/AcquisitionToken/ClientProtocol/UpstreamProtocol/Provider/IngressPath/IdempotencyKey/SessionHash/NativePassthrough/EvidenceLabel。
166. RequestControls：Tools/ToolChoice/Stop/StopSequences/Temperature/TopP/SystemPrompt/ParallelToolCalls/ResponseFormat/Seed/ToolNameHashAlgorithm。
167. CapabilityGraph：15 concrete node kinds、edge relationships、node/edge/graph ProtocolLoss。
168. ProviderProjection：per-node projection verdict、native path、overall verdict。
169. StreamPlan：event_classes、flush_policy、terminal flags、fallback boundary。
170. Accounting：reasoning/live/batch usage 与 evidence label。
171. Policy：data retention、auth、audit、redaction、release gate threshold。
172. Extensions：vendor/experimental prefix allowed，但不能隐藏 capability drop。
173. 当前 hookpoint 覆盖表：
174. `RequestToCanonical`：接口存在，无实现，无 gateway wire-up。
175. `CanonicalToClientResponse`：接口存在，无实现，无 gateway wire-up。
176. `CanonicalEventToClientChunk`：接口存在，forwarder 可调用，但三协议无实现。
177. `FinalizeClientStream`：接口存在，无 forwarder wire-up，无实现。
178. Upstream `ProviderEventToCanonicalEvents`：Anthropic/OpenAI/Gemini 已有不同程度实现。
179. Upstream `ProviderResponseToCanonical`：OpenAI/Gemini 有最小 buffered envelope；Anthropic 未实现。
180. Upstream `FinalizeUpstreamStream`：Anthropic/OpenAI/Gemini 都已有。
181. 结论：P-2 是 client boundary work，不是 upstream adapter work。
182. 结论：P-2 必须先做 shared canonical response/event builder，避免三 adapter 重复拼 event。
183. 结论：P-2 要补 contract tests，否则只靠 smoke raw passthrough 会掩盖 client adapter 失效。

## 3. P-2 切片

184. 建议 P-2 切成 D0 到 D13。
185. D0 是 shared infrastructure，不算三协议 × 四 hookpoint，但能降低重复。
186. D1-D12 是 3 × 4 正式矩阵。
187. D13 是 integration / docs / release gate。
188. 每个 D 切片都必须有明确输入、输出、测试、风险。
189. D0：ClientAdapter shared foundation。
190. D0 范围：新增 `backend/internal/proto/client_adapter_common.go` 或等价文件。
191. D0 范围：定义 `RequestMetaSeed` context helper。
192. D0 范围：定义 `ClientStreamState` interface 或每协议 state common fields。
193. D0 范围：定义 canonical event helper：message_start、content_block_start、delta、stop、usage 转协议块。
194. D0 范围：定义 JSON/SSE emit helper。
195. D0 范围：定义 typed loss constructor，优先写 v0.4 fields。
196. D0 范围：定义 adapter registry。
197. D0 不做任何 vendor-specific parsing。
198. D0 测试 positive：context seed 注入后 RequestMeta required fields 可填充。
199. D0 测试 positive：SSE emit 保留 event name + JSON data。
200. D0 测试 positive：Passthrough merge typed wins on conflict。
201. D0 测试 negative：缺 request_id / ingress_path 时 `RequestToCanonical` 返回 INV-5 风格错误。
202. D0 测试 negative：unknown client protocol registry miss fail-loud。
203. D0 测试 negative：loss entry severity set 但 reason/code 空被拒。
204. D0 依赖：P-1 HCSFEnvelope / ProtocolLoss / Passthrough helper。
205. D1：anthropic_messages.RequestToCanonical。
206. D1 输入：Anthropic Messages HTTP body。
207. D1 输出：HCSF v0.4 request envelope。
208. D1 必须解析 `model`、`max_tokens`、`messages`、`system`、`stop_sequences`、`temperature`、`top_p`、`stream`、`tools`、`tool_choice`。
209. D1 必须把 messages content string 规范为 text block。
210. D1 必须把 content block array 规范为 CanonicalContentBlock 与 CapabilityNode。
211. D1 必须把 `tool_use` / `tool_result` content block 映射到 graph node + requires edge。
212. D1 必须把 extended thinking request 映射到 thinking node 或 RequestControls extension，具体由 synthesis 决定。
213. D1 必须把 cache_control 映射到 cache_control node，不允许只放 extensions。
214. D1 必须把 system prompt 转 RequestControls.SystemPrompt 与 text/system node。
215. D1 positive tests：basic single user text。
216. D1 positive tests：system + two user/assistant turns。
217. D1 positive tests：tool_use + tool_result chain。
218. D1 positive tests：cache_control block creates cache node + source ref。
219. D1 positive tests：stream=true creates StreamPlan.Mode=streaming。
220. D1 negative tests：missing model。
221. D1 negative tests：missing max_tokens when protocol requires it。
222. D1 negative tests：tool_result has no matching tool_use -> INV-19。
223. D1 negative tests：content block unknown type -> ProtocolLoss error or 400。
224. D1 negative tests：cache_control breakpoint ref cannot resolve -> INV-26。
225. D1 依赖 D0。
226. D2：anthropic_messages.CanonicalToClientResponse。
227. D2 输入：HCSF buffered envelope with CanonicalResponse。
228. D2 输出：Anthropic Message JSON response。
229. D2 必须输出 `type=message` style response。
230. D2 必须把 CanonicalResponse.Content text/tool_use/reasoning_summary 转 Anthropic content blocks。
231. D2 必须把 CanonicalUsage input/output/cache fields 转 Anthropic usage naming。
232. D2 必须把 canonical stop reason 转 Anthropic stop_reason。
233. D2 对 unsupported content kind 不能丢弃，必须 loss audit。
234. D2 positive tests：text response。
235. D2 positive tests：tool_use response with canonical call id converted to Anthropic-style id policy。
236. D2 positive tests：thinking response obeys redaction policy。
237. D2 positive tests：usage cache fields serialized。
238. D2 negative tests：nil buffered response。
239. D2 negative tests：unknown canonical stop reason。
240. D2 negative tests：tool_use missing call_id。
241. D2 negative tests：hidden thinking tries to serialize visible text。
242. D2 negative tests：passthrough conflict uses typed field wins。
243. D2 依赖 D0、D1 loss helpers。
244. D3：anthropic_messages.CanonicalEventToClientChunk。
245. D3 输入：CanonicalEvent。
246. D3 输出：Anthropic Messages SSE bytes。
247. D3 必须输出 named events：`message_start`、`content_block_start`、`content_block_delta`、`content_block_stop`、`message_delta`、`message_stop`。
248. D3 必须维护 content block index。
249. D3 必须把 tool input deltas 序列化为 partial JSON delta。
250. D3 必须把 reasoning_delta 序列化为 thinking delta 或 policy loss。
251. D3 必须把 signature_delta 只在 policy 允许时输出。
252. D3 positive tests：canonical text lifecycle -> 5 Anthropic events。
253. D3 positive tests：tool input deltas preserve index。
254. D3 positive tests：message_delta usage cumulative。
255. D3 positive tests：passthrough top-level vendor fields merge。
256. D3 negative tests：delta before block_start。
257. D3 negative tests：duplicate message_stop idempotent no double terminal。
258. D3 negative tests：unknown event type -> loss + no output。
259. D3 negative tests：tool_input_delta invalid partial JSON policy path。
260. D3 negative tests：hidden thinking emitted as visible text blocked。
261. D3 依赖 D0。
262. D4：anthropic_messages.FinalizeClientStream。
263. D4 输入：Anthropic client stream state。
264. D4 输出：0..N final SSE chunks。
265. D4 必须补未关闭 content_block_stop。
266. D4 必须补 message_delta usage when available。
267. D4 必须补 message_stop once。
268. D4 必须返回 no-op on repeated finalize。
269. D4 不直接 settle billing ledger。
270. D4 只产出 finalize chunks 与 loss audit summary hook。
271. D4 positive tests：open text block finalize。
272. D4 positive tests：open tool block finalize。
273. D4 positive tests：already terminated finalize returns empty。
274. D4 positive tests：usage present emits final message_delta。
275. D4 negative tests：state type mismatch。
276. D4 negative tests：state has impossible block index。
277. D4 negative tests：loss audit sink fails -> returns error before terminal。
278. D4 negative tests：cache mutation callback invoked twice is prevented。
279. D4 依赖 D3。
280. D5：openai_chat.RequestToCanonical。
281. D5 输入：OpenAI Chat Completions body。
282. D5 输出：HCSF request envelope。
283. D5 必须解析 `model/messages/stream/tools/tool_choice/max_tokens/max_completion_tokens/stop/temperature/top_p/response_format/seed/parallel_tool_calls`。
284. D5 必须处理 `developer` role。
285. D5 必须处理 message content string 与 content part array。
286. D5 必须处理 image_url/input_audio/file 等 multimodal parts，未 first-class 的走 explicit projection verdict。
287. D5 必须把 `response_format` 写 RequestControls.ResponseFormat + structured_output node。
288. D5 必须把 `prompt_tokens_details.cached_tokens` 仅作为 response usage，不从 request 写。
289. D5 positive tests：basic chat text。
290. D5 positive tests：developer + user roles。
291. D5 positive tests：tools + tool_choice auto。
292. D5 positive tests：response_format json_schema。
293. D5 positive tests：image_url creates image node。
294. D5 negative tests：missing model。
295. D5 negative tests：messages not array。
296. D5 negative tests：tool schema not object。
297. D5 negative tests：duplicate tool call id in assistant history。
298. D5 negative tests：unsupported content part has explicit loss。
299. D5 依赖 D0。
300. D6：openai_chat.CanonicalToClientResponse。
301. D6 输入：HCSF buffered envelope。
302. D6 输出：OpenAI chat.completion JSON response。
303. D6 必须输出 `choices[0].message`。
304. D6 必须把 text blocks 合并为 content string 或 content parts，策略需决策。
305. D6 必须把 tool_use blocks 输出为 `tool_calls`。
306. D6 必须把 canonical stop reason 映射为 `finish_reason`。
307. D6 必须把 usage input/output/cache/reasoning 转 OpenAI usage naming。
308. D6 positive tests：text response。
309. D6 positive tests：tool_calls response。
310. D6 positive tests：max_tokens -> finish_reason length。
311. D6 positive tests：cache read usage maps to prompt_tokens_details.cached_tokens。
312. D6 negative tests：nil buffered response。
313. D6 negative tests：multiple assistant messages in response。
314. D6 negative tests：tool_use input not JSON object/stringable。
315. D6 negative tests：unknown canonical block -> loss。
316. D6 negative tests：passthrough conflict typed wins。
317. D6 依赖 D0。
318. D7：openai_chat.CanonicalEventToClientChunk。
319. D7 输入：CanonicalEvent。
320. D7 输出：OpenAI Chat SSE chunks with `data: <chat.completion.chunk>` and final `data: [DONE]` if policy says terminal.
321. D7 必须 emit role delta on message_start or first block start。
322. D7 必须 emit content delta for text_delta。
323. D7 必须 emit tool_calls incremental deltas for tool_input_delta。
324. D7 必须 emit finish_reason on message_delta / stop。
325. D7 必须 respect passthrough fields like system_fingerprint/service_tier。
326. D7 positive tests：text lifecycle -> role chunk + content chunks + finish + DONE。
327. D7 positive tests：tool call delta accumulation with stable index。
328. D7 positive tests：usage-only chunk when stream_options include_usage equivalent is enabled。
329. D7 positive tests：passthrough merge top-level field。
330. D7 negative tests：delta before role/message_start。
331. D7 negative tests：duplicate terminal only one DONE。
332. D7 negative tests：tool_call index missing。
333. D7 negative tests：unknown stop reason maps to null or loss per policy。
334. D7 negative tests：state type mismatch。
335. D7 依赖 D0。
336. D8：openai_chat.FinalizeClientStream。
337. D8 必须 close open tool/text chunks。
338. D8 必须 emit final finish chunk if upstream omitted terminal。
339. D8 必须 emit `[DONE]` once。
340. D8 必须 publish loss audit summary to forwarder draft, not billing ledger。
341. D8 positive tests：EOF no terminal emits finish + DONE。
342. D8 positive tests：already done no output。
343. D8 positive tests：usage final chunk policy enabled。
344. D8 positive tests：partial tool args block closes safely。
345. D8 negative tests：state mismatch。
346. D8 negative tests：impossible tool index。
347. D8 negative tests：finalize callback failure。
348. D8 negative tests：double finalize does not double cache mutate。
349. D8 依赖 D7。
350. D9：openai_responses.RequestToCanonical。
351. D9 输入：OpenAI Responses body。
352. D9 输出：HCSF request envelope。
353. D9 必须解析 `model/input/instructions/stream/tools/tool_choice/parallel_tool_calls/reasoning/text/max_output_tokens/store/metadata/previous_response_id`。
354. D9 必须处理 input string -> user text。
355. D9 必须处理 input array items。
356. D9 必须把 `instructions` 映射为 system prompt。
357. D9 必须把 `text.format` 映射 structured_output。
358. D9 必须把 built-in tools 中无法 first-class 的部分写 plugin/native projection，不丢 feature。
359. D9 必须把 `previous_response_id` 作为 session/native hint，不伪造 conversation storage。
360. D9 positive tests：input string。
361. D9 positive tests：input message array with input_text/input_image/input_file。
362. D9 positive tests：function tool。
363. D9 positive tests：reasoning effort + summary policy。
364. D9 positive tests：text.format json_schema。
365. D9 negative tests：missing model。
366. D9 negative tests：input item missing type。
367. D9 negative tests：unsupported built-in tool -> native_required loss。
368. D9 negative tests：store=true policy ambiguity recorded。
369. D9 negative tests：previous_response_id without native passthrough policy decision。
370. D9 依赖 D0。
371. D10：openai_responses.CanonicalToClientResponse。
372. D10 输入：HCSF buffered envelope。
373. D10 输出：OpenAI Response JSON object。
374. D10 必须输出 `object=response`。
375. D10 必须输出 `output[]` item array。
376. D10 必须把 text blocks 转 `message.content[].output_text`。
377. D10 必须把 tool_use 转 `function_call` output item。
378. D10 必须把 usage 转 input/output/total token shape。
379. D10 必须输出 status / incomplete_details。
380. D10 positive tests：text output。
381. D10 positive tests：function_call output。
382. D10 positive tests：reasoning usage。
383. D10 positive tests：incomplete max_tokens output。
384. D10 negative tests：nil buffered response。
385. D10 negative tests：unknown block requires native/loss。
386. D10 negative tests：missing output item id generation policy。
387. D10 negative tests：metadata passthrough conflict。
388. D10 negative tests：tool input malformed。
389. D10 依赖 D0。
390. D11：openai_responses.CanonicalEventToClientChunk。
391. D11 输入：CanonicalEvent。
392. D11 输出：OpenAI Responses SSE event bytes。
393. D11 必须 emit `response.created` / `response.in_progress` once at stream start。
394. D11 必须 emit `response.output_item.added` and `response.content_part.added` for first text block。
395. D11 必须 emit `response.output_text.delta` for text delta。
396. D11 必须 emit tool/function call deltas or loss/native_required when not representable。
397. D11 必须 emit `response.completed` with usage at terminal。
398. D11 positive tests：text lifecycle official event order。
399. D11 positive tests：usage final response.completed。
400. D11 positive tests：function_call lifecycle。
401. D11 positive tests：passthrough metadata retained。
402. D11 negative tests：delta before output_item.added。
403. D11 negative tests：duplicate response.completed。
404. D11 negative tests：unknown event type -> loss。
405. D11 negative tests：missing response id generation seed。
406. D11 negative tests：state mismatch。
407. D11 依赖 D0。
408. D12：openai_responses.FinalizeClientStream。
409. D12 必须 finish open content part。
410. D12 必须 finish open output item。
411. D12 必须 emit `response.completed` or `response.incomplete` based on stream end class。
412. D12 必须 not emit `[DONE]` unless OpenAI Responses docs/SDK compatibility requires it after verification。
413. D12 positive tests：normal terminal no extra output。
414. D12 positive tests：EOF no terminal emits completed/incomplete per policy。
415. D12 positive tests：open tool item closes。
416. D12 positive tests：usage retained。
417. D12 negative tests：state mismatch。
418. D12 negative tests：double finalize no duplicate completed。
419. D12 negative tests：loss audit sink failure。
420. D12 negative tests：cache mutate callback double-call prevented。
421. D12 依赖 D11。
422. D13：integration / docs / release gate。
423. D13 范围：client adapter registry 接入 forwarder。
424. D13 范围：gatewayhttp client protocol selector 接入 `/v1/chat/completions`、`/v1/responses`、`/v1/messages`。
425. D13 范围：non-streaming route 是否打开按 Owner 决策执行。
426. D13 范围：docs/specs/protocol-translation implementer notes 更新。
427. D13 范围：docs/process/reviews/PENDING 或 release notes 记录 test gaps。
428. D13 positive tests：OpenAI Chat client -> Anthropic upstream canonical -> OpenAI Chat SSE。
429. D13 positive tests：Anthropic client -> OpenAI upstream canonical -> Anthropic SSE。
430. D13 positive tests：Responses client -> Gemini upstream canonical -> Responses SSE。
431. D13 positive tests：buffered canonical -> three client JSON shapes。
432. D13 negative tests：nil registry fail-loud。
433. D13 negative tests：unknown client protocol 404/400。
434. D13 negative tests：adapter error aborts claim without settle。
435. D13 negative tests：client disconnect calls finalize/drain once。
436. D13 依赖 D1-D12。
437. 跨切片依赖总表：
438. D0 -> all。
439. D1 -> D2/D3/D4 test fixtures。
440. D5 -> D6/D7/D8 test fixtures。
441. D9 -> D10/D11/D12 test fixtures。
442. D3/D7/D11 -> D4/D8/D12。
443. D2/D6/D10 -> D13 buffered integration。
444. D4/D8/D12 -> D13 stream finalization integration。
445. Gateway wire-up should wait until at least D3/D7/D11 are green。
446. Non-streaming route opening should wait until D2/D6/D10 are green。
447. Test matrix minimum：
448. 每个 adapter/hookpoint 至少 4 positive + 4 negative。
449. 三 adapters × 四 hookpoints × 8 = 96 adapter-level tests。
450. D0 shared tests至少 6。
451. D13 integration tests至少 8。
452. 总新增测试建议 110-130 个命名测试或 subtests。
453. 这些新增测试不需要全部命名 `TestINV`。
454. INV 类测试只用于 schema invariant。
455. Adapter behavior 测试建议命名 `TestClientAdapter_<Protocol>_<Hook>_<Scenario>`。
456. Golden fixture 建议新增：
457. `fixtures/client/anthropic_request_basic.json`。
458. `fixtures/client/anthropic_request_tool_chain.json`。
459. `fixtures/client/anthropic_response_text.json`。
460. `fixtures/client/anthropic_stream_text_lifecycle.sse`。
461. `fixtures/client/openai_chat_request_basic.json`。
462. `fixtures/client/openai_chat_request_tool.json`。
463. `fixtures/client/openai_chat_response_tool.json`。
464. `fixtures/client/openai_chat_stream_tool.sse`。
465. `fixtures/client/openai_responses_request_basic.json`。
466. `fixtures/client/openai_responses_request_tool.json`。
467. `fixtures/client/openai_responses_response_text.json`。
468. `fixtures/client/openai_responses_stream_text_lifecycle.sse`。
469. Negative fixture 建议用 `_invalid_` 前缀复用 fixture walker。
470. 但 request body negative 不一定是 HCSF fixture，建议单独 walker。
471. Property tests：
472. P-2 至少保留一个 request roundtrip property：client request -> HCSF -> same client buffered/stream skeleton 不丢 mandatory fields。
473. P-2 至少保留一个 stream equivalence property：canonical event replay -> client chunks -> parseable by client protocol scanner。
474. P-2 至少保留一个 loss audit property：任何 unsupported projection 均有 non-silent ProtocolLoss。
475. P-2 至少保留一个 passthrough property：unknown top-level fields survive output unless typed conflict。
476. P-2 至少保留一个 idempotent finalize property：Finalize called N times still one terminal。

## 4. INV 扩展（是否需要 v0.4 -> v0.4.1 minor bump）

477. 我建议 P-2 做 v0.4.1 minor bump。
478. 理由：P-1 validator 明确把 INV-44 / INV-48 / INV-50 留给 P-2。
479. 理由：P-2 会第一次给 client adapter 定义 request source ref 上界、media locator mapping、多 data_retention merge。
480. 这属于 validator 行为增强，不是只写 adapter。
481. 但 v0.4.1 不应改动已有 v0.4 JSON 字段名。
482. v0.4.1 应作为 `HCSFVersion` 是否仍填 `"0.4"` 的兼容决策。
483. 选项 A：保持 wire version `"0.4"`，只在 docs 称 validator profile v0.4.1。
484. 选项 B：把 `HCSFVersion` 改为 `"0.4.1"`。
485. 选项 B 会导致所有 fixtures 和 version guard 改动，blast radius 更大。
486. 我建议选项 A，除非 Owner/PM 要版本号严格反映 validator profile。
487. INV-44 建议定义：projection severity 必填。
488. 当前 `ProtocolLossEntry.Severity` 是推荐填，不是强制。
489. P-2 client adapter 若要被 ops UI / release gate 消费，projection-level loss 应强制 severity。
490. INV-44 草案：`ProviderProjection.CapabilityResults[].ProtocolLoss[]` 在 verdict != preserved 时必须 `Severity` 非空。
491. INV-44 positive：lossy projection with severity warning passes。
492. INV-44 negative：lossy projection reason exists but severity empty fails。
493. INV-48 建议定义：CacheKeyHint / cache breakpoint 长度启发式。
494. 当前 `CacheControlNode` 已有 scope/locality/breakpoint refs 约束。
495. P-2 client adapter 会从 Anthropic/OpenAI prompt cache hints 派生 cache nodes。
496. INV-48 应防止过短 prompt prefix 写入 PASR cache hint。
497. INV-48 草案：当 cache scope 是 block/message 且 locality_hint 非空时，source text 或 cache key hint 必须达到最小长度阈值。
498. 阈值建议先不写死在 schema，而写 validator option。
499. 如果当前 validator 无 option 机制，P-2 先只做 adapter-level check，不进 INV。
500. INV-50 建议保留给 client adapter state/finalize idempotency。
501. INV-50 草案：StreamPlan.Mode=streaming 时 EventClasses 必须覆盖 client adapter 可能 emit 的 terminal class。
502. 也可把 INV-50 留空，不急于填满编号。
503. 我倾向 P-2 只落 INV-44。
504. INV-48 先做 adapter negative test，不进 global validator。
505. INV-50 保留。
506. Audio Transport <-> Locator.Kind 映射：P-2 需要 request parser 处理 OpenAI Chat audio / Responses input_audio 等。
507. 如果实现 audio/file/image locator，就需要补一个 INV-23 extension。
508. 但 P-2 首轮可以把 audio/video/live/batch/MCP 映射为 unsupported/native_required，不强推 full media validation。
509. DataRetention 多 node merge：P-2 必须至少决定 strict policy。
510. 建议 P-2 不支持多 data_retention nodes merge。
511. 多来源 data_retention policy 一律归并到 one policy node + graph-level notes。
512. 若 body 同时声明冲突 retention policy，RequestToCanonical 返回 400 + ProtocolLoss error。
513. 这个策略不需要新 INV，只需要 D1/D9 negative tests。
514. Version bump 决策表：
515. 若只新增 adapter tests，不改 ValidateEnvelope：不 bump。
516. 若新增 INV-44 且仍 `HCSFVersion="0.4"`：docs profile bump 到 v0.4.1。
517. 若修改 `HCSFVersion`：需要 Owner 批准，因为 fixture blast radius 大。
518. Codex 建议：采用 profile bump，不改 wire version。

## 5. 风险与权衡

519. 风险一：vendor docs 模糊或有 undocumented quirks。
520. 处理方式：只把官方 docs 明确字段做 typed first-class。
521. 处理方式：未明确但观察到的 fields 走 passthrough + FieldMatrix preserved_default。
522. 处理方式：不把 mock upstream 行为当 vendor truth。
523. 处理方式：real upstream smoke 只验证 parseability，不扩大为 protocol spec。
524. 风险二：OpenAI Chat 与 Responses 都属于 OpenAI，但 stream event shape 完全不同。
525. 处理方式：共享 CanonicalEvent builder，但 separate client serializer。
526. 风险三：Anthropic Messages 的 tool_result 在 user message content 中，OpenAI Chat tool result 是 tool role message。
527. 处理方式：HCSF graph 用 tool_use/tool_result + requires edge 统一，client adapter 做角色投影。
528. 风险四：tool call id 双向转换可能污染 upstream/client identifier。
529. 处理方式：canonical id 保持 `call_` 内部格式，client serializer 根据协议生成外部格式。
530. 风险五：cache mutation 被放进 client finalize 会越权。
531. 处理方式：client finalize 只调用 callback/interface，不直接写 cachemetrics 或 PASR state。
532. 风险六：billing settle 被放进 client adapter 会触碰高风险 billing ledger。
533. 处理方式：client adapter 只返回 chunks/loss/finalize metadata，settler 仍由 gatewayhttp 调用。
534. 风险七：stream terminal 双发导致 client SDK hang 或提前结束。
535. 处理方式：每协议 state 必须有 `Terminated` guard，Finalize idempotency 必测。
536. 风险八：response passthrough 与 typed fields 冲突。
537. 处理方式：沿用 `MergeExtrasInto` typed wins 语义。
538. 风险九：D13 一次接入三 route 容易扩大 blast radius。
539. 处理方式：先只在 proto tests + forwarder unit tests 接入，再打开 gatewayhttp route。
540. 风险十：official docs 更新快。
541. 处理方式：P-2 artifact 记录 vendor docs fetch date；实现前再 fetch 一次 OpenAI/Anthropic docs。
542. mock upstream test 搭法：
543. proto unit tests 使用 raw JSON/SSE fixtures，不起 HTTP server。
544. forwarder tests 使用 in-memory reader + httptest recorder。
545. gatewayhttp smoke 使用 mock upstream server。
546. mock upstream 只做 deterministic shape，不声称 vendor behavior。
547. real-upstream smoke：
548. 默认不在 CI 跑真实 vendor。
549. 手动 smoke 仅 4 vendor：anthropic、openai、gemini、codex。
550. 这 4 vendor 口径来自当前项目 memory / handler comments，不在本计划中作为外部 capability claim。
551. real smoke 需要 env-gated：`HUAKAI_REAL_SMOKE=1`。
552. real smoke 不使用真实 production secrets。
553. real smoke 输出只记录 request_id、protocol、status、parse result，不记录 prompt/secret。
554. 失败不自动阻塞 unit release，除非 Owner 指定 release candidate。
555. clean-room 权衡：
556. 不读取 reference project 源码会降低 undocumented compatibility 覆盖。
557. 但 P-2 是 client protocol adapter，官方协议已足够定义合法 request/response/chunk。
558. 若后续要做 reference-derived quirks，需要单独 clean-room lane guard。
559. 本次 P-2 plan 不包含 quirk mining。
560. security 权衡：
561. client adapter 解析 raw body，必须 size-bound。
562. handler 目前用 `http.MaxBytesReader` 限 1 MiB。[chat_completions_handler.go](/home/codex/HUAKAI/backend/internal/gatewayhttp/chat_completions_handler.go:100)
563. P-2 保持该上限，或由 route policy 配置。
564. unknown extension 不得携带 secret fields 到 logs。
565. ProtocolLoss reason 不得包含 raw user prompt。
566. performance 权衡：
567. request graph 构建会增加 CPU/alloc。
568. P-2 先追求 correctness。
569. Exit criteria 加入 microbench smoke，不设置硬 p99。
570. compatibility 权衡：
571. Strict validation 会拒绝一些宽松 SDK payload。
572. 建议 request parser 分两层：wire JSON lenient parse + HCSF strict validate。
573. 当 lenient accepted 但 HCSF strict 不可表达，返回 structured 400 + loss/code。

## 6. 工作量估计（engineer-day）

574. D0 shared foundation：1.0 到 1.5 天。
575. D1 anthropic RequestToCanonical：0.75 到 1.0 天。
576. D2 anthropic buffered response：0.5 到 0.75 天。
577. D3 anthropic stream chunks：0.75 到 1.0 天。
578. D4 anthropic finalize：0.25 到 0.5 天。
579. D5 openai_chat RequestToCanonical：0.75 到 1.0 天。
580. D6 openai_chat buffered response：0.5 到 0.75 天。
581. D7 openai_chat stream chunks：0.75 到 1.0 天。
582. D8 openai_chat finalize：0.25 到 0.5 天。
583. D9 openai_responses RequestToCanonical：1.0 到 1.5 天。
584. D10 openai_responses buffered response：0.75 到 1.0 天。
585. D11 openai_responses stream chunks：1.0 到 1.5 天。
586. D12 openai_responses finalize：0.25 到 0.5 天。
587. D13 integration/docs/review fixes：1.0 到 1.5 天。
588. 合计低估：8.25 天。
589. 合计高估：12.0 天。
590. 若不打开 gatewayhttp non-streaming route，可压到 8.0 到 10.0 天。
591. 若要完整打开 `/v1/responses` HTTP route，增加 1.0 到 1.5 天。
592. 若要加 real-upstream smoke harness，增加 0.75 到 1.0 天。
593. 若要把 INV-44 写入 global validator，增加 0.25 到 0.5 天。
594. 若要把 `HCSFVersion` 改到 `"0.4.1"`，增加 0.5 到 1.0 天，且需要 fixture churn。

## 7. 验证标准（exit criteria）

595. Exit 1：新增 client adapter registry，三协议均可按 client protocol resolve。
596. Exit 2：`RequestToCanonical` 三协议 positive/negative tests 通过。
597. Exit 3：每个 RequestToCanonical positive 输出完整 `ValidateEnvelope` 通过。
598. Exit 4：每个 RequestToCanonical negative 返回 typed error，不 panic。
599. Exit 5：`CanonicalToClientResponse` 三协议 buffered golden tests 通过。
600. Exit 6：buffered output JSON 可被对应 client protocol parser roundtrip。
601. Exit 7：`CanonicalEventToClientChunk` 三协议 stream golden tests 通过。
602. Exit 8：stream output SSE event names 与官方 docs shape 对齐。
603. Exit 9：`FinalizeClientStream` 三协议 idempotency tests 通过。
604. Exit 10：client disconnect / upstream EOF no terminal 路径不会重复 terminal。
605. Exit 11：ProtocolLoss v0.4 entries 非 silent。
606. Exit 12：unsupported/native_required feature 不被 silently dropped。
607. Exit 13：passthrough unknown fields preserve-by-default tests 通过。
608. Exit 14：P-1 35 fixtures 全部仍通过 `TestFixtures_AllValidate`。
609. Exit 15：P-1 INV tests 不回归。
610. Exit 16：`go test ./internal/proto` 通过。
611. Exit 17：`go test ./internal/gateway` 通过。
612. Exit 18：`go test ./internal/gatewayhttp` 通过。
613. Exit 19：如 D13 接入 route，dispatch smoke 覆盖 client adapter path，不再只测 raw passthrough。
614. Exit 20：docs/specs/protocol-translation implementer notes 更新。
615. Exit 21：风险登记或 release notes 记录 non-opened route / real-smoke gap。
616. Exit 22：提交前运行 `codex exec review --uncommitted --full-auto`。
617. Exit 23：HIGH findings 全部修复。
618. Exit 24：MED findings 修复或 Owner/PM 接受延期。
619. Exit 25：Chinese Owner summary 包含功能缩水、clean-room、安全风险。
620. 不满足 Exit 1-12 时，不得宣称 P-2 complete。
621. 不满足 Exit 16-18 时，不得进入 P-3，除非 Owner/PM 明确接受 test gap。
622. 不满足 Exit 22-24 时，不得 commit。

## 8. 与 Claude lane 的 cross-discuss 流程

623. Codex 本文件是独立 plan。
624. Codex 未读取 Claude plan。
625. Claude 也应独立写 `docs/process/plans/2026-05-12-p2-client-adapter-plan-claude.md`。
626. 两份 plan 完成后由 PM 做 synthesis。
627. synthesis 文件建议名：`docs/process/plans/2026-05-12-p2-client-adapter-plan.md`。
628. cross-discuss 首先对齐 scope。
629. 对齐点一：是否把 `/v1/responses` HTTP route 打开纳入 P-2。
630. 对齐点二：是否把 non-streaming `/v1/chat/completions` 与 `/v1/messages` route 打开纳入 P-2。
631. 对齐点三：RequestMetaSeed 走 context 还是改接口签名。
632. 对齐点四：v0.4.1 是 docs profile bump 还是 wire version bump。
633. 对齐点五：openai_responses stream 是否输出 `[DONE]`。
634. 对齐点六：tool call id client format 是否可逆映射还是 preserve canonical。
635. 对齐点七：cache mutation callback 属于 client finalize 还是 forwarder finalization。
636. 对齐点八：ProtocolLoss severity 在 projection 层是否 P-2 强制。
637. 对齐点九：real-upstream smoke 是否 release blocker。
638. 对齐点十：Responses built-in tools 是 native_required、plugin、还是 minimal first-class。
639. synthesis 应输出 Agreements。
640. synthesis 应输出 Conflicts。
641. synthesis 应输出 Gaps。
642. synthesis 应明确 Owner/PM 决策点。
643. synthesis 通过后才进入实现。
644. 实现应按 D0 -> D1/D5/D9 -> D2/D6/D10 -> D3/D7/D11 -> D4/D8/D12 -> D13。
645. 如果 Claude plan 把 gatewayhttp route 放在前面，Codex 建议先完成 proto-level adapter tests 再 wire-up。
646. 如果 Claude plan 建议读 reference project 源码，本 P-2 实现计划应拆成独立 clean-room specifier lane，不能在当前 implementer-style plan 中读取。
647. 如果 Claude plan 建议省略某协议 hookpoint，Codex 建议标为 Mandatory Roadmap 或 Safe Equivalent，不能 silent drop。
648. 如果两份 plan 对 test count 差异大，以 per-adapter/per-hookpoint matrix 覆盖为准，不以行数为准。

## 需要 Owner/PM 决策点

649. 决策 1：P-2 是否打开 `/v1/responses` HTTP route，还是只落 proto adapter 与 forwarder-level tests。
650. 决策 2：P-2 是否打开 non-streaming `/v1/chat/completions` 和 `/v1/messages`。
651. 决策 3：`RequestToCanonical` 的 RequestMeta 注入走 context seed，还是修改 ClientAdapter 接口签名。
652. 决策 4：是否采用 v0.4.1 validator profile bump；是否保持 wire `HCSFVersion="0.4"`。
653. 决策 5：是否强制 INV-44 projection-level ProtocolLoss severity 必填。
654. 决策 6：OpenAI Responses stream 末尾是否追加 `[DONE]` 兼容 chunk。
655. 决策 7：Responses built-in tools 在 P-2 是 `native_required`、Plugin shell，还是 first-class partial implementation。
656. 决策 8：tool call id 对 client 是否 preserve canonical `call_`，还是按 vendor dialect 重新生成。
657. 决策 9：real-upstream smoke 是否作为 P-2 exit blocker。
658. 决策 10：cache mutate / billing settle 的 finalize callback 边界由 forwarder 还是 client adapter 拥有。
659. 决策 11：P-2 是否允许新增 runtime dependency；Codex 建议不允许。
660. 决策 12：P-2 是否更新 docs/specs/protocol-translation 的 Released spec，还是只填 Implementer Notes。

## 执行前检查清单

661. 检查 P-1 分支最终状态，确认 HCSF v0.4 + INV-14..49 已合入。
662. 跑 `go test ./internal/proto` 获取 baseline。
663. 跑 `go test ./internal/gateway` 获取 baseline。
664. 跑 `go test ./internal/gatewayhttp` 获取 baseline。
665. 再次 fetch OpenAI `/v1/chat/completions` 与 `/v1/responses` 官方 OpenAPI。
666. 再次 fetch Anthropic Messages create + streaming 官方 docs。
667. 确认不读取 sub2api / new-api / portkey 等 reference project 源码。
668. 确认 Claude/Codex synthesis 已存在。
669. 确认 Owner/PM 对第 649-660 行决策点给出取舍。
670. 确认高风险文件不在 write scope。
671. 确认新增 fixture 命名不会污染 P-1 fixture walker。
672. 确认 adapter-level errors 不泄漏 prompt/secrets。
673. 确认 commit 前 Codex review 流程排期。

## Sources

674. HUAKAI project brief: [docs/01_PROJECT_BRIEF.md](/home/codex/HUAKAI/docs/01_PROJECT_BRIEF.md:1)。
675. HUAKAI feature parity matrix: [docs/03_FEATURE_PARITY_MATRIX.md](/home/codex/HUAKAI/docs/03_FEATURE_PARITY_MATRIX.md:1)。
676. HUAKAI risk register: [docs/10_RISK_REGISTER.md](/home/codex/HUAKAI/docs/10_RISK_REGISTER.md:1)。
677. HUAKAI agent workflow: [docs/12_AGENT_WORKFLOW.md](/home/codex/HUAKAI/docs/12_AGENT_WORKFLOW.md:1)。
678. HUAKAI release gates: [docs/15_RELEASE_GATES.md](/home/codex/HUAKAI/docs/15_RELEASE_GATES.md:1)。
679. Protocol translation spec: [docs/specs/protocol-translation.md](/home/codex/HUAKAI/docs/specs/protocol-translation.md:45)。
680. Streaming forwarder spec: [docs/specs/streaming-forwarder.md](/home/codex/HUAKAI/docs/specs/streaming-forwarder.md:45)。
681. HCSF envelope: [envelope.go](/home/codex/HUAKAI/backend/internal/proto/envelope.go:5)。
682. ClientAdapter interface: [proto.go](/home/codex/HUAKAI/backend/internal/proto/proto.go:23)。
683. Anthropic upstream adapter: [anthropic_sse.go](/home/codex/HUAKAI/backend/internal/proto/anthropic_sse.go:41)。
684. OpenAI upstream adapter: [openai_sse.go](/home/codex/HUAKAI/backend/internal/proto/openai_sse.go:15)。
685. Gemini upstream adapter: [gemini_sse.go](/home/codex/HUAKAI/backend/internal/proto/gemini_sse.go:16)。
686. Forwarder client adapter hook: [forwarder.go](/home/codex/HUAKAI/backend/internal/gateway/forwarder.go:41)。
687. Gateway HTTP handler current body parse: [chat_completions_handler.go](/home/codex/HUAKAI/backend/internal/gatewayhttp/chat_completions_handler.go:59)。
688. OpenAI official OpenAPI fetched via OpenAI Developer Docs MCP on 2026-05-12: `https://api.openai.com/v1/chat/completions`。
689. OpenAI official OpenAPI fetched via OpenAI Developer Docs MCP on 2026-05-12: `https://api.openai.com/v1/responses`。
690. Anthropic official streaming docs read on 2026-05-12: `https://docs.anthropic.com/en/docs/build-with-claude/streaming`。
691. Anthropic official create message docs read on 2026-05-12: `https://platform.claude.com/docs/en/api/messages/create`。

## Codex lane 起草时间 UTC

692. 2026-05-12T10:02:18Z

## agent ID

693. Codex-GPT5-20260512-P2-ClientAdapter
