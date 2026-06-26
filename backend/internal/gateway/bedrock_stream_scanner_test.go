// bedrock_stream_scanner_test.go — A3 atomic 单测：
// 用 A2 eventstream test-only encoder 构造合成 Bedrock binary 流，
// 验证 scanner 行为：chunk envelope 解析、exception 终止、unknown skip。
//
// 不依赖 AWS 网络。
package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/provider/bedrock/eventstream"
)

// encodeBedrockFrame 是 test-only encoder：复制 eventstream package 的
// 测试 encoder 形状（避免循环 import；包内私有）。仅 string headers。
func encodeBedrockFrame(headers map[string]string, payload []byte) []byte {
	var hbuf bytes.Buffer
	for name, value := range headers {
		hbuf.WriteByte(byte(len(name)))
		hbuf.WriteString(name)
		hbuf.WriteByte(byte(eventstream.HeaderTypeString))
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(value)))
		hbuf.Write(l[:])
		hbuf.WriteString(value)
	}
	headersBytes := hbuf.Bytes()
	headersLen := uint32(len(headersBytes))
	const preludeSize = 12
	const messageCRCSize = 4
	totalLen := uint32(preludeSize + int(headersLen) + len(payload) + messageCRCSize)

	var pre [preludeSize]byte
	binary.BigEndian.PutUint32(pre[0:4], totalLen)
	binary.BigEndian.PutUint32(pre[4:8], headersLen)
	binary.BigEndian.PutUint32(pre[8:12], crc32.ChecksumIEEE(pre[0:8]))

	var msg bytes.Buffer
	msg.Write(pre[:])
	msg.Write(headersBytes)
	msg.Write(payload)
	mc := crc32.ChecksumIEEE(msg.Bytes())
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], mc)
	msg.Write(crcBuf[:])
	return msg.Bytes()
}

// chunkPayload 构造一个 Bedrock chunk envelope（含 base64 编码的内层 JSON）。
func chunkPayload(innerJSON string) []byte {
	encoded := base64.StdEncoding.EncodeToString([]byte(innerJSON))
	return []byte(fmt.Sprintf(`{"bytes":%q}`, encoded))
}

func chunkFrame(innerJSON string) []byte {
	return encodeBedrockFrame(
		map[string]string{":message-type": "event", ":event-type": "chunk", ":content-type": "application/json"},
		chunkPayload(innerJSON),
	)
}

func collectBedrockEvents(t *testing.T, stream []byte) ([]SSEEvent, error) {
	t.Helper()
	scanner := &BedrockEventStreamScanner{}
	var out []SSEEvent
	var lastErr error
	for evt, err := range scanner.Scan(context.Background(), bytes.NewReader(stream), 0) {
		if err != nil {
			lastErr = err
			break
		}
		out = append(out, evt)
	}
	return out, lastErr
}

func TestBedrockScanner_HappyPath_ThreeChunks(t *testing.T) {
	frame1 := chunkFrame(`{"type":"message_start","message":{"id":"x"}}`)
	frame2 := chunkFrame(`{"type":"content_block_delta","delta":{"text":"hi"}}`)
	frame3 := chunkFrame(`{"type":"message_stop"}`)
	stream := append(append(frame1, frame2...), frame3...)

	events, err := collectBedrockEvents(t, stream)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(events) != 3 {
		t.Fatalf("event count=%d want 3", len(events))
	}
	wantTypes := []string{"message_start", "content_block_delta", "message_stop"}
	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Errorf("[%d] Type=%q want %q", i, events[i].Type, want)
		}
		if !strings.Contains(string(events[i].Data), want) {
			t.Errorf("[%d] Data=%q 不含 %q", i, events[i].Data, want)
		}
	}
}

