// Package credentialacq 负责 F-CRED-001 凭据获取流程的状态。
package credentialacq

import (
	"errors"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/subscriptionprofile"
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

type FieldKind string

const (
	FieldKindSecret     FieldKind = "secret"
	FieldKindString     FieldKind = "string"
	FieldKindURL        FieldKind = "url"
	FieldKindSelect     FieldKind = "select"
	FieldKindTextarea   FieldKind = "textarea"
	FieldKindJSONObject FieldKind = "json_object"
	FieldKindBoolean    FieldKind = "boolean"
)

type FieldRedaction string

const (
	RedactionSecret FieldRedaction = "secret"
	RedactionNone   FieldRedaction = "none"
)

type FieldGroup string

const (
	FieldGroupCredential  FieldGroup = "credential"
	FieldGroupOAuthClient FieldGroup = "oauth_client"
	FieldGroupCloud       FieldGroup = "cloud"
	FieldGroupAdvanced    FieldGroup = "advanced"
)

type RiskLevel string

const (
	RiskLevelLow     RiskLevel = "low"
	RiskLevelMedium  RiskLevel = "medium"
	RiskLevelHigh    RiskLevel = "high"
	RiskLevelBlocked RiskLevel = "blocked"
)

var (
	ErrFlowNotFound            = errors.New("credentialacq: flow not found")
	ErrFlowExpired             = errors.New("credentialacq: flow expired")
	ErrFlowReplay              = errors.New("credentialacq: flow replay")
	ErrStateMismatch           = errors.New("credentialacq: oauth state mismatch")
	ErrUnknownMode             = errors.New("credentialacq: unknown mode")
	ErrInvalidImportBody       = errors.New("credentialacq: invalid import body")
	ErrFeatureDisabled         = errors.New("credentialacq: feature disabled")
	ErrSecretInContext         = errors.New("credentialacq: redacted context contains secret-shaped material")
	ErrInvalidTokenShape       = errors.New("credentialacq: invalid token shape")
	ErrResponseTooLarge        = errors.New("credentialacq: response too large")
	ErrOAuthExchangerMissing   = errors.New("credentialacq: oauth exchanger missing")
	ErrOAuthRequiresCallback   = errors.New("credentialacq: oauth flow requires callback validation before finalize")
	ErrDevicePollPending       = errors.New("credentialacq: device authorization pending")
	ErrDevicePollInProgress    = errors.New("credentialacq: device poll in progress")
	ErrDevicePollTransient     = errors.New("credentialacq: device poll transient failure")
	ErrDeviceAccessDenied      = errors.New("credentialacq: device authorization denied")
	ErrDeviceExchangeAmbiguous = errors.New("credentialacq: device token exchange outcome ambiguous")
)

// ValidateHTTPHeaderMetadata 只接受可直接进入 Go HTTP 头值的短 ASCII 文本。
func ValidateHTTPHeaderMetadata(value string) error {
	if len(value) > 1024 {
		return ErrInvalidImportBody
	}
	for _, char := range value {
		if char < 0x20 || char > 0x7e {
			return ErrInvalidImportBody
		}
	}
	return nil
}

type DevicePollPendingError struct {
	RetryAfter time.Duration
}

func (e *DevicePollPendingError) Error() string {
	return ErrDevicePollPending.Error()
}

func (e *DevicePollPendingError) Unwrap() error {
	return ErrDevicePollPending
}

func DevicePollRetryAfter(err error) time.Duration {
	var pending *DevicePollPendingError
	if errors.As(err, &pending) && pending.RetryAfter > 0 {
		return pending.RetryAfter
	}
	return 5 * time.Second
}

type ModePlan struct {
	Vendor               string      `json:"vendor"`
	AuthMode             string      `json:"auth_mode"`
	Kind                 FlowKind    `json:"flow_kind"`
	ClientIdentitySource string      `json:"client_identity_source"`
	ManualFirst          bool        `json:"manual_first,omitempty"`
	LongLivedToggle      bool        `json:"long_lived_toggle,omitempty"`
	AllowedHelpers       []FlowKind  `json:"allowed_helpers,omitempty"`
	RequiredFields       []FieldSpec `json:"required_fields,omitempty"`
	IsEnabled            bool        `json:"is_enabled"`
	IsExperimental       bool        `json:"is_experimental"`
	FeatureFlag          string      `json:"feature_flag,omitempty"`
	RiskLevel            RiskLevel   `json:"risk_level"`
	RiskReasons          []string    `json:"risk_reasons,omitempty"`
}

type FieldSpec struct {
	Name       string         `json:"name"`
	Kind       FieldKind      `json:"kind"`
	Required   bool           `json:"required"`
	OneOfGroup string         `json:"one_of_group,omitempty"`
	Input      string         `json:"input,omitempty"`
	Redaction  FieldRedaction `json:"redaction"`
	Group      FieldGroup     `json:"group"`
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
	// ExternalAccountID/ExternalSubjectID/ExternalAccountEmail/AccountIDSource 携带在 token 交换时
	// 自动提取出的上游 provider 账号身份(accountident)。
	// 它们属于非机密的账号管理元数据,绝非授权输入,
	// 当提取无结果时为空(手工/operator 值优先)。
	ExternalAccountID    string
	ExternalSubjectID    string
	ExternalAccountEmail string
	AccountIDSource      string
	Subscription         subscriptionprofile.Observation
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
		apiKeyPlan(credentialstore.VendorAnthropic),
		// claude_ai_oauth 是交互式 OAuth(PKCE)模式,AllowedHelpers 必须仅含 FlowKindOAuth,
		// 与 chatgpt_oauth / code_assist / google_one 对齐。此前混入 FlowKindPaste 等于开了一扇手工旁路——
		// 管理员可 START flow_kind=paste 再 finalize,用手写 credentials body 注入任意 Anthropic token,
		// 完全绕过 callback/PKCE/state/授权码交换。需要粘贴既有 Anthropic OAuth 凭据者走 claude_code
		// (FlowKindCLIImport/JSONImport);粘贴 API key 走 anthropic/api_key(FlowKindPaste)。
		oauthPlan(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth, ClientSourcePublicCLI, []FlowKind{FlowKindOAuth}, RiskLevelMedium),
		cliSessionPlan(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeCode, ClientSourcePublicCLI, []FlowKind{FlowKindCLIImport, FlowKindJSONImport}),
		setupTokenPlan(),
		cloudPlan(credentialstore.VendorAnthropic, credentialstore.AuthModeBedrock, FlowKindPaste, ClientSourceNone, []FlowKind{FlowKindPaste, FlowKindCloudBootstrap, FlowKindOAuth}, awsSigV4Fields()),
		upstreamTokenPlan(credentialstore.VendorAnthropic, credentialstore.AuthModeVertexAnthropic, FlowKindJSONImport, ClientSourceOperatorConfig, []FlowKind{FlowKindJSONImport, FlowKindCloudBootstrap}, vertexFields()),
		apiKeyPlan(credentialstore.VendorOpenAI),
		oauthPlan(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth, ClientSourcePublicCLI, []FlowKind{FlowKindOAuth}, RiskLevelMedium),
		cliSessionPlan(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth, ClientSourcePublicCLI, []FlowKind{FlowKindCLIImport, FlowKindJSONImport, FlowKindOAuth}),
		// codex_web_oauth 是 Codex CLI 浏览器侧 authorization-code(PKCE)登录路径,与 codex_cli_oauth
		// 的 device-code 路径并列,作为独立可选的获取模式暴露为 OAuth-only(AllowedHelpers 仅含 FlowKindOAuth,
		// 同 chatgpt_oauth),由 boot 期 ValidateOAuthModeConsistency 闸守住其 exchanger 必须存在且非-fake。
		oauthPlan(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexWebOAuth, ClientSourcePublicCLI, []FlowKind{FlowKindOAuth}, RiskLevelMedium),
		upstreamTokenPlan(credentialstore.VendorOpenAI, credentialstore.AuthModeAzure, FlowKindPaste, ClientSourceOperatorConfig, []FlowKind{FlowKindPaste, FlowKindCloudBootstrap, FlowKindTokenExchange}, azureFields()),
		upstreamTokenPlan(credentialstore.VendorOpenAI, credentialstore.AuthModeRefreshToken, FlowKindTokenExchange, ClientSourcePerAccountOverride, []FlowKind{FlowKindTokenExchange, FlowKindPaste}, tokenOneOfFields("runtime_token", "access_token", "refresh_token")),
		apiKeyPlanWithMode(credentialstore.VendorGemini, credentialstore.AuthModeAIStudioAPIKey),
		upstreamTokenPlan(credentialstore.VendorGemini, credentialstore.AuthModeVertexSA, FlowKindJSONImport, ClientSourceOperatorConfig, []FlowKind{FlowKindJSONImport, FlowKindCloudBootstrap}, vertexFields()),
		oauthPlan(credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist, ClientSourcePublicCLI, []FlowKind{FlowKindOAuth}, RiskLevelMedium),
		oauthPlan(credentialstore.VendorGemini, credentialstore.AuthModeGoogleOne, ClientSourcePublicCLI, []FlowKind{FlowKindOAuth}, RiskLevelMedium),
		manualOAuthPlan(credentialstore.VendorGemini, credentialstore.AuthModeAntigravity, ClientSourcePublicCLI, []FlowKind{FlowKindOAuth, FlowKindTokenExchange}),
		manualOAuthPlan(credentialstore.VendorGemini, credentialstore.AuthModeOAuth, ClientSourceOperatorConfig, []FlowKind{FlowKindOAuth, FlowKindTokenExchange}),
		oauthPlan(credentialstore.VendorCopilot, credentialstore.AuthModeCopilotOAuth, ClientSourcePublicCLI, []FlowKind{FlowKindOAuth, FlowKindJSONImport}, RiskLevelMedium),
		manualOAuthPlan(credentialstore.VendorAntigravity, credentialstore.AuthModeOAuth, ClientSourceOperatorConfig, []FlowKind{FlowKindOAuth, FlowKindTokenExchange}),
		manualUpstreamTokenPlan(credentialstore.VendorWindsurf, credentialstore.AuthModeOAuth, FlowKindTokenExchange, ClientSourceOperatorConfig, []FlowKind{FlowKindTokenExchange, FlowKindPaste}, sessionTokenFields()),
		oauthPlan(credentialstore.VendorKimi, credentialstore.AuthModeKimiOAuth, ClientSourcePublicCLI, []FlowKind{FlowKindOAuth, FlowKindJSONImport}, RiskLevelMedium),
		hiddenOpenAICompatiblePlan(credentialstore.VendorOpenRouter),
		// 官 key 厂商(2026-07-02 Owner 指派接入):存储约束已由迁移 0169 放行,
		// grok/deepseek 从 hidden 提升为正式 api_key 粘贴计划,kimi + 国内大厂新增同构计划。
		apiKeyPlan(credentialstore.VendorDeepSeek),
		apiKeyPlan(credentialstore.VendorGrok),
		oauthPlan(credentialstore.VendorGrok, credentialstore.AuthModeXAIOAuth, ClientSourceOperatorConfig, []FlowKind{FlowKindOAuth}, RiskLevelMedium),
		apiKeyPlan(credentialstore.VendorKimi),
		apiKeyPlan(credentialstore.VendorQwen),
		apiKeyPlan(credentialstore.VendorGLM),
		apiKeyPlan(credentialstore.VendorYi),
		apiKeyPlan(credentialstore.VendorBaichuan),
		apiKeyPlan(credentialstore.VendorDoubao),
		apiKeyPlan(credentialstore.VendorMiniMax),
		apiKeyPlan(credentialstore.VendorErnie),
		apiKeyPlan(credentialstore.VendorHunyuan),
		apiKeyPlan(credentialstore.VendorStep),
		// 全球推理托管云:Owner 明确不接,保持 hidden(且无 handlerSpec、不进 0169 CHECK,双层拒绝)。
		hiddenOpenAICompatiblePlan(credentialstore.VendorMistral),
		hiddenOpenAICompatiblePlan(credentialstore.VendorGroqCloud),
		hiddenOpenAICompatiblePlan(credentialstore.VendorTogether),
		hiddenOpenAICompatiblePlan(credentialstore.VendorPerplexity),
		hiddenOpenAICompatiblePlan(credentialstore.VendorFireworks),
	}
}

