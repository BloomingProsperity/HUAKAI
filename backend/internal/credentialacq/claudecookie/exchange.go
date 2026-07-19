// Package claudecookie 把短期 Claude 网页会话转换为账号接入候选数据。
package claudecookie

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const (
	organizationsURL = "https://claude.ai/api/organizations"
	authorizeBaseURL = "https://claude.ai/v1/oauth/"
	tokenURL         = "https://platform.claude.com/v1/oauth/token"
	redirectURI      = "https://platform.claude.com/oauth/code/callback"
	publicClientID   = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	fullScope        = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	setupScope       = "user:inference"
	maxSessionBytes  = 16 << 10
	maxResponseBytes = 1 << 20
)

var (
	ErrNotConfigured              = errors.New("claude cookie: HTTP client not configured")
	ErrInvalidSession             = errors.New("claude cookie: invalid session")
	ErrOrganizationChoiceRequired = errors.New("claude cookie: organization choice required")
	ErrOrganizationNotFound       = errors.New("claude cookie: organization not found")
	ErrUpstreamRejected           = errors.New("claude cookie: upstream rejected request")
	ErrInvalidResponse            = errors.New("claude cookie: invalid upstream response")
)

type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	Kind string `json:"kind,omitempty"`
}

type OrganizationChoiceError struct {
	Organizations []Organization
}

func (e *OrganizationChoiceError) Error() string { return ErrOrganizationChoiceRequired.Error() }
func (e *OrganizationChoiceError) Unwrap() error { return ErrOrganizationChoiceRequired }

type Input struct {
	SessionKey     string
	OrganizationID string
	SetupToken     bool
}

type Result struct {
	ImportContent  string
	OrganizationID string
	AccountID      string
	Email          string
	AuthMode       string
}

type Exchanger struct {
	client *http.Client
	now    func() time.Time
}

func New(client *http.Client) *Exchanger {
	return &Exchanger{client: lockedClient(client)}
}

func (e *Exchanger) Exchange(ctx context.Context, in Input) (Result, error) {
	sessionKey := strings.TrimSpace(in.SessionKey)
	if sessionKey == "" || len(sessionKey) > maxSessionBytes {
		return Result{}, ErrInvalidSession
	}
	defer privacy.Zeroize([]byte(sessionKey))
	if e == nil || e.client == nil {
		return Result{}, ErrNotConfigured
	}

	organizations, err := e.organizations(ctx, sessionKey)
	if err != nil {
		return Result{}, err
	}
	organization, err := chooseOrganization(organizations, in.OrganizationID)
	if err != nil {
		return Result{}, err
	}
	verifier, challenge, state, err := newPKCE()
	if err != nil {
		return Result{}, err
	}
	code, err := e.authorizationCode(ctx, sessionKey, organization.ID, scopeFor(in.SetupToken), challenge, state)
	if err != nil {
		return Result{}, err
	}
	token, err := e.exchangeCode(ctx, code, verifier, state)
	if err != nil {
		return Result{}, err
	}
	defer privacy.Zeroize([]byte(token.AccessToken))
	defer privacy.Zeroize([]byte(token.RefreshToken))

	content, mode, err := buildImportContent(token, organization.ID, in.SetupToken, e.nowTime())
	if err != nil {
		return Result{}, err
	}
	return Result{
		ImportContent: content, OrganizationID: organization.ID,
		AccountID: strings.TrimSpace(token.Account.UUID),
		Email:     strings.TrimSpace(firstNonEmpty(token.Account.EmailAddress, token.Email)),
		AuthMode:  mode,
	}, nil
}

func (e *Exchanger) organizations(ctx context.Context, sessionKey string) ([]Organization, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, organizationsURL, nil)
	if err != nil {
		return nil, err
	}
	req.AddCookie(&http.Cookie{Name: "sessionKey", Value: sessionKey, Secure: true, HttpOnly: true})
	req.Header.Set("Accept", "application/json")
	resp, body, err := e.do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: organizations status %d", ErrUpstreamRejected, resp.StatusCode)
	}
	var rows []struct {
		UUID      string  `json:"uuid"`
		Name      string  `json:"name"`
		RavenType *string `json:"raven_type"`
	}
	if json.Unmarshal(body, &rows) != nil {
		return nil, ErrInvalidResponse
	}
	out := make([]Organization, 0, len(rows))
	seen := map[string]struct{}{}
	for _, row := range rows {
		id := strings.TrimSpace(row.UUID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		kind := ""
		if row.RavenType != nil {
			kind = strings.TrimSpace(*row.RavenType)
		}
		out = append(out, Organization{ID: id, Name: strings.TrimSpace(row.Name), Kind: kind})
	}
	if len(out) == 0 {
		return nil, ErrInvalidSession
	}
	return out, nil
}

func chooseOrganization(organizations []Organization, requested string) (Organization, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		for _, organization := range organizations {
			if organization.ID == requested {
				return organization, nil
			}
		}
		return Organization{}, ErrOrganizationNotFound
	}
	if len(organizations) == 1 {
		return organizations[0], nil
	}
	return Organization{}, &OrganizationChoiceError{Organizations: append([]Organization(nil), organizations...)}
}

