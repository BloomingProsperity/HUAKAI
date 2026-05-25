# P-0 Schema Spec — Claude × Codex Synthesis

**日期**: 2026-05-09
**前置 lanes**:
- `docs/process/plans/2026-05-09-p0-schema-spec-claude.md`（Sonnet 1377 行 / 76 KB）
- `docs/process/plans/2026-05-09-p0-schema-spec-codex.md`（Codex 67 KB / ~1300 行）
**触发**: Owner 2026-05-09 批 7 D 推荐 + 走 A 路径（立即开 P-0）

## TL;DR

两 lane 在大方向高度一致（14 capability、Anthropic-rich primary、JSON 不变量、protocol_loss 一等公民），但**工程模式不同**：
- **Sonnet**: interface + 5 EnvelopeKind 枚举（Request/Response/Event/Batch/NativePassthrough）
- **Codex**: tagged-union + nullable 字段（BufferedResponse / StreamEvents 可空区分形态）

**综合推荐采纳 Codex 工程模式**。理由见 §2。两 lane 共有的 14 capability / RequestMeta / ProviderProjection / StreamPlan / Accounting / Policy / 测试 fixture 要求 / 兼容迁移路径取并集 best-of。

P-0b LoC 估算 ~1500-2000，~25 新文件 + 6 修改。

## 1. 共识（直接采纳，不再决策）

