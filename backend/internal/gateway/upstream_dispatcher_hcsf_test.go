package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/vertex"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
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

func TestDispatchHCSFAutoTransportModeUsesSessionMimicry(t *testing.T) {
	t.Setenv("HUAKAI_TRANSPORT_MIMICRY", "true")
	standardCalls, mimicryCalls := 0, 0
	standard := dispatcherRoundTripFunc(func(*http.Request) (*http.Response, error) {
		standardCalls++
		return nil, errors.New("HCSF session 账号不得落入 standard transport")
	})
	mimicry := dispatcherRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		mimicryCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(openAIHCSFResponse)),
			Request:    req,
		}, nil
	})
	factory := transport.NewFactory()
	factory.SetStandard(standard)
	factory.SetMimicry(mimicry)
	adapter := &stubAdapter{platform: "copilot"}
	dispatcher := &UpstreamDispatcher{
		Adapters:         &stubRegistry{adapter: adapter},
		TransportFactory: factory,
	}
	env := testHCSFEnvelope()
	env.RequestMeta.ProtocolFamily = "copilot_session"
	env.RequestMeta.EndpointFamily = "copilot_session"
	env.RequestMeta.Provider = "copilot"
	ctx := ContextWithHCSFDispatchInput(context.Background(), HCSFDispatchInput{
		ProtocolFamily:  "copilot_session",
		UpstreamModelID: "gpt-4o-upstream",
		Account: provider.AccountInfo{
			AccountID: 7, Platform: "copilot", AccountType: credentialstore.AuthModeCopilotOAuth,
		},
		Credential: provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "session-token"},
	})

	got, err := dispatcher.DispatchHCSF(ctx, env)
	if err != nil {
		t.Fatalf("DispatchHCSF: %v", err)
	}
	if got.BufferedResponse == nil || got.BufferedResponse.Model != "gpt-4o-upstream" {
		t.Fatalf("buffered response=%+v", got.BufferedResponse)
	}
	if standardCalls != 0 || mimicryCalls != 1 {
		t.Fatalf("transport calls standard/mimicry=%d/%d want 0/1", standardCalls, mimicryCalls)
	}
	if adapter.lastInput.Account.Platform != string(transport.ProviderCopilot) {
		t.Fatalf("adapter account platform=%q want %q", adapter.lastInput.Account.Platform, transport.ProviderCopilot)
	}
}

func TestDispatchHCSFExplicitStandardStillNormalizesProvider(t *testing.T) {
	standardCalls := 0
	factory := transport.NewFactory()
	factory.SetStandard(dispatcherRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		standardCalls++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(openAIHCSFResponse)),
			Request:    req,
		}, nil
	}))
	adapter := &stubAdapter{platform: "copilot"}
	dispatcher := &UpstreamDispatcher{
		Adapters:         &stubRegistry{adapter: adapter},
		TransportFactory: factory,
	}
	env := testHCSFEnvelope()
	env.RequestMeta.ProtocolFamily = "copilot_session"
	env.RequestMeta.EndpointFamily = "copilot_session"
	env.RequestMeta.Provider = "openai"
	ctx := ContextWithHCSFDispatchInput(context.Background(), HCSFDispatchInput{
		ProtocolFamily:  "copilot_session",
		UpstreamModelID: "gpt-4o-upstream",
		Account: provider.AccountInfo{
			AccountID: 7, Platform: "openai", AccountType: credentialstore.AuthModeCopilotOAuth,
		},
		Credential:    provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "session-token"},
		TransportMode: transport.TransportModeStandard,
	})

	got, err := dispatcher.DispatchHCSF(ctx, env)
	if err != nil {
		t.Fatalf("DispatchHCSF: %v", err)
	}
	if got.BufferedResponse == nil || standardCalls != 1 {
		t.Fatalf("buffered response=%+v standard calls=%d", got.BufferedResponse, standardCalls)
	}
	if adapter.lastInput.Account.Platform != string(transport.ProviderCopilot) {
		t.Fatalf("adapter account platform=%q want %q", adapter.lastInput.Account.Platform, transport.ProviderCopilot)
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

// cursor_session 刻意留在 marshal fail-closed 例外表(上游是 Connect/proto
// 帧,openai_chat JSON 投影不可解析,见 hcsfProviderRequestModelFamily 排除
// 注释;OCAW 采集确认真实形态前不接)。本测试守:该族走 HCSF 非流式时在
// marshal 处 fail-closed,绝不把客户端 raw body 透传到上游。
// 变异:往 hcsfProviderRequestModelFamily 加 cursor_session→openai_chat
// → err==nil 断言红(同时守卫测试的例外表反向断言也红)。
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
			}, env, tc.family, tc.family, []byte(`{"raw_client_marker":"`+tc.marker+`"}`), nil)
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

