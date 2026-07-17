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
	ModeName string `json:"mode_name,omitempty"`
	// Preset 非空时走 uTLS 内置浏览器 ClientHello (chrome/firefox/safari/edge/ios),
	// 由 uTLS 生成真实当前浏览器指纹, 不手写 cipher 数组 (UTLS-05)。其它字段忽略。
	Preset              string    `json:"preset,omitempty"`
	CollectedAt         string    `json:"collected_at,omitempty"`
	TargetHost          string    `json:"target_host,omitempty"`
	TLSBackend          string    `json:"tls_backend,omitempty"`
	GREASE              bool      `json:"grease"`
	ExtensionOrder      string    `json:"extension_order,omitempty"`
	JA3                 string    `json:"ja3"`
	JA4                 string    `json:"ja4"`
	CipherSuites        []uint16  `json:"cipher_suites"`
	Extensions          []uint16  `json:"extensions"`
	SupportedVersions   []uint16  `json:"supported_versions"`
	EllipticCurves      []uint16  `json:"curves"`
	SignatureAlgorithms []uint16  `json:"sig_algos"`
	ALPNProtocols       []string  `json:"alpn_protocols,omitempty"`
	ECPointFormats      []uint8   `json:"ec_point_formats,omitempty"`
	KeyShareGroups      []uint16  `json:"key_share_groups,omitempty"`
	PSKModes            []uint8   `json:"psk_modes,omitempty"`
	PaddingLen          int       `json:"padding_len,omitempty"`
	EarlyDataEnabled    bool      `json:"early_data_enabled"`
	HTTPLayer           HTTPLayer `json:"http_layer,omitempty"`
	AuthLayer           AuthLayer `json:"auth_layer,omitempty"`
}

// HTTPLayer 记录模型 API 的 HTTP 指纹元数据。
// 这些字段用于请求构造与运维审计；旧 stub 缺失时不影响 TLS 模板加载。
type HTTPLayer struct {
	Protocol        string   `json:"protocol,omitempty"`
	Endpoint        string   `json:"endpoint,omitempty"`
	UserAgent       string   `json:"user_agent,omitempty"`
	HeaderOrder     []string `json:"header_order,omitempty"`
	AuthMechanism   string   `json:"auth_mechanism,omitempty"`
	RefreshEndpoint string   `json:"refresh_endpoint,omitempty"`
}

// AuthLayer 记录鉴权层摘要。真实 secret 必须只用占位符描述。
type AuthLayer struct {
	Mechanism           string   `json:"mechanism,omitempty"`
	AuthorizationHeader string   `json:"authorization_header,omitempty"`
	AccountHeader       string   `json:"account_header,omitempty"`
	ConditionalHeaders  []string `json:"conditional_headers,omitempty"`
	RefreshEndpoint     string   `json:"refresh_endpoint,omitempty"`
	TelemetryMechanism  string   `json:"telemetry_mechanism,omitempty"`
	TokenSource         string   `json:"token_source,omitempty"`
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
		TLSBackend:          raw.TLSBackend,
		JA3:                 raw.JA3.InputString,
		JA4:                 firstNonEmpty(raw.JA4.Hash, raw.JA4.Raw),
		CipherSuites:        raw.cipherIDs(),
		Extensions:          raw.extensionIDs(),
		SupportedVersions:   raw.SupportedVersions,
		EllipticCurves:      raw.SupportedGroups,
		SignatureAlgorithms: raw.signatureAlgorithms(),
		ALPNProtocols:       raw.ALPNProtocols,
		ECPointFormats:      raw.ECPointFormats,
		KeyShareGroups:      raw.keyShareGroups(),
		PSKModes:            raw.pskModes(),
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
	if t.JA4 == "" {
		return errors.New("mimicry: ja4 must be set when ja3 marks a real template")
	}
	if _, _, _, _, err := parseJA3(t.JA3); err != nil {
		return err
	}
	return nil
}

func (t *ClientHelloTemplate) IsStub() bool { return t != nil && t.JA3 == "" }

