package vertexsa

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// testKeyPEM 生成一把测试 RSA key 并返回其 PKCS1 PEM。
func testKeyPEM(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("生成测试 RSA key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(priv)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
	return priv, string(pemBytes)
}

// redirectTransport 把发往官方 host 的请求改路由到测试服务器,
// 从而在不放松 SSRF 守卫(token_uri 仍是 oauth2.googleapis.com)的前提下可测。
type redirectTransport struct {
	target *url.URL
}

func (rt redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = rt.target.Scheme
	clone.URL.Host = rt.target.Host
	clone.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

// TestMintSignsValidAssertionAndReturnsToken 验证:铸造发出的 assertion 必须带
// 正确的 iss/scope/aud 且 RS256 可验签;错误 claim 会被服务器拒→整体失败。
// 变异:把 signAssertion 的 scope 改成别的、或 iss 留空,服务器验证失败→本测试红。
func TestMintSignsValidAssertionAndReturnsToken(t *testing.T) {
	priv, pemStr := testKeyPEM(t)
	const wantEmail = "svc@proj.iam.gserviceaccount.com"

	var gotScope, gotIss, gotAud, gotGrant string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotGrant = r.Form.Get("grant_type")
		assertion := r.Form.Get("assertion")
		tok, err := jwt.Parse(assertion, func(*jwt.Token) (interface{}, error) {
			return &priv.PublicKey, nil
		}, jwt.WithValidMethods([]string{"RS256"}))
		if err != nil || !tok.Valid {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		mc, _ := tok.Claims.(jwt.MapClaims)
		gotScope, _ = mc["scope"].(string)
		gotIss, _ = mc["iss"].(string)
		gotAud, _ = mc["aud"].(string)
		_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "ya29.minted", ExpiresIn: 3600, TokenType: "Bearer"})
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)

	hc := &http.Client{Transport: redirectTransport{target: target}}
	// 用接近真实的 now:服务器端 jwt.Parse 按真实时间校验 exp,过去时间会被判过期。
	now := time.Now().UTC()
	// token_uri 留空 → 走官方 oauth2.googleapis.com,过 SSRF 守卫;Transport 改路由到本地。
	tok, err := Mint(context.Background(), hc, ServiceAccount{ClientEmail: wantEmail, PrivateKeyPEM: pemStr}, now)
	if err != nil {
		t.Fatalf("Mint 失败: %v", err)
	}
	if tok.AccessToken != "ya29.minted" {
		t.Fatalf("access_token=%q want ya29.minted", tok.AccessToken)
	}
	if want := now.Add(3600 * time.Second); !tok.ExpiresAt.Equal(want) {
		t.Fatalf("expiry=%v want %v", tok.ExpiresAt, want)
	}
	if gotGrant != jwtBearerGrant {
		t.Fatalf("grant_type=%q want %q", gotGrant, jwtBearerGrant)
	}
	if gotScope != cloudPlatformScope {
		t.Fatalf("assertion scope=%q want %q(签名 claim 错→本断言红)", gotScope, cloudPlatformScope)
	}
	if gotIss != wantEmail {
		t.Fatalf("assertion iss=%q want %q", gotIss, wantEmail)
	}
	if gotAud != defaultTokenURI {
		t.Fatalf("assertion aud=%q want %q", gotAud, defaultTokenURI)
	}
}

// TestResolveTokenURIHostGuard 隔离验证 SSRF host 守卫本身(不被 https/scheme 检查掩盖):
// https 非官方 host 必须被 allowedTokenHost 拒。变异:让 allowedTokenHost 恒 true → 本测试红。
func TestResolveTokenURIHostGuard(t *testing.T) {
	cases := []struct {
		name    string
		uri     string
		wantErr bool
	}{
		{"官方默认", "", false},
		{"官方oauth2", "https://oauth2.googleapis.com/token", false},
		{"官方子域", "https://sts.googleapis.com/v1/token", false},
		{"https内网host", "https://169.254.169.254/token", true},      // 元数据服务
		{"https伪造host", "https://evil.example.com/token", true},     // 攻击者 host
		{"https内网名", "https://token-endpoint.internal/token", true}, // 内网名
		{"非https", "http://oauth2.googleapis.com/token", true},      // scheme 也挡
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := resolveTokenURI(c.uri)
			if c.wantErr && err == nil {
				t.Fatalf("uri=%q 期望被拒", c.uri)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("uri=%q 期望放行,却报 %v", c.uri, err)
			}
		})
	}
}

// TestMintRejectsSSRFTokenURI 端到端验证:伪造 https 内网 token_uri 必须在发 HTTP 前被拒。
func TestMintRejectsSSRFTokenURI(t *testing.T) {
	_, pemStr := testKeyPEM(t)
	_, err := Mint(context.Background(), http.DefaultClient, ServiceAccount{
		ClientEmail: "svc@x.iam.gserviceaccount.com", PrivateKeyPEM: pemStr,
		TokenURI: "https://169.254.169.254/token",
	}, time.Now())
	if !errors.Is(err, ErrTokenURINotAllowed) {
		t.Fatalf("err=%v want ErrTokenURINotAllowed", err)
	}
}

// TestMintRejectsBadPrivateKey 验证坏 PEM 私钥被拒且不发请求。
func TestMintRejectsBadPrivateKey(t *testing.T) {
	_, err := Mint(context.Background(), http.DefaultClient, ServiceAccount{
		ClientEmail: "svc@x.iam.gserviceaccount.com", PrivateKeyPEM: "-----BEGIN RSA PRIVATE KEY-----\nnot-a-key\n-----END RSA PRIVATE KEY-----",
	}, time.Unix(1_700_000_000, 0))
	if !errors.Is(err, ErrPrivateKey) {
		t.Fatalf("err=%v want ErrPrivateKey", err)
	}
}

// TestMintPropagatesTokenEndpointError 验证 token 端点非 200 时报错且不回显 assertion。
func TestMintPropagatesTokenEndpointError(t *testing.T) {
	_, pemStr := testKeyPEM(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "bad assertion"})
	}))
	defer srv.Close()
	target, _ := url.Parse(srv.URL)
	hc := &http.Client{Transport: redirectTransport{target: target}}

	_, err := Mint(context.Background(), hc, ServiceAccount{ClientEmail: "svc@x.iam.gserviceaccount.com", PrivateKeyPEM: pemStr}, time.Unix(1_700_000_000, 0))
	if err == nil {
		t.Fatalf("期望 token 端点错误被传播")
	}
	if got := err.Error(); !contains(got, "invalid_grant") || contains(got, "assertion") && contains(got, "ya29") {
		t.Fatalf("错误信息不应回显 assertion 内容: %q", got)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