func TestBedrockScanner_ExceptionTerminates(t *testing.T) {
	// chunk + exception → exception 之后流终止
	chunk := chunkFrame(`{"type":"message_start"}`)
	exception := encodeBedrockFrame(
		map[string]string{":message-type": "exception", ":exception-type": "ModelStreamErrorException"},
		[]byte(`{"message":"upstream rate limited"}`),
	)
	postException := chunkFrame(`{"type":"should_not_arrive"}`)
	stream := append(append(chunk, exception...), postException...)

	scanner := &BedrockEventStreamScanner{}
	var events []SSEEvent
	var lastErr error
	for evt, err := range scanner.Scan(context.Background(), bytes.NewReader(stream), 0) {
		if err != nil {
			lastErr = err
			break
		}
		events = append(events, evt)
	}

	// 期望：[chunk message_start, exception error event] 共 2 条 + ErrBedrockException 终止
	if len(events) != 2 {
		t.Fatalf("event count=%d want 2 (chunk+exception emit)", len(events))
	}
	if events[0].Type != "message_start" {
		t.Errorf("event[0].Type=%q want message_start", events[0].Type)
	}
	if events[1].Type != "error" {
		t.Errorf("event[1].Type=%q want error", events[1].Type)
	}
	if !strings.Contains(string(events[1].Data), "rate limited") {
		t.Errorf("exception payload missing: %q", events[1].Data)
	}
	if !errors.Is(lastErr, ErrBedrockException) {
		t.Errorf("err=%v want ErrBedrockException", lastErr)
	}
}

func TestBedrockScanner_EventExceptionTypeTerminates(t *testing.T) {
	chunk := chunkFrame(`{"type":"message_start"}`)
	exception := encodeBedrockFrame(
		map[string]string{":message-type": "event", ":event-type": "modelStreamErrorException"},
		[]byte(`{"message":"upstream throttled"}`),
	)
	postException := chunkFrame(`{"type":"message_stop"}`)
	stream := append(append(chunk, exception...), postException...)

	scanner := &BedrockEventStreamScanner{}
	var events []SSEEvent
	var lastErr error
	for evt, err := range scanner.Scan(context.Background(), bytes.NewReader(stream), 0) {
		if err != nil {
			lastErr = err
			break
		}
		events = append(events, evt)
	}

	if !errors.Is(lastErr, ErrBedrockException) {
		t.Fatalf("err=%v want ErrBedrockException", lastErr)
	}
	if len(events) != 2 {
		t.Fatalf("event count=%d want 2 (chunk+exception emit)", len(events))
	}
	if events[0].Type != "message_start" {
		t.Errorf("event[0].Type=%q want message_start", events[0].Type)
	}
	if events[1].Type != "error" {
		t.Errorf("event[1].Type=%q want error", events[1].Type)
	}
	if !strings.Contains(string(events[1].Data), "upstream throttled") {
		t.Errorf("exception payload missing: %q", events[1].Data)
	}
	for i, evt := range events {
		if evt.Type == "message_stop" {
			t.Fatalf("event[%d] delivered clean terminal after exception: %+v", i, evt)
		}
	}
}

func TestBedrockScanner_UnknownMessageTypeTerminates(t *testing.T) {
	chunk := chunkFrame(`{"type":"message_start"}`)
	unknown := encodeBedrockFrame(
		map[string]string{":message-type": "unexpected-control"},
		[]byte(`{"message":"unknown frame"}`),
	)
	postUnknown := chunkFrame(`{"type":"message_stop"}`)
	stream := append(append(chunk, unknown...), postUnknown...)

	scanner := &BedrockEventStreamScanner{}
	var events []SSEEvent
	var lastErr error
	for evt, err := range scanner.Scan(context.Background(), bytes.NewReader(stream), 0) {
		if err != nil {
			lastErr = err
			break
		}
		events = append(events, evt)
	}

	if !errors.Is(lastErr, ErrBedrockException) {
		t.Fatalf("err=%v want ErrBedrockException", lastErr)
	}
	if len(events) != 2 {
		t.Fatalf("event count=%d want 2 (chunk+unknown-type error emit)", len(events))
	}
	if events[1].Type != "error" {
		t.Errorf("event[1].Type=%q want error", events[1].Type)
	}
	if !strings.Contains(string(events[1].Data), "unknown frame") {
		t.Errorf("unknown message payload missing: %q", events[1].Data)
	}
	for i, evt := range events {
		if evt.Type == "message_stop" {
			t.Fatalf("event[%d] delivered clean terminal after unknown message type: %+v", i, evt)
		}
	}
}

