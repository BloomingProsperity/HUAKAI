# P-1 Capability Graph IR Payload 细化 — Claude lane plan

- 日期：2026-05-12（UTC）
- 作者：Claude PM-Orchestrator（lane = specifier）
- 范围：backend/internal/proto/capability_*.go + envelope_validate.go + fixtures/
- 前置：P-0 + P-0c 已完成（commit b7d9079 → 4d06548，10 commit ahead origin）
- 平行 lane：[2026-05-12-p1-capability-payload-refinement-codex.md](2026-05-12-p1-capability-payload-refinement-codex.md)（独立起草，CLAUDE.md #10）

## 1. 目标

P-1 不是补字段（14 capability 的字段已在 P-0 落齐），是把 **payload 内部 semantic invariants + 跨 node 引用完整性 + 条件必填规则** 从「字段注释里的口头描述」抬升到 **可执行的 INV-14..INV-32 强校验**。

完成后 HCSF v0.4 schema 进入 **schema-locked** 状态，P-2 ClientAdapter / P-3 UpstreamAdapter 可以在 stable contract 上落地，不再 churn。

## 2. 当前状态分析

### 2.1 14 capability 骨架已包含的（不动）
- 14 个 payload struct 字段齐全（capability_text/tool/thinking/cache/structured/computer/file/image/audio/video/live/batch/mcp/data_retention.go）
- 5 个共用辅助结构：DataLocator / MediaDimensions / TimeRange / NodeSourceRef / RetryPolicy
- 各类 string enum（ToolNodeStatus / ApprovalState / CacheScope / StructuredOutputMode / DataSourceKind / MediaTransport / TranscriptPolicy / LiveTransport / BatchStatus / DataRetentionLabel / RedactionClass）已声明常量
- DataRetentionLabel 已锁 5 词汇并有 AllDataRetentionLabels 表

### 2.2 当前 INV 覆盖（envelope_validate.go 491 LoC）

| INV | 守门内容 |
|---|---|
| INV-0 | env != nil |
| INV-1 | round-trip stability（test-only） |
| INV-3 | Kind ↔ 14 nullable pointer tagged-union 一致性 + projection.Capability enum |
| INV-4 | Version == HCSFVersion |
| INV-5 | RequestMeta 5 必填字段 |
| INV-6 | BufferedResponse / StreamEvents 互斥（nil vs empty 区分） |
| INV-7 | ProtocolLossEntry silent drop（graph + edge + projection 三层） |
| INV-8 | edge ID/Type required + uniqueness + From/To resolve + AllEdgeTypes 成员 |
| INV-10 | DataRetention.Value 5-vocab enum |
| INV-12 | Extensions key 前缀（vendor:/experimental:） |
| INV-13 | StreamPlan.Mode required + enum |

### 2.3 INV 空白点（P-1 待补）

骨架字段已声明但**没有任何 validator 守门**的：

| 类别 | 空白 | 风险 |
|---|---|---|
| **payload 内部 enum** | ToolNodeStatus / ApprovalState / CacheScope / StructuredOutputMode / DataSourceKind / MediaTransport / TranscriptPolicy / LiveTransport / BatchStatus / RedactionClass 全部只声明常量未守门 | adapter 写非法字符串过校验、INV-10 模式不一致 |
| **payload 必填字段** | 各 *Node 的 `必填` 字段（注释里写但 validator 不查），如 TextNode.Block.Type / ToolUseNode.Name / FileNode.MediaType 全空字符串可过 | 半构造态穿过 P-2/P-3 |
| **跨字段约束** | DataLocator.Kind 必须等于父 Node.SourceKind（file/image/video 三处）；DataRetention.Value=="zdr_verified" 必须有 EvidenceRef；Value=="request_store_false" 必须 RequestStore=false | issue-derived 安全规则未守 |
| **跨 node 引用** | ToolResult→ToolUse、Batch→file/image/audio/video、ComputerUse→image/file（ScreenshotRef）、Live→tool node、MCP→tool_use/tool_result、CacheControl.BreakpointRefs→node id 全部不查链接完整性 | issue 模式 sub2api#1552 / litellm#27468 / portkey#1579 的 silent drop 仍可发生 |
| **Policy 顶层** | Policy.Redaction / Auth / Audit.Visibility 都是 enum 但 validator 未查 | RedactionPublic 默认值之外的非法字符串可过 |
| **ProtocolLossEntry 跨字段** | v0.3↔v0.4 字段同时存在时一致性（Severity=error ↔ Verdict=UNSUPPORTED 等）；Code=unsupported_capability_native_required 需 NativePath | v0.3/v0.4 双形态 entry 漂移 |

