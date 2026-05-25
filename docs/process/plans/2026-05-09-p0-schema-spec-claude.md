# P-0 Schema Spec — HCSFEnvelope v0.4 Go Type 锁定 (Claude Lane)

**日期**: 2026-05-09
**Lane**: claude (sonnet via general-purpose Agent)
**对应 Codex lane**: `docs/process/plans/2026-05-09-p0-schema-spec-codex.md`（写作时未见）
**前置批准 (Owner 2026-05-09)**: D1 不入库 / D2 14 capability / D3 不做 schema migration / D5 standard auth + audit / D6 cache_control 不含跨账号复制 / D7 4 vendor + $100/周 budget / D11 `/v1/responses` native passthrough only / D12 data_retention 5 词汇

**前置上下文**:
- `docs/process/plans/2026-05-09-hcsf-v04-implementation-synthesis.md`（synthesis 8 phase + 14 capability）
- `docs/process/plans/2026-05-09-hcsf-v04-implementation-claude.md`（自家 lane 8 phase / 13 capability — 本次按 D2 决议升到 14）
- `docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md`（market + issue mining 三 lane 合成）
- `docs/research/2026-05-09-axis3-huakai-current-state.md`（5 红线 + 全仓零 ClientAdapter 等现状）
- `docs/research/2026-05-09-issue-mining-cross-repo.md`（new-api#4678 / sub2api#1552 / Portkey#1579 等 110+ issue）
- `backend/internal/proto/proto.go`（`HCSF struct{}` 空壳 + ClientAdapter / UpstreamAdapter / ProtocolLossEntry / Verdict / Direction）
- `backend/internal/proto/hcsf.go`（CanonicalRequest / CanonicalMessage / CanonicalContentBlock / CanonicalEvent / CanonicalContentDelta / CanonicalResponse / CanonicalUsage / CanonicalStopReason）
- `backend/internal/proto/capability_matrix.go`（FeatureName / ClientProtocol / UpstreamProtocol 现有 enum + DefaultMatrix 粗粒度规则）
- `backend/internal/proto/field_matrix.go`（FieldVerdict / FieldTransformKind 字段级 matrix）
- `backend/internal/proto/passthrough.go`（U7-A PassthroughEnvelope）

---

## TL;DR

1. **HCSFEnvelope 是顶层信封**——把现有平铺的 `CanonicalRequest / CanonicalResponse / CanonicalEvent` 包成一个版本化、可路由、可审计的单一 wrapper（取代当前 `proto.HCSF struct{}` 空壳）；**内存 IR only，不入库（D1/D3）**。
2. **14 capability node**——按 D2 决议 file/image/audio/video 拆开；每个 node 一个 Go 类型 + 一个 JSON tag 标 + stream 行为标记（streaming / batch / live）；capability 之间用 `CapabilityGraph` 边模型记录 (provides / requires / loses)。
3. **`ProtocolLossEntry` 一等公民**——保留现有 4 字段（feature / direction / verdict / note）+ 升格新增 capability_id / vendor / lossy_axis 三字段；envelope 顶层带 `[]ProtocolLossEntry`，禁止 silent drop（issue `Portkey#1579` / `LiteLLM#27468` / `new-api#4697` 三连证据）。
4. **`ProviderProjection / StreamPlan / Accounting / Policy`**——四个子结构挂在 envelope 上，分别承载 vendor 选型 + 流播策略 + 计费/usage + 数据保留与审计。
5. **命名空间清理**：`proto.HCSF`（空 struct）删除 → 引入 `proto.HCSFEnvelope`；`proto.UpstreamState` 接口（在 `forwarder.go:337-351` 由 type-switch 实例化）保留但不进 envelope；`proto.ContentBlock` 不存在（现状是 `CanonicalContentBlock`），保持现命名 + 改 `Type` 字段为 typed enum。
6. **兼容性迁移**：v0.3 → v0.4 通过 `Version string` envelope 字段 + alias 类型；CanonicalRequest / CanonicalResponse / CanonicalEvent 不变，仅升格为 envelope 的字段，未来 P-1 才会扩 capability nodes。
7. **JSON Round-Trip 不变量**：envelope marshal → unmarshal 后所有 typed 字段、PassthroughEnvelope.Extra、ProtocolLossEntry 数量、CapabilityGraph 边、StreamPlan 三全字段必须一致；fixture 文件 `tests/fixtures/hcsf/v0.4/*.json` 验证。
8. **测试 fixture 要求**：每个 capability node 必须 ≥1 fixture；3 client × 5 upstream 共 15 pair 各至少 1 minimal fixture；issue regression fixture（cache_control / mid-stream / cch=xxx 三类）独立目录。
9. **推迟决策点 D4 / D9 / D10 留白**：tool name SHA / mid-stream fallback scope / capability matrix cell 数 由后续 phase 实施时补充字段；当前 schema 只留 `Reserved map[string]json.RawMessage` extension hook。

---

## 1. HCSFEnvelope v0.4 顶层结构

### 1.1 设计原则

| 原则 | 含义 |
|---|---|
| **顶层信封统一** | 不再用三个平铺类型（CanonicalRequest / CanonicalResponse / CanonicalEvent）做"事实上的 envelope"；引入显式 `HCSFEnvelope` 包住所有上下游交换；envelope 自描述其 kind（request / response / event / batch / native_passthrough） |
| **版本化必填** | `Version` 字段任何代码不可省；`v0.4` 是首个真值；v0.5+ 时通过 `Version + Migrations` 做 forward-compat（但 D3 已决：v0.4 不做 schema migration） |
| **内存 IR only** | 不持久化到数据库（D1/D3）；marshal 仅用于 capability matrix property test fixture / cross-attempt streaming continuity（P-2 末延展） / debug log；envelope 不进入 `usage_record` 表 |
| **Capability-driven** | capability 节点是 envelope 的"内容描述"——同一 envelope 可同时含 text + tool_use + thinking 节点，用 CapabilityGraph 边表达 provides / requires / mutually_exclusive / loses 关系 |
| **Provider-agnostic** | envelope 不绑定具体 vendor；`ProviderProjection` 是 vendor-resolved 投影结果，与 envelope 主体分离（vendor 切换不重写 envelope） |
| **Loss-visible** | `[]ProtocolLossEntry` 是 envelope 字段；任何 silent drop 必须改为 emit loss entry；P-5 capability matrix property test 验证 0 silent drop |

### 1.2 顶层 Go struct

文件路径：`backend/internal/proto/envelope.go`（新增）

```go
// HCSFEnvelope 是 v0.4 起的顶层信封——所有 client × upstream 交换的 IR 单一容器。
//
// 取代 v0.3 时期 proto.HCSF struct{} 的空壳（proto.go:13-18）。
// 三个事实上独立使用的 CanonicalRequest / CanonicalResponse / CanonicalEvent
// 升格为 envelope 的子字段，按 Kind 选用其一。
//
// 内存 IR only（D1/D3 锁定）：不持久化到 DB，marshal 仅用于：
//   - capability matrix property test fixture（P-5）
//   - cross-attempt streaming state continuity（P-2 留接口、P-8 实装）
//   - debug log + admin UI envelope inspector（P-7 dashboard tile）
type HCSFEnvelope struct {
    // Version 是 envelope schema 版本字符串。v0.4 起首个真值。
    // 任何 marshal 路径必须填；missing/empty → unmarshal 阶段报 ErrSchemaVersion。
    Version string `json:"hcsf_version"`

    // Kind 是 envelope 形态枚举。决定 Request / Response / Event / Batch /
    // NativePassthrough 五字段中哪一个是有效载荷。
    Kind EnvelopeKind `json:"kind"`

    // RequestID 是同一逻辑请求的全局 ID（非 attempt）。跨 envelope 共享。
    // 与 PASR-lite 的 attempt-lease-claim 三 ID 兼容：envelope.RequestID
    // 等同 PASR.RequestID；envelope 不持有 AttemptID/LeaseID/ClaimID（那是
    // pool/forwarder 域）。
    RequestID string `json:"request_id"`

    // CreatedAt 是 envelope 构造时间（gateway 入口侧创建）。UTC RFC3339Nano。
    // 用于 latency budget computation 与 debug log。
    CreatedAt string `json:"created_at"`

    // ClientProtocol 是入口客户端协议族。openai_chat / openai_responses /
    // anthropic_messages 三选一（与 capability_matrix.go ClientProtocol enum 对齐）。
    ClientProtocol ClientProtocol `json:"client_protocol"`

    // ============== 五选一载荷：按 Kind 决定 ==============

    // Request 是请求侧载荷；Kind == EnvelopeKindRequest 时非空。
    Request *CanonicalRequest `json:"request,omitempty"`

    // Response 是 buffered 响应侧载荷；Kind == EnvelopeKindResponse 时非空。
    Response *CanonicalResponse `json:"response,omitempty"`

    // Event 是流事件载荷；Kind == EnvelopeKindEvent 时非空。
    Event *CanonicalEvent `json:"event,omitempty"`

    // Batch 是 batch capability 的 job submit / status 载荷；Kind == EnvelopeKindBatch 时非空。
    // P-0 仅留 schema 占位；P-1 完整 schema；P-4 native passthrough 真接 OpenAI Batch / Anthropic Message Batches。
    Batch *BatchPayload `json:"batch,omitempty"`

    // NativePassthrough 是 /v1/native/<vendor>/<endpoint> 显式入口载荷；
    // Kind == EnvelopeKindNativePassthrough 时非空。
    // 不做 capability normalization——直接透传 raw bytes + auth substitution。
    NativePassthrough *NativePassthroughPayload `json:"native_passthrough,omitempty"`

    // ============== 横向能力描述 ==============

    // Capabilities 是本 envelope 涉及的 capability 节点集合。
    // 14 capability node 各自一个类型；envelope 可同时含多个（如 tool_use + thinking + cache_control）。
    Capabilities []CapabilityNode `json:"capabilities,omitempty"`

    // CapabilityGraph 是 capability 之间的边集合（provides / requires / loses / mutually_exclusive）。
    // P-1 实施时填；P-0 schema 锁定字段名 + 不必填。
    CapabilityGraph *CapabilityGraph `json:"capability_graph,omitempty"`

    // ProviderProjection 是 vendor 选型 + 路由结果。Router/Pool 决议后填。
    // envelope 主体不绑定 vendor；vendor 切换只换 ProviderProjection，envelope 体不变。
    ProviderProjection *ProviderProjection `json:"provider_projection,omitempty"`

    // StreamPlan 是 streaming 路径计划。Kind in (Request/Event) 时可填。
    // 含：是否流式、SSE/binary 形态、mid-stream fallback policy、resume 策略。
    StreamPlan *StreamPlan `json:"stream_plan,omitempty"`

    // Accounting 是 usage / cost / cache hit 计量结果。
    // Response/Event 完成阶段填；Request 阶段一般为空。
    Accounting *Accounting `json:"accounting,omitempty"`

    // Policy 是 data retention / audit / fingerprint sanitization / safety 策略。
    // 客户端 + tenant + admin policy 的合成结果。
    Policy *Policy `json:"policy,omitempty"`

    // Losses 是本 envelope 在 client→canonical / canonical→upstream / upstream→canonical /
    // canonical→client 四个方向中累计的 ProtocolLossEntry。
    // 禁止 silent drop——任何 verdict ∈ {LOSSY, UNSUPPORTED} 都必须 append。
    Losses []ProtocolLossEntry `json:"losses,omitempty"`

    // Reserved 是后续 phase 扩展占位。P-1 capability node、P-2 ClientAdapter
    // 实施时如需新字段又不破坏 v0.4 兼容，先放这里；P-1 末做 schema review 时
    // 决定是否升为正式字段。
    //
    // 注意：Reserved 不等同于 PassthroughEnvelope.Extra——后者是 vendor JSON 顶层
    // 未识别字段（U7-A），属于 vendor 域；Reserved 是 HUAKAI 内部 evolutionary 域。
    Reserved map[string]json.RawMessage `json:"reserved,omitempty"`
}

// EnvelopeKind 是 envelope 五种形态枚举。
type EnvelopeKind string

const (
    // EnvelopeKindRequest：请求侧（client → canonical / canonical → upstream）。
    EnvelopeKindRequest EnvelopeKind = "request"

    // EnvelopeKindResponse：buffered 响应（upstream → canonical → client，整体）。
    EnvelopeKindResponse EnvelopeKind = "response"

    // EnvelopeKindEvent：单个流事件（streaming SSE chunk 等价）。
    EnvelopeKindEvent EnvelopeKind = "event"

    // EnvelopeKindBatch：batch job 提交 / 状态 / 输出。
    EnvelopeKindBatch EnvelopeKind = "batch"

    // EnvelopeKindNativePassthrough：/v1/native/<vendor>/<endpoint> 显式透传。
    EnvelopeKindNativePassthrough EnvelopeKind = "native_passthrough"
)

// 当前版本常量。任何 envelope 构造路径建议直接用此常量。
const HCSFVersionV04 = "0.4"
```

