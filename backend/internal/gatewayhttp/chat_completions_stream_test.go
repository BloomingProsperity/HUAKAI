package gatewayhttp

// TestInjectStreamingRequestControlsResponseFormatRawPassthrough_OpenAIChat /
// TestInjectStreamingRequestControlsResponseFormatRawPassthrough_OpenAIResponses
// 守 P2-B 修复在流式 marshal 路径(injectStreamingRequestControls)。同非流式
// 测试逻辑:Type:"raw" 时 marshal 必须 1:1 还原 Schema 整体,不能再包
// {"type":"raw","schema":...}。
//
// Mutation:改回 raw-wrap 逻辑时两 用例必红 — response_format.type 变 "raw"
// 而非 inbound 'json_object',或 text.format 嵌套出多一层 schema 包壳。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auditledger"
	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/credentialstore"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/anthropic"
	providercopilot "github.com/BloomingProsperity/HUAKAI/internal/provider/copilot"
	provideropenai "github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/sign"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

func TestDeliveryTrackerMarksStartedOnWriteAndWriteHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	tracker := newDeliveryTracker(rec)
	if tracker.started() {
		t.Fatal("new tracker must start as not delivered")
	}
	if _, err := tracker.Write([]byte("data: hello\n\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !tracker.started() {
		t.Fatal("Write must mark delivery started")
	}
	if tracker.statusCode() != http.StatusOK {
		t.Fatalf("status=%d want 200 after implicit WriteHeader", tracker.statusCode())
	}

	rec = httptest.NewRecorder()
	tracker = newDeliveryTracker(rec)
	tracker.WriteHeader(http.StatusAccepted)
	if !tracker.started() {
		t.Fatal("WriteHeader must mark delivery started")
	}
	if tracker.statusCode() != http.StatusAccepted {
		t.Fatalf("status=%d want 202", tracker.statusCode())
	}
}

func TestHandleStreamingResponse_CrossProtocolTranslatesRequestAndResponse(t *testing.T) {
	upstream := &recordingStreamingDoer{responseBody: anthropicStreamingFixture()}
	adapterReg := provider.NewStaticRegistry()
	adapterReg.MustRegister("anthropic_messages", &anthropic.PassthroughAdapter{})
	reqBody := []byte(`{"model":"gpt-4o","stream":true,"max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(reqBody)))
	ex := &chatExecution{
		d: ChatHandlerDeps{
			Dispatcher: &gateway.UpstreamDispatcher{
				Adapters:         adapterReg,
				TransportFactory: transport.NewFactory(),
				HTTPClient:       upstream,
			},
			Forwarder: &gateway.StreamForwarder{
				ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry(),
				Scanners:         gateway.BuildDefaultStreamScannerRegistry(),
				ScannerBufferCap: 1 << 20,
			},
			Settler: &stubSettler{},
		},
		r:                 req,
		ctx:               req.Context(),
		startedAt:         time.Now(),
		ident:             auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 3},
		body:              reqBody,
		req:               chatRequest{Model: "gpt-4o", Stream: true},
		clientProtocol:    proto.ClientProtocolOpenAIChat,
		requestID:         "req_stream_cross_protocol",
		resolved:          registry.Resolved{ProtocolFamily: "anthropic_messages", ProviderModelID: "claude-3-5-haiku"},
		plan:              router.RoutePlan{SnapshotVersion: "test-snapshot"},
		attempt:           router.AttemptPlan{PoolGroupID: 42},
		routeID:           "route-1",
		reserveRes:        &billing.ReserveResult{ClaimID: 999},
		acquiredAccountID: 1,
		upstreamModelID:   "claude-3-5-haiku",
		cacheVendor:       "anthropic",
		cred:              provider.Credential{Type: provider.CredentialTypeAPIKey, Value: "sk-ant-test"},
		accInfo:           provider.AccountInfo{AccountID: 1, Platform: "anthropic", AccountType: "apikey"},
		forwardReq: gateway.ForwardRequest{
			TenantID:       7,
			AccountID:      1,
			RequestID:      "req_stream_cross_protocol",
			RouteID:        "route-1",
			PoolID:         "42",
			IngressPath:    "/v1/chat/completions",
			ProtocolFamily: "anthropic_messages",
			ClientProtocol: string(proto.ClientProtocolOpenAIChat),
			Model:          "claude-3-5-haiku",
			RequestedModel: "gpt-4o",
			Provider:       "anthropic",
		},
	}

	rec := httptest.NewRecorder()
	ex.handleStreamingResponse(rec)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	var sent map[string]any
	if err := json.Unmarshal(upstream.body, &sent); err != nil {
		t.Fatalf("upstream body json: %v\n%s", err, string(upstream.body))
	}
	if sent["model"] != "claude-3-5-haiku" {
		t.Fatalf("upstream model=%v want claude-3-5-haiku; body=%s", sent["model"], string(upstream.body))
	}
	if sent["stream"] != true {
		t.Fatalf("upstream stream=%v want true; body=%s", sent["stream"], string(upstream.body))
	}
	if sent["max_tokens"] != float64(16) {
		t.Fatalf("upstream max_tokens=%v want 16; body=%s", sent["max_tokens"], string(upstream.body))
	}
	messages, ok := sent["messages"].([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("upstream messages=%T/%v; body=%s", sent["messages"], sent["messages"], string(upstream.body))
	}
	first, ok := messages[0].(map[string]any)
	if !ok {
		t.Fatalf("first message=%T", messages[0])
	}
	content, ok := first["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("translated content=%T/%v; body=%s", first["content"], first["content"], string(upstream.body))
	}
	block, ok := content[0].(map[string]any)
	if !ok || block["type"] != "text" || block["text"] != "hi" {
		t.Fatalf("translated block=%T/%v; body=%s", content[0], content[0], string(upstream.body))
	}
	if strings.Contains(string(upstream.body), `"content":"hi"`) {
		t.Fatalf("raw OpenAI body leaked to Anthropic upstream: %s", string(upstream.body))
	}
	clientBody := rec.Body.String()
	if strings.Contains(clientBody, "event: message_start") {
		t.Fatalf("raw Anthropic SSE leaked to OpenAI client: %s", clientBody)
	}
	if !strings.Contains(clientBody, `"object":"chat.completion.chunk"`) || !strings.Contains(clientBody, "data: [DONE]") {
		t.Fatalf("client body did not use OpenAI Chat SSE shape: %s", clientBody)
	}
}

type recordingStreamingDoer struct {
	body         []byte
	responseBody string
}

func (d *recordingStreamingDoer) Do(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	d.body = body
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(d.responseBody)),
	}, nil
}

type selectiveTransportRoundTripper struct {
	called       bool
	err          error
	statusCode   int
	responseBody string
}

func (rt *selectiveTransportRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.called = true
	if rt.err != nil {
		return nil, rt.err
	}
	status := rt.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(rt.responseBody)),
		Request:    req,
	}, nil
}

func TestChatCompletions_ReverseSessionUsesMimicryTransport(t *testing.T) {
	// Mutation check: if chatExecution stops passing TransportMode into either
	// Dispatch or DispatchHCSF, these cases take the standard transport and fail
	// before delivery instead of returning the fixture response.
	t.Run("streaming raw dispatch", func(t *testing.T) {
		standard, mimicry, dispatcher := copilotTransportModeDispatcher(t, openAIStreamingFixture())
		reqBody := []byte(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
		req := httptest.NewRequest(http.MethodPost, "/v1/copilot/session", bytes.NewReader(reqBody))
		ex := &chatExecution{
			d: ChatHandlerDeps{
				Dispatcher: dispatcher,
				Forwarder: &gateway.StreamForwarder{
					ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry(),
					Scanners:         gateway.BuildDefaultStreamScannerRegistry(),
					ScannerBufferCap: 1 << 20,
				},
				Settler: &stubSettler{},
			},
			r:                 req,
			ctx:               req.Context(),
			startedAt:         time.Now(),
			ident:             auth.Identity{TenantID: 7, APIKeyID: 11, UserID: 3},
			body:              reqBody,
			req:               chatRequest{Model: "gpt-4o", Stream: true},
			clientProtocol:    proto.ClientProtocol("copilot_session"),
			requestID:         "req_transport_mode_stream",
			resolved:          registry.Resolved{ProtocolFamily: "copilot_session", ProviderModelID: "gpt-4o"},
			plan:              router.RoutePlan{SnapshotVersion: "test-snapshot"},
			attempt:           router.AttemptPlan{PoolGroupID: 42},
			routeID:           "route-1",
			reserveRes:        &billing.ReserveResult{ClaimID: 999},
			acquiredAccountID: 1,
			upstreamModelID:   "gpt-4o",
			cacheVendor:       "copilot",
			cred:              provider.Credential{Type: provider.CredentialTypeSessionToken, Value: "session-token"},
			accInfo:           provider.AccountInfo{AccountID: 1, TenantID: 7, Platform: credentialstore.VendorCopilot, AccountType: credentialstore.AuthModeCopilotOAuth},
			forwardReq: gateway.ForwardRequest{
				TenantID:       7,
				AccountID:      1,
				RequestID:      "req_transport_mode_stream",
				RouteID:        "route-1",
				PoolID:         "42",
				IngressPath:    "/v1/copilot/session",
				ProtocolFamily: "copilot_session",
				ClientProtocol: "copilot_session",
				Model:          "gpt-4o",
				RequestedModel: "gpt-4o",
				Provider:       "copilot",
			},
		}

		rec := httptest.NewRecorder()
		ex.handleStreamingResponse(rec)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
		}
		assertMimicryOnlyTransport(t, standard, mimicry)
	})

	t.Run("buffered HCSF dispatch", func(t *testing.T) {
		enableHCSFDispatchForTest(t)
		standard, mimicry, dispatcher := copilotTransportModeDispatcher(t, `{"id":"chatcmpl-transport-mode","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		deps := copilotTransportModeDeps(t, dispatcher)

		rec := invokeHandler(t, deps, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
		}
		assertMimicryOnlyTransport(t, standard, mimicry)
	})

	t.Run("buffered raw dispatch", func(t *testing.T) {
		t.Setenv("HUAKAI_DISPATCH_HCSF", "0")
		standard, mimicry, dispatcher := copilotTransportModeDispatcher(t, `{"id":"chatcmpl-transport-mode","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		deps := copilotTransportModeDeps(t, dispatcher)

		rec := invokeHandler(t, deps, `{"model":"gpt-4o","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
		}
		assertMimicryOnlyTransport(t, standard, mimicry)
	})
}

func TestTransportSelectionForDispatch_V2ReverseSessionShapes(t *testing.T) {
	cases := []struct {
		name           string
		account        provider.AccountInfo
		protocolFamily string
		wantPlatform   string
		wantMode       transport.TransportMode
	}{
		{
			name:           "openai codex auth mode uses chatgpt mimicry provider",
			account:        provider.AccountInfo{Platform: "openai", AccountType: credentialstore.AuthModeCodexCLIOAuth},
			protocolFamily: "openai_codex",
			wantPlatform:   string(transport.ProviderOpenAICodex),
			wantMode:       transport.TransportModeMimicryChatGPT,
		},
		{
			name:           "openai chatgpt auth mode uses chatgpt mimicry provider",
			account:        provider.AccountInfo{Platform: "openai", AccountType: credentialstore.AuthModeChatGPTOAuth},
			protocolFamily: "openai_codex",
			wantPlatform:   string(transport.ProviderOpenAICodex),
			wantMode:       transport.TransportModeMimicryChatGPT,
		},
		{
			name:           "gemini google one auth mode uses gemini advanced provider",
			account:        provider.AccountInfo{Platform: "gemini", AccountType: credentialstore.AuthModeGoogleOne},
			protocolFamily: "gemini_advanced_session",
			wantPlatform:   string(transport.ProviderGeminiAdvanced),
			wantMode:       transport.TransportModeMimicryGeminiAdvanced,
		},
		{
			name:           "gemini antigravity auth mode uses antigravity provider",
			account:        provider.AccountInfo{Platform: "gemini", AccountType: credentialstore.AuthModeAntigravity},
			protocolFamily: "antigravity_session",
			wantPlatform:   string(transport.ProviderAntigravity),
			wantMode:       transport.TransportModeMimicryAntigravity,
		},
		{
			name:           "openai api key remains standard",
			account:        provider.AccountInfo{Platform: "openai", AccountType: credentialstore.AuthModeAPIKey},
			protocolFamily: "openai_chat",
			wantPlatform:   string(transport.ProviderOpenAI),
			wantMode:       transport.TransportModeStandard,
		},
		{
			name:           "gemini api key remains standard",
			account:        provider.AccountInfo{Platform: "gemini", AccountType: credentialstore.AuthModeAIStudioAPIKey},
			protocolFamily: "gemini_messages",
			wantPlatform:   string(transport.ProviderGemini),
			wantMode:       transport.TransportModeStandard,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := transportSelectionForDispatch(tc.account, tc.protocolFamily)
			if got.account.Platform != tc.wantPlatform || got.mode != tc.wantMode {
				t.Fatalf("transport selection platform/mode=%q/%q want %q/%q", got.account.Platform, got.mode, tc.wantPlatform, tc.wantMode)
			}
			if err := transport.ValidateModeForProvider(transport.ProviderCode(got.account.Platform), got.mode); err != nil {
				t.Fatalf("selected transport is not policy-valid: %v", err)
			}
		})
	}
}

