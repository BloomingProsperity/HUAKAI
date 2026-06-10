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
	env.RequestMeta.ClientProtocol = proto.ClientProtocolOpenAIChat
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

func TestDispatchHCSFUnsupportedEndpointFamilyFailsBeforeRawFallback(t *testing.T) {
	const rawMarker = "RAWFALLBACK_MARKER"
	env := testHCSFEnvelope()
	env.RequestMeta.EndpointFamily = "gemini_generate"
	ctx := ContextWithHCSFDispatchInput(context.Background(), HCSFDispatchInput{
		ProtocolFamily:  "openai_chat",
		UpstreamModelID: "gpt-4o-upstream",
		Account:         provider.AccountInfo{AccountID: 7, Platform: "openai", AccountType: "apikey"},
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-test"},
		RawBody:         []byte(`{"raw_client_marker":"` + rawMarker + `"}`),
	})
	adapter := &stubAdapter{platform: "openai"}
	doer := &stubDoer{respStatus: 200, respBody: openAIHCSFResponse}
	d := newDispatcherForTest(adapter, doer)

	_, err := d.DispatchHCSF(ctx, env)
	if err == nil {
		t.Fatal("DispatchHCSF err=nil; want unsupported endpoint family error")
	}
	if !strings.Contains(err.Error(), "unsupported HCSF endpoint family") || !strings.Contains(err.Error(), "gemini_generate") {
		t.Fatalf("err=%v want unsupported endpoint family gemini_generate", err)
	}
	if doer.got != nil {
		body, _ := io.ReadAll(doer.got.Body)
		if strings.Contains(string(body), rawMarker) {
			t.Fatalf("raw fallback marker was forwarded upstream: %s", body)
		}
		t.Fatalf("HTTP Do was called for unsupported endpoint family with body: %s", body)
	}
}

func TestDispatchHCSFNativeOnlySessionFamilyFailsBeforeRawFallback(t *testing.T) {
	const rawMarker = "CURSOR_RAWFALLBACK_MARKER"
	env := testHCSFEnvelope()
	env.RequestMeta.ProtocolFamily = "cursor_session"
	env.RequestMeta.EndpointFamily = "cursor_session"
	ctx := ContextWithHCSFDispatchInput(context.Background(), HCSFDispatchInput{
		ProtocolFamily:  "cursor_session",
		UpstreamModelID: "cursor-model",
		Account:         provider.AccountInfo{AccountID: 11, Platform: "cursor", AccountType: "session"},
		Credential:      provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "cursor-session-test"},
		RawBody:         []byte(`{"raw_client_marker":"` + rawMarker + `"}`),
	})
	adapter := &stubAdapter{platform: "cursor"}
	doer := &stubDoer{respStatus: 200, respBody: openAIHCSFResponse}
	d := newDispatcherForTest(adapter, doer)

	_, err := d.DispatchHCSF(ctx, env)
	if err == nil {
		t.Fatal("DispatchHCSF err=nil; want unsupported cursor_session endpoint family error")
	}
	if !strings.Contains(err.Error(), "unsupported HCSF endpoint family") || !strings.Contains(err.Error(), "cursor_session") {
		t.Fatalf("err=%v want unsupported endpoint family cursor_session", err)
	}
	if doer.got != nil {
		body, _ := io.ReadAll(doer.got.Body)
		if strings.Contains(string(body), rawMarker) {
			t.Fatalf("cursor raw fallback marker was forwarded upstream: %s", body)
		}
		t.Fatalf("HTTP Do was called for cursor_session HCSF path with body: %s", body)
	}
}

func TestBuildHCSFProviderRequestNativeFamiliesUseExplicitNativeRawBody(t *testing.T) {
	for _, tc := range []struct {
		family   string
		provider string
		model    string
		marker   string
	}{
		{
			family:   "bedrock_invoke",
			provider: "bedrock",
			model:    "anthropic.claude-3-5-sonnet-20241022-v2:0",
			marker:   "BEDROCK_NATIVE_RAW_MARKER",
		},
		{
			family:   "openai_codex",
			provider: "openai",
			model:    "gpt-5-codex",
			marker:   "CODEX_NATIVE_RAW_MARKER",
		},
	} {
		t.Run(tc.family, func(t *testing.T) {
			env := testHCSFEnvelope()
			env.RequestMeta.EndpointFamily = tc.family
			adapter := &stubAdapter{platform: tc.provider}

			req, err := buildHCSFProviderRequest(context.Background(), adapter, provider.BuildInput{
				UpstreamModelID: tc.model,
				Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
				Account:         provider.AccountInfo{AccountID: 12, Platform: tc.provider, AccountType: "native"},
			}, env, tc.family, tc.family, []byte(`{"raw_client_marker":"`+tc.marker+`"}`))
			if err != nil {
				t.Fatalf("buildHCSFProviderRequest %s: %v", tc.family, err)
			}
			if !strings.Contains(string(adapter.lastInput.InboundBody), tc.marker) {
				t.Fatalf("%s native raw body = %s; want marker", tc.family, adapter.lastInput.InboundBody)
			}
			body, _ := io.ReadAll(req.Body)
			if !strings.Contains(string(body), tc.marker) {
				t.Fatalf("request body = %s; want %s native raw marker", body, tc.family)
			}
		})
	}
}