### 1.3 与现有 `proto.HCSF struct{}` 的关系

- `proto.HCSF` 删除（`proto.go:15-18` 当前空壳 + 注释 `TODO(phase-4): canonical request + response + event types`）。
- 删除后立即在 `proto.go` 顶部加一行 alias：`type HCSF = HCSFEnvelope`（仅作 v0.3 → v0.4 编译期兼容）；2 周后（P-1 进入时）alias 删除。
- 现有 4 vendor adapter（anthropic_sse / openai_sse / gemini_sse / bedrock_eventstream）当前直接接受 `*HCSF` 形参 → 改为 `*HCSFEnvelope`；alias 让中间状态可编译。

### 1.4 envelope 不收纳的内容（边界约束）

- **不持有 attempt-lease-claim 三 ID**：那是 PASR pool / forwarder 域；envelope 仅持 `RequestID`。
- **不持有 routing 决策日志**：routing 历史在 `usage_record_draft` 中追加，不混入 envelope。
- **不持有 raw HTTP request / response bytes**：raw 在 `forwarder.ForwardRequest.Body` / `streamScanner` 域；envelope 仅持解析后的 typed canonical 字段。
- **不持有 secret / credential**：`ProviderProjection.AccountID` 是引用 ID，不内嵌 token；任何 secret 通过 `transport.Factory` 注入。

---

## 2. 14 Capability Node Go 类型 + JSON tag

### 2.1 设计模式

- 每个 capability node 一个独立 Go struct + 独立 `.go` 文件（`backend/internal/proto/capability/<name>.go` 子包，或 `backend/internal/proto/capability_<name>.go` 平铺，**P-0 决策**：平铺到 `proto/` 包以避免循环 import；子包重构推到 P-1 末）。
- 所有 node 实现 `CapabilityNode` interface（描述其 kind / required vendor matrix / streamability）。
- `CapabilityNodeKind` 是 string 枚举常量；每个常量 = JSON tag 上的 `kind` 值。
- `CapabilityKind` 字段（每 node 都有）值固定为对应常量；用于 unmarshal 阶段的 dispatch。

### 2.2 14 capability node 一览（D2 决议 14 个）

| # | Node Go type | CapabilityKind | 主要 IR 形态 | Stream 行为 | 来源依据 |
|---|---|---|---|---|---|
| 1 | `TextCapability` | `capability_text` | role / 有序 ContentBlock / stop_reason / finish_class | streaming first-class | synthesis §2 共识 + Anthropic SSE 6 事件 (anthropic_sse.go:138-181) |
| 2 | `ToolUseCapability` | `capability_tool_use` | tool_call_id / display name / input JSON / partial argument deltas / signature | streaming + buffered 双形态 | synthesis §2 共识 + LiteLLM#27468 / new-api#4671 issue 证据 |
| 3 | `ThinkingCapability` | `capability_thinking` | reasoning budget / blocks / signatures / hidden token accounting / redaction class | streaming（Anthropic / Gemini thought parts） | synthesis §2 + Anthropic extended thinking + OpenAI o-series effort + Gemini thought parts |
| 4 | `CacheControlCapability` | `capability_cache_control` | scope / breakpoints / cache_key hint / read+write usage（**D6 不含跨账号复制意图**） | non-stream（marker on request） | synthesis §2 + new-api#4678 / Portkey#1579/#1589 issue 证据 |
| 5 | `StructuredOutputCapability` | `capability_structured_output` | json_mode intent / strict schema / parser mode / failure recovery / fallback strategy | streaming（partial parse） | synthesis §2 + Portkey#1570 issue (Vertex Anthropic schema reject) |
| 6 | `ComputerUseCapability` | `capability_computer_use` | env target / action / screenshot/input blocks / approval / audit | non-stream（默认 native passthrough） | synthesis §2 + Anthropic computer-use beta header |
| 7 | `FileCapability` | `capability_file` | source_kind / media type / file id/URL digest / size / retention label | non-stream（upload lifecycle） | synthesis §2（Codex 主张分开）+ issue mining 文件断点 |
| 8 | `ImageCapability` | `capability_image` | URI / base64 / file_id / media type / dimensions / loss audit | non-stream（content block） | synthesis §2（Codex 主张分开）+ envoy data-URI vs URL 数据 |
| 9 | `AudioCapability` | `capability_audio` | transport / format / sample / transcript policy / live compat | streaming / non-stream 双形态 | synthesis §2（Codex 主张分开） |
| 10 | `VideoCapability` | `capability_video` | URL/base64/file ref / time range / size/codec | non-stream（chunk）/ live（Gemini Live） | synthesis §2（Codex 主张分开）|
| 11 | `LiveSessionCapability` | `capability_live_session` | connect params / bidi event stream / modality set / tool availability / resume token / close reason | **bidirectional WSS（独立形态）** | synthesis §2 + Gemini Live + OpenAI Realtime |
| 12 | `BatchCapability` | `capability_batch` | async job graph / input file / endpoint / validation / output/error / cost / retry | **async（非 streaming，job lifecycle）** | synthesis §2 + Portkey#1156-1158 issue 证据 |
| 13 | `MCPServerCapability` | `capability_mcp_server` | server label / allowed ops / approval / invocation events / result blocks | streaming（events） | synthesis §2 + LiteLLM#7934 / Portkey#926 issue 证据 |
| 14 | `DataRetentionCapability` | `capability_data_retention` | no-train / ZDR / regional / request_store / audit / enforcement（**D12 五词汇 unknown / request_store_false / provider_contract_required / regional_asserted / zdr_verified**） | non-stream（policy on request） | synthesis §2 + Codex 主张 |

### 2.3 通用接口 + 嵌入字段

文件路径：`backend/internal/proto/capability_node.go`（新增）

```go
// CapabilityNode 是 14 capability 共有的 polymorphic 接口。
//
// envelope.Capabilities []CapabilityNode 字段在 marshal/unmarshal 时通过
// CapabilityKind 字段做 dispatch（参考 §2.6 unmarshal 设计）。
type CapabilityNode interface {
    // GetCapabilityKind 返回 node 的 kind 字符串（用于 dispatch）。
    GetCapabilityKind() CapabilityKind

    // SupportedClients 返回 client protocol 集合中已确认支持本 capability 的子集。
    // P-0 仅声明接口，实际值在 P-1 capability matrix 决议时填。
    SupportedClients() []ClientProtocol

    // SupportedUpstreams 返回 upstream protocol 集合中已确认支持本 capability 的子集。
    SupportedUpstreams() []UpstreamProtocol

    // IsStreamable 报告本 capability 是否原生支持 streaming（true）还是仅 buffered（false）。
    IsStreamable() bool

    // RequiresNativePassthrough 报告本 capability 在 D 决议下是否默认走 native passthrough
    // （D11 锁定 /v1/responses + computer_use + live_session + 部分 video → true）。
    RequiresNativePassthrough() bool
}

// CapabilityKind 是 capability node 类型枚举。
type CapabilityKind string

const (
    CapabilityKindText             CapabilityKind = "capability_text"
    CapabilityKindToolUse          CapabilityKind = "capability_tool_use"
    CapabilityKindThinking         CapabilityKind = "capability_thinking"
    CapabilityKindCacheControl     CapabilityKind = "capability_cache_control"
    CapabilityKindStructuredOutput CapabilityKind = "capability_structured_output"
    CapabilityKindComputerUse      CapabilityKind = "capability_computer_use"
    CapabilityKindFile             CapabilityKind = "capability_file"
    CapabilityKindImage            CapabilityKind = "capability_image"
    CapabilityKindAudio            CapabilityKind = "capability_audio"
    CapabilityKindVideo            CapabilityKind = "capability_video"
    CapabilityKindLiveSession      CapabilityKind = "capability_live_session"
    CapabilityKindBatch            CapabilityKind = "capability_batch"
    CapabilityKindMCPServer        CapabilityKind = "capability_mcp_server"
    CapabilityKindDataRetention    CapabilityKind = "capability_data_retention"
)

// CapabilityNodeBase 是所有 node 共用嵌入字段，提供 CapabilityKind + 公共 metadata。
// 各 node 用 struct embedding (CapabilityNodeBase + 自有字段) 拼装。
type CapabilityNodeBase struct {
    // CapabilityKind 是 node 类型常量。marshal/unmarshal 用作 dispatch key。
    CapabilityKind CapabilityKind `json:"kind"`

    // DeclaredBy 标注该 node 是哪一侧声明加入 envelope（client_intent / upstream_response /
    // gateway_inferred）；用于 audit + admin UI 显示来源。
    DeclaredBy DeclaredSource `json:"declared_by,omitempty"`

    // Notes 给 admin / debug 留可读 metadata（不影响行为）。
    Notes string `json:"notes,omitempty"`
}

// DeclaredSource 标注 capability node 来源。
type DeclaredSource string

const (
    DeclaredByClientIntent      DeclaredSource = "client_intent"      // 客户端请求显式包含
    DeclaredByUpstreamResponse  DeclaredSource = "upstream_response"  // 上游响应中识别
    DeclaredByGatewayInferred   DeclaredSource = "gateway_inferred"   // gateway 根据请求形态推断
)
```