| 项 | 共识内容 |
|---|---|
| 14 capability families | text / tool_use / tool_result / thinking / cache_control / structured_output / computer_use / file / image / audio / video / live_session / batch / mcp_server / data_retention |
| Anthropic-rich primary | IR schema 优先 Anthropic 字段保真（cache_control / thinking / tool_use 一等公民） |
| OpenAI-compatible storefront | 入口 `/v1/chat/completions`；canonical 不丢 |
| protocol_loss 一等公民 | 任何 capability 在任何 provider projection 上有 lossy → emit ProtocolLossEntry |
| JSON round-trip 不变量 | marshal → unmarshal → marshal 应字段顺序无关 deep equal |
| 14 capability flat in `proto/` package | 避免循环 import；子包决策推到 P-1 |
| 命名空间清理 | `proto.HCSF struct{}` 删除（保留临时 alias 给 P-0b 兼容） |
| 不入库 / 不持久化 | per D3，HCSFEnvelope 仅内存 IR |
| Comment 中文 / 标识符 + JSON tag 英文 | per `feedback_chinese_comments.md` |
| ~25 新文件 + 6 修改 | LoC 估算 ~1500-2000 |
| 测试 fixture | per capability ≥1 + per vendor projection + 边 case + 5 issue regression (#4678 / #1552 / #1579 / #27468 / #4697) |

## 2. 分歧 → 决策（Synthesis 拍板）

### 2.1 Capability node 模式：tagged-union 还是 interface？

**采纳 Codex 的 tagged-union**:

```go
type CapabilityNode struct {
    ID          string         `json:"id"`
    Kind        CapabilityKind `json:"kind"`
    Source      NodeSourceRef  `json:"source,omitempty"`
    StreamReady StreamReadiness `json:"stream_ready"`
    ProtocolLoss []ProtocolLossEntry `json:"protocol_loss,omitempty"`
    
    // 14 个 nullable pointer，Kind 决定哪个非空
    Text             *TextNode             `json:"text,omitempty"`
    ToolUse          *ToolUseNode          `json:"tool_use,omitempty"`
    ToolResult       *ToolResultNode       `json:"tool_result,omitempty"`
    // ... 11 more
}
```

理由：
- 标准 `encoding/json` 直接 round-trip，无需 custom unmarshal + 类型注册表
- 不引入 runtime dependency
- Validator 简单（一行 switch）
- Go ecosystem 主流（OpenAPI 自动生成的 schema 都是这种）
- Sonnet 的 interface 模式需要自定义 marshal/unmarshal——增加 P-0b 复杂度 + bug 面

代价：Node struct 胖（14 nullable pointer）；加新 capability 需改 Node struct——但 P-0 后不会频繁加，可接受。

### 2.2 Envelope 形态区分：EnvelopeKind 枚举 还是 nullable 字段？

**采纳 Codex 的 nullable 字段**：

```go
type HCSFEnvelope struct {
    Version           string                  `json:"version"`
    RequestMeta       RequestMeta             `json:"request_meta"`
    RequestControls   RequestControls         `json:"request_controls"`
    Messages          []CanonicalMessage      `json:"messages"`
    BufferedResponse  *CanonicalResponse      `json:"buffered_response,omitempty"`
    StreamEvents      []CanonicalEvent        `json:"stream_events,omitempty"`
    CapabilityGraph   CapabilityGraph         `json:"capability_graph"`
    ProviderProjection ProviderProjection     `json:"provider_projection"`
    StreamPlan        StreamPlan              `json:"stream_plan"`
    Accounting        Accounting              `json:"accounting"`
    Policy            Policy                  `json:"policy"`
    Extensions        map[string]json.RawMessage `json:"extensions,omitempty"`
}
```

形态推导规则：
- 仅 Messages 非空 + BufferedResponse=nil + StreamEvents=nil → request envelope
- BufferedResponse != nil → buffered response envelope
- StreamEvents != nil → event-replay envelope（fixture / replay）
- Native Passthrough → 不在 envelope；用 Policy.AuthMode + 直走 `/v1/native/<vendor>/<capability>` route

理由：
- 解 Sonnet 没解释清楚的"OpenAI/Gemini buffered response 当前返回 `&HCSF{}` 加 lossy entry"痛点（[`backend/internal/proto/openai_sse.go:148-156`](backend/internal/proto/openai_sse.go#L148-L156)、[`gemini_sse.go:103-111`](backend/internal/proto/gemini_sse.go#L103-L111)）
- 不需要额外 EnvelopeKind 字段（节省 schema 复杂度）
- 形态由数据本身决定，不是显式标签——更 Go-idiomatic

### 2.3 RequestControls 显式拆出还是埋在 Messages？

**采纳 Codex 的 RequestControls 显式子结构**:

```go
type RequestControls struct {
    Tools              []CanonicalTool  `json:"tools,omitempty"`
    ToolChoice         *ToolChoice      `json:"tool_choice,omitempty"`
    MaxTokens          *int             `json:"max_tokens,omitempty"`
    Stop               []string         `json:"stop,omitempty"`
    Temperature        *float64         `json:"temperature,omitempty"`
    TopP               *float64         `json:"top_p,omitempty"`
    SystemPrompt       string           `json:"system_prompt,omitempty"`
    ParallelToolCalls  *bool            `json:"parallel_tool_calls,omitempty"`
    ResponseFormat     *ResponseFormat  `json:"response_format,omitempty"`
    Seed               *int             `json:"seed,omitempty"`
    StopSequences      []string         `json:"stop_sequences,omitempty"`
    
    // D4 推迟：tool_call_id / tool_use_id 哈希算法
    ToolNameHashAlgorithm string `json:"tool_name_hash_algorithm,omitempty"`
}
```

理由：
- 现有 [`backend/internal/proto/hcsf.go:11-27`](backend/internal/proto/hcsf.go#L11-L27) 中 CanonicalRequest 已有这些字段
- 放 capability_graph 里太重（这些是简单 scalar/array，不是 graph node）
- 命名一致性 + 序列化稳定性
- D4 (SHA-8 vs SHA-12 collision detection) 字段以 string placeholder 留位，P-2/P-3 实施时再选

### 2.4 5 Edge type 命名采纳 Sonnet 提议

```go
type CapabilityEdgeType string

const (
    EdgeProvides           CapabilityEdgeType = "provides"            // node A 提供 node B 所需能力
    EdgeRequires           CapabilityEdgeType = "requires"            // node A 依赖 node B
    EdgeMutuallyExclusive  CapabilityEdgeType = "mutually_exclusive"  // A 与 B 不可共存
    EdgeLoses              CapabilityEdgeType = "loses"               // 投影时 lossy 关系
    EdgeRequiresNative     CapabilityEdgeType = "requires_native"     // 需走 native passthrough
)
```

Codex 的 edge model 在 §3 描述但没列具体枚举。Sonnet 的命名更标准。

### 2.5 推迟决策点 schema 留位

| 推迟决策 | Schema 位置 | 默认值 | 决定 phase |
|---|---|---|---|
| D4 SHA-8 vs SHA-12 | `RequestControls.ToolNameHashAlgorithm` (string) | `"sha8"` | P-2/P-3 |
| D8 Spend 数字来源 | `Accounting.UsageSource` (existing enum extends) | `inferred` | P-7 |
| D9 mid-stream fallback policy | `StreamPlan.MidStreamFallbackPolicy` (string) | `"none"` | P-8 |
| D10 capability matrix cell 数 | 不需 schema 字段，property test 自动生成 | n/a | P-5 |
| D13 release gate threshold | `Policy.ReleaseGateThreshold` (struct) | empty | P-5 |
| D14 测试依赖 | 不需 schema 字段 | n/a | P-5 |

## 3. 合并后的完整 P-0 文件清单

| 文件 | 操作 | 内容 |
|---|---|---|
| `backend/internal/proto/proto.go` | 修改 | 删 `HCSF struct{}`；改为 `type HCSF = HCSFEnvelope` 临时 alias |
| `backend/internal/proto/hcsf.go` | 修改 | 删 v0.3 stub；保留 CanonicalMessage/Event/Usage/Tool 等被 envelope 复用的类型 |
| `backend/internal/proto/envelope.go` | 新增 | HCSFEnvelope 顶层 struct + version constants |
| `backend/internal/proto/request_meta.go` | 新增 | RequestMeta + RequestControls |
| `backend/internal/proto/capability_graph.go` | 新增 | CapabilityGraph / CapabilityNode / CapabilityEdge / CapabilityKind enum |
| `backend/internal/proto/capability_text.go` | 新增 | TextNode |
| `backend/internal/proto/capability_tool.go` | 新增 | ToolUseNode + ToolResultNode |
| `backend/internal/proto/capability_thinking.go` | 新增 | ThinkingNode |
| `backend/internal/proto/capability_cache.go` | 新增 | CacheControlNode |
| `backend/internal/proto/capability_structured.go` | 新增 | StructuredOutputNode |
| `backend/internal/proto/capability_computer_use.go` | 新增 | ComputerUseNode |
| `backend/internal/proto/capability_file.go` | 新增 | FileNode |
| `backend/internal/proto/capability_image.go` | 新增 | ImageNode |
| `backend/internal/proto/capability_audio.go` | 新增 | AudioNode |
| `backend/internal/proto/capability_video.go` | 新增 | VideoNode |
| `backend/internal/proto/capability_live.go` | 新增 | LiveSessionNode |
| `backend/internal/proto/capability_batch.go` | 新增 | BatchNode |
| `backend/internal/proto/capability_mcp.go` | 新增 | MCPServerNode |
| `backend/internal/proto/capability_data_retention.go` | 新增 | DataRetentionNode + DataRetentionLabel enum |
| `backend/internal/proto/projection.go` | 新增 | ProviderProjection |
| `backend/internal/proto/stream_plan.go` | 新增 | StreamPlan |
| `backend/internal/proto/accounting.go` | 新增 | Accounting（复用 CanonicalUsage） |
| `backend/internal/proto/policy.go` | 新增 | Policy + DataRetentionLabel + AuthMode |
| `backend/internal/proto/protocol_loss.go` | 新增 | ProtocolLossEntry（v0.4 升级；保留 v0.3 4 字段 + 5 v0.4 字段） |
| `backend/internal/proto/envelope_validate.go` | 新增 | tagged-union validator（exactly-one payload + Kind 一致性） |
| `backend/internal/proto/envelope_test.go` | 新增 | JSON round-trip 不变量测试 |
| `backend/internal/proto/capability_*_test.go` | 新增 | per-node 测试 |
| `backend/internal/proto/fixtures/*.json` | 新增 | 测试 fixture：≥34 文件，按 envelope/response/event/regression 分类 |

总计 ~25 新文件 + 6 修改文件。

## 4. JSON Round-Trip 不变量（取两 lane 并集）

| 不变量 | 描述 |
|---|---|
| INV-1 | marshal → unmarshal → marshal 字段顺序无关 deep equal |
| INV-2 | 空 slice / nil slice 不区分（Go 习惯）—— `nil` 序列化省略 |
| INV-3 | tagged-union 一致性：`Kind == "text"` ⟺ `Text != nil` ⟺ 其它 14 nullable pointer 全 nil |
| INV-4 | Version 字段必须 = "0.4"（P-0 锁定） |
| INV-5 | RequestMeta 必填字段非空（RequestID, ClientProtocol, ProtocolFamily, Model） |
| INV-6 | EnvelopeShape 由数据派生：BufferedResponse + StreamEvents 至多一个非 nil |
| INV-7 | ProtocolLoss 不可作为 silent drop；缺字段必须有 entry |
| INV-8 | Edge 引用的 node ID 必须存在于 Nodes 数组（dangling edge 无效） |
| INV-9 | EdgeMutuallyExclusive 不可双向（A↔B 只一个 edge） |
| INV-10 | DataRetentionLabel 严格枚举（5 词汇外的值 reject） |
| INV-11 | StreamPlan.MidStreamFallbackPolicy 默认 "none"（P-8 才有非 none） |
| INV-12 | Extensions key 必须 `vendor:` 或 `experimental:` 前缀 |

## 5. 测试 fixture 要求

至少 34 个 fixture：

- **envelope/** ≥ 14：每 capability 的 minimal envelope（仅含一个 capability node + 必要 RequestMeta/Messages/CapabilityGraph）
- **response/** ≥ 5：BufferedResponse + StreamEvents 各种组合
- **event/** ≥ 5：典型 streaming event 序列
- **regression/** ≥ 5：5 个 issue regression（new-api#4678 cache_read=0 / sub2api#1552 / Portkey#1579 / LiteLLM#27468 / new-api#4697）
- **edge_case/** ≥ 5：empty graph / single text / tool_use chain / native passthrough required / cross-tenant prefix

## 6. P-0b 实施计划（Day-by-Day）

| Day | 任务 | 产物 |
|---|---|---|
| Day 1 | envelope.go + version constants + RequestMeta + RequestControls | 顶层 struct |
| Day 2 | capability_graph.go + CapabilityNode tagged-union + CapabilityKind enum + 5 edge types | graph 骨架 |
| Day 3 | 14 capability_*.go 文件（每个 node payload struct）| 14 nodes |
| Day 4 | projection.go + stream_plan.go + accounting.go + policy.go + protocol_loss.go | 子结构 |
| Day 5 | envelope_validate.go + tagged-union validator | 完整性校验 |
| Day 6 | envelope_test.go + JSON round-trip 不变量 12 条 | INV-1..12 测试 |
| Day 7 | 34+ fixture 文件 + per-capability 测试 | 完整测试 |
| Day 8 | proto.HCSF alias 兼容 + sunset 标 | 现有调用继续工作 |
| Day 9 | go test ./backend/internal/... | 全包绿 |
| Day 10 | sonnet review (codex 工具坏的 backup) + Owner 综合 | review report |

如 D-1..10 全顺利，1.5 周内 P-0b commit ready；如有 schema 调整需求 buffer 到 2 周。

## 7. 风险与盲点

- **P-0 schema 锁定后调整成本高**：14 capability 的 nullable pointer 改名/合并会影响所有调用方
- **现有 OpenAI/Gemini 走 `&HCSF{}` 路径**：必须在 P-0b 同 commit 改完，否则 build 不过
- **proto.HCSF alias sunset**：临时 alias 必须在 P-2 删除（P-2 ClientAdapter 落地时）
- **fixtures regression 依赖 issue 实测**：5 个 issue 的 fixture 需要从公开 issue body paraphrase 构造；不能 verbatim copy（per #11）
- **测试 timing**：不变量 INV-1..12 + 34 fixture 测试可能在 sandbox 跑 30s+；P-0b CI 时间预算需考虑

## 8. 给 Owner 的批准点

如同意采纳 Codex 工程模式（tagged-union + nullable + RequestControls），可立即开 P-0b 实施。

如要 override：
- (a) 改 interface dispatch 模式（Sonnet 提议）— 增加自定义 marshal 复杂度
- (b) 加 EnvelopeKind 枚举（Sonnet 提议）— 增加 schema 字段但不必要
- (c) 不拆 RequestControls — 现有 CanonicalRequest 字段会丢

## Tail block (per AGENTS.md template)

Source files read: `docs/process/plans/2026-05-09-p0-schema-spec-{claude,codex}.md` (HUAKAI internal — exempt per #12)；`backend/internal/proto/hcsf.go` / `proto.go` / `openai_sse.go` / `gemini_sse.go` (HUAKAI internal — exempt)
Lane: synthesizer (cross-discuss + agree/conflict/gaps + tagged-union vs interface evaluation)
Agent: Claude opus-4-7 [1m]
UTC timestamp: 2026-05-09T17:50Z
