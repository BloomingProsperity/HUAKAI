// 包 tls — TLS 扩展字段的详细解析逻辑。
package tls

import (
	"encoding/binary"
)

// 已知的 TLS 扩展类型常量，按 IANA 注册编号定义。
const (
	ExtServerName           uint16 = 0x0000 // SNI
	ExtMaxFragmentLength    uint16 = 0x0001
	ExtStatusRequest        uint16 = 0x0005
	ExtSupportedGroups      uint16 = 0x000a // 椭圆曲线组
	ExtECPointFormats       uint16 = 0x000b
	ExtSignatureAlgorithms  uint16 = 0x000d
	ExtHeartbeat            uint16 = 0x000f
	ExtALPN                 uint16 = 0x0010 // 应用层协议协商
	ExtSignedCertTimestamp  uint16 = 0x0012
	ExtPadding              uint16 = 0x0015
	ExtExtendedMasterSecret uint16 = 0x0017
	ExtSessionTicket        uint16 = 0x0023
	ExtPreSharedKey         uint16 = 0x0029
	ExtEarlyData            uint16 = 0x002a
	ExtSupportedVersions    uint16 = 0x002b
	ExtCookie               uint16 = 0x002c
	ExtPSKKeyExchangeModes  uint16 = 0x002d
	ExtKeyShare             uint16 = 0x0033
	ExtCompressCert         uint16 = 0x001b
	ExtEncryptedClientHello uint16 = 0xfe0d // ECH 草案扩展
)

// ParsedExtension 保存单个 TLS 扩展的解析结果。
type ParsedExtension struct {
	// Type 是扩展编号（含 GREASE 值）
	Type uint16 `json:"type"`
	// TypeName 是人类可读的扩展名，未知时为空字符串
	TypeName string `json:"type_name,omitempty"`
	// IsGREASEValue 标记此扩展是否是 GREASE 占位
	IsGREASEValue bool `json:"is_grease,omitempty"`
	// DataLen 是扩展数据原始字节数
	DataLen int `json:"data_len"`

	// SNI 扩展（0x0000）解析结果
	SNIHostname string `json:"sni_hostname,omitempty"`

	// SupportedVersions 扩展（0x002b）解析结果
	SupportedVersions []uint16 `json:"supported_versions,omitempty"`

	// KeyShareGroups 扩展（0x0033）中的命名组列表（含 GREASE）
	KeyShareGroups []uint16 `json:"key_share_groups,omitempty"`

	// SignatureAlgorithms 扩展（0x000d）列表
	SignatureAlgorithms []uint16 `json:"signature_algorithms,omitempty"`

	// SupportedGroups 扩展（0x000a）命名组列表（含 GREASE）
	SupportedGroups []uint16 `json:"supported_groups,omitempty"`

	// ALPNProtocols 扩展（0x0010）列表，如 ["h2", "http/1.1"]
	ALPNProtocols []string `json:"alpn_protocols,omitempty"`

	// ECPointFormats 扩展（0x000b）列表
	ECPointFormats []uint8 `json:"ec_point_formats,omitempty"`

	// PaddingLen 扩展（0x0015）填充字节数
	PaddingLen int `json:"padding_len,omitempty"`

	// PSKModes 扩展（0x002d）列表
	PSKModes []uint8 `json:"psk_modes,omitempty"`

	// ECHPresent 标记 ClientHello 中是否存在 ECH 扩展（不解析内容）
	ECHPresent bool `json:"ech_present,omitempty"`
}

// extensionName 返回扩展编号对应的人类可读名称。
func extensionName(t uint16) string {
	switch t {
	case ExtServerName:
		return "server_name"
	case ExtMaxFragmentLength:
		return "max_fragment_length"
	case ExtStatusRequest:
		return "status_request"
	case ExtSupportedGroups:
		return "supported_groups"
	case ExtECPointFormats:
		return "ec_point_formats"
	case ExtSignatureAlgorithms:
		return "signature_algorithms"
	case ExtHeartbeat:
		return "heartbeat"
	case ExtALPN:
		return "application_layer_protocol_negotiation"
	case ExtSignedCertTimestamp:
		return "signed_certificate_timestamp"
	case ExtPadding:
		return "padding"
	case ExtExtendedMasterSecret:
		return "extended_master_secret"
	case ExtSessionTicket:
		return "session_ticket"
	case ExtPreSharedKey:
		return "pre_shared_key"
	case ExtEarlyData:
		return "early_data"
	case ExtSupportedVersions:
		return "supported_versions"
	case ExtCookie:
		return "cookie"
	case ExtPSKKeyExchangeModes:
		return "psk_key_exchange_modes"
	case ExtKeyShare:
		return "key_share"
	case ExtCompressCert:
		return "compress_certificate"
	case ExtEncryptedClientHello:
		return "encrypted_client_hello"
	default:
		return ""
	}
}

// parseServerName 解析 SNI 扩展数据，返回第一个 host_name 类型条目。
func parseServerName(data []byte) string {
	// SNI 结构：2字节列表总长 + 若干条目（1字节类型 + 2字节名称长度 + 名称）
	if len(data) < 2 {
		return ""
	}
	listLen := int(binary.BigEndian.Uint16(data[0:2]))
	if len(data) < 2+listLen {
		return ""
	}
	pos := 2
	for pos < 2+listLen {
		if pos+3 > len(data) {
			break
		}
		nameType := data[pos]
		nameLen := int(binary.BigEndian.Uint16(data[pos+1 : pos+3]))
		pos += 3
		if pos+nameLen > len(data) {
			break
		}
		if nameType == 0 { // host_name
			return string(data[pos : pos+nameLen])
		}
		pos += nameLen
	}
	return ""
}

