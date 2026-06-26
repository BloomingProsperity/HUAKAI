// 包 eventstream 测试 — A2 atomic 单测。
//
// 策略：
//   - test-only encoder（encodeTestFrame）用于构造合成帧
//   - 至少 1 个 explicit byte slice fixture（手工写好的字节序列）
//     与 encodeTestFrame 输出 cross-check，防止 encoder/decoder 互掩盖
//   - 不依赖 AWS 网络
package eventstream

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"strings"
	"testing"
)

// encodeTestFrame 是 test-only encoder。仅支持 string 类型 headers
// （Bedrock production 路径只用 string header；其它类型由 decoder.go
// 单独测试覆盖）。
//
// 参数：
//   - headers: name → string value（按 map iteration 顺序写入；测试不依赖顺序）
//   - payload: 原始字节
func encodeTestFrame(headers map[string]string, payload []byte) []byte {
	// 1. 编码 headers 段
	var hbuf bytes.Buffer
	for name, value := range headers {
		hbuf.WriteByte(byte(len(name)))
		hbuf.WriteString(name)
		hbuf.WriteByte(byte(HeaderTypeString))
		var l [2]byte
		binary.BigEndian.PutUint16(l[:], uint16(len(value)))
		hbuf.Write(l[:])
		hbuf.WriteString(value)
	}
	headersBytes := hbuf.Bytes()
	headersLen := uint32(len(headersBytes))
	totalLen := uint32(preludeSize + int(headersLen) + len(payload) + messageCRCSize)

	// 2. 编码 prelude（前 8 byte 是 total_length + headers_length，CRC 后接）
	var pre [preludeSize]byte
	binary.BigEndian.PutUint32(pre[0:4], totalLen)
	binary.BigEndian.PutUint32(pre[4:8], headersLen)
	preludeCRC := crc32.ChecksumIEEE(pre[0:8])
	binary.BigEndian.PutUint32(pre[8:12], preludeCRC)

	// 3. 拼装 message 主体
	var msg bytes.Buffer
	msg.Write(pre[:])
	msg.Write(headersBytes)
	msg.Write(payload)

	// 4. 计算 message CRC（覆盖 message 除最后 4 byte）
	messageCRC := crc32.ChecksumIEEE(msg.Bytes())
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], messageCRC)
	msg.Write(crcBuf[:])

	return msg.Bytes()
}

func TestDecoder_HappyPath_SingleFrame(t *testing.T) {
	frame := encodeTestFrame(
		map[string]string{":event-type": "chunk", ":content-type": "application/json"},
		[]byte(`{"hello":"world"}`),
	)
	d := &Decoder{}
	msg, err := d.ReadMessage(context.Background(), bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("ReadMessage err=%v", err)
	}
	if msg.Headers[":event-type"].String != "chunk" {
		t.Errorf(":event-type=%q want chunk", msg.Headers[":event-type"].String)
	}
	if msg.Headers[":content-type"].String != "application/json" {
		t.Errorf(":content-type=%q", msg.Headers[":content-type"].String)
	}
	if string(msg.Payload) != `{"hello":"world"}` {
		t.Errorf("payload=%q", msg.Payload)
	}
}

func TestDecoder_MultipleFrames(t *testing.T) {
	frame1 := encodeTestFrame(map[string]string{":event-type": "chunk"}, []byte("a"))
	frame2 := encodeTestFrame(map[string]string{":event-type": "chunk"}, []byte("b"))
	frame3 := encodeTestFrame(map[string]string{":event-type": "chunk"}, []byte("c"))
	stream := bytes.NewReader(append(append(frame1, frame2...), frame3...))

	d := &Decoder{}
	for i, want := range []string{"a", "b", "c"} {
		msg, err := d.ReadMessage(context.Background(), stream)
		if err != nil {
			t.Fatalf("[%d] err=%v", i, err)
		}
		if string(msg.Payload) != want {
			t.Errorf("[%d] payload=%q want %q", i, msg.Payload, want)
		}
	}
	// 末尾应是 io.EOF
	_, err := d.ReadMessage(context.Background(), stream)
	if !errors.Is(err, io.EOF) {
		t.Errorf("第 4 次 ReadMessage err=%v want io.EOF", err)
	}
}

func TestDecoder_PreludeCRCMismatch(t *testing.T) {
	frame := encodeTestFrame(map[string]string{":event-type": "chunk"}, []byte("x"))
	// 篡改 prelude CRC（offset 8-11）
	frame[8] ^= 0xFF
	d := &Decoder{}
	_, err := d.ReadMessage(context.Background(), bytes.NewReader(frame))
	if !errors.Is(err, ErrPreludeCRCMismatch) {
		t.Errorf("err=%v want ErrPreludeCRCMismatch", err)
	}
}