func copilotTransportModeDispatcher(t *testing.T, responseBody string) (*selectiveTransportRoundTripper, *selectiveTransportRoundTripper, *gateway.UpstreamDispatcher) {
	t.Helper()
	standard := &selectiveTransportRoundTripper{err: errors.New("standard transport must not be used for copilot session")}
	mimicry := &selectiveTransportRoundTripper{responseBody: responseBody}
	factory := transport.NewFactory()
	factory.SetStandard(standard)
	factory.SetMimicry(mimicry)

	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("copilot_session", &providercopilot.CopilotSessionAdapter{
		Endpoint: "https://copilot.example.test/chat/completions",
	})
	return standard, mimicry, &gateway.UpstreamDispatcher{
		Adapters:         adapters,
		TransportFactory: factory,
	}
}

func copilotTransportModeDeps(t *testing.T, dispatcher *gateway.UpstreamDispatcher) ChatHandlerDeps {
	t.Helper()
	vault := provider.NewStaticVault()
	if err := vault.Set(1, provider.Credential{
		Type:  provider.CredentialTypeSessionToken,
		Value: "session-token",
	}, provider.AccountInfo{
		AccountID:           1,
		TenantID:            7,
		Platform:            credentialstore.VendorCopilot,
		AccountType:         credentialstore.AuthModeCopilotOAuth,
		AccountCredentialID: 9101,
		CredentialVersion:   1,
	}); err != nil {
		t.Fatalf("vault.Set: %v", err)
	}

	deps := clientAdapterDeps(t)
	deps.Registry = stubRegistry{resolved: registry.Resolved{
		PublicAlias:      "gpt-4o",
		CanonicalModelID: "copilot/gpt-4o",
		ProviderModelID:  "gpt-4o",
		ProtocolFamily:   "copilot_session",
		PoolCandidates:   []int64{42},
	}}
	deps.CredentialVault = vault
	deps.Dispatcher = dispatcher
	deps.Forwarder = &gateway.StreamForwarder{
		ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry(),
		Scanners:         gateway.BuildDefaultStreamScannerRegistry(),
		ScannerBufferCap: 1 << 20,
	}
	return deps
}

func assertMimicryOnlyTransport(t *testing.T, standard, mimicry *selectiveTransportRoundTripper) {
	t.Helper()
	if standard.called {
		t.Fatal("standard transport was used for reverse session account")
	}
	if !mimicry.called {
		t.Fatal("mimicry transport was not used for reverse session account")
	}
}

type erroringStreamingDoer struct {
	body           []byte
	responsePrefix string
	err            error
}

func (d *erroringStreamingDoer) Do(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	d.body = body
	readErr := d.err
	if readErr == nil {
		readErr = io.ErrUnexpectedEOF
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       &failingReadCloser{remaining: []byte(d.responsePrefix), err: readErr},
	}, nil
}

type failingReadCloser struct {
	remaining []byte
	err       error
}

func (r *failingReadCloser) Read(p []byte) (int, error) {
	if len(r.remaining) > 0 {
		n := copy(p, r.remaining)
		r.remaining = r.remaining[n:]
		return n, nil
	}
	if r.err != nil {
		err := r.err
		r.err = nil
		return 0, err
	}
	return 0, io.EOF
}

func (r *failingReadCloser) Close() error {
	return nil
}

func anthropicStreamingFixture() string {
	return strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-3-5-haiku","usage":{"input_tokens":1}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"pong"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":1}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n")
}

func TestAT_GW_002_14_StreamingIdempotencyReplayRecordsSSEAndReplays(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	body := openAIStreamingRequestBody()
	claimID := int64(77701)
	settler := &recordingSettler{}

	firstDeps := streamingReplayDeps(t, claimID, false, openAIStreamingFixture(), replayStore)
	firstDeps.Settler = settler
	first := invokeWithIdempotencyKey(t, firstDeps, body, "stream-idem-main")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d want 200; body=%s", first.Code, first.Body.String())
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	if settler.calls[0].StreamAttempt == nil || !settler.calls[0].StreamAttempt.State.Chargeable() {
		t.Fatalf("graceful stream must commit a chargeable attempt; StreamAttempt=%#v", settler.calls[0].StreamAttempt)
	}
	if len(settler.aborts) != 0 {
		t.Fatalf("abort calls=%d want 0", len(settler.aborts))
	}
	if got := first.Header().Get("Content-Type"); !strings.HasPrefix(got, idempotencyReplayContentTypeSSE) {
		t.Fatalf("first Content-Type=%q want %s", got, idempotencyReplayContentTypeSSE)
	}
	stored, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID)
	if err != nil {
		t.Fatalf("lookup replay: %v", err)
	}
	if !ok {
		t.Fatal("stream replay record missing after successful settlement")
	}
	if stored.ContentType != idempotencyReplayContentTypeSSE {
		t.Fatalf("stored ContentType=%q want %q", stored.ContentType, idempotencyReplayContentTypeSSE)
	}
	if string(stored.ResponseBody) != first.Body.String() {
		t.Fatalf("stored body mismatch:\nstored=%s\nfirst=%s", string(stored.ResponseBody), first.Body.String())
	}

	secondDeps := streamingReplayDeps(t, claimID, true, "data: should-not-dispatch\n\n", replayStore)
	secondDeps.Dispatcher = &gateway.UpstreamDispatcher{}
	second := invokeWithIdempotencyKey(t, secondDeps, body, "stream-idem-main")
	if second.Code != http.StatusOK {
		t.Fatalf("replay status=%d want 200; body=%s", second.Code, second.Body.String())
	}
	if got := second.Header().Get("X-HUAKAI-Idempotency-Hit"); got != "true" {
		t.Fatalf("replay hit header=%q want true", got)
	}
	if got := second.Header().Get("Content-Type"); !strings.HasPrefix(got, idempotencyReplayContentTypeSSE) {
		t.Fatalf("replay Content-Type=%q want %s", got, idempotencyReplayContentTypeSSE)
	}
	if got := second.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("replay Cache-Control=%q want no-cache", got)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replay body mismatch:\nfirst=%s\nsecond=%s", first.Body.String(), second.Body.String())
	}
}

func TestStreamingLedgerAppendAndDLQFailureProductionDoesNotSettle(t *testing.T) {
	// Risk killed: streaming Append+DLQ double failure must not become a
	// chargeable 200 with no audit row. Mutation self-check: removing the
	// post-Forward ledger-result settle gate records a settle for this fixture;
	// removing trailer reconciliation leaves StreamState=partial instead of failed.
	t.Setenv("HUAKAI_RELEASE_MODE", "production")
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	settler := &recordingSettler{}
	dlqSink := &recordingGatewayAuditLedgerDLQ{id: 0, err: errors.New("dlq unavailable")}
	deps := streamingReplayDeps(t, 77708, false, openAIStreamingFixture(), nil)
	deps.AuditLedger = &failingAppendLedger{appendErr: errors.New("ledger unavailable")}
	deps.AuditLedgerDLQ = dlqSink
	deps.Signer = signer
	deps.Settler = settler

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want stream response already delivered; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "pong") {
		t.Fatalf("stream body=%s want delivered fixture content", rec.Body.String())
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0 when streaming ledger has no durable result", len(settler.calls))
	}
	if len(settler.aborts) != 1 || settler.aborts[0].reason != "audit_ledger_error" {
		t.Fatalf("aborts=%+v want one audit_ledger_error abort", settler.aborts)
	}
	if len(dlqSink.events) != 1 {
		t.Fatalf("DLQ events=%d want 1 attempted enqueue", len(dlqSink.events))
	}
	result := rec.Result()
	if got := result.Trailer.Get(headerHUAKAIStreamState); got != "failed" {
		t.Fatalf("%s trailer=%q want failed for fail-closed ledger abort", headerHUAKAIStreamState, got)
	}
	if got := result.Trailer.Get(headerHUAKAIStreamState); got == "partial" {
		t.Fatalf("%s trailer must not stay chargeable partial after fail-closed abort", headerHUAKAIStreamState)
	}
}

