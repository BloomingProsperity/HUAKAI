# 2026-05-08 Upgrade #7 — 上游字段 passthrough 完整性矩阵 (claude lane plan)

## 当前现状（Explore agent audit）

- 所有 proto adapter（OpenAI/Anthropic/Gemini/Bedrock）用 typed struct + `json.Unmarshal` —— **未识别字段全部静默丢失**
- 只有 Gemini `PromptFeedback map[string]any` 一处 bag-of-extra，但仍 drop
- HCSF (`CanonicalRequest` / `CanonicalEvent`) 零 reservoir 字段
- `CapabilityMatrix` 是 feature-level（`text_streaming` / `tool_use` 等粗粒度），**不是 field-level**
- `ProtocolLossEntry` 只记 known gap（unknown event type / stop_reason 等），**不记 unmarshal drops**
- 无 unknown-field roundtrip 测试
- Forwarder 无 raw-bytes fallback

## Owner directive

"B → 升级 #7"——HUAKAI 区别于 sub2api/new-api 的核心：vendor 加新字段不再丢。

## 升级目标

1. **PRESERVE-by-default**：未在矩阵显式登记的字段默认透传，不静默丢
2. **可观测的 loss**：transform/drop 必须显式登记 + 触发 `ProtocolLossEntry`
3. **新字段 forward-compat**：OpenAI/Anthropic 加 `system_fingerprint` / `logprobs` / `cache_control` 新别名 / `tool_choice` 新值 / `response_format` 新选项 / `reasoning_effort` 新档位等，HUAKAI 不改代码即透传给客户端
4. **字段级矩阵**：CapabilityMatrix 扩展到 per-field PRESERVE/TRANSFORM/DROP，每条都可被运维查询

## Scope

**In scope**:
- 新增 `proto.PassthroughEnvelope`：标准 JSON 包装类型，unknown fields → `Extra map[string]json.RawMessage`
- HCSF `CanonicalRequest` / `CanonicalEvent` / `CanonicalResponse` 各加 `Passthrough json.RawMessage` 字段
- 字段级 matrix `proto.FieldMatrix`：per-(client_protocol, upstream_protocol) 列 known fields 的 PRESERVE/TRANSFORM/DROP verdict
- 在 OpenAI adapter / Anthropic adapter 各做一遍 wire：unmarshal 时把 unknown 灌入 `Extra`，marshal 出去时合并回 envelope
- 测试矩阵：unknown-field roundtrip + 新字段 forward-compat 测试
- 不改 SQL schema（matrix 是代码级 const，不入库）

**Out of scope**:
- Gemini / Bedrock 适配（U7-D 单独 atomic）
- 实时观测 dashboard / metrics（U7-后续单独 atomic）
- request body 翻译方向 fix（U7-D 单独）
- Bedrock A7+A8 相关（已 deferred）

## 原子拆分（atomic 序列）

| atomic | 内容 | 文件 |
|---|---|---|
| **U7-A** | `proto.PassthroughEnvelope` + Helper 函数（unmarshal-with-extras / marshal-merging-extras）+ 单元测试 | proto/passthrough.go + _test |
| **U7-B** | HCSF 三主类型加 `Passthrough` 字段；不破坏既有 round-trip 测试 | proto/hcsf.go |
| **U7-C** | OpenAI adapter 接入：unmarshal 时填 envelope.Extra，event.Passthrough 携带未知字段；新增 forward-compat 测试 | proto/openai_sse.go + _test |
| **U7-D** | Anthropic adapter 接入；同 forward-compat 测试 | proto/anthropic_sse.go + _test |
| **U7-E** | `proto.FieldMatrix` 字段级 matrix + 已知字段登记 + matrix Lookup + tests | proto/field_matrix.go + _test |

注：U7-D Gemini/Bedrock 留作下一波（Bedrock 走 Anthropic delegate，自然受益于 U7-D Anthropic 完成）。

## Success criteria

1. 每个 atomic：`go test ./internal/proto/... -race` 通过
2. **forward-compat 测试**（关键）：构造一个 OpenAI streaming chunk，里面含一个 HUAKAI 不认识的字段（如 `"system_fingerprint":"fp_xxx"`），喂给 adapter → CanonicalEvent → 反序列化 → 客户端最终看到 `system_fingerprint` 原值
3. 字段级 matrix `Lookup("openai_chat", "openai", "system_fingerprint") == VerdictPreserved`，且 `Lookup` 任何未登记字段返回 `VerdictPreservedDefault`（不是 `VerdictUnsupported`，**这是与 feature 矩阵的关键区别**）
4. **不破坏既有测试**：full-sweep `go test ./...` pass

