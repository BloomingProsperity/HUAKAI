package hermes

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewRunnerClientFromEnvMissingDisablesHermes(t *testing.T) {
	t.Setenv(RunnerURLEnv, "")
	t.Setenv(RunnerJWTPrivateKeyEnv, "/run/secrets/hermes/private.pem")
	t.Setenv(RunnerJWTKIDEnv, "mounted-but-disabled")

	client, err := NewRunnerClientFromEnv()

	if err != nil {
		t.Fatalf("NewRunnerClientFromEnv error=%v want nil", err)
	}
	if client != nil {
		t.Fatalf("client=%#v want nil when Hermes env is absent", client)
	}
}

func TestNewRunnerClientFromEnvPartialConfigErrors(t *testing.T) {
	t.Setenv(RunnerURLEnv, "http://127.0.0.1:8080")
	t.Setenv(RunnerJWTPrivateKeyEnv, "")
	t.Setenv(RunnerJWTKIDEnv, "")

	client, err := NewRunnerClientFromEnv()

	if err == nil {
		t.Fatalf("err=nil client=%#v want partial Hermes env to fail", client)
	}
}

func TestNewRunnerClientFromEnvRejectsLegacyHMACOnlyConfig(t *testing.T) {
	// 遗留的共享密钥不能构造出已认证客户端，runner 只接受非对称 JWT 合同。
	t.Setenv(RunnerURLEnv, "http://runner.local")
	t.Setenv("HUAKAI_HERMES_SHARED_SECRET", "runner-secret")
	t.Setenv(RunnerJWTPrivateKeyEnv, "")
	t.Setenv(RunnerJWTKIDEnv, "")

	client, err := NewRunnerClientFromEnv()

	if !errors.Is(err, ErrMisconfigured) || client != nil {
		t.Fatalf("client=%v err=%v want fail-closed misconfiguration for legacy HMAC-only env", client, err)
	}
}

func TestRunnerClientJWTAuthSetsBearerAndBindsTenantUser(t *testing.T) {
	// 回归守护：JWT mode 必须把 tenant/user 绑定进签名，不能只依赖可改 header。
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	const (
		tenantID = int64(7)
		userID   = int64(42)
	)
	var seenAuth, seenSignature, seenTenant, seenUser string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seenAuth = r.Header.Get("Authorization")
		seenSignature = r.Header.Get("X-Hermes-Signature")
		seenTenant = r.Header.Get(HeaderTenant)
		seenUser = r.Header.Get(HeaderUser)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})
	client, err := NewRunnerClient(RunnerConfig{
		RunnerURL:     "http://runner.local",
		JWTPrivateKey: privateKey,
		JWTKID:        "kid-jwt",
		HTTPClient:    &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("NewRunnerClient JWT: %v", err)
	}
	client.now = func() time.Time { return time.Unix(1700000000, 0).UTC() }

	resp, err := client.Chat(context.Background(), tenantID, userID, []byte(`{"messages":[]}`))
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	_ = resp.Body.Close()

	if seenSignature != "" {
		t.Fatalf("HMAC signature header=%q want empty in JWT mode", seenSignature)
	}
	if seenTenant != "7" || seenUser != "42" {
		t.Fatalf("tenant/user headers=(%q,%q) want 7/42", seenTenant, seenUser)
	}
	token := strings.TrimPrefix(seenAuth, "Bearer ")
	if token == seenAuth || token == "" {
		t.Fatalf("Authorization=%q want Bearer token", seenAuth)
	}
	claims, err := VerifyAt(publicKey, token, time.Unix(1700000000, 0).UTC())
	if err != nil {
		t.Fatalf("Verify runner JWT: %v", err)
	}
	if claims.Sub != "7:42" || claims.Kid != "kid-jwt" || claims.Aud != DefaultJWTAudience {
		t.Fatalf("claims=%+v want tenant:user subject, kid, audience", claims)
	}
}

func TestNewRunnerClientFromEnvIgnoresLegacyHMACEnvAndSignsBearer(t *testing.T) {
	// 回归守护：遗留的 HMAC env 和 auth-mode 开关绝不能覆盖纯 JWT 的 Bearer 鉴权。
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	now := time.Unix(1700000000, 0).UTC()
	var seenAuth, seenSignature string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seenAuth = r.Header.Get(HeaderAuthorization)
		seenSignature = r.Header.Get("X-Hermes-Signature")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})
	t.Setenv(RunnerURLEnv, "http://runner.local")
	t.Setenv("HUAKAI_HERMES_SHARED_SECRET", "runner-secret")
	t.Setenv("HUAKAI_HERMES_CLIENT_AUTH_MODE", "hmac")
	t.Setenv(RunnerJWTPrivateKeyEnv, writeRunnerClientPrivateKey(t, privateKey))
	t.Setenv(RunnerJWTKIDEnv, "kid-env")

	client, err := NewRunnerClientFromEnv()
	if err != nil {
		t.Fatalf("NewRunnerClientFromEnv: %v", err)
	}
	client.now = func() time.Time { return now }
	client.httpClient = &http.Client{Transport: transport}

	resp, err := client.Chat(context.Background(), 7, 42, []byte(`{"messages":[]}`))
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	_ = resp.Body.Close()

	if seenSignature != "" {
		t.Fatalf("legacy HMAC signature=%q want empty in JWT-only mode", seenSignature)
	}
	token := strings.TrimPrefix(seenAuth, "Bearer ")
	if token == "" || token == seenAuth {
		t.Fatalf("Authorization=%q want Bearer token", seenAuth)
	}
	claims, err := VerifyAt(publicKey, token, now)
	if err != nil {
		t.Fatalf("Verify env runner JWT: %v", err)
	}
	if claims.Sub != "7:42" || claims.Kid != "kid-env" {
		t.Fatalf("claims=%+v want env JWT subject and kid", claims)
	}
}

func TestRunnerClientRejectsMissingJWTMinimumConfig(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	_ = publicKey
	tests := []struct {
		name string
		cfg  RunnerConfig
	}{
		{name: "missing key and kid", cfg: RunnerConfig{RunnerURL: "http://runner.local"}},
		{name: "missing kid", cfg: RunnerConfig{RunnerURL: "http://runner.local", JWTPrivateKey: privateKey}},
		{name: "missing private key", cfg: RunnerConfig{RunnerURL: "http://runner.local", JWTKID: "kid-a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewRunnerClient(tt.cfg)
			if !errors.Is(err, ErrMisconfigured) || client != nil {
				t.Fatalf("client=%v err=%v want ErrMisconfigured", client, err)
			}
		})
	}
}

func writeRunnerClientPrivateKey(t *testing.T, privateKey ed25519.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("编码测试私钥失败：%v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	path := filepath.Join(t.TempDir(), "runner-client.key")
	if err := os.WriteFile(path, pemBytes, 0o400); err != nil {
		t.Fatalf("WriteFile private key: %v", err)
	}
	return path
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
