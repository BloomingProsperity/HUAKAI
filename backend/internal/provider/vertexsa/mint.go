// Package vertexsa 把 Google Service Account(client_email + PEM private_key)
// 按 RFC 7523 JWT-bearer 流程铸造成短期 access token,供 Vertex / Google 上游做
// Bearer 鉴权。此前 legacy service_account 在物化侧 fail-closed(无法铸 token),
// 本包补上真正的「私钥签 JWT assertion → 向官方 token 端点换 access_token」链路。
package vertexsa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// defaultTokenURI 是 Google 官方 token 端点;SA JSON 未显式给 token_uri 时用它。
	defaultTokenURI = "https://oauth2.googleapis.com/token"
	// cloudPlatformScope 是 Vertex/Google 生成式端点所需的默认 scope。
	cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"
	// jwtBearerGrant 是 RFC 7523 定义的 assertion 授权类型。
	jwtBearerGrant = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	// assertionTTL 是签发的 JWT assertion 有效期(Google 上限 1h)。
	assertionTTL = time.Hour
)

// ErrTokenURINotAllowed 表示 SA JSON 里的 token_uri host 不在官方允许集内。
// 这是 SSRF 守卫:private_key 铸出的 assertion 只允许发往 Google 官方 token 端点,
// 不能被 SA JSON 里伪造的 token_uri 引导到内网/元数据服务。
var ErrTokenURINotAllowed = errors.New("vertexsa: token_uri host 不在官方允许集内")

// ErrPrivateKey 表示 PEM 私钥无法解析为 RSA key。
var ErrPrivateKey = errors.New("vertexsa: service account private_key 解析失败")

// ServiceAccount 是铸造所需的最小 SA 输入。
type ServiceAccount struct {
	ClientEmail   string // JWT iss/sub
	PrivateKeyPEM string // RS256 私钥(PEM)
	TokenURI      string // 官方 token 端点;空则用 defaultTokenURI
	Scope         string // 空则用 cloudPlatformScope
}

// Token 是铸造结果。
type Token struct {
	AccessToken string
	ExpiresAt   time.Time
}

// tokenResponse 对应官方 token 端点的成功响应。
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
	TokenType   string `json:"token_type"`
}

// allowedTokenHost 判定 token_uri 的 host 是否为 Google 官方端点。
// 只允许 oauth2.googleapis.com / accounts.google.com 及 *.googleapis.com,
// 显式挡掉 IP、link-local、内网名等 SSRF 目标。
func allowedTokenHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	switch host {
	case "oauth2.googleapis.com", "accounts.google.com", "googleapis.com":
		return true
	}
	return strings.HasSuffix(host, ".googleapis.com")
}

// resolveTokenURI 归一 token_uri 并做 SSRF 校验;返回可用的官方端点。
func resolveTokenURI(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultTokenURI, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("vertexsa: token_uri 无法解析: %w", err)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("%w: 非 https(%s)", ErrTokenURINotAllowed, u.Scheme)
	}
	if !allowedTokenHost(u.Hostname()) {
		return "", fmt.Errorf("%w: %s", ErrTokenURINotAllowed, u.Hostname())
	}
	return u.String(), nil
}

// signAssertion 用 SA 私钥按 RS256 签一个 RFC 7523 JWT assertion。
func signAssertion(sa ServiceAccount, tokenURI, scope string, now time.Time) (string, error) {
	block := strings.TrimSpace(sa.PrivateKeyPEM)
	if block == "" {
		return "", fmt.Errorf("%w: 私钥为空", ErrPrivateKey)
	}
	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(block))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrPrivateKey, err)
	}
	claims := jwt.MapClaims{
		"iss":   sa.ClientEmail,
		"sub":   sa.ClientEmail,
		"scope": scope,
		"aud":   tokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(assertionTTL).Unix(),
	}
	assertion := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := assertion.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("vertexsa: assertion 签名失败: %w", err)
	}
	return signed, nil
}

// Mint 用 SA 私钥签 assertion 并向官方 token 端点换取 access token。
// httpClient 为 nil 时用 http.DefaultClient。now 显式传入以便测试与稳定 exp。
func Mint(ctx context.Context, httpClient *http.Client, sa ServiceAccount, now time.Time) (Token, error) {
	if strings.TrimSpace(sa.ClientEmail) == "" {
		return Token{}, fmt.Errorf("vertexsa: client_email 为空")
	}
	tokenURI, err := resolveTokenURI(sa.TokenURI)
	if err != nil {
		return Token{}, err
	}
	scope := strings.TrimSpace(sa.Scope)
	if scope == "" {
		scope = cloudPlatformScope
	}
	assertion, err := signAssertion(sa, tokenURI, scope, now)
	if err != nil {
		return Token{}, err
	}

	form := url.Values{}
	form.Set("grant_type", jwtBearerGrant)
	form.Set("assertion", assertion)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, fmt.Errorf("vertexsa: 构造 token 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := httpClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("vertexsa: token 请求失败: %w", err)
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(resp.Body)
	if resp.StatusCode != http.StatusOK {
		// 官方错误体形如 {"error":"...","error_description":"..."};不回显 assertion。
		var e struct {
			Error string `json:"error"`
			Desc  string `json:"error_description"`
		}
		_ = dec.Decode(&e)
		return Token{}, fmt.Errorf("vertexsa: token 端点返回 %d error=%q", resp.StatusCode, e.Error)
	}
	var tr tokenResponse
	if err := dec.Decode(&tr); err != nil {
		return Token{}, fmt.Errorf("vertexsa: 解析 token 响应失败: %w", err)
	}
	if strings.TrimSpace(tr.AccessToken) == "" {
		return Token{}, fmt.Errorf("vertexsa: token 响应缺 access_token")
	}
	expiresIn := tr.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = int64(assertionTTL.Seconds())
	}
	return Token{
		AccessToken: tr.AccessToken,
		ExpiresAt:   now.Add(time.Duration(expiresIn) * time.Second),
	}, nil
}
