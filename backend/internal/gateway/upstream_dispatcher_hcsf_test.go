package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
)

const openAIHCSFResponse = `{"id":"chatcmpl-hcsf","object":"chat.completion","model":"gpt-4o-upstream","choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":7,"total_tokens":12}}`

func testHCSFEnvelope() *proto.HCSF {
	env := proto.NewEmptyEnvelope()
	env.RequestMeta.ProtocolFamily = "openai_chat"
	env.RequestMeta.Model = "gpt-4o"
	env.RequestMeta.UpstreamModel = "gpt-4o-upstream"
	env.RequestMeta.Provider = "openai"
	max := 64
	temp := 0.2
	env.RequestControls.MaxTokens = &max
	env.RequestControls.Temperature = &temp
	env.Messages = []proto.CanonicalMessage{{Role: "user", Content: []proto.CanonicalContentBlock{{Type: "text", Text: "raw fallback text"}}}}
	msgIdx, blkIdx := 0, 0
	env.CapabilityGraph.Nodes = []proto.CapabilityNode{{
		ID:          "n_text_1",
		Kind:        proto.CapabilityText,
		StreamReady: proto.StreamReadyYes,
		Source:      &proto.NodeSourceRef{MessageIndex: &msgIdx, BlockIndex: &blkIdx},
		Text:        &proto.TextNode{Role: "user", Block: proto.CanonicalContentBlock{Type: "text", Text: "graph text"}},
	}}
	env.Accounting.ModelChain = &proto.ModelChain{Requested: "gpt-4o", RouteDecided: "gpt-4o-upstream"}
	return env
}

func hcsfCtx() context.Context {
	return gatewayHCSFCtx(context.Background())
}

func gatewayHCSFCtx(ctx context.Context) context.Context {
	return ContextWithHCSFDispatchInput(ctx, HCSFDispatchInput{
		ProtocolFamily:  "openai_chat",
		UpstreamModelID: "gpt-4o-upstream",
		Account:         provider.AccountInfo{AccountID: 7, Platform: "openai", AccountType: "apikey"},
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-test"},
	})
}

func TestDispatchHCSFHappyPathBuildsBodyFromEnvelope(t *testing.T) {
	adapter := &stubAdapter{platform: "openai"}
	doer := &stubDoer{respStatus: 200, respBody: openAIHCSFResponse}
	d := newDispatcherForTest(adapter, doer)

	got, err := d.DispatchHCSF(hcsfCtx(), testHCSFEnvelope())
	if err != nil {
		t.Fatalf("DispatchHCSF: %v", err)
	}
	if got.BufferedResponse == nil || got.BufferedResponse.Model != "gpt-4o-upstream" {
		t.Fatalf("buffered response = %+v", got.BufferedResponse)
	}
	var body map[string]any
	if err := json.Unmarshal(adapter.lastInput.InboundBody, &body); err != nil {
		t.Fatalf("built body json: %v\n%s", err, adapter.lastInput.InboundBody)
	}
	if body["model"] != "gpt-4o-upstream" || body["max_tokens"].(float64) != 64 {
		t.Fatalf("body model/max_tokens = %v/%v", body["model"], body["max_tokens"])
	}
	msgs := body["messages"].([]any)
	if msgs[0].(map[string]any)["content"] != "graph text" {
		t.Fatalf("messages = %+v; want CapabilityGraph text", msgs)
	}
}

func TestDispatchHCSFRoundTripPreservesRequestControls(t *testing.T) {
	env := testHCSFEnvelope()
	d := newDispatcherForTest(&stubAdapter{platform: "openai"}, &stubDoer{respStatus: 200, respBody: openAIHCSFResponse})
	got, err := d.DispatchHCSF(hcsfCtx(), env)
	if err != nil {
		t.Fatalf("DispatchHCSF: %v", err)
	}
	if got.RequestControls.MaxTokens == nil || *got.RequestControls.MaxTokens != 64 {
		t.Fatalf("MaxTokens not preserved: %+v", got.RequestControls.MaxTokens)
	}
	if len(got.CapabilityGraph.Nodes) != 1 || got.CapabilityGraph.Nodes[0].ID != "n_text_1" {
		t.Fatalf("CapabilityGraph not preserved: %+v", got.CapabilityGraph.Nodes)
	}
}

func TestDispatchHCSFFillsModelChainUpstreamReported(t *testing.T) {
	d := newDispatcherForTest(&stubAdapter{platform: "openai"}, &stubDoer{respStatus: 200, respBody: openAIHCSFResponse})
	got, err := d.DispatchHCSF(hcsfCtx(), testHCSFEnvelope())
	if err != nil {
		t.Fatalf("DispatchHCSF: %v", err)
	}
	if got.Accounting.ModelChain == nil || got.Accounting.ModelChain.UpstreamReported != "gpt-4o-upstream" {
		t.Fatalf("ModelChain = %+v", got.Accounting.ModelChain)
	}
}

func TestDispatchHCSFUpstream5xxReturnsError(t *testing.T) {
	d := newDispatcherForTest(&stubAdapter{platform: "openai"}, &stubDoer{respStatus: 503, respBody: `{"error":"busy"}`})
	_, err := d.DispatchHCSF(hcsfCtx(), testHCSFEnvelope())
	if err == nil || !strings.Contains(err.Error(), "上游状态码 503") {
		t.Fatalf("err=%v want upstream 503", err)
	}
}

func TestDispatchHCSFUnregisteredUpstreamAdapterFailsLoud(t *testing.T) {
	d := newDispatcherForTest(&stubAdapter{platform: "openai"}, &stubDoer{respStatus: 200, respBody: openAIHCSFResponse})
	d.ProtocolAdapters = NewStaticProtocolAdapterRegistry()
	_, err := d.DispatchHCSF(hcsfCtx(), testHCSFEnvelope())
	if !errors.Is(err, ErrUnknownProtocolFamily) {
		t.Fatalf("err=%v want ErrUnknownProtocolFamily", err)
	}
}

type envelopeBuilderStub struct {
	stubAdapter
	called bool
}

func (s *envelopeBuilderStub) BuildRequestFromEnvelope(ctx context.Context, in provider.BuildInput, env *proto.HCSF) (*http.Request, error) {
	s.called = true
	in.InboundBody = []byte(`{"model":"from-envelope-builder","messages":[{"role":"user","content":"builder"}]}`)
	return s.stubAdapter.BuildRequest(ctx, in)
}

func TestDispatchHCSFPrefersProviderEnvelopeBuilder(t *testing.T) {
	adapter := &envelopeBuilderStub{stubAdapter: stubAdapter{platform: "openai"}}
	doer := &stubDoer{respStatus: 200, respBody: openAIHCSFResponse}
	d := newDispatcherForTest(adapter, doer)

	if _, err := d.DispatchHCSF(hcsfCtx(), testHCSFEnvelope()); err != nil {
		t.Fatalf("DispatchHCSF: %v", err)
	}
	if !adapter.called {
		t.Fatal("BuildRequestFromEnvelope was not called")
	}
	body, _ := io.ReadAll(doer.got.Body)
	if !strings.Contains(string(body), "from-envelope-builder") {
		t.Fatalf("request body = %s", body)
	}
}
