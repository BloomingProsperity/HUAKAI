# PROTO 透明性 Slice 1 OpenAI 流式适配器实施计划 — Codex 独立稿

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none for this Codex artifact

REFERENCE PROJECTS IN SCOPE: LiteLLM / New-API

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors —
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: <relative paths>
  Lane: <specifier | reviewer>
  Agent: <model + ID>
  UTC timestamp: <ISO 8601>

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

| Owner directive | “独立起草 HUAKAI PROTO 透明性 Slice 1(OpenAI 流式适配器)的实施计划；只写计划文档，不写实现代码，不 commit/push。” |
| --- | --- |
| Scope | 只计划修改 `backend/internal/proto/openai/sse.go` 与 `backend/internal/proto/openai/sse_test.go`。不碰 `hcsf.go`、`capability_matrix.go`、`protocol_selector.go`、gateway/gatewayhttp、migration、cmd wiring。 |
| Success criteria | 计划覆盖 ADP-1 / ADP-4 / ADP-5 的修法、Feature 枚举决策、mutation-discriminating 测试、blast radius、fusion-upgrade delta、Owner 开放决策；文档落在本文件；无实现代码和无 commit/push。 |
| Time estimate | 计划评审 30-45 分钟；后续实现预计 1-2 小时，测试 30 分钟。 |
| Blast radius | OpenAI Chat Completions SSE 上游适配；仅影响 upstream-to-canonical event/loss 输出。不会改变 gateway 路由、quota、billing、auth、DB schema。 |
| Failure modes | 块索引错导致前端/下游解析乱序；reasoning 被算入正文；refusal 被吞或双计；metadata loss 每帧刷屏；误把 `logprobs:null` 当 loss；纯文本流回归。对应测试见 §7。 |
| Decision points | 是否把 raw finish reason `refusal` 也映射为 canonical refusal；metadata 顶层字段已 passthrough 时是否仍用 `LOSSY` 表示“未 canonical typed projection”；是否把 `reasoning` alias 纳入本 slice。 |
| Pre-execution checklist | 1. 读本计划与 Owner 合成稿。2. 确认只改 `sse.go`/`sse_test.go`。3. 先写红测。4. 实现最小改动。5. 跑 `cd backend && go test ./internal/proto/openai ./internal/proto`。6. stage 后按项目规则跑 per-commit Codex review，再由 Owner 决定是否 commit。 |

独立性 caveat：本稿没有主动打开 `docs/process/plans/2026-05-29-proto-transparency-openai-claude.md`。但在搜 F-PROTO/F-TRUST 本地证据时，一次 `rg` 范围过宽，终端输出误命中了该文件的少量摘要行。以下计划按我独立读取的本地源码、OpenAI 官方 schema、LiteLLM/New-API 最小 clean-room 行为证据重新成文；交叉讨论时应把本 caveat 当作过程风险记录。

Metadata truth-first：Observed regions: 18；Inferences: 6；Open questions: 3。

## 1. 当前观察

ADP-1 确认：OpenAI stream delta 当前声明了 role/content/tool/refusal，但没有接收 reasoning text 的字段；流式主循环只处理 content、tool_calls、finish_reason（`backend/internal/proto/openai/sse.go:78`、`backend/internal/proto/openai/sse.go:255`、`backend/internal/proto/openai/sse.go:266`、`backend/internal/proto/openai/sse.go:272`）。canonical 槽位已经存在：`CanonicalContentDelta.ReasoningText` 和 delta type 字符串路径可复用（`backend/internal/proto/hcsf.go:80`-`backend/internal/proto/hcsf.go:86`）。Gemini 已把 thought text 作为 `reasoning_delta` 放进现有 text block index，且不写入 accumulated answer content（`backend/internal/proto/gemini/sse.go:251`-`backend/internal/proto/gemini/sse.go:258`、`backend/internal/proto/gemini/sse.go:261`-`backend/internal/proto/gemini/sse.go:267`）。Anthropic streaming thinking delta 也已映射到同一 canonical delta type（`backend/internal/proto/anthropic/sse.go:481`-`backend/internal/proto/anthropic/sse.go:489`）。