## 3. INV-14..INV-32 扩展清单（19 条）

每条一行 spec；具体 message 形态参考 envelope_validate.go 现有 ValidationError 用法。

### Group A — capability payload 内部 enum + 必填（INV-14..INV-26）

- **INV-14** TextNode：Role ∈ {user, assistant, system, tool}；Block.Type == "text"。
- **INV-15** ToolUseNode：ToolCallID/Name 非空；Status ∈ ToolNodeStatus enum；Input 必须是合法 JSON `object` 或 `null`（json.RawMessage 解析守门）。
- **INV-16** ToolResultNode：ToolCallID 非空；Status ∈ {complete, error}；Content slice 非 nil（空数组合法）。
- **INV-17** ThinkingNode：Redaction ∈ RedactionClass enum；Blocks slice 非 nil。
- **INV-18** CacheControlNode：Scope ∈ CacheScope enum；BreakpointRefs slice 非 nil；CacheKeyHint 不得包含明文 prompt（启发式：长度 > 256 时记 warning，由 INV-7 ProtocolLoss 落地）。
- **INV-19** StructuredOutputNode：Mode ∈ StructuredOutputMode enum；Schema 必须是合法 JSON；当 Strict==true 且 Mode==json_schema 时 Schema 不得为 JSON null。
- **INV-20** ComputerUseNode：Environment/Action 非空；Approval ∈ ApprovalState enum。
- **INV-21** FileNode/ImageNode/VideoNode：DataLocator.Kind == 父 Node.SourceKind；MediaType 非空且包含 "/"；Locator.Value 非空。
- **INV-22** AudioNode：Transport ∈ MediaTransport enum；Format 非空；TranscriptPolicy 若设值须 ∈ TranscriptPolicy enum；Locator.Value 非空。Transport ↔ Locator.Kind 映射表（**不是 1:1 相等**，因为 Transport 4 vocab 与 DataSourceKind 4 vocab 不同义）：Transport=inline ↔ Locator.Kind=inline_base64；Transport=file ↔ Locator.Kind=file_id；Transport=url ↔ Locator.Kind=url；Transport=stream ↔ Locator.Kind ∈ {url, digest_ref}（stream 转写 URL 或外部 digest）。映射不匹配 → INV-22。
- **INV-23** LiveSessionNode：Transport ∈ {wss, sse}；SessionID 非空；Modalities slice 非 nil。
- **INV-24** BatchNode：Validation ∈ BatchStatus enum；JobID/Endpoint/InputRef 非空；RetryPolicy 若非 nil 须 MaxAttempts >= 0。
- **INV-25** MCPServerNode：ServerLabel 非空；AllowedOperations slice 非 nil（空数组合法）。
- **INV-26** DataRetentionNode：AuditLabel 非空；Enforcement ∈ {unknown, asserted, contract_required, verified}。

### Group B — 条件必填（INV-27）

- **INV-27** DataRetentionNode 条件必填表：
  - Value == "zdr_verified" → EvidenceRef 非空
  - Value == "request_store_false" → RequestStore != nil 且 \*RequestStore == false
  - Value == "regional_asserted" → Region 非空
  - Value == "provider_contract_required" → 由 P-2 时再决定是否要 EvidenceRef，P-1 暂不强制（口子留 audit_label 文本）

### Group C — 跨 node 引用完整性（INV-28）

- **INV-28** 引用完整性表（凡 *Ref / *IDs 字段必须在同 envelope 内 resolve；未 resolve 时降级 ProtocolLossEntry severity=warning，code=ref_unresolved，由 INV-7 守 silent-drop）：

| 字段 | 期望指向 |
|---|---|
| ToolResultNode.ToolCallID | ToolUseNode 同 envelope 同 ToolCallID（**一对一**） |
| BatchNode.InputRef / OutputRef / ErrorRef | file/image/audio/video node ID 或 file_id digest_ref URL |
| ComputerUseNode.ScreenshotRef | image/file node ID |
| LiveSessionNode.ToolNodeIDs | tool_use/computer_use/mcp_server node ID |
| MCPServerNode.InvocationNodeIDs | tool_use node ID |
| MCPServerNode.ResultNodeIDs | tool_result node ID |
| CacheControlNode.BreakpointRefs | 任意 node ID（不限 kind） |

ToolUse↔ToolResult 关联在已有 EdgeRequires 之上额外要求 ToolCallID 字符串相等（避免 edge 缺失时 ref 无法成立）。

### Group D — Policy & ProtocolLossEntry 一致性（INV-29..INV-32）

