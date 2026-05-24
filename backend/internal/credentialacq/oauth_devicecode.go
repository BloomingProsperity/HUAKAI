package credentialacq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const deviceCodeSlowDownStep = 5 * time.Second

type deviceCodeExchanger struct{}

func NewDeviceCodeExchanger() Exchanger {
	return deviceCodeExchanger{}
}

func (deviceCodeExchanger) StartOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
	return startDeviceAuthorization(ctx, store, in, cfg, AuthTypeDeviceCode)
}

func (deviceCodeExchanger) ExchangeOAuthCode(context.Context, Session, string) (CredentialCandidate, error) {
	return CredentialCandidate{}, errors.New("credentialacq: device-code flow does not use oauth callback exchange")
}

type DeviceCodeOption func(*deviceCodeOptions)

type deviceCodeOptions struct {
	client *http.Client
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
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
	return pollDeviceAuthorizationToken(ctx, session, cfg, AuthTypeDeviceCode, opts...)
}

type deviceAuthorizationStartResponse struct {
	DeviceCode             string `json:"device_code"`
	DeviceCodeCamel        string `json:"deviceCode"`
	UserCode               string `json:"user_code"`
	UserCodeCamel          string `json:"userCode"`
	VerificationURI        string `json:"verification_uri"`
	VerificationURICamel   string `json:"verificationUri"`
	VerificationURIAlt     string `json:"verification_url"`
	VerificationComplete   string `json:"verification_uri_complete"`
	VerificationCompleteGo string `json:"verificationUriComplete"`
	ExpiresIn              int    `json:"expires_in"`
	ExpiresInCamel         int    `json:"expiresIn"`
	Interval               int    `json:"interval"`
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
	client := http.DefaultClient
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
	userCode := firstNonEmpty(resp.UserCode, resp.UserCodeCamel)
	verificationURI := firstNonEmpty(resp.VerificationURI, resp.VerificationURICamel, resp.VerificationURIAlt)
	verificationComplete := firstNonEmpty(resp.VerificationComplete, resp.VerificationCompleteGo)
	expiresIn := firstPositive(resp.ExpiresIn, resp.ExpiresInCamel)
	interval := firstPositive(resp.Interval)
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
	options := deviceCodeOptions{client: http.DefaultClient, now: time.Now, sleep: sleepDeviceContext}
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
			"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
			"grantType":   "urn:ietf:params:oauth:grant-type:device_code",
		}, &response)
		if err != nil {
			return CredentialCandidate{}, err
		}
		if token := firstNonEmpty(stringField(response, "access_token"), stringField(response, "accessToken")); token != "" {
			raw, err := normalizedTokenPayload(response, token)
			if err != nil {
				return CredentialCandidate{}, err
			}
			return CredentialCandidate{
				TenantID: session.TenantID, ProviderAccountID: session.ProviderAccountID,
				Vendor: session.Vendor, AuthMode: session.AuthMode, Payload: raw, ActorID: session.ActorID,
			}, nil
		}
		pollErr := strings.TrimSpace(firstNonEmpty(stringField(response, "error"), stringField(response, "errorCode")))
		switch pollErr {
		case "authorization_pending", "authorizationPending":
		case "slow_down", "slowDown":
			interval += deviceCodeSlowDownStep
		case "expired_token", "expiredToken":
			return CredentialCandidate{}, ErrFlowExpired
		default:
			if status < 200 || status >= 300 {
				return CredentialCandidate{}, fmt.Errorf("credentialacq: token poll returned status %d", status)
			}
			return CredentialCandidate{}, fmt.Errorf("%w: token poll response missing access token", ErrInvalidTokenShape)
		}
		if err := options.sleep(ctx, interval); err != nil {
			return CredentialCandidate{}, err
		}
	}
}

func postJSON(ctx context.Context, client *http.Client, url string, body map[string]any, out any) error {
	status, err := postJSONStatus(ctx, client, url, body, out)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("credentialacq: endpoint returned status %d", status)
	}
	return nil
}

func postJSONStatus(ctx context.Context, client *http.Client, url string, body map[string]any, out any) (int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, err
	}
	if len(respBody) > 0 && out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func normalizedTokenPayload(response map[string]any, accessToken string) ([]byte, error) {
	payload := map[string]any{"access_token": strings.TrimSpace(accessToken)}
	for _, key := range []string{"refresh_token", "refreshToken", "token_type", "tokenType", "expires_in", "expiresIn"} {
		if value, ok := response[key]; ok {
			payload[key] = value
		}
	}
	return json.Marshal(payload)
}

func mergeRedactedContext(base map[string]any, extra map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		if strings.TrimSpace(k) != "" && v != nil {
			out[k] = v
		}
	}
	return out
}

func issuedAtFromPayload(payload map[string]any, fallback time.Time) time.Time {
	if raw := stringFromPayload(payload, "issued_at"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			return parsed
		}
	}
	return fallback
}

func stringFromPayload(payload map[string]any, key string) string {
	return stringField(payload, key)
}

func intFromPayload(payload map[string]any, key string) int {
	if payload == nil {
		return 0
	}
	switch value := payload[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		n, _ := value.Int64()
		return int(n)
	case string:
		var n int
		_, _ = fmt.Sscanf(strings.TrimSpace(value), "%d", &n)
		return n
	default:
		return 0
	}
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func sleepDeviceContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
