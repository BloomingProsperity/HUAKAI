# 2026-05-12 P-1 Capability Graph IR Payload 细化独立计划（Codex）

## 目标

| 字段 | 内容 |
| --- | --- |
| Owner directive | “HUAKAI 项目 PM-Orchestrator 让你作为 P-1 平行 plan 起草人写一份独立 plan。” |
| 工作单元 | P-1：Capability Graph IR payload 细化。 |
| 产出边界 | 只产出本 plan 文档，不编码、不改 validator、不改 fixture。 |
| 输出文件 | `docs/plans/2026-05-12-p1-capability-payload-refinement-codex.md` |
| 独立性 | 未读取 Claude lane 文件 `docs/plans/2026-05-12-p1-capability-payload-refinement-claude.md`。 |
| 当前分支 | `claude/phase-1`，工作区已有未跟踪 Claude plan，未触碰。 |
| P-0 基线 | commit `4d06548` 已给出 HCSF v0.4 envelope、14 capability families、INV-1..13 validator、35 fixture round-trip。 |
| 计划语言 | 中文。 |
| 成功标准 | 给出 INV-14..N 扩展清单、实施切片、破坏面、验证标准。 |
| 非目标 | 不扩展数据库 schema。 |
| 非目标 | 不改变 auth core。 |
| 非目标 | 不改变 billing ledger。 |
| 非目标 | 不接入真实 provider。 |
| 非目标 | 不读取非 MIT 参考项目源码。 |

本计划的核心判断：
P-0 已经把 envelope 形态和 tagged-union 框架立住。
P-1 不应再扩大 capability family 数量。
P-1 应把已有 payload 字段从“可反序列化”提升到“语义可验证”。
validator 仍应保持内存 IR 层校验，不引入 provider runtime 依赖。
跨 node 引用应以 `CapabilityGraph.Nodes` 和 `CapabilityGraph.Edges` 为真相源。
外部 provider id、URL、digest ref 应先被建模成 file/image/audio/video node，再被其它 node 引用。
这样可以避免 payload 字符串暗藏第二套图结构。

## 当前状态分析

### 已读源码

- `backend/internal/proto/capability_graph.go:3-173`：capability kind、stream readiness、node/edge/loss、tagged-union。
- `backend/internal/proto/envelope.go:5-55` 定义 HCSFEnvelope v0.4 顶层结构。
- `backend/internal/proto/envelope.go:57-93` 定义 `NewEmptyEnvelope` 默认值。
- `backend/internal/proto/envelope_validate.go:20-485`：当前 INV-1..13、graph/tagged-union/projection/stream/policy/extensions validator。
- `backend/internal/proto/protocol_loss.go:3-97`：loss severity、loss entry、silent drop 判定。
- `backend/internal/proto/capability_text.go:3-10`：TextNode。
- `backend/internal/proto/capability_tool.go:5-52`：ToolUse/ToolResult。
- `backend/internal/proto/capability_file.go:3-59` 与 image/audio/video 文件：DataLocator 与媒体 payload。
- `backend/internal/proto/capability_cache.go:3-41`：CacheControlNode。
- `backend/internal/proto/capability_structured.go:5-34`：StructuredOutputNode。
- `backend/internal/proto/capability_computer_use.go:5-34`：ComputerUseNode。
- `backend/internal/proto/capability_thinking.go:3-29`：ThinkingNode。
- `backend/internal/proto/capability_live.go:5-35`：LiveSessionNode。
- `backend/internal/proto/capability_batch.go:3-47`：BatchNode。
- `backend/internal/proto/capability_mcp.go:3-25`：MCPServerNode。
- `backend/internal/proto/capability_data_retention.go:3-54`：DataRetentionNode。
- `backend/internal/proto/hcsf.go:36-52` 定义 CanonicalMessage/CanonicalContentBlock。
- `backend/internal/proto/request_meta.go:19-120` 定义 RequestMeta/RequestControls。
- `backend/internal/proto/projection.go:3-51` 定义 ProviderProjection。
- `backend/internal/proto/stream_plan.go:3-69` 定义 StreamPlan。
- `backend/internal/proto/accounting.go:7-50` 定义 Accounting。
- `backend/internal/proto/fixtures_test.go:61-119` 定义 fixture validate 与 round-trip 测试。
- `backend/internal/proto/envelope_test.go:23-360,897-1024` 覆盖现有 INV 与全 capability round-trip。