func (e *Exchanger) authorizationCode(ctx context.Context, sessionKey, organizationID, scope, challenge, state string) (string, error) {
	body, err := json.Marshal(map[string]string{
		"response_type": "code", "client_id": publicClientID,
		"organization_uuid": organizationID, "redirect_uri": redirectURI,
		"scope": scope, "state": state, "code_challenge": challenge,
		"code_challenge_method": "S256",
	})
	if err != nil {
		return "", err
	}
	endpoint := authorizeBaseURL + url.PathEscape(organizationID) + "/authorize"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.AddCookie(&http.Cookie{Name: "sessionKey", Value: sessionKey, Secure: true, HttpOnly: true})
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://claude.ai")
	req.Header.Set("Referer", "https://claude.ai/new")
	resp, raw, err := e.do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%w: authorize status %d", ErrUpstreamRejected, resp.StatusCode)
	}
	var result struct {
		RedirectURI string `json:"redirect_uri"`
	}
	if json.Unmarshal(raw, &result) != nil || strings.TrimSpace(result.RedirectURI) == "" {
		return "", ErrInvalidResponse
	}
	callback, err := url.Parse(result.RedirectURI)
	if err != nil || callback.Scheme != "https" || callback.Host != "platform.claude.com" || callback.Path != "/oauth/code/callback" {
		return "", ErrInvalidResponse
	}
	if callback.Query().Get("state") != state {
		return "", ErrInvalidResponse
	}
	code := strings.TrimSpace(callback.Query().Get("code"))
	if code == "" {
		return "", ErrInvalidResponse
	}
	return code, nil
}

type tokenResponse struct {
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	TokenType    string          `json:"token_type"`
	Scope        string          `json:"scope"`
	ExpiresIn    json.RawMessage `json:"expires_in"`
	Account      struct {
		UUID         string `json:"uuid"`
		EmailAddress string `json:"email_address"`
	} `json:"account"`
	Email string `json:"email"`
}

func (e *Exchanger) exchangeCode(ctx context.Context, code, verifier, state string) (tokenResponse, error) {
	body, err := json.Marshal(map[string]string{
		"code": code, "grant_type": "authorization_code", "client_id": publicClientID,
		"redirect_uri": redirectURI, "code_verifier": verifier, "state": state,
	})
	if err != nil {
		return tokenResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(body))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, raw, err := e.do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tokenResponse{}, fmt.Errorf("%w: token status %d", ErrUpstreamRejected, resp.StatusCode)
	}
	var token tokenResponse
	if json.Unmarshal(raw, &token) != nil || strings.TrimSpace(token.AccessToken) == "" {
		return tokenResponse{}, ErrInvalidResponse
	}
	return token, nil
}

func (e *Exchanger) do(req *http.Request) (*http.Response, []byte, error) {
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(body) > maxResponseBytes {
		return nil, nil, ErrInvalidResponse
	}
	return resp, body, nil
}

func lockedClient(base *http.Client) *http.Client {
	if base == nil {
		return nil
	}
	client := *base
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Timeout <= 0 || client.Timeout > 60*time.Second {
		client.Timeout = 60 * time.Second
	}
	return &client
}

func newPKCE() (verifier, challenge, state string, err error) {
	verifier, err = randomToken(32)
	if err != nil {
		return "", "", "", err
	}
	state, err = randomToken(32)
	if err != nil {
		return "", "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, state, nil
}

func randomToken(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func scopeFor(setup bool) string {
	if setup {
		return setupScope
	}
	return fullScope
}

func buildImportContent(token tokenResponse, organizationID string, setup bool, now time.Time) (string, string, error) {
	fields := map[string]any{
		"vendor": "anthropic", "organization_id": strings.TrimSpace(organizationID),
	}
	accountID := strings.TrimSpace(token.Account.UUID)
	email := strings.TrimSpace(firstNonEmpty(token.Account.EmailAddress, token.Email))
	if accountID != "" {
		fields["external_account_id"] = accountID
	}
	if email != "" {
		fields["external_account_email"] = email
	}
	mode := "claude_ai_oauth"
	if setup {
		mode = "claude_setup_token"
		fields["setup_token"] = strings.TrimSpace(token.AccessToken)
	} else {
		if strings.TrimSpace(token.RefreshToken) == "" {
			return "", "", ErrInvalidResponse
		}
		fields["access_token"] = strings.TrimSpace(token.AccessToken)
		fields["refresh_token"] = strings.TrimSpace(token.RefreshToken)
		if tokenType := strings.TrimSpace(token.TokenType); tokenType != "" {
			fields["token_type"] = tokenType
		}
		if scope := strings.TrimSpace(token.Scope); scope != "" {
			fields["scope"] = scope
		}
		if expiresIn := parseExpiresIn(token.ExpiresIn); expiresIn > 0 {
			fields["expires_at"] = now.UTC().Add(time.Duration(expiresIn) * time.Second).Format(time.RFC3339)
		}
	}
	fields["auth_mode"] = mode
	raw, err := json.Marshal(fields)
	if err != nil {
		return "", "", err
	}
	return string(raw), mode, nil
}

func parseExpiresIn(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var number int64
	if json.Unmarshal(raw, &number) == nil {
		return number
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		number, _ = strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	}
	return number
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func (e *Exchanger) nowTime() time.Time {
	if e != nil && e.now != nil {
		return e.now().UTC()
	}
	return time.Now().UTC()
}
