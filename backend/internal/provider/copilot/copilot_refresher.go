package copilot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
)

const defaultCopilotServiceTokenURL = "https://api.github.com/copilot_internal/v2/token"

var ErrCopilotAuthExpired = errors.New("copilot refresh: github authorization expired")

var (
	copilotSessionTokenJSONPattern = regexp.MustCompile(`(?i)("session_token"\s*:\s*)(?:"(?:[^"\\]|\\.)*"|[^,\s}]+)`)
	copilotGitHubTokenPattern      = regexp.MustCompile(`\b(gh[oprsu]_)[A-Za-z0-9_]+\b`)
	copilotGitHubPATPattern        = regexp.MustCompile(`\b(github_pat_)[A-Za-z0-9_]+\b`)
)

type CopilotCredentialStore interface {
	LoadCopilotCredential(ctx context.Context, accountID int64) ([]byte, error)
	SaveCopilotCredential(ctx context.Context, accountID int64, credential []byte, expiresAt time.Time) error
}

type CopilotFailureRecorder interface {
	RecordCopilotRefreshFailure(ctx context.Context, accountID int64, outcome string, cause error) error
}

type CopilotRefresher struct {
	Store   CopilotCredentialStore
	Adapter CopilotRefreshAdapter
}

type CopilotRefreshAdapter struct {
	TokenURL   string
	HTTPClient *http.Client
	Now        func() time.Time
}

func (r *CopilotRefresher) Refresh(ctx context.Context, accountID int64) error {
	if r == nil || r.Store == nil {
		return errors.New("copilot refresh: credential store missing")
	}
	current, err := r.Store.LoadCopilotCredential(ctx, accountID)
	if err != nil {
		return err
	}
	newCredential, expiresAt, err := r.Adapter.RefreshForProvider(ctx, accountID, "copilot", current)
	if err != nil {
		if recorder, ok := r.Store.(CopilotFailureRecorder); ok {
			outcome := auth.RefreshFailureAuditOutcome(
				auth.ClassifyRefreshError(err, "copilot", copilotRefreshStatusCode(err)),
				copilotRefreshFailureOutcome(err),
			)
			classifiedErr := auth.WithRefreshAuditOutcome(err, outcome)
			return errors.Join(classifiedErr, recorder.RecordCopilotRefreshFailure(ctx, accountID, outcome, classifiedErr))
		}
		return err
	}
	return r.Store.SaveCopilotCredential(ctx, accountID, newCredential, expiresAt)
}

func (r CopilotRefreshAdapter) RefreshForProvider(ctx context.Context, accountID int64, providerName string, currentCredential []byte) ([]byte, time.Time, error) {
	cred, err := parseCopilotCredential(currentCredential)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("copilot refresh account %d: %w", accountID, err)
	}
	githubToken := githubTokenFromCredential(cred)
	if githubToken == "" {
		return nil, time.Time{}, fmt.Errorf("copilot refresh account %d: github access token is empty", accountID)
	}
	service, err := r.fetchServiceToken(ctx, githubToken)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("copilot refresh account %d: %w", accountID, err)
	}
	token := mapString(service, "token")
	if token == "" {
		token = nestedMapString(service, "token", "value")
	}
	if token == "" {
		return nil, time.Time{}, fmt.Errorf("copilot refresh account %d: service token missing", accountID)
	}
	endpointAPI := extractCopilotEndpointAPI(service)
	if endpointAPI == "" {
		return nil, time.Time{}, fmt.Errorf("copilot refresh account %d: endpoint api missing", accountID)
	}
	expiresAt, ttl := r.serviceTokenExpiry(service)
	cred["github_access_token"] = githubToken
	cred["access_token"] = token
	cred["session_token"] = token
	cred["endpoint_api"] = endpointAPI
	cred["base_url"] = endpointAPI
	cred["expires_at"] = expiresAt.Format(time.RFC3339)
	if ttl > 0 {
		cred["expires_in"] = ttl
	}
	if tokenType := firstNonEmptyString(mapString(service, "token_type"), nestedMapString(service, "token", "token_type")); tokenType != "" {
		cred["token_type"] = tokenType
	}
	out, err := json.Marshal(cred)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("copilot refresh account %d: marshal credential: %w", accountID, err)
	}
	return out, expiresAt, nil
}

func (r CopilotRefreshAdapter) fetchServiceToken(ctx context.Context, githubToken string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, firstNonEmptyString(r.TokenURL, defaultCopilotServiceTokenURL), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+githubToken)
	req.Header.Set("User-Agent", defaultCopilotUserAgent)
	req.Header.Set("Editor-Version", defaultCopilotEditorVersion)
	req.Header.Set("Editor-Plugin-Version", defaultCopilotEditorPluginVersion)
	req.Header.Set("X-Github-Api-Version", defaultCopilotAPIVersion)
	req.Header.Set("Copilot-Integration-Id", defaultCopilotIntegrationID)

	resp, err := r.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	errorBody := func() string {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return sanitizeCopilotRefreshErrorBody(string(raw))
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, &CopilotRefreshError{StatusCode: resp.StatusCode, Body: errorBody(), Retryable: false, Cause: ErrCopilotAuthExpired}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &CopilotRefreshError{StatusCode: resp.StatusCode, Body: errorBody(), Retryable: resp.StatusCode >= http.StatusInternalServerError}
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	decoder.UseNumber()
	var out map[string]any
	if err := decoder.Decode(&out); err != nil {
		return nil, fmt.Errorf("service token response invalid: %w", err)
	}
	return out, nil
}