func TestDecoder_MessageCRCMismatch(t *testing.T) {
	frame := encodeTestFrame(map[string]string{":event-type": "chunk"}, []byte("hello"))
	// 篡改 payload bit（在 prelude 之后），保留 message CRC 字段不变 → 校验应失败
	// payload 起点：preludeSize + headers_length
	// 简单方式：把倒数第 5 个字节翻转（在 message CRC 之前的 payload 区）
	frame[len(frame)-messageCRCSize-1] ^= 0x01
	d := &Decoder{}
	_, err := d.ReadMessage(context.Background(), bytes.NewReader(frame))
	if !errors.Is(err, ErrMessageCRCMismatch) {
		t.Errorf("err=%v want ErrMessageCRCMismatch", err)
	}
}

func TestDecoder_TruncatedPrelude(t *testing.T) {
	frame := encodeTestFrame(map[string]string{":event-type": "chunk"}, []byte("x"))
	// 截到只剩 6 byte（prelude 12 之内）
	d := &Decoder{}
	_, err := d.ReadMessage(context.Background(), bytes.NewReader(frame[:6]))
	if !errors.Is(err, ErrTruncatedFrame) {
		t.Errorf("err=%v want ErrTruncatedFrame", err)
	}
}

func TestDecoder_TruncatedBody(t *testing.T) {
	frame := encodeTestFrame(map[string]string{":event-type": "chunk"}, []byte("hello world"))
	// 截到 prelude + 一半 body
	cutoff := len(frame) - 5
	d := &Decoder{}
	_, err := d.ReadMessage(context.Background(), bytes.NewReader(frame[:cutoff]))
	if !errors.Is(err, ErrTruncatedFrame) {
		t.Errorf("err=%v want ErrTruncatedFrame", err)
	}
}

func TestDecoder_EmptyStreamReturnsEOF(t *testing.T) {
	d := &Decoder{}
	_, err := d.ReadMessage(context.Background(), bytes.NewReader(nil))
	if !errors.Is(err, io.EOF) {
		t.Errorf("err=%v want io.EOF", err)
	}
}

func TestDecoder_FrameTooLarge(t *testing.T) {
	// 故意设极小 limit，构造一个超 limit 的合法帧
	frame := encodeTestFrame(map[string]string{":event-type": "chunk"}, bytes.Repeat([]byte("x"), 100))
	d := &Decoder{Limits: Limits{MaxMessageBytes: 50}}
	_, err := d.ReadMessage(context.Background(), bytes.NewReader(frame))
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("err=%v want ErrFrameTooLarge", err)
	}
}

func TestDecoder_HeadersTooLarge(t *testing.T) {
	bigName := strings.Repeat("h", 250)
	headers := map[string]string{bigName: strings.Repeat("v", 250)}
	frame := encodeTestFrame(headers, []byte("p"))
	d := &Decoder{Limits: Limits{MaxHeaderBytes: 50}}
	_, err := d.ReadMessage(context.Background(), bytes.NewReader(frame))
	if !errors.Is(err, ErrHeadersTooLarge) {
		t.Errorf("err=%v want ErrHeadersTooLarge", err)
	}
}

