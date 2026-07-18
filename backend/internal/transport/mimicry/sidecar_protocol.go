package mimicry

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
)

const (
	SidecarProtocolVersion uint16 = 2

	sidecarOperationConnect = "connect"
	sidecarOperationReady   = "ready"

	SidecarCapabilityBuiltinProfile = "builtin_profile"
	SidecarCapabilityInlineProfile  = "inline_profile"
	SidecarCapabilityHTTPProxy      = "http_proxy"
	SidecarCapabilityHTTPSProxy     = "https_proxy"
	SidecarCapabilitySOCKS5Proxy    = "socks5_proxy"
	SidecarCapabilityH2Bridge       = "h2_bridge"
	SidecarCapabilityForceH1        = "force_h1"

	SidecarProfileAnthropicCLIMimicryV1 = "anthropic-cli-mimicry-v1"
	SidecarProfileOpenAICodexCLIV1      = "openai-codex-cli-v1"
	SidecarProfileGeminiCLIV1           = "gemini-cli-v1"
	SidecarProfileKiroCLIV1             = "kiro-cli-v1"

	sidecarMaxFrameLen = 1024 * 1024
)

const (
	SidecarErrorProtocolUnsupported  = "protocol_unsupported"
	SidecarErrorOperationUnsupported = "operation_unsupported"
	SidecarErrorProfileUnknown       = "profile_unknown"
	SidecarErrorProfileInvalid       = "profile_invalid"
	SidecarErrorTargetInvalid        = "target_invalid"
	SidecarErrorProxyInvalid         = "proxy_invalid"
	SidecarErrorProxyConnect         = "proxy_connect"
	SidecarErrorUpstreamDNS          = "upstream_dns"
	SidecarErrorConnectionRefused    = "upstream_connection_refused"
	SidecarErrorNetworkUnreachable   = "upstream_network_unreachable"
	SidecarErrorUpstreamConnect      = "upstream_connect"
	SidecarErrorUpstreamTimeout      = "upstream_timeout"
	SidecarErrorTLSHandshake         = "tls_handshake"
	SidecarErrorInternal             = "internal"
)

// InlineTLSProfile 是 Go 与 Rust 共享的动态 TLS 指纹合同。它只包含数据库能够
// 无损表达且实际影响 ClientHello 的字段，不携带账号凭据或请求内容。
type InlineTLSProfile struct {
	ID                   string   `json:"id"`
	GreaseEnabled        bool     `json:"grease_enabled"`
	CipherSuites         []uint16 `json:"cipher_suites"`
	SupportedGroups      []uint16 `json:"supported_groups"`
	ECPointFormats       []uint8  `json:"ec_point_formats"`
	SignatureAlgorithms  []uint16 `json:"signature_algorithms"`
	ALPNProtocols        []string `json:"alpn_protocols"`
	TLSSupportedVersions []uint16 `json:"tls_supported_versions"`
	KeyShareGroups       []uint16 `json:"key_share_groups"`
	PSKModes             []uint8  `json:"psk_modes"`
	ExtensionsOrder      []uint16 `json:"extensions_order"`
}

