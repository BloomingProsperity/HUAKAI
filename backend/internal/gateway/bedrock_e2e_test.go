// bedrock_e2e_test.go — A6 atomic e2e 烟测：完整 Bedrock binary EventStream
// 链路 → forwarder pipeline → 客户端透传或 canonical event 翻译。
//
// 链路覆盖：
//  1. 合成 AWS Binary EventStream byte stream（含 prelude + headers + payload + CRC）
//  2. 走 BedrockEventStreamScanner（A3）切帧 + 解 chunk envelope
//  3. 走 bedrock.EventStreamAdapter（A4）转 CanonicalEvent
//  4. 走 StreamForwarder.Forward 写入 http.ResponseWriter
//
// 不依赖 AWS 网络，不引新依赖；与 bedrock_stream_scanner_test.go 共用
// encodeBedrockFrame helper（同 package）。
package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/provider/bedrock/eventstream"
)

// 注意：bedrock_stream_scanner_test.go 已定义同名 helper（encodeBedrockFrame /
// chunkPayload / chunkFrame），同 package 下复用即可，无需重复定义。

// bedrockE2EStream 拼接 Bedrock-on-Anthropic happy path 的 3 帧 binary 流：
// message_start + content_block_delta(text="Hello") + message_stop。
func bedrockE2EStream(t *testing.T) []byte {
	t.Helper()
	frame1 := chunkFrame(`{"type":"message_start","message":{"id":"msg_e2e","model":"claude-3-7-sonnet"}}`)
	frame2 := chunkFrame(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`)
	frame3 := chunkFrame(`{"type":"message_stop"}`)
	return append(append(frame1, frame2...), frame3...)
}

// TestBedrockE2E_ForwarderPipeline_Happy 验证完整链路：合成 binary stream
// → forwarder → ResponseRecorder 收到 SSE 输出 + draft.EndClass=Graceful。
func TestBedrockE2E_ForwarderPipeline_Happy(t *testing.T) {
	stream := bedrockE2EStream(t)
	upstream := bytes.NewReader(stream)

	recorder := httptest.NewRecorder()
	forwarder := newForwarder()

	draft, err := forwarder.Forward(context.Background(), upstream, recorder, ForwardRequest{
		ProtocolFamily: "bedrock_invoke",
		Model:          "claude-3-7-sonnet",
	})
	if err != nil {
		t.Fatalf("Forward err=%v", err)
	}
	if draft.EndClass != StreamEndGraceful {
		t.Errorf("EndClass=%q want %q", draft.EndClass, StreamEndGraceful)
	}

	body := recorder.Body.String()
	// 透传路径（无 ClientAdapter）写出 SSE 形态：event: <type>\ndata: <json>\n\n
	for _, want := range []string{"message_start", "content_block_delta", "Hello", "message_stop"} {
		if !strings.Contains(body, want) {
			t.Errorf("response body 缺 %q\n--- body ---\n%s", want, body)
		}
	}
}

// TestBedrockE2E_TruncatedFrame_PropagatesAsScanError 截断 binary 流应触发
// scanner ErrTruncatedFrame，forwarder 把它当 scan error 报告。
func TestBedrockE2E_TruncatedFrame_PropagatesAsScanError(t *testing.T) {
	stream := bedrockE2EStream(t)
	truncated := stream[:len(stream)-5]
	recorder := httptest.NewRecorder()
	forwarder := newForwarder()

	_, err := forwarder.Forward(context.Background(), bytes.NewReader(truncated), recorder, ForwardRequest{
		ProtocolFamily: "bedrock_invoke",
	})
	if err == nil {
		t.Fatal("截断帧应有 error 返回")
	}
	if !errors.Is(err, eventstream.ErrTruncatedFrame) {
		t.Errorf("err=%v want ErrTruncatedFrame", err)
	}
}

// TestBedrockE2E_ExceptionFrame_TerminatesWithError Bedrock exception 帧应
// 触发 ErrBedrockException 并通过 forwarder 报错。
func TestBedrockE2E_ExceptionFrame_TerminatesWithError(t *testing.T) {
	exception := encodeBedrockExceptionFrame(t, "ModelStreamErrorException", `{"message":"upstream rate limited"}`)
	recorder := httptest.NewRecorder()
	forwarder := newForwarder()

	_, err := forwarder.Forward(context.Background(), bytes.NewReader(exception), recorder, ForwardRequest{
		ProtocolFamily: "bedrock_invoke",
	})
	if err == nil {
		t.Fatal("exception 帧应有 error 返回")
	}
	if !errors.Is(err, ErrBedrockException) {
		t.Errorf("err=%v want ErrBedrockException", err)
	}
	// scanner 在 yield ErrBedrockException **之前**先 emit 一条 error SSEEvent，
	// forwarder.handleEventWithAdapter 透传到 ResponseRecorder。
	body := recorder.Body.String()
	if !strings.Contains(body, "rate limited") {
		t.Errorf("exception payload 应被 emit 到 body，得 %q", body)
	}
}

// TestBedrockE2E_RegistryWiring_BothLanesPresent 单元层守界：bedrock_invoke
// 在 ProtocolAdapters 与 Scanners 两 registry 都注册（任一缺失 forwarder 入口
// 即 fail-loud）。回归保险。
func TestBedrockE2E_RegistryWiring_BothLanesPresent(t *testing.T) {
	adapters := BuildDefaultProtocolAdapterRegistry()
	scanners := BuildDefaultStreamScannerRegistry()

	if _, err := adapters.For("bedrock_invoke"); err != nil {
		t.Errorf("ProtocolAdapters.For(bedrock_invoke) err=%v want nil", err)
	}
	if _, err := scanners.For("bedrock_invoke"); err != nil {
		t.Errorf("Scanners.For(bedrock_invoke) err=%v want nil", err)
	}
}

// encodeBedrockExceptionFrame 拼一个 :message-type=exception 帧，A3 scanner
// 见此 type 即 emit error SSEEvent + yield ErrBedrockException。
//
// 与 chunk frame 不同点：headers 里 :message-type 是 "exception" 而非 "event"，
// payload 是 raw JSON（非 chunk envelope）。CRC 等其它 wire format 同。
func encodeBedrockExceptionFrame(t *testing.T, exceptionType, payload string) []byte {
	t.Helper()
	headers := map[string]string{
		":message-type":   "exception",
		":exception-type": exceptionType,
	}
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
	msg.WriteString(payload)
	mc := crc32.ChecksumIEEE(msg.Bytes())
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], mc)
	msg.Write(crcBuf[:])
	return msg.Bytes()
}

// 静态使用断言：避免 unused import 警告（base64 / time 在 helpers 中由其它
// 测试文件间接使用，本文件如果不引用编译会失败）。
var _ = base64.StdEncoding
var _ time.Duration
var _ = fmt.Sprintf
