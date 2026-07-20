package credentialacq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

const (
	xaiOAuthAuthURL  = "https://auth.x.ai/oauth2/authorize"
	xaiOAuthTokenURL = "https://auth.x.ai/oauth2/token"
	xaiOAuthClientID = "b1a00492-073a-47ea-816f-4c329264a828"
	xaiOAuthScope    = "openid profile email offline_access grok-cli:access api:access"

	AntigravityOAuthAuthURL         = "https://accounts.google.com/o/oauth2/v2/auth"
	AntigravityOAuthTokenURL        = "https://oauth2.googleapis.com/token"
	AntigravityOAuthRedirectURI     = "http://127.0.0.1:1455/auth/callback"
	AntigravityOAuthClientIDEnv     = "HUAKAI_ANTIGRAVITY_OAUTH_CLIENT_ID"
	AntigravityOAuthClientSecretEnv = "HUAKAI_ANTIGRAVITY_OAUTH_CLIENT_SECRET"
	AntigravityPublicCLIScope       = "https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/cclog https://www.googleapis.com/auth/experimentsandconfigs"
	antigravityBuiltinClientID      = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"
	antigravityBuiltinClientSecret  = "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"
)

// 导出供 credentialworker 做 token 刷新复用(单一事实来源)。
const (
	XAIOAuthTokenURL = xaiOAuthTokenURL
	XAIOAuthClientID = xaiOAuthClientID
)

type Exchanger interface {
	StartOAuthFlow(context.Context, *PostgresSessionStore, StartInput, OAuthClientConfig) (OAuthStartResult, error)
	ExchangeOAuthCode(context.Context, Session, string) (CredentialCandidate, error)
}

type StoreAwareExchanger interface {
	ExchangeOAuthCodeWithStore(context.Context, *PostgresSessionStore, Session, string, string) (CredentialCandidate, error)
}

type ExchangerRegistry struct {
	mu         sync.RWMutex
	exchangers map[string]Exchanger
}

func NewExchangerRegistry() *ExchangerRegistry {
	return &ExchangerRegistry{exchangers: map[string]Exchanger{}}
}

func DefaultExchangerRegistry() *ExchangerRegistry {
	r := NewExchangerRegistry()
	register := func(name string, exc Exchanger) {
		_ = r.RegisterExchanger(name, exc)
	}
	openAICodexDeviceCode := openAICodexDeviceCodeExchanger{}
	register(credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeClaudeAIOAuth), newClaudeAIOAuthExchanger())
	register(credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist), newGeminiPublicCLIOAuthExchanger(credentialstore.AuthModeCodeAssist))
	register(credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeGoogleOne), newGeminiPublicCLIOAuthExchanger(credentialstore.AuthModeGoogleOne))
	register(credentialstore.ModeKey(credentialstore.VendorGemini, credentialstore.AuthModeAntigravity),
		newAuthorizationCodeOAuthExchanger(
			credentialstore.VendorGemini,
			credentialstore.AuthModeAntigravity,
			TokenShapeAnySessionOrAccess,
			AntigravityPublicCLIConfig(),
		))
	register("gemini/oauth", newAuthorizationCodeOAuthExchanger(credentialstore.VendorGemini, credentialstore.AuthModeOAuth, TokenShapeAnySessionOrAccess))
	register(credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth), newChatGPTOAuthExchanger())
	register(credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexCLIOAuth), openAICodexDeviceCode)
	// codex_web_oauth 与 codex_cli_oauth(device-code)并列:web 路径走 authorization-code(PKCE)
	// 浏览器登录,绑定独立 mode key,不扰动 device-code 注册。
	register(credentialstore.ModeKey(credentialstore.VendorOpenAI, credentialstore.AuthModeCodexWebOAuth), newCodexWebOAuthExchanger())
	register("openai_codex/device-code", openAICodexDeviceCode)
	register("openai_codex/device_code", openAICodexDeviceCode)
	register(credentialstore.ModeKey(credentialstore.VendorAnthropic, credentialstore.AuthModeBedrock), NewSSOExchanger())
	register("antigravity/oauth", newAuthorizationCodeOAuthExchanger(
		credentialstore.VendorAntigravity,
		credentialstore.AuthModeOAuth,
		TokenShapeAnySessionOrAccess,
		AntigravityPublicCLIConfig(),
	))
	register(credentialstore.ModeKey(credentialstore.VendorGrok, credentialstore.AuthModeXAIOAuth),
		newAuthorizationCodeOAuthExchanger(credentialstore.VendorGrok, credentialstore.AuthModeXAIOAuth, TokenShapeAccessRefresh, xaiOAuthConfig()))
	copilotDeviceCode := newCopilotDeviceCodeExchanger()
	register(credentialstore.ModeKey(credentialstore.VendorCopilot, credentialstore.AuthModeCopilotOAuth), copilotDeviceCode)
	register("copilot/device_code", copilotDeviceCode)
	register(credentialstore.ModeKey(credentialstore.VendorKimi, credentialstore.AuthModeKimiOAuth), newKimiDeviceCodeExchanger())
	register("kiro/sso", NewSSOExchanger())
	// cursor/oauth 无对应 ModePlan、windsurf/oauth 的 ModePlan 实为 FlowKindTokenExchange(types.go),
	// 两个 fake exchanger 注册已成孤儿(orphaned dead/dangerous wiring)。移除,确保默认 registry 不残留任何
	// 会把回调码当 JSON 凭据吞下的 fake exchanger。
	return r
}

