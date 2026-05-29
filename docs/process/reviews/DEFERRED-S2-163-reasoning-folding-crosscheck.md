# DEFERRED — reasoning-aware token 交叉校验（逐 provider folding 建模）

**来源**: S2-163-fu sub-part 2（流式 token 交叉校验）#8 review R2 [P2]，归类 S1，已在本切片以
**「folding 不可知即跳过」**（Option A）消除误报。本文档记录被**有意延后**的完整解法。
**日期**: 2026-05-29  **基线**: fix/hermes（sub-part 2 落地后）

## 已解决（本切片，Option A）
当某流既流出 reasoning 文本（`EstimatedReasoningTokens>0`）又**不**单列 `ReasoningTokens` 时，
`crossCheckAudit` 直接跳过交叉校验（保持 `confidence=1.0`、不 `pending`）。这消除了主路径
Claude / Gemini thinking 流的误报，代价是**在这类流上不做 token 交叉校验**（少覆盖、不误报）。

## 未解决（本文档延后项）
**根因**：reported `OutputTokens` 是否已包含 thinking token 因 provider 而异，而 canonical 层
没有携带这个「folding」信号：

| Provider | thinking 流出形式 | 单列 ReasoningTokens | thinking 是否计入 reported OutputTokens | 证据 |
|---|---|---|---|---|
| Anthropic 扩展思考 | `Delta.ReasoningText` | 否 | **是**（计入 output_tokens） | `backend/internal/proto/anthropic/sse.go:488-489`、`:502-533`（mergeUsage 不写 ReasoningTokens）、`:95-106`（usage 无 reasoning 字段） |
| Gemini thought | `Delta.ReasoningText`（流式）/ `ReasoningSummary`（buffered） | 否（连 thoughtsTokenCount 都不解析） | **否**（不计入 candidatesTokenCount，thoughts 独立） | `backend/internal/proto/gemini/sse.go:252-258`、`:93-98`（usageMetadata 无 thoughtsTokenCount）、`:346-357` |
| OpenAI o1/o3 | 不流文本（隐藏） | **是** | 是（已折入 completion_tokens） | `backend/internal/proto/openai/sse.go:466`、`:106-109` |

因为 Anthropic「计入且不单列」与 Gemini「不计入且不单列」两者都满足 `ReasoningTokens==0`，
仅凭 `ReasoningTokens==0` 无法区分该把 reasoning 估算**加回**（Anthropic 需要）还是**排除**
（Gemini 需要）。任一固定规则都会修好一个 provider、弄坏另一个 → 故本切片选择「不可知即跳过」。

## 延后解法候选（择一，做时与 codex parallel-draft + reference 对照 #15）
1. **canonical 携带 folding 信号**（架构升级）：在 `CanonicalUsage` 加一个布尔/枚举标记
   「reasoning 是否已计入 OutputTokens」，由各 provider adapter 按其 usage 契约填写。
   crossCheckAudit 据此决定：folded → 估算需含 reasoning（加回 `EstimatedReasoningTokens`）；
   not-folded → 估算排除 reasoning（现状）。**注意 frozen proto**：加既有 struct 字段允许。
2. **统一 ReasoningTokens 解析**（生态升级）：为 Gemini 解析 `thoughtsTokenCount` → 写
   `CanonicalUsage.ReasoningTokens`（`geminiUsageMetadata` 需加字段，gemini 子包非 frozen）。
   Anthropic 无单列 count，仍需 (1) 的 folding 信号或用 `EstimatedReasoningTokens` 充当扣除量。
   做法须核实 Gemini usage 契约（`candidatesTokenCount` 是否含 thoughts），以 `<repo>@<sha>:file:line` 取证。
3. **始终对「总输出」比对**（算法升级）：把两侧都归一到「含 reasoning 的总输出」——
   estimated = `EstimatedOutputTokens + EstimatedReasoningTokens`，reported = `OutputTokens +
   (ReasoningTokens if 未折入 else 0)`。同样依赖 folding 信号判断 reported 侧是否加 ReasoningTokens，
   故仍需 (1)。

## 验收（做这切片时）
- 三类 provider（Anthropic 计入 / Gemini 不计入 / OpenAI 隐藏）各一判别测试，self-proving：
  正确路径与「用错 folding 假设」的路径断言相异；mutation（翻转 folding 标记）→ RED。
- 仍是**审计-only**：不改 cost / usage_source / 不迁移。
- 移除本切片的「folding 不可知即跳过」短路（被精确建模取代），并验证原跳过场景现在能正确判定。

## 风险/优先级
- 当前状态**安全**（少覆盖、零误报），非紧急。优先级低于 money-path（sub-part 3）与 piece B test-infra。
- 触及 frozen proto（加字段，允许）+ 多 provider adapter；需 reference 对照（≥2 项目对 reasoning/thinking
  token 的 usage 建模）方可向 Owner surface 决策（#15）。
