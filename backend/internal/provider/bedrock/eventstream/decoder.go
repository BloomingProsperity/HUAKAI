// 包 eventstream — AWS Binary Event Stream wire-format decoder（自实现，
// clean-room）。
//
// 实现依据：AWS 官方 Event Stream wire format 公开规范（不读 aws-sdk-go）。
// 仅覆盖 Bedrock invoke-with-response-stream 实际使用的子集 + 测试需要的
// 边界（CRC、长度限制、字符串 header）。
//
// 帧布局（big-endian / network byte order）：
//
//   ┌──────────────────────────────────────────────────────────────┐
//   │ Prelude (12 bytes)                                           │
//   │   total_length    uint32   整 message 字节数（含 prelude+CRC）│
//   │   headers_length  uint32   headers 段字节数                   │
//   │   prelude_crc     uint32   CRC32(IEEE) of 前 8 byte           │
//   ├──────────────────────────────────────────────────────────────┤
//   │ Headers (headers_length bytes)                               │
//   │   每条 = name_len(uint8) + name(utf8) + value_type(uint8)    │
//   │           + value(type-specific)                             │
//   ├──────────────────────────────────────────────────────────────┤
//   │ Payload (total_length - headers_length - 16 bytes)           │
//   ├──────────────────────────────────────────────────────────────┤
//   │ message_crc (4 bytes) CRC32(IEEE) of 整 message 除最后 4 byte │
//   └──────────────────────────────────────────────────────────────┘
//
// 设计约束：
//   - 必须 io.ReadFull，不用 bufio.Scanner（行扫描会切碎 binary）
//   - CRC32 用 IEEE polynomial（crc32.IEEETable）— **不是** Castagnoli
//   - 所有长度字段在写入 Message 前 sanity-check（防恶意/损坏帧）
//   - ctx 取消必须传播：每帧读完 prelude 后检查一次
//
// 不实现（A2 范围外）：
//   - 写帧（wire encoder） — 测试用 helper 自带 encoder，不暴露 production API
//   - timestamp/uuid 等 Bedrock 不用的 header 类型 — Skip + skip-byte 计数即可
package eventstream

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

// HeaderValueType 是 AWS spec 定义的 9 种 header 值类型枚举。
// 当前 production 路径只用 String (7)；其它类型保留以便正确跳过未知 header。
type HeaderValueType uint8

const (
	HeaderTypeBoolTrue  HeaderValueType = 0
	HeaderTypeBoolFalse HeaderValueType = 1
	HeaderTypeByte      HeaderValueType = 2 // int8
	HeaderTypeShort     HeaderValueType = 3 // int16
	HeaderTypeInteger   HeaderValueType = 4 // int32
	HeaderTypeLong      HeaderValueType = 5 // int64
	HeaderTypeByteArray HeaderValueType = 6 // uint16 len + bytes
	HeaderTypeString    HeaderValueType = 7 // uint16 len + utf8 bytes (最常见)
	HeaderTypeTimestamp HeaderValueType = 8 // int64 ms epoch
	HeaderTypeUUID      HeaderValueType = 9 // 16 bytes
)

// HeaderValue 携带 header 解码后的值。Type 决定如何解读 String/Bytes/Int 等字段。
// 当前阶段 production code 只读 String (供 Bedrock :event-type 等 header 用)；
// 其它类型字段保留以备将来扩展。
type HeaderValue struct {
	Type   HeaderValueType
	String string
	Bytes  []byte
	Int    int64
	Bool   bool
}

// Message 是解码后的单帧。Headers 已 parse 为 name → HeaderValue map；
// Payload 是原始 byte slice（由 caller 决定如何 deserialize：Bedrock chunk
// 是 JSON {"bytes":"<base64>"}，由 BedrockEventStreamScanner 处理）。
type Message struct {
	Headers       map[string]HeaderValue
	Payload       []byte
	TotalLength   uint32
	HeadersLength uint32
}

