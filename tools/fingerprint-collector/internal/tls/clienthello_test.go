// 包 tls — ClientHello 解析器的表驱动单元测试。
// 测试用的 ClientHello 字节序列均为程序化构造，不来自任何真实捕获文件。
package tls

import (
	"encoding/hex"
	"testing"
)

// buildClientHello 程序化构建一个最小 TLS 1.3 ClientHello 记录（含 TLS 记录头）。
// 结构遵循 RFC 8446 §4.1.2。
func buildMinimalClientHello(ciphers []uint16, extensions []byte) []byte {
	var body []byte

	// legacy_version: 0x0303（TLS 1.2）
	body = append(body, 0x03, 0x03)

	// random: 32 字节全零（测试用）
	body = append(body, make([]byte, 32)...)

	// legacy_session_id: 0 字节
	body = append(body, 0x00)

	// cipher_suites
	csBytes := make([]byte, len(ciphers)*2)
	for i, c := range ciphers {
		csBytes[i*2] = byte(c >> 8)
		csBytes[i*2+1] = byte(c)
	}
	csLen := len(csBytes)
	body = append(body, byte(csLen>>8), byte(csLen))
	body = append(body, csBytes...)

	// compression_methods: 1 字节（null）
	body = append(body, 0x01, 0x00)

	// extensions（含长度前缀）
	if len(extensions) > 0 {
		body = append(body, byte(len(extensions)>>8), byte(len(extensions)))
		body = append(body, extensions...)
	}

	// 握手消息头：类型(1) + 长度(3)
	msgLen := len(body)
	handshake := []byte{
		HandshakeTypeClientHello,
		byte(msgLen >> 16),
		byte(msgLen >> 8),
		byte(msgLen),
	}
	handshake = append(handshake, body...)

	// TLS 记录头：类型(1) + 版本(2) + 长度(2)
	recLen := len(handshake)
	record := []byte{
		TLSRecordTypeHandshake,
		0x03, 0x01, // legacy record version
		byte(recLen >> 8),
		byte(recLen),
	}
	return append(record, handshake...)
}

// buildExtension 构建单个 TLS 扩展字节序列（类型 + 数据长度 + 数据）。
func buildExtension(extType uint16, data []byte) []byte {
	ext := []byte{
		byte(extType >> 8), byte(extType),
		byte(len(data) >> 8), byte(len(data)),
	}
	return append(ext, data...)
}

// buildSNIExtension 构建 server_name 扩展（type=0x0000）。
func buildSNIExtension(hostname string) []byte {
	nameBytes := []byte(hostname)
	nameLen := len(nameBytes)
	// 格式：2字节列表长度 + 1字节name_type(0x00) + 2字节name长度 + name
	listLen := 1 + 2 + nameLen
	data := []byte{
		byte(listLen >> 8), byte(listLen),
		0x00, // host_name type
		byte(nameLen >> 8), byte(nameLen),
	}
	data = append(data, nameBytes...)
	return buildExtension(ExtServerName, data)
}

// buildSupportedVersionsExtension 构建 supported_versions 扩展（ClientHello 格式）。
func buildSupportedVersionsExtension(versions []uint16) []byte {
	dataLen := len(versions) * 2
	data := []byte{byte(dataLen)}
	for _, v := range versions {
		data = append(data, byte(v>>8), byte(v))
	}
	return buildExtension(ExtSupportedVersions, data)
}

// buildSupportedGroupsExtension 构建 supported_groups 扩展。
func buildSupportedGroupsExtension(groups []uint16) []byte {
	listLen := len(groups) * 2
	data := []byte{byte(listLen >> 8), byte(listLen)}
	for _, g := range groups {
		data = append(data, byte(g>>8), byte(g))
	}
	return buildExtension(ExtSupportedGroups, data)
}

// buildALPNExtension 构建 ALPN 扩展。
func buildALPNExtension(protocols []string) []byte {
	var protoBytes []byte
	for _, p := range protocols {
		protoBytes = append(protoBytes, byte(len(p)))
		protoBytes = append(protoBytes, []byte(p)...)
	}
	data := []byte{byte(len(protoBytes) >> 8), byte(len(protoBytes))}
	data = append(data, protoBytes...)
	return buildExtension(ExtALPN, data)
}

// TestParseClientHello_Minimal 测试最小 ClientHello（无扩展）的解析。
func TestParseClientHello_Minimal(t *testing.T) {
	ciphers := []uint16{0x1301, 0x1302, 0x1303} // TLS 1.3 密码套件
	record := buildMinimalClientHello(ciphers, nil)

	ch, err := ParseClientHelloFromRecord(record)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	if ch.LegacyVersion != 0x0303 {
		t.Errorf("期望 legacy_version=0x0303，得到 0x%04x", ch.LegacyVersion)
	}
	if ch.RandomLen != 32 {
		t.Errorf("期望 random_len=32，得到 %d", ch.RandomLen)
	}
	if len(ch.CipherSuites) != 3 {
		t.Errorf("期望 3 个密码套件，得到 %d", len(ch.CipherSuites))
	}
	for i, c := range ciphers {
		if ch.CipherSuites[i] != c {
			t.Errorf("密码套件[%d] 期望 0x%04x，得到 0x%04x", i, c, ch.CipherSuites[i])
		}
	}
	if len(ch.Extensions) != 0 {
		t.Errorf("期望 0 个扩展，得到 %d", len(ch.Extensions))
	}
}

