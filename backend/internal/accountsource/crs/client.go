// Package crs 实现受限的兼容账号源连接器。
package crs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/accountsource"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

const (
	AllowedHostsEnv   = "HUAKAI_CRS_ALLOWED_HOSTS"
	DefaultLoginPath  = "/api/auth/login"
	DefaultExportPath = "/api/admin/accounts/export"
	responseLimit     = 4 << 20
)

var (
	ErrDisabled          = errors.New("crs connector host is not allowlisted")
	ErrEndpointBlocked   = errors.New("crs connector endpoint blocked")
	ErrUpstreamRejected  = errors.New("crs connector upstream rejected request")
	ErrResponseMalformed = errors.New("crs connector response malformed")
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type Client struct {
	http     HTTPDoer
	resolver Resolver
	allowed  map[string]struct{}
	now      func() time.Time
}

type FetchInput struct {
	BaseURL    string
	LoginPath  string
	ExportPath string
	Username   string
	Password   string
}

func NewClient(allowedHosts []string) *Client {
	base := &http.Client{Timeout: 30 * time.Second}
	return &Client{
		http: auth.NewSSRFProtectedOAuthClient(base), resolver: net.DefaultResolver,
		allowed: normalizedHosts(allowedHosts), now: time.Now,
	}
}

func (c *Client) Fetch(ctx context.Context, in FetchInput) ([]accountsource.Item, map[string]any, error) {
	base, err := c.validateEndpoint(ctx, in.BaseURL)
	if err != nil {
		return nil, nil, err
	}
	loginPath, err := restrictedPath(in.LoginPath, DefaultLoginPath)
	if err != nil {
		return nil, nil, err
	}
	exportPath, err := restrictedPath(in.ExportPath, DefaultExportPath)
	if err != nil {
		return nil, nil, err
	}
	username := strings.TrimSpace(in.Username)
	if username == "" || in.Password == "" || len(username) > 320 || len(in.Password) > 16<<10 {
		return nil, nil, ErrResponseMalformed
	}
	token, err := c.login(ctx, endpointURL(base, loginPath), username, in.Password)
	in.Password = ""
	if err != nil {
		return nil, nil, err
	}
	defer clearString(&token)
	items, err := c.export(ctx, endpointURL(base, exportPath), token)
	if err != nil {
		return nil, nil, err
	}
	return items, map[string]any{
		"source_host": base.Hostname(), "login_path": loginPath, "export_path": exportPath,
	}, nil
}

func (c *Client) login(ctx context.Context, endpoint, username, password string) (string, error) {
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return "", err
	}
	defer privacy.Zeroize(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := c.do(req)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", ErrUpstreamRejected
	}
	raw, err := readBounded(response.Body)
	if err != nil {
		return "", err
	}
	defer privacy.Zeroize(raw)
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		return "", ErrResponseMalformed
	}
	token := findString(decoded, "access_token", "accessToken", "token")
	if token == "" || len(token) > 64<<10 {
		return "", ErrResponseMalformed
	}
	return token, nil
}

func (c *Client) export(ctx context.Context, endpoint, token string) ([]accountsource.Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := c.do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, ErrUpstreamRejected
	}
	raw, err := readBounded(response.Body)
	if err != nil {
		return nil, err
	}
	defer privacy.Zeroize(raw)
	return parseItems(raw)
}

func (c *Client) do(request *http.Request) (*http.Response, error) {
	if c == nil || c.http == nil {
		return nil, ErrDisabled
	}
	return c.http.Do(request)
}

func (c *Client) validateEndpoint(ctx context.Context, raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, ErrEndpointBlocked
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, ErrEndpointBlocked
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if _, ok := c.allowed[host]; !ok || len(c.allowed) == 0 {
		return nil, ErrDisabled
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return nil, ErrEndpointBlocked
	}
	if c.resolver == nil {
		return nil, ErrEndpointBlocked
	}
	addresses, err := c.resolver.LookupIPAddr(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, ErrEndpointBlocked
	}
	for _, address := range addresses {
		if !publicIP(address.IP) {
			return nil, ErrEndpointBlocked
		}
	}
	parsed.Host = host
	return parsed, nil
}

