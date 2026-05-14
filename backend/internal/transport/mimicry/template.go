// 包 mimicry 提供 R-3 传输层指纹模板与 uTLS 规格转换。
package mimicry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ClientHelloTemplate 是 collector 净化输出到 uTLS ClientHelloSpec 的中间格式。
type ClientHelloTemplate struct {
	ModeName            string   `json:"mode_name,omitempty"`
	CollectedAt         string   `json:"collected_at,omitempty"`
	TargetHost          string   `json:"target_host,omitempty"`
	JA3                 string   `json:"ja3"`
	JA4                 string   `json:"ja4"`
	CipherSuites        []uint16 `json:"cipher_suites"`
	Extensions          []uint16 `json:"extensions"`
	SupportedVersions   []uint16 `json:"supported_versions"`
	EllipticCurves      []uint16 `json:"curves"`
	SignatureAlgorithms []uint16 `json:"sig_algos"`
	ALPNProtocols       []string `json:"alpn_protocols,omitempty"`
	ECPointFormats      []uint8  `json:"ec_point_formats,omitempty"`
	KeyShareGroups      []uint16 `json:"key_share_groups,omitempty"`
	PSKModes            []uint8  `json:"psk_modes,omitempty"`
	PaddingLen          int      `json:"padding_len,omitempty"`
	EarlyDataEnabled    bool     `json:"early_data_enabled"`
}

// LoadFromCollectorOutput 读取 collector v1 输出或 Phase A 合并模板。
// path 可传旧版 clienthello-template.json 文件路径，也可传 output 目录；
// targetName 非空时读取 output/<target_name>/clienthello-template.json。
func LoadFromCollectorOutput(path string, targetName ...string) (*ClientHelloTemplate, error) {
	resolvedTargetName := optionalTargetName(targetName)
	data, err := os.ReadFile(collectorTemplatePath(path, resolvedTargetName))
	if err != nil {
		return nil, err
	}
	var flat ClientHelloTemplate
	if err := json.Unmarshal(data, &flat); err == nil && flat.looksFlat() {
		if resolvedTargetName != "" && flat.ModeName == "" {
			flat.ModeName = resolvedTargetName
		}
		if err := flat.Validate(); err != nil {
			return nil, err
		}
		return &flat, nil
	}

	var raw collectorOutput
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	t := &ClientHelloTemplate{
		ModeName:            firstNonEmpty(firstNonEmpty(raw.ModeName, resolvedTargetName), "anthropic-claude-code"),
		CollectedAt:         firstNonEmpty(raw.CollectedAt, raw.CaptureTime),
		TargetHost:          firstNonEmpty(raw.TargetHost, "api.anthropic.com"),
		JA3:                 raw.JA3.InputString,
		JA4:                 firstNonEmpty(raw.JA4.Hash, raw.JA4.Raw),
		CipherSuites:        raw.cipherIDs(),
		Extensions:          raw.extensionIDs(),
		SupportedVersions:   raw.SupportedVersions,
		EllipticCurves:      raw.SupportedGroups,
		SignatureAlgorithms: raw.signatureAlgorithms(),
		ALPNProtocols:       raw.ALPNProtocols,
		ECPointFormats:      raw.ECPointFormats,
		KeyShareGroups:      []uint16{29},
		PSKModes:            []uint8{1},
		PaddingLen:          raw.paddingLen(),
	}
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return t, nil
}

func optionalTargetName(targetName []string) string {
	if len(targetName) == 0 {
		return ""
	}
	return strings.TrimSpace(targetName[0])
}

func collectorTemplatePath(path string, targetName string) string {
	if targetName != "" {
		if strings.HasSuffix(filepath.Base(path), ".json") {
			path = filepath.Dir(path)
		}
		return filepath.Join(path, targetName, "clienthello-template.json")
	}
	if strings.HasSuffix(filepath.Base(path), ".json") {
		return path
	}
	return filepath.Join(path, "clienthello-template.json")
}