### 2.4 14 节点详细 Go 类型

文件路径：`backend/internal/proto/capability_*.go`（每 node 一个文件，命名见列）

#### 2.4.1 `TextCapability`（`capability_text.go`）

```go
type TextCapability struct {
    CapabilityNodeBase
    // FinishClass 是 stop_reason 高维归一化（end_turn / max_tokens / stop_sequence /
    // tool_use / refusal / unknown），与 CanonicalStopReason 对齐。
    FinishClass CanonicalStopReason `json:"finish_class,omitempty"`
    // BlockCount 是本 envelope 中 text content block 数量（P-1 校准统计）。
    BlockCount int `json:"block_count,omitempty"`
}
```

- IsStreamable: true
- RequiresNativePassthrough: false

#### 2.4.2 `ToolUseCapability`（`capability_tool_use.go`）

```go
type ToolUseCapability struct {
    CapabilityNodeBase
    // ToolNameHashAlgorithm 标注 OpenAI 64-char tool name truncation 选用算法。
    // P-0 锁定字段名；具体值（sha8 / sha12 / sha8_with_collision_detect）由 D4 在 P-3 决定。
    ToolNameHashAlgorithm string `json:"tool_name_hash_algorithm,omitempty"`
    // PreservesSignature 报告该 envelope 是否保留 thought_signature（Anthropic）。
    PreservesSignature bool `json:"preserves_signature"`
    // ParallelAllowed 是 parallel_tool_calls 与 tool_choice 联合判定结果。
    ParallelAllowed bool `json:"parallel_allowed"`
    // ToolCount 统计请求中宣告的 tool 数（含 hosted tools）。
    ToolCount int `json:"tool_count,omitempty"`
}
```

- IsStreamable: true（partial JSON deltas）
- RequiresNativePassthrough: false（hosted tools 例外路径在 §2.4.5）

#### 2.4.3 `ThinkingCapability`（`capability_thinking.go`）

```go
type ThinkingCapability struct {
    CapabilityNodeBase
    // BudgetTokens 是 Anthropic extended thinking 的 budget_tokens 原值。
    // P-0 锁字段类型；P-1 实施时禁止 bucketize（per LiteLLM trap §3）。
    BudgetTokens *int `json:"budget_tokens,omitempty"`
    // EffortHint 是 OpenAI o-series style enum (minimal/low/medium/high)；
    // 与 BudgetTokens 互斥时取 BudgetTokens 优先。
    EffortHint *string `json:"effort_hint,omitempty"`
    // EmitsSignature 报告该 envelope 是否含 thinking signature_delta。
    EmitsSignature bool `json:"emits_signature"`
    // RedactionClass 是 redaction policy（visible / summary_only / hidden / placeholder）。
    RedactionClass string `json:"redaction_class,omitempty"`
}
```

- IsStreamable: true
- RequiresNativePassthrough: false

#### 2.4.4 `CacheControlCapability`（`capability_cache_control.go`）

```go
type CacheControlCapability struct {
    CapabilityNodeBase
    // Scope 是 Anthropic prompt-caching scope（"ephemeral" / "persistent"）。
    Scope string `json:"scope,omitempty"`
    // TTL 是缓存 TTL（"5m" / "1h"）。
    TTL string `json:"ttl,omitempty"`
    // BreakpointCount 标注 envelope 中 cache_control marker 数量。
    BreakpointCount int `json:"breakpoint_count,omitempty"`
    // LocalityHint 是客户端语义标签（如 "system_prompt" / "tool_definitions" /
    // "few_shot_example"）。对接 PASR-A2 cache locality scoring。
    LocalityHint string `json:"locality_hint,omitempty"`
    // SanitizerApplied 报告 gateway-injected metadata sanitizer 是否在请求路径已运行
    //（per new-api#4678 — cch=xxx 不应进 system prompt 破坏 prefix cache）。
    SanitizerApplied bool `json:"sanitizer_applied"`
    // CacheCreationTokens / CacheReadTokens 是 vendor 报回的 token 数（如有）。
    // 与 Accounting.CacheCreationInputTokens / CacheReadInputTokens 互校。
    CacheCreationTokens *int `json:"cache_creation_tokens,omitempty"`
    CacheReadTokens     *int `json:"cache_read_tokens,omitempty"`
    // 注意：D6 决议——本 v0.4 不含跨账号复制意图字段；
    // P-8 Direction 1 启动时再扩 ReplicationIntent。
}
```

- IsStreamable: false
- RequiresNativePassthrough: false

#### 2.4.5 `StructuredOutputCapability`（`capability_structured_output.go`）

```go
type StructuredOutputCapability struct {
    CapabilityNodeBase
    // Type 是 response_format 类型（"json_schema" / "json_object" / "text"）。
    Type string `json:"type,omitempty"`
    // Schema 是 JSON Schema draft 2020-12 raw（可空）。
    Schema json.RawMessage `json:"schema,omitempty"`
    // Strict 是 OpenAI strict mode flag。Anthropic 端未原生支持 → fallback by sys prompt。
    Strict *bool `json:"strict,omitempty"`
    // Name 给 schema 命名（OpenAI 必填，Anthropic / Gemini optional）。
    Name string `json:"name,omitempty"`
    // FallbackStrategy 标注 unsupported vendor 时降级路径（"system_prompt_inject" /
    // "passthrough" / "reject"）。
    FallbackStrategy string `json:"fallback_strategy,omitempty"`
}
```

- IsStreamable: true
- RequiresNativePassthrough: false

#### 2.4.6 `ComputerUseCapability`（`capability_computer_use.go`）

```go
type ComputerUseCapability struct {
    CapabilityNodeBase
    // EnvTarget 是 sandbox 环境标识（默认 native passthrough — HUAKAI 不持 sandbox）。
    EnvTarget string `json:"env_target,omitempty"`
    // ApprovalRequired 标注是否需要用户 approval。
    ApprovalRequired bool `json:"approval_required"`
    // BetaHeaders 是 Anthropic computer-use beta headers list。
    BetaHeaders []string `json:"beta_headers,omitempty"`
    // AuditRequired 标注是否要 audit log（默认 true，per Policy.AuditMode）。
    AuditRequired bool `json:"audit_required"`
}
```

- IsStreamable: false
- RequiresNativePassthrough: **true**（Anthropic-only，HUAKAI 走 `/v1/native/anthropic/messages`）

#### 2.4.7 `FileCapability`（`capability_file.go`）

```go
type FileCapability struct {
    CapabilityNodeBase
    // SourceKind 标注文件来源（"upload_id" / "url" / "base64"）。
    SourceKind string `json:"source_kind"`
    // MediaType 是 MIME type（如 "application/pdf"）。
    MediaType string `json:"media_type"`
    // FileID 是 vendor 端文件 ID（OpenAI files / Anthropic document block）。
    FileID string `json:"file_id,omitempty"`
    // SizeBytes 是已知大小（可空）。
    SizeBytes *int64 `json:"size_bytes,omitempty"`
    // RetentionLabel 与 DataRetentionCapability.RetentionLabel 一致（5 词汇之一）。
    RetentionLabel string `json:"retention_label,omitempty"`
    // URLDigest 当 SourceKind=="url" 时存储 SHA256 摘要（避免 envelope 持 URL 本身）。
    URLDigest string `json:"url_digest,omitempty"`
}
```

- IsStreamable: false
- RequiresNativePassthrough: false（content block 路径）

#### 2.4.8 `ImageCapability`（`capability_image.go`）

```go
type ImageCapability struct {
    CapabilityNodeBase
    // Source 标注图片来源（"base64" / "url" / "vendor_ref"）。
    Source string `json:"source"`
    // MediaType 是 MIME（"image/png" / "image/jpeg" / "image/webp" 等）。
    MediaType string `json:"media_type"`
    // Width / Height 维度（如已知）。
    Width  *int `json:"width,omitempty"`
    Height *int `json:"height,omitempty"`
    // RemoteFetchPolicy 标注当 vendor 不接 URL 时的 gateway 行为
    //（"fetch_and_base64" / "passthrough_url" / "reject"）。
    RemoteFetchPolicy string `json:"remote_fetch_policy,omitempty"`
    // SizeCapBytes 是 fetch_and_base64 路径下的大小上限（默认 10 MB）。
    SizeCapBytes int64 `json:"size_cap_bytes,omitempty"`
    // VendorRef 是 vendor 端 file id（如 OpenAI file_id）。
    VendorRef string `json:"vendor_ref,omitempty"`
}
```

- IsStreamable: false
- RequiresNativePassthrough: false

#### 2.4.9 `AudioCapability`（`capability_audio.go`）

```go
type AudioCapability struct {
    CapabilityNodeBase
    // Direction 是 input / output / bidirectional。
    Direction string `json:"direction"`
    // Format 是音频格式（"mp3" / "wav" / "pcm" 等）。
    Format string `json:"format,omitempty"`
    // SampleRate Hz。
    SampleRate *int `json:"sample_rate,omitempty"`
    // TranscriptPolicy 标注是否同时返回文字（"auto" / "always" / "never"）。
    TranscriptPolicy string `json:"transcript_policy,omitempty"`
    // LiveCompatible 标注本节点是否兼容 live_session（true 时一般也加
    // LiveSessionCapability node）。
    LiveCompatible bool `json:"live_compatible"`
}
```

- IsStreamable: true（input streaming + output streaming 双向）
- RequiresNativePassthrough: false（audio gen 端点 partial native，但 schema 在 IR）

#### 2.4.10 `VideoCapability`（`capability_video.go`）

