# 2026-05-07 Bedrock EventStream 接入计划（Codex 独立版）

| 字段 | 内容 |
| --- | --- |
| Lane | planner |
| Owner directive | “在 /c/HUAKAI/repo/docs/plans/ 写一份 plan，文件名 2026-05-07-bedrock-eventstream-codex.md。Lane = planner（独立思考，不要看 Claude 的 plan 文件，不要 grep claude plan）。” |
| 独立性声明 | 未读取、未搜索 Claude plan 文件；只读取 HUAKAI 本地代码/规则/spec，并用 AWS 官方文档确认 EventStream/Bedrock streaming 事实。 |
| 目标 | `/v1/chat/completions` 选中 `bedrock_invoke` 上游时，可以消费 AWS Bedrock `invoke-with-response-stream` 的 binary EventStream，解析 Anthropic-on-Bedrock chunk，继续向客户端输出 HUAKAI SSE，并保留 usage/end_class/drain 语义。 |

## Scope

In scope:

- 设计 Bedrock binary EventStream 解码边界。
- 设计 `StreamForwarder` 从固定 SSE scanner 演进到可插拔 scanner。
- 设计 Bedrock chunk payload 到 HCSF canonical event 的 adapter 位置与接口。
- 设计 clean-room 边界、atomic 拆分和测试策略。
- 对齐当前代码现实：`event_scanner.go` 是 line-based SSE；`StreamForwarder.Forward` 固定 `ScanSSEEvents`；`BuildDefaultProtocolAdapterRegistry()` 暂不注册 `bedrock_invoke`；`provider/bedrock` 已有 stream endpoint 模板但 stream intent 目前放在 `Credential.Extra["stream"]`。

Out of scope:

- 不直接改代码。
- 不接真实 AWS 网络、不使用真实 credential。
- 不改 database schema、billing ledger、quota enforcement、auth core。
- 不接 ConverseStream；本计划只覆盖 Runtime `InvokeModelWithResponseStream`。

Success criteria:

- Bedrock binary EventStream 永不经过 `bufio.Scanner`。
- SSE 现有路径不回退，原 forwarder/proto tests 继续通过。
- Bedrock frame decoder 有 length/CRC/partial read/exception/oversize 测试。
- Anthropic-on-Bedrock payload 可以转 canonical events，并由现有 client SSE 输出路径消费。
- 不复制 AWS SDK 源码、结构、注释或测试。

## 关键设计判断

结论：**不要把 binary EventStream frame decoder 放进 `backend/internal/proto/`**。

推荐两层拆分：

- `backend/internal/provider/bedrock/eventstream`：wire 层 decoder。只负责 AWS EventStream binary framing：prelude、headers、payload、CRC、limits。
- `backend/internal/proto/bedrock_eventstream.go`：semantic adapter。只负责“已解包、已 base64 decode 的 Anthropic event JSON -> HUAKAI canonical events”。

原因：

- EventStream 是 AWS transport/wire framing，不是 HCSF protocol translation。
- `proto` 应保持语义协议转换边界；否则会把二进制帧、CRC、安全 limits 混进 canonical adapter。
- Bedrock 不能简单注册成 `AnthropicAdapter`：Anthropic JSON 语义可复用，但 Bedrock 有独立 chunk envelope、exception event 和 wire error。

建议文件：

```text
backend/internal/provider/bedrock/eventstream/decoder.go
backend/internal/provider/bedrock/eventstream/decoder_test.go
backend/internal/gateway/stream_scanner.go
backend/internal/gateway/bedrock_stream_scanner.go
backend/internal/gateway/bedrock_stream_scanner_test.go
backend/internal/proto/bedrock_eventstream.go
backend/internal/proto/bedrock_eventstream_test.go
```

## 数据结构与接口

### EventStream decoder

```go
package eventstream

type Limits struct {
    MaxMessageBytes int
    MaxHeaderBytes  int
    MaxPayloadBytes int
}

type Message struct {
    Headers       HeaderMap
    Payload       []byte
    TotalLength   uint32
    HeadersLength uint32
}

type HeaderMap map[string]HeaderValue

type HeaderValue struct {
    Type  HeaderValueType
    Value any
}

type Decoder struct {
    Limits Limits
}

func (d *Decoder) ReadMessage(ctx context.Context, r io.Reader) (Message, error)
```

Decoder 规则：

- 用 `io.ReadFull`，不使用 `bufio.Scanner`。
- 读 12-byte prelude：`total_length`、`headers_length`、`prelude_crc`，均 big endian。
- 校验 `total_length >= 16`、`headers_length <= total_length - 16`、frame/header/payload 不超过 limit。
- prelude CRC 覆盖前 8 byte；message CRC 覆盖除最后 4 byte 外的完整 message。
- 至少解析 string header；建议完整支持 AWS EventStream primitive header 类型，便于后续 exception/control frame。

