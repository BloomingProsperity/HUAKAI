package credentialacq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

const deviceCodeSlowDownStep = 5 * time.Second
const oauthDeviceCodeGrantType = "urn:ietf:params:oauth:grant-type:device_code"

const (
	openAICodexDeviceAuthorizationURL = "https://auth.openai.com/api/accounts/deviceauth/usercode"
	openAICodexDeviceTokenURL         = "https://auth.openai.com/api/accounts/deviceauth/token"
	openAICodexDeviceVerificationURI  = "https://auth.openai.com/codex/device"
	openAICodexDeviceRedirectURI      = "https://auth.openai.com/deviceauth/callback"
	openAICodexOAuthTokenURL          = "https://auth.openai.com/oauth/token"
)

// 导出给定时刷新链复用，避免账号获取与续期使用不同的公开客户端身份。
const (
	OpenAICodexOAuthTokenURL     = openAICodexOAuthTokenURL
	OpenAICodexOAuthClientID     = chatgptOAuthClientID
	OpenAICodexOAuthRefreshScope = "openid profile email"
)

const (
	kimiOAuthClientID               = "17e5f671-d194-4dfb-9706-5516cb48c098"
	kimiOAuthDeviceAuthorizationURL = "https://auth.kimi.com/api/oauth/device_authorization"
	kimiOAuthTokenURL               = "https://auth.kimi.com/api/oauth/token"
)

const (
	copilotOAuthClientID               = "Iv1.b507a08c87ecfe98"
	copilotOAuthDeviceAuthorizationURL = "https://github.com/login/device/code"
	copilotOAuthTokenURL               = "https://github.com/login/oauth/access_token"
	copilotOAuthScope                  = "read:user"
)

// 导出供 credentialworker 做 token 刷新复用(单一事实来源)。
const (
	KimiOAuthTokenURL = kimiOAuthTokenURL
	KimiOAuthClientID = kimiOAuthClientID
)

type copilotDeviceCodeExchanger struct{}

func newCopilotDeviceCodeExchanger() Exchanger {
	return copilotDeviceCodeExchanger{}
}

func (copilotDeviceCodeExchanger) StartOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, override OAuthClientConfig) (OAuthStartResult, error) {
	cfg := OAuthClientConfig{
		ClientID:   copilotOAuthClientID,
		AuthURL:    copilotOAuthDeviceAuthorizationURL,
		TokenURL:   copilotOAuthTokenURL,
		Scopes:     []string{copilotOAuthScope},
		Source:     ClientSourcePublicCLI,
		HTTPClient: defaultDeviceCodeHTTPClient(),
	}
	// 公开客户端身份属于服务端合同。请求方只能注入测试/部署传输，不能替换
	// client_id、授权端点或 scope，把管理员凭据引向任意 OAuth 服务。
	if override.HTTPClient != nil {
		cfg.HTTPClient = override.HTTPClient
	}
	in.Vendor = credentialstore.VendorCopilot
	in.AuthMode = credentialstore.AuthModeCopilotOAuth
	in.ClientIdentitySource = ClientSourcePublicCLI
	return startDeviceAuthorization(ctx, store, in, cfg, AuthTypeDeviceCode)
}

func (copilotDeviceCodeExchanger) ExchangeOAuthCode(context.Context, Session, string) (CredentialCandidate, error) {
	return CredentialCandidate{}, errors.New("credentialacq: copilot device-code flow does not use oauth callback exchange")
}

type kimiDeviceCodeExchanger struct{}

func newKimiDeviceCodeExchanger() Exchanger {
	return kimiDeviceCodeExchanger{}
}

func (kimiDeviceCodeExchanger) StartOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
	kimiCfg, err := kimiDeviceCodeOAuthConfig(cfg)
	if err != nil {
		return OAuthStartResult{}, err
	}
	in.Vendor = credentialstore.VendorKimi
	in.AuthMode = credentialstore.AuthModeKimiOAuth
	in.ClientIdentitySource = ClientSourcePublicCLI
	return startDeviceAuthorization(ctx, store, in, kimiCfg, AuthTypeDeviceCode)
}

func (kimiDeviceCodeExchanger) ExchangeOAuthCode(context.Context, Session, string) (CredentialCandidate, error) {
	return CredentialCandidate{}, errors.New("credentialacq: kimi device-code flow does not use oauth callback exchange")
}

