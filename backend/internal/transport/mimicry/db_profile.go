package mimicry

import (
	"fmt"
	"strings"
)

// UTLS-03：按账号的 DB TLS 指纹 profile -> ClientHelloTemplate。
//
// admin 存储(internal/tlsfpadmin / db)把每租户的指纹 profile 以 int32 列保存。
// 本转换器将其加宽成既有 uTLS dialer 可消费的 ClientHelloTemplate，使绑定到 DB
// profile 的账号真正驱动其上游 ClientHello(而非让该 FK 沦为只写元数据)。放在
// 本包内并以纯 []int 入参 struct，从而**不** import tlsfpadmin/db(避免 import 环)。

// ProfileFields 镜像 DB 里的 TLS 指纹列(int32 加宽为 int)。
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
// `preset:*` 依赖 Go uTLS 的浏览器模板，当前不能无损交给 BoringSSL，因此明确拒绝，
// 避免运行时把浏览器 preset 偷换成另一套 ClientHello。
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

// TemplateFromProfileFields 把 admin 存储的字段转换成 ClientHelloTemplate。遇到
// 越界 id 或缺少核心字段(cipher suites / curves / supported versions)时**fail
// loud**——非法 id 会破坏 ClientHello——这样调用方可回退到内置的 per-mode 模板，
// 而非发出一次损坏的握手。
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

// ValidateProfileFields 报告一个 DB TLS profile 是否仍能产出可用的 uTLS
// ClientHello。preset profile 当且仅当 preset 名已知时有效；自定义 profile 必须既
// 能转换(范围合法)又能无错地构建 uTLS spec。UTLS-06 的 drift worker 用它来标记
// 那些已无法驱动握手的 profile(如 cipher id 非法，或编辑后 preset 变为未知)，且不
// 计算 JA3(那会有误报风险)。返回 nil = 健康。
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