ADP-4 确认：OpenAI stream delta 已解析 refusal 字段，但主循环没有 emit 它（`backend/internal/proto/openai/sse.go:78`-`backend/internal/proto/openai/sse.go:83`、`backend/internal/proto/openai/sse.go:254`-`backend/internal/proto/openai/sse.go:286`）。非流式 helper 会把 OpenAI content part 里的 refusal 文本拼入返回文本，因此流式/非流式不对称（`backend/internal/proto/openai/sse.go:592`-`backend/internal/proto/openai/sse.go:621`）。canonical stop reason 已有 refusal 枚举（`backend/internal/proto/hcsf.go:132`-`backend/internal/proto/hcsf.go:138`），当前 OpenAI stop mapper 只把 `content_filter` 映射到 canonical refusal（`backend/internal/proto/openai/sse.go:480`-`backend/internal/proto/openai/sse.go:490`）。

ADP-5 需要拆成两类事实，而不是照旧缺陷描述直接写实现：

| 字段类 | 当前事实 | 计划结论 |
| --- | --- | --- |
| 顶层 provider metadata：`system_fingerprint` / `service_tier` / `prompt_filter_results` | `UnmarshalWithExtras` 已捕获顶层 unknown 并 attach 到每条 emitted event（`backend/internal/proto/openai/sse.go:217`-`backend/internal/proto/openai/sse.go:228`）；现有 passthrough 测试覆盖 fingerprint/tier、多 event copy、nested filter results（`backend/internal/proto/openai/passthrough_test.go:17`-`backend/internal/proto/openai/passthrough_test.go:80`、`backend/internal/proto/openai/passthrough_test.go:102`-`backend/internal/proto/openai/passthrough_test.go:129`）。 | 不是 raw byte 静默吞；真正缺口是“未进入 canonical typed projection，也没有 ProtocolLossEntry 账”。Slice 1 应补字段级 loss/info 记录，并保持 passthrough 不破。 |
| choice 内 metadata：`logprobs` | 当前 `openAIStreamChoice` 没有该字段，顶层 extras helper 不抓 nested choice 字段（`backend/internal/proto/passthrough.go:48`-`backend/internal/proto/passthrough.go:56`、`backend/internal/proto/openai/sse.go:72`-`backend/internal/proto/openai/sse.go:76`）。OpenAI 官方 Chat streaming example 显示 stream choice 可带 logprobs，顶层可带 system fingerprint（OpenAI OpenAPI `/v1/chat/completions`, retrieved 2026-05-29）。 | 需要在 stream choice 上解析 presence，但不要把 raw logprob/token payload 塞进 loss，避免把 generated token metadata 扩散到 audit 文本。 |

Field matrix 目前把 refusal 与上述 metadata 标为 preserved（`backend/internal/proto/field_matrix.go:130`-`backend/internal/proto/field_matrix.go:152`）。Slice 1 不改 field matrix，但实现后应让真实行为与矩阵更接近：refusal 真的 emit；metadata raw 继续 passthrough，同时新增显式 loss 账说明 canonical projection 缺口。

## 2. ADP-1 修法：reasoning_content

推荐修法：