func (t *ClientHelloTemplate) Validate() error {
	if t == nil {
		return errors.New("mimicry: nil clienthello template")
	}
	stub := t.IsStub()
	checks := []struct {
		name string
		ok   bool
	}{
		{"mode_name", t.ModeName != ""},
		{"collected_at", t.CollectedAt != ""},
		{"target_host", t.TargetHost != ""},
		{"cipher_suites", stub || len(t.CipherSuites) > 0},
		{"extensions", stub || len(t.Extensions) > 0},
		{"supported_versions", stub || len(t.SupportedVersions) > 0},
		{"curves", stub || len(t.EllipticCurves) > 0},
		{"sig_algos", stub || len(t.SignatureAlgorithms) > 0},
	}
	for _, c := range checks {
		if !c.ok {
			return fmt.Errorf("mimicry: missing required field %s", c.name)
		}
	}
	if stub {
		return nil
	}
	if t.JA3 == "" || t.JA4 == "" {
		return errors.New("mimicry: ja3 and ja4 must both be set or both be empty for stub")
	}
	if _, _, _, _, err := parseJA3(t.JA3); err != nil {
		return err
	}
	return nil
}

func (t *ClientHelloTemplate) IsStub() bool { return t != nil && t.JA3 == "" && t.JA4 == "" }

// PhaseADefaultTemplate 是 2026-05-06 Anthropic 样本的净化模板。
func PhaseADefaultTemplate() *ClientHelloTemplate {
	return &ClientHelloTemplate{
		ModeName:            "anthropic-claude-code",
		CollectedAt:         "2026-05-06T10:37:23Z",
		TargetHost:          "api.anthropic.com",
		JA3:                 "772,4865-4866-4867-49195-49199-49196-49200-52393-52392-49161-49171-49162-49172-156-157-47-53,0-65037-23-65281-10-11-35-16-5-13-18-51-45-43,29-23-24,0",
		JA4:                 "t13d1715_ht_ca21dff6868a_bd55c1d574e4",
		CipherSuites:        []uint16{4865, 4866, 4867, 49195, 49199, 49196, 49200, 52393, 52392, 49161, 49171, 49162, 49172, 156, 157, 47, 53},
		Extensions:          []uint16{0, 65037, 23, 65281, 10, 11, 35, 16, 5, 13, 18, 51, 45, 43, 21},
		SupportedVersions:   []uint16{772, 771},
		EllipticCurves:      []uint16{29, 23, 24},
		SignatureAlgorithms: anthropicSigAlgos(),
		ALPNProtocols:       []string{"http/1.1"},
		ECPointFormats:      []uint8{0},
		KeyShareGroups:      []uint16{29},
		PSKModes:            []uint8{1},
		PaddingLen:          41,
	}
}

func anthropicSigAlgos() []uint16 {
	return []uint16{1027, 2052, 1025, 1283, 2053, 1281, 2054, 1537, 513}
}

func parseJA3(s string) (uint16, []uint16, []uint16, []uint16, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 5 {
		return 0, nil, nil, nil, fmt.Errorf("mimicry: invalid ja3 field count")
	}
	ver, err := parseUint16(parts[0])
	if err != nil {
		return 0, nil, nil, nil, fmt.Errorf("mimicry: invalid ja3 version: %w", err)
	}
	ciphers, err := parseUint16List(parts[1])
	if err != nil {
		return 0, nil, nil, nil, fmt.Errorf("mimicry: invalid ja3 ciphers: %w", err)
	}
	exts, err := parseUint16List(parts[2])
	if err != nil {
		return 0, nil, nil, nil, fmt.Errorf("mimicry: invalid ja3 extensions: %w", err)
	}
	curves, err := parseUint16List(parts[3])
	if err != nil {
		return 0, nil, nil, nil, fmt.Errorf("mimicry: invalid ja3 curves: %w", err)
	}
	return ver, ciphers, exts, curves, nil
}

func parseUint16List(s string) ([]uint16, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, "-")
	out := make([]uint16, len(parts))
	for i, p := range parts {
		v, err := parseUint16(p)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func (t ClientHelloTemplate) looksFlat() bool {
	return t.ModeName != "" || t.CollectedAt != "" || t.TargetHost != "" || t.JA3 != "" || t.JA4 != ""
}

func parseUint16(s string) (uint16, error) {
	v, err := strconv.ParseUint(s, 10, 16)
	return uint16(v), err
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