func apiKeyPlan(vendor string) ModePlan {
	return apiKeyPlanWithMode(vendor, credentialstore.AuthModeAPIKey)
}

func apiKeyPlanWithMode(vendor, authMode string) ModePlan {
	return ModePlan{
		Vendor: vendor, AuthMode: authMode, Kind: FlowKindPaste, ClientIdentitySource: ClientSourceNone,
		AllowedHelpers: []FlowKind{FlowKindPaste, FlowKindCSVImport, FlowKindJSONImport},
		RequiredFields: apiKeyFields(),
		IsEnabled:      true,
		RiskLevel:      RiskLevelLow,
	}
}

func hiddenOpenAICompatiblePlan(vendor string) ModePlan {
	plan := apiKeyPlan(vendor)
	plan.IsExperimental = true
	plan.FeatureFlag = "account_modes.openai_compatible"
	plan.RiskLevel = RiskLevelMedium
	plan.RiskReasons = []string{"account credential storage constraints are not released for this provider"}
	return plan
}

func oauthPlan(vendor, authMode, clientSource string, helpers []FlowKind, risk RiskLevel) ModePlan {
	return ModePlan{
		Vendor: vendor, AuthMode: authMode, Kind: FlowKindOAuth, ClientIdentitySource: clientSource,
		AllowedHelpers: helpers,
		RequiredFields: sessionTokenFields(),
		IsEnabled:      true,
		RiskLevel:      risk,
	}
}