1. 在 `openAIStreamDelta` 增加 OpenAI-compatible reasoning content 接收槽，最小只收 `reasoning_content`。不要在本 slice 同时支持 `reasoning` alias，除非 Owner 明确批准；alias 会扩大 provider-specific 兼容范围，测试矩阵也要增加。
2. 在 `openAIChunkToCanonicalEvents` 的每个 choice 内，优先处理 reasoning，再处理 content，再处理 tool calls，再处理 finish reason。若同一 chunk 同时有 reasoning 与 visible content，event 顺序为 reasoning_delta 在前、text_delta 在后，便于下游保持“思考轨迹先于最终答案片段”的可观察顺序。
3. 块管理复用现有 text block index：调用 `ensureOpenAITextBlock(state)`，然后 emit `content_block_delta`，`Delta.Type = "reasoning_delta"`，`Delta.ReasoningText = <reasoning text>`。理由是 Gemini 已这样处理 thought part，且 HCSF 当前没有 reasoning 专用 block type（`backend/internal/proto/gemini/sse.go:251`-`backend/internal/proto/gemini/sse.go:258`、`backend/internal/proto/hcsf.go:80`-`backend/internal/proto/hcsf.go:86`）。
4. reasoning 不计入 `state.AccumulatedContent`，也不作为 answer body。保持与 Gemini 行为一致：Gemini thought path return 前没有追加 accumulated content，普通 text path 才追加（`backend/internal/proto/gemini/sse.go:251`-`backend/internal/proto/gemini/sse.go:267`）。
5. 不新增 loss。原因：本 slice 的目标是把 upstream reasoning 可见地投影到 canonical delta；只要成功 emit，就不是 loss。若未来 client 协议不支持把 reasoning 再投回某客户端格式，应由 client adapter 单独记 loss。

边界：如果 reasoning 出现在纯 tool-call 流之前，会开启 text block，后续 tool block 拿新的 block index；finish 时 `appendOpenAIBlockStops` 会先关 text block 再关 tool blocks。测试必须覆盖 index 不与正文错位。

## 3. ADP-4 修法：streaming refusal

推荐修法：

1. 对非空 `choice.Delta.Refusal`，复用 text block 并 emit `content_block_delta`，`Delta.Type = "text_delta"`，`Delta.Text = <refusal text>`。
2. 把 refusal 文本追加到 `state.AccumulatedContent`，并增加 delivered chunk count。理由：非流式 `openAIResponseText` 已把 refusal 内容拼进 text；流式按 text_delta 对齐后，下游客户端能在同一答案正文通道看到拒答文本（`backend/internal/proto/openai/sse.go:592`-`backend/internal/proto/openai/sse.go:621`）。
3. 不新增 refusal 专用 delta type。本 slice 不改 `hcsf.go`，canonical 也没有 refusal text delta。用 text_delta 是 safe equivalent；语义 refusal 由 finish reason 的 canonical stop reason 表示。
4. stop 映射维持 `content_filter -> CanonicalStopRefusal`。建议同时把 raw finish reason `refusal` 也映射到 `CanonicalStopRefusal`，因为参考项目行为区读到有把 refusal 作为终止原因对待的路径（LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/responses/litellm_completion_transformation/transformation.py:1534、LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/responses/litellm_completion_transformation/transformation.py:1547、LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/responses/litellm_completion_transformation/transformation.py:1549；New-API@20d3e73734527cded251aff23202dfbf5a2584ca:relay/reasonmap/reasonmap.go:19、New-API@20d3e73734527cded251aff23202dfbf5a2584ca:relay/reasonmap/reasonmap.go:35）。这是行为对照，不复制实现。
5. 不新增 loss。只要 refusal 文本通过 text_delta 出去，stop reason 映射为 canonical refusal，就不是 silent drop。若同一 stream 同时出现 refusal delta 与 `content_filter` finish，输出是“文本可见 + stop_reason=refusal”，不是双重错误信号。

风险：如果未来 HCSF 增加 refusal 专用 content part，本 slice 的 text_delta 会成为历史兼容路径。当前不应为了类型纯度触碰 `hcsf.go`。

## 4. ADP-5 修法：metadata 解析、loss 记录、去重

### 4.1 字段处理策略

