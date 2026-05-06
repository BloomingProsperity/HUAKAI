// 包 output 负责将捕获结果序列化为各种输出文件格式。
package output

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tlspkg "github.com/BloomingProsperity/HUAKAI/tools/fingerprint-collector/internal/tls"
)

// 工具版本号，构建时可通过 -ldflags 覆盖。
var Version = "0.1.0"

// ClientHelloTemplate 是可提交到版本库的净化 ClientHello 指纹模板。
// 不含 IP、MAC、主机名；SNI 默认替换为占位符。
type ClientHelloTemplate struct {
	// SchemaVersion 用于未来格式兼容性检测
	SchemaVersion string `json:"schema_version"`
	// CaptureTime 是捕获时的 UTC 时间戳（ISO 8601）
	CaptureTime string `json:"capture_time"`
	// SampleCount 是用于生成此模板的 ClientHello 样本数
	SampleCount int `json:"sample_count"`
	// LegacyVersion 是 ClientHello legacy_version 字段值
	LegacyVersion uint16 `json:"legacy_version"`
	// LegacyVersionHex 同上，十六进制表示便于阅读
	LegacyVersionHex string `json:"legacy_version_hex"`
	// RandomLen 始终为 32
	RandomLen int `json:"random_len"`
	// LegacySessionIDLen 是 session_id 长度
	LegacySessionIDLen int `json:"legacy_session_id_len"`
	// CipherSuites 是密码套件有序列表（含 GREASE 标记）
	CipherSuites []CipherSuiteEntry `json:"cipher_suites"`
	// CompressionMethods 是压缩方法列表
	CompressionMethods []uint8 `json:"compression_methods"`
	// Extensions 是扩展有序列表
	Extensions []ExtensionEntry `json:"extensions"`
	// SNI 在 -include-sni 未设置时为占位符 "<redacted>"
	SNI string `json:"sni"`
	// ALPNProtocols 是 ALPN 协议列表
	ALPNProtocols []string `json:"alpn_protocols"`
	// SupportedGroups 是 supported_groups 有序列表
	SupportedGroups []uint16 `json:"supported_groups"`
	// ECPointFormats 是 ec_point_formats 列表
	ECPointFormats []uint8 `json:"ec_point_formats"`
	// SupportedVersions 是 supported_versions 有序列表
	SupportedVersions []uint16 `json:"supported_versions"`
	// JA3 是 JA3 指纹计算结果
	JA3 tlspkg.JA3Result `json:"ja3"`
	// JA4 是 JA4 指纹计算结果
	JA4 tlspkg.JA4Result `json:"ja4"`
}

// CipherSuiteEntry 是单个密码套件的描述性条目。
type CipherSuiteEntry struct {
	Value    uint16 `json:"value"`
	ValueHex string `json:"value_hex"`
	IsGREASE bool   `json:"is_grease,omitempty"`
}

// ExtensionEntry 是单个扩展在输出模板中的简化表示。
type ExtensionEntry struct {
	Type     uint16 `json:"type"`
	TypeHex  string `json:"type_hex"`
	TypeName string `json:"type_name,omitempty"`
	IsGREASE bool   `json:"is_grease,omitempty"`
	DataLen  int    `json:"data_len"`
}

// Metadata 是 metadata.json 的内容结构。
type Metadata struct {
	// ToolVersion 是工具版本号
	ToolVersion string `json:"tool_version"`
	// CaptureStartTime 是开始捕获的 UTC 时间
	CaptureStartTime string `json:"capture_start_time"`
	// CaptureEndTime 是结束捕获的 UTC 时间
	CaptureEndTime string `json:"capture_end_time"`
	// TargetHost 是捕获目标主机名
	TargetHost string `json:"target_host"`
	// SampleCount 是捕获到的 ClientHello 样本数
	SampleCount int `json:"sample_count"`
	// MITMDetectionEnabled 是否启用了 MITM 检测
	MITMDetectionEnabled bool `json:"mitm_detection_enabled"`
	// MITMCheckResult MITM 检查结论（ok/warning/skipped）
	MITMCheckResult string `json:"mitm_check_result"`
	// Note 说明性备注
	Note string `json:"note,omitempty"`
	// OperatorInfo 操作员信息（仅在 -include-operator-info 时填充）
	OperatorInfo *OperatorInfo `json:"operator_info,omitempty"`
}

// OperatorInfo 是操作员主机信息，默认不收集，需显式 opt-in。
type OperatorInfo struct {
	Hostname string `json:"hostname,omitempty"`
}

// HTTP2Settings 是 http2-settings.json 的内容结构。
type HTTP2Settings struct {
	// Available 标记是否捕获到 HTTP/2 SETTINGS 数据
	Available bool `json:"available"`
	// LimitationNote 说明为何 v1 不支持此功能
	LimitationNote string `json:"limitation_note"`
	// Settings 如果通过其他机制捕获到数据，在此填充（v1 始终为 nil）
	Settings []HTTP2SettingEntry `json:"settings,omitempty"`
}

// HTTP2SettingEntry 是单个 HTTP/2 SETTINGS 参数。
type HTTP2SettingEntry struct {
	ID    uint16 `json:"id"`
	Name  string `json:"name"`
	Value uint32 `json:"value"`
}

// Writer 负责将所有输出文件写入到指定目录。
type Writer struct {
	// OutDir 是输出目录路径
	OutDir string
	// IncludeSNI 控制是否在模板中保留真实 SNI 值
	IncludeSNI bool
	// IncludeOperatorInfo 控制是否在 metadata.json 中包含主机名
	IncludeOperatorInfo bool
}

