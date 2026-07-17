// Package claudecookie 把短时网页会话转换为标准 Claude OAuth 候选凭据。
package claudecookie

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	claudeWebBaseURL = "https://claude.ai"
	claudeTokenURL   = "https://platform.claude.com/v1/oauth/token"
	claudeRedirect   = "https://platform.claude.com/oauth/code/callback"
	claudeClientID   = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeFullScope  = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	maxResponseBytes = 1 << 20
	maxSessionKeyLen = 16 << 10
	exchangeTimeout  = 30 * time.Second
)

var (
	ErrInvalidInput                  = errors.New("claude cookie input invalid")
	ErrUpstreamUnauthorized          = errors.New("claude cookie rejected by upstream")
	ErrUpstreamUnavailable           = errors.New("claude cookie upstream unavailable")
	ErrOrganizationSelectionRequired = errors.New("claude organization selection required")
)

type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

type OrganizationSelectionError struct {
	Organizations []Organization
}

func (e *OrganizationSelectionError) Error() string {
	return ErrOrganizationSelectionRequired.Error()
}

func (e *OrganizationSelectionError) Unwrap() error {
	return ErrOrganizationSelectionRequired
}

type ExchangeResult struct {
	AccessToken         string
	RefreshToken        string
	TokenType           string
	Scope               string
	ExpiresIn           json.RawMessage
	AccountUUID         string
	AccountEmailAddress string
	Email               string
	Organization        Organization
}

type Client struct {
	httpClient *http.Client
	webBaseURL string
	tokenURL   string
	redirect   string
}

func NewClient(httpClient *http.Client) *Client {
	return &Client{httpClient: httpClient, webBaseURL: claudeWebBaseURL, tokenURL: claudeTokenURL, redirect: claudeRedirect}
}

func (c *Client) Exchange(ctx context.Context, rawSessionKey, requestedOrganizationID string) (ExchangeResult, error) {
	if c == nil || c.httpClient == nil {
		return ExchangeResult{}, ErrUpstreamUnavailable
	}
	sessionKey := strings.TrimSpace(rawSessionKey)
	if sessionKey == "" || len(sessionKey) > maxSessionKeyLen {
		return ExchangeResult{}, fmt.Errorf("%w: session key is empty or too large", ErrInvalidInput)
	}
	cookie := &http.Cookie{Name: "sessionKey", Value: sessionKey, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode}
	if err := cookie.Valid(); err != nil {
		return ExchangeResult{}, fmt.Errorf("%w: session key cannot be encoded as a cookie", ErrInvalidInput)
	}
	organizations, err := c.listOrganizations(ctx, cookie)
	if err != nil {
		return ExchangeResult{}, err
	}
	organization, err := selectOrganization(organizations, requestedOrganizationID)
	if err != nil {
		return ExchangeResult{}, err
	}
	verifier, challenge, state, err := newPKCEValues()
	if err != nil {
		return ExchangeResult{}, ErrUpstreamUnavailable
	}
	code, err := c.authorize(ctx, cookie, organization.ID, challenge, state)
	if err != nil {
		return ExchangeResult{}, err
	}
	result, err := c.exchangeCode(ctx, code, verifier, state)
	if err != nil {
		return ExchangeResult{}, err
	}
	result.Organization = organization
	return result, nil
}

func (c *Client) listOrganizations(ctx context.Context, cookie *http.Cookie) ([]Organization, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.webBaseURL, "/")+"/api/organizations", nil)
	if err != nil {
		return nil, ErrUpstreamUnavailable
	}
	req.Header.Set("Accept", "application/json")
	req.AddCookie(cookie)
	resp, err := c.do(req)
	if err != nil {
		return nil, ErrUpstreamUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ErrUpstreamUnauthorized
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, ErrUpstreamUnavailable
	}
	var wire []struct {
		UUID      string  `json:"uuid"`
		Name      string  `json:"name"`
		RavenType *string `json:"raven_type"`
	}
	if err := decodeBoundedJSON(resp.Body, &wire); err != nil {
		return nil, ErrUpstreamUnavailable
	}
	organizations := make([]Organization, 0, len(wire))
	seen := make(map[string]struct{}, len(wire))
	for _, item := range wire {
		id := strings.TrimSpace(item.UUID)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			return nil, ErrUpstreamUnavailable
		}
		seen[id] = struct{}{}
		kind := ""
		if item.RavenType != nil {
			kind = strings.TrimSpace(*item.RavenType)
		}
		organizations = append(organizations, Organization{ID: id, Name: strings.TrimSpace(item.Name), Type: kind})
	}
	if len(organizations) == 0 {
		return nil, ErrUpstreamUnauthorized
	}
	return organizations, nil
}