| 字段 | 解析方式 | 输出方式 | loss 方式 |
| --- | --- | --- | --- |
| `system_fingerprint` | 继续从 top-level passthrough envelope 识别 presence。 | raw 仍附在 emitted events 的 `Passthrough`，保持现有测试。 | 每个 stream 最多 1 条 field-level entry，说明 canonical typed event 没有 dedicated slot，raw preserved via passthrough。 |
| `service_tier` | 继续从 top-level passthrough envelope 识别 presence。 | raw 仍附在 emitted events 的 `Passthrough`，保持现有测试。 | 每个 stream 最多 1 条 field-level entry，说明 canonical typed event 没有 dedicated slot，raw preserved via passthrough。 |
| `prompt_filter_results` | 继续从 top-level passthrough envelope 识别 presence；只判断 presence，不解析内部政策结构。 | raw 仍附在 emitted events 的 `Passthrough`，保持现有测试。 | 每个 stream 最多 1 条 field-level entry；不要把嵌套过滤结果复制进 note/reason。 |
| `logprobs` | 在 stream choice 上新增 raw presence 字段；`null` 不算 loss，非 null object/array 算 semantic projection loss。 | 本 slice 不把 raw logprob payload 放到 canonical event；避免把 token-level metadata 写入 audit 文本。 | 每个 stream 最多 1 条 field-level warning，说明 nested choice metadata cannot be represented in current canonical event. |

### 4.2 Loss entry 形态

推荐不新增 Feature 枚举，复用 `FeatureTextStreaming`，并用 v0.4 字段补语义：

| 决策 | 推荐 | 理由 |
| --- | --- | --- |
| Feature enum | 复用 `FeatureTextStreaming` | metadata 随 streaming chunk 到达；缺口是 OpenAI stream -> canonical stream projection，不是请求 capability。现有 enum 无 metadata 专用项（`backend/internal/proto/capability_matrix.go:33`-`backend/internal/proto/capability_matrix.go:48`）。 |
| 是否新增 `FeatureProviderMetadata` | 不在 Slice 1 新增 | 会触碰 `capability_matrix.go` 与 `allFeatures`，属于跨适配器矩阵决策；用户硬约束也只允许计划后续实现改 `openai/sse.go` + `openai/sse_test.go`。 |
| Verdict | `LOSSY` | 表达“canonical typed projection 不完整”。顶层 metadata 的 raw bytes 已 passthrough，因此 note/reason 必须写清“raw preserved via passthrough, semantic canonical slot absent”，避免误导为 raw 丢失。 |
| Severity | warning | 不是请求失败，但会影响可观测性/审计解释。ProtocolLossEntry 支持 Severity/Field/Vendor/Reason/Code 等 v0.4 字段（`backend/internal/proto/protocol_loss.go:16`-`backend/internal/proto/protocol_loss.go:24`、`backend/internal/proto/protocol_loss.go:28`-`backend/internal/proto/protocol_loss.go:70`）。 |
| Note/Reason 内容 | 只写字段名与 projection 缺口，不写 raw 值 | 避免把 `logprobs` token payload、policy filter payload、fingerprint value 写进审计文本。 |

实现时可先调用 `proto.NewLossEntry(proto.FeatureTextStreaming, proto.DirectionUpstreamToCanonical, proto.VerdictLossy, "...")`，再补 `Field` / `Vendor` / `Severity` / `Reason` / `Code`。这仍不需要改 `capability_matrix.go`。

### 4.3 去重

在 `UpstreamState` 里增加一个小的 per-stream seen map，例如按 field path 归一化：`system_fingerprint`、`service_tier`、`prompt_filter_results`、`choices[].logprobs`。`ensureOpenAIState` 初始化。每次 chunk 解析后，先扫描 top-level envelope 与每个 choice metadata；字段第一次出现且值非 null/非空时 append loss，之后同字段不再 append。这样两帧都带 fingerprint 或 logprobs 只产生一条解释，不会每帧刷屏。

metadata-only chunk 的边界：即使 chunk 没有 choices、没有 emitted event，只要出现本 slice 关注的 metadata，仍应返回 loss entry；这样“无 canonical event 可挂 passthrough”的场景也不会静默。

## 5. Feature 枚举决策

推荐结论：Slice 1 不新增 enum；metadata semantic loss 使用 `FeatureTextStreaming` + `Field` / `Code` / `Reason` 区分。

原因：