// NewWriter 创建一个新的 Writer 实例，确保输出目录存在。
func NewWriter(outDir string) (*Writer, error) {
	if err := os.MkdirAll(outDir, 0750); err != nil {
		return nil, fmt.Errorf("创建输出目录失败 %q: %w", outDir, err)
	}
	return &Writer{OutDir: outDir}, nil
}

// WriteClientHelloTemplate 将 ClientHello 模板写入 clienthello-template.json。
func (w *Writer) WriteClientHelloTemplate(ch *tlspkg.ClientHello, ja3 tlspkg.JA3Result, ja4 tlspkg.JA4Result, sampleCount int) error {
	tmpl := buildTemplate(ch, ja3, ja4, sampleCount, w.IncludeSNI)
	return w.writeJSON("clienthello-template.json", tmpl)
}

// WriteJA3 将 JA3 结果写入 ja3-hashes.txt。
func (w *Writer) WriteJA3(results []tlspkg.JA3Result) error {
	path := filepath.Join(w.OutDir, "ja3-hashes.txt")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建 ja3-hashes.txt 失败: %w", err)
	}
	defer f.Close()
	for i, r := range results {
		fmt.Fprintf(f, "# 样本 %d\n", i+1)
		fmt.Fprintf(f, "input: %s\n", r.InputString)
		fmt.Fprintf(f, "hash:  %s\n\n", r.Hash)
	}
	return nil
}

// WriteJA4 将 JA4 结果写入 ja4-hashes.txt。
func (w *Writer) WriteJA4(results []tlspkg.JA4Result) error {
	path := filepath.Join(w.OutDir, "ja4-hashes.txt")
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("创建 ja4-hashes.txt 失败: %w", err)
	}
	defer f.Close()
	for i, r := range results {
		fmt.Fprintf(f, "# 样本 %d\n", i+1)
		fmt.Fprintf(f, "%s\n\n", r.Hash)
	}
	return nil
}

// WriteHTTP2Settings 写入 http2-settings.json，v1 仅写入限制说明。
func (w *Writer) WriteHTTP2Settings() error {
	settings := HTTP2Settings{
		Available: false,
		LimitationNote: "HTTP/2 SETTINGS 帧在 TLS 记录层内部加密，无法通过被动 pcap 捕获。" +
			"如需捕获 HTTP/2 SETTINGS，建议使用以下方案之一：" +
			"(1) 由操作员自行配置 SSLKEYLOGFILE 环境变量后用 Wireshark 解密；" +
			"(2) 在本机运行带 TLS 终止的透明代理（需操作员完全控制）。" +
			"v1 不提供自动化支持；此字段为空是预期行为，不影响 JA3/JA4 指纹可用性。",
	}
	return w.writeJSON("http2-settings.json", settings)
}

// WriteMetadata 写入 metadata.json。
func (w *Writer) WriteMetadata(meta Metadata) error {
	return w.writeJSON("metadata.json", meta)
}

// writeJSON 将任意值序列化为缩进 JSON 并写入文件。
func (w *Writer) writeJSON(filename string, v interface{}) error {
	path := filepath.Join(w.OutDir, filename)
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("JSON 序列化 %q 失败: %w", filename, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0640); err != nil {
		return fmt.Errorf("写入文件 %q 失败: %w", path, err)
	}
	return nil
}

// buildTemplate 从已解析的 ClientHello 构建输出模板。
func buildTemplate(ch *tlspkg.ClientHello, ja3 tlspkg.JA3Result, ja4 tlspkg.JA4Result, sampleCount int, includeSNI bool) ClientHelloTemplate {
	// 构建密码套件列表（带 GREASE 标记）
	ciphers := make([]CipherSuiteEntry, len(ch.CipherSuites))
	for i, c := range ch.CipherSuites {
		ciphers[i] = CipherSuiteEntry{
			Value:    c,
			ValueHex: fmt.Sprintf("0x%04x", c),
			IsGREASE: tlspkg.IsGREASE(c),
		}
	}

	// 构建扩展列表（净化版：不含具体扩展内容）
	exts := make([]ExtensionEntry, len(ch.Extensions))
	for i, e := range ch.Extensions {
		exts[i] = ExtensionEntry{
			Type:     e.Type,
			TypeHex:  fmt.Sprintf("0x%04x", e.Type),
			TypeName: e.TypeName,
			IsGREASE: e.IsGREASEValue,
			DataLen:  e.DataLen,
		}
	}

	// SNI 处理
	sni := "<redacted>"
	if includeSNI {
		sni = ch.SNI()
	}

	return ClientHelloTemplate{
		SchemaVersion:      "1.0",
		CaptureTime:        time.Now().UTC().Format(time.RFC3339),
		SampleCount:        sampleCount,
		LegacyVersion:      ch.LegacyVersion,
		LegacyVersionHex:   fmt.Sprintf("0x%04x", ch.LegacyVersion),
		RandomLen:          ch.RandomLen,
		LegacySessionIDLen: ch.LegacySessionIDLen,
		CipherSuites:       ciphers,
		CompressionMethods: ch.CompressionMethods,
		Extensions:         exts,
		SNI:                sni,
		ALPNProtocols:      ch.ALPNProtocols(),
		SupportedGroups:    ch.SupportedGroups(),
		ECPointFormats:     ch.ECPointFormats(),
		SupportedVersions:  ch.SupportedVersions(),
		JA3:                ja3,
		JA4:                ja4,
	}
}