func xaiOAuthConfig() OAuthClientConfig {
	return OAuthClientConfig{
		AuthURL: xaiOAuthAuthURL, TokenURL: xaiOAuthTokenURL,
		ClientID: xaiOAuthClientID, Scopes: strings.Fields(xaiOAuthScope),
		Source: ClientSourceOperatorConfig,
	}
}

// AntigravityPublicCLIConfig 是导入、授权码交换和刷新共同使用的公开客户端身份。
// 环境变量只允许部署者替换整套公开客户端凭据，不允许单次请求注入不同身份。
func AntigravityPublicCLIConfig() OAuthClientConfig {
	clientID := strings.TrimSpace(os.Getenv(AntigravityOAuthClientIDEnv))
	if clientID == "" {
		clientID = antigravityBuiltinClientID
	}
	clientSecret := strings.TrimSpace(os.Getenv(AntigravityOAuthClientSecretEnv))
	if clientSecret == "" {
		clientSecret = antigravityBuiltinClientSecret
	}
	return OAuthClientConfig{
		AuthURL: AntigravityOAuthAuthURL, TokenURL: AntigravityOAuthTokenURL,
		ClientID: clientID, ClientSecret: clientSecret,
		RedirectURI: AntigravityOAuthRedirectURI,
		Scopes:      strings.Fields(AntigravityPublicCLIScope),
		Source:      ClientSourcePublicCLI,
	}
}

var defaultExchangers = DefaultExchangerRegistry()

func RegisterExchanger(name string, exc Exchanger) error {
	return defaultExchangers.RegisterExchanger(name, exc)
}

func RegisterOrReplaceExchanger(name string, exc Exchanger) error {
	return defaultExchangers.RegisterOrReplaceExchanger(name, exc)
}

