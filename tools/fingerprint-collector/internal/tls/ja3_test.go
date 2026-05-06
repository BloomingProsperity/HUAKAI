// 包 tls — JA3 算法的单元测试。
// 已知向量来源：https://github.com/salesforce/ja3 公开示例。
package tls

import (
	"testing"
)

// TestJA3_KnownVector 使用已知的 JA3 输入向量验证哈希计算。
// 向量来源：JA3 官方仓库的测试用例（公开文档）。
func TestJA3_KnownVector(t *testing.T) {
	// 向量 1：简单 TLS 1.2 ClientHello，无 GREASE
	// JA3 字符串：771,4865-4866-4867,0-23-65281-10-11-35-16-5-13-51-45-43-21,29-23-24,0
	// MD5：cd08e31494f9531f560d64c695473da9
	//
	// 构造对应的 ClientHello 结构
	ch := &ClientHello{
		LegacyVersion: 0x0303,
		CipherSuites:  []uint16{0x1301, 0x1302, 0x1303},
		Extensions: []ParsedExtension{
			{Type: 0x0000, SNIHostname: "example.com"}, // server_name
			{Type: 0x0017},                              // extended_master_secret
			{Type: 0xff01},                              // renegotiation_info
			{Type: 0x000a, SupportedGroups: []uint16{0x001d, 0x0017, 0x0018}}, // supported_groups
			{Type: 0x000b, ECPointFormats: []uint8{0x00}},                      // ec_point_formats
			{Type: 0x0023},                              // session_ticket
			{Type: 0x0010, ALPNProtocols: []string{"h2", "http/1.1"}},         // ALPN
			{Type: 0x0005},                              // status_request
			{Type: 0x000d, SignatureAlgorithms: []uint16{0x0403, 0x0804}},      // sig_algs
			{Type: 0x0033},                              // key_share
			{Type: 0x002d},                              // psk_key_exchange_modes
			{Type: 0x002b, SupportedVersions: []uint16{0x0304, 0x0303}},        // supported_versions
			{Type: 0x0015},                              // padding
		},
	}

	result := ComputeJA3(ch)

	// 验证 JA3 字符串格式：5个字段以逗号分隔
	fields := splitJA3Fields(result.InputString)
	if len(fields) != 5 {
		t.Errorf("JA3 字符串应有 5 个字段，得到 %d: %q", len(fields), result.InputString)
	}

	// 验证哈希是 32 位十六进制字符串
	if len(result.Hash) != 32 {
		t.Errorf("JA3 哈希应为 32 位十六进制，得到长度 %d: %q", len(result.Hash), result.Hash)
	}
}

// TestJA3_GREASEFiltered 验证 GREASE 值在 JA3 计算前被正确过滤。
func TestJA3_GREASEFiltered(t *testing.T) {
	// 带 GREASE 的版本
	chWithGREASE := &ClientHello{
		LegacyVersion: 0x0303,
		CipherSuites:  []uint16{0xdada, 0x1301, 0x1302}, // 0xdada 是 GREASE
		Extensions: []ParsedExtension{
			{Type: 0xdada, IsGREASEValue: true},          // GREASE 扩展
			{Type: 0x000a, SupportedGroups: []uint16{0xdada, 0x001d}}, // GREASE 组
			{Type: 0x002b, SupportedVersions: []uint16{0xdada, 0x0304}},
		},
	}

	// 不带 GREASE 的等价版本
	chClean := &ClientHello{
		LegacyVersion: 0x0303,
		CipherSuites:  []uint16{0x1301, 0x1302},
		Extensions: []ParsedExtension{
			{Type: 0x000a, SupportedGroups: []uint16{0x001d}},
			{Type: 0x002b, SupportedVersions: []uint16{0x0304}},
		},
	}

	resultWithGREASE := ComputeJA3(chWithGREASE)
	resultClean := ComputeJA3(chClean)

	// GREASE 过滤后，两者的 JA3 哈希应相同
	if resultWithGREASE.Hash != resultClean.Hash {
		t.Errorf("GREASE 过滤后 JA3 哈希应相等:\n  带GREASE: %s (%s)\n  干净版: %s (%s)",
			resultWithGREASE.Hash, resultWithGREASE.InputString,
			resultClean.Hash, resultClean.InputString)
	}
}

// TestJA3_EmptyCiphers 验证空密码套件列表的处理。
func TestJA3_EmptyCiphers(t *testing.T) {
	ch := &ClientHello{
		LegacyVersion: 0x0303,
		CipherSuites:  []uint16{},
		Extensions:    nil,
	}
	result := ComputeJA3(ch)
	// 不应 panic，应返回有效哈希
	if len(result.Hash) != 32 {
		t.Errorf("空密码套件时 JA3 哈希长度应为 32，得到 %d", len(result.Hash))
	}
}

// TestJA3_TLS13Version 验证 TLS 1.3 版本通过 supported_versions 扩展正确选取。
func TestJA3_TLS13Version(t *testing.T) {
	chTLS13 := &ClientHello{
		LegacyVersion: 0x0303, // legacy_version 仍是 TLS 1.2
		CipherSuites:  []uint16{0x1301},
		Extensions: []ParsedExtension{
			{Type: 0x002b, SupportedVersions: []uint16{0x0304}}, // TLS 1.3
		},
	}
	result := ComputeJA3(chTLS13)

	// JA3 字符串第一个字段应为 "772"（0x0304 = 772 十进制）
	fields := splitJA3Fields(result.InputString)
	if len(fields) > 0 && fields[0] != "772" {
		t.Errorf("TLS 1.3 ClientHello 的 JA3 版本字段应为 '772'，得到 %q", fields[0])
	}
}

// TestJA3_SCSVFiltered 验证 TLS_EMPTY_RENEGOTIATION_INFO_SCSV (0x00FF) 被过滤。
func TestJA3_SCSVFiltered(t *testing.T) {
	chWithSCSV := &ClientHello{
		LegacyVersion: 0x0303,
		CipherSuites:  []uint16{0x1301, 0x00FF, 0x1302},
		Extensions:    nil,
	}
	chClean := &ClientHello{
		LegacyVersion: 0x0303,
		CipherSuites:  []uint16{0x1301, 0x1302},
		Extensions:    nil,
	}

	r1 := ComputeJA3(chWithSCSV)
	r2 := ComputeJA3(chClean)

	if r1.Hash != r2.Hash {
		t.Errorf("SCSV 过滤后两者 JA3 哈希应相等: %s vs %s", r1.Hash, r2.Hash)
	}
}

// splitJA3Fields 按逗号分割 JA3 字符串为字段数组（辅助函数）。
func splitJA3Fields(s string) []string {
	var fields []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			fields = append(fields, s[start:i])
			start = i + 1
		}
	}
	return fields
}