### Gateway scanner abstraction

```go
type StreamWireProtocol string

const (
    StreamWireSSE                StreamWireProtocol = "sse"
    StreamWireBedrockEventStream StreamWireProtocol = "bedrock_eventstream"
)

type StreamEvent struct {
    Type       string
    Data       []byte
    Headers    map[string]string
    ObservedAt time.Time
    Wire        StreamWireProtocol
    WireBytes   int64
}

type StreamScanLimits struct {
    MaxEventBytes   int
    MaxFrameBytes   int
    MaxHeaderBytes  int
    MaxPayloadBytes int
}

type StreamScanner interface {
    Scan(ctx context.Context, r io.Reader, limits StreamScanLimits) iter.Seq2[StreamEvent, error]
}

type StreamScannerRegistry interface {
    For(protocolFamily string) (StreamScanner, error)
}
```

兼容策略：

- `SSEStreamScanner` 包装现有 `ScanSSEEvents`，先保持 `ScannerBufferCap` 语义。
- `StreamForwarder` 新增 `StreamScanners StreamScannerRegistry`，按 `req.ProtocolFamily` 取 scanner。
- SSE protocol families 注册到 `SSEStreamScanner`；`bedrock_invoke` 注册到 `BedrockEventStreamScanner`。
- `handleEventWithAdapter` 初期继续把 `evt.Data` 传给 adapter，降低改动面。

### Bedrock scanner 解包

Bedrock EventStream frame headers 需要关注：

```text
:message-type = event | exception
:event-type   = chunk | internalServerException | throttlingException | ...
:content-type = application/json
```

chunk payload 建议结构：

```go
type BedrockChunkEnvelope struct {
    Bytes string `json:"bytes"`
}
```

处理规则：

- `:event-type == "chunk"`：parse payload JSON `{"bytes":"<base64>"}`，base64 decode 后得到 Anthropic event JSON，输出 `StreamEvent{Type:"chunk", Data: decodedJSON, Wire: StreamWireBedrockEventStream}`。
- `:message-type == "exception"` 或 event type 是 exception：返回 `ErrBedrockStreamException{EventType, Message, OriginalStatusCode}`。
- unknown event type：返回 typed error，不静默吞。
- 可以在测试中覆盖“SDK-style already decoded bytes”兼容场景，但 raw HTTP 主路径必须按 `{"bytes":"..."}` 处理。

### Proto adapter

```go
type BedrockEventStreamAdapter struct {
    CarryForwardSignatureDelta bool
}

type BedrockUpstreamState struct {
    Anthropic       UpstreamState
    FramesSeen      int
    ChunkEventsSeen int
    ExceptionSeen   bool
    Terminated      bool
}

func (a *BedrockEventStreamAdapter) ProviderEventToCanonicalEvents(
    ctx context.Context,
    providerEvt any,
    state any,
) ([]any, []ProtocolLossEntry, error)
```

语义：

- Adapter 不解析 EventStream frame，只解析模型 payload。
- 初期 `providerEvt` 接受 `[]byte`，即已 decode 的 Anthropic event JSON。
- 可在 `proto` 包内提取私有 helper，让 `AnthropicAdapter` 和 `BedrockEventStreamAdapter` 共用“Anthropic event JSON -> canonical”的本地语义逻辑。
- 保留独立 adapter/state，避免 Bedrock wire/error 特性被隐藏。

## StreamForwarder 接入

建议最小改动：

```go
type StreamForwarder struct {
    ProtocolAdapters ProtocolAdapterRegistry
    StreamScanners   StreamScannerRegistry
    ClientAdapter    proto.ClientAdapter
    Timeouts         TimeoutConfig
    ScannerBufferCap int
    DrainBudgets     DrainBudgets
    CostEstimator    func(drainedBytes int64, acc UsageAccumulator) decimal.Decimal
}
```

`Forward` 新流程：

1. 按 `req.ProtocolFamily` 取 semantic adapter。
2. 按 `req.ProtocolFamily` 取 stream scanner。
3. scanner 把 upstream reader 转为 `StreamEvent`。
4. adapter 消费 `evt.Data`。
5. client 输出仍走现有 `clientChunks`，所以下游仍是 SSE。

状态初始化先用小 switch：

```go
func (f *StreamForwarder) newUpstreamState(req ForwardRequest) any {
    switch req.ProtocolFamily {
    case "bedrock_invoke":
        return &proto.BedrockUpstreamState{}
    default:
        return &proto.UpstreamState{}
    }
}
```

后续更干净的方向是 adapter registry 返回 state factory，但第一轮不要扩大抽象。

## Chat Completions 请求侧缺口