func selectOrganization(organizations []Organization, requestedID string) (Organization, error) {
	requestedID = strings.TrimSpace(requestedID)
	if requestedID == "" {
		if len(organizations) == 1 {
			return organizations[0], nil
		}
		return Organization{}, &OrganizationSelectionError{Organizations: append([]Organization(nil), organizations...)}
	}
	var selected *Organization
	for index := range organizations {
		if organizations[index].ID != requestedID {
			continue
		}
		if selected != nil {
			return Organization{}, ErrUpstreamUnavailable
		}
		copy := organizations[index]
		selected = &copy
	}
	if selected == nil {
		return Organization{}, fmt.Errorf("%w: organization is not available to this session", ErrInvalidInput)
	}
	return *selected, nil
}

func (c *Client) authorize(ctx context.Context, cookie *http.Cookie, organizationID, challenge, state string) (string, error) {
	body, err := json.Marshal(map[string]string{
		"response_type": "code", "client_id": claudeClientID,
		"organization_uuid": organizationID, "redirect_uri": c.redirect,
		"scope": claudeFullScope, "state": state,
		"code_challenge": challenge, "code_challenge_method": "S256",
	})
	if err != nil {
		return "", ErrUpstreamUnavailable
	}
	target := strings.TrimRight(c.webBaseURL, "/") + "/v1/oauth/" + url.PathEscape(organizationID) + "/authorize"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return "", ErrUpstreamUnavailable
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", strings.TrimRight(c.webBaseURL, "/"))
	req.Header.Set("Referer", strings.TrimRight(c.webBaseURL, "/")+"/new")
	req.AddCookie(cookie)
	resp, err := c.do(req)
	if err != nil {
		return "", ErrUpstreamUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", ErrUpstreamUnauthorized
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", ErrUpstreamUnavailable
	}
	var response struct {
		RedirectURI string `json:"redirect_uri"`
	}
	if err := decodeBoundedJSON(resp.Body, &response); err != nil {
		return "", ErrUpstreamUnavailable
	}
	redirect, err := url.Parse(strings.TrimSpace(response.RedirectURI))
	if err != nil || redirect.Scheme != "https" || !strings.EqualFold(redirect.Host, "platform.claude.com") ||
		redirect.User != nil || redirect.EscapedPath() != "/oauth/code/callback" || redirect.Fragment != "" {
		return "", ErrUpstreamUnavailable
	}
	code := strings.TrimSpace(redirect.Query().Get("code"))
	responseState := strings.TrimSpace(redirect.Query().Get("state"))
	if code == "" || responseState == "" || subtle.ConstantTimeCompare([]byte(responseState), []byte(state)) != 1 {
		return "", ErrUpstreamUnavailable
	}
	return code, nil
}

func (c *Client) exchangeCode(ctx context.Context, code, verifier, state string) (ExchangeResult, error) {
	body, err := json.Marshal(map[string]string{
		"grant_type": "authorization_code", "code": code, "client_id": claudeClientID,
		"redirect_uri": c.redirect, "code_verifier": verifier, "state": state,
	})
	if err != nil {
		return ExchangeResult{}, ErrUpstreamUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, bytes.NewReader(body))
	if err != nil {
		return ExchangeResult{}, ErrUpstreamUnavailable
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req)
	if err != nil {
		return ExchangeResult{}, ErrUpstreamUnavailable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusBadRequest {
		return ExchangeResult{}, ErrUpstreamUnauthorized
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return ExchangeResult{}, ErrUpstreamUnavailable
	}
	var token struct {
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
	if err := decodeBoundedJSON(resp.Body, &token); err != nil {
		return ExchangeResult{}, ErrUpstreamUnavailable
	}
	if strings.TrimSpace(token.AccessToken) == "" || strings.TrimSpace(token.RefreshToken) == "" {
		return ExchangeResult{}, ErrUpstreamUnavailable
	}
	return ExchangeResult{
		AccessToken: strings.TrimSpace(token.AccessToken), RefreshToken: strings.TrimSpace(token.RefreshToken),
		TokenType: strings.TrimSpace(token.TokenType), Scope: strings.TrimSpace(token.Scope), ExpiresIn: append(json.RawMessage(nil), token.ExpiresIn...),
		AccountUUID: strings.TrimSpace(token.Account.UUID), AccountEmailAddress: strings.TrimSpace(token.Account.EmailAddress), Email: strings.TrimSpace(token.Email),
	}, nil
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	client := *c.httpClient
	if client.Timeout <= 0 || client.Timeout > exchangeTimeout {
		client.Timeout = exchangeTimeout
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client.Do(req)
}

func newPKCEValues() (verifier, challenge, state string, err error) {
	verifierBytes := make([]byte, 32)
	stateBytes := make([]byte, 32)
	if _, err = rand.Read(verifierBytes); err != nil {
		return "", "", "", err
	}
	if _, err = rand.Read(stateBytes); err != nil {
		return "", "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(verifierBytes)
	digest := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(digest[:])
	state = base64.RawURLEncoding.EncodeToString(stateBytes)
	return verifier, challenge, state, nil
}

func decodeBoundedJSON(reader io.Reader, dst any) error {
	raw, err := io.ReadAll(io.LimitReader(reader, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes {
		return ErrUpstreamUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrUpstreamUnavailable
	}
	return nil
}