func kimiDeviceCodeOAuthConfig(override OAuthClientConfig) (OAuthClientConfig, error) {
	cfg := OAuthClientConfig{
		ClientID:   kimiOAuthClientID,
		AuthURL:    kimiOAuthDeviceAuthorizationURL,
		TokenURL:   kimiOAuthTokenURL,
		Scopes:     override.Scopes,
		Source:     ClientSourcePublicCLI,
		HTTPClient: defaultDeviceCodeHTTPClient(),
	}
	if override.HTTPClient != nil {
		cfg.HTTPClient = override.HTTPClient
	}
	if raw := strings.TrimSpace(override.AuthURL); raw != "" {
		if err := validateKimiOAuthEndpointURL(raw); err != nil {
			return OAuthClientConfig{}, fmt.Errorf("%w: kimi_oauth auth_url rejected (%v)", ErrFeatureDisabled, err)
		}
		cfg.AuthURL = raw
	}
	if raw := strings.TrimSpace(override.TokenURL); raw != "" {
		if err := validateKimiOAuthEndpointURL(raw); err != nil {
			return OAuthClientConfig{}, fmt.Errorf("%w: kimi_oauth token_url rejected (%v)", ErrFeatureDisabled, err)
		}
		cfg.TokenURL = raw
	}
	return cfg, nil
}

func validateKimiOAuthEndpointURL(raw string) error {
	if err := validateOAuthEndpointURL(raw); err != nil {
		return err
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return err
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "kimi.com" && !strings.HasSuffix(host, ".kimi.com") {
		return fmt.Errorf("host=%s is outside kimi.com", host)
	}
	return nil
}

type openAICodexDeviceCodeExchanger struct{}

func (openAICodexDeviceCodeExchanger) StartOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
	var err error
	cfg, err = openAICodexDeviceCodeOAuthConfig(cfg)
	if err != nil {
		return OAuthStartResult{}, err
	}
	in.Vendor = credentialstore.VendorOpenAI
	in.AuthMode = credentialstore.AuthModeCodexCLIOAuth
	in.ClientIdentitySource = cfg.Source
	return startOpenAICodexDeviceAuthorization(ctx, store, in, cfg)
}

func (openAICodexDeviceCodeExchanger) ExchangeOAuthCode(context.Context, Session, string) (CredentialCandidate, error) {
	return CredentialCandidate{}, errors.New("credentialacq: openai codex device-code flow does not use oauth callback exchange")
}

func openAICodexDeviceCodeOAuthConfig(override OAuthClientConfig) (OAuthClientConfig, error) {
	if !hasOpenAICodexOperatorOverride(override) {
		cfg := OAuthClientConfig{
			AuthURL:     openAICodexDeviceAuthorizationURL,
			TokenURL:    openAICodexDeviceTokenURL,
			ClientID:    chatgptOAuthClientID,
			RedirectURI: openAICodexDeviceRedirectURI,
			Scopes:      strings.Fields(chatgptOAuthScope),
			Source:      ClientSourcePublicCLI,
			HTTPClient:  defaultDeviceCodeHTTPClient(),
		}
		if override.HTTPClient != nil {
			cfg.HTTPClient = override.HTTPClient
		}
		return cfg, nil
	}
	if err := validateOpenAICodexOperatorDeviceCodeConfig(override); err != nil {
		return OAuthClientConfig{}, err
	}
	return override, nil
}

func hasOpenAICodexOperatorOverride(cfg OAuthClientConfig) bool {
	return strings.TrimSpace(cfg.AuthURL) != "" || strings.TrimSpace(cfg.TokenURL) != "" ||
		strings.TrimSpace(cfg.ClientID) != "" || strings.TrimSpace(cfg.ClientSecret) != "" ||
		strings.TrimSpace(cfg.RedirectURI) != "" || len(trimmedFields(cfg.Scopes)) > 0 ||
		(strings.TrimSpace(cfg.Source) != "" && strings.TrimSpace(cfg.Source) != ClientSourcePublicCLI)
}