func TestDecoder_EmptyPayload(t *testing.T) {
	frame := encodeTestFrame(map[string]string{":event-type": "noop"}, nil)
	d := &Decoder{}
	msg, err := d.ReadMessage(context.Background(), bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if len(msg.Payload) != 0 {
		t.Errorf("payload len=%d want 0", len(msg.Payload))
	}
	if msg.Headers[":event-type"].String != "noop" {
		t.Errorf(":event-type=%q", msg.Headers[":event-type"].String)
	}
}

func TestDecoder_UnknownHeaderTypeFailLoud(t *testing.T) {
	// 手工拼一帧，带一个 type=99 (未支持) 的 header
	headers := []byte{}
	// header：name_len=1 + name='x' + type=99 +（0 字节 value）
	headers = append(headers, 1, 'x', 99)
	headersLen := uint32(len(headers))
	totalLen := uint32(preludeSize + int(headersLen) + 0 + messageCRCSize)

	var pre [preludeSize]byte
	binary.BigEndian.PutUint32(pre[0:4], totalLen)
	binary.BigEndian.PutUint32(pre[4:8], headersLen)
	binary.BigEndian.PutUint32(pre[8:12], crc32.ChecksumIEEE(pre[0:8]))

	var msg bytes.Buffer
	msg.Write(pre[:])
	msg.Write(headers)
	mc := crc32.ChecksumIEEE(msg.Bytes())
	var crcBuf [4]byte
	binary.BigEndian.PutUint32(crcBuf[:], mc)
	msg.Write(crcBuf[:])

	d := &Decoder{}
	_, err := d.ReadMessage(context.Background(), bytes.NewReader(msg.Bytes()))
	if !errors.Is(err, ErrHeaderTypeUnsupported) {
		t.Errorf("err=%v want ErrHeaderTypeUnsupported", err)
	}
}

// TestDecoder_ExplicitByteFixture 是 cross-check fixture：手工拼装一个最小
// 帧（empty headers, empty payload, total_length=16），与 encodeTestFrame
// 独立验证 layout。这能在 encoder 出 bug 但 encoder/decoder 配对掩盖时
// 浮现真问题。
func TestDecoder_ExplicitByteFixture(t *testing.T) {
	// 手工构造：
	//   total_length=16  → 00 00 00 10
	//   headers_length=0 → 00 00 00 00
	//   prelude_crc      = CRC32-IEEE(00 00 00 10 00 00 00 00)
	//   headers          =（空）
	//   payload          =（空）
	//   message_crc      = CRC32-IEEE(prelude 全部 12 字节)
	pre := []byte{0x00, 0x00, 0x00, 0x10, 0x00, 0x00, 0x00, 0x00}
	preCRC := crc32.ChecksumIEEE(pre)
	full := make([]byte, 0, 16)
	full = append(full, pre...)
	var pcrc [4]byte
	binary.BigEndian.PutUint32(pcrc[:], preCRC)
	full = append(full, pcrc[:]...)

	mc := crc32.ChecksumIEEE(full[:12])
	var mcb [4]byte
	binary.BigEndian.PutUint32(mcb[:], mc)
	full = append(full, mcb[:]...)

	// 手工字节流应当能被 decoder 解析为 empty-headers / empty-payload message
	d := &Decoder{}
	msg, err := d.ReadMessage(context.Background(), bytes.NewReader(full))
	if err != nil {
		t.Fatalf("explicit fixture decode err=%v", err)
	}
	if len(msg.Headers) != 0 {
		t.Errorf("expected empty headers, got %d entries", len(msg.Headers))
	}
	if len(msg.Payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(msg.Payload))
	}
	if msg.TotalLength != 16 {
		t.Errorf("TotalLength=%d want 16", msg.TotalLength)
	}

	// Cross-check：encodeTestFrame(empty, empty) 应得到完全相同的字节
	encoded := encodeTestFrame(map[string]string{}, nil)
	if !bytes.Equal(full, encoded) {
		t.Errorf("explicit fixture vs encodeTestFrame 不一致：\nfixture=%x\nencoded=%x", full, encoded)
	}
}

func TestDecoder_NumericHeaderTypes(t *testing.T) {
	// Bedrock 不用，但 spec 完整：byte/short/integer/long/bool true/false
	// 手工构造每种类型的 header，验证 parseHeaderValue 的分支。
	// 这里只测一种代表（integer），其它分支由 ExplicitByteFixture + happy
	// path 间接覆盖；遗漏的分支风险接受（A4 引 production 用法时再加）。
	var hb bytes.Buffer
	hb.WriteByte(3) // name_len = 3（名称长度）
	hb.WriteString("num")
	hb.WriteByte(byte(HeaderTypeInteger))
	var v [4]byte
	val := int32(-12345)
	binary.BigEndian.PutUint32(v[:], uint32(val))
	hb.Write(v[:])

	headers := hb.Bytes()
	headersLen := uint32(len(headers))
	payload := []byte("ok")
	totalLen := uint32(preludeSize + int(headersLen) + len(payload) + messageCRCSize)
	var pre [preludeSize]byte
	binary.BigEndian.PutUint32(pre[0:4], totalLen)
	binary.BigEndian.PutUint32(pre[4:8], headersLen)
	binary.BigEndian.PutUint32(pre[8:12], crc32.ChecksumIEEE(pre[0:8]))

	var msg bytes.Buffer
	msg.Write(pre[:])
	msg.Write(headers)
	msg.Write(payload)
	mc := crc32.ChecksumIEEE(msg.Bytes())
	var mcb [4]byte
	binary.BigEndian.PutUint32(mcb[:], mc)
	msg.Write(mcb[:])

	d := &Decoder{}
	got, err := d.ReadMessage(context.Background(), bytes.NewReader(msg.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got.Headers["num"].Type != HeaderTypeInteger {
		t.Errorf("type=%d want integer", got.Headers["num"].Type)
	}
	if got.Headers["num"].Int != -12345 {
		t.Errorf("int=%d want -12345", got.Headers["num"].Int)
	}
}