### 已读 fixture 抽样

- 已统计 `backend/internal/proto/fixtures/` 下共有 35 个 JSON fixture。
- 已抽样 envelope minimal 家族：text/tool_use/tool_result/data_retention/file/image/audio/video/batch/cache/computer/live/mcp/structured/thinking。
- 已抽样 edge case：tool_use_chain、cross_tenant_isolation、native_passthrough_required。
- 已抽样 regression：new_api_4678_cache_metadata_sanitize、portkey_1579_cache_control_strip、litellm_27468_tool_args_lost。
- 已抽样 response：buffered_anthropic_tool_use。

### P-0 已覆盖

- INV-1：marshal/unmarshal round-trip 稳定，由测试覆盖。
- INV-2：nil/empty slice 兼容，由测试覆盖。
- INV-3：CapabilityNode tagged-union 与 CapabilityKind 一致。
- INV-4：Version 必须为 `0.4`。
- INV-5：RequestMeta 必填字段非空。
- INV-6：BufferedResponse 与非空 StreamEvents 不可同时存在。
- INV-7：ProtocolLoss 不允许 silent drop。
- INV-8：edge ID/type/from/to 基础引用完整性。
- INV-9：mutually_exclusive 不可双向重复。
- INV-10：Policy.DataRetention.Value 5 词汇枚举。
- INV-11：P-0 MidStreamFallbackPolicy 必须为 none。
- INV-12：Extensions key 只能用 `vendor:` 或 `experimental:` 前缀。
- INV-13：StreamPlan.Mode 必填且为 buffered/streaming/replay。

### P-1 缺口

- `CapabilityNode.StreamReady` 有 enum 定义，但当前未校验。
- TextNode 注释要求 `Block.Type=="text"`，当前未校验。
- TextNode Role 注释给出 canonical role，当前未校验。
- ToolUse/ToolResult status 有 enum，当前只靠 Go type，不校验 JSON 字符串。
- ToolUse.Input 注释要求 JSON object 或 null，当前未校验。
- ToolResult 与 ToolUse 的 ToolCallID 关系当前未校验。
- ToolResult 需要 `requires` edge 指向匹配 ToolUse，当前未校验。
- DataLocator.Kind 与 File/Image/Video SourceKind 一致性当前未校验。
- Audio.Transport 与 DataLocator.Kind 一致性当前未校验。
- Audio.Format 仍是自由字符串。
- ComputerUse.Environment 仍是自由字符串。
- StructuredOutput ParserMode/FailureRecovery/FallbackStrategy 仍是自由字符串。
- CacheControl.LocalityHint 仍是自由字符串。
- CacheControl.BreakpointRefs 当前只在 edge 层间接表达，不校验 refs。
- BatchNode.InputRef 当前是自由字符串。
- LiveSession.Modalities 当前是自由字符串数组。
- MCPServer InvocationNodeIDs/ResultNodeIDs 当前不校验引用目标。
- DataRetention 条件必填规则当前只校验顶层 Value 枚举。
- DataRetention node 与 Policy.DataRetention 的一致性当前未校验。
- ProviderProjection.NodeID 当前不校验存在性和 kind 一致性。
- ProtocolLoss Severity/Capability/NodeID/NativePath 的 v0.4 字段语义未完全校验。
- NodeSourceRef 指向 message/block/event 的索引范围当前未校验。

### fixture 破坏面预判