1. 用户硬边界只允许计划后续实现改 `backend/internal/proto/openai/sse.go` 与 `backend/internal/proto/openai/sse_test.go`；新增 enum 需要改 `backend/internal/proto/capability_matrix.go`，还可能连带 `allFeatures` 与矩阵测试。
2. 现有 Feature 枚举是 capability-level，不是 field-level；metadata 属于 field projection。项目已经有 `ProtocolLossEntry.Field` 作为字段级表达空间（`backend/internal/proto/protocol_loss.go:28`-`backend/internal/proto/protocol_loss.go:38`）。
3. 顶层 metadata 当前 raw passthrough 已存在；新增 enum 会把“小范围透明性补账”升级为跨 provider capability taxonomy，容易扩大 slice。
4. 如果后续 Slice 2/3 发现 Anthropic/Gemini metadata 都需要同类账，再在 synthesis slice 统一设计 `FeatureProviderMetadata` 或 field-matrix driven loss，不在 OpenAI 单点先行。

## 6. 具体执行顺序

1. 只在 `backend/internal/proto/openai/sse_test.go` 增加/调整测试，先跑 OpenAI package 单测确认红。
2. 在 `backend/internal/proto/openai/sse.go` 扩展 stream delta 接收 reasoning content。
3. 在 choice 主循环中添加 reasoning_delta emit，复用 text block，不写 accumulated content。
4. 在 choice 主循环中添加 refusal text_delta emit，并维持/补充 canonical refusal stop 映射。
5. 在 stream choice 上解析 `logprobs` presence。
6. 在 `UpstreamState` 增加 metadata loss seen 状态，添加 helper 产生字段级 loss。
7. 在 `providerDataToCanonicalEvents` 中，`openAIChunkToCanonicalEvents` 返回后或内部 choice 循环中合并 metadata losses；保留 `attachPassthrough(events, env)` 的现有行为。
8. 跑 `cd backend && go test ./internal/proto/openai`。
9. 跑 `cd backend && go test ./internal/proto`，确认 shared proto tests 不因 ProtocolLossEntry 字段或 stop mapping 回归。
10. 不 commit/push；若进入实现提交，按项目规则 stage intended diff 后跑 per-commit review。

## 7. 测试计划

所有测试 in-memory，无 PG、无 gateway wiring。每个测试都必须能说明 mutation 怎么变红。

