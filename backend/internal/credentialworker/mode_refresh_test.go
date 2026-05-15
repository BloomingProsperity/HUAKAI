package credentialworker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
)

func TestDefaultModeAdapterRegistryCoversFifteenModes(t *testing.T) {
	registry := DefaultModeAdapterRegistry()
	if got := registry.Names(); len(got) != 15 {
		t.Fatalf("mode adapter count=%d want 15: %v", len(got), got)
	}
	for _, key := range credentialstore.DefaultHandlerRegistry().Names() {
		vendor, mode := splitCredentialModeKey(key)
		if _, ok := registry.Lookup(vendor, mode); !ok {
			t.Fatalf("missing mode refresh adapter %s", key)
		}
	}
}

func TestMockTokenExchangeAdapterRefreshesAzureWithoutSDK(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("method=%s want POST", r.Method)
		}
		return jsonResponse(`{"access_token":"azure-access","expires_in":1800,"token_type":"Bearer"}`), nil
	})}

	raw := []byte(`{"mock_token_endpoint":"http://mock.local/token","tenant_id":"t"}`)
	result, err := (mockTokenExchangeAdapter{providerName: "azure", client: client}).RefreshCredential(context.Background(), ModeRefreshInput{
		Vendor: credentialstore.VendorOpenAI, AuthMode: credentialstore.AuthModeAzure, Payload: raw, Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(result.Payload, &got); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if got["access_token"] != "azure-access" || got["token_type"] != "Bearer" {
		t.Fatalf("payload=%v", got)
	}
	if time.Until(result.AccessExpiresAt) <= time.Minute {
		t.Fatalf("AccessExpiresAt not advanced: %s", result.AccessExpiresAt)
	}
}

func TestMetadataTokenAdapterUsesStdlibMetadataRequest(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Metadata-Flavor"); got != "Google" {
			t.Fatalf("Metadata-Flavor=%q want Google", got)
		}
		return jsonResponse(`{"access_token":"gcp-access","expires_in":3600}`), nil
	})}

	raw := []byte(`{"metadata_token_endpoint":"http://metadata.local/token","client_email":"svc@example.test"}`)
	result, err := (metadataTokenAdapter{client: client}).RefreshCredential(context.Background(), ModeRefreshInput{
		Vendor: credentialstore.VendorGemini, AuthMode: credentialstore.AuthModeVertexSA, Payload: raw, Now: time.Now(),
	})
	if err != nil {
		t.Fatalf("RefreshCredential: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(result.Payload, &got); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if got["access_token"] != "gcp-access" {
		t.Fatalf("payload=%v", got)
	}
}

func splitCredentialModeKey(key string) (string, string) {
	for i, r := range key {
		if r == '/' {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}