func TestStreamingPersistedLedgerIDIsTrailerOnly(t *testing.T) {
	// Risk killed: C-13 moves streaming ledger emission after body bytes, so
	// LedgerID must be a declared trailer, not an ordinary header. Mutation
	// self-check: writing the old ordinary header before first byte leaves
	// Result().Header populated and Result().Trailer empty.
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	ledger, err := auditledger.NewMemoryLedger(signer)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	deps := streamingReplayDeps(t, 77711, false, openAIStreamingFixture(), nil)
	deps.AuditLedger = ledger
	deps.Signer = signer

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	result := rec.Result()
	if got := result.Header.Get(headerHUAKAIAuditLedgerID); got != "" {
		t.Fatalf("%s ordinary header=%q want empty for streaming trailer", headerHUAKAIAuditLedgerID, got)
	}
	if got := result.Trailer.Get(headerHUAKAIAuditLedgerID); got == "" {
		t.Fatalf("%s trailer is empty for persisted streaming ledger", headerHUAKAIAuditLedgerID)
	}
	if got := result.Trailer.Get("X-HUAKAI-Ledger-DLQ-Ref"); got != "" {
		t.Fatalf("X-HUAKAI-Ledger-DLQ-Ref trailer=%q want empty for persisted ledger", got)
	}
}

func TestStreamingLedgerCallback_PersistedSetsVerifyAndFingerprintTrailers(t *testing.T) {
	// 删除 Persisted 分支的 Fingerprint 或 Verify 写入 -> 该测试 trailer 断言变红。
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	deps := streamingReplayDeps(t, 77713, false, openAIStreamingFixture(), nil)
	deps.AuditLedger = fixedAppendLedger{
		entry: auditledger.LedgerEntry{
			LedgerID:          "ledger-stream-1",
			PubkeyFingerprint: "fp-stream-1",
			TenantID:          validIdentity().TenantID,
		},
	}
	deps.Signer = signer

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	result := rec.Result()
	if got := result.Trailer.Get(headerHUAKAIAuditLedgerID); got != "ledger-stream-1" {
		t.Fatalf("%s trailer=%q want ledger-stream-1", headerHUAKAIAuditLedgerID, got)
	}
	if got := result.Trailer.Get(headerHUAKAIAuditSigFingerprint); got != "fp-stream-1" {
		t.Fatalf("%s trailer=%q want fp-stream-1", headerHUAKAIAuditSigFingerprint, got)
	}
	verifyHeader := result.Trailer.Get(headerHUAKAIAuditVerify)
	verifyURL, err := url.Parse(verifyHeader)
	if err != nil {
		t.Fatalf("%s trailer=%q parse error: %v", headerHUAKAIAuditVerify, verifyHeader, err)
	}
	if got := verifyURL.Path; got != "/v1/audit/verify" {
		t.Fatalf("%s path=%q want /v1/audit/verify in %q", headerHUAKAIAuditVerify, got, verifyHeader)
	}
	query := verifyURL.Query()
	if got := query.Get("request_id"); got == "" {
		t.Fatalf("%s request_id is empty in %q", headerHUAKAIAuditVerify, verifyHeader)
	}
	if got := query.Get("ledger-id"); got != "ledger-stream-1" {
		t.Fatalf("%s ledger-id=%q want ledger-stream-1 in %q", headerHUAKAIAuditVerify, got, verifyHeader)
	}
	if got, want := query.Get("tenant_scope_ref"), auditledger.TenantScopeRef(validIdentity().TenantID); got != want {
		t.Fatalf("%s tenant_scope_ref=%q want %q in %q", headerHUAKAIAuditVerify, got, want, verifyHeader)
	}

	deferredRec := httptest.NewRecorder()
	declareStreamBillingTrailers(deferredRec.Header())
	deferredRec.WriteHeader(http.StatusOK)
	if _, err := deferredRec.Write([]byte("data: pong\n\n")); err != nil {
		t.Fatalf("write deferred fixture: %v", err)
	}
	writeStreamingLedgerTrailers(deferredRec.Header(), auditledger.AuditLedgerResult{
		State:  auditledger.LedgerResultStateDeferred,
		DLQRef: "dlq:1",
	}, "req-deferred", validIdentity().TenantID)
	deferredResult := deferredRec.Result()
	if got := deferredResult.Trailer.Get(headerHUAKAIAuditLedgerDLQRef); got != "dlq:1" {
		t.Fatalf("%s trailer=%q want dlq:1", headerHUAKAIAuditLedgerDLQRef, got)
	}
	if got := deferredResult.Trailer.Get(headerHUAKAIAuditVerify); got != "" {
		t.Fatalf("%s trailer=%q want empty for Deferred ledger", headerHUAKAIAuditVerify, got)
	}
	if got := deferredResult.Trailer.Get(headerHUAKAIAuditSigFingerprint); got != "" {
		t.Fatalf("%s trailer=%q want empty for Deferred ledger", headerHUAKAIAuditSigFingerprint, got)
	}
}

func TestStreamingDeferredLedgerDLQRefIsTrailer(t *testing.T) {
	// Risk killed: C-13 rev2 requires Deferred streaming results to expose
	// DLQRef in its own trailer, never mixed into LedgerID. Mutation
	// self-check: omitting the Deferred trailer writer leaves DLQRef empty.
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	dlqSink := &recordingGatewayAuditLedgerDLQ{id: 728}
	deps := streamingReplayDeps(t, 77712, false, openAIStreamingFixture(), nil)
	deps.AuditLedger = &failingAppendLedger{appendErr: errors.New("ledger unavailable")}
	deps.AuditLedgerDLQ = dlqSink
	deps.Signer = signer

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(dlqSink.events) != 1 {
		t.Fatalf("DLQ events=%d want 1", len(dlqSink.events))
	}
	result := rec.Result()
	if got := result.Trailer.Get(headerHUAKAIAuditLedgerID); got != "" {
		t.Fatalf("%s trailer=%q want empty for Deferred ledger", headerHUAKAIAuditLedgerID, got)
	}
	if got := result.Trailer.Get("X-HUAKAI-Ledger-DLQ-Ref"); got != "audit_ledger_dlq:728" {
		t.Fatalf("X-HUAKAI-Ledger-DLQ-Ref trailer=%q want audit_ledger_dlq:728", got)
	}
}

func TestStreamingLedgerDuplicateRequestIDProductionDoesNotSettleOrDLQ(t *testing.T) {
	// Risk killed: duplicate request_id is never recoverable by DLQ replay.
	// Mutation self-check: removing the duplicate special-case enqueues DLQ,
	// sends a Deferred callback, and the handler settles this chargeable stream.
	t.Setenv("HUAKAI_RELEASE_MODE", "production")
	signer, err := sign.GenerateKey()
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	settler := &recordingSettler{}
	dlqSink := &recordingGatewayAuditLedgerDLQ{id: 315}
	deps := streamingReplayDeps(t, 77709, false, openAIStreamingFixture(), nil)
	deps.AuditLedger = &failingAppendLedger{appendErr: auditledger.ErrDuplicateRequestID}
	deps.AuditLedgerDLQ = dlqSink
	deps.Signer = signer
	deps.Settler = settler

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want stream response already delivered; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "pong") {
		t.Fatalf("stream body=%s want delivered fixture content", rec.Body.String())
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0 for duplicate request_id", len(settler.calls))
	}
	if len(settler.aborts) != 1 || settler.aborts[0].reason != "audit_ledger_error" {
		t.Fatalf("aborts=%+v want one audit_ledger_error abort", settler.aborts)
	}
	if len(dlqSink.events) != 0 {
		t.Fatalf("DLQ events=%d want 0 for duplicate request_id", len(dlqSink.events))
	}
}

func TestStreamingIdempotencyReplayAbortsZeroChargeGracefulStream(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	body := openAIStreamingRequestBody()
	claimID := int64(77706)
	settler := &recordingSettler{}

	firstDeps := streamingReplayDeps(t, claimID, false, zeroTokenOpenAIStreamingFixture(), replayStore)
	firstDeps.Settler = settler
	first := invokeWithIdempotencyKey(t, firstDeps, body, "stream-idem-zero-charge")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d want 200; body=%s", first.Code, first.Body.String())
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0 for zero-delivery stream", len(settler.calls))
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("abort calls=%d want 1 for zero-delivery stream", len(settler.aborts))
	}
	if got := settler.aborts[0].claimID; got != claimID {
		t.Fatalf("abort claimID=%d want %d", got, claimID)
	}
	if got := settler.aborts[0].reason; got != "stream_no_billable_delivery" {
		t.Fatalf("abort reason=%q want stream_no_billable_delivery", got)
	}
	if _, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID); err != nil {
		t.Fatalf("lookup replay: %v", err)
	} else if ok {
		t.Fatal("zero-delivery stream must not record idempotency replay")
	}

	retryClaimID := claimID + 1
	retrySettler := &recordingSettler{}
	secondDeps := streamingReplayDeps(t, retryClaimID, false, openAIStreamingFixture(), replayStore)
	secondDeps.Settler = retrySettler
	second := invokeWithIdempotencyKey(t, secondDeps, body, "stream-idem-zero-charge")
	if second.Code != http.StatusOK {
		t.Fatalf("retry status=%d want 200; body=%s", second.Code, second.Body.String())
	}
	if got := second.Header().Get("X-HUAKAI-Idempotency-Hit"); got != "" {
		t.Fatalf("retry idempotency-hit header=%q want empty after aborted first claim", got)
	}
	if !strings.Contains(second.Body.String(), "pong") {
		t.Fatalf("retry should dispatch upstream and return fresh stream; body=%s", second.Body.String())
	}
	if len(retrySettler.calls) != 1 {
		t.Fatalf("retry settle calls=%d want 1", len(retrySettler.calls))
	}
	if retrySettler.calls[0].ClaimID != retryClaimID {
		t.Fatalf("retry settle claimID=%d want %d", retrySettler.calls[0].ClaimID, retryClaimID)
	}
	stored, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, retryClaimID)
	if err != nil {
		t.Fatalf("lookup replay: %v", err)
	}
	if !ok {
		t.Fatal("retry chargeable stream must record replay after successful settlement")
	}
	if stored.ContentType != idempotencyReplayContentTypeSSE {
		t.Fatalf("stored ContentType=%q want %q", stored.ContentType, idempotencyReplayContentTypeSSE)
	}
	if string(stored.ResponseBody) != second.Body.String() {
		t.Fatalf("stored retry body mismatch:\nstored=%s\nretry=%s", string(stored.ResponseBody), second.Body.String())
	}
	if first.Body.String() == second.Body.String() {
		t.Fatalf("fixture sanity: zero-delivery first body should differ from fresh chargeable retry\nfirst=%s\nsecond=%s", first.Body.String(), second.Body.String())
	}
}