func (r *ExchangerRegistry) RegisterExchanger(name string, exc Exchanger) error {
	if r == nil {
		return errors.New("credentialacq: exchanger registry is nil")
	}
	if exc == nil {
		return errors.New("credentialacq: exchanger is nil")
	}
	key := normalizeExchangerName(name)
	if key == "" {
		return errors.New("credentialacq: exchanger name is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.exchangers == nil {
		r.exchangers = map[string]Exchanger{}
	}
	if _, exists := r.exchangers[key]; exists {
		return fmt.Errorf("credentialacq: exchanger already registered: %s", key)
	}
	r.exchangers[key] = exc
	return nil
}

func (r *ExchangerRegistry) RegisterOrReplaceExchanger(name string, exc Exchanger) error {
	if r == nil {
		return errors.New("credentialacq: exchanger registry is nil")
	}
	if exc == nil {
		return errors.New("credentialacq: exchanger is nil")
	}
	key := normalizeExchangerName(name)
	if key == "" {
		return errors.New("credentialacq: exchanger name is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.exchangers == nil {
		r.exchangers = map[string]Exchanger{}
	}
	r.exchangers[key] = exc
	return nil
}

func (r *ExchangerRegistry) Lookup(name string) (Exchanger, bool) {
	if r == nil {
		return nil, false
	}
	key := normalizeExchangerName(name)
	r.mu.RLock()
	defer r.mu.RUnlock()
	exc, ok := r.exchangers[key]
	return exc, ok
}

func (r *ExchangerRegistry) Exchange(ctx context.Context, session Session, code string) (CredentialCandidate, error) {
	exc, ok := r.Lookup(exchangerKey(session.Vendor, session.AuthMode))
	if !ok {
		exc, ok = r.Lookup(session.Vendor)
	}
	if !ok {
		return CredentialCandidate{}, fmt.Errorf("%w: %s", ErrOAuthExchangerMissing, exchangerKey(session.Vendor, session.AuthMode))
	}
	return exc.ExchangeOAuthCode(ctx, session, code)
}

func (r *ExchangerRegistry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.exchangers))
	for name := range r.exchangers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func exchangerKey(vendor, authMode string) string {
	return credentialstore.ModeKey(vendor, authMode)
}

func normalizeExchangerName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// failClosedExchanger 是一个显式停用的 acquisition exchanger:StartOAuthFlow 与 ExchangeOAuthCode 都
// 静默接受伪造凭据,或 missing exchanger 只在回调期才暴露 ErrOAuthExchangerMissing)。reason 写明原因。
type failClosedExchanger struct {
	reason string
}

func newFailClosedExchanger(reason string) Exchanger {
	return failClosedExchanger{reason: reason}
}

func (e failClosedExchanger) StartOAuthFlow(context.Context, *PostgresSessionStore, StartInput, OAuthClientConfig) (OAuthStartResult, error) {
	return OAuthStartResult{}, fmt.Errorf("%w: %s", ErrFeatureDisabled, e.reason)
}

func (e failClosedExchanger) ExchangeOAuthCode(context.Context, Session, string) (CredentialCandidate, error) {
	return CredentialCandidate{}, fmt.Errorf("%w: %s", ErrFeatureDisabled, e.reason)
}

// ValidateOAuthModeConsistency 是启动期自一致性闸:每个 Kind==FlowKindOAuth 的 ModePlan 必须在 registry
// 解析到一个非-nil 且非-fake 的 exchanger。fake exchanger(pkceFakeExchanger)会把攻击者可影响的回调码当
// JSON 凭据接受 —— acquisition 信任边界破坏;缺失 exchanger 只会在回调期静默 ErrOAuthExchangerMissing。
// 本闸把这两类配置漂移在 boot 时暴露为 fatal,杜绝"暴露为可完成的 OAuth mode 实则映射到 fake/缺失"的回归。
// lookup 顺序与运行期 ExchangerRegistry.Exchange 保持一致(先 mode key,后 vendor 级 fallback)。
func ValidateOAuthModeConsistency(plans []ModePlan, registry *ExchangerRegistry) error {
	if registry == nil {
		return errors.New("credentialacq: OAuth 自一致性校验需要非 nil 的 exchanger registry")
	}
	var problems []string
	for _, plan := range plans {
		if plan.Kind != FlowKindOAuth {
			continue
		}
		key := exchangerKey(plan.Vendor, plan.AuthMode)
		exc, ok := registry.Lookup(key)
		if !ok || exc == nil {
			exc, ok = registry.Lookup(plan.Vendor)
		}
		if !ok || exc == nil {
			problems = append(problems, fmt.Sprintf("%s: 无 exchanger 注册(回调期会 ErrOAuthExchangerMissing)", key))
			continue
		}
		if _, isFake := exc.(pkceFakeExchanger); isFake {
			problems = append(problems, fmt.Sprintf("%s: 注册的是 fake exchanger(会把回调码当 JSON 凭据接受)", key))
		}
		if _, disabled := exc.(failClosedExchanger); disabled {
			problems = append(problems, fmt.Sprintf("%s: 注册的是停用 exchanger(入口会稳定失败)", key))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("credentialacq: OAuth ModePlan 自一致性校验失败: %s", strings.Join(problems, "; "))
	}
	return nil
}

type TokenShape string

const (
	TokenShapeAccessRefresh      TokenShape = "access_refresh"
	TokenShapeSession            TokenShape = "session"
	TokenShapeAnySessionOrAccess TokenShape = "any_session_or_access"
)

type pkceFakeExchanger struct {
	shape TokenShape
}

func NewPKCEFakeExchanger(shape TokenShape) Exchanger {
	if shape == "" {
		shape = TokenShapeAnySessionOrAccess
	}
	return pkceFakeExchanger{shape: shape}
}

func (e pkceFakeExchanger) StartOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
	return startPKCEOAuthFlow(ctx, store, in, cfg)
}

func (e pkceFakeExchanger) ExchangeOAuthCode(_ context.Context, session Session, code string) (CredentialCandidate, error) {
	fields, raw, err := parseFakeTokenPayload(code)
	if err != nil {
		return CredentialCandidate{}, err
	}
	if err := validateTokenShape(fields, e.shape); err != nil {
		return CredentialCandidate{}, err
	}
	return CredentialCandidate{
		TenantID: session.TenantID, ProviderAccountID: session.ProviderAccountID,
		Vendor: session.Vendor, AuthMode: session.AuthMode, Payload: raw, ActorID: session.ActorID,
	}, nil
}

func parseFakeTokenPayload(code string) (map[string]any, []byte, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, nil, fmt.Errorf("%w: empty fake token payload", ErrInvalidTokenShape)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(code), &fields); err != nil {
		return nil, nil, fmt.Errorf("%w: fake mode expects JSON token payload", ErrInvalidTokenShape)
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, err
	}
	return fields, raw, nil
}

func validateTokenShape(fields map[string]any, shape TokenShape) error {
	hasAccess := stringField(fields, "access_token") != ""
	hasRefresh := stringField(fields, "refresh_token") != ""
	hasSession := stringField(fields, "session_token") != ""
	switch shape {
	case TokenShapeAccessRefresh:
		if hasAccess || hasRefresh {
			return nil
		}
	case TokenShapeSession:
		if hasSession {
			return nil
		}
	default:
		if hasSession || hasAccess || hasRefresh {
			return nil
		}
	}
	return fmt.Errorf("%w: token payload does not match %s", ErrInvalidTokenShape, shape)
}

func stringField(fields map[string]any, key string) string {
	if fields == nil {
		return ""
	}
	value, ok := fields[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}