```go
type VideoCapability struct {
    CapabilityNodeBase
    // Source 标注 video 来源（"url" / "base64" / "file_ref"）。
    Source string `json:"source"`
    // MediaType / Codec 描述。
    MediaType string `json:"media_type,omitempty"`
    Codec     string `json:"codec,omitempty"`
    // TimeRange 是片段时间窗口（"PT0M-PT5M" 等 ISO8601 duration range）。
    TimeRange string `json:"time_range,omitempty"`
    // SizeBytes 大小。
    SizeBytes *int64 `json:"size_bytes,omitempty"`
    // PreferLiveStream 标注客户端期望走 live_session 而非 buffered chunk。
    PreferLiveStream bool `json:"prefer_live_stream"`
}
```

- IsStreamable: false（buffered）/ partial（Gemini Live 走 LiveSessionCapability）
- RequiresNativePassthrough: **true** for Veo / Sora generation endpoint（特殊 vendor endpoint）

#### 2.4.11 `LiveSessionCapability`（`capability_live_session.go`）

```go
type LiveSessionCapability struct {
    CapabilityNodeBase
    // VendorTarget 标注哪个 live 端点（"gemini_live" / "openai_realtime"）。
    VendorTarget string `json:"vendor_target"`
    // Modalities 是会话支持的 modality 列表（["text", "audio", "video"] 等）。
    Modalities []string `json:"modalities"`
    // ResumeToken 是 mid-session 续约 token（存在则尝试恢复）。
    ResumeToken string `json:"resume_token,omitempty"`
    // ToolAvailability 标注会话期间是否允许 tool_use（true / false）。
    ToolAvailability bool `json:"tool_availability"`
    // CloseReason 是 session 结束原因（仅 response/event 阶段填）。
    CloseReason string `json:"close_reason,omitempty"`
}
```

- IsStreamable: bidirectional WSS（特殊形态——StreamPlan.SessionMode == "websocket"）
- RequiresNativePassthrough: **true**（默认 native passthrough，HUAKAI 不重写状态机）

#### 2.4.12 `BatchCapability`（`capability_batch.go`）

```go
type BatchCapability struct {
    CapabilityNodeBase
    // BatchID 是 vendor 端 batch job ID（已提交时填）。
    BatchID string `json:"batch_id,omitempty"`
    // Endpoint 标注 batch 包含的 API endpoint（"/v1/chat/completions" 等）。
    Endpoint string `json:"endpoint"`
    // Status 是 job 状态（"queued" / "in_progress" / "completed" / "failed" / "expired"）。
    Status string `json:"status,omitempty"`
    // CompletionWindow 是请求的完成窗口（"24h"）。
    CompletionWindow string `json:"completion_window,omitempty"`
    // RequestCount / SucceededCount / FailedCount 计数。
    RequestCount   int `json:"request_count,omitempty"`
    SucceededCount int `json:"succeeded_count,omitempty"`
    FailedCount    int `json:"failed_count,omitempty"`
    // OutputFileRef / ErrorFileRef 输出引用（vendor file id）。
    OutputFileRef string `json:"output_file_ref,omitempty"`
    ErrorFileRef  string `json:"error_file_ref,omitempty"`
}
```

- IsStreamable: false（async polling）
- RequiresNativePassthrough: partial（OpenAI Batch SDK 习惯走 native）

#### 2.4.13 `MCPServerCapability`（`capability_mcp_server.go`）

```go
type MCPServerCapability struct {
    CapabilityNodeBase
    // ServerLabel 是 MCP server 标识。
    ServerLabel string `json:"server_label"`
    // Transport 是协议（"stdio" / "sse" / "http"）。
    Transport string `json:"transport"`
    // AllowedOps 是允许的 op 列表。
    AllowedOps []string `json:"allowed_ops,omitempty"`
    // ApprovalRequired 标注是否需要 client approval。
    ApprovalRequired bool `json:"approval_required"`
    // AuthRef 是引用 BackendSecurityPolicy 的 ID（不内嵌 token）。
    AuthRef string `json:"auth_ref,omitempty"`
}
```

- IsStreamable: true（events）
- RequiresNativePassthrough: false（IR 内表达；P-8 实装真桥接）

#### 2.4.14 `DataRetentionCapability`（`capability_data_retention.go`）

```go
type DataRetentionCapability struct {
    CapabilityNodeBase
    // RetentionLabel 是 D12 锁定的 5 词汇之一。
    RetentionLabel DataRetentionLabel `json:"retention_label"`
    // DisableTraining 标注是否要求 vendor 关闭训练数据使用（OpenAI store=false 等）。
    DisableTraining *bool `json:"disable_training,omitempty"`
    // RegionalAssertion 标注 vendor 端区域限制声明（如 "us-only"）。
    RegionalAssertion string `json:"regional_assertion,omitempty"`
    // ZDRProofRef 是 ZDR 验证证据引用（仅 RetentionLabel == "zdr_verified" 时必填）。
    ZDRProofRef string `json:"zdr_proof_ref,omitempty"`
    // AuditTags 是 audit log handler 链使用的 tag 列表。
    AuditTags []string `json:"audit_tags,omitempty"`
    // PIIScrubbing 是 PII 处理策略（"none" / "regex" / "ml_model"）；P-1 留接口。
    PIIScrubbing string `json:"pii_scrubbing,omitempty"`
}

// DataRetentionLabel 是 D12 决议的 5 词汇 enum。
type DataRetentionLabel string

const (
    // DataRetentionUnknown 未知或未声明。默认值；admin UI 标黄色风险。
    DataRetentionUnknown DataRetentionLabel = "unknown"

    // DataRetentionRequestStoreFalse 客户端通过 OpenAI store=false 等 flag 显式关闭。
    DataRetentionRequestStoreFalse DataRetentionLabel = "request_store_false"

    // DataRetentionProviderContractRequired vendor 合同要求（不可代客户端声明）。
    DataRetentionProviderContractRequired DataRetentionLabel = "provider_contract_required"

    // DataRetentionRegionalAsserted 区域限制声明（如 GDPR EU-only）。
    DataRetentionRegionalAsserted DataRetentionLabel = "regional_asserted"

    // DataRetentionZDRVerified ZDR 验证（必须有 Owner 提供的 vendor/account proof）。
    DataRetentionZDRVerified DataRetentionLabel = "zdr_verified"
)
```

- IsStreamable: false（policy on request）
- RequiresNativePassthrough: false

### 2.5 marshal/unmarshal dispatch

文件路径：`backend/internal/proto/capability_dispatch.go`（新增）

```go
// capabilityRegistry 是 CapabilityKind → factory 映射，用于 unmarshal 阶段从 raw JSON
// 还原具体 node 类型。
var capabilityRegistry = map[CapabilityKind]func() CapabilityNode{
    CapabilityKindText:             func() CapabilityNode { return &TextCapability{} },
    CapabilityKindToolUse:          func() CapabilityNode { return &ToolUseCapability{} },
    CapabilityKindThinking:         func() CapabilityNode { return &ThinkingCapability{} },
    CapabilityKindCacheControl:     func() CapabilityNode { return &CacheControlCapability{} },
    CapabilityKindStructuredOutput: func() CapabilityNode { return &StructuredOutputCapability{} },
    CapabilityKindComputerUse:      func() CapabilityNode { return &ComputerUseCapability{} },
    CapabilityKindFile:             func() CapabilityNode { return &FileCapability{} },
    CapabilityKindImage:            func() CapabilityNode { return &ImageCapability{} },
    CapabilityKindAudio:            func() CapabilityNode { return &AudioCapability{} },
    CapabilityKindVideo:            func() CapabilityNode { return &VideoCapability{} },
    CapabilityKindLiveSession:      func() CapabilityNode { return &LiveSessionCapability{} },
    CapabilityKindBatch:            func() CapabilityNode { return &BatchCapability{} },
    CapabilityKindMCPServer:        func() CapabilityNode { return &MCPServerCapability{} },
    CapabilityKindDataRetention:    func() CapabilityNode { return &DataRetentionCapability{} },
}

// UnmarshalCapabilityNode 从 raw JSON 还原 CapabilityNode 实例。
// 算法：
//  1. 先 unmarshal 到 capabilityKindOnly（只取 kind 字段）
//  2. 在 capabilityRegistry 找 factory
//  3. 用 factory 创建空 node 实例
//  4. 把 raw JSON 整体 unmarshal 到该实例
func UnmarshalCapabilityNode(raw json.RawMessage) (CapabilityNode, error) { /* P-0 实现 */ }
```

---

## 3. CapabilityGraph 边模型

### 3.1 设计目标

把 14 capability 之间的关系外显，让 capability matrix property test（P-5）可以根据图结构自动衍生 fixture——而不是手工列举 14 capability × 5 client × 5 upstream 的 350 cell。

### 3.2 边类型

| Edge type | 含义 | 例 |
|---|---|---|
| `provides` | A 提供 B 所需的子能力 | `live_session` provides `audio` + `video` + `text` |
| `requires` | A 启用时强制 B 也 enable | `tool_use` requires `text`（tool 调用结果通过 text 反馈） |
| `mutually_exclusive` | A 与 B 不可同时 enable | `live_session` mutually_exclusive `batch` |
| `loses` | A 在路由到无 B 支持的 vendor 时降级 | `cache_control` loses to `text`（OpenAI Chat 隐式 prefix cache 无 marker） |
| `requires_native` | A 触发 native passthrough 路径 | `computer_use` requires_native to `/v1/native/anthropic/messages` |

### 3.3 Go 类型

文件路径：`backend/internal/proto/capability_graph.go`（新增）

```go
// CapabilityGraph 是 envelope 内 capability 节点之间关系的图模型。
//
// envelope.CapabilityGraph 字段在 P-1 capability matrix 实施时填；P-0 仅锁字段名。
// 图本身不持有节点（节点在 envelope.Capabilities）；只记录边集合。
type CapabilityGraph struct {
    Edges []CapabilityEdge `json:"edges"`
}

// CapabilityEdge 是单条有向边。
type CapabilityEdge struct {
    // From / To 是边的起止 capability kind。
    From CapabilityKind `json:"from"`
    To   CapabilityKind `json:"to"`
    // EdgeType 标注边语义。
    EdgeType CapabilityEdgeType `json:"edge_type"`
    // Note 给 admin UI 显示的可读说明（"OpenAI Chat 隐式 prefix cache 无 marker" 等）。
    Note string `json:"note,omitempty"`
    // ConditionalOnVendor 限定边仅当路由到指定 upstream 时生效。
    // 空数组表示无条件。
    ConditionalOnVendor []UpstreamProtocol `json:"conditional_on_vendor,omitempty"`
}

// CapabilityEdgeType 是边类型枚举。
type CapabilityEdgeType string

const (
    EdgeProvides           CapabilityEdgeType = "provides"
    EdgeRequires           CapabilityEdgeType = "requires"
    EdgeMutuallyExclusive  CapabilityEdgeType = "mutually_exclusive"
    EdgeLoses              CapabilityEdgeType = "loses"
    EdgeRequiresNative     CapabilityEdgeType = "requires_native"
)
```

