package mimicry

import (
	"fmt"
	"strings"
)

// ProfileFields 镜像数据库里的 TLS 指纹列，供解析层转换成 Rust IPC 动态 profile。
type ProfileFields struct {
	ID                   int64
	Name                 string
	GreaseEnabled        bool
	CipherSuites         []int
	SupportedCurves      []int
	EcPointFormats       []int
	SignatureAlgorithms  []int
	AlpnProtocols        []string
	TLSSupportedVersions []int
	KeyShareGroups       []int
	PskModes             []int
	ExtensionsOrder      []int
	ExpectedJA3Hash      string
}

// InlineTLSProfileFromFields 把数据库字段转换为 IPC v2 的动态 profile。
// `preset:*` 没有对应的 BoringSSL 线缆合同，因此明确拒绝，避免偷换 ClientHello。
func InlineTLSProfileFromFields(f ProfileFields) (*InlineTLSProfile, error) {
	if preset, ok := strings.CutPrefix(f.Name, "preset:"); ok && strings.TrimSpace(preset) != "" {
		return nil, fmt.Errorf("mimicry: preset profile %q 尚无 Rust/BoringSSL 等价实现", strings.TrimSpace(preset))
	}
	if len(f.CipherSuites) == 0 || len(f.SupportedCurves) == 0 || len(f.TLSSupportedVersions) == 0 || len(f.ExtensionsOrder) == 0 {
		return nil, fmt.Errorf("mimicry: incomplete TLS profile (cipher_suites/curves/supported_versions/extensions_order required)")
	}
	ciphers, err := toUint16s("cipher_suites", f.CipherSuites)
	if err != nil {
		return nil, err
	}
	groups, err := toUint16s("supported_curves", f.SupportedCurves)
	if err != nil {
		return nil, err
	}
	versions, err := toUint16s("tls_supported_versions", f.TLSSupportedVersions)
	if err != nil {
		return nil, err
	}
	signatures, err := toUint16s("signature_algorithms", f.SignatureAlgorithms)
	if err != nil {
		return nil, err
	}
	keyShares, err := toUint16s("key_share_groups", f.KeyShareGroups)
	if err != nil {
		return nil, err
	}
	extensions, err := toUint16s("extensions_order", f.ExtensionsOrder)
	if err != nil {
		return nil, err
	}
	points, err := toUint8s("ec_point_formats", f.EcPointFormats)
	if err != nil {
		return nil, err
	}
	pskModes, err := toUint8s("psk_modes", f.PskModes)
	if err != nil {
		return nil, err
	}
	id := "db-profile:" + strings.TrimSpace(f.Name)
	if f.ID > 0 {
		id = fmt.Sprintf("db-profile-%d", f.ID)
	}
	profile := &InlineTLSProfile{
		ID:                   id,
		GreaseEnabled:        f.GreaseEnabled,
		CipherSuites:         ciphers,
		SupportedGroups:      groups,
		ECPointFormats:       points,
		SignatureAlgorithms:  signatures,
		ALPNProtocols:        append([]string(nil), f.AlpnProtocols...),
		TLSSupportedVersions: versions,
		KeyShareGroups:       keyShares,
		PSKModes:             pskModes,
		ExtensionsOrder:      extensions,
	}
	if err := profile.Validate(); err != nil {
		return nil, err
	}
	return profile, nil
}

func toUint16s(field string, in []int) ([]uint16, error) {
	out := make([]uint16, 0, len(in))
	for _, v := range in {
		if v < 0 || v > 0xFFFF {
			return nil, fmt.Errorf("mimicry: %s id %d out of uint16 range", field, v)
		}
		out = append(out, uint16(v))
	}
	return out, nil
}

func toUint8s(field string, in []int) ([]uint8, error) {
	out := make([]uint8, 0, len(in))
	for _, v := range in {
		if v < 0 || v > 0xFF {
			return nil, fmt.Errorf("mimicry: %s id %d out of uint8 range", field, v)
		}
		out = append(out, uint8(v))
	}
	return out, nil
}

// ValidateProfileFields 报告数据库 profile 是否能无损转换成 Rust IPC 动态 profile。
// 返回 nil 只表示数据合同可执行；真实 wire 一致性由 Rust 线缆测试负责。
func ValidateProfileFields(f ProfileFields) error {
	_, err := InlineTLSProfileFromFields(f)
	return err
}
