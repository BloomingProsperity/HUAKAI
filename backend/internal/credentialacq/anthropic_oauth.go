package credentialacq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialacq/accountident"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

const (
	claudeAIOAuthAuthURL          = "https://claude.ai/oauth/authorize"
	claudeAIOAuthTokenURL         = "https://api.anthropic.com/v1/oauth/token"
	claudeAIOAuthPublicClientID   = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	claudeAIOAuthScope            = "org:create_api_key user:profile user:inference"
	claudeAIOAuthLoopbackRedirect = "http://localhost:54545/callback"
	claudeAIOAuthLoopbackPath     = "/callback"

	claudeAIOAuthApprovedProfileSource = "approved_builtin_profile"
)

type claudeAIOAuthExchanger struct {
	now        func() time.Time
	httpClient *http.Client
}

func newClaudeAIOAuthExchanger() claudeAIOAuthExchanger {
	return claudeAIOAuthExchanger{}
}

// NewClaudeAIOAuthExchangerWithClient 返回带显式 HTTP client 的 exchanger。
// 生产 wiring 注入经过启动探测的 Rust sidecar client；nil 只表示尚未接线，
// 实际换码时会明确失败，不会退回标准库 TLS。
func NewClaudeAIOAuthExchangerWithClient(client *http.Client) Exchanger {
	return claudeAIOAuthExchanger{httpClient: client}
}

// IsClaudeAIOAuthExchangerWithExplicitClient 返 true 当且仅当传入 exchanger
// 是 claudeAIOAuthExchanger 且已注入非 nil HTTP client。供 wiring fail-loud
// 自检使用 — 生产启动时调用, 防止 install 调用被删 / helper 退化导致
// mimicry transport 失效却 silent 通过。
func IsClaudeAIOAuthExchangerWithExplicitClient(exc Exchanger) bool {
	e, ok := exc.(claudeAIOAuthExchanger)
	if !ok {
		return false
	}
	return e.httpClient != nil
}

func (e claudeAIOAuthExchanger) StartOAuthFlow(ctx context.Context, store *PostgresSessionStore, in StartInput, cfg OAuthClientConfig) (OAuthStartResult, error) {
	cfg = builtinProfileConfig(cfg)
	if err := validateBuiltinProfile(cfg); err != nil {
		return OAuthStartResult{}, err
	}
	in.Vendor = credentialstore.VendorAnthropic
	in.AuthMode = credentialstore.AuthModeClaudeAIOAuth
	return startStoredPKCEOAuthFlow(ctx, store, in, cfg)
}

func (e claudeAIOAuthExchanger) ExchangeOAuthCode(context.Context, Session, string) (CredentialCandidate, error) {
	return CredentialCandidate{}, fmt.Errorf("%w: anthropic claude_ai_oauth requires stored PKCE verifier", ErrOAuthExchangerMissing)
}

