# 2026-05-08 Bedrock A4 — proto.BedrockEventStreamAdapter（claude lane plan）

| Owner directive | "继续 bedrock"（A 路径：完 Bedrock A4-A8 之后再升级） |
| Scope | In: 实现 `backend/internal/proto/bedrock_eventstream.go` + tests，把 A3 scanner 产出的 `gateway.SSEEvent`（已 base64-decode 内层 Anthropic 事件 JSON）转换为 canonical event 流。Out: 不改 anthropic_sse.go；不改 matrix；不动 forwarder 注册（A5+A6 才接入）；不引依赖。 |
| Success criteria | (a) Adapter 实现 `proto.UpstreamAdapter` 接口；(b) `ProviderEventToCanonicalEvents` 把 SSEEvent.Data（Anthropic JSON）→ CanonicalEvent 列表，与 AnthropicAdapter 同 fixture 同输出；(c) `FinalizeUpstreamStream` 处理未结束 block；(d) Bedrock-on-Anthropic 全 5 种事件 happy path + ctx cancel + unknown type loss 通过测试；(e) 与 A3 scanner 集成 e2e（A3 scanner → A4 adapter → 校验 canonical 流）。 |
| Time estimate | 45-60 分钟 wall clock；一 lane（codex 平行） |
| Blast radius | 只新增一个文件 + 一个测试文件；不改 forwarder / capability matrix / proto.go；A5+A6 才接入注册。回滚 = `git revert`。 |
| Failure modes | (1) SSEEvent → anthropicEvent 字段语义错配（A3 scanner emit 的 SSEEvent.Type 是内层 Anthropic 事件 type，已对齐）；(2) AnthropicAdapter 的内部解析能否复用——直接 delegate 而非 copy；(3) Bedrock 特有 stop_reason 映射差异——Bedrock 透传 Anthropic 字段，应同；(4) signature_delta：matrix 显示 Bedrock-on-Anthropic 路径无 LOSSY 标记，复用 AnthropicAdapter 默认行为即可。 |
| Decision points | 是否新增独立 struct vs 直接复用 AnthropicAdapter。**选独立 struct（Option X）**——理由：未来 Llama-on-Bedrock / Cohere-on-Bedrock 不能 delegate Anthropic；matrix loss entries 可能不同；exception/error 帧虽由 A3 scanner 处理但 A4 仍需 finalize 时区分上下文。 |
| Pre-execution checklist | 1. 已读 anthropic_sse.go 知道 UpstreamState 结构；2. 已读 capability_matrix.go 确认 UpstreamProtocolBedrock 已声明；3. 派发 codex 平行 lane 做相同任务；4. 完成后 diff codex lane 选实现。 |

## 设计大纲（claude）

```go
// backend/internal/proto/bedrock_eventstream.go
package proto

// BedrockEventStreamAdapter 把 gateway.BedrockEventStreamScanner 产出的
// SSEEvent（Type=内层 Anthropic 事件 type，Data=内层 Anthropic JSON）
// 转换为 canonical 事件。
//
// 当前只支持 Bedrock-on-Anthropic（Bedrock 跑 Claude 模型）。
// 未来 Bedrock-on-Llama / Bedrock-on-Cohere 时再分流。
type BedrockEventStreamAdapter struct {
    inner *AnthropicAdapter
}

func NewBedrockEventStreamAdapter() *BedrockEventStreamAdapter {
    return &BedrockEventStreamAdapter{inner: &AnthropicAdapter{}}
}

func (s *BedrockEventStreamAdapter) ProviderEventToCanonicalEvents(
    ctx context.Context, providerEvt any, state any,
) ([]any, []ProtocolLossEntry, error) {
    // 把 gateway.SSEEvent 翻译为 anthropicEvent 给 inner adapter
    // 不直接 import gateway 包（依赖方向反），而是用 type switch + 鸭子类型
    // ...
    return s.inner.ProviderEventToCanonicalEvents(ctx, anthEvt, state)
}
```

## 跨包依赖问题

`gateway.SSEEvent` 在 gateway 包；`proto` 不能 import gateway（否则反向依赖）。
方案：A4 adapter 接受 `[]byte`（原始 Anthropic JSON）+ optional event-type string，
而非 `gateway.SSEEvent`。这是 AnthropicAdapter 已有的 coerce 行为。

具体：A4 的 `ProviderEventToCanonicalEvents` 接受 `any`，handle:
1. `anthropicEvent`（适配 inner adapter 已支持）
2. `[]byte`（Anthropic JSON）
3. **新增**：`struct{ Type string; Data []byte }` 形 duck（A3 emit shape）

集成层（gateway forwarder）后续负责 SSEEvent → 这个 struct 的 mapping。

## 测试矩阵

1. happy 5 事件序列 → 5 canonical events
2. message_start 缺失 ID → 仍能产出（state.MessageID = ""）
3. content_block_start + tool_use → CanonicalContentBlock.Type=tool_use
4. signature_delta → 默认 LOSSY skipped
5. unknown event type → ErrUnknownEventType
6. ctx cancel → 不应 panic（adapter 不直接做 IO）
7. FinalizeUpstreamStream：剩 2 个 block in progress → emit 2 个 content_block_stop + 1 个 message_stop
8. e2e via A3 scanner：合成 binary 流 → A3 scanner → A4 adapter → 校验 canonical 序列

## 平行交叉法（CLAUDE.md #10）

- claude lane（本 plan）：本文件
- codex lane：将派发独立 plan，相同 scope，结果存 `/tmp/parallel-a4-codex/`

完成后 diff 两 lane，记录差异 + 选 prod 版本。

## 引用源

- `backend/internal/proto/anthropic_sse.go`（已存在，HUAKAI 内部）
- `backend/internal/gateway/bedrock_stream_scanner.go`（A3，emit shape）
- AWS Bedrock 公开文档（chunk envelope 形态）

严禁读 aws-sdk-go / botocore / aws-encryption-sdk reference 实现源码（CLAUDE.md #11）。

Lane: claude
Time: 2026-05-08T<UTC>