### 3.4 默认边集（P-1 末填，P-0 占位）

P-0 schema 锁定字段；P-1 capability schema 落地时根据 issue mining 证据填默认 14 节点 × N 边。预估：

- `tool_use` requires `text`
- `thinking` requires `text`
- `cache_control` loses（条件：OpenAI Chat / OpenAI Responses）
- `computer_use` requires_native（条件：所有非 Anthropic）
- `live_session` mutually_exclusive `batch`
- `live_session` provides `audio` + `video` + `text`
- `mcp_server` requires `tool_use`
- `data_retention` 是横切——envelope 中只能有一个 DataRetentionCapability

预计 12-18 条边（P-1 实施时确认）。

---

## 4. ProtocolLossEntry 一等公民结构

### 4.1 与 v0.3 ProtocolLossEntry 的关系

现有 `proto.ProtocolLossEntry`（`proto.go:37-42`）已有 4 字段（feature / direction / verdict / note）。v0.4 不破坏现 schema，但**升格为 envelope 一等公民**——任何 client/upstream adapter 检测到 lossy/unsupported 必须 emit + envelope.Losses 累加。

### 4.2 v0.4 升级字段

```go
// ProtocolLossEntry 升级为 envelope 一等公民。v0.3 字段保留 + 新增 v0.4 字段。
type ProtocolLossEntry struct {
    // ============== v0.3 已有字段（保留） ==============

    Feature   string  `json:"feature"`
    Direction string  `json:"direction"`  // 现有 4 值不变
    Verdict   Verdict `json:"verdict"`    // PRESERVED / LOSSY / UNSUPPORTED
    Note      string  `json:"note"`

    // ============== v0.4 新增字段 ==============

    // CapabilityID 是关联的 capability node kind（与 CapabilityKind 字符串一致）。
    // 让 admin UI 可以根据 capability 维度切片 loss 统计。
    CapabilityID CapabilityKind `json:"capability_id,omitempty"`

    // Vendor 是 upstream 协议族（如 anthropic / openai / gemini）。
    Vendor UpstreamProtocol `json:"vendor,omitempty"`

    // LossyAxis 标注 loss 的轴向（"semantic" / "field" / "schema" / "stream_event"）。
    // 让 capability matrix property test 可按轴汇总。
    LossyAxis string `json:"lossy_axis,omitempty"`

    // RecoveryHint 是给客户端 / operator 看的恢复建议（"use /v1/native/anthropic/messages" 等）。
    RecoveryHint string `json:"recovery_hint,omitempty"`

    // ObservedAt 是 loss 发生时间（UTC RFC3339Nano）。
    ObservedAt string `json:"observed_at,omitempty"`
}
```

### 4.3 emission 规则（schema 约定，P-1 实施）

| 场景 | 必 emit 字段 | Verdict |
|---|---|---|
| `cache_control` 路由到 OpenAI Chat | CapabilityID=cache_control / Vendor=openai / LossyAxis=semantic / RecoveryHint=route_to_anthropic | LOSSY |
| `computer_use` 路由到非 Anthropic | CapabilityID=computer_use / RecoveryHint=use_native_passthrough_anthropic | UNSUPPORTED |
| `tool_use` name > 64 chars + SHA-8 collision | CapabilityID=tool_use / LossyAxis=field / Note=collision_detected_falling_back_sha12 | LOSSY |
| upstream stream ended without terminal event（issue sub2api#1552） | CapabilityID=text / LossyAxis=stream_event / Note=forced_sentinel_completion | LOSSY |
| Gemini cachedContentTokenCount 缺位 PASR observe | CapabilityID=cache_control / Vendor=gemini / LossyAxis=semantic / Note=gemini_cache_observation_skipped | LOSSY |
| Anthropic stop_reason 未知值落 unknown | CapabilityID=text / LossyAxis=field / Note=stop_reason_fallback_unknown | LOSSY |

### 4.4 不许 silent drop 的运行时校验

Schema 约束（P-5 property test 验证）：
- 任何 `Verdict ∈ {LOSSY, UNSUPPORTED}` 必须有对应 envelope.Losses entry
- 任何 envelope.Losses entry 必须含 CapabilityID 或 Note 至少之一（不允许只填 Feature 字符串）
- envelope marshal/unmarshal round-trip 后 Losses 数量与内容一致

---

## 5. ProviderProjection / StreamPlan / Accounting / Policy 子结构

### 5.1 ProviderProjection（`provider_projection.go`）

```go
// ProviderProjection 是 envelope 在 router/pool 决议后，针对具体 vendor 的投影结果。
// envelope 主体不绑定 vendor；切换 vendor 仅替换 ProviderProjection。
type ProviderProjection struct {
    // ProtocolFamily 是 19 family 之一（与 forwarder.go ResolvedModel.ProtocolFamily 对齐）。
    ProtocolFamily string `json:"protocol_family"`

    // Vendor 是 upstream protocol enum（与 capability_matrix.go UpstreamProtocol 对齐）。
    Vendor UpstreamProtocol `json:"vendor"`

    // VendorModelName 是 vendor 端的真实 model 名（如 "claude-3-5-sonnet-20241022"）。
    VendorModelName string `json:"vendor_model_name"`

    // AccountID 是 PASR pool 选中的 account 引用 ID（不内嵌 token）。
    AccountID string `json:"account_id,omitempty"`

    // TenantID 是请求所属 tenant（与 PASR cache locality 三全键对齐）。
    TenantID string `json:"tenant_id,omitempty"`

    // SegmentID 是 PASR segment ID（如有，用于 cache locality 评分）。
    SegmentID string `json:"segment_id,omitempty"`

    // PrefixHash 是 cache locality 的 prefix 哈希（与 cachemetrics 三全键一致）。
    PrefixHash string `json:"prefix_hash,omitempty"`

    // SelectedAt 是路由决议时间。
    SelectedAt string `json:"selected_at,omitempty"`

    // FallbackChain 是已经尝试过的 (vendor, account) 序列（mid-stream fallback 后填）。
    // P-8 mid-stream fallback continuation 实装时使用。
    FallbackChain []FallbackHop `json:"fallback_chain,omitempty"`
}

// FallbackHop 是 fallback 链的单跳。
type FallbackHop struct {
    Vendor    UpstreamProtocol `json:"vendor"`
    AccountID string           `json:"account_id"`
    Reason    string           `json:"reason"`     // "rate_limit" / "5xx" / "stream_drop" 等
    AtTime    string           `json:"at_time"`
}
```

### 5.2 StreamPlan（`stream_plan.go`）

```go
// StreamPlan 是 streaming 路径的计划结构。
// envelope.StreamPlan 在 EnvelopeKindRequest / EnvelopeKindEvent 时使用。
type StreamPlan struct {
    // Stream 是否流式（与 CanonicalRequest.Stream 一致；envelope 顶层 mirror 便于查询）。
    Stream bool `json:"stream"`

    // SessionMode 是流形态（"sse" / "binary_eventstream" / "websocket" / "buffered"）。
    SessionMode string `json:"session_mode"`

    // SentinelEnforce 标注是否启用 sentinel 强制补全（issue sub2api#1552 / new-api#4697）。
    // true 时 forwarder Phase D drain 必须确保 content_block_stop + message_delta + message_stop 三 sentinel 齐全。
    SentinelEnforce bool `json:"sentinel_enforce"`

    // MidStreamFallbackPolicy 标注中段失败策略（D7/D9 留接口）。
    // 当前 v0.4 默认 "no_retry"；P-8 启动 mid-stream fallback continuation prompt synthesis 时改 "continuation_synthesis"。
    MidStreamFallbackPolicy string `json:"mid_stream_fallback_policy"`

    // ResumeStrategy 标注 mid-stream resume 策略（"none" / "from_last_sentinel" / "from_last_block_index"）。
    ResumeStrategy string `json:"resume_strategy,omitempty"`

    // BlockIndexCheckpoint 是 cross-attempt streaming continuity 用的 last emitted block index。
    // P-2 ClientAdapter 实施时填；P-0 锁字段名。
    BlockIndexCheckpoint *int `json:"block_index_checkpoint,omitempty"`

    // BufferCapBytes 是 stream scanner buffer 上限（默认 64 MiB，与 event_scanner.go 对齐）。
    BufferCapBytes int64 `json:"buffer_cap_bytes,omitempty"`
}
```

### 5.3 Accounting（`accounting.go`）

```go
// Accounting 是 usage / cost / cache / 失败语义计量结果。
// 仅在 Kind ∈ {Response, Event-final, Batch} 时有意义。
type Accounting struct {
    // Usage 是与 CanonicalUsage 对应的 token 计数（input / output / total + cache 三字段）。
    Usage CanonicalUsage `json:"usage"`

    // FailureClass 是 4-状态失败语义（per new-api#4168 修复点）。
    // 空字符串表示成功。
    FailureClass FailureClass `json:"failure_class,omitempty"`

    // BillingMultiplier 标注 cache_read tokens 应用的倍率（per sub2api#2293 修复点）。
    BillingMultiplier float64 `json:"billing_multiplier,omitempty"`

    // EstimatedCostUSDMicros 是 estimated 成本（micro-USD 整数）。P-7 spend metric 用。
    EstimatedCostUSDMicros int64 `json:"estimated_cost_usd_micros,omitempty"`

    // CacheHitRate 是本 envelope 观察到的 cache 命中率（cache_read / (cache_read + cache_creation)）。
    CacheHitRate float64 `json:"cache_hit_rate,omitempty"`

    // ToolCallCount 统计 tool_use block 数量（P-5 property test 用）。
    ToolCallCount int `json:"tool_call_count,omitempty"`
}

// FailureClass 是 4-状态失败语义。
type FailureClass string

const (
    FailureClassClientGone       FailureClass = "client_gone"        // downstream socket 提前断
    FailureClassUpstreamTimeout  FailureClass = "upstream_timeout"   // upstream 卡住或超时
    FailureClassUpstream5xx      FailureClass = "upstream_5xx"       // upstream 5xx 响应
    FailureClassOutputTokenZero  FailureClass = "output_token_zero"  // 上游成功但 0 输出 token
)
```

### 5.4 Policy（`policy.go`）

