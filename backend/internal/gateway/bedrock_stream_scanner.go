// bedrock_stream_scanner.go — A3 atomic：把 AWS Bedrock 二进制 EventStream
// 转换为 forwarder 消费的 SSEEvent 流。
//
// 与 SSEStreamScanner 同形态实现 StreamScanner 接口，但底层走
// provider/bedrock/eventstream 包的 binary frame decoder，不走 SSE 行扫描。
//
// 行为概要：
//   - 循环读 binary frame
//   - 按 :message-type / :event-type header 分支
//   - "event" + "chunk" → payload 是 {"bytes":"<base64>"}，解 base64 得
//     内层 Anthropic event JSON → 提取 {"type":"..."} → emit 为
//     SSEEvent{Type: 内层 type, Data: 原 JSON 字节}
//   - "exception" / "error" → emit 为 protocol-level error
//     SSEEvent（Type="error", Data=原 payload），随后 yield ErrBedrockException
//     结束流（R4 决策：当 protocol-level error 处理）
//   - Bedrock response exception event-types / unknown message-type →
//     protocol-level error；明确的 control event 才可跳过
//   - decoder 错误传播为 (SSEEvent{}, err)，scanner 退出
//
// 设计约束（与 codex_session 同条款）：
// 不读 aws-sdk-go EventStreamHandler / TranscribeAdapter
//     等 reference 实现；逻辑基于 AWS Bedrock 公开文档的 streaming response
//     形态推导
//   - 只支持 Anthropic-on-Bedrock 子集（chunk envelope = {"bytes": ...}）；
//     Llama-on-Bedrock / Cohere-on-Bedrock 等 chunk 内部形态可能不同，
//     待 A4 / OCAW 验证后扩展
package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/provider/bedrock/eventstream"
	"github.com/BloomingProsperity/HUAKAI/internal/redact"
)

// ErrBedrockException 表示 Bedrock 后端发回了 :message-type=exception 帧。
// scanner 把 exception payload 作为最后一条 SSEEvent emit 后，再 yield 此 error。
var ErrBedrockException = errors.New("gateway: Bedrock EventStream 返回 exception 帧")

// ErrBedrockChunkPayload 表示 chunk envelope 解析失败（base64 / JSON 错）。
var ErrBedrockChunkPayload = errors.New("gateway: Bedrock chunk envelope 解析失败")

var bedrockExceptionEventTypes = map[string]struct{}{
	"internalServerException":     {},
	"modelStreamErrorException":   {},
	"modelTimeoutException":       {},
	"serviceUnavailableException": {},
	"throttlingException":         {},
	"validationException":         {},
}

// BedrockEventStreamScanner 实现 StreamScanner，把二进制 EventStream
// 切帧并解 chunk envelope 为内层 SSEEvent。
//
// Limits 留零值时用 eventstream.Decoder 默认（16 MiB / frame）。
type BedrockEventStreamScanner struct {
	Limits eventstream.Limits
}

// 编译期 interface 断言
var _ StreamScanner = (*BedrockEventStreamScanner)(nil)

// Scan 实现 StreamScanner 接口。
//
// bufferCap 当前未使用：binary EventStream 的内存上限由 Limits.MaxMessageBytes
// 控制，不是 SSE-style line buffer 大小。保留入参签名以与 StreamScanner 兼容。
func (s *BedrockEventStreamScanner) Scan(ctx context.Context, r io.Reader, bufferCap int) iter.Seq2[SSEEvent, error] {
	return func(yield func(SSEEvent, error) bool) {
		dec := &eventstream.Decoder{Limits: s.Limits}
		for {
			// ctx 取消短路（IO 之间）
			select {
			case <-ctx.Done():
				yield(SSEEvent{}, ctx.Err())
				return
			default:
			}

			msg, err := dec.ReadMessage(ctx, r)
			if errors.Is(err, io.EOF) {
				return // 流正常结束
			}
			if err != nil {
				yield(SSEEvent{}, err)
				return
			}

			// 按 :message-type 分支
			messageType := msg.Headers[":message-type"].String
			eventType := msg.Headers[":event-type"].String

			switch messageType {
			case "event":
				if !s.handleEventFrame(eventType, msg.Payload, yield) {
					return
				}
			case "exception", "error":
				// R4 决策：当 protocol-level error；emit error event + 终止
				yieldBedrockProtocolError("message-type", messageType, msg.Payload, yield)
				return
			default:
				yieldBedrockProtocolError("message-type", messageType, msg.Payload, yield)
				return
			}
		}
	}
}

// handleEventFrame 处理 :message-type=event 帧。当前只识别 :event-type=chunk
// 的 Anthropic-on-Bedrock 形态。返回 false 表示 yield 收到 stop 信号。
func (s *BedrockEventStreamScanner) handleEventFrame(eventType string, payload []byte, yield func(SSEEvent, error) bool) bool {
	switch eventType {
	case "chunk":
	case "initial-response":
		return true
	default:
		if _, ok := bedrockExceptionEventTypes[eventType]; ok {
			return yieldBedrockProtocolError("exception event-type", eventType, payload, yield)
		}
		return yieldBedrockProtocolError("event-type", eventType, payload, yield)
	}

	// chunk envelope 形态：{"bytes": "<base64-encoded inner JSON>"}
	var envelope struct {
		Bytes string `json:"bytes"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return yield(SSEEvent{}, fmt.Errorf("%w: chunk envelope JSON 解析失败: %v", ErrBedrockChunkPayload, err))
	}
	innerJSON, err := base64.StdEncoding.DecodeString(envelope.Bytes)
	if err != nil {
		return yield(SSEEvent{}, fmt.Errorf("%w: chunk bytes base64 解码失败: %v", ErrBedrockChunkPayload, err))
	}

	// 提取内层 type 字段（Anthropic event JSON 顶层有 "type":"..."）
	var inner struct {
		Type string `json:"type"`
	}
	// 解析失败不致命：仍可作为 anonymous SSEEvent emit（forwarder 下游
	// adapter 自己再解析）；只是 SSEEvent.Type 为空。
	_ = json.Unmarshal(innerJSON, &inner)

	return yield(SSEEvent{
		Type:       inner.Type,
		Data:       innerJSON,
		ObservedAt: time.Now(),
	}, nil)
}

func yieldBedrockProtocolError(kind, value string, payload []byte, yield func(SSEEvent, error) bool) bool {
	if !yield(SSEEvent{Type: "error", Data: payload, ObservedAt: time.Now()}, nil) {
		return false
	}
	yield(SSEEvent{}, fmt.Errorf("%w: %s=%q payload_summary=%s", ErrBedrockException, kind, value, redact.SafePayloadLogSummary(payload)))
	return false
}

// 参阅的来源文件:
//   - https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_InvokeModelWithResponseStream.html (Bedrock 流式响应形态)
//   - https://docs.aws.amazon.com/bedrock/latest/userguide/inference-invoke-stream.html (chunk envelope:bytes 字段,base64 编码的内层 JSON)
//   - HUAKAI 内部:backend/internal/provider/bedrock/eventstream/decoder.go (A2 atomic)
// Lane: claude
// Time: 2026-05-07T<UTC>