func validateOpenAICodexOperatorDeviceCodeConfig(cfg OAuthClientConfig) error {
	var missing []string
	if strings.TrimSpace(cfg.Source) != ClientSourceOperatorConfig {
		missing = append(missing, "source=operator_config")
	}
	if strings.TrimSpace(cfg.AuthURL) == "" {
		missing = append(missing, "device_authorization_url")
	}
	if strings.TrimSpace(cfg.TokenURL) == "" {
		missing = append(missing, "token_url")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		missing = append(missing, "client_id")
	}
	if !hasOAuthScope(cfg.Scopes) {
		missing = append(missing, "scope")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: openai codex operator device-code config missing %s", ErrFeatureDisabled, strings.Join(missing, ","))
	}
	for _, endpoint := range []struct{ name, raw string }{{"device_authorization_url", cfg.AuthURL}, {"token_url", cfg.TokenURL}} {
		if err := validateOAuthEndpointURL(endpoint.raw); err != nil {
			return fmt.Errorf("%w: openai codex %s rejected (%v)", ErrFeatureDisabled, endpoint.name, err)
		}
	}
	return nil
}

func hasOAuthScope(scopes []string) bool {
	for _, scope := range scopes {
		if strings.TrimSpace(scope) != "" {
			return true
		}
	}
	return false
}

func WithDeviceCodeHTTPClient(client *http.Client) DeviceCodeOption {
	return func(o *deviceCodeOptions) {
		if client != nil {
			o.client = client
		}
	}
}

func WithDeviceCodeNow(now func() time.Time) DeviceCodeOption {
	return func(o *deviceCodeOptions) {
		if now != nil {
			o.now = now
		}
	}
}

func WithDeviceCodeSleeper(sleep func(context.Context, time.Duration) error) DeviceCodeOption {
	return func(o *deviceCodeOptions) {
		if sleep != nil {
			o.sleep = sleep
		}
	}
}

func PollDeviceCodeToken(ctx context.Context, session Session, cfg OAuthClientConfig, opts ...DeviceCodeOption) (CredentialCandidate, error) {
	if err := validateDeviceCodePollEndpoints(session, cfg); err != nil {
		return CredentialCandidate{}, err
	}
	if credentialstore.Normalize(session.Vendor) == credentialstore.VendorOpenAI &&
		credentialstore.Normalize(session.AuthMode) == credentialstore.AuthModeCodexCLIOAuth &&
		stringFromPayload(session.DeviceCodePayload, "device_auth_id") != "" {
		return pollOpenAICodexDeviceAuthorizationToken(ctx, session, cfg, opts...)
	}
	return pollDeviceAuthorizationToken(ctx, session, cfg, AuthTypeDeviceCode, opts...)
}

type deviceAuthorizationStartResponse struct {
	DeviceAuthorizationID  string `json:"device_auth_id"`
	DeviceCode             string `json:"device_code"`
	DeviceCodeCamel        string `json:"deviceCode"`
	UserCode               string `json:"user_code"`
	UserCodeCamel          string `json:"userCode"`
	UserCodeFallback       string `json:"usercode"`
	VerificationURI        string `json:"verification_uri"`
	VerificationURICamel   string `json:"verificationUri"`
	VerificationURIAlt     string `json:"verification_url"`
	VerificationComplete   string `json:"verification_uri_complete"`
	VerificationCompleteGo string `json:"verificationUriComplete"`
	ExpiresIn              int    `json:"expires_in"`
	ExpiresInCamel         int    `json:"expiresIn"`
	Interval               intish `json:"interval"`
}

type intish int