// TestBuildHCSFProviderRequestNativeRawIngressGuard 非流式 native-raw 直转的
// 跨协议守卫(DM-20 评审 S2):许可集与流式 needsStreamingHCSFTranslation 严格
// 镜像——同族/空 ingress 直通,anthropic→bedrock 走 adapter 内 AutoTranslate
// 直通,openai_responses→openai_codex 同为 Responses 形直通,openai/anthropic
// →codex 已改走 canonical marshal,openai→bedrock 仍 fail-closed。
// 变异: 删 validateNativeRawBodyIngress 调用(恢复 fail-open)→ anthropic
// body 原样直发 bedrock / openai body 进 bedrock 嗅探误译 → wantErr 用例 RED;
// 把 anthropic→bedrock 许可删掉 → AutoTranslate 合法路径被误杀 → 该用例 RED。
func TestBuildHCSFProviderRequestNativeRawIngressGuard(t *testing.T) {
	for _, tc := range []struct {
		name           string
		ingressFamily  string
		endpointFamily string
		wantErr        bool
	}{
		{"responses→codex Responses形直通", "openai_responses", "openai_codex", false},
		{"openai→bedrock fail-closed(嗅探误译路径)", "openai_chat", "bedrock_invoke", true},
		{"anthropic→bedrock AutoTranslate 直通", "anthropic_messages", "bedrock_invoke", false},
		{"同族 codex 直通", "openai_codex", "openai_codex", false},
		{"空 ingress 回退直通", "", "bedrock_invoke", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testHCSFEnvelope()
			env.RequestMeta.EndpointFamily = tc.endpointFamily
			adapter := &stubAdapter{platform: "native"}

			_, err := buildHCSFProviderRequest(context.Background(), adapter, provider.BuildInput{
				UpstreamModelID: "m",
				Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
				Account:         provider.AccountInfo{AccountID: 12, Platform: "native", AccountType: "native"},
			}, env, tc.ingressFamily, tc.endpointFamily, []byte(`{"raw":"body"}`), nil)

			if tc.wantErr && err == nil {
				t.Fatalf("ingress=%q endpoint=%q 应 fail-closed,got nil err(垃圾 body 将直发上游)", tc.ingressFamily, tc.endpointFamily)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ingress=%q endpoint=%q 应直通,got err=%v", tc.ingressFamily, tc.endpointFamily, err)
			}
			if !tc.wantErr && !strings.Contains(string(adapter.lastInput.InboundBody), `"raw":"body"`) {
				t.Fatalf("直通路径 body 丢失: %s", adapter.lastInput.InboundBody)
			}
		})
	}
}