- 35 个 fixture 当前都应继续作为正向基线保留。
- `fixtures/envelope/tool_result_minimal.json` 会被“ToolResult 必须对应 ToolUse”规则打破。
- 建议把它改成包含匹配 ToolUse + required requires edge 的正向 fixture。
- 或者改名为 `_invalid_tool_result_orphan.json` 作为负向 fixture。
- 我建议前者，因为 envelope family 的 minimal 正向样本仍应存在。
- `fixtures/envelope/batch_minimal.json` 会被“Batch InputRef 必须指 file node”规则打破。
- 建议在同一 fixture 添加 `file` node，`batch.input_ref` 改为该 node ID。
- provider file id 应放入 FileNode.Locator，不应直接藏在 BatchNode.InputRef。
- `fixtures/envelope/cache_control_minimal.json` 的空 breakpoint_refs 应继续合法。
- 仅 block/message scope 应要求 breakpoint_refs 非空并可解析。
- `fixtures/envelope/data_retention_minimal.json` 已有 `request_store:false`，可通过条件规则。
- `fixtures/edge_case/cross_tenant_isolation.json` 已有 regional region，可通过条件规则。
- 当前没有 zdr_verified fixture，P-1 应新增正向和负向 fixture。
- 当前没有 partial ToolUse + PartialInput fixture，P-1 应新增。
- 当前没有 invalid enum fixture，P-1 应新增。

## INV 扩展清单