func TestStreamingCaseCDefaultNoBillAbortsWithZeroObservedInput(t *testing.T) {
	settler := &recordingSettler{}
	deps := inputOnlyInterruptedStreamDeps(t, 77801, 17, billing.NewPolicyResolver(&streamPolicyStore{}, time.Minute))
	deps.Settler = settler

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0 for case C no_bill", len(settler.calls))
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("abort calls=%d want 1 for case C no_bill", len(settler.aborts))
	}
	if got := settler.aborts[0].reason; got != "upstream_5xx" {
		t.Fatalf("abort reason=%q want upstream_5xx", got)
	}
	if got := settler.aborts[0].observedInputTokens; got != 0 {
		t.Fatalf("observed input tokens=%d want 0 for default no_bill", got)
	}
}

func TestStreamingCaseCNoBillRecordAbortsWithObservedInput(t *testing.T) {
	settler := &recordingSettler{}
	deps := inputOnlyInterruptedStreamDeps(t, 77802, 23, streamPolicyResolver(billing.StreamInputOnlyInterruptedPolicyNoBillRecord))
	deps.Settler = settler

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0 for case C no_bill_record", len(settler.calls))
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("abort calls=%d want 1 for case C no_bill_record", len(settler.aborts))
	}
	if got := settler.aborts[0].observedInputTokens; got != 23 {
		t.Fatalf("observed input tokens=%d want 23 for no_bill_record", got)
	}
}

func TestStreamingNoBillRecordTrueZeroDeliveryKeepsZeroObservedInput(t *testing.T) {
	settler := &recordingSettler{}
	deps := streamingReplayDeps(t, 77803, false, zeroTokenOpenAIStreamingFixture(), nil)
	deps.BillingPolicyResolver = streamPolicyResolver(billing.StreamInputOnlyInterruptedPolicyNoBillRecord)
	deps.Settler = settler

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0 for true zero-delivery stream", len(settler.calls))
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("abort calls=%d want 1 for true zero-delivery stream", len(settler.aborts))
	}
	if got := settler.aborts[0].observedInputTokens; got != 0 {
		t.Fatalf("observed input tokens=%d want 0 for true zero-delivery stream", got)
	}
}

func TestStreamingCaseCNilResolverDefaultsNoBill(t *testing.T) {
	settler := &recordingSettler{}
	deps := inputOnlyInterruptedStreamDeps(t, 77804, 31, nil)
	deps.Settler = settler

	rec := invokeHandler(t, deps, openAIStreamingRequestBody())
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("abort calls=%d want 1 for nil resolver case C", len(settler.aborts))
	}
	if got := settler.aborts[0].observedInputTokens; got != 0 {
		t.Fatalf("observed input tokens=%d want 0 for nil resolver", got)
	}
}

func TestAT_GW_002_19_TokenizerFallbackInferredUsage(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	body := openAIStreamingRequestBody()
	claimID := int64(77707)
	settler := &recordingSettler{}

	firstDeps := streamingReplayDeps(t, claimID, false, openAIStreamingEOFNoTerminalFixture(), replayStore)
	firstDeps.Settler = settler
	first := invokeWithIdempotencyKey(t, firstDeps, body, "stream-idem-eof-no-terminal")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d want 200; body=%s", first.Code, first.Body.String())
	}
	if got := first.Header().Get("X-Huakai-Forward-Error"); got != "" {
		t.Fatalf("no-terminal fixture fwdErr=%q want nil; body=%s", got, first.Body.String())
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	draft := settler.calls[0].Draft
	if draft.EndClass != gateway.UpstreamEOFNoTerminal {
		t.Fatalf("EndClass=%q want %q", draft.EndClass, gateway.UpstreamEOFNoTerminal)
	}
	if !draft.PendingReconciliation {
		t.Fatal("EOF without terminal marker must set PendingReconciliation")
	}
	if draft.UsageSource != gateway.UsageSourceInferred {
		t.Fatalf("UsageSource=%q want %q for delivered stream without reported usage", draft.UsageSource, gateway.UsageSourceInferred)
	}
	if draft.ConfidenceScore == nil {
		t.Fatal("ConfidenceScore=nil want audit confidence recorded for inferred stream usage")
	}
	if settler.calls[0].StreamAttempt == nil || !settler.calls[0].StreamAttempt.State.Chargeable() {
		t.Fatalf("fixture must deliver a chargeable partial stream; StreamAttempt=%#v", settler.calls[0].StreamAttempt)
	}
	if len(settler.aborts) != 0 {
		t.Fatalf("abort calls=%d want 0", len(settler.aborts))
	}
	stored, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID)
	if err != nil {
		t.Fatalf("lookup replay: %v", err)
	}
	if !ok {
		t.Fatal("EOF without terminal marker must still record replay after successful forwarding and settlement")
	}
	if stored.ContentType != idempotencyReplayContentTypeSSE {
		t.Fatalf("stored ContentType=%q want %q", stored.ContentType, idempotencyReplayContentTypeSSE)
	}
	if string(stored.ResponseBody) != first.Body.String() {
		t.Fatalf("stored body mismatch:\nstored=%s\nfirst=%s", string(stored.ResponseBody), first.Body.String())
	}

	secondDeps := streamingReplayDeps(t, claimID, true, openAIStreamingFixture(), replayStore)
	second := invokeWithIdempotencyKey(t, secondDeps, body, "stream-idem-eof-no-terminal")
	if second.Code != http.StatusOK {
		t.Fatalf("replay status=%d want 200; body=%s", second.Code, second.Body.String())
	}
	if got := second.Header().Get("X-HUAKAI-Idempotency-Hit"); got != "true" {
		t.Fatalf("replay hit header=%q want true", got)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replay body mismatch:\nfirst=%s\nsecond=%s", first.Body.String(), second.Body.String())
	}
}

func TestAT_GW_002_17_TenantIsolationUnderLoad(t *testing.T) {
	// Risk killed: replay persistence is keyed by tenant_id + claim_id under
	// concurrent streams. Mutation self-check: removing tenant_id from the replay
	// key causes shared claim IDs below to collide and body-marker assertions turn
	// red for at least one tenant.
	const (
		tenants          = 5
		streamsPerTenant = 20
	)
	replayStore := billing.NewMemoryReplayStore()
	body := openAIStreamingRequestBody()

	var wg sync.WaitGroup
	errCh := make(chan error, tenants*streamsPerTenant)
	for tenantIdx := 0; tenantIdx < tenants; tenantIdx++ {
		tenantID := int64(7000 + tenantIdx)
		for streamIdx := 0; streamIdx < streamsPerTenant; streamIdx++ {
			streamIdx := streamIdx
			tenantID := tenantID
			claimID := int64(88000 + streamIdx)
			marker := fmt.Sprintf("tenant_%d_stream_%02d", tenantID, streamIdx)
			deps := streamingReplayDeps(t, claimID, false, strings.Replace(openAIStreamingFixture(), "pong", marker, 1), replayStore)
			deps.Auth = stubAuth{identity: auth.Identity{
				TenantID: tenantID,
				APIKeyID: tenantID + 100,
				UserID:   tenantID + 200,
			}}
			idempotencyKey := fmt.Sprintf("tenant-shared-stream-%02d", streamIdx)
			wg.Add(1)
			go func() {
				defer wg.Done()
				h := NewChatCompletionsHandler(deps)
				req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Idempotency-Key", idempotencyKey)
				rec := httptest.NewRecorder()
				h(rec, req)
				if rec.Code != http.StatusOK {
					errCh <- fmt.Errorf("tenant %d stream %d status=%d body=%s", tenantID, streamIdx, rec.Code, rec.Body.String())
					return
				}
				if !strings.Contains(rec.Body.String(), marker) {
					errCh <- fmt.Errorf("tenant %d stream %d response missing marker %q: %s", tenantID, streamIdx, marker, rec.Body.String())
				}
			}()
		}
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}

	for tenantIdx := 0; tenantIdx < tenants; tenantIdx++ {
		tenantID := int64(7000 + tenantIdx)
		for streamIdx := 0; streamIdx < streamsPerTenant; streamIdx++ {
			claimID := int64(88000 + streamIdx)
			marker := fmt.Sprintf("tenant_%d_stream_%02d", tenantID, streamIdx)
			stored, ok, err := replayStore.Lookup(context.Background(), tenantID, claimID)
			if err != nil {
				t.Fatalf("lookup tenant=%d claim=%d: %v", tenantID, claimID, err)
			}
			if !ok {
				t.Fatalf("missing replay tenant=%d claim=%d", tenantID, claimID)
			}
			body := string(stored.ResponseBody)
			if !strings.Contains(body, marker) {
				t.Fatalf("tenant=%d claim=%d body missing marker %q: %s", tenantID, claimID, marker, body)
			}
			for otherTenantIdx := 0; otherTenantIdx < tenants; otherTenantIdx++ {
				otherTenantID := int64(7000 + otherTenantIdx)
				if otherTenantID == tenantID {
					continue
				}
				otherMarker := fmt.Sprintf("tenant_%d_stream_%02d", otherTenantID, streamIdx)
				if strings.Contains(body, otherMarker) {
					t.Fatalf("tenant=%d claim=%d replay leaked other tenant marker %q: %s", tenantID, claimID, otherMarker, body)
				}
			}
		}
	}
}

