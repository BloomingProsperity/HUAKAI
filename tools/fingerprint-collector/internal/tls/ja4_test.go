// 包 tls — JA4 算法的单元测试。
// 向量来源：https://github.com/FoxIO-LLC/ja4 公开规范文档。
package tls

import (
	"strings"
	"testing"
)

// TestJA4_Format 验证 JA4 输出格式符合规范。
// 格式：<prefix>_<cipherHash>_<extHash>
// prefix 格式：<protocol><version><sni><cipherCount><extCount>_<alpn>
func TestJA4_Format(t *testing.T) {
	ch := &ClientHello{
		LegacyVersion: 0x0303,
		CipherSuites:  []uint16{0x1301, 0x1302, 0x1303},
		Extensions: []ParsedExtension{
			{Type: 0x0000, SNIHostname: "api.anthropic.com"},
			{Type: 0x0010, ALPNProtocols: []string{"h2"}},
			{Type: 0x002b, SupportedVersions: []uint16{0x0304}},
			{Type: 0x000a, SupportedGroups: []uint16{0x001d}},
		},
	}

	result := ComputeJA4(ch)
	hash := result.Hash

	// JA4 格式：<prefix>_<alpn>_<cipher_hash>_<ext_hash>，共 3 个下划线（4 段）
	// 例：t13d0304_h2_abc123456789_def123456789
	parts := strings.Split(hash, "_")
	if len(parts) != 4 {
		t.Fatalf("JA4 哈希应有 4 段（3个 '_' 分隔），得到 %d 段: %q", len(parts), hash)
	}

	prefix := parts[0]
	// parts[1] 是 ALPN 首条（如 "h2" 或 "00"）
	cipherHash := parts[2]
	extHash := parts[3]

	// 前缀至少以 "t" 开头（TCP/TLS 协议标记）
	if len(prefix) == 0 || prefix[0] != 't' {
		t.Errorf("JA4 前缀应以 't' 开头，得到 %q", prefix)
	}

	// cipher_hash 应为 12 位十六进制字符
	if len(cipherHash) != 12 {
		t.Errorf("JA4 cipher_hash 应为 12 位，得到 %d 位: %q", len(cipherHash), cipherHash)
	}
	if !isHex(cipherHash) {
		t.Errorf("JA4 cipher_hash 应为十六进制字符串，得到 %q", cipherHash)
	}

	// ext_hash 应为 12 位十六进制字符
	if len(extHash) != 12 {
		t.Errorf("JA4 ext_hash 应为 12 位，得到 %d 位: %q", len(extHash), extHash)
	}
	if !isHex(extHash) {
		t.Errorf("JA4 ext_hash 应为十六进制字符串，得到 %q", extHash)
	}
}

func TestJA4_ALPNTokenUsesLastAdvertisedProtocol(t *testing.T) {
	tests := []struct {
		name string
		alpn []string
		want string
	}{
		{name: "empty", alpn: nil, want: "00"},
		{name: "single h2", alpn: []string{"h2"}, want: "h2"},
		{name: "h2 then http1", alpn: []string{"h2", "http/1.1"}, want: "ht"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ja4ALPNTokenFromHash(ComputeJA4(ja4ClientHelloWithALPN(tt.alpn)).Hash)
			if got != tt.want {
				t.Fatalf("JA4 ALPN token for %v = %q, want %q", tt.alpn, got, tt.want)
			}
		})
	}

	singleH2 := ComputeJA4(ja4ClientHelloWithALPN([]string{"h2"})).Hash
	dualALPN := ComputeJA4(ja4ClientHelloWithALPN([]string{"h2", "http/1.1"})).Hash
	if singleH2 == dualALPN {
		t.Fatalf("fixture is not discriminating: single and dual ALPN both produced %q", dualALPN)
	}
}