| INV | 一行 spec |
| --- | --- |
| INV-14 | 每个 CapabilityNode.StreamReady 必须为 `yes` / `no` / `partial`，空值或其它字符串拒绝。 |
| INV-15 | TextNode 必须满足 Role ∈ `user` / `assistant` / `system` / `tool`，且 Block.Type 必须等于 `text`。 |
| INV-16 | ToolUseNode 必须满足 ToolCallID/Name/Status 必填，Status ∈ `pending` / `partial` / `complete` / `error`，Input 只能是 JSON object 或 null。 |
| INV-17 | ToolUseNode 的 PartialInput 只能在 Status 为 `pending` 或 `partial` 时出现，Status=`partial` 时必须有 PartialInput 或 Input 不完整证据。 |
| INV-18 | ToolResultNode 必须满足 ToolCallID/Status/Content 必填，Status 只能为 `complete` / `error`，Status=`error` 时 IsError 必须为 true。 |
| INV-19 | 每个 ToolResultNode.ToolCallID 必须在同一 CapabilityGraph 内找到匹配 ToolUseNode.ToolCallID，且必须存在 required `requires` edge：tool_result node → tool_use node。 |
| INV-20 | DataLocator.Kind 必须为 `inline_base64` / `url` / `file_id` / `digest_ref`，Value 非空；File/Image/Video 的 SourceKind 必须等于 Locator.Kind。 |
| INV-21 | File/Image/Audio/Video 数值字段不得为负；Dimensions 出现时 width/height 都必须大于 0；TimeRange 不得负数且 EndMillis>0 时必须大于等于 StartMillis。 |
| INV-22 | ImageNode.MediaType 必须以 `image/` 开头，VideoNode.MediaType 必须以 `video/` 开头，FileNode.MediaType 必须包含 `/`。 |
| INV-23 | AudioNode 必须满足 Transport ∈ `inline` / `file` / `url` / `stream`，Transport 与 Locator.Kind 兼容，Format 固化为 P-1 白名单。 |
| INV-24 | AudioNode TranscriptPolicy 若非空必须为 `none` / `requested` / `provided`，SampleRateHz/Channels/DurationMillis 不得为负。 |
| INV-25 | CacheControlNode.Scope 必须为 `request` / `message` / `block` / `session` / `vendor`；LocalityHint 若非空必须为 `account_pin` / `account_recent` / `global`。 |
| INV-26 | CacheControlNode.BreakpointRefs 中每个非空 ref 必须解析到现有 node；Scope=`block` 或 `message` 时 BreakpointRefs 不得为空。 |
| INV-27 | BatchNode 必须满足 JobID/Endpoint/InputRef/Validation 必填，Validation ∈ `pending` / `validated` / `failed` / `complete`。 |
| INV-28 | BatchNode.InputRef 必须解析到同图 FileNode.ID；外部 URL/provider file id 必须通过该 FileNode.Locator 表达。 |
| INV-29 | RetryPolicy.MaxAttempts 不得为负；Backoff 若非空必须为 `fixed` / `exponential` / `provider_default`。 |
| INV-30 | DataRetentionNode.Value 必须沿用 5 词汇；`request_store_false` 必须显式 RequestStore=false。 |
| INV-31 | DataRetentionNode.Value=`regional_asserted` 必须有 Region；Value=`zdr_verified` 必须有 EvidenceRef 且 Enforcement=`verified`。 |
| INV-32 | DataRetentionNode.Value=`provider_contract_required` 必须 Enforcement=`contract_required` 且必须有 EvidenceRef 或明确的 deferred proof reason。 |
| INV-33 | Graph 内 DataRetentionNode 若存在，Policy.DataRetention 必须与最强 data_retention node 一致；data_retention provides edge 表示作用范围。 |
| INV-34 | ComputerUseNode 必须满足 Environment ∈ `browser` / `desktop` / `shell` / `mobile` / `other`，Approval ∈ `required` / `granted` / `denied` / `not_required`。 |
| INV-35 | ComputerUseNode.ScreenshotRef 若非空必须解析到 ImageNode 或 FileNode，Approval=`required` 时 AuditLabel 必填。 |
| INV-36 | StructuredOutputNode.Mode 必须为 `json_mode` / `json_schema` / `tool_strategy` / `provider_native`。 |
| INV-37 | StructuredOutputNode.Mode=`json_schema` 时 Schema 必须是 JSON object；Mode=`provider_native` 时必须有 native_required projection 或 RequestMeta.NativePassthrough=true。 |
| INV-38 | StructuredOutputNode.ParserMode/FailureRecovery/FallbackStrategy 若非空必须落在 P-1 白名单中。 |
| INV-39 | ThinkingNode BudgetTokens/HiddenTokens 不得为负；Redaction 必须为 `public` / `redacted` / `hidden` / `provider_only`。 |
| INV-40 | ThinkingNode.Redaction 为 `hidden` 或 `provider_only` 时，Blocks 不得携带可见 text；Policy.Redaction 不得弱于 node Redaction。 |
| INV-41 | LiveSessionNode.Transport 必须为 `wss` / `sse`，Modalities 只能包含 `text` / `audio` / `video`，ToolNodeIDs 必须解析到 tool_use/computer_use/mcp_server node。 |
| INV-42 | MCPServerNode.ServerLabel 必填，AllowedOperations 不得包含空字符串，InvocationNodeIDs 必须指 tool_use/computer_use node，ResultNodeIDs 必须指 tool_result node。 |
| INV-43 | CapabilityProjection.NodeID 若非空必须解析到现有 node，且 CapabilityProjection.Capability 必须等于该 node.Kind。 |
| INV-44 | 每个非 preserved projection 的 ProtocolLoss 必须带 Severity 枚举、Capability、NodeID 或明确图级说明；native_required 必须有 NativePath。 |
| INV-45 | ProtocolLossEntry.Severity 若非空必须为 `info` / `warning` / `error`；NodeID 若非空必须解析到现有 node；Capability 若非空必须合法。 |
| INV-46 | NodeSourceRef 的 message_index/block_index/event_index 必须非负并在 Messages/StreamEvents 范围内；RequestField 必须在 allowlist。 |
| INV-47 | 每个 CapabilityNode 建议有且仅有一个 matching CapabilityProjection；例外只能是 empty graph 或明确图级 projection。 |

### 白名单建议

- Audio.Format P-1 白名单建议：`wav` / `mp3` / `opus` / `pcm16` / `flac` / `m4a` / `webm`。
- StructuredOutput.ParserMode P-1 白名单建议：`client` / `provider` / `parser`。
- StructuredOutput.FailureRecovery P-1 白名单建议：`none` / `retry` / `repair` / `native_required`。
- StructuredOutput.FallbackStrategy P-1 白名单建议：`prompt` / `tool` / `native` / `unsupported`。
- CacheControl.LocalityHint 白名单沿用代码注释：`account_pin` / `account_recent` / `global`。
- Batch RetryPolicy.Backoff 白名单沿用代码注释：`fixed` / `exponential` / `provider_default`。