func TestBuildHCSFProviderRequestCodexCrossProtocolMarshalsResponsesShape(t *testing.T) {
	for _, tc := range []struct {
		name          string
		ingressFamily string
	}{
		{"openai→codex", "openai_chat"},
		{"anthropic→codex", "anthropic_messages"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testHCSFEnvelope()
			env.RequestMeta.EndpointFamily = "openai_codex"
			env.RequestControls.SystemPrompt = "system policy"
			adapter := &stubAdapter{platform: "codex"}

			req, err := buildHCSFProviderRequest(context.Background(), adapter, provider.BuildInput{
				UpstreamModelID: "gpt-5-codex",
				Credential:      provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "secret"},
				Account:         provider.AccountInfo{AccountID: 12, Platform: "codex", AccountType: "native"},
			}, env, tc.ingressFamily, "openai_codex", []byte(`{"raw":"body"}`), nil)
			if err != nil {
				t.Fatalf("buildHCSFProviderRequest: %v", err)
			}
			if strings.Contains(string(adapter.lastInput.InboundBody), `"raw":"body"`) {
				t.Fatalf("cross-protocol codex must not native-raw forward body: %s", adapter.lastInput.InboundBody)
			}
			var body map[string]any
			if err := json.Unmarshal(adapter.lastInput.InboundBody, &body); err != nil {
				t.Fatalf("built body json: %v\n%s", err, adapter.lastInput.InboundBody)
			}
			if _, ok := body["input"].([]any); !ok {
				t.Fatalf("codex body missing Responses input array: %+v", body)
			}
			if body["instructions"] != "system policy" {
				t.Fatalf("instructions=%v want system policy; body=%+v", body["instructions"], body)
			}
			if _, ok := body["messages"]; ok {
				t.Fatalf("codex body must not use chat messages shape: %+v", body)
			}
			wire, _ := io.ReadAll(req.Body)
			if !strings.Contains(string(wire), `"input"`) || strings.Contains(string(wire), `"raw":"body"`) {
				t.Fatalf("wire body=%s want Responses shape without native raw marker", wire)
			}
		})
	}
}