## Time estimate

- U7-A：30-45 分钟（核心 envelope + helper + tests）
- U7-B：15-20 分钟（HCSF 字段加 + 现有 test 不破）
- U7-C：60-90 分钟（OpenAI adapter 接入 + forward-compat fixture）
- U7-D：60-90 分钟（Anthropic adapter）
- U7-E：60-90 分钟（matrix）

合计 4-6 小时跨多 atomic；U7-A 单独可立刻开始。

## Blast radius

| atomic | radius | 风险 |
|---|---|---|
| U7-A | 0（新文件） | 极低 |
| U7-B | HCSF 类型变化，所有 adapter 重新编译；已有 reflect.DeepEqual 测试可能因新字段失败需更新 | 中 |
| U7-C | OpenAI 路径全 vendor 受益（deepseek/groq/together/perplexity/fireworks/openrouter/grok/copilot/cursor/antigravity/kiro/windsurf 12 family 全走 OpenAIAdapter） | 中 |
| U7-D | Anthropic 路径（anthropic_messages）+ Bedrock-on-Anthropic 受益（A4 BedrockAdapter delegate） | 中 |
| U7-E | 添加查询接口，纯添加无破坏 | 低 |

## Failure modes

1. **JSON 解析顺序**：unknown field 与 known field 同名冲突时，known field 优先。Go json 默认行为已对——但测试要覆盖。
2. **Extra 字段污染 known struct**：unmarshal 失败回滚——helper 实现要 explicit two-pass（先 typed → 再 raw map → 减去 known keys）。
3. **HCSF 类型扩展破坏现有 reflect.DeepEqual 测试**：U7-B 必须 grep 所有 reflect.DeepEqual 调用并预先评估。
4. **Marshal 合并冲突**：客户端响应序列化时，typed field 与 Passthrough.Extra 同名应以 typed 优先；helper 内置去重。
5. **field matrix 维护负担**：手动维护 known-field 表会随 vendor API 演进腐化——但比"什么都不登记"好，且 PRESERVE-default 兜底。
6. **clean-room**：sub2api 应该没有这套（hardcode mapping）；portkey 有 `extras` 字段 idea（思路启发，不 verbatim）。

## Decision points

| 项 | 选项 | 决策 |
|---|---|---|
| Default verdict | `Preserved` vs `Unsupported` | **`Preserved`**（PRESERVE-by-default 是核心升级语义） |
| Extra 字段类型 | `map[string]json.RawMessage` vs `json.RawMessage` 整块 | **`map[string]json.RawMessage`**（per-field detection + matrix 查询友好） |
| HCSF 在哪加 Passthrough | 全 type 都加 vs 只在 Event 加 | **全加**（Request/Response/Event 都可能携带未知字段） |
| Matrix 实现 | hardcode Go map vs JSON spec 文件 | **hardcode Go map** PHASE 1（避免运行时加载 + 类型安全）；P2 再考虑 JSON spec |
| 是否保留 typed 字段 | 全转 RawMessage vs 保留 typed + 加 Extra | **保留 typed + 加 Extra**（已有 adapter 逻辑不破坏） |
| HUAKAI 区别于 portkey 的点 | portkey 的 extras 是 untyped 透传 | HUAKAI Extra **+ 字段级 matrix verdict**——可以查"哪些 field 是 known/transformed/dropped"，运维可观测 |

## 设计大纲

### U7-A: proto/passthrough.go

```go
// PassthroughEnvelope 是 unknown-field 透传容器。
// 与典型 typed JSON 解析共存：先 typed unmarshal 拿 known，再 raw unmarshal
// 拿全字段，减去 known set 得到 Extra。
type PassthroughEnvelope struct {
    Extra map[string]json.RawMessage `json:"-"`
}

// UnmarshalWithExtras 把 raw JSON 解到 typed + 把未识别字段抓到 dst.Extra。
// known 集合从 typed struct 的 json tag 反射推导（cached）。
func UnmarshalWithExtras(raw []byte, typed any, dst *PassthroughEnvelope) error

// MergeExtrasInto 把 dst.Extra 合并到 typedJSON 输出：
//   - typed field 与 Extra key 冲突时 typed 优先（去重）
//   - 用于 client 响应 marshal 时透传 unknown 字段
func MergeExtrasInto(typedJSON []byte, env *PassthroughEnvelope) ([]byte, error)
```