func cliSessionPlan(vendor, authMode, clientSource string, helpers []FlowKind) ModePlan {
	return ModePlan{
		Vendor: vendor, AuthMode: authMode, Kind: FlowKindCLIImport, ClientIdentitySource: clientSource,
		AllowedHelpers: helpers,
		RequiredFields: sessionTokenFields(),
		IsEnabled:      true,
		RiskLevel:      RiskLevelMedium,
	}
}

func setupTokenPlan() ModePlan {
	return ModePlan{
		Vendor: credentialstore.VendorAnthropic, AuthMode: credentialstore.AuthModeClaudeSetupToken,
		Kind: FlowKindSetupToken, ClientIdentitySource: ClientSourcePublicCLI,
		AllowedHelpers: []FlowKind{FlowKindSetupToken},
		RequiredFields: []FieldSpec{secretField("setup_token", true)},
		IsEnabled:      true, RiskLevel: RiskLevelMedium,
		RiskReasons: []string{"长期访问令牌必须只通过秘密输入与加密存储处理"},
	}
}

func manualOAuthPlan(vendor, authMode, clientSource string, helpers []FlowKind) ModePlan {
	plan := oauthPlan(vendor, authMode, clientSource, helpers, RiskLevelMedium)
	plan.ManualFirst = true
	return plan
}

