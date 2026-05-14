package gatewayhttp

import (
	"net/http"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
)

func TestChatCompletionsClientAdapter_NonStreamingModelChainAndHeaders(t *testing.T) {
	dispatcher := &mockCanonicalBufferedDispatcher{}
	d := clientAdapterDeps(t)
	d.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias:      "gpt-4o",
		CanonicalModelID: "openai/gpt-4o",
		ProviderModelID:  "gpt-4o-2024-08-06",
		ProtocolFamily:   "openai_chat",
		PoolCandidates:   []int64{42},
	}}
	d.CanonicalDispatcher = dispatcher

	rec := invokeHandlerPath(t, d, "/v1/chat/completions", `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(headerHUAKAIModelRequested); got != "gpt-4o" {
		t.Fatalf("%s=%q want gpt-4o", headerHUAKAIModelRequested, got)
	}
	if got := rec.Header().Get(headerHUAKAIModelDelivered); got != "gpt-4o-2024-08-06" {
		t.Fatalf("%s=%q want gpt-4o-2024-08-06", headerHUAKAIModelDelivered, got)
	}
	if dispatcher.observed == nil || dispatcher.observed.RequestEnvelope == nil {
		t.Fatal("dispatcher did not observe request envelope")
	}
	accounting := dispatcher.observed.RequestEnvelope.Accounting
	if accounting.ModelChain == nil {
		t.Fatal("request envelope ModelChain is nil")
	}
	if accounting.ModelChain.Requested != "gpt-4o" {
		t.Fatalf("ModelChain.Requested=%q want gpt-4o", accounting.ModelChain.Requested)
	}
	if accounting.ModelChain.RouteDecided != "gpt-4o-2024-08-06" {
		t.Fatalf("ModelChain.RouteDecided=%q want provider model", accounting.ModelChain.RouteDecided)
	}
	if len(accounting.HopChain) != 4 {
		t.Fatalf("HopChain len=%d want 4", len(accounting.HopChain))
	}
	for i, hop := range accounting.HopChain {
		if len(hop.Detail) != 0 {
			t.Fatalf("hop[%d] detail must stay empty, got %s", i, hop.Detail)
		}
	}
}

func TestSetHUAKAIModelHeadersOmitsEmptyDelivered(t *testing.T) {
	h := http.Header{}
	setHUAKAIModelHeaders(h, "requested-model", proto.NewEmptyEnvelope())

	if got := h.Get(headerHUAKAIModelRequested); got != "requested-model" {
		t.Fatalf("%s=%q want requested-model", headerHUAKAIModelRequested, got)
	}
	if got := h.Get(headerHUAKAIModelDelivered); got != "" {
		t.Fatalf("%s=%q want empty", headerHUAKAIModelDelivered, got)
	}
}