func TestStreamingIdempotencyReplaySkipsOverLimit(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	body := openAIStreamingRequestBody()
	claimID := int64(77702)

	firstDeps := streamingReplayDeps(t, claimID, false, oversizedOpenAIStreamingFixture(maxIdempotencyReplayBodyBytes), replayStore)
	first := invokeWithIdempotencyKey(t, firstDeps, body, "stream-idem-over-limit")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d want 200; body len=%d body prefix=%q", first.Code, first.Body.Len(), first.Body.String()[:min(first.Body.Len(), 256)])
	}
	if first.Body.Len() <= maxIdempotencyReplayBodyBytes {
		t.Fatalf("first body len=%d must exceed replay limit %d", first.Body.Len(), maxIdempotencyReplayBodyBytes)
	}
	if _, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID); err != nil {
		t.Fatalf("lookup replay: %v", err)
	} else if ok {
		t.Fatal("over-limit stream response must not be recorded for replay")
	}

	secondDeps := streamingReplayDeps(t, claimID, true, openAIStreamingFixture(), replayStore)
	second := invokeWithIdempotencyKey(t, secondDeps, body, "stream-idem-over-limit")
	if second.Code != http.StatusConflict {
		t.Fatalf("replay miss status=%d want 409; body=%s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "replay_without_cache") {
		t.Fatalf("body=%q want replay_without_cache", second.Body.String())
	}
}

func TestStreamingIdempotencyReplayRecordsForwardErrorPartialStream(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	claimID := int64(77703)
	settler := &recordingSettler{}

	deps := streamingReplayDeps(t, claimID, false, "", replayStore)
	deps.Settler = settler
	scanners := gateway.NewStaticStreamScannerRegistry()
	scanners.MustRegister("openai_chat", scannerThenError{
		event: partialOpenAIStreamingEventBeforeReadError(),
		err:   io.ErrUnexpectedEOF,
	})
	deps.Forwarder.Scanners = scanners
	rec := invokeWithIdempotencyKey(t, deps, openAIStreamingRequestBody(), "stream-idem-forward-error")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Huakai-Forward-Error"); got != "" {
		t.Fatalf("X-Huakai-Forward-Error=%q want empty post-Forward dead header", got)
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1 for partial delivery with forward error", len(settler.calls))
	}
	if settler.calls[0].StreamAttempt == nil || !settler.calls[0].StreamAttempt.State.Chargeable() {
		t.Fatalf("partial delivery with forward error must commit a chargeable attempt; StreamAttempt=%#v", settler.calls[0].StreamAttempt)
	}
	if len(settler.aborts) != 0 {
		t.Fatalf("abort calls=%d want 0", len(settler.aborts))
	}
	stored, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID)
	if err != nil {
		t.Fatalf("lookup replay: %v", err)
	}
	if !ok {
		t.Fatal("forward-error partial stream must be recorded after successful settlement")
	}
	if stored.ContentType != idempotencyReplayContentTypeSSE {
		t.Fatalf("stored ContentType=%q want %q", stored.ContentType, idempotencyReplayContentTypeSSE)
	}
	if string(stored.ResponseBody) != rec.Body.String() {
		t.Fatalf("stored body mismatch:\nstored=%s\nclient=%s", string(stored.ResponseBody), rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "partial") || strings.Contains(rec.Body.String(), "data: [DONE]") {
		t.Fatalf("fixture must deliver only partial SSE bytes; body=%s", rec.Body.String())
	}
}

func TestStreamingIdempotencyReplaySettlesAmbiguousUsageWithDeliveredContent(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	claimID := int64(77710)
	settler := &recordingSettler{}

	deps := streamingReplayDeps(t, claimID, false, "", replayStore)
	deps.Settler = settler
	scanners := gateway.NewStaticStreamScannerRegistry()
	scanners.MustRegister("openai_chat", scannerThenError{
		event: partialOpenAIStreamingEventBeforeReadError(),
		err:   errors.New("unknown stream termination"),
	})
	deps.Forwarder.Scanners = scanners

	rec := invokeWithIdempotencyKey(t, deps, openAIStreamingRequestBody(), "stream-idem-ambiguous-delivered")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Huakai-Forward-Error"); got != "" {
		t.Fatalf("X-Huakai-Forward-Error=%q want empty post-Forward dead header", got)
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1 for delivered ambiguous usage stream", len(settler.calls))
	}
	if len(settler.aborts) != 0 {
		t.Fatalf("abort calls=%d want 0 for delivered ambiguous usage stream", len(settler.aborts))
	}
	draft := settler.calls[0].Draft
	if draft.EndClass != gateway.AmbiguousUsage {
		t.Fatalf("EndClass=%q want %q", draft.EndClass, gateway.AmbiguousUsage)
	}
	if draft.DeliveredTokenCount <= 0 {
		t.Fatalf("DeliveredTokenCount=%d want >0 for delivered ambiguous usage stream", draft.DeliveredTokenCount)
	}
	if settler.calls[0].StreamAttempt == nil {
		t.Fatal("StreamAttempt missing")
	}
	if settler.calls[0].StreamAttempt.State.Chargeable() {
		t.Fatalf("ambiguous usage attempt must stay non-chargeable; StreamAttempt=%#v", settler.calls[0].StreamAttempt)
	}
	if settler.calls[0].StreamAttempt.DeliveredTokenCount <= 0 {
		t.Fatalf("StreamAttempt DeliveredTokenCount=%d want >0", settler.calls[0].StreamAttempt.DeliveredTokenCount)
	}
	stored, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID)
	if err != nil {
		t.Fatalf("lookup replay: %v", err)
	}
	if !ok {
		t.Fatal("delivered ambiguous usage stream must record replay after settlement")
	}
	if stored.ContentType != idempotencyReplayContentTypeSSE {
		t.Fatalf("stored ContentType=%q want %q", stored.ContentType, idempotencyReplayContentTypeSSE)
	}
	if string(stored.ResponseBody) != rec.Body.String() {
		t.Fatalf("stored body mismatch:\nstored=%s\nclient=%s", string(stored.ResponseBody), rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "partial") || strings.Contains(rec.Body.String(), "data: [DONE]") {
		t.Fatalf("fixture must deliver only partial SSE bytes; body=%s", rec.Body.String())
	}
}

func TestStreamingForwardSettleAndAbortErrorsAreLoggedNotHeaders(t *testing.T) {
	t.Run("forward error after delivery", func(t *testing.T) {
		const marker = "SENSITIVE_STREAM_FORWARD_MARKER"
		logs := captureSlogForTest(t)
		deps := streamingReplayDeps(t, 77901, false, "", nil)
		scanners := gateway.NewStaticStreamScannerRegistry()
		scanners.MustRegister("openai_chat", scannerThenError{
			event: partialOpenAIStreamingEventBeforeReadError(),
			err:   errors.New(marker),
		})
		deps.Forwarder.Scanners = scanners

		rec := invokeHandler(t, deps, openAIStreamingRequestBody())
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("X-Huakai-Forward-Error"); got != "" {
			t.Fatalf("X-Huakai-Forward-Error=%q want empty", got)
		}
		assertLogContains(t, logs, "forward_failed", "error_class")
		assertLogOmits(t, logs, marker)
	})

	t.Run("settle error after delivery", func(t *testing.T) {
		const marker = "SENSITIVE_STREAM_SETTLE_MARKER"
		logs := captureSlogForTest(t)
		deps := streamingReplayDeps(t, 77902, false, openAIStreamingFixture(), nil)
		deps.Settler = &failingSettleSettler{err: errors.New(marker)}

		rec := invokeHandler(t, deps, openAIStreamingRequestBody())
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("X-Huakai-Settle-Error"); got != "" {
			t.Fatalf("X-Huakai-Settle-Error=%q want empty", got)
		}
		assertLogContains(t, logs, "settle_failed", "error_class")
		assertLogOmits(t, logs, marker)
	})

	t.Run("abort error after delivery", func(t *testing.T) {
		const marker = "SENSITIVE_STREAM_ABORT_MARKER"
		logs := captureSlogForTest(t)
		deps := streamingReplayDeps(t, 77903, false, zeroTokenOpenAIStreamingFixture(), nil)
		deps.Settler = &failingAbortSettler{err: errors.New(marker)}

		rec := invokeHandler(t, deps, openAIStreamingRequestBody())
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("X-Huakai-Abort-Failed"); got != "" {
			t.Fatalf("X-Huakai-Abort-Failed=%q want empty post-Forward dead header", got)
		}
		assertLogContains(t, logs, "abort_failed", "error_class")
		assertLogOmits(t, logs, marker)
	})
}