func TestHCSFRequestBodyOmitsSeedForOpenAIResponses(t *testing.T) {
	seed := 2026
	env := testHCSFEnvelope()
	env.RequestControls.Seed = &seed

	raw, err := hcsfRequestBody(env, "openai_responses")
	if err != nil {
		t.Fatalf("hcsfRequestBody openai_responses: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("responses body json: %v\n%s", err, raw)
	}
	if _, ok := body["seed"]; ok {
		t.Fatalf("openai_responses body must not include unsupported seed: %s", raw)
	}
	if body["max_output_tokens"] != float64(64) {
		t.Fatalf("max_output_tokens=%v want 64", body["max_output_tokens"])
	}
}

func TestDispatchHCSFOpenAICompatibleAliasUsesModeledChatBody(t *testing.T) {
	for _, tc := range []struct {
		name     string
		family   string
		provider string
		model    string
	}{
		{name: "direct_api_key_alias", family: "deepseek_chat", provider: "deepseek", model: "deepseek-chat"},
		{name: "opt_in_session_alias", family: "copilot_session", provider: "copilot", model: "gpt-4o-copilot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seed := 4242
			env := testHCSFEnvelope()
			env.RequestMeta.ProtocolFamily = tc.family
			env.RequestMeta.EndpointFamily = tc.family
			env.RequestMeta.Provider = tc.provider
			env.RequestMeta.Model = tc.model
			env.RequestMeta.UpstreamModel = tc.model
			env.RequestControls.Seed = &seed
			env.Accounting.ModelChain = &proto.ModelChain{Requested: tc.model, RouteDecided: tc.model}
			ctx := ContextWithHCSFDispatchInput(context.Background(), HCSFDispatchInput{
				ProtocolFamily:  tc.family,
				UpstreamModelID: tc.model,
				Account:         provider.AccountInfo{AccountID: 8, Platform: tc.provider, AccountType: "apikey"},
				Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-alias-test"},
				RawBody:         []byte(`{"model":"raw-stale-model","messages":[{"role":"user","content":"raw stale"}],"max_tokens":5,"max_completion_tokens":64,"seed":999,"n":2,"frequency_penalty":0.75,"logit_bias":{"198":-100},"metadata":{"trace":"alias-raw-control"}}`),
			})
			adapter := &stubAdapter{platform: tc.provider}
			doer := &stubDoer{respStatus: 200, respBody: openAIHCSFResponse}
			d := newDispatcherForTest(adapter, doer)

			_, err := d.DispatchHCSF(ctx, env)
			if err != nil {
				t.Fatalf("DispatchHCSF %s alias: %v", tc.family, err)
			}
			var body map[string]any
			if err := json.Unmarshal(adapter.lastInput.InboundBody, &body); err != nil {
				t.Fatalf("alias body json: %v\n%s", err, adapter.lastInput.InboundBody)
			}
			if body["model"] != tc.model {
				t.Fatalf("alias body model=%v want %s", body["model"], tc.model)
			}
			if body["seed"] != float64(seed) {
				t.Fatalf("alias body seed=%v want %d", body["seed"], seed)
			}
			if _, ok := body["max_tokens"]; ok {
				t.Fatalf("alias body must preserve max_completion_tokens dialect instead of max_tokens: %s", adapter.lastInput.InboundBody)
			}
			if body["max_completion_tokens"] != float64(64) {
				t.Fatalf("alias max_completion_tokens=%v want canonical limit 64; body=%s", body["max_completion_tokens"], adapter.lastInput.InboundBody)
			}
			if _, ok := body["n"]; ok {
				t.Fatalf("alias body must not passthrough multi-choice n because buffered response drops extra choices: %s", adapter.lastInput.InboundBody)
			}
			msgs := body["messages"].([]any)
			if msgs[0].(map[string]any)["content"] != "graph text" {
				t.Fatalf("alias messages = %+v; want CapabilityGraph text", msgs)
			}
			if body["frequency_penalty"] != 0.75 {
				t.Fatalf("alias frequency_penalty=%v want raw passthrough 0.75; body=%s", body["frequency_penalty"], adapter.lastInput.InboundBody)
			}
			logitBias, ok := body["logit_bias"].(map[string]any)
			if !ok || logitBias["198"] != float64(-100) {
				t.Fatalf("alias logit_bias=%+v want raw passthrough 198=-100; body=%s", body["logit_bias"], adapter.lastInput.InboundBody)
			}
			metadata, ok := body["metadata"].(map[string]any)
			if !ok || metadata["trace"] != "alias-raw-control" {
				t.Fatalf("alias metadata=%+v want raw passthrough trace; body=%s", body["metadata"], adapter.lastInput.InboundBody)
			}
		})
	}
}

