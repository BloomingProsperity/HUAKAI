package mimicry

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// TransportMode 是 mimicry 子包内的 mode key，值与 transport.TransportMode 保持一致。
type TransportMode string

const (
	ModeMimicryClaudeCode     TransportMode = "mimicry_claude_code"
	ModeMimicryChatGPT        TransportMode = "mimicry_chatgpt"
	ModeMimicryGeminiAdvanced TransportMode = "mimicry_gemini_advanced"
	ModeMimicryAntigravity    TransportMode = "mimicry_antigravity"
	ModeMimicryCursor         TransportMode = "mimicry_cursor"
	ModeMimicryCopilot        TransportMode = "mimicry_copilot"
	ModeMimicryKiro           TransportMode = "mimicry_kiro"
	ModeMimicryWindsurf       TransportMode = "mimicry_windsurf"

	sidecarProbeProfileID = "__huakai_probe__"
)

// TemplateRegistry 保存每个 mimicry mode 对应的 ClientHello 模板。
type TemplateRegistry struct {
	templates map[TransportMode]*ClientHelloTemplate
}

func SidecarProfileForMode(mode TransportMode) (string, bool) {
	switch mode {
	case ModeMimicryClaudeCode:
		return SidecarProfileAnthropicCLIMimicryV1, true
	default:
		return "", false
	}
}

func NewSidecarRoundTripperForMode(socketPath string, mode TransportMode) (http.RoundTripper, error) {
	profileID, ok := SidecarProfileForMode(mode)
	if !ok {
		return nil, fmt.Errorf("mimicry: no sidecar profile for mode %s", mode)
	}
	return NewSidecarRoundTripper(NewSidecarClient(socketPath), profileID), nil
}

func ProbeSidecarForMode(ctx context.Context, socketPath string, mode TransportMode) error {
	if _, ok := SidecarProfileForMode(mode); !ok {
		return fmt.Errorf("mimicry: no sidecar profile for mode %s", mode)
	}
	if socketPath == "" {
		return fmt.Errorf("mimicry sidecar: empty socket path")
	}
	conn, err := sidecarDialContext(ctx, "unix", socketPath)
	if err != nil {
		return fmt.Errorf("mimicry sidecar: dial unix socket %s: %w", socketPath, err)
	}
	defer conn.Close()
	if err := setDeadlineFromContext(conn, ctx); err != nil {
		return err
	}
	req := sidecarControlRequest{
		TargetHost: sidecarProbeProfileID,
		Port:       1,
		ProfileID:  sidecarProbeProfileID,
	}
	if err := writeSidecarFrame(conn, req); err != nil {
		return fmt.Errorf("mimicry sidecar: write probe frame: %w", err)
	}
	var ack sidecarControlAck
	if err := readSidecarFrame(conn, &ack); err != nil {
		return fmt.Errorf("mimicry sidecar: read probe ack frame: %w", err)
	}
	return nil
}

// NewTemplateRegistry 返回空 registry。
func NewTemplateRegistry() *TemplateRegistry {
	return &TemplateRegistry{templates: make(map[TransportMode]*ClientHelloTemplate)}
}

func NewDefaultTemplateRegistry() *TemplateRegistry {
	r := NewTemplateRegistry()
	if err := r.Register(ModeMimicryClaudeCode, AnthropicCLIMimicryV1Template()); err != nil {
		panic(err)
	}
	return r
}

// Register 注册单个 mode 模板；重复注册会 fail-loud，避免目录里双写覆盖。
func (r *TemplateRegistry) Register(mode TransportMode, tmpl *ClientHelloTemplate) error {
	if r == nil {
		return fmt.Errorf("mimicry: nil template registry")
	}
	if mode == "" {
		return fmt.Errorf("mimicry: empty transport mode")
	}
	if tmpl == nil {
		return fmt.Errorf("mimicry: nil template for mode %s", mode)
	}
	if err := tmpl.Validate(); err != nil {
		return fmt.Errorf("mimicry: validate template for mode %s: %w", mode, err)
	}
	if r.templates == nil {
		r.templates = make(map[TransportMode]*ClientHelloTemplate)
	}
	if _, ok := r.templates[mode]; ok {
		return fmt.Errorf("mimicry: duplicate template for mode %s", mode)
	}
	r.templates[mode] = tmpl
	return nil
}

// Lookup 查询 mode 模板。
func (r *TemplateRegistry) Lookup(mode TransportMode) (*ClientHelloTemplate, bool) {
	if r == nil || r.templates == nil {
		return nil, false
	}
	tmpl, ok := r.templates[mode]
	return tmpl, ok
}