// TestBuildHCSFProviderRequestVertexFamiliesProduceCorrectOutboundBody 钉死非
// 流式 HCSF 全链对两个 Vertex 族的出站 body 形态:
//   - vertex_gemini  → marshal 出 gemini_messages body,vertex adapter(ModeGemini)
//     原样直通到 publishers/google endpoint;出站 body 必是 Gemini 形(contents/
//     generationConfig),绝不含 anthropic_version。
//   - vertex_anthropic → marshal 出标准 anthropic_messages body,vertex adapter
//     (ModeAnthropic)再剥 model/stream + 注 anthropic_version(两步串联);出站
//     body 必含 anthropic_version=vertex-2023-10-16 且无顶层 model/stream,
//     publishers/anthropic + rawPredict。
//
// 判别性:用真实 vertex.PassthroughAdapter(非 stub),从实际 *http.Request 读
// 出站 body;漏 hcsfProviderRequestModelFamily 映射 → marshal unsupported 报错;
// 漏 ModeAnthropic reshape → anthropic 出站缺 anthropic_version 断言红;
// Gemini 误走 reshape → 出站含 anthropic_version 断言红。
func TestBuildHCSFProviderRequestVertexFamiliesProduceCorrectOutboundBody(t *testing.T) {
	t.Run("vertex_gemini", func(t *testing.T) {
		env := testHCSFEnvelope()
		env.RequestMeta.EndpointFamily = "vertex_gemini"
		env.RequestMeta.Provider = "vertex"
		env.RequestMeta.UpstreamModel = "gemini-2.5-pro"
		adapter := &vertex.PassthroughAdapter{Mode: vertex.ModeGemini}

		req, err := buildHCSFProviderRequest(context.Background(), adapter, provider.BuildInput{
			UpstreamModelID: "gemini-2.5-pro",
			Credential: provider.Credential{
				Type:  provider.CredentialTypeUpstreamPassthrough,
				Value: "Bearer vertex-tok",
				Extra: map[string]string{"project_id": "p", "auth_header": "Authorization"},
			},
			Account: provider.AccountInfo{AccountID: 1, Platform: "vertex", AccountType: "vertex_sa"},
		}, env, "vertex_gemini", "vertex_gemini", nil, nil)
		if err != nil {
			t.Fatalf("buildHCSFProviderRequest(vertex_gemini): %v", err)
		}
		if got, want := req.URL.String(), "https://us-central1-aiplatform.googleapis.com/v1/projects/p/locations/us-central1/publishers/google/models/gemini-2.5-pro:generateContent"; got != want {
			t.Fatalf("vertex_gemini URL=%q want %q", got, want)
		}
		body, _ := io.ReadAll(req.Body)
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("vertex_gemini body json: %v\n%s", err, body)
		}
		// Gemini 形:必有 contents（marshalGeminiMessages 投影），绝无 anthropic_version。
		if _, ok := m["contents"]; !ok {
			t.Fatalf("vertex_gemini body 缺 contents（应是 Gemini 形）: %s", body)
		}
		if _, ok := m["anthropic_version"]; ok {
			t.Fatalf("vertex_gemini body 误注 anthropic_version（Gemini 模式应原样直通）: %s", body)
		}
	})

	t.Run("vertex_anthropic", func(t *testing.T) {
		env := testHCSFEnvelope()
		env.RequestMeta.EndpointFamily = "vertex_anthropic"
		env.RequestMeta.Provider = "vertex"
		env.RequestMeta.UpstreamModel = "claude-opus-4-1"
		adapter := &vertex.PassthroughAdapter{Mode: vertex.ModeAnthropic}

		req, err := buildHCSFProviderRequest(context.Background(), adapter, provider.BuildInput{
			UpstreamModelID: "claude-opus-4-1",
			Credential: provider.Credential{
				Type:  provider.CredentialTypeUpstreamPassthrough,
				Value: "Bearer vertex-tok",
				Extra: map[string]string{"project_id": "p", "location": "us-east5", "auth_header": "Authorization"},
			},
			Account: provider.AccountInfo{AccountID: 2, Platform: "vertex", AccountType: "vertex_anthropic"},
		}, env, "vertex_anthropic", "vertex_anthropic", nil, nil)
		if err != nil {
			t.Fatalf("buildHCSFProviderRequest(vertex_anthropic): %v", err)
		}
		if got, want := req.URL.String(), "https://us-east5-aiplatform.googleapis.com/v1/projects/p/locations/us-east5/publishers/anthropic/models/claude-opus-4-1:rawPredict"; got != want {
			t.Fatalf("vertex_anthropic URL=%q want %q", got, want)
		}
		body, _ := io.ReadAll(req.Body)
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatalf("vertex_anthropic body json: %v\n%s", err, body)
		}
		// 两步串联终态:Vertex Anthropic 形——必含 anthropic_version、无顶层 model/stream。
		if m["anthropic_version"] != vertex.AnthropicVersionVertex {
			t.Fatalf("vertex_anthropic body anthropic_version=%v want %q（marshal+reshape 链断裂）: %s", m["anthropic_version"], vertex.AnthropicVersionVertex, body)
		}
		if _, ok := m["model"]; ok {
			t.Fatalf("vertex_anthropic body 残留顶层 model（reshape 未剥）: %s", body)
		}
		if _, ok := m["stream"]; ok {
			t.Fatalf("vertex_anthropic body 残留顶层 stream（reshape 未剥）: %s", body)
		}
		// marshal 投影的 messages 必须存活（标准 anthropic body 主体）。
		if _, ok := m["messages"]; !ok {
			t.Fatalf("vertex_anthropic body 缺 messages: %s", body)
		}
	})
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
		// 12 个后补兼容族的 dispatch 级代表(marshal 级守卫见
		// TestMarshalCompatFamiliesProjectToOpenAIChat):映射缺失时本用例
		// 在 DispatchHCSF 即报 unsupported,必红。
		{name: "late_compat_family_alias", family: "kimi_chat", provider: "kimi", model: "kimi-k2"},
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
				// 本用例只验证 HCSF 线形映射；显式 standard，避免 session alias
				// 的生产拟态选择掺入独立的 transport 模板依赖。
				TransportMode: transport.TransportModeStandard,
				RawBody:       []byte(`{"model":"raw-stale-model","messages":[{"role":"user","content":"raw stale"}],"max_tokens":5,"max_completion_tokens":64,"seed":999,"n":2,"frequency_penalty":0.75,"logit_bias":{"198":-100},"metadata":{"trace":"alias-raw-control"}}`),
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

// 变异: DispatchHCSF 构造 provider.BuildInput 时丢 InboundBetaTokens
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

