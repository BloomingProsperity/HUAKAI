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

const anthropicLossyHCSFResponse = `{
	"id":"msg_loss",
	"type":"message",
	"role":"assistant",
	"model":"claude-3-5-sonnet",
	"content":[{},{"type":"future_block","payload":{"x":1}}],
	"stop_reason":"mystery_stop"
}`

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

func anthropicHCSFCtx() context.Context {
	return ContextWithHCSFDispatchInput(context.Background(), HCSFDispatchInput{
		ProtocolFamily:  "anthropic_messages",
		UpstreamModelID: "claude-3-5-sonnet",
		Account:         provider.AccountInfo{AccountID: 9, Platform: "anthropic", AccountType: "apikey"},
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant-test"},
	})
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
	const marker = "SENSITIVE_UPSTREAM_MARKER"
	d := newDispatcherForTest(&stubAdapter{platform: "openai"}, &stubDoer{respStatus: 503, respBody: `{"error":"` + marker + `"}`})
	_, err := d.DispatchHCSF(hcsfCtx(), testHCSFEnvelope())
	if err == nil || !strings.Contains(err.Error(), "上游状态码 503") {
		t.Fatalf("err=%v want upstream 503", err)
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("UpstreamHTTPError.Error leaked body marker: %v", err)
	}
	var upstreamErr *UpstreamHTTPError
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("err=%T want UpstreamHTTPError", err)
	}
	if !strings.Contains(string(upstreamErr.Body), marker) {
		t.Fatalf("UpstreamHTTPError.Body=%q want marker retained for classification", upstreamErr.Body)
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

func TestDispatchHCSFAppendsResponseProtocolLoss(t *testing.T) {
	env := testHCSFEnvelope()
	env.RequestMeta.ProtocolFamily = "anthropic_messages"
	env.RequestMeta.EndpointFamily = "anthropic_messages"
	env.RequestMeta.Model = "claude-3-5-sonnet"
	env.RequestMeta.UpstreamModel = "claude-3-5-sonnet"
	env.RequestMeta.Provider = "anthropic"
	env.Accounting.ModelChain = &proto.ModelChain{Requested: "claude-3-5-sonnet", RouteDecided: "claude-3-5-sonnet"}
	env.CapabilityGraph.ProtocolLoss = []proto.ProtocolLossEntry{{
		Severity: proto.ProtocolLossWarning,
		Code:     "request_side_loss",
		Reason:   "request-side loss must stay on cloned envelope",
	}}

	d := newDispatcherForTest(&stubAdapter{platform: "anthropic"}, &stubDoer{respStatus: 200, respBody: anthropicLossyHCSFResponse})
	got, err := d.DispatchHCSF(anthropicHCSFCtx(), env)
	if err != nil {
		t.Fatalf("DispatchHCSF: %v", err)
	}
	losses := got.CapabilityGraph.ProtocolLoss
	if !hasProtocolLossCode(losses, "request_side_loss") {
		t.Fatalf("request-side loss was not preserved: %+v", losses)
	}
	if !hasProtocolLossNote(losses, "missing usage") {
		t.Fatalf("response-side adapter loss was not appended: %+v", losses)
	}
	if len(losses) < 2 {
		t.Fatalf("losses=%+v want request and response losses", losses)
	}
}

func hasProtocolLossCode(losses []proto.ProtocolLossEntry, code string) bool {
	for _, loss := range losses {
		if loss.Code == code {
			return true
		}
	}
	return false
}

func hasProtocolLossNote(losses []proto.ProtocolLossEntry, needle string) bool {
	for _, loss := range losses {
		if strings.Contains(loss.Note, needle) {
			return true
		}
	}
	return false
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