| 测试 | 守的缺陷 | 判别 fixture 具体值 | 断言 | Mutation 会如何变红 |
| --- | --- | --- | --- | --- |
| `TestOpenAIAdapterReasoningContentEmitsReasoningDelta` | ADP-1 reasoning 被吞 | chunk1: model `deepseek-r1`, delta reasoning text `check hidden sum: 2+2=4`; chunk2 content `Final: 4`; finish `stop` | 至少一条 `content_block_delta` 的 delta type 为 `reasoning_delta`，ReasoningText 精确等于 `check hidden sum: 2+2=4` | 不解析字段 -> 没有 reasoning_delta；误发 text_delta -> type 断言失败；字段名错 -> ReasoningText 空 |
| `TestOpenAIAdapterReasoningContentDoesNotAccumulateAnswerText` | reasoning 污染答案正文/计费正文 | chunk1: model `deepseek-r1`, delta reasoning text `check hidden sum: 2+2=4`; chunk2 content `Final: 4`; finish `stop` | `state.AccumulatedContent == "Final: 4"`，且普通 text_delta 只有 `Final: 4` | 把 reasoning append 到 content -> accumulated content 变长；把 reasoning 当 text_delta -> 普通 text delta 数/值失败 |
| `TestOpenAIAdapterRefusalDeltaEmitsTextAndStopRefusal` | ADP-4 streaming refusal 被吞 | chunk1 delta refusal `I cannot assist with credential theft.`；finish `content_filter` | 输出 text_delta 精确等于 refusal；`state.AccumulatedContent` 等于 refusal；message_delta stop reason 为 `CanonicalStopRefusal` | 现状不 emit -> text_delta 缺失；不累计 -> state 失败；stop mapping 改坏 -> stop reason 失败 |
| `TestOpenAIAdapterRawRefusalFinishReasonMapsToCanonicalRefusal` | refusal/content_filter 双信号边界 | content/refusal 任一非空，finish reason `refusal` | stop reason 为 `CanonicalStopRefusal`，且没有 unknown finish_reason loss | 不加 `refusal` mapping -> StopUnknown 或 unknown loss，红。若 Owner 不批准该行为，此测试从 Slice 1 移到 open question。 |
| `TestOpenAIAdapterMetadataLossesAreRecordedOncePerField` | ADP-5 metadata 无显式账 + 去重 | 两个 chunks 都带 top-level `system_fingerprint:"fp_loss_a/b"`, `service_tier:"priority"`, `prompt_filter_results:[{...}]`；两个 choices 都带 non-null `logprobs` object with token `"a"`/`"b"` | losses 中字段集合精确为 `system_fingerprint`, `service_tier`, `prompt_filter_results`, `choices[].logprobs`；每字段 1 条；Feature 为 `text_streaming`；Direction upstream_to_canonical；Verdict LOSSY；Reason/Note 不含 `fp_loss_` 和 token payload | 不 emit metadata loss -> 空；不 parse nested logprobs -> 缺字段；不去重 -> 每字段多条；泄露 raw 值 -> redaction 断言失败 |
| `TestOpenAIAdapterNullLogprobsDoesNotEmitMetadataLoss` | 避免 false positive | 普通 content chunk 带 `logprobs:null`，content `plain`，finish `stop` | losses 为空，event sequence 与纯文本一致 | naive presence check 把 null 当 loss -> losses 非空 |
| 现有 `TestOpenAIAdapterHappyPathGoldenSSE` 保持 | 纯 content 回归不变 | 现有 `hi` + ` there` + usage fixture | event type sequence、usage、AccumulatedContent、losses=0 不变 | 新逻辑扰动普通流 -> 现有测试红 |
| 可选 `TestOpenAIAdapterMetadataOnlyChunkReturnsLossWithoutEvent` | metadata-only chunk 不静默 | choices 缺失，top-level `system_fingerprint:"fp_meta_only"` | events 为空但 losses 有 field-level entry | 只在 emitted events 上附 metadata -> loss 空 |

测试命令：

- `cd backend && go test ./internal/proto/openai -run 'TestOpenAIAdapter(Reasoning|Refusal|Metadata|NullLogprobs|HappyPath)'`
- `cd backend && go test ./internal/proto/openai`
- `cd backend && go test ./internal/proto`

质量要求：不要写 `!= bad` 式断言；每个新增测试都断言 expected event type、field value、loss count、loss field。`logprobs` fixture 的 token 值故意设置为 `"a"`/`"b"`，并断言 loss 文本不含它，防止测试只证明“有 loss”却不守隐私边界。

## 8. Blast Radius 与边界风险

| 风险 | 说明 | 缓解 |
| --- | --- | --- |
| 块管理错 | reasoning 复用 text block；若实现错误可能重复 start 或 stop 错 index。 | 用 reasoning-before-content fixture 断言 start/delta/stop 顺序；复用 Gemini pattern。 |
| reasoning 污染正文 | 如果把 reasoning append 到 `AccumulatedContent`，用户答案、billing provisional count、下游 text projection 都可能错。 | 专门测 accumulated content 和普通 text_delta。 |
| refusal 双信号 | refusal text 与 `content_filter`/`refusal` finish reason 同时出现时，可能被当两次错误。 | 计划定义为 text visible + stop_reason refusal；不额外 loss。 |
| metadata 刷屏 | streaming 每帧都有 fingerprint/tier/logprobs。 | per-stream field dedupe。 |
| metadata raw 泄露 | logprobs/prompt_filter_results 可能含 token 或 policy details。 | loss entry 只写 field path，不写 raw value；top-level raw 仍仅在 Passthrough 结构中按现有路径携带。 |
| 当前 passthrough 测试被破坏 | 若把 top-level metadata 改为 typed struct 字段，`UnmarshalWithExtras` 不再把它们放入 `Passthrough.Extra`。 | 不把 top-level metadata 加到 `openAIChatCompletionChunk`；继续从 env.Extra 识别。 |
| Feature enum 扩大 | 新 enum 会拉动矩阵和 shared tests。 | Slice 1 复用 `FeatureTextStreaming`，未来统一抽象再加 enum。 |

