package hermes

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	t.Setenv(RunnerSharedSecretEnv, "")

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
	t.Setenv(RunnerSharedSecretEnv, "")

	client, err := NewRunnerClientFromEnv()

	if err == nil {
		t.Fatalf("err=nil client=%#v want partial Hermes env to fail", client)
	}
}

func TestRunnerClientSignsMethodPathQueryTenantUserHeaders(t *testing.T) {
	const (
		tenantID = int64(7)
		userID   = int64(42)
		secret   = "runner-secret"
		rawQuery = "limit=5&cursor=abc"
	)
	var seenSignature, seenTenant, seenUser, seenTimestamp, seenMethod, seenPath, seenQuery, seenBody string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seenSignature = r.Header.Get(HeaderSignature)
		seenTenant = r.Header.Get(HeaderTenant)
		seenUser = r.Header.Get(HeaderUser)
		seenTimestamp = r.Header.Get(HeaderTimestamp)
		seenMethod = r.Method
		seenPath = r.URL.Path
		seenQuery = r.URL.RawQuery
		raw, _ := io.ReadAll(r.Body)
		seenBody = string(raw)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})

	client, err := NewRunnerClient(RunnerConfig{
		RunnerURL:    "http://runner.local/hermes",
		SharedSecret: secret,
		HTTPClient:   &http.Client{Transport: transport},
	})
	if err != nil {
		t.Fatalf("NewRunnerClient: %v", err)
	}
	client.now = func() time.Time { return time.Unix(1234, 0).UTC() }

	resp, err := client.Conversations(context.Background(), tenantID, userID, rawQuery)
	if err != nil {
		t.Fatalf("Conversations: %v", err)
	}
	_ = resp.Body.Close()

	if seenTenant != fmt.Sprintf("%d", tenantID) || seenUser != fmt.Sprintf("%d", userID) {
		t.Fatalf("tenant/user headers=(%q,%q) want (%d,%d)", seenTenant, seenUser, tenantID, userID)
	}
	if seenMethod != http.MethodGet || seenPath != "/hermes/conversations" || seenQuery != rawQuery {
		t.Fatalf("canonical request parts method/path/query=(%q,%q,%q) want (%q,%q,%q)",
			seenMethod, seenPath, seenQuery, http.MethodGet, "/hermes/conversations", rawQuery)
	}
	if seenBody != "" {
		t.Fatalf("body=%q want empty body for conversations request", seenBody)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(seenTimestamp + "\nGET\n/hermes/conversations\n" + rawQuery + "\n7\n42\n"))
	wantSignature := hex.EncodeToString(mac.Sum(nil))
	if seenSignature != wantSignature {
		t.Fatalf("signature=%q want %q", seenSignature, wantSignature)
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
		seenSignature = r.Header.Get(HeaderSignature)
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

func TestRunnerClientAuthModeTransitionHeaderSelection(t *testing.T) {
	// 回归守护：transition 期双凭据默认仍要发 HMAC；只有显式 client auth mode=jwt 才发 Bearer。
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	now := time.Unix(1700000000, 0).UTC()
	body := []byte(`{"messages":[]}`)
	tests := []struct {
		name          string
		cfg           RunnerConfig
		wantHMAC      bool
		wantBearerJWT bool
	}{
		{
			name: "dual credentials explicit hmac sends hmac",
			cfg: RunnerConfig{
				RunnerURL:      "http://runner.local",
				SharedSecret:   "runner-secret",
				JWTPrivateKey:  privateKey,
				JWTKID:         "kid-dual",
				ClientAuthMode: "hmac",
			},
			wantHMAC: true,
		},
		{
			name: "dual credentials explicit jwt sends bearer",
			cfg: RunnerConfig{
				RunnerURL:      "http://runner.local",
				SharedSecret:   "runner-secret",
				JWTPrivateKey:  privateKey,
				JWTKID:         "kid-dual",
				ClientAuthMode: "jwt",
			},
			wantBearerJWT: true,
		},
		{
			name: "no mode with only hmac sends hmac",
			cfg: RunnerConfig{
				RunnerURL:    "http://runner.local",
				SharedSecret: "runner-secret",
			},
			wantHMAC: true,
		},
		{
			name: "no mode with only jwt sends bearer",
			cfg: RunnerConfig{
				RunnerURL:     "http://runner.local",
				JWTPrivateKey: privateKey,
				JWTKID:        "kid-jwt",
			},
			wantBearerJWT: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var seenReq *http.Request
			var seenBody []byte
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				seenReq = r
				seenBody, _ = io.ReadAll(r.Body)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
					Header:     make(http.Header),
				}, nil
			})
			tt.cfg.HTTPClient = &http.Client{Transport: transport}
			client, err := NewRunnerClient(tt.cfg)
			if err != nil {
				t.Fatalf("NewRunnerClient: %v", err)
			}
			client.now = func() time.Time { return now }

			resp, err := client.Chat(context.Background(), 7, 42, body)
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			_ = resp.Body.Close()

			if tt.wantHMAC {
				if got := seenReq.Header.Get(HeaderAuthorization); got != "" {
					t.Fatalf("Authorization=%q want empty for HMAC client mode", got)
				}
				if !VerifyRunnerHMACRequest(seenReq, seenBody, []byte("runner-secret"), now) {
					t.Fatalf("HMAC signature did not verify for %s", tt.name)
				}
			}
			if tt.wantBearerJWT {
				if got := seenReq.Header.Get(HeaderSignature); got != "" {
					t.Fatalf("HMAC signature=%q want empty for JWT client mode", got)
				}
				token := strings.TrimPrefix(seenReq.Header.Get(HeaderAuthorization), "Bearer ")
				if token == "" || token == seenReq.Header.Get(HeaderAuthorization) {
					t.Fatalf("Authorization=%q want Bearer token", seenReq.Header.Get(HeaderAuthorization))
				}
				claims, err := VerifyAt(publicKey, token, now)
				if err != nil {
					t.Fatalf("Verify runner JWT: %v", err)
				}
				if claims.Sub != "7:42" {
					t.Fatalf("claims.Sub=%q want 7:42", claims.Sub)
				}
			}
		})
	}
}

