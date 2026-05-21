package gatewayhttp

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/pool"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
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
	// Anthropic 非流式 buffered 翻译器未实现,
	// handler 现 fail-fast 拒 (501)。本 test 验 reject 触发, 不静默扣上游额度。
	body := `{"model":"claude-3-5-sonnet","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
	rec := invokeHandlerPath(t, d, "/v1/messages", body)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d; want 501; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "buffered_anthropic_not_supported") {
		t.Fatalf("expected buffered_anthropic_not_supported error code; got %s", rec.Body.String())
	}
	if dispatcher.observed != nil {
		t.Fatalf("dispatcher should NOT be called when reject fires; observed = %+v", dispatcher.observed)
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

func TestHandler_WaitPlanReturnsQueueWait(t *testing.T) {
	settler := &stubSettler{}
	d := minimalDeps()
	d.Selector = waitPlanSelector{}
	d.Settler = settler

	rec := invokeHandler(t, d, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status=%d body=%s; want 429", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "3" {
		t.Fatalf("Retry-After=%q want 3", got)
	}
	if !strings.Contains(rec.Body.String(), "queue_wait") {
		t.Fatalf("body=%s; want queue_wait", rec.Body.String())
	}
	if settler.abortCalls != 1 || settler.lastAbortClaimID != 999 || settler.lastAbortReason != "queue_wait" {
		t.Fatalf("abort calls/id/reason=%d/%d/%q; want 1/999/queue_wait",
			settler.abortCalls, settler.lastAbortClaimID, settler.lastAbortReason)
	}
}

func TestHandler_AttemptLoopSkeletonPassesAttemptSeqAndEmptyExclusions(t *testing.T) {
	unsetEnvForTest(t, "HUAKAI_DISPATCH_HCSF")
	dispatcher := &mockCanonicalBufferedDispatcher{}
	selector := &recordingSelectionRequestSelector{}
	d := clientAdapterDeps(t)
	d.CanonicalDispatcher = dispatcher
	d.Selector = selector
	d.Router = stubRouter{plan: router.RoutePlan{
		Attempts: []router.AttemptPlan{
			{Index: 0, PoolGroupID: 42, UpstreamModelID: "gpt-4o", Reason: "primary"},
			{Index: 1, PoolGroupID: 43, UpstreamModelID: "gpt-4o-backup", Reason: "cross_pool_fallback"},
		},
		AttemptBudget:   2,
		SnapshotVersion: "registry:7:1;router:multi",
	}}

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s; want 200", rec.Code, rec.Body.String())
	}
	if selector.calls != 1 {
		t.Fatalf("selector calls=%d want 1 because PR3 clamps effective budget to 1", selector.calls)
	}
	req := selector.requests[0]
	if req.PoolGroupID != 42 {
		t.Fatalf("PoolGroupID=%d want first planned pool 42", req.PoolGroupID)
	}
	if req.AttemptSeq != 1 {
		t.Fatalf("AttemptSeq=%d want 1", req.AttemptSeq)
	}
	if req.ExcludedAccounts == nil {
		t.Fatal("ExcludedAccounts must be a non-nil empty map in PR3")
	}
	if len(req.ExcludedAccounts) != 0 {
		t.Fatalf("ExcludedAccounts=%v want empty map in PR3", req.ExcludedAccounts)
	}
}

func TestRouterResolvedModelFromRegistryMapsPerPoolModelOverrides(t *testing.T) {
	poolAOverride := "pool-a-upstream"
	poolBOverride := "pool-b-upstream"
	resolved := registry.Resolved{
		PublicAlias:            "gpt-4o",
		CanonicalModelID:       "openai/gpt-4o",
		DefaultProviderModelID: "default-upstream",
		ProviderModelID:        "pool-a-upstream",
		ContextWindow:          128000,
		Capabilities:           []string{"stream"},
		PricingClass:           "standard",
		ProtocolFamily:         "openai_chat",
		PoolCandidates:         []int64{701, 702, 703},
		BindingMetadata: []registry.BindingMetadata{
			{BindingID: 1, PoolGroupID: 701, Priority: 10, Weight: 5, SelectionMode: "strict_priority", FallbackClass: "normal", ProviderModelIDOverride: &poolAOverride},
			{BindingID: 2, PoolGroupID: 702, Priority: 20, Weight: 3, SelectionMode: "strict_priority", FallbackClass: "quota", ProviderModelIDOverride: &poolBOverride},
			{BindingID: 3, PoolGroupID: 703, Priority: 30, Weight: 1, SelectionMode: "strict_priority", FallbackClass: "manual"},
		},
		SnapshotVersion: "registry:7:3",
	}

	got := routerResolvedModelFromRegistry(resolved)

	if got.ProviderModelID != "pool-a-upstream" {
		t.Fatalf("ProviderModelID=%q want primary override", got.ProviderModelID)
	}
	if len(got.PoolMetadata) != 3 {
		t.Fatalf("PoolMetadata len=%d want 3", len(got.PoolMetadata))
	}
	want := []router.PoolCandidateMeta{
		{PoolGroupID: 701, ProviderModelID: "pool-a-upstream"},
		{PoolGroupID: 702, ProviderModelID: "pool-b-upstream"},
		{PoolGroupID: 703, ProviderModelID: "default-upstream"},
	}
	for i := range want {
		if got.PoolMetadata[i] != want[i] {
			t.Fatalf("PoolMetadata[%d]=%+v want %+v", i, got.PoolMetadata[i], want[i])
		}
	}
}

type waitPlanSelector struct{}

func (waitPlanSelector) Select(context.Context, pool.SelectionRequest) (*pool.SelectionResult, error) {
	return &pool.SelectionResult{WaitPlan: &pool.WaitPlan{
		AccountID:      1,
		MaxConcurrency: 2,
		TimeoutMS:      2500,
		MaxWaiting:     8,
	}}, nil
}

type recordingSelectionRequestSelector struct {
	calls    int
	requests []pool.SelectionRequest
}

func (s *recordingSelectionRequestSelector) Select(_ context.Context, req pool.SelectionRequest) (*pool.SelectionResult, error) {
	s.calls++
	s.requests = append(s.requests, req)
	return &pool.SelectionResult{AccountID: 1}, nil
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
