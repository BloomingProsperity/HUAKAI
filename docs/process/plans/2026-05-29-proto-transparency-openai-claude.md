# PROTO 透明性 Slice 1 (OpenAI 流式) 实施计划 — Claude 独立稿 (2026-05-29)

> CLAUDE.md #10 平行计划法 Claude 一侧, 独立成文未参考 codex 稿。分支 work/quota-subsystem。
> 实现由 Claude 直接写代码(Owner 2026-05-29 指令); planning 走交叉并行。

## 0. 背景与已坐实缺陷(我亲自核过代码)

HUAKAI 核心差异化 = 信任链"链路损失必须可见"(F-PROTO-002 / F-TRUST)。OpenAI 流式适配器 `backend/internal/proto/openai/sse.go` 有 3 个透明性缺陷, 均已 grep+读码确认:

1. **ADP-1 reasoning 丢失**: `openAIStreamDelta`(:78-83) 无 `reasoning_content` 字段 → DeepSeek-R1 / o1-via-chat-compat 经 openai.Adapter(6 vendor 复用)路由时思维链对客户端完全丢失, 且无 loss entry。对比 gemini 子包 sse.go:252-257 `part.Thought → reasoning_delta`、anthropic :487 已实现。
2. **ADP-4 refusal 不 emit**: `openAIStreamDelta.Refusal`(:82) 已解析, 但流式循环 `openAIChunkToCanonicalEvents`(:254-289) 只读 Content/ToolCalls/FinishReason, **从不读 Refusal** → 流式拒答内容丢失; 非流式 `openAIResponseText`(:615-616) 正确拼接 = 流/非流不对称。
3. **ADP-5 metadata 静默吞**: chunk 结构(:64-70)只解析 ID/Object/Model/Choices/Usage; `system_fingerprint`/`service_tier`/`logprobs`/`prompt_filter_results` 在 JSON 反序列化阶段被丢, **无 loss entry**。:218 注释自认这点。

canonical 槽位已存在(实现不碰 hcsf.go = 新机红线): `CanonicalContentDelta.ReasoningText`(hcsf.go:85)、`reasoning_delta` delta type(gemini 在用)、`CanonicalStopRefusal`(hcsf.go:137)、`ProtocolLossEntry`/`proto.NewLossEntry`(capability_matrix.go:168)。

## 1. 范围(小切片闭合 — 只 OpenAI 一对文件)

- **In**: 只改 `backend/internal/proto/openai/sse.go` + `backend/internal/proto/openai/sse_test.go`(判别测试)。
- **Out(后续切片)**: Slice 2 = anthropic thinking 流式块误标 unknown(:282) + anthropic metadata; Slice 3 = gemini metadata loss。
- **不碰**: hcsf.go(新机 CanonicalUsage)、frozen `internal/gateway`/`gatewayhttp`、`protocol_selector.go`(适配器已注册无需新增)、migration、cmd/gateway wiring。

## 2. 设计(镜像 gemini/anthropic 既有范式)

### ADP-1 reasoning
- 给 `openAIStreamDelta` 加 `ReasoningContent *string json:"reasoning_content,omitempty"`(DeepSeek/标准 OpenAI-compat 字段名)。
- 循环内: `if d.ReasoningContent != nil && *d.ReasoningContent != ""` → `ensureOpenAITextBlock(state)` 后 emit `content_block_delta{Index: TextBlockIndex, Delta:{Type:"reasoning_delta", ReasoningText: *d.ReasoningContent}}`。**不**累加进 `AccumulatedContent`(思维链非答案正文)。镜像 gemini sse.go:251-258。
- 顺序: reasoning 在同一 choice 内先于普通 content 处理(与上游帧顺序一致)。

### ADP-4 refusal
- 循环内: `if d.Refusal != nil && *d.Refusal != ""` → `ensureOpenAITextBlock(state)` 后 emit `content_block_delta{text_delta, Text: *d.Refusal}`(与非流式 :615-616 把 refusal 折进 content 一致, 客户端能拿到拒答文本)+ 追加一条 `VerdictDegraded` loss entry: "OpenAI 结构化 refusal 以 text 内容呈现(canonical 无专用 refusal 块)"。这样既不丢内容、又留可见损失记录。

### ADP-5 metadata loss
- chunk 结构加: `SystemFingerprint string json:"system_fingerprint,omitempty"`、`ServiceTier string json:"service_tier,omitempty"`、top-level `PromptFilterResults json.RawMessage json:"prompt_filter_results,omitempty"`(Azure); choice 加 `Logprobs json.RawMessage json:"logprobs,omitempty"`。
- 仅当字段非空 emit loss entry(每类一条, 去重: 用 state 标记每种只 emit 一次, 避免每帧刷屏)。Verdict=Lossy(metadata 确实没进 canonical)。
- **Feature 枚举决策(开放, 见 §6-D1)**: 默认**复用 `FeatureTextStreaming`** + 描述性 note(如 "OpenAI system_fingerprint dropped"), 以保持切片纯在 openai 子包、零碰 frozen capability_matrix.go。备选 D1b 是给 capability_matrix.go 加新枚举(更语义化但碰 frozen-root 共享文件)。

## 3. 测试计划(mutation-discriminating #14, in-memory 无需 PG)