func cloudPlan(vendor, authMode string, kind FlowKind, clientSource string, helpers []FlowKind, fields []FieldSpec) ModePlan {
	return ModePlan{
		Vendor: vendor, AuthMode: authMode, Kind: kind, ClientIdentitySource: clientSource,
		ManualFirst:    true,
		AllowedHelpers: helpers,
		RequiredFields: fields,
		IsEnabled:      true,
		RiskLevel:      RiskLevelMedium,
	}
}

func upstreamTokenPlan(vendor, authMode string, kind FlowKind, clientSource string, helpers []FlowKind, fields []FieldSpec) ModePlan {
	return ModePlan{
		Vendor: vendor, AuthMode: authMode, Kind: kind, ClientIdentitySource: clientSource,
		ManualFirst:    kind == FlowKindPaste || kind == FlowKindOAuth,
		AllowedHelpers: helpers,
		RequiredFields: fields,
		IsEnabled:      true,
		RiskLevel:      RiskLevelMedium,
	}
}

func manualUpstreamTokenPlan(vendor, authMode string, kind FlowKind, clientSource string, helpers []FlowKind, fields []FieldSpec) ModePlan {
	plan := upstreamTokenPlan(vendor, authMode, kind, clientSource, helpers, fields)
	plan.ManualFirst = true
	return plan
}

func apiKeyFields() []FieldSpec {
	return []FieldSpec{secretField("api_key", true)}
}

func sessionTokenFields() []FieldSpec {
	return tokenOneOfFields("runtime_token", "session_token", "access_token", "refresh_token")
}

func tokenOneOfFields(group string, names ...string) []FieldSpec {
	fields := make([]FieldSpec, 0, len(names))
	for _, name := range names {
		f := secretField(name, false)
		f.OneOfGroup = group
		fields = append(fields, f)
	}
	return fields
}