func (e claudeAIOAuthExchanger) ExchangeOAuthCodeWithStore(ctx context.Context, store *PostgresSessionStore, session Session, state string, code string) (CredentialCandidate, error) {
	if store == nil {
		return CredentialCandidate{}, errors.New("credentialacq: session store not configured")
	}
	payload, err := decryptStoredPKCEPayload(ctx, store, session)
	if err != nil {
		return CredentialCandidate{}, err
	}
	cfg := builtinProfileConfig(OAuthClientConfig{
		ClientID:    payload.ClientID,
		TokenURL:    payload.TokenURL,
		RedirectURI: payload.RedirectURI,
		Scopes:      append([]string(nil), payload.Scopes...),
		Source:      ClientSourcePublicCLI,
	})
	if err := validateBuiltinProfile(cfg); err != nil {
		return CredentialCandidate{}, err
	}
	payload.ClientID = cfg.ClientID
	payload.TokenURL = cfg.TokenURL
	payload.RedirectURI = cfg.RedirectURI
	payload.Scopes = append([]string(nil), cfg.Scopes...)

	token, err := e.exchangeAuthorizationCodeJSON(ctx, payload, state, code)
	if err != nil {
		return CredentialCandidate{}, err
	}
	raw, err := e.claudeAIOAuthTokenPayload(token, payload)
	if err != nil {
		return CredentialCandidate{}, err
	}
	fields, _, err := parseFakeTokenPayload(string(raw))
	if err != nil {
		return CredentialCandidate{}, err
	}
	if err := validateTokenShape(fields, TokenShapeAccessRefresh); err != nil {
		return CredentialCandidate{}, err
	}
	candidate := CredentialCandidate{
		TenantID: session.TenantID, ProviderAccountID: session.ProviderAccountID,
		Vendor: session.Vendor, AuthMode: session.AuthMode, Payload: raw, ActorID: session.ActorID,
		RedactedContext: map[string]any{"client_identity_source": claudeAIOAuthApprovedProfileSource},
	}
	// 自动捕获内联在 token-exchange 响应体里的上游 Anthropic 账户身份
	//（account.uuid / account.email_address）；uuid 为空时回退到 manual 绑定，
	// 且绝不阻断获取。这是 chatgpt/codex/gemini id_token 接缝在实时路径上的孪生实现，
	// 使全部四家 vendor 都在获取时捕获上游账户元数据。
	AttachIdentity(&candidate, accountident.ExtractAnthropic(token.Account.UUID, token.Account.EmailAddress, token.Email))
	return candidate, nil
}

func builtinProfileConfig(override OAuthClientConfig) OAuthClientConfig {
	cfg := OAuthClientConfig{
		ClientID:    claudeAIOAuthPublicClientID,
		AuthURL:     claudeAIOAuthAuthURL,
		TokenURL:    claudeAIOAuthTokenURL,
		RedirectURI: claudeAIOAuthLoopbackRedirect,
		Scopes:      strings.Fields(claudeAIOAuthScope),
		Source:      ClientSourcePublicCLI,
	}
	if strings.TrimSpace(override.ClientID) != "" {
		cfg.ClientID = strings.TrimSpace(override.ClientID)
	}
	if strings.TrimSpace(override.ClientSecret) != "" {
		cfg.ClientSecret = strings.TrimSpace(override.ClientSecret)
	}
	if strings.TrimSpace(override.AuthURL) != "" {
		cfg.AuthURL = strings.TrimSpace(override.AuthURL)
	}
	if strings.TrimSpace(override.TokenURL) != "" {
		cfg.TokenURL = strings.TrimSpace(override.TokenURL)
	}
	if strings.TrimSpace(override.RedirectURI) != "" {
		cfg.RedirectURI = strings.TrimSpace(override.RedirectURI)
	}
	if len(override.Scopes) > 0 {
		cfg.Scopes = append([]string(nil), override.Scopes...)
	}
	if strings.TrimSpace(override.Source) != "" {
		cfg.Source = strings.TrimSpace(override.Source)
	}
	if override.HTTPClient != nil {
		cfg.HTTPClient = override.HTTPClient
	}
	return cfg
}

func validateBuiltinProfile(cfg OAuthClientConfig) error {
	var mismatches []string
	if strings.TrimSpace(cfg.ClientID) != claudeAIOAuthPublicClientID {
		mismatches = append(mismatches, "client_id")
	}
	if strings.TrimSpace(cfg.ClientSecret) != "" {
		mismatches = append(mismatches, "client_secret")
	}
	if strings.TrimSpace(cfg.AuthURL) != claudeAIOAuthAuthURL {
		mismatches = append(mismatches, "auth_url")
	}
	if strings.TrimSpace(cfg.TokenURL) != claudeAIOAuthTokenURL {
		mismatches = append(mismatches, "token_url")
	}
	// redirect_uri 此前只查非空,致使管理员 override 的任意 redirect(含攻击者 https host)能进
	// authorize URL 接走授权码。改为严格 loopback 校验,与 gemini/chatgpt 的 loopback 分支等强(不达标即
	// 作 profile mismatch 拒绝)。claude_ai_oauth 是 claude.ai 固定 public client、只注册 loopback redirect,
	// claude.ai 不会接受非 loopback;且 HTTPS admin server callback 还需把 flow_id 注入 redirect(本模式走
	// 通用 startStoredPKCEOAuthFlow,未注入),否则回调缺 flow_id 必被 admin handler 拒(400)。故此处一律拒
	// 非 loopback —— 不放出一条无法完成的 admin 回调路径。HTTPS admin allowlist 对齐留 roadmap。
	if err := validateClaudeAIRedirectURI(cfg.RedirectURI); err != nil {
		mismatches = append(mismatches, "redirect_uri")
	}
	if strings.Join(trimmedFields(cfg.Scopes), " ") != claudeAIOAuthScope {
		mismatches = append(mismatches, "scope")
	}
	if source := strings.TrimSpace(cfg.Source); source != "" && source != ClientSourcePublicCLI {
		mismatches = append(mismatches, "source")
	}
	if len(mismatches) > 0 {
		return fmt.Errorf("%w: anthropic claude_ai_oauth built-in profile mismatch: %s", ErrFeatureDisabled, strings.Join(mismatches, ","))
	}
	return nil
}