func (p *InlineTLSProfile) Validate() error {
	if p == nil {
		return errors.New("mimicry sidecar: nil inline profile")
	}
	if id := strings.TrimSpace(p.ID); id == "" || len(id) > 128 {
		return fmt.Errorf("mimicry sidecar: inline profile id 长度必须为 1..128")
	}
	if len(p.CipherSuites) == 0 || len(p.CipherSuites) > 256 {
		return fmt.Errorf("mimicry sidecar: inline profile cipher_suites 数量必须为 1..256")
	}
	if len(p.SupportedGroups) == 0 || len(p.SupportedGroups) > 64 {
		return fmt.Errorf("mimicry sidecar: inline profile supported_groups 数量必须为 1..64")
	}
	if len(p.TLSSupportedVersions) == 0 || len(p.TLSSupportedVersions) > 8 {
		return fmt.Errorf("mimicry sidecar: inline profile tls_supported_versions 数量必须为 1..8")
	}
	if len(p.ExtensionsOrder) == 0 || len(p.ExtensionsOrder) > 128 {
		return fmt.Errorf("mimicry sidecar: inline profile extensions_order 数量必须为 1..128")
	}
	if len(p.SignatureAlgorithms) > 128 || len(p.KeyShareGroups) > 32 || len(p.PSKModes) > 8 || len(p.ECPointFormats) > 16 {
		return fmt.Errorf("mimicry sidecar: inline profile 数组字段超过上限")
	}
	if len(p.ALPNProtocols) > 8 {
		return fmt.Errorf("mimicry sidecar: inline profile alpn_protocols 数量超过 8")
	}
	for _, protocol := range p.ALPNProtocols {
		if protocol == "" || len(protocol) > 255 {
			return fmt.Errorf("mimicry sidecar: inline profile ALPN 长度必须为 1..255")
		}
	}
	for _, version := range p.TLSSupportedVersions {
		switch version {
		case 0x0301, 0x0302, 0x0303, 0x0304:
		default:
			return fmt.Errorf("mimicry sidecar: inline profile 不支持 TLS version %d", version)
		}
	}
	if duplicateUint16(p.CipherSuites) || duplicateUint16(p.SupportedGroups) || duplicateUint16(p.SignatureAlgorithms) || duplicateUint16(p.TLSSupportedVersions) || duplicateUint16(p.KeyShareGroups) || duplicateUint16(p.ExtensionsOrder) {
		return fmt.Errorf("mimicry sidecar: inline profile 不允许重复的 uint16 字段")
	}
	if duplicateUint8(p.ECPointFormats) || duplicateUint8(p.PSKModes) || duplicateString(p.ALPNProtocols) {
		return fmt.Errorf("mimicry sidecar: inline profile 不允许重复值")
	}
	if len(p.PSKModes) != 1 || p.PSKModes[0] != 1 {
		return fmt.Errorf("mimicry sidecar: inline profile psk_modes 当前必须精确为 [1]")
	}
	if !orderedUint16Subset(p.KeyShareGroups, p.SupportedGroups) {
		return fmt.Errorf("mimicry sidecar: inline profile key_share_groups 必须按 supported_groups 的相同顺序取子集")
	}
	for _, required := range []struct {
		extension uint16
		present   bool
		field     string
	}{
		{extension: 10, present: len(p.SupportedGroups) > 0, field: "supported_groups"},
		{extension: 11, present: len(p.ECPointFormats) > 0, field: "ec_point_formats"},
		{extension: 13, present: len(p.SignatureAlgorithms) > 0, field: "signature_algorithms"},
		{extension: 16, present: len(p.ALPNProtocols) > 0, field: "alpn_protocols"},
		{extension: 43, present: len(p.TLSSupportedVersions) > 0, field: "tls_supported_versions"},
		{extension: 45, present: len(p.PSKModes) > 0, field: "psk_modes"},
		{extension: 51, present: len(p.KeyShareGroups) > 0, field: "key_share_groups"},
	} {
		if required.present && !sidecarContainsUint16(p.ExtensionsOrder, required.extension) {
			return fmt.Errorf("mimicry sidecar: inline profile extensions_order 缺少 %s 对应的扩展 %d", required.field, required.extension)
		}
	}
	return nil
}

func (p *InlineTLSProfile) clone() *InlineTLSProfile {
	if p == nil {
		return nil
	}
	out := *p
	out.CipherSuites = append([]uint16(nil), p.CipherSuites...)
	out.SupportedGroups = append([]uint16(nil), p.SupportedGroups...)
	out.ECPointFormats = append([]uint8(nil), p.ECPointFormats...)
	out.SignatureAlgorithms = append([]uint16(nil), p.SignatureAlgorithms...)
	out.ALPNProtocols = append([]string(nil), p.ALPNProtocols...)
	out.TLSSupportedVersions = append([]uint16(nil), p.TLSSupportedVersions...)
	out.KeyShareGroups = append([]uint16(nil), p.KeyShareGroups...)
	out.PSKModes = append([]uint8(nil), p.PSKModes...)
	out.ExtensionsOrder = append([]uint16(nil), p.ExtensionsOrder...)
	return &out
}

