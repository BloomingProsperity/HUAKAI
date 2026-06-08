package mimicry

import (
	"fmt"
	"strings"
)

// UTLS-03: per-account DB TLS-fingerprint profile -> ClientHelloTemplate.
//
// The admin store (internal/tlsfpadmin / db) holds per-tenant fingerprint
// profiles as int32 columns. This converter widens them into a
// ClientHelloTemplate that the existing uTLS dialer consumes, so an account
// bound to a DB profile actually drives its upstream ClientHello (instead of the
// FK being write-only metadata). Kept in this package with a plain-[]int input
// struct so it does NOT import tlsfpadmin/db (no import cycle).

// ProfileFields mirrors the DB TLS-fingerprint columns (int32 widened to int).
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

// TemplateFromProfileFields converts admin-stored fields into a
// ClientHelloTemplate. It FAILS LOUD on out-of-range ids or missing core fields
// (cipher suites / curves / supported versions) — an invalid id would corrupt
// the ClientHello — so the caller can fall back to the builtin per-mode template
// rather than emit a broken handshake.
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

// ValidateProfileFields reports whether a DB TLS profile still produces a usable
// uTLS ClientHello. Preset profiles are valid iff the preset name is known;
// custom profiles must convert (range-valid) AND build a uTLS spec without error.
// UTLS-06 drift worker uses this to flag profiles that can no longer drive a
// handshake (e.g. bad cipher ids, or an unknown preset after an edit), without
// computing JA3 (which would risk false positives). nil = healthy.
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
