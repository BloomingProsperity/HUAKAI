package mimicry

import (
	"fmt"
	"strings"
)

// ProfileFields 是账号级数据库 TLS 指纹配置到 ClientHelloTemplate 的转换输入。
//
// 管理端存储按租户保存 int32 形态的指纹字段;这里把它们拓宽成 uTLS 拨号器
// 可消费的模板,使绑定 DB profile 的账号真正驱动上游 ClientHello。该结构保留
// 普通 []int 输入,避免本包反向导入管理端存储实现并形成 import cycle。

// ProfileFields 映射 DB TLS 指纹列(int32 在边界处拓宽为 int)。
type ProfileFields struct {
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

// TemplateFromProfileFields 把管理端存储字段转换成 ClientHelloTemplate。越界 id
// 或缺少核心字段(cipher suites / curves / supported versions)时必须 fail-loud:
// 无效 id 会破坏 ClientHello,调用方应回退到内置模式模板,不能发出坏握手。
func TemplateFromProfileFields(f ProfileFields) (*ClientHelloTemplate, error) {
	// UTLS-05: name "preset:<browser>" -> uTLS 内置浏览器 ClientHello, 无需
	// 手写 cipher 数组。运营建一个 name=preset:chrome 的 profile 即让绑定账号
	// 走真实 Chrome 指纹 (经 UTLS-03 DB-profile 路消费)。
	if preset, ok := strings.CutPrefix(f.Name, "preset:"); ok && strings.TrimSpace(preset) != "" {
		return &ClientHelloTemplate{ModeName: f.Name, Preset: strings.TrimSpace(preset), GREASE: f.GreaseEnabled, JA3: f.ExpectedJA3Hash}, nil
	}
	if len(f.CipherSuites) == 0 || len(f.SupportedCurves) == 0 || len(f.TLSSupportedVersions) == 0 {
		return nil, fmt.Errorf("mimicry: incomplete TLS profile (cipher_suites/curves/supported_versions required)")
	}
	ciphers, err := toUint16s("cipher_suites", f.CipherSuites)
	if err != nil {
		return nil, err
	}
	curves, err := toUint16s("supported_curves", f.SupportedCurves)
	if err != nil {
		return nil, err
	}
	versions, err := toUint16s("tls_supported_versions", f.TLSSupportedVersions)
	if err != nil {
		return nil, err
	}
	sigs, err := toUint16s("signature_algorithms", f.SignatureAlgorithms)
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
	return &ClientHelloTemplate{
		ModeName:            f.Name,
		TLSBackend:          "db_profile",
		GREASE:              f.GreaseEnabled,
		JA3:                 f.ExpectedJA3Hash,
		CipherSuites:        ciphers,
		Extensions:          extensions,
		SupportedVersions:   versions,
		EllipticCurves:      curves,
		SignatureAlgorithms: sigs,
		ALPNProtocols:       f.AlpnProtocols,
		ECPointFormats:      points,
		KeyShareGroups:      keyShares,
		PSKModes:            pskModes,
	}, nil
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

// ValidateProfileFields 检查 DB TLS profile 是否仍能生成可用的 uTLS ClientHello。
// preset profile 只有在名称可识别时才健康;自定义 profile 必须通过范围校验并能
// 构造 uTLS spec。漂移巡检用它标记无法再驱动握手的 profile(例如坏 cipher id
// 或编辑后未知 preset),且不计算 JA3,避免误报;nil 表示健康。
func ValidateProfileFields(f ProfileFields) error {
	tmpl, err := TemplateFromProfileFields(f)
	if err != nil {
		return err
	}
	if tmpl.Preset != "" {
		if _, ok := clientHelloIDForPreset(tmpl.Preset); !ok {
			return fmt.Errorf("mimicry: unknown preset %q", tmpl.Preset)
		}
		return nil
	}
	if _, err := tmpl.UTLSSpec(); err != nil {
		return err
	}
	return nil
}