func TestStreamingIdempotencyReplayAbortsZeroByteForwardError(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	claimID := int64(77709)
	settler := &recordingSettler{}

	deps := streamingReplayDeps(t, claimID, false, "", replayStore)
	deps.Settler = settler
	scanners := gateway.NewStaticStreamScannerRegistry()
	scanners.MustRegister("openai_chat", scannerImmediateError{err: io.ErrUnexpectedEOF})
	deps.Forwarder.Scanners = scanners

	first := invokeWithIdempotencyKey(t, deps, openAIStreamingRequestBody(), "stream-idem-zero-byte-forward-error")
	if first.Code != http.StatusBadGateway {
		t.Fatalf("first status=%d want 502; body=%s", first.Code, first.Body.String())
	}
	if !strings.Contains(first.Body.String(), "stream_forward_error") {
		t.Fatalf("first body=%s want stream_forward_error", first.Body.String())
	}
	if got := first.Header().Get("X-Huakai-Forward-Error"); got != "" {
		t.Fatalf("X-Huakai-Forward-Error=%q want empty post-Forward dead header", got)
	}
	if len(settler.calls) != 0 {
		t.Fatalf("settle calls=%d want 0 for zero-byte forward error", len(settler.calls))
	}
	if len(settler.aborts) != 1 {
		t.Fatalf("abort calls=%d want 1 for zero-byte forward error", len(settler.aborts))
	}
	if got := settler.aborts[0].claimID; got != claimID {
		t.Fatalf("abort claimID=%d want %d", got, claimID)
	}
	if got := settler.aborts[0].reason; got != "upstream_5xx" {
		t.Fatalf("abort reason=%q want upstream_5xx", got)
	}
	if _, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID); err != nil {
		t.Fatalf("lookup replay: %v", err)
	} else if ok {
		t.Fatal("zero-byte forward error must not record idempotency replay")
	}

	retryClaimID := claimID + 1
	retrySettler := &recordingSettler{}
	secondDeps := streamingReplayDeps(t, retryClaimID, false, openAIStreamingFixture(), replayStore)
	secondDeps.Settler = retrySettler
	second := invokeWithIdempotencyKey(t, secondDeps, openAIStreamingRequestBody(), "stream-idem-zero-byte-forward-error")
	if second.Code != http.StatusOK {
		t.Fatalf("retry status=%d want 200; body=%s", second.Code, second.Body.String())
	}
	if got := second.Header().Get("X-HUAKAI-Idempotency-Hit"); got != "" {
		t.Fatalf("retry idempotency-hit header=%q want empty after aborted first claim", got)
	}
	if !strings.Contains(second.Body.String(), "pong") {
		t.Fatalf("retry should dispatch upstream and return fresh stream; body=%s", second.Body.String())
	}
	if len(retrySettler.calls) != 1 {
		t.Fatalf("retry settle calls=%d want 1", len(retrySettler.calls))
	}
	if retrySettler.calls[0].ClaimID != retryClaimID {
		t.Fatalf("retry settle claimID=%d want %d", retrySettler.calls[0].ClaimID, retryClaimID)
	}
	if _, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, retryClaimID); err != nil {
		t.Fatalf("lookup retry replay: %v", err)
	} else if !ok {
		t.Fatal("retry chargeable stream must record replay after successful settlement")
	}
}

func TestStreamingIdempotencyReplayRecordsAfterClientCancelPostFlush(t *testing.T) {
	replayStore := &ctxSensitiveReplayStore{inner: billing.NewMemoryReplayStore()}
	body := openAIStreamingRequestBody()
	claimID := int64(77708)

	deps := streamingReplayDeps(t, claimID, false, openAIStreamingFixture(), replayStore)
	settler := &ctxSensitiveSettler{}
	deps.Settler = settler
	h := NewChatCompletionsHandler(deps)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "stream-idem-cancel-after-flush")
	rec := &cancelOnDoneFlushRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		cancel:           cancel,
	}

	h(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !rec.canceled {
		t.Fatal("fixture did not cancel request context after final SSE flush")
	}
	if got := rec.Header().Get("X-Huakai-Settle-Error"); got != "" {
		t.Fatalf("settle error=%q; settle context must outlive client cancellation", got)
	}
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	stored, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID)
	if err != nil {
		t.Fatalf("lookup replay: %v", err)
	}
	if !ok {
		t.Fatal("replay must be recorded even when client cancels after flushed SSE body")
	}
	if stored.ContentType != idempotencyReplayContentTypeSSE {
		t.Fatalf("stored ContentType=%q want %q", stored.ContentType, idempotencyReplayContentTypeSSE)
	}
	if string(stored.ResponseBody) != rec.Body.String() {
		t.Fatalf("stored body mismatch:\nstored=%s\nclient=%s", string(stored.ResponseBody), rec.Body.String())
	}
}

func TestStreamingIdempotencyReplaySkipsWithoutKeyOrStore(t *testing.T) {
	body := openAIStreamingRequestBody()

	t.Run("without key", func(t *testing.T) {
		replayStore := billing.NewMemoryReplayStore()
		claimID := int64(77704)
		deps := streamingReplayDeps(t, claimID, false, openAIStreamingFixture(), replayStore)
		rec := invokeHandler(t, deps, body)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
		}
		if _, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID); err != nil {
			t.Fatalf("lookup replay: %v", err)
		} else if ok {
			t.Fatal("stream response without Idempotency-Key must not be recorded")
		}
	})

	t.Run("without store", func(t *testing.T) {
		deps := streamingReplayDeps(t, 77705, false, openAIStreamingFixture(), nil)
		rec := invokeWithIdempotencyKey(t, deps, body, "stream-idem-no-store")
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d want 200; body=%s", rec.Code, rec.Body.String())
		}
	})
}

func streamingReplayDeps(t *testing.T, claimID int64, hit bool, responseBody string, replayStore billing.ReplayStore) ChatHandlerDeps {
	t.Helper()
	deps := clientAdapterDeps(t)
	adapters := provider.NewStaticRegistry()
	adapters.MustRegister("openai_chat", &provideropenai.PassthroughAdapter{})
	deps.Dispatcher = &gateway.UpstreamDispatcher{
		Adapters:         adapters,
		TransportFactory: transport.NewFactory(),
		HTTPClient:       &recordingStreamingDoer{responseBody: responseBody},
	}
	deps.Forwarder = &gateway.StreamForwarder{
		ProtocolAdapters: gateway.BuildDefaultProtocolAdapterRegistry(),
		Scanners:         gateway.BuildDefaultStreamScannerRegistry(),
		Timeouts: gateway.TimeoutConfig{
			FirstTokenTimeout:  500 * time.Millisecond,
			InterEventTimeout:  500 * time.Millisecond,
			TotalStreamTimeout: 5 * time.Second,
			DrainMaxSeconds:    100 * time.Millisecond,
		},
		ScannerBufferCap: 1 << 20,
	}
	deps.ClaimGate = replayClaimGate{claimID: claimID, hit: hit}
	deps.ReplayStore = replayStore
	deps.Settler = &recordingSettler{}
	return deps
}

func inputOnlyInterruptedStreamDeps(t *testing.T, claimID int64, inputTokens int, resolver *billing.PolicyResolver) ChatHandlerDeps {
	t.Helper()
	deps := streamingReplayDeps(t, claimID, false, "", nil)
	deps.BillingPolicyResolver = resolver
	scanners := gateway.NewStaticStreamScannerRegistry()
	scanners.MustRegister("openai_chat", scannerThenError{
		event: inputOnlyOpenAIUsageEvent(inputTokens),
		err:   io.ErrUnexpectedEOF,
	})
	deps.Forwarder.Scanners = scanners
	return deps
}

func streamPolicyResolver(policy billing.StreamInputOnlyInterruptedPolicy) *billing.PolicyResolver {
	return billing.NewPolicyResolver(&streamPolicyStore{
		ok:     true,
		policy: policy,
	}, time.Minute)
}

type streamPolicyStore struct {
	ok     bool
	policy billing.StreamInputOnlyInterruptedPolicy
}

func (s *streamPolicyStore) Get(_ context.Context, tenantID int64, key string) (billing.StoredBillingSetting, bool, error) {
	if !s.ok || key != billing.StreamInputOnlyInterruptedPolicyKey {
		return billing.StoredBillingSetting{}, false, nil
	}
	return billing.StoredBillingSetting{
		ID:        1,
		TenantID:  tenantID,
		Key:       key,
		Value:     s.policy.String(),
		UpdatedAt: time.Now().UTC(),
		UpdatedBy: "test",
	}, true, nil
}

func (s *streamPolicyStore) UpsertStreamInputOnlyInterruptedPolicy(_ context.Context, tenantID int64, policy billing.StreamInputOnlyInterruptedPolicy, updatedBy string) (billing.StoredBillingSetting, error) {
	s.ok = true
	s.policy = policy
	return billing.StoredBillingSetting{
		ID:        1,
		TenantID:  tenantID,
		Key:       billing.StreamInputOnlyInterruptedPolicyKey,
		Value:     policy.String(),
		UpdatedAt: time.Now().UTC(),
		UpdatedBy: updatedBy,
	}, nil
}

func (s *streamPolicyStore) List(_ context.Context, tenantID int64) ([]billing.StoredBillingSetting, error) {
	if !s.ok {
		return nil, nil
	}
	return []billing.StoredBillingSetting{{
		ID:        1,
		TenantID:  tenantID,
		Key:       billing.StreamInputOnlyInterruptedPolicyKey,
		Value:     s.policy.String(),
		UpdatedAt: time.Now().UTC(),
		UpdatedBy: "test",
	}}, nil
}

func openAIStreamingRequestBody() string {
	return `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`
}

func inputOnlyOpenAIUsageEvent(inputTokens int) gateway.SSEEvent {
	tokens := strconv.Itoa(inputTokens)
	return gateway.SSEEvent{Data: []byte(`{"id":"chatcmpl-case-c","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}],"usage":{"prompt_tokens":` + tokens + `,"completion_tokens":0,"total_tokens":` + tokens + `}}`)}
}