// TestReadBufferedUpstreamResponseDetectsOverflow 守 S2-7 核心: 读上游 buffered 响应必须
// 用 limit+1 哨兵探测溢出, 否则 >1MiB 响应被静默截断且无人知晓。
// 变异: 把 readBufferedUpstreamResponse 的 LimitReader 改回 maxBufferedUpstreamResponseBytes
// (无 +1) → over body 读到恰好 limit 字节 → len==limit 非 >limit → oversized=false → RED。
func TestReadBufferedUpstreamResponseDetectsOverflow(t *testing.T) {
	// 恰好上限: 不算 oversized, 全量返回。
	atLimit := strings.Repeat("a", maxBufferedUpstreamResponseBytes)
	raw, oversized, err := readBufferedUpstreamResponse(strings.NewReader(atLimit))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if oversized {
		t.Fatalf("body 恰好等于上限不应判 oversized")
	}
	if len(raw) != maxBufferedUpstreamResponseBytes {
		t.Fatalf("at-limit raw len=%d want %d", len(raw), maxBufferedUpstreamResponseBytes)
	}
	// 超 1 字节: 判 oversized, raw 截断到上限。
	over := strings.Repeat("a", maxBufferedUpstreamResponseBytes+1)
	raw, oversized, err = readBufferedUpstreamResponse(strings.NewReader(over))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if !oversized {
		t.Fatalf("body 超上限必须判 oversized(漏 +1 哨兵 = 静默截断 bug)")
	}
	if len(raw) != maxBufferedUpstreamResponseBytes {
		t.Fatalf("oversized raw 应截断到上限, got %d", len(raw))
	}
}

// TestDispatchHCSFRejectsOversizedSuccessResponse 守 S2-7: 上游 2xx 成功响应 >1MiB 必须在
// canonicalize 前被拒为 ErrUpstreamResponseTooLarge, 而非把截断字节喂 ProviderResponseToCanonical
// (静默截断→opaque 502 / SSE 形按部分响应错计费)。
// 变异: 删 DispatchHCSF 内 `if oversized { return ErrUpstreamResponseTooLarge }` → 截断体进
// adapter → err 变 nil 或 parse 错(均 ≠ ErrUpstreamResponseTooLarge) → RED。
func TestDispatchHCSFRejectsOversizedSuccessResponse(t *testing.T) {
	big := strings.Repeat("x", maxBufferedUpstreamResponseBytes+100)
	oversized := `{"id":"chatcmpl-big","object":"chat.completion","model":"gpt-4o-upstream","choices":[{"index":0,"message":{"role":"assistant","content":"` + big + `"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	adapter := &stubAdapter{platform: "openai"}
	doer := &stubDoer{respStatus: 200, respBody: oversized}
	d := newDispatcherForTest(adapter, doer)

	_, err := d.DispatchHCSF(hcsfCtx(), testHCSFEnvelope())
	if !errors.Is(err, ErrUpstreamResponseTooLarge) {
		t.Fatalf("DispatchHCSF err=%v; want ErrUpstreamResponseTooLarge for >1MiB 2xx body", err)
	}
}

// TestDispatchHCSFOversizedNon2xxStaysUpstreamHTTPError 守: 非 2xx 上游响应即便超 1MiB, 仍须
// 作 UpstreamHTTPError(带截断 body 供错误分类), 不能被当 too-large 拒绝(镜像 legacy oversizedNon2xx)。
// 变异: 把 oversized 检查挪到 status 判断之前 → 非 2xx oversized 被误拒为 too-large → RED。
func TestDispatchHCSFOversizedNon2xxStaysUpstreamHTTPError(t *testing.T) {
	big := strings.Repeat("x", maxBufferedUpstreamResponseBytes+100)
	adapter := &stubAdapter{platform: "openai"}
	doer := &stubDoer{respStatus: 500, respBody: big}
	d := newDispatcherForTest(adapter, doer)

	_, err := d.DispatchHCSF(hcsfCtx(), testHCSFEnvelope())
	if errors.Is(err, ErrUpstreamResponseTooLarge) {
		t.Fatalf("非 2xx oversized 不应被当 too-large 拒绝")
	}
	var upstreamErr *UpstreamHTTPError
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("非 2xx oversized 应仍是 UpstreamHTTPError(供错误分类), got %v", err)
	}
	if upstreamErr.StatusCode != 500 {
		t.Fatalf("UpstreamHTTPError.StatusCode=%d want 500", upstreamErr.StatusCode)
	}
}