// AnthropicCLIMimicryV1Template 是 Anthropic OAuth / Claude CLI 路径的
// Go uTLS Phase 1 profile。TLS 字段来自 HUAKAI 已净化的 2026-05-24
// Node.js v22.22.2/OpenSSL 3.5.5 ClientHello capture，profile id 与
// sidecar 后续 profile.toml 约定保持一致。
func AnthropicCLIMimicryV1Template() *ClientHelloTemplate {
	return &ClientHelloTemplate{
		ModeName:            SidecarProfileAnthropicCLIMimicryV1,
		CollectedAt:         "2026-05-24T12:56:55Z",
		TargetHost:          "api.anthropic.com",
		TLSBackend:          "nodejs/openssl",
		GREASE:              false,
		ExtensionOrder:      "captured",
		JA3:                 "772,4866-4867-4865-49199-49195-49200-49196-158-49191-103-49192-107-163-159-52393-52392-52394-49325-49311-49245-49249-49239-49235-162-49324-49310-49244-49248-49238-49234-49188-106-49187-64-49162-49172-57-56-49161-49171-51-50-157-49309-49233-156-49308-49232-61-60-53-47,65281-0-11-10-35-16-22-23-13-43-45-51,4588-29-23-30-24-25-256-257,0-1-2",
		JA4:                 "t13d5212_ht_9b003dc3eba7_4e5c652b160e",
		CipherSuites:        []uint16{4866, 4867, 4865, 49199, 49195, 49200, 49196, 158, 49191, 103, 49192, 107, 163, 159, 52393, 52392, 52394, 49325, 49311, 49245, 49249, 49239, 49235, 162, 49324, 49310, 49244, 49248, 49238, 49234, 49188, 106, 49187, 64, 49162, 49172, 57, 56, 49161, 49171, 51, 50, 157, 49309, 49233, 156, 49308, 49232, 61, 60, 53, 47},
		Extensions:          []uint16{65281, 0, 11, 10, 35, 16, 22, 23, 13, 43, 45, 51},
		SupportedVersions:   []uint16{772, 771},
		EllipticCurves:      []uint16{4588, 29, 23, 30, 24, 25, 256, 257},
		SignatureAlgorithms: []uint16{2309, 2310, 2308, 1027, 1283, 1539, 2055, 2056, 2074, 2075, 2076, 2057, 2058, 2059, 2052, 2053, 2054, 1025, 1281, 1537, 771, 769, 770, 1026, 1282, 1538},
		ALPNProtocols:       []string{"h2", "http/1.1"},
		ECPointFormats:      []uint8{0, 1, 2},
		KeyShareGroups:      []uint16{4588, 29},
		PSKModes:            []uint8{1},
		HTTPLayer: HTTPLayer{
			Protocol:        "h2_or_http1.1_nodejs_openssl",
			Endpoint:        "https://platform.claude.com/v1/oauth/token",
			UserAgent:       "claude-cli-compatible",
			HeaderOrder:     []string{"Content-Type", "Accept", "Authorization"},
			AuthMechanism:   "oauth_bearer",
			RefreshEndpoint: "https://platform.claude.com/v1/oauth/token",
		},
		AuthLayer: AuthLayer{
			Mechanism:           "oauth_bearer",
			AuthorizationHeader: "Authorization: Bearer <access_token>",
			RefreshEndpoint:     "https://platform.claude.com/v1/oauth/token",
			TokenSource:         "HUAKAI encrypted account credential",
		},
	}
}

// PhaseADefaultTemplate 是 2026-05-06 Anthropic 样本的旧 Phase A uTLS 兼容默认。
// 与 AnthropicCLIMimicryV1Template (Node22 OpenSSL 真抓包) 独立 — 本模板含
// ECH GREASE 扩展 65037 + GREASE=true,供 HUAKAI_TRANSPORT_PHASE_A_FALLBACK opt-in
// 测试与 stub fail-loud fallback 使用,不参与新 anthropic-cli-mimicry-v1 抓包数据。
func PhaseADefaultTemplate() *ClientHelloTemplate {
	return &ClientHelloTemplate{
		ModeName:            "anthropic-claude-code",
		CollectedAt:         "2026-05-06T10:37:23Z",
		TargetHost:          "api.anthropic.com",
		GREASE:              true,
		ExtensionOrder:      "captured",
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