// Limits 控制帧解码的安全上限。零值字段使用合理默认。
//
// 默认值参考 AWS spec：
//   - MaxMessageBytes 16 MiB（AWS 单 message 上限）
//   - MaxHeaderBytes  128 KiB（AWS headers 段上限）
//   - MaxPayloadBytes 16 MiB（max_message - prelude - max_headers）
type Limits struct {
	MaxMessageBytes int
	MaxHeaderBytes  int
	MaxPayloadBytes int
}

// 默认上限常量。caller 可在 Decoder.Limits 覆盖。
const (
	defaultMaxMessageBytes = 16 << 20 // 16 MiB
	defaultMaxHeaderBytes  = 128 << 10
	defaultMaxPayloadBytes = 16 << 20
	preludeSize            = 12
	messageCRCSize         = 4
	frameOverhead          = preludeSize + messageCRCSize // 16
)

// Decoder 把 io.Reader 上的字节流按 AWS Event Stream 帧切分。
// 字段 Limits 留零值使用默认。线程不安全 — 单 stream 单 Decoder。
type Decoder struct {
	Limits Limits
}

// 错误哨兵 — 调用方可用 errors.Is 区分。
var (
	ErrFrameTooLarge          = errors.New("eventstream: frame 超过 MaxMessageBytes")
	ErrHeadersTooLarge        = errors.New("eventstream: headers 段超过 MaxHeaderBytes")
	ErrPayloadTooLarge        = errors.New("eventstream: payload 超过 MaxPayloadBytes")
	ErrPreludeCRCMismatch     = errors.New("eventstream: prelude CRC32 校验失败")
	ErrMessageCRCMismatch     = errors.New("eventstream: message CRC32 校验失败")
	ErrHeaderTypeUnsupported  = errors.New("eventstream: 未支持的 header value type")
	ErrTruncatedFrame         = errors.New("eventstream: 帧被截断（io.ReadFull 短读）")
	ErrInvalidLength          = errors.New("eventstream: 长度字段非法（headers + 16 > total）")
	ErrEmptyHeaderName        = errors.New("eventstream: header name 长度为 0")
)