## 工作量切片

### Day 0.5：预检与破坏面固定

- 只读确认当前 35 fixtures 全部通过现有 `ValidateEnvelope`。
- 产出 fixture impact 表，列出会被 INV-19/INV-28 打破的文件。
- 文件读取范围：`backend/internal/proto/fixtures/**/*.json`。
- 不修改代码。
- 不修改 fixture。
- 预期新增文档：可选 `docs/plans/2026-05-12-p1-fixture-impact.md`。
- 若 Owner 只要求实现，不要求额外文档，则把 impact 表写入 PR 描述即可。

### Day 1：低破坏枚举与局部 payload validator

- 修改 `backend/internal/proto/envelope_validate.go`。
- 增加 `validateCapabilityNodePayload` 分发函数。
- 增加 enum set helper，避免多个 switch 漂移。
- 覆盖 INV-14、INV-15、INV-16、INV-18、INV-20、INV-21、INV-22。
- 覆盖 INV-23、INV-24、INV-25、INV-27、INV-29、INV-34、INV-36、INV-39。
- 修改 `backend/internal/proto/envelope_test.go`。
- 新增 table-driven negative tests 约 18-24 个。
- 不改正向 fixture。
- 预期所有现有 35 fixture 仍通过。
- 风险等级：低到中。
- 主要风险：Audio.Format 白名单过窄。
- 缓解：白名单先按现有 provider 常见格式放宽，后续只增不删。

### Day 2：跨 node 引用完整性

- 修改 `backend/internal/proto/envelope_validate.go`。
- 在 `validateCapabilityGraph` 中构建 node index、kind index、tool_call_id index。
- 覆盖 INV-19、INV-26、INV-28、INV-35、INV-41、INV-42、INV-43、INV-46、INV-47。
- 修改 `fixtures/envelope/tool_result_minimal.json`。
- 给 tool_result_minimal 添加匹配 ToolUse node。
- 添加 required `requires` edge：tool_result → tool_use。
- 修改 `fixtures/envelope/batch_minimal.json`。
- 添加 FileNode，Locator.Kind=`file_id`，Locator.Value 保留原 provider input file id。
- 把 BatchNode.InputRef 改成 FileNode.ID。
- 新增负向 fixture 约 6-8 个。
- 负向 fixture 使用 `_invalid_` 前缀复用现有 walker。
- 新增测试断言每个负向 fixture 返回指定 INV。
- 风险等级：中。
- 主要风险：真实历史对话可能出现 orphan tool_result。
- 缓解：P-1 先要求同 envelope 完整图；若未来需要截断历史，新增 ExternalToolCallRef，而不是放松 ToolResult。

### Day 3：条件必填与 policy 一致性

- 修改 `backend/internal/proto/envelope_validate.go`。
- 覆盖 INV-30、INV-31、INV-32、INV-33、INV-37、INV-38、INV-40、INV-44、INV-45。
- 增加 DataRetention 条件校验。
- 增加 DataRetention node 与 Policy.DataRetention 一致性校验。
- 增加 StructuredOutput native_required 关联校验。
- 增加 ProtocolLoss v0.4 字段语义校验。
- 新增 `zdr_verified` 正向 fixture 1 个。
- 新增 `zdr_verified` 缺 EvidenceRef 负向 fixture 1 个。
- 新增 `provider_contract_required` 缺 EvidenceRef 负向 fixture 1 个。
- 新增 `provider_native` 缺 native projection 负向 fixture 1 个。
- 新增 hidden thinking 携带 visible text 负向 fixture 1 个。
- 风险等级：中。
- 主要风险：Policy.DataRetention 与 graph 多 node 语义需要“最强”排序。
- 缓解：P-1 先只允许 0 或 1 个 data_retention node，多个 node 进入 P-2 再细化作用域合并。

### Day 4：测试收敛与文档同步