仅接 response scanner 不等于 `/v1/chat/completions` 已完整可用 Bedrock。

当前 chat handler 把 OpenAI chat body 原样交给 `provider/bedrock.PassthroughAdapter`；Bedrock Anthropic native body 需要 `anthropic_version/max_tokens/messages` 等字段。现有 Bedrock adapter 明确不 reshape body。

因此：

- A1-A7 只完成“Bedrock streaming response parser + forwarder 接入”。
- 真正 OpenAI-compatible chat-completions 到 Bedrock Anthropic，需要 A8 单独做 request translation：`OpenAI Chat -> Canonical -> Bedrock Anthropic native request`。
- A8 完成前，不能对外宣称“chat-completions 到 Bedrock Anthropic 完整兼容”。

另一个小修：

```go
type provider.BuildInput struct {
    ...
    Stream bool
}

type gateway.DispatchInput struct {
    ...
    Stream bool
}
```

`chat_completions_handler` 传 `Stream: req.Stream`；Bedrock adapter 用 `in.Stream || in.Credential.Extra["stream"] == "true"` 选择 stream endpoint，保留旧兼容。

## Atomic 拆分顺序

1. **A0：计划/spec 对齐**
   - 本 plan 完成后，如 Owner 要落地，补 Bedrock streaming 小 spec 或 protocol-translation implementer notes。

2. **A1：抽象 StreamScanner，SSE 行为不变**
   - 新增 `StreamEvent/StreamScanner/StreamScannerRegistry`。
   - `SSEStreamScanner` 包装现有 `ScanSSEEvents`。
   - `StreamForwarder` 改为从 scanner registry 取 scanner。
   - 测试：原 forwarder tests 全过；新增 registry miss 测试。

3. **A2：独立实现 AWS EventStream decoder**
   - 新包 `backend/internal/provider/bedrock/eventstream`。
   - 从官方 framing 事实自实现，不看、不复制 aws-sdk-go decoder。
   - 测试：正常帧、多帧、partial reader、prelude CRC 错、message CRC 错、length 越界、frame 超 limit、string headers。

4. **A3：BedrockEventStreamScanner**
   - 用 A2 decoder。
   - 解 `chunk.bytes` base64 为 Anthropic event JSON。
   - exception frame 转 typed scan error。
   - 测试用 synthetic EventStream frames，不访问 AWS。

5. **A4：`proto.BedrockEventStreamAdapter`**
   - 新增 adapter/state。
   - 映射 message_start、content_block_delta、message_delta usage、message_stop、unknown event。
   - 复用本地私有 helper，不注册成普通 `AnthropicAdapter`。

6. **A5：wire-up registry**
   - `bedrock_invoke -> &proto.BedrockEventStreamAdapter{}`。
   - default scanner registry 中 `bedrock_invoke -> BedrockEventStreamScanner`，其他 streaming family -> SSE。
   - `cmd/gateway/main.go` 注入两个 registry。

7. **A6：stream endpoint intent**
   - `BuildInput/DispatchInput` 增加 `Stream bool`。
   - chat handler 传 `req.Stream`。
   - Bedrock adapter 用 `in.Stream` 选 `/invoke-with-response-stream`。

8. **A7：无 AWS e2e smoke**
   - `httptest.Server` 返回 `application/vnd.amazon.eventstream` binary frames。
   - gateway 对客户端输出 `text/event-stream`。
   - 验证 graceful end、usage、first token flush。

9. **A8：请求体转换补齐（后续）**
   - 实装 OpenAI Chat request -> Bedrock Anthropic native request。
   - 建议走 F-PROTO-002 canonical request path，不塞进 provider passthrough。

## 测试策略：模拟 Bedrock 二进制流

测试 helper：

```go
func encodeTestEventStreamFrame(headers map[string]string, payload []byte) []byte
```

要求：

- test-only encoder 用 big endian + CRC32 写帧。
- 只支持测试需要的 string headers，减少和 production decoder 同错同过。
- 至少再放一个固定 hex fixture，防止 encoder/decoder 相互掩护。

Bedrock chunk fixture：

```json
{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}
```

base64 后包装：

```json
{"bytes":"<base64 anthropic event json>"}
```

frame headers：

```text
:message-type = event
:event-type = chunk
:content-type = application/json
```

Exception fixture：

```text
:message-type = exception
:event-type = throttlingException
:content-type = application/json
```

payload：

```json
{"message":"rate exceeded"}
```

期望：scanner 返回 `ErrBedrockStreamException{EventType:"throttlingException"}`；forwarder 分类为 `UpstreamRateLimit` 或至少不是 `ErrScannerOverflow`。

必须覆盖：