func TestDispatchHCSFOpenRouterAliasPreservesProviderRoutingControls(t *testing.T) {
	env := testHCSFEnvelope()
	env.RequestMeta.ProtocolFamily = "openrouter_chat"
	env.RequestMeta.EndpointFamily = "openrouter_chat"
	env.RequestMeta.Provider = "openrouter"
	env.RequestMeta.Model = "openrouter/auto"
	env.RequestMeta.UpstreamModel = "openrouter/auto"
	ctx := ContextWithHCSFDispatchInput(context.Background(), HCSFDispatchInput{
		ProtocolFamily:  "openrouter_chat",
		UpstreamModelID: "openrouter/auto",
		Account:         provider.AccountInfo{AccountID: 13, Platform: "openrouter", AccountType: "apikey"},
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-openrouter-test"},
		RawBody:         []byte(`{"model":"raw-openrouter-model","messages":[{"role":"user","content":"raw stale"}],"provider":{"order":["anthropic"],"allow_fallbacks":false,"sort":{"price":0.7,"latency":0.2,"throughput":0.1},"zdr":true,"only":["anthropic","openai"],"max_price":{"prompt":0.000001,"completion":0.000002},"preferred_min_throughput":80,"preferred_max_latency":1200,"data_collection":"deny","enforce_distillable_text":true},"transforms":["middle-out"],"route":"fallback"}`),
	})
	adapter := &stubAdapter{platform: "openrouter"}
	doer := &stubDoer{respStatus: 200, respBody: openAIHCSFResponse}
	d := newDispatcherForTest(adapter, doer)

	_, err := d.DispatchHCSF(ctx, env)
	if err != nil {
		t.Fatalf("DispatchHCSF openrouter_chat alias: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(adapter.lastInput.InboundBody, &body); err != nil {
		t.Fatalf("openrouter body json: %v\n%s", err, adapter.lastInput.InboundBody)
	}
	if body["model"] != "openrouter/auto" {
		t.Fatalf("openrouter body model=%v want HCSF upstream model; body=%s", body["model"], adapter.lastInput.InboundBody)
	}
	msgs := body["messages"].([]any)
	if msgs[0].(map[string]any)["content"] != "graph text" {
		t.Fatalf("openrouter messages = %+v; want CapabilityGraph text", msgs)
	}
	providerBody, ok := body["provider"].(map[string]any)
	if !ok {
		t.Fatalf("openrouter provider routing controls missing: %s", adapter.lastInput.InboundBody)
	}
	order := providerBody["order"].([]any)
	if order[0] != "anthropic" || providerBody["allow_fallbacks"] != false {
		t.Fatalf("provider routing controls = %+v", providerBody)
	}
	sortControls, ok := providerBody["sort"].(map[string]any)
	if !ok || sortControls["price"] != 0.7 || sortControls["latency"] != 0.2 || sortControls["throughput"] != 0.1 {
		t.Fatalf("provider.sort controls missing or changed: %+v", providerBody["sort"])
	}
	only, ok := providerBody["only"].([]any)
	if !ok || len(only) != 2 || only[0] != "anthropic" || only[1] != "openai" {
		t.Fatalf("provider.only = %+v; want [anthropic openai]", providerBody["only"])
	}
	maxPrice, ok := providerBody["max_price"].(map[string]any)
	if !ok || maxPrice["prompt"] != 0.000001 || maxPrice["completion"] != 0.000002 {
		t.Fatalf("provider.max_price = %+v; want prompt/completion caps", providerBody["max_price"])
	}
	if providerBody["zdr"] != true ||
		providerBody["preferred_min_throughput"] != float64(80) ||
		providerBody["preferred_max_latency"] != float64(1200) ||
		providerBody["data_collection"] != "deny" ||
		providerBody["enforce_distillable_text"] != true {
		t.Fatalf("openrouter provider policy/perf controls missing: %+v", providerBody)
	}
	transforms, ok := body["transforms"].([]any)
	if !ok || transforms[0] != "middle-out" || body["route"] != "fallback" {
		t.Fatalf("openrouter raw passthrough controls missing: transforms=%+v route=%v body=%s", body["transforms"], body["route"], adapter.lastInput.InboundBody)
	}
}

func TestDispatchHCSFCrossProtocolAliasDoesNotPassthroughSourceRawFields(t *testing.T) {
	env := testHCSFEnvelope()
	env.RequestMeta.ClientProtocol = proto.ClientProtocolAnthropicMessages
	env.RequestMeta.ProtocolFamily = "deepseek_chat"
	env.RequestMeta.EndpointFamily = "deepseek_chat"
	env.RequestMeta.Provider = "deepseek"
	env.RequestMeta.Model = "deepseek-chat"
	env.RequestMeta.UpstreamModel = "deepseek-chat"
	ctx := ContextWithHCSFDispatchInput(context.Background(), HCSFDispatchInput{
		ProtocolFamily:  "deepseek_chat",
		UpstreamModelID: "deepseek-chat",
		Account:         provider.AccountInfo{AccountID: 14, Platform: "deepseek", AccountType: "apikey"},
		Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-cross-protocol-test"},
		RawBody:         []byte(`{"system":"anthropic raw system","stop_sequences":["END"],"metadata":{"source":"anthropic"}}`),
	})
	adapter := &stubAdapter{platform: "deepseek"}
	doer := &stubDoer{respStatus: 200, respBody: openAIHCSFResponse}
	d := newDispatcherForTest(adapter, doer)

	_, err := d.DispatchHCSF(ctx, env)
	if err != nil {
		t.Fatalf("DispatchHCSF anthropic_messages -> deepseek_chat: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(adapter.lastInput.InboundBody, &body); err != nil {
		t.Fatalf("cross-protocol body json: %v\n%s", err, adapter.lastInput.InboundBody)
	}
	for _, key := range []string{"system", "stop_sequences", "metadata"} {
		if _, ok := body[key]; ok {
			t.Fatalf("cross-protocol raw field %q leaked into OpenAI-compatible body: %s", key, adapter.lastInput.InboundBody)
		}
	}
	if body["model"] != "deepseek-chat" {
		t.Fatalf("cross-protocol body model=%v want deepseek-chat; body=%s", body["model"], adapter.lastInput.InboundBody)
	}
	msgs := body["messages"].([]any)
	if msgs[0].(map[string]any)["content"] != "graph text" {
		t.Fatalf("cross-protocol messages = %+v; want CapabilityGraph text", msgs)
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

// MUTATION: DispatchHCSF 构造 provider.BuildInput 时丢 InboundBetaTokens
// 映射 → 红(DM-03 HCSF 路径穿线守卫)。
func TestDispatchHCSFPassesInboundBetaTokensToAdapter(t *testing.T) {
	adapter := &stubAdapter{platform: "openai"}
	doer := &stubDoer{respStatus: 200, respBody: openAIHCSFResponse}
	d := newDispatcherForTest(adapter, doer)

	ctx := ContextWithHCSFDispatchInput(context.Background(), HCSFDispatchInput{
		ProtocolFamily:    "openai_chat",
		UpstreamModelID:   "gpt-4o-upstream",
		Account:           provider.AccountInfo{AccountID: 7, Platform: "openai", AccountType: "apikey"},
		Credential:        provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-test"},
		InboundBetaTokens: []string{"context-management-2025-06-27"},
	})
	if _, err := d.DispatchHCSF(ctx, testHCSFEnvelope()); err != nil {
		t.Fatalf("DispatchHCSF: %v", err)
	}
	got := adapter.lastInput.InboundBetaTokens
	if len(got) != 1 || got[0] != "context-management-2025-06-27" {
		t.Fatalf("adapter InboundBetaTokens=%v; want HCSF 路径完整透传", got)
	}
}