```go
// Policy 是数据保留 / 审计 / fingerprint 脱敏 / safety 策略合成结果。
// 由客户端请求 + tenant default + admin policy 三层合并产生。
type Policy struct {
    // DataRetention 是 D12 决议的 5 词汇 enum（与 DataRetentionCapability.RetentionLabel 一致）。
    // envelope 顶层 mirror 让 P-7 dashboard 可快速 group_by。
    DataRetention DataRetentionLabel `json:"data_retention"`

    // AuditMode 标注 audit log 写入模式（"sample" / "full" / "redacted_only"）。
    AuditMode string `json:"audit_mode,omitempty"`

    // AuditTags 是 audit log handler 链的 tag 列表。
    AuditTags []string `json:"audit_tags,omitempty"`

    // FingerprintSanitize 标注是否对 client fingerprint 字段（device_id / platform / shell /
    // process metrics 等 40+ 字段，per sub2api#1451）做规范化。
    // 默认 true（HUAKAI 对中文中转主路径开启）。
    FingerprintSanitize bool `json:"fingerprint_sanitize"`

    // PIIScrubbing 是 PII 处理策略（与 DataRetentionCapability.PIIScrubbing 镜像）。
    PIIScrubbing string `json:"pii_scrubbing,omitempty"`

    // GatewayInjectedHeaderSanitize 标注是否清除 gateway 自注入的动态 header
    //（per new-api#4678 — cch=xxx 破坏 prefix cache）。
    GatewayInjectedHeaderSanitize bool `json:"gateway_injected_header_sanitize"`

    // AuthMode 标注 native passthrough 路径的鉴权模式（D5 锁定 "standard"）。
    // 仅当 envelope.Kind == EnvelopeKindNativePassthrough 时有意义。
    AuthMode string `json:"auth_mode,omitempty"`
}
```

### 5.5 BatchPayload + NativePassthroughPayload（占位 schema）

```go
// BatchPayload 是 EnvelopeKindBatch 的载荷。
// P-0 锁字段；P-1 完整；P-4 真接 vendor batch endpoint。
type BatchPayload struct {
    // Submission 是 batch 提交侧（含 capability=batch 描述的 input file 等）。
    Submission *BatchSubmission `json:"submission,omitempty"`

    // Status 是 polling 侧的状态信息（与 BatchCapability.Status 一致）。
    Status *BatchStatus `json:"status,omitempty"`
}

type BatchSubmission struct {
    // Endpoint 是批处理目标 endpoint（"/v1/chat/completions" 等）。
    Endpoint string `json:"endpoint"`
    // RequestsRef 是批处理 input file 引用（vendor file_id）。
    RequestsRef string `json:"requests_ref"`
    // Metadata 客户提供的 metadata。
    Metadata map[string]string `json:"metadata,omitempty"`
}

type BatchStatus struct {
    // BatchID / Status / OutputFileRef / ErrorFileRef 与 BatchCapability mirror。
    BatchID       string `json:"batch_id"`
    Status        string `json:"status"`
    OutputFileRef string `json:"output_file_ref,omitempty"`
    ErrorFileRef  string `json:"error_file_ref,omitempty"`
}

// NativePassthroughPayload 是 EnvelopeKindNativePassthrough 的载荷。
// /v1/native/<vendor>/<endpoint> 直连 vendor，不做 capability normalization。
type NativePassthroughPayload struct {
    // Vendor 是目标 vendor。
    Vendor UpstreamProtocol `json:"vendor"`
    // Endpoint 是 vendor 端 endpoint path（"/v1/messages" / "/realtime" 等）。
    Endpoint string `json:"endpoint"`
    // Method 是 HTTP method（GET/POST/PUT...）。
    Method string `json:"method"`
    // RawRequestRef 是 raw request body 引用（不内嵌 raw bytes — envelope 为内存 IR，
    // raw 在 forwarder.ForwardRequest.Body 域）。
    RawRequestRef string `json:"raw_request_ref"`
    // BetaHeaders 是 vendor beta header 列表（如 Anthropic computer-use beta）。
    BetaHeaders []string `json:"beta_headers,omitempty"`
    // CapabilityLossReason 是固定值 "native_passthrough_full_vendor_risk"，让 audit 显式可见
    //（per D5 — admin UI 必须明确标 native = 包括 vendor schema 改动风险）。
    CapabilityLossReason string `json:"capability_loss_reason"`
}
```

---

## 6. 命名空间清理

### 6.1 现有命名（v0.3）

| 类型 | 文件:行 | 状态 |
|---|---|---|
| `proto.HCSF` | `proto.go:15-18` | **空 struct，待删除** |
| `proto.ClientAdapter` | `proto.go:21-26` | 接口存在，全仓 0 实现 |
| `proto.UpstreamAdapter` | `proto.go:29-34` | 接口存在，4 vendor 实现（Phase B 全 ErrNotImplemented） |
| `proto.ProtocolLossEntry` | `proto.go:37-42` | 4 字段实存 |
| `proto.Verdict` | `proto.go:45-51` | 3 值 enum |
| `proto.Direction` | `proto.go:54-61` | 4 值 enum |
| `proto.CanonicalRequest` 等 7 类型 | `hcsf.go` | typed struct 完整 |
| `proto.UpstreamState` | 接口（forwarder.go:337-351 type-switch 实例化） | per-vendor 独立 state struct |
| `proto.ContentBlock` | 不存在（实际是 `CanonicalContentBlock`） | n/a |

### 6.2 v0.4 改动

| 改动 | 操作 | 兼容路径 |
|---|---|---|
| 删除 `proto.HCSF struct{}` 空壳 | 删除 + 加 `type HCSF = HCSFEnvelope` 临时 alias（P-1 进入时清理） | alias 期间编译通过 |
| 引入 `proto.HCSFEnvelope` | 新增 `envelope.go` 文件 | n/a |
| `ClientAdapter` 接口签名改 `*HCSF → *HCSFEnvelope` | 改接口 + 4 vendor adapter 当前类型签名 | alias 让中间状态可编译 |
| `UpstreamAdapter` 接口签名同上 | 同上 | 同上 |
| `ProtocolLossEntry` 加 5 新字段 | 字段加在末尾，不动现有 4 字段 | v0.3 marshal 出的 JSON 仍可 v0.4 unmarshal |
| `CanonicalRequest / Response / Event` 不动 | 仅升格为 envelope 子字段；类型本身 0 改 | 全兼容 |
| `proto.UpstreamState` 接口不动 | per-vendor state 不进 envelope（边界约束 §1.4） | n/a |
| `CanonicalContentBlock.Type` 字段 typed enum 化 | 当前 `Type string`，改 `Type ContentBlockType`（带枚举常量） | TextType / ToolUseType / ToolResultType / ImageType / ReasoningSummaryType / CacheBreakpointType / SignatureDeltaType 等 |

### 6.3 ContentBlockType 枚举（小型必做）

```go
// ContentBlockType 是 CanonicalContentBlock.Type 字段的 typed enum。
// 取代当前 hcsf.go:44 字符串字段。
type ContentBlockType string

const (
    ContentBlockText             ContentBlockType = "text"
    ContentBlockToolUse          ContentBlockType = "tool_use"
    ContentBlockToolResult       ContentBlockType = "tool_result"
    ContentBlockImage            ContentBlockType = "image"
    ContentBlockReasoningSummary ContentBlockType = "reasoning_summary"
    ContentBlockCacheBreakpoint  ContentBlockType = "cache_breakpoint"
    ContentBlockSignatureDelta   ContentBlockType = "signature_delta"
    ContentBlockFile             ContentBlockType = "file"
    ContentBlockAudio            ContentBlockType = "audio"
    ContentBlockVideo            ContentBlockType = "video"
)
```

### 6.4 包结构

P-0 不引入子包，所有新类型平铺到 `backend/internal/proto/` 包：

```
backend/internal/proto/
├── envelope.go              [新] HCSFEnvelope + EnvelopeKind + 常量
├── capability_node.go       [新] CapabilityNode interface + Base + Kind enum + DeclaredSource
├── capability_dispatch.go   [新] capabilityRegistry + UnmarshalCapabilityNode
├── capability_text.go       [新] TextCapability
├── capability_tool_use.go   [新] ToolUseCapability
├── capability_thinking.go   [新] ThinkingCapability
├── capability_cache_control.go [新] CacheControlCapability
├── capability_structured_output.go [新] StructuredOutputCapability
├── capability_computer_use.go [新] ComputerUseCapability
├── capability_file.go       [新] FileCapability
├── capability_image.go      [新] ImageCapability
├── capability_audio.go      [新] AudioCapability
├── capability_video.go      [新] VideoCapability
├── capability_live_session.go [新] LiveSessionCapability
├── capability_batch.go      [新] BatchCapability
├── capability_mcp_server.go [新] MCPServerCapability
├── capability_data_retention.go [新] DataRetentionCapability + DataRetentionLabel
├── capability_graph.go      [新] CapabilityGraph + CapabilityEdge + EdgeType
├── provider_projection.go   [新] ProviderProjection + FallbackHop
├── stream_plan.go           [新] StreamPlan
├── accounting.go            [新] Accounting + FailureClass
├── policy.go                [新] Policy
├── batch_payload.go         [新] BatchPayload + BatchSubmission + BatchStatus
├── native_passthrough_payload.go [新] NativePassthroughPayload
├── content_block_type.go    [新] ContentBlockType enum + 常量
├── proto.go                 [改] 删 HCSF struct{} + 加 HCSF alias + 接口签名改 *HCSFEnvelope + ProtocolLossEntry 加 5 字段
├── hcsf.go                  [改] CanonicalContentBlock.Type 改 ContentBlockType
├── capability_matrix.go     [改] FeatureName enum 保留（v0.3 兼容）；P-1 与 14 capability 对齐
├── field_matrix.go          [不动]
├── passthrough.go           [不动]
├── tool_call_id.go          [不动]
├── anthropic_sse.go         [改] *HCSF → *HCSFEnvelope（alias 期可不动；P-1 末整理）
├── openai_sse.go            [改] 同上
├── gemini_sse.go            [改] 同上
└── bedrock_eventstream.go   [改] 同上
```

新增 ~25 文件 + 改 ~6 文件。LoC 估算：
- envelope.go ~120 LoC（含注释）
- capability_node.go + capability_dispatch.go ~150 LoC
- 14 capability_*.go 文件平均 ~50 LoC = ~700 LoC
- capability_graph.go ~80 LoC
- 4 子结构文件 ~250 LoC
- 2 payload + 1 ContentBlockType ~120 LoC
- proto.go 改动 ~50 LoC
- 共 **~1500 LoC P-0 schema 代码 + 测试 fixture**

---

## 7. 兼容性迁移路径

### 7.1 v0.3 → v0.4 迁移阶段