## 9. Fusion-upgrade Delta

| 维度 | 参考项目行为级观察 | HUAKAI Slice 1 升级 |
| --- | --- | --- |
| 架构 | LiteLLM scoped read 显示其 streaming aggregation 会把 provider backend marker 放入 normalized response 对象（LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/litellm_core_utils/streaming_chunk_builder_utils.py:120；LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/litellm_core_utils/streaming_chunk_builder_utils.py:142），也会把 choice-level probability metadata 带到 streaming choice 对象（LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/litellm_core_utils/llm_response_utils/convert_dict_to_response.py:171；LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/litellm_core_utils/llm_response_utils/convert_dict_to_response.py:173）。New-API scoped read 显示其 OpenAI-compatible stream DTO 有 reasoning/probability/backend marker slots（New-API@20d3e73734527cded251aff23202dfbf5a2584ca:dto/openai_response.go:81；New-API@20d3e73734527cded251aff23202dfbf5a2584ca:dto/openai_response.go:88；New-API@20d3e73734527cded251aff23202dfbf5a2584ca:dto/openai_response.go:147）。 | HUAKAI 不只透传或落 DTO，而是把“canonical projection 没有槽位”写成 ProtocolLossEntry，可被 F-TRUST/运营审计消费。 |
| 算法 | LiteLLM scoped read 显示 chat-style reasoning delta 可转成 Responses-style reasoning summary delta（LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/responses/litellm_completion_transformation/streaming_iterator.py:1088；LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/responses/litellm_completion_transformation/streaming_iterator.py:1095）。New-API playground scoped read 显示 UI 把 stream reasoning 与 visible content 分通道更新（New-API@20d3e73734527cded251aff23202dfbf5a2584ca:web/default/src/features/playground/hooks/use-stream-request.ts:69；New-API@20d3e73734527cded251aff23202dfbf5a2584ca:web/default/src/features/playground/hooks/use-stream-request.ts:73；New-API@20d3e73734527cded251aff23202dfbf5a2584ca:web/default/src/features/playground/hooks/use-stream-request.ts:76）。 | HUAKAI 在 provider adapter 层直接投影为 HCSF `reasoning_delta`，并明确不计入 answer body；这让后续任意 client adapter 都能统一消费。 |
| 生态 | New-API scoped read 显示某些 cost-sensitive request metadata 默认过滤，需开关允许（New-API@20d3e73734527cded251aff23202dfbf5a2584ca:relay/common/relay_info.go:795；New-API@20d3e73734527cded251aff23202dfbf5a2584ca:relay/common/relay_info.go:797）。参考项目 scoped read 未观察到 HUAKAI-style per-field protocol loss ledger。 | HUAKAI 把“字段被保留、降级或不可投影”变成显式账；用户/Owner 不需要猜 gateway 是否吞了 fingerprint、tier、filter result、reasoning 或 refusal。 |

注意：上表是 behavior-level 对照，不复制参考项目实现、命名体系或测试。关于“未观察到 per-field loss ledger”只覆盖本次列出的最小源码区域，不声称对全部参考项目做了穷尽搜索。

## 10. 真正需要 Owner 拍板的开放决策

1. 是否批准把 raw finish reason `refusal` 映射为 `CanonicalStopRefusal`。Codex 推荐批准；理由是 refusal 语义与 `content_filter` 同属拒答终止，且参考项目行为对照出现了该终止类别。
2. 顶层 metadata 已经 passthrough 时，loss entry 的 Verdict 是否用 `LOSSY`。Codex 推荐用 `LOSSY`，但 Reason 写明 raw preserved via passthrough；这样表达的是“canonical typed projection loss”，不是 raw 丢失。
3. 是否把 OpenAI-compatible `reasoning` alias 纳入 Slice 1。Codex 推荐不纳入；只做用户点名的 `reasoning_content`，避免扩大测试面。若 Owner 认为 DeepSeek/o1-compatible 生态必须同时收 alias，则新增一个同类测试。