func sanitizeCopilotRefreshErrorBody(body string) string {
	msg := strings.TrimSpace(body)
	if msg == "" {
		return ""
	}
	msg = copilotSessionTokenJSONPattern.ReplaceAllString(msg, `${1}"<redacted>"`)
	msg = copilotGitHubPATPattern.ReplaceAllString(msg, `${1}<redacted>`)
	msg = copilotGitHubTokenPattern.ReplaceAllString(msg, `${1}<redacted>`)
	return auth.SanitizeOAuthMessage(msg)
}

type CopilotRefreshError struct {
	StatusCode int
	Body       string
	Retryable  bool
	Cause      error
}

func (e *CopilotRefreshError) Error() string {
	if e == nil {
		return "copilot refresh: service token endpoint failed"
	}
	body := ""
	if e.Body != "" {
		body = fmt.Sprintf(" body=%q", e.Body)
	}
	return fmt.Sprintf("copilot refresh: service token endpoint returned status %d%s", e.StatusCode, body)
}

func (e *CopilotRefreshError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *CopilotRefreshError) RetryableRefresh() bool {
	return e != nil && e.Retryable
}

func (r CopilotRefreshAdapter) serviceTokenExpiry(service map[string]any) (time.Time, int64) {
	now := r.now().UTC()
	ttl := firstPositiveInt64(
		mapInt64(service, "expires_in"),
		nestedMapInt64(service, "token", "expires_in"),
	)
	if ttl > 0 {
		return now.Add(time.Duration(ttl) * time.Second), ttl
	}
	if unixSeconds := firstPositiveInt64(
		mapInt64(service, "expires_at"),
		nestedMapInt64(service, "token", "expires_at"),
	); unixSeconds > 0 {
		if unixSeconds > 1_000_000_000_000 {
			unixSeconds = unixSeconds / 1000
		}
		return time.Unix(unixSeconds, 0).UTC(), 0
	}
	if raw := firstNonEmptyString(mapString(service, "expires_at"), nestedMapString(service, "token", "expires_at")); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			return parsed.UTC(), 0
		}
	}
	return now.Add(time.Hour), 3600
}

func (r CopilotRefreshAdapter) httpClient() *http.Client {
	if r.HTTPClient != nil {
		return r.HTTPClient
	}
	// 未注入 client 时(生产唯二构造点 wiring/mode_refresh 均零值 Adapter)必须回退到 SSRF 防护 client:
	// 该路径用 Authorization: token <github_token> 把高价值 GitHub access token 出站,裸 http.DefaultClient
	// 会读 HTTP_PROXY 经 env 代理外发 token、无拨号层 IP 校验、不禁 3xx。与 kiro/openai refresher 一致
	// (S2-054 同款防线,此前 copilot 漏修)。
	return auth.NewSSRFProtectedOAuthClient(http.DefaultClient)
}

func (r CopilotRefreshAdapter) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func parseCopilotCredential(raw []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errors.New("credential json is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var cred map[string]any
	if err := decoder.Decode(&cred); err != nil {
		return nil, fmt.Errorf("credential json invalid: %w", err)
	}
	return cred, nil
}

func githubTokenFromCredential(cred map[string]any) string {
	token := firstNonEmptyString(
		mapString(cred, "github_access_token"),
		mapString(cred, "github_oauth_token"),
	)
	if token != "" {
		return token
	}
	if mapString(cred, "session_token") == "" && mapString(cred, "copilot_access_token") == "" {
		return mapString(cred, "access_token")
	}
	return ""
}

func extractCopilotEndpointAPI(root map[string]any) string {
	return firstNonEmptyString(
		mapString(root, "endpoint_api"),
		nestedMapString(root, "endpoint", "api"),
		nestedMapString(root, "endpoints", "api"),
		nestedMapString(root, "token", "endpoint", "api"),
		nestedMapString(root, "token", "endpoints", "api"),
	)
}

func copilotRefreshFailureOutcome(err error) string {
	if errors.Is(err, ErrCopilotAuthExpired) {
		return "auth_expired"
	}
	return "refresh_failed"
}

func copilotRefreshStatusCode(err error) int {
	var refreshErr *CopilotRefreshError
	if errors.As(err, &refreshErr) {
		return refreshErr.StatusCode
	}
	return 0
}

func copilotChatEndpointFromAPIBase(apiBase string) string {
	raw := strings.TrimSpace(apiBase)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw
	}
	path := strings.TrimRight(parsed.Path, "/")
	if path == "" {
		parsed.Path = "/chat/completions"
		return parsed.String()
	}
	if strings.HasSuffix(path, "/chat/completions") {
		parsed.Path = path
		return parsed.String()
	}
	parsed.Path = path + "/chat/completions"
	return parsed.String()
}

func mapString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	switch v := values[key].(type) {
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return strings.TrimSpace(v.String())
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strings.TrimSpace(strconv.FormatFloat(v, 'f', -1, 64))
	default:
		return ""
	}
}

func nestedMapString(values map[string]any, path ...string) string {
	if len(path) == 0 {
		return ""
	}
	cur := values
	for _, key := range path[:len(path)-1] {
		next, ok := cur[key].(map[string]any)
		if !ok {
			return ""
		}
		cur = next
	}
	return mapString(cur, path[len(path)-1])
}

func mapInt64(values map[string]any, key string) int64 {
	if values == nil {
		return 0
	}
	switch v := values[key].(type) {
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return n
		}
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err == nil {
			return n
		}
	}
	return 0
}

func nestedMapInt64(values map[string]any, path ...string) int64 {
	if len(path) == 0 {
		return 0
	}
	cur := values
	for _, key := range path[:len(path)-1] {
		next, ok := cur[key].(map[string]any)
		if !ok {
			return 0
		}
		cur = next
	}
	return mapInt64(cur, path[len(path)-1])
}

func firstPositiveInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