// ReadMessage 从 r 读一个完整 Event Stream message。EOF 在 prelude 尚未
// 读完时返回 io.EOF（caller 据此结束循环）；中段截断返回 ErrTruncatedFrame。
//
// 步骤：
//  1. 读 12-byte prelude
//  2. 解 prelude 三字段，校验 prelude CRC32
//  3. 校验 length 字段（防溢出 + 不超 limits）
//  4. 读 headers + payload + message CRC（一次性）
//  5. 校验 message CRC32
//  6. 解析 headers 为 map
func (d *Decoder) ReadMessage(ctx context.Context, r io.Reader) (Message, error) {
	limits := d.effectiveLimits()

	// --- 1. 读 prelude ---
	preBuf := make([]byte, preludeSize)
	if _, err := io.ReadFull(r, preBuf); err != nil {
		// EOF 在帧边界 = 流正常结束
		if errors.Is(err, io.EOF) {
			return Message{}, io.EOF
		}
		// 中段断 = io.ErrUnexpectedEOF → 翻译为 truncated
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return Message{}, fmt.Errorf("%w: prelude 读取中断: %v", ErrTruncatedFrame, err)
		}
		return Message{}, fmt.Errorf("eventstream: 读 prelude 失败: %w", err)
	}

	// ctx 取消检查（在 IO 之间）
	select {
	case <-ctx.Done():
		return Message{}, ctx.Err()
	default:
	}

	// --- 2. 解析 prelude 三字段 ---
	totalLen := binary.BigEndian.Uint32(preBuf[0:4])
	headersLen := binary.BigEndian.Uint32(preBuf[4:8])
	gotPreludeCRC := binary.BigEndian.Uint32(preBuf[8:12])

	wantPreludeCRC := crc32.ChecksumIEEE(preBuf[0:8])
	if gotPreludeCRC != wantPreludeCRC {
		return Message{}, fmt.Errorf("%w: got=0x%08x want=0x%08x", ErrPreludeCRCMismatch, gotPreludeCRC, wantPreludeCRC)
	}

	// --- 3. 长度字段合法性 ---
	// 注意：所有比较先升 uint64 再 vs limit；防 32-bit 平台上 int(uint32) 回绕成负数
	// 旁路 limit 检查 + 后续 make([]byte, negative) panic（HIGH 安全 finding）。
	if uint64(totalLen) > uint64(limits.MaxMessageBytes) {
		return Message{}, fmt.Errorf("%w: total=%d max=%d", ErrFrameTooLarge, totalLen, limits.MaxMessageBytes)
	}
	if totalLen < frameOverhead {
		return Message{}, fmt.Errorf("%w: total_length=%d < %d (overhead)", ErrInvalidLength, totalLen, frameOverhead)
	}
	if uint64(headersLen)+uint64(frameOverhead) > uint64(totalLen) {
		return Message{}, fmt.Errorf("%w: headers_length=%d + 16 > total_length=%d", ErrInvalidLength, headersLen, totalLen)
	}
	if uint64(headersLen) > uint64(limits.MaxHeaderBytes) {
		return Message{}, fmt.Errorf("%w: headers=%d max=%d", ErrHeadersTooLarge, headersLen, limits.MaxHeaderBytes)
	}
	payloadLen := totalLen - headersLen - frameOverhead
	if uint64(payloadLen) > uint64(limits.MaxPayloadBytes) {
		return Message{}, fmt.Errorf("%w: payload=%d max=%d", ErrPayloadTooLarge, payloadLen, limits.MaxPayloadBytes)
	}

	// --- 4. 读 headers + payload + message CRC ---
	rest := make([]byte, int(totalLen)-preludeSize)
	if _, err := io.ReadFull(r, rest); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Message{}, fmt.Errorf("%w: headers/payload 读取中断: %v", ErrTruncatedFrame, err)
		}
		return Message{}, fmt.Errorf("eventstream: 读 message body 失败: %w", err)
	}

	// 切分 rest = headers || payload || messageCRC
	headersBytes := rest[:headersLen]
	payloadBytes := rest[headersLen : headersLen+payloadLen]
	gotMessageCRC := binary.BigEndian.Uint32(rest[len(rest)-messageCRCSize:])

	// --- 5. 校验 message CRC32（覆盖 prelude + headers + payload） ---
	tab := crc32.IEEETable
	hasher := crc32.New(tab)
	hasher.Write(preBuf)
	hasher.Write(headersBytes)
	hasher.Write(payloadBytes)
	wantMessageCRC := hasher.Sum32()
	if gotMessageCRC != wantMessageCRC {
		return Message{}, fmt.Errorf("%w: got=0x%08x want=0x%08x", ErrMessageCRCMismatch, gotMessageCRC, wantMessageCRC)
	}

	// --- 6. 解析 headers ---
	headers, err := parseHeaders(headersBytes)
	if err != nil {
		return Message{}, err
	}

	return Message{
		Headers:       headers,
		Payload:       bytes.Clone(payloadBytes), // 拷贝防 caller 持有 rest 整片
		TotalLength:   totalLen,
		HeadersLength: headersLen,
	}, nil
}

// parseHeaders 把 headers 段字节流解为 name → HeaderValue map。
// 未知 value type 直接报错（不静默 skip — fail-loud 防协议变化漏字段）。
func parseHeaders(b []byte) (map[string]HeaderValue, error) {
	out := make(map[string]HeaderValue)
	pos := 0
	for pos < len(b) {
		if pos+1 > len(b) {
			return nil, fmt.Errorf("%w: header name length 字段缺失", ErrTruncatedFrame)
		}
		nameLen := int(b[pos])
		pos++
		if nameLen == 0 {
			return nil, ErrEmptyHeaderName
		}
		if pos+nameLen+1 > len(b) {
			return nil, fmt.Errorf("%w: header name+type 超界", ErrTruncatedFrame)
		}
		name := string(b[pos : pos+nameLen])
		pos += nameLen
		valueType := HeaderValueType(b[pos])
		pos++

		hv, consumed, err := parseHeaderValue(valueType, b[pos:])
		if err != nil {
			return nil, fmt.Errorf("header %q: %w", name, err)
		}
		pos += consumed
		out[name] = hv
	}
	return out, nil
}