// parseSupportedVersions 解析 supported_versions 扩展数据（ClientHello 格式）。
// ClientHello 中格式为：1字节列表字节数 + 若干 uint16 版本号。
func parseSupportedVersions(data []byte) []uint16 {
	if len(data) < 1 {
		return nil
	}
	listLen := int(data[0])
	if len(data) < 1+listLen || listLen%2 != 0 {
		return nil
	}
	count := listLen / 2
	versions := make([]uint16, count)
	for i := 0; i < count; i++ {
		versions[i] = binary.BigEndian.Uint16(data[1+i*2 : 1+i*2+2])
	}
	return versions
}

// parseKeyShare 解析 key_share 扩展数据（ClientHello 格式），返回命名组列表。
// 格式：2字节总长 + 若干（2字节 group + 2字节 key_exchange 长度 + key_exchange 数据）。
func parseKeyShare(data []byte) []uint16 {
	if len(data) < 2 {
		return nil
	}
	listLen := int(binary.BigEndian.Uint16(data[0:2]))
	if len(data) < 2+listLen {
		return nil
	}
	var groups []uint16
	pos := 2
	end := 2 + listLen
	for pos+4 <= end {
		group := binary.BigEndian.Uint16(data[pos : pos+2])
		keyLen := int(binary.BigEndian.Uint16(data[pos+2 : pos+4]))
		groups = append(groups, group)
		pos += 4 + keyLen
	}
	return groups
}

// parseUint16List 解析以 2字节长度前缀开头的 uint16 列表（通用格式）。
func parseUint16List(data []byte) []uint16 {
	if len(data) < 2 {
		return nil
	}
	listLen := int(binary.BigEndian.Uint16(data[0:2]))
	if len(data) < 2+listLen || listLen%2 != 0 {
		return nil
	}
	count := listLen / 2
	result := make([]uint16, count)
	for i := 0; i < count; i++ {
		result[i] = binary.BigEndian.Uint16(data[2+i*2 : 2+i*2+2])
	}
	return result
}

// parseALPN 解析 ALPN 扩展数据，返回协议名称列表。
// 格式：2字节协议列表总长 + 若干（1字节协议名长度 + 协议名字节串）。
func parseALPN(data []byte) []string {
	if len(data) < 2 {
		return nil
	}
	listLen := int(binary.BigEndian.Uint16(data[0:2]))
	if len(data) < 2+listLen {
		return nil
	}
	var protocols []string
	pos := 2
	end := 2 + listLen
	for pos < end {
		if pos+1 > end {
			break
		}
		nameLen := int(data[pos])
		pos++
		if pos+nameLen > end {
			break
		}
		protocols = append(protocols, string(data[pos:pos+nameLen]))
		pos += nameLen
	}
	return protocols
}

// parseECPointFormats 解析 ec_point_formats 扩展数据。
// 格式：1字节列表长度 + 若干单字节格式值。
func parseECPointFormats(data []byte) []uint8 {
	if len(data) < 1 {
		return nil
	}
	listLen := int(data[0])
	if len(data) < 1+listLen {
		return nil
	}
	formats := make([]uint8, listLen)
	copy(formats, data[1:1+listLen])
	return formats
}

// parsePSKModes 解析 psk_key_exchange_modes 扩展数据。
// 格式：1字节列表长度 + 若干单字节模式值。
func parsePSKModes(data []byte) []uint8 {
	if len(data) < 1 {
		return nil
	}
	listLen := int(data[0])
	if len(data) < 1+listLen {
		return nil
	}
	modes := make([]uint8, listLen)
	copy(modes, data[1:1+listLen])
	return modes
}

// ParseExtension 解析单个 TLS 扩展的类型和数据，填充 ParsedExtension 结构。
func ParseExtension(extType uint16, data []byte) ParsedExtension {
	ext := ParsedExtension{
		Type:          extType,
		TypeName:      extensionName(extType),
		IsGREASEValue: IsGREASE(extType),
		DataLen:       len(data),
	}

	// GREASE 扩展不做内容解析
	if ext.IsGREASEValue {
		return ext
	}

	switch extType {
	case ExtServerName:
		ext.SNIHostname = parseServerName(data)
	case ExtSupportedVersions:
		ext.SupportedVersions = parseSupportedVersions(data)
	case ExtKeyShare:
		ext.KeyShareGroups = parseKeyShare(data)
	case ExtSignatureAlgorithms:
		ext.SignatureAlgorithms = parseUint16List(data)
	case ExtSupportedGroups:
		ext.SupportedGroups = parseUint16List(data)
	case ExtALPN:
		ext.ALPNProtocols = parseALPN(data)
	case ExtECPointFormats:
		ext.ECPointFormats = parseECPointFormats(data)
	case ExtPadding:
		// 只记录长度，不存储填充内容
		ext.PaddingLen = len(data)
	case ExtPSKKeyExchangeModes:
		ext.PSKModes = parsePSKModes(data)
	case ExtEncryptedClientHello:
		// ECH 存在即记录，不解析加密内容
		ext.ECHPresent = true
	}
	// 其他未知扩展只记录 DataLen（已在初始化时赋值）
	return ext
}