// validateClaudeAIRedirectURI 严格校验 claude_ai_oauth 的 redirect_uri:仅接受 localhost loopback
// (无 userinfo、显式端口 [1024,65535]、path 恰为 /callback)。claude.ai public client 只注册 loopback,
// 非 loopback(含任意 https host)一律拒绝 —— 闭合 "redirect 只查非空,override 任意目标可接走授权码"。
func validateClaudeAIRedirectURI(raw string) error {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("%w: invalid redirect_uri: %v", ErrFeatureDisabled, err)
	}
	if parsed.Scheme != "http" {
		return fmt.Errorf("%w: redirect must be http loopback (claude.ai public client registers loopback only)", ErrFeatureDisabled)
	}
	if parsed.User != nil {
		return fmt.Errorf("%w: loopback redirect must not include userinfo", ErrFeatureDisabled)
	}
	if parsed.Hostname() != "localhost" {
		return fmt.Errorf("%w: loopback redirect host must be localhost", ErrFeatureDisabled)
	}
	portRaw := parsed.Port()
	if portRaw == "" {
		return fmt.Errorf("%w: loopback redirect requires explicit port", ErrFeatureDisabled)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil || port < 1024 || port > 65535 {
		return fmt.Errorf("%w: loopback redirect port out of range", ErrFeatureDisabled)
	}
	if parsed.EscapedPath() != claudeAIOAuthLoopbackPath {
		return fmt.Errorf("%w: loopback redirect path must be %s", ErrFeatureDisabled, claudeAIOAuthLoopbackPath)
	}
	return nil
}