- 修改 `backend/internal/proto/envelope_test.go`。
- 修改 `backend/internal/proto/fixtures_test.go`，让负向 fixture 可校验期望 INV。
- 可通过 fixture 文件名或 JSON extension 写 expected INV。
- 我建议文件名携带 INV，例如 `_invalid_inv19_tool_result_orphan.json`。
- 运行 `go test ./backend/internal/proto`。
- 运行 `go test -tags debug ./backend/internal/proto`。
- 更新相关 proto 注释，保证注释与 validator 一致。
- 可选更新 `docs/16_PHASED_DELIVERY_PLAN.md` 的 P-1 状态。
- 不更新 `LICENSE`。
- 不改数据库 schema。
- 风险等级：低。
- 主要风险：负向 fixture walker 只判断“有错误”，不判断错误编号。
- 缓解：扩展测试 helper，不改变 production validator 行为。

### 工作量估计

- 总工程量：3.5-4.5 engineer-days。
- Day 0.5 可并入 Day 1，但建议保留，避免先改 validator 后发现 fixture 语义冲突。
- Day 1 预计 0.75-1.0 天。
- Day 2 预计 1.0-1.25 天。
- Day 3 预计 1.0-1.25 天。
- Day 4 预计 0.75-1.0 天。
- 新增 validator helper：约 250-400 行 Go。
- 修改测试：约 300-500 行 Go。
- 修改正向 fixture：2 个。
- 新增正向 fixture：1-2 个。
- 新增负向 fixture：12-18 个。
- 新增 INV：INV-14..47，共 34 条。

## 风险与权衡

### 条件必填会不会破 35 fixture

- `request_store_false → RequestStore=false` 不应破坏现有 fixture。
- `regional_asserted → Region required` 不应破坏现有 fixture。
- `zdr_verified → EvidenceRef required` 不影响现有 fixture，因为当前没有 zdr fixture。
- `provider_contract_required → EvidenceRef required` 不影响现有 fixture，因为当前没有该值 fixture。
- `provider_native → native_required projection` 不应破坏现有 native fixture。
- `json_schema → Schema object` 不应破坏现有 structured fixtures。
- `block/message cache scope → non-empty breakpoint_refs` 不应破坏现有 fixtures。
- `request cache scope → breakpoint_refs 可为空` 必须保留，否则会破 `cache_control_minimal`。

### 必然破坏点

- INV-19 会破 `fixtures/envelope/tool_result_minimal.json`。
- 这是合理破坏，因为 tool_result orphan 会让 tool 调用链无法审计。
- 但真实截断历史场景可能需要外部 tool_call ref。
- P-1 不应偷渡自由字符串 ref；应在 P-2 设计 ExternalToolCallRef。
- INV-28 会破 `fixtures/envelope/batch_minimal.json`。
- 这是合理破坏，因为外部 provider file id 应归入 FileNode.Locator。
- 这样 BatchNode.InputRef 只承担图内引用，不承担 provider id 语义。

### enum 固化风险

- enum 过窄会拒绝未来 provider 新格式。
- enum 过宽会让 validator 失去价值。
- 建议 P-1 enum 只约束已知稳定协议层字段。
- MCP AllowedOperations 不应做固定全局 enum。
- Tool name 不应做固定全局 enum。
- Audio format 可做白名单，但必须允许后续只增不删。
- ComputerUse Environment 可做白名单，因为代码注释已经给出固定集合。

### graph 一致性风险

- ProviderProjection.NodeID 当前可选，图级 projection 可能省略。
- P-1 不应强制所有 projection 都有 NodeID。
- 但只要 NodeID 非空，就必须解析并与 Capability 一致。
- “每个 node 必须有 projection”可作为建议性 INV-47。
- 若 empty graph，则 projection 为空合法。
- 若 node 是纯内部 policy node，也仍建议有 projection，避免 silent capability drop。

### DataRetention 合并风险

- 多个 DataRetentionNode 的作用域可能通过 provides edge 表达。
- P-1 若直接实现“最强 policy”排序，会引入复杂规则。
- 建议 P-1 先支持 0/1 个 DataRetentionNode 的强一致。
- 多 node 场景只校验每个 node 自身条件与 edge target。
- 多 node 合并进入 P-2。