// TestJA4_SNIFlag 验证有无 SNI 时标志位正确。
func TestJA4_SNIFlag(t *testing.T) {
	// 有 SNI
	chWithSNI := &ClientHello{
		LegacyVersion: 0x0303,
		CipherSuites:  []uint16{0x1301},
		Extensions: []ParsedExtension{
			{Type: 0x0000, SNIHostname: "api.anthropic.com"},
			{Type: 0x002b, SupportedVersions: []uint16{0x0304}},
		},
	}
	// 无 SNI
	chNoSNI := &ClientHello{
		LegacyVersion: 0x0303,
		CipherSuites:  []uint16{0x1301},
		Extensions: []ParsedExtension{
			{Type: 0x002b, SupportedVersions: []uint16{0x0304}},
		},
	}

	rWith := ComputeJA4(chWithSNI)
	rNo := ComputeJA4(chNoSNI)

	// 有 SNI 时前缀段（第一个 '_' 前）应含 "d"
	withPrefix := strings.Split(rWith.Hash, "_")[0]
	noPrefix := strings.Split(rNo.Hash, "_")[0]

	if !strings.Contains(withPrefix, "d") {
		t.Errorf("有 SNI 时 JA4 前缀应含 'd'，得到 %q", withPrefix)
	}
	if !strings.Contains(noPrefix, "i") {
		t.Errorf("无 SNI 时 JA4 前缀应含 'i'，得到 %q", noPrefix)
	}
}

// TestJA4_GREASEFiltered 验证 GREASE 值在 JA4 计算前被过滤。
func TestJA4_GREASEFiltered(t *testing.T) {
	// 带 GREASE 的密码套件
	chGREASE := &ClientHello{
		LegacyVersion: 0x0303,
		CipherSuites:  []uint16{0xdada, 0x1301, 0x1302}, // 0xdada GREASE
		Extensions: []ParsedExtension{
			{Type: 0xdada, IsGREASEValue: true}, // GREASE 扩展
			{Type: 0x002b, SupportedVersions: []uint16{0x0304}},
		},
	}
	// 等价无 GREASE 版本
	chClean := &ClientHello{
		LegacyVersion: 0x0303,
		CipherSuites:  []uint16{0x1301, 0x1302},
		Extensions: []ParsedExtension{
			{Type: 0x002b, SupportedVersions: []uint16{0x0304}},
		},
	}

	rGREASE := ComputeJA4(chGREASE)
	rClean := ComputeJA4(chClean)

	// GREASE 过滤后哈希应相同
	if rGREASE.Hash != rClean.Hash {
		t.Errorf("GREASE 过滤后 JA4 哈希应相等:\n  带GREASE: %s\n  干净版: %s",
			rGREASE.Hash, rClean.Hash)
	}
}

// TestJA4_VersionMapping 验证 TLS 版本号的两字符映射。
func TestJA4_VersionMapping(t *testing.T) {
	cases := []struct {
		version  uint16
		expected string
	}{
		{0x0304, "13"}, // TLS 1.3
		{0x0303, "12"}, // TLS 1.2
		{0x0302, "11"}, // TLS 1.1
		{0x0301, "10"}, // TLS 1.0
		{0x0300, "s3"}, // SSL 3.0
	}
	for _, tc := range cases {
		got := ja4VersionString(tc.version)
		if got != tc.expected {
			t.Errorf("版本 0x%04x: 期望 %q，得到 %q", tc.version, tc.expected, got)
		}
	}
}

// TestJA4_CipherCount 验证密码套件计数正确（过滤 GREASE 和 SCSV 后）。
func TestJA4_CipherCount(t *testing.T) {
	ch := &ClientHello{
		LegacyVersion: 0x0303,
		// 5个套件：1个GREASE + 1个SCSV + 3个正常
		CipherSuites: []uint16{0xdada, 0x00FF, 0x1301, 0x1302, 0x1303},
		Extensions:   nil,
	}
	result := ComputeJA4(ch)

	// 过滤后应为 3 个，前缀应含 "03"
	if !strings.Contains(result.Hash, "03") {
		t.Errorf("密码套件计数应为 03（过滤后），JA4=%q", result.Hash)
	}
}

// isHex 检查字符串是否全为十六进制字符。
func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func ja4ClientHelloWithALPN(alpn []string) *ClientHello {
	extensions := []ParsedExtension{
		{Type: ExtServerName, SNIHostname: "api.anthropic.com"},
		{Type: ExtSupportedVersions, SupportedVersions: []uint16{0x0304}},
	}
	if alpn != nil {
		extensions = append(extensions, ParsedExtension{Type: ExtALPN, ALPNProtocols: alpn})
	}
	return &ClientHello{
		LegacyVersion: 0x0303,
		CipherSuites:  []uint16{0x1301},
		Extensions:    extensions,
	}
}

func ja4ALPNTokenFromHash(hash string) string {
	parts := strings.Split(hash, "_")
	if len(parts) != 4 {
		return ""
	}
	return parts[1]
}