- `io.Pipe`/chunked reader 每次只吐 1-3 bytes。
- 每 10-20ms 写一帧，确认 first token timeout 内到达。
- `total_length < 16`。
- `headers_length > total_length - 16`。
- 超过 `MaxFrameBytes`。
- prelude CRC 错、message CRC 错。
- base64 非法。
- payload 非 JSON。
- decoded Anthropic event JSON 超过 `MaxEventBytes`。
- terminal 缺失 EOF -> `UpstreamEOFNoTerminal`。
- client disconnect 后 drain 仍可从 Bedrock scanner 读 usage。

## Clean-room 风险

AWS SDK 许可证判断：

- `aws-sdk-go` / AWS SDK v2 是 Apache-2.0，MIT 项目可学习概念或依赖。
- 但本项目 Bedrock SigV4 已自实现；本任务建议继续不新增 AWS SDK runtime dependency。
- 官方 AWS 文档足够确认 framing/content-type/response event 事实，建议实现者不要读 SDK decoder 源码。

允许：

- 使用 AWS 官方文档中的协议事实。
- 使用 Go 标准库 `encoding/binary`、`hash/crc32`、`encoding/base64`、`encoding/json`。
- 独立设计 HUAKAI 自有错误类型和测试 fixture。

禁止：

- 复制 aws-sdk-go decoder 源码、函数名、文件布局、注释或测试。
- 把 SDK 内部错误类型命名原样搬进 HUAKAI。
- 从 AGPL/LGPL relay 项目读取或搬运 Bedrock streaming 实现。

记录要求：

- 实施 PR 标注协议依据来自 AWS 官方文档，decoder 为 HUAKAI 独立实现。
- 如果实现者读过 SDK 源码，应记录 contamination，并让未读源码的 session 重写或 clean-room review。

## 主要风险

| 风险 | 影响 | 缓解 |
| --- | --- | --- |
| 把 EventStream 当 SSE 扫描 | binary frame 被截断/误读 | `StreamScanner` 抽象；Bedrock 不走 `bufio.Scanner` |
| length/CRC 未校验 | 错帧、恶意大帧、内存风险 | `io.ReadFull` + hard limits + CRC 校验 |
| `chunk.bytes` raw/SDK 语义混淆 | base64 双重 decode 或漏 decode | raw path 明确解析 `{"bytes":"..."}`；already-decoded 只做测试兼容 |
| HTTP 200 后出现 exception frame | forwarder 误判 graceful EOF | typed scan error + `classifyScanError` Bedrock 映射 |
| request body 未转换 | chat-completions 到 Bedrock 仍不可用 | A8 单独补 request translation；A1-A7 不宣称完整兼容 |
| 过度复用 AnthropicAdapter | Bedrock wire/error 特性被隐藏 | 独立 `BedrockEventStreamAdapter` + 独立 state |
| clean-room 污染 | MIT 开源风险 | 官方 docs + 自实现；禁止 verbatim SDK 代码 |
| registry 改动影响所有 provider | SSE 回归 | A1 先只重构 scanner abstraction，跑全量 forwarder/proto tests |

## Owner 决策点

1. 是否确认 binary decoder 放 `backend/internal/provider/bedrock/eventstream`，`backend/internal/proto` 只放 semantic adapter。
2. 是否允许本轮新增 `provider.BuildInput.Stream` / `gateway.DispatchInput.Stream`。
3. A8 request translation 是否纳入同一 vertical slice，还是 streaming response parser 合并后再做。
4. 是否禁止实现者读取 AWS SDK decoder 源码；Codex 建议禁止，官方文档足够。
5. Bedrock streaming 首发是否只支持 Anthropic models；其他 Bedrock models 先 `Feature Flag` 或 `Mandatory Roadmap`。

## 官方事实来源

- AWS EventStream message format：AWS official EventStreamMessage reference 描述 total length、headers length、prelude CRC、headers、payload、trailing CRC，CRC 使用 CRC32。
- Bedrock API reference：`InvokeModelWithResponseStream` response content-type 为 `application/vnd.amazon.eventstream`，stream body 有 `chunk` 和 exception events。
- Botocore Bedrock Runtime reference：`chunk.bytes` 是 payload data 的 base64-encoded bytes，并列出 stream exception variants。
- AWS Bedrock Anthropic streaming examples：SDK handler 对 chunk bytes 解 JSON，Anthropic event type 如 `content_block_delta` 可直接读取。

## 最小实施顺序建议

第一批做 A1-A4：单元层跑通 Bedrock 二进制流，不进生产 registry。

第二批做 A5-A7：HUAKAI gateway 无 AWS e2e smoke 通过。

第三批做 A8：OpenAI chat-completions 客户端真正调用 Bedrock Anthropic streaming。

这样避免把 wire parser、forwarder registry、provider endpoint、request translation、billing settlement 一次性混进一个高风险 patch。

