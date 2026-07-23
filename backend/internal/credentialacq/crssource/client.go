// Package crssource 从受控 CRS 端点读取账号导出并归一成中立账号事实。
package crssource

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

const (
	maxLoginResponseBytes  = 1 << 20
	maxExportResponseBytes = 8 << 20
	maxExportAccounts      = 500
	requestTimeout         = 20 * time.Second
)

var (
	ErrNotConfigured    = errors.New("crs source: not configured")
	ErrInvalidInput     = errors.New("crs source: invalid input")
	ErrEndpointDenied   = errors.New("crs source: endpoint denied")
	ErrAuthentication   = errors.New("crs source: authentication failed")
	ErrUpstream         = errors.New("crs source: upstream failed")
	ErrResponseInvalid  = errors.New("crs source: response invalid")
	ErrResponseTooLarge = errors.New("crs source: response too large")
	ErrTooManyAccounts  = errors.New("crs source: too many accounts")
)

type HostLookup func(context.Context, string) ([]netip.Addr, error)

type Policy struct {
	AllowedHosts      []string
	AllowPrivateHosts bool
	AllowInsecureHTTP bool
}

type Input struct {
	BaseURL  string
	Username string
	Password string
}

type Proxy struct {
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int32  `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type Account struct {
	SourceType  string
	SourceID    string
	Name        string
	Vendor      string
	AuthMode    string
	AccountType string
	Enabled     bool
	Schedulable bool
	Priority    int32
	Concurrency int32
	Credentials map[string]any
	Extra       map[string]any
	Proxy       *Proxy
	Warnings    []string
	InvalidCode string
}

type Export struct {
	BaseURL  string
	Accounts []Account
}

type Client struct {
	http   *http.Client
	policy Policy
	lookup HostLookup
}

func New(client *http.Client, policy Policy) *Client {
	return &Client{http: lockedClient(client), policy: normalizePolicy(policy), lookup: lookupHost}
}

func (c *Client) Fetch(ctx context.Context, in Input) (Export, error) {
	if c == nil || c.http == nil || c.lookup == nil || len(c.policy.AllowedHosts) == 0 {
		return Export{}, ErrNotConfigured
	}
	baseURL, host, err := validateBaseURL(in.BaseURL, c.policy)
	if err != nil {
		return Export{}, err
	}
	if strings.TrimSpace(in.Username) == "" || strings.TrimSpace(in.Password) == "" || len(in.Username) > 1024 || len(in.Password) > 16<<10 {
		return Export{}, ErrInvalidInput
	}
	if err := c.validateResolvedHost(ctx, host); err != nil {
		return Export{}, err
	}
	token, err := c.login(ctx, baseURL, in.Username, in.Password)
	if err != nil {
		return Export{}, err
	}
	if err := c.validateResolvedHost(ctx, host); err != nil {
		return Export{}, err
	}
	wire, err := c.export(ctx, baseURL, token)
	if err != nil {
		return Export{}, err
	}
	accounts, err := normalizeExport(wire)
	if err != nil {
		return Export{}, err
	}
	return Export{BaseURL: baseURL, Accounts: accounts}, nil
}

func (c *Client) login(ctx context.Context, baseURL, username, password string) (string, error) {
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		return "", ErrInvalidInput
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/web/auth/login", bytes.NewReader(body))
	if err != nil {
		return "", ErrInvalidInput
	}
	req.Header.Set("Content-Type", "application/json")
	var response struct {
		Success bool   `json:"success"`
		Token   string `json:"token"`
	}
	status, err := c.doJSON(req, maxLoginResponseBytes, &response)
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 || !response.Success || strings.TrimSpace(response.Token) == "" {
		return "", ErrAuthentication
	}
	return strings.TrimSpace(response.Token), nil
}

func (c *Client) export(ctx context.Context, baseURL, token string) (exportResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/admin/sync/export-accounts?include_secrets=true", nil)
	if err != nil {
		return exportResponse{}, ErrInvalidInput
	}
	req.Header.Set("Authorization", "Bearer "+token)
	var response exportResponse
	status, err := c.doJSON(req, maxExportResponseBytes, &response)
	if err != nil {
		return exportResponse{}, err
	}
	if status < 200 || status >= 300 || !response.Success {
		return exportResponse{}, ErrUpstream
	}
	return response, nil
}

func (c *Client) doJSON(req *http.Request, limit int64, dst any) (int, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, ErrUpstream
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return resp.StatusCode, ErrUpstream
	}
	if int64(len(raw)) > limit {
		return resp.StatusCode, ErrResponseTooLarge
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, nil
	}
	if json.Unmarshal(raw, dst) != nil {
		return resp.StatusCode, ErrResponseInvalid
	}
	return resp.StatusCode, nil
}

func (c *Client) validateResolvedHost(ctx context.Context, host string) error {
	_, err := resolveAllowedHost(ctx, c.policy, c.lookup, host)
	return err
}

func resolveAllowedHost(ctx context.Context, policy Policy, lookup HostLookup, host string) ([]netip.Addr, error) {
	policy = normalizePolicy(policy)
	host = strings.ToLower(strings.TrimSpace(host))
	if lookup == nil || host == "" || !slices.Contains(policy.AllowedHosts, host) {
		return nil, ErrEndpointDenied
	}
	addresses, err := lookup(ctx, host)
	if err != nil || len(addresses) == 0 {
		return nil, ErrEndpointDenied
	}
	result := make([]netip.Addr, 0, len(addresses))
	seen := make(map[netip.Addr]struct{}, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if !address.IsValid() || (!policy.AllowPrivateHosts && !publicAddress(address)) {
			return nil, ErrEndpointDenied
		}
		if _, ok := seen[address]; ok {
			continue
		}
		seen[address] = struct{}{}
		result = append(result, address)
	}
	if len(result) == 0 {
		return nil, ErrEndpointDenied
	}
	return result, nil
}

func validateBaseURL(raw string, policy Policy) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", ErrEndpointDenied
	}
	if parsed.Scheme != "https" && !(policy.AllowInsecureHTTP && parsed.Scheme == "http") {
		return "", "", ErrEndpointDenied
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", "", ErrEndpointDenied
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" || !slices.Contains(policy.AllowedHosts, host) {
		return "", "", ErrEndpointDenied
	}
	parsed.Path = ""
	return strings.TrimSuffix(parsed.String(), "/"), host, nil
}

func normalizePolicy(in Policy) Policy {
	out := in
	out.AllowedHosts = make([]string, 0, len(in.AllowedHosts))
	seen := map[string]struct{}{}
	for _, host := range in.AllowedHosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			continue
		}
		if parsed, err := url.Parse("https://" + host); err == nil && parsed.Hostname() != "" {
			host = strings.ToLower(parsed.Hostname())
		}
		if _, exists := seen[host]; exists {
			continue
		}
		seen[host] = struct{}{}
		out.AllowedHosts = append(out.AllowedHosts, host)
	}
	slices.Sort(out.AllowedHosts)
	return out
}

func lockedClient(base *http.Client) *http.Client {
	if base == nil {
		return nil
	}
	copy := *base
	if copy.Timeout <= 0 || copy.Timeout > requestTimeout {
		copy.Timeout = requestTimeout
	}
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copy
}

func lookupHost(ctx context.Context, host string) ([]netip.Addr, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{address.Unmap()}, nil
	}
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

func publicAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() {
		return false
	}
	if address.Is4() {
		bytes := address.As4()
		if bytes[0] == 100 && bytes[1]&0xc0 == 64 {
			return false
		}
	}
	return true
}

type accountWire struct {
	Kind               string         `json:"kind"`
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	AuthType           string         `json:"authType"`
	IsActive           bool           `json:"isActive"`
	Schedulable        bool           `json:"schedulable"`
	Priority           int32          `json:"priority"`
	MaxConcurrentTasks int32          `json:"maxConcurrentTasks"`
	Proxy              *Proxy         `json:"proxy"`
	Credentials        map[string]any `json:"credentials"`
	Extra              map[string]any `json:"extra"`
}

type exportResponse struct {
	Success bool `json:"success"`
	Data    struct {
		ClaudeAccounts          []accountWire `json:"claudeAccounts"`
		ClaudeConsoleAccounts   []accountWire `json:"claudeConsoleAccounts"`
		OpenAIOAuthAccounts     []accountWire `json:"openaiOAuthAccounts"`
		OpenAIResponsesAccounts []accountWire `json:"openaiResponsesAccounts"`
		GeminiOAuthAccounts     []accountWire `json:"geminiOAuthAccounts"`
		GeminiAPIKeyAccounts    []accountWire `json:"geminiApiKeyAccounts"`
	} `json:"data"`
}

func normalizeExport(wire exportResponse) ([]Account, error) {
	total := len(wire.Data.ClaudeAccounts) + len(wire.Data.ClaudeConsoleAccounts) +
		len(wire.Data.OpenAIOAuthAccounts) + len(wire.Data.OpenAIResponsesAccounts) +
		len(wire.Data.GeminiOAuthAccounts) + len(wire.Data.GeminiAPIKeyAccounts)
	if total > maxExportAccounts {
		return nil, ErrTooManyAccounts
	}
	out := make([]Account, 0, total)
	for _, item := range wire.Data.ClaudeAccounts {
		mode := credentialstore.AuthModeClaudeAIOAuth
		if auth := strings.ToLower(strings.TrimSpace(item.AuthType)); auth == "setup-token" || auth == "setup_token" {
			mode = credentialstore.AuthModeClaudeSetupToken
		} else if auth != "" && auth != "oauth" {
			out = append(out, invalidAccount("claude", item, "unsupported_auth_mode"))
			continue
		}
		out = append(out, normalizedAccount("claude", item, credentialstore.VendorAnthropic, mode, "oauth"))
	}
	for _, item := range wire.Data.ClaudeConsoleAccounts {
		out = append(out, normalizedAccount("claude_console", item, credentialstore.VendorAnthropic, credentialstore.AuthModeAPIKey, "api_key"))
	}
	for _, item := range wire.Data.OpenAIOAuthAccounts {
		out = append(out, normalizedAccount("openai_oauth", item, credentialstore.VendorOpenAI, credentialstore.AuthModeChatGPTOAuth, "oauth"))
	}
	for _, item := range wire.Data.OpenAIResponsesAccounts {
		out = append(out, normalizedAccount("openai_responses", item, credentialstore.VendorOpenAI, credentialstore.AuthModeAPIKey, "api_key"))
	}
	for _, item := range wire.Data.GeminiOAuthAccounts {
		out = append(out, normalizedAccount("gemini_oauth", item, credentialstore.VendorGemini, credentialstore.AuthModeCodeAssist, "oauth"))
	}
	for _, item := range wire.Data.GeminiAPIKeyAccounts {
		out = append(out, normalizedAccount("gemini_api_key", item, credentialstore.VendorGemini, credentialstore.AuthModeAIStudioAPIKey, "api_key"))
	}
	return out, nil
}

// SourceModeAllowed 把 CRS 的来源类型绑定到唯一生产归一化结果。
// 它同时约束真实客户端和测试/插件实现，防止未来的 CRSSource 旁路注入其他已注册模式。
func SourceModeAllowed(sourceType, vendor, authMode string) bool {
	vendor, authMode = credentialstore.CanonicalCredentialMode(vendor, authMode)
	switch strings.TrimSpace(sourceType) {
	case "claude":
		return vendor == credentialstore.VendorAnthropic &&
			(authMode == credentialstore.AuthModeClaudeAIOAuth ||
				authMode == credentialstore.AuthModeClaudeSetupToken)
	case "claude_console":
		return vendor == credentialstore.VendorAnthropic && authMode == credentialstore.AuthModeAPIKey
	case "openai_oauth":
		return vendor == credentialstore.VendorOpenAI && authMode == credentialstore.AuthModeChatGPTOAuth
	case "openai_responses":
		return vendor == credentialstore.VendorOpenAI && authMode == credentialstore.AuthModeAPIKey
	case "gemini_oauth":
		return vendor == credentialstore.VendorGemini && authMode == credentialstore.AuthModeCodeAssist
	case "gemini_api_key":
		return vendor == credentialstore.VendorGemini && authMode == credentialstore.AuthModeAIStudioAPIKey
	default:
		return false
	}
}

func normalizedAccount(sourceType string, item accountWire, vendor, authMode, accountType string) Account {
	credentials := cloneMap(item.Credentials)
	warnings := stripEndpointOverrides(credentials)
	if authMode == credentialstore.AuthModeClaudeSetupToken {
		if token := firstString(credentials, "setup_token", "access_token"); token != "" {
			credentials["setup_token"] = token
		}
		delete(credentials, "access_token")
		delete(credentials, "refresh_token")
	}
	concurrency := int32(3)
	if item.MaxConcurrentTasks > 0 {
		concurrency = item.MaxConcurrentTasks
	}
	return Account{
		SourceType: sourceType, SourceID: strings.TrimSpace(item.ID), Name: strings.TrimSpace(item.Name),
		Vendor: vendor, AuthMode: authMode, AccountType: accountType,
		Enabled: item.IsActive && item.Schedulable, Schedulable: item.Schedulable,
		Priority: item.Priority, Concurrency: concurrency,
		Credentials: credentials, Extra: cloneMap(item.Extra), Proxy: cloneProxy(item.Proxy), Warnings: warnings,
	}
}

func invalidAccount(sourceType string, item accountWire, code string) Account {
	return Account{SourceType: sourceType, SourceID: strings.TrimSpace(item.ID), Name: strings.TrimSpace(item.Name), InvalidCode: code}
}

func stripEndpointOverrides(fields map[string]any) []string {
	warnings := make([]string, 0, 1)
	for _, key := range []string{"base_url", "token_url", "token_endpoint", "oauth_token_endpoint", "endpoint", "endpoint_api", "auth_header"} {
		if _, exists := fields[key]; exists {
			delete(fields, key)
			warnings = append(warnings, "source_endpoint_override_removed")
		}
	}
	return slices.Compact(warnings)
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneProxy(in *Proxy) *Proxy {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func firstString(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := fields[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (e Export) SourceRef() string {
	parsed, err := url.Parse(e.BaseURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return ""
	}
	sum := sha256Bytes([]byte(strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)))
	return hexPrefix(sum, 16)
}

func sha256Bytes(value []byte) [32]byte {
	return sha256.Sum256(value)
}

func hexPrefix(value [32]byte, length int) string {
	encoded := fmt.Sprintf("%x", value[:])
	if length >= len(encoded) {
		return encoded
	}
	return encoded[:length]
}
