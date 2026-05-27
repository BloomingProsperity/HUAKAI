// Package credentialacq owns F-CRED-001 credential acquisition flow state.
package credentialacq

import (
	"errors"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

type FlowKind string

const (
	FlowKindOAuth          FlowKind = "oauth"
	FlowKindCLIImport      FlowKind = "cli_import"
	FlowKindPaste          FlowKind = "paste"
	FlowKindCSVImport      FlowKind = "csv_import"
	FlowKindJSONImport     FlowKind = "json_import"
	FlowKindCloudBootstrap FlowKind = "cloud_bootstrap"
	FlowKindTokenExchange  FlowKind = "token_exchange"
	FlowKindSetupToken     FlowKind = "setup_token"
	FlowKindManualFirst    FlowKind = "manual_first"
)

type FlowStatus string

const (
	StatusStarted          FlowStatus = "started"
	StatusWaitingForUser   FlowStatus = "waiting_for_user"
	StatusCallbackReceived FlowStatus = "callback_received"
	StatusValidated        FlowStatus = "validated"
	StatusFinalized        FlowStatus = "finalized"
	StatusCancelled        FlowStatus = "cancelled"
	StatusExpired          FlowStatus = "expired"
	StatusFailed           FlowStatus = "failed"
)

type AuthType string

const (
	AuthTypePKCE       AuthType = "pkce"
	AuthTypeDeviceCode AuthType = "device_code"
	AuthTypeSSO        AuthType = "sso"
)

const (
	ClientSourceNone                  = "none"
	ClientSourcePublicCLI             = "public_cli_client"
	ClientSourceOperatorConfig        = "operator_config"
	ClientSourcePerAccountOverride    = "per_account_override"
	ClientSourceDisabledMissingConfig = "disabled_missing_config"
)

var (
	ErrFlowNotFound          = errors.New("credentialacq: flow not found")
	ErrFlowExpired           = errors.New("credentialacq: flow expired")
	ErrFlowReplay            = errors.New("credentialacq: flow replay")
	ErrStateMismatch         = errors.New("credentialacq: oauth state mismatch")
	ErrUnknownMode           = errors.New("credentialacq: unknown mode")
	ErrInvalidImportBody     = errors.New("credentialacq: invalid import body")
	ErrFeatureDisabled       = errors.New("credentialacq: feature disabled")
	ErrSecretInContext       = errors.New("credentialacq: redacted context contains secret-shaped material")
	ErrInvalidTokenShape     = errors.New("credentialacq: invalid token shape")
	ErrResponseTooLarge      = errors.New("credentialacq: response too large")
	ErrOAuthExchangerMissing = errors.New("credentialacq: oauth exchanger missing")
)

type ModePlan struct {
	Vendor               string     `json:"vendor"`
	AuthMode             string     `json:"auth_mode"`
	Kind                 FlowKind   `json:"flow_kind"`
	ClientIdentitySource string     `json:"client_identity_source"`
	ManualFirst          bool       `json:"manual_first,omitempty"`
	LongLivedToggle      bool       `json:"long_lived_toggle,omitempty"`
	AllowedHelpers       []FlowKind `json:"allowed_helpers,omitempty"`
}

type StartInput struct {
	// ID 仅供需要在 authorize URL 里预置 flow_id 的 OAuth 启动路径使用；普通 caller 留空由 store 生成。
	ID                    string
	TenantID              int64
	ProviderAccountID     int64
	Vendor                string
	AuthMode              string
	Kind                  FlowKind
	ActorID               string
	ActorRole             string
	StateHash             []byte
	NonceHash             []byte
	EncryptedPKCEVerifier []byte
	ClientIdentitySource  string
	RedirectURI           string
	RequestedScopes       []string
	RedactedContext       map[string]any
	LongLivedRequested    bool
	IdempotencyKey        string
	ExpiresAt             time.Time
}

type CallbackInput struct {
	FlowID string
	State  string
	Code   string
}

type CredentialCandidate struct {
	TenantID          int64
	ProviderAccountID int64
	Vendor            string
	AuthMode          string
	Payload           []byte
	ActorID           string
	RedactedContext   map[string]any
}

type Session struct {
	ID                        string         `json:"id"`
	TenantID                  int64          `json:"tenant_id"`
	ProviderAccountID         int64          `json:"provider_account_id"`
	Vendor                    string         `json:"vendor"`
	AuthMode                  string         `json:"auth_mode"`
	Kind                      FlowKind       `json:"flow_kind"`
	Status                    FlowStatus     `json:"status"`
	ActorID                   string         `json:"actor_id"`
	ActorRole                 string         `json:"actor_role"`
	StateHash                 []byte         `json:"-"`
	NonceHash                 []byte         `json:"-"`
	EncryptedPKCEVerifier     []byte         `json:"-"`
	ClientIdentitySource      string         `json:"client_identity_source"`
	AuthType                  AuthType       `json:"auth_type,omitempty"`
	RedirectURI               string         `json:"redirect_uri,omitempty"`
	RequestedScopes           []string       `json:"requested_scopes,omitempty"`
	RedactedContext           map[string]any `json:"redacted_context"`
	DeviceCodePayload         map[string]any `json:"-"`
	LongLivedRequested        bool           `json:"long_lived_requested"`
	IdempotencyKeyHash        []byte         `json:"-"`
	ResultAccountCredentialID int64          `json:"result_account_credential_id,omitempty"`
	ErrorClass                string         `json:"error_class,omitempty"`
	ErrorMessageRedacted      string         `json:"error_message_redacted,omitempty"`
	ExpiresAt                 time.Time      `json:"expires_at"`
	ConsumedAt                time.Time      `json:"consumed_at,omitempty"`
	CancelledAt               time.Time      `json:"cancelled_at,omitempty"`
	CreatedAt                 time.Time      `json:"created_at"`
	UpdatedAt                 time.Time      `json:"updated_at"`
}

type FinalizeResult struct {
	Session          Session                            `json:"flow"`
	Credential       credentialstore.CredentialMetadata `json:"credential"`
	AlreadyFinalized bool                               `json:"already_finalized"`
}

func DefaultModePlans() []ModePlan {
	return []ModePlan{
		{Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeAPIKey, Kind: FlowKindPaste, ClientIdentitySource: ClientSourceNone, AllowedHelpers: []FlowKind{FlowKindPaste, FlowKindCSVImport, FlowKindJSONImport}},
		{Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeAIOAuth, Kind: FlowKindOAuth, ClientIdentitySource: ClientSourcePublicCLI, AllowedHelpers: []FlowKind{FlowKindOAuth, FlowKindPaste}},
		{Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeCode, Kind: FlowKindCLIImport, ClientIdentitySource: ClientSourcePublicCLI, AllowedHelpers: []FlowKind{FlowKindCLIImport, FlowKindJSONImport}},
		{Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeBedrock, Kind: FlowKindPaste, ClientIdentitySource: ClientSourceNone, ManualFirst: true, AllowedHelpers: []FlowKind{FlowKindPaste, FlowKindCloudBootstrap, FlowKindOAuth}},
		{Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeVertexAnthropic, Kind: FlowKindJSONImport, ClientIdentitySource: ClientSourceOperatorConfig, AllowedHelpers: []FlowKind{FlowKindJSONImport, FlowKindCloudBootstrap}},
		{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAPIKey, Kind: FlowKindPaste, ClientIdentitySource: ClientSourceNone, AllowedHelpers: []FlowKind{FlowKindPaste, FlowKindCSVImport, FlowKindJSONImport}},
		{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeChatGPTOAuth, Kind: FlowKindOAuth, ClientIdentitySource: ClientSourcePublicCLI, AllowedHelpers: []FlowKind{FlowKindOAuth}},
		{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeCodexCLIOAuth, Kind: FlowKindCLIImport, ClientIdentitySource: ClientSourcePublicCLI, AllowedHelpers: []FlowKind{FlowKindCLIImport, FlowKindJSONImport, FlowKindOAuth}},
		{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAzure, Kind: FlowKindPaste, ClientIdentitySource: ClientSourceOperatorConfig, ManualFirst: true, AllowedHelpers: []FlowKind{FlowKindPaste, FlowKindCloudBootstrap, FlowKindTokenExchange}},
		{Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeRefreshToken, Kind: FlowKindTokenExchange, ClientIdentitySource: ClientSourcePerAccountOverride, AllowedHelpers: []FlowKind{FlowKindTokenExchange, FlowKindPaste}},
		{Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeAIStudioAPIKey, Kind: FlowKindPaste, ClientIdentitySource: ClientSourceNone, AllowedHelpers: []FlowKind{FlowKindPaste, FlowKindCSVImport, FlowKindJSONImport}},
		{Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeVertexSA, Kind: FlowKindJSONImport, ClientIdentitySource: ClientSourceOperatorConfig, AllowedHelpers: []FlowKind{FlowKindJSONImport, FlowKindCloudBootstrap}},
		{Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeCodeAssist, Kind: FlowKindOAuth, ClientIdentitySource: ClientSourcePublicCLI, AllowedHelpers: []FlowKind{FlowKindOAuth}},
		{Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeGoogleOne, Kind: FlowKindOAuth, ClientIdentitySource: ClientSourcePublicCLI, AllowedHelpers: []FlowKind{FlowKindOAuth}},
		{Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeAntigravity, Kind: FlowKindOAuth, ClientIdentitySource: ClientSourcePublicCLI, AllowedHelpers: []FlowKind{FlowKindOAuth, FlowKindTokenExchange}, ManualFirst: true},
		{Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeOAuth, Kind: FlowKindOAuth, ClientIdentitySource: ClientSourceOperatorConfig, AllowedHelpers: []FlowKind{FlowKindOAuth, FlowKindTokenExchange}, ManualFirst: true},
		{Vendor: credentialstore.VendorCopilot, AuthMode: credentialstore.AuthModeCopilotOAuth, Kind: FlowKindOAuth, ClientIdentitySource: ClientSourcePublicCLI, AllowedHelpers: []FlowKind{FlowKindOAuth, FlowKindJSONImport}},
		{Vendor: credentialstore.VendorAntigravity, AuthMode: credentialstore.AuthModeOAuth, Kind: FlowKindOAuth, ClientIdentitySource: ClientSourceOperatorConfig, AllowedHelpers: []FlowKind{FlowKindOAuth, FlowKindTokenExchange}, ManualFirst: true},
		{Vendor: credentialstore.VendorWindsurf, AuthMode: credentialstore.AuthModeOAuth, Kind: FlowKindTokenExchange, ClientIdentitySource: ClientSourceOperatorConfig, AllowedHelpers: []FlowKind{FlowKindTokenExchange, FlowKindPaste}, ManualFirst: true},
	}
}

func LookupModePlan(vendor, authMode string) (ModePlan, bool) {
	key := credentialstore.ModeKey(vendor, authMode)
	for _, plan := range DefaultModePlans() {
		if credentialstore.ModeKey(plan.Vendor, plan.AuthMode) == key {
			return plan, true
		}
	}
	return ModePlan{}, false
}

func NormalizeFlowKind(kind FlowKind) FlowKind {
	switch kind {
	case FlowKindOAuth, FlowKindCLIImport, FlowKindPaste, FlowKindCSVImport, FlowKindJSONImport, FlowKindCloudBootstrap, FlowKindTokenExchange, FlowKindSetupToken, FlowKindManualFirst:
		return kind
	default:
		return ""
	}
}

// flowKindAllowed 判 candidate 是否落在 ModePlan.AllowedHelpers 白名单内。
// 用于 P0 闸门 — OAuth-only 模式 (chatgpt_oauth / code_assist / google_one
// 等 AllowedHelpers 仅含 FlowKindOAuth) 必须拒绝 paste / cli-import / json-import
// 之类的手工绕过路径。Owner 2026-05-26 抓出, 详见 session_store.go CreateFromStart。
func flowKindAllowed(allowed []FlowKind, candidate FlowKind) bool {
	candidate = NormalizeFlowKind(candidate)
	for _, k := range allowed {
		if NormalizeFlowKind(k) == candidate {
			return true
		}
	}
	return false
}

func NormalizeFlowStatus(status FlowStatus) FlowStatus {
	switch status {
	case StatusStarted, StatusWaitingForUser, StatusCallbackReceived, StatusValidated, StatusFinalized, StatusCancelled, StatusExpired, StatusFailed:
		return status
	default:
		return ""
	}
}