// parseHeaderValue 按 type 解析 value，返回 HeaderValue 与消耗的字节数。
func parseHeaderValue(t HeaderValueType, b []byte) (HeaderValue, int, error) {
	switch t {
	case HeaderTypeBoolTrue:
		return HeaderValue{Type: t, Bool: true}, 0, nil
	case HeaderTypeBoolFalse:
		return HeaderValue{Type: t, Bool: false}, 0, nil
	case HeaderTypeByte:
		if len(b) < 1 {
			return HeaderValue{}, 0, fmt.Errorf("%w: byte value 缺", ErrTruncatedFrame)
		}
		return HeaderValue{Type: t, Int: int64(int8(b[0]))}, 1, nil
	case HeaderTypeShort:
		if len(b) < 2 {
			return HeaderValue{}, 0, fmt.Errorf("%w: short value 缺", ErrTruncatedFrame)
		}
		return HeaderValue{Type: t, Int: int64(int16(binary.BigEndian.Uint16(b[:2])))}, 2, nil
	case HeaderTypeInteger:
		if len(b) < 4 {
			return HeaderValue{}, 0, fmt.Errorf("%w: integer value 缺", ErrTruncatedFrame)
		}
		return HeaderValue{Type: t, Int: int64(int32(binary.BigEndian.Uint32(b[:4])))}, 4, nil
	case HeaderTypeLong:
		if len(b) < 8 {
			return HeaderValue{}, 0, fmt.Errorf("%w: long value 缺", ErrTruncatedFrame)
		}
		return HeaderValue{Type: t, Int: int64(binary.BigEndian.Uint64(b[:8]))}, 8, nil
	case HeaderTypeByteArray, HeaderTypeString:
		if len(b) < 2 {
			return HeaderValue{}, 0, fmt.Errorf("%w: bytes/string 长度字段缺", ErrTruncatedFrame)
		}
		l := int(binary.BigEndian.Uint16(b[:2]))
		if 2+l > len(b) {
			return HeaderValue{}, 0, fmt.Errorf("%w: bytes/string 内容超界", ErrTruncatedFrame)
		}
		raw := bytes.Clone(b[2 : 2+l])
		hv := HeaderValue{Type: t, Bytes: raw}
		if t == HeaderTypeString {
			hv.String = string(raw)
		}
		return hv, 2 + l, nil
	case HeaderTypeTimestamp:
		if len(b) < 8 {
			return HeaderValue{}, 0, fmt.Errorf("%w: timestamp value 缺", ErrTruncatedFrame)
		}
		return HeaderValue{Type: t, Int: int64(binary.BigEndian.Uint64(b[:8]))}, 8, nil
	case HeaderTypeUUID:
		if len(b) < 16 {
			return HeaderValue{}, 0, fmt.Errorf("%w: uuid value 缺", ErrTruncatedFrame)
		}
		return HeaderValue{Type: t, Bytes: bytes.Clone(b[:16])}, 16, nil
	default:
		return HeaderValue{}, 0, fmt.Errorf("%w: type=%d", ErrHeaderTypeUnsupported, t)
	}
}

// effectiveLimits 返回应用了默认值的 Limits 副本。
func (d *Decoder) effectiveLimits() Limits {
	l := d.Limits
	if l.MaxMessageBytes <= 0 {
		l.MaxMessageBytes = defaultMaxMessageBytes
	}
	if l.MaxHeaderBytes <= 0 {
		l.MaxHeaderBytes = defaultMaxHeaderBytes
	}
	if l.MaxPayloadBytes <= 0 {
		l.MaxPayloadBytes = defaultMaxPayloadBytes
	}
	return l
}

// 参阅的资料来源:
//   - https://docs.aws.amazon.com/transcribe/latest/dg/event-stream.html (event stream wire format 公开规范)
//   - https://docs.aws.amazon.com/AmazonS3/latest/API/RESTSelectObjectAppendix.html#api-eventstream-format (header value 类型)
// 通道: claude
// 时间: 2026-05-07T<UTC>
