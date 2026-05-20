package gatewayhttp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/auth"
	"github.com/BloomingProsperity/HUAKAI/internal/billing"
	"github.com/BloomingProsperity/HUAKAI/internal/gateway"
	"github.com/BloomingProsperity/HUAKAI/internal/proto"
	"github.com/BloomingProsperity/HUAKAI/internal/provider"
	"github.com/BloomingProsperity/HUAKAI/internal/provider/anthropic"
	provideropenai "github.com/BloomingProsperity/HUAKAI/internal/provider/openai"
	"github.com/BloomingProsperity/HUAKAI/internal/registry"
	"github.com/BloomingProsperity/HUAKAI/internal/router"
	"github.com/BloomingProsperity/HUAKAI/internal/transport"
)

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

func TestStreamingIdempotencyReplayRecordsSSEAndReplays(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	body := openAIStreamingRequestBody()
	claimID := int64(77701)

	firstDeps := streamingReplayDeps(t, claimID, false, openAIStreamingFixture(), replayStore)
	first := invokeWithIdempotencyKey(t, firstDeps, body, "stream-idem-main")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d want 200; body=%s", first.Code, first.Body.String())
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

func TestStreamingIdempotencyReplayRecordsZeroChargeGracefulStream(t *testing.T) {
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
	if len(settler.calls) != 1 {
		t.Fatalf("settle calls=%d want 1", len(settler.calls))
	}
	if settler.calls[0].StreamAttempt == nil {
		t.Fatal("stream settle request missing StreamAttempt")
	}
	if settler.calls[0].StreamAttempt.State.Chargeable() {
		t.Fatalf("stream state=%s must be zero-charge/non-chargeable", settler.calls[0].StreamAttempt.State)
	}
	stored, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID)
	if err != nil {
		t.Fatalf("lookup replay: %v", err)
	}
	if !ok {
		t.Fatal("zero-charge graceful stream must still record replay after successful settlement")
	}
	if stored.ContentType != idempotencyReplayContentTypeSSE {
		t.Fatalf("stored ContentType=%q want %q", stored.ContentType, idempotencyReplayContentTypeSSE)
	}
	if string(stored.ResponseBody) != first.Body.String() {
		t.Fatalf("stored body mismatch:\nstored=%s\nfirst=%s", string(stored.ResponseBody), first.Body.String())
	}

	secondDeps := streamingReplayDeps(t, claimID, true, "data: should-not-dispatch\n\n", replayStore)
	secondDeps.Dispatcher = &gateway.UpstreamDispatcher{}
	second := invokeWithIdempotencyKey(t, secondDeps, body, "stream-idem-zero-charge")
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

func TestStreamingIdempotencyReplayRecordsEOFNoTerminalWhenForwardAndSettleSucceed(t *testing.T) {
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
	if settler.calls[0].StreamAttempt == nil || !settler.calls[0].StreamAttempt.State.Chargeable() {
		t.Fatalf("fixture must deliver a chargeable partial stream; StreamAttempt=%#v", settler.calls[0].StreamAttempt)
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

func TestStreamingIdempotencyReplaySkipsForwardError(t *testing.T) {
	replayStore := billing.NewMemoryReplayStore()
	claimID := int64(77703)

	deps := streamingReplayDeps(t, claimID, false, "", replayStore)
	deps.Dispatcher.HTTPClient = &erroringStreamingDoer{responsePrefix: partialOpenAIStreamingFixtureBeforeReadError()}
	rec := invokeWithIdempotencyKey(t, deps, openAIStreamingRequestBody(), "stream-idem-forward-error")
	if got := rec.Header().Get("X-Huakai-Forward-Error"); got == "" {
		t.Fatalf("X-Huakai-Forward-Error header empty; fixture must trigger fwdErr != nil, body=%s", rec.Body.String())
	}
	if _, ok, err := replayStore.Lookup(context.Background(), validIdentity().TenantID, claimID); err != nil {
		t.Fatalf("lookup replay: %v", err)
	} else if ok {
		t.Fatal("forward error stream response must not be recorded for replay")
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

func openAIStreamingRequestBody() string {
	return `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`
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