func openAIStreamingFixture() string {
	return strings.Join([]string{
		`data: {"id":"chatcmpl-stream","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"pong"},"finish_reason":null}]}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")
}

func zeroTokenOpenAIStreamingFixture() string {
	return strings.Join([]string{
		`data: {"id":"chatcmpl-zero","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl-zero","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")
}

func openAIStreamingEOFNoTerminalFixture() string {
	return strings.Join([]string{
		`data: {"id":"chatcmpl-eof","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"partial"},"finish_reason":null}]}`,
		``,
		``,
	}, "\n")
}

func partialOpenAIStreamingFixtureBeforeReadError() string {
	return strings.Join([]string{
		`data: {"id":"chatcmpl-error","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"partial"},"finish_reason":null}]}`,
		``,
	}, "\n")
}

func partialOpenAIStreamingEventBeforeReadError() gateway.SSEEvent {
	return gateway.SSEEvent{
		Data: []byte(`{"id":"chatcmpl-error","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"partial"},"finish_reason":null}]}`),
	}
}

func oversizedOpenAIStreamingFixture(minBytes int) string {
	var b strings.Builder
	b.WriteString(`data: {"id":"chatcmpl-big","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n")
	content := strings.Repeat("x", 2048)
	for b.Len() <= minBytes {
		b.WriteString(`data: {"id":"chatcmpl-big","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":`)
		b.WriteString(strconv.Quote(content))
		b.WriteString(`},"finish_reason":null}]}` + "\n\n")
	}
	b.WriteString(`data: {"id":"chatcmpl-big","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n")
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

var _ gateway.HTTPDoer = (*recordingStreamingDoer)(nil)
var _ gateway.HTTPDoer = (*erroringStreamingDoer)(nil)

type scannerThenError struct {
	event gateway.SSEEvent
	err   error
}

func (s scannerThenError) Scan(context.Context, io.Reader, int) iter.Seq2[gateway.SSEEvent, error] {
	return func(yield func(gateway.SSEEvent, error) bool) {
		if !yield(s.event, nil) {
			return
		}
		yield(gateway.SSEEvent{}, s.err)
	}
}

type scannerImmediateError struct {
	err error
}

func (s scannerImmediateError) Scan(context.Context, io.Reader, int) iter.Seq2[gateway.SSEEvent, error] {
	return func(yield func(gateway.SSEEvent, error) bool) {
		err := s.err
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		yield(gateway.SSEEvent{}, err)
	}
}

type cancelOnDoneFlushRecorder struct {
	*httptest.ResponseRecorder
	cancel   context.CancelFunc
	canceled bool
}

func (w *cancelOnDoneFlushRecorder) Flush() {
	w.ResponseRecorder.Flush()
	if !w.canceled && strings.Contains(w.Body.String(), "data: [DONE]") {
		w.canceled = true
		w.cancel()
	}
}

type ctxSensitiveReplayStore struct {
	inner *billing.MemoryReplayStore
}

func (s *ctxSensitiveReplayStore) Record(ctx context.Context, tenantID, claimID int64, status int, contentType string, body []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.inner.Record(ctx, tenantID, claimID, status, contentType, body, ttl)
}

func (s *ctxSensitiveReplayStore) Lookup(ctx context.Context, tenantID, claimID int64) (*billing.ReplayRecord, bool, error) {
	return s.inner.Lookup(ctx, tenantID, claimID)
}

func (s *ctxSensitiveReplayStore) DeleteExpired(ctx context.Context) (int64, error) {
	return s.inner.DeleteExpired(ctx)
}

var _ billing.ReplayStore = (*ctxSensitiveReplayStore)(nil)

type ctxSensitiveSettler struct {
	recordingSettler
}

func (s *ctxSensitiveSettler) Settle(ctx context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.recordingSettler.Settle(ctx, req)
}

type fixedAppendLedger struct {
	entry auditledger.LedgerEntry
}

func (l fixedAppendLedger) Append(context.Context, auditledger.PreparedEntry) (auditledger.LedgerEntry, error) {
	return l.entry, nil
}

func (l fixedAppendLedger) GetByRequestID(context.Context, string) (auditledger.LedgerEntry, error) {
	return auditledger.LedgerEntry{}, auditledger.ErrLedgerEntryNotFound
}

func (l fixedAppendLedger) GetByRequestIDAndTenantScope(context.Context, string, string) (auditledger.LedgerEntry, error) {
	return auditledger.LedgerEntry{}, auditledger.ErrLedgerEntryNotFound
}

func (l fixedAppendLedger) LatestMerkleRoot(context.Context) ([32]byte, error) {
	return auditledger.ZeroRoot, nil
}

func (l fixedAppendLedger) Size(context.Context) int {
	return 1
}

type failingSettleSettler struct {
	recordingSettler
	err error
}

func (s *failingSettleSettler) Settle(ctx context.Context, req billing.SettleRequest) (*billing.SettleResult, error) {
	_, _ = s.recordingSettler.Settle(ctx, req)
	return nil, s.err
}

func captureSlogForTest(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() {
		slog.SetDefault(prev)
	})
	return &buf
}

func assertLogContains(t *testing.T, logs *bytes.Buffer, wants ...string) {
	t.Helper()
	got := logs.String()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("log output=%s missing %q", got, want)
		}
	}
}

func assertLogOmits(t *testing.T, logs *bytes.Buffer, forbiddens ...string) {
	t.Helper()
	got := logs.String()
	for _, forbidden := range forbiddens {
		if strings.Contains(got, forbidden) {
			t.Fatalf("log output=%s leaked %q", got, forbidden)
		}
	}
}

