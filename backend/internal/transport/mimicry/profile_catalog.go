package mimicry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
)

var (
	ErrSidecarUnavailable        = errors.New("mimicry sidecar: unavailable")
	ErrSidecarProfileUnavailable = errors.New("mimicry sidecar: profile unavailable")
)

var requiredSidecarCapabilities = []string{
	SidecarCapabilityBuiltinProfile,
	SidecarCapabilityInlineProfile,
	SidecarCapabilityHTTPProxy,
	SidecarCapabilityHTTPSProxy,
	SidecarCapabilitySOCKS5Proxy,
	SidecarCapabilityH2Bridge,
	SidecarCapabilityForceH1,
	SidecarCapabilityTargetIPPinning,
	SidecarCapabilityProxyIPPinning,
}

var requiredSidecarProfiles = []string{
	SidecarProfileAnthropicCLIMimicryV1,
	SidecarProfileOpenAICodexCLIV1,
	SidecarProfileGeminiCLIV1,
	SidecarProfileKiroCLIV1,
	SidecarProfileAntigravitySafeV1,
	SidecarProfileCursorSafeV1,
	SidecarProfileCopilotSafeV1,
	SidecarProfileWindsurfSafeV1,
	SidecarProfileOperatorSourceSafeV1,
}

// SidecarProfileForMode 返回每个 mimicry mode 的独立 Rust profile 合同。
// 精确采集项与运行时等价项使用不同 ID，运维面不会把等价项冒充成精确复刻。
func SidecarProfileForMode(mode TransportMode) (string, bool) {
	switch mode {
	case ModeMimicryClaudeCode:
		return SidecarProfileAnthropicCLIMimicryV1, true
	case ModeMimicryChatGPT:
		return SidecarProfileOpenAICodexCLIV1, true
	case ModeMimicryGeminiAdvanced:
		return SidecarProfileGeminiCLIV1, true
	case ModeMimicryAntigravity:
		return SidecarProfileAntigravitySafeV1, true
	case ModeMimicryCursor:
		return SidecarProfileCursorSafeV1, true
	case ModeMimicryCopilot:
		return SidecarProfileCopilotSafeV1, true
	case ModeMimicryKiro:
		return SidecarProfileKiroCLIV1, true
	case ModeMimicryWindsurf:
		return SidecarProfileWindsurfSafeV1, true
	default:
		return "", false
	}
}

func NewSidecarRoundTripperForMode(socketPath string, mode TransportMode) (http.RoundTripper, error) {
	return NewSidecarRoundTripperForModeForceH1(socketPath, mode, false)
}

func NewSidecarRoundTripperForModeForceH1(socketPath string, mode TransportMode, forceH1 bool) (http.RoundTripper, error) {
	profileID, ok := SidecarProfileForMode(mode)
	if !ok {
		return nil, fmt.Errorf("%w: no profile for mode %s", ErrSidecarProfileUnavailable, mode)
	}
	return NewSidecarRoundTripperForceH1(NewSidecarClient(socketPath), profileID, forceH1), nil
}

// ProbeSidecarForMode 核验 sidecar 的协议、能力和指定内置 profile，不连接上游。
func ProbeSidecarForMode(ctx context.Context, socketPath string, mode TransportMode) error {
	profileID, ok := SidecarProfileForMode(mode)
	if !ok {
		return fmt.Errorf("%w: no profile for mode %s", ErrSidecarProfileUnavailable, mode)
	}
	if socketPath == "" {
		return fmt.Errorf("%w: empty socket path", ErrSidecarUnavailable)
	}
	status, err := NewSidecarClient(socketPath).Inspect(ctx)
	if err != nil {
		return err
	}
	if err := requireSidecarCapabilities(status, SidecarCapabilityBuiltinProfile); err != nil {
		return err
	}
	if !containsString(status.ProfileIDs, profileID) {
		return fmt.Errorf("%w: profile %s 未加载", ErrSidecarProfileUnavailable, profileID)
	}
	if err := requireSidecarCapabilities(status, SidecarCapabilityForceH1); err != nil {
		return err
	}
	return nil
}

// ProbeSidecarReadiness 一次核验网关会使用的全部 IPC 能力和内置 profile。
// 它不连接任何上游，适用于启动门和 /readyz 持续探测。
func ProbeSidecarReadiness(ctx context.Context, socketPath string) error {
	if socketPath == "" {
		return fmt.Errorf("%w: empty socket path", ErrSidecarUnavailable)
	}
	status, err := NewSidecarClient(socketPath).Inspect(ctx)
	if err != nil {
		return err
	}
	if err := requireSidecarCapabilities(status, requiredSidecarCapabilities...); err != nil {
		return err
	}
	for _, profileID := range requiredSidecarProfiles {
		if !containsString(status.ProfileIDs, profileID) {
			return fmt.Errorf("%w: profile %s 未加载", ErrSidecarProfileUnavailable, profileID)
		}
	}
	return nil
}

func requireSidecarCapabilities(status *SidecarStatus, capabilities ...string) error {
	if status == nil {
		return fmt.Errorf("%w: empty status", ErrSidecarUnavailable)
	}
	for _, capability := range capabilities {
		if !containsString(status.Capabilities, capability) {
			return fmt.Errorf("%w: sidecar 缺少 %s capability", ErrSidecarUnavailable, capability)
		}
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
