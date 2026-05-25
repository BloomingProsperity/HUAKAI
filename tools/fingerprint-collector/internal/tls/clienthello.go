// 包 tls — TLS ClientHello 报文的二进制解析器。
// 只解析未加密的握手数据，不涉及任何密钥材料或载荷解密。
package tls

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// TLS 记录层和握手层常量
const (
	// TLSRecordTypeHandshake 是握手记录类型标识
	TLSRecordTypeHandshake byte = 0x16
	// HandshakeTypeClientHello 是 ClientHello 握手子类型
	HandshakeTypeClientHello byte = 0x01
	// TLSVersionTLS10 即 0x0301，ClientHello legacy_version 常见值
	TLSVersionTLS10 uint16 = 0x0301
)

// ClientHello 保存从原始字节解析出的完整 ClientHello 信息。
// 字段命名遵循 RFC 8446 §4.1.2 术语。
type ClientHello struct {
	// LegacyVersion 是 ClientHello 中的协议版本字段（TLS 1.3 固定为 0x0303）
	LegacyVersion uint16 `json:"legacy_version"`
	// RandomLen 是 random 字段的字节数（始终为 32，仅记录长度）
	RandomLen int `json:"random_len"`
	// LegacySessionIDLen 是 legacy_session_id 的字节数
	LegacySessionIDLen int `json:"legacy_session_id_len"`
	// CipherSuites 是密码套件有序列表（含 GREASE 值）
	CipherSuites []uint16 `json:"cipher_suites"`
	// CompressionMethods 是压缩方法列表（TLS 1.3 中应只有 null）
	CompressionMethods []uint8 `json:"compression_methods"`
	// Extensions 是扩展的有序解析结果
	Extensions []ParsedExtension `json:"extensions"`
	// RawLen 是完整 ClientHello 握手消息的字节数（不含 TLS 记录头）
	RawLen int `json:"raw_len"`
}

// ExtensionTypes 返回所有扩展类型的有序 uint16 切片（含 GREASE），
// 便于 JA3/JA4 计算调用。
func (ch *ClientHello) ExtensionTypes() []uint16 {
	types := make([]uint16, len(ch.Extensions))
	for i, ext := range ch.Extensions {
		types[i] = ext.Type
	}
	return types
}

// SupportedGroups 从已解析扩展中返回 supported_groups 列表（含 GREASE）。
func (ch *ClientHello) SupportedGroups() []uint16 {
	for _, ext := range ch.Extensions {
		if ext.Type == ExtSupportedGroups {
			return ext.SupportedGroups
		}
	}
	return nil
}

// ECPointFormats 从已解析扩展中返回 ec_point_formats 列表。
func (ch *ClientHello) ECPointFormats() []uint8 {
	for _, ext := range ch.Extensions {
		if ext.Type == ExtECPointFormats {
			return ext.ECPointFormats
		}
	}
	return nil
}

// SNI 从已解析扩展中返回 SNI 主机名，未设置时返回空串。
func (ch *ClientHello) SNI() string {
	for _, ext := range ch.Extensions {
		if ext.Type == ExtServerName {
			return ext.SNIHostname
		}
	}
	return ""
}

// ALPNProtocols 从已解析扩展中返回 ALPN 协议列表。
func (ch *ClientHello) ALPNProtocols() []string {
	for _, ext := range ch.Extensions {
		if ext.Type == ExtALPN {
			return ext.ALPNProtocols
		}
	}
	return nil
}

// SupportedVersions 从已解析扩展中返回 supported_versions 列表。
func (ch *ClientHello) SupportedVersions() []uint16 {
	for _, ext := range ch.Extensions {
		if ext.Type == ExtSupportedVersions {
			return ext.SupportedVersions
		}
	}
	return nil
}

// ParseError 封装 ClientHello 解析过程中出现的结构性错误。
type ParseError struct {
	Field string
	Msg   string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("clienthello parse error at %s: %s", e.Field, e.Msg)
}

// parseErr 创建 ParseError 的简便函数。
func parseErr(field, msg string) error {
	return &ParseError{Field: field, Msg: msg}
}

// ParseClientHelloFromRecord 从完整的 TLS 记录层帧解析 ClientHello。
// 输入可以是一帧或多帧连续字节；只处理第一个合法的 ClientHello 记录。
// 若输入不是握手记录或子类型不是 ClientHello，返回 ErrNotClientHello。
func ParseClientHelloFromRecord(data []byte) (*ClientHello, error) {
	// TLS 记录头：1字节类型 + 2字节版本 + 2字节长度 = 5字节
	if len(data) < 5 {
		return nil, parseErr("record_header", "too short")
	}
	if data[0] != TLSRecordTypeHandshake {
		return nil, ErrNotClientHello
	}
	// 取记录体长度，不验证版本字段（某些客户端使用不同旧版本号）
	recLen := int(binary.BigEndian.Uint16(data[3:5]))
	if len(data) < 5+recLen {
		return nil, parseErr("record_body", "truncated")
	}
	body := data[5 : 5+recLen]
	return ParseClientHelloFromHandshake(body)
}