// LoadFromDirectory 递归扫描目录内全部 *.json，并按文件名或 mode_name 注册模板。
//
// 子目录命名以下划线开头的 (如 _pending-backfill/) 会被整体跳过, 跟 Go 工具链
// 对 _* 目录的默认忽略一致。R-D capture artifact 走 _pending-backfill/ 路径
// 等 admin 一键 promote 后才进 builtin 池, 不参与 runtime 加载, 避免出现
// mode_name 跟 builtin 撞 (如 codex-cli.json + openai_codex-real-*.json
// 都 register mimicry_chatgpt).
func (r *TemplateRegistry) LoadFromDirectory(dir string) error {
	loaded := 0
	if err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// 根目录本身不跳 (即使以 _ 开头也允许); 仅跳子目录
			if path != dir && strings.HasPrefix(info.Name(), "_") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(info.Name()), ".json") {
			return nil
		}
		tmpl, err := LoadFromCollectorOutput(path)
		if err != nil {
			return fmt.Errorf("mimicry: load template %s: %w", path, err)
		}
		mode, ok := modeFromStem(strings.TrimSuffix(info.Name(), filepath.Ext(info.Name())))
		if !ok {
			mode, ok = modeFromName(tmpl.ModeName)
		}
		if !ok {
			return fmt.Errorf("mimicry: unknown template mode for %s", path)
		}
		if tmpl.IsStub() {
			slog.Warn("mimicry template placeholder", "mode", mode, "reason_class", "template_placeholder")
		}
		if err := r.Register(mode, tmpl); err != nil {
			return err
		}
		loaded++
		return nil
	}); err != nil {
		return err
	}
	if loaded == 0 {
		return fmt.Errorf("mimicry: no template json loaded from %s", dir)
	}
	return nil
}

// Modes 返回已注册 mode，排序后便于测试和 admin 展示。
func (r *TemplateRegistry) Modes() []TransportMode {
	if r == nil || r.templates == nil {
		return nil
	}
	out := make([]TransportMode, 0, len(r.templates))
	for mode := range r.templates {
		out = append(out, mode)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func modeFromStem(stem string) (TransportMode, bool) {
	return modeFromKey(strings.ReplaceAll(stem, "_", "-"))
}

func modeFromName(name string) (TransportMode, bool) {
	return modeFromKey(strings.ReplaceAll(name, "_", "-"))
}

func modeFromKey(key string) (TransportMode, bool) {
	switch strings.ToLower(key) {
	case "anthropic-cli-mimicry-v1", "anthropic-claude-code", "claude-code":
		return ModeMimicryClaudeCode, true
	case "chatgpt", "chatgpt-web", "codex-cli", "openai-codex", "openai-codex-cli":
		return ModeMimicryChatGPT, true
	case "gemini-advanced":
		return ModeMimicryGeminiAdvanced, true
	case "antigravity":
		return ModeMimicryAntigravity, true
	case "cursor", "cursor-cli":
		return ModeMimicryCursor, true
	case "copilot", "github-copilot":
		return ModeMimicryCopilot, true
	case "kiro", "kiro-cli":
		return ModeMimicryKiro, true
	case "windsurf", "windsurf-cli":
		return ModeMimicryWindsurf, true
	}
	return "", false
}

type collectorOutput struct {
	ModeName     string `json:"mode_name"`
	CollectedAt  string `json:"collected_at"`
	CaptureTime  string `json:"capture_time"`
	TargetHost   string `json:"target_host"`
	TLSBackend   string `json:"tls_backend"`
	CipherSuites []struct {
		Value uint16 `json:"value"`
	} `json:"cipher_suites"`
	Extensions []struct {
		Type                uint16   `json:"type"`
		DataLen             int      `json:"data_len"`
		SignatureAlgorithms []uint16 `json:"signature_algorithms"`
		KeyShareGroups      []uint16 `json:"key_share_groups"`
		PSKModes            []uint8  `json:"psk_modes"`
	} `json:"extensions"`
	ALPNProtocols     []string `json:"alpn_protocols"`
	SupportedGroups   []uint16 `json:"supported_groups"`
	ECPointFormats    []uint8  `json:"ec_point_formats"`
	SupportedVersions []uint16 `json:"supported_versions"`
	KeyShareGroups    []uint16 `json:"key_share_groups"`
	PSKModes          []uint8  `json:"psk_modes"`
	JA3               struct {
		InputString string `json:"input_string"`
	} `json:"ja3"`
	JA4 struct {
		Raw  string `json:"raw"`
		Hash string `json:"hash"`
	} `json:"ja4"`
}

func (r collectorOutput) cipherIDs() []uint16 {
	out := make([]uint16, len(r.CipherSuites))
	for i, c := range r.CipherSuites {
		out[i] = c.Value
	}
	return out
}

func (r collectorOutput) extensionIDs() []uint16 {
	out := make([]uint16, len(r.Extensions))
	for i, e := range r.Extensions {
		out[i] = e.Type
	}
	return out
}

func (r collectorOutput) paddingLen() int {
	for _, e := range r.Extensions {
		if e.Type == 21 {
			return e.DataLen
		}
	}
	return 0
}

func (r collectorOutput) signatureAlgorithms() []uint16 {
	for _, e := range r.Extensions {
		if e.Type == 13 && len(e.SignatureAlgorithms) > 0 {
			return append([]uint16(nil), e.SignatureAlgorithms...)
		}
		if e.Type == 13 && e.DataLen == 20 {
			return anthropicSigAlgos()
		}
	}
	return nil
}

func (r collectorOutput) keyShareGroups() []uint16 {
	if len(r.KeyShareGroups) > 0 {
		return append([]uint16(nil), r.KeyShareGroups...)
	}
	for _, e := range r.Extensions {
		if e.Type == 51 && len(e.KeyShareGroups) > 0 {
			return append([]uint16(nil), e.KeyShareGroups...)
		}
	}
	return []uint16{29}
}

func (r collectorOutput) pskModes() []uint8 {
	if len(r.PSKModes) > 0 {
		return append([]uint8(nil), r.PSKModes...)
	}
	for _, e := range r.Extensions {
		if e.Type == 45 && len(e.PSKModes) > 0 {
			return append([]uint8(nil), e.PSKModes...)
		}
	}
	return []uint8{1}
}