## 11. 我预计可能与另一稿分歧的点

1. 另一稿可能把 ADP-5 描述为 top-level metadata 完全静默吞；我会坚持当前源码事实：top-level unknown 已 passthrough，缺的是显式 loss 账与 nested `logprobs`。
2. 另一稿可能建议新增 metadata Feature enum；我建议 Slice 1 不碰 `capability_matrix.go`，用 `FeatureTextStreaming + Field` 完成最小闭环。
3. 另一稿可能把 refusal 做专用 canonical 信号；我建议不碰 HCSF schema，按非流式行为对齐为 text_delta + stop_reason refusal。
4. 另一稿可能给 top-level metadata 每帧记 loss；我建议 per-stream per-field 去重，避免 F-TRUST/ops 账被 streaming 高频字段刷屏。

## 12. 结论

本 slice 应是小而闭合的透明性修复：OpenAI reasoning_content 变成 HCSF reasoning_delta；OpenAI streaming refusal 变成可见 text_delta 且 stop reason 为 refusal；metadata 不扩 schema、不破 passthrough，但把 canonical projection 缺口显式写入 ProtocolLossEntry，并对高频 stream 字段去重。

中文 Owner 摘要：本计划只安排 `backend/internal/proto/openai/sse.go` 与 `backend/internal/proto/openai/sse_test.go` 的后续实现，不写代码不提交；真实观察是 OpenAI reasoning_content 缺接收槽、refusal 已解析未 emit、top-level metadata 已 passthrough 但无 loss 账、nested logprobs 仍会静默丢；合理推断是 Slice 1 应复用现有 HCSF reasoning_delta/text_delta/ProtocolLossEntry，而不是改 hcsf 或新增 Feature enum；open question 共 3 个，最高优先级是 Owner 是否批准 raw finish reason `refusal` 映射到 canonical refusal。

Source files read:
- HUAKAI: `docs/RULES.md`
- HUAKAI: `backend/internal/proto/openai/sse.go`
- HUAKAI: `backend/internal/proto/openai/sse_test.go`
- HUAKAI: `backend/internal/proto/openai/passthrough.go`
- HUAKAI: `backend/internal/proto/openai/passthrough_test.go`
- HUAKAI: `backend/internal/proto/hcsf.go`
- HUAKAI: `backend/internal/proto/capability_matrix.go`
- HUAKAI: `backend/internal/proto/protocol_loss.go`
- HUAKAI: `backend/internal/proto/passthrough.go`
- HUAKAI: `backend/internal/proto/field_matrix.go`
- HUAKAI: `backend/internal/proto/gemini/sse.go`
- HUAKAI: `backend/internal/proto/anthropic/sse.go`
- Reference: `LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/litellm_core_utils/streaming_chunk_builder_utils.py`
- Reference: `LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/litellm_core_utils/llm_response_utils/convert_dict_to_response.py`
- Reference: `LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/responses/litellm_completion_transformation/streaming_iterator.py`
- Reference: `LiteLLM@79b45786719778117debd57e38b9262283431ce2:litellm/responses/litellm_completion_transformation/transformation.py`
- Reference: `New-API@20d3e73734527cded251aff23202dfbf5a2584ca:dto/openai_response.go`
- Reference: `New-API@20d3e73734527cded251aff23202dfbf5a2584ca:web/default/src/features/playground/hooks/use-stream-request.ts`
- Reference: `New-API@20d3e73734527cded251aff23202dfbf5a2584ca:relay/common/relay_info.go`
- Reference: `New-API@20d3e73734527cded251aff23202dfbf5a2584ca:relay/reasonmap/reasonmap.go`
- Official protocol reference: OpenAI OpenAPI `/v1/chat/completions`, fetched through OpenAI developer docs MCP on 2026-05-29.

Lane: specifier
Agent: Codex GPT-5
UTC timestamp: 2026-05-29T02:24:21Z
