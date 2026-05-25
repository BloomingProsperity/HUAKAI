package hermes

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