func azureFields() []FieldSpec {
	return tokenOneOfFields("runtime_token", "api_key", "azure_api_key", "access_token")
}

func vertexFields() []FieldSpec {
	return []FieldSpec{
		withOneOf(secretField("access_token", false), "runtime_token"),
		{Name: "metadata_token_endpoint", Kind: FieldKindURL, OneOfGroup: "runtime_token", Redaction: RedactionNone, Group: FieldGroupCloud},
		{Name: "client_email", Kind: FieldKindString, OneOfGroup: "runtime_token", Redaction: RedactionNone, Group: FieldGroupCloud},
	}
}

func awsSigV4Fields() []FieldSpec {
	return []FieldSpec{
		{Name: "aws_access_key_id", Kind: FieldKindSecret, Required: true, Redaction: RedactionSecret, Group: FieldGroupCloud},
		{Name: "aws_secret_access_key", Kind: FieldKindSecret, Required: true, Redaction: RedactionSecret, Group: FieldGroupCloud},
		{Name: "aws_region", Kind: FieldKindString, Required: true, Redaction: RedactionNone, Group: FieldGroupCloud},
	}
}

func secretField(name string, required bool) FieldSpec {
	return FieldSpec{Name: name, Kind: FieldKindSecret, Required: required, Redaction: RedactionSecret, Group: FieldGroupCredential}
}

func withOneOf(field FieldSpec, group string) FieldSpec {
	field.OneOfGroup = group
	return field
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
// 之类的手工绕过路径。
func flowKindAllowed(allowed []FlowKind, candidate FlowKind) bool {
	candidate = NormalizeFlowKind(candidate)
	for _, k := range allowed {
		if NormalizeFlowKind(k) == candidate {
			return true
		}
	}
	return false
}

// RequiresCallbackValidation 报告某 acquisition flow 是否必须先经 OAuth 回调校验(status=validated)
// 才能 finalize。callback 式 OAuth(PKCE,经 CompleteOAuthCallback 校验 state 并交换授权码)的 finalize
// 不能仅凭 caller 手写 credentials body 完成,否则越权/恶意 admin 可绕过回调直接注入任意凭据。
// 但 device_code / sso 式 flow 的凭据来自轮询(PollDeviceCodeToken / PollSSOToken),其生命周期【不经】
// validated 状态 —— 必须豁免,否则会永久阻断 device-code/sso 凭据获取(copilot/openai-codex/kiro)。
// 故按 auth_type 精确区分:排除 device_code/sso,其余 OAuth(含 pkce 与 auth_type 未定的 callback flow)
// 一律要求 validated。
func RequiresCallbackValidation(kind FlowKind, authType AuthType) bool {
	return NormalizeFlowKind(kind) == FlowKindOAuth && authType != AuthTypeDeviceCode && authType != AuthTypeSSO
}

// isTerminalStatus 报告某 acquisition flow 是否已处终态(finalized/cancelled/expired/failed)。终态 flow 的
// 状态机已结束:既不能再被 OAuth 回调重新驱动(CompleteOAuthCallback replay),也不能被 UpdateStatus 前推
// 或被 Cancel。任何对终态行的状态写入都应作为 ErrFlowReplay 拒绝,迫使调用方开新 flow —— 否则攻击者/重放
// 可把已 cancelled/failed/expired 的 flow 拉回 callback_received→validated 复活后注入凭据。
// 此前 finalized 单独被守(consumed_at + StatusFinalized),cancelled/expired/failed 三态无人守:既是
// CompleteOAuthCallback 的提前 replay 闸,也是 UpdateStatus/Cancel SQL CAS predicate 的 Go 侧真相源,
// 三处共用避免漂移(同 RequiresCallbackValidation 在 的 fake-vs-真 SQL 教训)。
func isTerminalStatus(status FlowStatus) bool {
	switch status {
	case StatusFinalized, StatusCancelled, StatusExpired, StatusFailed:
		return true
	default:
		return false
	}
}

func NormalizeFlowStatus(status FlowStatus) FlowStatus {
	switch status {
	case StatusStarted, StatusWaitingForUser, StatusCallbackReceived, StatusValidated, StatusFinalized, StatusCancelled, StatusExpired, StatusFailed:
		return status
	default:
		return ""
	}
}