// TestInjectStreamingRequestControlsResponseFormatRawPassthrough_OpenAIChat
// 守 P2-B 修复在流式 marshal 路径(injectStreamingRequestControls)。
// 与非流式 hcsf_graph_marshal_test.go 中同名用例同源:Type:"raw" 时 marshal
// 必须 1:1 还原 Schema 整体,不再包 {"type":"raw","schema":...}。
//
// Mutation:把流式 helper 改回原 wrap 逻辑时本用例必红 — body["response_format"]
// 会变成 {"type":"raw","schema":{"type":"json_object"}},.type != "json_object"。
func TestInjectStreamingRequestControlsResponseFormatRawPassthrough_OpenAIChat(t *testing.T) {
	raw := json.RawMessage(`{"type":"json_object"}`)
	env := &proto.HCSF{
		RequestControls: proto.RequestControls{
			ResponseFormat: &proto.ResponseFormat{Type: "raw", Schema: raw},
		},
	}
	out, err := injectStreamingRequestControls([]byte(`{}`), env, "openai_chat")
	if err != nil {
		t.Fatalf("injectStreamingRequestControls err=%v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, out)
	}
	rf, ok := body["response_format"].(map[string]any)
	if !ok {
		t.Fatalf("response_format must be JSON object, got %T: %v", body["response_format"], body["response_format"])
	}
	if rf["type"] != "json_object" {
		t.Fatalf("streaming response_format.type = %v, want inbound 'json_object' (raw-wrap regression)", rf["type"])
	}
	if _, hasSchema := rf["schema"]; hasSchema {
		t.Fatalf("streaming response_format 出现 'schema' key 是 raw-wrap regression: body=%+v", body)
	}
}

// TestInjectStreamingRequestControlsResponseFormatRawPassthrough_OpenAIResponses
// 守 P2-B 修复在 Responses 流式 marshal 路径。
//
// Mutation:同上,改回原 wrap 时 body["text"] 会成
// {"format":{"type":"raw","schema":...}} 而非 inbound 原 text 整体。
func TestInjectStreamingRequestControlsResponseFormatRawPassthrough_OpenAIResponses(t *testing.T) {
	raw := json.RawMessage(`{"format":{"type":"json_schema","json_schema":{"name":"Person","schema":{"type":"object"}}}}`)
	env := &proto.HCSF{
		RequestControls: proto.RequestControls{
			ResponseFormat: &proto.ResponseFormat{Type: "raw", Schema: raw},
		},
	}
	out, err := injectStreamingRequestControls([]byte(`{}`), env, "openai_responses")
	if err != nil {
		t.Fatalf("injectStreamingRequestControls err=%v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, out)
	}
	text, ok := body["text"].(map[string]any)
	if !ok {
		t.Fatalf("text must be JSON object, got %T: %v", body["text"], body["text"])
	}
	format, ok := text["format"].(map[string]any)
	if !ok {
		t.Fatalf("text.format must be object, got %T: %v", text["format"], text["format"])
	}
	if format["type"] != "json_schema" {
		t.Fatalf("streaming text.format.type = %v, want inbound 'json_schema' (raw-wrap regression)", format["type"])
	}
	if _, hasOuter := format["schema"]; hasOuter {
		t.Fatalf("streaming text.format 出现包壳 'schema' 字段是 raw-wrap regression: body=%+v", body)
	}
}

func TestInjectStreamingRequestControlsMergesRequestPassthrough(t *testing.T) {
	max := 12
	env := &proto.HCSF{
		RequestControls: proto.RequestControls{MaxTokens: &max},
		Passthrough: &proto.PassthroughEnvelope{Extra: map[string]json.RawMessage{
			"max_tool_calls":    json.RawMessage(`3`),
			"prompt_cache_key":  json.RawMessage(`"tenant-a:stable-prefix"`),
			"max_output_tokens": json.RawMessage(`999`),
		}},
	}
	out, err := injectStreamingRequestControls([]byte(`{}`), env, "openai_responses")
	if err != nil {
		t.Fatalf("injectStreamingRequestControls err=%v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, out)
	}
	maxToolCalls, ok := body["max_tool_calls"].(float64)
	if !ok || maxToolCalls != 3 {
		t.Fatalf("max_tool_calls passthrough lost: %+v", body)
	}
	if body["prompt_cache_key"] != "tenant-a:stable-prefix" {
		t.Fatalf("prompt_cache_key passthrough lost: %+v", body)
	}
	maxOutputTokens, ok := body["max_output_tokens"].(float64)
	if !ok || maxOutputTokens != 12 {
		t.Fatalf("modeled max_output_tokens should win over passthrough conflict: %+v", body)
	}
}

// TestStreamingProviderRequestBodyNormalizesMarshalShape 守卫流式翻译路径
// 的形态归一:injectStreamingRequestControls 的按形态分支(response_format
// raw 直通的 case "openai_chat" 等)必须收到归一后的形态族,不能收到原始
// 族名。判别点(真实可达路径 = 跨协议翻译,如 gemini 客户端→kimi 上游):
//  1. kimi_chat → max_tokens=33 + 顶层 stream:true(openai_chat 形态);
//  2. kimi_chat + ResponseFormat{Type:"raw"} → body 出现 response_format
//     ——不归一时 family="kimi_chat" 命中不了 case "openai_chat",raw
//     response_format 被静默丢弃。
//  3. 留 fail-closed 的族(openai_codex/cursor_session/gemini_advanced_session)
//     在此报错,不产出 body。
// Mutation:删掉 streamingProviderRequestBody 开头的形态归一行 → 2 必红
// (response_format 缺失);把归一改错成恒等 → 同红。
func TestStreamingProviderRequestBodyNormalizesMarshalShape(t *testing.T) {
	newEnv := func() *proto.HCSF {
		env := proto.NewEmptyEnvelope()
		env.RequestMeta.Model = "m-in"
		env.RequestMeta.UpstreamModel = "m-up"
		env.CapabilityGraph.Nodes = []proto.CapabilityNode{{
			ID:          "n1",
			Kind:        proto.CapabilityText,
			StreamReady: proto.StreamReadyYes,
			Text:        &proto.TextNode{Role: "user", Block: proto.CanonicalContentBlock{Type: "text", Text: "hi"}},
		}}
		max := 33
		env.RequestControls.MaxTokens = &max
		env.RequestControls.ResponseFormat = &proto.ResponseFormat{Type: "raw", Schema: json.RawMessage(`{"type":"json_object"}`)}
		return env
	}

	rawKimi, err := streamingProviderRequestBody(newEnv(), "kimi_chat")
	if err != nil {
		t.Fatalf("kimi_chat streamingProviderRequestBody err=%v(兼容族流式翻译路径回归 501)", err)
	}
	var kimi map[string]any
	if err := json.Unmarshal(rawKimi, &kimi); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rawKimi)
	}
	if got, ok := kimi["max_tokens"].(float64); !ok || got != 33 {
		t.Errorf("kimi_chat 应注入 max_tokens=33(openai_chat 形态),got=%v", kimi["max_tokens"])
	}
	if kimi["stream"] != true {
		t.Errorf("kimi_chat 应注入 stream:true,got=%v", kimi["stream"])
	}
	rf, ok := kimi["response_format"].(map[string]any)
	if !ok || rf["type"] != "json_object" {
		t.Errorf("kimi_chat 的 raw response_format 未直通(形态归一缺失时 case openai_chat 不命中、静默丢弃): %v", kimi["response_format"])
	}

	for _, fam := range []string{"openai_codex", "cursor_session", "gemini_advanced_session"} {
		if _, err := streamingProviderRequestBody(newEnv(), fam); err == nil {
			t.Errorf("family %q 应在 marshal 处 fail-closed(待 OCAW 确认形态),却产出了 body", fam)
		}
	}
}

// TestStreamingProviderRequestBodyDifyChat 抓的回归:dify_chat 流式翻译 body
// 被 openai 形处理污染——Dify 的流式语义只在 body 内 response_mode 字段,
// (1) forceStreamingRequest 注顶层 stream:true、(2) injectStreamingRequestControls
// 注 max_tokens 等 openai 形 controls,任一发生都是协议污染;且被丢弃的
// MaxTokens 控制必须在 marshal 内记 loss 而非静默蒸发。
// Mutation:从 streamingProviderRequestBody 的跳过分支删掉 dify_chat → 顶层
// stream 断言红;从 injectStreamingRequestControls 删掉 dify_chat 早退 →
// max_tokens 断言红。
func TestStreamingProviderRequestBodyDifyChat(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	env.RequestMeta.Model = "dify-app"
	env.RequestMeta.RequestID = "req_dify_stream"
	env.StreamPlan.Mode = proto.StreamModeStreaming
	env.CapabilityGraph.Nodes = []proto.CapabilityNode{{
		ID:          "n1",
		Kind:        proto.CapabilityText,
		StreamReady: proto.StreamReadyYes,
		Text:        &proto.TextNode{Role: "user", Block: proto.CanonicalContentBlock{Type: "text", Text: "hi"}},
	}}
	max := 64
	env.RequestControls.MaxTokens = &max

	raw, err := streamingProviderRequestBody(env, "dify_chat")
	if err != nil {
		t.Fatalf("streamingProviderRequestBody(dify_chat): %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, raw)
	}
	if body["response_mode"] != "streaming" {
		t.Errorf("response_mode=%v want streaming(流式语义须由 marshal 在 body 内表达)", body["response_mode"])
	}
	if _, ok := body["stream"]; ok {
		t.Errorf("dify body 不得含顶层 stream 字段(forceStreamingRequest 须跳过): %v", body)
	}
	if _, ok := body["max_tokens"]; ok {
		t.Errorf("dify body 不得含 max_tokens(openai 形 controls 注入须跳过): %v", body)
	}
	var maxTokensLoss bool
	for _, loss := range env.CapabilityGraph.ProtocolLoss {
		if loss.Field == "max_tokens" && loss.Code == "unsupported_request_control" {
			maxTokensLoss = true
		}
	}
	if !maxTokensLoss {
		t.Errorf("MaxTokens 被丢弃但 marshal 未记 loss: %+v", env.CapabilityGraph.ProtocolLoss)
	}
}

// TestStreamingProviderRequestBodyOllamaNative 抓的回归:ollama_native 流式
// 翻译 body 被 openai 形处理污染——Ollama 的采样控制只认 options{} 嵌套
// (num_predict),injectStreamingRequestControls 注顶层 max_tokens 即协议污染;
// stream 字段由 marshal 按 StreamPlan 显式写,真相源必须唯一(forceStreaming
// 跳过为单源纪律,其 true 写入与 marshal 幂等,判别断言落在 controls 注入)。
// Mutation:从 injectStreamingRequestControls 删掉 ollama_native 早退 →
// 顶层 max_tokens 断言红。
func TestStreamingProviderRequestBodyOllamaNative(t *testing.T) {
	env := proto.NewEmptyEnvelope()
	env.RequestMeta.Model = "llama3.2"
	env.RequestMeta.RequestID = "req_ollama_stream"
	env.StreamPlan.Mode = proto.StreamModeStreaming
	env.CapabilityGraph.Nodes = []proto.CapabilityNode{{
		ID:          "n1",
		Kind:        proto.CapabilityText,
		StreamReady: proto.StreamReadyYes,
		Text:        &proto.TextNode{Role: "user", Block: proto.CanonicalContentBlock{Type: "text", Text: "hi"}},
	}}
	max := 64
	env.RequestControls.MaxTokens = &max

	raw, err := streamingProviderRequestBody(env, "ollama_native")
	if err != nil {
		t.Fatalf("streamingProviderRequestBody(ollama_native): %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, raw)
	}
	if body["stream"] != true {
		t.Errorf("stream=%v want true(流式语义由 marshal 在 body 内显式表达)", body["stream"])
	}
	if _, ok := body["max_tokens"]; ok {
		t.Errorf("ollama body 不得含顶层 max_tokens(openai 形 controls 注入须跳过): %v", body)
	}
	options, ok := body["options"].(map[string]any)
	if !ok || options["num_predict"] != float64(64) {
		t.Errorf("options.num_predict=%v want 64(MaxTokens 可表达,必须嵌 options)", body["options"])
	}
	// MaxTokens 已投影进 options,不应同时记 unsupported loss(与 dify 相反)。
	for _, loss := range env.CapabilityGraph.ProtocolLoss {
		if loss.Field == "max_tokens" {
			t.Errorf("可表达的 MaxTokens 不应记 loss: %+v", loss)
		}
	}
}

// TestNeedsStreamingHCSFTranslation_CompatFamiliesRawPassthrough 守卫流式
// 翻译门(renew-156 族集不对称第 5 处变体):上游族的 wire 形态与客户端协议
// 同形时(kimi/qwen/... == openai_chat;openai_codex == openai_responses)
// 必须走 raw 直通——既保留 vendor 专有字段(top_k 等,流式无 raw-merge,
// 走 HCSF 翻译会被静默丢),也是此前全部兼容族流式 501 的根因(返回 true 后
// MarshalToProviderRequest 不认这些族)。真跨协议(anthropic→kimi、
// openai→anthropic)仍须翻译。
// Mutation:删掉 needsStreamingHCSFTranslation 的同形态 fast-path → 兼容族
// 用例红;把 fast-path 错写成无条件 false → 跨协议用例红。
func TestNeedsStreamingHCSFTranslation_CompatFamiliesRawPassthrough(t *testing.T) {
	cases := []struct {
		name string
		cp   proto.ClientProtocol
		fam  string
		want bool
	}{
		{"openai→kimi 同形态直通", proto.ClientProtocolOpenAIChat, "kimi_chat", false},
		{"openai→qwen 同形态直通", proto.ClientProtocolOpenAIChat, "qwen_chat", false},
		{"openai→cohere 同形态直通", proto.ClientProtocolOpenAIChat, "cohere_chat", false},
		{"openai→ollama 同形态直通", proto.ClientProtocolOpenAIChat, "ollama_chat", false},
		{"openai→grok 同形态直通", proto.ClientProtocolOpenAIChat, "grok_chat", false},
		{"openai→deepseek 同形态直通", proto.ClientProtocolOpenAIChat, "deepseek_chat", false},
		{"openai→copilot_session JSON形 session 直通", proto.ClientProtocolOpenAIChat, "copilot_session", false},
		// cursor(Connect/proto 帧)/codex(形态仓内互斥)不在映射表 →
		// 仍走翻译路径,在 marshal 处 fail-closed 501(见
		// hcsfProviderRequestModelFamily 排除注释;OCAW 采集后再接)。
		{"openai→cursor_session 留 fail-closed", proto.ClientProtocolOpenAIChat, "cursor_session", true},
		{"responses→codex 留 fail-closed", proto.ClientProtocolOpenAIResponses, "openai_codex", true},
		{"openai→openai 既有直通不回归", proto.ClientProtocolOpenAIChat, "openai_chat", false},
		{"openai→anthropic 跨协议须翻译", proto.ClientProtocolOpenAIChat, "anthropic_messages", true},
		{"anthropic→kimi 跨协议须翻译", proto.ClientProtocolAnthropicMessages, "kimi_chat", true},
		{"openai→gemini_advanced 留 fail-closed", proto.ClientProtocolOpenAIChat, "gemini_advanced_session", true},
		// ClientProtocolGemini=="gemini" ≠ 族名 "gemini_messages":gemini 客户端
		// 永不进 fast-path,同形态对也走翻译(保守现状)。钉住这一不对称——
		// 若有人把枚举值改成 "gemini_messages",此行翻转,必须显式 review。
		{"gemini→gemini_messages 仍走翻译(枚举值≠族名)", proto.ClientProtocolGemini, "gemini_messages", true},
		{"anthropic→bedrock 适配器内翻译不走 HCSF", proto.ClientProtocolAnthropicMessages, "bedrock_invoke", false},
		{"无 client protocol 直通", proto.ClientProtocol(""), "kimi_chat", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ex := &chatExecution{
				clientProtocol: tc.cp,
				resolved:       registry.Resolved{ProtocolFamily: tc.fam},
			}
			if got := ex.needsStreamingHCSFTranslation(); got != tc.want {
				t.Errorf("needsStreamingHCSFTranslation(cp=%q fam=%q) = %v, want %v", tc.cp, tc.fam, got, tc.want)
			}
		})
	}
}
