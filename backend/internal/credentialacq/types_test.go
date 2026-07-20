package credentialacq

import (
	"errors"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

type acqFlowKind string

const (
	flowKindOAuth          acqFlowKind = "oauth"
	flowKindCLIImport      acqFlowKind = "cli_import"
	flowKindPaste          acqFlowKind = "paste"
	flowKindCSVImport      acqFlowKind = "csv_import"
	flowKindJSONImport     acqFlowKind = "json_import"
	flowKindCloudBootstrap acqFlowKind = "cloud_bootstrap"
	flowKindTokenExchange  acqFlowKind = "token_exchange"
	flowKindSetupToken     acqFlowKind = "setup_token"
	flowKindManualFirst    acqFlowKind = "manual_first"
)

type acqFlowStatus string

const (
	statusStarted          acqFlowStatus = "started"
	statusWaitingForUser   acqFlowStatus = "waiting_for_user"
	statusCallbackReceived acqFlowStatus = "callback_received"
	statusValidated        acqFlowStatus = "validated"
	statusFinalized        acqFlowStatus = "finalized"
	statusCancelled        acqFlowStatus = "cancelled"
	statusExpired          acqFlowStatus = "expired"
	statusFailed           acqFlowStatus = "failed"
)

const (
	clientSourceNone                = "none"
	clientSourcePublicCLI           = "public_cli_client"
	clientSourceOperatorConfig      = "operator_config"
	clientSourcePerAccountOverride  = "per_account_override"
	clientSourceDisabledMissingConf = "disabled_missing_config"
)

var (
	errFlowNotFound      = errors.New("acquisition flow not found")
	errFlowExpired       = errors.New("acquisition flow expired")
	errFlowReplay        = errors.New("acquisition flow replay")
	errStateMismatch     = errors.New("oauth state mismatch")
	errUnknownMode       = errors.New("unknown acquisition mode")
	errInvalidImportBody = errors.New("invalid import body")
)

type acqModePlan struct {
	Vendor               string
	AuthMode             string
	Kind                 acqFlowKind
	ClientIdentitySource string
	ManualFirst          bool
	LongLivedToggle      bool
}

type acqCandidate struct {
	TenantID          int64
	ProviderAccountID int64
	Vendor            string
	AuthMode          string
	Payload           []byte
	ActorID           string
	RedactedContext   map[string]any
}

func phaseAModePlans() []acqModePlan {
	return []acqModePlan{
		{Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth, Kind: flowKindOAuth, ClientIdentitySource: clientSourcePublicCLI},
		{Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeCode, Kind: flowKindCLIImport, ClientIdentitySource: clientSourcePublicCLI},
		{Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeSetupToken, Kind: flowKindSetupToken, ClientIdentitySource: clientSourcePublicCLI},
		{Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeBedrock, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone, ManualFirst: true},
		{Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeVertexAnthropic, Kind: flowKindJSONImport, ClientIdentitySource: clientSourceOperatorConfig},
		{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth, Kind: flowKindOAuth, ClientIdentitySource: clientSourcePublicCLI},
		{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth, Kind: flowKindCLIImport, ClientIdentitySource: clientSourcePublicCLI},
		{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexWebOAuth, Kind: flowKindOAuth, ClientIdentitySource: clientSourcePublicCLI},
		{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAzure, Kind: flowKindPaste, ClientIdentitySource: clientSourceOperatorConfig, ManualFirst: true},
		{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeRefreshToken, Kind: flowKindTokenExchange, ClientIdentitySource: clientSourcePerAccountOverride},
		{Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeAIStudioAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeVertexSA, Kind: flowKindJSONImport, ClientIdentitySource: clientSourceOperatorConfig},
		{Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeCodeAssist, Kind: flowKindOAuth, ClientIdentitySource: clientSourcePublicCLI},
		{Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeGoogleOne, Kind: flowKindOAuth, ClientIdentitySource: clientSourcePublicCLI},
		{Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeAntigravity, Kind: flowKindOAuth, ClientIdentitySource: clientSourcePublicCLI, ManualFirst: true},
		{Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeOAuth, Kind: flowKindOAuth, ClientIdentitySource: clientSourceOperatorConfig, ManualFirst: true},
		{Vendor: credentialstore.VendorCopilot, AuthMode: credentialstore.AuthModeCopilotOAuth, Kind: flowKindOAuth, ClientIdentitySource: clientSourcePublicCLI},
		{Vendor: credentialstore.VendorAntigravity, AuthMode: credentialstore.AuthModeOAuth, Kind: flowKindOAuth, ClientIdentitySource: clientSourcePublicCLI, ManualFirst: true},
		{Vendor: credentialstore.VendorWindsurf, AuthMode: credentialstore.AuthModeOAuth, Kind: flowKindTokenExchange, ClientIdentitySource: clientSourceOperatorConfig, ManualFirst: true},
		{Vendor: credentialstore.VendorKimi, AuthMode: credentialstore.AuthModeKimiOAuth, Kind: flowKindOAuth, ClientIdentitySource: clientSourcePublicCLI},
		{Vendor: credentialstore.VendorOpenRouter, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorDeepSeek, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorGrok, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorGrok, AuthMode: credentialstore.AuthModeXAIOAuth, Kind: flowKindOAuth, ClientIdentitySource: clientSourceOperatorConfig},
		// 官 key 厂商(2026-07-02 接入,迁移 0169 放行存储):kimi + 国内大厂,纯 api_key 粘贴形状。
		{Vendor: credentialstore.VendorKimi, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorQwen, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorGLM, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorYi, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorBaichuan, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorDoubao, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorMiniMax, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorErnie, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorHunyuan, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorStep, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorMistral, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorGroqCloud, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorTogether, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorPerplexity, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorFireworks, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
	}
}

func TestFlowKindContract(t *testing.T) {
	want := map[acqFlowKind]bool{
		flowKindOAuth: true, flowKindCLIImport: true, flowKindPaste: true,
		flowKindCSVImport: true, flowKindJSONImport: true, flowKindCloudBootstrap: true,
		flowKindTokenExchange: true, flowKindSetupToken: true, flowKindManualFirst: true,
	}
	for _, plan := range phaseAModePlans() {
		if !want[plan.Kind] {
			t.Fatalf("mode %s/%s uses unknown flow kind %q", plan.Vendor, plan.AuthMode, plan.Kind)
		}
	}
	if len(want) != 9 {
		t.Fatalf("flow kind count=%d want 9", len(want))
	}
}

func TestFlowStatusTransitionContract(t *testing.T) {
	allowed := map[acqFlowStatus][]acqFlowStatus{
		statusStarted:          {statusWaitingForUser, statusValidated, statusCancelled, statusExpired, statusFailed},
		statusWaitingForUser:   {statusCallbackReceived, statusCancelled, statusExpired, statusFailed},
		statusCallbackReceived: {statusValidated, statusFailed},
		statusValidated:        {statusFinalized, statusFailed, statusExpired},
		statusFinalized:        nil,
		statusCancelled:        nil,
		statusExpired:          nil,
		statusFailed:           nil,
	}
	if _, ok := allowed[statusFinalized]; !ok {
		t.Fatal("finalized status missing from transition table")
	}
	for terminal, next := range map[acqFlowStatus][]acqFlowStatus{
		statusFinalized: allowed[statusFinalized],
		statusCancelled: allowed[statusCancelled],
		statusExpired:   allowed[statusExpired],
		statusFailed:    allowed[statusFailed],
	} {
		if len(next) != 0 {
			t.Fatalf("terminal status %q has outgoing transitions: %v", terminal, next)
		}
	}
}

func TestModePlanCoversCredentialStoreModesExceptDedicatedIntake(t *testing.T) {
	registry := credentialstore.DefaultHandlerRegistry()
	plans := phaseAModePlans()
	registryModes := map[string]bool{}
	for _, key := range registry.Names() {
		registryModes[key] = true
	}
	seen := map[string]bool{}
	for _, plan := range plans {
		key := credentialstore.ModeKey(plan.Vendor, plan.AuthMode)
		if seen[key] {
			t.Fatalf("duplicate mode plan %s", key)
		}
		seen[key] = true
		if registryModes[key] {
			if _, err := registry.MustLookup(plan.Vendor, plan.AuthMode); err != nil {
				t.Fatalf("mode plan %s is not accepted by F-AUTH-005 registry: %v", key, err)
			}
		}
		if plan.ClientIdentitySource == "" {
			t.Fatalf("mode plan %s has empty client identity source", key)
		}
	}
	for _, key := range registry.Names() {
		if !seen[key] {
			if key == credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexAgent) {
				continue
			}
			t.Fatalf("F-AUTH-005 registry mode %s missing from Phase A plan", key)
		}
	}
}

func TestCodexAgentIdentityCannotUsePlatformGenericAcquisition(t *testing.T) {
	if _, ok := LookupModePlan(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexAgent); ok {
		t.Fatal("Codex Agent Identity 不得进入部署管理员通用凭据获取入口")
	}
}

func TestXAIOAuthModePlan(t *testing.T) {
	// 变异:移除 grok/xai_oauth 的 ModePlan 播种,或把它暴露为 paste
	// helper,本测试就必须变红。
	plan, ok := LookupModePlan(credentialstore.VendorGrok, credentialstore.AuthModeXAIOAuth)
	if !ok {
		t.Fatal("DefaultModePlans missing grok/xai_oauth")
	}
	if plan.Kind != FlowKindOAuth {
		t.Fatalf("kind=%s want %s", plan.Kind, FlowKindOAuth)
	}
	if plan.ClientIdentitySource != ClientSourceOperatorConfig {
		t.Fatalf("client source=%q want %q", plan.ClientIdentitySource, ClientSourceOperatorConfig)
	}
	if len(plan.AllowedHelpers) != 1 || plan.AllowedHelpers[0] != FlowKindOAuth {
		t.Fatalf("allowed helpers=%v want [oauth]", plan.AllowedHelpers)
	}
	if !plan.IsEnabled {
		t.Fatal("grok/xai_oauth should be enabled")
	}
}

func TestClaudeSetupTokenModePlanIsDedicated(t *testing.T) {
	plan, ok := LookupModePlan(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeSetupToken)
	if !ok {
		t.Fatal("DefaultModePlans 缺少 anthropic/claude_setup_token")
	}
	if plan.Kind != FlowKindSetupToken || plan.LongLivedToggle || len(plan.AllowedHelpers) != 1 || plan.AllowedHelpers[0] != FlowKindSetupToken {
		t.Fatalf("setup token plan=%+v", plan)
	}
	if len(plan.RequiredFields) != 1 || plan.RequiredFields[0].Name != "setup_token" || plan.RequiredFields[0].Redaction != RedactionSecret {
		t.Fatalf("setup token fields=%+v", plan.RequiredFields)
	}
}

func TestDefaultModePlansPreserveManualFirstContract(t *testing.T) {
	for _, expected := range phaseAModePlans() {
		plan, ok := LookupModePlan(expected.Vendor, expected.AuthMode)
		if !ok {
			t.Fatalf("DefaultModePlans missing %s/%s", expected.Vendor, expected.AuthMode)
		}
		if plan.ManualFirst != expected.ManualFirst {
			t.Fatalf("%s/%s manual_first=%v want %v", expected.Vendor, expected.AuthMode, plan.ManualFirst, expected.ManualFirst)
		}
	}
}