func TestBedrockScanner_InitialResponseEventSkipped(t *testing.T) {
	// Smithy Event Stream RPC 协议可能发送 initial-response 控制事件。
	// 它们不是 Bedrock 模型输出，可以跳过而不会掩盖错误。
	control := encodeBedrockFrame(
		map[string]string{":message-type": "event", ":event-type": "initial-response"},
		[]byte(`{"requestId":"control"}`),
	)
	chunk := chunkFrame(`{"type":"message_stop"}`)
	stream := append(control, chunk...)

	events, err := collectBedrockEvents(t, stream)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	// 只 emit chunk，不 emit control event
	if len(events) != 1 {
		t.Fatalf("event count=%d want 1 (only the chunk)", len(events))
	}
	if events[0].Type != "message_stop" {
		t.Errorf("got Type=%q want message_stop", events[0].Type)
	}
}

func TestBedrockScanner_TruncatedFramePropagates(t *testing.T) {
	frame := chunkFrame(`{"type":"message_start"}`)
	// 截到一半 → decoder ErrTruncatedFrame
	truncated := frame[:len(frame)-5]

	scanner := &BedrockEventStreamScanner{}
	var lastErr error
	for _, err := range scanner.Scan(context.Background(), bytes.NewReader(truncated), 0) {
		if err != nil {
			lastErr = err
			break
		}
	}
	if !errors.Is(lastErr, eventstream.ErrTruncatedFrame) {
		t.Errorf("err=%v want ErrTruncatedFrame", lastErr)
	}
}

func TestBedrockScanner_BadBase64InChunk(t *testing.T) {
	// chunk envelope 含非 base64 字符
	bad := []byte(`{"bytes":"not-valid-base64-because-of-this-special-char-@@@"}`)
	frame := encodeBedrockFrame(
		map[string]string{":message-type": "event", ":event-type": "chunk"},
		bad,
	)
	scanner := &BedrockEventStreamScanner{}
	var lastErr error
	for _, err := range scanner.Scan(context.Background(), bytes.NewReader(frame), 0) {
		if err != nil {
			lastErr = err
			break
		}
	}
	if !errors.Is(lastErr, ErrBedrockChunkPayload) {
		t.Errorf("err=%v want ErrBedrockChunkPayload", lastErr)
	}
}

func TestBedrockScanner_BadJSONInChunkEnvelope(t *testing.T) {
	frame := encodeBedrockFrame(
		map[string]string{":message-type": "event", ":event-type": "chunk"},
		[]byte(`{this is not json`),
	)
	scanner := &BedrockEventStreamScanner{}
	var lastErr error
	for _, err := range scanner.Scan(context.Background(), bytes.NewReader(frame), 0) {
		if err != nil {
			lastErr = err
			break
		}
	}
	if !errors.Is(lastErr, ErrBedrockChunkPayload) {
		t.Errorf("err=%v want ErrBedrockChunkPayload", lastErr)
	}
}

func TestBedrockScanner_ContextCancellation(t *testing.T) {
	frame := chunkFrame(`{"type":"message_start"}`)
	stream := append(frame, frame...)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	scanner := &BedrockEventStreamScanner{}
	var lastErr error
	for _, err := range scanner.Scan(ctx, bytes.NewReader(stream), 0) {
		if err != nil {
			lastErr = err
			break
		}
	}
	if !errors.Is(lastErr, context.Canceled) {
		// 注意：取决于 IO 完成时机，可能在 ReadMessage 内部读完后才检查 ctx
		// 也可能直接 ctx.Err — 两种都可接受，只要错误传递回来
		if lastErr == nil {
			t.Errorf("ctx 取消应传播错误，得到 nil")
		}
	}
}

func TestBedrockScanner_EmptyStream(t *testing.T) {
	events, err := collectBedrockEvents(t, nil)
	if err != nil {
		t.Errorf("空流应优雅结束（io.EOF 不进 yield），err=%v", err)
	}
	if len(events) != 0 {
		t.Errorf("空流应无 event，得到 %d", len(events))
	}
}

// 验证 io.EOF 不被 yield 为 error（应直接 return 终止 iterator）
var _ io.Reader = (*bytes.Reader)(nil)