- **INV-29** Policy.Redaction ∈ RedactionClass enum；Policy.Auth ∈ AuthPolicy enum；Policy.Audit.Visibility ∈ AuditVisibility enum。
- **INV-30** NodeSourceRef：MessageIndex/BlockIndex/EventIndex 至少一个非 nil **或** RequestField 非空，否则节点必须由 capability_graph 顶层 ProtocolLoss 解释（INV-7 守门），不允许 source-less 静默节点。
- **INV-31** ProtocolLossEntry v0.3↔v0.4 一致性：当 Severity 与 Verdict 同时设值，必须满足映射表（info↔PRESERVED、warning↔LOSSY、error↔UNSUPPORTED），不一致 → INV-31。
- **INV-32** ProtocolLossEntry NativePath 必填：当 Code == "unsupported_capability_native_required" 或 Severity == error 且 Capability 设值时，NativePath 必须非空（Q3 决议 native passthrough 路径必须可寻址）。

## 4. 工作量切片（10 day，与项目不急原则匹配）

| Day | 范围 | 文件 | 新 INV | 测试 | Fixture 影响 |
|---|---|---|---|---|---|
| D1 | spec 锁定 + ValidationError 扩 Path 字段（嵌套定位）+ helper 工具函数（getNodeIDSet / kindOfNode） | envelope_validate.go | — | — | — |
| D2 | text/tool 三 INV（14/15/16）+ ToolUse↔ToolResult ref check | envelope_validate.go + envelope_test.go | 14/15/16 | +6 | 检查 tool_use/tool_result minimal fixture 是否需 Block.Type 显式 |
| D3 | thinking/cache/structured（17/18/19） | 同上 | 17/18/19 | +6 | thinking_minimal.json Redaction 字段；structured Schema 是否需 null sentinel |
| D4 | computer/file/image/video（20/21）+ MediaType 校验 | 同上 | 20/21 | +5 | 4 fixture SourceKind ↔ Locator.Kind 对齐检查 |
| D5 | audio/live/batch/mcp（22/23/24/25） | 同上 | 22/23/24/25 | +6 | audio TranscriptPolicy 字段，live Modalities slice 非 nil |
| D6 | data_retention 条件必填（26/27） | 同上 | 26/27 | +8 | data_retention_minimal.json 全 5 vocab 各跑一条 fixture 或 edge_case 加 4 个补丁 |
| D7 | 跨 node 引用完整性 INV-28（最重） | envelope_validate.go + 新增 cross_ref.go 拆分 | 28 | +10 | regression fixture sub2api_1552/litellm_27468 检查跨 ref 形态 |
| D8 | policy / source / loss 一致性（29/30/31/32） | 同上 | 29/30/31/32 | +8 | 检查 envelope text_minimal.json policy.redaction 是否齐 |
| D9 | 35 fixture pass 二次扫 + round-trip stability 重测 + go build/vet/test 全绿（含 -tags debug 双 build） | fixtures/*.json | — | — | 视 D2-D8 决定 |
| D10 | codex review 二次 pass + bug fix + commit | — | — | — | — |

预计 LoC delta：
- envelope_validate.go +400~600
- 新增 cross_ref.go (split D7) ~150
- envelope_test.go +600~800
- fixtures/*.json: ~30 patch（多数零修改，data_retention 边缘 case 增 4 条新 fixture）
- 总 ~1300 LoC，与 P-0c-A 的 +430 同量级

## 5. 风险与权衡

### 5.1 35 fixture 破坏面
新 INV 触发面广，需逐 fixture 检查：
- envelope/*minimal.json：可能缺 Role / Approval / Redaction 等必填字符串（当前 fixture 是 P-0 D1 阶段写的）
- response/*.json：buffered_anthropic_with_thinking 是否声明 Redaction 字段
- event/*.json：Mode==replay 时 streamEvents 内不引入 capability node，影响小
- regression/*.json：5 条都是 issue case，schema 形态可能与新 INV 冲突 — **D7 必须先扫 regression 再写 cross-ref INV**

**对策**：D1 起草后先跑一次 `go test ./internal/proto/ -run TestFixtures` 把每条 fixture 触发的 INV 编号列清单；如发现需大改超过 5 个 fixture，分两批切：第一批仅加 INV，第二批改 fixture，确保 commit 可二分。

### 5.2 跨 node ref 性能（INV-28）
当前 ValidateEnvelope 是 O(N·M)（N=node，M=edge）；加 ref check 后变 O(N + sum(refs))，最坏 O(N²) when 全图都引用。

**对策**：
- hot path 仍走 ValidateEnvelopeVersionGuard（不变）
- ValidateEnvelope 用于 debug build / fixture 加载 / P-2 启动时一次性 schema 锁定校验，不在 per-request 路径
- 引用解析用预构 nodeIDSet map[string]CapabilityKind，单次 build O(N)，per-ref check O(1)

### 5.3 ProtocolLossEntry v0.3/v0.4 一致性（INV-31）破坏旧 adapter
anthropic_sse.go / openai_sse.go / gemini_sse.go 仍用 `newLossEntry(Feature, Direction, Verdict, Note)` 不带 Severity。

**对策**：
- INV-31 仅在 **Severity 与 Verdict 同时非空** 时触发；旧 adapter 不填 Severity 不受影响
- 等 P-2 重写 client adapter 时一并补 Severity（届时 v0.3 兼容字段可逐步下沉）

### 5.4 D9 schema hook 与 P-1 边界
StreamPlan.MidStreamFallbackPolicy / FallbackBoundary / Recoverable 是 D9 留口子，P-1 是否补 INV？
- **不补**。P-1 仅细化 14 capability payload；mid-stream 行为在 P-3 UpstreamAdapter 落地时再处理（D9 决议）

### 5.5 Codex 验证窗口
按 CLAUDE.md #8 per-commit review，每个 D2..D8 commit 都需 `codex exec review --uncommitted --full-auto`。
**对策**：
- 把 D2/D3/D4/D5/D6 合成 1 个 commit（INV-14..INV-26 单调扩展）一次过 review
- D7 cross-ref 单独 1 commit（最复杂，独立 review）
- D8 policy/loss 单独 1 commit
- D9 fixture 扫尾 + 全测试 单独 1 commit
- D10 是 codex synthesis commit
- 共 5 commit，与 P-0c-A/B/C 节奏一致

### 5.6 与 P-2/P-3 contract 冻结
P-1 完成后 14 capability schema **进入 schema-locked**（v0.4 → v0.4.1 patch only）。P-2 ClientAdapter 写出 RequestToCanonical 时不允许再扩字段；新需求要走 P-1.1 minor bump。

## 6. 验证标准

P-1 完成定义（exit criteria）：

1. **静态校验**：`go build ./... && go vet ./... && go build -tags debug ./...` 全部 0 error
2. **测试**：
   - `go test ./backend/internal/proto/...` 全绿
   - `go test -tags debug ./backend/internal/proto/...` 全绿
   - 新增 ~50 个 `TestINV1[4-9]_*` / `TestINV2[0-9]_*` / `TestINV3[0-2]_*` 子测试全 PASS
3. **Fixture 100% 通过**：35 条原 fixture + 新增 ~4 条 data_retention edge case fixture，validate + round-trip 全 PASS
4. **Hot path 不动**：`grep ValidateEnvelopeVersionGuard backend/internal/proto/openai_sse.go backend/internal/proto/gemini_sse.go` 仍只调 guard，未误升 ValidateEnvelope
5. **Codex review**：所有 commit `codex exec review --uncommitted` 0 HIGH
6. **文档**：envelope_validate.go godoc 从 "INV-1..13" 改 "INV-1..32"；docs/16 Phase 4.5 P-1 行勾 ✅
7. **Round-trip stability**：TestINV1_FullCapabilityRoundTrip 15 子测试加 4 新 fixture（data_retention 5 vocab）继续 PASS

## 7. 后续 phase 衔接

- **P-2** ClientAdapter 落地（3 个 client：openai_chat / openai_responses / anthropic_messages × RequestToCanonical + CanonicalToClientResponse + CanonicalEventToClientChunk + FinalizeClientStream）：在 schema-locked P-1 之后开工，估时 2-3 周
- **P-3** UpstreamAdapter 完整化（Bedrock eventstream / Gemini full / OpenAI full）：依赖 P-2 已有的 Tx1/Tx2 hookpoints
- **Q3 native passthrough route**：与 P-1 解耦，可在 P-1 期间由 codex 平行线推进（不阻塞 P-1）

## 8. 与 Codex lane 的 cross-discuss 流程

1. 本 plan 与 codex plan 并行起草（codex 已派任务 ID `be4lmvt97`）
2. 双 plan 写完后做 synthesis：列 agree / conflict / gaps 三表
3. 共识点：INV 编号、切片节奏、fixture 影响面
4. 分歧点：cross-ref severity 默认（warning vs error）、INV-28 是否拆 INV-28a/28b、ValidationError 是否加 Path 字段
5. Owner 通过 synthesis 后开 D1