测试：
- happy: 已知字段 + 未知字段混合 → typed 装满，Extra 装未知
- 字段冲突：JSON 同时有 typed key + 别名 → typed 优先
- 空 Extra：merge 返回原 JSON 不变
- nil envelope：UnmarshalWithExtras 不 panic
- 嵌套 unknown 字段：Extra value 是 RawMessage（保留嵌套结构）

### U7-B: HCSF 字段加

`hcsf.go`:
```go
type CanonicalRequest struct {
    // ... existing fields
    Passthrough *PassthroughEnvelope `json:"-"`  // 携带未识别字段，serialize 时 merge
}

type CanonicalResponse struct { ... Passthrough *PassthroughEnvelope `json:"-"` }
type CanonicalEvent    struct { ... Passthrough *PassthroughEnvelope `json:"-"` }
```

注意：JSON tag `"-"` 防止 Passthrough 自己变成一个嵌套字段；序列化由 Merge helper 控制。

### U7-C: OpenAI adapter wire-up

`openai_sse.go` 修改 `ProviderEventToCanonicalEvents`:
- 解析 SSE chunk JSON 用新的 `UnmarshalWithExtras` 而非裸 `json.Unmarshal`
- typed `openAIChatCompletionChunk` 装 known，envelope.Extra 装未知
- 把 envelope 复制到 `CanonicalEvent.Passthrough`，由 forwarder 经 ClientAdapter 写出时 Merge

forward-compat 测试：
```go
chunk := `{"id":"x","model":"gpt-4o","system_fingerprint":"fp_abc","choices":[...]}`
evt := adapter.Process(chunk)
assert evt.Passthrough.Extra["system_fingerprint"] == json.RawMessage(`"fp_abc"`)
```

### U7-D: Anthropic adapter wire-up

同 U7-C，针对 `anthropic_sse.go` 的 `anthropicEnvelope`。Bedrock-on-Anthropic 自动受益（A4 delegator 不变）。

### U7-E: proto/field_matrix.go

```go
type FieldVerdict string
const (
    FieldPreserved        FieldVerdict = "preserved"      // 显式登记 PRESERVE
    FieldTransformed      FieldVerdict = "transformed"    // 已知 transformation（lossy/lossless）
    FieldDropped          FieldVerdict = "dropped"        // 显式 known drop（rare）
    FieldPreservedDefault FieldVerdict = "preserved_default" // 未登记，默认透传
)

type FieldMatrix map[ClientProtocol]map[UpstreamProtocol]map[string]FieldVerdict

func (m FieldMatrix) Lookup(client ClientProtocol, upstream UpstreamProtocol, fieldName string) FieldVerdict {
    if v, ok := m[client][upstream][fieldName]; ok { return v }
    return FieldPreservedDefault
}

func DefaultFieldMatrix() FieldMatrix {
    // 登记已知 known fields（系统 fingerprint / cache_control / 等）
    // 其它 vendor 添加新字段时无需改代码
}
```

## 测试矩阵

1. **U7-A unit**: PassthroughEnvelope 单元测试 6+ 用例
2. **U7-A race**: 并发 UnmarshalWithExtras 不 race
3. **U7-B**: HCSF round-trip 通过；reflect.DeepEqual 在 Passthrough nil 时仍等价
4. **U7-C forward-compat**: OpenAI chunk 含 `system_fingerprint` / `logprobs` / `service_tier` / `prompt_filter_results` 等 4+ 真实 OpenAI 新字段透传通过
5. **U7-D forward-compat**: Anthropic chunk 含 `cache_creation_input_tokens` / `cache_read_input_tokens` / `service_tier` 等真实 Anthropic 新字段透传通过
6. **U7-E lookup**: 已登记字段返回登记 verdict；未登记返回 `FieldPreservedDefault`
7. **不破坏 sweep**: full `go test ./...` 通过

## 平行交叉法（CLAUDE.md #10）

- claude lane plan: 本文件
- codex lane plan: 计划派 foreground codex（bg 在本会话已 3 次失败）
- explore agent audit (already done): `agent-a94d435005f693c95` — 关键发现纳入本 plan §"当前现状"

## 引用源

- HUAKAI 内部：`backend/internal/proto/proto.go` / `hcsf.go` / `capability_matrix.go` / `openai_sse.go` / `anthropic_sse.go`
- 思路启发（不 verbatim）：portkey 的 extras 字段透传 idea、OpenAI Cookbook 的 unknown-field 处理建议
- 严禁读 sub2api / new-api / litellm reference 实现源（CLAUDE.md #11）

Lane: claude
Time: 2026-05-08T<UTC>
