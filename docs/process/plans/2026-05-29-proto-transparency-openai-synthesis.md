# PROTO 透明性 Slice 1 (OpenAI) — 平行计划交叉综合 (2026-05-29)

> CLAUDE.md #10 交叉综合。Claude 稿 + Codex 稿独立成文后交叉。实现由 Claude 直接写代码。

## 交叉结果

**一致点(两稿相同, 直接采纳)**:
- ADP-1 reasoning_content → `reasoning_delta` 复用 text block index(镜像 gemini sse.go:251-258), **不**计入 AccumulatedContent。
- ADP-4 refusal → `text_delta` + 累加进 AccumulatedContent(对齐非流式 :615), 不碰 hcsf.go。
- 不新增 Feature 枚举、不碰 capability_matrix.go / hcsf.go / protocol_selector.go。
- 只 `reasoning_content`, 不做 `reasoning` alias(避免扩测试面)。

**Codex 纠正了 Claude 稿一处事实错误(已由 Claude 亲自核码确认)**:
- Claude 稿称 top-level metadata(system_fingerprint/service_tier/prompt_filter_results)"静默吞" —— **错**。
- 真相: `UnmarshalWithExtras`(passthrough.go:63) 只抓 **top-level** unknown 字段 → 这些已被 `attachPassthrough`(sse.go:228) 挂到每条事件的 Passthrough, 且有现成测试 `TestOpenAI_StreamingChunk_PassthroughCarriesUnknownFields` 覆盖。**非静默丢, 是已保留但无 ProtocolLossEntry 账**。
- 真正没解析的只有 nested `choices[].logprobs`(typed openAIStreamChoice 无此字段, top-level extras helper 不递归进 choices)。

**Codex 新增(好抓, 采纳)**:
- raw finish_reason `refusal`(真实 OpenAI 值)当前落 `CanonicalStopUnknown` + 触发 unknown-reason loss。应映射到 `CanonicalStopRefusal`(与 content_filter 同属拒答终止)。参考: LiteLLM/New-API reasonmap 行为对照(见 codex 稿 §3.4 cite)。

## 最终范围决定(收紧 — 小切片闭合)

**Slice 1 = ADP-1 reasoning + ADP-4 refusal**(两个无歧义真缺陷, 两稿全一致 + 已核码):
1. `openAIStreamDelta` 加 `ReasoningContent *string json:"reasoning_content,omitempty"`。
2. 流式循环 choice 内顺序: reasoning_delta(复用 text block, 不累正文) → content text_delta → refusal text_delta(累加正文) → tool calls → finish。
3. refusal 非空: emit text_delta + 累加 + DeliveredChunkCount++; 不单独记 loss(text 已 emit + stop reason 携带语义 = 非丢失, 采 codex 判断)。
4. `mapOpenAIStopReason` 加 `case "refusal": return CanonicalStopRefusal`。

**延后到"跨 vendor metadata 透明性切片"**: ADP-5 metadata-loss 账。理由:
- premise 部分错(top-level 已 passthrough 保留, 非丢);
- 真开放问题"已 passthrough 的字段要不要记 LOSSY 账 + 用什么 Verdict"宜与 anthropic/gemini metadata 一起统一设计(呼应 codex 稿 §5.4);
- nested logprobs 账 + top-level projection-gap 账放一起做更内聚, 避免 OpenAI 单点先行造成跨 vendor 不一致。

## Slice 1 内的已决默认(两稿一致, 无需 Owner gate, 已按 #10 交叉)

- reasoning 块管理: 复用 text block index(非新块) — gemini 既有范式。
- reasoning 不计正文 — 防污染答案/计费正文。
- refusal: text_delta + 累加 + 无单独 loss。
- finish_reason `refusal` → CanonicalStopRefusal。
- 不做 `reasoning` alias。

## 判别测试(Slice 1)

1. ReasoningContentEmitsReasoningDelta: delta reasoning_content="思考X" → 产出 reasoning_delta ReasoningText=="思考X"。mutation 删处理→无事件→红。
2. ReasoningDoesNotAccumulateAnswerText: reasoning+content 混合 → AccumulatedContent 只含 content。mutation 累加 reasoning→正文变长→红。
3. RefusalEmitsTextDeltaAndAccumulates: delta refusal="我不能" → text_delta=="我不能" 且累加。mutation 不读 Refusal(现状)→无事件→红。
4. RefusalFinishReasonMapsToCanonicalRefusal: finish_reason="refusal" → stop reason==CanonicalStopRefusal 且无 unknown-reason loss。mutation 不加 case→StopUnknown+loss→红。
5. PureContentRegressionUnchanged: 纯 content 帧 → 事件序列/usage/AccumulatedContent/losses 与改前一致。mutation reasoning/refusal 误触发→多事件→红。

Lane: synthesis (Claude reviewer of both plans + 亲自核码)｜Agent: Claude Opus 4.8｜UTC: 2026-05-29