func (i *intish) UnmarshalJSON(raw []byte) error {
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		*i = intish(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	_, _ = fmt.Sscanf(strings.TrimSpace(s), "%d", &n)
	*i = intish(n)
	return nil
}

func startOpenAICodexDeviceAuthorization(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
	if store == nil {
		return OAuthStartResult{}, errors.New("credentialacq: session store not configured")
	}
	client := defaultDeviceCodeHTTPClient()
	if cfg.HTTPClient != nil {
		client = cfg.HTTPClient
	}
	var response deviceAuthorizationStartResponse
	if err := postJSON(ctx, client, strings.TrimSpace(cfg.AuthURL), map[string]any{
		"client_id": strings.TrimSpace(cfg.ClientID),
	}, &response); err != nil {
		return OAuthStartResult{}, err
	}
	payload, err := normalizeOpenAICodexDeviceStartResponse(response, cfg, store.now().UTC())
	if err != nil {
		return OAuthStartResult{}, err
	}
	in.Kind = FlowKindOAuth
	if in.RedirectURI == "" {
		in.RedirectURI = firstNonEmpty(cfg.RedirectURI, openAICodexDeviceRedirectURI)
	}
	if len(in.RequestedScopes) == 0 {
		in.RequestedScopes = cfg.Scopes
	}
	in.RedactedContext = mergeRedactedContext(in.RedactedContext, map[string]any{
		"auth_type":             string(AuthTypeDeviceCode),
		"device_user_display":   payload["user_code"],
		"verification_uri":      payload["verification_uri"],
		"poll_interval_seconds": payload["interval"],
		"expires_in_seconds":    payload["expires_in"],
	})
	session, err := store.CreateFromStart(ctx, in)
	if err != nil {
		return OAuthStartResult{}, err
	}
	waiting, err := store.UpdateStatus(ctx, session.ID, StatusWaitingForUser, "", "")
	if err != nil {
		return OAuthStartResult{}, err
	}
	waiting.AuthType = AuthTypeDeviceCode
	waiting.DeviceCodePayload = payload
	if err := store.SetAuthPayload(ctx, waiting.ID, AuthTypeDeviceCode, payload); err != nil {
		return OAuthStartResult{}, err
	}
	return OAuthStartResult{
		Session: waiting, AuthType: AuthTypeDeviceCode,
		UserCode:                stringFromPayload(payload, "user_code"),
		VerificationURI:         stringFromPayload(payload, "verification_uri"),
		VerificationURIComplete: stringFromPayload(payload, "verification_uri_complete"),
		PollIntervalSeconds:     intFromPayload(payload, "interval"),
		ExpiresInSeconds:        intFromPayload(payload, "expires_in"),
		AuthorizeURL:            firstNonEmpty(stringFromPayload(payload, "verification_uri_complete"), stringFromPayload(payload, "verification_uri")),
	}, nil
}

func normalizeOpenAICodexDeviceStartResponse(resp deviceAuthorizationStartResponse, cfg OAuthClientConfig, issuedAt time.Time) (map[string]any, error) {
	deviceAuthID := firstNonEmpty(resp.DeviceAuthorizationID, resp.DeviceCode, resp.DeviceCodeCamel)
	userCode := firstNonEmpty(resp.UserCode, resp.UserCodeCamel, resp.UserCodeFallback)
	if strings.TrimSpace(deviceAuthID) == "" || strings.TrimSpace(userCode) == "" {
		return nil, fmt.Errorf("%w: openai codex device start response missing required fields", ErrInvalidTokenShape)
	}
	expiresIn := firstPositive(resp.ExpiresIn, resp.ExpiresInCamel)
	if expiresIn == 0 {
		expiresIn = 900
	}
	interval := firstPositive(int(resp.Interval))
	if interval == 0 {
		interval = 5
	}
	deviceTokenURL := strings.TrimSpace(cfg.TokenURL)
	return map[string]any{
		"auth_type":                 string(AuthTypeDeviceCode),
		"device_auth_id":            strings.TrimSpace(deviceAuthID),
		"user_code":                 strings.TrimSpace(userCode),
		"verification_uri":          openAICodexDeviceVerificationURI,
		"verification_uri_complete": openAICodexDeviceVerificationURI,
		"expires_in":                expiresIn,
		"interval":                  interval,
		"issued_at":                 issuedAt.Format(time.RFC3339Nano),
		"token_url":                 deviceTokenURL,
		"oauth_token_url":           resolveOpenAICodexOAuthTokenURL(deviceTokenURL),
		"client_id":                 strings.TrimSpace(cfg.ClientID),
		"redirect_uri":              strings.TrimSpace(firstNonEmpty(cfg.RedirectURI, openAICodexDeviceRedirectURI)),
	}, nil
}

func startDeviceAuthorization(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig, authType AuthType) (OAuthStartResult, error) {
	if store == nil {
		return OAuthStartResult{}, errors.New("credentialacq: session store not configured")
	}
	deviceURL := strings.TrimSpace(cfg.AuthURL)
	tokenURL := strings.TrimSpace(cfg.TokenURL)
	if deviceURL == "" || tokenURL == "" {
		return OAuthStartResult{}, fmt.Errorf("%w: fake %s endpoints required", ErrFeatureDisabled, authType)
	}
	client := defaultDeviceCodeHTTPClient()
	if cfg.HTTPClient != nil {
		client = cfg.HTTPClient
	}
	reqBody := map[string]any{"client_id": strings.TrimSpace(cfg.ClientID)}
	if len(cfg.Scopes) > 0 {
		reqBody["scope"] = strings.Join(cfg.Scopes, " ")
	}
	var response deviceAuthorizationStartResponse
	if err := postJSON(ctx, client, deviceURL, reqBody, &response); err != nil {
		return OAuthStartResult{}, err
	}
	payload, err := normalizeDeviceStartResponse(response, tokenURL, cfg.ClientID, store.now().UTC(), authType)
	if err != nil {
		return OAuthStartResult{}, err
	}
	in.Kind = FlowKindOAuth
	if cfg.Source != "" {
		in.ClientIdentitySource = cfg.Source
	}
	if in.RedirectURI == "" {
		in.RedirectURI = cfg.RedirectURI
	}
	if len(in.RequestedScopes) == 0 {
		in.RequestedScopes = cfg.Scopes
	}
	in.RedactedContext = mergeRedactedContext(in.RedactedContext, map[string]any{
		"auth_type":             string(authType),
		"device_user_display":   payload["user_code"],
		"verification_uri":      payload["verification_uri"],
		"poll_interval_seconds": payload["interval"],
		"expires_in_seconds":    payload["expires_in"],
	})
	session, err := store.CreateFromStart(ctx, in)
	if err != nil {
		return OAuthStartResult{}, err
	}
	waiting, err := store.UpdateStatus(ctx, session.ID, StatusWaitingForUser, "", "")
	if err != nil {
		return OAuthStartResult{}, err
	}
	waiting.AuthType = authType
	waiting.DeviceCodePayload = payload
	if err := store.SetAuthPayload(ctx, waiting.ID, authType, payload); err != nil {
		return OAuthStartResult{}, err
	}
	return OAuthStartResult{
		Session: waiting, AuthType: authType,
		UserCode:                stringFromPayload(payload, "user_code"),
		VerificationURI:         stringFromPayload(payload, "verification_uri"),
		VerificationURIComplete: stringFromPayload(payload, "verification_uri_complete"),
		PollIntervalSeconds:     intFromPayload(payload, "interval"),
		ExpiresInSeconds:        intFromPayload(payload, "expires_in"),
		AuthorizeURL:            firstNonEmpty(stringFromPayload(payload, "verification_uri_complete"), stringFromPayload(payload, "verification_uri")),
	}, nil
}

func normalizeDeviceStartResponse(resp deviceAuthorizationStartResponse, tokenURL, clientID string, issuedAt time.Time, authType AuthType) (map[string]any, error) {
	deviceCode := firstNonEmpty(resp.DeviceCode, resp.DeviceCodeCamel)
	userCode := firstNonEmpty(resp.UserCode, resp.UserCodeCamel, resp.UserCodeFallback)
	verificationURI := firstNonEmpty(resp.VerificationURI, resp.VerificationURICamel, resp.VerificationURIAlt)
	verificationComplete := firstNonEmpty(resp.VerificationComplete, resp.VerificationCompleteGo)
	expiresIn := firstPositive(resp.ExpiresIn, resp.ExpiresInCamel)
	interval := firstPositive(int(resp.Interval))
	if interval == 0 {
		interval = 5
	}
	if expiresIn == 0 {
		expiresIn = 900
	}
	if strings.TrimSpace(deviceCode) == "" || strings.TrimSpace(userCode) == "" || strings.TrimSpace(verificationURI) == "" {
		return nil, fmt.Errorf("%w: %s start response missing required fields", ErrInvalidTokenShape, authType)
	}
	return map[string]any{
		"auth_type":                 string(authType),
		"device_code":               strings.TrimSpace(deviceCode),
		"user_code":                 strings.TrimSpace(userCode),
		"verification_uri":          strings.TrimSpace(verificationURI),
		"verification_uri_complete": strings.TrimSpace(verificationComplete),
		"expires_in":                expiresIn,
		"interval":                  interval,
		"issued_at":                 issuedAt.Format(time.RFC3339Nano),
		"token_url":                 strings.TrimSpace(tokenURL),
		"client_id":                 strings.TrimSpace(clientID),
	}, nil
}

func pollDeviceAuthorizationToken(ctx context.Context, session Session, cfg OAuthClientConfig, authType AuthType, opts ...DeviceCodeOption) (CredentialCandidate, error) {
	options := deviceCodeOptions{client: defaultDeviceCodeHTTPClient(), now: time.Now, sleep: sleepDeviceContext}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	if session.AuthType != "" && session.AuthType != authType {
		return CredentialCandidate{}, fmt.Errorf("%w: session auth_type=%s poller=%s", ErrInvalidTokenShape, session.AuthType, authType)
	}
	payload := session.DeviceCodePayload
	if payload == nil {
		return CredentialCandidate{}, fmt.Errorf("%w: device payload missing", ErrInvalidTokenShape)
	}
	deviceCode := stringFromPayload(payload, "device_code")
	tokenURL := firstNonEmpty(cfg.TokenURL, stringFromPayload(payload, "token_url"))
	clientID := firstNonEmpty(cfg.ClientID, stringFromPayload(payload, "client_id"))
	if deviceCode == "" || tokenURL == "" {
		return CredentialCandidate{}, fmt.Errorf("%w: token poll fields missing", ErrInvalidTokenShape)
	}
	interval := time.Duration(firstPositive(intFromPayload(payload, "interval"), 5)) * time.Second
	expiresAt := issuedAtFromPayload(payload, options.now()).Add(time.Duration(firstPositive(intFromPayload(payload, "expires_in"), 900)) * time.Second)
	for {
		if !options.now().Before(expiresAt) {
			return CredentialCandidate{}, ErrFlowExpired
		}
		var response map[string]any
		status, err := postJSONStatus(ctx, options.client, tokenURL, map[string]any{
			"client_id":   clientID,
			"device_code": deviceCode,
			"deviceCode":  deviceCode,
			"grant_type":  oauthDeviceCodeGrantType,
			"grantType":   oauthDeviceCodeGrantType,
		}, &response)
		if err != nil {
			return CredentialCandidate{}, classifyDevicePollRequestError(err)
		}
		if token := firstNonEmpty(stringField(response, "access_token"), stringField(response, "accessToken")); token != "" {
			raw, err := normalizedTokenPayload(response, token)
			if err != nil {
				return CredentialCandidate{}, err
			}
			return candidateFromDeviceTokenPayload(session, raw), nil
		}
		pollErr := strings.TrimSpace(firstNonEmpty(stringField(response, "error"), stringField(response, "errorCode")))
		switch pollErr {
		case "authorization_pending", "authorizationPending":
		case "slow_down", "slowDown":
			interval += deviceCodeSlowDownStep
		case "access_denied", "accessDenied":
			return CredentialCandidate{}, ErrDeviceAccessDenied
		case "expired_token", "expiredToken":
			return CredentialCandidate{}, ErrFlowExpired
		default:
			if status == http.StatusTooManyRequests || status >= http.StatusInternalServerError {
				return CredentialCandidate{}, ErrDevicePollTransient
			}
			if status < 200 || status >= 300 {
				return CredentialCandidate{}, fmt.Errorf("%w: token poll returned status %d", ErrInvalidTokenShape, status)
			}
			return CredentialCandidate{}, fmt.Errorf("%w: token poll response missing access token", ErrInvalidTokenShape)
		}
		if options.singleAttempt {
			return CredentialCandidate{}, &DevicePollPendingError{RetryAfter: interval}
		}
		if err := options.sleep(ctx, interval); err != nil {
			return CredentialCandidate{}, err
		}
	}
}

func pollOpenAICodexDeviceAuthorizationToken(ctx context.Context, session Session, cfg OAuthClientConfig, opts ...DeviceCodeOption) (CredentialCandidate, error) {
	options := deviceCodeOptions{client: defaultDeviceCodeHTTPClient(), now: time.Now, sleep: sleepDeviceContext}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	payload := session.DeviceCodePayload
	if payload == nil {
		return CredentialCandidate{}, fmt.Errorf("%w: openai codex device payload missing", ErrInvalidTokenShape)
	}
	deviceAuthID := stringFromPayload(payload, "device_auth_id")
	userCode := stringFromPayload(payload, "user_code")
	deviceTokenURL := firstNonEmpty(cfg.TokenURL, stringFromPayload(payload, "token_url"))
	clientID := firstNonEmpty(cfg.ClientID, stringFromPayload(payload, "client_id"))
	if deviceAuthID == "" || userCode == "" || deviceTokenURL == "" || clientID == "" {
		return CredentialCandidate{}, fmt.Errorf("%w: openai codex device poll fields missing", ErrInvalidTokenShape)
	}
	interval := time.Duration(firstPositive(intFromPayload(payload, "interval"), 5)) * time.Second
	expiresAt := issuedAtFromPayload(payload, options.now()).Add(time.Duration(firstPositive(intFromPayload(payload, "expires_in"), 900)) * time.Second)
	for {
		if !options.now().Before(expiresAt) {
			return CredentialCandidate{}, ErrFlowExpired
		}
		var response map[string]any
		status, err := postJSONStatus(ctx, options.client, deviceTokenURL, map[string]any{
			"device_auth_id": deviceAuthID,
			"user_code":      userCode,
		}, &response)
		if err != nil {
			return CredentialCandidate{}, classifyDevicePollRequestError(err)
		}
		pollErr := strings.TrimSpace(firstNonEmpty(stringField(response, "error"), stringField(response, "errorCode")))
		switch pollErr {
		case "authorization_pending", "authorizationPending":
			if options.singleAttempt {
				return CredentialCandidate{}, &DevicePollPendingError{RetryAfter: interval}
			}
			if err := options.sleep(ctx, interval); err != nil {
				return CredentialCandidate{}, err
			}
			continue
		case "slow_down", "slowDown":
			interval += deviceCodeSlowDownStep
			if options.singleAttempt {
				return CredentialCandidate{}, &DevicePollPendingError{RetryAfter: interval}
			}
			if err := options.sleep(ctx, interval); err != nil {
				return CredentialCandidate{}, err
			}
			continue
		case "expired_token", "expiredToken":
			return CredentialCandidate{}, ErrFlowExpired
		case "access_denied", "accessDenied":
			return CredentialCandidate{}, ErrDeviceAccessDenied
		}
		if status >= http.StatusOK && status < http.StatusMultipleChoices {
			if token := firstNonEmpty(stringField(response, "access_token"), stringField(response, "accessToken")); token != "" {
				raw, err := normalizedTokenPayload(response, token)
				if err != nil {
					return CredentialCandidate{}, err
				}
				return candidateFromDeviceTokenPayload(session, raw), nil
			}
			authCode := stringField(response, "authorization_code")
			verifier := stringField(response, "code_verifier")
			if authCode == "" || verifier == "" {
				return CredentialCandidate{}, fmt.Errorf("%w: openai codex device token response missing authorization exchange fields", ErrInvalidTokenShape)
			}
			raw, err := exchangeOpenAICodexAuthorizationCode(ctx, options.client, payload, cfg, authCode, verifier)
			if err != nil {
				return CredentialCandidate{}, err
			}
			return candidateFromDeviceTokenPayload(session, raw), nil
		}
		if status == http.StatusTooManyRequests || status >= http.StatusInternalServerError {
			return CredentialCandidate{}, ErrDevicePollTransient
		}
		if status != http.StatusForbidden && status != http.StatusNotFound {
			return CredentialCandidate{}, fmt.Errorf("%w: openai codex device token poll returned status %d", ErrInvalidTokenShape, status)
		}
		if options.singleAttempt {
			return CredentialCandidate{}, &DevicePollPendingError{RetryAfter: interval}
		}
		if err := options.sleep(ctx, interval); err != nil {
			return CredentialCandidate{}, err
		}
	}
}

func exchangeOpenAICodexAuthorizationCode(ctx context.Context, client *http.Client, payload map[string]any, cfg OAuthClientConfig, authCode, verifier string) ([]byte, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", firstNonEmpty(cfg.ClientID, stringFromPayload(payload, "client_id")))
	form.Set("code", strings.TrimSpace(authCode))
	form.Set("redirect_uri", firstNonEmpty(cfg.RedirectURI, stringFromPayload(payload, "redirect_uri"), openAICodexDeviceRedirectURI))
	form.Set("code_verifier", strings.TrimSpace(verifier))
	exchangeURL := firstNonEmpty(stringFromPayload(payload, "oauth_token_url"), resolveOpenAICodexOAuthTokenURL(firstNonEmpty(cfg.TokenURL, stringFromPayload(payload, "token_url"))))
	var response map[string]any
	if _, err := postFormJSON(ctx, client, exchangeURL, form, &response); err != nil {
		if errors.Is(err, ErrResponseTooLarge) || errors.Is(err, ErrFeatureDisabled) {
			return nil, err
		}
		return nil, ErrDeviceExchangeAmbiguous
	}
	token := firstNonEmpty(stringField(response, "access_token"), stringField(response, "accessToken"))
	if token == "" {
		return nil, fmt.Errorf("%w: openai codex oauth exchange missing access token", ErrInvalidTokenShape)
	}
	return normalizedTokenPayload(response, token)
}
