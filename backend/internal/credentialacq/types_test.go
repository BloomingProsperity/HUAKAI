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
		{Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeBedrock, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone, ManualFirst: true},
		{Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeVertexAnthropic, Kind: flowKindJSONImport, ClientIdentitySource: clientSourceOperatorConfig},
		{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth, Kind: flowKindOAuth, ClientIdentitySource: clientSourcePublicCLI},
		{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth, Kind: flowKindCLIImport, ClientIdentitySource: clientSourcePublicCLI},
		{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAzure, Kind: flowKindPaste, ClientIdentitySource: clientSourceOperatorConfig, ManualFirst: true},
		{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeRefreshToken, Kind: flowKindTokenExchange, ClientIdentitySource: clientSourcePerAccountOverride},
		{Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeAIStudioAPIKey, Kind: flowKindPaste, ClientIdentitySource: clientSourceNone},
		{Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeVertexSA, Kind: flowKindJSONImport, ClientIdentitySource: clientSourceOperatorConfig},
		{Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeCodeAssist, Kind: flowKindOAuth, ClientIdentitySource: clientSourcePublicCLI},
		{Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeGoogleOne, Kind: flowKindOAuth, ClientIdentitySource: clientSourcePublicCLI},
		{Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeAntigravity, Kind: flowKindOAuth, ClientIdentitySource: clientSourcePublicCLI},
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

func TestModePlanCoversFifteenCredentialStoreModes(t *testing.T) {
	registry := credentialstore.DefaultHandlerRegistry()
	plans := phaseAModePlans()
	if len(plans) != 15 {
		t.Fatalf("mode plan count=%d want 15", len(plans))
	}
	seen := map[string]bool{}
	for _, plan := range plans {
		key := credentialstore.ModeKey(plan.Vendor, plan.AuthMode)
		if seen[key] {
			t.Fatalf("duplicate mode plan %s", key)
		}
		seen[key] = true
		if _, err := registry.MustLookup(plan.Vendor, plan.AuthMode); err != nil {
			t.Fatalf("mode plan %s is not accepted by F-AUTH-005 registry: %v", key, err)
		}
		if plan.ClientIdentitySource == "" {
			t.Fatalf("mode plan %s has empty client identity source", key)
		}
	}
	for _, key := range registry.Names() {
		if !seen[key] {
			t.Fatalf("F-AUTH-005 registry mode %s missing from Phase A plan", key)
		}
	}
}