| 测试 | 守的缺陷 | 判别 fixture | mutation 变红 |
|---|---|---|---|
| T1 ReasoningContentEmitsReasoningDelta | reasoning 丢失 | chunk delta 带 reasoning_content="思考X" → 断言产出 reasoning_delta 事件 ReasoningText=="思考X" 且**未**进 AccumulatedContent/普通 text_delta | 删 reasoning 处理 → 无 reasoning_delta → 红 |
| T2 ReasoningNotCountedAsAnswerText | reasoning 误当正文 | reasoning_content + content 混合帧 → 断言 text block 只含 content 文本, reasoning 单独走 reasoning_delta | 把 reasoning 累加进 AccumulatedContent → 正文多了思考 → 红 |
| T3 RefusalEmitsContentDelta | refusal 流式丢失 | delta 带 refusal="我不能" → 断言 emit text_delta Text=="我不能" + 有 Degraded loss entry | 不读 Refusal(现状)→ 无事件 → 红 |
| T4 MetadataLossRecorded | metadata 静默吞 | chunk 带 system_fingerprint="fp_x" → 断言产出含该语义的 ProtocolLossEntry | 不 emit loss(现状)→ losses 空 → 红 |
| T5 MetadataLossDedupedPerKind | 每帧刷屏 | 连续多帧带同 system_fingerprint → 断言该类 loss 只 1 条 | 每帧都 emit → >1 条 → 红 |
| T6 ExistingTextStreamingUnaffected | 回归 | 纯 content 帧 → 行为与改动前完全一致(text_delta 不变, 无多余 reasoning/refusal/loss) | reasoning/refusal 误触发 → 多事件 → 红 |

- 自证: T2 同测内对比 reasoning-mixed 与 pure-content 的 text block 内容必须不同。
- 复用 openai/sse_test.go 既有测试基建(构造 chunk → 调 events → 断言)。

## 4. blast radius / what-could-go-wrong

- 只改一个非冻结子包文件 + 测试; 适配器已在 protocol_selector 注册, 无 wiring 变更, 不影响其它 vendor。
- 风险: reasoning_delta 块管理错(该用 text block index 还是新块)→ 严格镜像 gemini(复用 TextBlockIndex)。
- 风险: refusal 与 content_filter finish_reason 双重信号 → refusal 字段(结构化拒答)与 content_filter(finish_reason)是不同机制, 各自独立处理, 不冲突。
- 风险: metadata loss 每帧刷屏 → T5 + state 去重守。
- 回归: T6 守纯 content 路径不变。

## 5. fusion-upgrade delta(三维)

- **架构**: 把"协议字段损失"统一成 ProtocolLossEntry 一等公民(已有机制), OpenAI 适配器补齐到与 gemini/anthropic 同级 — 三 vendor 损失记录对称。
- **算法**: reasoning/refusal 流-非流对称化(消除 capability-matrix Lossy 不对称); metadata 损失从"静默吞"升级为"显式可见+去重"。
- **生态**: F-TRUST"链路损失必须可见"在协议维度兑现 — 运维/用户可见上游 reasoning/refusal 是否被丢, 这是 HUAKAI 透明性卖点(HUAKAI 内部能力陈述, 不在此对参考项目做未经 source-read 的 parity 声明)。

> 注: 本稿最终被 `2026-05-29-proto-transparency-openai-synthesis.md` 收紧为只做 reasoning + refusal; metadata-loss 账(及其参考项目对照)延后到跨 vendor metadata 切片, 届时按 #12 source-read + 逐条引用。

## 6. 决策(交叉后定稿, 见 synthesis)

- **D1 metadata loss(含 Feature 枚举 + 参考项目对照)**: **延后**到跨 vendor metadata 切片。理由(synthesis): top-level metadata 已由 `UnmarshalWithExtras` passthrough 保留(非静默丢), 只 nested logprobs 真缺; "已 passthrough 字段是否记 LOSSY + 用什么 Verdict" 是开放问题, 宜与 anthropic/gemini metadata 一起设计, 届时 source-read 参考项目并逐条 #12 引用。本 Slice 1 不含 metadata-loss。
- **D2 refusal 呈现(已定)**: 折进 text_delta(对齐非流式) + 累加正文; **不单独记 loss**(text 已 emit + finish_reason 的 canonical stop reason 携带拒答语义 = 非丢失)。
- **finish_reason `refusal` 映射(已定)**: → CanonicalStopRefusal(原落 unknown 误报 loss)。

## 7. Source files read(SPECIFIER lane, 仅 HUAKAI 自有代码)

`backend/internal/proto/openai/sse.go`(:64-99,:208-296,:480-499,:603-616)、`backend/internal/proto/gemini/sse.go`(:240-261)、`backend/internal/proto/anthropic/sse.go`(:282-296,:396-405,:487)、`backend/internal/proto/hcsf.go`(:85,:137)、`backend/internal/proto/capability_matrix.go`(:168)。**仅 HUAKAI 自有代码**; 本稿不含经 source-read 的参考项目 parity 声明(metadata 切片再做并按 #12 引用)。reasoning_delta 范式对齐 HUAKAI 自有 gemini 子包实现。

Lane: specifier｜Agent: Claude Opus 4.8｜UTC: 2026-05-29