// ErrNotClientHello 表示输入数据不是 ClientHello 握手消息。
var ErrNotClientHello = errors.New("not a ClientHello handshake message")

// ParseClientHelloFromHandshake 从握手层载荷（不含 TLS 记录头）解析 ClientHello。
// 握手消息格式：1字节类型 + 3字节长度 + 消息体。
func ParseClientHelloFromHandshake(data []byte) (*ClientHello, error) {
	// 握手消息头：1字节类型 + 3字节长度 = 4字节
	if len(data) < 4 {
		return nil, parseErr("handshake_header", "too short")
	}
	if data[0] != HandshakeTypeClientHello {
		return nil, ErrNotClientHello
	}
	// 3字节大端长度
	msgLen := int(data[1])<<16 | int(data[2])<<8 | int(data[3])
	if len(data) < 4+msgLen {
		return nil, parseErr("handshake_body", "truncated")
	}
	body := data[4 : 4+msgLen]
	return parseClientHelloBody(body, 4+msgLen)
}

// parseClientHelloBody 解析 ClientHello 消息体（不含握手头）。
func parseClientHelloBody(body []byte, rawLen int) (*ClientHello, error) {
	ch := &ClientHello{RawLen: rawLen}
	pos := 0

	// 2字节 legacy_version
	if len(body)-pos < 2 {
		return nil, parseErr("legacy_version", "too short")
	}
	ch.LegacyVersion = binary.BigEndian.Uint16(body[pos : pos+2])
	pos += 2

	// 32字节 random（只记录长度，不存储内容）
	if len(body)-pos < 32 {
		return nil, parseErr("random", "too short")
	}
	ch.RandomLen = 32
	pos += 32

	// 1字节 legacy_session_id 长度 + 内容
	if len(body)-pos < 1 {
		return nil, parseErr("session_id_len", "too short")
	}
	sidLen := int(body[pos])
	pos++
	if len(body)-pos < sidLen {
		return nil, parseErr("session_id", "truncated")
	}
	ch.LegacySessionIDLen = sidLen
	pos += sidLen

	// 2字节 cipher_suites 列表字节数 + 内容
	if len(body)-pos < 2 {
		return nil, parseErr("cipher_suites_len", "too short")
	}
	csLen := int(binary.BigEndian.Uint16(body[pos : pos+2]))
	pos += 2
	if len(body)-pos < csLen || csLen%2 != 0 {
		return nil, parseErr("cipher_suites", "truncated or odd length")
	}
	csCount := csLen / 2
	ch.CipherSuites = make([]uint16, csCount)
	for i := 0; i < csCount; i++ {
		ch.CipherSuites[i] = binary.BigEndian.Uint16(body[pos+i*2 : pos+i*2+2])
	}
	pos += csLen

	// 1字节 compression_methods 长度 + 内容
	if len(body)-pos < 1 {
		return nil, parseErr("compression_methods_len", "too short")
	}
	cmLen := int(body[pos])
	pos++
	if len(body)-pos < cmLen {
		return nil, parseErr("compression_methods", "truncated")
	}
	ch.CompressionMethods = make([]uint8, cmLen)
	copy(ch.CompressionMethods, body[pos:pos+cmLen])
	pos += cmLen

	// 扩展是可选的（某些 TLS 1.2 ClientHello 没有扩展块）
	if pos >= len(body) {
		return ch, nil
	}

	// 2字节扩展总字节数
	if len(body)-pos < 2 {
		return nil, parseErr("extensions_len", "too short")
	}
	extTotalLen := int(binary.BigEndian.Uint16(body[pos : pos+2]))
	pos += 2
	if len(body)-pos < extTotalLen {
		return nil, parseErr("extensions", "truncated")
	}

	extBody := body[pos : pos+extTotalLen]
	exts, err := parseExtensionList(extBody)
	if err != nil {
		return nil, err
	}
	ch.Extensions = exts
	return ch, nil
}

// parseExtensionList 解析扩展列表块（已去掉列表长度前缀）。
func parseExtensionList(data []byte) ([]ParsedExtension, error) {
	var exts []ParsedExtension
	pos := 0
	for pos < len(data) {
		// 每个扩展：2字节类型 + 2字节数据长度 + 数据
		if len(data)-pos < 4 {
			return nil, parseErr("extension_header", "too short")
		}
		extType := binary.BigEndian.Uint16(data[pos : pos+2])
		extDataLen := int(binary.BigEndian.Uint16(data[pos+2 : pos+4]))
		pos += 4
		if len(data)-pos < extDataLen {
			return nil, parseErr(fmt.Sprintf("extension[%04x]", extType), "truncated data")
		}
		extData := data[pos : pos+extDataLen]
		exts = append(exts, ParseExtension(extType, extData))
		pos += extDataLen
	}
	return exts, nil
}
