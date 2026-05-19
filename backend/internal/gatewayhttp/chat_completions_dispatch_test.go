package gatewayhttp

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

type recordingClaimGate struct {
	endpointFamily string
}

func (g *recordingClaimGate) Reserve(_ context.Context, req billing.ReserveRequest) (*billing.ReserveResult, error) {
	g.endpointFamily = req.EndpointFamily
	return &billing.ReserveResult{ClaimID: 999}, nil
}

func TestHandler_NoStream(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	d := clientAdapterDeps(t)
	rec := invokeHandler(t, d, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d; want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "upstream_unknown_upstream") {
		t.Fatalf("body = %q; want normalized upstream error", rec.Body.String())
	}
}

func TestHandler_DefaultHCSFOn(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if dispatcher.calls != 1 {
		t.Fatalf("canonical dispatcher calls = %d; want 1", dispatcher.calls)
	}
}

func TestHandler_EnvOffHCSFOff(t *testing.T) {
	t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadGateway || dispatcher.calls != 0 {
		t.Fatalf("status/calls = %d/%d; body = %s", rec.Code, dispatcher.calls, rec.Body.String())
	}
}

func TestHandler_AnthropicEndpointFamilySet(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := anthropicClientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	body := `{"model":"claude-3-5-sonnet","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
	rec := invokeHandlerPath(t, d, "/v1/messages", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if dispatcher.observed == nil || dispatcher.observed.RequestMeta.EndpointFamily != "anthropic_messages" {
		t.Fatalf("EndpointFamily = %+v", dispatcher.observed)
	}
}

func TestHandler_OpenAIEndpointFamilySet(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if dispatcher.observed == nil || dispatcher.observed.RequestMeta.EndpointFamily != "openai_chat" {
		t.Fatalf("EndpointFamily = %+v", dispatcher.observed)
	}
}

func TestResponsesRoute200RoundTrip(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := responsesClientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	rec := invokeResponsesHandlerPath(t, d, "/v1/responses", `{"model":"gpt-4o","stream":false,"input":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"object":"response"`) {
		t.Fatalf("body = %s; want OpenAI Responses response object", rec.Body.String())
	}
	if dispatcher.calls != 1 {
		t.Fatalf("canonical dispatcher calls = %d; want 1", dispatcher.calls)
	}
}

func TestResponsesFamilySetEndpointFamily(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	claimGate := &recordingClaimGate{}
	d := responsesClientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.ClaimGate = claimGate
	rec := invokeResponsesHandlerPath(t, d, "/v1/responses", `{"model":"gpt-4o","stream":false,"input":"hi"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body = %s", rec.Code, rec.Body.String())
	}
	if claimGate.endpointFamily != "openai_responses" {
		t.Fatalf("billing EndpointFamily=%q want openai_responses", claimGate.endpointFamily)
	}
	if dispatcher.observed == nil {
		t.Fatal("canonical dispatcher did not observe request")
	}
	if string(dispatcher.observed.RequestMeta.ClientProtocol) != "openai_responses" ||
		dispatcher.observed.RequestMeta.EndpointFamily != "openai_responses" {
		t.Fatalf("responses meta client/family=%q/%q", dispatcher.observed.RequestMeta.ClientProtocol, dispatcher.observed.RequestMeta.EndpointFamily)
	}
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	_ = os.Unsetenv(key)
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func anthropicClientAdapterDeps(t *testing.T) ChatHandlerDeps {
	t.Helper()
	d := minimalDeps()
	d.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias:      "claude-3-5-sonnet",
		CanonicalModelID: "anthropic/claude-3-5-sonnet",
		ProviderModelID:  "claude-3-5-sonnet",
		ProtocolFamily:   "anthropic_messages",
		PoolCandidates:   []int64{42},
	}}
	vault := provider.NewStaticVault()
	if err := vault.Set(1, provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant-test"}, provider.AccountInfo{AccountID: 1, Platform: "anthropic", AccountType: "apikey", AccountCredentialID: 9002, CredentialVersion: 1}); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}
	d.CredentialVault = vault
	return d
}

func responsesClientAdapterDeps(t *testing.T) ChatHandlerDeps {
	t.Helper()
	d := clientAdapterDeps(t)
	d.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias:      "gpt-4o",
		CanonicalModelID: "openai/gpt-4o",
		ProviderModelID:  "gpt-4o",
		ProtocolFamily:   "openai_responses",
		PoolCandidates:   []int64{42},
	}}
	return d
}