| 阶段 | 操作 | 时间 | 兼容状态 |
|---|---|---|---|
| **P-0 Day 1** | 删除 `HCSF struct{}` + 加 `type HCSF = HCSFEnvelope` alias + 新增 envelope.go | Day 1 | 现有代码编译通过（alias 路径） |
| **P-0 Day 2-3** | 14 capability node + CapabilityGraph + 4 子结构 + 2 payload + ContentBlockType + ProtocolLossEntry 5 新字段 | Day 2-3 | 现有 4 vendor adapter 不变（envelope 字段全 optional） |
| **P-0 Day 4** | unit test：envelope JSON round-trip / 14 node unmarshal dispatch / ContentBlockType enum 安全性 | Day 4 | n/a |
| **P-0 Day 5** | mock-implement 14 capability schema 占位 fixture + property test 框架 | Day 5 | n/a |
| **P-1 Day 1-2** | 删除 `HCSF` alias；4 vendor adapter 类型签名 `*HCSF → *HCSFEnvelope` | P-1 Day 1-2 | breaking change but P-1 owner-approved |
| **P-1 Day 3+** | capability schema 实落 + capability matrix 依据 graph 改造 | P-1 Day 3+ | n/a |

### 7.2 v0.5+ forward-compat 设计

虽然 D3 决议 v0.4 不做 schema migration，**但 envelope 设计已经支持 v0.5 forward-compat**：

- `Version` 字段未来对应 `0.5` / `0.6` 等
- `Reserved map[string]json.RawMessage` 字段为后续未规划字段留位
- ProtocolLossEntry 可继续向末尾追加字段不破坏现有
- CapabilityKind 可注册新 kind 而老 envelope 仍可解
- `EnvelopeKind` 枚举可以追加（如 `EnvelopeKindWebhook`）

### 7.3 持久化 path（D1/D3 决议为否）

**不实施**。但 schema 设计已经允许未来开启持久化：
- envelope.RequestID 是合规主键候选
- envelope.Version + Reserved 可承担未来 schema migration
- envelope 结构 JSON-clean（无 channel / context / func 字段），future marshal 进 PG `JSONB` 字段成本可控

---

## 8. JSON Round-Trip 不变量

### 8.1 不变量定义

定义集合（fixture 验证 + property test 验证 P-1 末完成）：

| ID | 不变量 | 验证方式 |
|---|---|---|
| **INV-1** | envelope marshal → unmarshal 后所有 typed 字段值不变 | 标准 round-trip equality |
| **INV-2** | PassthroughEnvelope.Extra 字段在 envelope 中 round-trip 后保持顺序无关、值相等 | go-cmp deep equal |
| **INV-3** | envelope.Losses 数量 round-trip 不变 | len 比较 |
| **INV-4** | 每条 ProtocolLossEntry 的 5 关键字段（Feature / Direction / Verdict / Note / CapabilityID）round-trip 不变 | 按字段比较 |
| **INV-5** | envelope.Capabilities[] 中每个 node 的 CapabilityKind 与具体类型严格匹配（不允许 ToolUseCapability 装在 capability_text kind 下） | 类型断言 + reflect |
| **INV-6** | envelope.CapabilityGraph.Edges 顺序不变（slice 顺序敏感） | sliceEq |
| **INV-7** | StreamPlan 三全字段（Stream / SessionMode / SentinelEnforce）round-trip 必须存在；nil StreamPlan 与 zero-value StreamPlan 区分 | nil-check |
| **INV-8** | Accounting.Usage 中 5 token 计数（input / output / total / cache_creation / cache_read）round-trip 严格相等 | int 比较 |
| **INV-9** | Policy.DataRetention 必须是 D12 五词汇之一；任何其他值 unmarshal 失败 | enum 校验 |
| **INV-10** | EnvelopeKind 与五选一载荷（Request / Response / Event / Batch / NativePassthrough）的 active 字段一致；Kind=Request 但 Response 非空 → unmarshal 时 emit ProtocolLossEntry note=kind_payload_mismatch | dispatch 校验 |
| **INV-11** | ContentBlockType enum 不变；未知 type 字符串 unmarshal 后落到 "unknown" 枚举成员（不报错——保 forward compat） | enum 校验 |
| **INV-12** | envelope.Reserved map 中字段 round-trip 后键集合 + 值字节相等 | byte-level compare |

### 8.2 fixture-driven property test 雏形

```go
// envelope_roundtrip_test.go 雏形
func TestHCSFEnvelopeRoundTrip(t *testing.T) {
    cases := []string{
        "tests/fixtures/hcsf/v0.4/text_only_request.json",
        "tests/fixtures/hcsf/v0.4/tool_use_with_thinking.json",
        "tests/fixtures/hcsf/v0.4/cache_control_anthropic.json",
        "tests/fixtures/hcsf/v0.4/native_passthrough_computer_use.json",
        "tests/fixtures/hcsf/v0.4/live_session_gemini_live.json",
        // ... 详见 §9
    }
    for _, file := range cases {
        raw := loadFile(file)
        var env1 HCSFEnvelope
        require.NoError(t, json.Unmarshal(raw, &env1))
        marshaled, err := json.Marshal(env1)
        require.NoError(t, err)
        var env2 HCSFEnvelope
        require.NoError(t, json.Unmarshal(marshaled, &env2))
        require.Empty(t, cmp.Diff(env1, env2)) // INV-1..12 全验
    }
}
```

---

## 9. 测试 fixture 要求

### 9.1 fixture 目录结构

```
tests/fixtures/hcsf/v0.4/
├── envelope/
│   ├── request_text_only.json
│   ├── request_tool_use_streaming.json
│   ├── request_thinking_with_signature.json
│   ├── request_cache_control_anthropic.json
│   ├── request_structured_output_strict.json
│   ├── request_image_base64.json
│   ├── request_image_url_remote_fetch.json
│   ├── request_audio_input.json
│   ├── request_video_chunk.json
│   ├── request_file_pdf.json
│   ├── request_native_passthrough_computer_use.json
│   ├── request_live_session_gemini.json
│   ├── request_batch_submit.json
│   ├── request_mcp_server.json
│   └── request_data_retention_zdr.json
├── response/
│   ├── response_text_buffered.json
│   ├── response_tool_use_with_args.json
│   ├── response_with_losses.json
│   └── response_cache_hit_metrics.json
├── event/
│   ├── event_text_block_delta.json
│   ├── event_tool_use_partial_json.json
│   ├── event_thinking_signature.json
│   └── event_finalize_with_sentinel.json
└── regression/
    ├── new_api_4678_cch_sanitization.json   [issue regression]
    ├── sub2api_1552_no_terminal_event.json
    ├── portkey_1579_cache_control_strip.json
    ├── litellm_27468_tool_args_lost.json
    └── new_api_4697_anthropic_sentinel_missing.json
```

### 9.2 必要 fixture 数量

- **14 capability node × ≥1 fixture = 14 minimum**（覆盖 envelope/ 子目录）
- **3 client × 5 upstream = 15 pair × ≥1 minimal fixture = 15** (cross 矩阵)
- **5 issue regression fixture = 5** (regression/ 子目录)
- **总：≥34 fixture file**

### 9.3 fixture 格式约束

每个 fixture：
- 必须含 `hcsf_version: "0.4"`
- 必须含 `kind` 字段
- 必须含 `request_id`（可以是固定值如 `req_test_001`）
- 必须含 `created_at`
- 必须含 `client_protocol`
- envelope.Capabilities[] 中所有 node 必须 dispatch unmarshal 成功
- 任何 LOSSY/UNSUPPORTED verdict 在 envelope.Losses 中存在
- 整体 round-trip 通过 INV-1..12 校验

### 9.4 fixture 生成约束

- **手写优先**——P-0 不引入 fixture generator（避免与 P-5 property test 框架重复）
- fixture 命名清晰反映场景（不允许 `test_001.json` 这种无语义命名）
- 每个 fixture 顶部加 JSON 注释（Go 解析 JSON 不支持注释，但可以加 `_comment` 字段）说明场景

### 9.5 issue regression fixture 内容约定

| Fixture file | issue ref | 期望验证 |
|---|---|---|
| `new_api_4678_cch_sanitization.json` | new-api#4678 | envelope.Policy.GatewayInjectedHeaderSanitize == true，且 envelope 不含 cch=xxx 类动态 header；CacheControlCapability.SanitizerApplied == true |
| `sub2api_1552_no_terminal_event.json` | sub2api#1552 | StreamPlan.SentinelEnforce == true；envelope.Losses 中含 LossyAxis=stream_event + Note=forced_sentinel_completion |
| `portkey_1579_cache_control_strip.json` | Portkey#1579 | 路由到 Vertex Anthropic 时 CacheControlCapability 仍存在；envelope.Losses 中含 CapabilityID=cache_control + RecoveryHint=route_to_anthropic_direct |
| `litellm_27468_tool_args_lost.json` | LiteLLM#27468 | OpenAI→Anthropic round-trip 后 ToolUseCapability.ToolCount 不变；ContentBlock 中 input json.RawMessage byte-level 等价 |
| `new_api_4697_anthropic_sentinel_missing.json` | new-api#4697 | qwen3 sim envelope 终态含 message_stop event；缺失时 Losses 中 LossyAxis=stream_event |

---

## 10. 推迟决策点 (D4 / D9 / D10) 的 schema 留空间

D4 / D9 / D10 是 implementation-claude.md 第 8 节列出的 8 个 DECISION-POINT 中由 PM 拍板的推迟项；P-0 schema 必须为这些决策预留位置而不锁死。

### 10.1 D4 — Tool name SHA-truncation 算法（P-3 决定）

**留位**：`ToolUseCapability.ToolNameHashAlgorithm` 字段（§2.4.2）。

- 字段类型 `string`，候选值 `"sha8"` / `"sha12"` / `"sha8_with_collision_detect"`。
- P-0 锁字段名；P-3 实施时根据 vendor 实测 collision rate 选值。
- envelope 不内嵌真实算法常量到 schema enum——保留为字符串便于未来加 `"sha16"` 等扩展。

### 10.2 D9 — Mid-stream fallback 范围（P-8 实施）

**留位**：`StreamPlan.MidStreamFallbackPolicy` + `StreamPlan.ResumeStrategy` + `StreamPlan.BlockIndexCheckpoint` + `ProviderProjection.FallbackChain[]` 四字段（§5.1 + §5.2）。

- v0.4 默认 `MidStreamFallbackPolicy="no_retry"`；P-8 启动 mid-stream fallback continuation prompt synthesis 时改 `"continuation_synthesis"`。
- `BlockIndexCheckpoint` 是 cross-attempt streaming continuity 的 last emitted block index；P-2 ClientAdapter 留写入接口；P-8 真使用。
- `FallbackChain[]` 累计已尝试 vendor 序列；P-8 mid-stream fallback 真消费。