func TestNewRunnerClientFromEnvReadsClientAuthMode(t *testing.T) {
	// 回归守护：env client auth mode=jwt 必须覆盖 transition 默认 HMAC 选择。
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	now := time.Unix(1700000000, 0).UTC()
	var seenAuth, seenSignature string
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seenAuth = r.Header.Get(HeaderAuthorization)
		seenSignature = r.Header.Get(HeaderSignature)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Header:     make(http.Header),
		}, nil
	})
	t.Setenv(RunnerURLEnv, "http://runner.local")
	t.Setenv(RunnerSharedSecretEnv, "runner-secret")
	t.Setenv(RunnerJWTPrivateKeyEnv, writeRunnerClientPrivateKey(t, privateKey))
	t.Setenv(RunnerJWTKIDEnv, "kid-env")
	t.Setenv(RunnerClientAuthModeEnv, RunnerClientAuthModeJWT)

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
		t.Fatalf("HMAC signature=%q want empty when env client mode is jwt", seenSignature)
	}
	token := strings.TrimPrefix(seenAuth, "Bearer ")
	if token == "" || token == seenAuth {
		t.Fatalf("Authorization=%q want Bearer token from env client mode", seenAuth)
	}
	claims, err := VerifyAt(publicKey, token, now)
	if err != nil {
		t.Fatalf("Verify env runner JWT: %v", err)
	}
	if claims.Sub != "7:42" || claims.Kid != "kid-env" {
		t.Fatalf("claims=%+v want env JWT subject and kid", claims)
	}
}

func writeRunnerClientPrivateKey(t *testing.T, privateKey ed25519.PrivateKey) string {
	t.Helper()
	pemBytes, err := EncodePrivateKeyPEM(privateKey)
	if err != nil {
		t.Fatalf("EncodePrivateKeyPEM: %v", err)
	}
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