// TestParseClientHello_WithExtensions 测试含 SNI、ALPN、supported_versions 扩展的 ClientHello。
func TestParseClientHello_WithExtensions(t *testing.T) {
	ciphers := []uint16{0xdada, 0x1301, 0x1302, 0x00ff} // 包含 GREASE 和 SCSV
	var extBytes []byte
	extBytes = append(extBytes, buildSNIExtension("api.anthropic.com")...)
	extBytes = append(extBytes, buildSupportedVersionsExtension([]uint16{0x0a0a, 0x0304, 0x0303})...)
	extBytes = append(extBytes, buildSupportedGroupsExtension([]uint16{0xdada, 0x001d, 0x0017})...)
	extBytes = append(extBytes, buildALPNExtension([]string{"h2", "http/1.1"})...)

	record := buildMinimalClientHello(ciphers, extBytes)
	ch, err := ParseClientHelloFromRecord(record)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	// 验证密码套件顺序
	if ch.CipherSuites[0] != 0xdada {
		t.Errorf("期望第一个密码套件为 GREASE 0xdada，得到 0x%04x", ch.CipherSuites[0])
	}

	// 验证 SNI
	sni := ch.SNI()
	if sni != "api.anthropic.com" {
		t.Errorf("期望 SNI=%q，得到 %q", "api.anthropic.com", sni)
	}

	// 验证 supported_versions（含 GREASE）
	vers := ch.SupportedVersions()
	if len(vers) != 3 {
		t.Errorf("期望 3 个 supported_versions，得到 %d", len(vers))
	}
	if vers[0] != 0x0a0a {
		t.Errorf("期望第一个 supported_version 为 GREASE 0x0a0a，得到 0x%04x", vers[0])
	}

	// 验证 supported_groups（含 GREASE）
	groups := ch.SupportedGroups()
	if len(groups) != 3 || groups[0] != 0xdada {
		t.Errorf("supported_groups 解析错误: %v", groups)
	}

	// 验证 ALPN
	alpn := ch.ALPNProtocols()
	if len(alpn) != 2 || alpn[0] != "h2" {
		t.Errorf("ALPN 解析错误: %v", alpn)
	}

	// 验证扩展数量
	if len(ch.Extensions) != 4 {
		t.Errorf("期望 4 个扩展，得到 %d", len(ch.Extensions))
	}
}

// TestParseClientHello_NotClientHello 测试非 ClientHello 输入。
func TestParseClientHello_NotClientHello(t *testing.T) {
	// 非握手记录类型
	data := []byte{0x17, 0x03, 0x03, 0x00, 0x01, 0x00}
	_, err := ParseClientHelloFromRecord(data)
	if err != ErrNotClientHello {
		t.Errorf("期望 ErrNotClientHello，得到 %v", err)
	}
}

// TestParseClientHello_Truncated 测试截断输入的处理。
func TestParseClientHello_Truncated(t *testing.T) {
	// 只有 3 字节（小于 TLS 记录头的 5 字节）
	_, err := ParseClientHelloFromRecord([]byte{0x16, 0x03, 0x01})
	if err == nil {
		t.Error("期望解析截断数据时返回错误")
	}
}

// TestParseClientHello_GREASEDetection 验证 GREASE 值在解析中被正确保留。
func TestParseClientHello_GREASEDetection(t *testing.T) {
	greaseValues := []uint16{0x0a0a, 0x1a1a, 0x2a2a, 0x3a3a}
	ciphers := append(greaseValues, 0x1301, 0x1302)
	record := buildMinimalClientHello(ciphers, nil)

	ch, err := ParseClientHelloFromRecord(record)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	// GREASE 值应被保留在密码套件列表中
	for i, gv := range greaseValues {
		if ch.CipherSuites[i] != gv {
			t.Errorf("位置[%d] GREASE 值应为 0x%04x，得到 0x%04x", i, gv, ch.CipherSuites[i])
		}
		if !IsGREASE(ch.CipherSuites[i]) {
			t.Errorf("位置[%d] 值 0x%04x 应被识别为 GREASE", i, ch.CipherSuites[i])
		}
	}

	// 非 GREASE 值应不被识别为 GREASE
	if IsGREASE(0x1301) {
		t.Error("0x1301 不应被识别为 GREASE")
	}
}

// TestParseClientHello_RealWorldHex 使用一段真实世界 Chrome-like ClientHello 的十六进制数据进行测试。
// 此数据段经过程序化构造（非真实捕获），仅验证边界条件。
func TestParseClientHello_RealWorldHex(t *testing.T) {
	// 一个程序化构造的 Chrome-like ClientHello（部分字段）
	// TLS 记录头 + 握手头 + ClientHello body
	// 使用 buildMinimalClientHello 辅助函数构造，然后转换为 hex 验证往返
	ciphers := []uint16{0xdada, 0x1301, 0x1302, 0x1303, 0xc02b, 0xc02f}
	var extBytes []byte
	extBytes = append(extBytes, buildSNIExtension("api.anthropic.com")...)
	extBytes = append(extBytes, buildSupportedVersionsExtension([]uint16{0xdada, 0x0304})...)

	record := buildMinimalClientHello(ciphers, extBytes)
	hexStr := hex.EncodeToString(record)

	// 从 hex 往返解码
	decoded, err := hex.DecodeString(hexStr)
	if err != nil {
		t.Fatalf("hex 解码失败: %v", err)
	}

	ch, err := ParseClientHelloFromRecord(decoded)
	if err != nil {
		t.Fatalf("往返解析失败: %v", err)
	}

	if len(ch.CipherSuites) != 6 {
		t.Errorf("期望 6 个密码套件，得到 %d", len(ch.CipherSuites))
	}
	if ch.SNI() != "api.anthropic.com" {
		t.Errorf("SNI 往返失败，得到 %q", ch.SNI())
	}
}