### 10.3 D10 — Capability matrix cell 数（P-5 实施）

**留位**：`CapabilityGraph.Edges[]` 数量不锁定（§3.3）。

- P-1 末填默认 12-18 条 edge。
- P-5 property test 根据图衍生 cell 数：每个 node × 5 client × 5 upstream = 350 maximum；按 graph 边过滤后约 80 cell（与 implementation-claude.md §4.2 对齐）。
- envelope schema 不限制 cell 数——纯由 capability matrix property test runner 决定。

### 10.4 D8 / D13 / D14 — 其他延迟决策

| Decision | 留位字段 | 阶段 |
|---|---|---|
| D8 P-7 spend dashboard 数字来源标记 | `Accounting.EstimatedCostUSDMicros` + 未来 `Accounting.SourceTag string` (P-7 加) | P-7 |
| D13 P-5 release gate 阈值 | `CapabilityGraph` + property test runner 配置（envelope 不锁） | P-5 |
| D14 测试依赖 | fixture file 路径约定（§9.1）；envelope schema 不锁 | P-5 |

---

## 风险与盲点

### R1: envelope 顶层信封是 v0.4 新增——可能引入 4 vendor adapter 适配层 churn

- **风险**: 现有 anthropic_sse / openai_sse / gemini_sse / bedrock_eventstream 4 个 adapter 直接接受 `*HCSF` 形参；改 `*HCSFEnvelope` 是接口签名变更，即便 alias 让编译通过，runtime 行为可能微差。
- **mitigations**: P-0 Day 1 仅加 alias；P-1 Day 1-2 才删 alias 改签名；alias 期间 4 vendor adapter 0 改动，给 P-0 留充分时间验证 envelope schema。

### R2: 14 capability node 平铺到 `proto/` 包文件数过多

- **风险**: 25 个新文件全在 `proto/` 包，包级 namespace 拥挤，未来若想拆子包成本增加。
- **mitigations**: P-0 接受平铺；P-1 末做 schema review 时决议是否拆 `proto/capability/` 子包（决策窗口在 P-1，不影响 P-0）。

### R3: `Reserved map[string]json.RawMessage` 字段可能被滥用为绕过 schema review 的"随便加东西"通道

- **风险**: 后续 P-1 实施者把"还没想清楚"的字段直接放 Reserved，schema 永远不收敛。
- **mitigations**: 在 envelope.go Reserved 字段注释中明确写"P-1 末必做 schema review，决定是否升为正式字段"；P-1 Owner gate 包括 Reserved 字段清理。

### R4: ProtocolLossEntry 5 新字段对 v0.3 序列化向后兼容，但 v0.3 序列化器不会填新字段

- **风险**: v0.3 → v0.4 mixed 部署期间，v0.3 envelope 反序列化为 v0.4 时 CapabilityID/Vendor/LossyAxis 等字段为零值，admin UI 显示空。
- **mitigations**: v0.3 → v0.4 不混部署（D1/D3 决议 not in DB；envelope 仅内存，进程重启即纯 v0.4）。

### R5: `EnvelopeKind` 与五选一载荷的一致性约束（INV-10）由代码强制——schema 本身不强制

- **风险**: schema 允许 EnvelopeKind=Request 同时 Response 非空，runtime 检查不到位会有不一致 envelope。
- **mitigations**: P-0 Day 4 unit test 全覆盖 INV-10；envelope.go 加 `Validate() error` 方法在 marshal/unmarshal 路径强制调用。

### R6: 14 capability node 与现有 `FeatureName` enum（capability_matrix.go:31-49 / 15 features）有重叠但语义不同

- **风险**: `FeatureName.FeatureToolUse` 与 `CapabilityKind.CapabilityKindToolUse` 字符串值不同；现有 `DefaultMatrix()` 用 FeatureName，P-1 用 CapabilityKind。
- **mitigations**: P-0 schema 保留 FeatureName enum；P-1 实施 capability matrix 时显式写 mapping `FeatureName ↔ CapabilityKind`，避免代码两套并存。

### R7: D6 不含跨账号复制意图——但 PASR-A2 已落 cache locality scoring；schema 字段缺失会让 PASR feedback loop 缺数据

- **风险**: PASR-A2 score blend 需要 envelope 提供 LocalityHint；CacheControlCapability.LocalityHint 字段已留位，但 PASR Direction 1 推到 P-8。
- **mitigations**: LocalityHint 字段在 v0.4 即可填 + PASR-A2 已可读；只是"跨账号复制意图"动作（pre-warm replicate）推到 P-8。schema 已支持，行为推迟。

### R8: 现有 hcsf.go 中 ToolChoice 是 `any` 类型——P-0 schema 未强 typed union

- **风险**: 现状 `CanonicalRequest.ToolChoice any`（hcsf.go:18 / 实际是 hcsf.go 第 17 行附近）让 runtime 形态不确定；P-0 schema 未做 typed union 改造（implementation-claude.md §1.2 提到的 typed union 是 P-0 工作）。
- **mitigations**: P-0 实施时 ToolChoice typed union 是 implementation-claude.md §1.2 锁定项；本 spec 未单独列入主 schema 是因为它是 CanonicalRequest 内部演进，不影响 envelope 顶层 schema。P-1 capability schema 完成时再校验 ToolUseCapability 与 ToolChoice typed union 协同。

### R9: capability_dispatch.go 的 capabilityRegistry 是全局变量

- **风险**: 全局可变 map 在并发 register/lookup 路径有 race；测试也可能误注册。
- **mitigations**: P-0 实施时 capabilityRegistry 在 init() 一次性初始化；不暴露 Register API；测试需要新 capability 时用 mock + dependency injection。

### R10: 本 spec 是 sonnet (general-purpose subagent) 单 lane 起草——未与 codex lane 比对

- **风险**: 与 codex lane 草案存在隐性矛盾，需要 PM 合成。
- **mitigations**: 按 CLAUDE.md #10 合成流程，PM-orchestrator 在 codex lane 完成后做 synthesis。本 lane 已严格不读 codex lane（per 任务要求）。

---

## Source citations

本 spec 全程为 HUAKAI internal 工作（CLAUDE.md #12 exempt）；所有 file:line 引用均为 HUAKAI 仓库内部：

| 引用 | 文件 | 用途 |
|---|---|---|
| `proto.HCSF struct{}` 空壳 | `backend/internal/proto/proto.go:13-18` | §1.3 / §6.1 待删依据 |
| ClientAdapter 接口 | `backend/internal/proto/proto.go:21-26` | §6.1 接口签名改动 |
| UpstreamAdapter 接口 | `backend/internal/proto/proto.go:29-34` | §6.1 接口签名改动 |
| ProtocolLossEntry v0.3 4 字段 | `backend/internal/proto/proto.go:37-42` | §4.1 v0.4 升级基础 |
| Verdict / Direction enum | `backend/internal/proto/proto.go:45-61` | §4.2 Verdict 沿用 |
| CanonicalRequest 等 7 类型 | `backend/internal/proto/hcsf.go` 全文 | §1.4 envelope 子字段 |
| CanonicalContentBlock.Type 字符串字段 | `backend/internal/proto/hcsf.go:44` | §6.3 ContentBlockType enum 化 |
| ClientProtocol / UpstreamProtocol enum | `backend/internal/proto/capability_matrix.go:11-28` | §1.2 envelope.ClientProtocol |
| FeatureName enum 15 值 | `backend/internal/proto/capability_matrix.go:31-49` | §R6 FeatureName ↔ CapabilityKind 映射 |
| DefaultMatrix() 粗粒度规则 | `backend/internal/proto/capability_matrix.go:73-94` | §R6 P-1 改造点 |
| FieldVerdict / FieldTransformKind | `backend/internal/proto/field_matrix.go:24-52` | §4.3 ProtocolLossEntry vs FieldMatrix 关系参照 |
| PassthroughEnvelope 原型 | `backend/internal/proto/passthrough.go:42-46` | §1.4 / §8.1 INV-2 Reserved vs Extra 区分 |
| anthropic_sse forwarder 入口 | `backend/internal/proto/anthropic_sse.go:138-181` | §2.4.1 6 事件依据 |
| HUAKAI axis-3 现状 5 红线 | `docs/research/2026-05-09-axis3-huakai-current-state.md` Q3 + Top 5 缺口 | §1.1 设计原则 + §6.1 现状 |
| 14 capability synthesis | `docs/process/plans/2026-05-09-hcsf-v04-implementation-synthesis.md` §2 | §2.2 capability 一览 |
| issue mining new-api#4678 / sub2api#1552 等 | `docs/research/2026-05-09-issue-mining-cross-repo.md` Q1 + URL refs | §4.3 emission 规则 + §9.5 regression fixture |
| Owner D 决议 | `docs/process/plans/2026-05-09-hcsf-v04-implementation-synthesis.md` §11 | §1 / §2.4.4 D6 / §2.4.14 D12 / §5.4 D5 |
| capability schema 设计依据 | `docs/process/plans/2026-05-09-hcsf-v04-implementation-claude.md` §1.3 | §2.4.* 14 node 字段细节 |

非 HUAKAI 引用：**0**（per CLAUDE.md #12 — 本 spec 为内部 schema 定义工作，无需读 ~/refs/* 上游源）。

---

## Tail block

Source files read: `backend/internal/proto/proto.go`, `backend/internal/proto/hcsf.go`, `backend/internal/proto/capability_matrix.go`, `backend/internal/proto/field_matrix.go`, `backend/internal/proto/passthrough.go`（部分）, `docs/process/plans/2026-05-09-hcsf-v04-implementation-synthesis.md`, `docs/process/plans/2026-05-09-hcsf-v04-implementation-claude.md`（offset 1-400 + 400-731）, `docs/process/plans/2026-05-09-hcsf-canonical-synthesis.md`, `docs/research/2026-05-09-axis3-huakai-current-state.md`, `docs/research/2026-05-09-issue-mining-cross-repo.md` — 全为 HUAKAI internal artefacts (CLAUDE.md #12 exempt).
Reference projects read: 0（per 任务约束：P-0 schema 锁定阶段 source-must-read 已在前置 lanes 完成；本 spec 为 internal schema 设计）.
Lane: claude (sonnet via general-purpose subagent — Agent ID `ac38ec39f01297cc9`)
Codex lane status: 不读（per 任务硬要求）；synthesis 由 PM-orchestrator 在两 lane 完成后做.
UTC timestamp: 2026-05-09T16:30Z