func (e claudeAIOAuthExchanger) exchangeAuthorizationCodeJSON(ctx context.Context, payload storedPKCEPayload, state, code string) (oauthTokenResponse, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return oauthTokenResponse{}, fmt.Errorf("%w: authorization code is empty", ErrInvalidTokenShape)
	}
	body, err := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"redirect_uri":  strings.TrimSpace(payload.RedirectURI),
		"client_id":     claudeAIOAuthPublicClientID,
		"code_verifier": strings.TrimSpace(payload.CodeVerifier),
		"state":         strings.TrimSpace(state),
	})
	if err != nil {
		return oauthTokenResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, claudeAIOAuthTokenURL, bytes.NewReader(body))
	if err != nil {
		return oauthTokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	client := e.client()
	if client == nil {
		return oauthTokenResponse{}, errors.New("credentialacq: Anthropic OAuth Rust sidecar client 未接线")
	}
	resp, err := client.Do(req)
	if err != nil {
		return oauthTokenResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return oauthTokenResponse{}, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return oauthTokenResponse{}, fmt.Errorf("credentialacq: anthropic oauth token endpoint returned status %d: %s", resp.StatusCode, oauthErrorSummary(respBody))
	}
	var token oauthTokenResponse
	if err := json.Unmarshal(respBody, &token); err != nil {
		return oauthTokenResponse{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return oauthTokenResponse{}, fmt.Errorf("%w: anthropic oauth token response missing access token", ErrInvalidTokenShape)
	}
	if strings.TrimSpace(token.RefreshToken) == "" {
		return oauthTokenResponse{}, fmt.Errorf("%w: anthropic oauth token response missing refresh token", ErrInvalidTokenShape)
	}
	return token, nil
}

func (e claudeAIOAuthExchanger) claudeAIOAuthTokenPayload(token oauthTokenResponse, stored storedPKCEPayload) ([]byte, error) {
	raw, err := tokenCandidatePayload(token, stored, e.nowTime())
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	out["client_identity_source"] = claudeAIOAuthApprovedProfileSource
	out["client_id_source"] = claudeAIOAuthApprovedProfileSource
	return json.Marshal(out)
}

func (e claudeAIOAuthExchanger) client() *http.Client {
	return e.httpClient
}

func (e claudeAIOAuthExchanger) nowTime() time.Time {
	if e.now != nil {
		return e.now().UTC()
	}
	return time.Now().UTC()
}

func trimmedFields(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

const (
	// maxOAuthErrorPreview 是非标准错误响应体在摘要里保留的最大 rune 数(整条上游字节,从严)。
	maxOAuthErrorPreview = 200
	// maxOAuthErrorField 是规范 OAuth 字段(error / error_description)的上限 rune 数。这些是合规、面向
	// 操作者的诊断文本,放宽到比 preview 更大,但仍设界并去控制字符——防被攻陷/异常上游借 error_description
	// 注入换行或塞超长内容(这是比非标准 fallback 更常见的路径)。
	maxOAuthErrorField = 512
)

// oauthErrorSummary 把 OAuth token 端点的错误响应体提炼成一行简短摘要,供拼进面向操作者的错误信息。
// 两条路径都经「折叠为单行 + 去控制字符 + 按 rune 设界」处理,绝不把上游任意字节整条反射进操作者可见错误:
//   - 规范 OAuth 错误(error / error_description)是合规诊断字段,设较宽上界 maxOAuthErrorField 后返回;
//   - 非标准响应体(HTML 错误页、代理错误页、限流页等)设较严上界 maxOAuthErrorPreview 的预览 + 原始字节数。
//
// 目的:secret-mask(上游若回显请求内容/内部细节不被原样带出)+ 防日志注入(折叠换行/去控制字符)+ 防错误
// 信息无界膨胀(最大可达上游响应体读取上限)。原始响应体如需深度排障由调用方按需在内部日志单独记录。
func oauthErrorSummary(raw []byte) string {
	var decoded struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(raw, &decoded); err == nil {
		var field string
		switch {
		case decoded.Error != "" && decoded.ErrorDescription != "":
			field = decoded.Error + ": " + decoded.ErrorDescription
		case decoded.Error != "":
			field = decoded.Error
		case decoded.ErrorDescription != "":
			field = decoded.ErrorDescription
		}
		if field != "" {
			return truncateRunes(collapseAndStripControl(field), maxOAuthErrorField)
		}
	}
	if strings.TrimSpace(string(raw)) == "" {
		return "empty response body"
	}
	return boundedOAuthErrorPreview(raw)
}

// collapseAndStripControl 把文本折叠为单行(strings.Fields 折叠所有空白含换行/制表/多空格)并删除剩余的
// 非打印控制字符(防日志注入与终端转义)。
func collapseAndStripControl(text string) string {
	collapsed := strings.Join(strings.Fields(text), " ")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, collapsed)
}

// truncateRunes 把 s 按 rune 截断到 max,超长则加省略号(按 rune 计而非字节,多字节 UTF-8 不被切碎)。
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

// boundedOAuthErrorPreview 把非标准响应体压成单行、去控制字符、长度有界的预览,并标注原始字节数。
func boundedOAuthErrorPreview(raw []byte) string {
	preview := collapseAndStripControl(string(raw))
	if preview == "" {
		return fmt.Sprintf("non-standard error response (%d bytes)", len(raw))
	}
	return fmt.Sprintf("non-standard error response (%d bytes): %s", len(raw), truncateRunes(preview, maxOAuthErrorPreview))
}