func parseItems(raw []byte) ([]accountsource.Item, error) {
	var decoded any
	if json.Unmarshal(raw, &decoded) != nil {
		return nil, ErrResponseMalformed
	}
	rows := findArray(decoded, "accounts", "items", "data")
	if len(rows) == 0 || len(rows) > accountsource.MaxItems {
		return nil, ErrResponseMalformed
	}
	items := make([]accountsource.Item, 0, len(rows))
	complete := false
	defer func() {
		if !complete {
			accountsource.ZeroizeItems(items)
		}
	}()
	for _, row := range rows {
		object, ok := row.(map[string]any)
		if !ok {
			return nil, ErrResponseMalformed
		}
		vendor := credentialstore.Normalize(firstString(object, "vendor", "platform", "provider"))
		authMode := credentialstore.Normalize(firstString(object, "auth_mode", "authMode", "type"))
		credentialObject := firstObject(object, "credentials", "credential", "auth")
		if credentialObject == nil || vendor == "" || authMode == "" {
			return nil, ErrResponseMalformed
		}
		payload, err := json.Marshal(credentialObject)
		if err != nil {
			return nil, ErrResponseMalformed
		}
		candidate := credentialacq.CredentialCandidate{
			Vendor: vendor, AuthMode: authMode, Payload: payload,
			RedactedContext: map[string]any{"shape": "remote_account_source"},
		}
		credentialacq.AttachIdentity(&candidate, accountident.Identity{
			AccountID: firstString(object, "external_account_id", "account_id", "accountId"),
			SubjectID: firstString(object, "external_subject_id", "user_id", "userId"),
			Email:     firstString(object, "external_account_email", "email"),
			Source:    accountident.SourceImportPayload,
		})
		items = append(items, accountsource.Item{Template: accountsource.AccountTemplate{
			Name: firstString(object, "name", "display_name"), SourceProvider: firstString(object, "provider_code", "provider"),
			AccountType: firstNonEmpty(firstString(object, "account_type", "accountType"), accountTypeFor(authMode)),
			Enabled:     boolValue(object["enabled"], true),
		}, Candidate: candidate})
	}
	complete = true
	return items, nil
}

func readBounded(reader io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, responseLimit+1))
	if err != nil || len(raw) > responseLimit {
		return nil, ErrResponseMalformed
	}
	return raw, nil
}

func restrictedPath(raw, fallback string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = fallback
	}
	parsed, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(parsed.Path, "/api/") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Host != "" || parsed.Scheme != "" || path.Clean(parsed.Path) != parsed.Path {
		return "", ErrEndpointBlocked
	}
	return parsed.Path, nil
}

func endpointURL(base *url.URL, endpointPath string) string {
	copy := *base
	copy.Path = endpointPath
	return copy.String()
}

func publicIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	return ip.IsGlobalUnicast()
}

func normalizedHosts(hosts []string) map[string]struct{} {
	out := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
		if host != "" && !strings.ContainsAny(host, "/:@* ") {
			out[host] = struct{}{}
		}
	}
	return out
}

func findString(value any, names ...string) string {
	if object, ok := value.(map[string]any); ok {
		if direct := firstString(object, names...); direct != "" {
			return direct
		}
		for _, key := range []string{"data", "result"} {
			if nested, exists := object[key]; exists {
				if found := findString(nested, names...); found != "" {
					return found
				}
			}
		}
	}
	return ""
}

func findArray(value any, names ...string) []any {
	if array, ok := value.([]any); ok {
		return array
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	for _, name := range names {
		if array, ok := object[name].([]any); ok {
			return array
		}
		if nested, ok := object[name].(map[string]any); ok {
			if array := findArray(nested, names...); len(array) > 0 {
				return array
			}
		}
	}
	return nil
}

func firstObject(object map[string]any, names ...string) map[string]any {
	for _, name := range names {
		if value, ok := object[name].(map[string]any); ok {
			return value
		}
	}
	return nil
}

func firstString(object map[string]any, names ...string) string {
	for _, name := range names {
		if value, ok := object[name].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func boolValue(value any, fallback bool) bool {
	if parsed, ok := value.(bool); ok {
		return parsed
	}
	return fallback
}

func accountTypeFor(authMode string) string {
	switch credentialstore.Normalize(authMode) {
	case "api_key", "official_api_key", "azure_api_key":
		return "api_key"
	case "service_account", "vertex_service_account":
		return "service_account"
	default:
		return "oauth"
	}
}

func clearString(value *string) {
	if value != nil {
		*value = ""
	}
}