### Clean-room 风险

- 本计划只读取 HUAKAI 内部代码和 fixture。
- 未读取非 MIT reference project source。
- 未复制参考项目结构、schema 或实现。
- clean-room 风险为低。

### 安全风险

- 计划阶段无运行时安全风险。
- P-1 实施时需避免把 `AuthRef`、`CacheKeyHint`、`ServerURI` 当作真实 secret 校验。
- P-1 可以增加“明显 secret 前缀”负向测试，但不应扫描真实 secret。
- P-1 不触碰 auth core。
- P-1 不触碰 billing ledger。
- P-1 不触碰 quota enforcement。

### 需要 Owner/PM 决策

- 是否接受 INV-28 的严格语义：BatchNode.InputRef 只允许 file node id。
- 是否接受 INV-19 的严格语义：ToolResult 必须同 envelope 有 ToolUse。
- 是否允许 P-1 暂不支持多 DataRetentionNode 合并。
- Audio.Format 初始白名单是否采用本计划建议。
- `provider_contract_required` 是否必须 EvidenceRef，还是允许 deferred proof reason。
- INV-47 是强制还是 warning；我建议 P-1 先强制“NodeID 非空时校验”，projection 覆盖率作为测试期 warning。

## 验证标准

### 代码验证

- `go test ./backend/internal/proto` 必须通过。
- `go test -tags debug ./backend/internal/proto` 必须通过。
- 所有正向 fixture 必须通过 `ValidateEnvelope`。
- 所有负向 fixture 必须被 `ValidateEnvelope` 拒绝。
- 负向 fixture 必须断言错误中包含对应 INV 编号。
- INV-14..47 每条至少有一个负向测试。
- 每个 payload family 至少保留一个正向 fixture。
- 修改后的 35 个原始正向 fixture 仍应 round-trip 稳定。

### fixture 验证

- 正向 fixture 数量：现有 35 个继续保留为正向。
- 允许修改其中 2 个以满足更强 schema。
- `tool_result_minimal` 修改后仍是 tool_result family 正向样本。
- `batch_minimal` 修改后仍是 batch family 正向样本。
- 新增 ZDR 正向 fixture。
- 新增 partial tool_use 正向 fixture。
- 新增 negative fixture 12-18 个。
- negative fixture 文件名必须以 `_invalid_` 开头。
- negative fixture 文件名建议包含 INV 编号。

### 不变量验收

- 每条新增 INV 在 `envelope_validate.go` 注释中出现。
- 每条新增 INV 在 `envelope_test.go` 或 fixture negative test 中出现。
- validator 错误必须返回 `ValidationError{Inv: "INV-X"}`。
- 同一错误不要复用过宽 INV 编号掩盖真实失败。
- helper 命名应按 payload family 聚合，避免一个巨型函数难审。
- enum set 应集中定义，避免测试和 validator 双真相源。

### 回归验收

- P-0 的 INV-1..13 测试不能删除。
- P-0 的 fixture walker 不能降低严格度。
- P-0 的 protocol_loss silent drop 规则不能放宽。
- P-0 的 native_required NativePath 规则不能放宽。
- P-0 的 MidStreamFallbackPolicy none 规则不能放宽。
- P-0 的 Extensions prefix 规则不能放宽。

### Owner 汇报标准

- 汇报必须说明：新增了哪些 INV。
- 汇报必须说明：改了哪些 fixture。
- 汇报必须说明：有没有功能缩水。
- 汇报必须说明：有没有 clean-room 风险。
- 汇报必须说明：有没有安全风险。
- 汇报必须说明：哪些决策需要 Owner 确认。

结论：
P-1 应先扩 fixture，再开严格 validator。
否则 INV-19 和 INV-28 会让现有正向全集变红。
推荐执行顺序是“impact 表 → 正向 fixture 修补 → 低破坏 enum 校验 → 跨 node 引用 → 条件必填 → negative fixture 精确 INV 断言”。