func duplicateUint16(values []uint16) bool {
	seen := make(map[uint16]struct{}, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func duplicateUint8(values []uint8) bool {
	seen := make(map[uint8]struct{}, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func duplicateString(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func orderedUint16Subset(subset, values []uint16) bool {
	start := 0
	for _, wanted := range subset {
		found := false
		for start < len(values) {
			value := values[start]
			start++
			if value == wanted {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func sidecarContainsUint16(values []uint16, wanted uint16) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

type sidecarControlRequest struct {
	Version       uint16            `json:"version"`
	Operation     string            `json:"operation"`
	TargetHost    string            `json:"target_host,omitempty"`
	Port          uint16            `json:"port,omitempty"`
	ProfileID     string            `json:"profile_id,omitempty"`
	InlineProfile *InlineTLSProfile `json:"inline_profile,omitempty"`
	CorrelationID string            `json:"correlation_id,omitempty"`
	ForceH1       *bool             `json:"force_h1,omitempty"`
	Proxy         *sidecarProxySpec `json:"proxy,omitempty"`
}

type sidecarControlAck struct {
	Version      uint16               `json:"version"`
	OK           bool                 `json:"ok"`
	Error        *sidecarControlError `json:"error,omitempty"`
	Capabilities []string             `json:"capabilities,omitempty"`
	ProfileIDs   []string             `json:"profile_ids,omitempty"`
}

type sidecarControlError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SidecarError 保留 Rust 返回的稳定错误码，调用链无需再解析英文错误文本。
type SidecarError struct {
	Code    string
	Message string
}

func (e *SidecarError) Error() string {
	if e == nil {
		return "mimicry sidecar: unknown error"
	}
	if e.Message == "" {
		return "mimicry sidecar: " + e.Code
	}
	return fmt.Sprintf("mimicry sidecar: %s: %s", e.Code, e.Message)
}

func (e *SidecarError) TransportErrorCode() string {
	if e == nil {
		return ""
	}
	return e.Code
}

func (e *SidecarError) Is(target error) bool {
	if e == nil {
		return false
	}
	switch target {
	case ErrSidecarProfileUnavailable:
		return e.Code == SidecarErrorProfileUnknown || e.Code == SidecarErrorProfileInvalid
	case ErrSidecarUnavailable:
		return e.Code == SidecarErrorProtocolUnsupported || e.Code == SidecarErrorOperationUnsupported || e.Code == SidecarErrorInternal
	default:
		return false
	}
}

type SidecarStatus struct {
	Version      uint16
	Capabilities []string
	ProfileIDs   []string
}

func writeSidecarFrame(conn net.Conn, value any) (int, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	if len(body) > sidecarMaxFrameLen {
		return 0, fmt.Errorf("frame length %d exceeds max %d", len(body), sidecarMaxFrameLen)
	}
	var prefix [4]byte
	binary.LittleEndian.PutUint32(prefix[:], uint32(len(body)))
	if err := writeFullConn(conn, prefix[:]); err != nil {
		return 0, err
	}
	if err := writeFullConn(conn, body); err != nil {
		return 0, err
	}
	return len(body), nil
}

func readSidecarFrame(conn net.Conn, value any) error {
	var prefix [4]byte
	if _, err := io.ReadFull(conn, prefix[:]); err != nil {
		return err
	}
	n := binary.LittleEndian.Uint32(prefix[:])
	if n > sidecarMaxFrameLen {
		return fmt.Errorf("frame length %d exceeds max %d", n, sidecarMaxFrameLen)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(conn, body); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("sidecar frame contains trailing JSON value")
		}
		return err
	}
	return nil
}

func writeFullConn(conn net.Conn, buf []byte) error {
	for len(buf) > 0 {
		n, err := conn.Write(buf)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		buf = buf[n:]
	}
	return nil
}
